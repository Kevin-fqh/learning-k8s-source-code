# Apiserver之多版本资源注册-apimachinery机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引子](#引子)
  - [type GroupMeta struct](#type-groupmeta-struct)
  - [type APIRegistrationManager struct](#type-apiregistrationmanager-struct)
    - [NewAPIRegistrationManager](#newapiregistrationmanager)
	- [RegisterVersions](#registerversions)
	- [RegisterGroup](#registergroup)
	- [EnableVersions](#enableversions)
	- [IsAllowedVersion](#isallowedversion)
	- [IsEnabledVersion](#isenabledversion)
	- [EnabledVersionsForGroup](#enabledversionsforgroup)
	- [Group](#group)
	- [其它，AddThirdPartyAPIGroupVersions](#其它，addthirdpartyapigroupversions)
	- [RESTMapper](#restmapper)
	- [AllPreferredGroupVersions](#allpreferredgroupversions)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文主要介绍apimachinery package中的`APIRegistrationManager`。
APIRegistrationManager负责对外提供已经注册并enable了的GroupVersions。

然后还介绍了type GroupMeta struct的定义。

## 引子
```
pkg/apimachinery	Package apimachinery contains the generic API machinery code that is common to both server and clients.
pkg/apimachinery/announced	Package announced contains tools for announcing API group factories.
pkg/apimachinery/registered	Package to keep track of API Versions that can be registered and are enabled in api.Scheme.
```
- Package apimachinery是负责管理k8s的多版本API的，这是一套通用机制。
- Package announced提供announcing API group factories的接口。
- Package registered 负责管理那些能够在api.Scheme中进行注册和enable的API Versions。

下面分别对三者进行一一解读。
```
                     -------package announced
                     |
                     |
package apimachinery---------package registered---type APIRegistrationManager struct
                     |
                     |
                     -----type.go					  
```

## type GroupMeta struct
GroupMeta stores the metadata of a group。其定义见/pkg/apimachinery/types.go
```go
/*
	type GroupMeta struct 简介：
	主要包括Group的元信息。

	GroupMeta的成员RESTMapper，与APIGroupVersion中的成员RESTMapper值是一样的，
	其实APIGroupVersion的RESTMapper直接取值于GroupMeta的RESTMapper.
	APIGroupVersion在介绍master的时候会进行介绍。

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
	/*
		SelfLinker用于set或者get所有API types的SelfLink字段
	*/
	SelfLinker runtime.SelfLinker

	// RESTMapper provides the default mapping between REST paths and the objects declared in api.Scheme and all known
	// versions.
	/*
		译：RESTMapper提供 REST路径 与 那些在api.Scheme和所有已知版本中声明的对象 之间的默认映射。

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
	/*
		接口InterfacesFor用于返回指定version的默认 Codec and ResourceVersioner
	*/
	InterfacesFor func(version unversioned.GroupVersion) (*meta.VersionInterfaces, error)

	// InterfacesByVersion stores the per-version interfaces.
	/*
		存储每一个version的接口
		meta.VersionInterfaces定义在/pkg/api/meta/interfaces.go
			==>type VersionInterfaces struct
	*/
	InterfacesByVersion map[unversioned.GroupVersion]*meta.VersionInterfaces
}

// VersionInterfaces contains the interfaces one should use for dealing with types of a particular version.
/*
	type VersionInterfaces struct 包含一个用于处理一个特定version的types的接口
*/
type VersionInterfaces struct {
	runtime.ObjectConvertor
	MetadataAccessor //
}
```
关于的介绍，可以查看[StorageVersions寻根]()一文。

查看type GroupMeta struct其实现的函数功能
```go
/*
	func (gm *GroupMeta) DefaultInterfacesFor是接口InterfacesFor的实现，
	用于返回指定version的默认 Codec and ResourceVersioner
*/
func (gm *GroupMeta) DefaultInterfacesFor(version unversioned.GroupVersion) (*meta.VersionInterfaces, error) {
	if v, ok := gm.InterfacesByVersion[version]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("unsupported storage version: %s (valid: %v)", version, gm.GroupVersions)
}

/*
	func AddVersionInterfaces 把指定的version添加到group中。
	本函数只能在初始化的时候被调用，在之后的时间里面，GroupMeta的数据是不可变的。
	不是线程安全的。
	如果要使用本函数，必须设置 .InterfacesFor = .DefaultInterfacesFor
*/
func (gm *GroupMeta) AddVersionInterfaces(version unversioned.GroupVersion, interfaces *meta.VersionInterfaces) error {
	if e, a := gm.GroupVersion.Group, version.Group; a != e {
		return fmt.Errorf("got a version in group %v, but am in group %v", a, e)
	}
	if gm.InterfacesByVersion == nil {
		gm.InterfacesByVersion = make(map[unversioned.GroupVersion]*meta.VersionInterfaces)
	}
	gm.InterfacesByVersion[version] = interfaces

	// TODO: refactor to make the below error not possible, this function
	// should *set* GroupVersions rather than depend on it.
	for _, v := range gm.GroupVersions {
		if v == version {
			return nil
		}
	}
	return fmt.Errorf("added a version interface without the corresponding version %v being in the list %#v", version, gm.GroupVersions)
}
```

## type APIRegistrationManager struct
介绍package registered其实就是介绍type APIRegistrationManager struct。

APIRegistrationManager负责对外提供已经注册并enable了的GroupVersions。
这在前面[StorageVersions寻根]()一文也提到过。
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

下面开始分析type APIRegistrationManager struct实现的函数功能，这里只是把其所有函数函数功能进行列表性介绍，不涉及调用等逻辑关系。

### NewAPIRegistrationManager
New一个APIRegistrationManager对象
```
/*
	包级变量，
	初始化了一个DefaultAPIRegistrationManager对象
*/
var (
	DefaultAPIRegistrationManager = NewOrDie(os.Getenv("KUBE_API_VERSIONS"))
)

func NewOrDie(kubeAPIVersions string) *APIRegistrationManager {
	/*
		调用type APIRegistrationManager struct的初始化函数
	*/
	fmt.Println("NewOrDie，创建了一个默认的APIRegistrationManager")
	m, err := NewAPIRegistrationManager(kubeAPIVersions)
	if err != nil {
		glog.Fatalf("Could not construct version manager: %v (KUBE_API_VERSIONS=%q)", err, kubeAPIVersions)
	}
	return m
}

// NewAPIRegistrationManager constructs a new manager. The argument ought to be
// the value of the KUBE_API_VERSIONS env var, or a value of this which you
// wish to test.
/*
	译：NewAPIRegistrationManager构造一个新的管理器。
	   入口参数应该是KUBE_API_VERSIONS env 的值，或者是您希望测试的值。
*/
func NewAPIRegistrationManager(kubeAPIVersions string) (*APIRegistrationManager, error) {
	m := &APIRegistrationManager{
		registeredVersions:      map[unversioned.GroupVersion]struct{}{},
		thirdPartyGroupVersions: []unversioned.GroupVersion{},
		enabledVersions:         map[unversioned.GroupVersion]struct{}{},
		groupMetaMap:            map[string]*apimachinery.GroupMeta{},
		envRequestedVersions:    []unversioned.GroupVersion{},
	}

	// 如果对环境变量KUBE_API_VERSIONS进行了设置的话，进行遍历
	if len(kubeAPIVersions) != 0 {
		// 通过逗号进行分隔
		for _, version := range strings.Split(kubeAPIVersions, ",") {
			/*
				解析version并转换成GroupVersion格式
				一般这里的version是group/version格式，比如'/api/v1'
			*/
			gv, err := unversioned.ParseGroupVersion(version)
			if err != nil {
				return nil, fmt.Errorf("invalid api version: %s in KUBE_API_VERSIONS: %s.",
					version, kubeAPIVersions)
			}
			// 然后将该gv放入envRequestedVersions
			m.envRequestedVersions = append(m.envRequestedVersions, gv)
		}
	}
	/*
		 	如果没有对环境变量KUBE_API_VERSIONS进行设置的话，返回一个空的APIRegistrationManager
			默认情况下就是没有对其进行设置！！！
			那么就需要考虑到底是在哪里对groupMeta进行register & enable？？？？
				===>查看type APIRegistrationManager struct的方法
					==>func (m *APIRegistrationManager) RegisterGroup(groupMeta apimachinery.GroupMeta)
	*/
	glog.Infof("没有对环境变量KUBE_API_VERSIONS进行设置")
	return m, nil
}
```
### RegisterVersions  
向APIRegistrationManager注册GroupVersion
```go
// RegisterVersions adds the given group versions to the list of registered group versions.
/*
	译：RegisterVersions将指定的一个group versions添加到map registeredVersions，
		向APIRegistrationManager注册GroupVersion
*/
func (m *APIRegistrationManager) RegisterVersions(availableVersions []unversioned.GroupVersion) {
	for _, v := range availableVersions {
		m.registeredVersions[v] = struct{}{}
	}
}
```

### RegisterGroup  
向APIRegistrationManager注册groupMeta
```go
// RegisterGroup adds the given group to the list of registered groups.
/*
	向APIRegistrationManager注册一个group，即把groupMeta存入groupMetaMap中
*/
func (m *APIRegistrationManager) RegisterGroup(groupMeta apimachinery.GroupMeta) error {
	/*
		func (m *APIRegistrationManager) RegisterGroup(groupMeta apimachinery.GroupMeta)
		的入参就是GroupMeta,所以需要知道哪里调用了本函数？？继续查看GroupMeta结构的初始化。
			==>查看 pkg/master/import_known_versions.go
		或者模仿上面一堆函数面值的调用方法全局搜索关键字 registered.RegisterGroup，查看哪里调用了本函数？
	*/
	groupName := groupMeta.GroupVersion.Group
	if _, found := m.groupMetaMap[groupName]; found {
		return fmt.Errorf("group %v is already registered", m.groupMetaMap)
	}
	//把入参groupMeta赋值给APIRegistrationManager，完成一个group的注册
	m.groupMetaMap[groupName] = &groupMeta
	return nil
}
```

### EnableVersions  
设置一个GroupVersion为enable，激活它
```go
// EnableVersions adds the versions for the given group to the list of enabled versions.
// Note that the caller should call RegisterGroup before calling this method.
// The caller of this function is responsible to add the versions to scheme and RESTMapper.
/*
	译：EnableVersions将指定的组的版本添加到the list of enabled versions。
		请注意，在调用此方法之前，调用者应调用 RegisterGroup。
		本函数的调用者负责将versions添加到scheme和RESTMapper。
*/
func (m *APIRegistrationManager) EnableVersions(versions ...unversioned.GroupVersion) error {
	var unregisteredVersions []unversioned.GroupVersion
	for _, v := range versions {
		if _, found := m.registeredVersions[v]; !found {
			unregisteredVersions = append(unregisteredVersions, v)
		}
		m.enabledVersions[v] = struct{}{}
	}
	if len(unregisteredVersions) != 0 {
		return fmt.Errorf("Please register versions before enabling them: %v", unregisteredVersions)
	}
	return nil
}
```

### IsAllowedVersion
```go
// IsAllowedVersion returns if the version is allowed by the KUBE_API_VERSIONS
// environment variable. If the environment variable is empty, then it always
// returns true.
/*
	译：IsAllowedVersion返回的结果表示：环境变量KUBE_API_VERSIONS是否允许该version。
		如果环境变量KUBE_API_VERSIONS为空，则始终返回true。
*/
func (m *APIRegistrationManager) IsAllowedVersion(v unversioned.GroupVersion) bool {
	if len(m.envRequestedVersions) == 0 {
		return true
	}
	for _, envGV := range m.envRequestedVersions {
		if v == envGV {
			return true
		}
	}
	return false
}
```

### IsEnabledVersion
```go
// IsEnabledVersion returns if a version is enabled.
func (m *APIRegistrationManager) IsEnabledVersion(v unversioned.GroupVersion) bool {
	_, found := m.enabledVersions[v]
	return found
}
```

### EnabledVersions
```go
// EnabledVersions returns all enabled versions.  Groups are randomly ordered, but versions within groups
// are priority order from best to worst
/*
	EnabledVersions返回所有已enabled的版本。
	Groups是随机排序的，但一个Group内的Version是从最佳到最差的最优排序
*/
func (m *APIRegistrationManager) EnabledVersions() []unversioned.GroupVersion {
	ret := []unversioned.GroupVersion{}
	for _, groupMeta := range m.groupMetaMap {
		for _, version := range groupMeta.GroupVersions {
			if m.IsEnabledVersion(version) {
				ret = append(ret, version)
			}
		}
	}
	return ret
}
```

### EnabledVersionsForGroup
```go
// EnabledVersionsForGroup returns all enabled versions for a group in order of best to worst
/*
	返回某一个group下的enabled的GroupVersion，同样是排在前面是最佳的Version
*/
func (m *APIRegistrationManager) EnabledVersionsForGroup(group string) []unversioned.GroupVersion {
	groupMeta, ok := m.groupMetaMap[group]
	if !ok {
		return []unversioned.GroupVersion{}
	}

	ret := []unversioned.GroupVersion{}
	for _, version := range groupMeta.GroupVersions {
		if m.IsEnabledVersion(version) {
			ret = append(ret, version)
		}
	}
	return ret
}
```

### Group
```go
// Group returns the metadata of a group if the group is registered, otherwise
// an error is returned.
/*
	返回一个已经registered了的group的metadata
*/
func (m *APIRegistrationManager) Group(group string) (*apimachinery.GroupMeta, error) {
	groupMeta, found := m.groupMetaMap[group]
	if !found {
		return nil, fmt.Errorf("group %v has not been registered", group)
	}
	groupMetaCopy := *groupMeta
	return &groupMetaCopy, nil
}
```

太多了，一次性贴出来，其中有一个和第三方Groupversion相关的，比较重要，可以通过它来实现用户自主添加API资源。
### 其它，AddThirdPartyAPIGroupVersions
```go
// IsRegistered takes a string and determines if it's one of the registered groups
/*
	查看某一group是否已经注册。
*/
func (m *APIRegistrationManager) IsRegistered(group string) bool {
	_, found := m.groupMetaMap[group]
	return found
}

// IsRegisteredVersion returns if a version is registered.
/*
	查看某一GroupVersion是否已经注册。
*/
func (m *APIRegistrationManager) IsRegisteredVersion(v unversioned.GroupVersion) bool {
	_, found := m.registeredVersions[v]
	return found
}

// RegisteredGroupVersions returns all registered group versions.
/*
	返回所有已经registered的group versions
*/
func (m *APIRegistrationManager) RegisteredGroupVersions() []unversioned.GroupVersion {
	ret := []unversioned.GroupVersion{}
	for groupVersion := range m.registeredVersions {
		ret = append(ret, groupVersion)
	}
	return ret
}

// IsThirdPartyAPIGroupVersion returns true if the api version is a user-registered group/version.
/*
	判断是否是第三方的GroupVersion。
*/
func (m *APIRegistrationManager) IsThirdPartyAPIGroupVersion(gv unversioned.GroupVersion) bool {
	for ix := range m.thirdPartyGroupVersions {
		if m.thirdPartyGroupVersions[ix] == gv {
			return true
		}
	}
	return false
}

// AddThirdPartyAPIGroupVersions sets the list of third party versions,
// registers them in the API machinery and enables them.
// Skips GroupVersions that are already registered.
// Returns the list of GroupVersions that were skipped.
/*

	AddThirdPartyAPIGroupVersions()负责管理第三方的GroupVersion。
	可以向APIRegistrationManager注册和enable第三方的GroupVersion。
	略过已经注册过了的GroupVersion，最后return这些略过的GroupVersions。
*/
func (m *APIRegistrationManager) AddThirdPartyAPIGroupVersions(gvs ...unversioned.GroupVersion) []unversioned.GroupVersion {
	filteredGVs := []unversioned.GroupVersion{}
	skippedGVs := []unversioned.GroupVersion{}
	for ix := range gvs {
		if !m.IsRegisteredVersion(gvs[ix]) {
			filteredGVs = append(filteredGVs, gvs[ix])
		} else {
			glog.V(3).Infof("Skipping %s, because its already registered", gvs[ix].String())
			skippedGVs = append(skippedGVs, gvs[ix])
		}
	}
	if len(filteredGVs) == 0 {
		return skippedGVs
	}
	/*
		先调用RegistrerVersions()，
		再调用EnableVersions，
		加入到thirdPartyGroupVersions字段中
	*/
	m.RegisterVersions(filteredGVs)
	m.EnableVersions(filteredGVs...)
	m.thirdPartyGroupVersions = append(m.thirdPartyGroupVersions, filteredGVs...)

	return skippedGVs
}

// InterfacesFor is a union meta.VersionInterfacesFunc func for all registered types
/*
	一个针对所有已经注册了的GroupVersion的接口，用于获取一个GroupVersion的InterfacesFor
*/
func (m *APIRegistrationManager) InterfacesFor(version unversioned.GroupVersion) (*meta.VersionInterfaces, error) {
	/*
		先获取GV对应的groupMeta，然后通过groupMeta的InterfacesFor接口获取VersionInterfaces
	*/
	groupMeta, err := m.Group(version.Group)
	if err != nil {
		return nil, err
	}
	return groupMeta.InterfacesFor(version)
}

// TODO: This is an expedient function, because we don't check if a Group is
// supported throughout the code base. We will abandon this function and
// checking the error returned by the Group() function.
/*
	获取一个group对应的GroupMeta。
	本接口后面会被abandon掉
	
	会在后面初始化创建一个APIGroupInfo的时候被使用
		==>/pkg/registry/core/rest/storage_core.go
			==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
*/
func (m *APIRegistrationManager) GroupOrDie(group string) *apimachinery.GroupMeta {
	groupMeta, found := m.groupMetaMap[group]
	if !found {
		if group == "" {
			panic("The legacy v1 API is not registered.")
		} else {
			panic(fmt.Sprintf("Group %s is not registered.", group))
		}
	}
	groupMetaCopy := *groupMeta
	return &groupMetaCopy
}
```

然后还有一个`func (m *APIRegistrationManager) RESTMapper`很重要。
### RESTMapper
```go
/*
	func (m *APIRegistrationManager) RESTMapper返回一个RESTMapper聚合体，按下面的顺序进行最优排列:
	1: if KUBE_API_VERSIONS is specified, then KUBE_API_VERSIONS in order, OR
	2: legacy kube group preferred version, extensions preferred version, metrics perferred version, legacy
       kube any version, extensions any version, metrics any version, all other groups alphabetical preferred version,
       all other groups alphabetical.
*/
func (m *APIRegistrationManager) RESTMapper(versionPatterns ...unversioned.GroupVersion) meta.RESTMapper {
	/*
		遍历map enabledVersions，
		把每一个Group中groupMeta的RESTMapper收集起来，append到unionMapper。

		在后面return的时候，会把unionMapper封装成PriorityRESTMapper
	*/
	unionMapper := meta.MultiRESTMapper{}
	unionedGroups := sets.NewString()
	for enabledVersion := range m.enabledVersions {
		if !unionedGroups.Has(enabledVersion.Group) {
			unionedGroups.Insert(enabledVersion.Group)
			groupMeta := m.groupMetaMap[enabledVersion.Group]
			unionMapper = append(unionMapper, groupMeta.RESTMapper)
		}
	}

	//如果versionPatterns不为空，则使用versionPatterns作为优先级依据
	if len(versionPatterns) != 0 {
		resourcePriority := []unversioned.GroupVersionResource{}
		kindPriority := []unversioned.GroupVersionKind{}
		for _, versionPriority := range versionPatterns {
			resourcePriority = append(resourcePriority, versionPriority.WithResource(meta.AnyResource))
			kindPriority = append(kindPriority, versionPriority.WithKind(meta.AnyKind))
		}

		return meta.PriorityRESTMapper{Delegate: unionMapper, ResourcePriority: resourcePriority, KindPriority: kindPriority}
	}

	//如果envRequestedVersions不为空，则使用envRequestedVersions作为优先级依据
	if len(m.envRequestedVersions) != 0 {
		resourcePriority := []unversioned.GroupVersionResource{}
		kindPriority := []unversioned.GroupVersionKind{}

		for _, versionPriority := range m.envRequestedVersions {
			resourcePriority = append(resourcePriority, versionPriority.WithResource(meta.AnyResource))
			kindPriority = append(kindPriority, versionPriority.WithKind(meta.AnyKind))
		}

		return meta.PriorityRESTMapper{Delegate: unionMapper, ResourcePriority: resourcePriority, KindPriority: kindPriority}
	}

	//使用默认的优先级
	prioritizedGroups := []string{"", "extensions", "metrics"}
	resourcePriority, kindPriority := m.prioritiesForGroups(prioritizedGroups...)

	prioritizedGroupsSet := sets.NewString(prioritizedGroups...)
	remainingGroups := sets.String{}
	for enabledVersion := range m.enabledVersions {
		if !prioritizedGroupsSet.Has(enabledVersion.Group) {
			remainingGroups.Insert(enabledVersion.Group)
		}
	}

	remainingResourcePriority, remainingKindPriority := m.prioritiesForGroups(remainingGroups.List()...)
	resourcePriority = append(resourcePriority, remainingResourcePriority...)
	kindPriority = append(kindPriority, remainingKindPriority...)

	return meta.PriorityRESTMapper{Delegate: unionMapper, ResourcePriority: resourcePriority, KindPriority: kindPriority}
}

// prioritiesForGroups returns the resource and kind priorities for a PriorityRESTMapper, preferring the preferred version of each group first,
// then any non-preferred version of the group second.
/*
	PREFERREDForGroups返回PriorityRESTMapper的resource和resource，
	首先优先选择每个Group的首选Version，
	然后再选择该组的任何非首选版本。
*/
func (m *APIRegistrationManager) prioritiesForGroups(groups ...string) ([]unversioned.GroupVersionResource, []unversioned.GroupVersionKind) {
	resourcePriority := []unversioned.GroupVersionResource{}
	kindPriority := []unversioned.GroupVersionKind{}

	for _, group := range groups {
		availableVersions := m.EnabledVersionsForGroup(group)
		if len(availableVersions) > 0 {
			resourcePriority = append(resourcePriority, availableVersions[0].WithResource(meta.AnyResource))
			kindPriority = append(kindPriority, availableVersions[0].WithKind(meta.AnyKind))
		}
	}
	for _, group := range groups {
		resourcePriority = append(resourcePriority, unversioned.GroupVersionResource{Group: group, Version: meta.AnyVersion, Resource: meta.AnyResource})
		kindPriority = append(kindPriority, unversioned.GroupVersionKind{Group: group, Version: meta.AnyVersion, Kind: meta.AnyKind})
	}

	return resourcePriority, kindPriority
}
```

### AllPreferredGroupVersions
AllPreferredGroupVersions，在前面[StorageVersions寻根]()提到过。
Apiserver通过它来获取所有Group的首选version。
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

		func (m *APIRegistrationManager) AllPreferredGroupVersions() 比较简单，
		就是从m.groupMetaMap中取出所有的groupMeta，
		然后通过逗号拼接成"group1/version1,group2/version2,..."的字符串。
		那么m *APIRegistrationManager的groupMeta哪里来的？

		这里既然有对groupMetaMap的遍历，那总得有groupMeta啊。
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

## 总结
本文主要介绍apimachinery package中的`APIRegistrationManager`。
然后还介绍了type GroupMeta struct的定义。

下文准备讲解是在Apiserver启动过程中是如何初始化`APIRegistrationManager`的groupMetaMap成员的。

## 参考
[/package apimachinery](https://godoc.org/k8s.io/apimachinery/pkg/apimachinery) 
