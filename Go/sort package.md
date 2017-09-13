# sort

简单介绍[package sort](https://godoc.org/sort)的用法。

对于sliece，目前sort包官方仅仅支持int float64 string三个类型的slice的排序。这对于期望对自定义的数据结构进行排序是不够的。

所以我们需要针对自定义的数据结构实现func Len() int 、Swap(i, j int)、Less(i, j int) bool函数，然后就可以直接用sort.sort()来对自定义的数据进行排序了。

## 例子1
```go
package main

import (
	"fmt"
	"sort"
)

type Person struct {
	Name string
	Age  int
	High float64
}

type PersonSlice []Person

func (ps PersonSlice) Len() int           { return len(ps) }
func (ps PersonSlice) Swap(i, j int)      { ps[i], ps[j] = ps[j], ps[i] }
func (ps PersonSlice) Less(i, j int) bool { return ps[i].Age < ps[j].Age }

func main() {
	var persons = PersonSlice{
		{"a", 1, 1.1},
		{"c", 5, 3.1},
		{"b", 2, 2.1},
	}
	fmt.Println(persons)
	sort.Sort(persons)
	fmt.Println(persons)

}
```
输出
```go
[{a 1 1.1} {c 5 3.1} {b 2 2.1}]
[{a 1 1.1} {b 2 2.1} {c 5 3.1}]
```
这是一个比较简单的例子，容易上手。但如果我希望能按照`Name`属性或者`High`属性来排序呢？这就得重新构造一个slice，再重写三个函数......是不是觉得心累？

可以发现Len() int 和Swap(i, j int)应该是一样的，实现不变。那么我们应该把Less(i, j int) bool提取成一个通用的模型。

type lessFunc func(p, q Person) bool

## 例子2
```go
package main

import (
	"fmt"
	"sort"
)

type Person struct {
	Name string
	Age  int
	High float64
}

func main() {
	var persons = []Person{
		{"a", 1, 1.1},
		{"c", 5, 3.1},
		{"b", 2, 5.1},
	}
	fmt.Println(persons)
	var pm = PersonMutil{
		p:    persons,
		//从这里更改条件即可
		less: func(i, j Person) bool { return i.High < j.High },
	}

	sort.Sort(pm)
	//注意此时仍是打印var persons，而不是pm
	fmt.Println(persons)

}

type lessFunc func(p, q Person) bool
type PersonMutil struct {
	p    []Person
	less lessFunc
}

//注意函数的操作对象是pm.p，而不是pm
func (pm PersonMutil) Len() int      { return len(pm.p) }
func (pm PersonMutil) Swap(i, j int) { pm.p[i], pm.p[j] = pm.p[j], pm.p[i] }
func (pm PersonMutil) Less(i, j int) bool {
	return pm.less(pm.p[i], pm.p[j])
}
```
或者main函数写成
```go
func main() {
	var persons = []Person{
		{"a", 1, 1.1},
		{"c", 5, 3.1},
		{"b", 2, 5.1},
	}
	fmt.Println(persons)
	high_attr := func(i, j Person) bool { return i.High < j.High }
	sort.Sort(PersonMutil{persons, high_attr})
	fmt.Println(persons)

}
```
