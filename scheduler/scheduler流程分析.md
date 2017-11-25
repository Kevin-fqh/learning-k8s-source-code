# scheduler流程分析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [scheduler简介](#scheduler简介)
  - [评分策略的注册](#评分策略的注册)	
  - [实例化Scheduler](#实例化Scheduler)
    - [type ConfigFactory struct](#type-configfactory-struct)
	- [createConfig](#createconfig)
	- [Watch-List机制](#watch-list机制)
	- [8个reflector](#8个reflector)
  - [scheduler.Run和scheduler.scheduleOne](#scheduler-run和scheduler-scheduleone)
  - [genericScheduler.Schedule](#genericscheduler-schedule)
    - [预选](#预选)
    - [优选](#优选)
    - [selectHost得出最终节点](#selecthost得出最终节点)
  - [总结](#总结)
  - [Scheduler中的数据结构汇总](#scheduler中的数据结构汇总)
    - [type ScheduleAlgorithm interface](#type-schedulealgorithm-interface)
	- [type ScheduleAlgorithm interface](#type-schedulealgorithm-interface)
	- [type genericScheduler struct](#type-genericscheduler-struct)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 版本说明
v1.3.6

## scheduler简介
kube-scheduler是k8s中的调度模块，负责调度Pod到具体的node节点上。 
其工作原理是kube-scheduler需要对未被调度的Pod进行Watch，同时也需要对node进行watch，因为pod需要绑定到具体的Node上，当kube-scheduler监测到未被调度的pod，它会取出这个pod，然后依照内部设定的调度算法，选择合适的node，然后通过apiserver写回到etcd，至此该pod就绑定到该node上，后续kubelet会读取到该信息，然后在node上把pod给拉起来。

kube-scheduler将PodSpec.NodeName字段为空的Pods逐个进行评分，经过预选(Predicates)和优选(Priorities)两个步骤，挑选最合适的Node作为该Pod的Destination。

1. **预选** 
  * 根据配置的Predicates Policies（默认为DefaultProvider中定义的default predicates policies集合）过滤掉那些不满足这些Policies的的Nodes，
  * 剩下的Nodes就作为优选的输入。
2. **优选**
  * 根据配置的Priorities Policies（默认为DefaultProvider中定义的default priorities policies集合）给预选后的Nodes进行打分排名，得分最高的Node即作为最适合的Node，该Pod就Bind到这个Node。
  * 如果经过优选将Nodes打分排名后，有多个Nodes并列得分最高，那么scheduler将随机从中选择一个Node作为目标Node。

## 评分策略的注册
在kube-scheduler的main()函数运行之前，位于plugin/pkg/scheduler/algorithmprovider/defaults/defaults.go的func init() 函数会先运行，完成评分函数的注册。

DefaultProvider是一个algorithm provider，记录着默认的defaultPredicates()、defaultPriorities()，关于详细的评分策略介绍后续再进行介绍。
```go
func init() {
	/*
		init()函数在main运行前就会运行，它会生成一个algorithm provider
		注册好默认的预选和优选策略
	*/

	factory.RegisterAlgorithmProvider(factory.DefaultProvider, defaultPredicates(), defaultPriorities())
	// EqualPriority is a prioritizer function that gives an equal weight of one to all nodes
	// Register the priority function so that its available
	// but do not include it as part of the default priorities
	/*
				译：EqualPriority是一个优先级函数，给予所有节点一个相等的权重
				   注册优先级函数，使其可用
		  		   但不包括把它作为默认优先级的一部分
	*/
	factory.RegisterPriorityFunction("EqualPriority", scheduler.EqualPriority, 1)
	...
	...
	//这部分都是一些预选策略和优选策略的注册
	...
}
```

## 实例化Scheduler
Scheduler的config设置相对比较简单，来看看一个Scheduler对象是怎么生成的，见 /plugin/cmd/kube-scheduler/app/server.go。

func Run(s *options.SchedulerServer)是cmd/kube-scheduler中最重要的方法：
  * 负责config的生成。
  * 根据config创建sheduler对象。
  * 启动HTTP服务，提供/debug/pprof http接口方便进行性能数据收集调优，提供/metrics http接口以供prometheus收集监控数据。
  * kube-scheduler自选举完成后立刻开始循环执行scheduler.Run进行调度。

```go
// Run runs the specified SchedulerServer.  This should never exit.

func Run(s *options.SchedulerServer) error {
	if c, err := configz.New("componentconfig"); err == nil {
		c.Set(s.KubeSchedulerConfiguration)
	} else {
		glog.Errorf("unable to register configz: %s", err)
	}
	/*
		首先是生成masterClient的配置

		关注kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
		生成配置文件kubeconfig
		因为配置指定会有多种方式，因此需要进行合并

		BuildConfigFromFlags 定义在 /pkg/client/unversioned/clientcmd/client_config.go
		----->func BuildConfigFromFlags(masterUrl, kubeconfigPath string)
	*/
	kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
	if err != nil {
		return err
	}

	kubeconfig.ContentType = s.ContentType
	// Override kubeconfig qps/burst settings from flags
	/*
	 qps（并发度）以及对应的burst（容量）
	*/
	kubeconfig.QPS = s.KubeAPIQPS
	kubeconfig.Burst = int(s.KubeAPIBurst)

	/*
		合并完配置， 新建一个RESTClient
		kubeClient, err := client.New(kubeconfig)
		定义在 /pkg/client/unversioned/helper.go中
		    ---->func New(c *restclient.Config) (*Client, error)
	*/
	kubeClient, err := client.New(kubeconfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	go func() {
		mux := http.NewServeMux()
		healthz.InstallHandler(mux)
		if s.EnableProfiling {
			mux.HandleFunc("/debug/pprof/", pprof.Index)
			mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		}
		configz.InstallHandler(mux)
		//prometheus是源于 Google Borgmon 的一个开源监控系统，用 Golang 开发。被很多人称为下一代监控系统
		mux.Handle("/metrics", prometheus.Handler())

		server := &http.Server{
			Addr:    net.JoinHostPort(s.Address, strconv.Itoa(int(s.Port))),
			Handler: mux,
		}
		glog.Fatal(server.ListenAndServe())
	}()
	/*
		函数首先会构建一个configFactory，一个工厂类。
		NewConfigFactory函数，构建了一些ListerAndWatcher，主要是为了从apiserver这里获取资源信息，
		它不仅获取了pod，node，还获取了PV、PVC、Service、controller(其实是replication controller)、replicaset这些信息后面都用得到。

		定义在/plugin/pkg/scheduler/factory/factory.go
		--->func NewConfigFactory(....）

		Factory包含了众多的ListerAndWatcher，主要是为了watch apiserver，获取最新的资源。创建Factory时，并没有立即启动Factory
		configFactory会在后续的config, err := createConfig(s, configFactory)中启动
	*/
	configFactory := factory.NewConfigFactory(kubeClient, s.SchedulerName, s.HardPodAffinitySymmetricWeight, s.FailureDomains)
	/*
		createConfig(s, configFactory)，configFactory包含创建一个调度器必须的数据结构,
		主要是一些ListerAndWatch，用来watch资源，调度算法依赖于这些资源。

		接着我们用它来创建一个scheduler config
	*/
	config, err := createConfig(s, configFactory)

	if err != nil {
		glog.Fatalf("Failed to create scheduler configuration: %v", err)
	}
	//新建新建一个event广播器，同时监听对应的事件，并且通过EventSink将其存储
	eventBroadcaster := record.NewBroadcaster()
	config.Recorder = eventBroadcaster.NewRecorder(api.EventSource{Component: s.SchedulerName})
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(kubeClient.Events(""))

	//根据config创建sheduler对象。
	sched := scheduler.New(config)

	/*
	   kube-scheduler自选举完成后立刻开始循环执行scheduler.Run进行调度

	   sched.Run()运行调度器。
	   定义于plugin/pkg/scheduler/scheduler.go--->func (s *Scheduler) Run()
	*/
	run := func(_ <-chan struct{}) {
		sched.Run()
		select {}
	}

	if !s.LeaderElection.LeaderElect {
		run(nil)
		glog.Fatal("this statement is unreachable")
		panic("unreachable")
	}

	id, err := os.Hostname()
	if err != nil {
		return err
	}
	//高可用，当有多个kube-scheduler时，确保只有一个能运行。
	leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		EndpointsMeta: api.ObjectMeta{
			Namespace: "kube-system",
			Name:      "kube-scheduler",
		},
		Client:        kubeClient,
		Identity:      id,
		EventRecorder: config.Recorder,
		LeaseDuration: s.LeaderElection.LeaseDuration.Duration,
		RenewDeadline: s.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:   s.LeaderElection.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				glog.Fatalf("lost master")
			},
		},
	})

	glog.Fatal("this statement is unreachable")
	panic("unreachable")
}
```

### type ConfigFactory struct
Factory包含了众多的ListerAndWatcher，主要是为了watch apiserver，及时获取最新的资源。 

两个重要成员PodQueue和PodLister：
  * PodQueue存放着等待kube-scheduler调度的pod
  * PodLister=schedulerCache

```go
// Initializes the factory.
func NewConfigFactory(client *client.Client, schedulerName string, hardPodAffinitySymmetricWeight int, failureDomains string) *ConfigFactory {
	//Factory包含了众多的ListerAndWatcher，主要是为了watch apiserver，获取最新的资源。创建Factory时，并没有立即启动Factory
	stopEverything := make(chan struct{})
	schedulerCache := schedulercache.New(30*time.Second, stopEverything)
	/*
		PodQueue存放着等待kube-scheduler调度的pod,
	*/
	c := &ConfigFactory{
		Client:             client,
		PodQueue:           cache.NewFIFO(cache.MetaNamespaceKeyFunc),
		ScheduledPodLister: &cache.StoreToPodLister{},
		// Only nodes in the "Ready" condition with status == "True" are schedulable
		NodeLister:                     &cache.StoreToNodeLister{},
		PVLister:                       &cache.StoreToPVFetcher{Store: cache.NewStore(cache.MetaNamespaceKeyFunc)},
		PVCLister:                      &cache.StoreToPVCFetcher{Store: cache.NewStore(cache.MetaNamespaceKeyFunc)},
		ServiceLister:                  &cache.StoreToServiceLister{Store: cache.NewStore(cache.MetaNamespaceKeyFunc)},
		ControllerLister:               &cache.StoreToReplicationControllerLister{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})},
		ReplicaSetLister:               &cache.StoreToReplicaSetLister{Store: cache.NewStore(cache.MetaNamespaceKeyFunc)},
		schedulerCache:                 schedulerCache,
		StopEverything:                 stopEverything,
		SchedulerName:                  schedulerName,
		HardPodAffinitySymmetricWeight: hardPodAffinitySymmetricWeight,
		FailureDomains:                 failureDomains,
	}

	c.PodLister = schedulerCache

	// On add/delete to the scheduled pods, remove from the assumed pods.
	// We construct this here instead of in CreateFromKeys because
	// ScheduledPodLister is something we provide to plug in functions that
	// they may need to call.
	c.ScheduledPodLister.Indexer, c.scheduledPodPopulator = framework.NewIndexerInformer(
		/*
			获取所有已经调度完成的，且不处于Terminated状态的pod
		*/
		c.createAssignedNonTerminatedPodLW(),
		&api.Pod{},
		0,
		framework.ResourceEventHandlerFuncs{
			AddFunc:    c.addPodToCache,
			UpdateFunc: c.updatePodInCache,
			DeleteFunc: c.deletePodFromCache,
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	c.NodeLister.Store, c.nodePopulator = framework.NewInformer(
		c.createNodeLW(),
		&api.Node{},
		0,
		framework.ResourceEventHandlerFuncs{
			AddFunc:    c.addNodeToCache,
			UpdateFunc: c.updateNodeInCache,
			DeleteFunc: c.deleteNodeFromCache,
		},
	)

	return c
}
```
```go
// ConfigFactory knows how to fill out a scheduler config with its support functions.
type ConfigFactory struct {
	Client *client.Client
	// queue for pods that need scheduling
	PodQueue *cache.FIFO
	// a means to list all known scheduled pods.
	ScheduledPodLister *cache.StoreToPodLister
	// a means to list all known scheduled pods and pods assumed to have been scheduled.
	PodLister algorithm.PodLister
	// a means to list all nodes
	NodeLister *cache.StoreToNodeLister
	// a means to list all PersistentVolumes
	PVLister *cache.StoreToPVFetcher
	// a means to list all PersistentVolumeClaims
	PVCLister *cache.StoreToPVCFetcher
	// a means to list all services
	ServiceLister *cache.StoreToServiceLister
	// a means to list all controllers
	ControllerLister *cache.StoreToReplicationControllerLister
	// a means to list all replicasets
	ReplicaSetLister *cache.StoreToReplicaSetLister

	// Close this to stop all reflectors
	StopEverything chan struct{}

	scheduledPodPopulator *framework.Controller
	nodePopulator         *framework.Controller

	schedulerCache schedulercache.Cache

	// SchedulerName of a scheduler is used to select which pods will be
	// processed by this scheduler, based on pods's annotation key:
	// 'scheduler.alpha.kubernetes.io/name'
	SchedulerName string

	// RequiredDuringScheduling affinity is not symmetric, but there is an implicit PreferredDuringScheduling affinity rule
	// corresponding to every RequiredDuringScheduling affinity rule.
	// HardPodAffinitySymmetricWeight represents the weight of implicit PreferredDuringScheduling affinity rule, in the range 0-100.
	HardPodAffinitySymmetricWeight int

	// Indicate the "all topologies" set for empty topologyKey when it's used for PreferredDuringScheduling pod anti-affinity.
	FailureDomains string
}
```

### createConfig
createConfig函数，主要分为两条路径：
  * configFactory.CreateFromConfig(policy)，需要用户自行传递，用户可以自己组合评分函数
  * configFactory.CreateFromProvider(s.AlgorithmProvider)，使用前面系统默认的评分函数组合

这里需要注意的是`configFactory.CreateFromProvider()`中会调动configFactory的Run()函数，对apiserver进行List-Watch，开始获取资源。
```go
func createConfig(s *options.SchedulerServer, configFactory *factory.ConfigFactory) (*scheduler.Config, error) {
	/*
		用户自定义的Policy函数，它包含了PodFitsPorts、PodFitsResources、NoDiskConflict等过滤函数；
		LeastRequestedPriority、BalancedResourceAllocation等加权函数。

		这些函数在 plugin/pkg/scheduler/algorithmprovider/defaults/defaults.go中的
		   --->func init() 中已经注册进去
	*/
	if _, err := os.Stat(s.PolicyConfigFile); err == nil {
		var (
			policy     schedulerapi.Policy
			configData []byte
		)
		configData, err := ioutil.ReadFile(s.PolicyConfigFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read policy config: %v", err)
		}
		if err := runtime.DecodeInto(latestschedulerapi.Codec, configData, &policy); err != nil {
			return nil, fmt.Errorf("invalid configuration: %v", err)
		}
		return configFactory.CreateFromConfig(policy)
		/*
			CreateFromConfig(policy)定义于/plugin/pkg/scheduler/factory/factory.go
			--->func (f *ConfigFactory) CreateFromConfig(policy schedulerapi.Policy)
		*/
	}

	// if the config file isn't provided, use the specified (or default) provider
	return configFactory.CreateFromProvider(s.AlgorithmProvider)
	/*
		无论是从policyfile，还是从algorithmprovider创建scheduler，最后都会在func (f *ConfigFactory) CreateFromKeys函数中
		调用一个函数NewGenericScheduler。这个函数构建了一个真正的调度器。
		--->algo := scheduler.NewGenericScheduler(f.schedulerCache, predicateFuncs, priorityConfigs, extenders)
	*/

}
```

### Watch-List机制
1. 启动factory，watch apiserver，获取最新的资源。
2. 调用NewGenericScheduler()，生成真正的Scheduler
```go
// Creates a scheduler from a set of registered fit predicate keys and priority keys.
//译：基于一组注册的predicate keys and priority keys 创建scheduler
func (f *ConfigFactory) CreateFromKeys(predicateKeys, priorityKeys sets.String, extenders []algorithm.SchedulerExtender) (*scheduler.Config, error) {
	glog.V(2).Infof("creating scheduler with fit predicates '%v' and priority functions '%v", predicateKeys, priorityKeys)

	if f.HardPodAffinitySymmetricWeight < 0 || f.HardPodAffinitySymmetricWeight > 100 {
		return nil, fmt.Errorf("invalid hardPodAffinitySymmetricWeight: %d, must be in the range 0-100", f.HardPodAffinitySymmetricWeight)
	}

	predicateFuncs, err := f.GetPredicates(predicateKeys)
	if err != nil {
		return nil, err
	}

	priorityConfigs, err := f.GetPriorityFunctionConfigs(priorityKeys)
	if err != nil {
		return nil, err
	}
	/*
		启动factory
		Factory包含了众多的ListerAndWatcher，主要是为了watch apiserver，获取最新的资源。
		创建Factory时，并没有立即启动Factory，而是在创建scheduler config时，启动了这个Factory。也就是这里的f.Run()
	*/
	f.Run()
	/*
		调用NewGenericScheduler(f.schedulerCache, predicateFuncs, priorityConfigs, extenders)
		定义于/plugin/pkg/scheduler/generic_scheduler.go
		--->func NewGenericScheduler(...)
	*/
	algo := scheduler.NewGenericScheduler(f.schedulerCache, predicateFuncs, priorityConfigs, extenders)

	podBackoff := podBackoff{
		perPodBackoff: map[types.NamespacedName]*backoffEntry{},
		clock:         realClock{},

		defaultDuration: 1 * time.Second,
		maxDuration:     60 * time.Second,
	}

	return &scheduler.Config{
		SchedulerCache: f.schedulerCache,
		// The scheduler only needs to consider schedulable nodes.
		NodeLister:          f.NodeLister.NodeCondition(getNodeConditionPredicate()),
		Algorithm:           algo,
		Binder:              &binder{f.Client},
		PodConditionUpdater: &podConditionUpdater{f.Client},
		NextPod: func() *api.Pod {
			return f.getNextPod()
		},
		Error:          f.makeDefaultErrorFunc(&podBackoff, f.PodQueue),
		StopEverything: f.StopEverything,
	}, nil
}
```

### 8个reflector
这里启动了kube-schduler用到的8个reflector
```go
func (f *ConfigFactory) Run() {
	/*
		这里创建了6个reflector，还有两个在创建Factory时就创建好的reflector：scheduledPodPopulator和nodePopulator。
		然后启动了这些reflector。
	*/
	// Watch and queue pods that need scheduling.
	/*获取需要进行调度的pod资源存放到PodQueue*/
	cache.NewReflector(f.createUnassignedNonTerminatedPodLW(), &api.Pod{}, f.PodQueue, 0).RunUntil(f.StopEverything)

	// Begin populating scheduled pods.
	go f.scheduledPodPopulator.Run(f.StopEverything)

	// Begin populating nodes.
	go f.nodePopulator.Run(f.StopEverything)

	// Watch PVs & PVCs
	// They may be listed frequently for scheduling constraints, so provide a local up-to-date cache.
	cache.NewReflector(f.createPersistentVolumeLW(), &api.PersistentVolume{}, f.PVLister.Store, 0).RunUntil(f.StopEverything)
	cache.NewReflector(f.createPersistentVolumeClaimLW(), &api.PersistentVolumeClaim{}, f.PVCLister.Store, 0).RunUntil(f.StopEverything)

	// Watch and cache all service objects. Scheduler needs to find all pods
	// created by the same services or ReplicationControllers/ReplicaSets, so that it can spread them correctly.
	// Cache this locally.
	cache.NewReflector(f.createServiceLW(), &api.Service{}, f.ServiceLister.Store, 0).RunUntil(f.StopEverything)

	// Watch and cache all ReplicationController objects. Scheduler needs to find all pods
	// created by the same services or ReplicationControllers/ReplicaSets, so that it can spread them correctly.
	// Cache this locally.
	cache.NewReflector(f.createControllerLW(), &api.ReplicationController{}, f.ControllerLister.Indexer, 0).RunUntil(f.StopEverything)

	// Watch and cache all ReplicaSet objects. Scheduler needs to find all pods
	// created by the same services or ReplicationControllers/ReplicaSets, so that it can spread them correctly.
	// Cache this locally.
	cache.NewReflector(f.createReplicaSetLW(), &extensions.ReplicaSet{}, f.ReplicaSetLister.Store, 0).RunUntil(f.StopEverything)
}

/*
	定义了需要调度的pod的条件
*/
// Returns a cache.ListWatch that finds all pods that need to be
// scheduled.
func (factory *ConfigFactory) createUnassignedNonTerminatedPodLW() *cache.ListWatch {
	selector := fields.ParseSelectorOrDie("spec.nodeName==" + "" + ",status.phase!=" + string(api.PodSucceeded) + ",status.phase!=" + string(api.PodFailed))
	return cache.NewListWatchFromClient(factory.Client, "pods", api.NamespaceAll, selector)
}
```

## scheduler.Run和scheduler.scheduleOne
kube-scheduler自选举完成后立刻开始循环执行scheduler.Run进行调度，见plugin/pkg/scheduler/scheduler.go

```go
// Run begins watching and scheduling. It starts a goroutine and returns immediately.
func (s *Scheduler) Run() {
	/*
		启动goroutine，循环反复执行Scheduler.scheduleOne方法，直到收到shut down scheduler的信号
	*/
	go wait.Until(s.scheduleOne, 0, s.config.StopEverything)
}
```

Scheduler.scheduleOne开始真正的调度逻辑，每次负责一个Pod的调度：
  * 首先pod := s.config.NextPod()从PodQueue中获取一个未调度的pod，
  * 然后s.config.Algorithm.Schedule(pod, s.config.NodeLister)，即用算法为pod选择一个合适的node，
  * AssumePod(&assumed),更新SchedulerCache中Pod的状态(AssumePod)，假设该Pod已经被scheduled
  * 最后s.config.Binder.Bind(b)绑定pod到node上去。
  * 绑定失败，回滚处理
```go
func (s *Scheduler) scheduleOne() {
	/*
		/plugin/pkg/scheduler/factory/factory.go
			==>func (f *ConfigFactory) getNextPod() *api.Pod
	*/
	pod := s.config.NextPod()

	glog.V(3).Infof("Attempting to schedule: %+v", pod)
	start := time.Now()
	/*
		 	进行实际调度，默认是调度算法是上面提到的DefaultProvider，也就是执行具体的调度算法

			选择合适的node
			定义于plugin/pkg/scheduler/generic_scheduler.go
			---->func (g *genericScheduler) Schedule(pod *api.Pod, nodeLister algorithm.NodeLister)
	*/
	dest, err := s.config.Algorithm.Schedule(pod, s.config.NodeLister)
	if err != nil {
		glog.V(1).Infof("Failed to schedule: %+v", pod)
		s.config.Error(pod, err)
		s.config.Recorder.Eventf(pod, api.EventTypeWarning, "FailedScheduling", "%v", err)
		s.config.PodConditionUpdater.Update(pod, &api.PodCondition{
			Type:   api.PodScheduled,
			Status: api.ConditionFalse,
			Reason: "Unschedulable",
		})
		return
	}
	metrics.SchedulingAlgorithmLatency.Observe(metrics.SinceInMicroseconds(start))

	// Optimistically assume that the binding will succeed and send it to apiserver
	// in the background.
	// The only risk in this approach is that if the binding fails because of some
	// reason, scheduler will be assuming that it succeeded while scheduling next
	// pods, until the assumption in the internal cache expire (expiration is
	// defined as "didn't read the binding via watch within a given timeout",
	// timeout is currently set to 30s). However, after this timeout, the situation
	// will self-repair.
	/*
		译：乐观地假定绑定将成功，并将其发送到后台的apiserver。
		   唯一的风险在于如果因为某些情况绑定失败了，scheduler在准备开始调度下一个pod的时候，会假设前一个pod已经绑定成功，
		   直到内部cache的假设过期时间（30s之内没有通过watch读到绑定的信息）。
			超过这个时间，该情况会被修复
	*/
	assumed := *pod
	assumed.Spec.NodeName = dest
	/*
		AssumePod(&assumed)
		更新SchedulerCache中该Pod的状态，假设该Pod已经被预绑定，
		同时把pod information写入NodeInfo中

		定义在/plugin/pkg/scheduler/schedulercache/cache.go
		--->func (cache *schedulerCache) AssumePod(pod *api.Pod)
	*/
	if err := s.config.SchedulerCache.AssumePod(&assumed); err != nil {
		glog.Errorf("scheduler cache AssumePod failed: %v", err)
	}

	go func() {
		defer metrics.E2eSchedulingLatency.Observe(metrics.SinceInMicroseconds(start))
		//将pod绑定到node，Binding也是一个类型，&api.Binding
		b := &api.Binding{
			ObjectMeta: api.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name},
			Target: api.ObjectReference{
				Kind: "Node",
				Name: dest,
			},
		}

		bindingStart := time.Now()
		// If binding succeeded then PodScheduled condition will be updated in apiserver so that
		// it's atomic with setting host.
		/*
			译：如果绑定成功，则PodScheduled condition将被更新到apiserver中，以便它与设置主机是原子的。
		*/
		/*
			err := s.config.Binder.Bind(b) 发送调度结果给master
			调用kube-Client的Bind接口，完成node和pod的Bind操作，如果Bind失败，从SchedulerCache中删除上一步中已经Assumed的Pod
				==>/plugin/pkg/scheduler/factory/factory.go
					==>func (b *binder) Bind(binding *api.Binding) error
		*/
		err := s.config.Binder.Bind(b)
		if err != nil {
			glog.V(1).Infof("Failed to bind pod: %v/%v", pod.Namespace, pod.Name)
			s.config.Error(pod, err)
			s.config.Recorder.Eventf(pod, api.EventTypeNormal, "FailedScheduling", "Binding rejected: %v", err)
			//绑定失败后的处理
			s.config.PodConditionUpdater.Update(pod, &api.PodCondition{
				Type:   api.PodScheduled,
				Status: api.ConditionFalse,
				Reason: "BindingRejected",
			})
			return
		}
		metrics.BindingLatency.Observe(metrics.SinceInMicroseconds(bindingStart))
		//记录一条调度信息
		s.config.Recorder.Eventf(pod, api.EventTypeNormal, "Scheduled", "Successfully assigned %v to %v", pod.Name, dest)
	}()
}
```

- 预绑定时更新NodeInfo信息

见/plugin/pkg/scheduler/schedulercache/node_info.go
```go
// addPod adds pod information to this NodeInfo.
func (n *NodeInfo) addPod(pod *api.Pod) {
	cpu, mem, nvidia_gpu, non0_cpu, non0_mem := calculateResource(pod)
	n.requestedResource.MilliCPU += cpu
	n.requestedResource.Memory += mem
	n.requestedResource.NvidiaGPU += nvidia_gpu
	n.nonzeroRequest.MilliCPU += non0_cpu
	n.nonzeroRequest.Memory += non0_mem
	n.pods = append(n.pods, pod)
}
```

## genericScheduler.Schedule
下面的核心就是如何选择一个合适的Node节点，见plugin/pkg/scheduler/generic_scheduler.go

method Schedule尝试将给定pod安排到节点列表中的某个节点：
  * 如果成功，它将返回节点的名称。
  * 如果失败，它将返回一个error。

```go
// Schedule tries to schedule the given pod to one of node in the node list.
// If it succeeds, it will return the name of the node.
// If it fails, it will return a Fiterror error with reasons.

/*
	genericScheduler作为一个默认Scheduler，当然也必须实现定义于pkg/scheduler/algorithm/scheduler_interface.go中的
		----->type ScheduleAlgorithm interface
*/
func (g *genericScheduler) Schedule(pod *api.Pod, nodeLister algorithm.NodeLister) (string, error) {
	var trace *util.Trace
	if pod != nil {
		trace = util.NewTrace(fmt.Sprintf("Scheduling %s/%s", pod.Namespace, pod.Name))
	} else {
		trace = util.NewTrace("Scheduling <nil> pod")
	}
	defer trace.LogIfLong(20 * time.Millisecond)
	// 从cache中获取可被调度的Nodes
	nodes, err := nodeLister.List()
	if err != nil {
		return "", err
	}
	if len(nodes.Items) == 0 {
		return "", ErrNoNodesAvailable
	}

	// Used for all fit and priority funcs.
	nodeNameToInfo, err := g.cache.GetNodeNameToInfoMap()
	if err != nil {
		return "", err
	}
	// 开始预选
	trace.Step("Computing predicates")
	filteredNodes, failedPredicateMap, err := findNodesThatFit(pod, nodeNameToInfo, g.predicates, nodes, g.extenders)
	if err != nil {
		return "", err
	}

	if len(filteredNodes.Items) == 0 {
		return "", &FitError{
			Pod:              pod,
			FailedPredicates: failedPredicateMap,
		}
	}
	// 开始优选打分，调用PrioritizeNodes
	trace.Step("Prioritizing")
	priorityList, err := PrioritizeNodes(pod, nodeNameToInfo, g.prioritizers, algorithm.FakeNodeLister(filteredNodes), g.extenders)
	if err != nil {
		return "", err
	}
	// 如果优选出多个Node，则随机选择一个Node作为最佳Node返回
	trace.Step("Selecting host")
	return g.selectHost(priorityList)
}
```

### 预选
checkNode会调用podFitsOnNode完成配置的所有Predicates Policies对该Node的检查。

podFitsOnNode循环执行所有配置的Predicates Polic对应的predicateFunc。只有全部策略都通过，该node才符合要求。

具体的Predicate Policy对应的PredicateFunc都定义在plugin/pkg/scheduler/algorithm/predicates/predicates.Go中
```go
// Filters the nodes to find the ones that fit based on the given predicate functions
// Each node is passed through the predicate functions to determine if it is a fit
func findNodesThatFit(pod *api.Pod, nodeNameToInfo map[string]*schedulercache.NodeInfo, predicateFuncs map[string]algorithm.FitPredicate, nodes api.NodeList, extenders []algorithm.SchedulerExtender) (api.NodeList, FailedPredicateMap, error) {
	predicateResultLock := sync.Mutex{}
	filtered := []api.Node{}
	failedPredicateMap := FailedPredicateMap{}
	errs := []error{}
	// checkNode会调用podFitsOnNode完成配置的所有Predicates Policies对该Node的检查。
	checkNode := func(i int) {
		nodeName := nodes.Items[i].Name
		fits, failedPredicate, err := podFitsOnNode(pod, nodeNameToInfo[nodeName], predicateFuncs)

		predicateResultLock.Lock()
		defer predicateResultLock.Unlock()
		if err != nil {
			errs = append(errs, err)
			return
		}
		if fits {
			filtered = append(filtered, nodes.Items[i])
		} else {
			failedPredicateMap[nodeName] = failedPredicate
		}
	}
	// 根据nodes数量，启动最多16个个goroutine worker执行checkNode方法
	workqueue.Parallelize(16, len(nodes.Items), checkNode)
	if len(errs) > 0 {
		return api.NodeList{}, FailedPredicateMap{}, errors.NewAggregate(errs)
	}
	// 如果配置了Extender，则执行Extender的Filter逻辑再次进行甩选。
	if len(filtered) > 0 && len(extenders) != 0 {
		for _, extender := range extenders {
			filteredList, err := extender.Filter(pod, &api.NodeList{Items: filtered})
			if err != nil {
				return api.NodeList{}, FailedPredicateMap{}, err
			}
			filtered = filteredList.Items
			if len(filtered) == 0 {
				break
			}
		}
	}
	return api.NodeList{Items: filtered}, failedPredicateMap, nil
}

// Checks whether node with a given name and NodeInfo satisfies all predicateFuncs.
//译：检查具有给定名称和NodeInfo的节点是否满足所有predicateFuncs。
func podFitsOnNode(pod *api.Pod, info *schedulercache.NodeInfo, predicateFuncs map[string]algorithm.FitPredicate) (bool, string, error) {
	/*
		循环执行所有配置的Predicates Polic对应的predicateFunc。
		只有全部策略都通过，该node才符合要求

		具体的Predicate Policy对应的PredicateFunc都定义在plugin/pkg/scheduler/algorithm/predicates/predicates.Go中
	*/
	for _, predicate := range predicateFuncs {
		fit, err := predicate(pod, info)
		if err != nil {
			switch e := err.(type) {
			case *predicates.InsufficientResourceError:
				if fit {
					err := fmt.Errorf("got InsufficientResourceError: %v, but also fit='true' which is unexpected", e)
					return false, "", err
				}
			case *predicates.PredicateFailureError:
				if fit {
					err := fmt.Errorf("got PredicateFailureError: %v, but also fit='true' which is unexpected", e)
					return false, "", err
				}
			default:
				return false, "", err
			}
		}
		if !fit {
			if re, ok := err.(*predicates.InsufficientResourceError); ok {
				return false, fmt.Sprintf("Insufficient %s", re.ResourceName), nil
			}
			if re, ok := err.(*predicates.PredicateFailureError); ok {
				return false, re.PredicateName, nil
			} else {
				err := fmt.Errorf("SchedulerPredicates failed due to %v, which is unexpected.", err)
				return false, "", err
			}
		}
	}
	return true, "", nil
}
```

### 优选
先使用PriorityFunction计算节点的分数，在向priorityFunctionMap注册PriorityFunction时，会指定该PriorityFunction对应的weight，然后再累加每个PriorityFunction和weight相乘的积，这就样就得到了这个节点的分数。

具体的Priorities Policy对应的PriorityFunc都定义在plugin/pkg/scheduler/algorithm/priorities/*.go中
```go
// Prioritizes the nodes by running the individual priority functions in parallel.
// Each priority function is expected to set a score of 0-10
// 0 is the lowest priority score (least preferred node) and 10 is the highest
// Each priority function can also have its own weight
// The node scores returned by the priority function are multiplied by the weights to get weighted scores
// All scores are finally combined (added) to get the total weighted scores of all nodes
func PrioritizeNodes(
	pod *api.Pod,
	nodeNameToInfo map[string]*schedulercache.NodeInfo,
	priorityConfigs []algorithm.PriorityConfig,
	nodeLister algorithm.NodeLister,
	extenders []algorithm.SchedulerExtender,
) (schedulerapi.HostPriorityList, error) {
	/*
		func PrioritizeNodes（...） 优选打分
		根据所有配置到Priorities Policies对所有预选后的Nodes进行优选打分
		每个Priorities policy对每个node打分范围为0-10分，分越高表示越合适

		具体的Priorities Policy对应的PriorityFunc都定义在plugin/pkg/scheduler/algorithm/priorities/*.go中
	*/
	result := schedulerapi.HostPriorityList{}

	// If no priority configs are provided, then the EqualPriority function is applied
	// This is required to generate the priority list in the required format
	if len(priorityConfigs) == 0 && len(extenders) == 0 {
		return EqualPriority(pod, nodeNameToInfo, nodeLister)
	}

	var (
		mu             = sync.Mutex{}
		wg             = sync.WaitGroup{}
		combinedScores = map[string]int{}
		errs           []error
	)

	for _, priorityConfig := range priorityConfigs {
		// skip the priority function if the weight is specified as 0
		if priorityConfig.Weight == 0 {
			continue
		}

		wg.Add(1)
		go func(config algorithm.PriorityConfig) {
			defer wg.Done()
			weight := config.Weight
			priorityFunc := config.Function
			prioritizedList, err := priorityFunc(pod, nodeNameToInfo, nodeLister)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			//对得分进行加权求和得到最终分数
			for i := range prioritizedList {
				host, score := prioritizedList[i].Host, prioritizedList[i].Score
				combinedScores[host] += score * weight
			}
		}(priorityConfig)
	}
	if len(errs) != 0 {
		return schedulerapi.HostPriorityList{}, errors.NewAggregate(errs)
	}

	// wait for all go routines to finish
	wg.Wait()
	// 如果配置了Extender，则再执行Extender的优选打分方法Extender.Prioritize
	if len(extenders) != 0 && nodeLister != nil {
		nodes, err := nodeLister.List()
		if err != nil {
			return schedulerapi.HostPriorityList{}, err
		}
		for _, extender := range extenders {
			wg.Add(1)
			go func(ext algorithm.SchedulerExtender) {
				defer wg.Done()
				prioritizedList, weight, err := ext.Prioritize(pod, &nodes)
				if err != nil {
					// Prioritization errors from extender can be ignored, let k8s/other extenders determine the priorities
					return
				}
				mu.Lock()
				// 执行combinedScores，将非Extender优选后的node得分再次经过Extender的优选打分排序
				for i := range *prioritizedList {
					host, score := (*prioritizedList)[i].Host, (*prioritizedList)[i].Score
					combinedScores[host] += score * weight
				}
				mu.Unlock()
			}(extender)
		}
	}
	// wait for all go routines to finish
	wg.Wait()

	for host, score := range combinedScores {
		glog.V(10).Infof("Host %s Score %d", host, score)
		result = append(result, schedulerapi.HostPriority{Host: host, Score: score})
	}
	return result, nil
}
```

### selectHost得出最终节点
```go
// selectHost takes a prioritized list of nodes and then picks one
// in a round-robin manner from the nodes that had the highest score.
/*
	如果分数最高的节点有多个，则根据最高分节点的个数进行round-robin选择。
*/
func (g *genericScheduler) selectHost(priorityList schedulerapi.HostPriorityList) (string, error) {
	if len(priorityList) == 0 {
		return "", fmt.Errorf("empty priorityList")
	}

	sort.Sort(sort.Reverse(priorityList))
	//获取最高分
	maxScore := priorityList[0].Score
	firstAfterMaxScore := sort.Search(len(priorityList), func(i int) bool { return priorityList[i].Score < maxScore })

	g.lastNodeIndexLock.Lock()
	ix := int(g.lastNodeIndex % uint64(firstAfterMaxScore))
	g.lastNodeIndex++
	g.lastNodeIndexLock.Unlock()

	return priorityList[ix].Host, nil
}
```

## 总结
1. kube-scheduler作为kubernetes master上一个单独的进程提供调度服务，通过–master指定kube-api-server的地址，用来watch pod和node和调用api server bind接口完成node和pod的Bind操作。

2. kube-scheduler中维护了一个FIFO类型的PodQueue cache，新创建的Pod都会被ConfigFactory watch到，被添加到该PodQueue中，每次调度都从该PodQueue中getNextPod作为即将调度的Pod。

3. 获取到待调度的Pod后，就执行AlgorithmProvider配置Algorithm的Schedule方法进行调度，整个调度过程分两个关键步骤：Predicates和Priorities，最终选出一个最适合的Node返回。

4. 更新SchedulerCache中Pod的状态(AssumePod)，标志该Pod为scheduled。

5. 向apiserver发送&api.Binding对象，表示绑定成功。如果Bind失败，执行回滚操作。

## Scheduler中的数据结构汇总
### type AlgorithmProviderConfig struct
调度算法提供者，FitPredicateKeys代表了一组预选函数，PriorityFunctionKeys代表了一组优选函数。
```go
type AlgorithmProviderConfig struct {
	FitPredicateKeys     sets.String
	PriorityFunctionKeys sets.String
}
```

### type ScheduleAlgorithm interface
ScheduleAlgorithm是一个实现如何将pod调度到machines上的一个接口。 见/plugin/pkg/scheduler/algorithm/scheduler_interface.go
```go
// ScheduleAlgorithm is an interface implemented by things that know how to schedule pods
// onto machines.

/*
	type ScheduleAlgorithm interface 是Schedule Algorithm要实现的Schedule接口：
*/
type ScheduleAlgorithm interface {
	Schedule(*api.Pod, NodeLister) (selectedMachine string, err error)
}
```

### type genericScheduler struct
genericScheduler是一个具体的调度者，它实现了type ScheduleAlgorithm interface
```go
type genericScheduler struct {
	cache             schedulercache.Cache
	predicates        map[string]algorithm.FitPredicate
	prioritizers      []algorithm.PriorityConfig
	extenders         []algorithm.SchedulerExtender
	pods              algorithm.PodLister
	lastNodeIndexLock sync.Mutex
	lastNodeIndex     uint64
}
```

## 参考
[Kubernetes Scheduler源码分析](http://blog.csdn.net/waltonwang/article/details/54565638)

[kube-scheduler剖析](http://licyhust.com/容器技术/2016/10/02/kube-scheduler/)