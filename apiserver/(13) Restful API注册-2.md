# Apiserver Restful API注册-2

我们接着上文，从函数`func (m *Master) InstallLegacyAPI`出发，研究core Group的资源是怎么注册成为API的？
见/pkg/master/master.go
## 注册核心Group-InstallLegacyAPI
InstallLegacyAPI函数流程并不复杂，一共有3步：
1. 通过`type LegacyRESTStorageProvider struct`生成Core Group的RESTStorage和apiGroupInfo
```go
//定义在pkg/registry/core/rest/storage_core.go
legacyRESTStorage, apiGroupInfo, err := legacyRESTStorageProvider.NewLegacyRESTStorage(restOptionsGetter)
```
2. 新建一个apiserver的引导controller
3. install core Group's API
```go
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
```

下面我们逐步来分析
## type LegacyRESTStorageProvider struct
type LegacyRESTStorageProvider struct用于提供创建core Group的RESTStorage的信息。见/pkg/registry/core/rest/storage_core.go。

```go
// LegacyRESTStorageProvider provides information needed to build RESTStorage for core, but
// does NOT implement the "normal" RESTStorageProvider (yet!)

type LegacyRESTStorageProvider struct {
	StorageFactory genericapiserver.StorageFactory
	// Used for custom proxy dialing, and proxy TLS options
	ProxyTransport      http.RoundTripper
	KubeletClientConfig kubeletclient.KubeletClientConfig
	EventTTL            time.Duration

	// ServiceIPRange is used to build cluster IPs for discovery.
	ServiceIPRange       net.IPNet
	ServiceNodePortRange utilnet.PortRange

	LoopbackClientConfig *restclient.Config
}
```

其实每一个Group的资源都有其REST实现，`k8s.io/kubernetes/pkg/registry`下所有的Group都有一个rest目录，存储的就是对应资源的REST Storage。
```go
k8s.io/kubernetes/pkg/registry/
▾ apps/
  ▸ petset/
  ▸ rest/
       storage_apps.go  #提供给GenericAPIServer的storage
▸ authentication/
▸ authorization/
▸ autoscaling/
▸ batch/
▸ cachesize/
▸ certificates/
▸ core/             #core/rest中实现了NewLegacyRESTStorage()
▸ extensions/
▸ policy/
▸ rbac/
▸ registrytest/
▸ settings/
▸ storage/
```

