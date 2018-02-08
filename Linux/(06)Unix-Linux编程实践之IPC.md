# Unix/Linux编程实践之IPC
个人读书笔记

两个进程交换数据，选择合适的通信方式也是提高程序效率的一种方法。

## 概念
1. I/O多路复用，挂起并等待从多个源端输入：select、poll、epoll
2. 命名管道，mkfifo
3. 共享内存,shmget、shmat、shmctl、shmdt
4. 文件锁
5. 信号量，进程间的加锁机制
6. IPC，InterProcess Communication。包括文件、fifo、共享内存等方式

## I/O多路复用
情景分析： 一个进程如果需要从多个输入源获取信息，每次调用Read()，必然要从用户态`切换`到内核态进行操作。 
假设cpu只有单核的情况下，也就是说在某一个时刻，内核仅仅只能和一个I/O源打交道。

这种时候，就要用到I/O多路复用机制。

`流`的概念，一个流可以是文件，socket，pipe等等可以进行I/O操作的内核对象。不管是文件，还是套接字，还是管道，我们都可以把他们看作流。

关于这部分的详细内容可以参考文章[containerd之monitor](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/docker/(23)containerd之monitor.md)，在分析containerd对容器中进程进行monitor的时候，用到的就是`epoll机制`。

## 进程间传输数据
一个进程如何从另外一个进程中获取数据？ 

有三种解决办法：文件、有名管道(FIFO)、共享内存。 
这三种方法分别通过磁盘、内核、用户空间进行数据传输。

![三种传输数据的办法](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/三种传输数据的办法.png)

1. 文件  
注意读写权限、一写多读、竞态条件

2. FIFO  
无名管道pipe只能连接相关进程，常规管道由进程创建，并由最后一个进程关闭。

使用`命名管道FIFO`可以连接不相关的进程，并且`可以独立于进程存在`。相关调用如下：
```shell
int mkfifo(const char * pathname,mode_t mode);
unlink(fifoname) ,用于删除fifo
open
read
write
```
FIFO不存在竞态条件问题，在信息长度不超过容量的前提下，read和write都是一个原子操作。 在读者和写者连通之前，内核会把进程都挂起来，所以这里不需要锁机制。

关于FIFO的更详细信息可以参考[FIFO管道](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/docker/(18)FIFO管道.md)

3. 共享内存段
可以发现，`文件`和`FIFO`两种方式都是通过write把字节流从用户空间中复制到`内核缓存`中，read把数据从内核缓存复制到用户空间中。都需要用户态和内核态的切换。

共享内存就不需要从用户态切换到内核态。同一个系统里面的两个进程使用共享内存的方式进行交换数据，两个进程都有一个指向该内存空间的指针，资源是共享的，不需要把数据进行复制来拷贝去的。

实际上，在存储器中存储数据比想象的中要复杂得多。 
虚拟内存系统允许`用户空间中的段`交换到磁盘上， **也就是说其实`共享内存段`的方法也是有可能对磁盘进行读写操作的** 。

共享内存之于进程，就类似于共享变量之于线程。

![进程通过共享内存交换数据](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/进程通过共享内存交换数据.png)

使用共享内存有几个注意点：
  * 共享内存段在内存中的存在，是不依赖于进程的存在的
  * 共享内存段有自己的名字，称为关键字key
  * 关键字是一个整型数
  * 共享内存段有自己的拥有者以及权限位
  * 进程可以连接到某共享内存段，并且获得指向此段的指针

相关系统调用
```c
int shmget(key_t key, size_t size, int shmflg); //得到一个共享内存标识符或创建一个共享内存对象并返回共享内存标识符
void *shmat(int shmid, const void *shmaddr, int shmflg); //连接共享内存标识符为shmid的共享内存，连接成功后把共享内存区对象映射到调用进程的地址空间，随后可像本地空间一样访问
char *strcpy(char* dest, const char *src); //把从src地址开始且含有NULL结束符的字符串复制到以dest开始的地址空间
```

和文件系统类似，有竞态条件，拥有者和权限位的概念。

## 选择哪一种通信方式
交换数据的通信方式有很多，应该如何选择呢？ 
这里不是说有个明显的标准，而是需要熟悉各种通信机制的特点，根据场景来选择最优的通信方式。

1. 速度  
一般而言，通过文件或者是FIFO的方式来传输数据需要更多的操作，因为其过程都需要CPU从用户态切换到内核态，然后再切换回到用户态。 
如果是文件的话，内核还需要把数据复制到磁盘上，然后再从磁盘中把数据拷走。。。。

前面也提到，`共享内存段`的方法也是有可能对磁盘进行读写操作。

2. 连接和无连接  
文件和共享内存就像公告牌一样，数据生产者把信息贴在广告牌上，多个消费者可以同时从上面读取信息。

FIFO则要求建立连接，因为在内核转换数据之前，读者和写者都必须等待FIFO被打开，并且只有一个读者可以读取此信息。

流socket是面向连接的，而数据报socket则不是。

3. 传输范围  
共享内存和FIFO只允许本机上进程间的通信。

文件可以支持不同host主机上进程的通信。

使用IP地址的socket可以支持不同host主机上进程的通信，而使用Unix地址的socket则不能。

4. 访问限制  
你是希望所有人都能与服务器进行通信还是只有特定权限的用户才行？ 
文件、FIFO、共享内存、Unix地址的socket都能提供标准的Unix文件系统权限，而Internet Socket则不行。

5. 竞态条件  
使用共享内存和共享文件要比使用管道、socket麻烦得多。 

管道和socket都是由内核来进行管理的队列。写者只需把数据放入一端，而读者则从另一段读出，进程不需关心其内部实现。

然而对于共享文件、共享内存来说，对它们的访问不是由内核来管理。需要进程来进行管理，比如文件锁和信号量。

## 文件锁
有三种锁：flock、lockf和fcntl。其中最灵活的和移植性最好的是`fcntl`锁。
```c
int fcntl(int fd, int cmd); 
int fcntl(int fd, int cmd, long arg); 
int fcntl(int fd, int cmd, struct flock *lock);
```
fcntl()针对(文件)描述符提供控制。 
参数fd 是被参数cmd操作的描述符。 
针对cmd的值,fcntl能够接受第三个参数int arg

## 信号量
信号量是一个内核变量，可以被系统中的任意一个进程访问，是系统级的全局变量。










