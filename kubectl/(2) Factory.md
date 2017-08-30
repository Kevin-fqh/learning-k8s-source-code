# cmdutil.Factory 解析

接着上一篇文章，
从func NewFactory出发
## func NewFactory
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
		设置默认的clientConfig
	*/
	if optionalClientConfig == nil {
		clientConfig = DefaultClientConfig(flags) [1]
	}

	/*
		获取client
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
这里需要了解的关键点有
- type Factory interface
- func DefaultClientConfig
- func NewClientCache

### import文件引入的init函数
NewClientCache 定义在/pkg/kubectl/cmd/util/clientcache.go
会有类似于apiserver 资源注册的init函数执行
(NewOrDie，创建了一个默认的APIRegistrationManager)
```go
"k8s.io/kubernetes/pkg/api/unversioned"
"k8s.io/kubernetes/pkg/apimachinery/registered"
```

### type Factory interface
```go
// Factory provides abstractions that allow the Kubectl command to be extended across multiple types
// of resources and different API sets.
/*
	译：Factory定义了一系列抽象动作，目的就是用于kubectl command可以扩展很多不同的types和不同的 API sets
*/
// TODO: make the functions interfaces
// TODO: pass the various interfaces on the factory directly into the command constructors (so the
// commands are decoupled from the factory).
type Factory interface {
	// Returns internal flagset
	FlagSet() *pflag.FlagSet

	// Returns a discovery client
	DiscoveryClient() (discovery.CachedDiscoveryInterface, error)
	// Returns interfaces for dealing with arbitrary runtime.Objects.
	Object() (meta.RESTMapper, runtime.ObjectTyper)
	// Returns interfaces for dealing with arbitrary
	// runtime.Unstructured. This performs API calls to discover types.
	/*
		译：返回用于处理任意runtime.Unstructured的接口。 这将执行API调用以便发现types。
	*/
	UnstructuredObject() (meta.RESTMapper, runtime.ObjectTyper, error)
```

### func DefaultClientConfig(flags *pflag.FlagSet)
总结起来应该是基于默认规则loadingRules, 重写信息overrides和os.Stdin来生成ClientConfig
```go
/*
	译：func DefaultClientConfig 根据下面的规则来生成一个clientcmd.ClientConfig。规则呈现如下层次结构

	一: 使用kubeconfig builder。这里的合并和覆盖次数有点疯狂。
		1、合并kubeconfig本身。 这是通过以下层次结构规则完成的：
			(1)CommandLineLocation - 这是从命令行解析的，so it must be late bound。
			   如果指定了这一点，则不会合并其他kubeconfig文件。 此文件必须存在。
			(2)如果设置了$KUBECONFIG，那么它被视为应该被合并的文件之一。
			(2)主目录位置 HomeDirectoryLocation
		2、根据此规则链中的第一个命中确定要使用的上下文---context
			(1)命令行参数 - 再次从命令行解析，so it must be late bound
			(2)CurrentContext from the merged kubeconfig file
			(3)Empty is allowed at this stage
		3、确定要使用的群集信息和身份验证信息。---cluster info and auth info
		   在这一点上，我们可能有也可能没有上下文。
		   他们是建立在这个规则链中的第一个命中。（运行两次，一次为auth，一次为集群）
			(1)命令行参数
			(2)If context is present, then use the context value
			(3)Empty is allowed
		4、确定要使用的实际群集信息。---actual cluster info
		   在这一点上，我们可能有也可能没有集群信息。
		   基于下述规则链构建集群信息：
			(1)命令行参数
			(2)If cluster info is present and a value for the attribute is present, use it.
			(3)If you don't have a server location, bail.
		5、cluster info and auth info是使用同样的规则来进行创建的。
		   除非你在auth info仅仅使用了一种认证方式。
		   下述情况将会导致ERROR：
			(1)如果从命令行指定了两个冲突的认证方式，则失败。
			(2)如果命令行未指定，并且auth info具有冲突的认证方式，则失败。
			(3)如果命令行指定一个，并且auth info指定另一个，则遵守命令行指定的认证方式。
	二: 使用默认值，并可能提示验证信息

	如果在容器环境中运行kubernetes cluster...
*/
func DefaultClientConfig(flags *pflag.FlagSet) clientcmd.ClientConfig {
	/*
		clientcmd.ClientConfig定义在
		==>/pkg/client/unversioned/clientcmd/client_config.go
			==>type ClientConfig interface

		NewDefaultClientConfigLoadingRules返回一个用默认值填充的ClientConfigLoadingRules对象
			==>/pkg/client/unversioned/clientcmd/loader.go
				==>func NewDefaultClientConfigLoadingRules() *ClientConfigLoadingRules
	*/
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	// use the standard defaults for this client command
	// DEPRECATED: remove and replace with something more accurate
	/*
		设置kubectl中apiserver等集群信息的参数
		DefaultClientConfig 定义在/pkg/client/unversioned/clientcmd/client_config.go
	*/
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig

	/*
		获取cmd中的kubeconfig参数
	*/
	flags.StringVar(&loadingRules.ExplicitPath, "kubeconfig", "", "Path to the kubeconfig file to use for CLI requests.")

	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}

	flagNames := clientcmd.RecommendedConfigOverrideFlags("")
	// short flagnames are disabled by default.  These are here for compatibility with existing scripts
	flagNames.ClusterOverrideFlags.APIServer.ShortName = "s"

	/*
		把标志绑定到其关联变量
			==>/pkg/client/unversioned/clientcmd/overrides.go
				==>func BindOverrideFlags(overrides *ConfigOverrides, flags *pflag.FlagSet, flagNames ConfigOverrideFlags)
		这个绑定和下面的NewInteractiveDeferredLoadingClientConfig是有关系的
	*/
	clientcmd.BindOverrideFlags(overrides, flags, flagNames)
	/*
		使用传递的上下文和后续的授权读取器，创建一个ConfigClientClientConfig
			==>/pkg/client/unversioned/clientcmd/merged_client_builder.go
				==>func NewInteractiveDeferredLoadingClientConfig(loader ClientConfigLoader, overrides *ConfigOverrides, fallbackReader io.Reader)
					==>了解type DeferredLoadingClientConfig struct
		基于默认规则loadingRules, 重写信息overrides和os.Stdin来生成ClientConfig
	*/
	clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, overrides, os.Stdin)

	return clientConfig
}


