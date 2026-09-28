package router

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/route"
)

// KaiwuProxyHTTPClient 抽象代理使用的 HTTP 客户端，便于测试替换并保证
// 生产实现禁止自动跟随重定向。
type KaiwuProxyHTTPClient interface {
	// Do 执行一个出站 HTTP 请求，并返回未自动跟随重定向的响应。
	Do(request *http.Request) (*http.Response, error)
}

// kaiwuProxyAPI 保存开物代理所需的运行时读取器、Cookie 签名器和 HTTP 客户端。
type kaiwuProxyAPI struct {
	runtimeReader KaiwuRuntimeReader
	cookieSigner  KaiwuCookieSigner
	httpClient    KaiwuProxyHTTPClient
}

// registerKaiwuProxy 注册 Console 和 BOSS 的浏览器代理入口。即使依赖为 nil
// 也注册路由，以便测试和路由契约能发现入口缺失；处理函数随后失败关闭。
func registerKaiwuProxy(svc *route.RouterGroup, reader KaiwuRuntimeReader, cookieSigner KaiwuCookieSigner, httpClient KaiwuProxyHTTPClient) {
	api := kaiwuProxyAPI{
		runtimeReader: reader,
		cookieSigner:  cookieSigner,
		httpClient:    httpClient,
	}
	api.register(svc)
}

// register 将同一个代理处理函数挂到两个客户端的基础路径和通配子路径上。
func (api kaiwuProxyAPI) register(svc *route.RouterGroup) {
	for _, client := range []string{kaiwuClientConsole, kaiwuClientBoss} {
		base := kaiwuProxyBasePath(client)
		svc.Any(base, api.proxy)
		svc.Any(base+"/*path", api.proxy)
	}
}

// proxy 是所有 /kaiwu/console 与 /kaiwu/boss 请求的唯一入口。它先校验
// Gateway 签名 Cookie；首次进入时执行 DSH token 交换，后续请求再转发到
// 集群内 ClusterIP Service。
func (api kaiwuProxyAPI) proxy(ctx context.Context, c *app.RequestContext) {
	client, internalPath, ok := parseKaiwuProxyPath(string(c.Path()))
	if !ok {
		writeKaiwuError(c, http.StatusNotFound, "KAIWU_PROXY_NOT_FOUND", "Kaiwu proxy path not found")
		return
	}

	identity, bootstrapValid, sessionValid, err := api.authorize(c, client)
	if err != nil {
		if errors.Is(err, errKaiwuProxyForbidden) {
			writeKaiwuError(c, http.StatusForbidden, "KAIWU_PROXY_FORBIDDEN", "Kaiwu client mismatch")
			return
		}
		if errors.Is(err, errKaiwuProxyUnavailable) {
			return
		}
		writeKaiwuError(c, http.StatusUnauthorized, "KAIWU_PROXY_UNAUTHORIZED", "valid Kaiwu proxy cookie required")
		return
	}

	// Session Cookie 已存在且 DSH Cookie 也存在时才直接转发；否则重新执行
	// token 交换，确保只有 Gateway Cookie 不能直接打开 DSH 页面。
	if sessionValid && hasKaiwuDSHCookie(c, client) {
		api.forward(ctx, c, client, internalPath, identity)
		return
	}
	if bootstrapValid || sessionValid {
		api.exchangeBootstrap(ctx, c, client, identity)
		return
	}
	writeKaiwuError(c, http.StatusUnauthorized, "KAIWU_PROXY_UNAUTHORIZED", "valid Kaiwu proxy cookie required")
}

