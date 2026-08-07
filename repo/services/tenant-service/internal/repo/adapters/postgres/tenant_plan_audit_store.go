package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantPlanAuditStore 基于 PostgreSQL 实现 ports.TenantPlanAuditStore
// （配额套餐域审计，复用现有 audit_logs 分区表）。
// 本文件当前为【占位实现】：方法体以 panic("not implemented") 标记，
// 仅用于建立编译通过的类型契约，具体 SQL 逻辑由后续审计类 issue 填充。
type PostgresTenantPlanAuditStore struct {
	db *pgxpool.Pool
}

// 编译期断言：确保 PostgresTenantPlanAuditStore 满足 ports.TenantPlanAuditStore 接口。
var _ ports.TenantPlanAuditStore = (*PostgresTenantPlanAuditStore)(nil)

// NewPostgresTenantPlanAuditStore 构造配额套餐域审计存储实例。
func NewPostgresTenantPlanAuditStore(db *pgxpool.Pool) ports.TenantPlanAuditStore {
	return &PostgresTenantPlanAuditStore{db: db}
}

func (s *PostgresTenantPlanAuditStore) Create(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
	panic("not implemented: audit issue")
}

func (s *PostgresTenantPlanAuditStore) ListPlanAuditLogs(ctx context.Context, planID uuid.UUID, filter ports.AuditLogFilter) (ports.AuditLogListResult, error) {
	panic("not implemented: audit issue")
}
