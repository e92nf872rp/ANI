package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantPlanStore 基于 PostgreSQL 实现 ports.TenantPlanStore。
// 本文件当前为【占位实现】：方法体以 panic("not implemented") 标记，
// 仅用于建立编译通过的类型契约，具体 SQL 逻辑由后续 issue 填充。
type PostgresTenantPlanStore struct {
	db *pgxpool.Pool
}

// 编译期断言：确保 PostgresTenantPlanStore 满足 ports.TenantPlanStore 接口。
var _ ports.TenantPlanStore = (*PostgresTenantPlanStore)(nil)

// NewPostgresTenantPlanStore 构造套餐存储实例。
func NewPostgresTenantPlanStore(db *pgxpool.Pool) ports.TenantPlanStore {
	return &PostgresTenantPlanStore{db: db}
}

func (s *PostgresTenantPlanStore) Create(ctx context.Context, in ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) GetByID(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) GetByCode(ctx context.Context, code string) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) List(ctx context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) Update(ctx context.Context, id uuid.UUID, in ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) Activate(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) Disable(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) Delete(ctx context.Context, id uuid.UUID) error {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) GetQuotaLimits(ctx context.Context, planID uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) GetQuotaLimitViews(ctx context.Context, planID uuid.UUID) ([]ports.PlanQuotaLimitView, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) UpdateQuotaLimits(ctx context.Context, planID uuid.UUID, items []ports.PlanQuotaLimitInput) error {
	panic("not implemented: issue-006")
}

func (s *PostgresTenantPlanStore) ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	panic("not implemented: issue-004")
}

func (s *PostgresTenantPlanStore) GetApprovedQuotaChanges(ctx context.Context, tenantID uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("not implemented: issue-004")
}
