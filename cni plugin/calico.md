# Calico

## 简介

Calico为容器和虚拟机提供安全的网络连接。

Calico创建并管理第3层平面网络，为每个宿主（容器、VM）分配一个完全可路由的IP地址。
Calico是一个纯三层的方法，使用虚拟路由代替虚拟交换，每一台虚拟路由通过BGP协议传播可达信息（路由）到剩余数据中心。

工作负载可以在没有IP封装或网络地址转换的情况下进行通信，以实现裸机性能，更轻松的故障排除和更好的互操作性。 
在需要覆盖的环境中，Calico使用IP-in-IP tunneling或可以与其他overlay 网络如flannel一起使用。

Calico还提供网络安全规则的动态控制。 
使用Calico简单的策略语言，您可以实现对容器，虚拟机工作负载和裸机主机端点之间通信的精细控制。

已经可以应用在Kubernetes, OpenShift, and OpenStack中。

## 工作原理
Calico利用Linux内核本地的路由和iptables firewall功能。

故如果采用Calico的话，宿主上会存在大量的iptables规则，在节点规模大的情况下，根本无法维护。

所有来自容器，虚拟机和主机的所有流量在路由到其目标之前，都会遍历这些内核中的规则。

![Calico.png](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/Calico.png)

- calicoctl：通过简单的命令行界面实现高级策略和网络连接。

- orchestrator plugin：负责和k8s等进行协调

- key/value store：保存Calico的策略和网络配置状态。

- calico/node：在每个主机上运行，从键/值存储中读取相关的策略和网络配置信息，并在Linux内核中实现。

## Calico部署

