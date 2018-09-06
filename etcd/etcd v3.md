# etcd v3

https://coreos.com/blog/etcd3-a-new-etcd.html


1. 这是一个简易版的client工具
2. 利用v3的get命令实现ls的效果，手动更改endpoints和etcdctl的绝对路径即可
3. 如果没有证书，设置为""即可

```bash
#! /bin/bash
# Already test in etcd V3.0.4 and V3.1.7
# Deal with ls command

ENDPOINTS="http://192.168.56.101:2379"
ETCDCTL_ABSPATH="/home/expend-disk/etcd/bin/etcdctl"
CERT_ARGS=""

export ETCDCTL_API=3

if [ $1 == "ls" ]
then
    keys=$2
    if [ -z $keys ]
    then
        keys="/"
    fi
    if [ ${keys: -1} != "/" ]
    then
        keys=$keys"/"
    fi
    num=`echo $keys | grep -o "/" | wc -l`
    (( num=$num+1 ))
    #/home/expend-disk/etcd/bin/etcdctl --endpoints="$ENDPOINTS" get $keys --prefix=true --keys-only=true  | sed '/^\s*$/d'|sort
    $ETCDCTL_ABSPATH --endpoints="$ENDPOINTS" get $keys --prefix=true --keys-only=true $CERT_ARGS | cut -d '/' -f 1-$num | grep -v "^$" | grep -v "compact_rev_key" | uniq | sort
    exit 0
fi
# Deal with get command
if [ $1 == "get" ]
then
    $ETCDCTL_ABSPATH --endpoints="$ENDPOINTS" $* $CERT_ARGS
#--print-value-only=true
    exit 0
fi
# Deal with other command
$ETCDCTL_ABSPATH --endpoints="$ENDPOINTS" $* $CERT_ARGS
exit 0
```