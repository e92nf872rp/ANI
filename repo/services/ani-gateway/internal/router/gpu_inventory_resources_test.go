package router

import (
	"context"
	"testing"
)

func TestGPUInventoryAPIListsInventoryAndOccupancy(t *testing.T) {
	api := newGPUInventoryAPI()
	records, err := api.inventory.ListNodeClasses(context.Background(), api.gpuFilter("", "", ""))
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "")
	if len(listResponse.Items) == 0 || listResponse.Total != len(listResponse.Items) {
		t.Fatalf("inventory response = %+v, want items and total", listResponse)
	}
	requireLocalCoreDevProfile(t, listResponse.DevProfile, "local-gpu-inventory")
	if listResponse.Items[0].ID == "" || listResponse.Items[0].NodeName == "" || listResponse.Items[0].GPUType == "" {
		t.Fatalf("first GPU = %+v, want schema fields", listResponse.Items[0])
	}
	requireLocalCoreDevProfile(t, listResponse.Items[0].DevProfile, "local-gpu-inventory")

	occupancy := api.gpuOccupancyFromNodes(records)
	if occupancy.Total != len(listResponse.Items) || occupancy.Available+occupancy.InUse+occupancy.Fault != occupancy.Total {
		t.Fatalf("occupancy = %+v, inventory total = %d", occupancy, len(listResponse.Items))
	}
	if len(occupancy.ByGPUType) == 0 {
		t.Fatalf("occupancy by_gpu_type is empty")
	}
	requireLocalCoreDevProfile(t, occupancy.DevProfile, "local-gpu-inventory")
}

func TestGPUInventoryAPISandboxTemplatesUseLocalCatalog(t *testing.T) {
	api := newGPUInventoryAPI()
	result, err := api.templates.ListSandboxTemplates(context.Background(), api.sandboxTemplateListRequest(10, ""))
	if err != nil {
		t.Fatalf("ListSandboxTemplates error = %v", err)
	}
	response := api.sandboxTemplateListFromResult(result)
	if len(response.Items) == 0 || response.Total != len(response.Items) {
		t.Fatalf("templates response = %+v, want items and total", response)
	}
	if response.Items[0].ID == "" || response.Items[0].Image == "" || !response.Items[0].IsBuiltin {
		t.Fatalf("template = %+v, want builtin schema fields", response.Items[0])
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-sandbox-template-catalog")
	requireLocalCoreDevProfile(t, response.Items[0].DevProfile, "local-sandbox-template-catalog")
}
