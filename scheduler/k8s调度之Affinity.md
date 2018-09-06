# k8s调度之Affinity

基于 V1.7.9 

## 调度策略简介

分为预选和优选两个阶段

- 预选策略，Predicates是强制性规则，遍历所有的Node节点，按照具体的预选策略筛选出符合要求的Node列表，如没有Node符合Predicates策略规则，那该Pod就会被挂起，直到有Node能够满足
- 优选策略，在第一步筛选的基础上，按照优选策略为待选Node打分排序，获取最优者

### 预选策略

默认的Predicates策略有11个

- NoDiskConflict：检查在此主机上是否存在卷冲突。如果这个主机已经挂载了卷，其它同样使用这个卷的Pod不能调度到这个主机上，不同的存储后端具体规则不同
- NoVolumeZoneConflict：检查给定的zone限制前提下，检查如果在此主机上部署Pod是否存在卷冲突
- PodToleratesNodeTaints：确保pod定义的tolerates能接纳node定义的taints
- CheckNodeMemoryPressure：检查pod是否可以调度到已经报告了主机内存压力过大的节点
- CheckNodeDiskPressure：检查pod是否可以调度到已经报告了主机的存储压力过大的节点
- MaxEBSVolumeCount：确保已挂载的EBS存储卷不超过设置的最大值，默认39
- MaxGCEPDVolumeCount：确保已挂载的GCE存储卷不超过设置的最大值，默认16
- MaxAzureDiskVolumeCount：确保已挂载的Azure存储卷不超过设置的最大值，默认16
- MatchInterPodAffinity：检查pod和其他pod是否符合亲和性规则
- GeneralPredicates：检查pod与主机上kubernetes相关组件是否匹配
- NoVolumeNodeConflict：检查给定的Node限制前提下，检查如果在此主机上部署Pod是否存在卷冲突

还有部分注册但默认不加载的策略，用户可自行选择使用

- MatchNodeSelector：检查Node节点的label定义是否满足Pod的NodeSelector属性需求
- PodFitsResources：检查主机的资源是否满足Pod的需求，根据实际已经分配（Limit）的资源量做调度，而不是使用已实际使用的资源量做调度
- PodFitsPorts(拟停用)、PodFitsHostPorts：检查Pod内每一个容器所需的HostPort是否已被其它容器占用，如果有所需的HostPort不满足需求，那么Pod不能调度到这个主机上
- HostName：检查主机名称是不是Pod指定的NodeName

### 优选策略

默认的Priority策略有7个

- LeastRequestedPriority：计算Pods需要的CPU和内存在当前节点可用资源的百分比，具有最小百分比的节点就是最优，得分计算公式：`cpu((capacity – sum(requested)) * 10 / capacity) + memory((capacity – sum(requested)) * 10 / capacity) / 2`
- BalancedResourceAllocation：节点上各项资源（CPU、内存）使用率最均衡的为最优，得分计算公式：10 – abs(totalCpu/cpuNodeCapacity-totalMemory/memoryNodeCapacity)*10
- ServiceSpreadingPriority(拟停用)、SelectorSpreadPriority：按Service和Replicaset归属计算Node上分布最少的同类Pod数量，得分计算：数量越少得分越高
- NodePreferAvoidPodsPriority：判断alpha.kubernetes.io/preferAvoidPods属性，设置权重为10000，覆盖其他策略
- NodeAffinityPriority：节点亲和性选择策略，提供两种选择器支持：requiredDuringSchedulingIgnoredDuringExecution（必须部署到满足条件的节点上，如果没有满足条件的节点，就不断重试）、preferresDuringSchedulingIgnoredDuringExecution（优先部署在满足条件的节点上，如果没有满足条件的节点，就忽略这些条件，按照正常逻辑部署）
- TaintTolerationPriority：类似于Predicates策略中的PodToleratesNodeTaints，优先调度到标记了Taint的节点
- InterPodAffinityPriority：pod亲和性选择策略，类似NodeAffinityPriority，提供两种选择器支持: requiredDuringSchedulingIgnoredDuringExecution（保证所选的主机必须满足所有Pod对主机的规则要求）、preferresDuringSchedulingIgnoredDuringExecution（调度器会尽量但不保证满足NodeSelector的所有要求），两个子策略：podAffinity和podAntiAffinity

	
同理，还有部分注册但默认不加载的策略，用户可自行选择使用

