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

func parseTenantUUID(raw string) (uuid.UUID, error) {
	return parseAdminUUID(raw, "tenant_id")
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
