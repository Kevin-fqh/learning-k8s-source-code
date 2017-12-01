# Client端与Daemon端的通信

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [docker Client端](#docker-client端)
    - [type DockerCli struct](#type-dockercli-struct)
    - [docker image ls](#docker-image-ls)
	- [type APIClient interface 和 type Client struct](#type-apiclient-interface-和-type-client-struct)
	  - [type Client struct](#type-client-struct)
  - [docker daemon端](#docker-daemon端)
    - [type DaemonCli struct](#type-daemoncli-struct)
	- [type Server struct](#type-server-struct)
	- [启动daemonCli](#启动daemoncli)
	- [路由注册](#路由注册)
	- [image相关Api](#image相关api)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## docker Client端
### type DockerCli struct
前面说到会生成一个type DockerCli struct对象，见/cli/command/cli.go

docker Client解析了命令行之后，会通过其中的`client.APIClient`和docker Server端进行通信
```go
// DockerCli represents the docker command line client.
// Instances of the client can be returned from NewDockerCli.
/*
	type DockerCli struct代表了docker command line client，
	docker Client会通过其中的`client.APIClient`和docker Server端进行通信
*/
type DockerCli struct {
	configFile *configfile.ConfigFile
	in         *InStream
	out        *OutStream
	err        io.Writer
	keyFile    string
	/*
		client.APIClient定义在
			==>/moby-1.13.1/client/client.go
				==>type APIClient interface
	*/
	client          client.APIClient
	hasExperimental bool
	defaultVersion  string
}

// Client returns the APIClient
func (cli *DockerCli) Client() client.APIClient {
	return cli.client
}
```

### docker image ls
我们以`docker image ls`为例子，研究docker的Client端是怎么把信息发给Server端的。 见/moby-1.13.1/client/image_list.go
```go
func runImages(dockerCli *command.DockerCli, opts imagesOptions) error {
	ctx := context.Background()

	filters := opts.filter.Value()
	if opts.matchName != "" {
		filters.Add("reference", opts.matchName)
	}

	options := types.ImageListOptions{
		All:     opts.all,
		Filters: filters,
	}

	/*
		访问docker的server端，获取images列表
	*/
	images, err := dockerCli.Client().ImageList(ctx, options)
	if err != nil {
		return err
	}
	...
	...
	...
}
```

dockerCli.Client()获取到的正是前面所说的`client.APIClient`，我们来看看ImageList()，见/moby-1.13.1/client/image_list.go
```go
// ImageList returns a list of images in the docker host.
func (cli *Client) ImageList(ctx context.Context, options types.ImageListOptions) ([]types.ImageSummary, error) {
	...
	...
	...

	/*
		==>/moby-1.13.1/client/request.go
			==>func (cli *Client) get
	*/
	serverResp, err := cli.get(ctx, "/images/json", query, nil)
	if err != nil {
		return images, err
	}

	err = json.NewDecoder(serverResp.body).Decode(&images)
	ensureReaderClosed(serverResp)
	return images, err
}
```

### type APIClient interface 和 type Client struct
关于dockerCli中client.APIClient的初始化，官方的描述是creates a new APIClient from command line flags，见/cli/command/cli.go的func NewAPIClientFromFlags()。

type APIClient interface的定义在 /client/interface_stable.go
```go
// APIClient is an interface that clients that talk with a docker server must implement.
type APIClient interface {
	CommonAPIClient
	apiClientExperimental
}

// Ensure that Client always implements APIClient.
/*
	_ 确保Client 实现了 type APIClient interface
*/
var _ APIClient = &Client{}
```

#### type Client struct 
```go
// Client is the API client that performs all operations
// against a docker server.
/*
	type Client struct是一个API client，提供了一个和docker server对话的通道
*/
type Client struct {
	// scheme sets the scheme for the client
	scheme string
	// host holds the server address to connect to
	host string
	// proto holds the client protocol i.e. unix.
	proto string
	// addr holds the client address.
	addr string
	// basePath holds the path to prepend to the requests.
	basePath string
	// client used to send and receive http requests.
	client *http.Client
	// version of the server to talk to.
	version string
	// custom http headers configured by users.
	customHTTPHeaders map[string]string
	// manualOverride is set to true when the version was set by users.
	manualOverride bool
}

// NewClient initializes a new API client for the given host and API version.
// It uses the given http client as transport.
// It also initializes the custom http headers to add to each request.
//
// It won't send any version information if the version number is empty. It is
// highly recommended that you set a version or your client may break if the
// server is upgraded.
/*
	func NewClient根据给定的host信息和API version来初始化一个API client
	使用http client进行通信
	会给每一个request加上header信息
	如果version信息缺失，不会发送任何version信息
*/
func NewClient(host string, version string, client *http.Client, httpHeaders map[string]string) (*Client, error) {
	/*
		host is:  unix:///var/run/docker.sock
	*/
	proto, addr, basePath, err := ParseHost(host)
	if err != nil {
		return nil, err
	}

	if client != nil {
		if _, ok := client.Transport.(*http.Transport); !ok {
			return nil, fmt.Errorf("unable to verify TLS configuration, invalid transport %v", client.Transport)
		}
	} else {
		transport := new(http.Transport)
		sockets.ConfigureTransport(transport, proto, addr)
		client = &http.Client{
			Transport: transport,
		}
	}

	scheme := "http"
	tlsConfig := resolveTLSConfig(client.Transport)
	if tlsConfig != nil {
		// TODO(stevvooe): This isn't really the right way to write clients in Go.
		// `NewClient` should probably only take an `*http.Client` and work from there.
		// Unfortunately, the model of having a host-ish/url-thingy as the connection
		// string has us confusing protocol and transport layers. We continue doing
		// this to avoid breaking existing clients but this should be addressed.
		scheme = "https"
	}

	return &Client{
		scheme:            scheme,
		host:              host,
		proto:             proto,
		addr:              addr,
		basePath:          basePath,
		client:            client,
		version:           version,
		customHTTPHeaders: httpHeaders,
	}, nil
}
```

接着上面`docker image ls`，查看Client提供的get()，见/moby-1.13.1/client/request.go
```go
// getWithContext sends an http request to the docker API using the method GET with a specific go context.
func (cli *Client) get(ctx context.Context, path string, query url.Values, headers map[string][]string) (serverResponse, error) {
	return cli.sendRequest(ctx, "GET", path, query, nil, headers)
}

func (cli *Client) sendRequest(ctx context.Context, method, path string, query url.Values, body io.Reader, headers headers) (serverResponse, error) {
	req, err := cli.buildRequest(method, cli.getAPIPath(path, query), body, headers)
	if err != nil {
		return serverResponse{}, err
	}
	/*
		客户端发送请求
	*/
	return cli.doRequest(ctx, req)
}

func (cli *Client) doRequest(ctx context.Context, req *http.Request) (serverResponse, error) {
	serverResp := serverResponse{statusCode: -1}

	/*
		发送请求，接收响应信息response
	*/
	resp, err := ctxhttp.Do(ctx, cli.client, req)
	if err != nil {
		...
		...
	}

	/*
		开始处理response信息
	*/
	if resp != nil {
		serverResp.statusCode = resp.StatusCode
	}

	if serverResp.statusCode < 200 || serverResp.statusCode >= 400 {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return serverResp, err
		}
		if len(body) == 0 {
			return serverResp, fmt.Errorf("Error: request returned %s for API route and version %s, check if the server supports the requested API version", http.StatusText(serverResp.statusCode), req.URL)
		}

		var errorMessage string
		if (cli.version == "" || versions.GreaterThan(cli.version, "1.23")) &&
			resp.Header.Get("Content-Type") == "application/json" {
			var errorResponse types.ErrorResponse
			if err := json.Unmarshal(body, &errorResponse); err != nil {
				return serverResp, fmt.Errorf("Error reading JSON: %v", err)
			}
			errorMessage = errorResponse.Message
		} else {
			errorMessage = string(body)
		}

		return serverResp, fmt.Errorf("Error response from daemon: %s", strings.TrimSpace(errorMessage))
	}

	serverResp.body = resp.Body
	serverResp.header = resp.Header
	return serverResp, nil
}
```
至此，docker client端的信息已经发送给docker daemon端了，并取得Response信息。 其它所有的命令都是类似的

## docker daemon端
首先来看看docker daemon端的启动流程。

1. 实例化一个type DaemonCli struct对象
2. 启动daemonCli
```go
func runDaemon(opts daemonOptions) error {
	if opts.version {
		showVersion()
		return nil
	}

	/*
		创建daemon客户端对象
	*/
	daemonCli := NewDaemonCli()

	/*这部分和windowns相关*/

	if stop {
		return nil
	}

	/*
		启动daemonCli
	*/
	err = daemonCli.start(opts)
	notifyShutdown(err)
	return err
}
```

### type DaemonCli struct
type DaemonCli struct包含了配置信息，配置文件，参数信息，APIServer,Daemon对象，authzMiddleware认证插件
```go
// DaemonCli represents the daemon CLI.

type DaemonCli struct {
	/*
		*daemon.Config的数据来源于
			==>/cmd/dockerd/docker.go
				==>func newDaemonCommand()
					==>daemonConfig: daemon.NewConfig(),
	*/
	*daemon.Config
	configFile *string
	flags      *pflag.FlagSet

	/*
		提供api服务
		==>/api/server/server.go
			==>type Server struct
	*/
	api *apiserver.Server
	/*
		==>/daemon/daemon.go
			==>type Daemon struct
	*/
	d               *daemon.Daemon
	authzMiddleware *authorization.Middleware // authzMiddleware enables to dynamically reload the authorization plugins
}

// NewDaemonCli returns a daemon CLI
func NewDaemonCli() *DaemonCli {
	return &DaemonCli{}
}
```

### type Server struct
```go
// Server contains instance details for the server
type Server struct {
	cfg           *Config
	servers       []*HTTPServer
	routers       []router.Router
	routerSwapper *routerSwapper
	middlewares   []middleware.Middleware
}
```
- HTTPServer
```go
// HTTPServer contains an instance of http server and the listener.
// srv *http.Server, contains configuration to create an http server and a mux router with all api end points.
// l   net.Listener, is a TCP or Socket listener that dispatches incoming request to the router.
/*
	l   net.Listener, is a TCP or Socket listener，分发接收到的请求给router
*/
type HTTPServer struct {
	srv *http.Server
	l   net.Listener
}
```
- routerSwapper
```go
// routerSwapper is an http.Handler that allows you to swap
// mux routers.
/*
	routerSwapper提供方法用于替换 多路复用路由器。
*/
type routerSwapper struct {
	mu     sync.Mutex
	router *mux.Router
}
```

### 启动daemonCli
接着上面的主体，启动daemonCli，daemonCli.start(opts)，其主要流程如下：
1. 设置相关参数
2. 创建一个type Server struct对象，同时设置监控地址
3. registryService，daemon程序在pull镜像等操作时，需要与registry服务交互
4. 创建与`docker-containerd`通信的对象containerdRemote
5. 创建Daemon对象，daemon.NewDaemon(cli.Config, registryService, containerdRemote)
6. 初始化API Server的路由
7. 起一个groutine来启动server

```go
func (cli *DaemonCli) start(opts daemonOptions) (err error) {
	stopc := make(chan bool)
	defer close(stopc)

	// warn from uuid package when running the daemon
	uuid.Loggerf = logrus.Warnf

	/*
		==>/cli/flags/common.go
			==>func (commonOpts *CommonOptions) SetDefaultOptions
		现在只有和TLS相关的参数
	*/
	opts.common.SetDefaultOptions(opts.flags)

	if cli.Config, err = loadDaemonCliConfig(opts); err != nil {
		return err
	}
	cli.configFile = &opts.configFile
	cli.flags = opts.flags

	if opts.common.TrustKey == "" {
		opts.common.TrustKey = filepath.Join(
			getDaemonConfDir(cli.Config.Root),
			cliflags.DefaultTrustKeyFile)
	}

	if cli.Config.Debug {
		utils.EnableDebug()
	}

	if cli.Config.Experimental {
		logrus.Warn("Running experimental build")
	}

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: jsonlog.RFC3339NanoFixed,
		DisableColors:   cli.Config.RawLogs,
	})

	if err := setDefaultUmask(); err != nil {
		return fmt.Errorf("Failed to set umask: %v", err)
	}

	if len(cli.LogConfig.Config) > 0 {
		if err := logger.ValidateLogOpts(cli.LogConfig.Type, cli.LogConfig.Config); err != nil {
			return fmt.Errorf("Failed to set log opts: %v", err)
		}
	}

	// Create the daemon root before we create ANY other files (PID, or migrate keys)
	// to ensure the appropriate ACL is set (particularly relevant on Windows)
	if err := daemon.CreateDaemonRoot(cli.Config); err != nil {
		return err
	}

	if cli.Pidfile != "" {
		pf, err := pidfile.New(cli.Pidfile)
		if err != nil {
			return fmt.Errorf("Error starting daemon: %v", err)
		}
		defer func() {
			if err := pf.Remove(); err != nil {
				logrus.Error(err)
			}
		}()
	}

	serverConfig := &apiserver.Config{
		Logging:     true,
		SocketGroup: cli.Config.SocketGroup,
		Version:     dockerversion.Version,
		EnableCors:  cli.Config.EnableCors,
		CorsHeaders: cli.Config.CorsHeaders,
	}

	if cli.Config.TLS {
		tlsOptions := tlsconfig.Options{
			CAFile:   cli.Config.CommonTLSOptions.CAFile,
			CertFile: cli.Config.CommonTLSOptions.CertFile,
			KeyFile:  cli.Config.CommonTLSOptions.KeyFile,
		}

		if cli.Config.TLSVerify {
			// server requires and verifies client's certificate
			tlsOptions.ClientAuth = tls.RequireAndVerifyClientCert
		}
		tlsConfig, err := tlsconfig.Server(tlsOptions)
		if err != nil {
			return err
		}
		serverConfig.TLSConfig = tlsConfig
	}

	if len(cli.Config.Hosts) == 0 {
		cli.Config.Hosts = make([]string, 1)
	}

	//创建一个type Server struct对象
	api := apiserver.New(serverConfig)
	cli.api = api

	/*
	   daemon程序可以设置监控多个地址
	   默认情况下,cli.Config.Hosts 长度为 1，监控地址为unix:///var/run/docker.sock
	*/
	for i := 0; i < len(cli.Config.Hosts); i++ {
		var err error
		if cli.Config.Hosts[i], err = dopts.ParseHost(cli.Config.TLS, cli.Config.Hosts[i]); err != nil {
			return fmt.Errorf("error parsing -H %s : %v", cli.Config.Hosts[i], err)
		}

		/*
			protoAddr的值: unix:///var/run/docker.sock
		*/
		protoAddr := cli.Config.Hosts[i]
		protoAddrParts := strings.SplitN(protoAddr, "://", 2)
		if len(protoAddrParts) != 2 {
			return fmt.Errorf("bad format %s, expected PROTO://ADDR", protoAddr)
		}

		proto := protoAddrParts[0]
		addr := protoAddrParts[1]

		// It's a bad idea to bind to TCP without tlsverify.
		if proto == "tcp" && (serverConfig.TLSConfig == nil || serverConfig.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert) {
			logrus.Warn("[!] DON'T BIND ON ANY IP ADDRESS WITHOUT setting -tlsverify IF YOU DON'T KNOW WHAT YOU'RE DOING [!]")
		}
		/*
			根据host解析内容初始化监听器listener
				==>/pkg/listeners/listeners_unix.go
		*/
		ls, err := listeners.Init(proto, addr, serverConfig.SocketGroup, serverConfig.TLSConfig)
		if err != nil {
			return err
		}
		ls = wrapListeners(proto, ls)
		// If we're binding to a TCP port, make sure that a container doesn't try to use it.
		if proto == "tcp" {
			if err := allocateDaemonPort(addr); err != nil {
				return err
			}
		}
		logrus.Debugf("Listener created for HTTP on %s (%s)", proto, addr)
		/*
			Accept()设置服务器接受连接的监听器
			addr is: /var/run/docker.sock
				==>/api/server/server.go
		*/
		api.Accept(addr, ls...)
	}

	if err := migrateKey(cli.Config); err != nil {
		return err
	}

	// FIXME: why is this down here instead of with the other TrustKey logic above?
	cli.TrustKeyPath = opts.common.TrustKey

	/*
		daemon程序在pull镜像等操作时，需要与registry服务交互，
		这里即创建了registryService对象，用于与registry服务交互
		a registry service应该实现的接口，见/registry/service.go
			==>type Service interface
	*/
	registryService := registry.NewService(cli.Config.ServiceOptions)
	/*
		创建与`docker-containerd`通信的对象containerdRemote
			==>/libcontainerd/remote_unix.go
				==>func New
		/libcontainerd目录下仅提供用于通信的接口函数，供daemon程序调用，以控制管理容器的运行
			==>/libcontainerd/types.go
				==>type Client interface
		`docker-containerd`真正的源码在 https://github.com/containerd/containerd
		通过｀make all｀进行编译的时候，会自行下载
	*/
	containerdRemote, err := libcontainerd.New(cli.getLibcontainerdRoot(), cli.getPlatformRemoteOptions()...)
	if err != nil {
		return err
	}
	/*
		声明监听系统的一些终止信号，
		如果监听到这些信息，就停止DaemonCli
	*/
	signal.Trap(func() {
		cli.stop()
		<-stopc // wait for daemonCli.start() to return
	})

	/*
		创建Daemon对象
			==>/daemon/daemon.go
				==>func NewDaemon
	*/
	d, err := daemon.NewDaemon(cli.Config, registryService, containerdRemote)
	if err != nil {
		return fmt.Errorf("Error starting daemon: %v", err)
	}

	if cli.Config.MetricsAddress != "" {
		if !d.HasExperimental() {
			return fmt.Errorf("metrics-addr is only supported when experimental is enabled")
		}
		if err := startMetricsServer(cli.Config.MetricsAddress); err != nil {
			return err
		}
	}

	name, _ := os.Hostname()

	//新建cluster对象
	c, err := cluster.New(cluster.Config{
		Root:                   cli.Config.Root,
		Name:                   name,
		Backend:                d,
		NetworkSubnetsProvider: d,
		DefaultAdvertiseAddr:   cli.Config.SwarmDefaultAdvertiseAddr,
		RuntimeRoot:            cli.getSwarmRunRoot(),
	})
	if err != nil {
		logrus.Fatalf("Error creating cluster component: %v", err)
	}

	// Restart all autostart containers which has a swarm endpoint
	// and is not yet running now that we have successfully
	// initialized the cluster.
	/*
		V1.12版本开始集成了swarm的相关功能，
		这里将自动启动安装有swarm endpoint的容器
	*/
	d.RestartSwarmContainers()

	logrus.Info("Daemon has completed initialization")

	logrus.WithFields(logrus.Fields{
		"version":     dockerversion.Version,
		"commit":      dockerversion.GitCommit,
		"graphdriver": d.GraphDriverName(),
	}).Info("Docker daemon")

	/*
		将新建的Daemon对象与DaemonCli相关联：cli.d = d
	*/
	cli.d = d

	// initMiddlewares needs cli.d to be populated. Dont change this init order.
	/*
		给API Server注册一些中间件，
		主要进行版本兼容性检查、添加CORS跨站点请求相关响应头、对请求进行认证
	*/
	if err := cli.initMiddlewares(api, serverConfig); err != nil {
		logrus.Fatalf("Error creating middlewares: %v", err)
	}
	d.SetCluster(c)
	/*
		初始化API Server的路由
	*/
	initRouter(api, d, c)

	cli.setupConfigReloadTrap()

	// The serve API routine never exits unless an error occurs
	// We need to start it as a goroutine and wait on it so
	// daemon doesn't exit
	/*
		起一个groutine来启动server
		如果出现error，channel serveAPIWait会收到信息
			==>/api/server/server.go
				==>func (s *Server) Wait(waitChan chan error)
	*/
	serveAPIWait := make(chan error)
	go api.Wait(serveAPIWait)

	// after the daemon is done setting up we can notify systemd api
	/*
		==>/cmd/dockerd/daemon_linux.go
			==>func notifySystem()
		给host主机发送一个信息，表示server已经Ready了，可以接收request了
	*/
	notifySystem()

	// Daemon is fully initialized and handling API traffic
	// Wait for serve API to complete
	/*
		如果不出现error，channel serveAPIWait会阻塞
	*/
	errAPI := <-serveAPIWait
	c.Cleanup()                //有错误信息传出，对cluster进行清理操作
	shutdownDaemon(d)          //关闭Daemon
	containerdRemote.Cleanup() //关闭libcontainerd
	if errAPI != nil {
		return fmt.Errorf("Shutting down due to ServeAPI error: %v", errAPI)
	}

	return nil
}
```

### 路由注册
func initRouter 添加各种路由到routers中，然后根据路由表routers来初始化apiServer的路由器。 见/cmd/dockerd/daemon.go
```go
func initRouter(s *apiserver.Server, d *daemon.Daemon, c *cluster.Cluster) {
	/* 获取解码器decoder */
	decoder := runconfig.ContainerDecoder{}

	/*
		添加路由
	*/
	routers := []router.Router{
		// we need to add the checkpoint router before the container router or the DELETE gets masked
		/*
			添加的顺序有要求
		*/
		checkpointrouter.NewRouter(d, decoder),
		container.NewRouter(d, decoder),
		/*
			以image的路为例子
				==>/api/server/router/image/image.go
					==>	func NewRouter
		*/
		image.NewRouter(d, decoder),
		systemrouter.NewRouter(d, c),
		volume.NewRouter(d),
		build.NewRouter(dockerfile.NewBuildManager(d)),
		swarmrouter.NewRouter(c),
		pluginrouter.NewRouter(d.PluginManager()),
	}

	//网络相关路由
	if d.NetworkControllerEnabled() {
		routers = append(routers, network.NewRouter(d, c))
	}

	if d.HasExperimental() {
		/*
			Experimental模式下的api
		*/
		for _, r := range routers {
			for _, route := range r.Routes() {
				if experimental, ok := route.(router.ExperimentalRoute); ok {
					experimental.Enable()
				}
			}
		}
	}

	/*
		根据设置好的路由表routers来初始化apiServer的路由器
	*/
	s.InitRouter(utils.IsDebugEnabled(), routers...)
}

// InitRouter initializes the list of routers for the server.
// This method also enables the Go profiler if enableProfiler is true.
func (s *Server) InitRouter(enableProfiler bool, routers ...router.Router) {
	s.routers = append(s.routers, routers...)

	m := s.createMux() //真正的api注册
	if enableProfiler {
		profilerSetup(m)
	}
	s.routerSwapper = &routerSwapper{
		router: m,
	}
}
```

func (s *Server) createMux()完成真正的api注册
```go
// createMux initializes the main router the server uses.
/*
	这里进行真正的路由注册，handler(f)
*/
func (s *Server) createMux() *mux.Router {
	m := mux.NewRouter()

	logrus.Debug("Registering routers")
	for _, apiRouter := range s.routers { //遍历每个路由
		for _, r := range apiRouter.Routes() { //遍历每个路由的子命令、动作
			f := s.makeHTTPHandler(r.Handler())

			logrus.Debugf("Registering %s, %s", r.Method(), r.Path())
			/*
				在mux.Route路由结构中根据这个r.Path()路径设置一个适配器来匹配方法method和handler。
				当满足versionMatcher + r.Path()路径的正则表达式要求，就可以适配到相应的方法名及该handler
			*/
			m.Path(versionMatcher + r.Path()).Methods(r.Method()).Handler(f)
			m.Path(r.Path()).Methods(r.Method()).Handler(f)
		}
	}

	/*
		mux.Route没有找到请求数据所对应的方法或函数handler时的处理办法
	*/
	err := errors.NewRequestNotFoundError(fmt.Errorf("page not found"))
	notFoundHandler := httputils.MakeErrorHandler(err)
	m.HandleFunc(versionMatcher+"/{path:.*}", notFoundHandler)
	m.NotFoundHandler = notFoundHandler

	return m
}
```

### image相关Api
```go
// NewRouter initializes a new image router
func NewRouter(backend Backend, decoder httputils.ContainerDecoder) router.Router {
	r := &imageRouter{
		backend: backend,
		decoder: decoder,
	}
	r.initRoutes()
	return r
}

// initRoutes initializes the routes in the image router
func (r *imageRouter) initRoutes() {
	r.routes = []router.Route{
		// GET
		router.NewGetRoute("/images/json", r.getImagesJSON), /*建立一个Get方式的本地路由对象localRoute*/
		router.NewGetRoute("/images/search", r.getImagesSearch),
		router.NewGetRoute("/images/get", r.getImagesGet),
		router.NewGetRoute("/images/{name:.*}/get", r.getImagesGet),
		router.NewGetRoute("/images/{name:.*}/history", r.getImagesHistory),
		router.NewGetRoute("/images/{name:.*}/json", r.getImagesByName),
		// POST
		router.NewPostRoute("/commit", r.postCommit),
		router.NewPostRoute("/images/load", r.postImagesLoad),
		router.Cancellable(router.NewPostRoute("/images/create", r.postImagesCreate)),
		router.Cancellable(router.NewPostRoute("/images/{name:.*}/push", r.postImagesPush)),
		router.NewPostRoute("/images/{name:.*}/tag", r.postImagesTag),
		router.NewPostRoute("/images/prune", r.postImagesPrune),
		// DELETE
		router.NewDeleteRoute("/images/{name:.*}", r.deleteImages),
	}
}
```

其中定义在/api/server/router/image/backend.go的type Backend interface声明了提供image相关功能需要实现的函数集合
```go
// Backend is all the methods that need to be implemented
// to provide image specific functionality.
/*
	type Backend interface声明了提供image相关功能需要实现的函数集合
*/
type Backend interface {
	containerBackend
	imageBackend
	importExportBackend
	registryBackend
}

type containerBackend interface {
	Commit(name string, config *backend.ContainerCommitConfig) (imageID string, err error)
}

type imageBackend interface {
	ImageDelete(imageRef string, force, prune bool) ([]types.ImageDelete, error)
	ImageHistory(imageName string) ([]*types.ImageHistory, error)
	Images(imageFilters filters.Args, all bool, withExtraAttrs bool) ([]*types.ImageSummary, error)
	LookupImage(name string) (*types.ImageInspect, error)
	TagImage(imageName, repository, tag string) error
	ImagesPrune(pruneFilters filters.Args) (*types.ImagesPruneReport, error)
}
```


## 参考
[docker客户端与服务器端通信模块](http://blog.xbblfz.site/2017/04/20/docker客户端与服务器端通信模块一/)
[Docker源码分析](https://www.kancloud.cn/infoq/docker-source-code-analysis/80528)





