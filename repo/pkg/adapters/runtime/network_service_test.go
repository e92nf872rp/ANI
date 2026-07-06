package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalNetworkServiceVPCDevProfile(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-vpc-a",
		Name:           "tenant-a-vpc",
		CIDR:           "10.20.0.0/16",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	if vpc.VPCID == "" || vpc.State != ports.NetworkResourceAvailable || vpc.CIDR != "10.20.0.0/16" {
		t.Fatalf("vpc = %+v, want available local VPC", vpc)
	}
	replay, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-vpc-a",
		Name:           "tenant-a-vpc-retry",
		CIDR:           "10.99.0.0/16",
	})
	if err != nil {
		t.Fatalf("CreateVPC replay error = %v", err)
	}
	if replay.VPCID != vpc.VPCID || replay.CIDR != vpc.CIDR {
		t.Fatalf("replay vpc = %+v, want original %+v", replay, vpc)
	}
	items, err := service.ListVPCs(context.Background(), ports.NetworkResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListVPCs error = %v", err)
	}
	if len(items) != 1 || items[0].VPCID != vpc.VPCID {
		t.Fatalf("tenant-a vpcs = %#v, want created vpc", items)
	}
	otherTenant, err := service.ListVPCs(context.Background(), ports.NetworkResourceListRequest{TenantID: "tenant-b"})
	if err != nil {
		t.Fatalf("ListVPCs(other tenant) error = %v", err)
	}
	if len(otherTenant) != 0 {
		t.Fatalf("tenant-b vpcs = %#v, want tenant isolation", otherTenant)
	}
}

func TestLocalNetworkServiceSubnetRequiresTenantVPC(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "network-vpc-b", Name: "vpc-a"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-subnet-a",
		VPCID:          vpc.VPCID,
		Name:           "subnet-a",
		CIDR:           "10.20.1.0/24",
		Gateway:        "10.20.1.1",
	})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	if subnet.SubnetID == "" || subnet.VPCID != vpc.VPCID || subnet.State != ports.NetworkResourceAvailable {
		t.Fatalf("subnet = %+v, want available subnet under vpc", subnet)
	}
	if _, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-b",
		IdempotencyKey: "network-subnet-bad",
		VPCID:          vpc.VPCID,
		Name:           "bad-subnet",
	}); err == nil {
		t.Fatalf("CreateSubnet with another tenant VPC succeeded, want error")
	}
	if _, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-subnet-bad-gateway",
		VPCID:          vpc.VPCID,
		Name:           "bad-gateway",
		CIDR:           "10.20.2.0/24",
		Gateway:        "10.20.3.1",
	}); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("CreateSubnet gateway outside CIDR error = %v, want ErrInvalid", err)
	}
}

