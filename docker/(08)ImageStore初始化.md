# ImageStore 初始化

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引子](#引子)
  - [type fs struct](#type-fs-struct)
    - [创建fs对象](#创建fs对象)
	- [func Walk](#func-walk)
  - [imageStore](#imagestore)
    - [创建imageStore对象](#创建imagestore对象)
	- [func restore](#func-restore)
	- [根据diffID计算一个layer的chainID](#根据diffid计算一个layer的chainid)
  - [type RootFS struct](#type-rootfs-struct)
  - [type Digest string](#type-digest-string)

<!-- END MUNGE: GENERATED_TOC -->

承接前面daemon的创建过程，本文主要对 ImageStore 的初始化过程进行解析。 

ImageStore，根据所有layer来构建image，维护所有image的元数据。

## 引子
1. 创建一个type fs struct对象 ifs
2. 根据StoreBackend ifs和 layerStore来创建一个imageStore
```go
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
```

两个子目录
```go
const (
	contentDirName  = "content"
	metadataDirName = "metadata"
)
```

## type fs struct
type fs struct实现了type StoreBackend interface，提供方法供imageStore调用
```go
// StoreBackend provides interface for image.Store persistence
/*
	StoreBackend为image.Store的持久化提供接口
*/
type StoreBackend interface {
	Walk(f DigestWalkFunc) error
	Get(id digest.Digest) ([]byte, error)
	Set(data []byte) (digest.Digest, error)
	Delete(id digest.Digest) error
	SetMetadata(id digest.Digest, key string, data []byte) error
	GetMetadata(id digest.Digest, key string) ([]byte, error)
	DeleteMetadata(id digest.Digest, key string) error
}

// fs implements StoreBackend using the filesystem.
type fs struct {
	sync.RWMutex
	root string
}
```
主要讲解其中的两个方法

### 创建fs对象
就是创建了3个目录，/var/lib/docker/image/${graphDriverName}/imagedb
```go
// NewFSStoreBackend returns new filesystem based backend for image.Store
func NewFSStoreBackend(root string) (StoreBackend, error) {
	/*
		root: /var/lib/docker/image/${graphDriverName}/imagedb
	*/
	return newFSStore(root)
}

func newFSStore(root string) (*fs, error) {
	s := &fs{
		root: root,
	}
	if err := os.MkdirAll(filepath.Join(root, contentDirName, string(digest.Canonical)), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, metadataDirName, string(digest.Canonical)), 0700); err != nil {
		return nil, err
	}
	return s, nil
}
```

### func Walk
func (s *fs) Walk 会遍历下的目录/var/lib/docker/image/overlay/imagedb/content/sha256/，用入参提供的方法f DigestWalkFunc来对所有的目录ID进行计算。
```go
// Walk calls the supplied callback for each image ID in the storage backend.

func (s *fs) Walk(f DigestWalkFunc) error {
	// Only Canonical digest (sha256) is currently supported
	/*
		目前仅支持sha256，用来计算digest值
		Canonical = SHA256
			==>/vendor/github.com/docker/distribution/digest/digester.go
	*/
	s.RLock()
	/*
		获取到 /var/lib/docker/image/overlay/imagedb/content/sha256/下的所有目录名字
		一个目录名字代表了一个image ID
	*/
	dir, err := ioutil.ReadDir(filepath.Join(s.root, contentDirName, string(digest.Canonical)))
	s.RUnlock()
	if err != nil {
		return err
	}
	for _, v := range dir {
		/*
			就是简单的字符串拼接
			==>/vendor/github.com/docker/distribution/digest/digest.go
				==>func NewDigestFromHex
			dgst：sha256:7173b809ca12ec5dee4506cd86be934c4596dd234ee82c0662eac04a8c2c71dc
		*/
		dgst := digest.NewDigestFromHex(string(digest.Canonical), v.Name())
		if err := dgst.Validate(); err != nil {
			logrus.Debugf("Skipping invalid digest %s: %s", dgst, err)
			continue
		}
		/*
			执行f(dgst)
		*/
		if err := f(dgst); err != nil {
			return err
		}
	}
	return nil
}
```

## imageStore
首先查看相关定义
```go
type imageMeta struct {
	layer    layer.Layer //该镜像的最后一层
	children map[ID]struct{}
}

type store struct {
	sync.Mutex
	ls        LayerGetReleaser
	images    map[ID]*imageMeta //存放镜像ID对应的layer及子镜像
	fs        StoreBackend
	digestSet *digest.Set //存放所有的镜像ID
}
```

### 创建imageStore对象
主要是调用restore()函数来根据/var/lib/docker/image/overlay/imagedb目录下的内容设置ImageStore的属性
```go
// NewImageStore returns new store object for given layer store
func NewImageStore(fs StoreBackend, ls LayerGetReleaser) (Store, error) {
	is := &store{
		ls:        ls,
		images:    make(map[ID]*imageMeta),
		fs:        fs,
		digestSet: digest.NewSet(),
	}

	// load all current images and retain layers
	/*
		根据/var/lib/docker/image/overlay/imagedb目录下的内容设置ImageStore的属性
	*/
	if err := is.restore(); err != nil {
		return nil, err
	}

	return is, nil
}
```

### func restore
```go
func (is *store) restore() error {
	/*
		==>/image/fs.go
			==>func (s *fs) Walk
	*/
	err := is.fs.Walk(func(dgst digest.Digest) error {
		/*
			根据image ID来生成type Image struct对象，记录了一个image的结构信息
			对应的文件是 /var/lib/docker/image/overlay/imagedb/content/sha256/20c44cd7596ff4807aef84273c99588d22749e2a7e15a7545ac96347baa65eda
		*/
		img, err := is.Get(IDFromDigest(dgst))
		if err != nil {
			logrus.Errorf("invalid image %v, %v", dgst, err)
			return nil
		}
		var l layer.Layer
		/*
			根据img.RootFS中记录的diffID计算出该image的最后一个chainID
				==>/image/rootfs.go
					==>func (r *RootFS) ChainID()
		*/
		if chainID := img.RootFS.ChainID(); chainID != "" {
			l, err = is.ls.Get(chainID)
			if err != nil {
				return err
			}
		}
		/*
			在digestSet *digest.Set中记录下该image ID
		*/
		if err := is.digestSet.Add(dgst); err != nil {
			return err
		}

		/*
			image元数据
		*/
		imageMeta := &imageMeta{
			layer:    l,
			children: make(map[ID]struct{}),
		}
		/*
		   image元数据和image ID的映射
		*/
		is.images[IDFromDigest(dgst)] = imageMeta

		return nil
	})
	if err != nil {
		return err
	}

	// Second pass to fill in children maps
	for id := range is.images {
		/*
			设置ImageID中的父子关系
			根据id读取/var/lib/docker/overlay/imagedb/metadata/sha256/{id}下的文件内容
		*/
		if parent, err := is.GetParent(id); err == nil {
			if parentMeta := is.images[parent]; parentMeta != nil {
				parentMeta.children[id] = struct{}{}
			}
		}
	}

	return nil
}
```

### 根据diffID计算一个layer的chainID
见/image/rootfs.go
```go
// ChainID returns the ChainID for the top layer in RootFS.
/*
	返回在RootFS中最顶层的那一个ChainID
*/
func (r *RootFS) ChainID() layer.ChainID {
	if runtime.GOOS == "windows" && r.Type == typeLayersWithBase {
		logrus.Warnf("Layer type is unsupported on this platform. DiffIDs: '%v'", r.DiffIDs)
		return ""
	}
	/*
		==>/layer/layer.go
			==>func CreateChainID
	*/
	return layer.CreateChainID(r.DiffIDs)
}

// CreateChainID returns ID for a layerDigest slice
/*
	计算一个镜像所有的 ChainID
*/
func CreateChainID(dgsts []DiffID) ChainID {
	return createChainIDFromParent("", dgsts...)
}

/*
	递归进行计算
*/
func createChainIDFromParent(parent ChainID, dgsts ...DiffID) ChainID {
	if len(dgsts) == 0 {
		return parent
	}
	if parent == "" {
		return createChainIDFromParent(ChainID(dgsts[0]), dgsts[1:]...)
	}
	// H = "H(n-1) SHA256(n)"
	dgst := digest.FromBytes([]byte(string(parent) + " " + string(dgsts[0])))
	return createChainIDFromParent(ChainID(dgst), dgsts[1:]...)
}
```

## type RootFS struct
type RootFS struct记录了一个镜像所有layer的diffID值，可以通过查看/var/lib/docker/image/overlay/imagedb/content/sha256/{id}文件来查看一个镜像的rootfs属性
```go
// RootFS describes images root filesystem
// This is currently a placeholder that only supports layers. In the future
// this can be made into an interface that supports different implementations.
/*
	type RootFS struct记录了一个镜像所有layer的diffID值
	其中"type": "layers"
	cat /var/lib/docker/image/overlay/imagedb/content/sha256/99e59f495ffaa222bfeb67580213e8c28c1e885f1d245ab2bbe3b1b1ec3bd0b2  |python -mjson.tool
	可以看到该image的rootfs属性
*/
type RootFS struct {
	Type    string         `json:"type"`
	DiffIDs []layer.DiffID `json:"diff_ids,omitempty"`
}

// NewRootFS returns empty RootFS struct
func NewRootFS() *RootFS {
	return &RootFS{Type: TypeLayers}
}

// Append appends a new diffID to rootfs
func (r *RootFS) Append(id layer.DiffID) {
	r.DiffIDs = append(r.DiffIDs, id)
}
```

## type Digest string
Digest：｛hash算法：hash值｝，可以简单地认为是该段内容的一个属性

关于Digest，可以查看https://docs.docker.com/registry/spec/api/#content-digests

```go
// Digest allows simple protection of hex formatted digest strings, prefixed
// by their algorithm. Strings of type Digest have some guarantee of being in
// the correct format and it provides quick access to the components of a
// digest string.
//
// The following is an example of the contents of Digest types:
//
// 	sha256:7173b809ca12ec5dee4506cd86be934c4596dd234ee82c0662eac04a8c2c71dc
//
// This allows to abstract the digest behind this type and work only in those
// terms.
type Digest string

// NewDigestFromHex returns a Digest from alg and a the hex encoded digest.
/*
	Digest：｛hash算法：hash值｝，可以简单地认为是该段内容的一个属性
	关于Digest，可以查看https://docs.docker.com/registry/spec/api/#content-digests
*/
func NewDigestFromHex(alg, hex string) Digest {
	return Digest(fmt.Sprintf("%s:%s", alg, hex))
}
```
其中定义在//image/image.go中ID，就是Digest后面的id，两者可以互相转化
```go
// ID is the content-addressable ID of an image.
/*
	image的ID。
	可以和digest.Digest进行互相转化
	Digest：｛hash算法：hash值｝，可以简单地认为是该段内容的一个属性
	关于Digest，可以查看https://docs.docker.com/registry/spec/api/#content-digests
		==>/vendor/github.com/docker/distribution/digest/digest.go
*/
type ID digest.Digest

// Digest converts ID into a digest
func (id ID) Digest() digest.Digest {
	return digest.Digest(id)
}

// IDFromDigest creates an ID from a digest
func IDFromDigest(digest digest.Digest) ID {
	return ID(digest)
}
```




