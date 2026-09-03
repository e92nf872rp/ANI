package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type PrometheusInstanceObservabilityConfig struct {
	PrometheusURL                     string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesFieldManager            string
	HTTPClient                        *http.Client
	Now                               func() time.Time
}

type PrometheusInstanceObservability struct {
	prometheusURL string
	kubeClient    *KubernetesRESTClient
	now           func() time.Time
	mu            sync.RWMutex
	// logStore 是可选的日志持久化存储实现（ports.LogStore），nil 时 fallback 到
	// 现有 K8s pod log API（零回归）。由 runtime 在创建时通过 SetLogStore 注入，
	// 通常根据环境变量 INSTANCE_OBSERVABILITY_LOG_STORE 选择具体实现（loki / nil）。
	logStore ports.LogStore
}

// SetLogStore 注入日志持久化存储实现。由 runtime 在创建 adapter 时调用，
// 传入 nil 等价于不注入（fallback 到 K8s pod log API）。
func (o *PrometheusInstanceObservability) SetLogStore(store ports.LogStore) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logStore = store
}

func NewPrometheusInstanceObservability(config PrometheusInstanceObservabilityConfig) (*PrometheusInstanceObservability, error) {
	prometheusURL := strings.TrimRight(strings.TrimSpace(config.PrometheusURL), "/")
	if prometheusURL == "" {
		return nil, fmt.Errorf("%w: prometheus_url is required", ports.ErrNotConfigured)
	}
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:            config.KubernetesAPIHost,
		ServiceHost:     config.KubernetesServiceHost,
		ServicePort:     config.KubernetesServicePort,
		BearerToken:     config.KubernetesBearerToken,
		BearerTokenFile: config.KubernetesServiceAccountTokenFile,
		CAFile:          config.KubernetesServiceAccountCAFile,
		FieldManager:    firstNonEmpty(config.KubernetesFieldManager, "ani-instance-observability"),
		HTTPClient:      config.HTTPClient,
		Now:             config.Now,
	})
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PrometheusInstanceObservability{
		prometheusURL: prometheusURL,
		kubeClient:    client,
		now:           now,
	}, nil
}

func (o *PrometheusInstanceObservability) ListLogs(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceLogListResult{}, err
	}
	// logStore 注入路径：logStore != nil 时走持久化存储（如 Loki），
	// nil 时 fallback 到现有 K8s pod log API（零回归，PRD FR-6/FR-8）。
	o.mu.RLock()
	store := o.logStore
	o.mu.RUnlock()
	if store != nil {
		return o.listLogsFromLogStore(ctx, request, store)
	}
	return o.listLogsFromK8sAPI(ctx, request)
}

// listLogsFromLogStore 调用注入的 LogStore 实现查询持久化日志。
// 租户 namespace 由 tenantNamespace(record.TenantID) 推导（复用现有隔离逻辑）。
func (o *PrometheusInstanceObservability) listLogsFromLogStore(ctx context.Context, request ports.InstanceObservationListRequest, store ports.LogStore) (ports.InstanceLogListResult, error) {
	result, err := store.QueryLogs(ctx, ports.LogQueryRequest{
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
		Namespace:  tenantNamespace(request.TenantID),
		Limit:      request.Limit,
		Cursor:     request.Cursor,
		Level:      request.Level,
	})
	if err != nil {
		return ports.InstanceLogListResult{}, fmt.Errorf("logStore query failed: %w", err)
	}
	return ports.InstanceLogListResult{
		Items:      result.Items,
		Total:      len(result.Items),
		NextCursor: result.NextCursor,
		DevProfile: prometheusInstanceObservabilityDevProfile(),
	}, nil
}

