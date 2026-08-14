package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantStore 基于 PostgreSQL 实现 ports.TenantStore（最小版）。
// 仅覆盖绑定套餐所需：GetByID（判 status）/ UpdatePlan（改 plan_id）。
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
	// 步骤 1：按主键读租户行（仅选现有列）
	var t ports.Tenant
	err := s.db.QueryRow(ctx, `
		SELECT id, name, display_name, status, plan_id, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`, id).Scan(
		&t.ID, &t.Name, &t.DisplayName, &t.Status, &t.PlanID, &t.CreatedAt, &t.UpdatedAt,
	)

	// 步骤 2：映射 not found / 其它错误
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	if err != nil {
		return ports.Tenant{}, fmt.Errorf("get tenant by id: %w", err)
	}
	return t, nil
}

// UpdatePlan 仅更新 tenants.plan_id，不影响配额。
func (s *PostgresTenantStore) UpdatePlan(ctx context.Context, id uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
	// 步骤 1：更新 plan_id 并回读
	var t ports.Tenant
	err := s.db.QueryRow(ctx, `
		UPDATE tenants
		SET plan_id = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, name, display_name, status, plan_id, created_at, updated_at
	`, id, planID).Scan(
		&t.ID, &t.Name, &t.DisplayName, &t.Status, &t.PlanID, &t.CreatedAt, &t.UpdatedAt,
	)

	// 步骤 2：映射 not found / 其它错误（含 plan_id FK）
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	if err != nil {
		if isForeignKeyViolation(err) {
			return ports.Tenant{}, ports.ErrTenantPlanNotFound
		}
		return ports.Tenant{}, fmt.Errorf("update tenant plan: %w", err)
	}
	return t, nil
}
