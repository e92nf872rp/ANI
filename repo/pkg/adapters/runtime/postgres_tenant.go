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

func (t *PostgresTenant) CreateTenant(context.Context, ports.CreateTenantInput) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
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
