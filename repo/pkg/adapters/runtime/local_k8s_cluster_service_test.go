package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalK8sClusterServiceAppliesVClusterProviderAndRegistersProxyTarget(t *testing.T) {
	provider := &fakeK8sClusterProviderApply{
		result: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-provider"},
			ProxyTarget: ports.K8sClusterProxyTarget{
				Server:      "https://k8sclu-provider.ani-tenant-tenant-a.svc:443",
				BearerToken: "tenant-token",
			},
			Reason:    "vCluster Helm release applied",
			AppliedAt: time.Unix(900, 0),
		},
	}
	targets := NewLocalK8sClusterProxyTargetStore()
	service := NewLocalK8sClusterService(
		WithK8sClusterProviderApply(provider),
		WithK8sClusterProxyTargetStore(targets),
	)

	record, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}
	if provider.last.ClusterID != record.ClusterID || provider.last.TenantID != "tenant-a" || provider.last.Name != "vc-a" {
		t.Fatalf("provider request = %+v, want created cluster identity", provider.last)
	}
	if !record.RealProvider || record.Provider != "vcluster" || len(record.ProviderRefs) != 1 {
		t.Fatalf("record provider evidence = %+v, want vcluster real provider refs", record)
	}
	if record.State != ports.K8sClusterStateRunning || record.Reason != "vCluster Helm release applied" {
		t.Fatalf("record state/reason = %s/%q", record.State, record.Reason)
	}

	target, err := targets.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  "tenant-a",
		ClusterID: record.ClusterID,
	})
	if err != nil {
		t.Fatalf("ResolveK8sClusterProxyTarget() error = %v", err)
	}
	if target.Server != "https://k8sclu-provider.ani-tenant-tenant-a.svc:443" || target.BearerToken != "tenant-token" {
		t.Fatalf("proxy target = %+v, want provider target", target)
	}
}

func TestLocalK8sClusterServiceGetsProviderKubeconfigForRealVCluster(t *testing.T) {
	provider := &fakeK8sClusterProvider{
		applyResult: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-provider"},
			Reason:       "vCluster Helm release applied",
			AppliedAt:    time.Unix(900, 0),
		},
		kubeconfigResult: ports.K8sClusterKubeconfigRecord{
			Server:     "https://k8sclu-provider.ani-tenant-tenant-a.svc:443",
			Namespace:  "ani-tenant-tenant-a",
			Token:      "tenant-token",
			Kubeconfig: "apiVersion: v1\nusers:\n- name: vc-a\n  user:\n    token: tenant-token\n",
			CreatedAt:  1000,
			ExpiresAt:  4600,
		},
	}
	service := NewLocalK8sClusterService(
		WithK8sClusterProviderApply(provider),
		WithK8sClusterKubeconfigProvider(provider),
	)

	record, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	kubeconfig, err := service.GetKubeconfig(context.Background(), ports.K8sClusterKubeconfigRequest{
		TenantID:  "tenant-a",
		ClusterID: record.ClusterID,
	})
	if err != nil {
		t.Fatalf("GetKubeconfig() error = %v", err)
	}
	if provider.kubeconfigLast.ClusterID != record.ClusterID || provider.kubeconfigLast.TenantID != "tenant-a" || provider.kubeconfigLast.Name != "vc-a" {
		t.Fatalf("kubeconfig provider request = %+v, want created cluster identity", provider.kubeconfigLast)
	}
	if kubeconfig.Server != "https://k8sclu-provider.ani-tenant-tenant-a.svc:443" || kubeconfig.Namespace != "ani-tenant-tenant-a" || kubeconfig.Token != "tenant-token" {
		t.Fatalf("kubeconfig metadata = %+v, want provider metadata", kubeconfig)
	}
	if kubeconfig.ClusterID != record.ClusterID || kubeconfig.TenantID != "tenant-a" {
		t.Fatalf("kubeconfig identity = %+v, want service-filled identity", kubeconfig)
	}
	if kubeconfig.Kubeconfig == "" || kubeconfig.CreatedAt != 1000 || kubeconfig.ExpiresAt != 4600 {
		t.Fatalf("kubeconfig payload/timestamps = %+v, want provider payload", kubeconfig)
	}
}

