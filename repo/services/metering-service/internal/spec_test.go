package internal

import (
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestBuildSpecGPUContainer(t *testing.T) {
	spec := buildSpec("tenant-1", "inst-1", "my-gpu-app", "gpu_container", 2)
	if spec.ResourceRef != "inst-1" {
		t.Errorf("ResourceRef = %q, want inst-1", spec.ResourceRef)
	}
	if spec.WorkloadName != "my-gpu-app" {
		t.Errorf("WorkloadName = %q, want my-gpu-app", spec.WorkloadName)
	}
	if spec.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want tenant-1", spec.TenantID)
	}
	if spec.WorkloadKind != "gpu_container" {
		t.Errorf("WorkloadKind = %q, want gpu_container", spec.WorkloadKind)
	}
	if len(spec.Dimensions) != 3 {
		t.Fatalf("Dimensions len = %d, want 3", len(spec.Dimensions))
	}
	if spec.Dimensions[0].ResourceType != ports.MeteringResourceInstanceGPUSeconds {
		t.Errorf("dim0 = %v, want GPU", spec.Dimensions[0].ResourceType)
	}
	if spec.Dimensions[1].ResourceType != ports.MeteringResourceInstanceCPUSeconds {
		t.Errorf("dim1 = %v, want CPU", spec.Dimensions[1].ResourceType)
	}
	if spec.Dimensions[2].ResourceType != ports.MeteringResourceInstanceMemorySeconds {
		t.Errorf("dim2 = %v, want Mem", spec.Dimensions[2].ResourceType)
	}
	if spec.IntervalSec != 60 {
		t.Errorf("IntervalSec = %d, want 60", spec.IntervalSec)
	}
	if spec.StartedAt.IsZero() {
		t.Error("StartedAt is zero, want time.Now()")
	}
	if spec.GPUSpec == nil {
		t.Fatal("GPUSpec is nil, want non-nil")
	}
	if spec.GPUSpec.Count != 2 {
		t.Errorf("GPUSpec.Count = %d, want 2", spec.GPUSpec.Count)
	}
}

func TestBuildSpecVM(t *testing.T) {
	spec := buildSpec("t-2", "inst-2", "my-vm", "vm", 0)
	if len(spec.Dimensions) != 2 {
		t.Fatalf("vm Dimensions len = %d, want 2", len(spec.Dimensions))
	}
	if spec.Dimensions[0].ResourceType != ports.MeteringResourceInstanceCPUSeconds {
		t.Errorf("vm dim0 = %v, want CPU", spec.Dimensions[0].ResourceType)
	}
	if spec.Dimensions[1].ResourceType != ports.MeteringResourceInstanceMemorySeconds {
		t.Errorf("vm dim1 = %v, want Mem", spec.Dimensions[1].ResourceType)
	}
	if spec.GPUSpec != nil {
		t.Errorf("vm GPUSpec = %v, want nil", spec.GPUSpec)
	}
}

func TestBuildSpecContainer(t *testing.T) {
	spec := buildSpec("t-3", "inst-3", "my-container", "container", 0)
	if len(spec.Dimensions) != 2 {
		t.Fatalf("container Dimensions len = %d, want 2", len(spec.Dimensions))
	}
	if spec.GPUSpec != nil {
		t.Errorf("container GPUSpec = %v, want nil", spec.GPUSpec)
	}
}

func TestBuildSpecUnknownKindDefaultsToCPUAndMem(t *testing.T) {
	spec := buildSpec("t-4", "inst-4", "my-unknown", "unknown_kind", 0)
	if len(spec.Dimensions) != 2 {
		t.Fatalf("unknown kind Dimensions len = %d, want 2", len(spec.Dimensions))
	}
	if spec.Dimensions[0].ResourceType != ports.MeteringResourceInstanceCPUSeconds {
		t.Errorf("unknown kind dim0 = %v, want CPU", spec.Dimensions[0].ResourceType)
	}
	if spec.Dimensions[1].ResourceType != ports.MeteringResourceInstanceMemorySeconds {
		t.Errorf("unknown kind dim1 = %v, want Mem", spec.Dimensions[1].ResourceType)
	}
}

func TestBuildSpecGPUContainerZeroCount(t *testing.T) {
	spec := buildSpec("t-5", "inst-5", "my-gpu-zero", "gpu_container", 0)
	if spec.GPUSpec != nil {
		t.Errorf("gpu_container with 0 count GPUSpec = %v, want nil", spec.GPUSpec)
	}
}

