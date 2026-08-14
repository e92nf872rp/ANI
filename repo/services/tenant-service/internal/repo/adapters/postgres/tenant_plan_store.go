package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantPlanStore 基于 PostgreSQL 实现 ports.TenantPlanStore。
type PostgresTenantPlanStore struct {
	db *pgxpool.Pool
}

var _ ports.TenantPlanStore = (*PostgresTenantPlanStore)(nil)

// NewPostgresTenantPlanStore 构造套餐存储实例。
func NewPostgresTenantPlanStore(db *pgxpool.Pool) ports.TenantPlanStore {
	return &PostgresTenantPlanStore{db: db}
}

// Create 插入 tenant_plans(status=draft) 与 plan_quota_limits。
func (s *PostgresTenantPlanStore) Create(ctx context.Context, in ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	// 步骤 1：开启本地事务
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ports.TenantPlan{}, fmt.Errorf("begin create tenant plan tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id          uuid.UUID
		code        string
		name        string
		description *string
		status      ports.TenantPlanStatus
		isDeleted   bool
		deletedAt   *time.Time
		createdAt   time.Time
		updatedAt   time.Time
	)

	// 步骤 2：插入套餐主表；status 固定 draft；空 description 存 NULL
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_plans (code, name, description, status)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		RETURNING id, code, name, description, status, is_deleted, deleted_at, created_at, updated_at
	`, in.Code, in.Name, in.Description, ports.TenantPlanStatusDraft).Scan(
		&id, &code, &name, &description, &status, &isDeleted, &deletedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ports.TenantPlan{}, ports.ErrPlanCodeConflict
		}
		return ports.TenantPlan{}, fmt.Errorf("insert tenant_plans: %w", err)
	}

	// 步骤 3：写入各维度限额（service 层已用 default_quota 兜底具体 total）
	for _, lim := range in.QuotaLimits {
		_, err = tx.Exec(ctx, `
			INSERT INTO plan_quota_limits (plan_id, resource_type, total)
			VALUES ($1, $2, $3)
		`, id, lim.ResourceType, lim.Total)
		if err != nil {
			if isUniqueViolation(err) {
				return ports.TenantPlan{}, fmt.Errorf("%w: duplicate resource_type %s", ports.ErrValidationFailed, lim.ResourceType)
			}
			if isForeignKeyViolation(err) {
				return ports.TenantPlan{}, ports.ErrQuotaResourceNotRegistered
			}
			return ports.TenantPlan{}, fmt.Errorf("insert plan_quota_limits: %w", err)
		}
	}

	// 步骤 4：提交事务并组装返回
	if err := tx.Commit(ctx); err != nil {
		return ports.TenantPlan{}, fmt.Errorf("commit create tenant plan tx: %w", err)
	}
	desc := ""
	if description != nil {
		desc = *description
	}
	return ports.TenantPlan{
		ID:          id,
		Code:        code,
		Name:        name,
		Description: desc,
		Status:      status,
		IsDeleted:   isDeleted,
		DeletedAt:   deletedAt,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// GetByID 按主键查询未删除套餐，并附带 tenant_count。
func (s *PostgresTenantPlanStore) GetByID(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	var plan ports.TenantPlan
	err := s.db.QueryRow(ctx, `
		SELECT p.id, p.code, p.name, COALESCE(p.description, ''), p.status, p.is_deleted,
		       p.created_at, p.updated_at,
		       COALESCE((
		         SELECT COUNT(*) FROM tenants t
		         WHERE t.plan_id = p.id AND t.status <> 'disabled'
		       ), 0) AS tenant_count
		FROM tenant_plans p
		WHERE p.id = $1 AND p.is_deleted = FALSE
	`, id).Scan(
		&plan.ID, &plan.Code, &plan.Name, &plan.Description, &plan.Status,
		&plan.IsDeleted, &plan.CreatedAt, &plan.UpdatedAt, &plan.TenantCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
		}
		return ports.TenantPlan{}, fmt.Errorf("get tenant plan by id: %w", err)
	}
	return plan, nil
}

// List 游标分页查询未删除套餐；支持 status / search(name ILIKE)。
func (s *PostgresTenantPlanStore) List(ctx context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	// 步骤 1：limit 边界
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 步骤 2：过滤条件（软删除套餐不出现）
	where := []string{"p.is_deleted = FALSE"}
	args := make([]any, 0, 6)

	status := filter.Status
	if status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("p.status = $%d", len(args)))
	}

	search := strings.TrimSpace(filter.Search)
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, fmt.Sprintf("p.name ILIKE $%d", len(args)))
	}

	whereSQL := strings.Join(where, " AND ")

	// 步骤 3：总条数仅按 status/search，不含 cursor
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM tenant_plans p WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return ports.TenantPlanListResult{}, fmt.Errorf("count tenant plans: %w", err)
	}

	// 步骤 4：游标（created_at DESC, id DESC）；非法 cursor → VALIDATION_FAILED
	listArgs := append([]any{}, args...)
	listWhere := whereSQL
	if cursor := strings.TrimSpace(filter.Cursor); cursor != "" {
		createdAt, id, err := types.DecodeCursor(cursor)
		if err != nil {
			return ports.TenantPlanListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrValidationFailed)
		}
		listArgs = append(listArgs, createdAt, id)
		listWhere += fmt.Sprintf(" AND (p.created_at, p.id) < ($%d, $%d)", len(listArgs)-1, len(listArgs))
	}

	// 步骤 5：多取 1 条判断是否还有下一页；不含 quota_limits
	listArgs = append(listArgs, limit+1)
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.code, p.name, p.description, p.status,
		       COALESCE((
		         SELECT COUNT(*) FROM tenants t
		         WHERE t.plan_id = p.id AND t.status <> 'disabled'
		       ), 0) AS tenant_count,
		       p.created_at, p.updated_at
		FROM tenant_plans p
		WHERE `+listWhere+`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $`+fmt.Sprintf("%d", len(listArgs)), listArgs...)
	if err != nil {
		return ports.TenantPlanListResult{}, fmt.Errorf("list tenant plans: %w", err)
	}
	defer rows.Close()

	// 步骤 6a：扫描本页行
	items := make([]ports.TenantPlanListItem, 0, limit)
	for rows.Next() {
		var (
			item        ports.TenantPlanListItem
			description *string
		)
		if err := rows.Scan(
			&item.ID, &item.Code, &item.Name, &description, &item.Status,
			&item.TenantCount, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return ports.TenantPlanListResult{}, fmt.Errorf("scan tenant plan list item: %w", err)
		}
		if description != nil {
			item.Description = *description
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ports.TenantPlanListResult{}, fmt.Errorf("iterate tenant plan list: %w", err)
	}

	// 步骤 6b：有多余一行则截断并编码 next_cursor；否则已到末页
	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}

	return ports.TenantPlanListResult{
		Items:      items,
		Total:      total,
		NextCursor: nextCursor,
	}, nil
}

