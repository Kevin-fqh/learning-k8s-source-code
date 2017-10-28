# Apiserver端List-Watch机制-1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引子](#引子)
  - [type watchCache struct](#type-watchcache-struct)
  - [type cacherListerWatcher struct](#type-cacherlisterwatcher-struct)
  - [type Cacher struct](#type-cacher-struct)
  - [type Reflector struct](#type-reflector-struct)
  - [启动Cacher](#启动cacher)
  - [Event流向各个订阅者的watcher](#event流向各个订阅者的watcher)


<!-- END MUNGE: GENERATED_TOC -->

Apiserver针对每一类资源(pod、service、replication controller),都会与etcd建立一个连接，获取该资源的opt。
Watch功能就是其中的一个opt。

什么是watch?
kubelet、kube-controller-manager、kube-scheduler需要监控各种资源(pod、service等)的变化，
当这些对象发生变化时(add、delete、update)，kube-apiserver能够主动通知这些组件。这是Client端的Watch实现。

而Apiserver端的Watch机制是建立在etcd的Watch基础上的。
etcd的watch是没有过滤功能的，而kube-apiserver增加了过滤功能。

什么是过滤功能？
比如说kubelet只对调度到本节点上的pod感兴趣，也就是pod.host=node1；
而kube-scheduler只对未被调度的pod感兴趣，也就是pod.host=”“。
etcd只能watch到pod的add、delete、update。
kube-apiserver则增加了过滤功能，将订阅方感兴趣的部分资源发给订阅方。

Event数据流向如下：
1. 从etcd-->Cacher，是一个watchCache，存储apiserver从etcd那里watch到的对象。
2. 结合etcd和Cacher的resourceVersion进行对比，形成一个WatchEvent，分发到各个观察者watcher中

## 引子
前面/pkg/storage/cacher.go提到`func NewCacherFromConfig`根据给定的配置，创建一个新的Cacher，负责服务WATCH和LIST内部缓存请求，并在后台更新缓存。分析其流程，如下：
1. 新建一个watchCache，用来存储apiserver从etcd那里watch到的对象
2. 新建一个listerWatcher
3. 实例化一个type Cacher struct对象，其核心是reflector机制
4. 启动dispatchEvents协程，分发event到各个订阅方
5. cacher.startCaching(stopCh)

```go
func NewCacherFromConfig(config CacherConfig) *Cacher {
	/*
		新建一个watchCache，用来存储apiserver从etcd那里watch到的对象
	*/
	watchCache := newWatchCache(config.CacheCapacity, config.KeyFunc)
	/*
		对config.Storage进行list和watch
		config.Storage是数据源（可以简单理解为etcd、带cache的etcd），一个资源的etcd handler
	*/
	listerWatcher := newCacherListerWatcher(config.Storage, config.ResourcePrefix, config.NewListFunc)

	// Give this error when it is constructed rather than when you get the
	// first watch item, because it's much easier to track down that way.
	/*
		译：在构造时给出错误，而不是在第一次去watch该item时。因为这种方式更容易跟踪。

		编码器进行类型检查
	*/
	if obj, ok := config.Type.(runtime.Object); ok {
		if err := runtime.CheckCodec(config.Codec, obj); err != nil {
			panic("storage codec doesn't seem to match given type: " + err.Error())
		}
	}

	/*
		Cacher接口必然也实现了storage.Interface接口所需要的方法。
		因为该Cacher只用于WATCH和LIST的request，
		所以可以看下cacher提供的API,除了WATCH和LIST相关的之外的接口都是调用了之前创建的storage的API。

		四个重要的成员：storage、watchCache、reflector、watchers
	*/
	cacher := &Cacher{
		ready: newReady(),
		//config.Storage就是和etcd建立连接后返回该资源的handler
		storage:    config.Storage,
		objectType: reflect.TypeOf(config.Type),
		//watchCache用来存储apiserver从etcd那里watch到的对象
		watchCache: watchCache,
		/*
			reflector这个对象，包含两个重要的数据成员listerWatcher和watchCache,
			而listerWatcher包装了config.Storage，会对storage进行list和watch。
			reflector工作主要是将watch到的config.Type类型的对象存放到watcherCache中。
			==>定义在/pkg/client/cache/reflector.go
				==>func NewReflector
		*/
		reflector: cache.NewReflector(listerWatcher, config.Type, watchCache, 0),
		//Versioner控制resource的版本
		versioner:   config.Versioner,
		triggerFunc: config.TriggerPublisherFunc,
		watcherIdx:  0,
		/*
			allWatchers、valueWatchers 都是一个map，map的值类型为cacheWatcher，
			当kubelet、kube-scheduler需要watch某类资源时，
			他们会向kube-apiserver发起watch请求，kube-apiserver就会生成一个cacheWatcher，
			他们负责将watch的资源通过http从apiserver传递到kubelet、kube-scheduler
				==>event分发功能是在下面的 go cacher.dispatchEvents()中完成

			watcher是kube-apiserver watch的发布方和订阅方的枢纽
			watchers是在哪里注册添加成员的？？?
				==>func newCacheWatcher(resourceVersion uint64, chanSize int, initEvents []watchCacheEvent, filter filterObjectFunc, forget func(bool)) *cacheWatcher {
		*/
		watchers: indexedWatchers{
			allWatchers:   make(map[int]*cacheWatcher),
			valueWatchers: make(map[string]watchersMap),
		},
		// TODO: Figure out the correct value for the buffer size.
		/*
			incoming会被分发到 watchers中

			这个要和/pkg/storage/etcd/etcd_watcher.go中的channel etcdIncoming进行区分，两者不是一个通道
		*/
		incoming: make(chan watchCacheEvent, 100),
		// We need to (potentially) stop both:
		// - wait.Until go-routine
		// - reflector.ListAndWatch
		// and there are no guarantees on the order that they will stop.
		// So we will be simply closing the channel, and synchronizing on the WaitGroup.
		stopCh: make(chan struct{}),
	}
	/*
		设置watchCache的onEvent这个handler。
		cacher.processEvent是incoming chan watchCacheEvent的生产者
	*/
	watchCache.SetOnEvent(cacher.processEvent)
	/*
		完成event分发功能，把event分发到对应的watchers中。
		是incoming chan watchCacheEvent的消费者
	*/
	go cacher.dispatchEvents()

	stopCh := cacher.stopCh
	cacher.stopWg.Add(1)
	go func() {
		defer cacher.stopWg.Done()
		wait.Until(
			func() {
				if !cacher.isStopped() {
					/*
						apiserver端，list-watch机制 V1.0
					*/
					cacher.startCaching(stopCh)
				}
			}, time.Second, stopCh,
		)
	}()
	return cacher
}
```

## type watchCache struct
结构体watchCache包含两个重要成员，cache和store。
- cache存储的是event(add、delete、update)。
- store则存储资源对象。

```go
// watchCache implements a Store interface.
// However, it depends on the elements implementing runtime.Object interface.
//
// watchCache is a "sliding window" (with a limited capacity) of objects
// observed from a watch.
type watchCache struct {
	/*
		结构体watchCache包含两个重要成员，cache和store。
		cache存储的是event(add、delete、update)
		store则存储资源对象
	*/
	sync.RWMutex

	// Condition on which lists are waiting for the fresh enough
	// resource version.
	cond *sync.Cond

	// Maximum size of history window.
	capacity int

	// keyFunc is used to get a key in the underlying storage for a given object.
	keyFunc func(runtime.Object) (string, error)

	// cache is used a cyclic buffer - its first element (with the smallest
	// resourceVersion) is defined by startIndex, its last element is defined
	// by endIndex (if cache is full it will be startIndex + capacity).
	// Both startIndex and endIndex can be greater than buffer capacity -
	// you should always apply modulo capacity to get an index in cache array.
	cache      []watchCacheElement
	startIndex int
	endIndex   int

	// store will effectively support LIST operation from the "end of cache
	// history" i.e. from the moment just after the newest cached watched event.
	// It is necessary to effectively allow clients to start watching at now.
	// NOTE: We assume that <store> is thread-safe.
	/*
		store cache.Store定义在/pkg/client/cache/store.go
			==>var _ Store = &cache{}
	*/
	store cache.Store

	// ResourceVersion up to which the watchCache is propagated.
	resourceVersion uint64

	// This handler is run at the end of every successful Replace() method.
	onReplace func()

	// This handler is run at the end of every Add/Update/Delete method
	// and additionally gets the previous value of the object.
	/*
		译：onEvent func(watchCacheEvent)是一个函数，这个handler会在每一个Add/Update/Delete method的最后运行，
			获取对象的前一个值
	*/
	onEvent func(watchCacheEvent)

	// for testing timeouts.
	clock clock.Clock
}
```

- func newWatchCache  
新建一个WatchCache
```go
func newWatchCache(capacity int, keyFunc func(runtime.Object) (string, error)) *watchCache {
	wc := &watchCache{
		capacity:   capacity,
		keyFunc:    keyFunc,
		cache:      make([]watchCacheElement, capacity),
		startIndex: 0,
		endIndex:   0,
		/*
			定义在/pkg/client/cache/store.go
				==>func NewStore(keyFunc KeyFunc) Store
		*/
		store:           cache.NewStore(storeElementKey),
		resourceVersion: 0,
		clock:           clock.RealClock{},
	}
	wc.cond = sync.NewCond(wc.RLocker())
	return wc
}
```

来看看`func NewStore`函数的实现
```go
/*
	Store和Indexer都是一个cache，其本质都是一个threadSafeStore。
	不同的是Store的Indexers参数为空，而Indexer的Indexers参数有值。
*/
// NewStore returns a Store implemented simply with a map and a lock.
func NewStore(keyFunc KeyFunc) Store {
	return &cache{
		cacheStorage: NewThreadSafeStore(Indexers{}, Indices{}),
		keyFunc:      keyFunc,
	}
}
```

实现了Add、Update、processEvent等一系列函数对cache中的event数据流进行处理。
这里先有个概念，后面用到的时候再进行详细的介绍。
```
func (w *watchCache) Add(obj interface{}) error
func (w *watchCache) Update(obj interface{}) error
func (w *watchCache) Delete(obj interface{}) error
...

func (c *Cacher) processEvent(event watchCacheEvent) {
	if curLen := int64(len(c.incoming)); c.incomingHWM.Update(curLen) {
		// Monitor if this gets backed up, and how much.
		glog.V(1).Infof("cacher (%v): %v objects queued in incoming channel.", c.objectType.String(), curLen)
	}
	/*
		type Cacher struct的channel incoming的生产者
		在/pkg/storage/watch_cache.go
			==>func (w *watchCache) Add(obj interface{}) ......
				==>func (w *watchCache) processEvent
					==>w.onEvent(watchCacheEvent)中完成真正的调用
	*/
	c.incoming <- event
}
```

## type cacherListerWatcher struct
cacherListerWatcher把不透明的storage.Interface暴露给cache.ListerWatcher。

type cacherListerWatcher struct实现了List和Watch方法，但其实都是调用定义在/pkg/storage/etcd/etcd_helper.go中的etcdHelper（满足storage interface）的List和Watch方法。
```go
// cacherListerWatcher opaques storage.Interface to expose cache.ListerWatcher.
type cacherListerWatcher struct {
	storage        Interface
	resourcePrefix string
	newListFunc    func() runtime.Object
}
```
- newCacherListerWatcher  

```go
func newCacherListerWatcher(storage Interface, resourcePrefix string, newListFunc func() runtime.Object) cache.ListerWatcher {
	return &cacherListerWatcher{
		storage:        storage,
		resourcePrefix: resourcePrefix,
		newListFunc:    newListFunc,
	}
}
```

## type Cacher struct
Cacher负责从其内部缓存提供给定资源的WATCH和LIST请求，并根据底层存储内容在后台更新其缓存。
Cacher实现storage.Interface（虽然大部分的调用只是委托给底层的存储）。
Cacher的核心是reflector机制。

Cacher接口必然也实现了storage.Interface接口所需要的方法。
因为该Cacher只用于WATCH和LIST的request，所以可以看下cacher提供的API,除了WATCH和LIST相关的之外的接口都是调用了之前创建的storage的API。

Cacher的四个重要的成员：storage、watchCache、reflector、watchers。
- storage，数据源（可以简单理解为etcd、带cache的etcd），一个资源的etcd handler
- watchCache，用来存储apiserver从etcd那里watch到的对象
- reflector，包含两个重要的数据成员listerWatcher和watchCache。reflector的工作主要是将watch到的config.Type类型的对象存放到watcherCache中。
- watchers， 当kubelet、kube-scheduler需要watch某类资源时，他们会向kube-apiserver发起watch请求，kube-apiserver就会生成一个cacheWatcher，他们负责将watch的资源通过http从apiserver传递到kubelet、kube-scheduler这些订阅方。watcher是kube-apiserver watch的发布方和订阅方的枢纽。

```go
// Cacher is responsible for serving WATCH and LIST requests for a given
// resource from its internal cache and updating its cache in the background
// based on the underlying storage contents.
// Cacher implements storage.Interface (although most of the calls are just
// delegated to the underlying storage).
 
type Cacher struct {
	// HighWaterMarks for performance debugging.
	// Important: Since HighWaterMark is using sync/atomic, it has to be at the top of the struct due to a bug on 32-bit platforms
	// See: https://golang.org/pkg/sync/atomic/ for more information
	incomingHWM HighWaterMark
	// Incoming events that should be dispatched to watchers.
	/** Incoming events 会被分发到watchers **/
	incoming chan watchCacheEvent

	sync.RWMutex

	// Before accessing the cacher's cache, wait for the ready to be ok.
	// This is necessary to prevent users from accessing structures that are
	// uninitialized or are being repopulated right now.
	// ready needs to be set to false when the cacher is paused or stopped.
	// ready needs to be set to true when the cacher is ready to use after
	// initialization.
	/*
		在访问cacher的cache之前，等待ready变成ok。
		这是必要的，以防止用户访问未初始化或正在重新填充的结构。
		当cacher被stop时，需要把ready设置成false
		当初始化之后准备开始使用cacher时，需要把ready设置为true
	*/
	ready *ready

	// Underlying storage.Interface.
	storage Interface

	// Expected type of objects in the underlying cache.
	objectType reflect.Type

	// "sliding window" of recent changes of objects and the current state.
	watchCache *watchCache
	reflector  *cache.Reflector

	// Versioner is used to handle resource versions.
	versioner Versioner

	// triggerFunc is used for optimizing amount of watchers that needs to process
	// an incoming event.
	triggerFunc TriggerPublisherFunc
	// watchers is mapping from the value of trigger function that a
	// watcher is interested into the watchers

	watcherIdx int
	watchers   indexedWatchers

	// Handling graceful termination.
	stopLock sync.RWMutex
	stopped  bool
	stopCh   chan struct{}
	stopWg   sync.WaitGroup
}
```

### type Reflector struct
Reflector主要是watch一个指定的resource，会把resource发生的任何变化反映到指定的store中。
- 定义
```go
// Reflector watches a specified resource and causes all changes to be reflected in the given store.
type Reflector struct {
	// name identifies this reflector. By default it will be a file:line if possible.
	name string

	// The type of object we expect to place in the store.
	expectedType reflect.Type
	// The destination to sync up with the watch source
	store Store
	// listerWatcher is used to perform lists and watches.
	/*
		listerWatcher用来进行list和watch操作
	*/
	listerWatcher ListerWatcher
	// period controls timing between one watch ending and
	// the beginning of the next one.
	period       time.Duration
	resyncPeriod time.Duration
	// now() returns current time - exposed for testing purposes
	now func() time.Time
	// lastSyncResourceVersion is the resource version token last
	// observed when doing a sync with the underlying store
	// it is thread safe, but not synchronized with the underlying store
	lastSyncResourceVersion string
	// lastSyncResourceVersionMutex guards read/write access to lastSyncResourceVersion
	lastSyncResourceVersionMutex sync.RWMutex
}
```

- 新建一个Reflector对象
```go
// NewReflector creates a new Reflector object which will keep the given store up to
// date with the server's contents for the given resource. Reflector promises to
// only put things in the store that have the type of expectedType, unless expectedType
// is nil. If resyncPeriod is non-zero, then lists will be executed after every
// resyncPeriod, so that you can use reflectors to periodically process everything as
// well as incrementally processing the things that change.
/*
	译：func NewReflector创建一个新的type Reflector struct对象，
		Reflector会保持‘store中存储的expectedType’和etcd端的内容同步更新。
		Reflector保证只会把符合expectedType类型的对象存放到store中，除非expectedType的值为nil。
		如果resyncPeriod非0，那么list操作会间隔resyncPeriod执行一次，
		所以可以使用reflectors周期性处理所有的数据、后续更新。
*/
func NewReflector(lw ListerWatcher, expectedType interface{}, store Store, resyncPeriod time.Duration) *Reflector {
	return NewNamedReflector(getDefaultReflectorName(internalPackages...), lw, expectedType, store, resyncPeriod)
}

// NewNamedReflector same as NewReflector, but with a specified name for logging
/*
	func NewNamedReflector和func NewReflector一样，but with a specified name for logging
*/
func NewNamedReflector(name string, lw ListerWatcher, expectedType interface{}, store Store, resyncPeriod time.Duration) *Reflector {
	r := &Reflector{
		name:          name,
		listerWatcher: lw,
		store:         store,
		expectedType:  reflect.TypeOf(expectedType),
		period:        time.Second,
		resyncPeriod:  resyncPeriod,
		now:           time.Now,
	}
	return r
}
```
关于type Reflector struct实现的方法在后面用到的时候会进行介绍。

## 启动Cacher
分析其流程如下：
1. 首先会通过terminateAllWatchers注销所有的cachewatcher,因为这个时候apiserver还处于初始化阶段，因此不可能接受其他组件的watch，也就不可能有watcher。
2. 然后调用c.reflector.ListAndWatch函数，完成前面说过的功能：reflector主要将apiserver组件从etcd中watch到的资源存储到watchCache中。

```go
func (c *Cacher) startCaching(stopChannel <-chan struct{}) {
	// The 'usable' lock is always 'RLock'able when it is safe to use the cache.
	// It is safe to use the cache after a successful list until a disconnection.
	// We start with usable (write) locked. The below OnReplace function will
	// unlock it after a successful list. The below defer will then re-lock
	// it when this function exits (always due to disconnection), only if
	// we actually got a successful list. This cycle will repeat as needed.
	/*
		译：在连接中断之前，在一个成功的lis操作之后使用cache是读写安全的
	*/
	successfulList := false
	c.watchCache.SetOnReplace(func() {
		successfulList = true
		c.ready.set(true)
	})
	defer func() {
		if successfulList {
			c.ready.set(false)
		}
	}()

	//终止所有的watcher
	c.terminateAllWatchers()
	// Note that since onReplace may be not called due to errors, we explicitly
	// need to retry it on errors under lock.
	// Also note that startCaching is called in a loop, so there's no need
	// to have another loop here.
	/*
		apiserver端，list-watch机制 V2.0
		func (c *Cacher) startCaching已经是在一个循环中被调用，所以这里不再有循环
		ListAndWatch(stopChannel)定义在/pkg/client/cache/reflector.go
			==>func (r *Reflector) ListAndWatch(stopCh <-chan struct{}) error
	*/
	if err := c.reflector.ListAndWatch(stopChannel); err != nil {
		glog.Errorf("unexpected ListAndWatch error: %v", err)
	}
}
```

- 调用Reflector的ListAndWatch  
分析其流程，如下：
  1. 执行list操作
  2. 执行watch操作
  3. 调用func (r *Reflector) watchHandler

```go
// ListAndWatch first lists all items and get the resource version at the moment of call,
// and then use the resource version to watch.
// It returns error if ListAndWatch didn't even try to initialize watch.
/*
	译：func (r *Reflector) ListAndWatch 首先会list所有的items，得到resource version；
		然后使用该resource version去watch。
		如果ListAndWatch没有尝试去初始化watch，返回error

	注意func (r *Reflector) ListAndWatch函数会被apiserver和kubelet等多个组件复用。
	区别： apiserver去watch etcd，而kubelet去watch apiserver
*/
func (r *Reflector) ListAndWatch(stopCh <-chan struct{}) error {
	glog.V(3).Infof("Listing and watching %v from %s", r.expectedType, r.name)
	var resourceVersion string
	resyncCh, cleanup := r.resyncChan()
	defer cleanup()

	// Explicitly set "0" as resource version - it's fine for the List()
	// to be served from cache and potentially be delayed relative to
	// etcd contents. Reflector framework will catch up via Watch() eventually.
	/*
		译：明确把resource version设置为"0"---这样子是适用于对cache进行 List()操作的，虽然可能会造成内容相对于
			etcd中的数据有所延迟。
		   Reflector框架是通过Watch()操作来追赶上来。
	*/
	options := api.ListOptions{ResourceVersion: "0"}
	/*
		apiserver端，list-watch机制 V3.0 ，List操作

		用resource version＝"0"来进行list操作，
		r.listerWatcher.List定义在/pkg/storage/cacher.go
			==>func (lw *cacherListerWatcher) List(options api.ListOptions)
	*/
	list, err := r.listerWatcher.List(options)
	if err != nil {
		return fmt.Errorf("%s: Failed to list %v: %v", r.name, r.expectedType, err)
	}
	/*
		获取该类型的List接口，定义在
		==>/pkg/api/meta/meta.go
			==>func ListAccessor(obj interface{}) (List, error)
	*/
	listMetaInterface, err := meta.ListAccessor(list)
	if err != nil {
		return fmt.Errorf("%s: Unable to understand list result %#v: %v", r.name, list, err)
	}
	resourceVersion = listMetaInterface.GetResourceVersion()
	items, err := meta.ExtractList(list)
	if err != nil {
		return fmt.Errorf("%s: Unable to understand list result %#v (%v)", r.name, list, err)
	}
	if err := r.syncWith(items, resourceVersion); err != nil {
		return fmt.Errorf("%s: Unable to sync list result: %v", r.name, err)
	}
	r.setLastSyncResourceVersion(resourceVersion)

	resyncerrc := make(chan error, 1)
	cancelCh := make(chan struct{})
	defer close(cancelCh)
	go func() {
		for {
			select {
			case <-resyncCh:
			case <-stopCh:
				return
			case <-cancelCh:
				return
			}
			glog.V(4).Infof("%s: forcing resync", r.name)
			if err := r.store.Resync(); err != nil {
				resyncerrc <- err
				return
			}
			cleanup()
			resyncCh, cleanup = r.resyncChan()
		}
	}()

	for {
		timemoutseconds := int64(minWatchTimeout.Seconds() * (rand.Float64() + 1.0))
		options = api.ListOptions{
			ResourceVersion: resourceVersion,
			// We want to avoid situations of hanging watchers. Stop any wachers that do not
			// receive any events within the timeout window.
			TimeoutSeconds: &timemoutseconds,
		}

		/*
			apiserver端，list-watch机制 V3.0 ，Watch操作
			定义在/pkg/storage/cacher.go
			==>func (lw *cacherListerWatcher) Watch(options api.ListOptions) (watch.Interface, error)

			生成一个watcher，该watcher实现了watch.Interface（用接口来让kubelet、apiserver复用该接口）
		*/
		w, err := r.listerWatcher.Watch(options)
		if err != nil {
			switch err {
			case io.EOF:
				// watch closed normally
			case io.ErrUnexpectedEOF:
				glog.V(1).Infof("%s: Watch for %v closed with unexpected EOF: %v", r.name, r.expectedType, err)
			default:
				utilruntime.HandleError(fmt.Errorf("%s: Failed to watch %v: %v", r.name, r.expectedType, err))
			}
			// If this is "connection refused" error, it means that most likely apiserver is not responsive.
			// It doesn't make sense to re-list all objects because most likely we will be able to restart
			// watch where we ended.
			// If that's the case wait and resend watch request.
			if urlError, ok := err.(*url.Error); ok {
				if opError, ok := urlError.Err.(*net.OpError); ok {
					if errno, ok := opError.Err.(syscall.Errno); ok && errno == syscall.ECONNREFUSED {
						time.Sleep(time.Second)
						continue
					}
				}
			}
			return nil
		}

		/*
			apiserver端，list-watch机制 V4.0
			把上面生成的watcher w传进去
			调用func (r *Reflector) watchHandler
		*/
		if err := r.watchHandler(w, &resourceVersion, resyncerrc, stopCh); err != nil {
			if err != errorStopRequested {
				glog.Warningf("%s: watch of %v ended with: %v", r.name, r.expectedType, err)
			}
			return nil
		}
	}
}
```

- cacherListerWatcher的List和Watch
```go
// Implements cache.ListerWatcher interface.
func (lw *cacherListerWatcher) List(options api.ListOptions) (runtime.Object, error) {
	list := lw.newListFunc()
	/*
		调用storage的List方法，定义在
		==>/pkg/storage/etcd/etcd_helper.go
			==>func (h *etcdHelper) List(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate, listObj runtime.Object) error
	*/
	if err := lw.storage.List(context.TODO(), lw.resourcePrefix, "", Everything, list); err != nil {
		return nil, err
	}
	return list, nil
}

// Implements cache.ListerWatcher interface.
func (lw *cacherListerWatcher) Watch(options api.ListOptions) (watch.Interface, error) {
	/*
		调用storage的WatchList方法，定义在
		==>/pkg/storage/etcd/etcd_helper.go
			==>func (h *etcdHelper) WatchList(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate)
	*/
	return lw.storage.WatchList(context.TODO(), lw.resourcePrefix, options.ResourceVersion, Everything)
}
```
然后调用的是etcdHelper的List和Watch
```go
// Implements storage.Interface.
func (h *etcdHelper) List(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate, listObj runtime.Object) error {
	if ctx == nil {
		glog.Errorf("Context is nil")
	}
	trace := util.NewTrace("List " + getTypeName(listObj))
	defer trace.LogIfLong(400 * time.Millisecond)
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return err
	}
	key = h.prefixEtcdKey(key)
	startTime := time.Now()
	trace.Step("About to list etcd node")
	nodes, index, err := h.listEtcdNode(ctx, key)
	trace.Step("Etcd node listed")
	metrics.RecordEtcdRequestLatency("list", getTypeName(listPtr), startTime)
	if err != nil {
		return err
	}
	if err := h.decodeNodeList(nodes, storage.SimpleFilter(pred), listPtr); err != nil {
		return err
	}
	trace.Step("Node list decoded")
	if err := h.versioner.UpdateList(listObj, index); err != nil {
		return err
	}
	return nil
}

// Implements storage.Interface.
func (h *etcdHelper) Watch(ctx context.Context, key string, resourceVersion string, pred storage.SelectionPredicate) (watch.Interface, error) {
	if ctx == nil {
		glog.Errorf("Context is nil")
	}
	watchRV, err := storage.ParseWatchResourceVersion(resourceVersion)
	if err != nil {
		return nil, err
	}
	key = h.prefixEtcdKey(key)
	w := newEtcdWatcher(false, h.quorum, nil, storage.SimpleFilter(pred), h.codec, h.versioner, nil, h)
	go w.etcdWatch(ctx, h.etcdKeysAPI, key, watchRV)
	return w, nil
}
```
可以发现这是建立在etcd的watch基础上的，关于etcd的watch，将在[EtcdWatcher]()一文中进行讲述。

- 启动reflect的watchHandler函数  
func watchHandler watches w and keeps *resourceVersion up to date。

将event对象从channel outgoing中读取出来，然后根据event.Type去操作r.store，即操作type watchCache struct。
```go
func (r *Reflector) watchHandler(w watch.Interface, resourceVersion *string, errc chan error, stopCh <-chan struct{}) error {
	start := time.Now()
	eventCount := 0

	// Stopping the watcher should be idempotent and if we return from this function there's no way
	// we're coming back in with the same watch interface.
	defer w.Stop()

loop:
	for {
		select {
		case <-stopCh:
			return errorStopRequested
		case err := <-errc:
			return err
		case event, ok := <-w.ResultChan():
			/*
				ResultChan()的返回值是channel outgoing  消费者
				==>定义在/pkg/storage/etcd/etcd_watcher.go
					==>func (w *etcdWatcher) ResultChan() <-chan watch.Event
			*/
			if !ok {
				break loop
			}
			if event.Type == watch.Error {
				return apierrs.FromObject(event.Object)
			}
			if e, a := r.expectedType, reflect.TypeOf(event.Object); e != nil && e != a {
				utilruntime.HandleError(fmt.Errorf("%s: expected type %v, but watch event object had type %v", r.name, e, a))
				continue
			}
			meta, err := meta.Accessor(event.Object)
			if err != nil {
				utilruntime.HandleError(fmt.Errorf("%s: unable to understand watch event %#v", r.name, event))
				continue
			}
			newResourceVersion := meta.GetResourceVersion()
			switch event.Type {
			case watch.Added:
				/*
					r.store的初始化是在/pkg/storage/cacher.go
						==>func NewCacherFromConfig(config CacherConfig) *Cacher
							==>watchCache := newWatchCache(config.CacheCapacity, config.KeyFunc)
					那么Add函数定义在/pkg/storage/watch_cache.go
						==>func (w *watchCache) Add(obj interface{}) error

					传进去的参数是event.Object，
					Add这些函数里面会将object重新包装成event

					上面的描述是针对apiserver端的，所以其r.store的初始化是func NewCacherFromConfig(config CacherConfig) *Cacher。
					但对于kubelet而言，其r.store的初始化和apiserver并不一样！
					****
					****
					kubelet的reflector的初始化是在
					==>/pkg/kubelet/config/apiserver.go
						==>func newSourceApiserverFromLW(lw cache.ListerWatcher, updates chan<- interface{})
							==>cache.NewReflector(lw, &api.Pod{}, cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc), 0).Run()
					其r.store是cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc)，
					类型是type UndeltaStore struct
					那么其Add函数也就是定义在/pkg/client/cache/undelta_store.go
						==>func (u *UndeltaStore) Add(obj interface{})

					可以比较以下两种store类型的Add函数的区别和相同之处：
						相同：最后都会调用定义在pkg/client/cache/store.go中的Add函数
								==>func (c *cache) Add(obj interface{}) error
				*/
				r.store.Add(event.Object)
			case watch.Modified:
				r.store.Update(event.Object)
			case watch.Deleted:
				// TODO: Will any consumers need access to the "last known
				// state", which is passed in event.Object? If so, may need
				// to change this.
				r.store.Delete(event.Object)
			default:
				utilruntime.HandleError(fmt.Errorf("%s: unable to understand watch event %#v", r.name, event))
			}
			*resourceVersion = newResourceVersion
			r.setLastSyncResourceVersion(newResourceVersion)
			eventCount++
		}
	}

	watchDuration := time.Now().Sub(start)
	if watchDuration < 1*time.Second && eventCount == 0 {
		glog.V(4).Infof("%s: Unexpected watch close - watch lasted less than a second and no items received", r.name)
		return errors.New("very short watch")
	}
	glog.V(4).Infof("%s: Watch close - %v total %v items received", r.name, r.expectedType, eventCount)
	return nil
}
```

这里需要注意的是r.store的初始化，apiserver和kubelet的初始化是不一样的，所以对应的r.store也不一样。

以apiserver的Add函数为例，定义在`pkg/storage/watch_cache.go`
```go
// Add takes runtime.Object as an argument.
func (w *watchCache) Add(obj interface{}) error {
	object, resourceVersion, err := objectToVersionedRuntimeObject(obj)
	if err != nil {
		return err
	}
	/*
		把入口参数object重新包装成event
	*/
	event := watch.Event{Type: watch.Added, Object: object}

	/*
		定义函数面值f， w.store.Add(elem)定义在是pkg/client/cache/store.go
			==>func (c *cache) Add(obj interface{}) error
	*/
	f := func(elem *storeElement) error { return w.store.Add(elem) }
	/*
		调用func (w *watchCache) processEvent
	*/
	return w.processEvent(event, resourceVersion, f)
}
```

- processEvent处理event  
继续查看processEvent函数，其流程如下：
  1. 将event封装成watchCacheEvent
  2. 调用w.onEvent(watchCacheEvent)，后面会通知对应的watcher
  3. 调用updateFunc(elem)往Cache里面insert一个Event,  这里的updateFunc就是上面Add函数的`f := func(elem *storeElement) error { return w.store.Add(elem) }`。 **也就是说这里完成了Event从etcd流向Cache。**

```go
func (w *watchCache) processEvent(event watch.Event, resourceVersion uint64, updateFunc func(*storeElement) error) error {
	key, err := w.keyFunc(event.Object)
	if err != nil {
		return fmt.Errorf("couldn't compute key: %v", err)
	}
	elem := &storeElement{Key: key, Object: event.Object}

	// TODO: We should consider moving this lock below after the watchCacheEvent
	// is created. In such situation, the only problematic scenario is Replace(
	// happening after getting object from store and before acquiring a lock.
	// Maybe introduce another lock for this purpose.
	w.Lock()
	defer w.Unlock()
	/*
		根据elem获取该enevt的previous
	*/
	previous, exists, err := w.store.Get(elem)
	if err != nil {
		return err
	}
	var prevObject runtime.Object
	if exists {
		prevObject = previous.(*storeElement).Object
	}
	/*
		将event封装成watchCacheEvent
	*/
	watchCacheEvent := watchCacheEvent{
		Type:            event.Type,
		Object:          event.Object,
		PrevObject:      prevObject, //和elem相关
		Key:             key,
		ResourceVersion: resourceVersion,
	}
	if w.onEvent != nil {
		/*
			调用onEvent函数面值，其赋值是在
				==>/pkg/storage/cacher.go
					==>func NewCacherFromConfig(config CacherConfig) *Cacher
						==>watchCache.SetOnEvent(cacher.processEvent)

			type Cacher struct的channel incoming的生产者
			其消费地方在于/pkg/storage/cacher.go
			==>func (c *Cacher) dispatchEvents()
		*/
		w.onEvent(watchCacheEvent)
	}
	w.updateCache(resourceVersion, &watchCacheEvent)
	w.resourceVersion = resourceVersion
	w.cond.Broadcast()
	/*
		调用updateFunc更新store，往cache里面insert一个Event
	*/
	return updateFunc(elem)
}
```

查看updateFunc里面的Add函数，在pkg/client/cache/store.go
```go
// Add inserts an item into the cache.
func (c *cache) Add(obj interface{}) error {
	key, err := c.keyFunc(obj)
	if err != nil {
		return KeyError{obj, err}
	}
	c.cacheStorage.Add(key, obj)
	return nil
}
```

至此Event从etcd流向WatchCache的过程已经基本清晰。

## Event流向各个订阅者的watcher
那么后面的重点就来到了`w.onEvent(watchCacheEvent)`，其最终是调用`/pkg/storage/cacher.go`中的`func (c *Cacher) processEvent(event watchCacheEvent)`。

这个在上面已经介绍过了，里面会把一个Event发送到channel incoming中，这是一个生产者。

那么对应的消费者在哪呢？在`func (c *Cacher) dispatchEvents()`中。
```go
func (c *Cacher) dispatchEvents() {
	for {
		select {
		/*
			type Cacher struct的channel incoming的消费者
		*/
		case event, ok := <-c.incoming:
			if !ok {
				return
			}
			c.dispatchEvent(&event)
		case <-c.stopCh:
			return
		}
	}
}

func (c *Cacher) dispatchEvent(event *watchCacheEvent) {
	/*
		获取event中的value、前一个event的value
	*/
	triggerValues, supported := c.triggerValues(event)

	// TODO: For now we assume we have a given <timeout> budget for dispatching
	// a single event. We should consider changing to the approach with:
	// - budget has upper bound at <max_timeout>
	// - we add <portion> to current timeout every second
	timeout := time.Duration(250) * time.Millisecond

	/*
	   RWMutex提供了四个方法：
	   func (*RWMutex) Lock  写锁定
	   func (*RWMutex) Unlock  写解锁
	   func (*RWMutex) RLock  读锁定
	   func (*RWMutex) RUnlock  读解锁
	*/
	c.Lock()
	defer c.Unlock()
	// Iterate over "allWatchers" no matter what the trigger function is.
	/*
		对Cacher中的watchers.allWatchers进行遍历，
		把event 发送到所有的watcher中
	*/
	for _, watcher := range c.watchers.allWatchers {
		watcher.add(event, &timeout)
	}
	if supported {
		// Iterate over watchers interested in the given values of the trigger.
		for _, triggerValue := range triggerValues {
			for _, watcher := range c.watchers.valueWatchers[triggerValue] {
				watcher.add(event, &timeout)
			}
		}
	} else {
		// supported equal to false generally means that trigger function
		// is not defined (or not aware of any indexes). In this case,
		// watchers filters should generally also don't generate any
		// trigger values, but can cause problems in case of some
		// misconfiguration. Thus we paranoidly leave this branch.

		// Iterate over watchers interested in exact values for all values.
		for _, watchers := range c.watchers.valueWatchers {
			for _, watcher := range watchers {
				watcher.add(event, &timeout)
			}
		}
	}
}
```

来看看`watcher.add(event, &timeout)`函数，是如何把event分发到一个`type cacheWatcher struct`的。
```go
func (c *cacheWatcher) add(event *watchCacheEvent, timeout *time.Duration) {
	// Try to send the event immediately, without blocking.
	/*
		channel取不到值时，走default通道，就是select语句结束，继续执行后续部分
		如果取到值，直接return
		这里完成event的分发，channel input的生产者，
		其对应的消费者在type cacheWatcher struct
		==>/pkg/storage/cacher.go
			==>func newCacheWatcher
				==>func (c *cacheWatcher) process(initEvents []watchCacheEvent, resourceVersion uint64)
	*/
	select {
	case c.input <- *event:
		return
	default:
	}

	// OK, block sending, but only for up to <timeout>.
	// cacheWatcher.add is called very often, so arrange
	// to reuse timers instead of constantly allocating.
	/*
		func (c *cacheWatcher) add会很频繁地被调用，设置了一个定时器
	*/
	startTime := time.Now()

	t, ok := timerPool.Get().(*time.Timer)
	if ok {
		t.Reset(*timeout)
	} else {
		t = time.NewTimer(*timeout)
	}
	defer timerPool.Put(t)

	select {
	case c.input <- *event:
		stopped := t.Stop()
		if !stopped {
			// Consume triggered (but not yet received) timer event
			// so that future reuse does not get a spurious timeout.
			<-t.C
		}
	case <-t.C:
		// This means that we couldn't send event to that watcher.
		// Since we don't want to block on it infinitely,
		// we simply terminate it.
		c.forget(false)
		c.stop()
	}

	if *timeout = *timeout - time.Since(startTime); *timeout < 0 {
		*timeout = 0
	}
}
```

至此可以说Event已经分发到了各个订阅者的watcher中了，后续各个Watcher组件会从channel input中获取到event。

至于，各个Watcher是怎么创建的，后面会进行介绍。
