package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type localK8sClusterService struct {
	mu                 sync.Mutex
	byID               map[string]ports.K8sClusterRecord
	nodePools          map[string]ports.K8sClusterNodePoolRecord
	idem               map[string]string
	upgradeIdem        map[string]ports.K8sClusterRecord
	nodePoolCreateIdem map[string]string
	nodePoolUpdateIdem map[string]ports.K8sClusterNodePoolRecord
	providerApply      ports.K8sClusterProviderApply
	providerUpgrade    ports.K8sClusterProviderUpgrade
	nodePoolProvider   ports.K8sClusterNodePoolProvider
	kubeconfigProvider ports.K8sClusterKubeconfigProvider
	targetStore        ports.K8sClusterProxyTargetStore
}

type K8sClusterServiceOption func(*localK8sClusterService)

func WithK8sClusterProviderApply(provider ports.K8sClusterProviderApply) K8sClusterServiceOption {
	return func(service *localK8sClusterService) {
		service.providerApply = provider
	}
}

func WithK8sClusterProviderUpgrade(provider ports.K8sClusterProviderUpgrade) K8sClusterServiceOption {
	return func(service *localK8sClusterService) {
		service.providerUpgrade = provider
	}
}

func WithK8sClusterNodePoolProvider(provider ports.K8sClusterNodePoolProvider) K8sClusterServiceOption {
	return func(service *localK8sClusterService) {
		service.nodePoolProvider = provider
	}
}

func WithK8sClusterKubeconfigProvider(provider ports.K8sClusterKubeconfigProvider) K8sClusterServiceOption {
	return func(service *localK8sClusterService) {
		service.kubeconfigProvider = provider
	}
}

func WithK8sClusterProxyTargetStore(store ports.K8sClusterProxyTargetStore) K8sClusterServiceOption {
	return func(service *localK8sClusterService) {
		service.targetStore = store
	}
}

