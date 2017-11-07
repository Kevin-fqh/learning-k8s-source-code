# Admission Control机制之Resource Quota

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
```go
type quotaEvaluator struct {
	quotaAccessor QuotaAccessor
	// lockAquisitionFunc acquires any required locks and returns a cleanup method to defer
	lockAquisitionFunc func([]api.ResourceQuota) func()

	// registry that knows how to measure usage for objects
	registry quota.Registry

	// TODO these are used together to bucket items by namespace and then batch them up for processing.
	// The technique is valuable for rollup activities to avoid fanout and reduce resource contention.
	// We could move this into a library if another component needed it.
	// queue is indexed by namespace, so that we bundle up on a per-namespace basis
	queue      *workqueue.Type
	workLock   sync.Mutex
	work       map[string][]*admissionWaiter
	dirtyWork  map[string][]*admissionWaiter
	inProgress sets.String

	// controls the run method so that we can cleanly conform to the Evaluator interface
	workers int
	stopCh  <-chan struct{}
	init    sync.Once
}
```
- type attributesRecord struct
```go
type attributesRecord struct {
	kind        unversioned.GroupVersionKind
	namespace   string
	name        string
	resource    unversioned.GroupVersionResource
	subresource string
	operation   Operation
	object      runtime.Object
	oldObject   runtime.Object
	userInfo    user.Info
}
```



