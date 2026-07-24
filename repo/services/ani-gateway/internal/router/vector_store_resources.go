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

type vectorDocumentInputBody struct {
	ID       string         `json:"id,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type vectorStoreResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Dimension  int                    `json:"dimension"`
	Metric     string                 `json:"metric"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
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
	v1.POST("/vector-stores/:vector_store_id/documents", api.insertVectorStoreDocuments)
	v1.DELETE("/vector-stores/:vector_store_id/documents", api.deleteVectorStoreDocuments)
}

func (api *vectorStoreAPI) createVectorStore(ctx context.Context, c *app.RequestContext) {
	var req createVectorStoreRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector store request")
		return
	}
	record, err := api.service.CreateVectorStore(ctx, ports.VectorStoreCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Dimension:      req.Dimension,
		Metric:         req.Metric,
	})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) listVectorStores(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListVectorStores(ctx, ports.VectorStoreResourceListRequest{TenantID: demoTenantID(c)})
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
	record, err := api.service.GetVectorStore(ctx, ports.VectorStoreResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) deleteVectorStore(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVectorStore(ctx, ports.VectorStoreResourceGetRequest{TenantID: demoTenantID(c), ResourceID: c.Param("vector_store_id")})
	if err != nil {
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreFromRecord(record))
}

func (api *vectorStoreAPI) searchVectorStore(ctx context.Context, c *app.RequestContext) {
	var req searchVectorStoreRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector search request")
		return
	}
	results, err := api.service.SearchVectorStore(ctx, ports.VectorStoreResourceSearchRequest{
		TenantID:   demoTenantID(c),
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

func (api *vectorStoreAPI) insertVectorStoreDocuments(ctx context.Context, c *app.RequestContext) {
	var req vectorStoreDocumentInsertRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid vector document insert request")
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
		TenantID:       demoTenantID(c),
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
		writeDemoError(c, http.StatusBadRequest, "INVALID_FILTER", "filter 表达式不能为空")
		return
	}
	if len(filter) > 512 {
		writeDemoError(c, http.StatusBadRequest, "INVALID_FILTER", "filter 表达式长度不能超过 512")
		return
	}
	result, err := api.service.DeleteDocuments(ctx, ports.VectorStoreDocumentDeleteRequest{
		TenantID:   demoTenantID(c),
		ResourceID: c.Param("vector_store_id"),
		Filter:     filter,
	})
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeDemoError(c, http.StatusNotFound, "VECTOR_STORE_NOT_FOUND", "向量存储不存在")
			return
		}
		if errors.Is(err, ports.ErrFailedPrecondition) {
			writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
			return
		}
		if errors.Is(err, ports.ErrUnavailable) {
			writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error())
			return
		}
		if errors.Is(err, ports.ErrInvalid) {
			writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "filter 表达式非法")
			return
		}
		writeVectorStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, vectorStoreDocumentDeleteFromResult(result))
}

func vectorStoreFromRecord(record ports.VectorStoreRecord) vectorStoreResponse {
	return vectorStoreResponse{
		ID:         record.StoreID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		Dimension:  record.Dimension,
		Metric:     record.Metric,
		State:      string(record.State),
		Reason:     record.Reason,
		DevProfile: localCoreDevProfile("local-vector-store-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:  networkTime(record.CreatedAt),
		UpdatedAt:  networkTime(record.UpdatedAt),
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
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrUnavailable):
		writeDemoError(c, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
