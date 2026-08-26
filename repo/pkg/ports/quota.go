package ports

import (
	"context"
	"time"
)

// ResourceType identifies one of the finite set of quota dimensions tracked by
// the platform. Each resource type must be registered in resource_quota_meta
// and enabled before it can be reserved or managed.
type ResourceType string

const (
	QuotaGPUCount              ResourceType = "gpu_count"
	QuotaCPUCore               ResourceType = "cpu_core"
	QuotaMemoryGB              ResourceType = "memory_gb"
	QuotaStorageGB             ResourceType = "storage_gb"
	QuotaTokenCount            ResourceType = "token_count"
	QuotaKBQueryCount          ResourceType = "kb_query_count"
	QuotaMemberCount           ResourceType = "member_count"
	QuotaInferenceServiceCount ResourceType = "inference_service_count"
)

// QuotaTryRequest describes a single dimension reservation attempt.
type QuotaTryRequest struct {
	TenantID     string
	ResourceType ResourceType
	Amount       int64
}

// QuotaReservation identifies a successfully reserved quota flow. Confirm,
// Cancel and Release operate on TxID.
type QuotaReservation struct {
	TxID      string
	ExpiresAt time.Time
}

// QuotaView is a per-tenant snapshot of every tracked resource dimension.
type QuotaView struct {
	TenantID   string
	TenantName string // from tenants.name (only populated by List; empty otherwise)
	Total      map[ResourceType]int64
	Used       map[ResourceType]int64
	Reserved   map[ResourceType]int64
}

// QuotaPutRequest carries a platform-level quota configuration write. Total is
// keyed by resource type; it is upserted without clamping so an invalid total
// surfaces the CHECK-constraint error to the caller.
type QuotaPutRequest struct {
	TenantID       string
	Total          map[ResourceType]int64
	IdempotencyKey string
}

// QuotaListRequest filters a tenant-level quota listing. An empty TenantID
// returns all tenants paginated by tenant-level keyset cursor.
type QuotaListRequest struct {
	TenantID string // optional filter; empty returns all tenants
	Limit    int
	Cursor   string
}

// QuotaListResult is a page of per-tenant quota views plus paging metadata.
type QuotaListResult struct {
	Items      []QuotaView
	Total      int
	NextCursor string
}

// QuotaService owns the Try-Confirm-Cancel/Release (TCC) reservation state
// machine. Try and TryMany self-open a tenant-scoped transaction because there
// is no business row to attach to before the reservation exists. TryTx and
// TryManyTx accept an external MetadataTx so the caller can reserve quota
// atomically with its own business row insert (e.g. workload instance) in the
// same transaction. Confirm, Cancel and Release also accept an external
// MetadataTx for the same atomic-commit reason.
type QuotaService interface {
	Try(ctx context.Context, req QuotaTryRequest) (QuotaReservation, error)
	TryMany(ctx context.Context, reqs []QuotaTryRequest) ([]QuotaReservation, error)
	// TryTx 单维度预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内执行预占逻辑。
	// 调用方负责开 WithTenantTx 并注入 TenantContext；失败时只返回 err，由外层事务统一回滚。
	TryTx(ctx context.Context, tx MetadataTx, req QuotaTryRequest) (QuotaReservation, error)
	// TryManyTx 多维度批量预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内循环预占。
	// 调用方负责开 WithTenantTx 并注入 TenantContext；任一维度失败只返回 err，由外层事务统一回滚。
	TryManyTx(ctx context.Context, tx MetadataTx, reqs []QuotaTryRequest) ([]QuotaReservation, error)
	Confirm(ctx context.Context, tx MetadataTx, txIDs []string, resourceRef string) error
	Cancel(ctx context.Context, tx MetadataTx, txIDs []string) error
	Release(ctx context.Context, tx MetadataTx, txIDs []string) error
}

// QuotaStoreService exposes configuration reads and writes used by operational
// and tenant-facing handlers. Put and List self-open a platform transaction;
// GetMy self-opens a tenant transaction so RLS filters to the current tenant;
// GetTotalForUpdateTx accepts an external MetadataTx so the caller can lock the
// row inside its own transaction for concurrent reservation validation.
type QuotaStoreService interface {
	Put(ctx context.Context, idempotencyKey string, req QuotaPutRequest) (QuotaView, error)
	List(ctx context.Context, req QuotaListRequest) (QuotaListResult, error)
	GetMy(ctx context.Context, tenantID string) (QuotaView, error)
	GetTotalForUpdateTx(ctx context.Context, tx MetadataTx, tenantID string, rt ResourceType) (int64, error)
}
