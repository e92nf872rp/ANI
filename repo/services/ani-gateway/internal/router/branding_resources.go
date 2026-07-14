package router

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type brandingAPI struct {
	service ports.BrandingService
}

func registerBranding(v1 *route.RouterGroup) {
	registerBrandingWithService(v1, nil)
}

func registerBrandingWithService(v1 *route.RouterGroup, service ports.BrandingService) {
	api := newBrandingAPI(service)
	v1.GET("/branding", api.getBranding)
	v1.PUT("/branding", api.updateBranding)
	v1.POST("/branding/logo", api.uploadBrandingLogo)
}

func newBrandingAPI(service ports.BrandingService) *brandingAPI {
	if service == nil {
		service = runtimeadapter.NewLocalBrandingService()
	}
	return &brandingAPI{service: service}
}

func (api *brandingAPI) getBranding(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetBranding(ctx)
	if err != nil {
		writeBrandingError(c, err)
		return
	}
	c.JSON(http.StatusOK, brandingResponseFromRecord(record))
}

func (api *brandingAPI) updateBranding(ctx context.Context, c *app.RequestContext) {
	_ = middleware.GetTenantID(c)
	var req brandingUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid branding request")
		return
	}
	record, err := api.service.UpdateBranding(ctx, ports.BrandingUpdateRequest{
		IdempotencyKey: req.IdempotencyKey,
		PlatformName:   req.PlatformName,
		LogoLightURL:   req.LogoLightURL,
		LogoDarkURL:    req.LogoDarkURL,
		FaviconURL:     req.FaviconURL,
		PrimaryColor:   req.PrimaryColor,
		SecondaryColor: req.SecondaryColor,
		ICPNumber:      req.ICPNumber,
	})
	if err != nil {
		writeBrandingError(c, err)
		return
	}
	c.JSON(http.StatusOK, brandingResponseFromRecord(record))
}

type brandingUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	PlatformName   string `json:"platform_name"`
	LogoLightURL   string `json:"logo_light_url"`
	LogoDarkURL    string `json:"logo_dark_url"`
	FaviconURL     string `json:"favicon_url"`
	PrimaryColor   string `json:"primary_color"`
	SecondaryColor string `json:"secondary_color"`
	ICPNumber      string `json:"icp_number"`
}

func (api *brandingAPI) uploadBrandingLogo(ctx context.Context, c *app.RequestContext) {
	_ = middleware.GetTenantID(c)
	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader == nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "logo file is required")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid logo file")
		return
	}
	defer file.Close()
	variant := strings.TrimSpace(string(c.FormValue("variant")))
	record, err := api.service.UploadBrandingLogo(ctx, ports.BrandingLogoUploadRequest{
		IdempotencyKey: strings.TrimSpace(string(c.FormValue("idempotency_key"))),
		Variant:        variant,
		ContentType:    fileHeader.Header.Get("Content-Type"),
		Body:           file,
		SizeBytes:      fileHeader.Size,
	})
	if err != nil {
		writeBrandingError(c, err)
		return
	}
	objectURL := brandingURLForVariant(record, variant)
	c.JSON(http.StatusOK, map[string]any{
		"platform_name":  record.PlatformName,
		"logo_light_url": record.LogoLightURL,
		"logo_dark_url":  record.LogoDarkURL,
		"favicon_url":    record.FaviconURL,
		"variant":        normalizeBrandingVariantResponse(variant),
		"object_url":     objectURL,
	})
}

func brandingURLForVariant(record ports.BrandingRecord, variant string) string {
	switch normalizeBrandingVariantResponse(variant) {
	case "dark":
		return record.LogoDarkURL
	case "favicon":
		return record.FaviconURL
	default:
		return record.LogoLightURL
	}
}

func normalizeBrandingVariantResponse(variant string) string {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "dark":
		return "dark"
	case "favicon":
		return "favicon"
	default:
		return "light"
	}
}

func brandingResponseFromRecord(record ports.BrandingRecord) map[string]any {
	return map[string]any{
		"platform_name":   record.PlatformName,
		"logo_light_url":  record.LogoLightURL,
		"logo_dark_url":   record.LogoDarkURL,
		"favicon_url":     record.FaviconURL,
		"primary_color":   record.PrimaryColor,
		"secondary_color": record.SecondaryColor,
		"icp_number":      record.ICPNumber,
	}
}

func writeBrandingError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "BRANDING_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrNotConfigured):
		writeDemoError(c, http.StatusServiceUnavailable, "BRANDING_OBJECT_STORE_NOT_CONFIGURED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "BRANDING_LOOKUP_FAILED", err.Error())
	}
}
