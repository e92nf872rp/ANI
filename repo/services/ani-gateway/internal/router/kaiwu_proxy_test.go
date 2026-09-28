package router

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
)

// kaiwuProxyTestFixture 保存代理测试使用的 Hertz 服务、DSH fake 服务、
// runtime reader 和签名器，便于断言端到端请求链路。
type kaiwuProxyTestFixture struct {
	hertz         *server.Hertz
	dsh           *httptest.Server
	runtimeReader *fakeKaiwuRuntimeReader
	cookieSigner  KaiwuCookieSigner
}

// setupKaiwuProxyFixture 构建一个指向进程内 fake DSH 的完整代理测试环境。
func setupKaiwuProxyFixture(t *testing.T, handler http.HandlerFunc) *kaiwuProxyTestFixture {
	t.Helper()
	dsh := httptest.NewServer(handler)
	t.Cleanup(dsh.Close)
	target, err := url.Parse(dsh.URL)
	if err != nil {
		t.Fatalf("parse DSH URL: %v", err)
	}
	reader := &fakeKaiwuRuntimeReader{target: target, webToken: "launch-token"}
	signer := newTestKaiwuCookieSigner(t)
	h := server.New()
	registerKaiwuProxy(
		h.Group(""),
		reader,
		signer,
		&http.Client{
			Transport:     http.DefaultTransport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	)
	return &kaiwuProxyTestFixture{
		hertz:         h,
		dsh:           dsh,
		runtimeReader: reader,
		cookieSigner:  signer,
	}
}

// performKaiwuProxyRequest 直接调用 Hertz 引擎执行开物代理请求，并保留
// 原始响应对象，便于测试流式响应体。
func performKaiwuProxyRequest(h *server.Hertz, path string, headers ...ut.Header) *protocol.Response {
	requestContext := h.Engine.NewContext()
	outboundRequest := protocol.NewRequest(http.MethodGet, path, nil)
	outboundRequest.CopyTo(&requestContext.Request)
	for _, header := range headers {
		if requestContext.Request.Header.Get(header.Key) != "" {
			requestContext.Request.Header.Add(header.Key, header.Value)
		} else {
			requestContext.Request.Header.Set(header.Key, header.Value)
		}
	}
	h.Engine.ServeHTTP(context.Background(), requestContext)
	return &requestContext.Response
}

// issueKaiwuProxyTestCookie 为指定身份签发测试 Cookie。
func issueKaiwuProxyTestCookie(t *testing.T, fixture *kaiwuProxyTestFixture, cookieType KaiwuCookieType, identity KaiwuCookieIdentity) KaiwuCookie {
	t.Helper()
	var cookie KaiwuCookie
	var err error
	if cookieType == KaiwuCookieTypeSession {
		cookie, err = fixture.cookieSigner.IssueKaiwuSessionCookie(identity, time.Now(), false)
	} else {
		cookie, err = fixture.cookieSigner.IssueKaiwuBootstrapCookie(identity, time.Now(), false)
	}
	if err != nil {
		t.Fatalf("issue Kaiwu proxy cookie: %v", err)
	}
	return cookie
}

// kaiwuConsoleTestIdentity 返回 Console 代理测试使用的合法身份。
func kaiwuConsoleTestIdentity() KaiwuCookieIdentity {
	return KaiwuCookieIdentity{
		Client:   kaiwuClientConsole,
		Scope:    "tenant",
		TenantID: "11111111-1111-1111-1111-111111111111",
		UserID:   "22222222-2222-2222-2222-222222222222",
	}
}

// TestKaiwuProxyBootstrapExchangesToken 验证首次代理请求会实时读取 DSH
// webToken，改写 DSH Cookie，并在不暴露 webToken 的情况下重定向回代理路径。
func TestKaiwuProxyBootstrapExchangesToken(t *testing.T) {
	var mutex sync.Mutex
	var receivedTokens []string
	fixture := setupKaiwuProxyFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		receivedTokens = append(receivedTokens, request.URL.Query().Get("token"))
		mutex.Unlock()
		writer.Header().Set("Location", "/")
		writer.Header().Add("Set-Cookie", "dsh_session=exchange-value; Path=/; HttpOnly; SameSite=Lax")
		writer.WriteHeader(http.StatusSeeOther)
	})
	cookie := issueKaiwuProxyTestCookie(t, fixture, KaiwuCookieTypeBootstrap, kaiwuConsoleTestIdentity())

	response := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console/assets/app.js",
		ut.Header{Key: "Cookie", Value: cookie.Name + "=" + cookie.Value},
	)
	if response.StatusCode() != http.StatusFound {
		t.Fatalf("status=%d body=%s", response.StatusCode(), string(response.Body()))
	}
	if location := response.Header.Get("Location"); location != "/kaiwu/console" {
		t.Fatalf("Location=%q", location)
	}
	setCookies := strings.Join(response.Header.GetAll("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, "kaiwu_console_dsh_session=exchange-value") ||
		!strings.Contains(setCookies, "Path=/kaiwu/console") ||
		!strings.Contains(setCookies, "ani_kaiwu_session_console=") {
		t.Fatalf("Set-Cookie=%s", setCookies)
	}
	if strings.Contains(setCookies, "launch-token") || strings.Contains(string(response.Body()), "launch-token") {
		t.Fatalf("DSH token leaked: cookies=%s body=%s", setCookies, string(response.Body()))
	}
	if len(fixture.runtimeReader.calls) != 1 || fixture.runtimeReader.calls[0] != kaiwuClientConsole {
		t.Fatalf("runtime calls=%v", fixture.runtimeReader.calls)
	}
	mutex.Lock()
	if len(receivedTokens) != 1 || receivedTokens[0] != "launch-token" {
		t.Fatalf("DSH tokens=%v", receivedTokens)
	}
	mutex.Unlock()
}

// TestKaiwuProxyRereadsWebTokenWithoutDSHCookie 验证仅携带 Gateway session
// Cookie 的请求会重新实时读取 Secret，并可以使用轮换后的 DSH webToken。
func TestKaiwuProxyRereadsWebTokenWithoutDSHCookie(t *testing.T) {
	var mutex sync.Mutex
	var receivedTokens []string
	fixture := setupKaiwuProxyFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		receivedTokens = append(receivedTokens, request.URL.Query().Get("token"))
		mutex.Unlock()
		writer.Header().Add("Set-Cookie", "dsh_session=value; Path=/; HttpOnly")
		writer.WriteHeader(http.StatusSeeOther)
	})

	firstCookie := issueKaiwuProxyTestCookie(t, fixture, KaiwuCookieTypeSession, kaiwuConsoleTestIdentity())
	first := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console",
		ut.Header{Key: "Cookie", Value: firstCookie.Name + "=" + firstCookie.Value},
	)
	if first.StatusCode() != http.StatusFound {
		t.Fatalf("first status=%d body=%s", first.StatusCode(), string(first.Body()))
	}
	fixture.runtimeReader.webToken = "rotated-token"
	secondCookie := issueKaiwuProxyTestCookie(t, fixture, KaiwuCookieTypeSession, kaiwuConsoleTestIdentity())
	second := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console",
		ut.Header{Key: "Cookie", Value: secondCookie.Name + "=" + secondCookie.Value},
	)
	if second.StatusCode() != http.StatusFound {
		t.Fatalf("second status=%d body=%s", second.StatusCode(), string(second.Body()))
	}
	if len(fixture.runtimeReader.calls) != 2 {
		t.Fatalf("runtime calls=%v", fixture.runtimeReader.calls)
	}
	mutex.Lock()
	if len(receivedTokens) != 2 || receivedTokens[0] != "launch-token" || receivedTokens[1] != "rotated-token" {
		t.Fatalf("DSH tokens=%v", receivedTokens)
	}
	mutex.Unlock()
}

