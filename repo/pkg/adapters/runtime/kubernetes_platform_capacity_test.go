package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// platformCapacityFakeGPUInventory 固定返回可预测的 GPU 节点清单：
// gpu-node-a Ready 2 设备（zone az-a）、gpu-node-b Ready 1 设备（zone az-b）、
// gpu-node-c NotReady 1 设备（fault）。
// gpuNodeADevices 覆盖 gpu-node-a 的设备数（>0 时生效），供需要在
// 非截断场景下断言 in_use 的用例使用。
type platformCapacityFakeGPUInventory struct {
	fail            bool
	gpuNodeADevices int
}

func (f *platformCapacityFakeGPUInventory) ListNodeClasses(ctx context.Context, filter ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	if f.fail {
		return nil, errors.New("inventory unavailable")
	}
	nodeADevices := f.gpuNodeADevices
	if nodeADevices <= 0 {
		nodeADevices = 2
	}
	return []ports.GPUNodeClass{
		{
			NodeName:    "gpu-node-a",
			Ready:       true,
			Labels:      map[string]string{"topology.kubernetes.io/zone": "az-a"},
			Devices:     make([]ports.GPUDeviceClass, nodeADevices),
			Allocatable: map[string]string{"cpu": "31.5", "memory": "120Gi"},
		},
		{
			NodeName:    "gpu-node-b",
			Ready:       true,
			Labels:      map[string]string{"failure-domain.beta.kubernetes.io/zone": "az-b"},
			Devices:     []ports.GPUDeviceClass{{Model: "L40S"}},
			Allocatable: map[string]string{"cpu": "16", "memory": "128Mi"},
		},
		{
			NodeName: "gpu-node-c",
			Ready:    false,
			Labels:   map[string]string{"topology.kubernetes.io/zone": "az-c"},
			Devices:  []ports.GPUDeviceClass{{Model: "L40S"}},
		},
	}, nil
}

func (f *platformCapacityFakeGPUInventory) GetNodeClass(ctx context.Context, nodeName string) (ports.GPUNodeClass, error) {
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

func (f *platformCapacityFakeGPUInventory) PlanScheduling(ctx context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	return ports.GPUSchedulingDecision{}, ports.ErrUnsupported
}

func (f *platformCapacityFakeGPUInventory) ListSpecAvailability(ctx context.Context, tenantID string) ([]ports.GPUSpecAvailability, error) {
	return nil, nil
}

// platformCapacityPodsRoundTripper 拦截集群级 pods 请求，返回跨租户 Pod 列表。
type platformCapacityPodsRoundTripper struct {
	statusCode int
	body       string
	requested  *string
}

func (r *platformCapacityPodsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.requested != nil {
		*r.requested = req.URL.String()
	}
	status := r.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     http.Header{},
	}, nil
}

func newPlatformCapacityTestClient(t *testing.T, rt http.RoundTripper) *KubernetesRESTClient {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:       "https://kubernetes.test",
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	return client
}

func TestKubernetesPlatformCapacityOverview(t *testing.T) {
	requested := ""
	rt := &platformCapacityPodsRoundTripper{requested: &requested, body: `{"items":[
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"volcano.sh/vgpu-number":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t2"},"spec":{"nodeName":"gpu-node-b","containers":[{"resources":{"limits":{"nvidia.com/vgpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t2"},"spec":{"nodeName":"gpu-node-b","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Pending"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-c","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}}
	]}`}
	service := NewKubernetesPlatformCapacityService(
		&platformCapacityFakeGPUInventory{},
		newPlatformCapacityTestClient(t, rt),
		&platformCapacityFakeTenantService{tenants: []ports.TenantSummary{{ID: "t1"}, {ID: "t2"}}},
	)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	// 存在性 label selector（不带值）覆盖全部租户 namespace。
	if !strings.Contains(requested, "/api/v1/pods?labelSelector=ani.kubercloud.io%2Ftenant-id") {
		t.Fatalf("pods endpoint = %q, want cluster-level tenant-label existence selector", requested)
	}
	region := overview.Regions[0]
	// gpu_total=4（2+1+1，含 NotReady 节点设备）、fault=1、nodes=2；
	// in_use = min(2, gpu-node-a 2 Pods) + min(1, gpu-node-b 1 Running Pod) = 3；
	// gpu_free = 4 - 3 - 1 = 0。
	if region.Capacity.GPUTotal != 4 || region.Capacity.Nodes != 2 {
		t.Fatalf("capacity = %+v, want gpu_total=4 nodes=2", region.Capacity)
	}
	if region.Capacity.GPUFree != 0 {
		t.Fatalf("gpu_free = %d, want 0", region.Capacity.GPUFree)
	}
	if region.Capacity.CPUCores != 47 || region.Capacity.MemoryGiB != 120 {
		t.Fatalf("cpu/memory = %d/%d, want 47 (31.5+16 向下取整)/120 (120Gi+128Mi 向下取整 GiB)", region.Capacity.CPUCores, region.Capacity.MemoryGiB)
	}
	if len(region.AZs) != 2 || region.AZs[0] != "az-a" || region.AZs[1] != "az-b" {
		t.Fatalf("azs = %v, want [az-a az-b]（Ready 节点 zone 去重）", region.AZs)
	}
	if region.TenantCount != 2 {
		t.Fatalf("tenant_count = %d, want 2", region.TenantCount)
	}
	if !overview.DevProfile.RealProvider || overview.DevProfile.Mode != "real" {
		t.Fatalf("dev_profile = %+v, want real provider", overview.DevProfile)
	}
}