// Update 更新套餐基本信息（name / description）；nil 字段表示不更新。
func (s *PostgresTenantPlanStore) Update(ctx context.Context, id uuid.UUID, in ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	// 步骤 1：动态拼 SET（仅非 nil 字段）；始终刷新 updated_at
	sets := []string{"updated_at = now()"}
	args := []any{id}
	argN := 2
	if in.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", argN))
		args = append(args, *in.Name)
		argN++
	}
	if in.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", argN))
		args = append(args, *in.Description)
		argN++
	}

	// 步骤 2：条件更新未删除套餐；命中则 RETURNING 组装实体
	q := fmt.Sprintf(`
		UPDATE tenant_plans
		SET %s
		WHERE id = $1 AND is_deleted = FALSE
		RETURNING id, code, name, COALESCE(description, ''), status, is_deleted, created_at, updated_at
	`, strings.Join(sets, ", "))

	var plan ports.TenantPlan
	err := s.db.QueryRow(ctx, q, args...).Scan(
		&plan.ID, &plan.Code, &plan.Name, &plan.Description, &plan.Status,
		&plan.IsDeleted, &plan.CreatedAt, &plan.UpdatedAt,
	)
	if err == nil {
		return plan, nil
	}

	// 步骤 3：未命中 → 404；其它 DB 错误上抛
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return ports.TenantPlan{}, fmt.Errorf("update tenant plan: %w", err)
}

// Activate 将套餐置为 active（仅 draft/disabled 可转）。
func (s *PostgresTenantPlanStore) Activate(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	// 步骤 1：条件更新 draft|disabled → active；命中则 RETURNING 组装实体
	var plan ports.TenantPlan
	err := s.db.QueryRow(ctx, `
		UPDATE tenant_plans
		SET status = $2, updated_at = now()
		WHERE id = $1
		  AND is_deleted = FALSE
		  AND status IN ($3, $4)
		RETURNING id, code, name, COALESCE(description, ''), status, is_deleted, created_at, updated_at
	`, id, ports.TenantPlanStatusActive, ports.TenantPlanStatusDraft, ports.TenantPlanStatusDisabled).Scan(
		&plan.ID, &plan.Code, &plan.Name, &plan.Description, &plan.Status,
		&plan.IsDeleted, &plan.CreatedAt, &plan.UpdatedAt,
	)
	if err == nil {
		return plan, nil
	}

	// 步骤 2：未命中时区分 DB 错误 / 404 / 409 PLAN_STATE_INVALID
	if !errors.Is(err, pgx.ErrNoRows) {
		return ports.TenantPlan{}, fmt.Errorf("activate tenant plan: %w", err)
	}
	return ports.TenantPlan{}, s.stateTransitionReject(ctx, id)
}

