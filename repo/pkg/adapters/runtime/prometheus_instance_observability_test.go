package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestPrometheusInstanceObservabilityListsLogsEventsAndSecurityEvents(t *testing.T) {
	var requests []string
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.String())
		switch r.URL.Path {
		case "/api/v1/namespaces/ani-tenant-tenant-a/pods/pod-a/log":
			return jsonResponse(http.StatusOK, "info booted\nwarn restarted\n"), nil
		case "/api/v1/namespaces/ani-tenant-tenant-a/events":
			return jsonResponse(http.StatusOK, `{
				"items": [
					{"metadata":{"uid":"evt-a"},"type":"Normal","reason":"Scheduled","message":"pod scheduled","count":2,"lastTimestamp":"2026-06-19T08:29:00Z"},
					{"metadata":{"uid":"evt-b"},"type":"Warning","reason":"Unhealthy","message":"readiness probe failed","count":1,"eventTime":"2026-06-19T08:30:00Z"}
				]
			}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	logs, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Limit:      1,
		Level:      "warn",
	})
	if err != nil {
		t.Fatalf("ListLogs() error = %v", err)
	}
	if len(logs.Items) != 1 || logs.Items[0].Level != "warn" || logs.Items[0].Message != "warn restarted" {
		t.Fatalf("logs = %+v, want one warning log from Kubernetes pod logs", logs)
	}
	if logs.DevProfile.Mode != "dev_profile" || logs.DevProfile.RealProvider {
		t.Fatalf("logs dev profile = %+v, want non-real dev_profile marker", logs.DevProfile)
	}

	events, err := service.ListEvents(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Type:       "Warning",
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events.Items) != 1 || events.Items[0].ID != "evt-b" || events.Items[0].Reason != "Unhealthy" {
		t.Fatalf("events = %+v, want filtered Kubernetes warning event", events)
	}

	security, err := service.ListSecurityEvents(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Severity:   "warning",
	})
	if err != nil {
		t.Fatalf("ListSecurityEvents() error = %v", err)
	}
	if len(security.Items) != 1 || security.Items[0].EventType != "kubernetes_warning" {
		t.Fatalf("security events = %+v, want warning event projection", security)
	}
	if len(requests) != 3 || !strings.Contains(requests[0], "tailLines=1") || !strings.Contains(requests[1], "involvedObject.name%3Dpod-a") {
		t.Fatalf("requests = %+v, want Kubernetes logs/events API calls", requests)
	}
}

func TestPrometheusInstanceObservabilityGetsMetricsFromPrometheus(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("path = %s, want Prometheus query API", r.URL.Path)
		}
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"23.5"]}]}}`), nil
		case strings.Contains(query, "container_memory_working_set_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1610612736"]}]}}`), nil
		case strings.Contains(query, "container_spec_memory_limit_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"2147483648"]}]}}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1048576"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"524288"]}]}}`), nil
		default:
			t.Fatalf("unexpected query = %q", query)
			return nil, nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.InstanceID != "pod-a" || metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 23.5 {
		t.Fatalf("metrics = %+v, want Prometheus CPU utilization", metrics)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 1536.0 {
		t.Fatalf("memory_used_mb = %+v, want 1536 MB (1610612736 bytes)", metrics.MemoryUsedMB)
	}
	if metrics.NetworkRXBytes == nil || *metrics.NetworkRXBytes != 1048576 {
		t.Fatalf("network_rx_bytes = %+v, want 1048576", metrics.NetworkRXBytes)
	}
	if metrics.NetworkTXBytes == nil || *metrics.NetworkTXBytes != 524288 {
		t.Fatalf("network_tx_bytes = %+v, want 524288", metrics.NetworkTXBytes)
	}
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 2048.0 {
		t.Fatalf("memory_total_mb = %+v, want 2048 MB (2147483648 bytes limit)", metrics.MemoryTotalMB)
	}
	if !metrics.Timestamp.Equal(time.Unix(1780000000, 0).UTC()) {
		t.Fatalf("timestamp = %s, want Prometheus sample timestamp", metrics.Timestamp)
	}
	if metrics.DevProfile.Provider != "prometheus-kubernetes-instance-observability" || metrics.DevProfile.RealProvider {
		t.Fatalf("metrics dev profile = %+v, want Prometheus/Kubernetes contract marker", metrics.DevProfile)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsGPUContainerAggregatesDCGM 验证
// gpu_container 在 DCGM exporter 可用时填充 GPU 相关字段。
func TestPrometheusInstanceObservabilityGetMetricsGPUContainerAggregatesDCGM(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"45.0"]}]}}`), nil
		case strings.Contains(query, "container_memory_working_set_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1073741824"]}]}}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"0"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"0"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_GPU_UTIL"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"82.5"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_FB_FREE") && strings.Contains(query, "DCGM_FI_DEV_FB_USED"):
			// adapter 推导 total 的组合查询：sum(FB_FREE) + sum(FB_USED)，返回两者之和（MiB）。
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"16384"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_FB_USED"):
			// adapter 单独查询 FB_USED，返回 used 值（MiB）。
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"8192"]}]}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Kind:       ports.WorkloadKindGPUContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.GPUUtilizationPct == nil || *metrics.GPUUtilizationPct != 82.5 {
		t.Fatalf("gpu_utilization_pct = %+v, want 82.5", metrics.GPUUtilizationPct)
	}
	if metrics.GPUMemoryUsedMB == nil || *metrics.GPUMemoryUsedMB != 8192.0 {
		t.Fatalf("gpu_memory_used_mb = %+v, want 8192 MiB", metrics.GPUMemoryUsedMB)
	}
	if metrics.GPUMemoryTotalMB == nil || *metrics.GPUMemoryTotalMB != 16384.0 {
		t.Fatalf("gpu_memory_total_mb = %+v, want 16384 MiB (FB_FREE 8192 + FB_USED 8192)", metrics.GPUMemoryTotalMB)
	}
	if metrics.CPUUtilizationPct == nil {
		t.Fatalf("cpu_utilization_pct = nil, want filled from metrics.k8s.io")
	}
}

// TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerGPUNil 验证
// 非 gpu_container 的 GPU 字段为 nil（禁止用 0 代替缺失）。
func TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerGPUNil(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		// 任何 DCGM 查询都不应到达；若到达则返回错误以暴露越界调用。
		if strings.Contains(query, "DCGM_FI_DEV") {
			t.Fatalf("unexpected DCGM query for non-gpu_container: %q", query)
			return nil, nil
		}
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"10.0"]}]}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Kind:       ports.WorkloadKindContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.GPUUtilizationPct != nil {
		t.Fatalf("gpu_utilization_pct = %+v, want nil for non-gpu_container", metrics.GPUUtilizationPct)
	}
	if metrics.GPUMemoryUsedMB != nil {
		t.Fatalf("gpu_memory_used_mb = %+v, want nil for non-gpu_container", metrics.GPUMemoryUsedMB)
	}
	if metrics.GPUMemoryTotalMB != nil {
		t.Fatalf("gpu_memory_total_mb = %+v, want nil for non-gpu_container", metrics.GPUMemoryTotalMB)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsSingleExporterDegradation 验证
// 单个 exporter 不可用时不阻塞其他字段采集；已采集字段正常返回，不可采集字段为 nil。
func TestPrometheusInstanceObservabilityGetMetricsSingleExporterDegradation(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			// CPU exporter 可用
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"30.0"]}]}}`), nil
		case strings.Contains(query, "container_memory_working_set_bytes"):
			// 内存 exporter 不可用：返回错误状态
			return jsonResponse(http.StatusInternalServerError, `{"status":"error","error":"internal"}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			// 网络 exporter 不可用：返回空结果
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Kind:       ports.WorkloadKindContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v, err should be nil (single exporter degradation should not block)", err)
	}
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 30.0 {
		t.Fatalf("cpu_utilization_pct = %+v, want 30.0 (collected)", metrics.CPUUtilizationPct)
	}
	if metrics.MemoryUsedMB != nil {
		t.Fatalf("memory_used_mb = %+v, want nil (exporter unavailable)", metrics.MemoryUsedMB)
	}
	if metrics.NetworkRXBytes != nil {
		t.Fatalf("network_rx_bytes = %+v, want nil (exporter empty result)", metrics.NetworkRXBytes)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsGPUContainerDCGMUnavailable 验证
// gpu_container 当 DCGM exporter 不可用时，GPU 字段为 nil，CPU/内存等正常返回。
func TestPrometheusInstanceObservabilityGetMetricsGPUContainerDCGMUnavailable(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"55.0"]}]}}`), nil
		case strings.Contains(query, "container_memory_working_set_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"536870912"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV"):
			// DCGM exporter 不可用
			return jsonResponse(http.StatusServiceUnavailable, `{"status":"error","error":"dcgm unavailable"}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Kind:       ports.WorkloadKindGPUContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v, want nil (DCGM degradation should not block)", err)
	}
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 55.0 {
		t.Fatalf("cpu_utilization_pct = %+v, want 55.0 (collected from metrics.k8s.io)", metrics.CPUUtilizationPct)
	}
	if metrics.GPUUtilizationPct != nil {
		t.Fatalf("gpu_utilization_pct = %+v, want nil (DCGM unavailable)", metrics.GPUUtilizationPct)
	}
	if metrics.GPUMemoryUsedMB != nil {
		t.Fatalf("gpu_memory_used_mb = %+v, want nil (DCGM unavailable)", metrics.GPUMemoryUsedMB)
	}
	if metrics.GPUMemoryTotalMB != nil {
		t.Fatalf("gpu_memory_total_mb = %+v, want nil (DCGM unavailable)", metrics.GPUMemoryTotalMB)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsGPUContainerE2EIntegration 验证
// kind=gpu_container 端到端集成路径（依赖 #1 handler 传 Kind + #2 DCGM scrape 配置）：
//  1. request.Kind == WorkloadKindGPUContainer 触发 adapter GPU 分支
//  2. PromQL 使用 DCGM_FI_DEV_GPU_UTIL / DCGM_FI_DEV_FB_USED / DCGM_FI_DEV_FB_FREE 指标名（AC #1：指标名对齐真实 DCGM exporter）
//  3. GPU 字段（利用率、显存 used/total）为非 nil 值（AC #2）
//  4. kind != gpu_container 时分支隔离，GPU 字段为 nil（AC #3）
//
// 本测试为 issue-003-gpu-adapter-e2e-verify 的集成测试补全（AC #5）。
// live gate 2026-07-20 复现：真实 DCGM exporter 不暴露 DCGM_FI_DEV_FB_TOTAL，改用 FB_FREE + FB_USED 推导 total。
func TestPrometheusInstanceObservabilityGetMetricsGPUContainerE2EIntegration(t *testing.T) {
	const (
		tenantID   = "tenant-gpu-e2e"
		instanceID = "gpu-instance-e2e"
	)
	var seenQueries []string
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("path = %s, want Prometheus query API", r.URL.Path)
		}
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		seenQueries = append(seenQueries, query)
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"40.0"]}]}}`), nil
		case strings.Contains(query, "container_memory_working_set_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1073741824"]}]}}`), nil
		case strings.Contains(query, "container_spec_memory_limit_bytes"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"2147483648"]}]}}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1048576"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"524288"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_GPU_UTIL"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"77.0"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_FB_FREE") && strings.Contains(query, "DCGM_FI_DEV_FB_USED"):
			// adapter 推导 total 的组合查询：sum(FB_FREE) + sum(FB_USED)，返回两者之和（MiB）。
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"12288"]}]}}`), nil
		case strings.Contains(query, "DCGM_FI_DEV_FB_USED"):
			// adapter 单独查询 FB_USED，返回 used 值（MiB）。
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"6144"]}]}}`), nil
		default:
			t.Fatalf("unexpected query = %q", query)
			return nil, nil
		}
	})

	// 端到端调用 GetMetrics，传 Kind=gpu_container（模拟 handler 透传 record.Kind 修复后的生产路径）
	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   tenantID,
		InstanceID: instanceID,
		Kind:       ports.WorkloadKindGPUContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v, want nil on gpu_container e2e path", err)
	}

	// AC #1：验证 PromQL 使用了 DCGM 冻结指标名（对齐真实 DCGM exporter）
	joinedQueries := strings.Join(seenQueries, "\n")
	expectedDCGMMetrics := []string{
		"DCGM_FI_DEV_GPU_UTIL",
		"DCGM_FI_DEV_FB_USED",
		"DCGM_FI_DEV_FB_FREE",
	}
	for _, metric := range expectedDCGMMetrics {
		if !strings.Contains(joinedQueries, metric) {
			t.Fatalf("expected DCGM metric %q in Prometheus queries; queries=\n%s", metric, joinedQueries)
		}
	}

	// AC #2：验证 GPU 字段（利用率、显存 used/total）为非 nil 值
	if metrics.GPUUtilizationPct == nil {
		t.Fatalf("gpu_utilization_pct = nil, want non-nil for gpu_container (DCGM available)")
	}
	if *metrics.GPUUtilizationPct != 77.0 {
		t.Fatalf("gpu_utilization_pct = %v, want 77.0", *metrics.GPUUtilizationPct)
	}
	if metrics.GPUMemoryUsedMB == nil {
		t.Fatalf("gpu_memory_used_mb = nil, want non-nil for gpu_container (DCGM available)")
	}
	if *metrics.GPUMemoryUsedMB != 6144.0 { // DCGM 单位为 MiB，直传无需换算
		t.Fatalf("gpu_memory_used_mb = %v, want 6144.0 MiB", *metrics.GPUMemoryUsedMB)
	}
	if metrics.GPUMemoryTotalMB == nil {
		t.Fatalf("gpu_memory_total_mb = nil, want non-nil for gpu_container (DCGM available)")
	}
	if *metrics.GPUMemoryTotalMB != 12288.0 { // FB_FREE 6144 + FB_USED 6144 = 12288 MiB
		t.Fatalf("gpu_memory_total_mb = %v, want 12288.0 MiB (FB_FREE 6144 + FB_USED 6144)", *metrics.GPUMemoryTotalMB)
	}

	// 顺带验证 container 指标在 gpu_container 路径下也正常填充（非 GPU 字段不回归）
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 40.0 {
		t.Fatalf("cpu_utilization_pct = %+v, want 40.0 (container metrics should not regress on gpu_container)", metrics.CPUUtilizationPct)
	}
	if metrics.InstanceID != instanceID {
		t.Fatalf("InstanceID = %q, want %q", metrics.InstanceID, instanceID)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerE2EIntegration 验证
// kind != gpu_container 时 GPU 分支不触发，GPU 字段为 nil（端到端集成测试 AC #3）。
// 依赖 #1 handler 传 Kind 修复后，adapter 能正确识别 kind 并跳过 DCGM 分支。
func TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerE2EIntegration(t *testing.T) {
	nonGPUKinds := []ports.WorkloadKind{
		ports.WorkloadKindContainer,
		ports.WorkloadKindSandbox,
		ports.WorkloadKindBatchJob,
		ports.WorkloadKindNotebook,
	}
	for _, kind := range nonGPUKinds {
		t.Run(string(kind), func(t *testing.T) {
			service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
				query, _ := url.QueryUnescape(r.URL.RawQuery)
				// DCGM 查询触发即判定失败：t.Fatalf 在 roundTrip 内终止子测试并报告意外查询。
				if strings.Contains(query, "DCGM_FI_DEV") {
					t.Fatalf("unexpected DCGM query for kind=%q: %q", kind, query)
					return nil, nil
				}
				switch {
				case strings.Contains(query, "container_cpu_usage_seconds_total"):
					return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"20.0"]}]}}`), nil
				default:
					return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
				}
			})

			metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
				TenantID:   "tenant-isolation",
				InstanceID: "instance-" + string(kind),
				Kind:       kind,
			})
			if err != nil {
				t.Fatalf("GetMetrics(kind=%q) error = %v", kind, err)
			}
			if metrics.GPUUtilizationPct != nil {
				t.Fatalf("gpu_utilization_pct = %+v, want nil for kind=%q", metrics.GPUUtilizationPct, kind)
			}
			if metrics.GPUMemoryUsedMB != nil {
				t.Fatalf("gpu_memory_used_mb = %+v, want nil for kind=%q", metrics.GPUMemoryUsedMB, kind)
			}
			if metrics.GPUMemoryTotalMB != nil {
				t.Fatalf("gpu_memory_total_mb = %+v, want nil for kind=%q", metrics.GPUMemoryTotalMB, kind)
			}
		})
	}
}

