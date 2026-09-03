package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
)

// PostgresTenant implements tenant read / tenant-plan adapters against the control-plane DB.
type PostgresTenant struct {
	store ports.MetadataStore
}

// NewPostgresTenant constructs a TenantService backed by MetadataStore (platform tx).
func NewPostgresTenant(store ports.MetadataStore) *PostgresTenant {
	return &PostgresTenant{store: store}
}

var _ ports.TenantService = (*PostgresTenant)(nil)

// GetTenant returns the minimal tenant row (status / plan_id).
func (t *PostgresTenant) GetTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var (
			rowID       uuid.UUID
			planID      uuid.UUID
			createdAt   time.Time
			updatedAt   time.Time
			name        string
			displayName string
			status      string
		)
		scanErr := tx.QueryRow(ctx, `
			SELECT id, name, display_name, status, plan_id, created_at, updated_at
			FROM tenants
			WHERE id = $1
		`, id).Scan(&rowID, &name, &displayName, &status, &planID, &createdAt, &updatedAt)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ports.ErrTenantNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("get tenant: %w", scanErr)
		}
		out = ports.Tenant{
			ID:          rowID.String(),
			Name:        name,
			DisplayName: displayName,
			Status:      status,
			PlanID:      planID.String(),
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		}
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

func (t *PostgresTenant) CreateTenant(ctx context.Context, in ports.CreateTenantInput) (ports.Tenant, error) {
	// 步骤 1：规范化并校验必填字段 / plan_id UUID
	name := strings.TrimSpace(in.Name)
	displayName := strings.TrimSpace(in.DisplayName)
	contactEmail := strings.TrimSpace(in.ContactEmail)
	adminEmail := strings.TrimSpace(in.AdminEmail)
	adminName := strings.TrimSpace(in.AdminName)
	passwordHash := strings.TrimSpace(in.AdminPasswordHash)
	if name == "" || displayName == "" || contactEmail == "" || adminEmail == "" || adminName == "" || passwordHash == "" {
		return ports.Tenant{}, fmt.Errorf("%w: name, display_name, email, admin_email, admin_name, admin_password_hash required", ports.ErrInvalid)
	}
	planID, err := parseTenantUUID(in.PlanID)
	if err != nil {
		return ports.Tenant{}, fmt.Errorf("%w: plan_id must be a uuid", ports.ErrInvalid)
	}

	// 步骤 2：平台事务内原子写入（任一失败全部回滚）
	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2a：先查 name 是否已存在 → ErrTenantNameConflict
		var nameExists bool
		if scanErr := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM tenants WHERE name = $1)
		`, name).Scan(&nameExists); scanErr != nil {
			return fmt.Errorf("check tenant name: %w", scanErr)
		}
		if nameExists {
			return ports.ErrTenantNameConflict
		}

		// 步骤 2b：INSERT tenants（status=active）；并发下 UNIQUE 仍映射冲突
		var (
			rowID       uuid.UUID
			rowPlanID   uuid.UUID
			createdAt   time.Time
			updatedAt   time.Time
			rowName     string
			rowDisplay  string
			rowStatus   string
			rowContact  string
			rowFrozen   *time.Time
			rowDisabled *time.Time
		)
		scanErr := tx.QueryRow(ctx, `
			INSERT INTO tenants (name, display_name, status, plan_id, contact_email)
			VALUES ($1, $2, 'active', $3, $4)
			RETURNING id, name, display_name, status, plan_id, contact_email, frozen_at, disabled_at, created_at, updated_at
		`, name, displayName, planID, contactEmail).Scan(
			&rowID, &rowName, &rowDisplay, &rowStatus, &rowPlanID, &rowContact, &rowFrozen, &rowDisabled, &createdAt, &updatedAt,
		)
		if scanErr != nil {
			if isPGUniqueViolation(scanErr) {
				return ports.ErrTenantNameConflict
			}
			return fmt.Errorf("insert tenants: %w", scanErr)
		}

		// 步骤 2c：INSERT tenant_auth（默认 sso_enabled=false / mfa_required=false）
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_auth (tenant_id) VALUES ($1)
		`, rowID); execErr != nil {
			return fmt.Errorf("insert tenant_auth: %w", execErr)
		}

		// 步骤 2d：INSERT users（admin_name→username；password_hash 已 bcrypt）
		var userID uuid.UUID
		if scanErr := tx.QueryRow(ctx, `
			INSERT INTO users (tenant_id, username, email, password_hash, status, display_name)
			VALUES ($1, $2, $3, $4, 'active', $5)
			RETURNING id
		`, rowID, adminName, adminEmail, passwordHash, adminName).Scan(&userID); scanErr != nil {
			if isPGUniqueViolation(scanErr) {
				return fmt.Errorf("%w: admin email or username conflict", ports.ErrInvalid)
			}
			return fmt.Errorf("insert users: %w", scanErr)
		}

		// 步骤 2e：绑定内置 tenant-admin 角色
		var roleID uuid.UUID
		if scanErr := tx.QueryRow(ctx, `
			SELECT id FROM roles
			WHERE tenant_id IS NULL AND name = 'tenant-admin'
			LIMIT 1
		`).Scan(&roleID); scanErr != nil {
			return fmt.Errorf("lookup tenant-admin role: %w", scanErr)
		}
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
		`, userID, roleID); execErr != nil {
			return fmt.Errorf("insert user_roles: %w", execErr)
		}

		// 步骤 2f：INSERT tenant_lifecycle('create')（可选 user_id / request_id）
		var actor any
		if actorID, parseErr := uuid.Parse(strings.TrimSpace(in.ActorUserID)); parseErr == nil {
			actor = actorID
		}
		var requestID any
		if rid := strings.TrimSpace(in.RequestID); rid != "" {
			requestID = rid
		}
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_lifecycle (tenant_id, action, user_id, request_id)
			VALUES ($1, 'create', $2, $3)
		`, rowID, actor, requestID); execErr != nil {
			return fmt.Errorf("insert tenant_lifecycle: %w", execErr)
		}

		// 步骤 2g：组装返回视图（新建租户 admin_count=1）
		out = ports.Tenant{
			ID:           rowID.String(),
			Name:         rowName,
			DisplayName:  rowDisplay,
			Status:       rowStatus,
			PlanID:       rowPlanID.String(),
			ContactEmail: rowContact,
			FrozenAt:     rowFrozen,
			DisabledAt:   rowDisabled,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			AdminCount:   1,
		}
		return nil
	})
	// 步骤 3：上抛事务错误或返回租户
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (t *PostgresTenant) ListTenants(context.Context, ports.ListTenantsFilter) (ports.TenantListResult, error) {
	return ports.TenantListResult{}, ports.ErrUnsupported
}

