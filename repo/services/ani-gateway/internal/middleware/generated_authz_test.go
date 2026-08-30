package middleware

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// TestInstallGeneratedPrincipalContext 冻结 C3 的 identity 投影契约：
// tenant 主体安装 Hertz 字段 + 无 legacy roles 的 types.TenantContext；
// platform 主体只设 platform scope，不注入零 UUID tenant context。
func TestInstallGeneratedPrincipalContext(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	tenantRequest := &app.RequestContext{}
	tenantCtx, err := InstallGeneratedPrincipalContext(
		context.Background(), tenantRequest, authz.Principal{
			Kind:             authz.PrincipalUser,
			CredentialScheme: authz.CredentialBearer,
			CredentialDomain: authz.DomainTenant,
			TenantID:         tenantID.String(),
			SubjectID:        userID.String(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	projected, ok := types.TryFromContext(tenantCtx)
	if !ok || projected.TenantID != tenantID || projected.UserID != userID || len(projected.Roles) != 0 {
		t.Fatalf("tenant projection = %#v, present=%v", projected, ok)
	}
	if got := tenantRequest.GetString("tenant_id"); got != tenantID.String() {
		t.Fatalf("tenant_id = %q", got)
	}
	if got := tenantRequest.GetString("user_id"); got != userID.String() {
		t.Fatalf("user_id = %q", got)
	}
	if got := tenantRequest.GetString("scope"); got != "tenant" {
		t.Fatalf("scope = %q", got)
	}

	platformRequest := &app.RequestContext{}
	platformCtx, err := InstallGeneratedPrincipalContext(
		context.Background(), platformRequest, authz.Principal{
			Kind:             authz.PrincipalUser,
			CredentialScheme: authz.CredentialBearer,
			CredentialDomain: authz.DomainPlatform,
			SubjectID:        userID.String(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := types.TryFromContext(platformCtx); ok {
		t.Fatal("platform principal must not install tenant context")
	}
	if got := platformRequest.GetString("tenant_id"); got != "" {
		t.Fatalf("platform tenant_id = %q", got)
	}
	if got := platformRequest.GetString("scope"); got != "platform" {
		t.Fatalf("scope = %q", got)
	}
}
