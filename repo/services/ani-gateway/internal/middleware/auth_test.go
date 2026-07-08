package middleware

import "testing"

func TestAuthPublicPaths(t *testing.T) {
	publicPaths := []string{
		"/health",
		"/ready",
		"/healthz",
		"/readyz",
		"/api/v1/branding",
		"/api/v1/auth/oidc/begin",
		"/api/v1/auth/token",
		"/api/v1/auth/refresh",
		"/api/v1/instances/inst-1/exec/session-1",
	}
	for _, path := range publicPaths {
		if !isPublicPath(path) {
			t.Fatalf("isPublicPath(%q) = false, want true", path)
		}
	}
}

func TestTenantContextFromIDs(t *testing.T) {
	tc, err := tenantContextFromIDs("00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", []string{"tenant-admin"})
	if err != nil {
		t.Fatalf("tenantContextFromIDs() error = %v", err)
	}
	if tc.TenantID.String() != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("tenant id = %s", tc.TenantID)
	}
}

func TestAuthProtectedPaths(t *testing.T) {
	protectedPaths := []string{
		"/api/v1/auth/logout",
		"/api/v1/auth/api-keys",
		"/api/v1/instances",
		"/api/v1/instances/inst-1/exec",
		"/api/v1/instances/inst-1/logs",
	}
	for _, path := range protectedPaths {
		if isPublicPath(path) {
			t.Fatalf("isPublicPath(%q) = true, want false", path)
		}
	}
}
