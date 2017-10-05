# go-restful

简单介绍[package go-restful](https://godoc.org/github.com/emicklei/go-restful)的用法。
构建REST-style WebServices的一个第三方包

本文摘抄于[apiServer之go-restful的使用](http://dockone.io/article/2171)

## 关键组件
### Route
路由包含两种，一种是标准JSR311接口规范的实现RouterJSR311,一种是快速路由CurlyRouter。

CurlyRouter支持正则表达式和动态参数，相比RouterJSR11更加轻量级，apiserver中使用的就是这种路由。

A Route is defined by a HTTP method, an URL path and (optionally) the MIME types it consumes (Content-Type) and produces (Accept). 
Route binds a HTTP Method,Path,Consumes combination to a RouteFunction.
其中请求方法(http Method)，请求路径(URL Path),输入输出类型(JSON/YAML)，响应内容类型(Accept)。

### WebService
WebService逻辑上是Route的集合，功能上主要是为一组Route统一设置包括root path,请求响应的数据类型等一些通用的属性。

需要注意的是，WebService必须加入到Container中才能生效。

### Container
Container逻辑上是WebService的集合，功能上可以实现多终端的效果。

它包括一组restful.WebService和一个http.ServeMux对象，使用RouteSelector进行请求派发。

例如，下面代码中创建了两个Container，分别在不同的port上提供服务。
```go
package main

import (
"github.com/emicklei/go-restful"
"io"
"log"
"net/http"
)

func main() {
ws := new(restful.WebService)
ws.Route(ws.GET("/hello").To(hello))
/*
	ws被添加到默认的container restful.DefaultContainer中
	DefaultContainer is a restful.Container that uses http.DefaultServeMux
*/
restful.Add(ws)
go func() {
    // restful.DefaultContainer监听在端口8080上
    http.ListenAndServe(":8080", nil)
}()

/*
	NewContainer creates a new Container using a new ServeMux and default router (CurlyRouter)
*/
container2 := restful.NewContainer()
ws2 := new(restful.WebService)
ws2.Route(ws2.GET("/hello").To(hello2))
//WebService ws2被添加到container2中
container2.Add(ws2)
// container2中监听端口8081
server := &http.Server{Addr: ":8081", Handler: container2}
log.Fatal(server.ListenAndServe())
}

func hello(req *restful.Request, resp *restful.Response) {
io.WriteString(resp, "default world")
}

func hello2(req *restful.Request, resp *restful.Response) {
io.WriteString(resp, "second world")
}
```

### Filter
Filter用于动态的拦截请求和响应，类似于放置在相应组件前的钩子，在相应组件功能运行前捕获请求或者响应，主要用于记录log，验证，重定向等功能。
go-restful中有三种类型的Filter：
- Container Filter:
运行在Container中所有的WebService执行之前。
```go
// install a (global) filter for the default container (processed before any webservice)
restful.Filter(globalLogging)
```
- WebService Filter：
运行在WebService中所有的Route执行之前。
```go
// install a webservice filter (processed before any route)
ws.Filter(webserviceLogging).Filter(measureTime)
```
- Route Filter:
运行在调用Route绑定的方法之前。
```go
// install 2 chained route filters (processed before calling findUser)
ws.Route(ws.GET("/{user-id}").Filter(routeLogging).Filter(NewCountFilter().routeCounter).To(findUser))
```

例子如下：
```go
package main

import (
"github.com/emicklei/go-restful"
"log"
"net/http"
)

type User struct {
Id, Name string
}

type UserResource struct {
// normally one would use DAO (data access object)
users map[string]User
}

func (u UserResource) Register(container *restful.Container) {
// 创建新的WebService
ws := new(restful.WebService)

// 设定WebService对应的路径("/users")和支持的MIME类型(restful.MIME_XML/ restful.MIME_JSON)
ws.
    Path("/users").
    Consumes(restful.MIME_XML, restful.MIME_JSON).
    Produces(restful.MIME_JSON, restful.MIME_XML) // you can specify this per route as well

// 添加路由： GET /{user-id} --> u.findUser
ws.Route(ws.GET("/{user-id}").To(u.findUser))

// 添加路由： POST / --> u.updateUser
ws.Route(ws.POST("").To(u.updateUser))

// 添加路由： PUT /{user-id} --> u.createUser
ws.Route(ws.PUT("/{user-id}").To(u.createUser))

// 添加路由： DELETE /{user-id} --> u.removeUser
ws.Route(ws.DELETE("/{user-id}").To(u.removeUser))

// 将初始化好的WebService添加到Container中
container.Add(ws)
}

// GET http://localhost:8080/users/1
//
func (u UserResource) findUser(request *restful.Request, response *restful.Response) {
id := request.PathParameter("user-id")
usr := u.users[id]
if len(usr.Id) == 0 {
    response.AddHeader("Content-Type", "text/plain")
    response.WriteErrorString(http.StatusNotFound, "User could not be found.")
} else {
    response.WriteEntity(usr)
}
}

// POST http://localhost:8080/users
// <User><Id>1</Id><Name>Melissa Raspberry</Name></User>
//
func (u *UserResource) updateUser(request *restful.Request, response *restful.Response) {
usr := new(User)
err := request.ReadEntity(&usr)
if err == nil {
    u.users[usr.Id] = *usr
    response.WriteEntity(usr)
} else {
    response.AddHeader("Content-Type", "text/plain")
    response.WriteErrorString(http.StatusInternalServerError, err.Error())
}
}

// PUT http://localhost:8080/users/1
// <User><Id>1</Id><Name>Melissa</Name></User>
//
func (u *UserResource) createUser(request *restful.Request, response *restful.Response) {
usr := User{Id: request.PathParameter("user-id")}
err := request.ReadEntity(&usr)
if err == nil {
    u.users[usr.Id] = usr
    response.WriteHeader(http.StatusCreated)
    response.WriteEntity(usr)
} else {
    response.AddHeader("Content-Type", "text/plain")
    response.WriteErrorString(http.StatusInternalServerError, err.Error())
}
}

// DELETE http://localhost:8080/users/1
//
func (u *UserResource) removeUser(request *restful.Request, response *restful.Response) {
id := request.PathParameter("user-id")
delete(u.users, id)
}

func main() {
// 创建一个空的Container
wsContainer := restful.NewContainer()

// 设定路由为CurlyRouter(快速路由)，这个也是默认值
wsContainer.Router(restful.CurlyRouter{})

// 创建自定义的Resource Handle(此处为UserResource)
u := UserResource{map[string]User{}}

// 创建WebService，并将WebService加入到Container中
u.Register(wsContainer)

log.Printf("start listening on localhost:8080")
server := &http.Server{Addr: ":8080", Handler: wsContainer}

// 启动服务
log.Fatal(server.ListenAndServe())
}
```
## 总结
上面的示例代码构建RESTful服务，分为几个步骤:
- 创建Container
- 配置Container属性：ServeMux/Router type等
- 创建自定义的Resource Handle，实现Resource相关的处理方式。
- 创建对应Resource的WebService，在WebService中添加响应Route，并将WebService加入到Container中。
- 启动监听服务。

通俗来说，三者的关系如下:
- Container: 一个Container包含多个WebService
- WebService: 一个WebService包含多条route
- Route: 一条route包含一个method(GET、POST、DELETE等)，一条具体的path(URL)以及一个响应的handler function。

