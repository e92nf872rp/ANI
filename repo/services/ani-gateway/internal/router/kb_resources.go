package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
)

// kbInjectedClient / kbInjectedSSEConfig are the KB gRPC client and SSE wiring
// injected by RegisterWithOptions before routes are registered. They are kept
// package-level so the Spec-split contract can register the whole KB surface
// with a single-argument registerKnowledgeBases(svc) call while still allowing
// tests to inject clients directly via registerKnowledgeBasesWithClient.
var (
	kbInjectedClient    KBGRPCClient
	kbInjectedSSEConfig KbSSEConfig
)

// registerKnowledgeBases wires the 18 KB endpoints (SPEC §4.1/§4.3 端点表):
//   - 11 P0 gRPC passthrough endpoints routed to kb-service
//   - 1 SSE streaming query endpoint held by the gateway
//   - 3 P1 endpoints (citations/sessions/permissions) routed to kb-service;
//     kb-service returns UNIMPLEMENTED which maps to HTTP 501.
//   - 3 B2 endpoints (#11 chunks / #17 session messages / #18 session delete)
//
// Dependencies (client / SSE config) are injected via RegisterWithOptions.
func registerKnowledgeBases(svc *route.RouterGroup) {
	registerKnowledgeBasesWithClient(svc, kbInjectedClient, kbInjectedSSEConfig)
}

// registerKnowledgeBasesWithClient wires the 18 KB endpoints using an explicit
// gRPC client and SSE config, so tests can inject fakes directly.
//
// When client is nil the gRPC handlers return 503 UNAVAILABLE so the gateway
// stays up if kb-service is not configured at boot (the P1 citations/sessions/
// permissions handlers return 501 to mirror the pre-B1 UNIMPLEMENTED status).
func registerKnowledgeBasesWithClient(svc *route.RouterGroup, client KBGRPCClient, sseCfg KbSSEConfig) {
	api := &kbAPI{client: client}
	svc.GET("/knowledge-bases", api.listKnowledgeBases)
	svc.POST("/knowledge-bases", api.createKnowledgeBase)
	svc.GET("/knowledge-bases/:kb_id", api.getKnowledgeBase)
	svc.PUT("/knowledge-bases/:kb_id", api.updateKnowledgeBase)
	svc.DELETE("/knowledge-bases/:kb_id", api.deleteKnowledgeBase)
	svc.GET("/knowledge-bases/:kb_id/documents", api.listKnowledgeBaseDocuments)
	svc.POST("/knowledge-bases/:kb_id/documents", api.uploadKnowledgeBaseDocument)
	svc.POST("/knowledge-bases/:kb_id/documents/:doc_id/notify-uploaded", api.notifyDocumentUploaded)
	svc.GET("/knowledge-bases/:kb_id/documents/:doc_id", api.getKnowledgeBaseDocument)
	svc.DELETE("/knowledge-bases/:kb_id/documents/:doc_id", api.deleteKnowledgeBaseDocument)
	svc.POST("/knowledge-bases/:kb_id/query", api.queryKnowledgeBase)
	// SSE streaming query (SPEC §4.3 / US-017): gateway-held, orchestrates
	// rag-engine retrieval + vLLM streaming. Separate endpoint from JSON
	// query to allow clean SDK generation.
	svc.GET("/knowledge-bases/:kb_id/query/stream", streamQueryKnowledgeBaseSSE(sseCfg))
	// P1 endpoints (SPEC §4.1): kb-service returns UNIMPLEMENTED → gateway 501.
	svc.GET("/knowledge-bases/:kb_id/citations", api.listKnowledgeBaseCitations)
	svc.GET("/knowledge-bases/:kb_id/sessions", api.listKnowledgeBaseSessions)
	svc.PUT("/knowledge-bases/:kb_id/permissions", api.updateKnowledgeBasePermissions)
	// B2 endpoints (SPEC §4.3 #11/#17/#18): document chunk listing, session
	// message listing and session deletion, all gRPC passthrough.
	svc.GET("/knowledge-bases/:kb_id/documents/:doc_id/chunks", api.listKnowledgeBaseDocumentChunks)
	svc.GET("/knowledge-bases/:kb_id/sessions/:session_id/messages", api.listKnowledgeBaseSessionMessages)
	svc.DELETE("/knowledge-bases/:kb_id/sessions/:session_id", api.deleteKnowledgeBaseSession)
	// B3 endpoint (SPEC §4.3 #12): reparse a document for a fresh parse run;
	// 202 AsyncTask + route baseline cleanup (issue-048).
	svc.POST("/knowledge-bases/:kb_id/documents/:doc_id/reparse", api.reparseKnowledgeBaseDocument)
}

