# 定制API之controller

前面已经完善了apiserver和kubectl，现在我们接着来完善kube-controller。

## 初始化
这个和kubectl中是一样的，如果已经完成kubectl，那么这步略过。

`/pkg/client/clientset_generated/internalclientset/import_known_versions.go` 和 `/pkg/client/clientset_generated/release_1_5/import_known_versions.go`中完成Group的初始化安装，这个地方自动化生成代码工具居然没有更新。。。
```
_ "k8s.io/kubernetes/pkg/apis/premierleague/install"
```

## match controller
把`premierleague.Match`资源视为独占资源，不需要与其它controller共享，所以不使用shareinformer来获取。

新建文件`/pkg/controller/match/match_controller.go`，代码实现如下：
```go
/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package match

import (
	"fmt"
	"reflect"
	"time"

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/premierleague"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/metrics"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/util/workqueue"
	"k8s.io/kubernetes/pkg/watch"
)

type MatchController struct {
	kubeClient      clientset.Interface             //负责与ApiServer通信
	lwController    *cache.Controller               //负责执行List-Watch
	matchStoreIndex cache.Store                     //存放所有List-Watch到的match对象
	queue           workqueue.RateLimitingInterface //存放等到sync的match对象,会被worker()消费
	syncHandler     func(key string) error          //由worker调用，负责处理queue中match对象
}

const (
	FullControllerResyncPeriod = 10 * time.Minute
)

func NewMatchController(kubeClient clientset.Interface) *MatchController {
	//向Metric注册match_controller
	if kubeClient != nil && kubeClient.Core().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("match_controller", kubeClient.Core().RESTClient().GetRateLimiter())
	}
	matchController := &MatchController{
		kubeClient: kubeClient,
		queue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "match"),
	}

	//使用一个通用的controller框架来执行List-Watch
	indexer, controller := cache.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return matchController.kubeClient.Premierleague().Matchs(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return matchController.kubeClient.Premierleague().Matchs(api.NamespaceAll).Watch(options)
			},
		},
		&premierleague.Match{},
		FullControllerResyncPeriod,
		//向controller注册Hander函数
		cache.ResourceEventHandlerFuncs{
			AddFunc:    matchController.addMatch,
			UpdateFunc: matchController.updateMatch,

			DeleteFunc: matchController.deleteMatch,
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	matchController.matchStoreIndex = indexer
	matchController.lwController = controller
	matchController.syncHandler = matchController.SyncMatch
	return matchController
}

func (mc *MatchController) addMatch(obj interface{}) {
	fmt.Println("触发Add动作")
	mc.enqueueMatch(obj)
}
func (mc *MatchController) updateMatch(old, cur interface{}) {
	fmt.Println("触发Update动作")
	fmt.Println("%s change to %s", reflect.ValueOf(old), reflect.ValueOf(cur))
	mc.enqueueMatch(cur)
}
func (mc *MatchController) deleteMatch(obj interface{}) {
	fmt.Println("触发Delete动作")
	mc.enqueueMatch(obj)
}

func (mc *MatchController) enqueueMatch(obj interface{}) {
	//排队等待sync
	key, err := controller.KeyFunc(obj)
	if err != nil {
		glog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}
	mc.queue.Add(key)
}

func (mc *MatchController) SyncMatch(key string) error {
	//处理一个item
	startTime := time.Now()
	defer func() {
		glog.V(0).Infof("Finished syncing match %q (%v)", key, time.Now().Sub(startTime))
	}()

	obj, exists, err := mc.matchStoreIndex.GetByKey(key)
	if !exists {
		glog.Infof("Match has been deleted %v", key)
		return nil
	}
	if err != nil {
		/*
			如果从store.Indexer中取值出错，把该key重新放入queue中
		*/
		glog.Infof("Unable to retrieve match %v from store: %v", key, err)
		mc.queue.Add(key)
		return err
	}

	//obj转化为premierleague.Match
	match := *obj.(*premierleague.Match)
	fmt.Println("将要发送给apiserver的match如下: ", reflect.ValueOf(match))
	/*
		调用clientSet的update或updateStatus，把信息发送给ApiServer，更新该match对象
	*/
	_, err = mc.kubeClient.Premierleague().Matchs(match.Namespace).Update(&match)
	return err
}

func (mc *MatchController) worker() {
	//调用mc.syncHandler，即SyncMatch()
	//消费queue
	work := func() bool {
		key, quit := mc.queue.Get()
		if quit {
			return true
		}
		defer mc.queue.Done(key)

		err := mc.syncHandler(key.(string))
		if err == nil {
			//该item在开始处理前就已经完成了，无论是成功还是失败
			//==>pkg/util/workqueue/rate_limitting_queue.go
			mc.queue.Forget(key)
			return false
		}

		mc.queue.AddRateLimited(key)
		utilruntime.HandleError(err)
		return false
	}

	for {
		if quit := work(); quit {
			return
		}
	}
}

func (mc *MatchController) Run(workers int, stopCh <-chan struct{}) {
	//执行worker goroutine
	defer utilruntime.HandleCrash()
	defer mc.queue.ShutDown()
	glog.Infof("Starting match controller")

	go mc.lwController.Run(stopCh)

	for i := 0; i < workers; i++ {
		go wait.Until(mc.worker, time.Second, stopCh)
	}

	<-stopCh
	glog.Infof("Shutting down match controller")
}
```

