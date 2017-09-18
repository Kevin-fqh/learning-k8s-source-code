# Docker搭建ceph环境

## 基础环境配置
```
# ifconfig

enp0s8: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 192.168.56.101  netmask 255.255.255.0  broadcast 192.168.56.255
        inet6 fe80::2407:e89c:1adc:1272  prefixlen 64  scopeid 0x20<link>
        ether 08:00:27:4f:f7:1d  txqueuelen 1000  (Ethernet)
        RX packets 6053  bytes 482631 (471.3 KiB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 5583  bytes 5674853 (5.4 MiB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0
```
下载镜像
```
docker pull ceph/daemon:tag-build-master-jewel-ubuntu-16.04
yum install ceph-common
```

## 运行monitor
```
docker run -d --privileged --net=host -v /etc/ceph:/etc/ceph -v /var/lib/ceph/:/var/lib/ceph/ -e MON_IP=192.168.56.101 -e CEPH_PUBLIC_NETWORK=192.168.56.0/24 ceph/daemon:tag-build-master-jewel-ubuntu-16.04 mon
```

## 修改/etc/ceph/ceph.conf
新增最后两个属性
```
[global]
fsid = 7d885ac4-b748-40d0-992b-394b9f0322b8
mon initial members = fqhnode01
mon host = 192.168.56.101
auth cluster required = cephx
auth service required = cephx
auth client required = cephx
public network = 192.168.56.0/24
cluster network = 192.168.56.0/24
osd journal size = 100
osd max object name len = 256
osd max object namespace len = 64
```
## 新建OSD目录
```
cd /var/lib/osd
[root@fqhnode01 osd]# mkdir ceph-0
[root@fqhnode01 osd]# mkdir ceph-1
[root@fqhnode01 osd]# mkdir ceph-2
```
执行三次docker exec <mon-container-id> ceph osd create
```
[root@fqhnode01 osd]# docker exec 4489d6bc0cc7 ceph osd create
0
[root@fqhnode01 osd]# docker exec 4489d6bc0cc7 ceph osd create
1
[root@fqhnode01 osd]# docker exec 4489d6bc0cc7 ceph osd create
2
```
## 起osd
```
docker run -d --privileged --net=host -v /etc/ceph:/etc/ceph -v /var/lib/ceph/:/var/lib/ceph/ -e MON_IP=192.168.56.101 -e CEPH_PUBLIC_NETWORK=192.168.56.0/24 ceph/daemon:tag-build-master-jewel-ubuntu-16.04 osd_directory
```
```
[root@fqhnode01 osd]# ceph -s
    cluster 7d885ac4-b748-40d0-992b-394b9f0322b8
     health HEALTH_ERR
            64 pgs are stuck inactive for more than 300 seconds
            64 pgs stuck inactive
            64 pgs stuck unclean
            too few PGs per OSD (21 < min 30)
     monmap e1: 1 mons at {fqhnode01=192.168.56.101:6789/0}
            election epoch 3, quorum 0 fqhnode01
     osdmap e8: 3 osds: 3 up, 3 in
            flags sortbitwise,require_jewel_osds
      pgmap v9: 64 pgs, 1 pools, 0 bytes data, 0 objects
            0 kB used, 0 kB / 0 kB avail
                  64 creating
```
## 消除错误
由于默认只有一个rbd pool，所以我们需要调整rbd pool的pg数。由于我们有3个OSD，而副本数目前为3，所以3*100/3=100，取2的次方为128。
```
[root@fqhnode01 osd]# ceph osd pool set rbd pg_num 128
set pool 0 pg_num to 128
[root@fqhnode01 osd]# ceph osd pool set rbd pgp_num 128
set pool 0 pgp_num to 128
[root@fqhnode01 osd]# ceph -s
    cluster 7d885ac4-b748-40d0-992b-394b9f0322b8
     health HEALTH_ERR
            128 pgs are stuck inactive for more than 300 seconds
            128 pgs degraded
            128 pgs stuck inactive
            128 pgs stuck unclean
            128 pgs undersized
     monmap e1: 1 mons at {fqhnode01=192.168.56.101:6789/0}
            election epoch 3, quorum 0 fqhnode01
     osdmap e14: 3 osds: 3 up, 3 in
            flags sortbitwise,require_jewel_osds
      pgmap v23: 128 pgs, 1 pools, 0 bytes data, 0 objects
            30439 MB used, 18670 MB / 49110 MB avail
                 128 undersized+degraded+peered
```
系统默认按主机来对副本进行存储的，而我们的系统为单机环境，所以把rbd pool的副本数调整为1。
```
ceph osd pool set rbd size 1
set pool 0 size to 1
[root@fqhnode01 osd]# ceph -s
    cluster 7d885ac4-b748-40d0-992b-394b9f0322b8
     health HEALTH_OK
     monmap e1: 1 mons at {fqhnode01=192.168.56.101:6789/0}
            election epoch 3, quorum 0 fqhnode01
     osdmap e16: 3 osds: 3 up, 3 in
            flags sortbitwise,require_jewel_osds
      pgmap v28: 128 pgs, 1 pools, 0 bytes data, 0 objects
            30440 MB used, 18669 MB / 49110 MB avail
                 128 active+clean

[root@fqhnode01 osd]# rados lspools
rbd
```