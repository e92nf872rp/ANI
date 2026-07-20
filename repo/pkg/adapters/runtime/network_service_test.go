package runtime

import (
	"context"
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
}

func TestLocalNetworkServiceOverviewSummarizesNetworkResources(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "overview-vpc-a",
		Name:           "overview-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	if _, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "overview-subnet-a",
		VPCID:          vpc.VPCID,
		Name:           "overview-subnet",
	}); err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	sg, err := service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "overview-sg-a",
		Name:           "overview-sg",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup error = %v", err)
	}
	if _, err := service.CreateLoadBalancer(context.Background(), ports.NetworkLoadBalancerCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "overview-lb-a",
		Name:           "overview-lb",
		VPCID:          vpc.VPCID,
	}); err != nil {
		t.Fatalf("CreateLoadBalancer error = %v", err)
	}
	if _, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "overview-route-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "10.0.0.1",
	}); err != nil {
		t.Fatalf("CreateRoute error = %v", err)
	}
	if _, err := service.CreateSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		IdempotencyKey:  "overview-sg-rule-a",
		Priority:        100,
		Direction:       "ingress",
		Protocol:        "tcp",
		PortRange:       "443",
		CIDR:            "0.0.0.0/0",
		Action:          "allow",
	}); err != nil {
		t.Fatalf("CreateSecurityGroupRule error = %v", err)
	}

	overview, err := service.GetOverview(context.Background(), ports.NetworkOverviewRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("GetOverview error = %v", err)
	}
	if got := overview.Resources["vpc"].Total; got != 1 {
		t.Fatalf("overview vpc total = %d, want 1", got)
	}
	if got := overview.Resources["security_group"].Available; got != 1 {
		t.Fatalf("overview security_group available = %d, want 1", got)
	}
	if len(overview.Capabilities) != 8 || len(overview.CreateOrder) != 4 || len(overview.Relationships) == 0 || len(overview.DeleteRisks) == 0 {
		t.Fatalf("overview = %+v, want first-screen metadata", overview)
	}
	other, err := service.GetOverview(context.Background(), ports.NetworkOverviewRequest{TenantID: "tenant-b"})
	if err != nil {
		t.Fatalf("GetOverview(other tenant) error = %v", err)
	}
	if got := other.Resources["vpc"].Total; got != 0 {
		t.Fatalf("other tenant overview vpc total = %d, want 0", got)
	}
}

func TestLocalNetworkServiceRouteGetDeleteAndTenantIsolation(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "route-get-vpc", Name: "route-vpc"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	route, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "route-get-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "10.10.0.0/16",
		NextHopType:     "gateway",
		NextHopID:       "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateRoute error = %v", err)
	}
	if _, err := service.GetRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-b", ResourceID: route.RouteID}); err == nil {
		t.Fatalf("GetRoute from another tenant succeeded, want not found")
	}
	got, err := service.GetRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: route.RouteID})
	if err != nil {
		t.Fatalf("GetRoute error = %v", err)
	}
	if got.RouteID != route.RouteID {
		t.Fatalf("GetRoute = %+v, want route %s", got, route.RouteID)
	}
	deleted, err := service.DeleteRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: route.RouteID})
	if err != nil {
		t.Fatalf("DeleteRoute error = %v", err)
	}
	if deleted.State != ports.NetworkResourceDeleted {
		t.Fatalf("deleted route state = %s, want deleted", deleted.State)
	}
	if _, err := service.GetRoute(context.Background(), ports.NetworkResourceGetRequest{TenantID: "tenant-a", ResourceID: route.RouteID}); err == nil {
		t.Fatalf("GetRoute after delete succeeded, want not found")
	}
	routes, err := service.ListRoutes(context.Background(), ports.NetworkRouteListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListRoutes error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes after delete = %#v, want hidden deleted route", routes)
	}
}

func TestLocalNetworkServiceSubnetIPAllocationsRequireTenantSubnet(t *testing.T) {
	service := NewLocalNetworkService()
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{TenantID: "tenant-a", IdempotencyKey: "ipalloc-vpc", Name: "ipalloc-vpc"})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := service.CreateSubnet(context.Background(), ports.NetworkSubnetCreateRequest{TenantID: "tenant-a", IdempotencyKey: "ipalloc-subnet", VPCID: vpc.VPCID, Name: "ipalloc-subnet"})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	items, err := service.ListSubnetIPAllocations(context.Background(), ports.NetworkSubnetIPAllocationListRequest{TenantID: "tenant-a", SubnetID: subnet.SubnetID})
	if err != nil {
		t.Fatalf("ListSubnetIPAllocations error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ip allocations = %#v, want empty local allocation list", items)
	}
	if _, err := service.ListSubnetIPAllocations(context.Background(), ports.NetworkSubnetIPAllocationListRequest{TenantID: "tenant-b", SubnetID: subnet.SubnetID}); err == nil {
		t.Fatalf("ListSubnetIPAllocations from another tenant succeeded, want not found")
	}
}

