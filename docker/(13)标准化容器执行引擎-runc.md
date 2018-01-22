## 标准化容器执行引擎-runc

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [容器的标准OCF](#容器的标准OCF)
    - [容器标准包（bundle）](#容器标准包-bundle)
    - [运行时状态](#运行时状态)
  - [runc启动一个容器](#runc启动一个容器)
    - [使用docker来生成一个容器的rootfs](#使用docker来生成一个容器的rootfs)
    - [spec配置](#spec配置)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 版本说明
* runc version 1.0.0-rc2
* containerd version 0.2.4  

runc和containerd的版本也要匹配，否则在解析config.json的时候也容易出问题
编译都使用go-1.8.4，好像低于1.8的容易出问题，比如grpc连接不上

## 容器的标准OCF
容器标准化的目标：操作标准化、内容无关、基础设施无关。 runc是OCF的其中一个具体实现，下面从runc的角度来介绍OCF，这并不是OCF的全部标准

### 容器标准包（bundle）
现在的runc下的bundle包含了两个部分，rootfs目录必须与config.json文件同时存在容器目录最顶层。
* /rootfs目录，根文件系统目录，包含了容器执行所需的必要环境依赖，如/bin、/var、/lib、/dev、/usr等目录及相应文件。

* 配置文件config.json，包含了基本配置和容器运行时的相关配置，主要包括
    * ociVersion, runc的版本
	* platform，宿主机信息
	* process，容器启动时的初始化进程，通过args传递命令，用户uid/gid绑定，rlimit配置、capabilities设置
	* root，rootfs目录的路径，读写权限设置
	* hostname
	* mounts，挂载信息设置
	* hooks，钩子，设置容器运行前和停止后执行的自定义脚本
	* linux，针对Linux的特性支持的诸如namespace设置、cgroups资源限额、设备权限配置、apparmor配置项目录、selinux标记以及seccomp配置。
```json
{
	"ociVersion": "1.0.0-rc2-dev",
	"platform": {
		"os": "linux",
		"arch": "amd64"
	},
	"process": {
		"terminal": false, //false表示以后台模式运行
		"user": {
			"uid": 0,  //都是0，表示进入容器后是root用户
			"gid": 0
		},
		"args": [
			"sleep", "10"
		],
		"env": [
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"TERM=xterm"
		],
		"cwd": "/",  //工作目录为/
		"capabilities": [ //使用白名单制度
			"CAP_AUDIT_WRITE", //允许写入审计日志
			"CAP_KILL", //允许发送信号
			"CAP_NET_BIND_SERVICE" //允许绑定socket到网络端口
		],
		"rlimits": [
			{
				"type": "RLIMIT_NOFILE",
				"hard": 1024,
				"soft": 1024
			}
		],
		"noNewPrivileges": true
	},
	"root": {
		"path": "rootfs",
		"readonly": true
	},
	"hostname": "runc",
	"mounts": [
		{
			"destination": "/proc",
			"type": "proc",
			"source": "proc"
		},
		...
		...
	],
	"hooks": {},
	"linux": {
		"resources": {
			"devices": [
				{
					"allow": false,
					"access": "rwm"
				}
			]
		},
		"namespaces": [
			{
				"type": "pid"
			},
			{
				"type": "network"
			},
			{
				"type": "ipc"
			},
			{
				"type": "uts"
			},
			{
				"type": "mount"
			}
		],
		"maskedPaths": [
			"/proc/kcore",
			"/proc/latency_stats",
			"/proc/timer_list",
			"/proc/timer_stats",
			"/proc/sched_debug",
			"/sys/firmware"
		],
		"readonlyPaths": [
			"/proc/asound",
			"/proc/bus",
			"/proc/fs",
			"/proc/irq",
			"/proc/sys",
			"/proc/sysrq-trigger"
		]
	}
}
```

### 运行时状态
OCF要求容器把自身运行时的状态持久化到磁盘中，这样便于外部的其他工具对此信息使用。
该运行时状态以JSON格式编码存储。 
推荐把运行时状态的json文件存储在临时文件系统中以便系统重启后会自动移除。

runc以state.json文件保存一个容器运行时的状态信息,默认存放在/run/runc/{containerID}/state.json。

## runc启动一个容器
runc是基于容器标准包（bundle）来启动一个容器，官方步骤如下：

### 使用docker来生成一个容器的rootfs
```shell
# create the top most bundle directory
mkdir /mycontainer
cd /mycontainer

# create the rootfs directory
mkdir rootfs

# export busybox via Docker into the rootfs directory
docker export $(docker create busybox:latest) | tar -C rootfs -xvf -

# 现在可以从docker中删除该容器，我们的目的仅是创建一个容器的rootfs
docker rm {containerID}
```
至此，OCF标准的rootfs目录创建好了。 使用Docker只是为了获取rootfs目录的方便，runc的运行本身不依赖Docker。

### spec配置
关于spec详细信息，可以查看[runtime-spec](https://github.com/opencontainers/runtime-spec)。 

containerd中也有一个具体的例子，见[containerd-bundle](https://github.com/containerd/containerd/blob/v0.2.3/docs/bundle.md)
```shell
docker-runc spec
```
runc spec 命令会自动生成一个config.json文件，可以自行设置。 
如果不设置，直接start一个容器，那么效果就是`docker exec -it {containerID} sh`。 

修改config.json中的process部分，以sleep命令启动一个容器
```json
   		"process": {
                "terminal": false,
                "user": {
                        "uid": 0,
                        "gid": 0
                },
                "args": [
                        "sleep", "5"
                ],
                "env": [
                        "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                        "TERM=xterm"
                ],
                "cwd": "/",
                "capabilities": [
                        "CAP_AUDIT_WRITE",
                        "CAP_KILL",
                        "CAP_NET_BIND_SERVICE"
                ],
                "rlimits": [
                        {
                                "type": "RLIMIT_NOFILE",
                                "hard": 1024,
                                "soft": 1024
                        }
                ],
                "noNewPrivileges": true
        },
```
现在，可以启动容器了
```shell
runc create mycontainerid

# view the container is created and in the "created" state
runc list

# start the process inside the container
runc start mycontainerid

# after 5 seconds view that the container has exited and is now in the stopped state
runc list

# now delete the container
runc delete mycontainerid
```

最后可以用systemd来管理一个runc容器，实现在其退出后自动重启。

## 参考
[runc官网](https://github.com/opencontainers/runc/tree/v1.0.0-rc2)

[runC介绍](http://www.infoq.com/cn/articles/docker-standard-container-execution-engine-runc)

[opencontainers/runtime-spec](https://github.com/opencontainers/specs)