# 一个Request的流程之Filters

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [引言](#引言)
  - [DefaultBuildHandlerChain](#defaultbuildhandlerchain)
  - [WithAuthentication](#withauthentication)
  - [参考](#参考)

<!-- END MUNGE: GENERATED_TOC -->

## 引言
在[deep dive]()系列中提到过：当HTTP请求命中Kubernetes API时，HTTP请求首先由在`DefaultBuildHandlerChain（）`中注册的过滤器链进行处理。 
该过滤器对其执行一系列过滤操作。 
过滤器通过并附加相应信息到`ctx.RequestInfo`，例如经过身份验证的user或返回适当的HTTP响应代码。

![API-server-flow](https://github.com/Kevin-fqh/learning-k8s-source-code/blob/master/images/API-server-flow.png)

## DefaultBuildHandlerChain
kube-apiserver使用go－restful框架来建立Web服务，分为http和https两个handler。见/pkg/genericapiserver/config.go的func (c completedConfig) New() 

```go
	s.HandlerContainer = mux.NewAPIContainer(http.NewServeMux(), c.Serializer)
	/*
		上面Container已创建并且也进行了初始化。该轮到WebService了
	*/
	s.installAPI(c.Config)

	s.Handler, s.InsecureHandler = c.BuildHandlerChainsFunc(s.HandlerContainer.ServeMux, c.Config)
```
关于Handler和InsecureHandler的使用可以参考[Restful API注册]()系列文章。

而BuildHandlerChainsFunc的声明如下：
```go
func DefaultBuildHandlerChain(apiHandler http.Handler, c *Config) (secure, insecure http.Handler) {
	attributeGetter := apiserverfilters.NewRequestAttributeGetter(c.RequestContextMapper)

	generic := func(handler http.Handler) http.Handler {
		handler = genericfilters.WithCORS(handler, c.CorsAllowedOriginList, nil, nil, nil, "true")
		handler = genericfilters.WithPanicRecovery(handler, c.RequestContextMapper)
		handler = apiserverfilters.WithRequestInfo(handler, NewRequestInfoResolver(c), c.RequestContextMapper)
		handler = api.WithRequestContext(handler, c.RequestContextMapper)
		handler = genericfilters.WithTimeoutForNonLongRunningRequests(handler, c.LongRunningFunc)
		handler = genericfilters.WithMaxInFlightLimit(handler, c.MaxRequestsInFlight, c.LongRunningFunc)
		return handler
	}
	audit := func(handler http.Handler) http.Handler {
		return apiserverfilters.WithAudit(handler, attributeGetter, c.AuditWriter)
	}
	protect := func(handler http.Handler) http.Handler {
		/*
			封装了Authorization、Authentication的handler
		*/
		handler = apiserverfilters.WithAuthorization(handler, attributeGetter, c.Authorizer)
		handler = apiserverfilters.WithImpersonation(handler, c.RequestContextMapper, c.Authorizer)
		handler = audit(handler) // before impersonation to read original user
		handler = authhandlers.WithAuthentication(handler, c.RequestContextMapper, c.Authenticator, authhandlers.Unauthorized(c.SupportsBasicAuth))
		return handler
	}

	return generic(protect(apiHandler)), generic(audit(apiHandler))
}
```
以Authentication为例，看看其handler定义。

## WithAuthentication

WithAuthentication()会创建一个http handler，会对指定的request进行认证。 
认证的返回信息的一个user，WithAuthentication()会把这些user信息附加到该request的context上。 
- 如果身份认证失败或返回错误，则使用失败的handler来进行处理。 
- 如果成功，会从request的header中删除"Authorization"信息，并调用handler来为该request提供服务。 

见/pkg/auth/handlers/handlers.go

```go
// WithAuthentication creates an http handler that tries to authenticate the given request as a user, and then
// stores any such user found onto the provided context for the request. If authentication fails or returns an error
// the failed handler is used. On success, "Authorization" header is removed from the request and handler
// is invoked to serve the request.

func WithAuthentication(handler http.Handler, mapper api.RequestContextMapper, auth authenticator.Request, failed http.Handler) http.Handler {
	if auth == nil {
		glog.Warningf("Authentication is disabled")
		return handler
	}
	return api.WithRequestContext(
		http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			/*
				认证
				==>/plugin/pkg/auth/authenticator/request/union/union.go
					==>func (authHandler *unionAuthRequestHandler) AuthenticateRequest
			*/
			user, ok, err := auth.AuthenticateRequest(req)
			if err != nil || !ok {
				if err != nil {
					glog.Errorf("Unable to authenticate the request due to an error: %v", err)
				}
				//认证失败时的处理
				failed.ServeHTTP(w, req)
				return
			}

			// authorization header is not required anymore in case of a successful authentication.
			//如果前面认证成功了，不再需要authorization header信息
			req.Header.Del("Authorization")

			//把认证返回的user信息附加到该request的context上，供后续决策流程使用
			if ctx, ok := mapper.Get(req); ok {
				mapper.Update(req, api.WithUser(ctx, user))
			}

			authenticatedUserCounter.WithLabelValues(compressUsername(user.GetName())).Inc()

			handler.ServeHTTP(w, req)
		}),
		mapper,
	)
}
```

这里的`user, ok, err := auth.AuthenticateRequest(req)`调用的正是定义在`/plugin/pkg/auth/authenticator/request/union/union.go`中的AuthenticateRequest()。 

这就和`认证机制`一文中的流程对应上了，见[Authenticator机制]()。 

## 参考
[accessing the api](https://kubernetes.io/docs/admin/accessing-the-api/)

[authentication](https://kubernetes.io/docs/admin/authentication/)

[authorization](https://kubernetes.io/docs/admin/authorization/)

[admission-controllers](https://kubernetes.io/docs/admin/admission-controllers/)







