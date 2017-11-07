# StorageVersions寻根

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引子](#引子)
  - [StorageVersions的定义](#storageversions的定义)
  - [AllPreferredGroupVersions函数解读](#allpreferredgroupversions函数解读)
  - [一堆关键数据结构](#一堆关键数据结构)
    - [type APIRegistrationManager struct](#type-apiregistrationmanager-struct)
	- [type GroupVersion struct](#type-groupversion-struct)
	- [type GroupMeta struct](#type-groupmeta-struct)
	- [type RESTMapper interface](#type-restmapper-interface)
	- [GroupVersionKind,GroupVersionResource,GroupKind](#groupversionkind-groupversionresource-groupkind)
	- [type RESTMapping struct](#type-restmapping-struct)
	- [type RESTScope interface](#type-restscope-interface)
	- [type ObjectConvertor interface](#type-objectconvertor-interface)
	- [type MetadataAccessor interface](#type-metadataaccessor-interface)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文的目的是从StorageVersions出发，期望找出Apiserver复杂的多版本API管理过程中涉及到的`概念接口`，理清各个概念之间的调用关系、包含关系。
## 引子
前文提到，在`/pkg/genericapiserver/options/server_run_options.go`中有
```go
/*
	registered.AllPreferredGroupVersions(),通过函数面值来调用，定义在
	==>/pkg/apimachinery/registered/registered.go
		==>AllPreferredGroupVersions     = DefaultAPIRegistrationManager.AllPreferredGroupVersions
	从这里去延伸考虑整个流程是在哪里对groupMeta进行register & enable的？？？？？？
*/
DefaultStorageVersions:    registered.AllPreferredGroupVersions(),
```

## StorageVersions的定义
StorageVersions是什么？作用是什么？
可以参考[apiserver参数详解]()一文中的说明。
```
--storage-versions string  按组划分资源存储的版本。
以"group1/version1,group2/version2,..."的格式指定。

当对象从一组移动到另一组时, 你可以指定"group1=group2/v1beta1,group3/v1beta1,..."的格式。
你只需要传入你希望从结果中改变的组的列表。
默认为从KUBE_API_VERSIONS环境变量集成而来，所有注册组的首选版本列表。

默认值为:
"admission.k8s.io/v1alpha1,admissionregistration.k8s.io/v1alpha1,apps/v1beta1,
authentication.k8s.io/v1,authorization.k8s.io/v1,autoscaling/v1,batch/v1,
certificates.k8s.io/v1beta1,componentconfig/v1alpha1,extensions/v1beta1,
federation/v1beta1,imagepolicy.k8s.io/v1alpha1,networking.k8s.io/v1,
policy/v1beta1,rbac.authorization.k8s.io/v1beta1,settings.k8s.io/v1alpha1,storage.k8s.io/v1,v1"
```

## AllPreferredGroupVersions函数解读
我们从这里开始解读下去，/pkg/apimachinery/registered/registered.go
```go
// People are calling global functions. Let them continue to do that (for now).
/*
	包级变量，定义了一些函数面值
	在分析apiServer的启动流程的时候，会发现初始化ServerRunOptions对象时，大量使用了这里的全局函数面值
		比如：pkg/genericapiserver/options/server_run_options.go中的
				==>func NewServerRunOptions() *ServerRunOptions中的
					===>DefaultStorageVersions:    registered.AllPreferredGroupVersions(),
					就是通过调用下面的函数面值AllPreferredGroupVersions来调用真正的函数
					func (m *APIRegistrationManager) AllPreferredGroupVersions() string
*/
var (
	ValidateEnvRequestedVersions  = DefaultAPIRegistrationManager.ValidateEnvRequestedVersions
	AllPreferredGroupVersions     = DefaultAPIRegistrationManager.AllPreferredGroupVersions
	RESTMapper                    = DefaultAPIRegistrationManager.RESTMapper
	GroupOrDie                    = DefaultAPIRegistrationManager.GroupOrDie
	AddThirdPartyAPIGroupVersions = DefaultAPIRegistrationManager.AddThirdPartyAPIGroupVersions
	IsThirdPartyAPIGroupVersion   = DefaultAPIRegistrationManager.IsThirdPartyAPIGroupVersion
	RegisteredGroupVersions       = DefaultAPIRegistrationManager.RegisteredGroupVersions
	IsRegisteredVersion           = DefaultAPIRegistrationManager.IsRegisteredVersion
	IsRegistered                  = DefaultAPIRegistrationManager.IsRegistered
	Group                         = DefaultAPIRegistrationManager.Group
	EnabledVersionsForGroup       = DefaultAPIRegistrationManager.EnabledVersionsForGroup
	EnabledVersions               = DefaultAPIRegistrationManager.EnabledVersions
	IsEnabledVersion              = DefaultAPIRegistrationManager.IsEnabledVersion
	IsAllowedVersion              = DefaultAPIRegistrationManager.IsAllowedVersion
	EnableVersions                = DefaultAPIRegistrationManager.EnableVersions
	RegisterGroup                 = DefaultAPIRegistrationManager.RegisterGroup
	RegisterVersions              = DefaultAPIRegistrationManager.RegisterVersions
	InterfacesFor                 = DefaultAPIRegistrationManager.InterfacesFor
)
```
查看`AllPreferredGroupVersions()`函数，从字面来理解，AllPreferredGroupVersions的意思就是所有的默认的GroupVersion。
```go
// AllPreferredGroupVersions returns the preferred versions of all registered
// groups in the form of "group1/version1,group2/version2,..."
/*
	译：AllPreferredGroupVersions以"group1/version1,group2/version2,..."的形式返回所有注册组的首选版本。
*/
func (m *APIRegistrationManager) AllPreferredGroupVersions() string {
	/*
		如果没有注册groupMeta的话，这里就==0。
		不过不可能没有注册，至于在哪里进行注册就得看下后面介绍的GroupMeta初始化了

		func (m *APIRegistrationManager) AllPreferredGroupVersions() 的功能：
		就是从m.groupMetaMap中取出所有的groupMeta，
		然后通过逗号拼接成"group1/version1,group2/version2,..."的字符串。
		那么m *APIRegistrationManager的groupMeta哪里来的？

		这里既然有遍历操作，那总得有groupMeta啊。
		而我们看APIRegistrationManager的初始化函数func NewAPIRegistrationManager(kubeAPIVersions string)，
		如果没有设置KUBE_API_VERSIONS环境变量的话，根本就没有groupMeta。
		既然不可能没有groupMeta，那肯定得从别的地方进行register & enable。
		我们可以从APIRegistrationManager提供的RegisterGroup方法入手
			==>func (m *APIRegistrationManager) RegisterGroup(groupMeta apimachinery.GroupMeta)
	*/
	if len(m.groupMetaMap) == 0 {
		return ""
	}
	var defaults []string
	for _, groupMeta := range m.groupMetaMap {
		defaults = append(defaults, groupMeta.GroupVersion.String())
	}
	sort.Strings(defaults)
	return strings.Join(defaults, ",")
}
```

## 一堆关键数据结构
到这里之后就可以引出很多概念了，我们一一来看，这里的概念很重要，需要搞懂各个概念之间的关系。
这里只是先把概念罗列出来，后面再就每一个结构体的具体作用进行详解。

### type APIRegistrationManager struct
显然，APIRegistrationManager负责对外提供已经注册并enable了的GroupVersions。
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

### type GroupVersion struct
我们先来看看`unversioned.GroupVersion`的定义，可以看出就是两个string，一个group，一个version。定义了一个API所处的分组和版本。
这个是kubernetes实现多版本的基础定义。
```go
// GroupVersion contains the "group" and the "version", which uniquely identifies the API.
//
// +protobuf.options.(gogoproto.goproto_stringer)=false
type GroupVersion struct {
	Group   string `protobuf:"bytes,1,opt,name=group"`
	Version string `protobuf:"bytes,2,opt,name=version"`
}
```

### type GroupMeta struct
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

### type RESTMapper interface
这里有个`RESTMapper`的概念，RESTMapper是一个interface，声明了一组函数接口。

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
这里延伸出来的概念就多了，GroupVersionKind、GroupVersionResource、GroupKind、RESTMapping。

### GroupVersionKind GroupVersionResource GroupKind
GroupVersionKind、GroupVersionResource、GroupKind 这三者和前面的GroupVersion一样，也是定义在pkg/api/unversioned/group_version.go之中。其实就是Group、Version、Kind、Resource的组合。
```go
// GroupVersionKind unambiguously identifies a kind.  It doesn't anonymously include GroupVersion
// to avoid automatic coersion.  It doesn't use a GroupVersion to avoid custom marshalling
//
// +protobuf.options.(gogoproto.goproto_stringer)=false
type GroupVersionKind struct {
	Group   string `protobuf:"bytes,1,opt,name=group"`
	Version string `protobuf:"bytes,2,opt,name=version"`
	Kind    string `protobuf:"bytes,3,opt,name=kind"`
}

// GroupVersionResource unambiguously identifies a resource.  It doesn't anonymously include GroupVersion
// to avoid automatic coersion.  It doesn't use a GroupVersion to avoid custom marshalling
//
// +protobuf.options.(gogoproto.goproto_stringer)=false
type GroupVersionResource struct {
	Group    string `protobuf:"bytes,1,opt,name=group"`
	Version  string `protobuf:"bytes,2,opt,name=version"`
	Resource string `protobuf:"bytes,3,opt,name=resource"`
}

// GroupKind specifies a Group and a Kind, but does not force a version.  This is useful for identifying
// concepts during lookup stages without having partially valid types
//
// +protobuf.options.(gogoproto.goproto_stringer)=false
type GroupKind struct {
	Group string `protobuf:"bytes,1,opt,name=group"`
	Kind  string `protobuf:"bytes,2,opt,name=kind"`
}
```

那么问题来了，Group、Version、Kind、Resource分别是什么？怎么使用？
这个可以查看kubectl系列文章中[kubernetes里面各种Client]()一文
- Resource
```go
// APIResource specifies the name of a resource and whether it is namespaced.
type APIResource struct {
	// name is the name of the resource.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// namespaced indicates if a resource is namespaced or not.
	Namespaced bool `json:"namespaced" protobuf:"varint,2,opt,name=namespaced"`
	// kind is the kind for the resource (e.g. 'Foo' is the kind for a resource 'foo')
	Kind string `json:"kind" protobuf:"bytes,3,opt,name=kind"`
}

reousrce := unversioned.APIResource{
		Name:       "nodes", //单数复数
		Namespaced: false,
		Kind:       "Node", //首字母大写
	}
reousrce = unversioned.APIResource{
		Name:       "pods",
		Namespaced: true,
		Kind:       "Pod",
	}
```

- Kind  

Kind is a string value representing the REST resource this object represents. 
可以看出，Kind就是定义在`/kubernetes-1.5.2/pkg/api/types.go`中的各种结构体，这里的`/kubernetes-1.5.2/pkg/api/types.go`的数据结构都是internal version的对象。如：

```go
type Pod struct
type PodList struct

type Node struct
type NodeList struct
```
从这里可以发现每一个结构体都会有一个对应的List结构体，Pod->PodList，Node->NodeList。
这个List结构体好像是必须要有的。

- Group、Version
那么Group、Version的值呢？定义在`/kubernetes-1.5.2/pkg/api/v1/types.go`中的是一个external version，如下：
```
Group="core"
Version=v1
```

- 小结  
至此，Group、Version、Kind、Resource就已经清楚了。而GroupVersionKind、GroupVersionResource、GroupKind无非就是某个Group的某个Version中的Kind、Resource。

注意是某个Group 的一个Version，而不是某个Version的一个Group。
注意区别！这个和Apiserver对多版本的组织相关。
可以这么来理解，各个Group是相互独立的，发育程度也不一样，有快有慢，而kubernetes是以插件的形式来使用各个Group的。
这就提供了很大的灵活性。可以根据需求决定使用A Group的哪一个Version。

### type RESTMapping struct
RESTMapping包含以RESTful方式处理一个特定的resource and kind的对象所需的信息。
定义在/kubernetes-1.5.2/pkg/api/meta/interfaces.go中。
```go
/*
	RESTMapping包含一个Resource名称，
	及其对应的GVK，
	还有一个Scope(标明资源是否为root或者namespaced)，
	还有一个Convertor用来转换该GVK对应的Object
	和一个MetadataAccessor用来提取Object的meta信息。

	那么RESTMapping怎么用呢？
	比如/pkg/apiserver/api_installer.go中就有使用到RESTMapping中的Scope用来生成合适的URL(RESTScopeNameRoot和RESTScopeNameNamespace处理不同)。
	再比如/pkg/kubectl/resource_printer.go中的VersionedPrinter中的converter也是来自RESTMapping中的Convertor(和Scheme相关)。
*/
type RESTMapping struct {
	// Resource is a string representing the name of this resource as a REST client would see it
	Resource string

	GroupVersionKind unversioned.GroupVersionKind

	// Scope contains the information needed to deal with REST Resources that are in a resource hierarchy
	Scope RESTScope

	runtime.ObjectConvertor
	MetadataAccessor
}
```
继续，这里出来了个RESTScope、ObjectConvertor和MetadataAccessor

### type RESTScope interface
RESTScope用于标识某个资源是处于Namespace下，还是全局资源(没有Namespace)。
```go
const (
	/*
		RESTScopeNamespace表明该资源是在Namespace下的，如pods，rc等；
		RESTScopeRoot标明资源是全局的，如nodes, pv，namespace，ComponentStatus。
	*/
	RESTScopeNameNamespace RESTScopeName = "namespace"
	RESTScopeNameRoot      RESTScopeName = "root"
)

// RESTScope contains the information needed to deal with REST resources that are in a resource hierarchy
/*
	译：RESTScope包含处理资源层次结构中的REST资源所需的信息

	RESTScope具体由type restScope struct实现。restScope定义在/pkg/api/meta/restmapper.go中
*/
type RESTScope interface {
	// Name of the scope
	Name() RESTScopeName
	// ParamName is the optional name of the parameter that should be inserted in the resource url
	// If empty, no param will be inserted
	ParamName() string
	// ArgumentName is the optional name that should be used for the variable holding the value.
	ArgumentName() string
	// ParamDescription is the optional description to use to document the parameter in api documentation
	ParamDescription() string
}
```

### type ObjectConvertor interface
Convertor用来转换该GVK对应的Object，定义在/pkg/runtime/interfaces.go
```go
// ObjectConvertor converts an object to a different version.
/*
	ObjectConvertor将一个object转换为不同的版本。
*/
type ObjectConvertor interface {
	// Convert attempts to convert one object into another, or returns an error. This method does
	// not guarantee the in object is not mutated. The context argument will be passed to
	// all nested conversions.
	Convert(in, out, context interface{}) error
	// ConvertToVersion takes the provided object and converts it the provided version. This
	// method does not guarantee that the in object is not mutated. This method is similar to
	// Convert() but handles specific details of choosing the correct output version.
	ConvertToVersion(in Object, gv GroupVersioner) (out Object, err error)
	ConvertFieldLabel(version, kind, label, value string) (string, string, error)
}
```

### type MetadataAccessor interface
type MetadataAccessor interface可以让你在任何external version或者internal version中操作object和list这些metadata。

如果尝试在不支持该字段（名称，UID，列表上的命名空间）的对象上设置或检索字段，将是无效且返回默认值。

MetadataAccessor以一种方式暴露interface，可以被多个object使用。
```go
type MetadataAccessor interface {
	APIVersion(obj runtime.Object) (string, error)
	SetAPIVersion(obj runtime.Object, version string) error

	Kind(obj runtime.Object) (string, error)
	SetKind(obj runtime.Object, kind string) error

	Namespace(obj runtime.Object) (string, error)
	SetNamespace(obj runtime.Object, namespace string) error

	Name(obj runtime.Object) (string, error)
	SetName(obj runtime.Object, name string) error

	GenerateName(obj runtime.Object) (string, error)
	SetGenerateName(obj runtime.Object, name string) error

	UID(obj runtime.Object) (types.UID, error)
	SetUID(obj runtime.Object, uid types.UID) error

	SelfLink(obj runtime.Object) (string, error)
	SetSelfLink(obj runtime.Object, selfLink string) error

	Labels(obj runtime.Object) (map[string]string, error)
	SetLabels(obj runtime.Object, labels map[string]string) error

	Annotations(obj runtime.Object) (map[string]string, error)
	SetAnnotations(obj runtime.Object, annotations map[string]string) error

	runtime.ResourceVersioner
}

//runtime.Object 定义在/pkg/runtime/interfaces.go

// All API types registered with Scheme must support the Object interface. Since objects in a scheme are
// expected to be serialized to the wire, the interface an Object must provide to the Scheme allows
// serializers to set the kind, version, and group the object is represented as. An Object may choose
// to return a no-op ObjectKindAccessor in cases where it is not expected to be serialized.
/*
	译：在Scheme中注册的所有API类型都必须支持Object接口。
		这是因为在scheme中的objects是会被序列化成线的，所以一个Object必须提供接口给scheme来序列化地设置其kidn、version、group。
		在不需要序列化的情况下，Object可以选择返回一个无操作的ObjectKindAccessor。
*/
type Object interface {
	GetObjectKind() unversioned.ObjectKind
}

// ResourceVersioner provides methods for setting and retrieving
// the resource version from an API object.
//设置和接收一个API object的resource version
type ResourceVersioner interface {
	SetResourceVersion(obj Object, version string) error
	ResourceVersion(obj Object) (string, error)
}
```

## 总结
本章节主要是从APIRegistrationManager出发，然后主要讲解到了RESTMapper、Group、Version、Kind、Resource、RESTMapping等概念。
重点注意到是某个Group 的一个Version，而不是某个Version的一个Group。

这个和Apiserver对多版本的组织相关。
各个Group是相互独立的，发育程度也不一样，有快有慢，而kubernetes是以插件的形式来使用各个Group的。
这就提供了很大的灵活性。可以根据需求决定使用A Group的哪一个Version。

到这之后，是不是感觉数据结构太多了。。我们可以进行整理一下，如图所示。
![多个数据结构总结](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/StorageVersions-00.jpeg)

显然APIRegistrationManager是下一步要了解的入手点。
