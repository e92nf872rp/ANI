package router

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
)

func registerBranding(v1 *route.RouterGroup) {
	v1.GET("/branding", getBranding)
}

func getBranding(ctx context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, map[string]any{
		"logo_url":      "",
		"primary_color": "#000000",
		"name":          "",
	})
	_ = ctx
}
