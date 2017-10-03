# Apiserver从资源到Restful API的关键概念


## type RESTMapper interface
RESTMapper是一个interface，声明了一组函数接口。

RESTMapper可以从GVR获取GVK，并基于GVK生成一个RESTMapping来处理该GVR。

RESTMapping中有Resource名称，GVK，Scope，Convertor，Accessor等和GVR有关的信息。
```go
// RESTMapper allows clients to map resources to kind, and map kind and version
// to interfaces for manipulating those objects. It is primarily intended for
// consumers of Kubernetes compatible REST APIs as defined in docs/devel/api-conventions.md.
//
// The Kubernetes API provides versioned resources and object kinds which are scoped
// to API groups. In other words, kinds and resources should not be assumed to be
// unique across groups.

/*
	译：RESTMapper允许clients将resources 映射到kind，
		并将kind和version映射到用于操纵这些对象的接口。
		它主要面向docs/devel/api-conventions.md中定义的Kubernetes兼容REST API的消费者。

	   kinds 和 resources在各个groups不应该被认为是唯一的。

	RESTMapper映射是指GVR(GroupVersionResource)和GVK(GroupVersionKind)的关系，
	可以通过GVR找到合适的GVK，
	并可以通过GVK生成一个RESTMapping

	总结：RESTMapper可以从GVR获取GVK，
		并生成一个RESTMapping来处理该GVR。
		RESTMapping中有Resource名称，GVK，Scope，Convertor，Accessor等和GVR有关的信息。
*/
//
// TODO: split into sub-interfaces
type RESTMapper interface {
	// KindFor takes a partial resource and returns the single match.  Returns an error if there are multiple matches
	KindFor(resource unversioned.GroupVersionResource) (unversioned.GroupVersionKind, error)

	// KindsFor takes a partial resource and returns the list of potential kinds in priority order
	KindsFor(resource unversioned.GroupVersionResource) ([]unversioned.GroupVersionKind, error)

	// ResourceFor takes a partial resource and returns the single match.  Returns an error if there are multiple matches
	ResourceFor(input unversioned.GroupVersionResource) (unversioned.GroupVersionResource, error)

	// ResourcesFor takes a partial resource and returns the list of potential resource in priority order
	ResourcesFor(input unversioned.GroupVersionResource) ([]unversioned.GroupVersionResource, error)

	// RESTMapping identifies a preferred resource mapping for the provided group kind.
	//译：RESTMapping为指定的group kind 生成一个resource mapping。
	RESTMapping(gk unversioned.GroupKind, versions ...string) (*RESTMapping, error)
	// RESTMappings returns all resource mappings for the provided group kind.
	RESTMappings(gk unversioned.GroupKind) ([]*RESTMapping, error)

	AliasesForResource(resource string) ([]string, bool)
	ResourceSingularizer(resource string) (singular string, err error)
}
```

## type GroupMeta struct
```go
// GroupMeta stores the metadata of a group.
/*
	type GroupMeta struct 简介：
	主要包括Group的元信息，
	里面的成员RESTMapper，与APIGroupVersion一样，
	其实APIGroupVersion的RESTMapper直接取值于GroupMeta的RESTMapper.
	一个Group可能包含多个版本，存储在 GroupVersions 中，
	而 GroupVersion 是默认存储在etcd中的版本。
*/
type GroupMeta struct {
	// GroupVersion represents the preferred version of the group.
	// 该group的默认版本
	GroupVersion unversioned.GroupVersion

	// GroupVersions is Group + all versions in that group.
	// 该Group中可能会有多个版本，该字段就包含了所有的versions
	GroupVersions []unversioned.GroupVersion

	// Codec is the default codec for serializing output that should use
	// the preferred version.  Use this Codec when writing to
	// disk, a data store that is not dynamically versioned, or in tests.
	// This codec can decode any object that the schema is aware of.
	// 用于编解码
	Codec runtime.Codec

	// SelfLinker can set or get the SelfLink field of all API types.
	// TODO: when versioning changes, make this part of each API definition.
	// TODO(lavalamp): Combine SelfLinker & ResourceVersioner interfaces, force all uses
	// to go through the InterfacesFor method below.
	SelfLinker runtime.SelfLinker

	// RESTMapper provides the default mapping between REST paths and the objects declared in api.Scheme and all known
	// versions.
	/*
		译：RESTMapper提供 REST路径 与 那些在api.Scheme和所有已知版本中声明的对象之间的默认映射。

		用于类型，对象之间的转换
		RESTMapper定义在/pkg/api/meta/restmapper.go
			==>type DefaultRESTMapper struct
		/pkg/api/meta/interfaces.go
			==>type RESTMapper interface
	*/
	RESTMapper meta.RESTMapper

	// InterfacesFor returns the default Codec and ResourceVersioner for a given version
	// string, or an error if the version is not known.
	// TODO: make this stop being a func pointer and always use the default
	// function provided below once every place that populates this field has been changed.
	InterfacesFor func(version unversioned.GroupVersion) (*meta.VersionInterfaces, error)

	// InterfacesByVersion stores the per-version interfaces.
	InterfacesByVersion map[unversioned.GroupVersion]*meta.VersionInterfaces
}
```

