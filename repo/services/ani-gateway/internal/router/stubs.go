// Package router contains shared stub handler for not-yet-implemented endpoints.
package router

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

func notImplemented(ctx context.Context, c *app.RequestContext) {
	c.JSON(http.StatusNotImplemented, map[string]any{
		"code":       "NOT_IMPLEMENTED",
		"message":    "this endpoint is not yet implemented",
		"request_id": middleware.GetRequestID(c),
	})
	_ = ctx
}

// legacyInferenceStream is the pre-Envoy stream placeholder. Chat completion
// traffic is intentionally not registered on this control-plane gateway.
func legacyInferenceStream(ctx context.Context, c *app.RequestContext) {
	notImplemented(ctx, c)
}