- EqualPriority：所有节点同样优先级，无实际效果
- ImageLocalityPriority：根据主机上是否已具备Pod运行的环境来打分，得分计算：不存在所需镜像，返回0分，存在镜像，镜像越大得分越高
- MostRequestedPriority：动态伸缩集群环境比较适用，会优先调度pod到使用率最高的主机节点，这样在伸缩集群时，就会腾出空闲机器，从而进行停机处理
	
## 调度之Affinity

亲和性策略（Affinity），实际上就是预选策略`MatchNodeSelector`和`MatchInterPodAffinity`、优选策略中的`NodeAffinityPriority`策略和`InterPodAffinityPriority`策略的具体应用。

`GeneralPredicates`中会用到`MatchNodeSelector`，对应了NodeAffinity的硬约束部分。 
而`MatchInterPodAffinity`则对应了PodAffinity的硬约束部分。

| 项目 | Node | Pod |
| ------| ------ | ------ |
| 硬性 | MatchNodeSelector | MatchInterPodAffinity |
| 软性 | NodeAffinityPriority | InterPodAffinityPriority | 


Affinity能够提供比NodeSelector或者Taints更灵活丰富的调度方式，例如：

- 丰富的匹配表达式（In, NotIn, Exists, DoesNotExist. Gt, and Lt）
- 软约束和硬约束（Required/Preferred），在软策略的情况下，如果没有满足调度条件的节点，pod 会忽略这条规则，继续完成调度过程
- 以节点上的其他Pod作为参照物进行调度计算
	
### NodeAffinity 用法

`Node Affinity`目的是在调度的时候让Pod可以灵活地选择Node

`NodeAffinityPriority`提供两种选择器支持：

- requiredDuringSchedulingIgnoredDuringExecution，必须部署到满足条件的节点上，如果没有满足条件的节点，就不断重试
- preferresDuringSchedulingIgnoredDuringExecution，优先部署在满足条件的节点上，如果没有满足条件的节点，就忽略这些条件，按照正常逻辑部署
	
`IgnoredDuringExecution`，pod部署之后运行的时候，如果节点label发生了变化，不再满足pod指定的条件，pod也会继续运行。

与之对应的是 `RequiredDuringExecution`，如果运行的pod所在节点不再满足条件，k8s会把pod从该节点中删除，重新选择符合要求的节点。

据官方说法未来`NodeSeletor`策略会被废弃，由NodeAffinityPriority策略中`requiredDuringSchedulingIgnoredDuringExecution`替代。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: with-node-affinity
spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: kubernetes.io/e2e-az-name
            operator: In
            values:
            - e2e-az1
            - e2e-az2
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 1
        preference:
          matchExpressions:
          - key: another-node-label-key
            operator: NotIn
            values:
            - another-node-label-value-1
            - another-node-label-value-2
  containers:
  - name: with-node-affinity
    image: k8s.gcr.io/pause:2.0