// TestKaiwuProxyForwardsSessionRequest 验证普通 DSH 请求和 SSE 响应中的
// 路径、查询参数、Header 与 Cookie 改写逻辑。
func TestKaiwuProxyForwardsSessionRequest(t *testing.T) {
	fixture := setupKaiwuProxyFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/assets/app.js" || request.URL.RawQuery != "x=1" {
			t.Errorf("DSH path/query = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		if request.Header.Get("Cookie") != "dsh_session=abc" {
			t.Errorf("DSH Cookie = %q", request.Header.Get("Cookie"))
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("ANI Authorization was forwarded to DSH")
		}
		if request.Host == "" {
			t.Error("DSH Host is empty")
		}
		if request.Header.Get("X-Forwarded-Host") != "gateway.example" {
			t.Errorf("X-Forwarded-Host = %q", request.Header.Get("X-Forwarded-Host"))
		}
		if request.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("X-Forwarded-Proto = %q", request.Header.Get("X-Forwarded-Proto"))
		}
		if request.Header.Get("X-Forwarded-For") == "" {
			t.Error("X-Forwarded-For is empty")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Location", "/assets/next")
		writer.Header().Add("Set-Cookie", "dsh_session=next; Path=/; HttpOnly; SameSite=Lax")
		_, _ = writer.Write([]byte("data: ok\n\n"))
	})
	cookie := issueKaiwuProxyTestCookie(t, fixture, KaiwuCookieTypeSession, kaiwuConsoleTestIdentity())
	cookieHeader := strings.Join([]string{
		cookie.Name + "=" + cookie.Value,
		"kaiwu_console_dsh_session=abc",
		"ani_kaiwu_bootstrap_boss=must-not-forward",
	}, "; ")

	response := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console/assets/app.js?x=1",
		ut.Header{Key: "Cookie", Value: cookieHeader},
		ut.Header{Key: "Authorization", Value: "Bearer ani-token"},
		ut.Header{Key: "X-Forwarded-Host", Value: "gateway.example"},
	)
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode(), string(response.Body()))
	}
	body, readErr := io.ReadAll(response.BodyStream())
	if readErr != nil {
		t.Fatalf("read DSH response body: %v", readErr)
	}
	if string(body) != "data: ok\n\n" {
		t.Fatalf("body=%q", string(body))
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("Content-Type=%q", contentType)
	}
	if location := response.Header.Get("Location"); location != "/kaiwu/console/assets/next" {
		t.Fatalf("Location=%q", location)
	}
	setCookies := strings.Join(response.Header.GetAll("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, "kaiwu_console_dsh_session=next") || !strings.Contains(setCookies, "Path=/kaiwu/console") {
		t.Fatalf("Set-Cookie=%s", setCookies)
	}
	if len(fixture.runtimeReader.calls) != 1 || fixture.runtimeReader.calls[0] != kaiwuClientConsole {
		t.Fatalf("runtime calls=%v", fixture.runtimeReader.calls)
	}
}

