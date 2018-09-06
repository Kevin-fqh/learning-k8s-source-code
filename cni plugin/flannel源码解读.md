# Flannel源码解读

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [三大核心概念](#三大核心概念)
  - [main()函数](#main函数)	
  - [NetworkManager](#networkmanager)
  - [subnet分配子网](#subnet分配子网)
  - [运行真正的网络插件backend](#运行真正的网络插件backend)
  - [运行flannel](#运行flannel)

<!-- END MUNGE: GENERATED_TOC -->

## 版本
flannel-0.7.1

## 源码目录说明
```
├── backend/          # 后端实现，目前支持 udp、vxlan、hostgw 等
├── network/          # 最上层的网络管理逻辑
├── pkg/              # 抽象出来的功能库，目前只有 `ip`
├── subnet/           # 子网管理功能
├── main.go           # 可执行文件的入口

```

flannel可以为容器提供网络服务。 
其模型为全部的容器使用一个network，然后在每个host上从network中划分一个子网subnet。 
为host上的容器创建网络时，从subnet中划分一个ip给容器。

其采用目前比较流行的no server的方式，即不存在所谓的控制节点，而是每个host上的flanneld从一个etcd中获取相关数据，然后声明自己的子网网段，并记录在etcd中。

其他的host对数据转发时，从etcd中查询到该子网所在的host的ip，然后将数据发往对应host上的flanneld，交由其进行转发。

根据kubernetes的模型，即为每个pod提供一个ip。
flannel的模型正好与之契合。

## 三大核心概念
- network 负责网络的管理（以后的方向是多网络模型，一个主机上同时存在多种网络模式），根据每个网络的配置调用 subnet;
- subnet 负责和 etcd 交互，把 etcd 中的信息转换为 flannel 的子网数据结构，并对 etcd 进行子网和网络的监听;
- backend 接受 subnet 的监听事件，负责增删相应的路由规则

## main()函数
1. 新建一个SubnetManager sm，负责与etcd交互
2. 新建一个NetworkManager nm，并执行其run()，负责调用SubnetManager sm

```go
func main() {
	// glog will log to tmp files by default. override so all entries
	// can flow into journald (if running under systemd)
	flag.Set("logtostderr", "true")

	// now parse command line args
	flag.Parse()

	if flag.NArg() > 0 || opts.help {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTION]...\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}

	if opts.version {
		fmt.Fprintln(os.Stderr, version.Version)
		os.Exit(0)
	}

	flagutil.SetFlagsFromEnv(flag.CommandLine, "FLANNELD")

	/*
		子网Manager，一个主机是一个子网，负责与etcd交互
	*/
	sm, err := newSubnetManager()
	if err != nil {
		log.Error("Failed to create SubnetManager: ", err)
		os.Exit(1)
	}

	// Register for SIGINT and SIGTERM
	/*
		flannel启动第一步：
			注册 SIGINT and SIGTERM信号监听
			安装一个监听内核信号量的handler
	*/
	log.Info("Installing signal handlers")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())

	var runFunc func(ctx context.Context)

	if opts.listen != "" {
		if opts.remote != "" {
			log.Error("--listen and --remote are mutually exclusive")
			os.Exit(1)
		}
		log.Info("running as server")
		runFunc = func(ctx context.Context) {
			remote.RunServer(ctx, sm, opts.listen, opts.remoteCAFile, opts.remoteCertfile, opts.remoteKeyfile)
		}
	} else {
		/*
			一般走这通道
		*/
		nm, err := network.NewNetworkManager(ctx, sm)
		if err != nil {
			log.Error("Failed to create NetworkManager: ", err)
			os.Exit(1)
		}

		/*
			运行 NetworkManager 的 run 函数，并等待它结束
		*/
		runFunc = func(ctx context.Context) {
			nm.Run(ctx)
		}
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		runFunc(ctx)
		wg.Done()
	}()

	<-sigs
	// unregister to get default OS nuke behaviour in case we don't exit cleanly
	signal.Stop(sigs)

	log.Info("Exiting...")
	cancel()

	wg.Wait()
}
```

## NetworkManager
NetworkManager 负责网络的管理，根据 etcd 中的网络配置host主机的网络信息，其中的bm backend.Manager是其核心部分。

```go
type Manager struct {
	ctx             context.Context //请求的上下文
	sm              subnet.Manager  // 子网管理，每台 flannel 节点都是一个子网
	bm              backend.Manager // 负责管理后端支持的插件，比如常用的 udp、xvlan、host-gw
	allowedNetworks map[string]bool //多网络模式会用到，就是一个flannel节点上同时存在多种网络模式
	mux             sync.Mutex
	networks        map[string]*Network //多网络模式下，要管理的网络列表
	watch           bool
	ipMasq          bool
	extIface        *backend.ExternalInterface //和外部节点通信的网络接口，比如 eth0
}
```

实例化一个NetworkManager
```go
func NewNetworkManager(ctx context.Context, sm subnet.Manager) (*Manager, error) {
	extIface, err := lookupExtIface(opts.iface)
	if err != nil {
		return nil, err
	}

	/*
		创建backend Manager
	*/
	bm := backend.NewManager(ctx, sm, extIface)

	manager := &Manager{
		ctx:             ctx,
		sm:              sm,
		bm:              bm,
		allowedNetworks: make(map[string]bool),
		networks:        make(map[string]*Network),
		watch:           opts.watchNetworks,
		ipMasq:          opts.ipMasq,
		extIface:        extIface,
	}

	for _, name := range strings.Split(opts.networks, ",") {
		if name != "" {
			manager.allowedNetworks[name] = true
		}
	}

	return manager, nil
}
```

### NetworkManager.Run()
对于每一个网络n，启动一个groutine执行 func (m *Manager) runNetwork(n *Network)

见/flannel-0.7.1/network/manager.go
```go
func (m *Manager) Run(ctx context.Context) {
	wg := sync.WaitGroup{}

	if m.isMultiNetwork() {
		//一个host主机上多种网络模式
		for {
			// Try adding initial networks
			result, err := m.sm.WatchNetworks(ctx, nil)
			if err == nil {
				for _, n := range result.Snapshot {
					if m.isNetAllowed(n) {
						m.networks[n] = NewNetwork(ctx, m.sm, m.bm, n, m.ipMasq)
					}
				}
				break
			}

			// Otherwise retry in a few seconds
			log.Warning("Failed to retrieve networks (will retry): %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	} else {
		/*
			添加要管理的网络，一个flannel节点只含有一种网络模式的情况
		*/
		m.networks[""] = NewNetwork(ctx, m.sm, m.bm, "", m.ipMasq)
	}

	// Run existing networks
	/*
		1. 在forEachNetwork()中会遍历m *Manager中启动的网络模式，存放到参数n中
		2. 然后，对于每一个网络n，启动一个groutine执行 func (m *Manager) runNetwork(n *Network)
	*/
	m.forEachNetwork(func(n *Network) {
		wg.Add(1)
		go func(n *Network) {
			m.runNetwork(n)
			wg.Done()
		}(n)
	})

	if opts.watchNetworks {
		m.watchNetworks()
	}

	wg.Wait()
	m.bm.Wait()
}
```

看func (m *Manager) runNetwork
```go
func (m *Manager) runNetwork(n *Network) {
	/*
		启动n.Run()
		  - 第一个参数m.extIface是网络接口
		  - 第二个参数是一个初始化的函数（这个函数的工作就是根据网络内容，把配置写到本地的文件）
	*/
	n.Run(m.extIface, func(bn backend.Network) {
		if m.isMultiNetwork() {
			log.Infof("%v: lease acquired: %v", n.Name, bn.Lease().Subnet)

			path := filepath.Join(opts.subnetDir, n.Name) + ".env"
			if err := writeSubnetFile(path, n.Config.Network, m.ipMasq, bn); err != nil {
				log.Warningf("%v failed to write subnet file: %s", n.Name, err)
				return
			}
		} else {
			log.Infof("Lease acquired: %v", bn.Lease().Subnet)

			if err := writeSubnetFile(opts.subnetFile, n.Config.Network, m.ipMasq, bn); err != nil {
				log.Warningf("%v failed to write subnet file: %s", n.Name, err)
				return
			}
			daemon.SdNotify(false, "READY=1")
		}
	})

	m.delNetwork(n)
}

func (n *Network) Run(extIface *backend.ExternalInterface, inited func(bn backend.Network)) {
	/*
		Run()的核心就是循环调用 runOnce()
	*/
	for {
		switch n.runOnce(extIface, inited) {
		case errInterrupted:

		case errCanceled:
			return
		default:
			panic("unexpected error returned")
		}
	}
}
```

### runOnce()

func (n *Network) runOnce 是NetworkManager中最核心的函数，其核心逻辑如下：
  * 读取 etcd 中的值，根据获得的值做一些初始化的工作，retryInit()
  * 调用 backend 的 Run 函数，让 backend 在后台运行，n.bn.Run(ctx)
  * 监听子网在 etcd 中的变化，执行对应的操作
```go
func (n *Network) runOnce(extIface *backend.ExternalInterface, inited func(bn backend.Network)) error {
	/*
		retryInit() 完成初始化的工作，主要是从 etcd 中获取网络的配置，
		根据里面的数据获取对应的 backend、
		从管理的网络中分配一个子网、
		把分配的子网保存到 backend 的数据结构中。
	*/
	if err := n.retryInit(); err != nil {
		return errCanceled
	}

	/*
		运行入参传进来的初始化工作函数inited()，就是上面提到的在本地创建 subnet.env 文件的函数
	*/
	inited(n.bn)

	ctx, interruptFunc := context.WithCancel(n.ctx)

	wg := sync.WaitGroup{}

	/*
		调用具体 backend.network 的 Run() 函数，在后台运行；
		每个 backend 的运行逻辑不同，但思路都是：监听 etcd 的变化，并根据变化后的内容执行命令
			==>/backend/hostgw/network.go
				==>func (n *network) Run(ctx context.Context)
	*/
	wg.Add(1)
	go func() {
		n.bn.Run(ctx)
		wg.Done()
	}()

	evts := make(chan subnet.Event)

	wg.Add(1)
	go func() {
		/*
			监听子网所在 etcd 的变化
		*/
		subnet.WatchLease(ctx, n.sm, n.Name, n.bn.Lease().Subnet, evts)
		wg.Done()
	}()

	/*
		defer语句
		在运行结束的时候，移除设置的 ipmasq(即SNAT)
	*/
	defer func() {
		if n.ipMasq {
			if err := teardownIPMasq(n.Config.Network); err != nil {
				log.Errorf("Failed to tear down IP Masquerade for network %v: %v", n.Name, err)
			}
		}
	}()

	defer wg.Wait()

	renewMargin := time.Duration(opts.subnetLeaseRenewMargin) * time.Minute

	dur := n.bn.Lease().Expiration.Sub(time.Now()) - renewMargin
	/*
		一个死循环，对网络进行续租
		如果是EventAdded说明是添加网络就更新租约时间，
		如果是删除网络事件就把flanneld进程停止
		如果按照正常的超时程序会RenewLease

		使用了etcd节点的ttl时效来实现
		当flanel每当超时后就会重新更新ttl时间。这样flannel就可以维护自己的子网段了
	*/
	for {
		select {
		case <-time.After(dur): /* 超时，要进行续租 */
			err := n.sm.RenewLease(n.ctx, n.Name, n.bn.Lease())
			if err != nil {
				log.Error("Error renewing lease (trying again in 1 min): ", err)
				dur = time.Minute
				continue
			}

			log.Info("Lease renewed, new expiration: ", n.bn.Lease().Expiration)
			dur = n.bn.Lease().Expiration.Sub(time.Now()) - renewMargin

		case e := <-evts: /* 收到监听的事件，进行处理。目前只有两种事件：子网添加，和子网删除 */
			switch e.Type {
			case subnet.EventAdded:
				n.bn.Lease().Expiration = e.Lease.Expiration
				dur = n.bn.Lease().Expiration.Sub(time.Now()) - renewMargin

			case subnet.EventRemoved:
				log.Warning("Lease has been revoked")
				interruptFunc()
				return errInterrupted
			}

		case <-n.ctx.Done():
			return errCanceled
		}
	}
}
```

- retryInit()

```go
func (n *Network) retryInit() error {
	for {
		/*
			init()比较关键
		*/
		err := n.init()
		if err == nil || err == context.Canceled {
			return err
		}

		log.Error(err)

		select {
		case <-n.ctx.Done():
			return n.ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (n *Network) init() error {
	var err error

	n.Config, err = n.sm.GetNetworkConfig(n.ctx, n.Name) //从 etcd 中获取网络的配置，
	if err != nil {
		return wrapError("retrieve network config", err)
	}

	be, err := n.bm.GetBackend(n.Config.BackendType) //获取对应的 backend
	if err != nil {
		return wrapError("create and initialize network", err)
	}

	n.bn, err = be.RegisterNetwork(n.ctx, n.Name, n.Config) //从管理的网络中分配一个子网，把分配的子网保存到 backend 的数据结构中
	if err != nil {
		return wrapError("register network", err)
	}

	if n.ipMasq {
		err = setupIPMasq(n.Config.Network)
		if err != nil {
			return wrapError("set up IP Masquerade", err)
		}
	}

	return nil
}
```
init()函数中需要注意的是`be.RegisterNetwork(n.ctx, n.Name, n.Config)`

## subnet分配子网
分配子网的核心在/subnet/local_manager.go的 `AcquireLease()`。 
会通过各个backend网络的RegisterNetwork()函数进行调用。
```go
func (m *LocalManager) AcquireLease(ctx context.Context, network string, attrs *LeaseAttrs) (*Lease, error) {
	config, err := m.GetNetworkConfig(ctx, network)
	if err != nil {
		return nil, err
	}

	for i := 0; i < raceRetries; i++ {
		l, err := m.tryAcquireLease(ctx, network, config, attrs.PublicIP, attrs)
		switch err {
		case nil:
			return l, nil
		case errTryAgain:
			continue
		default:
			return nil, err
		}
	}

	//最大次数了，获取失败
	return nil, errors.New("Max retries reached trying to acquire a subnet")
}

func (m *LocalManager) tryAcquireLease(ctx context.Context, network string, config *Config, extIaddr ip.IP4, attrs *LeaseAttrs) (*Lease, error) {
	leases, _, err := m.registry.getSubnets(ctx, network)
	if err != nil {
		return nil, err
	}

	/*
		先是看看这个publicip是否以及分配了，如果分配就按照之前分配的，这样服务重启过后仍然能保持
	*/
	// try to reuse a subnet if there's one that matches our IP
	if l := findLeaseByIP(leases, extIaddr); l != nil {
		// make sure the existing subnet is still within the configured network
		if isSubnetConfigCompat(config, l.Subnet) {
			log.Infof("Found lease (%v) for current IP (%v), reusing", l.Subnet, extIaddr)

			ttl := time.Duration(0)
			if !l.Expiration.IsZero() {
				// Not a reservation
				ttl = subnetTTL
			}
			exp, err := m.registry.updateSubnet(ctx, network, l.Subnet, attrs, ttl, 0)
			if err != nil {
				return nil, err
			}

			l.Attrs = *attrs
			l.Expiration = exp
			return l, nil
		} else {
			log.Infof("Found lease (%v) for current IP (%v) but not compatible with current config, deleting", l.Subnet, extIaddr)
			if err := m.registry.deleteSubnet(ctx, network, l.Subnet); err != nil {
				return nil, err
			}
		}
	}

	// no existing match, grab a new one
	/*
		给该节点主机新分配一个子网，注册到etcd中
	*/
	sn, err := m.allocateSubnet(config, leases)
	if err != nil {
		return nil, err
	}

	exp, err := m.registry.createSubnet(ctx, network, sn, attrs, subnetTTL)
	switch {
	case err == nil:
		return &Lease{
			Subnet:     sn,
			Attrs:      *attrs,
			Expiration: exp,
		}, nil
	case isErrEtcdNodeExist(err):
		return nil, errTryAgain
	default:
		return nil, err
	}
}

/*
	func (m *LocalManager) allocateSubnet负责给本主机节点租用一个网段，注册到etcd中
*/
func (m *LocalManager) allocateSubnet(config *Config, leases []Lease) (ip.IP4Net, error) {
	log.Infof("Picking subnet in range %s ... %s", config.SubnetMin, config.SubnetMax)

	var bag []ip.IP4
	sn := ip.IP4Net{IP: config.SubnetMin, PrefixLen: config.SubnetLen}

OuterLoop:
	/*
		从低到高，依次列举所有可以分配的子网(上限是 100)，
		找到那些和已分配的网络没有冲突的，放到数组中备选
	*/
	for ; sn.IP <= config.SubnetMax && len(bag) < 100; sn = sn.Next() {
		for _, l := range leases {
			if sn.Overlaps(l.Subnet) {
				continue OuterLoop
			}
		}
		bag = append(bag, sn.IP)
	}

	/*
		从可用的子网中随机选择一个返回，如果没有可用的，报错。
	*/
	if len(bag) == 0 {
		return ip.IP4Net{}, errors.New("out of subnets")
	} else {
		i := randInt(0, len(bag))
		return ip.IP4Net{IP: bag[i], PrefixLen: config.SubnetLen}, nil
	}
}
```

## 运行真正的网络插件backend
目前支持的backend类型有allpc，awsvpc，gce，hostgw，udp和vxlan。 
从上面可以知道在NetworkManager的函数runOnce()中会运行真正的网络插件backend，以`hostgw`为例子，见/backend/hostgw/network.go

hostgw 模式下，每台主机上的 flannel 只负责路由表的维护就行了，当发现 etcd 中有节点信息变化的时候就随时更新自己主机的路由表项。

func (n *network) Run总结:
  * 监听 etcd 中的子网信息，有event发生的时候调用对应的处理函数。
  * 处理函数handleSubnetEvents()只负责两种事件：子网被添加和子网被删除
```go
func (n *network) Run(ctx context.Context) {
	wg := sync.WaitGroup{}

	/*
		创建一个 Event 的 channel evts，用来存储监听过程中出现的事件
	*/
	log.Info("Watching for new subnet leases")
	evts := make(chan []subnet.Event)
	wg.Add(1)
	/*
		调用 goroutine 在后台监听子网租期的变化，把结果放到 evts 里面传回来
	*/
	go func() {
		subnet.WatchLeases(ctx, n.sm, n.name, n.lease, evts)
		wg.Done()
	}()

	/*
		另外一个后台程序，用来保证主机上的路由表和 etcd 中数据保持一致。
		注意：这个程序只负责添加，不负责删除。
			如果管理的路由表项被手动删除了，或者重启失效，会自动添加上去
	*/
	n.rl = make([]netlink.Route, 0, 10)
	wg.Add(1)
	go func() {
		n.routeCheck(ctx)
		wg.Done()
	}()

	defer wg.Wait()

	/*
		event loop：处理监听到的事件，程序退出的时候返回
	*/
	for {
		select {
		case evtBatch := <-evts:
			n.handleSubnetEvents(evtBatch)

		case <-ctx.Done():
			return
		}
	}
}
```

### 处理函数handleSubnetEvents()
func (n *network) handleSubnetEvents的注意点：为了容错，添加和删除路由出错的时候只是记一条 log，然后就跳过。 
在极端的情况下，会出现本地的路由表项和 etcd 中数据不一致的情况

触发添加或删除的具体逻辑如下：
1. EventAdded:
  * 先是创建一个路由对象，查找本地路由比对，
  * 如果目的地址一样但网关不一样，删了重建
  * 如果已经有重复的则不做任何操作
  * 如果没有这条路由则通过RouteAdd添加到本地路由表中并通过addToRouteList写到本地缓存中

2. EventRemoved:
  * 直接删除本地路由表和本地缓存

```go
func (n *network) handleSubnetEvents(batch []subnet.Event) {
	for _, evt := range batch {
		switch evt.Type {
		case subnet.EventAdded:
			/*
				添加子网事件发生时的处理步骤：
					检查参数是否正常，根据参数构建路由表项，
					把路由表项添加到主机，把路由表项添加到自己的数据结构中
			*/
			log.Infof("Subnet added: %v via %v", evt.Lease.Subnet, evt.Lease.Attrs.PublicIP)

			if evt.Lease.Attrs.BackendType != "host-gw" {
				log.Warningf("Ignoring non-host-gw subnet: type=%v", evt.Lease.Attrs.BackendType)
				continue
			}

			route := netlink.Route{
				Dst:       evt.Lease.Subnet.ToIPNet(),
				Gw:        evt.Lease.Attrs.PublicIP.ToIP(),
				LinkIndex: n.linkIndex,
			}

			// Check if route exists before attempting to add it
			routeList, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
				Dst: route.Dst,
			}, netlink.RT_FILTER_DST)
			if err != nil {
				log.Warningf("Unable to list routes: %v", err)
			}
			//   Check match on Dst for match on Gw
			if len(routeList) > 0 && !routeList[0].Gw.Equal(route.Gw) {
				// Same Dst different Gw. Remove it, correct route will be added below.
				log.Warningf("Replacing existing route to %v via %v with %v via %v.", evt.Lease.Subnet, routeList[0].Gw, evt.Lease.Subnet, evt.Lease.Attrs.PublicIP)
				if err := netlink.RouteDel(&route); err != nil {
					log.Errorf("Error deleting route to %v: %v", evt.Lease.Subnet, err)
					continue
				}
			}
			if len(routeList) > 0 && routeList[0].Gw.Equal(route.Gw) {
				// Same Dst and same Gw, keep it and do not attempt to add it.
				log.Infof("Route to %v via %v already exists, skipping.", evt.Lease.Subnet, evt.Lease.Attrs.PublicIP)
			} else if err := netlink.RouteAdd(&route); err != nil {
				log.Errorf("Error adding route to %v via %v: %v", evt.Lease.Subnet, evt.Lease.Attrs.PublicIP, err)
				continue
			}
			n.addToRouteList(route)

		case subnet.EventRemoved:
			/*
				删除子网事件发生时的处理步骤：
					检查参数是否正常，根据参数构建路由表项，
					把路由表项从主机删除，把路由表项从管理的数据结构中删除
			*/
			log.Info("Subnet removed: ", evt.Lease.Subnet)

			if evt.Lease.Attrs.BackendType != "host-gw" {
				log.Warningf("Ignoring non-host-gw subnet: type=%v", evt.Lease.Attrs.BackendType)
				continue
			}

			route := netlink.Route{
				Dst:       evt.Lease.Subnet.ToIPNet(),
				Gw:        evt.Lease.Attrs.PublicIP.ToIP(),
				LinkIndex: n.linkIndex,
			}
			if err := netlink.RouteDel(&route); err != nil {
				log.Errorf("Error deleting route to %v: %v", evt.Lease.Subnet, err)
				continue
			}
			n.removeFromRouteList(route)

		default:
			log.Error("Internal error: unknown event type: ", int(evt.Type))
		}
	}
}
```

### backend中的公共interface定义
见/backend/common.go
```go
/*
	1. Run 函数是 backend 初始化的时候被调用的，用来启动 event loop，接受关闭信号，以便做一些清理工作
	2. RegisterNetwork 函数在需要后端要管理某个网络的时候被调用，目标就是注册要管理的网络信息到自己的数据结构中，以便后面使用
*/
type Backend interface {
	// Called first to start the necessary event loops and such
	Run(ctx context.Context)
	// Called when the backend should create or begin managing a new network
	RegisterNetwork(ctx context.Context, network string, config *subnet.Config) (Network, error)
}

/*
	具体的工作在每个 backend 的 network 中执行
*/
type Network interface {
	Lease() *subnet.Lease    //返回后端管理的子网租期信息
	MTU() int                //返回后端锁管理网络的 MTU（Maximum Transmission Unit）
	Run(ctx context.Context) //执行后端的核心逻辑
}
```

## 运行flannel
1. flanneld网络使用etcd来保证一致性，所以需要先配置etcd集群
2. 设置网段
```
# etcdctl set /coreos.com/network/config  '{ "Network": "10.1.0.0/16" }'

# etcdctl ls /atomic.io/network/subnets

```
3. 运行flannel，每个节点都要执行
```
# flanneld -etcd-endpoints=http://192.168.91.200:2379,http://192.168.91.201:2379,http://192.168.91.202:2379,http://192.168.91.203:2379 -etcd-prefix="/coreos.com/network">> /var/log/flanneld.log 2>&1 &
```
![iptables](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/flannel-1.png)
4. Flanneld网络会自动给每个节点分配一个网段以保证Docker容器在整个集群内的IP唯一，所以需要把一开始就已经存在的Docker网桥删除掉
```
# iptables -t nat -F
# ifconfig docker0 down
# brctl delbr docker0
```
![iptables](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/flannel-2.png)
5. 重启docker服务
```
# source /run/flannel/subnet.env
# docker -d --bip=${FLANNEL_SUBNET} --mtu=${FLANNEL_MTU} >> /var/log/docker.log 2>&1 &
```
![iptables](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/flannel-3.png)

## 参考
[浅析flannel与docker结合的机制和原理](https://xuxinkun.github.io/2016/07/18/flannel-docker/)

