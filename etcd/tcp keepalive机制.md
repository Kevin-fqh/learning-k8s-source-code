# tcp keepalive

## 背景
在测试kubernetes(V1.5.2)稳定性的过程中，发现一个问题。
一个apiserver，后端连着3个etcd servers，假设分别起在4张独立的网卡上。然后对3个etcd进行拔网线测试。

因为etcd官方提供的`/github.com/coreos/etcd/client`默认采用的是随机选择一个etcd节点来和Apiserver进行通信，
故以为不应该出现什么异常情况。

### 理想的情况
原来以为只要保证etcd节点可用的个数不少于2的情况下，apiserver这边都能正常运行。
随便执行kubectl get/create 等操作都能得到正确的结果。

### 测试结果
理想和现实总是有点差距的。测试的结果是偶尔会出现卡顿的情况，无论是get操作，还是create操作。
有的时候，甚至出现部分数据错误、丢失的情况，比如说明明拔掉的是Node A的网线，但执行`kubectl get node`的时候得到NotReady的节点却是`Node A和B`。
但如果出现卡顿等异常的情况下，都要20分钟左右就会自主恢复。Apiserver会进行重新List-Watch，集群恢复正常。

我在拔etcd 集群的leader节点的时候，每次都能出现卡顿等异常情况。

拔非leader节点的时候，并没有出现异常。

### 分析和改进
一开始的情况下，猜测是Apiserver不能正确获取etcd集群的数据。但为什么呢？
一个etcd节点已经和Apiserver失联了，
Apiserver使用的是etcd官方提供的`/github.com/coreos/etcd/client`来和etcd集群进行通信的，那么应该能正确获悉etcd集群的状态，及时更新通信节点，获取新的数据。

在分析`/github.com/coreos/etcd/client`过程中，发现了client有个超时参数，然而kubernetes在使用的时候，并没有进行设置，而是采用了默认值0，也就是说没有超时设置。好的，我来给它加上，设置超时参数：
```go
//kubernetes-1.5.2/pkg/storage/storagebackend/factory/etcd2.go

func newETCD2Client(tr *http.Transport, serverList []string) (etcd2client.Client, error) {
	cli, err := etcd2client.New(etcd2client.Config{
		Endpoints: serverList,
		Transport: tr,
		//		这地方加上超时属性？以应对ifdown 拔网线apiserver访问etcd卡住的情况
		HeaderTimeoutPerRequest: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	return cli, nil
}
```

### 再进行测试
1. 有效果了，设置了超时参数的情况下，拔掉etcd 集群的leader节点的网线。此时`kubectl get xx`等读操作立马就能反映过程，得到的都是正确的数据和状态。

2. 执行`kubectl create -f rc.yaml`的时候出现问题了。。。rc创建成功了，rc的数据也成功写入了etcd数据库中。但pod并没有成功创建，查询etcd数据库中的数据，也没有pod的数据。

3. 理想的情况下，存在于被拔掉网线的那个节点上的pod应该会被迁移到别的节点上。然而并没有，这些pod状态会很快地变为Unknow之后，并没有迁移到别的正常节点上。可以排除是什么配额之类的原因。

### 再分析
etcd/client prefers to use the same endpoint as long as the endpoint continues to work well. This saves socket resources, and improves efficiency for both client and server side. This preference doesn't remove consistency from the data consumed by the client because data replicated to each etcd member has already passed through the consensus process.

etcd/client does round-robin rotation on other available endpoints if the preferred endpoint isn't functioning properly. For example, if the member that etcd/client connects to is hard killed, etcd/client will fail on the first attempt with the killed member, and succeed on the second attempt with another member. If it fails to talk to all available endpoints, it will return all errors happened.

Default etcd/client cannot handle the case that the remote server is SIGSTOPed now. TCP keepalive mechanism doesn't help in this scenario because operating system may still send TCP keep-alive packets. Over time we'd like to improve this functionality, but solving this issue isn't high priority because a real-life case in which a server is stopped, but the connection is kept alive, hasn't been brought to our attention.

etcd/client cannot detect whether a member is healthy with watches and non-quorum read requests. If the member is isolated from the cluster, etcd/client may retrieve outdated data. Instead, users can either issue quorum read requests or monitor the /health endpoint for member health information.

这里提到了etcd使用了TCP的keep-alive机制，所以下面来分析一下keep-alive机制。

## Keepalive是什么
tcp keepaliver，即保活器。

TCP并不会去主动检测连接的丢失，这意味着，如果双方不产生交互，那么如果网络断了或者有一方机器崩溃，另外一方将永远不知道连接已经不可用了。

Keepalive是很多的TCP实现提供的一种机制，它允许连接在空闲的时候双方会发送一些特殊的数据段，并通过响应与否来判断连接是否还存活着。

Keepalive能够保证TCP连接一直保持，但是TCP的保活定时器不是每个TCP/IP协议栈就实现了，因为RFC并不要求TCP保活定时器一定要实现。
但其实很多实现都提供了Keepalive机制。

