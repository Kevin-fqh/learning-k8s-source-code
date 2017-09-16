# Event机制-3

## 版本说明
本文涉及代码是V1.5.2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [EventCorrelator](#eventcorrelator)
  - [总结](#总结)
<!-- END MUNGE: GENERATED_TOC -->


## EventCorrelator
上文中提到`func (eventBroadcaster *eventBroadcasterImpl) StartRecordingToSink`和`func (eventBroadcaster *eventBroadcasterImpl) StartLogging`调用了StartEventWatcher来生成一个EventWatcher。我们查看在V1.5.2中是如何的？
```go
func (eventBroadcaster *eventBroadcasterImpl) StartRecordingToSink(sink EventSink) watch.Interface {
	// The default math/rand package functions aren't thread safe, so create a
	// new Rand object for each StartRecording call.
	/*
		随机生成一个时间间隔
	*/
	randGen := rand.New(rand.NewSource(time.Now().UnixNano()))
	/*
		实例化一个eventCorrelator，它负责处理收到的所有Events，并执行聚合等操作以防止大量的Events冲垮整个系统
	*/
	eventCorrelator := NewEventCorrelator(clock.RealClock{})
	return eventBroadcaster.StartEventWatcher(
		/*
			这里使用recordToSink()函数作为event的处理函数。
			因为同一个Event可能被多个watcher监听，所以在对Events进行处理前，先要拷贝一份备用。
			接着同样使用EventCorrelator对Events进行整理，
			然后在有限的重试次数里通过recordEvent()方法对该Event进行记录。
			recordEvent()方法试着将Event写到对应的EventSink里，如果写成功或可无视的错误将返回true，其他错误返回false。
			如果要写入的Event已经存在，就将它更新，否则创建一个新的Event。
			在这个过程中如果出错，不管是构造新的Event失败，还是服务器拒绝了这个event，都属于可无视的错误，将返回true。
			而HTTP传输错误，或其他不可预料的对象错误，都会返回false，并在上一层函数里进行重试。
			在/pkg/client/record/event.go里指定了单个Event的最大重试次数为12次。
			另外，为了避免在master挂掉之后所有的Event同时重试导致不能同步，
			所以每次重试的间隔时间将随机产生(第一次间隔由前面的随机数发生器randGen生成)。
		*/
		func(event *api.Event) {
			recordToSink(sink, event, eventCorrelator, randGen, eventBroadcaster.sleepDuration)
		})
}

func recordToSink(sink EventSink, event *api.Event, eventCorrelator *EventCorrelator, randGen *rand.Rand, sleepDuration time.Duration) {
	// Make a copy before modification, because there could be multiple listeners.
	// Events are safe to copy like this.
	/*
		因为同一个Event可能被多个watcher监听，所以在对Events进行处理前，先要拷贝一份备用。
	*/
	eventCopy := *event
	event = &eventCopy
	/*
		使用EventCorrelator对Events进行整理
	*/
	result, err := eventCorrelator.EventCorrelate(event)
	if err != nil {
		utilruntime.HandleError(err)
	}
	if result.Skip {
		return
	}
	/*
		在有限的重试次数里通过recordEvent()方法对该Event进行记录
		recordEvent()方法试着将Event写到对应的EventSink里，如果写成功或可无视的错误将返回true，其他错误返回false。
		如果要写入的Event已经存在，就将它更新，否则创建一个新的Event。
		在这个过程中如果出错，不管是构造新的Event失败，还是服务器拒绝了这个event，都属于可无视的错误，将返回true。
		而HTTP传输错误，或其它不可预料的对象错误，都会返回false，并在上一层函数里进行重试。
		在/pkg/client/record/event.go里指定了单个Event的最大重试次数为12次。
		另外，为了避免在master挂掉之后所有的Event同时重试导致不能同步，
		所以每次重试的间隔时间将随机产生(第一次间隔由前面的随机数发生器randGen生成)。
	*/
	tries := 0
	for {
		if recordEvent(sink, result.Event, result.Patch, result.Event.Count > 1, eventCorrelator) {
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
}
```
这部分和前面V1.1.2是类似的，但是有一个新的概念EventCorrelator
```go

```

## 总结
- 对整个Event的定义和应用进行总结如下
Event由Kubernetes的核心组件Kubelet和ControllerManager等产生，用来记录系统一些重要的状态变更。
ControllerManager里包含了一些小controller，比如deployment_controller，它们拥有EventBroadCaster的对象，负责将采集到的Event进行广播。
Kubelet包含一些小的manager，比如docker_manager，它们会通过EventRecorder输出各种Event。
当然，Kubelet本身也拥有EventBroadCaster对象和EventRecorder对象。

EventRecorder通过generateEvent()实际生成各种Event，并将其添加到监视队列。
我们通过kubectl get events看到的NAME并不是Events的真名，而是与该Event相关的资源的名称，真正的Event名称还包含了一个时间戳。
Event对象通过InvolvedObject成员与发生该Event的资源建立关联。

Kubernetes的资源分为“可被描述资源”和“不可被描述资源”。
当我们用kubectl describe {可描述资源}，比如Pod时，除了获取Pod的相应信息，
还会通过FieldSelector获取相应的Event列表。
Kubelet在初始化的时候已经指明了该Event的Source为Kubelet。

EventBroadcaster会将收到的Event交于各个处理函数进行处理。
接收Event的缓冲队列长为25，不停地取走Event并广播给各个watcher。
watcher由StartEventWatcher()实例产生，并被塞入EventBroadcaster的watcher列表里。
后实例化的watcher只能获取后面的Event历史，不能获取全部历史event。
watcher通过recordEvent()方法将Event写入对应的EventSink里，最大重试次数为12次，重试间隔随机生成。

在写入EventSink前，会对所有的Events进行聚合等操作。
将Events分为相同和相似两类，分别使用EventLogger和EventAggregator进行操作。
EventLogger将相同的Event去重为1个，并通过计数表示它出现的次数。
EventAggregator将对10分钟内出现10次的Event进行分组，依据是Event的Source、InvolvedObject、Type和Reason域。
这样可以避免系统长时间运行时产生的大量Event冲击etcd，或占用大量内存。
EventAggregator和EventLogger采用大小为4096的LRU Cache，存放先前已产生的不重复Events。超出Cache范围的Events会被压缩。

### 参考
[K8s Events之捉妖记](https://www.kubernetes.org.cn/1195.html)