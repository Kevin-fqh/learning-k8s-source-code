# Builder

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [First Blood](#first-blood)
  - [func NewBuilder](#func-newbuilder)
  - [Builder提供的方法](#builder提供的方法)
	- [func Do详解](#func-do详解)
	- [Visitor](#visitor)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

承接kubectl系列(1)中的func RunGet运行过程，现在开始了解Builder。

## First Blood
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
首先查看其返回值的定义，可以发现：type Result struct里面包含了一个Visitor和Info
```go
// Result contains helper methods for dealing with the outcome of a Builder.
/*
	译：type Result struct 包含了helper method，用于处理一个Builder的outcome
*/
type Result struct {
	err     error
	visitor Visitor

	sources  []Visitor
	singular bool

	ignoreErrors []utilerrors.Matcher

	// populated by a call to Infos
	//通过func (r *Result) Infos()来填充
	info []*Info
}

// Visitor lets clients walk a list of resources.
/*
	译：type Visitor interface可以让clients列出a list of resources。
*/
type Visitor interface {
	Visit(VisitorFunc) error
}

// VisitorFunc implements the Visitor interface for a matching function.
// If there was a problem walking a list of resources, the incoming error
// will describe the problem and the function can decide how to handle that error.
// A nil returned indicates to accept an error to continue loops even when errors happen.
// This is useful for ignoring certain kinds of errors or aggregating errors in some way.
/*
	译：VisitorFunc实现了matche功能的Visitor接口。
		如果在list resources的过程中出现问题，入参error将描述出现的问题，该函数可以决定如何处理该错误。
		无返回表示即使发生错误也接受错误，以继续执行循环。
		这对于忽略某些类型的错误或以某种方式聚合错误是有用的。
		
	VisitorFunc()可以对Info及Error进行处理
*/
type VisitorFunc func(*Info, error) error

// Info contains temporary info to execute a REST call, or show the results
// of an already completed REST call.
/*
	type Info struct包含执行REST调用的临时信息，或显示已完成的REST调用的结果。
*/
type Info struct {
	Client    RESTClient
	Mapping   *meta.RESTMapping
	Namespace string
	Name      string

	// Optional, Source is the filename or URL to template file (.json or .yaml),
	// or stdin to use to handle the resource
	/*
		译：可选，Source是模板文件（.json或.yaml）的文件名或URL，或用于处理资源的stdin
	*/
	Source string
	// Optional, this is the provided object in a versioned type before defaulting
	// and conversions into its corresponding internal type. This is useful for
	// reflecting on user intent which may be lost after defaulting and conversions.
	/*
		译：可选，这是一个version中的object，会有一个对应的internal type。
			这将有利于在把该object转为为之后对应的internal type之后，反应出用户的意图，
	*/
	VersionedObject interface{}
	// Optional, this is the most recent value returned by the server if available
	/*
		译：可选，这是服务器返回的最新值（如果可用）
	*/
	runtime.Object
	// Optional, this is the most recent resource version the server knows about for
	// this type of resource. It may not match the resource version of the object,
	// but if set it should be equal to or newer than the resource version of the
	// object (however the server defines resource version).
	/*
		译：可选，这是server端知道的此类resource的最新resource version。
			它可能与该object的resource version 不匹配，
			但如果设置它应该等于或新于对象的资源版本（但服务器定义资源版本）。

		简单来说，ResourceVersion的值是etcd中全局最新的Index
	*/
	ResourceVersion string
	// Optional, should this resource be exported, stripped of cluster-specific and instance specific fields
	Export bool
}
```

其后，查看r := b.visitorResult()，可以看出其返回值是一个type Result struct指针。
根据前面设置的参数值（或者说是命令行cmd的参数）来选择相应的Visitor
```go
func (b *Builder) visitorResult() *Result {
	if len(b.errs) > 0 {
		return &Result{err: utilerrors.NewAggregate(b.errs)}
	}

	if b.selectAll {
		b.selector = labels.Everything()
	}

	/*
		对于kubectl get node 而言，
		b.selectAll:  false
		b.selector:  []
		b.paths:  []
		b.resourceTuples :  []
		b.names:  []
		b.resources:  [nodes]

		对于kubectl get all而言
		b.selector:  []
		b.resources:  [pods replicationcontrollers services statefulsets horizontalpodautoscalers jobs deployments replicasets]
	*/
	/*
		根据b *Builder的属性值，生成对应的的Result
		只要有一个满足，就return
	*/
	// visit items specified by paths
	if len(b.paths) != 0 {
		/*
			指定yaml文件路径时走这个，kubectl create -f rc.yaml的时候走这里
		*/
		return b.visitByPaths()
	}

	// visit selectors
	if b.selector != nil {
		/*
			kubectl get node、 kubectl get all、kubectl get pod走这里
		*/
		return b.visitBySelector()
	}

	// visit items specified by resource and name
	if len(b.resourceTuples) != 0 {
		return b.visitByResource()
	}

	// visit items specified by name
	if len(b.names) != 0 {
		/*
			kubectl get pod tomcat7-xmv03 走这里
		*/
		return b.visitByName()
	}

	if len(b.resources) != 0 {
		return &Result{err: fmt.Errorf("resource(s) were provided, but no name, label selector, or --all flag specified")}
	}
	return &Result{err: missingResourceError}
}
```
综上，可以看出Do()函数主要的根据Builder设置的属性值来获取一个type Result struct。
type Result struct中最重要的数据结构是visitor Visitor和info []*Info。
然后在func RunGet 函数中可以看到调用了`infos, err := r.Infos()`
```go
// Infos returns an array of all of the resource infos retrieved via traversal.
// Will attempt to traverse the entire set of visitors only once, and will return
// a cached list on subsequent calls.
/*
	译：func (r *Result) Infos()以数组的形式返回所有的resource infos。
		尝试遍历整个visitors一次，然后在后续的调用中将返回一个cached list。

	func (r *Result) Infos()会执行visitor的Visit方法，获取到命令行从ApiServer端获取到的数据，
	该数据存储在func(info *Info, err error)中的info *Info中。
	一个info表示了一个对象
	一个个info添加到infos中，return回去
*/
func (r *Result) Infos() ([]*Info, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.info != nil {
		return r.info, nil
	}

	infos := []*Info{}
	/*
		一个个info添加到infos中
	*/
	err := r.visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		fmt.Println("a info's Name is:", info.Name)
		fmt.Println("a info's Object ;s type is:", reflect.TypeOf(info.Object))
		fmt.Println("a info's Object 's value is:", reflect.ValueOf(info.Object))
		fmt.Println("a info's ResourceVersion is:", info.ResourceVersion)
		fmt.Println("a info's Export is:", info.Export)
		fmt.Println("a info's VersionedObject TypeOf is:", reflect.TypeOf(info.VersionedObject))
		fmt.Println("a info's VersionedObject ValueOf is:", reflect.ValueOf(info.VersionedObject))
		infos = append(infos, info)
		return nil
	})
	err = utilerrors.FilterOut(err, r.ignoreErrors...)

	r.info, r.err = infos, err
	return infos, err
}
```
所以，Builder通过Result中的Visitor从ApiServer获取到对应的信息，进行一些过滤后，得到要输出的信息。
把该信息存储到`infos, err := r.Infos()`中。
这里的`infos`就是平时执行命令行`kubectl get.......`得到的结果，只不过还没有进行相应的格式化处理而已。

### Visitor
在上面已经介绍了type Result struct和type Visitor interface的定义，那么我们现在来看看一个实体化Result中的的Visitor是怎么样的？
Visitor是在生成对应Result的时候生成的。
以visitBySelector为例子：
```go
func (b *Builder) visitBySelector() *Result {
	if len(b.names) != 0 {
		return &Result{err: fmt.Errorf("name cannot be provided when a selector is specified")}
	}
	if len(b.resourceTuples) != 0 {
		return &Result{err: fmt.Errorf("selectors and the all flag cannot be used when passing resource/name arguments")}
	}
	if len(b.resources) == 0 {
		return &Result{err: fmt.Errorf("at least one resource must be specified to use a selector")}
	}
	/*
		获取mapping
	*/
	mappings, err := b.resourceMappings()
	if err != nil {
		return &Result{err: err}
	}

	/*
		针对每一个mapping生成一个type Selector struct（实现了接口Visitor）
		全部append到切片visitors中
	*/
	visitors := []Visitor{}
	for _, mapping := range mappings {
		client, err := b.mapper.ClientForMapping(mapping)
		if err != nil {
			return &Result{err: err}
		}
		selectorNamespace := b.namespace
		if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
			selectorNamespace = ""
		}
		/*
			NewSelector重点函数
		*/
		visitors = append(visitors, NewSelector(client, mapping, selectorNamespace, b.selector, b.export))
	}
	/*
		生成Result
		这里有EagerVisitorList(visitors)和VisitorList(visitors)，实现了类型转换
		其中type EagerVisitorList和type VisitorList 和Visitor 都是实现了Visitor接口的

		一般情况下b.continueOnError会是true
	*/
	if b.continueOnError {
		return &Result{visitor: EagerVisitorList(visitors), sources: visitors}
	}
	return &Result{visitor: VisitorList(visitors), sources: visitors}
}
```
这里面有个比较重要的函数NewSelector，定义在/pkg/kubectl/resource/selector.go
```go
// NewSelector creates a resource selector which hides details of getting items by their label selector.
/*
	译：NewSelector创建一个资源选择器，它会隐藏由标签选择器获取项目的细节。
*/
func NewSelector(client RESTClient, mapping *meta.RESTMapping, namespace string, selector labels.Selector, export bool) *Selector {
	return &Selector{
		Client:    client,
		Mapping:   mapping,
		Namespace: namespace,
		Selector:  selector,
		Export:    export,
	}
}

// Selector is a Visitor for resources that match a label selector.
/*
	译：Selector是一个resources的Visitor，实现了label selector

	type Selector struct实现了/pkg/kubectl/resource/visitor.go中的type Visitor interface
*/
type Selector struct {
	Client    RESTClient
	Mapping   *meta.RESTMapping
	Namespace string
	Selector  labels.Selector
	Export    bool
}
```
查看type Selector struct的visit函数，从其返回值可以得知上面所说的命令行cmd从server端获取到结果存储在info 中。
```go
// Visit implements Visitor
func (r *Selector) Visit(fn VisitorFunc) error {
	......
	......
	return fn(info, nil)
}
```
也就是说type Selector struct 是一个type Visitor interface。
	
# 总结
至此，Builder、type Result struct和visitor的介绍已经结束。总结起来就是：
Builder的Do()返回一个type Result struct，
该type Result struct中含有一个visitor Visitor，
visitor 能访问在Builder中定义的resources。
最后通过type Result struct的Infos()方法把命令行如`kubectl get po`的结果取出来存储到type Info struct中。
下一篇文章将对种类繁多的Visitor进行进一步的解析。