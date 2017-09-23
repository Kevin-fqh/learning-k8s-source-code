# Event机制-1

## 版本说明
本文涉及代码是V1.5.2，Event机制的源码走读会是V1.5.2和V1.1.2的混合。


**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [流程](#流程)
  - [Event](#event)
	- [Event的定义](#event的定义)
	- [InvolvedObject属性和Source属性](#involvedobject属性和source属性)

<!-- END MUNGE: GENERATED_TOC -->

在kubectl describe pod的命令中，会发现有一些事件Event的描述，这些Event对集群的日常运维是非常有帮助的。
那么这些Event怎么来的呢？其机制和原理是什么呢？我们就从kubectl describe pod出发，开始研究k8s Event的机制。

## 流程
从/kubernetes-1.5.2/pkg/kubectl/cmd/describe.go中的func RunDescribe出发
```go
for _, info := range infos {
		mapping := info.ResourceMapping()
		/*
			获取对应的describer，定义在/pkg/kubectl/cmd/util/factory.go
				==>func (f *factory) Describer(mapping *meta.RESTMapping)
		*/
		describer, err := f.Describer(mapping)
		if err != nil {
			allErrs = append(allErrs, err)
			continue
		}
		s, err := describer.Describe(info.Namespace, info.Name, *describerSettings)
		if err != nil {
			allErrs = append(allErrs, err)
			continue
		}
		if first {
			first = false
			fmt.Fprint(out, s)
		} else {
			fmt.Fprintf(out, "\n\n%s", s)
		}
	}
```
来到/pkg/kubectl/cmd/util/factory.go，
```go
/*
	返回一个Describer以显示指定的RESTMapping type或错误。
*/
func (f *factory) Describer(mapping *meta.RESTMapping) (kubectl.Describer, error) {
	mappingVersion := mapping.GroupVersionKind.GroupVersion()
	if mapping.GroupVersionKind.Group == federation.GroupName {
		fedClientSet, err := f.clients.FederationClientSetForVersion(&mappingVersion)
		if err != nil {
			return nil, err
		}
		if mapping.GroupVersionKind.Kind == "Cluster" {
			return &kubectl.ClusterDescriber{Interface: fedClientSet}, nil
		}
	}
	clientset, err := f.clients.ClientSetForVersion(&mappingVersion)
	if err != nil {
		return nil, err
	}
	/*
		/pkg/kubectl/describe.go
			==>func DescriberFor(kind unversioned.GroupKind, c clientset.Interface) (Describer, bool)
	*/
	if describer, ok := kubectl.DescriberFor(mapping.GroupVersionKind.GroupKind(), clientset); ok {
		return describer, nil
	}
	return nil, fmt.Errorf("no description has been implemented for %q", mapping.GroupVersionKind.Kind)
}

// Describer returns the default describe functions for each of the standard
// Kubernetes types.
func DescriberFor(kind unversioned.GroupKind, c clientset.Interface) (Describer, bool) {
	f, ok := describerMap(c)[kind]
	return f, ok
}

/*
	Kubernetes中只有部分资源可以被describe，可以称为“DescribableResources”，
	通过一个资源类型(unversioned.GroupKind)和对应描述器(Describer)的Map映射相关联。
	这些资源有我们常见的Pod、ReplicationController、Service、Node、Deloyment、ReplicaSet等等。
	注意这些资源里并不包含Events。

	这里就定义一个Map映射关系表
*/
func describerMap(c clientset.Interface) map[unversioned.GroupKind]Describer {
	m := map[unversioned.GroupKind]Describer{
		api.Kind("Pod"):                   &PodDescriber{c},
		api.Kind("ReplicationController"): &ReplicationControllerDescriber{c},
		api.Kind("Secret"):                &SecretDescriber{c},
		api.Kind("Service"):               &ServiceDescriber{c},
		api.Kind("ServiceAccount"):        &ServiceAccountDescriber{c},
		api.Kind("Node"):                  &NodeDescriber{c},
		api.Kind("LimitRange"):            &LimitRangeDescriber{c},
		api.Kind("ResourceQuota"):         &ResourceQuotaDescriber{c},
		api.Kind("PersistentVolume"):      &PersistentVolumeDescriber{c},
		api.Kind("PersistentVolumeClaim"): &PersistentVolumeClaimDescriber{c},
		api.Kind("Namespace"):             &NamespaceDescriber{c},
		api.Kind("Endpoints"):             &EndpointsDescriber{c},
		api.Kind("ConfigMap"):             &ConfigMapDescriber{c},

		extensions.Kind("ReplicaSet"):                  &ReplicaSetDescriber{c},
		extensions.Kind("HorizontalPodAutoscaler"):     &HorizontalPodAutoscalerDescriber{c},
		extensions.Kind("NetworkPolicy"):               &NetworkPolicyDescriber{c},
		autoscaling.Kind("HorizontalPodAutoscaler"):    &HorizontalPodAutoscalerDescriber{c},
		extensions.Kind("DaemonSet"):                   &DaemonSetDescriber{c},
		extensions.Kind("Deployment"):                  &DeploymentDescriber{c},
		extensions.Kind("Job"):                         &JobDescriber{c},
		extensions.Kind("Ingress"):                     &IngressDescriber{c},
		batch.Kind("Job"):                              &JobDescriber{c},
		batch.Kind("CronJob"):                          &CronJobDescriber{c},
		apps.Kind("StatefulSet"):                       &StatefulSetDescriber{c},
		certificates.Kind("CertificateSigningRequest"): &CertificateSigningRequestDescriber{c},
		storage.Kind("StorageClass"):                   &StorageClassDescriber{c},
		policy.Kind("PodDisruptionBudget"):             &PodDisruptionBudgetDescriber{c},
	}

	return m
}
```

我们研究describe pod，从/pkg/kubectl/describe.go中func (d *PodDescriber) Describe出发。
```go
type PodDescriber struct {
	/*
		定义在/pkg/client/clientset_generated/internalclientset/clientset.go
			==>type Interface interface
	*/
	clientset.Interface
}

func (d *PodDescriber) Describe(namespace, name string, describerSettings DescriberSettings) (string, error) {
	pod, err := d.Core().Pods(namespace).Get(name)
	if err != nil {
		/*
			获取Pod失败时：
				Events的 GetFieldSelector 方法同时根据InvolvedObject的名称、命名空间、资源类型和UID生成一个FieldSelector。
				使用它作为ListOptions，可以选中满足这个Selector对应的资源。
				如果选中的Events不为空，说明“获取Pod出错，但发现了Events”的情况，并将其按照特定的格式打印。
		*/
		if describerSettings.ShowEvents {
			eventsInterface := d.Core().Events(namespace)
			/*
				FieldSelector和LabelSelector的设计异曲同工，
				不同的是Field匹配的是该资源的域，比如Name、Namespace，
				而Label匹配的是Labels域里的键值对。
			*/
			selector := eventsInterface.GetFieldSelector(&name, &namespace, nil, nil)
			options := api.ListOptions{FieldSelector: selector}
			events, err2 := eventsInterface.List(options)
			if describerSettings.ShowEvents && err2 == nil && len(events.Items) > 0 {
				return tabbedString(func(out io.Writer) error {
					fmt.Fprintf(out, "Pod '%v': error '%v', but found events.\n", name, err)
					/*
						这里有个DescribeEvents(events, out)
						func DescribeEvents(el *api.EventList, w io.Writer)
					*/
					DescribeEvents(events, out)
					return nil
				})
			}
		}
		return "", err
	}

	/*
		*api.EventList定义在/pkg/api/types.go
			==>type EventList struct
	*/
	var events *api.EventList
	if describerSettings.ShowEvents {
		if ref, err := api.GetReference(pod); err != nil {
			/*
				获取Pod成功，GetReference()失败
				GetReference()根据传入的K8s资源实例，构造它的引用说明。
				如果执行失败，记录失败日志，并直接执行describePod()，将目前获取的结果输出到屏幕上。
			*/
			glog.Errorf("Unable to construct reference to '%#v': %v", pod, err)
		} else {
			/*
				获取Pod成功，GetReference()成功
				GetReference()成功后，调用Events的Search()方法，寻找关于该Pod的所有Events。
				最终执行describePod()，并将目前获取的结果输出到屏幕上。
			*/
			ref.Kind = ""
			/*
				Core()定义在/pkg/client/clientset_generated/internalclientset/clientset.go
					==>func (c *Clientset) Core() internalversioncore.CoreInterface
			*/
			events, _ = d.Core().Events(namespace).Search(ref)
		}
	}

	return describePod(pod, events)
}

/*
	DescriberSettings保存每个object的describer的配置信息，以控制打印的内容。
	默认值为true
		==>pkg/kubectl/cmd/describe.go
			==>cmd.Flags().BoolVar(&describerSettings.ShowEvents, "show-events", true, "If true, display events related to the described object.")
*/
type DescriberSettings struct {
	ShowEvents bool
}
```
最后来看看func DescribeEvents
```go
func DescribeEvents(el *api.EventList, w io.Writer) {
	if len(el.Items) == 0 {
		fmt.Fprint(w, "No events.\n")
		return
	}
	sort.Sort(events.SortableEvents(el.Items))
	fmt.Fprint(w, "Events:\n  FirstSeen\tLastSeen\tCount\tFrom\tSubObjectPath\tType\tReason\tMessage\n")
	fmt.Fprint(w, "  ---------\t--------\t-----\t----\t-------------\t--------\t------\t-------\n")
	for _, e := range el.Items {
		fmt.Fprintf(w, "  %s\t%s\t%d\t%v\t%v\t%v\t%v\t%v\n",
			translateTimestamp(e.FirstTimestamp),
			translateTimestamp(e.LastTimestamp),
			e.Count,
			e.Source,
			e.InvolvedObject.FieldPath,
			e.Type,
			e.Reason,
			e.Message)
	}
}
```

可以发现Core()定义在/pkg/client/clientset_generated/internalclientset/clientset.go，这是一个比较重要的概念，
设计到API中GVK的处理。type Clientset struct 含有所有的groups的一个确定version。
```go
// Clientset contains the clients for groups. Each group has exactly one
// version included in a Clientset.
/*
	译：type Clientset struct 是包含groups的clients。
		每个group中只有一个版本被包含在Clientset中。
*/
type Clientset struct {
	*discovery.DiscoveryClient
	*internalversioncore.CoreClient
	*internalversionapps.AppsClient
	*internalversionauthentication.AuthenticationClient
	*internalversionauthorization.AuthorizationClient
	*internalversionautoscaling.AutoscalingClient
	*internalversionbatch.BatchClient
	*internalversioncertificates.CertificatesClient
	*internalversionextensions.ExtensionsClient
	*internalversionpolicy.PolicyClient
	*internalversionrbac.RbacClient
	*internalversionstorage.StorageClient
}

// Core retrieves the CoreClient
/*
	负责处理Core Group
*/
func (c *Clientset) Core() internalversioncore.CoreInterface {
	if c == nil {
		return nil
	}
	/*
		CoreClient定义在 pkg/client/clientset_generated/internalclientset/typed/core/internalversion/core_client.go
			==>type CoreClient struct
	*/
	return c.CoreClient
}
```
继续查看type Clientset struct
```go
// CoreClient is used to interact with features provided by the k8s.io/kubernetes/pkg/apimachinery/registered.Group group.
/*
	type CoreClient struct用于与k8s.io/kubernetes/pkg/apimachinery/registered.Group组提供的features进行交互。
*/
type CoreClient struct {
	restClient restclient.Interface
}

func (c *CoreClient) Events(namespace string) EventInterface {
	return newEvents(c, namespace)
}
```
newEvents函数定义在/pkg/client/clientset_generated/internalclientset/typed/core/internalversion/event.go中
```go
// newEvents returns a Events
func newEvents(c *CoreClient, namespace string) *events {
	return &events{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// events implements EventInterface
type events struct {
	client restclient.Interface
	ns     string
}

// Search finds events about the specified object. The namespace of the
// object must match this event's client namespace unless the event client
// was made with the "" namespace.
/*
	译：func (e *events) Search查找入参object的events。
		入参object的namespace必须和event's client的namespace匹配。
		除非event's client是使用namespace""来创建的。
*/
func (e *events) Search(objOrRef runtime.Object) (*api.EventList, error) {
	ref, err := api.GetReference(objOrRef)
	if err != nil {
		return nil, err
	}
	if e.ns != "" && ref.Namespace != e.ns {
		return nil, fmt.Errorf("won't be able to find any events of namespace '%v' in namespace '%v'", ref.Namespace, e.ns)
	}
	stringRefKind := string(ref.Kind)
	var refKind *string
	if stringRefKind != "" {
		refKind = &stringRefKind
	}
	stringRefUID := string(ref.UID)
	var refUID *string
	if stringRefUID != "" {
		refUID = &stringRefUID
	}
	fieldSelector := e.GetFieldSelector(&ref.Name, &ref.Namespace, refKind, refUID)
	return e.List(api.ListOptions{FieldSelector: fieldSelector})
}
```
至此，func (d *PodDescriber) Describe的整体流程已经结束。

## Event
### Event的定义
下一步，我们来看看定义在/pkg/api/types.go中的api.EventList，type EventList struct。
```go
// EventList is a list of events.
/*
	type EventList struct，
	这就是使用kubectl get events和CURL GET /api/v1/namespaces/{namespace}/events获取Events列表时K8s使用的数据结构
*/
type EventList struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	unversioned.ListMeta `json:"metadata,omitempty"`

	Items []Event `json:"items"`
}
```
还有一个type Event struct组成的数组
```go
// Event is a report of an event somewhere in the cluster.
// TODO: Decide whether to store these separately or with the object they apply to.
/*
	type Event struct
	除了标准的Kubernetes资源必备的unversioned.TypeMeta和ObjectMeta成员外，
	Event结构体还包含了Events相关的对象、原因、内容、消息源、首次记录时间、最近记录时间、记录统计和类型

	重要的有两个成员，一个是InvolvedObject， 另一个是Source
*/
type Event struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	/*
		Events的真名，由三部分构成：”发生该Events的Pod名称” + “.” + “数字串”
			或者说Event的命名由被记录的对象和时间戳构成
		==>/pkg/client/record/event.go
			==>func (recorder *recorderImpl) makeEvent
				==>Name:      fmt.Sprintf("%v.%x", ref.Name, t.UnixNano())
	*/
	ObjectMeta `json:"metadata,omitempty"`

	// Required. The object that this event is about.
	// +optional
	/*
		InvolvedObject表示的是这个Events涉及到资源。它的类型是ObjectReference
				ObjectReference里包含的信息足够我们唯一确定该资源实例

				=>一个Node的Event
					involvedObject:
		  				kind: Node
						name: fqhnode
						uid: fqhnode
				=>一个由rc创建的Pod的Event
					involvedObject:
						apiVersion: v1
						kind: ReplicationController
						name: tomcat7
						namespace: default
						resourceVersion: "16437"
						uid: 3dc60ca7-9788-11e7-ba64-080027e58fc6
	*/
	InvolvedObject ObjectReference `json:"involvedObject,omitempty"`

	// Optional; this should be a short, machine understandable string that gives the reason
	// for this event being generated. For example, if the event is reporting that a container
	// can't start, the Reason might be "ImageNotFound".
	// TODO: provide exact specification for format.
	// +optional
	/*
		事件产生的原因，可以在 pkg/kubelet/events/event.go 看到 kubelet 定义的所有事件类型
	*/
	Reason string `json:"reason,omitempty"`

	// Optional. A human-readable description of the status of this operation.
	// TODO: decide on maximum length.
	// +optional
	Message string `json:"message,omitempty"`

	// Optional. The component reporting this event. Should be a short machine understandable string.
	// +optional
	/*
		Source表示的是该Events的来源，eg: replication-controller、kubelet。
		是哪个k8s组件在哪个host主机上生成的该Event。
	*/
	Source EventSource `json:"source,omitempty"`

	// The time at which the event was first recorded. (Time of server receipt is in TypeMeta.)
	// +optional
	FirstTimestamp unversioned.Time `json:"firstTimestamp,omitempty"`

	// The time at which the most recent occurrence of this event was recorded.
	// +optional
	LastTimestamp unversioned.Time `json:"lastTimestamp,omitempty"`

	// The number of times this event has occurred.
	// +optional
	/*
		数量（kubernetes 会把多个相同的事件汇聚到一起）
	*/
	Count int32 `json:"count,omitempty"`

	// Type of this event (Normal, Warning), new types could be added in the future.
	// +optional
	/*
		Events分为两类，分别是EventTypeNormal和EventTypeWarning，
		它们分别表示该Events“仅表示信息，不会造成影响”和“可能有些地方不太对”。
		
		eg:
			s.Recorder.Eventf(s.Config.NodeRef, api.EventTypeNormal, "Starting", "Starting kube-proxy.")
	*/
	Type string `json:"type,omitempty"`
}

const (
	// Information only and will not cause any problems
	EventTypeNormal string = "Normal"
	// These events are to warn that something might go wrong
	EventTypeWarning string = "Warning"
)
```
可以发现都含有两个属性unversioned.TypeMeta和ObjectMeta。其中type TypeMeta struct定义在/pkg/api/unversioned/types.go。
这里直接把type ObjectMeta struct贴出来，内容有点多，方便以后直接查找。可以发现type ObjectMeta struct里面有很多属性的属性值。
```go
// TypeMeta describes an individual object in an API response or request
// with strings representing the type of the object and its API schema version.
// Structures that are versioned or persisted should inline TypeMeta.
/*
	定义了该资源的类别和版本，对应yaml文件的kind: Pod和apiVersion: v1
*/
type TypeMeta struct {
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#types-kinds
	/*
		kind不能被更新，代表了一类REST resource
	*/
	Kind string `json:"kind,omitempty" protobuf:"bytes,1,opt,name=kind"`

	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#resources
	/*
		APIVersion 定义对象的一个版本的schema。
		服务器会把能识别的schemas转化到最新的内部schemas，并可能会拒绝无法识别的值。
	*/
	APIVersion string `json:"apiVersion,omitempty" protobuf:"bytes,2,opt,name=apiVersion"`
}


// ObjectMeta is metadata that all persisted resources must have, which includes all objects
// users must create.
/*
	ObjectMeta是所有持久化资源必须具有的元数据，包括用户必须创建的所有对象。
*/
type ObjectMeta struct {
	// Name is unique within a namespace.  Name is required when creating resources, although
	// some resources may allow a client to request the generation of an appropriate name
	// automatically. Name is primarily intended for creation idempotence and configuration
	// definition.
	// +optional
	/*
		一个namespace内唯一的
	*/
	Name string `json:"name,omitempty"`

	// GenerateName indicates that the name should be made unique by the server prior to persisting
	// it. A non-empty value for the field indicates the name will be made unique (and the name
	// returned to the client will be different than the name passed). The value of this field will
	// be combined with a unique suffix on the server if the Name field has not been provided.
	// The provided value must be valid within the rules for Name, and may be truncated by the length
	// of the suffix required to make the value unique on the server.
	//
	// If this field is specified, and Name is not present, the server will NOT return a 409 if the
	// generated name exists - instead, it will either return 201 Created or 500 with Reason
	// ServerTimeout indicating a unique name could not be found in the time allotted, and the client
	// should retry (optionally after the time indicated in the Retry-After header).
	// +optional
	GenerateName string `json:"generateName,omitempty"`

	// Namespace defines the space within which name must be unique. An empty namespace is
	// equivalent to the "default" namespace, but "default" is the canonical representation.
	// Not all objects are required to be scoped to a namespace - the value of this field for
	// those objects will be empty.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// SelfLink is a URL representing this object.
	// +optional
	SelfLink string `json:"selfLink,omitempty"`

	// UID is the unique in time and space value for this object. It is typically generated by
	// the server on successful creation of a resource and is not allowed to change on PUT
	// operations.
	// +optional
	UID types.UID `json:"uid,omitempty"`

	// An opaque value that represents the version of this resource. May be used for optimistic
	// concurrency, change detection, and the watch operation on a resource or set of resources.
	// Clients must treat these values as opaque and values may only be valid for a particular
	// resource or set of resources. Only servers will generate resource versions.
	// +optional
	ResourceVersion string `json:"resourceVersion,omitempty"`

	// A sequence number representing a specific generation of the desired state.
	// Populated by the system. Read-only.
	// +optional
	Generation int64 `json:"generation,omitempty"`

	// CreationTimestamp is a timestamp representing the server time when this object was
	// created. It is not guaranteed to be set in happens-before order across separate operations.
	// Clients may not set this value. It is represented in RFC3339 form and is in UTC.
	// +optional
	CreationTimestamp unversioned.Time `json:"creationTimestamp,omitempty"`

	// DeletionTimestamp is RFC 3339 date and time at which this resource will be deleted. This
	// field is set by the server when a graceful deletion is requested by the user, and is not
	// directly settable by a client. The resource is expected to be deleted (no longer visible
	// from resource lists, and not reachable by name) after the time in this field. Once set,
	// this value may not be unset or be set further into the future, although it may be shortened
	// or the resource may be deleted prior to this time. For example, a user may request that
	// a pod is deleted in 30 seconds. The Kubelet will react by sending a graceful termination
	// signal to the containers in the pod. After that 30 seconds, the Kubelet will send a hard
	// termination signal (SIGKILL) to the container and after cleanup, remove the pod from the
	// API. In the presence of network partitions, this object may still exist after this
	// timestamp, until an administrator or automated process can determine the resource is
	// fully terminated.
	// If not set, graceful deletion of the object has not been requested.
	//
	// Populated by the system when a graceful deletion is requested.
	// Read-only.
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	// +optional
	DeletionTimestamp *unversioned.Time `json:"deletionTimestamp,omitempty"`

	// DeletionGracePeriodSeconds records the graceful deletion value set when graceful deletion
	// was requested. Represents the most recent grace period, and may only be shortened once set.
	// +optional
	DeletionGracePeriodSeconds *int64 `json:"deletionGracePeriodSeconds,omitempty"`

	// Labels are key value pairs that may be used to scope and select individual resources.
	// Label keys are of the form:
	//     label-key ::= prefixed-name | name
	//     prefixed-name ::= prefix '/' name
	//     prefix ::= DNS_SUBDOMAIN
	//     name ::= DNS_LABEL
	// The prefix is optional.  If the prefix is not specified, the key is assumed to be private
	// to the user.  Other system components that wish to use labels must specify a prefix.  The
	// "kubernetes.io/" prefix is reserved for use by kubernetes components.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations are unstructured key value data stored with a resource that may be set by
	// external tooling. They are not queryable and should be preserved when modifying
	// objects.  Annotation keys have the same formatting restrictions as Label keys. See the
	// comments on Labels for details.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// List of objects depended by this object. If ALL objects in the list have
	// been deleted, this object will be garbage collected. If this object is managed by a controller,
	// then an entry in this list will point to this controller, with the controller field set to true.
	// There cannot be more than one managing controller.
	// +optional
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`

	// Must be empty before the object is deleted from the registry. Each entry
	// is an identifier for the responsible component that will remove the entry
	// from the list. If the deletionTimestamp of the object is non-nil, entries
	// in this list can only be removed.
	// +optional
	Finalizers []string `json:"finalizers,omitempty"`

	// The name of the cluster which the object belongs to.
	// This is used to distinguish resources with same name and namespace in different clusters.
	// This field is not set anywhere right now and apiserver is going to ignore it if set in create or update request.
	// +optional
	ClusterName string `json:"clusterName,omitempty"`
}
```

我们可以来看看`kubectl get events`的输出，注意这里的NAME属性并不是该Event的Name。
```
LASTSEEN   FIRSTSEEN   COUNT     NAME            KIND                    SUBOBJECT                  TYPE      REASON              SOURCE                      MESSAGE
3m         3m          1         tomcat7-nnxtd   Pod                     spec.containers{tomcat7}   Normal    Started             {kubelet fqhnode}           Started container with docker id 4ca30b2f9be9
3m         3m          1         tomcat7         ReplicationController                              Normal    SuccessfulCreate    {replication-controller }   Created pod: tomcat7-nnxtd
3m         4h          3         fqhnode         Node                                               Normal    NodeNotReady            {controllermanager }        Node fqhnode status is now: NodeNotReady
```
再查看`kubectl get events -o yaml`的部分输出
```go
apiVersion: v1
items:
- apiVersion: v1
  count: 1
  firstTimestamp: 2017-09-12T08:33:29Z
  involvedObject:
    apiVersion: v1
    kind: ReplicationController
    name: tomcat7
    namespace: default
    resourceVersion: "16437"
    uid: 3dc60ca7-9788-11e7-ba64-080027e58fc6
  kind: Event
  lastTimestamp: 2017-09-12T08:33:29Z
  message: 'Created pod: tomcat7-nnxtd'
  metadata:
    creationTimestamp: 2017-09-12T08:33:29Z
    name: tomcat7.14e39029d7232c20
    namespace: default
    resourceVersion: "22438"
    selfLink: /api/v1/namespaces/default/events/tomcat7.14e39029d7232c20
    uid: 0dfa2db7-9795-11e7-ba64-080027e58fc6
  reason: SuccessfulCreate
  source:
    component: replication-controller
  type: Normal
kind: List
metadata: {}
resourceVersion: ""
selfLink: ""
```
对比`kubectl get events`和`kubectl get events -o yaml`的输出，可以发现后者属性是能和type TypeMeta struct、type ObjectMeta struct对应上的。那么我们可以发现一个Event的真正name应该是`tomcat7.14e39029d7232c20`，而不是`tomcat7`。我们把`tomcat7.14e39029d7232c20`称为该Event真正的名字。

Events的真名，由三部分构成：”发生该Events的Pod名称” + “.” + “数字串”。或者说Event的命名由被记录的对象和时间戳构成。

我们执行下面命令进行验证。
```
# kubectl get events tomcat7.14e39029d7232c20

LASTSEEN   FIRSTSEEN   COUNT     NAME      KIND                    SUBOBJECT   TYPE      REASON             SOURCE                      MESSAGE
34m        34m         1         tomcat7   ReplicationController               Normal    SuccessfulCreate   {replication-controller }   Created pod: tomcat7-nnxtd

# kubectl get events tomcat7.14e39029d7232c20 -o yaml

apiVersion: v1
count: 1
firstTimestamp: 2017-09-12T08:33:29Z
involvedObject:
  apiVersion: v1
  kind: ReplicationController
  name: tomcat7
  namespace: default
  resourceVersion: "16437"
  uid: 3dc60ca7-9788-11e7-ba64-080027e58fc6
kind: Event
lastTimestamp: 2017-09-12T08:33:29Z
message: 'Created pod: tomcat7-nnxtd'
metadata:
  creationTimestamp: 2017-09-12T08:33:29Z
  name: tomcat7.14e39029d7232c20
  namespace: default
  resourceVersion: "22438"
  selfLink: /api/v1/namespaces/default/events/tomcat7.14e39029d7232c20
  uid: 0dfa2db7-9795-11e7-ba64-080027e58fc6
reason: SuccessfulCreate
source:
  component: replication-controller     //这个replication-controller表示的是控制器rc
type: Normal
```
再查看一个
```
# kubectl get events fqhnode.14e376a677335278 -o yaml

apiVersion: v1
count: 4
firstTimestamp: 2017-09-12T00:45:57Z
involvedObject:
  kind: Node
  name: fqhnode
  uid: fqhnode
kind: Event
lastTimestamp: 2017-09-12T10:24:58Z
message: 'Node fqhnode status is now: NodeReady'
metadata:
  creationTimestamp: 2017-09-12T10:24:58Z
  name: fqhnode.14e376a677335278
  namespace: default
  resourceVersion: "26948"
  selfLink: /api/v1/namespaces/default/events/fqhnode.14e376a677335278
  uid: a07d6605-97a4-11e7-ba64-080027e58fc6
reason: NodeReady
source:
  component: kubelet
  host: fqhnode
type: Normal
```

### InvolvedObject属性和Source属性
InvolvedObject表示的是这个Events涉及到资源，并不是Event本身。它的类型是ObjectReference。ObjectReference里包含的信息足够我们唯一确定该资源实例。
```go
// ObjectReference contains enough information to let you inspect or modify the referred object.
/*
	type ObjectReference struct 里包含的信息足够我们唯一确定该资源实例。
*/
type ObjectReference struct {
	// +optional
	Kind string `json:"kind,omitempty"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	UID types.UID `json:"uid,omitempty"`
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
	// +optional
	ResourceVersion string `json:"resourceVersion,omitempty"`

	// Optional. If referring to a piece of an object instead of an entire object, this string
	// should contain information to identify the sub-object. For example, if the object
	// reference is to a container within a pod, this would take on a value like:
	// "spec.containers{name}" (where "name" refers to the name of the container that triggered
	// the event) or if no container name is specified "spec.containers[2]" (container with
	// index 2 in this pod). This syntax is chosen only to have some well-defined way of
	// referencing a part of an object.
	// TODO: this design is not final and this field is subject to change in the future.
	// +optional
	FieldPath string `json:"fieldPath,omitempty"`
}
```
```
=>一个Node的Event
	involvedObject:
		kind: Node
		name: fqhnode
		uid: fqhnode

=>一个由rc创建的Pod的Event
	involvedObject:
		apiVersion: v1
		kind: ReplicationController    //这个rc指的是yaml文件声明的kind rc
		name: tomcat7
		namespace: default
		resourceVersion: "16437"
		uid: 3dc60ca7-9788-11e7-ba64-080027e58fc6
```

Source表示的是该Events的来源，eg: replication-controller、kubelet。是哪个k8s组件在哪个host主机上生成的该Event。
这里的replication-controller表示的是控制器rc，不同于上面InvolvedObject属性中所说的。
```go
type EventSource struct {
	// Component from which the event is generated.
	// +optional
	Component string `json:"component,omitempty"`
	// Node name on which the event is generated.
	// +optional
	Host string `json:"host,omitempty"`
}
```
