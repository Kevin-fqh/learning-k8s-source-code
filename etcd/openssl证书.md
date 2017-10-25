# K8S添加openssl证书并授权多使用者

## 生成证书文件

### 配置ssl证书"使用者备用名称（DNS)"
修改openssl配置文件
```
vim /etc/pki/tls/openssl.cfg
```
确保`[ req ]`下存在以下两行（第一行是默认有的，第二行被注释掉了）
```
[ req ]
distinguished_name = req_distinguished_name
req_extensions = v3_req
```
确保`[ req_distinguished_name ]`下没有0.xxx的标，有的话把0.去掉
```
[ req_distinguished_name ]
countryName = Country Name (2 letter code)
countryName_default = XX
countryName_min = 2
countryName_max = 2
stateOrProvinceName = State or Province Name (full name)
localityName = Locality Name (eg, city)
localityName_default = Default City
organizationName = Organization Name (eg, company)
organizationName_default = Default Company Ltd
organizationalUnitName = Organizational Unit Name (eg, section)
commonName = Common Name (eg, your name or your server\'s hostname)
commonName_max  64
emailAddress = Email Address
emailAddress_max = 64
```
新增`[ v3_req ]`最后一行内容
```
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names
```
新增`[ alt_names ]`内容，其中DNS的名称就是多使用者的名字
```
[ alt_names]
DNS.1 = kubernetes
DNS.2 = master1
DNS.3 = master2
DNS.4 = master3
IP.1 = 10.100.0.1
```
### 根据openssl.cfg生成ssl证书
openssl.cfg要求部分文件及目录存在，在生成证书目录执行以下操作
```
mkdir -p CA/{certs,cri,newcerts,private}
touch CA/index.txt
echo 00 > CA/serial
```
如果最后一步提示找不到目录，改用
```
mkdir -p ./{certs,cri,newcerts,private}
touch ./index.txt
echo 00 > ./serial
```

然后开始进行ca证书签名以及签署操作
```
openssl req -new -x509 -days 3650 -keyout ca.key -out ca.crt -config ./openssl.cnf
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr -config ./openssl.cnf
openssl ca -in server.csr -out server.crt -cert ca.crt -keyfile ca.key -passin pass:secret -batch -extensions v3_req -config ./openssl.cnf
```
注意第三条命令执行后，Common Name要写主域名（注意：这个域名也要在openssl.cnf的DNS.x里）

## K8S添加证书认证
采取kubeconfig模式添加证书  
Kubernetes各个组件启用https添加证书参数如下
```
kube-apiserver --logtostderr=false --v=0 --etcd-servers=http://192.169.39.209:4001 --insecure-bind-address=0.0.0.0 --allow-privileged=false --service-cluster-ip-range=10.100.0.0/16 --admission-control=AlwaysAdmit,NamespaceLifecycle,NamespaceExists,LimitRanger,ResourceQuota --token_auth_file=/home/container/keystone/token --audit-log-path=/var/log/kubernetes/audit --log-dir=/var/log/kubernetes/kube-apiserver --experimental-keystone-url=http://192.169.39.211:35357 --experimental-keystone-auth-file=/etc/kubernetes/security/keystone_auth_file --authorization-mode=ABAC --authorization-policy-file=/etc/kubernetes/security/policy_file.json --client-ca-file=/root/.kube/keys/ca.crt --tls-private-key-file=/root/.kube/keys/server.key --tls-cert-file=/root/.kube/keys/server.crt

kube-controller-manager --logtostderr=false --v=0 --log-dir=/var/log/kubernetes/kube-controller-manager --kubeconfig=/root/.kube/config

kube-scheduler --logtostderr=false --v=0 --log-dir=/var/log/kubernetes/kube-scheduler --kubeconfig=/root/.kube/config

kube-proxy --logtostderr=false --v=0 --log-dir=/var/log/kubernetes/kube-proxy  --kubeconfig=/root/.kube/config

kubelet --logtostderr=false --v=0 --address=0.0.0.0 --hostname-override=192.169.39.209 --allow-privileged=false --cpu-cfs-quota=true --kubeconfig=/root/.kube/config --require-kubeconfig=True --pod-infra-container-image=pause:0.8.0 --log-dir=/var/log/kubernetes/kubelet
```
位于/root/.kube/目录下的config文件，是Kubernetes的默认配置文件本地kubectl默认也会根据该文件执行操作，该文件一般内容如下
```
apiVersion: v1
kind: Config
users:
- name: visitor
  user:
    client-certificate: /root/.kube/keys/server.crt
    client-key: /root/.kube/keys/server.key
clusters:
- name: kubernetes
  cluster:
    certificate-authority: /root/.kube/keys/ca.crt
    server: https://kubernetes:6444
contexts:
- context:
    cluster: kubernetes
    user: visitor
  name: kubernetes-context
current-context: kubernetes-context
```
其中server字段填写的是api-server的ip地址和端口。
其中/root/.kube/下需要手打创建keys文件夹，下面存放openssl生成的证书文件。
