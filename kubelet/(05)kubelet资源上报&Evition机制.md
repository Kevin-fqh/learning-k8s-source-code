# kubelet资源上报&Evition机制

## 版本
k8s v1.7.9

## 需要关注的参数

kube-controller-manager

```
--node-monitor-grace-period duration                                Amount of time which we allow running Node to be unresponsive before marking it unhealthy. Must be N times more than kubelet's nodeStatusUpdateFrequency, where N means number of retries allowed for kubelet to post node status. (default 40s)

--node-monitor-period duration                                      The period for syncing NodeStatus in NodeController. (default 5s)

--pod-eviction-timeout duration                                     The grace period for deleting pods on failed nodes. (default 5m0s)
```

kubelet

```
--node-status-update-frequency duration                   Specifies how often kubelet posts node status to master. Note: be cautious when changing the constant, it must work with nodeMonitorGracePeriod in nodecontroller. (default 10s)
```

下面所有时间从 0s 单独计算，互不干扰：

    * kubelet 间隔`--node-status-update-frequency duration`向apiserver上报node status信息
	
    * kube-controller-manager 间隔`node-monitor-grace-period`时间之后，把node状态设置为NotReady
	
    * kube-controller-manager 在第一次kubelet notReady事件之后的`pod-eviction-timeout duration`时间后，开始驱逐pod。并不是CM把node状态设置为notready之后再等待`pod-eviction-timeout duration`时间
	
    * kube-controller-manager 间隔`node-monitor-period`时间后从apiserver同步node status信息
	
其中kubelet的上报时间不宜过短，因为收集完整的node信息需要一定的时间。

## kubelet端
kubelet端会定期向apiserver上报node节点信息，具体流程如下：

1. 心跳信息的上报是由一个专门的client来负责的，和普通的kubeClient有点小区别，见`/kubernetes-1.7.9/cmd/kubelet/app/server.go`的`func run`。 禁用了throttling

```go
			// make a separate client for heartbeat with throttling disabled and a timeout attached
			/*
				建立一个单独的心跳监测client，禁用throttling、附加超时机制
				其实本质上和kubeClient没太大区别
				把qps设置为-1 就是禁用限流么？
					==>https://www.jianshu.com/p/6bd35188c1c6
			*/
			heartbeatClientConfig := *clientConfig
			heartbeatClientConfig.Timeout = s.KubeletConfiguration.NodeStatusUpdateFrequency.Duration
			heartbeatClientConfig.QPS = float32(-1)
			heartbeatClient, err = v1coregenerated.NewForConfig(&heartbeatClientConfig)
```

2. kubelet启动的时候，获取一次node status进行初始化注册

3. 后续间隔固定时间（默认10s），执行一次`func (kl *Kubelet) tryUpdateNodeStatus(tryNumber int)`更新node状态。 
在大型集群中，在本函数中对Node object的GET、PATCH操作是apiserver和etcd的主要负载。 
为了减少etcd上的负载，现在是从apiserver 的cache中提供GET操作（数据可能会稍微延迟，但它似乎不会引起更多冲突 - 延迟非常小）。 
如果发生了conflict，所有的重试都会从etcd开始。

	```go
	// tryUpdateNodeStatus tries to update node status to master. If ReconcileCBR0
	// is set, this function will also confirm that cbr0 is configured correctly.
	func (kl *Kubelet) tryUpdateNodeStatus(tryNumber int) error {
		// In large clusters, GET and PUT operations on Node objects coming
		// from here are the majority of load on apiserver and etcd.
		// To reduce the load on etcd, we are serving GET operations from
		// apiserver cache (the data might be slightly delayed but it doesn't
		// seem to cause more conflict - the delays are pretty small).
		// If it result in a conflict, all retries are served directly from etcd.
		/*
			在大型集群中，在本函数中对Node object的GET、PUT操作是apiserver和etcd的主要负载。
			为了减少etcd上的负载，GET操作会从apiserver 的cache中获取数据（数据可能会稍微延迟，但它似乎不会引起更多冲突 - 延迟非常小）。
			如果发生了conflict，所有的重试都会从etcd开始。
		*/
		opts := metav1.GetOptions{}
		if tryNumber == 0 {
			util.FromApiserverCache(&opts)
		}
		/*
			从apiserver的cache处进行GET操作，根据nodeName获取node信息
		*/
		node, err := kl.heartbeatClient.Nodes().Get(string(kl.nodeName), opts)
		if err != nil {
			return fmt.Errorf("error getting node %q: %v", kl.nodeName, err)
		}
	
		clonedNode, err := conversion.NewCloner().DeepCopy(node)
		if err != nil {
			return fmt.Errorf("error clone node %q: %v", kl.nodeName, err)
		}
	
		/*
			类型断言
			clonedNode是否实现了接口v1.Node
			originalNode相当于copy一份node的旧状态信息
		*/
		originalNode, ok := clonedNode.(*v1.Node)
		if !ok || originalNode == nil {
			return fmt.Errorf("failed to cast %q node object %#v to v1.Node", kl.nodeName, clonedNode)
		}
	
		kl.updatePodCIDR(node.Spec.PodCIDR)
	
		/*
			更新状态的核心函数
			直接对node进行了修改，这里node是一个指针
		*/
		kl.setNodeStatus(node)
		// Patch the current status on the API server
		/*
			PATCH请求
			提交到apiserver
			==>/pkg/util/node/node.go
				==>func PatchNodeStatus(c v1core.CoreV1Interface, nodeName types.NodeName, oldNode *v1.Node, newNode *v1.Node)
	
			[PATCH] 方法可以用来更新资源的一个组成部分。举个例子，当你仅需更新资源的某一项，
			[PUT] 一个完整的资源就显得很累赘同时会消耗更多带宽。
			[PUT] 方法是幂等的。对同一资源的多次 [PUT] 操作，不应该返回不同的资源，而对同一资源的多次 [POST] 可以生产多个资源。
			[PATCH] 即不完全也不幂等
			一个 API 实现 [PATCH] 必须是原子的。它一定不能出现只 [GET] 到被 [PATCH] 更新了一半的资源。
		*/
		updatedNode, err := nodeutil.PatchNodeStatus(kl.heartbeatClient, types.NodeName(kl.nodeName), originalNode, node)
		if err != nil {
			return err
		}
		// If update finishes successfully, mark the volumeInUse as reportedInUse to indicate
		// those volumes are already updated in the node's status
		kl.volumeManager.MarkVolumesAsReportedInUse(updatedNode.Status.VolumesInUse)
		return nil
	}
	```

