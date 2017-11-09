# Authenticator机制

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [Authentication strategies](#authentication-strategies)
  - [一个request请求经历认证的流程](#一个request请求经历认证的流程)
  - [初始化加载Authentication Plugin](#初始化加载authentication-plugin)
  - [type KeystoneAuthenticator struct](#type-keystoneauthenticator-struct)
  - [type Authenticator struct](#type-authenticator-struct)
  - [type unionAuthRequestHandler struct](#type-unionauthrequesthandler-struct)
  - [type GroupAdder struct](#type-groupadder-struct)
  - [总结](#总结)

## Authentication strategies
Authenticator步骤的输入是整个HTTP请求，但是，它通常只是检查HTTP Headers and/or client certificate。

可以指定多个Authenticator模块，在这种情况下，每个认证模块都按顺序尝试，直到其中一个成功即可。

如果认证成功，则用户的`username`会传入授权模块做进一步授权验证；而对于认证失败的请求则返回HTTP 401。

虽然Kubernetes在访问控制决策和请求记录中使用`username`，但它没有用户对象，也不在用户对象存储中存储有关用户的username或其他信息。

Kubernetes使用client certificates, bearer tokens, an authenticating proxy, or HTTP basic auth, 通过身份验证插件对API请求进行身份验证。 当向API服务器发出一个HTTP请求，Authentication plugin会尝试将以下属性与请求关联：
- Username: 标识终端用户的字符串, 常用值可能是kube-admin或jane@example.com。
- UID: 标识终端用户的字符串,比Username更具有唯一性。
- Groups: a set of strings which associate users with a set of commonly grouped users.
- Extra fields: 可能有用的额外信息

系统中把这4个属性封装成一个type DefaultInfo struct ，见/pkg/auth/user/user.go。

The `system:authenticated` group 被包含在经过认证的用户Group列表中。

## 一个request请求经历认证的流程
我们首先来看看一个request请求经历认证的流程
1. /pkg/genericapiserver/filters/timeout.go中 func (t *timeoutHandler) ServeHTTP
```go
go func() {
		t.handler.ServeHTTP(tw, r)
		close(done)
	}()
```
2. /pkg/api/requestcontext.go中 func WithRequestContext
3. /pkg/apiserver/filters/requestinfo.go中 func WithRequestInfo
4. /pkg/genericapiserver/filters/panics.go中 func WithPanicRecovery
5. /pkg/api/requestcontext.go中 func WithRequestContext

***

6. /pkg/auth/handlers/handlers.go中 func WithAuthentication
```go
user, ok, err := auth.AuthenticateRequest(req)
```
7. /plugin/pkg/auth/authenticator/request/union/union.go 中 func (authHandler *unionAuthRequestHandler) AuthenticateRequest
8. /pkg/auth/group/group_adder.go中 func (g *GroupAdder) AuthenticateRequest
9. 再到具体的Authenticator的自身的认证函数，如keystone的func AuthenticatePassword()

## 初始化加载Authentication Plugin
见/cmd/kube-apiserver/app/server.go的Run()
```go
	/*
		安全认证相关，Authenticator机制
			==>/pkg/apiserver/authenticator/authn.go
				==>func New
	*/
	apiAuthenticator, securityDefinitions, err := authenticator.New(authenticator.AuthenticatorConfig{
		Anonymous: s.GenericServerRunOptions.AnonymousAuth,
		AnyToken:  s.GenericServerRunOptions.EnableAnyToken,
		/*
			BasicAuthFile,指定basicauthfile文件所在的位置，
			当这个参数不为空的时候,会开启basicauth的认证方式，这是一个.csv文件，
			三列分别是password，username,useruid
		*/
		BasicAuthFile: s.GenericServerRunOptions.BasicAuthFile,
		/*
			ClientCAFile,用于给客户端签名的根证书，
			当这个参数不为空的时候,会开启https的认证方式，
			会通过这个根证书对客户端的证书进行身份认证
		*/
		ClientCAFile: s.GenericServerRunOptions.ClientCAFile,
		/*
			TokenAuthFile,用于Token文件所在的位置，
			当这个参数不为空的时候，会采用token的认证方式，
			token文件也是csv的格式，分别是“token,username,userid”
		*/
		TokenAuthFile:     s.GenericServerRunOptions.TokenAuthFile,
		OIDCIssuerURL:     s.GenericServerRunOptions.OIDCIssuerURL,
		OIDCClientID:      s.GenericServerRunOptions.OIDCClientID,
		OIDCCAFile:        s.GenericServerRunOptions.OIDCCAFile,
		OIDCUsernameClaim: s.GenericServerRunOptions.OIDCUsernameClaim,
		OIDCGroupsClaim:   s.GenericServerRunOptions.OIDCGroupsClaim,
		/*
			ServiceAccountKeyFiles,
			当不为空的时候，采用ServiceAccount的认证方式，这其实是一个公钥方式。
			发过来的信息是客户端使用对应的私钥加密，服务端使用指定的公钥来解密信息
		*/
		ServiceAccountKeyFiles: s.ServiceAccountKeyFiles,
		/*
			ServiceAccountLookup,默认为false。
			如果为true的话，就会从etcd中取出对应的ServiceAccount与
			传过来的信息进行对比验证，反之不会
		*/
		ServiceAccountLookup:        s.ServiceAccountLookup,
		ServiceAccountTokenGetter:   serviceAccountGetter,
		KeystoneURL:                 s.GenericServerRunOptions.KeystoneURL,
		KeystoneCAFile:              s.GenericServerRunOptions.KeystoneCAFile,
		WebhookTokenAuthnConfigFile: s.WebhookTokenAuthnConfigFile,
		WebhookTokenAuthnCacheTTL:   s.WebhookTokenAuthnCacheTTL,
		RequestHeaderConfig:         s.GenericServerRunOptions.AuthenticationRequestHeaderConfig(),
	})
```

### 创建apiAuthenticator
生成一个authenticator.Request用于支持kubernetes系统的authentication机制。 

分析其流程如下：
1. 生成各个具体的Authenticator，在这里会进一步把具体的Authenticator进一步封装，以实现一些公共接口函数。 比如keystone Authenticator的AuthenticateRequest()就是通过type Authenticator struct来得到的，并不是直接定义的。
2. 进行一次封装成type unionAuthRequestHandler struct对象。
3. 进一步封装成type GroupAdder struct对象。

```go
// New returns an authenticator.Request or an error that supports the standard
// Kubernetes authentication mechanisms.

func New(config AuthenticatorConfig) (authenticator.Request, *spec.SecurityDefinitions, error) {
	var authenticators []authenticator.Request
	securityDefinitions := spec.SecurityDefinitions{}
	hasBasicAuth := false
	hasTokenAuth := false

	// front-proxy, BasicAuth methods, local first, then remote
	// Add the front proxy authenticator if requested
	if config.RequestHeaderConfig != nil {
		requestHeaderAuthenticator, err := headerrequest.NewSecure(
			config.RequestHeaderConfig.ClientCA,
			config.RequestHeaderConfig.AllowedClientNames,
			config.RequestHeaderConfig.UsernameHeaders,
		)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, requestHeaderAuthenticator)
	}

	if len(config.BasicAuthFile) > 0 {
		basicAuth, err := newAuthenticatorFromBasicAuthFile(config.BasicAuthFile)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, basicAuth)
		hasBasicAuth = true
	}
	if len(config.KeystoneURL) > 0 {
		keystoneAuth, err := newAuthenticatorFromKeystoneURL(config.KeystoneURL, config.KeystoneCAFile)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, keystoneAuth)
		hasBasicAuth = true
	}

	// X509 methods
	if len(config.ClientCAFile) > 0 {
		certAuth, err := newAuthenticatorFromClientCAFile(config.ClientCAFile)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, certAuth)
	}

	// Bearer token methods, local first, then remote
	if len(config.TokenAuthFile) > 0 {
		tokenAuth, err := newAuthenticatorFromTokenFile(config.TokenAuthFile)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, tokenAuth)
		hasTokenAuth = true
	}
	if len(config.ServiceAccountKeyFiles) > 0 {
		serviceAccountAuth, err := newServiceAccountAuthenticator(config.ServiceAccountKeyFiles, config.ServiceAccountLookup, config.ServiceAccountTokenGetter)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, serviceAccountAuth)
		hasTokenAuth = true
	}
	// NOTE(ericchiang): Keep the OpenID Connect after Service Accounts.
	//
	// Because both plugins verify JWTs whichever comes first in the union experiences
	// cache misses for all requests using the other. While the service account plugin
	// simply returns an error, the OpenID Connect plugin may query the provider to
	// update the keys, causing performance hits.
	if len(config.OIDCIssuerURL) > 0 && len(config.OIDCClientID) > 0 {
		oidcAuth, err := newAuthenticatorFromOIDCIssuerURL(config.OIDCIssuerURL, config.OIDCClientID, config.OIDCCAFile, config.OIDCUsernameClaim, config.OIDCGroupsClaim)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, oidcAuth)
		hasTokenAuth = true
	}
	if len(config.WebhookTokenAuthnConfigFile) > 0 {
		webhookTokenAuth, err := newWebhookTokenAuthenticator(config.WebhookTokenAuthnConfigFile, config.WebhookTokenAuthnCacheTTL)
		if err != nil {
			return nil, nil, err
		}
		authenticators = append(authenticators, webhookTokenAuth)
		hasTokenAuth = true
	}

	// always add anytoken last, so that every other token authenticator gets to try first
	if config.AnyToken {
		authenticators = append(authenticators, bearertoken.New(anytoken.AnyTokenAuthenticator{}))
		hasTokenAuth = true
	}

	if hasBasicAuth {
		securityDefinitions["HTTPBasic"] = &spec.SecurityScheme{
			SecuritySchemeProps: spec.SecuritySchemeProps{
				Type:        "basic",
				Description: "HTTP Basic authentication",
			},
		}
	}

	if hasTokenAuth {
		securityDefinitions["BearerToken"] = &spec.SecurityScheme{
			SecuritySchemeProps: spec.SecuritySchemeProps{
				Type:        "apiKey",
				Name:        "authorization",
				In:          "header",
				Description: "Bearer Token authentication",
			},
		}
	}

	if len(authenticators) == 0 {
		if config.Anonymous {
			return anonymous.NewAuthenticator(), &securityDefinitions, nil
		}
	}

	switch len(authenticators) {
	case 0:
		return nil, &securityDefinitions, nil
	}

	/*
		进行一次封装成type unionAuthRequestHandler struct对象
			=>/plugin/pkg/auth/authenticator/request/union/union.go
	*/
	authenticator := union.New(authenticators...)

	/*
		进一步封装成type GroupAdder struct对象
		/pkg/auth/group/group_adder.go
			==>func NewGroupAdder
		user.AllAuthenticated的值是"system:authenticated"
	*/
	authenticator = group.NewGroupAdder(authenticator, []string{user.AllAuthenticated})

	if config.Anonymous {
		// If the authenticator chain returns an error, return an error (don't consider a bad bearer token anonymous).
		authenticator = union.NewFailOnError(authenticator, anonymous.NewAuthenticator())
	}

	return authenticator, &securityDefinitions, nil
}
```

## type KeystoneAuthenticator struct
来看看具体的Authenticator是怎么生成的，见/plugin/pkg/auth/authenticator/password/keystone/keystone.go
```go
// KeystoneAuthenticator contacts openstack keystone to validate user's credentials passed in the request.
// The keystone endpoint is passed during apiserver startup
type KeystoneAuthenticator struct {
	authURL   string
	transport http.RoundTripper
}

// NewKeystoneAuthenticator returns a password authenticator that validates credentials using openstack keystone
func NewKeystoneAuthenticator(authURL string, caFile string) (*KeystoneAuthenticator, error) {
	if !strings.HasPrefix(authURL, "https") {
		return nil, errors.New("Auth URL should be secure and start with https")
	}
	if authURL == "" {
		return nil, errors.New("Auth URL is empty")
	}
	if caFile != "" {
		roots, err := certutil.NewPool(caFile)
		if err != nil {
			return nil, err
		}
		config := &tls.Config{}
		config.RootCAs = roots
		transport := netutil.SetOldTransportDefaults(&http.Transport{TLSClientConfig: config})
		return &KeystoneAuthenticator{authURL, transport}, nil
	}

	return &KeystoneAuthenticator{authURL: authURL}, nil
}
```
查看其认证函数AuthenticatePassword()，原理是生成openstack client，然后去认证用户名和密码，最后返回的是user。
```go
// AuthenticatePassword checks the username, password via keystone call
func (keystoneAuthenticator *KeystoneAuthenticator) AuthenticatePassword(username string, password string) (user.Info, bool, error) {
	opts := gophercloud.AuthOptions{
		IdentityEndpoint: keystoneAuthenticator.authURL,
		Username:         username,
		Password:         password,
	}

	_, err := keystoneAuthenticator.AuthenticatedClient(opts)
	if err != nil {
		glog.Info("Failed: Starting openstack authenticate client:" + err.Error())
		return nil, false, errors.New("Failed to authenticate")
	}

	return &user.DefaultInfo{Name: username}, true, nil
}

// AuthenticatedClient logs in to an OpenStack cloud found at the identity endpoint specified by options, acquires a
// token, and returns a Client instance that's ready to operate.
func (keystoneAuthenticator *KeystoneAuthenticator) AuthenticatedClient(options gophercloud.AuthOptions) (*gophercloud.ProviderClient, error) {
	client, err := openstack.NewClient(options.IdentityEndpoint)
	if err != nil {
		return nil, err
	}

	if keystoneAuthenticator.transport != nil {
		client.HTTPClient.Transport = keystoneAuthenticator.transport
	}

	err = openstack.Authenticate(client, options)
	return client, err
}
```

## type Authenticator struct
给password类型的Authenticator提供统一的AuthenticateRequest()入口，供type GroupAdder struct调用。  见/plugin/pkg/auth/authenticator/request/basicauth/basicauth.go。
```go
// Authenticator authenticates requests using basic auth
type Authenticator struct {
	auth authenticator.Password
}

// New returns a request authenticator that validates credentials using the provided password authenticator
func New(auth authenticator.Password) *Authenticator {
	return &Authenticator{auth}
}

// AuthenticateRequest authenticates the request using the "Authorization: Basic" header in the request
func (a *Authenticator) AuthenticateRequest(req *http.Request) (user.Info, bool, error) {
	username, password, found := req.BasicAuth()
	if !found {
		return nil, false, nil
	}
	return a.auth.AuthenticatePassword(username, password)
}
```

## type unionAuthRequestHandler struct
接受到一个Request之后，负责遍历所有的Authenticator，进行认证，只要有其中一个认证通过，直接返回认证成功。 见/plugin/pkg/auth/authenticator/request/union/union.go。
```go
// unionAuthRequestHandler authenticates requests using a chain of authenticator.Requests
/*
	unionAuthRequestHandler使用一个authenticator.Requests链来验证一个request
*/
type unionAuthRequestHandler struct {
	// Handlers is a chain of request authenticators to delegate to
	Handlers []authenticator.Request
	// FailOnError determines whether an error returns short-circuits the chain
	FailOnError bool
}

// New returns a request authenticator that validates credentials using a chain of authenticator.Request objects.
// The entire chain is tried until one succeeds. If all fail, an aggregate error is returned.
/*
	简单封装成一个type unionAuthRequestHandler struct 对象
*/
func New(authRequestHandlers ...authenticator.Request) authenticator.Request {
	if len(authRequestHandlers) == 1 {
		return authRequestHandlers[0]
	}
	return &unionAuthRequestHandler{Handlers: authRequestHandlers, FailOnError: false}
}
```
### 认证函数AuthenticateRequest
负责遍历，然后通过type GroupAdder struct调用具体的Authenticator进行认证。
```go
// AuthenticateRequest authenticates the request using a chain of authenticator.Request objects.
func (authHandler *unionAuthRequestHandler) AuthenticateRequest(req *http.Request) (user.Info, bool, error) {
	var errlist []error
	for _, currAuthRequestHandler := range authHandler.Handlers {
		/*
			==>/pkg/auth/group/group_adder.go
				==>func (g *GroupAdder) AuthenticateRequest
		*/
		info, ok, err := currAuthRequestHandler.AuthenticateRequest(req)
		if err != nil {
			if authHandler.FailOnError {
				return info, ok, err
			}
			errlist = append(errlist, err)
			continue
		}

		if ok {
			return info, ok, err
		}
	}

	return nil, false, utilerrors.NewAggregate(errlist)
}
```

## type GroupAdder struct
调用各个具体Authenticator的AuthenticateRequest()，对请求进行进行认证，得到user信息之后返回
```go
// GroupAdder adds groups to an authenticated user.Info
type GroupAdder struct {
	// Authenticator is delegated to make the authentication decision
	Authenticator authenticator.Request
	// Groups are additional groups to add to the user.Info from a successful authentication
	Groups []string
}
```
### 认证函数AuthenticateRequest
```go
func (g *GroupAdder) AuthenticateRequest(req *http.Request) (user.Info, bool, error) {
	/*
		调用各个具体Authenticator的AuthenticateRequest()
		对请求进行进行认证，得到user信息之后返回

		keystone Authenticator的AuthenticateRequest()是通过type Authenticator struct来得到的，并不是直接定义
			==>/plugin/pkg/auth/authenticator/request/basicauth/basicauth.go
				==>type Authenticator struct
					==>func (a *Authenticator) AuthenticateRequest
	*/
	u, ok, err := g.Authenticator.AuthenticateRequest(req)
	if err != nil || !ok {
		return nil, ok, err
	}
	/*
		Info 描述一个已经经过Authenticator组件认证的user信息
	*/
	return &user.DefaultInfo{
		Name:   u.GetName(),
		UID:    u.GetUID(),
		Groups: append(u.GetGroups(), g.Groups...),
		Extra:  u.GetExtra(),
	}, true, nil
}
```
### Groups的类别
见/pkg/auth/user/user.go
```go
// well-known user and group names
const (
	SystemPrivilegedGroup = "system:masters"
	NodesGroup            = "system:nodes"
	AllUnauthenticated    = "system:unauthenticated"
	AllAuthenticated      = "system:authenticated"

	Anonymous     = "system:anonymous"
	APIServerUser = "system:apiserver"
)
```

## 总结
1. type Authenticator struct、type unionAuthRequestHandler struct、type GroupAdder struct都有一个AuthenticateRequest()，注意调用关系。

2. type Authenticator struct负责给password类型的Authenticator(如KeystoneAuthenticator)提供统一的AuthenticateRequest()入口，供type GroupAdder struct调用。

3. type unionAuthRequestHandler struct 接受到一个Request之后，负责遍历所有的Authenticator，进行认证，只要有其中一个认证通过，直接返回认证成功。 通过调用type GroupAdder struct来完成。

4. type GroupAdder struct 调用各个具体Authenticator的AuthenticateRequest()，对请求进行进行认证，得到user信息之后返回

