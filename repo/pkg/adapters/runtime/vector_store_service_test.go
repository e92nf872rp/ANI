package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalVectorStoreServiceDevProfile(t *testing.T) {
	service := NewLocalVectorStoreService()

	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-a",
		Name:           "kb-main",
		Dimension:      3,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	if store.StoreID == "" || store.State != ports.VectorStoreReady || store.Metric != "cosine" {
		t.Fatalf("store = %#v, want ready cosine store", store)
	}
	replay, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-a",
		Name:           "kb-main-retry",
		Dimension:      99,
		Metric:         "l2",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore replay error = %v", err)
	}
	if replay.StoreID != store.StoreID || replay.Dimension != store.Dimension {
		t.Fatalf("replay store = %#v, want original %#v", replay, store)
	}
	if _, err := service.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-b", ResourceID: store.StoreID}); err == nil {
		t.Fatalf("GetVectorStore from another tenant succeeded, want isolation error")
	}
	results, err := service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2, 0.3},
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchVectorStore() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want empty local dev profile result", len(results))
	}
	deleted, err := service.DeleteVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("DeleteVectorStore() error = %v", err)
	}
	if deleted.State != ports.VectorStoreDeleted {
		t.Fatalf("deleted state = %q, want deleted", deleted.State)
	}
}

func TestLocalVectorStoreServiceSearchValidatesDimension(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-b",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	_, err = service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2},
	})
	if err == nil {
		t.Fatalf("SearchVectorStore() error = nil, want dimension mismatch")
	}
}

func TestLocalVectorStoreServiceSearchRequiresReadyStore(t *testing.T) {
	service := NewLocalVectorStoreService()
	now := time.Now().UTC()
	service.stores["vst-pending"] = ports.VectorStoreRecord{
		TenantID:  "tenant-a",
		StoreID:   "vst-pending",
		Name:      "pending-store",
		Dimension: 3,
		Metric:    "cosine",
		State:     ports.VectorStorePending,
		Reason:    "index is still building",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: "vst-pending",
		Vector:     []float32{0.1, 0.2, 0.3},
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("SearchVectorStore error = %v, want ErrFailedPrecondition", err)
	}
}

func TestLocalVectorStoreServiceCanCreatePendingDevProfileStore(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-pending",
		Name:           "pending-index",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	if store.State != ports.VectorStorePending {
		t.Fatalf("store state = %s, want pending", store.State)
	}
	_, err = service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2, 0.3},
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("SearchVectorStore error = %v, want ErrFailedPrecondition", err)
	}
}

func TestLocalVectorStoreServiceInsertDocumentsUsesVectorStorePort(t *testing.T) {
	backend := &fakeVectorStore{}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-docs",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	result, err := service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-docs-a",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-a", Content: "hello vector", Metadata: map[string]string{"source": "unit"}},
			{Content: "second document"},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments() error = %v", err)
	}
	if result.InsertedCount != 2 || result.TaskID == "" || result.Status != "completed" {
		t.Fatalf("insert result = %#v, want completed 2 document task", result)
	}
	if backend.upsertRef.TenantID != "tenant-a" || backend.upsertRef.KBID != store.StoreID {
		t.Fatalf("upsert ref = %#v, want tenant-a store collection", backend.upsertRef)
	}
	if len(backend.upsertRecords) != 2 || backend.upsertRecords[0].ID != "doc-a" || len(backend.upsertRecords[0].Vector) != 3 {
		t.Fatalf("upsert records = %#v, want two records with store dimension vectors", backend.upsertRecords)
	}
	if backend.upsertRecords[0].Metadata["source"] != "unit" || backend.upsertRecords[0].Metadata["content"] != "hello vector" {
		t.Fatalf("metadata = %#v, want source and content", backend.upsertRecords[0].Metadata)
	}

	replay, err := service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-docs-a",
		Documents:      []ports.VectorDocumentInput{{Content: "changed"}},
	})
	if err != nil {
		t.Fatalf("InsertDocuments replay error = %v", err)
	}
	if replay.TaskID != result.TaskID || replay.InsertedCount != result.InsertedCount {
		t.Fatalf("replay result = %#v, want original %#v", replay, result)
	}
	updated, err := service.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("GetVectorStore after insert error = %v", err)
	}
	if updated.VectorCount != 2 || updated.IndexStatus != "ready" || updated.LastIndexedAt.IsZero() {
		t.Fatalf("updated store = %#v, want vector_count/index status updated", updated)
	}
}

