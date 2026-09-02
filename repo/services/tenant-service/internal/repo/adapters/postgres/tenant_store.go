package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantStore 是 TenantStore 的 PostgreSQL 适配器。
// 操作 tenant_lifecycle / tenant_quota_change；不直接读写 tenants / tenant_auth。
type PostgresTenantStore struct {
	db *pgxpool.Pool
}

var _ ports.TenantStore = (*PostgresTenantStore)(nil)

// NewPostgresTenantStore 构造租户本地表存储。
func NewPostgresTenantStore(db *pgxpool.Pool) ports.TenantStore {
	return &PostgresTenantStore{db: db}
}

func (s *PostgresTenantStore) ListLifecycle(context.Context, uuid.UUID, ports.TenantLifecycleFilter) (ports.TenantLifecycleListResult, error) {
	return ports.TenantLifecycleListResult{}, ports.ErrNotImplemented
}

func (s *PostgresTenantStore) UpsertPendingQuotaChanges(context.Context, uuid.UUID, []ports.QuotaChangePendingInput) error {
	return ports.ErrNotImplemented
}

func (s *PostgresTenantStore) ListQuotaChangesByTenant(context.Context, uuid.UUID, string) ([]ports.QuotaChangeRequest, error) {
	return nil, ports.ErrNotImplemented
}

func (s *PostgresTenantStore) GetQuotaChangeByID(context.Context, uuid.UUID, uuid.UUID) (ports.QuotaChangeRequest, error) {
	return ports.QuotaChangeRequest{}, ports.ErrNotImplemented
}

func (s *PostgresTenantStore) SetQuotaChangeStatus(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) (int64, error) {
	return 0, ports.ErrNotImplemented
}
