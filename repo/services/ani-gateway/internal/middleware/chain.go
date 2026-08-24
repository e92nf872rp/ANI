// Package middleware registers the ANI Gateway middleware chain.
// Execution order: RequestID → TLS → Auth → RBAC → RateLimit → Idempotency → Audit → Route
package middleware

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// Register wires all middleware onto the Hertz server in the correct order.
// 启动前校验 authz 配置（ValidateBase）；非法组合返回 error，调用方必须在监听前 fail closed。
func Register(h *server.Hertz, store GatewayStore) error {
	if store == nil {
		return errors.New("gateway middleware store is required")
	}
	registry := authz.CoreRegistry()
	cfg, err := authz.ConfigFromEnv()
	if err != nil {
		return err
	}
	registerLegacyCompatibleChain(h, store, NewAuthClientFromEnv(), registry, cfg)
	return nil
}

// registerLegacyCompatibleChain 注册 B0 链路：
// policy resolver → legacy 认证（generated 也按 legacy 调 ValidateToken）→
// legacy 授权（只调旧 CheckPermission）→ 横切（限流/幂等/审计，统一 identity key）。
func registerLegacyCompatibleChain(
	h *server.Hertz, store GatewayStore, client AuthClient,
	registry authz.Registry, cfg authz.Config,
) {
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(registry, cfg),
		AuthWithResolvedPolicy(client),
		RBACWithResolvedPolicy(client),
		RateLimit(store),
		Idempotency(store),
		Audit(),
	)
}
