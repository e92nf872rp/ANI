package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresBillingStore 基于 PostgreSQL 实现 ports.BillingStore。
// 对应表：billing_invoices / billing_adjustments / billing_credit_accounts / billing_pricing
// （迁移 20260907_001_tenant_billing.sql；计费结算域方案 §6.1）；
// 操作流水表 billing_operation_logs（迁移 20260908_001_billing_operation_logs.sql），
// 与业务写同一事务落库：业务写失败回滚则流水不落库。
type PostgresBillingStore struct {
	db *pgxpool.Pool
}

var _ ports.BillingStore = (*PostgresBillingStore)(nil)

// billingQuerier 抽象 *pgxpool.Pool 与 pgx.Tx 的 QueryRow（同事务写入用；
// 流水落库也走 QueryRow ... RETURNING，避免引入 Exec 依赖）。
type billingQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewPostgresBillingStore 构造计费结算存储实例。
func NewPostgresBillingStore(db *pgxpool.Pool) ports.BillingStore {
	return &PostgresBillingStore{db: db}
}

// ListInvoicesByTenant 返回该租户全部账单（issued_at 倒序；软删行不返回）。
func (s *PostgresBillingStore) ListInvoicesByTenant(ctx context.Context, tenantID uuid.UUID) ([]ports.BillingInvoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY issued_at DESC, created_at DESC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list billing invoices: %w", err)
	}
	defer rows.Close()

	out := make([]ports.BillingInvoice, 0)
	for rows.Next() {
		inv, scanErr := scanBillingInvoice(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing invoices: %w", err)
	}
	return out, nil
}

// GetInvoiceByPeriod 返回该租户该账期的活跃账单（软删行视为无账单，可重新出账）；无账单返回 nil（不视为错误）。
func (s *PostgresBillingStore) GetInvoiceByPeriod(ctx context.Context, tenantID uuid.UUID, period string) (*ports.BillingInvoice, error) {
	inv, err := queryBillingInvoice(ctx, s.db, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE tenant_id = $1 AND period = $2 AND deleted_at IS NULL
	`, tenantID, period)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// GetInvoice 按 id 查活跃账单（软删行不可见，404）；供结清/授信/删除预读组装流水摘要。
func (s *PostgresBillingStore) GetInvoice(ctx context.Context, id uuid.UUID) (*ports.BillingInvoice, error) {
	inv, err := queryBillingInvoice(ctx, s.db, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// CountInvoicesByNoPrefix 统计账单号前缀匹配数（账单号 seq 生成用；含软删行——历史单号不复用）。
func (s *PostgresBillingStore) CountInvoicesByNoPrefix(ctx context.Context, prefix string) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing_invoices WHERE no LIKE $1 || '%'
	`, prefix).Scan(&count); err != nil {
		return 0, fmt.Errorf("count billing invoices by no prefix: %w", err)
	}
	return count, nil
}

// CreateInvoice 插入账单（status 由入参隐含为 issued，落库固定 issued）；
// (tenant_id, period) / no / idempotency_key 任一唯一冲突 → ErrBillingInvoiceExists
// （由 service 重查后区分重放 / 409 / 撞号重试）。
// in.Operation 非 nil 时与账单插入同一事务落一条操作流水；
// 唯一冲突（事务回滚）或流水写入失败均不落库、不落流水。
func (s *PostgresBillingStore) CreateInvoice(ctx context.Context, in ports.CreateBillingInvoiceInput) (*ports.BillingInvoice, error) {
	if in.Operation == nil {
		inv, err := queryBillingInvoice(ctx, s.db, insertBillingInvoiceSQL,
			in.TenantID, in.Period, in.No, in.AmountUSD, ports.BillingInvoiceIssued,
			in.DueDate, in.IssuedAt, in.IdempotencyKey)
		if err != nil {
			return nil, mapBillingInvoiceInsertErr(err)
		}
		return inv, nil
	}

	// 事务路径：账单插入 + 操作流水原子落库
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin billing invoice transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 已 Commit 时为 no-op

	inv, err := queryBillingInvoice(ctx, tx, insertBillingInvoiceSQL,
		in.TenantID, in.Period, in.No, in.AmountUSD, ports.BillingInvoiceIssued,
		in.DueDate, in.IssuedAt, in.IdempotencyKey)
	if err != nil {
		return nil, mapBillingInvoiceInsertErr(err) // 返回前 defer Rollback 生效
	}
	period, refID := in.Period, inv.ID
	if err := insertBillingOperationLog(ctx, tx, in.TenantID, &period, &refID, in.Operation); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit billing invoice transaction: %w", err)
	}
	return inv, nil
}

