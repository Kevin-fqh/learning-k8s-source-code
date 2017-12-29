# docker container create命令

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [用法](#用法)
  - [postContainersCreate](#postcontainerscreate)
  - [ContainerCreate](#containercreate)
  - [func create](#func-create)
  - [newContainer](#newcontainer)
	- [BaseContainer](#basecontainer)
	- [type CommonContainer struct](#type-commoncontainer-struct)
	- [创建一个container的RWLayer](#创建一个container的rwlayer)
	- [MountPoints的设置](#mountpoints的设置)
<!-- END MUNGE: GENERATED_TOC -->

## 用法
```go
# docker container create --name myetcd etcd_cluster:gc4.0 
```

create命令完成的工作：
1. 在hots主机上根据用户指定的配置生成一个container的工作目录，/var/lib/docker/containers/{id}，同时把配置属性给持久化下来。
2. 然后向daemon注册该container，注册之后，daemon就可以通过<container.ID>来使用该容器。
3. 并没后把该容器run起来！不涉及到底层`containerd`等工具的调用。

## postContainersCreate
这是docker daemon响应docker client端命令的handler method。 
见/api/server/router/container/container_routes.go

1. 解析request，得到相关参数
2. 调用backend.ContainerCreate()，其中backend其实就是daemon
```go
func (s *containerRouter) postContainersCreate(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	if err := httputils.ParseForm(r); err != nil {
		return err
	}
	if err := httputils.CheckForJSON(r); err != nil {
		return err
	}

	/*
		如果指定了name
	*/
	name := r.Form.Get("name")

	/*
		从这里可以学习go是如何把复杂的json数据转化为响应的数据结构的！
		解析request.body，得到配置信息
		把一个io.Reader转化为一个container的configuration
			==>/runconfig/config.go
				==>func (r ContainerDecoder) DecodeConfig
	*/
	config, hostConfig, networkingConfig, err := s.decoder.DecodeConfig(r.Body)
	if err != nil {
		return err
	}
	version := httputils.VersionFromContext(ctx)
	adjustCPUShares := versions.LessThan(version, "1.19")

	/*
		create一个container
			==>/daemon/create.go
				==>func (daemon *Daemon) ContainerCreate
	*/
	ccr, err := s.backend.ContainerCreate(types.ContainerCreateConfig{
		Name:             name,
		Config:           config,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		AdjustCPUShares:  adjustCPUShares,
	})
	if err != nil {
		return err
	}

	return httputils.WriteJSON(w, http.StatusCreated, ccr)
}
```

## ContainerCreate
关键是最后的调用func (daemon *Daemon) create
```go
// ContainerCreate creates a regular container
func (daemon *Daemon) ContainerCreate(params types.ContainerCreateConfig) (containertypes.ContainerCreateCreatedBody, error) {
	return daemon.containerCreate(params, false)
}

func (daemon *Daemon) containerCreate(params types.ContainerCreateConfig, managed bool) (containertypes.ContainerCreateCreatedBody, error) {
	start := time.Now()
	/*
		验证HostConfig、Config、NetworkingConfig、AdjustCPUShares配置信息
	*/
	if params.Config == nil {
		return containertypes.ContainerCreateCreatedBody{}, fmt.Errorf("Config cannot be empty in order to create a container")
	}

	warnings, err := daemon.verifyContainerSettings(params.HostConfig, params.Config, false)
	if err != nil {
		return containertypes.ContainerCreateCreatedBody{Warnings: warnings}, err
	}

	err = daemon.verifyNetworkingConfig(params.NetworkingConfig)
	if err != nil {
		return containertypes.ContainerCreateCreatedBody{Warnings: warnings}, err
	}

	if params.HostConfig == nil {
		params.HostConfig = &containertypes.HostConfig{}
	}
	err = daemon.adaptContainerSettings(params.HostConfig, params.AdjustCPUShares)
	if err != nil {
		return containertypes.ContainerCreateCreatedBody{Warnings: warnings}, err
	}

	/*
		调用func (daemon *Daemon) create
	*/
	container, err := daemon.create(params, managed)
	if err != nil {
		return containertypes.ContainerCreateCreatedBody{Warnings: warnings}, daemon.imageNotExistToErrcode(err)
	}
	containerActions.WithValues("create").UpdateSince(start)

	return containertypes.ContainerCreateCreatedBody{ID: container.ID, Warnings: warnings}, nil
}
```

## func create
1. 获取到image
2. 调用func (daemon *Daemon) newContainer 创建一个baseContainer
3. 设置baseContainer的读写层、config文件
4. 向daemon注册该container，注册之后，daemon就可以通过<container.ID>来使用该容器
```go
// Create creates a new container from the given configuration with a given name.
func (daemon *Daemon) create(params types.ContainerCreateConfig, managed bool) (retC *container.Container, retErr error) {
	/*
		创建一个container需要三个元素
	*/
	var (
		container *container.Container
		img       *image.Image
		imgID     image.ID
		err       error
	)

	if params.Config.Image != "" {
		img, err = daemon.GetImage(params.Config.Image) //镜像
		if err != nil {
			return nil, err
		}

		if runtime.GOOS == "solaris" && img.OS != "solaris " {
			return nil, errors.New("Platform on which parent image was created is not Solaris")
		}
		imgID = img.ID() //镜像ID
	}

	if err := daemon.mergeAndVerifyConfig(params.Config, img); err != nil {
		return nil, err
	}

	if err := daemon.mergeAndVerifyLogConfig(&params.HostConfig.LogConfig); err != nil {
		return nil, err
	}

	/*
		继续调用func (daemon *Daemon) newContainer
			==>/daemon/container.go
	*/
	if container, err = daemon.newContainer(params.Name, params.Config, params.HostConfig, imgID, managed); err != nil {
		return nil, err
	}
	defer func() {
		/*
			创建失败的话，调用func (daemon *Daemon) cleanupContainer执行cleanup动作
				==>/daemon/delete.go
		*/
		if retErr != nil {
			if err := daemon.cleanupContainer(container, true, true); err != nil {
				logrus.Errorf("failed to cleanup container on create error: %v", err)
			}
		}
	}()

	//设置容器的安全参数
	if err := daemon.setSecurityOptions(container, params.HostConfig); err != nil {
		return nil, err
	}

	container.HostConfig.StorageOpt = params.HostConfig.StorageOpt

	// Set RWLayer for container after mount labels have been set
	/* 设置容器的可读写层layer */
	if err := daemon.setRWLayer(container); err != nil {
		return nil, err
	}

	/*
		==>/pkg/idtools/idtools.go
		得到的rootUID, rootGID值：0，0
	*/
	rootUID, rootGID, err := idtools.GetRootUIDGID(daemon.uidMaps, daemon.gidMaps)
	if err != nil {
		return nil, err
	}
	if err := idtools.MkdirAs(container.Root, 0700, rootUID, rootGID); err != nil {
		return nil, err
	}
	/*
		创建目录 /var/lib/docker/containers/{id}/checkpoints
		并设置ownership
		==>/container/container.go
			==>func (container *Container) CheckpointDir()
	*/
	if err := idtools.MkdirAs(container.CheckpointDir(), 0700, rootUID, rootGID); err != nil {
		return nil, err
	}
	/*
		执行到这里，/var/lib/docker/containers/{id}下仅仅创建了一个checkpoints目录
	*/
	/*
		setHostConfig()设置container的一些属性（包括MountPoints），
		然后把容器的配置信息持久化
		/var/lib/docker/containers/{id}/config.v2.json
		/var/lib/docker/containers/{id}/hostconfig.json
	*/
	if err := daemon.setHostConfig(container, params.HostConfig); err != nil {
		return nil, err
	}

	if err := daemon.createContainerPlatformSpecificSettings(container, params.Config, params.HostConfig); err != nil {
		return nil, err
	}

	var endpointsConfigs map[string]*networktypes.EndpointSettings
	if params.NetworkingConfig != nil {
		endpointsConfigs = params.NetworkingConfig.EndpointsConfig
	}
	// Make sure NetworkMode has an acceptable value. We do this to ensure
	// backwards API compatibility.
	container.HostConfig = runconfig.SetDefaultNetModeIfBlank(container.HostConfig)

	daemon.updateContainerNetworkSettings(container, endpointsConfigs)

	if err := container.ToDisk(); err != nil {
		logrus.Errorf("Error saving new container to disk: %v", err)
		return nil, err
	}
	/*
		向daemon注册该container
		注册之后，daemon就可以通过<container.ID>来使用该容器
	*/
	if err := daemon.Register(container); err != nil {
		return nil, err
	}
	daemon.LogContainerEvent(container, "create")
	return container, nil
}
```

## newContainer
```go
func (daemon *Daemon) newContainer(name string, config *containertypes.Config, hostConfig *containertypes.HostConfig, imgID image.ID, managed bool) (*container.Container, error) {
	var (
		id             string
		err            error
		noExplicitName = name == ""
	)
	/*
		生成容器的ID和name
	*/
	id, name, err = daemon.generateIDAndName(name)
	if err != nil {
		return nil, err
	}

	if hostConfig.NetworkMode.IsHost() {
		if config.Hostname == "" {
			config.Hostname, err = os.Hostname()
			if err != nil {
				return nil, err
			}
		}
	} else {
		daemon.generateHostname(id, config)
	}
	entrypoint, args := daemon.getEntrypointAndArgs(config.Entrypoint, config.Cmd)

	/*
		创建一个BaseContainer
	*/
	base := daemon.newBaseContainer(id)
	base.Created = time.Now().UTC()
	base.Managed = managed
	base.Path = entrypoint
	base.Args = args //FIXME: de-duplicate from config
	base.Config = config
	base.HostConfig = &containertypes.HostConfig{}
	base.ImageID = imgID
	base.NetworkSettings = &network.Settings{IsAnonymousEndpoint: noExplicitName}
	base.Name = name
	base.Driver = daemon.GraphDriverName()

	return base, err
}
```

### BaseContainer
```go
// NewBaseContainer creates a new container with its
// basic configuration.
func NewBaseContainer(id, root string) *Container {
	return &Container{
		CommonContainer: CommonContainer{
			ID:            id,
			State:         NewState(),
			ExecCommands:  exec.NewStore(),
			Root:          root,
			MountPoints:   make(map[string]*volume.MountPoint),
			StreamConfig:  stream.NewConfig(),
			attachContext: &attachContext{},
		},
	}
}
```

至此，创建一个BaseContainer流程已经完成，那么后面将由daemon继续完成设置该container的属性配置、读写layer设置和注册等工作。

### type CommonContainer struct
见/container/container.go
```go
// CommonContainer holds the fields for a container which are
// applicable across all platforms supported by the daemon.
type CommonContainer struct {
	StreamConfig *stream.Config
	// embed for Container to support states directly.
	*State          `json:"State"` // Needed for Engine API version <= 1.11
	Root            string         `json:"-"` // Path to the "home" of the container, including metadata.
	/*
		BaseFS，其值一般会设置为RWLayer的挂载点path，这是一个container整个文件系统的路径
	*/
	BaseFS          string         `json:"-"` // Path to the graphdriver mountpoint
	RWLayer         layer.RWLayer  `json:"-"`
	ID              string
	Created         time.Time
	Managed         bool
	Path            string
	Args            []string
	Config          *containertypes.Config
	ImageID         image.ID `json:"Image"`
	NetworkSettings *network.Settings
	LogPath         string
	Name            string
	Driver          string
	// MountLabel contains the options for the 'mount' command
	MountLabel             string
	ProcessLabel           string
	RestartCount           int
	HasBeenStartedBefore   bool
	HasBeenManuallyStopped bool // used for unless-stopped restart policy
	MountPoints            map[string]*volume.MountPoint
	HostConfig             *containertypes.HostConfig `json:"-"` // do not serialize the host config in the json, otherwise we'll make the container unportable
	ExecCommands           *exec.Store                `json:"-"`
	SecretStore            agentexec.SecretGetter     `json:"-"`
	SecretReferences       []*swarmtypes.SecretReference
	// logDriver for closing
	LogDriver      logger.Logger  `json:"-"`
	LogCopier      *logger.Copier `json:"-"`
	restartManager restartmanager.RestartManager
	attachContext  *attachContext
}
```

### 创建一个container的RWLayer
见/daemon/create.go
```go
func (daemon *Daemon) setRWLayer(container *container.Container) error {
	var layerID layer.ChainID
	if container.ImageID != "" {
		img, err := daemon.imageStore.Get(container.ImageID)
		if err != nil {
			return err
		}
		/*
			根据img.RootFS中记录的diffID计算出该image的最后一个chainID
				==>/image/rootfs.go
					==>func (r *RootFS) ChainID()
		*/
		layerID = img.RootFS.ChainID()
	}

	/*
		创建该container的RWLayer
		==>/layer/layer_store.go
			==>func (ls *layerStore) CreateRWLayer
	*/
	rwLayer, err := daemon.layerStore.CreateRWLayer(container.ID, layerID, container.MountLabel, daemon.getLayerInit(), container.HostConfig.StorageOpt)

	if err != nil {
		return err
	}
	container.RWLayer = rwLayer

	return nil
}
```

继续CreateRWLayer()
```go
func (ls *layerStore) CreateRWLayer(name string, parent ChainID, mountLabel string, initFunc MountInit, storageOpt map[string]string) (RWLayer, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	m, ok := ls.mounts[name]
	if ok {
		return nil, ErrMountNameConflict
	}

	var err error
	var pid string
	var p *roLayer
	if string(parent) != "" {
		p = ls.get(parent)
		if p == nil {
			return nil, ErrLayerDoesNotExist
		}
		pid = p.cacheID

		// Release parent chain if error
		defer func() {
			if err != nil {
				ls.layerL.Lock()
				ls.releaseLayer(p)
				ls.layerL.Unlock()
			}
		}()
	}

	m = &mountedLayer{
		name:   name, //container.ID
		parent: p,    //一个container的读写layer的parent属性是其使用的image的最后一层的ChainID
		/*
			mountID 对应的目录是 /var/docker/overlay/{mountID}
			Random随机生成的
		*/
		mountID:    ls.mountID(name),
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if initFunc != nil {
		pid, err = ls.initMount(m.mountID, pid, mountLabel, initFunc, storageOpt)
		if err != nil {
			return nil, err
		}
		m.initID = pid
	}

	createOpts := &graphdriver.CreateOpts{
		StorageOpt: storageOpt,
	}

	if err = ls.driver.CreateReadWrite(m.mountID, pid, createOpts); err != nil {
		return nil, err
	}

	if err = ls.saveMount(m); err != nil {
		return nil, err
	}

	return m.getReference(), nil
}
```
其中type mountedLayer struct的定义如下
```go
/*
	实现了/layer/layer.go中的type RWLayer interface
*/
type mountedLayer struct {
	name       string //其值一般是container.ID
	mountID    string //对应的目录是 /var/docker/overlay/{mountID}
	initID     string
	parent     *roLayer //一个container的读写layer的parent属性是其使用的image的最后一层的ChainID
	path       string
	layerStore *layerStore

	references map[RWLayer]*referencedRWLayer
}
```

### MountPoints的设置
最后，关于该container挂载点的设置需要注意一下规则，一个容器可能会有多个MountPoints，规则如下，按顺序走一遍：
1. 如果有的话，选择容器的先前配置的MountPoints。
2. 选择从另一个容器装入的卷。 覆盖以前配置的MountPoints。
3. 选择client端设置的 bind mounts。 覆盖以前配置的MountPoints。
4. 清理即将被重新分配的旧volumes。
```go
// registerMountPoints initializes the container mount points with the configured volumes and bind mounts.
// It follows the next sequence to decide what to mount in each final destination:
//
// 1. Select the previously configured mount points for the containers, if any.
// 2. Select the volumes mounted from another containers. Overrides previously configured mount point destination.
// 3. Select the bind mounts set by the client. Overrides previously configured mount point destinations.
// 4. Cleanup old volumes that are about to be reassigned.
/*
	func registerMountPoints 设置container.MountPoints（一个map类型）
*/
func (daemon *Daemon) registerMountPoints(container *container.Container, hostConfig *containertypes.HostConfig) (retErr error) {
	binds := map[string]bool{}
	mountPoints := map[string]*volume.MountPoint{}
	defer func() {
		// clean up the container mountpoints once return with error
		if retErr != nil {
			for _, m := range mountPoints {
				if m.Volume == nil {
					continue
				}
				daemon.volumes.Dereference(m.Volume, container.ID)
			}
		}
	}()

	// 1. Read already configured mount points.
	for destination, point := range container.MountPoints {
		mountPoints[destination] = point
	}

	// 2. Read volumes from other containers.
	for _, v := range hostConfig.VolumesFrom {
		containerID, mode, err := volume.ParseVolumesFrom(v)
		if err != nil {
			return err
		}

		c, err := daemon.GetContainer(containerID)
		if err != nil {
			return err
		}

		for _, m := range c.MountPoints {
			cp := &volume.MountPoint{
				Name:        m.Name,
				Source:      m.Source,
				RW:          m.RW && volume.ReadWrite(mode),
				Driver:      m.Driver,
				Destination: m.Destination,
				Propagation: m.Propagation,
				Spec:        m.Spec,
				CopyData:    false,
			}

			if len(cp.Source) == 0 {
				v, err := daemon.volumes.GetWithRef(cp.Name, cp.Driver, container.ID)
				if err != nil {
					return err
				}
				cp.Volume = v
			}

			mountPoints[cp.Destination] = cp
		}
	}

	// 3. Read bind mounts
	for _, b := range hostConfig.Binds {
		bind, err := volume.ParseMountRaw(b, hostConfig.VolumeDriver)
		if err != nil {
			return err
		}

		// #10618
		_, tmpfsExists := hostConfig.Tmpfs[bind.Destination]
		if binds[bind.Destination] || tmpfsExists {
			return fmt.Errorf("Duplicate mount point '%s'", bind.Destination)
		}

		if bind.Type == mounttypes.TypeVolume {
			// create the volume
			v, err := daemon.volumes.CreateWithRef(bind.Name, bind.Driver, container.ID, nil, nil)
			if err != nil {
				return err
			}
			bind.Volume = v
			bind.Source = v.Path()
			// bind.Name is an already existing volume, we need to use that here
			bind.Driver = v.DriverName()
			if bind.Driver == volume.DefaultDriverName {
				setBindModeIfNull(bind)
			}
		}

		binds[bind.Destination] = true
		mountPoints[bind.Destination] = bind
	}

	for _, cfg := range hostConfig.Mounts {
		mp, err := volume.ParseMountSpec(cfg)
		if err != nil {
			return dockererrors.NewBadRequestError(err)
		}

		if binds[mp.Destination] {
			return fmt.Errorf("Duplicate mount point '%s'", cfg.Target)
		}

		if mp.Type == mounttypes.TypeVolume {
			var v volume.Volume
			if cfg.VolumeOptions != nil {
				var driverOpts map[string]string
				if cfg.VolumeOptions.DriverConfig != nil {
					driverOpts = cfg.VolumeOptions.DriverConfig.Options
				}
				v, err = daemon.volumes.CreateWithRef(mp.Name, mp.Driver, container.ID, driverOpts, cfg.VolumeOptions.Labels)
			} else {
				v, err = daemon.volumes.CreateWithRef(mp.Name, mp.Driver, container.ID, nil, nil)
			}
			if err != nil {
				return err
			}

			if err := label.Relabel(mp.Source, container.MountLabel, false); err != nil {
				return err
			}
			mp.Volume = v
			mp.Name = v.Name()
			mp.Driver = v.DriverName()

			// only use the cached path here since getting the path is not necessary right now and calling `Path()` may be slow
			if cv, ok := v.(interface {
				CachedPath() string
			}); ok {
				mp.Source = cv.CachedPath()
			}
		}

		binds[mp.Destination] = true
		mountPoints[mp.Destination] = mp
	}

	container.Lock()

	// 4. Cleanup old volumes that are about to be reassigned.
	for _, m := range mountPoints {
		if m.BackwardsCompatible() {
			if mp, exists := container.MountPoints[m.Destination]; exists && mp.Volume != nil {
				daemon.volumes.Dereference(mp.Volume, container.ID)
			}
		}
	}
	container.MountPoints = mountPoints

	container.Unlock()

	return nil
}
```
