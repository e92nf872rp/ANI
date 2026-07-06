package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubeOVNNetworkRenderer struct{}

func NewKubeOVNNetworkRenderer() *KubeOVNNetworkRenderer {
	return &KubeOVNNetworkRenderer{}
}

func (r *KubeOVNNetworkRenderer) RenderVPC(_ context.Context, record ports.NetworkVPCRecord) ([]ports.WorkloadManifest, error) {
	if err := requireNetworkRecord(record.TenantID, record.VPCID, record.Name, record.State); err != nil {
		return nil, err
	}
	name := networkProviderName("vpc", record.VPCID)
	content := manifest(map[string]any{
		"apiVersion": "kubeovn.io/v1",
		"kind":       "Vpc",
		"metadata":   networkProviderMetadata(record.TenantID, name, "vpc", record.VPCID),
		"spec": map[string]any{
			"namespaces": []any{tenantNamespace(record.TenantID)},
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "Vpc", Provider: "kubeovn", Content: content}}, nil
}

func (r *KubeOVNNetworkRenderer) RenderSubnet(_ context.Context, record ports.NetworkSubnetRecord) ([]ports.WorkloadManifest, error) {
	if err := requireNetworkRecord(record.TenantID, record.SubnetID, record.Name, record.State); err != nil {
		return nil, err
	}
	if strings.TrimSpace(record.VPCID) == "" {
		return nil, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	name := networkProviderName("subnet", record.SubnetID)
	spec := map[string]any{
		"protocol":    "IPv4",
		"cidrBlock":   record.CIDR,
		"vpc":         networkProviderName("vpc", record.VPCID),
		"namespaces":  []any{tenantNamespace(record.TenantID)},
		"private":     true,
		"natOutgoing": false,
	}
	if strings.TrimSpace(record.Gateway) != "" {
		spec["gateway"] = strings.TrimSpace(record.Gateway)
	}
	content := manifest(map[string]any{
		"apiVersion": "kubeovn.io/v1",
		"kind":       "Subnet",
		"metadata":   networkProviderMetadata(record.TenantID, name, "subnet", record.SubnetID),
		"spec":       spec,
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "Subnet", Provider: "kubeovn", Content: content}}, nil
}

func (r *KubeOVNNetworkRenderer) RenderSecurityGroup(_ context.Context, record ports.NetworkSecurityGroupRecord) ([]ports.WorkloadManifest, error) {
	if err := requireNetworkRecord(record.TenantID, record.SecurityGroupID, record.Name, record.State); err != nil {
		return nil, err
	}
	name := networkProviderName("sg", record.SecurityGroupID)
	spec := map[string]any{
		"podSelector": map[string]any{},
		"policyTypes": []any{"Ingress", "Egress"},
		"ingress":     networkPolicyIngress(record.Rules),
		"egress":      networkPolicyEgress(record.Rules),
	}
	content := manifest(map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   networkProviderNamespacedMetadata(record.TenantID, name, "security-group", record.SecurityGroupID),
		"spec":       spec,
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "NetworkPolicy", Provider: "kubernetes", Content: content}}, nil
}

func (r *KubeOVNNetworkRenderer) RenderLoadBalancer(_ context.Context, record ports.NetworkLoadBalancerRecord) ([]ports.WorkloadManifest, error) {
	if err := requireNetworkRecord(record.TenantID, record.LoadBalancerID, record.Name, record.State); err != nil {
		return nil, err
	}
	if strings.TrimSpace(record.VPCID) == "" {
		return nil, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	name := networkProviderName("lb", record.LoadBalancerID)
	serviceType := "ClusterIP"
	if strings.TrimSpace(record.Scheme) == "public" {
		serviceType = "LoadBalancer"
	}
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   networkProviderLoadBalancerMetadata(record, name),
		"spec": map[string]any{
			"type":     serviceType,
			"selector": map[string]any{"ani.kubercloud.io/network-load-balancer": record.LoadBalancerID},
			"ports":    networkServicePorts(record.Listeners),
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "Service", Provider: "kubernetes", Content: content}}, nil
}

func (r *KubeOVNNetworkRenderer) RenderRoute(_ context.Context, record ports.NetworkRouteRecord) ([]ports.WorkloadManifest, error) {
	if err := requireNetworkRecord(record.TenantID, record.RouteID, record.RouteID, record.State); err != nil {
		return nil, err
	}
	if strings.TrimSpace(record.VPCID) == "" {
		return nil, fmt.Errorf("%w: vpc_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.DestinationCIDR) == "" || strings.TrimSpace(record.NextHopID) == "" {
		return nil, fmt.Errorf("%w: destination_cidr and next_hop_id are required", ports.ErrInvalid)
	}
	nextHopType := strings.ToLower(strings.TrimSpace(record.NextHopType))
	if nextHopType != "gateway" {
		return nil, fmt.Errorf("%w: kubeovn route rendering currently supports gateway next_hop_type only", ports.ErrUnsupported)
	}
	name := networkProviderName("vpc", record.VPCID)
	metadata := networkProviderMetadata(record.TenantID, name, "vpc", record.VPCID)
	metadata["annotations"] = map[string]string{
		"ani.kubercloud.io/network-route-id":            record.RouteID,
		"ani.kubercloud.io/network-route-next-hop":      strings.TrimSpace(record.NextHopID),
		"ani.kubercloud.io/network-route-next-hop-type": nextHopType,
	}
	content := manifest(map[string]any{
		"apiVersion": "kubeovn.io/v1",
		"kind":       "Vpc",
		"metadata":   metadata,
		"spec": map[string]any{
			"staticRoutes": []any{map[string]any{
				"cidr":      strings.TrimSpace(record.DestinationCIDR),
				"nextHopIP": strings.TrimSpace(record.NextHopID),
				"policy":    "policyDst",
			}},
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "Vpc", Provider: "kubeovn", Content: content}}, nil
}

func networkProviderMetadata(tenantID string, name string, resourceKind string, resourceID string) map[string]any {
	return map[string]any{
		"name":   name,
		"labels": networkProviderLabels(tenantID, resourceKind, resourceID),
	}
}

func networkProviderNamespacedMetadata(tenantID string, name string, resourceKind string, resourceID string) map[string]any {
	metadata := networkProviderMetadata(tenantID, name, resourceKind, resourceID)
	metadata["namespace"] = tenantNamespace(tenantID)
	return metadata
}

func networkProviderLoadBalancerMetadata(record ports.NetworkLoadBalancerRecord, name string) map[string]any {
	metadata := networkProviderNamespacedMetadata(record.TenantID, name, "load-balancer", record.LoadBalancerID)
	metadata["annotations"] = map[string]string{
		"ani.kubercloud.io/load-balancer-scheme": firstNetworkNonEmpty(record.Scheme, "internal"),
		"ani.kubercloud.io/vpc-id":               record.VPCID,
		"ani.kubercloud.io/subnet-id":            record.SubnetID,
	}
	return metadata
}

func networkProviderLabels(tenantID string, resourceKind string, resourceID string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/part-of":          "ani-platform",
		"app.kubernetes.io/managed-by":       "ani-core",
		"ani.kubercloud.io/tenant-id":        tenantID,
		"ani.kubercloud.io/network-kind":     resourceKind,
		"ani.kubercloud.io/network-resource": resourceID,
	}
}

func networkPolicyIngress(rules []ports.NetworkSecurityGroupRule) []any {
	result := []any{}
	for _, rule := range rules {
		if !isAllowedNetworkRule(rule, "ingress") {
			continue
		}
		entry := map[string]any{}
		if peer := networkPolicyPeer(rule.CIDR); peer != nil {
			entry["from"] = []any{peer}
		}
		if port := networkPolicyPort(rule); port != nil {
			entry["ports"] = []any{port}
		}
		result = append(result, entry)
	}
	return result
}

func networkPolicyEgress(rules []ports.NetworkSecurityGroupRule) []any {
	result := []any{}
	for _, rule := range rules {
		if !isAllowedNetworkRule(rule, "egress") {
			continue
		}
		entry := map[string]any{}
		if peer := networkPolicyPeer(rule.CIDR); peer != nil {
			entry["to"] = []any{peer}
		}
		if port := networkPolicyPort(rule); port != nil {
			entry["ports"] = []any{port}
		}
		result = append(result, entry)
	}
	return result
}

func isAllowedNetworkRule(rule ports.NetworkSecurityGroupRule, direction string) bool {
	return strings.EqualFold(rule.Direction, direction) && strings.EqualFold(firstNetworkNonEmpty(rule.Action, "allow"), "allow")
}

func networkPolicyPeer(cidr string) map[string]any {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return nil
	}
	return map[string]any{"ipBlock": map[string]any{"cidr": cidr}}
}

func networkPolicyPort(rule ports.NetworkSecurityGroupRule) map[string]any {
	port, err := strconv.Atoi(strings.TrimSpace(rule.PortRange))
	if err != nil || port <= 0 {
		return nil
	}
	protocol := strings.ToUpper(firstNetworkNonEmpty(rule.Protocol, "tcp"))
	return map[string]any{"protocol": protocol, "port": port}
}

func networkServicePorts(listeners []ports.NetworkLoadBalancerListener) []any {
	result := make([]any, 0, len(listeners))
	for _, listener := range listeners {
		port := int(listener.Port)
		targetPort := int(listener.TargetPort)
		if targetPort == 0 {
			targetPort = port
		}
		result = append(result, map[string]any{
			"name":       strings.ToLower(firstNetworkNonEmpty(listener.Protocol, "tcp")) + "-" + strconv.Itoa(port),
			"protocol":   strings.ToUpper(firstNetworkNonEmpty(listener.Protocol, "tcp")),
			"port":       port,
			"targetPort": targetPort,
		})
	}
	return result
}

func networkProviderName(prefix string, value string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		case r == '-' || r == '.':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(value))
	clean = strings.Trim(clean, "-.")
	if clean == "" {
		clean = "resource"
	}
	name := prefix + "-" + clean
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-.")
}

func KubeOVNProviderName(prefix string, value string) string {
	return networkProviderName(prefix, value)
}

var _ ports.NetworkProviderRenderer = (*KubeOVNNetworkRenderer)(nil)