// kbAPI holds the injected gRPC client. Handlers read the tenant id from the
// Auth middleware (middleware.GetTenantID) and inject it into every gRPC
// request; the client-supplied tenant_id in the JSON body is ignored for
// cross-tenant isolation (SPEC §7.1).
type kbAPI struct {
	client KBGRPCClient
}

// ── request body structs ────────────────────────────────────────────────────

type createKnowledgeBaseRequest struct {
	IdempotencyKey string  `json:"idempotency_key"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	EmbeddingModel string  `json:"embedding_model"`
	ChunkSize      int32   `json:"chunk_size"`
	TopK           int32   `json:"top_k"`
	ScoreThreshold float32 `json:"score_threshold"`
	RetrievalMode  string  `json:"retrieval_mode"`
}

// updateKnowledgeBaseRequest mirrors UpdateKnowledgeBaseRequest in
// services/v1.yaml (SPEC §4.3 #5): idempotency_key is required; empty
// name/description mean "no change".
type updateKnowledgeBaseRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Description    string `json:"description"`
}

type getDocumentUploadURLRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	FileName       string `json:"file_name"`
	FileType       string `json:"file_type"`
	FileSizeBytes  int64  `json:"file_size_bytes"`
	ChecksumSha256 string `json:"checksum_sha256"`
	CustomMetadata string `json:"custom_metadata"`
}

type queryKnowledgeBaseRequest struct {
	IdempotencyKey       string  `json:"idempotency_key"`
	Question             string  `json:"question"`
	SessionID            string  `json:"session_id"`
	TopK                 int32   `json:"top_k"`
	ScoreThreshold       float32 `json:"score_threshold"`
	InferenceServiceName string  `json:"inference_service_name"`
	RetrievalMode        string  `json:"retrieval_mode"`
}

// reparseKnowledgeBaseDocumentRequest mirrors ReparseDocumentRequest in
// services/v1.yaml (SPEC §4.3 #12): idempotency_key is a required client
// generated uuid for replay safety.
type reparseKnowledgeBaseDocumentRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type updateKBPermissionsRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	PublicRead     bool     `json:"public_read"`
	AllowedUserIDs []string `json:"allowed_user_ids"`
}

// ── 11 P0 handlers (gRPC passthrough) ───────────────────────────────────────

func (a *kbAPI) listKnowledgeBases(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBs(ctx, instanceTenantID(c), int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]knowledgeBaseJSON, 0, len(resp.GetKbs()))
	for _, kb := range resp.GetKbs() {
		items = append(items, kbToJSON(kb))
	}
	nextCursor := ""
	if meta := resp.GetMeta(); meta != nil {
		nextCursor = meta.GetNextCursor()
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"total":       resp.GetMeta().GetTotal(),
		"next_cursor": nextCursor,
	})
}

func (a *kbAPI) createKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req createKnowledgeBaseRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base request")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	kb, err := a.client.CreateKB(ctx, instanceTenantID(c), req.IdempotencyKey, &kbv1.CreateKBRequest{
		Name:           req.Name,
		Description:    req.Description,
		EmbeddingModel: req.EmbeddingModel,
		ChunkSize:      req.ChunkSize,
		TopK:           req.TopK,
		ScoreThreshold: req.ScoreThreshold,
		RetrievalMode:  req.RetrievalMode,
	})
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusCreated, kbToJSON(kb))
}

func (a *kbAPI) getKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	kb, err := a.client.GetKB(ctx, instanceTenantID(c), c.Param("kb_id"))
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbToJSON(kb))
}

// updateKnowledgeBase handles PUT /knowledge-bases/{kb_id} (SPEC §4.3 #5):
// partial update of name/description; empty fields mean "no change". The
// idempotency_key is required so retries replay the first result.
func (a *kbAPI) updateKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req updateKnowledgeBaseRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base update request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	kb, err := a.client.UpdateKB(ctx, instanceTenantID(c), c.Param("kb_id"), req.IdempotencyKey, req.Name, req.Description)
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbToJSON(kb))
}

func (a *kbAPI) deleteKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	if _, err := a.client.DeleteKB(ctx, instanceTenantID(c), c.Param("kb_id")); err != nil {
		writeKBError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *kbAPI) listKnowledgeBaseDocuments(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	parseStatus := string(c.QueryArgs().Peek("parse_status"))
	resp, err := a.client.ListDocuments(ctx, instanceTenantID(c), c.Param("kb_id"), parseStatus, int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]kbDocumentJSON, 0, len(resp.GetDocuments()))
	for _, doc := range resp.GetDocuments() {
		items = append(items, kbDocumentToJSON(doc))
	}
	nextCursor := ""
	if meta := resp.GetMeta(); meta != nil {
		nextCursor = meta.GetNextCursor()
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"total":       resp.GetMeta().GetTotal(),
		"next_cursor": nextCursor,
	})
}

func (a *kbAPI) uploadKnowledgeBaseDocument(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req getDocumentUploadURLRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid document upload request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	if strings.TrimSpace(req.FileName) == "" || strings.TrimSpace(req.FileType) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "file_name and file_type are required")
		return
	}
	resp, err := a.client.GetDocumentUploadURL(ctx, instanceTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.GetDocumentUploadURLRequest{
		FileName:       req.FileName,
		FileType:       req.FileType,
		FileSizeBytes:  req.FileSizeBytes,
		ChecksumSha256: req.ChecksumSha256,
		CustomMetadata: req.CustomMetadata,
	})
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"doc_id":       resp.GetDocId(),
		"upload_url":   resp.GetUploadUrl(),
		"storage_path": resp.GetStoragePath(),
	})
}

func (a *kbAPI) notifyDocumentUploaded(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req struct {
		DocID       string `json:"doc_id"`
		StoragePath string `json:"storage_path"`
	}
	_ = c.BindJSON(&req) // optional body; storage_path may be empty
	storagePath := req.StoragePath
	docID := req.DocID
	if docID == "" {
		docID = c.Param("doc_id")
	}
	taskRef, err := a.client.NotifyDocumentUploaded(ctx, instanceTenantID(c), c.Param("kb_id"), docID, storagePath)
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, asyncTaskRefJSON{
		TaskID:   taskRef.GetTaskId(),
		TaskType: taskRef.GetTaskType(),
		Status:   taskRef.GetStatus(),
	})
}

// asyncTaskRefJSON is the 202 payload shared by notifyDocumentUploaded and
// reparseKnowledgeBaseDocument (AsyncTaskRef passthrough shape).
type asyncTaskRefJSON struct {
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
	Status   string `json:"status"`
}

func (a *kbAPI) deleteKnowledgeBaseDocument(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	if _, err := a.client.DeleteDocument(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("doc_id")); err != nil {
		writeKBError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// getKnowledgeBaseDocument handles GET /knowledge-bases/{kb_id}/documents/{doc_id}
// (SPEC §4.3 #10): returns the document detail as KBDocument JSON.
func (a *kbAPI) getKnowledgeBaseDocument(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	doc, err := a.client.GetDocument(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("doc_id"))
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbDocumentToJSON(doc))
}

func (a *kbAPI) queryKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req queryKnowledgeBaseRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base query request")
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "question is required")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	resp, err := a.client.Query(ctx, instanceTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.QueryRequest{
		Question:             req.Question,
		SessionId:            req.SessionID,
		TopK:                 req.TopK,
		ScoreThreshold:       req.ScoreThreshold,
		InferenceServiceName: req.InferenceServiceName,
		RetrievalMode:        req.RetrievalMode,
	})
	if err != nil {
		writeKBError(c, err)
		return
	}
	sources := make([]sourceChunkJSON, 0, len(resp.GetSources()))
	for _, s := range resp.GetSources() {
		sources = append(sources, sourceChunkToJSON(s))
	}
	c.JSON(http.StatusOK, map[string]any{
		"answer":        resp.GetAnswer(),
		"sources":       sources,
		"session_id":    resp.GetSessionId(),
		"input_tokens":  resp.GetInputTokens(),
		"output_tokens": resp.GetOutputTokens(),
	})
}

// ── 3 P1 handlers (route registered; kb-service returns UNIMPLEMENTED → 501) ─

func (a *kbAPI) listKnowledgeBaseCitations(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		// Without a client we cannot even reach kb-service; return 501 to mirror
		// the P1 UNIMPLEMENTED status so the route surface is consistent.
		writeInstanceError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC ListKBCitations not implemented")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBCitations(ctx, instanceTenantID(c), c.Param("kb_id"), int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]kbCitationJSON, 0, len(resp.GetItems()))
	for _, cit := range resp.GetItems() {
		items = append(items, kbCitationToJSON(cit))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
	})
}

func (a *kbAPI) listKnowledgeBaseSessions(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC ListKBSessions not implemented")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBSessions(ctx, instanceTenantID(c), c.Param("kb_id"), int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]kbSessionJSON, 0, len(resp.GetItems()))
	for _, s := range resp.GetItems() {
		items = append(items, kbSessionToJSON(s))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
	})
}

func (a *kbAPI) updateKnowledgeBasePermissions(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC UpdateKBPermissions not implemented")
		return
	}
	var req updateKBPermissionsRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base permissions request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	kb, err := a.client.UpdateKBPermissions(ctx, instanceTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.UpdateKBPermissionsRequest{
		PublicRead:     req.PublicRead,
		AllowedUserIds: req.AllowedUserIDs,
	})
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbToJSON(kb))
}

// ── B2 handlers (SPEC §4.3 #11/#17/#18) ─────────────────────────────────────

// listKnowledgeBaseDocumentChunks handles GET
// /knowledge-bases/{kb_id}/documents/{doc_id}/chunks (SPEC §4.3 #11):
// cursor-paginated chunk listing with optional chunk_type filter.
// custom_metadata arrives from kb-service as a JSONB string and is unmarshalled
// here so the REST contract exposes it as an object (SPEC §3.2 JSONB 序列化).
func (a *kbAPI) listKnowledgeBaseDocumentChunks(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "limit must be between 1 and 100")
		return
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	chunkType := string(c.QueryArgs().Peek("chunk_type"))
	resp, err := a.client.ListDocumentChunks(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("doc_id"), chunkType, int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]kbChunkJSON, 0, len(resp.GetItems()))
	for _, chunk := range resp.GetItems() {
		item, err := kbChunkToJSON(chunk)
		if err != nil {
			writeInstanceError(c, http.StatusInternalServerError, "INTERNAL", "failed to serialize chunks response")
			return
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
	})
}

// listKnowledgeBaseSessionMessages handles GET
// /knowledge-bases/{kb_id}/sessions/{session_id}/messages (SPEC §4.3 #17):
// cursor-paginated session message listing (created_at ASC).
// sources arrives as a JSONB string (source_chunks) and is unmarshalled here so
// the REST contract exposes it as an array; user messages carry no sources and
// render as null (SPEC §3.2 / §5.4 user 消息 sources 输出 null).
func (a *kbAPI) listKnowledgeBaseSessionMessages(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 100)
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "limit must be between 1 and 100")
		return
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.GetSessionMessages(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("session_id"), int32(limit), cursor)
	if err != nil {
		writeKBError(c, err)
		return
	}
	items := make([]kbSessionMessageJSON, 0, len(resp.GetItems()))
	for _, m := range resp.GetItems() {
		item, err := kbSessionMessageToJSON(m)
		if err != nil {
			writeInstanceError(c, http.StatusInternalServerError, "INTERNAL", "failed to serialize session messages response")
			return
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
	})
}

// deleteKnowledgeBaseSession handles DELETE
// /knowledge-bases/{kb_id}/sessions/{session_id} (SPEC §4.3 #18): idempotent
// hard delete; a missing session still returns 204, only a missing KB yields
// 404 (surfaced by kb-service as NOT_FOUND via writeKBError).
func (a *kbAPI) deleteKnowledgeBaseSession(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	if _, err := a.client.DeleteSession(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("session_id")); err != nil {
		writeKBError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// reparseKnowledgeBaseDocument handles POST
// /knowledge-bases/{kb_id}/documents/{doc_id}/reparse (SPEC §4.3 #12,
// issue-048): re-queues an already-ingested document for a fresh parse run.
// The 202 AsyncTask JSON mirrors notifyDocumentUploaded. Guard errors surface
// from kb-service via writeKBError: doc missing → 404 NOT_FOUND, doc=ready or
// KB=rebuilding → 409 CONFLICT (FAILED_PRECONDITION, SPEC §6.1).
func (a *kbAPI) reparseKnowledgeBaseDocument(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req reparseKnowledgeBaseDocumentRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid reparse request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(req.IdempotencyKey)); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key must be a uuid")
		return
	}
	taskRef, err := a.client.ReparseDocument(ctx, instanceTenantID(c), c.Param("kb_id"), c.Param("doc_id"), req.IdempotencyKey)
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, asyncTaskRefJSON{
		TaskID:   taskRef.GetTaskId(),
		TaskType: taskRef.GetTaskType(),
		Status:   taskRef.GetStatus(),
	})
}

// ── JSON response structs ────────────────────────────────────────────────────
//
// These mirror the services/v1.yaml component schemas so the gateway response
// shape matches the OpenAPI contract that Console/BOSS codegen against.

type knowledgeBaseJSON struct {
	TenantID       string  `json:"tenant_id"`
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	EmbeddingModel string  `json:"embedding_model"`
	ChunkSize      int32   `json:"chunk_size"`
	TopK           int32   `json:"top_k"`
	ScoreThreshold float32 `json:"score_threshold"`
	RetrievalMode  string  `json:"retrieval_mode"`
	Status         string  `json:"status"`
	DocCount       int32   `json:"doc_count"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type kbDocumentJSON struct {
	TenantID       string `json:"tenant_id"`
	KbID           string `json:"kb_id"`
	ID             string `json:"id"`
	FileName       string `json:"file_name"`
	FileType       string `json:"file_type"`
	FileSizeBytes  int64  `json:"file_size_bytes"`
	ParseStatus    string `json:"parse_status"`
	ChunkCount     int32  `json:"chunk_count"`
	ErrorMessage   string `json:"error_message"`
	CustomMetadata string `json:"custom_metadata,omitempty"`
	CreatedAt      string `json:"created_at"`
	ParsedAt       string `json:"parsed_at,omitempty"`
}

type sourceChunkJSON struct {
	DocID    string  `json:"doc_id"`
	FileName string  `json:"file_name"`
	Page     int32   `json:"page"`
	Content  string  `json:"content"`
	Score    float32 `json:"score"`
}

type kbCitationJSON struct {
	ID        string  `json:"id"`
	KbID      string  `json:"kb_id"`
	DocID     string  `json:"doc_id"`
	FileName  string  `json:"file_name"`
	Page      int32   `json:"page"`
	Content   string  `json:"content"`
	Score     float32 `json:"score"`
	CreatedAt string  `json:"created_at"`
	// B2 enhancement (SPEC §4.3 #15): locate the assistant message (and its
	// session) that cited the document. omitempty keeps pre-B2 kb-service
	// responses (empty proto fields) from surfacing empty strings.
	MessageID string `json:"message_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type kbSessionJSON struct {
	ID           string `json:"id"`
	KbID         string `json:"kb_id"`
	MessageCount int32  `json:"message_count"`
	LastQuery    string `json:"last_query"`
	CreatedAt    string `json:"created_at"`
	// last_active_at is nullable in services/v1.yaml (an empty session has
	// no messages): omitempty keeps the proto zero value from surfacing as
	// 1970-01-01T00:00:00Z.
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// kbChunkJSON mirrors the KBChunk schema in services/v1.yaml (SPEC §3.2):
// nullable DB columns surface as omitempty so proto zero values ("" / 0) do
// not leak into responses; custom_metadata is an object, not a JSONB string.
type kbChunkJSON struct {
	ID             string          `json:"id"`
	DocID          string          `json:"doc_id"`
	KbID           string          `json:"kb_id"`
	ParentChunkID  string          `json:"parent_chunk_id,omitempty"`
	ChunkType      string          `json:"chunk_type"`
	Content        string          `json:"content"`
	ParentContent  string          `json:"parent_content,omitempty"`
	PageNumber     int32           `json:"page_number,omitempty"`
	ContentType    string          `json:"content_type,omitempty"`
	FileName       string          `json:"file_name"`
	TokenCount     int32           `json:"token_count,omitempty"`
	CustomMetadata json.RawMessage `json:"custom_metadata,omitempty"`
	CreatedAt      string          `json:"created_at"`
}

// kbSessionMessageJSON mirrors the KBSessionMessage schema in services/v1.yaml
// (SPEC §3.2): source_chunks (DB JSONB column) is exposed as the REST sources
// array; user messages keep it null (contract nullable, no fabricated array).
type kbSessionMessageJSON struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	Sources      json.RawMessage `json:"sources"`
	InputTokens  int32           `json:"input_tokens,omitempty"`
	OutputTokens int32           `json:"output_tokens,omitempty"`
	DurationMs   int64           `json:"duration_ms,omitempty"`
	CreatedAt    string          `json:"created_at"`
}

func kbToJSON(kb *kbv1.KnowledgeBase) knowledgeBaseJSON {
	if kb == nil {
		return knowledgeBaseJSON{}
	}
	return knowledgeBaseJSON{
		TenantID:       kb.GetTenantId(),
		ID:             kb.GetId(),
		Name:           kb.GetName(),
		Description:    kb.GetDescription(),
		EmbeddingModel: kb.GetEmbeddingModel(),
		ChunkSize:      kb.GetChunkSize(),
		TopK:           kb.GetTopK(),
		ScoreThreshold: kb.GetScoreThreshold(),
		RetrievalMode:  kb.GetRetrievalMode(),
		Status:         kb.GetStatus(),
		DocCount:       kb.GetDocCount(),
		CreatedAt:      protoTimestampToRFC3339(kb.GetCreatedAt()),
		UpdatedAt:      protoTimestampToRFC3339(kb.GetUpdatedAt()),
	}
}

func kbDocumentToJSON(doc *kbv1.KBDocument) kbDocumentJSON {
	if doc == nil {
		return kbDocumentJSON{}
	}
	return kbDocumentJSON{
		TenantID:       doc.GetTenantId(),
		KbID:           doc.GetKbId(),
		ID:             doc.GetId(),
		FileName:       doc.GetFileName(),
		FileType:       doc.GetFileType(),
		FileSizeBytes:  doc.GetFileSizeBytes(),
		ParseStatus:    doc.GetParseStatus(),
		ChunkCount:     doc.GetChunkCount(),
		ErrorMessage:   doc.GetErrorMessage(),
		CustomMetadata: doc.GetCustomMetadata(),
		CreatedAt:      protoTimestampToRFC3339(doc.GetCreatedAt()),
		ParsedAt:       protoTimestampToRFC3339(doc.GetParsedAt()),
	}
}

func sourceChunkToJSON(s *kbv1.SourceChunk) sourceChunkJSON {
	if s == nil {
		return sourceChunkJSON{}
	}
	return sourceChunkJSON{
		DocID:    s.GetDocId(),
		FileName: s.GetFileName(),
		Page:     s.GetPage(),
		Content:  s.GetContent(),
		Score:    s.GetScore(),
	}
}

func kbCitationToJSON(c *kbv1.KBCitation) kbCitationJSON {
	if c == nil {
		return kbCitationJSON{}
	}
	return kbCitationJSON{
		ID:        c.GetId(),
		KbID:      c.GetKbId(),
		DocID:     c.GetDocId(),
		FileName:  c.GetFileName(),
		Page:      c.GetPage(),
		Content:   c.GetContent(),
		Score:     c.GetScore(),
		CreatedAt: protoTimestampToRFC3339(c.GetCreatedAt()),
		MessageID: c.GetMessageId(),
		SessionID: c.GetSessionId(),
	}
}

func kbSessionToJSON(s *kbv1.KBSession) kbSessionJSON {
	if s == nil {
		return kbSessionJSON{}
	}
	return kbSessionJSON{
		ID:           s.GetId(),
		KbID:         s.GetKbId(),
		MessageCount: s.GetMessageCount(),
		LastQuery:    s.GetLastQuery(),
		CreatedAt:    protoTimestampToRFC3339(s.GetCreatedAt()),
		LastActiveAt: protoTimestampToRFC3339(s.GetLastActiveAt()),
	}
}

// kbChunkToJSON converts a proto KBChunk to the REST KBChunk shape. The
// custom_metadata JSONB string is passed through as raw JSON so the response
// carries an object (empty → omitted, never a quoted string); invalid JSONB
// surfaces an error — it cannot occur with DB-constrained writes (SPEC §7.2).
func kbChunkToJSON(chunk *kbv1.KBChunk) (kbChunkJSON, error) {
	if chunk == nil {
		return kbChunkJSON{}, nil
	}
	metadata, err := jsonbToRaw(chunk.GetCustomMetadata())
	if err != nil {
		return kbChunkJSON{}, fmt.Errorf("chunk %s: %w", chunk.GetId(), err)
	}
	return kbChunkJSON{
		ID:             chunk.GetId(),
		DocID:          chunk.GetDocId(),
		KbID:           chunk.GetKbId(),
		ParentChunkID:  chunk.GetParentChunkId(),
		ChunkType:      chunk.GetChunkType(),
		Content:        chunk.GetContent(),
		ParentContent:  chunk.GetParentContent(),
		PageNumber:     chunk.GetPageNumber(),
		ContentType:    chunk.GetContentType(),
		FileName:       chunk.GetFileName(),
		TokenCount:     chunk.GetTokenCount(),
		CustomMetadata: metadata,
		CreatedAt:      protoTimestampToRFC3339(chunk.GetCreatedAt()),
	}, nil
}

// kbSessionMessageToJSON converts a proto KBSessionMessage to the REST
// KBSessionMessage shape. The source_chunks JSONB string is passed through as
// raw JSON so the response carries an array; user messages have no sources and
// serialize as null (no fabricated empty array), matching SPEC §5.4. Invalid
// JSONB surfaces an error — it cannot occur with DB-constrained writes
// (SPEC §7.2).
func kbSessionMessageToJSON(m *kbv1.KBSessionMessage) (kbSessionMessageJSON, error) {
	if m == nil {
		return kbSessionMessageJSON{}, nil
	}
	sources, err := jsonbToRaw(m.GetSourceChunks())
	if err != nil {
		return kbSessionMessageJSON{}, fmt.Errorf("message %s: %w", m.GetId(), err)
	}
	return kbSessionMessageJSON{
		ID:           m.GetId(),
		SessionID:    m.GetSessionId(),
		Role:         m.GetRole(),
		Content:      m.GetContent(),
		Sources:      sources,
		InputTokens:  m.GetInputTokens(),
		OutputTokens: m.GetOutputTokens(),
		DurationMs:   m.GetDurationMs(),
		CreatedAt:    protoTimestampToRFC3339(m.GetCreatedAt()),
	}, nil
}

// jsonbToRaw converts a JSONB string carried in a proto string field into raw
// JSON for direct response embedding (custom_metadata → object, source_chunks
// → array). Empty and "null" inputs are legitimate NULLs and return nil (which
// serializes as JSON null); invalid input returns an error so the handler can
// surface 500 instead of silently dropping data — the REST contract never
// exposes a JSONB string as a quoted string (SPEC §3.2 / §7.2).
func jsonbToRaw(raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	b := []byte(raw)
	if !json.Valid(b) {
		return nil, fmt.Errorf("invalid JSONB payload %q", raw)
	}
	return json.RawMessage(b), nil
}

// protoTimestampToRFC3339 converts a protobuf Timestamp to an RFC3339 string.
// Returns "" for nil/zero timestamps so omitted JSON fields stay consistent
// with services/v1.yaml (which marks parsed_at/last_active_at as optional
// and nullable). A proto3 Timestamp cannot express "unset", so kb-service
// maps DB NULL to the zero message (seconds=0); both the Go zero time and
// the epoch zero must therefore serialize as "" — otherwise an empty
// session surfaces last_active_at=1970-01-01T00:00:00Z (e2e T3a).
func protoTimestampToRFC3339(ts interface {
	AsTime() time.Time
}) string {
	if ts == nil {
		return ""
	}
	t := ts.AsTime()
	if t.IsZero() || t.Equal(time.Unix(0, 0).UTC()) {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
