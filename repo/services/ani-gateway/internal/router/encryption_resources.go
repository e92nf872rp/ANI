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

type encryptionAPI struct{ service ports.EncryptionService }
type encryptionCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Algorithm      string `json:"algorithm"`
}
type encryptionRotateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}
type encryptionRevokeRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}
type encryptionResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Algorithm  string                 `json:"algorithm"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}
type encryptionRotationResponse struct {
	RotationID    string                 `json:"rotation_id"`
	TenantID      string                 `json:"tenant_id"`
	PreviousKeyID string                 `json:"previous_key_id"`
	RotatedKey    encryptionResponse     `json:"rotated_key"`
	RotatedAt     string                 `json:"rotated_at"`
	DevProfile    coreDevProfileResponse `json:"dev_profile"`
}
type encryptionSealRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	KeyID          string `json:"key_id"`
	ObjectURI      string `json:"object_uri"`
}
type encryptionUnsealTokenRequest struct {
	IdempotencyKey  string `json:"idempotency_key"`
	KeyID           string `json:"key_id"`
	SealedObjectURI string `json:"sealed_object_uri"`
}
type encryptionSealResponse struct {
	KeyID           string                 `json:"key_id"`
	TenantID        string                 `json:"tenant_id"`
	ObjectURI       string                 `json:"object_uri"`
	SealedObjectURI string                 `json:"sealed_object_uri"`
	UnsealToken     string                 `json:"unseal_token"`
	ExpiresAt       string                 `json:"expires_at"`
	DevProfile      coreDevProfileResponse `json:"dev_profile"`
}
type encryptionUnsealTokenResponse struct {
	KeyID           string                 `json:"key_id"`
	TenantID        string                 `json:"tenant_id"`
	SealedObjectURI string                 `json:"sealed_object_uri"`
	UnsealToken     string                 `json:"unseal_token"`
	ExpiresAt       string                 `json:"expires_at"`
	DevProfile      coreDevProfileResponse `json:"dev_profile"`
}

func newEncryptionAPI() *encryptionAPI {
	return newEncryptionAPIWithService(nil)
}

func newEncryptionAPIWithService(service ports.EncryptionService) *encryptionAPI {
	if service == nil {
		service = runtimeadapter.NewLocalEncryptionService()
	}
	return &encryptionAPI{service: service}
}
func registerEncryptionResources(v1 *route.RouterGroup) {
	registerEncryptionResourcesWithService(v1, nil)
}

func registerEncryptionResourcesWithService(v1 *route.RouterGroup, service ports.EncryptionService) {
	api := newEncryptionAPIWithService(service)
	v1.GET("/encryption/keys", api.listKeys)
	v1.POST("/encryption/keys", api.createKey)
	v1.GET("/encryption/keys/:key_id", api.getKey)
	v1.DELETE("/encryption/keys/:key_id", api.deleteKey)
	v1.POST("/encryption/keys/:key_id/rotate", api.rotateKey)
	v1.POST("/encryption/keys/:key_id/revoke", api.revokeKey)
	v1.POST("/encryption/seal", api.seal)
	v1.POST("/encryption/unseal-token", api.createUnsealToken)
}
func (api *encryptionAPI) createKey(ctx context.Context, c *app.RequestContext) {
	var req encryptionCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid encryption key request")
		return
	}
	rec, err := api.service.CreateKey(ctx, ports.EncryptionKeyCreateRequest{TenantID: middleware.GetTenantID(c), IdempotencyKey: req.IdempotencyKey, Name: req.Name, Algorithm: req.Algorithm})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusCreated, encryptionFromRecord(rec))
}
func (api *encryptionAPI) listKeys(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListKeys(ctx, ports.EncryptionKeyListRequest{TenantID: middleware.GetTenantID(c)})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	items := make([]encryptionResponse, 0, len(recs))
	for _, r := range recs {
		items = append(items, encryptionFromRecord(r))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}
func (api *encryptionAPI) getKey(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetKey(ctx, ports.EncryptionKeyGetRequest{TenantID: middleware.GetTenantID(c), KeyID: c.Param("key_id")})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionFromRecord(rec))
}
func (api *encryptionAPI) deleteKey(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.DeleteKey(ctx, ports.EncryptionKeyGetRequest{TenantID: middleware.GetTenantID(c), KeyID: c.Param("key_id")})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionFromRecord(rec))
}
func (api *encryptionAPI) rotateKey(ctx context.Context, c *app.RequestContext) {
	var req encryptionRotateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid encryption key rotation request")
		return
	}
	rec, err := api.service.RotateKey(ctx, ports.EncryptionKeyRotateRequest{TenantID: middleware.GetTenantID(c), KeyID: c.Param("key_id"), IdempotencyKey: req.IdempotencyKey})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionRotationFromRecord(rec))
}
func (api *encryptionAPI) revokeKey(ctx context.Context, c *app.RequestContext) {
	var req encryptionRevokeRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid encryption key revoke request")
		return
	}
	rec, err := api.service.RevokeKey(ctx, ports.EncryptionKeyRevokeRequest{TenantID: middleware.GetTenantID(c), KeyID: c.Param("key_id"), IdempotencyKey: req.IdempotencyKey, Reason: req.Reason})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionFromRecord(rec))
}
func (api *encryptionAPI) seal(ctx context.Context, c *app.RequestContext) {
	var req encryptionSealRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid encryption seal request")
		return
	}
	rec, err := api.service.Seal(ctx, ports.EncryptionSealRequest{TenantID: middleware.GetTenantID(c), IdempotencyKey: req.IdempotencyKey, KeyID: req.KeyID, ObjectURI: req.ObjectURI})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionSealFromRecord(rec))
}
func (api *encryptionAPI) createUnsealToken(ctx context.Context, c *app.RequestContext) {
	var req encryptionUnsealTokenRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid encryption unseal-token request")
		return
	}
	rec, err := api.service.CreateUnsealToken(ctx, ports.EncryptionUnsealTokenRequest{TenantID: middleware.GetTenantID(c), IdempotencyKey: req.IdempotencyKey, KeyID: req.KeyID, SealedObjectURI: req.SealedObjectURI})
	if err != nil {
		writeEncryptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, encryptionUnsealTokenFromRecord(rec))
}
func encryptionFromRecord(r ports.EncryptionKeyRecord) encryptionResponse {
	return encryptionResponse{ID: r.KeyID, TenantID: r.TenantID, Name: r.Name, Algorithm: r.Algorithm, State: r.State, DevProfile: encryptionDevProfile(r.RealProvider, r.Provider, "Core dev/local profile; provider KMS integration is deferred"), CreatedAt: time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339), UpdatedAt: time.Unix(r.UpdatedAt, 0).UTC().Format(time.RFC3339)}
}
func encryptionRotationFromRecord(r ports.EncryptionKeyRotationRecord) encryptionRotationResponse {
	return encryptionRotationResponse{RotationID: r.RotationID, TenantID: r.TenantID, PreviousKeyID: r.PreviousKey.KeyID, RotatedKey: encryptionFromRecord(r.RotatedKey), RotatedAt: time.Unix(r.RotatedAt, 0).UTC().Format(time.RFC3339), DevProfile: encryptionDevProfile(r.RotatedKey.RealProvider, r.RotatedKey.Provider, "Core dev/local profile; key rotation is simulated")}
}
func encryptionSealFromRecord(r ports.EncryptionSealRecord) encryptionSealResponse {
	return encryptionSealResponse{KeyID: r.KeyID, TenantID: r.TenantID, ObjectURI: r.ObjectURI, SealedObjectURI: r.SealedObjectURI, UnsealToken: r.UnsealToken, ExpiresAt: time.Unix(r.ExpiresAt, 0).UTC().Format(time.RFC3339), DevProfile: encryptionDevProfile(r.RealProvider, r.Provider, "Core dev/local profile; seal/unseal token is simulated")}
}
func encryptionUnsealTokenFromRecord(r ports.EncryptionUnsealTokenRecord) encryptionUnsealTokenResponse {
	return encryptionUnsealTokenResponse{KeyID: r.KeyID, TenantID: r.TenantID, SealedObjectURI: r.SealedObjectURI, UnsealToken: r.UnsealToken, ExpiresAt: time.Unix(r.ExpiresAt, 0).UTC().Format(time.RFC3339), DevProfile: encryptionDevProfile(r.RealProvider, r.Provider, "Core dev/local profile; seal/unseal token is simulated")}
}

func encryptionDevProfile(realProvider bool, provider string, localReason string) coreDevProfileResponse {
	if realProvider && provider == "kms-sm4" {
		return coreDevProfileResponse{
			Mode:         "real",
			Provider:     "kms-sm4-provider",
			RealProvider: true,
			Reason:       "KMS/SM4 provider handled key material or seal operation; live KMS validation is gated separately",
		}
	}
	return localCoreDevProfile("local-encryption-service", localReason)
}
func writeEncryptionError(c *app.RequestContext, err error) {
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