func TestLocalK8sClusterServiceUpgradesClusterThroughProvider(t *testing.T) {
	provider := &fakeK8sClusterUpgradeProvider{
		applyResult: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-upgrade"},
			Reason:       "vCluster Helm release applied",
			AppliedAt:    time.Unix(900, 0),
		},
		upgradeResult: ports.K8sClusterProviderUpgradeResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-upgrade"},
			Reason:       "vCluster Helm release upgraded",
			AppliedAt:    time.Unix(1000, 0),
		},
	}
	service := NewLocalK8sClusterService(
		WithK8sClusterProviderApply(provider),
		WithK8sClusterProviderUpgrade(provider),
	)
	record, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-upgrade",
		Name:           "vc-upgrade",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	upgraded, err := service.UpgradeCluster(context.Background(), ports.K8sClusterUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      record.ClusterID,
		IdempotencyKey: "upgrade-vc",
		Version:        "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeCluster() error = %v", err)
	}
	upgradedAgain, err := service.UpgradeCluster(context.Background(), ports.K8sClusterUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      record.ClusterID,
		IdempotencyKey: "upgrade-vc",
		Version:        "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeCluster(idempotent) error = %v", err)
	}
	if provider.upgradeCalls != 1 {
		t.Fatalf("provider upgrade calls = %d, want 1", provider.upgradeCalls)
	}
	if provider.upgradeLast.ClusterID != record.ClusterID || provider.upgradeLast.CurrentVersion != "v1.30.0" || provider.upgradeLast.TargetVersion != "v1.31.0" {
		t.Fatalf("provider upgrade request = %+v, want current and target versions", provider.upgradeLast)
	}
	if upgraded.Version != "v1.31.0" || upgraded.State != ports.K8sClusterStateRunning || upgraded.Reason != "vCluster Helm release upgraded" {
		t.Fatalf("upgraded record = %+v, want running v1.31.0 with provider reason", upgraded)
	}
	if upgradedAgain.UpdatedAt != upgraded.UpdatedAt || upgradedAgain.Version != upgraded.Version {
		t.Fatalf("idempotent replay = %+v, want original upgraded record %+v", upgradedAgain, upgraded)
	}
}

