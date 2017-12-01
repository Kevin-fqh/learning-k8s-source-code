# daemon的创建过程

接着前面daemon的创建过程，来看看怎么真正创建一个Daemon。

## type Daemon struct
```go
// Daemon holds information about the Docker daemon.
type Daemon struct {
	ID                        string
	repository                string
	containers                container.Store
	execCommands              *exec.Store
	referenceStore            reference.Store
	downloadManager           *xfer.LayerDownloadManager
	uploadManager             *xfer.LayerUploadManager
	distributionMetadataStore dmetadata.Store
	trustKey                  libtrust.PrivateKey
	idIndex                   *truncindex.TruncIndex
	configStore               *Config
	statsCollector            *statsCollector
	defaultLogConfig          containertypes.LogConfig
	RegistryService           registry.Service
	EventsService             *events.Events
	netController             libnetwork.NetworkController
	volumes                   *store.VolumeStore
	discoveryWatcher          discoveryReloader
	root                      string
	seccompEnabled            bool
	shutdown                  bool
	uidMaps                   []idtools.IDMap
	gidMaps                   []idtools.IDMap
	layerStore                layer.Store
	imageStore                image.Store
	PluginStore               *plugin.Store // todo: remove
	pluginManager             *plugin.Manager
	nameIndex                 *registrar.Registrar
	linkIndex                 *linkIndex
	containerd                libcontainerd.Client
	containerdRemote          libcontainerd.Remote
	defaultIsolation          containertypes.Isolation // Default isolation mode on Windows
	clusterProvider           cluster.Provider
	cluster                   Cluster

	seccompProfile     []byte
	seccompProfilePath string
}
```

## NewDaemon
func NewDaemon负责真正创建一个type Daemon struct对象，检查一些参数设置，同时负责创建各个目录作为docker的工作目录。部分目录文件列举如下：
- /var/lib/docker/tmp 临时目录
- /var/lib/docker/containers 用来记录的是容器相关的信息，每运行一个容器，就在这个目录下面生成一个容器id对应的子目录
- /var/lib/docker/image/${graphDriverName}/layerdb 记录镜像layer的元数据，每个层级都是一个目录，其ID称之为ChainID
- /var/lib/docker/image/${graphDriverName}/imagedb 记录镜像元数据
- /var/lib/docker/volumes/metadata.db 记录volume的元数据
- /var/lib/docker/trust 用来放一些证书文件
- /var/lib/docker/image/${graphDriverName}/distribution 这个目录用来记录layer元数据与镜像元数据之间的关联关系
- /var/lib/docker/image/${graphDriverName}/repositories.json 是用来记录镜像仓库元数据,是imagedb 目录的索引

列出其中的关键核心结构体的创建过程，后面再一一进行分析。
1. type layerStore struct，记录主机上面所有的layer层信息，包括只读layer和读写layer。其中涉及到graph driver ，主要用来管理容器文件系统及镜像存储的组件,与宿主机对各文件系统的支持相关。
2. ImageStore，根据所有layer来构建image，维护所有image的元数据
3. type VolumeStore struct，记录着所有的volumes，同时跟踪它们的使用情况
4. type FSMetadataStore struct，使用filesystem来把 layer和image IDs关联起来
5. reference store是一个tag store，负责管理镜像的tag

