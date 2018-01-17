# containerd之container和process

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type container struct](#type-container-struct)
  - [type process struct](#type-process-struct)

<!-- END MUNGE: GENERATED_TOC -->

前面说到，Supervisor的worker会对task进行处理，一个request对应一个task。

## type container struct
```go
type container struct {
	// path to store runtime state information
	root        string //containerd的工作目录，属性 --state-dir /var/run/docker/libcontainerd/containerd
	id          string
	bundle      string
	runtime     string
	runtimeArgs []string
	shim        string
	processes   map[string]*process
	labels      []string
	oomFds      []int
	/*
		比较chroot和pivot_root
			==>http://blog.csdn.net/linuxchyu/article/details/21109335
			==>http://hustcat.github.io/namespace-implement-1/
	*/
	noPivotRoot bool
	timeout     time.Duration
}

// New returns a new container
/*
	生成一个container，并把state信息记录在state.json文件中
*/
func New(opts ContainerOpts) (Container, error) {
	c := &container{
		root:        opts.Root,
		id:          opts.ID,
		bundle:      opts.Bundle,
		labels:      opts.Labels,
		processes:   make(map[string]*process),
		runtime:     opts.Runtime,
		runtimeArgs: opts.RuntimeArgs,
		shim:        opts.Shim,
		noPivotRoot: opts.NoPivotRoot,
		timeout:     opts.Timeout,
	}
	//创建state.json
	if err := os.Mkdir(filepath.Join(c.root, c.id), 0755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(c.root, c.id, StateFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(state{
		Bundle:      c.bundle,
		Labels:      c.labels,
		Runtime:     c.runtime,
		RuntimeArgs: c.runtimeArgs,
		Shim:        c.shim,
		NoPivotRoot: opts.NoPivotRoot,
	}); err != nil {
		return nil, err
	}
	return c, nil
}
```
先来看看两个类似的函数`Start()`和`Exec()`，分别对应`docker start`和`docker exec`，两者最后都会调用`createCmd()`

### container的Start()
```go
func (c *container) Start(checkpointPath string, s Stdio) (Process, error) {
	/*
		/var/run/docker/libcontainerd/containerd/{containerID}/init
		创建容器初始化进程工作的根目录
	*/
	processRoot := filepath.Join(c.root, c.id, InitProcessID)
	if err := os.Mkdir(processRoot, 0755); err != nil {
		return nil, err
	}
	/*
		生成命令cmd：shim {containerID} {bundleDirPath} runc，工作目录为process目录processRoot；
		containerd-shim 会调用`runc create {containerID}`
	*/
	cmd := exec.Command(c.shim,
		c.id, c.bundle, c.runtime,
	)
	cmd.Dir = processRoot //指定cmd命令执行的工作目录，这里是init命令
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	/*
		读取bundle目录下的config.json文件
	*/
	spec, err := c.readSpec()
	if err != nil {
		return nil, err
	}
	config := &processConfig{
		checkpoint: checkpointPath,
		root:       processRoot,
		/*InitProcessID = "init" 所有容器的初始化进程*/
		id:          InitProcessID,
		c:           c,
		stdio:       s,
		spec:        spec,
		processSpec: specs.ProcessSpec(spec.Process),
	}
	/*
		根据构建的进程配置文件config，创建一个type process struct对象
	*/
	p, err := newProcess(config)
	if err != nil {
		return nil, err
	}
	/*
		`docker run`启动初始cmd
		`/root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers start redi /home/fqh-runc-container-test/redis`
		start cmd is:  &{/root/lib-containerd/containerd/bin/containerd-shim [/root/lib-containerd/containerd/bin/containerd-shim redi /home/fqh-runc-container-test/redis runc] [] /var/run/docker/libcontainerd/containerd/redi/init <nil> <nil> <nil> [] 0xc420146f30 <nil> <nil> <nil> <nil> false [] [] [] [] <nil> <nil>}
		进程p is: process is:  &{/var/run/docker/libcontainerd/containerd/redi/init init 0 0xc42000e838 0xc42000e840 0xc4202700b0 {true {0 0 []} [sh] [PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin TERM=xterm] / [CAP_AUDIT_WRITE CAP_KILL CAP_NET_BIND_SERVICE] [{RLIMIT_NOFILE 1024 1024}] true  } {/tmp/ctr-229053666/stdin /tmp/ctr-229053666/stdout /tmp/ctr-229053666/stderr} 0xc420264580 false 0xc42025ac60 running {0 0} }*/
	if err := c.createCmd(InitProcessID, cmd, p); err != nil {
		return nil, err
	}
	return p, nil
}
```

### func Exec()
`docker exec cmd`根据传入的参数生成一个p Process，此时，宿主机上会再执行一个`contaninerd-shim`，把cmd传递给`contaninerd-shim`。 
加上`docker start`时的init进程，宿主机上会同时存在两个`contaninerd-shim`进程。
```go
func (c *container) Exec(pid string, pspec specs.ProcessSpec, s Stdio) (pp Process, err error) {
	/*
		生成exec cmd的工作目录
	*/
	processRoot := filepath.Join(c.root, c.id, pid)
	if err := os.Mkdir(processRoot, 0755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.RemoveProcess(pid)
		}
	}()
	/*
		和初始化进程类似，通过containerd-shim执行
		cmd.Dir = processRoot ，其值和初始化进程是不一样的
		shim正是根据process.json中的相关属性来判断是Start()还是Exec()
	*/
	cmd := exec.Command(c.shim,
		c.id, c.bundle, c.runtime,
	)
	cmd.Dir = processRoot //指定cmd的工作目录，containerd-shim正是通过这来获取此次执行的命令到底是什么？是date？还是sh？还是bin/bash？
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	spec, err := c.readSpec()
	if err != nil {
		return nil, err
	}
	/*
		exec的config需标识exec: true
		containerd-shim给runc组装参数时，根据这来区分`docke exec`和`docker start`
	*/
	config := &processConfig{
		exec:        true,
		id:          pid,
		root:        processRoot,
		c:           c,
		processSpec: pspec,
		spec:        spec,
		stdio:       s,
	}
	//config会写入process.json文件
	p, err := newProcess(config)
	if err != nil {
		return nil, err
	}
	/*
		`docke exec ./date`
		`/root/lib-containerd/containerd/bin/ctr --address unix:///var/run/docker/libcontainerd/docker-containerd.sock containers exec -id redi --pid 333  --cwd /bin --tty --attach ./date`
		exec cmd is: &{/root/lib-containerd/containerd/bin/containerd-shim [/root/lib-containerd/containerd/bin/containerd-shim redi /home/fqh-runc-container-test/redis runc] [] /var/run/docker/libcontainerd/containerd/redi/333 <nil> <nil> <nil> [] 0xc420147170 <nil> <nil> <nil> <nil> false [] [] [] [] <nil> <nil>}
		进程p is: &{/var/run/docker/libcontainerd/containerd/redi/333 333 0 0xc42000e910 0xc42000e918 0xc4202700b0 {true {0 0 []} [./date] [] /bin [] [] false  } {/tmp/ctr-685612849/stdin /tmp/ctr-685612849/stdout /tmp/ctr-685612849/stderr} 0xc420265080 false 0xc42025b680 running {0 0} }
	*/
	if err := c.createCmd(pid, cmd, p); err != nil {
		return nil, err
	}
	return p, nil
}
```

### func createCmd()
createCmd()执行一个前面Start()和Exec()生成的containerd-shim命令。

当具体容器内进程pid生成(由runc生成)后，createCmd会启动一个go routine来等待shim命令的结束。 
shim命令一般不会退出。 
当shim发生退出时，如果容器内的进程仍在运行，则需要把该进程杀死；如果容器内进程已经不存在，则无需清理工作。
```go
func (c *container) createCmd(pid string, cmd *exec.Cmd, p *process) error {
	/*
		exec ./data 时的p值：process is:  &{/var/run/docker/libcontainerd/containerd/rew2/333 333 0 0xc42000e840 0xc42000e848 0xc4201ee000 {true {0 0 []} [./date] [] /bin [] [] false  } {/tmp/ctr-630000849/stdin /tmp/ctr-630000849/stdout /tmp/ctr-630000849/stderr} 0xc42028c420 false 0xc420286f00 running {0 0} }
		初始化进程的p：会调用本函数两次

		start和exec的cmd都是cmd：containerd-shim
	*/
	p.cmd = cmd
	/*
		执行cmd containerd-shim命令
			===>转向了`containerd-shim`的源码
	*/
	if err := cmd.Start(); err != nil {
		close(p.cmdDoneCh)
		if exErr, ok := err.(*exec.Error); ok {
			if exErr.Err == exec.ErrNotFound || exErr.Err == os.ErrNotExist {
				return fmt.Errorf("%s not installed on system", c.shim)
			}
		}
		return err
	}
	go func() {
		/*
			Wait()，等待cmd执行完成
		*/
		err := p.cmd.Wait()
		if err == nil {
			p.cmdSuccess = true
		}
		/*
			V0.2.4版本在这增加了一个错误处理：
			在调用ctr kill时都会执行到，表明shim进程退出时所要做的处理
			系统中进程的启动时间和内存中记录的时间比较，查看是否为同一process
			此处如果是正常退出的话，则linux系统上进程已经不存在，所以linux系统上进程时间为空
			如果是异常退出的话，如kill -9 shim进程，则linux系统上进程仍存在，需要强制kill掉

			换而言之：Container在containerd-shim退出时需要做清理工作。如果containerd-shim已经退出，但process还在执行，
					那么执行close(p.cmdDoneCh)，关闭cmdDoneCh以通知process退出

			本版本的startTime直接记录在/var/run/docker/libcontainerd/containerd/{containerID}/init/starttime 以文件形式存在
		*/
		/*
			关闭进程的channel cmdDoneCh，如果进程状态异常(p.cmdSuccess ！= true)，会强制kill掉
				==>/runtime/process.go
					==>func (p *process) Start() error
		*/
		close(p.cmdDoneCh)
	}()
	/*
		waitForCreate，等待进程创建完成
	*/
	if err := c.waitForCreate(p, cmd); err != nil {
		return err
	}
	c.processes[pid] = p //记录成功创建的容器中进程pid
	return nil
}
```

### func Load()
func Load(), 读取container的state.json及各进程的process.json，还原container对象
```go
// Load return a new container from the matchin state file on disk.
/*
	读取container的state.json及各进程的process.json，还原container对象。
*/
func Load(root, id, shimName string, timeout time.Duration) (Container, error) {
	var s state
	f, err := os.Open(filepath.Join(root, id, StateFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	c := &container{
		root:        root,
		id:          id,
		bundle:      s.Bundle,
		labels:      s.Labels,
		runtime:     s.Runtime,
		runtimeArgs: s.RuntimeArgs,
		shim:        s.Shim,
		noPivotRoot: s.NoPivotRoot,
		processes:   make(map[string]*process),
		timeout:     timeout,
	}

	if c.shim == "" {
		c.shim = shimName
	}

	dirs, err := ioutil.ReadDir(filepath.Join(root, id))
	if err != nil {
		return nil, err
	}
	// 一个目录代表一个进程
	for _, d := range dirs {
		/*
			如果容器只有一个初始化进程，
			那么一个是init文件夹，一个是state.json文件
		*/
		if !d.IsDir() {
			continue
		}
		pid := d.Name()
		// 读取var/run/docker/libcontainerd/containerd/{containerID}/init/process.json
		s, err := readProcessState(filepath.Join(root, id, pid))
		if err != nil {
			return nil, err
		}
		p, err := loadProcess(filepath.Join(root, id, pid), pid, c, s)
		if err != nil {
			logrus.WithField("id", id).WithField("pid", pid).Debug("containerd: error loading process %s", err)
			continue
		}
		c.processes[pid] = p
	}
	return c, nil
}
```

### func readProcessState()
func readProcessState(), 读取var/run/docker/libcontainerd/containerd/{containerID}/init/process.json，获取容器中进程状态 
```go
/*
	读取var/run/docker/libcontainerd/containerd/{containerID}/init/process.json
*/
func readProcessState(dir string) (*ProcessState, error) {
	f, err := os.Open(filepath.Join(dir, "process.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s ProcessState
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}
```

### func readSpec()
readSpec()读取bundle目录下的config.json文件。
```go
/*
	readSpec()读取bundle目录下的config.json文件。
*/
func (c *container) readSpec() (*specs.Spec, error) {
	var spec specs.Spec
	f, err := os.Open(filepath.Join(c.bundle, "config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&spec); err != nil {
		return nil, err
	}
	return &spec, nil
}
```

### func Delete()
Delete()先移除containerd目录下的容器目录，然后调用runc delete id删除容器。
```go
/*
	Delete()先移除containerd目录下的容器目录，
	然后调用runc delete id删除容器。
*/
func (c *container) Delete() error {
	err := os.RemoveAll(filepath.Join(c.root, c.id))

	/*调用runc delete {containerID}*/
	args := c.runtimeArgs
	args = append(args, "delete", c.id)
	if b, derr := exec.Command(c.runtime, args...).CombinedOutput(); err != nil {
		err = fmt.Errorf("%s: %q", derr, string(b))
	}
	return err
}
```

### func RemoveProcess()
```
/*
	删除指定process的目录。在containerd中，一个process用一个目录表示。
*/
func (c *container) RemoveProcess(pid string) error {
	delete(c.processes, pid)
	return os.RemoveAll(filepath.Join(c.root, c.id, pid))
}
```

### Pause()和Resume()
```go
/*
	调用`runc pause {containerID}`挂起一个容器
*/
func (c *container) Pause() error {
	args := c.runtimeArgs
	args = append(args, "pause", c.id)
	b, err := exec.Command(c.runtime, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %q", err.Error(), string(b))
	}
	return nil
}

/*
	与Pause()相对应，Resume()恢复某一容器。
	`runc resume {containerID}`
*/
func (c *container) Resume() error {
	args := c.runtimeArgs
	args = append(args, "resume", c.id)
	b, err := exec.Command(c.runtime, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %q", err.Error(), string(b))
	}
	return nil
}
```

### Status()
```go
// Status implements the runtime Container interface.
/*
	通过runc state {containerID}获取容器的状态信息
*/
func (c *container) Status() (State, error) {
	args := c.runtimeArgs
	args = append(args, "state", c.id)

	out, err := exec.Command(c.runtime, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %q", err.Error(), out)
	}

	// We only require the runtime json output to have a top level Status field.
	var s struct {
		Status State `json:"status"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return "", err
	}
	return s.Status, nil
}
```

### waitForCreate()
```go
//等待进程process创建结束
func (c *container) waitForCreate(p *process, cmd *exec.Cmd) error {
	wc := make(chan error, 1)
	/*
		新建一个goroutine对象来循环读取pidfile（pid文件其实就记录了一行表示该进程的pid）,
		如果都到该pid的存在，表示创建成功。

		并监听读取过程中发生的错误，如果读取失败则开始分析shim-log.json及log.json来获取错误原因并返回
	*/
	go func() {
		for {
			if _, err := p.getPidFromFile(); err != nil {
				//读取失败后的处理
				if os.IsNotExist(err) || err == errInvalidPidInt {
					alive, err := isAlive(cmd)
					if err != nil {
						wc <- err
						return
					}
					if !alive {
						// runc could have failed to run the container so lets get the error
						// out of the logs or the shim could have encountered an error
						messages, err := readLogMessages(filepath.Join(p.root, "shim-log.json"))
						if err != nil {
							wc <- err
							return
						}
						for _, m := range messages {
							if m.Level == "error" {
								wc <- fmt.Errorf("shim error: %v", m.Msg)
								return
							}
						}
						// no errors reported back from shim, check for runc/runtime errors
						messages, err = readLogMessages(filepath.Join(p.root, "log.json"))
						if err != nil {
							if os.IsNotExist(err) {
								err = ErrContainerNotStarted
							}
							wc <- err
							return
						}
						for _, m := range messages {
							if m.Level == "error" {
								wc <- fmt.Errorf("oci runtime error: %v", m.Msg)
								return
							}
						}
						wc <- ErrContainerNotStarted
						return
					}
					time.Sleep(15 * time.Millisecond)
					continue
				}
				wc <- err
				return
			}
			// the pid file was read successfully
			wc <- nil
			return
		}
	}()
	select {
	//channel wc的容量为1
	case err := <-wc:
		if err != nil {
			return err
		}
		return nil
	case <-time.After(c.timeout):
		cmd.Process.Kill()
		cmd.Wait()
		return ErrContainerStartTimeout
	}
}
```

## type process struct
表示容器内部运行的一个进程
```go
/*
	表示容器内部运行的一个进程
		初始进程 init
		其它进程
		exec cmd也会产生一个进程
*/
type process struct {
	root        string
	id          string
	pid         int
	exitPipe    *os.File
	controlPipe *os.File
	container   *container
	spec        specs.ProcessSpec
	stdio       Stdio
	cmd         *exec.Cmd
	cmdSuccess  bool
	cmdDoneCh   chan struct{}
	state       State
	stateLock   sync.Mutex
}
```

### func newProcess
```go
/*
#ls /var/run/docker/libcontainerd/containerd/{containerID}/init/
control  exit  log.json  pid  process.json  shim-log.json  starttime
*/
func newProcess(config *processConfig) (*process, error) {
	p := &process{
		root:      config.root,
		id:        config.id,
		container: config.c,
		spec:      config.processSpec,
		stdio:     config.stdio,
		cmdDoneCh: make(chan struct{}),
		state:     Running,
	}
	uid, gid, err := getRootIDs(config.spec)
	if err != nil {
		return nil, err
	}
	//创建process.json文件
	f, err := os.Create(filepath.Join(config.root, "process.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	//构建进程状态文件
	ps := ProcessState{
		ProcessSpec: config.processSpec,
		Exec:        config.exec,
		PlatformProcessState: PlatformProcessState{
			Checkpoint: config.checkpoint,
			RootUID:    uid,
			RootGID:    gid,
		},
		Stdin:       config.stdio.Stdin,
		Stdout:      config.stdio.Stdout,
		Stderr:      config.stdio.Stderr,
		RuntimeArgs: config.c.runtimeArgs,
		NoPivotRoot: config.c.noPivotRoot,
	}

	//写入process.json
	if err := json.NewEncoder(f).Encode(ps); err != nil {
		return nil, err
	}
	/*
		根据ExitFile文件路径创建Mkfifo管道对象，并以O_NONBLOCK,O_RDONLY模式打开管道OpenFile等待写入的数据并读取。
		非阻塞的打开方式

		和/containerd-0.2.3/containerd-shim/main.go中的以O_WRONLY方式打开exit管道对应
			==>func start(log *os.File) error
				==>f, err := os.OpenFile("exit", syscall.O_WRONLY, 0)
	*/
	exit, err := getExitPipe(filepath.Join(config.root, ExitFile))
	if err != nil {
		return nil, err
	}
	/*
		根据ControlFile文件路径创建Mkfifo管道对象，以O_NONBLOCK,O_RDWD模式打开管道OpenFile等待写入的数据并读取
	*/
	control, err := getControlPipe(filepath.Join(config.root, ControlFile))
	if err != nil {
		return nil, err
	}
	/*
		把fifo管道exit赋给p.exitPipe
		后面由monitor的epoll机制来读取该管道
	*/
	p.exitPipe = exit
	p.controlPipe = control
	return p, nil
}
```

### func loadProcess
```go
/*
	从process.json中还原process
*/
func loadProcess(root, id string, c *container, s *ProcessState) (*process, error) {
	p := &process{
		root:      root,
		id:        id,
		container: c,
		spec:      s.ProcessSpec,
		stdio: Stdio{
			Stdin:  s.Stdin,
			Stdout: s.Stdout,
			Stderr: s.Stderr,
		},
		state: Stopped,
	}
	if _, err := p.getPidFromFile(); err != nil {
		return nil, err
	}
	if _, err := p.ExitStatus(); err != nil {
		if err == ErrProcessNotExited {
			exit, err := getExitPipe(filepath.Join(root, ExitFile))
			if err != nil {
				return nil, err
			}
			p.exitPipe = exit

			control, err := getControlPipe(filepath.Join(root, ControlFile))
			if err != nil {
				return nil, err
			}
			p.controlPipe = control

			p.state = Running
			return p, nil
		}
		return nil, err
	}
	return p, nil
}
```

### func Signal()
```go
// Signal sends the provided signal to the process
/*
	向process发送信号
*/
func (p *process) Signal(s os.Signal) error {
	return syscall.Kill(p.pid, s.(syscall.Signal))
}
```

### process的Start()
要注意的是func (p *process) Start()最终调用`runc start {containerID}`来启动容器的“init”进程。 
而container的Start()最终调用的是runc create(通过shim调用)
```go
// Start unblocks the associated container init process.
// This should only be called on the process with ID "init"
func (p *process) Start() error {
	if p.ID() == InitProcessID {
		var (
			errC = make(chan error, 1)
			args = append(p.container.runtimeArgs, "start", p.container.id)
			cmd  = exec.Command(p.container.runtime, args...)
		)
		go func() {
			/*若果runc start执行成功，向errC发送nil*/
			out, err := cmd.CombinedOutput()
			if err != nil {
				errC <- fmt.Errorf("%s: %q", err.Error(), out)
			}
			errC <- nil
		}()
		select {
		case err := <-errC: //出现error
			if err != nil {
				return err
			}
		case <-p.cmdDoneCh: //cmdDoneCh被关闭之后，判断如果不是成功执行，强制kill掉
			if !p.cmdSuccess {
				/*
					一个cmd如果是成功执行的，p.cmdSuccess = true
						==>/runtime/container.go
							==>func (c *container) createCmd(pid string, cmd *exec.Cmd, p *process)
				*/
				cmd.Process.Kill()
				cmd.Wait()
				return ErrShimExited
			}
			err := <-errC
			if err != nil {
				return err
			}
		}
	}
	return nil
}
```