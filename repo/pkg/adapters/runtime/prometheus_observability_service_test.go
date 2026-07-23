package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeInstanceLookup 模拟实例记录查询，返回预设的 WorkloadInstanceRecord。
type fakeInstanceLookup struct {
	record ports.WorkloadInstanceRecord
	err    error
}

func (f *fakeInstanceLookup) Get(_ context.Context, _ ports.WorkloadInstanceGetRequest) (ports.WorkloadInstanceRecord, error) {
	return f.record, f.err
}

// newTestPrometheusObservabilityService 创建带 mock Prometheus HTTP server 的测试实例。
func newTestPrometheusObservabilityService(t *testing.T, handler http.HandlerFunc, lookup InstanceLookup) *PrometheusObservabilityService {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	service, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  server.URL,
		InstanceLookup: lookup,
		Now:            func() time.Time { return time.Unix(1780000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewPrometheusObservabilityService error = %v", err)
	}
	return service
}

func TestPrometheusObservabilityServiceRewritePromQLLabels(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-kjs-container-6",
	}}
	service, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "http://prometheus:9090",
		InstanceLookup: lookup,
	})
	if err != nil {
		t.Fatalf("NewPrometheusObservabilityService error = %v", err)
	}

	// 前端把 instance_id 同时注入 namespace 和 pod 占位符
	input := `100 * (container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"} / container_spec_memory_limit_bytes{namespace="inst_1",pod="inst_1"})`
	rewritten, err := service.rewritePromQLLabels(context.Background(), "00000000-0000-0000-0000-000000000001", input)
	if err != nil {
		t.Fatalf("rewritePromQLLabels error = %v", err)
	}

	// namespace 应被重写为真实 namespace（精确匹配），pod 应被重写为正则
	if !strings.Contains(rewritten, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("namespace not rewritten: %s", rewritten)
	}
	if !strings.Contains(rewritten, `pod=~"^test-kjs-container-6(-.*)?$"`) {
		t.Fatalf("pod not rewritten to regex: %s", rewritten)
	}
	// 原始精确匹配不应残留
	if strings.Contains(rewritten, `namespace="inst_1"`) {
		t.Fatalf("original namespace label still present: %s", rewritten)
	}
	if strings.Contains(rewritten, `pod="inst_1"`) {
		t.Fatalf("original pod label still present: %s", rewritten)
	}
}

// TestPrometheusObservabilityServiceRewritePromQLLabelsNameLabel 验证 VM 指标的 name label
// 被重写为 record.Name 的精确匹配（非正则）。VM 模板用 name="{{instance_id}}" 占位符，
// 后端 rewritePromQLLabels 把 instance_id 查到的 record.Name 用精确匹配注入。
func TestPrometheusObservabilityServiceRewritePromQLLabelsNameLabel(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-vm-aaa",
	}}
	service, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "http://prometheus:9090",
		InstanceLookup: lookup,
	})
	if err != nil {
		t.Fatalf("NewPrometheusObservabilityService error = %v", err)
	}

	// VM PromQL 模板：namespace 和 name 都用 instance_id 占位符
	input := `rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="inst_1",name="inst_1"}[5m])`
	rewritten, err := service.rewritePromQLLabels(context.Background(), "00000000-0000-0000-0000-000000000001", input)
	if err != nil {
		t.Fatalf("rewritePromQLLabels error = %v", err)
	}

	// namespace 应被重写为真实 namespace（精确匹配）
	if !strings.Contains(rewritten, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("namespace not rewritten: %s", rewritten)
	}
	// name 应被重写为 record.Name 的精确匹配（=，非 =~）
	if !strings.Contains(rewritten, `name="test-vm-aaa"`) {
		t.Fatalf("name not rewritten to exact match: %s", rewritten)
	}
	// name 不应使用正则匹配
	if strings.Contains(rewritten, `name=~"`) {
		t.Fatalf("name should not use regex match: %s", rewritten)
	}
	// 原始 instance_id label 值不应残留
	if strings.Contains(rewritten, `name="inst_1"`) {
		t.Fatalf("original name label still present: %s", rewritten)
	}
}

