# docker命令行

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [命令行分类](#命令行分类)
  - [docker常用命令](#docker常用命令)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 版本说明
v1.13.1

## 命令行分类
docker现在采用也是"github.com/spf13/cobra"来管理其命令结构，和k8s是一样的。

来看看docker的命令行分类，见/moby-1.13.1/cli/command/commands/commands.go
```go
// AddCommands adds all the commands from cli/command to the root command
func AddCommands(cmd *cobra.Command, dockerCli *command.DockerCli) {
	cmd.AddCommand(
		/*
			根据资源划分，添加各种子命令到root command下
		*/
		node.NewNodeCommand(dockerCli),
		service.NewServiceCommand(dockerCli),
		swarm.NewSwarmCommand(dockerCli),
		secret.NewSecretCommand(dockerCli),
		container.NewContainerCommand(dockerCli),
		image.NewImageCommand(dockerCli),
		system.NewSystemCommand(dockerCli),
		container.NewRunCommand(dockerCli),
		image.NewBuildCommand(dockerCli),
		network.NewNetworkCommand(dockerCli),
		hide(system.NewEventsCommand(dockerCli)),
		registry.NewLoginCommand(dockerCli),
		registry.NewLogoutCommand(dockerCli),
		registry.NewSearchCommand(dockerCli),
		system.NewVersionCommand(dockerCli),
		volume.NewVolumeCommand(dockerCli),
		hide(system.NewInfoCommand(dockerCli)),
		hide(container.NewAttachCommand(dockerCli)),
		hide(container.NewCommitCommand(dockerCli)),
		hide(container.NewCopyCommand(dockerCli)),
		hide(container.NewCreateCommand(dockerCli)),
		hide(container.NewDiffCommand(dockerCli)),
		hide(container.NewExecCommand(dockerCli)),
		hide(container.NewExportCommand(dockerCli)),
		hide(container.NewKillCommand(dockerCli)),
		hide(container.NewLogsCommand(dockerCli)),
		hide(container.NewPauseCommand(dockerCli)),
		hide(container.NewPortCommand(dockerCli)),
		hide(container.NewPsCommand(dockerCli)),
		hide(container.NewRenameCommand(dockerCli)),
		hide(container.NewRestartCommand(dockerCli)),
		hide(container.NewRmCommand(dockerCli)),
		hide(container.NewStartCommand(dockerCli)),
		hide(container.NewStatsCommand(dockerCli)),
		hide(container.NewStopCommand(dockerCli)),
		hide(container.NewTopCommand(dockerCli)),
		hide(container.NewUnpauseCommand(dockerCli)),
		hide(container.NewUpdateCommand(dockerCli)),
		hide(container.NewWaitCommand(dockerCli)),
		hide(image.NewHistoryCommand(dockerCli)),
		hide(image.NewImagesCommand(dockerCli)),
		hide(image.NewImportCommand(dockerCli)),
		hide(image.NewLoadCommand(dockerCli)),
		hide(image.NewPullCommand(dockerCli)),
		hide(image.NewPushCommand(dockerCli)),
		hide(image.NewRemoveCommand(dockerCli)),
		hide(image.NewSaveCommand(dockerCli)),
		hide(image.NewTagCommand(dockerCli)),
		hide(system.NewInspectCommand(dockerCli)),
		stack.NewStackCommand(dockerCli),
		stack.NewTopLevelDeployCommand(dockerCli),
		checkpoint.NewCheckpointCommand(dockerCli),
		plugin.NewPluginCommand(dockerCli),
	)

}

/*
	在设置了 环境变量“DOCKER_HIDE_LEGACY_COMMANDS”的情况下
	func hide的功能就是给入参 cmd 增加两个属性：Hidden和Aliases
*/
func hide(cmd *cobra.Command) *cobra.Command {
	if os.Getenv("DOCKER_HIDE_LEGACY_COMMANDS") == "" {
		return cmd
	}
	cmdCopy := *cmd
	cmdCopy.Hidden = true
	cmdCopy.Aliases = []string{}
	return &cmdCopy
}
```

docker 之前版本的命令管理比较复杂的，容易混淆。 
在v1.13.1中进行了重构，旧的语法仍然支持。

func AddCommands将命令按照逻辑分组到`management commands`中，以下就是docker中的顶级命令（注意这里都是`单数`的）：
```
Management Commands:
  container   Manage containers
  image       Manage images
  network     Manage networks
  node        Manage Swarm nodes
  plugin      Manage plugins
  secret      Manage Docker secrets
  service     Manage services
  stack       Manage Docker stacks
  swarm       Manage Swarm
  system      Manage Docker
  volume      Manage volumes
```

每一个管理命令有一套类似的`子命令`，他们负责执行操作:
```
  子命令                 用途
    ls             获取<image,container,volume,secret等等>的列表
    rm             移除<image,container,volume等等>
    inspect        检阅<image,container,volume等等>
```
这也就是说：`docker image ls` 效果等同于 `docker images`。

重构之后，一些平时使用频繁的`子命令`仍然留在顶层。 
默认情况下，所有的顶级命令也会显示出来。 
可以设置`DOCKER_HIDE_LEGACY_COMMANDS环境变量`来只显示顶级命令，这就是`func hide`函数的作用。 
但即便如此`docker --help`依然会显示所有的顶级命令和管理命令。

最后可以通过下述命令来强制只显示管理命令
```shell
DOCKER_HIDE_LEGACY_COMMANDS=true docker --help
```
可以和`docker --help`命令的效果进行对比下

## docker常用命令
最后列举一下docker常用命令
```
1.12    1.13                用途

attach  container attach    附加到一个运行的容器
build   image build         从一个Dockerfile构建镜像
commit  container commit    从一个容器的修改创建一个新的镜像
cp      container cp        在容器与本地文件系统之间复制文件/文件夹
create  container create    创建新的容器
diff    container diff      检阅一个容器文件系统的修改
events  system events       获取服务器的实时时间
exec    container exec      在运行的容器内执行命令
export  container export    打包一个容器文件系统到tar文件
history image history       展示镜像历史信息
images  image ls            展示镜像列表
import  image import        用tar文件导入并创建镜像文件
info    system info         展示整个系统信息
inspect container inspect   展示一个容器/镜像或者任务的底层信息
kill    container kill      终止一个或者多个运行中的容器
load    image load          从tar文件或者标准输入载入镜像
login   login               登录Docker registry
logout  logout              从Docker registry登出
logs    container logs      获取容器的日志
network network             管理Docker网络
node    node                管理Docker Swarm节点
pause   container pause     暂停一个或者多个容器的所有进程
port    container port      展示容器的端口映射
ps      container ls        展示容器列表
pull    image pull          从某个registry拉取镜像或者仓库
push    image push          推送镜像或者仓库到某个registry
rename  container rename    重命名容器
restart container restart   重启容器
rm      container rm        移除一个或多个容器
rmi     image rm            移除一个或多个镜像
run     container run       运行一个新的容器
save    image save          打包一个或多个镜像到tar文件(默认是到标准输出)
search  search              在Docker Hub搜索镜像
service service             管理Docker services
start   container start     启动一个或者多个容器
stats   container stats     获取容器的实时资源使用统计
stop    container stop      停止一个或多个运行容器
swarm   swarm               管理Docker Swarm
tag     image tag           标记一个镜像到仓库
top     container top       展示容器运行进程
unpause container unpause   解除暂停一个或多个容器的所有进程
update  container update    更新一个或多个容器的配置
version version             显示Docker版本信息
volume  volume              管理Docker volumes
wait    container wait      阻塞直到容器停止，然后打印退出代码
```
	
## 参考
[Docker 1.13 管理命令](http://dockone.io/article/2059)

[Docker 1.13 Management Commands](https://blog.couchbase.com/docker-1-13-management-commands/)