package runtime

import "testing"

// TestParseRunningGPUPodOccupancyFiltersNonGPUPods 锁定共享占用口径：
// 只有 ani-tenant-* 命名空间中真的请求 GPU 扩展资源的 Running Pod 才算占用。
// 这是 /gpu-inventory/occupancy 与 /platform/capacity 一致的唯一判定入口。
func TestParseRunningGPUPodOccupancyFiltersNonGPUPods(t *testing.T) {
	body := []byte(`{"items":[
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1","ani.kubercloud.io/instance":"gpu-a"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t2","labels":{"ani.kubercloud.io/tenant-id":"t2","ani.kubercloud.io/instance":"gpu-b"}},"spec":{"nodeName":"node-2","containers":[{"resources":{"limits":{"volcano.sh/vgpu-number":"1","volcano.sh/vgpu-memory":"1228"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"cpu":"2","memory":"4Gi"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-system","labels":{"ani.kubercloud.io/tenant-id":"t1","ani.kubercloud.io/instance":"platform-pod"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1","ani.kubercloud.io/instance":"pending-gpu"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Pending"}},
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1","ani.kubercloud.io/instance":"unscheduled"}},"spec":{"nodeName":"","containers":[{"resources":{"limits":{"nvidia.com/gpu":"1"}}}]},"status":{"phase":"Running"}}
	]}`)

	records, err := ParseRunningGPUPodOccupancy(body)
	if err != nil {
		t.Fatalf("ParseRunningGPUPodOccupancy() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d (%+v), want 2 (只保留真的请求 GPU 的租户 Running Pod)", len(records), records)
	}
	if records[0].TenantID != "t1" || records[0].InstanceName != "gpu-a" || records[0].NodeName != "node-1" {
		t.Fatalf("records[0] = %+v, want t1/gpu-a/node-1", records[0])
	}
	if records[1].TenantID != "t2" || records[1].InstanceName != "gpu-b" || records[1].NodeName != "node-2" {
		t.Fatalf("records[1] = %+v, want t2/gpu-b/node-2", records[1])
	}
}

// TestParseRunningGPUPodOccupancyAllowsMissingInstanceLabel 覆盖裸 GPU Pod
// （未打实例 label）——它仍占用设备，只是不参与 instance_id 回显。
func TestParseRunningGPUPodOccupancyAllowsMissingInstanceLabel(t *testing.T) {
	body := []byte(`{"items":[
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"nvidia.com/vgpu":"1"}}}]},"status":{"phase":"Running"}}
	]}`)

	records, err := ParseRunningGPUPodOccupancy(body)
	if err != nil {
		t.Fatalf("ParseRunningGPUPodOccupancy() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InstanceName != "" || records[0].NodeName != "node-1" {
		t.Fatalf("record = %+v, want empty instance name + node-1", records[0])
	}
}

func TestParseRunningGPUPodOccupancyHandlesEmptyAndInvalidBody(t *testing.T) {
	records, err := ParseRunningGPUPodOccupancy(nil)
	if err != nil || records != nil {
		t.Fatalf("empty body = (%v, %v), want (nil, nil)", records, err)
	}
	if _, err := ParseRunningGPUPodOccupancy([]byte("not json")); err == nil {
		t.Fatal("invalid body error = nil, want unmarshal error")
	}
}

func TestPodRequestsGPUReadsLimitsOnly(t *testing.T) {
	container := func(limits map[string]string) []kubernetesPodContainerSpec {
		spec := kubernetesPodContainerSpec{}
		spec.Resources.Limits = limits
		return []kubernetesPodContainerSpec{spec}
	}
	cases := []struct {
		name   string
		limits map[string]string
		want   bool
	}{
		{"whole card", map[string]string{"nvidia.com/gpu": "1"}, true},
		{"vgpu slice", map[string]string{"nvidia.com/vgpu": "1"}, true},
		{"volcano vgpu", map[string]string{"volcano.sh/vgpu-number": "1"}, true},
		{"cpu only", map[string]string{"cpu": "1", "memory": "1Gi"}, false},
		{"empty", map[string]string{}, false},
		{"gpu memory only is not a device request", map[string]string{"volcano.sh/vgpu-memory": "1228"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := podRequestsGPU(container(tc.limits)); got != tc.want {
				t.Fatalf("podRequestsGPU(%v) = %v, want %v", tc.limits, got, tc.want)
			}
		})
	}
}

// TestPodGPUCountSumsContainersAndFallsBackToOne 锁定 GPU 数量口径：
// 多卡 Pod（nvidia.com/gpu=2）占 2 台设备；多容器 limits 之和；vGPU Pod 按
// volcano.sh/vgpu-number；解析失败/缺失保守按 1（与旧「每 Pod 1 设备」一致）。
func TestPodGPUCountSumsContainersAndFallsBackToOne(t *testing.T) {
	spec := func(limits map[string]string) kubernetesPodContainerSpec {
		s := kubernetesPodContainerSpec{}
		s.Resources.Limits = limits
		return s
	}
	cases := []struct {
		name       string
		containers []kubernetesPodContainerSpec
		want       int
	}{
		{"multi-gpu whole card", []kubernetesPodContainerSpec{spec(map[string]string{"nvidia.com/gpu": "2"})}, 2},
		{"vgpu slice", []kubernetesPodContainerSpec{spec(map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "1228"})}, 1},
		{"multi-container sums", []kubernetesPodContainerSpec{
			spec(map[string]string{"nvidia.com/gpu": "1"}),
			spec(map[string]string{"nvidia.com/gpu": "2"}),
		}, 3},
		{"invalid value falls back to one", []kubernetesPodContainerSpec{spec(map[string]string{"nvidia.com/gpu": "abc"})}, 1},
		{"zero falls back to one", []kubernetesPodContainerSpec{spec(map[string]string{"nvidia.com/gpu": "0"})}, 1},
		{"no gpu limits falls back to one", []kubernetesPodContainerSpec{spec(map[string]string{"cpu": "1"})}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := podGPUCount(tc.containers); got != tc.want {
				t.Fatalf("podGPUCount = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestParseRunningGPUPodOccupancyCarriesGPUCount 端到端断言解析记录携带
// GPUCount：整卡 2 卡 Pod → 2，vGPU 切片 → 1。
func TestParseRunningGPUPodOccupancyCarriesGPUCount(t *testing.T) {
	body := []byte(`{"items":[
		{"metadata":{"namespace":"ani-tenant-t1","labels":{"ani.kubercloud.io/tenant-id":"t1","ani.kubercloud.io/instance":"multi-gpu"}},"spec":{"nodeName":"node-1","containers":[{"resources":{"limits":{"nvidia.com/gpu":"2"}}}]},"status":{"phase":"Running"}},
		{"metadata":{"namespace":"ani-tenant-t2","labels":{"ani.kubercloud.io/tenant-id":"t2","ani.kubercloud.io/instance":"vgpu-slice"}},"spec":{"nodeName":"node-2","containers":[{"resources":{"limits":{"volcano.sh/vgpu-number":"1","volcano.sh/vgpu-memory":"1228"}}}]},"status":{"phase":"Running"}}
	]}`)

	records, err := ParseRunningGPUPodOccupancy(body)
	if err != nil {
		t.Fatalf("ParseRunningGPUPodOccupancy() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].GPUCount != 2 {
		t.Fatalf("records[0].GPUCount = %d, want 2", records[0].GPUCount)
	}
	if records[1].GPUCount != 1 {
		t.Fatalf("records[1].GPUCount = %d, want 1", records[1].GPUCount)
	}
}
