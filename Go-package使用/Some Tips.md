# Some Tips

## fmt.Printf()、数组的数据类型
输出一个值的类型

以下为数组在 Go 和 C 中的主要区别。在 Go 中,
- 数组是值。 将一个数组赋值给另一个数组会复制其所有元素。 （值传递，而非引用传递）
- 特别地,若将某个数组传入某个函数,它将接收到该数组的一份副本而非指针。 
- 数组的大小是其类型的一部分。类型 [10]int 和 [20]int 是不同的。

```go
package main

import (
	"fmt"
)

func main() {
	s := [3]int{1, 2, 3}
	fmt.Printf("%T ", s)
}
```
结果是
```
[3]int 
```

## defer语句
defer 语句用于预设一个函数调用(即推迟执行函数)。

被推迟函数的实参(如果该函数为方法则还包括接收者)在推迟执行时就会求值, 而不是在真正调用执行时才求值。 
这样无需担心变量值在函数执行时被改变。

在下面的例子中，声明` defer un(trace("a"))`时，un()函数是作为被推迟函数，那么un()函数的参数在声明的时候就会进行求值，而不是到真正执行un()函数时再去求值。

```go
func trace(s string) string {
    fmt.Println("entering:", s)
    return s
}
func un(s string) {
    fmt.Println("leaving:", s)
}
func a() {
    defer un(trace("a"))
    fmt.Println("in a")
}
func b() {
    defer un(trace("b"))
    fmt.Println("in b")
    a()
}
func main() {
    b()
}
```
输出如下
```
entering: b
in b
entering: a
in a
leaving: a
leaving: b
```

## golang中的nil
1. nil没有type
2. 在Go语言中，未显示初始化的变量拥有其类型的zero value。 共有6种类型变量的zero value是nil，包括：pointer，slice，map，channel，function和interface。

```
类型	                     nil值含义
pointer	                 指向nothing
slice	                 slice变量中的3个成员值：buf为nil（没有backing array），len和cap都是0
map，channel，function	 一个nil pointer，指向nothing
interface	             interface包含”type, value”，一个nil interface必须二者都为nil: ”nil, nil”
```

由于Go中interface会同时存储类型和值，如果将一个nil对象赋值给一个interface，这个interface为非nil。

nil只能赋值给指针、channel、func、interface、map或slice类型的变量。
如果将nil赋值给其他变量的时候将会引发panic。

见[理解Go中的nil](https://studygolang.com/topics/2863)

## panic-1
当 panic 被调用后(包括不明确的运行时错误,例如切片检索越界或类型断言失败), 
程序将立刻终止当前函数的执行,并开始回溯Go程的栈, 运行任何被推迟的函数。
若回溯到达 Go 程栈的顶端,程序就会终止。

如果在一个groutine中发生了panic， 定义在panic后的语句都不会被执行到，包括defer语句。 
只有定义在panic语句前面的defer语句才会在回溯过程中执行。

所以recover()语句必须放在panic()前面的defer语句中，才会生效。

recover()语句负责处理panic产生的错误。

```go
package main

import (
	"fmt"
)

func main() {
	st := []int{1, 2, 3, 4, 5}
	defer func(s []int) {
		_ = s
		fmt.Println("start")
	}(st)

	defer func() {
		if err := recover(); err != nil {
			fmt.Println("recover :", err)
		}
	}()

	for k, a := range st {
		if k > 0 {
			break
		}
		fmt.Println(a)
		//		debug.PrintStack()
		panic("this is panic")
	}

	st = []int{2, 2, 2}
	defer func(s []int) {
		fmt.Println("end")
		_ = s
	}(st)

}
```
理论上的输出如下，实际上可能有差异
```
1
recover : this is panic
start
```

## panic-2
```go
package main

import (
	"fmt"
)

func main() {
	st := []int{1, 2, 3, 4, 5}
	defer func(s []int) {
		_ = s
		fmt.Println("start")
	}(st)

	for k, a := range st {
		if k > 0 {
			break
		}
		fmt.Println(a)
		//		debug.PrintStack()
		panic("this is panic")
	}

	st = []int{2, 2, 2}
	defer func(s []int) {
		fmt.Println("end")
		_ = s
	}(st)

}
```
理论上的输出如下，实际上可能有差异
```
1
start
panic: this is panic

goroutine 1 [running]:
main.main()
	/Users/fanqihong/Desktop/go-project/src/ftmtest/fmttest.go:20 +0x150
```