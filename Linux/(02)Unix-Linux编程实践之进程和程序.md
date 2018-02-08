# Unix/Linux编程实践之进程和程序

个人读书笔记

## 进程和程序
1. Unix通过把可执行代码(程序)装入进程，并执行它。 
2. 进程是运行一个程序所需的内存空间和其他的资源集合。 
3. 每个运行中的程序在自己的进程中运行。
4. 每个进程都有一个唯一的进程ID、所有者、大小及其他属性。

## shell
shell是什么？ shell是一个管理进程和运行程序的程序，主要有三个功能：
1. 运行程序，shell把date、ls这些普通程序装入内存，并运行它们
2. 管理输入输出
3. 可编程

可以理解为一个shell需要三项技术：
1. 如何建立一个进程，fork
2. 如何运行一个程序，execvp
3. 父进程等待子进程结束

### shell是如何运行程序的
现象：shell打印提示符号，用户输入命令后，shell就运行这个命令，然后shell再次打印提示符。 这个过程发生了什么？具体步骤如下：
1. 用户键入 a.out
2. shell建立一个新的进程来运行这个程序
3. shell将程序从磁盘中载入
4. 程序在它的进程中运行直到结束

![一个shell的主循环](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/一个shell的主循环.png)

写成伪代码，如下：
```
while(!end_of_input){
	get_command()
	execute_command()
	wait_for_command_to_finish()
}
```

### 一个进程是如何运行另外一个进程的-exec()系统调用
答案是程序调用`execvp`，在指定路径中查找并执行一个文件，函数原型`result = execvp(const char *file, const char *argv[])`。 

![execvp的原理](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/execvp的原理.png)
具体流程如下：
1. 程序调用execvp()，这是exec家族的一员
2. 内核从磁盘中把用户期待的程序载入当前进程
3. 内核把arglist复制到进程
4. 内核调用main(argc，argv)

存放在硬盘上的可执行文件能够被UNIX执行的唯一方法是：由一个现有进程调用六个`exec`函数中的某一个。

```c
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(void)
{
    printf("entering main process---\n");
    int ret;
    // execvp 有两个参数，要运行的文件名cmd；参数数组argv[]
    // 参数数组argv[]的第一个元素需要设置为文件名cmd，且最后一个元素必须是NULL
    char *argv[] = {"ls","-l",NULL};
    ret = execvp("ls",argv);
    if(ret == -1)
        perror("execl error");
    printf("exiting main process ----\n");
    return 0;
}
```
执行效果如下
```shell
[root@fqhnode01 c]# cc execvp.c -o myexec
[root@fqhnode01 c]# 
[root@fqhnode01 c]# ./myexec 
entering main process---
总用量 32
-rw-r--r--. 1 root root  483 1月  21 04:19 execvp.c
-rwxr-xr-x. 1 root root 8656 1月  21 04:19 myexec
```

这里面有个疑问，为什么`exiting main process`没有打印出来？

一个程序在一个进程（即内存和内核中相应的一些数据结构）中运行。 
而`execpv`的功能是把一个程序从磁盘载入一个进程中，以便它可以被运行。但是载入到哪一个进程里面？ 
这就是问题所在：内核将新程序载入到当前进程，替代当前进程的代码和数据！

也就是说，`exec`系统调用 **从当前进程中把当前程序的机器指令清除，然后在空的进程中载入调用时指定的程序代码，最后运行这个新的程序** 。 
exec调整进程的内存分配以适应新的程序对内存的要求。 
相同的进程，不同的内容（程序）。

exec系列函数（execl、execlp、execle、execv、execvp），函数原型如下：
```c
int execl(const char *path, const char *arg, ...);

int execlp(const char *file, const char *arg, ...);

int execle(const char *path, const char *arg, ..., char * const envp[]);

int execv(const char *path, char *const argv[]);

int execvp(const char *file, char *const argv[]);
```
path参数表示你要启动程序的名称包括路径名；

arg参数表示启动程序所带的参数，一般第一个参数为要执行命令名，不是带路径且arg必须以NULL结束

返回值:成功返回0,失败返回-1

上述exec系列函数底层都是通过execve系统调用实现：
```c
#include <unistd.h>
  int execve(const char *filename, char *const argv[],char *const envp[]);
```

### 建立新的进程-fork()系统调用
前面运行的`execvp.c`在执行完之后，就直接退出了。 现在要实现一个shell，让其执行完一个cmd之后，继续等待下一个cmd的到来，而不是直接退出。 
shell对于内核来说也是一个进程。

可以利用系统调用fork()来创建一个新的进程，让新的进程调用execvp()来执行用户指定的cmd，而shell自己继续等待新的命令到来。

