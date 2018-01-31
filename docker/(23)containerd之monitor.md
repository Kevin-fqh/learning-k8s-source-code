# containerd之monitor

containerd的核心是type Supervisor struct ，在创建Supervisor的时候会创建一个`type Monitor struct`对象来负责监视host主机上所有容器中的进程。
这里用到了Linux的`I/O 多路复用 epoll`，可以先阅读[epoll的原理是什么？](https://www.zhihu.com/question/20122137/answer/14049112)和[Linux IO模式及 select、poll、epoll详解](https://segmentfault.com/a/1190000003063859#articleHeader5)。

我们首先来看看type Monitor struct的运作流程，然后再来介绍Linux的`I/O 多路复用 epoll`。

## type Monitor struct
用`EpollCreate1`创建一个Epoll对象，见/containerd-0.2.3/supervisor/monitor_linux.go
```go
// Monitor represents a runtime.Process monitor
/*
	负责监视容器中的进程，runtime.Process monitor
*/
type Monitor struct {
	m         sync.Mutex
	receivers map[int]interface{}
	exits     chan runtime.Process
	ooms      chan string
	epollFd   int
}

// NewMonitor starts a new process monitor and returns it
func NewMonitor() (*Monitor, error) {
	m := &Monitor{
		receivers: make(map[int]interface{}),
		exits:     make(chan runtime.Process, 1024),
		ooms:      make(chan string, 1024),
	}
	/*
		通过cgo调用，使用Linux的Epoll机制，I/O多路复用
		生成一个Epoll对象，返回句柄fd
	*/
	fd, err := archutils.EpollCreate1(0)
	if err != nil {
		return nil, err
	}
	m.epollFd = fd
	go m.start()
	return m, nil
}
```

### start()
对`EpollWait`的使用，把一个进程(容器)的fd往前面生成的Epoll对象注册
```go
func (m *Monitor) start() {
	var events [128]syscall.EpollEvent
	for {
		/*
			等待m.epollFd上的io事件，参数events[:]用来从内核得到事件的集合
			返回值n表示得到的event数量
		*/
		n, err := archutils.EpollWait(m.epollFd, events[:], -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			logrus.WithField("error", err).Fatal("containerd: epoll wait")
		}
		// process events
		for i := 0; i < n; i++ {
			fd := int(events[i].Fd)
			m.m.Lock()
			r := m.receivers[fd]
			switch t := r.(type) {
			case runtime.Process:
				//处理挂起事件
				if events[i].Events == syscall.EPOLLHUP {
					delete(m.receivers, fd)
					if err = syscall.EpollCtl(m.epollFd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{
						Events: syscall.EPOLLHUP,
						Fd:     int32(fd),
					}); err != nil {
						logrus.WithField("error", err).Error("containerd: epoll remove fd")
					}
					if err := t.Close(); err != nil {
						logrus.WithField("error", err).Error("containerd: close process IO")
					}
					EpollFdCounter.Dec(1)
					m.exits <- t
				}
			case runtime.OOM:
				//处理OOM事件
				// always flush the event fd
				t.Flush()
				if t.Removed() {
					delete(m.receivers, fd)
					// epoll will remove the fd from its set after it has been closed
					t.Close()
					EpollFdCounter.Dec(1)
				} else {
					m.ooms <- t.ContainerID()
				}
			}
			m.m.Unlock()
		}
	}
}
```

### 两个监控
可以发现func (m *Monitor) Monitor函数监控的是`/var/run/docker/libcontainerd/containerd/{containerID}/init/`下的`exit`管道文件。 而MonitorOOM()监控的是`/proc/{pid}/cgroup`文件。

都是通过对`EpollCtl`的使用，来通知containerd某一个进程(容器)有状况发生。

- exit fd
```go
// Monitor adds a process to the list of the one being monitored
func (m *Monitor) Monitor(p runtime.Process) error {
	m.m.Lock()
	defer m.m.Unlock()
	/*
		获取容器内进程p 的exit fd
			==>/containerd-0.2.3/runtime/process.go
				==>func (p *process) ExitFD() int
		
	*/
	fd := p.ExitFD()
	event := syscall.EpollEvent{
		Fd:     int32(fd),
		Events: syscall.EPOLLHUP,
	}
	if err := archutils.EpollCtl(m.epollFd, syscall.EPOLL_CTL_ADD, fd, &event); err != nil {
		return err
	}
	EpollFdCounter.Inc(1)
	m.receivers[fd] = p
	return nil
}
```

- MonitorOOM
```go
// MonitorOOM adds a container to the list of the ones monitored for OOM
/*
	Monitor一个容器的oom
	https://segmentfault.com/a/1190000003063859#articleHeader5
*/
func (m *Monitor) MonitorOOM(c runtime.Container) error {
	m.m.Lock()
	defer m.m.Unlock()
	/*
		/containerd-0.2.3/runtime/container_linux.go
			==>func (c *container) OOM() (OOM, error)
	*/
	o, err := c.OOM()
	if err != nil {
		return err
	}
	fd := o.FD() //声明要监听的fd
	event := syscall.EpollEvent{
		Fd:     int32(fd),
		Events: syscall.EPOLLHUP | syscall.EPOLLIN, //要监听的事件是 表示对应的文件描述符被挂断，表示对应的文件描述符可以读（包括对端SOCKET正常关闭）
	}
	/*
		把要监听的fd放入到前面创建好的epoll对象m.epollFd中
		event：是告诉内核需要监听什么事件
	*/
	if err := archutils.EpollCtl(m.epollFd, syscall.EPOLL_CTL_ADD, fd, &event); err != nil {
		return err
	}
	EpollFdCounter.Inc(1)
	m.receivers[fd] = o
	return nil
}

func (c *container) OOM() (OOM, error) {
	p := c.processes[InitProcessID]
	if p == nil {
		return nil, fmt.Errorf("no init process found")
	}

	mountpoint, hostRoot, err := findCgroupMountpointAndRoot(os.Getpid(), "memory")
	if err != nil {
		return nil, err
	}

	/*
		读取该进程的/proc/{pid}/cgroup文件
	*/
	cgroups, err := parseCgroupFile(fmt.Sprintf("/proc/%d/cgroup", p.pid))
	if err != nil {
		return nil, err
	}

	root, ok := cgroups["memory"]
	if !ok {
		return nil, fmt.Errorf("no memory cgroup for container %s", c.ID())
	}

	// Take care of the case were we're running inside a container
	// ourself
	root = strings.TrimPrefix(root, hostRoot)

	return c.getMemeoryEventFD(filepath.Join(mountpoint, root))
}
```
至此，可以看出来，containerd通过epoll机制及时获知各个容器(进程)的输出信息，或者是触发了oom，然后根据读到的信息，进行相应的操作。

## epoll三个接口
这部分摘抄于[Linux IO模式及 select、poll、epoll详解](https://segmentfault.com/a/1190000003063859#articleHeader5)。

`流的概念`，一个流可以是文件，socket，pipe等等可以进行I/O操作的内核对象。不管是文件，还是套接字，还是管道，我们都可以把他们看作流。

```c
int epoll_create(int size)；//创建一个epoll的句柄，size用来告诉内核这个监听的数目一共有多大
int epoll_ctl(int epfd, int op, int fd, struct epoll_event *event)；
int epoll_wait(int epfd, struct epoll_event * events, int maxevents, int timeout);
```
### epoll_create
epoll_create()创建一个epoll的句柄，size用来告诉内核这个监听的数目一共有多大，参数size并不是限制了epoll所能监听的描述符最大个数，只是对内核初始分配内部数据结构的一个建议。

当创建好epoll句柄后，它就会占用一个fd值，在linux下如果查看`/proc/进程id/fd/`，是能够看到这个fd的，所以在使用完epoll后，必须调用close()关闭，否则可能导致fd被耗尽。

### epoll_ctl
函数是对指定描述符fd执行op操作。
- epfd：是epoll_create()的返回值。
- op：表示op操作，用三个宏来表示：添加EPOLL_CTL_ADD，删除EPOLL_CTL_DEL，修改EPOLL_CTL_MOD。分别添加、删除和修改对fd的监听事件。
- fd：是需要监听的fd（文件描述符）
- epoll_event：是告诉内核需要监听什么事，struct epoll_event结构如下：
```
struct epoll_event {
  __uint32_t events;  /* Epoll events */
  epoll_data_t data;  /* User data variable */
};

//events可以是以下几个宏的集合：
EPOLLIN ：表示对应的文件描述符可以读（包括对端SOCKET正常关闭）；
EPOLLOUT：表示对应的文件描述符可以写；
EPOLLPRI：表示对应的文件描述符有紧急的数据可读（这里应该表示有带外数据到来）；
EPOLLERR：表示对应的文件描述符发生错误；
EPOLLHUP：表示对应的文件描述符被挂断；
EPOLLET： 将EPOLL设为边缘触发(Edge Triggered)模式，这是相对于水平触发(Level Triggered)来说的。
EPOLLONESHOT：只监听一次事件，当监听完这次事件之后，如果还需要继续监听这个socket的话，需要再次把这个socket加入到EPOLL队列里
```

### epoll_wait
epoll_wait()等待epfd上的io事件，最多返回maxevents个事件。

参数events用来从内核得到事件的集合，maxevents告之内核这个events有多大，这个maxevents的值不能大于创建epoll_create()时的size，参数timeout是超时时间（毫秒，0会立即返回，-1将不确定，也有说法说是永久阻塞）。该函数返回需要处理的事件数目，如返回0表示已超时。


非阻塞模式，相当于告诉了系统内核： “当我请求的I/O 操作不能够马上完成，请马上返回一个错误给我。”
阻塞方式block，就是进程或是线程执行到这些函数时必须等待某个事件的发生，如果事件没有发生，进程或线程就被阻塞，函数不能立即返回。

select、poll、epoll有个时间参数，可以设置为以阻塞的方式运行；还是以非阻塞的方式运行；或者阻塞一段时间，然后return

## 参考
[epoll的原理是什么？](https://www.zhihu.com/question/20122137/answer/14049112)

[Linux IO模式及 select、poll、epoll详解](https://segmentfault.com/a/1190000003063859#articleHeader5)

[我读过的最好的epoll讲解](https://www.cnblogs.com/ajianbeyourself/p/5859989.html)