## type Scheme struct
type Scheme struct，用于API资源之间的序列化、反序列化、版本转换。Scheme里面还有好几个map，前面的结构体存储的都是unversioned.GroupVersionKind、unversioned.GroupVersion这些东西，这些东西本质上只是表示资源的字符串标识。
Scheme存储了对应着标志的具体的API资源的结构体，即reflect.Type=>定义在/pkg/api/types.go中如Pod、Service这些go Struct。

RESTMapper管理的是GVR和GVK的关系，Scheme管理的是GVK和Type的关系。

系统中所有的Type都要注册到Scheme中，目前系统只有一个Scheme，即api.Scheme,定义在/pkg/api/register.go。
Scheme除了管理GVK和Type的关系，还管理有默认设置函数，并聚合了converter及cloner。
从Scheme的定义可以看出，Scheme是个converter，也是个cloner。

Kubernetes内部组件的流通的结构体值使用的是内部版本，所有的外部版本都要向内部版本进行转换；
内部版本必须转换成外部版本才能进行输出。
外部版本之间不能直接转换。
etcd中存储的是带有版本的数据。

```go
type Scheme struct {
	// versionMap allows one to figure out the go type of an object with
	// the given version and name.
	//用gvk找出对应的Type，一个gvk只能对应一个Type
	gvkToType map[unversioned.GroupVersionKind]reflect.Type

	// typeToGroupVersion allows one to find metadata for a given go object.
	// The reflect.Type we index by should *not* be a pointer.
	/*
		存储Type和gvk的关系，一个type可能对应多个GVK
		kind, gvk:  v1.ListOptions authorization.k8s.io/v1beta1, Kind=ListOptions
		kind, gvk:  v1.ListOptions apps/v1beta1, Kind=ListOptions
	*/
	typeToGVK map[reflect.Type][]unversioned.GroupVersionKind

	// unversionedTypes are transformed without conversion in ConvertToVersion.
	/*
		译：unversionedTypes在版本转转中无需改变

		记录没有版本控制的Type（即unversionedTypes）和GVK的关系，unversionedTypes无需版本转换；
	*/
	unversionedTypes map[reflect.Type]unversioned.GroupVersionKind

	// unversionedKinds are the names of kinds that can be created in the context of any group
	// or version
	// TODO: resolve the status of unversioned types.
	/*
		译：unversionedKinds是可以在任何group或version的上下文中创建的kinds的名称
		记录unversioned的GVK和Type的关系
	*/
	unversionedKinds map[string]reflect.Type

	// Map from version and resource to the corresponding func to convert
	// resource field labels in that version to internal version.
	/*
		译：从version and resource映射到相应的func，以将该版本中的resource字段标签转换为内部版本。

		field selector转换函数
		管理field selector的转换，如旧版本v1的spec.host需要转换成spec.nodeName
		(详见在/pkg/api/v1/conversion.go中的addConversionFuncs()函数)；
	*/
	fieldLabelConversionFuncs map[string]map[string]FieldLabelConversionFunc

	// defaulterFuncs is an array of interfaces to be called with an object to provide defaulting
	// the provided object must be a pointer.
	/*
		译：defaulterFuncs是一个数组接口，可以以一个对象的形式调用，被用来提供默认值。提供的对象必须是一个指针。

		存储Type及其对应的默认值设置函数；
	*/
	defaulterFuncs map[reflect.Type]func(interface{})

	// converter stores all registered conversion functions. It also has
	// default coverting behavior.
	/*
		译：converter存储所有注册转换函数。 它还具有默认转换功能。

		用来转换不同版本的结构体值；
	*/
	converter *conversion.Converter

	// cloner stores all registered copy functions. It also has default
	// deep copy behavior.
	/*
		译：cloner存储所有的copy函数。它还具有默认的深度拷贝功能

		用来获取结构体值的拷贝。
	*/
	cloner *conversion.Cloner
}
```