func TestLocalK8sClusterServiceManagesNodePools(t *testing.T) {
	service := NewLocalK8sClusterService()
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-node-pool",
		Name:           "vc-node-pool",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	created, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-node-pool-a",
		Name:           "gpu-pool",
		NodeCount:      2,
		InstanceType:   "gpu.l4.xlarge",
		GPU: ports.K8sClusterNodePoolGPU{
			Vendor:       "nvidia",
			Model:        "L4",
			Count:        1,
			ResourceName: "nvidia.com/gpu",
		},
	})
	if err != nil {
		t.Fatalf("CreateNodePool() error = %v", err)
	}
	createdAgain, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-node-pool-a",
		Name:           "gpu-pool",
		NodeCount:      2,
		InstanceType:   "gpu.l4.xlarge",
	})
	if err != nil {
		t.Fatalf("CreateNodePool(idempotent) error = %v", err)
	}
	if createdAgain.NodePoolID != created.NodePoolID {
		t.Fatalf("idempotent node pool id = %s, want %s", createdAgain.NodePoolID, created.NodePoolID)
	}
	if created.ClusterID != cluster.ClusterID || created.TenantID != "tenant-a" || created.State != ports.K8sClusterNodePoolStateRunning {
		t.Fatalf("created node pool = %+v, want running tenant cluster node pool", created)
	}
	if created.NodeCount != 2 || created.InstanceType != "gpu.l4.xlarge" || created.GPU.ResourceName != "nvidia.com/gpu" {
		t.Fatalf("created node pool sizing = %+v, want requested shape", created)
	}

	updated, err := service.UpdateNodePool(context.Background(), ports.K8sClusterNodePoolUpdateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		NodePoolID:     created.NodePoolID,
		IdempotencyKey: "scale-node-pool-a",
		NodeCount:      3,
		InstanceType:   "gpu.l4.xlarge",
		GPU:            created.GPU,
	})
	if err != nil {
		t.Fatalf("UpdateNodePool() error = %v", err)
	}
	updatedAgain, err := service.UpdateNodePool(context.Background(), ports.K8sClusterNodePoolUpdateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		NodePoolID:     created.NodePoolID,
		IdempotencyKey: "scale-node-pool-a",
		NodeCount:      3,
		InstanceType:   "gpu.l4.xlarge",
		GPU:            created.GPU,
	})
	if err != nil {
		t.Fatalf("UpdateNodePool(idempotent) error = %v", err)
	}
	if updated.NodeCount != 3 || updated.State != ports.K8sClusterNodePoolStateRunning {
		t.Fatalf("updated node pool = %+v, want scaled running node pool", updated)
	}
	if updatedAgain.UpdatedAt != updated.UpdatedAt || updatedAgain.NodeCount != updated.NodeCount {
		t.Fatalf("idempotent update = %+v, want original update %+v", updatedAgain, updated)
	}

	got, err := service.GetNodePool(context.Background(), ports.K8sClusterNodePoolGetRequest{
		TenantID:   "tenant-a",
		ClusterID:  cluster.ClusterID,
		NodePoolID: created.NodePoolID,
	})
	if err != nil {
		t.Fatalf("GetNodePool() error = %v", err)
	}
	if got.NodeCount != 3 {
		t.Fatalf("got node count = %d, want updated count 3", got.NodeCount)
	}

	list, err := service.ListNodePools(context.Background(), ports.K8sClusterNodePoolListRequest{
		TenantID:  "tenant-a",
		ClusterID: cluster.ClusterID,
	})
	if err != nil {
		t.Fatalf("ListNodePools() error = %v", err)
	}
	if len(list) != 1 || list[0].NodePoolID != created.NodePoolID {
		t.Fatalf("node pool list = %+v, want created node pool", list)
	}

	deleted, err := service.DeleteNodePool(context.Background(), ports.K8sClusterNodePoolGetRequest{
		TenantID:   "tenant-a",
		ClusterID:  cluster.ClusterID,
		NodePoolID: created.NodePoolID,
	})
	if err != nil {
		t.Fatalf("DeleteNodePool() error = %v", err)
	}
	if deleted.State != ports.K8sClusterNodePoolStateDeleting {
		t.Fatalf("deleted node pool state = %s, want deleting", deleted.State)
	}
}

func TestLocalK8sClusterServiceKeepsNodePoolsLocalWithoutNodePoolProvider(t *testing.T) {
	clusterProvider := &fakeK8sClusterProviderApply{
		result: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-nodepool"},
			Reason:       "vCluster Helm release applied",
			AppliedAt:    time.Unix(900, 0),
		},
	}
	service := NewLocalK8sClusterService(WithK8sClusterProviderApply(clusterProvider))
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-nodepool-local",
		Name:           "vc-nodepool-local",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	nodePool, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-node-pool-local",
		Name:           "system-pool",
		NodeCount:      2,
		InstanceType:   "general.large",
	})
	if err != nil {
		t.Fatalf("CreateNodePool() error = %v", err)
	}
	if nodePool.RealProvider || nodePool.Provider != "local" || len(nodePool.ProviderRefs) != 0 {
		t.Fatalf("node pool provider evidence = %+v, want local profile without node pool provider", nodePool)
	}
}