// authorize 校验当前路径对应的 bootstrap/session Cookie。若 Cookie 属于
// 另一个客户端，则返回 forbidden，避免 Console 与 BOSS 互用。
func (api kaiwuProxyAPI) authorize(c *app.RequestContext, client string) (KaiwuCookieIdentity, bool, bool, error) {
	if api.cookieSigner == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu cookie signer unavailable")
		return KaiwuCookieIdentity{}, false, false, errKaiwuProxyUnavailable
	}

	bootstrapValue := strings.TrimSpace(string(c.Cookie(kaiwuCookieName(KaiwuCookieTypeBootstrap, client))))
	sessionValue := strings.TrimSpace(string(c.Cookie(kaiwuCookieName(KaiwuCookieTypeSession, client))))
	otherClient := kaiwuClientBoss
	if client == kaiwuClientBoss {
		otherClient = kaiwuClientConsole
	}
	otherBootstrapValue := strings.TrimSpace(string(c.Cookie(kaiwuCookieName(KaiwuCookieTypeBootstrap, otherClient))))
	otherSessionValue := strings.TrimSpace(string(c.Cookie(kaiwuCookieName(KaiwuCookieTypeSession, otherClient))))

	var identity KaiwuCookieIdentity
	bootstrapValid := false
	sessionValid := false
	for _, rawCookie := range []string{bootstrapValue, sessionValue} {
		if rawCookie == "" {
			continue
		}
		if verified, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, client, rawCookie, time.Now()); err == nil {
			identity = verified
			bootstrapValid = true
		}
		if verified, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeSession, client, rawCookie, time.Now()); err == nil {
			identity = verified
			sessionValid = true
		}
		if _, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, otherClient, rawCookie, time.Now()); err == nil {
			return KaiwuCookieIdentity{}, false, false, errKaiwuProxyForbidden
		}
		if _, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeSession, otherClient, rawCookie, time.Now()); err == nil {
			return KaiwuCookieIdentity{}, false, false, errKaiwuProxyForbidden
		}
	}
	if !bootstrapValid && !sessionValid {
		for _, rawCookie := range []string{otherBootstrapValue, otherSessionValue} {
			if rawCookie == "" {
				continue
			}
			if _, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, otherClient, rawCookie, time.Now()); err == nil {
				return KaiwuCookieIdentity{}, false, false, errKaiwuProxyForbidden
			}
			if _, err := api.cookieSigner.VerifyKaiwuCookie(KaiwuCookieTypeSession, otherClient, rawCookie, time.Now()); err == nil {
				return KaiwuCookieIdentity{}, false, false, errKaiwuProxyForbidden
			}
		}
		return KaiwuCookieIdentity{}, false, false, errKaiwuProxyUnauthorized
	}
	return identity, bootstrapValid, sessionValid, nil
}

// exchangeBootstrap 实时读取 DSH webToken，向内部根路径发起 token 交换，
// 改写 DSH Cookie，签发 Gateway session Cookie，并跳回不带 token 的代理路径。
func (api kaiwuProxyAPI) exchangeBootstrap(ctx context.Context, c *app.RequestContext, client string, identity KaiwuCookieIdentity) {
	target, webToken, err := api.readRuntime(ctx, client)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	if api.httpClient == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu HTTP client unavailable")
		return
	}

	exchangeURL := *target
	exchangeURL.Path = "/"
	exchangeURL.RawQuery = "token=" + url.QueryEscape(webToken)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, exchangeURL.String(), nil)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	request.Host = kaiwuRequestAuthority(c)
	response, err := api.httpClient.Do(request)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 399 || len(response.Header.Values("Set-Cookie")) == 0 {
		_, _ = io.Copy(io.Discard, response.Body)
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu token exchange failed")
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)

	rewriteKaiwuSetCookies(c, response, client)
	sessionCookie, err := api.cookieSigner.IssueKaiwuSessionCookie(identity, time.Now(), isKaiwuSecureRequest(c))
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu cookie signer unavailable")
		return
	}
	setKaiwuCookie(c, sessionCookie)
	c.Redirect(http.StatusFound, []byte(kaiwuProxyBasePath(client)))
}