// UpdateInvoiceStatus 以 status='issued' 为 CAS 前提应用状态迁移；不满足 → ErrBillingStateConflict；
// 账单不存在 → ErrBillingInvoiceNotFound。
// op 非 nil 时与状态迁移同一事务落一条操作流水；CAS 失败（404/409）则事务回滚、不落流水。
func (s *PostgresBillingStore) UpdateInvoiceStatus(ctx context.Context, id uuid.UUID, action ports.BillingInvoiceAction, at time.Time, op *ports.BillingOperationLogInput) (*ports.BillingInvoice, error) {
	// 步骤 1：按 action 组装目标状态与时间戳列（issued → settled | credited，终态）
	var status ports.BillingInvoiceStatus
	var settledAt, creditedAt *time.Time
	switch action {
	case ports.BillingActionSettle:
		status = ports.BillingInvoiceSettled
		settledAt = &at
	case ports.BillingActionCredit:
		status = ports.BillingInvoiceCredited
		creditedAt = &at
	default:
		return nil, ports.ErrBillingActionInvalid
	}

	// 步骤 2：无流水要求时走直写路径（条件更新 + 404/409 区分）
	if op == nil {
		return s.casUpdateInvoice(ctx, s.db, id, status, settledAt, creditedAt)
	}

	// 步骤 3：事务路径——CAS 状态迁移 + 操作流水原子落库；
	// CAS 未命中（404/409）在事务内甄别后回滚，不落流水。
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin billing invoice action transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 已 Commit 时为 no-op

	inv, err := queryBillingInvoice(ctx, tx, casUpdateBillingInvoiceSQL,
		id, status, settledAt, creditedAt, ports.BillingInvoiceIssued)
	if err != nil {
		if errors.Is(err, ports.ErrBillingInvoiceNotFound) {
			// 未命中时区分 404（账单不存在或已删除——软删行不可见）与 409（状态不满足 CAS）
			var exists bool
			if existsErr := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM billing_invoices WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists); existsErr != nil {
				return nil, fmt.Errorf("check billing invoice exists: %w", existsErr)
			}
			if !exists {
				return nil, ports.ErrBillingInvoiceNotFound
			}
			return nil, ports.ErrBillingStateConflict
		}
		return nil, err
	}
	period, refID := inv.Period, inv.ID
	if err := insertBillingOperationLog(ctx, tx, inv.TenantID, &period, &refID, op); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit billing invoice action transaction: %w", err)
	}
	return inv, nil
}

// casUpdateInvoice 直写路径的条件更新（无事务流水要求时）；命中则 RETURNING 组装实体，
// 未命中区分 404 / 409。
func (s *PostgresBillingStore) casUpdateInvoice(ctx context.Context, q billingQuerier, id uuid.UUID, status ports.BillingInvoiceStatus, settledAt, creditedAt *time.Time) (*ports.BillingInvoice, error) {
	inv, err := queryBillingInvoice(ctx, q, casUpdateBillingInvoiceSQL,
		id, status, settledAt, creditedAt, ports.BillingInvoiceIssued)
	if err != nil {
		if errors.Is(err, ports.ErrBillingInvoiceNotFound) {
			var exists bool
			if existsErr := s.db.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM billing_invoices WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists); existsErr != nil {
				return nil, fmt.Errorf("check billing invoice exists: %w", existsErr)
			}
			if !exists {
				return nil, ports.ErrBillingInvoiceNotFound
			}
			return nil, ports.ErrBillingStateConflict
		}
		return nil, err
	}
	return inv, nil
}

// SoftDeleteInvoice 软删除账单（deleted_at 标记，行保留可审计）；
// 仅活跃 issued 行可删：CAS 前提 status='issued' AND deleted_at IS NULL；
// 终态（settled/credited）→ ErrBillingStateConflict；不存在或已删除 → ErrBillingInvoiceNotFound
// （重复删除 404 幂等无害）。op 非 nil 时与软删同一事务落一条 invoice.deleted 流水；
// CAS 失败（404/409）则事务回滚、不落流水。成功返回删除前账单快照（status 仍为 issued）。
func (s *PostgresBillingStore) SoftDeleteInvoice(ctx context.Context, id uuid.UUID, at time.Time, op *ports.BillingOperationLogInput) (*ports.BillingInvoice, error) {
	// 步骤 1：无流水要求时走直写路径（条件更新 + 404/409 区分）
	if op == nil {
		return s.softDeleteInvoiceDirect(ctx, s.db, id, at)
	}

	// 步骤 2：事务路径——CAS 软删 + invoice.deleted 流水原子落库；
	// CAS 未命中（404/409）在事务内甄别后回滚，不落流水。
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin billing invoice delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 已 Commit 时为 no-op

	inv, err := softDeleteBillingInvoice(ctx, tx, id, at)
	if err != nil {
		if errors.Is(err, ports.ErrBillingInvoiceNotFound) {
			// 未命中时甄别：活跃行不存在但终态行存在 → 409；活跃行不存在（含已删除）→ 404
			var exists bool
			if existsErr := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM billing_invoices WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists); existsErr != nil {
				return nil, fmt.Errorf("check billing invoice exists: %w", existsErr)
			}
			if !exists {
				return nil, ports.ErrBillingInvoiceNotFound
			}
			return nil, ports.ErrBillingStateConflict
		}
		return nil, err
	}
	period, refID := inv.Period, inv.ID
	if err := insertBillingOperationLog(ctx, tx, inv.TenantID, &period, &refID, op); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit billing invoice delete transaction: %w", err)
	}
	return inv, nil
}