// TestPrometheusInstanceObservabilityGetMetricsDeploymentPodNameRegex 验证
// 当实例由 Deployment 创建（pod 名带 ReplicaSet/Job hash 后缀）时，GetMetrics
// 用正则 pod=~"^instance(-.*)?$" 匹配真实 pod，而非精确匹配实例名。
// 复现 issue-011：实例名 test-kjs-container-6，真实 pod test-kjs-container-6-5599cd774d-p428s，
// 精确 pod="test-kjs-container-6" 查不到数据导致所有指标为 null。
func TestPrometheusInstanceObservabilityGetMetricsDeploymentPodNameRegex(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		// 验证所有 container 指标查询都使用正则 pod=~ 而非精确 pod=
		if strings.Contains(query, "container_cpu_usage_seconds_total") {
			if !strings.Contains(query, `pod=~"^test-kjs-container-6(-.*)?$"`) {
				t.Fatalf("cpu query should use pod regex matcher, got: %s", query)
			}
			if !strings.Contains(query, `container!="",container!="POD"`) {
				t.Fatalf("cpu query should filter pause container, got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"42.0"]}]}}`), nil
		}
		switch {
		case strings.Contains(query, "container_memory_working_set_bytes"):
			if !strings.Contains(query, `pod=~"^test-kjs-container-6(-.*)?$"`) {
				t.Fatalf("memory query should use pod regex matcher, got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"536870912"]}]}}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			if !strings.Contains(query, `pod=~"^test-kjs-container-6(-.*)?$"`) {
				t.Fatalf("network rx query should use pod regex matcher, got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"2048"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1024"]}]}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		InstanceID: "test-kjs-container-6",
		Kind:       ports.WorkloadKindContainer,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 42.0 {
		t.Fatalf("cpu_utilization_pct = %+v, want 42.0 (matched via pod name regex)", metrics.CPUUtilizationPct)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 512.0 {
		t.Fatalf("memory_used_mb = %+v, want 512 MB", metrics.MemoryUsedMB)
	}
	if metrics.NetworkRXBytes == nil || *metrics.NetworkRXBytes != 2048 {
		t.Fatalf("network_rx_bytes = %+v, want 2048", metrics.NetworkRXBytes)
	}
}

// TestPromQLPodMatcher 验证 pod 名正则匹配器构造正确，兼容直接 Pod 与控制器生成的 pod。
func TestPromQLPodMatcher(t *testing.T) {
	cases := []struct {
		name string
		pod  string
		want string
	}{
		{"直接 Pod 无后缀", "pod-a", "^pod-a(-.*)?$"},
		{"Deployment pod 带 hash", "test-kjs-container-6", "^test-kjs-container-6(-.*)?$"},
		{"含点号转义", "app.v1", "^app\\.v1(-.*)?$"},
		{"空字符串", "", "^(-.*)?$"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := promQLPodMatcher(tc.pod)
			if got != tc.want {
				t.Fatalf("promQLPodMatcher(%q) = %q, want %q", tc.pod, got, tc.want)
			}
		})
	}
}

func newTestPrometheusInstanceObservability(t *testing.T, roundTrip roundTripFunc) *PrometheusInstanceObservability {
	t.Helper()
	return newTestPrometheusInstanceObservabilityWithClock(t, roundTrip, func() time.Time {
		return time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	})
}

func newTestPrometheusInstanceObservabilityWithClock(t *testing.T, roundTrip roundTripFunc, now func() time.Time) *PrometheusInstanceObservability {
	t.Helper()
	transport := http.RoundTripper(http.DefaultTransport)
	if roundTrip != nil {
		// GetMetrics 会先经 pod 精确解析器请求 K8s pods list（path 以 /pods 结尾）。
		// 既有测试均按旧前缀正则行为编写，这里默认让 pods list 返回 500 触发降级，
		// 保证既有断言不受影响；需要验证精确解析的测试自行构造 service（见
		// TestPrometheusInstanceObservabilityGetMetricsUsesPrecisePodNames）。
		transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pods") {
				return jsonResponse(http.StatusInternalServerError, `{"message":"pod resolver disabled by test default"}`), nil
			}
			return roundTrip(r)
		})
	}
	service, err := NewPrometheusInstanceObservability(PrometheusInstanceObservabilityConfig{
		PrometheusURL:         "https://prometheus.example.test",
		KubernetesAPIHost:     "https://kubernetes.example.test",
		KubernetesBearerToken: "token",
		HTTPClient:            &http.Client{Transport: transport},
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("NewPrometheusInstanceObservability() error = %v", err)
	}
	return service
}

