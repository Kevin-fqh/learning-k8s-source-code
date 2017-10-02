# Apiserver之多版本资源注册-Scheme-1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [全局唯一的Scheme对象](#全局唯一的scheme对象)
  - [Scheme的定义](#scheme的定义)
    - [type Scheme struct](#type-scheme-struct)
  - [NewScheme()函数](#newscheme()函数)

  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文主要对Scheme的定义和提供的功能函数进行介绍。

在初始化apiserver core v1的过程中，`addVersionsToScheme(externalVersions...)`，将所有的GroupVersions添加到Scheme。那么Scheme的定义和作用是什么呢？

## 全局唯一的Scheme对象
Apiserver全局范围内，只有一个Scheme，即api.Scheme。
所有的GroupVersion受这个api.Scheme管理。
定义在pkg/api/register.go。
```go
// Scheme is the default instance of runtime.Scheme to which types in the Kubernetes API are already registered.
// NOTE: If you are copying this file to start a new api group, STOP! Copy the
// extensions group instead. This Scheme is special and should appear ONLY in
// the api group, unless you really know what you're doing.
// TODO(lavalamp): make the above error impossible.
/*
	首字母大写，包级变量

	Scheme，是runtime.Scheme的默认实例化对象，Scheme管理的就是GVK和Type的关系。
	API资源的初始化在引入包的时候就已经完成了，即它们在main函数执行前已经完成。
	NewScheme() 定义在/pkg/runtime/scheme.go
	==>func NewScheme() *Scheme
*/
var Scheme = runtime.NewScheme()
```

## Scheme的定义
首先查看对Scheme的介绍，
```go
// Scheme defines methods for serializing and deserializing API objects, a type
// registry for converting group, version, and kind information to and from Go
// schemas, and mappings between Go schemas of different versions. A scheme is the
// foundation for a versioned API and versioned configuration over time.
/*
	Scheme定义了序列化、反序列化、版本转换的方法。
	Scheme是多版本API的基础。
*/
//
// In a Scheme, a Type is a particular Go struct, a Version is a point-in-time
// identifier for a particular representation of that Type (typically backwards
// compatible), a Kind is the unique name for that Type within the Version, and a
// Group identifies a set of Versions, Kinds, and Types that evolve over time. An
// Unversioned Type is one that is not yet formally bound to a type and is promised
// to be backwards compatible (effectively a "v1" of a Type that does not expect
// to break in the future).
/*
	在Scheme的定义里面，一个Type，就是一个特定的Go Struct，
					 一个Version，是该Type的特定表示的时间点标识符（通常向后兼容），
					 一个Kind，是一个Type在该Version中的唯一name。
					 一个Group，标识了一组Versions, Kinds, and Types。
					 一个Unversioned Type，是一种还没正式绑定到一个Type的Type，会被往后兼容。
					 (实际上，a "v1" of a Type在将来是不会被破坏的)
*/
// Schemes are not expected to change at runtime and are only threadsafe after registration is complete.
```

### type Scheme struct
```go
/*
	type Scheme struct 简介：
	用于API资源之间的序列化、反序列化、版本转换。
	Scheme里面还有好几个map，
	前面的结构体存储的都是unversioned.GroupVersionKind、unversioned.GroupVersion这些东西，
	这些东西本质上只是表示资源的字符串标识，
	Scheme存储了对应着标志的具体的API资源的结构体，即relect.Type=>定义在/pkg/api/types.go中如Pod、Service这些Types

	如果说RESTMapper管理的是GVR和GVK的关系，
	那么Scheme管理的就是GVK和Type的关系。
	系统中所有的Type都要注册到Scheme中，当然目前系统只有一个Scheme，即api.Scheme,定义在/pkg/api/register.go
	Scheme除了管理GVK和Type的关系，还管理有默认设置函数，并聚合了converter及cloner。

	gvkToType: 存储gvk和Type的关系，一个gvk只能对应一个Type；
	typeToGVK：存储Type和gvk的关系，一个type可能对应多个GVK；
	unversionedTypes：记录unversioned的Type和GVK的关系，unversioned无需版本转换；
	unversionedKinds：记录unversioned的GVK和Type的关系；
	fieldLabelConversionFuncs：管理field selector的转换，如旧版本v1的spec.host需要转换成spec.nodeName(详见在/pkg/api/v1/conversion.go中的addConversionFuncs()函数)；
	defaulterFuncs：存储Type及其对应的默认值设置函数；
	converter：用来转换不同版本的结构体值；
	cloner：用来获取结构体值的拷贝。

	Kubernetes内部组件的流通的结构体值使用的是内部版本，所有的外部版本都要向内部版本进行转换；
	内部版本必须转换成外部版本才能进行输出。外部版本之间不能直接转换。
	etcd中存储的是带有版本的数据。
	从Scheme的定义可以看出，Scheme是个converter，也是个cloner，
*/
type Scheme struct {
	// versionMap allows one to figure out the go type of an object with
	// the given version and name.
	gvkToType map[unversioned.GroupVersionKind]reflect.Type

	// typeToGroupVersion allows one to find metadata for a given go object.
	// The reflect.Type we index by should *not* be a pointer.
	/*
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
	*/
	unversionedKinds map[string]reflect.Type

	// Map from version and resource to the corresponding func to convert
	// resource field labels in that version to internal version.
	/*
		译：从version and resource映射到相应的func，以将该版本中的resource字段标签转换为内部版本。

		field selector转换函数
	*/
	fieldLabelConversionFuncs map[string]map[string]FieldLabelConversionFunc

	// defaulterFuncs is an array of interfaces to be called with an object to provide defaulting
	// the provided object must be a pointer.
	/*
		译：defaulterFuncs是一个数组接口，可以以一个对象的形式调用，被用来提供默认值。提供的对象必须是一个指针。

		默认值设置函数
	*/
	defaulterFuncs map[reflect.Type]func(interface{})

	// converter stores all registered conversion functions. It also has
	// default coverting behavior.
	/*
		译：converter存储所有注册转换函数。 它还具有默认转换功能。

		聚合converter结构体
	*/
	converter *conversion.Converter

	// cloner stores all registered copy functions. It also has default
	// deep copy behavior.
	/*
		译：cloner存储所有的copy函数。它还具有默认的深度拷贝功能

		聚合cloner结构体
	*/
	cloner *conversion.Cloner
}
```

## NewScheme()函数
```go
// NewScheme creates a new Scheme. This scheme is pluggable by default.
/*
	译：NewScheme创建了一个新的Scheme。这个Scheme默认是可插拔的
*/
func NewScheme() *Scheme {
	/*
		定义了一个空的Scheme
	*/
	s := &Scheme{
		gvkToType:        map[unversioned.GroupVersionKind]reflect.Type{},
		typeToGVK:        map[reflect.Type][]unversioned.GroupVersionKind{},
		unversionedTypes: map[reflect.Type]unversioned.GroupVersionKind{},
		unversionedKinds: map[string]reflect.Type{},
		//初始化一个cloner
		cloner:           conversion.NewCloner(),
		fieldLabelConversionFuncs: map[string]map[string]FieldLabelConversionFunc{},
		defaulterFuncs:            map[reflect.Type]func(interface{}){},
	}
	//创建converter，用于不同版本对象转换
	s.converter = conversion.NewConverter(s.nameFunc)
	// 增加一些转换函数
	s.AddConversionFuncs(DefaultEmbeddedConversions()...)

	// Enable map[string][]string conversions by default
	if err := s.AddConversionFuncs(DefaultStringConversions...); err != nil {
		panic(err)
	}
	if err := s.RegisterInputDefaults(&map[string][]string{}, JSONKeyMapper, conversion.AllowDifferentFieldTypeNames|conversion.IgnoreMissingFields); err != nil {
		panic(err)
	}
	if err := s.RegisterInputDefaults(&url.Values{}, JSONKeyMapper, conversion.AllowDifferentFieldTypeNames|conversion.IgnoreMissingFields); err != nil {
		panic(err)
	}
	/*
		上面就创建了一个空的、新的Scheme，return
	*/
	return s
}
```