参考[calico](https://docs.projectcalico.org/v2.5/getting-started/kubernetes/installation/integration)

### k8s集群
```
[root@node-141 etcd-v3.1.5-linux-amd64]# ./etcd -name 141etcd -data-dir '/var/lib/etcd/141etcd' -advertise-client-urls 'http://12.12.223.141:2379' --listen-client-urls 'http://12.12.223.141:2379' &

[root@151node ~]# kube-apiserver --logtostderr=true --v=0 --insecure-bind-address=0.0.0.0 --insecure-port=8080 --etcd-servers=http://12.12.223.141:2379 --service-cluster-ip-range=10.10.10.0/16
[root@151node ~]# kube-controller-manager --logtostderr=true --v=0 --master=http://0.0.0.0:8080
[root@151node ~]# kube-scheduler --logtostderr=true --v=0 --master=http://0.0.0.0:8080
[root@151node ~]# kube-proxy --logtostderr=true --v=0 --master=http://0.0.0.0:8080
[root@151node ~]# kubelet --address=0.0.0.0 --api-servers=http://0.0.0.0:8080  --logtostderr=true --v=0 --cgroup-driver=systemd --network-plugin=cni --cni-conf-dir=/etc/cni/net.d --cni-bin-dir=/opt/cni/bin

[root@152node ~]# kube-proxy --logtostderr=true --v=0 --master=http://12.12.223.151:8080
[root@152node ~]# kubelet --address=0.0.0.0 --api-servers=http://12.12.223.151:8080  --logtostderr=true --v=0 --cgroup-driver=systemd --network-plugin=cni --cni-conf-dir=/etc/cni/net.d --cni-bin-dir=/opt/cni/bin
```

做完下面的第4小步，再启动kubelet

### calico部署
参考[calico](https://docs.projectcalico.org/v2.5/getting-started/kubernetes/installation/integration)

1. 用calicoctl启动calico node（会以docker方式起）
```bash
# Download and install `calicoctl`
wget https://github.com/projectcalico/calicoctl/releases/download/v1.5.0/calicoctl
sudo chmod +x calicoctl

# Run the calico/node container
# sudo ETCD_ENDPOINTS=http://<ETCD_IP>:<ETCD_PORT> ./calicoctl node run --node-image=quay.io/calico/node:v2.5.1
sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl node run --node-image=quay.io/calico/node:v2.5.1
```

2. 下载kubelet需要的cni plugin

```bash
wget -N -P /opt/cni/bin https://github.com/projectcalico/cni-plugin/releases/download/v1.10.0/calico
wget -N -P /opt/cni/bin https://github.com/projectcalico/cni-plugin/releases/download/v1.10.0/calico-ipam
chmod +x /opt/cni/bin/calico /opt/cni/bin/calico-ipam
```

3. 下载standard CNI lo plugin
```bash
wget https://github.com/containernetworking/cni/releases/download/v0.3.0/cni-v0.3.0.tgz
tar -zxvf cni-v0.3.0.tgz
sudo cp loopback /opt/cni/bin/
```

4. cni配置
```
mkdir -p /etc/cni/net.d
cat >/etc/cni/net.d/10-calico.conf <<EOF
{
    "name": "calico-k8s-network",
    "cniVersion": "0.1.0",
    "type": "calico",
    "etcd_endpoints": "http://<ETCD_IP>:<ETCD_PORT>",
    "log_level": "info",
    "ipam": {
        "type": "calico-ipam"
    },
    "policy": {
        "type": "k8s"
    },
    "kubernetes": {
        "kubeconfig": "</PATH/TO/KUBECONFIG>"
    }
}
EOF
```

我的配置如下：
```
[root@151node calico]# cat /etc/cni/net.d/10-calico.conf
{
    "name": "calico-k8s-network",
    "cniVersion": "0.1.0",
    "type": "calico",
    "etcd_endpoints": "http://12.12.223.141:2379",
    "log_level": "info",
    "ipam": {
        "type": "calico-ipam"
    },
    "policy": {
        "type": "k8s"
    },
    "kubernetes": {
        "kubeconfig": "/root/.kube/config"
    }
}
[root@151node calico]# cat /root/.kube/config
apiVersion: v1
clusters:
- cluster:
    server: http://12.12.223.151:8080
  name: kubernetes
contexts:
- context:
    cluster: kubernetes
    user: visitor
  name: kubernetes-context
current-context: kubernetes-context
kind: Config
preferences: {}
users:
- name: visitor
  user: {}
```

5. 下载policy-controller.yaml
```bash
# wget https://docs.projectcalico.org/v2.5/getting-started/kubernetes/installation/policy-controller.yaml
# 修改其中的<ETCD_ENDPOINTS>
# kubectl create -f policy-controller.yaml


[root@151node calico]# kubectl get po -n kube-system
NAME                                        READY     STATUS    RESTARTS   AGE
calico-policy-controller-2427550665-lbdqj   1/1       Running   0          41m

[root@151node calico]# cat policy-controller.yaml 
# Calico Version v2.5.1
# https://docs.projectcalico.org/v2.5/releases#v2.5.1
# This manifest includes the following component versions:
#   calico/kube-policy-controller:v0.7.0

# Create this manifest using kubectl to deploy
# the Calico policy controller on Kubernetes.
# It deploys a single instance of the policy controller.
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: calico-policy-controller
  namespace: kube-system
  labels:
    k8s-app: calico-policy
spec:
  # Only a single instance of the policy controller should be
  # active at a time.  Since this pod is run as a Deployment,
  # Kubernetes will ensure the pod is recreated in case of failure,
  # removing the need for passive backups.
  replicas: 1
  strategy:
    type: Recreate
  template:
    metadata:
      name: calico-policy-controller
      namespace: kube-system
      labels:
        k8s-app: calico-policy
    spec:
      hostNetwork: true
      containers:
        - name: calico-policy-controller
          # Make sure to pin this to your desired version.
          image: quay.io/calico/kube-policy-controller:v0.7.0
          env:
            # Configure the policy controller with the location of
            # your etcd cluster.
            - name: ETCD_ENDPOINTS
              value: "http://12.12.223.141:2379"
            # Location of the Kubernetes API - this shouldn't need to be
            # changed so long as it is used in conjunction with
            # CONFIGURE_ETC_HOSTS="true".
            - name: K8S_API
              value: "https://kubernetes.default:443"
            # Configure /etc/hosts within the container to resolve
            # the kubernetes.default Service to the correct clusterIP
            # using the environment provided by the kubelet.
            # This removes the need for KubeDNS to resolve the Service.
            - name: CONFIGURE_ETC_HOSTS
              value: "true"
```

6. 启动calico node，所有节点都执行
```bash
[root@152node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl node run --node-image=quay.io/calico/node:v2.5.1
Running command to load modules: modprobe -a xt_set ip6_tables
Enabling IPv4 forwarding
Enabling IPv6 forwarding
Increasing conntrack limit
Removing old calico-node container (if running).
Running the following command to start calico-node:

docker run --net=host --privileged --name=calico-node -d --restart=always -e NODENAME=152node -e CALICO_NETWORKING_BACKEND=bird -e CALICO_LIBNETWORK_ENABLED=true -e ETCD_ENDPOINTS=http://12.12.223.141:2379 -v /var/log/calico:/var/log/calico -v /var/run/calico:/var/run/calico -v /lib/modules:/lib/modules -v /run:/run -v /run/docker/plugins:/run/docker/plugins -v /var/run/docker.sock:/var/run/docker.sock quay.io/calico/node:v2.5.1

Image may take a short time to download if it is not available locally.
Container started, checking progress logs.

Skipping datastore connection test
Using autodetected IPv4 address on interface ens37: 12.12.223.152/24
No AS number configured on node resource, using global value
Using node name: 152node
Starting libnetwork service
Calico node started successfully
```

7. 效果
```bash
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl node status
Calico process is running.

IPv4 BGP status
+---------------+-------------------+-------+----------+-------------+
| PEER ADDRESS  |     PEER TYPE     | STATE |  SINCE   |    INFO     |
+---------------+-------------------+-------+----------+-------------+
| 12.12.223.152 | node-to-node mesh | up    | 12:35:53 | Established |
+---------------+-------------------+-------+----------+-------------+

IPv6 BGP status
No IPv6 peers found.

[root@151node calico]# 
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get node
NAME      
151node
152node
```

`calicoctl node status`的时候至少需要两个或以上节点才能建立Bgp连接，如果是`AllInOne`的部署,单节点当然没有bgp邻居[45](https://github.com/gjmzj/kubeasz/issues/45)。


8. 管理ip pool，其中`192.168.0.0/16`为calico默认的ip pool
```bash
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get pool -o wide
CIDR                       NAT     IPIP    
192.168.0.0/16             true    false   
fd80:24e2:f998:72d6::/64   false   false

[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl delete pool 192.168.0.0/16
Successfully deleted 1 'ipPool' resource(s)
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get pool -o wide
CIDR                       NAT     IPIP    
fd80:24e2:f998:72d6::/64   false   false


[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl create -f calico-pool.yaml 
Successfully created 1 'ipPool' resource(s)
[root@151node calico]# 
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get pool -o wide
CIDR                       NAT     IPIP    
10.100.0.0/16              true    false
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get pool 10.100.0.0/16 -o yaml
- apiVersion: v1
  kind: ipPool
  metadata:
    cidr: 10.100.0.0/16
  spec:
    ipip:
      mode: always
	  enabled: false
    nat-outgoing: true

[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl create -f calico-pool.yaml 
Successfully created 1 'ipPool' resource(s)
[root@151node calico]# sudo ETCD_ENDPOINTS=http://12.12.223.141:2379 ./calicoctl get pool -o wide
CIDR                       NAT     IPIP    
10.100.0.0/16              true    false   
20.200.0.0/16              true    false   
fd80:24e2:f998:72d6::/64   false   false
```

### 测试连通性

```bash
[root@151node yaml]# cat registry.yaml 
apiVersion: v1
kind: ReplicationController
metadata:
  name: registry
  namespace: default
spec:
  replicas: 40
  selector:
    name: registry
  template:
    metadata:
      labels:
        name: registry
    spec:
      containers:
        - name: registry
          image: docker.io/registry
          ports:
          - containerPort: 5000
            name: registry
            protocol: TCP
[root@151node yaml]# kubectl create -f registry.yaml 
replicationcontroller "registry" created
```

#### node ping 分布在自身的pod
```
[root@151node yaml]# kubectl get po -o wide
NAME             READY     STATUS    RESTARTS   AGE       IP               NODE
registry-17zc4   1/1       Running   0          32m       10.100.151.208   151node
registry-324mg   1/1       Running   0          32m       10.100.73.80     152node
registry-378l2   1/1       Running   0          32m       10.100.151.203   151node
registry-3pshh   1/1       Running   0          32m       10.100.73.70     152node
registry-4fjzf   1/1       Running   0          32m       10.100.151.199   151node

[root@151node yaml]# ping 10.100.151.192
PING 10.100.151.192 (10.100.151.192) 56(84) bytes of data.
64 bytes from 10.100.151.192: icmp_seq=1 ttl=64 time=0.120 ms
64 bytes from 10.100.151.192: icmp_seq=2 ttl=64 time=0.061 ms
64 bytes from 10.100.151.192: icmp_seq=3 ttl=64 time=0.084 ms
64 bytes from 10.100.151.192: icmp_seq=4 ttl=64 time=0.099 ms

--- 10.100.151.192 ping statistics ---
4 packets transmitted, 4 received, 0% packet loss, time 3000ms
rtt min/avg/max/mdev = 0.061/0.091/0.120/0.021 ms

[root@151node yaml]# ip route
default via 192.168.181.2 dev ens33 proto static metric 100 
10.100.73.64/26 via 12.12.223.152 dev ens37 proto bird 
10.100.151.192 dev cali963131f0902 scope link 
blackhole 10.100.151.192/26 proto bird 
10.100.151.193 dev cali03d9fe6dae7 scope link 
10.100.151.194 dev calia6561cad39f scope link 
10.100.151.195 dev cali8c1db875518 scope link 
10.100.151.196 dev calie0dfa16e65d scope link 
10.100.151.197 dev cali639a91e418f scope link 
10.100.151.198 dev cali2b09270fc02 scope link 
10.100.151.199 dev calid505d705fb0 scope link 
10.100.151.200 dev cali72a5f2b9eac scope link 
10.100.151.201 dev calif48d1c80afc scope link 
10.100.151.202 dev cali1f38e4219b3 scope link 
10.100.151.203 dev califbd2867929f scope link 
10.100.151.204 dev cali92506aa3c5e scope link 
10.100.151.205 dev calif25d2d844cb scope link 
10.100.151.206 dev calif8bec296d62 scope link 
10.100.151.207 dev calie01456d5332 scope link 
10.100.151.208 dev calif90be432d9a scope link 
10.100.151.209 dev caliddf3ff9a536 scope link 
10.100.151.210 dev cali243e3577560 scope link 
12.12.223.0/24 dev ens37 proto kernel scope link src 12.12.223.151 metric 100 
172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1 
192.168.181.0/24 dev ens33 proto kernel scope link src 192.168.181.139 metric 100
```

可以看出calico给每个pod和本地node之间建立了一个veth对

#### 151node ping 另外一个node的pod

```bash
# kubectl get pod
registry-sdgdw   1/1       Running             0          44s       20.20.111.194   152node

# ping 20.20.111.194
PING 20.20.111.194 (20.20.111.194) 56(84) bytes of data.
```

如果跨node ping不同，可能是iptables规则的问题，执行`iptables -F`


 
