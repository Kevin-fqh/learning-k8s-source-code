# runc create命令

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [createCommand](#createcommand)
    - [startContainer](#startcontainer)
    - [createContainer](#createcontainer)
  - [libcontainer.Factory](#libcontainerfactory)
    - [loadFactory](#loadfactory)
    - [LinuxFactory.Create](#linuxfactorycreate)
  - [type runner struct](#type-runner-struct)
  - [linuxContainer.Start](#linuxcontainerstart)
  - [linuxContainer.newParentProcess](#linuxcontainernewparentprocess)
<!-- END MUNGE: GENERATED_TOC -->

`docker start`命令，经历了`containerd`，`container-shim`的中转之后，最后会调用三个`runc`命令：
```shell
runc create
/proc/self/exe init 
runc start
```
其实`ctr start {containerID}`调用的也是这三个命令。

本文先讲解runc creata命令，其实执行`runc create`的时候，会自动调用`/proc/self/exe init`，然后通过FIFO管道来实现阻塞的效果。

## createCommand
见/runc-1.0.0-rc2/create.go
```go
var createCommand = cli.Command{
	Name:  "create",
	Usage: "create a container",
	ArgsUsage: `<container-id>
	...
	...
	...
	/*
		声明了`runc create`子命令的动作Action
	*/
	Action: func(context *cli.Context) error {
		//初始化设置,如加载config.json
		spec, err := setupSpec(context)
		if err != nil {
			return err
		}
		/*
			==>/utils_linux.go
				==>func startContainer
		*/
		status, err := startContainer(context, spec, true)
		if err != nil {
			return err
		}
		// exit with the container's exit status so any external supervisor is
		// notified of the exit with the correct exit status.
		/*
			父进程(runc create进程本身)exit了
			用户期待在容器内运行的进程还活着
			子进程会被container-shim接管？
			这是为什么呢？默认情况下，应该是被pid=1的系统Init进程接管才对？
			这是因为通过系统调用设置了container-shim进程的"child subreaper"属性
		*/
		os.Exit(status)
		return nil
	},
}
```

### startContainer
1. 生成一个libcontainer.Container，状态处于 stopped/destroyed
2. 然后把libcontainer.Container封装到type runner struct对象中
3. 通过runner.run来把容器中进程给跑起来
```go
func startContainer(context *cli.Context, spec *specs.Spec, create bool) (int, error) {
	id := context.Args().First()
	if id == "" {
		return -1, errEmptyID
	}
	/*
		生成一个libcontainer.Container
		状态处于 stopped/destroyed
	*/
	container, err := createContainer(context, id, spec)
	if err != nil {
		return -1, err
	}
	detach := context.Bool("detach")
	// Support on-demand socket activation by passing file descriptors into the container init process.
	listenFDs := []*os.File{}
	if os.Getenv("LISTEN_FDS") != "" {
		listenFDs = activation.Files(false)
	}
	/*
		把libcontainer.Container封装到type runner struct对象中
	*/
	r := &runner{
		enableSubreaper: !context.Bool("no-subreaper"),
		shouldDestroy:   true,
		container:       container,
		listenFDs:       listenFDs,
		console:         context.String("console"),
		detach:          detach,
		pidFile:         context.String("pid-file"),
		create:          create,
	}
	return r.run(&spec.Process)
}
```

### createContainer
1. 生成一个libcontainer.Factory，用于配置容器
2. 调用factory.Create()方法生成libcontainer.Container
```go
func createContainer(context *cli.Context, id string, spec *specs.Spec) (libcontainer.Container, error) {
	//设置Libcontainer的Config
	config, err := specconv.CreateLibcontainerConfig(&specconv.CreateOpts{
		CgroupName:       id,
		UseSystemdCgroup: context.GlobalBool("systemd-cgroup"),
		NoPivotRoot:      context.Bool("no-pivot"),
		NoNewKeyring:     context.Bool("no-new-keyring"),
		Spec:             spec,
	})
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(config.Rootfs); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("rootfs (%q) does not exist", config.Rootfs)
		}
		return nil, err
	}

	/*
	 生成一个libcontainer.Factory
	*/
	factory, err := loadFactory(context)
	if err != nil {
		return nil, err
	}
	/*
		调用factory.Create()方法生成libcontainer.Container
			==>/libcontainer/factory_linux.go
				==>func (l *LinuxFactory) Create(id string, config *configs.Config)
	*/
	return factory.Create(id, config)
}
```

## libcontainer.Factory
LinuxFactory用于设置容器的参数
```go
// LinuxFactory implements the default factory interface for linux based systems.
type LinuxFactory struct {
	// Root directory for the factory to store state.
	/*
		factory 存放数据的根目录  默认是 /run/runc
		而/run/runc/{containerID} 目录下，会有两个文件：一个是管道exec.fifo
													一个是state.json
	*/
	Root string

	// InitArgs are arguments for calling the init responsibilities for spawning
	// a container.
	/*
		用于设置 init命令 ，固定是 InitArgs:  []string{"/proc/self/exe", "init"},
	*/
	InitArgs []string

	// CriuPath is the path to the criu binary used for checkpoint and restore of
	// containers.
	// 用于checkpoint and restore
	CriuPath string

	// Validator provides validation to container configurations.
	Validator validate.Validator

	// NewCgroupsManager returns an initialized cgroups manager for a single container.
	// 初始化一个针对单个容器的cgroups manager
	NewCgroupsManager func(config *configs.Cgroup, paths map[string]string) cgroups.Manager
}
```

### loadFactory
返回用于运行容器的配置工厂。
```go
// loadFactory returns the configured factory instance for execing containers.
func loadFactory(context *cli.Context) (libcontainer.Factory, error) {
	/*
		factory 存放数据的根目录  默认是 /run/runc

		而/run/runc/{containerID} 目录下，会有两个文件：一个是管道exec.fifo
													一个是state.json
	*/
	root := context.GlobalString("root")
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	/*
		用于生成一个CgroupsManager
			==>/libcontainer/factory_linux.go
				==>func Cgroupfs(l *LinuxFactory) error
	*/
	cgroupManager := libcontainer.Cgroupfs
	if context.GlobalBool("systemd-cgroup") {
		if systemd.UseSystemd() {
			cgroupManager = libcontainer.SystemdCgroups
		} else {
			return nil, fmt.Errorf("systemd cgroup flag passed, but systemd support for managing cgroups is not available")
		}
	}
	/*
		生成一个configured factory
		==>/libcontainer/factory_linux.go
			==>func New(root string, options ...func(*LinuxFactory) error) (Factory, error)
		可变参数是两个函数。。。。
	*/
	return libcontainer.New(abs, cgroupManager, libcontainer.CriuPath(context.GlobalString("criu")))
}
```

### LinuxFactory.Create
1. 生成一个/run/runc/{containerID}/exec.fifo，管道
2. 创建一个linuxContainer对象，状态处于 stopped
```go
func (l *LinuxFactory) Create(id string, config *configs.Config) (Container, error) {
	if l.Root == "" {
		return nil, newGenericError(fmt.Errorf("invalid root"), ConfigInvalid)
	}
	if err := l.validateID(id); err != nil {
		return nil, err
	}
	if err := l.Validator.Validate(config); err != nil {
		return nil, newGenericError(err, ConfigInvalid)
	}
	uid, err := config.HostUID()
	if err != nil {
		return nil, newGenericError(err, SystemError)
	}
	gid, err := config.HostGID()
	if err != nil {
		return nil, newGenericError(err, SystemError)
	}
	/*
		/run/runc/{containerID}
	*/
	containerRoot := filepath.Join(l.Root, id)
	if _, err := os.Stat(containerRoot); err == nil {
		return nil, newGenericError(fmt.Errorf("container with id exists: %v", id), IdInUse)
	} else if !os.IsNotExist(err) {
		return nil, newGenericError(err, SystemError)
	}
	if err := os.MkdirAll(containerRoot, 0711); err != nil {
		return nil, newGenericError(err, SystemError)
	}
	if err := os.Chown(containerRoot, uid, gid); err != nil {
		return nil, newGenericError(err, SystemError)
	}
	/*
		/run/runc/{containerID}/exec.fifo
	*/
	fifoName := filepath.Join(containerRoot, execFifoFilename)
	/*
		syscall.Umask 通过设置一些位来禁止一些权限，改成0000之后，会使得子进程的umask也为0000
		第一位始终是0不能被设置
		000表示创建的时候不屏蔽任何人open调用的读写执行设置
		可以用chmod更改
		http://blog.chinaunix.net/uid-9525959-id-2001810.html
	*/
	oldMask := syscall.Umask(0000)
	if err := syscall.Mkfifo(fifoName, 0622); err != nil {
		syscall.Umask(oldMask)
		return nil, newGenericError(err, SystemError)
	}
	syscall.Umask(oldMask)
	if err := os.Chown(fifoName, uid, gid); err != nil {
		return nil, newGenericError(err, SystemError)
	}
	/*
		创建好了一个linuxContainer对象
			==>/libcontainer/container_linux.go
				==>type linuxContainer struct
	*/
	c := &linuxContainer{
		id:            id,
		root:          containerRoot,
		config:        config,
		initArgs:      l.InitArgs, /* /proc/self/exe init */
		criuPath:      l.CriuPath,
		cgroupManager: l.NewCgroupsManager(config.Cgroups, nil),
	}
	/*
		状态处于 stopped
		==>/libcontainer/state_linux.go
			==>type stoppedState struct
	*/
	c.state = &stoppedState{c: c}
	return c, nil
}
```

## type runner struct
分析runner.run()流程如下：
1. 根据config.json来设置将要在容器中执行的process
2. runc create ,调用container.Start(process)==>func (c *linuxContainer) Start(process *Process)
3. runc start，调用container.Run(process)==>func (c *linuxContainer) Run(process *Process)
				
```go
type runner struct {
	enableSubreaper bool
	shouldDestroy   bool
	detach          bool
	listenFDs       []*os.File
	pidFile         string
	console         string
	container       libcontainer.Container
	create          bool
}

func (r *runner) run(config *specs.Process) (int, error) {
	/*
		根据config.json来设置将要在容器中执行的process
	*/
	process, err := newProcess(*config)
	if err != nil {
		//出错的话，销毁掉该runner，一个容器对应一个runner
		r.destroy()
		return -1, err
	}
	if len(r.listenFDs) > 0 {
		/*将listenFDs加入process的环境变量和需要在新进程保持打开的文件列表中（ExtraFiles）*/
		process.Env = append(process.Env, fmt.Sprintf("LISTEN_FDS=%d", len(r.listenFDs)), "LISTEN_PID=1")
		process.ExtraFiles = append(process.ExtraFiles, r.listenFDs...)
	}
	rootuid, err := r.container.Config().HostUID()
	if err != nil {
		r.destroy()
		return -1, err
	}
	rootgid, err := r.container.Config().HostGID()
	if err != nil {
		r.destroy()
		return -1, err
	}
	// io和tty配置
	tty, err := setupIO(process, rootuid, rootgid, r.console, config.Terminal, r.detach || r.create)
	if err != nil {
		r.destroy()
		return -1, err
	}
	/*
		创建一个signalHandler来处理tty和signal
	*/
	handler := newSignalHandler(tty, r.enableSubreaper)
	/*
		runc create ,调用container.Start(process)
		runc start，调用container.Run(process)
			==>/libcontainer/container_linux.go
				==>func (c *linuxContainer) Start(process *Process)
				==>func (c *linuxContainer) Run(process *Process)
	*/
	startFn := r.container.Start
	if !r.create {
		startFn = r.container.Run
	}
	defer tty.Close()
	if err := startFn(process); err != nil {
		r.destroy()
		return -1, err
	}
	if err := tty.ClosePostStart(); err != nil {
		r.terminate(process)
		r.destroy()
		return -1, err
	}
	if r.pidFile != "" {
		if err := createPidFile(r.pidFile, process); err != nil {
			r.terminate(process)
			r.destroy()
			return -1, err
		}
	}
	if r.detach || r.create {
		return 0, nil
	}
	status, err := handler.forward(process)
	if err != nil {
		r.terminate(process)
	}
	r.destroy()
	return status, err
}
```

## linuxContainer.Start
见/libcontainer/container_linux.go

1. linuxContainer.newParentProcess组装将要执行的命令parent
2. parent.start()会根据parent的类型来选择对应的start()，自此之后，将进入`/proc/self/exe init`，也就是`runc init`
3. 容器的状态是 created
```go
func (c *linuxContainer) Start(process *Process) error {
	c.m.Lock()
	defer c.m.Unlock()
	status, err := c.currentStatus()
	if err != nil {
		return err
	}
	return c.start(process, status == Stopped)
}

func (c *linuxContainer) start(process *Process, isInit bool) error {
	parent, err := c.newParentProcess(process, isInit)
	if err != nil {
		return newSystemErrorWithCause(err, "creating new parent process")
	}
	/*
		异步启动parent进程
		如果是Init进程，将执行runc 的initCommand（原来手工启动的runc create进程是init进程的父进程）
		也就是clone出一个namespace隔离的新进程之后，再调用/proc/self/exe init
			==>/runc-1.0.0-rc2/main_unix.go
				==>var initCommand = cli.Command

		parent.start()会根据parent的类型来选择对应的start()
			==>/libcontainer/process_linux.go
				==>func (p *initProcess) start()
				==>func (p *setnsProcess) start()
	*/
	if err := parent.start(); err != nil {
		// terminate the process to ensure that it properly is reaped.
		if err := parent.terminate(); err != nil {
			logrus.Warn(err)
		}
		return newSystemErrorWithCause(err, "starting container process")
	}
	// generate a timestamp indicating when the container was started
	c.created = time.Now().UTC()
	c.state = &runningState{
		c: c,
	}
	if isInit {
		// 记录下 Init 进程的信息，容器状态处于 created
		c.state = &createdState{
			c: c,
		}
		//把此时的容器信息持久化到state.json文件，此刻对应的状态是 created
		state, err := c.updateState(parent)
		if err != nil {
			return err
		}
		c.initProcessStartTime = state.InitProcessStartTime

		// 遍历spec里面的Poststart hook，分别调用
		if c.config.Hooks != nil {
			s := configs.HookState{
				Version:    c.config.Version,
				ID:         c.id,
				Pid:        parent.pid(),
				Root:       c.config.Rootfs,
				BundlePath: utils.SearchLabels(c.config.Labels, "bundle"),
			}
			for i, hook := range c.config.Hooks.Poststart {
				if err := hook.Run(s); err != nil {
					if err := parent.terminate(); err != nil {
						logrus.Warn(err)
					}
					return newSystemErrorWithCausef(err, "running poststart hook %d", i)
				}
			}
		}
	}
	return nil
}
```

## linuxContainer.newParentProcess
1. 组装将要在容器中运行的cmd，对于执行`docker start`的时候，组装出来的命令是`/proc/self/exe init`，也就是`runc init`
2. 通过无名管道`parentPipe, childPipe, err := newPipe()`来在`runc create`和`runc init`这对父子进程之间通信
```go
func (c *linuxContainer) newParentProcess(p *Process, doInit bool) (parentProcess, error) {
	/*
		管道pipe 用于两个进程的通信
	*/
	parentPipe, childPipe, err := newPipe()
	if err != nil {
		return nil, newSystemErrorWithCause(err, "creating new init pipe")
	}
	rootDir, err := os.Open(c.root)
	if err != nil {
		return nil, err
	}
	/*
		组装将要运行的cmd
		对于Init的时候，cmd 是  /proc/self/exe init
	*/
	cmd, err := c.commandTemplate(p, childPipe, rootDir)
	if err != nil {
		return nil, newSystemErrorWithCause(err, "creating new command template")
	}
	if !doInit {
		//Init之后，用用户自定义的cmd来替换Init进程
		return c.newSetnsProcess(p, cmd, parentPipe, childPipe, rootDir)
	}
	// Init时
	return c.newInitProcess(p, cmd, parentPipe, childPipe, rootDir)
}

func (c *linuxContainer) commandTemplate(p *Process, childPipe, rootDir *os.File) (*exec.Cmd, error) {
	cmd := exec.Command(c.initArgs[0], c.initArgs[1:]...)
	cmd.Stdin = p.Stdin
	cmd.Stdout = p.Stdout
	cmd.Stderr = p.Stderr
	cmd.Dir = c.config.Rootfs
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	/*
		表示把childPipe传递给子进程
	*/
	cmd.ExtraFiles = append(p.ExtraFiles, childPipe, rootDir)
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("_LIBCONTAINER_INITPIPE=%d", stdioFdCount+len(cmd.ExtraFiles)-2),
		fmt.Sprintf("_LIBCONTAINER_STATEDIR=%d", stdioFdCount+len(cmd.ExtraFiles)-1))
	// NOTE: when running a container with no PID namespace and the parent process spawning the container is
	// PID1 the pdeathsig is being delivered to the container's init process by the kernel for some reason
	// even with the parent still running.
	if c.config.ParentDeathSignal > 0 {
		cmd.SysProcAttr.Pdeathsig = syscall.Signal(c.config.ParentDeathSignal)
	}
	return cmd, nil
}

func (c *linuxContainer) newInitProcess(p *Process, cmd *exec.Cmd, parentPipe, childPipe, rootDir *os.File) (*initProcess, error) {
	cmd.Env = append(cmd.Env, "_LIBCONTAINER_INITTYPE="+string(initStandard))
	nsMaps := make(map[configs.NamespaceType]string)
	for _, ns := range c.config.Namespaces {
		if ns.Path != "" {
			nsMaps[ns.Type] = ns.Path
		}
	}
	_, sharePidns := nsMaps[configs.NEWPID]
	/*
		设置Namespaces.CloneFlags()，6个Namespace标识
		将namespace、uid/gid mapping等信息使用 bootstrapData 函数封装为一个 io.Reader，使用的是 netlink 用于内核间的通信
	*/
	data, err := c.bootstrapData(c.config.Namespaces.CloneFlags(), nsMaps, "")
	if err != nil {
		return nil, err
	}
	return &initProcess{
		cmd:           cmd,
		childPipe:     childPipe,
		parentPipe:    parentPipe,
		manager:       c.cgroupManager,
		config:        c.newInitConfig(p),
		container:     c,
		process:       p,
		bootstrapData: data,
		sharePidns:    sharePidns,
		rootDir:       rootDir,
	}, nil
}
```

