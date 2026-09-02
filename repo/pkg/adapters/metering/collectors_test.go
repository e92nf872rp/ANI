package metering

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// makeSpec 构造测试用 CollectionSpec。
func makeSpec(tenantID, ref, name string, gpuCount int, interval int) ports.CollectionSpec {
	spec := ports.CollectionSpec{
		ResourceRef:  ref,
		WorkloadName: name,
		TenantID:     tenantID,
		WorkloadKind: "gpu_container",
		IntervalSec:  interval,
		StartedAt:    time.Now(),
		Dimensions: []ports.CollectionDimension{
			{ResourceType: ports.MeteringResourceInstanceGPUSeconds, Source: "dcgm_gpu"},
			{ResourceType: ports.MeteringResourceInstanceCPUSeconds, Source: "kubelet_cpu"},
			{ResourceType: ports.MeteringResourceInstanceMemorySeconds, Source: "kubelet_mem"},
		},
	}
	if gpuCount > 0 {
		spec.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}
	}
	return spec
}

// --- DCGMGPUCollector 测试 ---

func TestDCGMGPUCollector_GPUNilSkips(t *testing.T) {
	c := DCGMGPUCollector{}
	spec := ports.CollectionSpec{
		ResourceRef: "inst-1",
		TenantID:    "tenant-a",
		IntervalSec: 60,
	}
	// GPUSpec 为 nil 时应返回 nil
	records, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if records != nil {
		t.Fatalf("expected nil records when GPUSpec is nil, got %v", records)
	}
}

func TestDCGMGPUCollector_QuantityCalculation(t *testing.T) {
	c := DCGMGPUCollector{}
	spec := ports.CollectionSpec{
		ResourceRef: "inst-1",
		TenantID:    "tenant-a",
		IntervalSec: 60,
		GPUSpec:     &ports.GPUEventSpec{Count: 2},
	}
	records, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	// 2 张 GPU × 60s = 120 gpu_seconds
	if r.TotalQuantity != 120 {
		t.Errorf("expected quantity 120, got %f", r.TotalQuantity)
	}
	if r.Unit != "gpu_second" {
		t.Errorf("expected unit gpu_second, got %s", r.Unit)
	}
	if r.ResourceType != ports.MeteringResourceInstanceGPUSeconds {
		t.Errorf("expected resource type instance_gpu_seconds, got %s", r.ResourceType)
	}
	if r.Period != "2026-08-13T10:00" {
		t.Errorf("expected period 2026-08-13T10:00, got %s", r.Period)
	}
	if r.TenantID != "tenant-a" || r.ResourceRef != "inst-1" {
		t.Errorf("unexpected tenant/ref: %s/%s", r.TenantID, r.ResourceRef)
	}
}

func TestDCGMGPUCollector_FieldsPopulated(t *testing.T) {
	c := DCGMGPUCollector{}
	spec := ports.CollectionSpec{
		ResourceRef: "inst-gpu",
		TenantID:    "t-100",
		IntervalSec: 30,
		GPUSpec:     &ports.GPUEventSpec{Count: 4},
	}
	records, _ := c.Collect(context.Background(), spec, "2026-01-01T00:00")
	if len(records) != 1 {
		t.Fatalf("expected 1 record")
	}
	r := records[0]
	// 验证所有字段都被填充
	if r.TenantID == "" || r.ResourceRef == "" || r.ResourceType == "" || r.Unit == "" || r.Period == "" {
		t.Errorf("record has empty required field: %+v", r)
	}
	// 4 × 30 = 120
	if r.TotalQuantity != 120 {
		t.Errorf("expected 120, got %f", r.TotalQuantity)
	}
}

// --- KubeletCPUCollector 测试 ---

func newPromMockServer(t *testing.T, status string, resultType string, values [][]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		if query == "" {
			t.Errorf("missing query param")
		}
		resp := map[string]any{
			"status": status,
			"data": map[string]any{
				"resultType": resultType,
				"result":     []map[string]any{},
			},
		}
		if len(values) > 0 && status == "success" {
			results := make([]map[string]any, 0, len(values))
			for _, v := range values {
				results = append(results, map[string]any{
					"metric": map[string]string{},
					"value":  v,
				})
			}
			resp["data"] = map[string]any{
				"resultType": resultType,
				"result":     results,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestKubeletCPUCollector_QuantityCalculation(t *testing.T) {
	// rate 返回 0.5 核，60s 周期 → 0.5 × 60 = 30 cpu_seconds
	server := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "0.5"},
	})
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-cpu",
		TenantID:    "tenant-cpu",
		IntervalSec: 60,
	}
	records, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	if r.TotalQuantity != 30 {
		t.Errorf("expected quantity 30, got %f", r.TotalQuantity)
	}
	if r.Unit != "cpu_second" {
		t.Errorf("expected unit cpu_second, got %s", r.Unit)
	}
	if r.ResourceType != ports.MeteringResourceInstanceCPUSeconds {
		t.Errorf("expected resource type instance_cpu_seconds, got %s", r.ResourceType)
	}
}