// TestPrometheusObservabilityServiceRewritePromQLLabelsNameLabelMemoryTemplate 验证 VM 内存模板
// 含多个 name label 选择器时全部被正确重写为 record.Name 精确匹配。
func TestPrometheusObservabilityServiceRewritePromQLLabelsNameLabelMemoryTemplate(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-vm-bbb",
	}}
	service, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "http://prometheus:9090",
		InstanceLookup: lookup,
	})
	if err != nil {
		t.Fatalf("NewPrometheusObservabilityService error = %v", err)
	}

	// VM 内存模板含 3 个 name label 选择器
	input := `(kubevirt_vmi_memory_domain_bytes{namespace="inst_1",name="inst_1"} - kubevirt_vmi_memory_usable_bytes{namespace="inst_1",name="inst_1"}) / kubevirt_vmi_memory_domain_bytes{namespace="inst_1",name="inst_1"}`
	rewritten, err := service.rewritePromQLLabels(context.Background(), "00000000-0000-0000-0000-000000000001", input)
	if err != nil {
		t.Fatalf("rewritePromQLLabels error = %v", err)
	}

	// 所有 3 个 name label 都应被重写为 record.Name 精确匹配
	count := strings.Count(rewritten, `name="test-vm-bbb"`)
	if count != 3 {
		t.Fatalf("expected 3 name labels rewritten, got %d: %s", count, rewritten)
	}
	// 所有 3 个 namespace label 都应被重写为真实 namespace
	nsCount := strings.Count(rewritten, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`)
	if nsCount != 3 {
		t.Fatalf("expected 3 namespace labels rewritten, got %d: %s", nsCount, rewritten)
	}
	// 原始 instance_id 值不应残留
	if strings.Contains(rewritten, `name="inst_1"`) {
		t.Fatalf("original name label still present: %s", rewritten)
	}
}

// TestPrometheusObservabilityServiceRewritePromQLLabelsContainerPodNotRegress 验证
// container/gpu_container 的 pod label 重写在新增 name label 支持后不回归。
func TestPrometheusObservabilityServiceRewritePromQLLabelsContainerPodNotRegress(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-container-6",
	}}
	service, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "http://prometheus:9090",
		InstanceLookup: lookup,
	})
	if err != nil {
		t.Fatalf("NewPrometheusObservabilityService error = %v", err)
	}

	// container 模板含 namespace 和 pod label（不含 name label）
	input := `sum(rate(container_cpu_usage_seconds_total{namespace="inst_1",pod="inst_1"}[5m]))`
	rewritten, err := service.rewritePromQLLabels(context.Background(), "00000000-0000-0000-0000-000000000001", input)
	if err != nil {
		t.Fatalf("rewritePromQLLabels error = %v", err)
	}

	// pod 仍应被重写为正则匹配（=~），不受 name label 新增影响
	if !strings.Contains(rewritten, `pod=~"^test-container-6(-.*)?$"`) {
		t.Fatalf("pod not rewritten to regex (regression): %s", rewritten)
	}
	// namespace 仍应被重写为精确匹配
	if !strings.Contains(rewritten, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("namespace not rewritten (regression): %s", rewritten)
	}
	// 不应意外引入 name label
	if strings.Contains(rewritten, `name=`) {
		t.Fatalf("unexpected name label in container query: %s", rewritten)
	}
}

// TestPrometheusObservabilityServiceQueryVMForwardsToPrometheus 验证 VM 模板的 name label
// 在 Query 端到端流程中被正确重写后转发到 Prometheus。
func TestPrometheusObservabilityServiceQueryVMForwardsToPrometheus(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-vm-ccc",
	}}
	var capturedQuery string
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{
						"value": []any{float64(1780000000), "12.5"},
					},
				},
			},
		})
	}, lookup)

	result, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="inst_1",name="inst_1"}[5m])`,
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	// 验证转发到 Prometheus 的查询已重写 name label 为精确匹配
	if !strings.Contains(capturedQuery, `name="test-vm-ccc"`) {
		t.Fatalf("forwarded query name not rewritten to exact match: %s", capturedQuery)
	}
	if strings.Contains(capturedQuery, `name=~"`) {
		t.Fatalf("forwarded query name should not use regex match: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("forwarded query namespace not rewritten: %s", capturedQuery)
	}
	// 验证返回结果
	if len(result.Results) != 1 || result.Results[0].Value != 12.5 {
		t.Fatalf("result = %+v, want single sample value 12.5", result.Results)
	}
	if !result.DevProfile.RealProvider {
		t.Fatalf("dev_profile.real_provider = false, want true for successful query")
	}
}

func TestPrometheusObservabilityServiceQueryForwardsToPrometheus(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-kjs-container-6",
	}}
	var capturedQuery string
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{
						"value": []any{float64(1780000000), "42.5"},
					},
				},
			},
		})
	}, lookup)

	result, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"}`,
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	// 验证转发到 Prometheus 的查询已重写 label
	if !strings.Contains(capturedQuery, `namespace="ani-tenant-00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("forwarded query namespace not rewritten: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, `pod=~"^test-kjs-container-6(-.*)?$"`) {
		t.Fatalf("forwarded query pod not rewritten: %s", capturedQuery)
	}
	// 验证返回结果
	if len(result.Results) != 1 || result.Results[0].Value != 42.5 {
		t.Fatalf("result = %+v, want single sample value 42.5", result.Results)
	}
	if result.DevProfile.Provider != "prometheus-observability-service" || !result.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real prometheus provider", result.DevProfile)
	}
}

func TestPrometheusObservabilityServiceQueryDegradesOnLookupFailure(t *testing.T) {
	lookup := &fakeInstanceLookup{err: fmt.Errorf("%w: instance not found", ports.ErrNotFound)}
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("Prometheus should not be called on lookup failure")
	}, lookup)

	result, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "tenant-a",
		Query:    `container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"}`,
	})
	if err != nil {
		t.Fatalf("Query error = %v, err should degrade to empty result not error", err)
	}
	if len(result.Results) != 0 {
		t.Fatalf("result should be empty on lookup failure, got %+v", result.Results)
	}
}

