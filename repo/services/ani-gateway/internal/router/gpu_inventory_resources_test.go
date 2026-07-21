package router

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestGPUInventoryAPIListsInventoryAndOccupancy(t *testing.T) {
	api := newGPUInventoryAPI()
	records, err := api.inventory.ListNodeClasses(context.Background(), api.gpuFilter("", "", ""))
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	emptyOccupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", emptyOccupancy)
	if len(listResponse.Items) == 0 || listResponse.Total != len(listResponse.Items) {
		t.Fatalf("inventory response = %+v, want items and total", listResponse)
	}
	requireLocalCoreDevProfile(t, listResponse.DevProfile, "local-gpu-inventory")
	if listResponse.Items[0].ID == "" || listResponse.Items[0].NodeName == "" || listResponse.Items[0].GPUType == "" {
		t.Fatalf("first GPU = %+v, want schema fields", listResponse.Items[0])
	}
	requireLocalCoreDevProfile(t, listResponse.Items[0].DevProfile, "local-gpu-inventory")

	occupancy := api.gpuOccupancyFromNodes(records, emptyOccupancy)
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

func TestGPUInventoryAPIWithProviderMarksRealDevProfile(t *testing.T) {
	api := newGPUInventoryAPIWithInventory(fakeGPUInventory{nodes: []ports.GPUNodeClass{{
		NodeName: "gpu-node-a",
		Vendor:   ports.GPUVendorNVIDIA,
		Model:    "NVIDIA-L40S",
		Ready:    true,
		Devices: []ports.GPUDeviceClass{{
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         "NVIDIA-L40S",
			ResourceName:  "nvidia.com/gpu",
			DriverVersion: "device-plugin",
		}},
	}}})

	records, err := api.inventory.ListNodeClasses(context.Background(), api.gpuFilter("", "", ""))
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	emptyOccupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", emptyOccupancy)
	if listResponse.DevProfile.Mode != "real" || !listResponse.DevProfile.RealProvider || listResponse.DevProfile.Provider != "kubernetes-gpu-inventory" {
		t.Fatalf("list dev_profile = %+v, want Kubernetes GPU real provider", listResponse.DevProfile)
	}
	if len(listResponse.Items) != 1 || listResponse.Items[0].DevProfile.Provider != "kubernetes-gpu-inventory" || !listResponse.Items[0].DevProfile.RealProvider {
		t.Fatalf("items = %+v, want real provider item profile", listResponse.Items)
	}

	occupancy := api.gpuOccupancyFromNodes(records, emptyOccupancy)
	if occupancy.DevProfile.Mode != "real" || !occupancy.DevProfile.RealProvider || occupancy.DevProfile.Provider != "kubernetes-gpu-inventory" {
		t.Fatalf("occupancy dev_profile = %+v, want Kubernetes GPU real provider", occupancy.DevProfile)
	}
}

type fakeGPUInventory struct {
	nodes []ports.GPUNodeClass
}

func (f fakeGPUInventory) ListNodeClasses(context.Context, ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	return f.nodes, nil
}

func (f fakeGPUInventory) GetNodeClass(context.Context, string) (ports.GPUNodeClass, error) {
	if len(f.nodes) == 0 {
		return ports.GPUNodeClass{}, ports.ErrNotFound
	}
	return f.nodes[0], nil
}

func (f fakeGPUInventory) PlanScheduling(context.Context, ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	return ports.GPUSchedulingDecision{}, ports.ErrUnsupported
}

// stubInstanceStore is an in-memory WorkloadInstanceStore for GPU inventory
// echo tests. It only implements List; other methods return ErrNotFound /
// ErrUnsupported.
type stubInstanceStore struct {
	records []ports.WorkloadInstanceRecord
	err     error
}

func (s stubInstanceStore) UpsertStatus(context.Context, ports.WorkloadInstanceRecord) error {
	return ports.ErrUnsupported
}
func (s stubInstanceStore) Get(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
	return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
}
func (s stubInstanceStore) List(_ context.Context, _ string, _ ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.records, nil
}

var _ ports.WorkloadInstanceStore = (*stubInstanceStore)(nil)

// newFakeGPUInventoryWithNode builds a fake inventory with a single ready GPU
// node containing deviceCount cards.
func newFakeGPUInventoryWithNode(nodeName string, deviceCount int) fakeGPUInventory {
	devices := make([]ports.GPUDeviceClass, 0, deviceCount)
	for i := 0; i < deviceCount; i++ {
		devices = append(devices, ports.GPUDeviceClass{
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         "NVIDIA-L40S",
			ResourceName:  "nvidia.com/gpu",
			DriverVersion: "device-plugin",
		})
	}
	return fakeGPUInventory{nodes: []ports.GPUNodeClass{{
		NodeName: nodeName,
		Vendor:   ports.GPUVendorNVIDIA,
		Model:    "NVIDIA-L40S",
		Ready:    true,
		Devices:  devices,
	}}}
}

func TestGPUInventoryListEchoesInstanceIDForRunningGPUContainerOnSameNode(t *testing.T) {
	// Scenario: 1 GPU node with 2 cards; 1 running gpu_container instance on
	// that node. Per Plan A (node-level ownership), all cards on the same
	// node echo the same instance_id and status is set to in_use.
	store := stubInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-a-001",
		Kind:       ports.WorkloadKindGPUContainer,
		Status: ports.WorkloadStatus{
			NodeName: "gpu-node-a",
			State:    ports.WorkloadStateRunning,
		},
	}}}
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("gpu-node-a", 2), store, nil)

	// Build occupancy map directly (bypass Hertz context).
	occupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{
		"gpu-node-a": {TenantID: "tenant-a", InstanceID: "inst-a-001", NodeName: "gpu-node-a"},
	}}
	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", occupancy)
	if len(listResponse.Items) != 2 {
		t.Fatalf("items = %d, want 2 devices", len(listResponse.Items))
	}
	for i, item := range listResponse.Items {
		if item.Status != "in_use" {
			t.Fatalf("item[%d].status = %q, want in_use", i, item.Status)
		}
		if item.InstanceID == nil || *item.InstanceID != "inst-a-001" {
			t.Fatalf("item[%d].instance_id = %v, want inst-a-001", i, item.InstanceID)
		}
		if item.TenantID == nil || *item.TenantID != "tenant-a" {
			t.Fatalf("item[%d].tenant_id = %v, want tenant-a", i, item.TenantID)
		}
	}
}