func TestKubeletCPUCollector_PromError(t *testing.T) {
	// Prometheus 返回 error status
	server := newPromMockServer(t, "error", "vector", nil)
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-cpu",
		TenantID:    "tenant-cpu",
		IntervalSec: 60,
	}
	records, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if records != nil {
		t.Fatalf("expected nil records on error")
	}
	if !strings.Contains(err.Error(), "kubelet_cpu") {
		t.Errorf("expected error to contain 'kubelet_cpu' prefix, got: %v", err)
	}
}

func TestKubeletCPUCollector_EmptyResult(t *testing.T) {
	// Prometheus 返回成功但无数据
	server := newPromMockServer(t, "success", "vector", nil)
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-cpu",
		TenantID:    "tenant-cpu",
		IntervalSec: 60,
	}
	_, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err == nil {
		t.Fatalf("expected error for empty result")
	}
}

func TestKubeletCPUCollector_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-cpu",
		TenantID:    "tenant-cpu",
		IntervalSec: 60,
	}
	_, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err == nil {
		t.Fatalf("expected error on HTTP 500")
	}
}

func TestKubeletCPUCollector_FieldsPopulated(t *testing.T) {
	server := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1.0"},
	})
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-x",
		TenantID:    "t-x",
		IntervalSec: 60,
	}
	records, _ := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if len(records) != 1 {
		t.Fatalf("expected 1 record")
	}
	r := records[0]
	if r.TenantID == "" || r.ResourceRef == "" || r.ResourceType == "" || r.Unit == "" || r.Period == "" {
		t.Errorf("record has empty required field: %+v", r)
	}
}

// --- KubeletMemCollector 测试 ---

func TestKubeletMemCollector_QuantityCalculation(t *testing.T) {
	// 1 GiB = 1073741824 bytes，60s → 1 × 60 = 60 gib_seconds
	server := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1073741824"},
	})
	defer server.Close()

	c := NewKubeletMemCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-mem",
		TenantID:    "tenant-mem",
		IntervalSec: 60,
	}
	records, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	// 1073741824 / 1073741824 × 60 = 60
	if r.TotalQuantity != 60 {
		t.Errorf("expected quantity 60, got %f", r.TotalQuantity)
	}
	if r.Unit != "gib_second" {
		t.Errorf("expected unit gib_second, got %s", r.Unit)
	}
	if r.ResourceType != ports.MeteringResourceInstanceMemorySeconds {
		t.Errorf("expected resource type instance_memory_gib_seconds, got %s", r.ResourceType)
	}
}

func TestKubeletMemCollector_PromError(t *testing.T) {
	server := newPromMockServer(t, "error", "vector", nil)
	defer server.Close()

	c := NewKubeletMemCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-mem",
		TenantID:    "tenant-mem",
		IntervalSec: 60,
	}
	_, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "kubelet_mem") {
		t.Errorf("expected error to contain 'kubelet_mem' prefix, got: %v", err)
	}
}

func TestKubeletMemCollector_FieldsPopulated(t *testing.T) {
	server := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "5368709120"}, // 5 GiB
	})
	defer server.Close()

	c := NewKubeletMemCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-y",
		TenantID:    "t-y",
		IntervalSec: 60,
	}
	records, _ := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if len(records) != 1 {
		t.Fatalf("expected 1 record")
	}
	r := records[0]
	if r.TenantID == "" || r.ResourceRef == "" || r.ResourceType == "" || r.Unit == "" || r.Period == "" {
		t.Errorf("record has empty required field: %+v", r)
	}
}

// --- Resolve 测试 ---

func TestResolve_DCGMGPU(t *testing.T) {
	resetCollectors()
	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	c, ok := Resolve("dcgm_gpu")
	if !ok {
		t.Fatalf("expected dcgm_gpu to be registered")
	}
	if _, ok := c.(DCGMGPUCollector); !ok {
		t.Errorf("expected DCGMGPUCollector type")
	}
}

func TestResolve_UnknownSource(t *testing.T) {
	resetCollectors()
	_, ok := Resolve("unknown_source")
	if ok {
		t.Fatalf("expected unknown source to return false")
	}
}

