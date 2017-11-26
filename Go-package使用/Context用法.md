# Context用法

## 简介
在 Go http包的Server中，每一个请求在都有一个对应的 goroutine 去处理。 请求处理函数通常会启动额外的 goroutine 用来访问后端服务，比如数据库和RPC服务。 用来处理一个请求的 goroutine 通常需要访问一些与请求特定的数据，比如终端用户的身份认证信息、验证相关的token、请求的截止时间。 当一个请求被取消或超时时，所有用来处理该请求的 goroutine 都应该迅速退出，然后系统才能释放这些 goroutine 占用的资源。 那么该如何优雅地同时关闭多个goroutine呢？ 这个时候就轮到context包上场了。

## Context的核心数据结构
type Context interface 是 package context的核心数据结构：

1. Done 方法返回一个 channel，这个 channel 对于以 Context 方式运行的函数而言，是一个取消信号。 当这个 channel 关闭时，上面提到的这些函数应该终止手头的工作并立即返回。 之后，Err 方法会返回一个错误，告知为什么 Context 被取消。

2. Context 对象是线程安全的，你可以把一个 Context 对象传递给任意个数的 gorotuine，对它执行取消操作时，所有 goroutine 都会接收到取消信号。

3. Deadline() 方法允许函数确定它们是否应该开始工作。 如果剩下的时间太少，也许这些函数就不值得启动。在代码中，我们也可以使用 Deadline 对象为 I/O 操作设置截止时间。

4. Value 方法允许 Context 对象携带request作用域的数据，该数据必须是线程安全的。

5. 一个 Context 不能拥有 Cancel() 方法，同时我们也只能使用 Done channel 来接收数据。 原因是：接收取消信号的函数和发送取消信号的函数通常不是一个。 一个典型的场景是：父操作为子操作操作启动 goroutine，子操作也就不能取消父操作。 作为一个折中，WithCancel() 函数提供了一种取消新的 Context 的方法。

```go
// A Context carries a deadline, a cancelation signal, and other values across
// API boundaries.
//
// Context's methods may be called by multiple goroutines simultaneously.

type Context interface {
	// Deadline returns the time when work done on behalf of this context
	// should be canceled. Deadline returns ok==false when no deadline is
	// set. Successive calls to Deadline return the same results.
	Deadline() (deadline time.Time, ok bool)

	// Done returns a channel that's closed when work done on behalf of this
	// context should be canceled. Done may return nil if this context can
	// never be canceled. Successive calls to Done return the same value.
	//
	// WithCancel arranges for Done to be closed when cancel is called;
	// WithDeadline arranges for Done to be closed when the deadline
	// expires; WithTimeout arranges for Done to be closed when the timeout
	// elapses.
	//
	// Done is provided for use in select statements:
	//
	//  // Stream generates values with DoSomething and sends them to out
	//  // until DoSomething returns an error or ctx.Done is closed.
	//  func Stream(ctx context.Context, out chan<- Value) error {
	//  	for {
	//  		v, err := DoSomething(ctx)
	//  		if err != nil {
	//  			return err
	//  		}
	//  		select {
	//  		case <-ctx.Done():
	//  			return ctx.Err()
	//  		case out <- v:
	//  		}
	//  	}
	//  }
	//
	// See http://blog.golang.org/pipelines for more examples of how to use
	// a Done channel for cancelation.
	Done() <-chan struct{}

	// Err returns a non-nil error value after Done is closed. Err returns
	// Canceled if the context was canceled or DeadlineExceeded if the
	// context's deadline passed. No other values for Err are defined.
	// After Done is closed, successive calls to Err return the same value.
	Err() error

	// Value returns the value associated with this context for key, or nil
	// if no value is associated with key. Successive calls to Value with
	// the same key returns the same result.
	//
	// Use context values only for request-scoped data that transits
	// processes and API boundaries, not for passing optional parameters to
	// functions.
	//
	// A key identifies a specific value in a Context. Functions that wish
	// to store values in Context typically allocate a key in a global
	// variable then use that key as the argument to context.WithValue and
	// Context.Value. A key can be any type that supports equality;
	// packages should define keys as an unexported type to avoid
	// collisions.
	//
	// Packages that define a Context key should provide type-safe accessors
	// for the values stores using that key:
	//
	// 	// Package user defines a User type that's stored in Contexts.
	// 	package user
	//
	// 	import "golang.org/x/net/context"
	//
	// 	// User is the type of value stored in the Contexts.
	// 	type User struct {...}
	//
	// 	// key is an unexported type for keys defined in this package.
	// 	// This prevents collisions with keys defined in other packages.
	// 	type key int
	//
	// 	// userKey is the key for user.User values in Contexts. It is
	// 	// unexported; clients use user.NewContext and user.FromContext
	// 	// instead of using this key directly.
	// 	var userKey key = 0
	//
	// 	// NewContext returns a new Context that carries value u.
	// 	func NewContext(ctx context.Context, u *User) context.Context {
	// 		return context.WithValue(ctx, userKey, u)
	// 	}
	//
	// 	// FromContext returns the User value stored in ctx, if any.
	// 	func FromContext(ctx context.Context) (*User, bool) {
	// 		u, ok := ctx.Value(userKey).(*User)
	// 		return u, ok
	// 	}
	Value(key interface{}) interface{}
}
```

## 创建根context
context 包提供了一些函数，协助用户从现有的 Context 对象创建新的 Context 对象。 这些 Context 对象形成一棵树：当一个 Context 对象被取消时，继承自它的所有 Context 都会被取消。

Background()是所有 Context 对象树的根，它不能被取消。
```go
func Background() Context {
	return background
}
```

## 创建子context
WithCancel 和 WithTimeout 函数会返回继承的 Context 对象，这些对象可以比它们的父 Context 更早地取消。 当请求处理函数返回时，与该请求关联的 Context 会被取消。
- 当使用多个副本发送请求时，可以使用 WithCancel 取消多余的请求。
- WithTimeout 在设置对后端服务器请求截止时间时非常有用。
```go
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	ctx, f := context.WithCancel(parent)
	return ctx, CancelFunc(f)
}

func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
	return WithDeadline(parent, time.Now().Add(timeout))
}
```

## WithTimeout例子
以下是官方提供的例子，子context ctx会在30Second后被关闭，从而触发了`ctx.Done()`
```go
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-time.After(2 * time.Minute):
		fmt.Println("overslept")
	case <-ctx.Done():
		fmt.Println(ctx.Err()) // prints "context deadline exceeded"
	}
}
```
这里输出结果是
```shell
context deadline exceeded
```

## context的使用规范
1. context包里的方法是线程安全的，可以被多个线程使用
2. 就算是被多个不同的goroutine使用，context也是安全的
3. 把context作为第一个参数，并且一般都把变量命名为ctx

## 参考
[Go语言并发模型：使用 context](https://segmentfault.com/a/1190000006744213)

[Golang之Context的使用](http://www.nljb.net/default/Golang之Context的使用/)

[Golang之Context](https://studygolang.com/articles/9485)

[源码解读](http://blog.csdn.net/xiaohu50/article/details/49100433)