// fakeLogStore 是 ports.LogStore 的测试桩实现，记录入参并返回预设结果。
type fakeLogStore struct {
	called  bool
	lastReq ports.LogQueryRequest
	result  ports.LogQueryResult
	err     error
}

func (f *fakeLogStore) QueryLogs(_ context.Context, req ports.LogQueryRequest) (ports.LogQueryResult, error) {
	f.called = true
	f.lastReq = req
	return f.result, f.err
}

// TestPrometheusInstanceObservabilityListLogsFallbackToK8sAPI 验证未注入 LogStore 时
// ListLogs fallback 到现有 K8s pod log API（零回归，PRD FR-6 / US-006 AC）。
func TestPrometheusInstanceObservabilityListLogsFallbackToK8sAPI(t *testing.T) {
	var k8sCalled bool
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/namespaces/ani-tenant-tenant-a/pods/pod-a/log" {
			k8sCalled = true
			return jsonResponse(http.StatusOK, "info booted\nwarn restarted\n"), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		return nil, nil
	})

	// 不调用 SetLogStore，logStore 保持 nil。
	logs, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Limit:      100,
	})
	if err != nil {
		t.Fatalf("ListLogs() error = %v", err)
	}
	if !k8sCalled {
		t.Fatalf("k8s pod log API not called, want fallback path when logStore is nil")
	}
	if len(logs.Items) != 2 {
		t.Fatalf("logs = %d items, want 2 from K8s API fallback", len(logs.Items))
	}
	if logs.Items[0].Message != "info booted" {
		t.Fatalf("first log = %q, want K8s API fallback content", logs.Items[0].Message)
	}
}

