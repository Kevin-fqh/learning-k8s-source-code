# docker镜像存储分析

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [环境和准备工作](#环境和准备工作)
  - [image 目录分析](#image-目录分析)
    - [repositories.json 文件](#repositoriesjson-文件)
    - [imagedb 目录](#imagedb-目录)
	  - [镜像ID的计算](#镜像id的计算)
    - [layerdb 目录](#layerdb-目录)
	  - [计算一个镜像的所有层级ChainID](#计算一个镜像的所有层级chainid)
	  - [通过ChainID找到真正的存储目录](#通过chainid找到真正的存储目录)	
  - [解压镜像tar包](#解压镜像tar包)
  - [启动一个容器](#启动一个容器)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

本文主要研究 docker 是如何存储一个镜像的。 主要是docker工作目录下的`/image`和`/overlay`目录

Docker镜像设计上将镜像元数据和镜像文件的存储完全分开，Docker在管理镜像层元数据时采用的是从上至下repository，image，layer三个层次。 
因为docker以分层的形式存储镜像，因此repository和image这两类元数据没有物理上的镜像文件与之对应，而layer则存在物理上的镜像文件与之对应。

## 环境和准备工作
```
centos 7
docker version: 1.13.1
```
1. 为保持环境的干净，新建一个工作目录`/home/docker-test-dir`
```shell
# dockerd -H 0.0.0.0:4243 -H unix:///var/run/docker.sock -g /home/docker-test-dir
```
2. 下载一个ubuntu镜像
```shell
# docker images
REPOSITORY          TAG                 IMAGE ID            CREATED             SIZE
ubuntu              latest              dd6f76d9cc90        6 days ago          122 MB

# pwd
/home/docker-test-dir

# du -sh ./*
0	./containers
468K	./image
32K	./network
131M	./overlay
0	./plugins
0	./swarm
0	./tmp
0	./trust
24K	./volumes
```
可以发现仅仅有`/image`和`/overlay`发生了变化。 
可以推断`/image`是维护镜像的层级配置信息，而`/overlay`则是存储实际的layer级内容。

## image 目录分析
image目录下会根据底层存储驱动的类型进行分类，我们现在是overlay，在overlay目录下，有着下述文件和目录：
   * distribution 目录
   * repositories.json 文件, 入口文件，imagedb 目录的索引
   * imagedb 目录，Image数据库，从中获取一个镜像的config文件，根据这个文件计算出layerdb 目录中的信息
   * layerdb 目录，记录着一个镜像的各个layer层信息，根据这可以获取到一个layer真正存储的物理目录


### repositories.json 文件
repositories.json 文件是记录着目前系统中一共有几个镜像，镜像的tag，镜像ID
```shell
{
    "Repositories": {
        "ubuntu": {
            "ubuntu:latest": "sha256:dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be",
            "ubuntu@sha256:152b4ccc429f6f28533aff625d8345baf1ba3808e9a99446e86b2bf3efa18571": "sha256:dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be"
        }
    }
}
```
现在知道镜像的ID是`dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be`

### imagedb 目录
- content 目录，有个config文件记录着镜像详细信息
- metadata 目录
```shell
# tree /home/docker-test-dir/image/overlay/imagedb
/home/docker-test-dir/image/overlay/imagedb
├── content
│   └── sha256
│       └── dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be
└── metadata
    └── sha256

4 directories, 1 file
```

打印`dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be`这个json文件的信息,里面记录着这个镜像的`rootfs`信息。
```shell
 "rootfs": {
        "diff_ids": [
            "sha256:0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368",
            "sha256:f1c896f31e4935defe0f9714c011ee31b3179ac745a4ed04e07c2e6ef2a7c349",
            "sha256:51db18d04d72c86f0fdc6087c6903bc3ad6bec155f6cfbb9b3051e70fb910cf3",
            "sha256:f51f76255b02ab76a187976397bad344c086ab3b1f132058b09e545beb7e8d2d",
            "sha256:174a611570d45cc705d866329385f28f25b917b97641013603a85ba148996a05"
        ],
        "type": "layers"
    }
```

`Rootfs`：代表一个Docker Container在启动时（而非运行后）其内部进程可见的文件系统视角，或者是Docker Container的根目录。容器中的进程对其只有只读的权限。

#### 镜像ID的计算
计算`dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be 文件内容`的sha256编码即可得到镜像ID。
```shell
[root@fqhnode01 sha256]# sha256sum ./dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be 
dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be  ./dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be
```

### layerdb 目录
目前只有两个目录：
- sha256 目录: 存储layer信息；
- tmp: 临时目录

```shell
[root@fqhnode01 layerdb]# tree /home/docker-test-dir/image/overlay/layerdb
/home/docker-test-dir/image/overlay/layerdb
├── sha256
│   ├── 0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368
│   │   ├── cache-id
│   │   ├── diff
│   │   ├── size
│   │   └── tar-split.json.gz
│   ├── 6428d162737d88c3d35c01efc0eacf39eb1b040f7c5aea7ed30e72d062a36d89
│   │   ├── cache-id
│   │   ├── diff
│   │   ├── parent
│   │   ├── size
│   │   └── tar-split.json.gz
│   ├── 97e940224b19489fe0345f4ec05397889f4ec6250ce0cbd2363b9ed9d54bac2e
│   │   ├── cache-id
│   │   ├── diff
│   │   ├── parent
│   │   ├── size
│   │   └── tar-split.json.gz
│   ├── 9b85b1f6f82cccad421643297784c6326e660d9da5e97686cdb5f07a3e09153f
│   │   ├── cache-id
│   │   ├── diff
│   │   ├── parent
│   │   ├── size
│   │   └── tar-split.json.gz
│   └── a25db7dbfd3205a483f67bfaa81de9924b6c687c4f1b3ccf6bd42da9bc7387ad
│       ├── cache-id
│       ├── diff
│       ├── parent
│       ├── size
│       └── tar-split.json.gz
└── tmp

7 directories, 24 files
```

从上面`sha256 目录`内容能看出，这个ubuntu镜像一共有5层，每个层级都是一个目录，其ID称之为`ChainID`，层级ID都已经列出来。 
那么这些ID是怎么计算出来的？ 
都是从上面`imagedb 目录`中的一个镜像的config文件中`rootfs`信息计算出来的。

#### 计算一个镜像的所有层级ChainID
在docker源码中，可以见/moby-1.13.1/moby-1.13.1/layer/layer.go中的`func CreateChainID`

1. 根据之前镜像的`rootfs`信息，发现这个有5个`diff_ids`值，如下所示: 
```shell
 "diff_ids": [
            "sha256:0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368",
            "sha256:f1c896f31e4935defe0f9714c011ee31b3179ac745a4ed04e07c2e6ef2a7c349",
            "sha256:51db18d04d72c86f0fdc6087c6903bc3ad6bec155f6cfbb9b3051e70fb910cf3",
            "sha256:f51f76255b02ab76a187976397bad344c086ab3b1f132058b09e545beb7e8d2d",
            "sha256:174a611570d45cc705d866329385f28f25b917b97641013603a85ba148996a05"
        ],
```

2. 第一个 diff_id 值是 0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368，那么ubuntu镜像的第一层的 ChainID 就是 0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368。

3. 第二个 diff_id 值是 f1c896f31e4935defe0f9714c011ee31b3179ac745a4ed04e07c2e6ef2a7c349，这里要分两步来进行计算。
  - 第一步，和前一个 ChainID 合并（前一层的ChainID + " " +本层的diff_id)
```shell
"sha256:0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368 sha256:f1c896f31e4935defe0f9714c011ee31b3179ac745a4ed04e07c2e6ef2a7c349"
```

  - 第二步，对合并后的值进行计算sha256编码得出ubuntu镜像第二层的 ChainID, 这里需要用"crypto/sha256"来进行计算。用sha256sum求与GO语言求得到的结果不一样，因为cat或echo会在字符串后加’\n’。
```go
package main

import (
	sha256 "crypto/sha256"
	"fmt"
)

func main() {
	s := "sha256:0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368 sha256:f1c896f31e4935defe0f9714c011ee31b3179ac745a4ed04e07c2e6ef2a7c349"
	h := sha256.New()
	h.Write([]byte(s))
	bs := h.Sum(nil)
	fmt.Printf("%x", bs)
}
```

  - 输出内容如下，这就是ubuntu镜像的第二层
```shell
a25db7dbfd3205a483f67bfaa81de9924b6c687c4f1b3ccf6bd42da9bc7387ad
```

4. 以此类推就可以计算出ubuntu镜像5个层级ChainID了。

#### 通过ChainID找到真正的存储目录
以该layer的ChainID来作为目录的名字，里面的文件如下所示:
```shell
# pwd
/home/docker-test-dir/image/overlay/layerdb/sha256/a25db7dbfd3205a483f67bfaa81de9924b6c687c4f1b3ccf6bd42da9bc7387ad

# ls
cache-id  diff  parent  size  tar-split.json.gz
```

* cache-id :记录着本层内容真正存储的位置
* diff :本层的diff_id
* size :本层的大小
* tar-split.json.gz
* parent : 上一层的ChainID，如果本层是一个镜像的第一层，不会有parent文件

最后根据`cache-id`来查找一层内容真正存储的地方
```shell
[root@fqhnode01 overlay]# pwd
/home/docker-test-dir/overlay

[root@fqhnode01 overlay]# ls
89665e55800327c8a29243e757ce8e233f0ddd4628e8316053ca9e5c0b91da8d
942b6f85df80963d45597f41f995ed4527021bd51a219d70241f6dd7eb16625e
985b43df566530ecb7b2922f68dca0c8a2ae1f567be690d60b6f9430cc1ad538
f353ff97827c1c625e976cbef4f6baeb4e02bb49db7b39e92628a1e179130d8a
fb8689ec83db4eab9f5ab10719ecd98e71ad20158c0c95d768e7dd17763b0ae1
```
可以发现这里的目录和`cache-id`的值都是一一对应的。

至此，docker是怎么管理一个镜像所有的layer层基本清楚。 后面需要了解的是
- docker是如何从一个镜像的tar包中得出其配置信息，计算其`rootfs`信息，特别是 diff_ids 的值。
- docker启动一个容器的时候，怎么获取一个镜像
	
## 解压镜像tar包
用tar命令解压ubuntu镜像的tar包，内容如下：
```shell
# ls
0aa493ba1d60b986feb38f13dbdba07ad5e10c409187f8a0b3975f319fc7de54
18f61761bde1b9663172cec5598955410621ddb198935bbe472b2e30c1caf644
3ffe70f1a23acb9d07372dcae6e7a3489f527ce8f2c01b13610871b62de53138
ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5
dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be.json
ffd1891c9cd0690e88a85eebf70a5e804eb30b404ff941b89626e8e440b07147
manifest.json
repositories
```

### Config文件
其中的dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be.json文件就是前面imagedb 目录下说到的一个镜像的config文件，是dockr直接copy过去的。
```shell
[root@fqhnode01 ubuntu]# diff dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be.json /home/docker-test-dir/image/overlay/imagedb/content/sha256/dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be 
[root@fqhnode01 ubuntu]#
```

### repositories文件
```shell
# cat repositories 
{"ubuntu":{"latest":"3ffe70f1a23acb9d07372dcae6e7a3489f527ce8f2c01b13610871b62de53138"}}
```

### 每一层的目录
每一层Layer都有一个目录，其内容如下
```shell
[root@fqhnode01 ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5]# ls
json  layer.tar  VERSION

[root@fqhnode01 ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5]# cat json |python -mjson.tool
{
    "container_config": {
        "AttachStderr": false,
        "AttachStdin": false,
        "AttachStdout": false,
        "Cmd": null,
        "Domainname": "",
        "Entrypoint": null,
        "Env": null,
        "Hostname": "",
        "Image": "",
        "Labels": null,
        "OnBuild": null,
        "OpenStdin": false,
        "StdinOnce": false,
        "Tty": false,
        "User": "",
        "Volumes": null,
        "WorkingDir": ""
    },
    "created": "2017-11-04T09:45:35.219223981Z",
    "id": "ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5"
}
```
json文件中的id号就是本层的目录名字。

### manifest.json文件
```shell
# cat manifest.json |python -mjson.tool
[
    {
        "Config": "dd6f76d9cc90f3ec2bded9e1c970bb6a8c5259e05401b52df42c997dec1e79be.json",
        "Layers": [
            "ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5/layer.tar",
            "0aa493ba1d60b986feb38f13dbdba07ad5e10c409187f8a0b3975f319fc7de54/layer.tar",
            "18f61761bde1b9663172cec5598955410621ddb198935bbe472b2e30c1caf644/layer.tar",
            "ffd1891c9cd0690e88a85eebf70a5e804eb30b404ff941b89626e8e440b07147/layer.tar",
            "3ffe70f1a23acb9d07372dcae6e7a3489f527ce8f2c01b13610871b62de53138/layer.tar"
        ],
        "RepoTags": [
            "ubuntu:latest"
        ]
    }
]
```
- Config字段为config的文件名
- Layers为镜像tar包中的存储位置,据此可以计算出每一层的 diff_ids 值
- RepoTags标明镜像名

其中Layers字段中的层级是按顺序排列的，也就是说ubuntu镜像的第一层是ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5。 那么我们据此来计算其 diff_ids 值：
```shell
[root@fqhnode01 ab217d62e9ea5cc3133ccf9223284fbe9c2bdf899d276c731463646de631a8d5]# sha256sum ./layer.tar 
0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368  ./layer.tar
```
得到的`0f5ff0cf6a1c53f94b15f03536c490040f233bc455f1232f54cc8eb344a3a368`就是Config文件中第一层的diff_ids。

后面4层的diff_ids也是这样计算出来，直接计算sha256sum编码即可。

## 启动一个容器
启动前后对比，docker run -it dd6f76d9cc90 sh
```shell
[root@fqhnode01 docker-test-dir]# du -sh ./*
0	./containers
468K	./image
32K	./network
131M	./overlay
0	./plugins
0	./swarm
0	./tmp
0	./trust
24K	./volumes
[root@fqhnode01 docker-test-dir]# 
[root@fqhnode01 docker-test-dir]# du -sh ./*
28K	./containers
480K	./image
44K	./network
131M	./overlay
0	./plugins
0	./swarm
0	./tmp
0	./trust
24K	./volumes
```

可以发现`/image`目录是发生了变化的,会多出一个`mount`目录，记录一个容器启动的时候的挂载点信息
```shell
[root@fqhnode01 mounts]# pwd
/home/docker-test-dir/image/overlay/layerdb/mounts

[root@fqhnode01 mounts]# docker ps
CONTAINER ID        IMAGE               COMMAND             CREATED             STATUS              PORTS               NAMES
e7530a5fe331        dd6f76d9cc90        "sh"                24 seconds ago      Up 24 seconds                           pedantic_perlman

[root@fqhnode01 mounts]# ls
e7530a5fe331bd4a63094d0c62f2ad010cbe5866910f0071a8f171468317482c
```

查看e7530a5fe331bd4a63094d0c62f2ad010cbe5866910f0071a8f171468317482c容器目录，发现以下文件：
- init-id，容器的init目录，这里内容是  555a92ef59e6f46885577ba05f9c860e166844703458cf41a13e0270f24ca972-init
- mount-id，容器的启动目录，这里内容是 555a92ef59e6f46885577ba05f9c860e166844703458cf41a13e0270f24ca972
- parent，容器使用的镜像的最后一层的ChainID，这里内容是 sha256:6428d162737d88c3d35c01efc0eacf39eb1b040f7c5aea7ed30e72d062a36d89
	
查看此时docker工作目录下的`/overlay`目录
```shell
[root@fqhnode01 overlay]# pwd
/home/docker-test-dir/overlay
[root@fqhnode01 overlay]# ls
555a92ef59e6f46885577ba05f9c860e166844703458cf41a13e0270f24ca972
555a92ef59e6f46885577ba05f9c860e166844703458cf41a13e0270f24ca972-init
89665e55800327c8a29243e757ce8e233f0ddd4628e8316053ca9e5c0b91da8d
942b6f85df80963d45597f41f995ed4527021bd51a219d70241f6dd7eb16625e
985b43df566530ecb7b2922f68dca0c8a2ae1f567be690d60b6f9430cc1ad538
f353ff97827c1c625e976cbef4f6baeb4e02bb49db7b39e92628a1e179130d8a
fb8689ec83db4eab9f5ab10719ecd98e71ad20158c0c95d768e7dd17763b0ae1
```
发现init-id和mount-id指定的目录都在这里找到了。

## 总结
1. 根据manifest.json文件中的Layers每一层的内容layer.tar来计算该层的diff_ids值

2. 根据每一层的diff_ids值来计算出所有的ChainID值

3. 每一个ChainID在layerdb中都有一个对应目录，其中的cache-id记录着该层layer存储的真实物理位置

4. Docker镜像设计上将镜像元数据和镜像文件的存储完全分开，Docker在管理镜像层元数据时采用的是从上至下repository，image，layer三个层次 

5. 因为docker以分层的形式存储镜像，因此repository和image这两类元数据没有物理上的镜像文件与之对应，而layer则存在物理上的镜像文件与之对应

6. docker启动的时候，建立一个mount目录，记录着该容器的启动挂载点信息、使用的镜像的最后一层的ChainID