# Builder

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [开始的地方](#开始的地方)
  - [func NewBuilder](#func-newbuilder)
  - [Builder提供的方法](#builder提供的方法)
	- [func Do详解](#func-do详解)



  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

承接kubectl系列(1)中的func RunGet运行过程，现在开始了解Builder。

## 开始的地方
从/pkg/kubectl/cmd/get.go 的func RunGet函数中可以发现
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

## func NewBuilder
定义在/pkg/kubectl/resource/builder.go
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
并将其转换为一系列的resources，以便可以迭代使用Visitor interface。
```go
// Builder provides convenience functions for taking arguments and parameters
// from the command line and converting them to a list of resources to iterate
// over using the Visitor interface.
/*
	Builder是Kubectl命令行信息的内部载体，可以通过Builder生成Result对象。Builder大多方法支持链式调用
*/
type Builder struct {
	/*
		Builder中有一个mapper，
		然后就是一些资源方面的字段，这些资源字段都有方法对其进行设置。
		其中，resources需要和names或selector配合使用
	*/
	mapper *Mapper

	errs []error

	paths  []Visitor
	stream bool
	dir    bool

	selector  labels.Selector
	selectAll bool

	resources []string

	namespace    string
	allNamespace bool
	names        []string

	resourceTuples []resourceTuple

	defaultNamespace bool
	requireNamespace bool

	flatten bool
	latest  bool

	requireObject bool

	singleResourceType bool
	continueOnError    bool

	singular bool

	export bool

	schema validation.Schema
}
```

## Builder提供的方法
本节将对上面调用的Builder方法进行介绍。
大部分的方法都是对Builder中的属性进行赋值。
```go
// NamespaceParam accepts the namespace that these resources should be
// considered under from - used by DefaultNamespace() and RequireNamespace()
/*
	func (b *Builder) NamespaceParam 设置b *Builder的namespace属性，
	会被 DefaultNamespace() and RequireNamespace()使用
*/
func (b *Builder) NamespaceParam(namespace string) *Builder {
	b.namespace = namespace
	return b
}

// DefaultNamespace instructs the builder to set the namespace value for any object found
// to NamespaceParam() if empty.
/*
	让builder在namespace为空的时候，找到namespace的值
*/
func (b *Builder) DefaultNamespace() *Builder {
	b.defaultNamespace = true
	return b
}

// AllNamespaces instructs the builder to use NamespaceAll as a namespace to request resources
// across all of the namespace. This overrides the namespace set by NamespaceParam().
/*
	func AllNamespaces 让builder使用NamespaceAll作为cmd的namespace，向所有的namespace请求resources。
	将重写由func (b *Builder) NamespaceParam(namespace string)设置的属性namespace
*/
func (b *Builder) AllNamespaces(allNamespace bool) *Builder {
	/*
		如果入参allNamespace bool＝true,那么重写b *Builder的namespace和allNamespace属性
			api.NamespaceAll定义在 pkg/api/v1/types.go
				==>NamespaceAll string = ""
	*/
	if allNamespace {
		b.namespace = api.NamespaceAll
	}
	b.allNamespace = allNamespace
	return b
}

// FilenameParam groups input in two categories: URLs and files (files, directories, STDIN)
// If enforceNamespace is false, namespaces in the specs will be allowed to
// override the default namespace. If it is true, namespaces that don't match
// will cause an error.
// If ContinueOnError() is set prior to this method, objects on the path that are not
// recognized will be ignored (but logged at V(2)).
/*
	译：func (b *Builder) FilenameParam以URLs and files (files, directories, STDIN)两种形式来传入参数。
		如果enforceNamespace＝false，specs中声明的namespaces将允许被重写为default namespace。
		如果enforceNamespace＝true，不匹配的namespaces将导致error。
		如果在此方法之前设置了ContinueOnError()，则路径上无法识别的objects将被忽略（记录在 V(2)级别的log中）。
*/
func (b *Builder) FilenameParam(enforceNamespace bool, filenameOptions *FilenameOptions) *Builder {
	recursive := filenameOptions.Recursive
	paths := filenameOptions.Filenames
	for _, s := range paths {
		switch {
		case s == "-":
			b.Stdin()
		case strings.Index(s, "http://") == 0 || strings.Index(s, "https://") == 0:
			url, err := url.Parse(s)
			if err != nil {
				b.errs = append(b.errs, fmt.Errorf("the URL passed to filename %q is not valid: %v", s, err))
				continue
			}
			b.URL(defaultHttpGetAttempts, url)
		default:
			if !recursive {
				b.singular = true
			}
			b.Path(recursive, s)
		}
	}

	if enforceNamespace {
		b.RequireNamespace()
	}

	return b
}

// SelectorParam defines a selector that should be applied to the object types to load.
// This will not affect files loaded from disk or URL. If the parameter is empty it is
// a no-op - to select all resources invoke `b.Selector(labels.Everything)`.
/*
	译：func (b *Builder) SelectorParam 定义了一个selector，应用在将要加载的object types上。
		这不会影响从磁盘或URL加载的文件。
		如果selector parameter为empty，那么将没有特定的选择行为，将使用`b.Selector(labels.Everything)`
	selector和label结合使用，eg: nodeSelector
*/
func (b *Builder) SelectorParam(s string) *Builder {
	selector, err := labels.Parse(s)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("the provided selector %q is not valid: %v", s, err))
		return b
	}
	if selector.Empty() {
		return b
	}
	if b.selectAll {
		b.errs = append(b.errs, fmt.Errorf("found non empty selector %q with previously set 'all' parameter. ", s))
		return b
	}
	//设置builder的selector属性
	return b.Selector(selector)
}

// ExportParam accepts the export boolean for these resources
/*
	设置b *Builder的export属性
*/
func (b *Builder) ExportParam(export bool) *Builder {
	b.export = export
	return b
}

// ResourceTypeOrNameArgs indicates that the builder should accept arguments
// of the form `(<type1>[,<type2>,...]|<type> <name1>[,<name2>,...])`. When one argument is
// received, the types provided will be retrieved from the server (and be comma delimited).
// When two or more arguments are received, they must be a single type and resource name(s).
// The allowEmptySelector permits to select all the resources (via Everything func).
/*
	译：ResourceTypeOrNameArgs指定b *Builder应接受参数的形式为`(<type1>[,<type2>,...]|<type> <name1>[,<name2>,...])`。
		当接收到一个参数时，将从服务器检索提供的types（并以逗号分隔）。
		当收到两个或多个参数时，它们必须是a single type and resource name(s)。
		allowEmptySelector 允许选择所有的资源（通过Everything func）。
*/
func (b *Builder) ResourceTypeOrNameArgs(allowEmptySelector bool, args ...string) *Builder {
	args = normalizeMultipleResourcesArgs(args)
	if ok, err := hasCombinedTypeArgs(args); ok {
		if err != nil {
			b.errs = append(b.errs, err)
			return b
		}
		for _, s := range args {
			tuple, ok, err := splitResourceTypeName(s)
			if err != nil {
				b.errs = append(b.errs, err)
				return b
			}
			if ok {
				b.resourceTuples = append(b.resourceTuples, tuple)
			}
		}
		return b
	}
	if len(args) > 0 {
		// Try replacing aliases only in types
		args[0] = b.ReplaceAliases(args[0])
	}
	switch {
	case len(args) > 2:
		b.names = append(b.names, args[1:]...)
		b.ResourceTypes(SplitResourceArgument(args[0])...)
	case len(args) == 2:
		b.names = append(b.names, args[1])
		b.ResourceTypes(SplitResourceArgument(args[0])...)
	case len(args) == 1:
		b.ResourceTypes(SplitResourceArgument(args[0])...)
		if b.selector == nil && allowEmptySelector {
			//allowEmptySelector默认值是true
			b.selector = labels.Everything()
		}
	case len(args) == 0:
	default:
		b.errs = append(b.errs, fmt.Errorf("arguments must consist of a resource or a resource and name"))
	}
	return b
}

// ContinueOnError will attempt to load and visit as many objects as possible, even if some visits
// return errors or some objects cannot be loaded. The default behavior is to terminate after
// the first error is returned from a VisitorFunc.
/*
	ContinueOnError将尝试加载并访问尽可能多的对象，即使某些访问返回错误或某些对象无法加载。
	默认行为是在 ‘VisitorFunc返回第一个错误之后’ 终止。
*/
func (b *Builder) ContinueOnError() *Builder {
	b.continueOnError = true
	return b
}

// Latest will fetch the latest copy of any objects loaded from URLs or files from the server.
/*
	译：func (b *Builder) Latest() 将从server端获取该URL或文件加载objects的最新副本。
*/
func (b *Builder) Latest() *Builder {
	b.latest = true
	return b
}

// Flatten will convert any objects with a field named "Items" that is an array of runtime.Object
// compatible types into individual entries and give them their own items. The original object
// is not passed to any visitors.
/*
	译：Flatten将使用一个名为“Items”的字段将任何对象转换为一个runtime.Object兼容类型的数组，并将它们分配给各自的items。
		 原始对象不会传递给任何访问者。
*/
func (b *Builder) Flatten() *Builder {
	b.flatten = true
	return b
}

// Do returns a Result object with a Visitor for the resources identified by the Builder.
// The visitor will respect the error behavior specified by ContinueOnError. Note that stream
// inputs are consumed by the first execution - use Infos() or Object() on the Result to capture a list
// for further iteration.
/*
	译：func (b *Builder) Do() 返回一个type Result struct，
		该type Result struct中含有一个visitor Visitor，
		visitor 能访问在Builder中定义的resources。

	   The visitor将遵守由ContinueOnError指定的错误行为。
      这里有一个生产-消费模型****
*/
func (b *Builder) Do() *Result {
	r := b.visitorResult()
	if r.err != nil {
		return r
	}
	if b.flatten {
		r.visitor = NewFlattenListVisitor(r.visitor, b.mapper)
	}
	helpers := []VisitorFunc{}
	if b.defaultNamespace {
		helpers = append(helpers, SetNamespace(b.namespace))
	}
	if b.requireNamespace {
		helpers = append(helpers, RequireNamespace(b.namespace))
	}
	helpers = append(helpers, FilterNamespace)
	if b.requireObject {
		helpers = append(helpers, RetrieveLazy)
	}
	r.visitor = NewDecoratedVisitor(r.visitor, helpers...)
	if b.continueOnError {
		r.visitor = ContinueOnErrorVisitor{r.visitor}
	}
	return r
}
```
前面的几个方法都是设置对应的属性值，最后的`func (b *Builder) Do() *Result`比较重要。

### func Do详解

	
	