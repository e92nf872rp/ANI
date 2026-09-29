package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// fakeKaiwuRuntimeReader 是 entry 测试使用的内存运行时 reader。它会记录
// 被请求的客户端，便于测试验证 console 和 boss 不会意外交换运行时凭据。
type fakeKaiwuRuntimeReader struct {
	target   *url.URL
	webToken string
	err      error
	calls    []string
}

// GetKaiwuRuntime 返回配置的运行时，并记录被请求的客户端。
func (f *fakeKaiwuRuntimeReader) GetKaiwuRuntime(_ context.Context, client string) (*url.URL, string, error) {
	f.calls = append(f.calls, client)
	return f.target, f.webToken, f.err
}

// fakeKaiwuTenantService 只实现 GetTenant，因为 entry handler 不使用其他
// 租户管理操作。
type fakeKaiwuTenantService struct {
	ports.TenantService
	tenant ports.Tenant
	err    error
}

// GetTenant 为授权测试返回配置的租户或错误。
func (f *fakeKaiwuTenantService) GetTenant(context.Context, string) (ports.Tenant, error) {
	return f.tenant, f.err
}

// fakeKaiwuPlatformUserStore 只实现 Get，因为 BOSS entry handler 只需要
// 校验权威 platform-admin 角色、root 账号名和状态。
type fakeKaiwuPlatformUserStore struct {
	ports.PlatformUserAdminStore
	user ports.PlatformUserAdmin
	err  error
}

// Get 为授权测试返回配置的平台用户或错误。
func (f *fakeKaiwuPlatformUserStore) Get(context.Context, uuid.UUID) (ports.PlatformUserAdmin, error) {
	return f.user, f.err
}

// kaiwuTestIdentity 模拟生产 Auth 中间件写入的认证上下文。空
// credential_scheme 表示传统 bearer 身份。
type kaiwuTestIdentity struct {
	tenantID         string
	userID           string
	scope            string
	principalKind    string
	credentialScheme string
}

// testKaiwuPublicEntry 返回测试用的独占 origin 入口配置。
func testKaiwuPublicEntry() KaiwuPublicEntryConfig {
	return KaiwuPublicEntryConfig{
		ConsoleURL: "http://kaiwu-console.test:30088",
		BossURL:    "http://kaiwu-boss.test:30089",
	}
}

// setupKaiwuEntryTestServer 构建带测试身份上下文的服务器，并直接注册
// 开物入口处理函数。
func setupKaiwuEntryTestServer(
	identity kaiwuTestIdentity,
	reader KaiwuRuntimeReader,
	tenantService ports.TenantService,
	platformUserStore ports.PlatformUserAdminStore,
	publicEntry KaiwuPublicEntryConfig,
) *server.Hertz {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", identity.tenantID)
		c.Set("user_id", identity.userID)
		c.Set("scope", identity.scope)
		c.Set("principal_kind", identity.principalKind)
		c.Set("credential_scheme", identity.credentialScheme)
		c.Next(ctx)
	})
	registerKaiwuResources(h.Group("/api/v1/svc"), reader, tenantService, platformUserStore, publicEntry)
	return h
}

// performKaiwuEntryRequest 执行一个 entry 请求，并返回原始响应。
func performKaiwuEntryRequest(h *server.Hertz, path string) *protocol.Response {
	return ut.PerformRequest(h.Engine, http.MethodGet, path, nil).Result()
}

// decodeKaiwuJSON 将 Kaiwu 响应解码为通用对象，用于契约断言。
func decodeKaiwuJSON(t *testing.T, resp *protocol.Response) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatalf("decode response body %q: %v", string(resp.Body()), err)
	}
	return payload
}

// TestRegisterWithOptionsRegistersKaiwuResources 验证两个 entry 路由均已
// 通过 RegisterWithOptions 暴露。
func TestRegisterWithOptionsRegistersKaiwuResources(t *testing.T) {
	h := server.New()
	RegisterWithOptions(h, RegisterOptions{KaiwuRuntimeReader: &fakeKaiwuRuntimeReader{}})

	for _, path := range []string{
		"/api/v1/svc/integrations/kaiwu/console/entry",
		"/api/v1/svc/integrations/kaiwu/boss/entry",
	} {
		if resp := performKaiwuEntryRequest(h, path); resp.StatusCode() == http.StatusNotFound {
			t.Fatalf("Kaiwu route was not registered: %s", path)
		}
	}
}

