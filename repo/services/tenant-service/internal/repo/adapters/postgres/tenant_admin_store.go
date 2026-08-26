package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantAdminStore 是 TenantAdminStore 的 PostgreSQL 适配器。
// 仅操作 tenant_admin_invitation / audit_logs，不直接访问 users/user_roles/roles。
type PostgresTenantAdminStore struct {
	db *pgxpool.Pool
}

var _ ports.TenantAdminStore = (*PostgresTenantAdminStore)(nil)

// NewPostgresTenantAdminStore 构造租户管理员存储。
func NewPostgresTenantAdminStore(db *pgxpool.Pool) ports.TenantAdminStore {
	return &PostgresTenantAdminStore{db: db}
}

func (s *PostgresTenantAdminStore) HasPendingInvitation(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	// 步骤 1：查询该租户下该用户是否存在 status=inviting 的邀请
	var pending bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM tenant_admin_invitation
			WHERE tenant_id = $1
			  AND user_id = $2
			  AND status = $3
		)
	`, tenantID, userID, ports.InvitationStatusInviting).Scan(&pending)
	if err != nil {
		return false, fmt.Errorf("has pending invitation: %w", err)
	}
	return pending, nil
}

func (s *PostgresTenantAdminStore) InsertInvitation(ctx context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	// 步骤 1：校验必填字段
	if inv.TenantID == uuid.Nil || inv.UserID == uuid.Nil {
		return ports.TenantAdminInvitation{}, fmt.Errorf("%w: tenant_id and user_id required", ports.ErrValidationFailed)
	}
	if strings.TrimSpace(inv.TokenHash) == "" {
		return ports.TenantAdminInvitation{}, fmt.Errorf("%w: token_hash required", ports.ErrValidationFailed)
	}
	status := strings.TrimSpace(inv.Status)
	if status == "" {
		status = ports.InvitationStatusInviting
	}
	if inv.ExpireAt.IsZero() {
		return ports.TenantAdminInvitation{}, fmt.Errorf("%w: expire_at required", ports.ErrValidationFailed)
	}

	// 步骤 2：事务内先查最新邀请，再决定 UPDATE（expired）或 INSERT
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ports.TenantAdminInvitation{}, fmt.Errorf("begin insert invitation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var latest ports.TenantAdminInvitation
	latestErr := tx.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
		FROM tenant_admin_invitation
		WHERE tenant_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`, inv.TenantID, inv.UserID).Scan(
		&latest.ID, &latest.TenantID, &latest.UserID, &latest.TokenHash, &latest.Status,
		&latest.ExpireAt, &latest.CreatedAt, &latest.AcceptedAt, &latest.RejectedAt,
	)

	var out ports.TenantAdminInvitation
	switch {
	case latestErr == nil && latest.Status == ports.InvitationStatusExpired:
		// 最新为 expired → 原地刷新 token / 过期时间并回归 inviting
		err = tx.QueryRow(ctx, `
			UPDATE tenant_admin_invitation
			SET token_hash = $2,
			    expire_at = $3,
			    status = $4,
			    accepted_at = NULL,
			    rejected_at = NULL
			WHERE id = $1
			RETURNING id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
		`, latest.ID, inv.TokenHash, inv.ExpireAt, status).Scan(
			&out.ID, &out.TenantID, &out.UserID, &out.TokenHash, &out.Status,
			&out.ExpireAt, &out.CreatedAt, &out.AcceptedAt, &out.RejectedAt,
		)
	case errors.Is(latestErr, pgx.ErrNoRows), latestErr == nil:
		// 无记录，或最新非 expired（如已终态）→ 新建行
		err = tx.QueryRow(ctx, `
			INSERT INTO tenant_admin_invitation (
				tenant_id, user_id, token_hash, status, expire_at
			) VALUES ($1, $2, $3, $4, $5)
			RETURNING id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
		`, inv.TenantID, inv.UserID, inv.TokenHash, status, inv.ExpireAt).Scan(
			&out.ID, &out.TenantID, &out.UserID, &out.TokenHash, &out.Status,
			&out.ExpireAt, &out.CreatedAt, &out.AcceptedAt, &out.RejectedAt,
		)
	default:
		return ports.TenantAdminInvitation{}, fmt.Errorf("get latest invitation in tx: %w", latestErr)
	}
	if err != nil {
		if isUniqueViolation(err) {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "uk_tenant_admin_invitation_pending" {
				return ports.TenantAdminInvitation{}, ports.ErrTenantInvitationPending
			}
			return ports.TenantAdminInvitation{}, fmt.Errorf("%w: token_hash conflict", ports.ErrValidationFailed)
		}
		if isForeignKeyViolation(err) {
			return ports.TenantAdminInvitation{}, ports.ErrTenantAdminNotFound
		}
		return ports.TenantAdminInvitation{}, fmt.Errorf("upsert tenant_admin_invitation: %w", err)
	}

	// 步骤 3：提交事务
	if err := tx.Commit(ctx); err != nil {
		return ports.TenantAdminInvitation{}, fmt.Errorf("commit insert invitation tx: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantAdminStore) GetLatestInvitation(ctx context.Context, tenantID, userID uuid.UUID) (ports.TenantAdminInvitation, error) {
	// 步骤 1：按 created_at DESC 取最新一条
	var out ports.TenantAdminInvitation
	err := s.db.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
		FROM tenant_admin_invitation
		WHERE tenant_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, tenantID, userID).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.TokenHash, &out.Status,
		&out.ExpireAt, &out.CreatedAt, &out.AcceptedAt, &out.RejectedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.TenantAdminInvitation{}, ports.ErrTenantAdminInvitationNotFound
	}
	if err != nil {
		return ports.TenantAdminInvitation{}, fmt.Errorf("get latest invitation: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantAdminStore) UpdateInvitation(ctx context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	// 步骤 1：按 id 更新 token_hash / expire_at / status，并清空 accepted_at / rejected_at
	if inv.ID == uuid.Nil {
		return ports.TenantAdminInvitation{}, fmt.Errorf("%w: invitation id required", ports.ErrValidationFailed)
	}
	var out ports.TenantAdminInvitation
	err := s.db.QueryRow(ctx, `
		UPDATE tenant_admin_invitation
		SET token_hash = $2,
		    expire_at = $3,
		    status = $4,
		    accepted_at = NULL,
		    rejected_at = NULL
		WHERE id = $1
		RETURNING id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
	`, inv.ID, inv.TokenHash, inv.ExpireAt, inv.Status).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.TokenHash, &out.Status,
		&out.ExpireAt, &out.CreatedAt, &out.AcceptedAt, &out.RejectedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.TenantAdminInvitation{}, ports.ErrTenantAdminInvitationNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			// 区分两种唯一冲突：
			// - uk_tenant_admin_invitation_pending (tenant_id,user_id WHERE status='inviting') → 已有 pending 邀请
			// - uk_tenant_admin_invitation_token_hash → token_hash 碰撞（极罕见）
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "uk_tenant_admin_invitation_pending" {
				return ports.TenantAdminInvitation{}, ports.ErrTenantInvitationPending
			}
			return ports.TenantAdminInvitation{}, fmt.Errorf("%w: token_hash conflict", ports.ErrValidationFailed)
		}
		return ports.TenantAdminInvitation{}, fmt.Errorf("update tenant_admin_invitation: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantAdminStore) ListInvitationFlags(ctx context.Context, tenantID *uuid.UUID, statusFilter string) ([]ports.InvitationFlag, error) {
	// 步骤 1：校验 statusFilter
	statusFilter = strings.TrimSpace(statusFilter)
	switch statusFilter {
	case "", ports.InvitationStatusInviting, ports.InvitationStatusExpired:
		// ok
	default:
		return nil, fmt.Errorf("%w: statusFilter must be inviting|expired|empty", ports.ErrValidationFailed)
	}

	// 步骤 2：取每个 (tenant_id,user_id) 最新一条邀请记录，按其 status 设置标记
	rows, err := s.db.Query(ctx, `
		SELECT tenant_id, user_id,
		       status = $2 AS is_inviting,
		       status = $3 AS is_expired
		FROM (
			SELECT tenant_id, user_id, status,
			       ROW_NUMBER() OVER (PARTITION BY tenant_id, user_id ORDER BY created_at DESC, id DESC) AS rn
			FROM tenant_admin_invitation
			WHERE ($1::uuid IS NULL OR tenant_id = $1)
			  AND status IN ($2, $3)
		) latest
		WHERE rn = 1
		  AND (
			CASE
				WHEN $4 = $2 THEN status = $2
				WHEN $4 = $3 THEN status = $3
				ELSE TRUE
			END
		  )
	`, tenantID, ports.InvitationStatusInviting, ports.InvitationStatusExpired, statusFilter)
	if err != nil {
		return nil, fmt.Errorf("list invitation flags: %w", err)
	}
	defer rows.Close()

	out := make([]ports.InvitationFlag, 0)
	for rows.Next() {
		var flag ports.InvitationFlag
		if scanErr := rows.Scan(&flag.TenantID, &flag.UserID, &flag.IsInviting, &flag.IsExpired); scanErr != nil {
			return nil, fmt.Errorf("scan invitation flag: %w", scanErr)
		}
		out = append(out, flag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitation flags: %w", err)
	}
	return out, nil
}

func (s *PostgresTenantAdminStore) ListAuditLogs(ctx context.Context, tenantID, userID uuid.UUID, filter ports.TenantAdminAuditLogFilter) (ports.TenantAdminAuditLogListResult, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = filter
	return ports.TenantAdminAuditLogListResult{}, ports.ErrNotImplemented
}
