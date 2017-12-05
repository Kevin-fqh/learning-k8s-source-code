# docker container start命令

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [postContainersStart](#postcontainersstart)
  - [ContainerStart](#containerstart)
    - [关键函数containerStart](#关键函数containerstart)
  - [设置文件系统](#设置文件系统)
    - [overlay](#overlay)
  - [设置网络模式](#设置网络模式)
    - [allocateNetwork](#allocatenetwork)
  - [spec文件](#spec文件)
  - [调用containerd进行Create容器](#调用containerd进行create容器)
    - [container.start()](#container-start)
    - [发送grpc请求](#发送grpc请求)
  - [daemon启动libContainerd](#daemon启动libcontainerd)
    - [libcontainerd](#libcontainerd)
  - [参考](#参考)
	
<!-- END MUNGE: GENERATED_TOC -->

## postContainersStart
daemon的路由如下
```go
//POST
router.NewPostRoute("/containers/{name:.*}/start", r.postContainersStart),
```
查看handler函数postContainersStart：
1. 解析request
2. 调用daemon启动ContainerStart()，s.backend.ContainerStart(vars["name"], hostConfig, checkpoint, checkpointDir)
```go
func (s *containerRouter) postContainersStart(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	// If contentLength is -1, we can assumed chunked encoding
	// or more technically that the length is unknown
	// https://golang.org/src/pkg/net/http/request.go#L139
	// net/http otherwise seems to swallow any headers related to chunked encoding
	// including r.TransferEncoding
	// allow a nil body for backwards compatibility

	version := httputils.VersionFromContext(ctx)
	var hostConfig *container.HostConfig
	// A non-nil json object is at least 7 characters.
	if r.ContentLength > 7 || r.ContentLength == -1 {
		if versions.GreaterThanOrEqualTo(version, "1.24") {
			return validationError{fmt.Errorf("starting container with non-empty request body was deprecated since v1.10 and removed in v1.12")}
		}

		if err := httputils.CheckForJSON(r); err != nil {
			return err
		}

		c, err := s.decoder.DecodeHostConfig(r.Body)
		if err != nil {
			return err
		}
		hostConfig = c
	}

	if err := httputils.ParseForm(r); err != nil {
		return err
	}

	checkpoint := r.Form.Get("checkpoint")
	checkpointDir := r.Form.Get("checkpoint-dir")
	/*
		参数解析完毕之后
		准备ContainerStart()
			==>/daemon/start.go
	*/
	if err := s.backend.ContainerStart(vars["name"], hostConfig, checkpoint, checkpointDir); err != nil {
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
```

## ContainerStart
1. 根据名称(ID)获取容器对象
2. 验证配置
3. 开始启动container，daemon.containerStart(container, checkpoint, checkpointDir, true)
```go
// ContainerStart starts a container.
func (daemon *Daemon) ContainerStart(name string, hostConfig *containertypes.HostConfig, checkpoint string, checkpointDir string) error {
	if checkpoint != "" && !daemon.HasExperimental() {
		/*
			checkpoint 仅仅支持在experimental mode下使用
		*/
		return apierrors.NewBadRequestError(fmt.Errorf("checkpoint is only supported in experimental mode"))
	}

	/*
		根据名称(ID)获取容器对象
	*/
	container, err := daemon.GetContainer(name)
	if err != nil {
		return err
	}

	if container.IsPaused() {
		//paused状态
		return fmt.Errorf("Cannot start a paused container, try unpause instead.")
	}

	if container.IsRunning() {
		//running状态
		err := fmt.Errorf("Container already started")
		return apierrors.NewErrorWithStatusCode(err, http.StatusNotModified)
	}

	// Windows does not have the backwards compatibility issue here.
	/*
		Windows在这里没有向后兼容性问题。
		非Windows系统中，为了保持兼容性，hostconfig应该在create的时候被传给该container，而不是在start期间
	*/
	if runtime.GOOS != "windows" {
		// This is kept for backward compatibility - hostconfig should be passed when
		// creating a container, not during start.
		if hostConfig != nil {
			logrus.Warn("DEPRECATED: Setting host configuration options when the container starts is deprecated and has been removed in Docker 1.12")
			oldNetworkMode := container.HostConfig.NetworkMode
			if err := daemon.setSecurityOptions(container, hostConfig); err != nil {
				return err
			}
			if err := daemon.mergeAndVerifyLogConfig(&hostConfig.LogConfig); err != nil {
				return err
			}
			if err := daemon.setHostConfig(container, hostConfig); err != nil {
				return err
			}
			newNetworkMode := container.HostConfig.NetworkMode
			if string(oldNetworkMode) != string(newNetworkMode) {
				/*
					用户在启动的时候更改了网络模式
				*/
				// if user has change the network mode on starting, clean up the
				// old networks. It is a deprecated feature and has been removed in Docker 1.12
				container.NetworkSettings.Networks = nil
				if err := container.ToDisk(); err != nil {
					return err
				}
			}
			container.InitDNSHostConfig()
		}
	} else {
		if hostConfig != nil {
			return fmt.Errorf("Supplying a hostconfig on start is not supported. It should be supplied on create")
		}
	}

	// check if hostConfig is in line with the current system settings.
	// It may happen cgroups are umounted or the like.
	/*验证配置文件数据*/
	if _, err = daemon.verifyContainerSettings(container.HostConfig, nil, false); err != nil {
		return err
	}
	// Adapt for old containers in case we have updates in this function and
	// old containers never have chance to call the new function in create stage.
	if hostConfig != nil {
		if err := daemon.adaptContainerSettings(container.HostConfig, false); err != nil {
			return err
		}
	}

	/*
		开始启动container
	*/
	return daemon.containerStart(container, checkpoint, checkpointDir, true)
}
```

### 关键函数containerStart
继续查看daemon.containerStart()，这是`docker start`中的核心部分。 分析其流程如下：
1. 设置容器的文件系统，/var/lib/docker/overlay/{container.RWLayer.mountID}/merged
2. 设置容器的网络模式，调用了libnetwork，即CNM模型
3. 创建/proc /dev等spec文件，对容器所特有的属性都进行设置，例如：资源限制，命名空间，安全模式等等配置信息
4. 调用containerd进行Create容器

后面会就者四点分别展开介绍
```go
// containerStart prepares the container to run by setting up everything the
// container needs, such as storage and networking, as well as links
// between containers. The container is left waiting for a signal to
// begin running.
/*
	func (daemon *Daemon) containerStart通过设置容器所需的所有内容（如存储、网络、容器之间的链接）来准备要运行的容器。
	容器正在等待信号开始运行。
*/
func (daemon *Daemon) containerStart(container *container.Container, checkpoint string, checkpointDir string, resetRestartManager bool) (err error) {
	start := time.Now()
	/*
		给准备启动的container上锁
	*/
	container.Lock()
	defer container.Unlock()

	if resetRestartManager && container.Running { // skip this check if already in restarting step and resetRestartManager==false
		return nil
	}

	if container.RemovalInProgress || container.Dead {
		return fmt.Errorf("Container is marked for removal and cannot be started.")
	}

	// if we encounter an error during start we need to ensure that any other
	// setup has been cleaned up properly
	/*
		启动失败之后的clean up操作
	*/
	defer func() {
		if err != nil {
			container.SetError(err)
			// if no one else has set it, make sure we don't leave it at zero
			if container.ExitCode() == 0 {
				container.SetExitCode(128)
			}
			container.ToDisk()

			container.Reset(false)

			daemon.Cleanup(container)
			// if containers AutoRemove flag is set, remove it after clean up
			if container.HostConfig.AutoRemove {
				container.Unlock()
				if err := daemon.ContainerRm(container.ID, &types.ContainerRmConfig{ForceRemove: true, RemoveVolume: true}); err != nil {
					logrus.Errorf("can't remove container %s: %v", container.ID, err)
				}
				container.Lock()
			}
		}
	}()

	/*
		*****1st: 设置容器的文件系统*****
		设置container的BaseFS为该container的RWLayer的挂载点path
		/var/lib/docker/overlay/{container.RWLayer.mountID}/merged

			==>/daemon/daemon_unix.go
				==>func (daemon *Daemon) conditionalMountOnStart
		BaseFS 是该container整个文件系统的路径
		`docker exec -it {container.ID} bin/bash`进去看到的视图，就是/var/lib/docker/overlay/{container.RWLayer.mountID}/merged下的视图
		也就是说，后面在容器里面做的所有修改都会实时反应到目录/var/lib/docker/overlay/{container.RWLayer.mountID}/merged
		在外面所做的修改也就实时反馈到容器里面
	*/
	if err := daemon.conditionalMountOnStart(container); err != nil {
		return err
	}

	// Make sure NetworkMode has an acceptable value. We do this to ensure
	// backwards API compatibility.
	/*确保NetworkMode具有可接受的值。为了确保向后的API兼容性。*/
	container.HostConfig = runconfig.SetDefaultNetModeIfBlank(container.HostConfig)

	/*
		*****2nd: 设置容器的网络模式*****
		初始化容器的网络，
		默认模式bridge：同一个host主机上容器的通信通过Linux bridge进行
					  与宿主机外部网络的通信需要通过宿主机端口进行NAT
	*/
	if err := daemon.initializeNetworking(container); err != nil {
		return err
	}

	/*
		*****3rd: 创建/proc /dev等spec文件*****
		对容器所特有的属性都进行设置，例如：资源限制，命名空间，安全模式等等配置信息
		==>/daemon/oci_linux.go
			==>func (daemon *Daemon) createSpec(c *container.Container)
	*/
	spec, err := daemon.createSpec(container)
	if err != nil {
		return err
	}

	createOptions, err := daemon.getLibcontainerdCreateOptions(container)
	if err != nil {
		return err
	}

	if resetRestartManager {
		container.ResetRestartManager(true)
	}

	if checkpointDir == "" {
		checkpointDir = container.CheckpointDir()
	}

	/*
		前面所有的参数等设置都是为了这里调用containerd服务
		*****4th: 调用containerd进行Create*****
			==>/libcontainerd/client_unix.go
				==>func (clnt *client) Create
	*/
	if err := daemon.containerd.Create(container.ID, checkpoint, checkpointDir, *spec, container.InitializeStdio, createOptions...); err != nil {
		errDesc := grpc.ErrorDesc(err)
		contains := func(s1, s2 string) bool {
			return strings.Contains(strings.ToLower(s1), s2)
		}
		logrus.Errorf("Create container failed with error: %s", errDesc)
		// if we receive an internal error from the initial start of a container then lets
		// return it instead of entering the restart loop
		// set to 127 for container cmd not found/does not exist)
		if contains(errDesc, container.Path) &&
			(contains(errDesc, "executable file not found") ||
				contains(errDesc, "no such file or directory") ||
				contains(errDesc, "system cannot find the file specified")) {
			container.SetExitCode(127)
		}
		// set to 126 for container cmd can't be invoked errors
		if contains(errDesc, syscall.EACCES.Error()) {
			container.SetExitCode(126)
		}

		// attempted to mount a file onto a directory, or a directory onto a file, maybe from user specified bind mounts
		if contains(errDesc, syscall.ENOTDIR.Error()) {
			errDesc += ": Are you trying to mount a directory onto a file (or vice-versa)? Check if the specified host path exists and is the expected type"
			container.SetExitCode(127)
		}

		return fmt.Errorf("%s", errDesc)
	}

	containerActions.WithValues("start").UpdateSince(start)

	return nil
}
```

## 设置文件系统
* 设置container的BaseFS为该container的RWLayer的挂载点path：`/var/lib/docker/overlay/{container.RWLayer.mountID}/merged`。
* BaseFS 是该container整个文件系统的路径。
* `docker exec -it {container.ID} bin/bash`进去看到的视图，就是/var/lib/docker/overlay/{container.RWLayer.mountID}/merged下的视图
* 也就是说，后面在容器里面做的所有修改都会实时反应到目录/var/lib/docker/overlay/{container.RWLayer.mountID}/merged。 在外面所做的修改也就实时反馈到容器里面。

```go
// conditionalMountOnStart is a platform specific helper function during the
// container start to call mount.
func (daemon *Daemon) conditionalMountOnStart(container *container.Container) error {
	return daemon.Mount(container)
}

// Mount sets container.BaseFS
// (is it not set coming in? why is it unset?)
/*
	func (daemon *Daemon) Mount 负责设置一个container的BaseFS，
	设置为该container的RWLayer的挂载点path
*/
func (daemon *Daemon) Mount(container *container.Container) error {
	/*
		RWLayer.Mount()把该RWLayer进行挂载，然后return 路径给调用者
			==>/layer/mounted_layer.go
				==>func (rl *referencedRWLayer) Mount(mountLabel string)
		dir的值：/var/lib/docker/overlay/{container.RWLayer.mountID}/merged
	*/
	dir, err := container.RWLayer.Mount(container.GetMountLabel())
	if err != nil {
		return err
	}
	logrus.Debugf("container mounted via layerStore: %v", dir)

	/*
	   容器第一次启动的时候，container.BaseFS = ""
	*/
	if container.BaseFS != dir {
		// The mount path reported by the graph driver should always be trusted on Windows, since the
		// volume path for a given mounted layer may change over time.  This should only be an error
		// on non-Windows operating systems.
		/*
			同时满足3个条件，return error
				container.BaseFS != dir && 非windows系统 && container.BaseFS != ""
		*/
		if container.BaseFS != "" && runtime.GOOS != "windows" {
			daemon.Unmount(container)
			return fmt.Errorf("Error: driver %s is returning inconsistent paths for container %s ('%s' then '%s')",
				daemon.GraphDriverName(), container.ID, container.BaseFS, dir)
		}
	}
	container.BaseFS = dir // TODO: combine these fields
	return nil
}

func (rl *referencedRWLayer) Mount(mountLabel string) (string, error) {
	/*
		依靠mountedLayer.mountID和mountLabel
		获取一个container整个文件系统的路径
		以overlay为例子
			==>/daemon/graphdriver/overlay/overlay.go
				==>func (d *Driver) Get(id string, mountLabel string)
	*/
	return rl.layerStore.driver.Get(rl.mountedLayer.mountID, mountLabel)
}
```

### overlay
以overlay为例子，调用了`syscall.Mount("overlay", mergedDir, "overlay", 0, label.FormatMountLabel(opts, mountLabel))`使用了overlay。
```go
// Get creates and mounts the required file system for the given id and returns the mount path.
func (d *Driver) Get(id string, mountLabel string) (s string, err error) {
	/*
		入参id 表示一个layer所在的目录路径 /var/lib/docker/overlsy/{id}
	*/
	dir := d.dir(id)
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	// If id has a root, just return it
	rootDir := path.Join(dir, "root")
	if _, err := os.Stat(rootDir); err == nil {
		return rootDir, nil
	}
	mergedDir := path.Join(dir, "merged")
	/*
		增加一次一个layer的引用次数
		==>/daemon/graphdriver/counter.go
			==>func (c *RefCounter) Increment(path string)

		一般情况下，count<=1
	*/
	if count := d.ctr.Increment(mergedDir); count > 1 {
		return mergedDir, nil
	}
	defer func() {
		if err != nil {
			if c := d.ctr.Decrement(mergedDir); c <= 0 {
				syscall.Unmount(mergedDir, 0)
			}
		}
	}()
	lowerID, err := ioutil.ReadFile(path.Join(dir, "lower-id"))
	if err != nil {
		return "", err
	}
	var (
		lowerDir = path.Join(d.dir(string(lowerID)), "root")
		upperDir = path.Join(dir, "upper")
		workDir  = path.Join(dir, "work")
		opts     = fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)
	)
	/*
		调用 syscall.Mount
	*/
	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, label.FormatMountLabel(opts, mountLabel)); err != nil {
		return "", fmt.Errorf("error creating overlay mount to %s: %v", mergedDir, err)
	}
	// chown "workdir/work" to the remapped root UID/GID. Overlay fs inside a
	// user namespace requires this to move a directory from lower to upper.
	/*
		rootUID, rootGID：0 0
	*/
	rootUID, rootGID, err := idtools.GetRootUIDGID(d.uidMaps, d.gidMaps)
	if err != nil {
		return "", err
	}
	if err := os.Chown(path.Join(workDir, "work"), rootUID, rootGID); err != nil {
		return "", err
	}
	return mergedDir, nil
}
```

## 设置网络模式
涉及到libnetwork，即CNM模型
```go
/*
	docker 4种网络模式
		bridge hots none container
*/
func (daemon *Daemon) initializeNetworking(container *container.Container) error {
	var err error

	/*
		如果网络模式是 container模式，
			获取指定容器的网络配置，join in
	*/
	if container.HostConfig.NetworkMode.IsContainer() {
		// we need to get the hosts files from the container to join
		nc, err := daemon.getNetworkedContainer(container.ID, container.HostConfig.NetworkMode.ConnectedContainer())
		if err != nil {
			return err
		}
		initializeNetworkingPaths(container, nc)
		container.Config.Hostname = nc.Config.Hostname
		container.Config.Domainname = nc.Config.Domainname
		return nil
	}

	/*
		如果网络模式是 host模式，
			与宿主机共享ip
			仅需设置container.Config.Hostname
	*/
	if container.HostConfig.NetworkMode.IsHost() {
		if container.Config.Hostname == "" {
			container.Config.Hostname, err = os.Hostname()
			if err != nil {
				return err
			}
		}
	}

	/*
		分配网络资源
	*/
	if err := daemon.allocateNetwork(container); err != nil {
		return err
	}

	/*
		把前面对container的hostname设置持久化，写入到/var/lib/docker/containers/{container.ID}/hostname
			==>/container/container_unix.go
				==>func (container *Container) BuildHostnameFile()
	*/
	return container.BuildHostnameFile()
}
```

### allocateNetwork()
```go
/*
	涉及到libnetwork，即CNM模型
		http://www.cnblogs.com/YaoDD/p/6386166.html
	一个Sandbox，可以视为一个Network namespace；一个Sandbox可以包含多个处于不同Network的Endpoint。
	一个Endpoint，可以视为一个veth对；一个Endpoint只能属于一个Network和一个Sandbox。
	一个Network是一个能够互相通信的Endpoint的集合；Network的实现可以是一个Linux网桥，一个VLAN等等。
*/
func (daemon *Daemon) allocateNetwork(container *container.Container) error {
	start := time.Now()
	/*
		daemon的网络controller，其类型是libnetwork.NetworkController
	*/
	controller := daemon.netController

	if daemon.netController == nil {
		return nil
	}

	// Cleanup any stale sandbox left over due to ungraceful daemon shutdown
	/*
		清理一些残留无用的sandbox盒，
		sandbox就是一个容器的网络栈，相当于一个Network namespace
	*/
	if err := controller.SandboxDestroy(container.ID); err != nil {
		logrus.Errorf("failed to cleanup up stale network sandbox for container %s", container.ID)
	}

	updateSettings := false
	if len(container.NetworkSettings.Networks) == 0 {
		if container.Config.NetworkDisabled || container.HostConfig.NetworkMode.IsContainer() {
			return nil
		}

		/*
			更新容器的网络设置，
			其实就是根据容器网络模式来更新container.NetworkSettings.Networks的映射关系，就是网络名和endpoint的关系
		*/
		daemon.updateContainerNetworkSettings(container, nil)
		updateSettings = true
	}

	// always connect default network first since only default
	// network mode support link and we need do some setting
	// on sandbox initialize for link, but the sandbox only be initialized
	// on first network connecting.
	defaultNetName := runconfig.DefaultDaemonNetworkMode().NetworkName()
	if nConf, ok := container.NetworkSettings.Networks[defaultNetName]; ok {
		cleanOperationalData(nConf)
		/*
			第一次尝试连接默认的网络名，同时可以完成沙盒的初始化
		*/
		if err := daemon.connectToNetwork(container, defaultNetName, nConf.EndpointSettings, updateSettings); err != nil {
			return err
		}

	}

	// the intermediate map is necessary because "connectToNetwork" modifies "container.NetworkSettings.Networks"
	networks := make(map[string]*network.EndpointSettings)
	for n, epConf := range container.NetworkSettings.Networks {
		if n == defaultNetName {
			continue
		}

		networks[n] = epConf
	}

	for netName, epConf := range networks {
		cleanOperationalData(epConf)
		/*
			将所有endpoint连接到网络
		*/
		if err := daemon.connectToNetwork(container, netName, epConf.EndpointSettings, updateSettings); err != nil {
			return err
		}
	}

	/*
		持久化容器的hostconfig信息，/var/lib/docker/containers/{id}/hostconfig.json
			==>/container/container.go
				==>func (container *Container) WriteHostConfig()
	*/
	if err := container.WriteHostConfig(); err != nil {
		return err
	}
	networkActions.WithValues("allocate").UpdateSince(start)
	return nil
}
```

## spec文件
Linux 内核提供了一种通过`/proc`文件系统，在运行时访问内核内部数据结构、改变内核设置的机制。 
proc文件系统是一个伪文件系统，它只存在内存当中，而不占用外存空间。 
它以文件系统的方式为访问系统内核数据的操作提供接口。

## 调用containerd进行Create容器
调用libcontainerd模块
```go
/*
	clnt *client的初始化：
		==>/daemon/daemon.go
			==>func NewDaemon
				==>d.containerd, err = containerdRemote.Client(d)
		d.containerd就是clnt *client
					==>/libcontainerd/remote_unix.go
						==>func (r *remote) Client(b Backend)
*/
func (clnt *client) Create(containerID string, checkpoint string, checkpointDir string, spec specs.Spec, attachStdio StdioCallback, options ...CreateOption) (err error) {
	clnt.lock(containerID)
	defer clnt.unlock(containerID)

	/*
		获取libcontainerd模块中的containers,这里面的容器应该包括正在运行的容器
			==>/libcontainerd/client.go
				==>func (clnt *client) getContainer(containerID string)
	*/
	if _, err := clnt.getContainer(containerID); err == nil {
		return fmt.Errorf("Container %s is already active", containerID)
	}

	/*
		获取gid和uid
	*/
	uid, gid, err := getRootIDs(specs.Spec(spec))
	if err != nil {
		return err
	}
	/*
		迭代创建 statedir 所需目录，该目录路径在daemon.start定义libcontainerd模块的时候已经指定
			==>/libcontainerd/client_unix.go
	*/
	dir, err := clnt.prepareBundleDir(uid, gid)
	if err != nil {
		return err
	}

	/*
		创建一个containerCommon对象
			==>/libcontainerd/client_unix.go
	*/
	container := clnt.newContainer(filepath.Join(dir, containerID), options...)
	if err := container.clean(); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			container.clean()
			clnt.deleteContainer(containerID)
		}
	}()

	/*
		创建容器目录
	*/
	if err := idtools.MkdirAllAs(container.dir, 0700, uid, gid); err != nil && !os.IsExist(err) {
		return err
	}

	/*
		创建配置文件路径，并根据spec创建配置文件
	*/
	f, err := os.Create(filepath.Join(container.dir, configFilename))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(spec); err != nil {
		return err
	}

	/*
		容器启动
			==>/libcontainerd/container_unix.go
				==>func (ctr *container) start
	*/
	return container.start(checkpoint, checkpointDir, attachStdio)
}
```

### container.start()
```go
/*
	func (ctr *container) start
	配置containerd创建容器的标准输入输出端及请求数据的设置,并发送请求
*/
func (ctr *container) start(checkpoint string, checkpointDir string, attachStdio StdioCallback) (err error) {
	spec, err := ctr.spec() //根据读取的配置文件信息设置spec对象
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})

	fifoCtx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	/*
		创建一个先进先出的io管道对象，包括可读端和可写端
	*/
	iopipe, err := ctr.openFifos(fifoCtx, spec.Process.Terminal)
	if err != nil {
		return err
	}

	var stdinOnce sync.Once

	// we need to delay stdin closure after container start or else "stdin close"
	// event will be rejected by containerd.
	// stdin closure happens in attachStdio
	stdin := iopipe.Stdin
	iopipe.Stdin = ioutils.NewWriteCloserWrapper(stdin, func() error {
		var err error
		stdinOnce.Do(func() { // on error from attach we don't know if stdin was already closed
			err = stdin.Close()
			go func() {
				select {
				case <-ready:
				case <-ctx.Done():
				}
				select {
				case <-ready:
					if err := ctr.sendCloseStdin(); err != nil {
						logrus.Warnf("failed to close stdin: %+v", err)
					}
				default:
				}
			}()
		})
		return err
	})

	/*
		定义对containerd模块的请求对象
	*/
	r := &containerd.CreateContainerRequest{
		Id:            ctr.containerID,
		BundlePath:    ctr.dir,
		Stdin:         ctr.fifo(syscall.Stdin),
		Stdout:        ctr.fifo(syscall.Stdout),
		Stderr:        ctr.fifo(syscall.Stderr),
		Checkpoint:    checkpoint,
		CheckpointDir: checkpointDir,
		// check to see if we are running in ramdisk to disable pivot root
		NoPivotRoot: os.Getenv("DOCKER_RAMDISK") != "",
		Runtime:     ctr.runtime,
		RuntimeArgs: ctr.runtimeArgs,
	}
	ctr.client.appendContainer(ctr)

	if err := attachStdio(*iopipe); err != nil {
		ctr.closeFifos(iopipe)
		return err
	}

	/*
		向containerd模块发送请求数据，实现容器创建
		grpc调用
			==>/vendor/github.com/docker/containerd/api/grpc/types/api.pb.go
				==>func (c *aPIClient) CreateContainer
	*/
	resp, err := ctr.client.remote.apiClient.CreateContainer(context.Background(), r)
	if err != nil {
		ctr.closeFifos(iopipe)
		return err
	}
	ctr.systemPid = systemPid(resp.Container)
	close(ready)

	/*
		启动成功后更新daemon中的容器状态
	*/
	return ctr.client.backend.StateChanged(ctr.containerID, StateInfo{
		CommonStateInfo: CommonStateInfo{
			State: StateStart,
			Pid:   ctr.systemPid,
		}})
}
```

### 发送grpc请求
见/vendor/github.com/docker/containerd/api/grpc/types/api.pb.go
```go
func (c *aPIClient) CreateContainer(ctx context.Context, in *CreateContainerRequest, opts ...grpc.CallOption) (*CreateContainerResponse, error) {
	out := new(CreateContainerResponse)
	err := grpc.Invoke(ctx, "/types.API/CreateContainer", in, out, c.cc, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Invoke sends the RPC request on the wire and returns after response is received.
// Invoke is called by generated code. Also users can call Invoke directly when it
// is really needed in their use cases.
/*
	func Invoke是负责发送RPC请求request,
*/
func Invoke(ctx context.Context, method string, args, reply interface{}, cc *ClientConn, opts ...CallOption) error {
	if cc.dopts.unaryInt != nil {
		return cc.dopts.unaryInt(ctx, method, args, reply, cc, invoke, opts...)
	}
	return invoke(ctx, method, args, reply, cc, opts...)
}

func invoke(ctx context.Context, method string, args, reply interface{}, cc *ClientConn, opts ...CallOption) (err error) {
	/*
		检查、设置参数
	*/
	c := defaultCallInfo
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return toRPCErr(err)
		}
	}
	defer func() {
		for _, o := range opts {
			o.after(&c)
		}
	}()
	if EnableTracing {
		c.traceInfo.tr = trace.New("grpc.Sent."+methodFamily(method), method)
		defer c.traceInfo.tr.Finish()
		c.traceInfo.firstLine.client = true
		if deadline, ok := ctx.Deadline(); ok {
			c.traceInfo.firstLine.deadline = deadline.Sub(time.Now())
		}
		c.traceInfo.tr.LazyLog(&c.traceInfo.firstLine, false)
		// TODO(dsymonds): Arrange for c.traceInfo.firstLine.remoteAddr to be set.
		defer func() {
			if err != nil {
				c.traceInfo.tr.LazyLog(&fmtStringer{"%v", []interface{}{err}}, true)
				c.traceInfo.tr.SetError()
			}
		}()
	}
	topts := &transport.Options{
		Last:  true,
		Delay: false,
	}
	for {
		var (
			err    error
			t      transport.ClientTransport
			stream *transport.Stream
			// Record the put handler from Balancer.Get(...). It is called once the
			// RPC has completed or failed.
			put func()
		)
		// TODO(zhaoq): Need a formal spec of fail-fast.
		/*
			发送请求的目的地址：Host
					和路由：Method ==> "/types.API/CreateContainer"
		*/
		callHdr := &transport.CallHdr{
			Host:   cc.authority,
			Method: method,
		}
		if cc.dopts.cp != nil {
			callHdr.SendCompress = cc.dopts.cp.Type()
		}
		gopts := BalancerGetOptions{
			BlockingWait: !c.failFast,
		}
		t, put, err = cc.getTransport(ctx, gopts)
		if err != nil {
			// TODO(zhaoq): Probably revisit the error handling.
			if _, ok := err.(*rpcError); ok {
				return err
			}
			if err == errConnClosing || err == errConnUnavailable {
				if c.failFast {
					return Errorf(codes.Unavailable, "%v", err)
				}
				continue
			}
			// All the other errors are treated as Internal errors.
			return Errorf(codes.Internal, "%v", err)
		}
		if c.traceInfo.tr != nil {
			c.traceInfo.tr.LazyLog(&payload{sent: true, msg: args}, true)
		}
		/*
			发送Request
		*/
		stream, err = sendRequest(ctx, cc.dopts.codec, cc.dopts.cp, callHdr, t, args, topts)
		if err != nil {
			if put != nil {
				put()
				put = nil
			}
			// Retry a non-failfast RPC when
			// i) there is a connection error; or
			// ii) the server started to drain before this RPC was initiated.
			if _, ok := err.(transport.ConnectionError); ok || err == transport.ErrStreamDrain {
				if c.failFast {
					return toRPCErr(err)
				}
				continue
			}
			return toRPCErr(err)
		}
		/*
			接受响应Response
		*/
		err = recvResponse(cc.dopts, t, &c, stream, reply)
		if err != nil {
			if put != nil {
				put()
				put = nil
			}
			if _, ok := err.(transport.ConnectionError); ok || err == transport.ErrStreamDrain {
				if c.failFast {
					return toRPCErr(err)
				}
				continue
			}
			return toRPCErr(err)
		}
		if c.traceInfo.tr != nil {
			c.traceInfo.tr.LazyLog(&payload{sent: false, msg: reply}, true)
		}
		t.CloseStream(stream, nil)
		if put != nil {
			put()
			put = nil
		}
		return Errorf(stream.StatusCode(), "%s", stream.StatusDesc())
	}
}
```

## daemon启动libContainerd
上面介绍了daemon是如何发送一个grpc请求，那么问题来了？ grpc的服务器是怎么启动的？在哪启动的？

通过containerdRemote可以向grpc服务器发送请求，在containerdRemote的创建过程中则启动了Containerd后台进程并对docker-containerd.sock设置了监听
```go
//  cmd/dockerd/daemon.go
//  func (cli *DaemonCli) start(opts daemonOptions) (err error) 
containerdRemote, err := libcontainerd.New(cli.getLibcontainerdRoot(), cli.getPlatformRemoteOptions()...)
```

### libcontainerd
```go
// New creates a fresh instance of libcontainerd remote.
func New(stateDir string, options ...RemoteOption) (_ Remote, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("Failed to connect to containerd. Please make sure containerd is installed in your PATH or you have specified the correct address. Got error: %v", err)
		}
	}()
	r := &remote{
		stateDir:    stateDir,
		daemonPid:   -1,
		eventTsPath: filepath.Join(stateDir, eventTimestampFilename),
	}
	for _, option := range options {
		if err := option.Apply(r); err != nil {
			return nil, err
		}
	}

	if err := sysinfo.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}

	if r.rpcAddr == "" {
		/*
			containerdSockFilename即docker-containerd.sock，为常量
			是grpc服务器的地址，一个socker套接字
		*/
		r.rpcAddr = filepath.Join(stateDir, containerdSockFilename)
	}

	/*
		runContainerdDaemon()启动grpc服务器、对套件字进行监听
	*/
	if r.startDaemon {
		if err := r.runContainerdDaemon(); err != nil {
			return nil, err
		}
	}

	// don't output the grpc reconnect logging
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := append([]grpc.DialOption{grpc.WithInsecure()},
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)
	/*
		调用grpc.Dial与grpc服务器建立连接conn。
	*/
	conn, err := grpc.Dial(r.rpcAddr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("error connecting to containerd: %v", err)
	}

	r.rpcConn = conn
	/*
		根据该连接建立api客户端对象，通过该对象就可以通过该连接发送请求json数据
	*/
	r.apiClient = containerd.NewAPIClient(conn)

	// Get the timestamp to restore from
	t := r.getLastEventTimestamp()
	tsp, err := ptypes.TimestampProto(t)
	if err != nil {
		logrus.Errorf("libcontainerd: failed to convert timestamp: %q", err)
	}
	r.restoreFromTimestamp = tsp

	//连接健康检查，定时0.5s访问连接检查一次
	go r.handleConnectionChange()

	//开启异常事件监听，包括kill，pause等命令的处理
	if err := r.startEventsMonitor(); err != nil {
		return nil, err
	}

	return r, nil
}
```

- runContainerdDaemon()
```go
func (r *remote) runContainerdDaemon() error {
	pidFilename := filepath.Join(r.stateDir, containerdPidFilename)
	f, err := os.OpenFile(pidFilename, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// File exist, check if the daemon is alive
	b := make([]byte, 8)
	n, err := f.Read(b)
	if err != nil && err != io.EOF {
		return err
	}

	if n > 0 {
		pid, err := strconv.ParseUint(string(b[:n]), 10, 64)
		if err != nil {
			return err
		}
		if utils.IsProcessAlive(int(pid)) {
			logrus.Infof("libcontainerd: previous instance of containerd still alive (%d)", pid)
			r.daemonPid = int(pid)
			return nil
		}
	}

	// rewind the file
	_, err = f.Seek(0, os.SEEK_SET)
	if err != nil {
		return err
	}

	// Truncate it
	err = f.Truncate(0)
	if err != nil {
		return err
	}

	// Start a new instance
	/*
		设置docker－containerd命令执行的参数，其中－l flag指定了Containerd服务需要监听的套接字文件。
	*/
	args := []string{
		"-l", fmt.Sprintf("unix://%s", r.rpcAddr),
		"--metrics-interval=0",
		"--start-timeout", "2m",
		"--state-dir", filepath.Join(r.stateDir, containerdStateDir),
	}
	if goruntime.GOOS == "solaris" {
		args = append(args, "--shim", "containerd-shim", "--runtime", "runc")
	} else {
		args = append(args, "--shim", "docker-containerd-shim")
		if r.runtime != "" {
			args = append(args, "--runtime")
			args = append(args, r.runtime)
		}
	}
	if r.debugLog {
		args = append(args, "--debug")
	}
	if len(r.runtimeArgs) > 0 {
		for _, v := range r.runtimeArgs {
			args = append(args, "--runtime-args")
			args = append(args, v)
		}
		logrus.Debugf("libcontainerd: runContainerdDaemon: runtimeArgs: %s", args)
	}

	/*
		根据参数设置docker-contained命令：
		docker-containerd -l unix:///var/run/docker/libcontainerd/docker-containerd.sock --metrics-interval=0 --start-timeout 2m --state-dir /var/run/docker/libcontainerd/containerd --shim docker-containerd-shim --runtime docker-runc
		该docker-containerd为二进制文件，
		该二进制文件的生成是在Docker源码编译过程中hack/dockerfile/install-binaries.sh脚本文件中根据https://github.com/docker/containerd.git上的源码编译后产生的，
		所以当执行该docker-containerd命令后如何实现grpc服务器端端建立及对该套接字的监听则需要从github.com/docker/containerd这个项目的源码中分析。
	*/
	cmd := exec.Command(containerdBinary, args...)
	// redirect containerd logs to docker logs
	/*将containerd服务端的输入输出流信息重定向到dockerdaemon上来统一打印日志信息*/
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = setSysProcAttr(true)
	cmd.Env = nil
	// clear the NOTIFY_SOCKET from the env when starting containerd
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NOTIFY_SOCKET") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	/*
		执行docker-contained
	*/
	if err := cmd.Start(); err != nil {
		return err
	}
	logrus.Infof("libcontainerd: new containerd process, pid: %d", cmd.Process.Pid)
	if err := setOOMScore(cmd.Process.Pid, r.oomScore); err != nil {
		utils.KillProcess(cmd.Process.Pid)
		return err
	}
	if _, err := f.WriteString(fmt.Sprintf("%d", cmd.Process.Pid)); err != nil {
		utils.KillProcess(cmd.Process.Pid)
		return err
	}

	/*
		开启一个goroutine等待containerd的异常信号，
		若接收到异常信号关闭daemonwaitch
	*/
	r.daemonWaitCh = make(chan struct{})
	go func() {
		cmd.Wait()
		close(r.daemonWaitCh)
	}() // Reap our child when needed
	r.daemonPid = cmd.Process.Pid
	return nil
}
```

## 参考
[docker容器网络通信原理分析](http://blog.csdn.net/yarntime/article/details/51258824)

[Docker libnetwork模型](http://www.cnblogs.com/YaoDD/p/6386166.html)