// softDeleteInvoiceDirect 直写路径的软删（无事务流水要求时）；未命中区分 404 / 409：
// 活跃行（未删除）不存在 → 已删除返回 404、终态返回 409。
func (s *PostgresBillingStore) softDeleteInvoiceDirect(ctx context.Context, q billingQuerier, id uuid.UUID, at time.Time) (*ports.BillingInvoice, error) {
	inv, err := softDeleteBillingInvoice(ctx, q, id, at)
	if err != nil {
		if errors.Is(err, ports.ErrBillingInvoiceNotFound) {
			var exists bool
			if existsErr := s.db.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM billing_invoices WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists); existsErr != nil {
				return nil, fmt.Errorf("check billing invoice exists: %w", existsErr)
			}
			if !exists {
				return nil, ports.ErrBillingInvoiceNotFound
			}
			return nil, ports.ErrBillingStateConflict
		}
		return nil, err
	}
	return inv, nil
}

// ListAdjustmentsByTenant 返回该租户全部调账（created_at 升序）。
func (s *PostgresBillingStore) ListAdjustmentsByTenant(ctx context.Context, tenantID uuid.UUID) ([]ports.BillingAdjustment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, period, amount_usd, reason, operator, idempotency_key, created_at
		FROM billing_adjustments
		WHERE tenant_id = $1
		ORDER BY created_at ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list billing adjustments: %w", err)
	}
	defer rows.Close()

	out := make([]ports.BillingAdjustment, 0)
	for rows.Next() {
		var adj ports.BillingAdjustment
		if scanErr := rows.Scan(&adj.ID, &adj.TenantID, &adj.Period, &adj.AmountUSD,
			&adj.Reason, &adj.Operator, &adj.IdempotencyKey, &adj.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan billing adjustment: %w", scanErr)
		}
		out = append(out, adj)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing adjustments: %w", err)
	}
	return out, nil
}

// GetAdjustmentByIdempotencyKey 按幂等键查调账；无则返回 nil。
func (s *PostgresBillingStore) GetAdjustmentByIdempotencyKey(ctx context.Context, key uuid.UUID) (*ports.BillingAdjustment, error) {
	var adj ports.BillingAdjustment
	err := s.db.QueryRow(ctx, `
		SELECT id, tenant_id, period, amount_usd, reason, operator, idempotency_key, created_at
		FROM billing_adjustments
		WHERE idempotency_key = $1
	`, key).Scan(&adj.ID, &adj.TenantID, &adj.Period, &adj.AmountUSD,
		&adj.Reason, &adj.Operator, &adj.IdempotencyKey, &adj.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get billing adjustment by idempotency key: %w", err)
	}
	return &adj, nil
}

// CreateAdjustment 插入调账记录；幂等键唯一冲突 → ErrBillingIdempotencyConflict
// （由 service 重查后按重放语义返回已有记录）。
// in.Operation 非 nil 时与调账插入同一事务落一条操作流水；
// 唯一冲突（事务回滚）或流水写入失败均不落库、不落流水。
func (s *PostgresBillingStore) CreateAdjustment(ctx context.Context, in ports.CreateBillingAdjustmentInput) (*ports.BillingAdjustment, error) {
	if in.Operation == nil {
		return insertBillingAdjustment(ctx, s.db, in)
	}

	// 事务路径：调账插入 + 操作流水原子落库
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin billing adjustment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 已 Commit 时为 no-op

	adj, err := insertBillingAdjustment(ctx, tx, in)
	if err != nil {
		return nil, err // 返回前 defer Rollback 生效
	}
	period, refID := adj.Period, adj.ID
	if err := insertBillingOperationLog(ctx, tx, adj.TenantID, &period, &refID, in.Operation); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit billing adjustment transaction: %w", err)
	}
	return adj, nil
}

