package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type vectorStoreAPI struct {
	service ports.VectorStoreService
}

type createVectorStoreRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Dimension      int    `json:"dimension"`
	Metric         string `json:"metric"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
}

type searchVectorStoreRequest struct {
	Vector []float32         `json:"vector"`
	TopK   int               `json:"top_k"`
	Filter map[string]string `json:"filter"`
}

type vectorStoreDocumentInsertRequest struct {
	IdempotencyKey string                    `json:"idempotency_key"`
	Documents      []vectorDocumentInputBody `json:"documents"`
}

type vectorStoreRebuildIndexRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type vectorStoreKnowledgeBaseLinkRequest struct {
	IdempotencyKey   string                          `json:"idempotency_key"`
	KnowledgeBaseRef vectorStoreKnowledgeBaseRefJSON `json:"knowledge_base_ref"`
}

type vectorDocumentInputBody struct {
	ID       string         `json:"id,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type vectorStoreResponse struct {
	ID               string                           `json:"id"`
	TenantID         string                           `json:"tenant_id"`
	Name             string                           `json:"name"`
	Dimension        int                              `json:"dimension"`
	Metric           string                           `json:"metric"`
	State            string                           `json:"state"`
	EmbeddingModel   string                           `json:"embedding_model,omitempty"`
	VectorCount      int64                            `json:"vector_count,omitempty"`
	IndexStatus      string                           `json:"index_status,omitempty"`
	LastIndexedAt    string                           `json:"last_indexed_at,omitempty"`
	KnowledgeBaseRef *vectorStoreKnowledgeBaseRefJSON `json:"knowledge_base_ref,omitempty"`
	Reason           string                           `json:"reason,omitempty"`
	DevProfile       coreDevProfileResponse           `json:"dev_profile"`
	CreatedAt        string                           `json:"created_at"`
	UpdatedAt        string                           `json:"updated_at"`
}

type vectorStoreKnowledgeBaseRefJSON struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

type vectorStoreDeletePrecheckResponse struct {
	Deletable bool                           `json:"deletable"`
	Reason    string                         `json:"reason,omitempty"`
	Blockers  []vectorStoreDeleteBlockerJSON `json:"blockers"`
}

type vectorStoreDeleteBlockerJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type vectorSearchHitResponse struct {
	ID       string            `json:"id"`
	Score    float32           `json:"score"`
	Metadata map[string]string `json:"metadata"`
}

type vectorStoreDocumentInsertResponse struct {
	InsertedCount int    `json:"inserted_count"`
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
}

type vectorStoreDocumentDeleteResponse struct {
	DeletedCount int `json:"deleted_count"`
}

func newVectorStoreAPI() *vectorStoreAPI {
	return newVectorStoreAPIWithService(nil)
}

func registerVectorStoreResources(v1 *route.RouterGroup) {
	registerVectorStoreResourcesWithService(v1, nil)
}

func newVectorStoreAPIWithService(service ports.VectorStoreService) *vectorStoreAPI {
	if service == nil {
		service = runtimeadapter.NewLocalVectorStoreService()
	}
	return &vectorStoreAPI{service: service}
}

func registerVectorStoreResourcesWithService(v1 *route.RouterGroup, service ports.VectorStoreService) {
	api := newVectorStoreAPIWithService(service)
	v1.GET("/vector-stores", api.listVectorStores)
	v1.POST("/vector-stores", api.createVectorStore)
	v1.GET("/vector-stores/:vector_store_id", api.getVectorStore)
	v1.DELETE("/vector-stores/:vector_store_id", api.deleteVectorStore)
	v1.POST("/vector-stores/:vector_store_id/search", api.searchVectorStore)
	v1.POST("/vector-stores/:vector_store_id/rebuild-index", api.rebuildVectorStoreIndex)
	v1.PUT("/vector-stores/:vector_store_id/knowledge-base-link", api.setVectorStoreKnowledgeBaseLink)
	v1.DELETE("/vector-stores/:vector_store_id/knowledge-base-link", api.deleteVectorStoreKnowledgeBaseLink)
	v1.GET("/vector-stores/:vector_store_id/delete-precheck", api.precheckVectorStoreDelete)
	v1.POST("/vector-stores/:vector_store_id/documents", api.insertVectorStoreDocuments)
	v1.DELETE("/vector-stores/:vector_store_id/documents", api.deleteVectorStoreDocuments)
}

