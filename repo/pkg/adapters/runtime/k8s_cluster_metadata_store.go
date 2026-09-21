package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// MetadataK8sClusterStore 是 ports.K8sClusterStore 的 PostgreSQL 实现。集群记录此前只
// 存在网关进程内存，每次滚动重启即丢失，导致界面看不到集群而底座 Helm release 仍在。
type MetadataK8sClusterStore struct {
	store ports.MetadataStore
}

func NewMetadataK8sClusterStore(store ports.MetadataStore) *MetadataK8sClusterStore {
	return &MetadataK8sClusterStore{store: store}
}

const k8sClusterSelectSQL = `
	SELECT tenant_id::text, cluster_id, name, version, state, reason, provider, real_provider,
	       provider_refs, created_at, updated_at
	FROM k8s_clusters
`

func (s *MetadataK8sClusterStore) UpsertK8sCluster(ctx context.Context, record ports.K8sClusterRecord, createIdempotencyKey string) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := validateK8sClusterStoreRecord(record); err != nil {
		return err
	}
	providerRefs, err := marshalK8sClusterProviderRefs(record.ProviderRefs)
	if err != nil {
		return err
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, record.TenantID)
	if err != nil {
		return err
	}
	return s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 冲突目标是主键 (tenant_id, cluster_id)；同租户第二个集群命中的是
		// idx_k8s_clusters_tenant_unique 唯一索引，PostgreSQL 报 23505，由
		// mapK8sClusterStoreWriteError 转成 ErrConflict（409）。
		_, err := tx.Exec(ctx, `
			INSERT INTO k8s_clusters (
				tenant_id, cluster_id, name, version, state, reason, provider, real_provider,
				provider_refs, create_idempotency_key, created_at, updated_at
			)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, NULLIF($10, ''), $11, $12)
			ON CONFLICT (tenant_id, cluster_id) DO UPDATE SET
				name = EXCLUDED.name,
				version = EXCLUDED.version,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				provider = EXCLUDED.provider,
				real_provider = EXCLUDED.real_provider,
				provider_refs = EXCLUDED.provider_refs,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, k8s_clusters.create_idempotency_key),
				updated_at = EXCLUDED.updated_at
		`,
			record.TenantID, record.ClusterID, record.Name, record.Version, string(record.State), record.Reason,
			record.Provider, record.RealProvider, providerRefs, createIdempotencyKey,
			time.Unix(record.CreatedAt, 0).UTC(), time.Unix(record.UpdatedAt, 0).UTC())
		if err != nil {
			return mapK8sClusterStoreWriteError(err)
		}
		return nil
	})
}

func (s *MetadataK8sClusterStore) GetK8sCluster(ctx context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	if s.store == nil {
		return ports.K8sClusterRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.ClusterID) == "" {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: tenant_id/cluster_id required for k8s cluster lookup", ports.ErrInvalid)
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, req.TenantID)
	if err != nil {
		return ports.K8sClusterRecord{}, err
	}
	var record ports.K8sClusterRecord
	err = s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		return readK8sCluster(tx.QueryRow(ctx, k8sClusterSelectSQL+`
			WHERE tenant_id = $1::uuid AND cluster_id = $2
		`, req.TenantID, req.ClusterID), &record)
	})
	if err != nil {
		return ports.K8sClusterRecord{}, err
	}
	return record, nil
}

func (s *MetadataK8sClusterStore) ListK8sClusters(ctx context.Context, req ports.K8sClusterListRequest) ([]ports.K8sClusterRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id required for k8s cluster list", ports.ErrInvalid)
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}
	records := []ports.K8sClusterRecord{}
	err = s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, k8sClusterSelectSQL+`
			WHERE tenant_id = $1::uuid
			ORDER BY created_at ASC, cluster_id ASC
		`, req.TenantID)
		if err != nil {
			return fmt.Errorf("list k8s clusters: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.K8sClusterRecord
			if err := readK8sCluster(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataK8sClusterStore) DeleteK8sCluster(ctx context.Context, req ports.K8sClusterGetRequest) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.ClusterID) == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id required for k8s cluster delete", ports.ErrInvalid)
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, req.TenantID)
	if err != nil {
		return err
	}
	return s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM k8s_clusters
			WHERE tenant_id = $1::uuid AND cluster_id = $2
		`, req.TenantID, req.ClusterID); err != nil {
			return fmt.Errorf("delete k8s cluster record: %w", err)
		}
		return nil
	})
}

func (s *MetadataK8sClusterStore) FindK8sClusterByCreateIdempotencyKey(ctx context.Context, tenantID string, idempotencyKey string) (ports.K8sClusterRecord, error) {
	return s.findK8sClusterByIdempotencyKey(ctx, tenantID, idempotencyKey, "create_idempotency_key", "k8s cluster create")
}

func (s *MetadataK8sClusterStore) SetK8sClusterUpgradeIdempotency(ctx context.Context, tenantID string, clusterID string, idempotencyKey string) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clusterID) == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id required for k8s cluster upgrade idempotency", ports.ErrInvalid)
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, tenantID)
	if err != nil {
		return err
	}
	return s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE k8s_clusters SET upgrade_idempotency_key = NULLIF($3, '')
			WHERE tenant_id = $1::uuid AND cluster_id = $2
		`, tenantID, clusterID, idempotencyKey); err != nil {
			return fmt.Errorf("set k8s cluster upgrade idempotency: %w", err)
		}
		return nil
	})
}