// TestPrometheusInstanceObservabilityListLogsUsesInjectedLogStore 验证注入 LogStore 后
// ListLogs 走 logStore.QueryLogs 路径，不调用 K8s pod log API（PRD FR-8 / US-006 AC）。
func TestPrometheusInstanceObservabilityListLogsUsesInjectedLogStore(t *testing.T) {
	var k8sCalled bool
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/pods/") && strings.HasSuffix(r.URL.Path, "/log") {
			k8sCalled = true
		}
		return jsonResponse(http.StatusOK, ""), nil
	})

	fake := &fakeLogStore{
		result: ports.LogQueryResult{
			Items: []ports.InstanceLogEntry{
				{
					Timestamp: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
					Level:     "info",
					Message:   "loki persisted log",
					Container: "main",
					Stream:    "stdout",
				},
			},
			NextCursor: "2026-07-20T12:00:00Z",
		},
	}
	service.SetLogStore(fake)

	logs, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Limit:      100,
		Cursor:     "prev-cursor",
		Level:      "info",
	})
	if err != nil {
		t.Fatalf("ListLogs() error = %v", err)
	}
	if !fake.called {
		t.Fatalf("logStore.QueryLogs not called, want injected path when logStore is set")
	}
	if k8sCalled {
		t.Fatalf("K8s pod log API called, want it NOT called when logStore is injected (PRD FR-8)")
	}
	// 验证 QueryLogs 入参映射正确（tenant namespace 推导 + cursor/limit/level 透传）。
	if fake.lastReq.TenantID != "tenant-a" || fake.lastReq.InstanceID != "pod-a" {
		t.Fatalf("QueryLogs request = %+v, want tenant-a/pod-a", fake.lastReq)
	}
	if fake.lastReq.Namespace != "ani-tenant-tenant-a" {
		t.Fatalf("QueryLogs namespace = %q, want ani-tenant-tenant-a (tenantNamespace推导)", fake.lastReq.Namespace)
	}
	if fake.lastReq.Cursor != "prev-cursor" {
		t.Fatalf("QueryLogs cursor = %q, want prev-cursor", fake.lastReq.Cursor)
	}
	if fake.lastReq.Level != "info" {
		t.Fatalf("QueryLogs level = %q, want info", fake.lastReq.Level)
	}
	// 验证返回值映射正确。
	if len(logs.Items) != 1 || logs.Items[0].Message != "loki persisted log" {
		t.Fatalf("logs = %+v, want LogStore result", logs.Items)
	}
	if logs.NextCursor != "2026-07-20T12:00:00Z" {
		t.Fatalf("nextCursor = %q, want LogStore nextCursor", logs.NextCursor)
	}
}

