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