func TestLocalK8sClusterServiceAppliesNodePoolsThroughProvider(t *testing.T) {
	clusterProvider := &fakeK8sClusterProviderApply{
		result: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/k8sclu-nodepool"},
			Reason:       "vCluster Helm release applied",
			AppliedAt:    time.Unix(900, 0),
		},
	}
	nodePoolProvider := &fakeK8sClusterNodePoolProvider{
		applyResult: ports.K8sClusterNodePoolProviderResult{
			Applied:      true,
			Provider:     "clusterapi",
			ResourceRefs: []string{"clusterapi/MachineDeployment/gpu-pool"},
			Reason:       "Cluster API MachineDeployment applied",
			AppliedAt:    time.Unix(1000, 0),
		},
		deleteResult: ports.K8sClusterNodePoolProviderResult{
			Applied:      true,
			Provider:     "clusterapi",
			ResourceRefs: []string{"clusterapi/MachineDeployment/gpu-pool"},
			Reason:       "Cluster API MachineDeployment delete intent applied",
			AppliedAt:    time.Unix(1100, 0),
		},
	}
	service := NewLocalK8sClusterService(
		WithK8sClusterProviderApply(clusterProvider),
		WithK8sClusterNodePoolProvider(nodePoolProvider),
	)
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-nodepool-provider",
		Name:           "vc-nodepool-provider",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	created, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-provider-node-pool",
		Name:           "gpu-pool",
		NodeCount:      2,
		InstanceType:   "gpu.l4.xlarge",
		GPU: ports.K8sClusterNodePoolGPU{
			Vendor:       "nvidia",
			Model:        "L4",
			Count:        1,
			ResourceName: "nvidia.com/gpu",
		},
	})
	if err != nil {
		t.Fatalf("CreateNodePool() error = %v", err)
	}
	createdAgain, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-provider-node-pool",
		Name:           "gpu-pool",
		NodeCount:      2,
		InstanceType:   "gpu.l4.xlarge",
	})
	if err != nil {
		t.Fatalf("CreateNodePool(idempotent) error = %v", err)
	}
	if nodePoolProvider.applyCalls != 1 {
		t.Fatalf("node pool provider apply calls = %d, want 1", nodePoolProvider.applyCalls)
	}
	if nodePoolProvider.applyLast.Operation != "create" || nodePoolProvider.applyLast.ClusterName != "vc-nodepool-provider" || nodePoolProvider.applyLast.NodePoolID != created.NodePoolID {
		t.Fatalf("node pool provider create request = %+v, want created node pool identity", nodePoolProvider.applyLast)
	}
	if !created.RealProvider || created.Provider != "clusterapi" || created.Reason != "Cluster API MachineDeployment applied" {
		t.Fatalf("created node pool provider evidence = %+v, want clusterapi real provider", created)
	}
	if createdAgain.NodePoolID != created.NodePoolID {
		t.Fatalf("idempotent create node pool = %+v, want original %+v", createdAgain, created)
	}

	updated, err := service.UpdateNodePool(context.Background(), ports.K8sClusterNodePoolUpdateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		NodePoolID:     created.NodePoolID,
		IdempotencyKey: "scale-provider-node-pool",
		NodeCount:      3,
		InstanceType:   "gpu.l4.xlarge",
		GPU:            created.GPU,
	})
	if err != nil {
		t.Fatalf("UpdateNodePool() error = %v", err)
	}
	if nodePoolProvider.applyCalls != 2 || nodePoolProvider.applyLast.Operation != "update" || nodePoolProvider.applyLast.NodeCount != 3 {
		t.Fatalf("node pool provider update = calls %d request %+v, want update to 3", nodePoolProvider.applyCalls, nodePoolProvider.applyLast)
	}
	if updated.NodeCount != 3 || updated.Provider != "clusterapi" || !updated.RealProvider {
		t.Fatalf("updated node pool = %+v, want provider-backed scaled node pool", updated)
	}

	deleted, err := service.DeleteNodePool(context.Background(), ports.K8sClusterNodePoolGetRequest{
		TenantID:   "tenant-a",
		ClusterID:  cluster.ClusterID,
		NodePoolID: created.NodePoolID,
	})
	if err != nil {
		t.Fatalf("DeleteNodePool() error = %v", err)
	}
	if nodePoolProvider.deleteCalls != 1 || nodePoolProvider.deleteLast.NodePoolID != created.NodePoolID {
		t.Fatalf("node pool provider delete = calls %d request %+v, want deleted node pool", nodePoolProvider.deleteCalls, nodePoolProvider.deleteLast)
	}
	if deleted.State != ports.K8sClusterNodePoolStateDeleting || deleted.Provider != "clusterapi" || deleted.Reason != "Cluster API MachineDeployment delete intent applied" {
		t.Fatalf("deleted node pool = %+v, want provider-backed deleting node pool", deleted)
	}
}

