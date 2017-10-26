# EtcdWatcher

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type etcdWatcher struct定义](#type-etcdwatcher-struct定义)
  - [新建一个EtcdWatcher](#新建一个etcdwatcher)
  - [etcdWatch函数](#etcdwatch函数)
  - [translate函数](#translate函数)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文会根据event的数据流向来对EtcdWatcher进行学习。

## type etcdWatcher struct定义
```go
// etcdWatcher converts a native etcd watch to a watch.Interface.
type etcdWatcher struct {
	// HighWaterMarks for performance debugging.
	// Important: Since HighWaterMark is using sync/atomic, it has to be at the top of the struct due to a bug on 32-bit platforms
	// See: https://golang.org/pkg/sync/atomic/ for more information
	incomingHWM storage.HighWaterMark
	outgoingHWM storage.HighWaterMark

	encoding runtime.Codec
	// Note that versioner is required for etcdWatcher to work correctly.
	// There is no public constructor of it, so be careful when manipulating
	// with it manually.
	versioner storage.Versioner
	transform TransformFunc

	list    bool // If we're doing a recursive watch, should be true.
	quorum  bool // If we enable quorum, shoule be true
	include includeFunc
	filter  storage.FilterFunc

	etcdIncoming  chan *etcd.Response
	etcdError     chan error
	ctx           context.Context
	cancel        context.CancelFunc
	etcdCallEnded chan struct{}

	outgoing chan watch.Event
	userStop chan struct{}
	stopped  bool
	stopLock sync.Mutex
	// wg is used to avoid calls to etcd after Stop(), and to make sure
	// that the translate goroutine is not leaked.
	wg sync.WaitGroup

	// Injectable for testing. Send the event down the outgoing channel.
	emit func(watch.Event)

	cache etcdCache
}
```

## 新建一个EtcdWatcher
1. 生成一个etcdWatcher w，
2. 有两个比较重要的channel：
   - etcdIncoming，处理从etcd中watch到的Response
   - outgoging，处理etcdIncoming中的Response转化得到的event
3. 定义了一个函数面值emit，负责把event输入channel outgoing
4. 最后会启动一个线程运行w.translate()

注意这里的数据流向如下：  
etcd-->channel etcdIncoming-->channel outgoging
```go
// newEtcdWatcher returns a new etcdWatcher; if list is true, watch sub-nodes.
// The versioner must be able to handle the objects that transform creates.
func newEtcdWatcher(
	list bool, quorum bool, include includeFunc, filter storage.FilterFunc,
	encoding runtime.Codec, versioner storage.Versioner, transform TransformFunc,
	cache etcdCache) *etcdWatcher {
		
	w := &etcdWatcher{
		encoding:  encoding,
		versioner: versioner,
		transform: transform,
		list:      list,
		quorum:    quorum,
		include:   include,
		filter:    filter,
		// Buffer this channel, so that the etcd client is not forced
		// to context switch with every object it gets, and so that a
		// long time spent decoding an object won't block the *next*
		// object. Basically, we see a lot of "401 window exceeded"
		// errors from etcd, and that's due to the client not streaming
		// results but rather getting them one at a time. So we really
		// want to never block the etcd client, if possible. The 100 is
		// mostly arbitrary--we know it goes as high as 50, though.
		// There's a V(2) log message that prints the length so we can
		// monitor how much of this buffer is actually used.
		/*
			缓存channel etcdIncoming，避免etcd client不用每次获取到一个对象就进行上下文切换。
		*/
		etcdIncoming: make(chan *etcd.Response, 100),
		etcdError:    make(chan error, 1),
		// Similarly to etcdIncomming, we don't want to force context
		// switch on every new incoming object.
		outgoing: make(chan watch.Event, 100),
		userStop: make(chan struct{}),
		stopped:  false,
		wg:       sync.WaitGroup{},
		cache:    cache,
		ctx:      nil,
		cancel:   nil,
	}
	/*
		给type etcdWatcher struct的emit赋值
	*/
	w.emit = func(e watch.Event) {
		if curLen := int64(len(w.outgoing)); w.outgoingHWM.Update(curLen) {
			// Monitor if this gets backed up, and how much.
			glog.V(1).Infof("watch (%v): %v objects queued in outgoing channel.", reflect.TypeOf(e.Object).String(), curLen)
		}
		// Give up on user stop, without this we leak a lot of goroutines in tests.
		select {
		/*
			把event输入channel outgoing，生产者
		*/
		case w.outgoing <- e:
		case <-w.userStop:
		}
	}
	// translate will call done. We need to Add() here because otherwise,
	// if Stop() gets called before translate gets started, there'd be a
	// problem.
	w.wg.Add(1)
	go w.translate()
	return w
}
```

## etcdWatch函数
首先来看看之前在`/pkg/storage/etcd/etcd_helper.go`中提到的`etcdWatch`函数
```go
w := newEtcdWatcher(false, h.quorum, nil, storage.SimpleFilter(pred), h.codec, h.versioner, nil, h)
go w.etcdWatch(ctx, h.etcdKeysAPI, key, watchRV)
```

etcdWatch函数负责和etcdhelper打交道，调用etcdhelper的Watch，获取event。

然后把watch到的event传到channel etcdIncoming。
这里完成了etcd-->channel etcdIncoming
```go
// etcdWatch calls etcd's Watch function, and handles any errors. Meant to be called
// as a goroutine.
func (w *etcdWatcher) etcdWatch(ctx context.Context, client etcd.KeysAPI, key string, resourceVersion uint64) {
	defer utilruntime.HandleCrash()
	defer close(w.etcdError)
	defer close(w.etcdIncoming)

	// All calls to etcd are coming from this function - once it is finished
	// no other call to etcd should be generated by this watcher.
	done := func() {}

	// We need to be prepared, that Stop() can be called at any time.
	// It can potentially also be called, even before this function is called.
	// If that is the case, we simply skip all the code here.
	// See #18928 for more details.
	var watcher etcd.Watcher
	returned := func() bool {
		w.stopLock.Lock()
		defer w.stopLock.Unlock()
		if w.stopped {
			// Watcher has already been stopped - don't event initiate it here.
			return true
		}
		w.wg.Add(1)
		done = w.wg.Done
		// Perform initialization of watcher under lock - we want to avoid situation when
		// Stop() is called in the meantime (which in tests can cause etcd termination and
		// strange behavior here).
		/*
			初始化watcher
		*/
		if resourceVersion == 0 {
			latest, err := etcdGetInitialWatchState(ctx, client, key, w.list, w.quorum, w.etcdIncoming)
			fmt.Println("latest is", latest)
			if err != nil {
				w.etcdError <- err
				return true
			}
			resourceVersion = latest
		}

		opts := etcd.WatcherOptions{
			Recursive:  w.list,
			AfterIndex: resourceVersion,
		}
		watcher = client.Watcher(key, &opts)
		w.ctx, w.cancel = context.WithCancel(ctx)
		return false
	}()
	defer done()
	if returned {
		return
	}

	for {
		/*
			Next(context.Context) (*Response, error)会一直阻塞到一个etcd event的出现，
			然后return一个代表该event的Response。
		*/
		resp, err := watcher.Next(w.ctx)
		if err != nil {
			/*
				watch操作出错，channel etcdError 生产者
			*/
			w.etcdError <- err
			return
		}
		/*
			*********************
			把watch到的event传到channel etcdIncoming，生产者
		*/
		w.etcdIncoming <- resp
	}
}
```

## translate函数
func (w *etcdWatcher) translate()定义了一个死循环，用select语句来处理Error、用户主动Stop、正常三种信号。

负责消费etcdIncoming channel的event，调用sendResult，根据event信息中的动作类型(EtcdCreate, EtcdGet...)，进行分发。

这里完成了channel etcdIncoming-->channel outgoging
```go
// translate pulls stuff from etcd, converts, and pushes out the outgoing channel. Meant to be
// called as a goroutine.
func (w *etcdWatcher) translate() {
	defer w.wg.Done()
	defer close(w.outgoing)
	defer utilruntime.HandleCrash()

	for {
		select {
		case err := <-w.etcdError: //ERROR信号，channel etcdError 消费者
			if err != nil {
				var status *unversioned.Status
				switch {
				case etcdutil.IsEtcdWatchExpired(err):
					status = &unversioned.Status{
						Status:  unversioned.StatusFailure,
						Message: err.Error(),
						Code:    http.StatusGone, // Gone
						Reason:  unversioned.StatusReasonExpired,
					}
				// TODO: need to generate errors using api/errors which has a circular dependency on this package
				//   no other way to inject errors
				// case etcdutil.IsEtcdUnreachable(err):
				//   status = errors.NewServerTimeout(...)
				default:
					status = &unversioned.Status{
						Status:  unversioned.StatusFailure,
						Message: err.Error(),
						Code:    http.StatusInternalServerError,
						Reason:  unversioned.StatusReasonInternalError,
					}
				}
				/*
					运行type etcdWatcher struct的emit，这是一个函数面值，在func newEtcdWatcher中赋值了
					把event推送到channel outgoing里面，生产者
				*/
				w.emit(watch.Event{
					Type:   watch.Error,
					Object: status,
				})
			}
			return
		case <-w.userStop: //用户主动stop信号
			return
		case res, ok := <-w.etcdIncoming:
			/*
				**********正常通道***********
				消费channel etcdIncoming，消费者
			*/
			if ok {
				if curLen := int64(len(w.etcdIncoming)); w.incomingHWM.Update(curLen) {
					// Monitor if this gets backed up, and how much.
					glog.V(1).Infof("watch: %v objects queued in incoming channel.", curLen)
				}
				/*
					调用sendResult
				*/
				w.sendResult(res)
			}
			// If !ok, don't return here-- must wait for etcdError channel
			// to give an error or be closed.
		}
	}
}
```

来看看sendResult函数
```go
func (w *etcdWatcher) sendResult(res *etcd.Response) {
	/*
		这几个操作函数都是把res *etcd.Response反序列化为资源对象，
		然后组装成一个event对象，
		然后发送这个event对象到outgoing这个channel上
	*/
	switch res.Action {
	case EtcdCreate, EtcdGet:
		w.sendAdd(res)
	case EtcdSet, EtcdCAS:
		w.sendModify(res)
	case EtcdDelete, EtcdExpire, EtcdCAD:
		w.sendDelete(res)
	default:
		utilruntime.HandleError(fmt.Errorf("unknown action: %v", res.Action))
	}
}
```

以sendAdd函数为例子，可以看出其主要是将来自于etcd的Response反序列化为资源对象，
然后组装成一个event对象，
最后再调用emit函数把event对象发送到channel outgoing。
```go
func (w *etcdWatcher) sendAdd(res *etcd.Response) {
	if res.Node == nil {
		utilruntime.HandleError(fmt.Errorf("unexpected nil node: %#v", res))
		return
	}
	if w.include != nil && !w.include(res.Node.Key) {
		return
	}
	obj, err := w.decodeObject(res.Node)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failure to decode api object: %v\n'%v' from %#v %#v", err, string(res.Node.Value), res, res.Node))
		// TODO: expose an error through watch.Interface?
		// Ignore this value. If we stop the watch on a bad value, a client that uses
		// the resourceVersion to resume will never be able to get past a bad value.
		return
	}
	if !w.filter(obj) {
		return
	}
	action := watch.Added
	if res.Node.ModifiedIndex != res.Node.CreatedIndex {
		action = watch.Modified
	}
	/*
		运行type etcdWatcher struct的emit，这是一个函数面值，在func newEtcdWatcher中赋值了
		把event推送到channel outgoing里面，生产者

		其对应的消费者在/pkg/client/cache/reflector.go
			==>func (r *Reflector) watchHandler(w watch.Interface, resourceVersion *string, errc chan error, stopCh <-chan struct{})
				==>event, ok := <-w.ResultChan()
	*/
	w.emit(watch.Event{
		Type:   action,
		Object: obj,
	})
}
```

关于channel outgoing的消费者是在Reflector中。会在[Apiserver端List-Watch机制]()一文中进行介绍。

最后再来看一下ResultChan函数，负责把outgoing的event输送给Reflector
```go
// ResultChan implements watch.Interface.
func (w *etcdWatcher) ResultChan() <-chan watch.Event {
	return w.outgoing
}
```

## 总结
至此关于etcdWatcher的介绍已经结束了，其主要作用就是充当Reflector和etcd之间的强梁。

可以根据etcd-->channel etcdIncoming-->channel outgoging-->Reflector来进行了解。


