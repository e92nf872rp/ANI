// Package middleware registers the ANI Gateway middleware chain.
// Execution order: RequestID → AccessLog → TLS → Auth → RBAC → RateLimit → Idempotency → Audit → Route
package middleware

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// Register wires all middleware onto the Hertz server in the correct order.
// policy 路由恒为契约即开关：ConfigFromEnv 只解析 ANI_AUTH_MODE，无启动校验。
func Register(h *server.Hertz, store GatewayStore) error {
	return RegisterWithTargetIAM(h, store, nil)
}

// RegisterWithTargetIAM installs the target tracer route ahead of the current
// policy chain. A non-nil target client selects the fixed route; once selected,
// TargetIAMAuthorization fails closed and the current AuthService is skipped.
func RegisterWithTargetIAM(h *server.Hertz, store GatewayStore, targetClient ports.TargetIAM) error {
	if store == nil {
		return errors.New("gateway middleware store is required")
	}
	targetRegistry, err := authz.NewTargetOperationRegistry(authz.TargetPolicyRevision)
	if err != nil {
		return err
	}
	registerChainWithTarget(h, store, NewAuthClientFromEnv(), targetClient, authz.CoreRegistry(), targetRegistry, authz.ConfigFromEnv())
	return nil
}

func registerChainWithTarget(
	h *server.Hertz, store GatewayStore, client AuthClient, targetClient ports.TargetIAM,
	registry authz.Registry, targetRegistry authz.TargetOperationRegistry, cfg authz.Config,
) {
	h.Use(
		RequestID(),
		AccessLog(),
		TargetIAMAuthorization(targetClient, targetRegistry),
		ResolveAuthzPolicy(registry, cfg),
		AuthenticatePrincipal(client),
		AuthorizePrincipal(client),
		RateLimit(store),
		Idempotency(store),
		Audit(),
	)
}
