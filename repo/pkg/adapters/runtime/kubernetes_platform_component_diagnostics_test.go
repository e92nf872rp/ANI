package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// componentDiagnosticsPromFixture 是单条 Prometheus vector 样本的固定时间戳
// （2026-09-04T12:00:00Z 附近），供 mock handler 与断言共用。
var componentDiagnosticsPromFixtureTime = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// newComponentDiagnosticsPromServer 构造 mock Prometheus /api/v1/query：
// 按 PromQL 中的指标名分发固定样本值；missing 中的指标返回空 result
// （对应「单源无样本 → 字段级降级为 nil」）；promQueries 记录收到的 PromQL
// 以便断言 namespace/pod 正则构造。
func newComponentDiagnosticsPromServer(t *testing.T, values map[string]float64, missing map[string]bool, promQueries *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if promQueries != nil {
			*promQueries = append(*promQueries, query)
		}
		w.Header().Set("Content-Type", "application/json")
		if missing[queryKeyForPromQuery(query)] {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		value, ok := values[queryKeyForPromQuery(query)]
		if !ok {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		payload := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{{
					"metric": map[string]string{},
					"value":  []any{float64(componentDiagnosticsPromFixtureTime.Unix()), fmt.Sprintf("%f", value)},
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// queryKeyForPromQuery 把 PromQL 归一为 fixture 分发 key。
func queryKeyForPromQuery(query string) string {
	switch {
	case strings.Contains(query, "container_cpu_usage_seconds_total"):
		return "cpu"
	case strings.Contains(query, "container_memory_working_set_bytes"):
		return "mem_used"
	case strings.Contains(query, "container_spec_memory_limit_bytes"):
		return "mem_total"
	case strings.Contains(query, "container_network_receive_bytes_total"):
		return "rx"
	case strings.Contains(query, "container_network_transmit_bytes_total"):
		return "tx"
	}
	return "unknown"
}

// newComponentDiagnosticsLokiServer 构造 mock Loki /loki/api/v1/query_range：
// backward 返回 fixtureLines（最新在前），forward 返回 forwardLines；
// lokiQueries 记录请求参数以便断言 LogQL/游标构造。
func newComponentDiagnosticsLokiServer(t *testing.T, fixtureLines []string, forwardLines []string, lokiQueries *[]url.Values) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/query_range", func(w http.ResponseWriter, r *http.Request) {
		if lokiQueries != nil {
			*lokiQueries = append(*lokiQueries, r.URL.Query())
		}
		lines := fixtureLines
		if r.URL.Query().Get("direction") == "forward" {
			lines = forwardLines
		}
		values := make([][]string, 0, len(lines))
		for _, line := range lines {
			values = append(values, []string{"1757000000000000000", line})
		}
		payload := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "streams",
				"result": []map[string]any{{
					"stream": map[string]string{
						"namespace": "ani-system",
						"pod":       "ani-auth-service-7d9f6b9c4d-x2l7k",
						"container": "ani-auth-service",
					},
					"values": values,
				}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func newComponentDiagnosticsLokiStore(t *testing.T, server *httptest.Server, now func() time.Time) *LokiLogStore {
	t.Helper()
	store, err := NewLokiLogStore(LokiLogStoreConfig{BaseURL: server.URL, Now: now})
	if err != nil {
		t.Fatalf("NewLokiLogStore() error = %v", err)
	}
	return store
}

// TestGetComponentMetricsAggregatesPrometheusSamples 验证五个资源指标字段
// 均从 Prometheus 聚合得到，dev_profile 为 real，组件 ref 来自注册表。
func TestGetComponentMetricsAggregatesPrometheusSamples(t *testing.T) {
	var promQueries []string
	server := newComponentDiagnosticsPromServer(t, map[string]float64{
		"cpu": 0.25, "mem_used": 268435456, "mem_total": 536870912, "rx": 1024, "tx": 2048,
	}, nil, &promQueries)
	fixedNow := componentDiagnosticsPromFixtureTime.Add(-5 * time.Minute)
	service := NewKubernetesPlatformComponentDiagnosticsService(server.URL, nil, server.Client())
	service.now = func() time.Time { return fixedNow }

	metrics, err := service.GetComponentMetrics(context.Background(), "ani-auth-service")
	if err != nil {
		t.Fatalf("GetComponentMetrics() error = %v", err)
	}
	if metrics.Component.Name != "ani-auth-service" || metrics.Component.Namespace != "ani-system" || metrics.Component.Group != ports.ComponentGroupService {
		t.Fatalf("component ref = %+v, want ani-auth-service/ani-system/service", metrics.Component)
	}
	if metrics.CPUCores == nil || *metrics.CPUCores != 0.25 {
		t.Fatalf("CPUCores = %v, want 0.25", metrics.CPUCores)
	}
	if metrics.MemoryUsedMB == nil || *metrics.MemoryUsedMB != 256 {
		t.Fatalf("MemoryUsedMB = %v, want 256 (bytes→MiB)", metrics.MemoryUsedMB)
	}
	if metrics.MemoryTotalMB == nil || *metrics.MemoryTotalMB != 512 {
		t.Fatalf("MemoryTotalMB = %v, want 512", metrics.MemoryTotalMB)
	}
	if metrics.NetworkRxBytesPerSec == nil || *metrics.NetworkRxBytesPerSec != 1024 {
		t.Fatalf("NetworkRxBytesPerSec = %v, want 1024", metrics.NetworkRxBytesPerSec)
	}
	if metrics.NetworkTxBytesPerSec == nil || *metrics.NetworkTxBytesPerSec != 2048 {
		t.Fatalf("NetworkTxBytesPerSec = %v, want 2048", metrics.NetworkTxBytesPerSec)
	}
	// 任一指标有样本时间戳时，record.Timestamp 应为样本时间戳而非构造时间。
	if !metrics.Timestamp.Equal(componentDiagnosticsPromFixtureTime) {
		t.Fatalf("Timestamp = %v, want sample timestamp %v", metrics.Timestamp, componentDiagnosticsPromFixtureTime)
	}
	if metrics.DevProfile.Mode != "real" || !metrics.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real provider", metrics.DevProfile)
	}
	// 五条 PromQL 都必须携带 namespace 与 pod 前缀正则。
	if len(promQueries) != 5 {
		t.Fatalf("prom queries = %d, want 5", len(promQueries))
	}
	for _, query := range promQueries {
		if !strings.Contains(query, `namespace="ani-system"`) {
			t.Fatalf("query %q missing namespace filter", query)
		}
		if !strings.Contains(query, `pod=~"^ani\-auth\-service(\-.*)?$"`) && !strings.Contains(query, "ani-auth-service") {
			t.Fatalf("query %q missing pod matcher", query)
		}
	}
}

// TestGetComponentMetricsFieldLevelDegradation 验证单指标无样本时对应字段为
// nil（缺失不等于 0），其余字段正常返回且不报错。
func TestGetComponentMetricsFieldLevelDegradation(t *testing.T) {
	server := newComponentDiagnosticsPromServer(t, map[string]float64{
		"cpu": 0.5,
	}, map[string]bool{"mem_used": true, "mem_total": true, "rx": true, "tx": true}, nil)
	service := NewKubernetesPlatformComponentDiagnosticsService(server.URL, nil, server.Client())

	metrics, err := service.GetComponentMetrics(context.Background(), "ani-gateway")
	if err != nil {
		t.Fatalf("GetComponentMetrics() error = %v, want field-level degradation", err)
	}
	if metrics.CPUCores == nil || *metrics.CPUCores != 0.5 {
		t.Fatalf("CPUCores = %v, want 0.5", metrics.CPUCores)
	}
	for name, value := range map[string]*float64{
		"MemoryUsedMB":         metrics.MemoryUsedMB,
		"MemoryTotalMB":        metrics.MemoryTotalMB,
		"NetworkRxBytesPerSec": metrics.NetworkRxBytesPerSec,
		"NetworkTxBytesPerSec": metrics.NetworkTxBytesPerSec,
	} {
		if value != nil {
			t.Fatalf("%s = %v, want nil on missing samples", name, *value)
		}
	}
}

// TestGetComponentMetricsUnknownComponent 验证未注册组件返回包装 ErrNotFound。
func TestGetComponentMetricsUnknownComponent(t *testing.T) {
	server := newComponentDiagnosticsPromServer(t, nil, nil, nil)
	service := NewKubernetesPlatformComponentDiagnosticsService(server.URL, nil, server.Client())

	_, err := service.GetComponentMetrics(context.Background(), "not-a-component")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotFound", err)
	}
}

// TestGetComponentMetricsPrometheusNotConfigured 验证 prometheusURL 为空时
// 返回 ErrNotConfigured（router 层映射 503）。
func TestGetComponentMetricsPrometheusNotConfigured(t *testing.T) {
	service := NewKubernetesPlatformComponentDiagnosticsService("", nil, nil)
	_, err := service.GetComponentMetrics(context.Background(), "ani-gateway")
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotConfigured", err)
	}
}

// TestQueryComponentLogsMapsLokiEntries 验证列表查询映射 Loki 条目（含 pod 维度）
// 且条数未达 limit 时 next_cursor 为空。
func TestQueryComponentLogsMapsLokiEntries(t *testing.T) {
	var lokiQueries []url.Values
	lokiServer := newComponentDiagnosticsLokiServer(t, []string{
		`{"level":"error","message":"newest line","stream":"stderr"}`,
		`{"level":"info","message":"oldest line","stream":"stdout"}`,
	}, nil, &lokiQueries)
	fixedNow := componentDiagnosticsPromFixtureTime
	loki := newComponentDiagnosticsLokiStore(t, lokiServer, func() time.Time { return fixedNow })
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)

	result, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{
		Component: "ani-auth-service", Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryComponentLogs() error = %v", err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("total = %d items = %d, want 2", result.Total, len(result.Items))
	}
	// backward 倒序：最新在前。
	if result.Items[0].Message != "newest line" || result.Items[0].Level != "error" {
		t.Fatalf("items[0] = %+v, want newest error line first", result.Items[0])
	}
	if result.Items[0].Pod != "ani-auth-service-7d9f6b9c4d-x2l7k" {
		t.Fatalf("pod = %q, want stream label pod", result.Items[0].Pod)
	}
	if result.NextCursor != "" {
		t.Fatalf("NextCursor = %q, want empty below limit", result.NextCursor)
	}
	if result.DevProfile.Mode != "real" || !result.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real provider", result.DevProfile)
	}
	if len(lokiQueries) != 1 {
		t.Fatalf("loki queries = %d, want 1", len(lokiQueries))
	}
	query := lokiQueries[0].Get("query")
	if !strings.Contains(query, `{namespace="ani-system",pod=~"`) {
		t.Fatalf("LogQL = %q, want namespace+pod filter", query)
	}
	if lokiQueries[0].Get("direction") != "backward" {
		t.Fatalf("direction = %q, want backward", lokiQueries[0].Get("direction"))
	}
}

// TestQueryComponentLogsCursorPagination 验证条数达到 limit 时返回最早一条的
// RFC3339 时间戳作为 next_cursor，翻页请求以 cursor 为 end 边界。
func TestQueryComponentLogsCursorPagination(t *testing.T) {
	var lokiQueries []url.Values
	lokiServer := newComponentDiagnosticsLokiServer(t, []string{
		`{"level":"info","message":"line-a","stream":"stdout"}`,
		`{"level":"info","message":"line-b","stream":"stdout"}`,
	}, nil, &lokiQueries)
	fixedNow := componentDiagnosticsPromFixtureTime
	loki := newComponentDiagnosticsLokiStore(t, lokiServer, func() time.Time { return fixedNow })
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)

	result, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{
		Component: "ani-auth-service", Limit: 2,
	})
	if err != nil {
		t.Fatalf("QueryComponentLogs() error = %v", err)
	}
	// fixture 行时间戳为 1757000000000000000ns = 2025-09-05T15:33:20Z（UTC）。
	wantCursor := time.Unix(0, 1757000000000000000).UTC().Format(time.RFC3339)
	if result.NextCursor != wantCursor {
		t.Fatalf("NextCursor = %q, want %q", result.NextCursor, wantCursor)
	}

	// 第二页带 cursor：end 必须等于 cursor 对应的纳秒值。
	if _, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{
		Component: "ani-auth-service", Limit: 2, Cursor: result.NextCursor,
	}); err != nil {
		t.Fatalf("QueryComponentLogs(cursor) error = %v", err)
	}
	if len(lokiQueries) != 2 {
		t.Fatalf("loki queries = %d, want 2", len(lokiQueries))
	}
	wantEnd := fmt.Sprintf("%d", time.Unix(0, 1757000000000000000).UTC().UnixNano())
	if got := lokiQueries[1].Get("end"); got != wantEnd {
		t.Fatalf("page-2 end = %q, want cursor ns %q", got, wantEnd)
	}
}