// TestKubernetesPlatformCapacityExcludesNonGPUPods 锁定占用口径：只有
// ani-tenant-* 命名空间中真的请求 GPU 扩展资源的 Running Pod 才算 in_use。
// 节点设备数远大于 Pod 数，确保断言不被 min(设备数, Pod 数) 截断掩盖。
func TestKubernetesPlatformCapacityExcludesNonGPUPods(t *testing.T) {
	rt := &platformCapacityPodsRoundTripper{body: `{"items":[
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"volcano.sh/vgpu-number":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"cpu":"2","memory":"4Gi"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"cpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"cpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-system"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}}
	]}`}
	service := NewKubernetesPlatformCapacityService(
		&platformCapacityFakeGPUInventory{gpuNodeADevices: 8},
		newPlatformCapacityTestClient(t, rt),
		nil,
	)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	region := overview.Regions[0]
	// gpu_total = 8（node-a）+ 1（node-b）+ 1（node-c） = 10，fault = 1（node-c NotReady）。
	// 仅 2 个 GPU Pod 计入 in_use：3 个 CPU-only 租户 Pod（VM/探针/作业形态）
	// 与 1 个平台命名空间 Pod 都被排除；若被计入则 in_use=min(8,3)=3。
	// gpu_free = 10 - 2 - 1 = 7。
	if region.Capacity.GPUTotal != 10 || region.Capacity.GPUFree != 7 {
		t.Fatalf("capacity = %+v, want gpu_total=10 gpu_free=7 (只统计真的请求 GPU 的租户 Pod)", region.Capacity)
	}
}

func TestKubernetesPlatformCapacityPodCountTruncatedByDevices(t *testing.T) {
	rt := &platformCapacityPodsRoundTripper{body: `{"items":[
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1"},"spec":{"nodeName":"gpu-node-a","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}}
	]}`}
	service := NewKubernetesPlatformCapacityService(
		&platformCapacityFakeGPUInventory{},
		newPlatformCapacityTestClient(t, rt),
		nil,
	)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	region := overview.Regions[0]
	// in_use = min(2 设备, 4 Pods) = 2；gpu_total=4（含 NotReady 1 设备），
	// fault=1，nodes=2 → gpu_free = 4 - 2 - 1 = 1。
	if region.Capacity.GPUFree != 1 || region.Capacity.GPUTotal != 4 || region.Capacity.Nodes != 2 {
		t.Fatalf("capacity = %+v, want in_use truncated by device count", region.Capacity)
	}
}

func TestKubernetesPlatformCapacityDegradesWithoutK8sClient(t *testing.T) {
	service := NewKubernetesPlatformCapacityService(&platformCapacityFakeGPUInventory{}, nil, nil)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v, want degraded 200 semantics", err)
	}
	region := overview.Regions[0]
	// gpu_total=4（含 NotReady 1 设备）、in_use=0、fault=1 → gpu_free=3。
	if region.Capacity.GPUTotal != 4 || region.Capacity.GPUFree != 3 || region.Capacity.Nodes != 2 {
		t.Fatalf("capacity = %+v, want gpu_total=4 gpu_free=3 nodes=2 (in_use=0 无 k8sClient)", region.Capacity)
	}
	if overview.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real_provider=false 降级标记", overview.DevProfile)
	}
	if !strings.Contains(overview.DevProfile.Reason, "tenant service not configured") {
		t.Fatalf("reason = %q, want tenant degrade note", overview.DevProfile.Reason)
	}
}

func TestKubernetesPlatformCapacitySingleSourceFailureDoesNotBlock200(t *testing.T) {
	rt := &platformCapacityPodsRoundTripper{statusCode: http.StatusForbidden}
	service := NewKubernetesPlatformCapacityService(
		&platformCapacityFakeGPUInventory{fail: true},
		newPlatformCapacityTestClient(t, rt),
		&platformCapacityFakeTenantService{err: errors.New("db unavailable")},
	)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v, want 200 with degraded fields", err)
	}
	region := overview.Regions[0]
	if region.Capacity.GPUTotal != 0 || region.Capacity.Nodes != 0 || region.TenantCount != 0 {
		t.Fatalf("capacity = %+v, want zeroed fields on source failures", region.Capacity)
	}
	if overview.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real_provider=false", overview.DevProfile)
	}
	reason := overview.DevProfile.Reason
	// inventory 失败时 in_use 统计随 inventory 分支跳过，不产生 pods 查询。
	if !strings.Contains(reason, "gpu inventory list failed") ||
		!strings.Contains(reason, "tenant list failed") {
		t.Fatalf("reason = %q, want inventory + tenant degrade reasons", reason)
	}
}

func TestPlatformCapacityZoneLabelFallback(t *testing.T) {
	if got := platformCapacityZoneOf(map[string]string{"topology.kubernetes.io/zone": "az-1"}); got != "az-1" {
		t.Fatalf("zone = %q, want az-1 (topology label 优先)", got)
	}
	if got := platformCapacityZoneOf(map[string]string{"failure-domain.beta.kubernetes.io/zone": "az-2"}); got != "az-2" {
		t.Fatalf("zone = %q, want az-2 (legacy label 回退)", got)
	}
	if got := platformCapacityZoneOf(map[string]string{}); got != "" {
		t.Fatalf("zone = %q, want empty（缺失不报错）", got)
	}
}
