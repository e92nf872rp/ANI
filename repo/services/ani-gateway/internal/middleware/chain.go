// Package middleware registers the ANI Gateway middleware chain.
// Execution order: RequestID → TLS → Auth → RBAC → RateLimit → Audit → Route
package middleware

import "github.com/cloudwego/hertz/pkg/app/server"

// Register wires all middleware onto the Hertz server in the correct order.
func Register(h *server.Hertz, store GatewayStore) {
	if store == nil {
		panic("gateway middleware store is required")
	}
	authClient := NewAuthClientFromEnv()
	h.Use(
		RequestID(),
		AuthWithClient(authClient),
		RBACWithClient(authClient),
		RateLimit(store),
		Audit(),
	)
}