一个进程P调用`fork()`之后，内核发生了什么？，具体步骤如下：
1. 分配新的内存块和新的内核数据结构
2. 复制原来的进程到新的进程中
3. 向运行进程集添加新的进程
4. 将控制返回给两个进程
5. 从此之后，就有了两个一样的进程，而且都运行到相同的地方，然后各自开展自己独立的生命旅程。

fork出来的子进程不是从头开始运行的，而是从fork()返回的地方开始其生命旅程。

![系统调用fork](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/系统调用fork.png)

```c
#include <stdio.h>

int main(void)
{
    printf("my pid is %d\n",getpid());
    fork();
    fork();
    printf("my pid is %d\n",getpid());
}
```

可以根据`fork()`的返回值来判断，本进程是父进程还是子进程：
1. 如果是子进程，fork()返回 0
2. 如果出错，fork()返回 －1
3. 如果是父进程，得到的结果是子进程的pid（>0）

fork在子进程中返回0而不是父进程的ID的原因在于：任何子进程只有一个父进程，而且子进程总是可以通过调用getppid取得父进程的ID。 
相反，父进程可以有许多子进程，而且无法获得各个子进程的进程ID。 
如果父进程想要跟踪所有子进程的ID，那么它必须记录每次调用fork的返回值。

### 父进程如何等待子进程退出
系统调用`pid＝wait(&status)`：函数wait()会暂停调用它的进程，直到子进程结束。 

wait()的返回值是`子进程的pid`，而其入参`&status`的定义是`int型的地址`，会得到子进程结束的状态值`exit(n)`

具体步骤如下：
1. 父进程在左边调用fork()
2. 内核构造子进程
3. 子进程和父进程开始`并行`运行
4. 父进程调用wait()，内核会挂起父进程，直到子进程结束。也就是说这个时候父进程发生了阻塞。
5. 子进程任务结束时会调用exit(n)，n会复制给wait()的入参status
6. 此时内核唤醒父进程，父进程根据wait()的返回值来判断哪一个子进程结束了。

![系统调用wait](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/系统调用wait.png)

### 小结
所以到现在可以做个小结，一个shell的模型是怎样的？

shell用fork()创建一个新的进程，用exec()在新的进程中运行用户指定的程序，最后shell用wait()等待新进程的结束。 
wait()同时从内核中获取子进程的退出状态。

![shell模型](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/shell模型.png)

![shell模型简化版](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/shell模型简化版.png)

### 用进程编程、用函数编程
函数一般是 call/return； 而进程一般是 execvp/exit。是不是发现两者很类似。

![函数和进程的相似性](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/函数和进程的相似性.png)

Unix一般用环境变量的方式来解决fork()/exec()中全局变量的问题，还没有副作用。

### 僵尸进程
子进程的结束和父进程的运行是一个异步过程，即父进程永远无法预测子进程到底什么时候结束。 

**僵尸进程** ：僵尸进程是一个早已死亡的进程，但在进程表(processs table)中仍占了一个位置(slot)。通过ps命令查看其带有defunct的标志。

exit是fork的逆操作，会调用一个`_exit`的内核操作，负责处理所有分配给该进程的内存，关闭这个进程打开的所有文件，释放内核用来管理该进程的所有数据结构。

子进程传递给exit(n)的值n如何处理呢？ 这个值n是子进程结束的时候存放到内核中，等待其父进程通过wait()来获取的。 
如果父进程没有来取值，那么这个值将会被一直保存在内核中，直到父进程调用wait()来取。

一个子进程先于其父进程退出，但是他的父进程没有等待(调用wait / waitpid)它，那么它将变成一个僵尸进程。 

但是如果该进程的父进程已经先结束了，那么该进程就不会变成僵尸进程。 
因为每个进程结束的时候，系统都会扫描当前系统中所运行的所有进程，看看有没有哪个进程是刚刚结束的这个进程的子进程，
如果是的话，就由Init进程来接管他，成为他的父进程，从而保证每个进程都会有一个父进程。 
而Init进程会自动wait其子进程，因此被Init接管的所有进程都不会变成僵尸进程。

一般僵尸进程很难直接kill掉，不过您可以kill僵尸爸爸。父进程死后，僵尸进程成为”孤儿进程”，过继给1号进程init，init始终会负责清理僵尸进程．它产生的所有僵尸进程也跟着消失。

防止僵尸进程的产生：fork之后需要wait()子进程，接收子进程的退出信号。

## 可编程的shell
前面介绍到可以在shell中运行程序，而shell本身就是一种编程语言。
在unix中，很多引导程序都是使用的shell脚本。

shell是一种编程语言解释器，解释从键盘输入的命令，也解释存储在脚本中的命令序列。

shell的种类有`sh`，`bash`，`ksh`之类，语法大概类似。

exit(0)，代表了成功。

`set`命令列出当前shell定义的所有变量。

shell支持特殊的变量来表示系统设置，比如变量`$$`表示shell的进程ID，而`$?`表示最后一条命令的退出状态值。

