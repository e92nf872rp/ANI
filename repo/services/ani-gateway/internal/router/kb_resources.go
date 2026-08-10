package router

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
)

// registerKnowledgeBases wires the 12 KB endpoints (SPEC §4.1 端点表):
//   - 9 P0 endpoints routed to kb-service via gRPC (replacing the previous stubs)
//   - 1 SSE streaming query endpoint held by the gateway
//   - 3 P1 endpoints (citations/sessions/permissions) routed to kb-service;
//     kb-service returns UNIMPLEMENTED which maps to HTTP 501.
//
// When client is nil the 9 gRPC handlers return 503 UNAVAILABLE so the gateway
// stays up if kb-service is not configured at boot. The SSE and P1 handlers do
// not require the client for their pre-flight responses.
func registerKnowledgeBases(svc *route.RouterGroup) {
	registerKnowledgeBasesWithClient(svc, nil, KbSSEConfig{})
}

func registerKnowledgeBasesWithClient(svc *route.RouterGroup, client KBGRPCClient, sseCfg KbSSEConfig) {
	api := &kbAPI{client: client}
	svc.GET("/knowledge-bases", api.listKnowledgeBases)
	svc.POST("/knowledge-bases", api.createKnowledgeBase)
	svc.GET("/knowledge-bases/:kb_id", api.getKnowledgeBase)
	svc.DELETE("/knowledge-bases/:kb_id", api.deleteKnowledgeBase)
	svc.GET("/knowledge-bases/:kb_id/documents", api.listKnowledgeBaseDocuments)
	svc.POST("/knowledge-bases/:kb_id/documents", api.uploadKnowledgeBaseDocument)
	svc.POST("/knowledge-bases/:kb_id/documents/:doc_id/notify-uploaded", api.notifyDocumentUploaded)
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

type getDocumentUploadURLRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	FileName      string `json:"file_name"`
	FileType      string `json:"file_type"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	ChecksumSha256 string `json:"checksum_sha256"`
	CustomMetadata string `json:"custom_metadata"`
}

type queryKnowledgeBaseRequest struct {
	IdempotencyKey         string  `json:"idempotency_key"`
	Question               string  `json:"question"`
	SessionID              string  `json:"session_id"`
	TopK                   int32   `json:"top_k"`
	ScoreThreshold         float32 `json:"score_threshold"`
	InferenceServiceName   string  `json:"inference_service_name"`
	RetrievalMode          string  `json:"retrieval_mode"`
}

type updateKBPermissionsRequest struct {
	IdempotencyKey   string   `json:"idempotency_key"`
	PublicRead       bool     `json:"public_read"`
	AllowedUserIDs   []string `json:"allowed_user_ids"`
}

// ── 9 P0 handlers (gRPC passthrough) ────────────────────────────────────────

func (a *kbAPI) listKnowledgeBases(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBs(ctx, demoTenantID(c), int32(limit), cursor)
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
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req createKnowledgeBaseRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base request")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	kb, err := a.client.CreateKB(ctx, demoTenantID(c), req.IdempotencyKey, &kbv1.CreateKBRequest{
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
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	kb, err := a.client.GetKB(ctx, demoTenantID(c), c.Param("kb_id"))
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbToJSON(kb))
}

func (a *kbAPI) deleteKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	if _, err := a.client.DeleteKB(ctx, demoTenantID(c), c.Param("kb_id")); err != nil {
		writeKBError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *kbAPI) listKnowledgeBaseDocuments(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	parseStatus := string(c.QueryArgs().Peek("parse_status"))
	resp, err := a.client.ListDocuments(ctx, demoTenantID(c), c.Param("kb_id"), parseStatus, int32(limit), cursor)
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
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req getDocumentUploadURLRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid document upload request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	if strings.TrimSpace(req.FileName) == "" || strings.TrimSpace(req.FileType) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "file_name and file_type are required")
		return
	}
	resp, err := a.client.GetDocumentUploadURL(ctx, demoTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.GetDocumentUploadURLRequest{
		FileName:        req.FileName,
		FileType:        req.FileType,
		FileSizeBytes:   req.FileSizeBytes,
		ChecksumSha256:  req.ChecksumSha256,
		CustomMetadata:  req.CustomMetadata,
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
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req struct {
		StoragePath string `json:"storage_path"`
	}
	_ = c.BindJSON(&req) // optional body; storage_path may be empty
	storagePath := req.StoragePath
	taskRef, err := a.client.NotifyDocumentUploaded(ctx, demoTenantID(c), c.Param("kb_id"), c.Param("doc_id"), storagePath)
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, map[string]any{
		"task_id":   taskRef.GetTaskId(),
		"task_type": taskRef.GetTaskType(),
		"status":    taskRef.GetStatus(),
	})
}

func (a *kbAPI) deleteKnowledgeBaseDocument(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	if _, err := a.client.DeleteDocument(ctx, demoTenantID(c), c.Param("kb_id"), c.Param("doc_id")); err != nil {
		writeKBError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *kbAPI) queryKnowledgeBase(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	var req queryKnowledgeBaseRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base query request")
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "question is required")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	resp, err := a.client.Query(ctx, demoTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.QueryRequest{
		Question:               req.Question,
		SessionId:              req.SessionID,
		TopK:                   req.TopK,
		ScoreThreshold:         req.ScoreThreshold,
		InferenceServiceName:   req.InferenceServiceName,
		RetrievalMode:          req.RetrievalMode,
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
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC ListKBCitations not implemented")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBCitations(ctx, demoTenantID(c), c.Param("kb_id"), int32(limit), cursor)
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
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC ListKBSessions not implemented")
		return
	}
	limit := queryInt(c, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	cursor := string(c.QueryArgs().Peek("cursor"))
	resp, err := a.client.ListKBSessions(ctx, demoTenantID(c), c.Param("kb_id"), int32(limit), cursor)
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
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "kb-service P1 RPC UpdateKBPermissions not implemented")
		return
	}
	var req updateKBPermissionsRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid knowledge base permissions request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	kb, err := a.client.UpdateKBPermissions(ctx, demoTenantID(c), c.Param("kb_id"), req.IdempotencyKey, &kbv1.UpdateKBPermissionsRequest{
		PublicRead:      req.PublicRead,
		AllowedUserIds:  req.AllowedUserIDs,
	})
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbToJSON(kb))
}

// ── JSON response structs ────────────────────────────────────────────────────
//
// These mirror the services/v1.yaml component schemas so the gateway response
// shape matches the OpenAPI contract that Console/BOSS codegen against.

type knowledgeBaseJSON struct {
	TenantID       string `json:"tenant_id"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embedding_model"`
	ChunkSize      int32  `json:"chunk_size"`
	TopK           int32  `json:"top_k"`
	ScoreThreshold float32 `json:"score_threshold"`
	RetrievalMode  string `json:"retrieval_mode"`
	Status         string `json:"status"`
	DocCount       int32  `json:"doc_count"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
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
}

type kbSessionJSON struct {
	ID            string `json:"id"`
	KbID          string `json:"kb_id"`
	MessageCount  int32  `json:"message_count"`
	LastQuery     string `json:"last_query"`
	CreatedAt     string `json:"created_at"`
	LastActiveAt  string `json:"last_active_at"`
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

// protoTimestampToRFC3339 converts a protobuf Timestamp to an RFC3339 string.
// Returns "" for nil/zero timestamps so omitted JSON fields stay consistent
// with services/v1.yaml (which marks parsed_at as optional).
func protoTimestampToRFC3339(ts interface {
	AsTime() time.Time
}) string {
	if ts == nil {
		return ""
	}
	if ts.AsTime().IsZero() {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}
