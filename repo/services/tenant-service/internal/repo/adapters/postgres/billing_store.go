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
// （迁移 20260907_001_tenant_billing.sql；计费结算域方案 §6.1）。
type PostgresBillingStore struct {
	db *pgxpool.Pool
}

var _ ports.BillingStore = (*PostgresBillingStore)(nil)

// NewPostgresBillingStore 构造计费结算存储实例。
func NewPostgresBillingStore(db *pgxpool.Pool) ports.BillingStore {
	return &PostgresBillingStore{db: db}
}

// ListInvoicesByTenant 返回该租户全部账单（issued_at 倒序）。
func (s *PostgresBillingStore) ListInvoicesByTenant(ctx context.Context, tenantID uuid.UUID) ([]ports.BillingInvoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE tenant_id = $1
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

// GetInvoiceByPeriod 返回该租户该账期的账单；无账单返回 nil（不视为错误）。
func (s *PostgresBillingStore) GetInvoiceByPeriod(ctx context.Context, tenantID uuid.UUID, period string) (*ports.BillingInvoice, error) {
	inv, err := s.queryInvoice(ctx, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE tenant_id = $1 AND period = $2
	`, tenantID, period)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// GetInvoice 按主键查账单；不存在返回 ErrBillingInvoiceNotFound。
func (s *PostgresBillingStore) GetInvoice(ctx context.Context, id uuid.UUID) (*ports.BillingInvoice, error) {
	inv, err := s.queryInvoice(ctx, `
		SELECT id, tenant_id, period, no, amount_usd, status, due_date,
		       issued_at, settled_at, credited_at, idempotency_key, created_at
		FROM billing_invoices
		WHERE id = $1
	`, id)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// CountInvoicesByNoPrefix 统计账单号前缀匹配数（账单号 seq 生成用）。
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
func (s *PostgresBillingStore) CreateInvoice(ctx context.Context, in ports.CreateBillingInvoiceInput) (*ports.BillingInvoice, error) {
	inv, err := s.queryInvoice(ctx, `
		INSERT INTO billing_invoices
			(tenant_id, period, no, amount_usd, status, due_date, issued_at, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, tenant_id, period, no, amount_usd, status, due_date,
		          issued_at, settled_at, credited_at, idempotency_key, created_at
	`, in.TenantID, in.Period, in.No, in.AmountUSD, ports.BillingInvoiceIssued,
		in.DueDate, in.IssuedAt, in.IdempotencyKey)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ports.ErrBillingInvoiceExists
		}
		return nil, err
	}
	return inv, nil
}

// UpdateInvoiceStatus 以 status='issued' 为 CAS 前提应用状态迁移；不满足 → ErrBillingStateConflict。
func (s *PostgresBillingStore) UpdateInvoiceStatus(ctx context.Context, id uuid.UUID, action ports.BillingInvoiceAction, at time.Time) (*ports.BillingInvoice, error) {
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

	// 步骤 2：条件更新（仅 issued 可迁移）；命中则 RETURNING 组装实体
	inv, err := s.queryInvoice(ctx, `
		UPDATE billing_invoices
		SET status = $2, settled_at = $3, credited_at = $4
		WHERE id = $1 AND status = $5
		RETURNING id, tenant_id, period, no, amount_usd, status, due_date,
		          issued_at, settled_at, credited_at, idempotency_key, created_at
	`, id, status, settledAt, creditedAt, ports.BillingInvoiceIssued)
	if err != nil {
		if errors.Is(err, ports.ErrBillingInvoiceNotFound) {
			// 步骤 3：未命中时区分 404（账单不存在）与 409（状态不满足 CAS）
			var exists bool
			if existsErr := s.db.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM billing_invoices WHERE id = $1)`, id).Scan(&exists); existsErr != nil {
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
func (s *PostgresBillingStore) CreateAdjustment(ctx context.Context, in ports.CreateBillingAdjustmentInput) (*ports.BillingAdjustment, error) {
	var adj ports.BillingAdjustment
	err := s.db.QueryRow(ctx, `
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

// ListBillingTenantIDs 返回计费域有足迹（账单/调账/授信）的租户集合；
// tenantFilter 非 nil 时仅返回该租户（存在足迹才返回，无足迹返回空）。
func (s *PostgresBillingStore) ListBillingTenantIDs(ctx context.Context, tenantFilter *uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT tenant_id FROM (
			SELECT tenant_id FROM billing_invoices
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

// ── helpers（勿穿插到上方 Store 方法中间）────────────────────────────────

// queryInvoice 执行返回单行账单的查询；ErrNoRows 统一映射为 ErrBillingInvoiceNotFound。
func (s *PostgresBillingStore) queryInvoice(ctx context.Context, sql string, args ...any) (*ports.BillingInvoice, error) {
	var inv ports.BillingInvoice
	err := s.db.QueryRow(ctx, sql, args...).Scan(
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
