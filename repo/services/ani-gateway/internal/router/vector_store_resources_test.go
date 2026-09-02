package router

import (
	"bytes"
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
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
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

// contentReturningVectorStoreService is a mock VectorStoreService whose
// SearchVectorStore returns hits carrying the Content field (issue #028).
type contentReturningVectorStoreService struct {
	recordingVectorStoreService
	searchHits []ports.VectorSearchResult
}

func (s *contentReturningVectorStoreService) GetVectorStore(_ context.Context, _ ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{
		TenantID:  "tenant-a",
		StoreID:   "vst_search_content",
		Name:      "kb-main",
		Dimension: 3,
		Metric:    "cosine",
		State:     ports.VectorStoreReady,
	}, nil
}

func (s *contentReturningVectorStoreService) SearchVectorStore(_ context.Context, _ ports.VectorStoreResourceSearchRequest) ([]ports.VectorSearchResult, error) {
	return s.searchHits, nil
}

func TestVectorStoreAPISearchResponseIncludesContentField(t *testing.T) {
	service := &contentReturningVectorStoreService{
		searchHits: []ports.VectorSearchResult{
			{ID: "chunk-1", Score: 0.92, Content: "chunk text from backend", Metadata: map[string]string{"doc_id": "d1"}},
			{ID: "chunk-2", Score: 0.81, Content: "second chunk text", Metadata: map[string]string{"doc_id": "d2"}},
		},
	}
	h := setupVectorStoreTestServer(service)

	body := `{"vector":[0.1,0.2,0.3],"top_k":5}`
	resp := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/api/v1/vector-stores/vst_search_content/search",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("search status = %d body=%s, want 200", resp.StatusCode(), resp.Body())
	}
	var decoded map[string]any
	if err := json.Unmarshal(resp.Body(), &decoded); err != nil {
		t.Fatalf("decode search body: %v", err)
	}
	items, _ := decoded["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != "chunk-1" || first["score"] != 0.92 {
		t.Fatalf("first item = %v, want id and score preserved", first)
	}
	if first["content"] != "chunk text from backend" {
		t.Fatalf("first content = %v, want content field from backend", first["content"])
	}
	second, _ := items[1].(map[string]any)
	if second["content"] != "second chunk text" {
		t.Fatalf("second content = %v, want content field from backend", second["content"])
	}
}

func TestVectorStoreAPIInsertDocumentsPassesPrecomputedVectorToService(t *testing.T) {
	captured := &capturingInsertVectorStoreService{}
	h := setupVectorStoreTestServer(captured)

	body := `{"idempotency_key":"http-insert-precomputed","documents":[{"id":"doc-pre","content":"hello","vector":[0.11,0.22,0.33]}]}`
	resp := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/api/v1/vector-stores/vst_insert_pre/documents",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()

	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("insert status = %d body=%s, want 202", resp.StatusCode(), resp.Body())
	}
	if len(captured.lastDocuments) != 1 {
		t.Fatalf("captured documents = %d, want 1", len(captured.lastDocuments))
	}
	got := captured.lastDocuments[0]
	if got.ID != "doc-pre" || got.Content != "hello" {
		t.Fatalf("captured doc = %+v, want id and content", got)
	}
	if len(got.Vector) != 3 || got.Vector[0] != 0.11 || got.Vector[1] != 0.22 || got.Vector[2] != 0.33 {
		t.Fatalf("captured vector = %v, want precomputed [0.11 0.22 0.33]", got.Vector)
	}
}

// capturingInsertVectorStoreService records the last InsertDocuments request
// so the handler can be verified to forward the precomputed Vector field.
// Embeds recordingVectorStoreService for the remaining interface methods.
type capturingInsertVectorStoreService struct {
	recordingVectorStoreService
	lastDocuments []ports.VectorDocumentInput
}

