# Yaml、JSON文件读取

前面介绍的都是kubectl与apiserver进行交互，本文主要分析`kubectl create -f xxx.yaml`命令是怎么读取文件，进而转化为符合k8s系统所需要的对象。

## 流程分析
1. 通过FileVisitor读取一个文件（yaml，json）。
```go
// Visit in a FileVisitor is just taking care of opening/closing files
func (v *FileVisitor) Visit(fn VisitorFunc) error {
	var f *os.File
	if v.Path == constSTDINstr {
		f = os.Stdin
	} else {
		var err error
		/*
			kubectl create的时候打开文件
		*/
		if f, err = os.Open(v.Path); err != nil {
			return err
		}
	}
	defer f.Close()
	/*
		把从文件中读取的内容赋值给v.StreamVisitor.Reader
	*/
	v.StreamVisitor.Reader = f

	return v.StreamVisitor.Visit(fn)
}
```

2. StreamVisitor的Visit()函数先通过调用NewYAMLOrJSONDecoder()生成decoder，然后使用Decode()对YAML文件中每一个YAML体进行解码。 YAML文件支持以支持”---“分割符，可以把多个YAML写在一个文件中。
```go
// Visit implements Visitor over a stream. StreamVisitor is able to distinct multiple resources in one stream.
/*
	译：StreamVisitor能够在一个流中区分多个resources。

	从io.reader中获取数据流，然后转换成json格式，最后进行schema检查，封装成info对象
*/
func (v *StreamVisitor) Visit(fn VisitorFunc) error {
	/*
		使用NewYAMLOrJSONDecoder生成一个Decoder，把指定YAML文档或JSON文档作为一个stream来进行处理
			==>定义在/pkg/util/yaml/decoder.go中
				==>func NewYAMLOrJSONDecoder(r io.Reader, bufferSize int) *YAMLOrJSONDecoder
	*/
	d := yaml.NewYAMLOrJSONDecoder(v.Reader, 4096)
	for {
		ext := runtime.RawExtension{}
		/*
			用解码器的Decode()对stream进行解析
		*/
		if err := d.Decode(&ext); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		// TODO: This needs to be able to handle object in other encodings and schemas.
		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}
		/*
			进行schema检查
		*/
		if err := ValidateSchema(ext.Raw, v.Schema); err != nil {
			return fmt.Errorf("error validating %q: %v", v.Source, err)
		}
		/*
			InfoForData用传入的数据生成一个Info object。
			会把json对象转换成对应的struct类型
				==>/pkg/kubectl/resource/mapper.go
					==>func (m *Mapper) InfoForData(data []byte, source string) (*Info, error)
		*/
		info, err := v.InfoForData(ext.Raw, v.Source)
		//		fmt.Println("InfoForData用传入的数据生成一个Info object***********")
		//		fmt.Println("source string is: ", v.Source)
		//		fmt.Println("info is: ", info)
		//		fmt.Println("ext.Raw is", ext.Raw)
		/*
			# kubectl create -f quota.yaml
			source string is:  quota.yaml
			info is:  &{0xc420899c80 0xc420374770 default quota quota.yaml 0xc4208eb018 0xc4208eb018  false}
		*/

		if err != nil {
			if fnErr := fn(info, err); fnErr != nil {
				return fnErr
			}
			continue
		}
		if err := fn(info, nil); err != nil {
			return err
		}
	}
}
```

3. NewYAMLOrJSONDecoder返回一个解码器，它将把指定YAML文档或JSON文档作为一个stream来进行处理。

4. 用YAMLOrJSONDecoder的Decode()对stream进行解析，Decode()是分析一个Decoder的重点函数。

