# Apiserver端List-Watch机制-2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [WATCHLIST请求](#watchlist请求)
  - [Handler函数ListResource](#handler函数listresource)
  - [type Store struct](#type-store-struct)
  - [Apiserver针对WATCHLIST请求生成一个Watcher](#apiserver针对watchlist请求生成一个watcher)
  - [获取Event转发到订阅者](#获取event转发到订阅者)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

## WATCHLIST请求
想象一下，Apiserver接收到一个来自于其它组件（如kubelet）对一个Pod资源的WATCHLIST请求，其对应的handler函数在哪？ 
Apiserver的处理逻辑是哈？

回头去查看Apiserver的Restful Api构建过程，可以在`/kubernetes-1.5.2/pkg/apiserver/api_installer.go`中，`func (a *APIInstaller) registerResourceHandlers`可以看到下面的逻辑：
```go
case "WATCHLIST": // Watch all resources of a kind.
			doc := "watch individual changes to a list of " + kind
			if hasSubresource {
				doc = "watch individual changes to a list of " + subresource + " of " + kind
			}
			/*
				构造handler函数，重要的部分是ListResource(lister, watcher, reqScope, true, a.minRequestTimeout)函数
			*/
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, ListResource(lister, watcher, reqScope, true, a.minRequestTimeout))
			route := ws.GET(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("watch"+namespaced+kind+strings.Title(subresource)+"List"+operationSuffix).
				Produces(allMediaTypes...).
				Returns(http.StatusOK, "OK", versionedWatchEvent).
				Writes(versionedWatchEvent)
			if err := addObjectParams(ws, route, versionedListOptions); err != nil {
				return nil, err
			}
			addParams(route, action.Params)
			ws.Route(route)
```

### Handler函数ListResource
func ListResource 的核心步骤如下：
1. 调用watcher, err := rw.Watch(ctx, &opts) ，生成一个watcher。关于watcher，每种resource都不一样，需要分别去找。
2. 创建好watcher以后，函数会调用serveWatch(watcher, scope, req, res, timeout)处理传过来的event

```go
// ListResource returns a function that handles retrieving a list of resources from a rest.Storage object.
func ListResource(r rest.Lister, rw rest.Watcher, scope RequestScope, forceWatch bool, minRequestTimeout time.Duration) restful.RouteFunction {
	return func(req *restful.Request, res *restful.Response) {
		// For performance tracking purposes.
		trace := util.NewTrace("List " + req.Request.URL.Path)

		w := res.ResponseWriter

		namespace, err := scope.Namer.Namespace(req)
		if err != nil {
			scope.err(err, res.ResponseWriter, req.Request)
			return
		}

		// Watches for single objects are routed to this function.
		// Treat a /name parameter the same as a field selector entry.
		hasName := true
		_, name, err := scope.Namer.Name(req)
		if err != nil {
			hasName = false
		}

		ctx := scope.ContextFunc(req)
		ctx = api.WithNamespace(ctx, namespace)

		opts := api.ListOptions{}
		if err := scope.ParameterCodec.DecodeParameters(req.Request.URL.Query(), scope.Kind.GroupVersion(), &opts); err != nil {
			scope.err(err, res.ResponseWriter, req.Request)
			return
		}

		// transform fields
		// TODO: DecodeParametersInto should do this.
		if opts.FieldSelector != nil {
			fn := func(label, value string) (newLabel, newValue string, err error) {
				return scope.Convertor.ConvertFieldLabel(scope.Kind.GroupVersion().String(), scope.Kind.Kind, label, value)
			}
			if opts.FieldSelector, err = opts.FieldSelector.Transform(fn); err != nil {
				// TODO: allow bad request to set field causes based on query parameters
				err = errors.NewBadRequest(err.Error())
				scope.err(err, res.ResponseWriter, req.Request)
				return
			}
		}

		if hasName {
			// metadata.name is the canonical internal name.
			// SelectionPredicate will notice that this is
			// a request for a single object and optimize the
			// storage query accordingly.
			nameSelector := fields.OneTermEqualSelector("metadata.name", name)
			if opts.FieldSelector != nil && !opts.FieldSelector.Empty() {
				// It doesn't make sense to ask for both a name
				// and a field selector, since just the name is
				// sufficient to narrow down the request to a
				// single object.
				scope.err(errors.NewBadRequest("both a name and a field selector provided; please provide one or the other."), res.ResponseWriter, req.Request)
				return
			}
			opts.FieldSelector = nameSelector
		}

		if (opts.Watch || forceWatch) && rw != nil {
			/*
				rw rest.Watcher其实就是一个Storage，
				就pod而言，其对应的podStorage生成是在/pkg/registry/core/rest/storage_core.go
				==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
					==>podStorage := podetcd.NewStorage
						==>"pods":             podStorage.Pod,
					此时rw = podStorage.pod

				生成一个watcher
				Watch函数定义在/pkg/registry/generic/registry/store.go
					==>func (e *Store) Watch(ctx api.Context, options *api.ListOptions)
			*/
			watcher, err := rw.Watch(ctx, &opts)
			if err != nil {
				scope.err(err, res.ResponseWriter, req.Request)
				return
			}
			// TODO: Currently we explicitly ignore ?timeout= and use only ?timeoutSeconds=.
			timeout := time.Duration(0)
			if opts.TimeoutSeconds != nil {
				timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
			}
			if timeout == 0 && minRequestTimeout > 0 {
				timeout = time.Duration(float64(minRequestTimeout) * (rand.Float64() + 1.0))
			}
			/*
				创建好watcher以后，函数会调用serveWatch(watcher, scope, req, res, timeout)
				处理传过来的event
			*/
			serveWatch(watcher, scope, req, res, timeout)
			return
		}

		// Log only long List requests (ignore Watch).
		defer trace.LogIfLong(500 * time.Millisecond)
		trace.Step("About to List from storage")
		result, err := r.List(ctx, &opts)
		if err != nil {
			scope.err(err, res.ResponseWriter, req.Request)
			return
		}
		trace.Step("Listing from storage done")
		numberOfItems, err := setListSelfLink(result, req, scope.Namer)
		if err != nil {
			scope.err(err, res.ResponseWriter, req.Request)
			return
		}
		trace.Step("Self-linking done")
		write(http.StatusOK, scope.Kind.GroupVersion(), scope.Serializer, result, w, req.Request)
		trace.Step(fmt.Sprintf("Writing http response done (%d items)", numberOfItems))
	}
}
```
rw rest.Watcher其实就是一个Storage。 

就Resource Pod而言，其对应的podStorage来源于`/pkg/registry/core/rest/storage_core.go`中`func (c LegacyRESTStorageProvider) NewLegacyRESTStorage`
```go
"pods":             podStorage.Pod,
```
此时rw = podStorage.pod

其最后调用的是定义在`/pkg/registry/core/pod/etcd/etcd.go`的`func NewStorage`来生成一个podStorage的。

那么这里调用的Watch函数来自于`/pkg/registry/generic/registry/store.go`的`func (e *Store) Watch(ctx api.Context, options *api.ListOptions)`。这其中关系在[Storage机制]()一文中已经介绍过了。

## type Store struct
接着上面，继续查看`type Store struct`，其定义了各种Resource的公共Restful接口实现。
```go
// Watch makes a matcher for the given label and field, and calls
// WatchPredicate. If possible, you should customize PredicateFunc to produre a
// matcher that matches by key. SelectionPredicate does this for you
// automatically.
/*
	译：func (e *Store) Watch 根据指定的label and field进行匹配，调用WatchPredicate函数。
	   如果可能，应该自定义PredicateFunc。
	   SelectionPredicate 会完成该功能。
*/
func (e *Store) Watch(ctx api.Context, options *api.ListOptions) (watch.Interface, error) {
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}
	field := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		field = options.FieldSelector
	}
	resourceVersion := ""
	if options != nil {
		resourceVersion = options.ResourceVersion
	}
	/*
		调用func (e *Store) WatchPredicate
	*/
	return e.WatchPredicate(ctx, e.PredicateFunc(label, field), resourceVersion)
}

// WatchPredicate starts a watch for the items that m matches.
func (e *Store) WatchPredicate(ctx api.Context, p storage.SelectionPredicate, resourceVersion string) (watch.Interface, error) {
	if name, ok := p.MatchesSingle(); ok {
		if key, err := e.KeyFunc(ctx, name); err == nil {
			if err != nil {
				return nil, err
			}
			/*
				调用e.Storage.Watch(ctx, key, resourceVersion, p)

				e=podStorage.pod, /pkg/registry/core/pod/etcd/etcd.go
				那么e.Storage就是podStorage.pod的Storage，即store.Storage
					==>Storage:     storageInterface,
				所以e.Storage.Watch函数定义在/pkg/storage/cacher.go
				==>func (c *Cacher) Watch(ctx context.Context, key string, resourceVersion string, pred SelectionPredicate)

				开启了cache的时候，e.Storage is: *storage.Cacher
			*/

			w, err := e.Storage.Watch(ctx, key, resourceVersion, p)
			if err != nil {
				return nil, err
			}
			if e.Decorator != nil {
				return newDecoratedWatcher(w, e.Decorator), nil
			}
			return w, nil
		}
		// if we cannot extract a key based on the current context, the optimization is skipped
	}

	w, err := e.Storage.WatchList(ctx, e.KeyRootFunc(ctx), resourceVersion, p)
	if err != nil {
		return nil, err
	}
	if e.Decorator != nil {
		return newDecoratedWatcher(w, e.Decorator), nil
	}
	return w, nil
}
```

## Apiserver针对WATCHLIST请求生成一个Watcher
可以发现`w, err := e.Storage.Watch(ctx, key, resourceVersion, p)`最终调用的是定义在`/pkg/storage/cacher.go`的`func (c *Cacher) Watch`。
分析其流程如下：
1. 传入了一个filterFunction，apiserver的watch是带过滤功能的，就是由这个filter实现的。
2. 调用newCacheWatcher生成一个watcher，
3. 将这个watcher插入到cacher.watchers中去， 也就是说WatchCache中存储着各个订阅者。

```go
// Implements storage.Interface.
func (c *Cacher) Watch(ctx context.Context, key string, resourceVersion string, pred SelectionPredicate) (watch.Interface, error) {
	watchRV, err := ParseWatchResourceVersion(resourceVersion)
	if err != nil {
		return nil, err
	}

	c.ready.wait()

	// We explicitly use thread unsafe version and do locking ourself to ensure that
	// no new events will be processed in the meantime. The watchCache will be unlocked
	// on return from this function.
	// Note that we cannot do it under Cacher lock, to avoid a deadlock, since the
	// underlying watchCache is calling processEvent under its lock.
	c.watchCache.RLock()
	defer c.watchCache.RUnlock()
	initEvents, err := c.watchCache.GetAllEventsSinceThreadUnsafe(watchRV)
	if err != nil {
		// To match the uncached watch implementation, once we have passed authn/authz/admission,
		// and successfully parsed a resource version, other errors must fail with a watch event of type ERROR,
		// rather than a directly returned error.
		return newErrWatcher(err), nil
	}

	triggerValue, triggerSupported := "", false
	// TODO: Currently we assume that in a given Cacher object, any <predicate> that is
	// passed here is aware of exactly the same trigger (at most one).
	// Thus, either 0 or 1 values will be returned.
	if matchValues := pred.MatcherIndex(); len(matchValues) > 0 {
		triggerValue, triggerSupported = matchValues[0].Value, true
	}

	// If there is triggerFunc defined, but triggerSupported is false,
	// we can't narrow the amount of events significantly at this point.
	//
	// That said, currently triggerFunc is defined only for Pods and Nodes,
	// and there is only constant number of watchers for which triggerSupported
	// is false (excluding those issues explicitly by users).
	// Thus, to reduce the risk of those watchers blocking all watchers of a
	// given resource in the system, we increase the sizes of buffers for them.
	chanSize := 10
	if c.triggerFunc != nil && !triggerSupported {
		// TODO: We should tune this value and ideally make it dependent on the
		// number of objects of a given type and/or their churn.
		chanSize = 1000
	}

	c.Lock()
	defer c.Unlock()
	forget := forgetWatcher(c, c.watcherIdx, triggerValue, triggerSupported)
	/*
		传入了一个filterFunction，apiserver的watch是带过滤功能的，就是由这个filter实现的。
		调用newCacheWatcher生成一个watcher，
		并将这个watcher插入到cacher.watchers中去
	*/
	watcher := newCacheWatcher(watchRV, chanSize, initEvents, filterFunction(key, pred), forget)

	c.watchers.addWatcher(watcher, c.watcherIdx, triggerValue, triggerSupported)
	c.watcherIdx++
	return watcher, nil
}
```

- newCacheWatcher函数

```go
func newCacheWatcher(resourceVersion uint64, chanSize int, initEvents []watchCacheEvent, filter filterObjectFunc, forget func(bool)) *cacheWatcher {
	/*
		生成一个新的CacheWatcher
	*/
	watcher := &cacheWatcher{
		input:   make(chan watchCacheEvent, chanSize),
		result:  make(chan watch.Event, chanSize),
		done:    make(chan struct{}),
		filter:  filter,
		stopped: false,
		forget:  forget,
	}
	/*
		每一个Watcher都会有一些协程处理其channel input 消费者
	*/
	go watcher.process(initEvents, resourceVersion)
	return watcher
}
```

## 获取Event转发到订阅者
每一个Watcher都会有一些协程来消费 watchCache 的 channel input。 
其生产者在前面[Apiserver端List-Watch机制-1]()已经介绍过。

```go
func (c *cacheWatcher) process(initEvents []watchCacheEvent, resourceVersion uint64) {
	defer utilruntime.HandleCrash()

	const initProcessThreshold = 500 * time.Millisecond
	startTime := time.Now()
	for _, event := range initEvents {
		c.sendWatchCacheEvent(&event)
	}
	processingTime := time.Since(startTime)
	if processingTime > initProcessThreshold {
		objType := "<null>"
		if len(initEvents) > 0 {
			objType = reflect.TypeOf(initEvents[0].Object).String()
		}
		glog.V(2).Infof("processing %d initEvents of %s took %v", len(initEvents), objType, processingTime)
	}

	defer close(c.result)
	defer c.Stop()
	for {
		event, ok := <-c.input
		/*
			取出channel input，消费者
		*/
		if !ok {
			return
		}
		// only send events newer than resourceVersion
		/*
			结合etcd和Cacher的resourceVersion进行对比，形成一个WatchEvent，分发到各个观察者watcher中
		*/
		if event.ResourceVersion > resourceVersion {
			c.sendWatchCacheEvent(&event)
		}
	}
}

func (c *cacheWatcher) sendWatchCacheEvent(event *watchCacheEvent) {
	/*
		sendWatchCacheEvent会调用c.filter函数对watch的结果进行过滤
	*/
	curObjPasses := event.Type != watch.Deleted && c.filter(event.Key, event.Object)
	oldObjPasses := false
	if event.PrevObject != nil {
		oldObjPasses = c.filter(event.Key, event.PrevObject)
	}
	if !curObjPasses && !oldObjPasses {
		// Watcher is not interested in that object.
		return
	}

	object, err := api.Scheme.Copy(event.Object)
	if err != nil {
		glog.Errorf("unexpected copy error: %v", err)
		return
	}
	/*
		然后将过滤后的结果包装成watchEvent
	*/
	var watchEvent watch.Event
	switch {
	case curObjPasses && !oldObjPasses:
		watchEvent = watch.Event{Type: watch.Added, Object: object}
	case curObjPasses && oldObjPasses:
		watchEvent = watch.Event{Type: watch.Modified, Object: object}
	case !curObjPasses && oldObjPasses:
		watchEvent = watch.Event{Type: watch.Deleted, Object: object}
	}

	// We need to ensure that if we put event X to the c.result, all
	// previous events were already put into it before, no matter whether
	// c.done is close or not.
	// Thus we cannot simply select from c.done and c.result and this
	// would give us non-determinism.
	// At the same time, we don't want to block infinitely on putting
	// to c.result, when c.done is already closed.

	// This ensures that with c.done already close, we at most once go
	// into the next select after this. With that, no matter which
	// statement we choose there, we will deliver only consecutive
	// events.
	select {
	case <-c.done:
		return
	default:
	}

	/*
		然后将过滤后的结果包装成watchEvent，发送到c.result这个channel  生产者
		其对应的消费者在/pkg/apiserver/resthandler.go
			==>serveWatch(watcher, scope, req, res, timeout)
				=>func serveWatch
				==>/pkg/apiserver/watch.go
					==>func (s *WatchServer) ServeHTTP
						==>ch := s.watching.ResultChan()
	*/
	select {
	case c.result <- watchEvent:
	case <-c.done:
	}
}
```

最后再来看看c.result的消费者
```go
// ServeHTTP serves a series of encoded events via HTTP with Transfer-Encoding: chunked
// or over a websocket connection.

func (s *WatchServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w = httplog.Unlogged(w)

	if wsstream.IsWebSocketRequest(req) {
		w.Header().Set("Content-Type", s.mediaType)
		websocket.Handler(s.HandleWS).ServeHTTP(w, req)
		return
	}

	cn, ok := w.(http.CloseNotifier)
	if !ok {
		err := fmt.Errorf("unable to start watch - can't get http.CloseNotifier: %#v", w)
		utilruntime.HandleError(err)
		s.scope.err(errors.NewInternalError(err), w, req)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		err := fmt.Errorf("unable to start watch - can't get http.Flusher: %#v", w)
		utilruntime.HandleError(err)
		s.scope.err(errors.NewInternalError(err), w, req)
		return
	}

	framer := s.framer.NewFrameWriter(w)
	if framer == nil {
		// programmer error
		err := fmt.Errorf("no stream framing support is available for media type %q", s.mediaType)
		utilruntime.HandleError(err)
		s.scope.err(errors.NewBadRequest(err.Error()), w, req)
		return
	}
	e := streaming.NewEncoder(framer, s.encoder)

	// ensure the connection times out
	timeoutCh, cleanup := s.t.TimeoutCh()
	defer cleanup()
	defer s.watching.Stop()

	// begin the stream
	w.Header().Set("Content-Type", s.mediaType)
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var unknown runtime.Unknown
	internalEvent := &versioned.InternalEvent{}
	buf := &bytes.Buffer{}
	/*
		相对pod而言，此时的s.watching实质上是/pkg/storage/cacher.go
			==>type cacheWatcher struct
				在func newCacheWatcher 生成
	*/
	ch := s.watching.ResultChan()
	for {
		select {
		case <-cn.CloseNotify():
			return
		case <-timeoutCh:
			return
		case event, ok := <-ch:
			/*
				从channel result取出event，然后将其序列化，最后通过发送出去  消费者
				到这里就可以说kube-apiserver watch的结果已经发送给订阅方
				订阅方是指kube-controller-manager、proxy、scheduler、kubelet这些组件，向kube-apiserver订阅etcd的信息
			*/
			if !ok {
				// End of results.
				return
			}

			obj := event.Object
			s.fixup(obj)
			if err := s.embeddedEncoder.Encode(obj, buf); err != nil {
				// unexpected error
				utilruntime.HandleError(fmt.Errorf("unable to encode watch object: %v", err))
				return
			}

			// ContentType is not required here because we are defaulting to the serializer
			// type
			unknown.Raw = buf.Bytes()
			event.Object = &unknown

			// the internal event will be versioned by the encoder
			*internalEvent = versioned.InternalEvent(event)
			if err := e.Encode(internalEvent); err != nil {
				utilruntime.HandleError(fmt.Errorf("unable to encode watch object: %v (%#v)", err, e))
				// client disconnect.
				return
			}
			if len(ch) == 0 {
				flusher.Flush()
			}

			buf.Reset()
		}
	}
}
```
至此就可以说kube-apiserver watch的结果已经发送给订阅方。 
订阅方是指kube-controller-manager、proxy、scheduler、kubelet这些组件，向kube-apiserver订阅etcd的信息。

## 总结
kube-apiserver初始化时，建立对etcd的连接，并对etcd进行watch，将watch的结果存入watchCache。
当其他组件需要watch资源时，其他组件向apiserver发送一个watch请求，这个请求是可以带filter函数的。
apiserver针对这个请求会创建一个watcher，并基于watcher创建WatchServer。
watchCache watch的对象，首先会通过filter函数的过滤，假如过滤通过的话，则会通过WatcherServer发送给订阅组件。