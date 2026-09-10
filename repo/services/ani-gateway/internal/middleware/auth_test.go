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
		{"service scope on platform-workloads", "/api/v1/platform-workloads", "scope:platform-workloads:write", true},
		{"service read scope on capabilities", "/api/v1/platform-workload-capabilities", "scope:platform-workloads:read", true},
		{"tenant scope on platform-workloads", "/api/v1/platform-workloads", "tenant", false},
		{"platform scope on platform-workloads", "/api/v1/platform-workloads", "platform", false},
		{"service scope on tenant endpoint", "/api/v1/instances", "scope:platform-workloads:write", false},
		// Services 层路由：platform 和 tenant 均允许（角色级 RBAC 由 rbac.go 校验）
		{"platform token on svc endpoint", "/api/v1/svc/tenant-plans", "platform", true},
		{"platform token on svc tenant-admins", "/api/v1/svc/tenant-admins", "platform", true},
		{"tenant token on svc endpoint", "/api/v1/svc/tenant-plans", "tenant", true},
		{"sandbox token on svc endpoint", "/api/v1/svc/tenant-plans", "sandbox", false},
		{"platform token on admin endpoint", "/api/v1/admin/tenants/123", "platform", true},
		{"tenant token on admin endpoint", "/api/v1/admin/tenants/123", "tenant", false},
		// 集群级 GPU 资源目录：platform（BOSS）和 tenant 均允许（角色级 RBAC 由 rbac.go 校验）
		{"platform token on gpu-specs", "/api/v1/gpu-specs", "platform", true},
		{"platform token on gpu-specs detail", "/api/v1/gpu-specs/rtx4090-quarter", "platform", true},
		{"platform token on gpu-specs availability", "/api/v1/gpu-specs/availability", "platform", true},
		{"platform token on gpu-specs create", "/api/v1/gpu-specs", "platform", true},
		{"tenant token on gpu-specs", "/api/v1/gpu-specs", "tenant", true},
		{"platform token on gpu-inventory", "/api/v1/gpu-inventory", "platform", true},
		{"platform token on gpu-inventory occupancy", "/api/v1/gpu-inventory/occupancy", "platform", true},
		{"tenant token on gpu-inventory", "/api/v1/gpu-inventory", "tenant", true},
		// POST /gpu-inventory/gpu-partitions 是 BOSS 专属集群切分操作，仅 platform；
		// 精确匹配必须压过 gpu-inventory 前缀的双域规则，tenant 不得放行
		{"platform token on gpu-partitions", "/api/v1/gpu-inventory/gpu-partitions", "platform", true},
		{"tenant token on gpu-partitions denied", "/api/v1/gpu-inventory/gpu-partitions", "tenant", false},
		{"sandbox token on gpu-partitions denied", "/api/v1/gpu-inventory/gpu-partitions", "sandbox", false},
		{"tenant token on gpu-inventory detail stays dual-domain", "/api/v1/gpu-inventory/dev-001", "tenant", true},
		{"sandbox token on gpu-specs", "/api/v1/gpu-specs", "sandbox", false},
		// GPU 调度队列：handler 按 tenant label 过滤，platform 只见平台默认队列，双域放行
		{"platform token on gpu-scheduling queues", "/api/v1/gpu-scheduling/queues", "platform", true},
		{"tenant token on gpu-scheduling queues", "/api/v1/gpu-scheduling/queues", "tenant", true},
		// GET /quotas 是跨租户配额总览（绕过 RLS），仅 platform；租户自查走 /quotas/me
		{"platform token on quotas list", "/api/v1/quotas", "platform", true},
		{"tenant token on quotas list denied", "/api/v1/quotas", "tenant", false},
		{"platform token on quotas/me denied", "/api/v1/quotas/me", "platform", false},
		{"tenant token on quotas/me allowed", "/api/v1/quotas/me", "tenant", true},
		// 异步任务查询：handler 按 token 上下文 tenant_id 隔离，platform（BOSS
		// 提交 gpu_partition 后轮询）与 tenant 双域放行
		{"platform token on tasks get", "/api/v1/tasks/0198c5a2-7b1e-7f3a-9c1d-2e4f6a8b0c1d", "platform", true},
		{"platform token on tasks list", "/api/v1/tasks", "platform", true},
		{"tenant token on tasks list", "/api/v1/tasks", "tenant", true},
		{"sandbox token on tasks denied", "/api/v1/tasks", "sandbox", false},
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
