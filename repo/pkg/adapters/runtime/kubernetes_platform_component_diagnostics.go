package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// KubernetesPlatformComponentDiagnosticsService 平台组件诊断 real adapter：
//   - metrics：Prometheus cAdvisor 聚合（namespace + pod 前缀正则，[5m] rate），
//     单源失败字段为 nil，不阻塞 200；
//   - logs：复用 LokiLogStore 的 LogQL 构造与解析 helper（namespace + pod 前缀正则），
//     支持列表（backward + cursor）与 SSE 流式（回放 + forward 跟随）。
//
// 组件对象名必须命中组件状态静态注册表；未注册返回包装 ports.ErrNotFound。
type KubernetesPlatformComponentDiagnosticsService struct {
	prometheusURL string
	httpClient    *http.Client
	loki          *LokiLogStore
	now           func() time.Time
}

// 编译时断言实现两个诊断端口。
var (
	_ ports.PlatformComponentMetricsReader = (*KubernetesPlatformComponentDiagnosticsService)(nil)
	_ ports.PlatformComponentLogReader     = (*KubernetesPlatformComponentDiagnosticsService)(nil)
)

// NewKubernetesPlatformComponentDiagnosticsService 构造 real adapter。
// prometheusURL 为空 → metrics 返回 ErrNotConfigured（503）；loki 为 nil → logs 同理。
// httpClient 为 nil 时使用默认 client。
func NewKubernetesPlatformComponentDiagnosticsService(prometheusURL string, loki *LokiLogStore, httpClient *http.Client) *KubernetesPlatformComponentDiagnosticsService {
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := time.Now
	return &KubernetesPlatformComponentDiagnosticsService{
		prometheusURL: strings.TrimRight(strings.TrimSpace(prometheusURL), "/"),
		httpClient:    client,
		loki:          loki,
		now:           now,
	}
}

// GetComponentMetrics 返回单组件资源指标快照（Prometheus 单源，字段级降级）。
func (s *KubernetesPlatformComponentDiagnosticsService) GetComponentMetrics(ctx context.Context, component string) (ports.PlatformComponentMetrics, error) {
	entry, err := componentRegistryEntry(component)
	if err != nil {
		return ports.PlatformComponentMetrics{}, err
	}
	if s.prometheusURL == "" {
		return ports.PlatformComponentMetrics{}, fmt.Errorf("%w: prometheus url is required", ports.ErrNotConfigured)
	}

	ref := ports.PlatformComponentRef{Name: entry.Name, Namespace: entry.Namespace, Group: entry.Group}
	record := ports.PlatformComponentMetrics{
		Component: ref,
		Timestamp: s.now().UTC(),
		DevProfile: ports.DevProfileInfo{
			Mode:         "real",
			Provider:     "kubernetes-component-diagnostics",
			RealProvider: true,
		},
	}
	podMatcher := componentPodMatcher(entry.Name)

	// 口径与实例 metrics 对齐：业务容器聚合（过滤 pause 与 pod 级 series），
	// counter 用 [5m] rate；sum() 聚合多 pod/多 container 消除 series 非确定性。
	if sample, err := s.queryScalar(ctx, fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace=%q,pod=~%q,container!="",container!="POD"}[5m]))`, entry.Namespace, podMatcher)); err == nil {
		record.CPUCores = &sample.Value
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := s.queryScalar(ctx, fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace=%q,pod=~%q,container!="",container!="POD"})`, entry.Namespace, podMatcher)); err == nil {
		mb := sample.Value / 1024 / 1024
		record.MemoryUsedMB = &mb
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := s.queryScalar(ctx, fmt.Sprintf(`sum(container_spec_memory_limit_bytes{namespace=%q,pod=~%q,container!="",container!="POD"})`, entry.Namespace, podMatcher)); err == nil && sample.Value > 0 {
		mb := sample.Value / 1024 / 1024
		record.MemoryTotalMB = &mb
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := s.queryScalar(ctx, fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{namespace=%q,pod=~%q}[5m]))`, entry.Namespace, podMatcher)); err == nil {
		record.NetworkRxBytesPerSec = &sample.Value
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := s.queryScalar(ctx, fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{namespace=%q,pod=~%q}[5m]))`, entry.Namespace, podMatcher)); err == nil {
		record.NetworkTxBytesPerSec = &sample.Value
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	return record, nil
}