// listLogsFromK8sAPI 是现有的 K8s pod log API fallback 逻辑（零回归）。
// 未注入 LogStore 时走此路径，行为与现状完全一致。
func (o *PrometheusInstanceObservability) listLogsFromK8sAPI(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	query := url.Values{}
	if request.Limit > 0 {
		query.Set("tailLines", strconv.Itoa(normalizeLimit(request.Limit, 100, 1000)))
	}
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+podPath(tenantNamespace(request.TenantID), request.InstanceID)+"/log?"+query.Encode(), "", nil)
	if err != nil {
		return ports.InstanceLogListResult{}, err
	}
	items := parseInstanceLogEntries(string(body), o.now().UTC())
	items = filterLogs(items, request.Level)
	items = limitLogEntries(items, normalizeLimit(request.Limit, 100, 1000))
	return ports.InstanceLogListResult{Items: items, Total: len(items), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) ListEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceEventListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceEventListResult{}, err
	}
	events = filterEvents(events, request.Type)
	events = limitEventRecords(events, normalizeLimit(request.Limit, 50, 500))
	return ports.InstanceEventListResult{Items: events, Total: len(events), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) GetMetrics(ctx context.Context, request ports.InstanceObservationGetRequest) (ports.InstanceMetricsRecord, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceMetricsRecord{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	pod := request.InstanceID
	now := o.now().UTC()
	record := ports.InstanceMetricsRecord{
		InstanceID: request.InstanceID,
		Timestamp:  now,
		DevProfile: prometheusInstanceObservabilityDevProfile(),
	}

	// VM 分支：kind=vm 查询 KubeVirt kubevirt_vmi_* 指标（guest OS 真实资源数据），
	// 位于 container/GPU 分支之前，避免误走 container cAdvisor 或 DCGM 分支。
	// VM 指标 label 用 name="<vmi-name>" 精确匹配（VMI metadata.name = record.Name，无随机后缀）。
	if request.Kind == ports.WorkloadKindVM {
		return o.getMetricsForVM(ctx, namespace, pod, record), nil
	}

	// 实例名到真实 pod 名的匹配：container/batch 渲染为 Deployment/Job，
	// K8s 生成的 pod 名带 ReplicaSet/Job hash 后缀（如 name-<hash>-<hash>），
	// 用正则 pod=~"^name(-.*)?$" 同时匹配直接 Pod 与控制器生成的 pod。
	// 用 sum() 聚合消除多 series 非确定性：正则可能匹配多个 pod 或同一 pod
	// 多 container，sum() 将多 series 合并为单一标量，避免 Result[0] 取值不稳定。
	podMatcher := promQLPodMatcher(pod)

	// metrics.k8s.io exporter：CPU、内存、网络
	// 单个 exporter 不可用时不阻塞其他字段采集；已采集字段正常返回，不可采集字段为 nil。
	// container!="",container!="POD" 过滤 pause container 与 pod 级聚合 series，
	// 确保取到业务 container 的指标而非 pause 容器或 cAdvisor 聚合值。
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(container_cpu_usage_seconds_total{namespace=%q,pod=~%q,container!="",container!="POD"})`, namespace, podMatcher)); err == nil {
		record.CPUUtilizationPct = &sample.Value
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace=%q,pod=~%q,container!="",container!="POD"})`, namespace, podMatcher)); err == nil {
		mb := sample.Value / 1024 / 1024
		record.MemoryUsedMB = &mb
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	// memory_total_mb：从 container_spec_memory_limit_bytes 读取容器内存 limit。
	// limit=0（未设 limits）时该查询返回空，MemoryTotalMB 保持 nil（不伪造 0）。
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(container_spec_memory_limit_bytes{namespace=%q,pod=~%q,container!="",container!="POD"})`, namespace, podMatcher)); err == nil && sample.Value > 0 {
		mb := sample.Value / 1024 / 1024
		record.MemoryTotalMB = &mb
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(container_network_receive_bytes_total{namespace=%q,pod=~%q})`, namespace, podMatcher)); err == nil {
		v := int64(sample.Value)
		record.NetworkRXBytes = &v
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(container_network_transmit_bytes_total{namespace=%q,pod=~%q})`, namespace, podMatcher)); err == nil {
		v := int64(sample.Value)
		record.NetworkTXBytes = &v
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	// DCGM exporter：GPU 利用率与显存（仅 gpu_container）
	// 非 gpu_container 的 GPU 字段为 nil（禁止用 0 代替缺失）。
	// 带 namespace 过滤避免跨租户/跨 namespace 同名 pod 误匹配。
	// sum() 聚合多 GPU series，避免 Result[0] 非确定性。
	if request.Kind == ports.WorkloadKindGPUContainer {
		if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(DCGM_FI_DEV_GPU_UTIL{namespace=%q,pod=~%q})`, namespace, podMatcher)); err == nil {
			record.GPUUtilizationPct = &sample.Value
			if !sample.Timestamp.IsZero() {
				record.Timestamp = sample.Timestamp
			}
		}
		// DCGM exporter 单位为 MiB，无需 /1024/1024 换算。
		// 真实 DCGM exporter 不暴露 DCGM_FI_DEV_FB_TOTAL，改用 FB_FREE + FB_USED 计算（live gate 2026-07-20 复现）。
		if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(DCGM_FI_DEV_FB_USED{namespace=%q,pod=~%q})`, namespace, podMatcher)); err == nil {
			record.GPUMemoryUsedMB = &sample.Value
			if !sample.Timestamp.IsZero() {
				record.Timestamp = sample.Timestamp
			}
		}
		if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`sum(DCGM_FI_DEV_FB_FREE{namespace=%q,pod=~%q}) + sum(DCGM_FI_DEV_FB_USED{namespace=%q,pod=~%q})`, namespace, podMatcher, namespace, podMatcher)); err == nil {
			record.GPUMemoryTotalMB = &sample.Value
			if !sample.Timestamp.IsZero() {
				record.Timestamp = sample.Timestamp
			}
		}
	}

	return record, nil
}

// getMetricsForVM 查询 KubeVirt kubevirt_vmi_* 指标，反映 VM guest OS 真实资源使用。
// 指标 label 用 name="<vmi-name>" 精确匹配（VMI metadata.name = record.Name，无随机后缀），
// 不用 pod=~"..." 正则匹配 virt-launcher pod。
// 查询指标（AC2/FR-15）：kubevirt_vmi_memory_resident_bytes（Gauge，内存驻留原始值）。
// 内存已用公式（AC5/FR-17）：kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes，
// 不得直接用 kubevirt_vmi_memory_resident_bytes 作为使用率分子。
// 内存总量：kubevirt_vmi_memory_domain_bytes。
// KubeVirt virt-handler 不可用时字段为 nil，不伪造 0（延续现有单 exporter 降级语义）。
func (o *PrometheusInstanceObservability) getMetricsForVM(ctx context.Context, namespace, vmiName string, record ports.InstanceMetricsRecord) ports.InstanceMetricsRecord {
	// CPU 使用率：rate(kubevirt_vmi_cpu_usage_seconds_total{namespace,name}[5m])
	// Counter 类型，快照用 rate(...[5m]) 转换为瞬时速率。
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`rate(kubevirt_vmi_cpu_usage_seconds_total{namespace=%q,name=%q}[5m])`, namespace, vmiName)); err == nil {
		record.CPUUtilizationPct = &sample.Value
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	// 内存驻留原始值：kubevirt_vmi_memory_resident_bytes（Gauge，AC2/FR-15 必须查询）。
	// 该指标反映 guest 物理内存驻留量，但不作为使用率分子（PRD FR-17）。
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`kubevirt_vmi_memory_resident_bytes{namespace=%q,name=%q}`, namespace, vmiName)); err == nil {
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	// 内存总量：kubevirt_vmi_memory_domain_bytes（Gauge）
	// 先取 total，再用于计算 used = domain - usable。
	var memDomainBytes float64
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`kubevirt_vmi_memory_domain_bytes{namespace=%q,name=%q}`, namespace, vmiName)); err == nil {
		memDomainBytes = sample.Value
		mb := memDomainBytes / 1024 / 1024
		record.MemoryTotalMB = &mb
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	// 内存已用：kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes（PRD FR-17）
	// usable_bytes 为 guest 可用内存，domain - usable 即 guest 真实占用，不得用 resident_bytes 替代。
	if memDomainBytes > 0 {
		if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`kubevirt_vmi_memory_usable_bytes{namespace=%q,name=%q}`, namespace, vmiName)); err == nil {
			used := memDomainBytes - sample.Value
			mb := used / 1024 / 1024
			record.MemoryUsedMB = &mb
			if !sample.Timestamp.IsZero() {
				record.Timestamp = sample.Timestamp
			}
		}
	}

	// 网络 RX：rate(kubevirt_vmi_network_receive_bytes_total[5m])
	// Counter 类型，快照用 rate 转换为瞬时速率。
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`rate(kubevirt_vmi_network_receive_bytes_total{namespace=%q,name=%q}[5m])`, namespace, vmiName)); err == nil {
		v := int64(sample.Value)
		record.NetworkRXBytes = &v
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	// 网络 TX：rate(kubevirt_vmi_network_transmit_bytes_total[5m])
	if sample, err := o.queryPrometheusScalar(ctx, fmt.Sprintf(`rate(kubevirt_vmi_network_transmit_bytes_total{namespace=%q,name=%q}[5m])`, namespace, vmiName)); err == nil {
		v := int64(sample.Value)
		record.NetworkTXBytes = &v
		if !sample.Timestamp.IsZero() {
			record.Timestamp = sample.Timestamp
		}
	}

	return record
}

// promQLPodMatcher 构造 PromQL pod label 正则匹配器，兼容直接 Pod（无后缀）
// 与 Deployment/Job 控制器生成的 pod（name-<hash>[-<hash>]）。
// 返回带锚定的正则 ^name(-.*)?$，配合 pod=~ 使用。
func promQLPodMatcher(pod string) string {
	// 转义 PromQL 正则中的元字符，避免实例名含特殊字符时注入。
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

// ListSecurityEvents 返回 K8s Warning 事件作为安全事件列表。
func (o *PrometheusInstanceObservability) ListSecurityEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceSecurityEventListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceSecurityEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceSecurityEventListResult{}, err
	}
	items := make([]ports.InstanceSecurityEventRecord, 0, len(events))
	for _, event := range events {
		if event.Type != "Warning" {
			continue
		}
		items = append(items, ports.InstanceSecurityEventRecord{
			ID:          event.ID,
			InstanceID:  request.InstanceID,
			EventType:   "kubernetes_warning",
			Severity:    "warning",
			Description: strings.TrimSpace(event.Reason + ": " + event.Message),
			OccurredAt:  event.OccurredAt,
		})
	}
	items = filterSecurityEvents(items, request.Severity)
	items = limitSecurityEventRecords(items, normalizeLimit(request.Limit, 50, 500))
	return ports.InstanceSecurityEventListResult{Items: items, Total: len(items), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) readKubernetesEvents(ctx context.Context, tenantID string, instanceID string) ([]ports.InstanceEventRecord, error) {
	query := "fieldSelector=" + url.QueryEscape("involvedObject.name="+instanceID)
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+"/api/v1/namespaces/"+url.PathEscape(tenantNamespace(tenantID))+"/events?"+query, "", nil)
	if err != nil {
		return nil, err
	}
	return parseKubernetesEvents(body, instanceID, o.now().UTC())
}

func (o *PrometheusInstanceObservability) queryPrometheusScalar(ctx context.Context, query string) (prometheusScalarSample, error) {
	values := url.Values{"query": []string{query}}
	endpoint := o.prometheusURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.kubeClient.httpClient.Do(req)
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
	return payload.Data.Result[0].scalar(o.now().UTC())
}

func parseInstanceLogEntries(body string, timestamp time.Time) []ports.InstanceLogEntry {
	lines := strings.Split(body, "\n")
	items := make([]ports.InstanceLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, ports.InstanceLogEntry{
			Timestamp: timestamp,
			Level:     inferLogLevel(line),
			Message:   line,
			Container: "main",
			Stream:    "stdout",
		})
	}
	return items
}

func inferLogLevel(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(lower, "debug"), strings.Contains(lower, " debug "):
		return "debug"
	case strings.HasPrefix(lower, "warn"), strings.Contains(lower, " warning "), strings.Contains(lower, " warn "):
		return "warn"
	case strings.HasPrefix(lower, "error"), strings.Contains(lower, " error "):
		return "error"
	default:
		return "info"
	}
}

type kubernetesEventList struct {
	Items []kubernetesEvent `json:"items"`
}

type kubernetesEvent struct {
	Metadata struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	} `json:"metadata"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	EventTime      string `json:"eventTime"`
	LastTimestamp  string `json:"lastTimestamp"`
	FirstTimestamp string `json:"firstTimestamp"`
}

