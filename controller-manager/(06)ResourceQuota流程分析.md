# ResourceQuota流程分析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引子](#引子)
  - [获取所有资源的Evaluator](#获取所有资源的evaluator)
    - [PodEvaluator](#podevaluator)
  - [NewResourceQuotaController](#newresourcequotacontroller)
  - [ResourceQuotaController.Run函数](#resourcequotacontroller.run函数)
  - [func worker流程](#func-worker流程)
    - [syncResourceQuotaFromKey和syncResourceQuota](#syncresourcequotafromkey和syncresourcequota)
  - [replenishmentController](#replenishmentcontroller)
    - [以pod资源为例子](#以pod资源为例子)
    - [QuotaPod](#quotapod)
  - [总结](#总结)
<!-- END MUNGE: GENERATED_TOC -->

resourceQuotaController的主要任务就是维护resourceQuota对象的更新，同时也要能够根据pod、svc这些对象的变化来更新对应的resourceQuota对象。

## 引子
和其它controller的启动一样，先看看kube-controller-manager是如何新建一个resourceQuotaController并启动的。 见 /kubernetes-1.5.2/cmd/kube-controller-manager/app/controllermanager.go

分析其流程如下：
1. quotainstall.NewRegistry，初始化各种资源对应的evaluator，以一个map变量形式返回。
2. NewResourceQuotaController创建一个ResourceQuotaController，并执行其Run函数。

```go
resourceQuotaControllerClient := client("resourcequota-controller")
	/*
		nil？
		nil 意味着每次都直接查询pod/pvc资源，而不是使用sharedInformers来进行查询
		/pkg/quota/install/registry.go
			==>func NewRegistry(kubeClient clientset.Interface, f informers.SharedInformerFactory) quota.Registry
	*/
	//resourceQuotaRegistry := quotainstall.NewRegistry(resourceQuotaControllerClient, nil)
	resourceQuotaRegistry := quotainstall.NewRegistry(resourceQuotaControllerClient, sharedInformers)
	// 定义了需要监控的6种资源对象
	groupKindsToReplenish := []unversioned.GroupKind{
		api.Kind("Pod"),
		api.Kind("Service"),
		api.Kind("ReplicationController"),
		api.Kind("PersistentVolumeClaim"),
		api.Kind("Secret"),
		api.Kind("ConfigMap"),
	}
	resourceQuotaControllerOptions := &resourcequotacontroller.ResourceQuotaControllerOptions{
		KubeClient:                resourceQuotaControllerClient,
		ResyncPeriod:              controller.StaticResyncPeriodFunc(s.ResourceQuotaSyncPeriod.Duration),
		Registry:                  resourceQuotaRegistry,
		/*
			/pkg/controller/resourcequota/replenishment_controller.go
				==>func NewReplenishmentControllerFactory
			replenishmentController用来捕获对应资源的Update/Delete事件，
			将其对应的ResourceQuota加入到queue中，然后worker会对其进行重新计算Usage
		*/
		ControllerFactory:         resourcequotacontroller.NewReplenishmentControllerFactory(sharedInformers, resourceQuotaControllerClient),
		ReplenishmentResyncPeriod: ResyncPeriod(s),
		GroupKindsToReplenish:     groupKindsToReplenish,
	}
	go resourcequotacontroller.NewResourceQuotaController(resourceQuotaControllerOptions).Run(int(s.ConcurrentResourceQuotaSyncs), wait.NeverStop)
```

## 获取所有资源的Evaluator
quotainstall.NewRegistry(resourceQuotaControllerClient, sharedInformers)初始化各种资源对应的evaluator，以一个map变量形式返回。

其入参sharedInformers如果改为nil，则意味着每次都直接查询pod/pvc资源，而不是使用sharedInformers来进行查询。

```go
// NewRegistry returns a registry of quota evaluators.
// If a shared informer factory is provided, it is used by evaluators rather than performing direct queries.
/*
	译：NewRegistry返回配额评估程序的注册表。
		如果提供了一个共享的informer factory，则由evaluators使用该共享的informer factory，而不是直接查询。
*/
func NewRegistry(kubeClient clientset.Interface, f informers.SharedInformerFactory) quota.Registry {
	// TODO: when quota supports resources in other api groups, we will need to merge
	/*
		/pkg/quota/evaluator/core/registry.go
		==>func NewRegistry
	*/
	return core.NewRegistry(kubeClient, f)
}
```

继续查看`core.NewRegistry(kubeClient, f)`，可以发现只有`pod和pvc`的配额项使用`f informers.SharedInformerFactory`来获取。 其它资源都是直接通过Apiserver的client接口来获取的。
```go
// NewRegistry returns a registry that knows how to deal with core kubernetes resources
// If an informer factory is provided, evaluators will use them.
/*
	译：NewRegistry返回一个知道如何处理核心kubernetes资源的注册表。
		如果提供了informer factory，evaluators将使用它。
*/
func NewRegistry(kubeClient clientset.Interface, f informers.SharedInformerFactory) quota.Registry {
	/*
		只有pod和pvc 的配额项使用f informers.SharedInformerFactory来获取
	*/
	pod := NewPodEvaluator(kubeClient, f)
	service := NewServiceEvaluator(kubeClient)
	replicationController := NewReplicationControllerEvaluator(kubeClient)
	resourceQuota := NewResourceQuotaEvaluator(kubeClient)
	secret := NewSecretEvaluator(kubeClient)
	configMap := NewConfigMapEvaluator(kubeClient)
	persistentVolumeClaim := NewPersistentVolumeClaimEvaluator(kubeClient, f)
	return &generic.GenericRegistry{
		InternalEvaluators: map[unversioned.GroupKind]quota.Evaluator{
			pod.GroupKind():                   pod,
			service.GroupKind():               service,
			replicationController.GroupKind(): replicationController,
			secret.GroupKind():                secret,
			configMap.GroupKind():             configMap,
			resourceQuota.GroupKind():         resourceQuota,
			persistentVolumeClaim.GroupKind(): persistentVolumeClaim,
		},
	}
}
```
介绍一下PodEvaluator
### PodEvaluator
PodEvaluator是一个type GenericEvaluator struct对象
```go
// NewPodEvaluator returns an evaluator that can evaluate pods
// if the specified shared informer factory is not nil, evaluator may use it to support listing functions.
/*
	译：func NewPodEvaluator返回一个evaluator来对pod的使用情况进行评估。
		如果指定的SharedInformerFactory不为nil，evaluator会使用它来支持listing功能。
		默认是不为nil的
*/
func NewPodEvaluator(kubeClient clientset.Interface, f informers.SharedInformerFactory) quota.Evaluator {
	computeResources := []api.ResourceName{
		api.ResourceCPU,
		api.ResourceMemory,
		api.ResourceRequestsCPU,
		api.ResourceRequestsMemory,
		api.ResourceLimitsCPU,
		api.ResourceLimitsMemory,
	}
	allResources := append(computeResources, api.ResourcePods)
	listFuncByNamespace := listPodsByNamespaceFuncUsingClient(kubeClient)
	if f != nil {
		/*
			默认通道
			重新定义了list方法
			定义在 pkg/quota/generic/evaluator.go
				==>func ListResourceUsingInformerFunc
		*/
		listFuncByNamespace = generic.ListResourceUsingInformerFunc(f, unversioned.GroupResource{Resource: "pods"})
	}
	return &generic.GenericEvaluator{
		Name:              "Evaluator.Pod",
		InternalGroupKind: api.Kind("Pod"),
		InternalOperationResources: map[admission.Operation][]api.ResourceName{
			admission.Create: allResources,
			// TODO: the quota system can only charge for deltas on compute resources when pods support updates.
			// admission.Update: computeResources,
		},
		GetFuncByNamespace: func(namespace, name string) (runtime.Object, error) {
			return kubeClient.Core().Pods(namespace).Get(name)
		},
		ConstraintsFunc:      PodConstraintsFunc,
		MatchedResourceNames: allResources,
		MatchesScopeFunc:     PodMatchesScopeFunc,
		UsageFunc:            PodUsageFunc,
		ListFuncByNamespace:  listFuncByNamespace,
	}
}
```

## NewResourceQuotaController
新建了一个type ResourceQuotaController struct对象，其分析方法和“ReplicationManager中管理rc和pod资源”是类似的，都是通过一个controller来封装了Reflector机制，会触发相应的handler方法，然后运行几个worker协程来负责sync。 只不过这里List-watch的是ResourceQuota资源，&api.ResourceQuota{}。

需要注意的是，这里多了一个replenishmentController，用来捕获对应资源的Update/Delete事件，将其对应的ResourceQuota加入到queue中，然后Run函数中的worker会对其进行重新计算Usage。

```go
func NewResourceQuotaController(options *ResourceQuotaControllerOptions) *ResourceQuotaController {
	// build the resource quota controller
	rq := &ResourceQuotaController{
		kubeClient:               options.KubeClient,
		queue:                    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resourcequota_primary"),
		missingUsageQueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resourcequota_priority"),
		resyncPeriod:             options.ResyncPeriod,
		registry:                 options.Registry,
		replenishmentControllers: []cache.ControllerInterface{},
	}
	if options.KubeClient != nil && options.KubeClient.Core().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("resource_quota_controller", options.KubeClient.Core().RESTClient().GetRateLimiter())
	}
	// set the synchronization handler
	rq.syncHandler = rq.syncResourceQuotaFromKey

	// build the controller that observes quota
	/*
		这里和“ReplicationManager中管理rc和pod资源”是类似的
		通过一个controller来封装了Reflector机制

		这里List-watch的是ResourceQuota资源
		
		rqController负责watch待sync的ResourceQuota resource，
		触发相应的handler方法，把该quota resource加入到rq.queue和rq.missingUsageQueue中
		类似与rc、pod这些resource
	*/
	rq.rqIndexer, rq.rqController = cache.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return rq.kubeClient.Core().ResourceQuotas(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return rq.kubeClient.Core().ResourceQuotas(api.NamespaceAll).Watch(options)
			},
		},
		&api.ResourceQuota{},
		rq.resyncPeriod(),
		cache.ResourceEventHandlerFuncs{
			/*
				注册了Quota变化时触发的方法
			*/
			AddFunc: rq.addQuota,
			UpdateFunc: func(old, cur interface{}) {
				// We are only interested in observing updates to quota.spec to drive updates to quota.status.
				// We ignore all updates to quota.Status because they are all driven by this controller.
				// IMPORTANT:
				// We do not use this function to queue up a full quota recalculation.  To do so, would require
				// us to enqueue all quota.Status updates, and since quota.Status updates involve additional queries
				// that cannot be backed by a cache and result in a full query of a namespace's content, we do not
				// want to pay the price on spurious status updates.  As a result, we have a separate routine that is
				// responsible for enqueue of all resource quotas when doing a full resync (enqueueAll)
				oldResourceQuota := old.(*api.ResourceQuota)
				curResourceQuota := cur.(*api.ResourceQuota)
				if quota.Equals(curResourceQuota.Spec.Hard, oldResourceQuota.Spec.Hard) {
					return
				}
				rq.addQuota(curResourceQuota)
			},
			// This will enter the sync loop and no-op, because the controller has been deleted from the store.
			// Note that deleting a controller immediately after scaling it to 0 will not work. The recommended
			// way of achieving this is by performing a `stop` operation on the controller.
			DeleteFunc: rq.enqueueResourceQuota,
		},
		cache.Indexers{"namespace": cache.MetaNamespaceIndexFunc},
	)

	/*
		针对前面声明的6种resource，分别新建一个replenishmentController，随后都append到rq.replenishmentControllers中
	*/
	for _, groupKindToReplenish := range options.GroupKindsToReplenish {
		controllerOptions := &ReplenishmentControllerOptions{
			GroupKind:         groupKindToReplenish,
			ResyncPeriod:      options.ReplenishmentResyncPeriod,
			ReplenishmentFunc: rq.replenishQuota,
		}
		/*
			replenishmentController用来捕获对应资源的Update/Delete事件，
			将其对应的ResourceQuota加入到queue中，
			然后Run函数中的worker会对其进行重新计算Usage
			==>/pkg/controller/resourcequota/replenishment_controller.go
				==>func (r *replenishmentControllerFactory) NewController
				==>实质上就是返回一个PodInformer（和分析rc资源时候的PodInformer是一样的)

			Informer中注册对应的ResourceEventHandlerFuncs：UpdateFunc和DeleteFunc用来出watch的对象发生对应的change时需要调用的方法。
		*/
		replenishmentController, err := options.ControllerFactory.NewController(controllerOptions)
		if err != nil {
			glog.Warningf("quota controller unable to replenish %s due to %v, changes only accounted during full resync", groupKindToReplenish, err)
		} else {
			rq.replenishmentControllers = append(rq.replenishmentControllers, replenishmentController)
		}
	}
	return rq
}
```

## ResourceQuotaController.Run函数
1. 启动rq.rqController，其实就是完成了一个List-Watch功能，把资源resourcequota存放到queue和missingUsageQueue中
2. 启动rq.replenishmentControllers中的6中replenishmentController
3. 启动一定数量的worker协程，分别对queue和missingUsageQueue中的Item进行处理
4. 定期的进行全量的quota计算

```go
// Run begins quota controller using the specified number of workers
func (rq *ResourceQuotaController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	/*
		启动rqController，负责watch对应的ResourceQuota加入到queue和missingUsageQueue。
	*/
	go rq.rqController.Run(stopCh)
	// the controllers that replenish other resources to respond rapidly to state changes
	/*
		启动rq.replenishmentControllers中的6中replenishmentController
	*/
	for _, replenishmentController := range rq.replenishmentControllers {
		go replenishmentController.Run(stopCh)
	}
	// the workers that chug through the quota calculation backlog
	/*
		启动一定数量的worker协程，分别对queue和missingUsageQueue中的Item进行处理。
		默认配置是5个,可以通过--concurrent-resource-quota-syncs设置
	*/
	for i := 0; i < workers; i++ {
		go wait.Until(rq.worker(rq.queue), time.Second, stopCh) //一次worker流程结束后，间隔1s中继续下一次
		go wait.Until(rq.worker(rq.missingUsageQueue), time.Second, stopCh)
	}
	// the timer for how often we do a full recalculation across all quotas
	/*
		定期的进行全量的quota计算。
	*/
	go wait.Until(func() { rq.enqueueAll() }, rq.resyncPeriod(), stopCh)
	<-stopCh
	glog.Infof("Shutting down ResourceQuotaController")
	rq.queue.ShutDown()
}
```

关于第一部分几个controller的Run函数的分析和rc资源的分析是类似的。  

那么后期的工作就由rq.worker来完成了。
## func worker流程
其核心是调用 func (rq *ResourceQuotaController) syncResourceQuotaFromKey(key string) (err error)
```go
// worker runs a worker thread that just dequeues items, processes them, and marks them done.
func (rq *ResourceQuotaController) worker(queue workqueue.RateLimitingInterface) func() {
	workFunc := func() bool {
		/*
			从入参queue中获取Key
				==>/pkg/util/workqueue/queue.go
					==>func (q *Type) Get() (item interface{}, shutdown bool)
		*/
		key, quit := queue.Get()
		if quit {
			return true
		}
		defer queue.Done(key)
		/*
			核心，调用 func (rq *ResourceQuotaController) syncResourceQuotaFromKey(key string) (err error)
		*/
		err := rq.syncHandler(key.(string))
		if err == nil {
			queue.Forget(key)
			return false
		}
		utilruntime.HandleError(err)
		queue.AddRateLimited(key)
		return false
	}

	return func() {
		for {
			if quit := workFunc(); quit {
				glog.Infof("resource quota controller worker shutting down")
				return
			}
		}
	}
}
```

### syncResourceQuotaFromKey和syncResourceQuota
```go
// syncResourceQuotaFromKey syncs a quota key
/*
	对一个quota resource进行sync
*/
func (rq *ResourceQuotaController) syncResourceQuotaFromKey(key string) (err error) {
	startTime := time.Now()
	defer func() {
		glog.V(4).Infof("Finished syncing resource quota %q (%v)", key, time.Now().Sub(startTime))
	}()

	/*
		根据key从rqIndexer中得到api.ResourceQuota对象: quota，
		然后执行rq.syncResourceQuota(quota)
	*/
	obj, exists, err := rq.rqIndexer.GetByKey(key)
	if !exists {
		glog.Infof("Resource quota has been deleted %v", key)
		return nil
	}
	if err != nil {
		/*
			如果从rqIndexer中取值出错，把该key重新放入queue中
		*/
		glog.Infof("Unable to retrieve resource quota %v from store: %v", key, err)
		rq.queue.Add(key)
		return err
	}
	quota := *obj.(*api.ResourceQuota)
	return rq.syncResourceQuota(quota)
}
```

继续查看`func (rq *ResourceQuotaController) syncResourceQuota(resourceQuota api.ResourceQuota)`，其流程如下：

1. 计算得到资源的最新的已使用情况，得到newUsage
2. 判断该resourceQuota resource是否被修改过（dirty=true），第一次sync的时候默认为dirty
3. 如果dirty=true，通过kubeClient给kube-apisever发送请求，更新etcd中对应的resourcequotas对象的status信息。

```go
// syncResourceQuota runs a complete sync of resource quota status across all known kinds
func (rq *ResourceQuotaController) syncResourceQuota(resourceQuota api.ResourceQuota) (err error) {
	// quota is dirty if any part of spec hard limits differs from the status hard limits
	/*
		该quota resource是dirty的，如果spec.hard limits != status.hard limits
	*/
	dirty := !api.Semantic.DeepEqual(resourceQuota.Spec.Hard, resourceQuota.Status.Hard)

	// dirty tracks if the usage status differs from the previous sync,
	// if so, we send a new usage with latest status
	// if this is our first sync, it will be dirty by default, since we need track usage
	/*
		如果是第一次sync(resourceQuota.Status.Hard == nil || resourceQuota.Status.Used == nil)，
		该quota resource默认是dirty的。
	*/
	dirty = dirty || (resourceQuota.Status.Hard == nil || resourceQuota.Status.Used == nil)

	used := api.ResourceList{}
	if resourceQuota.Status.Used != nil {
		used = quota.Add(api.ResourceList{}, resourceQuota.Status.Used)
	}
	hardLimits := quota.Add(api.ResourceList{}, resourceQuota.Spec.Hard)

	/*
		根据namespace, quota的Scope，hardLimits，registry对该Item（resourceQuota）进行CalculateUsage
			==>/pkg/quota/resources.go
				==>func CalculateUsage
		计算得到资源的最新的已使用情况
	*/
	newUsage, err := quota.CalculateUsage(resourceQuota.Namespace, resourceQuota.Spec.Scopes, hardLimits, rq.registry)
	if err != nil {
		return err
	}
	for key, value := range newUsage {
		used[key] = value
	}

	// ensure set of used values match those that have hard constraints
	hardResources := quota.ResourceNames(hardLimits)
	used = quota.Mask(used, hardResources)

	// Create a usage object that is based on the quota resource version that will handle updates
	// by default, we preserve the past usage observation, and set hard to the current spec
	usage := api.ResourceQuota{
		ObjectMeta: api.ObjectMeta{
			Name:            resourceQuota.Name,
			Namespace:       resourceQuota.Namespace,
			ResourceVersion: resourceQuota.ResourceVersion,
			Labels:          resourceQuota.Labels,
			Annotations:     resourceQuota.Annotations},
		Status: api.ResourceQuotaStatus{
			Hard: hardLimits,
			Used: used,
		},
	}

	/*
		用刚计算得到的newUsage和上一次sync记录的resourceQuota.Status.Used进行对比，
		如果不同，该quota resource是dirty的
	*/
	dirty = dirty || !quota.Equals(usage.Status.Used, resourceQuota.Status.Used)

	// there was a change observed by this controller that requires we update quota
	/*
		如果是dirty的，通过kubeClient给kube-apisever发送请求，
		更新etcd中对应的resourcequotas对象的status信息。
		至此，一个worker的流程结束
	*/
	if dirty {
		_, err = rq.kubeClient.Core().ResourceQuotas(usage.Namespace).UpdateStatus(&usage)
		return err
	}
	return nil
}
```

## replenishmentController
replenishmentController就是用来捕获对应资源的Update/Delete事件，将其对应的ResourceQuota加入到`queue`中，然后worker会再对其进行重新计算Usage。

在前面新建ResourceQuotaController的时候，其中有`replenishmentController, err := options.ControllerFactory.NewController(controllerOptions)`。

NewController的作用是根据不同的资源类型，返回对应的Controller。 每种资源的Controller的定义都是通过创建一个对应的Informer完成。 

Informer中注册对应的ResourceEventHandlerFuncs：UpdateFunc和DeleteFunc用来出watch的对象发生对应的change时需要调用的方法。

```go
func (r *replenishmentControllerFactory) NewController(options *ReplenishmentControllerOptions) (result cache.ControllerInterface, err error) {
	/*
		根据不同的资源类型，返回对应的Controller。
		而每种资源的Controller的定义都是通过创建一个对应的Informer完成。
		Informer中注册对应的ResourceEventHandlerFuncs：
			UpdateFunc和DeleteFunc用来出watch的对象发生对应的change时需要调用的方法。
	*/
	if r.kubeClient != nil && r.kubeClient.Core().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("replenishment_controller", r.kubeClient.Core().RESTClient().GetRateLimiter())
	}

	switch options.GroupKind {
	case api.Kind("Pod"):
		if r.sharedInformerFactory != nil {
			result, err = controllerFor(api.Resource("pods"), r.sharedInformerFactory, cache.ResourceEventHandlerFuncs{
				UpdateFunc: PodReplenishmentUpdateFunc(options),
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			})
			break
		}
		result = informers.NewPodInformer(r.kubeClient, options.ResyncPeriod())
	case api.Kind("Service"):
		// TODO move to informer when defined
		_, result = cache.NewInformer(
			&cache.ListWatch{
				ListFunc: func(options api.ListOptions) (runtime.Object, error) {
					return r.kubeClient.Core().Services(api.NamespaceAll).List(options)
				},
				WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
					return r.kubeClient.Core().Services(api.NamespaceAll).Watch(options)
				},
			},
			&api.Service{},
			options.ResyncPeriod(),
			cache.ResourceEventHandlerFuncs{
				UpdateFunc: ServiceReplenishmentUpdateFunc(options),
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			},
		)
	case api.Kind("ReplicationController"):
		// TODO move to informer when defined
		_, result = cache.NewInformer(
			&cache.ListWatch{
				ListFunc: func(options api.ListOptions) (runtime.Object, error) {
					return r.kubeClient.Core().ReplicationControllers(api.NamespaceAll).List(options)
				},
				WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
					return r.kubeClient.Core().ReplicationControllers(api.NamespaceAll).Watch(options)
				},
			},
			&api.ReplicationController{},
			options.ResyncPeriod(),
			cache.ResourceEventHandlerFuncs{
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			},
		)
	case api.Kind("PersistentVolumeClaim"):
		if r.sharedInformerFactory != nil {
			result, err = controllerFor(api.Resource("persistentvolumeclaims"), r.sharedInformerFactory, cache.ResourceEventHandlerFuncs{
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			})
			break
		}
		// TODO (derekwaynecarr) remove me when we can require a sharedInformerFactory in all code paths...
		_, result = cache.NewInformer(
			&cache.ListWatch{
				ListFunc: func(options api.ListOptions) (runtime.Object, error) {
					return r.kubeClient.Core().PersistentVolumeClaims(api.NamespaceAll).List(options)
				},
				WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
					return r.kubeClient.Core().PersistentVolumeClaims(api.NamespaceAll).Watch(options)
				},
			},
			&api.PersistentVolumeClaim{},
			options.ResyncPeriod(),
			cache.ResourceEventHandlerFuncs{
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			},
		)
	case api.Kind("Secret"):
		// TODO move to informer when defined
		_, result = cache.NewInformer(
			&cache.ListWatch{
				ListFunc: func(options api.ListOptions) (runtime.Object, error) {
					return r.kubeClient.Core().Secrets(api.NamespaceAll).List(options)
				},
				WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
					return r.kubeClient.Core().Secrets(api.NamespaceAll).Watch(options)
				},
			},
			&api.Secret{},
			options.ResyncPeriod(),
			cache.ResourceEventHandlerFuncs{
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			},
		)
	case api.Kind("ConfigMap"):
		// TODO move to informer when defined
		_, result = cache.NewInformer(
			&cache.ListWatch{
				ListFunc: func(options api.ListOptions) (runtime.Object, error) {
					return r.kubeClient.Core().ConfigMaps(api.NamespaceAll).List(options)
				},
				WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
					return r.kubeClient.Core().ConfigMaps(api.NamespaceAll).Watch(options)
				},
			},
			&api.ConfigMap{},
			options.ResyncPeriod(),
			cache.ResourceEventHandlerFuncs{
				DeleteFunc: ObjectReplenishmentDeleteFunc(options),
			},
		)
	default:
		return nil, NewUnhandledGroupKindError(options.GroupKind)
	}
	return result, err
}
```

### 以pod资源为例子
func NewPodInformer和rc资源的分析是复用了一样的代码。 

查看pod资源注册的UpdateFunc和DeleteFunc。
```go
// PodReplenishmentUpdateFunc will replenish if the old pod was quota tracked but the new is not
/*
	如果一个old pod占用了一个配额，而新的pod缺没有，触发ReplenishmentFunc
*/
func PodReplenishmentUpdateFunc(options *ReplenishmentControllerOptions) func(oldObj, newObj interface{}) {
	return func(oldObj, newObj interface{}) {
		oldPod := oldObj.(*api.Pod)
		newPod := newObj.(*api.Pod)
		/*
			QuotaPod判断一个pod是否需要占用了一个配额
			我们约定，一个状态到达生命末期的pod不会占用配额
			根据pod的状态来决定
				==>/pkg/quota/evaluator/core/pods.go
					==>func QuotaPod(pod *api.Pod) bool
		*/
		if core.QuotaPod(oldPod) && !core.QuotaPod(newPod) {
			options.ReplenishmentFunc(options.GroupKind, newPod.Namespace, oldPod)
		}
	}
}

// ObjectReplenenishmentDeleteFunc will replenish on every delete
/*
	所有的delete操作都会触发ObjectReplenishmentDeleteFunc
*/
func ObjectReplenishmentDeleteFunc(options *ReplenishmentControllerOptions) func(obj interface{}) {
	return func(obj interface{}) {
		metaObject, err := meta.Accessor(obj)
		if err != nil {
			tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
			if !ok {
				glog.Errorf("replenishment controller could not get object from tombstone %+v, could take up to %v before quota is replenished", obj, options.ResyncPeriod())
				utilruntime.HandleError(err)
				return
			}
			metaObject, err = meta.Accessor(tombstone.Obj)
			if err != nil {
				glog.Errorf("replenishment controller tombstone contained object that is not a meta %+v, could take up to %v before quota is replenished", tombstone.Obj, options.ResyncPeriod())
				utilruntime.HandleError(err)
				return
			}
		}
		options.ReplenishmentFunc(options.GroupKind, metaObject.GetNamespace(), nil)
	}
}
```
在NewResourceQuotaController中创建replenishmentController时，options.ReplenishmentFunc的声明是`func (rq *ResourceQuotaController) replenishQuota`，如下：
```go
// replenishQuota is a replenishment function invoked by a controller to notify that a quota should be recalculated
/*
	被controller用于说明一个quota需要被recalculated

	最后一个参数object runtime.Object没用？。。。好吧
*/
func (rq *ResourceQuotaController) replenishQuota(groupKind unversioned.GroupKind, namespace string, object runtime.Object) {
	// check if the quota controller can evaluate this kind, if not, ignore it altogether...
	evaluators := rq.registry.Evaluators()
	evaluator, found := evaluators[groupKind]
	if !found {
		return
	}

	// check if this namespace even has a quota...
	indexKey := &api.ResourceQuota{}
	indexKey.Namespace = namespace
	/*
		根据namespace来获取resourceQuotas
	*/
	resourceQuotas, err := rq.rqIndexer.Index("namespace", indexKey)
	if err != nil {
		glog.Errorf("quota controller could not find ResourceQuota associated with namespace: %s, could take up to %v before a quota replenishes", namespace, rq.resyncPeriod())
	}
	if len(resourceQuotas) == 0 {
		return
	}

	// only queue those quotas that are tracking a resource associated with this kind.
	matchedResources := evaluator.MatchesResources()
	for i := range resourceQuotas {
		resourceQuota := resourceQuotas[i].(*api.ResourceQuota)
		resourceQuotaResources := quota.ResourceNames(resourceQuota.Status.Hard)
		if len(quota.Intersection(matchedResources, resourceQuotaResources)) > 0 {
			// TODO: make this support targeted replenishment to a specific kind, right now it does a full recalc on that quota.
			/*
				将该resourceQuota加入到队列queue，等待ResourceQuotaController的worker的重新计算
			*/
			rq.enqueueResourceQuota(resourceQuota)
		}
	}
}
```
至此，可以看出replenishmentController就是用来捕获对应资源的Update/Delete事件，将其对应的ResourceQuota加入到`queue`中，然后worker会再对其进行重新计算Usage。

### QuotaPod
见/pkg/quota/evaluator/core/pods.go
```go
// QuotaPod returns true if the pod is eligible to track against a quota
// if it's not in a terminal state according to its phase.
/*
	把状态处于Failed和Terminating的Pod排除
*/
func QuotaPod(pod *api.Pod) bool {
	// see GetPhase in kubelet.go for details on how it covers all restart policy conditions
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet.go#L3001
	return !(api.PodFailed == pod.Status.Phase || api.PodSucceeded == pod.Status.Phase)
}
```
可以略做更改，把处于Unkonwn状态的pod也排除掉
```go
func QuotaPodNew(pod *api.Pod) bool {
	// see GetPhase in kubelet.go for details on how it covers all restart policy conditions
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet.go#L3001
	if api.PodFailed == pod.Status.Phase || api.PodSucceeded == pod.Status.Phase {
		return false
	}
	//import pkg/util/node
	if podDeletionTimestamp != nil && pod.Status.Reason == node.NodeUnreachableReason {
		return false
	}
	return true
}
```

## 总结
1. type ResourceQuotaController struct中的两个队列：
  - queue workqueue.RateLimitingInterface, 用于存放待sync的ResourceQuota objects； replenishmentController也会把需要重新计算的ResourceQuota objects放入这里。
  - missingUsageQueue workqueue.RateLimitingInterface, 用于存放那些丢失了initial usage informatino的ResourceQuota objects。

2. ResourceQuotaController中有两种Controller：
  - rqController *cache.Controller, 用于watch所有的resourcequota resource的变化
  - replenishmentControllers []cache.ControllerInterface, 监控各种资源的Update/Delete操作，以通知worker重新补充一个ResourceQuota objects

3. rqController管理的是resourcequota 这个资源本身的变化，然后通过List-Watch反应出来，其类型是&api.ResourceQuota{}。

4. 而replenishmentControllers是监控pod这些资源的Update/Delete操作，然后用namespace作为key获取对应的resourcequota 资源(就是被rqController管着的那个)，把其放入queue workqueue.RateLimitingInterface中，等待worker的重新计算处理。