func TestLocalVectorStoreServiceInsertDocumentsUsesPrecomputedVector(t *testing.T) {
	backend := &fakeVectorStore{}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-precomputed",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	precomputed := []float32{0.11, 0.22, 0.33}
	result, err := service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-precomputed-a",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-pre", Content: "precomputed vector", Vector: precomputed},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments() error = %v", err)
	}
	if result.InsertedCount != 1 {
		t.Fatalf("inserted_count = %d, want 1", result.InsertedCount)
	}
	if len(backend.upsertRecords) != 1 {
		t.Fatalf("upsert records = %d, want 1", len(backend.upsertRecords))
	}
	got := backend.upsertRecords[0].Vector
	if len(got) != 3 {
		t.Fatalf("vector len = %d, want 3", len(got))
	}
	// 预计算向量必须原样传入 backend, 而非 localDocumentVector 伪向量
	if got[0] != precomputed[0] || got[1] != precomputed[1] || got[2] != precomputed[2] {
		t.Fatalf("stored vector = %v, want precomputed %v (not pseudo vector)", got, precomputed)
	}
}

func TestLocalVectorStoreServiceInsertDocumentsRejectsPrecomputedVectorDimensionMismatch(t *testing.T) {
	backend := &fakeVectorStore{}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-dim-mismatch",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	_, err = service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-dim-mismatch",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-bad", Content: "wrong dim", Vector: []float32{0.1, 0.2}},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("InsertDocuments error = %v, want ErrInvalid for dimension mismatch", err)
	}
	if len(backend.upsertRecords) != 0 {
		t.Fatalf("upsert records = %d, want 0 (nothing stored on invalid input)", len(backend.upsertRecords))
	}
}

func TestLocalVectorStoreServiceInsertDocumentsMixesPrecomputedAndPseudoVectors(t *testing.T) {
	backend := &fakeVectorStore{}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-mixed",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	precomputed := []float32{0.5, 0.6, 0.7}
	_, err = service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-mixed",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-pre", Content: "with precomputed vector", Vector: precomputed},
			{ID: "doc-pseudo", Content: "without vector"},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments() error = %v", err)
	}
	if len(backend.upsertRecords) != 2 {
		t.Fatalf("upsert records = %d, want 2", len(backend.upsertRecords))
	}
	// 第一条用预计算向量
	gotPre := backend.upsertRecords[0].Vector
	if gotPre[0] != precomputed[0] || gotPre[1] != precomputed[1] || gotPre[2] != precomputed[2] {
		t.Fatalf("first vector = %v, want precomputed %v", gotPre, precomputed)
	}
	// 第二条用 localDocumentVector 伪向量 (不等于预计算, 且维度为 3)
	gotPseudo := backend.upsertRecords[1].Vector
	if len(gotPseudo) != 3 {
		t.Fatalf("second vector len = %d, want 3", len(gotPseudo))
	}
	if gotPseudo[0] == precomputed[0] && gotPseudo[1] == precomputed[1] && gotPseudo[2] == precomputed[2] {
		t.Fatalf("second vector = %v, should be pseudo not equal to precomputed", gotPseudo)
	}
}

func TestLocalVectorStoreServiceSearchVectorStoreReturnsContentFromBackend(t *testing.T) {
	backend := &fakeVectorStoreWithSearch{
		hits: []ports.VectorSearchResult{
			{ID: "chunk-1", Score: 0.92, Content: "chunk text from backend", Metadata: map[string]string{"doc_id": "d1"}},
			{ID: "chunk-2", Score: 0.81, Content: "second chunk text", Metadata: map[string]string{"doc_id": "d2"}},
		},
	}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-search-content",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	results, err := service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2, 0.3},
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchVectorStore() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Content != "chunk text from backend" {
		t.Fatalf("results[0].Content = %q, want content from backend", results[0].Content)
	}
	if results[1].Content != "second chunk text" {
		t.Fatalf("results[1].Content = %q, want content from backend", results[1].Content)
	}
	if results[0].Score != 0.92 || results[0].ID != "chunk-1" {
		t.Fatalf("results[0] = %+v, want score and id preserved", results[0])
	}
}

