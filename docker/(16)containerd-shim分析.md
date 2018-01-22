# containerd-shim分析

containerd-shim充当containerd和runc之间的中间件，用来组装runc命令的参数，负责容器中进程的启动。

containerd中调用shim时执行的命令如下：
```shell
/root/lib-containerd/containerd/bin/containerd-shim {containerID} {bundleDirPath} runc
```
其中三个参数如下：
  - Arg0: id of the container
  - Arg1: bundle path
  - Arg2: runtime binary

## func main()
```go
func main() {
	/*
		os.Args[:] 的值如下：
		/root/lib-containerd/containerd/bin/containerd-shim redis /home/fqh-runc-container-test/redis runc
	*/
	flag.Parse()
	cwd, err := os.Getwd() //获取当前进程目录cwd, /run/docker/libcontainerd/containerd/{containerID}/init
	if err != nil {
		panic(err)
	}
	/*
		打开shim的日志文件
		/var/run/docker/libcontainerd/containerd/{containerID}/init/shim-log.json
	*/
	f, err := os.OpenFile(filepath.Join(cwd, "shim-log.json"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0666)
	if err != nil {
		panic(err)
	}
	/*
		启动容器
	*/
	if err := start(f); err != nil {
		/*启动失败时，把错误信息err记录到shim的log文件f中*/
		// this means that the runtime failed starting the container and will have the
		// proper error messages in the runtime log so we should to treat this as a
		// shim failure because the sim executed properly
		if err == errRuntime {
			f.Close()
			return
		}
		// log the error instead of writing to stderr because the shim will have
		// /dev/null as it's stdio because it is supposed to be reparented to system
		// init and will not have anyone to read from it
		writeMessage(f, "error", err)
		f.Close()
		os.Exit(1)
	}
}
```

## func start
核心步骤是基于containerID，bundle，runtimeName 构建一个进程对象，即type process struct。 
然后`p.create()`调用runc命令启动进程。
```go
func start(log *os.File) error {
	// start handling signals as soon as possible so that things are properly reaped
	// or if runtime exits before we hit the handler
	/*
		尽可能快地处理信号
		使用os的 func Notify(c chan<- os.Signal, sig ...os.Signal) 把监听到的信号存放到channel signals中
	*/
	signals := make(chan os.Signal, 2048)
	signal.Notify(signals)
	// set the shim as the subreaper for all orphaned processes created by the container
	/*
		把shim作为所有由容器创建的孤儿进程的回收者
			==>通过系统调用设置了container-shim进程的"child subreaper"属性，
				==>/containerd-0.2.3/osutils/prctl.go
					==>func SetSubreaper(i int) error
		入参为 1
			==>表示本进程是 下面所有子进程(孙子进程...)的 pid=1的Init进程
				充当孤儿进程挂靠pid=1的Init进程的效果
	*/
	if err := osutils.SetSubreaper(1); err != nil {
		return err
	}
	// open the exit pipe
	/*
		打开exit管道(p)，用来发送退出信号
		# ll /var/run/docker/libcontainerd/containerd/redis/init/
		总用量 12
		prwxr-xr-x. 1 root root   0 12月  8 10:17 control
		prwxr-xr-x. 1 root root   0 12月  8 10:17 exit
		-rw-r--r--. 1 root root   0 12月  8 10:17 log.json
		-rw-r--r--. 1 root root   4 12月  8 10:17 pid
		-rw-r--r--. 1 root root 540 12月  8 10:17 process.json
		-rw-r--r--. 1 root root   0 12月  8 10:17 shim-log.json
		-rw-r--r--. 1 root root   7 12月  8 10:17 starttime
	*/
	f, err := os.OpenFile("exit", syscall.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	/*
		打开control管道，可发送和接收容器控制信息
		/var/run/docker/libcontainerd/containerd/redis/init/control
	*/
	control, err := os.OpenFile("control", syscall.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer control.Close()
	/*
		基于containerID，bundle，runtimeName 构建一个进程对象，即type process struct
		redis
		/home/fqh-runc-container-test/redis
		runc
	*/
	p, err := newProcess(flag.Arg(0), flag.Arg(1), flag.Arg(2))
	if err != nil {
		return err
	}
	defer func() {
		if err := p.Close(); err != nil {
			writeMessage(log, "warn", err)
		}
	}()
	/*
		**********************
		**********************
		**********************
		创建进程，调用`runc`命令
	*/
	if err := p.create(); err != nil {
		p.delete()
		return err
	}
	/*
		创建控制信息通道 channel controlMessage
	*/
	msgC := make(chan controlMessage, 32)
	go func() {
		/*
			一个死循环，用于接收信号
			将control管道的信息格式化写入channel controlMessage

			***要学会使用！！Linux系统管道和go中channel通信***
		*/
		for {
			var m controlMessage
			if _, err := fmt.Fscanf(control, "%d %d %d\n", &m.Type, &m.Width, &m.Height); err != nil {
				continue
			}
			msgC <- m
		}
	}()
	var exitShim bool
	/*
		这里有个死循环
		==>在正常情况下，containerd-shim是不会主动退出
	*/
	for {
		select {
		case s := <-signals: //监听信号通道signals
			switch s {
			case syscall.SIGCHLD:
				exits, _ := osutils.Reap()
				for _, e := range exits {
					// check to see if runtime is one of the processes that has exited
					if e.Pid == p.pid() {
						exitShim = true
						writeInt("exitStatus", e.Status)
					}
				}
			}
			// runtime has exited so the shim can also exit
			if exitShim {
				// Let containerd take care of calling the runtime delete
				f.Close()
				p.Wait()
				return nil
			}
		case msg := <-msgC:
			/*
				监听控制信息通道 controlMessage
				0为关闭进程输入
				1为调整窗口大小
			*/
			switch msg.Type {
			case 0:
				// close stdin
				if p.stdinCloser != nil {
					p.stdinCloser.Close()
				}
			case 1:
				if p.console == nil {
					continue
				}
				ws := term.Winsize{
					Width:  uint16(msg.Width),
					Height: uint16(msg.Height),
				}
				term.SetWinsize(p.console.Fd(), &ws)
			}
		}
	}
	return nil
}
```

