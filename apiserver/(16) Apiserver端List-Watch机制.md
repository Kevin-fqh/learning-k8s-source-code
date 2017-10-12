# Apiserver端List-Watch机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [UndecoratedStorage](#undecoratedstorage)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

Apiserver针对每一类资源(pod、service、replication controller),都会与etcd建立一个连接。，获取该资源的opt。
Watch功能就是其中的一个opt。

什么是watch?kubelet、kube-controller-manager、kube-scheduler需要监控各种资源(pod、service等)的变化，
当这些对象发生变化时(add、delete、update)，kube-apiserver能够主动通知这些组件。这是Client端的Watch实现。

而Apiserver端的Watch机制是建立在etcd的Watch基础上的。
etcd的watch是没有过滤功能的，而kube-apiserver增加了过滤功能。

什么是过滤功能？，比如说kubelet只对调度到本节点上的pod感兴趣，也就是pod.host=node1；
而kube-scheduler只对未被调度的pod感兴趣，也就是pod.host=”“。
etcd只能watch到pod的add、delete、update。
kube-apiserver则增加了过滤功能，将订阅方感兴趣的部分资源发给订阅方。

## 


## 总结
kube-apiserver初始化时，建立对etcd的连接，并对etcd进行watch，将watch的结果存入watchCache。
当其他组件需要watch资源时，其他组件向apiserver发送一个watch请求，这个请求是可以带filter函数的。
apiserver针对这个请求会创建一个watcher，并基于watcher创建WatchServer。
watchCache watch的对象，首先会通过filter函数的过滤，假如过滤通过的话，则会通过WatcherServer发送给订阅组件。