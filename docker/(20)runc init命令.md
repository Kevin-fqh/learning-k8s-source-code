# runc init命令

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [initCommand](#initcommand)
  - [factory.StartInitialization](#factorystartinitialization)
    - [newContainerInit](#newcontainerinit)
  - [type linuxStandardInit struct](#type-linuxstandardinit-struct)
    - [linuxStandardInit.Init](#linuxstandardinitinit)
<!-- END MUNGE: GENERATED_TOC -->

在`runc create`中会clone出一个子进程，在子进程中调用`/proc/self/exe init`，也就是`runc init`。

## initCommand
现在已经进入之前clone出来的一个namespace隔离的子进程，在子进程中调用`runc init`来进行初始化设置。

见/runc-1.0.0-rc2/main_unix.go
```go
var initCommand = cli.Command{
	/*
		现在已经进入之前clone出来的一个namespace隔离的子进程
		在子进程中调用`runc init`来进行初始化设置
	*/
	Name:  "init",
	Usage: `initialize the namespaces and launch the process (do not call it outside of runc)`,
	Action: func(context *cli.Context) error {
		factory, _ := libcontainer.New("")
		/*
			`runc create`的时候会调用到这里
			==>/libcontainer/factory_linux.go
				==>func (l *LinuxFactory) StartInitialization()
		*/
		if err := factory.StartInitialization(); err != nil {
			// as the error is sent back to the parent there is no need to log
			// or write it to stderr because the parent process will handle this
			os.Exit(1)
		}
		panic("libcontainer: container init failed to exec")
	},
}
```

## factory.StartInitialization
1. Init进程通过管道pipe来读取父进程传送过来的信息
2. 调用func newContainerInit()，生成一个type linuxStandardInit struct对象
3. 执行linuxStandardInit.Init()
```go
// StartInitialization loads a container by opening the pipe fd from the parent to read the configuration and state
// This is a low level implementation detail of the reexec and should not be consumed externally
/*
	这是reexec的底部实现细节，不应该在外部使用
*/
func (l *LinuxFactory) StartInitialization() (err error) {
	var pipefd, rootfd int
	for _, pair := range []struct {
		k string
		v *int
	}{
		{"_LIBCONTAINER_INITPIPE", &pipefd},
		{"_LIBCONTAINER_STATEDIR", &rootfd},
	} {

		s := os.Getenv(pair.k)

		i, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("unable to convert %s=%s to int", pair.k, s)
		}
		*pair.v = i
	}
	var (
		/*
			Init进程通过管道来读取父进程传送过来的信息
		*/
		pipe = os.NewFile(uintptr(pipefd), "pipe")
		it   = initType(os.Getenv("_LIBCONTAINER_INITTYPE"))
	)
	// clear the current process's environment to clean any libcontainer
	// specific env vars.
	os.Clearenv()

	var i initer
	defer func() {
		// We have an error during the initialization of the container's init,
		// send it back to the parent process in the form of an initError.
		// If container's init successed, syscall.Exec will not return, hence
		// this defer function will never be called.
		if _, ok := i.(*linuxStandardInit); ok {
			//  Synchronisation only necessary for standard init.
			if werr := utils.WriteJSON(pipe, syncT{procError}); werr != nil {
				panic(err)
			}
		}
		if werr := utils.WriteJSON(pipe, newSystemError(err)); werr != nil {
			panic(err)
		}
		// ensure that this pipe is always closed
		pipe.Close()
	}()
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic from initialization: %v, %v", e, string(debug.Stack()))
		}
	}()
	/*
		==>/libcontainer/init_linux.go
			==>func newContainerInit
	*/
	i, err = newContainerInit(it, pipe, rootfd)
	if err != nil {
		return err
	}
	/*
		==>/libcontainer/standard_init_linux.go
			==>func (l *linuxStandardInit) Init()
	*/
	return i.Init()
}
```

### newContainerInit
`runc create {}` 时的t类型是  initStandard
```go
func newContainerInit(t initType, pipe *os.File, stateDirFD int) (initer, error) {
	var config *initConfig
	/*
		从pipe中解析出config信息
	*/
	if err := json.NewDecoder(pipe).Decode(&config); err != nil {
		return nil, err
	}
	if err := populateProcessEnvironment(config.Env); err != nil {
		return nil, err
	}
	/*
		`runc create {}` 时的t类型是  initStandard
	*/
	switch t {
	case initSetns:
		return &linuxSetnsInit{
			config: config,
		}, nil
	case initStandard:
		/*
			==>/libcontainer/standard_init_linux.go
				==>type linuxStandardInit struct
		*/
		return &linuxStandardInit{
			pipe:       pipe,
			parentPid:  syscall.Getppid(),
			config:     config,
			stateDirFD: stateDirFD,
		}, nil
	}
	return nil, fmt.Errorf("unknown init type %q", t)
}
```

## type linuxStandardInit struct
- 定义
```go
type linuxStandardInit struct {
	pipe       io.ReadWriteCloser
	parentPid  int
	stateDirFD int
	config     *initConfig
}
```

### linuxStandardInit.Init
看看其Init()函数，这是本文的重点函数。 分析其流程如下：
1. 前面部分主要是进行参数设置和状态检查等
2. `exec.LookPath(l.config.Args[0])`在当前系统的PATH中寻找 cmd 的绝对路径。这个cmd就是config.json中声明的用户希望执行的初始化命令。
3. 以"只写" 方式打开fifo管道，形成阻塞。等待另一端有进程以“读”的方式打开管道。
4. 如果单独执行`runc create`命令，到这里就会发生阻塞。 后面将是等待`runc start`以只读的方式打开FIFO管道，阻塞才会消除 ，本进程（Init进程）才会继续后面的流程。
5. 阻塞清除后，`Init进程`会根据config配置初始化seccomp，并调用syscall.Exec执行cmd。 系统调用syscall.Exec()，执行用户真正希望执行的命令，用来覆盖掉PID为1的Init进程。 至此，在容器内部PID为1的进程才是用户希望一直在前台执行的进程

```go
func (l *linuxStandardInit) Init() error {
	if !l.config.Config.NoNewKeyring {
		ringname, keepperms, newperms := l.getSessionRingParams()

		// do not inherit the parent's session keyring
		sessKeyId, err := keys.JoinSessionKeyring(ringname)
		if err != nil {
			return err
		}
		// make session keyring searcheable
		if err := keys.ModKeyringPerm(sessKeyId, keepperms, newperms); err != nil {
			return err
		}
	}

	var console *linuxConsole
	if l.config.Console != "" {
		console = newConsoleFromPath(l.config.Console)
		if err := console.dupStdio(); err != nil {
			return err
		}
	}
	if console != nil {
		if err := system.Setctty(); err != nil {
			return err
		}
	}
	/*配置容器内部的网络*/
	if err := setupNetwork(l.config); err != nil {
		return err
	}
	/*配置容器内部的路由*/
	if err := setupRoute(l.config.Config); err != nil {
		return err
	}
	/*
		检查selinux是否处于enabled状态
			==>/libcontainer/label/label_selinux.go
				==>func Init()
	*/
	label.Init()
	// InitializeMountNamespace() can be executed only for a new mount namespace
	/*
		如果设置了mount namespace，则调用setupRootfs在新的mount namespace中配置设备、挂载点以及文件系统。
	*/
	if l.config.Config.Namespaces.Contains(configs.NEWNS) {
		if err := setupRootfs(l.config.Config, console, l.pipe); err != nil {
			return err
		}
	}
	/*
		配置hostname、apparmor、processLabel、sysctl、readonlyPath、maskPath。
		这些对容器启动本身没有太多影响
	*/
	if hostname := l.config.Config.Hostname; hostname != "" {
		if err := syscall.Sethostname([]byte(hostname)); err != nil {
			return err
		}
	}
	if err := apparmor.ApplyProfile(l.config.AppArmorProfile); err != nil {
		return err
	}
	if err := label.SetProcessLabel(l.config.ProcessLabel); err != nil {
		return err
	}

	for key, value := range l.config.Config.Sysctl {
		if err := writeSystemProperty(key, value); err != nil {
			return err
		}
	}
	for _, path := range l.config.Config.ReadonlyPaths {
		if err := remountReadonly(path); err != nil {
			return err
		}
	}
	for _, path := range l.config.Config.MaskPaths {
		if err := maskPath(path); err != nil {
			return err
		}
	}
	// 获取父进程的退出信号量
	pdeath, err := system.GetParentDeathSignal()
	if err != nil {
		return err
	}
	if l.config.NoNewPrivileges {
		if err := system.Prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			return err
		}
	}
	// Tell our parent that we're ready to Execv. This must be done before the
	// Seccomp rules have been applied, because we need to be able to read and
	// write to a socket.
	/*
		通过管道与父进程进行同步，先发出procReady再等待procRun
	*/
	if err := syncParentReady(l.pipe); err != nil {
		return err
	}
	// Without NoNewPrivileges seccomp is a privileged operation, so we need to
	// do this before dropping capabilities; otherwise do it as late as possible
	// just before execve so as few syscalls take place after it as possible.
	if l.config.Config.Seccomp != nil && !l.config.NoNewPrivileges {
		// 初始化seccomp
		if err := seccomp.InitSeccomp(l.config.Config.Seccomp); err != nil {
			return err
		}
	}
	/*
		调用finalizeNamespace根据config配置将需要的特权capabilities加入白名单，
		设置user namespace，关闭不需要的文件描述符。
	*/
	if err := finalizeNamespace(l.config); err != nil {
		return err
	}
	// finalizeNamespace can change user/group which clears the parent death
	// signal, so we restore it here.
	/*
		恢复parent进程的death信号量并检查当前父进程pid是否为我们原来记录的。
		不是的话，kill ourself
	*/
	if err := pdeath.Restore(); err != nil {
		return err
	}
	// compare the parent from the inital start of the init process and make sure that it did not change.
	// if the parent changes that means it died and we were reparented to something else so we should
	// just kill ourself and not cause problems for someone else.
	if syscall.Getppid() != l.parentPid {
		return syscall.Kill(syscall.Getpid(), syscall.SIGKILL)
	}
	// check for the arg before waiting to make sure it exists and it is returned
	// as a create time error.
	/*
		exec.LookPath(l.config.Args[0])
		在当前系统的PATH中寻找 cmd 的绝对路径
	*/
	name, err := exec.LookPath(l.config.Args[0])
	if err != nil {
		return err
	}
	// close the pipe to signal that we have completed our init.
	/*
		与父进程之间的同步已经完成，关闭pipe，pipe是一个匿名管道（类似于go中的有容量的channel）
		匿名管道应用的一个重大限制是它没有名字，因此，只能用于具有亲缘关系的进程间通信。
		能把两个不相关的进程联系起来，FIFO就像一个公共通道，解决了不同进程之间的“代沟”。
		普通的无名管道只能让相关的进程进行沟通(比如父shell和子shell之间)
	*/
	l.pipe.Close()
	// wait for the fifo to be opened on the other side before
	// exec'ing the users process.
	/*
		"只写" 方式打开fifo管道并写入0，会一直保持阻塞，

		会通知parent进程（即runc create进程）退出。
		child进程（即容器中进程）会被container-shim接管；==>？？为什么？？
		如果直接runc拉起的，会被pid=1进程接管

		直到管道的另一端以读方式打开，并读取内容
		*************************
		至此，create操作流程已经结束

		一旦设置了阻塞标志，调用mkfifo建立好之后，那么管道的两端读写必须分别打开，有任何一方未打开，则在调用open的时候就阻塞
		了解FIFO管道
		==> http://blog.csdn.net/firefoxbug/article/details/8137762
		==> http://blog.csdn.net/firefoxbug/article/details/7358715

		func Openat(dirfd int, path string, flags int, mode uint32) (fd int, err error)
	*/
	fd, err := syscall.Openat(l.stateDirFD, execFifoFilename, os.O_WRONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return newSystemErrorWithCause(err, "openat exec fifo")
	}
	/*
		********************************************************
		*********runc create 与 runc start 的分割线**************
		********************************************************
		从这里开始，实际上是`runc start`的时候才会触发的操作了。
		阻塞清除后，`runc Init进程`会根据config配置初始化seccomp，
		并调用syscall.Exec执行config里面指定的命令

		如果单独执行`runc create`命令，在这里就会发生了阻塞。
		后面将是等待`runc start`以只读的方式打开FIFO管道，阻塞才会消除 ，本进程（Init进程）才会继续后面的流程。
	*/
	if _, err := syscall.Write(fd, []byte("0")); err != nil {
		return newSystemErrorWithCause(err, "write 0 exec fifo")
	}

	if l.config.Config.Seccomp != nil && l.config.NoNewPrivileges {
		if err := seccomp.InitSeccomp(l.config.Config.Seccomp); err != nil {
			return newSystemErrorWithCause(err, "init seccomp")
		}
	}
	/*
		************
		系统调用syscall.Exec()，执行用户真正希望执行的命令
		用来覆盖掉PID为1的Init进程
		至此，在容器内部PID为1的进程才是用户希望一直在前台执行的进程
		ps -ef 看到PID为1
	*/
	if err := syscall.Exec(name, l.config.Args[0:], os.Environ()); err != nil {
		return newSystemErrorWithCause(err, "exec user process")
	}
	return nil
}
```