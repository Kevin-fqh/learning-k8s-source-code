# 多版本资源注册-Scheme-2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [addVersionsToScheme 函数](#addversionstoscheme-函数)
  - [向Scheme注册internal version](#向scheme注册internal-version)
  - [SchemeBuilder](#schemebuilder)
  - [向Scheme注册enabled external versions](#向scheme注册enabled-external-versions)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

从`addVersionsToScheme(externalVersions...)`出发，看看在Apiserver初始化的过程中，
- 各个GroupVersion是怎么往Scheme注册的？
- Scheme的作用是什么？

## addVersionsToScheme 函数
将GroupVersions注册到Scheme中，见/kubernetes-1.5.2/pkg/api/install/install.go。

addVersionsToScheme 函数的主要流程就两个:
1. api.AddToScheme()，向Scheme注册internal version
2. 遍历externalVersions，向Scheme注册enabled external version。Core Group的external version只有v1，所以仅仅执行了v1.AddToScheme(api.Scheme)，把v1版本注册到Scheme中

```go
func addVersionsToScheme(externalVersions ...unversioned.GroupVersion) {
	// add the internal version to Scheme
	/*
		将internal version加入到api.Scheme。
		那么Scheme怎么来的？初始化在哪里？
			==>定义在 pkg/api/register.go
				==>全局变量 var Scheme = runtime.NewScheme()
	*/
	/*
		api.AddToScheme方法定义在pkg/api/register.go的全局变量，是一个函数面值
		==>var (
			SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs)
			AddToScheme   = SchemeBuilder.AddToScheme
			)
		********************************************
		对其进入深入分析可以得出一下结论：
			综上得出，api.AddToScheme(api.Scheme)是将internal version添加到Scheme中。
			为什么会有一个internal version呢？ 其实每一个Group都有一个internal version。
			而apiserver操作的也都是internal version.

		举个例子：假如有一个创建Pod的请求来了，apiserver首先会将请求给反序列化，
		用户发过来的Pod请求往往是有版本的，比如v1，因此会反序列化为一个v1.Pod。
		apiserver会立即将这个v1.Pod利用convertor转换成internal.Pod，然后进行一些操作，
		最后要把它存到etcd里面去，etcd里面的Pod信息是有版本的，因此会先发生一次转换，将其转换为v1.Pod，然后序列化存入etcd。
		这样看上去好像多此一举？
		其实这就是k8s对api多版本的支持，这样用户可以以一个v1beta1创建一个Pod,
		然后存入etcd的是一个相对稳定的版本，比如v1版本。
		
		转换必定存在着效率的问题，为了解决效率问题，转换函数由开发者自己写，然后会重新用代码生成一次，进行优化。
	*/
	if err := api.AddToScheme(api.Scheme); err != nil {
		// Programmer error, detect immediately
		panic(err)
	}
	// add the enabled external versions to Scheme
	/*
		把处于enabled的external versions加入到Scheme中
	*/
	for _, v := range externalVersions {
		if !registered.IsEnabledVersion(v) {
			glog.Errorf("Version %s is not enabled, so it will not be added to the Scheme.", v)
			continue
		}
		switch v {
		case v1.SchemeGroupVersion:
			/*
				继续执行v1.AddToScheme(api.Scheme)函数.
				其实就是把v1版本的api添加到Scheme中，和上面添加internal版本一样
				==>定义在/pkg/api/v1/register.go
					==>var (
						SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs, addConversionFuncs, addFastPathConversionFuncs)
						AddToScheme   = SchemeBuilder.AddToScheme
						)
			*/
			if err := v1.AddToScheme(api.Scheme); err != nil {
				// Programmer error, detect immediately
				panic(err)
			}
		}
	}
}
```

## 向Scheme注册internal version
api.AddToScheme方法定义在pkg/api/register.go的全局变量，是一个函数面值。
其作用是add the internal version to Scheme。
```go
var (
	/*
		通过runtime.NewSchemeBuilder()接口传入两个函数，然后创建了SchemeBuilder
		==>定义在/pkg/runtime/scheme_builder.go
			==>func NewSchemeBuilder(funcs ...func(*Scheme) error) SchemeBuilder
		把addKnownTypes, addDefaultingFuncs两个函数 传进去

		结合其功能来看，最后SchemeBuilder就是一个接口切片，包含了addKnownTypes, addDefaultingFuncs两个接口。
	*/
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs)
	/*
		AddToScheme，一个函数面值
		==>定义在/pkg/runtime/scheme_builder.go
		就是调用此时SchemeBuilder中的函数
		也就是说调用了addKnownTypes, addDefaultingFuncs两个函数
	*/
	AddToScheme = SchemeBuilder.AddToScheme
)
```

先看看`addKnownTypes, addDefaultingFuncs`两个函数：
- addKnownTypes，把k8s内置的version(即SchemeGroupVersion)添加到Scheme。SchemeGroupVersion的GroupName为空，Version是"__internal"。

- addDefaultingFuncs

注意这里的`func addKnownTypes`和`func (s *Scheme) AddKnownTypes`不是同一个函数。

Pod、Service这些Types都是定义在/pkg/api/types.go，这里定义的都是internal verion的数据类型。
```go
func addKnownTypes(scheme *runtime.Scheme) error {
	if err := scheme.AddIgnoredConversionType(&unversioned.TypeMeta{}, &unversioned.TypeMeta{}); err != nil {
		return err
	}
	/*
		把下列Type注册到Scheme中
		该SchemeGroupVersion的GroupName为""，Version是"__internal"
		所以该接口其实是把k8s内置的version添加到Scheme，而且每个group都有该步骤

		scheme.AddKnownTypes接口定义在/pkg/runtime/scheme.go
			==>func (s *Scheme) AddKnownTypes(gv unversioned.GroupVersion, types ...Object)
			是Scheme的type注册函数，参数为GV及types

		Pod、Service这些Types都是定义在/pkg/api/types.go，这里定义的都是internal verion的数据类型
	*/
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Pod{},
		&PodList{},
		&PodStatusResult{},
		&PodTemplate{},
		&PodTemplateList{},
		&ReplicationControllerList{},
		&ReplicationController{},
		&ServiceList{},
		&Service{},
		&ServiceProxyOptions{},
		&NodeList{},
		&Node{},
		&NodeProxyOptions{},
		&Endpoints{},
		&EndpointsList{},
		&Binding{},
		&Event{},
		&EventList{},
		&List{},
		&LimitRange{},
		&LimitRangeList{},
		&ResourceQuota{},
		&ResourceQuotaList{},
		&Namespace{},
		&NamespaceList{},
		&ServiceAccount{},
		&ServiceAccountList{},
		&Secret{},
		&SecretList{},
		&PersistentVolume{},
		&PersistentVolumeList{},
		&PersistentVolumeClaim{},
		&PersistentVolumeClaimList{},
		&DeleteOptions{},
		&ListOptions{},
		&PodAttachOptions{},
		&PodLogOptions{},
		&PodExecOptions{},
		&PodProxyOptions{},
		&ComponentStatus{},
		&ComponentStatusList{},
		&SerializedReference{},
		&RangeAllocation{},
		&ConfigMap{},
		&ConfigMapList{},
	)

	// Register Unversioned types under their own special group
	/*
		var Unversioned = unversioned.GroupVersion{Group: "", Version: "v1"}
		向Scheme注册unvertioned type
	*/
	scheme.AddUnversionedTypes(Unversioned,
		/*
			这些Type定义在/pkg/api/unversioned/types.go
		*/
		&unversioned.ExportOptions{},
		&unversioned.Status{},
		&unversioned.APIVersions{},
		&unversioned.APIGroupList{},
		&unversioned.APIGroup{},
		&unversioned.APIResourceList{},
	)
	return nil
}

// SchemeGroupVersion is group version used to register these objects
/*
	/pkg/runtime/interfaces.go中声明了包级常量
	APIVersionInternal = "__internal"
*/
var SchemeGroupVersion = unversioned.GroupVersion{Group: GroupName, Version: runtime.APIVersionInternal}

const GroupName = ""

func addDefaultingFuncs(scheme *runtime.Scheme) error {
	return scheme.AddDefaultingFuncs(
		func(obj *ListOptions) {
			if obj.LabelSelector == nil {
				obj.LabelSelector = labels.Everything()
			}
			if obj.FieldSelector == nil {
				obj.FieldSelector = fields.Everything()
			}
		},
	)
}
```

## SchemeBuilder
SchemeBuilder收集那些往scheme填充数据(或'注册')的函数接口，简单来说，就是一个`函数切片`。见/pkg/runtime/scheme_builder.go。

SchemeBuilder是各个版本都会用到的，只是NewSchemeBuilder时传递的参数不大一样。
```go
// SchemeBuilder collects functions that add things to a scheme. It's to allow
// code to compile without explicitly referencing generated types. You should
// declare one in each package that will have generated deep copy or conversion
// functions.

type SchemeBuilder []func(*Scheme) error

// NewSchemeBuilder calls Register for you.
func NewSchemeBuilder(funcs ...func(*Scheme) error) SchemeBuilder {
	var sb SchemeBuilder
	sb.Register(funcs...)
	return sb
}

// Register adds a scheme setup function to the list.
func (sb *SchemeBuilder) Register(funcs ...func(*Scheme) error) {
	for _, f := range funcs {
		*sb = append(*sb, f)
	}
}
```
从这里可以看出，`SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs)`，就是把两个函数接口`addKnownTypes, addDefaultingFuncs`收集起来。

再看看`SchemeBuilder.AddToScheme`函数
```go
// AddToScheme applies all the stored functions to the scheme. A non-nil error
// indicates that one function failed and the attempt was abandoned.
/*
	译：AddToScheme将SchemeBuilder中所有的函数应用于scheme。
		非零错误表示一个scheme失败，尝试被放弃。
*/
func (sb *SchemeBuilder) AddToScheme(s *Scheme) error {
	/*
		func (sb *SchemeBuilder) AddToScheme(s *Scheme)，就是调用此时SchemeBuilder中的函数
	*/
	for _, f := range *sb {
		if err := f(s); err != nil {
			return err
		}
	}
	return nil
}
```
到这里，就能理解了。前面的`api.AddToScheme(api.Scheme)`等效于
```go
addKnownTypes(api.Scheme)
addDefaultingFuncs(api.Scheme)
```

## 向Scheme注册enabled external versions
Core Group仅仅有v1一个external version，所以对v1.SchemeGroupVersion而言，见/pkg/api/v1/register.go
```go
// GroupName is the group name use in this package
/*
	const GroupName = ""的含义去查看pkg/api/install/install.go
	代表了Core Group
*/
const GroupName = ""

// SchemeGroupVersion is group version used to register these objects
/*
	SchemeGroupVersion，就定义了一个GroupName为空，Version是'v1'的GroupVersion。
*/
var SchemeGroupVersion = unversioned.GroupVersion{Group: GroupName, Version: "v1"}

var (
	/*
		这里可以看到v1相比较于internal版本，
		还多了好几个函数addConversionFuncs, addFastPathConversionFuncs。
		这些函数在执行AddToScheme()时其实都会要遍历执行，可以深入看下。
		其实就是向Scheme添加了转换函数，
		比如将v1.Pod转换为internal.Pod，将internal.Pod转换为v1.Pod。
		如果同时有v1,v2,v3会如何进行转换？
		其实也还是先统一转换成internal，然后再转换为相应的版本(v1,v2,v3).
		所以internal相当于转换的桥梁，更好的支持了不同版本的api。

		到这里Scheme的初始化基本结束了。
	*/
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, addDefaultingFuncs, addConversionFuncs, addFastPathConversionFuncs)
	AddToScheme   = SchemeBuilder.AddToScheme
)
```
分析基本和前面internal version的一样的，只是多了几个函数接口而已，然后类型定义的文件位置也不一样。

addConversionFuncs, addFastPathConversionFuncs这两个是转换函数。
```go
// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		/*
			这里的Type定义在/pkg/api/v1/types.go，是一个external version
		*/
		&Pod{},
		&PodList{},
		&PodStatusResult{},
		&PodTemplate{},
		&PodTemplateList{},
		&ReplicationController{},
		&ReplicationControllerList{},
		&Service{},
		&ServiceProxyOptions{},
		&ServiceList{},
		&Endpoints{},
		&EndpointsList{},
		&Node{},
		&NodeList{},
		&NodeProxyOptions{},
		&Binding{},
		&Event{},
		&EventList{},
		&List{},
		&LimitRange{},
		&LimitRangeList{},
		&ResourceQuota{},
		&ResourceQuotaList{},
		&Namespace{},
		&NamespaceList{},
		&Secret{},
		&SecretList{},
		&ServiceAccount{},
		&ServiceAccountList{},
		&PersistentVolume{},
		&PersistentVolumeList{},
		&PersistentVolumeClaim{},
		&PersistentVolumeClaimList{},
		&DeleteOptions{},
		&ExportOptions{},
		&ListOptions{},
		&PodAttachOptions{},
		&PodLogOptions{},
		&PodExecOptions{},
		&PodProxyOptions{},
		&ComponentStatus{},
		&ComponentStatusList{},
		&SerializedReference{},
		&RangeAllocation{},
		&ConfigMap{},
		&ConfigMapList{},
	)

	// Add common types
	scheme.AddKnownTypes(SchemeGroupVersion, &unversioned.Status{})

	// Add the watch version that applies
	versionedwatch.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
```
## 总结
本文主要从addVersionsToScheme出发，了解了是如何往api.Scheme里面添加注册各个GroupVersion的。

这里仅仅讲解了Group core，其实其它Group也是大同小异。需要记住的是，所有的GroupVersion都是往这个全局唯一的api.Scheme里面注册。

关于external version和internal version的对象如何进行转换的问题，后面再进行研究。

