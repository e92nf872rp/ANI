package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubeOVNNetworkRendererRendersVPCAndSubnet(t *testing.T) {
	renderer := NewKubeOVNNetworkRenderer()

	vpc, err := renderer.RenderVPC(context.Background(), ports.NetworkVPCRecord{
		TenantID: "tenant_a",
		VPCID:    "vpc_main",
		Name:     "main",
		CIDR:     "10.40.0.0/16",
		State:    ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderVPC() error = %v", err)
	}
	subnet, err := renderer.RenderSubnet(context.Background(), ports.NetworkSubnetRecord{
		TenantID: "tenant_a",
		SubnetID: "subnet_private",
		VPCID:    "vpc_main",
		Name:     "private",
		CIDR:     "10.40.1.0/24",
		Gateway:  "10.40.1.1",
		State:    ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderSubnet() error = %v", err)
	}

	for _, want := range []string{"kubeovn.io/v1", `"kind": "Vpc"`, "vpc-vpc-main", "ani-tenant-tenant-a"} {
		if !strings.Contains(vpc[0].Content, want) {
			t.Fatalf("rendered VPC missing %q:\n%s", want, vpc[0].Content)
		}
	}
	for _, want := range []string{"kubeovn.io/v1", `"kind": "Subnet"`, "subnet-subnet-private", "10.40.1.0/24", "10.40.1.1", "vpc-vpc-main"} {
		if !strings.Contains(subnet[0].Content, want) {
			t.Fatalf("rendered Subnet missing %q:\n%s", want, subnet[0].Content)
		}
	}
}

func TestKubeOVNNetworkRendererRendersSecurityGroupAsNetworkPolicy(t *testing.T) {
	renderer := NewKubeOVNNetworkRenderer()

	manifests, err := renderer.RenderSecurityGroup(context.Background(), ports.NetworkSecurityGroupRecord{
		TenantID:        "tenant-a",
		SecurityGroupID: "sg_web",
		Name:            "web",
		State:           ports.NetworkResourceAvailable,
		Rules: []ports.NetworkSecurityGroupRule{
			{Direction: "ingress", Protocol: "tcp", PortRange: "443", CIDR: "0.0.0.0/0", Action: "allow"},
			{Direction: "egress", Protocol: "udp", PortRange: "53", CIDR: "10.96.0.0/12", Action: "allow"},
			{Direction: "ingress", Protocol: "tcp", PortRange: "22", CIDR: "0.0.0.0/0", Action: "deny"},
		},
	})
	if err != nil {
		t.Fatalf("RenderSecurityGroup() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "NetworkPolicy"`, "networking.k8s.io/v1", "sg-sg-web", "ani-tenant-tenant-a", "0.0.0.0/0", `"port": 443`, "10.96.0.0/12", `"protocol": "UDP"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered NetworkPolicy missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, `"port": 22`) {
		t.Fatalf("rendered NetworkPolicy included denied rule:\n%s", content)
	}
}

func TestKubeOVNNetworkRendererRendersLoadBalancerService(t *testing.T) {
	renderer := NewKubeOVNNetworkRenderer()

	manifests, err := renderer.RenderLoadBalancer(context.Background(), ports.NetworkLoadBalancerRecord{
		TenantID:       "tenant-a",
		LoadBalancerID: "lb_web",
		Name:           "web",
		VPCID:          "vpc_main",
		SubnetID:       "subnet_private",
		Scheme:         "public",
		State:          ports.NetworkResourceAvailable,
		Listeners: []ports.NetworkLoadBalancerListener{
			{Protocol: "http", Port: 80, TargetPort: 8080},
		},
	})
	if err != nil {
		t.Fatalf("RenderLoadBalancer() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "Service"`, `"type": "LoadBalancer"`, "lb-lb-web", "load-balancer-scheme", `"port": 80`, `"targetPort": 8080`, "subnet_private"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered Service missing %q:\n%s", want, content)
		}
	}
}

func TestKubeOVNNetworkRendererRendersRouteAsVpcStaticRoute(t *testing.T) {
	renderer := NewKubeOVNNetworkRenderer()

	manifests, err := renderer.RenderRoute(context.Background(), ports.NetworkRouteRecord{
		TenantID:        "tenant-a",
		RouteID:         "rt_default",
		VPCID:           "vpc_main",
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "10.40.1.1",
		Description:     "default route",
		State:           ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderRoute() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("RenderRoute() manifests = %d, want 1", len(manifests))
	}
	if manifests[0].Kind != "Vpc" || manifests[0].Provider != "kubeovn" || manifests[0].Name != "vpc-vpc-main" {
		t.Fatalf("RenderRoute() manifest = %+v, want kubeovn Vpc vpc-vpc-main", manifests[0])
	}
	for _, want := range []string{`"kind": "Vpc"`, `"staticRoutes"`, `"cidr": "0.0.0.0/0"`, `"nextHopIP": "10.40.1.1"`, `"policy": "policyDst"`, "rt_default", `"namespaces"`, "ani-tenant-tenant-a"} {
		if !strings.Contains(manifests[0].Content, want) {
			t.Fatalf("rendered route missing %q:\n%s", want, manifests[0].Content)
		}
	}
}

func TestKubeOVNNetworkRendererRejectsNonIPGatewayNextHop(t *testing.T) {
	renderer := NewKubeOVNNetworkRenderer()

	_, err := renderer.RenderRoute(context.Background(), ports.NetworkRouteRecord{
		TenantID:        "tenant-a",
		RouteID:         "rt_default",
		VPCID:           "vpc_main",
		DestinationCIDR: "0.0.0.0/0",
		NextHopType:     "gateway",
		NextHopID:       "test",
		State:           ports.NetworkResourceAvailable,
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("RenderRoute(non-IP next hop) error = %v, want ErrInvalid", err)
	}
}