keepalive就是用来检测一个tcp connection是否还连接正常。
当一个tcp connection建立好之后，如果双方都不发送数据的话，tcp协议本身是不会发送其它的任何数据的。
也就是说，在一个空闲的connection上，两个socket之间不产生任何的数据交换。

言外之意就是我们只要启动一个客户端进程，同服务器建立了TCP连接，不管你离开几小时，几天，几星期或是几个月，连接依旧存在。
中间的路由器可能崩溃或者重启，电话线可能go down或者back up，只要连接两端的主机没有重启，连接依旧保持建立。

这种情况下认为不管是客户端的还是服务器端的应用程序都没有应用程序级（application-level）的定时器来探测连接的不活动状态（inactivity），从而引起任何一个应用程序的终止。

## keepalive缺点
- 在出现短暂差错的情况下，这可能会使一个非常好的连接释放掉
- 它们耗费不必要的带宽
- 在按分组计费的情况下会在互联网上花掉更多的钱

keepalive没有办法区分出到底是由于对方的程序意外终止还是由于网络故障而导致的connection的失效。

如果两个终端系统之间的某个中间网络上有连接的暂时中断，那么存活选项（option）就能够引起两个进程间一个良好连接的终止。
例如，如果正好在某个中间路由器崩溃、重启的时候发送存活探测，TCP就将会认为客户端主机已经崩溃，但事实并非如此。

keepalive还有一个缺点就是，设置SO_KEEPALIVE参数的时候，默认时间挺长的。
如果自主设置测试时间, 会影响其它开了这个选项的tcp连接。
也就是说这个时间参数是一个全局参数，并不是单单针对其中的某一个tcp连接。

## 一个场景
server和client建立了一个connection，server负责接收client的request。
当connection建立好之后，client由于某种原因机器停机了，但此时server端并不知道。
所以server就会一直监听着这个connection，但其实这个connection已经失效了。

keepalive就是为这样的场景准备的。
当把一个socket设置成了keepalive，那么这个socket空闲一段时间后，它就会向对方发送数据来确认对方仍然存在。
放在上面的例子中，如果client停机了，那么server所发送的keepalive数据就不会有response，这样server就能够确认client完蛋了（至少从表面上看是这样）。

## keepalive的最佳应用场景
其实keepalive在实际的应用中并不常见。为何如此？

这得归结于keepalive设计的初衷: **Keepalive适用于清除死亡时间比较长的连接**。 

一个用户创建tcp连接访问了一个web服务器，当用户完成他执行的操作后，很粗暴的直接拨了网线。
这种情况下，这个tcp连接已经断开了，但是web服务器并不知道，它会依然守护着这个连接。
如果web server设置了keepalive，那么它就能够在用户断开网线的大概几个小时以后，确认这个连接已经中断，然后丢弃此连接，回收资源。

采用keepalive，它会先要求此连接一定时间没有活动（一般是几个小时），然后发出数据段，
经过多次尝试后（每次尝试之间也有时间间隔），如果仍没有响应，则判断连接中断。
可想而知，整个周期需要很长的时间。
所以，如前面的场景那样，需要一种方法能够清除和回收那些在系统不知情的情况下死去了很久的连接，keepalive是非常好的选择。

## heart beart
想知道某connection是否失效，除了keepalive还有其它的一些办法，比如heartbeat，或者自己发送检测信息等等。

在大部分情况下，特别是分布式环境中，我们需要的是一个能够快速或者实时监控连接状态的机制，这里，heart-beat才是更加合适的方案。

- keepalive和heart beart的类似
Heart-beat（心跳），它的原理和keepalive非常类似，都是发送一个信号给对方，如果多次发送都没有响应的话，则判断连接中断。
- keepalive和heart beart的差异
它们的不同点在于，keepalive是tcp实现中内建的机制，是在创建tcp连接时通过设置参数启动keepalive机制；
而heart-beat则需要在tcp之上的应用层实现。

一个简单的heart-beat实现一般测试连接是否中断采用的时间间隔都比较短，可以很快的决定连接是否中断。
并且，由于是在应用层实现，因为可以自行决定当判断连接中断后应该采取的行为，而keepalive在判断连接失败后只会将连接丢弃。

关于heart-beat，一个非常有趣的问题是，应该在传输真正数据的连接中发送“心跳”信号，还是可以专门创建一个发送“心跳”信号的连接？
比如说，A，B两台机器之间通过连接m来传输数据，现在为了能够检测A，B之间的连接状态，我们是应该在连接m中传输“心跳”信号，还是创建新的连接n来专门传输“心跳”呢？
我个人认为两者皆可。如果担心的是端到端的连接状态，那么就直接在该条连接中实现“心跳”。
但很多时候，关注的是网络状况和两台主机间的连接状态，这种情况下，创建专门的“心跳”连接也未尝不可。 

## 后续
目前这个问题还没有彻底解决，还在研究过程中。如果出现了上面所说的异常情况，目前可以通过下面两个方法来进行恢复：
1. 重启Apiserver
2. 在Apiserver端使用tcpkill命令，把Apiserver和该etcd节点建立的tcp socket连接暴力kill掉
3. 把tcp keepalive机制换成心跳机制来完成一个应用层的检测？？




