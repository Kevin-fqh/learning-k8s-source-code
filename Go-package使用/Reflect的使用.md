# Reflect Package

[Reflect Package](https://golang.org/pkg/reflect/#Kind)

## 基本用法
Type()得到的是静态类型，Kind()得到的是相关类型
```
package main

import (
	"fmt"
	"reflect"
)

type myInt int

type T struct {
	A int
	B string
}

func main() {
	var i myInt

	i = 4
	a := reflect.ValueOf(i)
	/*
		Type()和Kind()的区别
	*/
	fmt.Println(a.Type())                // main.myInt, Type()得到的是静态类型
	fmt.Println(a.Kind())                // int， Kind()得到的是相关类型
	fmt.Println(a.Kind() == reflect.Int) // true

	fmt.Println(a.Interface()) // 4

	/*
		通过反射来修改value的值
	*/
	fmt.Println(a.CanSet()) // false,表示a不可以被设置。因为reflect.ValueOf得到的是i的一个复制副本， 而不是i本身
	point := reflect.ValueOf(&i)
	fmt.Println(point.CanSet()) // false,因为b是一个指针，我们要修改的是i的值，而不是指针&i的值

	c := point.Elem()       // 通过 Value 的 Elem() 方法来指针所指向内容的 Value
	fmt.Println(c.CanSet()) // true
	c.SetInt(99)
	fmt.Println(c.Interface()) // 99,修改成功
	fmt.Println(i)             // 99

	/*
		结构体
	*/
	t := T{33, "my"}
	point_e := reflect.ValueOf(&t).Elem()
	fmt.Println(point_e.Type())          // main.T
	point_e.Field(0).SetInt(11)          //通过反射修改结构体中的某个成员的值
	fmt.Println(point_e.Interface())     // &{11 my}
	fmt.Println(point_e.Field(0).Type()) //int
}
```