### NewLegacyRESTStorage
分析其流程，如下:
1. 生成一个type APIGroupInfo struct实例，这个和前面说的资源注册的`APIRegistrationManager、Scheme、GroupMeta...`有关系。
2. 初始化一个LegacyRESTStorage对象，即restStorage
3. 创建各类Storage，如podStorage、nodeStorage...(还搞不清和步骤2中restStorage的关系。。？？)
4. 把步骤3中创建的各种Storage保存到restStorageMap中，然后装在到APIGroupInfo中，APIGroupInfo.VersionedResourcesStorageMap["v1"]。这是API映射map。
5. return restStorage, APIGroupInfo
```go
func (c LegacyRESTStorageProvider) NewLegacyRESTStorage(restOptionsGetter genericapiserver.RESTOptionsGetter) (LegacyRESTStorage, genericapiserver.APIGroupInfo, error) {
	// 初始化创建一个APIGroupInfo
	apiGroupInfo := genericapiserver.APIGroupInfo{
		/*
			该GroupMeta是从APIRegistrationManager初始化后的结构体获取。
			api.GroupName，定义在/pkg/api/register.go
				==>const GroupName = ""
		*/
		GroupMeta:                    *registered.GroupOrDie(api.GroupName),
		VersionedResourcesStorageMap: map[string]map[string]rest.Storage{},
		/*
			这个api.Scheme的初始化可以参考
			==>/pkg/api/install/install.go中的func enableVersions(externalVersions []unversioned.GroupVersion)
				==>addVersionsToScheme(externalVersions...)
		*/
		Scheme:                      api.Scheme,
		ParameterCodec:              api.ParameterCodec,
		NegotiatedSerializer:        api.Codecs,
		SubresourceGroupVersionKind: map[string]unversioned.GroupVersionKind{},
	}
	/*
		判断下autoscaling是否已经注册并已经enabled，
		如果是的话，加入到apiGroupInfo.SubresourceGroupVersionKind
		key是该资源的path
	*/
	if autoscalingGroupVersion := (unversioned.GroupVersion{Group: "autoscaling", Version: "v1"}); registered.IsEnabledVersion(autoscalingGroupVersion) {
		apiGroupInfo.SubresourceGroupVersionKind["replicationcontrollers/scale"] = autoscalingGroupVersion.WithKind("Scale")
	}

	var podDisruptionClient policyclient.PodDisruptionBudgetsGetter
	if policyGroupVersion := (unversioned.GroupVersion{Group: "policy", Version: "v1beta1"}); registered.IsEnabledVersion(policyGroupVersion) {
		apiGroupInfo.SubresourceGroupVersionKind["pods/eviction"] = policyGroupVersion.WithKind("Eviction")

		var err error
		podDisruptionClient, err = policyclient.NewForConfig(c.LoopbackClientConfig)
		if err != nil {
			return LegacyRESTStorage{}, genericapiserver.APIGroupInfo{}, err
		}
	}
	/*
		初始化一个LegacyRESTStorage对象
		下面会进行各个接口的初始化，会有Node注册，IP申请，NodePort申请等等
	*/
	restStorage := LegacyRESTStorage{}

	/*
		创建各类Storage
	*/
	podTemplateStorage := podtemplateetcd.NewREST(restOptionsGetter(api.Resource("podTemplates")))

	eventStorage := eventetcd.NewREST(restOptionsGetter(api.Resource("events")), uint64(c.EventTTL.Seconds()))
	limitRangeStorage := limitrangeetcd.NewREST(restOptionsGetter(api.Resource("limitRanges")))

	resourceQuotaStorage, resourceQuotaStatusStorage := resourcequotaetcd.NewREST(restOptionsGetter(api.Resource("resourceQuotas")))
	secretStorage := secretetcd.NewREST(restOptionsGetter(api.Resource("secrets")))
	serviceAccountStorage := serviceaccountetcd.NewREST(restOptionsGetter(api.Resource("serviceAccounts")))
	persistentVolumeStorage, persistentVolumeStatusStorage := pvetcd.NewREST(restOptionsGetter(api.Resource("persistentVolumes")))
	persistentVolumeClaimStorage, persistentVolumeClaimStatusStorage := pvcetcd.NewREST(restOptionsGetter(api.Resource("persistentVolumeClaims")))
	configMapStorage := configmapetcd.NewREST(restOptionsGetter(api.Resource("configMaps")))

	namespaceStorage, namespaceStatusStorage, namespaceFinalizeStorage := namespaceetcd.NewREST(restOptionsGetter(api.Resource("namespaces")))
	restStorage.NamespaceRegistry = namespace.NewRegistry(namespaceStorage)

	endpointsStorage := endpointsetcd.NewREST(restOptionsGetter(api.Resource("endpoints")))
	restStorage.EndpointRegistry = endpoint.NewRegistry(endpointsStorage)

	nodeStorage, err := nodeetcd.NewStorage(restOptionsGetter(api.Resource("nodes")), c.KubeletClientConfig, c.ProxyTransport)
	if err != nil {
		return LegacyRESTStorage{}, genericapiserver.APIGroupInfo{}, err
	}
	restStorage.NodeRegistry = node.NewRegistry(nodeStorage.Node)

	/*
		创建PodStorage
		api.Resource("pods")是合成了一个GroupResource的结构

		podetcd.NewStorage函数定义在
			==>/pkg/registry/core/pod/etcd/etcd.go中
				==>func NewStorage

		restOptionsGetter(api.Resource("pods"))该步完成了opts的创建，该opt在具体的资源watch-list中很重要
		restOptionsGetter的由来可以查看/pkg/master/master.go中的master的func (c completedConfig) New() (*Master, error)
	*/
	podStorage := podetcd.NewStorage(
		restOptionsGetter(api.Resource("pods")),
		nodeStorage.KubeletConnectionInfo,
		c.ProxyTransport,
		podDisruptionClient,
	)

	serviceRESTStorage, serviceStatusStorage := serviceetcd.NewREST(restOptionsGetter(api.Resource("services")))
	restStorage.ServiceRegistry = service.NewRegistry(serviceRESTStorage)

	var serviceClusterIPRegistry rangeallocation.RangeRegistry
	serviceClusterIPRange := c.ServiceIPRange
	if serviceClusterIPRange.IP == nil {
		return LegacyRESTStorage{}, genericapiserver.APIGroupInfo{}, fmt.Errorf("service clusterIPRange is missing")
	}

	serviceStorageConfig, err := c.StorageFactory.NewConfig(api.Resource("services"))
	if err != nil {
		return LegacyRESTStorage{}, genericapiserver.APIGroupInfo{}, err
	}

	ServiceClusterIPAllocator := ipallocator.NewAllocatorCIDRRange(&serviceClusterIPRange, func(max int, rangeSpec string) allocator.Interface {
		mem := allocator.NewAllocationMap(max, rangeSpec)
		// TODO etcdallocator package to return a storage interface via the storageFactory
		etcd := etcdallocator.NewEtcd(mem, "/ranges/serviceips", api.Resource("serviceipallocations"), serviceStorageConfig)
		serviceClusterIPRegistry = etcd
		return etcd
	})
	restStorage.ServiceClusterIPAllocator = serviceClusterIPRegistry

	var serviceNodePortRegistry rangeallocation.RangeRegistry
	ServiceNodePortAllocator := portallocator.NewPortAllocatorCustom(c.ServiceNodePortRange, func(max int, rangeSpec string) allocator.Interface {
		mem := allocator.NewAllocationMap(max, rangeSpec)
		// TODO etcdallocator package to return a storage interface via the storageFactory
		etcd := etcdallocator.NewEtcd(mem, "/ranges/servicenodeports", api.Resource("servicenodeportallocations"), serviceStorageConfig)
		serviceNodePortRegistry = etcd
		return etcd
	})
	restStorage.ServiceNodePortAllocator = serviceNodePortRegistry

	controllerStorage := controlleretcd.NewStorage(restOptionsGetter(api.Resource("replicationControllers")))

	serviceRest := service.NewStorage(restStorage.ServiceRegistry, restStorage.EndpointRegistry, ServiceClusterIPAllocator, ServiceNodePortAllocator, c.ProxyTransport)

	/*
		初始化了一个restStorage的map，
		把前面创建的各种Storage保存到restStorageMap中

		然后赋值给APIGroupInfo.VersionedResourcesStorageMap["v1"]
		这是API映射map
	*/
	restStorageMap := map[string]rest.Storage{
		"pods":             podStorage.Pod,
		"pods/attach":      podStorage.Attach,
		"pods/status":      podStorage.Status,
		"pods/log":         podStorage.Log,
		"pods/exec":        podStorage.Exec,
		"pods/portforward": podStorage.PortForward,
		"pods/proxy":       podStorage.Proxy,
		"pods/binding":     podStorage.Binding,
		"bindings":         podStorage.Binding,

		"podTemplates": podTemplateStorage,

		"replicationControllers":        controllerStorage.Controller,
		"replicationControllers/status": controllerStorage.Status,

		"services":        serviceRest.Service,
		"services/proxy":  serviceRest.Proxy,
		"services/status": serviceStatusStorage,

		"endpoints": endpointsStorage,

		"nodes":        nodeStorage.Node,
		"nodes/status": nodeStorage.Status,
		"nodes/proxy":  nodeStorage.Proxy,

		"events": eventStorage,

		"limitRanges":                   limitRangeStorage,
		"resourceQuotas":                resourceQuotaStorage,
		"resourceQuotas/status":         resourceQuotaStatusStorage,
		"namespaces":                    namespaceStorage,
		"namespaces/status":             namespaceStatusStorage,
		"namespaces/finalize":           namespaceFinalizeStorage,
		"secrets":                       secretStorage,
		"serviceAccounts":               serviceAccountStorage,
		"persistentVolumes":             persistentVolumeStorage,
		"persistentVolumes/status":      persistentVolumeStatusStorage,
		"persistentVolumeClaims":        persistentVolumeClaimStorage,
		"persistentVolumeClaims/status": persistentVolumeClaimStatusStorage,
		"configMaps":                    configMapStorage,

		"componentStatuses": componentstatus.NewStorage(componentStatusStorage{c.StorageFactory}.serversToValidate),
	}
	if registered.IsEnabledVersion(unversioned.GroupVersion{Group: "autoscaling", Version: "v1"}) {
		restStorageMap["replicationControllers/scale"] = controllerStorage.Scale
	}
	if registered.IsEnabledVersion(unversioned.GroupVersion{Group: "policy", Version: "v1beta1"}) {
		restStorageMap["pods/eviction"] = podStorage.Eviction
	}
	// 将上面的restStorageMap赋值给v1，装载到了apiGroupInfo
	apiGroupInfo.VersionedResourcesStorageMap["v1"] = restStorageMap

	return restStorage, apiGroupInfo, nil
}
```
看看数据结构`type LegacyRESTStorage struct`:
- type LegacyRESTStorage struct
```go
// LegacyRESTStorage returns stateful information about particular instances of REST storage to
// master.go for wiring controllers.
/*
	LegacyRESTStorage将关于REST存储的特定实例的状态信息返回给master.go
*/
type LegacyRESTStorage struct {
	NodeRegistry              node.Registry
	NamespaceRegistry         namespace.Registry
	ServiceRegistry           service.Registry
	EndpointRegistry          endpoint.Registry
	ServiceClusterIPAllocator rangeallocation.RangeRegistry
	ServiceNodePortAllocator  rangeallocation.RangeRegistry
}
```

