# kubectl主体流程

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [kubectl主命令开始的地方](#kubectl开始run)
  - [func NewFactory](#cmd-factory)
  - [创建kubectl命令](#创建kubectl命令)
  - [kubectl get 为例](#添加get命令)
    - [func (f *factory) UnstructuredObject()](#func-f-factory-unstructuredobject)
    - [过滤函数和过滤参数](#过滤函数和过滤参数)
    - [Builder](#builder)
    - [infos](#infos-err-rinfos)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

## kubectl开始run
定义在/cmd/kubectl/app/kubectl.go
定义了一个cmd，然后执行cmd.Execute()
这里用到了第三方包"github.com/spf13/cobra"，这是一个功能强大的工具。
kubectl是基于其来构造生成命令行的
```go
func Run() error {
	logs.InitLogs()
	defer logs.FlushLogs()

	/*
		构建了一个cmd，然后调用了Execute
		参数除了几个标准的输入输出之外，就只有一个NewFactory

		NewKubectlCommand 定义在pkg/kubectl/cmd/cmd.go

		cmdutil.NewFactory(nil)
			==>/pkg/kubectl/cmd/util/factory.go
	*/
	cmd := cmd.NewKubectlCommand(cmdutil.NewFactory(nil), os.Stdin, os.Stdout, os.Stderr)
	return cmd.Execute()
}
```

## cmd Factory
定义在/pkg/kubectl/cmd/util/factory.go
```go
// NewFactory creates a factory with the default Kubernetes resources defined
// if optionalClientConfig is nil, then flags will be bound to a new clientcmd.ClientConfig.
// if optionalClientConfig is not nil, then this factory will make use of it.
/*
	译：func NewFactory用默认kubernetes resourecs 创建一个factory。
	   如果入参optionalClientConfig为nil，flags会被绑定到一个新的clientcmd.ClientConfig。
	   如果入参optionalClientConfig非nil，该factory会使用它。
*/
func NewFactory(optionalClientConfig clientcmd.ClientConfig) Factory {
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	flags.SetNormalizeFunc(utilflag.WarnWordSepNormalizeFunc) // Warn for "_" flags

	clientConfig := optionalClientConfig
	/*
		默认情况下，/cmd/kubectl/app/kubectl.go中传递过来的入参optionalClientConfig是nil
	*/
	if optionalClientConfig == nil {
		clientConfig = DefaultClientConfig(flags)
	}

	/*
		获取clients,type ClientCache struct
		type ClientCache struct 缓存先前加载的clients以便重用，并确保MatchServerVersion仅被调用一次
	*/
	clients := NewClientCache(clientConfig)

	f := &factory{
		flags:        flags,
		clientConfig: clientConfig,
		clients:      clients,
	}

	return f
}
```
其中type ClientCache struct提供的一个方法是：
```go
//根据指定的version初始化或者重用一个clientset
func (c *ClientCache) ClientSetForVersion(requiredVersion *unversioned.GroupVersion) (*internalclientset.Clientset, error)
```

## 创建kubectl命令
定义在/pkg/kubectl/cmd/cmd.go
```go
//NewKubectlCommand创建`kubectl`命令及其嵌套子命令。
func NewKubectlCommand(f cmdutil.Factory, in io.Reader, out, err io.Writer) *cobra.Command {
	/*
		kubectl 命令，根命令
	*/
	cmds := &cobra.Command{
		......
	}

	/*
		声明了多组 命令集合
		是对"github.com/spf13/cobra"的再一次封装

		/pkg/kubectl/cmd/templates/command_groups.go
			==>type CommandGroups []CommandGroup

		所有的命令都与入参f cmdutil.Factory有关，顺着f的数据流向搞懂factory的原理
	*/
	groups := templates.CommandGroups{......}
	/*
		Add定义在/pkg/kubectl/cmd/templates/command_groups.go
			==>func (g CommandGroups) Add(c *cobra.Command)
		把根命令kubectl 传递进去
		其完成的功能是把上面声明的所有命令(create、delete等)添加到kubectl下，成为kubectl的二级子命令
	*/
	groups.Add(cmds)
	return cmds
```
下面以get 命令为例子，go on！

## 添加get命令
定义在/pkg/kubectl/cmd/get.go
```go
//从server端获取数据
func NewCmdGet(f cmdutil.Factory, out io.Writer, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get [(-o|--output=)json|yaml|wide|custom-columns=...|custom-columns-file=...|go-template=...|go-template-file=...|jsonpath=...|jsonpath-file=...] (TYPE [NAME | -l label] | TYPE/NAME ...) [flags]",

		Run: func(cmd *cobra.Command, args []string) {
			err := RunGet(f, out, errOut, cmd, args, options)
			cmdutil.CheckErr(err)
		},
	}
	/*
		通过调用package cmdutil 中的函数来给一个cmd添加flag，主要用于一些公共flag
		或者直接添加flag
	*/
	cmdutil.AddPrinterFlags(cmd)
	cmd.Flags().StringP("selector", "l", "", "Selector (label query) to filter on")
	.......
	return cmd
}
```
RunGet函数是kubectl get命令真正执行的实体

下面以kubectl get po --namespace=kube-system为例子，go on！
```go
func RunGet(f cmdutil.Factory, out, errOut io.Writer, cmd *cobra.Command, args []string, options *GetOptions) error {
	/*
		eg: kubectl get po --namespace=kube-system
			options 为空
		len(options.Raw)＝0
	*/
	/*
		f cmdutil.Factory 定义在/pkg/kubectl/cmd/util/factory.go

		mappers包含了多个RESTMapper，如PriorityRESTMapper、MultiRESTMapper
		typer:
			map[{apps v1beta1 StatefulSet}:true {authorization.k8s.io v1beta1 SubjectAccessReview}:true
	*/
	mapper, typer, err := f.UnstructuredObject() [1]
	//获取过滤函数集合、过滤参数opts
	filterFuncs := f.DefaultResourceFilterFunc() [2]
	filterOpts := f.DefaultResourceFilterOptions(cmd, allNamespaces)

	/*
		cmdNamespace 是cmd中指定的namespace
		如果没指定，则使用default，此时enforceNamespace=false
		如果指定了enforceNamespace=true
	*/
	cmdNamespace, enforceNamespace, err := f.DefaultNamespace()
	
	if len(args) == 0 && cmdutil.IsFilenameEmpty(options.Filenames) {
		/*
			参数不对，get没有指定 the type of resource
			`kubectl get` 的时候进入这部分
		*/
	}
	
	/*
		kubectl get all 会获取pod、rc、svc资源
		此时会把printAll置为true
	*/
	for _, a := range args {
		if a == "all" {
			printAll = true
			break
		}
	}
	
	/*
		定义在/pkg/kubectl/resource/builder.go
			==>func HasNames(args []string) (bool, error)
		返回true如果提供的args里面含有resource names
		eg:
			kubectl get po po-tomcat 的时候argsHasNames为true
			kubectl get po 的时候argsHasNames为false
	*/
	argsHasNames, err := resource.HasNames(args)
	
	// handle watch separately since we cannot watch multiple resource types
	/*
		处理watch的handle要分离，因为我们不能watch多个resource types
	*/
	isWatch, isWatchOnly := cmdutil.GetFlagBool(cmd, "watch"), cmdutil.GetFlagBool(cmd, "watch-only")
	
	/*
		定义在/pkg/kubectl/resource/builder.go
			==>func NewBuilder
		Builder大多方法支持链式调用
		最后的Do()返回一个type Result struct

		这里一些列链式调用大部分都在根据传入的Cmd来设置新建Builder的属性值
	*/
	r := resource.NewBuilder(mapper, typer, resource.ClientMapperFunc(f.UnstructuredClientForMapping), runtime.UnstructuredJSONScheme).
		NamespaceParam(cmdNamespace).DefaultNamespace().AllNamespaces(allNamespaces).
		FilenameParam(enforceNamespace, &options.FilenameOptions).
		SelectorParam(selector).
		ExportParam(export).
		ResourceTypeOrNameArgs(true, args...).
		ContinueOnError().
		Latest().
		Flatten().
		Do() [3]
		
	/*
		根据cmd来获取输出的printer
		==>定义在/pkg/kubectl/cmd/util/printing.go
			==>func PrinterForCommand(cmd *cobra.Command) (kubectl.ResourcePrinter, bool, error)
		不指定格式时，generic=false
	*/
	printer, generic, err := cmdutil.PrinterForCommand(cmd)
	
	/*
		/pkg/kubectl/resource/result.go
			==>func (r *Result) Infos() ([]*Info, error)
		以数组的形式返回所有的resource infos。
		infos就是kubectl get命令查询server都后得到的结果

		type Info struct包含执行REST调用的临时信息，或显示已完成的REST调用的结果。
	*/
	infos, err := r.Infos() [4]
	
	/*
		把infos转化为obj
		objs[ix] 's type is: *runtime.Unstructured
	*/
	objs := make([]runtime.Object, len(infos))
	for ix := range infos {
		objs[ix] = infos[ix].Object [5]
	}
	//获取排序字段
	sorting, err := cmd.Flags().GetString("sort-by")
	
	/*
		printer重新置为nil，每个object都使用默认的printer
	*/
	printer = nil
	
	/*
		/pkg/kubectl/resource_printer.go
			==>func GetNewTabWriter(output io.Writer) *tabwriter.Writer
		返回一个tabwriter，将输入中的标签列转换成正确对齐的文本。
	*/
	w := kubectl.GetNewTabWriter(out)
	
	/*
		pkg/kubectl/cmd/util/helpers.go
			==>func MustPrintWithKinds
		如果showKind = true，则
		NAME             CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE
		svc/kubernetes   10.10.0.1    <none>        443/TCP   1h
	*/
	if cmdutil.MustPrintWithKinds(objs, infos, sorter, printAll) {
		showKind = true
	}
	
	/*
		下面的for循环就是正常的源码编译出来的kubectl get 一个资源输出的信息
	*/
	for ix := range objs{
		...
		if printer != nil {
				w.Flush()
				//不指定格式 或者 kubect -o wide 的时候走此处
				cmdutil.PrintFilterCount(filteredResourceCount, lastMapping.Resource, filterOpts)
			}
		...
	}
```
### func (f *factory) UnstructuredObject()
```go
/*
	func (f *factory) UnstructuredObject()
	返回用于处理任意runtime.Unstructured的接口。
	将执行API调用以便发现types。
	非结构化对象
*/
func (f *factory) UnstructuredObject() (meta.RESTMapper, runtime.ObjectTyper, error) {
	discoveryClient, err := f.DiscoveryClient()
	if err != nil {
		return nil, nil, err
	}
	groupResources, err := discovery.GetAPIGroupResources(discoveryClient)
	if err != nil && !discoveryClient.Fresh() {
		discoveryClient.Invalidate()
		groupResources, err = discovery.GetAPIGroupResources(discoveryClient)
	}
	if err != nil {
		return nil, nil, err
	}

	mapper := discovery.NewDeferredDiscoveryRESTMapper(discoveryClient, meta.InterfacesForUnstructured)
	typer := discovery.NewUnstructuredObjectTyper(groupResources)
	return NewShortcutExpander(mapper, discoveryClient), typer, nil
}
```
mapper 和 typer 的输出结果如下
```
mapper is:  {DeferredDiscoveryRESTMapper{
	PriorityRESTMapper{
	[{apps v1beta1 *} {authentication.k8s.io v1beta1 *} {authorization.k8s.io v1beta1 *} {autoscaling v1 *} {batch v1 *} {certificates.k8s.io v1alpha1 *} {extensions v1beta1 *} {policy v1beta1 *} {rbac.authorization.k8s.io v1alpha1 *} {storage.k8s.io v1beta1 *} { v1 *} {apps * *} {authentication.k8s.io * *} {authorization.k8s.io * *} {autoscaling * *} {batch * *} {certificates.k8s.io * *} {extensions * *} {policy * *} {rbac.authorization.k8s.io * *} {storage.k8s.io * *} { * *}]
	[apps/v1beta1, Kind=* authentication.k8s.io/v1beta1, Kind=* authorization.k8s.io/v1beta1, Kind=* autoscaling/v1, Kind=* batch/v1, Kind=* certificates.k8s.io/v1alpha1, Kind=* extensions/v1beta1, Kind=* policy/v1beta1, Kind=* rbac.authorization.k8s.io/v1alpha1, Kind=* storage.k8s.io/v1beta1, Kind=* /v1, Kind=* apps/*, Kind=* authentication.k8s.io/*, Kind=* authorization.k8s.io/*, Kind=* autoscaling/*, Kind=* batch/*, Kind=* certificates.k8s.io/*, Kind=* extensions/*, Kind=* policy/*, Kind=* rbac.authorization.k8s.io/*, Kind=* storage.k8s.io/*, Kind=* /*, Kind=*]
	MultiRESTMapper{
	DefaultRESTMapper{kindToPluralResource=map[apps/v1beta1, Kind=StatefulSetList:{apps v1beta1 statefulsetlists} apps/v1beta1, Kind=List:{apps v1beta1 lists} apps/v1beta1, Kind=StatefulSet:{apps v1beta1 statefulsets}]}
	DefaultRESTMapper{kindToPluralResource=map[authentication.k8s.io/v1beta1, Kind=List:{authentication.k8s.io v1beta1 lists} authentication.k8s.io/v1beta1, Kind=TokenReview:{authentication.k8s.io v1beta1 tokenreviews} authentication.k8s.io/v1beta1, Kind=TokenReviewList:{authentication.k8s.io v1beta1 tokenreviewlists}]}
	DefaultRESTMapper{kindToPluralResource=map[authorization.k8s.io/v1beta1, Kind=SubjectAccessReview:{authorization.k8s.io v1beta1 subjectaccessreviews} authorization.k8s.io/v1beta1, Kind=SubjectAccessReviewList:{authorization.k8s.io v1beta1 subjectaccessreviewlists} authorization.k8s.io/v1beta1, Kind=List:{authorization.k8s.io v1beta1 lists} authorization.k8s.io/v1beta1, Kind=LocalSubjectAccessReview:{authorization.k8s.io v1beta1 localsubjectaccessreviews} authorization.k8s.io/v1beta1, Kind=LocalSubjectAccessReviewList:{authorization.k8s.io v1beta1 localsubjectaccessreviewlists} authorization.k8s.io/v1beta1, Kind=SelfSubjectAccessReview:{authorization.k8s.io v1beta1 selfsubjectaccessreviews} authorization.k8s.io/v1beta1, Kind=SelfSubjectAccessReviewList:{authorization.k8s.io v1beta1 selfsubjectaccessreviewlists}]}
	DefaultRESTMapper{kindToPluralResource=map[autoscaling/v1, Kind=HorizontalPodAutoscaler:{autoscaling v1 horizontalpodautoscalers} autoscaling/v1, Kind=HorizontalPodAutoscalerList:{autoscaling v1 horizontalpodautoscalerlists} autoscaling/v1, Kind=List:{autoscaling v1 lists}]}
	DefaultRESTMapper{kindToPluralResource=map[batch/v1, Kind=Job:{batch v1 jobs} batch/v1, Kind=JobList:{batch v1 joblists} batch/v1, Kind=List:{batch v1 lists}]}
	DefaultRESTMapper{kindToPluralResource=map[certificates.k8s.io/v1alpha1, Kind=CertificateSigningRequestList:{certificates.k8s.io v1alpha1 certificatesigningrequestlists} certificates.k8s.io/v1alpha1, Kind=List:{certificates.k8s.io v1alpha1 lists} certificates.k8s.io/v1alpha1, Kind=CertificateSigningRequest:{certificates.k8s.io v1alpha1 certificatesigningrequests}]}
	DefaultRESTMapper{kindToPluralResource=map[extensions/v1beta1, Kind=ReplicaSet:{extensions v1beta1 replicasets} extensions/v1beta1, Kind=ReplicationControllerDummy:{extensions v1beta1 replicationcontrollerdummies} extensions/v1beta1, Kind=List:{extensions v1beta1 lists} extensions/v1beta1, Kind=DeploymentList:{extensions v1beta1 deploymentlists} extensions/v1beta1, Kind=HorizontalPodAutoscalerList:{extensions v1beta1 horizontalpodautoscalerlists} extensions/v1beta1, Kind=NetworkPolicyList:{extensions v1beta1 networkpolicylists} extensions/v1beta1, Kind=NetworkPolicy:{extensions v1beta1 networkpolicies} extensions/v1beta1, Kind=ReplicationControllerDummyList:{extensions v1beta1 replicationcontrollerdummylists} extensions/v1beta1, Kind=ThirdPartyResourceList:{extensions v1beta1 thirdpartyresourcelists} extensions/v1beta1, Kind=Deployment:{extensions v1beta1 deployments} extensions/v1beta1, Kind=ScaleList:{extensions v1beta1 scalelists} extensions/v1beta1, Kind=HorizontalPodAutoscaler:{extensions v1beta1 horizontalpodautoscalers} extensions/v1beta1, Kind=IngressList:{extensions v1beta1 ingresslists} extensions/v1beta1, Kind=Job:{extensions v1beta1 jobs} extensions/v1beta1, Kind=JobList:{extensions v1beta1 joblists} extensions/v1beta1, Kind=ThirdPartyResource:{extensions v1beta1 thirdpartyresources} extensions/v1beta1, Kind=DaemonSetList:{extensions v1beta1 daemonsetlists} extensions/v1beta1, Kind=DeploymentRollback:{extensions v1beta1 deploymentrollbacks} extensions/v1beta1, Kind=DeploymentRollbackList:{extensions v1beta1 deploymentrollbacklists} extensions/v1beta1, Kind=ReplicaSetList:{extensions v1beta1 replicasetlists} extensions/v1beta1, Kind=DaemonSet:{extensions v1beta1 daemonsets} extensions/v1beta1, Kind=Scale:{extensions v1beta1 scales} extensions/v1beta1, Kind=Ingress:{extensions v1beta1 ingresses}]}
	DefaultRESTMapper{kindToPluralResource=map[policy/v1beta1, Kind=PodDisruptionBudget:{policy v1beta1 poddisruptionbudgets} policy/v1beta1, Kind=PodDisruptionBudgetList:{policy v1beta1 poddisruptionbudgetlists} policy/v1beta1, Kind=List:{policy v1beta1 lists}]}
	DefaultRESTMapper{kindToPluralResource=map[rbac.authorization.k8s.io/v1alpha1, Kind=ClusterRoleList:{rbac.authorization.k8s.io v1alpha1 clusterrolelists} rbac.authorization.k8s.io/v1alpha1, Kind=ClusterRoleBinding:{rbac.authorization.k8s.io v1alpha1 clusterrolebindings} rbac.authorization.k8s.io/v1alpha1, Kind=ClusterRole:{rbac.authorization.k8s.io v1alpha1 clusterroles} rbac.authorization.k8s.io/v1alpha1, Kind=RoleBindingList:{rbac.authorization.k8s.io v1alpha1 rolebindinglists} rbac.authorization.k8s.io/v1alpha1, Kind=Role:{rbac.authorization.k8s.io v1alpha1 roles} rbac.authorization.k8s.io/v1alpha1, Kind=RoleList:{rbac.authorization.k8s.io v1alpha1 rolelists} rbac.authorization.k8s.io/v1alpha1, Kind=List:{rbac.authorization.k8s.io v1alpha1 lists} rbac.authorization.k8s.io/v1alpha1, Kind=ClusterRoleBindingList:{rbac.authorization.k8s.io v1alpha1 clusterrolebindinglists} rbac.authorization.k8s.io/v1alpha1, Kind=RoleBinding:{rbac.authorization.k8s.io v1alpha1 rolebindings}]}
	DefaultRESTMapper{kindToPluralResource=map[storage.k8s.io/v1beta1, Kind=List:{storage.k8s.io v1beta1 lists} storage.k8s.io/v1beta1, Kind=StorageClass:{storage.k8s.io v1beta1 storageclasses} storage.k8s.io/v1beta1, Kind=StorageClassList:{storage.k8s.io v1beta1 storageclasslists}]}
	DefaultRESTMapper{kindToPluralResource=map[/v1, Kind=EventList:{ v1 eventlists} /v1, Kind=NamespaceList:{ v1 namespacelists} /v1, Kind=ServiceList:{ v1 servicelists} /v1, Kind=PersistentVolumeList:{ v1 persistentvolumelists} /v1, Kind=PodTemplateList:{ v1 podtemplatelists} /v1, Kind=ReplicationControllerList:{ v1 replicationcontrollerlists} /v1, Kind=ResourceQuotaList:{ v1 resourcequotalists} /v1, Kind=ServiceAccount:{ v1 serviceaccounts} /v1, Kind=EndpointsList:{ v1 endpointslists} /v1, Kind=PersistentVolume:{ v1 persistentvolumes} /v1, Kind=EvictionList:{ v1 evictionlists} /v1, Kind=ResourceQuota:{ v1 resourcequotas} /v1, Kind=List:{ v1 lists} /v1, Kind=ConfigMapList:{ v1 configmaplists} /v1, Kind=PersistentVolumeClaimList:{ v1 persistentvolumeclaimlists} /v1, Kind=Scale:{ v1 scales} /v1, Kind=SecretList:{ v1 secretlists} /v1, Kind=ScaleList:{ v1 scalelists} /v1, Kind=ComponentStatus:{ v1 componentstatuses} /v1, Kind=ConfigMap:{ v1 configmaps} /v1, Kind=Endpoints:{ v1 endpoints} /v1, Kind=LimitRange:{ v1 limitranges} /v1, Kind=Node:{ v1 nodes} /v1, Kind=PodList:{ v1 podlists} /v1, Kind=Eviction:{ v1 evictions} /v1, Kind=BindingList:{ v1 bindinglists} /v1, Kind=ComponentStatusList:{ v1 componentstatuslists} /v1, Kind=LimitRangeList:{ v1 limitrangelists} /v1, Kind=Namespace:{ v1 namespaces} /v1, Kind=PersistentVolumeClaim:{ v1 persistentvolumeclaims} /v1, Kind=PodTemplate:{ v1 podtemplates} /v1, Kind=ServiceAccountList:{ v1 serviceaccountlists} /v1, Kind=NodeList:{ v1 nodelists} /v1, Kind=ReplicationController:{ v1 replicationcontrollers} /v1, Kind=Service:{ v1 services} /v1, Kind=Binding:{ v1 bindings} /v1, Kind=Event:{ v1 events} /v1, Kind=Pod:{ v1 pods} /v1, Kind=Secret:{ v1 secrets}]}
}
}
} [{ pods} { replicationcontrollers} { services} {apps statefulsets} {autoscaling horizontalpodautoscalers} {extensions jobs} {extensions deployments} {extensions replicasets}] 0xc420552000}
```
```
typer is:  &{map[{policy v1beta1 PodDisruptionBudget}:true { v1 Secret}:true {certificates.k8s.io v1alpha1 CertificateSigningRequest}:true {extensions v1beta1 DeploymentRollback}:true { v1 ConfigMap}:true { v1 Eviction}:true { v1 PodTemplate}:true {apps v1beta1 StatefulSet}:true {authorization.k8s.io v1beta1 SelfSubjectAccessReview}:true {authorization.k8s.io v1beta1 SubjectAccessReview}:true { v1 ReplicationController}:true {extensions v1beta1 Scale}:true {extensions v1beta1 Job}:true {extensions v1beta1 NetworkPolicy}:true {rbac.authorization.k8s.io v1alpha1 Role}:true { v1 LimitRange}:true { v1 PersistentVolumeClaim}:true {authentication.k8s.io v1beta1 TokenReview}:true {extensions v1beta1 Deployment}:true {rbac.authorization.k8s.io v1alpha1 RoleBinding}:true {storage.k8s.io v1beta1 StorageClass}:true { v1 Binding}:true { v1 ComponentStatus}:true { v1 Node}:true {extensions v1beta1 DaemonSet}:true {extensions v1beta1 ReplicationControllerDummy}:true {rbac.authorization.k8s.io v1alpha1 ClusterRoleBinding}:true { v1 PersistentVolume}:true { v1 Pod}:true { v1 ResourceQuota}:true {autoscaling v1 HorizontalPodAutoscaler}:true {rbac.authorization.k8s.io v1alpha1 ClusterRole}:true { v1 Endpoints}:true { v1 Service}:true {extensions v1beta1 HorizontalPodAutoscaler}:true {extensions v1beta1 ReplicaSet}:true { v1 Scale}:true {extensions v1beta1 ThirdPartyResource}:true { v1 Event}:true { v1 Namespace}:true { v1 ServiceAccount}:true {authorization.k8s.io v1beta1 LocalSubjectAccessReview}:true {batch v1 Job}:true {extensions v1beta1 Ingress}:true]}
```

### 过滤函数和过滤参数
目前只返回一个pod的过滤函数
/pkg/kubectl/resource_filter.go  中的过滤函数
```go
/*
	译：如果跳过该pod，则filterPods将返回true。
	   对于终止的pod，默认为true
*/
func filterPods(obj runtime.Object, options PrintOptions) bool 

```
Options 定义在/pkg/kubectl/resource_printer.go
根据输入的cmd来设置该结构体的值
```go
type PrintOptions struct {
	NoHeaders          bool
	WithNamespace      bool
	WithKind           bool
	Wide               bool
	ShowAll            bool
	ShowLabels         bool
	AbsoluteTimestamps bool
	Kind               string
	ColumnLabels       []string
}
```
### Builder
NamespaceParam、DefaultNamespace......这些函数都是对Builder的属性进行设置，
最后Do()返回一个type Result struct
该type Result struct中含有一个visitor Visitor，
visitor 能访问在Builder中定义的resources。

### infos, err := r.Infos()
```go
// Infos returns an array of all of the resource infos retrieved via traversal.
// Will attempt to traverse the entire set of visitors only once, and will return
// a cached list on subsequent calls.
/*
	译：func (r *Result) Infos()以数组的形式返回所有的resource infos。
		尝试遍历整个visitors一次，然后在后续的调用中将返回一个cached list。
*/
func (r *Result) Infos() ([]*Info, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.info != nil {
		return r.info, nil
	}

	infos := []*Info{}
	err := r.visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		/*
			一个个info添加到infos中
			下面会展示在这个地方输出的info信息
		*/
		infos = append(infos, info)
		return nil
	})
	err = utilerrors.FilterOut(err, r.ignoreErrors...)

	r.info, r.err = infos, err
	return infos, err
}

// Info contains temporary info to execute a REST call, or show the results
// of an already completed REST call.
/*
	type Info struct包含执行REST调用的临时信息，或显示已完成的REST调用的结果。
*/
type Info struct {
	Client    RESTClient
	Mapping   *meta.RESTMapping
	Namespace string
	Name      string

	// Optional, Source is the filename or URL to template file (.json or .yaml),
	// or stdin to use to handle the resource
	/*
		译：可选，Source是模板文件（.json或.yaml）的文件名或URL，或用于处理资源的stdin
	*/
	Source string
	// Optional, this is the provided object in a versioned type before defaulting
	// and conversions into its corresponding internal type. This is useful for
	// reflecting on user intent which may be lost after defaulting and conversions.
	/*
		译：可选，这是一个version中的object，会有一个对应的internal type。
			这将有利于在把该object转为为之后对应的internal type之后，反应出用户的意图，
	*/
	VersionedObject interface{}
	// Optional, this is the most recent value returned by the server if available
	/*
		译：可选，这是服务器返回的最新值（如果可用）
	*/
	runtime.Object
	// Optional, this is the most recent resource version the server knows about for
	// this type of resource. It may not match the resource version of the object,
	// but if set it should be equal to or newer than the resource version of the
	// object (however the server defines resource version).
	/*
		译：可选，这是server端知道的此类resource的最新resource version。
			它可能与该object的resource version 不匹配，
			它应该等于或新于object的resource version (但服务器定义资源版本）。
			
		简单来说，ResourceVersion的值是etcd中全局最新的Index
	*/
	ResourceVersion string
	// Optional, should this resource be exported, stripped of cluster-specific and instance specific fields
	Export bool
}
```
一个Info的输出信息如下：
```
1、kubectl get node而言，
	NAME      STATUS    AGE
	fqhnode   Ready     2h
	
	a info's Name is: fqhnode
	a info's Object 's type is: *runtime.Unstructured
	a info's Object 's value is: &{map[metadata:map[namespace: name:fqhnode selfLink:/api/v1/nodesfqhnode uid:be2234fd-8d4f-11e7-9c42-080027e58fc6 resourceVersion:9781 creationTimestamp:2017-08-30T06:52:08Z labels:map[beta.kubernetes.io/os:linux kubernetes.io/hostname:fqhnode beta.kubernetes.io/arch:amd64] annotations:map[volumes.kubernetes.io/controller-managed-attach-detach:true]] spec:map[externalID:fqhnode] status:map[daemonEndpoints:map[kubeletEndpoint:map[Port:10250]] nodeInfo:map[systemUUID:03B30D09-EBDF-4E73-9F13-904C9BA98883 kernelVersion:3.10.0-514.6.1.el7.x86_64 kubeletVersion:v1.5.2+08e0995 operatingSystem:linux architecture:amd64 machineID:03b30d09ebdf4e739f13904c9ba98883 bootID:cf04dbc5-c529-4752-a1bf-ee3e3ab8833f osImage:CentOS Linux 7 (Core) containerRuntimeVersion:docker://1.13.1 kubeProxyVersion:v1.5.2+08e0995] images:[map[names:[hikdata.io/tomcat:7.0.78] sizeBytes:862575480] map[names:[etcd_cluster:gc4.0] sizeBytes:341890262] map[names:[redis-sentinel:v2.0] sizeBytes:289596922] map[names:[gcr.io/google_containers/redis:v1 kubernetes/redis:v1] sizeBytes:145954175] map[sizeBytes:129465861 names:[ubuntu:16.04]] map[names:[registry@sha256:28be0609f90ef53e86e1872a11d672434ce1361711760cf1fe059efd222f8d37 registry@sha256:8698d41d35eb1491822e7dbb8bfb15c74aea231c2709700b5e9a0023c3649253 registry:latest] sizeBytes:33174034] map[names:[portainer/portainer@sha256:0e7c150ad00b8c0e2a6d6a18ef7cdc349db898c8dbb850346fbed7de9ddb3772 portainer/portainer:latest] sizeBytes:9920059] map[names:[busybox:latest] sizeBytes:1109996] map[names:[gcr.io/google_containers/pause-amd64:3.0] sizeBytes:746888]] capacity:map[alpha.kubernetes.io/nvidia-gpu:0 cpu:1 memory:2048668Ki pods:110] allocatable:map[alpha.kubernetes.io/nvidia-gpu:0 cpu:1 memory:2048668Ki pods:110] conditions:[map[type:OutOfDisk status:False lastHeartbeatTime:2017-08-30T09:19:22Z lastTransitionTime:2017-08-30T06:52:08Z reason:KubeletHasSufficientDisk message:kubelet has sufficient disk space available] map[type:MemoryPressure status:False lastHeartbeatTime:2017-08-30T09:19:22Z lastTransitionTime:2017-08-30T06:52:08Z reason:KubeletHasSufficientMemory message:kubelet has sufficient memory available] map[type:DiskPressure status:False lastHeartbeatTime:2017-08-30T09:19:22Z lastTransitionTime:2017-08-30T06:52:08Z reason:KubeletHasNoDiskPressure message:kubelet has no disk pressure] map[lastTransitionTime:2017-08-30T06:52:19Z reason:KubeletReady message:kubelet is posting ready status type:Ready status:True lastHeartbeatTime:2017-08-30T09:19:22Z]] addresses:[map[type:LegacyHostIP address:10.0.2.15] map[type:InternalIP address:10.0.2.15] map[type:Hostname address:fqhnode]]] kind:Node apiVersion:v1]}
	a info's Export is: false
	a info's VersionedObject TypeOf is: <nil>
	a info's VersionedObject ValueOf is: <invalid reflect.Value>
				
				
2、如果get一个resource，没有资源的时候，会提示No resources found.

3、kubectl get pod
	NAME            READY     STATUS    RESTARTS   AGE
	tomcat7-psnhd   1/1       Running   0          12s
	
	a info's Name is: tomcat7-9r1gx
	a info's Object 's type is: *runtime.Unstructured
	a info's Object 's value is: &{map[metadata:map[namespace:default uid:8d06d6cb-8d62-11e7-9c42-080027e58fc6 creationTimestamp:2017-08-30T09:06:47Z labels:map[name:tomcat7] annotations:map[kubernetes.io/created-by:{"kind":"SerializedReference","apiVersion":"v1","reference":{"kind":"ReplicationController","namespace":"default","name":"tomcat7","uid":"8cfa248e-8d62-11e7-9c42-080027e58fc6","apiVersion":"v1","resourceVersion":"8929"}}
] ownerReferences:[map[name:tomcat7 uid:8cfa248e-8d62-11e7-9c42-080027e58fc6 controller:true apiVersion:v1 kind:ReplicationController]] name:tomcat7-9r1gx generateName:tomcat7- selfLink:/api/v1/namespaces/default/pods/tomcat7-9r1gx resourceVersion:8947] spec:map[terminationGracePeriodSeconds:30 dnsPolicy:ClusterFirst nodeName:fqhnode securityContext:map[] volumes:[map[hostPath:map[path:/opt/tomcat/conf] name:conf] map[name:webapps hostPath:map[path:/opt/tomcat/webapps]]] containers:[map[terminationMessagePath:/dev/termination-log imagePullPolicy:IfNotPresent name:tomcat7 image:hikdata.io/tomcat:7.0.78 resources:map[] volumeMounts:[map[name:conf mountPath:/opt/tomcat/conf] map[name:webapps mountPath:/opt/tomcat/webapps]]]] restartPolicy:Always] status:map[phase:Running conditions:[map[type:Initialized status:True lastProbeTime:<nil> lastTransitionTime:2017-08-30T09:06:47Z] map[status:True lastProbeTime:<nil> lastTransitionTime:2017-08-30T09:06:49Z type:Ready] map[type:PodScheduled status:True lastProbeTime:<nil> lastTransitionTime:2017-08-30T09:06:47Z]] hostIP:10.0.2.15 podIP:172.17.0.5 startTime:2017-08-30T09:06:47Z containerStatuses:[map[image:hikdata.io/tomcat:7.0.78 imageID:docker://sha256:5055de391846a56ad9b428ed144941cb523bb168154ca2837871ef614fd00077 containerID:docker://c9719d95fe938228b77319d05041e4557349b9857c547662e6fc33316a789d27 name:tomcat7 state:map[running:map[startedAt:2017-08-30T09:06:48Z]] lastState:map[] ready:true restartCount:0]]] kind:Pod apiVersion:v1]}
	a info's ResourceVersion is: 9419
	a info's Export is: false
	a info's VersionedObject TypeOf is: <nil>
	a info's VersionedObject ValueOf is: <invalid reflect.Value>
```
# 总结
至此kubectl的主体流程已经结束，下一步我们将要详细分析几个核心点
- cmdutil.Factory
- mapper, typer
- Builder
- type Result struct
- type Result struct里面的visitor Visitor
- printer

我主要以`func RunGet(f cmdutil.Factory, out, errOut io.Writer, cmd *cobra.Command, args []string, options *GetOptions)`函数的运行过程为主线，对各个概念进行了解。