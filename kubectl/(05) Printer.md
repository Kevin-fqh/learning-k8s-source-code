# Printer

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [func PrinterForCommand()](#func-printerforcommand)
  - [func GetPrinter()](#func-getprinter)
  - [type JSONPrinter struct](#type-jsonprinter-struct)
  - [type YAMLPrinter struct](#type-yamlprinter-struct)
  - [type NamePrinter struct](#type-nameprinter-struct)
  - [type TemplatePrinter struct](#type-templateprinter-struct)
  - [type JSONPathPrinter struct](#type-jsonpathprinter-struct)
  - [type CustomColumnsPrinter struct](#type-customcolumnsprinter-struct)
  - [type HumanReadablePrinter struct](#type-humanreadableprinter-struct)
  - [对输出内容进行排序](#对输出内容进行排序)

<!-- END MUNGE: GENERATED_TOC -->


Printer 是kubectl 命令在输出的时候设置的显示格式。

Printer的类型繁多，目前支持JsonPrinter, YAMLPrinter, NamePrinter, TemplatePrinter, JSONPathPrinter, CustomColumnsPrinter, VersionedPrinter, HumanReadablePrinter。

## func PrinterForCommand()
func PrinterForCommand 返回入参cmd的默认printer。 
见/pkg/kubectl/cmd/util/printing.go

```go
// PrinterForCommand returns the default printer for this command.
// Requires that printer flags have been added to cmd (see AddPrinterFlags).

func PrinterForCommand(cmd *cobra.Command) (kubectl.ResourcePrinter, bool, error) {
	outputFormat := GetFlagString(cmd, "output")

	// templates are logically optional for specifying a format.
	// TODO once https://github.com/kubernetes/kubernetes/issues/12668 is fixed, this should fall back to GetFlagString
	templateFile, _ := cmd.Flags().GetString("template")
	if len(outputFormat) == 0 && len(templateFile) != 0 {
		outputFormat = "template"
	}

	templateFormat := []string{
		"go-template=", "go-template-file=", "jsonpath=", "jsonpath-file=", "custom-columns=", "custom-columns-file=",
	}
	for _, format := range templateFormat {
		if strings.HasPrefix(outputFormat, format) {
			templateFile = outputFormat[len(format):]
			outputFormat = format[:len(format)-1]
		}
	}

	/*
		获取输出的格式printer，
		generic的值和具体格式有关，一般情况下generic＝true
		==>/pkg/kubectl/resource_printer.go
			==>func GetPrinter
	*/
	printer, generic, err := kubectl.GetPrinter(outputFormat, templateFile, GetFlagBool(cmd, "no-headers"))
	if err != nil {
		return nil, generic, err
	}

	/*
		generic＝true的时候，再处理以下
	*/
	return maybeWrapSortingPrinter(cmd, printer), generic, nil
}
```

## func GetPrinter()
设置具体的Printer，见/pkg/kubectl/resource_printer.go

```go
// GetPrinter takes a format type, an optional format argument. It will return true
// if the format is generic (untyped), otherwise it will return false. The printer
// is agnostic to schema versions, so you must send arguments to PrintObj in the
// version you wish them to be shown using a VersionedPrinter (typically when
// generic is true).

func GetPrinter(format, formatArgument string, noHeaders bool) (ResourcePrinter, bool, error) {
	var printer ResourcePrinter
	switch format {
	case "json":
		printer = &JSONPrinter{}
	case "yaml":
		printer = &YAMLPrinter{}
	case "name":
		printer = &NamePrinter{
			// TODO: this is wrong, these should be provided as an argument to GetPrinter
			Typer:   api.Scheme,
			Decoder: api.Codecs.UniversalDecoder(),
		}
	case "template", "go-template":
		if len(formatArgument) == 0 {
			return nil, false, fmt.Errorf("template format specified but no template given")
		}
		var err error
		printer, err = NewTemplatePrinter([]byte(formatArgument))
		if err != nil {
			return nil, false, fmt.Errorf("error parsing template %s, %v\n", formatArgument, err)
		}
	case "templatefile", "go-template-file":
		if len(formatArgument) == 0 {
			return nil, false, fmt.Errorf("templatefile format specified but no template file given")
		}
		data, err := ioutil.ReadFile(formatArgument)
		if err != nil {
			return nil, false, fmt.Errorf("error reading template %s, %v\n", formatArgument, err)
		}
		printer, err = NewTemplatePrinter(data)
		if err != nil {
			return nil, false, fmt.Errorf("error parsing template %s, %v\n", string(data), err)
		}
	case "jsonpath":
		if len(formatArgument) == 0 {
			return nil, false, fmt.Errorf("jsonpath template format specified but no template given")
		}
		var err error
		printer, err = NewJSONPathPrinter(formatArgument)
		if err != nil {
			return nil, false, fmt.Errorf("error parsing jsonpath %s, %v\n", formatArgument, err)
		}
	case "jsonpath-file":
		if len(formatArgument) == 0 {
			return nil, false, fmt.Errorf("jsonpath file format specified but no template file file given")
		}
		data, err := ioutil.ReadFile(formatArgument)
		if err != nil {
			return nil, false, fmt.Errorf("error reading template %s, %v\n", formatArgument, err)
		}
		printer, err = NewJSONPathPrinter(string(data))
		if err != nil {
			return nil, false, fmt.Errorf("error parsing template %s, %v\n", string(data), err)
		}
	case "custom-columns":
		var err error
		if printer, err = NewCustomColumnsPrinterFromSpec(formatArgument, api.Codecs.UniversalDecoder(), noHeaders); err != nil {
			return nil, false, err
		}
	case "custom-columns-file":
		file, err := os.Open(formatArgument)
		if err != nil {
			return nil, false, fmt.Errorf("error reading template %s, %v\n", formatArgument, err)
		}
		defer file.Close()
		if printer, err = NewCustomColumnsPrinterFromTemplate(file, api.Codecs.UniversalDecoder()); err != nil {
			return nil, false, err
		}
		/*
		 go里面switch默认相当于每个case最后带有break，
		 匹配成功后不会自动向下执行其他case，而是跳出整个switch。
		 但是可以使用fallthrough强制执行后面的case代码，
		 fallthrough不会判断下一条case的expr结果是否为true。
		 default不会被执行
		*/
	case "wide":
		fallthrough
	case "":
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("output format %q not recognized", format)
	}
	return printer, true, nil
}
```

## type JSONPrinter struct
调用"encoding/json" package对对象进行格式化，以json格式输出内容
```go
// JSONPrinter is an implementation of ResourcePrinter which outputs an object as JSON.
type JSONPrinter struct {
}

func (p *JSONPrinter) AfterPrint(w io.Writer, res string) error {
	return nil
}

// PrintObj is an implementation of ResourcePrinter.PrintObj which simply writes the object to the Writer.
func (p *JSONPrinter) PrintObj(obj runtime.Object, w io.Writer) error {
	switch obj := obj.(type) {
	case *runtime.Unknown:
		var buf bytes.Buffer
		err := json.Indent(&buf, obj.Raw, "", "    ")
		if err != nil {
			return err
		}
		buf.WriteRune('\n')
		_, err = buf.WriteTo(w)
		return err
	}

	/*
		调用"encoding/json" package对对象进行格式化
	*/
	data, err := json.MarshalIndent(obj, "", "    ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// TODO: implement HandledResources()
func (p *JSONPrinter) HandledResources() []string {
	return []string{}
}
```

- 用法
```shell
kubectl get ns -o json
```

## type YAMLPrinter struct
使用package "github.com/ghodss/yaml"

```go
// YAMLPrinter is an implementation of ResourcePrinter which outputs an object as YAML.
// The input object is assumed to be in the internal version of an API and is converted
// to the given version first.
type YAMLPrinter struct {
	version   string
	converter runtime.ObjectConvertor
}

func (p *YAMLPrinter) AfterPrint(w io.Writer, res string) error {
	return nil
}

// PrintObj prints the data as YAML.
func (p *YAMLPrinter) PrintObj(obj runtime.Object, w io.Writer) error {
	switch obj := obj.(type) {
	case *runtime.Unknown:
		data, err := yaml.JSONToYAML(obj.Raw)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

    //使用package "github.com/ghodss/yaml"
	output, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(w, string(output))
	return err
}

// TODO: implement HandledResources()
func (p *YAMLPrinter) HandledResources() []string {
	return []string{}
}
```

- 用法
```shell
kubectl get ns -o yaml
```

## type NamePrinter struct
以"resource/name" 形式输出一个对象
```go
// NamePrinter is an implementation of ResourcePrinter which outputs "resource/name" pair of an object.
type NamePrinter struct {
	Decoder runtime.Decoder
	Typer   runtime.ObjectTyper
}

func (p *NamePrinter) AfterPrint(w io.Writer, res string) error {
	return nil
}

// PrintObj is an implementation of ResourcePrinter.PrintObj which decodes the object
// and print "resource/name" pair. If the object is a List, print all items in it.
func (p *NamePrinter) PrintObj(obj runtime.Object, w io.Writer) error {
	if meta.IsListType(obj) {
		items, err := meta.ExtractList(obj)
		if err != nil {
			return err
		}
		if errs := runtime.DecodeList(items, p.Decoder, runtime.UnstructuredJSONScheme); len(errs) > 0 {
			return utilerrors.NewAggregate(errs)
		}
		for _, obj := range items {
			if err := p.PrintObj(obj, w); err != nil {
				return err
			}
		}
		return nil
	}

	name := "<unknown>"
	if acc, err := meta.Accessor(obj); err == nil {
		/*
			获取对象的name
		*/
		if n := acc.GetName(); len(n) > 0 {
			name = n
		}
	}

	/*
		首先获取该obj的 Kind；
		然后调用KindToResource()函数把gvk转化为resource，并获取其中的小写形式
	*/
	if kind := obj.GetObjectKind().GroupVersionKind(); len(kind.Kind) == 0 {
		// this is the old code.  It's unnecessary on decoded external objects, but on internal objects
		// you may have to do it.  Tests are definitely calling it with internals and I'm not sure who else
		// is
		if gvks, _, err := p.Typer.ObjectKinds(obj); err == nil {
			// TODO: this is wrong, it assumes that meta knows about all Kinds - should take a RESTMapper
			_, resource := meta.KindToResource(gvks[0])
			fmt.Fprintf(w, "%s/%s\n", resource.Resource, name)
		} else {
			fmt.Fprintf(w, "<unknown>/%s\n", name)
		}

	} else {
		// TODO: this is wrong, it assumes that meta knows about all Kinds - should take a RESTMapper
		_, resource := meta.KindToResource(kind)
		fmt.Fprintf(w, "%s/%s\n", resource.Resource, name)
	}

	return nil
}

// TODO: implement HandledResources()
func (p *NamePrinter) HandledResources() []string {
	return []string{}
}
```

- 用法
```shell
# kubectl get ns -o name
namespace/default
namespace/kube-system
```

## type TemplatePrinter struct
以package "text/template" 来实现自定义格式输出，具体用法见[example](https://golang.org/pkg/text/template/#pkg-examples)

```
// TemplatePrinter is an implementation of ResourcePrinter which formats data with a Go Template.
type TemplatePrinter struct {
	rawTemplate string
	template    *template.Template
}

func NewTemplatePrinter(tmpl []byte) (*TemplatePrinter, error) {
	/*
		入参tmpl []byte是自定义格式
	*/
	t, err := template.New("output").
		Funcs(template.FuncMap{"exists": exists}).
		Parse(string(tmpl))
	if err != nil {
		return nil, err
	}
	return &TemplatePrinter{
		rawTemplate: string(tmpl),
		template:    t,
	}, nil
}

func (p *TemplatePrinter) AfterPrint(w io.Writer, res string) error {
	return nil
}

// PrintObj formats the obj with the Go Template.
func (p *TemplatePrinter) PrintObj(obj runtime.Object, w io.Writer) error {
	var data []byte
	var err error
	if unstructured, ok := obj.(*runtime.Unstructured); ok {
		data, err = json.Marshal(unstructured.Object)
	} else {
		data, err = json.Marshal(obj)

	}
	if err != nil {
		return err
	}

	out := map[string]interface{}{}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if err = p.safeExecute(w, out); err != nil {
		// It is way easier to debug this stuff when it shows up in
		// stdout instead of just stdin. So in addition to returning
		// a nice error, also print useful stuff with the writer.
		fmt.Fprintf(w, "Error executing template: %v. Printing more information for debugging the template:\n", err)
		fmt.Fprintf(w, "\ttemplate was:\n\t\t%v\n", p.rawTemplate)
		fmt.Fprintf(w, "\traw data was:\n\t\t%v\n", string(data))
		fmt.Fprintf(w, "\tobject given to template engine was:\n\t\t%+v\n\n", out)
		return fmt.Errorf("error executing template %q: %v", p.rawTemplate, err)
	}
	return nil
}

// TODO: implement HandledResources()
func (p *TemplatePrinter) HandledResources() []string {
	return []string{}
}

// safeExecute tries to execute the template, but catches panics and returns an error
// should the template engine panic.
func (p *TemplatePrinter) safeExecute(w io.Writer, obj interface{}) error {
	var panicErr error
	// Sorry for the double anonymous function. There's probably a clever way
	// to do this that has the defer'd func setting the value to be returned, but
	// that would be even less obvious.
	retErr := func() error {
		defer func() {
			if x := recover(); x != nil {
				panicErr = fmt.Errorf("caught panic: %+v", x)
			}
		}()
		return p.template.Execute(w, obj)
	}()
	if panicErr != nil {
		return panicErr
	}
	return retErr
}
```

- 用法
```shell
# kubectl get ns default  -o go-template="my attribute is {{.metadata.name}} {{.metadata.uid}} {{.apiVersion}}"
my attribute is default d8816de2-c709-11e7-861d-080027e58fc6 v1

# kubectl get ns default  -o go-template="The obj is {{.}}"
```
也可以把格式写入到一个文件中，用go-template-file指定格式文件

## type JSONPathPrinter struct
具体规则见https://kubernetes.io/docs/user-guide/jsonpath/
```go
# kubectl get ns default  -o jsonpath="The obj is {@}"
The obj is map[spec:map[finalizers:[kubernetes]] status:map[phase:Active] kind:Namespace apiVersion:v1 metadata:map[name:default selfLink:/api/v1/namespacesdefault uid:d8816de2-c709-11e7-861d-080027e58fc6 resourceVersion:14 creationTimestamp:2017-11-11T17:57:55Z]]
```

## type CustomColumnsPrinter struct
CustomColumnsPrinter允许用户定义列名，是JSONPath的加强版。支持从命令行直接输入模板和从文件读取模板两种方式。

具体用法见[custom-columns](https://kubernetes.io/docs/user-guide/kubectl-overview/#custom-columns)
```shell
# kubectl get ns default -o=custom-columns=NAME:.metadata.name,RSRC:.metadata.resourceVersion
NAME      RSRC
default   14
```
以文件形式指定格式，文件内容如下
```shell
# kubectl get ns default  -o=custom-columns-file=format
# cat format 
NAME                    RSRC
metadata.name           metadata.resourceVersion
```

## type HumanReadablePrinter struct
HumanReadablePrinter中有handlerMap，handlerMap记录了待打印的对象到处理函数的映射关系。系统中每种资源都有对应的打印函数。

`kubectl -o wide`调用的也是HumanReadablePrinter。

```go
// HumanReadablePrinter is an implementation of ResourcePrinter which attempts to provide
// more elegant output. It is not threadsafe, but you may call PrintObj repeatedly; headers
// will only be printed if the object type changes. This makes it useful for printing items
// received from watches.
type HumanReadablePrinter struct {
	handlerMap   map[reflect.Type]*handlerEntry
	options      PrintOptions
	lastType     reflect.Type
	hiddenObjNum int
}

//新建一个HumanReadablePrinter对象
// NewHumanReadablePrinter creates a HumanReadablePrinter.
func NewHumanReadablePrinter(options PrintOptions) *HumanReadablePrinter {
	printer := &HumanReadablePrinter{
		handlerMap: make(map[reflect.Type]*handlerEntry),
		options:    options,
	}
	printer.addDefaultHandlers()
	return printer
}
```

printHeader()打印第一行标题
```
func (h *HumanReadablePrinter) printHeader(columnNames []string, w io.Writer) error {
	if _, err := fmt.Fprintf(w, "%s\n", strings.Join(columnNames, "\t")); err != nil {
		return err
	}
	return nil
}
```

打印具体的资源，如pod
```go
func (h *HumanReadablePrinter) printPod(pod *api.Pod, w io.Writer, options PrintOptions) error {
	if err := printPodBase(pod, w, options); err != nil {
		return err
	}

	return nil
}

func printPodBase(pod *api.Pod, w io.Writer, options PrintOptions) error {
	name := formatResourceName(options.Kind, pod.Name, options.WithKind)
	namespace := pod.Namespace

	restarts := 0
	totalContainers := len(pod.Spec.Containers)
	readyContainers := 0

	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += int(container.RestartCount)
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}
	if !initializing {
		restarts = 0
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += int(container.RestartCount)
			if container.State.Waiting != nil && container.State.Waiting.Reason != "" {
				reason = container.State.Waiting.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason != "" {
				reason = container.State.Terminated.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason == "" {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else if container.Ready && container.State.Running != nil {
				readyContainers++
			}
		}
	}

	if pod.DeletionTimestamp != nil && pod.Status.Reason == node.NodeUnreachablePodReason {
		reason = "Unknown"
	} else if pod.DeletionTimestamp != nil {
		reason = "Terminating"
	}

	if options.WithNamespace {
		if _, err := fmt.Fprintf(w, "%s\t", namespace); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s\t%d/%d\t%s\t%d\t%s",
		name,
		readyContainers,
		totalContainers,
		reason,
		restarts,
		translateTimestamp(pod.CreationTimestamp),
	); err != nil {
		return err
	}

	if options.Wide {
		/*
			-o wide则写入IP和NODE
		*/
		nodeName := pod.Spec.NodeName
		podIP := pod.Status.PodIP
		if podIP == "" {
			podIP = "<none>"
		}
		if _, err := fmt.Fprintf(w, "\t%s\t%s",
			podIP,
			nodeName,
		); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprint(w, AppendLabels(pod.Labels, options.ColumnLabels)); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, AppendAllLabels(options.ShowLabels, pod.Labels)); err != nil {
		return err
	}

	return nil
}
```

## 对输出内容进行排序
```shell
kubectl get pods --sort-by=.metadata.name
```

## 参考
[kubectl-overview](https://kubernetes.io/docs/user-guide/kubectl-overview/#custom-columns)
[blog](https://fankangbest.github.io/2017/07/19/Kubectl概念解读(五)-Printer-v1-5-2/)