## 基于上面的生成的APIGroupInfo信息调用GenericAPIServer.InstallLegacyAPIGroup
```go
func (s *GenericAPIServer) InstallLegacyAPIGroup(apiPrefix string, apiGroupInfo *APIGroupInfo) error {
	// 判断前缀参数是否正确
	/*
		s.legacyAPIGroupPrefixes is: map[/api:{}]
	*/
	if !s.legacyAPIGroupPrefixes.Has(apiPrefix) {
		return fmt.Errorf("%q is not in the allowed legacy API prefixes: %v", apiPrefix, s.legacyAPIGroupPrefixes.List())
	}
	/*
		关键接口，真正install API（转化为resuful api）
		****
		调用func (s *GenericAPIServer) installAPIResources
		apiServer之资源注册-V3.0
	*/
	if err := s.installAPIResources(apiPrefix, apiGroupInfo); err != nil {
		return err
	}

	// setup discovery
	/*
		获取了该Group下所有的version信息
		添加一个WebService，其route路径是/api
	*/
	apiVersions := []string{}
	for _, groupVersion := range apiGroupInfo.GroupMeta.GroupVersions {
		apiVersions = append(apiVersions, groupVersion.Version)
	}
	// Install the version handler.
	// Add a handler at /<apiPrefix> to enumerate the supported api versions.
	apiserver.AddApiWebService(s.Serializer, s.HandlerContainer.Container, apiPrefix, func(req *restful.Request) *unversioned.APIVersions {
		clientIP := utilnet.GetClientIP(req.Request)

		apiVersionsForDiscovery := unversioned.APIVersions{
			ServerAddressByClientCIDRs: s.discoveryAddresses.ServerAddressByClientCIDRs(clientIP),
			Versions:                   apiVersions,
		}
		return &apiVersionsForDiscovery
	})
	return nil
}
```
这里执行一下`curl http://192.168.56.101:8080/api`命令
```
#curl http://192.168.56.101:8080/api
{
  "kind": "APIVersions",
  "versions": [
    "v1"
  ],
  "serverAddressByClientCIDRs": [
    {
      "clientCIDR": "0.0.0.0/0",
      "serverAddress": "10.0.2.15:6443"
    }
  ]
}
```