func TestGPUInventoryListLeavesAvailableWhenNoInstanceOnNode(t *testing.T) {
	// Scenario: GPU node ready, but InstanceStore has no running instance on
	// that node. Cards stay available; instance_id/tenant_id are nil.
	store := stubInstanceStore{records: nil}
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("gpu-node-a", 1), store, nil)

	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	emptyOccupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", emptyOccupancy)
	if len(listResponse.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResponse.Items))
	}
	item := listResponse.Items[0]
	if item.Status != "available" {
		t.Fatalf("status = %q, want available", item.Status)
	}
	if item.InstanceID != nil {
		t.Fatalf("instance_id = %v, want nil", item.InstanceID)
	}
	if item.TenantID != nil {
		t.Fatalf("tenant_id = %v, want nil", item.TenantID)
	}
}

func TestGPUInventoryListIgnoresNonRunningInstance(t *testing.T) {
	// Scenario: instance exists but state is non-running (e.g. pending); it
	// should not occupy GPU cards. gpuNodeOccupancy filters non-running.
	store := stubInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-a-002",
		Kind:       ports.WorkloadKindGPUContainer,
		Status: ports.WorkloadStatus{
			NodeName: "gpu-node-a",
			State:    ports.WorkloadStatePending,
		},
	}}}
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("gpu-node-a", 1), store, nil)

	occupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", occupancy)
	if len(listResponse.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResponse.Items))
	}
	if listResponse.Items[0].Status != "available" {
		t.Fatalf("status = %q, want available (non-running instance should not occupy)", listResponse.Items[0].Status)
	}
}

func TestGPUInventoryListMarksFaultNodeAsFaultRegardlessOfOccupancy(t *testing.T) {
	// Scenario: node NotReady; even if a running instance record exists,
	// card status should be fault and instance_id should not be echoed
	// (ownership on a faulty node is unreliable).
	store := stubInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-a-003",
		Kind:       ports.WorkloadKindGPUContainer,
		Status: ports.WorkloadStatus{
			NodeName: "fault-node",
			State:    ports.WorkloadStateRunning,
		},
	}}}
	inventory := fakeGPUInventory{nodes: []ports.GPUNodeClass{{
		NodeName: "fault-node",
		Vendor:   ports.GPUVendorNVIDIA,
		Model:    "NVIDIA-L40S",
		Ready:    false,
		Reason:   "KubeletNotReady",
		Devices: []ports.GPUDeviceClass{{
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         "NVIDIA-L40S",
			ResourceName:  "nvidia.com/gpu",
			DriverVersion: "device-plugin",
		}},
	}}}
	api := newGPUInventoryAPIWithStore(inventory, store, nil)

	occupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{
		"fault-node": {TenantID: "tenant-a", InstanceID: "inst-a-003", NodeName: "fault-node"},
	}}
	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", occupancy)
	if len(listResponse.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResponse.Items))
	}
	item := listResponse.Items[0]
	if item.Status != "fault" {
		t.Fatalf("status = %q, want fault", item.Status)
	}
	if item.InstanceID != nil {
		t.Fatalf("instance_id = %v, want nil on fault node", item.InstanceID)
	}
}

func TestGPUInventoryOccupancyCountsInUseWhenInstanceEchoed(t *testing.T) {
	// Scenario: 1 node, 2 cards, both have running instance echo; occupancy
	// stats should be InUse=2 / Available=0.
	store := stubInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-a-004",
		Kind:       ports.WorkloadKindGPUContainer,
		Status: ports.WorkloadStatus{
			NodeName: "gpu-node-a",
			State:    ports.WorkloadStateRunning,
		},
	}}}
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("gpu-node-a", 2), store, nil)

	occupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{
		"gpu-node-a": {TenantID: "tenant-a", InstanceID: "inst-a-004", NodeName: "gpu-node-a"},
	}}
	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	occupancyResp := api.gpuOccupancyFromNodes(records, occupancy)
	if occupancyResp.Total != 2 || occupancyResp.InUse != 2 || occupancyResp.Available != 0 {
		t.Fatalf("occupancy = %+v, want Total=2 InUse=2 Available=0", occupancyResp)
	}
}