## type YAMLOrJSONDecoder struct
```go
// NewYAMLOrJSONDecoder returns a decoder that will process YAML documents
// or JSON documents from the given reader as a stream. bufferSize determines
// how far into the stream the decoder will look to figure out whether this
// is a JSON stream (has whitespace followed by an open brace).
/*
	属性bufferSize确定解码器将在多长时间内查找出数据，以确定这是否是一个JSON流（在一个开放的大括号后有空格）。
*/
func NewYAMLOrJSONDecoder(r io.Reader, bufferSize int) *YAMLOrJSONDecoder {
	return &YAMLOrJSONDecoder{
		r:          r,
		bufferSize: bufferSize,
	}
}

// YAMLOrJSONDecoder attempts to decode a stream of JSON documents or
// YAML documents by sniffing for a leading { character.
type YAMLOrJSONDecoder struct {
	r          io.Reader
	bufferSize int

	decoder decoder
}

// Decode unmarshals the next object from the underlying stream into the
// provide object, or returns an error.

func (d *YAMLOrJSONDecoder) Decode(into interface{}) error {
	if d.decoder == nil {
		//判断输入的类型，JSON或YAML
		buffer, isJSON := GuessJSONStream(d.r, d.bufferSize)
		if isJSON {
			glog.V(4).Infof("decoding stream as JSON")
			d.decoder = json.NewDecoder(buffer)
		} else {
			glog.V(4).Infof("decoding stream as YAML")
			d.decoder = NewYAMLToJSONDecoder(buffer)
		}
	}
	/*
		进行解码
	*/
	err := d.decoder.Decode(into)
	if jsonDecoder, ok := d.decoder.(*json.Decoder); ok {
		if syntax, ok := err.(*json.SyntaxError); ok {
			data, readErr := ioutil.ReadAll(jsonDecoder.Buffered())
			if readErr != nil {
				glog.V(4).Infof("reading stream failed: %v", readErr)
			}
			js := string(data)
			start := strings.LastIndex(js[:syntax.Offset], "\n") + 1
			line := strings.Count(js[:start], "\n")
			return fmt.Errorf("json: line %d: %s", line, syntax.Error())
		}
	}
	return err
}
```
可以看到这里，根据输入的格式来使用不同的Decoder。 
如果输入的前缀是"{"，则判断为是JSON。
```go
// GuessJSONStream scans the provided reader up to size, looking
// for an open brace indicating this is JSON. It will return the
// bufio.Reader it creates for the consumer.
/*
	如果输入的前缀是"{"，则判断为是JSON
*/
func GuessJSONStream(r io.Reader, size int) (io.Reader, bool) {
	buffer := bufio.NewReaderSize(r, size)
	b, _ := buffer.Peek(size)
	return buffer, hasJSONPrefix(b)
}

var jsonPrefix = []byte("{")

// hasJSONPrefix returns true if the provided buffer appears to start with
// a JSON open brace.
func hasJSONPrefix(buf []byte) bool {
	return hasPrefix(buf, jsonPrefix)
}
```

## type YAMLToJSONDecoder struct
我们以YAML为例子，继续进行分析。 type YAMLToJSONDecoder struct本质上是对type YAMLReader struct进行了封装和操作。

type YAMLToJSONDecoder struct封装了一个Reader，以YAML的格式对io.Reader进行解码。 
它首先会把YAML转化为JSON，然后对JSON进行反序列化(unmarshal)操作。

```go
// NewYAMLToJSONDecoder decodes YAML documents from the provided
// stream in chunks by converting each document (as defined by
// the YAML spec) into its own chunk, converting it to JSON via
// yaml.YAMLToJSON, and then passing it to json.Decoder.
/*
	func NewYAMLToJSONDecoder把YAML文件进行解码，
	通过yaml.YAMLToJSON转换到JSON，
	最后传递给 json.Decoder
*/
func NewYAMLToJSONDecoder(r io.Reader) *YAMLToJSONDecoder {
	reader := bufio.NewReader(r)
	return &YAMLToJSONDecoder{
		reader: NewYAMLReader(reader),
	}
}

// YAMLToJSONDecoder decodes YAML documents from an io.Reader by
// separating individual documents. It first converts the YAML
// body to JSON, then unmarshals the JSON.
type YAMLToJSONDecoder struct {
	reader Reader
}

// Decode reads a YAML document as JSON from the stream or returns
// an error. The decoding rules match json.Unmarshal, not
// yaml.Unmarshal.
/*
	使用的是json.Unmarshal，而不是yaml.Unmarshal
*/
func (d *YAMLToJSONDecoder) Decode(into interface{}) error {
	/*
		返回整个yaml文件的数据给bytes
	*/
	bytes, err := d.reader.Read()
	if err != nil && err != io.EOF {
		return err
	}

	if len(bytes) != 0 {
		//把yaml转化为json，再进行json.Unmarshal(data, into)
		data, err := yaml.YAMLToJSON(bytes)
		if err != nil {
			return err
		}
		//对data进行反序列化操作
		return json.Unmarshal(data, into)
	}
	return err
}
```