func (api *vectorStoreAPI) createVectorStore(ctx context.Context, c *app.RequestContext) {
	var req createVectorStoreRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector store request")
		return
	}
	record, err := api.service.CreateVectorStore(ctx, ports.VectorStoreCreateRequest{
		TenantID:       instanceTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Dimension:      req.Dimension,
		Metric:         req.Metric,
		EmbeddingModel: req.EmbeddingModel,
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) listVectorStores(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListVectorStores(ctx, ports.VectorStoreResourceListRequest{TenantID: instanceTenantID(c)})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	items := make([]vectorStoreResponse, 0, len(records))
	for _, record := range records {
		items = append(items, vectorStoreFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *vectorStoreAPI) getVectorStore(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetVectorStore(ctx, ports.VectorStoreResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) deleteVectorStore(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVectorStore(ctx, ports.VectorStoreResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) searchVectorStore(ctx context.Context, c *app.RequestContext) {
	var req searchVectorStoreRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector search request")
		return
	}
	results, err := api.service.SearchVectorStore(ctx, ports.VectorStoreResourceSearchRequest{
		TenantID:   instanceTenantID(c),
		ResourceID: c.Param("vector_store_id"),
		Vector:     req.Vector,
		TopK:       req.TopK,
		Filter:     req.Filter,
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	items := make([]vectorSearchHitResponse, 0, len(results))
	for _, result := range results {
		items = append(items, vectorSearchHitResponse{
			ID:       result.ID,
			Score:    result.Score,
			Metadata: result.Metadata,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *vectorStoreAPI) rebuildVectorStoreIndex(ctx context.Context, c *app.RequestContext) {
	var req vectorStoreRebuildIndexRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector store rebuild request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	record, err := api.service.RebuildVectorStoreIndex(ctx, ports.VectorStoreRebuildIndexRequest{
		TenantID:       instanceTenantID(c),
		ResourceID:     c.Param("vector_store_id"),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	task := storageCompletedTask("vector_store.index.rebuild", "vector_store", req.IdempotencyKey, map[string]any{"vector_store": vectorStoreFromRecord(record)}, record.UpdatedAt)
	storageWriteAcceptedTask(c, task)
}

func (api *vectorStoreAPI) setVectorStoreKnowledgeBaseLink(ctx context.Context, c *app.RequestContext) {
	var req vectorStoreKnowledgeBaseLinkRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector store knowledge base link request")
		return
	}
	record, err := api.service.SetVectorStoreKnowledgeBaseLink(ctx, ports.VectorStoreKnowledgeBaseLinkRequest{
		TenantID:       instanceTenantID(c),
		ResourceID:     c.Param("vector_store_id"),
		IdempotencyKey: req.IdempotencyKey,
		KnowledgeBaseRef: ports.VectorStoreKnowledgeBaseRef{
			ID:     req.KnowledgeBaseRef.ID,
			Name:   req.KnowledgeBaseRef.Name,
			Source: req.KnowledgeBaseRef.Source,
		},
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) deleteVectorStoreKnowledgeBaseLink(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVectorStoreKnowledgeBaseLink(ctx, ports.VectorStoreResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) precheckVectorStoreDelete(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.PrecheckVectorStoreDelete(ctx, ports.VectorStoreResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreDeletePrecheckFromResult(result))
}

func (api *vectorStoreAPI) insertVectorStoreDocuments(ctx context.Context, c *app.RequestContext) {
	var req vectorStoreDocumentInsertRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector document insert request")
		return
	}
	documents := make([]ports.VectorDocumentInput, 0, len(req.Documents))
	for _, document := range req.Documents {
		documents = append(documents, ports.VectorDocumentInput{
			ID:       document.ID,
			Content:  document.Content,
			Metadata: stringMetadata(document.Metadata),
		})
	}
	result, err := api.service.InsertDocuments(ctx, ports.VectorStoreDocumentInsertRequest{
		TenantID:       instanceTenantID(c),
		ResourceID:     c.Param("vector_store_id"),
		IdempotencyKey: req.IdempotencyKey,
		Documents:      documents,
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.Response.Header.Set("Location", "/api/v1/tasks/"+result.TaskID)
	c.JSON(http.StatusAccepted, vectorStoreDocumentInsertFromResult(result))
}

func (api *vectorStoreAPI) deleteVectorStoreDocuments(ctx context.Context, c *app.RequestContext) {
	filter := strings.TrimSpace(string(c.QueryArgs().Peek("filter")))
	if filter == "" {
		writeInstanceError(c, http.StatusBadRequest, "INVALID_FILTER", "filter 表达式不能为空")
		return
	}
	if len(filter) > 512 {
		writeInstanceError(c, http.StatusBadRequest, "INVALID_FILTER", "filter 表达式长度不能超过 512")
		return
	}
	result, err := api.service.DeleteDocuments(ctx, ports.VectorStoreDocumentDeleteRequest{
		TenantID:   instanceTenantID(c),
		ResourceID: c.Param("vector_store_id"),
		Filter:     filter,
	})
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeInstanceError(c, http.StatusNotFound, "VECTOR_STORE_NOT_FOUND", "向量存储不存在")
			return
		}
		if errors.Is(err, ports.ErrFailedPrecondition) {
			writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
			return
		}
		if errors.Is(err, ports.ErrUnavailable) {
			writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error())
			return
		}
		if errors.Is(err, ports.ErrInvalid) {
			writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "filter 表达式非法")
			return
		}
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreDocumentDeleteFromResult(result))
}

func vectorStoreFromRecord(record ports.VectorStoreRecord) vectorStoreResponse {
	lastIndexedAt := ""
	if !record.LastIndexedAt.IsZero() {
		lastIndexedAt = networkTime(record.LastIndexedAt)
	}
	var knowledgeBaseRef *vectorStoreKnowledgeBaseRefJSON
	if record.KnowledgeBaseRef.Name != "" {
		knowledgeBaseRef = &vectorStoreKnowledgeBaseRefJSON{
			ID:     record.KnowledgeBaseRef.ID,
			Name:   record.KnowledgeBaseRef.Name,
			Source: record.KnowledgeBaseRef.Source,
		}
	}
	return vectorStoreResponse{
		ID:               record.StoreID,
		TenantID:         record.TenantID,
		Name:             record.Name,
		Dimension:        record.Dimension,
		Metric:           record.Metric,
		State:            string(record.State),
		EmbeddingModel:   record.EmbeddingModel,
		VectorCount:      record.VectorCount,
		IndexStatus:      record.IndexStatus,
		LastIndexedAt:    lastIndexedAt,
		KnowledgeBaseRef: knowledgeBaseRef,
		Reason:           record.Reason,
		DevProfile:       localCoreDevProfile("local-vector-store-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:        networkTime(record.CreatedAt),
		UpdatedAt:        networkTime(record.UpdatedAt),
	}
}

func vectorStoreDeletePrecheckFromResult(result ports.VectorStoreDeletePrecheck) vectorStoreDeletePrecheckResponse {
	blockers := make([]vectorStoreDeleteBlockerJSON, 0, len(result.Blockers))
	for _, blocker := range result.Blockers {
		blockers = append(blockers, vectorStoreDeleteBlockerJSON{
			Kind: blocker.Kind,
			ID:   blocker.ID,
			Name: blocker.Name,
		})
	}
	return vectorStoreDeletePrecheckResponse{
		Deletable: result.Deletable,
		Reason:    result.Reason,
		Blockers:  blockers,
	}
}

func vectorStoreDocumentInsertFromResult(result ports.VectorStoreDocumentInsertResult) vectorStoreDocumentInsertResponse {
	return vectorStoreDocumentInsertResponse{
		InsertedCount: result.InsertedCount,
		TaskID:        result.TaskID,
		Status:        result.Status,
	}
}

func vectorStoreDocumentDeleteFromResult(result ports.VectorStoreDocumentDeleteResult) vectorStoreDocumentDeleteResponse {
	return vectorStoreDocumentDeleteResponse{DeletedCount: result.DeletedCount}
}

func stringMetadata(metadata map[string]any) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	result := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if value == nil {
			result[key] = ""
			continue
		}
		result[key] = fmt.Sprint(value)
	}
	return result
}

func writeVectorStoreError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrUnavailable):
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeInstanceError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
