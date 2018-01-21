# Unix/Linux编程实践之IO重定向和管道

个人读书笔记

## I/O重定向的原理模型
`ls > test.file`是如何工作的？ shell是如何告诉程序把结果输出到文件，而不是屏幕？
在`who | sort > user.file`中，shell是如何把一个进程的输出连接到另一个进程的输入的？

![IO重定向](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/IO重定向.png)

其实原理就是`>`操作符是把文件看成任意大小的和结构的变量。 
下面两个实现是等价的，其中前者用`c`实现，后者用`shell`实现：
```c
x = func_a(func_b(y));  //把func_b的结果作为func_a的输入
```
等价于
```shell
prog_b | prog_a > x
```

### 标准I/O
标准I/O的定义是什么？ 
所有的Unix I/O重定向都是基于标准数据流的原理，所有的Unix工具默认都会携带三个数据流：
1. 标准输入，输出处理的数据流
2. 标准输出，结果数据流
3. 标准错误处理，错误消息流

![标准数据流](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/标准数据流.png)

![标准数据流模型](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/标准数据流模型.png)

从上面两个图可以看出来，所有的Unix工具都是使用这种数据流模型。
这三种流的每一种都是一种特别文件描述符fd。

### 重定向的是shell
一般情况下，通过shell来运行一个程序时，该进程的stdin、stdout和stderr都是默认被连接到当前终端上。 
所以用户的输入，程序的输出或者错误消息都是显示到屏幕终端。

**重定向的是shell，而不是程序本身。**
程序仅仅是持续不断地和`文件描述符0、1、2`打交道，把fd和指定文件联系起来的是`shell`。 
也就是说shell本身并不会把`重定向这个动作和指定文件`传递给程序。

### 最低可用文件描述符
文件描述符是一个数组的索引号。 
每个进程都有其打开的一组文件，这些打开的文件被保存在一个数组里面，fd就是某文件在此数组中的索引号。

当打开（open）一个文件的时候，内核为此文件安排的fd总是此数组中最低的可用位置的索引。

![最低可用fd原则](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/最低可用fd原则.png)

最后，库函数`isatty(fd)`，可以用来给一个进程判断一个fd的指向，和系统调用`fstat`有关。

## 重定向的实现
根据上面说明的模型，可以设想一下如何写一个程序来实现重定向的功能。 
有很多种实现方法，下面以stdin定向到一个文件为例进行说明。

### close-then-open策略
close-then-open策略，具体步骤如下：

1. close(0)，把标准输入的连接挂断
2. `fd = open(file_name，O_RDONLY)`, 和指定的文件建立一个连接，此时最低可用的fd应该是0

![重定向策略open-then-close](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/重定向策略open-then-close.png)

### open-close-dup-close策略
open-close-dup-close策略，具体步骤如下：

1. `fd = open(file_name，O_RDONLY)`, 和指定的文件建立一个连接
2. close(0)
3. `newfd = dup(fd)` ，copy open fd to 0，得到的newfd是0
4. close(fd)

![dup重定向](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/dup重定向.png)

### open-dup2-close策略
把`newfd = dup(fd)`和`close(0)`组成成一个单独的系统调用dup2，`newfd=dup2(oldfd,newfd)`，一般使用的newfd设置为0。


## shell为子进程重定向其输出
以`who>userlist.file`为例子，shell运行`who`程序，并将`who`的输出重定向到userlist.file。 其中的原理是什么？

关键之处在于fork和exec的时间间隙。 
fork之后，子进程准备执行exec。 
shell就利用这个时间间隙来完成子进程的输出重定向工作。

系统调用`exec`替换进程中运行的程序，但它不会改变进程的属性、和进程中所有的连接。 
文件描述符fd(复制了一个连接)、进程的用户ID、进程的优先级都不会被系统调用`exec`改变。 
这是因为打开的文件的并不是程序的代码，也不是数据。 fd是进程的一个属性，故exec不能改变它们。

也就是文件描述符fd是可以被`exec`传递的，也会从父进程传递到子进程。

![shell为子进程重定向其输出](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/shell为子进程重定向其输出.png)

具体流程如下：
1. 父进程fork()
2. 子进程继承了父进程的stdout fd＝1
3. 子进程close(1)
4. 子进程fd＝create(file，0644)，此时最低可用fd＝1
5. 子进程exec()


***

## 管道pipe
管道是内核中一个数据队列，其每一端都连接着一个文件描述符fd，管道有一个读取端和写入端。

pipe又被称为无名管道，只有共同父进程的进程之间才可以用pipe连接。原因是它没有“名字”或者说是匿名的，另外的进程看不到它。

函数原型`resutl=pipe(int array[2])`，其中array[0]为读取端的fd，array[1]为写入端的fd。 
管道的实现隐藏在内核中，进程只能看到两个文件描述符fd。

![管道](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/管道.png)

pipe()和前面open()这些系统调用是类似的，都适用于最低可用fd原则。 
那么一个进程创建一个pipe之后的效果图是怎样的？

![一个进程创建pipe](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/一个进程创建pipe.png)

可以发现一个进程刚创建一个管道成功的之后，一个pipe的两端都是连着自己的。 
一般而言，很少有进程利用管道向自己发送数据。 
都是把`pipe()`和`fork()`结合起来，连接两个不同的进程。

### 使用fork共享管道
一个进程刚创建一个管道成功的之后，调用fork()。那么子进程就拥有了两个fd，分别执行该管道的两端。
理论上，父子进程可以同时对该管道进行读写操作。 
一般是一个读，一个写。

![fork共享管道](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/fork共享管道.png)

docker就是把fork、pipe和exec结合起来使用。

### 管道并非文件
一方面，管道和文件一样，是不带任何结构的字节序列，都可以使用read、write等操作。
另一方面，两者还是存在不同之处的。

1. 管道的读取，必须要有进程往管道中写入数据，否则会产生阻塞
2. 多个读者可能会引起麻烦，因为管道是一个队列，读取之后，数据就不存在了
3. 写进程会产生阻塞，直到管道有足够的容量允许你写入
4. 若读者在读取数据，写操作会失败

如果两个进程没有关联，使用`FIFO`联系。 

如果两个进程处于不同的主机上面呢？这个时候把管道的思路扩展到`套接字socket`上。

### pipe和fifo的本质区别
无名管道不属于任何文件系统，只存在于内存中，它是无名无形的，但是可以把它看作一种特殊的文件，通过使用普通文件的read(),write()函数对管道进行操作。

为了使用fifo，LINUX中设立了一个专门的特殊文件系统--管道文件，它存在于文件系统中，任何进程可以在任何时候通过有名管道的路径和文件名来访问管道。但是在磁盘上的只是一个节点，而文件的数据则只存在于内存缓冲页面中，与普通管道一样。

fifo是基于VFS，对应的文件类型就是FIFO文件，可以通过`mknod`命令在磁盘上创建一个FIFO文件。
注意：这就是fifo与pipe的本质区别，pipe完全就是存在于内存中，在磁盘上毫无痕迹。

当进程想通过该FIFO来通信时就可以标准的API open打开该文件，然后开始读写操作。对于FIFO的读写实现，它与pipe是相同的。 
区别在于，FIFO有open这一操作，而pipe是在调用pipe这个系统调用时直接创建了一对文件描述符用于通信。 
并且，FIFO的open操作还有些细致的地方要考虑，例如如果写者先打开，尚无读者，那么肯定是不能通信了，所以就需要先去睡眠等待读者打开该FIFO，反之对读者亦然。

## 参考
[Linux中的pipe与named pipe](http://www.linuxidc.com/Linux/2011-05/36155.htm)

