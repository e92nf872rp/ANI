package router

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

const (
	// kaiwuClientConsole 表示面向租户 Console 的开物实例。
	kaiwuClientConsole = "console"
	// kaiwuClientBoss 表示面向平台管理端 BOSS 的开物实例。
	kaiwuClientBoss = "boss"
)

// KaiwuRuntimeReader 读取某个开物客户端当前可用的运行时连接信息。
//
// 实现方必须返回开物 ClusterIP Service 的内部访问地址，以及 DSH 启动时
// 发布到 Kubernetes Secret 的 webToken。这两个值只能留在服务端请求处理
// 内存中：调用方不得把它们返回给浏览器、写入公开 URL，或记录到日志。
type KaiwuRuntimeReader interface {
	// GetKaiwuRuntime 返回指定开物客户端的当前运行时目标和 webToken。
	//
	// client 只允许使用 kaiwuClientConsole 或 kaiwuClientBoss，不能信任
	// 请求方传入的其他自定义值。实现必须在每次调用时实时读取 Kubernetes
	// Service 和 Secret，尤其不得缓存 webToken；Kaiwu Pod 重启后会发布新
	// token，复用旧 token 会导致认证失败。
	//
	// 不支持的 client 应返回包装 ports.ErrInvalid 的错误；Kubernetes 依赖
	// 缺失、不可读或数据非法时应返回包装 ports.ErrUnavailable 的错误。
	GetKaiwuRuntime(ctx context.Context, client string) (target *url.URL, webToken string, err error)
}

// kaiwuRuntimeReader 由 RegisterWithOptions 注入，供后续开物入口和代理
// 处理函数使用。nil 表示 Kubernetes 运行时未配置，后续处理函数必须按
// 503 失败关闭，不得降级为公开入口。
var kaiwuRuntimeReader KaiwuRuntimeReader

const (
	kaiwuConsoleTenantName = "tenant-a"
	kaiwuEntryExpiresIn    = 120
)

// kaiwuAPI 保存开物入口处理函数使用的权威数据存储和运行时读取器。
type kaiwuAPI struct {
	runtimeReader     KaiwuRuntimeReader
	cookieSigner      KaiwuCookieSigner
	tenantService     ports.TenantService
	platformUserStore ports.PlatformUserAdminStore
}

// kaiwuEntryResponse 是公开 JSON 契约；它永远不包含内部 ClusterIP 目标
// 或 DSH webToken。
type kaiwuEntryResponse struct {
	Client    string `json:"client"`
	EntryURL  string `json:"entry_url"`
	ExpiresIn int    `json:"expires_in"`
}

// registerKaiwuResources 注册两个 Services 入口操作。即使运行时读取器
// 为 nil 也始终注册路由，以便发现契约漂移；处理函数随后按 503 失败关闭。
func registerKaiwuResources(svc *route.RouterGroup, reader KaiwuRuntimeReader, cookieSigner KaiwuCookieSigner, tenantService ports.TenantService, platformUserStore ports.PlatformUserAdminStore) {
	api := kaiwuAPI{
		runtimeReader:     reader,
		cookieSigner:      cookieSigner,
		tenantService:     tenantService,
		platformUserStore: platformUserStore,
	}
	svc.GET("/integrations/kaiwu/console/entry", api.consoleEntry)
	svc.GET("/integrations/kaiwu/boss/entry", api.bossEntry)
}

// consoleEntry 授权当前租户用户，并返回共享 Kaiwu Console 实例的
// Gateway 代理入口。
func (api *kaiwuAPI) consoleEntry(ctx context.Context, c *app.RequestContext) {
	if !isKaiwuBearerUser(c) {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_USER_CREDENTIAL_REQUIRED", "bearer user credential required")
		return
	}
	if middleware.GetScope(c) != "tenant" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_CONSOLE_TENANT_NOT_ALLOWED", "tenant scope required")
		return
	}
	if api.tenantService == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "tenant service unavailable")
		return
	}

	tenantID := strings.TrimSpace(middleware.GetTenantID(c))
	if tenantID == "" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_CONSOLE_TENANT_NOT_ALLOWED", "tenant context missing")
		return
	}
	tenant, err := api.tenantService.GetTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ports.ErrTenantNotFound) {
			writeKaiwuError(c, http.StatusForbidden, "KAIWU_CONSOLE_TENANT_NOT_ALLOWED", "tenant not allowed")
			return
		}
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "tenant service unavailable")
		return
	}
	if tenant.Name != kaiwuConsoleTenantName || tenant.ID != tenantID {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_CONSOLE_TENANT_NOT_ALLOWED", "tenant not allowed")
		return
	}
	if tenant.Status != ports.TenantStatusActive {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_TENANT_NOT_ACTIVE", "tenant is not active")
		return
	}
	cookie, err := api.issueKaiwuBootstrapCookie(c, KaiwuCookieIdentity{
		Client:   kaiwuClientConsole,
		Scope:    "tenant",
		TenantID: tenantID,
		UserID:   strings.TrimSpace(middleware.GetUserID(c)),
	})
	if err != nil {
		return
	}

	target, webToken, err := api.readRuntime(ctx, c, kaiwuClientConsole)
	if err != nil {
		return
	}
	writeKaiwuEntry(c, kaiwuClientConsole, target, webToken, cookie)
}