func TestLocalVectorStoreServiceManagementOperations(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-management",
		Name:           "kb-linked",
		Dimension:      3,
		EmbeddingModel: "bge-m3",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	if store.EmbeddingModel != "bge-m3" || store.IndexStatus != "ready" {
		t.Fatalf("store = %#v, want embedding model and ready index", store)
	}
	rebuilt, err := service.RebuildVectorStoreIndex(context.Background(), ports.VectorStoreRebuildIndexRequest{TenantID: "tenant-a", ResourceID: store.StoreID, IdempotencyKey: "rebuild-a"})
	if err != nil {
		t.Fatalf("RebuildVectorStoreIndex() error = %v", err)
	}
	if rebuilt.IndexStatus != "ready" || rebuilt.LastIndexedAt.IsZero() {
		t.Fatalf("rebuilt = %#v, want ready index with last_indexed_at", rebuilt)
	}
	replay, err := service.RebuildVectorStoreIndex(context.Background(), ports.VectorStoreRebuildIndexRequest{TenantID: "tenant-a", ResourceID: store.StoreID, IdempotencyKey: "rebuild-a"})
	if err != nil {
		t.Fatalf("RebuildVectorStoreIndex replay error = %v", err)
	}
	if !replay.LastIndexedAt.Equal(rebuilt.LastIndexedAt) || replay.Reason != rebuilt.Reason {
		t.Fatalf("replay = %#v, want original rebuild result %#v", replay, rebuilt)
	}
	linked, err := service.SetVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreKnowledgeBaseLinkRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "link-a",
		KnowledgeBaseRef: ports.VectorStoreKnowledgeBaseRef{
			ID:     "kb-001",
			Name:   "知识库",
			Source: "services_knowledge_base",
		},
	})
	if err != nil {
		t.Fatalf("SetVectorStoreKnowledgeBaseLink() error = %v", err)
	}
	if linked.KnowledgeBaseRef.ID != "kb-001" {
		t.Fatalf("linked = %#v, want kb-001 ref", linked)
	}
	_, err = service.SetVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreKnowledgeBaseLinkRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "link-invalid-source",
		KnowledgeBaseRef: ports.VectorStoreKnowledgeBaseRef{
			ID:     "kb-002",
			Name:   "bad source",
			Source: "rag_service",
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("SetVectorStoreKnowledgeBaseLink invalid source error = %v, want ErrInvalid", err)
	}
	precheck, err := service.PrecheckVectorStoreDelete(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("PrecheckVectorStoreDelete() error = %v", err)
	}
	if precheck.Deletable || len(precheck.Blockers) != 1 {
		t.Fatalf("precheck = %#v, want blocker while linked", precheck)
	}
	unlinked, err := service.DeleteVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("DeleteVectorStoreKnowledgeBaseLink() error = %v", err)
	}
	if unlinked.KnowledgeBaseRef.Name != "" {
		t.Fatalf("unlinked = %#v, want empty knowledge base ref", unlinked)
	}
	precheck, err = service.PrecheckVectorStoreDelete(context.Background(), ports.VectorStoreResourceGetRequest{TenantID: "tenant-a", ResourceID: store.StoreID})
	if err != nil {
		t.Fatalf("PrecheckVectorStoreDelete after unlink error = %v", err)
	}
	if !precheck.Deletable || len(precheck.Blockers) != 0 {
		t.Fatalf("precheck after unlink = %#v, want deletable", precheck)
	}
}

func TestLocalVectorStoreServiceInsertDocumentsRequiresReadyStore(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-docs-pending",
		Name:           "pending-index",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	_, err = service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "insert-pending-a",
		Documents:      []ports.VectorDocumentInput{{Content: "not ready"}},
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("InsertDocuments error = %v, want ErrFailedPrecondition", err)
	}
}

type fakeVectorStore struct {
	upsertRef       ports.VectorCollectionRef
	upsertRecords   []ports.VectorRecord
	deleteExprRef   ports.VectorCollectionRef
	deleteExpr      string
	deleteExprCount int
}

func (s *fakeVectorStore) EnsureCollection(context.Context, ports.VectorCollectionRef, int) error {
	return nil
}

func (s *fakeVectorStore) Upsert(_ context.Context, ref ports.VectorCollectionRef, records []ports.VectorRecord) error {
	s.upsertRef = ref
	s.upsertRecords = append([]ports.VectorRecord(nil), records...)
	return nil
}

func (s *fakeVectorStore) Search(context.Context, ports.VectorSearchQuery) ([]ports.VectorSearchResult, error) {
	return nil, nil
}

func (s *fakeVectorStore) Delete(context.Context, ports.VectorCollectionRef, []string) error {
	return nil
}

func (s *fakeVectorStore) DeleteByExpr(_ context.Context, ref ports.VectorCollectionRef, expr string) (int, error) {
	s.deleteExprRef = ref
	s.deleteExpr = expr
	return s.deleteExprCount, nil
}

func (s *fakeVectorStore) Health(context.Context) error {
	return nil
}

func (s *fakeVectorStore) CollectionHealth(context.Context, ports.VectorCollectionRef) (ports.VectorCollectionHealth, error) {
	return ports.VectorCollectionHealth{Ready: true}, nil
}

// fakeVectorStoreWithSearch is a fake VectorStore backend whose Search returns
// configurable hits (including the Content field added by issue #028).
// Embeds fakeVectorStore for the remaining interface methods.
type fakeVectorStoreWithSearch struct {
	fakeVectorStore
	hits []ports.VectorSearchResult
}

func (s *fakeVectorStoreWithSearch) Search(_ context.Context, query ports.VectorSearchQuery) ([]ports.VectorSearchResult, error) {
	out := make([]ports.VectorSearchResult, 0, len(s.hits))
	for _, hit := range s.hits {
		if query.TopK > 0 && len(out) >= query.TopK {
			break
		}
		out = append(out, hit)
	}
	return out, nil
}

