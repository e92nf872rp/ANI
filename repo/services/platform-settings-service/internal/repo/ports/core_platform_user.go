package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CorePlatformUserClient wraps Core SDK calls to /api/v1/admin/platform-users/*.
type CorePlatformUserClient interface {
	Create(ctx context.Context, in PlatformUserCreateInput) (id string, err error)
	List(ctx context.Context, filter PlatformUserListFilter) (PlatformUserListDTO, error)
	Get(ctx context.Context, userID uuid.UUID) (PlatformUserDTO, error)
	ChangeRole(ctx context.Context, userID uuid.UUID, roleID uuid.UUID) error
	ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error
	SetStatus(ctx context.Context, userID uuid.UUID, status string) error
	SoftDelete(ctx context.Context, userID uuid.UUID) error
	ListPlatformRoles(ctx context.Context) ([]PlatformRoleDTO, error)
	GetPlatformUserPermissions(ctx context.Context, userID uuid.UUID) (PlatformUserPermissionsDTO, error)
}

type PlatformUserCreateInput struct {
	Email       string
	Username    string
	DisplayName string
	RoleID      string // roles.id UUID
	Password    string
}

type PlatformUserListFilter struct {
	Limit  int
	Cursor string
	RoleID string // roles.id UUID；空 = 不过滤
	Status string
	Source string
	Search string
}

type PlatformUserDTO struct {
	ID          string
	Email       string
	Username    string
	DisplayName *string
	RoleID      string
	Role        string
	Status      string
	Source      string
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

type PlatformUserListDTO struct {
	Items      []PlatformUserDTO
	NextCursor string
}

type PlatformRoleDTO struct {
	ID          string
	Name        string
	Permissions []map[string]any // roles.permissions JSONB 原样（resource/actions/scope）
}

type PlatformUserPermissionsDTO struct {
	UserID      string
	RoleID      string
	Role        string
	Permissions []map[string]any
}
