package router

import (
	"context"
	"testing"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestNetworkAPIDevProfileVPCSubnetSecurityGroupAndLB(t *testing.T) {
	api := newNetworkAPI()
	vpc, err := api.service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vpc-a",
		Name:           "tenant-a-vpc",
		CIDR:           "10.30.0.0/16",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	if got := networkVPCFromRecord(vpc); got.ID == "" || got.State != "available" || got.TenantID != "tenant-a" {
		t.Fatalf("vpc response = %+v, want available tenant-a VPC", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-network-service")
	}
	subnet, err := api.service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-subnet-a",
		VPCID:          vpc.VPCID,
		Name:           "tenant-a-subnet",
		CIDR:           "10.30.1.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	if got := networkSubnetFromRecord(subnet); got.ID == "" || got.VPCID != vpc.VPCID || got.State != "available" {
		t.Fatalf("subnet response = %+v, want subnet under VPC", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-network-service")
	}
	sg, err := api.service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-sg-a",
		Name:           "web-sg",
		Rules: []ports.NetworkSecurityGroupRule{
			{Direction: "ingress", Protocol: "tcp", PortRange: "443", CIDR: "0.0.0.0/0", Action: "allow"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup error = %v", err)
	}
	if got := networkSecurityGroupFromRecord(sg); got.ID == "" || len(got.Rules) != 1 {
		t.Fatalf("security group response = %+v, want rule", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-network-service")
	}
	lb, err := api.service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-lb-a",
		Name:           "web-lb",
		VPCID:          vpc.VPCID,
		Listeners: []ports.NetworkLoadBalancerListener{
			{Protocol: "http", Port: 80, TargetPort: 8080},
		},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer error = %v", err)
	}
	if got := networkLoadBalancerFromRecord(lb); got.ID == "" || got.VPCID != vpc.VPCID || got.VIP == "" {
		t.Fatalf("load balancer response = %+v, want local lb", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-network-service")
	}
}

func TestNetworkAPIServiceKeepsTenantIsolation(t *testing.T) {
	api := newNetworkAPI()
	vpc, err := api.service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-vpc-b",
		Name:           "tenant-a-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	if _, err := api.service.GetVPC(context.Background(), ports.NetworkResourceGetRequest{
		TenantID:   "tenant-b",
		ResourceID: vpc.VPCID,
	}); err == nil {
		t.Fatalf("GetVPC from another tenant succeeded, want isolation error")
	}
}

func TestNetworkAPIDevProfileRoute(t *testing.T) {
	api := newNetworkAPI()
	vpc, err := api.service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-route-vpc-a",
		Name:           "route-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	route, err := api.service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "api-route-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "11111111-1111-1111-1111-111111111111",
		Description:     "default route",
	})
	if err != nil {
		t.Fatalf("CreateRoute error = %v", err)
	}
	got := networkRouteFromRecord(route)
	if got.ID == "" || got.VPCID != vpc.VPCID || got.DestinationCIDR != "0.0.0.0/0" || got.NextHopType != "gateway" {
		t.Fatalf("route response = %+v, want route schema fields", got)
	}
	requireLocalCoreDevProfile(t, got.DevProfile, "local-network-service")
}

func TestNetworkAPIOverviewRuleBindingAndIPAllocationResponses(t *testing.T) {
	api := newNetworkAPI()
	vpc, err := api.service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "api-overview-vpc", Name: "vpc"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := api.service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{TenantID: "tenant-a", IdempotencyKey: "api-overview-subnet", VPCID: vpc.VPCID, Name: "subnet"})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	sg, err := api.service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{TenantID: "tenant-a", IdempotencyKey: "api-overview-sg", Name: "sg"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup error = %v", err)
	}
	rule, err := api.service.CreateSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		IdempotencyKey:  "api-rule-a",
		Priority:        100,
		Direction:       "ingress",
		Protocol:        "tcp",
		PortRange:       "443",
		CIDR:            "0.0.0.0/0",
		Action:          "allow",
		Description:     "https",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroupRule error = %v", err)
	}
	ruleResponse := networkSecurityGroupRuleFromRecord(rule)
	if ruleResponse.ID != rule.RuleID || ruleResponse.SecurityGroupID != sg.SecurityGroupID || ruleResponse.Priority != 100 {
		t.Fatalf("rule response = %+v, want priority and parent identity", ruleResponse)
	}
	binding, err := api.service.CreateSecurityGroupBinding(context.Background(), ports.NetworkSecurityGroupBindingCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		IdempotencyKey:  "api-binding-a",
		TargetType:      "instance",
		TargetID:        "inst-a",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroupBinding error = %v", err)
	}
	bindingResponse := networkSecurityGroupBindingFromRecord(binding)
	if bindingResponse.ID != binding.BindingID || bindingResponse.TargetType != "instance" || bindingResponse.SecurityGroupID != sg.SecurityGroupID {
		t.Fatalf("binding response = %+v, want binding identity", bindingResponse)
	}
	overview, err := api.service.GetOverview(context.Background(), ports.NetworkOverviewRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("GetOverview error = %v", err)
	}
	overviewResponse := networkOverviewFromRecord(overview)
	if len(overviewResponse.Resources) != 5 || len(overviewResponse.Capabilities) != 8 || len(overviewResponse.CreateOrder) != 4 {
		t.Fatalf("overview response = %+v, want first-screen metadata", overviewResponse)
	}
	ipAllocations, err := api.service.ListSubnetIPAllocations(context.Background(), ports.NetworkSubnetIPAllocationListRequest{TenantID: "tenant-a", SubnetID: subnet.SubnetID})
	if err != nil {
		t.Fatalf("ListSubnetIPAllocations error = %v", err)
	}
	if len(ipAllocations) != 0 {
		t.Fatalf("ip allocations = %#v, want empty local list", ipAllocations)
	}
}

func TestNetworkAPIRouteResponseMarksRealProvider(t *testing.T) {
	got := networkRouteFromRecord(ports.NetworkRouteRecord{
		RouteID:         "rt-real",
		VPCID:           "vpc-real",
		DestinationCIDR: "10.250.0.0/16",
		NextHopType:     "gateway",
		NextHopID:       "10.244.180.1",
		Provider:        "kubeovn",
		RealProvider:    true,
	})
	if got.DevProfile.Mode != "real" || !got.DevProfile.RealProvider || got.DevProfile.Provider != "kubeovn" {
		t.Fatalf("route dev_profile = %+v, want Kube-OVN real provider", got.DevProfile)
	}
}

func TestNetworkAPIUsesInjectedService(t *testing.T) {
	service := newFakeNetworkService()
	api := newNetworkAPIWithService(service)

	_, err := api.service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-injected-vpc",
		Name:           "injected",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	if service.createVPCCalls != 1 {
		t.Fatalf("injected service createVPCCalls = %d, want 1", service.createVPCCalls)
	}
}

type fakeNetworkService struct {
	ports.NetworkService
	createVPCCalls int
}

func newFakeNetworkService() *fakeNetworkService {
	return &fakeNetworkService{NetworkService: runtimeadapter.NewLocalNetworkService()}
}

func (s *fakeNetworkService) CreateVPC(ctx context.Context, request ports.NetworkVPCCreateRequest) (ports.NetworkVPCRecord, error) {
	s.createVPCCalls++
	return s.NetworkService.CreateVPC(ctx, request)
}
