# First Blood
## /cmd/kubectl/app/kubectl.go
定义了一个cmd，然后执行cmd.Execute()
这里用到了第三方包"github.com/spf13/cobra"，这是一个功能强大的工具
```go
func Run() error {
	logs.InitLogs()
	defer logs.FlushLogs()

	/*
		构建了一个cmd，然后调用了Execute
		参数除了几个标准的输入输出之外，就只有一个NewFactory

		NewKubectlCommand 定义在pkg/kubectl/cmd/cmd.go

		cmdutil.NewFactory(nil)
			==>/pkg/kubectl/cmd/util/factory.go
	*/
	cmd := cmd.NewKubectlCommand(cmdutil.NewFactory(nil), os.Stdin, os.Stdout, os.Stderr)
	return cmd.Execute()
}
```	
## /pkg/kubectl/cmd/util/factory.go
`	// NewFactory creates a factory with the default Kubernetes resources defined
	// if optionalClientConfig is nil, then flags will be bound to a new clientcmd.ClientConfig.
	// if optionalClientConfig is not nil, then this factory will make use of it.
	/*
	译：func NewFactory用默认kubernetes resourecs 创建一个factory。
	   如果入参optionalClientConfig为nil，flags会被绑定到一个新的clientcmd.ClientConfig。
	   如果入参optionalClientConfig非nil，该factory会使用它。
	*/
	func NewFactory(optionalClientConfig clientcmd.ClientConfig) Factory {
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	flags.SetNormalizeFunc(utilflag.WarnWordSepNormalizeFunc) // Warn for "_" flags

	clientConfig := optionalClientConfig
	/*
		默认情况下，/cmd/kubectl/app/kubectl.go中传递过来的入参optionalClientConfig是nil
	*/
	if optionalClientConfig == nil {
		clientConfig = DefaultClientConfig(flags)
	}

	/*
		获取一个ClientCache
		type ClientCache struct 缓存先前加载的clients以便重用，并确保MatchServerVersion仅被调用一次
	*/
	clients := NewClientCache(clientConfig)

	f := &factory{
		flags:        flags,
		clientConfig: clientConfig,
		clients:      clients,
	}

	return f
	}`
## /pkg/kubectl/cmd/cmd.go
`	NewKubectlCommand创建kubectl命令及其嵌套子命令。
func NewKubectlCommand(f cmdutil.Factory, in io.Reader, out, err io.Writer) *cobra.Command{
	/*
		声明了多组 命令集合
		是对"github.com/spf13/cobra"的再一次封装

		/pkg/kubectl/cmd/templates/command_groups.go
			==>type CommandGroups []CommandGroup

		所有的命令都与入参f cmdutil.Factory有关，顺着f的数据流向搞懂factory的原理
	*/
	groups := templates.CommandGroups{
		......
	}
	
	/*
		Add定义在/pkg/kubectl/cmd/templates/command_groups.go
			==>func (g CommandGroups) Add(c *cobra.Command)
		把根命令kubectl 传递进去
		其完成的功能是把上面声明的所有命令(create、delete等)添加到kubectl下，成为kubectl的二级子命令
	*/
	groups.Add(cmds)
	}`
	下面以get 命令为例子，go on

## /pkg/kubectl/cmd/get.go
`	//从server段获取数据
	func NewCmdGet(f cmdutil.Factory, out io.Writer, errOut io.Writer) *cobra.Command`
	
	