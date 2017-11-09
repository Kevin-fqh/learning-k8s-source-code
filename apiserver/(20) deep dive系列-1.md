# Kubernetes deep dive: API Server – part 1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引言](#引言)
  - [API Server](#api-server)
  - [术语](#术语)
    - [Kind](#kind)
	- [API Group](#api-group)
	- [Version](#version)
	- [Request Flow and Processing](#request-flow-and-processing)

<!-- END MUNGE: GENERATED_TOC -->

## 引言
如果你对Kubernetes的内部机制感兴趣，以及如何调试k8s，那么这`deep dive`系列就是为你而设。
此外，如果希望扩展Kubernetes或开始贡献项目，你可能会从中受益。

在本文中，我们从介绍Kubernetes API Server开始，然后介绍一些术语并解释API请求流程。
未来将介绍API Server的存储相关主题和扩展点。

## API Server
在概念层面上，Kubernetes由一堆不同角色的节点组成。 主节点上的控制平面由API服务器，控制器管理器和调度程序组成。 API服务器是中央管理实体，是与分布式存储组件等直接对话的唯一组件。 它提供以下核心功能：

Kubernetes由一堆不同角色的node组成。master节点上的控制平面由 API Server, the Controller Manager and Scheduler(s)组成。 API Server是中心管理实体，是与分布式存储组件etcd直接对话的唯一组件。它提供以下核心功能：
- 提供[Kubernetes API](https://kubernetes.io/docs/concepts/overview/kubernetes-api/)，由工作节点以及由kubectl使用
- 代理群集组件，如Kubernetes UI
- 允许操纵对象的状态，例如pod和services
- 保持分布式存储（etcd）中对象的状态
![api server](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-overview.png)

Kubernetes API是一个具有JSON作为主序列化schema的HTTP API，但它也支持Protocol Buffers，主要用于集群内部通信。
为了可扩展性原因，Kubernetes支持‘不同API路径’的多个API版本，例如`/api/v1` 或者 `/apis/extensions/v1beta1`。
不同的API版本意味着不同程度的稳定性和支持：
- Alpha级别，例如v1alpha1默认情况下禁用，其支持的功能可能会被随时丢弃，恕不另行通知，并且只能在短期测试群集中使用。
- Beta级别，例如v2beta3，默认情况下启用，这意味着代码经过良好测试，但是后续测试版或稳定版本中对象的语义可能会以不兼容的方式更改。
- Stable级别，例如，v1将出现在许多后续版本的发行软件中。

现在来看看如何构建HTTP API空间。
在顶层设计中，我们把API分为下面三种：
- core group，everything below `/api/v1`, for historic reasons under this path and not under `/apis/core/v1`
- named groups，at path `/apis/$NAME/$VERSION`
- system-wide entities，如`/metrics`
![API-server-space](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-space.png)

展望未来，我们将专注于批处理操作。在Kubernetes 1.5中，存在两个版本的批处理操作：`/apis/batch/v1` 和 `/apis/batch/v2alpha1`，用于暴露不同实体的集合，可以被查询和操作。

现在展示与API进行交互的例子：
```yaml
$ curl http://127.0.0.1:8080/apis/batch/v1
{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "batch/v1",
  "resources": [
    {
      "name": "jobs",
      "namespaced": true,
      "kind": "Job"
    },
    {
      "name": "jobs/status",
      "namespaced": true,
      "kind": "Job"
    }
  ]
}
```
在将来，使用alpha VERSION
```yaml
$ curl http://127.0.0.1:8080/apis/batch/v2alpha1
{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "batch/v2alpha1",
  "resources": [
    {
      "name": "cronjobs",
      "namespaced": true,
      "kind": "CronJob"
    },
    {
      "name": "cronjobs/status",
      "namespaced": true,
      "kind": "CronJob"
    },
    {
      "name": "jobs",
      "namespaced": true,
      "kind": "Job"
    },
    {
      "name": "jobs/status",
      "namespaced": true,
      "kind": "Job"
    },
    {
      "name": "scheduledjobs",
      "namespaced": true,
      "kind": "ScheduledJob"
    },
    {
      "name": "scheduledjobs/status",
      "namespaced": true,
      "kind": "ScheduledJob"
    }
  ]
}
```
通常，Kubernetes API通过标准HTTP动词POST，PUT，DELETE和GET with JSON 作为默认有效载荷，在指定路径上执行创建，更新，删除和检索操作。

大多数API对象区分对象的`期望状态`和`当前状态`。 specification是对`期望状态`的完整描述。

## 术语
在对API Server 、HTTP API space 和其属性进行简要概述之后，我们现在正式地定义在这个上下文中使用的术语，像pods, services, endpoints, deployment...这些构成了kubernetes的类型。 我们使用以下术语：

### Kind
Kind 是实体的类型。 每个对象都有一个字段 kind，它告诉一个客户端(例如kubectl)，它代表一个pod：
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: webserver
spec:
  containers:
  - name: nginx
    image: nginx:1.9
    ports:
    - containerPort: 80
```

`Kinds`有三种：
- Objects 表示系统中的一个持久实体。一个object可能有多个resource，客户端可以使用它们来执行特定的操作。如Pod和Namespace。
- Lists 是一个或多个实体资源的集合。Lists具有有限的公共元数据。如：PodLists和NodeLists。
- Special purpose kinds 用于Objects和非持久化实体的特定动作。如/binding或/status，discovery uses APIGroup and APIResource, error results use Status。

### API Group 
API Group 是一组逻辑相关的`Kinds`的集合。 例如，所有批处理对象（如Job或ScheduledJob）都在`batch` API Group中。

### Version
每个API Group可以存在多个version。 例如，一个group首先显示为v1alpha1，然后升级到v1beta1，最后GA于v1。
在一个version中（如v1beta1）创建的object，可以在其它受支持的version中（如v1）进行接收。
API server进行无损转换以返回请求的版本中的对象。

### Resource
Resource 代表了系统实体，通过http以json方式发送和接收。
可以作为个人资源（例如`.../namespaces/default`）或资源集合（如`... /jobs`）进行暴露。

An API Group, a Version and a Resource (GVR) uniquely defines a HTTP path:
![API-server-gvr](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-gvr.png)

更准确地说，`jobs`的实际路径是`/apis/batch/v1/namespaces/$NAMESPACE/jobs`。
因为`jobs`不是一个集群范围的资源，与`node`资源相反。
为了简洁起见，我们在整篇文章中的路径中省略了`$NAMESPACE`部分。

请注意，`kinds`可能不仅存在于不同的version中，也可能存在于不同的`API Groups`中。
例如，`Deployment`在`extensions group`中作为alpha kind开始，最终在自己的group `apps.k8s.io`中升级为GA版本。
因此，要唯一识别一个`Kinds`，你需要`API Group, the version and the kind name (GVK)`。

## Request Flow and Processing
本节主要讨论如何处理一个API请求。API的路径在`k8s.io/pkg/api`，处于来自集群内部和外部client的请求。

那么，当HTTP请求命中Kubernetes API时，会发生了什么呢？
在一个较高的层次上，会发生以下交互：
1. HTTP请求首先由在`DefaultBuildHandlerChain（）`中注册的过滤器链进行处理。 该过滤器对其执行过滤操作（有关过滤器的更多详细信息，请参阅下文）。 过滤器通过并附加相应信息到`ctx.RequestInfo`，例如经过身份验证的user或返回适当的HTTP响应代码。
2. 接下来，多路复用器multiplexer（参见container.go）根据HTTP路径将HTTP请求路由到相应的处理程序。
3. 路由routes（在 `routes/*` 中定义）连接handler处理程序与HTTP路径。
4. 在API Group中注册的handler负责接收HTTP请求和上下文context（如用户，权限等），并从storage中获取请求的object。


`DefaultBuildHandlerChain`请参阅/pkg/genericapiserver/config.go，或[config.go](https://github.com/kubernetes/apiserver/blob/master/pkg/server/config.go)

在API Group中注册handler 请参阅[groupversion.go](https://github.com/kubernetes/apiserver/blob/master/pkg/endpoints/groupversion.go)和[installer.go](https://github.com/kubernetes/apiserver/blob/master/pkg/endpoints/installer.go)

完整的流程如下图所示：
![API-server-flow](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-flow.png)

再次注意，为了简洁起见，我们省略了上面图中HTTP路径的`$NAMESPACE`部分。

现在我们来看一下[config.go](https://github.com/kubernetes/apiserver/blob/master/pkg/server/config.go)中的DefaultBuildHandlerChain()设置的过滤器：
- WithRequestInfo()，将RequestInfo附加到上下文中
- WithMaxInFlightLimit()，限制了正在运行中的请求数
- WithTimeoutForNonLongRunningRequests()，设置一个non-long-running requests超时。 大多数GET，PUT，POST，DELETE请求是non-long-running requests。 而watches and proxy requests则是long-running requests。
- WithPanicRecovery()，包装一个handcler来纪录和recover panic。
- WithCORS()，提供了CORS实现。CORS代表`跨源`资源共享，该机制允许 嵌入到HTML页面中的JavaScript 将 XMLHttpRequests设置为   不同于JavaScript起源的域。
- WithAuthentication()，尝试以用户身份验证给定的请求，并将用户信息存储在提供的上下文中。成功后，`Authorization HTTP header `将从request中删除。
- WithAudit()，在handler中封装一个日志审计装饰器，对所有的incoming request起作用。审核日志条目包含请求的源IP，用户调用操作和请求的命名空间等信息。
- WithImpersonation()，处理user impersonation。通过检查一个request: 尝试更改用户（类似于sudo）。
- WithAuthorization()，将所有经过授权的request传递多路复用器multiplexer。multiplexer会分发到正确的handler处，否则返回forbidden error。

在下一篇文章中，将介绍资源的序列化是如何发生，以及对象如何在分布式存储中持久化。

## 参考
译自[Kubernetes deep dive](https://blog.openshift.com/kubernetes-deep-dive-api-server-part-1/)







