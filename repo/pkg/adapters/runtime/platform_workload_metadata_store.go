package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

type MetadataPlatformWorkloadStore struct {
	store ports.MetadataStore
	ctx   context.Context
}

func NewMetadataPlatformWorkloadStore(store ports.MetadataStore) *MetadataPlatformWorkloadStore {
	return &MetadataPlatformWorkloadStore{store: store, ctx: context.Background()}
}

func (s *MetadataPlatformWorkloadStore) get(tenantID, workloadID string) (kubernetesPlatformWorkload, error) {
	item, ok := s.getRaw(tenantID, workloadID)
	if !ok || item.deleted {
		return kubernetesPlatformWorkload{}, ports.ErrNotFound
	}
	return item, nil
}

func (s *MetadataPlatformWorkloadStore) getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool) {
	var item kubernetesPlatformWorkload
	var found bool
	err := s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		var specJSON, recordJSON []byte
		var deleted bool
		err := tx.QueryRow(ctx, `
			SELECT spec, record, deleted
			FROM platform_workloads
			WHERE tenant_id = $1::uuid AND id = $2::uuid
		`, tenantID, workloadID).Scan(&specJSON, &recordJSON, &deleted)
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) || strings.Contains(err.Error(), "no rows") {
				return nil
			}
			return err
		}
		if err := json.Unmarshal(specJSON, &item.spec); err != nil {
			return fmt.Errorf("decode platform workload spec: %w", err)
		}
		if err := json.Unmarshal(recordJSON, &item.record); err != nil {
			return fmt.Errorf("decode platform workload record: %w", err)
		}
		item.deleted = deleted
		found = true
		return nil
	})
	if err != nil || !found {
		return kubernetesPlatformWorkload{}, false
	}
	return item, true
}

func (s *MetadataPlatformWorkloadStore) put(item kubernetesPlatformWorkload) error {
	specJSON, err := json.Marshal(item.spec)
	if err != nil {
		return fmt.Errorf("encode platform workload spec: %w", err)
	}
	recordJSON, err := json.Marshal(item.record)
	if err != nil {
		return fmt.Errorf("encode platform workload record: %w", err)
	}
	return s.withTenant(item.record.TenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO platform_workloads (id, tenant_id, name, deleted, spec, record, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				deleted = EXCLUDED.deleted,
				spec = EXCLUDED.spec,
				record = EXCLUDED.record,
				updated_at = EXCLUDED.updated_at
		`, item.record.ID, item.record.TenantID, item.record.Name, item.deleted, specJSON, recordJSON, item.record.CreatedAt, item.record.UpdatedAt)
		return err
	})
}

func (s *MetadataPlatformWorkloadStore) remove(tenantID, workloadID, name, idempotencyKey string) {
	_ = name
	_ = s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		_, _ = tx.Exec(ctx, `DELETE FROM platform_workload_intents WHERE tenant_id = $1::uuid AND idempotency_key = $2::uuid`, tenantID, idempotencyKey)
		_, err := tx.Exec(ctx, `DELETE FROM platform_workloads WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, workloadID)
		return err
	})
}

func (s *MetadataPlatformWorkloadStore) intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool) {
	var intent platformWorkloadIntent
	var found bool
	err := s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		err := tx.QueryRow(ctx, `
			SELECT fingerprint, workload_id::text
			FROM platform_workload_intents
			WHERE tenant_id = $1::uuid AND idempotency_key = $2::uuid
		`, tenantID, idempotencyKey).Scan(&intent.fingerprint, &intent.workloadID)
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) || strings.Contains(err.Error(), "no rows") {
				return nil
			}
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return platformWorkloadIntent{}, false
	}
	return intent, true
}

func (s *MetadataPlatformWorkloadStore) putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent) {
	_ = s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO platform_workload_intents (tenant_id, idempotency_key, fingerprint, workload_id)
			VALUES ($1::uuid, $2::uuid, $3, $4::uuid)
			ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET
				fingerprint = EXCLUDED.fingerprint,
				workload_id = EXCLUDED.workload_id
		`, tenantID, idempotencyKey, intent.fingerprint, intent.workloadID)
		return err
	})
}

func (s *MetadataPlatformWorkloadStore) nameID(tenantID, name string) (string, bool) {
	var id string
	err := s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `
			SELECT id::text FROM platform_workloads
			WHERE tenant_id = $1::uuid AND name = $2 AND NOT deleted
		`, tenantID, name).Scan(&id)
	})
	if err != nil || id == "" {
		return "", false
	}
	return id, true
}

func (s *MetadataPlatformWorkloadStore) deleteName(tenantID, name string) {
	_ = tenantID
	_ = name
}

func (s *MetadataPlatformWorkloadStore) withTenant(tenantID string, fn func(context.Context, ports.MetadataTx) error) error {
	if s == nil || s.store == nil {
		return ports.ErrNotConfigured
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := types.TryFromContext(ctx); !ok {
		parsed, err := uuid.Parse(strings.TrimSpace(tenantID))
		if err != nil {
			return fmt.Errorf("%w: platform workload store requires UUID tenant_id", ports.ErrInvalid)
		}
		ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: parsed})
	}
	return s.store.WithTenantTx(ctx, fn)
}

var _ platformWorkloadStore = (*MetadataPlatformWorkloadStore)(nil)
var _ platformWorkloadStore = (*memoryPlatformWorkloadStore)(nil)
