# net/http 用法

## 用法
server.go代码如下
```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Book struct {
	Name  string  `json: "name"`
	Price float64 `json: "price"`
}

func getHandler(w http.ResponseWriter, req *http.Request) {
	fmt.Println("In func getHandler")

	b1 := Book{
		Name:  "book1",
		Price: 13.1,
	}
	
	//解析成json格式，对数据b1进行编码，写入 w http.ResponseWriter
	encoder := json.NewEncoder(w)
	// 间隔3s给client端发送一个信息
	for i := 0; i < 10; i++ {
		encoder.Encode(b1)
		w.(http.Flusher).Flush() //把缓存中的信息发送出去
		time.Sleep(3 * time.Second)
	}
}

func easyHandler(w http.ResponseWriter, req *http.Request) {
	fmt.Println("In func easyHandler")
	st := "I am easyHandler"
	res, err := json.Marshal(st)
	if err != nil {
		fmt.Println("occurs error")
		return
	}
	w.Write(res)
}

func main() {
	serverMux := http.NewServeMux()
	serverMux.HandleFunc("/easy", easyHandler)

	serverMux.HandleFunc("/getbook", getHandler)
	http.ListenAndServe("127.0.0.1:8080", serverMux)
}
```

client端代码如下，间隔3s收到一条消息
```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func main() {
	url := "http://127.0.0.1:8080/getbook"
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println(err)
		return
	}
	var v map[string]interface{}

	//从resp.Body中读取数据，解码成&v
	decoder := json.NewDecoder(resp.Body)
	// 在server端全部发送完消息后，会进行break
	for {
		err := decoder.Decode(&v)
		if err != nil {
			break
		}
		fmt.Println(v)
	}
	resp.Body.Close()

}
```