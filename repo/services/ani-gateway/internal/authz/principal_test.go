package authz

import (
	"strings"
	"testing"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
)

func TestIdentityKeyMatrix(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	user := "22222222-2222-2222-2222-222222222222"
	credential := "33333333-3333-3333-3333-333333333333"

	cases := []struct {
		name      string
		principal Principal
		want      string
	}{
		{
			name:      "tenant user",
			principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user},
			want:      "tenant:" + tenant + ":user:" + user,
		},
		{
			name:      "platform user",
			principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainPlatform, SubjectID: user},
			want:      "platform:user:" + user,
		},
		{
			name:      "tenant service",
			principal: Principal{Kind: PrincipalService, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user},
			want:      "tenant:service:" + user,
		},
		{
			name:      "platform service",
			principal: Principal{Kind: PrincipalService, CredentialScheme: CredentialBearer, CredentialDomain: DomainPlatform, SubjectID: user},
			want:      "platform:service:" + user,
		},
		{
			name:      "api key",
			principal: Principal{Kind: PrincipalAPIKey, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainTenant, TenantID: tenant, CredentialID: credential},
			want:      "tenant:" + tenant + ":api_key:" + credential,
		},
		{
			name:      "sandbox",
			principal: Principal{Kind: PrincipalSandbox, CredentialScheme: CredentialSandboxToken, CredentialDomain: DomainSandbox, TenantID: tenant, SandboxClaims: &SandboxClaims{TenantID: tenant, InstanceID: "instance-1"}},
			want:      "tenant:" + tenant + ":sandbox:instance-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.principal.Validate(); err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
			got, err := tc.principal.IdentityKey()
			if err != nil {
				t.Fatalf("IdentityKey: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IdentityKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrincipalValidateRejectsInvalidMatrix(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	user := "22222222-2222-2222-2222-222222222222"

	cases := []struct {
		name      string
		principal Principal
	}{
		{name: "unknown kind", principal: Principal{Kind: "ghost", CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user}},
		{name: "unknown domain", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: "other", SubjectID: user}},
		{name: "user with api key scheme", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user}},
		{name: "user with sandbox domain", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainSandbox, TenantID: tenant, SubjectID: user}},
		{name: "empty user subject", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant}},
		{name: "zero user subject", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: zeroUUID}},
		{name: "user with credential id", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user, CredentialID: user}},
		{name: "user with legacy roles", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user, LegacyRoles: []string{"admin"}}},
		{name: "platform user with tenant", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainPlatform, TenantID: tenant, SubjectID: user}},
		{name: "tenant user with zero uuid", principal: Principal{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: zeroUUID, SubjectID: user}},
		{name: "service empty subject", principal: Principal{Kind: PrincipalService, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant}},
		{name: "api key missing credential id", principal: Principal{Kind: PrincipalAPIKey, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainTenant, TenantID: tenant}},
		{name: "api key with sandbox claims", principal: Principal{Kind: PrincipalAPIKey, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainTenant, TenantID: tenant, SandboxClaims: &SandboxClaims{TenantID: tenant, InstanceID: "i"}}},
		{name: "api key in platform domain", principal: Principal{Kind: PrincipalAPIKey, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainPlatform, TenantID: tenant}},
		{name: "sandbox nil claims", principal: Principal{Kind: PrincipalSandbox, CredentialScheme: CredentialSandboxToken, CredentialDomain: DomainSandbox, TenantID: tenant}},
		{name: "sandbox claim tenant mismatch", principal: Principal{Kind: PrincipalSandbox, CredentialScheme: CredentialSandboxToken, CredentialDomain: DomainSandbox, TenantID: tenant, SandboxClaims: &SandboxClaims{TenantID: "44444444-4444-4444-4444-444444444444", InstanceID: "i"}}},
		{name: "sandbox empty instance", principal: Principal{Kind: PrincipalSandbox, CredentialScheme: CredentialSandboxToken, CredentialDomain: DomainSandbox, TenantID: tenant, SandboxClaims: &SandboxClaims{TenantID: tenant}}},
		{name: "sandbox with credential id", principal: Principal{Kind: PrincipalSandbox, CredentialScheme: CredentialSandboxToken, CredentialDomain: DomainSandbox, TenantID: tenant, CredentialID: user, SandboxClaims: &SandboxClaims{TenantID: tenant, InstanceID: "i"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.principal.Validate(); err == nil {
				t.Fatal("invalid principal accepted")
			}
			if _, err := tc.principal.IdentityKey(); err == nil {
				t.Fatal("IdentityKey must fail for invalid principal")
			}
		})
	}
}

