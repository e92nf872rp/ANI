package authz

import (
	"testing"
)

func TestNormalizeHertzFullPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/api/v1/tenants/:id", "/api/v1/tenants/{id}"},
		{"/api/v1/admin/tenants/:tenant_id/quota", "/api/v1/admin/tenants/{tenant_id}/quota"},
		{"/api/v1/health", "/api/v1/health"},
		{"/healthz", "/healthz"},
		{"/api/v1/a/:one/b/:two", "/api/v1/a/{one}/b/{two}"},
		// 非参数前缀的冒号保持不变。
		{"/api/v1/branding/logo", "/api/v1/branding/logo"},
	}
	for _, tc := range cases {
		if got := NormalizeHertzFullPath(tc.in); got != tc.want {
			t.Errorf("NormalizeHertzFullPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCoreRegistryBasics(t *testing.T) {
	registry := CoreRegistry()

	// C1 起 listQuotaMeta 迁移为 generated：Bearer OR ApiKey + V2 元数据。
	policy, ok := registry.Lookup("GET", "/api/v1/admin/quota-meta")
	if !ok {
		t.Fatal("GET /api/v1/admin/quota-meta not found")
	}
	if policy.Source != PolicySourceGenerated {
		t.Errorf("source = %q, want generated", policy.Source)
	}
	if policy.OperationID != "listQuotaMeta" {
		t.Errorf("operation id = %q, want listQuotaMeta", policy.OperationID)
	}
	if len(policy.SecurityAlternatives) != 2 {
		t.Fatalf("alternatives = %d, want 2 (Bearer OR ApiKey)", len(policy.SecurityAlternatives))
	}
	if policy.Version != "v1" || policy.Resource != "quota" || policy.Action != "read" {
		t.Errorf("policy meta = %q/%q/%q, want v1/quota/read", policy.Version, policy.Resource, policy.Action)
	}
	if policy.Boundary != BoundaryPlatform {
		t.Errorf("boundary = %q, want platform", policy.Boundary)
	}
	if !policy.AllowsPrincipalKind(PrincipalUser) {
		t.Error("policy should allow principal kind user")
	}

	// 派生 operationId 端点命中。
	refresh, ok := registry.LookupOperation("refreshToken")
	if !ok {
		t.Fatal("operation refreshToken not found")
	}
	if refresh.Method != "POST" || refresh.PathTemplate != "/api/v1/auth/refresh" {
		t.Errorf("refreshToken route = %s %s, want POST /api/v1/auth/refresh", refresh.Method, refresh.PathTemplate)
	}

	// public 端点命中且无 security。
	publicPolicy, ok := registry.Lookup("POST", "/api/v1/auth/password/login")
	if !ok {
		t.Fatal("POST /api/v1/auth/password/login not found")
	}
	if publicPolicy.Source != PolicySourcePublic {
		t.Errorf("source = %q, want public", publicPolicy.Source)
	}
	if len(publicPolicy.SecurityAlternatives) != 0 {
		t.Errorf("public alternatives = %d, want 0", len(publicPolicy.SecurityAlternatives))
	}

	// 未注册路由不命中。
	if _, ok := registry.Lookup("DELETE", "/api/v1/unknown"); ok {
		t.Error("unknown route should not match")
	}
}

func TestNewRegistryRejectsInvalidInput(t *testing.T) {
	if _, err := NewRegistry(map[string]Policy{
		"GET /x": {Method: "GET", PathTemplate: "/x"},
	}); err == nil {
		t.Error("missing operation id should fail")
	}
	if _, err := NewRegistry(map[string]Policy{
		"GET /x":  {OperationID: "op", Method: "GET", PathTemplate: "/x"},
		"POST /x": {OperationID: "op", Method: "POST", PathTemplate: "/x"},
	}); err == nil {
		t.Error("duplicate operation id should fail")
	}
}