func (s *capturingInsertVectorStoreService) InsertDocuments(_ context.Context, request ports.VectorStoreDocumentInsertRequest) (ports.VectorStoreDocumentInsertResult, error) {
	s.lastDocuments = append([]ports.VectorDocumentInput(nil), request.Documents...)
	return ports.VectorStoreDocumentInsertResult{InsertedCount: len(request.Documents), TaskID: "11111111-1111-4111-8111-111111111111", Status: "completed"}, nil
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

func TestVectorStoreHTTPDocumentInsertPersistsPollableTask(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
		if tenantID == "" {
			tenantID = "tenant-a"
		}
		c.Set("tenant_id", tenantID)
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerVectorStoreResourcesWithServiceAndTasks(v1, runtimeadapter.NewLocalVectorStoreService(), tasks)
	registerTasksWithStore(v1, tasks, nil)

	created := performJSONRequest(t, h, http.MethodPost, "/api/v1/vector-stores", `{"idempotency_key":"http-vector-doc-task-create","name":"kb-doc-task","dimension":3}`, http.StatusCreated)
	storeID := jsonStringField(t, created, "id")
	requestBody := `{"idempotency_key":"http-vector-doc-task-insert","documents":[{"id":"doc-a","content":"hello vector"}]}`
	resp := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/api/v1/vector-stores/"+storeID+"/documents",
		&ut.Body{Body: bytes.NewBufferString(requestBody), Len: len(requestBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("insert status = %d body=%s, want 202", resp.StatusCode(), resp.Body())
	}
	var inserted map[string]any
	if err := json.Unmarshal(resp.Body(), &inserted); err != nil {
		t.Fatalf("unmarshal insert body: %v", err)
	}
	taskID, _ := inserted["task_id"].(string)
	if taskID == "" || inserted["status"] != "completed" || inserted["inserted_count"] != float64(1) {
		t.Fatalf("insert body = %s, want existing v1 response fields", resp.Body())
	}
	location := string(resp.Header.Get("Location"))
	if location != "/api/v1/tasks/"+taskID {
		t.Fatalf("Location = %q, want task URL for %s", location, taskID)
	}

	taskResp := ut.PerformRequest(h.Engine, http.MethodGet, location, nil).Result()
	if taskResp.StatusCode() != http.StatusOK {
		t.Fatalf("task status = %d body=%s, want 200", taskResp.StatusCode(), taskResp.Body())
	}
	var task map[string]any
	if err := json.Unmarshal(taskResp.Body(), &task); err != nil {
		t.Fatalf("unmarshal task body: %v", err)
	}
	if task["id"] != taskID || task["task_type"] != "vector_store.document.insert" || task["resource_type"] != "vector_store" || task["status"] != "completed" {
		t.Fatalf("task body = %s, want completed vector document insert task", taskResp.Body())
	}
	result, _ := task["result"].(map[string]any)
	if result["vector_store_id"] != storeID || result["inserted_count"] != float64(1) {
		t.Fatalf("task result = %v, want vector store ID and inserted count", result)
	}

	replayResp := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/api/v1/vector-stores/"+storeID+"/documents",
		&ut.Body{Body: bytes.NewBufferString(requestBody), Len: len(requestBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if replayResp.StatusCode() != http.StatusAccepted {
		t.Fatalf("replay status = %d body=%s, want 202", replayResp.StatusCode(), replayResp.Body())
	}
	if replayedTaskID := jsonStringField(t, replayResp.Body(), "task_id"); replayedTaskID != taskID {
		t.Fatalf("replayed task ID = %q, want %q", replayedTaskID, taskID)
	}

	crossTenant := ut.PerformRequest(
		h.Engine,
		http.MethodGet,
		location,
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-b"},
	).Result()
	if crossTenant.StatusCode() != http.StatusNotFound {
		t.Fatalf("cross-tenant task status = %d body=%s, want 404", crossTenant.StatusCode(), crossTenant.Body())
	}

	persistedTaskID := "33333333-3333-4333-8333-333333333333"
	_, _, err := tasks.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       "tenant-a",
		ID:             persistedTaskID,
		IdempotencyKey: "http-vector-doc-task-pg-replay",
		TaskType:       "vector_store.document.insert",
		ResourceType:   "vector_store",
		Status:         "completed",
		AttemptCount:   1,
		MaxAttempts:    1,
		ProgressPct:    100,
		Result: map[string]any{
			"vector_store_id": storeID,
			"inserted_count":  1,
		},
	})
	if err != nil {
		t.Fatalf("seed persisted task: %v", err)
	}
	persistedReplayBody := `{"idempotency_key":"http-vector-doc-task-pg-replay","documents":[{"id":"doc-b","content":"persisted replay"}]}`
	persistedReplay := ut.PerformRequest(
		h.Engine,
		http.MethodPost,
		"/api/v1/vector-stores/"+storeID+"/documents",
		&ut.Body{Body: bytes.NewBufferString(persistedReplayBody), Len: len(persistedReplayBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if persistedReplay.StatusCode() != http.StatusAccepted {
		t.Fatalf("persisted replay status = %d body=%s, want 202", persistedReplay.StatusCode(), persistedReplay.Body())
	}
	if got := jsonStringField(t, persistedReplay.Body(), "task_id"); got != persistedTaskID {
		t.Fatalf("persisted replay task ID = %q, want %q", got, persistedTaskID)
	}
	if got := string(persistedReplay.Header.Get("Location")); got != "/api/v1/tasks/"+persistedTaskID {
		t.Fatalf("persisted replay Location = %q, want existing task URL", got)
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
func (s *notFoundVectorStoreService) RebuildVectorStoreIndex(context.Context, ports.VectorStoreRebuildIndexRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) SetVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreKnowledgeBaseLinkRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) DeleteVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrNotFound
}
func (s *notFoundVectorStoreService) PrecheckVectorStoreDelete(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreDeletePrecheck, error) {
	return ports.VectorStoreDeletePrecheck{}, ports.ErrNotFound
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
func (s *errorVectorStoreService) RebuildVectorStoreIndex(context.Context, ports.VectorStoreRebuildIndexRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrUnavailable
}
func (s *errorVectorStoreService) SetVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreKnowledgeBaseLinkRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrUnavailable
}
func (s *errorVectorStoreService) DeleteVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, ports.ErrUnavailable
}
func (s *errorVectorStoreService) PrecheckVectorStoreDelete(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreDeletePrecheck, error) {
	return ports.VectorStoreDeletePrecheck{}, ports.ErrUnavailable
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
func (s *successVectorStoreService) RebuildVectorStoreIndex(context.Context, ports.VectorStoreRebuildIndexRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) SetVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreKnowledgeBaseLinkRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) DeleteVectorStoreKnowledgeBaseLink(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreRecord, error) {
	return ports.VectorStoreRecord{}, nil
}
func (s *successVectorStoreService) PrecheckVectorStoreDelete(context.Context, ports.VectorStoreResourceGetRequest) (ports.VectorStoreDeletePrecheck, error) {
	return ports.VectorStoreDeletePrecheck{Deletable: true}, nil
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
