package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesNodePoolProviderAdapterRendersClusterAPIMachineDeployment(t *testing.T) {
	client := &fakeNodePoolProviderClient{}
	adapter := NewKubernetesNodePoolProviderAdapter(client)

	result, err := adapter.ApplyK8sClusterNodePool(context.Background(), ports.K8sClusterNodePoolProviderRequest{
		Operation:    "create",
		TenantID:     "tenant-a",
		ClusterID:    "k8sclu-provider",
		ClusterName:  "vc-a",
		NodePoolID:   "k8snp-gpu",
		Name:         "gpu-pool",
		NodeCount:    2,
		InstanceType: "gpu.l4.xlarge",
		GPU: ports.K8sClusterNodePoolGPU{
			Vendor:       "nvidia",
			Model:        "L4",
			Count:        1,
			ResourceName: "nvidia.com/gpu",
		},
	})
	if err != nil {
		t.Fatalf("ApplyK8sClusterNodePool() error = %v", err)
	}
	if !result.Applied || result.Provider != "clusterapi" {
		t.Fatalf("result = %+v, want applied clusterapi", result)
	}
	if len(client.manifests) != 1 {
		t.Fatalf("manifest count = %d, want 1", len(client.manifests))
	}
	manifest := client.manifests[0]
	if manifest.Provider != "clusterapi" || manifest.Kind != "MachineDeployment" || manifest.Name != "gpu-pool" {
		t.Fatalf("manifest metadata = %+v, want Cluster API MachineDeployment gpu-pool", manifest)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(manifest.Content), &doc); err != nil {
		t.Fatalf("manifest content is not JSON: %v", err)
	}
	metadata := doc["metadata"].(map[string]any)
	if metadata["namespace"] != "ani-tenant-tenant-a" {
		t.Fatalf("namespace = %v, want tenant namespace", metadata["namespace"])
	}
	spec := doc["spec"].(map[string]any)
	if spec["clusterName"] != "vc-a" || spec["replicas"].(float64) != 2 {
		t.Fatalf("spec = %+v, want clusterName vc-a replicas 2", spec)
	}
	template := spec["template"].(map[string]any)
	machineSpec := template["spec"].(map[string]any)
	if machineSpec["instanceType"] != "gpu.l4.xlarge" {
		t.Fatalf("machine spec = %+v, want instance type", machineSpec)
	}
	if !strings.Contains(manifest.Content, `"nvidia.com/gpu"`) || !strings.Contains(manifest.Content, `"ani.kubercloud.io/gpu-vendor"`) {
		t.Fatalf("manifest content = %s, want GPU scheduling labels", manifest.Content)
	}
}

func TestKubernetesNodePoolProviderAdapterRendersDeleteIntentAsZeroReplicas(t *testing.T) {
	client := &fakeNodePoolProviderClient{}
	adapter := NewKubernetesNodePoolProviderAdapter(client)

	result, err := adapter.DeleteK8sClusterNodePool(context.Background(), ports.K8sClusterNodePoolProviderRequest{
		TenantID:     "tenant-a",
		ClusterID:    "k8sclu-provider",
		ClusterName:  "vc-a",
		NodePoolID:   "k8snp-gpu",
		Name:         "gpu-pool",
		NodeCount:    2,
		InstanceType: "gpu.l4.xlarge",
	})
	if err != nil {
		t.Fatalf("DeleteK8sClusterNodePool() error = %v", err)
	}
	if !result.Applied || result.Reason != "Cluster API MachineDeployment delete intent applied" {
		t.Fatalf("result = %+v, want delete intent applied", result)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(client.manifests[0].Content), &doc); err != nil {
		t.Fatalf("manifest content is not JSON: %v", err)
	}
	spec := doc["spec"].(map[string]any)
	if spec["replicas"].(float64) != 0 {
		t.Fatalf("replicas = %v, want zero replica delete intent", spec["replicas"])
	}
	metadata := doc["metadata"].(map[string]any)
	annotations := metadata["annotations"].(map[string]any)
	if annotations["ani.kubercloud.io/delete-intent"] != "true" {
		t.Fatalf("annotations = %+v, want delete intent annotation", annotations)
	}
}

type fakeNodePoolProviderClient struct {
	manifests []ports.WorkloadManifest
}

func (c *fakeNodePoolProviderClient) ApplyManifests(_ context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	c.manifests = append([]ports.WorkloadManifest(nil), manifests...)
	return []string{"clusterapi/MachineDeployment/" + manifests[0].Name}, nil
}