func TestLocalVectorStoreServiceDeleteDocumentsRequiresFilter(t *testing.T) {
	service := NewLocalVectorStoreService()
	_, err := service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: "vst-any",
		Filter:     "   ",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("DeleteDocuments error = %v, want ErrInvalid", err)
	}
}

func TestLocalVectorStoreServiceDeleteDocumentsRejectsOversizedFilter(t *testing.T) {
	service := NewLocalVectorStoreService()
	longFilter := strings.Repeat("a", 513)
	_, err := service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: "vst-any",
		Filter:     longFilter,
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("DeleteDocuments error = %v, want ErrInvalid for oversized filter", err)
	}
}

func TestLocalVectorStoreServiceDeleteDocumentsRequiresExistingStore(t *testing.T) {
	service := NewLocalVectorStoreService()
	_, err := service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: "vst-missing",
		Filter:     `doc_id == "abc"`,
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("DeleteDocuments error = %v, want ErrNotFound", err)
	}
}

func TestLocalVectorStoreServiceDeleteDocumentsRequiresReadyStore(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-delete-pending",
		Name:           "pending-index",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	_, err = service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Filter:     `doc_id == "abc"`,
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("DeleteDocuments error = %v, want ErrFailedPrecondition", err)
	}
}

func TestLocalVectorStoreServiceDeleteDocumentsUsesVectorStorePort(t *testing.T) {
	backend := &fakeVectorStore{deleteExprCount: 5}
	service := NewLocalVectorStoreService(WithVectorStoreBackend(backend))
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-delete-docs",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	result, err := service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Filter:     `doc_id == "abc"`,
	})
	if err != nil {
		t.Fatalf("DeleteDocuments() error = %v", err)
	}
	if result.DeletedCount != 5 {
		t.Fatalf("DeletedCount = %d, want 5", result.DeletedCount)
	}
	if backend.deleteExpr != `doc_id == "abc"` {
		t.Fatalf("backend expr = %q, want filter expression", backend.deleteExpr)
	}
	if backend.deleteExprRef.TenantID != "tenant-a" || backend.deleteExprRef.KBID != store.StoreID {
		t.Fatalf("backend ref = %#v, want tenant-a store collection", backend.deleteExprRef)
	}
}

func TestLocalVectorStoreServiceDeleteDocumentsReturnsZeroWithoutBackend(t *testing.T) {
	service := NewLocalVectorStoreService()
	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-store-delete-no-backend",
		Name:           "kb-main",
		Dimension:      3,
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	result, err := service.DeleteDocuments(context.Background(), ports.VectorStoreDocumentDeleteRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Filter:     `doc_id == "abc"`,
	})
	if err != nil {
		t.Fatalf("DeleteDocuments() error = %v", err)
	}
	if result.DeletedCount != 0 {
		t.Fatalf("DeletedCount = %d, want 0 without backend", result.DeletedCount)
	}
}

// TestLocalVectorStoreServiceInsertDocumentsPersistsVectorCountToStore verifies
// that InsertDocuments persists VectorCount/IndexStatus to the resource store
// (H7 regression test). A writer service inserts documents; a separate reader
// (no shared in-memory map) must observe the updated VectorCount via the store.
func TestLocalVectorStoreServiceInsertDocumentsPersistsVectorCountToStore(t *testing.T) {
	store := newSharedMemoryVectorStore()
	clock := func() time.Time { return time.Unix(400, 0).UTC() }
	writer := NewLocalVectorStoreService(WithVectorStoreResourceStore(store), WithVectorStoreServiceClock(clock))
	reader := NewLocalVectorStoreService(WithVectorStoreResourceStore(store), WithVectorStoreServiceClock(clock))

	created, err := writer.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "vector-persist-test",
		Name:           "kb-persist",
		Dimension:      3,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}

	_, err = writer.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       storageStoreTenantID,
		ResourceID:     created.StoreID,
		IdempotencyKey: "insert-persist-test",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-a", Content: "hello"},
			{ID: "doc-b", Content: "world"},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments() error = %v", err)
	}

	// reader does not share writer's in-memory s.stores; it must read from store
	got, err := reader.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.StoreID,
	})
	if err != nil {
		t.Fatalf("reader GetVectorStore() error = %v", err)
	}
	if got.VectorCount != 2 {
		t.Fatalf("VectorCount = %d, want 2 (persisted to store)", got.VectorCount)
	}
	if got.IndexStatus != "ready" {
		t.Fatalf("IndexStatus = %q, want \"ready\"", got.IndexStatus)
	}
	if got.LastIndexedAt.IsZero() {
		t.Fatalf("LastIndexedAt is zero, want non-zero (persisted to store)")
	}
}
