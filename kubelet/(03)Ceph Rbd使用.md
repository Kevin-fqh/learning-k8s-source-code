# kubelet对rbd的使用

## rbd基本命令
1. rbd的创建过程一般如下所示
```shell
# ceph osd pool create rbdpool 64
# rbd crearte rbdpool/rbdimage  --size 10G  --image-feature layering
# rbd map rbdpool/rbdimage
# mkfs.ext4 /dev/rbd0
# mount
```
在v1.5.2版本中，pool和rbd image对于kubelet来说，是已经创建好了的。 
也就是说kubelet要完成`map、mkfs和mount`动作。

2. rbd的删除过程一般如下所示
```shell
# unmount /dev/rbd0
# rbd unmap /dev/rbd0
# rbd rm rbdimage –p rbdpool
```

3. 如果要删除一个pool
```shell
ceph osd pool delete {pool-name} {pool-name} --yes-i-really-really-mean-it
```

## 挂载过程
在storageClass+pvc的模型中，创建pvc的时候并不会进行mount动作。

把"ceph rbd image /dev/rbd0" 挂载到 "主机节点的目录globalPDPath"会出现下述现象：
1. 同一台物理机上，把RBD进行只读挂载后，再进行写挂载，由于会回改RBD挂载点的权限，所以RBD挂载点的权限会从ro变成rw。 
2. 同样的，在同一台物理机上，先把RBD进行写挂载，再进行读挂载，RBD挂载点的权限会从rw变成ro，从而写挂载不能写入数据。
3. 如果只读挂载和写挂载不在同一台物理主机上，则问题不存在。

```go
// utility to mount a disk based filesystem
func diskSetUp(manager diskManager, b rbdMounter, volPath string, mounter mount.Interface, fsGroup *int64) error {
	globalPDPath := manager.MakeGlobalPDName(*b.rbd)
	// TODO: handle failed mounts here.
	notMnt, err := mounter.IsLikelyNotMountPoint(volPath)

	if err != nil && !os.IsNotExist(err) {
		glog.Errorf("cannot validate mountpoint: %s", volPath)
		return err
	}
	if !notMnt {
		return nil
	}
	/*
		AttachDisk()把"ceph rbd image /dev/rbd0" 挂载到 "主机节点的目录globalPDPath"
		==>/pkg/volume/rbd/rbd_util.go
			==>func (util *RBDUtil) AttachDisk(b rbdMounter) error

		globalPDPath是主机节点上代表了persistentvolume的目录，形如  "rbd"+{pool}+"-image-"+{image}

		在storageClass+pvc的模型中，这里的读写权限由 spec.volumes.persistentVolumeClaim.readOnly 控制
		当spec.volumes.persistentVolumeClaim.readOnly为false时，
		K8s在挂载RBD的过程中会给该RBD image加入锁，所以集群中写方式的RBD image只能挂载一次
	*/
	if err := manager.AttachDisk(b); err != nil {
		glog.Errorf("failed to attach disk")
		return err
	}

	/*
		这里是把pv和容器中的目录进行了挂载，对应docker的操作是 docker -v 指定读写模式。

		把主机host目录globalPDPath 和 volume的挂载点volPath(pv) 进行mount操作
	*/
	if err := os.MkdirAll(volPath, 0750); err != nil {
		glog.Errorf("failed to mkdir:%s", volPath)
		return err
	}
	// Perform a bind mount to the full path to allow duplicate mounts of the same disk.
	options := []string{"bind"}
	if (&b).GetAttributes().ReadOnly {
		/*
			如果设置了只读模式，以read-only模式进行挂载
			在storageClass+pvc的模型中，
			读写权限由 spec.containers.volumeMounts.readOnly中的readOnly控制
		*/
		options = append(options, "ro")
	}
	/*
		==>/pkg/util/mount/mount_linux.go
			==>func (mounter *Mounter) Mount
		把pv和容器中的目录进行了挂载
	*/
	err = mounter.Mount(globalPDPath, volPath, "", options)
	if err != nil {
		glog.Errorf("failed to bind mount:%s", globalPDPath)
		return err
	}

	if !b.ReadOnly {
		volume.SetVolumeOwnership(&b, fsGroup)
	}

	return nil
}
```