// DeferredLoadingClientConfig is a ClientConfig interface that is backed by a client config loader.
// It is used in cases where the loading rules may change after you've instantiated them and you want to be sure that
// the most recent rules are used.  This is useful in cases where you bind flags to loading rule parameters before
// the parse happens and you want your calling code to be ignorant of how the values are being mutated to avoid
// passing extraneous information down a call stack
/*
	译：DeferredLoadingClientConfig是由client config loader支持的ClientConfig接口。
		它用于在你实例化加载规则后可能会更改的情况，并且你希望确保使用最新的规则。
		在解析发生之前，将标志绑定到加载规则参数的情况下，这是非常有用的，
		并且你希望调用代码不知道值是如何突变的，以避免将无关信息传递给调用堆栈
*/
type DeferredLoadingClientConfig struct {
	loader         ClientConfigLoader
	overrides      *ConfigOverrides
	fallbackReader io.Reader

	clientConfig ClientConfig
	loadingLock  sync.Mutex

	// provided for testing
	icc InClusterConfig
}
```
### func NewClientCache
基于入参ClientConfig实例化一个type ClientCache struct 
```go
// ClientCache caches previously loaded clients for reuse, and ensures MatchServerVersion
// is invoked only once
/*
	译：type ClientCache struct 缓存先前加载的clients以便重用，并确保MatchServerVersion仅被调用一次
*/
type ClientCache struct {
	loader        clientcmd.ClientConfig
	clientsets    map[unversioned.GroupVersion]*internalclientset.Clientset
	fedClientSets map[unversioned.GroupVersion]fed_clientset.Interface
	configs       map[unversioned.GroupVersion]*restclient.Config

	matchVersion bool

	defaultConfigLock sync.Mutex
	defaultConfig     *restclient.Config
	discoveryClient   discovery.DiscoveryInterface
}
```
至此func NewFactory分析完毕，下面从func RunGet的运行过程开始分析
## f.UnstructuredObject()
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
