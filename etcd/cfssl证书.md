# etcd使用cfssl证书

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [cfssl安装](#cfssl安装)
  - [生成ca证书](#生成ca证书)
  - [server证书](#server证书)
  - [client证书](#client证书)
  - [etcd使用cfssl](#etcd使用cfssl)
    - [客户端到服务器通信](#客户端到服务器通信)
    - [对等通信](#对等通信)
    - [两个例子](#两个例子)

<!-- END MUNGE: GENERATED_TOC -->


	etcd推荐使用cfssl工具来生成证书进行验证

## cfssl安装

以x86_64 Linux为例
```
mkdir ~/bin
curl -s -L -o ~/bin/cfssl https://pkg.cfssl.org/R1.2/cfssl_linux-amd64
curl -s -L -o ~/bin/cfssljson https://pkg.cfssl.org/R1.2/cfssljson_linux-amd64
chmod +x ~/bin/{cfssl,cfssljson}
export PATH=$PATH:~/bin
```
离线安装的情况，直接把两个文件下载下来重命名即可

## 生成ca证书

生成ca的配置文件
```
mkdir ~/cfssl
cd ~/cfssl
cfssl print-defaults config > ca-config.json
cfssl print-defaults csr > ca-csr.json
```
三大证书类型介绍：
```
client certificate： is used to authenticate client by server. For example etcdctl, etcd proxy, fleetctl or docker clients.
server certificate： is used by server and verified by client for server identity. For example docker server or kube-apiserver.
peer certificate： is used by etcd cluster members as they communicate with each other in both ways.
```
三大证书都由CA证书进行签发

其中ca-config.json中的expiry: 这个属性是指定证书的有效时间

然后执行命令生成CA证书
```
cfssl gencert -initca ca-csr.json | cfssljson -bare ca -
```
会得到ca-key.pem、ca.csr、ca.ppem。

其中 Please keep ca-key.pem file in safe. 
This key allows to create any kind of certificates within your CA.

csr证书在这里面用不到。至此，CA证书生成完毕，后面利用CA证书来生成server证书和client端的证书。

## server证书
生成server证书的配置文件
```
cfssl print-defaults csr > server.json
```
server.json的重要属性有：

```
...
    "CN": "coreos1",
    "hosts": [     //只能通过在hosts声明的方式来进行访问
        "172.17.0.2",
        "c2ccacf259ed",
        "coreos1.local",
        "coreos1"
    ],
...
```
以etcd为例子，后面可以通过
`curl -L --cacert /root/openssl/dd_ca.crt https://c2ccacf259ed:4001/version`进行访问

执行命令生成server端证书

```
cfssl gencert -ca=ca.pem -ca-key=ca-key.pem -config=ca-config.json -profile=server server.json | cfssljson -bare server
```
得到server-key.pem 、server.csr 、server.pem三个文件

## client证书

生成client配置文件
```
cfssl print-defaults csr > client.json
```
client证书可以忽略hosts属性的设置，设置CN即可

```
...
    "CN": "client",
    "hosts": [""],
...
```
执行
```
cfssl gencert -ca=ca.pem -ca-key=ca-key.pem -config=ca-config.json -profile=client client.json | cfssljson -bare client
```
得到client-key.pem、client.csr、client.pem

至此所有证书生成完毕

## etcd使用cfssl

etcd通过命令行标志或环境变量获取几个证书相关的配置选项：

### 客户端到服务器通信
```
--cert-file = <path>：用于与etcd的SSL/TLS连接的证书。设置此选项时，advertise-client-urls可以使用HTTPS模式。
--key-file = <path>：证书的密钥。必须未加密。
--client-cert-auth：设置此选项后，etcd将检查所有来自受信任CA签署的客户端证书的传入HTTPS请求，因此不提供有效客户端证书的请求将失败。
--trusted-ca-file = <path>：受信任的证书颁发机构。
--auto-tls：对客户端的TLS连接使用自动生成的自签名证书。
```

### 对等通信
对等通信--服务器到服务器/集群
对等选项的工作方式与客户端到服务器选项的工作方式相同：
```
--peer-cert-file = <path>：用于对等体之间SSL / TLS连接的证书。这将用于侦听对等体地址以及向其他对等体发送请求。
--peer-key-file = <path>：证书的密钥。必须未加密。
--peer-client-cert-auth：设置时，etcd将检查来自集群的所有传入对等请求，以获得由提供的CA签名的有效客户端证书。
--peer-trusted-ca-file = <path>：受信任的证书颁发机构。
--peer-auto-tls：对对等体之间的TLS连接使用自动生成的自签名证书。
```
如果提供客户端到服务器或对等体证书，则还必须设置密钥。所有这些配置选项也可通过环境变量ETCD_CA_FILE，ETCD_PEER_CA_FILE等获得。


### 两个例子
（1）带client证书

```
启动方式
etcd -name etcd -data-dir /var/lib/etcd  -advertise-client-urls=https://172.17.0.2:4001 -listen-client-urls=https://172.17.0.2:4001  -cert-file=/root/cfssl/server.pem -key-file=/root/cfssl/server-key.pem -client-cert-auth -trusted-ca-file=/root/cfssl/ca.pem &

访问方式
curl --cacert /root/cfssl/ca.pem --cert /root/cfssl/client.pem  --key /root/cfssl/client-key.pem https://c2ccacf259ed:4001/version
```

这里面c2ccacf259ed在前面的server.json文件hosts中进行了声明，同时需要在client端的／etc/hosts文件中声明c2ccacf259ed对应的ip地址。


（2）不带client证书

```
启动方式
etcd --name infra0 --data-dir infra0 \
  --cert-file=/path/to/server.crt --key-file=/path/to/server.key \
  --advertise-client-urls=https://127.0.0.1:2379 --listen-client-urls=https://127.0.0.1:2379

访问方式
 curl --cacert /path/to/ca.crt https://127.0.0.1:2379/v2/keys/foo -XPUT -d value=bar -v
```

## 参考

cfssl：

https://coreos.com/etcd/docs/latest/op-guide/security.html#example-3-transport-security--client-certificates-in-a-cluster

https://coreos.com/os/docs/latest/generate-self-signed-certificates.html

openssl：

http://www.cnblogs.com/breg/p/5923604.html

etcd的api：

https://coreos.com/etcd/docs/latest/v2/api.html

