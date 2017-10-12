# Storage机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type restOptionsFactory struct](#type-restoptionsfactory-struct)
  - [生成storage](#生成storage)
  - [podStorage](#podstorage)
    - [路径/pods/binding](#路径/pods/binding)
	- [路径/pods](#路径/pods)
  - [namespaceStorage](#namespacestorage)
  - [一个有用的debug函数](#一个有用的debug函数)
  - [Daemon](#daemon)

<!-- END MUNGE: GENERATED_TOC -->

Apiserver针对每一类资源(pod、service、replication controller),都会与etcd建立一个连接。
本文就是主要介绍Apiserver如何建立与etcd的连接，获取使用etcd的opt的，从而对资源进行操作。
然后还会介绍具体某个Storage（如podStorage）的使用方法。
最后介绍怎么判断一个Storage实现了哪些接口。

还是从`/pkg/master/master.go`的`func (c completedConfig) New() (*Master, error)`函数开始

## type restOptionsFactory struct
Apiserver先是通过type restOptionsFactory struct获取到etcd的opt的，先来看看`type restOptionsFactory struct`的定义。

- 定义
```go
type restOptionsFactory struct {
	deleteCollectionWorkers int
	enableGarbageCollection bool
	/*
		genericapiserver.StorageFactory
		==>定义在/pkg/genericapiserver/storage_factory.go
			==>var _ StorageFactory = &DefaultStorageFactory{}

		资源在etcd中存储的前缀路径和storageFactory相关
	*/
	storageFactory   genericapiserver.StorageFactory
	storageDecorator generic.StorageDecorator
}
```

- 创建一个type restOptionsFactory struct对象  
这里的重点在于`restOptionsFactory.storageDecorator`属性，这很重要。后面会展开详细的讲解。
```go
/*
		该接口初始化了一个restOptionsFactory变量，
		里面指定了最大的删除回收资源的协程数，是否使能GC和storageFactory
	*/
	restOptionsFactory := restOptionsFactory{
		deleteCollectionWorkers: c.DeleteCollectionWorkers,
		enableGarbageCollection: c.GenericConfig.EnableGarbageCollection,
		storageFactory:          c.StorageFactory,
	}

	/*
		判断是否enable了用于Watch的Cache。有无cache，赋值的是不同的接口实现。
		restOptionsFactory.storageDecorator：是一个各个资源的REST interface(CRUD)装饰者，
		后面调用NewStorage()时会用到该接口，并输出对应的CRUD接口及销毁接口。
		可以参考pkg/registry/core/pod/etcd/etcd.go中的NewStorage()
		其实这里有无cache的接口差异就在于：
			有cache的话，就提供操作cache的接口；
			无cache的话，就提供直接操作etcd的接口

		根据是否enable了WatchCache来完成NewStorage()接口中调用的装饰器接口的赋值。

		registry.StorageWithCacher：该接口是返回了操作cache的接口，和清除cache的操作接口。
		generic.UndecoratedStorage: 该接口会根据你配置的后端类型(etcd2/etcd3等)，来返回不同的etcd操作接口，
		其实是为所有的资源对象创建了etcd的链接，然后通过该链接发送不同的命令，最后还返回了断开该链接的接口。

		所以两者的实现完全不一样，一个操作cache，一个操作实际的etcd。

		在这里完成给storageDecorator赋值了！！！！！！
		*******需要深入了解两个storageDecorator类型*********
	*/
	if c.EnableWatchCache {
		/*
			函数StorageWithCacher定义在pkg/registry/generic/registry/storage_factory.go
				==>func StorageWithCacher
		*/
		restOptionsFactory.storageDecorator = registry.StorageWithCacher
	} else {
		/*
			函数UndecoratedStorage定义在pkg/registry/generic/storage_decorator.go
				==>func UndecoratedStorage
		*/
		restOptionsFactory.storageDecorator = generic.UndecoratedStorage
	}
```

后面会通过restOptionsFactory.NewFor的调用来生成一个opts
- restOptionsFactory.NewFor
```go
func (f restOptionsFactory) NewFor(resource unversioned.GroupResource) generic.RESTOptions {
	/*
		f.storageFactory.NewConfig，创建该资源的Storage Config
		==>定义在/pkg/genericapiserver/storage_factory.go
	*/
	//假设传进来的参数是"pods"
	storageConfig, err := f.storageFactory.NewConfig(resource)
	if err != nil {
		glog.Fatalf("Unable to find storage destination for %v, due to %v", resource, err.Error())
	}

	/*
		返回的就是RESTOptions, 就是/pkg/registry/core/pod/etcd/etcd.go中func NewStorage中用到的opts的类型
		需要关注f.storageDecorator的由来

		定义在/pkg/registry/generic/options.go
			==>type RESTOptions struct
	*/
	return generic.RESTOptions{
		// 用于生成Storage的config
		StorageConfig:           storageConfig,
		Decorator:               f.storageDecorator,
		DeleteCollectionWorkers: f.deleteCollectionWorkers,
		EnableGarbageCollection: f.enableGarbageCollection,
		ResourcePrefix:          f.storageFactory.ResourcePrefix(resource),
	}
}
```

## 生成storage
- func (c completedConfig) New()
```go
m.InstallLegacyAPI(c.Config, restOptionsFactory.NewFor, legacyRESTStorageProvider)
```
- func (m *Master) InstallLegacyAPI
```go
func (m *Master) InstallLegacyAPI(c *Config, restOptionsGetter genericapiserver.RESTOptionsGetter, legacyRESTStorageProvider corerest.LegacyRESTStorageProvider) {
	....
	/*
		legacyRESTStorageProvider这个对象，比较关键，需要深入查看
		返回了RESTStorage和apiGroupInfo，都是重量级的成员
		这些初始化也就在NewLegacyRESTStorage这个接口中
		==>定义在pkg/registry/core/rest/storage_core.go
			==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
	*/
	glog.Infof("生成apiGroupInfo, apiGroupInfo携带着restStorageMap")
	legacyRESTStorage, apiGroupInfo, err := legacyRESTStorageProvider.NewLegacyRESTStorage(restOptionsGetter)
	if err != nil {
		glog.Fatalf("Error building core storage: %v", err)
	}	
	....
}
```
- func (c LegacyRESTStorageProvider) NewLegacyRESTStorage  
这里的入参`restOptionsGetter`就是前面`restOptionsFactory.NewFor`生成的opt
```go
func (c LegacyRESTStorageProvider) NewLegacyRESTStorage(restOptionsGetter genericapiserver.RESTOptionsGetter) (LegacyRESTStorage, genericapiserver.APIGroupInfo, error) {
	...
	/*
		创建PodStorage
		api.Resource("pods")是合成了一个GroupResource的结构

		podetcd.NewStorage函数定义在
			==>/pkg/registry/core/pod/etcd/etcd.go中
				==>func NewStorage

		restOptionsGetter(api.Resource("pods"))该步完成了opts的创建，该opt在具体的资源watch-list中很重要
		restOptionsGetter的由来可以查看/pkg/master/master.go中的master的func (c completedConfig) New() (*Master, error)
	*/
	podStorage := podetcd.NewStorage(
		restOptionsGetter(api.Resource("pods")),
		nodeStorage.KubeletConnectionInfo,
		c.ProxyTransport,
		podDisruptionClient,
	)
	...

}
```
后面就是Apisever利用这些storage来生成Restful API、获取数据等操作了。

下面来分析怎么查看一个资源都实现哪些接口函数？以便在安装Restful API的类型断言的时候识别出来。
## podStorage
这个地方需要注意的有两点:
1. 是公有成员，还是私有成员？
2. 是否出现了同名函数？

podStorage是通过`NewStorage`函数来创建的，先看看其定义和创建函数

- type PodStorage struct
```
// PodStorage includes storage for pods and all sub resources
type PodStorage struct {
	Pod         *REST
	Binding     *BindingREST
	Eviction    *EvictionREST
	Status      *StatusREST
	Log         *podrest.LogREST
	Proxy       *podrest.ProxyREST
	Exec        *podrest.ExecREST
	Attach      *podrest.AttachREST
	PortForward *podrest.PortForwardREST
}
```

- func NewStorage
```go
func NewStorage(opts generic.RESTOptions, k client.ConnectionInfoGetter, proxyTransport http.RoundTripper, podDisruptionBudgetClient policyclient.PodDisruptionBudgetsGetter) PodStorage {
	prefix := "/" + opts.ResourcePrefix

	newListFunc := func() runtime.Object { return &api.PodList{} }
	/*
		调用接口装饰器，返回该storage的etcd操作接口及资源delete接口，
		该opts传参进来的，需要到上一层/pkg/master/master.go查看 restOptionsFactory.NewFor

		opts 的创建是在/pkg/registry/core/rest/storage_core.go中的
		==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
			==>restOptionsGetter(api.Resource("pods")),

		opts.Decorator的初始化是在/pkg/master/master.go
			==>func (c completedConfig) New() (*Master, error)
				==>restOptionsFactory.storageDecorator = registry.StorageWithCacher
				或=>restOptionsFactory.storageDecorator = generic.UndecoratedStorage
		这里调用函数和参数都是通过母体函数的入口参数传递过来的，这种用法在Go语言中很常见
	*/
	storageInterface, dFunc := opts.Decorator(
		opts.StorageConfig,
		// 这以下的参数都是用于开启cache时的接口使用
		cachesize.GetWatchCacheSizeByResource(cachesize.Pods),
		&api.Pod{},
		/*
			prefix: /registry
		*/
		prefix,
		/*
			Strategy是通过REST API创建和更新Pod时，对该pod适用的默认逻辑
			==>定义在/pkg/registry/core/pod/strategy.go
		*/
		pod.Strategy,
		newListFunc,
		pod.NodeNameTriggerFunc,
	)

	/*
		&registry.Store是公共的Store
		Storage如PodStorage是具体类型的Store，很多公共接口都定义在/pkg/registry/generic/registry/store.go中，如Watch
		具体类型的Storage都实现了&registry.Store接口

		*registry.Store 有12 个public方法
	*/
	store := &registry.Store{
		NewFunc:     func() runtime.Object { return &api.Pod{} },
		NewListFunc: newListFunc,
		KeyRootFunc: func(ctx api.Context) string {
			return registry.NamespaceKeyRootFunc(ctx, prefix)
		},
		KeyFunc: func(ctx api.Context, name string) (string, error) {
			return registry.NamespaceKeyFunc(ctx, prefix, name)
		},
		ObjectNameFunc: func(obj runtime.Object) (string, error) {
			return obj.(*api.Pod).Name, nil
		},
		PredicateFunc:           pod.MatchPod,
		QualifiedResource:       api.Resource("pods"),
		EnableGarbageCollection: opts.EnableGarbageCollection,
		DeleteCollectionWorkers: opts.DeleteCollectionWorkers,

		CreateStrategy:      pod.Strategy,
		UpdateStrategy:      pod.Strategy,
		DeleteStrategy:      pod.Strategy,
		ReturnDeletedObject: true,

		/*
			默认情况下开启了cache，Storage的是带cache的storage
			在/pkg/registry/generic/registry/storage_factory.go
				==>func StorageWithCacher 生成
		*/
		Storage:     storageInterface,
		DestroyFunc: dFunc,
	}

	statusStore := *store
	statusStore.UpdateStrategy = pod.StatusStrategy

	/*
		storage实现了什么方法都是由前面的结构体来决定的
		eg:
			PodStorage.Pod的方法来源于 type REST struct，
			而type REST struct的大部分方法来源于 *registry.Store
			==>所以PodStorage.Pod也就有12个方法

		那么问题来了，为什么&REST继承了*registry.Store的方法？而&BindingREST却没有继承？
			==>在于两者定义结构体的时候，声明属性为公有变量，还是私有变量！
				==>&BindingREST 声明为私有的，首字母小写
				==>而&REST则是声明为公有的 ＋ func (r *REST) ResourceLocation

		需要注意的一点是如果&REST显示声明了*registry.Store同名的函数，参考namespace的Delete函数
			==>/pkg/registry/core/namespace/etcd/etcd.go
				==>执行kubectl delete namespace xx的时候，会先调用函数func (r *REST) Delete，
					但最终还是调用&registry.Store的Delete()函数
	*/
	//	fqhGetMembers(&REST{})
	return PodStorage{
		Pod:         &REST{store, proxyTransport},
		Binding:     &BindingREST{store: store},
		Eviction:    newEvictionStorage(store, podDisruptionBudgetClient),
		Status:      &StatusREST{store: &statusStore},
		Log:         &podrest.LogREST{Store: store, KubeletConn: k},
		Proxy:       &podrest.ProxyREST{Store: store, ProxyTransport: proxyTransport},
		Exec:        &podrest.ExecREST{Store: store, KubeletConn: k},
		Attach:      &podrest.AttachREST{Store: store, KubeletConn: k},
		PortForward: &podrest.PortForwardREST{Store: store, KubeletConn: k},
	}
}
```

### 路径/pods/binding
就`/pods/binding`而言，声明了4个方法，如下：
```go
func (r *BindingREST) New
func (r *BindingREST) Create
func (r *BindingREST) setPodHostAndAnnotations
func (r *BindingREST) assignPod
```
通过对比`/pkg/api/rest/rest.go`中的interface定义，可以说`/pods/binding`符合`Creater、NamedCreater、`。
那么可以得出`/pods/binding`的actions应该是只有一个POST
```go
actions = appendIf(actions, action{"POST", resourcePath, resourceParams, namer, false}, isCreater)
```
而直接fmt.Print来看，其值如下：
```
[{POST namespaces/{namespace}/pods/{name}/binding [0xc4200259c0 0xc4200259b0] {0x4c134a0 {} 0x8303e0 false} false}]
```
两者是吻合的。

### 路径/pods
就`/pods`而言，其对应的stroage是`Pod:         &REST{store, proxyTransport},`，其通过`&registry.Store`实现了12个方法，如下:
```go
Create
Delete
DeleteCollection
Export
Get
List
ListPredicate
New
NewList
Update
Watch
WatchPredicate
```
还有通过`type REST struct`声明了一个`ResourceLocation`
```
func (r *REST) ResourceLocation
```

## namespaceStorage
namespaceStorage和podStorage.Pod的大部分是一样的，有一点区别在于
- namespaceStorage通过`type REST struct`来显式声明了一个Delete函数，而`&registry.Store`本身也有一个Delete函数，两边出现了同名函数。这个时候类型断言判断用到的Delete函数应该是显式声明的那个func (r *REST) Delete。而不是`&registry.Store`的Delete()
- podStorage.Pod通过`type REST struct`显式声明的函数没有和`&registry.Store`重名的

```go
// NewREST returns a RESTStorage object that will work against namespaces.
func NewREST(opts generic.RESTOptions) (*REST, *StatusREST, *FinalizeREST) {
	...
	...
	/*
		storage实现了什么方法都是由三个结构体type REST struct 、StatusREST、FinalizeREST来决定的

		type REST struct的大部分方法来源于 *registry.Store，自身又显式声明了一个Delete函数
		两边都有一个Delete函数
		这个时候，对外部显示的就只有type REST struct显式声明的Delete函数
		虽然显式的Delete函数最后还是会调用*registry.Store的同名函数
	*/
	return &REST{Store: store, status: &statusStore}, &StatusREST{store: &statusStore}, &FinalizeREST{store: &finalizeStore}
}
```

执行kubectl delete namespace {xx}的时候，会先调用函数func (r *REST) Delete，
但最终还是调用&registry.Store的同名Delete()函数。
```go
func (r *REST) Delete(ctx api.Context, name string, options *api.DeleteOptions) (runtime.Object, error) {
	...
	...
	/*
		调用&registry.Store的Delete()函数
	*/
	return r.Store.Delete(ctx, name, options)
}
```

## 一个有用的debug函数
查看该结构体所拥有的成员
```go
func fqhGetMembers(i interface{}) {
	t := reflect.TypeOf(i)
	fmt.Println(t.PkgPath())
	for {
		if t.Kind() == reflect.Struct {
			fmt.Printf("\n%-8v %v 个字段:\n", t, t.NumField())
			for i := 0; i < t.NumField(); i++ {
				fmt.Println(t.Field(i).Name, t.Field(i).Type)
			}
		}
		fmt.Printf("\n%-8v %v 个方法:\n", t, t.NumMethod())
		for i := 0; i < t.NumMethod(); i++ {
			fmt.Println(t.Method(i).Name, t.Method(i).PkgPath)
		}
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		} else {
			break
		}
	}
}
```
用法如下
```go
fqhGetMembers(&REST{})
fqhGetMembers(&registry.Store{})
```

## Daemon
仿造Apiserver利用类型断言来判断一个路径所支持的接口

```go
package main

import (
	"fmt"
	"reflect"
)

func GetMembers(i interface{}) {
	t := reflect.TypeOf(i)
	for {
		if t.Kind() == reflect.Struct {
			fmt.Printf("\n%-8v %v 个字段:\n", t, t.NumField())
			for i := 0; i < t.NumField(); i++ {
				fmt.Println(t.Field(i).Name)
			}
		}
		fmt.Printf("\n%-8v %v 个方法:\n", t, t.NumMethod())
		for i := 0; i < t.NumMethod(); i++ {
			fmt.Println(t.Method(i).Name)
		}
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		} else {
			break
		}
	}
}

//interface定义
type Storage interface {
	New()
}
type Lister interface {
	List()
}

type Exporter interface {
	Export()
}

//type Store struct定义
type Store struct {
	a string
}

func (c *Store) New() {
	fmt.Println("func (c *Store) New()")
}
func (c *Store) List() {
	fmt.Println("func (c *Store) List()")
}
func (c *Store) Export() {
	fmt.Println("func (c *Store) Export()")
}
func (c *Store) Create() {
	fmt.Println("func (c *Store) Create()")
}
func (c *Store) Delete() {
	fmt.Println("func (c *Store) Delete()")
}

//要检测的type REST struct
type REST struct {
	*Store
}

func new_REST() *REST {
	ss := &Store{a: "This is new_REST"}
	t := &REST{ss}
	return t
}

//要检测的type BindingREST struct
type BindingREST struct {
	store *Store //首字母小写
}

func (b *BindingREST) New() {
	fmt.Println("func (b *BindingREST) New()")
}

func (b *BindingREST) Bind() {
	fmt.Println("func (b *BindingREST) bind()")
}
func (b *BindingREST) Export() {
	fmt.Println("func (b *BindingREST) Export()")
}

func new_BindingREST() *BindingREST {
	ss := &Store{a: "This is new_BindingREST"}
	t := &BindingREST{store: ss}
	return t
}

//断言函数,要经过一个interface的转换，断言只能用在interface上
func judge(store Storage) {
	_, isLister := store.(Lister)
	fmt.Println("isLister", isLister)
	_, isExporter := store.(Exporter)
	fmt.Println("isLister", isExporter)
}
func main() {
	fmt.Println("check REST-----")
	target := new_REST()
	judge(target)
	GetMembers(target)

	fmt.Println("check BindingREST-----")
	ta := new_BindingREST()
	judge(ta)
	ta.Export()
	GetMembers(ta)
}
```

输出结果如下
```
check REST-----
isLister true
isLister true

*main.REST 5 个方法:
Create
Delete
Export
List
New

main.REST 1 个字段:
Store

main.REST 5 个方法:
Create
Delete
Export
List
New
check BindingREST-----
isLister false
isLister true
func (b *BindingREST) Export()

*main.BindingREST 3 个方法:
Bind
Export
New

main.BindingREST 1 个字段:
store

main.BindingREST 0 个方法:
```



