package ports

import (
	"context"
	"time"
)

type NetworkResourceState string

const (
	NetworkResourcePending   NetworkResourceState = "pending"
	NetworkResourceAvailable NetworkResourceState = "available"
	NetworkResourceFailed    NetworkResourceState = "failed"
	NetworkResourceDeleting  NetworkResourceState = "deleting"
	NetworkResourceDeleted   NetworkResourceState = "deleted"
)

type NetworkVPCRecord struct {
	TenantID  string
	VPCID     string
	Name      string
	CIDR      string
	State     NetworkResourceState
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type NetworkSubnetRecord struct {
	TenantID  string
	SubnetID  string
	VPCID     string
	Name      string
	CIDR      string
	Gateway   string
	State     NetworkResourceState
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type NetworkSecurityGroupRule struct {
	Direction string
	Protocol  string
	PortRange string
	CIDR      string
	Action    string
}

type NetworkSecurityGroupRecord struct {
	TenantID        string
	SecurityGroupID string
	Name            string
	Description     string
	Rules           []NetworkSecurityGroupRule
	State           NetworkResourceState
	Reason          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type NetworkLoadBalancerListener struct {
	Protocol   string
	Port       int32
	TargetPort int32
}

type NetworkLoadBalancerRecord struct {
	TenantID       string
	LoadBalancerID string
	Name           string
	VPCID          string
	SubnetID       string
	Scheme         string
	VIP            string
	Listeners      []NetworkLoadBalancerListener
	State          NetworkResourceState
	Reason         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NetworkRouteRecord struct {
	TenantID        string
	RouteID         string
	VPCID           string
	DestinationCIDR string
	NextHopType     string
	NextHopID       string
	Description     string
	State           NetworkResourceState
	Provider        string
	RealProvider    bool
	CreatedAt       time.Time
}

type NetworkVPCCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	CIDR           string
}

type NetworkSubnetCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	VPCID          string
	Name           string
	CIDR           string
	Gateway        string
}

type NetworkSecurityGroupCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Description    string
	Rules          []NetworkSecurityGroupRule
}

type NetworkSecurityGroupUpdateRequest struct {
	TenantID          string
	ResourceID        string
	IdempotencyKey    string
	Description       string
	UpdateDescription bool
	Rules             []NetworkSecurityGroupRule
}

type NetworkLoadBalancerCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	VPCID          string
	SubnetID       string
	Scheme         string
	Listeners      []NetworkLoadBalancerListener
}

type NetworkRouteCreateRequest struct {
	TenantID        string
	IdempotencyKey  string
	VPCID           string
	DestinationCIDR string
	NextHopType     string
	NextHopID       string
	Description     string
}

type NetworkResourceGetRequest struct {
	TenantID   string
	ResourceID string
}

type NetworkResourceListRequest struct {
	TenantID string
	VPCID    string
	Limit    int
	Cursor   string
}

type NetworkRouteListRequest struct {
	TenantID string
	VPCID    string
	Limit    int
	Cursor   string
}

type NetworkService interface {
	CreateVPC(ctx context.Context, request NetworkVPCCreateRequest) (NetworkVPCRecord, error)
	ListVPCs(ctx context.Context, request NetworkResourceListRequest) ([]NetworkVPCRecord, error)
	GetVPC(ctx context.Context, request NetworkResourceGetRequest) (NetworkVPCRecord, error)
	DeleteVPC(ctx context.Context, request NetworkResourceGetRequest) (NetworkVPCRecord, error)

	CreateSubnet(ctx context.Context, request NetworkSubnetCreateRequest) (NetworkSubnetRecord, error)
	ListSubnets(ctx context.Context, request NetworkResourceListRequest) ([]NetworkSubnetRecord, error)
	GetSubnet(ctx context.Context, request NetworkResourceGetRequest) (NetworkSubnetRecord, error)
	DeleteSubnet(ctx context.Context, request NetworkResourceGetRequest) (NetworkSubnetRecord, error)

	CreateSecurityGroup(ctx context.Context, request NetworkSecurityGroupCreateRequest) (NetworkSecurityGroupRecord, error)
	ListSecurityGroups(ctx context.Context, request NetworkResourceListRequest) ([]NetworkSecurityGroupRecord, error)
	GetSecurityGroup(ctx context.Context, request NetworkResourceGetRequest) (NetworkSecurityGroupRecord, error)
	UpdateSecurityGroup(ctx context.Context, request NetworkSecurityGroupUpdateRequest) (NetworkSecurityGroupRecord, error)
	DeleteSecurityGroup(ctx context.Context, request NetworkResourceGetRequest) (NetworkSecurityGroupRecord, error)

	CreateLoadBalancer(ctx context.Context, request NetworkLoadBalancerCreateRequest) (NetworkLoadBalancerRecord, error)
	ListLoadBalancers(ctx context.Context, request NetworkResourceListRequest) ([]NetworkLoadBalancerRecord, error)
	GetLoadBalancer(ctx context.Context, request NetworkResourceGetRequest) (NetworkLoadBalancerRecord, error)
	DeleteLoadBalancer(ctx context.Context, request NetworkResourceGetRequest) (NetworkLoadBalancerRecord, error)

	CreateRoute(ctx context.Context, request NetworkRouteCreateRequest) (NetworkRouteRecord, error)
	ListRoutes(ctx context.Context, request NetworkRouteListRequest) ([]NetworkRouteRecord, error)
	GetRoute(ctx context.Context, request NetworkResourceGetRequest) (NetworkRouteRecord, error)
	DeleteRoute(ctx context.Context, request NetworkResourceGetRequest) (NetworkRouteRecord, error)
}