```

1. `requiredDuringSchedulingIgnoredDuringExecution`，硬约束，效果等同`NodeSeletor`，pod只能被调度到具有label `kubernetes.io/e2e-az-name=e2e-az1` 或`kubernetes.io/e2e-az-name=e2e-az2`的Node上

2. `preferredDuringSchedulingIgnoredDuringExecution`，软约束，不一定满足。k8s会尽量不把pod调度到具有label `another-node-label-key=another-node-label-value-1`或`another-node-label-key=another-node-label-value-2`的Node上

3. 如果同时定义了nodeSelector和nodeAffinity，那么必须两个条件都得到满足，Pod才能最终运行在指定的Node上

3. 如果`nodeAffinity`中有多个`nodeSelectorTerms`，如果节点满足任何一个就可以。 nodeSelectorTerms属性是设置要调度到的Node的标签的

4. 如果一个`nodeSelectorTerms`中有多个`matchExpressions`，一个Node必须满足所有的matchExpressions才能匹配成功

5. NodeAffinity语法支持的操作符有`In、NotIn、Exists、DoesNotExist、Gt、Lt`。 虽然没有节点排斥功能，但是用NotIn和DoesNotExist就可以实现排斥功能了。

```go
	// Affinity is a group of affinity scheduling rules.
	type Affinity struct {
		// Describes node affinity scheduling rules for the pod.
		// +optional
		NodeAffinity *NodeAffinity
		// Describes pod affinity scheduling rules (e.g. co-locate this pod in the same node, zone, etc. as some other pod(s)).
		// +optional
		PodAffinity *PodAffinity
		// Describes pod anti-affinity scheduling rules (e.g. avoid putting this pod in the same node, zone, etc. as some other pod(s)).
		// +optional
		PodAntiAffinity *PodAntiAffinity
	}

	type NodeAffinity struct {
		RequiredDuringSchedulingIgnoredDuringExecution *NodeSelector
	
		PreferredDuringSchedulingIgnoredDuringExecution []PreferredSchedulingTerm
	}
	
	type PreferredSchedulingTerm struct {
		// Weight associated with matching the corresponding nodeSelectorTerm, in the range 1-100.
		Weight int32
		// A node selector term, associated with the corresponding weight.
		Preference NodeSelectorTerm
	}
	
	// A null or empty node selector term matches no objects.
	type NodeSelectorTerm struct {
		//Required. A list of node selector requirements. The requirements are ANDed.
		MatchExpressions []NodeSelectorRequirement
	}
	
	// A node selector requirement is a selector that contains values, a key, and an operator
	// that relates the key and values.
	type NodeSelectorRequirement struct {
		// The label key that the selector applies to.
		Key string
		// Represents a key's relationship to a set of values.
		// Valid operators are In, NotIn, Exists, DoesNotExist. Gt, and Lt.
		Operator NodeSelectorOperator
		Values []string
	}
```

#### 预选策略-MatchNodeSelector

The pod can only schedule onto nodes that satisfy requirements in both `NodeAffinity and nodeSelector`.

`GeneralPredicates`中会用到`MatchNodeSelector`。

见`/plugin/pkg/scheduler/algorithm/predicates/predicates.go`

```go
// The pod can only schedule onto nodes that satisfy requirements in both NodeAffinity and nodeSelector.
// 预选：一个pod是否能调度到一个node上
func podMatchesNodeLabels(pod *v1.Pod, node *v1.Node) bool {
	// Check if node.Labels match pod.Spec.NodeSelector.
	/*
		pod.Spec.NodeSelector必须和node.Labels匹配
		否则false
	*/
	if len(pod.Spec.NodeSelector) > 0 {
		selector := labels.SelectorFromSet(pod.Spec.NodeSelector)
		if !selector.Matches(labels.Set(node.Labels)) {
			return false
		}
	}

	nodeAffinityMatches := true
	affinity := schedulercache.ReconcileAffinity(pod)
	if affinity != nil && affinity.NodeAffinity != nil {
		nodeAffinity := affinity.NodeAffinity
		// if no required NodeAffinity requirements, will do no-op, means select all nodes.
		// TODO: Replace next line with subsequent commented-out line when implement RequiredDuringSchedulingRequiredDuringExecution.
		/*
			如果nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution为nil，所有node都match
		*/
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			// if nodeAffinity.RequiredDuringSchedulingRequiredDuringExecution == nil && nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			return true
		}

		// Match node selector for requiredDuringSchedulingIgnoredDuringExecution.
		/*
			判断nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms中的matchExpressions，
			是否和node相匹配
		*/
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			nodeSelectorTerms := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
			glog.V(10).Infof("Match for RequiredDuringSchedulingIgnoredDuringExecution node selector terms %+v", nodeSelectorTerms)
			nodeAffinityMatches = nodeAffinityMatches && nodeMatchesNodeSelectorTerms(node, nodeSelectorTerms)
		}

	}
	return nodeAffinityMatches
}
```

#### 优选策略-NodeAffinityPriority

NodeAffinityPriority的源码解析如下：

```go
		// Prioritizes nodes that have labels matching NodeAffinity
		/* 
			使用MapReduce方式，先使用Map计算每个node的初始得分（满足NodeAffinity的PreferredDuringSchedulingIgnoredDuringExecution的node会累加 preferredSchedulingTerm.Weight），
			然后通过Reduce把得分映射到0-10的分数段。
			权重是1。
		*/
		factory.RegisterPriorityFunction2("NodeAffinityPriority", priorities.CalculateNodeAffinityPriorityMap, priorities.CalculateNodeAffinityPriorityReduce, 1),