func TestPrometheusObservabilityServiceQueryNoLabelsPassesThrough(t *testing.T) {
	lookup := &fakeInstanceLookup{}
	var capturedQuery string
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result":     []map[string]any{},
			},
		})
	}, lookup)

	// PromQL 不含 namespace/pod label，应原样转发不查实例记录
	_, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "tenant-a",
		Query:    "up",
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if capturedQuery != "up" {
		t.Fatalf("forwarded query = %q, want %q", capturedQuery, "up")
	}
}

func TestPrometheusObservabilityServiceQueryInfValueDegradesGracefully(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-kjs-container-6",
	}}
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, _ *http.Request) {
		// 模拟内存利用率 used/limit 当 limit=0 时 Prometheus 返回 +Inf
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"value": []any{float64(1780000000), "+Inf"}},
				},
			},
		})
	}, lookup)

	result, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `100 * (container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"} / container_spec_memory_limit_bytes{namespace="inst_1",pod="inst_1"})`,
	})
	if err != nil {
		t.Fatalf("Query error = %v, should degrade to empty result not error", err)
	}
	// +Inf 被过滤后结果应为空，而非触发 JSON 序列化 panic
	if len(result.Results) != 0 {
		t.Fatalf("results should be empty for +Inf value, got %+v", result.Results)
	}
}

func TestPrometheusObservabilityServiceAlertRuleDelegatesToLocal(t *testing.T) {
	lookup := &fakeInstanceLookup{}
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("Prometheus should not be called for alert rules")
	}, lookup)

	rule, err := service.CreateAlertRule(context.Background(), ports.ObservabilityAlertRuleCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "obs-alert-create",
		Name:           "High GPU",
		PromQL:         "avg(DCGM_FI_DEV_GPU_UTIL) > 80",
		Severity:       ports.ObservabilityAlertSeverityWarning,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule error = %v", err)
	}
	if rule.RuleID == "" || rule.State != "active" {
		t.Fatalf("rule = %+v, want active rule", rule)
	}
}

