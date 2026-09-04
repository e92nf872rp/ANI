package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantStore 是 TenantStore 的 PostgreSQL 适配器。
// 操作 tenant_quota_change；不直接读写 tenants / tenant_auth / tenant_lifecycle。
type PostgresTenantStore struct {
	db *pgxpool.Pool
}

var _ ports.TenantStore = (*PostgresTenantStore)(nil)

// NewPostgresTenantStore 构造租户本地表存储。
func NewPostgresTenantStore(db *pgxpool.Pool) ports.TenantStore {
	return &PostgresTenantStore{db: db}
}

func (s *PostgresTenantStore) InsertPendingQuotaChanges(ctx context.Context, tenantID, requestID uuid.UUID, items []ports.QuotaChangePendingInput) error {
	// 步骤 1：items 非空（调用方已校验；此处兜底）
	if len(items) == 0 {
		return fmt.Errorf("%w: items required", ports.ErrQuotaChangeRequestInvalid)
	}

	// 步骤 2：开事务，逐维 INSERT pending（同批共用 request_id）
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin insert pending quota changes: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, it := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_quota_change (
				tenant_id, request_id, resource_type, old_value, new_value, requested_by, status
			) VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		`, tenantID, requestID, it.ResourceType, it.OldValue, it.NewValue, it.RequestedBy)
		if err != nil {
			// 步骤 3：PK 冲突 → 同请求同维；FK（tenant/requested_by）→ VALIDATION_FAILED
			if isUniqueViolation(err) {
				return ports.ErrQuotaChangeRequestConflict
			}
			if isForeignKeyViolation(err) {
				return fmt.Errorf("%w: tenant_id or requested_by foreign key", ports.ErrValidationFailed)
			}
			return fmt.Errorf("insert pending quota change: %w", err)
		}
	}

	// 步骤 4：提交事务
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit insert pending quota changes: %w", err)
	}
	return nil
}

func (s *PostgresTenantStore) ListQuotaChangesByTenant(ctx context.Context, tenantID uuid.UUID, status string) ([]ports.QuotaChangeRequest, error) {
	// 步骤 1：按租户查询；status 空串不过滤；created_at DESC
	var (
		rows pgx.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.Query(ctx, `
			SELECT tenant_id, request_id, resource_type, old_value, new_value, status,
			       requested_by, reviewed_by, reviewed_at, created_at, updated_at
			FROM tenant_quota_change
			WHERE tenant_id = $1
			ORDER BY created_at DESC, resource_type ASC
		`, tenantID)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT tenant_id, request_id, resource_type, old_value, new_value, status,
			       requested_by, reviewed_by, reviewed_at, created_at, updated_at
			FROM tenant_quota_change
			WHERE tenant_id = $1 AND status = $2
			ORDER BY created_at DESC, resource_type ASC
		`, tenantID, status)
	}
	if err != nil {
		return nil, fmt.Errorf("list quota changes by tenant: %w", err)
	}
	defer rows.Close()

	// 步骤 2：扫描全部行（不分页）
	out := make([]ports.QuotaChangeRequest, 0)
	for rows.Next() {
		row, scanErr := scanQuotaChangeRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quota changes by tenant: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantStore) ListQuotaChangesByRequestID(ctx context.Context, tenantID, requestID uuid.UUID) ([]ports.QuotaChangeRequest, error) {
	// 步骤 1：按 (tenant_id, request_id) 列出同批全部维度
	rows, err := s.db.Query(ctx, `
		SELECT tenant_id, request_id, resource_type, old_value, new_value, status,
		       requested_by, reviewed_by, reviewed_at, created_at, updated_at
		FROM tenant_quota_change
		WHERE tenant_id = $1 AND request_id = $2
		ORDER BY resource_type ASC
	`, tenantID, requestID)
	if err != nil {
		return nil, fmt.Errorf("list quota changes by request_id: %w", err)
	}
	defer rows.Close()

	// 步骤 2：扫描；无行 → NotFound
	out := make([]ports.QuotaChangeRequest, 0)
	for rows.Next() {
		row, scanErr := scanQuotaChangeRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quota changes by request_id: %w", err)
	}
	if len(out) == 0 {
		return nil, ports.ErrQuotaChangeRequestNotFound
	}
	return out, nil
}

func (s *PostgresTenantStore) SetQuotaChangeStatusByRequestID(ctx context.Context, tenantID, requestID uuid.UUID, status string, reviewedBy uuid.UUID) (int64, error) {
	// 步骤 1：仅允许 approved / rejected
	switch status {
	case "approved", "rejected":
	default:
		return 0, fmt.Errorf("%w: status must be approved or rejected", ports.ErrQuotaChangeRequestInvalid)
	}

	// 步骤 2：乐观锁 UPDATE —— 仅 status='pending' 的同行整批更新；返回受影响行数
	tag, err := s.db.Exec(ctx, `
		UPDATE tenant_quota_change
		SET status = $3,
		    reviewed_by = $4,
		    reviewed_at = NOW(),
		    updated_at = NOW()
		WHERE tenant_id = $1 AND request_id = $2 AND status = 'pending'
	`, tenantID, requestID, status, reviewedBy)
	if err != nil {
		if isForeignKeyViolation(err) {
			return 0, fmt.Errorf("%w: reviewed_by foreign key", ports.ErrValidationFailed)
		}
		return 0, fmt.Errorf("set quota change status by request_id: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanQuotaChangeRequest(rows pgx.Rows) (ports.QuotaChangeRequest, error) {
	var row ports.QuotaChangeRequest
	if err := rows.Scan(
		&row.TenantID, &row.RequestID, &row.ResourceType, &row.OldValue, &row.NewValue, &row.Status,
		&row.RequestedBy, &row.ReviewedBy, &row.ReviewedAt, &row.CreatedAt, &row.UpdatedAt,
	); err != nil {
		return ports.QuotaChangeRequest{}, fmt.Errorf("scan quota change request: %w", err)
	}
	return row, nil
}
