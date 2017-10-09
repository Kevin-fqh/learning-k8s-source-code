# Some Tips

1. fmt.Printf()、数组的数据类型
输出一个值的类型

数组的长度是数组类型的一个组成部分，因此[3]int和[4]int是两种不同的数组类型
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

2. 