// TestKaiwuConsoleEntry_AllowedTenant 验证被允许的 active 租户能获得开物
// 独占 origin 的绝对入口地址，且响应不包含内部 ClusterIP。
func TestKaiwuConsoleEntry_AllowedTenant(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	reader := &fakeKaiwuRuntimeReader{
		target:   &url.URL{Scheme: "http", Host: "10.0.0.1:3080"},
		webToken: "test-dsh-console-token",
	}
	tenantService := &fakeKaiwuTenantService{tenant: ports.Tenant{
		ID:     tenantID,
		Name:   "tenant-a",
		Status: ports.TenantStatusActive,
	}}
	h := setupKaiwuEntryTestServer(
		kaiwuTestIdentity{
			tenantID:      tenantID,
			userID:        "22222222-2222-2222-2222-222222222222",
			scope:         "tenant",
			principalKind: "user",
		},
		reader,
		tenantService,
		nil,
		testKaiwuPublicEntry(),
	)

	resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/console/entry")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	payload := decodeKaiwuJSON(t, resp)
	if payload["client"] != "console" ||
		payload["entry_url"] != "http://kaiwu-console.test:30088/?token=test-dsh-console-token" ||
		payload["expires_in"] != float64(120) {
		t.Fatalf("payload=%v", payload)
	}
	if cookieHeader := resp.Header.Get("Set-Cookie"); cookieHeader != "" {
		t.Fatalf("entry API must not set a Gateway cookie: %s", cookieHeader)
	}
	if body := string(resp.Body()); strings.Contains(body, "10.0.0.1") || strings.Contains(body, "webToken") {
		t.Fatalf("response leaked internal runtime details: %s", body)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "console" {
		t.Fatalf("reader calls=%v", reader.calls)
	}
}

// TestKaiwuConsoleEntry_AuthorizationAndAvailability 覆盖租户名、租户 ID、
// 状态、凭据类型、scope 和依赖不可用场景。
func TestKaiwuConsoleEntry_AuthorizationAndAvailability(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	validTenant := ports.Tenant{ID: tenantID, Name: "tenant-a", Status: ports.TenantStatusActive}
	tests := []struct {
		name           string
		identity       kaiwuTestIdentity
		tenantService  ports.TenantService
		reader         KaiwuRuntimeReader
		publicEntry    KaiwuPublicEntryConfig
		expectedStatus int
		expectedCode   string
	}{
		{
			name:     "other tenant name",
			identity: kaiwuTestIdentity{tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user"},
			tenantService: &fakeKaiwuTenantService{tenant: ports.Tenant{
				ID: tenantID, Name: "tenant-b", Status: ports.TenantStatusActive,
			}},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_CONSOLE_TENANT_NOT_ALLOWED",
		},
		{
			name:     "tenant id mismatch",
			identity: kaiwuTestIdentity{tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user"},
			tenantService: &fakeKaiwuTenantService{tenant: ports.Tenant{
				ID: "33333333-3333-3333-3333-333333333333", Name: "tenant-a", Status: ports.TenantStatusActive,
			}},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_CONSOLE_TENANT_NOT_ALLOWED",
		},
		{
			name:     "inactive tenant",
			identity: kaiwuTestIdentity{tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user"},
			tenantService: &fakeKaiwuTenantService{tenant: ports.Tenant{
				ID: tenantID, Name: "tenant-a", Status: ports.TenantStatusFrozen,
			}},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_TENANT_NOT_ACTIVE",
		},
		{
			name: "api key credential",
			identity: kaiwuTestIdentity{
				tenantID: tenantID, userID: tenantID, scope: "tenant",
				principalKind: "service", credentialScheme: "api-key",
			},
			tenantService:  &fakeKaiwuTenantService{tenant: validTenant},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_USER_CREDENTIAL_REQUIRED",
		},
		{
			name: "platform scope",
			identity: kaiwuTestIdentity{
				tenantID: tenantID, userID: tenantID, scope: "platform", principalKind: "user",
			},
			tenantService:  &fakeKaiwuTenantService{tenant: validTenant},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_CONSOLE_TENANT_NOT_ALLOWED",
		},
		{
			name: "tenant service unavailable",
			identity: kaiwuTestIdentity{
				tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user",
			},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "KAIWU_BACKEND_UNAVAILABLE",
		},
		{
			name: "console origin not configured",
			identity: kaiwuTestIdentity{
				tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user",
			},
			tenantService:  &fakeKaiwuTenantService{tenant: validTenant},
			reader:         &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
			publicEntry:    KaiwuPublicEntryConfig{BossURL: "http://kaiwu-boss.test:30089"},
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "KAIWU_BACKEND_UNAVAILABLE",
		},
		{
			name: "runtime unavailable",
			identity: kaiwuTestIdentity{
				tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user",
			},
			tenantService:  &fakeKaiwuTenantService{tenant: validTenant},
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "KAIWU_BACKEND_UNAVAILABLE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicEntry := tt.publicEntry
			if publicEntry.ConsoleURL == "" && tt.name != "console origin not configured" {
				publicEntry = testKaiwuPublicEntry()
			}
			h := setupKaiwuEntryTestServer(tt.identity, tt.reader, tt.tenantService, nil, publicEntry)
			resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/console/entry")
			if resp.StatusCode() != tt.expectedStatus {
				t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
			}
			if payload := decodeKaiwuJSON(t, resp); payload["code"] != tt.expectedCode {
				t.Fatalf("code=%v body=%s", payload["code"], string(resp.Body()))
			}
		})
	}
}

// TestKaiwuBossEntry_AllowedPlatformAdmin 验证 active 平台管理员能获得开物
// BOSS 独占 origin 的绝对入口地址。
func TestKaiwuBossEntry_AllowedPlatformAdmin(t *testing.T) {
	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	reader := &fakeKaiwuRuntimeReader{
		target:   &url.URL{Scheme: "http", Host: "10.0.0.2:3080"},
		webToken: "test-dsh-boss-token",
	}
	h := setupKaiwuEntryTestServer(
		kaiwuTestIdentity{userID: userID.String(), scope: "platform", principalKind: "user"},
		reader,
		nil,
		&fakeKaiwuPlatformUserStore{user: ports.PlatformUserAdmin{
			ID: userID, Username: "local:root", Role: "platform-admin", Status: "active",
		}},
		testKaiwuPublicEntry(),
	)

	resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/boss/entry")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	payload := decodeKaiwuJSON(t, resp)
	if payload["client"] != "boss" ||
		payload["entry_url"] != "http://kaiwu-boss.test:30089/?token=test-dsh-boss-token" ||
		payload["expires_in"] != float64(120) {
		t.Fatalf("payload=%v", payload)
	}
	if cookieHeader := resp.Header.Get("Set-Cookie"); cookieHeader != "" {
		t.Fatalf("entry API must not set a Gateway cookie: %s", cookieHeader)
	}
	if body := string(resp.Body()); strings.Contains(body, "10.0.0.2") || strings.Contains(body, "webToken") {
		t.Fatalf("response leaked internal runtime details: %s", body)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "boss" {
		t.Fatalf("reader calls=%v", reader.calls)
	}
}

// TestKaiwuBossEntry_AuthorizationAndAvailability 覆盖角色、root 账号精确
// 匹配、状态、用户 ID、凭据类型、scope 和依赖不可用场景。
func TestKaiwuBossEntry_AuthorizationAndAvailability(t *testing.T) {
	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	validUser := ports.PlatformUserAdmin{ID: userID, Username: "local:root", Role: "platform-admin", Status: "active"}
	validReader := &fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"}
	tests := []struct {
		name              string
		identity          kaiwuTestIdentity
		platformUserStore ports.PlatformUserAdminStore
		reader            KaiwuRuntimeReader
		publicEntry       KaiwuPublicEntryConfig
		expectedStatus    int
		expectedCode      string
	}{
		{
			name:     "platform operations role",
			identity: kaiwuTestIdentity{userID: userID.String(), scope: "platform", principalKind: "user"},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: ports.PlatformUserAdmin{
				ID: userID, Role: "platform-ops", Status: "active",
			}},
			reader:         validReader,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_BOSS_ROLE_REQUIRED",
		},
		{
			name:     "non-root platform admin",
			identity: kaiwuTestIdentity{userID: userID.String(), scope: "platform", principalKind: "user"},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: ports.PlatformUserAdmin{
				ID: userID, Username: "local:ops-lead", Role: "platform-admin", Status: "active",
			}},
			reader:         validReader,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_BOSS_ROLE_REQUIRED",
		},
		{
			name:     "disabled platform user",
			identity: kaiwuTestIdentity{userID: userID.String(), scope: "platform", principalKind: "user"},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: ports.PlatformUserAdmin{
				ID: userID, Username: "local:root", Role: "platform-admin", Status: "disabled",
			}},
			reader:         validReader,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "KAIWU_PLATFORM_USER_NOT_ACTIVE",
		},
		{
			name: "invalid user id",
			identity: kaiwuTestIdentity{
				userID: "not-a-uuid", scope: "platform", principalKind: "user",
			},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: validUser},
			reader:            validReader,
			expectedStatus:    http.StatusForbidden,
			expectedCode:      "KAIWU_BOSS_ROLE_REQUIRED",
		},
		{
			name: "tenant scope",
			identity: kaiwuTestIdentity{
				userID: userID.String(), scope: "tenant", principalKind: "user",
			},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: validUser},
			reader:            validReader,
			expectedStatus:    http.StatusForbidden,
			expectedCode:      "KAIWU_BOSS_ROLE_REQUIRED",
		},
		{
			name: "api key credential",
			identity: kaiwuTestIdentity{
				userID: userID.String(), scope: "platform",
				principalKind: "service", credentialScheme: "api-key",
			},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: validUser},
			reader:            validReader,
			expectedStatus:    http.StatusForbidden,
			expectedCode:      "KAIWU_USER_CREDENTIAL_REQUIRED",
		},
		{
			name: "platform user store unavailable",
			identity: kaiwuTestIdentity{
				userID: userID.String(), scope: "platform", principalKind: "user",
			},
			reader:         validReader,
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "KAIWU_BACKEND_UNAVAILABLE",
		},
		{
			name: "boss origin not configured",
			identity: kaiwuTestIdentity{
				userID: userID.String(), scope: "platform", principalKind: "user",
			},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: validUser},
			reader:            validReader,
			publicEntry:       KaiwuPublicEntryConfig{ConsoleURL: "http://kaiwu-console.test:30088"},
			expectedStatus:    http.StatusServiceUnavailable,
			expectedCode:      "KAIWU_BACKEND_UNAVAILABLE",
		},
		{
			name: "runtime unavailable",
			identity: kaiwuTestIdentity{
				userID: userID.String(), scope: "platform", principalKind: "user",
			},
			platformUserStore: &fakeKaiwuPlatformUserStore{user: validUser},
			expectedStatus:    http.StatusServiceUnavailable,
			expectedCode:      "KAIWU_BACKEND_UNAVAILABLE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicEntry := tt.publicEntry
			if publicEntry.BossURL == "" && tt.name != "boss origin not configured" {
				publicEntry = testKaiwuPublicEntry()
			}
			h := setupKaiwuEntryTestServer(tt.identity, tt.reader, nil, tt.platformUserStore, publicEntry)
			resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/boss/entry")
			if resp.StatusCode() != tt.expectedStatus {
				t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
			}
			if payload := decodeKaiwuJSON(t, resp); payload["code"] != tt.expectedCode {
				t.Fatalf("code=%v body=%s", payload["code"], string(resp.Body()))
			}
		})
	}
}