## type APIRegistrationManager struct
APIRegistrationManager负责对外提供已经注册并enable了的GroupVersions。
```go
/*
	type APIRegistrationManager struct 简介：
	这个结构体主要提供了已经"registered"的概念，
	将所有已经注册的，已经enable的，第三方的的GroupVersions进行了汇总，
	还包括了各个GroupVersion的GroupMeta(元数据)。
*/
type APIRegistrationManager struct {
	// registeredGroupVersions stores all API group versions for which RegisterGroup is called.
	/*
		所有已经registered的GroupVersions
		都是通过调用RegisterVersions()方法来进行注册的

		unversioned.GroupVersion定义在
		==> pkg/api/unversioned/group_version.go
			==>type GroupVersion struct
	*/
	registeredVersions map[unversioned.GroupVersion]struct{}

	// thirdPartyGroupVersions are API versions which are dynamically
	// registered (and unregistered) via API calls to the apiserver
	/*
		第三方注册的GroupVersions,这些都向apiServer动态注册的
		使用AddThirdPartyAPIGroupVersions()进行注册
	*/
	thirdPartyGroupVersions []unversioned.GroupVersion

	// enabledVersions represents all enabled API versions. It should be a
	// subset of registeredVersions. Please call EnableVersions() to add
	// enabled versions.
	/*
		所有已经enable的GroupVersions，
		可以通过EnableVersions()将要enable的GroupVersion加入进来。
		只有enable了，才能使用对应的GroupVersion
	*/
	enabledVersions map[unversioned.GroupVersion]struct{}

	// map of group meta for all groups.
	/*
		所有groups的GroupMeta
		==>/pkg/apimachinery/types.go
			==>type GroupMeta struct
	*/
	groupMetaMap map[string]*apimachinery.GroupMeta

	// envRequestedVersions represents the versions requested via the
	// KUBE_API_VERSIONS environment variable. The install package of each group
	// checks this list before add their versions to the latest package and
	// Scheme.  This list is small and order matters, so represent as a slice
	/*
		存储KUBE_API_VERSIONS环境变量包含的版本，如果未指定，则KUBE_API_VERSIONS为空
	*/
	envRequestedVersions []unversioned.GroupVersion
}
```

## type APIGroupInfo struct
基于GroupMeta和Scheme来初始化一个genericapiserver.APIGroupInfo。见/pkg/genericapiserver/genericapiserver.go

初始化时候的GroupMeta是通过type APIRegistrationManager struct的函数来获取的。
```go
// Info about an API group.
type APIGroupInfo struct {
	/*
		该Group的元信息
		==>定义在/pkg/apimachinery/types.go
	*/
	GroupMeta apimachinery.GroupMeta
	// Info about the resources in this group. Its a map from version to resource to the storage.
	// 不同版本的所有的Storage
	VersionedResourcesStorageMap map[string]map[string]rest.Storage
	// OptionsExternalVersion controls the APIVersion used for common objects in the
	// schema like api.Status, api.DeleteOptions, and api.ListOptions. Other implementors may
	// define a version "v1beta1" but want to use the Kubernetes "v1" internal objects.
	// If nil, defaults to groupMeta.GroupVersion.
	// TODO: Remove this when https://github.com/kubernetes/kubernetes/issues/19018 is fixed.
	OptionsExternalVersion *unversioned.GroupVersion

	// Scheme includes all of the types used by this group and how to convert between them (or
	// to convert objects from outside of this group that are accepted in this API).
	// TODO: replace with interfaces
	/*
		译：Scheme包括此group使用的所有类型，以及如何在它们之间进行转换（或和别的group进行对象转换）。

		如果是core group的话，对应的就是api.Scheme
		==>定义在/pkg/runtime/scheme.go
			==>type Scheme struct
	*/
	Scheme *runtime.Scheme
	// NegotiatedSerializer controls how this group encodes and decodes data
	NegotiatedSerializer runtime.NegotiatedSerializer
	// ParameterCodec performs conversions for query parameters passed to API calls
	ParameterCodec runtime.ParameterCodec

	// SubresourceGroupVersionKind contains the GroupVersionKind overrides for each subresource that is
	// accessible from this API group version. The GroupVersionKind is that of the external version of
	// the subresource. The key of this map should be the path of the subresource. The keys here should
	// match the keys in the Storage map above for subresources.
	/*
		所有resources信息,key就是resource的path
		比如：key为"replicationcontrollers/scale",GroupVersionKind: autoscaling, v1, Scale
	*/
	SubresourceGroupVersionKind map[string]unversioned.GroupVersionKind
}
```

