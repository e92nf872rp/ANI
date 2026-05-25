package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type MetadataK8sClusterProxyTargetStore struct {
	store ports.MetadataStore
}

func NewMetadataK8sClusterProxyTargetStore(store ports.MetadataStore) *MetadataK8sClusterProxyTargetStore {
	return &MetadataK8sClusterProxyTargetStore{store: store}
}

func (s *MetadataK8sClusterProxyTargetStore) UpsertK8sClusterProxyTarget(ctx context.Context, target ports.K8sClusterProxyTarget) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := validateK8sClusterProxyTarget(target); err != nil {
		return err
	}
	target = cloneK8sClusterProxyTarget(target)
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO k8s_cluster_proxy_targets (tenant_id, cluster_id, server, bearer_token, updated_at)
			VALUES ($1::uuid, $2, $3, NULLIF($4, ''), NOW())
			ON CONFLICT (tenant_id, cluster_id) DO UPDATE SET
				server = EXCLUDED.server,
				bearer_token = EXCLUDED.bearer_token,
				updated_at = EXCLUDED.updated_at
		`, target.TenantID, target.ClusterID, target.Server, target.BearerToken)
		if err != nil {
			return fmt.Errorf("upsert k8s cluster proxy target: %w", err)
		}
		return nil
	})
}

func (s *MetadataK8sClusterProxyTargetStore) ResolveK8sClusterProxyTarget(ctx context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterProxyTarget, error) {
	if s.store == nil {
		return ports.K8sClusterProxyTarget{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.ClusterID) == "" {
		return ports.K8sClusterProxyTarget{}, fmt.Errorf("%w: tenant_id/cluster_id required for k8s proxy target lookup", ports.ErrInvalid)
	}

	var target ports.K8sClusterProxyTarget
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, cluster_id, server, COALESCE(bearer_token, '')
			FROM k8s_cluster_proxy_targets
			WHERE tenant_id = $1::uuid AND cluster_id = $2
		`, req.TenantID, req.ClusterID)
		return row.Scan(&target.TenantID, &target.ClusterID, &target.Server, &target.BearerToken)
	})
	if err != nil {
		return ports.K8sClusterProxyTarget{}, err
	}
	return cloneK8sClusterProxyTarget(target), nil
}

func (s *MetadataK8sClusterProxyTargetStore) DeleteK8sClusterProxyTarget(ctx context.Context, req ports.K8sClusterGetRequest) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.ClusterID) == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id required for k8s proxy target delete", ports.ErrInvalid)
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			DELETE FROM k8s_cluster_proxy_targets
			WHERE tenant_id = $1::uuid AND cluster_id = $2
		`, req.TenantID, req.ClusterID)
		if err != nil {
			return fmt.Errorf("delete k8s cluster proxy target: %w", err)
		}
		return nil
	})
}

var _ ports.K8sClusterProxyTargetStore = (*MetadataK8sClusterProxyTargetStore)(nil)