// GetCredit 返回该租户授信额度；无账户行返回 nil。
func (s *PostgresBillingStore) GetCredit(ctx context.Context, tenantID uuid.UUID) (*float64, error) {
	var credit float64
	err := s.db.QueryRow(ctx, `
		SELECT credit_usd FROM billing_credit_accounts WHERE tenant_id = $1
	`, tenantID).Scan(&credit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get billing credit: %w", err)
	}
	return &credit, nil
}

// ListPricing 返回全部定价行（代码零单价字面量，折算只读表）。
func (s *PostgresBillingStore) ListPricing(ctx context.Context) ([]ports.BillingPricing, error) {
	rows, err := s.db.Query(ctx, `
		SELECT resource_type, display_metric, unit_cost
		FROM billing_pricing
		ORDER BY display_metric
	`)
	if err != nil {
		return nil, fmt.Errorf("list billing pricing: %w", err)
	}
	defer rows.Close()

	out := make([]ports.BillingPricing, 0)
	for rows.Next() {
		var p ports.BillingPricing
		if scanErr := rows.Scan(&p.ResourceType, &p.DisplayMetric, &p.UnitCost); scanErr != nil {
			return nil, fmt.Errorf("scan billing pricing: %w", scanErr)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing pricing: %w", err)
	}
	return out, nil
}

// ListBillingTenantIDs 返回计费域有足迹（活跃账单/调账/授信）的租户集合；
// 软删账单不算足迹（删除后无其他足迹的租户行从总览消失）；
// tenantFilter 非 nil 时仅返回该租户（存在足迹才返回，无足迹返回空）。
func (s *PostgresBillingStore) ListBillingTenantIDs(ctx context.Context, tenantFilter *uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT tenant_id FROM (
			SELECT tenant_id FROM billing_invoices WHERE deleted_at IS NULL
			UNION
			SELECT tenant_id FROM billing_adjustments
			UNION
			SELECT tenant_id FROM billing_credit_accounts
		) footprints
		WHERE ($1::uuid IS NULL OR tenant_id = $1)
		ORDER BY tenant_id
	`, tenantFilter)
	if err != nil {
		return nil, fmt.Errorf("list billing tenant ids: %w", err)
	}
	defer rows.Close()

	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan billing tenant id: %w", scanErr)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing tenant ids: %w", err)
	}
	return out, nil
}

// ListOperations 返回该租户操作流水（created_at 倒序、分页）；
// total 为过滤后总条数（分页用）。无流水返回空切片（不视为错误）。
func (s *PostgresBillingStore) ListOperations(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]ports.BillingOperationLog, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM billing_operation_logs WHERE tenant_id = $1
	`, tenantID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count billing operation logs: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, period, action, ref_id, message, operator, created_at
		FROM billing_operation_logs
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, tenantID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list billing operation logs: %w", err)
	}
	defer rows.Close()

	out := make([]ports.BillingOperationLog, 0)
	for rows.Next() {
		var log ports.BillingOperationLog
		if scanErr := rows.Scan(&log.ID, &log.TenantID, &log.Period, &log.Action,
			&log.RefID, &log.Message, &log.Operator, &log.CreatedAt); scanErr != nil {
			return nil, 0, fmt.Errorf("scan billing operation log: %w", scanErr)
		}
		out = append(out, log)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate billing operation logs: %w", err)
	}
	return out, total, nil
}

// ── helpers（勿穿插到上方 Store 方法中间）────────────────────────────────

// insertBillingInvoiceSQL 账单插入语句（直写路径与流水事务路径共用）。
const insertBillingInvoiceSQL = `
	INSERT INTO billing_invoices
		(tenant_id, period, no, amount_usd, status, due_date, issued_at, idempotency_key)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	RETURNING id, tenant_id, period, no, amount_usd, status, due_date,
	          issued_at, settled_at, credited_at, idempotency_key, created_at
`

// casUpdateBillingInvoiceSQL 账单状态 CAS 迁移语句（仅活跃 issued 行可迁移，软删行不可见；
// 直写与事务路径共用）。
const casUpdateBillingInvoiceSQL = `
	UPDATE billing_invoices
	SET status = $2, settled_at = $3, credited_at = $4
	WHERE id = $1 AND status = $5 AND deleted_at IS NULL
	RETURNING id, tenant_id, period, no, amount_usd, status, due_date,
	          issued_at, settled_at, credited_at, idempotency_key, created_at
`

