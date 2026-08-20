package runtime

import (
	"context"
	"encoding/json"
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
	item, ok, err := s.getRaw(tenantID, workloadID)
	if err != nil {
		return kubernetesPlatformWorkload{}, err
	}
	if !ok || item.deleted {
		return kubernetesPlatformWorkload{}, ports.ErrNotFound
	}
	return item, nil
}

func (s *MetadataPlatformWorkloadStore) getRaw(tenantID, workloadID string) (kubernetesPlatformWorkload, bool, error) {
	var item kubernetesPlatformWorkload
	var found bool
	err := s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		decoded, ok, err := scanPlatformWorkload(ctx, tx, tenantID, workloadID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		item = decoded
		found = true
		return nil
	})
	if err != nil {
		return kubernetesPlatformWorkload{}, false, err
	}
	return item, found, nil
}

func (s *MetadataPlatformWorkloadStore) put(item kubernetesPlatformWorkload) error {
	specJSON, recordJSON, err := encodePlatformWorkload(item)
	if err != nil {
		return err
	}
	return s.withTenant(item.record.TenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		return insertPlatformWorkload(ctx, tx, item, specJSON, recordJSON)
	})
}

func (s *MetadataPlatformWorkloadStore) putWithIntent(item kubernetesPlatformWorkload, idempotencyKey string, intent platformWorkloadIntent) error {
	specJSON, recordJSON, err := encodePlatformWorkload(item)
	if err != nil {
		return err
	}
	if intent.status == "" {
		intent.status = platformWorkloadIntentPending
	}
	return s.withTenant(item.record.TenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		if err := insertPlatformWorkload(ctx, tx, item, specJSON, recordJSON); err != nil {
			return err
		}
		tag, err := reservePlatformWorkloadIntent(ctx, tx, item.record.TenantID, idempotencyKey, intent)
		if err != nil {
			return err
		}
		if tag.RowsAffected == 0 {
			return errPlatformWorkloadIntentReplay
		}
		return nil
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

func (s *MetadataPlatformWorkloadStore) intent(tenantID, idempotencyKey string) (platformWorkloadIntent, bool, error) {
	var intent platformWorkloadIntent
	var found bool
	err := s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		decoded, ok, err := scanPlatformWorkloadIntent(ctx, tx, tenantID, idempotencyKey)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		intent = decoded
		found = true
		return nil
	})
	if err != nil {
		return platformWorkloadIntent{}, false, err
	}
	return intent, found, nil
}

func (s *MetadataPlatformWorkloadStore) putIntent(tenantID, idempotencyKey string, intent platformWorkloadIntent) error {
	if intent.status == "" {
		intent.status = platformWorkloadIntentPending
	}
	return s.withTenant(tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
		tag, err := insertPlatformWorkloadIntent(ctx, tx, tenantID, idempotencyKey, intent)
		if err != nil {
			return err
		}
		if tag.RowsAffected > 0 {
			return nil
		}
		existing, ok, err := scanPlatformWorkloadIntent(ctx, tx, tenantID, idempotencyKey)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("platform workload intent persist failed")
		}
		if existing.fingerprint != intent.fingerprint {
			return platformWorkloadIntentConflict()
		}
		return nil
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

func encodePlatformWorkload(item kubernetesPlatformWorkload) ([]byte, []byte, error) {
	specJSON, err := json.Marshal(item.spec)
	if err != nil {
		return nil, nil, fmt.Errorf("encode platform workload spec: %w", err)
	}
	recordJSON, err := json.Marshal(item.record)
	if err != nil {
		return nil, nil, fmt.Errorf("encode platform workload record: %w", err)
	}
	return specJSON, recordJSON, nil
}

func insertPlatformWorkload(ctx context.Context, tx ports.MetadataTx, item kubernetesPlatformWorkload, specJSON, recordJSON []byte) error {
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
}

func insertPlatformWorkloadIntent(ctx context.Context, tx ports.MetadataTx, tenantID, idempotencyKey string, intent platformWorkloadIntent) (ports.CommandTag, error) {
	return tx.Exec(ctx, `
			INSERT INTO platform_workload_intents (tenant_id, idempotency_key, fingerprint, workload_id, status)
			VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5)
			ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET
				status = EXCLUDED.status
			WHERE platform_workload_intents.fingerprint = EXCLUDED.fingerprint
		`, tenantID, idempotencyKey, intent.fingerprint, intent.workloadID, intent.status)
}

func reservePlatformWorkloadIntent(ctx context.Context, tx ports.MetadataTx, tenantID, idempotencyKey string, intent platformWorkloadIntent) (ports.CommandTag, error) {
	return tx.Exec(ctx, `
			INSERT INTO platform_workload_intents (tenant_id, idempotency_key, fingerprint, workload_id, status)
			VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5)
			ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		`, tenantID, idempotencyKey, intent.fingerprint, intent.workloadID, intent.status)
}

func scanPlatformWorkload(ctx context.Context, tx ports.MetadataTx, tenantID, workloadID string) (kubernetesPlatformWorkload, bool, error) {
	var specJSON, recordJSON []byte
	var deleted bool
	err := tx.QueryRow(ctx, `
			SELECT spec, record, deleted
			FROM platform_workloads
			WHERE tenant_id = $1::uuid AND id = $2::uuid
		`, tenantID, workloadID).Scan(&specJSON, &recordJSON, &deleted)
	if err != nil {
		if isNoRows(err) {
			return kubernetesPlatformWorkload{}, false, nil
		}
		return kubernetesPlatformWorkload{}, false, err
	}
	var item kubernetesPlatformWorkload
	if err := json.Unmarshal(specJSON, &item.spec); err != nil {
		return kubernetesPlatformWorkload{}, false, fmt.Errorf("decode platform workload spec: %w", err)
	}
	if err := json.Unmarshal(recordJSON, &item.record); err != nil {
		return kubernetesPlatformWorkload{}, false, fmt.Errorf("decode platform workload record: %w", err)
	}
	item.deleted = deleted
	return item, true, nil
}

func scanPlatformWorkloadIntent(ctx context.Context, tx ports.MetadataTx, tenantID, idempotencyKey string) (platformWorkloadIntent, bool, error) {
	var intent platformWorkloadIntent
	err := tx.QueryRow(ctx, `
			SELECT fingerprint, workload_id::text, status
			FROM platform_workload_intents
			WHERE tenant_id = $1::uuid AND idempotency_key = $2::uuid
		`, tenantID, idempotencyKey).Scan(&intent.fingerprint, &intent.workloadID, &intent.status)
	if err != nil {
		if isNoRows(err) {
			return platformWorkloadIntent{}, false, nil
		}
		return platformWorkloadIntent{}, false, err
	}
	return intent, true, nil
}

var _ platformWorkloadStore = (*MetadataPlatformWorkloadStore)(nil)
var _ platformWorkloadStore = (*memoryPlatformWorkloadStore)(nil)