```

`/kubernetes-1.7.9/plugin/pkg/scheduler/algorithm/priorities/node_affinity.go`

```go
// CalculateNodeAffinityPriority prioritizes nodes according to node affinity scheduling preferences
// indicated in PreferredDuringSchedulingIgnoredDuringExecution. Each time a node match a preferredSchedulingTerm,
// it will a get an add of preferredSchedulingTerm.Weight. Thus, the more preferredSchedulingTerms
// the node satisfies and the more the preferredSchedulingTerm that is satisfied weights, the higher
// score the node gets.
/*
	CalculateNodeAffinityPriority根据PreferredDuringSchedulingIgnoredDuringExecution中指示的节点关联性对Node进行优先级排序。
	每次Node匹配preferredSchedulingTerm时，该Node将增加分数 preferredSchedulingTerm.Weight。
*/
func CalculateNodeAffinityPriorityMap(pod *v1.Pod, meta interface{}, nodeInfo *schedulercache.NodeInfo) (schedulerapi.HostPriority, error) {
	node := nodeInfo.Node()
	if node == nil {
		return schedulerapi.HostPriority{}, fmt.Errorf("node not found")
	}

	var affinity *v1.Affinity
	if priorityMeta, ok := meta.(*priorityMetadata); ok {
		affinity = priorityMeta.affinity
	} else {
		// We couldn't parse metadata - fallback to the podspec.
		affinity = schedulercache.ReconcileAffinity(pod)
	}

	var count int32
	// A nil element of PreferredDuringSchedulingIgnoredDuringExecution matches no objects.
	// An element of PreferredDuringSchedulingIgnoredDuringExecution that refers to an
	// empty PreferredSchedulingTerm matches all objects.
	if affinity != nil && affinity.NodeAffinity != nil && affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution != nil {
		// Match PreferredDuringSchedulingIgnoredDuringExecution term by term.
		/*
			遍历 affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
		*/
		for i := range affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			preferredSchedulingTerm := &affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[i]
			//yaml 中preferredSchedulingTerm.Weight，略过
			if preferredSchedulingTerm.Weight == 0 {
				continue
			}

			// TODO: Avoid computing it for all nodes if this becomes a performance problem.
			/*
				把[]NodeSelectorRequirement（包括key、opt、value） 转化为 labels.Selector
			*/
			nodeSelector, err := v1helper.NodeSelectorRequirementsAsSelector(preferredSchedulingTerm.Preference.MatchExpressions)
			if err != nil {
				return schedulerapi.HostPriority{}, err
			}
			/*
				比较node.Labels，判断该Node是否符合优选要求
			*/
			if nodeSelector.Matches(labels.Set(node.Labels)) {
				count += preferredSchedulingTerm.Weight
			}
		}
	}

	return schedulerapi.HostPriority{
		Host:  node.Name,
		Score: int(count),
	}, nil
}

