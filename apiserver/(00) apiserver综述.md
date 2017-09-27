# Apiserver综述

## 综述
- API Server作为整个Kubernetes集群的核心组件，让所有资源可被描述和配置；这里的资源包括了类似网络、存储、Pod这样的基础资源也包括了replication controller、deployment这样的管理对象；
- API Server某种程度上来说更像是包含了一定逻辑的对象数据库；接口上更加丰富、自带GC、支持对象 间的复杂逻辑；当然API Server本身是无状态的 数据都在etcd当中；
- API Server提供基于RESTful的管理接口，支持增删改查和patch、监听的操作，其他组件通过和API Server的接口获取资源配置和状态，以实现各种资源处理逻辑。

## 架构图

![APiserver架构图]()


## 参考
[如何扩展Kubernetes管理的资源对象](http://dockone.io/article/2405)
