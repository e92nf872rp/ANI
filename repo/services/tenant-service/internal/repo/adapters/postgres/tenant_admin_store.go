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

	// 步骤 2：插入邀请行（不改 users.status / 不绑角色）
	var out ports.TenantAdminInvitation
	err := s.db.QueryRow(ctx, `
		INSERT INTO tenant_admin_invitation (
			tenant_id, user_id, token_hash, status, expire_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, user_id, token_hash, status, expire_at, created_at, accepted_at, rejected_at
	`, inv.TenantID, inv.UserID, inv.TokenHash, status, inv.ExpireAt).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.TokenHash, &out.Status,
		&out.ExpireAt, &out.CreatedAt, &out.AcceptedAt, &out.RejectedAt,
	)
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
		if isForeignKeyViolation(err) {
			return ports.TenantAdminInvitation{}, ports.ErrTenantAdminNotFound
		}
		return ports.TenantAdminInvitation{}, fmt.Errorf("insert tenant_admin_invitation: %w", err)
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
			return ports.TenantAdminInvitation{}, fmt.Errorf("%w: token_hash conflict", ports.ErrValidationFailed)
		}
		return ports.TenantAdminInvitation{}, fmt.Errorf("update tenant_admin_invitation: %w", err)
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