### AttachDisk()
AttachDisk()把"ceph rbd image /dev/rbd0" 挂载到 "主机节点的目录globalPDPath"。

在storageClass+pvc的模型中，这里的读写权限由 spec.volumes.persistentVolumeClaim.readOnly 控制。
```go
func (util *RBDUtil) AttachDisk(b rbdMounter) error {
	/*
		完成rbd image map、格式化和mount三个动作
	*/
	var err error
	var output []byte

	devicePath, found := waitForPath(b.Pool, b.Image, 1)
	if !found {
		// modprobe
		_, err = b.plugin.execCommand("modprobe", []string{"rbd"})
		if err != nil {
			return fmt.Errorf("rbd: failed to modprobe rbd error:%v", err)
		}
		// rbd map
		l := len(b.Mon)
		// avoid mount storm, pick a host randomly
		start := rand.Int() % l
		// iterate all hosts until mount succeeds.
		for i := start; i < start+l; i++ {
			mon := b.Mon[i%l]
			glog.V(1).Infof("rbd: map mon %s", mon)
			if b.Secret != "" {
				output, err = b.plugin.execCommand("rbd",
					[]string{"map", b.Image, "--pool", b.Pool, "--id", b.Id, "-m", mon, "--key=" + b.Secret})
			} else {
				output, err = b.plugin.execCommand("rbd",
					[]string{"map", b.Image, "--pool", b.Pool, "--id", b.Id, "-m", mon, "-k", b.Keyring})
			}
			if err == nil {
				break
			}
			glog.V(1).Infof("rbd: map error %v %s", err, string(output))
		}
		if err != nil {
			return fmt.Errorf("rbd: map failed %v %s", err, string(output))
		}
		devicePath, found = waitForPath(b.Pool, b.Image, 10)
		if !found {
			return errors.New("Could not map image: Timeout after 10s")
		}
	}
	// mount it
	globalPDPath := b.manager.MakeGlobalPDName(*b.rbd)
	notMnt, err := b.mounter.IsLikelyNotMountPoint(globalPDPath)
	// in the first time, the path shouldn't exist and IsLikelyNotMountPoint is expected to get NotExist
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rbd: %s failed to check mountpoint", globalPDPath)
	}
	if !notMnt {
		return nil
	}

	/*
		globalPDPath是主机节点上代表了persistentvolume的目录，形如  "rbd"+{pool}+"-image-"+{image}
		devicePath则是形如 /dev/rbd0
	*/
	if err := os.MkdirAll(globalPDPath, 0750); err != nil {
		return fmt.Errorf("rbd: failed to mkdir %s, error", globalPDPath)
	}

	// fence off other mappers
	if err := util.fencing(b); err != nil {
		// rbd unmap before exit
		b.plugin.execCommand("rbd", []string{"unmap", devicePath})
		return fmt.Errorf("rbd: image %s is locked by other nodes", b.Image)
	}
	// rbd lock remove needs ceph and image config
	// but kubelet doesn't get them from apiserver during teardown
	// so persit rbd config so upon disk detach, rbd lock can be removed
	// since rbd json is persisted in the same local directory that is used as rbd mountpoint later,
	// the json file remains invisible during rbd mount and thus won't be removed accidentally.
	util.persistRBD(b, globalPDPath)

	/*
		格式化和mount
		==>/pkg/util/mount/mount.go
			==>func (mounter *SafeFormatAndMount) FormatAndMount
	*/
	if err = b.mounter.FormatAndMount(devicePath, globalPDPath, b.fsType, nil); err != nil {
		err = fmt.Errorf("rbd: failed to mount rbd volume %s [%s] to %s, error %v", devicePath, b.fsType, globalPDPath, err)
	}
	return err
}
```


