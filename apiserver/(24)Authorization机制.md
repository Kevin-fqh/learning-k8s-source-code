# Authorization机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Overview](#overview)
  - [初始化加载Authorization Plugin](#初始化加载authorization-plugin)
  - [type unionAuthzHandler](#type-unionauthzhandler)
  - [MODEL ABAC](#model-abac)
  - [MODEL RBAC](#model-rbac)
  - [RBAC vs ABAC](#rbac-vs-abac)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## Overview
当一个来自于 User 的 Request 通过认证之后，该 Request 必须要进行授权。 
一个Request必须包含以下内容：
  * the username of the requester
  * the requested action
  * the object affected by the action
该Request的授权能否通过，取决于是现有的授权规则是否声明了允许该user去完成其请求的action。

如下面例子所示，用户 bob 仅仅被允许在 namespace `projectCaribou` 中读取 pod 资源：
```yaml
{
    "apiVersion": "abac.authorization.kubernetes.io/v1beta1",
    "kind": "Policy",
    "spec": {
        "user": "bob",
        "namespace": "projectCaribou",
        "resource": "pods",
        "readonly": true
    }
}
```

此时用户 bob 发起以下请求，是可以通过授权的:
```json
{
  "apiVersion": "authorization.k8s.io/v1beta1",
  "kind": "SubjectAccessReview",
  "spec": {
    "resourceAttributes": {
      "namespace": "projectCaribou",
      "verb": "get",
      "group": "unicorn.example.org",
      "resource": "pods"
    }
  }
}
```

下述操作，都是会被授权机制给拒绝的：
  * 如果用户 bob 试图对 namespace `projectCaribou`中的资源进行写操作（`create` or `update`）；
  * 如果用户 bob 试图对其它 namespace 中的资源进行读操作（`get`）

k8s的Authorization机制要求用户使用通用的 REST 属性来和控制系统进行交互，这是因为控制系统可能需要和其它API进行交互。 
k8s的Authorization机制目前支持多种授权模型，如：
   * Node Mode, v1.7+支持，配合NodeRestriction准入控制来限制kubelet仅可访问node、endpoint、pod、service以及secret、configmap、PV和PVC等相关的资源。
   * ABAC Mode, 
   * RBAC Mode,
   * Webhook Mode，
   * AlwaysDeny仅用来测试，
   * AlwaysAllow则允许所有请求（会覆盖其他模式）

用户在启动kube-apiserver的时候可以指定多种模型。 
如果设置了多种模型，k8s会按顺序进行检查。 
  * 和`Authenticator机制`一样，只要有其中一种模型允许该 Request，那么就算 PASS 了。 
  * 如果所有的模型都 Say NO，则拒绝该 Request，返回 HTTP status code 403。 

这同时也说明，一个 Request 在默认情况下其`permissions`都是被拒绝的。

使用方法
```
--authorization-mode=RBAC
```

### Request Attributes
K8s授权机制仅处理以下的请求属性:
* user, group, extra
* API
* 请求方法如 get、post、update、patch和delete
* 请求路径（如/api和/healthz）
* 请求资源和子资源
* Namespace
* API Group


## 初始化加载Authorization Plugin
Authorization部分和Authentication部分是很类似的，还是见/cmd/kube-apiserver/app/server.go的Run()
```go
	/*
		授权，Authenticator机制
			==>/pkg/genericapiserver/authorizer/authz.go
				==>type AuthorizationConfig struct
	*/
	authorizationConfig := authorizer.AuthorizationConfig{
		PolicyFile:                  s.GenericServerRunOptions.AuthorizationPolicyFile,
		WebhookConfigFile:           s.GenericServerRunOptions.AuthorizationWebhookConfigFile,
		WebhookCacheAuthorizedTTL:   s.GenericServerRunOptions.AuthorizationWebhookCacheAuthorizedTTL,
		WebhookCacheUnauthorizedTTL: s.GenericServerRunOptions.AuthorizationWebhookCacheUnauthorizedTTL,
		RBACSuperUser:               s.GenericServerRunOptions.AuthorizationRBACSuperUser,
		InformerFactory:             sharedInformers,
	}
	authorizationModeNames := strings.Split(s.GenericServerRunOptions.AuthorizationMode, ",")
	apiAuthorizer, err := authorizer.NewAuthorizerFromAuthorizationConfig(authorizationModeNames, authorizationConfig)
	if err != nil {
		glog.Fatalf("Invalid Authorization Config: %v", err)
	}
```

- func NewAuthorizerFromAuthorizationConfig()

func NewAuthorizerFromAuthorizationConfig()最后会把`authorizers`封装到`type unionAuthzHandler []authorizer.Authorizer`中。
```go
// NewAuthorizerFromAuthorizationConfig returns the right sort of union of multiple authorizer.Authorizer objects
// based on the authorizationMode or an error.  authorizationMode should be a comma separated values
// of options.AuthorizationModeChoices.
func NewAuthorizerFromAuthorizationConfig(authorizationModes []string, config AuthorizationConfig) (authorizer.Authorizer, error) {

	if len(authorizationModes) == 0 {
		return nil, errors.New("At least one authorization mode should be passed")
	}

	var authorizers []authorizer.Authorizer
	authorizerMap := make(map[string]bool)

	for _, authorizationMode := range authorizationModes {
		if authorizerMap[authorizationMode] {
			return nil, fmt.Errorf("Authorization mode %s specified more than once", authorizationMode)
		}
		// Keep cases in sync with constant list above.
		switch authorizationMode {
		case options.ModeAlwaysAllow:
			authorizers = append(authorizers, NewAlwaysAllowAuthorizer())
		case options.ModeAlwaysDeny:
			authorizers = append(authorizers, NewAlwaysDenyAuthorizer())
		case options.ModeABAC:
			if config.PolicyFile == "" {
				return nil, errors.New("ABAC's authorization policy file not passed")
			}
			abacAuthorizer, err := abac.NewFromFile(config.PolicyFile)
			if err != nil {
				return nil, err
			}
			authorizers = append(authorizers, abacAuthorizer)
		case options.ModeWebhook:
			if config.WebhookConfigFile == "" {
				return nil, errors.New("Webhook's configuration file not passed")
			}
			webhookAuthorizer, err := webhook.New(config.WebhookConfigFile,
				config.WebhookCacheAuthorizedTTL,
				config.WebhookCacheUnauthorizedTTL)
			if err != nil {
				return nil, err
			}
			authorizers = append(authorizers, webhookAuthorizer)
		case options.ModeRBAC:
			rbacAuthorizer := rbac.New(
				config.InformerFactory.Roles().Lister(),
				config.InformerFactory.RoleBindings().Lister(),
				config.InformerFactory.ClusterRoles().Lister(),
				config.InformerFactory.ClusterRoleBindings().Lister(),
				config.RBACSuperUser,
			)
			authorizers = append(authorizers, rbacAuthorizer)
		default:
			return nil, fmt.Errorf("Unknown authorization mode %s specified", authorizationMode)
		}
		authorizerMap[authorizationMode] = true
	}

	if !authorizerMap[options.ModeABAC] && config.PolicyFile != "" {
		return nil, errors.New("Cannot specify --authorization-policy-file without mode ABAC")
	}
	if !authorizerMap[options.ModeWebhook] && config.WebhookConfigFile != "" {
		return nil, errors.New("Cannot specify --authorization-webhook-config-file without mode Webhook")
	}
	if !authorizerMap[options.ModeRBAC] && config.RBACSuperUser != "" {
		return nil, errors.New("Cannot specify --authorization-rbac-super-user without mode RBAC")
	}

	/*
		认证和授权都有一个union,注意区分
		==>/pkg/auth/authorizer/union/union.go
			==>func New
	*/
	return union.New(authorizers...), nil
}
```


## type unionAuthzHandler
认证和授权各自都有一个union handler, 注意区分。 见/pkg/auth/authorizer/union/union.go
```go
// unionAuthzHandler authorizer against a chain of authorizer.Authorizer
type unionAuthzHandler []authorizer.Authorizer

// New returns an authorizer that authorizes against a chain of authorizer.Authorizer objects
func New(authorizationHandlers ...authorizer.Authorizer) authorizer.Authorizer {
	return unionAuthzHandler(authorizationHandlers)
}

// Authorizes against a chain of authorizer.Authorizer objects and returns nil if successful and returns error if unsuccessful
func (authzHandler unionAuthzHandler) Authorize(a authorizer.Attributes) (bool, string, error) {
	var (
		errlist    []error
		reasonlist []string
	)

	/*
		调用各个具体authorizers的Authorize()进行授权认证
	*/
	for _, currAuthzHandler := range authzHandler {
		authorized, reason, err := currAuthzHandler.Authorize(a)

		if err != nil {
			errlist = append(errlist, err)
		}
		if len(reason) != 0 {
			reasonlist = append(reasonlist, reason)
		}
		if !authorized {
			continue
		}
		//授权通过
		return true, reason, nil
	}

	return false, strings.Join(reasonlist, "\n"), utilerrors.NewAggregate(errlist)
}
```


## MODEL ABAC
Attribute-based access control，基于属性的访问控制，支持user attributes, resource attributes, object, environment attributes 等属性设置。 

### ABAC Policy File Format
使用ABAC授权需要API Server配置`--authorization-policy-file=SOME_FILENAME`。 
ABAC规则需要保存在文件中, 每一行都是一个 `json` 对象。 
There should be no enclosing list or map, just one map per line. 
Each line is a “policy object”. 
A policy object is a map with the following properties:
  * 版本控制属性
    * `apiVersion`, type string; 一般设为“abac.authorization.kubernetes.io/v1beta1”
	* `kind`, type string: 一般设为 “Policy”
	
  * `spec`属性，是一个map
    * `user`, type string。来源于`--token-auth-file`，如果自行指定一个user，需要和前面认证流程的username匹配。
    * `group`, type string; if you specify group, it must match one of the groups of the authenticated user. `system:authenticated` matches all authenticated requests.  `system:unauthenticated` matches all unauthenticated requests.
	
  * `Resource-matching`属性
    * apiGroup，例子extensions
    * namespace，例子kube-system
    * resource，例子pods
	
  * `Non-resource-matching`属性
    * nonResourcePath，例子/version or /apis
	
  * `readonly`，type boolean。 如果设置为true，`Resource-matching`部分仅支持get, list, and watch操作；而`Non-resource-matching`部分则仅支持get操作

下面是一个完成的示例：
```json
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"*",         "nonResourcePath": "*", "readonly": true}}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"admin",     "namespace": "*",              "resource": "*",         "apiGroup": "*"                   }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"scheduler", "namespace": "*",              "resource": "pods",                       "readonly": true }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"scheduler", "namespace": "*",              "resource": "bindings"                                     }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"kubelet",   "namespace": "*",              "resource": "pods",                       "readonly": true }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"kubelet",   "namespace": "*",              "resource": "services",                   "readonly": true }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"kubelet",   "namespace": "*",              "resource": "endpoints",                  "readonly": true }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"kubelet",   "namespace": "*",              "resource": "events"                                       }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"alice",     "namespace": "projectCaribou", "resource": "*",         "apiGroup": "*"                   }}
{"apiVersion": "abac.authorization.kubernetes.io/v1beta1", "kind": "Policy", "spec": {"user":"bob",       "namespace": "projectCaribou", "resource": "*",         "apiGroup": "*", "readonly": true }}
```

### func Authorize()
func Authorize()会调用matches()去使用policyList中每一条policy去匹配请求。 
只要匹配到一条规则符合，直接通过。 
见/pkg/auth/authorizer/abac/abac.go
```go
// Authorizer implements authorizer.Authorize

func (pl policyList) Authorize(a authorizer.Attributes) (bool, string, error) {
	for _, p := range pl {
		if matches(*p, a) {
			return true, "", nil
		}
	}
	return false, "No policy matched.", nil
	// TODO: Benchmark how much time policy matching takes with a medium size
	// policy file, compared to other steps such as encoding/decoding.
	// Then, add Caching only if needed.
}

func matches(p api.Policy, a authorizer.Attributes) bool {
	if subjectMatches(p, a) {
		if verbMatches(p, a) {
			// Resource and non-resource requests are mutually exclusive, at most one will match a policy
			if resourceMatches(p, a) {
				return true
			}
			if nonResourceMatches(p, a) {
				return true
			}
		}
	}
	return false
}
```

### 读取policy文件构建plicylist
policyList 的形成来源于func NewFromFile(path string)，读取policy文件构建plicylist。
```go
/*
	Policy定义在/pkg/apis/abac/types.go中
*/
type policyList []*api.Policy

// TODO: Have policies be created via an API call and stored in REST storage.
/*
	读取policy文件构建plicylist
*/
func NewFromFile(path string) (policyList, error) {
	// File format is one map per line.  This allows easy concatentation of files,
	// comments in files, and identification of errors by line number.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	/*
		新建scanner，Go标准package bufio
	*/
	scanner := bufio.NewScanner(file)
	pl := make(policyList, 0)

	decoder := api.Codecs.UniversalDecoder()

	i := 0
	unversionedLines := 0
	//按行读取
	for scanner.Scan() {
		i++
		p := &api.Policy{}
		//读取行
		b := scanner.Bytes()

		// skip comment lines and blank lines
		trimmed := strings.TrimSpace(string(b))
		if len(trimmed) == 0 || strings.HasPrefix(trimmed, "#") {
			continue
		}

		decodedObj, _, err := decoder.Decode(b, nil, nil)
		if err != nil {
			if !(runtime.IsMissingVersion(err) || runtime.IsMissingKind(err) || runtime.IsNotRegisteredError(err)) {
				return nil, policyLoadError{path, i, b, err}
			}
			unversionedLines++
			// Migrate unversioned policy object
			oldPolicy := &v0.Policy{}
			if err := runtime.DecodeInto(decoder, b, oldPolicy); err != nil {
				return nil, policyLoadError{path, i, b, err}
			}
			if err := api.Scheme.Convert(oldPolicy, p, nil); err != nil {
				return nil, policyLoadError{path, i, b, err}
			}
			pl = append(pl, p)
			continue
		}

		decodedPolicy, ok := decodedObj.(*api.Policy)
		if !ok {
			return nil, policyLoadError{path, i, b, fmt.Errorf("unrecognized object: %#v", decodedObj)}
		}
		pl = append(pl, decodedPolicy)
	}

	if unversionedLines > 0 {
		glog.Warningf("Policy file %s contained unversioned rules. See docs/admin/authorization.md#abac-mode for ABAC file format details.", path)
	}

	if err := scanner.Err(); err != nil {
		return nil, policyLoadError{path, -1, nil, err}
	}
	return pl, nil
}
```


## MODEL RBAC
Role-Based Access 基于角色的访问控制机制，集群管理员可以对用户或服务账号的角色进行更精确的资源访问控制。 
在RBAC中，权限与角色相关联，用户通过成为适当角色的成员而得到这些角色的权限。 
这就极大地简化了权限的管理。 
在一个组织中，角色是为了完成各种工作而创造，用户则依据它的责任和资格来被指派相应的角色，用户可以很容易地从一个角色被指派到另一个角色。 
**使用RBAC可以很方便的更新访问授权策略而不用重启集群。** 
RBAC在1.6版本升到到beta版本，从Kubernetes 1.8开始，RBAC进入稳定版。

见/plugin/pkg/auth/authorizer/rbac/rbac.go 。

```go
type RBACAuthorizer struct {
	superUser string

	authorizationRuleResolver RequestToRuleMapper
}

func New(roles validation.RoleGetter, roleBindings validation.RoleBindingLister, clusterRoles validation.ClusterRoleGetter, clusterRoleBindings validation.ClusterRoleBindingLister, superUser string) *RBACAuthorizer {
	authorizer := &RBACAuthorizer{
		superUser: superUser,
		authorizationRuleResolver: validation.NewDefaultRuleResolver(
			roles, roleBindings, clusterRoles, clusterRoleBindings,
		),
	}
	return authorizer
}
```

查看其Authorize()函数
```go
func (r *RBACAuthorizer) Authorize(requestAttributes authorizer.Attributes) (bool, string, error) {
	/*
		如果是使用superUser来访问，则授权直接通过；
		否则使用RulesFor()获取用户的规则，再调用RulesAllow()来授权。
	*/
	if r.superUser != "" && requestAttributes.GetUser() != nil && requestAttributes.GetUser().GetName() == r.superUser {
		return true, "", nil
	}

	/*
		RulesFor()通过Bindings机制来获用户取对应的角色及角色对应的规则
		然后调用RulesAllow()，使用每一个规则去匹配请求
	*/
	rules, ruleResolutionError := r.authorizationRuleResolver.RulesFor(requestAttributes.GetUser(), requestAttributes.GetNamespace())
	if RulesAllow(requestAttributes, rules...) {
		return true, "", nil
	}

	return false, "", ruleResolutionError
}
```

### RBAC使用例子
* Role

Role（角色）是一系列权限的集合，例如一个角色可以包含读取 Pod 的权限和列出 Pod 的权限。 
Role只能用来给某个特定namespace中的资源作鉴权，对多namespace和集群级的资源或者是非资源类的API（如/healthz）使用ClusterRole。

```yaml
# Role示例
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  namespace: default
  name: pod-reader
rules:
- apiGroups: [""] # "" indicates the core API group
  resources: ["pods"]
  verbs: ["get", "watch", "list"]
```

* RoleBinding

RoleBinding 把角色（Role或ClusterRole）的权限映射到用户或者用户组，从而让这些用户继承角色在 namespace 中的权限。 
```yaml
# RoleBinding示例（引用Role）
# This role binding allows "jane" to read pods in the "default" namespace.
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: read-pods
  namespace: default
subjects:
- kind: User
  name: jane
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
```

## RBAC vs ABAC
目前kubernetes中已经有一系列鉴权机制。鉴权的作用是，决定一个用户是否有权使用 Kubernetes API 做某些事情。它除了会影响 kubectl 等组件之外，还会对一些运行在集群内部并对集群进行操作的软件产生作用，例如使用了 Kubernetes 插件的 Jenkins，或者是利用 Kubernetes API 进行软件部署的 Helm。ABAC 和 RBAC 都能够对访问策略进行配置。

ABAC（Attribute Based Access Control）本来是不错的概念，但是在 Kubernetes 中的实现比较难于管理和理解，而且需要对 Master 所在节点的 SSH 和文件系统权限，而且要使得对授权的变更成功生效，还`需要重新启动` API Server。

而 RBAC 的授权策略可以利用 kubectl 或者 Kubernetes API 直接进行配置。**RBAC 可以授权给用户，让用户有权进行授权管理，这样就可以无需接触节点，直接进行授权管理。RBAC 在 Kubernetes 中被映射为 API 资源和操作。

## 参考
[authorization overview](https://kubernetes.io/docs/admin/authorization/)

[MODEL ABAC](https://kubernetes.io/docs/admin/authorization/abac/)

[MODEL RBAC](https://kubernetes.io/docs/admin/authorization/rbac/)

部分文字COPY自[RBAC](https://github.com/feiskyer/kubernetes-handbook/blob/master/plugins/rbac.md)
