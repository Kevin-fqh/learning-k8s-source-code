# 控制器ReplicationManager分析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [ReplicationManager 定义](#replicationmanager-定义)
  - [run函数](#run函数)
  - [同步以rc和pod的一致性](#同步以rc和pod的一致性)
  - [判断一个pod是否处于Active状态](#判断一个pod是否处于active状态)
<!-- END MUNGE: GENERATED_TOC -->

学习ReplicationManager是如何维护一个rc下的pod数量与spec中期望的数量一致的。

##  ReplicationManager 定义
```go
// ReplicationManager is responsible for synchronizing ReplicationController objects stored
// in the system with actual running pods.
// TODO: this really should be called ReplicationController. The only reason why it's a Manager
// is to distinguish this type from API object "ReplicationController". We should fix this.
/*
	译：type ReplicationManager struct 负责将存储在系统中的ReplicationController对象(即rc)与实际运行的pod进行同步。
	TODO：这个应该叫做ReplicationController。 命名为Manager的原因是将此类型与API对象“ReplicationController”区分开来。 
*/

type ReplicationManager struct {
	/*
		访问apiserver的客户端
	*/
	kubeClient clientset.Interface
	/*
		pod操作函数的封装，在/pkg/controller/controller_utils.go里面定义实现
			==>type PodControlInterface interface
		提供Create/Delete Pod的操作接口。
	*/
	podControl controller.PodControlInterface

	// internalPodInformer is used to hold a personal informer.  If we're using
	// a normal shared informer, then the informer will be started for us.  If
	// we have a personal informer, we must start it ourselves.   If you start
	// the controller using NewReplicationManager(passing SharedInformer), this
	// will be null
	/*
		译：
			internalPodInformer 用于持有一个非共享的informer。
			如果我们使用一个普通的共享型informer，该informer是已经自动运行。
			如果我们使用一个非共享的informer，必须自己主动去start it。
			如果你使用NewReplicationManager(passing SharedInformer)去启动一个controller，internalPodInformer应该是一个nil值
	*/
	internalPodInformer cache.SharedIndexInformer

	// An rc is temporarily suspended after creating/deleting these many replicas.
	// It resumes normal action after observing the watch events for them.
	burstReplicas int //每次批量Create/Delete Pods时允许并发的最大数量。
	// To allow injection of syncReplicationController for testing.
	syncHandler func(rcKey string) error //真正执行Replica Sync的函数。

	// A TTLCache of pod creates/deletes each rc expects to see.
	/*
		维护每一个rc期望状态下的Pod的Uid Cache，并且提供了修正该Cache的接口。

		定义在/pkg/controller/controller_utils.go
			==>type UIDTrackingControllerExpectations struct
	*/
	expectations *controller.UIDTrackingControllerExpectations

	// A store of replication controllers, populated by the rcController
	rcStore cache.StoreToReplicationControllerLister //由rcController来填充和维护,resource rc的Indexer
	// Watches changes to all replication controllers
	/*
		rcController，监控所有rc resource的变化，实现rc同步的任务调度逻辑, watch到的change更新到rcStore中。
		==>定义在/pkg/client/cache/controller.go
			==>type Controller struct
	*/
	rcController *cache.Controller
	// A store of pods, populated by the podController
	podStore cache.StoreToPodLister //由podController来填充和维护,Pod的Indexer
	// Watches changes to all pods
	podController cache.ControllerInterface //监控所有pod绑定变化，实现pod同步的任务调度逻辑
	// podStoreSynced returns true if the pod store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	/*
		译：如果pod存储已至少同步一次，则podStoreSynced返回true。
		  作为成员添加到结构体以允许注入进行测试。
	*/
	podStoreSynced func() bool

	lookupCache *controller.MatchingCache //提供Pod和RC匹配信息的cache，以提高查询效率

	// Controllers that need to be synced
	/*
		用来存放待sync的rc resource，是一个RateLimit类型的queue。
	*/
	queue workqueue.RateLimitingInterface

	// garbageCollectorEnabled denotes if the garbage collector is enabled. RC
	// manager behaves differently if GC is enabled.
	garbageCollectorEnabled bool
}
```

## run函数
新建一个ReplicationManager之后，run起来，开始进行watching and syncing 资源rc和pod。
1. rcController.Run(stopCh)，负责watch所有的rc resource
2. podController.Run(stopCh)，负责watch所有的pod resource
3. go wait.Until(rm.worker, time.Second, stopCh)，启动workers数量的goroutine，每个goroutine都不断循环执行rm.worker，每个循环之间停留1s。
4. rm.worker就是负责从queue中获取rc并调用syncHandler进行同步。 
```go
// Run begins watching and syncing.
func (rm *ReplicationManager) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	glog.Infof("Starting RC Manager")
	/*
		分别启动rc和pod的controller
	*/
	go rm.rcController.Run(stopCh)
	go rm.podController.Run(stopCh)
	for i := 0; i < workers; i++ {
		/*
			运行func (rm *ReplicationManager) worker()
		*/
		go wait.Until(rm.worker, time.Second, stopCh)
	}

	if rm.internalPodInformer != nil {
		go rm.internalPodInformer.Run(stopCh)
	}

	<-stopCh
	glog.Infof("Shutting down RC Manager")
	rm.queue.ShutDown()
}
```
这里的rcController和podController的Run函数都是运行定义在/pkg/client/cache/shared_informer.go的`func (c *Controller) Run(stopCh <-chan struct{})`，只是两者的初始化参数不一样。 其功能是完成watch功能，分别把watch到的rc resource和pod resource存放到对应的store（rcStore，podStore）中。 

最后，由worker来完成同步功能。

## 同步以rc和pod的一致性

同步是由worker来完成，分析其流程，如下：
1. 从rm的RateLimited Queue中获取一个rc的key。
2. 调用rm.syncHandler，对该rc进行sync，即func (rm *ReplicationManager) syncReplicationController(key string) error
```go
// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
/*
	译：func (rm *ReplicationManager) worker() 运行一个worker线程，只需将items排队，处理它们并将其标记完毕。
	   func (rm *ReplicationManager) worker() 强制syncHandler从不与同一个键并发调用。
*/
func (rm *ReplicationManager) worker() {
	workFunc := func() bool {
		key, quit := rm.queue.Get()
		if quit {
			return true
		}
		defer rm.queue.Done(key)

		/*
			syncHandler是个重要的函数，负责pod与rc的同步，确保Pod副本数与rc规定的相同。
			rm.syncHandler = rm.syncReplicationController
				=>func (rm *ReplicationManager) syncReplicationController(key string) error
		*/
		err := rm.syncHandler(key.(string))
		if err == nil {
			rm.queue.Forget(key)
			return false
		}

		rm.queue.AddRateLimited(key)
		utilruntime.HandleError(err)
		return false
	}
	for {
		if quit := workFunc(); quit {
			glog.Infof("replication controller worker shutting down")
			return
		}
	}
}
```

- func (rm *ReplicationManager) syncReplicationController(key string) error

syncReplicationController将同步rc与指定的key，如果该rc已经满足了它的期望，这意味着它不再看到任何更多的pod创建或删除。 不能用同一个key来同时唤醒本函数。

其中最核心的部分在于调用manageReplicas方法，使得该rc管理的active状态的pods数量和期望值一样。分析其流程如下：

1. 如果podStore还没有被同步过一次，则将该rc的key重新加入到queue中，继续等待podStore同步，return nil
2. 根据该rc的key值，从rcStore中获取对应的rc object，如果不存在该rc object，则说明该rc已经被删除了，然后根据key从epectations中删除该rc并返回，return nil。
3. 检测expectations来判断该rc是否需要sync。
4. 如果启动了垃圾回收，则获取podStore中整个namespace下的pods，然后将matchesAndControlled和matchesNeedsController的pods作为过滤后待同步的filteredPods。 如果没有启动GC，则直接获取podStore中该namespace下匹配rc.Spec.Selector的Active状态的pods作为过滤后待同步的filteredPods。
5. 如果第3步中检测到该rc需要sync，并且DeletionTimestamp这个时间戳为nil，则调用manageReplicas方法，使得该rc管理的active状态的pods数量和期望值一样。 这一步是核心。
6. 执行完manageReplicas后，需要马上重新计算一下rc的status，更新status中的Conditions，Replicas，FullyLabeledReplicas，ReadyReplicas，AvailableReplicas信息。
7. 告诉Apiserver，更新该rc的status为上面重新计算后的值

```go
func (rm *ReplicationManager) syncReplicationController(key string) error {
	/*
		入参key 可以看作是一个rc resource
	*/
	trace := util.NewTrace("syncReplicationController: " + key)
	defer trace.LogIfLong(250 * time.Millisecond)

	startTime := time.Now()
	defer func() {
		glog.V(4).Infof("Finished syncing controller %q (%v)", key, time.Now().Sub(startTime))
	}()

	/*
		1. 如果podStore还没有被同步过一次，则将该rc的key重新加入到queue中，以等待podStore同步，return nil
	*/
	if !rm.podStoreSynced() {
		// Sleep so we give the pod reflector goroutine a chance to run.
		time.Sleep(PodStoreSyncedPollPeriod)
		glog.Infof("Waiting for pods controller to sync, requeuing rc %v", key)
		rm.queue.Add(key)
		return nil
	}

	/*
		2. 根据该rc的key值，从rcStore中获取对应的rc object，
			如果不存在该rc object，则说明该rc已经被删除了，然后根据key从epectations中删除该rc并返回，return nil。
			如果存在该rc object，则继续后面的流程。
	*/
	obj, exists, err := rm.rcStore.Indexer.GetByKey(key)
	if !exists {
		glog.Infof("Replication Controller has been deleted %v", key)
		rm.expectations.DeleteExpectations(key)
		return nil
	}
	if err != nil {
		return err
	}
	rc := *obj.(*api.ReplicationController)

	// Check the expectations of the rc before counting active pods, otherwise a new pod can sneak in
	// and update the expectations after we've retrieved active pods from the store. If a new pod enters
	// the store after we've checked the expectation, the rc sync is just deferred till the next relist.
	/*
		译：在计算active pod之前检查rc的expectations，
			否则在我们从stroe中获取到一个active的pod之后，一个新的pod会注入和更新到expectations。
			如果一个新的pod在我们检查完expectation之后进入store，那么rc的sync仅仅是被推迟到下一次的relist操作。
	*/
	rcKey, err := controller.KeyFunc(&rc)
	if err != nil {
		glog.Errorf("Couldn't get key for replication controller %#v: %v", rc, err)
		return err
	}
	trace.Step("ReplicationController restored")
	/*
		3. 检测expectations中的add和del以及距离上一个时间戳是否超时5min，来判断该rc是否需要sync。
			==>/pkg/controller/controller_utils.go
				==>func (r *ControllerExpectations) SatisfiedExpectations(controllerKey string) bool
	*/
	rcNeedsSync := rm.expectations.SatisfiedExpectations(rcKey)
	trace.Step("Expectations restored")

	// NOTE: filteredPods are pointing to objects from cache - if you need to
	// modify them, you need to copy it first.
	// TODO: Do the List and Filter in a single pass, or use an index.
	var filteredPods []*api.Pod
	/*
		4. 如果启动了GC，则获取podStore中整个namespace下的pods，
			然后将matchesAndControlled和matchesNeedsController的pods作为过滤后待同步的filteredPods。

		   如果没有启动GC，则直接获取podStore中该namespace下匹配rc.Spec.Selector的Active状态的pods作为过滤后待同步的filteredPods。
		
		默认是开启GC的
	*/
	if rm.garbageCollectorEnabled {
		// list all pods to include the pods that don't match the rc's selector
		// anymore but has the stale controller ref.
		pods, err := rm.podStore.Pods(rc.Namespace).List(labels.Everything())
		if err != nil {
			glog.Errorf("Error getting pods for rc %q: %v", key, err)
			rm.queue.Add(key)
			return err
		}
		/*
			定义在pkg/controller/controller_ref_manager.go
				==>/pkg/controller/controller_ref_manager.go
		*/
		cm := controller.NewPodControllerRefManager(rm.podControl, rc.ObjectMeta, labels.Set(rc.Spec.Selector).AsSelectorPreValidated(), getRCKind())
		matchesAndControlled, matchesNeedsController, controlledDoesNotMatch := cm.Classify(pods)
		for _, pod := range matchesNeedsController {
			err := cm.AdoptPod(pod)
			// continue to next pod if adoption fails.
			if err != nil {
				// If the pod no longer exists, don't even log the error.
				if !errors.IsNotFound(err) {
					utilruntime.HandleError(err)
				}
			} else {
				matchesAndControlled = append(matchesAndControlled, pod)
			}
		}
		filteredPods = matchesAndControlled
		// remove the controllerRef for the pods that no longer have matching labels
		var errlist []error
		for _, pod := range controlledDoesNotMatch {
			err := cm.ReleasePod(pod)
			if err != nil {
				errlist = append(errlist, err)
			}
		}
		if len(errlist) != 0 {
			aggregate := utilerrors.NewAggregate(errlist)
			// push the RC into work queue again. We need to try to free the
			// pods again otherwise they will stuck with the stale
			// controllerRef.
			rm.queue.Add(key)
			return aggregate
		}
	} else {
		pods, err := rm.podStore.Pods(rc.Namespace).List(labels.Set(rc.Spec.Selector).AsSelectorPreValidated())
		if err != nil {
			glog.Errorf("Error getting pods for rc %q: %v", key, err)
			rm.queue.Add(key)
			return err
		}
		filteredPods = controller.FilterActivePods(pods)
	}

	var manageReplicasErr error
	/*
		5. 如果第3步中检测到该rc需要sync，并且DeletionTimestamp这个时间戳为nil，
			则调用manageReplicas方法，使得该rc管理的active状态的pods数量和期望值一样。

		这一步是核心，负责rc对应replicas的修复工作（add or delete）
	*/
	if rcNeedsSync && rc.DeletionTimestamp == nil {
		manageReplicasErr = rm.manageReplicas(filteredPods, &rc)
	}
	trace.Step("manageReplicas done")

	/*
		6. 执行完manageReplicas后，需要马上重新计算一下rc的status，
		   更新status中的Conditions，Replicas，FullyLabeledReplicas，ReadyReplicas，AvailableReplicas信息。
	*/
	newStatus := calculateStatus(rc, filteredPods, manageReplicasErr)

	// Always updates status as pods come up or die.
	/*
		7. 通过updateReplicationControllerStatus方法调用kube-apiserver的接口更新该rc的status为上一步重新计算后的新status，流程结束。
	*/
	if err := updateReplicationControllerStatus(rm.kubeClient.Core().ReplicationControllers(rc.Namespace), rc, newStatus); err != nil {
		// Multiple things could lead to this update failing.  Returning an error causes a requeue without forcing a hotloop
		return err
	}

	return manageReplicasErr
}
```

- func (rm *ReplicationManager) manageReplicas(filteredPods []*api.Pod, rc *api.ReplicationController) error

如果一个rc需要sync，并且其DeletionTimestamp这个时间戳为nil， 则调用manageReplicas方法，使得该rc管理的active状态的pods数量和期望值一样。分析其流程如下：

1. 计算filteredPods中Pods数量和rc.Spec.Replicas中定义的期望数量的差值diff。

2. 如果差值diff为0，表示当前状态和期望状态一样，直接return

3. 如果差值diff为负数，表示当前Active状态的Pods数量不足
  - 比较|diff|和burstReplicas的值，以保证这次最多只创建burstReplicas数量的pods。
  - 调用expectations.ExpectCreations接口, 设置expectations中的add大小为|diff|的值，表示要新创建|diff|数量的pods以达到期望状态。
  - sync.WaitGroup启动|diff|数量的goroutine协程，每个goroutine分别负责调用podControl.CreatePodsWithControllerRef接口, 创建一个该namespace.rc管理的对应spec Template的pod。
  - 待所有goroutine都执行完毕后，如果其中一个或者多个pod创建失败，则返回err，否则返回nil，流程结束。

4. 如果差值diff为正数，表示当前Active状态的Pods数量超过了期望值
  - 比较|diff|和burstReplicas的值，以保证这次最多只删除burstReplicas数量的pods。
  - 对filteredPods中的pods进行排序，让stages越早的pods优先被delete。
  - 排序完之后，挑选前面|diff|个pods作为待delete的Pods。
  - 调用expectations.ExpectDeletions接口, 设置expectations中的del大小为|diff|的值，表示要新删除|diff|数量的pods以达到期望状态。
  - 待所有goroutine都执行完毕后，如果其中一个或者多个pod删除失败，则返回err，否则返回nil，流程结束。

```go
// manageReplicas checks and updates replicas for the given replication controller.
// Does NOT modify <filteredPods>.
func (rm *ReplicationManager) manageReplicas(filteredPods []*api.Pod, rc *api.ReplicationController) error {
	/*
		计算filteredPods中Pods数量和rc.Spec.Replicas中定义的期望数量的差值diff。
	*/
	diff := len(filteredPods) - int(rc.Spec.Replicas)
	rcKey, err := controller.KeyFunc(rc)
	if err != nil {
		return err
	}
	/*
		分支 1) 如果差值diff为0，表示当前状态和期望状态一样，直接return
	*/
	if diff == 0 {
		return nil
	}

	/*
		分支 2) 如果差值diff为负数，表示当前Active状态的Pods数量不足
	*/
	if diff < 0 {
		diff *= -1
		if diff > rm.burstReplicas {
			/*
				比较|diff|和burstReplicas的值，以保证这次最多只创建burstReplicas数量的pods。
			*/
			diff = rm.burstReplicas
		}
		// TODO: Track UIDs of creates just like deletes. The problem currently
		// is we'd need to wait on the result of a create to record the pod's
		// UID, which would require locking *across* the create, which will turn
		// into a performance bottleneck. We should generate a UID for the pod
		// beforehand and store it via ExpectCreations.
		errCh := make(chan error, diff)
		/*
			调用expectations.ExpectCreations接口设置expectations中的add大小为|diff|的值，
			表示要新创建|diff|数量的pods以达到期望状态。
		*/
		rm.expectations.ExpectCreations(rcKey, diff)
		var wg sync.WaitGroup
		wg.Add(diff)
		glog.V(2).Infof("Too few %q/%q replicas, need %d, creating %d", rc.Namespace, rc.Name, rc.Spec.Replicas, diff)
		/*
			sync.WaitGroup启动|diff|数量的goroutine协程，
			每个goroutine分别负责调用podControl.CreatePodsWithControllerRef接口, 创建一个该namespace.rc管理的对应spec Template的pod。
		*/
		for i := 0; i < diff; i++ {
			go func() {
				defer wg.Done()
				var err error
				if rm.garbageCollectorEnabled {
					// 默认情况rm.garbageCollectorEnabled＝true
					var trueVar = true
					controllerRef := &api.OwnerReference{
						APIVersion: getRCKind().GroupVersion().String(),
						Kind:       getRCKind().Kind,
						Name:       rc.Name,
						UID:        rc.UID,
						Controller: &trueVar,
					}
					//在etcd中写入pod的数据
					err = rm.podControl.CreatePodsWithControllerRef(rc.Namespace, rc.Spec.Template, rc, controllerRef)
				} else {
					err = rm.podControl.CreatePods(rc.Namespace, rc.Spec.Template, rc)
				}
				if err != nil {
					// Decrement the expected number of creates because the informer won't observe this pod
					glog.V(2).Infof("Failed creation, decrementing expectations for controller %q/%q", rc.Namespace, rc.Name)
					rm.expectations.CreationObserved(rcKey)
					errCh <- err
					utilruntime.HandleError(err)
				}
			}()
		}
		wg.Wait()

		/*
			待所有goroutine都执行完毕后，如果其中一个或者多个pod创建失败，则返回err，否则返回nil，流程结束。
		*/
		select {
		case err := <-errCh:
			// all errors have been reported before and they're likely to be the same, so we'll only return the first one we hit.
			if err != nil {
				return err
			}
		default:
		}

		return nil
	}

	/*
		分支 3) 如果差值diff为正数，表示当前Active状态的Pods数量超过了期望值
	*/
	if diff > rm.burstReplicas {
		/*
			比较|diff|和burstReplicas的值，以保证这次最多只删除burstReplicas数量的pods。
		*/
		diff = rm.burstReplicas
	}
	glog.V(2).Infof("Too many %q/%q replicas, need %d, deleting %d", rc.Namespace, rc.Name, rc.Spec.Replicas, diff)
	// No need to sort pods if we are about to delete all of them
	if rc.Spec.Replicas != 0 {
		// Sort the pods in the order such that not-ready < ready, unscheduled
		// < scheduled, and pending < running. This ensures that we delete pods
		// in the earlier stages whenever possible.
		/*
			对filteredPods中的pods进行排序，
			排序目的是：not-ready < ready, unscheduled < scheduled, and pending < running，
			让stages越早的pods优先被delete。
		*/
		sort.Sort(controller.ActivePods(filteredPods))
	}
	// Snapshot the UIDs (ns/name) of the pods we're expecting to see
	// deleted, so we know to record their expectations exactly once either
	// when we see it as an update of the deletion timestamp, or as a delete.
	// Note that if the labels on a pod/rc change in a way that the pod gets
	// orphaned, the rs will only wake up after the expectations have
	// expired even if other pods are deleted.
	deletedPodKeys := []string{}
	/*
		排序完之后，挑选前面|diff|个pods作为待delete的Pods。
	*/
	for i := 0; i < diff; i++ {
		deletedPodKeys = append(deletedPodKeys, controller.PodKey(filteredPods[i]))
	}
	// We use pod namespace/name as a UID to wait for deletions, so if the
	// labels on a pod/rc change in a way that the pod gets orphaned, the
	// rc will only wake up after the expectation has expired.
	errCh := make(chan error, diff)
	/*
		调用expectations.ExpectDeletions接口, 设置expectations中的del大小为|diff|的值，
		表示要新删除|diff|数量的pods以达到期望状态。
	*/
	rm.expectations.ExpectDeletions(rcKey, deletedPodKeys)
	var wg sync.WaitGroup
	wg.Add(diff)
	for i := 0; i < diff; i++ {
		go func(ix int) {
			defer wg.Done()
			if err := rm.podControl.DeletePod(rc.Namespace, filteredPods[ix].Name, rc); err != nil {
				// Decrement the expected number of deletes because the informer won't observe this deletion
				podKey := controller.PodKey(filteredPods[ix])
				glog.V(2).Infof("Failed to delete %v due to %v, decrementing expectations for controller %q/%q", podKey, err, rc.Namespace, rc.Name)
				rm.expectations.DeletionObserved(rcKey, podKey)
				errCh <- err
				utilruntime.HandleError(err)
			}
		}(i)
	}
	wg.Wait()

	/*
		待所有goroutine都执行完毕后，如果其中一个或者多个pod删除失败，则返回err，否则返回nil，流程结束。
	*/
	select {
	case err := <-errCh:
		// all errors have been reported before and they're likely to be the same, so we'll only return the first one we hit.
		if err != nil {
			return err
		}
	default:
	}

	return nil

}
```

## 判断一个pod是否处于Active状态
最后来看一下ReplicationManager是如何判断一个pod是否处于Active状态，见 /pkg/controller/controller_utils.go
```go
func IsPodActive(p *api.Pod) bool {
	return api.PodSucceeded != p.Status.Phase &&
		api.PodFailed != p.Status.Phase &&
		p.DeletionTimestamp == nil
}
```

至此，ReplicationManager的工作流程基本清晰，结合前面两篇[ControllerManager的List-Watch机制]()可以更好地了解数据的流向和工作机制。

## 参考
[Kubernetes ReplicationController源码分析](http://blog.csdn.net/waltonwang/article/details/62433143)