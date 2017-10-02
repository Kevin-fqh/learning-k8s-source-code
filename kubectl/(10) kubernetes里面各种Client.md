# kubernetes里面各种Client

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [定义](#定义)
  - [用法](#用法)
	- [使用RESTClient](#使用restclient)
	- [基于Clientset生成eventClient](#基于clientset生成eventclient)
	- [使用DynamicClient](#使用dynamicclient)
  - [使用Clientset创建删除一个pod](#使用clientset创建删除一个pod)
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
RESTClient是Kubernetes最基础的Client，封装了一个http client。

参考`/client-go-2.0.0/kubernetes/typed/core/v1/replicationcontroller.go`中的例子，直接使用RESTClient来操作资源
```go
// replicationControllers implements ReplicationControllerInterface
type replicationControllers struct {
	client rest.Interface //一个RESTClient
	ns     string
}

// Get takes name of the replicationController, and returns the corresponding replicationController object, and an error if there is any.
func (c *replicationControllers) Get(name string) (result *v1.ReplicationController, err error) {
	result = &v1.ReplicationController{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("replicationcontrollers").
		Name(name).
		Do().
		Into(result)
	return
}
```
生成RESTClient参考`/client-go-2.0.0/rest/config.go`
```go
// RESTClientFor returns a RESTClient that satisfies the requested attributes on a client Config
// object. Note that a RESTClient may require fields that are optional when initializing a Client.
// A RESTClient created by this method is generic - it expects to operate on an API that follows
// the Kubernetes conventions, but may not be the Kubernetes API.
func RESTClientFor(config *Config) (*RESTClient, error) {
	if config.GroupVersion == nil {
		return nil, fmt.Errorf("GroupVersion is required when initializing a RESTClient")
	}
	if config.NegotiatedSerializer == nil {
		return nil, fmt.Errorf("NegotiatedSerializer is required when initializing a RESTClient")
	}
	qps := config.QPS
	if config.QPS == 0.0 {
		qps = DefaultQPS
	}
	burst := config.Burst
	if config.Burst == 0 {
		burst = DefaultBurst
	}

	baseURL, versionedAPIPath, err := defaultServerUrlFor(config)
	if err != nil {
		return nil, err
	}

	transport, err := TransportFor(config)
	if err != nil {
		return nil, err
	}

	var httpClient *http.Client
	if transport != http.DefaultTransport {
		httpClient = &http.Client{Transport: transport}
		if config.Timeout > 0 {
			httpClient.Timeout = config.Timeout
		}
	}

	return NewRESTClient(baseURL, versionedAPIPath, config.ContentConfig, qps, burst, config.RateLimiter, httpClient)
}
```

下面的`Demo`就描述如何生成一个RESTClient，并用该RESTClient获取某具体rc的详细信息。
```go
package main
import (
        "flag"
        "fmt"
        "k8s.io/client-go/pkg/runtime"
        "k8s.io/client-go/pkg/runtime/serializer"
        "k8s.io/client-go/pkg/api"
        v1 "k8s.io/client-go/pkg/api/v1"
        "k8s.io/client-go/pkg/api/unversioned"
        "k8s.io/client-go/rest"
        "k8s.io/client-go/tools/clientcmd"
)
func main() {
        kubeconfig := flag.String("kubeconfig", "/home/fqhtool/bin/config", "Path to a kube config. Only required if out-of-cluster.")
        flag.Parse()
        config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
        if err != nil {
                fmt.Println("BuildConfigFromFlags error")
        }
        groupversion := &unversioned.GroupVersion{"", "v1"}
        config.GroupVersion = groupversion
        config.APIPath = "/api"
        config.ContentType = runtime.ContentTypeJSON
        config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: api.Codecs}
        restClient, err := rest.RESTClientFor(config)
        if err != nil {
                fmt.Println("RESTClientFor error")
        }
        rc := v1.ReplicationController{}
        err = restClient.Get().Resource("replicationcontrollers").Namespace("default").Name("registry").Do().Into(&rc)
        if err != nil {
                fmt.Println("error")
        }
        fmt.Println(rc)
}
```

### 基于Clientset生成eventClient
我们可以基于type Clientset struct获取如pod、event这些对象(eventClient、podClient)，在kubernetes一般的用法是

- /cmd/kubelet/app/server.go中直接基于config文件生成一个eventClient
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

- [client-go](https://github.com/kubernetes/client-go/tree/release-2.0)中的`Demo`  
k8s V1.5.2对应的client-go版本是v2.0
```go
package main
import (
        "flag"
        "fmt"
        "time"

        "k8s.io/client-go/kubernetes"
        "k8s.io/client-go/pkg/api/v1"
        "k8s.io/client-go/tools/clientcmd"
)

var (
        kubeconfig = flag.String("kubeconfig", "/home/fqhtool/bin/config", "absolute path to the kubeconfig file")
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

dynamicClient的生成方式参考`/client-go-2.0.0/dynamic/client_pool.go`
```go
// ClientForGroupVersion returns a client for the specified groupVersion, creates one if none exists. Kind
// in the GroupVersionKind may be empty.
func (c *clientPoolImpl) ClientForGroupVersionKind(kind unversioned.GroupVersionKind) (*Client, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	gv := kind.GroupVersion()

	// do we have a client already configured?
	if existingClient, found := c.clients[gv]; found {
		return existingClient, nil
	}

	// avoid changing the original config
	confCopy := *c.config
	conf := &confCopy

	// we need to set the api path based on group version, if no group, default to legacy path
	conf.APIPath = c.apiPathResolverFunc(kind)

	// we need to make a client
	conf.GroupVersion = &gv

	/*
		调用func NewClient生成dynamicClient
	*/
	dynamicClient, err := NewClient(conf)
	if err != nil {
		return nil, err
	}
	c.clients[gv] = dynamicClient
	return dynamicClient, nil
}

// NewClient returns a new client based on the passed in config. The
// codec is ignored, as the dynamic client uses it's own codec.
func NewClient(conf *rest.Config) (*Client, error) {
	// avoid changing the original config
	confCopy := *conf
	conf = &confCopy

	contentConfig := ContentConfig()
	contentConfig.GroupVersion = conf.GroupVersion
	if conf.NegotiatedSerializer != nil {
		contentConfig.NegotiatedSerializer = conf.NegotiatedSerializer
	}
	conf.ContentConfig = contentConfig

	if conf.APIPath == "" {
		conf.APIPath = "/api"
	}

	if len(conf.UserAgent) == 0 {
		conf.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	cl, err := rest.RESTClientFor(conf)
	if err != nil {
		return nil, err
	}

	return &Client{cl: cl}, nil
}

func (c *Client) Resource(resource *unversioned.APIResource, namespace string) *ResourceClient {
	return &ResourceClient{
		cl:             c.cl,
		resource:       resource,
		ns:             namespace,
		parameterCodec: c.parameterCodec,
	}
}
//后面就靠ResourceClient的接口了
// ResourceClient is an API interface to a specific resource under a
// dynamic client.
type ResourceClient struct {
	cl             *rest.RESTClient
	resource       *unversioned.APIResource
	ns             string
	parameterCodec runtime.ParameterCodec
}
```

下面的`Demo`就描述如何使用dynamicClient
```go
package main

import (
	"flag"
	"fmt"
	"reflect"

	"encoding/json"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var kubeconfig = flag.String("kubeconfig", "/home/fqhtool/bin/config", "the abs path to the kubeconfig")
	flag.Parse()
	//第一个参数masterUrl填""即可，将会使用config文件中的server
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		fmt.Println("build config error.", err)
		return
	}
	
	//GroupVersion is required when initializing a RESTClient
	config.GroupVersion = &unversioned.GroupVersion{Group: "", Version: "v1"}
	dynamicClient, err := dynamic.NewClient(config)
	if err != nil {
		fmt.Println("build dynamicClient error.", err)
		return
	}

	fmt.Println("获取node资源，非namespace资源")
	reousrce := unversioned.APIResource{
		Name:       "nodes", //复数
		Namespaced: false,
		Kind:       "Node",
	}
	obj, err := dynamicClient.Resource(&reousrce, "").List(&v1.ListOptions{})
	if err != nil {
		fmt.Println("list node error.", err)
		return
	}
	js, err := json.Marshal(reflect.ValueOf(obj).Elem().Interface())
	if err != nil {
		fmt.Println("Marshal error.", err)
		return
	}
	nodelist := v1.NodeList{}
	json.Unmarshal(js, &nodelist)
	fmt.Println(nodelist)

	fmt.Println("获取pod资源，namespace资源")
	reousrce = unversioned.APIResource{
		Name:       "pods",
		Namespaced: true,
		Kind:       "Pod",
	}
	obj, err = dynamicClient.Resource(&reousrce, "").List(&v1.ListOptions{})
	if err != nil {
		fmt.Println("list node error.", err)
		return
	}
	js, err = json.Marshal(reflect.ValueOf(obj).Elem().Interface())
	if err != nil {
		fmt.Println("Marshal error.", err)
		return
	}
	podlist := v1.PodList{}
	json.Unmarshal(js, &podlist)
	fmt.Println(podlist)

}
```

## 使用Clientset创建删除一个pod
参考/k8s.io/client-go/kubernetes/typed/core/v1/core_client.go
```go
package main

import (
	"flag"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeconfig = flag.String("kubeconfig", "/home/fqhtool/bin/config", "absolute path to the kubeconfig file")
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
	//自定义创建一个pod
	desirepod := v1.Pod{
		//通用必备属性TypeMeta和ObjectMeta
		TypeMeta: unversioned.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{
			Name:      "fqh-test-pod",
			Namespace: "default",
			Labels:    map[string]string{"name": "testapi"},
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyAlways,
			Containers: []v1.Container{
				v1.Container{
					Name:  "testapi",
					Image: "registry:v1",
					Ports: []v1.ContainerPort{
						v1.ContainerPort{
							ContainerPort: 80,
							Protocol:      v1.ProtocolTCP,
						},
					},
				},
			},
		},
	}

	newpod, err := clientset.Core().Pods("default").Create(&desirepod)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("create pod successful")
	//根据pod的名字来获取pod信息
	pod_handler, err := clientset.Core().Pods("default").Get(newpod.ObjectMeta.Name)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(pod_handler)

	//自定义创建一个namespace
	desirenamespace := v1.Namespace{
		TypeMeta: unversioned.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{
			Name:   "fqh-test-ns",
			Labels: map[string]string{"name": "testapi"},
		},
	}
	newnamespace, err := clientset.Core().Namespaces().Create(&desirenamespace)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("create ns successful", newnamespace)

	/*
		删除刚创建的pod
		需要加上UID保证唯一性
		Preconditions must be fulfilled before an operation (update, delete, etc.) is carried out.
	*/
	err = clientset.Core().Pods("default").Delete(
		pod_handler.ObjectMeta.Name,
		&v1.DeleteOptions{
			Preconditions: &v1.Preconditions{
				UID: &pod_handler.ObjectMeta.UID,
			},
		},
	)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("delete pod successful")

	/*
		删除namespace
	*/
	err = clientset.Core().Namespaces().Delete(
		newnamespace.ObjectMeta.Name,
		&v1.DeleteOptions{
			Preconditions: &v1.Preconditions{
				UID: &newnamespace.ObjectMeta.UID,
			},
		},
	)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("delete Namespaces successful")

}
```

## 总结
RESTClient是Kubernetes最基础的Client，封装了一个http client。

restclient 是dynamic client和clientset的基础，支持json与protobuf，可以访问所有资源，实现对自定义thirdpartresource资源的获取。

clientset的用法比较简单，而且也是k8s里面用得最多的。

client-go除了提供clientset的连接方式，还提供了dynamic client 和restful api的连接方式与apiserver交互。
通过dynamic client可以访问所有资源（包括thirdpartresource所能提供的资源）。

同时可以通过Clientset生成各种资源的Client，进行相关操作。

最后可以通过学习client-go这个包，能更简易地对k8s的内部机制进行了解。


## 参考
[使用client-go进行k8s相关操作](http://blog.csdn.net/yevvzi/article/details/54380944)





