# layerStore 初始化

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [type layerStore struct](#type-layerstore-struct)
    - [type MetadataStore interface](#type-metadatastore-interface)
    - [type Driver interface](#type-driver-interface)
  - [func NewStoreFromOptions](#func-newstorefromoptions)
    - [type DaemonCli struct](#type-daemoncli-struct)
	- [创建一个graphdriver](#创建一个graphdriver)
	- [func NewStoreFromGraphDriver](#func-newstorefromgraphdriver)

<!-- END MUNGE: GENERATED_TOC -->

承接前面daemon的创建过程，本文主要对type layerStore struct的初始化过程进行解析，见/layer/layer_store.go

## type layerStore struct
type layerStore struct记录一个hots主机上面存储着的所有的layer层信息，包括只读layer和读写layer。 
其工作目录是/var/lib/docker/image/${graphDriverName}/layerdb

* layerMap map[ChainID]*roLayer  以一层layer的ChainID为key
* mounts map[string]*mountedLayer 以一个容器id为key
```go
type layerStore struct {
	store  MetadataStore
	driver graphdriver.Driver //文件系统的驱动

	layerMap map[ChainID]*roLayer //存放镜像的只读layer信息，/var/lib/docker/image/overlay/layerdb/sha256/
	layerL   sync.Mutex

	mounts map[string]*mountedLayer //存放可读写层信息，/var/lib/docker/image/overlay/layerdb/mounts/
	mountL sync.Mutex
}
```

### ChainID和DiffID
```go
// ChainID is the content-addressable ID of a layer.
/*
	ChainID 是一个用来寻找一个layer层内容的ID编号
*/
type ChainID digest.Digest

// DiffID is the hash of an individual layer tar.
/*
	DiffID是一个layer.tar的hash，计算方法 sha256sum ./layer.tar
*/
type DiffID digest.Digest
```

### type MetadataStore interface
MetadataStore定义操作layer及其元数据的接口
```go
// MetadataStore represents a backend for persisting
// metadata about layers and providing the metadata
// for restoring a Store.
type MetadataStore interface {
	// StartTransaction starts an update for new metadata
	// which will be used to represent an ID on commit.
	StartTransaction() (MetadataTransaction, error)

	GetSize(ChainID) (int64, error)
	GetParent(ChainID) (ChainID, error)
	GetDiffID(ChainID) (DiffID, error)
	GetCacheID(ChainID) (string, error)
	GetDescriptor(ChainID) (distribution.Descriptor, error)
	TarSplitReader(ChainID) (io.ReadCloser, error)

	SetMountID(string, string) error
	SetInitID(string, string) error
	SetMountParent(string, ChainID) error

	GetMountID(string) (string, error)
	GetInitID(string) (string, error)
	GetMountParent(string) (ChainID, error)

	// List returns the full list of referenced
	// read-only and read-write layers
	/*
		返回所有的只读layer和读写layer
	*/
	List() ([]ChainID, []string, error)

	Remove(ChainID) error
	RemoveMount(string) error
}
```

### type Driver interface
一个type Driver interface需要实现一下接口
```go
// Driver is the interface for layered/snapshot file system drivers.
type Driver interface {
	ProtoDriver
	DiffDriver
}

type ProtoDriver interface {
	// String returns a string representation of this driver.
	String() string
	// CreateReadWrite creates a new, empty filesystem layer that is ready
	// to be used as the storage for a container. Additional options can
	// be passed in opts. parent may be "" and opts may be nil.
	CreateReadWrite(id, parent string, opts *CreateOpts) error
	// Create creates a new, empty, filesystem layer with the
	// specified id and parent and options passed in opts. Parent
	// may be "" and opts may be nil.
	Create(id, parent string, opts *CreateOpts) error
	// Remove attempts to remove the filesystem layer with this id.
	Remove(id string) error
	// Get returns the mountpoint for the layered filesystem referred
	// to by this id. You can optionally specify a mountLabel or "".
	// Returns the absolute path to the mounted layered filesystem.
	Get(id, mountLabel string) (dir string, err error)
	// Put releases the system resources for the specified id,
	// e.g, unmounting layered filesystem.
	Put(id string) error
	// Exists returns whether a filesystem layer with the specified
	// ID exists on this driver.
	Exists(id string) bool
	// Status returns a set of key-value pairs which give low
	// level diagnostic status about this driver.
	Status() [][2]string
	// Returns a set of key-value pairs which give low level information
	// about the image/container driver is managing.
	GetMetadata(id string) (map[string]string, error)
	// Cleanup performs necessary tasks to release resources
	// held by the driver, e.g., unmounting all layered filesystems
	// known to this driver.
	Cleanup() error
}

// DiffDriver is the interface to use to implement graph diffs
type DiffDriver interface {
	// Diff produces an archive of the changes between the specified
	// layer and its parent layer which may be "".
	Diff(id, parent string) (io.ReadCloser, error)
	// Changes produces a list of changes between the specified layer
	// and its parent layer. If parent is "", then all changes will be ADD changes.
	Changes(id, parent string) ([]archive.Change, error)
	// ApplyDiff extracts the changeset from the given diff into the
	// layer with the specified id and parent, returning the size of the
	// new layer in bytes.
	// The archive.Reader must be an uncompressed stream.
	ApplyDiff(id, parent string, diff io.Reader) (size int64, err error)
	// DiffSize calculates the changes between the specified id
	// and its parent and returns the size in bytes of the changes
	// relative to its base filesystem directory.
	DiffSize(id, parent string) (size int64, err error)
}
```

## func NewStoreFromOptions
创建daemon的过程中，正是调用此方法。其流程如下：

1. 新建一个Driver
2. 新建一个MetadataStore fms，这个比较简单，就是建立文件夹 /var/lib/docker/image/overlay/layerdb，同时提供操作接口。
3. 基于type fileMetadataStore struct和graph driver构建一个type layerStore struct对象
```go
// NewStoreFromOptions creates a new Store instance
func NewStoreFromOptions(options StoreOptions) (Store, error) {
	/*
		==>/daemon/graphdriver/driver.go
			==>func New
	*/
	driver, err := graphdriver.New(options.GraphDriver, options.PluginGetter, graphdriver.Options{
		Root:                options.StorePath,
		DriverOptions:       options.GraphDriverOptions,
		UIDMaps:             options.UIDMaps,
		GIDMaps:             options.GIDMaps,
		ExperimentalEnabled: options.ExperimentalEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("error initializing graphdriver: %v", err)
	}
	logrus.Debugf("Using graph driver %s", driver)

	/*
		建立文件夹 /var/lib/docker/image/overlay/layerdb，同时提供操作接口（实现type MetadataStore interface）
	*/
	fms, err := NewFSMetadataStore(fmt.Sprintf(options.MetadataStorePathTemplate, driver))
	if err != nil {
		return nil, err
	}

	/*
		基于type fileMetadataStore struct和graph driver构建一个type layerStore struct对象
	*/
	return NewStoreFromGraphDriver(fms, driver)
}
```

### 创建一个graphdriver
根据优先级priority来得到文件系统驱动，然后调用已经注册好的文件系统驱动初始化函数initFunc来初始化驱动
```go
// New creates the driver and initializes it at the specified root.
func New(name string, pg plugingetter.PluginGetter, config Options) (Driver, error) {
	if name != "" {
		/*用户自行自定了graphdriver*/
		logrus.Debugf("[graphdriver] trying provided driver: %s", name) // so the logs show specified driver
		return GetDriver(name, pg, config)
	}

	/*
		获取文件系统驱动，首先根据优先级priority来得到文件系统驱动，
		然后调用已经注册好的文件系统驱动初始化函数initFunc来初始化驱动。
		driver和initFunc映射关系的构建是通过各个驱动初始化的时候，自行调用func Register()来注册
	*/
	// Guess for prior driver
	/*
		driversMap map[overlay:true]
	*/
	driversMap := scanPriorDrivers(config.Root)
	/*
		priority is:  [aufs btrfs zfs overlay2 overlay devicemapper vfs]
			==>/daemon/graphdriver/driver_linux.go
	*/
	for _, name := range priority {
		// of the state found from prior drivers, check in order of our priority
		// which we would prefer
		driver, err := getBuiltinDriver(name, config.Root, config.DriverOptions, config.UIDMaps, config.GIDMaps)
		...
		...
	}
	...
	...
}
```

各个文件系统会在初始化的时候调用func Register，把自身的func Init()注册到drivers中
```go
// Register registers an InitFunc for the driver.

func Register(name string, initFunc InitFunc) error {
	if _, exists := drivers[name]; exists {
		return fmt.Errorf("Name already registered %s", name)
	}
	drivers[name] = initFunc

	return nil
}
```

以overlay为例子，/daemon/graphdriver/overlay/overlay.go
```go
func init() {
	graphdriver.Register("overlay", Init)
}
```

### func NewStoreFromGraphDriver
目录/var/lib/docker/image/overlay/layerdb下，有着sha256目录、mounts目录
```go
// NewStoreFromGraphDriver creates a new Store instance using the provided
// metadata store and graph driver. The metadata store will be used to restore
// the Store.
func NewStoreFromGraphDriver(store MetadataStore, driver graphdriver.Driver) (Store, error) {
	ls := &layerStore{
		store:    store,
		driver:   driver,
		layerMap: map[ChainID]*roLayer{},
		mounts:   map[string]*mountedLayer{},
	}

	/*
		/layer/filestore.go
			==>func (fms *fileMetadataStore) List() ([]ChainID, []string, error)
		根据fileMetadataStore中的root目录（即/var/lib/docker/image/overlay/layerdb）加载
		其下的sha256目录（ids）和mounts目录（mounts）中的信息
	*/
	ids, mounts, err := store.List()
	if err != nil {
		return nil, err
	}

	/*
		开始遍历sha256目录（ids），所有的只读layer
		/var/lib/docker/image/overlay/layerdb/sha256/1f7b04df09e72e9b94e923567a168b438d195c4c610a335ed7320cc6dea93c3f，
		其中1f7b04df09e72e9b94e923567a168b438d195c4c610a335ed7320cc6dea93c3f是该layer的ChainID
	*/
	for _, id := range ids {
		/*
			根据sha256下的id信息加载镜像的只读layer信息:
			包括diff（所有镜像层diff是根据镜像内容使用sha256算法得到）,size,cacheID,parent，descriptor。
			然后存放到ls.layerMap[ChainID]中去。
		*/
		l, err := ls.loadLayer(id)
		if err != nil {
			logrus.Debugf("Failed to load layer %s: %s", id, err)
			continue
		}
		if l.parent != nil {
			l.parent.referenceCount++
		}
	}
	/*
		同理，
		开始遍历mounts目录（mounts），所有的读写layer
		目录名一般为容器id
		目录下有三份文件，分别是init-id,mounts-id,parent
		分别对应可读写层初始化层id，可读写层id以及父镜像层的ChainID
		通过init-id,mounts-id，能在 /var/lib/docker/overlay/ 下找到对应的目录
		通过parent能在 /var/lib/docker/image/overlay/layerdb/sha256/ 下找到对应目录
	*/
	for _, mount := range mounts {
		if err := ls.loadMount(mount); err != nil {
			logrus.Debugf("Failed to load mount %s: %s", mount, err)
		}
	}

	return ls, nil
}
```

简单看一下loadLayer()和loadMount()
```go
func (ls *layerStore) loadLayer(layer ChainID) (*roLayer, error) {
	cl, ok := ls.layerMap[layer]
	if ok {
		//如果该层layer已经被记录到ls.layerMap中，直接return即可
		return cl, nil
	}

	diff, err := ls.store.GetDiffID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff id for %s: %s", layer, err)
	}

	size, err := ls.store.GetSize(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get size for %s: %s", layer, err)
	}

	cacheID, err := ls.store.GetCacheID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache id for %s: %s", layer, err)
	}

	parent, err := ls.store.GetParent(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent for %s: %s", layer, err)
	}

	descriptor, err := ls.store.GetDescriptor(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get descriptor for %s: %s", layer, err)
	}

	cl = &roLayer{
		chainID:    layer,
		diffID:     diff,
		size:       size,
		cacheID:    cacheID,
		layerStore: ls,
		references: map[Layer]struct{}{},
		descriptor: descriptor,
	}

	if parent != "" {
		p, err := ls.loadLayer(parent)
		if err != nil {
			return nil, err
		}
		cl.parent = p
	}
	/*
		把新的一层layer记录到ls.layerMap[chainID]中
	*/
	ls.layerMap[cl.chainID] = cl

	return cl, nil
}

func (ls *layerStore) loadMount(mount string) error {
	if _, ok := ls.mounts[mount]; ok {
		return nil
	}

	mountID, err := ls.store.GetMountID(mount)
	if err != nil {
		return err
	}

	initID, err := ls.store.GetInitID(mount)
	if err != nil {
		return err
	}

	parent, err := ls.store.GetMountParent(mount)
	if err != nil {
		return err
	}

	ml := &mountedLayer{
		name:       mount,
		mountID:    mountID,
		initID:     initID,
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if parent != "" {
		p, err := ls.loadLayer(parent)
		if err != nil {
			return err
		}
		ml.parent = p

		p.referenceCount++
	}
	/*
		加载一层读写layer到ls.mounts[container_id]
	*/
	ls.mounts[ml.name] = ml

	return nil
}
```