func (s *MetadataK8sClusterStore) FindK8sClusterByUpgradeIdempotencyKey(ctx context.Context, tenantID string, idempotencyKey string) (ports.K8sClusterRecord, error) {
	return s.findK8sClusterByIdempotencyKey(ctx, tenantID, idempotencyKey, "upgrade_idempotency_key", "k8s cluster upgrade")
}

func (s *MetadataK8sClusterStore) findK8sClusterByIdempotencyKey(ctx context.Context, tenantID string, idempotencyKey string, column string, label string) (ports.K8sClusterRecord, error) {
	if s.store == nil {
		return ports.K8sClusterRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: tenant_id required for %s idempotency lookup", ports.ErrInvalid, label)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: idempotency_key required for %s idempotency lookup", ports.ErrInvalid, label)
	}
	tenantCtx, err := k8sClusterStoreTenantContext(ctx, tenantID)
	if err != nil {
		return ports.K8sClusterRecord{}, err
	}
	var record ports.K8sClusterRecord
	err = s.store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		return readK8sCluster(tx.QueryRow(ctx, k8sClusterSelectSQL+`
			WHERE tenant_id = $1::uuid AND `+column+` = $2
		`, tenantID, idempotencyKey), &record)
	})
	if err != nil {
		return ports.K8sClusterRecord{}, err
	}
	return record, nil
}

// readK8sCluster 把一行记录读进 record，并把「无记录」统一映射为 ports.ErrNotFound。
func readK8sCluster(row ports.Row, record *ports.K8sClusterRecord) error {
	err := scanK8sCluster(row, record)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	return err
}

func scanK8sCluster(row ports.Row, record *ports.K8sClusterRecord) error {
	var (
		state        string
		providerRefs []byte
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(&record.TenantID, &record.ClusterID, &record.Name, &record.Version, &state,
		&record.Reason, &record.Provider, &record.RealProvider, &providerRefs, &createdAt, &updatedAt); err != nil {
		return err
	}
	refs, err := unmarshalK8sClusterProviderRefs(providerRefs)
	if err != nil {
		return err
	}
	record.State = ports.K8sClusterState(state)
	record.ProviderRefs = refs
	record.CreatedAt = createdAt.Unix()
	record.UpdatedAt = updatedAt.Unix()
	return nil
}

func mapK8sClusterStoreWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// 命中 idx_k8s_clusters_tenant_unique：该租户已有 vCluster（ANI-02 §2.1.2）。
		return fmt.Errorf("%w: tenant already has a k8s cluster; only one vCluster per tenant is supported", ports.ErrConflict)
	}
	return fmt.Errorf("upsert k8s cluster record: %w", err)
}

func validateK8sClusterStoreRecord(record ports.K8sClusterRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.ClusterID) == "" || strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id/name required for k8s cluster record", ports.ErrInvalid)
	}
	if record.State == "" {
		return fmt.Errorf("%w: state required for k8s cluster record", ports.ErrInvalid)
	}
	return nil
}

func marshalK8sClusterProviderRefs(refs []string) (string, error) {
	if refs == nil {
		refs = []string{}
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return "", fmt.Errorf("%w: encode k8s cluster provider refs: %v", ports.ErrInvalid, err)
	}
	return string(encoded), nil
}

func unmarshalK8sClusterProviderRefs(encoded []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" {
		return []string{}, nil
	}
	var refs []string
	if err := json.Unmarshal([]byte(trimmed), &refs); err != nil {
		return nil, fmt.Errorf("%w: decode k8s cluster provider refs: %v", ports.ErrInvalid, err)
	}
	if refs == nil {
		refs = []string{}
	}
	return refs, nil
}

func k8sClusterStoreTenantContext(ctx context.Context, tenantID string) (context.Context, error) {
	if _, ok := types.TryFromContext(ctx); ok {
		return ctx, nil
	}
	parsed, err := uuid.Parse(strings.TrimSpace(tenantID))
	if err != nil {
		return nil, fmt.Errorf("%w: metadata-backed k8s cluster persistence requires UUID tenant_id", ports.ErrInvalid)
	}
	return types.WithTenant(ctx, &types.TenantContext{TenantID: parsed}), nil
}

var _ ports.K8sClusterStore = (*MetadataK8sClusterStore)(nil)
