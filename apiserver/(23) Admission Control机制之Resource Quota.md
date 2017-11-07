# Admission Control机制之Resource Quota

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [plugin注册](#plugin注册)
  - [type quotaEvaluator struct](#type-quotaevaluator-struct)

<!-- END MUNGE: GENERATED_TOC -->

## plugin注册
各个Admission Control plugin在初始化的时候会向apiserver注册，见/cmd/kube-apiserver/app/plugins.go
```go
// Admission policies
	_ "k8s.io/kubernetes/plugin/pkg/admission/admit"
	_ "k8s.io/kubernetes/plugin/pkg/admission/alwayspullimages"
	_ "k8s.io/kubernetes/plugin/pkg/admission/antiaffinity"
	_ "k8s.io/kubernetes/plugin/pkg/admission/deny"
	_ "k8s.io/kubernetes/plugin/pkg/admission/exec"
	_ "k8s.io/kubernetes/plugin/pkg/admission/gc"
	_ "k8s.io/kubernetes/plugin/pkg/admission/imagepolicy"
	_ "k8s.io/kubernetes/plugin/pkg/admission/initialresources"
	_ "k8s.io/kubernetes/plugin/pkg/admission/limitranger"
	_ "k8s.io/kubernetes/plugin/pkg/admission/namespace/autoprovision"
	_ "k8s.io/kubernetes/plugin/pkg/admission/namespace/exists"
	_ "k8s.io/kubernetes/plugin/pkg/admission/namespace/lifecycle"
	_ "k8s.io/kubernetes/plugin/pkg/admission/persistentvolume/label"
	_ "k8s.io/kubernetes/plugin/pkg/admission/podnodeselector"
	_ "k8s.io/kubernetes/plugin/pkg/admission/resourcequota"
	_ "k8s.io/kubernetes/plugin/pkg/admission/security/podsecuritypolicy"
	_ "k8s.io/kubernetes/plugin/pkg/admission/securitycontext/scdeny"
	_ "k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
	_ "k8s.io/kubernetes/plugin/pkg/admission/storageclass/default"
```

各个plugin会通过自身的init函数进行注册，以Resource Quota为例子，见/plugin/pkg/admission/resourcequota/admission.go
```go
func init() {
	/*
		向kube-apiserver的admission controller注册插件ResourceQuota
		=>/pkg/admission/plugins.go
			==>func RegisterPlugin(name string, plugin Factory)
	*/
	admission.RegisterPlugin("ResourceQuota",
		func(client clientset.Interface, config io.Reader) (admission.Interface, error) {
			// NOTE: we do not provide informers to the registry because admission level decisions
			// does not require us to open watches for all items tracked by quota.
			/*
				不提供informers，因为admission 级别的跟踪quota不需要为所有的items都开启watch
					==>/pkg/quota/install/registry.go
						==>func NewRegistry
			*/
			registry := install.NewRegistry(client, nil)
			return NewResourceQuota(client, registry, 5, make(chan struct{}))
		})
}
```

### type quotaAdmission struct
quotaAdmission是核心主体，其中Evaluator负责检查quota约束是否得到满足。
```go
// quotaAdmission implements an admission controller that can enforce quota constraints
type quotaAdmission struct {
	*admission.Handler

	evaluator Evaluator
}
```

- func NewResourceQuota负责实例化type quotaAdmission struct

func NewResourceQuota会设置admission controller使用提供的registry quota.Registry来强制检查quota约束。

registry quota.Registry必须有权限去处理由server持久化的group/kinds。 admission controller 是拦截式的工作方式。
```go
// NewResourceQuota configures an admission controller that can enforce quota constraints
// using the provided registry.  The registry must have the capability to handle group/kinds that
// are persisted by the server this admission controller is intercepting

func NewResourceQuota(client clientset.Interface, registry quota.Registry, numEvaluators int, stopCh <-chan struct{}) (admission.Interface, error) {
	quotaAccessor, err := newQuotaAccessor(client)
	if err != nil {
		return nil, err
	}
	go quotaAccessor.Run(stopCh)

	evaluator := NewQuotaEvaluator(quotaAccessor, registry, nil, numEvaluators, stopCh)

	/*
		admission controller相关
		生成一个handler，这里表示ResourceQuota的会对Create和Update动作进行检查
	*/
	return &quotaAdmission{
		Handler:   admission.NewHandler(admission.Create, admission.Update),
		evaluator: evaluator,
	}, nil
}
```

### func Admit
Admit函数负责执行quota认证流程。 这个在前面调用流程已经说过了，在一个RestfulApi执行动作之前会执行各种plugin的Admit()。

也就是说这里可以认为是进行quota检查的总入口。
```
// Admit makes admission decisions while enforcing quota

func (q *quotaAdmission) Admit(a admission.Attributes) (err error) {
	// ignore all operations that correspond to sub-resource actions
	if a.GetSubresource() != "" {
		return nil
	}

	return q.evaluator.Evaluate(a)
}
```

## type quotaEvaluator struct
quotaEvaluator负责对一个a admission.Attributes进行quota检查。 
每次request请求都会新生成一个admission.Attributes对象。

quotaEvaluator中先会定义若干个workers，所有请求都会存储在work或dirtyWork的ns key下（如果正在处理该ns，则存储在ditryWork中）。 worker会处理work中某ns下的所有请求，并一次更新resource quota。 如果某请求超出配额，则该请求的result为error。 worker在处理完某ns时，dirtyWork中该ns下的所有请求转到work的ns下。

quotaEvaluator可以对某namespace下的所有请求进行配额累加处理(累加时如果超出配额，则拒绝该请求)， 如果某配额发生了更新，则quotaEvaluator会把对应的配额进行更新。

```go
type quotaEvaluator struct {
	quotaAccessor QuotaAccessor //获取quota的内容
	// lockAquisitionFunc acquires any required locks and returns a cleanup method to defer
	lockAquisitionFunc func([]api.ResourceQuota) func()

	// registry that knows how to measure usage for objects
	registry quota.Registry //管理podEvaluator等资源使用量计算器

	// TODO these are used together to bucket items by namespace and then batch them up for processing.
	// The technique is valuable for rollup activities to avoid fanout and reduce resource contention.
	// We could move this into a library if another component needed it.
	// queue is indexed by namespace, so that we bundle up on a per-namespace basis
	queue      *workqueue.Type //待处理的命名空间的队列
	workLock   sync.Mutex
	work       map[string][]*admissionWaiter //待处理的任务,注意其类型Type
	dirtyWork  map[string][]*admissionWaiter //如果某命名空间正在处理，则暂存入dirtyWork中
	inProgress sets.String                   //标识正在处理的命名空间

	// controls the run method so that we can cleanly conform to the Evaluator interface
	workers int //标明处理请求的协程数量。
	stopCh  <-chan struct{}
	/*
		sync.Once.Do(f func())保证once只执行一次，无论你是否更换once.Do(xx)这里的方法,这个sync.Once块只会执行一次。
	*/
	init sync.Once
}
```

来看看quotaEvaluator实现的功能函数，基本按照调用顺序来列出。
### NewQuotaEvaluator
用于生成一个quotaEvaluator对象， 其中registry记录着pod、svc等这些资源的Evaluator对象。
```go
// NewQuotaEvaluator configures an admission controller that can enforce quota constraints
// using the provided registry.  The registry must have the capability to handle group/kinds that
// are persisted by the server this admission controller is intercepting

func NewQuotaEvaluator(quotaAccessor QuotaAccessor, registry quota.Registry, lockAquisitionFunc func([]api.ResourceQuota) func(), workers int, stopCh <-chan struct{}) Evaluator {
	return &quotaEvaluator{
		quotaAccessor:      quotaAccessor,
		lockAquisitionFunc: lockAquisitionFunc,

		registry: registry,

		queue:      workqueue.NewNamed("admission_quota_controller"),
		work:       map[string][]*admissionWaiter{},
		dirtyWork:  map[string][]*admissionWaiter{},
		inProgress: sets.String{},

		workers: workers,
		stopCh:  stopCh,
	}
}
```
### Evaluate
核心函数，其流程如下：
1. 仅运行一次协程运行func (e *quotaEvaluator) run()
2. 根据入参a admission.Attributes获取判断是否需要继续进行quota检查和更改
3. e.addWork(waiter)
4. 等待waiter处理完毕，或者超时，收集结果return

```go
func (e *quotaEvaluator) Evaluate(a admission.Attributes) error {
	e.init.Do(func() {
		go e.run()
	})

	// if we do not know how to evaluate use for this kind, just ignore
	evaluators := e.registry.Evaluators()
	//根据Kind来查询对应的evaluator是否存在
	evaluator, found := evaluators[a.GetKind().GroupKind()]
	if !found {
		return nil
	}
	// for this kind, check if the operation could mutate any quota resources
	// if no resources tracked by quota are impacted, then just return
	/*
		对于此次的a admission.Attributes，检查其动作operation是否影响到`任意的受quota约束的resource`。
		如果不影响到有quota约束的resources，直接return nil，也就是说准入控制通过了。
			==>/pkg/quota/generic/evaluator.go
				==>func (g *GenericEvaluator) OperationResources

		受quota约束的resource是在生成具体的Evaluator的时候注册进去的
	*/
	op := a.GetOperation()
	operationResources := evaluator.OperationResources(op)
	/*
		动作op is:  CREATE
		对应的operationResources is: 7 [cpu memory requests.cpu requests.memory limits.cpu limits.memory pods]
	*/if len(operationResources) == 0 {
		return nil
	}

	waiter := newAdmissionWaiter(a)

	e.addWork(waiter)

	// wait for completion or timeout
	//等待waiter处理完毕，或者超时
	select {
	case <-waiter.finished:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout")
	}

	return waiter.result
}
```

### addWork
应该是把a *admissionWaiter做个分类标记，同时标记待处理的namespace，后续将进行quota检查。
```go
func (e *quotaEvaluator) addWork(a *admissionWaiter) {
	e.workLock.Lock()
	defer e.workLock.Unlock()

	/*
		把namespace加入到queue中标明该namespace下配额需要处理
	*/
	ns := a.attributes.GetNamespace()
	// this Add can trigger a Get BEFORE the work is added to a list, but this is ok because the getWork routine
	// waits the worklock before retrieving the work to do, so the writes in this method will be observed
	e.queue.Add(ns)

	/*
		如果该ns下的请求正在被处理，则先把请求加到e.dirtyWork中；
		否则append到e.work中！
	*/
	if e.inProgress.Has(ns) {
		e.dirtyWork[ns] = append(e.dirtyWork[ns], a)
		return
	}

	e.work[ns] = append(e.work[ns], a)
}
```

### run
启动多个groutine，每个groutine都执行func doWork 
```go
// Run begins watching and syncing.

func (e *quotaEvaluator) run() {
	defer utilruntime.HandleCrash()

	//开启workers个goroutine进行resource quota admit处理
	for i := 0; i < e.workers; i++ {
		go wait.Until(e.doWork, time.Second, e.stopCh)
	}
	<-e.stopCh
	glog.Infof("Shutting down quota evaluator")
	e.queue.ShutDown()
}
```

### doWork
1. 获取一个namespace及该namespace下的请求(即admissionAttributes)，
2. 然后调用checkAttributes()对这些请求进行quota检查，
3. 最后调用compleleWork()把dirtyWork中的请求转移到work中。
```go
func (e *quotaEvaluator) doWork() {
	workFunc := func() bool {
		//获取namespace及该namespace下的请求
		ns, admissionAttributes, quit := e.getWork()
		if quit {
			return true
		}
		//处理完成worker中的请求之后，会转移dirty worker中的请求到worker map
		defer e.completeWork(ns)
		if len(admissionAttributes) == 0 {
			return false
		}
		//进行quota检查
		e.checkAttributes(ns, admissionAttributes)
		return false
	}
	for {
		if quit := workFunc(); quit {
			glog.Infof("quota evaluator worker shutdown")
			return
		}
	}
}
```

### getWork
1. 先从queue中获取需要处理的namespace，
2. 然后获取该namespace下的所有request，并加入到inProgress以标识该namespace正在处理中。
```go
func (e *quotaEvaluator) getWork() (string, []*admissionWaiter, bool) {
	uncastNS, shutdown := e.queue.Get()
	if shutdown {
		return "", []*admissionWaiter{}, shutdown
	}
	ns := uncastNS.(string)

	e.workLock.Lock()
	defer e.workLock.Unlock()
	// at this point, we know we have a coherent view of e.work.  It is entirely possible
	// that our workqueue has another item requeued to it, but we'll pick it up early.  This ok
	// because the next time will go into our dirty list

	/*
		取出一个namespace中的所有request到临时变量work中；
		处理的数据已取走，清理e.work和e.dirtyWork的数据
	*/
	work := e.work[ns]
	delete(e.work, ns)
	delete(e.dirtyWork, ns)

	if len(work) != 0 {
		e.inProgress.Insert(ns)
		/*
			如果该ns中有未处理的请求，把其return
		*/
		return ns, work, false
	}

	e.queue.Done(ns)
	e.inProgress.Delete(ns)
	return ns, []*admissionWaiter{}, false
}
```

### completeWork
1. 把dirtyWork中的请求转移到work中
2. 把ns从标记正在处理的inProgress中删除
```go
func (e *quotaEvaluator) completeWork(ns string) {
	e.workLock.Lock()
	defer e.workLock.Unlock()

	e.queue.Done(ns)
	//把dirtyWork中的请求转移到work中
	e.work[ns] = e.dirtyWork[ns]
	delete(e.dirtyWork, ns)
	e.inProgress.Delete(ns)
}
```

### checkAttributes
func checkAttributes()迭代评估所有在等待的admissionAttributes。默认是拒绝的。 其流程如下：
1. 获取一个namespace下的quotas
2. 若无quota约束，直接return
3. 否则，调用checkQuotas()，检查resource quota，如需更新，则尝试更新
```go
// checkAttributes iterates evaluates all the waiting admissionAttributes.  It will always notify all waiters
// before returning.  The default is to deny.
/*
	迭代评估所有在等待的admissionAttributes。默认是拒绝的。
	先获取namespace下的quotas，然后调用checkQuotas()完成请求的配额检查，最后通知waiters已经完成配额检查
*/
func (e *quotaEvaluator) checkAttributes(ns string, admissionAttributes []*admissionWaiter) {
	// notify all on exit
	//通知所有的waiters
	defer func() {
		for _, admissionAttribute := range admissionAttributes {
			close(admissionAttribute.finished)
		}
	}()

	/*
		获取一个namespace下的quotas
			==>/plugin/pkg/admission/resourcequota/resource_access.go
				==>func (e *quotaAccessor) GetQuotas(namespace string)
	*/
	quotas, err := e.quotaAccessor.GetQuotas(ns)
	if err != nil {
		for _, admissionAttribute := range admissionAttributes {
			admissionAttribute.result = err
		}
		return
	}
	if len(quotas) == 0 {
		for _, admissionAttribute := range admissionAttributes {
			admissionAttribute.result = nil
		}
		//若无quota约束，直接return
		return
	}

	if e.lockAquisitionFunc != nil {
		releaseLocks := e.lockAquisitionFunc(quotas)
		defer releaseLocks()
	}

	//检查resource quota，如需更新，则尝试更新
	e.checkQuotas(quotas, admissionAttributes, 3)
}
```

在这里假设quota.yaml文件如下:
```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: quota-2
  namespace: default
spec:
  hard:
    persistentvolumeclaims: "10"
    pods: "2"
```

那么其获取到的quotas 内容如下:
```
[{{ } {quota-2  default /api/v1/namespaces/default/resourcequotas/quota-2 5b8980b9-c3cf-11e7-a512-080027e58fc6 406 0 2017-11-07 10:21:41 -0500 EST <nil> <nil> map[] map[] [] [] } {map[persistentvolumeclaims:{{10 0} {<nil>} 10 DecimalSI} pods:{{2 0} {<nil>} 2 DecimalSI}] []} {map[persistentvolumeclaims:{{10 0} {<nil>} 10 DecimalSI} pods:{{2 0} {<nil>} 2 DecimalSI}] map[persistentvolumeclaims:{{0 0} {<nil>} 0 DecimalSI} pods:{{0 0} {<nil>} 0 DecimalSI}]}}]
```
如果在一个namespace中创建了多个quota，获取到的quotas也会有多个。

### checkQuotas
调用func checkRequest()累加每个请求的资源量，并更新发生更新的quota
```go
// checkQuotas checks the admission attributes against the passed quotas.  If a quota applies, it will attempt to update it
// AFTER it has checked all the admissionAttributes.  The method breaks down into phase like this:
// 0. make a copy of the quotas to act as a "running" quota so we know what we need to update and can still compare against the
//    originals
// 1. check each admission attribute to see if it fits within *all* the quotas.  If it doesn't fit, mark the waiter as failed
//    and the running quota don't change.  If it did fit, check to see if any quota was changed.  It there was no quota change
//    mark the waiter as succeeded.  If some quota did change, update the running quotas
// 2. If no running quota was changed, return now since no updates are needed.
// 3. for each quota that has changed, attempt an update.  If all updates succeeded, update all unset waiters to success status and return.  If the some
//    updates failed on conflict errors and we have retries left, re-get the failed quota from our cache for the latest version
//    and recurse into this method with the subset.  It's safe for us to evaluate ONLY the subset, because the other quota
//    documents for these waiters have already been evaluated.  Step 1, will mark all the ones that should already have succeeded.

func (e *quotaEvaluator) checkQuotas(quotas []api.ResourceQuota, admissionAttributes []*admissionWaiter, remainingRetries int) {
	// yet another copy to compare against originals to see if we actually have deltas
	originalQuotas, err := copyQuotas(quotas)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}

	atLeastOneChanged := false
	for i := range admissionAttributes {
		admissionAttribute := admissionAttributes[i]
		//计算resource quota，如果超出配额，则err不为空
		newQuotas, err := e.checkRequest(quotas, admissionAttribute.attributes)
		//如果err不为空（如超出配额），则把拒绝该请求，并开始轮询下一个请求
		if err != nil {
			admissionAttribute.result = err
			continue
		}

		// if the new quotas are the same as the old quotas, then this particular one doesn't issue any updates
		// that means that no quota docs applied, so it can get a pass
		atLeastOneChangeForThisWaiter := false
		//检查是否有quota存在更新
		for j := range newQuotas {
			if !quota.Equals(quotas[j].Status.Used, newQuotas[j].Status.Used) {
				atLeastOneChanged = true
				atLeastOneChangeForThisWaiter = true
				break
			}
		}

		if !atLeastOneChangeForThisWaiter {
			admissionAttribute.result = nil
		}

		quotas = newQuotas
	}

	// if none of the requests changed anything, there's no reason to issue an update, just fail them all now
	if !atLeastOneChanged {
		return
	}

	// now go through and try to issue updates.  Things get a little weird here:
	// 1. check to see if the quota changed.  If not, skip.
	// 2. if the quota changed and the update passes, be happy
	// 3. if the quota changed and the update fails, add the original to a retry list
	var updatedFailedQuotas []api.ResourceQuota
	var lastErr error
	for i := range quotas {
		newQuota := quotas[i]

		// if this quota didn't have its status changed, skip it
		if quota.Equals(originalQuotas[i].Status.Used, newQuota.Status.Used) {
			continue
		}

		//更新resourceQuota
		if err := e.quotaAccessor.UpdateQuotaStatus(&newQuota); err != nil {
			updatedFailedQuotas = append(updatedFailedQuotas, newQuota)
			lastErr = err
		}
	}

	//resource quota更新成功
	if len(updatedFailedQuotas) == 0 {
		// all the updates succeeded.  At this point, anything with the default deny error was just waiting to
		// get a successful update, so we can mark and notify
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = nil
			}
		}
		return
	}

	// at this point, errors are fatal.  Update all waiters without status to failed and return
	//resource quota更新不成功
	if remainingRetries <= 0 {
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = lastErr
			}
		}
		return
	}

	// this retry logic has the same bug that its possible to be checking against quota in a state that never actually exists where
	// you've added a new documented, then updated an old one, your resource matches both and you're only checking one
	// updates for these quota names failed.  Get the current quotas in the namespace, compare by name, check to see if the
	// resource versions have changed.  If not, we're going to fall through an fail everything.  If they all have, then we can try again
	newQuotas, err := e.quotaAccessor.GetQuotas(quotas[0].Namespace)
	if err != nil {
		// this means that updates failed.  Anything with a default deny error has failed and we need to let them know
		for _, admissionAttribute := range admissionAttributes {
			if IsDefaultDeny(admissionAttribute.result) {
				admissionAttribute.result = lastErr
			}
		}
		return
	}

	// this logic goes through our cache to find the new version of all quotas that failed update.  If something has been removed
	// it is skipped on this retry.  After all, you removed it.
	quotasToCheck := []api.ResourceQuota{}
	for _, newQuota := range newQuotas {
		for _, oldQuota := range updatedFailedQuotas {
			if newQuota.Name == oldQuota.Name {
				quotasToCheck = append(quotasToCheck, newQuota)
				break
			}
		}
	}
	e.checkQuotas(quotasToCheck, admissionAttributes, remainingRetries-1)
}
```

### checkRequest
checkRequest() 计算请求的所需资源量，并进行配额比较，如果已使用资源+申请资源<配额，则返回新的已使用资源。 
把某请求的资源量累加到namespace下的quota(可能是多个)中。
```go
func (e *quotaEvaluator) checkRequest(quotas []api.ResourceQuota, a admission.Attributes) ([]api.ResourceQuota, error) {
	namespace := a.GetNamespace()
	evaluators := e.registry.Evaluators()
	//获取该请求所需要的evaluator
	evaluator, found := evaluators[a.GetKind().GroupKind()]
	if !found {
		return quotas, nil
	}

	//获取evaluator下对应op下所支持的配额项
	op := a.GetOperation()
	operationResources := evaluator.OperationResources(op)
	if len(operationResources) == 0 {
		return quotas, nil
	}

	// find the set of quotas that are pertinent to this request
	// reject if we match the quota, but usage is not calculated yet
	// reject if the input object does not satisfy quota constraints
	// if there are no pertinent quotas, we can just return
	inputObject := a.GetObject()
	interestingQuotaIndexes := []int{}
	for i := range quotas {
		resourceQuota := quotas[i]
		//查看evaluator所涉及到的资源是否resourceQuota相关，如果不相关，则continue
		match := evaluator.Matches(&resourceQuota, inputObject)
		if !match {
			continue
		}

		hardResources := quota.ResourceNames(resourceQuota.Status.Hard)
		evaluatorResources := evaluator.MatchesResources()
		requiredResources := quota.Intersection(hardResources, evaluatorResources)
		//检查inputObject中的资源是否符合要求
		if err := evaluator.Constraints(requiredResources, inputObject); err != nil {
			return nil, admission.NewForbidden(a, fmt.Errorf("failed quota: %s: %v", resourceQuota.Name, err))
		}
		if !hasUsageStats(&resourceQuota) {
			return nil, admission.NewForbidden(a, fmt.Errorf("status unknown for quota: %s", resourceQuota.Name))
		}

		//标记所涉及到的quota
		interestingQuotaIndexes = append(interestingQuotaIndexes, i)
	}
	if len(interestingQuotaIndexes) == 0 {
		return quotas, nil
	}

	// Usage of some resources cannot be counted in isolation. For example, when
	// the resource represents a number of unique references to external
	// resource. In such a case an evaluator needs to process other objects in
	// the same namespace which needs to be known.
	if accessor, err := meta.Accessor(inputObject); namespace != "" && err == nil {
		if accessor.GetNamespace() == "" {
			accessor.SetNamespace(namespace)
		}
	}

	// there is at least one quota that definitely matches our object
	// as a result, we need to measure the usage of this object for quota
	// on updates, we need to subtract the previous measured usage
	// if usage shows no change, just return since it has no impact on quota
	//计算请求资源
	deltaUsage := evaluator.Usage(inputObject)

	// ensure that usage for input object is never negative (this would mean a resource made a negative resource requirement)
	//检测deltaUsage中的项是否为负
	if negativeUsage := quota.IsNegative(deltaUsage); len(negativeUsage) > 0 {
		return nil, admission.NewForbidden(a, fmt.Errorf("quota usage is negative for resource(s): %s", prettyPrintResourceNames(negativeUsage)))
	}

	//Update的情况的deltaUsage=请求资源-已经拥有资源
	if admission.Update == op {
		prevItem := a.GetOldObject()
		if prevItem == nil {
			return nil, admission.NewForbidden(a, fmt.Errorf("unable to get previous usage since prior version of object was not found"))
		}

		// if we can definitively determine that this is not a case of "create on update",
		// then charge based on the delta.  Otherwise, bill the maximum
		metadata, err := meta.Accessor(prevItem)
		if err == nil && len(metadata.GetResourceVersion()) > 0 {
			prevUsage := evaluator.Usage(prevItem)
			deltaUsage = quota.Subtract(deltaUsage, prevUsage)
		}
	}
	if quota.IsZero(deltaUsage) {
		return quotas, nil
	}

	outQuotas, err := copyQuotas(quotas)
	if err != nil {
		return nil, err
	}

	for _, index := range interestingQuotaIndexes {
		resourceQuota := outQuotas[index]

		hardResources := quota.ResourceNames(resourceQuota.Status.Hard)
		requestedUsage := quota.Mask(deltaUsage, hardResources)
		//请求资源和原来的resource quota相加
		newUsage := quota.Add(resourceQuota.Status.Used, requestedUsage)
		maskedNewUsage := quota.Mask(newUsage, quota.ResourceNames(requestedUsage))

		//比较
		if allowed, exceeded := quota.LessThanOrEqual(maskedNewUsage, resourceQuota.Status.Hard); !allowed {
			failedRequestedUsage := quota.Mask(requestedUsage, exceeded)
			failedUsed := quota.Mask(resourceQuota.Status.Used, exceeded)
			failedHard := quota.Mask(resourceQuota.Status.Hard, exceeded)
			return nil, admission.NewForbidden(a,
				fmt.Errorf("exceeded quota: %s, requested: %s, used: %s, limited: %s",
					resourceQuota.Name,
					prettyPrint(failedRequestedUsage),
					prettyPrint(failedUsed),
					prettyPrint(failedHard)))
		}

		// update to the new usage number
		outQuotas[index].Status.Used = newUsage
	}

	return outQuotas, nil
}
```

# 总结
1. type quotaEvaluator struct是进行具体quota检测的主体
2. quotaEvaluator.work中是以ns为key来存放request，然后一次性取走一个ns的所有request来进行处理
3. Evaluate()->run()->doWork()->checkAttributes()-->checkQuotas()-->checkRequest()





