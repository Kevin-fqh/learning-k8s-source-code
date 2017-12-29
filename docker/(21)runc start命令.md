# runc start命令

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [startCommand](#startcommand)
  - [获取指定container](#获取指定container)
  - [container.Exec()](#containerexec)

<!-- END MUNGE: GENERATED_TOC -->

`runc start`只能启动状态处于`created`的容器。
## startCommand
1. 从context中获取id，再获取指定的容器
2. 启动处于Created状态的容器

见/runc-1.0.0-rc2/start.go
```go
var startCommand = cli.Command{
	Name:  "start",
	Usage: "executes the user defined process in a created container",
	ArgsUsage: `<container-id>

Where "<container-id>" is your name for the instance of the container that you
are starting. The name you provide for the container instance must be unique on
your host.`,
	Description: `The start command executes the user defined process in a created container.`,
	Action: func(context *cli.Context) error {
		/*
			获取指定的容器
			==>/utils_linux.go
				==>func getContainer(context *cli.Context) (libcontainer.Container, error)
		*/
		container, err := getContainer(context)
		if err != nil {
			return err
		}
		status, err := container.Status()
		if err != nil {
			return err
		}
		switch status {
		/*
			仅仅会启动处于Created状态的容器
		*/
		case libcontainer.Created:
			/*
				==>/libcontainer/container_linux.go
					==>func (c *linuxContainer) Exec()
			*/
			return container.Exec()
		case libcontainer.Stopped:
			return fmt.Errorf("cannot start a container that has run and stopped")
		case libcontainer.Running:
			return fmt.Errorf("cannot start an already running container")
		default:
			return fmt.Errorf("cannot start a container in the %s state", status)
		}
	},
}
```

## 获取指定container
```go
// getContainer returns the specified container instance by loading it from state
// with the default factory.
/*
	从容器文件夹中载入该容器的配置，生成libcontainer.Container对象
*/
func getContainer(context *cli.Context) (libcontainer.Container, error) {
	id := context.Args().First()
	if id == "" {
		return nil, errEmptyID
	}
	factory, err := loadFactory(context)
	if err != nil {
		return nil, err
	}
	return factory.Load(id)
}
```

## container.Exec()
以“只读”的方式打开FIFO管道，读取内容。 
这同时也恢复之前处于阻塞状态的`runc Init`进程，Init进程会执行最后调用用户期待的cmd部分。
```go
func (c *linuxContainer) Exec() error {
	c.m.Lock()
	defer c.m.Unlock()
	return c.exec()
}

func (c *linuxContainer) exec() error {
	path := filepath.Join(c.root, execFifoFilename)
	/*
		以“只读”的方式打开FIFO管道，读取内容。
		这同时也恢复之前处于阻塞状态的`runc Init`进程，Init进程会执行最后调用用户期待的cmd部分。

		func OpenFile(name string, flag int, perm FileMode) (file *File, err error)
		以“指定文件权限和打开方式”来打开name文件或者create文件
		flag标识：
			O_RDONLY：只读模式(read-only)
			O_WRONLY：只写模式(write-only)
			O_RDWR：读写模式(read-write)
		操作权限perm，除非创建文件时才需要指定，不需要创建新文件时可以将其设定为０

		os package的详细用法
		http://blog.csdn.net/chenbaoke/article/details/42494851
	*/
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return newSystemErrorWithCause(err, "open exec fifo for reading")
	}
	defer f.Close()
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		/*
			如果读取到的data长度大于0，则读取到Create流程中最后写入的“0”，则删除FIFO管道文件
		*/
		os.Remove(path)
		return nil
	}
	return fmt.Errorf("cannot start an already running container")
}
```