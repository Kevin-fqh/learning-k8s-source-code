# Event机制-2

## 版本说明
本文涉及代码是V1.1.2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [流程](#流程)
  - [Event](#event)
	- [Event的定义](#event的定义)
	- [InvolvedObject属性和Source属性](#involvedobject属性和source属性)

<!-- END MUNGE: GENERATED_TOC -->

## Event生产者
我们从kubelet对event的应用入手，/cmd/kubelet/app/server.go，func RunKubelet(kcfg *KubeletConfig, builder KubeletBuilder) 中
```go
	/*
		新建一个广播事件通道
		创建一个eventBroadcaster（在pkg/client/record/event.go）,
		该对象用于向api server发送kubelet管理pods时的各种事件
	*/
	eventBroadcaster := record.NewBroadcaster()
	/*
		创建eventRecord并且赋值给kubelet cfg，后面会用到，
		eventRecord会把event发送到eventBroadcaster中的watcher。
		此处event源是api.EventSource{Component: "kubelet", Host: kcfg.NodeName}
		记录的event同时以glog.V(3).Infof的日志等级记录下来
	*/
	kcfg.Recorder = eventBroadcaster.NewRecorder(api.EventSource{Component: "kubelet", Host: kcfg.NodeName})
	eventBroadcaster.StartLogging(glog.V(3).Infof)
```
是不是很眼熟，api.EventSource{Component: "kubelet", Host: kcfg.NodeName}记录了此处生成event的是本节点的kubelet组件。

这部分后面的代码如下，定义在pkg/client/record/event.go
```go
// Creates a new event broadcaster.
func NewBroadcaster() EventBroadcaster {
	/*
		watch.NewBroadcaster
		==>定义在 /pkg/watch/mux.go
	*/
	return &eventBroadcasterImpl{watch.NewBroadcaster(maxQueuedEvents, watch.DropIfChannelFull)}
}

// EventBroadcaster knows how to receive events and send them to any EventSink, watcher, or log.
/*
	接收events，把events发到任意一个EventSink, watcher, or log
*/
type EventBroadcaster interface {
	// StartEventWatcher starts sending events received from this EventBroadcaster to the given
	// event handler function. The return value can be ignored or used to stop recording, if
	// desired.
	StartEventWatcher(eventHandler func(*api.Event)) watch.Interface

	// StartRecordingToSink starts sending events received from this EventBroadcaster to the given
	// sink. The return value can be ignored or used to stop recording, if desired.
	StartRecordingToSink(sink EventSink) watch.Interface

	// StartLogging starts sending events received from this EventBroadcaster to the given logging
	// function. The return value can be ignored or used to stop recording, if desired.
	/*
		译：StartLogging 将从EventBroadcaster发送的事件发送到参数指定的日志当中。
		    如果需要，返回值可以被忽略或用于停止记录。
	*/
	StartLogging(logf func(format string, args ...interface{})) watch.Interface

	// NewRecorder returns an EventRecorder that can be used to send events to this EventBroadcaster
	// with the event source set to the given event source.
	/*
		返回一个EventRecorder，可以将events发送到EventBroadcaster，事件源设置为给定的事件源。
	*/
	NewRecorder(source api.EventSource) EventRecorder
}

/*
	type eventBroadcasterImpl struct 实现了type EventBroadcaster interface
*/
type eventBroadcasterImpl struct {
	*watch.Broadcaster
}

// NewRecorder returns an EventRecorder that records events with the given event source.
/*
	NewRecorder返回一个EventRecorder，用于记录与给定事件源的事件。
	传入的参数是source api.EventSource，就是期望记录的event源
*/
func (eventBroadcaster *eventBroadcasterImpl) NewRecorder(source api.EventSource) EventRecorder {
	return &recorderImpl{source, eventBroadcaster.Broadcaster}
}

// EventRecorder knows how to record events on behalf of an EventSource.
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
能看出，这里在启动各种manager失败的时候，生成一个event。关键点找到了。这里就是Event的生产者。查看type recorderImpl struct的Eventf方法
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

	event := makeEvent(ref, reason, message)
	event.Source = recorder.source

	recorder.Action(watch.Added, event)
}
```

