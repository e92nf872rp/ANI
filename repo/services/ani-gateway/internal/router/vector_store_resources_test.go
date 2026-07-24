package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type recordingVectorStoreService struct {
	createCalls int
}

func (s *recordingVectorStoreService) CreateVectorStore(_ context.Context, request ports.VectorStoreCreateRequest) (ports.VectorStoreRecord, error) {
	s.createCalls++
	return ports.VectorStoreRecord{
		TenantID:  request.TenantID,
		StoreID:   "vst_injected",
		Name:      request.Name,
		Dimension: request.Dimension,
		Metric:    request.Metric,
		State:     ports.VectorStoreReady,
	}, nil
}

func (s *recordingVectorStoreService) ListVectorStores(context.Context, ports.VectorStoreResourceListRequest) ([]ports.VectorStoreRecord, error) {
	return nil, nil
}

func (s *recordingVectorStoreService) GetVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) DeleteVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) SearchVectorStore(context.Context, ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return nil, nil
}

func (s *recordingVectorStoreService) InsertDocuments(context.Context, ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	return ports.VectorStoreDocumentInsertResult{}, nil
}

func (s *recordingVectorStoreService) DeleteDocuments(context.Context, ports.VectorStoreDocumentDeleteRequest) (ports.VectorStoreDocumentDeleteResult, error) {
	return ports.VectorStoreDocumentDeleteResult{}, nil
}

func TestVectorStoreAPIDevProfileCreateSearchAndDelete(t *testing.T) {
	api := newVectorStoreAPI()
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-a",
		Name:           "kb-main",
		Dimension:      3,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}
	if got := vectorStoreFromRecord(store); got.ID == "" || got.State != "ready" || got.Dimension != 3 {
		t.Fatalf("vector store response = %+v, want ready vector store", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-vector-store-service")
	}
	results, err := api.service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2, 0.3},
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchVectorStore error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want empty dev profile search result", len(results))
	}
	deleted, err := api.service.DeleteVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
	})
	if err != nil {
		t.Fatalf("DeleteVectorStore error = %v", err)
	}
	if deleted.State != ports.VectorStoreDeleted {
		t.Fatalf("deleted state = %q, want deleted", deleted.State)
	}
}

func TestVectorStoreAPIServiceKeepsTenantIsolation(t *testing.T) {
	api := newVectorStoreAPI()
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-b",
		Name:           "tenant-a-store",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}
	if _, err := api.service.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   "tenant-b",
		ResourceID: store.StoreID,
	}); err == nil {
		t.Fatalf("GetVectorStore from another tenant succeeded, want isolation error")
	}
}

func TestVectorStoreAPIDocumentInsertResponseMatchesCoreSchema(t *testing.T) {
	api := newVectorStoreAPI()
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-docs",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}

	result, err := api.service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "api-insert-docs",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-a", Content: "hello vector", Metadata: map[string]string{"source": "router"}},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments error = %v", err)
	}
	if got := vectorStoreDocumentInsertFromResult(result); got.InsertedCount != 1 || got.TaskID == "" || got.Status != "completed" {
		t.Fatalf("insert response = %+v, want VectorStoreDocumentInsertResponse fields", got)
	}
}

func TestVectorStoreAPIUsesInjectedService(t *testing.T) {
	service := &recordingVectorStoreService{}
	api := newVectorStoreAPIWithService(service)
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-injected",
		Name:           "kb-injected",
		Dimension:      3,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}
	if service.createCalls != 1 || store.StoreID != "vst_injected" {
		t.Fatalf("injected service createCalls=%d store=%+v, want injected service", service.createCalls, store)
	}
}

func TestVectorStoreAPIDeleteDocumentsResponseMatchesCoreSchema(t *testing.T) {
	api := newVectorStoreAPI()
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-delete-docs",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}
	result, err := api.service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Filter:     `doc_id == "abc"`,
	})
	if err != nil {
		t.Fatalf("DeleteDocuments error = %v", err)
	}
	if got := vectorStoreDocumentDeleteFromResult(result); got.DeletedCount != 0 {
		t.Fatalf("delete response = %+v, want VectorStoreDocumentDeleteResponse with deleted_count", got)
	}
}

// --- HTTP-level tests using Hertz test engine ---

// notFoundVectorStoreService is a mock whose DeleteDocuments always returns ErrNotFound.
type notFoundVectorStoreService struct{}

func (s *notFoundVectorStoreService) CreateVectorStore(context.Context, ports.VectorStoreCreateRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) ListVectorStores(context.Context, ports.VectorStoreResourceListRequest) ([]ports.VectorStoreRecord, error) {
	return nil, nil
}
func (s *notFoundVectorStoreService) GetVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) DeleteVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) SearchVectorStore(context.Context, ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return nil, nil
}
func (s *notFoundVectorStoreService) InsertDocuments(context.Context, ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	return ports.VectorStoreDocumentInsertResult{}, nil
}
func (s *notFoundVectorStoreService) DeleteDocuments(context.Context, ports.VectorStoreDocumentDeleteRequest) (ports.VectorStoreDocumentDeleteResult, error) {
	return ports.VectorStoreDocumentDeleteResult{}, ports.ErrNotFound
}