## type YAMLReader struct
type YAMLReader struct 负责读取整个 YAML 文件，封装了一个type LineReader struct。

```go
type YAMLReader struct {
	reader Reader
}

func NewYAMLReader(r *bufio.Reader) *YAMLReader {
	return &YAMLReader{
		/*
			封装了一个Lineder
		*/
		reader: &LineReader{reader: r},
	}
}

// Read returns a full YAML document.
/*
	读取整个 YAML 文件
*/
func (r *YAMLReader) Read() ([]byte, error) {
	var buffer bytes.Buffer
	for {
		/*
			读取YAML 文件中的一行
		*/
		line, err := r.reader.Read()
		if err != nil && err != io.EOF {
			return nil, err
		}

		/*
			分隔符 const separator = "---"
			YAML文件支持以”---“分割符，可以把多个YAML写在一个文件中。
		*/
		sep := len([]byte(separator))
		if i := bytes.Index(line, []byte(separator)); i == 0 {
			// We have a potential document terminator
			i += sep
			after := line[i:]
			if len(strings.TrimRightFunc(string(after), unicode.IsSpace)) == 0 {
				if buffer.Len() != 0 {
					//返回分割符之前的数据
					return buffer.Bytes(), nil
				}
				if err == io.EOF {
					return nil, err
				}
			}
		}
		if err == io.EOF {
			if buffer.Len() != 0 {
				// If we're at EOF, we have a final, non-terminated line. Return it.
				return buffer.Bytes(), nil
			}
			return nil, err
		}
		//把一行数据写入buffer中
		buffer.Write(line)
	}
}
```

## type LineReader struct
type LineReader struct负责读取YAML文件中的一行。 是对标准package bufio的封装使用。
```go
type LineReader struct {
	//封装了一个bufio.Reader
	reader *bufio.Reader
}

// Read returns a single line (with '\n' ended) from the underlying reader.
// An error is returned iff there is an error with the underlying reader.
/*
	调用bufio.Reader的ReadLine()方法读取一行，
	并在一行的结尾加上’\n’。
*/
func (r *LineReader) Read() ([]byte, error) {
	var (
		isPrefix bool  = true
		err      error = nil
		line     []byte
		buffer   bytes.Buffer
	)

	for isPrefix && err == nil {
		line, isPrefix, err = r.reader.ReadLine()
		buffer.Write(line)
	}
	buffer.WriteByte('\n')
	return buffer.Bytes(), err
}
```

## Demo
这里实现了把一个json文件或者yaml文件中的数据转化为一个`type generic map[string]interface{}`类型进行输出
```yaml
apiVersion: v1
kind: Pod
```
或者读取一个json文件

```go
package main

import (
	"bytes"
	"fmt"
	"k8s.io/kubernetes/pkg/util/yaml"
	"io"
	"os"
)

type generic map[string]interface{}

func main() {
	filePath := "/Users/fanqihong/Desktop/go-project/src/ftmtest/mysql.json"
	input := openFile(filePath)
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(input)), 4096)
	objs := []generic{}

	var err error
	for {
		out := make(generic)
		err = decoder.Decode(&out)
		if err != nil {
			break
		}
		fmt.Println(out["metadata"])
		objs = append(objs, out)
	}
	if err != io.EOF {
		fmt.Println("err is:", err)
		return
	}
	fmt.Println(objs)
}

func openFile(path string) []byte {
	file, err := os.Open(path) // For read access.
	if err != nil {
		fmt.Println(err)
	}
	data := make([]byte, 4096)
	count, err := file.Read(data)
	if err != nil {
		fmt.Println(err)
	}
	defer file.Close()
	return data[:count]

}
```