type fakeK8sClusterProviderApply struct {
	last   ports.K8sClusterProviderApplyRequest
	result ports.K8sClusterProviderApplyResult
	err    error
}

func (p *fakeK8sClusterProviderApply) ApplyK8sCluster(_ context.Context, request ports.K8sClusterProviderApplyRequest) (ports.K8sClusterProviderApplyResult, error) {
	p.last = request
	return p.result, p.err
}

var _ ports.K8sClusterProviderApply = (*fakeK8sClusterProviderApply)(nil)

type fakeK8sClusterProvider struct {
	applyLast          ports.K8sClusterProviderApplyRequest
	applyResult        ports.K8sClusterProviderApplyResult
	applyErr           error
	kubeconfigLast     ports.K8sClusterKubeconfigProviderRequest
	kubeconfigResult   ports.K8sClusterKubeconfigRecord
	kubeconfigProvider error
}

func (p *fakeK8sClusterProvider) ApplyK8sCluster(_ context.Context, request ports.K8sClusterProviderApplyRequest) (ports.K8sClusterProviderApplyResult, error) {
	p.applyLast = request
	return p.applyResult, p.applyErr
}

func (p *fakeK8sClusterProvider) GetK8sClusterKubeconfig(_ context.Context, request ports.K8sClusterKubeconfigProviderRequest) (ports.K8sClusterKubeconfigRecord, error) {
	p.kubeconfigLast = request
	return p.kubeconfigResult, p.kubeconfigProvider
}

var _ ports.K8sClusterProviderApply = (*fakeK8sClusterProvider)(nil)
var _ ports.K8sClusterKubeconfigProvider = (*fakeK8sClusterProvider)(nil)

type fakeK8sClusterNodePoolProvider struct {
	applyCalls   int
	applyLast    ports.K8sClusterNodePoolProviderRequest
	applyResult  ports.K8sClusterNodePoolProviderResult
	applyErr     error
	deleteCalls  int
	deleteLast   ports.K8sClusterNodePoolProviderRequest
	deleteResult ports.K8sClusterNodePoolProviderResult
	deleteErr    error
}

func (p *fakeK8sClusterNodePoolProvider) ApplyK8sClusterNodePool(_ context.Context, request ports.K8sClusterNodePoolProviderRequest) (ports.K8sClusterNodePoolProviderResult, error) {
	p.applyCalls++
	p.applyLast = request
	return p.applyResult, p.applyErr
}

func (p *fakeK8sClusterNodePoolProvider) DeleteK8sClusterNodePool(_ context.Context, request ports.K8sClusterNodePoolProviderRequest) (ports.K8sClusterNodePoolProviderResult, error) {
	p.deleteCalls++
	p.deleteLast = request
	return p.deleteResult, p.deleteErr
}

var _ ports.K8sClusterNodePoolProvider = (*fakeK8sClusterNodePoolProvider)(nil)

type fakeK8sClusterUpgradeProvider struct {
	applyLast     ports.K8sClusterProviderApplyRequest
	applyResult   ports.K8sClusterProviderApplyResult
	upgradeLast   ports.K8sClusterProviderUpgradeRequest
	upgradeResult ports.K8sClusterProviderUpgradeResult
	upgradeCalls  int
}

func (p *fakeK8sClusterUpgradeProvider) ApplyK8sCluster(_ context.Context, request ports.K8sClusterProviderApplyRequest) (ports.K8sClusterProviderApplyResult, error) {
	p.applyLast = request
	return p.applyResult, nil
}

func (p *fakeK8sClusterUpgradeProvider) UpgradeK8sCluster(_ context.Context, request ports.K8sClusterProviderUpgradeRequest) (ports.K8sClusterProviderUpgradeResult, error) {
	p.upgradeCalls++
	p.upgradeLast = request
	return p.upgradeResult, nil
}

var _ ports.K8sClusterProviderApply = (*fakeK8sClusterUpgradeProvider)(nil)
var _ ports.K8sClusterProviderUpgrade = (*fakeK8sClusterUpgradeProvider)(nil)
