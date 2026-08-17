package metering

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// Collector 定义单维度的用量采集能力。
type Collector interface {
	Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error)
}

// DCGMGPUCollector 采集 GPU 占用时长。计量语义是"持有时长"而非"利用率"，
// 不查询 DCGM/Prometheus：持有 N 张 GPU 运行 IntervalSec 秒 = N×IntervalSec gpu_seconds。
// spec.GPUSpec == nil 时返回 nil（跳过 GPU 维度，不写 0 错值）。
type DCGMGPUCollector struct{}

// Collect 实现 GPU 占用时长采集。spec.GPUSpec == nil 时返回 nil 跳过。
func (c DCGMGPUCollector) Collect(_ context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
	if spec.GPUSpec == nil {
		return nil, nil
	}
	quantity := float64(spec.GPUSpec.Count) * float64(spec.IntervalSec)
	return []ports.MeteringUsageRecord{{
		TenantID:      spec.TenantID,
		ResourceRef:   spec.ResourceRef,
		ResourceType:  ports.MeteringResourceInstanceGPUSeconds,
		TotalQuantity: quantity,
		Unit:          "gpu_second",
		Period:        period,
	}}, nil
}

// KubeletCPUCollector 采集 CPU Counter 增量。
// 查询 Prometheus container_cpu_usage_seconds_total 的 rate(...[IntervalSec])，
// 产出 TotalQuantity = cores × IntervalSec，unit=cpu_second。
type KubeletCPUCollector struct {
	prometheusURL string
	httpClient    *http.Client
}

// NewKubeletCPUCollector 创建 CPU collector。prometheusURL 为 Prometheus HTTP API 基地址。
func NewKubeletCPUCollector(prometheusURL string, httpClient *http.Client) KubeletCPUCollector {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	} else if httpClient.Timeout == 0 {
		httpClient.Timeout = 5 * time.Second
	}
	return KubeletCPUCollector{
		prometheusURL: strings.TrimRight(prometheusURL, "/"),
		httpClient:    httpClient,
	}
}

// Collect 查询 Prometheus 获取 CPU 使用核数（rate），乘以 IntervalSec 得到周期内 CPU 秒数。
func (c KubeletCPUCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
	namespace := tenantNamespace(spec.TenantID)
	podMatcher := promQLPodMatcher(spec.WorkloadName)
	interval := strconv.Itoa(spec.IntervalSec)
	// sum(rate(...)) 先对每条时间序列计算速率，再聚合多副本 pod 的 CPU 核数，
	// 乘以 IntervalSec 得到周期内累计 CPU 秒数。
	query := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace=%q,pod=~%q,container!="",container!="POD"}[%ss]))`, namespace, podMatcher, interval)
	cores, err := c.queryPrometheusScalar(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("kubelet_cpu: %w", err)
	}
	quantity := cores * float64(spec.IntervalSec)
	return []ports.MeteringUsageRecord{{
		TenantID:      spec.TenantID,
		ResourceRef:   spec.ResourceRef,
		ResourceType:  ports.MeteringResourceInstanceCPUSeconds,
		TotalQuantity: quantity,
		Unit:          "cpu_second",
		Period:        period,
	}}, nil
}

// KubeletMemCollector 采集内存 Gauge 瞬时占用加权时长。
// 查询 Prometheus container_memory_working_set_bytes，
// 产出 TotalQuantity = bytes / 1024^3 × IntervalSec，unit=gib_second。
type KubeletMemCollector struct {
	prometheusURL string
	httpClient    *http.Client
}

// NewKubeletMemCollector 创建内存 collector。prometheusURL 为 Prometheus HTTP API 基地址。
func NewKubeletMemCollector(prometheusURL string, httpClient *http.Client) KubeletMemCollector {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	} else if httpClient.Timeout == 0 {
		httpClient.Timeout = 5 * time.Second
	}
	return KubeletMemCollector{
		prometheusURL: strings.TrimRight(prometheusURL, "/"),
		httpClient:    httpClient,
	}
}

// Collect 查询 Prometheus 获取内存工作集字节数，转换为 GiB-秒。
func (c KubeletMemCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
	namespace := tenantNamespace(spec.TenantID)
	podMatcher := promQLPodMatcher(spec.WorkloadName)
	query := fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace=%q,pod=~%q,container!="",container!="POD"})`, namespace, podMatcher)
	bytes, err := c.queryPrometheusScalar(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("kubelet_mem: %w", err)
	}
	// GiB = bytes / 1024^3，乘以 IntervalSec 得到 GiB-秒加权量。
	quantity := bytes / (1024 * 1024 * 1024) * float64(spec.IntervalSec)
	return []ports.MeteringUsageRecord{{
		TenantID:      spec.TenantID,
		ResourceRef:   spec.ResourceRef,
		ResourceType:  ports.MeteringResourceInstanceMemorySeconds,
		TotalQuantity: quantity,
		Unit:          "gib_second",
		Period:        period,
	}}, nil
}

// queryPrometheusScalar 执行 instant query 并返回第一个标量结果。
// 与 adapters/runtime 中 queryPrometheusScalar 相同的 HTTP 交互模式。
func (c KubeletCPUCollector) queryPrometheusScalar(ctx context.Context, query string) (float64, error) {
	return queryPrometheusInstant(ctx, c.prometheusURL, c.httpClient, query)
}