## type APIGroupVersion struct
从type APIGroupInfo struct中获取信息生成一个APIGroupVersion对象，pkg/apiserver/apiserver.go。
```go
// APIGroupVersion is a helper for exposing rest.Storage objects as http.Handlers via go-restful
// It handles URLs of the form:
// /${storage_key}[/${object_name}]
// Where 'storage_key' points to a rest.Storage object stored in storage.
// This object should contain all parameterization necessary for running a particular API version
/*
	译：APIGroupVersion是一个helper，通过go-restful把rest.Storage objects转化为http.Handlers暴露出去。
		其URL格式如： /${storage_key}[/${object_name}]
		其中'storage_key'指向存储在storage中的一个rest.Storage object。
		APIGroupVersion 应包含运行特定API版本所需的所有参数
*/
/*
	type APIGroupVersion struct 简介:

	对API资源的组织，里面包含了Storage、GroupVersion、Mapper、Serializer、Convertor等成员。
	Storage是etcd的接口，这是一个map类型，每一种资源都会与etcd建立一个连接；
	GroupVersion表示该APIGroupVersion属于哪个Group、哪个version；
	Serializer用于序列化，反序列化；
	Convertor提供各个不同版本进行转化的接口；
	Mapper实现了RESTMapper接口。
*/
type APIGroupVersion struct {
	/*
		key存在对象的url，value是一个rest.Storage，用于对接etcd存储
	*/
	Storage map[string]rest.Storage

	/*
		Root: 该group的prefix，例如核心组的Root是'/api'
	*/
	Root string

	// GroupVersion is the external group version
	/*
		包含类似'api/v1'这样的string，用于标识这个实例
	*/
	GroupVersion unversioned.GroupVersion

	// OptionsExternalVersion controls the Kubernetes APIVersion used for common objects in the apiserver
	// schema like api.Status, api.DeleteOptions, and api.ListOptions. Other implementors may
	// define a version "v1beta1" but want to use the Kubernetes "v1" internal objects. If
	// empty, defaults to GroupVersion.
	OptionsExternalVersion *unversioned.GroupVersion

	/*
		Mapper: 关键性成员
	*/
	Mapper meta.RESTMapper

	// Serializer is used to determine how to convert responses from API methods into bytes to send over
	// the wire.
	/*
		对象序列化和反序列化器
	*/
	Serializer     runtime.NegotiatedSerializer
	ParameterCodec runtime.ParameterCodec

	/*
		Typer,Creater,Convertor,Copier 都会被赋值为Scheme结构
	*/
	Typer   runtime.ObjectTyper
	Creater runtime.ObjectCreater
	/*
		Convertor： 相互转换任意api版本的对象，需要事先注册转换函数
	*/
	Convertor runtime.ObjectConvertor
	Copier    runtime.ObjectCopier
	Linker    runtime.SelfLinker

	/*
		用于访问许可控制
	*/
	Admit   admission.Interface
	Context api.RequestContextMapper

	MinRequestTimeout time.Duration

	// SubresourceGroupVersionKind contains the GroupVersionKind overrides for each subresource that is
	// accessible from this API group version. The GroupVersionKind is that of the external version of
	// the subresource. The key of this map should be the path of the subresource. The keys here should
	// match the keys in the Storage map above for subresources.
	SubresourceGroupVersionKind map[string]unversioned.GroupVersionKind

	// ResourceLister is an interface that knows how to list resources
	// for this API Group.
	/*
		译：ResourceLister是一个直到如何列出此API Group的资源的接口。
	*/
	ResourceLister APIResourceLister
}
```

最后`apiGroupVersion.InstallREST(s.HandlerContainer.Container)`，完成从API资源到restful API的注册。