// forward 将当前外部代理路径转换为 DSH 内部路径，并转发请求体、查询参数、
// WebSocket Header 和带前缀的 DSH Cookie。
func (api kaiwuProxyAPI) forward(ctx context.Context, c *app.RequestContext, client, internalPath string, identity KaiwuCookieIdentity) {
	target, _, err := api.readRuntime(ctx, client)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	if api.httpClient == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu HTTP client unavailable")
		return
	}
	request, err := newKaiwuOutboundRequest(ctx, c, target, internalPath)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	addKaiwuForwardCookies(request, c, client)
	response, err := api.httpClient.Do(request)
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return
	}
	if response.StatusCode == http.StatusSwitchingProtocols {
		api.writeUpgradeResponse(c, response, client, target)
		return
	}
	writeKaiwuResponse(c, response, client, target)
}

// writeUpgradeResponse 将 DSH 的 101 响应返回给浏览器，并在连接劫持后
// 双向转发 WebSocket 字节流。
func (api kaiwuProxyAPI) writeUpgradeResponse(c *app.RequestContext, response *http.Response, client string, target *url.URL) {
	serverConnection, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		_ = response.Body.Close()
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu upgrade unavailable")
		return
	}
	copyKaiwuResponseHeaders(c, response, client, target, true)
	c.SetStatusCode(response.StatusCode)
	c.Hijack(func(clientConnection network.Conn) {
		copyKaiwuWebSocket(clientConnection, serverConnection)
	})
}

// writeKaiwuResponse 写入普通 HTTP、SSE 和静态资源响应。响应体使用流式
// 转发，不在 Gateway 内存中完整缓存。
func writeKaiwuResponse(c *app.RequestContext, response *http.Response, client string, target *url.URL) {
	copyKaiwuResponseHeaders(c, response, client, target, false)
	c.SetStatusCode(response.StatusCode)
	c.SetBodyStream(response.Body, -1)
}

// newKaiwuOutboundRequest 构造发往 DSH 的内部请求，并移除 ANI Authorization、
// Gateway Cookie 和不安全的代理 Header。
func newKaiwuOutboundRequest(ctx context.Context, c *app.RequestContext, target *url.URL, internalPath string) (*http.Request, error) {
	outboundURL := *target
	outboundURL.Path = internalPath
	outboundURL.RawQuery = string(c.Request.URI().QueryString())
	request, err := http.NewRequestWithContext(ctx, string(c.Method()), outboundURL.String(), c.RequestBodyStream())
	if err != nil {
		return nil, err
	}
	request.Host = kaiwuRequestAuthority(c)
	request.ContentLength = int64(c.Request.Header.ContentLength())
	if contentType := string(c.Request.Header.ContentType()); contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if userAgent := string(c.Request.Header.UserAgent()); userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}

	upgrade := isKaiwuUpgradeRequest(c)
	c.Request.Header.VisitAllCustomHeader(func(key, value []byte) {
		name := strings.ToLower(string(key))
		if isKaiwuRequestHeaderBlocked(name, upgrade) {
			return
		}
		request.Header.Add(string(key), string(value))
	})
	if upgrade {
		if connection := string(c.Request.Header.Peek("Connection")); connection != "" {
			request.Header.Set("Connection", connection)
		}
		if upgradeProtocol := string(c.Request.Header.Peek("Upgrade")); upgradeProtocol != "" {
			request.Header.Set("Upgrade", upgradeProtocol)
		}
	}
	request.Header.Set("X-Forwarded-Host", kaiwuRequestAuthority(c))
	request.Header.Set("X-Forwarded-Proto", kaiwuRequestProtocol(c))
	request.Header.Set("X-Forwarded-For", c.ClientIP())
	return request, nil
}

// addKaiwuForwardCookies 只把当前客户端前缀的 DSH Cookie 还原为原始名称；
// ANI Gateway 的 bootstrap/session Cookie 不会转发给 DSH。
func addKaiwuForwardCookies(request *http.Request, c *app.RequestContext, client string) {
	prefix := kaiwuDSHCookiePrefix(client)
	c.Request.Header.VisitAllCookie(func(key, value []byte) {
		name := string(key)
		if !strings.HasPrefix(name, prefix) {
			return
		}
		originalName := strings.TrimPrefix(name, prefix)
		if originalName == "" {
			return
		}
		request.AddCookie(&http.Cookie{Name: originalName, Value: string(value)})
	})
}

