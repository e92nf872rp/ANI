package runtime

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

// PostgresUserAdmin is the PostgreSQL adapter skeleton for ports.UserAdminService.
// Methods are declared only; SQL against users / user_roles / roles is filled later.
// Invitation state is not handled here (Services tenant_admin_invitation).
type PostgresUserAdmin struct {
	store ports.MetadataStore
}

var _ ports.UserAdminService = (*PostgresUserAdmin)(nil)

// NewPostgresUserAdmin constructs a UserAdminService backed by MetadataStore
// (platform tx / RLS bypass). The store is retained for the later SQL issue.
func NewPostgresUserAdmin(store ports.MetadataStore) *PostgresUserAdmin {
	return &PostgresUserAdmin{store: store}
}

func (u *PostgresUserAdmin) LookupUser(ctx context.Context, tenantID, email, username string) (ports.User, error) {
	_ = ctx
	_ = tenantID
	_ = email
	_ = username
	return ports.User{}, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return false, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) GetUser(ctx context.Context, tenantID, userID string) (ports.User, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.User{}, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) ListUsers(ctx context.Context, filter ports.UserListFilter) (ports.UserListResult, error) {
	_ = ctx
	_ = filter
	return ports.UserListResult{}, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) ChangeRole(ctx context.Context, tenantID, userID, role string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = role
	return ports.ErrUnsupported
}

func (u *PostgresUserAdmin) GetRolePermissions(ctx context.Context, tenantID, userID string) (ports.UserPermissions, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.UserPermissions{}, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) GetChangeableRoles(ctx context.Context, tenantID, userID string) (ports.ChangeableRoles, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ChangeableRoles{}, ports.ErrUnsupported
}

func (u *PostgresUserAdmin) TransferOwnership(ctx context.Context, tenantID, targetUserID string) error {
	_ = ctx
	_ = tenantID
	_ = targetUserID
	return ports.ErrUnsupported
}

func (u *PostgresUserAdmin) SetStatus(ctx context.Context, tenantID, userID, status string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = status
	return ports.ErrUnsupported
}

func (u *PostgresUserAdmin) SoftDelete(ctx context.Context, tenantID, userID string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ErrUnsupported
}

func (u *PostgresUserAdmin) ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = newPassword
	return ports.ErrUnsupported
}