// softDeleteBillingInvoiceSQL 账单软删语句（仅活跃 issued 行可删；直写与事务路径共用）。
const softDeleteBillingInvoiceSQL = `
	UPDATE billing_invoices
	SET deleted_at = $2
	WHERE id = $1 AND status = $3 AND deleted_at IS NULL
	RETURNING id, tenant_id, period, no, amount_usd, status, due_date,
	          issued_at, settled_at, credited_at, idempotency_key, created_at
`

// softDeleteBillingInvoice 在给定 querier（pool 或 tx）上执行账单软删并组装快照实体；
// CAS 未命中统一映射为 ErrBillingInvoiceNotFound（由调用方甄别 404 与 409）。
func softDeleteBillingInvoice(ctx context.Context, q billingQuerier, id uuid.UUID, at time.Time) (*ports.BillingInvoice, error) {
	return queryBillingInvoice(ctx, q, softDeleteBillingInvoiceSQL, id, at, ports.BillingInvoiceIssued)
}

// mapBillingInvoiceInsertErr 将账单插入错误统一映射（唯一冲突 → ErrBillingInvoiceExists）。
func mapBillingInvoiceInsertErr(err error) error {
	if isUniqueViolation(err) {
		return ports.ErrBillingInvoiceExists
	}
	return err
}

// queryBillingInvoice 在给定 querier（pool 或 tx）上执行返回单行账单的查询；
// ErrNoRows 统一映射为 ErrBillingInvoiceNotFound。
func queryBillingInvoice(ctx context.Context, q billingQuerier, sql string, args ...any) (*ports.BillingInvoice, error) {
	var inv ports.BillingInvoice
	err := q.QueryRow(ctx, sql, args...).Scan(
		&inv.ID, &inv.TenantID, &inv.Period, &inv.No, &inv.AmountUSD, &inv.Status, &inv.DueDate,
		&inv.IssuedAt, &inv.SettledAt, &inv.CreditedAt, &inv.IdempotencyKey, &inv.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.ErrBillingInvoiceNotFound
		}
		return nil, fmt.Errorf("query billing invoice: %w", err)
	}
	return &inv, nil
}

// insertBillingAdjustment 在给定 querier（pool 或 tx）上插入调账记录并组装实体；
// 幂等键唯一冲突 → ErrBillingIdempotencyConflict。
func insertBillingAdjustment(ctx context.Context, q billingQuerier, in ports.CreateBillingAdjustmentInput) (*ports.BillingAdjustment, error) {
	var adj ports.BillingAdjustment
	err := q.QueryRow(ctx, `
		INSERT INTO billing_adjustments (tenant_id, period, amount_usd, reason, operator, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, period, amount_usd, reason, operator, idempotency_key, created_at
	`, in.TenantID, in.Period, in.AmountUSD, in.Reason, in.Operator, in.IdempotencyKey).Scan(
		&adj.ID, &adj.TenantID, &adj.Period, &adj.AmountUSD,
		&adj.Reason, &adj.Operator, &adj.IdempotencyKey, &adj.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ports.ErrBillingIdempotencyConflict
		}
		return nil, fmt.Errorf("insert billing adjustment: %w", err)
	}
	return &adj, nil
}

// insertBillingOperationLog 在给定 querier（pool 或 tx）上落一条操作流水；
// tenant_id / period / ref_id 以业务落库事实为准，由调用方从业务行回填。
// 仅应在与业务写相同的事务内调用（q 为 tx），保证业务写失败回滚则流水不落库。
func insertBillingOperationLog(ctx context.Context, q billingQuerier, tenantID uuid.UUID, period *string, refID *uuid.UUID, op *ports.BillingOperationLogInput) error {
	var id uuid.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO billing_operation_logs (tenant_id, period, action, ref_id, message, operator)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, tenantID, period, op.Action, refID, op.Message, op.Operator).Scan(&id); err != nil {
		return fmt.Errorf("insert billing operation log: %w", err)
	}
	return nil
}

// scanBillingInvoice 从行集合扫描一条账单（列序与 SELECT 一致）。
func scanBillingInvoice(rows pgx.Rows) (ports.BillingInvoice, error) {
	var inv ports.BillingInvoice
	if err := rows.Scan(
		&inv.ID, &inv.TenantID, &inv.Period, &inv.No, &inv.AmountUSD, &inv.Status, &inv.DueDate,
		&inv.IssuedAt, &inv.SettledAt, &inv.CreditedAt, &inv.IdempotencyKey, &inv.CreatedAt,
	); err != nil {
		return ports.BillingInvoice{}, fmt.Errorf("scan billing invoice: %w", err)
	}
	return inv, nil
}
