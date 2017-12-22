# Adding an API Group
===============

本文译自[Adding an API Group V1.6](https://github.com/kubernetes/community/blob/release-1.6/contributors/devel/adding-an-APIGroup.md)

## 创建你的core group package
方法在未来会改进，演变的方向参考[16062](https://github.com/kubernetes/kubernetes/pull/16062)。

1. 在`pkg/apis`目录下创建一个文件夹，用于存放你准备新建的group。然后在types.go中定义你的API objects
    - 新建`pkg/apis/<group>/types.go`
    - 新建`pkg/apis/<group>/<version>/types.go`
	
2. 在registry.go中把本Group 的API Object注册到Scheme中，可以参考`pkg/apis/authentication/register.go 和 pkg/apis/authentication/v1beta1/register.go`。 registry文件必须有一个名为SchemeBuilder的变量，以供自动生成的代码引用。 必须有一个AddToScheme方法供安装程序引用。 可以参考pkg/apis/...下Group的register.go文件，但不要直接复制粘贴，它们不是通用的。
    - `pkg/apis/<group>/register.go`
    - `pkg/apis/<group>/<version>/register.go`

3. 新建install.go,可能仅仅需要更改pkg/apis/authentication/install.go中的Group Name和Version。 这个package必须要在k8s.io/kubernetes/pkg/api/install中被导入。
    - `pkg/apis/<group>/install/install.go`
	
第2步和第3步是机械性的，我们计划使用`cmd/libs/go2idl/`下的工具来自动生成。

## Type definitions in types.go
每一个type都应该是一个可导出的struct(大写name)。 
这个struct应该内嵌`TypeMeta`和`ObjectMeta`，也应该含有属性`Spec`和`Status`。 
如果该对象仅仅是用来进行存储数据的，并且不会被controller修改，那么其`Status`字段可以保持不变；然后`Spec`中的fileds可以直接内连到本struct中。

对于每个top-level type，还应该有一个`List`结构体。 List结构应该内嵌`TypeMeta`和`ListMeta`。 
还应该有一个`Items`字段，是一个本type的切片。

## Scripts changes and auto-generated code

1. Generate conversions and deep-copies:
    1. Add your "group/" or "group/version" into cmd/libs/go2idl/conversion-gen/main.go;
    2. 确保你的`pkg/apis/<group>/<version>`目录下的doc.go的文件中有一行注解`// +k8s:deepcopy-gen=package,register`，
       以便引起自动生成代码工具的注意。
    3. 确保你的`pkg/apis/<group>/<version>`目录下的doc.go的文件中有一行注解`// +k8s:conversion-gen=<internal-pkg>`。
       对于大部分API来说，一般都是填写其对应的internal Group目录，`k8s.io/kubernetes/pkg/apis/<group>`。
    4. 确保目录`pkg/apis/<group>` 和 `pkg/apis/<group>/<version>` 下的doc.go中有一行注解`+groupName=<group>.k8s.io`，
       以便生成正确的该Group的DNS后缀。
    5. 运行 `hack/update-all.sh`
	
2. Generate files for Ugorji codec:
    1. Touch types.generated.go in pkg/apis/<group>{/, <version>};
    2. 运行 `hack/update-codecgen.sh`

3. Generate protobuf objects:
    1. 在`cmd/libs/go2idl/go-to-protobuf/protobuf/cmd.go`中的`func New()`中的`Packages`字段中新增你的Group
    2. 运行 `hack/update-generated-protobuf.sh`

译者在V1.5.2试验的时候，改好了所有代码之后，直接执行`hack/update-all.sh`即可。

记得在文件types.generated.go中写上 `package premierleague`和`package {version}`,不能是空文件。

## Client (optional):
把你的Group添加到client package中，方法如下：
1. 新建`pkg/client/unversioned/<group>.go`，定义一个group client interface，然后实现该client。 可以参考pkg/client/unversioned/extensions.go

2. 在`pkg/client/unversioned/client.go`中新增你的group client interface ，并提供方法来获取该interface。 可以参考如何添加Extensions group的。

3. 最后，如果需要kubectl支持你的group，需要修改`pkg/kubectl/cmd/util/factory.go`