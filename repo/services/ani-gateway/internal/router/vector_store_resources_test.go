package router

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
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

func (s *recordingVectorStoreService) RebuildVectorStoreIndex(context.Context, ports.VectorStoreRebuildIndexRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) SetVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreKnowledgeBaseLinkRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) DeleteVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) PrecheckVectorStoreDelete(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreDeletePrecheck, error) {
	return ports.VectorStoreDeletePrecheck{}, ports.ErrNotFound
}

func (s *recordingVectorStoreService) SearchVectorStore(context.Context, ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return nil, nil
}

func (s *recordingVectorStoreService) InsertDocuments(context.Context, ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	return ports.VectorStoreDocumentInsertResult{}, nil
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

func TestVectorStoreAPIManagementResponsesMatchCoreSchema(t *testing.T) {
	api := newVectorStoreAPI()
	store, err := api.service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vector-management",
		Name:           "kb-linked",
		Dimension:      3,
		EmbeddingModel: "bge-m3",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore error = %v", err)
	}
	rebuilt, err := api.service.RebuildVectorStoreIndex(context.Background(), ports.VectorStoreRebuildIndexRequest{TenantID: "tenant-a", ResourceID: store.StoreID, IdempotencyKey: "api-vector-rebuild"})
	if err != nil {
		t.Fatalf("RebuildVectorStoreIndex error = %v", err)
	}
	if got := vectorStoreFromRecord(rebuilt); got.IndexStatus != "ready" || got.LastIndexedAt == "" || got.EmbeddingModel != "bge-m3" {
		t.Fatalf("rebuilt response = %+v, want index status and embedding model", got)
	}

	linked, err := api.service.SetVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreKnowledgeBaseLinkRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "api-vector-link",
		KnowledgeBaseRef: ports.VectorStoreKnowledgeBaseRef{
			ID:     "kb-001",
			Name:   "知识库",
			Source: "services_knowledge_base",
		},
	})
	if err != nil {
		t.Fatalf("SetVectorStoreKnowledgeBaseLink error = %v", err)
	}
	if got := vectorStoreFromRecord(linked); got.KnowledgeBaseRef == nil || got.KnowledgeBaseRef.ID != "kb-001" {
		t.Fatalf("linked response = %+v, want knowledge base ref", got)
	}

	precheck, err := api.service.PrecheckVectorStoreDelete(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("PrecheckVectorStoreDelete error = %v", err)
	}
	if got := vectorStoreDeletePrecheckFromResult(precheck); got.Deletable || len(got.Blockers) != 1 {
		t.Fatalf("precheck response = %+v, want one blocker", got)
	}

	unlinked, err := api.service.DeleteVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("DeleteVectorStoreKnowledgeBaseLink error = %v", err)
	}
	if got := vectorStoreFromRecord(unlinked); got.KnowledgeBaseRef != nil {
		t.Fatalf("unlinked response = %+v, want no knowledge base ref", got)
	}
}

func TestVectorStoreHTTPManagementOperationsEndToEnd(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerVectorStoreResourcesWithService(h.Group("/api/v1"), runtimeadapter.NewLocalVectorStoreService())

	created := performJSONRequest(t, h, http.MethodPost, "/api/v1/vector-stores", `{"idempotency_key":"http-vector-a","name":"kb-http","dimension":3,"metric":"cosine","embedding_model":"bge-m3"}`, http.StatusCreated)
	storeID := jsonStringField(t, created, "id")
	performJSONRequest(t, h, http.MethodPost, "/api/v1/vector-stores/"+storeID+"/rebuild-index", "", http.StatusBadRequest)
	rebuilt := performJSONRequest(t, h, http.MethodPost, "/api/v1/vector-stores/"+storeID+"/rebuild-index", `{"idempotency_key":"http-vector-rebuild"}`, http.StatusAccepted)
	if jsonStringField(t, rebuilt, "status") != "completed" {
		t.Fatalf("rebuilt body = %s, want completed rebuild task", rebuilt)
	}
	reloaded := performJSONRequest(t, h, http.MethodGet, "/api/v1/vector-stores/"+storeID, "", http.StatusOK)
	if jsonStringField(t, reloaded, "index_status") != "ready" {
		t.Fatalf("rebuilt body = %s, want ready index", rebuilt)
	}
	linked := performJSONRequest(t, h, http.MethodPut, "/api/v1/vector-stores/"+storeID+"/knowledge-base-link", `{"idempotency_key":"http-vector-link","knowledge_base_ref":{"id":"kb-001","name":"知识库","source":"services_knowledge_base"}}`, http.StatusOK)
	if jsonObjectStringField(t, linked, "knowledge_base_ref", "id") != "kb-001" {
		t.Fatalf("linked body = %s, want kb-001", linked)
	}
	precheck := performJSONRequest(t, h, http.MethodGet, "/api/v1/vector-stores/"+storeID+"/delete-precheck", "", http.StatusOK)
	if jsonBoolField(t, precheck, "deletable") {
		t.Fatalf("precheck body = %s, want blocked while linked", precheck)
	}
	unlinked := performJSONRequest(t, h, http.MethodDelete, "/api/v1/vector-stores/"+storeID+"/knowledge-base-link", "", http.StatusOK)
	if jsonStringField(t, unlinked, "reason") == "" {
		t.Fatalf("unlinked body = %s, want reason", unlinked)
	}
}

func jsonBoolField(t *testing.T, body []byte, key string) bool {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	value, _ := decoded[key].(bool)
	return value
}

func jsonObjectStringField(t *testing.T, body []byte, objectKey string, fieldKey string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	object, _ := decoded[objectKey].(map[string]any)
	value, _ := object[fieldKey].(string)
	return value
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
