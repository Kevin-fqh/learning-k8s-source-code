# 多版本资源注册-RESTMapper

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [RESTMapper函数](#restmapper函数)
  - [import_known_versions](#import_known_versions)
  - [Core Group分析](#core-group分析)
    - [一个Group和Version的声明](#一个group和version的声明)
	- [初始化函数init()](#初始化函数init())
	- [enableVersions-groupMeta的初始化](#enableversions-groupmeta的初始化)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

初始化apiserver core v1的过程中，生成groupMeta的时候用到`RESTMapper: newRESTMapper(externalVersions),`。我们从这里开始研究RESTMapper。

## newRESTMapper函数
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

/*
	所有的api资源可以分为两类：一类是有namespace，另一类是没有namespace。
	比如下面func newRESTMapper方法中的Node、Namespace、PersistentVolume、ComponentStatus不属于任何namespace。
	ignoredKinds是下面接口需要用到的参数，表示遍历Scheme时忽略这些kinds。
*/
func newRESTMapper(externalVersions []unversioned.GroupVersion) meta.RESTMapper {
	fmt.Println("调用func newRESTMapper")
	// the list of kinds that are scoped at the root of the api hierarchy
	// if a kind is not enumerated here, it is assumed to have a namespace scope
	/*
		译：在api层次结构根目录下的种类列表，如果没有在这里列出，则假定它具有命名空间范围

		这些枚举列出的是API最顶层的对象，可以理解为没有namespace的对象
		根据有无namespace，对象分为两类：
								RESTScopeNamespace
								RESTScopeRoot
	*/
	rootScoped := sets.NewString(
		"Node",
		"Namespace",
		"PersistentVolume",
		"ComponentStatus",
	)

	// these kinds should be excluded from the list of resources
	/*
		需要忽略Scheme中如下的kinds
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