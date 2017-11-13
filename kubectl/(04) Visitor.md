# Visitor

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [func visitorResult](#func-visitorresult)
	- [func visitBySelector](#func-visitbyselector)
  - [NewFlattenListVisitor](#newflattenlistvisitor)
  - [设置VisitorFunc](#设置visitorfunc)
  - [NewDecoratedVisitor](#newdecoratedvisitor)
  - [ContinueOnErrorVisitor](#continueonerrorvisitor)
  - [总结](#总结)
  - [Daemon-1](#daemon-1)
  - [Daemon-2](#daemon-2)
  - [各个Visitor汇总](#各个visitor汇总)
	- [StreamVisitor](#streamvisitor)
	- [FileVisitor](#filevisitor)
	- [URLVisitor](#urlvisitor)
	- [Selector](#selector)
	- [FilteredVisitor](#filteredvisitor)
	- [DecoratedVisitor](#decoratedvisitor)
	- [ContinueOnErrorVisitor](#type-continueonerrorvisitor-struct)
	- [FlattenListVisitor](#flattenlistvisitor)
	- [EagerVisitorList](#eagervisitorlist)
	- [VisitorList](#visitorlist)
	- [Info](#info)

<!-- END MUNGE: GENERATED_TOC -->

实现Visitor接口的结构体有很多，对其进行详细的介绍很有必要。
- 产生info的visitor有：FileVisitor, StreamVisitor, URLVisitor, Selector。
- 处理info的visitor有：VisitorList, EagerVisitorList, DecoratedVisitor, ContinueOnErrorVisitor, FlattenListVisitor, FilteredVisitor等

Builder中的Do()函数返回一个Result，而Visitor是Result里面最重要的数据结构。
那么Visitor的特性是什么？功能是什么？我们从Builder中的Do()函数出发，开始解析。
可以把func (b *Builder) Do()的主要步骤概括为下面5步:
```go
func (b *Builder) Do() *Result {
	r := b.visitorResult()
	
	if b.flatten {
		r.visitor = NewFlattenListVisitor(r.visitor, b.mapper)
	}
	
	helpers := []VisitorFunc{}
	append各种VisitorFunc
	
	r.visitor = NewDecoratedVisitor(r.visitor, helpers...)
	
	if b.continueOnError {
		r.visitor = ContinueOnErrorVisitor{r.visitor}
	}
	return r
}
```

## func visitorResult
Do()的第一行`r := b.visitorResult()`，定义在/pkg/kubectl/resource/builder.go。  
这里需要注意的一点是对于`kubectl get node`之类的命令而言，虽然`reflect.ValueOf(b.selector)`输出的结果显示为`[]`。
但此时`b.selector != nil`的结果为True。
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

### func visitBySelector
我们以kubectl get node为例子，对b.visitBySelector()进行解析。
```go
func (b *Builder) visitBySelector() *Result {
	......
	......
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
进一步查看函数func NewSelector，/pkg/kubectl/resource/selector.go。
根据mapping和namespace等属性生成对应的Selector。
```go
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

// NewSelector creates a resource selector which hides details of getting items by their label selector.
/*
	译：NewSelector创建一个资源选择器，它隐藏由标签选择器获取项目的细节。
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
```
查看Selector的Visit函数，关注其返回值是`fn(info, nil)`
```go
func (r *Selector) Visit(fn VisitorFunc) error {
	......
	......
	accessor := r.Mapping.MetadataAccessor
	resourceVersion, _ := accessor.ResourceVersion(list)
	info := &Info{
		Client:    r.Client,
		Mapping:   r.Mapping,
		Namespace: r.Namespace,

		Object:          list,
		ResourceVersion: resourceVersion,
	}
	return fn(info, nil)
}
```

#### 小结
可以发现实现了type Visitor interface的结构体有很多，只要实现了Visit(VisitorFunc) error函数的都是。  
主要包括：
- 产生info的visitor有：FileVisitor, StreamVisitor, URLVisitor, Selector。
- 处理info的visitor有：VisitorList, EagerVisitorList, DecoratedVisitor, ContinueOnErrorVisitor, FlattenListVisitor, FilteredVisitor等

其中在FileVisitor、StreamVisitor和URLVisitor在func (b *Builder) FilenameParam中会用到。
URLVisitor、FileVisitor里面都包含了一个StreamVisitor。

## NewFlattenListVisitor
回到Do()函数，也就是说此时`r := b.visitorResult()`得到值如下:
```go
visitors := []Visitor{}
visitors = append(visitors, NewSelector(client, mapping, selectorNamespace, b.selector, b.export))
&Result{visitor: EagerVisitorList(visitors), sources: visitors}
```
继续对Do()函数进行解析，对kubectl get node、kubectl get po而言，
```
b.flatten, b.defaultNamespace, b.requireNamespace, b.requireObject, b.continueOnError:  true true false true true
显示指定namespace的时候，b.requireNamespace会被置为true
```
```go
type FlattenListVisitor struct {
	Visitor
	*Mapper
}

// NewFlattenListVisitor creates a visitor that will expand list style runtime.Objects
// into individual items and then visit them individually.
/*
	译：NewFlattenListVisitor创建一个visitor，
		它将list样式的runtime.Objects扩展成单独的items，然后单独访问它们。
*/
func NewFlattenListVisitor(v Visitor, mapper *Mapper) Visitor {
	return FlattenListVisitor{v, mapper}
}
```
再看func (v FlattenListVisitor) Visit(fn VisitorFunc)的定义，
可以发现Visitor interface是层层嵌套的。

一个Visitor(父Visitor)结构体中包含另一个Visitor(子Visitor)。  
父Visitor中的Visit()方法会调用子Visitor的Visit()方法，并传入一个匿名的visitorFunc，这个匿名的visitorFunc在子Visitor看来就是fn。
```go
func (v FlattenListVisitor) Visit(fn VisitorFunc) error {
	/*
		调用FlattenListVisitor的Visitor的Visit方法
		嵌套使用Visitor interface
	*/
	return v.Visitor.Visit(func(info *Info, err error) error {
		...
		...
		if info.Object == nil {
			return fn(info, nil)
		}
		items, err := meta.ExtractList(info.Object)
		if err != nil {
			return fn(info, nil)
		}
		...
		...
	})
}
```

## 设置VisitorFunc
这里的VisitorFunc就是Visit(fn VisitorFunc)的参数。
包括四个VisitorFunc：SetNamespace、RequireNamespace、FilterNamespace、RetrieveLazy
```go
// SetNamespace ensures that every Info object visited will have a namespace
// set. If info.Object is set, it will be mutated as well.
/*
	译：SetNamespace确保访问的每个Info对象都将设置一个命名空间。
		如果设置了info.Object，它也将被突变。
*/
func SetNamespace(namespace string) VisitorFunc {
	return func(info *Info, err error) error {
		if err != nil {
			return err
		}
		if !info.Namespaced() {
			return nil
		}
		if len(info.Namespace) == 0 {
			info.Namespace = namespace
			UpdateObjectNamespace(info, nil)
		}
		return nil
	}
}

// RequireNamespace will either set a namespace if none is provided on the
// Info object, or if the namespace is set and does not match the provided
// value, returns an error. This is intended to guard against administrators
// accidentally operating on resources outside their namespace.
/*
	译：如果Info object中没有提供namespace，则func RequireNamespace将设置命名空间，
		或者如果命名空间已设置且与提供的值不匹配，则返回错误。
	   这是为了防止管理员意外地在其命名空间之外的资源上操作。
*/
func RequireNamespace(namespace string) VisitorFunc {
	return func(info *Info, err error) error {
		if err != nil {
			return err
		}
		if !info.Namespaced() {
			return nil
		}
		if len(info.Namespace) == 0 {
			info.Namespace = namespace
			UpdateObjectNamespace(info, nil)
			return nil
		}
		if info.Namespace != namespace {
			return fmt.Errorf("the namespace from the provided object %q does not match the namespace %q. You must pass '--namespace=%s' to perform this operation.", info.Namespace, namespace, info.Namespace)
		}
		return nil
	}
}

// FilterNamespace omits the namespace if the object is not namespace scoped
/*
	译：如果info object没有namespace这个属性，FilterNamespace将忽略命名空间。
		eg:node、pv资源
*/
func FilterNamespace(info *Info, err error) error {
	if err != nil {
		return err
	}
	if !info.Namespaced() {
		info.Namespace = ""
		UpdateObjectNamespace(info, nil)
	}
	return nil
}
```

## NewDecoratedVisitor
r.visitor = NewDecoratedVisitor(r.visitor, helpers...) 对r.visitor再进行一层封装，把上面的4个helper func封装进去。  
查看func (v DecoratedVisitor) Visit(fn VisitorFunc)，会发现DecoratedVisitor就是调用了这4个helper func对info进行处理。
```go
// DecoratedVisitor will invoke the decorators in order prior to invoking the visitor function
// passed to Visit. An error will terminate the visit.
/*
	译：在调用visitor function之前，DecoratedVisitor将调用decorators。
		错误将终止访问。
*/
type DecoratedVisitor struct {
	visitor    Visitor
	decorators []VisitorFunc
}

// NewDecoratedVisitor will create a visitor that invokes the provided visitor functions before
// the user supplied visitor function is invoked, giving them the opportunity to mutate the Info
// object or terminate early with an error.
/*
	译：NewDecoratedVisitor将在调用用户提供的visitor function之前，创建一个visitor来调用入参visitor functions，
		让他们有机会mutate Info对象或提前return error。
*/
func NewDecoratedVisitor(v Visitor, fn ...VisitorFunc) Visitor {
	if len(fn) == 0 {
		return v
	}
	return DecoratedVisitor{v, fn}
}

// Visit implements Visitor
func (v DecoratedVisitor) Visit(fn VisitorFunc) error {
	return v.visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		for i := range v.decorators {
			/*
				decorators []VisitorFunc对info对象进行mutate处理
			*/
			if err := v.decorators[i](info, nil); err != nil {
				return err
			}
		}
		return fn(info, nil)
	})
}
```

## ContinueOnErrorVisitor
再看看最后的r.visitor = ContinueOnErrorVisitor{r.visitor}。
```go
// ContinueOnErrorVisitor visits each item and, if an error occurs on
// any individual item, returns an aggregate error after all items
// are visited.
/*
	译：ContinueOnErrorVisitor访问每个item，如果任何一个item发生错误，则在访问所有item后返回一个聚合错误。
*/
type ContinueOnErrorVisitor struct {
	Visitor
}

// Visit returns nil if no error occurs during traversal, a regular
// error if one occurs, or if multiple errors occur, an aggregate
// error.  If the provided visitor fails on any individual item it
// will not prevent the remaining items from being visited. An error
// returned by the visitor directly may still result in some items
// not being visited.
/*
	译：如果遍历期间没有发生错误，func (v ContinueOnErrorVisitor) Visit返回nil。
		如果发生错误，或者发生多个错误，则返回聚合错误。
		如果指定的visitor在任何单独的item上失败，它不会阻止其余的item被访问。
		visitor直接返回error，可能会导致一个items没有被访问到。
*/
func (v ContinueOnErrorVisitor) Visit(fn VisitorFunc) error {
	errs := []error{}
	err := v.Visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if err := fn(info, nil); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return utilerrors.NewAggregate(errs)
}
```

## 总结
```go
type Visitor interface {
	Visit(VisitorFunc) error
}
type VisitorFunc func(*Info, error) error
```
只要实现了Visit(VisitorFunc) error方法的结构体都可以称为Visitor。  
重点了解Visitor的嵌套。可以通过下面的两个Daemon来进一步了解。
- 如果info已经生成，那么visitor嵌套中的visitor只要处理info即可；
- 如果还没有info，则最里面的visitor要在fn()调用之前生成info，以供其他visitor处理。

上面部分涉及到的Visitor是在kubectl get 命令过程中需要用到的Visitor种类。每一个命令可能用到的Visitor不是完全一样的。
在本文最后会对所有的Visitor来个汇总。

## Daemon-1
这个例子可以用于理解func (b *Builder) Do()中不断叠加嵌套几个Visitor。
```go
package main

import (
	"fmt"
)

type Visitor interface {
	Visit(VisitorFunc) error
}
type VisitorFunc func() error

type Visitor1 struct {
}

func (l Visitor1) Visit(fn VisitorFunc) error {
	fmt.Println("In Visitor1 before fn")
	fn()
	fmt.Println("In Visitor1 after fn")
	return nil
}

type Visitor2 struct {
	visitor Visitor
}

func (l Visitor2) Visit(fn VisitorFunc) error {
	return l.visitor.Visit(func() error {
		fmt.Println("In Visitor2 before fn")
		fn()
		fmt.Println("In Visitor2 after fn")
		return nil
	})
}

type Visitor3 struct {
	visitor Visitor
}

func (l Visitor3) Visit(fn VisitorFunc) error {
	return l.visitor.Visit(func() error {
		fmt.Println("In Visitor3 before fn")
		fn()
		fmt.Println("In Visitor3 after fn")
		return nil
	})
}
func main() {
	var visitor Visitor = new(Visitor1)
	visitor = Visitor2{visitor}
	visitor = Visitor3{visitor}
	visitor.Visit(func() error {
		fmt.Println("In visitFunc")
		return nil
	})
}
```
输出结果为
```
In Visitor1 before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In Visitor1 after fn
```
Daemon里面的嵌套关系为visitor = Visitor3{Visitor2{Visitor1}},
可以理解为Visitor3.Visitor＝Visitor2，Visitor2.Visitor＝Visitor1 。
 
那么main函数里面的visitor.Visit(fn)首先调用了func (l Visitor3) Visit(fn VisitorFunc)，
然后嵌套调用了func (l Visitor2) Visit(fn VisitorFunc)，同理推下去即可。

外边的Visitor的visitorFunc会嵌入到里边Visitor的fn处。

main函数visitor.Visit(fn)的调用参考/pkg/kubectl/resource/result.go中的func (r *Result) Infos() ([]*Info, error)中。

## Daemon-2
在func (b *Builder) visitBySelector()中有个遍历生成`visitors = append(visitors, NewSelector(client, mapping, selectorNamespace, b.selector, b.export))`，其中visitors := []Visitor{}。  
然后回到Do()函数里面，如`r.visitor = NewFlattenListVisitor(r.visitor, b.mapper)`，入参r.visitor正是上面循环遍历中生成的visitors。仿造该过程的Daemon如下所示。来看看这种情况下的函数调用关系是如何的？

来看Daemon
```go
package main

import (
	"fmt"
)

type Visitor interface {
	Visit(VisitorFunc) error
}
type VisitorFunc func() error
type VisitorList []Visitor // 参考/pkg/kubectl/resource/visitor.go中的type EagerVisitorList []Visitor

func (l VisitorList) Visit(fn VisitorFunc) error {
	for i := range l {
		if err := l[i].Visit(func() error {
			fmt.Println("In VisitorList before fn")
			fn()
			fmt.Println("In VisitorList after fn")
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

type Visitor1 struct {
	a int
}

func (l Visitor1) Visit(fn VisitorFunc) error {
	fmt.Println("In Visitor1 before fn-", l.a)
	fn()
	fmt.Println("In Visitor1 after fn-", l.a)
	return nil
}

type Visitor2 struct {
	visitor Visitor
}

func (l Visitor2) Visit(fn VisitorFunc) error {
	fmt.Println("In Visitor2 before Visit")
	l.visitor.Visit(func() error {
		fmt.Println("In Visitor2 before fn")
		fn()
		fmt.Println("In Visitor2 after fn")
		return nil
	})
	fmt.Println("In Visitor2 before Visit")
	return nil
}

type Visitor3 struct {
	visitor Visitor
}

func (l Visitor3) Visit(fn VisitorFunc) error {
	fmt.Println("In Visitor3 before Visit")
	l.visitor.Visit(func() error {
		fmt.Println("In Visitor3 before fn")
		fn()
		fmt.Println("In Visitor3 after fn")
		return nil
	})
	fmt.Println("In Visitor3 after Visit")
	return nil
}
func main() {
	var visitors []Visitor

	var visitor Visitor = Visitor1{a: 1}
	visitors = append(visitors, visitor)

	visitor = Visitor1{a: 2}
	visitors = append(visitors, visitor)

	visitor = Visitor2{VisitorList(visitors)}
	visitor = Visitor3{visitor}
	visitor.Visit(func() error {
		fmt.Println("In visitFunc")
		return nil
	})
}
```
输出结果为
```
In Visitor3 before Visit
In Visitor2 before Visit

In Visitor1 before fn- 1
In VisitorList before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In VisitorList after fn
In Visitor1 after fn- 1

In Visitor1 before fn- 2
In VisitorList before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In VisitorList after fn
In Visitor1 after fn- 2

In Visitor2 before Visit
In Visitor3 after Visit
```
若main函数增加一个visitor = Visitor1{a: 3}
```go
func main() {
	var visitors []Visitor

	var visitor Visitor = Visitor1{a: 1}
	visitors = append(visitors, visitor)

	visitor = Visitor1{a: 2}
	visitors = append(visitors, visitor)

	visitor = Visitor1{a: 3}
	visitors = append(visitors, visitor)

	visitor = Visitor2{VisitorList(visitors)}
	visitor = Visitor3{visitor}
	visitor.Visit(func() error {
		fmt.Println("In visitFunc")
		return nil
	})
}
```
输出如下
```
In Visitor3 before Visit
In Visitor2 before Visit

In Visitor1 before fn- 1
In VisitorList before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In VisitorList after fn
In Visitor1 after fn- 1

In Visitor1 before fn- 2
In VisitorList before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In VisitorList after fn
In Visitor1 after fn- 2

In Visitor1 before fn- 3
In VisitorList before fn
In Visitor2 before fn
In Visitor3 before fn
In visitFunc
In Visitor3 after fn
In Visitor2 after fn
In VisitorList after fn
In Visitor1 after fn- 3

In Visitor2 before Visit
In Visitor3 after Visit
```

## 各个Visitor汇总
前面提到有多种Visitor，现在来总结一下。
- StreamVisitor、FileVisitor、URLVisitor、Selector 生成info信息，其中FileVisitor、URLVisitor封装了StreamVisitor
- FilteredVisitor、DecoratedVisitor、ContinueOnErrorVisitor、FlattenListVisitor都是封装一个visitor Visitor，对info信息或者err进行处理
- EagerVisitorList、VisitorList都是[]Visitor类型，对info信息或者err进行处理
- type Info struct, info是用来存储REST请求的返回结果的

如果info已经生成，那么visitor嵌套中的visitor只要处理info即可；  
如果没有info，则最里面的visitor要在fn()调用之前生成info，以供其他visitor处理。

### StreamVisitor
```go
// StreamVisitor reads objects from an io.Reader and walks them. A stream visitor can only be
// visited once.
// TODO: depends on objects being in JSON format before being passed to decode - need to implement
// a stream decoder method on runtime.Codec to properly handle this.
/*
	译：StreamVisitor从io.reader中获取数据流。一个stream visitor只能被访问一次。
*/
type StreamVisitor struct {
	io.Reader
	*Mapper

	Source string
	Schema validation.Schema
}

// NewStreamVisitor is a helper function that is useful when we want to change the fields of the struct but keep calls the same.
/*
	译：在我们要更改struct的字段，但是希望保持调用方法一样的时候，使用func NewStreamVisitor
*/
func NewStreamVisitor(r io.Reader, mapper *Mapper, source string, schema validation.Schema) *StreamVisitor {
	return &StreamVisitor{
		Reader: r,
		Mapper: mapper,
		Source: source,
		Schema: schema,
	}
}

// Visit implements Visitor over a stream. StreamVisitor is able to distinct multiple resources in one stream.
/*
	译：StreamVisitor能够在一个流中区分多个resources。
*/
func (v *StreamVisitor) Visit(fn VisitorFunc) error {
	/*
		使用NewYAMLOrJSONDecoder生成一个Decoder，把指定YAML文档或JSON文档作为一个stream来进行处理
			==>定义在/pkg/util/yaml/decoder.go中
				==>func NewYAMLOrJSONDecoder(r io.Reader, bufferSize int) *YAMLOrJSONDecoder
	*/
	d := yaml.NewYAMLOrJSONDecoder(v.Reader, 4096)
	for {
		ext := runtime.RawExtension{}
		/*
			用解码器对stream进行解析
		*/
		if err := d.Decode(&ext); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		// TODO: This needs to be able to handle object in other encodings and schemas.
		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}
		/*
			进行schema检查
		*/
		if err := ValidateSchema(ext.Raw, v.Schema); err != nil {
			return fmt.Errorf("error validating %q: %v", v.Source, err)
		}
		/*
			InfoForData用传入的数据生成一个Info object。
			会把json对象转换成对应的struct类型
				==>/pkg/kubectl/resource/mapper.go
					==>func (m *Mapper) InfoForData(data []byte, source string) (*Info, error)
		*/
		info, err := v.InfoForData(ext.Raw, v.Source)
		if err != nil {
			if fnErr := fn(info, err); fnErr != nil {
				return fnErr
			}
			continue
		}
		if err := fn(info, nil); err != nil {
			return err
		}
	}
}
```

### FileVisitor
```go
// FileVisitor is wrapping around a StreamVisitor, to handle open/close files
/*
	FileVisitor封装了一个type StreamVisitor struct，用于处理open/close files
*/
type FileVisitor struct {
	Path string
	*StreamVisitor
}

// Visit in a FileVisitor is just taking care of opening/closing files
func (v *FileVisitor) Visit(fn VisitorFunc) error {
	var f *os.File
	if v.Path == constSTDINstr {
		f = os.Stdin
	} else {
		var err error
		if f, err = os.Open(v.Path); err != nil {
			return err
		}
	}
	defer f.Close()
	v.StreamVisitor.Reader = f

	return v.StreamVisitor.Visit(fn)
}
```
### URLVisitor
```go
// URLVisitor downloads the contents of a URL, and if successful, returns
// an info object representing the downloaded object.
/*
	译：URLVisitor下载URL的内容，如果成功，返回一个表示info object代表URL的信息。
	封装了一个StreamVisitor
*/
type URLVisitor struct {
	URL *url.URL
	*StreamVisitor
	HttpAttemptCount int
}

func (v *URLVisitor) Visit(fn VisitorFunc) error {
	body, err := readHttpWithRetries(httpgetImpl, time.Second, v.URL.String(), v.HttpAttemptCount)
	if err != nil {
		return err
	}
	defer body.Close()
	v.StreamVisitor.Reader = body
	return v.StreamVisitor.Visit(fn)
}
```
### Selector
```go
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

// NewSelector creates a resource selector which hides details of getting items by their label selector.
/*
	译：NewSelector创建一个资源选择器，它隐藏由标签选择器获取项目的细节。
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

// Visit implements Visitor
func (r *Selector) Visit(fn VisitorFunc) error {
	list, err := NewHelper(r.Client, r.Mapping).List(r.Namespace, r.ResourceMapping().GroupVersionKind.GroupVersion().String(), r.Selector, r.Export)
	if err != nil {
		if errors.IsBadRequest(err) || errors.IsNotFound(err) {
			if se, ok := err.(*errors.StatusError); ok {
				// modify the message without hiding this is an API error
				if r.Selector.Empty() {
					se.ErrStatus.Message = fmt.Sprintf("Unable to list %q: %v", r.Mapping.Resource, se.ErrStatus.Message)
				} else {
					se.ErrStatus.Message = fmt.Sprintf("Unable to find %q that match the selector %q: %v", r.Mapping.Resource, r.Selector, se.ErrStatus.Message)
				}
				return se
			}
			if r.Selector.Empty() {
				return fmt.Errorf("Unable to list %q: %v", r.Mapping.Resource, err)
			} else {
				return fmt.Errorf("Unable to find %q that match the selector %q: %v", r.Mapping.Resource, r.Selector, err)
			}
		}
		return err
	}
	accessor := r.Mapping.MetadataAccessor
	resourceVersion, _ := accessor.ResourceVersion(list)
	info := &Info{
		Client:    r.Client,
		Mapping:   r.Mapping,
		Namespace: r.Namespace,

		Object:          list,
		ResourceVersion: resourceVersion,
	}
	return fn(info, nil)
}
```

### FilteredVisitor
```go
type FilterFunc func(info *Info, err error) (bool, error)

/*
	FilteredVisitor可以检查info是否满足某些条件。如果满足条件，则往下执行，否则返回err
*/
type FilteredVisitor struct {
	visitor Visitor
	filters []FilterFunc
}

func NewFilteredVisitor(v Visitor, fn ...FilterFunc) Visitor {
	if len(fn) == 0 {
		return v
	}
	return FilteredVisitor{v, fn}
}

func (v FilteredVisitor) Visit(fn VisitorFunc) error {
	return v.visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		for _, filter := range v.filters {
			ok, err := filter(info, nil)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		return fn(info, nil)
	})
}
```
### DecoratedVisitor
```go
// DecoratedVisitor will invoke the decorators in order prior to invoking the visitor function
// passed to Visit. An error will terminate the visit.
/*
	译：在调用visitor function之前，DecoratedVisitor将调用decorators。
		错误将终止visit函数。
*/
type DecoratedVisitor struct {
	visitor    Visitor
	decorators []VisitorFunc
}

// NewDecoratedVisitor will create a visitor that invokes the provided visitor functions before
// the user supplied visitor function is invoked, giving them the opportunity to mutate the Info
// object or terminate early with an error.
/*
	译：NewDecoratedVisitor将在调用用户提供的visitor function之前，创建一个visitor来调用入参visitor functions，
		让他们有机会改变 Info对象或提前return error。
*/
func NewDecoratedVisitor(v Visitor, fn ...VisitorFunc) Visitor {
	if len(fn) == 0 {
		return v
	}
	return DecoratedVisitor{v, fn}
}

// Visit implements Visitor
func (v DecoratedVisitor) Visit(fn VisitorFunc) error {
	return v.visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		for i := range v.decorators {
			/*
				decorators []VisitorFunc对info对象进行处理
			*/
			if err := v.decorators[i](info, nil); err != nil {
				return err
			}
		}
		return fn(info, nil)
	})
}
```
### type ContinueOnErrorVisitor struct   
```go
// ContinueOnErrorVisitor visits each item and, if an error occurs on
// any individual item, returns an aggregate error after all items
// are visited.
/*
	译：ContinueOnErrorVisitor访问每个item，如果任何一个item发生错误，则在访问所有item后返回一个聚合错误。
*/
type ContinueOnErrorVisitor struct {
	Visitor
}

// Visit returns nil if no error occurs during traversal, a regular
// error if one occurs, or if multiple errors occur, an aggregate
// error.  If the provided visitor fails on any individual item it
// will not prevent the remaining items from being visited. An error
// returned by the visitor directly may still result in some items
// not being visited.
/*
	译：如果遍历期间没有发生错误，func (v ContinueOnErrorVisitor) Visit返回nil。
		如果发生错误，或者发生多个错误，则返回聚合错误。
		如果指定的visitor在任何单独的item上失败，它不会阻止其余的item被访问。
		visitor直接返回error，可能会导致一个items没有被访问到。
	收集子Visitor产生的错误，并返回
*/
func (v ContinueOnErrorVisitor) Visit(fn VisitorFunc) error {
	errs := []error{}
	err := v.Visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if err := fn(info, nil); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return utilerrors.NewAggregate(errs)
}
```
### FlattenListVisitor
```go
/*
	FlattenListVisitor将任何runtime.ExtractList转化为一个list－拥有一个公共字段"Items"。
	"Items" 是一个runtime.Objects切片
	任何子item的错误（例如，如果列表中包含没有注册的客户端或资源的对象）将终止FlattenListVisitor的visit函数。
*/
type FlattenListVisitor struct {
	Visitor
	*Mapper
}

// NewFlattenListVisitor creates a visitor that will expand list style runtime.Objects
// into individual items and then visit them individually.
/*
	译：NewFlattenListVisitor创建一个visitor，
		它将list样式的runtime.Objects扩展成单独的items，然后单独访问它们。
*/
func NewFlattenListVisitor(v Visitor, mapper *Mapper) Visitor {
	return FlattenListVisitor{v, mapper}
}

func (v FlattenListVisitor) Visit(fn VisitorFunc) error {
	return v.Visitor.Visit(func(info *Info, err error) error {
		if err != nil {
			return err
		}
		if info.Object == nil {
			return fn(info, nil)
		}
		/*
			从info.Object中提取列表items
		*/
		items, err := meta.ExtractList(info.Object)
		if err != nil {
			return fn(info, nil)
		}
		if errs := runtime.DecodeList(items, struct {
			runtime.ObjectTyper
			runtime.Decoder
		}{v.Mapper, v.Mapper.Decoder}); len(errs) > 0 {
			return utilerrors.NewAggregate(errs)
		}

		// If we have a GroupVersionKind on the list, prioritize that when asking for info on the objects contained in the list
		var preferredGVKs []unversioned.GroupVersionKind
		if info.Mapping != nil && !info.Mapping.GroupVersionKind.Empty() {
			preferredGVKs = append(preferredGVKs, info.Mapping.GroupVersionKind)
		}

		/*
			遍历上面刚提取出来的items
		*/
		for i := range items {
			item, err := v.InfoForObject(items[i], preferredGVKs)
			if err != nil {
				return err
			}
			if len(info.ResourceVersion) != 0 {
				item.ResourceVersion = info.ResourceVersion
			}
			if err := fn(item, nil); err != nil {
				return err
			}
		}
		return nil
	})
}
```

### EagerVisitorList
```go
// EagerVisitorList implements Visit for the sub visitors it contains. All errors
// will be captured and returned at the end of iteration.
/*
	译：EagerVisitorList 实现其包含的子Visitor的Visit方法。
		在遍历其子Visitor的过程中，所有的error会被收集起来，在迭代结束后一起return
*/
type EagerVisitorList []Visitor

// Visit implements Visitor, and gathers errors that occur during processing until
// all sub visitors have been visited.
func (l EagerVisitorList) Visit(fn VisitorFunc) error {
	errs := []error(nil)
	for i := range l {
		if err := l[i].Visit(func(info *Info, err error) error {
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			if err := fn(info, nil); err != nil {
				errs = append(errs, err)
			}
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
```
### VisitorList
```go
// VisitorList implements Visit for the sub visitors it contains. The first error
// returned from a child Visitor will terminate iteration.
/*
	译：VisitorList 实现其包含的子Visitor的Visit方法。
		在遍历其子Visitor的过程中，只要出现error，VisitorList的Visit立刻return
*/
type VisitorList []Visitor

// Visit implements Visitor
func (l VisitorList) Visit(fn VisitorFunc) error {
	for i := range l {
		if err := l[i].Visit(fn); err != nil {
			return err
		}
	}
	return nil
}
```

### Info
```go
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

// NewInfo returns a new info object
func NewInfo(client RESTClient, mapping *meta.RESTMapping, namespace, name string, export bool) *Info {
	return &Info{
		Client:    client,
		Mapping:   mapping,
		Namespace: namespace,
		Name:      name,
		Export:    export,
	}
}

// Visit implements Visitor
func (i *Info) Visit(fn VisitorFunc) error {
	return fn(i, nil)
}
```
	