// QueryComponentLogs 返回单组件日志列表（Loki backward，倒序 + cursor 翻页）。
func (s *KubernetesPlatformComponentDiagnosticsService) QueryComponentLogs(ctx context.Context, request ports.PlatformComponentLogQueryRequest) (ports.PlatformComponentLogListResult, error) {
	entry, err := componentRegistryEntry(request.Component)
	if err != nil {
		return ports.PlatformComponentLogListResult{}, err
	}
	if s.loki == nil {
		return ports.PlatformComponentLogListResult{}, fmt.Errorf("%w: loki log store is required", ports.ErrNotConfigured)
	}

	endNs, err := s.loki.cursorToEndNs(request.Cursor)
	if err != nil {
		return ports.PlatformComponentLogListResult{}, err
	}
	limit := normalizeLimit(request.Limit, 100, 1000)
	startNs := endNs - int64(24*time.Hour)
	logql := buildLokiLogQL(entry.Namespace, entry.Name)

	lokiResp, err := s.loki.queryRange(ctx, logql, startNs, endNs, limit, "backward")
	if err != nil {
		return ports.PlatformComponentLogListResult{}, fmt.Errorf("loki component query failed: %w", err)
	}
	items, nextCursor := mapLokiStreamsToComponentEntries(lokiResp, request.Level, limit)
	return ports.PlatformComponentLogListResult{
		Items:      items,
		Total:      len(items),
		NextCursor: nextCursor,
		DevProfile: s.diagnosticsDevProfile(),
	}, nil
}

// StreamComponentLogs SSE 流式组件日志：回放（backward 反转正序）+ forward 跟随，
// 身份去重与游标语义复刻 LokiLogStore.StreamLogs。
func (s *KubernetesPlatformComponentDiagnosticsService) StreamComponentLogs(ctx context.Context, request ports.PlatformComponentLogStreamRequest, sink func(ports.PlatformComponentLogEntry) error) error {
	entry, err := componentRegistryEntry(request.Component)
	if err != nil {
		return err
	}
	if s.loki == nil {
		return fmt.Errorf("%w: loki log store is required", ports.ErrNotConfigured)
	}

	limit := normalizeLimit(request.Limit, lokiStreamDefaultLimit, 1000)
	interval := lokiStreamDefaultInterval
	if request.IntervalSeconds > 0 {
		interval = time.Duration(request.IntervalSeconds) * time.Second
	}
	level := strings.TrimSpace(request.Level)
	logql := buildLokiLogQL(entry.Namespace, entry.Name)

	// ── 阶段 1：首屏回放（backward → 时间正序） ─────────────────────
	now := s.now()
	startNs := now.Add(-24 * time.Hour).UnixNano()
	endNs := now.UnixNano()
	lokiResp, err := s.loki.queryRange(ctx, logql, startNs, endNs, limit, "backward")
	if err != nil {
		return fmt.Errorf("loki component stream replay failed: %w", err)
	}
	entries := mapLokiStreamsBackwardToForward(lokiResp, level)
	componentEntries := toComponentEntries(lokiResp, entries)
	var lastTS time.Time
	for _, item := range componentEntries {
		if err := sink(item); err != nil {
			return nil
		}
		lastTS = item.Timestamp
	}
	if lastTS.IsZero() {
		lastTS = now
	}

	// ── 阶段 2：增量跟随（forward 轮询 + 去重） ─────────────────────
	seen := make(map[string]bool, len(entries))
	for _, item := range entries {
		seen[lokiEntryIdentity(item)] = true
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		now = s.now()
		queryStart := lastTS.Add(1 * time.Nanosecond).UnixNano()
		queryEnd := now.UnixNano()
		if queryStart > queryEnd {
			continue
		}
		lokiResp, err := s.loki.queryRange(ctx, logql, queryStart, queryEnd, lokiStreamForwardLimit, "forward")
		if err != nil {
			continue // 瞬时失败不中断流，下一周期自愈
		}
		fwdEntries := mapLokiStreamsForward(lokiResp, level)
		sort.Slice(fwdEntries, func(i, j int) bool {
			return fwdEntries[i].Timestamp.Before(fwdEntries[j].Timestamp)
		})
		for _, item := range fwdEntries {
			id := lokiEntryIdentity(item)
			if seen[id] {
				continue
			}
			seen[id] = true
			if !item.Timestamp.After(lastTS) {
				continue
			}
			lastTS = item.Timestamp
			if err := sink(toComponentEntry(lokiResp, item)); err != nil {
				return nil
			}
		}
	}
}

// diagnosticsDevProfile real adapter 固定 dev_profile。
func (s *KubernetesPlatformComponentDiagnosticsService) diagnosticsDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "real",
		Provider:     "kubernetes-component-diagnostics",
		RealProvider: true,
	}
}

