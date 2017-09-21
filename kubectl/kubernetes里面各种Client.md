# kubernetes里面各种Client

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [定义](#定义)
  - [用法](#用法)
    - [基于Clientset生成eventClient、podClient](#基于clientset生成eventclient)
	- [使用RESTClient](#使用restclient)
	- [使用DynamicClient](#使用dynamicclient)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

## 定义
在前面kubectl系统文章中的Event-1，提到过kubectl是通过`events, _ = d.Core().Events(namespace).Search(ref)`来获取具体资源的。本文主要对`Core()`进行深挖。看看其是如何取到最核心的资源。

/pkg/client/clientset_generated/internalclientset/clientset.go，可以发现type Clientset struct里面包含了所有group的Client，和`/pkg/master/import_known_versions.go`中import的group是大概对应的。每个group中只有一个版本被包含在Clientset中。

然后DiscoveryClient、CoreClient这些每个client都是封装了一个restClient。
也就是说type Clientset struct是对type RESTClient struct的封装。
```go
// Clientset contains the clients for groups. Each group has exactly one
// version included in a Clientset.
/*
	译：type Clientset struct 是包含groups的clients。
		每个group中只有一个版本被包含在Clientset中。
*/
type Clientset struct {
	*discovery.DiscoveryClient
	*internalversioncore.CoreClient
	*internalversionapps.AppsClient
	*internalversionauthentication.AuthenticationClient
	*internalversionauthorization.AuthorizationClient
	*internalversionautoscaling.AutoscalingClient
	*internalversionbatch.BatchClient
	*internalversioncertificates.CertificatesClient
	*internalversionextensions.ExtensionsClient
	*internalversionpolicy.PolicyClient
	*internalversionrbac.RbacClient
	*internalversionstorage.StorageClient
}

/*
	type CoreClient struct用于与k8s.io/kubernetes/pkg/apimachinery/registered.Group组提供的features进行交互。
*/
type CoreClient struct {
	restClient restclient.Interface
}

// AppsClient is used to interact with features provided by the k8s.io/kubernetes/pkg/apimachinery/registered.Group group.
type AppsClient struct {
	restClient restclient.Interface
}
```

/pkg/client/restclient/client.go，type RESTClient struct封装了一个Client *http.Client，应该是最底层的client了。
```go
// RESTClient imposes common Kubernetes API conventions on a set of resource paths.
// The baseURL is expected to point to an HTTP or HTTPS path that is the parent
// of one or more resources.  The server should return a decodable API resource
// object, or an api.Status object which contains information about the reason for
// any failure.
//
// Most consumers should use client.New() to get a Kubernetes API client.
/*
	译：type RESTClient struct 在一组resource paths上强加了常见的Kubernetes API约定。
		baseURL指向作为一个或多个resources的父级resources的HTTP（HTTPS）路径。
		server端应该返回一个可解码的API资源对象，或一个api.Status对象，其中包含有关任何故障原因的信息。
*/
type RESTClient struct {
	// base is the root URL for all invocations of the client
	base *url.URL
	// versionedAPIPath is a path segment connecting the base URL to the resource root
	versionedAPIPath string

	// contentConfig is the information used to communicate with the server.
	contentConfig ContentConfig

	// serializers contain all serializers for underlying content type.
	serializers Serializers

	// creates BackoffManager that is passed to requests.
	createBackoffMgr func() BackoffManager

	// TODO extract this into a wrapper interface via the RESTClient interface in kubectl.
	Throttle flowcontrol.RateLimiter

	// Set specific behavior of the client.  If not set http.DefaultClient will be used.
	Client *http.Client
}
```
## 用法
### 使用RESTClient
RESTClient是Kubernetes最基础的Client，封装了一个http client。下面的Demo就描述如何生成一个RESTClient，并用该RESTClient获取某具体Pod的详细信息。
```go

```

### 基于Clientset生成eventClient
我们可以基于type Clientset struct获取如pod、event这些对象(eventClient、podClient)，在kubernetes一般的用法是

- /cmd/kubelet/app/server.go中的基于config文件生成一个eventClient
```go
/*
	/pkg/client/clientset_generated/internalclientset/clientset.go
		==>func NewForConfig(c *restclient.Config) (*Clientset, error)
*/
eventClient, err = clientset.NewForConfig(&eventClientConfig)

// NewForConfig creates a new Clientset for the given config.
func NewForConfig(c *restclient.Config) (*Clientset, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	var clientset Clientset
	var err error
	clientset.CoreClient, err = internalversioncore.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.AppsClient, err = internalversionapps.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.AuthenticationClient, err = internalversionauthentication.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.AuthorizationClient, err = internalversionauthorization.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.AutoscalingClient, err = internalversionautoscaling.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.BatchClient, err = internalversionbatch.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.CertificatesClient, err = internalversioncertificates.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.ExtensionsClient, err = internalversionextensions.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.PolicyClient, err = internalversionpolicy.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.RbacClient, err = internalversionrbac.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	clientset.StorageClient, err = internalversionstorage.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}

	clientset.DiscoveryClient, err = discovery.NewDiscoveryClientForConfig(&configShallowCopy)
	if err != nil {
		glog.Errorf("failed to create the DiscoveryClient: %v", err)
		return nil, err
	}
	return &clientset, nil
}
```

- [client-go](https://github.com/kubernetes/client-go/tree/release-2.0)中的用法

k8s V1.5.2对应的client-go版本是v2.0
```go
import (
	"flag"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeconfig = flag.String("kubeconfig", "./config", "absolute path to the kubeconfig file")
)

func main() {
	flag.Parse()
	// uses the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	for {
		//创建一个podClient
		pods, err := clientset.Core().Pods("").List(v1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))
		time.Sleep(10 * time.Second)
	}
}
```

### 使用DynamicClient
`client-go`除了提供clientset的连接方式，还提供了dynamic client 和restful api的连接方式与apiserver交互。
通过dynamic client可以访问所有资源（包括thirdpartresource所能提供的资源）
```go

```

## 总结
RESTClient是Kubernetes最基础的Client，封装了一个http client。
restclient 是dynamic client和clientset的基础，支持json与protobuf，可以访问所有资源，实现对自定义thirdpartresource资源的获取。

client-go除了提供clientset的连接方式，还提供了dynamic client 和restful api的连接方式与apiserver交互。
通过dynamic client可以访问所有资源（包括thirdpartresource所能提供的资源）。

同时可以通过Clientset生成各种资源的Client，进行相关操作。