// hasKaiwuDSHCookie 判断浏览器是否已经携带当前客户端前缀的 DSH Cookie。
func hasKaiwuDSHCookie(c *app.RequestContext, client string) bool {
	prefix := kaiwuDSHCookiePrefix(client)
	found := false
	c.Request.Header.VisitAllCookie(func(key, value []byte) {
		if strings.HasPrefix(string(key), prefix) && len(value) > 0 {
			found = true
		}
	})
	return found
}

// readRuntime 实时读取当前客户端的内部目标和 DSH webToken，并执行防御校验。
func (api kaiwuProxyAPI) readRuntime(ctx context.Context, client string) (*url.URL, string, error) {
	if api.runtimeReader == nil {
		return nil, "", errKaiwuProxyUnavailable
	}
	target, webToken, err := api.runtimeReader.GetKaiwuRuntime(ctx, client)
	if err != nil || !validKaiwuRuntime(target, webToken) {
		return nil, "", errKaiwuProxyUnavailable
	}
	return target, webToken, nil
}

// copyKaiwuResponseHeaders 复制 DSH 响应 Header，改写 Location 和 Set-Cookie。
// 101 响应必须保留 Connection 与 Upgrade，普通响应则移除逐跳 Header。
func copyKaiwuResponseHeaders(c *app.RequestContext, response *http.Response, client string, target *url.URL, upgrade bool) {
	for key, values := range response.Header {
		if strings.EqualFold(key, "Set-Cookie") {
			continue
		}
		if isKaiwuResponseHeaderBlocked(strings.ToLower(key), upgrade) {
			continue
		}
		for _, value := range values {
			if strings.EqualFold(key, "Location") {
				value = rewriteKaiwuLocation(value, client, target)
			}
			c.Response.Header.Add(key, value)
		}
	}
	rewriteKaiwuSetCookies(c, response, client)
}

// rewriteKaiwuSetCookies 给 DSH Cookie 增加客户端前缀，并把 Path 限制在
// 对应的 /kaiwu/* 子路径，避免 Console 与 BOSS 互相覆盖。
func rewriteKaiwuSetCookies(c *app.RequestContext, response *http.Response, client string) {
	prefix := kaiwuDSHCookiePrefix(client)
	basePath := kaiwuProxyBasePath(client)
	secure := isKaiwuSecureRequest(c)
	for _, rawCookie := range response.Header.Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(rawCookie)
		if err != nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		browserCookie := &http.Cookie{
			Name:     prefix + cookie.Name,
			Value:    cookie.Value,
			Path:     basePath,
			Expires:  cookie.Expires,
			MaxAge:   cookie.MaxAge,
			HttpOnly: cookie.HttpOnly,
			Secure:   secure,
			SameSite: cookie.SameSite,
		}
		if browserCookie.SameSite == 0 {
			browserCookie.SameSite = http.SameSiteLaxMode
		}
		c.Response.Header.Add("Set-Cookie", browserCookie.String())
	}
}

// rewriteKaiwuLocation 把 DSH 内部相对路径或同主机绝对路径改写为 Gateway
// 的外部代理路径，避免把 ClusterIP 或内部根路径返回给浏览器。
func rewriteKaiwuLocation(location string, client string, target *url.URL) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return location
	}
	if parsed.IsAbs() && !strings.EqualFold(parsed.Host, target.Host) {
		return location
	}
	if strings.HasPrefix(parsed.Path, kaiwuProxyBasePath(client)) {
		return location
	}
	internalPath := parsed.Path
	if !strings.HasPrefix(internalPath, "/") {
		internalPath = "/" + internalPath
	}
	if internalPath == "/" {
		return kaiwuProxyBasePath(client)
	}
	parsed.Scheme = ""
	parsed.Opaque = ""
	parsed.Host = ""
	parsed.Path = kaiwuProxyBasePath(client) + internalPath
	return parsed.String()
}

