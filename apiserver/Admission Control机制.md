# Admission Control机制

## Admission Control的作用和类型
kubernetes 有3个层次的资源限制方式，分别在Container、Pod、Namespace 层次。
- Container层次主要利用容器本身的支持，比如Docker 对CPU、内存等的支持；
- Pod方面可以限制系统内创建Pod的资源范围，比如最大或者最小的CPU、memory需求；
- Namespace层次就是对用户级别的资源限额了，包括CPU、内存，还可以限定Pod、rc、service的数量。

当一个请求通过了认证和授权机制的认可之后，还需要经过准入控制处理通过之后，apiserver才会处理这个请求。 Admission Control有一个准入控制列表，可以通过命令行设置选择执行哪几个准入控制器。只有所有的准入控制器都检查通过之后，apiserver才执行该请求，否则返回拒绝。

同时Admission Control也会被用来给系统的某些对象设置默认值，以防止用户不提供对应的设置。

推荐的plugin设置顺序如下：
```go
--admission-control=NamespaceLifecycle,LimitRanger,ServiceAccount,PersistentVolumeLabel,DefaultStorageClass,ResourceQuota,DefaultTolerationSeconds
```

目前Admission Control主要有以下插件：
- AlwaysAdmit: 允许所有的请求通过
- AlwaysDeny: 拒绝所有的请求
- ServiceAccount: ServiceAccount会在生成的容器中注入安全相关的信息以访问Kubernetes集群
- SecurityContextDeny: PodSecurityPolicy相关，PodSecurityPolicy可以对容器进行各种限制
- ResourceQuota: 为集群上的当前用户强制执行配额约束，如果配额不足，可能会拒绝请求。 目前支持cpu、memory、pods数量等。（设置整个namespace的总配额）
- LimitRanger: 在一个namespace中对所有资源使用limit限制，目前仅支持cpu和memory。（设置该namespace内所有的pod、container单个的资源使用上限）
- NamespaceLifecycle: 如果context中的namespace不存在，拒绝request。同时也会拒绝在正在删除中的Namespace下创建资源的请求
- DefaultStorageClass，设置PersistentVolumeClaim默认的storage class，防止用户不提供值。

## 