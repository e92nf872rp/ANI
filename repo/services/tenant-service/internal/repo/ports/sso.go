package ports

import (
	"context"

	"github.com/google/uuid"
)

// SsoConfig 是从 K8s Secret 加载的 OIDC 配置（不写 tenant_auth 表）。
type SsoConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
}

// DiscoveryResult 是 OIDC discovery 测试结果。
type DiscoveryResult struct {
	Success           bool
	DiscoveryDocument map[string]any
	Error             string
}

// SsoConfigLoader 按租户与 provider 加载 SSO 详细配置。
// 实现：internal/repo/adapters/sso/sso_config_loader.go。
type SsoConfigLoader interface {
	Load(ctx context.Context, tenantID uuid.UUID, provider string) (SsoConfig, error)
}

// OidcDiscoveryTester 对给定配置发起 OIDC discovery（不写库不写审计）。
// 实现：internal/repo/adapters/sso/oidc_discovery_tester.go。
type OidcDiscoveryTester interface {
	Test(ctx context.Context, cfg SsoConfig) (DiscoveryResult, error)
}
