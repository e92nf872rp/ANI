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
// 发布到 Kubernetes Secret 的 webToken。内部 ClusterIP 目标只能留在
// 服务端请求处理内存中：调用方不得把它返回给浏览器或记录到日志。
//
// webToken 作为 DSH 原生的一次性启动参数出现在返回给浏览器的入口地址中
// （开物独占 origin，Gateway 不做子路径反向代理）。因此入口地址必须视为
// 凭据：只允许即时跳转，不得写入日志、工单、埋点或任何持久化存储。
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

const (
	// kaiwuConsoleTenantName 表示允许进入开物 Console 的默认租户名。
	kaiwuConsoleTenantName = "tenant-a"
	// kaiwuBossRootUsername 表示允许进入开物 BOSS 的默认 root 账号名
	// （库内用户名带 local:/oidc: 前缀，比较前必须剥除）。
	kaiwuBossRootUsername = "root"
	kaiwuEntryExpiresIn   = 120
)

// kaiwuAPI 保存开物入口处理函数使用的权威数据存储和运行时读取器。
type kaiwuAPI struct {
	runtimeReader     KaiwuRuntimeReader
	tenantService     ports.TenantService
	platformUserStore ports.PlatformUserAdminStore
	publicEntry       KaiwuPublicEntryConfig
}

// KaiwuPublicEntryConfig 描述开物独占 origin 的入口配置。开物页面依赖
// 根路径绝对资源（/assets、/plugins、/api），无法在子路径下运行，因此
// 部署让开物独占一个 origin（NodePort 或独立域名），Gateway 只负责
// 鉴权并返回该 origin 的绝对入口地址。
type KaiwuPublicEntryConfig struct {
	// ConsoleURL 是开物 Console 对外暴露的 origin 基址，例如
	// http://10.10.1.66:30088。为空表示该客户端未配置，入口按 503 失败关闭。
	ConsoleURL string
	// BossURL 是开物 BOSS 对外暴露的 origin 基址，语义同 ConsoleURL。
	BossURL string
	// TokenTTL 是入口地址有效期提示，零值或负值使用 kaiwuEntryExpiresIn。
	TokenTTL time.Duration
}

// kaiwuEntryResponse 是公开 JSON 契约；它永远不包含内部 ClusterIP 目标。
// entry_url 是开物自身 origin 的绝对地址，按设计携带一次性启动 token。
type kaiwuEntryResponse struct {
	Client    string `json:"client"`
	EntryURL  string `json:"entry_url"`
	ExpiresIn int    `json:"expires_in"`
}

// registerKaiwuResources 注册两个 Services 入口操作。即使运行时读取器
// 为 nil 也始终注册路由，以便发现契约漂移；处理函数随后按 503 失败关闭。
func registerKaiwuResources(svc *route.RouterGroup, reader KaiwuRuntimeReader, tenantService ports.TenantService, platformUserStore ports.PlatformUserAdminStore, publicEntry KaiwuPublicEntryConfig) {
	api := kaiwuAPI{
		runtimeReader:     reader,
		tenantService:     tenantService,
		platformUserStore: platformUserStore,
		publicEntry:       publicEntry,
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
	baseURL := api.publicEntryBaseURL(kaiwuClientConsole)
	if baseURL == "" {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu console origin not configured")
		return
	}
	_, webToken, err := api.readRuntime(ctx, c, kaiwuClientConsole)
	if err != nil {
		return
	}
	writeKaiwuPublicEntry(c, kaiwuClientConsole, kaiwuPublicEntryURL(baseURL, webToken), api.publicEntryTTLSeconds())
}

// bossEntry 授权默认 root 平台账号，并返回共享 Kaiwu BOSS 实例的
// Gateway 代理入口。产品语义只对 root 账号开放：即使同为 platform-admin
// 角色，其他平台账号也一律拒绝。
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
	// 精确到默认 root 账号：platform-admin 角色但用户名不是 root 的账号
	// 同样拒绝，防止新创建的平台管理员进入开物。
	if kaiwuBareUsername(user.Username) != kaiwuBossRootUsername {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_BOSS_ROLE_REQUIRED", "platform admin role required")
		return
	}
	if user.Status != "active" {
		writeKaiwuError(c, http.StatusForbidden, "KAIWU_PLATFORM_USER_NOT_ACTIVE", "platform user is not active")
		return
	}
	baseURL := api.publicEntryBaseURL(kaiwuClientBoss)
	if baseURL == "" {
		writeKaiwuError(c, http.StatusServiceUnavailable, "KAIWU_BACKEND_UNAVAILABLE", "Kaiwu boss origin not configured")
		return
	}
	_, webToken, err := api.readRuntime(ctx, c, kaiwuClientBoss)
	if err != nil {
		return
	}
	writeKaiwuPublicEntry(c, kaiwuClientBoss, kaiwuPublicEntryURL(baseURL, webToken), api.publicEntryTTLSeconds())
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

// publicEntryBaseURL 返回指定客户端配置的独占 origin 基址。未配置时返回
// 空字符串，调用方按 503 失败关闭。
func (api *kaiwuAPI) publicEntryBaseURL(client string) string {
	if client == kaiwuClientBoss {
		return strings.TrimSpace(api.publicEntry.BossURL)
	}
	return strings.TrimSpace(api.publicEntry.ConsoleURL)
}

// publicEntryTTLSeconds 返回独占 origin 入口地址的有效期提示（秒）。
func (api *kaiwuAPI) publicEntryTTLSeconds() int {
	if api.publicEntry.TokenTTL > 0 {
		return int(api.publicEntry.TokenTTL / time.Second)
	}
	return kaiwuEntryExpiresIn
}

// kaiwuPublicEntryURL 拼接开物独占 origin 的绝对入口地址。DSH 收到
// ?token= 后会立即以 303 跳转到根路径并下发自身会话 Cookie，token 不会
// 长期停留在地址栏，但该地址在有效期内等同凭据。
func kaiwuPublicEntryURL(baseURL string, webToken string) string {
	return strings.TrimRight(baseURL, "/") + "/?token=" + url.QueryEscape(webToken)
}

// writeKaiwuPublicEntry 返回开物独占 origin 的绝对入口地址。Gateway 不
// 签发任何代理 Cookie，也不承担子路径反向代理。
func writeKaiwuPublicEntry(c *app.RequestContext, client string, entryURL string, expiresIn int) {
	c.JSON(http.StatusOK, kaiwuEntryResponse{
		Client:    client,
		EntryURL:  entryURL,
		ExpiresIn: expiresIn,
	})
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

// kaiwuBareUsername 剥除库内用户名的 local:/oidc: 命名空间前缀，返回对外
// 账号名。与平台账号适配器 REGEXP_REPLACE 的剥前缀约定保持一致。
func kaiwuBareUsername(username string) string {
	username = strings.TrimPrefix(username, "local:")
	return strings.TrimPrefix(username, "oidc:")
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
