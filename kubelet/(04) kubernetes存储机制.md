# kubernetes存储机制

<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Volume](#volume)
    - [背景](#背景)
    - [Volume的类型](#volume的类型)
	  - [EmptyDir](#emptydir)
	  - [hostPath](#hostpath)
	  - [ConfigMap](#configmap)
	  - [secret](#secret)
	  - [persistentVolumeClaim](#persistentvolumeclaim)
	  - [RBD](#rbd)
	  - [CSI](#csi)
    - [PersistentVolumes](#persistentvolumes)
	  - [简介](#简介)
	  - [和普通volume的区别](#和普通volume的区别)
	  - [生命周期](#生命周期)
	  - [pv支持的volume类型](#pv支持的volume类型)
	  - [PV](#pv)
	  - [PersistentVolumeClaims](#persistentvolumeclaims)
	  - [Claims As Volumes](#claims-as-volumes)
  - [StorageClass](#storageclass)
  - [Dynamic Volume Provisioning](#dynamic-volume-provisioning)
	  - [Enabling Dynamic Provisioning](#enabling-dynamic-provisioning)
	  - [Using Dynamic Provisioning](#using-dynamic-provisioning)
	  - [Defaulting Behavior](#defaulting-behavior)
  - [一个组合方案](#一个组合方案)
  - [总结](#总结)
  - [参考](#参考)

本文档基于v1.9的特性进行说明，译自https://kubernetes.io/docs/concepts/storage。

## Volume
Kubernetes Volume解决了什么问题？首先，当一个容器崩溃时，kubelet会重新启动它，然后容器里面的文件都会丢失，容器会从一个干净的状态开始。
其次，在一个pod里面运行的多个容器之间通常需要共享文件。Kubernetes Volume的概念正好解决了这两个问题。

### 背景
Docker也有一个volume的概念，但其功能特性较少。在Docker中，一个volume可能就直接是物理机磁盘（或者是其它容器）上的一个目录。
其生命周期不受管理，直到最近的版本才出现local-disk-backed volumes。
Docker现在也提供了volume Drive，但现在功能比较有限。

另一方面，kubernetes的volume则具有明确的寿命，和使用其的pod一样。
因此，一个volume的存活时间是超出任何一个运行在pod中的container的，当一个container重启的时候，数据也会被保存下来。
当然，当一个pod消亡了，该volume也跟随消亡。可能更为重要的是，kubernetes支持多种类型的volume，一个pod可以同时使用任意数量的volume。

Volume的核心其实也是一个目录，里面可能存放着一些数据，允许一个pod中的所有containers访问。
该目录是如何形成的，取决于支持它的介质和选择的volume类型。
要使用一个kubernetes volume，可以在pod的spec.volumes 字段中声明。
然后kubernetes会把该volume挂载到容器的spec.containers.volumeMounts字段中。
容器中的进程会看到一个由docker image和volume组成的文件系统视图。
docker image位于文件系统结构树的root目录下，而所有的volume则会被挂载到该目录树的特定路径下。

Volume不可以被挂载到其它的volume上，两个volume之间也不允许有硬链接的存在。Pod中的每一个container需要单独去挂载每一个volume。

### Volume的类型
Kubernetes支持多种类型的volume，如下所示：
```
awsElasticBlockStore
azureDisk
azureFile
cephfs
configMap
csi
downwardAPI
emptyDir
fc (fibre channel)
flocker
gcePersistentDisk
gitRepo
glusterfs
hostPath
iscsi
local
nfs
persistentVolumeClaim
projected
portworxVolume
quobyte
rbd
scaleIO
secret
storageos
vsphereVolume
```

下面选择几个来简单介绍一下。
#### EmptyDir
EmptyDir是一个空目录，他的生命周期和所属的 Pod 是完全一致的，可能读者会奇怪，那还要他做什么？
EmptyDir的用处是，可以在同一 Pod 内的不同容器之间共享工作过程中产生的文件。	
缺省情况下，EmptyDir 是使用主机磁盘进行存储的，也可以设置emptyDir.medium 字段的值为Memory，来提高运行速度，但是这种设置，对该卷的占用会消耗容器的内存份额。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pd
spec:
  containers:
  - image: k8s.gcr.io/test-webserver
    name: test-container
    volumeMounts:
    - mountPath: /cache
      name: cache-volume
  volumes:
  - name: cache-volume
    emptyDir: {}
```

#### hostPath

hostpath用于把该容器本地host主机上的文件或者目录挂载到你的容器当中，因为数据只能存在于一个host主机上。
如果pod发生了迁移，数据并不会发生迁移。
这种卷一般和DaemonSet搭配使用，用来操作主机文件，例如进行日志采集的 FLK 中的 FluentD 就采用这种方式，加载主机的容器日志目录，达到收集本主机所有日志的目的。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pd
spec:
  containers:
  - image: k8s.gcr.io/test-webserver
    name: test-container
    volumeMounts:
    - mountPath: /test-pd
      name: test-volume
  volumes:
  - name: test-volume
    hostPath:
      # directory location on host
      path: /data
      # this field is optional
      type: Directory
```

#### ConfigMap

镜像使用的过程中，经常需要利用配置文件、启动脚本等方式来影响容器的运行方式，如果仅有少量配置，我们可以使用环境变量的方式来进行配置。
然而对于一些较为复杂的配置，就很难用这种方式进行控制了。
另外一些敏感信息暴露在 YAML 中也是不合适的。

configMap资源提供了一种将配置数据注入到Pod中的方法。
存储在ConfigMap对象中的数据可以在configMap类型的卷中引用，然后由运行在Pod中的容器化应用使用。
当引用一个configMap对象时，可以在卷中提供它的名字来引用它。 您还可以自定义用于ConfigMap中特定条目的路径。

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: special-config
  namespace: default
data:
  special.how: very
  special.type: charm
```

1. volume方式挂载configMap
```yaml
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
          image: etcdimage:0.3
          volumeMounts:
          - name: config-volume
            mountPath: /etc/config
      volumes:
      - name: config-volume
        configMap:
          name: special-config
```

效果如下：
```shell
root@registry-qbr3x:/# ll /etc/config/
total 4
drwxrwxrwx. 3 root root   94 Mar  6 03:28 ./
drwxr-xr-x. 1 root root 4096 Mar  6 03:28 ../
drwxr-xr-x. 2 root root   43 Mar  6 03:28 ..3983_05_03_22_28_27.572178815/
lrwxrwxrwx. 1 root root   31 Mar  6 03:28 ..data -> ..3983_05_03_22_28_27.572178815/
lrwxrwxrwx. 1 root root   18 Mar  6 03:28 special.how -> ..data/special.how
lrwxrwxrwx. 1 root root   19 Mar  6 03:28 special.type -> ..data/special.type
root@registry-qbr3x:/# cd /etc/config/
root@registry-qbr3x:/etc/config# ls
special.how  special.type
root@registry-qbr3x:/etc/config# cat special.how 
very
root@registry-qbr3x:/etc/config# cat special.type 
charm
```
主要注意的是，上面两个yaml文件是通过volume的方式把configMap挂载到容器里面。
后期如果用户把相应的configMap更新了，大概10s之后，对应容器里面的数据也会更新。
这就是configMap的热更新。

如果不使用volume的方式挂载configMap，而是使用ENV的方式来挂载configMap，是无法实现热更新的。
因为ENV 是在容器启动的时候注入的，启动之后 kubernetes 就不会再改变环境变量的值，且同一个namespace中的pod的环境变量是不断累加的。

2. Env方式注入configMap
```yaml
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
          image: etcdimage:0.3
          env:
            - name: SPECIAL_LEVEL_KEY
              valueFrom:
                configMapKeyRef:
                  name: special-config
                  key: special.how
            - name: SPECIAL_TYPE_KEY
              valueFrom:
                configMapKeyRef:
                  name: special-config
                  key: special.type
```

#### secret
secret用于传递敏感信息给pod中的容器使用，使用方式和ConfigMap是类似的。
Secret的数据在容器中是以文件的形式保存，容器通过读取文件可以获取所需的数据。

Secret的类型有三种：

  * Opaque： 自定义数据内容，默认类型是这个。Key/Value的形式，其中Value需要使用Base64加密。
  * ServiceAccount Token：ServiceAccount的认证内容
  * Dockercfg：和docker镜像仓库的认证相关

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mysecret
type: Opaque
data:
  username: YWRtaW4=
  password: MWYyZDFlMmU2N2Rm
```

Pod使用secret方式如下：
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mypod
spec:
  containers:
  - name: mypod
    image: redis
    volumeMounts:
    - name: foo
      mountPath: "/etc/foo"
      readOnly: true
  volumes:
  - name: foo
    secret:
      secretName: mysecret
```
更多关于secret的信息可以查询https://kubernetes.io/docs/concepts/configuration/secret/

#### persistentVolumeClaim
persistentVolumeClaim用于将PersistentVolume挂载到一个容器中。 
PersistentVolumes是在用户不知道特定云环境的细节的情况下声明持久存储的一种方式（例如GCE PersistentDisk或iSCSI卷）。
```yaml
kind: Pod
apiVersion: v1
metadata:
  name: mypod
spec:
  containers:
    - name: myfrontend
      image: dockerfile/nginx
      volumeMounts:
      - mountPath: "/var/www/html"
        name: mypd
  volumes:
    - name: mypd
      persistentVolumeClaim:
        claimName: myclaim
```

#### RBD
必须先安装好ceph集群，才能使用rbd卷。
Rbd卷与emptyDir的不同之处在于，随着pod的消亡，emptyDir会被删除，数据也就丢失了。
而Rbd卷是一个外部卷，可以通过删除策略的设置，把Rbd卷保留下来，即使pod被删除了。

RBD卷只能由单个消费者以读写模式挂载，不允许多个pod同时写入。
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: rbd
spec:
  containers:
    - image: kubernetes/pause
      name: rbd-rw
      volumeMounts:
      - name: rbdpd
        mountPath: /mnt/rbd
  volumes:
    - name: rbdpd
      rbd:
        monitors:
        - '10.16.154.78:6789'
        - '10.16.154.82:6789'
        - '10.16.154.83:6789'
        pool: kube
        image: foo
        fsType: ext4
        readOnly: true
        user: admin
        keyring: /etc/ceph/keyring
        imageformat: "2"
        imagefeatures: "layering"
```

#### CSI
Container Storage Interface，试图建立一个通用的storage标准接口，让k8s可以通过CSI使用符合标准的外部存储。
用户可以把CSI作为voluem的一种挂载到pod中进行使用，目前还处于alpha阶段，需要手工开启该特性。

## PersistentVolumes

### 简介
kubernetes存储子系统引入两个API来管理存储，分别是PersistentVolume 和PersistentVolumeClaim 。
PersistentVolume 和 PersistentVolumeClaim 提供了对存储支持的抽象，也提供了基础设施和应用之间的分界，管理员创建一系列的PV 提供存储，然后为应用提供PVC，应用程序仅需要加载一个PVC，就可以进行访问。

在kubernetes V1.5之后，提供了PV的动态生成。可以直接创建PVC，由系统根据StorageClass来动态生成PV。

### 和普通volume的区别
普通Volume和使用它的Pod之间是一种静态绑定关系，在定义Pod的文件里，同时定义了它使用的Volume。
Volume 是Pod的附属品，我们无法单独创建一个Volume，因为它不是一个独立的K8S资源对象。

Persistent Volume 简称PV是一个K8S资源对象，所以我们可以单独创建一个PV。
它不和Pod直接发生关系，而是通过Persistent Volume Claim，简称PVC来实现动态绑定。
Pod定义里指定的是PVC，然后PVC会根据Pod的要求去自动绑定合适的PV给Pod使用。

### 生命周期
PV是集群中的资源，PVC是对这些资源的请求。两者遵循以下的生命周期：

#### Provisioning
有两种方式来提供pv资源：statically 和dynamically。

  1. statically 
由k8s集群的系统管理员在事先创建好一定数量的PV资源，供上层用户消费使用。这些PV携带着真正可用的底层存储的细节信息。

  2. dynamically
和storageclass相关，由系统来动态完成PV的创建。在这种方式里面，PVC必须请求一个storageclass。

#### Binding
用户根据所需存储空间大小和访问模式创建（或在动态部署中已创建）一个 PersistentVolumeClaim。
Kubernetes的Master节点循环监控新产生的PVC，找到与之匹配的PV（如果有的话），并把他们绑定在一起。动态配置时，循环会一直将PV与这个PVC绑定，直到PV完全匹配PVC。
避免PVC请求和得到的PV不一致。绑定一旦形成，PersistentVolumeClaim绑定就是独有的，不管是使用何种模式绑定的。

如果找不到匹配的volume，用户请求会一直保持未绑定状态。
在匹配的volume可用之后，用户请求将会被绑定。
比如，一个配置了许多50Gi PV的集群不会匹配到一个要求100Gi的PVC。 只有在100Gi PV被加到集群之后，这个PVC才可以被绑定。

#### Using
Pod把一个Claim作为一个volume来使用。K8S会根据该cliam来找到对应绑定的volume，然后把该volume挂载到一个pod上。

#### Reclaiming
当用户删除一个pv绑定的pvc时，pv就会进行released状态，等待回收处理。处于Released状态的pv需要经过回收处理才能再次使用，回收策略包括：

    * Retain，等待人工回收处理
    * Recycle，由k8s自动进行清理，清理成功之后，该pv可以再次绑定使用。原来的数据是已经被删除了的。
    * Delete，直接删除。动态配置的pv继承其StorageClass的回收策略，默认为Delete。管理员应根据用户的期望配置StorageClass。

是否支持一个策略和具体的物理存储类型相关。

### pv支持的volume类型
PersistentVolume在k8s里面是作为一个plugin的形式实现的，目前k8s支持多种类型的plugin。我们现在主要使用的是：

    * RBD (Ceph Block Device)
    * CephFS
    * HostPath (Single node testing only – local storage is not supported in any way and WILL NOT WORK in a multi-node cluster)

### PV 
每一个pv都会包含一个spec和status，其中spec如下所示：
```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pv0003
spec:
  capacity:
    storage: 5Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Recycle
  storageClassName: slow
  mountOptions:
    - hard
    - nfsvers=4.1
  nfs:
    path: /tmp
    server: 172.17.0.2
```

#### volumeMode
在1.9之前，所有plugin的默认行为都是创建一个Filesystem。
在1.9中，用户可以通过指定volumeMode的值来使用原始的块设备。volumeMode 的值可以是“Filesystem” 或者“Block”，默认值是Filesystem。

#### Capacity
申请的容量大小

#### Access Modes

    * ReadWriteOnce – the volume can be mounted as read-write by a single node
    * ReadOnlyMany – the volume can be mounted read-only by many nodes
    * ReadWriteMany – the volume can be mounted as read-write by many nodes

#### Class
用于设置storageClassName

#### Reclaim Policy
三种回收策略在前面已经进行了介绍

#### Mount Options
支持额外设置挂载的参数。

#### Phase
一个pv的状态会是下面几种：

    * Available – a free resource that is not yet bound to a claim
    * Bound – the volume is bound to a claim
    * Released – the claim has been deleted, but the resource is not yet reclaimed by the cluster
    * Failed – the volume has failed its automatic reclamation

### PersistentVolumeClaims
每一个pvc都会包含一个spec和status，其中spec如下所示：
```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: myclaim
spec:
  accessModes:
    - ReadWriteOnce
  volumeMode: Filesystem
  resources:
    requests:
      storage: 8Gi
  storageClassName: slow
  selector:
    matchLabels:
      release: "stable"
    matchExpressions:
      - {key: environment, operator: In, values: [dev]}
```

#### Access Modes
和pv类似
#### Volume Modes
和pv类似
#### Resources
请求资源，就像pod请求cpu、memory一样
#### Selector
pvc中声明了label selector，只有符合该label的pv才会该pvc进行绑定。

    * matchLabels - the volume must have a label with this value
    * matchExpressions - a list of requirements made by specifying key, list of values, and operator that relates the key and values. Valid operators include In, NotIn, Exists, and DoesNotExist.

如果同时指定了matchLabels 和matchExpressions ，那么两个条件都必须要满足。

#### Class
pvc可以通过storageClassName属性来使用一个storageClassName。一个pv和pvc在申请同一个storageClass的情况下，才会被绑定。
pvc不是必须要申请storageClass，这种情况下，也只能和没申请storageClass的pv进行绑定。

#### Phase
一个pvc的状态会是：

    * pending – pvc创建成功之后进行等待状态，等待绑定pv。
    * Bound – 分配pv和pvc进行绑定，进行Bound状态。

### Claims As Volumes
pvc和pod必须处于同一个namespace
```yaml
kind: Pod
apiVersion: v1
metadata:
  name: mypod
spec:
  containers:
    - name: myfrontend
      image: dockerfile/nginx
      volumeMounts:
      - mountPath: "/var/www/html"
        name: mypd
  volumes:
    - name: mypd
      persistentVolumeClaim:
        claimName: myclaim
```

## StorageClass
StorageClass主要包含三部分，分别是provisioner、parameters和reclaimPolicy。
StorageClass的name属性非常重要，用户就是通过该name来申请使用StorageClass。

StorageClass一经创建之后，无法进行修改。
可以设置默认的StorageClass。
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: standard
provisioner: kubernetes.io/aws-ebs
parameters:
  type: gp2
reclaimPolicy: Retain
mountOptions:
  - debug
```

其中使用RBD的StorageClass例子如下所示：
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: fast
provisioner: kubernetes.io/rbd
parameters:
  monitors: 10.16.153.105:6789
  adminId: kube
  adminSecretName: ceph-secret
  adminSecretNamespace: kube-system
  pool: kube
  userId: kube
  userSecretName: ceph-secret-user
  fsType: ext4
  imageFormat: "2"
  imageFeatures: "layering"
```

## Dynamic Volume Provisioning
动态卷配置允许按需创建存储卷。
如果没有动态配置，群集管理员必须手动调用底层API来创建新的存储卷，然后创建PersistentVolume对象以在Kubernetes中表示它们。
动态配置功能避免了群集管理员预先配置存储，它会在用户请求时自动提供存储。

### Enabling Dynamic Provisioning
使用StorageClass有什么好处呢？除了volume由存储系统动态创建，节省了管理员的时间，还有一个好处是可以封装不同类型的存储供PVC选用。在StorageClass出现以前，PVC绑定一个PV只能根据两个条件，一个是存储的大小，另一个是访问模式。在StorageClass出现后，等于增加了一个绑定维度。

比如这里就有两个StorageClass，它们都是用GCE，但是一个使用的是普通磁盘，我们把这个StorageClass命名为slow。另一个使用的是SSD，我们把它命名为fast。
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: slow
provisioner: kubernetes.io/gce-pd
parameters:
  type: pd-standard

apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: fast
provisioner: kubernetes.io/gce-pd
parameters:
  type: pd-ssd
```

### Using Dynamic Provisioning
在PVC里除了常规的大小、访问模式的要求外，还通过annotation指定了Storage Class的名字为fast，这样这个PVC就会绑定一个SSD，而不会绑定一个普通的磁盘。（在1.9版本中是通过storageClassName属性来指定）
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: claim1
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: fast
  resources:
    requests:
      storage: 30Gi
```

上述文件会自动提供一个等效于 SSD 的持久盘，当这个 PVC 被删除，这个卷也随之销毁（默认回收策略是Delete）。

### Defaulting Behavior
所有的 PVC 都可以在不使用 StorageClass 注解的情况下，直接使用某个动态存储。
把一个StorageClass 对象标记为 “default” 就可以了。StorageClass 用注解`storageclass.beta.kubernetes.io/is-default-class`就可以成为缺省存储。

有了缺省的 StorageClass，用户创建 PVC 就不用 storage-class 的注解了，1.4 中新加入的DefaultStorageClass 准入控制器会自动把这个标注指向缺省存储类。

## 一个组合方案
从上面的几个概念，可以组合出多个pod使用volume的方案。下面介绍一种的方案：

1.	Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ceph-secret-user
  namespace: kube-system
type: kubernetes.io/rbd
data:
  key: {{ secret }}
```
其中{{secret}}的值来源于ceph集群为kubernetes用户创建的keyring文件，一般位置是/etc/ceph/ceph.client. kubernetes. Keyring，需要进行base64加密。

2.	StorageClass

StorageClass使用上面名为ceph-secret-user的secret来和底层ceph集群进行交互（创建删除rbd pool、image），yaml文件如下所示：

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
   name: slow
provisioner: kubernetes.io/rbd
parameters:
    monitors: 127.0.0.1:6789,127.0.0.2:6789, 127.0.0.3:6789
    adminId: admin
    adminSecretName: ceph-secret-admin
    adminSecretNamespace: "kube-system"
    pool: kube
    userId: kubernetes
    userSecretName: ceph-secret-user
```

其中几个属性介绍如下：

    * monitors：ceph monitor的IP:Port（也可以直接写IP即可，或域名），可以通过ceph -s查看。
    * adminId：ceph集群的管理员Id
    * adminSecretName：管理员Id用的 secret
    * adminSecretNamespace：管理员的secret所处的namespace
    * pool：给用户使用的rbd pool，这个pool需要管理员事先创建好。
    * userId：ceph集群给k8s集群创建了一个名为的kubernetes的用户
    * userSecretName：kubernetes用户的secret

如果给kubernetes也赋予了管理员权限，这上面两个Id直接都填kubernetes用户即可。
至此，用户只需要创建pvc即可，后续的pv和rbd Image会由系统来自动创建。

3.	PVC

创建一个名为registry的pvc

```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: registry
  namespace: kube-system
spec:
  accessModes:
    - ReadWriteOnce
  volumeMode: Filesystem
  resources:
    requests:
      storage: 8Gi
  storageClassName: slow
```

在V1.5.2版本中，是通过一个`volume.beta.kubernetes.io/storage-class 的annotations`来声明使用的storageClass的。
现在是升级为一个storageClassName的属性。

4.	RC
消费上面名为registry的pvc
```yaml
kind: ReplicationController
metadata:
  name: registry
  namespace: kube-system
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
          image: cargo:2.3.0
          resources:
            limits:
              cpu: 200m
              memory: 256Mi
            requests:
              cpu: 200m
              memory: 256Mi
          volumeMounts:
            - name: registry
              mountPath: /var/lib/registry
          livenessProbe:
            httpGet:
              path: /image/list
              port: 8080
              initialDelaySeconds: 60
              timeoutSeconds: 10
      volumes:
        - name: registry
          persistentVolumeClaim:
            claimName: regsitry
```

## 总结

两种存储卷：普通Volume 和Persistent Volume。

普通Volume在定义Pod的时候直接定义，Persistent Volume通过Persistent Volume Claim来动态绑定。
PV可以手动创建,也可以通过StorageClass来动态创建。

              
## 参考

https://kubernetes.io/docs/concepts/storage/volumes/

http://blog.csdn.net/liukuan73/article/details/60089305

http://blog.csdn.net/qq_34463875/article/details/71425619