```go
// NewDaemon sets up everything for the daemon to be able to service
// requests from the webserver.
func NewDaemon(config *Config, registryService registry.Service, containerdRemote libcontainerd.Remote) (daemon *Daemon, err error) {
	//配置Docker容器的MTU,容器网络的最大传输单元
	setDefaultMtu(config)

	// Ensure that we have a correct root key limit for launching containers.
	if err := ModifyRootKeyLimit(); err != nil {
		logrus.Warnf("unable to modify root key limit, number of containers could be limited by this quota: %v", err)
	}

	// Ensure we have compatible and valid configuration options
	/*
		/daemon/daemon_unix.go
			==>func verifyDaemonSettings
		检查参数设置是否有效
	*/
	if err := verifyDaemonSettings(config); err != nil {
		return nil, err
	}

	// Do we have a disabled network?
	config.DisableBridge = isBridgeNetworkDisabled(config)

	// Verify the platform is supported as a daemon
	if !platformSupported {
		return nil, errSystemNotSupported
	}

	// Validate platform-specific requirements
	if err := checkSystem(); err != nil {
		return nil, err
	}

	/*
		User namespaces的隔离
		将容器内的用户映射为宿主机上的普通用户
	*/
	uidMaps, gidMaps, err := setupRemappedRoot(config)
	if err != nil {
		return nil, err
	}
	/*rootUID, rootGID 的值：0 0 */
	rootUID, rootGID, err := idtools.GetRootUIDGID(uidMaps, gidMaps)
	if err != nil {
		return nil, err
	}

	/*
		设置daemon的oom
	*/
	if err := setupDaemonProcess(config); err != nil {
		return nil, err
	}

	// set up the tmpDir to use a canonical path
	/*创建临时目录*/
	tmp, err := tempDir(config.Root, rootUID, rootGID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get the TempDir under %s: %s", config.Root, err)
	}
	realTmp, err := fileutils.ReadSymlinkedDirectory(tmp)
	if err != nil {
		return nil, fmt.Errorf("Unable to get the full path to the TempDir (%s): %s", tmp, err)
	}
	os.Setenv("TMPDIR", realTmp)

	/*
		创建一个type Daemon struct对象
	*/
	d := &Daemon{configStore: config}
	// Ensure the daemon is properly shutdown if there is a failure during
	// initialization
	defer func() {
		if err != nil {
			if err := d.Shutdown(); err != nil {
				logrus.Error(err)
			}
		}
	}()

	if err := d.setupSeccompProfile(); err != nil {
		return nil, err
	}

	// Set the default isolation mode (only applicable on Windows)
	if err := d.setDefaultIsolation(); err != nil {
		return nil, fmt.Errorf("error setting default isolation mode: %v", err)
	}

	logrus.Debugf("Using default logging driver %s", config.LogConfig.Type)

	/*go线程数量限制*/
	if err := configureMaxThreads(config); err != nil {
		logrus.Warnf("Failed to configure golang's threads limit: %v", err)
	}

	if err := ensureDefaultAppArmorProfile(); err != nil {
		logrus.Errorf(err.Error())
	}

	/*
		初始化与镜像存储相关的目录及Store
		  /var/lib/docker/containers 这个目录是用来记录的是容器相关的信息，每运行一个容器，就在这个目录下面生成一个容器Id对应的子目录
	*/
	daemonRepo := filepath.Join(config.Root, "containers")
	if err := idtools.MkdirAllAs(daemonRepo, 0700, rootUID, rootGID); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if runtime.GOOS == "windows" {
		if err := system.MkdirAll(filepath.Join(config.Root, "credentialspecs"), 0); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}

	/*
		graph driver 是主要用来管理容器文件系统及镜像存储的组件,与宿主机对各文件系统的支持相关。
			比如 ubuntu 上默认使用的是 AUFS , Centos 上是 devicemapper , Coreos 上则是 btrfs 。
		graph driver 定义了一个统一的、抽象的接口,以一种可扩展的方式对各文件系统提供了支持。
	*/
	driverName := os.Getenv("DOCKER_DRIVER")
	if driverName == "" {
		driverName = config.GraphDriver
	}

	d.RegistryService = registryService
	d.PluginStore = plugin.NewStore(config.Root) // todo: remove
	// Plugin system initialization should happen before restore. Do not change order.
	d.pluginManager, err = plugin.NewManager(plugin.ManagerConfig{
		Root:               filepath.Join(config.Root, "plugins"),
		ExecRoot:           "/run/docker/plugins", // possibly needs fixing
		Store:              d.PluginStore,
		Executor:           containerdRemote,
		RegistryService:    registryService,
		LiveRestoreEnabled: config.LiveRestoreEnabled,
		LogPluginEvent:     d.LogPluginEvent, // todo: make private
	})
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create plugin manager")
	}

	/*
		layerStore：记录一个hots主机上面存储着的所有的layer层信息，包括只读layer和读写layer
		/var/lib/docker/image/${graphDriverName}/layerdb
		==>/layer/layer_store.go
			==>func NewStoreFromOptions
	*/
	d.layerStore, err = layer.NewStoreFromOptions(layer.StoreOptions{
		StorePath:                 config.Root,
		MetadataStorePathTemplate: filepath.Join(config.Root, "image", "%s", "layerdb"),
		GraphDriver:               driverName,
		GraphDriverOptions:        config.GraphOptions,
		UIDMaps:                   uidMaps,
		GIDMaps:                   gidMaps,
		PluginGetter:              d.PluginStore,
		ExperimentalEnabled:       config.Experimental,
	})
	if err != nil {
		return nil, err
	}

	graphDriver := d.layerStore.DriverName()
	imageRoot := filepath.Join(config.Root, "image", graphDriver)

	// Configure and validate the kernels security support
	if err := configureKernelSecuritySupport(config, graphDriver); err != nil {
		return nil, err
	}

	logrus.Debugf("Max Concurrent Downloads: %d", *config.MaxConcurrentDownloads)
	d.downloadManager = xfer.NewLayerDownloadManager(d.layerStore, *config.MaxConcurrentDownloads)
	logrus.Debugf("Max Concurrent Uploads: %d", *config.MaxConcurrentUploads)
	d.uploadManager = xfer.NewLayerUploadManager(*config.MaxConcurrentUploads)

	/*
		/var/lib/docker/image/${graphDriverName}/imagedb 这个目录是用来记录镜像元数据
			==>/image/fs.go
	*/
	ifs, err := image.NewFSStoreBackend(filepath.Join(imageRoot, "imagedb"))
	if err != nil {
		return nil, err
	}

	/*
		imageStore：根据所有layer来构建image，维护所有image的元数据
		根据StoreBackend ifs和 layerStore来创建一个imageStore
		==>/image/store.go
	*/
	d.imageStore, err = image.NewImageStore(ifs, d.layerStore)
	if err != nil {
		return nil, err
	}

	// Configure the volumes driver
	/*
		VolumeStore：记录着所有的volumes，同时跟踪它们的使用情况，相当于volume的一个cache
		创建/var/lib/docker/volumes/metadata.db，记录volume的元数据
		Volumes 是一种特殊的目录，其数据可以被一个或多个 container 共享,
		它和创建它的 container 的生命周期分离开来，
		在 container 被删去之后能继续存在。
	*/
	volStore, err := d.configureVolumes(rootUID, rootGID)
	if err != nil {
		return nil, err
	}

	trustKey, err := api.LoadOrCreateTrustKey(config.TrustKeyPath)
	if err != nil {
		return nil, err
	}

	/*
		/var/lib/docker/trust 这个目录用来放一些证书文件
	*/
	trustDir := filepath.Join(config.Root, "trust")

	if err := system.MkdirAll(trustDir, 0700); err != nil {
		return nil, err
	}

	/*
		type FSMetadataStore struct，使用filesystem来把 layer和image IDs关联起来
		/var/lib/docker/image/${graphDriverName}/distribution
			==>/distribution/metadata/metadata.go
	*/
	distributionMetadataStore, err := dmetadata.NewFSMetadataStore(filepath.Join(imageRoot, "distribution"))
	if err != nil {
		return nil, err
	}

	eventsService := events.New()

	/*
		reference store是一个tag store，负责管理镜像的tag
		/var/lib/docker/image/${graphDriverName}/repositories.json 是用来记录镜像仓库元数据，是imagedb 目录的索引
			==>/reference/store.go
	*/
	referenceStore, err := reference.NewReferenceStore(filepath.Join(imageRoot, "repositories.json"))
	if err != nil {
		return nil, fmt.Errorf("Couldn't create Tag store repositories: %s", err)
	}

	/*
		Migrate和迁移旧Graph数据相关
	*/
	migrationStart := time.Now()
	if err := v1.Migrate(config.Root, graphDriver, d.layerStore, d.imageStore, referenceStore, distributionMetadataStore); err != nil {
		logrus.Errorf("Graph migration failed: %q. Your old graph data was found to be too inconsistent for upgrading to content-addressable storage. Some of the old data was probably not upgraded. We recommend starting over with a clean storage directory if possible.", err)
	}
	logrus.Infof("Graph migration to content-addressability took %.2f seconds", time.Since(migrationStart).Seconds())

	// Discovery is only enabled when the daemon is launched with an address to advertise.  When
	// initialized, the daemon is registered and we can store the discovery backend as its read-only
	/*
		和对外发布相关
	*/
	if err := d.initDiscovery(config); err != nil {
		return nil, err
	}

	sysInfo := sysinfo.New(false)
	// Check if Devices cgroup is mounted, it is hard requirement for container security,
	// on Linux.
	if runtime.GOOS == "linux" && !sysInfo.CgroupDevicesEnabled {
		return nil, fmt.Errorf("Devices cgroup isn't mounted")
	}

	d.ID = trustKey.PublicKey().KeyID()
	d.repository = daemonRepo
	d.containers = container.NewMemoryStore()
	d.execCommands = exec.NewStore()
	d.referenceStore = referenceStore
	d.distributionMetadataStore = distributionMetadataStore
	d.trustKey = trustKey
	d.idIndex = truncindex.NewTruncIndex([]string{})
	d.statsCollector = d.newStatsCollector(1 * time.Second)
	d.defaultLogConfig = containertypes.LogConfig{
		Type:   config.LogConfig.Type,
		Config: config.LogConfig.Config,
	}
	d.EventsService = eventsService
	d.volumes = volStore
	d.root = config.Root
	d.uidMaps = uidMaps
	d.gidMaps = gidMaps
	d.seccompEnabled = sysInfo.Seccomp

	d.nameIndex = registrar.NewRegistrar()
	d.linkIndex = newLinkIndex()
	d.containerdRemote = containerdRemote

	go d.execCommandGC()

	d.containerd, err = containerdRemote.Client(d)
	if err != nil {
		return nil, err
	}

	/*
		根据/var/lib/docker/containers目录里容器目录还原部分容器、初始化容器依赖的网络环境，初始化容器之间的link关系等
	*/
	if err := d.restore(); err != nil {
		return nil, err
	}

	// FIXME: this method never returns an error
	info, _ := d.SystemInfo()

	engineVersion.WithValues(
		dockerversion.Version,
		dockerversion.GitCommit,
		info.Architecture,
		info.Driver,
		info.KernelVersion,
		info.OperatingSystem,
	).Set(1)
	engineCpus.Set(float64(info.NCPU))
	engineMemory.Set(float64(info.MemTotal))

	// set up SIGUSR1 handler on Unix-like systems, or a Win32 global event
	// on Windows to dump Go routine stacks
	stackDumpDir := config.Root
	if execRoot := config.GetExecRoot(); execRoot != "" {
		stackDumpDir = execRoot
	}
	d.setupDumpStackTrap(stackDumpDir)

	return d, nil
}
```