func (t *PostgresTenant) UpdateTenant(context.Context, string, ports.UpdateTenantInput) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}

func (t *PostgresTenant) FreezeTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}

func (t *PostgresTenant) UnfreezeTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}

func (t *PostgresTenant) DisableTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}

func (t *PostgresTenant) GetTenantAuth(context.Context, string) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrUnsupported
}

func (t *PostgresTenant) UpdateTenantAuth(context.Context, string, ports.TenantAuthPatch) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrUnsupported
}

func (t *PostgresTenant) ListTenantLifecycle(context.Context, string, ports.TenantLifecycleFilter) (ports.TenantLifecycleListResult, error) {
	return ports.TenantLifecycleListResult{}, ports.ErrUnsupported
}

func parseTenantUUID(raw string) (uuid.UUID, error) {
	return parseAdminUUID(raw, "tenant_id")
}

// ListAvailableTenants 返回 status <> 'disabled' 的租户列表，按 created_at DESC 排序。
func (t *PostgresTenant) ListAvailableTenants(ctx context.Context) ([]ports.TenantSummary, error) {
	// 步骤 1：准备结果切片，在平台事务内查询
	out := make([]ports.TenantSummary, 0)
	err := t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：查询非 disabled 租户，按 created_at DESC
		rows, queryErr := tx.Query(ctx, `
			SELECT id, name, display_name, status
			FROM tenants
			WHERE status <> 'disabled'
			ORDER BY created_at DESC
		`)
		if queryErr != nil {
			return fmt.Errorf("list available tenants: %w", queryErr)
		}
		defer rows.Close()
		// 步骤 3：逐行扫描并写入 TenantSummary
		for rows.Next() {
			var (
				id          uuid.UUID
				name        string
				displayName string
				status      string
			)
			if scanErr := rows.Scan(&id, &name, &displayName, &status); scanErr != nil {
				return fmt.Errorf("scan available tenant: %w", scanErr)
			}
			out = append(out, ports.TenantSummary{
				ID:          id.String(),
				Name:        name,
				DisplayName: displayName,
				Status:      status,
			})
		}
		if rows.Err() != nil {
			return fmt.Errorf("iterate available tenants: %w", rows.Err())
		}
		return nil
	})
	// 步骤 4：事务失败则上抛；成功返回列表
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseAdminUUID(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%w: %s required", ports.ErrInvalid, field)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a uuid", ports.ErrInvalid, field)
	}
	return id, nil
}

func isPGForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