func TestRegisterAndResolve_CPU(t *testing.T) {
	resetCollectors()
	// 注册一个 mock CPU collector
	mock := mockCollector{records: []ports.MeteringUsageRecord{{TenantID: "t"}}}
	RegisterCollector("kubelet_cpu", mock)

	c, ok := Resolve("kubelet_cpu")
	if !ok {
		t.Fatalf("expected kubelet_cpu to be registered")
	}
	records, _ := c.Collect(context.Background(), ports.CollectionSpec{}, "p")
	if len(records) != 1 || records[0].TenantID != "t" {
		t.Errorf("unexpected records from mock collector")
	}
}

// --- CollectAll 测试 ---

func TestCollectAll_AllDimensions(t *testing.T) {
	// 为 CPU 和 Mem 注册 mock server backed collector
	cpuServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "0.5"}, // 0.5 核 → 30 cpu_seconds
	})
	defer cpuServer.Close()
	memServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1073741824"}, // 1 GiB → 60 gib_seconds
	})
	defer memServer.Close()

	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	RegisterCollector("kubelet_cpu", NewKubeletCPUCollector(cpuServer.URL, nil))
	RegisterCollector("kubelet_mem", NewKubeletMemCollector(memServer.URL, nil))
	defer resetCollectors()

	spec := makeSpec("tenant-all", "inst-all", "demo-app", 2, 60)
	records, err := CollectAll(context.Background(), spec, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 维度 → 3 条记录（GPU 2×60=120, CPU 0.5×60=30, Mem 1×60=60）
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d: %+v", len(records), records)
	}
	// 按 ResourceType 验证
	byType := map[ports.MeteringResourceType]float64{}
	for _, r := range records {
		byType[r.ResourceType] = r.TotalQuantity
	}
	if byType[ports.MeteringResourceInstanceGPUSeconds] != 120 {
		t.Errorf("GPU: expected 120, got %f", byType[ports.MeteringResourceInstanceGPUSeconds])
	}
	if byType[ports.MeteringResourceInstanceCPUSeconds] != 30 {
		t.Errorf("CPU: expected 30, got %f", byType[ports.MeteringResourceInstanceCPUSeconds])
	}
	if byType[ports.MeteringResourceInstanceMemorySeconds] != 60 {
		t.Errorf("Mem: expected 60, got %f", byType[ports.MeteringResourceInstanceMemorySeconds])
	}
}

func TestCollectAll_GPUNilSkips(t *testing.T) {
	cpuServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1.0"},
	})
	defer cpuServer.Close()
	memServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "5368709120"},
	})
	defer memServer.Close()

	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	RegisterCollector("kubelet_cpu", NewKubeletCPUCollector(cpuServer.URL, nil))
	RegisterCollector("kubelet_mem", NewKubeletMemCollector(memServer.URL, nil))
	defer resetCollectors()

	// gpuCount=0 → GPUSpec=nil，GPU 维度应跳过
	spec := makeSpec("tenant-no-gpu", "inst-no-gpu", "demo-no-gpu", 0, 60)
	records, err := CollectAll(context.Background(), spec, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// GPU 跳过 → 2 条记录（CPU + Mem）
	if len(records) != 2 {
		t.Fatalf("expected 2 records (no GPU), got %d", len(records))
	}
	for _, r := range records {
		if r.ResourceType == ports.MeteringResourceInstanceGPUSeconds {
			t.Errorf("GPU record should not exist when GPUSpec is nil")
		}
	}
}

func TestCollectAll_UnknownSourceSkips(t *testing.T) {
	// 只注册 GPU，CPU/Mem 不注册 → 应 Warn 跳过
	resetCollectors()
	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	spec := makeSpec("t", "inst", "demo-app", 2, 60)
	records, err := CollectAll(context.Background(), spec, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 只有 GPU 维度产出 1 条记录
	if len(records) != 1 {
		t.Fatalf("expected 1 record (GPU only), got %d", len(records))
	}
	if records[0].ResourceType != ports.MeteringResourceInstanceGPUSeconds {
		t.Errorf("expected GPU record, got %s", records[0].ResourceType)
	}
}

func TestCollectAll_SingleDimFailureSkips(t *testing.T) {
	// CPU 返回错误，GPU 和 Mem 正常
	memServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1073741824"},
	})
	defer memServer.Close()

	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	RegisterCollector("kubelet_cpu", mockCollector{err: fmt.Errorf("mock cpu fail")})
	RegisterCollector("kubelet_mem", NewKubeletMemCollector(memServer.URL, nil))
	defer resetCollectors()

	spec := makeSpec("t", "inst", "demo-app", 1, 60)
	records, err := CollectAll(context.Background(), spec, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CPU 失败跳过 → 2 条记录（GPU + Mem）
	if len(records) != 2 {
		t.Fatalf("expected 2 records (CPU failed), got %d", len(records))
	}
	for _, r := range records {
		if r.ResourceType == ports.MeteringResourceInstanceCPUSeconds {
			t.Errorf("CPU record should not exist on failure")
		}
	}
}