// bossEntry 授权当前平台用户，并返回共享 Kaiwu BOSS 实例的 Gateway
// 代理入口。
func (api *kaiwuAPI) bossEntry(ctx context.Context, c *app.RequestContext) {
	if !isKaiwuBearerUser(c) {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_USER_CREDENTIAL_REQUIRED", "bearer user credential required")
		return
	}
	if middleware.GetScope(c) != "platform" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_BOSS_ROLE_REQUIRED", "platform scope required")
		return
	}
	if api.platformUserStore == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "platform user store unavailable")
		return
	}

	userID := strings.TrimSpace(middleware.GetUserID(c))
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_BOSS_ROLE_REQUIRED", "platform user context invalid")
		return
	}
	user, err := api.platformUserStore.Get(ctx, userUUID)
	if err != nil {
		if errors.Is(err, ports.ErrPlatformUserNotFound) {
			writeKaiwuError(c, http.StatusForbidden, "KAIWU_BOSS_ROLE_REQUIRED", "platform admin role required")
			return
		}
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "platform user store unavailable")
		return
	}
	if user.Role != "platform-admin" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_BOSS_ROLE_REQUIRED", "platform admin role required")
		return
	}
	if user.Status != "active" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_PLATFORM_USER_NOT_ACTIVE", "platform user is not active")
		return
	}
	cookie, err := api.issueKaiwuBootstrapCookie(c, KaiwuCookieIdentity{
		Client: kaiwuClientBoss,
		Scope:  "platform",
		UserID: userID,
	})
	if err != nil {
		return
	}

	target, webToken, err := api.readRuntime(ctx, c, kaiwuClientBoss)
	if err != nil {
		return
	}
	writeKaiwuEntry(c, kaiwuClientBoss, target, webToken, cookie)
}

// readRuntime 调用指定客户端的注入读取器，并执行本地纵深防御校验。
// 它不缓存、不暴露返回值。
func (api *kaiwuAPI) readRuntime(ctx context.Context, c *app.RequestContext, client string) (*url.URL, string, error) {
	if api.runtimeReader == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		return nil, "", ports.ErrUnavailable
	}
	target, webToken, err := api.runtimeReader.GetKaiwuRuntime(ctx, client)
	if err != nil || !validKaiwuRuntime(target, webToken) {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu runtime unavailable")
		if err == nil {
			err = ports.ErrUnavailable
		}
		return nil, "", err
	}
	return target, webToken, nil
}

// issueKaiwuBootstrapCookie 签名授权后的浏览器身份，并根据请求协议
// 设置 Secure Cookie 属性。
func (api *kaiwuAPI) issueKaiwuBootstrapCookie(c *app.RequestContext, identity KaiwuCookieIdentity) (KaiwuCookie, error) {
	if api.cookieSigner == nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu cookie signer unavailable")
		return KaiwuCookie{}, ports.ErrUnavailable
	}
	cookie, err := api.cookieSigner.IssueKaiwuBootstrapCookie(identity, time.Now(), isKaiwuSecureRequest(c))
	if err != nil {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu cookie signer unavailable")
		return KaiwuCookie{}, err
	}
	return cookie, nil
}

// writeKaiwuEntry 设置签名 HttpOnly Cookie，并只返回公开代理路径和 TTL。
// 内部目标与 DSH token 不会被序列化。
func writeKaiwuEntry(c *app.RequestContext, client string, _ *url.URL, _ string, cookie KaiwuCookie) {
	entryURL := "/kaiwu/console"
	if client == kaiwuClientBoss {
		entryURL = "/kaiwu/boss"
	}
	setKaiwuCookie(c, cookie)
	expiresIn := int(cookie.MaxAge / time.Second)
	c.JSON(http.StatusOK, kaiwuEntryResponse{
		Client:    client,
		EntryURL:  entryURL,
		ExpiresIn: expiresIn,
	})
}

// setKaiwuCookie 为签名 Gateway Cookie 统一设置 HttpOnly、SameSite=Lax、
// 客户端路径，以及由协议推导的 Secure 属性。
func setKaiwuCookie(c *app.RequestContext, cookie KaiwuCookie) {
	c.SetCookie(
		cookie.Name,
		cookie.Value,
		int(cookie.MaxAge/time.Second),
		cookie.Path,
		"",
		protocol.CookieSameSiteLaxMode,
		cookie.Secure,
		true,
	)
}

// isKaiwuSecureRequest 识别直接 HTTPS 请求，或可信 TLS 终止反向代理
// 传入的 X-Forwarded-Proto Header。
func isKaiwuSecureRequest(c *app.RequestContext) bool {
	if strings.EqualFold(string(c.Request.URI().Scheme()), "https") {
		return true
	}
	forwardedProto := strings.TrimSpace(string(c.GetHeader("X-Forwarded-Proto")))
	return strings.EqualFold(forwardedProto, "https")
}

// isKaiwuBearerUser 接受传统 bearer 用户和生成的 bearer 用户，但拒绝
// API Key、服务主体和沙箱主体。
func isKaiwuBearerUser(c *app.RequestContext) bool {
	if middleware.GetPrincipalKind(c) != "user" {
		return false
	}
	scheme := middleware.GetCredentialScheme(c)
	return scheme == "" || scheme == "bearer"
}

// validKaiwuRuntime 校验适配器返回的干净内部 HTTP 目标和非空令牌，
// 不信任任何畸形适配器数据。
func validKaiwuRuntime(target *url.URL, webToken string) bool {
	return target != nil &&
		target.Scheme == "http" &&
		target.Host != "" &&
		target.User == nil &&
		target.Path == "" &&
		target.RawQuery == "" &&
		target.Fragment == "" &&
		strings.TrimSpace(webToken) != ""
}

// writeKaiwuError 写入统一错误响应，不回显内部 Kubernetes 错误、
// ClusterIP、Cookie 或 DSH token。
func writeKaiwuError(c *app.RequestContext, status int, code, message string) {
	c.JSON(status, utils.H{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
	c.Abort()
}
