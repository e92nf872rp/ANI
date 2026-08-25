package runtime

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

// PostgresTenantAdmin is the PostgreSQL adapter for ports.TenantAdminService.
// Invitation state is not handled here (Services tenant_admin_invitation).
type PostgresTenantAdmin struct {
	store ports.MetadataStore
}

var _ ports.TenantAdminService = (*PostgresTenantAdmin)(nil)

// NewPostgresTenantAdmin constructs a TenantAdminService backed by MetadataStore
// (platform tx / RLS bypass).
func NewPostgresTenantAdmin(store ports.MetadataStore) *PostgresTenantAdmin {
	return &PostgresTenantAdmin{store: store}
}

func (u *PostgresTenantAdmin) LookupUser(ctx context.Context, tenantID, email, username string) (ports.User, error) {
	_ = ctx
	_ = tenantID
	_ = email
	_ = username
	return ports.User{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return false, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) GetUser(ctx context.Context, tenantID, userID string) (ports.User, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.User{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) ListUsers(ctx context.Context, filter ports.UserListFilter) (ports.UserListResult, error) {
	_ = ctx
	_ = filter
	return ports.UserListResult{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) ChangeRole(ctx context.Context, tenantID, userID, role string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = role
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) GetRolePermissions(ctx context.Context, tenantID, userID string) (ports.UserPermissions, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.UserPermissions{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) GetChangeableRoles(ctx context.Context, tenantID, userID string) (ports.ChangeableRoles, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ChangeableRoles{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) SetStatus(ctx context.Context, tenantID, userID, status string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = status
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) SoftDelete(ctx context.Context, tenantID, userID string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = newPassword
	return ports.ErrUnsupported
}

// PostgresUserAdmin is kept as a compatibility alias during the rename.
type PostgresUserAdmin = PostgresTenantAdmin

// NewPostgresUserAdmin is kept as a compatibility wrapper during the rename.
func NewPostgresUserAdmin(store ports.MetadataStore) *PostgresTenantAdmin {
	return NewPostgresTenantAdmin(store)
}
