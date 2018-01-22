# Unix-Linux编程实践之socket

个人读书笔记

## popen和fopen
`fopen("file_name","r")`,以只读方式打开一个指向文件file_name的带缓存的连接。

`popen()`是类似的，只不过打开的是一个指向进程的带缓冲的连接。 
popen通过封装pipe、fork、dup和exec等系统调用，使得对程序的操作变得和文件一样。 
其实现是运行了一个程序(fork)，并返回指向新进程的stdin和stdout的连接。
```c
fp = popen("who|sort", "r")
fgets(buf,len,fp)
printf(buf)
pclose(fp) //pclose会调用wait()来等待子进程退出，防止僵尸进程的产生
```
![fopen和popen](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/fopen和popen.png)

唯一能够运行任意shell命令的程序是shell本身，即`/bin/sh`。 
sh支持`-c`选项，告诉shell执行完某命令之后退出。

管道使得一个进程向另外一个进程发送数据，就像向文件发送数据一样。 
但管道只能用于有血缘关系的进程，也只能用于同一个host主机上的进程通信。
故Unix提供了另外一种通信机制---`socket`

popen对于编写网络服务是非常危险的，因为它直接把一行字符串传递给shell。

在网络程序中，把字符串传递给shell是非常错误的想法。 
因为不确定对方有足够的缓存来接受字符串。

## socket
socket允许在不相关的进程之间创建类似于管道的连接，甚至可以通过socket连接其他host主机上的进程。

基于流的系统都需要建立连接。

服务器端：
1. `sockid=socket(int domain, int type, int protocol)`,系统调用socket向内核申请一个socket，得到一个通信端点。
2. `result=bind(int sockid, struct sockaddr *addrp, socklen_t addrlen)`，绑定地址到socket,包括主机、port。
3. `result=listen(int sockid, int qsize)`，服务端请求内核允许指定的sockid接受请求，qsize是接收队列的长度。
4. `fd=accept(int sockid, struct sockaddr *callerid, socklen_t *addrlen)`,服务端使用sockid接收请求。
5. accept会一直阻塞当前进程，一直到有连接被建立起来
7. close关闭连接

client端：
1. `sockid=socket(int domain, int type, int protocol)`,系统调用socket向内核申请一个socket，得到一个通信端点。
2. `result=connect(int sockid, struct sockaddr *serv_addrp, socklen_t addrlen)`,建立连接
3. 传送数据和close
![socket流程图](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/socket流程图.png)

比较管道和socket：
  1. 管道是一对相连接的文件描述符
  2. socket是一个未连接的通信端点，也是一个潜在的文件描述符fd。客户进程通过把自己的socket和服务端的socket相连来创建一个通信连接。
  3. 到管道和socket的连接使用文件描述符。文件描述符为程序提供了与文件、设备和其他的进程通信的统一编程接口。

## waitpid()
一个进程fork多个子进程的时候，怎么wait()所有子进程的退出信号？ 
答案是`while(waitpid(-1,NULL,WNOHANG)>0)`，waitpid()提供了wait函数超集的功能。其中：

1. 第一个参数表示它要等待的进程ID，-1表示等待所有的子进程
2. 第二个参数用来获取状态，服务端不关心子进程状态时，填NULL
3. 最后一个参数表示选项，`WNOHANG`表示如果没有僵尸进程，则不必等待

## 数据报socket
前面所介绍的是`流socket`，是基于TCP协议实现的，双方通信之前需要建立连接，然后使用该连接进行字节流传送。

![网络传输切割数据包](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/网络传输切割数据包.png)

而本小节将要介绍的是`数据报socket`，是基于UDP协议实现的，Client不必建立连接，只要向特定的地址发送嘻嘻即可，而服务器进程在该地址接收信息。

TCP: 传输控制协议，Transmission Control Protocol
UDP: 用户数据报协议，User Datagram Protocol

TCP是流式的，具有分片/重组，排序的，可靠、连接的特性。 
而UDP则是数据报式，内核不会给数据加编号标签，在目的地也不会重组，不保证一定能到达，可能会有多个发送者。

UDP多适用于能容忍丢包的声音和视频流。

可以通过系统调用kill给一个进程发送编号为0的信号，用来判断该进程是否还存活。 如果进程不存在，内核将不会发送信号，而是返回一个error。

## Unix域socket
有两种连接，`流连接`和`数据报连接`。 

也有两种socket地址：
1. `Internet地址`，主机ID+端口，可以接收本地、甚至更大网络上的Client的请求
2. `本地地址`，又称Unix域地址，是一个文件名(/dev/log，/dev/printer)，没有主机号和端口号，仅用于一个host主机内部通信

socket是进程间通信的万能工具















