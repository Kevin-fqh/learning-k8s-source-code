# kubelet合并三个来源的pod

## 简介
kubelet可以从Apiserver、http 和 file 三个途径获取pod，然后会把所有的pod都汇总到一起，再按照其动作（update、add）来进行具体的操作。 下面我们仿照其实现写个Demo

## util
package util主要提供Until()函数，间隔固定时间来运行一个方法。 
如果输入NeverStop，Until()函数将永远不会退出。

```go
package util

import (
	"fmt"
	"runtime"
	"time"

	"github.com/golang/glog"
)

var ReallyCrash bool

// PanicHandlers is a list of functions which will be invoked when a panic happens.
var PanicHandlers = []func(interface{}){logPanic}

// logPanic logs the caller tree when a panic occurs.
func logPanic(r interface{}) {
	callers := ""
	for i := 0; true; i++ {
		_, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		callers = callers + fmt.Sprintf("%v:%v\n", file, line)
	}
	glog.Errorf("Recovered from panic: %#v (%v)\n%v", r, r, callers)
}

// HandleCrash simply catches a crash and logs an error. Meant to be called via defer.
// Additional context-specific handlers can be provided, and will be called in case of panic
func HandleCrash(additionalHandlers ...func(interface{})) {
	if ReallyCrash {
		return
	}
	if r := recover(); r != nil {
		for _, fn := range PanicHandlers {
			fn(r)
		}
		for _, fn := range additionalHandlers {
			fn(r)
		}
	}
}

// Until loops until stop channel is closed, running f every period.
// Catches any panics, and keeps going. f may not be invoked if
// stop channel is already closed. Pass NeverStop to Until if you
// don't want it stop.
func Until(f func(), period time.Duration, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		func() {
			defer HandleCrash()
			f()
		}()
		time.Sleep(period)
	}
}

// NeverStop may be passed to Until to make it never stop.
var NeverStop <-chan struct{} = make(chan struct{})
```

## merge
package merge 负责根据一个string生成一个channel，形成一个一一对应关系。 
如果提供了多个string，将被视为联合。 
其实并没有真正的合并，多少个channel，就有多少个groutine去调用func (m *Mux) listen

```go
package merge

import (
	"kubelet-collect-pod-demo/src/util"
	"sync"
)

type Merger interface {
	// Invoked when a change from a source is received.  May also function as an incremental
	// merger if you wish to consume changes incrementally.  Must be reentrant when more than
	// one source is defined.
	Merge(source string, update interface{}) error
}

// MergeFunc implements the Merger interface
type MergeFunc func(source string, update interface{}) error

func (f MergeFunc) Merge(source string, update interface{}) error {
	return f(source, update)
}

//Mux就两个东西，一个chan map还有一个Merger的interface。从用法上来看，其实Mux就是一个多生产者的合并
type Mux struct {
	// Invoked when an update is sent to a source.
	merger Merger

	// Sources and their lock.
	sourceLock sync.RWMutex
	// Maps source names to channels
	sources map[string]chan interface{}
}

// NewMux creates a new mux that can merge changes from multiple sources.
func NewMux(merger Merger) *Mux {
	mux := &Mux{
		sources: make(map[string]chan interface{}),
		merger:  merger,
	}
	return mux
}

// Channel returns a channel where a configuration source
// can send updates of new configurations. Multiple calls with the same
// source will return the same channel. This allows change and state based sources
// to use the same channel. Different source names however will be treated as a
// union.
/*
	译：func (m *Mux) Channel将返回一个channel，可以用来更新配置。
		Multiple calls with the same source will return the same channel（根据传进来的参数source string判定）；
		这允许sources的change 和 state 使用相同的channel。

		不同的source names将被视为联合。
		其实并没有真正的合并，多少个channel，就有多少个groutine去调用func (m *Mux) listen

*/
func (m *Mux) Channel(source string) chan interface{} {
	//Channel(source string)方法  就是生产者的注册，并返回生产者传送obj的channel。
	if len(source) == 0 {
		panic("Channel given an empty name")
	}
	m.sourceLock.Lock()
	defer m.sourceLock.Unlock()
	channel, exists := m.sources[source]
	if exists {
		//channel已经存在，直接return
		return channel
	}
	newChannel := make(chan interface{})
	m.sources[source] = newChannel
	/*
		如果channel是新的，起一个groutine
	*/
	go util.Until(func() { m.listen(source, newChannel) }, 0, util.NeverStop)
	return newChannel
}

func (m *Mux) listen(source string, listenChannel <-chan interface{}) {
	/*
		负责调用obj的Merge()进行输出处理
	*/
	for update := range listenChannel {
		m.merger.Merge(source, update)
	}
}
```

## main()
模仿两个生产者file和http, 假设要处理的数据类型是 string
```go
package main

import (
	"fmt"
	"kubelet-collect-pod-demo/src/merge"
	"sync"
)

type testStorage struct {
	update    chan string //要处理的数据类型是string
	updatLock sync.Mutex
}

func (s testStorage) Merge(source string, update interface{}) error {
	s.updatLock.Lock()
	defer s.updatLock.Unlock()
	//	if source == "http" {
	//	}
	st := fmt.Sprintf("source is %s, Got value %s", source, update.(string))
	fmt.Println(st)
	obj, ok := update.(string)
	if !ok {
		fmt.Println("not a string")
		return nil
	}
	s.update <- obj
	return nil
}

func (s testStorage) Customer() {
	for {
		select {
		case st := <-s.update:
			fmt.Println("消费", st)
		}
	}
}

func HttpInput(chHttp chan<- interface{}) {
	//往channel chHttp 输入数据
	chHttp <- "hello"
	chHttp <- "world"
	close(chHttp)
}

func FileInput(chFile chan<- interface{}) {
	//往channel chFile 输入数据
	chFile <- "I"
	chFile <- "am"
	chFile <- "file"
	close(chFile)
}

func main() {
	chupdate := make(chan string, 50)
	storage := testStorage{
		update: chupdate,
	}
	mux := merge.NewMux(storage)
	go HttpInput(mux.Channel("http"))
	go FileInput(mux.Channel("file"))
	go storage.Customer()
	select {}
}
```

输出如下
```
source is file, Got value I
source is file, Got value am
source is file, Got value file
source is http, Got value hello
消费 I
消费 am
消费 file
消费 hello
source is http, Got value world
消费 world
```