// TestKaiwuProxyAuthorizationFailures 覆盖缺少 Cookie、过期 Cookie 和跨客户端
// Cookie 的拒绝场景，并确认错误响应不会暴露签名 Cookie 内容。
func TestKaiwuProxyAuthorizationFailures(t *testing.T) {
	fixture := setupKaiwuProxyFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("DSH request should not be sent without a valid Gateway cookie")
		writer.WriteHeader(http.StatusOK)
	})
	identity := kaiwuConsoleTestIdentity()
	bossIdentity := KaiwuCookieIdentity{
		Client: kaiwuClientBoss,
		Scope:  "platform",
		UserID: identity.UserID,
	}
	bossCookie := issueKaiwuProxyTestCookie(t, fixture, KaiwuCookieTypeBootstrap, bossIdentity)
	expiredCookie, err := fixture.cookieSigner.IssueKaiwuBootstrapCookie(identity, time.Now().Add(-2*time.Minute), false)
	if err != nil {
		t.Fatalf("issue expired cookie: %v", err)
	}

	noCookie := performKaiwuProxyRequest(fixture.hertz, "/kaiwu/console")
	if noCookie.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("no-cookie status=%d body=%s", noCookie.StatusCode(), string(noCookie.Body()))
	}
	expired := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console",
		ut.Header{Key: "Cookie", Value: expiredCookie.Name + "=" + expiredCookie.Value},
	)
	if expired.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("expired status=%d body=%s", expired.StatusCode(), string(expired.Body()))
	}
	crossClient := performKaiwuProxyRequest(
		fixture.hertz,
		"/kaiwu/console",
		ut.Header{Key: "Cookie", Value: bossCookie.Name + "=" + bossCookie.Value},
	)
	if crossClient.StatusCode() != http.StatusForbidden {
		t.Fatalf("cross-client status=%d body=%s", crossClient.StatusCode(), string(crossClient.Body()))
	}
	for _, response := range []*protocol.Response{noCookie, expired, crossClient} {
		if body := string(response.Body()); strings.Contains(body, "launch-token") || strings.Contains(body, bossCookie.Value) {
			t.Fatalf("error response leaked cookie/token: %s", body)
		}
	}
}