// Disable 将套餐置为 disabled（仅 active 可转）。
func (s *PostgresTenantPlanStore) Disable(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	// 步骤 1：条件更新 active → disabled；命中则 RETURNING 组装实体
	var plan ports.TenantPlan
	err := s.db.QueryRow(ctx, `
		UPDATE tenant_plans
		SET status = $2, updated_at = now()
		WHERE id = $1
		  AND is_deleted = FALSE
		  AND status = $3
		RETURNING id, code, name, COALESCE(description, ''), status, is_deleted, created_at, updated_at
	`, id, ports.TenantPlanStatusDisabled, ports.TenantPlanStatusActive).Scan(
		&plan.ID, &plan.Code, &plan.Name, &plan.Description, &plan.Status,
		&plan.IsDeleted, &plan.CreatedAt, &plan.UpdatedAt,
	)
	if err == nil {
		return plan, nil
	}

	// 步骤 2：未命中时区分 DB 错误 / 404 / 409 PLAN_STATE_INVALID
	if !errors.Is(err, pgx.ErrNoRows) {
		return ports.TenantPlan{}, fmt.Errorf("disable tenant plan: %w", err)
	}
	return ports.TenantPlan{}, s.stateTransitionReject(ctx, id)
}

// Delete 软删除套餐（任意状态）；仅非 disabled 租户绑定会阻止删除（TENANT_PLAN_IN_USE）。
// 软删除不触发 FK CASCADE，plan_quota_limits 行随套餐行保留。
func (s *PostgresTenantPlanStore) Delete(ctx context.Context, id uuid.UUID) error {
	// 步骤 1：未删除套餐须存在，否则 404
	var exists bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM tenant_plans WHERE id = $1 AND is_deleted = FALSE
		)
	`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check tenant plan exists: %w", err)
	}
	if !exists {
		return ports.ErrTenantPlanNotFound
	}

	// 步骤 2：仅统计未 disabled 的绑定租户（disabled 等同已删除、不可恢复）→ 有则 409 TENANT_PLAN_IN_USE
	var bound int64
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM tenants WHERE plan_id = $1 AND status <> 'disabled'
	`, id).Scan(&bound); err != nil {
		return fmt.Errorf("count bound tenants: %w", err)
	}
	if bound > 0 {
		return ports.ErrTenantPlanInUse
	}

	// 步骤 3：软删除（is_deleted + deleted_at）；RowsAffected=0 再兜底 404
	tag, err := s.db.Exec(ctx, `
		UPDATE tenant_plans
		SET is_deleted = TRUE, deleted_at = now(), updated_at = now()
		WHERE id = $1 AND is_deleted = FALSE
	`, id)
	if err != nil {
		return fmt.Errorf("soft delete tenant plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrTenantPlanNotFound
	}
	return nil
}

