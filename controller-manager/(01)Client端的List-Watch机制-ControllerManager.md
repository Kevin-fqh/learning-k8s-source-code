# Client端的List-Watch机制-ControllerManager

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [kubelet获取Apiserver的数据](#kubelet获取apiserver的数据)
  - [func NewListWatchFromClient](#func-newlistwatchfromclient)
  - [func newSourceApiserverFromLW](#func-newsourceapiserverfromlw)
  - [type UndeltaStore struct](#type-undeltastore-struct)
  - [Reflector部分](#reflector部分)

<!-- END MUNGE: GENERATED_TOC -->

分析ControllerManager对资源的watch-list的时候，需要注意的一个点是： 一个资源是分为共享型和独占型的，两中类型的watch机制是不一样的。

比如说，一类是replication controller，另一类是pods。 
这两类资源刚好属于两个不同的范畴，pods是许多控制器共享的，像endpoint controller也需要对pods进行watch； 
而replication controller是独享的。因此对他们的watch机制也不一样。

所以informer也分为两类，共享和非共享。这两类informer本质上都是对reflector的封装。

本文首先以对pod资源的List-Watch的主线，进行 **共享型informer** 的学习。

## type sharedInformerFactory struct
SharedInformerFactory 是什么？ 
SharedInformerFactory provides interface which holds unique informers for **pods, nodes, namespaces, persistent volume claims and persistent volumes** 。 
其接口定义在`/pkg/controller/informers/factory.go`
```go
// SharedInformerFactory provides interface which holds unique informers for pods, nodes, namespaces, persistent volume
// claims and persistent volumes
type SharedInformerFactory interface {
	// Start starts informers that can start AFTER the API server and controllers have started
	Start(stopCh <-chan struct{})

	ForResource(unversioned.GroupResource) (GenericInformer, error)

	// when you update these, update generic.go/ForResource, same package

	Pods() PodInformer
	LimitRanges() LimitRangeInformer
	Namespaces() NamespaceInformer
	Nodes() NodeInformer
	PersistentVolumeClaims() PVCInformer
	PersistentVolumes() PVInformer
	ServiceAccounts() ServiceAccountInformer

	DaemonSets() DaemonSetInformer
	Deployments() DeploymentInformer
	ReplicaSets() ReplicaSetInformer

	ClusterRoleBindings() ClusterRoleBindingInformer
	ClusterRoles() ClusterRoleInformer
	RoleBindings() RoleBindingInformer
	Roles() RoleInformer

	StorageClasses() StorageClassInformer

	Jobs() JobInformer
}
```
而type sharedInformerFactory struct是type SharedInformerFactory interface的实现
```go
type sharedInformerFactory struct {
	client        clientset.Interface
	lock          sync.Mutex
	defaultResync time.Duration

	informers map[reflect.Type]cache.SharedIndexInformer
	// startedInformers is used for tracking which informers have been started
	// this allows calling of Start method multiple times
	startedInformers map[reflect.Type]bool
}
```
下面来看看type sharedInformerFactory struct 提供的功能函数，这些都会在后面kube-controller-manager启动的时候用得到。 
kube-controller-manager正是依靠Informer来获取对应的resource信息，从而做出反应。 

- 新建一个SharedInformerFactory对象

NewSharedInformerFactory constructs a new instance of sharedInformerFactory。 
其informers属性会记录着各种SharedIndexInformer。 
包括PodInformer、NodeInformer、NamespaceInformer、PVCInformer、ServiceAccountInformer ......。 
具体见`type SharedInformerFactory interface`定义的接口。
```go
func NewSharedInformerFactory(client clientset.Interface, defaultResync time.Duration) SharedInformerFactory {
	return &sharedInformerFactory{
		client:           client,
		defaultResync:    defaultResync,
		informers:        make(map[reflect.Type]cache.SharedIndexInformer),
		startedInformers: make(map[reflect.Type]bool),
	}
}
```

- 启动所有的informers

Start函数会把所有注册过的informers都分别启动一个groutine， run起来。
```go
// Start initializes all requested informers.
func (f *sharedInformerFactory) Start(stopCh <-chan struct{}) {
	f.lock.Lock()
	defer f.lock.Unlock()

	for informerType, informer := range f.informers {
		if !f.startedInformers[informerType] {
			/*
				运行informer.Run(stopCh)
				==>定义在pkg/client/cache/shared_informer.go
					==>func (s *sharedIndexInformer) Run(stopCh <-chan struct{})
			*/
			go informer.Run(stopCh)
			f.startedInformers[informerType] = true
		}
	}
}
```
关于`go informer.Run(stopCh)`， 是启动一个的informer，会在后面进行讲解。

- 具体resource的informer

kube-controller-manager会通过下述方式来获取对应的resource：
  1. sharedInformers.Pods().Informer(), 
  2. sharedInformers.Pods(), 
  3. sharedInformers.Nodes(), 
  4. sharedInformers.DaemonSets(),
		
```go
// Pods returns a SharedIndexInformer that lists and watches all pods
func (f *sharedInformerFactory) Pods() PodInformer {
	return &podInformer{sharedInformerFactory: f}
}

// Nodes returns a SharedIndexInformer that lists and watches all nodes
func (f *sharedInformerFactory) Nodes() NodeInformer {
	return &nodeInformer{sharedInformerFactory: f}
}
...
...
```

## type podInformer struct
type podInformer struct 实现了type PodInformer interface， 见`/pkg/controller/informers/core.go`。

```go
// PodInformer is type of SharedIndexInformer which watches and lists all pods.
// Interface provides constructor for informer and lister for pods
/*
	type PodInformer interface是一种 SharedIndexInformer ，用于watches and lists所有的pods
*/
type PodInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() *cache.StoreToPodLister
}

type podInformer struct {
	*sharedInformerFactory
}

// Informer checks whether podInformer exists in sharedInformerFactory and if not, it creates new informer of type
// podInformer and connects it to sharedInformerFactory
/*
	func (f *podInformer) Informer()检查podInformer是否已经在sharedInformerFactory中存在。
	如果不存在，func会创建一个新的informer，类型是podInformer，
	然后把其和sharedInformerFactory联系上。
*/
func (f *podInformer) Informer() cache.SharedIndexInformer {
	f.lock.Lock()
	defer f.lock.Unlock()

	informerType := reflect.TypeOf(&api.Pod{})
	informer, exists := f.informers[informerType]
	if exists {
		return informer
	}
	informer = NewPodInformer(f.client, f.defaultResync)
	f.informers[informerType] = informer

	return informer
}

// Lister returns lister for podInformer
func (f *podInformer) Lister() *cache.StoreToPodLister {
	informer := f.Informer()
	return &cache.StoreToPodLister{Indexer: informer.GetIndexer()}
}
```

来看看func (f *podInformer) Informer()中的`informer = NewPodInformer(f.client, f.defaultResync)`。 
主要是新建了一个type sharedIndexInformer struct对象。
```go
// NewPodInformer returns a SharedIndexInformer that lists and watches all pods
func NewPodInformer(client clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	sharedIndexInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return client.Core().Pods(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return client.Core().Pods(api.NamespaceAll).Watch(options)
			},
		},
		&api.Pod{},
		resyncPeriod,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	return sharedIndexInformer
}
```

## type sharedIndexInformer struct
```go
type sharedIndexInformer struct {
	indexer    Indexer
	controller *Controller

	processor             *sharedProcessor  //记录着所有注册的controller
	cacheMutationDetector CacheMutationDetector

	// This block is tracked to handle late initialization of the controller
	listerWatcher    ListerWatcher
	objectType       runtime.Object
	fullResyncPeriod time.Duration

	started     bool
	startedLock sync.Mutex

	// blockDeltas gives a way to stop all event distribution so that a late event handler
	// can safely join the shared informer.
	blockDeltas sync.Mutex
	// stopCh is the channel used to stop the main Run process.  We have to track it so that
	// late joiners can have a proper stop
	stopCh <-chan struct{}
}

type sharedProcessor struct {
	listeners []*processorListener
}
```

## kube-controller-manager启动各种controller
在`kubernetes-1.5.2/cmd/kube-controller-manager/app/controllermanager.go`中，kube-controller-manager启动启动的时候，会把所有的controller都run起来。 

分析func StartControllers，可以发现多个controller都是通过sharedInformers提供的接口来获取对应的resource。

比如说endpointcontroller、replicationcontroller和nodeController都需要对pod资源进行List-Watch。 

所以这些controller都会向`sharedInformers`注册自己的存在，表示我是`sharedInformers`的一个listener。 

```go
func StartControllers(s *options.CMServer, kubeconfig *restclient.Config, rootClientBuilder, clientBuilder controller.ControllerClientBuilder, stop <-chan struct{}, recorder record.EventRecorder) error {
	client := func(serviceAccountName string) clientset.Interface {
		return rootClientBuilder.ClientOrDie(serviceAccountName)
	}
	discoveryClient := client("controller-discovery").Discovery()
	/*
		创建了一个可以被多个controller共享的 sharedInformers
		后面各个conreller通过
			sharedInformers.Pods().Informer()
			sharedInformers.Pods(), sharedInformers.Nodes(), sharedInformers.DaemonSets(),
		来获取对应的resource

		NewSharedInformerFactory
		定义在/pkg/controller/informers/factory.go
			==>func NewSharedInformerFactory(client clientset.Interface, defaultResync time.Duration) SharedInformerFactory
	*/
	sharedInformers := informers.NewSharedInformerFactory(client("shared-informers"), ResyncPeriod(s)())
	
	...
	...
	
	go endpointcontroller.NewEndpointController(sharedInformers.Pods().Informer(), client("endpoint-controller")).
		Run(int(s.ConcurrentEndpointSyncs), wait.NeverStop)
	time.Sleep(wait.Jitter(s.ControllerStartInterval.Duration, ControllerStartJitter))
	
	/*
		NewReplicationManager函数定义在
			==>/pkg/controller/replication/replication_controller.go
				==>func NewReplicationManager

		sharedInformers.Pods().Informer()定义在
			==>/pkg/controller/informers/factory.go
				==>func NewSharedInformerFactory(client clientset.Interface, defaultResync time.Duration) SharedInformerFactory
					==>func (f *sharedInformerFactory) Pods() PodInformer
						==>/pkg/controller/informers/core.go
							==>func (f *podInformer) Informer() cache.SharedIndexInformer
	*/
	go replicationcontroller.NewReplicationManager(
		sharedInformers.Pods().Informer(),
		clientBuilder.ClientOrDie("replication-controller"),
		ResyncPeriod(s),
		replicationcontroller.BurstReplicas,
		int(s.LookupCacheSizeForRC),
		s.EnableGarbageCollector,
	).Run(int(s.ConcurrentRCSyncs), wait.NeverStop)
	
	...
	...
	
	nodeController, err := nodecontroller.NewNodeController(
		sharedInformers.Pods(), sharedInformers.Nodes(), sharedInformers.DaemonSets(),
		cloud, client("node-controller"),
		s.PodEvictionTimeout.Duration, s.NodeEvictionRate, s.SecondaryNodeEvictionRate, s.LargeClusterSizeThreshold, s.UnhealthyZoneThreshold, s.NodeMonitorGracePeriod.Duration,
		s.NodeStartupGracePeriod.Duration, s.NodeMonitorPeriod.Duration, clusterCIDR, serviceCIDR,
		int(s.NodeCIDRMaskSize), s.AllocateNodeCIDRs)
	if err != nil {
		glog.Fatalf("Failed to initialize nodecontroller: %v", err)
	}
	nodeController.Run()
	time.Sleep(wait.Jitter(s.ControllerStartInterval.Duration, ControllerStartJitter))

	serviceController, err := servicecontroller.New(cloud, client("service-controller"), s.ClusterName)
	
	...
	...
	
	/*
		上面已经初始化完所有的controllers
		启动sharedInformers
		定义在/pkg/controller/informers/factory.go
			==>func (f *sharedInformerFactory) Start(stopCh <-chan struct{})
	*/
	sharedInformers.Start(stop)

	select {}
```

最后的`sharedInformers.Start(stop)`，会把各种共享型informer都给run起来。 
当初始化完所有的controllers，才会启动这些SharedIndexInformer。  
函数定义见上面的[type sharedInformerFactory struct](#type-sharedinformerfactory-struct)

***

下面来看看一个controller是如何向一个共享型informer（即sharedInformers）注册自身的存在的。

## replicationcontroller向podInformer注册
以replicationcontroller向podInformer注册为例，见`/pkg/controller/replication/replication_controller.go`。

可以发现func NewReplicationManager的入参`podInformer cache.SharedIndexInformer`是上面的`sharedInformers.Pods().Informer()`。

```go
func NewReplicationManager(podInformer cache.SharedIndexInformer, kubeClient clientset.Interface, resyncPeriod controller.ResyncPeriodFunc, burstReplicas int, lookupCacheSize int, garbageCollectorEnabled bool) *ReplicationManager {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&unversionedcore.EventSinkImpl{Interface: kubeClient.Core().Events("")})
	/*
		调用newReplicationManager生成真正的replication manager
	*/
	return newReplicationManager(
		eventBroadcaster.NewRecorder(api.EventSource{Component: "replication-controller"}),
		podInformer, kubeClient, resyncPeriod, burstReplicas, lookupCacheSize, garbageCollectorEnabled)
}
```

主要关注replicationcontroller的pod资源`podInformer`的使用
```go
func newReplicationManager(eventRecorder record.EventRecorder, podInformer cache.SharedIndexInformer, kubeClient clientset.Interface, resyncPeriod controller.ResyncPeriodFunc, burstReplicas int, lookupCacheSize int, garbageCollectorEnabled bool) *ReplicationManager {
    ...
    ...
	/*
		共享型资源pod
		podinformer是共享的，即SharedIndexInformer，多个controller是如何共享该podinformer的？？？
			==>每一种controller需要使用podinformer时，都会注册event handler
		*****
		类似于Replication Controller向podInformer注册自己的存在，表示我订阅了你
		即Replication Controller会成为podInformer的一个listener

		当初始化完所有的controllers，才会启动这些SharedIndexInformer
		启动这些SharedIndexInformer 见
			==>/cmd/kube-controller-manager/app/controllermanager.go
				==>func StartControllers
					==>sharedInformers.Start(stop)

		AddEventHandler定义在
			==>/pkg/client/cache/shared_informer.go
				==>func (s *sharedIndexInformer) AddEventHandler(handler ResourceEventHandler)
	*/
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: rm.addPod,
		// This invokes the rc for every pod change, eg: host assignment. Though this might seem like overkill
		// the most frequent pod update is status, and the associated rc will only list from local storage, so
		// it should be ok.
		/*
			译：对于每个pod 的change都会唤起replication controller
		*/
		UpdateFunc: rm.updatePod,
		DeleteFunc: rm.deletePod,
	})
	rm.podStore.Indexer = podInformer.GetIndexer()
	rm.podController = podInformer.GetController()
	
    ...
    ...
```

- podInformer.AddEventHandler 
来看看type sharedIndexInformer struct的AddEventHandler函数，见`/pkg/client/cache/shared_informer.go`。

每一种controller需要使用podinformer时，都会注册，podinformer将handler ResourceEventHandler包装成listerner，然后添加到s.processor.listeners里面。

这里需要注意的是初始化注册的时候，s.started应该是false，走这个通道，注册完就return nil出去了。 

listener并没有在这里run起来，而是会在后面所有controller都初始化完成之后，统一run起来。
```go
func (s *sharedIndexInformer) AddEventHandler(handler ResourceEventHandler) error {
	/*
		类似于Replication Controller向podInformer注册自己的存在，表示我订阅了你
		即Replication Controller会成为podInformer的一个listener

		以资源pod为例：
			podinformer将某个controller对资源pod 的event handler包装成listerner，
			然后添加到s.processor.listeners里面
	*/
	s.startedLock.Lock()
	defer s.startedLock.Unlock()

	if !s.started {
		/*
			初始注册的时候，s.started应该是false，
			走这个通道，注册完就return nil出去了
			listener并没有run起来
		*/
		listener := newProcessListener(handler)
		s.processor.listeners = append(s.processor.listeners, listener)
		return nil
	}

	// in order to safely join, we have to
	// 1. stop sending add/update/delete notifications
	// 2. do a list against the store
	// 3. send synthetic "Add" events to the new handler
	// 4. unblock
	s.blockDeltas.Lock()
	defer s.blockDeltas.Unlock()

	/*
		注意listener和s.processor.listeners的区别和关系
	*/
	listener := newProcessListener(handler)
	s.processor.listeners = append(s.processor.listeners, listener)

	go listener.run(s.stopCh)
	go listener.pop(s.stopCh)

	items := s.indexer.List()
	/*
		往一个listener中添加事件event
	*/
	for i := range items {
		listener.add(addNotification{newObj: items[i]})
	}

	return nil
}
```

- newProcessListener  

```go
type processorListener struct {
	// lock/cond protects access to 'pendingNotifications'.
	lock sync.RWMutex
	cond sync.Cond

	// pendingNotifications is an unbounded slice that holds all notifications not yet distributed
	// there is one per listener, but a failing/stalled listener will have infinite pendingNotifications
	// added until we OOM.
	// TODO This is no worse that before, since reflectors were backed by unbounded DeltaFIFOs, but
	// we should try to do something better
	pendingNotifications []interface{}

	nextCh chan interface{}

	handler ResourceEventHandler
}

func newProcessListener(handler ResourceEventHandler) *processorListener {
	ret := &processorListener{
		pendingNotifications: []interface{}{},
		nextCh:               make(chan interface{}),
		handler:              handler,
	}

	ret.cond.L = &ret.lock
	return ret
}
```

至此replicationcontroller已经向podInformer成功注册。 
podInformer会在所有的controller都初始化完成之后启动。



## 一个informer run起来之后是如何运行的
在前面提到过`sharedInformers.Start(stop)`， 最后会调用定义在pkg/client/cache/shared_informer.go的`func (s *sharedIndexInformer) Run(stopCh <-chan struct{})` 来启动一个informer。

其流程如下：
1. 构建一个controller，controller的作用就是构建一个reflector，然后将watch到的资源放入fifo这个cache里面。
2. 放入之后Process: s.HandleDeltas会对资源进行处理。
3. 在启动controller之前，先启动了s.processor.run(stopCh)，启动在前面已经向sharedIndexInformer注册了的各个listener。

```go
func (s *sharedIndexInformer) Run(stopCh <-chan struct{}) {
	/*
		 对比/pkg/controller/replication/replication_controller.go
			==>func newReplicationManager
				==>rm.rcStore.Indexer, rm.rcController = cache.NewIndexerInformer
	*/
	defer utilruntime.HandleCrash()

	fifo := NewDeltaFIFO(MetaNamespaceKeyFunc, nil, s.indexer)

	cfg := &Config{
		Queue:            fifo,
		ListerWatcher:    s.listerWatcher,
		ObjectType:       s.objectType,
		FullResyncPeriod: s.fullResyncPeriod,
		RetryOnError:     false,

		/*
			查看func (s *sharedIndexInformer) HandleDeltas(obj interface{}) error
			共享型的Informer是如何处理event的？
			在这里定义了对event的分发函数 HandleDeltas
		*/
		Process: s.HandleDeltas,
	}

	func() {
		s.startedLock.Lock()
		defer s.startedLock.Unlock()

		/*
			构建一个controller
			controller的作用就是构建一个reflector，
			然后将watch到的资源放入fifo这个cache里面。
			放入之后Process: s.HandleDeltas会对资源进行处理
		*/
		s.controller = New(cfg)
		s.started = true
	}()

	s.stopCh = stopCh
	s.cacheMutationDetector.Run(stopCh)
	/*
		在启动controller之前，先启动了s.processor.run(stopCh)，
		启动已经向sharedIndexInformer注册了的各个listener
		各个listener是如何处理将要接收到的event的？
	*/
	s.processor.run(stopCh)
	s.controller.Run(stopCh)
}
```

先来看看`s.processor.run(stopCh)`，见`/pkg/client/cache/shared_informer.go`。 
这里的p.listeners正是前面的`func (s *sharedIndexInformer) AddEventHandler(handler ResourceEventHandler)`中一个controller向shareInformer注册时添加的。
```go
func (p *sharedProcessor) run(stopCh <-chan struct{}) {
	for _, listener := range p.listeners {
		/*
			启动已经向sharedIndexInformer注册了的各个listener
		*/
		go listener.run(stopCh)
		go listener.pop(stopCh)
	}
}


```
