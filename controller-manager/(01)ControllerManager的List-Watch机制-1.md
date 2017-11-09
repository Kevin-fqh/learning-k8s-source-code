# ControllerManager的List-Watch机制-1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type sharedInformerFactory struct](#type-sharedinformerfactory-struct)
  - [type podInformer struct](#type-podinformer-struct)
  - [type sharedIndexInformer struct](#type-sharedindexinformer-struct)
  - [kube-controller-manager启动各种controller](#kube-controller-manager启动各种controller)
  - [replicationcontroller向podInformer注册](#replicationcontroller向podinformer注册)
  - [一个informer run起来之后是如何运行的](#一个informer-run起来之后是如何运行的)
    - [type Controller struct](#type-controller-struct)
    - [nextCh chanel的生产者和消费者](#nextch-chanel的生产者和消费者)
  - [replication controller 注册的管理pod的函数](#replication-controller-注册的管理pod的函数)
  - [type DeltaFIFO struct](#type-deltafifo-struct)
  - [路线图](#路线图)

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
type podInformer struct 实现了type PodInformer interface， 见`/pkg/controller/informers/core.go`。 是其中的一种sharedIndexInformer。

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

看一下NewPodInformer函数，这里面定义了ListFunc和WatchFunc。 这里声明了下面Reflector机制的List-Watch的数据源头。
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

- type processorListener struct  

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
1. NewDeltaFIFO创建了一个type DeltaFIFO struct对象
2. 构建一个controller，controller的作用就是构建一个reflector，然后将watch到的资源放入fifo这个cache里面。
3. 放入之后Process: s.HandleDeltas会对资源进行处理。
4. 在启动controller之前，先启动了s.processor.run(stopCh)，启动在前面已经向sharedIndexInformer注册了的各个listener。

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

s.controller.Run(stopCh) 会完成消息的分发，把watch到的信息分发到各个listener中。

s.processor.run(stopCh) 中包含了一个生产消费者模型。 
这种模式也kubernetes中非常常见的。 通过两个groutine来构造一个生产消费者模型。

### type Controller struct 
controller的作用就是构建一个reflector，然后将watch到的资源放入fifo这个cache里面。 
放入之后Process: s.HandleDeltas会对资源进行处理，完成消息的分发。

首先来看看`Process: s.HandleDeltas,`的定义，它会在后面通过controller来启动。
```go
func (s *sharedIndexInformer) HandleDeltas(obj interface{}) error {
	s.blockDeltas.Lock()
	defer s.blockDeltas.Unlock()

	// from oldest to newest
	for _, d := range obj.(Deltas) {
		/*
			在func (p *sharedProcessor) distribute(obj interface{})中，
			被watch的资源被传到了各个listener
			各个listener是如何处理的？见
				==>/pkg/client/cache/shared_informer.go
					==>func (s *sharedIndexInformer) Run(stopCh <-chan struct{})
						==>s.processor.run(stopCh)
		*/
		switch d.Type {
		case Sync, Added, Updated:
			s.cacheMutationDetector.AddObject(d.Object)
			if old, exists, err := s.indexer.Get(d.Object); err == nil && exists {
				if err := s.indexer.Update(d.Object); err != nil {
					return err
				}
				s.processor.distribute(updateNotification{oldObj: old, newObj: d.Object})
			} else {
				if err := s.indexer.Add(d.Object); err != nil {
					return err
				}
				s.processor.distribute(addNotification{newObj: d.Object})
			}
		case Deleted:
			if err := s.indexer.Delete(d.Object); err != nil {
				return err
			}
			s.processor.distribute(deleteNotification{oldObj: d.Object})
		}
	}
	return nil
}
```

主要是调用distribute函数来完成信息的分发，把消息发送给所有的listener。
```go
func (p *sharedProcessor) distribute(obj interface{}) {
	for _, listener := range p.listeners {
		/*
			调用listernner的add函数，负责将notify装进pendingNotifications，
		*/
		listener.add(obj)
	}
}

func (p *processorListener) add(notification interface{}) {
	/*
		listenser的add函数负责将notify装进pendingNotifications，
	*/
	p.lock.Lock()
	defer p.lock.Unlock()

	p.pendingNotifications = append(p.pendingNotifications, notification)
	p.cond.Broadcast()
}
```

- type Controller struct  

type Controller struct 是一个通用的controller框架。 体现了Reflector机制。
```go

// Controller is a generic controller framework.

type Controller struct {
	config         Config
	reflector      *Reflector
	reflectorMutex sync.RWMutex
}

// New makes a new Controller from the given Config.
func New(c *Config) *Controller {
	ctlr := &Controller{
		config: *c,
	}
	return ctlr
}

// Run begins processing items, and will continue until a value is sent down stopCh.
// It's an error to call Run more than once.
// Run blocks; call via go.

func (c *Controller) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	/*
		首先构建了一个reflector,这里体现了Wtach-List
		从这里看出informer只是包装了reflector
	*/
	r := NewReflector(
		c.config.ListerWatcher,
		c.config.ObjectType,
		c.config.Queue, // Reflector机制的store，即前面的NewDeltaFIFO构建的type DeltaFIFO struct对象
		c.config.FullResyncPeriod,
	)

	c.reflectorMutex.Lock()
	c.reflector = r
	c.reflectorMutex.Unlock()

	/*
		启动reflector
	*/
	r.RunUntil(stopCh)

	/*
		启动func (c *Controller) processLoop()
		消费event
		其对应的生产者是上面的Reflector机制，
		reflector往fifo里面添加数据，而processLoop就不停地去消费这里这些数据。

		查看对应cache的Add、Update、Delete函数
		这里的cache是c.config.Queue，其类型是type DeltaFIFO struct ，
			eg：==>/pkg/client/cache/delta_fifo.go中的
					==>func (f *DeltaFIFO) Add(obj interface{}) error
					==>func (f *DeltaFIFO) Delete(obj interface{})
					==>func (f *DeltaFIFO) Update(obj interface{}) error
	*/
	wait.Until(c.processLoop, time.Second, stopCh) ///每秒调用一次
}

// processLoop drains the work queue.
// TODO: Consider doing the processing in parallel. This will require a little thought
// to make sure that we don't end up processing the same object multiple times
// concurrently.

func (c *Controller) processLoop() {
	for {
		/*
			调用func (f *DeltaFIFO) Pop(process PopProcessFunc)
			==>定义在/pkg/client/cache/delta_fifo.go
		*/
		obj, err := c.config.Queue.Pop(PopProcessFunc(c.config.Process))
		if err != nil {
			if c.config.RetryOnError {
				// This is the safe way to re-enqueue.
				c.config.Queue.AddIfNotPresent(obj)
			}
		}
	}
}
```
目前只需要知道`PopProcessFunc(c.config.Process)`就是上面的`func (s *sharedIndexInformer) HandleDeltas(obj interface{})`，
也就是说Controller完成了event的分发。 

Controller中List-Watch的数据源是一个Queue，type DeltaFIFO struct ，用到了Reflect机制，这部分的分析和Apiserver端的分析是差不多的。

### nextCh chanel的生产者和消费者
在上面的`s.processor.run(stopCh)`中，见`/pkg/client/cache/shared_informer.go`。 
这里的p.listeners正是前面的`func (s *sharedIndexInformer) AddEventHandler(handler ResourceEventHandler)`中一个controller向shareInformer注册时添加的一个个listener。
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

- type processorListener struct  

来看看processorListener的功能函数：  
- pop负责取出pendingNotifications的第一个nofify, 输入nextCh这个channel，是生产者。 这里就是前面的controller分发event对应上了。
- run函数则负责取出notify，然后根据notify的类型(增加、删除、更新)触发相应的处理函数，这个函数是各个controller注册的。

也就是说 type processorListener struct 的add函数负责将notify装进pendingNotifications。
而pop函数取出pendingNotifications的第一个nofify, 输入nextCh这个channel。 
最后run函数则负责取出notify，然后根据notify的类型(增加、删除、更新)触发相应的处理函数，这个函数是各个controller注册的。

```go
func (p *processorListener) pop(stopCh <-chan struct{}) {
	/*
		pop函数取出pendingNotifications的第一个nofify,输入nextCh这个channel
	*/
	defer utilruntime.HandleCrash()

	for {
		blockingGet := func() (interface{}, bool) {
			p.lock.Lock()
			defer p.lock.Unlock()

			for len(p.pendingNotifications) == 0 {
				// check if we're shutdown
				select {
				case <-stopCh:
					return nil, true
				default:
				}
				p.cond.Wait()
			}

			nt := p.pendingNotifications[0]
			p.pendingNotifications = p.pendingNotifications[1:]
			return nt, false
		}

		notification, stopped := blockingGet()
		if stopped {
			return
		}

		select {
		case <-stopCh:
			return
		case p.nextCh <- notification:
		}
	}
}

func (p *processorListener) run(stopCh <-chan struct{}) {
	/*
		run函数则负责取出notify，
		然后根据notify的类型(增加、删除、更新)触发相应的处理函数，
		这个函数是各个controller注册的。
	*/
	defer utilruntime.HandleCrash()

	for {
		var next interface{}
		select {
		case <-stopCh:
			func() {
				p.lock.Lock()
				defer p.lock.Unlock()
				p.cond.Broadcast()
			}()
			return
		case next = <-p.nextCh:
		}

		switch notification := next.(type) {
		case updateNotification:
			p.handler.OnUpdate(notification.oldObj, notification.newObj)
		case addNotification:
			/*
				 replication controller 注册的add函数定义在
					==>/pkg/controller/replication/replication_controller.go
						==>podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs
							==>AddFunc: rm.addPod,
			*/
			p.handler.OnAdd(notification.newObj)
		case deleteNotification:
			p.handler.OnDelete(notification.oldObj)
		default:
			utilruntime.HandleError(fmt.Errorf("unrecognized notification: %#v", next))
		}
	}
}
```

## replication controller 注册的管理pod的函数
最后再来看看replication controller 注册的管理pod的函数。

- 如何注册的？

首先它是通过定义在`/pkg/client/cache/shared_informer.go` 的 `func (s *sharedIndexInformer) AddEventHandler(handler ResourceEventHandler)`来完成注册的。

AddFunc、UpdateFunc和DeleteFunc都由handler来掌握，handler的类型是一个`type ResourceEventHandler interface `
```go
type ResourceEventHandler interface {
	OnAdd(obj interface{})
	OnUpdate(oldObj, newObj interface{})
	OnDelete(obj interface{})
}

// ResourceEventHandlerFuncs is an adaptor to let you easily specify as many or
// as few of the notification functions as you want while still implementing
// ResourceEventHandler.
/*
	ResourceEventHandlerFuncs是一个适配器，可让您轻松地指定尽可能少的通知函数，
	同时仍然在实现type ResourceEventHandler interface。
*/
type ResourceEventHandlerFuncs struct {
	AddFunc    func(obj interface{})
	UpdateFunc func(oldObj, newObj interface{})
	DeleteFunc func(obj interface{})
}
```

- 三个函数

定义在/pkg/controller/replication/replication_controller.go 中。

```go
// When a pod is created, enqueue the controller that manages it and update it's expectations.
/*
	译：创建pod后，将管理它的controller（指PodController）排入队列，并更新该rc的期望值。
*/
func (rm *ReplicationManager) addPod(obj interface{}) {
	pod := obj.(*api.Pod)

	/*
		首先会根据pod得到rc，
		当pod不属于任何一个rc时，则return。
		找到rc以后，更新rm.expectations.CreationObserved(rcKey)这个rc的期望值，
		也就是假如一个rc有4个pod，现在检测到创建了一个pod，则会将这个rc的期望值减少，变为3。
		最后将这个rc放入队列。
	*/
	rc := rm.getPodController(pod)
	if rc == nil {
		return
	}
	rcKey, err := controller.KeyFunc(rc)
	if err != nil {
		glog.Errorf("Couldn't get key for replication controller %#v: %v", rc, err)
		return
	}

	if pod.DeletionTimestamp != nil {
		// on a restart of the controller manager, it's possible a new pod shows up in a state that
		// is already pending deletion. Prevent the pod from being a creation observation.
		/*
			译：在重新启动controller manager时，可能会出现一个新的pod处于等待删除的状态。
		*/
		rm.deletePod(pod)
		return
	}
	rm.expectations.CreationObserved(rcKey)
	rm.enqueueController(rc)
}

// When a pod is updated, figure out what controller/s manage it and wake them
// up. If the labels of the pod have changed we need to awaken both the old
// and new controller. old and cur must be *api.Pod types.
func (rm *ReplicationManager) updatePod(old, cur interface{})

// When a pod is deleted, enqueue the controller that manages the pod and update its expectations.
// obj could be an *api.Pod, or a DeletionFinalStateUnknown marker item.
func (rm *ReplicationManager) deletePod(obj interface{}) 
```

enqueueController把消息obj发送给各个worker中。

```go
// obj could be an *api.ReplicationController, or a DeletionFinalStateUnknown marker item.
func (rm *ReplicationManager) enqueueController(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		glog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}

	// TODO: Handle overlapping controllers better. Either disallow them at admission time or
	// deterministically avoid syncing controllers that fight over pods. Currently, we only
	// ensure that the same controller is synced for a given pod. When we periodically relist
	// all controllers there will still be some replica instability. One way to handle this is
	// by querying the store for all controllers that this rc overlaps, as well as all
	// controllers that overlap this rc, and sorting them.
	/*
		会把obj的key加入到replicationmanager的queue里面

		这里相当于一个生产者
		其对应的消费者位于func (rm *ReplicationManager) worker()
			==>replicationmanager创建了五个worker去消费这里添加的key

		rm.queue.Add函数定义在
			==>package /pkg/util/workqueue
	*/
	rm.queue.Add(key)
}
```
这里的rm.queue 是类型是workqueue.RateLimitingInterface。更新完一个rc资源之后，把其放入queue中，等待ReplicationManager的worker的处理，确保Pod副本数与rc规定的相同。

## type DeltaFIFO struct
Reflector机制的store是一个type DeltaFIFO struct对象，Reflector保证只会把符合expectedType类型的对象存放到store中。

```go
type DeltaFIFO struct {
	// lock/cond protects access to 'items' and 'queue'.
	lock sync.RWMutex
	cond sync.Cond

	// We depend on the property that items in the set are in
	// the queue and vice versa, and that all Deltas in this
	// map have at least one Delta.
	items map[string]Deltas
	queue []string

	// populated is true if the first batch of items inserted by Replace() has been populated
	// or Delete/Add/Update was called first.
	populated bool
	// initialPopulationCount is the number of items inserted by the first call of Replace()
	initialPopulationCount int

	// keyFunc is used to make the key used for queued item
	// insertion and retrieval, and should be deterministic.
	keyFunc KeyFunc

	// deltaCompressor tells us how to combine two or more
	// deltas. It may be nil.
	deltaCompressor DeltaCompressor

	// knownObjects list keys that are "known", for the
	// purpose of figuring out which items have been deleted
	// when Replace() or Delete() is called.
	knownObjects KeyListerGetter
}

// Add inserts an item, and puts it in the queue. The item is only enqueued
// if it doesn't already exist in the set.
func (f *DeltaFIFO) Add(obj interface{}) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.populated = true
	/*
		Add、Update、Delete操作最后都调用了queueActionLocked函数
	*/
	return f.queueActionLocked(Added, obj)
}
```

- func (f *DeltaFIFO) queueActionLocked

最后处理的结果会更新到f.items里面，相当于一个生产者！ 
其对应的消费者在`func (f *DeltaFIFO) Pop(process PopProcessFunc)`

```go
// queueActionLocked appends to the delta list for the object, calling
// f.deltaCompressor if needed. Caller must lock first.
/*
	译：queueActionLocked附加到对象的增量列表中，
		如果需要，调用f.deltaCompressor。 Caller必须先执行锁操作。

	处理的结果会更新到f.items里面
*/
func (f *DeltaFIFO) queueActionLocked(actionType DeltaType, obj interface{}) error {
	id, err := f.KeyOf(obj)
	if err != nil {
		return KeyError{obj, err}
	}

	// If object is supposed to be deleted (last event is Deleted),
	// then we should ignore Sync events, because it would result in
	// recreation of this object.
	if actionType == Sync && f.willObjectBeDeletedLocked(id) {
		return nil
	}

	newDeltas := append(f.items[id], Delta{actionType, obj})
	newDeltas = dedupDeltas(newDeltas)
	if f.deltaCompressor != nil {
		newDeltas = f.deltaCompressor.Compress(newDeltas)
	}

	_, exists := f.items[id]
	if len(newDeltas) > 0 {
		if !exists {
			f.queue = append(f.queue, id)
		}
		f.items[id] = newDeltas
		f.cond.Broadcast()
	} else if exists {
		// The compression step removed all deltas, so
		// we need to remove this from our map (extra items
		// in the queue are ignored if they are not in the
		// map).
		delete(f.items, id)
	}
	return nil
}

func (f *DeltaFIFO) Pop(process PopProcessFunc) (interface{}, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	for {
		for len(f.queue) == 0 {
			f.cond.Wait()
		}
		id := f.queue[0]
		f.queue = f.queue[1:]
		/*
			从f.items取出object，然后调用process函数进行处理。
		*/
		item, ok := f.items[id]
		if f.initialPopulationCount > 0 {
			f.initialPopulationCount--
		}
		if !ok {
			// Item may have been deleted subsequently.
			continue
		}
		delete(f.items, id)
		/*
			在创建informer时，就已经指定了Process函数
				==>/pkg/client/cache/controller.go
					==>func NewIndexerInformer
						==>cfg的Process
		*/
		err := process(item)
		if e, ok := err.(ErrRequeue); ok {
			f.addIfNotPresent(id, item)
			err = e.Err
		}
		// Don't need to copyDeltas here, because we're transferring
		// ownership to the caller.
		return item, err
	}
}
```


## 路线图
可以看到kube-controller-manager的list-watch是要比kubelet组件复杂得多的。 但其本质上都是对Reflect机制的使用。

1. controller都会向共享型Informer进行注册，如replicationcontroller向podInformer注册。

2. 首先type sharedInformerFactory struct中会记录着所有的共享型Informer，每一个Informer都会通过一个协程run起来。

3. 所有Informer的协程中都会有一个type Controller struct，其作用是构建一个Reflector，然后将watch到的资源放入fifo这个cache里面。

4. 这里的Reflector机制的store是一个type DeltaFIFO struct对象，Reflector保证只会把符合expectedType类型的对象存放到store中。 其数据源在func NewPodInformer中进行了声明。

5. 一个sharedIndexInformer中会生成多个type processorListener struct。

6. type Controller struct会把消息分发到各个listener中，listener类型是type processorListener struct。

7. type processorListener struct 的add函数负责将notify装进pendingNotifications。 而pop函数取出pendingNotifications的第一个nofify, 输入nextCh这个channel。 最后run函数则负责取出notify，然后根据notify的类型(增加、删除、更新)触发相应的处理函数。

8. ReplicationManager的worker会负责处理各种event，确保Pod副本数与rc规定的相同。

9. 对于每个pod 的change都会唤起replication controller在podInformer.AddEventHandler中注册的方法。

10. 观察到pod资源的变化，会去更新其对应的rc资源。 通过影响rc来变更pod的状态。

11. 更新完一个rc资源之后，把其放入queue workqueue.RateLimitingInterface中，等待ReplicationManager的worker的处理，确保Pod副本数与rc规定的相同。

12. 从Kubernetes 1.7开始，所有需要监控资源变化情况的调用均推荐使用Informer。 Informer提供了基于事件通知的只读缓存机制，可以注册资源变化的回调函数，并可以极大减少API的调用。