很多shell允许用户通过对一个特定的变量赋值来设置命令提示符，sh和bash用变量`SP1`。

### 环境变量的传递
每个程序都会从调用它的进程中继承一个环境变量，环境变量用来保存回话(session)的全局设置和某个程序的参数设置。

环境变量是每一个程序都可以存取的一个字符串数组。数组的地址被存放在一个名为environ的全局变量中。

![环境变量的存储](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/环境变量的存储.png)

前面说到`exec`系统调用会把当前进程的所有数据全部清除，换成新的程序。但是`environ`指针指向的环境变量数据是个例外。 
当内核执行系统调用execve时，它会从调用者那里复制一份`environ`指向的数据。

系统调用`exec`替换进程中运行的程序，但它不会改变进程的属性、和进程中所有的连接。 
故文件描述符fd(复制了一个连接)、进程的用户ID、进程的优先级都不会被`exec`改变。 
其中文件描述符fd是`进程的一个属性`，并不是属于程序的。

![exec复制环境变量](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/exec复制环境变量.png)

还需要注意的是，子进程中环境变量是父进程env的一个副本，子进程不能修改父进程的env。 这是因为fork和exec的时候，env是会被自动复制的。

具体例子如下，首先构造一个`hello`的新程序：
```c
#include <unistd.h>
#include <stdio.h>
extern char** environ;

int main(void)
{
    printf("hello pid=%d\n", getpid());
    int i;
    for (i=0; environ[i]!=NULL; ++i)
    {
        printf("%s\n", environ[i]);
    }
    return 0;
}
```
然后构造一个`myexec`程序，来调用`hello`：
```c
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int main(int argc, char *argv[])
{
    char * const envp[] = {"A=1", "B=2", NULL};
    printf("Entering main \n");
    int ret;
    ret = execle("./hello", "hello", NULL, envp);
    if(ret == -1)
        perror("execl error");
    printf("Existing main \n"); //这句正常情况下不会打印出来的
    return 0;
}
```
执行效果如下所示：
```shell
[root@fqhnode01 c++]# cc hello.c -o hello
[root@fqhnode01 c++]# cc myexec.c -o myexec

[root@fqhnode01 c++]# ./myexec 
Entering main 
hello pid=2170
A=1
B=2
```

## 关于进程、fork、exec的一点思考，Copy-On-Write
首先弄清楚一个进程的组成。

一个进程由PCB、程序段和数据段组成，它在内存中的虚拟地址空间布局如下图所示：

![一个进程的虚拟地址空间](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/一个进程的虚拟地址空间.png)

- 栈，由编译器自动分配释放，存放函数的参数值，`局部变量`的值等。其操作方式类似于数据结构中的栈。 
- 堆，由程序员分配释放，若程序员不释放，程序结束时可能由OS回收。注意它与数据结构中的堆是两回事，分配方式倒是类似于链表。 
- 数据段，是静态存储区。包括BSS段的数据段，BSS段存储未初始化的全局变量、静态变量。而数据段存储经过初始化的`全局和静态变量`。
- 代码段，存放函数体的二进制代码。

前面说到一个进程fork()之后，会基于其本身复制一个子进程，两者互相独立。

这里面的`复制`是如何运作的？用到了`Copy-On-Write`技术。

父进程将代码段，堆，栈，数据段完全复制一份给子进程。也就是说，在子进程运行之初，它拥有父进程的一切变量和句柄。例如，父进程申明了某个hash表，那这个hash表也会被子进程拥有。

但os会让父进程和子进程`共用`一个`代码段`，因为两个进程的程序部分还是相同的。

cpu是以“页”为单位分配空间的，而无论是数据段，还是堆、栈都可能含有很多“页”。fork()在复制`堆，栈，数据段`的时候，只是“逻辑”上的复制，并非“物理”上的复制。 采用`Copy-On-Write`技术。

也就是说，实际执行fork()的时候，在物理空间上，两个进程的`堆，栈，数据段`还是共享着的，内核把这三个区域的访问权限变为只读。 
只有其中一个进程试图修改这些区域的时候，则内核中只为`修改区域的那块内存`制作一个副本，通常是虚拟存储系统中的一”页“。

对于exec，一个进程一旦调用了exec类函数，其本身就”死亡“了。内核会把代码段替换成新的程序代码，废弃原有的`堆，栈，数据段`，并为新进程分配新的堆、栈和数据段。只有进程的属性可以留存下来[#环境变量的传递]。

这也是为什么fork的时候使用Copy-On-Write的原因，为了提高效率。
fork()的实际开销就是复制父进程的页表以及给子进程创建惟一的进程描述符。 
需要注意的是，COW与exec没有必然联系。

## 参考
[fork和exec函数](http://blog.csdn.net/bad_good_man/article/details/49364947)

