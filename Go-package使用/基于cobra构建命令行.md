# 基于cobra构建命令行

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [环境准备](#环境准备)
  - [给root命令添加参数](#给root命令添加参数)
  - [添加子命令](#添加子命令)
  - [多级命令](#多级命令)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

从三个例子，来说明如何使用`github.com/spf13/cobra`，构造自己的命令行

## 环境准备
```shell
go get github.com/spf13/cobra/cobra
```
会发现$GOPATH/bin下生成了一个可执行文件cobra，利用可执行文件cobra来生成cobra_example工程。

```shell
./bin/cobra init cobra_example
```
会发现/src目录下自动生成cobra_example工程，目录如下所示：
```
▾ cobra_example/
    ▾ cmd/
        root.go
      main.go
```

## 给root命令添加参数
此时的代码如下：
- main.go
```go
package main

import "cobra_example/cmd"

func main() {
	cmd.Execute()
}
```

- cmd/root.go
```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	//  "github.com/spf13/viper"
	"cobra_example/add"
)

//var cfgFile string
var name string
var age int

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "demo",
	Short: "A test demo",
	Long:  `Demo is a test appcation for print things`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {
		if len(name) == 0 {
			cmd.Help()
			return
		}
		add.Show(name, age)
	},
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {
	RootCmd.Flags().StringVarP(&name, "name", "n", "", "person's name")
	RootCmd.Flags().IntVarP(&age, "age", "a", 0, "person's age")
}
```

- 新建个add package
```
▾ cobra_example/
    ▾ add/
        add.go
    ▾ cmd/
        root.go
      main.go
```
```go
package add

import(
    "fmt"
)

func Show(name string, age int) {
    fmt.Printf("My Name is %s, My age is %d\n", name, age)
}
```

然后go build 生成可执行文件，最后执行效果如下
```shell
# ./cobra_example -a 11 -n lining
My Name is lining, My age is 11
```

## 添加子命令
```shell
# ./cobra_example --help
Demo is a test appcation for print things

Usage:
  demo [flags]
  demo [command]

Available Commands:
  test        A brief description of your command

Flags:
  -a, --age int       person's age
  -n, --name string   person's name

Use "demo [command] --help" for more information about a command.

# ./cobra_example test  
test called
My Name is , My age is 0
```
我们在前面例子的基础上进行改进，目录结构如下
```
▾ cobra_example/
    ▾ add/
        add.go
    ▾ cmd/
        root.go
        test.go	
      main.go
```

- test.go文件内容如下
```go
package cmd

import (
	"fmt"

	"cobra_example/add"
	"github.com/spf13/cobra"
)

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("test called")
		add.Show(name, age)
	},
}

func init() {
	RootCmd.AddCommand(testCmd)

	testCmd.Flags().StringVarP(&name, "name", "n", "", "person's name")
	testCmd.Flags().IntVarP(&age, "age", "a", 0, "person's age")
}
```

## 多级命令
```go
package main
import (
	"fmt"
	"strings"
	"github.com/spf13/cobra"
)

func main() {
var echoTimes int
var cmdPrint = &cobra.Command{
		Use:   "print [string to print]",
		Short: "Print anything to the screen",
		Long: `print is for printing anything back to the screen.
            For many years people have printed back to the screen.
            `,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Print: " + strings.Join(args, " "))
		},
	}
var cmdEcho = &cobra.Command{
		Use:   "echo [string to echo]",
		Short: "Echo anything to the screen",
		Long: `echo is for echoing anything back.
            Echo works a lot like print, except it has a child command.
            `,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Print: " + strings.Join(args, " "))
		},
	}
var cmdTimes = &cobra.Command{
		Use:   "times [# times] [string to echo]",
		Short: "Echo anything to the screen more times",
		Long: `echo things multiple times back to the user by providing
            a count and a string.`,
		Run: func(cmd *cobra.Command, args []string) {
			for i := 0; i < echoTimes; i++ {
				fmt.Println("Echo: " + strings.Join(args, " "))
			}
		},
	}
cmdTimes.Flags().IntVarP(&echoTimes, "times", "t", 1, "times to echo the input")
var rootCmd = &cobra.Command{Use: "app"}
	rootCmd.AddCommand(cmdPrint, cmdEcho)
	cmdEcho.AddCommand(cmdTimes)
	rootCmd.Execute()
}
```



## 参考
[urfave/cli](https://github.com/urfave/cli)，这是另外一个构造命令行工具cli

[cobra](https://github.com/spf13/cobra)