func parseKubernetesEvents(body []byte, instanceID string, fallback time.Time) ([]ports.InstanceEventRecord, error) {
	var payload kubernetesEventList
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	records := make([]ports.InstanceEventRecord, 0, len(payload.Items))
	for _, item := range payload.Items {
		records = append(records, ports.InstanceEventRecord{
			ID:         firstNonEmpty(item.Metadata.UID, item.Metadata.Name, uuid.NewString()),
			InstanceID: instanceID,
			Type:       item.Type,
			Reason:     item.Reason,
			Message:    item.Message,
			Count:      item.Count,
			OccurredAt: parseKubernetesTimestamp(firstNonEmpty(item.EventTime, item.LastTimestamp, item.FirstTimestamp), fallback),
		})
	}
	return records, nil
}

func parseKubernetesTimestamp(value string, fallback time.Time) time.Time {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fallback
	}
	return parsed.UTC()
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string                   `json:"resultType"`
		Result     []prometheusVectorResult `json:"result"`
	} `json:"data"`
}

type prometheusVectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

type prometheusScalarSample struct {
	Timestamp time.Time
	Value     float64
}

func (r prometheusVectorResult) scalar(fallback time.Time) (prometheusScalarSample, error) {
	if len(r.Value) < 2 {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus sample value is incomplete", ports.ErrInvalid)
	}
	timestamp := fallback
	switch value := r.Value[0].(type) {
	case float64:
		timestamp = time.Unix(int64(value), 0).UTC()
	case string:
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			timestamp = time.Unix(int64(parsed), 0).UTC()
		}
	}
	raw, ok := r.Value[1].(string)
	if !ok {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus sample scalar is not a string", ports.ErrInvalid)
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	// 过滤 NaN/Inf：Prometheus 除零（如内存利用率 used/limit 当 limit=0）会返回 +Inf 或 NaN，
	// Go encoding/json 无法序列化这些值会触发 panic。返回错误让上层降级为 nil 字段或空结果。
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus sample value is NaN or Inf", ports.ErrInvalid)
	}
	return prometheusScalarSample{Timestamp: timestamp, Value: parsed}, nil
}

func prometheusInstanceObservabilityDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "dev_profile",
		Provider:     "prometheus-kubernetes-instance-observability",
		RealProvider: false,
		Reason:       "Sprint 13 A-track adapter maps Prometheus and Kubernetes API contracts; live provider evidence remains human-gated",
	}
}

var _ ports.InstanceObservability = (*PrometheusInstanceObservability)(nil)

// StreamLogs 流式输出实例日志。仅在注入 LokiLogStore 时可用，
// 非 loki profile（logStore=nil 或非 *LokiLogStore）返回 ErrNotConfigured（gateway 映射 503）。
func (o *PrometheusInstanceObservability) StreamLogs(ctx context.Context, request ports.InstanceLogStreamRequest, sink func(ports.InstanceLogEntry) error) error {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return err
	}
	o.mu.RLock()
	store := o.logStore
	o.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("%w: log stream requires INSTANCE_OBSERVABILITY_LOG_STORE=loki", ports.ErrNotConfigured)
	}
	lokiStore, ok := store.(*LokiLogStore)
	if !ok {
		return fmt.Errorf("%w: log stream requires Loki log store", ports.ErrNotConfigured)
	}
	return lokiStore.StreamLogs(ctx, request, tenantNamespace(request.TenantID), sink)
}
