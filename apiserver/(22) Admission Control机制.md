# Admission Control机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Admission Control的定义](#admission-control的定义)
  - [调用过程](#调用过程)
  - [chainAdmissionHandler](#chainadmissionhandler)
  - [所有plugin的通用函数](#所有plugin的通用函数)
  - [type attributesRecord struct](type-attributesrecord-struct)
  - [type Handler struct](#type-handler-struct)
  - [支持的设置请求类型](#支持的设置请求类型)
  - [总结](#总结)
<!-- END MUNGE: GENERATED_TOC -->

## Admission Control的定义
kubernetes 有3个层次的资源限制方式，分别在Container、Pod、Namespace 层次。
- Container层次主要利用容器本身的支持，比如Docker 对CPU、内存等的支持；
- Pod方面可以限制系统内创建Pod的资源范围，比如最大或者最小的CPU、memory需求；
- Namespace层次就是对用户级别的资源限额了，包括CPU、内存，还可以限定Pod、rc、service的数量。

当一个请求通过了认证和授权机制的认可之后，还需要经过准入控制处理通过之后，apiserver才会处理这个请求。 Admission Control有一个准入控制列表，可以通过命令行设置选择执行哪几个准入控制器。 只有所有的准入控制器都检查通过之后，apiserver才执行该请求，否则返回拒绝。

同时Admission Control也会被用来给系统的某些对象设置默认值，以防止用户不提供对应的值。

不同于授权和认证只关心请求的用户和操作，准入控制还处理请求的内容，并且仅对创建、更新、删除或连接（如代理）等有效，而对读操作无效。

推荐的plugin设置顺序如下：
```go
--admission-control=NamespaceLifecycle,LimitRanger,ServiceAccount,PersistentVolumeLabel,DefaultStorageClass,ResourceQuota,DefaultTolerationSeconds
```

目前Admission Control主要有以下插件：
- AlwaysAdmit: 允许所有的请求通过
- AlwaysDeny: 拒绝所有的请求
- ServiceAccount: ServiceAccount会在生成的容器中注入安全相关的信息以访问Kubernetes集群
- SecurityContextDeny: PodSecurityPolicy相关，PodSecurityPolicy可以对容器进行各种限制
- ResourceQuota: 为集群上的当前用户强制执行配额约束，如果配额不足，可能会拒绝请求。 目前支持cpu、memory、pods数量等。（设置整个namespace的总配额）
- LimitRanger: 在一个namespace中对所有资源使用limit限制，目前仅支持cpu和memory。（设置该namespace内所有的pod、container单个的资源使用上限）
- NamespaceLifecycle: 如果context中的namespace不存在，拒绝request。同时也会拒绝在正在删除中的Namespace下创建资源的请求
- DefaultStorageClass，设置PersistentVolumeClaim默认的storage class，防止用户不提供值。

## 调用过程
1. /cmd/kube-apiserver/app/server.go中获取所有注册的plugin
```go
/*
		***admission Control模块***
		准入控制器 admissionController
			==>/pkg/admission/chain.go
				==>func NewFromPlugins
	*/
	admissionController, err := admission.NewFromPlugins(client, admissionControlPluginNames, s.GenericServerRunOptions.AdmissionControlConfigFile, pluginInitializer)
	if err != nil {
		glog.Fatalf("Failed to initialize plugins: %v", err)
	}
```
2. 通过APIGroupVersion传递下去
3. /pkg/apiserver/resthandler.go中生成RestfulAPI使用Admission
```go
updateAdmit := func(updatedObject runtime.Object, currentObject runtime.Object) error {
			/*
				admit.Handles(admission.Update)和admit.Admit定义在
					==>/pkg/admission/chain.go
						==>func (admissionHandler chainAdmissionHandler) Admit(a Attributes)
				在里面会遍历各种plugin
			*/
			if admit != nil && admit.Handles(admission.Update) {
				userInfo, _ := api.UserFrom(ctx)
				return admit.Admit(admission.NewAttributesRecord(updatedObject, currentObject, scope.Kind, namespace, name, scope.Resource, scope.Subresource, admission.Update, userInfo))
			}

			return nil
		}
```

## chainAdmissionHandler
chainAdmissionHandler, admission认证的入口，见/pkg/admission/chain.go
```go
// chainAdmissionHandler is an instance of admission.Interface that performs admission control using a chain of admission handlers

type chainAdmissionHandler []Interface
```

来看看其实现的功能函数：
### NewFromPlugins  
```go
// NewFromPlugins returns an admission.Interface that will enforce admission control decisions of all
// the given plugins.
/*
	NewFromPlugins返回一个admission.Interface，它将执行所有给定插件的准入控制决策。
	把admission中的plugin加入到chainAdmissionHandler中
*/
func NewFromPlugins(client clientset.Interface, pluginNames []string, configFilePath string, plugInit PluginInitializer) (Interface, error) {
	plugins := []Interface{}
	for _, pluginName := range pluginNames {
		plugin := InitPlugin(pluginName, client, configFilePath)
		if plugin != nil {
			plugins = append(plugins, plugin)
		}
	}
	plugInit.Initialize(plugins)
	// ensure that plugins have been properly initialized
	if err := Validate(plugins); err != nil {
		return nil, err
	}
	/*
		类型转换
	*/
	return chainAdmissionHandler(plugins), nil
}
```

### Admit 
使用每个plugin对请求进行检查。如果一个plugin不能处理该请求的动作，则直接略过。 
每个plugin都实现有Admit()方法，都有一个handler对象 。
```go
// Admit performs an admission control check using a chain of handlers, and returns immediately on first error
/*
	在kube-apiserver生成RestfulAPI的时候调用到
	/pkg/apiserver/resthandler.go中（搜 admit != nil ）
*/
func (admissionHandler chainAdmissionHandler) Admit(a Attributes) error {
	//查看请求是否符合每一个admission

	for _, handler := range admissionHandler {
		/*
			ResourceQuota的Handler定义在
				==>/plugin/pkg/admission/resourcequota/admission.go
					==>func NewResourceQuota
						==>Handler:   admission.NewHandler(admission.Create, admission.Update),
		*/
		if !handler.Handles(a.GetOperation()) {
			continue
		}
		/*
			调用各个plugin的Admit函数
		*/
		err := handler.Admit(a)
		if err != nil {
			return err
		}
	}
	return nil
}
```

### Handles
判断chainAdmissionHandler中是否存在plugin可以处理该请求, 只要存在一个，直接return true。
```go
// Handles will return true if any of the handlers handles the given operation
func (admissionHandler chainAdmissionHandler) Handles(operation Operation) bool {
	for _, handler := range admissionHandler {
		if handler.Handles(operation) {
			return true
		}
	}
	return false
}
```

## 所有plugin的通用函数
所有的plugin在初始化的时候，都会调用这里的函数进行注册和实例化。 见/pkg/admission/plugins.go

### InitPlugin
```go
// InitPlugin creates an instance of the named interface.
func InitPlugin(name string, client clientset.Interface, configFilePath string) Interface {
	var (
		config *os.File
		err    error
	)

	if name == "" {
		glog.Info("No admission plugin specified.")
		return nil
	}

	if configFilePath != "" {
		config, err = os.Open(configFilePath)
		if err != nil {
			glog.Fatalf("Couldn't open admission plugin configuration %s: %#v",
				configFilePath, err)
		}

		defer config.Close()
	}

	/*
		通过getPlugin()获取plugin
	*/
	plugin, found, err := getPlugin(name, client, config)
	if err != nil {
		glog.Fatalf("Couldn't init admission plugin %q: %v", name, err)
	}
	if !found {
		glog.Fatalf("Unknown admission plugin: %s", name)
	}

	return plugin
}
```

### getPlugin
创建一个指定的plugin实例，name必须指定。 
只有name指定，且初始化失败了，才会return error。
```go
// getPlugin creates an instance of the named plugin.  It returns `false` if the
// the name is not known. The error is returned only when the named provider was
// known but failed to initialize.  The config parameter specifies the io.Reader
// handler of the configuration file for the cloud provider, or nil for no configuration.

func getPlugin(name string, client clientset.Interface, config io.Reader) (Interface, bool, error) {
	pluginsMutex.Lock()
	defer pluginsMutex.Unlock()
	//从plugins中获取指定plugin的初始化函数
	f, found := plugins[name]
	if !found {
		return nil, false, nil
	}

	config1, config2, err := splitStream(config)
	if err != nil {
		return nil, true, err
	}
	if !PluginEnabledFn(name, config1) {
		return nil, true, nil
	}

	ret, err := f(client, config2)
	return ret, true, err
}
```

### RegisterPlugin
所有的admission controller plugin都会通过`func RegisterPlugin`注册到`plugins = make(map[string]Factory)`中
```go
// RegisterPlugin registers a plugin Factory by name. This
// is expected to happen during app startup.
func RegisterPlugin(name string, plugin Factory) {
	pluginsMutex.Lock()
	defer pluginsMutex.Unlock()
	_, found := plugins[name]
	if found {
		glog.Fatalf("Admission plugin %q was registered twice", name)
	}
	glog.V(1).Infof("Registered admission plugin %q", name)
	plugins[name] = plugin
}
```

## type attributesRecord struct
每次request请求都会新生成一个admission.Attributes对象，而admission control 模块就是负责对一个a admission.Attributes进行检查，以判断该request是否满足约束。 见/pkg/admission/attributes.go
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

## type Handler struct
每个plugin 都会有一个`type Handler struct`对象，提供 Handls()函数。
各个plugin会在其Handls()函数中声明自己能够处理哪些请求，比如create、delete、update。
```go
// Handler is a base for admission control handlers that
// support a predefined set of operations
/*
	handler可以判断一个plugin是否需要处理某个请求
*/
type Handler struct {
	operations sets.String //操作的集合
	readyFunc  ReadyFunc   //标识该plugin是否做好处理请求的准备
}
```
### Handles
判断请求的动作是否在plugin的处理动作列表中
```go
// Handles returns true for methods that this handler supports
func (h *Handler) Handles(operation Operation) bool {
	return h.operations.Has(string(operation))
}
```
### func NewHandler
用于给一个plugin生成一个新的handler，设置operations
```go
// NewHandler creates a new base handler that handles the passed
// in operations
func NewHandler(ops ...Operation) *Handler {
	operations := sets.NewString()
	for _, op := range ops {
		operations.Insert(string(op))
	}
	return &Handler{
		operations: operations,
	}
}
```
### SetReadyFunc
设置handler的readyFunc字段
```go
// SetReadyFunc allows late registration of a ReadyFunc to know if the handler is ready to process requests.

func (h *Handler) SetReadyFunc(readyFunc ReadyFunc) {
	h.readyFunc = readyFunc
}
```

## 支持的设置请求类型
见/pkg/admission/interfaces.go
```go
// Operation constants
const (
	Create  Operation = "CREATE"
	Update  Operation = "UPDATE"
	Delete  Operation = "DELETE"
	Connect Operation = "CONNECT"
)
```

## 总结
要了解Admission机制，需要了解下面三者的关系即可：
- type chainAdmissionHandler []Interface汇总了所有的plugin
- 各个plugin，以及/pkg/admission/plugins.go中的plugin注册等函数
- type Handler struct，记录了一个plugin能够处理的请求类型

一个Admission controller plugin声明了自己需要对什么动作进行检查。 
同时，在创建如podEvaluator这些具体的Evaluator的时候，也针对自身的Kind声明了只需要对什么动作进行检查。 
所以，只有两边有交集的时候，一个plugin才会对一个Kind的进行quota检查。
