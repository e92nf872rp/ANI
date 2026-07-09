package router

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// imageAPI exposes Images (bootable ISO/disk image) routes. CDI/DataVolume
// details live entirely in pkg/adapters/runtime; this handler only maps
// HTTP <-> ports.ImageImportService.
type imageAPI struct {
	service ports.ImageImportService
}

type imageUploadCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Format         string `json:"format"`
	SizeGiB        int64  `json:"size_gib"`
	ContentType    string `json:"content_type,omitempty"`
	StorageClass   string `json:"storage_class,omitempty"`
}

type imageResponse struct {
	ID           string                 `json:"id"`
	TenantID     string                 `json:"tenant_id"`
	Name         string                 `json:"name"`
	Format       string                 `json:"format"`
	SizeGiB      int64                  `json:"size_gib"`
	ContentType  string                 `json:"content_type,omitempty"`
	State        string                 `json:"state"`
	Reason       string                 `json:"reason,omitempty"`
	Message      string                 `json:"message,omitempty"`
	VolumeID     string                 `json:"volume_id,omitempty"`
	StorageClass string                 `json:"storage_class,omitempty"`
	DevProfile   coreDevProfileResponse `json:"dev_profile"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
}

type imageUploadSessionResponse struct {
	Image     imageResponse `json:"image"`
	UploadURL string        `json:"upload_url"`
	Token     string        `json:"token"`
	ExpiresAt string        `json:"expires_at"`
	Method    string        `json:"method"`
}

func newImageAPI() *imageAPI {
	return newImageAPIWithService(nil)
}

func newImageAPIWithService(service ports.ImageImportService) *imageAPI {
	if service == nil {
		service = runtimeadapter.NewLocalImageImportService()
	}
	return &imageAPI{service: service}
}

func registerImageResources(v1 *route.RouterGroup) {
	registerImageResourcesWithService(v1, nil)
}

func registerImageResourcesWithService(v1 *route.RouterGroup, service ports.ImageImportService) {
	api := newImageAPIWithService(service)
	v1.GET("/images", api.listImages)
	v1.POST("/images/uploads", api.createImageUpload)
	v1.GET("/images/:image_id", api.getImage)
	v1.DELETE("/images/:image_id", api.deleteImage)
}

func (api *imageAPI) createImageUpload(ctx context.Context, c *app.RequestContext) {
	var req imageUploadCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid image upload request")
		return
	}
	session, err := api.service.CreateUpload(ctx, ports.ImageUploadCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Format:         ports.ImageFormat(req.Format),
		SizeGiB:        req.SizeGiB,
		ContentType:    req.ContentType,
		StorageClass:   req.StorageClass,
	})
	if err != nil {
		writeImageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, imageUploadSessionFromRecord(session))
}

func (api *imageAPI) listImages(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.List(ctx, ports.ImageListRequest{
		TenantID: middleware.GetTenantID(c),
		Format:   ports.ImageFormat(c.Query("format")),
		State:    ports.ImageState(c.Query("state")),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeImageError(c, err)
		return
	}
	items := make([]imageResponse, 0, len(result.Items))
	for _, record := range result.Items {
		items = append(items, imageFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": result.Total, "next_cursor": nil})
}

func (api *imageAPI) getImage(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.Get(ctx, ports.ImageGetRequest{TenantID: middleware.GetTenantID(c), ImageID: c.Param("image_id")})
	if err != nil {
		writeImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, imageFromRecord(record))
}

func (api *imageAPI) deleteImage(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.Delete(ctx, ports.ImageDeleteRequest{TenantID: middleware.GetTenantID(c), ImageID: c.Param("image_id")})
	if err != nil {
		writeImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, imageFromRecord(record))
}

func imageFromRecord(r ports.ImageRecord) imageResponse {
	return imageResponse{
		ID:           r.ID,
		TenantID:     r.TenantID,
		Name:         r.Name,
		Format:       string(r.Format),
		SizeGiB:      r.SizeGiB,
		ContentType:  r.ContentType,
		State:        string(r.State),
		Reason:       r.Reason,
		Message:      r.Message,
		VolumeID:     r.VolumeID,
		StorageClass: r.StorageClass,
		DevProfile: coreDevProfileResponse{
			Mode:         r.DevProfile.Mode,
			Provider:     r.DevProfile.Provider,
			RealProvider: r.DevProfile.RealProvider,
			Reason:       r.DevProfile.Reason,
		},
		CreatedAt: networkTime(r.CreatedAt),
		UpdatedAt: networkTime(r.UpdatedAt),
	}
}

func imageUploadSessionFromRecord(s ports.ImageUploadSession) imageUploadSessionResponse {
	return imageUploadSessionResponse{
		Image:     imageFromRecord(s.Image),
		UploadURL: s.UploadURL,
		Token:     s.Token,
		ExpiresAt: networkTime(s.ExpiresAt),
		Method:    s.Method,
	}
}

func writeImageError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
