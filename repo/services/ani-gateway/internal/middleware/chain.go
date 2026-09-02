// Package middleware registers the ANI Gateway middleware chain.
// Execution order: RequestID → AccessLog → TLS → Auth → RBAC → RateLimit → Idempotency → Audit → Route
package middleware

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// Register wires all middleware onto the Hertz server in the correct order.
// policy 路由恒为契约即开关：ConfigFromEnv 只解析 ANI_AUTH_MODE，无启动校验。
func Register(h *server.Hertz, store GatewayStore) error {
	if store == nil {
		return errors.New("gateway middleware store is required")
	}
	registry := authz.CoreRegistry()
	registerChain(h, store, NewAuthClientFromEnv(), registry, authz.ConfigFromEnv())
	return nil
}

// registerChain 注册 C 阶段链路：
// policy resolver → generated/legacy 分流认证 → generated/legacy 分流授权 →
// 横切（限流/幂等/审计，统一 identity key）。
func registerChain(
	h *server.Hertz, store GatewayStore, client AuthClient,
	registry authz.Registry, cfg authz.Config,
) {
	h.Use(
		RequestID(),
		AccessLog(),
		ResolveAuthzPolicy(registry, cfg),
		AuthenticatePrincipal(client),
		AuthorizePrincipal(client),
		RateLimit(store),
		Idempotency(store),
		Audit(),
	)
}
