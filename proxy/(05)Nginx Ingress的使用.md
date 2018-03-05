# Nginx Ingress的使用

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Ingress解决了什么问题](#ingress解决了什么问题)
  - [Ingress的组成](#ingress的组成)	
  - [Ingress搭建](#ingress搭建)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

Kubernetes 暴露内部服务给外部使用的方式有三种：LoadBlancer Service、NodePort Service、Ingress

## Ingress解决了什么问题
暴露k8s内部服务给外部使用会面临如下问题：

1. pod IP动态变化的问题

Pod的IP是会动态变化的，那么如何把这个动态的Pod IP暴露出去？
这里借助于Service机制，以label的形式选定一组带有指定标签的Pod，并监控和自动负载他们的 Pod IP，向外只需要暴露Service IP即可；
这就是`NodePort模式`：即在每个节点上开起一个端口，然后转发到内部Pod IP上。

2. 端口资源有限

使用NodePort模式的一个缺陷在于一个主机节点的端口资源是有限的，不适用于服务数量多的情况。

所以解决思路是：使用nginx在前端监听一个端口，然后按照域名(svc)向后转发。简单的实现就是使用DaemonSet在每个node上监听80端口，然后写好规则，因为 Nginx外面绑定了宿主机80端口(就像 NodePort)，本身又在集群内，那么向后直接转发到相应Service IP就行了。
相当于Nginx充当k8s集群内外交互的桥梁。

3. 域名变化和转发规则动态更新的问题

每次有新服务加入怎么改 Nginx 配置？ 不可能每次都手工更改吧？

Ingress就是为解决这个问题而产生的。

原来需要改Nginx配置，然后配置各种域名对应哪个Service，现在把这个动作抽象出来，变成一个`Ingress对象`，你可以用yaml创建，每次不要去改Nginx了，直接改yaml文件，然后创建/更新即可。 

通过创建/更新yaml来实现Nginx转发规则的更新。

## Ingress的组成
`Ingress`由两部分组成：Ingress Controller 和 Ingress(此处指的是k8s的一种资源ingress)。

资源ingress定义了路径到k8s集群service的一种映射关系，即流量转发规则。
从而实现，外部用户只需访问ingress中定义的路径即可访问真正的pod应用。(ingress定义的路径-->service:port-->pod:port)

Ingress Controoler通过与Kubernetes API的交互，动态的去感知集群中`Ingress规则`的变化，然后读取它，按照固定模板生成一段`Nginx 配置`，再写到 Nginx Pod里，最后reload一下。

Kubernetes已经将Nginx与Ingress Controller合并为一个组件，所以只需要部署Ingress Controller即可

## Ingress搭建
1. 首先搭建好希望对外提供的业务服务，我们这里搭建的是一个etcd的集群, 可以看到其中一个节点的service name是`pod1`，访问端口为2379
```shell
[root@fqhnode01 yaml]# kubectl get pod
NAME         READY     STATUS    RESTARTS   AGE
pod1-sh0mt   1/1       Running   0          48m
pod2-v8xs3   1/1       Running   0          47m
pod3-vtzg0   1/1       Running   0          46m

[root@fqhnode01 yaml]# kubectl get svc
NAME         CLUSTER-IP      EXTERNAL-IP   PORT(S)                                      AGE
kubernetes   10.10.0.1       <none>        443/TCP                                      49m
pod1         10.10.230.174   <none>        7070/TCP,2379/TCP,2380/TCP,22/TCP,9999/TCP   48m
pod2         10.10.47.44     <none>        7070/TCP,2379/TCP,2380/TCP,22/TCP,9999/TCP   47m
pod3         10.10.98.224    <none>        7070/TCP,2379/TCP,2380/TCP,22/TCP,9999/TCP   47m

[root@fqhnode01 yaml]# etcdctl --debug --endpoints http://10.10.230.174:2379 cluster-health
Cluster-Endpoints: http://10.10.230.174:2379
cURL Command: curl -X GET http://10.10.230.174:2379/v2/members
member 1af57124460c9172 is healthy: got healthy result from http://172.17.0.5:2379
member 7f76aa988d1e65cf is healthy: got healthy result from http://172.17.0.7:2379
member d37248e095babbc0 is healthy: got healthy result from http://172.17.0.6:2379
cluster is healthy

[root@fqhnode01 yaml]# curl -X GET http://10.10.230.174:2379/v2/members
{"members":[{"id":"1af57124460c9172","name":"10.10.230.174","peerURLs":["http://172.17.0.5:2380"],"clientURLs":["http://172.17.0.5:2379"]},{"id":"7f76aa988d1e65cf","name":"10.10.98.224","peerURLs":["http://172.17.0.7:2380"],"clientURLs":["http://172.17.0.7:2379"]},{"id":"d37248e095babbc0","name":"10.10.47.44","peerURLs":["http://172.17.0.6:2380"],"clientURLs":["http://172.17.0.6:2379"]}]}
```
可以发现，此时etcd集群是可以通过service IP来进行访问的。

2. 搭建default-http-backend，定义Nginx不能正确转发时使用的后端，有如404 Not Found等页面。其中default-http-backend.yaml内容如下所示：
```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: default-http-backend
  labels:
    k8s-app: default-http-backend
  namespace: kube-system
spec:
  replicas: 1
  template:
    metadata:
      labels:
        k8s-app: default-http-backend
    spec:
      terminationGracePeriodSeconds: 60
      containers:
      - name: default-http-backend
        image: googlecontainer/defaultbackend:1.0
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
            scheme: HTTP
          initialDelaySeconds: 30
          timeoutSeconds: 5
        ports:
        - containerPort: 8080
        resources:
          limits:
            cpu: 10m
            memory: 20Mi
          requests:
            cpu: 10m
            memory: 20Mi
---
apiVersion: v1
kind: Service
metadata:
  name: default-http-backend
  namespace: kube-system
  labels:
    k8s-app: default-http-backend
spec:
  ports:
  - port: 80
    targetPort: 8080
  selector:
    k8s-app: default-http-backend
```

3. 搭建ingress controller,其中nginx-ingress-controller.yaml内容如下所示：
```yaml
apiVersion: extensions/v1beta1
kind: DaemonSet
metadata:
  name: nginx-ingress-lb
  labels:
    name: nginx-ingress-lb
  namespace: kube-system
spec:
  template:
    metadata:
      labels:
        name: nginx-ingress-lb
    spec:
      terminationGracePeriodSeconds: 60
      hostNetwork: true
      containers:
      - image: googlecontainer/nginx-ingress-controller:0.9.0-beta.11
        name: nginx-ingress-lb
        readinessProbe:
          httpGet:
            path: /healthz
            port: 10254
            scheme: HTTP
        livenessProbe:
          httpGet:
            path: /healthz
            port: 10254
            scheme: HTTP
          initialDelaySeconds: 10
          timeoutSeconds: 1
        env:
          - name: POD_NAME
            valueFrom:
              fieldRef:
                fieldPath: metadata.name
          - name: POD_NAMESPACE
            valueFrom:
              fieldRef:
                fieldPath: metadata.namespace
        args:
        - /nginx-ingress-controller
        - --apiserver-host=http://192.168.56.101:8080
        - --default-backend-service=kube-system/default-http-backend
```
注意`--apiserver-host`需要填apiserver运行的物理主机的IP地址

4. 搭建Ingress，即声明域名到后面service的映射关系。my-ingress.yaml内容如下所示：
```yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: etcd-cluster
  namespace: default
  annotations:
    ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - host: fqhnode
    http:
      paths:
      - path: /default/pod1
        backend:
          serviceName: pod1
          servicePort: 2379
```
需要注意的是`namespace: default`这个是和前面业务etcd集群一样的；而`host: fqhnode`这个则是host物理主机的域名；最后的`serviceName 和 servicePort`则是我们希望访问的service。

那么理论上，我们现在可以通过`curl fqhnode/default/pod1/`来访问之前搭建的etcd集群业务。

5. 效果如下
```shell
[root@fqhnode01 ingress]# curl fqhnode/default/pod1/v2/members
{"members":[{"id":"1af57124460c9172","name":"10.10.230.174","peerURLs":["http://172.17.0.5:2380"],"clientURLs":["http://172.17.0.5:2379"]},{"id":"7f76aa988d1e65cf","name":"10.10.98.224","peerURLs":["http://172.17.0.7:2380"],"clientURLs":["http://172.17.0.7:2379"]},{"id":"d37248e095babbc0","name":"10.10.47.44","peerURLs":["http://172.17.0.6:2380"],"clientURLs":["http://172.17.0.6:2379"]}]}

[root@fqhnode01 ingress]# kubectl get ingress etcd-cluster -o yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  annotations:
    ingress.kubernetes.io/rewrite-target: /
  creationTimestamp: 2018-03-05T12:11:12Z
  generation: 1
  name: etcd-cluster
  namespace: default
  resourceVersion: "1222"
  selfLink: /apis/extensions/v1beta1/namespaces/default/ingresses/etcd-cluster
  uid: 4b80dcf9-206e-11e8-8367-080027e58fc6
spec:
  rules:
  - host: fqhnode
    http:
      paths:
      - backend:
          serviceName: pod1
          servicePort: 2379
        path: /default/pod1
status:
  loadBalancer:
    ingress:
    - ip: 192.168.56.101
```

6. apply一下ingress.yaml,更改其中的path路径
```yaml
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: etcd-cluster
  namespace: default
  annotations:
    ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - host: fqhnode
    http:
      paths:
      - path: /myetcd
        backend:
          serviceName: pod1
          servicePort: 2379
```
对应的访问方式是`curl fqhnode/myetcd/v2/members`

最后要说的是，如果需要在k8s集群外部访问的话，只需要在外部的客户端主机上`/etc/hosts`添加一个域名`192.168.56.101 fqhnode`即可。

通常可以使用对外使用多个域名，但后面用的都是同一个IP。
```
foo.bar.com --|                 |-> foo.bar.com s1:80
              | 178.91.123.132  |
bar.foo.com --|                 |-> bar.foo.com s2:80
```

## 参考
[Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/#)

[官方ingress模板](https://github.com/kubernetes/kubernetes/tree/8d3a19229fe97b4dcee9167ca4538466f7720314/test/e2e/testing-manifests/ingress/http)

[Kubernetes Nginx Ingress 教程](https://mritd.me/2017/03/04/how-to-use-nginx-ingress/)
