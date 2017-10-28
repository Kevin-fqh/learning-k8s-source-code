# Client端的List-Watch机制-kubelet

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [kubelet获取Apiserver的数据](#kubelet获取apiserver的数据)
  - [func NewListWatchFromClient](#func-newlistwatchfromclient)
  - [func newSourceApiserverFromLW](#func-newsourceapiserverfromlw)
  - [type UndeltaStore struct](#type-undeltastore-struct)
  - [Reflector部分](#reflector部分)

<!-- END MUNGE: GENERATED_TOC -->

在Apiserver的介绍中，已经介绍了Server端对etcd的List-Watch机制运行路线图。 
那么Kubelt这些Client端是如何从Apiserver获取数据的呢？ 
本文以kubelt获取pod资源为例子，介绍kubelet是如何从Apiserver获取数据的。

## kubelet获取Apiserver的数据
kubelet获取pod的来源有apiserver、http和file， 获取apiserver数据源的`func NewSourceApiserver`方法定义在`/kubernetes-1.5.2/pkg/kubelet/config/apiserver.go`。

分析`func NewSourceApiserver`的流程如下：
1. 调用cache.NewListWatchFromClient函数，从而构建一个type ListWatch struct，该struct实现了type ListerWatcher interface。
2. 第一个参数c.CoreClient，这个client就是apiserver的client（数据源头，可以简单理解为kubelet list-watch的数据源就是apiserver）
3. "pods"指需要watch的资源是pods
4. api.NamespaceAll指对所有namespaces下的pod都感兴趣
5. fieldSelector 过滤函数，只对api.PodHostField=nodeName的pod感兴趣，也就是只对scheduler到本节点上的Pod感兴趣
6. func newSourceApiserverFromLW负责从apiserver拉取数据。这个函数是Kubelet组件List-Watch机制的重点。

```go
// NewSourceApiserver creates a config source that watches and pulls from the apiserver.
func NewSourceApiserver(c *clientset.Clientset, nodeName types.NodeName, updates chan<- interface{}) {
	/*
		调用/pkg/client/cache/listwatch.go的func NewListWatchFromClient(c Getter, resource string, namespace string, fieldSelector fields.Selector) *ListWatch
		构建一个type ListWatch struct，该struct实现了type ListerWatcher interface
	*/
	lw := cache.NewListWatchFromClient(c.Core().RESTClient(), "pods", api.NamespaceAll, fields.OneTermEqualSelector(api.PodHostField, string(nodeName)))
	newSourceApiserverFromLW(lw, updates)
}
```

## func NewListWatchFromClient
func NewListWatchFromClient用于构建一个type ListWatch struct， 见`/pkg/client/cache/listwatch.go`。 分析其流程如下：
1. 定义了 listFunc 和 watchFunc， 用于生成一个type ListWatch struct对象。
2. 都是利用定义在 /pkg/client/restclient/client.go 的 type RESTClient struct 来进行操作，关于RESTClient的用法在介绍kubectl的过程中已经介绍过了。
```go
// NewListWatchFromClient creates a new ListWatch from the specified client, resource, namespace and field selector.
/*
译：func NewListWatchFromClient从指定的client, resource, namespace and field selector创建一个新的ListWatch。

	对于kubelet而言，
	第一个参数c.CoreClient，这个client就是apiserver的client（数据源头，
	可以简单理解为kubelet list-watch的数据源就是apiserver）
*/
func NewListWatchFromClient(c Getter, resource string, namespace string, fieldSelector fields.Selector) *ListWatch {
	listFunc := func(options api.ListOptions) (runtime.Object, error) {
		/*
			发起一个Get请求,定义在/pkg/client/restclient/client.go
				==>func (c *RESTClient) Get() *Request
			设置request的namespace、resource字段
			VersionedParams主要对options进行序列化，options主要包括ResourceVersion和TimeoutSeconds这两个参数
			FieldsSelectorParam函数主要将filter函数进行序列化

			将一个request序列化到一个嵌套的map里面。
			Do()函数发起真正的请求，并收到response，然后用r.transformResponse去处理response，包装成Result返回。
			Get()函数则主要对Result进行反序列化。最后返回结果
		*/
		return c.Get().
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, api.ParameterCodec).
			FieldsSelectorParam(fieldSelector).
			Do().
			Get()
	}
	watchFunc := func(options api.ListOptions) (watch.Interface, error) {
		/*
			Prefix("watch")主要在pathPrefix的结尾增加了watch字段，用来和List请求区分，
			因为List和Watch都是作为Get请求发送出去。

			Watch()函数定义在/pkg/client/restclient/request.go
				==>func (r *Request) Watch() (watch.Interface, error)
			Watch()函数会return一个watch.Interface，这个watch.Interface专门用来传送kubelet想要watch的resource，
			获得watcher以后，reflector会调用r.watchHandler(w, &resourceVersion, resyncerrc, stopCh)去处理这个watcher
			这可以从/pkg/kubelet/config/apiserver.go中的cache.NewReflector(lw, &api.Pod{}, cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc), 0).Run()开始研究
		*/
		return c.Get().
			Prefix("watch").
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, api.ParameterCodec).
			FieldsSelectorParam(fieldSelector).
			Watch()
	}
	return &ListWatch{ListFunc: listFunc, WatchFunc: watchFunc}
}
```

- type ListWatch struct  

关于 type ListWatch struct 的定义如下：
```go
// ListerWatcher is any object that knows how to perform an initial list and start a watch on a resource.
type ListerWatcher interface {
	// List should return a list type object; the Items field will be extracted, and the
	// ResourceVersion field will be used to start the watch in the right place.
	List(options api.ListOptions) (runtime.Object, error)
	// Watch should begin a watch at the specified version.
	Watch(options api.ListOptions) (watch.Interface, error)
}

// ListFunc knows how to list resources
type ListFunc func(options api.ListOptions) (runtime.Object, error)

// WatchFunc knows how to watch resources
type WatchFunc func(options api.ListOptions) (watch.Interface, error)

// ListWatch knows how to list and watch a set of apiserver resources.  It satisfies the ListerWatcher interface.
// It is a convenience function for users of NewReflector, etc.
// ListFunc and WatchFunc must not be nil
/*
	type ListWatch struct 实现了type ListerWatcher interface
	对用户来说新建一个Reflector，使用它是很方便的
*/
type ListWatch struct {
	ListFunc  ListFunc
	WatchFunc WatchFunc
}
```

## func newSourceApiserverFromLW

func newSourceApiserverFromLW利用上面生成的type ListWatch struct对象，从apiserver拉取数据。 分析其流程如下：
1. 定义了一个send函数面值，主要是将List到的Pod发送到updates这个channel，kubelet会从updates这个channel获取到Pod信息，进行处理。
2. 调用cache.NewUndeltaStore，从这开始将会和Apiserver的List-Watch复用部分代码。
3. 需要注意的是这里kubelet list-watch的数据源就是apiserver；而Apiserver的数据源则是etcd。 记住这个差异，这将会导致后面在分析func (r *Reflector) watchHandler 函数时候针对不同组件所存在的差异。
4.  生成了一个UndeltaStore，这个是Reflector的数据源

```go
// newSourceApiserverFromLW holds creates a config source that watches and pulls from the apiserver.
func newSourceApiserverFromLW(lw cache.ListerWatcher, updates chan<- interface{}) {
	send := func(objs []interface{}) {
		var pods []*api.Pod
		for _, o := range objs {
			pods = append(pods, o.(*api.Pod))
		}
		updates <- kubetypes.PodUpdate{Pods: pods, Op: kubetypes.SET, Source: kubetypes.ApiserverSource}
	}
	/*
		cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc)定义在
			==>/pkg/client/cache/undelta_store.go
				==>func NewUndeltaStore(pushFunc func([]interface{}), keyFunc KeyFunc) *UndeltaStore
	*/
	cache.NewReflector(lw, &api.Pod{}, cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc), 0).Run()
}
```

## type UndeltaStore struct
Reflector的数据源
```go
// UndeltaStore listens to incremental updates and sends complete state on every change.
// It implements the Store interface so that it can receive a stream of mirrored objects
// from Reflector.  Whenever it receives any complete (Store.Replace) or incremental change
// (Store.Add, Store.Update, Store.Delete), it sends the complete state by calling PushFunc.
// It is thread-safe.  It guarantees that every change (Add, Update, Replace, Delete) results
// in one call to PushFunc, but sometimes PushFunc may be called twice with the same values.
// PushFunc should be thread safe.
type UndeltaStore struct {
	Store
	PushFunc func([]interface{})
}

// NewUndeltaStore returns an UndeltaStore implemented with a Store.
func NewUndeltaStore(pushFunc func([]interface{}), keyFunc KeyFunc) *UndeltaStore {
	return &UndeltaStore{
		/*
			根据入参keyFunc KeyFunc调用NewStore
		*/
		Store:    NewStore(keyFunc),
		PushFunc: pushFunc,
	}
}
```

## Reflector部分
func NewReflector创建一个新的type Reflector struct对象，Reflector会保持‘store中存储的expectedType’和etcd端的内容同步更新。

Reflector保证只会把符合expectedType类型的对象存放到store中，除非expectedType的值为nil。

如果resyncPeriod非0，那么list操作会间隔resyncPeriod执行一次，所以可以使用reflectors周期性处理所有的数据、后续更新。

```go
// NewReflector creates a new Reflector object which will keep the given store up to
// date with the server's contents for the given resource. Reflector promises to
// only put things in the store that have the type of expectedType, unless expectedType
// is nil. If resyncPeriod is non-zero, then lists will be executed after every
// resyncPeriod, so that you can use reflectors to periodically process everything as
// well as incrementally processing the things that change.

func NewReflector(lw ListerWatcher, expectedType interface{}, store Store, resyncPeriod time.Duration) *Reflector {
	return NewNamedReflector(getDefaultReflectorName(internalPackages...), lw, expectedType, store, resyncPeriod)
}
```
```go
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

- Reflector会Run起来

这里会调用func (r *Reflector) ListAndWatch(stopCh <-chan struct{}) error， 注意各个组件之间开始复用一部分代码。 
主要是根据Reflector的store来进行区分。
func (r *Reflector) Run()开启一个watch，处理watch events；也会重启该watch如果该watch被关闭了。
```go
// Run starts a watch and handles watch events. Will restart the watch if it is closed.
// Run starts a goroutine and returns immediately.

func (r *Reflector) Run() {
	glog.V(3).Infof("Starting reflector %v (%s) from %s", r.expectedType, r.resyncPeriod, r.name)
	go wait.Until(func() {
		/*
			调用func (r *Reflector) ListAndWatch(stopCh <-chan struct{}) error
			注意各个组件之间开始复用一部分代码
		*/
		if err := r.ListAndWatch(wait.NeverStop); err != nil {
			utilruntime.HandleError(err)
		}
	}, r.period, wait.NeverStop)
}
```

关于Reflector的ListAndWatch的分析在Apiserver端已经进行了介绍，只是数据源不一样。 其核心流程还是： 
1. 执行list操作
2. 执行watch操作
3. 调用func (r *Reflector) watchHandler  

在func (r *Reflector) watchHandler 中，有下面部分代码
```go
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
```

所以我们来分析`func (u *UndeltaStore) Add(obj interface{})`，见`/kubernetes-1.5.2/pkg/client/cache/undelta_store.go`
```go
/*
	这里我们可以发现无论是add、delete或者modify，
	u.PushFunc(u.Store.List())他会发送存储的所有pods。
	因为在这些操作之前，它都会先操作Store里面的pods对象，确保Store里面存储的是分配到该节点的Pod的最新信息
*/
func (u *UndeltaStore) Add(obj interface{}) error {
	/*
		操作实际的Store，定义在是pkg/client/cache/store.go
			==>func (c *cache) Add(obj interface{}) error
	*/
	if err := u.Store.Add(obj); err != nil {
		return err
	}
	/*
		执行u.PushFunc(u.Store.List())操作，
		PushFunc函数就是/pkg/kubelet/config/apiserver.go
			cache.NewUndeltaStore(send, cache.MetaNamespaceKeyFunc)里面的send函数

		主要是将List到的Pod发送到updates这个channel，
		kubelet会从updates这个channel获取到Pod信息，进行处理。
	*/
	u.PushFunc(u.Store.List())
	return nil
}
```

至此，kubelet获取到了Apiserver中的pod数据，kubelet会把三个来源（file、http、Apiserver）的数据进行合并，然后再有dockermanager这些组件根据期望状态进行操作。

