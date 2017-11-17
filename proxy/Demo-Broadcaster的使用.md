# kube-proxy对广播机制Broadcaster的使用

和介绍Event时候的Broadcaster不是同一个结构体，但功能是类似的，都是把消息进行广播，发送给所有的订阅者。

## broadcaster

```go
package broadcaster

import (
	"sync"
)

type Listener interface {
	// OnUpdate is invoked when a change is made to an object.
	//listener其实就是一个带有OnUpdate的interface
	OnUpdate(instance interface{})
}

// ListenerFunc receives a representation of the change or object.
type ListenerFunc func(instance interface{})

func (f ListenerFunc) OnUpdate(instance interface{}) {
	f(instance)
}

type Broadcaster struct {
	// Listeners for changes and their lock.
	//Broadcaster(广播事件)就是一个listener的集合
	listenerLock sync.RWMutex
	listeners    []Listener
}

// NewBroadcaster registers a set of listeners that support the Listener interface
// and notifies them all on changes.
/*
	这里的type Broadcaster struct 会被kube-proxy用到
		==>/pkg/proxy/config/config.go
			==>bcaster := config.NewBroadcaster()
*/
func NewBroadcaster() *Broadcaster {

	return &Broadcaster{}
}

//如何使用广播事件Broadcaster？  看add方法和notify方法,注册和通知
// Add registers listener to receive updates of changes.
func (b *Broadcaster) Add(listener Listener) {
	//首先当然是注册listener
	b.listenerLock.Lock()
	defer b.listenerLock.Unlock()
	b.listeners = append(b.listeners, listener)
}

// Notify notifies all listeners.
func (b *Broadcaster) Notify(instance interface{}) {
	//然后就是事件通知了。
	b.listenerLock.RLock()
	listeners := b.listeners
	b.listenerLock.RUnlock()
	for _, listener := range listeners {
		listener.OnUpdate(instance)
	}
}
```

## main()
```go
package main

import (
	"fmt"
	"proxy-broadcaster-demo/broadcaster"
)

func main() {
	b := broadcaster.NewBroadcaster()
	//	b.Notify(struct{}{})

	ch := make(chan bool, 2)
	b.Add(broadcaster.ListenerFunc(func(object interface{}) {
		fmt.Println("I am listener one, get value: ", object)
		ch <- true
	}))
	b.Add(broadcaster.ListenerFunc(func(object interface{}) {
		fmt.Println("I am listener two, get value: ", object)
		ch <- true
	}))
	b.Notify("hello")
	<-ch
	<-ch
}
```

输出如下
```
I am listener one, get value:  hello
I am listener two, get value:  hello
```