4. 每次都会重新获取一次以下几种信息，进行上报。 这里涉及到的eviction机制需要和kubelet的OOM机制进行区分。kubelet的eviction机制，只有当节点内存和磁盘资源紧张时，才会开启，其目的就是为了回收node节点的资源。而oom-killer将Pod杀掉后，假如Pod的RestartPolicy设置为Always，则kubelet隔段时间后，仍然会在该节点上启动该Pod。而kublet eviction则会将该Pod从该节点上删除。

    * StatusInfo，包括host主机cpu等基本信息、本地images等
    * OODCondition，即out of disk space，和[DiskManager](https://segmentfault.com/a/1190000008339093)相关，要有足够的空间存放docker images，来create root fs
    * MemoryPressureCondition, 和[evictionManager](https://blog.csdn.net/WaltonWang/article/details/55804309)相关
    * DiskPressureCondition，和[evictionManager](https://blog.csdn.net/WaltonWang/article/details/55804309)相关
    * ReadyCondition，是否处于ready状态，汇报cri的运行状态，cni网络等情况
    * VolumesInUseStatus，和volumeManager相关
	
	```go
	// defaultNodeStatusFuncs is a factory that generates the default set of
	// setNodeStatus funcs
	/*
		注册kubelet默认的资源、status上报方法
	*/
	func (kl *Kubelet) defaultNodeStatusFuncs() []func(*v1.Node) error {
		// initial set of node status update handlers, can be modified by Option's
		withoutError := func(f func(*v1.Node)) func(*v1.Node) error {
			return func(n *v1.Node) error {
				f(n)
				return nil
			}
		}
		return []func(*v1.Node) error{
			kl.setNodeAddress,
			withoutError(kl.setNodeStatusInfo),
			withoutError(kl.setNodeOODCondition),
			withoutError(kl.setNodeMemoryPressureCondition),
			withoutError(kl.setNodeDiskPressureCondition),
			withoutError(kl.setNodeReadyCondition),
			withoutError(kl.setNodeVolumesInUseStatus),
			withoutError(kl.recordNodeSchedulableEvent),
		}
	}
	```
	
5. noded的状态

```go
// These are valid conditions of node. Currently, we don't have enough information to decide
// node condition. In the future, we will add more. The proposed set of conditions are:
// NodeReachable, NodeLive, NodeReady, NodeSchedulable, NodeRunnable.
const (
	// NodeReady means kubelet is healthy and ready to accept pods.
	NodeReady NodeConditionType = "Ready"
	// NodeOutOfDisk means the kubelet will not accept new pods due to insufficient free disk
	// space on the node.
	NodeOutOfDisk NodeConditionType = "OutOfDisk"
	// NodeMemoryPressure means the kubelet is under pressure due to insufficient available memory.
	NodeMemoryPressure NodeConditionType = "MemoryPressure"
	// NodeDiskPressure means the kubelet is under pressure due to insufficient available disk.
	NodeDiskPressure NodeConditionType = "DiskPressure"
	// NodeNetworkUnavailable means that network for the node is not correctly configured.
	NodeNetworkUnavailable NodeConditionType = "NetworkUnavailable"
	// NodeInodePressure means the kubelet is under pressure due to insufficient available inodes.
	NodeInodePressure NodeConditionType = "InodePressure"
)
```

## kube-controller-manager端

kube-controller-manager端涉及到这部分的重点是`nodeController`

### nodeController的Run()

1. WaitForCacheSync 函数等待 PodInformer、NodeInformer、DaemonSetInformer 的HasSyncs 都返回 true，完成同步，默认周期为100ms
2. nodeController会定期从apiserver中读取node status信息
3. 默认情况下都会启动Taint Manager
4. 默认TaintBasedEvictions=false，每隔100ms调用一次doEvictionPass，进行pod的驱逐工作

```go
// Run starts an asynchronous loop that monitors the status of cluster nodes.
/*
	启动一个异步循环，监视群集节点的状态。
*/
func (nc *NodeController) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	glog.Infof("Starting node controller")
	defer glog.Infof("Shutting down node controller")

	/*
		WaitForCacheSync 函数等待 PodInformer、NodeInformer、DaemonSetInformer 的HasSyncs 都返回 true，完成同步
		默认周期为100ms
	*/
	if !controller.WaitForCacheSync("node", stopCh, nc.nodeInformerSynced, nc.podInformerSynced, nc.daemonSetInformerSynced) {
		return
	}

	// Incorporate the results of node status pushed from kubelet to master.
	/*
		间隔`node-monitor-period`时间，处理kubelet发送给master的node status信息
	*/
	go wait.Until(func() {
		if err := nc.monitorNodeStatus(); err != nil {
			glog.Errorf("Error monitoring node status: %v", err)
		}
	}, nc.nodeMonitorPeriod, wait.NeverStop)

	if nc.runTaintManager {
		/*
			nc.runTaintManager通过`--enable-taint-manager`设置，
			默认为true，因此默认情况下都会启动Taint Manager。
			==>/pkg/controller/node/taint_controller.go
				==>func (tc *NoExecuteTaintManager) Run(stopCh <-chan struct{})
		*/
		go nc.taintManager.Run(wait.NeverStop)
	}

	if nc.useTaintBasedEvictions {
		/*
			如果useTaintBasedEvictions为true，
			每隔100ms调用一次doTaintingPass

			doTaintingPass就是根据Node Condition是NotReady或者Unknown，
			通知apiserver，分别给node打上对应的Taint：
				- node.alpha.kubernetes.io/notReady和node.alpha.kubernetes.io/unreachable。
		*/
		// Handling taint based evictions. Because we don't want a dedicated logic in TaintManager for NC-originated
		// taints and we normally don't rate limit evictions caused by taints, we need to rate limit adding taints.
		go wait.Until(nc.doTaintingPass, nodeEvictionPeriod, wait.NeverStop)
	} else {
		// Managing eviction of nodes:
		// When we delete pods off a node, if the node was not empty at the time we then
		// queue an eviction watcher. If we hit an error, retry deletion.
		/*
			如果useTaintBasedEvictions为false（默认TaintBasedEvictions=false），
			则每隔100ms调用一次doEvictionPass
		*/
		go wait.Until(nc.doEvictionPass, nodeEvictionPeriod, wait.NeverStop)
	}

	<-stopCh
}
```

### monitorNodeStatus()

nodeController会定期从apiserver中读取node status信息，默认时间间隔是5s，通过`--node-monitor-period`设置。
分析其流程如下：

1. 从本地cache中获取node，可以容忍一些相对于etcd状态的小延迟，因为其最后会具有一致性
2. 根据 nodes 与 knownNodeSet对node进行分类
    * added，节点在 nodes 中存在，而knownNodeSet 不存在
    * deleted，节点不在 nodes 中，而knownNodeSet 存在
3. 遍历added Node列表，表示Node Controller观察到一个新的Node加入集群。把该node加入到 knownNodeSet 中，为该节点创建新的 zone。其中zone和Eviction有关
4. 对于 deleted ，则从 knownNodeSet 中删除
5. 对所有从cache中读取到node进行遍历，根据其kubelet上报的status信息进行处理（决定pod驱逐等行为）
6. 调用handleDisruption()处理中断的情况，这里面会设置一个node驱逐的速度

```go
// monitorNodeStatus verifies node status are constantly updated by kubelet, and if not,
// post "NodeReady==ConditionUnknown". It also evicts all pods if node is not ready or
// not reachable for a long period of time.
/*
	kubelt如果停止上报node status状态信息，NodeController会发布"NodeReady==ConditionUnknown"。
	NodeController会驱逐pods，如果一个node长时间处于not ready或not reachable（默认5min）
*/
func (nc *NodeController) monitorNodeStatus() error {
	// We are listing nodes from local cache as we can tolerate some small delays
	// comparing to state from etcd and there is eventual consistency anyway.
	/*
		从本地cache中获取node，可以容忍一些相对于etcd状态的小延迟，因为其最后会具有一致性。
		列出所有节点
	*/
	nodes, err := nc.nodeLister.List(labels.Everything())
	if err != nil {
		return err
	}
	/*
		根据 nodes 与 knownNodeSet对node进行分类
		    - added，节点在 nodes 中存在，而knownNodeSet 不存在
			- deleted，节点不在 nodes 中，而knownNodeSet 存在
	*/
	added, deleted := nc.checkForNodeAddedDeleted(nodes)
	for i := range added {
		/*
			遍历added Node列表，表示Node Controller观察到一个新的Node加入集群
			加入到 knownNodeSet 中，为该节点创建新的 zone
		*/
		glog.V(1).Infof("NodeController observed a new Node: %#v", added[i].Name)
		recordNodeEvent(nc.recorder, added[i].Name, string(added[i].UID), v1.EventTypeNormal, "RegisteredNode", fmt.Sprintf("Registered Node %v in NodeController", added[i].Name))
		nc.knownNodeSet[added[i].Name] = added[i]
		// When adding new Nodes we need to check if new zone appeared, and if so add new evictor.
		zone := utilnode.GetZoneKey(added[i])
		if _, found := nc.zoneStates[zone]; !found {
			/*
				设置该Node对应的新zone状态为“Initial”
					==>https://my.oschina.net/jxcdwangtao/blog/1486616
			*/
			nc.zoneStates[zone] = stateInitial
			/*
				如果Node Controller的useTaintBasedEvictions为false
				在`--feature-gates`中指定，默认TaintBasedEvictions=false
				则添加该zone对应的zonePodEvictor，并设置evictionLimiterQPS（--node-eviction-rate设置，默认为0.1）
			*/
			if !nc.useTaintBasedEvictions {
				nc.zonePodEvictor[zone] =
					NewRateLimitedTimedQueue(
						flowcontrol.NewTokenBucketRateLimiter(nc.evictionLimiterQPS, evictionRateLimiterBurst))
			} else {
				/*
					如果Node Controller的useTaintBasedEvictions为true，
					则添加该zone对应的zoneNotReadyOrUnreachableTainer，并设置evictionLimiterQPS
				*/
				nc.zoneNotReadyOrUnreachableTainer[zone] =
					NewRateLimitedTimedQueue(
						flowcontrol.NewTokenBucketRateLimiter(nc.evictionLimiterQPS, evictionRateLimiterBurst))
			}
			// Init the metric for the new zone.
			glog.Infof("Initializing eviction metric for zone: %v", zone)
			EvictionsNumber.WithLabelValues(zone).Add(0)
		}
		if nc.useTaintBasedEvictions {
			/*
				如果Node Controller的useTaintBasedEvictions为true，调用RemoveTaintOffNode将Node上对应的Taints（node.alpha.kubernetes.io/notReady和node.alpha.kubernetes.io/unreachable）清除掉，
				并将其从zoneNotReadyOrUnreachableTainer Queue中Remove（如果它在这个Queue中）
			*/
			nc.markNodeAsHealthy(added[i])
		} else {
			/*
				如果Node Controller的useTaintBasedEvictions为false，即使用zonePodEvictor时，
				将该node从对应的zonePodEvictor Queue中Remove
			*/
			nc.cancelPodEviction(added[i])
		}
	}

	/*
		对于 deleted ，则从 knownNodeSet 中删除
	*/
	for i := range deleted {
		glog.V(1).Infof("NodeController observed a Node deletion: %v", deleted[i].Name)
		recordNodeEvent(nc.recorder, deleted[i].Name, string(deleted[i].UID), v1.EventTypeNormal, "RemovingNode", fmt.Sprintf("Removing Node %v from NodeController", deleted[i].Name))
		delete(nc.knownNodeSet, deleted[i].Name)
	}

	zoneToNodeConditions := map[string][]*v1.NodeCondition{}
	/*
		对所有node进行遍历
	*/
	for i := range nodes {
		var gracePeriod time.Duration
		var observedReadyCondition v1.NodeCondition //上一次观察到的NodeCondition
		var currentReadyCondition *v1.NodeCondition //当前的NodeCondition
		nodeCopy, err := api.Scheme.DeepCopy(nodes[i])
		if err != nil {
			utilruntime.HandleError(err)
			continue
		}
		node := nodeCopy.(*v1.Node)
		if err := wait.PollImmediate(retrySleepTime, retrySleepTime*nodeStatusUpdateRetry, func() (bool, error) {
			gracePeriod, observedReadyCondition, currentReadyCondition, err = nc.tryUpdateNodeStatus(node)
			if err == nil {
				return true, nil
			}
			name := node.Name
			node, err = nc.kubeClient.Core().Nodes().Get(name, metav1.GetOptions{})
			if err != nil {
				glog.Errorf("Failed while getting a Node to retry updating NodeStatus. Probably Node %s was deleted.", name)
				return false, err
			}
			return false, nil
		}); err != nil {
			glog.Errorf("Update status  of Node %v from NodeController error : %v. "+
				"Skipping - no pods will be evicted.", node.Name, err)
			continue
		}

		// We do not treat a master node as a part of the cluster for network disruption checking.
		/*
			不会将master节点视为集群网络中断检查的一部分
			对于非master节点，将node对应的NodeCondition添加到 zoneToNodeConditions Map中。
		*/
		if !system.IsMasterNode(node.Name) {
			zoneToNodeConditions[utilnode.GetZoneKey(node)] = append(zoneToNodeConditions[utilnode.GetZoneKey(node)], currentReadyCondition)
		}

		decisionTimestamp := nc.now()
		if currentReadyCondition != nil {
			// Check eviction timeout against decisionTimestamp
			/*
				当观察到Node的Condition为NotReady时，根据是否useTaintBasedEvictions是否为true，分别进行处理

				其中v1.ConditionFalse、v1.ConditionUnknown这些状态信息来自于kubelet的上报
			*/
			if observedReadyCondition.Status == v1.ConditionFalse {
				/* useTaintBasedEvictions为true时 */
				if nc.useTaintBasedEvictions {
					// We want to update the taint straight away if Node is already tainted with the UnreachableTaint
					/*
						如果该node的已经被Taint为UnreachableTaint，则将其改成NotReadyTaint
					*/
					if v1helper.TaintExists(node.Spec.Taints, UnreachableTaintTemplate) {
						taintToAdd := *NotReadyTaintTemplate
						if !swapNodeControllerTaint(nc.kubeClient, &taintToAdd, UnreachableTaintTemplate, node) {
							glog.Errorf("Failed to instantly swap UnreachableTaint to NotReadyTaint. Will try again in the next cycle.")
						}
					} else if nc.markNodeForTainting(node) {
						/*
							将node加入到Tainer Queue中，交给Taint Controller处理
						*/
						glog.V(2).Infof("Node %v is NotReady as of %v. Adding it to the Taint queue.",
							node.Name,
							decisionTimestamp,
						)
					}
				} else {
					/*
						如果useTaintBasedEvictions为false时，表示使用Pod Eivict方式。
						这是默认方式
					*/
					/*
						保证readyTransitionTimestamp + podEvictionTimeout（default 5min） > decisionTimestamp(当前时间)
					*/
					if decisionTimestamp.After(nc.nodeStatusMap[node.Name].readyTransitionTimestamp.Add(nc.podEvictionTimeout)) {
						/*将node加入到PodEvictor Queue中，交给PodEvictor处理*/
						if nc.evictPods(node) {
							glog.V(2).Infof("Node is NotReady. Adding Pods on Node %s to eviction queue: %v is later than %v + %v",
								node.Name,
								decisionTimestamp,
								nc.nodeStatusMap[node.Name].readyTransitionTimestamp,
								nc.podEvictionTimeout,
							)
						}
					}
				}
			}
			/*
				同理地，当观察到Node的Condition为Unknown时，根据是否useTaintBasedEvictions是否为true，分别进行处理
			*/
			if observedReadyCondition.Status == v1.ConditionUnknown {
				if nc.useTaintBasedEvictions {
					// We want to update the taint straight away if Node is already tainted with the UnreachableTaint
					/*
						如果该node的已经被Taint为UnreachableTaint，则将其改成NotReadyTaint
					*/
					if v1helper.TaintExists(node.Spec.Taints, NotReadyTaintTemplate) {
						taintToAdd := *UnreachableTaintTemplate
						if !swapNodeControllerTaint(nc.kubeClient, &taintToAdd, NotReadyTaintTemplate, node) {
							glog.Errorf("Failed to instantly swap UnreachableTaint to NotReadyTaint. Will try again in the next cycle.")
						}
					} else if nc.markNodeForTainting(node) {
						/*
							将node加入到Tainer Queue中，交给Taint Controller处理
						*/
						glog.V(2).Infof("Node %v is unresponsive as of %v. Adding it to the Taint queue.",
							node.Name,
							decisionTimestamp,
						)
					}
				} else {
					/*
						使用Pod Eivict方式
					*/
					if decisionTimestamp.After(nc.nodeStatusMap[node.Name].probeTimestamp.Add(nc.podEvictionTimeout)) {
						if nc.evictPods(node) {
							glog.V(2).Infof("Node is unresponsive. Adding Pods on Node %s to eviction queues: %v is later than %v + %v",
								node.Name,
								decisionTimestamp,
								nc.nodeStatusMap[node.Name].readyTransitionTimestamp,
								nc.podEvictionTimeout-gracePeriod,
							)
						}
					}
				}
			}
			/*
				同理地，当观察到Node的Condition为ConditionTrue时，根据是否useTaintBasedEvictions是否为true，分别进行处理
			*/
			if observedReadyCondition.Status == v1.ConditionTrue {
				if nc.useTaintBasedEvictions {
					/*
						将node从zoneNotReadyOrUnreachableTainer Queue中Remove（如果它在这个Queue中）
					*/
					removed, err := nc.markNodeAsHealthy(node)
					if err != nil {
						glog.Errorf("Failed to remove taints from node %v. Will retry in next iteration.", node.Name)
					}
					if removed {
						glog.V(2).Infof("Node %s is healthy again, removing all taints", node.Name)
					}
				} else {
					/*
						将该node从对应的zonePodEvictor Queue中Remove
					*/
					if nc.cancelPodEviction(node) {
						glog.V(2).Infof("Node %s is ready again, cancelled pod eviction", node.Name)
					}
				}
			}

			// Report node event.
			/*
				如果Node Status状态从Ready变为NotReady，则将给Node上的所有Pod Status设置为Not Ready
			*/
			if currentReadyCondition.Status != v1.ConditionTrue && observedReadyCondition.Status == v1.ConditionTrue {
				recordNodeStatusChange(nc.recorder, node, "NodeNotReady")
				if err = markAllPodsNotReady(nc.kubeClient, node); err != nil {
					utilruntime.HandleError(fmt.Errorf("Unable to mark all pods NotReady on node %v: %v", node.Name, err))
				}
			}

			// Check with the cloud provider to see if the node still exists. If it
			// doesn't, delete the node immediately.
			if currentReadyCondition.Status != v1.ConditionTrue && nc.cloud != nil {
				exists, err := nc.nodeExistsInCloudProvider(types.NodeName(node.Name))
				if err != nil {
					glog.Errorf("Error determining if node %v exists in cloud: %v", node.Name, err)
					continue
				}
				if !exists {
					glog.V(2).Infof("Deleting node (no longer present in cloud provider): %s", node.Name)
					recordNodeEvent(nc.recorder, node.Name, string(node.UID), v1.EventTypeNormal, "DeletingNode", fmt.Sprintf("Deleting Node %v because it's not present according to cloud provider", node.Name))
					go func(nodeName string) {
						defer utilruntime.HandleCrash()
						// Kubelet is not reporting and Cloud Provider says node
						// is gone. Delete it without worrying about grace
						// periods.
						if err := forcefullyDeleteNode(nc.kubeClient, nodeName); err != nil {
							glog.Errorf("Unable to forcefully delete node %q: %v", nodeName, err)
						}
					}(node.Name)
				}
			}
		}
	}
	/*
		处理Disruption
	*/
	nc.handleDisruption(zoneToNodeConditions, nodes)

	return nil
}
```

### zone stae的计算

可能有多个zone，每个zone都有一个自己的state

zone通过lable来区分，如果不指定，默认的zone是""

假如节点刚刚加入集群，它所在的zone刚刚被发现，则该zone的状态是initial，这是一个非常短暂的时间，其余的状态由`func ComputeZoneState`来决定。

```go
const (
	stateInitial = zoneState("Initial")
	stateNormal  = zoneState("Normal")
	/*
		完全中断
	*/
	stateFullDisruption = zoneState("FullDisruption")
	/*
		部分中断
	*/
	statePartialDisruption = zoneState("PartialDisruption")
)

// This function is expected to get a slice of NodeReadyConditions for all Nodes in a given zone.
// The zone is considered:
// - fullyDisrupted if there're no Ready Nodes,
// - partiallyDisrupted if at least than nc.unhealthyZoneThreshold percent of Nodes are not Ready,
// - normal otherwise
/*
 计算出该zone的state
*/
func (nc *NodeController) ComputeZoneState(nodeReadyConditions []*v1.NodeCondition) (int, zoneState) {
	readyNodes := 0
	notReadyNodes := 0
	for i := range nodeReadyConditions {
		if nodeReadyConditions[i] != nil && nodeReadyConditions[i].Status == v1.ConditionTrue {
			readyNodes++
		} else {
			notReadyNodes++
		}
	}
	switch {
	case readyNodes == 0 && notReadyNodes > 0:
		return notReadyNodes, stateFullDisruption
	case notReadyNodes > 2 && float32(notReadyNodes)/float32(notReadyNodes+readyNodes) >= nc.unhealthyZoneThreshold:
		/*
			nc.unhealthyZoneThreshold 通过 --unhealthy-zone-threshold 设置，默认为0.55
		*/
		return notReadyNodes, statePartialDisruption
	default:
		return notReadyNodes, stateNormal
	}
}
```

### node的驱逐速率
1. 如果zone state为normal，则设置Tainter Queue或者Pod Evictor Queue的rate limiter为evictionLimiterQPS（默认为0.1)，即每隔10s，清空一个节点
2. zone state为statePartialDisruption时，设置Tainter Queue或者Pod Evictor Queue的rate limiter为：
    * 如果当前zone size大于nc.largeClusterThreshold（默认为50），则设置为secondaryEvictionLimiterQPS（默认为0.01）
    * 否则设置为0
3. zone state为stateFullDisruption时，设置Tainter Queue或者Pod Evictor Queue的rate limiter为evictionLimiterQPS(默认0.1)

```go
func (nc *NodeController) handleDisruption(zoneToNodeConditions map[string][]*v1.NodeCondition, nodes []*v1.Node)

func (nc *NodeController) setLimiterInZone(zone string, zoneSize int, state zoneState) {
	switch state {
	case stateNormal:
		/*
			如果zone state为normal，则设置Tainter Queue或者Pod Evictor Queue的rate limiter为evictionLimiterQPS（默认为0.1）
		*/
		if nc.useTaintBasedEvictions {
			nc.zoneNotReadyOrUnreachableTainer[zone].SwapLimiter(nc.evictionLimiterQPS)
		} else {
			nc.zonePodEvictor[zone].SwapLimiter(nc.evictionLimiterQPS)
		}
	case statePartialDisruption:
		/*
			nc.enterFullDisruptionFunc和nc.enterPartialDisruptionFunc是在调用NewNodeController创建Node Controller的时候赋值注册的

			设置Tainter Queue或者Pod Evictor Queue的rate limiter为：
				- 如果当前zone size大于nc.largeClusterThreshold（默认为50），则设置为secondaryEvictionLimiterQPS（默认为0.01）
				- 否则设置为0
		*/
		if nc.useTaintBasedEvictions {
			nc.zoneNotReadyOrUnreachableTainer[zone].SwapLimiter(
				nc.enterPartialDisruptionFunc(zoneSize))
		} else {
			nc.zonePodEvictor[zone].SwapLimiter(
				nc.enterPartialDisruptionFunc(zoneSize))
		}
	case stateFullDisruption:
		/*
			设置Tainter Queue或者Pod Evictor Queue的rate limiter为evictionLimiterQPS(默认0.1)
		*/
		if nc.useTaintBasedEvictions {
			nc.zoneNotReadyOrUnreachableTainer[zone].SwapLimiter(
				nc.enterFullDisruptionFunc(zoneSize))
		} else {
			nc.zonePodEvictor[zone].SwapLimiter(
				nc.enterFullDisruptionFunc(zoneSize))
		}
	}
}
```

### doEvictionPass
1. 遍历所有zone的pod Evictor，
2. 从pod Evictor queue中获取node name，
3. 然后调用deletePods删除node上的所有pods，deamonSet对应的Pod除外

```go
func (nc *NodeController) doEvictionPass() {
	nc.evictorLock.Lock()
	defer nc.evictorLock.Unlock()
	/*
		遍历所有zone的pod Evictor，
		从pod Evictor queue中获取node name，
		然后调用deletePods删除node上的所有pods（deamonSet对应的Pod除外）
	*/
	for k := range nc.zonePodEvictor {
		// Function should return 'false' and a time after which it should be retried, or 'true' if it shouldn't (it succeeded).
		nc.zonePodEvictor[k].Try(func(value TimedValue) (bool, time.Duration) {
			node, err := nc.nodeLister.Get(value.Value)
			if apierrors.IsNotFound(err) {
				glog.Warningf("Node %v no longer present in nodeLister!", value.Value)
			} else if err != nil {
				glog.Warningf("Failed to get Node %v from the nodeLister: %v", value.Value, err)
			} else {
				zone := utilnode.GetZoneKey(node)
				EvictionsNumber.WithLabelValues(zone).Inc()
			}
			nodeUid, _ := value.UID.(string)
			/*
				调用deletePods
			*/
			remaining, err := deletePods(nc.kubeClient, nc.recorder, value.Value, nodeUid, nc.daemonSetStore)
			if err != nil {
				utilruntime.HandleError(fmt.Errorf("unable to evict node %q: %v", value.Value, err))
				return false, 0
			}
			if remaining {
				glog.Infof("Pods awaiting deletion due to NodeController eviction")
			}
			return true, 0
		})
	}
}

func deletePods(kubeClient clientset.Interface, recorder record.EventRecorder, nodeName, nodeUID string, daemonStore extensionslisters.DaemonSetLister) (bool, error) {
	remaining := false
	selector := fields.OneTermEqualSelector(api.PodHostField, nodeName).String()
	options := metav1.ListOptions{FieldSelector: selector}
	/*
		从apiserver中获取所有的pods对象。
	*/
	pods, err := kubeClient.Core().Pods(metav1.NamespaceAll).List(options)
	var updateErrList []error

	if err != nil {
		return remaining, err
	}

	if len(pods.Items) > 0 {
		recordNodeEvent(recorder, nodeName, nodeUID, v1.EventTypeNormal, "DeletingAllPods", fmt.Sprintf("Deleting all Pods from Node %v.", nodeName))
	}

	/*
		逐个遍历pods中的pod，筛选出该node上的pods
	*/
	for _, pod := range pods.Items {
		// Defensive check, also needed for tests.
		if pod.Spec.NodeName != nodeName {
			continue
		}

		// Set reason and message in the pod object.
		if _, err = setPodTerminationReason(kubeClient, &pod, nodeName); err != nil {
			if errors.IsConflict(err) {
				updateErrList = append(updateErrList,
					fmt.Errorf("update status failed for pod %q: %v", format.Pod(&pod), err))
				continue
			}
		}
		// if the pod has already been marked for deletion, we still return true that there are remaining pods.
		/*
			如果pod已经被标记为删除（pod.DeletionGracePeriodSeconds != nil ），跳过这个pod
		*/
		if pod.DeletionGracePeriodSeconds != nil {
			remaining = true
			continue
		}
		// if the pod is managed by a daemonset, ignore it
		/*
			如果pod是某个daemonset的pod，跳过这个pod
		*/
		_, err := daemonStore.GetPodDaemonSets(&pod)
		if err == nil { // No error means at least one daemonset was found
			continue
		}

		glog.V(2).Infof("Starting deletion of pod %v/%v", pod.Namespace, pod.Name)
		recorder.Eventf(&pod, v1.EventTypeNormal, "NodeControllerEviction", "Marking for deletion Pod %s from Node %s", pod.Name, nodeName)
		/*
			调用接口删除pod
		*/
		if err := kubeClient.Core().Pods(pod.Namespace).Delete(pod.Name, nil); err != nil {
			return false, err
		}
		remaining = true
	}

	if len(updateErrList) > 0 {
		return false, utilerrors.NewAggregate(updateErrList)
	}
	return remaining, nil
}
```

## kube-scheduler端
Kubelet会定期的将Node Condition传给kube-apiserver并存于etcd。kube-scheduler watch到Node Condition Pressure之后，会根据以下策略，阻止更多Pods Bind到该Node。

    * 当Node Condition为MemoryPressure时，Scheduler不会调度新的QoS Class为BestEffort的Pods到该Node。
    * 当Node Condition为DiskPressure时，Scheduler不会调度任何新的Pods到该Node。	

```go
// Determine if a pod is scheduled with best-effort QoS
func isPodBestEffort(pod *v1.Pod) bool {
	return v1qos.GetPodQOS(pod) == v1.PodQOSBestEffort
}

// CheckNodeMemoryPressurePredicate checks if a pod can be scheduled on a node
// reporting memory pressure condition.
func CheckNodeMemoryPressurePredicate(pod *v1.Pod, meta interface{}, nodeInfo *schedulercache.NodeInfo) (bool, []algorithm.PredicateFailureReason, error) {
	var podBestEffort bool
	if predicateMeta, ok := meta.(*predicateMetadata); ok {
		podBestEffort = predicateMeta.podBestEffort
	} else {
		// We couldn't parse metadata - fallback to computing it.
		podBestEffort = isPodBestEffort(pod)
	}
	// pod is not BestEffort pod
	if !podBestEffort {
		return true, nil, nil
	}

	// is node under presure?
	if nodeInfo.MemoryPressureCondition() == v1.ConditionTrue {
		return false, []algorithm.PredicateFailureReason{ErrNodeUnderMemoryPressure}, nil
	}
	return true, nil, nil
}

// CheckNodeDiskPressurePredicate checks if a pod can be scheduled on a node
// reporting disk pressure condition.
func CheckNodeDiskPressurePredicate(pod *v1.Pod, meta interface{}, nodeInfo *schedulercache.NodeInfo) (bool, []algorithm.PredicateFailureReason, error) {
	// is node under presure?
	if nodeInfo.DiskPressureCondition() == v1.ConditionTrue {
		return false, []algorithm.PredicateFailureReason{ErrNodeUnderDiskPressure}, nil
	}
	return true, nil, nil
}
```


