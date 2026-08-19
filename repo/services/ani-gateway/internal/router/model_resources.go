package router

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

var (
	modelServiceClient  ModelServiceClient
	modelPVCClaim       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	errModelStoragePath = errors.New("storage_path must be pvc://<claim>[#/path] for a tenant-local model directory")
)

func registerModels(svc *route.RouterGroup) {
	// Product model HTTP stays on Gateway. Console creates a model, then a
	// version whose storage_path is a tenant-local PVC (pvc://claim#/path),
	// then creates an InferenceService with that model_version_id. HuggingFace
	// and ModelScope import stay 501 until the download worker exists.
	svc.GET("/models", listModels)
	svc.POST("/models", createModel)
	svc.POST("/models/import", importModel)
	svc.GET("/models/:model_id", getModel)
	svc.DELETE("/models/:model_id", deleteModel)
	svc.GET("/models/:model_id/versions", listModelVersions)
	svc.POST("/models/:model_id/versions", createModelVersion)
}

type createModelJSON struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Description    string   `json:"description"`
	Capabilities   []string `json:"capabilities"`
}

type createModelVersionJSON struct {
	IdempotencyKey string `json:"idempotency_key"`
	Version        string `json:"version"`
	Format         string `json:"format"`
	StoragePath    string `json:"storage_path"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	IsEncrypted    bool   `json:"is_encrypted"`
}

func listModels(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	limit, err := parseModelListLimit(string(c.Query("limit")))
	if err != nil {
		writeModelInvalid(c, "limit must be an integer between 1 and 100")
		return
	}
	resp, err := modelServiceClient.ListModels(ctx, tenantID, strings.TrimSpace(string(c.Query("status"))), limit, string(c.Query("cursor")))
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, modelListJSON(resp))
}

func createModel(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	var req createModelJSON
	if err := c.BindJSON(&req); err != nil {
		writeModelInvalid(c, "invalid model request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.DisplayName) == "" {
		writeModelInvalid(c, "idempotency_key, name, and display_name are required")
		return
	}
	created, err := modelServiceClient.CreateModel(ctx, tenantID, &modelv1.CreateModelRequest{
		Name:         strings.TrimSpace(req.Name),
		DisplayName:  strings.TrimSpace(req.DisplayName),
		Description:  strings.TrimSpace(req.Description),
		Capabilities: req.Capabilities,
	})
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, modelJSON(created))
}

func importModel(ctx context.Context, c *app.RequestContext) {
	_ = middleware.GetTenantID(c)
	writeInstanceError(c, http.StatusNotImplemented, "FEATURE_NOT_AVAILABLE", "HuggingFace and ModelScope import is not available yet")
	_ = ctx
}

func getModel(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	got, err := modelServiceClient.GetModel(ctx, tenantID, c.Param("model_id"))
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, modelJSON(got))
}

func deleteModel(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	if _, err := modelServiceClient.DeleteModel(ctx, tenantID, c.Param("model_id")); err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func listModelVersions(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	got, err := modelServiceClient.GetModel(ctx, tenantID, c.Param("model_id"))
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, modelVersionListJSON(got))
}

func createModelVersion(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	var req createModelVersionJSON
	if err := c.BindJSON(&req); err != nil {
		writeModelInvalid(c, "invalid model version request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Version) == "" || strings.TrimSpace(req.Format) == "" || strings.TrimSpace(req.ChecksumSHA256) == "" {
		writeModelInvalid(c, "idempotency_key, version, format, and checksum_sha256 are required")
		return
	}
	if err := validateLocalPVCStoragePath(req.StoragePath); err != nil {
		writeModelInvalid(c, err.Error())
		return
	}
	created, err := modelServiceClient.CreateModelVersion(ctx, tenantID, &modelv1.CreateModelVersionRequest{
		ModelId:        c.Param("model_id"),
		Version:        strings.TrimSpace(req.Version),
		Format:         strings.TrimSpace(req.Format),
		StoragePath:    strings.TrimSpace(req.StoragePath),
		ChecksumSha256: strings.TrimSpace(req.ChecksumSHA256),
		SizeBytes:      req.SizeBytes,
		IsEncrypted:    req.IsEncrypted,
	})
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, modelVersionJSON(created))
}

func requireModelTenant(c *app.RequestContext) (string, bool) {
	tenantID := strings.TrimSpace(middleware.GetTenantID(c))
	if tenantID == "" {
		writeModelUnauthorized(c)
		return "", false
	}
	return tenantID, true
}

func parseModelListLimit(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 20, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errInvalidInferenceLogQuery
	}
	return int32(limit), nil
}

// validateLocalPVCStoragePath is the product local-directory source:
// a PVC that already exists in the tenant namespace, plus an optional
// in-container model path. HuggingFace / ModelScope / hostPath are rejected.
func validateLocalPVCStoragePath(raw string) error {
	path := strings.TrimSpace(raw)
	if path == "" {
		return errModelStoragePath
	}
	if strings.Contains(path, "..") {
		return errModelStoragePath
	}
	rest, ok := strings.CutPrefix(path, "pvc://")
	if !ok {
		return errModelStoragePath
	}
	claim, subpath, found := strings.Cut(rest, "#")
	if !modelPVCClaim.MatchString(strings.TrimSpace(claim)) {
		return errModelStoragePath
	}
	if found {
		subpath = strings.TrimSpace(subpath)
		if subpath == "" || !strings.HasPrefix(subpath, "/") {
			return errModelStoragePath
		}
	}
	return nil
}

func modelJSON(msg *modelv1.Model) map[string]any {
	if msg == nil {
		return map[string]any{}
	}
	capabilities := msg.GetCapabilities()
	if capabilities == nil {
		capabilities = []string{}
	}
	return map[string]any{
		"id":               msg.GetId(),
		"name":             msg.GetName(),
		"display_name":     msg.GetDisplayName(),
		"description":      emptyToNil(msg.GetDescription()),
		"source":           msg.GetSource(),
		"capabilities":     capabilities,
		"status":           msg.GetStatus(),
		"total_size_bytes": msg.GetTotalSizeBytes(),
		"created_at":       timestampJSON(msg.GetCreatedAt()),
		"updated_at":       timestampJSON(msg.GetUpdatedAt()),
		"versions":         modelVersionsJSON(msg.GetVersions()),
	}
}

func modelListJSON(msg *modelv1.ListModelsResponse) map[string]any {
	items := make([]map[string]any, 0)
	nextCursor := any(nil)
	if msg != nil {
		for _, item := range msg.GetModels() {
			items = append(items, modelJSON(item))
		}
		if msg.GetMeta() != nil && strings.TrimSpace(msg.GetMeta().GetNextCursor()) != "" {
			nextCursor = msg.GetMeta().GetNextCursor()
		}
	}
	return map[string]any{"items": items, "next_cursor": nextCursor}
}

func modelVersionListJSON(msg *modelv1.Model) map[string]any {
	return map[string]any{"items": modelVersionsJSON(msg.GetVersions()), "next_cursor": nil}
}

func modelVersionsJSON(versions []*modelv1.ModelVersion) []map[string]any {
	items := make([]map[string]any, 0, len(versions))
	for _, item := range versions {
		if item == nil {
			continue
		}
		items = append(items, modelVersionJSON(item))
	}
	return items
}

func modelVersionJSON(msg *modelv1.ModelVersion) map[string]any {
	if msg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":              msg.GetId(),
		"model_id":        msg.GetModelId(),
		"version":         msg.GetVersion(),
		"format":          msg.GetFormat(),
		"is_encrypted":    msg.GetIsEncrypted(),
		"size_bytes":      msg.GetSizeBytes(),
		"checksum_sha256": emptyToNil(msg.GetChecksumSha256()),
		"storage_path":    msg.GetStoragePath(),
		"created_at":      timestampJSON(msg.GetCreatedAt()),
	}
}