// TestPrometheusInstanceObservabilityListLogsLogStoreErrorPropagated 验证
// LogStore 查询失败时错误透传，不伪造空结果（PRD FR-9 / 边界：Loki 不可达返回包装错误）。
func TestPrometheusInstanceObservabilityListLogsLogStoreErrorPropagated(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("K8s API should not be called when logStore is injected")
		return nil, nil
	})
	service.SetLogStore(&fakeLogStore{err: fmt.Errorf("loki connection refused")})

	_, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Limit:      100,
	})
	if err == nil {
		t.Fatalf("ListLogs() error = nil, want wrapped LogStore error")
	}
	if !strings.Contains(err.Error(), "logStore query failed") {
		t.Fatalf("err = %v, want wrapped 'logStore query failed' error", err)
	}
	if !strings.Contains(err.Error(), "loki connection refused") {
		t.Fatalf("err = %v, want preserve underlying Loki error", err)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsVMBranch 验证 kind=vm 触发 VM 分支，
// 查询 kubevirt_vmi_* 指标，label 用 name 精确匹配（非 pod 正则）。
// 覆盖 issue-009 AC：PromQL 构造、label 匹配、字段填充。
func TestPrometheusInstanceObservabilityGetMetricsVMBranch(t *testing.T) {
	const (
		tenantID   = "tenant-vm"
		instanceID = "vm-instance-1" // VMI metadata.name，无随机后缀
	)
	var seenQueries []string
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("path = %s, want Prometheus query API", r.URL.Path)
		}
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		seenQueries = append(seenQueries, query)
		switch {
		case strings.Contains(query, "kubevirt_vmi_cpu_usage_seconds_total"):
			// 验证使用 name 精确匹配 + rate[5m]，非 pod 正则
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("cpu query should use name= exact match, got: %s", query)
			}
			if strings.Contains(query, `pod=~`) {
				t.Fatalf("cpu query should NOT use pod=~ regex, got: %s", query)
			}
			if !strings.Contains(query, `namespace="ani-tenant-tenant-vm"`) {
				t.Fatalf("cpu query should use tenant namespace, got: %s", query)
			}
			if !strings.Contains(query, `[5m]`) {
				t.Fatalf("cpu query should use rate(...[5m]), got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"33.5"]}]}}`), nil
		case strings.Contains(query, "kubevirt_vmi_memory_resident_bytes"):
			// AC2/FR-15 必须查询 resident_bytes 指标
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("memory resident query should use name= exact match, got: %s", query)
			}
			// resident = 300000000 bytes，不作为使用率分子（FR-17），仅查询
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"300000000"]}]}}`), nil
		case strings.Contains(query, "kubevirt_vmi_memory_usable_bytes"):
			// 验证内存已用公式 domain - usable（PRD FR-17）
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("memory usable query should use name= exact match, got: %s", query)
			}
			// usable = 536870912 bytes (512 MB)，used = domain(1073741824) - usable(536870912) = 536870912 (512 MB)
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"536870912"]}]}}`), nil
		case strings.Contains(query, "kubevirt_vmi_memory_domain_bytes"):
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("memory domain query should use name= exact match, got: %s", query)
			}
			// domain = 1073741824 bytes (1024 MB)
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1073741824"]}]}}`), nil
		case strings.Contains(query, "kubevirt_vmi_network_receive_bytes_total"):
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("network rx query should use name= exact match, got: %s", query)
			}
			if !strings.Contains(query, `[5m]`) {
				t.Fatalf("network rx query should use rate(...[5m]), got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"2048"]}]}}`), nil
		case strings.Contains(query, "kubevirt_vmi_network_transmit_bytes_total"):
			if !strings.Contains(query, `name="vm-instance-1"`) {
				t.Fatalf("network tx query should use name= exact match, got: %s", query)
			}
			if !strings.Contains(query, `[5m]`) {
				t.Fatalf("network tx query should use rate(...[5m]), got: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"1024"]}]}}`), nil
		default:
			t.Fatalf("unexpected query = %q", query)
			return nil, nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   tenantID,
		InstanceID: instanceID,
		Kind:       ports.WorkloadKindVM,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}

	// CPU 利用率来自 rate(kubevirt_vmi_cpu_usage_seconds_total[5m])
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 33.5 {
		t.Fatalf("cpu_utilization_pct = %+v, want 33.5 from kubevirt_vmi_cpu_usage_seconds_total", metrics.CPUUtilizationPct)
	}

	// 内存已用 = domain - usable = 1073741824 - 536870912 = 536870912 bytes = 512 MB（PRD FR-17 公式）
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 512.0 {
		t.Fatalf("memory_used_mb = %+v, want 512.0 MB (domain 1073741824 - usable 536870912)", metrics.MemoryUsedMB)
	}

	// 内存总量来自 kubevirt_vmi_memory_domain_bytes（1073741824 bytes = 1024 MB）
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 1024.0 {
		t.Fatalf("memory_total_mb = %+v, want 1024.0 MB (1073741824 bytes)", metrics.MemoryTotalMB)
	}

	// 网络 RX/TX 来自 rate(kubevirt_vmi_network_*_bytes_total[5m])
	if metrics.NetworkRXBytes == nil || *metrics.NetworkRXBytes != 2048 {
		t.Fatalf("network_rx_bytes = %+v, want 2048", metrics.NetworkRXBytes)
	}
	if metrics.NetworkTXBytes == nil || *metrics.NetworkTXBytes != 1024 {
		t.Fatalf("network_tx_bytes = %+v, want 1024", metrics.NetworkTXBytes)
	}

	// GPU 字段必须为 nil（VM 分支不查 DCGM）
	if metrics.GPUUtilizationPct != nil {
		t.Fatalf("gpu_utilization_pct = %+v, want nil for vm kind", metrics.GPUUtilizationPct)
	}
	if metrics.GPUMemoryUsedMB != nil {
		t.Fatalf("gpu_memory_used_mb = %+v, want nil for vm kind", metrics.GPUMemoryUsedMB)
	}
	if metrics.GPUMemoryTotalMB != nil {
		t.Fatalf("gpu_memory_total_mb = %+v, want nil for vm kind", metrics.GPUMemoryTotalMB)
	}

	// 验证查询了全部 6 个 kubevirt_vmi_* 指标（含 resident_bytes，AC2/FR-15 必须查询）
	joinedQueries := strings.Join(seenQueries, "\n")
	expectedMetrics := []string{
		"kubevirt_vmi_cpu_usage_seconds_total",
		"kubevirt_vmi_memory_resident_bytes",
		"kubevirt_vmi_memory_usable_bytes",
		"kubevirt_vmi_memory_domain_bytes",
		"kubevirt_vmi_network_receive_bytes_total",
		"kubevirt_vmi_network_transmit_bytes_total",
	}
	for _, metric := range expectedMetrics {
		if !strings.Contains(joinedQueries, metric) {
			t.Fatalf("expected kubevirt metric %q in queries; queries=\n%s", metric, joinedQueries)
		}
	}
	// 额外验证（PRD FR-17）：MemoryUsedMB 不得等于 resident_bytes 的值（300000000 bytes = 286.1 MB）
	// MemoryUsedMB 必须等于 domain - usable = 536870912 bytes = 512 MB
	if metrics.MemoryUsedMB != nil && *metrics.MemoryUsedMB == 300000000/1024/1024 {
		t.Fatalf("memory_used_mb = %+v, must NOT equal resident_bytes value (PRD FR-17)", metrics.MemoryUsedMB)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsVMDoesNotQueryContainerMetrics 验证
// kind=vm 不走 container 分支（不查询 container_* / DCGM_* 指标）。
func TestPrometheusInstanceObservabilityGetMetricsVMDoesNotQueryContainerMetrics(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		// container_* 和 DCGM_* 查询触发即判定失败
		if strings.Contains(query, "container_cpu_usage_seconds_total") {
			t.Fatalf("unexpected container_cpu query for vm kind: %q", query)
		}
		if strings.Contains(query, "container_memory_working_set_bytes") {
			t.Fatalf("unexpected container_memory query for vm kind: %q", query)
		}
		if strings.Contains(query, "DCGM_FI_DEV") {
			t.Fatalf("unexpected DCGM query for vm kind: %q", query)
		}
		switch {
		case strings.Contains(query, "kubevirt_vmi_"):
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"10"]}]}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-vm",
		InstanceID: "vm-1",
		Kind:       ports.WorkloadKindVM,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	// VM 分支应填充 CPU 字段（kubevirt 指标返回了数据）
	if metrics.CPUUtilizationPct == nil {
		t.Fatalf("cpu_utilization_pct = nil, want filled from kubevirt_vmi metric")
	}
}

// TestPrometheusInstanceObservabilityGetMetricsVMVirtHandlerUnavailable 验证
// KubeVirt virt-handler 不可用时 VM 字段为 nil，不伪造 0（延续单 exporter 降级语义）。
func TestPrometheusInstanceObservabilityGetMetricsVMVirtHandlerUnavailable(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		// 所有 kubevirt_vmi_* 查询返回空结果（virt-handler 不可用）
		return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-vm",
		InstanceID: "vm-1",
		Kind:       ports.WorkloadKindVM,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v, want nil (virt-handler degradation should not block)", err)
	}
	if metrics.CPUUtilizationPct != nil {
		t.Fatalf("cpu_utilization_pct = %+v, want nil (virt-handler unavailable)", metrics.CPUUtilizationPct)
	}
	if metrics.MemoryUsedMB != nil {
		t.Fatalf("memory_used_mb = %+v, want nil (virt-handler unavailable)", metrics.MemoryUsedMB)
	}
	if metrics.MemoryTotalMB != nil {
		t.Fatalf("memory_total_mb = %+v, want nil (virt-handler unavailable)", metrics.MemoryTotalMB)
	}
	if metrics.NetworkRXBytes != nil {
		t.Fatalf("network_rx_bytes = %+v, want nil (virt-handler unavailable)", metrics.NetworkRXBytes)
	}
	if metrics.NetworkTXBytes != nil {
		t.Fatalf("network_tx_bytes = %+v, want nil (virt-handler unavailable)", metrics.NetworkTXBytes)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsNonVMDoesNotQueryKubeVirt 验证
// kind != vm 时 VM 分支不触发，不查询 kubevirt_vmi_* 指标（AC: kind != vm 不受影响）。
func TestPrometheusInstanceObservabilityGetMetricsNonVMDoesNotQueryKubeVirt(t *testing.T) {
	nonVMKinds := []ports.WorkloadKind{
		ports.WorkloadKindContainer,
		ports.WorkloadKindGPUContainer,
		ports.WorkloadKindSandbox,
		ports.WorkloadKindBatchJob,
		ports.WorkloadKindNotebook,
	}
	for _, kind := range nonVMKinds {
		t.Run(string(kind), func(t *testing.T) {
			service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
				query, _ := url.QueryUnescape(r.URL.RawQuery)
				// kubevirt_vmi_* 查询触发即判定失败
				if strings.Contains(query, "kubevirt_vmi_") {
					t.Fatalf("unexpected kubevirt_vmi query for kind=%q: %q", kind, query)
					return nil, nil
				}
				switch {
				case strings.Contains(query, "container_cpu_usage_seconds_total"):
					return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"15.0"]}]}}`), nil
				default:
					return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`), nil
				}
			})

			_, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
				TenantID:   "tenant-non-vm",
				InstanceID: "instance-" + string(kind),
				Kind:       kind,
			})
			if err != nil {
				t.Fatalf("GetMetrics(kind=%q) error = %v", kind, err)
			}
		})
	}
}

