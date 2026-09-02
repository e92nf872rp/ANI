package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

const (
	resolvedPolicyContextKey  = "ani.authz.resolved_policy"
	principalContextKey       = "ani.authz.principal"
	legacyPrincipalContextKey = "ani.authz.legacy_principal"
)

// ResolvedPolicy 是请求级 authz policy 的解析结果：
// policy 是注册表条目，Source 是经 mode 计算后的有效来源。
type ResolvedPolicy struct {
	Policy authz.Policy
	Source authz.PolicySource
}

func SetResolvedPolicy(c *app.RequestContext, resolved ResolvedPolicy) {
	c.Set(resolvedPolicyContextKey, resolved)
}

func GetResolvedPolicy(c *app.RequestContext) (ResolvedPolicy, error) {
	value, ok := c.Get(resolvedPolicyContextKey)
	if !ok {
		return ResolvedPolicy{}, errors.New("resolved policy missing")
	}
	resolved, ok := value.(ResolvedPolicy)
	if !ok {
		return ResolvedPolicy{}, errors.New("resolved policy has invalid type")
	}
	return resolved, nil
}

// IsCorePolicyPath 判断规范化后的 full path 是否属于 Core policy 域。
// 规则与 validate_core_gateway_authz_routes.py 的分类保持一致：
// /healthz、/readyz 是 Core policy path；/health、/ready 是既有 public route 豁免；
// /api/v1/* 且非 /api/v1/svc/*（Services 过渡面）和 /api/v1/demo/*（dev.yaml 演示层）
// 属于 Core policy 域。
func IsCorePolicyPath(path string) bool {
	switch path {
	case "/healthz", "/readyz":
		return true
	case "/health", "/ready":
		return false
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	return !strings.HasPrefix(path, "/api/v1/svc/") &&
		!strings.HasPrefix(path, "/api/v1/demo/")
}

// ResolvePolicyForRequest 解析请求命中的 authz policy，但不触发 c.Next，
// 供测试或主链之外的组合场景复用。
func ResolvePolicyForRequest(registry authz.Registry, cfg authz.Config, c *app.RequestContext) (ResolvedPolicy, error) {
	fullPath := string(c.FullPath())
	if fullPath == "" {
		// 未匹配路由：按 legacy 处理，交由后续 404/认证逻辑收敛。
		return ResolvedPolicy{Source: authz.PolicySourceLegacy}, nil
	}
	normalized := authz.NormalizeHertzFullPath(fullPath)
	if !IsCorePolicyPath(normalized) {
		return ResolvedPolicy{Source: authz.PolicySourceLegacy}, nil
	}
	policy, ok := registry.Lookup(string(c.Method()), normalized)
	if !ok {
		return ResolvedPolicy{}, errors.New("registered route has no authz policy")
	}
	return ResolvedPolicy{Policy: policy, Source: cfg.EffectiveSource(policy)}, nil
}

// ResolveAuthzPolicy 解析请求命中的 authz policy 并写入请求 context。
// 未匹配路由（FullPath 为空）交给 Hertz NoRoute 返回 404；
// Core 注册 route 缺 registry 返回 503，Services/proxy 不受本批次影响。
func ResolveAuthzPolicy(registry authz.Registry, cfg authz.Config) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		fullPath := string(c.FullPath())
		if fullPath == "" {
			// 未匹配路由：标记为 public 放行，交由 Hertz NoRoute 返回 404。
			SetResolvedPolicy(c, ResolvedPolicy{Source: authz.PolicySourcePublic})
			c.Next(ctx)
			return
		}
		resolved, err := ResolvePolicyForRequest(registry, cfg, c)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "AUTHZ_POLICY_MISSING", "registered route has no authz policy")
			return
		}
		SetResolvedPolicy(c, resolved)
		c.Next(ctx)
	}
}

func SetPrincipal(c *app.RequestContext, principal authz.Principal) {
	c.Set(principalContextKey, principal)
}

func GetPrincipal(c *app.RequestContext) (authz.Principal, error) {
	value, ok := c.Get(principalContextKey)
	if !ok {
		return authz.Principal{}, errors.New("principal missing")
	}
	principal, ok := value.(authz.Principal)
	if !ok {
		return authz.Principal{}, errors.New("principal has invalid type")
	}
	return principal, principal.Validate()
}

func SetLegacyPrincipalView(c *app.RequestContext, view authz.LegacyPrincipalView) {
	c.Set(legacyPrincipalContextKey, view)
}

// RequestIdentityKey 返回横切（限流/幂等/审计）使用的稳定身份键：
// 优先规范 Principal，回退 legacy view。
func RequestIdentityKey(c *app.RequestContext) (string, error) {
	if principal, err := GetPrincipal(c); err == nil {
		return principal.IdentityKey()
	}
	value, ok := c.Get(legacyPrincipalContextKey)
	if !ok {
		return "", errors.New("request identity missing")
	}
	view, ok := value.(authz.LegacyPrincipalView)
	if !ok {
		return "", errors.New("legacy principal has invalid type")
	}
	return view.IdentityKey()
}
