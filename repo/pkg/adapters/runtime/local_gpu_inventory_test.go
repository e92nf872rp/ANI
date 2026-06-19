package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalGPUInventoryListsNodeClassesWithDevProfileData(t *testing.T) {
	inventory := NewLocalGPUInventory()
	nodes, err := inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{
		Vendors: []ports.GPUVendor{ports.GPUVendorNVIDIA},
	})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("nodes is empty, want local GPU inventory")
	}
	if nodes[0].Vendor != ports.GPUVendorNVIDIA || len(nodes[0].Devices) == 0 {
		t.Fatalf("node = %+v, want NVIDIA devices", nodes[0])
	}
	if nodes[0].Devices[0].MemoryMiB <= 0 || nodes[0].Devices[0].DriverVersion == "" {
		t.Fatalf("device = %+v, want memory and driver metadata", nodes[0].Devices[0])
	}
}

func TestLocalGPUInventoryGetNodeClassAndScheduling(t *testing.T) {
	inventory := NewLocalGPUInventory()
	node, err := inventory.GetNodeClass(context.Background(), "ani-gpu-a")
	if err != nil {
		t.Fatalf("GetNodeClass error = %v", err)
	}
	if node.NodeName != "ani-gpu-a" || !node.Ready {
		t.Fatalf("node = %+v, want ready ani-gpu-a", node)
	}
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 2,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.ResourceName != "nvidia.com/gpu" || decision.ResourceQuantity != "2" {
		t.Fatalf("decision = %+v, want nvidia.com/gpu x2", decision)
	}
}
