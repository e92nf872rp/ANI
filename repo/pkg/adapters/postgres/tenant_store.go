package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
)

// PostgresTenantStore 基于 PostgreSQL 实现 ports.TenantStore（最小版）。
// 本文件当前为【占位实现】：方法体以 panic("not implemented") 标记，
// 仅用于建立编译通过的类型契约，具体 SQL 逻辑由后续 issue 填充。
// 完整 TenantStore（Create/List/Freeze/Disable 等）由独立租户管理 PR 补充。
type PostgresTenantStore struct {
	db *pgxpool.Pool
}

// 编译期断言：确保 PostgresTenantStore 满足 ports.TenantStore 接口。
var _ ports.TenantStore = (*PostgresTenantStore)(nil)

// NewTenantStore 构造租户存储实例。
func NewTenantStore(db *pgxpool.Pool) ports.TenantStore {
	return &PostgresTenantStore{db: db}
}

// GetByID 按主键查询租户（返回 status / plan_id）。
func (s *PostgresTenantStore) GetByID(ctx context.Context, id uuid.UUID) (ports.Tenant, error) {
	panic("not implemented: issue-007")
}

// UpdatePlan 仅更新 tenants.plan_id，不影响配额。
func (s *PostgresTenantStore) UpdatePlan(ctx context.Context, id uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
	panic("not implemented: issue-007")
}
