# qps限流

k8s v1.7.9

## 简介

kube-controller-manager 和 kube-scheduler 会对发送给apiserver的流量进行限速，下面梳理一下其原理。

```
--kube-api-burst int32            Burst to use while talking with kubernetes apiserver (default 30)

--kube-api-qps float32            QPS to use while talking with kubernetes apiserver (default 20)
```

其功能见下面的[flowcontrol的RateLimiter](#flowcontrol的ratelimiter)

## 新建一个clientset

clientset携带的QPS、Burst参数限定了该clientset和Apiserver通信的速度

```
// Override kubeconfig qps/burst settings from flags
kubeconfig.QPS = s.KubeAPIQPS
kubeconfig.Burst = int(s.KubeAPIBurst)
kubeClient, err := clientset.NewForConfig(restclient.AddUserAgent(kubeconfig, "controller-manager"))
```

## func NewForConfig

clientset的RateLimiter调用了`/vendor/k8s.io/client-go/util/flowcontrol/throttle.go`的`NewTokenBucketRateLimiter`

```go
// NewForConfig creates a new Clientset for the given config.
func NewForConfig(c *rest.Config) (*Clientset, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	var cs Clientset
	var err error
	cs.AdmissionregistrationV1alpha1Client, err = admissionregistrationv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.CoreV1Client, err = corev1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	...
	...
	...
}

```

## flowcontrol的RateLimiter

flowcontrol的RateLimiter基于token bucket实现了一个 rate limiter。
该rate limiter允许最大的流量为`burst`（理论上应该大于QPS），但会维持一个平滑的速率QPS。

Bucket初始化的时候会填充`burst`个token，然后以`qps`的速率往里面填充token。 
Bucket中最大的token数目为`burst`个。

一个request只有得到一个token，才会被该rate limiter放行。

见`/vendor/k8s.io/client-go/util/flowcontrol/throttle.go`

```go
// NewTokenBucketRateLimiter creates a rate limiter which implements a token bucket approach.
// The rate limiter allows bursts of up to 'burst' to exceed the QPS, while still maintaining a
// smoothed qps rate of 'qps'.
// The bucket is initially filled with 'burst' tokens, and refills at a rate of 'qps'.
// The maximum number of tokens in the bucket is capped at 'burst'.
func NewTokenBucketRateLimiter(qps float32, burst int) RateLimiter {
	limiter := ratelimit.NewBucketWithRate(float64(qps), int64(burst))
	return newTokenBucketRateLimiter(limiter, qps)
}


type RateLimiter interface {
	// TryAccept returns true if a token is taken immediately. Otherwise,
	// it returns false.
	/*
		不等待询问“现在是否有空闲的token可用”，return true/false
	*/
	TryAccept() bool
	/*
		一直阻塞，直到到有空闲的token可用，才return
	*/
	// Accept returns once a token becomes available.
	Accept()
	// Stop stops the rate limiter, subsequent calls to CanAccept will return false
	/*
		停止rateLimiter，但随后对accept()的调用将返回false
	*/
	Stop()
	// Saturation returns a percentage number which describes how saturated
	// this rate limiter is.
	// Usually we use token bucket rate limiter. In that case,
	// 1.0 means no tokens are available; 0.0 means we have a full bucket of tokens to use.
	/*
		Saturation返回一个百分比数字，描述该rate limiter的饱和度。
		对于token bucket rate limiter而言，
			1.0表示没有可用的令牌;
			0.0表示有一整桶令牌可供使用。
	*/
	Saturation() float64
	// QPS returns QPS of this rate limiter
	QPS() float32
}

func newTokenBucketRateLimiter(limiter *ratelimit.Bucket, qps float32) RateLimiter {
	return &tokenBucketRateLimiter{
		limiter: limiter,
		qps:     qps,
	}
}

type tokenBucketRateLimiter struct {
        limiter *ratelimit.Bucket
        qps     float32
}

func (t *tokenBucketRateLimiter) TryAccept() bool {
	return t.limiter.TakeAvailable(1) == 1
}

func (t *tokenBucketRateLimiter) Saturation() float64 {
	capacity := t.limiter.Capacity()
	avail := t.limiter.Available()
	return float64(capacity-avail) / float64(capacity)
}

// Accept will block until a token becomes available
func (t *tokenBucketRateLimiter) Accept() {
	t.limiter.Wait(1)
}

func (t *tokenBucketRateLimiter) Stop() {
}

func (t *tokenBucketRateLimiter) QPS() float32 {
	return t.qps
}
```

### 第三方库 juju-ratelimit

flowcontrol的底层实现调用的是第三方库[github.com/juju/ratelimit](https://github.com/juju/ratelimit)，该库的实现比较简单。 
由于Clock的关系，如果qps设置得过高，真实的速率可能存在`1%的偏差`。

该库提供的方法是线程安全的。

```go
// NewBucketWithRate returns a token bucket that fills the bucket
// at the rate of rate tokens per second up to the given
// maximum capacity. Because of limited clock resolution,
// at high rates, the actual rate may be up to 1% different from the
// specified rate.
func NewBucketWithRate(rate float64, capacity int64) *Bucket {
	return NewBucketWithRateAndClock(rate, capacity, realClock{})
}

// Bucket represents a token bucket that fills at a predetermined rate.
// Methods on Bucket may be called concurrently.
type Bucket struct {
	startTime    time.Time
	capacity     int64
	quantum      int64
	fillInterval time.Duration
	clock        Clock

	// The mutex guards the fields following it.
	mu sync.Mutex

	// avail holds the number of available tokens
	// in the bucket, as of availTick ticks from startTime.
	// It will be negative when there are consumers
	// waiting for tokens.
	avail     int64
	availTick int64
}

// Wait takes count tokens from the bucket, waiting until they are
// available.
func (tb *Bucket) Wait(count int64) {
	if d := tb.Take(count); d > 0 {
		tb.clock.Sleep(d)
	}
}

// Sleep is identical to time.Sleep.
func (realClock) Sleep(d time.Duration) {
	time.Sleep(d)
}
```

## controller-manager对ratelimiter的使用

以`endpoints`资源为例，了解controller-manager对ratelimiter的使用，见`/kubernetes-1.7.9/pkg/client/clientset_generated/clientset/typed/core/v1/endpoints.go`

```go
// Update takes the representation of a endpoints and updates it. Returns the server's representation of the endpoints, and an error, if there is any.
func (c *endpoints) Update(endpoints *v1.Endpoints) (result *v1.Endpoints, err error) {
	result = &v1.Endpoints{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("endpoints").
		Name(endpoints.Name).
		Body(endpoints).
		Do().
		Into(result)
	return
}

/*
 Do()方法
*/

func (r *Request) Do() Result {
	/*
		限速设置
	*/
	r.tryThrottle()

	var result Result
	err := r.request(func(req *http.Request, resp *http.Response) {
		result = r.transformResponse(resp, req)
	})
	if err != nil {
		return Result{err: err}
	}
	return result
}

func (r *Request) tryThrottle() {
	now := time.Now()
	if r.throttle != nil {
		r.throttle.Accept()
	}
	/*
		如果等待的时间超过了longThrottleLatency 50ms，glog打印
	*/
	if latency := time.Since(now); latency > longThrottleLatency {
		glog.V(4).Infof("Throttling request took %v, request: %s:%s", latency, r.verb, r.URL().String())
	}
}

	// longThrottleLatency defines threshold for logging requests. All requests being
	// throttle for more than longThrottleLatency will be logged.
	longThrottleLatency = 50 * time.Millisecond
```

可以发现，其实是调用了flowcontrol的RateLimiter的Accept()，即一个request会被一直阻塞，直到其获取到空闲的token，才会被放行。





