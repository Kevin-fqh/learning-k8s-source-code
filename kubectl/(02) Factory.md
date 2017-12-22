# cmdutil.Factory 解析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [func NewFactory](#func-newfactory)
    - [import文件引入的init函数](#import文件引入的init函数)
	- [type Factory interface](#type-factory-interface)
	- [func DefaultClientConfig](#func-defaultclientconfig)
	- [func NewClientCache](#func-newclientcache)
  - [函数UnstructuredObject](#函数unstructuredobject)
	- [discoveryClient是个啥](#discoveryclient是个啥)
	  - [RESTClient](#type-restclient-struct)
	- [四大函数](#四大函数)
  - [结语](#结语)
<!-- END MUNGE: GENERATED_TOC -->

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
NewClientCache 定义在/pkg/kubectl/cmd/util/clientcache.go，
会有类似于apiserver 资源注册的init函数执行
```go
/pkg/client/clientset_generated/internalclientset/import_known_versions.go
```

NewOrDie，创建了一个默认的APIRegistrationManager。
```go
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

### func DefaultClientConfig
总结起来应该是基于默认规则loadingRules, 重写信息overrides和os.Stdin来生成ClientConfig
```go
/*
	译：func DefaultClientConfig 根据下面的规则来生成一个clientcmd.ClientConfig。规则呈现如下层次结构

	一: 使用kubeconfig builder。这里的合并和覆盖次数有点多。
		1、合并kubeconfig本身。 这是通过以下层次结构规则完成的：
			(1)CommandLineLocation - 这是从命令行解析的，so it must be late bound。
			   如果指定了这一点，则不会合并其他kubeconfig文件。 此文件必须存在。
			(2)如果设置了$KUBECONFIG，那么它被视为应该被合并的文件之一。
			(3)主目录位置 HomeDirectoryLocation ,即${HOME}/.kube/config==>/root/.kube/config
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

    type DeferredLoadingClientConfig struct实现了clientcmd.ClientConfig interface
  		==>/pkg/client/unversioned/clientcmd/client_config.go
  			==>type ClientConfig interface
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
type DeferredLoadingClientConfig struct实现了clientcmd.ClientConfig interface

func DefaultClientConfig的返回值是一个clientcmd.ClientConfig interface，其实就是一个type DeferredLoadingClientConfig struct实体。那么clientcmd.ClientConfig到底是什么？提供了什么功能？
```go
// ClientConfig is used to make it easy to get an api server client
/*
	译：type ClientConfig interface 使得获取一个api server client更加easy
*/
type ClientConfig interface {
	// RawConfig returns the merged result of all overrides
	RawConfig() (clientcmdapi.Config, error)
	// ClientConfig returns a complete client config
	/*
		返回一个完整的client config
	*/
	ClientConfig() (*restclient.Config, error)
	// Namespace returns the namespace resulting from the merged
	// result of all overrides and a boolean indicating if it was
	// overridden
	Namespace() (string, bool, error)
	// ConfigAccess returns the rules for loading/persisting the config.
	ConfigAccess() ConfigAccess
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

## 函数UnstructuredObject
RunGet函数中调用了f.UnstructuredObject()，
定义在/pkg/kubectl/cmd/util/factory.go
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
	/*
		/pkg/client/typed/discovery/restmapper.go
			==>func GetAPIGroupResources(cl DiscoveryInterface) ([]*APIGroupResources, error)
		GetAPIGroupResources使用入参discoveryClient，收集discovery information并填充一个APIGroupResources切片。
	*/
	groupResources, err := discovery.GetAPIGroupResources(discoveryClient)
	/*
		可以认为只要cache目录下有文件存在，fresh＝false
		如果把cache目录/root/.kube/cache/discovery/localhost_8080 删除，fresh＝true
	*/
	if err != nil && !discoveryClient.Fresh() {
		/*
			在err!=nil && fresh＝false的情况下，重新获取groupResources
			也就是说只要fresh＝true的时候，都可以认为该groupResources是可以直接使用的
			Invalidate()把client的属性fresh和invalidated都置为true
		*/
		discoveryClient.Invalidate()
		groupResources, err = discovery.GetAPIGroupResources(discoveryClient)
	}
	if err != nil {
		return nil, nil, err
	}

	/*
		/pkg/client/typed/discovery/restmapper.go
			==>func NewDeferredDiscoveryRESTMapper
		利用discoveryClient获取discovery information，用于执行REST映射，返回一个DeferredDiscoveryRESTMapper
	*/
	mapper := discovery.NewDeferredDiscoveryRESTMapper(discoveryClient, meta.InterfacesForUnstructured)
	/*
		/pkg/client/typed/discovery/unstructured.go
			==>func NewUnstructuredObjectTyper(groupResources []*APIGroupResources) *UnstructuredObjectTyper
		返回一个runtime.ObjectTyper（UnstructuredObjectTyper），即一个反映discovery information的unstructred objects
		可以简单地认为func NewUnstructuredObjectTyper是把入参GVR转化为一个UnstructuredObjectTyper类型
	*/
	typer := discovery.NewUnstructuredObjectTyper(groupResources)
	/*
		把userResources、mapper、discoveryClient封装成一个ShortcutExpander结构，就是一个简单的封装

		userResources定义在/pkg/kubectl/cmd/util/shortcut_restmapper.go
			==>userResources是主要的资源名称，用户client tools使用的资源。
	*/
	return NewShortcutExpander(mapper, discoveryClient), typer, nil
}
```
需要了解的关键函数和概念：
- discoveryClient, err := f.DiscoveryClient()，discoveryClient是个啥？都有什么功能？
	- DiscoveryClient包含的restClient作用的什么？
- package discovery 的三大函数
  - func GetAPIGroupResources
  - func GetAPIGroupResources
  - func NewUnstructuredObjectTyper
- NewShortcutExpander

### discoveryClient是个啥
```go
func (f *factory) DiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	/*
		获取完整的cfg
		f.clientConfig的真正实现定义在
		==>/pkg/client/unversioned/clientcmd/merged_client_builder.go
			==>type DeferredLoadingClientConfig struct
	*/
	cfg, err := f.clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	/*
		/pkg/client/typed/discovery/discovery_client.go
			==>func NewDiscoveryClientForConfig(c *restclient.Config) (*DiscoveryClient, error)
		基于指定的配置创建一个新的DiscoveryClient。
		该DiscoveryClient可用于发现API server中支持的resources。
	*/
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
  /*
		cacheDir 的值:  /root/.kube/cache/discovery/localhost_8080
					   /root/.kube/cache/discovery/kubernetes_6443
		kubectl 用域名localhost kubernetes去和apiserver链接，后面是apiserver的端口
	*/
	cacheDir := computeDiscoverCacheDir(filepath.Join(homedir.HomeDir(), ".kube", "cache", "discovery"), cfg.Host)
	return NewCachedDiscoveryClient(discoveryClient, cacheDir, time.Duration(10*time.Minute)), nil
}
```
首先来了解一下`func (f *factory) DiscoveryClient() (discovery.CachedDiscoveryInterface, error)`的返回值discovery.CachedDiscoveryInterface，定义在/pkg/client/typed/discovery/discovery_client.go,这是接口定义。
从type DiscoveryInterface interface的定义可以发现，里面有个RESTClient() restclient.Interface，后面要继续研究。
```go
// DiscoveryInterface holds the methods that discover server-supported API groups,
// versions and resources.
/*
	译：DiscoveryInterface的接口实现了：发现server端支持的API groups，versions and resources
*/
type DiscoveryInterface interface {
	/*
		RESTClient()返回一个restclient.Interface，用于与API服务器进行通信。
	*/
	RESTClient() restclient.Interface
	ServerGroupsInterface    //获取 API server支持的gropus
	ServerResourcesInterface //获取 API server支持的resource
	ServerVersionInterface   //获取 API server支持的version
	SwaggerSchemaInterface   //接收和解析API server支持的swagger API
}

// CachedDiscoveryInterface is a DiscoveryInterface with cache invalidation and freshness.
/*
	译：CachedDiscoveryInterface是一个DiscoveryInterface，且具有Fresh()和Invalidate()方法
*/
type CachedDiscoveryInterface interface {
	DiscoveryInterface
	// Fresh returns true if no cached data was used that had been retrieved before the instantiation.
	/*
		如果在实例化之前没有使用已经检索到的缓存数据，则Fresh将返回true。
	*/
	Fresh() bool
	// Invalidate enforces that no cached data is used in the future that is older than the current time.
	/*
		Invalidate()会把client的属性fresh和invalidated都置为true
	*/	
	Invalidate()
}
```

现在让我们回到func (f *factory) DiscoveryClient()的实现过程。
type DeferredLoadingClientConfig struct 在前面已经介绍过。
下面来了解`discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)`,
这里的type DiscoveryClient struct实现了上面所说的type DiscoveryInterface interface
```go
// NewDiscoveryClientForConfig creates a new DiscoveryClient for the given config. This client
// can be used to discover supported resources in the API server.
/*
	译：NewDiscoveryClientForConfig基于指定的配置创建一个新的DiscoveryClient。
		该DiscoveryClient可用于发现API server中支持的resources。
*/
func NewDiscoveryClientForConfig(c *restclient.Config) (*DiscoveryClient, error) {
	config := *c
	//设置默认参数
	if err := setDiscoveryDefaults(&config); err != nil {
		return nil, err
	}
	/*
		/pkg/client/restclient/config.go
			==>func UnversionedRESTClientFor(config *Config) (*RESTClient, error)
		返回一个满足client Config要求的RESTClient。
		由此方法创建的RESTClient是通用的 - 它希望可以按照Kubernetes约定的API进行操作，但可能不是Kubernetes API。
	*/
	client, err := restclient.UnversionedRESTClientFor(&config)
	return &DiscoveryClient{restClient: client, LegacyPrefix: "/api"}, err
}

// DiscoveryClient implements the functions that discover server-supported API groups,
// versions and resources.
/*
	译：type DiscoveryClient struct实现了发现server-supported API groups, versions and resources 的方法
*/
type DiscoveryClient struct {
	restClient restclient.Interface

	LegacyPrefix string
}
```
最后基于生成的discoveryClient调用定义在/pkg/kubectl/cmd/util/cached_discovery.go的`func NewCachedDiscoveryClient`
```go
// NewCachedDiscoveryClient creates a new DiscoveryClient.  cacheDirectory is the directory where discovery docs are held.  It must be unique per host:port combination to work well.
/*
	译：NewCachedDiscoveryClient创建一个新的DiscoveryClient。
		属性cacheDirectory是discovery docs存储的目录。
  		它必须是唯一的host:port组合
*/
func NewCachedDiscoveryClient(delegate discovery.DiscoveryInterface, cacheDirectory string, ttl time.Duration) *CachedDiscoveryClient {
	return &CachedDiscoveryClient{
		delegate:       delegate,
		cacheDirectory: cacheDirectory,
		ttl:            ttl,
		ourFiles:       map[string]struct{}{},
		fresh:          true,
	}
}

// CachedDiscoveryClient implements the functions that discovery server-supported API groups,
// versions and resources.
/*
	译：CachedDiscoveryClient的功能是：发现server端支持的API groups，versions and resources
*/
type CachedDiscoveryClient struct {
	/*
		/pkg/client/typed/discovery/discovery_client.go
			==>type DiscoveryInterface interface
	*/
	delegate discovery.DiscoveryInterface

	// cacheDirectory is the directory where discovery docs are held.  It must be unique per host:port combination to work well.
	/*
		译：cacheDirectory是discovery docs存储的目录。它必须是唯一的host:port组合。
	*/
	cacheDirectory string

	// ttl is how long the cache should be considered valid
	/*
		译：ttl是cache的有效时间
	*/
	ttl time.Duration

	// mutex protects the variables below
	/*
		互斥保护
	*/
	mutex sync.Mutex

	// ourFiles are all filenames of cache files created by this process
	/*
		译：ourFiles都是由此进程创建的缓存文件的文件名
	*/
	ourFiles map[string]struct{}
	// invalidated is true if all cache files should be ignored that are not ours (e.g. after Invalidate() was called)
	/*
		invalidated=true，如果所有缓存文件都应该被忽略
	*/
	invalidated bool
	// fresh is true if all used cache files were ours
	/*
		fresh=true，如果所使用的cache files是可以使用的，不需要更新
	*/
	fresh bool
}
```

#### type RESTClient struct
DiscoveryClient里面包含了一个restClient，那么这个restClient的作用的什么？
```go
// RESTClient imposes common Kubernetes API conventions on a set of resource paths.
// The baseURL is expected to point to an HTTP or HTTPS path that is the parent
// of one or more resources.  The server should return a decodable API resource
// object, or an api.Status object which contains information about the reason for
// any failure.
//
// Most consumers should use client.New() to get a Kubernetes API client.
/*
	译：type RESTClient struct 在一组resource paths上强加了常见的Kubernetes API约定。
		baseURL指向作为一个或多个resources的父级resources的HTTP（HTTPS）路径。
		server端应该返回一个可解码的API资源对象，或一个api.Status对象，其中包含有关任何故障原因的信息。
*/
type RESTClient struct {
	// base is the root URL for all invocations of the client
	base *url.URL
	// versionedAPIPath is a path segment connecting the base URL to the resource root
	versionedAPIPath string

	// contentConfig is the information used to communicate with the server.
	contentConfig ContentConfig

	// serializers contain all serializers for underlying content type.
	serializers Serializers

	// creates BackoffManager that is passed to requests.
	createBackoffMgr func() BackoffManager

	// TODO extract this into a wrapper interface via the RESTClient interface in kubectl.
	Throttle flowcontrol.RateLimiter

	// Set specific behavior of the client.  If not set http.DefaultClient will be used.
	Client *http.Client
}
```

至此，func (f *factory) DiscoveryClient() (discovery.CachedDiscoveryInterface, error)基本清楚。
DiscoveryClient通过type RESTClient struct发现server端支持的API groups，versions and resources。

下面，回到`func (f *factory) UnstructuredObject()`，准备了解定义在/pkg/client/typed/discovery的三大函数func GetAPIGroupResources、func GetAPIGroupResources、func NewUnstructuredObjectTyper

### 四大函数
#### func GetAPIGroupResources
```go
// GetAPIGroupResources uses the provided discovery client to gather
// discovery information and populate a slice of APIGroupResources.
/*
	译：GetAPIGroupResources使用入参discoveryClient，收集discovery information并填充一个APIGroupResources切片。
*/
func GetAPIGroupResources(cl DiscoveryInterface) ([]*APIGroupResources, error) {
	apiGroups, err := cl.ServerGroups()
	if err != nil {
		return nil, err
	}
	var result []*APIGroupResources
	for _, group := range apiGroups.Groups {
		groupResources := &APIGroupResources{
			Group:              group,
			VersionedResources: make(map[string][]unversioned.APIResource),
		}
		for _, version := range group.Versions {
			resources, err := cl.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if errors.IsNotFound(err) {
					continue // ignore as this can race with deletion of 3rd party APIs
				}
				return nil, err
			}
			groupResources.VersionedResources[version.Version] = resources.APIResources
		}
		result = append(result, groupResources)
	}
	return result, nil
}
```

#### func NewDeferredDiscoveryRESTMapper
利用discoveryClient获取discovery information，用于执行REST映射，返回一个DeferredDiscoveryRESTMapper
```go
// NewDeferredDiscoveryRESTMapper returns a
// DeferredDiscoveryRESTMapper that will lazily query the provided
// client for discovery information to do REST mappings.
/*
	译：func NewDeferredDiscoveryRESTMapper返回一个DeferredDiscoveryRESTMapper，
		它将懒惰地查询指定的client（也就是discoveryClient）获取discovery information，用于执行REST映射。
*/
func NewDeferredDiscoveryRESTMapper(cl CachedDiscoveryInterface, versionInterface meta.VersionInterfacesFunc) *DeferredDiscoveryRESTMapper {
	return &DeferredDiscoveryRESTMapper{
		cl:               cl,
		versionInterface: versionInterface,
	}
}

// DeferredDiscoveryRESTMapper is a RESTMapper that will defer
// initialization of the RESTMapper until the first mapping is
// requested.
/*
	译：DeferredDiscoveryRESTMapper是一个RESTMapper，将延迟RESTMapper的初始化，直到请求第一个mapping。
*/
type DeferredDiscoveryRESTMapper struct {
	initMu           sync.Mutex
	delegate         meta.RESTMapper
	cl               CachedDiscoveryInterface
	versionInterface meta.VersionInterfacesFunc
}
```

#### func NewUnstructuredObjectTyper
可以简单地认为func NewUnstructuredObjectTyper是把入参GVR转化为一个UnstructuredObjectTyper类型
```go
// NewUnstructuredObjectTyper returns a runtime.ObjectTyper for
// unstructred objects based on discovery information.
/*
	func NewUnstructuredObjectTyper 返回一个runtime.ObjectTyper（UnstructuredObjectTyper），
	一个反映discovery information的unstructred objects
*/
func NewUnstructuredObjectTyper(groupResources []*APIGroupResources) *UnstructuredObjectTyper {
	dot := &UnstructuredObjectTyper{registered: make(map[unversioned.GroupVersionKind]bool)}
	for _, group := range groupResources {
		for _, discoveryVersion := range group.Group.Versions {
			resources, ok := group.VersionedResources[discoveryVersion.Version]
			if !ok {
				continue
			}

			gv := unversioned.GroupVersion{Group: group.Group.Name, Version: discoveryVersion.Version}
			for _, resource := range resources {
				dot.registered[gv.WithKind(resource.Kind)] = true
			}
		}
	}
	return dot
}
```
看看func NewUnstructuredObjectTyper入参和返回值定义
```go
// UnstructuredObjectTyper provides a runtime.ObjectTyper implmentation for
// runtime.Unstructured object based on discovery information.
/*
	译：UnstructuredObjectTyper基于discovery information，
		为runtime.Unstructured对象提供了一个runtime.ObjectTyper实现。
*/
type UnstructuredObjectTyper struct {
	registered map[unversioned.GroupVersionKind]bool
}

// APIGroupResources is an API group with a mapping of versions to
// resources.
/*
	type APIGroupResources struct是一个API group，具有版本到资源的映射。
*/
type APIGroupResources struct {
	Group unversioned.APIGroup
	// A mapping of version string to a slice of APIResources for
	// that version.
	/*
		一个映射关系：
			version string ==>该version的APIResources
	*/
	VersionedResources map[string][]unversioned.APIResource
}
```

#### func NewShortcutExpander
type ShortcutExpander struct是可以用于Kubernetes资源的RESTMapper。
把userResources、mapper、discoveryClient封装成一个ShortcutExpander结构，可以理解为就是一个简单的封装
```go
func NewShortcutExpander(delegate meta.RESTMapper, client discovery.DiscoveryInterface) ShortcutExpander {
	return ShortcutExpander{All: userResources, RESTMapper: delegate, discoveryClient: client}
}

// userResources are the resource names that apply to the primary, user facing resources used by
// client tools. They are in deletion-first order - dependent resources should be last.
/*
	译：userResources是主要的资源名称，
		用户client tools使用的资源。
		他们是删除第一顺序，依赖资源应该是最后的。
*/
var userResources = []unversioned.GroupResource{
	{Group: "", Resource: "pods"},
	{Group: "", Resource: "replicationcontrollers"},
	{Group: "", Resource: "services"},
	{Group: "apps", Resource: "statefulsets"},
	{Group: "autoscaling", Resource: "horizontalpodautoscalers"},
	{Group: "extensions", Resource: "jobs"},
	{Group: "extensions", Resource: "deployments"},
	{Group: "extensions", Resource: "replicasets"},
}
```

# 结语
至此，func (f *factory) UnstructuredObject()函数的介绍已经基本完成