func NewLocalK8sClusterService(options ...K8sClusterServiceOption) ports.K8sClusterService {
	service := &localK8sClusterService{
		byID:               map[string]ports.K8sClusterRecord{},
		nodePools:          map[string]ports.K8sClusterNodePoolRecord{},
		idem:               map[string]string{},
		upgradeIdem:        map[string]ports.K8sClusterRecord{},
		nodePoolCreateIdem: map[string]string{},
		nodePoolUpdateIdem: map[string]ports.K8sClusterNodePoolRecord{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *localK8sClusterService) CreateCluster(ctx context.Context, req ports.K8sClusterCreateRequest) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.Name == "" || req.IdempotencyKey == "" {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: tenant_id/name/idempotency_key required", ports.ErrInvalid)
	}
	key := req.TenantID + ":" + req.IdempotencyKey
	if id, ok := s.idem[key]; ok {
		return s.byID[id], nil
	}
	now := time.Now().Unix()
	rec := ports.K8sClusterRecord{ClusterID: "k8sclu-" + uuid.NewString(), TenantID: req.TenantID, Name: req.Name, Version: req.Version, State: ports.K8sClusterStateRunning, Reason: "local vcluster profile", Provider: "local", CreatedAt: now, UpdatedAt: now}
	s.byID[rec.ClusterID] = rec
	s.idem[key] = rec.ClusterID
	if s.providerApply != nil {
		result, err := s.providerApply.ApplyK8sCluster(ctx, ports.K8sClusterProviderApplyRequest{
			TenantID:  rec.TenantID,
			ClusterID: rec.ClusterID,
			Name:      rec.Name,
			Version:   rec.Version,
		})
		if err != nil {
			delete(s.byID, rec.ClusterID)
			delete(s.idem, key)
			return ports.K8sClusterRecord{}, err
		}
		if !result.Applied {
			delete(s.byID, rec.ClusterID)
			delete(s.idem, key)
			return ports.K8sClusterRecord{}, fmt.Errorf("%w: k8s cluster provider did not apply cluster", ports.ErrNotConfigured)
		}
		rec.Provider = result.Provider
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		rec.Reason = firstNonEmpty(result.Reason, "vCluster provider applied")
		rec.State = ports.K8sClusterStateRunning
		rec.UpdatedAt = time.Now().Unix()
		if s.targetStore != nil && result.ProxyTarget.Server != "" {
			target := result.ProxyTarget
			target.TenantID = rec.TenantID
			target.ClusterID = rec.ClusterID
			if err := s.targetStore.UpsertK8sClusterProxyTarget(ctx, target); err != nil {
				delete(s.byID, rec.ClusterID)
				delete(s.idem, key)
				return ports.K8sClusterRecord{}, err
			}
		}
		s.byID[rec.ClusterID] = rec
	}
	return rec, nil
}

func (s *localK8sClusterService) GetCluster(_ context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	return rec, nil
}
func (s *localK8sClusterService) ListClusters(_ context.Context, req ports.K8sClusterListRequest) ([]ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.K8sClusterRecord{}
	for _, r := range s.byID {
		if r.TenantID == req.TenantID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (s *localK8sClusterService) DeleteCluster(ctx context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	rec.State = ports.K8sClusterStateDeleting
	rec.UpdatedAt = time.Now().Unix()
	s.byID[req.ClusterID] = rec
	if s.targetStore != nil {
		if err := s.targetStore.DeleteK8sClusterProxyTarget(ctx, req); err != nil && err != ports.ErrNotFound {
			return ports.K8sClusterRecord{}, err
		}
	}
	return rec, nil
}

func (s *localK8sClusterService) UpgradeCluster(ctx context.Context, req ports.K8sClusterUpgradeRequest) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.ClusterID == "" || req.IdempotencyKey == "" || req.Version == "" {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: tenant_id/cluster_id/idempotency_key/version required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.ClusterID + ":" + req.IdempotencyKey
	if rec, ok := s.upgradeIdem[idemKey]; ok {
		return rec, nil
	}
	rec, ok := s.byID[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	if rec.State != ports.K8sClusterStateRunning {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: upgrade requires a running k8s cluster", ports.ErrConflict)
	}
	previousVersion := rec.Version
	rec.Version = req.Version
	rec.Reason = "local vcluster profile upgraded"
	if s.providerUpgrade != nil && rec.RealProvider {
		result, err := s.providerUpgrade.UpgradeK8sCluster(ctx, ports.K8sClusterProviderUpgradeRequest{
			TenantID:       rec.TenantID,
			ClusterID:      rec.ClusterID,
			Name:           rec.Name,
			CurrentVersion: previousVersion,
			TargetVersion:  req.Version,
		})
		if err != nil {
			return ports.K8sClusterRecord{}, err
		}
		if !result.Applied {
			return ports.K8sClusterRecord{}, fmt.Errorf("%w: k8s cluster provider did not apply upgrade", ports.ErrNotConfigured)
		}
		rec.Provider = firstNonEmpty(result.Provider, rec.Provider)
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		rec.Reason = firstNonEmpty(result.Reason, "vCluster provider upgraded")
	}
	rec.State = ports.K8sClusterStateRunning
	rec.UpdatedAt = time.Now().Unix()
	s.byID[req.ClusterID] = rec
	s.upgradeIdem[idemKey] = rec
	return rec, nil
}

func (s *localK8sClusterService) CreateNodePool(ctx context.Context, req ports.K8sClusterNodePoolCreateRequest) (ports.K8sClusterNodePoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.ClusterID == "" || req.IdempotencyKey == "" || req.Name == "" || req.InstanceType == "" {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: tenant_id/cluster_id/idempotency_key/name/instance_type required", ports.ErrInvalid)
	}
	if req.NodeCount <= 0 {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: node_count must be greater than zero", ports.ErrInvalid)
	}
	if req.GPU.Count < 0 {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: gpu.count cannot be negative", ports.ErrInvalid)
	}
	cluster, err := s.requireRunningClusterLocked(req.TenantID, req.ClusterID, "create node pool")
	if err != nil {
		return ports.K8sClusterNodePoolRecord{}, err
	}
	idemKey := req.TenantID + ":" + req.ClusterID + ":" + req.IdempotencyKey
	if id, ok := s.nodePoolCreateIdem[idemKey]; ok {
		return s.nodePools[id], nil
	}
	now := time.Now().Unix()
	rec := ports.K8sClusterNodePoolRecord{
		NodePoolID:   "k8snp-" + uuid.NewString(),
		TenantID:     req.TenantID,
		ClusterID:    req.ClusterID,
		Name:         req.Name,
		NodeCount:    req.NodeCount,
		InstanceType: req.InstanceType,
		GPU:          req.GPU,
		State:        ports.K8sClusterNodePoolStateRunning,
		Reason:       "local vcluster node pool profile",
		Provider:     "local",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if s.nodePoolProvider != nil && cluster.RealProvider {
		result, err := s.nodePoolProvider.ApplyK8sClusterNodePool(ctx, k8sClusterNodePoolProviderRequest("create", cluster, rec))
		if err != nil {
			return ports.K8sClusterNodePoolRecord{}, err
		}
		if !result.Applied {
			return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: k8s cluster node pool provider did not apply node pool", ports.ErrNotConfigured)
		}
		rec.Provider = firstNonEmpty(result.Provider, rec.Provider)
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		rec.Reason = firstNonEmpty(result.Reason, "node pool provider applied")
		rec.UpdatedAt = time.Now().Unix()
	}
	s.nodePools[rec.NodePoolID] = rec
	s.nodePoolCreateIdem[idemKey] = rec.NodePoolID
	return rec, nil
}

func (s *localK8sClusterService) GetNodePool(_ context.Context, req ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.requireTenantClusterLocked(req.TenantID, req.ClusterID); err != nil {
		return ports.K8sClusterNodePoolRecord{}, err
	}
	rec, ok := s.nodePools[req.NodePoolID]
	if !ok || rec.TenantID != req.TenantID || rec.ClusterID != req.ClusterID {
		return ports.K8sClusterNodePoolRecord{}, ports.ErrNotFound
	}
	return rec, nil
}

func (s *localK8sClusterService) ListNodePools(_ context.Context, req ports.K8sClusterNodePoolListRequest) ([]ports.K8sClusterNodePoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.requireTenantClusterLocked(req.TenantID, req.ClusterID); err != nil {
		return nil, err
	}
	out := []ports.K8sClusterNodePoolRecord{}
	for _, rec := range s.nodePools {
		if rec.TenantID == req.TenantID && rec.ClusterID == req.ClusterID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (s *localK8sClusterService) UpdateNodePool(ctx context.Context, req ports.K8sClusterNodePoolUpdateRequest) (ports.K8sClusterNodePoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.ClusterID == "" || req.NodePoolID == "" || req.IdempotencyKey == "" || req.InstanceType == "" {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: tenant_id/cluster_id/node_pool_id/idempotency_key/instance_type required", ports.ErrInvalid)
	}
	if req.NodeCount < 0 {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: node_count cannot be negative", ports.ErrInvalid)
	}
	if req.GPU.Count < 0 {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: gpu.count cannot be negative", ports.ErrInvalid)
	}
	cluster, err := s.requireRunningClusterLocked(req.TenantID, req.ClusterID, "update node pool")
	if err != nil {
		return ports.K8sClusterNodePoolRecord{}, err
	}
	idemKey := req.TenantID + ":" + req.ClusterID + ":" + req.NodePoolID + ":" + req.IdempotencyKey
	if rec, ok := s.nodePoolUpdateIdem[idemKey]; ok {
		return rec, nil
	}
	rec, ok := s.nodePools[req.NodePoolID]
	if !ok || rec.TenantID != req.TenantID || rec.ClusterID != req.ClusterID {
		return ports.K8sClusterNodePoolRecord{}, ports.ErrNotFound
	}
	if rec.State == ports.K8sClusterNodePoolStateDeleting {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: cannot update deleting node pool", ports.ErrConflict)
	}
	rec.NodeCount = req.NodeCount
	rec.InstanceType = req.InstanceType
	rec.GPU = req.GPU
	rec.State = ports.K8sClusterNodePoolStateRunning
	rec.Reason = "local vcluster node pool profile updated"
	rec.UpdatedAt = time.Now().Unix()
	if s.nodePoolProvider != nil && cluster.RealProvider {
		result, err := s.nodePoolProvider.ApplyK8sClusterNodePool(ctx, k8sClusterNodePoolProviderRequest("update", cluster, rec))
		if err != nil {
			return ports.K8sClusterNodePoolRecord{}, err
		}
		if !result.Applied {
			return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: k8s cluster node pool provider did not apply node pool update", ports.ErrNotConfigured)
		}
		rec.Provider = firstNonEmpty(result.Provider, rec.Provider)
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		rec.Reason = firstNonEmpty(result.Reason, "node pool provider updated")
		rec.UpdatedAt = time.Now().Unix()
	}
	s.nodePools[req.NodePoolID] = rec
	s.nodePoolUpdateIdem[idemKey] = rec
	return rec, nil
}

func (s *localK8sClusterService) DeleteNodePool(ctx context.Context, req ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cluster, err := s.requireTenantClusterLocked(req.TenantID, req.ClusterID)
	if err != nil {
		return ports.K8sClusterNodePoolRecord{}, err
	}
	rec, ok := s.nodePools[req.NodePoolID]
	if !ok || rec.TenantID != req.TenantID || rec.ClusterID != req.ClusterID {
		return ports.K8sClusterNodePoolRecord{}, ports.ErrNotFound
	}
	rec.State = ports.K8sClusterNodePoolStateDeleting
	rec.Reason = "local vcluster node pool profile deleting"
	rec.UpdatedAt = time.Now().Unix()
	if s.nodePoolProvider != nil && cluster.RealProvider {
		result, err := s.nodePoolProvider.DeleteK8sClusterNodePool(ctx, k8sClusterNodePoolProviderRequest("delete", cluster, rec))
		if err != nil {
			return ports.K8sClusterNodePoolRecord{}, err
		}
		if !result.Applied {
			return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: k8s cluster node pool provider did not apply node pool delete", ports.ErrNotConfigured)
		}
		rec.Provider = firstNonEmpty(result.Provider, rec.Provider)
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		rec.Reason = firstNonEmpty(result.Reason, "node pool provider delete intent applied")
		rec.UpdatedAt = time.Now().Unix()
	}
	s.nodePools[req.NodePoolID] = rec
	return rec, nil
}

func (s *localK8sClusterService) GetKubeconfig(ctx context.Context, req ports.K8sClusterKubeconfigRequest) (ports.K8sClusterKubeconfigRecord, error) {
	s.mu.Lock()
	rec, ok := s.byID[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		s.mu.Unlock()
		return ports.K8sClusterKubeconfigRecord{}, ports.ErrNotFound
	}
	if rec.State != ports.K8sClusterStateRunning {
		s.mu.Unlock()
		return ports.K8sClusterKubeconfigRecord{}, fmt.Errorf("%w: kubeconfig requires a running k8s cluster", ports.ErrConflict)
	}
	kubeconfigProvider := s.kubeconfigProvider
	s.mu.Unlock()
	if rec.RealProvider && kubeconfigProvider != nil {
		kubeconfig, err := kubeconfigProvider.GetK8sClusterKubeconfig(ctx, ports.K8sClusterKubeconfigProviderRequest{
			TenantID:  rec.TenantID,
			ClusterID: rec.ClusterID,
			Name:      rec.Name,
			Version:   rec.Version,
		})
		if err != nil {
			return ports.K8sClusterKubeconfigRecord{}, err
		}
		kubeconfig.TenantID = rec.TenantID
		kubeconfig.ClusterID = rec.ClusterID
		return kubeconfig, nil
	}
	now := time.Now().Unix()
	server := fmt.Sprintf("https://%s.local.ani.invalid", rec.ClusterID)
	namespace := "tenant-" + req.TenantID
	token := "local-kubeconfig-" + uuid.NewString()
	caData := base64.StdEncoding.EncodeToString([]byte("local-dev-profile-ca:" + rec.ClusterID))
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: %s
  context:
    cluster: %s
    namespace: %s
    user: %s-user
current-context: %s
users:
- name: %s-user
  user:
    token: %s
`, rec.Name, server, caData, rec.Name, rec.Name, namespace, rec.Name, rec.Name, rec.Name, token)
	return ports.K8sClusterKubeconfigRecord{
		ClusterID:  rec.ClusterID,
		TenantID:   rec.TenantID,
		Server:     server,
		Namespace:  namespace,
		CAData:     caData,
		Token:      token,
		Kubeconfig: kubeconfig,
		ExpiresAt:  now + 3600,
		CreatedAt:  now,
	}, nil
}

func (s *localK8sClusterService) Proxy(_ context.Context, req ports.K8sClusterProxyRequest) (ports.K8sClusterProxyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.K8sClusterProxyRecord{}, ports.ErrNotFound
	}
	if rec.State != ports.K8sClusterStateRunning {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: proxy requires a running k8s cluster", ports.ErrConflict)
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := normalizeK8sProxyPath(req.Path)
	if method == "" || path == "" {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: method/path required for k8s proxy", ports.ErrInvalid)
	}
	if !isAllowedK8sProxyPath(path) {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: k8s proxy path must start with /api/, /apis/, /healthz, /livez, /readyz or /version", ports.ErrInvalid)
	}
	if req.IdempotencyKey == "" {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: idempotency_key required for k8s proxy", ports.ErrInvalid)
	}
	now := time.Now().Unix()
	query := copyStringMap(req.Query)
	body := copyAnyMap(req.Body)
	return ports.K8sClusterProxyRecord{
		ClusterID:  rec.ClusterID,
		TenantID:   rec.TenantID,
		Method:     method,
		Path:       path,
		Query:      query,
		StatusCode: 200,
		Headers: map[string]string{
			"content-type":              "application/json",
			"x-ani-provider":            "local-k8s-cluster-service",
			"x-ani-k8s-cluster-version": rec.Version,
		},
		Body: map[string]any{
			"apiVersion": "v1",
			"kind":       "ANIProxyPreview",
			"metadata": map[string]any{
				"cluster_id": rec.ClusterID,
				"tenant_id":  rec.TenantID,
				"path":       path,
				"method":     method,
			},
			"request": map[string]any{
				"query": query,
				"body":  body,
			},
			"message": "local dev profile; request was not forwarded to a real vCluster API server",
		},
		ProxiedAt: now,
	}, nil
}

func (s *localK8sClusterService) requireTenantClusterLocked(tenantID string, clusterID string) (ports.K8sClusterRecord, error) {
	rec, ok := s.byID[clusterID]
	if !ok || rec.TenantID != tenantID {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	return rec, nil
}

func (s *localK8sClusterService) requireRunningClusterLocked(tenantID string, clusterID string, action string) (ports.K8sClusterRecord, error) {
	rec, err := s.requireTenantClusterLocked(tenantID, clusterID)
	if err != nil {
		return ports.K8sClusterRecord{}, err
	}
	if rec.State != ports.K8sClusterStateRunning {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: %s requires a running k8s cluster", ports.ErrConflict, action)
	}
	return rec, nil
}

func k8sClusterNodePoolProviderRequest(operation string, cluster ports.K8sClusterRecord, rec ports.K8sClusterNodePoolRecord) ports.K8sClusterNodePoolProviderRequest {
	return ports.K8sClusterNodePoolProviderRequest{
		Operation:    operation,
		TenantID:     rec.TenantID,
		ClusterID:    rec.ClusterID,
		ClusterName:  cluster.Name,
		NodePoolID:   rec.NodePoolID,
		Name:         rec.Name,
		NodeCount:    rec.NodeCount,
		InstanceType: rec.InstanceType,
		GPU:          rec.GPU,
	}
}

func normalizeK8sProxyPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func isAllowedK8sProxyPath(path string) bool {
	switch {
	case path == "/healthz", path == "/livez", path == "/readyz", path == "/version":
		return true
	case strings.HasPrefix(path, "/api/"), strings.HasPrefix(path, "/apis/"):
		return true
	default:
		return false
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
