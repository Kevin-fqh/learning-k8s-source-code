# k8s里面的kubeconfig

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [模板](#模板)
  - [clusters模块](#clusters模块)
  - [users模块](#users模块)
  - [contexts模块](#contexts模块)
  - [current-context](#current-context)
  - [加载和合并kubeconfig规则](#加载和合并kubeconfig规则)
  - [kubectl config命令](#kubectl-config命令)
  - [模板2](#模板2)

<!-- END MUNGE: GENERATED_TOC -->


在kubectl系列文章里面的对Factory进行介绍时，提到过kubeconfig文件。

`kubectl config view` 命令可以展示当前的 kubeconfig 设置。

## 模板
`vim /root/.kube/config`，默认路径

```
apiVersion: v1
kind: Config
preferences: {}
users:
- name: visitor
  user:
    client-certificate: /root/.kube/server.crt
    client-key: /root/.kube/server.key
clusters:
- name: kubernetes
  cluster:
    server: https://kubernetes:6443
    certificate-authority: /root/.kube/ca.crt
contexts:
- context:
    cluster: kubernetes
    user: visitor
  name: default-context
current-context: default-context
```
apiVersion 和 kind 标识客户端解析器的版本和模式

## clusters模块
cluster中包含kubernetes集群的端点数据，包括 kubernetes apiserver 的完整 url 以及集群的证书颁发机构。

可以使用 `kubectl config set-cluster` 添加或修改 cluster 条目。

## users模块
user定义用于向kubernetes集群进行身份验证的客户端凭据。
在加载/合并kubeconfig之后，user将有一个名称作为用户条目列表中的key。 

可用凭证有 `client-certificate、client-key、token 和 username/password`。 
`username/password` 和 `token` 是二者只能选择一个，但 `client-certificate` 和 `client-key` 可以分别与它们组合。

您可以使用 `kubectl config set-credentials` 添加或者修改 user 条目。

## contexts模块
context定义了一个命名的`cluster、user、namespace`元组，用于使用提供的认证信息和命名空间将请求发送到指定的集群。

三个都是可选的；
仅使用 cluster、user、namespace 之一指定上下文，或指定`none`。 

未指定的值或在加载的 kubeconfig 中没有相应条目的命名值将被替换为默认值。
加载和合并 kubeconfig 文件的规则很简单，但有很多，具体可以查看[加载和合并kubeconfig规则](#加载和合并kubeconfig规则)。

可以使用`kubectl config set-context`添加或修改上下文条目。

## current-context
current-context 是作为`cluster、user、namespace`元组的 ”key“，
当kubectl从该文件中加载配置的时候会被默认使用。

可以在kubectl命令行里覆盖这些值，通过分别传入`—context=CONTEXT、 —cluster=CLUSTER、--user=USER 和 --namespace=NAMESPACE`。

可以使用`kubectl config use-context`更改 current-context。

## 加载和合并kubeconfig规则
根据下面的规则来生成一个clientcmd.ClientConfig。规则呈现如下层次结构

一: 使用kubeconfig builder。这里的合并和覆盖次数有点多。

1. 合并kubeconfig本身。 这是通过以下层次结构规则完成的  
    (1)CommandLineLocation - 这是从命令行解析的，so it must be late bound。  
	   如果指定了这一点，则不会合并其他kubeconfig文件。 此文件必须存在。  
	(2)如果设置了$KUBECONFIG，那么它被视为应该被合并的文件之一。  
	(3)主目录位置 HomeDirectoryLocation ,即${HOME}/.kube/config==>/root/.kube/config
	
2. 根据此规则链中的第一个命中确定要使用的上下文---context  
    (1)命令行参数 - 再次从命令行解析，so it must be late bound  
	(2)CurrentContext from the merged kubeconfig file  
	(3)Empty is allowed at this stage
3. 确定要使用的群集信息和身份验证信息。---cluster info and auth info  
    在这里，我们可能有也可能没有上下文。  
	他们是建立在这个规则链中的第一个命中。（运行两次，一次为auth，一次为集群）  
	(1)命令行参数  
	(2)If context is present, then use the context value  
	(3)Empty is allowed
4. 确定要使用的实际群集信息。---actual cluster info  
    在这一点上，我们可能有也可能没有集群信息。基于下述规则链构建集群信息：  
	(1)命令行参数  
	(2)If cluster info is present and a value for the attribute is present, use it.  
	(3)If you don't have a server location, bail.
5. cluster info and auth info是使用同样的规则来进行创建的。  
    除非你在auth info仅仅使用了一种认证方式。  
	下述情况将会导致ERROR：  
	(1)如果从命令行指定了两个冲突的认证方式，则失败。  
	(2)如果命令行未指定，并且auth info具有冲突的认证方式，则失败。  
	(3)如果命令行指定一个，并且auth info指定另一个，则遵守命令行指定的认证方式。
	
二: 对于任何仍然缺少的信息，使用默认值，并可能提示验证信息

Kubeconfig 文件中的任何路径都相对于 kubeconfig 文件本身的位置进行解析。

## kubectl config命令
```shell
$ kubectl config set-credentials myself --username=admin --password=secret
$ kubectl config set-cluster local-server --server=http://localhost:8080
$ kubectl config set-context default-context --cluster=local-server --user=myself
$ kubectl config use-context default-context
$ kubectl config set contexts.default-context.namespace the-right-prefix
$ kubectl config view
```
产生对应的kubeconfig文件如下所示：
```
apiVersion: v1
clusters:
- cluster:
    server: http://localhost:8080
  name: local-server
contexts:
- context:
    cluster: local-server
    namespace: the-right-prefix
    user: myself
  name: default-context
current-context: default-context
kind: Config
preferences: {}
users:
- name: myself
  user:
    password: secret
    username: admin
```

## 模版2
最后，来看一个官方的详细一点模版
```
current-context: federal-context
apiVersion: v1
clusters:
- cluster:
    api-version: v1
    server: http://cow.org:8080
  name: cow-cluster
- cluster:
    certificate-authority: path/to/my/cafile
    server: https://horse.org:4443
  name: horse-cluster
- cluster:
    insecure-skip-tls-verify: true
    server: https://pig.org:443
  name: pig-cluster
contexts:
- context:
    cluster: horse-cluster
    namespace: chisel-ns
    user: green-user
  name: federal-context
- context:
    cluster: pig-cluster
    namespace: saw-ns
    user: black-user
  name: queen-anne-context
kind: Config
preferences:
  colors: true
users:
- name: blue-user
  user:
    token: blue-token
- name: green-user
  user:
    client-certificate: path/to/my/client/cert
    client-key: path/to/my/client/key
```
