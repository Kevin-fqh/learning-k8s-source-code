# cp、mv、rm、rename的底层实现

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [环境](#环境)
  - [touch](#touch)
  - [cp](#cp)
  - [rm](#rm)
  - [rename](#rename)
  - [mv](#mv)
  - [删除正在使用的文件](#删除正在使用的文件)
    - [删除正在被读写的文件](#删除正在被读写的文件)
    - [删除正在运行的可执行文件](#删除正在运行的可执行文件)
    - [删除正在使用的动态链接库](#删除正在使用的动态链接库)
  - [总结](#总结)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

研究来研究去，才发现原来最基础的才是最牛逼的。。
记得看过一句话，喜欢看，看得懂教科书的人才是真正的大神。因为教科书是各路大神千锤百炼出来最朴素(最孤躁)的语言，直指真谛。 
而所谓的参考书，技巧细节介绍得多，看起来入门的难度就要低一个级别了。
当然，这并不是说参考书不好，而是有点`大道至简，殊途同归`的意思。

## 环境
每个系统实现的可能有点不一样，但原理是类似的。
```
[root@fqhnode01 go-program-fqh-test]# uname -a
Linux fqhnode01 3.10.0-514.6.1.el7.x86_64 #1 SMP Wed Jan 18 13:06:36 UTC 2017 x86_64 x86_64 x86_64 GNU/Linux

[root@fqhnode01 go-program-fqh-test]# cat /etc/redhat-release 
CentOS Linux release 7.3.1611 (Core)
```

我们首先来进行静态的实验，即对一个还没被任何进程使用的文件进行操作。

## touch
首先我们新建一个文件，看看touch的底层的系统调用是什么？
```shell
[root@fqhnode01 go-program-fqh-test]# strace touch file_1 2>&1|egrep 'file_1'
execve("/usr/bin/touch", ["touch", "file_1"], [/* 26 vars */]) = 0
open("file_1", O_WRONLY|O_CREAT|O_NOCTTY|O_NONBLOCK, 0666) = 3

[root@fqhnode01 go-program-fqh-test]# stat file_1 
  文件："file_1"
  大小：0         	块：0          IO 块：4096   普通空文件
设备：801h/2049d	Inode：37224652    硬链接：1
权限：(0644/-rw-r--r--)  Uid：(    0/    root)   Gid：(    0/    root)
环境：unconfined_u:object_r:admin_home_t:s0
最近访问：2018-02-08 06:57:02.445519592 -0500
最近更改：2018-02-08 06:57:02.445519592 -0500
最近改动：2018-02-08 06:57:02.445519592 -0500
创建时间：-
```

## cp
当file_2不存在时，执行cp file_1 file_2，可以发现file_2和file_1的inode不一样，也就是用open()新建一个文件file_2，然后读取file_1的数据再写入file_2。
```shell
[root@fqhnode01 go-program-fqh-test]# strace cp file_1 file_2 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/cp", ["cp", "file_1", "file_2"], [/* 26 vars */]) = 0
stat("file_2", 0x7fff0ef3cdc0)          = -1 ENOENT (No such file or directory)
stat("file_1", {st_mode=S_IFREG|0644, st_size=0, ...}) = 0
stat("file_2", 0x7fff0ef3cb20)          = -1 ENOENT (No such file or directory)
open("file_1", O_RDONLY)                = 3
open("file_2", O_WRONLY|O_CREAT|O_EXCL, 0644) = 4

[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 0
37224652 -rw-r--r--. 1 root root 0 2月   8 06:57 file_1
33712192 -rw-r--r--. 1 root root 0 2月   8 07:52 file_2
[root@fqhnode01 go-program-fqh-test]# md5sum file_1 file_2
d41d8cd98f00b204e9800998ecf8427e  file_1
d41d8cd98f00b204e9800998ecf8427e  file_2
```

现在file_2已经存在了，再执行一次`cp file_1 file_2`。 
可以发现file_2的inode信息是没有发生变化的。 
也就是说cp命令，在目标文件file_2已经存在的情况下，实际上是清空了目标文件file_2的内容，之后把新的内容写入目标文件file_2。
```
[root@fqhnode01 go-program-fqh-test]# strace cp file_1 file_2 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/cp", ["cp", "file_1", "file_2"], [/* 26 vars */]) = 0
stat("file_2", {st_mode=S_IFREG|0644, st_size=0, ...}) = 0
stat("file_1", {st_mode=S_IFREG|0644, st_size=0, ...}) = 0
stat("file_2", {st_mode=S_IFREG|0644, st_size=0, ...}) = 0
open("file_1", O_RDONLY)                = 3
open("file_2", O_WRONLY|O_TRUNC)        = 4

[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 0
37224652 -rw-r--r--. 1 root root 0 2月   8 06:57 file_1
33712192 -rw-r--r--. 1 root root 0 2月   8 07:54 file_2
[root@fqhnode01 go-program-fqh-test]# md5sum file_1 file_2
d41d8cd98f00b204e9800998ecf8427e  file_1
d41d8cd98f00b204e9800998ecf8427e  file_2
```

## rm
在Linux中，要真正删除一个文件，需要满足两个条件：
  - 链接数为0
  - 没有进程打开该文件

系统调用unlink()是移除目标文件的一个链接。可以发现`rm`底层调用的其实就是`unlink()`

```shell
[root@fqhnode01 go-program-fqh-test]# strace rm file 2>&1|egrep 'file'
execve("/usr/bin/rm", ["rm", "file"], [/* 26 vars */]) = 0
access("/etc/ld.so.preload", R_OK)      = -1 ENOENT (No such file or directory)
newfstatat(AT_FDCWD, "file", {st_mode=S_IFREG|0644, st_size=18, ...}, AT_SYMLINK_NOFOLLOW) = 0
unlinkat(AT_FDCWD, "file", 0)           = 0

[root@fqhnode01 go-program-fqh-test]# strace unlink file 2>&1|egrep 'file'
execve("/usr/bin/unlink", ["unlink", "file"], [/* 26 vars */]) = 0
access("/etc/ld.so.preload", R_OK)      = -1 ENOENT (No such file or directory)
unlink("file")                          = 0
```

一个进程用open()或create()创建一个文件，然后立刻调用unlink()。因为文件仍旧是打开的，其内容`不会被删除`。只有当进程关闭该文件或终止时(这种情况下，内核关闭该进程所打开的全部文件)，该文件的内容才会被删除。这种特性经常被用来确保即使是程序崩溃时，它所创建的临时文件也不会遗留下来。

## rename
对应的系统调用是`rename(const char *oldname, const char *newname)`

当file_2不存在时，执行`rename file_1 file_2 file_1`,可以发现inode信息没变
```shell
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
37224652 -rw-r--r--. 1 root root 20 2月   8 08:17 file_1
[root@fqhnode01 go-program-fqh-test]# strace rename file_1 file_2 file_1 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/rename", ["rename", "file_1", "file_2", "file_1"], [/* 26 vars */]) = 0
rename("file_1", "file_2")              = 0
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
37224652 -rw-r--r--. 1 root root 20 2月   8 08:17 file_2
```

如果目标文件已经存在了呢？,可以发现目标文件37224652是被删除了，但36529688不变，且重命名成功。
```shell
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
36529688 -rw-r--r--. 1 root root  0 2月   8 08:23 file_1
37224652 -rw-r--r--. 1 root root 20 2月   8 08:17 file_2
[root@fqhnode01 go-program-fqh-test]# echo "Hello World, file_1" >file_1 
[root@fqhnode01 go-program-fqh-test]# strace rename file_1 file_2 file_1 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/rename", ["rename", "file_1", "file_2", "file_1"], [/* 26 vars */]) = 0
rename("file_1", "file_2")              = 0
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
36529688 -rw-r--r--. 1 root root 20 2月   8 08:24 file_2
```

## mv
当目标文件file_2不存在时，执行`mv file_1 file_2`，可以发现inode信息不变，底层调用了rename()。。。
```shell
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
36529688 -rw-r--r--. 1 root root 20 2月   8 08:24 file_1
[root@fqhnode01 go-program-fqh-test]# strace mv file_1 file_2 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/mv", ["mv", "file_1", "file_2"], [/* 26 vars */]) = 0
stat("file_2", 0x7fff8e0b2120)          = -1 ENOENT (No such file or directory)
lstat("file_1", {st_mode=S_IFREG|0644, st_size=20, ...}) = 0
lstat("file_2", 0x7fff8e0b1dd0)         = -1 ENOENT (No such file or directory)
rename("file_1", "file_2")              = 0
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
36529688 -rw-r--r--. 1 root root 20 2月   8 08:24 file_2
```

如果目标文件file_2已经存在呢？
```shell
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 8
37224652 -rw-r--r--. 1 root root 20 2月   8 08:40 file_1
36529688 -rw-r--r--. 1 root root 20 2月   8 08:40 file_2
[root@fqhnode01 go-program-fqh-test]# strace mv file_1 file_2 2>&1|egrep 'file_1|file_2'
execve("/usr/bin/mv", ["mv", "file_1", "file_2"], [/* 26 vars */]) = 0
stat("file_2", {st_mode=S_IFREG|0644, st_size=20, ...}) = 0
lstat("file_1", {st_mode=S_IFREG|0644, st_size=20, ...}) = 0
lstat("file_2", {st_mode=S_IFREG|0644, st_size=20, ...}) = 0
rename("file_1", "file_2")              = 0
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 4
37224652 -rw-r--r--. 1 root root 20 2月   8 08:40 file_2
```
可以看出，mv 的主要功能就是检查初始文件和目标文件是否存在及是否有访问权限，之后执行rename()系统调用。 
因而，当目标文件存在时，mv 的行为由 rename()系统调用决定，即类似于删除文件后再重建一个同名文件。

至此，静态的实验已经验证完毕。
***

## 删除正在使用的文件
如果一个进程打开了一个文件file,然后我们执行上述操作，系统怎么办？会不会影响该进程的运行？文件file会受到什么样的影响？

如果删除一个正在执行的二进制文件呢？

参考[删除正在使用的文件——釜底抽薪？](http://blog.csdn.net/lqt641/article/details/60899884)一文，研究下面三种情况：
1. 删除正在被读写的文件
2. 删除正在运行的可执行文件
3. 删除正在使用的动态链接库

### 删除正在被读写的文件
首先用下面的代码来模拟一个进程在读取一个文件
```c
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <sys/types.h>
#include <sys/stat.h>
#include <unistd.h>
#include <fcntl.h>
#define BUFFER_SIZE 1024

int main(void) {
  int fd;
  int i = 0;
  char buffer[BUFFER_SIZE];

  if ((fd=open("data.txt", O_RDONLY)) == -1)   {
    printf("Open file Error\n");
    exit(1);
  }

  int pid = getpid();
  int n = 0;
  while(1)   {
    ++i;
    n= read(fd, buffer, BUFFER_SIZE-1);
    if(n == -1)  {
      printf("read Error\n");
      exit(1);
    }
    buffer[n] = '\0';
    printf("%d pid:%d, fd:%d, content: %s\n", i, pid, fd, buffer);
    sleep(1);
    lseek(fd, 0L, SEEK_SET);
  }

  close(fd);
  exit(0);
}
```

执行效果如下
```shell
[root@fqhnode01 go-program-fqh-test]# ll -li
总用量 20
37224652 -rw-r--r--. 1 root root    6 2月   8 10:12 data.txt
34270886 -rwxr-xr-x. 1 root root 8912 2月   8 10:12 myopenfile
37114809 -rw-r--r--. 1 root root  670 2月   8 10:11 myopenfile.c

[root@fqhnode01 go-program-fqh-test]# ./myopenfile 
1 pid:1788, fd:3, content: hello

2 pid:1788, fd:3, content: hello

3 pid:1788, fd:3, content: hello

```

查看该进程的fd信息，可以发现fd=3是一个符号链接，指向的正是前面进程正在读取的data.txt文件
```shell
[root@fqhnode01 fd]# pwd
/proc/1788/fd
[root@fqhnode01 fd]# cat 3
hello
[root@fqhnode01 fd]# stat 3
  文件："3" -> "/root/go-program-fqh-test/data.txt"
  大小：64        	块：0          IO 块：1024   符号链接
设备：3h/3d	Inode：23403       硬链接：1
权限：(0500/lr-x------)  Uid：(    0/    root)   Gid：(    0/    root)
环境：unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023
最近访问：2018-02-08 10:13:05.990582285 -0500
最近更改：2018-02-08 10:13:01.568372445 -0500
最近改动：2018-02-08 10:13:01.568372445 -0500
创建时间：-
[root@fqhnode01 fd]# ll -li
总用量 0
23400 lrwx------. 1 root root 64 2月   8 10:13 0 -> /dev/pts/0
23401 lrwx------. 1 root root 64 2月   8 10:13 1 -> /dev/pts/0
23402 lrwx------. 1 root root 64 2月   8 10:13 2 -> /dev/pts/0
23403 lr-x------. 1 root root 64 2月   8 10:13 3 -> /root/go-program-fqh-test/data.txt
[root@fqhnode01 fd]# ll -Li 3
37224652 -rw-r--r--. 1 root root 6 2月   8 10:12 3
```

关键步骤来了，现在执行删除命令`rm data.txt`
```shell
[root@fqhnode01 go-program-fqh-test]# rm data.txt 
rm：是否删除普通文件 "data.txt"？y
[root@fqhnode01 go-program-fqh-test]# ls
myopenfile  myopenfile.c
```

进程中记录的状态如下，可以发现该文件已经被标记为`deleted`了。
但`ll -Li 3`依然能依照链接追踪到目标文件，这说明该文件依然是存在于系统中的，没有被真正的删除，inode信息也没有改变。
```shell
[root@fqhnode01 fd]# ll -Li 3
37224652 -rw-r--r--. 0 root root 6 2月   8 10:12 3
[root@fqhnode01 fd]# ll -li
总用量 0
23400 lrwx------. 1 root root 64 2月   8 10:13 0 -> /dev/pts/0
23401 lrwx------. 1 root root 64 2月   8 10:13 1 -> /dev/pts/0
23402 lrwx------. 1 root root 64 2月   8 10:13 2 -> /dev/pts/0
23403 lr-x------. 1 root root 64 2月   8 10:13 3 -> /root/go-program-fqh-test/data.txt (deleted)
[root@fqhnode01 fd]# stat 3
  文件："3" -> "/root/go-program-fqh-test/data.txt (deleted)"
  大小：64        	块：0          IO 块：1024   符号链接
设备：3h/3d	Inode：23403       硬链接：1
权限：(0500/lr-x------)  Uid：(    0/    root)   Gid：(    0/    root)
环境：unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023
最近访问：2018-02-08 10:13:05.990582285 -0500
最近更改：2018-02-08 10:13:01.568372445 -0500
最近改动：2018-02-08 10:13:01.568372445 -0500
创建时间：-
```

但此时进程1788依然在输出原来的信息，即上面的删除动作没有影响到进程1788的正常运行。背后的原理是虽然data.txt在相应目录中已经被删除，但是由于有其他进程打开这一文件后还未关闭，操作系统其实还为这些进程保留了磁盘上被打开的文件内容。
```shell
758 pid:1788, fd:3, content: hello

759 pid:1788, fd:3, content: hello
```

现在在原有目录中再次建立 data.txt 文件(一个新的inode节点36529688)，并且更改其内容，会发生什么？
```shell
[root@fqhnode01 go-program-fqh-test]# touch data.txt
[root@fqhnode01 go-program-fqh-test]# echo "world">data.txt 
[root@fqhnode01 go-program-fqh-test]# cat data.txt 
world

[root@fqhnode01 go-program-fqh-test]# stat data.txt 
  文件："data.txt"
  大小：6         	块：8          IO 块：4096   普通文件
设备：801h/2049d	Inode：36529688    硬链接：1
权限：(0644/-rw-r--r--)  Uid：(    0/    root)   Gid：(    0/    root)
环境：unconfined_u:object_r:admin_home_t:s0
最近访问：2018-02-08 10:30:43.550059478 -0500
最近更改：2018-02-08 10:30:40.939755073 -0500
最近改动：2018-02-08 10:30:40.939755073 -0500
创建时间：-
```

进程1788此时的输出呢？可以发现依然是原来的信息，与新建文件的内容无关。
```shell
1118 pid:1788, fd:3, content: hello

1119 pid:1788, fd:3, content: hello
```

**小结:** 当进程打开一个文件后，如果我们在磁盘上删除这个文件，虽然表面上看在目录中已经成功删除了这个文件名，但是实际上系统依然保留了文件内容，直至所有进程都关闭了这一文件。这与`unlink()`系统调用的功能描述是一致的。

### 删除正在运行的可执行文件
如果一个程序在执行，此时把此程序对应的磁盘文件删除，程序会崩溃吗？

程序通常是部分载入内存的，当发生缺页时，会去磁盘上读取新的页面，如果从这个角度看，删除磁盘上的可执行文件，程序通常会因为找不到磁盘文件而崩溃。那么事实是怎样的呢？

直接用上面正在运行的1788进程来做实验,用`lsof`命令查看，可以发现二进制文件myopenfile正在被进程1788使用。
```shell
[root@fqhnode01 go-program-fqh-test]# lsof myopenfile
COMMAND    PID USER  FD   TYPE DEVICE SIZE/OFF     NODE NAME
myopenfil 1788 root txt    REG    8,1     8912 34270886 myopenfile

[root@fqhnode01 1788]# ll -Lil exe /root/go-program-fqh-test/myopenfile
34270886 -rwxr-xr-x. 1 root root 8912 2月   8 10:12 exe
34270886 -rwxr-xr-x. 1 root root 8912 2月   8 10:12 /root/go-program-fqh-test/myopenfile

[root@fqhnode01 1788]# md5sum  exe /root/go-program-fqh-test/myopenfile
ff7f648c8a14b29ee0f80de9866d16b3  exe
ff7f648c8a14b29ee0f80de9866d16b3  /root/go-program-fqh-test/myopenfile
```

现在我们来执行删除动作
```shell
[root@fqhnode01 go-program-fqh-test]# rm myopenfile
rm：是否删除普通文件 "myopenfile"？y
[root@fqhnode01 go-program-fqh-test]# ls
data.txt  myopenfile.c
```

可以发现，删除之后，进程依然在正常运行
```shell
2154 pid:1788, fd:3, content: hello

2155 pid:1788, fd:3, content: hello
```

其他状态如下
```shell
[root@fqhnode01 1788]# md5sum  exe /root/go-program-fqh-test/myopenfile
ff7f648c8a14b29ee0f80de9866d16b3  exe
md5sum: /root/go-program-fqh-test/myopenfile: 没有那个文件或目录

[root@fqhnode01 1788]# ll -Lil exe /root/go-program-fqh-test/myopenfile
ls: 无法访问/root/go-program-fqh-test/myopenfile: 没有那个文件或目录
34270886 -rwxr-xr-x. 0 root root 8912 2月   8 10:12 exe

[root@fqhnode01 1788]# stat exe
  文件："exe" -> "/root/go-program-fqh-test/myopenfile (deleted)"
  大小：0         	块：0          IO 块：1024   符号链接
设备：3h/3d	Inode：23371       硬链接：1
权限：(0777/lrwxrwxrwx)  Uid：(    0/    root)   Gid：(    0/    root)
环境：unconfined_u:unconfined_r:unconfined_t:s0-s0:c0.c1023
最近访问：2018-02-08 10:12:58.359769061 -0500
最近更改：2018-02-08 10:12:58.358768561 -0500
最近改动：2018-02-08 10:12:58.358768561 -0500
创建时间：-
```

可以发现这里和前面的删除一个正在被某进程读取文件是类似的。

**小结:** 如果一个进程正在运行，删除其对应的可执行文件是安全的，正在执行的进程并不会崩溃，这一安全性由操作系统来保证。

最后，如果试图向正在运行的可执行文件中写入内容时，会写入失败，系统提示 “Text file busy” 表示有进程正在执行这一文件。
所以是不可以直接用`cp`命令来直接实现热更新一个二进制文件的。因为`cp`在目标文件已经存在的情况下，实际上是清空了目标文件的内容，之后把新的内容写入目标文件。

至此，可以看出为什么用`rm`命令删除一个正在运行的可执行文件会成功，而用`cp`命令覆盖正在运行的可执行文件却会失败。这是因为用 rm 删除时，只是删除了文件名，系统为运行的进程自动保留了可执行文件的内容。而用 cp 命令覆盖时，会尝试向当前可执行文件中写入新内容，如果成功写入，必然会影响当前正在运行的进程，因而操作系统禁止了对可执行文件的写入操作。

### 删除正在使用的动态链接库
如果一个动态连接库正在被使用，这时删除它，正在使用动态库的进程会崩溃吗？

一个进程使用的动态链接库可以在目录`/proc/pid/map_files`中查看。

当动态链接库正在被使用时，rm 命令删除的也只是文件名，虽然在原目录下已经没有了对应的库文件，但是操作系统会为使用库的进程保留库文件内容，因而rm 并未真正删除磁盘上的库文件。从这点上来看，当删除使用中的动态链接库时，操作系统的机制和删除可执行文件及删除被打开的文件是一样的。

那么，如果一个动态链接库正在被使用，向此库文件中写入内容，会发生什么？操作系统是允许我们向使用中的动态库中写入内容的，并不会像写入可执行文件一样报告“Text file busy”，因而在写入方面，操作系统只对可执行文件进行了保护。但如果往一个进程正在使用的动态库中写入内容，该进程将会`崩溃`。即向动态库写入内容会对进程的运行造成影响。这是由于内存映射区与磁盘文件的自动同步造成的。

## 总结
首先研究了Linux文件系统中几个常用的命令底层实现，然后再研究删除以下三类文件时操作系统的行为：

- 删除正在被打开的文件
- 删除正在被运行的可执行文件
- 删除正在被使用的动态链接库文件

针对上述三类文件，操作系统提供了合理的保护机制，即我们虽然”在表面上“成功删除了文件，但是操作系统依然为使用它们的进程保留了原始的磁盘文件内容，直到所有进程都释放这些文件后，操作系统才会真正的把文件内容从磁盘上删除。这体现了linux 系统的健壮性。

对上面三类使用中的文件进行写入时，只有正在运行的可执行文件得到了操作系统的保护，被打开的文件及正在使用的动态链接库文件都是可以被写入的。对使用中的动态链接库的写入，通常是不需要的，并且很可能导致程序崩溃。因而要避免对动态库文件的写入。特别的，由于cp命令覆盖已存在的文件时，采用的是写入操作，因而对动态链接库的更新，不要使用cp 命令，而是要使用 rm 删除原库文件。之后把再新的库放到相应位置，重启使用它的程序或者使用 dlopen 动态加载新的库。

在上线需要更新可执行程序或动态链接库时，不要使用 cp 命令覆盖，而是要使用 rm 删除旧有文件，然后再把新的文件移动到原文件的位置。 install 命令和rpm包安装时使用的机制都是先删除旧文件，再建立新文件。这种操作能安全的更新文件，并且不影响当前进程的运行。当然，如果要想让新文件生效，则需要重新载入文件（重新打开文件、重启程序、重新加载动态链接库）。

推荐的热更新方法：
1. 删除原有可执行文件。例如：rm a.out
2. 以相同的文件名把新的可执行文件放到原文件的位置：mv b.out a.out
3. 重启服务。

## 参考
[UNIX环境高级编程（中文第三版）]()

[Unix Linux编程实践教程]()

部分内容摘抄于[删除正在使用的文件——釜底抽薪？](http://blog.csdn.net/lqt641/article/details/60899884)


