# ControllerManager的List-Watch机制-2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  -[type ReplicationManager struct](#type-replicationmanager-struct)
  - [创建一个replication manager](#创建一个replication-manager)
  - [NewIndexerInformer](#newindexerinformer)
  - [type DeltaFIFO struct](#type-deltafifo-struct)
  - [确保Pod副本数与rc规定的相同](#确保pod副本数与rc规定的相同)
<!-- END MUNGE: GENERATED_TOC -->

本文主要讲解 **非共享型informer** ，主要是replication controller 是如何对rc资源进行List-Watch的。

## type ReplicationManager struct
```go
type ReplicationManager struct {
	/*
		访问apiserver的客户端
	*/
	kubeClient clientset.Interface
	/*
		pod操作函数的封装，在/pkg/controller/controller_utils.go里面定义实现
			==>type PodControlInterface interface
		提供Create/Delete Pod的操作接口。
	*/
	podControl controller.PodControlInterface

	// internalPodInformer is used to hold a personal informer.  If we're using
	// a normal shared informer, then the informer will be started for us.  If
	// we have a personal informer, we must start it ourselves.   If you start
	// the controller using NewReplicationManager(passing SharedInformer), this
	// will be null
	/*
		译：
			internalPodInformer 用于持有一个非共享的informer。
			如果我们使用一个普通的共享型informer，该informer是已经自动运行。
			如果我们使用一个非共享的informer，必须自己主动去start it。
			如果你使用NewReplicationManager(passing SharedInformer)去启动一个controller，internalPodInformer应该是一个nil值
	*/
	internalPodInformer cache.SharedIndexInformer

	// An rc is temporarily suspended after creating/deleting these many replicas.
	// It resumes normal action after observing the watch events for them.
	burstReplicas int //每次批量Create/Delete Pods时允许并发的最大数量。
	// To allow injection of syncReplicationController for testing.
	syncHandler func(rcKey string) error //真正执行Replica Sync的函数。

	// A TTLCache of pod creates/deletes each rc expects to see.
	/*
		维护每一个rc期望状态下的Pod的Uid Cache，并且提供了修正该Cache的接口。

		定义在/pkg/controller/controller_utils.go
			==>type UIDTrackingControllerExpectations struct
	*/
	expectations *controller.UIDTrackingControllerExpectations

	// A store of replication controllers, populated by the rcController
	rcStore cache.StoreToReplicationControllerLister //由rcController来填充和维护,resource rc的Indexer
	// Watches changes to all replication controllers
	/*
		rcController，监控所有rc resource的变化，实现rc同步的任务调度逻辑, watch到的change更新到rcStore中。
		==>定义在/pkg/client/cache/controller.go
			==>type Controller struct
	*/
	rcController *cache.Controller
	// A store of pods, populated by the podController
	podStore cache.StoreToPodLister //由podController来填充和维护,Pod的Indexer
	// Watches changes to all pods
	podController cache.ControllerInterface //监控所有pod绑定变化，实现pod同步的任务调度逻辑
	// podStoreSynced returns true if the pod store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	/*
		译：如果pod存储已至少同步一次，则podStoreSynced返回true。
		  作为成员添加到结构体以允许注入进行测试。
	*/
	podStoreSynced func() bool

	lookupCache *controller.MatchingCache //提供Pod和RC匹配信息的cache，以提高查询效率

	// Controllers that need to be synced
	/*
		用来存放待sync的rc resource，是一个RateLimit类型的queue。
	*/
	queue workqueue.RateLimitingInterface

	// garbageCollectorEnabled denotes if the garbage collector is enabled. RC
	// manager behaves differently if GC is enabled.
	garbageCollectorEnabled bool
}
```

## 创建一个replication manager
见/pkg/controller/replication/replication_controller.go，分析其流程如下：
1. 实体化了一个type ReplicationManager struct对象
2. rm.rcStore.Indexer, rm.rcController = cache.NewIndexerInformer，是replication controller 是对rc资源进行List-Watch的核心。 其中rcStore是缓存rc信息的，rcController是rc的控制器，注意概念，不要搞乱了。
3. 向共享型podinformer注册自身存在。

```go
// NewReplicationManager creates a replication manager
/*
	创建一个replication manager
*/
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

// newReplicationManager configures a replication manager with the specified event recorder
/*
	对比两类informer，本质上都是对reflector的封装
*/
func newReplicationManager(eventRecorder record.EventRecorder, podInformer cache.SharedIndexInformer, kubeClient clientset.Interface, resyncPeriod controller.ResyncPeriodFunc, burstReplicas int, lookupCacheSize int, garbageCollectorEnabled bool) *ReplicationManager {
	if kubeClient != nil && kubeClient.Core().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("replication_controller", kubeClient.Core().RESTClient().GetRateLimiter())
	}

	/*
		   实体化了一个type ReplicationManager struct对象
				1. 通过controller.NewUIDTrackingControllerExpectations配置expectations。
				2. 通过workqueue.NewNamedRateLimitingQueue配置queue。
				3. 配置rcStore, podStore, rcController, podController。
				4. 配置syncHandler为rm.syncReplicationController，syncReplicationController就是做核心工作的的方法，可以说Replica的自动维护都是由它来完成的。
	*/
	rm := &ReplicationManager{
		kubeClient: kubeClient,
		/*
			var _ PodControlInterface = &RealPodControl{}
			==>定义在/pkg/controller/controller_utils.go
			定义了pod的创建等操作
		*/
		podControl: controller.RealPodControl{
			KubeClient: kubeClient,
			Recorder:   eventRecorder,
		},
		burstReplicas: burstReplicas,
		expectations:  controller.NewUIDTrackingControllerExpectations(controller.NewControllerExpectations()),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "replicationmanager"), //这个是rc资源，不是rc控制器
		garbageCollectorEnabled: garbageCollectorEnabled,
	}

	/*
		rm.rcStore.Indexer, rm.rcController = cache.NewIndexerInformer

		Replication controller 中 Watch-List的实现！！

		cache.NewIndexerInformer定义在
			==>pkg/client/cache/controller.go
				==>func NewIndexerInformer

		rcStore是缓存rc信息的，rcController是rc的控制器
	*/
	rm.rcStore.Indexer, rm.rcController = cache.NewIndexerInformer(
		//这里定义了对资源rc进行watch-list的数据源
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return rm.kubeClient.Core().ReplicationControllers(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return rm.kubeClient.Core().ReplicationControllers(api.NamespaceAll).Watch(options)
			},
		},
		/*
			独享型资源rc
			rc资源，不是controller Replication
		*/
		&api.ReplicationController{},
		// TODO: Can we have much longer period here?
		FullControllerResyncPeriod,
		cache.ResourceEventHandlerFuncs{
			/*
				event会发给rc的Add Update Delete方法
				三个操作最后都会调用到func (rm *ReplicationManager) enqueueController(obj interface{})
			*/
			AddFunc:    rm.enqueueController,
			UpdateFunc: rm.updateRC,
			// This will enter the sync loop and no-op, because the controller has been deleted from the store.
			// Note that deleting a controller immediately after scaling it to 0 will not work. The recommended
			// way of achieving this is by performing a `stop` operation on the controller.
			/*
				译：这回进入一个死循环操作，因为此时resource rc已经从store中删除了。
					请注意，在将一个rc缩放为0后立即删除该rc将不起作用。
					推荐的方法是通过在控制器上执行“停止”操作。
			*/
			DeleteFunc: rm.enqueueController,
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

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

	/*
		rm.syncHandler = rm.syncReplicationController是ReplicationManager控制器的核心，
		完成Replica的自动维护
	*/
	rm.syncHandler = rm.syncReplicationController
	rm.podStoreSynced = rm.podController.HasSynced
	rm.lookupCache = controller.NewMatchingCache(lookupCacheSize)
	return rm
}
```

## NewIndexerInformer
1. 这个函数有两个返回结果，cache.Indexer和*Controller，输入参数为五个分别为lw、objectType、resyncPeriod、h和indexers，类型分别为cache.ListerWatcher、runtime.Object、time.Duration、ResourceEventHandler和cache.Indexers。
2. lw其实就是一个ListWatch，包含List函数和Watch函数，
3. objectType表示要watch的资源类型，
4. resyncPeriod是个时间间隔，
5. h是一个触发函数，当watch到资源以后，用这个函数去处理，它包括OnAdd、onUpdate和onUpdate三个函数，根据watch的类型相应触发这些函数。
6. 生成一个config， config包装了lw、objectType；还有一个cache（Queue）,cache的类型为DeltaFIFO,即是一个队列；最后还定义了一个Process函数。
6. 返回的clientState是本地缓存client，为了避免反复请求apiserver
7. 基于Config创建一个Controller。这个Controller在前面已经介绍过了，是对Reflector的封装。
8. informer只是包装了reflector。reflector的几个传入参数：c.config.ListerWatcher、c.config.ObjectType和c.config.Queue,Queue就是一个Cache。从之前的分析我们可以看到运行这个reflector以后，会调用ListerWatcher,然后把结果传入Cache。Cache的类型是type DeltaFIFO struct。

```go
// NewIndexerInformer returns a Indexer and a controller for populating the index
// while also providing event notifications. You should only used the returned
// Index for Get/List operations; Add/Modify/Deletes will cause the event
// notifications to be faulty.
/*
	NewIndexerInformer返回一个Indexer和一个controller来填充索引，同时提供event通知。
	本函数func NewIndexerInformer的返回值Indexer 只能被用于Get/List操作；
	而/Modify/Deletes 会导致event 通知机制报错
*/
//
// Parameters:
//  * lw is list and watch functions for the source of the resource you want to
//    be informed of.
//  * objType is an object of the type that you expect to receive.
//  * resyncPeriod: if non-zero, will re-list this often (you will get OnUpdate
//    calls, even if nothing changed). Otherwise, re-list will be delayed as
//    long as possible (until the upstream source closes the watch or times out,
//    or you stop the controller).
//  * h is the object you want notifications sent to.

/*
	参数：
		lw ListerWatcher，观察objType这类resource的wtach-list方法
		objType runtime.Object, 希望wtach的资源
		resyncPeriod time.Duration, 如果非零，会定期re-list。否则re-list会被尽可能地推迟（直到watch超时、stop controller或者the upstream source closes）
		h ResourceEventHandler，触发函数，当watch到资源以后，用这个函数去处理，即event发给谁
*/
//
func NewIndexerInformer(
	lw ListerWatcher,
	objType runtime.Object,
	resyncPeriod time.Duration,
	h ResourceEventHandler,
	indexers Indexers,
) (Indexer, *Controller) {
	// This will hold the client state, as we know it.
	clientState := NewIndexer(DeletionHandlingMetaNamespaceKeyFunc, indexers)

	// This will hold incoming changes. Note how we pass clientState in as a
	// KeyLister, that way resync operations will result in the correct set
	// of update/delete deltas.
	/*
		生成一个type DeltaFIFO struct fifo
		用来存在list-watch的结果
	*/
	fifo := NewDeltaFIFO(MetaNamespaceKeyFunc, nil, clientState)

	/*
		生成一个config，
		config包装了lw、objectType；
		还有一个cache（Queue）,cache的类型为DeltaFIFO,即是一个队列；
		最后还定义了一个Process函数
	*/
	cfg := &Config{
		Queue:            fifo,
		ListerWatcher:    lw,
		ObjectType:       objType,
		FullResyncPeriod: resyncPeriod,
		RetryOnError:     false,

		/*
			Process的调用会在/pkg/client/cache/delta_fifo.go
				==>func (f *DeltaFIFO) Pop(process PopProcessFunc) (interface{}, error)
		*/
		Process: func(obj interface{}) error {
			/*
				实际上obj被存储在clientState里面。
				在对obj进行Add、Update、Delete时，会触发onAdd、onUpdate、onDelete。
			*/
			// from oldest to newest
			for _, d := range obj.(Deltas) {
				switch d.Type {
				case Sync, Added, Updated:
					if old, exists, err := clientState.Get(d.Object); err == nil && exists {
						if err := clientState.Update(d.Object); err != nil {
							return err
						}
						h.OnUpdate(old, d.Object)
					} else {
						if err := clientState.Add(d.Object); err != nil {
							return err
						}
						h.OnAdd(d.Object)
					}
				case Deleted:
					if err := clientState.Delete(d.Object); err != nil {
						return err
					}
					h.OnDelete(d.Object)
				}
			}
			return nil
		},
	}
	/*
		返回的clientState是本地缓存client，为了避免反复请求apiserver

		New(cfg)返回一个controller，
		深入去看该控制器run起来干了啥工作？
			==>/pkg/client/cache/controller.go
				==>func (c *Controller) Run(stopCh <-chan struct{})
	*/
	return clientState, New(cfg)
}
```

## type DeltaFIFO struct
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

// Update is just like Add, but makes an Updated Delta.
func (f *DeltaFIFO) Update(obj interface{}) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.populated = true
	return f.queueActionLocked(Updated, obj)
}

// Delete is just like Add, but makes an Deleted Delta. If the item does not
// already exist, it will be ignored. (It may have already been deleted by a
// Replace (re-list), for example.
/*
	如果该item已经不存在，该delete操作会被忽略
*/
func (f *DeltaFIFO) Delete(obj interface{}) error {
	id, err := f.KeyOf(obj)
	if err != nil {
		return KeyError{obj, err}
	}
	f.lock.Lock()
	defer f.lock.Unlock()
	f.populated = true
	if f.knownObjects == nil {
		if _, exists := f.items[id]; !exists {
			// Presumably, this was deleted when a relist happened.
			// Don't provide a second report of the same deletion.
			return nil
		}
	} else {
		// We only want to skip the "deletion" action if the object doesn't
		// exist in knownObjects and it doesn't have corresponding item in items.
		// Note that even if there is a "deletion" action in items, we can ignore it,
		// because it will be deduped automatically in "queueActionLocked"
		_, exists, err := f.knownObjects.GetByKey(id)
		_, itemsExist := f.items[id]
		if err == nil && !exists && !itemsExist {
			// Presumably, this was deleted when a relist happened.
			// Don't provide a second report of the same deletion.
			// TODO(lavalamp): This may be racy-- we aren't properly locked
			// with knownObjects.
			return nil
		}
	}

	return f.queueActionLocked(Deleted, obj)
}
```
可以发现Add、Update、Delete三个函数最后都调用了queueActionLocked，把处理的结果更新到f.items里面，相当于一个生产者！！ 其对应的消费者在 func (f *DeltaFIFO) Pop(process PopProcessFunc)。 这个和前面对pod资源的分析是类似的。
```go
// queueActionLocked appends to the delta list for the object, calling
// f.deltaCompressor if needed. Caller must lock first.
/*
	译：queueActionLocked附加到对象的增量列表中，
		如果需要，调用f.deltaCompressor。 Caller必须先执行锁操作。

	处理的结果会更新到f.items里面
*/
func (f *DeltaFIFO) queueActionLocked(actionType DeltaType, obj interface{}) error {
	/*
		最后处理的结果会更新到f.items里面
		相当于一个生产者！！
		对应的消费者在 func (f *DeltaFIFO) Pop(process PopProcessFunc)
	*/
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
```
Controller启动以后，运行了wait.Until(c.processLoop, time.Second, stopCh)。我们看看processLoop函数
```go
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
这是一个典型的生产者和消费者模型，reflector往fifo里面添加数据，而processLoop就不停去消费这里这些数据。cache.PopProcessFunc(c.config.Process)将前面Process函数传递进去。

`func (f *DeltaFIFO) Pop(process PopProcessFunc) (interface{}, error)`主要从f.items取出object，然后调用在创建informer时声明定义的process函数进行处理。
```go
		/*
			Process的调用会在/pkg/client/cache/delta_fifo.go
				==>func (f *DeltaFIFO) Pop(process PopProcessFunc) (interface{}, error)
		*/
		Process: func(obj interface{}) error {
			/*
				实际上obj被存储在clientState里面。
				在对obj进行Add、Update、Delete时，会触发onAdd、onUpdate、onDelete。
			*/
			// from oldest to newest
			for _, d := range obj.(Deltas) {
				switch d.Type {
				case Sync, Added, Updated:
					if old, exists, err := clientState.Get(d.Object); err == nil && exists {
						if err := clientState.Update(d.Object); err != nil {
							return err
						}
						h.OnUpdate(old, d.Object)
					} else {
						if err := clientState.Add(d.Object); err != nil {
							return err
						}
						h.OnAdd(d.Object)
					}
				case Deleted:
					if err := clientState.Delete(d.Object); err != nil {
						return err
					}
					h.OnDelete(d.Object)
				}
			}
			return nil
		},
```
实际上obj被存储在clientState里面。在对obj进行Add、Update、Delete时，会触发onAdd、onUpdate、onDelete。最终他们落到一下几个函数上面。

```go
cache.ResourceEventHandlerFuncs{
			/*
				event会发给rc的Add Update Delete方法
				三个操作最后都会调用到func (rm *ReplicationManager) enqueueController(obj interface{})
			*/
			AddFunc:    rm.enqueueController,
			UpdateFunc: rm.updateRC,
			// This will enter the sync loop and no-op, because the controller has been deleted from the store.
			// Note that deleting a controller immediately after scaling it to 0 will not work. The recommended
			// way of achieving this is by performing a `stop` operation on the controller.
			/*
				译：这回进入一个死循环操作，因为此时resource rc已经从store中删除了。
					请注意，在将一个rc缩放为0后立即删除该rc将不起作用。
					推荐的方法是通过在控制器上执行“停止”操作。
			*/
			DeleteFunc: rm.enqueueController,
		},
```

## 确保Pod副本数与rc规定的相同
obj最终会调用rm.enqueueController,会把obj的key假如到replicationmanager的queue里面。
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
又是一个生产者与消费者模型，replicationmanager创建了五个worker去消费添加的key。syncHandler是个重要的函数，由他负责pod与rc的同步，确保Pod副本数与rc规定的相同。
```go
// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
/*
	译：func (rm *ReplicationManager) worker() 运行一个worker线程，只需将items排队，处理它们并将其标记完毕。
	   func (rm *ReplicationManager) worker() 强制syncHandler从不与同一个键并发调用。
*/
func (rm *ReplicationManager) worker() {
	workFunc := func() bool {
		key, quit := rm.queue.Get()
		if quit {
			return true
		}
		defer rm.queue.Done(key)

		/*
			syncHandler是个重要的函数，负责pod与rc的同步，确保Pod副本数与rc规定的相同。
			rm.syncHandler = rm.syncReplicationController
				=>func (rm *ReplicationManager) syncReplicationController(key string) error
		*/
		err := rm.syncHandler(key.(string))
		if err == nil {
			rm.queue.Forget(key)
			return false
		}

		rm.queue.AddRateLimited(key)
		utilruntime.HandleError(err)
		return false
	}
	for {
		if quit := workFunc(); quit {
			glog.Infof("replication controller worker shutting down")
			return
		}
	}
}

// syncReplicationController will sync the rc with the given key if it has had its expectations fulfilled, meaning
// it did not expect to see any more of its pods created or deleted. This function is not meant to be invoked
// concurrently with the same key.
/*
	译：syncReplicationController将同步rc与指定的key，
		如果该rc已经满足了它的期望，这意味着它不再看到任何更多的pod创建或删除。
		不能用同一个key来同时唤醒本函数。
*/
func (rm *ReplicationManager) syncReplicationController(key string) error {
	/*
		入参key 可以看作是一个rc
	*/
	trace := util.NewTrace("syncReplicationController: " + key)
	defer trace.LogIfLong(250 * time.Millisecond)

	startTime := time.Now()
	defer func() {
		glog.V(4).Infof("Finished syncing controller %q (%v)", key, time.Now().Sub(startTime))
	}()

	if !rm.podStoreSynced() {
		// Sleep so we give the pod reflector goroutine a chance to run.
		time.Sleep(PodStoreSyncedPollPeriod)
		glog.Infof("Waiting for pods controller to sync, requeuing rc %v", key)
		rm.queue.Add(key)
		return nil
	}

	obj, exists, err := rm.rcStore.Indexer.GetByKey(key)
	if !exists {
		glog.Infof("Replication Controller has been deleted %v", key)
		rm.expectations.DeleteExpectations(key)
		return nil
	}
	if err != nil {
		return err
	}
	rc := *obj.(*api.ReplicationController)

	// Check the expectations of the rc before counting active pods, otherwise a new pod can sneak in
	// and update the expectations after we've retrieved active pods from the store. If a new pod enters
	// the store after we've checked the expectation, the rc sync is just deferred till the next relist.
	rcKey, err := controller.KeyFunc(&rc)
	if err != nil {
		glog.Errorf("Couldn't get key for replication controller %#v: %v", rc, err)
		return err
	}
	trace.Step("ReplicationController restored")
	rcNeedsSync := rm.expectations.SatisfiedExpectations(rcKey)
	trace.Step("Expectations restored")

	// NOTE: filteredPods are pointing to objects from cache - if you need to
	// modify them, you need to copy it first.
	// TODO: Do the List and Filter in a single pass, or use an index.
	var filteredPods []*api.Pod
	if rm.garbageCollectorEnabled {
		// list all pods to include the pods that don't match the rc's selector
		// anymore but has the stale controller ref.
		pods, err := rm.podStore.Pods(rc.Namespace).List(labels.Everything())
		if err != nil {
			glog.Errorf("Error getting pods for rc %q: %v", key, err)
			rm.queue.Add(key)
			return err
		}
		cm := controller.NewPodControllerRefManager(rm.podControl, rc.ObjectMeta, labels.Set(rc.Spec.Selector).AsSelectorPreValidated(), getRCKind())
		matchesAndControlled, matchesNeedsController, controlledDoesNotMatch := cm.Classify(pods)
		for _, pod := range matchesNeedsController {
			err := cm.AdoptPod(pod)
			// continue to next pod if adoption fails.
			if err != nil {
				// If the pod no longer exists, don't even log the error.
				if !errors.IsNotFound(err) {
					utilruntime.HandleError(err)
				}
			} else {
				matchesAndControlled = append(matchesAndControlled, pod)
			}
		}
		filteredPods = matchesAndControlled
		// remove the controllerRef for the pods that no longer have matching labels
		var errlist []error
		for _, pod := range controlledDoesNotMatch {
			err := cm.ReleasePod(pod)
			if err != nil {
				errlist = append(errlist, err)
			}
		}
		if len(errlist) != 0 {
			aggregate := utilerrors.NewAggregate(errlist)
			// push the RC into work queue again. We need to try to free the
			// pods again otherwise they will stuck with the stale
			// controllerRef.
			rm.queue.Add(key)
			return aggregate
		}
	} else {
		pods, err := rm.podStore.Pods(rc.Namespace).List(labels.Set(rc.Spec.Selector).AsSelectorPreValidated())
		if err != nil {
			glog.Errorf("Error getting pods for rc %q: %v", key, err)
			rm.queue.Add(key)
			return err
		}
		filteredPods = controller.FilterActivePods(pods)
	}

	var manageReplicasErr error
	if rcNeedsSync && rc.DeletionTimestamp == nil {
		manageReplicasErr = rm.manageReplicas(filteredPods, &rc)
	}
	trace.Step("manageReplicas done")

	newStatus := calculateStatus(rc, filteredPods, manageReplicasErr)

	// Always updates status as pods come up or die.
	if err := updateReplicationControllerStatus(rm.kubeClient.Core().ReplicationControllers(rc.Namespace), rc, newStatus); err != nil {
		// Multiple things could lead to this update failing.  Returning an error causes a requeue without forcing a hotloop
		return err
	}

	return manageReplicasErr
}
```

至此，对非共享型informer，rc资源进行List-Watch基本清楚。注意和前面对共享型Informer pod资源进行List-Watch进行对比。

1. 这边要处理的信息obj是一个个rc资源，而那边要处理的信息obj则是一个个pod资源。
2. 两边都是用了type DeltaFIFO struct作为List-Watch的cache，存储watch得到的信息。
3. 注意两边在对obj进行Add、Update、Delete时，触发函数根本不是同一个函数，别搞乱了。
4. 关于如何维护保持一个rc名下的pod数量始终与期望状态一致，会在后面[控制器ReplicationManager分析]()一文中详细分析。