type NetworkResourceStore interface {
	UpsertVPC(ctx context.Context, record NetworkVPCRecord) error
	UpsertSubnet(ctx context.Context, record NetworkSubnetRecord) error
	UpsertSecurityGroup(ctx context.Context, record NetworkSecurityGroupRecord) error
	UpsertLoadBalancer(ctx context.Context, record NetworkLoadBalancerRecord) error
	UpsertRoute(ctx context.Context, record NetworkRouteRecord) error
	UpdateResourceState(ctx context.Context, request NetworkResourceStateUpdateRequest) error

	ListVPCs(ctx context.Context, tenantID string) ([]NetworkVPCRecord, error)
	GetVPC(ctx context.Context, tenantID string, vpcID string) (NetworkVPCRecord, error)
	ListSubnets(ctx context.Context, tenantID string) ([]NetworkSubnetRecord, error)
	GetSubnet(ctx context.Context, tenantID string, subnetID string) (NetworkSubnetRecord, error)
	ListSecurityGroups(ctx context.Context, tenantID string) ([]NetworkSecurityGroupRecord, error)
	GetSecurityGroup(ctx context.Context, tenantID string, securityGroupID string) (NetworkSecurityGroupRecord, error)
	ListLoadBalancers(ctx context.Context, tenantID string) ([]NetworkLoadBalancerRecord, error)
	GetLoadBalancer(ctx context.Context, tenantID string, loadBalancerID string) (NetworkLoadBalancerRecord, error)
	ListRoutes(ctx context.Context, request NetworkRouteListRequest) ([]NetworkRouteRecord, error)
	GetRoute(ctx context.Context, tenantID string, routeID string) (NetworkRouteRecord, error)
	CountDeleteDependencies(ctx context.Context, request NetworkResourceGetRequest, resourceKind string) ([]string, error)
}

type NetworkProviderRenderer interface {
	RenderVPC(ctx context.Context, record NetworkVPCRecord) ([]WorkloadManifest, error)
	RenderSubnet(ctx context.Context, record NetworkSubnetRecord) ([]WorkloadManifest, error)
	RenderSecurityGroup(ctx context.Context, record NetworkSecurityGroupRecord) ([]WorkloadManifest, error)
	RenderLoadBalancer(ctx context.Context, record NetworkLoadBalancerRecord) ([]WorkloadManifest, error)
	RenderRoute(ctx context.Context, record NetworkRouteRecord) ([]WorkloadManifest, error)
}

type NetworkProviderOperation string

const (
	NetworkProviderOperationCreate NetworkProviderOperation = "create"
	NetworkProviderOperationDelete NetworkProviderOperation = "delete"
)

type NetworkProviderDryRunRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	Operation       NetworkProviderOperation
	Manifests       []WorkloadManifest
	PermissionProof string
	RequestedAt     time.Time
}

type NetworkProviderDryRunResult struct {
	Accepted      bool
	Provider      string
	ManifestCount int
	ResourceRefs  []string
	Reason        string
	Warnings      []string
	CheckedAt     time.Time
}

type NetworkProviderApplyRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	Operation       NetworkProviderOperation
	Manifests       []WorkloadManifest
	PermissionProof string
	DryRunResult    NetworkProviderDryRunResult
	RequestedAt     time.Time
}

type NetworkProviderApplyResult struct {
	Applied       bool
	Provider      string
	ManifestCount int
	Operation     NetworkProviderOperation
	ResourceRefs  []string
	Reason        string
	Warnings      []string
	AppliedAt     time.Time
}

type NetworkProviderStatusRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	ApplyResult     NetworkProviderApplyResult
	PermissionProof string
	RequestedAt     time.Time
}

type NetworkProviderStatusResult struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	Provider     string
	ResourceRefs []string
	State        NetworkResourceState
	Reason       string
	ObservedAt   time.Time
}

type NetworkResourceStateUpdateRequest struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	State        NetworkResourceState
	Reason       string
	UpdatedAt    time.Time
}

type NetworkReconcileRequest struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	ApplyResult  NetworkProviderApplyResult
	Observation  NetworkProviderStatusResult
	RequestedAt  time.Time
}

type NetworkReconcileResult struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	State        NetworkResourceState
	Reason       string
	Persisted    bool
	ReconciledAt time.Time
}

type NetworkProviderDryRun interface {
	DryRun(ctx context.Context, request NetworkProviderDryRunRequest) (NetworkProviderDryRunResult, error)
}

type NetworkProviderApply interface {
	Apply(ctx context.Context, request NetworkProviderApplyRequest) (NetworkProviderApplyResult, error)
}

type NetworkProviderStatusReader interface {
	Observe(ctx context.Context, request NetworkProviderStatusRequest) (NetworkProviderStatusResult, error)
}

type NetworkStatusReconciler interface {
	Reconcile(ctx context.Context, request NetworkReconcileRequest) (NetworkReconcileResult, error)
}