// TestKaiwuProxyPathHelpers 验证外部路径映射和 Location 重写逻辑。
func TestKaiwuProxyPathHelpers(t *testing.T) {
	tests := []struct {
		path             string
		client           string
		internal         string
		ok               bool
		location         string
		externalLocation string
	}{
		{path: "/kaiwu/console", client: kaiwuClientConsole, internal: "/", ok: true, location: "/", externalLocation: "/kaiwu/console"},
		{path: "/kaiwu/console/", client: kaiwuClientConsole, internal: "/", ok: true, location: "/assets/x", externalLocation: "/kaiwu/console/assets/x"},
		{path: "/kaiwu/console/assets/x", client: kaiwuClientConsole, internal: "/assets/x", ok: true, location: "relative", externalLocation: "/kaiwu/console/relative"},
		{path: "/kaiwu/boss", client: kaiwuClientBoss, internal: "/", ok: true, location: "/", externalLocation: "/kaiwu/boss"},
		{path: "/kaiwu/other", ok: false},
	}
	for _, tt := range tests {
		client, internal, ok := parseKaiwuProxyPath(tt.path)
		if client != tt.client || internal != tt.internal || ok != tt.ok {
			t.Fatalf("parse(%q)=(%q,%q,%v), want (%q,%q,%v)", tt.path, client, internal, ok, tt.client, tt.internal, tt.ok)
		}
		if !tt.ok {
			continue
		}
		target := &url.URL{Scheme: "http", Host: "10.0.0.1:3080"}
		if got := rewriteKaiwuLocation(tt.location, client, target); got != tt.externalLocation {
			t.Fatalf("rewrite(%q,%q)=%q, want %q", tt.location, client, got, tt.externalLocation)
		}
	}
}

// TestKaiwuProxyDependenciesFailClosed 验证签名器、运行时读取器或 HTTP
// 客户端缺失时返回 503，而不是打开未认证代理。
func TestKaiwuProxyDependenciesFailClosed(t *testing.T) {
	dsh := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer dsh.Close()
	target, err := url.Parse(dsh.URL)
	if err != nil {
		t.Fatalf("parse DSH URL: %v", err)
	}
	reader := &fakeKaiwuRuntimeReader{target: target, webToken: "token"}
	signer := newTestKaiwuCookieSigner(t)
	cookie, err := signer.IssueKaiwuBootstrapCookie(kaiwuConsoleTestIdentity(), time.Now(), false)
	if err != nil {
		t.Fatalf("issue cookie: %v", err)
	}
	tests := []struct {
		name       string
		reader     KaiwuRuntimeReader
		signer     KaiwuCookieSigner
		httpClient KaiwuProxyHTTPClient
	}{
		{name: "missing signer", reader: reader},
		{name: "missing runtime", signer: signer, httpClient: &http.Client{}},
		{name: "missing http client", reader: reader, signer: signer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := server.New()
			registerKaiwuProxy(h.Group(""), tt.reader, tt.signer, tt.httpClient)
			response := performKaiwuProxyRequest(
				h,
				"/kaiwu/console",
				ut.Header{Key: "Cookie", Value: cookie.Name + "=" + cookie.Value},
			)
			if response.StatusCode() != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", response.StatusCode(), string(response.Body()))
			}
		})
	}
}

// TestCopyKaiwuWebSocketForwardsBothDirections 验证 WebSocket 劫持后的
// 双向字节流转发，确保浏览器和 DSH 两个方向都不会被代理截断。
func TestCopyKaiwuWebSocketForwardsBothDirections(t *testing.T) {
	browserConnection, proxyClientConnection := net.Pipe()
	proxyServerConnection, dshConnection := net.Pipe()
	done := make(chan struct{})
	go func() {
		copyKaiwuWebSocket(proxyClientConnection, proxyServerConnection)
		close(done)
	}()

	browserWriteDone := make(chan struct{})
	go func() {
		_, _ = browserConnection.Write([]byte("browser"))
		close(browserWriteDone)
	}()
	browserMessage := make([]byte, len("browser"))
	if _, err := io.ReadFull(dshConnection, browserMessage); err != nil {
		t.Fatalf("read browser message: %v", err)
	}
	select {
	case <-browserWriteDone:
	case <-time.After(2 * time.Second):
		t.Fatal("browser write timed out")
	}

	dshWriteDone := make(chan struct{})
	go func() {
		_, _ = dshConnection.Write([]byte("dsh"))
		close(dshWriteDone)
	}()
	dshMessage := make([]byte, len("dsh"))
	if _, err := io.ReadFull(browserConnection, dshMessage); err != nil {
		t.Fatalf("read DSH message: %v", err)
	}
	select {
	case <-dshWriteDone:
	case <-time.After(2 * time.Second):
		t.Fatal("DSH write timed out")
	}

	_ = browserConnection.Close()
	_ = dshConnection.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket proxy did not stop")
	}
}