func CalculateNodeAffinityPriorityReduce(pod *v1.Pod, meta interface{}, nodeNameToInfo map[string]*schedulercache.NodeInfo, result schedulerapi.HostPriorityList) error {
	var maxCount int
	/*
		找出所有Node中在NodeAffinityPriority计算中的最高分数maxCount
	*/
	for i := range result {
		if result[i].Score > maxCount {
			maxCount = result[i].Score
		}
	}
	maxCountFloat := float64(maxCount)

	var fScore float64
	/*
		把每个Node的在NodeAffinityPriority计算的分数映射到 `0-10`的分数段，return
	*/
	for i := range result {
		if maxCount > 0 {
			fScore = 10 * (float64(result[i].Score) / maxCountFloat)
		} else {
			fScore = 0
		}
		if glog.V(10) {
			// We explicitly don't do glog.V(10).Infof() to avoid computing all the parameters if this is
			// not logged. There is visible performance gain from it.
			glog.Infof("%v -> %v: NodeAffinityPriority, Score: (%d)", pod.Name, result[i].Host, int(fScore))
		}
		result[i].Score = int(fScore)
	}
	return nil
}
```

### PodAffinity 用法

PodAffinity是干嘛的呢？
简单来说，就说根据Node上运行的Pod的Label来进行调度匹配的规则，匹配的表达式有：`In, NotIn, Exists, DoesNotExist`，通过该策略，可以更灵活地对Pod进行调度。
调度能够考虑pod之间的关系，而不仅仅是pod-node的关系。

举个例子，系统服务A和服务B尽量部署在同个主机、机房，因为它们网络沟通比较多。
再比如，数据服务C和数据服务D尽量分开，因为如果它们分配到一起，然后主机或者机房出了问题，会导致应用完全不可用；如果它们是分开的，应用虽然有影响，但还是可用的。

- InterPodAffinityPriority策略有podAffinity和podAntiAffinity两种配置方式。
- 和node affinity相似，InterPodAffinityPriority也有requiredDuringSchedulingIgnoredDuringExecution和preferredDuringSchedulingIgnoredDuringExecution，意义也和之前一样。
- 与node affinity不同的是：InterPodAffinityPriority策略是依据Pod的Label进行调度，所以会受到namespace约束。

下面的yaml文件，其调度目标为

- podAffinity+requiredDuringSchedulingIgnoredDuringExecution，pod必须要调度到某个zone（通过`failure-domain.beta.kubernetes.io/zone`指定），这个zone至少有一个节点上运行了这样的pod：这个pod有`security:S1`label
- podAntiAffinity+preferredDuringSchedulingIgnoredDuringExecution，最好不要调度到这样的节点，这个节点上运行了某个pod，而且这个pod有`security:S2`label。

如果把podAntiAffinity中的`topologyKey: kubernetes.io/hostname`换成`topologyKey: failure-domain.beta.kubernetes.io/zone`，意味着不可以把一个pod调度到一个Node上，该Node所处的zone中有一个pod：这个pod有`security:S2`label。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: with-pod-affinity
spec:
  affinity:
    podAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchExpressions: # 要匹配的pod的，标签定义，如果定义了多个matchExpressions，则所有标签必须同时满足。
          - key: security
            operator: In
            values:
            - S1
        topologyKey: failure-domain.beta.kubernetes.io/zone # 节点所属拓朴域
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
            - key: security
              operator: In
              values:
              - S2
          topologyKey: kubernetes.io/hostname
  containers:
  - name: with-pod-affinity
    image: k8s.gcr.io/pause:2.0
```

在labelSelector和topologyKey的同级，还可以定义`namespaces`列表，表示匹配哪些namespace里面的pod。
默认情况下，会匹配定义的pod所在的namespace。
如果定义了这个字段，但是它的值为空，则匹配所有的namespaces。

#### topologyKey

k8s的PodAffinity中加入了一个TopologyKey概念，和其他的predicate算法不同的是，一般算法只关注当前node自身的状态（即拓扑域只到节点），而PodAffinity还需要`全部existingPod信息`，从而找出符合Affinity条件的pod，再比较符合条件的pod所在的node和当前node是否在同一个拓扑域（即TopologyKey的键值都相同）

- `topologyKey`用于定于调度时作用于特指域，这是通过`Node节点的标签`来实现的，例如指定为kubernetes.io/hostname，那就是以Node节点hostname为区分范围，如果指定为beta.kubernetes.io/os,则以Node节点的操作系统类型来区分。`k8s内置的lable`有

	```
	const (
		LabelHostname          = "kubernetes.io/hostname"
		LabelZoneFailureDomain = "failure-domain.beta.kubernetes.io/zone"
		LabelZoneRegion        = "failure-domain.beta.kubernetes.io/region"
	
		LabelInstanceType = "beta.kubernetes.io/instance-type"
	
		LabelOS   = "beta.kubernetes.io/os"
		LabelArch = "beta.kubernetes.io/arch"
	)
	```
