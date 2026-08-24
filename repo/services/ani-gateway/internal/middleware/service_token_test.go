package middleware

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// v2CallCounts 记录 tokenStub 的 V2 方法调用数；mode=off 测试断言其始终为零。
type v2CallCounts struct {
	ValidatePrincipal int
	CheckPermissionV2 int
}

type tokenStub struct {
	tenant *commonv1.TenantContext
	err    error
	v2     *v2CallCounts
}

// v2c 返回共享的 V2 调用计数指针（值接收者无法直接自增）。
func (s tokenStub) v2c() *v2CallCounts {
	if s.v2 == nil {
		s.v2 = &v2CallCounts{}
	}
	return s.v2
}

func (s tokenStub) ValidateToken(context.Context, string) (*commonv1.TenantContext, error) {
	return s.tenant, s.err
}
func (s tokenStub) CheckPermission(context.Context, *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
	return &authv1.CheckPermissionResponse{Allowed: true}, nil
}

// ValidatePrincipal 是 V2 stub：B1 mode=off 下不允许被调用，调用即测试失败。
func (s tokenStub) ValidatePrincipal(context.Context, string, authz.CredentialScheme) (*authv1.PrincipalContext, error) {
	s.v2c().ValidatePrincipal++
	return nil, errors.New("V2 ValidatePrincipal must not be called in mode=off")
}

// CheckPermissionV2 是 V2 stub：B1 mode=off 下不允许被调用，调用即测试失败。
func (s tokenStub) CheckPermissionV2(context.Context, *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error) {
	s.v2c().CheckPermissionV2++
	return nil, errors.New("V2 CheckPermissionV2 must not be called in mode=off")
}
func (tokenStub) BeginOIDCLogin(context.Context, *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	return nil, nil
}
func (tokenStub) CompleteOIDCLogin(context.Context, *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}
func (tokenStub) Login(context.Context, *authv1.LoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}
func (tokenStub) PlatformPasswordLogin(context.Context, *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}
func (tokenStub) RefreshToken(context.Context, string) (*authv1.AccessToken, error) {
	return nil, nil
}
func (tokenStub) RevokeToken(context.Context, string) error { return nil }
func (tokenStub) CreateAPIKey(context.Context, *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	return nil, nil
}
func (tokenStub) ListAPIKeys(context.Context, *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	return nil, nil
}
func (tokenStub) RevokeAPIKey(context.Context, *authv1.RevokeAPIKeyRequest) error {
	return nil
}

func TestAuthAcceptsServiceJWTOnPlatformWorkloads(t *testing.T) {
	prevMode := os.Getenv("ANI_AUTH_MODE")
	_ = os.Unsetenv("ANI_AUTH_MODE")
	t.Cleanup(func() {
		if prevMode == "" {
			_ = os.Unsetenv("ANI_AUTH_MODE")
			return
		}
		_ = os.Setenv("ANI_AUTH_MODE", prevMode)
	})

	client := tokenStub{
		tenant: &commonv1.TenantContext{
			TenantId: "11111111-1111-1111-1111-111111111111",
			UserId:   "00000000-0000-0000-0000-0000000000aa",
			Roles:    []string{"service"},
			Scope:    "scope:platform-workloads:write",
		},
		v2: &v2CallCounts{},
	}
	h := server.New()
	h.Use(AuthWithClient(client))
	h.Use(RBACWithClient(client))
	h.POST("/api/v1/platform-workloads", func(ctx context.Context, c *app.RequestContext) {
		if GetPrincipalKind(c) != "service" {
			t.Fatalf("principal = %q", GetPrincipalKind(c))
		}
		if GetServiceScope(c) != "scope:platform-workloads:write" {
			t.Fatalf("service scope = %q", GetServiceScope(c))
		}
		if GetTenantID(c) != "11111111-1111-1111-1111-111111111111" {
			t.Fatalf("tenant = %q", GetTenantID(c))
		}
		c.JSON(http.StatusAccepted, map[string]string{"ok": "true"})
	})

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/platform-workloads", nil,
		ut.Header{Key: "Authorization", Value: "Bearer service-jwt"},
	).Result()
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	// mode=off：legacy 链路不得触发 V2 RPC。
	if client.v2.ValidatePrincipal != 0 || client.v2.CheckPermissionV2 != 0 {
		t.Fatalf("V2 calls in mode=off = %+v, want zero", client.v2)
	}
}

func TestAuthRejectsTenantJWTOnPlatformWorkloads(t *testing.T) {
	prevMode := os.Getenv("ANI_AUTH_MODE")
	_ = os.Unsetenv("ANI_AUTH_MODE")
	t.Cleanup(func() {
		if prevMode == "" {
			_ = os.Unsetenv("ANI_AUTH_MODE")
			return
		}
		_ = os.Setenv("ANI_AUTH_MODE", prevMode)
	})

	client := tokenStub{
		tenant: &commonv1.TenantContext{
			TenantId: "11111111-1111-1111-1111-111111111111",
			UserId:   "22222222-2222-2222-2222-222222222222",
			Roles:    []string{"tenant-admin"},
			Scope:    "tenant",
		},
		v2: &v2CallCounts{},
	}
	h := server.New()
	h.Use(AuthWithClient(client))
	h.POST("/api/v1/platform-workloads", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/platform-workloads", nil,
		ut.Header{Key: "Authorization", Value: "Bearer tenant-jwt"},
	).Result()
	if resp.StatusCode() != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	// mode=off：legacy 链路（含拒绝分支）不得触发 V2 RPC。
	if client.v2.ValidatePrincipal != 0 || client.v2.CheckPermissionV2 != 0 {
		t.Fatalf("V2 calls in mode=off = %+v, want zero", client.v2)
	}
}
