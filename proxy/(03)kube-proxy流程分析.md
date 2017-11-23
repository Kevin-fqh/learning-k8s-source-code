# kube-proxy流程分析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [简介](#简介)
  - [入口函数](#入口函数)	
  - [NewProxyServerDefault](#newproxyserverdefault)
    - [RegisterHandler](#registerhandler)
  - [ProxyServer的Run()](#proxyserver的run)
    - [SyncLoop()](#syncloop)
	- [syncProxyRules()](#syncproxyrules)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 版本说明
v1.3.6

## 简介
kube-proxy的核心是从kube-apiserver同步service和endpoint信息，然后更新到iptables。

每个service都有一个cluster ip，service依靠selector label，对应后台的Pod，这个工作主要有kube-controller-manager的endpoint-controller完成。 这个endpoint-controller会生成与service相对应的endpoint。

kube-proxy运行于每一个node节点，它的主要工作就是对service、endpoint进行watch，然后在每一个节点建立相关的iptables表项，这样我们就可以在任意一个node上访问service，同时，假如一个service包含多个endpoint，它还起着负载均衡的作用，流量将平均分配到一个endpoint。

Service的重要作用是，一个服务后端的Pods可能会随着生存灭亡而发生IP的改变，Service的出现，给服务提供了一个固定的IP，而无视后端Endpoint的变化。

一个Service的ServiceType决定了其发布服务的方式：
1. ClusterIP：这是k8s默认的ServiceType。通过集群内的ClusterIP在内部发布服务。
2. NodePort：这种方式是常用的，用来对集群外暴露Service，可以通过访问集群内的每个 NodeIP:NodePort 的方式，访问到对应Service后端的Endpoint。
3. LoadBalancer: 这也是用来对集群外暴露服务的，需要Cloud Provider的支持。
4. ExternalName：这个也是在集群内发布服务用的，需要借助KubeDNS(version >= 1.7)的支持，就是用KubeDNS将该service和ExternalName做一个Map，KubeDNS返回一个CNAME记录。

ClusterIP工作原理：一个service对外暴露一个Virtual IP，即Cluster IP, 集群内通过访问这个`Cluster IP:Port`就能访问到集群内对应的serivce下的Pod。

NodePort的工作原理：发送到某个`NodeIP:NodePort`的请求，通过iptables重定向到kube-proxy对应的端口(Node上的随机端口)上，然后由kube-proxy再将请求发送到其中的一个`Pod:TargetPort`。

## 入口函数
流程分析：
1. 首先options.NewProxyConfig()生成config
2. 然后创建服务app.NewProxyServerDefault(config)，
3. 最后s.Run()
```go
func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	config := options.NewProxyConfig()
	config.AddFlags(pflag.CommandLine)

	flag.InitFlags()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()
	/*
		kube-proxy的主要代码就位于创建服务，解析app.NewProxyServerDefault(config)这个函数。
		定义在cmd/kube-proxy/app/server.go中
	*/
	s, err := app.NewProxyServerDefault(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if err = s.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
```

## NewProxyServerDefault
kube-proxy有两种运行模式，一种是基于用户态proxy，另一种是基于内核态的iptables。 

用户态模式的弊端：service的请求会先从用户空间进入内核iptables，然后再回到用户空间，由kube-proxy完成后端Endpoints的选择和代理工作，这样流量从用户空间进出内核带来的性能损耗过大。

基于iptables，效率更高，也是现在k8s默认的工作模式，当然效率高也是相对而言。

func NewProxyServerDefault用于构造一个ProxyServer对象，其流程如下：
1. 创建iptables的接口 iptInterface
2. 判断proxyMode：proxyModeIptables，proxyModeUserspace
3. NewProxier()创建proxier
4. 生成serviceConfig、endpointsConfig，负责watch apiserver
5. serviceConfig.RegisterHandler(proxier)，添加对应的listener用来处理service update时逻辑。
6. return NewProxyServer()

```go
// NewProxyServerDefault creates a new ProxyServer object with default parameters.
func NewProxyServerDefault(config *options.ProxyServerConfig) (*ProxyServer, error) {
	if c, err := configz.New("componentconfig"); err == nil {
		c.Set(config.KubeProxyConfiguration)
	} else {
		glog.Errorf("unable to register configz: %s", err)
	}
	protocol := utiliptables.ProtocolIpv4
	if net.ParseIP(config.BindAddress).To4() == nil {
		protocol = utiliptables.ProtocolIpv6
	}

	// Create a iptables utils.
	/*
		创建iptables的接口
	*/
	execer := exec.New()
	dbus := utildbus.New()
	iptInterface := utiliptables.New(execer, dbus, protocol)

	// We omit creation of pretty much everything if we run in cleanup mode
	if config.CleanupAndExit {
		return &ProxyServer{
			Config:       config,
			IptInterface: iptInterface,
		}, nil
	}

	// TODO(vmarmol): Use container config for this.
	var oomAdjuster *oom.OOMAdjuster
	if config.OOMScoreAdj != nil {
		oomAdjuster = oom.NewOOMAdjuster()
		if err := oomAdjuster.ApplyOOMScoreAdj(0, int(*config.OOMScoreAdj)); err != nil {
			glog.V(2).Info(err)
		}
	}

	if config.ResourceContainer != "" {
		// Run in its own container.
		if err := util.RunInResourceContainer(config.ResourceContainer); err != nil {
			glog.Warningf("Failed to start in resource-only container %q: %v", config.ResourceContainer, err)
		} else {
			glog.V(2).Infof("Running in resource-only container %q", config.ResourceContainer)
		}
	}

	// Create a Kube Client
	// define api config source
	if config.Kubeconfig == "" && config.Master == "" {
		glog.Warningf("Neither --kubeconfig nor --master was specified.  Using default API client.  This might not work.")
	}
	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if non-empty.
	/*
		译：这将创建一个Kube Client，首先加载任何指定的kubeconfig文件，
		   然后overriding the Master flag（如果非空）。
	*/
	kubeconfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: config.Master}}).ClientConfig()
	if err != nil {
		return nil, err
	}

	kubeconfig.ContentType = config.ContentType
	// Override kubeconfig qps/burst settings from flags
	kubeconfig.QPS = config.KubeAPIQPS
	kubeconfig.Burst = int(config.KubeAPIBurst)

	client, err := kubeclient.New(kubeconfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	// Create event recorder
	hostname := nodeutil.GetHostname(config.HostnameOverride)
	// 创建event Broadcaster和event recorder
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(api.EventSource{Component: "kube-proxy", Host: hostname})

	//定义proxier和endpointsHandler，分别用于处理services和endpoints的update event。
	var proxier proxy.ProxyProvider
	var endpointsHandler proxyconfig.EndpointsConfigHandler
	//从config中获取proxy mode，现在默认是iptables
	proxyMode := getProxyMode(string(config.Mode), client.Nodes(), hostname, iptInterface, iptables.LinuxKernelCompatTester{})
	/*
		kube-proxy有两种运行模式，一种是基于用户态proxy，
		另一种是基于内核态的iptables。
		基于iptables，效率更高。

		主要讨论基于iptables的工作模式
	*/
	if proxyMode == proxyModeIptables {
		glog.V(0).Info("Using iptables Proxier.")
		if config.IPTablesMasqueradeBit == nil {
			// IPTablesMasqueradeBit must be specified or defaulted.
			return nil, fmt.Errorf("Unable to read IPTablesMasqueradeBit from config")
		}
		/*
			调用pkg/proxy/iptables/proxier.go中的iptables.NewProxier来创建proxier，
			赋值给前面定义的proxier和endpointsHandler，
			表示由该proxier同时负责service和endpoint的event处理。
		*/
		proxierIptables, err := iptables.NewProxier(iptInterface, execer, config.IPTablesSyncPeriod.Duration, config.MasqueradeAll, int(*config.IPTablesMasqueradeBit), config.ClusterCIDR, hostname, getNodeIP(client, hostname))
		if err != nil {
			glog.Fatalf("Unable to create proxier: %v", err)
		}
		/*
			iptables Proxier
			IF_ELSE模块内部主要构建了两个对象＊＊＊＊，
			一个是proxier，另一个是endpointsHandler，
			它们都是proxierIptables。
		*/
		proxier = proxierIptables
		endpointsHandler = proxierIptables
		// No turning back. Remove artifacts that might still exist from the userspace Proxier.
		/*
			删除用户态Proxier中可能的残留规则。
		*/
		glog.V(0).Info("Tearing down userspace rules.")
		//关闭用户空间规则
		userspace.CleanupLeftovers(iptInterface)
	} else {
		glog.V(0).Info("Using userspace Proxier.")
		// This is a proxy.LoadBalancer which NewProxier needs but has methods we don't need for
		// our config.EndpointsConfigHandler.
		loadBalancer := userspace.NewLoadBalancerRR()
		// set EndpointsConfigHandler to our loadBalancer
		endpointsHandler = loadBalancer

		proxierUserspace, err := userspace.NewProxier(
			loadBalancer,
			net.ParseIP(config.BindAddress),
			iptInterface,
			*utilnet.ParsePortRangeOrDie(config.PortRange),
			config.IPTablesSyncPeriod.Duration,
			config.UDPIdleTimeout.Duration,
		)
		if err != nil {
			glog.Fatalf("Unable to create proxier: %v", err)
		}
		proxier = proxierUserspace
		// Remove artifacts from the pure-iptables Proxier.
		glog.V(0).Info("Tearing down pure-iptables proxy rules.")
		iptables.CleanupLeftovers(iptInterface)
	}
	iptInterface.AddReloadFunc(proxier.Sync)

	// Create configs (i.e. Watches for Services and Endpoints)
	// Note: RegisterHandler() calls need to happen before creation of Sources because sources
	// only notify on changes, and the initial update (on process start) may be lost if no handlers
	// are registered yet.

	/*
		serviceconfig创建
		创建serviceConfig负责service的watchforUpdates

		NewServiceConfig()定义在/pkg/proxy/config/config.go中
			---->func NewServiceConfig() *ServiceConfig

	*/
	serviceConfig := proxyconfig.NewServiceConfig()
	/*
		给serviceConfig注册proxier，既添加对应的listener用来处理service update时逻辑。

		serviceConfig.RegisterHandler(proxier)其实就是注册了一个listener，
		到收到通知时，它会执行handler.OnServiceUpdate(instance.([]api.Service))函数。

		RegisterHandler函数定义在/pkg/proxy/config/config.go中
			---->func (c *ServiceConfig) RegisterHandler(handler ServiceConfigHandler)
	*/
	serviceConfig.RegisterHandler(proxier)

	/*
		endpointsConfig的创建
		逻辑和serviceConfig的创建完全一样，只是将service换成了endpoint。
	*/
	endpointsConfig := proxyconfig.NewEndpointsConfig()
	endpointsConfig.RegisterHandler(endpointsHandler)

	/*
		从上面看到serviceconfig和endpointsConfig已经创建完成

		service、endpoint肯定来源于apiserver，kube-proxy如何获取呢？
		获取的关键代码是proxyconfig.NewSourceAPI这个函数。
		我们看看这个函数传入的几个参数，
		主要看后两个，serviceConfig.Channel("api")和endpointsConfig.Channel("api")，
		这里以service为例：

		serviceConfig.Channel("api")定义的在/pkg/proxy/config/config.go中的
			--->func (c *ServiceConfig) Channel(source string) chan ServiceUpdate
		会得到几个返回值作为NewSourceAPI函数的参数

		NewSourceAPI函数定义在 /pkg/proxy/config/api.go中的
			---->func NewSourceAPI(c cache.Getter, period time.Duration, servicesChan chan<- ServiceUpdate, endpointsChan chan<- EndpointsUpdate)

		NewSourceAPI通过ListWatch apiserver的Service和endpoint，并周期性的维护serviceStore和endpointStore的更新
	*/
	proxyconfig.NewSourceAPI(
		client,
		config.ConfigSyncPeriod,
		/*
			从这里看出kube-proxy的数据来源只有kube-apiserver一个
			但为什么还要用这个mux框架来管理chanel，而不是直接一个channel完事？？
		*/
		serviceConfig.Channel("api"),
		endpointsConfig.Channel("api"),
	)

	config.NodeRef = &api.ObjectReference{
		Kind:      "Node",
		Name:      hostname,
		UID:       types.UID(hostname),
		Namespace: "",
	}

	conntracker := realConntracker{}

	//把前面创建的对象作为参数，构造出ProxyServer对象。
	return NewProxyServer(client, config, iptInterface, proxier, eventBroadcaster, recorder, conntracker, proxyMode)
}
```
从`serviceConfig.Channel("api"),`可以看到proxy和kubelet一样，也是应用了MUX框架来管理channel的。 只不过不同于kubelet有三个数据来源，kube-proxy的数据来源只有kube-apiserver一个。

关于其中的List-watch过程和其它组件都是差不多的。

### RegisterHandler
下面来看看service更新时触发的处理逻辑，见 /kubernetes-1.3.6/pkg/proxy/config/config.go
```go
//serviceConfig.RegisterHandler正是负责给Broadcaster注册listener的
func (c *ServiceConfig) RegisterHandler(handler ServiceConfigHandler) {
	/*
		serviceConfig.RegisterHandler(proxier)其实就是注册了一个listener，
		到收到通知时，它会执行handler.OnServiceUpdate(instance.([]api.Service))函数。

		OnServiceUpdate函数定义在/pkg/proxy/iptables/proxier.go（或者/pkg/proxy/userspace/proxier.go）中的
			--->func (proxier *Proxier) OnServiceUpdate(allServices []api.Service)
	*/
	/*
		思考：＊＊＊＊＊＊＊
			从下面的func watchForUpdates (...)可以看出
			只有updates可读时，才会触发bcaster.Notify，进而促发handler.OnServiceUpdate(instance.([]api.Service)),
			那么谁会往updates这个channel里面写入东西呢？？？？

	*/
	c.bcaster.Add(config.ListenerFunc(func(instance interface{}) {
		glog.V(3).Infof("Calling handler.OnServiceUpdate()")
		handler.OnServiceUpdate(instance.([]api.Service))
	}))
}
```

- OnServiceUpdate()
```go
func (proxier *Proxier) OnServiceUpdate(allServices []api.Service) {
	...
	...
	proxier.syncProxyRules()
	proxier.deleteServiceConnections(staleUDPServices.List())
}
```
函数OnServiceUpdate()的核心是根据获取的service信息，构建proxier.serviceMap，然后调用proxier.syncProxyRules()去同步iptables信息。一旦service、endpoint有变化，相应的iptables规则就会得到更新。

## ProxyServer的Run()
Run()的作用主要是每隔proxier.syncPeriod，通过执行s.Proxier.SyncLoop()，会调用一次proxier.Sync()，进而调用proxier.syncProxyRules()。 proxier.syncPeriod的默认值为30秒
```go
// Run runs the specified ProxyServer.  This should never exit (unless CleanupAndExit is set).
func (s *ProxyServer) Run() error {
	// remove iptables rules and exit
	if s.Config.CleanupAndExit {
		encounteredError := userspace.CleanupLeftovers(s.IptInterface)
		encounteredError = iptables.CleanupLeftovers(s.IptInterface) || encounteredError
		if encounteredError {
			return errors.New("Encountered an error while tearing down rules.")
		}
		return nil
	}

	s.Broadcaster.StartRecordingToSink(s.Client.Events(""))

	// Start up a webserver if requested
	if s.Config.HealthzPort > 0 {
		http.HandleFunc("/proxyMode", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s", s.ProxyMode)
		})
		configz.InstallHandler(http.DefaultServeMux)
		go wait.Until(func() {
			err := http.ListenAndServe(s.Config.HealthzBindAddress+":"+strconv.Itoa(int(s.Config.HealthzPort)), nil)
			if err != nil {
				glog.Errorf("Starting health server failed: %v", err)
			}
		}, 5*time.Second, wait.NeverStop)
	}

	// Tune conntrack, if requested
	if s.Conntracker != nil {
		max, err := getConntrackMax(s.Config)
		if err != nil {
			return err
		}
		if max > 0 {
			err := s.Conntracker.SetMax(max)
			if err != nil {
				if err != readOnlySysFSError {
					return err
				}
				// readOnlySysFSError is caused by a known docker issue (https://github.com/docker/docker/issues/24000),
				// the only remediation we know is to restart the docker daemon.
				// Here we'll send an node event with specific reason and message, the
				// administrator should decide whether and how to handle this issue,
				// whether to drain the node and restart docker.
				// TODO(random-liu): Remove this when the docker bug is fixed.
				const message = "DOCKER RESTART NEEDED (docker issue #24000): /sys is read-only: can't raise conntrack limits, problems may arise later."
				s.Recorder.Eventf(s.Config.NodeRef, api.EventTypeWarning, err.Error(), message)
			}
		}
		if s.Config.ConntrackTCPEstablishedTimeout.Duration > 0 {
			if err := s.Conntracker.SetTCPEstablishedTimeout(int(s.Config.ConntrackTCPEstablishedTimeout.Duration / time.Second)); err != nil {
				return err
			}
		}
	}

	// Birth Cry after the birth is successful
	s.birthCry()

	// Just loop forever for now...
	/*
		Run()的作用主要是每隔proxier.syncPeriod，通过执行s.Proxier.SyncLoop()，
		会调用一次proxier.Sync()，
		进而调用proxier.syncProxyRules()。
		proxier.syncPeriod的默认值为30秒

		SyncLoop()定义在/pkg/proxy/iptables/proxier.go中（或者/pkg/proxy/userspace/proxier.go）
			---->func (proxier *Proxier) SyncLoop()
	*/
	s.Proxier.SyncLoop()
	return nil
}
```

### SyncLoop()
```go
// SyncLoop runs periodic work.  This is expected to run as a goroutine or as the main loop of the app.  It does not return.
func (proxier *Proxier) SyncLoop() {
	t := time.NewTicker(proxier.syncPeriod)
	defer t.Stop()
	for {
		<-t.C
		glog.V(6).Infof("Periodic sync")
		//调用Sync()
		proxier.Sync()
	}
}

// Sync is called to immediately synchronize the proxier state to iptables
func (proxier *Proxier) Sync() {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	//调用syncProxyRules()来更新iptables
	proxier.syncProxyRules()
}
```

### syncProxyRules()
syncProxyRules()负责将proxy中缓存的service/endpoint同步更新到iptables中生成对应Chain和NAT Rules。
关于其具体分析见[iptables]()一文

## 参考
[kube-proxy](http://licyhust.com/容器技术/2016/11/05/kube-proxy/)
[kube-proxy工作原理](http://blog.csdn.net/waltonwang/article/details/55236300)
