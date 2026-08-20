package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantAdminStore 是 TenantAdminStore 的 PostgreSQL 适配器骨架。
// 本 Issue 只声明方法签名；具体 SQL 在后续 issue（建表 + 邀请/审计实现）填充。
// 仅操作 tenant_admin_invitation / audit_logs，不直接访问 users/user_roles/roles。
type PostgresTenantAdminStore struct{}

var _ ports.TenantAdminStore = (*PostgresTenantAdminStore)(nil)

// NewPostgresTenantAdminStore 构造租户管理员存储骨架。
func NewPostgresTenantAdminStore() ports.TenantAdminStore {
	return &PostgresTenantAdminStore{}
}

func (s *PostgresTenantAdminStore) HasPendingInvitation(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return false, ports.ErrNotImplemented
}

func (s *PostgresTenantAdminStore) InsertInvitation(ctx context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	_ = ctx
	return ports.TenantAdminInvitation{}, ports.ErrNotImplemented
}

func (s *PostgresTenantAdminStore) GetLatestInvitation(ctx context.Context, tenantID, userID uuid.UUID) (ports.TenantAdminInvitation, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.TenantAdminInvitation{}, ports.ErrNotImplemented
}

func (s *PostgresTenantAdminStore) UpdateInvitation(ctx context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	_ = ctx
	return ports.TenantAdminInvitation{}, ports.ErrNotImplemented
}

func (s *PostgresTenantAdminStore) CreateAuditLog(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
	_ = ctx
	_ = log
	return uuid.Nil, ports.ErrNotImplemented
}

func (s *PostgresTenantAdminStore) ListAuditLogs(ctx context.Context, tenantID, userID uuid.UUID, filter ports.TenantAdminAuditLogFilter) (ports.TenantAdminAuditLogListResult, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = filter
	return ports.TenantAdminAuditLogListResult{}, ports.ErrNotImplemented
}