// queryScalar 调 Prometheus /api/v1/query 取标量（vector 首样本）。
// 口径与 PrometheusInstanceObservability.queryPrometheusScalar 一致：
// success 且有样本才返回，否则错误（调用方按字段级降级处理）。
func (s *KubernetesPlatformComponentDiagnosticsService) queryScalar(ctx context.Context, query string) (prometheusScalarSample, error) {
	values := url.Values{"query": []string{query}}
	endpoint := s.prometheusURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return prometheusScalarSample{}, closeErr
		}
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus query returned %d", ports.ErrInvalid, resp.StatusCode)
	}
	var payload prometheusQueryResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
	closeErr := resp.Body.Close()
	if decodeErr != nil {
		return prometheusScalarSample{}, decodeErr
	}
	if closeErr != nil {
		return prometheusScalarSample{}, closeErr
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus query returned no samples", ports.ErrInvalid)
	}
	return payload.Data.Result[0].scalar(s.now().UTC())
}

// toComponentEntry 从 Loki 响应中找回该 entry 所属 stream 的 pod label。
// identity（时间戳+message）在多 stream 下可能碰撞，找不到时 pod 为空（不阻塞）。
func toComponentEntry(resp lokiResponse, entry ports.InstanceLogEntry) ports.PlatformComponentLogEntry {
	for _, stream := range resp.Data.Result {
		container := stream.Stream["container"]
		pod := stream.Stream["pod"]
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			tsInt, parseErr := strconv.ParseInt(v[0], 10, 64)
			if parseErr != nil {
				continue
			}
			ts := time.Unix(0, tsInt).UTC()
			parsed := parseLokiLogLine(v[1], ts, container)
			if parsed.Timestamp.Equal(entry.Timestamp) && parsed.Message == entry.Message {
				item := toComponentEntryFromInstance(entry)
				item.Pod = pod
				return item
			}
		}
	}
	return toComponentEntryFromInstance(entry)
}

// toComponentEntries 批量映射（保持 entries 顺序），pod 从 resp stream label 反查。
func toComponentEntries(resp lokiResponse, entries []ports.InstanceLogEntry) []ports.PlatformComponentLogEntry {
	items := make([]ports.PlatformComponentLogEntry, 0, len(entries))
	for _, entry := range entries {
		items = append(items, toComponentEntry(resp, entry))
	}
	return items
}

func toComponentEntryFromInstance(entry ports.InstanceLogEntry) ports.PlatformComponentLogEntry {
	return ports.PlatformComponentLogEntry{
		Timestamp: entry.Timestamp,
		Level:     entry.Level,
		Message:   entry.Message,
		Container: entry.Container,
		Stream:    entry.Stream,
	}
}

// mapLokiStreamsToComponentEntries backward 倒序列表映射（带 pod 维度与 next_cursor）。
func mapLokiStreamsToComponentEntries(resp lokiResponse, level string, limit int) ([]ports.PlatformComponentLogEntry, string) {
	instanceEntries, nextCursor := mapLokiStreamsToLogEntries(resp, level, limit)
	items := make([]ports.PlatformComponentLogEntry, 0, len(instanceEntries))
	for _, entry := range instanceEntries {
		items = append(items, toComponentEntry(resp, entry))
	}
	return items, nextCursor
}

// componentPodMatcher 构造组件 pod 前缀正则（与 promQLPodMatcher 一致）：
// Deployment pod 名 = <name>-<rs-hash>-<pod-hash>，同时兼容直接 Pod。
func componentPodMatcher(component string) string {
	return "^" + escapeLogQLRegex(component) + "(-.*)?$"
}

// componentRegistryEntry 按组件对象名查注册表；未注册返回包装 ErrNotFound。
func componentRegistryEntry(component string) (componentRegistration, error) {
	component = strings.TrimSpace(component)
	if component == "" {
		return componentRegistration{}, fmt.Errorf("%w: component name is required", ports.ErrInvalid)
	}
	for _, entry := range componentStatusRegistry {
		if entry.Name == component {
			return entry, nil
		}
	}
	return componentRegistration{}, fmt.Errorf("%w: component %q is not registered", ports.ErrNotFound, component)
}

// ValidatePlatformComponentRegistered 供 router 在进入 SSE 前做预流校验：
// 组件名必须命中注册表，未注册返回包装 ports.ErrNotFound（HTTP 404 JSON）。
func ValidatePlatformComponentRegistered(component string) (ports.PlatformComponentRef, error) {
	entry, err := componentRegistryEntry(component)
	if err != nil {
		return ports.PlatformComponentRef{}, err
	}
	return ports.PlatformComponentRef{Name: entry.Name, Namespace: entry.Namespace, Group: entry.Group}, nil
}