## installAPIResources
分析`func (s *GenericAPIServer) installAPIResources`，其流程如下:
1. 遍历该Group的所有versions（一个Group调用一次本函数，亦即所有Group最后都是调用本函数）
2. apiGroupInfo, groupVersion, apiPrefix基于创建一个type APIGroupVersion struct对象
3. 根据创建的APIGroupVersion,然后安装restful API，`apiGroupVersion.InstallREST`
```go
// installAPIResources is a private method for installing the REST storage backing each api groupversionresource
/*
	译：installAPIResources是一个私有函数，用于安装每个api groupversionresource的REST存储
*/
func (s *GenericAPIServer) installAPIResources(apiPrefix string, apiGroupInfo *APIGroupInfo) error {
	// 遍历该Group下的所有GroupVersons
	for _, groupVersion := range apiGroupInfo.GroupMeta.GroupVersions {
		/*
			创建APIGroupVersion

			调用func (s *GenericAPIServer) getAPIGroupVersion
			apiServer之资源注册-V4.0
		*/
		apiGroupVersion, err := s.getAPIGroupVersion(apiGroupInfo, groupVersion, apiPrefix)
		if err != nil {
			return err
		}
		if apiGroupInfo.OptionsExternalVersion != nil {
			apiGroupVersion.OptionsExternalVersion = apiGroupInfo.OptionsExternalVersion
		}

		/*
			根据之前创建的APIGroupVersion,然后安装restful API
			该s.HandlerContainer.Container就是go-restful的Container
			InstallREST 定义在：pkg/apiserver/apiserver.go
					==>func (g *APIGroupVersion) InstallREST(container *restful.Container)
		*/
		if err := apiGroupVersion.InstallREST(s.HandlerContainer.Container); err != nil {
			return fmt.Errorf("Unable to setup API %v: %v", apiGroupInfo, err)
			/*
				到这里从API资源到restful API,就已经注册完成了。
			*/
		}
	}

	return nil
}
```

