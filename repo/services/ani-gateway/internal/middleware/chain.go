// Package middleware registers the ANI Gateway middleware chain.
// Execution order: RequestID → AccessLog → TLS → Auth → RBAC → RateLimit → Idempotency → Audit → Route
package middleware

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// Register wires all middleware onto the Hertz server in the correct order.
// 启动前校验 authz 配置；非法组合返回 error，调用方必须在监听前 fail closed。
func Register(h *server.Hertz, store GatewayStore) error {
	if store == nil {
		return errors.New("gateway middleware store is required")
	}
	registry := authz.CoreRegistry()
	cfg, err := authz.ConfigFromEnv()
	if err != nil {
		return err
	}
	// C2：监听前执行带 registry 的完整校验，非法 pilot 组合直接启动失败。
	if err := cfg.Validate(registry); err != nil {
		return err
	}
	registerChain(h, store, NewAuthClientFromEnv(), registry, cfg)
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
