# 定制API之kubectl

承接前面[定制一个API之ApiServer](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/apiserver/定制一个API之ApiServer篇/定制一个API之ApiServer.md)，现在来完善kubectl

## 初始化
`/pkg/client/clientset_generated/internalclientset/import_known_versions.go` 和 `/pkg/client/clientset_generated/release_1_5/import_known_versions.go`中完成Group的初始化安装，这个地方自动化生成代码工具居然没有更新。。。
```
_ "k8s.io/kubernetes/pkg/apis/premierleague/install"
```

## Create
现在我们可以创建前面定义好的premierleague.Match资源了，premierleague-match.yaml文件如下：
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

效果如下
```shell
[root@fqhnode01 yaml]# kubectl create -f premierleague-match.yaml 
[root@fqhnode01 ~]# etcdctl --endpoints 172.17.0.2:2379 get  /registry/matchs/default/fqhmatch
{"kind":"Match","apiVersion":"premierleague.k8s.io/v1","metadata":{"name":"fqhmatch","namespace":"default","uid":"546fc0f9-e74e-11e7-8c9c-080027e58fc6","generation":1,"creationTimestamp":"2017-12-22T19:28:46Z"},"spec":{"host":"A","guest":"B"}}
```

## Get
指定json或者yaml格式执行get操作，`kubectl get match -o json`
```json
enforceNamespace is:  false
args is: [match]
len(args) is: 1
generic is:  true
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "premierleague.k8s.io/v1",
            "kind": "Match",
            "metadata": {
                "creationTimestamp": "2017-12-22T19:28:46Z",
                "generation": 1,
                "name": "fqhmatch",
                "namespace": "default",
                "resourceVersion": "31",
                "selfLink": "/apis/premierleague.k8s.io/v1/namespaces/default/matchs/fqhmatch",
                "uid": "546fc0f9-e74e-11e7-8c9c-080027e58fc6"
            },
            "spec": {
                "guest": "B",
                "host": "A"
            }
        }
    ],
    "kind": "List",
    "metadata": {},
    "resourceVersion": "",
    "selfLink": ""
}
```
其实，此时指定`/pkg/kubectl/resource_printer.go`中的`func GetPrinter`的大部分Printer都能得到输出，除了`-o wide`和`不指定格式`的情况。


### 代码
修改`pkg/kubectl/resource_printer.go`

1. import
```go
"k8s.io/kubernetes/pkg/apis/premierleague"
```

2. 定义输出item
```go
// NOTE: When adding a new resource type here, please update the list
// pkg/kubectl/cmd/get.go to reflect the new resource type.
var (
	matchColumns                 = []string{"NAME", "HOST", "GUEST", "AGE"}
	podColumns                   = []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}
	...
	...
	)
```

3. addDefaultHandlers中增加Match对象的print函数
```go
// addDefaultHandlers adds print handlers for default Kubernetes types.
func (h *HumanReadablePrinter) addDefaultHandlers() {
	h.Handler(matchColumns, printMatch)
	h.Handler(matchColumns, printMatchList)
	h.Handler(podColumns, h.printPodList)
	...
	...
}
```
4. 两个print函数如下
```
func printMatch(match *premierleague.Match, w io.Writer, options PrintOptions) error {
	name := formatResourceName(options.Kind, match.Name, options.WithKind)

	namespace := match.Namespace

	if options.WithNamespace {
		if _, err := fmt.Fprintf(w, "%s\t", namespace); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s", name, match.Spec.Host, match.Spec.Guest, translateTimestamp(match.CreationTimestamp)); err != nil {
		return err
	}

	if _, err := fmt.Fprint(w, AppendLabels(match.Labels, options.ColumnLabels)); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, AppendAllLabels(options.ShowLabels, match.Labels))
	return err
}

func printMatchList(matchList *premierleague.MatchList, w io.Writer, options PrintOptions) error {
	for _, match := range matchList.Items {
		if err := printMatch(&match, w, options); err != nil {
			return err
		}
	}
	return nil
}
```

### 效果
```shell
[root@fqhnode01 yaml]# kubectl get match
NAME       HOST      GUEST     AGE
fqhmatch   A         B         1h
```