## 多种格式的相互转换
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type ConfigStruct struct {
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	AnalyticsFile     string   `json:"analytics_file"`
	StaticFileVersion int      `json:"static_file_version"`
	StaticDir         string   `json:"static_dir"`
	TemplatesDir      string   `json:"templates_dir"`
	SerTcpSocketHost  string   `json:"serTcpSocketHost"`
	SerTcpSocketPort  int      `json:"serTcpSocketPort"`
	Fruits            []string `json:"fruits"`
}

type Other struct {
	SerTcpSocketHost string   `json:"serTcpSocketHost"`
	SerTcpSocketPort int      `json:"serTcpSocketPort"`
	Fruits           []string `json:"fruits"`
}

func main() {
	jsonStr := `{"host": "http://localhost:9090","port": 9090,"analytics_file": "","static_file_version": 1,"static_dir": "E:/Project/goTest/src/","templates_dir": "E:/Project/goTest/src/templates/","serTcpSocketHost": ":12340","serTcpSocketPort": 12340,"fruits": ["apple", "peach"]}`

	//json str 转map
	var dat map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &dat); err == nil {
		fmt.Println("==============json str 转map=======================")
		fmt.Println(dat)
		fmt.Println(dat["host"])
	}

	//json str 转struct
	var config ConfigStruct
	if err := json.Unmarshal([]byte(jsonStr), &config); err == nil {
		fmt.Println("================json str 转struct==")
		fmt.Println(config)
		fmt.Println(config.Host)
	}

	//json str 转struct(部份字段)
	var part Other
	if err := json.Unmarshal([]byte(jsonStr), &part); err == nil {
		fmt.Println("================json str 转struct==")
		fmt.Println(part)
		fmt.Println(part.SerTcpSocketPort)
	}

	//struct 到json str
	if b, err := json.Marshal(config); err == nil {
		fmt.Println("================struct 到json str==")
		fmt.Println(string(b))
	}

	//map 到json str
	fmt.Println("================map 到json str=====================")
	enc := json.NewEncoder(os.Stdout)
	enc.Encode(dat)

	//array 到 json str
	arr := []string{"hello", "apple", "python", "golang", "base", "peach", "pear"}
	lang, err := json.Marshal(arr)
	if err == nil {
		fmt.Println("================array 到 json str==")
		fmt.Println(string(lang))
	}

	//json 到 []string
	var wo []string
	if err := json.Unmarshal(lang, &wo); err == nil {
		fmt.Println("================json 到 []string==")
		fmt.Println(wo)
	}
}
```

## 序列化和反序列化

把对象转换为字节序列的过程称为对象的序列化；把字节序列恢复为对象的过程称为对象的反序列化。

对象的序列化主要有两种用途：

1. 把对象的字节序列永久地保存到硬盘上，通常存放在一个文件中；
2. 在网络上传送对象的字节序列。

在很多应用中，需要对某些对象进行序列化，让它们离开内存空间，入驻物理硬盘，以便长期保存。比如最常见的是Web服务器中的Session对象，当有 10万用户并发访问，就有可能出现10万个Session对象，内存可能吃不消，于是Web容器就会把一些seesion先序列化到硬盘中，等要用了，再把保存在硬盘中的对象还原到内存中。

当两个进程在进行远程通信时，彼此可以发送各种类型的数据。无论是何种类型的数据，都会以二进制序列的形式在网络上传送。发送方需要把这个具体类型的对象转换为字节序列，才能在网络上传送；接收方则需要把字节序列再恢复为具体类型的对象。
