package router

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

var (
	modelServiceClient  ModelServiceClient
	modelPVCClaim       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	errModelStoragePath = errors.New("storage_path must be pvc://<claim>[#/path] or a tenant-owned object://models path")
)

func registerModels(svc *route.RouterGroup) {
	// Product model HTTP stays on Gateway. Console can create a PVC-backed
	// version directly or request an object-store upload URL and register the
	// verified object path. Remote imports are asynchronous and delegated to
	// model-service; the downloader worker consumes the resulting task later.
	svc.GET("/models", listModels)
	svc.POST("/models", createModel)
	svc.POST("/models/import", importModel)
	svc.GET("/models/:model_id", getModel)
	svc.DELETE("/models/:model_id", deleteModel)
	svc.GET("/models/:model_id/versions", listModelVersions)
	svc.POST("/models/:model_id/versions", createModelVersion)
	svc.POST("/models/:model_id/upload-url", getModelUploadURL)
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

type importModelJSON struct {
	Source         string  `json:"source"`
	RepoID         string  `json:"repo_id"`
	Revision       string  `json:"revision"`
	IdempotencyKey string  `json:"idempotency_key"`
	WebhookURL     string  `json:"webhook_url"`
	Token          *string `json:"token"`
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
	resp, err := modelServiceClient.ListModels(ctx, tenantID, strings.TrimSpace(string(c.Query("status"))), strings.TrimSpace(string(c.Query("source"))), strings.TrimSpace(string(c.Query("capability"))), strings.TrimSpace(string(c.Query("keyword"))), limit, string(c.Query("cursor")))
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
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		Name:           strings.TrimSpace(req.Name),
		DisplayName:    strings.TrimSpace(req.DisplayName),
		Description:    strings.TrimSpace(req.Description),
		Capabilities:   req.Capabilities,
	})
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, modelJSON(created))
}

