# 定制一个API之ApiServer

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [创建你的Group Package](#创建你的group-package)
  - [实现你的Rest Storage](#实现你的rest-storage)
  - [Enabled你的Group和Resources](#enabled你的group和resources)
  - [自动生成代码工具](#自动生成代码工具)
  - [End](#end)

<!-- END MUNGE: GENERATED_TOC -->

直奔主题，在ApiServer端添加一个自己的GVK，且被被外面访问到

## 环境
本文是基于kubernetes V1.5.2进行, go1.7.5 linux/amd64


## 创建你的Group Package
首先定义一个`type Match struct`对象，Group名为`premierleague.k8s.io`，具体代码见[pkg-apis-premierleague](https://github.com/Kevin-fqh/learning-k8s-source-code/tree/master/apiserver/定制一个API之ApiServer篇/pkg-apis-premierleague)

参考[Adding-an-API-Group](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/apiserver/(28)Adding%20an%20API%20Group.md)

在这里要同时定义internal和versiond两个版本的数据结构，完成资源的注册。

要注意的是两个`types.generated.go`文件要写上package，不能直接是空文件去运行脚本。

完成数据结构的定义之后，不要急着去运行脚本生成代码，我们完成ApiServer端所有的逻辑之后，再去执行生成代码脚本。

## 实现你的Rest Storage
目录位置是`pkg/registry/premierleague`，具体代码见[pkg-registry-premierleague](https://github.com/Kevin-fqh/learning-k8s-source-code/tree/master/apiserver/定制一个API之ApiServer篇/pkg-registry-premierleague)

## Enabled你的Group和Resources
修改[pkg/master/master.go](https://github.com/kubernetes/kubernetes/blob/v1.8.0-alpha.2/pkg/master/master.go#L381)
```go
import (
	premierleagueapiv1 "k8s.io/kubernetes/pkg/apis/premierleague/v1"
	premierleaguerest "k8s.io/kubernetes/pkg/registry/premierleague/rest"
)

restStorageProviders := []genericapiserver.RESTStorageProvider{
		......
		......
		premierleaguerest.RESTStorageProvider{},
	}
	

func DefaultAPIResourceConfigSource() *genericapiserver.ResourceConfig {
	ret := genericapiserver.NewResourceConfig()
	ret.EnableVersions(
		...
		...
		premierleagueapiv1.SchemeGroupVersion,
	)

	ret.EnableResources(
		...
		...
		premierleagueapiv1.SchemeGroupVersion.WithResource("matchs"),
	)

	return ret
}
```

最后别忘了修改`pkg/master/import_known_versions.go`，初始化你的Group
```go
// These imports are the API groups the API server will support.
import (
	"fmt"

	_ "k8s.io/kubernetes/pkg/api/install"
	...
	...
	_ "k8s.io/kubernetes/pkg/apis/premierleague/install"
)
```
好了，至此Apiserver端的代码已经完成了。

## 自动生成代码工具
1. `/kubernetes-1.5.2/hack/lib/init.sh`中增加你的GV
```shell
# 这地方注意格式，前面都是有一个空格的，只有最后一个没空格！！！否则会出问题！！！
KUBE_AVAILABLE_GROUP_VERSIONS="${KUBE_AVAILABLE_GROUP_VERSIONS:-\
v1 \
apps/v1beta1 \
authentication.k8s.io/v1beta1 \
authorization.k8s.io/v1beta1 \
autoscaling/v1 \
batch/v1 \
batch/v2alpha1 \
certificates.k8s.io/v1alpha1 \
extensions/v1beta1 \
imagepolicy.k8s.io/v1alpha1 \
policy/v1beta1 \
rbac.authorization.k8s.io/v1alpha1 \
storage.k8s.io/v1beta1 \
premierleague.k8s.io/v1\
}"
```

2. `/kubernetes-1.5.2/cmd/libs/go2idl/go-to-protobuf/protobuf/cmd.go`中增加你的GV
```go
func New() *Generator {
	...
	...
		Packages: strings.Join([]string{
			...
			...
			`k8s.io/kubernetes/pkg/apis/storage/v1beta1`,
			`k8s.io/kubernetes/pkg/apis/premierleague/v1`,
		}, ","),
	...
	...
}
```

3. `/kubernetes-1.5.2/cmd/libs/go2idl/conversion-gen/main.go`中增加Group和GV
```go
// Custom args.
	customArgs := &generators.CustomArgs{
		ExtraPeerDirs: []string{
			...
			...
			"k8s.io/kubernetes/pkg/apis/premierleague",
			"k8s.io/kubernetes/pkg/apis/premierleague/v1",
		},
		SkipUnsafe: false,
	}
```

4. `/kubernetes-1.5.2/cmd/libs/go2idl/client-gen/main.go`中增加Group
```go
var (
	
	inputVersions = flag.StringSlice("input", []string{
		"api/",
		...
		...
		"premierleague/",
	}, 
	...
	...
)
```

自动生成代码工具会基于`+genclient=true` `+k8s:deepcopy-gen=package,register`这些注释来进行工作。

## End
执行脚本来生成转换函数和DeepCopy等代码，其中在`hack/update-all.sh`这一步，如果不是kubernetes源码不是用git clone下载的话，在生成doc文档的时候会报错退出，不过前面的步骤已经生成我们需要的代码。
```shell
$ make clean && make generated_files

$ hack/update-all.sh

$ make
```

效果如下，注意的是，这里的路由路径是`premierleague.k8s.io`，如果希望不带`k8s.io`，需要另作处理
```shell
[root@fqhnode01 premierleagueClient]# curl http://192.168.56.101:8080/apis/premierleague.k8s.io
{
  "kind": "APIGroup",
  "apiVersion": "v1",
  "name": "premierleague.k8s.io",
  "versions": [
    {
      "groupVersion": "premierleague.k8s.io/v1",
      "version": "v1"
    }
  ],
  "preferredVersion": {
    "groupVersion": "premierleague.k8s.io/v1",
    "version": "v1"
  },
  "serverAddressByClientCIDRs": null
}

[root@fqhnode01 premierleagueClient]# curl http://192.168.56.101:8080/apis/premierleague.k8s.io/v1
{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "premierleague.k8s.io/v1",
  "resources": [
    {
      "name": "matchs",
      "namespaced": true,
      "kind": "Match"
    }
  ]
}

[root@fqhnode01 premierleagueClient]# curl http://192.168.56.101:8080/apis/premierleague.k8s.io/v1/matchs
{
  "kind": "MatchList",
  "apiVersion": "premierleague.k8s.io/v1",
  "metadata": {
    "selfLink": "/apis/premierleague.k8s.io/v1/matchs",
    "resourceVersion": "29"
  },
  "items": null
}
```




