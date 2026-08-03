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
		"/api/v1/auth/password/login",
		"/api/v1/auth/platform/password/login",
	}
	for _, path := range publicPaths {
		if !isPublicPath(path) {
			t.Fatalf("isPublicPath(%q) = false, want true", path)
		}
	}
}

func TestAuthProtectedPaths(t *testing.T) {
	protectedPaths := []string{
		"/api/v1/auth/logout",
		"/api/v1/auth/api-keys",
		"/api/v1/instances",
	}
	for _, path := range protectedPaths {
		if isPublicPath(path) {
			t.Fatalf("isPublicPath(%q) = true, want false", path)
		}
	}
}

// TestPlatformLogin_TenantIsolation verifies the scope whitelist enforced by
// scopeAllowedForPath. Platform tokens (scope=platform) must only reach
// /api/v1/auth/platform/* endpoints; tenant tokens (scope=tenant) must not
// reach platform endpoints. Violations are the basis for 403 FORBIDDEN.
func TestPlatformLogin_TenantIsolation(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		scope     string
		allowedOk bool
	}{
		{"platform token on platform endpoint", "/api/v1/auth/platform/password/login", "platform", true},
		{"platform token on platform sub-path", "/api/v1/auth/platform/users", "platform", true},
		{"tenant token on platform endpoint", "/api/v1/auth/platform/password/login", "tenant", false},
		{"tenant token on tenant endpoint", "/api/v1/auth/password/login", "tenant", true},
		{"tenant token on tenant endpoint (api-keys)", "/api/v1/auth/api-keys", "tenant", true},
		{"platform token on tenant endpoint", "/api/v1/auth/password/login", "platform", false},
		{"platform token on tenant endpoint (api-keys)", "/api/v1/auth/api-keys", "platform", false},
		{"empty scope on platform endpoint (defensive)", "/api/v1/auth/platform/password/login", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scopeAllowedForPath(tc.path, tc.scope)
			if got != tc.allowedOk {
				t.Fatalf("scopeAllowedForPath(%q, %q) = %v, want %v", tc.path, tc.scope, got, tc.allowedOk)
			}
		})
	}
}
