# StorageWithCacher和UndecoratedStorage

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [UndecoratedStorage](#undecoratedstorage)
  - [StorageWithCacher定义](#storagewithcacher定义)
  - [NewRawStorage](#newrawstorage)
    - [newETCD2Storage](#newetcd2storage)
  - [type etcdHelper struct](#type-etcdhelper-struct)
  - [type CacherConfig struct](#type-cacherconfig-struct)
  - [func NewCacherFromConfig 创建cacher](#func-newcacherfromconfig-创建cacher)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

restOptionsFactory.storageDecorator是Apiserver和etcd连接的接口，其类型有两种：
- StorageWithCacher，通过一个缓存cache来间接操作etcd的接口
- UndecoratedStorage，不带cache，直接操作etcd的接口

StorageWithCacher和UndecoratedStorage的相同之处在于基于`RawStorage`来进行操作的。

## UndecoratedStorage
定义在/pkg/registry/generic/storage_decorator.go。可以发现其主要是调用了`NewRawStorage(config)`来创建一个RawStorage。

```go
// Returns given 'storageInterface' without any decoration.
func UndecoratedStorage(
	config *storagebackend.Config,
	capacity int,
	objectType runtime.Object,
	resourcePrefix string,
	scopeStrategy rest.NamespaceScopedStrategy,
	newListFunc func() runtime.Object,
	trigger storage.TriggerPublisherFunc) (storage.Interface, factory.DestroyFunc) {
	/*
		和定义在/pkg/registry/generic/registry/storage_factory.go中的func StorageWithCacher对比，可以发现：
		registry.StorageWithCacher()接口也是调用了NewRawStorage()接口，其实现就是少了cache。
	*/
	return NewRawStorage(config)
}
```
关于`NewRawStorage`在下面介绍StorageWithCacher的时候一起分析。

## StorageWithCacher定义
StorageWithCacher是一个函数定义，其作用是根据指定的config生成一个cacher。
其调用是在`/pkg/registry/core/pod/etcd/etcd.go中的func NewStorage=>storageInterface, dFunc := opts.Decorator(...)｀。
该opts.Decorator就是func StorageWithCacher，一个函数面值。
调用函数和参数都是通过母体函数的入口参数传递过来的，这种用法在Go语言中很常见，两个函数应结合起来看。

分析func StorageWithCacher的流程，如下所示:
1. 入口参数storageConfig是后端存储的config，定义了存储类型，存储服务器List，TLS证书信息，Cache大小等。其生成是在/pkg/master/master.go中的func (f restOptionsFactory) NewFor中根据资源类型关键字（如"pod"）来生成的。
2. 调用NewRawStorage()接口创建了一个存储后端。NewRawStorage() 接口就是generic.UndecoratedStorage()接口的实现。
3. cacherConfig的属性设置
4. 根据cacherConfig进行cacher创建

```go
// Creates a cacher based given storageConfig.

func StorageWithCacher(
	storageConfig *storagebackend.Config,
	capacity int,
	objectType runtime.Object,
	resourcePrefix string,
	scopeStrategy rest.NamespaceScopedStrategy,
	newListFunc func() runtime.Object,
	triggerFunc storage.TriggerPublisherFunc) (storage.Interface, factory.DestroyFunc) {
	/*
		storageConfig是后端存储的config，定义了存储类型，存储服务器List，TLS证书信息，Cache大小等。
		其生成是在/pkg/master/master.go中的func (f restOptionsFactory) NewFor中根据资源类型关键字（如"pod"）来生成的

		调用NewRawStorage()接口创建了一个存储后端
		NewRawStorage() 接口就是generic.UndecoratedStorage()接口的实现。
			==>定义在/pkg/registry/generic/storage_decorator.go
				==>func NewRawStorage(config *storagebackend.Config)

		StorageWithCacher()接口相比较于generic.UndecoratedStorage()，也就是多了下面的cacher操作
	*/
	s, d := generic.NewRawStorage(storageConfig)
	// TODO: we would change this later to make storage always have cacher and hide low level KV layer inside.
	// Currently it has two layers of same storage interface -- cacher and low level kv.
	/**TODO：目标是让storage一直拥有cacher**/
	/**目前拥有两个级别的的storage interface -- cacher和底层的kv **/
	/*
		CacherConfig的初始化
		==>定义在/pkg/storage/cacher.go
			==>type CacherConfig struct
	*/
	cacherConfig := storage.CacherConfig{
		CacheCapacity:        capacity,
		Storage:              s,
		Versioner:            etcdstorage.APIObjectVersioner{},
		Type:                 objectType,
		ResourcePrefix:       resourcePrefix,
		NewListFunc:          newListFunc,
		TriggerPublisherFunc: triggerFunc,
		Codec:                storageConfig.Codec,
	}
	/*
		根据是否有namespace来进行区分赋值
		KeyFunc函数用于获取该object的Key:
			有namespace的话，key的格式：prefix + "/" + Namespace + "/" + name
			无namespace的话，key的格式：prefix + "/" + name
		==>定义在/pkg/storage/util.go

		一个例子：
			/registry "/"｛Namespace｝"/" {name}
	*/
	if scopeStrategy.NamespaceScoped() {
		cacherConfig.KeyFunc = func(obj runtime.Object) (string, error) {
			return storage.NamespaceKeyFunc(resourcePrefix, obj)
		}
	} else {
		cacherConfig.KeyFunc = func(obj runtime.Object) (string, error) {
			return storage.NoNamespaceKeyFunc(resourcePrefix, obj)
		}
	}
	/*
		＊＊＊＊＊＊＊＊＊＊＊＊＊＊＊＊＊
		根据之前初始化的Cacher的config，进行cacher创建，
		比较关键，定义在pkg/storage/cacher.go
				==>func NewCacherFromConfig(config CacherConfig) *Cacher
	*/
	cacher := storage.NewCacherFromConfig(cacherConfig)
	destroyFunc := func() {
		cacher.Stop()
		d()
	}

	return cacher, destroyFunc
}
```

## NewRawStorage
func NewRawStorage符合创建和etcd的连接，把storage和关闭连接的DestroyFunc返回给上层。

```go
// NewRawStorage creates the low level kv storage. This is a work-around for current
// two layer of same storage interface.
/*
	译：NewRawStorage创建底层的kv存储。 这是一个解决当前两层相同存储接口的解决方案。
*/
// TODO: Once cacher is enabled on all registries (event registry is special), we will remove this method.
func NewRawStorage(config *storagebackend.Config) (storage.Interface, factory.DestroyFunc) {
	/*
		Create函数定义在pkg/storage/storagebackend/factory/factory.go
		去连接etcd
	*/
	s, d, err := factory.Create(*config)
	if err != nil {
		glog.Fatalf("Unable to create storage backend: config (%v), err (%v)", config, err)
	}
	return s, d
}
```

继续查看`factory.Create(*config)`函数，
其核心工作就是根据Aiserver指定的存储后端，去调用etcd官方提供的client来建立和etcd数据库的连接。
默认是etcd2。
```go
func Create(c storagebackend.Config) (storage.Interface, DestroyFunc, error) {
	/*
		判断存储类型：etcd2 、etcd3
		不显示设置的时候，走etcd2
	*/
	switch c.Type {
	case storagebackend.StorageTypeUnset, storagebackend.StorageTypeETCD2:
		return newETCD2Storage(c)
	case storagebackend.StorageTypeETCD3:
		// TODO: We have the following features to implement:
		// - Support secure connection by using key, cert, and CA files.
		// - Honor "https" scheme to support secure connection in gRPC.
		// - Support non-quorum read.
		return newETCD3Storage(c)
	default:
		return nil, nil, fmt.Errorf("unknown storage type: %s", c.Type)
	}
}
```

### newETCD2Storage
分析其步骤，如下：
1. 根据配置的TLS证书信息创建http.Transport
2. 创建etcd2 client，返回的是httpClusterClient结构
3. 关键的步骤NewEtcdStorage

```go
func newETCD2Storage(c storagebackend.Config) (storage.Interface, DestroyFunc, error) {
	// 根据配置的TLS证书信息创建http.Transport
	tr, err := newTransportForETCD2(c.CertFile, c.KeyFile, c.CAFile)
	if err != nil {
		return nil, nil, err
	}
	// 创建etcd2 client，返回的是httpClusterClient结构
	client, err := newETCD2Client(tr, c.ServerList)
	if err != nil {
		return nil, nil, err
	}
	/*
		 newTransportForETCD2和newETCD2Client两步都是为了创建与etcd连接的client。

		 NewEtcdStorage比较关键
		 根据入参初始化一个实现了storage.Interface接口的etcdHelper变量
		 	==>定义在/pkg/storage/etcd/etcd_helper.go
				==>func NewEtcdStorage
		c.Prefix的值: /registry

		由于没有显示地对c.Quorum进行赋值，如果命令行中也没有对该值进行设置的话，使用go语言的默认值false
	*/

	s := etcd.NewEtcdStorage(client, c.Codec, c.Prefix, c.Quorum, c.DeserializationCacheSize)
	// 返回etcdHelper变量，及关闭链接的函数
	return s, tr.CloseIdleConnections, nil
}
```

- newETCD2Client  
`newETCD2Client`函数需要注意一下的是，这里面有个超时属性`HeaderTimeoutPerRequest`，感觉应该进行设置。这个和Apiserver和etcd之间的连接超时有关系，还是TCP机制相关，比较复杂。
```go
func newETCD2Client(tr *http.Transport, serverList []string) (etcd2client.Client, error) {
	cli, err := etcd2client.New(etcd2client.Config{
		Endpoints: serverList,
		Transport: tr,
		//		这地方加上超时属性？以应对ifdown 拔网线apiserver访问etcd卡住的情况
		HeaderTimeoutPerRequest: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	return cli, nil
}
```
- NewEtcdStorage  
主要是通过`type etcdHelper struct`来提供操作etcd的接口。

**这个地方可以继续深挖，研究到etcd本身的watch机制。**
```go
// Creates a new storage interface from the client
// TODO: deprecate in favor of storage.Config abstraction over time
/*
	func NewEtcdStorage接口只是很简单的初始化。
	需要关注的是etcdHelper附带的通用的RESTFul 方法，
	可以看到storage.Interface接口所需要的方法都实现了，是一一对应的。
		==>/pkg/storage/interfaces.go
			==>type Interface interface
*/
func NewEtcdStorage(client etcd.Client, codec runtime.Codec, prefix string, quorum bool, cacheSize int) storage.Interface {
	return &etcdHelper{
		// 创建一个httpMembersAPI变量，附带很多方法
		etcdMembersAPI: etcd.NewMembersAPI(client),
		/*
			创建一个httpKeysAPI变量，同样附带各类方法
			后面从etcd watch数据的时候，就是用etcdKeysAPI的接口（这个地方可以继续深挖，研究到etcd本身的watch机制）
		*/
		etcdKeysAPI: etcd.NewKeysAPI(client),
		// 编解码使用
		codec:     codec,
		versioner: APIObjectVersioner{},
		// 用于序列化反序列化，版本间转换，兼容等
		copier:     api.Scheme,
		pathPrefix: path.Join("/", prefix),
		quorum:     quorum,
		// 创建cache结构
		cache: utilcache.NewCache(cacheSize),
	}
}
```

## type etcdHelper struct
可以发现`type etcdHelper struct`实现了storage.Interface接口的所有方法，一一对应的。
==>/pkg/storage/interfaces.go==>type Interface interface

```go
// etcdHelper is the reference implementation of storage.Interface.
type etcdHelper struct {
	etcdMembersAPI etcd.MembersAPI
	etcdKeysAPI    etcd.KeysAPI
	codec          runtime.Codec
	copier         runtime.ObjectCopier
	// Note that versioner is required for etcdHelper to work correctly.
	// The public constructors (NewStorage & NewEtcdStorage) are setting it
	// correctly, so be careful when manipulating with it manually.
	// optional, has to be set to perform any atomic operations
	versioner storage.Versioner
	// prefix for all etcd keys
	pathPrefix string
	// if true,  perform quorum read
	quorum bool

	// We cache objects stored in etcd. For keys we use Node.ModifiedIndex which is equivalent
	// to resourceVersion.
	// This depends on etcd's indexes being globally unique across all objects/types. This will
	// have to revisited if we decide to do things like multiple etcd clusters, or etcd will
	// support multi-object transaction that will result in many objects with the same index.
	// Number of entries stored in the cache is controlled by maxEtcdCacheEntries constant.
	// TODO: Measure how much this cache helps after the conversion code is optimized.
	cache utilcache.Cache
}
```
之后Apiserver对etcd进行的操作，最终都是调用`type etcdHelper struct`提供的restful接口进行操作的。

最后，我们回到前面的cacher
## type CacherConfig struct
在对type CacherConfig struct进行初始化的时候，会根据是否有namespace来对`KeyFunc`属性进行区分赋值。

KeyFunc函数用于获取该object的Key:
- 有namespace的话，key的格式：prefix + "/" + Namespace + "/" + name
- 无namespace的话，key的格式：prefix + "/" + name

```go
// CacherConfig contains the configuration for a given Cache.
type CacherConfig struct {
	// Maximum size of the history cached in memory.
	CacheCapacity int

	// An underlying storage.Interface.
	Storage Interface

	// An underlying storage.Versioner.
	Versioner Versioner

	// The Cache will be caching objects of a given Type and assumes that they
	// are all stored under ResourcePrefix directory in the underlying database.
	Type           interface{}
	ResourcePrefix string

	// KeyFunc is used to get a key in the underyling storage for a given object.
	/*
		KeyFunc该对象在etcd中的key，用于获取给定对象的底层存储中的键。
	*/
	KeyFunc func(runtime.Object) (string, error)

	// TriggerPublisherFunc is used for optimizing amount of watchers that
	// needs to process an incoming event.
	/*
		TriggerPublisherFunc用来优化那些需要处理incoming event的watchers
	*/
	TriggerPublisherFunc TriggerPublisherFunc

	// NewList is a function that creates new empty object storing a list of
	// objects of type Type.
	NewListFunc func() runtime.Object

	Codec runtime.Codec
}
```

## func NewCacherFromConfig 创建cacher
`func NewCacherFromConfig`根据给定的配置，创建一个新的Cacher，负责服务WATCH和LIST内部缓存请求，并在后台更新缓存。

Cacher是List-Watch机制的核心数据结构。

```go
// Create a new Cacher responsible from service WATCH and LIST requests from its
// internal cache and updating its cache in the background based on the given
// configuration.

/*
	func NewCacherFromConfig
	接口主要用于开启cacher,而该cache只用于WATCH和LIST的request。
*/
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
这里先不对List－Watch机制进行展开介绍。

## 总结
至此，关于StorageWithCacher和UndecoratedStorage已经介绍完毕，是Apiserver和etcd连接的两种接口。

需要记住其应用是在`/pkg/registry/core/pod/etcd/etcd.go中的func NewStorage=>storageInterface, dFunc := opts.Decorator(...)｀。
结合两边来看，就会对Apiserver和如何建立与etcd的连接，有个清晰的认识。




