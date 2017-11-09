# Apiserver之多版本资源注册-Scheme-1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [全局唯一的Scheme对象](#全局唯一的scheme对象)
  - [Scheme的定义](#scheme的定义)
    - [type Scheme struct](#type-scheme-struct)
  - [NewScheme 函数](#newscheme-函数)
  - [Sheme实现的函数接口](#sheme实现的函数接口)
    - [addKnownTypes](#addknowntypes)
    - [AddUnversionedTypes](#addunversionedtypes)
    - [nameFunc](#namefunc)
	- [AddKnownTypeWithName](#addknowntypewithname)
	- [KnownTypes、AllKnownTypes](#knowntypes-allknowntypes)
	- [ObjectKinds、ObjectKind](#objectkinds-objectkind)
	- [Recognizes](#recognizes)
	- [IsUnversioned](#isunversioned)
	- [converter相关注册函数](#converter相关注册函数)
	- [cloner相关注册函数](#cloner相关注册函数)
	- [Default](#default)
	- [Copy，DeepCopy](#copy-deepcopy)
	- [Convert，ConvertFieldLabel，ConvertToVersion](#convert-convertfieldlabel-converttoversion)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文主要对Scheme的定义和提供的功能函数进行介绍。

在初始化apiserver core v1的过程中，`addVersionsToScheme(externalVersions...)`，将所有的GroupVersions添加到Scheme。那么Scheme的定义和作用是什么呢？本文主要介绍：
- 是如何对Scheme进行初始化的？
- Scheme的定义和功能函数

## 全局唯一的Scheme对象
Apiserver全局范围内，只有一个Scheme，即api.Scheme。
所有的GroupVersion受这个api.Scheme管理。所有的GroupVersion的Type都是往这个全局唯一的api.Scheme里面注册。
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
首先查看对Scheme的介绍，这里是不是可以把Type和Kind视为等同的？？？
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

## NewScheme 函数
初始化一个新的Scheme
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

## Sheme实现的函数接口
### addKnownTypes
AddKnownTypes()为Scheme的type注册函数，参数为GV及types，其中types和GV先组成GVK，然后向gvkToType和typeToGVK填充Type和GVK的关系。

利用package reflect反射，组装一个gvk。
```go
// AddKnownTypes registers all types passed in 'types' as being members of version 'version'.
// All objects passed to types should be pointers to structs. The name that go reports for
// the struct becomes the "kind" field when encoding. Version may not be empty - use the
// APIVersionInternal constant if you have a type that does not have a formal version.
/*
	译：AddKnownTypes将入参“types”中传递的所有类型注册为版本“version”的成员。
		传递给“types”的所有对象都应该是指向结构体的指针。
		编码时，该结构的名称为“kind”字段。
		版本可能不为空 - 如果您使用一个不具有正式版本的“type”，请使用APIVersionInternal常量。
*/
func (s *Scheme) AddKnownTypes(gv unversioned.GroupVersion, types ...Object) {
	/*
		func (s *Scheme) AddKnownTypes 主要操作了s.gvkToType和s.typeToGVK，用于转换的目的。
	*/
	if len(gv.Version) == 0 {
		panic(fmt.Sprintf("version is required on all types: %s %v", gv, types[0]))
	}
	for _, obj := range types {
		t := reflect.TypeOf(obj)
		if t.Kind() != reflect.Ptr {
			panic("All types must be pointers to structs.")
		}
		//Elem()能对指针进行解引用
		t = t.Elem()
		if t.Kind() != reflect.Struct {
			panic("All types must be pointers to structs.")
		}

		/*
			gv:group+version
			gvk:gv+kind,kind就是“types”
			==>定义在/pkg/api/unversioned/group_version.go
			
			利用package reflect反射，组装一个gvk
		*/
		gvk := gv.WithKind(t.Name())
		//一个GVK只能对应一个type
		s.gvkToType[gvk] = t
		/*
			t, gvk:  v1.Event /v1, Kind=Event
			同一个type，可能对应多个gvk
		*/
		s.typeToGVK[t] = append(s.typeToGVK[t], gvk)
	}
}

//WithKind基于调用者的GroupVersion和入参kind，来创建一个GroupVersionKind。
func (gv GroupVersion) WithKind(kind string) GroupVersionKind {
	return GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: kind}
}
```

### AddUnversionedTypes  
AddUnversionedTypes将入参所提供的types注册为“unversioned”，这意味着它们遵循特殊规则。
每当这种types的对象被序列化时，它将使用入参提供的group version进行序列化，并且不被转换。
因此，unversioned objects 预计将永远保持向后兼容，就好像它们处于不会更新的API组和版本。
```go
// AddUnversionedTypes registers the provided types as "unversioned", which means that they follow special rules.
// Whenever an object of this type is serialized, it is serialized with the provided group version and is not
// converted. Thus unversioned objects are expected to remain backwards compatible forever, as if they were in an
// API group and version that would never be updated.

// TODO: there is discussion about removing unversioned and replacing it with objects that are manifest into every version with particular schemas. Resolve this method at that point.

/*
	AddUnversionedTypes()方法可以向Scheme注册unvertioned type，
	Unversioned type不需要进行转换。
*/
func (s *Scheme) AddUnversionedTypes(version unversioned.GroupVersion, types ...Object) {
	s.AddKnownTypes(version, types...)
	for _, obj := range types {
		t := reflect.TypeOf(obj).Elem()
		gvk := version.WithKind(t.Name())
		s.unversionedTypes[t] = gvk
		if _, ok := s.unversionedKinds[gvk.Kind]; ok {
			panic(fmt.Sprintf("%v has already been registered as unversioned kind %q - kind name must be unique", reflect.TypeOf(t), gvk.Kind))
		}
		s.unversionedKinds[gvk.Kind] = t
	}
}
```

### nameFunc
从这里看出一个Type可能对应多个GVK
```go
// nameFunc returns the name of the type that we wish to use to determine when two types attempt
// a conversion. Defaults to the go name of the type if the type is not registered.
/*
	译：当两个types尝试转换的时候，nameFunc会返回我们希望使用的type的名称
	   如果一个type未注册，则默认使用 the go name of the type。
*/
func (s *Scheme) nameFunc(t reflect.Type) string {
	// find the preferred names for this type
	//找出入参Type t对应的GVK
	gvks, ok := s.typeToGVK[t]
	if !ok {
		/*
			入参Type t没有注册过
			使用默认值，the go name of the type
		*/
		return t.Name()
	}

	/*
			从这里看出一个type可能对应多个GVK

		    遍历gvks，更换每一个gvk的Version为"__internal"，组成一个internalGVK；
		    然后用internalGVK到Scheme的gvkToType中查找该internalGVK是否已经被注册过？
				=>如果已经注册过了，找出该internalGVK对应的internalType，然后return s.typeToGVK[internalType][0].Kind
	*/
	for _, gvk := range gvks {
		/*
			定义在/pkg/api/unversioned/group_version.go
				==>func (gvk GroupVersionKind) GroupVersion() GroupVersion

			WithKind基于调用者的GroupVersion和入参kind，来创建一个GroupVersionKind。
		*/
		internalGV := gvk.GroupVersion()
		internalGV.Version = "__internal" // this is hacky and maybe should be passed in
		internalGVK := internalGV.WithKind(gvk.Kind)

		if internalType, exists := s.gvkToType[internalGVK]; exists {
			return s.typeToGVK[internalType][0].Kind
		}
	}
	/*
		遍历结束，都没找到，那就直接return gvks[0].Kind
	*/
	return gvks[0].Kind
}
```

### AddKnownTypeWithName
和`AddKnownTypes`是类似的，但`AddKnownTypeWithName`指定object对应的gvk。
```go
// AddKnownTypeWithName is like AddKnownTypes, but it lets you specify what this type should
// be encoded as. Useful for testing when you don't want to make multiple packages to define
// your structs. Version may not be empty - use the APIVersionInternal constant if you have a
// type that does not have a formal version.
/*
	AddKnownTypeWithName()可以指定object对应的gvk。
	可以指定obj的kind，而不用像AddKnownTypes()去反射获取type的name作为kind
*/
func (s *Scheme) AddKnownTypeWithName(gvk unversioned.GroupVersionKind, obj Object) {
	t := reflect.TypeOf(obj)
	if len(gvk.Version) == 0 {
		panic(fmt.Sprintf("version is required on all types: %s %v", gvk, t))
	}
	if t.Kind() != reflect.Ptr {
		panic("All types must be pointers to structs.")
	}
	t = t.Elem()
	if t.Kind() != reflect.Struct {
		panic("All types must be pointers to structs.")
	}

	s.gvkToType[gvk] = t
	s.typeToGVK[t] = append(s.typeToGVK[t], gvk)
}
```

### KnownTypes、AllKnownTypes
```go
// KnownTypes returns the types known for the given version.
/*
	返回一个指定GV的所有Type
*/
func (s *Scheme) KnownTypes(gv unversioned.GroupVersion) map[string]reflect.Type {
	types := make(map[string]reflect.Type)
	/*
		通过gv在scheme中找到所有合适的types
		具体某个gv下的kind是唯一
	*/
	for gvk, t := range s.gvkToType {
		if gv != gvk.GroupVersion() {
			continue
		}

		types[gvk.Kind] = t
	}
	return types
}
```
```go

// AllKnownTypes returns the all known types.
/*
	返回Scheme中所有的GVK到Type的映射关系
*/
func (s *Scheme) AllKnownTypes() map[unversioned.GroupVersionKind]reflect.Type {
	return s.gvkToType
}
```

### ObjectKinds、ObjectKind
```go
// ObjectKinds returns all possible group,version,kind of the go object, true if the
// object is considered unversioned, or an error if it's not a pointer or is unregistered.
/*
	ObjectKinds()通过obj的type获取对应的GVK(可能存在多个GVK)
	true，如果该obj是unversioned的；
	err，如果obj不是一个指针，或者该obj尚未被注册
*/
func (s *Scheme) ObjectKinds(obj Object) ([]unversioned.GroupVersionKind, bool, error) {
	v, err := conversion.EnforcePtr(obj)
	if err != nil {
		return nil, false, err
	}
	t := v.Type()

	gvks, ok := s.typeToGVK[t]
	if !ok {
		return nil, false, NewNotRegisteredErr(unversioned.GroupVersionKind{}, t)
	}
	_, unversionedType := s.unversionedTypes[t]

	return gvks, unversionedType, nil
}
```
```go
// ObjectKind returns the group,version,kind of the go object and true if this object
// is considered unversioned, or an error if it's not a pointer or is unregistered.
/*
	ObjectKinds()通过obj的type获取对应的GVK(可能存在多个GVK)；
	ObjectKind()则在ObjectKinds()的基础上返回第一个GVk。
*/
func (s *Scheme) ObjectKind(obj Object) (unversioned.GroupVersionKind, bool, error) {
	gvks, unversionedType, err := s.ObjectKinds(obj)
	if err != nil {
		return unversioned.GroupVersionKind{}, false, err
	}
	return gvks[0], unversionedType, nil
}
```

### Recognizes
```go
// Recognizes returns true if the scheme is able to handle the provided group,version,kind
// of an object.
/*
	return true，如果入参gvk在Scheme中已经被注册过了
*/
func (s *Scheme) Recognizes(gvk unversioned.GroupVersionKind) bool {
	_, exists := s.gvkToType[gvk]
	return exists
}
```

### IsUnversioned
```go
//判断obj是否属于unversioned
func (s *Scheme) IsUnversioned(obj Object) (bool, bool) {
	v, err := conversion.EnforcePtr(obj)
	if err != nil {
		return false, false
	}
	t := v.Type()

	if _, ok := s.typeToGVK[t]; !ok {
		return false, false
	}
	_, ok := s.unversionedTypes[t]
	return ok, true
}
```

### New
一个gvk只能对应一个Type
```go
// New returns a new API object of the given version and name, or an error if it hasn't
// been registered. The version and kind fields must be specified.
/*
	利用reflect.New为一个gvk创建一个API object
*/
func (s *Scheme) New(kind unversioned.GroupVersionKind) (Object, error) {
	//versioned的情况
	if t, exists := s.gvkToType[kind]; exists {
		return reflect.New(t).Interface().(Object), nil
	}

	//unversioned的情况
	if t, exists := s.unversionedKinds[kind.Kind]; exists {
		return reflect.New(t).Interface().(Object), nil
	}
	return nil, NewNotRegisteredErr(kind, nil)
}
```

### converter相关注册函数
```go
// AddGenericConversionFunc adds a function that accepts the ConversionFunc call pattern
// (for two conversion types) to the converter. These functions are checked first during
// a normal conversion, but are otherwise not called. Use AddConversionFuncs when registering
// typed conversions.
func (s *Scheme) AddGenericConversionFunc(fn conversion.GenericConversionFunc) {
	s.converter.AddGenericConversionFunc(fn)
}

// Log sets a logger on the scheme. For test purposes only
func (s *Scheme) Log(l conversion.DebugLogger) {
	s.converter.Debug = l
}

// AddIgnoredConversionType identifies a pair of types that should be skipped by
// conversion (because the data inside them is explicitly dropped during
// conversion).
func (s *Scheme) AddIgnoredConversionType(from, to interface{}) error {
	//需要忽略的类型转换
	return s.converter.RegisterIgnoredConversion(from, to)
}

// AddConversionFuncs adds functions to the list of conversion functions. The given
// functions should know how to convert between two of your API objects, or their
// sub-objects. We deduce how to call these functions from the types of their two
// parameters; see the comment for Converter.Register.
//
// Note that, if you need to copy sub-objects that didn't change, you can use the
// conversion.Scope object that will be passed to your conversion function.
// Additionally, all conversions started by Scheme will set the SrcVersion and
// DestVersion fields on the Meta object. Example:
//
// s.AddConversionFuncs(
//	func(in *InternalObject, out *ExternalObject, scope conversion.Scope) error {
//		// You can depend on Meta() being non-nil, and this being set to
//		// the source version, e.g., ""
//		s.Meta().SrcVersion
//		// You can depend on this being set to the destination version,
//		// e.g., "v1".
//		s.Meta().DestVersion
//		// Call scope.Convert to copy sub-fields.
//		s.Convert(&in.SubFieldThatMoved, &out.NewLocation.NewName, 0)
//		return nil
//	},
// )
//
// (For more detail about conversion functions, see Converter.Register's comment.)
//
// Also note that the default behavior, if you don't add a conversion function, is to
// sanely copy fields that have the same names and same type names. It's OK if the
// destination type has extra fields, but it must not remove any. So you only need to
// add conversion functions for things with changed/removed fields.
/*
	向converter注册conversion函数
*/
func (s *Scheme) AddConversionFuncs(conversionFuncs ...interface{}) error {
	for _, f := range conversionFuncs {
		if err := s.converter.RegisterConversionFunc(f); err != nil {
			return err
		}
	}
	return nil
}

// Similar to AddConversionFuncs, but registers conversion functions that were
// automatically generated.
/*
	向converter注册GeneratedConversionFuncs，
	其中GeneratedConversionFuncs为自动生成的转换函数
*/
func (s *Scheme) AddGeneratedConversionFuncs(conversionFuncs ...interface{}) error {
	for _, f := range conversionFuncs {
		if err := s.converter.RegisterGeneratedConversionFunc(f); err != nil {
			return err
		}
	}
	return nil
}

// AddFieldLabelConversionFunc adds a conversion function to convert field selectors
// of the given kind from the given version to internal version representation.
/*
	向Scheme注册field selector转换函数,把指定的external version转换到internal version
*/
func (s *Scheme) AddFieldLabelConversionFunc(version, kind string, conversionFunc FieldLabelConversionFunc) error {
	if s.fieldLabelConversionFuncs[version] == nil {
		s.fieldLabelConversionFuncs[version] = map[string]FieldLabelConversionFunc{}
	}

	s.fieldLabelConversionFuncs[version][kind] = conversionFunc
	return nil
}

// AddStructFieldConversion allows you to specify a mechanical copy for a moved
// or renamed struct field without writing an entire conversion function. See
// the comment in conversion.Converter.SetStructFieldCopy for parameter details.
// Call as many times as needed, even on the same fields.
/*
	向converter注册struct字段转换函数
*/
func (s *Scheme) AddStructFieldConversion(srcFieldType interface{}, srcFieldName string, destFieldType interface{}, destFieldName string) error {
	return s.converter.SetStructFieldCopy(srcFieldType, srcFieldName, destFieldType, destFieldName)
}

// RegisterInputDefaults sets the provided field mapping function and field matching
// as the defaults for the provided input type.  The fn may be nil, in which case no
// mapping will happen by default. Use this method to register a mechanism for handling
// a specific input type in conversion, such as a map[string]string to structs.
/*
	向convertor注册转换发生缺省时默认的处理方式函数
*/
func (s *Scheme) RegisterInputDefaults(in interface{}, fn conversion.FieldMappingFunc, defaultFlags conversion.FieldMatchingFlags) error {
	return s.converter.RegisterInputDefaults(in, fn, defaultFlags)
}

// AddDefaultingFuncs adds functions to the list of default-value functions.
// Each of the given functions is responsible for applying default values
// when converting an instance of a versioned API object into an internal
// API object.  These functions do not need to handle sub-objects. We deduce
// how to call these functions from the types of their two parameters.
//
// s.AddDefaultingFuncs(
//	func(obj *v1.Pod) {
//		if obj.OptionalField == "" {
//			obj.OptionalField = "DefaultValue"
//		}
//	},
// )

/*
	向converter注册DefaultingFunc函数，DefaultingFunc函数可以设置相关结构体值的默认值。
*/
func (s *Scheme) AddDefaultingFuncs(defaultingFuncs ...interface{}) error {
	for _, f := range defaultingFuncs {
		err := s.converter.RegisterDefaultingFunc(f)
		if err != nil {
			return err
		}
	}
	return nil
}
```

### cloner相关注册函数
```go
// AddDeepCopyFuncs adds a function to the list of deep-copy functions.
// For the expected format of deep-copy function, see the comment for
// Copier.RegisterDeepCopyFunction.
/*
	向cloner注册克隆函数
*/
func (s *Scheme) AddDeepCopyFuncs(deepCopyFuncs ...interface{}) error {
	for _, f := range deepCopyFuncs {
		if err := s.cloner.RegisterDeepCopyFunc(f); err != nil {
			return err
		}
	}
	return nil
}

// Similar to AddDeepCopyFuncs, but registers deep-copy functions that were
// automatically generated.
/*
	向cloner注册GeneratedDeepCopyFuncs，其中GeneratedDeepCopyFuncs为自动生成的克隆函数
*/
func (s *Scheme) AddGeneratedDeepCopyFuncs(deepCopyFuncs ...conversion.GeneratedDeepCopyFunc) error {
	for _, fn := range deepCopyFuncs {
		if err := s.cloner.RegisterGeneratedDeepCopyFunc(fn); err != nil {
			return err
		}
	}
	return nil
}
```

### Default
对object src设置默认值
```go
// Default sets defaults on the provided Object.
func (s *Scheme) Default(src Object) {
	//一言概之：各个组件根据参数src这个预设好的key来这寻找已经注册到map变量defaulterFuncs中的默认方法
	/*
		对Scheduler控件而言，
		传进来的src=&v1alpha1.KubeSchedulerConfiguration{}
		defaulterFuncs是一个数组接口

		Default(src Object)做的工作就是从Scheme.defaulterFuncs这个Map中
		获取&v1alpha1.KubeSchedulerConfiguration{}这个type对应的defaultFunc fn，
		并执行fn(&v1alpha1.KubeSchedulerConfiguration{})来完成默认参数的配置。

		那么问题来了。这些type对应的defaultFunc是怎么register到Scheme.defaulterFuncs这个Map中的呢？

		答案就在  pkg/apis/componentconfig/v1alpha1/register.go  中定义的全局变量SchemeBuilder
		SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs) 在创建SchemeBuilder时
		就调用了addDefaultFuncs函数。
		注册defaultFunc的工作应该就是在addDefaultingFuncs方法中实现的。
	*/
	if fn, ok := s.defaulterFuncs[reflect.TypeOf(src)]; ok {
		fn(src)
	}
}
```

### Copy，DeepCopy
```go
// Copy does a deep copy of an API object.
/*
	对cloner.DeepCopy()的封装，可以深度拷贝一个object
*/
func (s *Scheme) Copy(src Object) (Object, error) {
	dst, err := s.DeepCopy(src)
	if err != nil {
		return nil, err
	}
	return dst.(Object), nil
}

// Performs a deep copy of the given object.
func (s *Scheme) DeepCopy(src interface{}) (interface{}, error) {
	return s.cloner.DeepCopy(src)
}
```

### Convert，ConvertFieldLabel，ConvertToVersion
```go
// Convert will attempt to convert in into out. Both must be pointers. For easy
// testing of conversion functions. Returns an error if the conversion isn't
// possible. You can call this with types that haven't been registered (for example,
// a to test conversion of types that are nested within registered types). The
// context interface is passed to the convertor.
// TODO: identify whether context should be hidden, or behind a formal context/scope
//   interface
/*
	转换函数
*/
func (s *Scheme) Convert(in, out interface{}, context interface{}) error {
	flags, meta := s.generateConvertMeta(in)
	meta.Context = context
	if flags == 0 {
		flags = conversion.AllowDifferentFieldTypeNames
	}
	return s.converter.Convert(in, out, flags, meta)
}

// Converts the given field label and value for an kind field selector from
// versioned representation to an unversioned one.
/*
	对field selector进行转换
*/
func (s *Scheme) ConvertFieldLabel(version, kind, label, value string) (string, string, error) {
	if s.fieldLabelConversionFuncs[version] == nil {
		return "", "", fmt.Errorf("No field label conversion function found for version: %s", version)
	}
	conversionFunc, ok := s.fieldLabelConversionFuncs[version][kind]
	if !ok {
		return "", "", fmt.Errorf("No field label conversion function found for version %s and kind %s", version, kind)
	}
	return conversionFunc(label, value)
}
```

- ConvertToVersion
```go
// ConvertToVersion attempts to convert an input object to its matching Kind in another
// version within this scheme. Will return an error if the provided version does not
// contain the inKind (or a mapping by name defined with AddKnownTypeWithName). Will also
// return an error if the conversion does not result in a valid Object being
// returned. Passes target down to the conversion methods as the Context on the scope.
/*
	把入参in Object转换成合适的版本
*/
func (s *Scheme) ConvertToVersion(in Object, target GroupVersioner) (Object, error) {
	return s.convertToVersion(true, in, target)
}


// convertToVersion handles conversion with an optional copy.
func (s *Scheme) convertToVersion(copy bool, in Object, target GroupVersioner) (Object, error) {
	// determine the incoming kinds with as few allocations as possible.
	t := reflect.TypeOf(in)
	if t.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("only pointer types may be converted: %v", t)
	}
	t = t.Elem()
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("only pointers to struct types may be converted: %v", t)
	}
	kinds, ok := s.typeToGVK[t]
	if !ok || len(kinds) == 0 {
		return nil, NewNotRegisteredErr(unversioned.GroupVersionKind{}, t)
	}

	/*
		从多个gvk中选择出符合target的要求的gvk
	*/
	gvk, ok := target.KindForGroupVersionKinds(kinds)
	if !ok {
		// try to see if this type is listed as unversioned (for legacy support)
		// TODO: when we move to server API versions, we should completely remove the unversioned concept
		if unversionedKind, ok := s.unversionedTypes[t]; ok {
			if gvk, ok := target.KindForGroupVersionKinds([]unversioned.GroupVersionKind{unversionedKind}); ok {
				return copyAndSetTargetKind(copy, s, in, gvk)
			}
			return copyAndSetTargetKind(copy, s, in, unversionedKind)
		}

		// TODO: should this be a typed error?
		return nil, fmt.Errorf("%v is not suitable for converting to %q", t, target)
	}

	// target wants to use the existing type, set kind and return (no conversion necessary)
	for _, kind := range kinds {
		if gvk == kind {
			return copyAndSetTargetKind(copy, s, in, gvk)
		}
	}

	// type is unversioned, no conversion necessary
	if unversionedKind, ok := s.unversionedTypes[t]; ok {
		if gvk, ok := target.KindForGroupVersionKinds([]unversioned.GroupVersionKind{unversionedKind}); ok {
			return copyAndSetTargetKind(copy, s, in, gvk)
		}
		return copyAndSetTargetKind(copy, s, in, unversionedKind)
	}

	out, err := s.New(gvk)
	if err != nil {
		return nil, err
	}

	if copy {
		copied, err := s.Copy(in)
		if err != nil {
			return nil, err
		}
		in = copied
	}

	flags, meta := s.generateConvertMeta(in)
	meta.Context = target
	if err := s.converter.Convert(in, out, flags, meta); err != nil {
		return nil, err
	}

	setTargetKind(out, gvk)
	return out, nil
}
```

## 总结
本文主要介绍了Apiserver是如何对Scheme进行初始化的，同时介绍了Scheme的定义和功能函数。
后面会结合scheduler等组件，来看看各个组件是如何使用这个Scheme的？？