package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

var _ ports.TenantPlanService = (*PostgresTenant)(nil)

// NewPostgresTenantPlan constructs a TenantPlanService backed by MetadataStore.
func NewPostgresTenantPlan(store ports.MetadataStore) ports.TenantPlanService {
	return &PostgresTenant{store: store}
}

// UpdateTenantPlan updates tenants.plan_id only (does not touch quota rows).
func (t *PostgresTenant) UpdateTenantPlan(ctx context.Context, tenantID string, planID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}
	pid, err := parseAdminUUID(planID, "plan_id")
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var (
			rowID       uuid.UUID
			newPlanID   uuid.UUID
			createdAt   time.Time
			updatedAt   time.Time
			name        string
			displayName string
			status      string
		)
		scanErr := tx.QueryRow(ctx, `
			UPDATE tenants
			SET plan_id = $2, updated_at = now()
			WHERE id = $1
			RETURNING id, name, display_name, status, plan_id, created_at, updated_at
		`, id, pid).Scan(&rowID, &name, &displayName, &status, &newPlanID, &createdAt, &updatedAt)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ports.ErrTenantNotFound
		}
		if scanErr != nil {
			if isPGForeignKeyViolation(scanErr) {
				return ports.ErrTenantPlanNotFound
			}
			return fmt.Errorf("update tenant plan: %w", scanErr)
		}
		out = ports.Tenant{
			ID:          rowID.String(),
			Name:        name,
			DisplayName: displayName,
			Status:      status,
			PlanID:      newPlanID.String(),
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

// CountBoundTenants counts non-disabled tenants bound to each plan_id.
func (t *PostgresTenant) CountBoundTenants(ctx context.Context, planIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(planIDs))
	ids := make([]uuid.UUID, 0, len(planIDs))
	seen := make(map[uuid.UUID]struct{}, len(planIDs))
	for _, raw := range planIDs {
		id, err := parseAdminUUID(raw, "plan_id")
		if err != nil {
			return nil, err
		}
		out[id.String()] = 0
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out, nil
	}

	err := t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, queryErr := tx.Query(ctx, `
			SELECT plan_id, COUNT(*)::bigint
			FROM tenants
			WHERE plan_id = ANY($1) AND status <> 'disabled'
			GROUP BY plan_id
		`, ids)
		if queryErr != nil {
			return fmt.Errorf("count bound tenants: %w", queryErr)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				planID uuid.UUID
				count  int64
			)
			if scanErr := rows.Scan(&planID, &count); scanErr != nil {
				return fmt.Errorf("scan bound tenant count: %w", scanErr)
			}
			out[planID.String()] = count
		}
		if rows.Err() != nil {
			return fmt.Errorf("iterate bound tenant counts: %w", rows.Err())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListBoundTenants returns non-disabled tenants bound to planID, ordered by name.
func (t *PostgresTenant) ListBoundTenants(ctx context.Context, planID string) ([]ports.TenantSummary, error) {
	pid, err := parseAdminUUID(planID, "plan_id")
	if err != nil {
		return nil, err
	}
	return t.queryTenantSummaries(ctx, `
		SELECT id, name, display_name, status
		FROM tenants
		WHERE plan_id = $1 AND status <> 'disabled'
		ORDER BY name
	`, pid)
}

// ListBindableTenants returns non-disabled tenants not currently bound to planID, ordered by name.
func (t *PostgresTenant) ListBindableTenants(ctx context.Context, planID string) ([]ports.TenantSummary, error) {
	pid, err := parseAdminUUID(planID, "plan_id")
	if err != nil {
		return nil, err
	}
	return t.queryTenantSummaries(ctx, `
		SELECT id, name, display_name, status
		FROM tenants
		WHERE status <> 'disabled' AND plan_id IS DISTINCT FROM $1
		ORDER BY name
	`, pid)
}

func (t *PostgresTenant) queryTenantSummaries(ctx context.Context, sql string, planID uuid.UUID) ([]ports.TenantSummary, error) {
	out := make([]ports.TenantSummary, 0)
	err := t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, queryErr := tx.Query(ctx, sql, planID)
		if queryErr != nil {
			return fmt.Errorf("list tenants by plan: %w", queryErr)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				id          uuid.UUID
				name        string
				displayName string
				status      string
			)
			if scanErr := rows.Scan(&id, &name, &displayName, &status); scanErr != nil {
				return fmt.Errorf("scan tenant summary: %w", scanErr)
			}
			out = append(out, ports.TenantSummary{
				ID:          id.String(),
				Name:        name,
				DisplayName: displayName,
				Status:      status,
			})
		}
		if rows.Err() != nil {
			return fmt.Errorf("iterate tenant summaries: %w", rows.Err())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