## 创建APIGroupVersion过程
- getAPIGroupVersion
```go
func (s *GenericAPIServer) getAPIGroupVersion(apiGroupInfo *APIGroupInfo, groupVersion unversioned.GroupVersion, apiPrefix string) (*apiserver.APIGroupVersion, error) {
	storage := make(map[string]rest.Storage)
	/*
		如果是核心组的话，Version为"v1",
		apiGroupInfo.VersionedResourcesStorageMap的初始化可以查看
			==>/pkg/registry/core/rest/storage_core.go
				==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage

		遍历所有的ResourcesStorage，并赋值给storage
	*/
	for k, v := range apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version] {
		/*
			[k,v] is:  pods/portforward &{0xc4200b1770 0xc4207a2cd0}
			[k,v] is:  bindings &{0xc4200b1770}
			[k,v] is:  namespaces &{0xc4200b0d20 0xc4200b0e10}.......
		*/
		storage[strings.ToLower(k)] = v
	}
	/*
		创建APIGroupVersion

		调用func (s *GenericAPIServer) newAPIGroupVersion
		apiServer之资源注册-V5.0
	*/
	version, err := s.newAPIGroupVersion(apiGroupInfo, groupVersion)
	// 设置Prefix, 核心组的话是"/api"
	version.Root = apiPrefix
	version.Storage = storage
	return version, err
}
```

