package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalGPUSpecServiceListsAvailableSpecsFromInventory(t *testing.T) {
	service := NewLocalGPUSpecService(NewLocalGPUInventory())
	items, err := service.ListGPUSpecs(context.Background(), ports.GPUSpecListRequest{})
	if err != nil {
		t.Fatalf("ListGPUSpecs error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "gpu-a100-full" || !items[0].Available || items[0].Shares != 1 {
		t.Fatalf("specs = %#v, want available A100 full-card spec", items)
	}
}

func TestLocalGPUSpecServiceReturnsUnavailableSpecForHistory(t *testing.T) {
	service := NewLocalGPUSpecService(NewLocalGPUInventory())
	available := false
	items, err := service.ListGPUSpecs(context.Background(), ports.GPUSpecListRequest{Available: &available})
	if err != nil {
		t.Fatalf("ListGPUSpecs error = %v", err)
	}
	if len(items) != 1 || items[0].GPUType != "L40S" || items[0].Available {
		t.Fatalf("specs = %#v, want unavailable L40S history spec", items)
	}
	got, err := service.GetGPUSpec(context.Background(), items[0].ID)
	if err != nil {
		t.Fatalf("GetGPUSpec error = %v", err)
	}
	if got.ID != items[0].ID || got.Available {
		t.Fatalf("spec = %+v, want unavailable history spec", got)
	}
}