// TestQueryComponentLogsInvalidCursor 验证非法 cursor 返回 ErrInvalid。
func TestQueryComponentLogsInvalidCursor(t *testing.T) {
	loki := newComponentDiagnosticsLokiStore(t, newComponentDiagnosticsLokiServer(t, nil, nil, nil), time.Now)
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)

	_, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{
		Component: "ani-gateway", Cursor: "not-a-timestamp",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("error = %v, want wrapped ports.ErrInvalid", err)
	}
}

// TestQueryComponentLogsLokiNotConfigured 验证 loki 未配置时返回 ErrNotConfigured。
func TestQueryComponentLogsLokiNotConfigured(t *testing.T) {
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", nil, nil)
	_, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{Component: "ani-gateway"})
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotConfigured", err)
	}
}

// TestQueryComponentLogsUnknownComponent 验证未注册组件返回 ErrNotFound。
func TestQueryComponentLogsUnknownComponent(t *testing.T) {
	loki := newComponentDiagnosticsLokiStore(t, newComponentDiagnosticsLokiServer(t, nil, nil, nil), time.Now)
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)
	_, err := service.QueryComponentLogs(context.Background(), ports.PlatformComponentLogQueryRequest{Component: "not-a-component"})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotFound", err)
	}
}

// TestStreamComponentLogsReplayOrderAndCancel 验证 SSE 流：首屏回放按时间正序
// 写出（带 pod 维度），ctx 取消后立即返回 nil。
func TestStreamComponentLogsReplayOrderAndCancel(t *testing.T) {
	lokiServer := newComponentDiagnosticsLokiServer(t, []string{
		`{"level":"warn","message":"second replay line","stream":"stderr"}`,
		`{"level":"info","message":"first replay line","stream":"stdout"}`,
	}, nil, nil)
	fixedNow := componentDiagnosticsPromFixtureTime
	loki := newComponentDiagnosticsLokiStore(t, lokiServer, func() time.Time { return fixedNow })
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)

	ctx, cancel := context.WithCancel(context.Background())
	var streamed []ports.PlatformComponentLogEntry
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	err := service.StreamComponentLogs(ctx, ports.PlatformComponentLogStreamRequest{
		Component: "ani-auth-service", Limit: 10, IntervalSeconds: 1,
	}, func(entry ports.PlatformComponentLogEntry) error {
		streamed = append(streamed, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamComponentLogs() error = %v, want nil on ctx cancel", err)
	}
	if len(streamed) != 2 {
		t.Fatalf("streamed = %d entries, want 2 replay entries", len(streamed))
	}
	// 回放正序：fixture backward（最新在前）反转后 first 在前。
	if streamed[0].Message != "first replay line" || streamed[1].Message != "second replay line" {
		t.Fatalf("replay order = [%s, %s], want first→second", streamed[0].Message, streamed[1].Message)
	}
	if streamed[0].Pod != "ani-auth-service-7d9f6b9c4d-x2l7k" {
		t.Fatalf("pod = %q, want stream label pod", streamed[0].Pod)
	}
}

// TestStreamComponentLogsSinkErrorStops 验证 sink 返回 error 时立即静默退出。
func TestStreamComponentLogsSinkErrorStops(t *testing.T) {
	lokiServer := newComponentDiagnosticsLokiServer(t, []string{
		`{"level":"info","message":"replay","stream":"stdout"}`,
	}, nil, nil)
	fixedNow := componentDiagnosticsPromFixtureTime
	loki := newComponentDiagnosticsLokiStore(t, lokiServer, func() time.Time { return fixedNow })
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)

	err := service.StreamComponentLogs(context.Background(), ports.PlatformComponentLogStreamRequest{
		Component: "ani-auth-service", Limit: 10, IntervalSeconds: 30,
	}, func(ports.PlatformComponentLogEntry) error {
		return errors.New("client disconnected")
	})
	if err != nil {
		t.Fatalf("StreamComponentLogs() error = %v, want nil on sink error", err)
	}
}

// TestStreamComponentLogsUnknownComponent 验证未注册组件在进入流前返回 ErrNotFound。
func TestStreamComponentLogsUnknownComponent(t *testing.T) {
	loki := newComponentDiagnosticsLokiStore(t, newComponentDiagnosticsLokiServer(t, nil, nil, nil), time.Now)
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", loki, nil)
	err := service.StreamComponentLogs(context.Background(), ports.PlatformComponentLogStreamRequest{
		Component: "not-a-component",
	}, func(ports.PlatformComponentLogEntry) error { return nil })
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotFound", err)
	}
}

// TestStreamComponentLogsLokiNotConfigured 验证 loki 未配置时返回 ErrNotConfigured。
func TestStreamComponentLogsLokiNotConfigured(t *testing.T) {
	service := NewKubernetesPlatformComponentDiagnosticsService("http://prom.test", nil, nil)
	err := service.StreamComponentLogs(context.Background(), ports.PlatformComponentLogStreamRequest{
		Component: "ani-gateway",
	}, func(ports.PlatformComponentLogEntry) error { return nil })
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("error = %v, want wrapped ports.ErrNotConfigured", err)
	}
}