topologyKey的使用约束：

- 除了podAntiAffinity的preferredDuringSchedulingIgnoredDuringExecution，其他模式下的topologyKey不能为空。
- 如果Admission Controller中添加了LimitPodHardAntiAffinityTopology，那么podAntiAffinity的requiredDuringSchedulingIgnoredDuringExecution被强制约束为kubernetes.io/hostname。
- 如果podAntiAffinity的preferredDuringSchedulingIgnoredDuringExecution中的topologyKey为空，则默认为适配kubernetes.io/hostname,failure-domain.beta.kubernetes.io/zone,failure-domain.beta.kubernetes.io/region。

#### podAffinity的对称性

假设Pod A中有对Pod B的AntiAffinity，如果Pod A先运行在某个node上，在调度Pod B时即使Pod B的Spec中没有写对Pod A的AntiAffinity，由于Pod A的AntiAffinity，也是不能调度在运行Pod A的那个node上的。

#### 预选策略-MatchInterPodAffinity

见`/plugin/pkg/scheduler/algorithm/predicates/predicates.go`

```go
func (c *PodAffinityChecker) InterPodAffinityMatches(pod *v1.Pod, meta interface{}, nodeInfo *schedulercache.NodeInfo) (bool, []algorithm.PredicateFailureReason, error) {
	/*
		首先使用meta中的matchingAntiAffinityTerms（包含existingpod的anitAffinityTerm）检查`待调度pod`和当前node是否有冲突，
		之后再检查`待调度pod`的Affinity和AntiAffinity与当前node的关系。
	*/
	node := nodeInfo.Node()
	if node == nil {
		return false, nil, fmt.Errorf("node not found")
	}
	/*
		判断如果把新的pod调度到该node上面，
		是否会破坏了existing pods指定的anti-affinity rule
	*/
	if !c.satisfiesExistingPodsAntiAffinity(pod, meta, node) {
		return false, []algorithm.PredicateFailureReason{ErrPodAffinityNotMatch}, nil
	}

	// Now check if <pod> requirements will be satisfied on this node.
	affinity := schedulercache.ReconcileAffinity(pod)
	if affinity == nil || (affinity.PodAffinity == nil && affinity.PodAntiAffinity == nil) {
		return true, nil, nil
	}
	/*
		判断如果把一个pod调度到一个node上，是否会破坏`该待调度pod`指定的rule
	*/
	if !c.satisfiesPodsAffinityAntiAffinity(pod, node, affinity) {
		return false, []algorithm.PredicateFailureReason{ErrPodAffinityNotMatch}, nil
	}

	if glog.V(10) {
		// We explicitly don't do glog.V(10).Infof() to avoid computing all the parameters if this is
		// not logged. There is visible performance gain from it.
		glog.Infof("Schedule Pod %+v on Node %+v is allowed, pod (anti)affinity constraints satisfied",
			podName(pod), node.Name)
	}
	return true, nil, nil
}
```

#### 优选策略-InterPodAffinityPriority

```go
		// pods should be placed in the same topological domain (e.g. same node, same rack, same zone, same power domain, etc.)
		// as some other pods, or, conversely, should not be placed in the same topological domain as some other pods.
		/*
			pods 应该存在与相同的topological domain中，同时附加一些特征的pods。
			同理相反，should not be placed in the same topological domain as some other pods.
		*/
		factory.RegisterPriorityConfigFactory(
			"InterPodAffinityPriority",
			factory.PriorityConfigFactory{
				Function: func(args factory.PluginFactoryArgs) algorithm.PriorityFunction {
					return priorities.NewInterPodAffinityPriority(args.NodeInfo, args.NodeLister, args.PodLister, args.HardPodAffinitySymmetricWeight)
				},
				Weight: 1,
			},
		),
```

见`/plugin/pkg/scheduler/algorithm/priorities/interpod_affinity.go`