// TestKaiwuEntry_RuntimeValidation 验证畸形内部目标会被拒绝，且内部
// reader 错误不会返回给客户端。
func TestKaiwuEntry_RuntimeValidation(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	tenantService := &fakeKaiwuTenantService{tenant: ports.Tenant{
		ID: tenantID, Name: "tenant-a", Status: ports.TenantStatusActive,
	}}
	identity := kaiwuTestIdentity{
		tenantID: tenantID, userID: tenantID, scope: "tenant", principalKind: "user",
	}
	tests := []struct {
		name     string
		target   *url.URL
		webToken string
		err      error
	}{
		{name: "valid target", target: runtimeTestTarget(), webToken: "token"},
		{name: "nil target", webToken: "token"},
		{name: "empty token", target: runtimeTestTarget()},
		{name: "https target", target: &url.URL{Scheme: "https", Host: "10.0.0.3:3080"}, webToken: "token"},
		{
			name: "target with userinfo",
			target: &url.URL{
				Scheme: "http", Host: "10.0.0.3:3080", User: url.UserPassword("user", "password"),
			},
			webToken: "token",
		},
		{name: "target with path", target: &url.URL{Scheme: "http", Host: "10.0.0.3:3080", Path: "/internal"}, webToken: "token"},
		{name: "target with query", target: &url.URL{Scheme: "http", Host: "10.0.0.3:3080", RawQuery: "token=secret"}, webToken: "token"},
		{
			name: "reader error", target: runtimeTestTarget(), webToken: "token",
			err: errors.New("secret internal kubernetes failure"),
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeKaiwuRuntimeReader{target: tt.target, webToken: tt.webToken, err: tt.err}
			h := setupKaiwuEntryTestServer(identity, reader, tenantService, nil, testKaiwuPublicEntry())
			resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/console/entry")
			expectedStatus := http.StatusOK
			if index != 0 {
				expectedStatus = http.StatusServiceUnavailable
			}
			if resp.StatusCode() != expectedStatus {
				t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
			}
			body := string(resp.Body())
			if strings.Contains(body, "10.0.0.3") || strings.Contains(body, "secret internal kubernetes failure") {
				t.Fatalf("response leaked internal runtime details: %s", body)
			}
		})
	}
}

