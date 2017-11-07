# ResourceQuota概念介绍

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [目录结构](#目录结构)
  - [两个重要的interface](#两个重要的interface)
  - [type GenericRegistry struct](#type-genericregistry-struct)
  - [type GenericEvaluator struct](#type-genericevaluator-struct)
  - [type ResourceQuotaController struct](#type-resourcequotacontroller-struct)
  - [type replenishmentControllerFactory struct](#type-replenishmentcontrollerfactory-struct)
<!-- END MUNGE: GENERATED_TOC -->

## 目录结构
```go
/pkg/quota
.
├── evaluator    // 负责统计各种资源使用情况
│   └── core
│       ├── configmap.go   // ConfigMapEvaluator的实现，负责ConfigMap资源的统计
│       ├── persistent_volume_claims.go    // PVCEvaluator的实现，负责PVC资源的统计
│       ├── pods.go    //PodEvaluator的实现，负责Pod资源的统计
│       ├── registry.go    // 创建Registry时注册所有的Evaluators
│       ├── replication_controllers.go    // RCEvaluator的实现，负责rc资源的统计
│       ├── resource_quotas.go    // ResourceQuotaEvaluator的实现，负责ResourceQuota资源的统计
│       ├── secrets.go    // SecretEvaluator的实现，负责Secret资源的统计   
│       └── services.go  // ServiceEvaluator的实现，负责Service资源的统计
├── generic    // genericEvaluator的定义和实现
│   ├── evaluator.go    // 实现了genericEvaluator的接口，包括最重要的CalculateUsageStats接口
│   └── registry.go    // 定义了type GenericRegistry struct
├── install
│   └── registry.go    // 定义了startResourceQuotaController时会调用创建ResourceQuota Registry的方法
├── interfaces.go    // 定义了各种Interface，包括type Evaluator interface 和 type Registry interface
└── resources.go    // 定义Resources的集合操作以及CalculateUsage方法
```
其中interfaces.go定义了各种接口，对各个结构体的功能说明还是比较清楚的。

```go
/pkg/controller/resourcequota
.
├── replenishment_controller.go    // 定义replenishmentControllerFactory，用来创建replenishmentController 
└── resource_quota_controller.go    // 定义ResourceQuotaController及其Run方法，syncResourceQuota方法等，属于核心文件。
```

## 两个重要的interface
- type Evaluator interface
后面的GenericEvaluator就是实现了type Evaluator interface。 

```go
// Evaluator knows how to evaluate quota usage for a particular group kind
/*
	type Evaluator interface知道如何去计算一个指定group kind的quota usage
*/
type Evaluator interface {
	// Constraints ensures that each required resource is present on item
	Constraints(required []api.ResourceName, item runtime.Object) error
	// Get returns the object with specified namespace and name
	Get(namespace, name string) (runtime.Object, error)
	// GroupKind returns the groupKind that this object knows how to evaluate
	GroupKind() unversioned.GroupKind
	// MatchesResources is the list of resources that this evaluator matches
	MatchesResources() []api.ResourceName
	// Matches returns true if the specified quota matches the input item
	Matches(resourceQuota *api.ResourceQuota, item runtime.Object) bool
	// OperationResources returns the set of resources that could be updated for the
	// specified operation for this kind.  If empty, admission control will ignore
	// quota processing for the operation.
	OperationResources(operation admission.Operation) []api.ResourceName
	// Usage returns the resource usage for the specified object
	Usage(object runtime.Object) api.ResourceList
	// UsageStats calculates latest observed usage stats for all objects
	UsageStats(options UsageStatsOptions) (UsageStats, error)
}
```

- type Registry interface
```go
// Registry holds the list of evaluators associated to a particular group kind
/*
	type Registry interface 拥有一个指定group kind的evaluators
*/
type Registry interface {
	// Evaluators returns the set Evaluator objects registered to a groupKind
	Evaluators() map[unversioned.GroupKind]Evaluator
}
```

## type GenericRegistry struct
GenericRegistry的功能非常简单，就是记录了GroupKind和各个Evaluator的映射关系。
```go
// GenericRegistry implements Registry
type GenericRegistry struct {
	// internal evaluators by group kind
	/*
		记录了GroupKind(没有version)和Evaluator的映射关系
	*/
	InternalEvaluators map[unversioned.GroupKind]quota.Evaluator
}

// Evaluators returns the map of evaluators by groupKind
func (r *GenericRegistry) Evaluators() map[unversioned.GroupKind]quota.Evaluator {
	return r.InternalEvaluators
}
```

## type GenericEvaluator struct
type Evaluator interface的具体实现。 所谓的PodEvaluator、ServiceEvaluator...其实都是一个GenericEvaluator对象，只是其中的属性赋值不一样而已。
```go
// GenericEvaluator provides an implementation for quota.Evaluator
type GenericEvaluator struct {
	// Name used for logging
	Name string //表示该Evaluator的名称，如 "Evaluator.Pod", "Evaluator.Service"
	// The GroupKind that this evaluator tracks
	InternalGroupKind unversioned.GroupKind //表示该Evaluator所处理资源的InternalGroupKind；
	// The set of resources that are pertinent to the mapped operation
	/*
		//表示该Evaluator所支持的请求的类型，如Create, Update等及这些操作所支持的资源
	*/
	InternalOperationResources map[admission.Operation][]api.ResourceName
	// The set of resource names this evaluator matches
	MatchedResourceNames []api.ResourceName //表示该Evaluator所对应的资源名称，如ResourceCPU, ResourcePods等；
	// A function that knows how to evaluate a matches scope request
	/*
		resourcequota的scope判断函数。
		resourcequota只处理满足scope判断函数的请求(即只统计部分对象的配额)，
		目前有Terminating, NotTerminating, BestEffort, NotBestEffort这些Scope；
	*/
	MatchesScopeFunc MatchesScopeFunc
	// A function that knows how to return usage for an object
	/*
		用来计算对象所占资源
	*/
	UsageFunc UsageFunc
	// A function that knows how to list resources by namespace
	ListFuncByNamespace ListFuncByNamespace //对象List函数；
	// A function that knows how to get resource in a namespace
	// This function must be specified if the evaluator needs to handle UPDATE
	/*
		用于在一个namespace中get一个resource，
		如果一个evaluator需要处理UPDATE操作，那么本函数必须要实现
	*/
	GetFuncByNamespace GetFuncByNamespace
	// A function that checks required constraints are satisfied
	/*
		对对象申请的资源进行合理性检查，如requests<limits。
	*/
	ConstraintsFunc ConstraintsFunc
}
```

### func OperationResources()
 对于指定kind而言，func OperationResources(op)将返回可以被动作op修改的resources。 
 如果为空，admission control将忽略该op操作的配额处理。
```go
// OperationResources returns the set of resources that could be updated for the
// specified operation for this kind.  If empty, admission control will ignore
// quota processing for the operation.

func (g *GenericEvaluator) OperationResources(operation admission.Operation) []api.ResourceName {
	/*
		InternalOperationResources在创建具体的Evaluator的时候注册
			=>/pkg/quota/evaluator/core/pods.go
				=>func NewPodEvaluator
	*/
	return g.InternalOperationResources[operation]
}

//看看注册函数
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
			定义在 pkg/quota/generic/evaluator.go
				==>func ListResourceUsingInformerFunc
		*/
		listFuncByNamespace = generic.ListResourceUsingInformerFunc(f, unversioned.GroupResource{Resource: "pods"})
	}
	return &generic.GenericEvaluator{
		Name:              "Evaluator.Pod",
		InternalGroupKind: api.Kind("Pod"),
		/*
			这里表示对于Kind pod而言，仅仅需要检查Create动作即可
		*/
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

## type ResourceQuotaController struct
```go
// ResourceQuotaController is responsible for tracking quota usage status in the system
/*
	负责跟踪系统的quota usage status
*/
type ResourceQuotaController struct {
	// Must have authority to list all resources in the system, and update quota status
	kubeClient clientset.Interface //可以list系统中所有的resources，并且更新quota status
	// An index of resource quota objects by namespace
	rqIndexer cache.Indexer //索引，通过namespace作为key获取一个resource quota objects
	// Watches changes to all resource quota
	rqController *cache.Controller //用于watch所有的resource quota，根据情况将ResourceQuota加入到queue和missingUsageQueue。
	// ResourceQuota objects that need to be synchronized
	queue workqueue.RateLimitingInterface //存放待sync的ResourceQuota objects
	// missingUsageQueue holds objects that are missing the initial usage informatino
	missingUsageQueue workqueue.RateLimitingInterface //存放那些丢失了initial usage informatino的ResourceQuota objects
	// To allow injection of syncUsage for testing.
	syncHandler func(key string) error
	// function that controls full recalculation of quota usage
	/*
		默认5min会做一次全量的quota usage同步。
		可通过kube-controller-manager的--resource-quota-sync-period
	*/
	resyncPeriod controller.ResyncPeriodFunc
	// knows how to calculate usage
	registry quota.Registry //知道怎么计算usage，存放着各个Evaluator的映射map表
	// controllers monitoring to notify for replenishment
	/*
		用于监控各种资源的Update/Delete操作，以通知worker重新补充一个ResourceQuota objects，将ResourceQuota加入到queue
	*/
	replenishmentControllers []cache.ControllerInterface
}
```

## type replenishmentControllerFactory struct
用于提供各种资源的`replenishmentControllers`
```go
// replenishmentControllerFactory implements ReplenishmentControllerFactory
type replenishmentControllerFactory struct {
	kubeClient            clientset.Interface
	sharedInformerFactory informers.SharedInformerFactory
}
```