func setupVectorStoreTestServer(service ports.VectorStoreService) *server.Hertz {
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
		if tenantID == "" {
			tenantID = "demo-tenant"
		}
		c.Set("tenant_id", tenantID)
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerVectorStoreResourcesWithService(v1, service)
	return h
}

func TestVectorStoreAPIDeleteDocumentsNotFoundReturnsVectorStoreNotFound(t *testing.T) {
	h := setupVectorStoreTestServer(&notFoundVectorStoreService{})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_missing/documents?filter=doc_id+%3D%3D+%22abc%22",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "VECTOR_STORE_NOT_FOUND" {
		t.Fatalf("code = %v, want VECTOR_STORE_NOT_FOUND", body["code"])
	}
}

// errorVectorStoreService returns a configurable error from DeleteDocuments.
type errorVectorStoreService struct {
	deleteErr error
}

func (s *errorVectorStoreService) CreateVectorStore(context.Context, ports.VectorStoreCreateRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *errorVectorStoreService) ListVectorStores(context.Context, ports.VectorStoreResourceListRequest) ([]ports.VectorStoreRecord, error) {
	return nil, nil
}
func (s *errorVectorStoreService) GetVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *errorVectorStoreService) DeleteVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *errorVectorStoreService) SearchVectorStore(context.Context, ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return nil, nil
}
func (s *errorVectorStoreService) InsertDocuments(context.Context, ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	return ports.VectorStoreDocumentInsertResult{}, nil
}
func (s *errorVectorStoreService) DeleteDocuments(context.Context, ports.VectorStoreDocumentDeleteRequest) (ports.VectorStoreDocumentDeleteResult, error) {
	return ports.VectorStoreDocumentDeleteResult{}, s.deleteErr
}

func TestVectorStoreAPIDeleteDocumentsEmptyFilterReturnsInvalidFilter(t *testing.T) {
	h := setupVectorStoreTestServer(&errorVectorStoreService{})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "INVALID_FILTER" {
		t.Fatalf("code = %v, want INVALID_FILTER", body["code"])
	}
}

func TestVectorStoreAPIDeleteDocumentsOversizedFilterReturnsInvalidFilter(t *testing.T) {
	h := setupVectorStoreTestServer(&errorVectorStoreService{})
	longFilter := strings.Repeat("a", 513)

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents?filter="+url.QueryEscape(longFilter),
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "INVALID_FILTER" {
		t.Fatalf("code = %v, want INVALID_FILTER", body["code"])
	}
}

func TestVectorStoreAPIDeleteDocumentsNotReadyReturnsPreconditionFailed(t *testing.T) {
	h := setupVectorStoreTestServer(&errorVectorStoreService{deleteErr: fmt.Errorf("%w: vector store is not ready", ports.ErrFailedPrecondition)})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents?filter=doc_id+%3D%3D+%22abc%22",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "PRECONDITION_FAILED" {
		t.Fatalf("code = %v, want PRECONDITION_FAILED", body["code"])
	}
}

func TestVectorStoreAPIDeleteDocumentsUnavailableReturnsUnavailable(t *testing.T) {
	h := setupVectorStoreTestServer(&errorVectorStoreService{deleteErr: fmt.Errorf("%w: milvus connection refused", ports.ErrUnavailable)})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents?filter=doc_id+%3D%3D+%22abc%22",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "UNAVAILABLE" {
		t.Fatalf("code = %v, want UNAVAILABLE", body["code"])
	}
}

func TestVectorStoreAPIDeleteDocumentsMilvusInvalidExprReturnsPreconditionFailed(t *testing.T) {
	h := setupVectorStoreTestServer(&errorVectorStoreService{deleteErr: fmt.Errorf("%w: invalid expression", ports.ErrInvalid)})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents?filter=invalid+expr",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["code"] != "PRECONDITION_FAILED" {
		t.Fatalf("code = %v, want PRECONDITION_FAILED", body["code"])
	}
}

// successVectorStoreService returns a successful delete result with deleted_count.
type successVectorStoreService struct{}

func (s *successVectorStoreService) CreateVectorStore(context.Context, ports.VectorStoreCreateRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) ListVectorStores(context.Context, ports.VectorStoreResourceListRequest) ([]ports.VectorStoreRecord, error) {
	return nil, nil
}
func (s *successVectorStoreService) GetVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) DeleteVectorStore(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) SearchVectorStore(context.Context, ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return nil, nil
}
func (s *successVectorStoreService) InsertDocuments(context.Context, ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	return ports.VectorStoreDocumentInsertResult{}, nil
}
func (s *successVectorStoreService) DeleteDocuments(context.Context, ports.VectorStoreDocumentDeleteRequest) (ports.VectorStoreDocumentDeleteResult, error) {
	return ports.VectorStoreDocumentDeleteResult{DeletedCount: 7}, nil
}

func TestVectorStoreAPIDeleteDocumentsSuccessReturnsDeletedCount(t *testing.T) {
	h := setupVectorStoreTestServer(&successVectorStoreService{})

	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/vector-stores/vst_1/documents?filter=doc_id+%3D%3D+%22abc%22",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	var body struct {
		DeletedCount int `json:"deleted_count"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body.DeletedCount != 7 {
		t.Fatalf("deleted_count = %d, want 7", body.DeletedCount)
	}
}
