# project apimachinery

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [project apimachinery](#project-apimachinery)
  - [k8s里面的apimachinery package](#k8s里面的apimachinery-package)
  - [project apimachinery中各个package的作用](#project-apimachinery中各个package的作用)
  - [总结](#总结)

<!-- END MUNGE: GENERATED_TOC -->

首先弄清两个apimachinery的范畴，一个是project，一个是k8s里面的一个package。前者包含了后者,
k8s是前者的消费者。

## project apimachinery
查看项目[kubernetes/apimachinery](https://github.com/kubernetes/apimachinery)的介绍，

Scheme, typing, encoding, decoding, and conversion packages for Kubernetes and Kubernetes-like API objects。
这是一套用于kubernetes和类kubernetes API的Scheme, typing, encoding, decoding, and conversion packages。

This library is a shared dependency for servers and clients to work with Kubernetes API infrastructure without direct type dependencies. It's first comsumers are k8s.io/kubernetes, k8s.io/client-go, and k8s.io/apiserver.

这里提到了kubernetes采用的API机制就是本项目，见`/kubernetes-1.5.2/pkg/apimachinery`，用的就是本项目的`pkg/apimachinery/`

从这里可以看出，kubernetes把其很多组件以开源项目的形式单独开源出来了，比如[k8s.io/apiserver](https://github.com/kubernetes/apiserver)。

需要注意的一点是，Do not add API types to this repo. This is for the machinery, not for the types.
也就是说apimachinery是一套通用的API机制，不要往里面添加一些具体的type，比如说k8s里面的'pod、service'这些就是一些具体的type。

这个项目基本是和kubernetes源码保持一致的，所以可以通过它来学习kubernetes的通用API机制。[godoc-apimachinery](https://godoc.org/k8s.io/apimachinery)中列出了本项目的情况，可以发现很多都是和kuebrnetes源码对应的。

## k8s里面的apimachinery package
我们来查看k8s源码里面的目录结构
```
                     -------announced
                     |
                     |
pkg---apimachinery----------registered
                     |
                     |
                     -----type.go					  
```
三个package的作用
```
pkg/apimachinery	Package apimachinery contains the generic API machinery code that is common to both server and clients.

pkg/apimachinery/announced	Package announced contains tools for announcing API group factories.

pkg/apimachinery/registered	Package to keep track of API Versions that can be registered and are enabled in api.Scheme.

```

## project apimachinery中各个package的作用
先来个总体的概念，后面才好更好地深入了解。
```
pkg/api/errors	提供了api字段验证的详细错误类型。－Package errors provides detailed error types for api field validation.

pkg/apimachinery	Package apimachinery contains the generic API machinery code that is common to both server and clients.

pkg/apimachinery/announced	Package announced contains tools for announcing API group factories.

pkg/apimachinery/registered	Package to keep track of API Versions that can be registered and are enabled in api.Scheme.

pkg/api/meta	Package meta provides functions for retrieving API metadata from objects belonging to the Kubernetes API

pkg/api/resource	Package resource is a generated protocol buffer package.
pkg/apis/meta/fuzzer	
pkg/apis/meta/internalversion	
pkg/apis/meta/v1	
pkg/apis/meta/v1alpha1	
pkg/apis/meta/v1/unstructured	
pkg/apis/meta/v1/validation	
pkg/apis/testapigroup	
pkg/apis/testapigroup/fuzzer	

pkg/apis/testapigroup/install	Package install installs the certificates API group, making it available as an option to all of the API encoding/decoding machinery.

pkg/apis/testapigroup/v1	
pkg/api/testing	
pkg/api/testing/fuzzer	
pkg/api/testing/roundtrip	

pkg/api/validation	Package validation contains generic api type validation functions.
pkg/api/validation/path	

pkg/conversion	Package conversion provides go object versioning.

pkg/conversion/queryparams	Package queryparams provides conversion from versioned runtime objects to URL query values

pkg/conversion/unstructured	Package unstructured provides conversion from runtime objects to map[string]interface{} representation.

pkg/fields	Package fields implements a simple field system, parsing and matching selectors with sets of fields.

pkg/labels	Package labels implements a simple label system, parsing and matching selectors with sets of labels.

pkg/runtime	Defines conversions between generic types and structs to map query strings to struct objects.

pkg/runtime/schema	Package schema is a generated protocol buffer package.

pkg/runtime/serializer	

pkg/runtime/serializer/json	

pkg/runtime/serializer/protobuf	Package protobuf provides a Kubernetes serializer for the protobuf format.

pkg/runtime/serializer/recognizer	

pkg/runtime/serializer/streaming	Package streaming implements encoder and decoder for streams of runtime.Objects over io.Writer/Readers.

pkg/runtime/serializer/testing	
pkg/runtime/serializer/versioning	
pkg/runtime/serializer/yaml	
pkg/runtime/testing	
pkg/selection	
pkg/test	

pkg/types	Package types implements various generic types used throughout kubernetes.

pkg/util/cache	
pkg/util/clock	
pkg/util/diff
	
pkg/util/errors	Package errors implements various utility functions and types around errors.

pkg/util/framer	Package framer implements simple frame decoding techniques for an io.ReadCloser

pkg/util/httpstream	Package httpstream adds multiplexed streaming support to HTTP requests and responses via connection upgrades.

pkg/util/httpstream/spdy
pkg/util/initialization	
pkg/util/intstr	Package intstr is a generated protocol buffer package.
pkg/util/json	
pkg/util/jsonmergepatch	
pkg/util/mergepatch	
pkg/util/net	
pkg/util/proxy	Package proxy provides transport and upgrade support for proxies.
pkg/util/rand	Package rand provides utilities related to randomization.
pkg/util/remotecommand	
pkg/util/runtime	
pkg/util/sets	Package sets has auto-generated set types.
pkg/util/sets/types	Package types just provides input types to the set generator.
pkg/util/strategicpatch	
pkg/util/uuid	
pkg/util/validation	
pkg/util/validation/field	
pkg/util/wait	Package wait provides tools for polling or listening for changes to a condition.
pkg/util/yaml	
pkg/version	Package version supplies the type for version information collected at build time.

pkg/watch	Package watch contains a generic watchable interface, and a fake for testing code that uses the watch interface.

third_party/forked/golang/json	Package json is forked from the Go standard library to enable us to find the field of a struct that a given JSON key maps to.

third_party/forked/golang/netutil	

third_party/forked/golang/reflect	Package reflect is a fork of go's standard library reflection package, which allows for deep equal with equality functions defined.

```

## 总结
本文主要对开源项目apimachinery来了个整体性的介绍，并没有深入讲解，但相信，了解这些项目的由来和关系对后面深入学习k8s的机制是很有帮助的。

下一篇将对[k8s里面的apimachinery package](#k8s里面的apimachinery-package)进行讲解。



## 参考
[开源项目-apimachinery](https://github.com/kubernetes/apimachinery)

[godoc-开源项目apimachinery](https://godoc.org/k8s.io/apimachinery)

[开源项目-apiserver](https://github.com/kubernetes/apiserver)