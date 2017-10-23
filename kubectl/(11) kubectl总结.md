# The summary of kubectl

## part 1
kubectl是基于package cobra来进行命令的构造。
每次执行kubectl命令时候，都会通过Factory 来重新获取Apiserver的gvk、restmapper这些信息，存储到cache目录/root/.kube/cache/discovery/localhost_8080下。

根据cmd的参数来生成一个Builder，type Builder struct提供了从命令行获取arguments和参数的函数接口，
并将其转换为一系列的resources，以便可以迭代使用Visitor interface。
通过多个Visitor的嵌套来进info信息进行过滤。

Builder的Do()函数最后会返回一个type Result struct，这里面存储的就是执行kubectl命令的返回结果。
最后会通过获取特定的Printer(describer)来对信息进行格式化输出。

## part 2
通过对event的定义和输出的了解，可以知道kubernetes中定义一个资源的通用必备属性：unversioned.TypeMeta和ObjectMeta。

然后研究event的流向，可以发现kubernetes通过list-watch机制进行消息的传递。
Broadcaster广播机制也是list-watch机制的一种实现方式。
各个组件都会向Apiserver发送自身产生的event。
kubernetes会通过EventCorrelator对event进行聚合和去重的处理，以避免由于event的数据量过大而造成系统的不稳定。

kubernetes是一个`level driven(state)`的系统，并非一个`edge driven(event)`系统。
也就是会说，k8s不是一接收到信号，就会立马触发某个事件。
而是，系统声明了这么一个信息，然后在未来一段时间里面，系统会根据这个信息做出相应的处理，这是一种`声明式`的处理方式。

![kubernetes-controller](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/kubernetes-controller.png)

## part 3
最后通过的client-go的使用，可以更加简易地了解到kubernetes的内部核心机制。
RESTClient是Kubernetes最基础的Client，封装了一个http client。 restclient 是dynamic client和clientset的基础。
重点学习和掌握clientset的用法，根据其衍生出各种实体资源的Client(PodClient...)。