## 启动controller
在`/cmd/kube-controller-manager/app/controllermanager.go`中启动matchController
```go
import (
	matchcontroller "k8s.io/kubernetes/pkg/controller/match"
)

func StartControllers(s *options.CMServer, kubeconfig *restclient.Config, rootClientBuilder, clientBuilder controller.ControllerClientBuilder, stop <-chan struct{}, recorder record.EventRecorder) error {
	...
	...
	/*
		启动matchController
	*/
	go matchcontroller.NewMatchController(
		clientBuilder.ClientOrDie("match-controller"),
	).Run(1, wait.NeverStop)
	time.Sleep(wait.Jitter(s.ControllerStartInterval.Duration, ControllerStartJitter))
	...
	...
}
```

## End
premierleague-match.yaml内容如下
```yaml
apiVersion: premierleague.k8s.io/v1
kind: Match
metadata:
  name: fqhmatch
  namespace: default
spec:
  host: A
  guest: B
```

1. 执行`kubectl create -f premierleague-match.yaml`，kube-controller-manager的日志输出如下
```
触发Add动作
将要发送给apiserver的match如下:  {{ } {fqhmatch  default /apis/premierleague.k8s.io/v1/namespaces/default/matchs/fqhmatch 67994800-e7cf-11e7-88d3-080027e58fc6 860 1 2017-12-23 05:52:44 -0500 EST <nil> <nil> map[] map[] [] [] } {A B}}
触发Update动作
%s change to %s &{{ } {fqhmatch  default /apis/premierleague.k8s.io/v1/namespaces/default/matchs/fqhmatch 67994800-e7cf-11e7-88d3-080027e58fc6 860 1 2017-12-23 05:52:44 -0500 EST <nil> <nil> map[] map[] [] [] } {A B}} &{{ } {fqhmatch  default /apis/premierleague.k8s.io/v1/namespaces/default/matchs/fqhmatch 67994800-e7cf-11e7-88d3-080027e58fc6 861 1 2017-12-23 05:52:44 -0500 EST <nil> <nil> map[] map[] [] [] } {A B}}
I1223 05:52:44.073928   26447 match_controller.go:115] Finished syncing match "default/fqhmatch" (17.882353ms)
将要发送给apiserver的match如下:  {{ } {fqhmatch  default /apis/premierleague.k8s.io/v1/namespaces/default/matchs/fqhmatch 67994800-e7cf-11e7-88d3-080027e58fc6 861 1 2017-12-23 05:52:44 -0500 EST <nil> <nil> map[] map[] [] [] } {A B}}
I1223 05:52:44.076619   26447 match_controller.go:115] Finished syncing match "default/fqhmatch" (2.633394ms)
```

2. 执行`kubectl delete match fqhmatch`，kube-controller-manager的日志输出如下
```
触发Delete动作
I1223 05:55:43.992228   26447 match_controller.go:120] Match has been deleted default/fqhmatch
I1223 05:55:43.992240   26447 match_controller.go:115] Finished syncing match "default/fqhmatch" (21.771µs)
```