func TestLocalNetworkServiceSecurityGroupRulePriorityAndParentMismatch(t *testing.T) {
	service := NewLocalNetworkService()
	sgA, err := service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{TenantID: "tenant-a", IdempotencyKey: "sg-rule-parent-a", Name: "sg-a"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup A error = %v", err)
	}
	sgB, err := service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{TenantID: "tenant-a", IdempotencyKey: "sg-rule-parent-b", Name: "sg-b"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup B error = %v", err)
	}
	rule, err := service.CreateSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sgA.SecurityGroupID,
		IdempotencyKey:  "sg-rule-a",
		Priority:        90,
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
	if rule.Priority != 90 || rule.SecurityGroupID != sgA.SecurityGroupID {
		t.Fatalf("rule = %+v, want priority and parent security group", rule)
	}
	if _, err := service.GetSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleGetRequest{TenantID: "tenant-a", SecurityGroupID: sgB.SecurityGroupID, RuleID: rule.RuleID}); err == nil {
		t.Fatalf("GetSecurityGroupRule with mismatched parent succeeded, want not found")
	}
	updated, err := service.UpdateSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleUpdateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sgA.SecurityGroupID,
		RuleID:          rule.RuleID,
		Priority:        80,
		Action:          "deny",
	})
	if err != nil {
		t.Fatalf("UpdateSecurityGroupRule error = %v", err)
	}
	if updated.Priority != 80 || updated.Action != "deny" || updated.Direction != "ingress" {
		t.Fatalf("updated rule = %+v, want partial update preserving existing fields", updated)
	}
	list, err := service.ListSecurityGroupRules(context.Background(), ports.NetworkSecurityGroupRuleListRequest{TenantID: "tenant-a", SecurityGroupID: sgA.SecurityGroupID})
	if err != nil {
		t.Fatalf("ListSecurityGroupRules error = %v", err)
	}
	if len(list) != 1 || list[0].RuleID != rule.RuleID {
		t.Fatalf("rules = %#v, want updated rule", list)
	}
	deleted, err := service.DeleteSecurityGroupRule(context.Background(), ports.NetworkSecurityGroupRuleGetRequest{TenantID: "tenant-a", SecurityGroupID: sgA.SecurityGroupID, RuleID: rule.RuleID})
	if err != nil {
		t.Fatalf("DeleteSecurityGroupRule error = %v", err)
	}
	if deleted.RuleID != rule.RuleID {
		t.Fatalf("deleted rule = %+v, want rule %s", deleted, rule.RuleID)
	}
	list, err = service.ListSecurityGroupRules(context.Background(), ports.NetworkSecurityGroupRuleListRequest{TenantID: "tenant-a", SecurityGroupID: sgA.SecurityGroupID})
	if err != nil {
		t.Fatalf("ListSecurityGroupRules after delete error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("rules after delete = %#v, want empty", list)
	}
}

func TestLocalNetworkServiceSecurityGroupBindings(t *testing.T) {
	service := NewLocalNetworkService()
	sg, err := service.CreateSecurityGroup(context.Background(), ports.NetworkSecurityGroupCreateRequest{TenantID: "tenant-a", IdempotencyKey: "sg-binding-parent", Name: "sg"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup error = %v", err)
	}
	binding, err := service.CreateSecurityGroupBinding(context.Background(), ports.NetworkSecurityGroupBindingCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		IdempotencyKey:  "sg-binding-a",
		TargetType:      "instance",
		TargetID:        "inst-a",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroupBinding error = %v", err)
	}
	replay, err := service.CreateSecurityGroupBinding(context.Background(), ports.NetworkSecurityGroupBindingCreateRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		IdempotencyKey:  "sg-binding-a",
		TargetType:      "load_balancer",
		TargetID:        "lb-a",
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroupBinding replay error = %v", err)
	}
	if replay.BindingID != binding.BindingID || replay.TargetType != "instance" {
		t.Fatalf("idempotent binding = %+v, want original %+v", replay, binding)
	}
	items, err := service.ListSecurityGroupBindings(context.Background(), ports.NetworkSecurityGroupBindingListRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		TargetType:      "instance",
	})
	if err != nil {
		t.Fatalf("ListSecurityGroupBindings error = %v", err)
	}
	if len(items) != 1 || items[0].BindingID != binding.BindingID {
		t.Fatalf("bindings = %#v, want created binding", items)
	}
	deleted, err := service.DeleteSecurityGroupBinding(context.Background(), ports.NetworkSecurityGroupBindingDeleteRequest{
		TenantID:        "tenant-a",
		SecurityGroupID: sg.SecurityGroupID,
		BindingID:       binding.BindingID,
	})
	if err != nil {
		t.Fatalf("DeleteSecurityGroupBinding error = %v", err)
	}
	if deleted.BindingID != binding.BindingID {
		t.Fatalf("deleted binding = %+v, want binding %s", deleted, binding.BindingID)
	}
	items, err = service.ListSecurityGroupBindings(context.Background(), ports.NetworkSecurityGroupBindingListRequest{TenantID: "tenant-a", SecurityGroupID: sg.SecurityGroupID})
	if err != nil {
		t.Fatalf("ListSecurityGroupBindings after delete error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("bindings after delete = %#v, want empty", items)
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