func TestLocalNetworkServiceSecurityGroupAndLoadBalancer(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "network-vpc-c", Name: "vpc-a"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	sg, err := service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-sg-a",
		Name:           "web-sg",
		Rules: []ports.NetworkSecurityGroupRule{
			{Direction: "ingress", Protocol: "tcp", PortRange: "443", CIDR: "0.0.0.0/0", Action: "allow"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup error = %v", err)
	}
	if sg.SecurityGroupID == "" || len(sg.Rules) != 1 {
		t.Fatalf("security group = %+v, want one rule", sg)
	}
	updated, err := service.UpdateSecurityGroup(context.Background(), ports.NetworkSecurityGroupUpdateRequest{
		TenantID:          "tenant-a",
		ResourceID:        sg.SecurityGroupID,
		IdempotencyKey:    "network-sg-update-a",
		Description:       "updated description",
		UpdateDescription: true,
		Rules: []ports.NetworkSecurityGroupRule{
			{Direction: "egress", Protocol: "all", PortRange: "all", CIDR: "10.0.0.0/8", Action: "allow"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateSecurityGroup error = %v", err)
	}
	if updated.Description != "updated description" || len(updated.Rules) != 1 || updated.Rules[0].Direction != "egress" {
		t.Fatalf("updated security group = %+v, want replaced rules and description", updated)
	}
	lb, err := service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-lb-a",
		Name:           "web-lb",
		VPCID:          vpc.VPCID,
		Scheme:         "public",
		Listeners: []ports.NetworkLoadBalancerListener{
			{Protocol: "http", Port: 80, TargetPort: 8080},
		},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer error = %v", err)
	}
	if lb.LoadBalancerID == "" || lb.VIP == "" || lb.State != ports.NetworkResourceAvailable {
		t.Fatalf("load balancer = %+v, want available local lb", lb)
	}
	if _, err := service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-lb-bad-vpc",
		Name:           "bad-lb",
		VPCID:          "vpc-missing",
	}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("CreateLoadBalancer missing VPC error = %v, want ErrNotFound", err)
	}
	otherVPC, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "network-vpc-other", Name: "other-vpc"})
	if err != nil {
		t.Fatalf("CreateVPC other error = %v", err)
	}
	otherSubnet, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-subnet-other",
		VPCID:          otherVPC.VPCID,
		Name:           "other-subnet",
	})
	if err != nil {
		t.Fatalf("CreateSubnet other error = %v", err)
	}
	if _, err := service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-lb-bad-subnet",
		Name:           "bad-subnet-lb",
		VPCID:          vpc.VPCID,
		SubnetID:       otherSubnet.SubnetID,
	}); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("CreateLoadBalancer subnet mismatch error = %v, want ErrInvalid", err)
	}
	deleted, err := service.DeleteLoadBalancer(context.Background(), ports.NetworkResourceGetRequest{
		TenantID:   "tenant-a",
		ResourceID: lb.LoadBalancerID,
	})
	if err != nil {
		t.Fatalf("DeleteLoadBalancer error = %v", err)
	}
	if deleted.State != ports.NetworkResourceDeleted {
		t.Fatalf("deleted lb state = %s, want deleted", deleted.State)
	}
	list, err := service.ListLoadBalancers(context.Background(), ports.NetworkResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListLoadBalancers error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("load balancers = %#v, want deleted item hidden", list)
	}
}

func TestLocalNetworkServiceListSubnetsCanFilterByVPC(t *testing.T) {
	service := NewLocalNetworkService()
	vpcA, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "filter-vpc-a", Name: "vpc-a"})
	if err != nil {
		t.Fatalf("CreateVPC A error = %v", err)
	}
	vpcB, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "filter-vpc-b", Name: "vpc-b"})
	if err != nil {
		t.Fatalf("CreateVPC B error = %v", err)
	}
	subnetA, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{TenantID: "tenant-a", IdempotencyKey: "filter-subnet-a", VPCID: vpcA.VPCID, Name: "subnet-a"})
	if err != nil {
		t.Fatalf("CreateSubnet A error = %v", err)
	}
	if _, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{TenantID: "tenant-a", IdempotencyKey: "filter-subnet-b", VPCID: vpcB.VPCID, Name: "subnet-b"}); err != nil {
		t.Fatalf("CreateSubnet B error = %v", err)
	}

	items, err := service.ListSubnets(context.Background(), ports.NetworkResourceListRequest{TenantID: "tenant-a", VPCID: vpcA.VPCID})
	if err != nil {
		t.Fatalf("ListSubnets error = %v", err)
	}
	if len(items) != 1 || items[0].SubnetID != subnetA.SubnetID {
		t.Fatalf("filtered subnets = %+v, want only %s", items, subnetA.SubnetID)
	}
}

func TestLocalNetworkServiceDeleteDetectsDependencies(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "dep-vpc", Name: "dep-vpc"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{TenantID: "tenant-a", IdempotencyKey: "dep-subnet", VPCID: vpc.VPCID, Name: "dep-subnet"})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	if _, err := service.DeleteVPC(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: vpc.VPCID}); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("DeleteVPC error = %v, want ErrConflict while subnet exists", err)
	}
	lb, err := service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{TenantID: "tenant-a", IdempotencyKey: "dep-lb", Name: "dep-lb", VPCID: vpc.VPCID, SubnetID: subnet.SubnetID})
	if err != nil {
		t.Fatalf("CreateLoadBalancer error = %v", err)
	}
	if _, err := service.DeleteSubnet(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: subnet.SubnetID}); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("DeleteSubnet error = %v, want ErrConflict while load balancer uses subnet", err)
	}
	if _, err := service.DeleteLoadBalancer(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: lb.LoadBalancerID}); err != nil {
		t.Fatalf("DeleteLoadBalancer error = %v", err)
	}
	if _, err := service.DeleteSubnet(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: subnet.SubnetID}); err != nil {
		t.Fatalf("DeleteSubnet after dependencies removed error = %v", err)
	}
}

