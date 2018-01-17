# containerd基本流程

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [main函数](#main函数)
    - [type App struct](#type-app-struct)
    - [func daemon()](#func-daemon)
  - [grpc server端的路由](#grpc-server端的路由)
    - [RegisterAPIServer()注册API](#registerapiserver注册api)
    - [Handler函数](#handler函数)
  - [type Supervisor struct](#type-supervisor-struct)
    - [创建一个Supervisor对象](#创建一个supervisor对象)
    - [Supervisor的SendTask()](#supervisor的sendtask)
    - [Supervisor的Start()](#supervisor的start)
    - [处理docker start和ctr start对应的task](#处理docker-start和ctr-start对应的task)
  - [Supervisor的worker](#supervisor的worker)
    - [创建一个Supervisor对象](#创建一个supervisor对象)
    - [worker的Start()](#worker的start)
<!-- END MUNGE: GENERATED_TOC -->

## 说明
v0.2.3

v0.2.3版本的containerd，只负责管理容器的生命周期，不关心容器的镜像数据，所以containerd的管理对象只是运行中的容器，停止的容器containerd并不关心，所以只要containerd创建一个容器就相当于启动一个容器。

在最新的contianerd代码中，已经可以管理镜像了。
![architecture](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/containerd-architecture.png)

调试过程中用到的命令如下
```shell
# /root/lib-containerd/containerd/bin/containerd -l unix:///var/run/docker/libcontainerd/docker-containerd.sock --metrics-interval=0 --start-timeout 2m --state-dir /var/run/docker/libcontainerd/containerd --shim /root/lib-containerd/containerd/bin/containerd-shim --runtime /root/runc-source-code/runc/runc

# /root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers start redis /home/fqh-runc-container-test/redis

# /root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers exec -id redis --pid 33  --cwd /bin --tty --attach ./date
```

先说重点，
```
数据通道：
		docker-daemon--->tasks chan Task--->func (s *Supervisor) Start()消费
		--->存放到startTasks  chan *startTask-->func (w *worker) Start()消费
```

## main函数
前面介绍过docker-daemon是通过grpc与contanierd建立连接的，也就是说containerd相当于docekr-daemon的Server端。 
那么我们来看看containerd是如何建立Server，并提供服务的。

其主要流程分析如下：
1. 创建了一个type App struct对象，代表了containerd
2. 声明了app.Action，运行func daemon()，在app.Run(os.Args)中会得到调用
3. 执行app.Run(os.Args)
```go
func main() {
	logrus.SetFormatter(&logrus.TextFormatter{TimestampFormat: time.RFC3339Nano})
	/*
		==>/vendor/src/github.com/codegangsta/cli/app.go
		containerd是一个cli application
		而app则是其主要结构
	*/
	app := cli.NewApp()
	app.Name = "containerd"
	if containerd.GitCommit != "" {
		app.Version = fmt.Sprintf("%s commit: %s", containerd.Version, containerd.GitCommit)
	} else {
		app.Version = containerd.Version
	}
	app.Usage = usage
	//声明了Flags
	app.Flags = daemonFlags
	//定义了Before()
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
			if context.GlobalDuration("metrics-interval") > 0 {
				if err := debugMetrics(context.GlobalDuration("metrics-interval"), context.GlobalString("graphite-address")); err != nil {
					return err
				}
			}

		}
		if p := context.GlobalString("pprof-address"); len(p) > 0 {
			pprof.Enable(p)
		}
		if err := checkLimits(); err != nil {
			return err
		}
		return nil
	}

	/*
		定义了func Action()，当没有指定subcommands时执行
		启动containerd的daemon进程。开启grpc服务器
	*/
	app.Action = func(context *cli.Context) {
		if err := daemon(context); err != nil {
			logrus.Fatal(err)
		}
	}
	/*
		==>/vendor/src/github.com/codegangsta/cli/app.go
			==>func (a *App) Run(arguments []string) (err error)
		cli application的入口
	*/
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
```

### type App struct
主要看其run()函数，这里需要注意的是执行`containerd`和执行`ctr`命令的区别。 

- a.Action(context)会直接调用func daemon()  
见 /containerd-0.2.3/vendor/src/github.com/codegangsta/cli/app.go
```go
// Entry point to the cli app. Parses the arguments slice and routes to the proper flag/args combination
/*
	cli app的入口
	解析参数切片，然后路由到适当的flag/args组合
*/
func (a *App) Run(arguments []string) (err error) {
	...
	...

	// parse flags
	/*
		开始解析参数，得到参数集合set
	*/
	set := flagSet(a.Name, a.Flags)
	set.SetOutput(ioutil.Discard)
	err = set.Parse(arguments[1:])
	nerr := normalizeFlags(a.Flags, set) //检查参数合法性
	...
	...

	//执行Before()
	if a.Before != nil {
		err = a.Before(context)
		if err != nil {
			fmt.Fprintf(a.Writer, "%v\n\n", err)
			ShowAppHelp(context)
			return err
		}
	}

	/*
		第一次运行`containerd` daemon时，args is:  []，即len(args)=0

		执行 `ctr containers` 时，args is: [containers]，即len(args)>0
	*/
	args := context.Args()
	if args.Present() {
		name := args.First()
		c := a.Command(name)
		if c != nil {
			return c.Run(context)
		}
	}

	// Run default Action
	//执行Action()
	a.Action(context)
	return nil
}

// Checks if there are any arguments present
func (a Args) Present() bool {
	return len(a) != 0
}
```

### func daemon()
分析其流程如下：
1. 新建一个type Supervisor struct对象，这个是containerd的核心部件
2. supervisor创建的10个worker的Start()，负责处理创建新容器的task
3. supervisor本身的Start()，消费tasks chan Task
4. 启动grpc server端，这里会接收来自docker-daemon的Request

关于task的介绍，见下面的[Supervisor](#type-supervisor-struct)
```go
/*
	核心是：
		1. supervisor创建的10个worker的Start()，负责处理创建新容器的task
		2. supervisor本身的Start()，消费tasks chan Task
*/
func daemon(context *cli.Context) error {
	// setup a standard reaper so that we don't leave any zombies if we are still alive
	// this is just good practice because we are spawning new processes
	/*
		创建一个标准的reaper(回收者)，避免留下孤立进程。
		containd daemon在创建容器的时候，会产生新的进程。

		系统Signal处理，通知系统退出，即kill pragram-pid
		当收到信号后，会执行相关清理程序或通知各个子进程做自清理
	*/
	s := make(chan os.Signal, 2048)
	signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)
	/*
		新建一个supervisor，这个是containerd的核心部件
			==>/supervisor/supervisor.go
				==>func New
	*/
	sv, err := supervisor.New(
		context.String("state-dir"),
		context.String("runtime"),
		context.String("shim"),
		context.StringSlice("runtime-args"),
		context.Duration("start-timeout"),
		context.Int("retain-count"))
	if err != nil {
		return err
	}
	/*
		wg为sync.WaitGroup同步goroutine，
		一个worker里面包含一个supervisor和sync.WaitGroup，
		这里的wg主要用于实现容器的启动部分
	*/
	wg := &sync.WaitGroup{}
	/*
		supervisor 启动10个worker
			==>/supervisor/worker.go
	*/
	for i := 0; i < 10; i++ {
		wg.Add(1)
		w := supervisor.NewWorker(sv, wg)
		go w.Start()
	}
	//启动supervisor
	if err := sv.Start(); err != nil {
		return err
	}
	// Split the listen string of the form proto://addr
	/*
		根据参数获取监听器
		listenSpec的值为 unix:///var/run/docker/libcontainerd/docker-containerd.sock
	*/
	listenSpec := context.String("listen")
	listenParts := strings.SplitN(listenSpec, "://", 2)
	if len(listenParts) != 2 {
		return fmt.Errorf("bad listen address format %s, expected proto://address", listenSpec)
	}
	/*
		启动grpc server端
	*/
	server, err := startServer(listenParts[0], listenParts[1], sv)
	if err != nil {
		return err
	}
	for ss := range s {
		/*	收到系统Signal信号，退出containerd */
		switch ss {
		default:
			logrus.Infof("stopping containerd after receiving %s", ss)
			server.Stop()
			os.Exit(0)
		}
	}
	return nil
}
```
至此，大概的脉络图已经清晰。

## grpc server端的路由
下面我们先来看看grpc server端的建立，是如何接收来自于docker-daemon端的请求的。
![link1](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/dockerDaemon-containerd.png)
```go
/*
	proto文件
		==>/containerd-0.2.3/api/grpc/types/api.proto
*/
func startServer(protocol, address string, sv *supervisor.Supervisor) (*grpc.Server, error) {
	// TODO: We should use TLS.
	// TODO: Add an option for the SocketGroup.
	/*
		生成一个socker套接字
	*/
	sockets, err := listeners.Init(protocol, address, "", nil)
	if err != nil {
		return nil, err
	}
	if len(sockets) != 1 {
		return nil, fmt.Errorf("incorrect number of listeners")
	}
	l := sockets[0]
	s := grpc.NewServer()
	/*
		注册API
		==>/api/grpc/types/api.pb.go
	*/
	types.RegisterAPIServer(s, server.NewServer(sv))
	go func() {
		logrus.Debugf("containerd: grpc api on %s", address)
		//将grpc服务器与指定套接字关联起来，从而监听docker-containerd.sock
		if err := s.Serve(l); err != nil {
			logrus.WithField("error", err).Fatal("containerd: serve grpc")
		}
	}()
	return s, nil
}
```

### RegisterAPIServer()注册API
这里是`protoc`工具自动生成的代码，声明了grpc server端的路由
```go
func RegisterAPIServer(s *grpc.Server, srv APIServer) {
	// _API_serviceDesc 声明了路由-->handler
	s.RegisterService(&_API_serviceDesc, srv)
}

var _API_serviceDesc = grpc.ServiceDesc{
	ServiceName: "types.API",
	HandlerType: (*APIServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetServerVersion",
			Handler:    _API_GetServerVersion_Handler,
		},
		{
			MethodName: "CreateContainer",
			Handler:    _API_CreateContainer_Handler,
		},
		{
			MethodName: "UpdateContainer",
			Handler:    _API_UpdateContainer_Handler,
		},
		{
			MethodName: "Signal",
			Handler:    _API_Signal_Handler,
		},
		{
			MethodName: "UpdateProcess",
			Handler:    _API_UpdateProcess_Handler,
		},
		{
			MethodName: "AddProcess",
			Handler:    _API_AddProcess_Handler,
		},
		{
			MethodName: "CreateCheckpoint",
			Handler:    _API_CreateCheckpoint_Handler,
		},
		{
			MethodName: "DeleteCheckpoint",
			Handler:    _API_DeleteCheckpoint_Handler,
		},
		{
			MethodName: "ListCheckpoint",
			Handler:    _API_ListCheckpoint_Handler,
		},
		{
			MethodName: "State",
			Handler:    _API_State_Handler,
		},
		{
			MethodName: "Stats",
			Handler:    _API_Stats_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Events",
			Handler:       _API_Events_Handler,
			ServerStreams: true,
		},
	},
}
```

### Handler函数
还记得之前介绍的`docker start {containerID}`的路由是`/types.API/CreateContainer`，那么其对应的Handler就是`func _API_CreateContainer_Handler`
```go
func _API_CreateContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(APIServer).CreateContainer(ctx, in)
	}
	/*
		对应的是docker start {containerID}命令
			  ctr start {containerID} {path-to-rootfs}
	*/
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/types.API/CreateContainer",
	}
	/*
		==>/api/grpc/server/server.go
			==>func (s *apiServer) CreateContainer
	*/
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(APIServer).CreateContainer(ctx, req.(*CreateContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func (s *apiServer) CreateContainer(ctx context.Context, c *types.CreateContainerRequest) (*types.CreateContainerResponse, error) {
	if c.BundlePath == "" {
		return nil, errors.New("empty bundle path")
	}
	/*
		新建一个StartTask e，存放创建容器的request信息，本函数的任务类型是 StartTask
			==>/supervisor/worker.go
				==>type startTask struct
	*/
	e := &supervisor.StartTask{}
	e.ID = c.Id
	e.BundlePath = c.BundlePath
	e.Stdin = c.Stdin
	e.Stdout = c.Stdout
	e.Stderr = c.Stderr
	e.Labels = c.Labels
	e.NoPivotRoot = c.NoPivotRoot
	e.Runtime = c.Runtime
	e.RuntimeArgs = c.RuntimeArgs
	e.StartResponse = make(chan supervisor.StartResponse, 1)
	if c.Checkpoint != "" {
		e.CheckpointDir = c.CheckpointDir
		e.Checkpoint = &runtime.Checkpoint{
			Name: c.Checkpoint,
		}
	}
	/*
		把StartTask e发给the the supervisors main event loop
			==>/supervisor/supervisor.go
				==>func (s *Supervisor) SendTask(evt Task)
	*/
	s.sv.SendTask(e)
	if err := <-e.ErrorCh(); err != nil {
		return nil, err
	}
	/*
		StartResponse channel容量为1，如果没有接收到信息，阻塞
		等待/supervisor/worker.go中func (w *worker) Start()的处理结果
	*/
	r := <-e.StartResponse
	apiC, err := createAPIContainer(r.Container, false)
	if err != nil {
		return nil, err
	}
	return &types.CreateContainerResponse{
		Container: apiC,
	}, nil
}
```
可以看到这里接收到一个Request之后，会把其转化为一个type startTask struct，经`s.sv.SendTask(e)`发送给supervisors的main event loop，然后等待supervisors的处理结果，等到之后再Resonese给docker-daemon端。

## type Supervisor struct
type Supervisor struct是containerd的核心部件。Supervisor是把一个Request转化为一个Task来进行管理，为一个Task调用`containerd-shim`，从而启动容器。 理清其数据通道，是了解containerd的关键。

两个核心属性：
  * startTasks  chan *startTask ,这是containerd到runc的桥梁，由func (w *worker) Start()消费
  * tasks          chan Task ,所有来自于docker-daemon的request都会转化为event存放到这，由func (s *Supervisor) Start()消费
```go
// Supervisor represents a container supervisor
/*
	数据通道：
		docker-daemon--->tasks chan Task--->func (s *Supervisor) Start()消费
		--->存放到startTasks  chan *startTask-->func (w *worker) Start()消费
*/
type Supervisor struct {
	// stateDir is the directory on the system to store container runtime state information.
	stateDir string
	// name of the OCI compatible runtime used to execute containers
	runtime     string
	runtimeArgs []string
	shim        string
	containers  map[string]*containerInfo
	startTasks  chan *startTask //这是containerd到runc的桥梁，由func (w *worker) Start()消费
	// we need a lock around the subscribers map only because additions and deletions from
	// the map are via the API so we cannot really control the concurrency
	subscriberLock sync.RWMutex
	subscribers    map[chan Event]struct{}
	machine        Machine
	tasks          chan Task //所有来自于docker-daemon的request都会转化为event存放到这，由func (s *Supervisor) Start()消费
	monitor        *Monitor
	eventLog       []Event
	eventLock      sync.Mutex
	timeout        time.Duration
}

/*
	这是首字母小写的startTask
*/
type startTask struct {
	Container      runtime.Container
	CheckpointPath string
	Stdin          string
	Stdout         string
	Stderr         string
	Err            chan error
	StartResponse  chan StartResponse
}
```
### 创建一个Supervisor对象
```go
// New returns an initialized Process supervisor.
/*
	新建一个supervisor，任务管理器，处理tasks，记录着并监视着系统中每个container
*/
func New(stateDir string, runtimeName, shimName string, runtimeArgs []string, timeout time.Duration, retainCount int) (*Supervisor, error) {
	startTasks := make(chan *startTask, 10)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, err
	}
	//获取宿主机的cpu和memory信息
	machine, err := CollectMachineInformation()
	if err != nil {
		return nil, err
	}
	/*
		monitor用于监视容器中的进程
		==>/supervisor/monitor_linux.go
	*/
	monitor, err := NewMonitor()
	if err != nil {
		return nil, err
	}
	s := &Supervisor{
		stateDir:    stateDir,
		containers:  make(map[string]*containerInfo),
		startTasks:  startTasks,
		machine:     machine,
		subscribers: make(map[chan Event]struct{}),
		tasks:       make(chan Task, defaultBufferSize),
		monitor:     monitor,
		runtime:     runtimeName,
		runtimeArgs: runtimeArgs,
		shim:        shimName,
		timeout:     timeout,
	}
	//处理event log
	if err := setupEventLog(s, retainCount); err != nil {
		return nil, err
	}
	//利用monitor处理exit的进程和触发oom的进程
	go s.exitHandler()
	go s.oomHandler()
	if err := s.restore(); err != nil {
		return nil, err
	}
	return s, nil
}
```

### Supervisor的SendTask()
```go
// SendTask sends the provided event to the the supervisors main event loop
/*
	SendTask将evt Task发送给 the supervisors main event loop
	所有来自于docker-daemon的request都会转化为event存放到这，生产者
*/
func (s *Supervisor) SendTask(evt Task) {
	TasksCounter.Inc(1) //任务数+1
	s.tasks <- evt
}
```

### Supervisor的Start()
StartTask是`docker start`和`ctr start`对应的task类型，可以看出每一个Request都有着自己的task类型，而Supervisor正是据此来做出相应处理。

其主要功能是消费tasks chan Task
```go
/*
	Start()是一个非阻塞的调用，运行监视器来监视contianer进程，并运行新的容器。

	这里的event loop是唯一一个地方可以修改容器和其process的状态
*/
func (s *Supervisor) Start() error {
	logrus.WithFields(logrus.Fields{
		"stateDir":    s.stateDir,
		"runtime":     s.runtime,
		"runtimeArgs": s.runtimeArgs,
		"memory":      s.machine.Memory,
		"cpus":        s.machine.Cpus,
	}).Debug("containerd: supervisor running")
	go func() {
		/*
			消费tasks chan Task
		*/
		for i := range s.tasks {
			/*新建一个goroutine来遍历s.tasks通道中的所有任务并处理*/
			s.handleTask(i)
		}
	}()
	return nil
}

func (s *Supervisor) handleTask(i Task) {
	var err error
	/*
		通过任务类型识别调用具体的方法执行函数
	*/
	switch t := i.(type) {
	case *AddProcessTask:
		err = s.addProcess(t)
	case *CreateCheckpointTask:
		err = s.createCheckpoint(t) //创建检查点的进一步实现
	case *DeleteCheckpointTask:
		err = s.deleteCheckpoint(t)
	case *StartTask: //`docker start`和`ctr start`对应的task
		err = s.start(t)
	case *DeleteTask:
		err = s.delete(t)
	case *ExitTask:
		err = s.exit(t)
	case *GetContainersTask:
		err = s.getContainers(t)
	case *SignalTask:
		err = s.signal(t)
	case *StatsTask:
		err = s.stats(t)
	case *UpdateTask:
		err = s.updateContainer(t)
	case *UpdateProcessTask:
		err = s.updateProcess(t)
	case *OOMTask:
		err = s.oom(t)
	default:
		err = ErrUnknownTask
	}
	if err != errDeferredResponse {
		i.ErrorCh() <- err
		close(i.ErrorCh())
	}
}
```

### 处理docker start和ctr start对应的task
把task发给Supervisor的startTasks  chan *startTask
```go
func (s *Supervisor) start(t *StartTask) error {
	start := time.Now()
	rt := s.runtime
	rtArgs := s.runtimeArgs
	if t.Runtime != "" {
		rt = t.Runtime
		rtArgs = t.RuntimeArgs
	}
	/*
		创建一个runtime.container
			==>/runtime/container.go
				==>func New
	*/
	container, err := runtime.New(runtime.ContainerOpts{
		Root:        s.stateDir,
		ID:          t.ID,
		Bundle:      t.BundlePath,
		Runtime:     rt,
		RuntimeArgs: rtArgs,
		Shim:        s.shim,
		Labels:      t.Labels,
		NoPivotRoot: t.NoPivotRoot,
		Timeout:     s.timeout,
	})
	if err != nil {
		return err
	}
	/*
		记录到Supervisor.containers[]中
		容器数量＋1
	*/
	s.containers[t.ID] = &containerInfo{
		container: container,
	}
	ContainersCounter.Inc(1)
	//把runtime.container封装到type startTask struct中
	task := &startTask{
		Err:           t.ErrorCh(),
		Container:     container,
		StartResponse: t.StartResponse,
		Stdin:         t.Stdin,
		Stdout:        t.Stdout,
		Stderr:        t.Stderr,
	}
	if t.Checkpoint != nil {
		task.CheckpointPath = filepath.Join(t.CheckpointDir, t.Checkpoint.Name)
	}

	/*
		把task发给Supervisor的startTasks  chan *startTask
	*/
	s.startTasks <- task
	ContainerCreateTimer.UpdateSince(start)
	return errDeferredResponse
}
```

## Supervisor的worker
见/supervisor/worker.go，前面说到containerd的主线程里面会起10个go groutine来启动worker
```go
type worker struct {
	wg *sync.WaitGroup
	s  *Supervisor
}

// NewWorker return a new initialized worker
func NewWorker(s *Supervisor, wg *sync.WaitGroup) Worker {
	return &worker{
		s:  s,
		wg: wg,
	}
}
```

### worker的Start()
负责调用containerd-shim，监控容器的进程，把结果回传给StartResponse  chan StartResponse。 

本函数的重点是：
1. Container.Start()，会通过containerd-shim调用`runc create {containerID}`命令
2. process.Start()中会调用`runc start {containerID}`命令启动容器中的进程
```go
// Start runs a loop in charge of starting new containers
/*
	处理创建新的container，本函数的重点是
		1. Container.Start()，会通过containerd-shim调用`runc create {containerID}`命令
		2. process.Start()中会调用`runc start {containerID}`命令启动容器中的进程
*/
func (w *worker) Start() {
	defer w.wg.Done()
	/*消费startTasks  chan *startTask*/
	for t := range w.s.startTasks {
		started := time.Now()
		/*
			通过containerd-shim调用`runc create {containerID}`命令
			==>/runtime/container.go
				==>func (c *container) Start(checkpointPath string, s Stdio)
			其返回值是容器的进程
			process is:  &{/var/run/docker/libcontainerd/containerd/rew/init init 11436 0xc42000e6c8 0xc42000e6d0 0xc42008afd0 {true {0 0 []} [sh] [PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin TERM=xterm] / [CAP_AUDIT_WRITE CAP_KILL CAP_NET_BIND_SERVICE] [{RLIMIT_NOFILE 1024 1024}] true  } {/tmp/ctr-377229724/stdin /tmp/ctr-377229724/stdout /tmp/ctr-377229724/stderr} 0xc42009d1e0 false 0xc420240000 running {0 0} 426461}
		*/
		process, err := t.Container.Start(t.CheckpointPath, runtime.NewStdio(t.Stdin, t.Stdout, t.Stderr))
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
				"id":    t.Container.ID(),
			}).Error("containerd: start container")
			t.Err <- err
			//启动失败，创建DeleteTask，并放入tasks中
			evt := &DeleteTask{
				ID:      t.Container.ID(),
				NoEvent: true,
				Process: process,
			}
			w.s.SendTask(evt)
			continue
		}
		/*
			把该容器加入oom监控列表中
		*/
		if err := w.s.monitor.MonitorOOM(t.Container); err != nil && err != runtime.ErrContainerExited {
			if process.State() != runtime.Stopped {
				logrus.WithField("error", err).Error("containerd: notify OOM events")
			}
		}
		/*
			监控容器的进程
			这里就和/supervisor/supervisor.go中的func New中处理exit的进程对应上了
		*/
		if err := w.s.monitorProcess(process); err != nil {
			logrus.WithField("error", err).Error("containerd: add process to monitor")
			t.Err <- err
			evt := &DeleteTask{
				ID:      t.Container.ID(),
				NoEvent: true,
				Process: process,
			}
			w.s.SendTask(evt)
			continue
		}
		/*
			process.Start()中会调用`runc start {containerID}`命令启动容器
				==>/runtime/process.go
					==>func (p *process) Start() error
		*/
		if err := process.Start(); err != nil {
			logrus.WithField("error", err).Error("containerd: start init process")
			t.Err <- err
			evt := &DeleteTask{
				ID:      t.Container.ID(),
				NoEvent: true,
				Process: process,
			}
			w.s.SendTask(evt)
			continue
		}
		ContainerStartTimer.UpdateSince(started)
		t.Err <- nil
		/*
			结果回传给StartResponse  chan StartResponse
				和/api/grpc/server/server.go中的等待对应上
		*/
		t.StartResponse <- StartResponse{
			Container: t.Container,
		}
		w.s.notifySubscribers(Event{
			Timestamp: time.Now(),
			ID:        t.Container.ID(),
			Type:      StateStart,
		})
	}
}
```

至此，整个containerd的数据脉络图基本清楚。 后面分别对Container和process进行分析。

