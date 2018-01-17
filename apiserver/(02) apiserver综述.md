# Apiserver综述

## 综述
- API Server作为整个Kubernetes集群的核心组件，让所有资源可被描述和配置；这里的资源包括了类似网络、存储、Pod这样的基础资源也包括了replication controller、deployment这样的管理对象。
- API Server某种程度上来说更像是包含了一定逻辑的对象数据库；接口上更加丰富、自带GC、支持对象间的复杂逻辑；当然API Server本身是无状态的,数据都是存储在etcd当中。
- API Server提供集群管理的REST API接口，支持增删改查和patch、监听的操作，其他组件通过和API Server的接口获取资源配置和状态，以实现各种资源处理逻辑。
- 只有API Server才直接操作etcd

## 架构图
![APiserver架构图](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/apiserver-00.jpeg)

- Scheme：定义了资源序列化和反序列化的方法以及资源类型和版本的对应关系；这里我们可以理解成一张映射表。
- Storage：是对资源的完整封装，实现了资源创建、删除、watch等所有操作。
- APIGroupInfo：是在同一个Group下的所有资源的集合。

## 资源版本
一个资源对应着两个版本: 一个版本是用户访问的接口对象（yaml或者json通过接口传递的格式），称之为external version;
另一个版本则是核心对象，实现了资源创建和删除等，并且直接参与持久化，对应了在etcd中存储，称之为internal version。
这两个版本的资源是需要相互转化的，并且转换方法需要事先注册在Scheme中。

版本化的API旨在保持稳定，而internal version是为了最好地反映Kubernetes代码本身的需要。

这也就是说如果要自己新定义一个资源，需要声明两个版本！
同时应该还要在Scheme中注册版本转换函数。

etcd中存储的是带版本的，这也是Apiserver实现多版本转换的核心。
多个external version版本之间的资源进行相互转换，都是需要通过internal version进行中转。

对于core Group而言，internal version的对象定义在`/kubernetes-1.5.2/pkg/api/types.go`。  
v1是其中一个external version，其对象定义在`/kubernetes-1.5.2/pkg/api/v1/types.go`。  
一个对象在internal version和external version中的定义可以一样，也可以不一样。

## Apiserver启动时初始化流程
1. initial.go中的初始化主要用internal version和external versions填充了Scheme，完成了 APIRegistrationManager中GroupMeta的初始化。GroupMeta的主要用于后面的初始化APIGroupVersion。

2. GroupMeta包含成员其中就有Groupversion、RESTMapper。初始化groupMeta的时候会根据Scheme和externalVersions新建一个RESTMapper。

3. /pkg/registry/core/rest/storage_core.go中的NewLegacyRESTStorage基于上面的Scheme和GroupMeta生成了一个APIGroupInfo。初始化时候的GroupMeta是通过type APIRegistrationManager struct的函数来获取的。

4. 然后基于APIGroupInfo生成一个APIGroupVersion。

5. 最后`apiGroupVersion.InstallREST(s.HandlerContainer.Container)`，完成从API资源到restful API的注册。

6. 在InstallREST的过程中会用到RESTMapper生成的RESTMapping

```go
重要结构体:
一：
	APIGroupVersion===>定义在pkg/apiserver/apiserver.go==>type APIGroupVersion struct
	创建APIGroupVersion的地方在/pkg/genericapiserver/genericapiserver.go中的
		--->func (s *GenericAPIServer) newAPIGroupVersion
	*************************
	*************************
	-->可以从/pkg/master/master.go中的-->func (c completedConfig) New() (*Master, error)中的
	-->m.InstallLegacyAPI(c.Config, restOptionsFactory.NewFor, legacyRESTStorageProvider) 和
	   m.InstallAPIs(c.Config.GenericConfig.APIResourceConfigSource, restOptionsFactory.NewFor, restStorageProviders...)
	   开始分析

二：
	APIRegistrationManager===>/pkg/apimachinery/registered/registered.go==>type APIRegistrationManager struct
	创建APIRegistrationManager的地方在/pkg/apimachinery/registered/registered.go中

总结：
	综合上面所有的初始化可以看到（APIGroupVersion、APIGroupInfo、Scheme、GroupMeta、RESTMapper、APIRegistrationManager），
	其实主要用internal version和external versions填充Scheme，
	用external versions去填充GroupMeta以及其成员RESTMapper。
	GroupMeta有啥作用呢？主要用于生成最后的APIGroupVersion。
```

## API资源注册为Restful API
当API资源初始化完成以后，需要将这些API资源注册为restful api，用来接收用户的请求。
kube-apiServer使用了go-restful这套框架，里面主要包括三种对象：
- Container: 一个Container包含多个WebService
- WebService: 一个WebService包含多条route
- Route: 一条route包含一个method(GET、POST、DELETE等)，一条具体的path(URL)以及一个响应的handler function。

API注册的入口函数有两个： m.InstallAPIs 和 m.InstallLegacyAPI。
文件路径：pkg/master/master.go
这两个函数分别用于注册"/api"和"/apis"的API,这里先拿InstallLegacyAPI进行介绍。
这些接口都是在config.Complete().New()函数中被调用

## Storage机制
Apiserver针对每一类资源(pod、service、replication controller),都会与etcd建立一个连接,获取该资源的opt。
所有资源都定义了restful实现。
Apiserver正是通过这个opt去操作对应的资源。

## Apiserver端List-Watch机制
什么是watch?kubelet、kube-controller-manager、kube-scheduler需要监控各种资源(pod、service等)的变化，
当这些对象发生变化时(add、delete、update)，kube-apiserver能够主动通知这些组件。这是Client端的Watch实现。

Apiserver端的Watch机制是建立在etcd的Watch基础上的。
etcd的watch是没有过滤功能的，而kube-apiserver增加了过滤功能。

什么是过滤功能？，比如说kubelet只对调度到本节点上的pod感兴趣，也就是pod.host=node1；
而kube-scheduler只对未被调度的pod感兴趣，也就是pod.host=”“。
etcd只能watch到pod的add、delete、update。
kube-apiserver则增加了过滤功能，将订阅方感兴趣的部分资源发给订阅方。

## 一个Restful请求需要经过的流程

Authentication-->Authorization-->Admission Control

![一个请求需要经过的流程](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/access-control-overview.jpg)

## 参考
[如何扩展Kubernetes管理的资源对象](http://dockone.io/article/2405)