func TestLocalNetworkServiceRoutesDevProfileAndIdempotency(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "route-vpc-a",
		Name:           "route-vpc",
		CIDR:           "10.70.0.0/16",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}

	route, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "route-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "11111111-1111-1111-1111-111111111111",
		Description:     "default route",
	})
	if err != nil {
		t.Fatalf("CreateRoute error = %v", err)
	}
	if _, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "route-conflict",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "33333333-3333-3333-3333-333333333333",
	}); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("CreateRoute duplicate destination error = %v, want ErrConflict", err)
	}
	retry, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "route-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "10.0.0.0/8",
		NextHopType:     "nat",
		NextHopID:       "22222222-2222-2222-2222-222222222222",
	})
	if err != nil {
		t.Fatalf("CreateRoute retry error = %v", err)
	}
	if retry.RouteID != route.RouteID || retry.DestinationCIDR != route.DestinationCIDR {
		t.Fatalf("idempotent route = %+v, want original %+v", retry, route)
	}

	routes, err := service.ListRoutes(context.Background(), ports.NetworkRouteListRequest{TenantID: "tenant-a", VPCID: vpc.VPCID})
	if err != nil {
		t.Fatalf("ListRoutes error = %v", err)
	}
	if len(routes) != 1 || routes[0].RouteID != route.RouteID || routes[0].State != ports.NetworkResourceAvailable {
		t.Fatalf("routes = %+v, want one available route", routes)
	}
	got, err := service.GetRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: route.RouteID})
	if err != nil {
		t.Fatalf("GetRoute error = %v", err)
	}
	if got.RouteID != route.RouteID {
		t.Fatalf("GetRoute = %+v, want %s", got, route.RouteID)
	}
	deleted, err := service.DeleteRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: route.RouteID})
	if err != nil {
		t.Fatalf("DeleteRoute error = %v", err)
	}
	if deleted.State != ports.NetworkResourceDeleted {
		t.Fatalf("deleted route state = %s, want deleted", deleted.State)
	}
}

func TestLocalNetworkServiceRouteCanUseKubeOVNProviderPipeline(t *testing.T) {
	provider := &fakeNetworkRouteProvider{}
	service := NewLocalNetworkService(
		WithNetworkRouteProvider(
			NewKubeOVNNetworkRenderer(),
			provider,
			provider,
			provider,
			NetworkProviderExecutionConfig{
				UserID:          "ani-core-network-provider",
				PermissionProof: "rbac-scope:networks.write",
			},
		),
		WithNetworkServiceClock(func() time.Time { return time.Unix(2000, 0) }),
	)
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "route-provider-vpc",
		Name:           "route-provider-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}

	route, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "route-provider-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "10.70.0.1",
	})
	if err != nil {
		t.Fatalf("CreateRoute error = %v", err)
	}
	if route.State != ports.NetworkResourceAvailable {
		t.Fatalf("route state = %s, want provider observation available", route.State)
	}
	if !route.RealProvider || route.Provider != "kubeovn" {
		t.Fatalf("route provider = real:%v provider:%q, want kubeovn real provider", route.RealProvider, route.Provider)
	}
	if provider.dryRuns != 2 || provider.applies != 2 || provider.observes != 2 {
		t.Fatalf("provider calls dry=%d apply=%d observe=%d, want VPC + route provider calls", provider.dryRuns, provider.applies, provider.observes)
	}
	if provider.lastDryRun.ResourceKind != "route" || provider.lastDryRun.ResourceID != route.RouteID {
		t.Fatalf("dry-run identity = %#v, want route %s", provider.lastDryRun, route.RouteID)
	}
	if provider.lastDryRun.UserID != "ani-core-network-provider" || provider.lastDryRun.PermissionProof == "" {
		t.Fatalf("dry-run execution identity = %#v, want explicit provider identity", provider.lastDryRun)
	}
	if len(provider.lastDryRun.Manifests) != 1 || provider.lastDryRun.Manifests[0].Kind != "Vpc" {
		t.Fatalf("dry-run manifests = %#v, want route rendered as Vpc staticRoutes", provider.lastDryRun.Manifests)
	}
}

