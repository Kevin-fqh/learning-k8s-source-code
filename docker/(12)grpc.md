# grpc

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Install from source code](#install-from-source-code)
  - [rpc种类](#rpc种类)
  - [简单rpc Demo](#简单rpc-demo)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## Install from source code
参考[Compiling your protocol buffers](https://developers.google.com/protocol-buffers/docs/gotutorial)

1. 下载https://github.com/google/protobuf源码，直接下载最新版即可

2. 依赖关系(mac os)
```shell
# brew install automake
# brew install libtool
```

3. protocol buffers编译安装
```shell
# cd protobuf
# ./autogen.sh
# ./configure
# make
# make check
# make install
# protoc  --version
```

4. golang/protobuf安装，下载github.com/golang/protobuf/protoc-gen-go源码，也是最新版，和上面匹配
```shell
# go build github.com/golang/protobuf/protoc-gen-go
# go install github.com/golang/protobuf/protoc-gen-go
```

5. 环境变量设置
```shell
# export PATH=$PATH:$GOPATH/bin
```

6. grpc安装，下载https://github.com/grpc/grpc-go源码，最新版。如果提示 undefined: grpc.SupportPackageIsVersion4 ，就是grpc版本不对。

7. 至此，安装成功，后面就可以利用protoc工具来自动生成客户端和服务器端的通用代码，方便我们建立客户端和服务器端的服务。如果中间出现啥版本问题，直接下最新版。。。。应该是ok的。
```shell
protoc --go_out=plugins,grpc:. helloworld.proto
```


## rpc种类
- 简单rpc
- 客户端流式rpc，客户端使用流，写入一个消息序列并将其发送到服务器。一旦客户端完成写入消息，它等待服务器完成读取返回它的响应。在请求类型前指定 stream 关键字
```
 rpc SayHello (stream Point) returns (Length) {}
```
- server端流式rpc，客户端发送请求到服务器，拿到一个流去读取返回的消息序列。 客户端读取返回的流，直到里面没有任何消息。在Response类型前指定 stream 关键字
```
 rpc SayHello (Point) returns (stream Length) {}
```
- 双向流式rpc，此时server端和client端可以任意顺序读写，两个stream是互相独立的
```
 rpc SayHello (stream Point) returns (stream Length) {}
```

## 简单rpc Demo
### proto文件
```proto
syntax = "proto3";

/*
1，2，3称之为tags，用于标识filed在message的二进制中的格式
如果message正在被使用，那么tags不能被改动
[1-15]的tag在编码时仅占用1byte，而[16，2047]需要2byte
故应该把[1-15]留给频繁使用的filed
https://developers.google.com/protocol-buffers/docs/encoding#structure
有些tag是预留的，不能使用

Repeated fields are slices.
*/

package helloworld;

// The service definition.
service GetLength {
    rpc SayHello (Point) returns (Length) {}
}

message Point {
    int32 x = 1;
    int32 y = 2;
}

message Length {
    double length = 1;
}
```
生成的go文件helloworld.pb.go中同时包含了Server端和Client端都可以使用的代码。 其中proto文件中声明的`service GetLength`会生成Client API和Server API：
  * `Client API for GetLength service`，仅提供给Client端调用
  * `Server API for GetLength service`，仅提供给Server端调用

### server端
调用`helloworld.pb.go`中的接口提供服务，grpc.NewServer()生成一个grpc的server
```go
package main

import (
	"net"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pb "grpc-demo/helloworld"

	"fmt"
	"math"
)

const (
	port = ":50051"
)

type server struct {
}

func (s *server) SayHello(ctx context.Context, point *pb.Point) (*pb.Length, error) {
	fmt.Println("receive a request")
	x := point.GetX()
	y := point.GetY()
	l := x*x + y*y
	//注意类型
	lengthObj := &pb.Length{
		Length: math.Sqrt(float64(l)),
	}
	return lengthObj, nil
}

func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		fmt.Println("occur ", err)
		return
	}
	s:=grpc.NewServer()
	/*
		把type server struct中自定义实现的方法SayHello()注册到rpc服务器中
	 */
	pb.RegisterGetLengthServer(s,&server{})
	if err:=s.Serve(lis); err != nil {
		fmt.Println("start server error ",err)
		return
	}
}
```
### client端
调用`helloworld.pb.go`中的接口连接服务器，发送rpc请求
```go
package main

import (
	"log"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pb "grpc-demo/helloworld"
	"fmt"
)

const (
	address     = "localhost:50051"
)

func main() {
	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewGetLengthClient(conn)

	// Contact the server and print out its response.
	point := &pb.Point{
		X: 1,
		Y: 1,
	}
	r, err := c.SayHello(context.Background(),point)
	if err != nil {
		log.Fatalf("could not get data from server: %v", err)
	}
	fmt.Println("the length is:",r)
}
```


## 参考
[gotutorial](https://developers.google.com/protocol-buffers/docs/gotutorial)

[proto3语法](https://developers.google.com/protocol-buffers/docs/proto3)
[Protobuf3-language-guide-中文集合](http://colobu.com/2017/03/16/Protobuf3-language-guide/)

[github.com/protobuf](https://github.com/golang/protobuf)

[example](https://github.com/grpc/grpc-go/tree/master/examples/helloworld)
