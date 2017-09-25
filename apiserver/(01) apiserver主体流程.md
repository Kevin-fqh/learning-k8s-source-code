# Apiserver主体流程

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [main函数](#main函数)
  - [准备开始Run Apiserver](#准备开始run-apiserver)

  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

## main函数
直接上代码
```go
func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	/*
		NewServerRunOptions新建一个apiserver对象
		基本完成apiServer的运行参数初始化关键性步骤：
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
		将创建的apiserver对象传入app.Run()中，
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
1. main函数会首先根据相关的参数构建一个默认的ServerRunOptions参数对象s，其中`type ServerRunOptions struct`包含运行一个通用的api server所需的参数。
2. 然后通过`s.AddFlags(pflag.CommandLine)`获取命令行的输入信息，对s进行重新覆盖。
3. 最后执行`Run(s)`。

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
	DefaultStorageVersions string
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
		这里的控制策略还是很全面的:
		包括安全控制(CertDirectory, HTTPS默认启动)、
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
		// 默认的对象存储类型
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

## 准备开始Run Apiserver
app.Run(s)接口实现在cmd/kube-apiserver/app/server.go。
