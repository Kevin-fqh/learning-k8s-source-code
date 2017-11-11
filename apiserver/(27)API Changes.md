# API Changes

This document aims to guide you through the process, though
not all API changes will need all of these steps.

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
Kubernetes认为其API的前向和后向兼容性是非常重要的。 如果进行了下述操作，那么可以认为一个API是兼容的：
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

现在，你已经满足以下规则：提供旧式Param的API调用仍然可以工作，而不理解ExtraParams的服务器可以忽略它。 这在API方面有些不尽人意，但是它是完全兼容的。






## 参考
译自[api_changes](https://github.com/kubernetes/kubernetes/blob/release-1.1/docs/devel/api_changes.md)

