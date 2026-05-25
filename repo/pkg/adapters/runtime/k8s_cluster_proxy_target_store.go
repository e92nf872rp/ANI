package runtime

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalK8sClusterProxyTargetStore struct {
	mu      sync.Mutex
	targets map[string]ports.K8sClusterProxyTarget
}

func NewLocalK8sClusterProxyTargetStore() *LocalK8sClusterProxyTargetStore {
	return &LocalK8sClusterProxyTargetStore{targets: map[string]ports.K8sClusterProxyTarget{}}
}

func (s *LocalK8sClusterProxyTargetStore) UpsertK8sClusterProxyTarget(_ context.Context, target ports.K8sClusterProxyTarget) error {
	if err := validateK8sClusterProxyTarget(target); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[k8sClusterProxyTargetKey(target.TenantID, target.ClusterID)] = cloneK8sClusterProxyTarget(target)
	return nil
}

func (s *LocalK8sClusterProxyTargetStore) ResolveK8sClusterProxyTarget(_ context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterProxyTarget, error) {
	if req.TenantID == "" || req.ClusterID == "" {
		return ports.K8sClusterProxyTarget{}, fmt.Errorf("%w: tenant_id/cluster_id required for k8s proxy target lookup", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, ok := s.targets[k8sClusterProxyTargetKey(req.TenantID, req.ClusterID)]
	if !ok {
		return ports.K8sClusterProxyTarget{}, ports.ErrNotFound
	}
	return cloneK8sClusterProxyTarget(target), nil
}

func (s *LocalK8sClusterProxyTargetStore) DeleteK8sClusterProxyTarget(_ context.Context, req ports.K8sClusterGetRequest) error {
	if req.TenantID == "" || req.ClusterID == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id required for k8s proxy target delete", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.targets, k8sClusterProxyTargetKey(req.TenantID, req.ClusterID))
	return nil
}

func validateK8sClusterProxyTarget(target ports.K8sClusterProxyTarget) error {
	if target.TenantID == "" || target.ClusterID == "" || strings.TrimSpace(target.Server) == "" {
		return fmt.Errorf("%w: tenant_id/cluster_id/server required for k8s proxy target", ports.ErrInvalid)
	}
	if _, err := url.ParseRequestURI(strings.TrimSpace(target.Server)); err != nil {
		return fmt.Errorf("%w: invalid k8s proxy target server: %v", ports.ErrInvalid, err)
	}
	return nil
}

func k8sClusterProxyTargetKey(tenantID string, clusterID string) string {
	return tenantID + "/" + clusterID
}

func cloneK8sClusterProxyTarget(target ports.K8sClusterProxyTarget) ports.K8sClusterProxyTarget {
	target.Server = strings.TrimRight(strings.TrimSpace(target.Server), "/")
	target.BearerToken = strings.TrimSpace(target.BearerToken)
	return target
}

var _ ports.K8sClusterProxyTargetStore = (*LocalK8sClusterProxyTargetStore)(nil)
