# Convertor的使用

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [FieldMatchingFlags](#fieldmatchingflags)
  - [Converter](#converter)
  - [Demo－byteSlice](#demo－byteslice)
  - [Demo－MismatchedTypes](#demo－mismatchedtypes)
  - [Demo-DefaultConvert](#demo-defaultconvert)
  - [Demo-DeepCopy](#demo-deepcopy)
<!-- END MUNGE: GENERATED_TOC -->

Scheme中用到了Convertor，本文展示一些简单Demo来进行用法说明。
## FieldMatchingFlags
声明转换模式，FieldMatchingFlags表示了哪些struct中的字段可以被copy, 可以用“｜”来设置多个。
```go
// FieldMatchingFlags contains a list of ways in which struct fields could be
// copied. These constants may be | combined.

type FieldMatchingFlags int

const (
	// Loop through destination fields, search for matching source
	// field to copy it from. Source fields with no corresponding
	// destination field will be ignored. If SourceToDest is
	// specified, this flag is ignored. If neither is specified,
	// or no flags are passed, this flag is the default.
	/*
		通过循环destination的fields，搜索匹配的source fields以将其复制。
		没有相应destination字段的source字段将被忽略。
		如果指定了SourceToDest模式，则忽略此模式。
		如果没有显式设置模式，DestFromSource是默认的模式。
	*/
	DestFromSource FieldMatchingFlags = 0 //0
	// Loop through source fields, search for matching dest field
	// to copy it into. Destination fields with no corresponding
	// source field will be ignored.
	/*
		对source字段进行循环
	*/
	SourceToDest FieldMatchingFlags = 1 << iota //2
	// Don't treat it as an error if the corresponding source or
	// dest field can't be found.
	IgnoreMissingFields //4
	// Don't require type names to match.
	AllowDifferentFieldTypeNames //8
)
```
## Converter
Converter knows how to convert one type to another.
- type Converter struct
```go
type Converter struct {
	// Map from the conversion pair to a function which can
	// do the conversion.
	conversionFuncs          ConversionFuncs
	generatedConversionFuncs ConversionFuncs

	// genericConversions are called during normal conversion to offer a "fast-path"
	// that avoids all reflection. These methods are not called outside of the .Convert()
	// method.
	genericConversions []GenericConversionFunc

	// Set of conversions that should be treated as a no-op
	ignoredConversions map[typePair]struct{}

	// This is a map from a source field type and name, to a list of destination
	// field type and name.
	structFieldDests map[typeNamePair][]typeNamePair

	// Allows for the opposite lookup of structFieldDests. So that SourceFromDest
	// copy flag also works. So this is a map of destination field name, to potential
	// source field name and type to look for.
	structFieldSources map[typeNamePair][]typeNamePair

	// Map from a type to a function which applies defaults.
	defaultingFuncs map[reflect.Type]reflect.Value

	// Similar to above, but function is stored as interface{}.
	defaultingInterfaces map[reflect.Type]interface{}

	// Map from an input type to a function which can apply a key name mapping
	inputFieldMappingFuncs map[reflect.Type]FieldMappingFunc

	// Map from an input type to a set of default conversion flags.
	inputDefaultFlags map[reflect.Type]FieldMatchingFlags

	// If non-nil, will be called to print helpful debugging info. Quite verbose.
	Debug DebugLogger

	// nameFunc is called to retrieve the name of a type; this name is used for the
	// purpose of deciding whether two types match or not (i.e., will we attempt to
	// do a conversion). The default returns the go type name.
	/*
		调用nameFunc来检索类型的名称;
		该名称用于确定两种类型是否匹配（我们将尝试进行转换）。
		默认值返回go类型名称。
		New一个类型的Converter，要显示声明该属性，或者使用DefaultNameFunc。
	*/
	nameFunc func(t reflect.Type) string
}

var DefaultNameFunc = func(t reflect.Type) string { return t.Name() }
```

- func (c *Converter) Convert  
完成两个类型变量之间的转换，如果两个类型不相同，那么必须事先注册转换函数
```go
// Convert will translate src to dest if it knows how. Both must be pointers.
// If no conversion func is registered and the default copying mechanism
// doesn't work on this type pair, an error will be returned.
// Read the comments on the various FieldMatchingFlags constants to understand
// what the 'flags' parameter does.
// 'meta' is given to allow you to pass information to conversion functions,
// it is not used by Convert() other than storing it in the scope.
// Not safe for objects with cyclic references!
func (c *Converter) Convert(src, dest interface{}, flags FieldMatchingFlags, meta *Meta) error {
	if len(c.genericConversions) > 0 {
		// TODO: avoid scope allocation
		s := &scope{converter: c, flags: flags, meta: meta}
		for _, fn := range c.genericConversions {
			if ok, err := fn(src, dest, s); ok {
				return err
			}
		}
	}
	return c.doConversion(src, dest, flags, meta, c.convert)
}
```

## Demo－byteSlice
两个变量类型相同的时候，其实就是直接copy
```go
package main

import (
	"fmt"
	"reflect"

	"k8s.io/kubernetes/pkg/conversion"
)

var DefaultNameFunc = func(t reflect.Type) string { return t.Name() }

func main() {
	c := conversion.NewConverter(DefaultNameFunc)
	src := []byte{1, 2, 3}
	dest := []byte{}
	//Convert will translate src to dest if it knows how. Both must be pointers
	err := c.Convert(&src, &dest, 0, nil)
	if err != nil {
		fmt.Println(err)
	}
	//DeepEqual reports whether x and y are ``deeply equal,''
	if e, a := src, dest; !reflect.DeepEqual(e, a) {
		fmt.Errorf("expected %#v, got %#v", e, a)
	}
	fmt.Println(dest)
}
```
输出如下
```
[1 2 3]
```

## Demo－MismatchedTypes
两个变量类型不同时，需往Converter中注册转换函数,否则会报错
```go
package main

import (
	"fmt"
	"log"
	"reflect"
	"strconv"

	"k8s.io/kubernetes/pkg/conversion"
)

var DefaultNameFunc = func(t reflect.Type) string { return t.Name() }

func main() {
	c := conversion.NewConverter(DefaultNameFunc)

	err := c.RegisterConversionFunc(
		//in *[]string到out *int的转换
		func(in *[]string, out *int, s conversion.Scope) error {
			//注意这里仅仅转换了(*in)[0]
			if str, err := strconv.Atoi((*in)[0]); err != nil {
				return err
			} else {
				*out = str
				return nil //或者使用 return s.Convert(&in.Baz, &out.Baz, 0)
			}
		},
	)
	if err != nil {
		log.Fatalf("Unexpected error: %v", err)
	}

	src := []string{"5"}
	var dest *int
	err = c.Convert(&src, &dest, 0, nil)
	if err != nil {
		log.Fatalf("unexpected error: %v", err)
	}
	if e, a := 5, *dest; e != a {
		fmt.Errorf("expected %#v, got %#v", e, a)
	}
	fmt.Println(src)
	fmt.Println(*dest)
}
```
输出如下
```
[5]
5
```
如果src := []string{"5", "6"}，输出也不会改变，因为转换函数仅仅转换了(*in)[0]

## Demo-DefaultConvert
两边filed一样时，不需要进行显式转换说明，如下面例子中的“Baz”。
对于dest中某些不存在与src的字段，如果不进行显示说明转换办法，那么dest的字段将默认设置为该field的默认值。
```go
package main

import (
	"fmt"
	"log"
	"reflect"

	"k8s.io/kubernetes/pkg/conversion"
)

var DefaultNameFunc = func(t reflect.Type) string { return t.Name() }

func main() {
	type A struct {
		Foo string
		Baz int
	}
	type B struct {
		Bar string
		Baz int
	}
	c := conversion.NewConverter(
		func(t reflect.Type) string { return "MyType" },
	)

	// Ensure conversion funcs can call DefaultConvert to get default behavior,
	// then fixup remaining fields manually
	err := c.RegisterConversionFunc(
		func(in *A, out *B, s conversion.Scope) error {
			//4代表了IgnoreMissingFields模式
			if err := s.DefaultConvert(in, out, 4); err != nil {
				return err
			}
			/*
				显示说明in和out中不一样的字段转换
				如果不进行显示说明转换办法，那么dest的字段将默认设置为该field的默认值
			*/
			out.Bar = in.Foo
			return nil
		},
	)
	if err != nil {
		log.Fatalf("unexpected error %v", err)
	}

	x := A{"hello", 3}
	y := B{}

	err = c.Convert(&x, &y, 0, nil)
	if err != nil {
		log.Fatalf("unexpected error %v", err)
	}
	fmt.Printf("%+v", x)
	fmt.Printf("%+v", y)
}
```
输出如下
```
{Foo:hello Baz:3}
{Bar:hello Baz:3}
```

## Demo-DeepCopy
x和y都属于同一种类型type A struct，不需要显式声明转换函数
```go
package main

import (
	"fmt"
	"reflect"

	"k8s.io/kubernetes/pkg/conversion"
)

var DefaultNameFunc = func(t reflect.Type) string { return t.Name() }

func main() {
	type A struct {
		Foo *string
		Bar []string
		Baz interface{}
		Qux map[string]string
	}
	c := conversion.NewConverter(DefaultNameFunc)

	foo, baz := "foo", "baz"
	x := A{
		Foo: &foo,
		Bar: []string{"bar", "fff"},
		Baz: &baz,
		Qux: map[string]string{"qux": "qux"},
	}
	y := A{}
	/*
		x和y都属于同一种类型type A struct，所以不需要显式声明转换函数
	*/

	if err := c.Convert(&x, &y, 0, nil); err != nil {
		fmt.Println("unexpected error %v", err)
	}
	fmt.Printf("%#v", x)
	fmt.Println()
	fmt.Printf("%#v", y)
	*x.Foo = "foo2"
	x.Bar[0] = "bar2"
	*x.Baz.(*string) = "baz2"
	x.Qux["qux"] = "qux2"
	if e, a := *x.Foo, *y.Foo; e == a {
		fmt.Println("expected difference between %v and %v", e, a)
	}
	if e, a := x.Bar, y.Bar; reflect.DeepEqual(e, a) {
		fmt.Println("expected difference between %v and %v", e, a)
	}
	if e, a := *x.Baz.(*string), *y.Baz.(*string); e == a {
		fmt.Println("expected difference between %v and %v", e, a)
	}
	if e, a := x.Qux, y.Qux; reflect.DeepEqual(e, a) {
		fmt.Println("expected difference between %v and %v", e, a)
	}
}
```
输出如下
```
main.A{Foo:(*string)(0xc4200721d0), Bar:[]string{"bar", "fff"}, Baz:(*string)(0xc4200721e0), Qux:map[string]string{"qux":"qux"}}
main.A{Foo:(*string)(0xc420072230), Bar:[]string{"bar", "fff"}, Baz:(*string)(0xc4200722f0), Qux:map[string]string{"qux":"qux"}}
```

## 参考
/kubernetes-1.5.2/pkg/conversion/converter_test.go


