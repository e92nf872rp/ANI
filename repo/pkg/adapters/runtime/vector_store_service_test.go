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
	upsertRef     ports.VectorCollectionRef
	upsertRecords []ports.VectorRecord
	deleteExprRef ports.VectorCollectionRef
	deleteExpr     string
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
