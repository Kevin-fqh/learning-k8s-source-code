# 多版本资源注册-RESTMapper

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [RESTMapper函数](#restmapper函数)
  - [func NewDefaultRESTMapper](#func-newdefaultrestmapper)
    - [NewDefaultRESTMapperFromScheme](#newdefaultrestmapperfromscheme)
    - [小结](#小结)
  - [type DefaultRESTMapper struct](#type-defaultrestmapper-struct)
    - [NewDefaultRESTMapper](#newdefaultrestmapper)
    - [Add](#add)
    - [RESTMapping](#restmapping)
	- [KindFor](#kindfor)
	- [ResourceFor](#resourcefor)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

在前面[StorageVersions寻根]()一文中已经介绍了`RESTMapper、RESTMapping、RESTScope、ObjectConvertor和MetadataAccessor`等概念。

初始化apiserver core v1的过程中，生成groupMeta的时候用到`RESTMapper: newRESTMapper(externalVersions),`。我们从这里开始研究RESTMapper。

```go
/*
	RESTMapper其实包含的是一种转换关系，
	resource到kind，kind到resource，kind到scope的转换。
	resource还分单数和复数。

	kind和resource有什么区别呢？
	二者都是字符串，kind是通过Kind=reflector.TypeOf(&Pod{}).Elem().Name()进行取值，取得的就是Pod这个结构体的名字。
	             resource是通过plural, singular := KindToResource(kind)取值。
	singular是将Kind转换为小写字母，而plural是变为复数。

	示例：
	以Pod为例，Kind是{Group:"", Version: "v1", Kind: "Pod"},
	那么singular是{Group:"", Version: "v1", Kind: "pod"},
	plural则是{Group："", Version："v1", Resource:"pods"}。
	resource要区分单复数，是为了获取Pods信息。
	比如可以kubectl get pod，也可以kubectl get pods.
*/
```

## newRESTMapper函数
根据有无namespace，所有的api资源分为两类：
- RESTScopeNamespace,有namespace
- RESTScopeRoot,没有namespace,是API最顶层的对象
```go
func newRESTMapper(externalVersions []unversioned.GroupVersion) meta.RESTMapper {
	fmt.Println("调用func newRESTMapper")
	// the list of kinds that are scoped at the root of the api hierarchy
	// if a kind is not enumerated here, it is assumed to have a namespace scope
	/*
		译：在api层次结构根目录下的种类列表，如果没有在这里列出，则假定它具有命名空间范围
	*/
	//rootScoped枚举列出的是API最顶层的对象，可以理解为没有namespace的对象。
	rootScoped := sets.NewString(
		"Node",
		"Namespace",
		"PersistentVolume",
		"ComponentStatus",
	)

	// these kinds should be excluded from the list of resources
	/*
		ignoredKinds是下面接口需要用到的参数，表示遍历Scheme时忽略这些kinds。
	*/
	ignoredKinds := sets.NewString(
		"ListOptions",
		"DeleteOptions",
		"Status",
		"PodLogOptions",
		"PodExecOptions",
		"PodAttachOptions",
		"PodProxyOptions",
		"NodeProxyOptions",
		"ServiceProxyOptions",
		"ThirdPartyResource",
		"ThirdPartyResourceData",
		"ThirdPartyResourceList")

	/*
		调用api.NewDefaultRESTMapper()，
		==>定义在pkg/api/mapper.go
			==>func NewDefaultRESTMapper
		importPrefix 的值为："k8s.io/kubernetes/pkg/api"，
		externalVersions: [v1]
		interfacesFor是一个函数func interfacesFor(version unversioned.GroupVersion)
	*/
	mapper := api.NewDefaultRESTMapper(externalVersions, interfacesFor, importPrefix, ignoredKinds, rootScoped)

	return mapper
}
```

## func NewDefaultRESTMapper
其入参可以参考上面的注解，定义在pkg/api/mapper.go
```go
// Instantiates a DefaultRESTMapper based on types registered in api.Scheme
/*
	译：根据在api.Scheme中注册的types来实例化一个DefaultRESTMapper
*/
func NewDefaultRESTMapper(defaultGroupVersions []unversioned.GroupVersion, interfacesFunc meta.VersionInterfacesFunc,
	importPathPrefix string, ignoredKinds, rootScoped sets.String) *meta.DefaultRESTMapper {
	// 指定一个Scheme,并继续调用下面的接口
	return NewDefaultRESTMapperFromScheme(defaultGroupVersions, interfacesFunc, importPathPrefix, ignoredKinds, rootScoped, Scheme)
}
```
调用NewDefaultRESTMapperFromScheme时，新增了一个参数Scheme，这个入参Scheme和前面对`Scheme机制`进行介绍时所说的全局唯一的`api.Scheme`是同一个变量。
```go
//pkg/api/register.go
var Scheme = runtime.NewScheme()
```

### NewDefaultRESTMapperFromScheme
func NewDefaultRESTMapperFromScheme的流程总结：
1. 先创建了一个空的DefaultRESTMapper,
2. 然后根据"/api/v1"的groupVersion（只举了其中的一个groupversion，所以可以依据defaultGroupVersions来区别DefaultRESTMapper）,
3. 遍历Scheme中所有的kinds，
4. 接着再调用mapper.Add(gvk, scope)去填充这个mapper，
5. 最后返回该mapper。

整个Apiserver生成的mapper一共有13个，其对应的defaultGroupVersions分别是
```
[v1], [apps/v1beta1], [authentication.k8s.io/v1beta1], [authorization.k8s.io/v1beta1],
[autoscaling/v1], [batch/v1 batch/v2alpha1], [certificates.k8s.io/v1alpha1],
[componentconfig/v1alpha1], [extensions/v1beta1], [policy/v1beta1],
[rbac.authorization.k8s.io/v1alpha1], [storage.k8s.io/v1beta1], [imagepolicy.k8s.io/v1alpha1]
```
initial time的时候,mapper大部分属性值都是nil的，如下：
```
mapper is:  &{[{apps v1beta1}] map[] map[] map[] map[] map[] 0x1de32d0 map[]}
mapper is:  &{[{authorization.k8s.io v1beta1}] map[] map[] map[] map[] map[] 0x1de32d0 map[]}
```

输出每一个groupversion下的所有kind
```go
NewOrDie，创建了一个默认的APIRegistrationManager
初始化函数init
registered.RegisterVersions注册了availableVersions:  [v1]
/pkg/api/install/install.go externalVersions is : [v1]
再进行enable，其实就是存入APIRegistrationManager.enabledVersions
将所有的GroupVersions添加到Scheme
就是这里！ 进行了GroupMeta的初始化。关键之处
调用func newRESTMapper
defaultGroupVersions is:  [v1]
mapper is:  &{[{ v1}] map[] map[] map[] map[] map[] 0xebb920 map[]}
gv, kind is: v1 PersistentVolumeList
gv, kind is: v1 ResourceQuota
gv, kind is: v1 PodStatusResult
gv, kind is: v1 APIResourceList
gv, kind is: v1 RangeAllocation
gv, kind is: v1 Pod
gv, kind is: v1 PodTemplateList
gv, kind is: v1 PodProxyOptions
gv, kind is: v1 ReplicationController
gv, kind is: v1 ServiceList
gv, kind is: v1 ResourceQuotaList
gv, kind is: v1 ComponentStatusList
gv, kind is: v1 Node
gv, kind is: v1 Binding
gv, kind is: v1 APIGroup
gv, kind is: v1 SerializedReference
gv, kind is: v1 PodLogOptions
gv, kind is: v1 Service
gv, kind is: v1 ServiceAccountList
gv, kind is: v1 PodList
gv, kind is: v1 LimitRangeList
gv, kind is: v1 Status
gv, kind is: v1 EventList
gv, kind is: v1 ConfigMap
gv, kind is: v1 ConfigMapList
gv, kind is: v1 ExportOptions
gv, kind is: v1 NodeList
gv, kind is: v1 Event
gv, kind is: v1 PodExecOptions
gv, kind is: v1 ServiceProxyOptions
gv, kind is: v1 LimitRange
gv, kind is: v1 WatchEvent
gv, kind is: v1 PersistentVolume
gv, kind is: v1 ServiceAccount
gv, kind is: v1 ReplicationControllerList
gv, kind is: v1 APIGroupList
gv, kind is: v1 APIVersions
gv, kind is: v1 PodTemplate
gv, kind is: v1 NodeProxyOptions
gv, kind is: v1 NamespaceList
gv, kind is: v1 PersistentVolumeClaimList
gv, kind is: v1 ComponentStatus
gv, kind is: v1 ListOptions
gv, kind is: v1 DeleteOptions
gv, kind is: v1 Namespace
gv, kind is: v1 SecretList
gv, kind is: v1 Secret
gv, kind is: v1 List
gv, kind is: v1 Endpoints
gv, kind is: v1 PersistentVolumeClaim
gv, kind is: v1 PodAttachOptions
gv, kind is: v1 EndpointsList
前面都是register和enable了groupsversions字符串，这里才是真正进行了一个Group的register。
defaultGroupVersions is:  [apps/v1beta1]
mapper is:  &{[{apps v1beta1}] map[] map[] map[] map[] map[] 0x1de3480 map[]}
gv, kind is: apps/v1beta1 StatefulSet
gv, kind is: apps/v1beta1 ExportOptions
gv, kind is: apps/v1beta1 ListOptions
gv, kind is: apps/v1beta1 StatefulSetList
gv, kind is: apps/v1beta1 DeleteOptions
gv, kind is: apps/v1beta1 WatchEvent
defaultGroupVersions is:  [authentication.k8s.io/v1beta1]
mapper is:  &{[{authentication.k8s.io v1beta1}] map[] map[] map[] map[] map[] 0x1de3480 map[]}
gv, kind is: authentication.k8s.io/v1beta1 DeleteOptions
gv, kind is: authentication.k8s.io/v1beta1 ExportOptions
gv, kind is: authentication.k8s.io/v1beta1 ListOptions
gv, kind is: authentication.k8s.io/v1beta1 TokenReview
defaultGroupVersions is:  [authorization.k8s.io/v1beta1]
mapper is:  &{[{authorization.k8s.io v1beta1}] map[] map[] map[] map[] map[] 0x1de3480 map[]}
```

```go
// Instantiates a DefaultRESTMapper based on types registered in the given scheme.
/*
	译：基于指定的 Scheme 中注册的“types”实例化一个DefaultRESTMapper

	scope=RESTScopeNamespace或RESTScopeRoot

NewDefaultRESTMapperFromScheme()函数依据传入的defaultGroupVersions和interfacesFunc参数生成mapper，
然后把在Scheme中defaultGroupVersions下的资源注册到mapper中。
这里的Scheme即api.Scheme，全部的类型都会注册到api.Scheme中。
所以可以依据defaultGroupVersions来区别DefaultRESTMapper。
*/
func NewDefaultRESTMapperFromScheme(defaultGroupVersions []unversioned.GroupVersion, interfacesFunc meta.VersionInterfacesFunc,
	importPathPrefix string, ignoredKinds, rootScoped sets.String, scheme *runtime.Scheme) *meta.DefaultRESTMapper {

	/*
		初始化了一个DefaultRESTMapper对象
		meta.NewDefaultRESTMapper定义在/pkg/api/meta/restmapper.go
	*/
	mapper := meta.NewDefaultRESTMapper(defaultGroupVersions, interfacesFunc)
	fmt.Println("defaultGroupVersions is: ", reflect.ValueOf(defaultGroupVersions))
	fmt.Println("initial time, mapper is: ", reflect.ValueOf(mapper))
	// enumerate all supported versions, get the kinds, and register with the mapper how to address
	// our resources.
	/*
		译：遍历所有支持的versions，获取kinds，在mapper中注册如何去address our resource
	*/
	/*
		根据输入的defaultGroupVersions,比如"/api/v1"，
		从Scheme中遍历所有的kinds，
		然后进行Add
	*/
	for _, gv := range defaultGroupVersions {
		//从scheme获取一个指定GV的所有Type
		for kind, oType := range scheme.KnownTypes(gv) {
			fmt.Println("gv, kind is:", gv, kind)
			gvk := gv.WithKind(kind)
			// TODO: Remove import path check.
			// We check the import path because we currently stuff both "api" and "extensions" objects
			// into the same group within Scheme since Scheme has no notion of groups yet.
			/*
				译：检查‘import path’，因为我们目前将“api”和“extensions”对象同时包含在Scheme中，因为Scheme还没有groups的概念。
			*/
			/*
				过滤掉不属于"k8s.io/kubernetes/pkg/api"路径下的api，和ignoredKinds
			*/
			if !strings.Contains(oType.PkgPath(), importPathPrefix) || ignoredKinds.Has(kind) {
				continue
			}
			// 判断该kind是否有namespace属性
			scope := meta.RESTScopeNamespace
			if rootScoped.Has(kind) {
				scope = meta.RESTScopeRoot
			}
			/*
				然后将该gvk加入到对应的组中
				Add定义在/pkg/api/meta/restmapper.go
				==>func (m *DefaultRESTMapper) Add(kind unversioned.GroupVersionKind, scope RESTScope)
			*/
			mapper.Add(gvk, scope)
		}
	}
	return mapper
}
```
这里引出一个重要的数据结构，`type DefaultRESTMapper struct`，调用了
```go
NewDefaultRESTMapper(defaultGroupVersions, interfacesFunc)
func (m *DefaultRESTMapper) Add(kind unversioned.GroupVersionKind, scope RESTScope)
```
关于`type DefaultRESTMapper struct`会在下面进行详细的分析。

### 小结
至此，可以看到RESTMapper的初始化流程已经基本结束。到这里，结合前面的几篇初始化流程、Scheme机制来看，主要用internal version和external versions填充Scheme，用external versions去填充GroupMeta以及其成员RESTMapper。而GroupMeta有主要用于后期来初始化APIGroupVersion。


## type DefaultRESTMapper struct
用于暴露定义在runtime.Scheme中的那些“types”的映射关系。
实现了/pkg/meta/interfaces.go中type RESTMapper interface。

`type DefaultRESTMapper struct`用于管理所有对象的信息。
- 外部要获取的话，直接通过version,group获取到RESTMapper，
- 然后通过kind类型可以获取到对应的信息。
- groupMeta中的RESTMapper就是实现了一个DefaultRESTMapper结构。
- DefaultRESTMapper中的resource是指GVR，kind是指GVK
- singular和Plural都是GVR

```go
// DefaultRESTMapper exposes mappings between the types defined in a
// runtime.Scheme. It assumes that all types defined the provided scheme
// can be mapped with the provided MetadataAccessor and Codec interfaces.
//
// The resource name of a Kind is defined as the lowercase,
// English-plural version of the Kind string.
// When converting from resource to Kind, the singular version of the
// resource name is also accepted for convenience.
//
/*
译：DefaultRESTMapper 用于暴露定义在runtime.Scheme中的那些“types”的映射关系。
   它假设定义在 指定的Scheme 中的所有“types”，都可以使用指定的MetadataAccessor和Codec接口进行映射。

   一个Kind应该是单数的驼峰式，如Pod
   一个Kind的resource name 被定义为一个 小写的、复数的Kind字符串。如pods
   从resource转换为Kind时，为方便起见，也可以使用resource name的单数版本。
*/
// TODO: Only accept plural for some operations for increased control?
// (`get pod bar` vs `get pods bar`)

type DefaultRESTMapper struct {
	/*
		RESTMapper包含的是一种转换关系，
		resource到kind，kind到resource，kind到scope的转换。
		resource还分单数和复数(plural, singular)。

		kind和resource有什么区别呢？
		二者都是字符串，kind是通过Kind=reflector.TypeOf(&Pod{}).Elem().Name()进行取值，取得的就是Pod这个结构体的名字。
		             resource是通过plural, singular := KindToResource(kind)取值。
		singular是将Kind转换为小写字母，而plural是变为复数。

		Scope contains the information needed to deal with REST Resources that are in a resource hierarchy
	*/
	defaultGroupVersions []unversioned.GroupVersion

	resourceToKind       map[unversioned.GroupVersionResource]unversioned.GroupVersionKind
	kindToPluralResource map[unversioned.GroupVersionKind]unversioned.GroupVersionResource
	kindToScope          map[unversioned.GroupVersionKind]RESTScope
	singularToPlural     map[unversioned.GroupVersionResource]unversioned.GroupVersionResource
	pluralToSingular     map[unversioned.GroupVersionResource]unversioned.GroupVersionResource

	interfacesFunc VersionInterfacesFunc

	// aliasToResource is used for mapping aliases to resources
	aliasToResource map[string][]string
}
```
分析DefaultRESTMapper的字段的涵义：
- defaultGroupVersions: 默认的GroupVersion，如v1，apps/v1beta1等，一般一个DefaultRESTMapper只设一个默认的GroupVersion
- resourceToKind：GVR(单数,复数)到GVK的map；
- kindToPluralResource：GVK到GVR(复数)的map；
- kindToScope：GVK到Scope的map；
- singularToPlural：GVR(单数)到GVR(复数)的map；
- interfacesFunc：用来产生Convertor和MetadataAccessor，具体实现为/pkg/api/install/install.go中的interfacesFor()函数。
- aliasToResource：用于将别名映射到资源

现在来分析`type DefaultRESTMapper struct`提供的功能函数
### NewDefaultRESTMapper
NewDefaultRESTMapper()生成一个新的DefaultRESTMapper。
```go
/*
NewDefaultRESTMapper initializes a mapping 
between Kind and APIVersion to a resource name and back based on the objects in a runtime.Scheme and the Kubernetes API conventions.
 
Takes a priority list of the versions to search 
when an object has no default version (set empty to return an error) 
and a function that retrieves the correct codec and metadata for a given version.
*/

func NewDefaultRESTMapper(defaultGroupVersions []unversioned.GroupVersion, f VersionInterfacesFunc) *DefaultRESTMapper {
	resourceToKind := make(map[unversioned.GroupVersionResource]unversioned.GroupVersionKind)
	kindToPluralResource := make(map[unversioned.GroupVersionKind]unversioned.GroupVersionResource)
	kindToScope := make(map[unversioned.GroupVersionKind]RESTScope)
	singularToPlural := make(map[unversioned.GroupVersionResource]unversioned.GroupVersionResource)
	pluralToSingular := make(map[unversioned.GroupVersionResource]unversioned.GroupVersionResource)
	aliasToResource := make(map[string][]string)
	// TODO: verify name mappings work correctly when versions differ

	return &DefaultRESTMapper{
		resourceToKind:       resourceToKind,
		kindToPluralResource: kindToPluralResource,
		kindToScope:          kindToScope,
		defaultGroupVersions: defaultGroupVersions,
		singularToPlural:     singularToPlural,
		pluralToSingular:     pluralToSingular,
		aliasToResource:      aliasToResource,
		interfacesFunc:       f,
	}
}
```

### Add
Add()方法主要是把GVK（kind）和GVK对应的scope加入到DefaultRESTMapper对应的map属性中。
```go
/*
	Add adds objects from a runtime.Scheme and its named versions to this map.
	If mixedCase is true,
	the legacy v1beta1/v1beta2 Kubernetes resource naming convention will be applied (camelCase vs lowercase).
*/

func (m *DefaultRESTMapper) Add(kind unversioned.GroupVersionKind, scope RESTScope) {
	// resource分为复数和单数,根据gvk找到对应的GVR
	plural, singular := KindToResource(kind)

	// 单数，复数相互转换
	m.singularToPlural[singular] = plural
	m.pluralToSingular[plural] = singular
	// 根据单复数的resource找到对应的kind
	m.resourceToKind[singular] = kind
	m.resourceToKind[plural] = kind
	// 根据kind找到对应的单复数resource
	m.kindToPluralResource[kind] = plural
	// kind到scope的转换
	m.kindToScope[kind] = scope
	/*
		RESTMapper其实包含的是一种转换关系，
		resource到kind，kind到resource，kind到scope的转换。
		resource还分单数和复数。

		kind和resource有什么区别呢？
		二者都是字符串，kind是通过Kind=reflector.TypeOf(&Pod{}).Elem().Name()进行取值，取得的就是Pod这个结构体的名字。
		             resource是通过plural, singular := KindToResource(kind)取值。
		singular是将Kind转换为小写字母，而plural是变为复数。

		示例：
		以Pod为例，Kind是{Group:"", Version: "v1", Kind: "Pod"},
		那么singular是{Group:"", Version: "v1", Kind: "pod"},
		plural则是{Group："", Version："v1", Resource:"pods"}。
		resource要区分单复数，是为了获取Pods信息。
		比如可以kubectl get pod，也可以kubectl get pods.
	*/
}
```

### RESTMapping
```go
// RESTMapping returns a struct representing the resource path and conversion interfaces a
// RESTClient should use to operate on the provided group/kind in order of versions. If a version search
// order is not provided, the search order provided to DefaultRESTMapper will be used to resolve which
// version should be used to access the named group/kind.
// TODO: consider refactoring to use RESTMappings in a way that preserves version ordering and preference
/*
	RESTMapping()的参数是GK和versions，通常的做法是把一个GVK直接拆成GK和Version，然后获取mapping。

	RESTMapping()的流程如下：
		构造GVK：使用GK和Versions，或GK和DefaultGroupVersions，构造GVK；
		获取GVR：从kindToPluralResource中获取GVR；
		获取scope：从kindToScope中获取scope；
		使用interfacesFunc()获取Convertor和MetadataAccessor；
		组装成RESTMapping并返回。
*/
func (m *DefaultRESTMapper) RESTMapping(gk unversioned.GroupKind, versions ...string) (*RESTMapping, error) {
	// Pick an appropriate version
	var gvk *unversioned.GroupVersionKind
	hadVersion := false
	//构造GVK
	for _, version := range versions {
		if len(version) == 0 || version == runtime.APIVersionInternal {
			continue
		}

		currGVK := gk.WithVersion(version)
		hadVersion = true
		/*
			一旦找到合适的version，则break
			这些map变量都在func (m *DefaultRESTMapper) Add函数中完成值的添加
		*/
		if _, ok := m.kindToPluralResource[currGVK]; ok {
			gvk = &currGVK
			break
		}
	}
	// Use the default preferred versions
	if !hadVersion && (gvk == nil) {
		for _, gv := range m.defaultGroupVersions {
			if gv.Group != gk.Group {
				continue
			}

			currGVK := gk.WithVersion(gv.Version)
			if _, ok := m.kindToPluralResource[currGVK]; ok {
				gvk = &currGVK
				break
			}
		}
	}
	if gvk == nil {
		return nil, &NoKindMatchError{PartialKind: gk.WithVersion("")}
	}

	// Ensure we have a REST mapping
	/*
		根据GVK获取resource
		从原注释看，GVK和GVR的关系叫做REST mapping
	*/
	resource, ok := m.kindToPluralResource[*gvk]
	if !ok {
		found := []unversioned.GroupVersion{}
		for _, gv := range m.defaultGroupVersions {
			if _, ok := m.kindToPluralResource[*gvk]; ok {
				found = append(found, gv)
			}
		}
		if len(found) > 0 {
			return nil, fmt.Errorf("object with kind %q exists in versions %v, not %v", gvk.Kind, found, gvk.GroupVersion().String())
		}
		return nil, fmt.Errorf("the provided version %q and kind %q cannot be mapped to a supported object", gvk.GroupVersion().String(), gvk.Kind)
	}

	// Ensure we have a REST scope
	/*
		根据GVK获取scope
	*/
	scope, ok := m.kindToScope[*gvk]
	if !ok {
		return nil, fmt.Errorf("the provided version %q and kind %q cannot be mapped to a supported scope", gvk.GroupVersion().String(), gvk.Kind)
	}

	interfaces, err := m.interfacesFunc(gvk.GroupVersion())
	if err != nil {
		return nil, fmt.Errorf("the provided version %q has no relevant versions: %v", gvk.GroupVersion().String(), err)
	}

	/*
		构造RESTMapping
		RESTMapping中有resource(名称), GVK, scope, convertor, accessor
	*/
	retVal := &RESTMapping{
		/*
			系统会先读取/root/.kube/config文件，然后注入RESTMapping，
			再生成一个client的config，然后用该config生成client，类似restclient之类的
			需要去看kubectl的源码
		*/
		Resource:         resource.Resource,
		GroupVersionKind: *gvk,
		Scope:            scope,

		ObjectConvertor:  interfaces.ObjectConvertor,
		MetadataAccessor: interfaces.MetadataAccessor,
	}

	return retVal, nil
}
```

### KindFor
```go
/*
	KindFor()通过GVR(信息不一定要全)找到一个最合适的注册的GVK。规则和ResourceFor()一样：
		如果参数GVR没有有Resource，则返回错误。
		如果参数GVR限定Group，Version和Resource，则匹配Group，Version和Resource；
		如果参数GVR限定Group和Resource，则匹配Group和Resource；
		如果参数GVR限定Version和Resource，则匹配Version和Resource；
		如果参数GVR只有Resource，则匹配Resource。
		如果系统中存在多个匹配，则返回错误(系统现在还不支持在不同的Group中定义相同的type)。
	规则需要把KindFor和KindsFor函数结合起来看
*/
func (m *DefaultRESTMapper) KindFor(resource unversioned.GroupVersionResource) (unversioned.GroupVersionKind, error) {
	/*
		调用func (m *DefaultRESTMapper) KindsFor(input unversioned.GroupVersionResource)
	*/
	kinds, err := m.KindsFor(resource)
	if err != nil {
		return unversioned.GroupVersionKind{}, err
	}
	if len(kinds) == 1 {
		return kinds[0], nil
	}

	return unversioned.GroupVersionKind{}, &AmbiguousResourceError{PartialResource: resource, MatchingKinds: kinds}
}

/通过GVR获取GVK
func (m *DefaultRESTMapper) KindsFor(input unversioned.GroupVersionResource) ([]unversioned.GroupVersionKind, error) {

	resource := coerceResourceForMatching(input)

	hasResource := len(resource.Resource) > 0
	hasGroup := len(resource.Group) > 0
	hasVersion := len(resource.Version) > 0

	if !hasResource {
		return nil, fmt.Errorf("a resource must be present, got: %v", resource)
	}

	ret := []unversioned.GroupVersionKind{}
	switch {
	// fully qualified.  Find the exact match
	case hasGroup && hasVersion:
		kind, exists := m.resourceToKind[resource]
		if exists {
			ret = append(ret, kind)
		}

	case hasGroup:
		foundExactMatch := false
		requestedGroupResource := resource.GroupResource()
		for currResource, currKind := range m.resourceToKind {
			if currResource.GroupResource() == requestedGroupResource {
				foundExactMatch = true
				ret = append(ret, currKind)
			}
		}

		// if you didn't find an exact match, match on group prefixing. This allows storageclass.storage to match
		// storageclass.storage.k8s.io
		if !foundExactMatch {
			for currResource, currKind := range m.resourceToKind {
				if !strings.HasPrefix(currResource.Group, requestedGroupResource.Group) {
					continue
				}
				if currResource.Resource == requestedGroupResource.Resource {
					ret = append(ret, currKind)
				}
			}

		}

	case hasVersion:
		for currResource, currKind := range m.resourceToKind {
			if currResource.Version == resource.Version && currResource.Resource == resource.Resource {
				ret = append(ret, currKind)
			}
		}

	default:
		for currResource, currKind := range m.resourceToKind {
			if currResource.Resource == resource.Resource {
				ret = append(ret, currKind)
			}
		}
	}

	if len(ret) == 0 {
		return nil, &NoResourceMatchError{PartialResource: input}
	}

	sort.Sort(kindByPreferredGroupVersion{ret, m.defaultGroupVersions})
	return ret, nil
}
```

### ResourceFor
```go
/*
	通过GVR(信息不一定要全)找到一个最合适的注册的GVR：
		如果参数GVR没有有Resource，则返回错误。
		如果参数GVR限定Group，Version和Resource，则匹配Group，Version和Resource；
		如果参数GVR限定Group和Resource，则匹配Group和Resource；
		如果参数GVR限定Version和Resource，则匹配Version和Resource；
		如果参数GVR只有Resource，则匹配Resource。
		如果系统中存在多个匹配，则返回错误(系统现在还不支持在不同的Group中定义相同的type)
*/
func (m *DefaultRESTMapper) ResourceFor(resource unversioned.GroupVersionResource) (unversioned.GroupVersionResource, error) {
	//调用func (m *DefaultRESTMapper) ResourcesFor
	resources, err := m.ResourcesFor(resource)
	if err != nil {
		return unversioned.GroupVersionResource{}, err
	}
	/*
		如果系统中只存在一个匹配值，则返回该resource
		如果系统中存在多个匹配，则返回错误
	*/
	if len(resources) == 1 {
		return resources[0], nil
	}

	return unversioned.GroupVersionResource{}, &AmbiguousResourceError{PartialResource: resource, MatchingResources: resources}
}

func (m *DefaultRESTMapper) ResourcesFor(input unversioned.GroupVersionResource) ([]unversioned.GroupVersionResource, error) {
	//根据input unversioned.GroupVersionResource获取GVR
	resource := coerceResourceForMatching(input)

	hasResource := len(resource.Resource) > 0
	hasGroup := len(resource.Group) > 0
	hasVersion := len(resource.Version) > 0

	if !hasResource {
		return nil, fmt.Errorf("a resource must be present, got: %v", resource)
	}

	ret := []unversioned.GroupVersionResource{}
	switch {
	//限定group和version，则比较GVR
	case hasGroup && hasVersion:
		// fully qualified.  Find the exact match
		for plural, singular := range m.pluralToSingular {
			if singular == resource {
				ret = append(ret, plural)
				break
			}
			if plural == resource {
				ret = append(ret, plural)
				break
			}
		}

	//只限定group，则比较GR
	case hasGroup:
		// given a group, prefer an exact match.  If you don't find one, resort to a prefix match on group
		foundExactMatch := false
		requestedGroupResource := resource.GroupResource()
		for plural, singular := range m.pluralToSingular {
			if singular.GroupResource() == requestedGroupResource {
				foundExactMatch = true
				ret = append(ret, plural)
			}
			if plural.GroupResource() == requestedGroupResource {
				foundExactMatch = true
				ret = append(ret, plural)
			}
		}

		// if you didn't find an exact match, match on group prefixing. This allows storageclass.storage to match
		// storageclass.storage.k8s.io
		if !foundExactMatch {
			for plural, singular := range m.pluralToSingular {
				if !strings.HasPrefix(plural.Group, requestedGroupResource.Group) {
					continue
				}
				if singular.Resource == requestedGroupResource.Resource {
					ret = append(ret, plural)
				}
				if plural.Resource == requestedGroupResource.Resource {
					ret = append(ret, plural)
				}
			}

		}

	//限定version，则比较version和resource
	case hasVersion:
		for plural, singular := range m.pluralToSingular {
			if singular.Version == resource.Version && singular.Resource == resource.Resource {
				ret = append(ret, plural)
			}
			if plural.Version == resource.Version && plural.Resource == resource.Resource {
				ret = append(ret, plural)
			}
		}

	//只比较Resource
	default:
		for plural, singular := range m.pluralToSingular {
			if singular.Resource == resource.Resource {
				ret = append(ret, plural)
			}
			if plural.Resource == resource.Resource {
				ret = append(ret, plural)
			}
		}
	}

	if len(ret) == 0 {
		return nil, &NoResourceMatchError{PartialResource: resource}
	}

	sort.Sort(resourceByPreferredGroupVersion{ret, m.defaultGroupVersions})
	return ret, nil
}
```

## 总结
本文主要讲解了RESTMapper的初始化，分析了Apiserver启动过程中，加载13个groupVersion对RESTMapper的初始化过程。然后重点分析了type DefaultRESTMapper struct的定义及其实现的几个功能函数。

到这里，可以说从`/pkg/genericapiserver/options/server_run_options.go`中`registered.AllPreferredGroupVersions(),`延伸出来的流程和概念，基本是告一段落了。

后面将对APiserver如何使用Scheme和Restmapper进行Restful API的注册进行讲解。

RESTMapper用于管理所有对象的信息。
外部要获取的话，直接通过version，group获取到RESTMapper，然后通过kind类型可以获取到相对应的信息。
kubectl等组件就是通过这种方式获取的。

## 参考
[package-meta](https://godoc.org/github.com/ukai/kubernetes-0/pkg/api/meta)

