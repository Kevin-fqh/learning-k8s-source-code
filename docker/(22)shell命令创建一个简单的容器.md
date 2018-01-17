# shell命令创建一个简单的容器

## 准备rootfs
有两个方法来准备rootfs，一个是利用docker命令，另外一个是自己下载安装一个文件系统编译安装。以busybox为例，我们用docker export提供的rootfs作为试验对象

## 验证rootfs的有效性
```shell
root@fqhnode:~/fqhcontainer# ll
drwxr-xr-x 13 root root 4096 Jan  9 11:39 buxybox/

root@fqhnode:~/fqhcontainer# chroot buxybox/ sh
/ # which date
/bin/date
/ # date
Wed Jan 10 08:31:18 UTC 2018
/ # which sh
/bin/sh
```
此时可以另起一个终端，用`pstree`命令来查看`sh进程`的根路径，对比在内部和外部看到的区别。

## 创建容器
1. 目录`outdir`用于容器内部和host主机的数据共享
2. pivot_root命令要求原来根目录的挂载点为private
3. --propagation private 是为了容器内部的挂载点都是私有，private 表示既不继承主挂载点中挂载和卸载操作，自身的挂载和卸载操作也不会反向传播到主挂载点中。
4. 查看`unshare`进程，可以发现其6个namesoace都已经和其父进程不一样了。所以下面再执行的命令其实已经处于另外一个namespace了
5. 如果在另外一个终端上查看hostname，会发现还是`fqhnode`，而不是刚刚设置的`aaa`
6. exec bash ,通过exec系统调用，执行容器内部的bash命令
7. `mount --bind ./buxybox/ ./buxybox/`的作用是在原地创建一个新的挂载点，这是pivot_root的要求
8. 把outdir和interdir挂载起来，实现容器内外的通信
9. mount -t proc none /proc之后需要umout掉老挂载点的信息
```shell
root@fqhnode:~/fqhcontainer# mkdir outdir
root@fqhnode:~/fqhcontainer# unshare --user --mount --ipc --pid --net --uts -r --fork --propagation private bash

root@fqhnode:~/fqhcontainer# hostname aaa
root@fqhnode:~/fqhcontainer# hostname
aaa

root@fqhnode:~/fqhcontainer# exec bash
root@aaa:~/fqhcontainer#

root@aaa:~/fqhcontainer# mkdir -p buxybox/old_root buxybox/interdir
root@aaa:~/fqhcontainer# mount --bind ./buxybox/ ./buxybox/

root@aaa:~/fqhcontainer# mount --bind outdir/ ./buxybox/interdir/

root@aaa:~/fqhcontainer/buxybox# pivot_root ./ ./old_root/
root@aaa:~/fqhcontainer/buxybox# exec sh
/ # export PS1='root@$(hostname):$(pwd)# '
root@aaa:/# mount
mount: no /proc/mounts

root@aaa:/# mount -t proc none /proc
root@aaa:/# mount
/dev/sda1 on /old_root type ext4 (rw,relatime,errors=remount-ro,data=ordered)
udev on /old_root/dev type devtmpfs (rw,nosuid,relatime,size=487892k,nr_inodes=121973,mode=755)
devpts on /old_root/dev/pts type devpts (rw,nosuid,noexec,relatime,gid=5,mode=620,ptmxmode=000)
tmpfs on /old_root/dev/shm type tmpfs (rw,nosuid,nodev)
mqueue on /old_root/dev/mqueue type mqueue (rw,relatime)
hugetlbfs on /old_root/dev/hugepages type hugetlbfs (rw,relatime)
...
...
none on /proc type proc (rw,nodev,relatime)

root@aaa:/# umount -l /old_root
root@aaa:/# mount
/dev/sda1 on / type ext4 (rw,relatime,errors=remount-ro,data=ordered)
/dev/sda1 on /interdir type ext4 (rw,relatime,errors=remount-ro,data=ordered)
none on /proc type proc (rw,nodev,relatime)

root@aaa:/# ps -ef
PID   USER     TIME   COMMAND
    1 root       0:00 sh
   65 root       0:00 ps -ef

root@aaa:/# cd interdir/
root@aaa:/interdir# ls
root@aaa:/interdir# 
root@aaa:/interdir# touch inter.txt
```
此时可以在host主机外部的outdir看到对应的文件

## chroot和pivot_root的区别
chroot - run command or interactive shell with special root directory

pivot_root - change the root filesystem

看起来挺迷糊的。。。从效果上看，pivot_root会修改一个进程的`根目录、工作目录`；而chroot只会修改进程的工作目录，不会修改其根目录

### chroot
在一个终端运行`chroot`命令，查看该进程的根目录，可以发现是会输出host主机上的路径的`/root/fqhcontainer/buxybox`
```shell
root@fqhnode:~/fqhcontainer# chroot buxybox/ sh
/ #
```
在另一个终端进行查看
```shell
root@fqhnode:~# pstree -l -p
root@fqhnode:~# readlink /proc/16154/exe
/root/fqhcontainer/buxybox/bin/sh
```

## Namespace的系统调用
clone： 创建一个新的进程并把他放到新的namespace中

sents： 将当前进程加入到已有的namespace中

unshare: 使当前进程退出指定类型的namespace，并加入到新创建的namespace（相当于创建并加入新的namespace）

## 参考
[Cgroups, namespaces, and beyond: what are containers made from?](https://www.youtube.com/watch?v=sK5i-N34im8)

[https://segmentfault.com/a/1190000006913509](https://segmentfault.com/a/1190000006913509)

[Linux Namespace分析——mnt namespace的实现与应用](http://hustcat.github.io/namespace-implement-1/)

[mount详解](http://www.jinbuguo.com/man/mount.html)