func importModel(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	var req importModelJSON
	if err := c.BindJSON(&req); err != nil {
		writeModelInvalid(c, "invalid model import request")
		return
	}
	if req.Token != nil {
		// Remote imports are public-only. Credentials must never cross the
		// Gateway/model-service boundary or be persisted by the import task.
		writeModelInvalid(c, "token is not supported for model imports")
		return
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source != "huggingface" && source != "modelscope" {
		writeModelInvalid(c, "source must be huggingface or modelscope")
		return
	}
	repoID := strings.TrimSpace(req.RepoID)
	if repoID == "" {
		writeModelInvalid(c, "repo_id is required")
		return
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		writeModelInvalid(c, "idempotency_key is required")
		return
	}
	revision := strings.TrimSpace(req.Revision)
	if revision == "" {
		revision = "main"
	}
	webhookURL := strings.TrimSpace(req.WebhookURL)
	taskRef, err := modelServiceClient.ImportModel(ctx, tenantID, &modelv1.ImportModelRequest{
		TenantId:       tenantID,
		Source:         source,
		RepoId:         repoID,
		Revision:       revision,
		IdempotencyKey: idempotencyKey,
		WebhookUrl:     webhookURL,
	})
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	if taskRef == nil || strings.TrimSpace(taskRef.GetTaskId()) == "" {
		// A successful import call must always return a pollable task. Treat a
		// malformed dependency response as unavailable rather than emitting a
		// 202 that can never be followed up.
		writeInstanceError(c, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "model dependency returned an invalid task reference")
		return
	}
	taskID := strings.TrimSpace(taskRef.GetTaskId())
	c.Response.Header.Set("Location", "/api/v1/tasks/"+taskID)
	response := map[string]any{
		"task_id":   taskID,
		"task_type": taskRef.GetTaskType(),
		"status":    taskRef.GetStatus(),
	}
	c.JSON(http.StatusAccepted, response)
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
	limit, err := parseModelListLimit(string(c.Query("limit")))
	if err != nil {
		writeModelInvalid(c, "limit must be an integer between 1 and 100")
		return
	}
	got, err := modelServiceClient.ListModelVersions(ctx, tenantID, c.Param("model_id"), limit, string(c.Query("cursor")))
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, modelVersionListResponseJSON(got))
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
	if err := validateModelStoragePath(req.StoragePath, tenantID, c.Param("model_id")); err != nil {
		writeModelInvalid(c, err.Error())
		return
	}
	created, err := modelServiceClient.CreateModelVersion(ctx, tenantID, &modelv1.CreateModelVersionRequest{
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
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

type uploadModelURLJSON struct {
	IdempotencyKey string `json:"idempotency_key"`
	Version        string `json:"version"`
	FileName       string `json:"file_name"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

func getModelUploadURL(ctx context.Context, c *app.RequestContext) {
	if modelServiceClient == nil {
		writeModelUnavailable(c)
		return
	}
	tenantID, ok := requireModelTenant(c)
	if !ok {
		return
	}
	var req uploadModelURLJSON
	if err := c.BindJSON(&req); err != nil {
		writeModelInvalid(c, "invalid upload URL request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Version) == "" || strings.TrimSpace(req.FileName) == "" || req.SizeBytes <= 0 {
		writeModelInvalid(c, "idempotency_key, version, file_name, and positive size_bytes are required")
		return
	}
	got, err := modelServiceClient.GetUploadURL(ctx, tenantID, &modelv1.GetUploadURLRequest{
		TenantId: tenantID, ModelId: c.Param("model_id"), Version: strings.TrimSpace(req.Version), FileName: strings.TrimSpace(req.FileName), SizeBytes: req.SizeBytes, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), ChecksumSha256: strings.TrimSpace(req.ChecksumSHA256),
	})
	if err != nil {
		writeModelGRPCError(c, err)
		return
	}
	response := map[string]any{"upload_url": got.GetUploadUrl(), "storage_path": got.GetStoragePath(), "expires_at": timestampJSON(got.GetExpiresAt())}
	if len(got.GetUploadHeaders()) > 0 {
		response["upload_headers"] = got.GetUploadHeaders()
	}
	c.JSON(http.StatusCreated, response)
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

// validateModelStoragePath accepts either a tenant-local PVC or the canonical
// model-service object path. Gateway validates the tenant/model components so
// an object reference cannot be redirected across tenant boundaries.
func validateModelStoragePath(raw, tenantID, modelID string) error {
	if strings.HasPrefix(strings.TrimSpace(raw), "object://") {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Scheme != "object" || u.Host != "models" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Path, "..") {
			return errModelStoragePath
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != strings.TrimSpace(tenantID) || parts[1] != strings.TrimSpace(modelID) {
			return errModelStoragePath
		}
		if _, err := uuid.Parse(parts[0]); err != nil {
			return errModelStoragePath
		}
		if _, err := uuid.Parse(parts[1]); err != nil {
			return errModelStoragePath
		}
		for _, part := range parts[2:] {
			if strings.TrimSpace(part) == "" || part == "." || part == ".." {
				return errModelStoragePath
			}
		}
		return nil
	}
	return validateLocalPVCStoragePath(raw)
}

// validateLocalPVCStoragePath is the product local-directory source:
// a PVC that already exists in the tenant namespace, plus an optional
// in-container model path.
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

func modelVersionListResponseJSON(msg *modelv1.ListModelVersionsResponse) map[string]any {
	if msg == nil {
		return map[string]any{"items": []map[string]any{}, "next_cursor": nil}
	}
	nextCursor := any(nil)
	if msg.GetMeta() != nil && strings.TrimSpace(msg.GetMeta().GetNextCursor()) != "" {
		nextCursor = msg.GetMeta().GetNextCursor()
	}
	return map[string]any{"items": modelVersionsJSON(msg.GetVersions()), "next_cursor": nextCursor}
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
