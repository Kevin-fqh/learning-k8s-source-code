# Event机制-3

## 版本说明
本文涉及代码是V1.5.2


**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [EventCorrelator](#eventcorrelator)
  - [event handler函数recordToSink](#event-handler函数recordtosink)
  - [eventCorrelator的EventCorrelate函数](#eventcorrelator的eventcorrelate函数)
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
			*******重点*****
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

// StartLogging starts sending events received from this EventBroadcaster to the given logging function.
// The return value can be ignored or used to stop recording, if desired.
func (eventBroadcaster *eventBroadcasterImpl) StartLogging(logf func(format string, args ...interface{})) watch.Interface {
	return eventBroadcaster.StartEventWatcher(
		func(e *api.Event) {
			logf("Event(%#v): type: '%v' reason: '%v' %v", e.InvolvedObject, e.Type, e.Reason, e.Message)
		})
}
```
这部分和前面V1.1.2是类似的，但是有一个新的概念EventCorrelator。
func NewEventCorrelator用默认值来初始化一个EventCorrelator。
EventCorrelator负责event过滤，聚合和计数，然后与API服务器进行交互以记录event。

EventCorrelator定义包含了三个成员，分别是
- 过滤Events的filterFunc，
- 进行相似Event聚合的aggregator
- 以及记录相同Event的logger。

EventCorrelator负责处理收到的所有Events，并执行聚合等操作以防止大量的Events冲垮整个系统。
EventCorrelator会过滤频繁发生的相似Events来防止系统向用户发送难以区分的信息和执行去重操作，以使相同的Events被压缩为被多次计数单个Event。

EventCorrelator检查每个接收到的Event，并让每个子组件可以访问和修改这个Event。
首先EventAggregator对每个Event进行聚合操作，它基于aggregateKey将Events进行分组，组内区分的唯一标识是localKey。
然后EventLogger会把相同的Event（除了时间戳之外其他字段都相同）变成同一个Event。

aggregator和logger都会在内部维护一个缓存（默认长度是 4096)，
事件的相似性和相同性比较是和缓存中的事件进行的，
也就是说它并不在乎kubelet启动之前的事件。
而且如果事件超过4096的长度，最近没有被访问的事件也会被从缓存中移除。
这也是/pkg/client/record/events_cache.go的文件名带有cache的原因。

Kubernetes的Events可以按照两种方式分类：相同和相似。

相同指的是两个Events除了时间戳以外的其他信息均相同。

相似指的是两个Events除了时间戳和消息(message)以外的其他信息均相同。
按照这个分类方法，为了减少Event流对etcd的冲击，将相同的Events合并计数和将相似的Events聚合，提出“最大努力”的Event压缩算法。
最大努力指的是在最坏的情况下，N个Event仍然会产生N条Event记录。

每个Event对象包含不只一个时间戳域：FirstTimestamp、LastTimestamp，
同时还有统计在FirstTimestamp和LastTimestamp之间出现频次的域Count。
同时对于每个可以产生Events的组件，都需要维持一个生成过的Event的历史记录：通过Least Recently Used Cache实现。
```go
// NewEventCorrelator returns an EventCorrelator configured with default values.
//
// The EventCorrelator is responsible for event filtering, aggregating, and counting
// prior to interacting with the API server to record the event.
//
// The default behavior is as follows:
//   * No events are filtered from being recorded
//   * Aggregation is performed if a similar event is recorded 10 times in a
//     in a 10 minute rolling interval.  A similar event is an event that varies only by
//     the Event.Message field.  Rather than recording the precise event, aggregation
//     will create a new event whose message reports that it has combined events with
//     the same reason.
//   * Events are incrementally counted if the exact same event is encountered multiple
//     times.
/*
	译：func NewEventCorrelator用默认值来初始化一个EventCorrelator。
		EventCorrelator负责event过滤，聚合和计数，然后与API服务器进行交互以记录event。
		默认的行为包括：
			* 所有的Event都不可忽略
			* 如果在10分钟的时间周期内收到一个类似event 10次或10次以上，那么将执行聚合操作。
			  并且创建一个仅有Message区别的新Event。这条Message标识这是一组相似的Events，并且会被后续的Event操作序列处理
			  聚合不是记录精确的event。
			  而是将创建一个新的event，报告基于相同的原因进行event的聚合操作。
			* 如果多次遇到完全相同的Event，Event将被递增计数。
*/
func NewEventCorrelator(clock clock.Clock) *EventCorrelator {
	cacheSize := maxLruCacheEntries
	return &EventCorrelator{
		/*
			默认对于所有的Events均返回false，表示都不可忽略
			目前不做过滤，也就是说所有的事件都要经过后续处理，后面可能会做扩展
		*/
		filterFunc: DefaultEventFilterFunc,
		/*
			如果在最近10分钟出现过10个相似的事件（除了 message 和时间戳之外其他关键字段都相同的事件），
			aggregator 会把它们的 message 设置为"events with common reason combined"，这样它们就完全一样了
		*/
		aggregator: NewEventAggregator(
			cacheSize,                          //大小为4096
			EventAggregatorByReasonFunc,        // 通过相同的Event域来进行分组
			EventAggregatorByReasonMessageFunc, // 生成"根据同样的原因进行分组"消息
			defaultAggregateMaxEvents,          // 每个时间间隔里最多统计10个Events
			defaultAggregateIntervalInSeconds,  // 最大时间间隔为10mins
			clock),
		/*
			logger这个变量的名字有点奇怪，其实它会把相同的事件（除了时间戳之外其他字段都相同）变成同一个事件，
			通过增加事件的 Count 字段来记录该事件发生了多少次。
			经过 aggregator 的事件会在这里变成同一个事件

			EventLogger观察相同的Event，并通过在Cache里与它关联的计数来统计它出现的次数。
		*/
		logger: newEventLogger(cacheSize, clock),
	}
}

// EventCorrelator processes all incoming events and performs analysis to avoid overwhelming the system.  It can filter all
// incoming events to see if the event should be filtered from further processing.  It can aggregate similar events that occur
// frequently to protect the system from spamming events that are difficult for users to distinguish.  It performs de-duplication
// to ensure events that are observed multiple times are compacted into a single event with increasing counts.
/*
	译：EventCorrelator处理所有传入的事件并执行分析以避免系统超负荷。
		它可以过滤所有传入的事件，以查看事件是否应该被进一步处理过滤。
		它可以聚合发生频繁的类似事件，以保护系统免受用户难以区分的垃圾邮件事件的影响。
		它执行重复数据删除，以确保多次观察到的事件被压缩成单个事件，并增加计数。
*/
type EventCorrelator struct {
	// the function to filter the event
	filterFunc EventFilterFunc
	// the object that performs event aggregation
	aggregator *EventAggregator
	// the object that observes events as they come through
	logger *eventLogger
}

// EventAggregator identifies similar events and aggregates them into a single event
/*
	EventAggregator通过EventAggregatroKeyFunc，EventAggregator会将10mins内出现过10次的相似Event进行整合：
		丢弃作为输入的Event，并且创建一个仅有Message区别的新Event。
		这条Message标识这是一组相似的Events，并且会被后续的Event操作序列处理
*/
type EventAggregator struct {
	// 读写锁
	sync.RWMutex

	// The cache that manages aggregation state
	// 存放整合状态的Cache
	cache *lru.Cache

	// The function that groups events for aggregation
	// 用来对Events进行分组的函数
	keyFunc EventAggregatorKeyFunc

	// The function that generates a message for an aggregate event
	// 为整合的Events生成一个message的函数
	messageFunc EventAggregatorMessageFunc

	// The maximum number of events in the specified interval before aggregation occurs
	// 每个时间间隔里可统计的最大Events数
	maxEvents int

	// The amount of time in seconds that must transpire since the last occurrence of a similar event before it's considered new
	// 相同的Events间最大时间间隔以及一个时钟
	maxIntervalInSeconds int

	// clock is used to allow for testing over a time interval
	clock clock.Clock
}

/*
	eventLogger观察相同的Event，并通过在Cache里与它关联的计数来统计它出现的次数。
*/
type eventLogger struct {
	sync.RWMutex
	cache *lru.Cache
	clock clock.Clock
}
```
查看各个默认函数
- 过滤函数
```go
// DefaultEventFilterFunc returns false for all incoming events
func DefaultEventFilterFunc(event *api.Event) bool {
	return false
}
```
- 分组聚合函数
```go
// EventAggregatorKeyFunc is responsible for grouping events for aggregation
// It returns a tuple of the following:
// aggregateKey - key the identifies the aggregate group to bucket this event
// localKey - key that makes this event in the local group
/*
	负责对events进行分组聚合，返回值：
		aggregateKey- 识别该聚合event组的key
		localKey- 一个group内某个event的唯一标识
*/
type EventAggregatorKeyFunc func(event *api.Event) (aggregateKey string, localKey string)

// EventAggregatorByReasonFunc aggregates events by exact match on event.Source, event.InvolvedObject, event.Type and event.Reason
/*
	译：event.Source, event.InvolvedObject, event.Type and event.Reason都符合的event进行聚合

	EventAggregator对每个Event进行聚合操作，它基于aggregateKey将Events进行分组，组内区分的唯一标识是localKey。
	默认的聚合函数EventAggregatorByReasonFunc将event.Message作为localKey，
	使用event.Source、event.InvolvedObject、event.Type和event.Reason一同构成aggregateKey。

	通过 EventAggregatorKeyFunc ，EventAggregator会将10mins内出现过10次的相似Event进行整合：
		丢弃作为输入的Event，并且创建一个仅有Message区别的新Event。
		这条Message标识这是一组相似的Events，并且会被后续的Event操作序列处理。
*/
func EventAggregatorByReasonFunc(event *api.Event) (string, string) {
	return strings.Join([]string{
		event.Source.Component,
		event.Source.Host,
		event.InvolvedObject.Kind,
		event.InvolvedObject.Namespace,
		event.InvolvedObject.Name,
		string(event.InvolvedObject.UID),
		event.InvolvedObject.APIVersion,
		event.Type,
		event.Reason,
	},
		""), event.Message
}
```
- 为整合的Events生成一个message的函数，生成"根据同样的原因进行分组"消息，return
```go
// EventAggregatorMessageFunc is responsible for producing an aggregation message
type EventAggregatorMessageFunc func(event *api.Event) string

// EventAggregratorByReasonMessageFunc returns an aggregate message by prefixing the incoming message
func EventAggregatorByReasonMessageFunc(event *api.Event) string {
	return "(events with common reason combined)"
}
```
- 常量
```go
const (
	maxLruCacheEntries = 4096

	// if we see the same event that varies only by message
	// more than 10 times in a 10 minute period, aggregate the event

	defaultAggregateMaxEvents         = 10
	defaultAggregateIntervalInSeconds = 600
)
```

## event handler函数recordToSink
`func recordToSink`负责把事件发送到apiserver，这里的sink其实就是和apiserver交互的restclient，
event是要发送的事件，eventCorrelator在发送事件之前先对事件进行预处理。

recordToSink 对事件的处理分为两个步骤：
- eventCorrelator.EventCorrelate会对事件做预处理，主要是聚合相同的事件，避免产生的事件过多，增加etcd和apiserver的压力，也会导致查看pod事件很不清晰；
- recordEvent 负责最终把事件发送到 apiserver，它会重试很多次（默认是12次），并且每次重试都有一定时间间隔（默认是10秒钟）。
```go
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
		/*
			第一次重试增加随机性，防止 apiserver 重启的时候所有的事件都在同一时间发送事件
		*/
		if tries == 1 {
			time.Sleep(time.Duration(float64(sleepDuration) * randGen.Float64()))
		} else {
			time.Sleep(sleepDuration)
		}
	}
}

// recordEvent attempts to write event to a sink. It returns true if the event
// was successfully recorded or discarded, false if it should be retried.
// If updateExistingEvent is false, it creates a new event, otherwise it updates
// existing event.
/*
	recordEvent 负责最终把事件发送到 apiserver，
	它会重试很多次（默认是12次），并且每次重试都有一定时间间隔（默认是10秒钟）

	func recordEvent根据eventCorrelator的结果来决定是新建一个事件还是更新已经存在的事件，
	并根据请求的结果决定是否需要重试（返回值为false说明需要重试，返回值为true表明已经操作成功或者忽略请求错误）。

	sink.Create 和 sink.Patch 是自动生成的apiserver的client，
	==>定义在/pkg/client/clientset_generated/internalclientset/typed/core/internalversion/event_expansion.go
		==>func (e *EventSinkImpl) Create(event *api.Event) (*api.Event, error)
*/
func recordEvent(sink EventSink, event *api.Event, patch []byte, updateExistingEvent bool, eventCorrelator *EventCorrelator) bool {
	var newEvent *api.Event
	var err error
	// 更新已经存在的事件
	if updateExistingEvent {
		newEvent, err = sink.Patch(event, patch)
	}
	// Update can fail because the event may have been removed and it no longer exists.
	// 创建一个新的事件
	if !updateExistingEvent || (updateExistingEvent && isKeyNotFoundError(err)) {
		// Making sure that ResourceVersion is empty on creation
		event.ResourceVersion = ""
		newEvent, err = sink.Create(event)
	}
	if err == nil {
		// we need to update our event correlator with the server returned state to handle name/resourceversion
		eventCorrelator.UpdateState(newEvent)
		return true
	}

	// If we can't contact the server, then hold everything while we keep trying.
	// Otherwise, something about the event is malformed and we should abandon it.
	/*
		如果是已知错误，就不要再重试了；
		否则，返回 false，让上层进行重试
	*/
	switch err.(type) {
	case *restclient.RequestConstructionError:
		// We will construct the request the same next time, so don't keep trying.
		glog.Errorf("Unable to construct event '%#v': '%v' (will not retry!)", event, err)
		return true
	case *errors.StatusError:
		if errors.IsAlreadyExists(err) {
			glog.V(5).Infof("Server rejected event '%#v': '%v' (will not retry!)", event, err)
		} else {
			glog.Errorf("Server rejected event '%#v': '%v' (will not retry!)", event, err)
		}
		return true
	case *errors.UnexpectedObjectError:
		// We don't expect this; it implies the server's response didn't match a
		// known pattern. Go ahead and retry.
	default:
		// This case includes actual http transport errors. Go ahead and retry.
	}
	glog.Errorf("Unable to write event: '%v' (may retry after sleeping)", err)
	return false
}
```

## eventCorrelator的EventCorrelate函数
前面提到eventCorrelator.EventCorrelate会对event进行预处理, /pkg/client/record/events_cache.go。可以发现，aggregator先进行聚合操作，然后logger对aggregateEvent进行去重操作。预处理后返回的事件可能是原来的事件，也可能是新创建的事件。
```go
// EventCorrelate filters, aggregates, counts, and de-duplicates all incoming events
/*
	EventCorrelator的主要方法EventCorrelate()，
	每次收到一个Event首先判断它是否可以被跳过(前面提过默认均不可忽略)。
	然后对该Event进行aggregator和logger处理。
*/
func (c *EventCorrelator) EventCorrelate(newEvent *api.Event) (*EventCorrelateResult, error) {
	if c.filterFunc(newEvent) {
		return &EventCorrelateResult{Skip: true}, nil
	}
	aggregateEvent, err := c.aggregator.EventAggregate(newEvent)
	if err != nil {
		return &EventCorrelateResult{}, err
	}
	observedEvent, patch, err := c.logger.eventObserve(aggregateEvent)
	return &EventCorrelateResult{Event: observedEvent, Patch: patch}, err
}

// EventAggregate identifies similar events and groups into a common event if required
/*
	func (e *EventAggregator) EventAggregate负责识别类似的events，在有需要的情况下进行分组
*/
func (e *EventAggregator) EventAggregate(newEvent *api.Event) (*api.Event, error)


// eventObserve records the event, and determines if its frequency should update
/*
	计算event的频率
*/
func (e *eventLogger) eventObserve(newEvent *api.Event) (*api.Event, []byte, error)
```
在Cache里的Key是Event对象除去Timestamp/Counts等剩余部分构成的。下面的任意组合都可以唯一构造Cache里Event唯一的Key：
```
event.Source.Component
event.Source.Host
event.InvolvedObject.Kind
event.InvolvedObject.Namespace
event.InvolvedObject.Name
event.InvolvedObject.UID
event.InvolvedObject.APIVersion
event.Reason
event.Message
```

不管对于EventAggregator或EventLogger，LRU Cache大小仅为4096。
这也意味着当一个组件（比如Kubelet）运行很长时间，并且产生了大量的不重复Event，
先前产生的未被检查的Events并不会让Cache大小继续增长，而是将最老的Event从Cache中排除。

当一个Event被产生，先前产生的Event Cache会被检查:  
- 如果新产生的Event的Key跟先前产生的Event的Key相匹配（意味着前面所有的域都相匹配），那么它被认为是重复的，并且在etcd里已存在的这条记录将被更新。使用PUT方法来更新etcd里存放的这条记录，仅更新它的LastTimestamp和Count域。同时还会更新先前生成的Event Cache里对应记录的Count、LastTimestamp、Name以及新的ResourceVersion。

- 如果新产生的Event的Key并不能跟先前产生的Event相匹配（意味着前面所有的域都不匹配），这个Event将被认为是新的且是唯一的记录，并写入etcd里。使用POST方法来在etcd里创建该记录。对该Event的记录同样被加入到先前生成的Event Cache里。

当然这样还存在一些问题。对于每个组件来说，Event历史都存放在内存里，如果该程序重启，那么历史将被清空。另外，如果产生了大量的唯一Event，旧的Event将从Cache里去除。只有从Cache里去除的Event才会被压缩，同时任何一个此Event的新实例都会在etcd里创建新记录。

## 总结
- 对整个Event的定义和应用进行总结如下：

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
将Events分为相似和相同两类，分别使用EventAggregator和EventLogger进行操作。
EventAggregator将对10分钟内出现10次的Event进行分组，依据是Event的Source、InvolvedObject、Type和Reason域。
EventLogger将相同的Event去重为1个，并通过计数表示它出现的次数。
这样可以避免系统长时间运行时产生的大量Event冲击etcd，或占用大量内存。
EventAggregator和EventLogger采用大小为4096的LRU Cache，存放先前已产生的不重复Events。超出Cache范围的Events会被压缩。

最后通过restclient(eventClient）调用对应的方法，给apiserver发送请求，这个过程如果出错会进行重试。Apiserver接收到事件的请求把数据更新到etcd。


Event 和 kubernetes 中其他的资源不同，它有一个很重要的特性就是它可以丢失。如果某个事件丢了，并不会影响集群的正常工作。事件的重要性远低于集群的稳定性，所以我们看到事件整个流程中如果有错误，会直接忽略这个事件。

事件的另外一个特性是它的数量很多，相比于 pod 或者 deployment 等资源，事件要比它们多很多，而且每次有事件都要对 etcd 进行写操作。整个集群如果不加管理地往 etcd 中写事件，会对 etcd 造成很大的压力，而 etcd 的可用性是整个集群的基础，所以每个组件在写事件之前，会对事件进行汇聚和去重工作，减少最终的写操作。

### 参考
[K8s Events之捉妖记](https://www.kubernetes.org.cn/1195.html)  
[kubelet源码分析:事件处理](http://cizixs.com/2017/06/22/kubelet-source-code-analysis-part4-event)