```go
// compute a sum by iterating through the elements of weightedPodAffinityTerm and adding
// "weight" to the sum if the corresponding PodAffinityTerm is satisfied for
// that node; the node(s) with the highest sum are the most preferred.
// Symmetry need to be considered for preferredDuringSchedulingIgnoredDuringExecution from podAffinity & podAntiAffinity,
// symmetry need to be considered for hard requirements from podAffinity
/*
	在PodAffinity中一般会进行双向检查，
		即待调度的pod的Affinity检查已存在pod，以及已存在pod的Affinity检查待调度pod。
*/
func (ipa *InterPodAffinity) CalculateInterPodAffinityPriority(pod *v1.Pod, nodeNameToInfo map[string]*schedulercache.NodeInfo, nodes []*v1.Node) (schedulerapi.HostPriorityList, error) {
	// pod:  待调度的pod
	affinity := schedulercache.ReconcileAffinity(pod)
	hasAffinityConstraints := affinity != nil && affinity.PodAffinity != nil
	hasAntiAffinityConstraints := affinity != nil && affinity.PodAntiAffinity != nil

	allNodeNames := make([]string, 0, len(nodeNameToInfo))
	for name := range nodeNameToInfo {
		allNodeNames = append(allNodeNames, name)
	}

	// convert the topology key based weights to the node name based weights
	var maxCount float64
	var minCount float64
	// priorityMap stores the mapping from node name to so-far computed score of
	// the node.
	/*
		创建了一个podAffinityPriorityMap对象pm，其中保存了node列表，node初始得分列表，和默认的失败域（node标签上的nodename，zone，region）
	*/
	pm := newPodAffinityPriorityMap(nodes)

	/*
		定义两个函数
			- processNode处理每个node
			- processPod处理每个node上的每个正在运行的pod
	*/
	processPod := func(existingPod *v1.Pod) error {
		/*
			两个阶段
		*/
		existingPodNode, err := ipa.info.GetNodeInfo(existingPod.Spec.NodeName)
		if err != nil {
			return err
		}
		existingPodAffinity := schedulercache.ReconcileAffinity(existingPod)
		existingHasAffinityConstraints := existingPodAffinity != nil && existingPodAffinity.PodAffinity != nil
		existingHasAntiAffinityConstraints := existingPodAffinity != nil && existingPodAffinity.PodAntiAffinity != nil

		/*
			1. 检查 待调度的pod
				检查了`待调度pod`的Affinity和AntiAffinity的PreferredDuringSchedulingIgnoredDuringExecution是否和existingPod的label匹配，
				如果匹配成功则会给当前existingPod所在node及相同拓扑域的所有node加上（AntiAffinity对应减去）1*Weight得分
		*/
		if hasAffinityConstraints {
			// For every soft pod affinity term of <pod>, if <existingPod> matches the term,
			// increment <pm.counts> for every node in the cluster with the same <term.TopologyKey>
			// value as that of <existingPods>`s node by the term`s weight.
			terms := affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			pm.processTerms(terms, pod, existingPod, existingPodNode, 1)
		}
		if hasAntiAffinityConstraints {
			// For every soft pod anti-affinity term of <pod>, if <existingPod> matches the term,
			// decrement <pm.counts> for every node in the cluster with the same <term.TopologyKey>
			// value as that of <existingPod>`s node by the term`s weight.
			terms := affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			pm.processTerms(terms, pod, existingPod, existingPodNode, -1)
		}

		/*
			2. 检查existingPod
				而对于existingPod检查`待调度pod`除了常规的PreferredDuringSchedulingIgnoredDuringExecution外，
				还特别检查了Affinity的 RequiredDuringSchedulingIgnoredDuringExecution。

				Require本应该都是出现在Predicate算法中，而在这Priority出现原因通过官方设计文档解读，是因为类似的对称性，
				这里特意给了这个Require一个特殊的参数hardPodAffinityWeight，这个参数是由DefaultProvider提供的（默认值是1）。
					==>https://github.com/kubernetes/community/blob/master/contributors/design-proposals/scheduling/podaffinity.md
						==>Special considerations for RequiredDuringScheduling affinity

				因此existingPod的RequiredDuringSchedulingIgnoredDuringExecution如果匹配到待调度pod，
				与其运行的node具有相同拓扑域的全部node都会增加hardPodAffinityWeight*Weight得分。
		*/
		if existingHasAffinityConstraints {
			// For every hard pod affinity term of <existingPod>, if <pod> matches the term,
			// increment <pm.counts> for every node in the cluster with the same <term.TopologyKey>
			// value as that of <existingPod>'s node by the constant <ipa.hardPodAffinityWeight>
			if ipa.hardPodAffinityWeight > 0 {
				terms := existingPodAffinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
				//if len(existingPodAffinity.PodAffinity.RequiredDuringSchedulingRequiredDuringExecution) != 0 {
				//	terms = append(terms, existingPodAffinity.PodAffinity.RequiredDuringSchedulingRequiredDuringExecution...)
				//}
				for _, term := range terms {
					pm.processTerm(&term, existingPod, pod, existingPodNode, float64(ipa.hardPodAffinityWeight))
				}
			}
			// For every soft pod affinity term of <existingPod>, if <pod> matches the term,
			// increment <pm.counts> for every node in the cluster with the same <term.TopologyKey>
			// value as that of <existingPod>'s node by the term's weight.
			terms := existingPodAffinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			pm.processTerms(terms, existingPod, pod, existingPodNode, 1)
		}
		if existingHasAntiAffinityConstraints {
			// For every soft pod anti-affinity term of <existingPod>, if <pod> matches the term,
			// decrement <pm.counts> for every node in the cluster with the same <term.TopologyKey>
			// value as that of <existingPod>'s node by the term's weight.
			terms := existingPodAffinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			pm.processTerms(terms, existingPod, pod, existingPodNode, -1)
		}
		return nil
	}
	processNode := func(i int) {
		nodeInfo := nodeNameToInfo[allNodeNames[i]]
		if hasAffinityConstraints || hasAntiAffinityConstraints {
			// We need to process all the nodes.
			for _, existingPod := range nodeInfo.Pods() {
				if err := processPod(existingPod); err != nil {
					pm.setError(err)
				}
			}
		} else {
			// The pod doesn't have any constraints - we need to check only existing
			// ones that have some.
			// 如果待调度的pod没有亲和/反亲和约束，那么只需要轮询`具有约束的existingPod`即可
			for _, existingPod := range nodeInfo.PodsWithAffinity() {
				if err := processPod(existingPod); err != nil {
					pm.setError(err)
				}
			}
		}
	}
	/*
		对于processNode使用workqueue.Parallelize并行执行方式，16
	*/
	workqueue.Parallelize(16, len(allNodeNames), processNode)
	if pm.firstError != nil {
		return nil, pm.firstError
	}

	for _, node := range nodes {
		if pm.counts[node.Name] > maxCount {
			maxCount = pm.counts[node.Name]
		}
		if pm.counts[node.Name] < minCount {
			minCount = pm.counts[node.Name]
		}
	}

	// calculate final priority score for each node
	/*
		把全部node得分映射到0-10
	*/
	result := make(schedulerapi.HostPriorityList, 0, len(nodes))
	for _, node := range nodes {
		fScore := float64(0)
		if (maxCount - minCount) > 0 {
			fScore = 10 * ((pm.counts[node.Name] - minCount) / (maxCount - minCount))
		}
		result = append(result, schedulerapi.HostPriority{Host: node.Name, Score: int(fScore)})
		if glog.V(10) {
			// We explicitly don't do glog.V(10).Infof() to avoid computing all the parameters if this is
			// not logged. There is visible performance gain from it.
			glog.V(10).Infof("%v -> %v: InterPodAffinityPriority, Score: (%d)", pod.Name, node.Name, int(fScore))
		}
	}
	return result, nil
}
```

## 参考
[scheduler 调度简介](https://blog.csdn.net/tiger435/article/details/73650123)

[kubernetes 亲和性调度](http://cizixs.com/2017/05/17/kubernetes-scheulder-affinity)

[深入kubernetes调度之Affinity](https://blog.csdn.net/tiger435/article/details/78489369)

[Node affinity and NodeSelector](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/scheduling/nodeaffinity.md)

[Inter-pod topological affinity and anti-affinity](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/scheduling/podaffinity.md)

[kube-scheduler原理解析](http://www.yidianzixun.com/article/0It3oMhq)
