# Kubernetes deep dive: API Server – part 2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [etcd](#etcd)
  - [Cluster state in etcd](#cluster-state-in-etcd)
  - [Serialization of State Flow in Detail](#serialization-of-state-flow-in-detail)
  - [Validation and Admission](#validation-and-admission)
  - [Migration of Storage Objects](#migration-of-storage-objects)

<!-- END MUNGE: GENERATED_TOC -->

API Server本身是无状态的，是与分布式存储etcd直接对话的唯一组件。

## etcd
从你的*nix操作系统，可以知道`/etc`用于存储配置数据。
实际上，etcd的名称是受其启发，“d”代表了distributed。
所有的分布式系统可能都需要像etcd这样的东西来存储有关系统状态的数据，使其能够以一致、可靠的方式来获取系统状态。

为了协调分布式设置中的数据访问，etcd使用了Raft协议。
在概念上，数据模型上支持的是key-value存储。
在etcd2中，keys形成了一个层次结构。
在etcd3中，引入了如下所示的平面模型，同时保持了与etcd2中`分层键key`的兼容性：
![etcd-101](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/etcd-101.png)

使用容器化版本的etcd，我们可以创建上面的树，然后检索如下：
```json
$ docker run --rm -d -p 2379:2379 \ 
 --name test-etcd3 quay.io/coreos/etcd:v3.1.0 /usr/local/bin/etcd \
 --advertise-client-urls http://0.0.0.0:2379 --listen-client-urls http://0.0.0.0:2379
$ curl localhost:2379/v2/keys/foo -XPUT -d value="some value"
$ curl localhost:2379/v2/keys/bar/this -XPUT -d value=42
$ curl localhost:2379/v2/keys/bar/that -XPUT -d value=take
$ http localhost:2379/v2/keys/?recursive=true
HTTP/1.1 200 OK 
Content-Length: 327
Content-Type: application/json
Date: Tue, 06 Jun 2017 12:28:28 GMT
X-Etcd-Cluster-Id: 10e5e39849dab251
X-Etcd-Index: 6
X-Raft-Index: 7
X-Raft-Term: 2

{
    "action": "get",
    "node": {
        "dir": true,
        "nodes": [
            {
                "createdIndex": 4,
                "key": "/foo",
                "modifiedIndex": 4,
                "value": "some value"
            },
            {
                "createdIndex": 5,
                "dir": true,
                "key": "/bar",
                "modifiedIndex": 5,
                "nodes": [
                    {
                        "createdIndex": 5,
                        "key": "/bar/this",
                        "modifiedIndex": 5,
                        "value": "42"
                    },
                    {
                        "createdIndex": 6,
                        "key": "/bar/that",
                        "modifiedIndex": 6,
                        "value": "take"
                    }
                ]
            }
        ]
    }
}
```
下面对kubernetes如何使用etcd进行介绍。

## Cluster state in etcd
在Kubernetes中，etcd是控制平面的独立组件。
直到Kubernetes 1.5.2，我们都是使用etcd2，之后就切换到etcd3了。

在Kubernetes 1.5.x etcd3仍然在v2 API模式下使用，并且可以向前转变为v3 API，包括所使用的数据模型。
从开发人员的角度来看，影响不大，因为API Server负责与etcd进行交互，比较v2和v3的存储后端实现。
但是，从群集管理员的角度来看，知道使用哪个etcd版本是很有必要的，因为在不同的etcd版本进行备份和恢复的操作是不一样的。

可以通过下述参数对etcd进行设置
```
$ kube-apiserver -h
...
--etcd-cafile string   SSL Certificate Authority file used to secure etcd communication.
--etcd-certfile string SSL certification file used to secure etcd communication.
--etcd-keyfile string  SSL key file used to secure etcd communication.
...
--etcd-quorum-read     If true, enable quorum read.
--etcd-servers         List of etcd servers to connect with (scheme://ip:port) …
...
```

Kubernetes将其objects以JSON字符或者[Protocol Buffers](https://developers.google.com/protocol-buffers/)（简称“protobuf”）的格式存储在etcd中。
让我们来看一个具体的例子，etcd version 3.1.0：
```yaml
$ cat pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: webserver
  namespace: apiserver-sandbox
spec:
  containers:
  - name: nginx
    image: tomaskral/nonroot-nginx
    ports:
    - containerPort: 80

$ kubectl create -f pod.yaml 

$ etcdctl ls /
/registry

$ etcdctl get /registry/pods/apiserver-sandbox/webserver
{
  "kind": "Pod",
  "apiVersion": "v1",
  "metadata": {
    "name": "webserver",
...
```

那么，从`kubectl create -f pod.yaml`开始，是如何最终存储到etcd中的？
数据流如下图所示：
![API-server-serialization-overview](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-serialization-overview.png)
1. 如kubectl一样的client端提供一个desired object state，如Version v1中的YAML。
2. kubectl converts the YAML into JSON to send it over the wire.
3. 在不同`version`中定义的同一个`kind`，API Server可以进行无损转换，利用注释来存储在旧API Version中无法表达的信息。
4. API Server将输入的对象状态转换为`一个权威的存储版本`，具体取决于API Server版本本身，通常是最新的stable版本，例如v1。
5. 在etcd的实际存储过程中，对于一个key，会将其转换为JSON或protobuf编码。

可以使用`--storage-media-type`对kube-apiserver的序列化进行配置，该选项默认为`application/vnd.kubernetes.protobuf`。
同样可以使用`--storage-versions`来设置每个Group的默认存储Version。

下面来看看无损转换在实践中是如何工作。
我们使用type为[Horizontal Pod Autoscaling](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/)的Kubernetes对象。HPA有一个controller监督和更新ReplicationController，根据设定的利用率指标(utilization metrics)来进行更新。
```yaml
$ cat registry-rc.yaml
apiVersion: v1
kind: ReplicationController
metadata:
  name: registry
  namespace: default
spec:
  replicas: 1
  selector:
    name: registry
  template:
    metadata:
      labels:
        name: registry
    spec:
      containers:
        - name: registry
          image: registry:v1
```
```yaml
$ kubectl create -f registry-rc.yaml

$ kubectl autoscale rc registry --min=2 --max=5 --cpu-percent=80

$ kubectl get hpa/registry -o yaml
apiVersion: autoscaling/v1
kind: HorizontalPodAutoscaler
metadata:
  creationTimestamp: 2017-10-16T10:47:50Z
  name: registry
  namespace: default
  resourceVersion: "209"
  selfLink: /apis/autoscaling/v1/namespaces/default/horizontalpodautoscalers/registry
  uid: 74467372-b25f-11e7-a5d3-080027e58fc6
spec:
  maxReplicas: 5
  minReplicas: 2
  scaleTargetRef:
    apiVersion: v1
    kind: ReplicationController
    name: registry
  targetCPUUtilizationPercentage: 80
status:
  currentCPUUtilizationPercentage: 0
  currentReplicas: 2
  desiredReplicas: 2
  lastScaleTime: 2017-10-16T10:47:50Z
```

现在，让我们访问Api Server，分别使用HPA object当前的稳定版本`autoscaling/v1`和前一个版本`extensions/v1beta1`。
也可以使用[httpie](https://httpie.org/)工具来代替curl。
```json
$ curl http://192.168.56.101:8080/apis/extensions/v1beta1/namespaces/default/horizontalpodautoscalers/registry > hpa-v1beta1.json
{
  "kind": "HorizontalPodAutoscaler",
  "apiVersion": "extensions/v1beta1",
  "metadata": {
    "name": "registry",
    "namespace": "default",
    "selfLink": "/apis/extensions/v1beta1/namespaces/default/horizontalpodautoscalers/registry",
    "uid": "74467372-b25f-11e7-a5d3-080027e58fc6",
    "resourceVersion": "209",
    "creationTimestamp": "2017-10-16T10:47:50Z"
  },
  "spec": {
    "scaleRef": {
      "kind": "ReplicationController",
      "name": "registry",
      "apiVersion": "v1",
      "subresource": "scale"
    },
    "minReplicas": 2,
    "maxReplicas": 5,
    "cpuUtilization": {
      "targetPercentage": 80
    }
  },
  "status": {
    "lastScaleTime": "2017-10-16T10:47:50Z",
    "currentReplicas": 2,
    "desiredReplicas": 2,
    "currentCPUUtilizationPercentage": 0
  }
}

$ curl http://192.168.56.101:8080/apis/autoscaling/v1/namespaces/default/horizontalpodautoscalers/registry > hpa-v1.json
{
  "kind": "HorizontalPodAutoscaler",
  "apiVersion": "autoscaling/v1",
  "metadata": {
    "name": "registry",
    "namespace": "default",
    "selfLink": "/apis/autoscaling/v1/namespaces/default/horizontalpodautoscalers/registry",
    "uid": "74467372-b25f-11e7-a5d3-080027e58fc6",
    "resourceVersion": "209",
    "creationTimestamp": "2017-10-16T10:47:50Z"
  },
  "spec": {
    "scaleTargetRef": {
      "kind": "ReplicationController",
      "name": "registry",
      "apiVersion": "v1"
    },
    "minReplicas": 2,
    "maxReplicas": 5,
    "targetCPUUtilizationPercentage": 80
  },
  "status": {
    "lastScaleTime": "2017-10-16T10:47:50Z",
    "currentReplicas": 2,
    "desiredReplicas": 2,
    "currentCPUUtilizationPercentage": 0
  }
}
```
用diff对上面两个输出进行对比
```
$ diff -u hpa-v1beta1.json hpa-v1.json
--- hpa-v1beta1.json	2017-10-16 06:59:48.603581169 -0400
+++ hpa-v1.json	2017-10-16 06:59:31.938252669 -0400
@@ -1,26 +1,23 @@
 {
   "kind": "HorizontalPodAutoscaler",
-  "apiVersion": "extensions/v1beta1",
+  "apiVersion": "autoscaling/v1",
   "metadata": {
     "name": "registry",
     "namespace": "default",
-    "selfLink": "/apis/extensions/v1beta1/namespaces/default/horizontalpodautoscalers/registry",
+    "selfLink": "/apis/autoscaling/v1/namespaces/default/horizontalpodautoscalers/registry",
     "uid": "74467372-b25f-11e7-a5d3-080027e58fc6",
     "resourceVersion": "209",
     "creationTimestamp": "2017-10-16T10:47:50Z"
   },
   "spec": {
-    "scaleRef": {
+    "scaleTargetRef": {
       "kind": "ReplicationController",
       "name": "registry",
-      "apiVersion": "v1",
-      "subresource": "scale"
+      "apiVersion": "v1"
     },
     "minReplicas": 2,
     "maxReplicas": 5,
-    "cpuUtilization": {
-      "targetPercentage": 80
-    }
+    "targetCPUUtilizationPercentage": 80
   },
   "status": {
     "lastScaleTime": "2017-10-16T10:47:50Z",
```
可以看到`HorizontalPodAutoscaler`的schema从`v1beta1`变成了`v1`。Api Server能够在这些版本之间进行无损转换，不管etcd实际上存储的是哪个版本。

现在让我们来关注 how the API server encodes and decodes payload as well as stores it in either JSON or protobuf。

## Serialization of State Flow in Detail
API Server将所有已知的Kubernetes object kinds保存在名为Scheme的Go [type registry](https://github.com/kubernetes/kubernetes/tree/master/pkg/registry)中。
In this registry, each version of kinds are defined along with how they can be converted, how new objects can be created, and how objects can be encoded and decoded to JSON or protobuf.

![API-server-storage-flow](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-storage-flow.png)

当API Server接收一个object时（比如来自与kubectl），它将从HTTP path中获取期待的Version。
它使用Scheme以正确的Version创建一个与之匹配的空对象，并使用JSON或protobuf解码器对HTTP payload进行转换。
解码器会将二进制payload转换为创建的对象。

解码的对象是 one of the supported versions for the given type。 
对于某些`types`，在整个开发过程中可能会有多个version。
为了避免出现问题，API Server必须要知道如何在每一对版本之间进行转换（例如，v1⇔v1alpha1，v1⇔v1beta1，v1beta1⇔v1alpha1）。
API Server为每个 types 使用一个特殊的`internal version`。 
一个type的internal version 是该type的所有version的superset，它具有所有version的功能。
Decoder会首先把the incoming object转换到internal version，然后将其转换为the storage version，如下所示：
```
v1beta1 ⇒ internal ⇒ v1
```
在转换的第一步中，如果用户省略了某些字段，Decoder会把其设置为默认值。
想象一下，`v1beta1`缺少一些在`v1`中是必填的字段。
在这种情况下，用户甚至不能填写该字段的值。
然后，转换步骤将为此字段设置默认值，以创建有效的 internal object。

## Validation and Admission
转换之后还有两个重要的步骤。实际流程如下所示：
```
v1beta1 ⇒ internal ⇒    |    ⇒       |    ⇒  v1  ⇒ json/yaml ⇒ etcd
                     admission    validation
```
Validation 和 Admission 是进行对象的创建和更新。 这是他们的角色：
1. **Admission**，通过验证集群的全局约束，可以检查是否可以创建或更新对象，并根据集群配置设置默认值。
  - NamespaceLifecycle，如果context中的namespace不存在，拒绝request
  - LimitRanger，在一个namespace中对所有资源使用limit限制，目前仅支持cpu和memory。（设置该namespace内所有的pod、container单个的资源使用上限）
  - ServiceAccount，creates a service account for a pod.
  - DefaultStorageClass，设置PersistentVolumeClaim默认的storage class，防止用户不提供值。
  - ResourceQuota - 为集群上的当前用户强制执行配额约束，如果配额不足，可能会拒绝请求。 目前支持cpu、memory、pods数量等。（设置整个namespace的总配额）
2. **Validation**，检查一个incoming object (during creation and updates)是否有效。比如：
  - 检查所有必填字段是否设置。
  - 检查所有字符串是否具有有效的格式（例如，只包括小写字符）。
  - 检查没有设置矛盾的字段（例如，两个具有相同名称的容器）。

Validation不会查看该type的其他实例，甚至其它type的实例。
换句话说，validation is local, static checks for each object，独立于任何API Server配置。

可以使用`--admission-control = <plugins>`标志来启用/禁用`Admission`插件。
它们中的大多数也可以由群集管理员进行配置。
此外，在Kubernetes 1.7中，有一个webhook机制来扩展`Admission`机制；
同时还有一个initializer的概念使用controller来对新的object定制 Admission。

## Migration of Storage Objects
将Kubernetes升级到较新版本时，备份群集状态并遵循每个版本的迁移步骤变得越来越重要。
这源于从etcd2到etcd3的转变，以及Kubernetes  kinds 和 versions 的不断发展。

在etcd中，每个object都以其kind的首选storage version存储。
但是，随着时间的推移，可能会在etcd中存储着一个version很旧的object。
如果此version已被弃用且最终从API Server中删除，你将无法再解码其protobuf或JSON。
因此，在集群升级之前执行的`迁移过程`需要重写这些objects。

下面的文档有助于迁移的顺利进行：
1. [Cluster Management Guide for Version 1.6](https://kubernetes.io/docs/tasks/administer-cluster/upgrade-1-6/)
2. [Upgrading to a different API version](https://kubernetes.io/docs/tasks/administer-cluster/cluster-management/#upgrading-to-a-different-api-version)


下一篇文章，我们将讨论如何使用[Custom Resource Definitions](https://github.com/kubernetes/features/issues/95)和`User API Servers`来扩展Kubernetes API。

## 参考
译自[Kubernetes Deep Dive: API Server – Part 2](https://blog.openshift.com/kubernetes-deep-dive-api-server-part-2/)





















