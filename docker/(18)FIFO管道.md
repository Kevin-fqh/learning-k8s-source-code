# FIFO管道

runc中用到了匿名管道pipe和有名管道FIFO来进行通信和流程控制。 

pipe是一个匿名管道，匿名管道应用的一个重大限制是它没有名字，因此，只能用于具有亲缘关系的进程间通信。Go语言中通过os.Pipe()来创建。

FIFO管道就像一个公共通道，能把两个不相关的进程联系起来。 本文通过一个例子看来看看FIFO的使用。

## client端
实现的功能是往一个fifo管道中间隔5s写入数据
```shell
#!/bin/bash
#Client.sh
#不断向fifo写入数据

tmp_fifo="/home/fifo/test.fifo"
rm -f $tmp_fifo
mkfifo $tmp_fifo
exec 6>$tmp_fifo   #以'写'方式打开管道，此时必须有进程以读的方式打开该FIFO，本进程才能继续运行，否则会一直阻塞在这里
#exec 6<>$tmp_fifo #这句话能把管道变成非阻塞！ 读写管道

i=0
while :
do
        sleep 5                 # 5秒写一次
        echo "$i" >&6
        echo "$i"    #可以挂起进程观察效果
        let i++
done

exec 6>&-
```

## server端
从fifo管道中读取client端写入的数据
```shell
#!/bin/bash
#Server.sh
#不断从fifo中读出数据

tmp_fifo="/home/fifo/test.fifo"
echo "$tmp_fifo"
exec 6<$tmp_fifo                   #以只读的方式打开FIFO管道				
#exec 6<>$tmp_fifo                 #以读写的方式使用该管道，这样子即使对面不打开，本进程也不会阻塞

while :
do
        read TEXT
        sleep 7
        echo "$TEXT"          #每1s就读取一个数据，并且打印到终端，要停止，最好挂起进程！
done <&6
```

## 调用
1. 第一步，client端以“只写”方式打开fifo管道，运行`sh client.sh`，此时会发生阻塞
2. 第二步，server端以"只读"方式或者“读写”方式打开fifo管道，会发现前面的client端开始写入数据了。然后，server端就可以读取数据。
3. server端会一直把管道中数据全部读完，即使中间client端进程被挂起
4. 如果在第一步中，client端是以“读写”方式打开管道，那么不会发生阻塞，直接就能够写入数据。

## runc中的应用
runc中的create和start命令就是通过FIFO的使用来实现阻塞，见/runc-1.0.0-rc2/libcontainer/standard_init_linux.go
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
		与父进程之间的同步已经完成，关闭pipe，pipe是一个匿名管道
		匿名管道应用的一个重大限制是它没有名字，因此，只能用于具有亲缘关系的进程间通信。
		普通的无名管道只能让相关的进程进行沟通(比如父shell和子shell之间)
		
		而，FIFO就像一个公共通道，能把两个不相关的进程联系起来，解决了不同进程之间的“代沟”。
	*/
	l.pipe.Close()
	// wait for the fifo to be opened on the other side before
	// exec'ing the users process.
	/*
		以"只写" 方式打开fifo管道并写入0，会一直保持阻塞，
		直到管道的另一端以读方式打开，并读取内容
		*************************
		至此，create操作流程已经结束

		一旦设置了阻塞标志，调用mkfifo建立好之后，那么管道的两端读写必须分别打开，有任何一方未打开，则在调用open的时候就阻塞
		了解FIFO管道
		==> http://blog.csdn.net/firefoxbug/article/details/8137762
		==> http://blog.csdn.net/firefoxbug/article/details/7358715
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
		阻塞清除后，根据config配置初始化seccomp，
		并调用syscall.Exec执行config里面指定的命令
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
		 **********
		系统调用syscall.Exec()，执行用户真正希望执行的命令
		用来覆盖掉PID为1的Init进程
	*/
	if err := syscall.Exec(name, l.config.Args[0:], os.Environ()); err != nil {
		return newSystemErrorWithCause(err, "exec user process")
	}
	return nil
}
```

## 参考
[fifo](http://blog.csdn.net/firefoxbug/article/details/7358715)

[fifo－2](http://www.firefoxbug.com/index.php/archives/1739/)