- newAPIGroupVersion
```go
func (s *GenericAPIServer) newAPIGroupVersion(apiGroupInfo *APIGroupInfo, groupVersion unversioned.GroupVersion) (*apiserver.APIGroupVersion, error) {
	/*
		结构体 APIGroupVersion 定义在
		==>pkg/apiserver/apiserver.go
			==>type APIGroupVersion struct
		整个k8s中会生成多个APIGroupVersion
	*/
	glog.Infof("生成APIGroupVersion")
	return &apiserver.APIGroupVersion{
		/*
			在创建APIGroupVersion过程中还用到了好几个别的结构：APIGroupInfo、Scheme、GroupMeta
			都需要详细了解，是apiServer之资源注册的核心结构

			其中APIGroupInfo定义在/pkg/genericapiserver/genericapiserver.go
								==>type APIGroupInfo struct

			   Scheme定义在/pkg/runtime/scheme.go
						 ==>type Scheme struct

			   GroupMeta定义在/pkg/apimachinery/types.go
							==>type GroupMeta struct
		*/
		GroupVersion: groupVersion,

		ParameterCodec: apiGroupInfo.ParameterCodec,
		Serializer:     apiGroupInfo.NegotiatedSerializer,
		Creater:        apiGroupInfo.Scheme,
		Convertor:      apiGroupInfo.Scheme,
		Copier:         apiGroupInfo.Scheme,
		Typer:          apiGroupInfo.Scheme,
		SubresourceGroupVersionKind: apiGroupInfo.SubresourceGroupVersionKind,
		Linker: apiGroupInfo.GroupMeta.SelfLinker,
		Mapper: apiGroupInfo.GroupMeta.RESTMapper,

		Admit:             s.admissionControl,
		Context:           s.RequestContextMapper(),
		MinRequestTimeout: s.minRequestTimeout,
	}, nil
}
```

## InstallREST安装restful API
见pkg/apiserver/apiserver.go
```go
// InstallREST registers the REST handlers (storage, watch, proxy and redirect) into a restful Container.
// It is expected that the provided path root prefix will serve all operations. Root MUST NOT end
// in a slash.
/*
	译：InstallREST将REST handlers（storage, watch, proxy and redirect）注册到go-restful框架的Container中。
		预期的结果是提供的路径root prefix在所有的服务都是生效的，
		Root不能以 / 结束
*/
/*
	当API资源初始化完成以后，需要将这些API资源注册为restful api，用来接收用户的请求。
	kube-apiServer使用了go-restful这套框架，里面主要包括三种对象：
	- Container: 一个Container包含多个WebService
	- WebService: 一个WebService包含多条route
	- Route: 一条route包含一个method(GET、POST、DELETE等)，一条具体的path(URL)以及一个响应的handler function。
*/
func (g *APIGroupVersion) InstallREST(container *restful.Container) error {
	/*
		newInstaller()  拼装path: "Prefix/Group/Version"
		然后填充并返回一个APIInstaller对象
	*/
	installer := g.newInstaller()
	// 创建一个WebService
	ws := installer.NewWebService()
	/*
		*********************************************
		*********************************************
		调用Install函数
		这个是关键，会对各种URL进行注册！
		在这个注册的过程中，InstallREST最终调用了registerResourceHandlers()接口，
		registerResourceHandlers()接口最终会把一个rest.Storage对象转换成实际的getter、lister等处理函数，
		并和实际的URL关联起来。
	*/
	apiResources, registrationErrors := installer.Install(ws)
	lister := g.ResourceLister
	if lister == nil {
		lister = staticLister{apiResources}
	}
	// 增加一个list的API
	AddSupportedResourcesWebService(g.Serializer, ws, g.GroupVersion, lister)
	// 将该WebService加入到Container
	container.Add(ws)
	return utilerrors.NewAggregate(registrationErrors)
}
```