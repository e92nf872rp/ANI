package router

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestK8sClusterAPIDevProfileAndIdempotency(t *testing.T) {
	api := newK8sClusterAPI()
	a, err := api.service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{TenantID: "t1", IdempotencyKey: "idem-1", Name: "vc-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{TenantID: "t1", IdempotencyKey: "idem-1", Name: "vc-a"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ClusterID != b.ClusterID {
		t.Fatalf("want idempotent cluster id, got %s != %s", a.ClusterID, b.ClusterID)
	}
	resp := k8sClusterFromRecord(a)
	requireLocalCoreDevProfile(t, resp.DevProfile, "local-k8s-cluster-service")

	kubeconfig, err := api.service.GetKubeconfig(context.Background(), ports.K8sClusterKubeconfigRequest{TenantID: "t1", ClusterID: a.ClusterID})
	if err != nil {
		t.Fatal(err)
	}
	if kubeconfig.Kubeconfig == "" || kubeconfig.Token == "" || kubeconfig.Server == "" {
		t.Fatalf("want kubeconfig content, got %+v", kubeconfig)
	}
	kubeconfigResp := k8sClusterKubeconfigFromRecord(kubeconfig)
	requireLocalCoreDevProfile(t, kubeconfigResp.DevProfile, "local-k8s-cluster-service")

	proxy, err := api.service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "t1",
		ClusterID:      a.ClusterID,
		IdempotencyKey: "idem-proxy-1",
		Method:         "get",
		Path:           "/api/v1/namespaces/default/pods",
		Query:          map[string]string{"limit": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Method != "GET" || proxy.StatusCode != 200 || proxy.Body["kind"] != "ANIProxyPreview" {
		t.Fatalf("unexpected proxy response: %+v", proxy)
	}
	proxyResp := k8sClusterProxyFromRecord(proxy)
	requireLocalCoreDevProfile(t, proxyResp.DevProfile, "local-k8s-cluster-service")

	nodePool, err := api.service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "t1",
		ClusterID:      a.ClusterID,
		IdempotencyKey: "idem-node-pool-1",
		Name:           "system-pool",
		NodeCount:      2,
		InstanceType:   "general.large",
	})
	if err != nil {
		t.Fatal(err)
	}
	if nodePool.ClusterID != a.ClusterID || nodePool.State != ports.K8sClusterNodePoolStateRunning {
		t.Fatalf("node pool = %+v, want running cluster node pool", nodePool)
	}
	nodePoolResp := k8sClusterNodePoolFromRecord(nodePool)
	requireLocalCoreDevProfile(t, nodePoolResp.DevProfile, "local-k8s-cluster-service")

	if _, err := api.service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "t1",
		ClusterID:      a.ClusterID,
		IdempotencyKey: "idem-proxy-2",
		Method:         "GET",
		Path:           "/forbidden",
	}); err == nil {
		t.Fatalf("want invalid path error")
	}
}

func TestK8sClusterAPIUsesInjectedProxyService(t *testing.T) {
	service := &fakeK8sClusterService{
		upgradeRecord: ports.K8sClusterRecord{
			ClusterID: "k8sclu-a",
			TenantID:  "tenant-a",
			Name:      "vc-a",
			Version:   "v1.31.0",
			State:     ports.K8sClusterStateRunning,
			UpdatedAt: time.Unix(910, 0).Unix(),
		},
		nodePoolRecord: ports.K8sClusterNodePoolRecord{
			NodePoolID:   "np-a",
			TenantID:     "tenant-a",
			ClusterID:    "k8sclu-a",
			Name:         "system-pool",
			NodeCount:    2,
			InstanceType: "general.large",
			State:        ports.K8sClusterNodePoolStateRunning,
			UpdatedAt:    time.Unix(920, 0).Unix(),
		},
		proxyRecord: ports.K8sClusterProxyRecord{
			ClusterID:  "k8sclu-a",
			TenantID:   "tenant-a",
			Method:     "GET",
			Path:       "/api/v1/namespaces/default/pods",
			Query:      map[string]string{"limit": "20"},
			StatusCode: 206,
			Headers:    map[string]string{"x-upstream": "vcluster-a"},
			Body:       map[string]any{"kind": "PodList"},
			ProxiedAt:  time.Unix(900, 0).Unix(),
		},
	}
	api := newK8sClusterAPIWithService(service)

	got, err := api.service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "tenant-a",
		ClusterID:      "k8sclu-a",
		IdempotencyKey: "idem-proxy-a",
		Method:         "GET",
		Path:           "/api/v1/namespaces/default/pods",
		Query:          map[string]string{"limit": "20"},
	})
	if err != nil {
		t.Fatalf("Proxy error = %v", err)
	}
	if !service.proxyCalled {
		t.Fatalf("injected service Proxy was not called")
	}
	if got.StatusCode != 206 || got.Headers["x-upstream"] != "vcluster-a" || got.Body["kind"] != "PodList" {
		t.Fatalf("proxy response = %+v, want injected service response", got)
	}

	upgraded, err := api.service.UpgradeCluster(context.Background(), ports.K8sClusterUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      "k8sclu-a",
		IdempotencyKey: "idem-upgrade-a",
		Version:        "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeCluster error = %v", err)
	}
	if !service.upgradeCalled {
		t.Fatalf("injected service UpgradeCluster was not called")
	}
	if upgraded.Version != "v1.31.0" {
		t.Fatalf("upgraded version = %s, want v1.31.0", upgraded.Version)
	}

	nodePool, err := api.service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      "k8sclu-a",
		IdempotencyKey: "idem-node-pool-a",
		Name:           "system-pool",
		NodeCount:      2,
		InstanceType:   "general.large",
	})
	if err != nil {
		t.Fatalf("CreateNodePool error = %v", err)
	}
	if !service.nodePoolCalled {
		t.Fatalf("injected service CreateNodePool was not called")
	}
	if nodePool.NodePoolID != "np-a" || nodePool.NodeCount != 2 {
		t.Fatalf("node pool = %+v, want injected node pool", nodePool)
	}
}

