package ports

import (
	"context"
	"time"
)

type K8sClusterState string

const (
	K8sClusterStateProvisioning K8sClusterState = "provisioning"
	K8sClusterStateRunning      K8sClusterState = "running"
	K8sClusterStateDeleting     K8sClusterState = "deleting"
)

type K8sClusterCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Version        string
}

type K8sClusterGetRequest struct {
	TenantID  string
	ClusterID string
}

type K8sClusterUpgradeRequest struct {
	TenantID       string
	ClusterID      string
	IdempotencyKey string
	Version        string
}

type K8sClusterNodePoolCreateRequest struct {
	TenantID       string
	ClusterID      string
	IdempotencyKey string
	Name           string
	NodeCount      int
	InstanceType   string
	GPU            K8sClusterNodePoolGPU
}

type K8sClusterNodePoolUpdateRequest struct {
	TenantID       string
	ClusterID      string
	NodePoolID     string
	IdempotencyKey string
	NodeCount      int
	InstanceType   string
	GPU            K8sClusterNodePoolGPU
}

type K8sClusterNodePoolGetRequest struct {
	TenantID   string
	ClusterID  string
	NodePoolID string
}

type K8sClusterNodePoolListRequest struct {
	TenantID  string
	ClusterID string
}

type K8sClusterListRequest struct {
	TenantID string
}

type K8sClusterKubeconfigRequest struct {
	TenantID  string
	ClusterID string
}

type K8sClusterProxyRequest struct {
	TenantID       string
	ClusterID      string
	IdempotencyKey string
	Method         string
	Path           string
	Query          map[string]string
	Body           map[string]any
}

type K8sClusterRecord struct {
	ClusterID    string
	TenantID     string
	Name         string
	Version      string
	State        K8sClusterState
	Reason       string
	Provider     string
	RealProvider bool
	ProviderRefs []string
	CreatedAt    int64
	UpdatedAt    int64
}

type K8sClusterNodePoolState string

const (
	K8sClusterNodePoolStateRunning  K8sClusterNodePoolState = "running"
	K8sClusterNodePoolStateDeleting K8sClusterNodePoolState = "deleting"
)

type K8sClusterNodePoolGPU struct {
	Vendor       string
	Model        string
	Count        int
	ResourceName string
}

type K8sClusterNodePoolRecord struct {
	NodePoolID   string
	TenantID     string
	ClusterID    string
	Name         string
	NodeCount    int
	InstanceType string
	GPU          K8sClusterNodePoolGPU
	State        K8sClusterNodePoolState
	Reason       string
	Provider     string
	RealProvider bool
	ProviderRefs []string
	CreatedAt    int64
	UpdatedAt    int64
}

type K8sClusterKubeconfigRecord struct {
	ClusterID  string
	TenantID   string
	Server     string
	Namespace  string
	CAData     string
	Token      string
	Kubeconfig string
	ExpiresAt  int64
	CreatedAt  int64
}

type K8sClusterProxyRecord struct {
	ClusterID  string
	TenantID   string
	Method     string
	Path       string
	Query      map[string]string
	StatusCode int
	Headers    map[string]string
	Body       map[string]any
	ProxiedAt  int64
}

type K8sClusterProxyTarget struct {
	TenantID    string
	ClusterID   string
	Server      string
	BearerToken string
}

type K8sClusterProxyTargetResolver interface {
	ResolveK8sClusterProxyTarget(ctx context.Context, req K8sClusterGetRequest) (K8sClusterProxyTarget, error)
}

type K8sClusterProxyTargetStore interface {
	K8sClusterProxyTargetResolver
	UpsertK8sClusterProxyTarget(ctx context.Context, target K8sClusterProxyTarget) error
	DeleteK8sClusterProxyTarget(ctx context.Context, req K8sClusterGetRequest) error
}

type K8sClusterProviderApplyRequest struct {
	TenantID  string
	ClusterID string
	Name      string
	Version   string
}

type K8sClusterProviderApplyResult struct {
	Applied      bool
	Provider     string
	ResourceRefs []string
	ProxyTarget  K8sClusterProxyTarget
	Reason       string
	AppliedAt    time.Time
}

type K8sClusterProviderUpgradeRequest struct {
	TenantID       string
	ClusterID      string
	Name           string
	CurrentVersion string
	TargetVersion  string
}

type K8sClusterProviderUpgradeResult struct {
	Applied      bool
	Provider     string
	ResourceRefs []string
	Reason       string
	AppliedAt    time.Time
}

type K8sClusterNodePoolProviderRequest struct {
	Operation    string
	TenantID     string
	ClusterID    string
	ClusterName  string
	NodePoolID   string
	Name         string
	NodeCount    int
	InstanceType string
	GPU          K8sClusterNodePoolGPU
}

type K8sClusterNodePoolProviderResult struct {
	Applied      bool
	Provider     string
	ResourceRefs []string
	Reason       string
	AppliedAt    time.Time
}

type K8sClusterKubeconfigProviderRequest struct {
	TenantID  string
	ClusterID string
	Name      string
	Version   string
	Server    string
}

type K8sClusterProviderApply interface {
	ApplyK8sCluster(ctx context.Context, req K8sClusterProviderApplyRequest) (K8sClusterProviderApplyResult, error)
}

type K8sClusterProviderUpgrade interface {
	UpgradeK8sCluster(ctx context.Context, req K8sClusterProviderUpgradeRequest) (K8sClusterProviderUpgradeResult, error)
}

type K8sClusterNodePoolProvider interface {
	ApplyK8sClusterNodePool(ctx context.Context, req K8sClusterNodePoolProviderRequest) (K8sClusterNodePoolProviderResult, error)
	DeleteK8sClusterNodePool(ctx context.Context, req K8sClusterNodePoolProviderRequest) (K8sClusterNodePoolProviderResult, error)
}

type K8sClusterKubeconfigProvider interface {
	GetK8sClusterKubeconfig(ctx context.Context, req K8sClusterKubeconfigProviderRequest) (K8sClusterKubeconfigRecord, error)
}

type K8sClusterService interface {
	CreateCluster(ctx context.Context, req K8sClusterCreateRequest) (K8sClusterRecord, error)
	GetCluster(ctx context.Context, req K8sClusterGetRequest) (K8sClusterRecord, error)
	ListClusters(ctx context.Context, req K8sClusterListRequest) ([]K8sClusterRecord, error)
	DeleteCluster(ctx context.Context, req K8sClusterGetRequest) (K8sClusterRecord, error)
	UpgradeCluster(ctx context.Context, req K8sClusterUpgradeRequest) (K8sClusterRecord, error)
	CreateNodePool(ctx context.Context, req K8sClusterNodePoolCreateRequest) (K8sClusterNodePoolRecord, error)
	GetNodePool(ctx context.Context, req K8sClusterNodePoolGetRequest) (K8sClusterNodePoolRecord, error)
	ListNodePools(ctx context.Context, req K8sClusterNodePoolListRequest) ([]K8sClusterNodePoolRecord, error)
	UpdateNodePool(ctx context.Context, req K8sClusterNodePoolUpdateRequest) (K8sClusterNodePoolRecord, error)
	DeleteNodePool(ctx context.Context, req K8sClusterNodePoolGetRequest) (K8sClusterNodePoolRecord, error)
	GetKubeconfig(ctx context.Context, req K8sClusterKubeconfigRequest) (K8sClusterKubeconfigRecord, error)
	Proxy(ctx context.Context, req K8sClusterProxyRequest) (K8sClusterProxyRecord, error)
}
