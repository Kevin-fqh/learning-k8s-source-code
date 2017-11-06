# Apiserver Restful API注册-2

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [注册核心Group-InstallLegacyAPI](#注册核心group-installlegacyapi)
  - [type LegacyRESTStorageProvider struct](#type-legacyreststorageprovider-struct)
	- [NewLegacyRESTStorage](#newlegacyreststorage)
  - [InstallLegacyAPIGroup，使用APIGroupInfo](#installlegacyapigroup)
  - [installAPIResources](#installapiresources)
  - [创建APIGroupVersion](#创建apigroupversion)
  - [InstallREST安装restful API](#installrest安装restful-api)
  - [type APIInstaller struct](#type-apiinstaller-struct)
	- [NewWebService](#newwebservice)
	- [Install](#install)
	- [registerResourceHandlers](#registerresourcehandlers)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

我们接着上文，从函数`func (m *Master) InstallLegacyAPI`出发，研究各个Group的资源是怎么注册成为API的？
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
4. 把步骤3中创建的各种Storage保存到restStorageMap中，然后装在到APIGroupInfo中，APIGroupInfo.VersionedResourcesStorageMap["v1"]。这是API映射map，这很重要，在后面的利用APIGroupInfo来生成APIGroupVersion的时候，就是依靠这个map映射关系来获取对应version的资源的rest strorage实现。
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
		
		restOptionsGetter的由来可以查看/pkg/master/master.go中的master的
		==>func (c completedConfig) New() (*Master, error)
			==>func (f restOptionsFactory) NewFor
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
		
		podStorage 是所有路径为/pods的rest storage，要注意区分范围
	*/
	restStorageMap := map[string]rest.Storage{
		"pods":             podStorage.Pod, //这个才是最小范围的真正的/pods 的storage
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
	/*
		将上面的restStorageMap赋值给v1，装载到了apiGroupInfo

		这映射关系很重要，在后面的利用APIGroupInfo来生成APIGroupVersion的时候，
		就是依靠这个map映射关系来获取对应version的资源的rest strorage实现。
			==>/pkg/genericapiserver/genericapiserver.go
				==>func (s *GenericAPIServer) getAPIGroupVersion
	*/
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

## InstallLegacyAPIGroup
NewLegacyRESTStorage主要功能：在APIGroupInfo中装载各类的storage。

后期，GenericAPIServer会根据APIGroupInfo中的storage，自动生成REST handler。

apiserver在存取资源时，最终也是通过各个storage完成
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
1. 遍历该Group的所有versions（一个Group调用一次本函数，亦即所有Group最后都是调用本函数来安装Restful API）
2. 基于apiGroupInfo, groupVersion, apiPrefix创建一个type APIGroupVersion struct对象
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

## 创建APIGroupVersion
基于APIGroupInfo生成一个APIGroupVersion。

这里对apiGroupInfo.VersionedResourcesStorageMap进行遍历，和前面设置资源的rest storage存储对应上了。
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
	//完成storage的赋值
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

		/*
			把前面初始化好的admissionControl赋值给APIGroupVersion
			后面注册restfulApi的时候会用到
		*/
		Admit:             s.admissionControl,
		Context:           s.RequestContextMapper(),
		MinRequestTimeout: s.minRequestTimeout,
	}, nil
}
```

## InstallREST安装restful API
InstallREST将REST handlers（storage, watch, proxy and redirect）注册到go-restful框架的Container中，见pkg/apiserver/apiserver.go。

分析其流程，如下:
1. 创建了一个type APIInstaller struct对象
2. 构造一个webservice
3. 往webservice里面添加Route
4. 往container中添加webservice

```go
// InstallREST registers the REST handlers (storage, watch, proxy and redirect) into a restful Container.
// It is expected that the provided path root prefix will serve all operations. Root MUST NOT end
// in a slash.
/*
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
	// 创建一个WebService，设置了ws的path属性
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
	/*
		一个list功能的API
		添加了一个Route，对应路径是"/"
		访问形如"Prefix/Group/Version"这样的根路径时候，返回该GroupVersion所支持的resources
	    curl http://192.168.56.101:8080/api/v1
	*/
	lister := g.ResourceLister
	if lister == nil {
		lister = staticLister{apiResources}
	}
	AddSupportedResourcesWebService(g.Serializer, ws, g.GroupVersion, lister)
	// 将该WebService加入到Container
	container.Add(ws)
	return utilerrors.NewAggregate(registrationErrors)
}
```
函数中`apiResources, registrationErrors := installer.Install(ws)`得到的`apiResources`部分返回结果如下
```
此时的prefix是:  /api/v1
**apiResources is : [{bindings true Binding} {componentstatuses false ComponentStatus} {configmaps true ConfigMap} {endpoints true Endpoints} {events true Event} {limitranges true LimitRange} {namespaces false Namespace} {namespaces/finalize false Namespace} {namespaces/status false Namespace} {nodes false Node} {nodes/proxy false Node} {nodes/status false Node} {persistentvolumeclaims true PersistentVolumeClaim} {persistentvolumeclaims/status true PersistentVolumeClaim} {persistentvolumes false PersistentVolume} {persistentvolumes/status false PersistentVolume} {pods true Pod} {pods/attach true Pod} {pods/binding true Binding} {pods/eviction true Eviction} {pods/exec true Pod} {pods/log true Pod} {pods/portforward true Pod} {pods/proxy true Pod} {pods/status true Pod} {podtemplates true PodTemplate} {replicationcontrollers true ReplicationController} {replicationcontrollers/scale true Scale} {replicationcontrollers/status true ReplicationController} {resourcequotas true ResourceQuota} {resourcequotas/status true ResourceQuota} {secrets true Secret} {serviceaccounts true ServiceAccount} {services true Service} {services/proxy true Service} {services/status true Service}]
此时的prefix是:  /apis/apps/v1beta1
**apiResources is : [{statefulsets true StatefulSet} {statefulsets/status true StatefulSet}]
此时的prefix是:  /apis/authentication.k8s.io/v1beta1
**apiResources is : [{tokenreviews false TokenReview}]
此时的prefix是:  /apis/authorization.k8s.io/v1beta1
**apiResources is : [{localsubjectaccessreviews true LocalSubjectAccessReview} {selfsubjectaccessreviews false SelfSubjectAccessReview} {subjectaccessreviews false SubjectAccessReview}]
此时的prefix是:  /apis/autoscaling/v1
**apiResources is : [{horizontalpodautoscalers true HorizontalPodAutoscaler} {horizontalpodautoscalers/status true HorizontalPodAutoscaler}]
此时的prefix是:  /apis/batch/v1
**apiResources is : [{jobs true Job} {jobs/status true Job}]
......
......
```

再来看看`type APIInstaller struct`的定义和功能函数，这也是本文的重点。
## type APIInstaller struct
```go
type APIInstaller struct {
	group             *APIGroupVersion
	prefix            string // Path prefix where API resources are to be registered.
	minRequestTimeout time.Duration
}

// newInstaller is a helper to create the installer.  Used by InstallREST and UpdateREST.
/*
	译：newInstaller是创建installer的一个helper。由InstallREST和UpdateREST使用。
*/
func (g *APIGroupVersion) newInstaller() *APIInstaller {
	/*
		拼装path: "Prefix/Group/Version"
		然后填充并返回一个APIInstaller对象
	*/
	prefix := path.Join(g.Root, g.GroupVersion.Group, g.GroupVersion.Version)
	/*
		prefix 是: /api/v1, /apis/apps/v1beta1, /apis/authentication.k8s.io/v1beta1,
			/apis/authorization.k8s.io/v1beta1, /apis/autoscaling/v1 ........
	*/
	installer := &APIInstaller{
		group:             g,
		prefix:            prefix,
		minRequestTimeout: g.MinRequestTimeout,
	}
	return installer
}
```

### NewWebService
基于APIInstaller对象创建一个webservice，设置了ws的Path
```go
// NewWebService creates a new restful webservice with the api installer's prefix and version.
func (a *APIInstaller) NewWebService() *restful.WebService {
	ws := new(restful.WebService)
	/*
		设置了ws的Path
	*/
	ws.Path(a.prefix)
	// a.prefix contains "prefix/group/version"
	ws.Doc("API at " + a.prefix)
	// Backwards compatibility, we accepted objects with empty content-type at V1.
	// If we stop using go-restful, we can default empty content-type to application/json on an
	// endpoint by endpoint basis
	ws.Consumes("*/*")
	mediaTypes, streamMediaTypes := mediaTypesForSerializer(a.group.Serializer)
	ws.Produces(append(mediaTypes, streamMediaTypes...)...)
	ws.ApiVersion(a.group.GroupVersion.String())

	return ws
}
```

### Install
func (a *APIInstaller) Install重点函数，为入参ws设置各种Route，见/pkg/apiserver/api_installer.go。

分析其流程，如下:
1. 获取一个GroupVersion下所有的paths，排序
2. 遍历paths，获取每一个path对应资源对应的rest storage，调用registerResourceHandlers

```go
// Installs handlers for API resources.
/*
	func (a *APIInstaller) Install(ws *restful.WebService)
	先是遍历所有的path，并升序重新排列，
	然后循环调用接口注册各个URL的API,并将这些注册成功的APIResource加入到同一个切片中。
*/
func (a *APIInstaller) Install(ws *restful.WebService) (apiResources []unversioned.APIResource, errors []error) {
	errors = make([]error, 0)

	proxyHandler := (&ProxyHandler{
		prefix: a.prefix + "/proxy/",
		//a.group.Storage 是如eventStorage、namespaceStorage这些
		storage:    a.group.Storage,
		serializer: a.group.Serializer,
		mapper:     a.group.Context,
	})

	// Register the paths in a deterministic (sorted) order to get a deterministic swagger spec.
	// 将所有的path合成一个切片，并按照升序重新排序
	paths := make([]string, len(a.group.Storage))
	var i int = 0
	for path := range a.group.Storage {
		paths[i] = path
		i++
	}
	sort.Strings(paths)
	fmt.Println("此时的prefix是: ", a.prefix)
	/*
		prefix 是: /api/v1 时
		paths: [bindings componentstatuses configmaps endpoints events limitranges
				namespaces namespaces/finalize namespaces/status nodes nodes/proxy
				nodes/status persistentvolumeclaims persistentvolumeclaims/status
				persistentvolumes persistentvolumes/status pods pods/attach pods/binding
				pods/eviction pods/exec pods/log pods/portforward pods/proxy pods/status
				podtemplates replicationcontrollers replicationcontrollers/scale
				replicationcontrollers/status resourcequotas resourcequotas/status
				secrets serviceaccounts services services/proxy services/status]
				
		prefix 是: /apis/apps/v1beta1时
		paths:  [statefulsets statefulsets/status]
		......
		......
	*/
	for _, path := range paths {
		/*
			*********************************************
			*********************************************
			注册各个URL，该接口是关键，
			最终将一个rest.Storage对象转换成实际的restful api，
			比如getter、lister等处理函数，并将实际的URL关联起来
			传入的参数：path，rest.Storage，WebService，Handler
			
			一次给一个资源注册Route
		*/
		/*
			假设此时参数如下，进行分析:
				prefix: /api/v1
				path: pods
			那么对应的是定义在/pkg/registry/core/rest/storage_core.go中的
				==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
						==>"pods":             podStorage.Pod,
		*/
		apiResource, err := a.registerResourceHandlers(path, a.group.Storage[path], ws, proxyHandler)
		if err != nil {
			errors = append(errors, fmt.Errorf("error in registering resource: %s, %v", path, err))
		}
		/*
			将所有注册成功的Resource合成一个切片apiResources
			将该切片作为返回值，便于之后的接口注册list Resources的API
		*/
		if apiResource != nil {
			apiResources = append(apiResources, *apiResource)
		}
	}
	return apiResources, errors
}
```

### registerResourceHandlers
这里完成了关键的REST API注册，分析其流程如下(分析案例prefix: /api/v1 path: pods):
1. 根据path获取资源，此时resource is:  pods
2. 根据resource获取restMapping。mapping, err := a.restMapping(resource)
3. 利用类型断言判断该storage实现了的Interface，获取Creater/Lister...的接口(必须是public函数，首字母大写)。一部分公共的接口是通过&registry.Store来声明的，而不是直接定义在具体的Storage如PodStorage.Pod的结构体上。这里注意当两边声明了同名函数的时候会优先调用type Rest Struct的显式声明函数。在type Rest Struct的函数中来调用`/pkg/registry/generic/registry/store.go`中的同名函数。
```
	==>/pkg/registry/core/namespace/etcd/etcd.go，/pkg/registry/core/pod/etcd/etcd.go
	==>/pkg/registry/generic/registry/store.go
```
4. 设置URL的部分参数值
```go
	/*
		func (w *WebService) PathParameter(name, description string) *Parameter
		设置URL的参数值

		用法:
		ws.Route(ws.GET("/{user-id}").To(u.findUser))
		id := request.PathParameter("user-id")
	*/
	nameParam := ws.PathParameter("name", "name of the "+kind).DataType("string")
	pathParam := ws.PathParameter("path", "path to the resource").DataType("string")
```
5. 给该资源(分两类进行，有无namespace)添加其支持的verb，存储到actions中。这里可以了解一下`type action struct`。
```go
// Struct capturing information about an action ("GET", "POST", "WATCH", PROXY", etc).
/*
	type action struct 定义了一个动作
	action{ "LIST",
			"componentstatuses",
			"[]",
			{0x4c144e0 {} /api/v1/componentstatuses },
			false
		  }
*/
type action struct {
	Verb          string               // Verb identifying the action ("GET", "POST", "WATCH", PROXY", etc).
	Path          string               // The path of the action
	Params        []*restful.Parameter // List of parameters associated with the action.
	Namer         ScopeNamer
	AllNamespaces bool // true iff the action is namespaced but works on aggregate result for all namespaces
}
```
6. 遍历切片actions，注册Route到WebService中
```
此时的prefix是:  /api/v1
**actions length is:*** 1
actions content is: [{POST namespaces/{namespace}/bindings [0xc420447f60] {0x4c144a0 {} 0x830830 false} false}]
**actions length is:*** 2
actions content is: [{LIST componentstatuses [] {0x4c144e0 {} /api/v1/componentstatuses } false} {GET componentstatuses/{name} [0xc420447f90] {0x4c144e0 {} /api/v1/componentstatuses } false}]

**actions length is:*** 11
actions content is: [{LIST nodes [] {0x4c144e0 {} /api/v1/nodes } false} {POST nodes [] {0x4c144e0 {} /api/v1/nodes } false} {DELETECOLLECTION nodes [] {0x4c144e0 {} /api/v1/nodes } false} {WATCHLIST watch/nodes [] {0x4c144e0 {} /api/v1/nodes } false} {GET nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false} {PUT nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false} {PATCH nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false} {DELETE nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false} {WATCH watch/nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false} {PROXY proxy/nodes/{name}/{path:*} [0xc420024c78 0xc420024c80] {0x4c144e0 {} /api/v1/nodes } false} {PROXY proxy/nodes/{name} [0xc420024c78] {0x4c144e0 {} /api/v1/nodes } false}]

**actions length is:*** 9
actions content is: [{LIST namespaces [] {0x4c144e0 {} /api/v1/namespaces } false} {POST namespaces [] {0x4c144e0 {} /api/v1/namespaces } false} {DELETECOLLECTION namespaces [] {0x4c144e0 {} /api/v1/namespaces } false} {WATCHLIST watch/namespaces [] {0x4c144e0 {} /api/v1/namespaces } false} {GET namespaces/{name} [0xc420447740] {0x4c144e0 {} /api/v1/namespaces } false} {PUT namespaces/{name} [0xc420447740] {0x4c144e0 {} /api/v1/namespaces } false} {PATCH namespaces/{name} [0xc420447740] {0x4c144e0 {} /api/v1/namespaces } false} {DELETE namespaces/{name} [0xc420447740] {0x4c144e0 {} /api/v1/namespaces } false} {WATCH watch/namespaces/{name} [0xc420447740] {0x4c144e0 {} /api/v1/namespaces } false}]

**actions length is:*** 9
actions content is: [{LIST persistentvolumes [] {0x4c144e0 {} /api/v1/persistentvolumes } false} {POST persistentvolumes [] {0x4c144e0 {} /api/v1/persistentvolumes } false} {DELETECOLLECTION persistentvolumes [] {0x4c144e0 {} /api/v1/persistentvolumes } false} {WATCHLIST watch/persistentvolumes [] {0x4c144e0 {} /api/v1/persistentvolumes } false} {GET persistentvolumes/{name} [0xc420022138] {0x4c144e0 {} /api/v1/persistentvolumes } false} {PUT persistentvolumes/{name} [0xc420022138] {0x4c144e0 {} /api/v1/persistentvolumes } false} {PATCH persistentvolumes/{name} [0xc420022138] {0x4c144e0 {} /api/v1/persistentvolumes } false} {DELETE persistentvolumes/{name} [0xc420022138] {0x4c144e0 {} /api/v1/persistentvolumes } false} {WATCH watch/persistentvolumes/{name} [0xc420022138] {0x4c144e0 {} /api/v1/persistentvolumes } false}]
```

- func (a *APIInstaller) registerResourceHandlers  
再来看看`registerResourceHandlers`函数的定义
```go
func (a *APIInstaller) registerResourceHandlers(path string, storage rest.Storage, ws *restful.WebService, proxyHandler http.Handler) (*unversioned.APIResource, error) {
	admit := a.group.Admit
	context := a.group.Context

	optionsExternalVersion := a.group.GroupVersion
	if a.group.OptionsExternalVersion != nil {
		optionsExternalVersion = *a.group.OptionsExternalVersion
	}

	resource, subresource, err := splitSubresource(path)
	/*
		以prefix是:  /api/v1 为例子
		path is: pods
		resource, subresource is:  pods
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
		path is: pods/attach
		resource, subresource is:  pods attach
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
		path is: pods/binding
		resource, subresource is:  pods binding
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
		path is: pods/eviction
		resource, subresource is:  pods eviction
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
		path is: pods/exec
		resource, subresource is:  pods exec
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
		path is: pods/log
		resource, subresource is:  pods log
		mapping is:  &{pods /v1, Kind=Pod 0x4c124a0 0xc4201e9800 {}}
	*/
	if err != nil {
		return nil, err
	}

	/*
		根据resource获取restMapping
	*/
	mapping, err := a.restMapping(resource)
	if err != nil {
		return nil, err
	}

	/*
		根据resource, storage获取GVK
		有一套规则，并不是简单获取
	*/
	fqKindToRegister, err := a.getResourceKind(path, storage)
	if err != nil {
		return nil, err
	}

	versionedPtr, err := a.group.Creater.New(fqKindToRegister)
	if err != nil {
		return nil, err
	}
	defaultVersionedObject := indirectArbitraryPointer(versionedPtr)
	kind := fqKindToRegister.Kind
	hasSubresource := len(subresource) > 0

	// what verbs are supported by the storage, used to know what verbs we support per path
	//该storage支持哪些verbs
	/*
		rest.Creater、rest.Lister、rest.Getter这些interface
		==>定义在/pkg/api/rest/rest.go

		利用类型断言，判断该storage是否实现了该interface
		这里已经得到该资源支持什么动作，true或者false

		这里需要注意的一点就是很多公共的接口都是通过&registry.Store来实现的，
		而不是直接定义在具体的Storage结构体上。
		注意当两边声明了同名函数的时候会优先调用type Rest Struct的函数。通过type Rest Struct的函数来调用`/pkg/registry/generic/registry/store.go`中的同名函数。
			==>/pkg/registry/core/namespace/etcd/etcd.go，/pkg/registry/core/pod/etcd/etcd.go
			==>/pkg/registry/generic/registry/store.go

		在/pkg/registry/core/pod/etcd/etcd.go查看pod所支持的接口

		类型断言：该storage是否实现了rest.Creater接口，如果实现了，则返回creater，true
				否则返回false
	*/
	creater, isCreater := storage.(rest.Creater)
	namedCreater, isNamedCreater := storage.(rest.NamedCreater)
	lister, isLister := storage.(rest.Lister)
	getter, isGetter := storage.(rest.Getter)
	getterWithOptions, isGetterWithOptions := storage.(rest.GetterWithOptions)
	deleter, isDeleter := storage.(rest.Deleter)
	gracefulDeleter, isGracefulDeleter := storage.(rest.GracefulDeleter)
	collectionDeleter, isCollectionDeleter := storage.(rest.CollectionDeleter)
	updater, isUpdater := storage.(rest.Updater)
	patcher, isPatcher := storage.(rest.Patcher)
	/*
		对于pod而言，/pkg/registry/core/pod/etcd/etcd.go，
		其watch接口来源于/pkg/registry/generic/registry/store.go的
		func (e *Store) Watch(ctx api.Context, options *api.ListOptions)

		就/pods而言，其对应的storage生成是在/pkg/registry/core/rest/storage_core.go
		==>func (c LegacyRESTStorageProvider) NewLegacyRESTStorage
			==>podStorage := podetcd.NewStorage
				==>"pods":             podStorage.Pod,
	*/
	watcher, isWatcher := storage.(rest.Watcher)
	_, isRedirector := storage.(rest.Redirector)
	connecter, isConnecter := storage.(rest.Connecter)
	storageMeta, isMetadata := storage.(rest.StorageMetadata)
	if !isMetadata {
		storageMeta = defaultStorageMetadata{}
	}
	exporter, isExporter := storage.(rest.Exporter)
	if !isExporter {
		exporter = nil
	}

	versionedExportOptions, err := a.group.Creater.New(optionsExternalVersion.WithKind("ExportOptions"))
	if err != nil {
		return nil, err
	}

	if isNamedCreater {
		isCreater = true
	}

	var versionedList interface{}
	if isLister {
		list := lister.NewList()
		listGVKs, _, err := a.group.Typer.ObjectKinds(list)
		if err != nil {
			return nil, err
		}
		versionedListPtr, err := a.group.Creater.New(a.group.GroupVersion.WithKind(listGVKs[0].Kind))
		if err != nil {
			return nil, err
		}
		versionedList = indirectArbitraryPointer(versionedListPtr)
	}

	versionedListOptions, err := a.group.Creater.New(optionsExternalVersion.WithKind("ListOptions"))
	if err != nil {
		return nil, err
	}

	var versionedDeleteOptions runtime.Object
	var versionedDeleterObject interface{}
	switch {
	case isGracefulDeleter:
		versionedDeleteOptions, err = a.group.Creater.New(optionsExternalVersion.WithKind("DeleteOptions"))
		if err != nil {
			return nil, err
		}
		versionedDeleterObject = indirectArbitraryPointer(versionedDeleteOptions)
		isDeleter = true
	case isDeleter:
		gracefulDeleter = rest.GracefulDeleteAdapter{Deleter: deleter}
	}

	versionedStatusPtr, err := a.group.Creater.New(optionsExternalVersion.WithKind("Status"))
	if err != nil {
		return nil, err
	}
	versionedStatus := indirectArbitraryPointer(versionedStatusPtr)
	var (
		getOptions             runtime.Object
		versionedGetOptions    runtime.Object
		getOptionsInternalKind unversioned.GroupVersionKind
		getSubpath             bool
	)
	if isGetterWithOptions {
		getOptions, getSubpath, _ = getterWithOptions.NewGetOptions()
		getOptionsInternalKinds, _, err := a.group.Typer.ObjectKinds(getOptions)
		if err != nil {
			return nil, err
		}
		getOptionsInternalKind = getOptionsInternalKinds[0]
		versionedGetOptions, err = a.group.Creater.New(optionsExternalVersion.WithKind(getOptionsInternalKind.Kind))
		if err != nil {
			return nil, err
		}
		isGetter = true
	}

	var versionedWatchEvent interface{}
	if isWatcher {
		versionedWatchEventPtr, err := a.group.Creater.New(a.group.GroupVersion.WithKind("WatchEvent"))
		if err != nil {
			return nil, err
		}
		versionedWatchEvent = indirectArbitraryPointer(versionedWatchEventPtr)
	}

	var (
		connectOptions             runtime.Object
		versionedConnectOptions    runtime.Object
		connectOptionsInternalKind unversioned.GroupVersionKind
		connectSubpath             bool
	)
	if isConnecter {
		connectOptions, connectSubpath, _ = connecter.NewConnectOptions()
		if connectOptions != nil {
			connectOptionsInternalKinds, _, err := a.group.Typer.ObjectKinds(connectOptions)
			if err != nil {
				return nil, err
			}

			connectOptionsInternalKind = connectOptionsInternalKinds[0]
			versionedConnectOptions, err = a.group.Creater.New(optionsExternalVersion.WithKind(connectOptionsInternalKind.Kind))
			if err != nil {
				return nil, err
			}
		}
	}

	var ctxFn ContextFunc
	ctxFn = func(req *restful.Request) api.Context {
		if context == nil {
			return api.WithUserAgent(api.NewContext(), req.HeaderParameter("User-Agent"))
		}
		if ctx, ok := context.Get(req.Request); ok {
			return api.WithUserAgent(ctx, req.HeaderParameter("User-Agent"))
		}
		return api.WithUserAgent(api.NewContext(), req.HeaderParameter("User-Agent"))
	}

	allowWatchList := isWatcher && isLister // watching on lists is allowed only for kinds that support both watch and list.
	scope := mapping.Scope
	/*
		func (w *WebService) PathParameter(name, description string) *Parameter
		设置URL的参数值

		用法:
		ws.Route(ws.GET("/{user-id}").To(u.findUser))
		id := request.PathParameter("user-id")
	*/
	nameParam := ws.PathParameter("name", "name of the "+kind).DataType("string")
	pathParam := ws.PathParameter("path", "path to the resource").DataType("string")

	params := []*restful.Parameter{}
	actions := []action{}

	var resourceKind string
	kindProvider, ok := storage.(rest.KindProvider)
	if ok {
		resourceKind = kindProvider.Kind()
	} else {
		resourceKind = kind
	}

	var apiResource unversioned.APIResource
	// Get the list of actions for the given scope.
	/*
		k8s资源分为两类：无namespace的RESTScopeNameRoot;
					  有namespace的RESTScopeNameNamespace

		在对应的path上添加各类action，存储到切片actions中
		actions记录着一个资源支持哪些verb，及其访问路径
	*/
	switch scope.Name() {
	case meta.RESTScopeNameRoot:
		// Handle non-namespace scoped resources like nodes.
		resourcePath := resource
		resourceParams := params
		itemPath := resourcePath + "/{name}"
		nameParams := append(params, nameParam)
		proxyParams := append(nameParams, pathParam)
		suffix := ""
		if hasSubresource {
			suffix = "/" + subresource
			itemPath = itemPath + suffix
			resourcePath = itemPath
			resourceParams = nameParams
		}
		apiResource.Name = path
		apiResource.Namespaced = false
		apiResource.Kind = resourceKind
		namer := rootScopeNaming{scope, a.group.Linker, gpath.Join(a.prefix, resourcePath, "/"), suffix}

		// Handler for standard REST verbs (GET, PUT, POST and DELETE).
		// Add actions at the resource path: /api/apiVersion/resource
		/*
			给resource path: /api/apiVersion/resource 添加动作
			namer就是GVR

			****例子***
			resourcePath, resourceParams is:  componentstatuses []
			itemPath is:  componentstatuses/{name}
			namer is:  {0x4c124e0 {} /api/v1/componentstatuses }

			resourcePath, resourceParams is:  namespaces []
			itemPath is:  namespaces/{name}
			namer is:  {0x4c124e0 {} /api/v1/namespaces }
		*/
		actions = appendIf(actions, action{"LIST", resourcePath, resourceParams, namer, false}, isLister)
		actions = appendIf(actions, action{"POST", resourcePath, resourceParams, namer, false}, isCreater)
		actions = appendIf(actions, action{"DELETECOLLECTION", resourcePath, resourceParams, namer, false}, isCollectionDeleter)
		// DEPRECATED
		actions = appendIf(actions, action{"WATCHLIST", "watch/" + resourcePath, resourceParams, namer, false}, allowWatchList)

		// Add actions at the item path: /api/apiVersion/resource/{name}
		actions = appendIf(actions, action{"GET", itemPath, nameParams, namer, false}, isGetter)
		if getSubpath {
			actions = appendIf(actions, action{"GET", itemPath + "/{path:*}", proxyParams, namer, false}, isGetter)
		}
		actions = appendIf(actions, action{"PUT", itemPath, nameParams, namer, false}, isUpdater)
		actions = appendIf(actions, action{"PATCH", itemPath, nameParams, namer, false}, isPatcher)
		actions = appendIf(actions, action{"DELETE", itemPath, nameParams, namer, false}, isDeleter)
		actions = appendIf(actions, action{"WATCH", "watch/" + itemPath, nameParams, namer, false}, isWatcher)
		// We add "proxy" subresource to remove the need for the generic top level prefix proxy.
		// The generic top level prefix proxy is deprecated in v1.2, and will be removed in 1.3, or 1.4 at the latest.
		// TODO: DEPRECATED in v1.2.
		actions = appendIf(actions, action{"PROXY", "proxy/" + itemPath + "/{path:*}", proxyParams, namer, false}, isRedirector)
		// TODO: DEPRECATED in v1.2.
		actions = appendIf(actions, action{"PROXY", "proxy/" + itemPath, nameParams, namer, false}, isRedirector)
		actions = appendIf(actions, action{"CONNECT", itemPath, nameParams, namer, false}, isConnecter)
		actions = appendIf(actions, action{"CONNECT", itemPath + "/{path:*}", proxyParams, namer, false}, isConnecter && connectSubpath)
		break
	case meta.RESTScopeNameNamespace:
		// Handler for standard REST verbs (GET, PUT, POST and DELETE).
		namespaceParam := ws.PathParameter(scope.ArgumentName(), scope.ParamDescription()).DataType("string")
		namespacedPath := scope.ParamName() + "/{" + scope.ArgumentName() + "}/" + resource
		namespaceParams := []*restful.Parameter{namespaceParam}

		resourcePath := namespacedPath
		resourceParams := namespaceParams
		itemPathPrefix := gpath.Join(a.prefix, scope.ParamName()) + "/"
		itemPath := namespacedPath + "/{name}"
		itemPathMiddle := "/" + resource + "/"
		nameParams := append(namespaceParams, nameParam)
		proxyParams := append(nameParams, pathParam)
		itemPathSuffix := ""
		if hasSubresource {
			itemPathSuffix = "/" + subresource
			itemPath = itemPath + itemPathSuffix
			resourcePath = itemPath
			resourceParams = nameParams
		}
		apiResource.Name = path
		apiResource.Namespaced = true
		apiResource.Kind = resourceKind

		itemPathFn := func(name, namespace string) bytes.Buffer {
			var buf bytes.Buffer
			buf.WriteString(itemPathPrefix)
			buf.WriteString(url.QueryEscape(namespace))
			buf.WriteString(itemPathMiddle)
			buf.WriteString(url.QueryEscape(name))
			buf.WriteString(itemPathSuffix)
			return buf
		}
		namer := scopeNaming{scope, a.group.Linker, itemPathFn, false}
		/*
			resourcePath, resourceParams is:  namespaces/{namespace}/bindings [0xc42043df48]
			itemPath is:  namespaces/{namespace}/bindings/{name}
			namer is:  {0x4c124a0 {} 0x830550 false}

			resourcePath, resourceParams is:  namespaces/{namespace}/events [0xc420026520]
			itemPath is:  namespaces/{namespace}/events/{name}
			namer is:  {0x4c124a0 {} 0x830550 false}
		*/

		actions = appendIf(actions, action{"LIST", resourcePath, resourceParams, namer, false}, isLister)
		actions = appendIf(actions, action{"POST", resourcePath, resourceParams, namer, false}, isCreater)
		actions = appendIf(actions, action{"DELETECOLLECTION", resourcePath, resourceParams, namer, false}, isCollectionDeleter)
		// DEPRECATED
		actions = appendIf(actions, action{"WATCHLIST", "watch/" + resourcePath, resourceParams, namer, false}, allowWatchList)

		actions = appendIf(actions, action{"GET", itemPath, nameParams, namer, false}, isGetter)
		if getSubpath {
			actions = appendIf(actions, action{"GET", itemPath + "/{path:*}", proxyParams, namer, false}, isGetter)
		}
		actions = appendIf(actions, action{"PUT", itemPath, nameParams, namer, false}, isUpdater)
		actions = appendIf(actions, action{"PATCH", itemPath, nameParams, namer, false}, isPatcher)
		actions = appendIf(actions, action{"DELETE", itemPath, nameParams, namer, false}, isDeleter)
		actions = appendIf(actions, action{"WATCH", "watch/" + itemPath, nameParams, namer, false}, isWatcher)
		// We add "proxy" subresource to remove the need for the generic top level prefix proxy.
		// The generic top level prefix proxy is deprecated in v1.2, and will be removed in 1.3, or 1.4 at the latest.
		// TODO: DEPRECATED in v1.2.
		actions = appendIf(actions, action{"PROXY", "proxy/" + itemPath + "/{path:*}", proxyParams, namer, false}, isRedirector)
		// TODO: DEPRECATED in v1.2.
		actions = appendIf(actions, action{"PROXY", "proxy/" + itemPath, nameParams, namer, false}, isRedirector)
		actions = appendIf(actions, action{"CONNECT", itemPath, nameParams, namer, false}, isConnecter)
		actions = appendIf(actions, action{"CONNECT", itemPath + "/{path:*}", proxyParams, namer, false}, isConnecter && connectSubpath)

		// list or post across namespace.
		// For ex: LIST all pods in all namespaces by sending a LIST request at /api/apiVersion/pods.
		// TODO: more strongly type whether a resource allows these actions on "all namespaces" (bulk delete)
		if !hasSubresource {
			namer = scopeNaming{scope, a.group.Linker, itemPathFn, true}
			actions = appendIf(actions, action{"LIST", resource, params, namer, true}, isLister)
			actions = appendIf(actions, action{"WATCHLIST", "watch/" + resource, params, namer, true}, allowWatchList)
		}
		break
	default:
		return nil, fmt.Errorf("unsupported restscope: %s", scope.Name())
	}

	/*************************/
	/**为上面的action创建路由**/
	/*************************/
	// Create Routes for the actions.
	// TODO: Add status documentation using Returns()
	// Errors (see api/errors/errors.go as well as go-restful router):
	// http.StatusNotFound, http.StatusMethodNotAllowed,
	// http.StatusUnsupportedMediaType, http.StatusNotAcceptable,
	// http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
	// http.StatusRequestTimeout, http.StatusConflict, http.StatusPreconditionFailed,
	// 422 (StatusUnprocessableEntity), http.StatusInternalServerError,
	// http.StatusServiceUnavailable
	// and api error codes
	// Note that if we specify a versioned Status object here, we may need to
	// create one for the tests, also
	// Success:
	// http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent
	//
	// test/integration/auth_test.go is currently the most comprehensive status code test

	mediaTypes, streamMediaTypes := mediaTypesForSerializer(a.group.Serializer)
	allMediaTypes := append(mediaTypes, streamMediaTypes...)
	ws.Produces(allMediaTypes...)

	reqScope := RequestScope{
		ContextFunc:    ctxFn,
		Serializer:     a.group.Serializer,
		ParameterCodec: a.group.ParameterCodec,
		Creater:        a.group.Creater,
		Convertor:      a.group.Convertor,
		Copier:         a.group.Copier,

		// TODO: This seems wrong for cross-group subresources. It makes an assumption that a subresource and its parent are in the same group version. Revisit this.
		Resource:    a.group.GroupVersion.WithResource(resource),
		Subresource: subresource,
		Kind:        fqKindToRegister,
	}
	/*
		在前面生成actions存储着该资源的所有动作(每次只有一个资源)

		根据之前生成的actions,进行遍历
		然后在WebService中添加指定的route
	*/
	/*
		此时的prefix是:  /api/v1
		**actions length is:*** 1
		actions content is: [{POST namespaces/{namespace}/bindings [0xc420447f60] {0x4c144a0 {} 0x830830 false} false}]
		**actions length is:*** 2
		actions content is: [{LIST componentstatuses [] {0x4c144e0 {} /api/v1/componentstatuses } false} {GET componentstatuses/{name} [0xc420447f90] {0x4c144e0 {} /api/v1/componentstatuses } false}]
		**actions length is:*** 1
		[{POST namespaces/{namespace}/pods/{name}/binding [0xc4200259c0 0xc4200259b0] {0x4c134a0 {} 0x8303e0 false} false}]
	*/

	for _, action := range actions {
		versionedObject := storageMeta.ProducesObject(action.Verb)
		if versionedObject == nil {
			versionedObject = defaultVersionedObject
		}
		reqScope.Namer = action.Namer
		namespaced := ""
		if apiResource.Namespaced {
			namespaced = "Namespaced"
		}
		operationSuffix := ""
		if strings.HasSuffix(action.Path, "/{path:*}") {
			operationSuffix = operationSuffix + "WithPath"
		}
		if action.AllNamespaces {
			operationSuffix = operationSuffix + "ForAllNamespaces"
			namespaced = ""
		}

		/*
			根据action的动作类型
			生成响应的handler，创建route添加到WebService中
			注意该动作是针对一个resource还是所有的resources？？？
		*/
		switch action.Verb {
		case "GET": // Get a resource.
			var handler restful.RouteFunction
			/*
				判断是否有参数，以决定handler函数
			*/
			if isGetterWithOptions {
				handler = GetResourceWithOptions(getterWithOptions, reqScope)
			} else {
				handler = GetResource(getter, exporter, reqScope)
			}
			/*
				生成处理函数
			*/
			handler = metrics.InstrumentRouteFunc(action.Verb, resource, handler)
			doc := "read the specified " + kind
			if hasSubresource {
				doc = "read " + subresource + " of the specified " + kind
			}
			route := ws.GET(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("read"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Returns(http.StatusOK, "OK", versionedObject).
				Writes(versionedObject)
			if isGetterWithOptions {
				if err := addObjectParams(ws, route, versionedGetOptions); err != nil {
					return nil, err
				}
			}
			if isExporter {
				if err := addObjectParams(ws, route, versionedExportOptions); err != nil {
					return nil, err
				}
			}
			addParams(route, action.Params)
			ws.Route(route)
		case "LIST": // List all resources of a kind.
			doc := "list objects of kind " + kind
			if hasSubresource {
				doc = "list " + subresource + " of objects of kind " + kind
			}
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, ListResource(lister, watcher, reqScope, false, a.minRequestTimeout))
			route := ws.GET(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("list"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), allMediaTypes...)...).
				Returns(http.StatusOK, "OK", versionedList).
				Writes(versionedList)
			if err := addObjectParams(ws, route, versionedListOptions); err != nil {
				return nil, err
			}
			switch {
			case isLister && isWatcher:
				doc := "list or watch objects of kind " + kind
				if hasSubresource {
					doc = "list or watch " + subresource + " of objects of kind " + kind
				}
				route.Doc(doc)
			case isWatcher:
				doc := "watch objects of kind " + kind
				if hasSubresource {
					doc = "watch " + subresource + "of objects of kind " + kind
				}
				route.Doc(doc)
			}
			addParams(route, action.Params)
			ws.Route(route)
		case "PUT": // Update a resource.
			doc := "replace the specified " + kind
			if hasSubresource {
				doc = "replace " + subresource + " of the specified " + kind
			}
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, UpdateResource(updater, reqScope, a.group.Typer, admit))
			route := ws.PUT(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("replace"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Returns(http.StatusOK, "OK", versionedObject).
				Reads(versionedObject).
				Writes(versionedObject)
			addParams(route, action.Params)
			ws.Route(route)
		case "PATCH": // Partially update a resource
			//部分更新一个resource
			doc := "partially update the specified " + kind
			if hasSubresource {
				doc = "partially update " + subresource + " of the specified " + kind
			}
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, PatchResource(patcher, reqScope, a.group.Typer, admit, mapping.ObjectConvertor))
			route := ws.PATCH(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Consumes(string(api.JSONPatchType), string(api.MergePatchType), string(api.StrategicMergePatchType)).
				Operation("patch"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Returns(http.StatusOK, "OK", versionedObject).
				Reads(unversioned.Patch{}).
				Writes(versionedObject)
			addParams(route, action.Params)
			ws.Route(route)
		case "POST": // Create a resource.
			var handler restful.RouteFunction
			if isNamedCreater {
				handler = CreateNamedResource(namedCreater, reqScope, a.group.Typer, admit)
			} else {
				handler = CreateResource(creater, reqScope, a.group.Typer, admit)
			}
			handler = metrics.InstrumentRouteFunc(action.Verb, resource, handler)
			article := utilstrings.GetArticleForNoun(kind, " ")
			doc := "create" + article + kind
			if hasSubresource {
				doc = "create " + subresource + " of" + article + kind
			}
			route := ws.POST(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("create"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Returns(http.StatusOK, "OK", versionedObject).
				Reads(versionedObject).
				Writes(versionedObject)
			addParams(route, action.Params)
			ws.Route(route)
		case "DELETE": // Delete a resource.
			article := utilstrings.GetArticleForNoun(kind, " ")
			doc := "delete" + article + kind
			if hasSubresource {
				doc = "delete " + subresource + " of" + article + kind
			}
			/*
				  eg:
					doc is 是:  delete a Job
					resource is: jobs
			*/
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, DeleteResource(gracefulDeleter, isGracefulDeleter, reqScope, admit))
			route := ws.DELETE(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("delete"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Writes(versionedStatus).
				Returns(http.StatusOK, "OK", versionedStatus)
			if isGracefulDeleter {
				route.Reads(versionedDeleterObject)
				if err := addObjectParams(ws, route, versionedDeleteOptions); err != nil {
					return nil, err
				}
			}
			addParams(route, action.Params)
			ws.Route(route)
		case "DELETECOLLECTION":
			doc := "delete collection of " + kind
			if hasSubresource {
				doc = "delete collection of " + subresource + " of a " + kind
			}
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, DeleteCollection(collectionDeleter, isCollectionDeleter, reqScope, admit))
			route := ws.DELETE(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("deletecollection"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(append(storageMeta.ProducesMIMETypes(action.Verb), mediaTypes...)...).
				Writes(versionedStatus).
				Returns(http.StatusOK, "OK", versionedStatus)
			if err := addObjectParams(ws, route, versionedListOptions); err != nil {
				return nil, err
			}
			addParams(route, action.Params)
			ws.Route(route)
		// TODO: deprecated
		case "WATCH": // Watch a resource.
			doc := "watch changes to an object of kind " + kind
			if hasSubresource {
				doc = "watch changes to " + subresource + " of an object of kind " + kind
			}
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, ListResource(lister, watcher, reqScope, true, a.minRequestTimeout))
			route := ws.GET(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("watch"+namespaced+kind+strings.Title(subresource)+operationSuffix).
				Produces(allMediaTypes...).
				Returns(http.StatusOK, "OK", versionedWatchEvent).
				Writes(versionedWatchEvent)
			if err := addObjectParams(ws, route, versionedListOptions); err != nil {
				return nil, err
			}
			addParams(route, action.Params)
			ws.Route(route)
		// TODO: deprecated
		case "WATCHLIST": // Watch all resources of a kind.
			doc := "watch individual changes to a list of " + kind
			if hasSubresource {
				doc = "watch individual changes to a list of " + subresource + " of " + kind
			}
			/*
				构造handler函数，重要的部分是ListResource(lister, watcher, reqScope, true, a.minRequestTimeout)函数
			*/
			handler := metrics.InstrumentRouteFunc(action.Verb, resource, ListResource(lister, watcher, reqScope, true, a.minRequestTimeout))
			route := ws.GET(action.Path).To(handler).
				Doc(doc).
				Param(ws.QueryParameter("pretty", "If 'true', then the output is pretty printed.")).
				Operation("watch"+namespaced+kind+strings.Title(subresource)+"List"+operationSuffix).
				Produces(allMediaTypes...).
				Returns(http.StatusOK, "OK", versionedWatchEvent).
				Writes(versionedWatchEvent)
			if err := addObjectParams(ws, route, versionedListOptions); err != nil {
				return nil, err
			}
			addParams(route, action.Params)
			ws.Route(route)
		// We add "proxy" subresource to remove the need for the generic top level prefix proxy.
		// The generic top level prefix proxy is deprecated in v1.2, and will be removed in 1.3, or 1.4 at the latest.
		// TODO: DEPRECATED in v1.2.
		case "PROXY": // Proxy requests to a resource.
			// Accept all methods as per http://issue.k8s.io/3996
			addProxyRoute(ws, "GET", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
			addProxyRoute(ws, "PUT", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
			addProxyRoute(ws, "POST", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
			addProxyRoute(ws, "DELETE", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
			addProxyRoute(ws, "HEAD", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
			addProxyRoute(ws, "OPTIONS", a.prefix, action.Path, proxyHandler, namespaced, kind, resource, subresource, hasSubresource, action.Params, operationSuffix)
		case "CONNECT":
			for _, method := range connecter.ConnectMethods() {
				doc := "connect " + method + " requests to " + kind
				if hasSubresource {
					doc = "connect " + method + " requests to " + subresource + " of " + kind
				}
				handler := metrics.InstrumentRouteFunc(action.Verb, resource, ConnectResource(connecter, reqScope, admit, path))
				route := ws.Method(method).Path(action.Path).
					To(handler).
					Doc(doc).
					Operation("connect" + strings.Title(strings.ToLower(method)) + namespaced + kind + strings.Title(subresource) + operationSuffix).
					Produces("*/*").
					Consumes("*/*").
					Writes("string")
				if versionedConnectOptions != nil {
					if err := addObjectParams(ws, route, versionedConnectOptions); err != nil {
						return nil, err
					}
				}
				addParams(route, action.Params)
				ws.Route(route)
			}
		default:
			return nil, fmt.Errorf("unrecognized action verb: %s", action.Verb)
		}
		// Note: update GetAttribs() when adding a custom handler.
	}
	return &apiResource, nil
}
```

## 总结
本文主要讲解了如何把所有GroupVersion中定义的资源生成Restful API，主要是根据go－restful的流程。中间穿插着APIGroupVersion、APIGroupInfo、Scheme、GroupMeta、RESTMapper、APIRegistrationManager几个数据结构的使用。搞清楚这几个数据结构的脉络关系，基本就了解这个注册过程。

关于/api和/apis的区别其实并不大，注册过程都是大同小异。

后续将要讲解里面提到的podStorage是怎么实现？同时怎么查看一个资源都实现哪些接口函数？以便在类型断言的时候识别出来。