func TestK8sClusterResponseMarksRealProviderWhenVClusterWasCreated(t *testing.T) {
	resp := k8sClusterFromRecord(ports.K8sClusterRecord{
		ClusterID:    "k8sclu-real",
		TenantID:     "tenant-a",
		Name:         "vc-real",
		Version:      "v1.30.0",
		State:        ports.K8sClusterStateRunning,
		Reason:       "vCluster Helm release applied",
		Provider:     "vcluster",
		RealProvider: true,
		ProviderRefs: []string{"vcluster/HelmRelease/k8sclu-real"},
		CreatedAt:    time.Unix(900, 0).Unix(),
		UpdatedAt:    time.Unix(901, 0).Unix(),
	})
	if resp.DevProfile.Mode != "real" || !resp.DevProfile.RealProvider || resp.DevProfile.Provider != "vcluster-provider" {
		t.Fatalf("dev_profile = %+v, want vCluster provider", resp.DevProfile)
	}
}

type fakeK8sClusterService struct {
	proxyCalled    bool
	upgradeCalled  bool
	nodePoolCalled bool
	proxyRecord    ports.K8sClusterProxyRecord
	upgradeRecord  ports.K8sClusterRecord
	nodePoolRecord ports.K8sClusterNodePoolRecord
}

func (s *fakeK8sClusterService) CreateCluster(context.Context, ports.K8sClusterCreateRequest) (ports.K8sClusterRecord, error) {
	return ports.K8sClusterRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) GetCluster(context.Context, ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	return ports.K8sClusterRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) ListClusters(context.Context, ports.K8sClusterListRequest) ([]ports.K8sClusterRecord, error) {
	return nil, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) DeleteCluster(context.Context, ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	return ports.K8sClusterRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) UpgradeCluster(_ context.Context, req ports.K8sClusterUpgradeRequest) (ports.K8sClusterRecord, error) {
	s.upgradeCalled = true
	s.upgradeRecord.TenantID = req.TenantID
	s.upgradeRecord.ClusterID = req.ClusterID
	s.upgradeRecord.Version = req.Version
	return s.upgradeRecord, nil
}

func (s *fakeK8sClusterService) CreateNodePool(_ context.Context, req ports.K8sClusterNodePoolCreateRequest) (ports.K8sClusterNodePoolRecord, error) {
	s.nodePoolCalled = true
	s.nodePoolRecord.TenantID = req.TenantID
	s.nodePoolRecord.ClusterID = req.ClusterID
	s.nodePoolRecord.Name = req.Name
	s.nodePoolRecord.NodeCount = req.NodeCount
	s.nodePoolRecord.InstanceType = req.InstanceType
	return s.nodePoolRecord, nil
}

func (s *fakeK8sClusterService) GetNodePool(context.Context, ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	return ports.K8sClusterNodePoolRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) ListNodePools(context.Context, ports.K8sClusterNodePoolListRequest) ([]ports.K8sClusterNodePoolRecord, error) {
	return nil, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) UpdateNodePool(context.Context, ports.K8sClusterNodePoolUpdateRequest) (ports.K8sClusterNodePoolRecord, error) {
	return ports.K8sClusterNodePoolRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) DeleteNodePool(context.Context, ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	return ports.K8sClusterNodePoolRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) GetKubeconfig(context.Context, ports.K8sClusterKubeconfigRequest) (ports.K8sClusterKubeconfigRecord, error) {
	return ports.K8sClusterKubeconfigRecord{}, ports.ErrUnsupported
}

func (s *fakeK8sClusterService) Proxy(_ context.Context, req ports.K8sClusterProxyRequest) (ports.K8sClusterProxyRecord, error) {
	s.proxyCalled = true
	s.proxyRecord.TenantID = req.TenantID
	s.proxyRecord.ClusterID = req.ClusterID
	return s.proxyRecord, nil
}

var _ ports.K8sClusterService = (*fakeK8sClusterService)(nil)
