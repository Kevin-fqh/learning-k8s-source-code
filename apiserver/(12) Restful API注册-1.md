# Apiserver Restful API注册-1

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type Master struct](#type-master-struct)
    - [New一个Master实例](#new一个master实例)
	- [InstallLegacyAPI](#installlegacyapi)
	- [InstallAPIs](#installapis)
  - [type GenericAPIServer struct](#type-genericapiserver-struct)
    - [New一个GenericAPIServer](#new一个genericapiserver)
	- [installAPI](#installapi)
	- [DynamicApisDiscovery](#dynamicapisdiscovery)
	  - [func NewApisWebService](#func-newapiswebservice)
  - [New了一个APIContainer](#new了一个apicontainer)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

Apiserver应用的是[package go-restful](https://godoc.org/github.com/emicklei/go-restful)来发布服务，应该先行进行了解。

本文主要介绍`/apis`路径下GET("/")的生成过程。

## type Master struct
```go
// Master contains state for a Kubernetes cluster master/api server.
type Master struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}
```
查看其相关的功能函数

### New一个Master实例
func (c completedConfig) New() 基于给定的配置生成一个新的Master实例。如果未设置，某些配置字段将被设置为默认值。某些字段是必须指定的，比如：KubeletClientConfig

分析func (c completedConfig) New()函数的流程，如下所示:
1. 调用`func (c completedConfig) New() (*GenericAPIServer, error)`，创建一个type GenericAPIServer struct实例
2. 判断是否enable了用于Watch的Cache，和etcd建立连接
3. 调用`InstallLegacyAPI`进行"/api"的API安装
4. 调用`InstallAPIs`进行"/apis"的API安装，如果其处于enabled状态

```go
// New returns a new instance of Master from the given config.
// Certain config fields will be set to a default value if unset.
// Certain config fields must be specified, including:
//   KubeletClientConfig

func (c completedConfig) New() (*Master, error) {
	if reflect.DeepEqual(c.KubeletClientConfig, kubeletclient.KubeletClientConfig{}) {
		return nil, fmt.Errorf("Master.New() called with empty config.KubeletClientConfig")
	}

	/*
		************很重要，apiserver对restful api的container进行初始化*******************
		***************************需要深入了解******************************************
		*******************************************************************************
		返回值s中包涵了s.HandlerContainer，
		也就是说s.HandlerContainer在这里完成了初始化
		定义在/pkg/genericapiserver/config.go
			==>func (c completedConfig) New() (*GenericAPIServer, error)

		其实里面还完成WebService的创建，该WebService是用于list 一个group下的所有versions，因为只注册了简单的路由规则。
		同时把WebService注入到了s.HandlerContainer中。
		真正核心的注册都会在pkg/apiserver/apiserver.go中的func (g *APIGroupVersion) InstallREST 中进行。

		/api的注册接口是InstallLegacyAPIGroup()接口
		/apis的注册接口是InstallAPIGroup()。
		这两个接口后面都会调用s.installAPIResources()，最后再调用apiGroupVersion.InstallREST(s.HandlerContainer.Container)进行API注册。
	*/
	s, err := c.Config.GenericConfig.SkipComplete().New() // completion is done in Complete, no need for a second time
	if err != nil {
		return nil, err
	}

	if c.EnableUISupport {
		routes.UIRedirect{}.Install(s.HandlerContainer)
	}
	if c.EnableLogsSupport {
		routes.Logs{}.Install(s.HandlerContainer)
	}

	m := &Master{
		GenericAPIServer: s,
	}

	/*
		该接口初始化了一个restOptionsFactory变量，
		里面指定了最大的删除回收资源的协程数，是否使能GC和storageFactory
	*/
	restOptionsFactory := restOptionsFactory{
		deleteCollectionWorkers: c.DeleteCollectionWorkers,
		enableGarbageCollection: c.GenericConfig.EnableGarbageCollection,
		storageFactory:          c.StorageFactory,
	}

	/*
		判断是否enable了用于Watch的Cache。有无cache，赋值的是不同的接口实现。
		restOptionsFactory.storageDecorator：是一个各个资源的REST interface(CRUD)装饰者，
		后面调用NewStorage()时会用到该接口，并输出对应的CRUD接口及销毁接口。
		可以参考pkg/registry/core/pod/etcd/etcd.go中的NewStorage()
		其实这里有无cache的接口差异就在于：
			有cache的话，就提供操作cache的接口；
			无cache的话，就提供直接操作etcd的接口

		根据是否enable了WatchCache来完成NewStorage()接口中调用的装饰器接口的赋值。

		registry.StorageWithCacher：该接口是返回了操作cache的接口，和清除cache的操作接口。
		generic.UndecoratedStorage: 该接口会根据你配置的后端类型(etcd2/etcd3等)，来返回不同的etcd操作接口，
		其实是为所有的资源对象创建了etcd的链接，然后通过该链接发送不同的命令，最后还返回了断开该链接的接口。

		所以两者的实现完全不一样，一个操作cache，一个操作实际的etcd。

		在这里完成给storageDecorator赋值了！！！！！！
		*******需要深入了解两个storageDecorator类型*********
	*/
	if c.EnableWatchCache {
		/*
			函数StorageWithCacher定义在pkg/registry/generic/registry/storage_factory.go
				==>func StorageWithCacher
		*/
		restOptionsFactory.storageDecorator = registry.StorageWithCacher
	} else {
		/*
			函数UndecoratedStorage定义在pkg/registry/generic/storage_decorator.go
				==>func UndecoratedStorage
		*/
		restOptionsFactory.storageDecorator = generic.UndecoratedStorage
	}

	// install legacy rest storage
	/*
		判断/api/v1的group是否已经注册并enable，是的话再进行install
	*/
	if c.GenericConfig.APIResourceConfigSource.AnyResourcesForVersionEnabled(apiv1.SchemeGroupVersion) {
		/*
			legacyRESTStorageProvider对象主要提供了一个NewLegacyRESTStorage()的接口
				==>/pkg/registry/core/rest/storage_core.go
		*/
		legacyRESTStorageProvider := corerest.LegacyRESTStorageProvider{
			StorageFactory:       c.StorageFactory,
			ProxyTransport:       c.ProxyTransport,
			KubeletClientConfig:  c.KubeletClientConfig,
			EventTTL:             c.EventTTL,
			ServiceIPRange:       c.ServiceIPRange,
			ServiceNodePortRange: c.ServiceNodePortRange,
			LoopbackClientConfig: c.GenericConfig.LoopbackClientConfig,
		}
		/*
			进行"/api"的API安装

			调用func (m *Master) InstallLegacyAPI
			apiServer之资源注册-V1.0(偏重于讲解几个重要的数据结构是如何初始化的)
			***********
			API资源注册为restful api-V1.0

			m.InstallLegacyAPI 和 m.InstallAPIs这两个函数分别用于注册"/api"和"/apis"的API
		*/
		/*
			通过restOptionsFactory.NewFor的调用来生成一个opts
		*/
		m.InstallLegacyAPI(c.Config, restOptionsFactory.NewFor, legacyRESTStorageProvider)
	}

	restStorageProviders := []genericapiserver.RESTStorageProvider{
		appsrest.RESTStorageProvider{},
		authenticationrest.RESTStorageProvider{Authenticator: c.GenericConfig.Authenticator},
		authorizationrest.RESTStorageProvider{Authorizer: c.GenericConfig.Authorizer},
		autoscalingrest.RESTStorageProvider{},
		batchrest.RESTStorageProvider{},
		certificatesrest.RESTStorageProvider{},
		extensionsrest.RESTStorageProvider{ResourceInterface: thirdparty.NewThirdPartyResourceServer(s, c.StorageFactory)},
		policyrest.RESTStorageProvider{},
		rbacrest.RESTStorageProvider{AuthorizerRBACSuperUser: c.GenericConfig.AuthorizerRBACSuperUser},
		storagerest.RESTStorageProvider{},
	}
	/*
		进行"/apis"的API安装
		调用func (m *Master) InstallAPIs
		apiServer之资源注册-V1.0(偏重于讲解几个重要的数据结构是如何初始化的)
		***********
		API资源注册为restful api-V1.0

		m.InstallLegacyAPI 和 m.InstallAPIs这两个函数分别用于注册"/api"和"/apis"的API
	*/
	m.InstallAPIs(c.Config.GenericConfig.APIResourceConfigSource, restOptionsFactory.NewFor, restStorageProviders...)

	if c.Tunneler != nil {
		m.installTunneler(c.Tunneler, coreclient.NewForConfigOrDie(c.GenericConfig.LoopbackClientConfig).Nodes())
	}

	glog.Infof("生成一个master")
	return m, nil
}
```

### InstallLegacyAPI
进行"/api"的API安装
```go
func (m *Master) InstallLegacyAPI(c *Config, restOptionsGetter genericapiserver.RESTOptionsGetter, legacyRESTStorageProvider corerest.LegacyRESTStorageProvider) {
	/*
		legacyRESTStorageProvider这个对象，比较关键，需要深入查看
		返回了RESTStorage和apiGroupInfo，都是重量级的成员
		这些初始化也就在NewLegacyRESTStorage这个接口中
		==>定义在pkg/registry/core/rest/storage_core.go
			==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
	*/
	glog.Infof("生成apiGroupInfo, apiGroupInfo携带着restStorageMap")
	legacyRESTStorage, apiGroupInfo, err := legacyRESTStorageProvider.NewLegacyRESTStorage(restOptionsGetter)
	if err != nil {
		glog.Fatalf("Error building core storage: %v", err)
	}

	/*
		判断是否enable了controller,默认是true
		启动apiserver的时候，会有一个引导controller，就是这里

	*/
	if c.EnableCoreControllers {
		serviceClient := coreclient.NewForConfigOrDie(c.GenericConfig.LoopbackClientConfig)
		/*
			调用func (c *Config) NewBootstrapController
			etcd数据库里面的/registry/namespaces/kube-system就在这里定义
		*/
		bootstrapController := c.NewBootstrapController(legacyRESTStorage, serviceClient)
		if err := m.GenericAPIServer.AddPostStartHook("bootstrap-controller", bootstrapController.PostStartHook); err != nil {
			glog.Fatalf("Error registering PostStartHook %q: %v", "bootstrap-controller", err)
		}
	}

	// install core Group's API
	/*
		调用InstallLegacyAPIGroup，定义在
		==>/pkg/genericapiserver/genericapiserver.go
			==>func (s *GenericAPIServer) InstallLegacyAPIGroup(apiPrefix string, apiGroupInfo *APIGroupInfo)
		apiServer之资源注册-V2.0
	*/
	if err := m.GenericAPIServer.InstallLegacyAPIGroup(genericapiserver.DefaultLegacyAPIPrefix, &apiGroupInfo); err != nil {
		glog.Fatalf("Error in registering group versions: %v", err)
	}
}
```

### InstallAPIs
进行"/apis"的API安装，如果其处于enabled状态
```go
// InstallAPIs will install the APIs for the restStorageProviders if they are enabled.
func (m *Master) InstallAPIs(apiResourceConfigSource genericapiserver.APIResourceConfigSource, restOptionsGetter genericapiserver.RESTOptionsGetter, restStorageProviders ...genericapiserver.RESTStorageProvider) {
	apiGroupsInfo := []genericapiserver.APIGroupInfo{}

	for _, restStorageBuilder := range restStorageProviders {
		groupName := restStorageBuilder.GroupName()
		if !apiResourceConfigSource.AnyResourcesForGroupEnabled(groupName) {
			glog.V(1).Infof("Skipping disabled API group %q.", groupName)
			continue
		}
		apiGroupInfo, enabled := restStorageBuilder.NewRESTStorage(apiResourceConfigSource, restOptionsGetter)
		if !enabled {
			glog.Warningf("Problem initializing API group %q, skipping.", groupName)
			continue
		}
		glog.V(1).Infof("Enabling API group %q.", groupName)

		if postHookProvider, ok := restStorageBuilder.(genericapiserver.PostStartHookProvider); ok {
			name, hook, err := postHookProvider.PostStartHook()
			if err != nil {
				glog.Fatalf("Error building PostStartHook: %v", err)
			}
			if err := m.GenericAPIServer.AddPostStartHook(name, hook); err != nil {
				glog.Fatalf("Error registering PostStartHook %q: %v", name, err)
			}
		}

		apiGroupsInfo = append(apiGroupsInfo, apiGroupInfo)
	}

	for i := range apiGroupsInfo {
		/*
			调用func (s *GenericAPIServer) InstallAPIGroup(apiGroupInfo *APIGroupInfo)
			==>定义在/pkg/genericapiserver/genericapiserver.go
			apiServer之资源注册-V2.0
		*/
		if err := m.GenericAPIServer.InstallAPIGroup(&apiGroupsInfo[i]); err != nil {
			glog.Fatalf("Error in registering group versions: %v", err)
		}
	}
}
```

## type GenericAPIServer struct
GenericAPIServer记录着Kubernetes集群master服务器的状态
```go
// GenericAPIServer contains state for a Kubernetes cluster api server.
type GenericAPIServer struct {
	// discoveryAddresses is used to build cluster IPs for discovery.
	discoveryAddresses DiscoveryAddresses

	// LoopbackClientConfig is a config for a privileged loopback connection to the API server
	LoopbackClientConfig *restclient.Config

	// minRequestTimeout is how short the request timeout can be.  This is used to build the RESTHandler
	minRequestTimeout time.Duration

	// enableSwaggerSupport indicates that swagger should be served.  This is currently separate because
	// the API group routes are created *after* initialization and you can't generate the swagger routes until
	// after those are available.
	// TODO eventually we should be able to factor this out to take place during initialization.
	enableSwaggerSupport bool

	// legacyAPIGroupPrefixes is used to set up URL parsing for authorization and for validating requests
	// to InstallLegacyAPIGroup
	/*
		legacyAPIGroupPrefixes用于设置URL解析，以进行授权和验证对InstallLegacyAPIGroup的请求
	*/
	legacyAPIGroupPrefixes sets.String

	// admissionControl is used to build the RESTStorage that backs an API Group.
	admissionControl admission.Interface

	// requestContextMapper provides a way to get the context for a request.  It may be nil.
	requestContextMapper api.RequestContextMapper

	// The registered APIs
	/*
		这个*genericmux.APIContainer是 go-restful框架中的container的封装，用来装载webservice之用
		定义在pkg/genericapiserver/mux/container.go
	*/
	HandlerContainer *genericmux.APIContainer

	SecureServingInfo   *SecureServingInfo
	InsecureServingInfo *ServingInfo

	// numerical ports, set after listening
	effectiveSecurePort, effectiveInsecurePort int

	// ExternalAddress is the address (hostname or IP and port) that should be used in
	// external (public internet) URLs for this GenericAPIServer.
	ExternalAddress string

	// storage contains the RESTful endpoints exposed by this GenericAPIServer
	storage map[string]rest.Storage

	// Serializer controls how common API objects not in a group/version prefix are serialized for this server.
	// Individual APIGroups may define their own serializers.
	Serializer runtime.NegotiatedSerializer

	// "Outputs"
	Handler         http.Handler
	InsecureHandler http.Handler

	// Map storing information about all groups to be exposed in discovery response.
	// The map is from name to the group.
	apiGroupsForDiscoveryLock sync.RWMutex
	apiGroupsForDiscovery     map[string]unversioned.APIGroup

	// See Config.$name for documentation of these flags

	enableOpenAPISupport bool
	openAPIConfig        *common.Config

	// PostStartHooks are each called after the server has started listening, in a separate go func for each
	// with no guaranteee of ordering between them.  The map key is a name used for error reporting.
	// It may kill the process with a panic if it wishes to by returning an error
	postStartHookLock    sync.Mutex
	postStartHooks       map[string]postStartHookEntry
	postStartHooksCalled bool

	// healthz checks
	healthzLock    sync.Mutex
	healthzChecks  []healthz.HealthzChecker
	healthzCreated bool
}
```

### New一个GenericAPIServer
```go
// New returns a new instance of GenericAPIServer from the given config.
// Certain config fields will be set to a default value if unset,
// including:
//   ServiceClusterIPRange
//   ServiceNodePortRange
//   MasterCount
//   ReadWritePort
//   PublicAddress
// Public fields:
//   Handler -- The returned GenericAPIServer has a field TopHandler which is an
//   http.Handler which handles all the endpoints provided by the GenericAPIServer,
//   including the API, the UI, and miscellaneous debugging endpoints.  All
//   these are subject to authorization and authentication.
//   InsecureHandler -- an http.Handler which handles all the same
//   endpoints as Handler, but no authorization and authentication is done.
// Public methods:
//   HandleWithAuth -- Allows caller to add an http.Handler for an endpoint
//   that uses the same authentication and authorization (if any is configured)
//   as the GenericAPIServer's built-in endpoints.
//   If the caller wants to add additional endpoints not using the GenericAPIServer's
//   auth, then the caller should create a handler for those endpoints, which delegates the
//   any unhandled paths to "Handler".
/*
	译：New()会根据config生成一个GenericAPIServer实例。
	   如果config中的属性没有设定，就使用默认值，
	   包括：ServiceClusterIPRange，ServiceNodePortRange，MasterCount，ReadWritePort，PublicAddress
	Public fields:
		Handler -- 返回的GenericAPIServer有一个字段TopHandler，它是一个http.Handler，
			它处理GenericAPIServer提供的所有endpoints，包括API，UI和其他调试endpoints。
			所有这些都需要授权和认证。

	    InsecureHandler -- 一个http.Handler，它处理与Handler相同的所有端点，但不进行授权和认证。

	Public methods:
		HandleWithAuth -- 允许caller对一个endpoint增加一个http.Handler，
						该Handler拥有和GenericAPIServer内置endpoints相同的身份验证和授权（如果已配置）。
  		如果调用者想要增加不需要认证和授权的additional endpoints，那么调用者应为这些endpoints创建一个handler，
		该handler将任何未处理的路径委托给Public fields中的“Handler”。
*/
func (c completedConfig) New() (*GenericAPIServer, error) {
	if c.Serializer == nil {
		return nil, fmt.Errorf("Genericapiserver.New() called with config.Serializer == nil")
	}

	s := &GenericAPIServer{
		discoveryAddresses:     c.DiscoveryAddresses,
		LoopbackClientConfig:   c.LoopbackClientConfig,
		/*
			c.LegacyAPIGroupPrefixes值是/api，取值于
				==>/pkg/genericapiserver/config.go
					==>DefaultLegacyAPIPrefix = "/api"
		*/
		legacyAPIGroupPrefixes: c.LegacyAPIGroupPrefixes,
		admissionControl:       c.AdmissionControl,
		requestContextMapper:   c.RequestContextMapper,
		Serializer:             c.Serializer,

		minRequestTimeout:    time.Duration(c.MinRequestTimeout) * time.Second,
		enableSwaggerSupport: c.EnableSwaggerSupport,

		SecureServingInfo:   c.SecureServingInfo,
		InsecureServingInfo: c.InsecureServingInfo,
		ExternalAddress:     c.ExternalAddress,

		apiGroupsForDiscovery: map[string]unversioned.APIGroup{},

		enableOpenAPISupport: c.EnableOpenAPISupport,
		openAPIConfig:        c.OpenAPIConfig,

		postStartHooks: map[string]postStartHookEntry{},
	}

	/*
		这里进行了HandlerContainer的初始化
		NewAPIContainer定义在/pkg/genericapiserver/mux/container.go
			==>func NewAPIContainer(mux *http.ServeMux, s runtime.NegotiatedSerializer) *APIContainer

		传进去的两个参数：
				http.NewServeMux()新建了一个http的ServeMux;
				c.Serializer则是实现了编解码序列化反序列化的对象
	*/
	s.HandlerContainer = mux.NewAPIContainer(http.NewServeMux(), c.Serializer)
	/*
		上面Container已创建并且也进行了初始化。该轮到WebService了
	*/
	s.installAPI(c.Config)

	/*
		BuildHandlerChainsFunc封装了一些对Request进行认证、授权、审计等相关的handler
	*/
	s.Handler, s.InsecureHandler = c.BuildHandlerChainsFunc(s.HandlerContainer.ServeMux, c.Config)

	return s, nil
}
```
这里需要注意的有：
1. type GenericAPIServer struct
2. `mux.NewAPIContainer(http.NewServeMux(), c.Serializer)`创建了一个APIContainer
3. `s.installAPI(c.Config)`添加了WebService

先来介绍WebService注册函数`func (s *GenericAPIServer) installAPI`，再介绍APIContainer的创建。
### installAPI
```go
func (s *GenericAPIServer) installAPI(c *Config) {
	if c.EnableIndex {
		routes.Index{}.Install(s.HandlerContainer)
	}
	if c.EnableSwaggerSupport && c.EnableSwaggerUI {
		routes.SwaggerUI{}.Install(s.HandlerContainer)
	}
	if c.EnableProfiling {
		routes.Profiling{}.Install(s.HandlerContainer)
		if c.EnableContentionProfiling {
			goruntime.SetBlockProfileRate(1)
		}
	}
	if c.EnableMetrics {
		if c.EnableProfiling {
			routes.MetricsWithReset{}.Install(s.HandlerContainer)
		} else {
			routes.DefaultMetrics{}.Install(s.HandlerContainer)
		}
	}
	routes.Version{Version: c.Version}.Install(s.HandlerContainer)
	/*
		往HandlerContainer中的Container里添加WebService（参考go-restfule框架中的流程），
		WebService的创建在s.DynamicApisDiscovery()中进行，
		实际上创建的WebService是用于list 该group下的所有versions。
	*/
	s.HandlerContainer.Add(s.DynamicApisDiscovery())
}
```
这是通过`func (s *GenericAPIServer) DynamicApisDiscovery()`生成一个WebService
### DynamicApisDiscovery
```go
// DynamicApisDiscovery returns a webservice serving api group discovery.
// Note: during the server runtime apiGroupsForDiscovery might change.
/*
	译：DynamicApisDiscovery返回一个webservice，用于api group的discovery。
		注意：在服务器运行期间apiGroupsForDiscovery可能会更改。
	实际上这里创建的WebService是用于list 该group下的所有versions，因为只注册了简单的路由规则。
*/
func (s *GenericAPIServer) DynamicApisDiscovery() *restful.WebService {
	/*
		调用了NewApisWebService函数，定义在
		==>/pkg/apiserver/apiserver.go
			==>func NewApisWebService(s runtime.NegotiatedSerializer, apiPrefix string, f func(req *restful.Request)

		这里传进去三个参数，分别是： 编解码对象，APIGroup的Prefix，还有一个function

		APIGroupPrefix is where non-legacy API group will be located.
		APIGroupPrefix = "/apis"
	*/
	return apiserver.NewApisWebService(s.Serializer, APIGroupPrefix, func(req *restful.Request) []unversioned.APIGroup {
		/*
			需要加锁
			接口注释也有说明。因为k8s可以动态加载第三方apiGroups
		*/
		s.apiGroupsForDiscoveryLock.RLock()
		defer s.apiGroupsForDiscoveryLock.RUnlock()

		// sort to have a deterministic order
		// 将apiGroupsForDiscovery中所有的APIGroup按照其名字进行升序排序
		sortedGroups := []unversioned.APIGroup{}
		groupNames := make([]string, 0, len(s.apiGroupsForDiscovery))
		for groupName := range s.apiGroupsForDiscovery {
			groupNames = append(groupNames, groupName)
		}
		sort.Strings(groupNames)
		for _, groupName := range groupNames {
			sortedGroups = append(sortedGroups, s.apiGroupsForDiscovery[groupName])
		}

		// 创建切片，并填充各个APIGroup的ServerAddressByClientCIDRs信息
		clientIP := utilnet.GetClientIP(req.Request)
		serverCIDR := s.discoveryAddresses.ServerAddressByClientCIDRs(clientIP)
		groups := make([]unversioned.APIGroup, len(sortedGroups))
		for i := range sortedGroups {
			groups[i] = sortedGroups[i]
			groups[i].ServerAddressByClientCIDRs = serverCIDR
		}
		return groups
	})
}
```
#### func NewApisWebService
来看看`func NewApisWebService`，真正生成一个WebService
```go
// NewApisWebService returns a webservice serving the available api version under /apis.
/*
	生成一个webservice，用于支持发现/apis目录下的restful api
	其路径是配置GET("/")
*/
func NewApisWebService(s runtime.NegotiatedSerializer, apiPrefix string, f func(req *restful.Request) []unversioned.APIGroup) *restful.WebService {
	/*
		这里只会在执Apiserver初始化的时候执行一次
		apiPrefix 的值是 /apis
	*/

	// Because in release 1.1, /apis returns response with empty APIVersion, we
	// use StripVersionNegotiatedSerializer to keep the response backwards
	// compatible.
	// 用于向后兼容v1.1版本，返回一个空的APIGroup
	ss := StripVersionNegotiatedSerializer{s}
	// 获取支持的媒体类型,比如：application/json，application/yaml
	mediaTypes, _ := mediaTypesForSerializer(s)
	// 构建go-restful的Route处理方法
	rootAPIHandler := RootAPIHandler(ss, f)
	// 创建WebService
	ws := new(restful.WebService)
	// 添加Path，/apis
	ws.Path(apiPrefix)
	// API 说明
	ws.Doc("get available API versions")
	/*
	   设置了一条路由
	   配置GET("/") 转到rootAPIHandler()方法
	*/
	ws.Route(ws.GET("/").To(rootAPIHandler).
		Doc("get available API versions").
		Operation("getAPIVersions").
		Produces(mediaTypes...).
		Consumes(mediaTypes...).
		Writes(unversioned.APIGroupList{}))
	return ws
	/*
		到这里list某个Group下所有的versions的API已经注册完成了。
		这些都不是关键的RESTful API的注册，关键的注册都会在pkg/apiserver/apiserver.go中的
		func (g *APIGroupVersion) InstallREST(container *restful.Container) 中进行。
	*/
}
```
查看其handler函数`func RootAPIHandler`，作用是list the provided groups and versions as available。
```go
// RootAPIHandler returns a handler which will list the provided groups and versions as available.
func RootAPIHandler(s runtime.NegotiatedSerializer, f func(req *restful.Request) []unversioned.APIGroup) restful.RouteFunction {
	return func(req *restful.Request, resp *restful.Response) {
		writeNegotiated(s, unversioned.GroupVersion{}, resp.ResponseWriter, req.Request, http.StatusOK, &unversioned.APIGroupList{Groups: filterAPIGroups(req, f(req))})
	}
}
```
可以通过下面命令来试试效果
```
#curl -k -XGET   http://localhost:8080/apis/
[root@fqhnode01 ~]# curl http://192.168.56.101:8080/apis/
{
  "kind": "APIGroupList",
  "groups": [
    {
      "name": "apps",
      "versions": [
        {
          "groupVersion": "apps/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "apps/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "authentication.k8s.io",
      "versions": [
        {
          "groupVersion": "authentication.k8s.io/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "authentication.k8s.io/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "authorization.k8s.io",
      "versions": [
        {
          "groupVersion": "authorization.k8s.io/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "authorization.k8s.io/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "autoscaling",
      "versions": [
        {
          "groupVersion": "autoscaling/v1",
          "version": "v1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "autoscaling/v1",
        "version": "v1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "batch",
      "versions": [
        {
          "groupVersion": "batch/v1",
          "version": "v1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "batch/v1",
        "version": "v1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "certificates.k8s.io",
      "versions": [
        {
          "groupVersion": "certificates.k8s.io/v1alpha1",
          "version": "v1alpha1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "certificates.k8s.io/v1alpha1",
        "version": "v1alpha1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "extensions",
      "versions": [
        {
          "groupVersion": "extensions/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "extensions/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "policy",
      "versions": [
        {
          "groupVersion": "policy/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "policy/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "rbac.authorization.k8s.io",
      "versions": [
        {
          "groupVersion": "rbac.authorization.k8s.io/v1alpha1",
          "version": "v1alpha1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "rbac.authorization.k8s.io/v1alpha1",
        "version": "v1alpha1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    },
    {
      "name": "storage.k8s.io",
      "versions": [
        {
          "groupVersion": "storage.k8s.io/v1beta1",
          "version": "v1beta1"
        }
      ],
      "preferredVersion": {
        "groupVersion": "storage.k8s.io/v1beta1",
        "version": "v1beta1"
      },
      "serverAddressByClientCIDRs": [
        {
          "clientCIDR": "0.0.0.0/0",
          "serverAddress": "10.0.2.15:6443"
        }
      ]
    }
  ]
}
```

## New了一个APIContainer
对`go-restful`中的container进行了封装处理

mux是一个http请求多路复用器。它将每个传入请求的URL与已注册模式的列表相匹配，并调用对应的http.handler来处理。
```
// APIContainer is a restful container which in addition support registering
// handlers that do not show up in swagger or in /
type APIContainer struct {
	/*
		封装了一个restful.Container "github.com/emicklei/go-restful"
		其初始化路径：
			main --> App.Run --> master.Complete.New --> c.Config.GenericConfig.SkipComplete().New()
	*/
	*restful.Container
	NonSwaggerRoutes PathRecorderMux
	SecretRoutes     Mux
}

// NewAPIContainer constructs a new container for APIs
func NewAPIContainer(mux *http.ServeMux, s runtime.NegotiatedSerializer) *APIContainer {
	c := APIContainer{
		//创建一个go-restful框架中的Container
		Container: restful.NewContainer(),
		NonSwaggerRoutes: PathRecorderMux{
			mux: mux,
		},
		SecretRoutes: mux,
	}
	// 配置http.ServeMux
	c.Container.ServeMux = mux
	// 配置该Container的路由方式：CurlyRouter 即快速路由
	c.Container.Router(restful.CurlyRouter{}) // e.g. for proxy/{kind}/{name}/{*}

	/*
		配置panic产生之后的恢复处理函数：

		InstallRecoverHandler定义在/pkg/apiserver/apiserver.go
			==>func InstallRecoverHandler(s runtime.NegotiatedSerializer, container *restful.Container)

		apiserver.InstallServiceErrorHandler()接口，其实就是修改Service Error产生后的错误处理函数，
		默认是调用writeServiceError()
		定义在/pkg/apiserver/serviceerror.go
	*/
	apiserver.InstallRecoverHandler(s, c.Container)
	apiserver.InstallServiceErrorHandler(s, c.Container)

	return &c
}
```

## 总结
本文主要介绍`/apis`下GET("/")的生成过程，这是最简单的一个路由，是对go-restful package的一个简单使用。
下篇文章会就Master的InstallLegacyAPI函数进行展开。

## 参考
[godoc master](https://godoc.org/k8s.io/kubernetes/pkg/master#Master)