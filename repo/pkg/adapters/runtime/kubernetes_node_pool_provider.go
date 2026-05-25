package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesNodePoolProviderClient interface {
	ApplyManifests(ctx context.Context, manifests []ports.WorkloadManifest) ([]string, error)
}

type KubernetesNodePoolProviderAdapter struct {
	client KubernetesNodePoolProviderClient
	now    func() time.Time
}

type KubernetesNodePoolProviderOption func(*KubernetesNodePoolProviderAdapter)

func WithKubernetesNodePoolProviderClock(now func() time.Time) KubernetesNodePoolProviderOption {
	return func(adapter *KubernetesNodePoolProviderAdapter) {
		if now != nil {
			adapter.now = now
		}
	}
}

func NewKubernetesNodePoolProviderAdapter(client KubernetesNodePoolProviderClient, options ...KubernetesNodePoolProviderOption) *KubernetesNodePoolProviderAdapter {
	adapter := &KubernetesNodePoolProviderAdapter{client: client, now: time.Now}
	for _, option := range options {
		option(adapter)
	}
	return adapter
}

func (a *KubernetesNodePoolProviderAdapter) ApplyK8sClusterNodePool(ctx context.Context, request ports.K8sClusterNodePoolProviderRequest) (ports.K8sClusterNodePoolProviderResult, error) {
	if err := validateK8sClusterNodePoolProviderRequest(request); err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	if a.client == nil {
		return ports.K8sClusterNodePoolProviderResult{}, fmt.Errorf("%w: Kubernetes node pool provider client is required", ports.ErrNotConfigured)
	}
	manifest, err := renderKubernetesNodePoolManifest(request, false)
	if err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	refs, err := a.client.ApplyManifests(ctx, []ports.WorkloadManifest{manifest})
	if err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	return ports.K8sClusterNodePoolProviderResult{
		Applied:      true,
		Provider:     "clusterapi",
		ResourceRefs: refs,
		Reason:       "Cluster API MachineDeployment applied",
		AppliedAt:    a.now().UTC(),
	}, nil
}

func (a *KubernetesNodePoolProviderAdapter) DeleteK8sClusterNodePool(ctx context.Context, request ports.K8sClusterNodePoolProviderRequest) (ports.K8sClusterNodePoolProviderResult, error) {
	if err := validateK8sClusterNodePoolProviderRequest(request); err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	if a.client == nil {
		return ports.K8sClusterNodePoolProviderResult{}, fmt.Errorf("%w: Kubernetes node pool provider client is required", ports.ErrNotConfigured)
	}
	manifest, err := renderKubernetesNodePoolManifest(request, true)
	if err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	refs, err := a.client.ApplyManifests(ctx, []ports.WorkloadManifest{manifest})
	if err != nil {
		return ports.K8sClusterNodePoolProviderResult{}, err
	}
	return ports.K8sClusterNodePoolProviderResult{
		Applied:      true,
		Provider:     "clusterapi",
		ResourceRefs: refs,
		Reason:       "Cluster API MachineDeployment delete intent applied",
		AppliedAt:    a.now().UTC(),
	}, nil
}

func validateK8sClusterNodePoolProviderRequest(request ports.K8sClusterNodePoolProviderRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" || strings.TrimSpace(request.ClusterName) == "" || strings.TrimSpace(request.NodePoolID) == "" || strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.InstanceType) == "" {
		return fmt.Errorf("%w: tenant_id, cluster_id, cluster_name, node_pool_id, name and instance_type are required for node pool provider", ports.ErrInvalid)
	}
	if request.NodeCount < 0 {
		return fmt.Errorf("%w: node_count cannot be negative", ports.ErrInvalid)
	}
	if request.GPU.Count < 0 {
		return fmt.Errorf("%w: gpu.count cannot be negative", ports.ErrInvalid)
	}
	return nil
}

func renderKubernetesNodePoolManifest(request ports.K8sClusterNodePoolProviderRequest, deleteIntent bool) (ports.WorkloadManifest, error) {
	replicas := request.NodeCount
	if deleteIntent {
		replicas = 0
	}
	name := kubernetesNodePoolName(request)
	labels := map[string]string{
		"app.kubernetes.io/managed-by":       "ani-core",
		"ani.kubercloud.io/tenant-id":        request.TenantID,
		"ani.kubercloud.io/k8s-cluster-id":   request.ClusterID,
		"ani.kubercloud.io/node-pool-id":     request.NodePoolID,
		"ani.kubercloud.io/node-pool-name":   request.Name,
		"cluster.x-k8s.io/cluster-name":      request.ClusterName,
		"ani.kubercloud.io/instance-type":    request.InstanceType,
		"ani.kubercloud.io/provider-intent":  "node-pool",
		"ani.kubercloud.io/provider-version": "v1",
	}
	if request.GPU.Vendor != "" {
		labels["ani.kubercloud.io/gpu-vendor"] = request.GPU.Vendor
	}
	if request.GPU.Model != "" {
		labels["ani.kubercloud.io/gpu-model"] = request.GPU.Model
	}
	annotations := map[string]string{
		"ani.kubercloud.io/operation": operationOrDefault(request.Operation, "apply"),
	}
	if deleteIntent {
		annotations["ani.kubercloud.io/delete-intent"] = "true"
	}
	machineSpec := map[string]any{
		"clusterName":  request.ClusterName,
		"instanceType": request.InstanceType,
	}
	if request.GPU.Count > 0 {
		machineSpec["gpu"] = map[string]any{
			"vendor":        request.GPU.Vendor,
			"model":         request.GPU.Model,
			"count":         request.GPU.Count,
			"resourceName":  request.GPU.ResourceName,
			"schedulingKey": firstNonEmpty(request.GPU.ResourceName, request.GPU.Vendor),
		}
	}
	doc := map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
		"kind":       "MachineDeployment",
		"metadata": map[string]any{
			"name":        name,
			"namespace":   tenantNamespace(request.TenantID),
			"labels":      labels,
			"annotations": annotations,
		},
		"spec": map[string]any{
			"clusterName": request.ClusterName,
			"replicas":    replicas,
			"selector": map[string]any{
				"matchLabels": map[string]string{
					"ani.kubercloud.io/node-pool-id": request.NodePoolID,
				},
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec":     machineSpec,
			},
		},
	}
	content, err := json.Marshal(doc)
	if err != nil {
		return ports.WorkloadManifest{}, err
	}
	return ports.WorkloadManifest{
		Provider: "clusterapi",
		Kind:     "MachineDeployment",
		Name:     name,
		Content:  string(content),
	}, nil
}

func kubernetesNodePoolName(request ports.K8sClusterNodePoolProviderRequest) string {
	name := strings.ToLower(firstNonEmpty(request.Name, request.NodePoolID))
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('-')
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		result = "node-pool"
	}
	if len(result) > 63 {
		result = strings.TrimRight(result[:63], "-")
	}
	return result
}

func operationOrDefault(operation string, fallback string) string {
	if strings.TrimSpace(operation) == "" {
		return fallback
	}
	return strings.TrimSpace(operation)
}

var _ ports.K8sClusterNodePoolProvider = (*KubernetesNodePoolProviderAdapter)(nil)
