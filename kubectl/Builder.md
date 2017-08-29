# Builder

## /pkg/kubectl/cmd/get.go
```go
/*
		定义在/pkg/kubectl/resource/builder.go
			==>func NewBuilder
		Builder大多方法支持链式调用
		最后的Do()返回一个type Result struct

		这里一些列链式调用大部分都在根据传入的Cmd来设置新建Builder的属性值
*/
	r := resource.NewBuilder(mapper, typer, resource.ClientMapperFunc(f.UnstructuredClientForMapping), runtime.UnstructuredJSONScheme).
		NamespaceParam(cmdNamespace).DefaultNamespace().AllNamespaces(allNamespaces).
		FilenameParam(enforceNamespace, &options.FilenameOptions).
		SelectorParam(selector).
		ExportParam(export).
		ResourceTypeOrNameArgs(true, args...).
		ContinueOnError().
		Latest().
		Flatten().
		Do()
```
## /pkg/kubectl/resource/builder.go
```go
/*
	func NewBuilder创建一个builder，用于操作generic objects
	Builder是Kubectl命令行信息的内部载体，可以通过Builder生成Result对象
*/
func NewBuilder(mapper meta.RESTMapper, typer runtime.ObjectTyper, clientMapper ClientMapper, decoder runtime.Decoder) *Builder {
	return &Builder{
		mapper:        &Mapper{typer, mapper, clientMapper, decoder},
		requireObject: true,
	}
}
```
type Builder struct提供了从命令行获取arguments和参数的函数接口，
并将其转换为一系列的resources，迭代使用Visitor interface。

	
	