// TestKaiwuEntry_PublicOriginTokenTTL 验证 TTL 覆盖生效，且 token 经过 URL
// 转义后拼进入口地址。
func TestKaiwuEntry_PublicOriginTokenTTL(t *testing.T) {
	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	h := setupKaiwuEntryTestServer(
		kaiwuTestIdentity{userID: userID.String(), scope: "platform", principalKind: "user"},
		&fakeKaiwuRuntimeReader{
			target:   &url.URL{Scheme: "http", Host: "10.0.0.2:3080"},
			webToken: "boss token/with+special",
		},
		nil,
		&fakeKaiwuPlatformUserStore{user: ports.PlatformUserAdmin{ID: userID, Username: "local:root", Role: "platform-admin", Status: "active"}},
		KaiwuPublicEntryConfig{BossURL: "https://kaiwu.example.com", TokenTTL: 300 * time.Second},
	)

	resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/boss/entry")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	payload := decodeKaiwuJSON(t, resp)
	if payload["client"] != "boss" ||
		payload["entry_url"] != "https://kaiwu.example.com/?token=boss+token%2Fwith%2Bspecial" ||
		payload["expires_in"] != float64(300) {
		t.Fatalf("payload=%v", payload)
	}
}

// TestKaiwuEntry_PublicOriginKeepsAuthorization 验证独占 origin 模式不会
// 跳过租户授权检查。
func TestKaiwuEntry_PublicOriginKeepsAuthorization(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	h := setupKaiwuEntryTestServer(
		kaiwuTestIdentity{
			tenantID:      tenantID,
			userID:        tenantID,
			scope:         "tenant",
			principalKind: "user",
		},
		&fakeKaiwuRuntimeReader{target: runtimeTestTarget(), webToken: "token"},
		&fakeKaiwuTenantService{tenant: ports.Tenant{ID: tenantID, Name: "other-tenant", Status: ports.TenantStatusActive}},
		nil,
		testKaiwuPublicEntry(),
	)

	resp := performKaiwuEntryRequest(h, "/api/v1/svc/integrations/kaiwu/console/entry")
	if resp.StatusCode() != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	if payload := decodeKaiwuJSON(t, resp); payload["code"] != "KAIWU_CONSOLE_TENANT_NOT_ALLOWED" {
		t.Fatalf("code=%v", payload["code"])
	}
}

// runtimeTestTarget 返回 handler 测试使用的干净内部 HTTP 目标。
func runtimeTestTarget() *url.URL {
	return &url.URL{Scheme: "http", Host: "10.0.0.4:3080"}
}
