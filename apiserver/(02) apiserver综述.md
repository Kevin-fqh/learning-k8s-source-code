# Apiserver综述

## 综述
- API Server作为整个Kubernetes集群的核心组件，让所有资源可被描述和配置；这里的资源包括了类似网络、存储、Pod这样的基础资源也包括了replication controller、deployment这样的管理对象。
- API Server某种程度上来说更像是包含了一定逻辑的对象数据库；接口上更加丰富、自带GC、支持对象间的复杂逻辑；当然API Server本身是无状态的,数据都是存储在etcd当中。
- API Server提供基于RESTful的管理接口，支持增删改查和patch、监听的操作，其他组件通过和API Server的接口获取资源配置和状态，以实现各种资源处理逻辑。

## 架构图
![APiserver架构图](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/apiserver-00.jpeg)

- Scheme：定义了资源序列化和反序列化的方法以及资源类型和版本的对应关系；这里我们可以理解成一张映射表。
- Storage：是对资源的完整封装，实现了资源创建、删除、watch等所有操作。
- APIGroupInfo：是在同一个Group下的所有资源的集合。

## 资源版本
一个资源对应着两个版本: 一个版本是用户访问的接口对象（yaml或者json通过接口传递的格式），称之为internal version;
另一个版本则是核心对象，实现了资源创建和删除等，并且直接参与持久化，对应了在etcd中存储，称之为external version。
这两个版本的资源是需要相互转化的，并且转换方法需要事先注册在Scheme中。

etcd中存储的是带版本的，这也是Apiserver实现多版本转换的核心。
多个external version版本之间的资源进行相互转换，都是需要通过internal version进行中转。

internal version的对象定义在`/kubernetes-1.5.2/pkg/api/types.go`。  
core v1是其中一个external version，其对象定义在`/kubernetes-1.5.2/pkg/api/v1/types.go`。  
一个对象在internal version和external version中的定义可以一样，也可以不一样。

资源的核心对象是该类型资源的主体，参与操作资源的相关方法和持久化。
这句话怎么理解呢？
也就是说，如果一个Struct A如果在internal version中仅定义属性a；同时在一个external version中定义了属性a和b。
那么，当Struct A的对象持久化存储到etcd中时，仅有a属性会被存储下来，属性b并不会被保存下来。

## Apiserver启动时初始化流程
1. initial.go中的初始化主要用internal version和external versions填充了Scheme，完成了 APIRegistrationManager中GroupMeta的初始化。GroupMeta的主要用于后面的初始化APIGroupVersion。

2. GroupMeta包含成员其中就有Groupversion、RESTMapper。初始化groupMeta的时候会根据Scheme和externalVersions新建一个RESTMapper。

3. /pkg/registry/core/rest/storage_core.go中的NewLegacyRESTStorage基于上面的Scheme和GroupMeta生成了一个APIGroupInfo。初始化时候的GroupMeta是通过type APIRegistrationManager struct的函数来获取的。

4. 然后基于APIGroupInfo生成一个APIGroupVersion。

5. 最后`apiGroupVersion.InstallREST(s.HandlerContainer.Container)`，完成从API资源到restful API的注册。

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
	GroupMeta有啥作用呢？主要用于初始化APIGroupVersion。
```


## 参考
[如何扩展Kubernetes管理的资源对象](http://dockone.io/article/2405)