func (s *PostgresTenantPlanStore) GetQuotaLimits(ctx context.Context, planID uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	// 步骤 1：套餐须存在且未删除
	if err := s.requirePlanExists(ctx, planID); err != nil {
		return nil, err
	}

	// 步骤 2：读原始限额行
	rows, err := s.db.Query(ctx, `
		SELECT plan_id, resource_type, total
		FROM plan_quota_limits
		WHERE plan_id = $1
		ORDER BY resource_type
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("get quota limits: %w", err)
	}
	defer rows.Close()

	// 步骤 3：扫描结果集
	out := make([]ports.PlanQuotaLimit, 0)
	for rows.Next() {
		var item ports.PlanQuotaLimit
		if err := rows.Scan(&item.PlanID, &item.ResourceType, &item.Total); err != nil {
			return nil, fmt.Errorf("scan quota limit: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quota limits: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantPlanStore) UpdateQuotaLimits(ctx context.Context, planID uuid.UUID, items []ports.PlanQuotaLimitInput) error {
	// 步骤 1：套餐须存在（任意状态均可改限额）
	if err := s.requirePlanExists(ctx, planID); err != nil {
		return err
	}

	// 步骤 2：开启事务
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update quota limits tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 步骤 3：逐项 UPSERT（service 已将 nil total 替换为 default_quota）
	for _, it := range items {
		_, err = tx.Exec(ctx, `
			INSERT INTO plan_quota_limits (plan_id, resource_type, total)
			VALUES ($1, $2, $3)
			ON CONFLICT (plan_id, resource_type) DO UPDATE
			SET total = EXCLUDED.total
		`, planID, it.ResourceType, it.Total)
		if err != nil {
			if isForeignKeyViolation(err) {
				return ports.ErrQuotaResourceNotRegistered
			}
			return fmt.Errorf("upsert plan_quota_limits: %w", err)
		}
	}

	// 步骤 4：触摸套餐 updated_at
	if _, err := tx.Exec(ctx, `
		UPDATE tenant_plans SET updated_at = now() WHERE id = $1 AND is_deleted = FALSE
	`, planID); err != nil {
		return fmt.Errorf("touch tenant plan updated_at: %w", err)
	}

	// 步骤 5：提交事务
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update quota limits tx: %w", err)
	}
	return nil
}

func (s *PostgresTenantPlanStore) ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	// 步骤 1：套餐须存在
	if err := s.requirePlanExists(ctx, planID); err != nil {
		return nil, err
	}

	// 步骤 2：查询非 disabled 绑定租户（与删除/tenant_count 语义一致）
	rows, err := s.db.Query(ctx, `
		SELECT id, name, display_name, status
		FROM tenants
		WHERE plan_id = $1 AND status <> 'disabled'
		ORDER BY name
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("list bound tenants: %w", err)
	}
	defer rows.Close()

	// 步骤 3：扫描摘要列表
	out := make([]ports.BoundTenant, 0)
	for rows.Next() {
		var t ports.BoundTenant
		if err := rows.Scan(&t.ID, &t.Name, &t.DisplayName, &t.Status); err != nil {
			return nil, fmt.Errorf("scan bound tenant: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bound tenants: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantPlanStore) ListBindableTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	// 步骤 1：套餐须存在（否则 TENANT_PLAN_NOT_FOUND）
	if err := s.requirePlanExists(ctx, planID); err != nil {
		return nil, err
	}

	// 步骤 2：可绑定 = 非 disabled 且未绑定本套餐（含 plan_id NULL / 其它套餐）
	rows, err := s.db.Query(ctx, `
		SELECT id, name, display_name, status
		FROM tenants
		WHERE status <> 'disabled'
		  AND plan_id IS DISTINCT FROM $1
		ORDER BY name
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("list bindable tenants: %w", err)
	}
	defer rows.Close()

	// 步骤 3：扫描摘要列表
	out := make([]ports.BoundTenant, 0)
	for rows.Next() {
		var t ports.BoundTenant
		if err := rows.Scan(&t.ID, &t.Name, &t.DisplayName, &t.Status); err != nil {
			return nil, fmt.Errorf("scan bindable tenant: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bindable tenants: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantPlanStore) GetApprovedQuotaChanges(ctx context.Context, tenantID uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	// 步骤 1：查 tenant_quota_change 中 status=approved 的维度
	rows, err := s.db.Query(ctx, `
		SELECT tenant_id, resource_type
		FROM tenant_quota_change
		WHERE tenant_id = $1 AND status = 'approved'
		ORDER BY resource_type
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get approved quota changes: %w", err)
	}
	defer rows.Close()

	// 步骤 2：扫描结果（供同步时跳过覆盖）
	out := make([]ports.ApprovedQuotaChange, 0)
	for rows.Next() {
		var item ports.ApprovedQuotaChange
		if err := rows.Scan(&item.TenantID, &item.ResourceType); err != nil {
			return nil, fmt.Errorf("scan approved quota change: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approved quota changes: %w", err)
	}
	return out, nil
}

// requirePlanExists 校验未删除套餐存在，否则 ErrTenantPlanNotFound。
func (s *PostgresTenantPlanStore) requirePlanExists(ctx context.Context, id uuid.UUID) error {
	// 步骤 1：EXISTS 查询未删除套餐
	var exists bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tenant_plans WHERE id = $1 AND is_deleted = FALSE)
	`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check tenant plan exists: %w", err)
	}
	// 步骤 2：不存在 → 404
	if !exists {
		return ports.ErrTenantPlanNotFound
	}
	return nil
}

// ── helpers（勿穿插到上方 Store 方法中间）────────────────────────────────

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// stateTransitionReject 在状态 UPDATE 未命中时区分 404 / 409。
func (s *PostgresTenantPlanStore) stateTransitionReject(ctx context.Context, id uuid.UUID) error {
	// 步骤 1：查未删除套餐当前 status
	var status ports.TenantPlanStatus
	err := s.db.QueryRow(ctx, `
		SELECT status FROM tenant_plans WHERE id = $1 AND is_deleted = FALSE
	`, id).Scan(&status)

	// 步骤 2：不存在 → 404；存在但状态不满足守卫 → 409
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrTenantPlanNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup tenant plan status: %w", err)
	}
	return fmt.Errorf("%w: current status %s", ports.ErrPlanStateInvalid, status)
}