// TestBuildSpecConsumerCall 显式覆盖 consumer 调用契约：
// buildSpec(event.TenantID, event.InstanceID, event.Name, event.WorkloadKind, gpuCount)，
// gpuCount 从 event.GPUSpec.Count 提取（nil 则 0）。
func TestBuildSpecConsumerCall(t *testing.T) {
	event := ports.InstanceLifecycleEvent{
		InstanceID:   "inst-consumer",
		TenantID:     "tenant-consumer",
		Name:         "my-deployment",
		WorkloadKind: "gpu_container",
		GPUSpec:      &ports.GPUEventSpec{Count: 3},
	}
	gpuCount := 0
	if event.GPUSpec != nil {
		gpuCount = event.GPUSpec.Count
	}
	spec := buildSpec(event.TenantID, event.InstanceID, event.Name, event.WorkloadKind, gpuCount)
	if spec.ResourceRef != "inst-consumer" {
		t.Errorf("ResourceRef = %q, want inst-consumer", spec.ResourceRef)
	}
	if spec.WorkloadName != "my-deployment" {
		t.Errorf("WorkloadName = %q, want my-deployment", spec.WorkloadName)
	}
	if spec.TenantID != "tenant-consumer" {
		t.Errorf("TenantID = %q, want tenant-consumer", spec.TenantID)
	}
	if spec.GPUSpec == nil || spec.GPUSpec.Count != 3 {
		t.Errorf("GPUSpec = %v, want count=3", spec.GPUSpec)
	}
}

// TestBuildSpecConsumerCallNilGPUSpec 覆盖 consumer 收到 nil GPUSpec 的契约：gpuCount 提取为 0。
func TestBuildSpecConsumerCallNilGPUSpec(t *testing.T) {
	event := ports.InstanceLifecycleEvent{
		InstanceID:   "inst-nil-gpu",
		TenantID:     "tenant-x",
		Name:         "my-app",
		WorkloadKind: "container",
		GPUSpec:      nil,
	}
	gpuCount := 0
	if event.GPUSpec != nil {
		gpuCount = event.GPUSpec.Count
	}
	spec := buildSpec(event.TenantID, event.InstanceID, event.Name, event.WorkloadKind, gpuCount)
	if spec.GPUSpec != nil {
		t.Errorf("nil GPUSpec should produce nil spec.GPUSpec, got %v", spec.GPUSpec)
	}
}

// TestBuildSpecRebuilderCall 显式覆盖 rebuilder 调用契约：
// buildSpec(tenantID, instanceID, name, kind, gpuCount)，
// gpuCount 从 parseGPUCount(gpuStatusJSON) 提取。
func TestBuildSpecRebuilderCall(t *testing.T) {
	gpuStatusJSON := []byte(`{"count": 8}`)
	gpuCount := parseGPUCount(gpuStatusJSON)
	spec := buildSpec("tenant-rebuild", "inst-rebuild", "my-gpu-vm", "gpu_container", gpuCount)
	if spec.ResourceRef != "inst-rebuild" {
		t.Errorf("ResourceRef = %q, want inst-rebuild", spec.ResourceRef)
	}
	if spec.WorkloadName != "my-gpu-vm" {
		t.Errorf("WorkloadName = %q, want my-gpu-vm", spec.WorkloadName)
	}
	if spec.TenantID != "tenant-rebuild" {
		t.Errorf("TenantID = %q, want tenant-rebuild", spec.TenantID)
	}
	if spec.GPUSpec == nil || spec.GPUSpec.Count != 8 {
		t.Errorf("GPUSpec = %v, want count=8", spec.GPUSpec)
	}
}

// TestBuildSpecRebuilderCallMissingGPUCount 覆盖 rebuilder 解析缺失 gpu_status 的契约：gpuCount=0。
func TestBuildSpecRebuilderCallMissingGPUCount(t *testing.T) {
	gpuStatusJSON := []byte(`{}`)
	gpuCount := parseGPUCount(gpuStatusJSON)
	spec := buildSpec("t-r2", "inst-r2", "my-vm-2", "vm", gpuCount)
	if spec.GPUSpec != nil {
		t.Errorf("missing gpu_status should produce nil GPUSpec, got %v", spec.GPUSpec)
	}
}

func TestParseGPUCount(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"normal", []byte(`{"count": 4}`), 4},
		{"missing", []byte(`{}`), 0},
		{"null", []byte(`null`), 0},
		{"empty", []byte(``), 0},
		{"nil", nil, 0},
		{"malformed", []byte(`{"count":`), 0},
		{"negative_preserved", []byte(`{"count": -1}`), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseGPUCount(tc.in); got != tc.want {
				t.Errorf("parseGPUCount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