// TestPrometheusInstanceObservabilityGetMetricsSandboxKata 验证 kind=sandbox 的 kata 主路径：
//   - CPU/网络走 kata 兼容 cAdvisor 选择器（container="" 匹配 kata series 无标签，
//     id!~"/kata_overhead/.*" 排除 shim/qemu 开销），命中后不得再发 fallback 标准查询；
//   - 内存走 kata-monitor 的 kata_guest_meminfo：total=mem_total，used=mem_total-mem_available，
//     不得误取宿主机 working_set（kata 下 pause ~5MB 假数据）；
//   - GPU 字段保持 nil（sandbox 无 GPU 数据源）；
//   - sample 时间戳覆盖 record.Timestamp。
func TestPrometheusInstanceObservabilityGetMetricsSandboxKata(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		const okBody = `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,`
		switch {
		case strings.Contains(query, "kata_guest_meminfo"):
			if strings.Contains(query, `item="mem_total"`) {
				return jsonResponse(http.StatusOK, okBody+`"2147483648"]}]}}`), nil // 2 GiB
			}
			if strings.Contains(query, `item="mem_available"`) {
				return jsonResponse(http.StatusOK, okBody+`"1073741824"]}]}}`), nil // 1 GiB
			}
			t.Fatalf("unexpected kata_guest_meminfo query without item matcher: %q", query)
			return nil, nil
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			// kata 主路径命中后不允许再发标准 container fallback 查询
			if !strings.Contains(query, `container=""`) {
				t.Fatalf("kata cpu primary hit but fallback query issued: %q", query)
			}
			if !strings.Contains(query, `id!~"/kata_overhead/.*"`) {
				t.Fatalf("kata cpu query should exclude kata_overhead series: %q", query)
			}
			// CPU 必须做 rate 换算（计数器原始值是开机以来累计秒数，不是利用率）
			if !strings.Contains(query, "rate(") || !strings.Contains(query, "[5m]") {
				t.Fatalf("kata cpu query should use rate(...[5m]) for utilization semantics: %q", query)
			}
			return jsonResponse(http.StatusOK, okBody+`"23.5"]}]}}`), nil
		case strings.Contains(query, "container_network_receive_bytes_total"):
			if !strings.Contains(query, `container=""`) {
				t.Fatalf("kata network rx primary hit but fallback query issued: %q", query)
			}
			return jsonResponse(http.StatusOK, okBody+`"2048"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			if !strings.Contains(query, `container=""`) {
				t.Fatalf("kata network tx primary hit but fallback query issued: %q", query)
			}
			return jsonResponse(http.StatusOK, okBody+`"1024"]}]}}`), nil
		default:
			t.Fatalf("unexpected query in kata main path: %q", query)
			return nil, nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-sandbox",
		InstanceID: "sandbox-kata-1",
		Kind:       ports.WorkloadKindSandbox,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 23.5 {
		t.Fatalf("cpu_utilization_pct = %+v, want 23.5 (kata cAdvisor selector)", metrics.CPUUtilizationPct)
	}
	// total = 2 GiB = 2048 MB；used = 2 GiB - 1 GiB = 1024 MB（guest 内真实内存语义）
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 2048.0 {
		t.Fatalf("memory_total_mb = %+v, want 2048 MB (kata_guest_meminfo mem_total)", metrics.MemoryTotalMB)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 1024.0 {
		t.Fatalf("memory_used_mb = %+v, want 1024 MB (mem_total - mem_available)", metrics.MemoryUsedMB)
	}
	if metrics.NetworkRXBytes == nil || *metrics.NetworkRXBytes != 2048 {
		t.Fatalf("network_rx_bytes = %+v, want 2048", metrics.NetworkRXBytes)
	}
	if metrics.NetworkTXBytes == nil || *metrics.NetworkTXBytes != 1024 {
		t.Fatalf("network_tx_bytes = %+v, want 1024", metrics.NetworkTXBytes)
	}
	if metrics.GPUUtilizationPct != nil || metrics.GPUMemoryUsedMB != nil || metrics.GPUMemoryTotalMB != nil {
		t.Fatalf("gpu fields = %+v/%+v/%+v, want all nil for sandbox", metrics.GPUUtilizationPct, metrics.GPUMemoryUsedMB, metrics.GPUMemoryTotalMB)
	}
	if metrics.Timestamp.Unix() != 1780000000 {
		t.Fatalf("timestamp = %v, want sample timestamp 1780000000 (not fallback clock)", metrics.Timestamp)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsUsesPrecisePodNames 验证 GetMetrics 优先
// 使用 pod 精确解析（K8s instance label → 真实 pod 名列表）：查询中的 pod=~ 使用锚定
// 精确列表而非前缀正则，且 TTL 内复用解析结果（第二次 GetMetrics 不再请求 pods API）。
func TestPrometheusInstanceObservabilityGetMetricsUsesPrecisePodNames(t *testing.T) {
	var podListCalls int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pods"):
			podListCalls++
			if got := r.URL.Query().Get("labelSelector"); got != "ani.kubercloud.io/instance=pod-a" {
				t.Fatalf("labelSelector = %q, want instance label selector", got)
			}
			return jsonResponse(http.StatusOK, `{"items":[
				{"metadata":{"name":"pod-a-x9k2z"}},
				{"metadata":{"name":"pod-a-abc12"}}
			]}`), nil
		case r.URL.Path == "/api/v1/query":
			query, _ := url.QueryUnescape(r.URL.RawQuery)
			if !strings.Contains(query, `pod=~"^(pod-a-abc12|pod-a-x9k2z)$"`) {
				t.Fatalf("query should use anchored precise pod list, got: %s", query)
			}
			if strings.Contains(query, `(-.*)?$`) {
				t.Fatalf("query should not fall back to prefix regex: %s", query)
			}
			return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,"23.5"]}]}}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})
	service, err := NewPrometheusInstanceObservability(PrometheusInstanceObservabilityConfig{
		PrometheusURL:         "https://prometheus.example.test",
		KubernetesAPIHost:     "https://kubernetes.example.test",
		KubernetesBearerToken: "token",
		HTTPClient:            &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewPrometheusInstanceObservability() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		record, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
			TenantID:   "tenant-a",
			InstanceID: "pod-a",
		})
		if err != nil {
			t.Fatalf("GetMetrics() call %d error = %v", i+1, err)
		}
		if record.CPUUtilizationPct == nil || *record.CPUUtilizationPct != 23.5 {
			t.Fatalf("GetMetrics() call %d cpu = %v, want 23.5", i+1, record.CPUUtilizationPct)
		}
	}
	if podListCalls != 1 {
		t.Fatalf("pods API calls = %d, want 1 (resolver cache within TTL)", podListCalls)
	}
}

// TestPrometheusInstanceObservabilityGetMetricsSandboxRuncFallback 验证 kind=sandbox 的
// runc fallback 场景：kata 查询全部查空（RuntimeClass 换用 runc / kata-monitor 未部署）时，
// CPU/网络/内存自动 fallback 到标准 container 选择器，行为与 container 分支一致。
func TestPrometheusInstanceObservabilityGetMetricsSandboxRuncFallback(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		const okBody = `{"status":"success","data":{"resultType":"vector","result":[{"value":[1780000000,`
		emptyBody := `{"status":"success","data":{"resultType":"vector","result":[]}}`
		switch {
		case strings.Contains(query, "kata_guest_meminfo"):
			// kata-monitor 未部署/无 series：内存走 runc fallback
			return jsonResponse(http.StatusOK, emptyBody), nil
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			if strings.Contains(query, `container=""`) {
				// kata 选择器查空 → 触发 fallback
				return jsonResponse(http.StatusOK, emptyBody), nil
			}
			// fallback 查询也必须做 rate 换算（与公共 container 分支语义一致）
			if !strings.Contains(query, "rate(") || !strings.Contains(query, "[5m]") {
				t.Fatalf("runc fallback cpu query should use rate(...[5m]): %q", query)
			}
			return jsonResponse(http.StatusOK, okBody+`"30.0"]}]}}`), nil
		case strings.Contains(query, "container_spec_memory_limit_bytes"):
			return jsonResponse(http.StatusOK, okBody+`"1073741824"]}]}}`), nil // 1 GiB
		case strings.Contains(query, "container_memory_working_set_bytes"):
			return jsonResponse(http.StatusOK, okBody+`"536870912"]}]}}`), nil // 512 MiB
		case strings.Contains(query, "container_network_receive_bytes_total"):
			if strings.Contains(query, `container=""`) {
				return jsonResponse(http.StatusOK, emptyBody), nil
			}
			return jsonResponse(http.StatusOK, okBody+`"4096"]}]}}`), nil
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			if strings.Contains(query, `container=""`) {
				return jsonResponse(http.StatusOK, emptyBody), nil
			}
			return jsonResponse(http.StatusOK, okBody+`"2048"]}]}}`), nil
		default:
			t.Fatalf("unexpected query in runc fallback path: %q", query)
			return nil, nil
		}
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-sandbox",
		InstanceID: "sandbox-runc-1",
		Kind:       ports.WorkloadKindSandbox,
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 30.0 {
		t.Fatalf("cpu_utilization_pct = %+v, want 30.0 (standard container fallback)", metrics.CPUUtilizationPct)
	}
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 1024.0 {
		t.Fatalf("memory_total_mb = %+v, want 1024 MB (container limit fallback)", metrics.MemoryTotalMB)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 512.0 {
		t.Fatalf("memory_used_mb = %+v, want 512 MB (working_set fallback)", metrics.MemoryUsedMB)
	}
	if metrics.NetworkRXBytes == nil || *metrics.NetworkRXBytes != 4096 {
		t.Fatalf("network_rx_bytes = %+v, want 4096", metrics.NetworkRXBytes)
	}
	if metrics.NetworkTXBytes == nil || *metrics.NetworkTXBytes != 2048 {
		t.Fatalf("network_tx_bytes = %+v, want 2048", metrics.NetworkTXBytes)
	}
}
