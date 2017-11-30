# VolumeStore初始化

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [创建一个VolumeStore对象](#创建一个volumestore对象)
  - [volume驱动](#volume驱动)
    - [type Root struct](#type-root-struct)
	- [type localVolume struct](#type-localvolume-struct)
  - [驱动向volumedrivers注册](#驱动向volumedrivers注册)
  - [type VolumeStore struct](#type-volumestore-struct)
    - [VolumeStore定义声明](#volumestore定义声明)
	- [func restore](#func-restore)
<!-- END MUNGE: GENERATED_TOC -->

type VolumeStore struct：记录着所有的volumes，同时跟踪它们的使用情况，相当于volume的一个cache
- 其工作目录/var/lib/docker/volumes
- /var/lib/docker/volumes/metadata.db，记录volume的元数据，是一个文件型的数据库

## 创建一个VolumeStore对象
```go
	// Configure the volumes driver
	/*
		VolumeStore：记录着所有的volumes，同时跟踪它们的使用情况，相当于volume的一个cache
		创建/var/lib/docker/volumes/metadata.db，记录volume的元数据
		Volumes 是一种特殊的目录，其数据可以被一个或多个 container 共享,
		它和创建它的 container 的生命周期分离开来，
		在 container 被删去之后能继续存在。
	*/
	volStore, err := d.configureVolumes(rootUID, rootGID)
	
	func (daemon *Daemon) configureVolumes(rootUID, rootGID int) (*store.VolumeStore, error) {
	/*
		创建一个volume驱动
	*/
	volumesDriver, err := local.New(daemon.configStore.Root, rootUID, rootGID)
	if err != nil {
		return nil, err
	}

	volumedrivers.RegisterPluginGetter(daemon.PluginStore)

	/*
		向volumedrivers注册
			==>/volume/drivers/extpoint.go
				==>func Register
	*/
	if !volumedrivers.Register(volumesDriver, volumesDriver.Name()) {
		return nil, fmt.Errorf("local volume driver could not be registered")
	}
	/*
		创建一个type VolumeStore struct对象
		==>/volume/store/store.go
			==>func New
	*/
	return store.New(daemon.configStore.Root)
}
```
分析func configureVolumes()，其流程如下：
1. 创建一个volume驱动，volumesDriver
2. 把驱动向volumedrivers注册
3. 创建一个type VolumeStore struct对象，VolumeStore

## volume驱动
核心就是两个概念：
* type Root struct 是volume的驱动
* type localVolume struct 则是一个volume

见/volume/local/local.go
```go
// New instantiates a new Root instance with the provided scope. Scope
// is the base path that the Root instance uses to store its
// volumes. The base path is created here if it does not exist.
/*
	基于入参scope，创建一个type Root struct 对象。
	scope是type Root struct 对象的base path，用于存储volumes。
*/
func New(scope string, rootUID, rootGID int) (*Root, error) {
	/*
		scope: /var/lib/docker
		数据盘的工作目录 /var/lib/docker/volumes
	*/
	rootDirectory := filepath.Join(scope, volumesPathName)

	if err := idtools.MkdirAllAs(rootDirectory, 0700, rootUID, rootGID); err != nil {
		return nil, err
	}

	/*
		两个核心概念
		type Root struct 是volume的驱动
		type localVolume struct 则是一个volume
	*/
	r := &Root{
		scope:   scope,
		path:    rootDirectory,
		volumes: make(map[string]*localVolume),
		rootUID: rootUID,
		rootGID: rootGID,
	}

	/*
		ioutil.ReadDir(dirmane) 读取目录 dirmane 中的所有目录和文件（不包括子目录）
		返回读取到的文件的信息列表和读取过程中遇到的任何错误
		返回的文件列表是经过排序的
	*/
	dirs, err := ioutil.ReadDir(rootDirectory)
	if err != nil {
		return nil, err
	}

	/*
		解析系统的/proc/self/mountinfo文件，获取docker daemon所有的挂载信息
	*/
	mountInfos, err := mount.GetMounts()
	if err != nil {
		logrus.Debugf("error looking up mounts for local volume cleanup: %v", err)
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}

		/*
			Base(path) returns the last element of path
			d.Name()的值：f1c39a84b42a935853c2099072998356ba7f71140a0d6e2a9e1e1cc8ba488a32
						  metadata.db
		*/
		name := filepath.Base(d.Name())
		//创建了一个localVolume对象
		v := &localVolume{
			driverName: r.Name(),
			name:       name,
			path:       r.DataPath(name),
		}
		//记录数据卷name和数据卷对象的关系
		r.volumes[name] = v
		/*
			数据卷配置信息opt.json的路径
			optsFilePath：/var/lib/docker/volumes/{volumeId}/opts.json
			读取数据卷配置文件信息
		*/
		optsFilePath := filepath.Join(rootDirectory, name, "opts.json")
		if b, err := ioutil.ReadFile(optsFilePath); err == nil {
			opts := optsConfig{}
			/*
				解码，把opts.json中信息存放到optsConfig{}结构体中
					==>/volume/local/local_unix.go
						==>type optsConfig struct
			*/
			if err := json.Unmarshal(b, &opts); err != nil {
				return nil, errors.Wrapf(err, "error while unmarshaling volume options for volume: %s", name)
			}
			// Make sure this isn't an empty optsConfig.
			// This could be empty due to buggy behavior in older versions of Docker.
			/*
				确保optsConfig非nil
			*/
			if !reflect.DeepEqual(opts, optsConfig{}) {
				v.opts = &opts
			}

			// unmount anything that may still be mounted (for example, from an unclean shutdown)
			/*
				这是在初始化的时候unmount一切挂载点
			*/
			for _, info := range mountInfos {
				if info.Mountpoint == v.path {
					mount.Unmount(v.path)
					break
				}
			}
		}
	}

	return r, nil
}
```

### type Root struct
type Root struct实现了the Driver interface，负责管理volumes的创建/删除。 
仅支持标准的vfs命令，在其scope范围内进行目录的创建/删除。 
实现了type Driver interface
```go
// Root implements the Driver interface for the volume package and
// manages the creation/removal of volumes. It uses only standard vfs
// commands to create/remove dirs within its provided scope.
/*
	可以把type Root struct认为是volume的Driver。
		==>/volume/volume.go
			==>type Driver interface
*/
type Root struct {
	m       sync.Mutex
	scope   string
	path    string
	volumes map[string]*localVolume //记录数据卷name和数据卷对象的映射关系
	rootUID int
	rootGID int
}

// DataPath returns the constructed path of this volume.
/*
	volume下的 _data 目录
*/
func (r *Root) DataPath(volumeName string) string {
	return filepath.Join(r.path, volumeName, VolumeDataPathName)
}

// Name returns the name of Root, defined in the volume package in the DefaultDriverName constant.
/*
	返回驱动的name
	const DefaultDriverName = "local"
		==>/volume/volume.go
*/
func (r *Root) Name() string {
	return volume.DefaultDriverName
}

// VolumeDataPathName is the name of the directory where the volume data is stored.
// It uses a very distinctive name to avoid collisions migrating data between
// Docker versions.
/*
	VolumeDataPathName是存储卷数据的目录。
	它使用一个非常独特的名字来避免在不同Docker版本之间迁移产生的数据冲突。
*/
const (
	VolumeDataPathName = "_data"
	volumesPathName    = "volumes"
)
```

### type localVolume struct
localVolume代表了一个由volume驱动type Root struct创建的volume，实现了type Volume interface
```go
// localVolume implements the Volume interface from the volume package and
// represents the volumes created by Root.
/*
	type localVolume struct实现了Volume interface，
		==>/volume/volume.go
			==>type Volume interface
*/
type localVolume struct {
	m sync.Mutex
	// unique name of the volume
	name string
	// path is the path on the host where the data lives
	path string
	// driverName is the name of the driver that created the volume.
	driverName string
	// opts is the parsed list of options used to create the volume
	opts *optsConfig
	// active refcounts the active mounts
	active activeMount
}
```

## 驱动向volumedrivers注册
其实就是建立一个映射关系
```go
// Register associates the given driver to the given name, checking if
// the name is already associated
func Register(extension volume.Driver, name string) bool {
	if name == "" {
		return false
	}

	drivers.Lock()
	defer drivers.Unlock()

	_, exists := drivers.extensions[name]
	if exists {
		return false
	}

	if err := validateDriver(extension); err != nil {
		return false
	}
	/*
		建立一个映射关系
	*/
	drivers.extensions[name] = extension

	return true
}
```

## type VolumeStore struct
type VolumeStore struct：记录着所有的volumes，同时跟踪它们的使用情况，相当于volume的一个cache

这里涉及到一个文件操作类型的数据库，github.com/boltdb/bolt，其具体操作可以参考 https://segmentfault.com/a/1190000010098668

就是用bolt数据库来对metadata.db文件进行操作，`/var/lib/docker/volumes/metadata.db`是volume元数据的信息文件
```go
// New initializes a VolumeStore to keep
// reference counting of volumes in the system.
/*
	VolumeStore用于保持系统中volume的参考计数
*/
func New(rootPath string) (*VolumeStore, error) {
	vs := &VolumeStore{
		locks:   &locker.Locker{},
		names:   make(map[string]volume.Volume),
		refs:    make(map[string][]string),
		labels:  make(map[string]map[string]string),
		options: make(map[string]map[string]string),
	}

	if rootPath != "" {
		// initialize metadata store
		volPath := filepath.Join(rootPath, volumeDataDir)
		if err := os.MkdirAll(volPath, 750); err != nil {
			return nil, err
		}
		/*
			volPath 的值是 /var/lib/docker/volumes/
			metadata.db是volume元数据的信息文件
			使用github.com/boltdb/bolt，文件操作类型的数据库，一种k/v数据库，只能单点写入和读取，
			如果多个同时操作的话后者会被挂起直到前者关闭操作为止， boltdb一次只允许一个读写事务，但一次允许多个只读事务

			具体操作可以参考 https://segmentfault.com/a/1190000010098668
		*/
		dbPath := filepath.Join(volPath, "metadata.db")

		var err error
		/*
			Open，创建和启动数据库
		*/
		vs.db, err = bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
		if err != nil {
			return nil, errors.Wrap(err, "error while opening volume store metadata database")
		}

		// initialize volumes bucket
		/*
						Update，读写事务操作
						err := db.Update(func(tx *bolt.Tx) error {
			    			...
			    			return nil
						})
						func是个闭包
						如果err==nil，则执行commit提交
						如果err!=nil，则回滚
		*/
		if err := vs.db.Update(func(tx *bolt.Tx) error {
			//创建一个名为volumes的bucket对象
			if _, err := tx.CreateBucketIfNotExists(volumeBucketName); err != nil {
				return errors.Wrap(err, "error while setting up volume store metadata database")
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	/*
		把metadata.db中的元数据信息填充到VolumeStore vs中
	*/
	vs.restore()

	return vs, nil
}
```

### VolumeStore定义声明
```go
// VolumeStore is a struct that stores the list of volumes available and keeps track of their usage counts
/*
	type VolumeStore struct记录着所有的volumes，同时跟踪它们的使用情况
*/
type VolumeStore struct {
	// locks ensures that only one action is being performed on a particular volume at a time without locking the entire store
	// since actions on volumes can be quite slow, this ensures the store is free to handle requests for other volumes.
	/*
		locks 确保一次只在一个特定卷上执行一个动作，而不会锁定整个存储区，因为卷上的动作可能相当缓慢，这确保了存储区可以自由处理其他卷的请求。
	*/
	locks *locker.Locker
	// globalLock is used to protect access to mutable structures used by the store object
	globalLock sync.RWMutex
	// names stores the volume name -> volume relationship.
	// This is used for making lookups faster so we don't have to probe all drivers
	/*
		volume name和volume本身的映射关系
		names 用于快速查找，避免探测所有驱动程序
	*/
	names map[string]volume.Volume
	// refs stores the volume name and the list of things referencing it
	/*
		记录volume name和`引用了该volume的对象`的映射关系
	*/
	refs map[string][]string
	// labels stores volume labels for each volume
	/*记录每一个volume的label*/
	labels map[string]map[string]string
	// options stores volume options for each volume
	/*记录了每一个volume的属性*/
	options map[string]map[string]string
	db      *bolt.DB
}
```

### func restore
func restore()负责把`/var/lib/docker/volumes/metadata.db`中的元数据信息填充到VolumeStore vs中
```go
// restore is called when a new volume store is created.
// It's primary purpose is to ensure that all drivers' refcounts are set based
// on known volumes after a restart.
// This only attempts to track volumes that are actually stored in the on-disk db.
// It does not probe the available drivers to find anything that may have been added
// out of band.
func (s *VolumeStore) restore() {
	var ls []volumeMetadata
	/*
		db.View只读模式，
		读取metadata中的元数据（key,value）
	*/
	s.db.View(func(tx *bolt.Tx) error {
		ls = listMeta(tx)
		return nil
	})

	chRemove := make(chan *volumeMetadata, len(ls))
	var wg sync.WaitGroup
	/*
		遍历每一个元数据k/v
	*/
	for _, meta := range ls {
		wg.Add(1)
		// this is potentially a very slow operation, so do it in a goroutine
		go func(meta volumeMetadata) {
			defer wg.Done()

			var v volume.Volume
			var err error
			if meta.Driver != "" {
				/*
					从指定的驱动中获取对应volume名称的Volume
					前面所有的volume都已经向volumedrivers进行了注册
				*/
				v, err = lookupVolume(meta.Driver, meta.Name)
				if err != nil && err != errNoSuchVolume {
					logrus.WithError(err).WithField("driver", meta.Driver).WithField("volume", meta.Name).Warn("Error restoring volume")
					return
				}
				if v == nil {
					// doesn't exist in the driver, remove it from the db
					/*
						该drive中找到不该volume，准备删除该volume
					*/
					chRemove <- &meta
					return
				}
			} else {
				/*
					如果没有指定驱动则从VolumeStore中获取Volume
					会探测所有driver，直到找到具有该name的第一个volume
				*/
				v, err = s.getVolume(meta.Name)
				if err != nil {
					if err == errNoSuchVolume {
						chRemove <- &meta
					}
					return
				}
				/*
					更新元数据内容meta到数据库
				*/
				meta.Driver = v.DriverName()
				if err := s.setMeta(v.Name(), meta); err != nil {
					logrus.WithError(err).WithField("driver", meta.Driver).WithField("volume", v.Name()).Warn("Error updating volume metadata on restore")
				}
			}

			// increment driver refcount
			volumedrivers.CreateDriver(meta.Driver)

			// cache the volume
			/*
				根据元数据meta中的Labels及Options更新VolumeStore对象中的数据
				VolumeStore 相当于把所有的volume信息的一个cache
			*/
			s.globalLock.Lock()
			s.options[v.Name()] = meta.Options
			s.labels[v.Name()] = meta.Labels
			s.names[v.Name()] = v
			s.globalLock.Unlock()
		}(meta)
	}

	wg.Wait()
	close(chRemove)
	/*
		删除找不到的volume
	*/
	s.db.Update(func(tx *bolt.Tx) error {
		for meta := range chRemove {
			if err := removeMeta(tx, meta.Name); err != nil {
				logrus.WithField("volume", meta.Name).Warnf("Error removing stale entry from volume db: %v", err)
			}
		}
		return nil
	})
}
```