# iptables

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [简介](#简介)
  - [Chain](#chain)	
  - [Rule](#rule)
    - [匹配条件](#匹配条件)
	- [处理动作](#处理动作)
  - [Table](#table)
  - [filter表、nat表、mangle表的作用总结](#filter表-nat表-mangle表的作用总结)
  - [结合kube-proxy源码分析](#结合kube-proxy源码分析)
  - [iptables基本使用命令](#iptables基本使用命令)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 简介
iptables 并不是并不是真正的防火墙，而是一个客户端工具。 用户通过iptables去操作真正的防火墙 netfilter。 
netfilter位于内核态。 
netfilter/iptables 组合提供封包过滤、封包重定向和NAT等功能。

## Chain
![iptables](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/iptables-1.png)

Chain： prerouting、forward、postrouting ＋ input、output

一个报文只有符合一个Chain上所有的匹配Rule，才会被放行(执行相应动作)。

## Rule
每个Chain中可能会存在Rule（0条或者多条），一个报文只有匹配一个Chain上面的Rule，才会执行相应的动作。
一个Chain上所有的规则都会按顺序进行一次匹配过滤。

一条规则最重要的就是其中的匹配条件和动作。

### 匹配条件
匹配条件分为基本条件和扩展匹配条件：
- 基本条件：源地址和目的地址
- 扩展条件： 如源端口和目标端口等，需要相应内核模块的支持

### 处理动作
当一个报文符合一条Rule的时候，会执行相应的动作：
- ACCEPT
- DROP，直接丢弃，不会给client端任何回应，client端只能因为超时再做出反应
- REJECT，拒绝，会告诉client端
- SNAT，源地址转换，一般用于内网朝公网发送信息
- DNAT，目的地址转换，目的网络地址转换
- MASQUERADE，SANT的一种特殊形式，适用于动态的、临时会变的IP上
- REDIRECT，在本机做端口映射
- LOG，记录信息在/var/log/message，然后让下一跳Rule继续处理该报文

一个SNAT例子： 当公网宿主机接收到来自于172.1.0.0/24的报文时，把报文的源IP地址替换成公网宿主机的出口网卡eth1的ip。 从而实现在内网访问外网
```shell
# 在公网宿主机上设置下面路由规则
iptables -t nat -A POSTROUTING -s 172.1.0.0/24 -o eth1 -j MASQUERADE
```

一个DNAT例子：当公网宿主机的80端口接收到tcp请求之后，转发到内网的地址 172.1.10.2:80 。从而实现内网的服务可以被外网访问
```shell
# 在公网宿主机上设置下面路由规则
iptables -t -nat -A PREROUTING -p tcp -m tcp --dport 80 -j DNAT --to-destination 172.1.10.2:80
```

## Table
把具有相同功能的Rule放到一起，称之为一个Table。 都需要相应内核模块的支持。

Table 有4个，优先顺序从高到低是按照：raw->managle->nat->filter 
- raw 表，关闭nat表上启用的连接追踪机制
- managle 表，拆解报文，做出修改，并重新封装
- nat 表，网络地址转换
- filter 表，负责过滤

有些Table是肯定不会包含某些Chain的规则。

**在实际使用过程中，一般是通过 Table 作为操作入口** ，对 Rule 进行定义。

用户可以自定义 Chain，在其中针对某个应用设计一些 Rule。 但自定义的Chain不能直接使用，需要通过系统默认Chain的调用才能生效(通过Rule 的动作)。

![iptables路由次序图](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/iptables路由次序图.png)

## filter表、nat表、mangle表的作用总结
**filter表** 是专门过滤包的，它内建三个链，可以毫无问题地对包进行DROP、LOG、ACCEPT和REJECT等操作。
- FORWARD链过滤所有不是本地产生的并且目的地不是本地的包，
- 而 INPUT 是针对那些目的地是本地的包。
- OUTPUT 用来过滤所有本地生成的包。

**nat表** 的主要用处是网络地址转换，即Network Address Translation，缩写为NAT。做过NAT操作的数据包的地址就被改变了，当然这种改变是根据我们的规则进行的。属于一个流的包只会经过这个表一次。如果第一个包被允许做NAT或Masqueraded，那么余下的包都会自动地被做相同的操作。也就是说，余下的包不会再通过这个表，一个一个的被NAT，而是自动地完成。
- PREROUTING 链的作用是在包刚刚到达防火墙时改变它的目的地址（DNAT），如果需要的话。
- OUTPUT链改变本地产生的包的目的地址（DNAT）。
- POSTROUTING链在包就要离开防火墙之前改变其源地址（SNAT）。
- DNAT 是做目的网络地址转换的，就是重写包的目的IP地址。如果一个包被匹配了，那么和它属于同一个流的所有的包都会被自动转换，然后就可以被路由到正确的主机或网络。DNAT是非常有用的。比如，你的Web服务器在LAN内部，而且没有可在Internet上使用的真实IP地址，那就可以使用DNAT让防火墙把所有到它自己HTTP端口的包转发给LAN内部真正的Web服务器。DNAT的目的地址也可以是一个范围，这样的话，DNAT会为每一个流随机分配一个地址。因此，我们可以用这个DNAT做某种类型的负载均衡。

**mangle表** 主要用来修改数据包。我们可以改变不同的包及包头的内容，比如TTL，TOS或MARK。 注意MARK并没有真正地改动数据包，它只是在内核空间为包设了一个标记。防火墙内的其他的规则或程序可以使用这种标记对包进行过滤或高级路由。这个表有五个内建的链：PREROUTING，POSTROUTING，OUTPUT，INPUT和FORWARD。
- PREROUTING在包进入防火墙之后、路由判断之前改变包，POSTROUTING是在所有路由判断之后。 
- OUTPUT在确定包的目的之前更改数据包。
- INPUT在包被路由到本地之后，但在用户空间的程序看到它之前改变包。
- FORWARD在最初的路由判断之后、最后一次更改包的目的之前mangle包。
- 注意，mangle表不能做任何NAT，它只是改变数据包的TTL，TOS或MARK。

## 结合kube-proxy源码分析
从上面iptables路由次序图，可以看出来，如果我们要让某台Linux主机充当路由和负载均衡角色的话，我们显然应该在该主机的`nat表的prerouting链`中对数据包做`DNAT`操作。

Kubernetes可以利用iptables来做针对service的路由和负载均衡，其核心逻辑是通过定义在`/kubernetes-1.5.2/pkg/proxy/iptables/proxier.go`中的函数syncProxyRules()来实现的。 

syncProxyRules()就是在service所在node的nat表的prerouting链中对数据包做DNAT操作。

Kubernetes通过在目标node的iptables中的nat表的PREROUTING和POSTROUTING链中创建一系列的自定义链，这些自定义链主要是：
  * “KUBE-SERVICES”链
  * “KUBE-POSTROUTING”链
  * 每个服务所对应的“KUBE-SVC-XXXXXXXXXXXXXXXX”链
  * “KUBE-SEP-XXXXXXXXXXXXXXXX”链  
然后通过这些自定义链对流经到该node的数据包做DNAT和SNAT操作以实现路由、负载均衡和地址转换。

```go
// This is where all of the iptables-save/restore calls happen.
// The only other iptables rules are those that are setup in iptablesInit()
// assumes proxier.mu is held
/*
	唯一两处对iptables规则进行设置的地方：
		syncProxyRules()和iptablesInit()
			==>/pkg/proxy/userspace/proxier.go
				==>func iptablesInit(ipt iptables.Interface) error	
*/
func (proxier *Proxier) syncProxyRules() {
	if proxier.throttle != nil {
		proxier.throttle.Accept()
	}
	start := time.Now()
	defer func() {
		glog.V(4).Infof("syncProxyRules took %v", time.Since(start))
	}()
	// don't sync rules till we've received services and endpoints
	if !proxier.haveReceivedEndpointsUpdate || !proxier.haveReceivedServiceUpdate {
		glog.V(2).Info("Not syncing iptables until Services and Endpoints have been received from master")
		return
	}
	glog.V(3).Infof("Syncing iptables rules")

	// Create and link the kube services chain.
	/*
		建立kube services Chain
		1、  分别在表filter和表nat中创建名为“KUBE-SERVICES”的自定义链
		2、  调用iptables对filter表的output链、对nat表的output和prerouting链创建了如下三条规则：
			-- 将经过filter表的output链的数据包重定向到自定义链KUBE-SERVICES中
			   iptables -I OUTPUT -m comment –comment “kubernetes service portals” -j KUBE-SERVICES
			-- 将经过nat表的prerouting链的数据包重定向到自定义链KUBE-SERVICES中
			   ***kubernetes这里要做DNAT！！！***
			   iptables –t nat -I PREROUTING -m comment –comment “kubernetes service portals” -j KUBE-SERVICES
			-- 将经过nat表的output链的数据包重定向到自定义链KUBE-SERVICES中
			   iptables –t nat –I OUTPUT -m comment –comment “kubernetes service portals” -j KUBE-SERVICES
	*/
	{
		tablesNeedServicesChain := []utiliptables.Table{utiliptables.TableFilter, utiliptables.TableNAT}
		for _, table := range tablesNeedServicesChain {
			/*
				确保自定义Chain “KUBE-SERVICES”存在于指定table中，
				如果不存在，负责创建它
					==>/pkg/util/iptables/iptables.go
						==>func (runner *runner) EnsureChain
			*/
			if _, err := proxier.iptables.EnsureChain(table, kubeServicesChain); err != nil {
				glog.Errorf("Failed to ensure that %s chain %s exists: %v", table, kubeServicesChain, err)
				return
			}
		}

		/*
			tableChainsNeedJumpServices定义了需要在
			filter表的output链、nat表的output和prerouting链创建Rule
		*/
		tableChainsNeedJumpServices := []struct {
			table utiliptables.Table
			chain utiliptables.Chain
		}{
			{utiliptables.TableFilter, utiliptables.ChainOutput},
			{utiliptables.TableNAT, utiliptables.ChainOutput},
			{utiliptables.TableNAT, utiliptables.ChainPrerouting},
		}
		comment := "kubernetes service portals"
		args := []string{"-m", "comment", "--comment", comment, "-j", string(kubeServicesChain)}
		for _, tc := range tableChainsNeedJumpServices {
			/*
				确保规则的存在
				==>/pkg/util/iptables/iptables.go
					==>func (runner *runner) EnsureRule
			*/
			if _, err := proxier.iptables.EnsureRule(utiliptables.Prepend, tc.table, tc.chain, args...); err != nil {
				glog.Errorf("Failed to ensure that %s chain %s jumps to %s: %v", tc.table, tc.chain, kubeServicesChain, err)
				return
			}
		}
	}

	// Create and link the kube postrouting chain.
	/*
		1. 在表nat中创建了名为“KUBE-POSTROUTING”的自定义Chain
		2. 调用iptables对nat表的postrouting链创建了如下规则：
		   -- 将经过nat表的postrouting链的数据包重定向到自定义链KUBE-POSTROUTING中
		      ***kubernetes这里要做SNAT***
			  iptables –t nat -I POSTROUTING -m comment –comment “kubernetes postrouting rules” -j KUBE-POSTROUTING
	*/
	{
		if _, err := proxier.iptables.EnsureChain(utiliptables.TableNAT, kubePostroutingChain); err != nil {
			glog.Errorf("Failed to ensure that %s chain %s exists: %v", utiliptables.TableNAT, kubePostroutingChain, err)
			return
		}

		comment := "kubernetes postrouting rules"
		args := []string{"-m", "comment", "--comment", comment, "-j", string(kubePostroutingChain)}
		if _, err := proxier.iptables.EnsureRule(utiliptables.Prepend, utiliptables.TableNAT, utiliptables.ChainPostrouting, args...); err != nil {
			glog.Errorf("Failed to ensure that %s chain %s jumps to %s: %v", utiliptables.TableNAT, utiliptables.ChainPostrouting, kubePostroutingChain, err)
			return
		}
	}

	// Get iptables-save output so we can check for existing chains and rules.
	// This will be a map of chain name to chain with rules as stored in iptables-save/iptables-restore
	/*
		kubernetes调用iptables-save命令解析当前node中iptables的filter表和nat表中已经存在的chain，
		kubernetes会将这些chain存在两个map中（ existingFilterChains 和 existingNATChains ），
		然后再创建四个protobuf中的buffer（分别是filterChains、filterRules、natChains和natRules），
		后续kubernetes会往这四个buffer中写入大量iptables规则，
		最后再调用iptables-restore写回到当前node的iptables中。
	*/
	existingFilterChains := make(map[utiliptables.Chain]string)
	iptablesSaveRaw, err := proxier.iptables.Save(utiliptables.TableFilter)
	if err != nil { // if we failed to get any rules
		glog.Errorf("Failed to execute iptables-save, syncing all rules: %v", err)
	} else { // otherwise parse the output
		existingFilterChains = utiliptables.GetChainLines(utiliptables.TableFilter, iptablesSaveRaw)
	}

	existingNATChains := make(map[utiliptables.Chain]string)
	iptablesSaveRaw, err = proxier.iptables.Save(utiliptables.TableNAT)
	if err != nil { // if we failed to get any rules
		glog.Errorf("Failed to execute iptables-save, syncing all rules: %v", err)
	} else { // otherwise parse the output
		existingNATChains = utiliptables.GetChainLines(utiliptables.TableNAT, iptablesSaveRaw)
	}

	filterChains := bytes.NewBuffer(nil)
	filterRules := bytes.NewBuffer(nil)
	natChains := bytes.NewBuffer(nil)
	natRules := bytes.NewBuffer(nil)

	// Write table headers.
	writeLine(filterChains, "*filter")
	writeLine(natChains, "*nat")

	// Make sure we keep stats for the top-level chains, if they existed
	// (which most should have because we created them above).
	/*
		如果当前node的iptables的filter表和nat表中，
		已经存在名为“KUBE-SERVICES”、“KUBE-NODEPORTS”、“KUBE-POSTROUTING”和“KUBE-MARK-MASQ”的自定义链，
		那就原封不动将它们按照原来的形式（:<chain-name> <chain-policy> [<packet-counter>:<byte-counter>]）写入到filterChains和natChains中；

		如果没有，则以“:<chain-name> <chain-policy> [0:0]”的格式写入上述4个chain，
		（即将“:KUBE-SERVICES – [0:0]”、“:KUBE-NODEPORTS – [0:0]”、“:KUBE-POSTROUTING – [0:0]”和“:KUBE-MARK-MASQ – [0:0]”写入到filterChains和natChains中，
		这相当于在filter表和nat表中创建了上述4个自定义链）；
	*/
	if chain, ok := existingFilterChains[kubeServicesChain]; ok {
		writeLine(filterChains, chain)
	} else {
		writeLine(filterChains, utiliptables.MakeChainLine(kubeServicesChain))
	}
	if chain, ok := existingNATChains[kubeServicesChain]; ok {
		writeLine(natChains, chain)
	} else {
		writeLine(natChains, utiliptables.MakeChainLine(kubeServicesChain))
	}
	if chain, ok := existingNATChains[kubeNodePortsChain]; ok {
		writeLine(natChains, chain)
	} else {
		writeLine(natChains, utiliptables.MakeChainLine(kubeNodePortsChain))
	}
	if chain, ok := existingNATChains[kubePostroutingChain]; ok {
		writeLine(natChains, chain)
	} else {
		writeLine(natChains, utiliptables.MakeChainLine(kubePostroutingChain))
	}
	if chain, ok := existingNATChains[KubeMarkMasqChain]; ok {
		writeLine(natChains, chain)
	} else {
		writeLine(natChains, utiliptables.MakeChainLine(KubeMarkMasqChain))
	}

	// Install the kubernetes-specific postrouting rules. We use a whole chain for
	// this so that it is easier to flush and change, for example if the mark
	// value should ever change.
	/*
		对nat表的自定义链“KUBE-POSTROUTING”写入如下规则：
			-A KUBE-POSTROUTING -m comment –comment “kubernetes service traffic requiring SNAT” -m mark –mark 0x4000/0x4000 -j MASQUERADE
	*/
	writeLine(natRules, []string{
		"-A", string(kubePostroutingChain),
		"-m", "comment", "--comment", `"kubernetes service traffic requiring SNAT"`,
		"-m", "mark", "--mark", proxier.masqueradeMark,
		"-j", "MASQUERADE",
	}...)

	// Install the kubernetes-specific masquerade mark rule. We use a whole chain for
	// this so that it is easier to flush and change, for example if the mark
	// value should ever change.
	/*
		对nat表的自定义链“KUBE-MARK-MASQ”写入如下规则:
			-A KUBE-MARK-MASQ -j MARK –set-xmark 0x4000/0x4000

		自定义Chain “KUBE-MARK-MASQ”和“KUBE-POSTROUTING”的作用：
		    kubernetes会让所有kubernetes集群内部产生的数据包流经nat表的自定义链“KUBE-MARK-MASQ”，
		    然后在这里kubernetes会对这些数据包打一个标记（0x4000/0x4000），
		    接着在nat的自定义链 “KUBE-POSTROUTING” 中根据上述标记匹配所有的kubernetes集群内部的数据包，
		    匹配的目的是kubernetes会对这些包做SNAT操作。
	*/
	writeLine(natRules, []string{
		"-A", string(KubeMarkMasqChain),
		"-j", "MARK", "--set-xmark", proxier.masqueradeMark,
	}...)

	// Accumulate NAT chains to keep.
	activeNATChains := map[utiliptables.Chain]bool{} // use a map as a set

	// Accumulate the set of local ports that we will be holding open once this update is complete
	replacementPortsMap := map[localPort]closeable{}

	// Build rules for each service.
	/*
		为每一个service建立rules
	*/
	for svcName, svcInfo := range proxier.serviceMap {
		/*
			遍历所有服务，对每一个服务，在nat表中创建名为“KUBE-SVC-XXXXXXXXXXXXXXXX”的自定义链。
			这里的XXXXXXXXXXXXXXXX是一个16位字符串，kubernetes使用SHA256 算法对“服务名+协议名”生成哈希值，
			然后通过base32对该哈希值编码，最后取编码值的前16位，
			kubernetes通过这种方式保证每个服务对应的“KUBE-SVC-XXXXXXXXXXXXXXXX”都不一样。
		*/
		protocol := strings.ToLower(string(svcInfo.protocol))

		// Create the per-service chain, retaining counters if possible.
		svcChain := servicePortChainName(svcName, protocol)
		if chain, ok := existingNATChains[svcChain]; ok {
			writeLine(natChains, chain)
		} else {
			writeLine(natChains, utiliptables.MakeChainLine(svcChain))
		}
		activeNATChains[svcChain] = true

		svcXlbChain := serviceLBChainName(svcName, protocol)
		if svcInfo.onlyNodeLocalEndpoints {
			// Only for services with the externalTraffic annotation set to OnlyLocal
			// create the per-service LB chain, retaining counters if possible.
			if lbChain, ok := existingNATChains[svcXlbChain]; ok {
				writeLine(natChains, lbChain)
			} else {
				writeLine(natChains, utiliptables.MakeChainLine(svcXlbChain))
			}
			activeNATChains[svcXlbChain] = true
		} else if activeNATChains[svcXlbChain] {
			// Cleanup the previously created XLB chain for this service
			delete(activeNATChains, svcXlbChain)
		}

		/*
			然后对每个服务，根据服务是否有cluster ip、是否有external ip、是否启用了外部负载均衡服务，
			在nat表的自定义链“KUBE-SERVICES”中加入类似如下这样的规则：
				-A KUBE-SERVICES -d 172.30.32.92/32 -p tcp -m comment –comment “kongxl/test2:8778-tcp cluster IP” -m tcp –dport 8778 -j KUBE-SVC-XAKTM6QUKQ53BZHS
			上面这个规则的意思是：
				所有流经自定义链KUBE-SERVICES的来自于服务“kongxl/test2:8778-tcp”的数据包都会跳转到自定义链KUBE-SVC-XAKTM6QUKQ53BZHS中
		*/
		// Capture the clusterIP.
		args := []string{
			"-A", string(kubeServicesChain),
			"-m", "comment", "--comment", fmt.Sprintf(`"%s cluster IP"`, svcName.String()),
			"-m", protocol, "-p", protocol,
			"-d", fmt.Sprintf("%s/32", svcInfo.clusterIP.String()),
			"--dport", fmt.Sprintf("%d", svcInfo.port),
		}
		if proxier.masqueradeAll {
			writeLine(natRules, append(args, "-j", string(KubeMarkMasqChain))...)
		}
		if len(proxier.clusterCIDR) > 0 {
			writeLine(natRules, append(args, "! -s", proxier.clusterCIDR, "-j", string(KubeMarkMasqChain))...)
		}
		writeLine(natRules, append(args, "-j", string(svcChain))...)

		// Capture externalIPs.
		for _, externalIP := range svcInfo.externalIPs {
			// If the "external" IP happens to be an IP that is local to this
			// machine, hold the local port open so no other process can open it
			// (because the socket might open but it would never work).
			if local, err := isLocalIP(externalIP); err != nil {
				glog.Errorf("can't determine if IP is local, assuming not: %v", err)
			} else if local {
				lp := localPort{
					desc:     "externalIP for " + svcName.String(),
					ip:       externalIP,
					port:     svcInfo.port,
					protocol: protocol,
				}
				if proxier.portsMap[lp] != nil {
					glog.V(4).Infof("Port %s was open before and is still needed", lp.String())
					replacementPortsMap[lp] = proxier.portsMap[lp]
				} else {
					socket, err := proxier.portMapper.OpenLocalPort(&lp)
					if err != nil {
						glog.Errorf("can't open %s, skipping this externalIP: %v", lp.String(), err)
						continue
					}
					replacementPortsMap[lp] = socket
				}
			} // We're holding the port, so it's OK to install iptables rules.
			args := []string{
				"-A", string(kubeServicesChain),
				"-m", "comment", "--comment", fmt.Sprintf(`"%s external IP"`, svcName.String()),
				"-m", protocol, "-p", protocol,
				"-d", fmt.Sprintf("%s/32", externalIP),
				"--dport", fmt.Sprintf("%d", svcInfo.port),
			}
			// We have to SNAT packets to external IPs.
			writeLine(natRules, append(args, "-j", string(KubeMarkMasqChain))...)

			// Allow traffic for external IPs that does not come from a bridge (i.e. not from a container)
			// nor from a local process to be forwarded to the service.
			// This rule roughly translates to "all traffic from off-machine".
			// This is imperfect in the face of network plugins that might not use a bridge, but we can revisit that later.
			externalTrafficOnlyArgs := append(args,
				"-m", "physdev", "!", "--physdev-is-in",
				"-m", "addrtype", "!", "--src-type", "LOCAL")
			writeLine(natRules, append(externalTrafficOnlyArgs, "-j", string(svcChain))...)
			dstLocalOnlyArgs := append(args, "-m", "addrtype", "--dst-type", "LOCAL")
			// Allow traffic bound for external IPs that happen to be recognized as local IPs to stay local.
			// This covers cases like GCE load-balancers which get added to the local routing table.
			writeLine(natRules, append(dstLocalOnlyArgs, "-j", string(svcChain))...)
		}

		// Capture load-balancer ingress.
		for _, ingress := range svcInfo.loadBalancerStatus.Ingress {
			if ingress.IP != "" {
				// create service firewall chain
				fwChain := serviceFirewallChainName(svcName, protocol)
				if chain, ok := existingNATChains[fwChain]; ok {
					writeLine(natChains, chain)
				} else {
					writeLine(natChains, utiliptables.MakeChainLine(fwChain))
				}
				activeNATChains[fwChain] = true
				// The service firewall rules are created based on ServiceSpec.loadBalancerSourceRanges field.
				// This currently works for loadbalancers that preserves source ips.
				// For loadbalancers which direct traffic to service NodePort, the firewall rules will not apply.

				args := []string{
					"-A", string(kubeServicesChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s loadbalancer IP"`, svcName.String()),
					"-m", protocol, "-p", protocol,
					"-d", fmt.Sprintf("%s/32", ingress.IP),
					"--dport", fmt.Sprintf("%d", svcInfo.port),
				}
				// jump to service firewall chain
				writeLine(natRules, append(args, "-j", string(fwChain))...)

				args = []string{
					"-A", string(fwChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s loadbalancer IP"`, svcName.String()),
				}

				// Each source match rule in the FW chain may jump to either the SVC or the XLB chain
				chosenChain := svcXlbChain
				// If we are proxying globally, we need to masquerade in case we cross nodes.
				// If we are proxying only locally, we can retain the source IP.
				if !svcInfo.onlyNodeLocalEndpoints {
					writeLine(natRules, append(args, "-j", string(KubeMarkMasqChain))...)
					chosenChain = svcChain
				}

				if len(svcInfo.loadBalancerSourceRanges) == 0 {
					// allow all sources, so jump directly to the KUBE-SVC or KUBE-XLB chain
					writeLine(natRules, append(args, "-j", string(chosenChain))...)
				} else {
					// firewall filter based on each source range
					allowFromNode := false
					for _, src := range svcInfo.loadBalancerSourceRanges {
						writeLine(natRules, append(args, "-s", src, "-j", string(chosenChain))...)
						// ignore error because it has been validated
						_, cidr, _ := net.ParseCIDR(src)
						if cidr.Contains(proxier.nodeIP) {
							allowFromNode = true
						}
					}
					// generally, ip route rule was added to intercept request to loadbalancer vip from the
					// loadbalancer's backend hosts. In this case, request will not hit the loadbalancer but loop back directly.
					// Need to add the following rule to allow request on host.
					if allowFromNode {
						writeLine(natRules, append(args, "-s", fmt.Sprintf("%s/32", ingress.IP), "-j", string(chosenChain))...)
					}
				}

				// If the packet was able to reach the end of firewall chain, then it did not get DNATed.
				// It means the packet cannot go thru the firewall, then mark it for DROP
				writeLine(natRules, append(args, "-j", string(KubeMarkDropChain))...)
			}
		}

		// Capture nodeports.  If we had more than 2 rules it might be
		// worthwhile to make a new per-service chain for nodeport rules, but
		// with just 2 rules it ends up being a waste and a cognitive burden.
		/*
			在遍历每一个服务的过程中，还会检查该服务是否启用了nodeports，
			如果启用了且该服务有对应的endpoints，
			则会在nat表的自定义链“KUBE-NODEPORTS”中加入如下两条规则：
				-- 所有流经自定义链KUBE-NODEPORTS的来自于服务“ym/echo-app-nodeport”的数据包都会跳转到自定义链KUBE-MARK-MASQ中，
				   即kubernetes会对来自上述服务的这些数据包打一个标记（0x4000/0x4000）
				   -A KUBE-NODEPORTS -p tcp -m comment –comment “ym/echo-app-nodeport:” -m tcp –dport 30001 -j KUBE-MARK-MASQ
				-- 所有流经自定义链KUBE-NODEPORTS的来自于服务“ym/echo-app-nodeport”的数据包都会跳转到自定义链KUBE-SVC-LQ6G5YLNLUHHZYH5中
				   -A KUBE-NODEPORTS -p tcp -m comment –comment “ym/echo-app-nodeport:” -m tcp –dport 30001 -j KUBE-SVC-LQ6G5YLNLUHHZYH5
		*/
		if svcInfo.nodePort != 0 {
			// Hold the local port open so no other process can open it
			// (because the socket might open but it would never work).
			lp := localPort{
				desc:     "nodePort for " + svcName.String(),
				ip:       "",
				port:     svcInfo.nodePort,
				protocol: protocol,
			}
			if proxier.portsMap[lp] != nil {
				glog.V(4).Infof("Port %s was open before and is still needed", lp.String())
				replacementPortsMap[lp] = proxier.portsMap[lp]
			} else {
				socket, err := proxier.portMapper.OpenLocalPort(&lp)
				if err != nil {
					glog.Errorf("can't open %s, skipping this nodePort: %v", lp.String(), err)
					continue
				}
				replacementPortsMap[lp] = socket
			} // We're holding the port, so it's OK to install iptables rules.

			args := []string{
				"-A", string(kubeNodePortsChain),
				"-m", "comment", "--comment", svcName.String(),
				"-m", protocol, "-p", protocol,
				"--dport", fmt.Sprintf("%d", svcInfo.nodePort),
			}
			if !svcInfo.onlyNodeLocalEndpoints {
				// Nodeports need SNAT, unless they're local.
				writeLine(natRules, append(args, "-j", string(KubeMarkMasqChain))...)
				// Jump to the service chain.
				writeLine(natRules, append(args, "-j", string(svcChain))...)
			} else {
				// TODO: Make all nodePorts jump to the firewall chain.
				// Currently we only create it for loadbalancers (#33586).
				writeLine(natRules, append(args, "-j", string(svcXlbChain))...)
			}
		}

		// If the service has no endpoints then reject packets.
		/*
			如果一个服务启用了nodeports，
			但该服务没有对应的endpoints，
			则会在filter表的自定义链“KUBE-SERVICES”中加入如下规则：
				-- 如果service没有配置endpoints，那么kubernetes这里会REJECT所有数据包,
				   这意味着没有endpoints的service是无法被访问到的
				   -A KUBE-SERVICES -d 172.30.32.92/32 -p tcp -m comment –comment “kongxl/test2:8080-tcp has no endpoints” -m tcp –dport 8080 -j REJECT
		*/
		if len(proxier.endpointsMap[svcName]) == 0 {
			writeLine(filterRules,
				"-A", string(kubeServicesChain),
				"-m", "comment", "--comment", fmt.Sprintf(`"%s has no endpoints"`, svcName.String()),
				"-m", protocol, "-p", protocol,
				"-d", fmt.Sprintf("%s/32", svcInfo.clusterIP.String()),
				"--dport", fmt.Sprintf("%d", svcInfo.port),
				"-j", "REJECT",
			)
			continue
		}

		// Generate the per-endpoint chains.  We do this in multiple passes so we
		// can group rules together.
		// These two slices parallel each other - keep in sync
		endpoints := make([]*endpointsInfo, 0)
		endpointChains := make([]utiliptables.Chain, 0)
		for _, ep := range proxier.endpointsMap[svcName] {
			endpoints = append(endpoints, ep)
			endpointChain := servicePortEndpointChainName(svcName, protocol, ep.ip)
			endpointChains = append(endpointChains, endpointChain)

			// Create the endpoint chain, retaining counters if possible.
			if chain, ok := existingNATChains[utiliptables.Chain(endpointChain)]; ok {
				writeLine(natChains, chain)
			} else {
				writeLine(natChains, utiliptables.MakeChainLine(endpointChain))
			}
			activeNATChains[endpointChain] = true
		}

		// First write session affinity rules, if applicable.
		/*
			在遍历每一个服务的过程中，对每一个服务，如果这个服务有对应的endpoints，
			那么在nat表中创建名为“KUBE-SEP-XXXXXXXXXXXXXXXX”的自定义链。
			这里的XXXXXXXXXXXXXXXX是一个16位字符串，kubernetes使用SHA256 算法对“服务名+协议名+端口”生成哈希值，
			然后通过base32对该哈希值编码，最后取编码值的前16位，
			kubernetes通过这种方式保证每个服务的endpoint对应的“KUBE-SEP-XXXXXXXXXXXXXXXX”都不一样。

			然后对每个endpoint，如果该服务配置了session affinity，
			则在nat表的该service对应的自定义链“KUBE-SVC-XXXXXXXXXXXXXXXX”中加入类似如下这样的规则：
				-- 所有流经自定义链KUBE-SVC-ECTPRXTXBM34L34Q的来自于服务”default/docker-registry:5000-tcp”的数据包
				   都会跳转到自定义链 KUBE-SEP-LPCU5ERTNL2YBWXG中，且会在一段时间内保持session affinity，保持时间为180秒
				   -A KUBE-SVC-ECTPRXTXBM34L34Q -m comment –comment “default/docker-registry:5000-tcp” -m recent –rcheck –seconds 180 –reap –name KUBE-SEP-LPCU5ERTNL2YBWXG –mask 255.255.255.255 –rsource -j KUBE-SEP-LPCU5ERTNL2YBWXG
				   这里kubernetes用“-m recent –rcheck –seconds 180 –reap”实现了会话保持
		*/
		if svcInfo.sessionAffinityType == api.ServiceAffinityClientIP {
			for _, endpointChain := range endpointChains {
				writeLine(natRules,
					"-A", string(svcChain),
					"-m", "comment", "--comment", svcName.String(),
					"-m", "recent", "--name", string(endpointChain),
					"--rcheck", "--seconds", fmt.Sprintf("%d", svcInfo.stickyMaxAgeMinutes*60), "--reap",
					"-j", string(endpointChain))
			}
		}

		// Now write loadbalancing & DNAT rules.
		/*
			在nat表的该service对应的自定义链“KUBE-SVC-XXXXXXXXXXXXXXXX”中加入类似如下这样的规则，
			如果该服务对应的endpoints大于等于2，则还会加入负载均衡规则。

			所有流经自定义链KUBE-SVC-VX5XTMYNLWGXYEL4的来自于服务“ym/echo-app”的数据包
			既可能会跳转到自定义链KUBE-SEP-27OZWHQEIJ47W5ZW，
			也可能会跳转到自定义链KUBE-SEP-AA6LE4U3XA6T2EZB，
			这里kubernetes用“-m statistic –mode random –probability 0.50000000000”实现了对该服务访问的负载均衡

			-A KUBE-SVC-VX5XTMYNLWGXYEL4 -m comment –comment “ym/echo-app:” -m statistic –mode random –probability 0.50000000000 -j KUBE-SEP-27OZWHQEIJ47W5ZW

			-A KUBE-SVC-VX5XTMYNLWGXYEL4 -m comment –comment “ym/echo-app:” -j KUBE-SEP-AA6LE4U3XA6T2EZB
		*/
		n := len(endpointChains)
		for i, endpointChain := range endpointChains {
			// Balancing rules in the per-service chain.
			args := []string{
				"-A", string(svcChain),
				"-m", "comment", "--comment", svcName.String(),
			}
			if i < (n - 1) {
				// Each rule is a probabilistic match.
				args = append(args,
					"-m", "statistic",
					"--mode", "random",
					"--probability", fmt.Sprintf("%0.5f", 1.0/float64(n-i)))
			}
			// The final (or only if n == 1) rule is a guaranteed match.
			args = append(args, "-j", string(endpointChain))
			writeLine(natRules, args...)

			// Rules in the per-endpoint chain.
			args = []string{
				"-A", string(endpointChain),
				"-m", "comment", "--comment", svcName.String(),
			}
			// Handle traffic that loops back to the originator with SNAT.
			writeLine(natRules, append(args,
				"-s", fmt.Sprintf("%s/32", strings.Split(endpoints[i].ip, ":")[0]),
				"-j", string(KubeMarkMasqChain))...)
			// Update client-affinity lists.
			if svcInfo.sessionAffinityType == api.ServiceAffinityClientIP {
				args = append(args, "-m", "recent", "--name", string(endpointChain), "--set")
			}
			// DNAT to final destination.
			args = append(args, "-m", protocol, "-p", protocol, "-j", "DNAT", "--to-destination", endpoints[i].ip)
			writeLine(natRules, args...)
		}

		// The logic below this applies only if this service is marked as OnlyLocal
		if !svcInfo.onlyNodeLocalEndpoints {
			continue
		}

		// Now write ingress loadbalancing & DNAT rules only for services that have a localOnly annotation
		// TODO - This logic may be combinable with the block above that creates the svc balancer chain
		/*
			最后，在遍历每一个服务的过程中，对每一个服务的endpoints，
			在nat表的该endpoint对应的自定义链“KUBE-SEP-XXXXXXXXXXXXXXXX”中加入如下规则，
			实现到该服务最终目的地的DNAT：

			服务“ym/echo-app”有两个endpoints，之前kubernetes已经对该服务做了负载均衡，所以这里一共会产生4条跳转规则：
			-A KUBE-SEP-27OZWHQEIJ47W5ZW -s 10.1.0.8/32 -m comment –comment “ym/echo-app:” -j KUBE-MARK-MASQ
			-A KUBE-SEP-27OZWHQEIJ47W5ZW -p tcp -m comment –comment “ym/echo-app:” -m tcp -j DNAT –to-destination 10.1.0.8:8080
			-A KUBE-SEP-AA6LE4U3XA6T2EZB -s 10.1.1.4/32 -m comment –comment “ym/echo-app:” -j KUBE-MARK-MASQ
			-A KUBE-SEP-AA6LE4U3XA6T2EZB -p tcp -m comment –comment “ym/echo-app:” -m tcp -j DNAT –to-destination 10.1.1.4:8080
		*/
		localEndpoints := make([]*endpointsInfo, 0)
		localEndpointChains := make([]utiliptables.Chain, 0)
		for i := range endpointChains {
			if endpoints[i].localEndpoint {
				// These slices parallel each other; must be kept in sync
				localEndpoints = append(localEndpoints, endpoints[i])
				localEndpointChains = append(localEndpointChains, endpointChains[i])
			}
		}
		// First rule in the chain redirects all pod -> external vip traffic to the
		// Service's ClusterIP instead. This happens whether or not we have local
		// endpoints; only if clusterCIDR is specified
		if len(proxier.clusterCIDR) > 0 {
			args = []string{
				"-A", string(svcXlbChain),
				"-m", "comment", "--comment",
				fmt.Sprintf(`"Redirect pods trying to reach external loadbalancer VIP to clusterIP"`),
				"-s", proxier.clusterCIDR,
				"-j", string(svcChain),
			}
			writeLine(natRules, args...)
		}

		numLocalEndpoints := len(localEndpointChains)
		if numLocalEndpoints == 0 {
			// Blackhole all traffic since there are no local endpoints
			args := []string{
				"-A", string(svcXlbChain),
				"-m", "comment", "--comment",
				fmt.Sprintf(`"%s has no local endpoints"`, svcName.String()),
				"-j",
				string(KubeMarkDropChain),
			}
			writeLine(natRules, args...)
		} else {
			// Setup probability filter rules only over local endpoints
			for i, endpointChain := range localEndpointChains {
				// Balancing rules in the per-service chain.
				args := []string{
					"-A", string(svcXlbChain),
					"-m", "comment", "--comment",
					fmt.Sprintf(`"Balancing rule %d for %s"`, i, svcName.String()),
				}
				if i < (numLocalEndpoints - 1) {
					// Each rule is a probabilistic match.
					args = append(args,
						"-m", "statistic",
						"--mode", "random",
						"--probability", fmt.Sprintf("%0.5f", 1.0/float64(numLocalEndpoints-i)))
				}
				// The final (or only if n == 1) rule is a guaranteed match.
				args = append(args, "-j", string(endpointChain))
				writeLine(natRules, args...)
			}
		}
	}

	// Delete chains no longer in use.
	/*
		删掉当前节点中已经不存在的服务所对应的“KUBE-SVC-XXXXXXXXXXXXXXXX”链和“KUBE-SEP-XXXXXXXXXXXXXXXX”链；
	*/
	for chain := range existingNATChains {
		if !activeNATChains[chain] {
			chainString := string(chain)
			if !strings.HasPrefix(chainString, "KUBE-SVC-") && !strings.HasPrefix(chainString, "KUBE-SEP-") && !strings.HasPrefix(chainString, "KUBE-FW-") && !strings.HasPrefix(chainString, "KUBE-XLB-") {
				// Ignore chains that aren't ours.
				continue
			}
			// We must (as per iptables) write a chain-line for it, which has
			// the nice effect of flushing the chain.  Then we can remove the
			// chain.
			writeLine(natChains, existingNATChains[chain])
			writeLine(natRules, "-X", chainString)
		}
	}

	// Finally, tail-call to the nodeports chain.  This needs to be after all
	// other service portal rules.
	/*
		向nat表的自定义链“KUBE-SERVICES”中写入如下这样规则：
			-- 将目的地址是本地的数据包跳转到自定义链KUBE-NODEPORTS中
			 -A KUBE-SERVICES -m comment –comment “kubernetes service nodeports; NOTE: this must be the last rule in this chain” -m addrtype –dst-type LOCAL -j KUBE-NODEPORTS
	*/
	writeLine(natRules,
		"-A", string(kubeServicesChain),
		"-m", "comment", "--comment", `"kubernetes service nodeports; NOTE: this must be the last rule in this chain"`,
		"-m", "addrtype", "--dst-type", "LOCAL",
		"-j", string(kubeNodePortsChain))

	// Write the end-of-table markers.
	writeLine(filterRules, "COMMIT")
	writeLine(natRules, "COMMIT")

	// Sync rules.
	// NOTE: NoFlushTables is used so we don't flush non-kubernetes chains in the table.
	/*
		合并已经被写入了大量规则的四个protobuf中的buffer（分别是filterChains、filterRules、natChains和natRules），
		然后调用iptables-restore写回到当前node的iptables中。
	*/
	filterLines := append(filterChains.Bytes(), filterRules.Bytes()...)
	natLines := append(natChains.Bytes(), natRules.Bytes()...)
	lines := append(filterLines, natLines...)

	glog.V(3).Infof("Restoring iptables rules: %s", lines)
	err = proxier.iptables.RestoreAll(lines, utiliptables.NoFlushTables, utiliptables.RestoreCounters)
	if err != nil {
		glog.Errorf("Failed to execute iptables-restore: %v\nRules:\n%s", err, lines)
		// Revert new local ports.
		revertPorts(replacementPortsMap, proxier.portsMap)
		return
	}

	// Close old local ports and save new ones.
	for k, v := range proxier.portsMap {
		if replacementPortsMap[k] == nil {
			v.Close()
		}
	}
	proxier.portsMap = replacementPortsMap
}
```


## iptables基本使用命令
所谓的定义iptables规则，其实就是对raw、managle、nat、filter这四张表进行增删改查操作。

### 查
如果不指定一个表，默认操作的是filter表

```shell
# iptables -L -t nat

# 列出更详细信息
[root@fqhnode01 yaml]# iptables -vL 
Chain INPUT (policy ACCEPT 11 packets, 644 bytes)
 pkts bytes target     prot opt in     out     source               destination         
 147K   76M KUBE-FIREWALL  all  --  any    any     anywhere             anywhere            

Chain FORWARD (policy DROP 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination         
 741K   85M DOCKER-ISOLATION  all  --  any    any     anywhere             anywhere            
 741K   85M DOCKER     all  --  any    docker0  anywhere             anywhere            
 741K   85M ACCEPT     all  --  any    docker0  anywhere             anywhere             ctstate RELATED,ESTABLISHED
    0     0 ACCEPT     all  --  docker0 !docker0  anywhere             anywhere            
   35  2100 ACCEPT     all  --  docker0 docker0  anywhere             anywhere 

# iptables -nvL --line
# iptables -nxvL
```
讲一下里面的属性：
- Chain INPUT (policy ACCEPT 11 packets, 644 bytes) ，表示filter表中的INPUT 关卡默认动作是ACCEPT, packets是当前Chain默认规则匹配到的包数量，644 bytes则是包大小。
- pkts ，对应规则匹配到的报文个数
- bytes，对应规则匹配到的报文大小总和
- target，匹配成功后的动作
- prot，协议，针对哪些协议使用此规则
- opt，此规则对应的选项
- in，报文是从哪个网卡流入的需要匹配此规则
- out，报文是从哪个网卡流出的
- source，报文源地址，可以是一个IP或一个网段
- destination

## 增
注意点：添加规则时，需要注意规则的顺序

在指定表的最后增加一条规则
```
命令语法：iptables -t 表名 -A 链名 匹配条件 -j 动作
示例：iptables -t filter -A INPUT -s 192.168.1.146 -j DROP
```

在指定表的指定位置增加一条规则
```
命令语法：iptables -t 表名 -I 链名 规则序号 匹配条件 -j 动作
示例：iptables -t filter -I INPUT 5 -s 192.168.1.146 -j REJECT
```

设置表的默认策略
```
命令语法：iptables -t 表名 -P 链名 动作
示例：iptables -t filter -P FORWARD ACCEPT
```

## 删
注意点：如果没有保存规则，谨慎删除规则

删除指定序号的规则
```
命令语法：iptables -t 表名 -D 链名 规则序号
示例：iptables -t filter -D INPUT 3
```

根据匹配条件来删除规则
```
命令语法：iptables -t 表名 -D 链名 匹配条件 -j 动作
示例：iptables -t filter -D INPUT -s 192.168.1.146 -j DROP
```

删除指定表的指定Chain 的所有规则
```
命令语法：iptables -t 表名 -F 链名
示例：iptables -t filter -F INPUT
```

删除指定表的所有规则
```
命令语法：iptables -t 表名 -F
示例：iptables -t filter -F
```

最后要说的是对iptables rule的进行的操作只是临时生效的，如果要想让其永久生效，需要使用iptables save来保存规则。

## 参考
[iptables系列](http://www.zsythink.net/archives/tag/iptables/)

[Kubernetes如何利用iptables](http://www.dbsnake.net/category/paas)