func TestLegacyViewPlatformZeroUUIDNormalizesEmpty(t *testing.T) {
	view, err := LegacyViewFromTenantContext(newPlatformTenantContext(userUUIDForTest), CredentialBearer)
	if err != nil {
		t.Fatalf("LegacyViewFromTenantContext: %v", err)
	}
	if view.TenantID != "" {
		t.Fatalf("platform view tenant = %q, want empty", view.TenantID)
	}
	key, err := view.IdentityKey()
	if err != nil {
		t.Fatalf("IdentityKey: %v", err)
	}
	want := "platform:user:" + userUUIDForTest
	if key != want {
		t.Fatalf("IdentityKey = %q, want %q", key, want)
	}
}

func TestLegacyViewAPIKeyTenantScopedWithoutRawKey(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	view := LegacyPrincipalView{CredentialScheme: CredentialAPIKey, TenantID: tenant}
	key, err := view.IdentityKey()
	if err != nil {
		t.Fatalf("IdentityKey: %v", err)
	}
	want := "tenant:" + tenant + ":api_key:legacy"
	if key != want {
		t.Fatalf("IdentityKey = %q, want %q", key, want)
	}
	if strings.Contains(key, "raw-key-value") {
		t.Fatal("identity key must not contain raw key material")
	}
}

func TestLegacyViewSandboxRequiresClaims(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	view := LegacyPrincipalView{CredentialScheme: CredentialSandboxToken, TenantID: tenant}
	if _, err := view.IdentityKey(); err == nil {
		t.Fatal("sandbox view without claims: want error")
	}
	view.SandboxClaims = &SandboxClaims{TenantID: tenant, InstanceID: "instance-1"}
	key, err := view.IdentityKey()
	if err != nil {
		t.Fatalf("IdentityKey: %v", err)
	}
	if want := "tenant:" + tenant + ":sandbox:instance-1"; key != want {
		t.Fatalf("IdentityKey = %q, want %q", key, want)
	}
}

func TestLegacyViewBearerInvalidSubjectRejected(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	for _, subject := range []string{"", zeroUUID, "not-a-uuid"} {
		view := LegacyPrincipalView{CredentialScheme: CredentialBearer, TenantID: tenant, SubjectID: subject, Scope: "tenant"}
		if _, err := view.IdentityKey(); err == nil {
			t.Fatalf("subject %q: want error", subject)
		}
	}
}

const userUUIDForTest = "22222222-2222-2222-2222-222222222222"

func newPlatformTenantContext(userID string) *commonv1.TenantContext {
	return &commonv1.TenantContext{TenantId: zeroUUID, UserId: userID, Scope: "platform"}
}

// TestPrincipalProtoRoundTrip 验证 Principal -> Proto -> PrincipalFromProto 的无损往返，
// 以及 nil / 非法 proto 的 fail-closed 行为。
func TestPrincipalProtoRoundTrip(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	user := userUUIDForTest
	credential := "33333333-3333-3333-3333-333333333333"

	cases := []Principal{
		{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainTenant, TenantID: tenant, SubjectID: user},
		{Kind: PrincipalUser, CredentialScheme: CredentialBearer, CredentialDomain: DomainPlatform, SubjectID: user},
		{Kind: PrincipalService, CredentialScheme: CredentialBearer, CredentialDomain: DomainPlatform, SubjectID: "svc-1"},
		{Kind: PrincipalAPIKey, CredentialScheme: CredentialAPIKey, CredentialDomain: DomainTenant, TenantID: tenant, CredentialID: credential},
	}
	for _, principal := range cases {
		pb := principal.Proto()
		got, err := PrincipalFromProto(pb)
		if err != nil {
			t.Fatalf("round trip %v: %v", principal, err)
		}
		if got.Kind != principal.Kind || got.CredentialScheme != principal.CredentialScheme ||
			got.CredentialDomain != principal.CredentialDomain || got.TenantID != principal.TenantID ||
			got.SubjectID != principal.SubjectID || got.CredentialID != principal.CredentialID {
			t.Fatalf("round trip mismatch: got %+v want %+v", got, principal)
		}
	}
}

func TestPrincipalFromProtoFailClosed(t *testing.T) {
	if _, err := PrincipalFromProto(nil); err == nil {
		t.Fatal("nil proto: want error")
	}
	// 未知 kind：结构校验失败。
	if _, err := PrincipalFromProto(&authv1.PrincipalContext{
		PrincipalKind: "ghost", CredentialScheme: "bearer",
		CredentialDomain: "tenant", TenantId: "11111111-1111-1111-1111-111111111111",
		SubjectId: userUUIDForTest,
	}); err == nil {
		t.Fatal("unknown kind proto: want error")
	}
	// platform 域携带 tenant：结构校验失败。
	if _, err := PrincipalFromProto(&authv1.PrincipalContext{
		PrincipalKind: "user", CredentialScheme: "bearer",
		CredentialDomain: "platform", TenantId: "11111111-1111-1111-1111-111111111111",
		SubjectId: userUUIDForTest,
	}); err == nil {
		t.Fatal("platform with tenant proto: want error")
	}
}