func TestNewPrometheusObservabilityServiceRequiresConfig(t *testing.T) {
	if _, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "",
		InstanceLookup: &fakeInstanceLookup{},
	}); err == nil {
		t.Fatal("expected error for empty prometheus_url")
	}
	// nil InstanceLookup 允许构造（延迟注入场景：Gateway 启动时 demo instance
	// store 尚未创建，router 注册后通过 SetInstanceLookup 注入）。
	svc, err := NewPrometheusObservabilityService(PrometheusObservabilityServiceConfig{
		PrometheusURL:  "http://prometheus:9090",
		InstanceLookup: nil,
	})
	if err != nil {
		t.Fatalf("unexpected error for nil instance_lookup: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service for nil instance_lookup")
	}
	// QueryRange 在 lookup 未注入时应返回空结果而非 panic。
	result, err := svc.QueryRange(context.Background(), ports.ObservabilityRangeQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `100 * avg(rate(container_cpu_usage_seconds_total{namespace="inst_1",pod="inst_1"}[5m]))`,
		Start:    time.Now().Add(-15 * time.Minute),
		End:      time.Now(),
		Step:     time.Minute,
	})
	if err != nil {
		t.Fatalf("QueryRange with nil lookup error = %v", err)
	}
	if len(result.Results) != 0 {
		t.Fatalf("expected empty results for nil lookup, got %d", len(result.Results))
	}
}

// TestPrometheusObservabilityServiceQueryRange 验证 range query 正确解析 Prometheus matrix 响应，
// 返回多个时间采样点用于绘制时序曲线。
func TestPrometheusObservabilityServiceQueryRange(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-container-6",
	}}
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/query_range") {
			t.Fatalf("expected query_range endpoint, got %s", r.URL.Path)
		}
		// 返回含两条 series 的 matrix，每条 series 含 3 个采样点
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{
					{
						"metric": map[string]string{"container": "test-container-6", "pod": "test-container-6-78d-p428s"},
						"values": [][]any{
							{float64(1780000000), "6.95"},
							{float64(1780000015), "7.10"},
							{float64(1780000030), "6.80"},
						},
					},
				},
			},
		})
	}, lookup)

	result, err := service.QueryRange(context.Background(), ports.ObservabilityRangeQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `100 * (container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"} / container_spec_memory_limit_bytes{namespace="inst_1",pod="inst_1"})`,
		Start:    time.Unix(1780000000, 0).UTC(),
		End:      time.Unix(1780000030, 0).UTC(),
		Step:     15 * time.Second,
	})
	if err != nil {
		t.Fatalf("QueryRange error = %v", err)
	}
	if result.ResultType != ports.ObservabilityResultMatrix {
		t.Fatalf("result_type = %s, want matrix", result.ResultType)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results len = %d, want 1 series", len(result.Results))
	}
	if len(result.Results[0].Values) != 3 {
		t.Fatalf("values len = %d, want 3 points", len(result.Results[0].Values))
	}
	if result.Results[0].Values[0].Value != 6.95 {
		t.Fatalf("first value = %v, want 6.95", result.Results[0].Values[0].Value)
	}
	if !result.DevProfile.RealProvider {
		t.Fatalf("dev_profile.real_provider = false, want true for successful range query")
	}
}

// TestPrometheusObservabilityServiceQueryRangeFiltersInf 验证 range query 过滤 NaN/Inf 采样点。
func TestPrometheusObservabilityServiceQueryRangeFiltersInf(t *testing.T) {
	lookup := &fakeInstanceLookup{record: ports.WorkloadInstanceRecord{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "inst_1",
		Name:       "test-container-6",
	}}
	service := newTestPrometheusObservabilityService(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{
					{
						"metric": map[string]string{"container": "test-container-6"},
						"values": [][]any{
							{float64(1780000000), "+Inf"}, // 过滤
							{float64(1780000015), "6.95"}, // 保留
							{float64(1780000030), "NaN"},  // 过滤
						},
					},
				},
			},
		})
	}, lookup)

	result, err := service.QueryRange(context.Background(), ports.ObservabilityRangeQueryRequest{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Query:    `container_memory_working_set_bytes{namespace="inst_1",pod="inst_1"}`,
		Start:    time.Unix(1780000000, 0).UTC(),
		End:      time.Unix(1780000030, 0).UTC(),
		Step:     15 * time.Second,
	})
	if err != nil {
		t.Fatalf("QueryRange error = %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results len = %d, want 1 series", len(result.Results))
	}
	if len(result.Results[0].Values) != 1 {
		t.Fatalf("values len = %d, want 1 (Inf/NaN filtered)", len(result.Results[0].Values))
	}
	if result.Results[0].Values[0].Value != 6.95 {
		t.Fatalf("value = %v, want 6.95", result.Results[0].Values[0].Value)
	}
}
