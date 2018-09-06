# Event机制-2

## 版本说明
本文涉及代码是V1.1.2


**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Event生产者](#event生产者)
  - [EventBroadcaster和Broadcaster](#eventbroadcaster和broadcaster)
    - [EventWatcher](#eventwatcher)
<!-- END MUNGE: GENERATED_TOC -->

## Event生产者
我们从kubelet对event的应用入手，/cmd/kubelet/app/server.go，func RunKubelet(kcfg *KubeletConfig, builder KubeletBuilder) 中
```go
	/*
		新建一个广播事件通道
		创建一个eventBroadcaster,
		该对象用于向api server发送kubelet管理pods时的各种事件

		==>定义在pkg/client/record/event.go
			==>func NewBroadcaster() EventBroadcaster
	*/
	eventBroadcaster := record.NewBroadcaster()
	/*
		创建eventRecord并且赋值给kubelet cfg，后面会用到，
		eventRecord会把event发送到eventBroadcaster中的watcher。
		event源是api.EventSource{Component: "kubelet", Host: kcfg.NodeName}
		记录的event同时以glog.V(3).Infof的日志等级记录下来
	*/
	kcfg.Recorder = eventBroadcaster.NewRecorder(api.EventSource{Component: "kubelet", Host: kcfg.NodeName})
```
是不是很眼熟，api.EventSource{Component: "kubelet", Host: kcfg.NodeName}记录了此处生成event的是本节点的kubelet组件。

最后的eventBroadcaster.StartLogging(glog.V(3).Infof)是将收到的Events交于相应的Log供日志输出。

这部分后面的代码如下，定义在pkg/client/record/event.go
```go
const maxQueuedEvents = 1000

// Creates a new event broadcaster.
func NewBroadcaster() EventBroadcaster {
	/*
		watch.NewBroadcaster方法
		==>定义在 /pkg/watch/mux.go
			==>func NewBroadcaster(queueLength int, fullChannelBehavior FullChannelBehavior) *Broadcaster
	*/
	return &eventBroadcasterImpl{watch.NewBroadcaster(maxQueuedEvents, watch.DropIfChannelFull)}
}

// EventBroadcaster knows how to receive events and send them to any EventSink, watcher, or log.
/*
	接收events，把events发到任意一个EventSink, watcher, or log
*/
type EventBroadcaster interface {
	/*
		将收到的Events交于相应的处理函数，
		同时生成一个eventWatcher
	*/
	StartEventWatcher(eventHandler func(*api.Event)) watch.Interface
	/*
		将收到的Events交于EventSink
	*/
	StartRecordingToSink(sink EventSink) watch.Interface
	/*
		将收到的Events交于相应的Log供日志输出
	*/
	StartLogging(logf func(format string, args ...interface{})) watch.Interface
	/*
		返回一个EventRecorder，可以将events发送到EventBroadcaster，
		event source设置为入参source api.EventSource。

		初始化一个EventRecorder，并向EventBroadcaster发送Events
	*/
	NewRecorder(source api.EventSource) EventRecorder
}

/*
	type eventBroadcasterImpl struct 实现了type EventBroadcaster interface

	Broadcaster 就是广播的意思，主要功能就是把发给它的消息，广播给所有的监听者（watcher）。
	它的实现代码在 pkg/watch/mux.go
	watch.Broadcaster 是一个分发器，内部保存了一个消息队列，可以通过 Watch 创建监听它内部的 worker。
	当有消息发送到队列中，watch.Broadcaster 后台运行的 goroutine 会接收消息并发送给所有的 watcher。
	而每个 watcher 都有一个接收消息的 channel，用户可以通过它的 ResultChan() 获取这个 channel 从中读取数据进行处理。
*/
type eventBroadcasterImpl struct {
	*watch.Broadcaster
}

// NewRecorder returns an EventRecorder that records events with the given event source.
/*
	NewRecorder返回一个EventRecorder，用于记录与给定事件源的事件。
	传入的参数是source api.EventSource，就是记录的event源
*/
func (eventBroadcaster *eventBroadcasterImpl) NewRecorder(source api.EventSource) EventRecorder {
	return &recorderImpl{source, eventBroadcaster.Broadcaster}
}

// EventRecorder knows how to record events on behalf of an EventSource.
/*
	根据EventSource的表现记录相应的event，也就是生成event
	不管是PastEventf()、Eventf()还是Event()最终都指向了函数func (recorder *recorderImpl) generateEvent。
	略有区别的地方是Eventf()调用了Sprintf()来输出Events message，PastEventf()可创建指定时间发生的Events。
*/
type EventRecorder interface {
	// Event constructs an event from the given information and puts it in the queue for sending.
	// 'object' is the object this event is about. Event will make a reference-- or you may also
	// pass a reference to the object directly.
	// 'reason' is the reason this event is generated. 'reason' should be short and unique; it
	// should be in UpperCamelCase format (starting with a capital letter). "reason" will be used
	// to automate handling of events, so imagine people writing switch statements to handle them.
	// You want to make that easy.
	// 'message' is intended to be human readable.
	//
	// The resulting event will be created in the same namespace as the reference object.
	Event(object runtime.Object, reason, message string)

	// Eventf is just like Event, but with Sprintf for the message field.
	Eventf(object runtime.Object, reason, messageFmt string, args ...interface{})

	// PastEventf is just like Eventf, but with an option to specify the event's 'timestamp' field.
	PastEventf(object runtime.Object, timestamp unversioned.Time, reason, messageFmt string, args ...interface{})
}

/*
	type recorderImpl struct实现了type EventRecorder interface
*/
type recorderImpl struct {
	source api.EventSource
	*watch.Broadcaster
}
```


生成一个Recorder之后，kubelet如何使用它？查看/pkg/kubelet/kubelet.go中的func (kl *Kubelet) Run(updates <-chan PodUpdate)，能发现如下用法
```go
	if err := kl.imageManager.Start(); err != nil {
		kl.recorder.Eventf(kl.nodeRef, "KubeletSetupFailed", "Failed to start ImageManager %v", err)
		glog.Errorf("Failed to start ImageManager, images may not be garbage collected: %v", err)
	}

	if err := kl.cadvisor.Start(); err != nil {
		kl.recorder.Eventf(kl.nodeRef, "KubeletSetupFailed", "Failed to start CAdvisor %v", err)
		glog.Errorf("Failed to start CAdvisor, system may not be properly monitored: %v", err)
	}

	if err := kl.containerManager.Start(); err != nil {
		kl.recorder.Eventf(kl.nodeRef, "KubeletSetupFailed", "Failed to start ContainerManager %v", err)
		glog.Errorf("Failed to start ContainerManager, system may not be properly isolated: %v", err)
	}

	if err := kl.oomWatcher.Start(kl.nodeRef); err != nil {
		kl.recorder.Eventf(kl.nodeRef, "KubeletSetupFailed", "Failed to start OOM watcher %v", err)
		glog.Errorf("Failed to start OOM watching: %v", err)
	}
```
可以看出，这里在启动各种manager失败的时候，生成一个event。关键点找到了。这里就是Event的生产者,利用recoder生成了一个event。
在pkg/client/record/event.go，我们查看type recorderImpl struct的Eventf方法，可以发现最后调用的是generateEvent方法。

在generateEvent方法中有两个重要的地方：一方面调用makeEvent方法生成一个Event；另一方面调用了recorder.Action把指定的event分发给所有的watchers。
```go
func (recorder *recorderImpl) Eventf(object runtime.Object, reason, messageFmt string, args ...interface{}) {
	recorder.Event(object, reason, fmt.Sprintf(messageFmt, args...))
}

func (recorder *recorderImpl) Event(object runtime.Object, reason, message string) {
	recorder.generateEvent(object, unversioned.Now(), reason, message)
}

func (recorder *recorderImpl) generateEvent(object runtime.Object, timestamp unversioned.Time, reason, message string) {
	ref, err := api.GetReference(object)
	if err != nil {
		glog.Errorf("Could not construct reference to: '%#v' due to: '%v'. Will not report event: '%v' '%v'", object, err, reason, message)
		return
	}

	/*
		调用makeEvent生成真正的一个event
	*/
	event := makeEvent(ref, reason, message)
	event.Source = recorder.source

	/*
		把指定的event分发给所有的watchers
		定义在/pkg/watch/mux.go
			==>func (m *Broadcaster) Action(action EventType, obj runtime.Object)
	*/
	recorder.Action(watch.Added, event)
}

func makeEvent(ref *api.ObjectReference, reason, message string) *api.Event {
	//时间戳
	t := unversioned.Now()
	namespace := ref.Namespace
	if namespace == "" {
		namespace = api.NamespaceDefault
	}
	/*
		这是最终的生成Event的地方
		属性值和/pkg/api/types.go中的type Event struct的定义一摸一样
	*/
	return &api.Event{
		ObjectMeta: api.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", ref.Name, t.UnixNano()),
			Namespace: namespace,
		},
		InvolvedObject: *ref,
		Reason:         reason,
		Message:        message,
		FirstTimestamp: t,
		LastTimestamp:  t,
		Count:          1,
	}
}
```
查看recorder *recorderImpl的Action方法，
```go
// Action distributes the given event among all watchers.
/*
	把Event送往Broadcaster中的channel incoming，channel incoming的生产者
*/
func (m *Broadcaster) Action(action EventType, obj runtime.Object) {
	m.incoming <- Event{action, obj}
}
```
至此，Event的定义和生产过程都已经说清楚了。我们可以认为拥有EventsRecorder成员的k8s资源都可以产生Events，
如，负责管理注册、注销等NodeController，会将Node的状态变化信息记录为Events。
DeploymentController会记录回滚、扩容等的Events。他们都在ControllerManager启动时被初始化并运行。
与此同时Kubelet除了会记录它本身运行时的Events，比如：无法为Pod挂载卷等，还包含了一系列像docker_manager这样的小单元，它们各司其职，并记录相应的Events。

## EventBroadcaster和Broadcaster
在上面提到了EventBroadcaster有四种方式输出日志。分别是 处理函数handler、EventSink, watcher, or log。
这个EventBroadcaster实际上是调用定义在 /pkg/watch/mux.go中的func NewBroadcaster方法生成的。

Broadcaster 就是广播的意思，主要功能就是把发给它的消息，广播给所有的监听者（watcher）。

EventBroadcaster是type Broadcaster struct的一种实现，查看其定义
```go
// NewBroadcaster creates a new Broadcaster. queueLength is the maximum number of events to queue per watcher.
// It is guaranteed that events will be distributed in the order in which they occur,
// but the order in which a single event is distributed among all of the watchers is unspecified.
/*
	译：NewBroadcaster创建一个新的广播电台。 queueLength是每个watcher队列的最大事件数。
	确保事件按照发生的顺序进行分发，
	但是在所有watcher之间分发单个事件的顺序是未指定的。
*/
func NewBroadcaster(queueLength int, fullChannelBehavior FullChannelBehavior) *Broadcaster {
	m := &Broadcaster{
		watchers:            map[int64]*broadcasterWatcher{},
		incoming:            make(chan Event, incomingQueueLength),
		watchQueueLength:    queueLength,
		fullChannelBehavior: fullChannelBehavior,
	}
	m.distributing.Add(1)
	/*
		运行一个groutine，完成分发event操作，消费channel incoming
	*/
	go m.loop()
	return m
}

const incomingQueueLength = 25

// Broadcaster distributes event notifications among any number of watchers. Every event
// is delivered to every watcher.
/*
	Broadcaster在任何数量的观察者之间分发事件通知。 每个事件都被传递给每个观察者。
*/
type Broadcaster struct {
	lock sync.Mutex

	watchers     map[int64]*broadcasterWatcher
	nextWatcher  int64
	distributing sync.WaitGroup

	incoming chan Event

	// How large to make watcher's channel.
	watchQueueLength int
	// If one of the watch channels is full, don't wait for it to become empty.
	// Instead just deliver it to the watchers that do have space in their
	// channels and move on to the next event.
	// It's more fair to do this on a per-watcher basis than to do it on the
	// "incoming" channel, which would allow one slow watcher to prevent all
	// other watchers from getting new events.
	/*
		译：如果其中一个watch channels已满，请勿等到它变空。
			而是将其传递给channel有空间的watcher，然后开始处理下一个event。
			在watcher基础上做这个比在“incoming”channel上做的要更公平，这将允许一个slow watcher阻止所有其他watcher获得新的event。
	*/
	fullChannelBehavior FullChannelBehavior
}
```
可以看出，每一个EventBroadcaster都包含一堆watcher，而对于每个watcher，都监视同一个长度为1000的Events Queue，由此保证分发时队列按Events发生的时间排序。但是同一个Events发送至Watcher的顺序得不到保证。为了防止短时间内涌入的Events导致来不及处理，每个EventBroadcaster都拥有一个长度为25的接收缓冲队列。定义的最后指定了队列满时的相应操作。

查看其最后的loop操作，channel incoming的消费者，这就和前面channel income的生产者对应上了。

在func (m *Broadcaster) distribute中可以看到是把event填进了watcher的channel result中。
这会和下面介绍的[EventWatcher](#eventwatcher)中提到的eventBroadcaster.StartRecordingToSink和eventBroadcaster.StartLogging(glog.V(3).Infof)的消费watcher 的channel result对应上了。
```go
// loop receives from m.incoming and distributes to all watchers.
/*
	func (m *Broadcaster) loop()从channel m.incoming中获取数据，然后分发给所有的watchers
	channel incoming的消费者
*/
func (m *Broadcaster) loop() {
	// Deliberately not catching crashes here. Yes, bring down the process if there's a
	// bug in watch.Broadcaster.
	for {
		event, ok := <-m.incoming
		if !ok {
			break
		}
		m.distribute(event)
	}
	m.closeAll()
	m.distributing.Done()
}

// distribute sends event to all watchers. Blocking.
func (m *Broadcaster) distribute(event Event) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.fullChannelBehavior == DropIfChannelFull {
		for _, w := range m.watchers {
			select {
			case w.result <- event:
			case <-w.stopped:
			default: // Don't block if the event can't be queued.
			}
		}
	} else {
		for _, w := range m.watchers {
			select {
			case w.result <- event:
			case <-w.stopped:
			}
		}
	}
}
```
### EventWatcher
至此，基本思路已经清晰。那么还有一个问题就是event的watcher是怎么来？在哪里向eventBroadcaster注册的？

EventBroadcaster是个事件广播器，通过 EventRecorder 提供接口，用户可以往Recoder对象里面发送事件，内部把接收到的事件发送给处理函数。
处理函数是可以扩展的，用户可以通过 StartEventWatcher 来编写自己的事件处理逻辑，
kubelet 默认会使用 StartRecordingToSink 和 StartLogging，
也就是说任何一个事件会同时发送给 apiserver，并打印到日志中。

我们还是从/cmd/kubelet/app/server.go，func RunKubelet(kcfg *KubeletConfig, builder KubeletBuilder) 出发，
```go
	eventBroadcaster.StartLogging(glog.V(3).Infof)
	/*
		在Kubelet运行过程初始化EventBroadcaster之后，如果KubeletConfig里的EventClient不为空，就指定对应的EventSink。
		EventSink是一组接口，包含存储Events的Create、Update、Patch方法，实际由对应的Client实现
	*/
	if kcfg.KubeClient != nil {
		//这地方表明kubelet会把自己的事情通知 api server
		glog.V(4).Infof("Sending events to api server.")
		if kcfg.EventRecordQPS == 0.0 {
			/*
				EventRecordQPS默认值是0.0
				eventBroadcaster开始从watcher的result channel中获取event,发送给api server
				==>定义在/pkg/client/record/event.go
					==>func (eventBroadcaster *eventBroadcasterImpl) StartRecordingToSink(sink EventSink) watch.Interface

				kcfg.KubeClient.Events("")命名空间是""

				StartRecordingToSink()方法先根据当前时间生成一个随机数发生器randGen，
				接着实例化一个EventCorrelator（V1.5.2），
				最后将recordToSink()函数作为处理函数(V1.5.2），
				实现了StartEventWatcher。

				StartLogging()类似地将用于输出日志的匿名函数作为处理函数，实现了StartEventWatcher。
			*/
			eventBroadcaster.StartRecordingToSink(kcfg.KubeClient.Events(""))
		} else {
			eventClient := *kcfg.KubeClient
			eventClient.Throttle = util.NewTokenBucketRateLimiter(kcfg.EventRecordQPS, kcfg.EventBurst)
			eventBroadcaster.StartRecordingToSink(eventClient.Events(""))
		}
	} else {
		glog.Warning("No api server defined - no events will be sent to API server.")
	}
```
StartLogging 和 StartRecordingToSink 创建了两个不同的事件处理函数，分别把事件记录到日志和发送给apiserver。
查看StartLogging和StartRecordingToSink的定义
```go
// StartRecordingToSink starts sending events received from the specified eventBroadcaster to the given sink.
// The return value can be ignored or used to stop recording, if desired.
// TODO: make me an object with parameterizable queue length and retry interval
/*
	译：StartRecordingToSink开始把 从指定的eventBroadcaster中接收到的事件 发送给指定的接收器。

		如果需要，返回值可以被忽略或用于停止记录。
		TODO：使我成为一个具有可参数化队列长度和重试间隔的对象
*/
func (eventBroadcaster *eventBroadcasterImpl) StartRecordingToSink(sink EventSink) watch.Interface {
	// The default math/rand package functions aren't thread safe, so create a
	// new Rand object for each StartRecording call.
	randGen := rand.New(rand.NewSource(time.Now().UnixNano()))
	var eventCache *historyCache = NewEventCache()
	/*
		eventBroadcaster.StartEventWatcher中会不断的从watcher的result channel中获取event,
		然后调用func中的recordEvent发送event（见下面的［8］）
	*/
	return eventBroadcaster.StartEventWatcher(
		func(event *api.Event) {
			// Make a copy before modification, because there could be multiple listeners.
			// Events are safe to copy like this.
			/*
				因为同一个Event可能被多个watcher监听，所以在对Events进行处理前，先要拷贝一份备用。
			*/
			eventCopy := *event
			event = &eventCopy

			previousEvent := eventCache.getEvent(event)
			updateExistingEvent := previousEvent.Count > 0
			if updateExistingEvent {
				event.Count = previousEvent.Count + 1
				event.FirstTimestamp = previousEvent.FirstTimestamp
				event.Name = previousEvent.Name
				event.ResourceVersion = previousEvent.ResourceVersion
			}

			/*
				在有限的重试次数里通过recordEvent()方法对该Event进行记录
				recordEvent()方法试着将Event写到对应的EventSink里，如果写成功或可无视的错误将返回true，其他错误返回false。
				如果要写入的Event已经存在，就将它更新，否则创建一个新的Event。
				在这个过程中如果出错，不管是构造新的Event失败，还是服务器拒绝了这个event，都属于可无视的错误，将返回true。
				而HTTP传输错误，或其他不可预料的对象错误，都会返回false，并在上一层函数里进行重试。
				在/pkg/client/record/event.go里指定了单个Event的最大重试次数为12次。
				另外，为了避免在master挂掉之后所有的Event同时重试导致不能同步，
				所以每次重试的间隔时间将随机产生(第一次间隔由前面的随机数发生器randGen生成)。
			*/
			tries := 0
			for {
				//recordEvent中就是向api server发送event的code ［8］
				if recordEvent(sink, event, updateExistingEvent, eventCache) {
					break
				}
				tries++
				if tries >= maxTriesPerEvent {
					glog.Errorf("Unable to write event '%#v' (retry limit exceeded!)", event)
					break
				}
				// Randomize the first sleep so that various clients won't all be
				// synced up if the master goes down.
				if tries == 1 {
					time.Sleep(time.Duration(float64(sleepDuration) * randGen.Float64()))
				} else {
					time.Sleep(sleepDuration)
				}
			}
		})
}

func (eventBroadcaster *eventBroadcasterImpl) StartLogging(logf func(format string, args ...interface{})) watch.Interface {
	return eventBroadcaster.StartEventWatcher(
		func(e *api.Event) {
			logf("Event(%#v): reason: '%v' %v", e.InvolvedObject, e.Reason, e.Message)
		})
}
```
可以发现StartLogging和StartRecordingToSink主要就是调用了StartEventWatcher。

StartRecordingToSink()方法先根据当前时间生成一个随机数发生器randGen，接着实例化一个EventCorrelator，最后将recordToSink()函数作为处理函数，实现了StartEventWatcher。StartLogging()类似地将用于输出日志的匿名函数作为处理函数，实现了StartEventWatcher。

继续查看StartEventWatcher的定义，可以发现其主要的功能有两个，一是将收到的Events交于相应的处理函数，二是生成一个eventWatcher
```go
/*
	StartEventWatcher()首先实例化一个watcher，每个watcher都被塞入该Broadcaster的watcher列表中，
	并且新实例化的watcher只能获得后续的Events，不能获取整个Events历史。
	入队列的时候加锁以保证安全。
	接着启动一个goroutine用来监视Broadcaster发来的Events。
	EventBroadcaster会在分发Event的时候将所有的Events都送入一个ResultChan。
	watcher不断从ResultChan取走每个Event，如果获取过程发送错误，将Crash并记录日志。
	否则在获得该Events后，交于对应的处理函数进行处理，即eventHandler(event)
*/
func (eventBroadcaster *eventBroadcasterImpl) StartEventWatcher(eventHandler func(*api.Event)) watch.Interface {
	/*
		生成一个eventWatcher
		==>/pkg/watch/mux.go
			==>func (m *Broadcaster) Watch()
	*/
	watcher := eventBroadcaster.Watch()
	/*
		监视Broadcaster发来的Events
		启动一个 goroutine，不断从 watcher.ResultChan() 中读取消息，
		然后调用 eventHandler(event) 对事件进行处理。
	*/
	go func() {
		defer util.HandleCrash()
		for {
			watchEvent, open := <-watcher.ResultChan()
			if !open {
				return
			}
			event, ok := watchEvent.Object.(*api.Event)
			if !ok {
				// This is all local, so there's no reason this should
				// ever happen.
				continue
			}
			/*
				这就是go语言的特色，把函数作为参数传进来调用。！！
			*/
			eventHandler(event)
		}
	}()
	return watcher
}

// Watch adds a new watcher to the list and returns an Interface for it.
// Note: new watchers will only receive new events. They won't get an entire history
// of previous events.
/*
	译：func (m *Broadcaster) Watch()在列表中添加一个新的watcher并返回一个接口。
		注意：新的watcher只会收到新的events。 他们不会得到以前的events的整个历史。
*/
func (m *Broadcaster) Watch() Interface {
	m.lock.Lock()
	defer m.lock.Unlock()
	id := m.nextWatcher
	m.nextWatcher++
	w := &broadcasterWatcher{
		result:  make(chan Event, m.watchQueueLength),
		stopped: make(chan struct{}),
		id:      id,
		m:       m,
	}
	m.watchers[id] = w
	return w
}
```
显然，可以发现下一步的重点就是了解在生成watcher时候，构造的eventHandler func(*api.Event)函数。
