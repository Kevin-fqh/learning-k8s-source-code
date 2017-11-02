# Cgroups用法

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [创建并挂载一个hierarchy](#创建并挂载一个hierarchy)
  - [在新建的hierarchy上的cgroup的根节点中扩展出两个子cgroup](#在新建的hierarchy上的cgroup的根节点中扩展出两个子cgroup)
  - [通过subsystem来限制cgroup中进程的资源](#通过subsystem来限制cgroup中进程的资源)
  - [Docker中的Cgroups](#docker中的cgroups)

<!-- END MUNGE: GENERATED_TOC -->

环境说明：
```shell
[root@fqhnode01 ~]# uname -a
Linux fqhnode01 3.10.0-514.6.1.el7.x86_64 #1 SMP Wed Jan 18 13:06:36 UTC 2017 x86_64 x86_64 x86_64 GNU/Linux
[root@fqhnode01 ~]# docker version
Client:
 Version:      1.13.1
```

## 创建并挂载一个hierarchy
```shell
[root@fqhnode01 home]# mkdir cgroups-test
[root@fqhnode01 home]# mount -t cgroup -o none,name=cgroups-test cgroups-test ./cgroups-test
[root@fqhnode01 home]# ll cgroups-test/
总用量 0
-rw-r--r--. 1 root root 0 11月  1 12:31 cgroup.clone_children
--w--w--w-. 1 root root 0 11月  1 12:31 cgroup.event_control
-rw-r--r--. 1 root root 0 11月  1 12:31 cgroup.procs
-r--r--r--. 1 root root 0 11月  1 12:31 cgroup.sane_behavior
-rw-r--r--. 1 root root 0 11月  1 12:31 notify_on_release
-rw-r--r--. 1 root root 0 11月  1 12:31 release_agent
-rw-r--r--. 1 root root 0 11月  1 12:31 tasks
```

## 在新建的hierarchy上的cgroup的根节点中扩展出两个子cgroup
```shell
[root@fqhnode01 cgroups-test]# cd cgroups-test/
[root@fqhnode01 cgroups-test]# mkdir cgroup-1 cgroup-2
[root@fqhnode01 cgroups-test]# ls
cgroup-1  cgroup.clone_children  cgroup.procs          notify_on_release  tasks
cgroup-2  cgroup.event_control   cgroup.sane_behavior  release_agent
[root@fqhnode01 cgroups-test]# 
[root@fqhnode01 cgroups-test]# tree
.
├── cgroup-1
│   ├── cgroup.clone_children
│   ├── cgroup.event_control
│   ├── cgroup.procs
│   ├── notify_on_release
│   └── tasks
├── cgroup-2
│   ├── cgroup.clone_children
│   ├── cgroup.event_control
│   ├── cgroup.procs
│   ├── notify_on_release
│   └── tasks
├── cgroup.clone_children
├── cgroup.event_control
├── cgroup.procs
├── cgroup.sane_behavior
├── notify_on_release
├── release_agent
└── tasks

2 directories, 17 files
```
可以看出，在一个cgroup的目录下创建文件夹时，系统Kernel会自动把该文件夹标记为这个cgroup的子cgroup，它们会继承父cgroup的属性。

几个文件功能说明：
- tasks，标识该cgroup下面的进程ID。如果把一个进程ID写入了tasks文件，就是把该进程加入了这个cgroup中。
- cgroup.procs 是树中当前cgroup中的进程组ID。如果是根节点，会包含所有的进程组ID。
- cgroup.clone_children，默认值为0。如果设置为1，子cgroup会继承父cgroup的cpuset配置。

## 往一个cgroup中添加和移动进程
1. 首先，查看一个进程目前所处的cgroup
```shell
[root@fqhnode01 cgroup-1]# echo $$
1019
[root@fqhnode01 cgroup-1]# cat /proc/1019/cgroup 
12:name=cgroups-test:/
11:devices:/
10:memory:/
9:hugetlb:/
8:cpuset:/
7:blkio:/
6:cpuacct,cpu:/
5:freezer:/
4:net_prio,net_cls:/
3:perf_event:/
2:pids:/
1:name=systemd:/user.slice/user-0.slice/session-1.scope
```
从`name=cgroups-test:/`可以看到当前进程（$$）位于hierarchy cgroups-test:/的根节点上。

2. 把进程移动到节点cgroup-1/中
```shell
[root@fqhnode01 cgroups-test]# cd cgroup-1/
[root@fqhnode01 cgroup-1]# cat tasks 
[root@fqhnode01 cgroup-1]# 
[root@fqhnode01 cgroup-1]# cat cgroup.procs 
[root@fqhnode01 cgroup-1]#
```
可以看到目前节点cgroup-1的tasks文件是空的。
```shell
[root@fqhnode01 cgroup-1]# echo $$ >> tasks
[root@fqhnode01 cgroup-1]# cat /proc/1019/cgroup 
12:name=cgroups-test:/cgroup-1
11:devices:/
10:memory:/
9:hugetlb:/
8:cpuset:/
7:blkio:/
6:cpuacct,cpu:/
5:freezer:/
4:net_prio,net_cls:/
3:perf_event:/
2:pids:/
1:name=systemd:/user.slice/user-0.slice/session-1.scope

[root@fqhnode01 cgroup-1]# cat cgroup.procs 
1019
1436
[root@fqhnode01 cgroup-1]# cat tasks 
1019
1437
```
可以看到，当前进程1019已经加入到cgroups-test:/cgroup-1中了。

需要注意的是，到目前为止，上面创建的hierarchy并没有关联到任何的subsystem，所以还是没有办法通过上面的cgroup节点来限制一个进程的资源占用。

## 通过subsystem来限制cgroup中进程的资源
系统已经默认为每一个subsystem创建了一个hierarchy，以memory的hierarchy为例子
```shell
# mount |grep memory
cgroup on /sys/fs/cgroup/memory type cgroup (rw,nosuid,nodev,noexec,relatime,memory)
```
可以看到memory subsystem的hierarchy的目录是`/sys/fs/cgroup/memory`。 
这个目录就跟上面我们创建的目录类似，只不过它是系统默认创建的，已经和memory subsystem关联起来，可以限制其名下进程占用的内存。

1. 安装stress工具
```shell
yum install stress.x86_64
```

2. 不使用Cgroups限制，启动一个占用200M内存的stress进程
```shell
[root@fqhnode01 memory]# stress --vm-bytes 200m --vm-keep -m 1
```
测试结果如下，本机内存为2G，所以2G＊10%＝200M：
```shell
# top
 PID USER      PR  NI    VIRT    RES    SHR S %CPU %MEM     TIME+ COMMAND                                                     
 1851 root      20   0  212060 204796    132 R 96.3 10.0   0:18.27 stress  
```

3. 使用Cgroups进行限制，仅允许使用100M内存  

首先参照前面步骤，在memory 的根节点下新建一个cgroup节点test-limit-memory
```shell
[root@fqhnode01 memory]# cd /sys/fs/cgroup/memory
[root@fqhnode01 memory]# mkdir test-limit-memory
```
然后，设置该cgroup节点的最大内存占用为100M
```shell
[root@fqhnode01 memory]# cd test-limit-memory/
[root@fqhnode01 test-limit-memory]# cat memory.limit_in_bytes 
9223372036854771712
[root@fqhnode01 test-limit-memory]# echo "100m" >> ./memory.limit_in_bytes 
[root@fqhnode01 test-limit-memory]# 
[root@fqhnode01 test-limit-memory]# cat memory.limit_in_bytes 
104857600
```
然后，把当前进程移动到这个cgroup节点test-limit-memory中 
```shell
[root@fqhnode01 test-limit-memory]# cat tasks 
[root@fqhnode01 test-limit-memory]# 
[root@fqhnode01 test-limit-memory]# echo $$ >> tasks 
[root@fqhnode01 test-limit-memory]# cat tasks 
1019
1878
```
最后，再次运行`stress --vm-bytes 200m --vm-keep -m 1`，测试结果如下：
```shell
 PID USER      PR  NI    VIRT    RES    SHR S %CPU %MEM     TIME+ COMMAND                                                     
 1881 root      20   0  212060  87924    132 R 37.0  4.3   0:12.05 stress  
```
观察显示内存占用最大值只能达到5%，也就是100m。 成功地使用Cgroups把stree进程的内存占用限制到了100m的范围之内。

最后说一下上面创建的`/sys/fs/cgroup/memory/test-limit-memory`文件会在重启之后被系统自动删除。
如果要删除一个cgroup，用umount命令。

## Docker中的Cgroups
```shell
[root@fqhnode01 memory]# docker run -itd  -e is_leader=true -e node_name=aaa -m 128m 98c2ef0aa9bb
e19acd747074941e9062f2beb5163927b097296f31b7a0317ecb93387cc16466
```
docker 会自动在memory hierarchy的目录下新建一个cgroup节点
```shell
[root@fqhnode01 e19acd747074941e9062f2beb5163927b097296f31b7a0317ecb93387cc16466]# pwd
/sys/fs/cgroup/memory/docker/e19acd747074941e9062f2beb5163927b097296f31b7a0317ecb93387cc16466
[root@fqhnode01 e19acd747074941e9062f2beb5163927b097296f31b7a0317ecb93387cc16466]# cat memory.limit_in_bytes 
134217728
[root@fqhnode01 e19acd747074941e9062f2beb5163927b097296f31b7a0317ecb93387cc16466]# cat memory.usage_in_bytes 
66355200
```

- memory.limit_in_bytes 内存限制
- memory.usage_in_bytes 该cgroup中的进程已经使用的memory