func TestGPUInventoryListWithNilStoreFallsBackToNoEcho(t *testing.T) {
	// Scenario: no InstanceStore injected (local/dev profile); behaviour
	// matches the old hardcoded nil path.
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("gpu-node-a", 1), nil, nil)

	records, err := api.inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	emptyOccupancy := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	listResponse := api.gpuInventoryListFromNodes(records, "", "", "", emptyOccupancy)
	if len(listResponse.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResponse.Items))
	}
	if listResponse.Items[0].Status != "available" {
		t.Fatalf("status = %q, want available (no store, no echo)", listResponse.Items[0].Status)
	}
}

// minimalRequestContext returns an empty app.RequestContext for directly
// invoking gpuNodeOccupancy (it only depends on middleware.GetTenantID).
func minimalRequestContext() *app.RequestContext {
	ctx := app.NewContext(0)
	return ctx
}

// newGPUInventoryAPIWithPodFetcher builds a gpuInventoryAPI whose
// gpuNodeOccupancy uses the injected podOccupancyFetcher instead of a real
// K8s client. Used to test the occupancy map construction logic without a
// running Kubernetes cluster.
func newGPUInventoryAPIWithPodFetcher(inventory ports.GPUInventory, pods []gpuPodOccupancy) *gpuInventoryAPI {
	api := newGPUInventoryAPIWithStore(inventory, nil, nil)
	api.podOccupancyFetcher = func(_ context.Context, _ string) []gpuPodOccupancy {
		return pods
	}
	return api
}

func TestGPUNodeOccupancyBuildsMapFromRunningInstances(t *testing.T) {
	pods := []gpuPodOccupancy{
		{InstanceName: "test-2", NodeName: "dev-phys-02", Phase: "Running"},
		{InstanceName: "test-dj", NodeName: "dev-phys-02", Phase: "Running"},
		// Non-running pod should be skipped.
		{InstanceName: "test-failed", NodeName: "dev-phys-03", Phase: "Pending"},
		// Pod with empty node should be skipped.
		{InstanceName: "test-pending", NodeName: "", Phase: "Running"},
		// Pod with empty instance name should be skipped.
		{InstanceName: "", NodeName: "dev-phys-03", Phase: "Running"},
	}
	api := newGPUInventoryAPIWithPodFetcher(newFakeGPUInventoryWithNode("dev-phys-02", 1), pods)

	occupancy := api.gpuNodeOccupancy(context.Background(), minimalRequestContext())
	// dev-phys-02 has 2 running pods; the lexicographically smallest instance
	// name wins (test-2 < test-dj).
	if entry, ok := occupancy.lookup("dev-phys-02"); !ok || entry.InstanceID != "test-2" {
		t.Fatalf("lookup(dev-phys-02) = %+v ok=%v, want test-2", entry, ok)
	}
	// dev-phys-03 only has a Pending pod and an empty-instance pod; both
	// skipped, so no entry.
	if _, ok := occupancy.lookup("dev-phys-03"); ok {
		t.Fatalf("lookup(dev-phys-03) should be absent (only Pending/empty pods)")
	}
	if _, ok := occupancy.lookup(""); ok {
		t.Fatalf("lookup(\"\") should be absent (empty nodeName)")
	}
}

func TestGPUNodeOccupancyWithNilFetcherAndNilClientReturnsEmpty(t *testing.T) {
	api := newGPUInventoryAPIWithStore(newFakeGPUInventoryWithNode("node-a", 1), nil, nil)
	occupancy := api.gpuNodeOccupancy(context.Background(), minimalRequestContext())
	if len(occupancy.entries) != 0 {
		t.Fatalf("occupancy.entries = %d, want 0 (nil fetcher and nil client)", len(occupancy.entries))
	}
}

func TestGPUNodeOccupancyPicksStableInstanceWhenMultipleOnSameNode(t *testing.T) {
	// Two running pods on the same node; the one with the smallest instance
	// name (lexicographic) should be kept for stability.
	pods := []gpuPodOccupancy{
		{InstanceName: "test-zzz", NodeName: "node-a", Phase: "Running"},
		{InstanceName: "test-aaa", NodeName: "node-a", Phase: "Running"},
	}
	api := newGPUInventoryAPIWithPodFetcher(newFakeGPUInventoryWithNode("node-a", 1), pods)

	occupancy := api.gpuNodeOccupancy(context.Background(), minimalRequestContext())
	entry, ok := occupancy.lookup("node-a")
	if !ok {
		t.Fatalf("lookup(node-a) not found")
	}
	if entry.InstanceID != "test-aaa" {
		t.Fatalf("instance_id = %q, want test-aaa (lexicographically smallest)", entry.InstanceID)
	}
}
