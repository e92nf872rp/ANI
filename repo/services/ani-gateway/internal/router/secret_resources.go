package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type secretAPI struct {
	service ports.SecretService
}

type secretCreateRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Data           map[string]string `json:"data"`
}

type secretBindRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	MountPath      string `json:"mount_path"`
	EnvPrefix      string `json:"env_prefix"`
}

type secretResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Keys       []string               `json:"keys"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

type secretBindingResponse struct {
	ID         string                 `json:"id"`
	SecretID   string                 `json:"secret_id"`
	TenantID   string                 `json:"tenant_id"`
	TargetType string                 `json:"target_type"`
	TargetID   string                 `json:"target_id"`
	MountPath  string                 `json:"mount_path,omitempty"`
	EnvPrefix  string                 `json:"env_prefix,omitempty"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
}

func newSecretAPI() *secretAPI {
	return newSecretAPIWithService(nil)
}

func newSecretAPIWithService(service ports.SecretService) *secretAPI {
	if service == nil {
		service = runtimeadapter.NewLocalSecretService()
	}
	return &secretAPI{service: service}
}

func registerSecretResources(v1 *route.RouterGroup) {
	registerSecretResourcesWithService(v1, nil)
}

func registerSecretResourcesWithService(v1 *route.RouterGroup, service ports.SecretService) {
	api := newSecretAPIWithService(service)
	v1.GET("/secrets", api.listSecrets)
	v1.POST("/secrets", api.createSecret)
	v1.GET("/secrets/:secret_id", api.getSecret)
	v1.DELETE("/secrets/:secret_id", api.deleteSecret)
	v1.POST("/secrets/:secret_id/bindings", api.bindSecret)
}

func (api *secretAPI) createSecret(ctx context.Context, c *app.RequestContext) {
	var req secretCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid secret request")
		return
	}
	rec, err := api.service.CreateSecret(ctx, ports.SecretCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Type:           req.Type,
		Data:           req.Data,
	})
	if err != nil {
		writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusCreated, secretFromRecord(rec))
}

func (api *secretAPI) listSecrets(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListSecrets(ctx, ports.SecretListRequest{TenantID: middleware.GetTenantID(c)})
	if err != nil {
		writeSecretError(c, err)
		return
	}
	items := make([]secretResponse, 0, len(records))
	for _, record := range records {
		items = append(items, secretFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *secretAPI) getSecret(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetSecret(ctx, ports.SecretGetRequest{TenantID: middleware.GetTenantID(c), SecretID: c.Param("secret_id")})
	if err != nil {
		writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusOK, secretFromRecord(rec))
}

func (api *secretAPI) deleteSecret(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.DeleteSecret(ctx, ports.SecretGetRequest{TenantID: middleware.GetTenantID(c), SecretID: c.Param("secret_id")})
	if err != nil {
		writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusOK, secretFromRecord(rec))
}

func (api *secretAPI) bindSecret(ctx context.Context, c *app.RequestContext) {
	var req secretBindRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid secret binding request")
		return
	}
	rec, err := api.service.BindSecret(ctx, ports.SecretBindRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		SecretID:       c.Param("secret_id"),
		TargetType:     req.TargetType,
		TargetID:       req.TargetID,
		MountPath:      req.MountPath,
		EnvPrefix:      req.EnvPrefix,
	})
	if err != nil {
		writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusCreated, secretBindingFromRecord(rec))
}

func secretFromRecord(r ports.SecretRecord) secretResponse {
	devProfile := localCoreDevProfile("local-secret-service", "Core dev/local profile; secret values are stored only in the local adapter and never returned")
	if r.RealProvider && r.Provider == "kubernetes" {
		devProfile = coreDevProfileResponse{
			Mode:         "real",
			Provider:     "kubernetes-secret-provider",
			RealProvider: true,
			Reason:       "Kubernetes Secret has been written; instance environment/file injection is gated separately",
		}
	}
	return secretResponse{
		ID:         r.SecretID,
		TenantID:   r.TenantID,
		Name:       r.Name,
		Type:       r.Type,
		Keys:       r.Keys,
		State:      r.State,
		DevProfile: devProfile,
		CreatedAt:  time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339),
		UpdatedAt:  time.Unix(r.UpdatedAt, 0).UTC().Format(time.RFC3339),
	}
}

func secretBindingFromRecord(r ports.SecretBindingRecord) secretBindingResponse {
	return secretBindingResponse{
		ID:         r.BindingID,
		SecretID:   r.SecretID,
		TenantID:   r.TenantID,
		TargetType: r.TargetType,
		TargetID:   r.TargetID,
		MountPath:  r.MountPath,
		EnvPrefix:  r.EnvPrefix,
		State:      r.State,
		DevProfile: localCoreDevProfile("local-secret-service", "Core dev/local profile; provider Secret injection is gated separately"),
		CreatedAt:  time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339),
	}
}

func writeSecretError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