func TestCollectAll_PeriodMinuteAligned(t *testing.T) {
	resetCollectors()
	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	spec := makeSpec("t", "inst", "demo-app", 1, 60)
	records, _ := CollectAll(context.Background(), spec, slog.Default())
	if len(records) == 0 {
		t.Fatalf("expected at least 1 record")
	}
	// Period 应为分钟对齐格式 "2006-01-02T15:04"（UTC，与查询侧时区契约一致）
	now := time.Now().UTC().Format("2006-01-02T15:04")
	for _, r := range records {
		if r.Period != now {
			t.Errorf("expected period %s, got %s", now, r.Period)
		}
	}
}

// --- 辅助类型和函数 ---

type mockCollector struct {
	records []ports.MeteringUsageRecord
	err     error
}

func (m mockCollector) Collect(_ context.Context, _ ports.CollectionSpec, _ string) ([]ports.MeteringUsageRecord, error) {
	return m.records, m.err
}

// resetCollectors 清空全局 collector registry。
// init() 已移除，collector 注册统一由 RegisterAll 在 main.go 调用，
// 测试中需要手动注册所需 collector。
func resetCollectors() {
	collectorMu.Lock()
	defer collectorMu.Unlock()
	for k := range collectorCache {
		delete(collectorCache, k)
	}
}

// --- PromQL 查询验证测试 ---

func TestKubeletCPUCollector_PromQLContainsRate(t *testing.T) {
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		resp := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{
						"metric": map[string]string{},
						"value":  []any{1718294400, "0.1"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewKubeletCPUCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-plq",
		TenantID:    "tenant-plq",
		IntervalSec: 60,
	}
	_, _ = c.Collect(context.Background(), spec, "2026-08-13T10:00")

	if !strings.Contains(capturedQuery, "rate(") {
		t.Errorf("expected query to contain rate(), got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "container_cpu_usage_seconds_total") {
		t.Errorf("expected query to contain container_cpu_usage_seconds_total, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "[60s]") {
		t.Errorf("expected query to contain [60s] interval, got: %s", capturedQuery)
	}
}

func TestKubeletMemCollector_PromQLContainsMemMetric(t *testing.T) {
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		resp := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{
						"metric": map[string]string{},
						"value":  []any{1718294400, "1024"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewKubeletMemCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-plq",
		TenantID:    "tenant-plq",
		IntervalSec: 60,
	}
	_, _ = c.Collect(context.Background(), spec, "2026-08-13T10:00")

	if !strings.Contains(capturedQuery, "container_memory_working_set_bytes") {
		t.Errorf("expected query to contain container_memory_working_set_bytes, got: %s", capturedQuery)
	}
}

func TestKubeletMemCollector_NaNValue(t *testing.T) {
	server := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "NaN"},
	})
	defer server.Close()

	c := NewKubeletMemCollector(server.URL, nil)
	spec := ports.CollectionSpec{
		ResourceRef: "inst-nan",
		TenantID:    "t-nan",
		IntervalSec: 60,
	}
	_, err := c.Collect(context.Background(), spec, "2026-08-13T10:00")
	if err == nil {
		t.Fatalf("expected error for NaN value")
	}
}

func TestCollectAll_AllFieldsPopulated(t *testing.T) {
	cpuServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1.0"},
	})
	defer cpuServer.Close()
	memServer := newPromMockServer(t, "success", "vector", [][]any{
		{1718294400, "1073741824"},
	})
	defer memServer.Close()

	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	RegisterCollector("kubelet_cpu", NewKubeletCPUCollector(cpuServer.URL, nil))
	RegisterCollector("kubelet_mem", NewKubeletMemCollector(memServer.URL, nil))
	defer resetCollectors()

	spec := makeSpec("tenant-fields", "inst-fields", "demo-fields", 3, 60)
	records, _ := CollectAll(context.Background(), spec, slog.Default())

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	for i, r := range records {
		if r.TenantID == "" {
			t.Errorf("record %d: TenantID empty", i)
		}
		if r.ResourceRef == "" {
			t.Errorf("record %d: ResourceRef empty", i)
		}
		if r.ResourceType == "" {
			t.Errorf("record %d: ResourceType empty", i)
		}
		if r.TotalQuantity == 0 {
			t.Errorf("record %d: TotalQuantity is 0", i)
		}
		if r.Unit == "" {
			t.Errorf("record %d: Unit empty", i)
		}
		if r.Period == "" {
			t.Errorf("record %d: Period empty", i)
		}
	}
}

func TestCollectAll_NilLogger(t *testing.T) {
	resetCollectors()
	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	spec := makeSpec("t", "inst", "demo-app", 1, 60)
	// nil logger 不应 panic
	records, err := CollectAll(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
}