// copyKaiwuWebSocket 双向复制客户端与 DSH 升级连接中的数据。
func copyKaiwuWebSocket(clientConnection net.Conn, serverConnection io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(serverConnection, clientConnection)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConnection, serverConnection)
		done <- struct{}{}
	}()
	<-done
	_ = serverConnection.Close()
	_ = clientConnection.Close()
}

// isKaiwuUpgradeRequest 判断当前请求是否为 WebSocket 或其他协议升级请求。
func isKaiwuUpgradeRequest(c *app.RequestContext) bool {
	connection := strings.ToLower(string(c.Request.Header.Peek("Connection")))
	upgrade := strings.ToLower(string(c.Request.Header.Peek("Upgrade")))
	return strings.Contains(connection, "upgrade") && upgrade != ""
}

// isKaiwuRequestHeaderBlocked 定义不转发给 DSH 的请求 Header。
func isKaiwuRequestHeaderBlocked(name string, upgrade bool) bool {
	switch name {
	case "authorization", "cookie", "host", "content-length", "proxy-authorization",
		"proxy-connection", "te", "trailer", "transfer-encoding",
		"x-forwarded-host", "x-forwarded-proto", "x-forwarded-for":
		return true
	case "connection", "upgrade":
		return !upgrade
	default:
		return false
	}
}

// isKaiwuResponseHeaderBlocked 定义不透传给浏览器的响应 Header。
func isKaiwuResponseHeaderBlocked(name string, upgrade bool) bool {
	switch name {
	case "content-length", "transfer-encoding", "trailer", "keep-alive",
		"proxy-authenticate", "proxy-authorization", "te":
		return true
	case "connection", "upgrade":
		return !upgrade
	default:
		return false
	}
}

// parseKaiwuProxyPath 将外部代理路径解析为客户端标识和 DSH 内部路径。
func parseKaiwuProxyPath(path string) (string, string, bool) {
	for _, client := range []string{kaiwuClientConsole, kaiwuClientBoss} {
		base := kaiwuProxyBasePath(client)
		if path == base || path == base+"/" {
			return client, "/", true
		}
		if strings.HasPrefix(path, base+"/") {
			return client, "/" + strings.TrimPrefix(path, base+"/"), true
		}
	}
	return "", "", false
}

// kaiwuProxyBasePath 返回指定客户端的浏览器入口路径。
func kaiwuProxyBasePath(client string) string {
	if client == kaiwuClientBoss {
		return "/kaiwu/boss"
	}
	return "/kaiwu/console"
}

// kaiwuDSHCookiePrefix 返回指定客户端的 DSH Cookie 浏览器前缀。
func kaiwuDSHCookiePrefix(client string) string {
	return "kaiwu_" + client + "_"
}

// kaiwuRequestAuthority 返回浏览器实际访问 Gateway 的 Host authority。
func kaiwuRequestAuthority(c *app.RequestContext) string {
	if authority := strings.TrimSpace(string(c.Host())); authority != "" {
		return authority
	}
	return strings.TrimSpace(string(c.GetHeader("X-Forwarded-Host")))
}

// kaiwuRequestProtocol 返回浏览器实际访问 Gateway 的协议。
func kaiwuRequestProtocol(c *app.RequestContext) string {
	if isKaiwuSecureRequest(c) {
		return "https"
	}
	return "http"
}

// errKaiwuProxyUnavailable 表示开物代理依赖不可用。
var errKaiwuProxyUnavailable = errors.New("kaiwu proxy unavailable")

// errKaiwuProxyUnauthorized 表示缺少或缺少有效的 Gateway 代理 Cookie。
var errKaiwuProxyUnauthorized = errors.New("kaiwu proxy unauthorized")

// errKaiwuProxyForbidden 表示 Cookie 属于另一个开物客户端。
var errKaiwuProxyForbidden = errors.New("kaiwu proxy forbidden")