func TestLocalNetworkServiceVPCAndSubnetUseKubeOVNProviderPipeline(t *testing.T) {
	provider := &fakeNetworkRouteProvider{}
	service := NewLocalNetworkService(
		WithNetworkProvider(
			NewKubeOVNNetworkRenderer(),
			provider,
			provider,
			provider,
			NetworkProviderExecutionConfig{
				UserID:          "ani-core-network-provider",
				PermissionProof: "rbac-scope:networks.write",
			},
		),
		WithNetworkServiceClock(func() time.Time { return time.Unix(3000, 0) }),
	)
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "provider-vpc",
		Name:           "provider-vpc",
		CIDR:           "10.80.0.0/16",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "provider-subnet",
		VPCID:          vpc.VPCID,
		Name:           "provider-subnet",
		CIDR:           "10.80.1.0/24",
		Gateway:        "10.80.1.1",
	})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	if vpc.State != ports.NetworkResourceAvailable || subnet.State != ports.NetworkResourceAvailable {
		t.Fatalf("vpc/subnet state = %s/%s, want available", vpc.State, subnet.State)
	}
	if provider.dryRuns != 2 || provider.applies != 2 || provider.observes != 2 {
		t.Fatalf("provider calls dry=%d apply=%d observe=%d, want 2/2/2", provider.dryRuns, provider.applies, provider.observes)
	}
	if provider.lastDryRun.ResourceKind != "subnet" || provider.lastDryRun.ResourceID != subnet.SubnetID {
		t.Fatalf("last dry-run identity = %#v, want subnet %s", provider.lastDryRun, subnet.SubnetID)
	}
	if len(provider.lastDryRun.Manifests) != 1 || provider.lastDryRun.Manifests[0].Kind != "Subnet" {
		t.Fatalf("last dry-run manifests = %#v, want Subnet manifest", provider.lastDryRun.Manifests)
	}
}

type fakeNetworkRouteProvider struct {
	dryRuns    int
	applies    int
	observes   int
	lastDryRun ports.NetworkProviderDryRunRequest
}

func (p *fakeNetworkRouteProvider) DryRun(_ context.Context, request ports.NetworkProviderDryRunRequest) (ports.NetworkProviderDryRunResult, error) {
	p.dryRuns++
	p.lastDryRun = request
	return ports.NetworkProviderDryRunResult{
		Accepted:      true,
		Provider:      "kubeovn",
		ManifestCount: len(request.Manifests),
		ResourceRefs:  []string{"kubeovn/Vpc/vpc-" + request.ResourceID},
		Reason:        "accepted by fake kubeovn dry-run",
		CheckedAt:     time.Unix(2001, 0),
	}, nil
}

func (p *fakeNetworkRouteProvider) Apply(_ context.Context, request ports.NetworkProviderApplyRequest) (ports.NetworkProviderApplyResult, error) {
	p.applies++
	return ports.NetworkProviderApplyResult{
		Applied:       true,
		Provider:      "kubeovn",
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  append([]string(nil), request.DryRunResult.ResourceRefs...),
		Reason:        "applied by fake kubeovn provider",
		AppliedAt:     time.Unix(2002, 0),
	}, nil
}

func (p *fakeNetworkRouteProvider) Observe(_ context.Context, request ports.NetworkProviderStatusRequest) (ports.NetworkProviderStatusResult, error) {
	p.observes++
	return ports.NetworkProviderStatusResult{
		TenantID:     request.TenantID,
		ResourceKind: request.ResourceKind,
		ResourceID:   request.ResourceID,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: append([]string(nil), request.ApplyResult.ResourceRefs...),
		State:        ports.NetworkResourceAvailable,
		Reason:       "observed by fake kubeovn provider",
		ObservedAt:   time.Unix(2003, 0),
	}, nil
}

var _ ports.NetworkProviderDryRun = (*fakeNetworkRouteProvider)(nil)
var _ ports.NetworkProviderApply = (*fakeNetworkRouteProvider)(nil)
var _ ports.NetworkProviderStatusReader = (*fakeNetworkRouteProvider)(nil)