## type process struct
```go
type process struct {
	sync.WaitGroup
	id             string
	bundle         string
	stdio          *stdio
	exec           bool
	containerPid   int
	checkpoint     *checkpoint
	checkpointPath string
	shimIO         *IO
	stdinCloser    io.Closer
	console        *os.File
	consolePath    string
	state          *processState
	runtime        string
}
```
### func newProcess
```go
func newProcess(id, bundle, runtimeName string) (*process, error) {
	p := &process{
		id:      id,
		bundle:  bundle,
		runtime: runtimeName,
	}
	/*
		读取该进程的process.json文件，得到一个type processState struct对象
	*/
	s, err := loadProcess()
	if err != nil {
		return nil, err
	}
	p.state = s
	if s.CheckpointPath != "" {
		cpt, err := loadCheckpoint(s.CheckpointPath)
		if err != nil {
			return nil, err
		}
		p.checkpoint = cpt
		p.checkpointPath = s.CheckpointPath
	}
	/*
		打开进程的输入输出端
	*/
	if err := p.openIO(); err != nil {
		return nil, err
	}
	return p, nil
}
```
### func create()
组装runc的参数，然后执行，自此之后，将进入`runc`的源码解读
```go
func (p *process) create() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	//设置runc日志路径
	logPath := filepath.Join(cwd, "log.json")
	/*
		开始根据不同情况组装`runc`的参数
	*/
	args := append([]string{
		"--log", logPath,
		"--log-format", "json",
	}, p.state.RuntimeArgs...)
	/*`docker exec`时标记为true了 */
	if p.state.Exec {
		args = append(args, "exec",
			"-d",
			"--process", filepath.Join(cwd, "process.json"),
			"--console", p.consolePath,
		)
	} else if p.checkpoint != nil {
		args = append(args, "restore",
			"--image-path", p.checkpointPath,
			"--work-path", filepath.Join(p.checkpointPath, "criu.work", "restore-"+time.Now().Format(time.RFC3339)),
		)
		add := func(flags ...string) {
			args = append(args, flags...)
		}
		if p.checkpoint.Shell {
			add("--shell-job")
		}
		if p.checkpoint.TCP {
			add("--tcp-established")
		}
		if p.checkpoint.UnixSockets {
			add("--ext-unix-sk")
		}
		if p.state.NoPivotRoot {
			add("--no-pivot")
		}
		for _, ns := range p.checkpoint.EmptyNS {
			add("--empty-ns", ns)
		}

	} else {
		/*
			初始化进程走这通道
		*/
		args = append(args, "create",
			"--bundle", p.bundle,
			"--console", p.consolePath,
		)
		if p.state.NoPivotRoot {
			args = append(args, "--no-pivot")
		}
	}
	args = append(args,
		"--pid-file", filepath.Join(cwd, "pid"),
		p.id,
	)
	/*
		start=>/root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers start redis /home/fqh-runc-container-test/redis
			args is: --log /run/docker/libcontainerd/containerd/redis/init/log.json --log-format json create --bundle /home/fqh-runc-container-test/redis --console /dev/pts/2 --pid-file /run/docker/libcontainerd/containerd/redis/init/pid redis

		exec==>/root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers exec -id redis --pid 33  --cwd /bin --tty --attach ./date
			args is: --log /run/docker/libcontainerd/containerd/redis/33/log.json --log-format json exec -d --process /run/docker/libcontainerd/containerd/redis/33/process.json --console /dev/pts/3 --pid-file /run/docker/libcontainerd/containerd/redis/33/pid redis
	*/
	/*
		构造`runc` 命令
	*/
	cmd := exec.Command(p.runtime, args...)
	cmd.Dir = p.bundle
	cmd.Stdin = p.stdio.stdin
	cmd.Stdout = p.stdio.stdout
	cmd.Stderr = p.stdio.stderr
	// Call out to setPDeathSig to set SysProcAttr as elements are platform specific
	cmd.SysProcAttr = setPDeathSig()

	/*
		start `runc` 命令
	*/
	if err := cmd.Start(); err != nil {
		if exErr, ok := err.(*exec.Error); ok {
			if exErr.Err == exec.ErrNotFound || exErr.Err == os.ErrNotExist {
				return fmt.Errorf("%s not installed on system", p.runtime)
			}
		}
		return err
	}
	p.stdio.stdout.Close()
	p.stdio.stderr.Close()
	//等待命令执行完毕
	if err := cmd.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return errRuntime
		}
		return err
	}
	/*
		读取pid文件信息，得到进程pid数据
		pid文件中信息由runc写入
	*/
	data, err := ioutil.ReadFile("pid")
	if err != nil {
		return err
	}
	//string转int
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return err
	}
	/*
		回填容器的属性pid
	*/
	p.containerPid = pid
	return nil
}
```