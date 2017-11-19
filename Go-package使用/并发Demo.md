# 并发Demo

## 最简单的生产消费者模型
最后借用stopch chan bool来实现优雅 exit
```go
package main

import (
	"fmt"
	"time"
)

func produce(channel chan int, stopch chan bool, args ...int) {
	for _, arg := range args {
		channel <- arg
		fmt.Println(arg)
		time.Sleep(2 * time.Second)
	}
	defer close(channel)
	stopch <- true
}

func custom(channel chan int) {
	//	for {
	//		select {
	//		case x, ok := <-channel:
	//			fmt.Println(x, ok)
	//			if !ok {
	//				return
	//			}
	//		}
	//	}
	for arg := range channel {
		fmt.Println("xx", arg)
	}
}

func main() {
	channel := make(chan int)
	stopch := make(chan bool)
	go produce(channel, stopch, []int{1, 3, 7}...)
	go custom(channel)
	select {
	case <-stopch:
		break
	}
}
```

## 一个泄露的缓冲区

server端和client端共用一个容量为100的buffer `freeList`。

client端从网络io读取信息存放到buffer `freeList`中。 
在buffer `freeList`有空闲的时候，就使用已经分配好的空间，否则就`new`一个。

负责消费client存放到buffer `freeList`中的信息，处理完毕后，将`Buffer b`放回空闲列表`freeList`中直到列表已满, 此时缓冲区将被丢弃, 并被垃圾回收器回收。
select 语句中的 default 子句在没有条件符合时执行,这也就意味着 select 永远不会被阻塞。 
这意味着缓冲区发生了槽位泄露。

```go
var freeList = make(chan *Buffer, 100)
var serverChan = make(chan *Buffer)

func client() {
	for {
		var b *Buffer
		select { // 若缓冲区可用就用它,不可用就分配个新的。
		case b = <-freeList:
			fmt.Println("got a free buffer")
		default:
			// 非空闲,因此分配一个新的。
			b = new(Buffer)
		}
		load(b)         // 从网络中读取下一条消息。
		serverChan <- b // 发送至服务器。
	}
}

func server() {
	for {
		b := <-serverChan // 等待工作。
		process(b)
		select { // 若缓冲区有空间就重用它。
		case freeList <- b:
			// 将缓冲区放到空闲列表中,不做别的。
			/*
				若freeList没有多余的空间了，那么此次执行过程中的b无法存放到freeList中。
				那么将转而执行 default 语句。
				但此次的 b 已经丢失。。。。。。
			*/
		default:
			// 空闲列表已满,保持就好。
		}
	}
}
```