func (c KubeletMemCollector) queryPrometheusScalar(ctx context.Context, query string) (float64, error) {
	return queryPrometheusInstant(ctx, c.prometheusURL, c.httpClient, query)
}

// queryPrometheusInstant 向 Prometheus /api/v1/query 发送 instant query，返回标量值。
func queryPrometheusInstant(ctx context.Context, prometheusURL string, httpClient *http.Client, query string) (float64, error) {
	values := url.Values{"query": []string{query}}
	endpoint := prometheusURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return 0, closeErr
		}
		return 0, fmt.Errorf("prometheus query returned %d", resp.StatusCode)
	}
	var payload prometheusQueryResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
	closeErr := resp.Body.Close()
	if decodeErr != nil {
		return 0, decodeErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 {
		return 0, fmt.Errorf("prometheus query returned no samples")
	}
	return payload.Data.Result[0].scalar()
}

// prometheusQueryResponse 是 Prometheus HTTP API 响应结构。
type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string                   `json:"resultType"`
		Result     []prometheusVectorResult `json:"result"`
	} `json:"data"`
}

// prometheusVectorResult 是 Prometheus 向量结果中的一条 series。
type prometheusVectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

// scalar 从 Prometheus 向量结果中提取标量值，过滤 NaN/Inf。
func (r prometheusVectorResult) scalar() (float64, error) {
	if len(r.Value) < 2 {
		return 0, fmt.Errorf("prometheus sample value is incomplete")
	}
	raw, ok := r.Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("prometheus sample scalar is not a string")
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("prometheus sample value is NaN or Inf")
	}
	return parsed, nil
}

// Resolve 根据 collectorID 路由到对应的 Collector 实现。
// 路由 key：dcgm_gpu / kubelet_cpu / kubelet_mem。
// CPU/Mem collector 需要预注入 prometheusURL 和 httpClient，未注入时返回 false。
var (
	collectorMu    sync.RWMutex
	collectorCache = make(map[string]Collector)
)

// RegisterCollector 注册或替换一个 collector 实例到全局路由表。
// 供 main.go 在启动时注入带 Prometheus URL 的 CPU/Mem collector。
func RegisterCollector(id string, c Collector) {
	collectorMu.Lock()
	defer collectorMu.Unlock()
	collectorCache[id] = c
}

// Resolve 根据 collectorID 返回对应的 Collector 实例。
func Resolve(collectorID string) (Collector, bool) {
	collectorMu.RLock()
	v, ok := collectorCache[collectorID]
	collectorMu.RUnlock()
	return v, ok
}

// RegisterAll 注册全部三个 collector：GPU（无外部依赖）+ CPU/Mem（需 Prometheus URL）。
// 由 main.go 在启动时调用，统一注册入口。
func RegisterAll(prometheusURL string, httpClient *http.Client) {
	RegisterCollector("dcgm_gpu", DCGMGPUCollector{})
	RegisterCollector("kubelet_cpu", NewKubeletCPUCollector(prometheusURL, httpClient))
	RegisterCollector("kubelet_mem", NewKubeletMemCollector(prometheusURL, httpClient))
}

// CollectAll 是包级路由入口：生成分钟对齐 Period → 遍历 spec.Dimensions →
// 逐个 Resolve + Collect → 聚合返回。
// unknown collector source 时 Warn 日志并跳过；单维度 Collect 失败时 Error 日志并跳过。
func CollectAll(ctx context.Context, spec ports.CollectionSpec, logger *slog.Logger) ([]ports.MeteringUsageRecord, error) {
	period := time.Now().Format("2006-01-02T15:04")
	out := make([]ports.MeteringUsageRecord, 0, len(spec.Dimensions))
	for _, dim := range spec.Dimensions {
		// 先复制 collector 实例引用，释放锁后再调用 Collect（避免 Collect 中的阻塞 IO 持有读锁）。
		col, ok := Resolve(dim.Source)
		if !ok {
			if logger != nil {
				logger.Warn("unknown collector source, skipping dimension", "source", dim.Source, "resource_ref", spec.ResourceRef)
			}
			continue
		}
		records, err := col.Collect(ctx, spec, period)
		if err != nil {
			if logger != nil {
				logger.Error("collector collect failed, skipping dimension", "source", dim.Source, "resource_ref", spec.ResourceRef, "error", err)
			}
			continue
		}
		out = append(out, records...)
	}
	return out, nil
}

// tenantNamespace 将租户 ID 转换为 K8s namespace 名称，与 dryrun_renderer.go 中逻辑一致。
func tenantNamespace(tenantID string) string {
	return "ani-tenant-" + strings.ReplaceAll(tenantID, "_", "-")
}

// promQLPodMatcher 构造正则匹配表达式，兼容直接 Pod 与控制器生成的带 hash 后缀的 pod。
// 与 adapters/runtime 中 promQLPodMatcher 逻辑一致。
func promQLPodMatcher(pod string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`^`, `\^`,
		`$`, `\$`,
		`.`, `\.`,
		`*`, `\*`,
		`+`, `\+`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`|`, `\|`,
	).Replace(pod)
	return "^" + escaped + "(-.*)?$"
}
