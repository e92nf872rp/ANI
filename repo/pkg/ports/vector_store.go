package ports

import (
	"context"
	"time"
)

type VectorStoreState string

const (
	VectorStorePending  VectorStoreState = "pending"
	VectorStoreReady    VectorStoreState = "ready"
	VectorStoreFailed   VectorStoreState = "failed"
	VectorStoreDeleting VectorStoreState = "deleting"
	VectorStoreDeleted  VectorStoreState = "deleted"
)

type VectorStoreRecord struct {
	TenantID         string
	StoreID          string
	Name             string
	Dimension        int
	Metric           string
	EmbeddingModel   string
	VectorCount      int64
	IndexStatus      string
	LastIndexedAt    time.Time
	KnowledgeBaseRef VectorStoreKnowledgeBaseRef
	State            VectorStoreState
	Reason           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type VectorStoreCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Dimension      int
	Metric         string
	EmbeddingModel string
}

type VectorStoreResourceGetRequest struct {
	TenantID   string
	ResourceID string
}

type VectorStoreRebuildIndexRequest struct {
	TenantID       string
	ResourceID     string
	IdempotencyKey string
}

type VectorStoreResourceListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
}

type VectorStoreResourceSearchRequest struct {
	TenantID   string
	ResourceID string
	Vector     []float32
	TopK       int
	Filter     map[string]string
}

type VectorDocumentInput struct {
	ID       string
	Content  string
	Metadata map[string]string
}

type VectorStoreDocumentInsertRequest struct {
	TenantID       string
	ResourceID     string
	IdempotencyKey string
	Documents      []VectorDocumentInput
}

type VectorStoreDocumentInsertResult struct {
	InsertedCount int
	TaskID        string
	Status        string
}

type VectorStoreKnowledgeBaseRef struct {
	ID     string
	Name   string
	Source string
}

type VectorStoreKnowledgeBaseLinkRequest struct {
	TenantID         string
	ResourceID       string
	IdempotencyKey   string
	KnowledgeBaseRef VectorStoreKnowledgeBaseRef
}

type VectorStoreDeletePrecheck struct {
	Deletable bool
	Reason    string
	Blockers  []VectorStoreDeleteBlocker
}

type VectorStoreDeleteBlocker struct {
	Kind string
	ID   string
	Name string
}

type VectorCollectionRef struct {
	TenantID string
	KBID     string
}

type VectorRecord struct {
	ID       string
	Vector   []float32
	Metadata map[string]string
}

type VectorSearchQuery struct {
	Collection VectorCollectionRef
	Vector     []float32
	TopK       int
	Filter     map[string]string
}

type VectorSearchResult struct {
	ID       string
	Score    float32
	Metadata map[string]string
}

type VectorCollectionHealth struct {
	Ready  bool
	Reason string
}

type VectorStore interface {
	Health(ctx context.Context) error
	EnsureCollection(ctx context.Context, ref VectorCollectionRef, dimension int) error
	Upsert(ctx context.Context, ref VectorCollectionRef, records []VectorRecord) error
	Search(ctx context.Context, query VectorSearchQuery) ([]VectorSearchResult, error)
	Delete(ctx context.Context, ref VectorCollectionRef, ids []string) error
	CollectionHealth(ctx context.Context, ref VectorCollectionRef) (VectorCollectionHealth, error)
}

type VectorStoreService interface {
	CreateVectorStore(ctx context.Context, request VectorStoreCreateRequest) (VectorStoreRecord, error)
	ListVectorStores(ctx context.Context, request VectorStoreResourceListRequest) ([]VectorStoreRecord, error)
	GetVectorStore(ctx context.Context, request VectorStoreResourceGetRequest) (VectorStoreRecord, error)
	DeleteVectorStore(ctx context.Context, request VectorStoreResourceGetRequest) (VectorStoreRecord, error)
	SearchVectorStore(ctx context.Context, request VectorStoreResourceSearchRequest) ([]VectorSearchResult, error)
	RebuildVectorStoreIndex(ctx context.Context, request VectorStoreRebuildIndexRequest) (VectorStoreRecord, error)
	SetVectorStoreKnowledgeBaseLink(ctx context.Context, request VectorStoreKnowledgeBaseLinkRequest) (VectorStoreRecord, error)
	DeleteVectorStoreKnowledgeBaseLink(ctx context.Context, request VectorStoreResourceGetRequest) (VectorStoreRecord, error)
	PrecheckVectorStoreDelete(ctx context.Context, request VectorStoreResourceGetRequest) (VectorStoreDeletePrecheck, error)
	InsertDocuments(ctx context.Context, request VectorStoreDocumentInsertRequest) (VectorStoreDocumentInsertResult, error)
}
