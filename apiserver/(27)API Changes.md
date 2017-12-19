# API Changes
===============

本文翻自[community-V1.6-api_changes](https://github.com/kubernetes/community/blob/release-1.6/contributors/devel/api_changes.md)

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->

- [Overview](#overview)
- [兼容性](#兼容性)
- [Changing versioned APIs](#changing-versioned-apis)
  - [Edit types.go](#edit-typesgo)
  - [Edit defaults.go](#edit-defaultsgo)
  - [Edit conversion.go](#edit-conversiongo)
- [Changing the internal structures](#changing-the-internal-structures)
  - [Edit types.go](#edit-typesgo-1)
- [Edit validation.go](#edit-validationgo)
- [Edit version conversions](#edit-version-conversions)
- [Generate protobuf objects](#generate-protobuf-objects)
- [Edit json (un)marshaling code](#edit-json-unmarshaling-code)
- [Making a new API Group](#making-a-new-api-group)
- [Update the fuzzer](#update-the-fuzzer)
- [Update the semantic comparisons](#update-the-semantic-comparisons)
- [Examples and docs](#examples-and-docs)

<!-- END MUNGE: GENERATED_TOC -->

因为版本更新比较快，注意k8s的版本是否适应本文档的方法。

## Overview

API对象的internal version是独立于任意一个external verion的。 
这为代码升级提供了很大的自由度，但它需要强大的基础设施来进行版本之间的转换。 
处理一个API操作需要很多步骤，简单如一个GET操作都涉及到大量的机制。

转换过程在逻辑上是以internal version为中心的“星”。 
每个版本化的API都可以转换为内部版本（反之亦然），但版本化的API不会直接转换为其他版本化的API。 
这看起来过于繁琐，但实际上，我们并不打算一次保留过多的external verion。

虽然所有的Kubernetes代码都是在internal version上运行， 
但在写入存储器（磁盘或etcd）或通过wire发送之前，它们总是被转换为版本化的形式。 
客户端应该独占地使用版本化的API。

演示一个普遍的流程，下面是一个（假设的）例子：
   1. A user POSTs a `Pod` object to `/api/v7beta1/...`
   2. The JSON is unmarshalled into a `v7beta1.Pod` structure
   3. Default values are applied to the `v7beta1.Pod`
   4. The `v7beta1.Pod` is converted to an `api.Pod` structure
   5. The `api.Pod` is validated, and any errors are returned to the user
   6. The `api.Pod` is converted to a `v6.Pod` (because v6 is the latest stable version)
   7. The `v6.Pod` is marshalled into JSON and written to etcd

那么现在，系统中有一个`Pod`对象在etcd中存储了，user可以使用任意一个系统支持的api version来GET该pod对象。如：
   1. A user GETs the `Pod` from `/api/v5/...`
   2. The JSON is read from etcd and unmarshalled into a `v6.Pod` structure
   3. Default values are applied to the `v6.Pod`
   4. The `v6.Pod` is converted to an `api.Pod` structure
   5. The `api.Pod` is converted to a `v5.Pod` structure
   6. The `v5.Pod` is marshalled into JSON and sent to the user

这么繁琐的处理过程的意义在于API的改变必须非常小心，而且向后兼容。

## 兼容性
Kubernetes认为其API的前向和后向兼容性是非常重要的。 如果符合下面的规范，那么可以认为一个API是兼容的：
   * 新增一个功能，该功能不是正确的行为所必须的。（如不增加新的必填字段）
   * 不会改变现有的语义，包括：
      * default values and behavior
      * interpretation of existing API types, fields, and values
      * which fields are required and which are not

换而言之，
1. 更改之前的任何API调用在更改之后都必须相同。（例如，发布到REST端点的结构）
2. 任何使用了你的change的API不能导致问题，就算Server端不包含你的change。（如程序崩溃或降级的时候）
3. 在多版本API之间进行转换不能导致问题
4. 用户可以像以前那样继续使用旧的方法运作，不需要了解你的变更

在假设的API中（假设我们的版本为v6），Frobber结构如下所示：
```go
// API v6.
type Frobber struct {
  Height int    `json:"height"`
  Param  string `json:"param"`
}
```
如果添加一个新的`Width`字段。 在不更改API版本的情况下添加新字段通常是安全的，因此可以简单地将其更改为：
```go
// Still API v6.
type Frobber struct {
  Height int    `json:"height"`
  Width  int    `json:"width"`
  Param  string `json:"param"`
}
```
有必要给`Width`字段设置一个默认值，以便符合上面的规则#1-在修改前能正常工作的API调用和存储的对象，在修改后也能正常工作。

如果要设置多个`Param`值，不能简单地把`Param string` 改为 `Params []string`，这违反了规则#1 和 #2。 可以使用下面的方法：
```go
// Still API v6, but kind of clumsy.
type Frobber struct {
  Height int           `json:"height"`
  Width  int           `json:"width"`
  Param  string        `json:"param"`  // the first param
  ExtraParams []string `json:"extraParams"` // additional params
}
```

现在，已经满足以下规则：提供旧式Param的API调用仍然可以工作，而不理解ExtraParams的服务器可以忽略它。 从API的角度来说不是很完善，但是它是完全兼容的。

## Changing versioned APIs

### Edit types.go
数据结构定义在`pkg/api/<version>/types.go`，versioned APIs中的所有`types`和`non-inline fields`必须以描述性注释开头，用于生成文档。
types的注释中不能包含该type的name。 
API文档是从这些注释生成的，不应暴露golang类型名称。
`optional字段`应该用`omitempty`来描述其json。
以type ComponentStatus struct为例子，
```go
// +genclient=true
// +nonNamespaced=true

// ComponentStatus (and ComponentStatusList) holds the cluster validation info.
type ComponentStatus struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Conditions []ComponentCondition `json:"conditions,omitempty"`
}
```

### Edit defaults.go
如果新添加的字段需要默认值，那么编辑`pkg/api/<version>/defaults.go`，同时别忘了修改test文件`pkg/api/<version>/defaults_test.go`。 
当你需要区分一个未设置的值和一个自动的零值时，使用指针。 
比如，`PodSpec.TerminationGracePeriodSeconds` 被定义为go的类型`*int64`，零值是`0s`，系统会自动为一个nil的值选择默认值。
```go
// 见/kubernetes-1.5.2/pkg/api/v1/types.go
// PodSpec is a description of a pod
type PodSpec struct {
	...
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty" protobuf:"varint,4,opt,name=terminationGracePeriodSeconds"`
	...
}
```

### Edit conversion.go
如果有对象需要进行转换，编辑`pkg/api/<version>/conversion.go`和`pkg/api/<version>/conversion_test.go`

## Changing the internal structures
需要修改internal structures，才能让上面的versioned APIs生效

### Edit types.go
和versioned APIs的修改类似，文件位置在`pkg/api/types.go`。 
核心思想是internal structs要可以能够表达所有的versioned APIs。

## Edit validation.go
对internal的数据结构进行更改通常需要进行验证，目前验证工作在`pkg/api/validation/validation.go`中进行。 
同时，不要忘了`pkg/api/validation/validation_test.go.`

## Edit version conversions
到这一步之后，versioned API changes and the internal structure changes 已经完成了。 
如果存在显著的差异（特别是field names, types, structural的变化），需要显示把versioned API转化为internal structure，特别是如果`serialization_test`中报出error的情况下。

conversions的性能对apiserver的性能影响很大。 因此，我们是自动生成的转换函数，比通常的（基于反射的，因此效率非常低）更有效率。

转换代码驻留在每个版本化的API中，包括两个文件：
  - `pkg/api/<version>/conversion.go`,包含手工编写的转换函数
  - `pkg/apis/extensions/<version>/zz_generated.conversion.go`，包含自动生成的转换函数

由于自动生成的转换函数使用手工编写的转换函数，因此手动编写的转换函数应该使用定义的约定来命名，即函数转换类型X（pkg a中的类型X转换为pkg中的Y）应该命名为：`convert_a_X_To_b_Y`。

请注意，可以在编写转换函数时使用自动生成的转换函数（出于效率原因）。 
一旦添加了必要的手工转换函数，需要重新生成自动转换函数。 
可以通过运行下述命令来重新生成自动转换函数：
```shell
hack/update-codegen.sh
```

作为构建的一部分，kubernetes还将生成代码来处理versioned api object的deep copy。 
深度拷贝代码驻留在每个版本化的API中：
  - `<path_to_versioned_api>/zz_generated.deepcopy.go`，包含自动生成的copy函数

如果由于编译错误造成不能再次重新生成代码，最简单的解决方法是注释掉导致错误的代码并让脚本重新生成它。 
如果自动生成的转换函数不是手动编写的，那么只需删除整个文件并让生成器从头开始创建即可。

最后，添加手动写入的转换函数，依然还是要求你将test添加到`添加手动写入的转换还要求您将测试添加到pkg / api / <version> /conversion_test.go`。

## Generate protobuf objects
对于任何的core API object，我们还需要生成Protobuf IDL和marshallers。 生成方法是运行：
```shell
hack/update-generated-protobuf.sh
```
绝大多数对象在转换为protobuf时不需要任何考虑。 
但要注意，如果你依赖于标准库中的Golang类型，则可能需要额外的工作，尽管在实践中我们通常使用自己的等价物来进行JSON序列化。
`pkg/api/serialization_test.go`将验证以确保protobuf序列化保留了所有fields - 请确保运行多次以确保所有字段都被完整地计算。

## Edit json (un)marshaling code
json的编码器和解码器也是自动生成的，其代码存在与每一个versiond api之中：
  - `pkg/api/<version>/types.generated.go`
  - `pkg/apis/extensions/<version>/types.generated.go`

可以通过执行下述命令来重新生成
```shell
hack/update-codecgen.sh
```

## Making a new API Group
这是介绍的新建一个Group，其实还有一种是在已有的Group里面新增一个Version。

现在在目录`/pkg/apis`下新建一个目录，然后参考`pkg/apis/authentication`下的目录结构把所有目录都建好。 
在`hack/{verify,update}-generated-{deep-copy,conversions,swagger}.sh`这三个位置的合适位置中把新的group/version给加上，只需要把其驾到一个bash array即可。具体参考[adding an API group](https://github.com/kubernetes/community/blob/release-1.6/contributors/devel/adding-an-APIGroup.md)

目前不支持在`/pkg/apis`目录之外添加API组，但是这是可行的。
The deep copy & conversion generators需要通过解析go文件工作，而不是通过反射; 那么他们将很容易指向任意目录：请参阅问题[13775](https://github.com/kubernetes/kubernetes/issues/13775)。

## Update the fuzzer
API的测试方案的一部分是“模糊”API对象（通过填充随机值），然后将其转换为不同的API版本。 
这有利于暴露那些信息丢失的地方、或是做出了错误假设的地方。
如果为字段添加了默认值，那么该字段将需要具有自定义的模糊函数，以确保该字段被模糊为非空值。

代码在`pkg/api/testing/fuzzer.go`

## Update the semantic comparisons
比较少见的情况

## Examples and docs
至此，所有的API Change工作已经完成了，可以在k8s中使用这新的API来完成定制开发。
最后，可以通过下述命令来更新文档说明
```shell
hack/update-swagger-spec.sh
hack/update-openapi-spec.sh
```

## 参考
译自[community-V1.6-api_changes](https://github.com/kubernetes/community/blob/release-1.6/contributors/devel/api_changes.md)

