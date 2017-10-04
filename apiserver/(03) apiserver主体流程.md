# Apiserver主体流程

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [main函数](#main函数)
  - [参数设置](#参数设置)
  - [Run Apiserver](#run-apiserver)
  - [New一个master](#new一个master)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

代码中有些注释可能过于细节，如果还不了解可以先忽略。
力求先对Apiserver的运转流程有个整体概念。

## main函数
直接上代码
```go
func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	/*
		NewServerRunOptions新建一个ServerRunOptions对象
		完成apiServer的运行参数初始化关键性步骤：
			==>通过两个同名NewServerRunOptions()函数构建一个默认的ServerRunOptions对象

		至于后面的s.AddFlags(pflag.CommandLine)就是获取命令行的输入信息，然后对s进行重新覆盖
	*/
	s := options.NewServerRunOptions()
	s.AddFlags(pflag.CommandLine) // 接受用户命令行输入，其实就是自定义上述ServerRunOptions对象

	// 解析并格式化用户传入的参数，最后填充ServerRunOptions结构体的各成员
	flag.InitFlags()
	// 初始化log配置，包括log输出位置、log等级等。
	logs.InitLogs()
	// 保证了即使apiserver异常崩溃了也能将内存中的log信息保存到磁盘文件中。
	defer logs.FlushLogs()

	// 如果用户只是想看apiserver的版本号而不是启动apiserver，则打印apiserver的版本号并退出。
	verflag.PrintAndExitIfRequested()

	/*
		将创建的ServerRunOptions对象传入app.Run()中，
		最终绑定本地端口并绑定本地端口
		并创建一个HTTP Server与一个HTTPS Server。

		初始化完成之后，最重要的任务就是启动实例了。
		所有的操作都是在Run函数中执行，app.Run(s)接口实现在cmd/kube-apiserver/app/server.go。
	*/
	if err := app.Run(s); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
```
可以看出  
1. main函数会首先根据相关的参数构建一个默认的ServerRunOptions参数对象s，其中`type ServerRunOptions struct`包含了运行一个通用的api server所需的参数。
2. 然后通过`s.AddFlags(pflag.CommandLine)`获取命令行的输入信息，对s的值进行重新覆盖。
3. 最后执行`Run(s)`。

## 参数设置
查看`ServerRunOptions`的构建过程，如下，/cmd/kube-apiserver/app/options/options.go
```go
// ServerRunOptions runs a kubernetes api server.
type ServerRunOptions struct {
	GenericServerRunOptions  *genericoptions.ServerRunOptions  //服务器通用的运行参数
	AllowPrivileged          bool                              // 是否配置特权模式，即允许Pod中运行的容器拥有系统特权
	EventTTL                 time.Duration                     // 事件留存时间，默认1h
	KubeletConfig            kubeletclient.KubeletClientConfig // K8S kubelet配置
	MaxConnectionBytesPerSec int64                             //每秒最大连接数
	SSHKeyfile               string                            //指定的话，可以通过SSH指定的秘钥文件和用户名对Node进行访问
	SSHUser                  string
	/*
		ServiceAccountKeyFiles，包含PEM-encoded x509 RSA公钥和私钥的文件路径，用于验证Service Account的token
		如不指定的话，则使用--tls-private-key-file指定的文件

		ServiceAccountLookup，设置为true时，系统会到etcd验证ServiceAccount token是否存在
	*/
	ServiceAccountKeyFiles      []string
	ServiceAccountLookup        bool
	WebhookTokenAuthnConfigFile string
	WebhookTokenAuthnCacheTTL   time.Duration
}

// NewServerRunOptions creates a new ServerRunOptions object with default parameters
func NewServerRunOptions() *ServerRunOptions {
	s := ServerRunOptions{
		/*
			调用了genericoptions.NewServerRunOptions().WithEtcdOptions()接口
			genericoptions.NewServerRunOptions()定义在
			==>/pkg/genericapiserver/options/server_run_options.go
			主要是设置了type ServerRunOptions struct 的参数
			初始化通用的apiserver运行参数，包括etcd后端存储参数

			后端存储etcd的配置初始化WithEtcdOptions()
			==>func (o *ServerRunOptions) WithEtcdOptions() *ServerRunOptions
		*/
		GenericServerRunOptions: genericoptions.NewServerRunOptions().WithEtcdOptions(),
		//事件的存储保留时间
		EventTTL: 1 * time.Hour,
		//Node上kubelet的客户端配置
		KubeletConfig: kubeletclient.KubeletClientConfig{
			Port: ports.KubeletPort,
			PreferredAddressTypes: []string{
				string(api.NodeHostName),
				string(api.NodeInternalIP),
				string(api.NodeExternalIP),
				string(api.NodeLegacyHostIP),
			},
			// 是否开启https
			EnableHttps: true,
			// HTTP超时
			HTTPTimeout: time.Duration(5) * time.Second,
		},
		// 将webhook token authenticator返回的响应保存在缓存内的时间
		WebhookTokenAuthnCacheTTL: 2 * time.Minute,
	}
	return &s
}
```
继续查看genericoptions.NewServerRunOptions()，/pkg/genericapiserver/options/server_run_options.go。
```go
type ServerRunOptions struct {
	/*
		AdmissionControl，准入控制，如："AlwaysAdmit","LimitRanger","ReousrceQuota"“NamespaceExists”等
	*/
	AdmissionControl           string
	AdmissionControlConfigFile string //准入控制的配置文件
	/*
		AdvertiseAddress，用于广播给集群的所有成员自己的IP地址，不指定的话就使用"--bind-address"的IP地址
	*/
	AdvertiseAddress net.IP

	// Authorization mode and associated flags.
	/*
		AuthorizationMode，授权。
		安全访问的认证模式列表,以逗号分隔，
		包括：AlwaysAllow、AlwaysDeny、ABAC、Webhook、RBAC
	*/
	AuthorizationMode       string
	AuthorizationPolicyFile string //mode设置为ABAC时使用的csv格式的授权配置文件
	/*
		下列三个跟mode配置成webhook有关
	*/
	AuthorizationWebhookConfigFile           string
	AuthorizationWebhookCacheAuthorizedTTL   time.Duration
	AuthorizationWebhookCacheUnauthorizedTTL time.Duration
	/*
		mode设置为RBAC时使用的超级用户名，用该用户名进行RBAC认证
	*/
	AuthorizationRBACSuperUser string

	AnonymousAuth bool
	/*
		HTTP Base基本认证方式：API Server启动参数 –basic-auth-file
		指向存储用户名和密码信息的基本认证文件
		（包含3列的csv文件，第一列为密码，第二列为用户名，第三列为用户UID）；
		HTTP请求头中的Authorization域需包含相关认证信息。
	*/
	BasicAuthFile string
	/*
		默认"0.0.0.0"，apiServer在该地址的6443端口上开启https服务
	*/
	BindAddress net.IP
	/*
		TLS证书所在目录，默认"/var/run/kubernetes"
	*/
	CertDirectory         string
	ClientCAFile          string //指定的话，该客户端证书将用于认证过程
	CloudConfigFile       string //和云服务商有关
	CloudProvider         string
	CorsAllowedOriginList []string //CORS 跨域资源共享
	/*
		DefaultStorageMediaType，默认的持久化存储格式，比如"application/json"
	*/
	DefaultStorageMediaType string
	/*
		指定清理的工作线程数，可以提高清理namespace的效率，但是会增加系统资源的占用
	*/
	DeleteCollectionWorkers int
	/*
		Audit日志相关策略
	*/
	AuditLogPath            string
	AuditLogMaxAge          int
	AuditLogMaxBackups      int
	AuditLogMaxSize         int
	EnableGarbageCollection bool //GC
	/*
		打开性能分析，可以通过<host>:<port>/debug/pprof/地址来查看程序栈，线程等信息
	*/
	EnableProfiling           bool
	EnableContentionProfiling bool
	/*
		使用swaggerUI,访问地址<host>:<port>/swagger-ui
	*/
	EnableSwaggerUI bool
	/*
		使用watch cache，对所有的watch操作进行缓存
	*/
	EnableWatchCache bool
	/*
		按资源覆盖etcd服务的设置，以逗号分隔，比如group/resource#servers,其中servers为: http://ip:port
	*/
	EtcdServersOverrides []string
	StorageConfig        storagebackend.Config
	/*
		用于生成该master对外的URL地址
	*/
	ExternalHost string
	/*
		绑定的不安全地址，即8080端口绑定的地址
	*/
	InsecureBindAddress net.IP
	InsecurePort        int
	/*
		设置keystone鉴权插件地址
	*/
	KeystoneURL               string
	KeystoneCAFile            string
	KubernetesServiceNodePort int
	LongRunningRequestRE      string
	MasterCount               int    //master数量
	MasterServiceNamespace    string //设置master服务所在的namespace,默认为default
	/*
		同时处理的最大请求数，默认为400，超过该请求数将被拒绝。仅用于长时间执行的请求
	*/
	MaxRequestsInFlight int
	/*
		最小请求处理超时时间，默认1800s,仅用于watch request
	*/
	MinRequestTimeout int
	/*
		OIDC 该文件内设置鉴权机构
	*/
	OIDCCAFile                   string
	OIDCClientID                 string
	OIDCIssuerURL                string
	OIDCUsernameClaim            string
	OIDCGroupsClaim              string
	RequestHeaderUsernameHeaders []string
	RequestHeaderClientCAFile    string
	RequestHeaderAllowedNames    []string
	/*
		RuntimeConfig:
			一组key=value用于运行时的配置信息。
			api/<groupVersion>/<resource>,用于打开或者关闭对某个API版本的支持.
			api/all和api/legacy特别用于支持所有版本的API或支持旧版本的API.

		用法：--runtime-config： 用于enable/disable extensions group。
			默认的情况下DaemonSets、Deployments、HorizontalPodAutoscalers、Ingress、Jobs和ReplicaSets都是enabled的,
			还有v1下的默认都是enabled的。
			另外的功能就可以通过该配置进行设置.
			例如：disable deployments：
					--runtime-config=extensions/v1beta1/deployments=false.
	*/
	RuntimeConfig config.ConfigurationMap
	/*
		https安全端口，默认6443；设置为0，表示不开启https
	*/
	SecurePort int
	//service的Cluster IP池
	ServiceClusterIPRange net.IPNet // TODO: make this a list
	//service的NodePort模式下能使用的主机端口号范围，默认是30000--32767
	ServiceNodePortRange utilnet.PortRange
	/*
		StorageVersions:
		持久化存储的资源版本号，例如"group1/version1,group2/version2,..."
	*/
	StorageVersions string
	// The default values for StorageVersions. StorageVersions overrides
	// these; you can change this if you want to change the defaults (e.g.,
	// for testing). This is not actually exposed as a flag.
	DefaultStorageVersions string //会被StorageVersions重写，本参数并不对外公开
	TargetRAMMB            int
	TLSCAFile              string //TLS CA文件
	TLSCertFile            string //包含x509证书的文件路径，用于https认证
	TLSPrivateKeyFile      string // 包含x509与tls-cert-file对应的私钥文件路径
	SNICertKeys            []config.NamedCertKey
	TokenAuthFile          string // 用于访问APIServer安全端口的token认证文件路径
	EnableAnyToken         bool   // 使用token
	/*
		设置各资源对象watch缓存大小的列表，以逗号分隔，格式为resource#size
		前提是EnableWatchCache为true
	*/
	WatchCacheSizes []string
}

func NewServerRunOptions() *ServerRunOptions {
	/*
		初始化的时候会有SecurePort、InsecurePort，实际就是对应HTTPS、HTTP的绑定端口。
		这里的控制策略包括:
		安全控制(CertDirectory, HTTPS默认启动)、
		权限控制(AdmissionControl,AuthorizationMode)、
		服务限流控制(MaxRequestsInFlight)等。
		具体的参数上面介绍结构体type ServerRunOptions基本都有提到。
	*/
	return &ServerRunOptions{
		//以逗号作为分隔符的Admission Control插件的排序列表
		AdmissionControl: "AlwaysAdmit",
		AnonymousAuth:    false,
		// 授权模式
		AuthorizationMode:                        "AlwaysAllow",
		AuthorizationWebhookCacheAuthorizedTTL:   5 * time.Minute,
		AuthorizationWebhookCacheUnauthorizedTTL: 30 * time.Second,
		// apiserver绑定的网卡地址
		BindAddress: net.ParseIP("0.0.0.0"),
		// 证书目录
		CertDirectory: "/var/run/kubernetes",
		// 默认持久化存储格式，即以json格式存储在etcd中
		DefaultStorageMediaType: "application/json",
		/*
			registered.AllPreferredGroupVersions(),通过函数面值来调用，定义在
			==>/pkg/apimachinery/registered/registered.go
				==>AllPreferredGroupVersions     = DefaultAPIRegistrationManager.AllPreferredGroupVersions
			从这里去延伸考虑整个流程是在哪里对groupMeta进行register & enable的？？？？？？
		*/
		DefaultStorageVersions:    registered.AllPreferredGroupVersions(),
		DeleteCollectionWorkers:   1,
		EnableGarbageCollection:   true,
		EnableProfiling:           true,
		EnableContentionProfiling: false,
		EnableWatchCache:          true,
		// HTTP绑定的IP地址和端口8080
		InsecureBindAddress:  net.ParseIP("127.0.0.1"),
		InsecurePort:         8080,
		LongRunningRequestRE: DefaultLongRunningRequestRE,
		// Kubernetes系统中Master的数量
		MasterCount:            1,
		MasterServiceNamespace: api.NamespaceDefault,
		MaxRequestsInFlight:    400,
		MinRequestTimeout:      1800,
		// k8s运行时环境配置
		RuntimeConfig: make(config.ConfigurationMap),
		// 安全端口
		SecurePort:           6443,
		ServiceNodePortRange: DefaultServiceNodePortRange,
		StorageVersions:      registered.AllPreferredGroupVersions(),
	}
}

func (o *ServerRunOptions) WithEtcdOptions() *ServerRunOptions {
	o.StorageConfig = storagebackend.Config{
		/*
			etcd的默认路径前缀：/registry
		*/
		Prefix: DefaultEtcdPathPrefix,
		// Default cache size to 0 - if unset, its size will be set based on target
		// memory usage.
		/*
			反序列化cache，未设置的话，会根据apiServer的内存限制进行配置
		*/
		DeserializationCacheSize: 0,
	}
	return o
}
```
在这里的会生成一个`DefaultStorageVersions`，涉及到k8s里面最顶层的概念设计：`group、restmapper、scheme...`这些概念。
后面将做详细的介绍，这里先略过。

至此，main()函数已经介绍完毕，下面进入`app.Run(s)`。

## Run Apiserver
app.Run(s)接口实现在cmd/kube-apiserver/app/server.go。
```go
// Run runs the specified APIServer.  This should never exit.
/*
	译：func Run(s *options.ServerRunOptions) 让apiserver跑起来，永远不会退出

	该接口调用主要用于生成master实例对象，
	各种api的请求最后都是通过master对象来处理的。
	在最后APIServer会启动HTTP/HTTPS服务。
*/
func Run(s *options.ServerRunOptions) error {
	// 检查etcd后端存储相关参数的有效性
	genericvalidation.VerifyEtcdServersList(s.GenericServerRunOptions)
	/*
		检查一些运行参数的有效性，并会设置一些默认值。
		比如options.AdvertiseAddress参数没有设置，并且bind-address也没有设置，
		k8s将会获取默认网卡的地址给该成员
	*/
	genericapiserver.DefaultAndValidateRunOptions(s.GenericServerRunOptions)
	/*
		根据之前初始化的GenericServerRunOptions对象来初始化创建genericapiserver.config
		NewConfig()是初始化了一个默认的config，
		ApplyOptions()根据GenericServerRunOptions进行再一遍的初始化
		Complete()对一些没填充的字段，可以根据别的字段进行初始化
		实际NewConfig()中也调用了ApplyOptions()接口，只是参数是default值
	*/
	genericConfig := genericapiserver.NewConfig(). // create the new config
							ApplyOptions(s.GenericServerRunOptions). // apply the options selected
							Complete()                               // set default values based on the known values

	// 根据ServiceClusterIPRange输入参数，获取IPRange和ServiceIP
	serviceIPRange, apiServerServiceIP, err := genericapiserver.DefaultServiceIPRange(s.GenericServerRunOptions.ServiceClusterIPRange)
	if err != nil {
		glog.Fatalf("Error determining service IP ranges: %v", err)
	}
	// 有需要的话生成证书
	if err := genericConfig.MaybeGenerateServingCerts(apiServerServiceIP); err != nil {
		glog.Fatalf("Failed to generate service certificate: %v", err)
	}

	/*
		定义在/pkg/capabilities/capabilities.go中的func Initialize(c Capabilities)
		初始化capability集合
		只能对每个二进制执行一次，后续调用将被忽略。（apiserver和kubelet都调用了）

		apiserver和kubelet都有一个allow-privileged参数，
		两者冲突时，咋整？？？？（apiserver管理全局？kubelet管理本节点？）

		kubelet的参数使用是在/cmd/kubelet/app/server.go中的
		==>capabilities.Setup(kubeCfg.AllowPrivileged, privilegedSources, 0)
	*/
	capabilities.Initialize(capabilities.Capabilities{
		// 是否有超级权限
		AllowPrivileged: s.AllowPrivileged,
		// TODO(vmarmol): Implement support for HostNetworkSources.
		PrivilegedSources: capabilities.PrivilegedSources{
			HostNetworkSources: []string{},
			HostPIDSources:     []string{},
			HostIPCSources:     []string{},
		},
		// 每个用户连接的最大值，字节数/秒。当前只适用于长时间运行的请求
		PerConnectionBandwidthLimitBytesPerSec: s.MaxConnectionBytesPerSec,
	})

	// Setup tunneler if needed
	// 有需要的话设置网络隧道
	var tunneler genericapiserver.Tunneler
	var proxyDialerFn apiserver.ProxyDialerFunc
	/*
		如果运行在云平台中，则需要安装本机的SSH Key到Kubernetes集群中所有节点上
		可以用于通过该用户名和私钥，SSH到node上
	*/
	if len(s.SSHUser) > 0 {
		......
		......
		......
	}

	// Proxying to pods and services is IP-based... don't expect to be able to verify the hostname
	proxyTLSClientConfig := &tls.Config{InsecureSkipVerify: true}

	/*
		   	后端存储etcd的反序列化缓存，
			如果没有设置的话，会根据Target 的RAMMB值进行恰当的设置
			TargetRAMMB：用户手动输入的apiServer的内存限制(单位：MB)
			小于1000MB的话按1000MB算
			默认值是0，也就是说没有设置
	*/
	if s.GenericServerRunOptions.StorageConfig.DeserializationCacheSize == 0 {
		// When size of cache is not explicitly set, estimate its size based on
		// target memory usage.
		/*
			当高速缓存的大小没有明确设置时，根据目标内存使用情况估算其大小。
			Initalizing deserialization cache size based on 0MB limit
		*/
		glog.V(2).Infof("Initalizing deserialization cache size based on %dMB limit", s.GenericServerRunOptions.TargetRAMMB)

		// This is the heuristics that from memory capacity is trying to infer
		// the maximum number of nodes in the cluster and set cache sizes based
		// on that value.
		// From our documentation, we officially recomment 120GB machines for
		// 2000 nodes, and we scale from that point. Thus we assume ~60MB of
		// capacity per node.
		/*
			译：这是从内存容量试图推断集群中最大节点数，并根据该值设置高速缓存大小的启发式方法。
			   根据我们的文档，我们推荐2000个节点的集群为120GB，
			   我们从这种条件开始扩展。 因此，我们假设每个节点的容量大约为60MB。
		*/
		// TODO: We may consider deciding that some percentage of memory will
		// be used for the deserialization cache and divide it by the max object
		// size to compute its size. We may even go further and measure
		// collective sizes of the objects in the cache.
		clusterSize := s.GenericServerRunOptions.TargetRAMMB / 60
		s.GenericServerRunOptions.StorageConfig.DeserializationCacheSize = 25 * clusterSize
		if s.GenericServerRunOptions.StorageConfig.DeserializationCacheSize < 1000 {
			s.GenericServerRunOptions.StorageConfig.DeserializationCacheSize = 1000
		}
	}

	/*
		存储组版本
		调用定义在/pkg/genericapiserver/options/server_run_options.go
			==>func (s *ServerRunOptions) StorageGroupsToEncodingVersion()
		获取从group name 到 group version的映射
	*/
	storageGroupsToEncodingVersion, err := s.GenericServerRunOptions.StorageGroupsToEncodingVersion()
	glog.V(0).Infof("storageGroupsToEncodingVersion is %s", storageGroupsToEncodingVersion)
	if err != nil {
		glog.Fatalf("error generating storage version map: %s", err)
	}
	/*
		创建api工厂，包括请求头、解析工具、编码格式、API配置
		创建了一个DefaultStorageFactory对象
		==>/pkg/genericapiserver/default_storage_factory_builder.go
			==>func BuildDefaultStorageFactory
		etcd存储的资源前缀也在这里设置
	*/
	storageFactory, err := genericapiserver.BuildDefaultStorageFactory(
		s.GenericServerRunOptions.StorageConfig, s.GenericServerRunOptions.DefaultStorageMediaType, api.Codecs,
		genericapiserver.NewDefaultResourceEncodingConfig(), storageGroupsToEncodingVersion,
		// FIXME: this GroupVersionResource override should be configurable
		[]unversioned.GroupVersionResource{batch.Resource("cronjobs").WithVersion("v2alpha1")},
		master.DefaultAPIResourceConfigSource(), s.GenericServerRunOptions.RuntimeConfig)
	if err != nil {
		glog.Fatalf("error in initializing storage factory: %s", err)
	}
	/*
		添加jobs和HPA(水平自动扩容)的接口
	*/
	storageFactory.AddCohabitatingResources(batch.Resource("jobs"), extensions.Resource("jobs"))
	storageFactory.AddCohabitatingResources(autoscaling.Resource("horizontalpodautoscalers"), extensions.Resource("horizontalpodautoscalers"))
	/*
		根据用户输入的etcd-servers-overrides参数，设置对应groupResource对应的etcd地址
	*/
	for _, override := range s.GenericServerRunOptions.EtcdServersOverrides {
		tokens := strings.Split(override, "#")
		if len(tokens) != 2 {
			glog.Errorf("invalid value of etcd server overrides: %s", override)
			continue
		}

		apiresource := strings.Split(tokens[0], "/")
		if len(apiresource) != 2 {
			glog.Errorf("invalid resource definition: %s", tokens[0])
			continue
		}
		group := apiresource[0]
		resource := apiresource[1]
		groupResource := unversioned.GroupResource{Group: group, Resource: resource}

		servers := strings.Split(tokens[1], ";")
		/*
			本for循环体的以上部分都是 解析用户输入的字符串，并生成对应的groupResource
		*/
		/*
			SetEtcdLocation，设置对应groupResource的etcdLocation
		*/
		storageFactory.SetEtcdLocation(groupResource, servers)
	}

	// Default to the private server key for service account token signing
	/*
		授权认证有关
	*/
	if len(s.ServiceAccountKeyFiles) == 0 && s.GenericServerRunOptions.TLSPrivateKeyFile != "" {
		if authenticator.IsValidServiceAccountKeyFile(s.GenericServerRunOptions.TLSPrivateKeyFile) {
			s.ServiceAccountKeyFiles = []string{s.GenericServerRunOptions.TLSPrivateKeyFile}
		} else {
			glog.Warning("No TLS key provided, service account token authentication disabled")
		}
	}

	var serviceAccountGetter serviceaccount.ServiceAccountTokenGetter
	/*
		判断是否设置为true，是的话则创建接口用于从etcd验证ServiceAccount token是否存在
	*/
	if s.ServiceAccountLookup {
		// If we need to look up service accounts and tokens,
		// go directly to etcd to avoid recursive auth insanity
		storageConfig, err := storageFactory.NewConfig(api.Resource("serviceaccounts"))
		if err != nil {
			glog.Fatalf("Unable to get serviceaccounts storage: %v", err)
		}
		serviceAccountGetter = serviceaccountcontroller.NewGetterFromStorageInterface(storageConfig, storageFactory.ResourcePrefix(api.Resource("serviceaccounts")), storageFactory.ResourcePrefix(api.Resource("secrets")))
	}

	/*
		安全认证相关
	*/
	apiAuthenticator, securityDefinitions, err := authenticator.New(authenticator.AuthenticatorConfig{
		Anonymous: s.GenericServerRunOptions.AnonymousAuth,
		AnyToken:  s.GenericServerRunOptions.EnableAnyToken,
		/*
			BasicAuthFile,指定basicauthfile文件所在的位置，
			当这个参数不为空的时候,会开启basicauth的认证方式，这是一个.csv文件，
			三列分别是password，username,useruid
		*/
		BasicAuthFile: s.GenericServerRunOptions.BasicAuthFile,
		/*
			ClientCAFile,用于给客户端签名的根证书，
			当这个参数不为空的时候,会开启https的认证方式，
			会通过这个根证书对客户端的证书进行身份认证
		*/
		ClientCAFile: s.GenericServerRunOptions.ClientCAFile,
		/*
			TokenAuthFile,用于Token文件所在的位置，
			当这个参数不为空的时候，会采用token的认证方式，
			token文件也是csv的格式，分别是“token,username,userid”
		*/
		TokenAuthFile:     s.GenericServerRunOptions.TokenAuthFile,
		OIDCIssuerURL:     s.GenericServerRunOptions.OIDCIssuerURL,
		OIDCClientID:      s.GenericServerRunOptions.OIDCClientID,
		OIDCCAFile:        s.GenericServerRunOptions.OIDCCAFile,
		OIDCUsernameClaim: s.GenericServerRunOptions.OIDCUsernameClaim,
		OIDCGroupsClaim:   s.GenericServerRunOptions.OIDCGroupsClaim,
		/*
			ServiceAccountKeyFiles,
			当不为空的时候，采用ServiceAccount的认证方式，这其实是一个公钥方式。
			发过来的信息是客户端使用对应的私钥加密，服务端使用指定的公钥来解密信息
		*/
		ServiceAccountKeyFiles: s.ServiceAccountKeyFiles,
		/*
			ServiceAccountLookup,默认为false。
			如果为true的话，就会从etcd中取出对应的ServiceAccount与
			传过来的信息进行对比验证，反之不会
		*/
		ServiceAccountLookup:        s.ServiceAccountLookup,
		ServiceAccountTokenGetter:   serviceAccountGetter,
		KeystoneURL:                 s.GenericServerRunOptions.KeystoneURL,
		KeystoneCAFile:              s.GenericServerRunOptions.KeystoneCAFile,
		WebhookTokenAuthnConfigFile: s.WebhookTokenAuthnConfigFile,
		WebhookTokenAuthnCacheTTL:   s.WebhookTokenAuthnCacheTTL,
		RequestHeaderConfig:         s.GenericServerRunOptions.AuthenticationRequestHeaderConfig(),
	})

	if err != nil {
		glog.Fatalf("Invalid Authentication Config: %v", err)
	}

	privilegedLoopbackToken := uuid.NewRandom().String()
	selfClientConfig, err := s.GenericServerRunOptions.NewSelfClientConfig(privilegedLoopbackToken)
	if err != nil {
		glog.Fatalf("Failed to create clientset: %v", err)
	}
	client, err := s.GenericServerRunOptions.NewSelfClient(privilegedLoopbackToken)
	if err != nil {
		glog.Errorf("Failed to create clientset: %v", err)
	}
	sharedInformers := informers.NewSharedInformerFactory(client, 10*time.Minute)

	authorizationConfig := authorizer.AuthorizationConfig{
		PolicyFile:                  s.GenericServerRunOptions.AuthorizationPolicyFile,
		WebhookConfigFile:           s.GenericServerRunOptions.AuthorizationWebhookConfigFile,
		WebhookCacheAuthorizedTTL:   s.GenericServerRunOptions.AuthorizationWebhookCacheAuthorizedTTL,
		WebhookCacheUnauthorizedTTL: s.GenericServerRunOptions.AuthorizationWebhookCacheUnauthorizedTTL,
		RBACSuperUser:               s.GenericServerRunOptions.AuthorizationRBACSuperUser,
		InformerFactory:             sharedInformers,
	}
	authorizationModeNames := strings.Split(s.GenericServerRunOptions.AuthorizationMode, ",")
	apiAuthorizer, err := authorizer.NewAuthorizerFromAuthorizationConfig(authorizationModeNames, authorizationConfig)
	if err != nil {
		glog.Fatalf("Invalid Authorization Config: %v", err)
	}

	admissionControlPluginNames := strings.Split(s.GenericServerRunOptions.AdmissionControl, ",")

	// TODO(dims): We probably need to add an option "EnableLoopbackToken"
	if apiAuthenticator != nil {
		var uid = uuid.NewRandom().String()
		tokens := make(map[string]*user.DefaultInfo)
		tokens[privilegedLoopbackToken] = &user.DefaultInfo{
			Name:   user.APIServerUser,
			UID:    uid,
			Groups: []string{user.SystemPrivilegedGroup},
		}

		tokenAuthenticator := authenticator.NewAuthenticatorFromTokens(tokens)
		apiAuthenticator = authenticatorunion.New(tokenAuthenticator, apiAuthenticator)

		tokenAuthorizer := authorizer.NewPrivilegedGroups(user.SystemPrivilegedGroup)
		apiAuthorizer = authorizerunion.New(tokenAuthorizer, apiAuthorizer)
	}

	pluginInitializer := admission.NewPluginInitializer(sharedInformers, apiAuthorizer)

	/*
		准入控制器 admissionController
	*/
	admissionController, err := admission.NewFromPlugins(client, admissionControlPluginNames, s.GenericServerRunOptions.AdmissionControlConfigFile, pluginInitializer)
	if err != nil {
		glog.Fatalf("Failed to initialize plugins: %v", err)
	}

	proxyTransport := utilnet.SetTransportDefaults(&http.Transport{
		Dial:            proxyDialerFn,
		TLSClientConfig: proxyTLSClientConfig,
	})
	kubeVersion := version.Get()

	/*
		genericConfig在该接口最开始进行了创建并初始化
	*/
	genericConfig.Version = &kubeVersion
	genericConfig.LoopbackClientConfig = selfClientConfig
	genericConfig.Authenticator = apiAuthenticator
	genericConfig.Authorizer = apiAuthorizer
	genericConfig.AdmissionControl = admissionController
	genericConfig.APIResourceConfigSource = storageFactory.APIResourceConfigSource
	genericConfig.OpenAPIConfig.Info.Title = "Kubernetes"
	genericConfig.OpenAPIConfig.Definitions = generatedopenapi.OpenAPIDefinitions
	genericConfig.EnableOpenAPISupport = true
	genericConfig.EnableMetrics = true
	genericConfig.OpenAPIConfig.SecurityDefinitions = securityDefinitions

	/*
		master.Config配置初始化
	*/
	config := &master.Config{
		GenericConfig: genericConfig.Config,

		StorageFactory:          storageFactory,
		EnableWatchCache:        s.GenericServerRunOptions.EnableWatchCache,
		EnableCoreControllers:   true,
		DeleteCollectionWorkers: s.GenericServerRunOptions.DeleteCollectionWorkers,
		EventTTL:                s.EventTTL,
		KubeletClientConfig:     s.KubeletConfig,
		EnableUISupport:         true,
		EnableLogsSupport:       true,
		ProxyTransport:          proxyTransport,

		Tunneler: tunneler,

		ServiceIPRange:       serviceIPRange,
		APIServerServiceIP:   apiServerServiceIP,
		APIServerServicePort: 443,

		ServiceNodePortRange:      s.GenericServerRunOptions.ServiceNodePortRange,
		KubernetesServiceNodePort: s.GenericServerRunOptions.KubernetesServiceNodePort,

		MasterCount: s.GenericServerRunOptions.MasterCount,
	}

	/*
		判断是否对watch cache进行了使能，默认是true。
		如果是true的话，会初始化watchCacheSize，然后设置各个resource的CacheSize
	*/
	if s.GenericServerRunOptions.EnableWatchCache {
		//Initalizing cache sizes based on 0MB limit 输出了
		glog.V(2).Infof("Initalizing cache sizes based on %dMB limit", s.GenericServerRunOptions.TargetRAMMB)
		cachesize.InitializeWatchCacheSizes(s.GenericServerRunOptions.TargetRAMMB)
		cachesize.SetWatchCacheSizes(s.GenericServerRunOptions.WatchCacheSizes)
	}

	/*
		创建master
		Complete()完善了config的初始化
		New()进行resources的初始化及RESTful-api注册
		==>定义在pkg/master/master.go
			==>func (c completedConfig) New() (*Master, error)
		apiServer之资源注册－V0.0
	*/
	m, err := config.Complete().New()
	if err != nil {
		return err
	}

	sharedInformers.Start(wait.NeverStop)
	/*
		运行HTTP/HTTPS服务
		/pkg/genericapiserver/genericapiserver.go
			==>func (s preparedGenericAPIServer) Run(stopCh <-chan struct{})
	*/
	m.GenericAPIServer.PrepareRun().Run(wait.NeverStop)
	return nil
}
```
看看最后的生成一个master对象，并执行Run(wait.NeverStop)。

## New一个master
那么重点就来到了这个master对象是怎么样的？见pkg/master/master.go。

func (c completedConfig) New() 基于给定的配置生成一个新的Master实例。如果未设置，某些配置字段将被设置为默认值。某些字段是必须指定的，比如：KubeletClientConfig
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
		//该对象主要提供了一个NewLegacyRESTStorage()的接口
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
在这里，先不对master进行过于深度的讲解。
至此，apiserver就已经run起来了，可以对外提供服务了。

## 总结
本文主要阅读了apiserver的参数设置，在run apiserver过程中以插件的形式启动认证、授权等模块。
最后，new一个master对象，通过master对象来完成路由的注册。

后面要讲解的内容包括
- k8s里面最顶层的概念设计：`group、restmapper、scheme...`这些概念
- api路由是怎么生成和管理的
- apiserver的多版本API管理机制
- 对go-restful package的使用
