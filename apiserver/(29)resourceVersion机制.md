# resourceVersion机制

## 作用
resourceVersion：标识一个object的internal version，Client端可以依据resourceVersion来判断该obj是否已经发生了变化。 
Client端不能忽视resourceVersion值，而且必须不加修改地回传给Server端。
客户端不应该假设resourceVersion在命名空间，不同类型的资源或不同的服务器上有意义。
有关更多详细信息，请参阅[并发控制](https://github.com/kubernetes/community/tree/master/contributors/devel#concurrency-control-and-consistency)

Kubernetes利用resourceVersion的概念来实现乐观的并发。 
所有Kubernetes资源都有一个“resourceVersion”字段作为其元数据的一部分。 
当一个记录即将更新时，会根据预先保存的值检查版本，如果不匹配，则更新会失败，并显示StatusConflict（HTTP状态码409）。


## 来源
resourceVersion的值是怎么来的？ 我们从`kubectl create`命令开始研究。

### etcdhelper
可以知道`kubectl create`会调用etcdhelp的Create()，见/pkg/storage/etcd/etcd_helper.go，其流程分析如下：
1. 根据用户传入的信息调用etcdClient的Set()在etcd中创建对象，并得到返回信息response。 具体用法参考[etcdClient](https://github.com/coreos/etcd/tree/master/client)
2. response中信息会包含etcd数据库此时最新的index，此值就是该obj的resourceversion
3. 把response中信息解析绑定到obj中

```go
// Implements storage.Interface.
/*kubectl create -f xx.yaml触发此函数*/
func (h *etcdHelper) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	trace := util.NewTrace("etcdHelper::Create " + getTypeName(obj))
	defer trace.LogIfLong(250 * time.Millisecond)
	if ctx == nil {
		glog.Errorf("Context is nil")
	}
	key = h.prefixEtcdKey(key)
	data, err := runtime.Encode(h.codec, obj)
	trace.Step("Object encoded")
	if err != nil {
		return err
	}
	/*
		==>/pkg/storage/etcd/api_object_versioner.go
			==>func (a APIObjectVersioner) ObjectResourceVersion
	*/
	if version, err := h.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		return errors.New("resourceVersion may not be set on objects to be created")
	}
	trace.Step("Version checked")

	startTime := time.Now()
	opts := etcd.SetOptions{
		TTL:       time.Duration(ttl) * time.Second,
		PrevExist: etcd.PrevNoExist,
	}
	response, err := h.etcdKeysAPI.Set(ctx, key, string(data), &opts)
	/*
		创建成功之后，
		返回的Response:  &{create {Key: /registry/resourcequotas/default/quota, CreatedIndex: 30, ModifiedIndex: 30, TTL: 0} <nil> 30}

		对应的etcd内容
		# etcdctl -o extended --endpoints 172.17.0.2:2379  get /registry/resourcequotas/default/quota
			Key: /registry/resourcequotas/default/quota
			Created-Index: 30
			Modified-Index: 30
			TTL: 0
			Index: 30

			{"kind":"ResourceQuota","apiVersion":"v1","metadata":{"name":"quota","namespace":"default","uid":"2b7ba689-eb27-11e7-b9d2-080027e58fc6","creationTimestamp":"2017-12-27T16:58:32Z"},"spec":{"hard":{"services":"5"}},"status":{}}
	*/
	trace.Step("Object created")
	metrics.RecordEtcdRequestLatency("create", getTypeName(obj), startTime)
	if err != nil {
		return toStorageErr(err, key, 0)
	}
	if out != nil {
		if _, err := conversion.EnforcePtr(out); err != nil {
			panic("unable to convert output object to pointer")
		}
		/*
			把response中信息解析绑定到obj中
		*/
		_, _, err = h.extractObj(response, err, out, false, false)
	}
	return err
}
```

以namespace default为例子
```shell
[root@fqhnode01 ~]# kubectl get ns default  -o yaml
apiVersion: v1
kind: Namespace
metadata:
  creationTimestamp: 2017-12-27T16:15:08Z
  name: default
  resourceVersion: "13"
  selfLink: /api/v1/namespacesdefault
  uid: 1b723730-eb21-11e7-ba17-080027e58fc6
spec:
  finalizers:
  - kubernetes
status:
  phase: Active

[root@fqhnode01 ~]# etcdctl -o extended --endpoints 172.17.0.2:2379 get /registry/namespaces/default
Key: /registry/namespaces/default
Created-Index: 13
Modified-Index: 13
TTL: 0
Index: 30

{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"default","uid":"1b723730-eb21-11e7-ba17-080027e58fc6","creationTimestamp":"2017-12-27T16:15:08Z"},"spec":{"finalizers":["kubernetes"]},"status":{"phase":"Active"}}
```
可以发现resourceVersion的值不是直接记录在etcd中该obj的信息上的，而是该obj在etcd中的`Modified-Index`。

### extractObj
extractObj()会把response中信息解析绑定到obj中
```go
func (h *etcdHelper) extractObj(response *etcd.Response, inErr error, objPtr runtime.Object, ignoreNotFound, prevNode bool) (body string, node *etcd.Node, err error) {
	if response != nil {
		if prevNode {
			node = response.PrevNode
		} else {
			//没有记录的前值，直接node = response.Node
			node = response.Node
		}
	}
	if inErr != nil || node == nil || len(node.Value) == 0 {
		if ignoreNotFound {
			v, err := conversion.EnforcePtr(objPtr)
			if err != nil {
				return "", nil, err
			}
			v.Set(reflect.Zero(v.Type()))
			return "", nil, nil
		} else if inErr != nil {
			return "", nil, inErr
		}
		return "", nil, fmt.Errorf("unable to locate a value on the response: %#v", response)
	}
	body = node.Value
	out, gvk, err := h.codec.Decode([]byte(body), nil, objPtr)
	if err != nil {
		return body, nil, err
	}
	if out != objPtr {
		return body, nil, fmt.Errorf("unable to decode object %s into %v", gvk.String(), reflect.TypeOf(objPtr))
	}
	// being unable to set the version does not prevent the object from being extracted
	/*
		无法通过设置version的方式，来阻止该object被提取
			==>/kubernetes-1.5.2/pkg/storage/etcd/api_object_versioner.go
				==>func (a APIObjectVersioner) UpdateObject(obj runtime.Object, resourceVersion uint64)
		这里是把node.ModifiedIndex作为该obj的新的resourceVersion
	*/
	_ = h.versioner.UpdateObject(objPtr, node.ModifiedIndex)
	return body, node, err
}
```

### 设置metadata
UpdateObject()会负责调用Accessor的SetResourceVersion()
```go
// UpdateObject implements Versioner
func (a APIObjectVersioner) UpdateObject(obj runtime.Object, resourceVersion uint64) error {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	versionString := ""
	if resourceVersion != 0 {
		versionString = strconv.FormatUint(resourceVersion, 10)
	}
	/*
		==>/pkg/api/meta/meta.go
			==>func (a genericAccessor) SetResourceVersion(version string)
	*/
	accessor.SetResourceVersion(versionString)
	return nil
}

func (a genericAccessor) SetResourceVersion(version string) {
	//设置一个obj中元数据的resourceVersion值
	*a.resourceVersion = version
}
```

最后，如果在yaml文件中指定了resourceVersion的值，是会被忽略的，在`func (h *etcdHelper) Create`中返回的response还是会包含etcd中的`Modified-Index`


