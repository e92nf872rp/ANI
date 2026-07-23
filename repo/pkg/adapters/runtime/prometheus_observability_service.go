package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// PrometheusObservabilityService 实现 ports.ObservabilityService，
// 将前端冻结模板渲染的 PromQL 代理查询转发到真实 Prometheus。
//
// 背景：Console 的 PromQL 冻结模板用 {{namespace}}/{{pod}} 占位符，
// renderPromQL 把 instance_id 同时注入两个占位符（Console 不掌握 namespace/pod 映射）。
// 本 adapter 在后端收到 PromQL 后：
//  1. 用正则识别 PromQL 中的 namespace="..."、pod="..." 和 name="..." label 值（即 instance_id）
//  2. 用 instance_id 查实例记录，获取真实 tenant_id（→ namespace）和 name（→ pod 正则前缀 / name 精确匹配）
//  3. 重写 PromQL 中的 label 值为真实 namespace、pod 正则与 name 精确匹配
//  4. 转发到 Prometheus /api/v1/query，解析返回结果
//
// AlertRule CRUD 是元数据管理，不走 Prometheus，委托给 LocalObservabilityService。
type PrometheusObservabilityService struct {
	prometheusURL  string
	instanceLookup InstanceLookup
	local          *LocalObservabilityService
	httpClient     *http.Client
	now            func() time.Time
}

// InstanceLookup 用 instance_id 查实例记录，解析真实 namespace 与 pod 名前缀。
type InstanceLookup interface {
	Get(ctx context.Context, request ports.WorkloadInstanceGetRequest) (ports.WorkloadInstanceRecord, error)
}

// PrometheusObservabilityServiceConfig 装配配置。
type PrometheusObservabilityServiceConfig struct {
	PrometheusURL  string
	InstanceLookup InstanceLookup
	HTTPClient     *http.Client
	Now            func() time.Time
}

// NewPrometheusObservabilityService 创建真实 Prometheus 可观测性代理服务。
//
// InstanceLookup 允许为 nil（延迟注入场景）：Gateway 启动时 demo instance
// store 尚未创建，先构造 service 占位，router 注册 demo instances 后通过
// SetInstanceLookup 注入真实 lookup。若 QueryRange 调用时 lookup 仍为 nil，
// 返回空结果（不 panic），与 local 降级一致。
func NewPrometheusObservabilityService(config PrometheusObservabilityServiceConfig) (*PrometheusObservabilityService, error) {
	prometheusURL := strings.TrimRight(strings.TrimSpace(config.PrometheusURL), "/")
	if prometheusURL == "" {
		return nil, fmt.Errorf("%w: prometheus_url is required", ports.ErrNotConfigured)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PrometheusObservabilityService{
		prometheusURL:  prometheusURL,
		instanceLookup: config.InstanceLookup,
		local:          NewLocalObservabilityService(),
		httpClient:     client,
		now:            now,
	}, nil
}

// SetInstanceLookup 延迟注入实例记录查询，用于 Gateway 启动时 demo instance
// store 尚未创建的场景。router 注册 demo instances 后调用此方法注入。
// 并发安全：仅建议在启动阶段、路由注册前调用一次。
func (s *PrometheusObservabilityService) SetInstanceLookup(lookup InstanceLookup) {
	if s != nil {
		s.instanceLookup = lookup
	}
}

// labelValuePattern 匹配 PromQL label 选择器中的 namespace/pod/name="..." 值。
// 捕获组 1 为 label 名（namespace、pod 或 name），捕获组 2 为双引号内的值。
// name label 用于 VM 指标（kubevirt_vmi_*），VMI metadata.name 无随机后缀，重写时用精确匹配。
var labelValuePattern = regexp.MustCompile(`(namespace|pod|name)="([^"]*)"`)

// Query 重写前端 PromQL 中的 namespace/pod/name label 并转发到真实 Prometheus。
func (s *PrometheusObservabilityService) Query(ctx context.Context, request ports.ObservabilityQueryRequest) (ports.ObservabilityQueryResult, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: observability query is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}

	// 重写 PromQL 中的 namespace/pod/name label 值为真实 namespace、pod 正则与 VMI name。
	// 前端把 instance_id 同时注入 namespace 和 pod，后端用实例记录解析真实映射。
	rewritten, err := s.rewritePromQLLabels(ctx, request.TenantID, query)
	if err != nil {
		// 实例记录解析失败时降级为空结果，不阻塞 API 返回 200；
		// DevProfile.Reason 体现降级原因，让前端/运维能区分"真实无数据"与"查询链路降级"。
		return ports.ObservabilityQueryResult{
			Query:      query,
			ResultType: ports.ObservabilityResultVector,
			Results:    []ports.ObservabilityQuerySample{},
			DevProfile: prometheusObservabilityDegradedProfile("instance lookup failed: " + err.Error()),
		}, nil
	}

	result, err := s.queryPrometheus(ctx, rewritten)
	if err != nil {
		// Prometheus 查询失败时降级为空结果，不阻塞 API 返回 200；
		// DevProfile.Reason 体现降级原因，让前端/运维能区分"真实无数据"与"查询链路降级"。
		return ports.ObservabilityQueryResult{
			Query:      query,
			ResultType: ports.ObservabilityResultVector,
			Results:    []ports.ObservabilityQuerySample{},
			DevProfile: prometheusObservabilityDegradedProfile("prometheus query failed: " + err.Error()),
		}, nil
	}
	return result, nil
}

// prometheusObservabilityDegradedProfile 返回降级时的 dev profile，RealProvider=false
// 标记实际未从真实 Prometheus 返回数据，Reason 体现降级原因。
func prometheusObservabilityDegradedProfile(reason string) ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "real",
		Provider:     "prometheus-observability-service",
		RealProvider: false,
		Reason:       reason,
	}
}

// QueryRange 重写 PromQL label 后转发到 Prometheus /api/v1/query_range，返回时间区间内多个采样点。
func (s *PrometheusObservabilityService) QueryRange(ctx context.Context, request ports.ObservabilityRangeQueryRequest) (ports.ObservabilityRangeQueryResult, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return ports.ObservabilityRangeQueryResult{}, fmt.Errorf("%w: observability query is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.ObservabilityRangeQueryResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if request.Start.IsZero() || request.End.IsZero() || request.Step <= 0 {
		return ports.ObservabilityRangeQueryResult{}, fmt.Errorf("%w: start, end and positive step are required", ports.ErrInvalid)
	}

	// 重写 PromQL 中的 namespace/pod/name label 值为真实 namespace、pod 正则与 VMI name。
	rewritten, err := s.rewritePromQLLabels(ctx, request.TenantID, query)
	if err != nil {
		return ports.ObservabilityRangeQueryResult{
			Query:      query,
			ResultType: ports.ObservabilityResultMatrix,
			Results:    []ports.ObservabilityRangeSeries{},
			DevProfile: prometheusObservabilityDegradedProfile("instance lookup failed: " + err.Error()),
		}, nil
	}

	result, err := s.queryPrometheusRange(ctx, rewritten, request.Start, request.End, request.Step)
	if err != nil {
		return ports.ObservabilityRangeQueryResult{
			Query:      query,
			ResultType: ports.ObservabilityResultMatrix,
			Results:    []ports.ObservabilityRangeSeries{},
			DevProfile: prometheusObservabilityDegradedProfile("prometheus range query failed: " + err.Error()),
		}, nil
	}
	return result, nil
}

// queryPrometheusRange 转发 range query 到 Prometheus /api/v1/query_range 并解析 matrix 结果。
func (s *PrometheusObservabilityService) queryPrometheusRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (ports.ObservabilityRangeQueryResult, error) {
	values := url.Values{
		"query": []string{query},
		"start": []string{strconv.FormatFloat(float64(start.Unix()), 'f', -1, 64)},
		"end":   []string{strconv.FormatFloat(float64(end.Unix()), 'f', -1, 64)},
		"step":  []string{strconv.FormatFloat(step.Seconds(), 'f', -1, 64) + "s"},
	}
	endpoint := s.prometheusURL + "/api/v1/query_range?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ports.ObservabilityRangeQueryResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ports.ObservabilityRangeQueryResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ObservabilityRangeQueryResult{}, fmt.Errorf("%w: Prometheus range query returned %d", ports.ErrInvalid, resp.StatusCode)
	}
	var payload prometheusRangeQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ports.ObservabilityRangeQueryResult{}, err
	}
	if payload.Status != "success" {
		return ports.ObservabilityRangeQueryResult{}, fmt.Errorf("%w: Prometheus range query status %q", ports.ErrInvalid, payload.Status)
	}

	result := ports.ObservabilityRangeQueryResult{
		Query:      query,
		ResultType: mapPrometheusResultType(payload.Data.ResultType),
		Results:    []ports.ObservabilityRangeSeries{},
		DevProfile: prometheusObservabilityDevProfile(),
	}
	for _, item := range payload.Data.Result {
		series := ports.ObservabilityRangeSeries{Metric: item.Metric}
		for _, point := range item.Values {
			ts, ok := point[0].(float64)
			if !ok {
				continue
			}
			raw, ok := point[1].(string)
			if !ok {
				continue
			}
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				// 过滤 NaN/Inf 与解析失败的采样点，跳过而非整条 series 失败。
				continue
			}
			series.Values = append(series.Values, ports.ObservabilityRangePoint{
				Timestamp: time.Unix(int64(ts), 0).UTC(),
				Value:     value,
			})
		}
		if len(series.Values) > 0 {
			result.Results = append(result.Results, series)
		}
	}
	return result, nil
}

// rewritePromQLLabels 将 PromQL 中的 namespace="x"、pod="x" 和 name="x" 重写为真实 namespace、pod 正则与 VMI name。
// 前端把 instance_id 同时注入 namespace 和 pod 占位符，后端用首次出现的 label 值查实例记录，
// 后续同名 label 用同一实例记录的映射结果替换，保证同一 PromQL 内多个选择器一致。
// name label 用于 VM 指标（kubevirt_vmi_*），VMI metadata.name 等于 record.Name（无随机后缀），用精确匹配。
func (s *PrometheusObservabilityService) rewritePromQLLabels(ctx context.Context, tenantID string, query string) (string, error) {
	matches := labelValuePattern.FindAllStringSubmatchIndex(query, -1)
	if len(matches) == 0 {
		return query, nil
	}

	// 收集所有 label 值（去重），取首个非空值作为 instance_id 查实例记录。
	var instanceID string
	for _, idx := range matches {
		value := query[idx[4]:idx[5]]
		if value != "" {
			instanceID = value
			break
		}
	}
	if instanceID == "" {
		return query, nil
	}

	// 查实例记录，获取真实 namespace 与 pod 名前缀。
	// lookup 延迟注入未完成时返回空结果，与 local 降级一致（不 panic）。
	if s.instanceLookup == nil {
		return "", fmt.Errorf("%w: instance_lookup not configured", ports.ErrNotConfigured)
	}
	record, err := s.instanceLookup.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		return "", err
	}
	// 租户隔离校验：请求方的 tenant_id 必须与实例记录的 tenant_id 一致，
	// 防止跨租户 instance_id 泄露其他租户的 namespace 指标。
	if record.TenantID != tenantID {
		return "", fmt.Errorf("%w: instance tenant_id mismatch", ports.ErrInvalid)
	}
	realNamespace := tenantNamespace(record.TenantID)
	podMatcher := promQLPodMatcher(record.Name)

	// 逐个替换 label 值。namespace → 精确匹配真实 namespace，pod → 正则匹配带 hash 后缀的 pod，name → 精确匹配 VMI name。
	var b strings.Builder
	last := 0
	for _, idx := range matches {
		b.WriteString(query[last:idx[0]])
		labelName := query[idx[2]:idx[3]]
		switch labelName {
		case "namespace":
			// namespace 是固定字符串，用精确匹配 =（与既有实例观测 adapter 一致），不走正则引擎。
			b.WriteString(`namespace="`)
			b.WriteString(realNamespace)
			b.WriteString(`"`)
		case "pod":
			// pod 名由 Deployment/Job 控制器追加 hash 后缀，必须用正则 =~ 匹配。
			b.WriteString(`pod=~"`)
			b.WriteString(podMatcher)
			b.WriteString(`"`)
		case "name":
			// name label 用于 VM 指标（kubevirt_vmi_*），VMI metadata.name 等于 record.Name（无随机后缀），用精确匹配 =。
			b.WriteString(`name="`)
			b.WriteString(record.Name)
			b.WriteString(`"`)
		}
		last = idx[1]
	}
	b.WriteString(query[last:])
	return b.String(), nil
}

// queryPrometheus 转发 instant query 到 Prometheus /api/v1/query 并解析结果。
func (s *PrometheusObservabilityService) queryPrometheus(ctx context.Context, query string) (ports.ObservabilityQueryResult, error) {
	values := url.Values{"query": []string{query}}
	if s.now != nil {
		values.Set("time", fmt.Sprintf("%d", s.now().UTC().Unix()))
	}
	endpoint := s.prometheusURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ports.ObservabilityQueryResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ports.ObservabilityQueryResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: Prometheus query returned %d", ports.ErrInvalid, resp.StatusCode)
	}
	var payload prometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ports.ObservabilityQueryResult{}, err
	}
	if payload.Status != "success" {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: Prometheus query status %q", ports.ErrInvalid, payload.Status)
	}

	result := ports.ObservabilityQueryResult{
		Query:      query,
		ResultType: mapPrometheusResultType(payload.Data.ResultType),
		Results:    []ports.ObservabilityQuerySample{},
		DevProfile: prometheusObservabilityDevProfile(),
	}
	now := s.now().UTC()
	for _, item := range payload.Data.Result {
		sample, err := item.scalar(now)
		if err != nil {
			continue
		}
		result.Results = append(result.Results, ports.ObservabilityQuerySample{
			Metric:    item.Metric,
			Value:     sample.Value,
			Timestamp: sample.Timestamp,
		})
	}
	return result, nil
}

// prometheusObservabilityDevProfile 返回 real provider 的 dev profile 标记。
func prometheusObservabilityDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "real",
		Provider:     "prometheus-observability-service",
		RealProvider: true,
		Reason:       "PromQL proxy query forwarded to real Prometheus instance",
	}
}

// mapPrometheusResultType 将 Prometheus 返回的 resultType 字符串映射为 ports 枚举。
// 未知类型回退 vector（instant query 默认返回 vector），保证前端总能解析。
func mapPrometheusResultType(value string) ports.ObservabilityResultType {
	switch value {
	case "scalar":
		return ports.ObservabilityResultScalar
	case "matrix":
		return ports.ObservabilityResultMatrix
	case "string":
		return ports.ObservabilityResultString
	default:
		return ports.ObservabilityResultVector
	}
}

// CreateAlertRule 委托给 LocalObservabilityService（告警规则是元数据，不走 Prometheus）。
func (s *PrometheusObservabilityService) CreateAlertRule(ctx context.Context, request ports.ObservabilityAlertRuleCreateRequest) (ports.ObservabilityAlertRuleRecord, error) {
	return s.local.CreateAlertRule(ctx, request)
}

// ListAlertRules 委托给 LocalObservabilityService。
func (s *PrometheusObservabilityService) ListAlertRules(ctx context.Context, request ports.ObservabilityAlertRuleListRequest) ([]ports.ObservabilityAlertRuleRecord, error) {
	return s.local.ListAlertRules(ctx, request)
}

// GetAlertRule 委托给 LocalObservabilityService。
func (s *PrometheusObservabilityService) GetAlertRule(ctx context.Context, request ports.ObservabilityAlertRuleGetRequest) (ports.ObservabilityAlertRuleRecord, error) {
	return s.local.GetAlertRule(ctx, request)
}

// UpdateAlertRule 委托给 LocalObservabilityService。
func (s *PrometheusObservabilityService) UpdateAlertRule(ctx context.Context, request ports.ObservabilityAlertRuleUpdateRequest) (ports.ObservabilityAlertRuleRecord, error) {
	return s.local.UpdateAlertRule(ctx, request)
}

// DeleteAlertRule 委托给 LocalObservabilityService。
func (s *PrometheusObservabilityService) DeleteAlertRule(ctx context.Context, request ports.ObservabilityAlertRuleGetRequest) (ports.ObservabilityAlertRuleRecord, error) {
	return s.local.DeleteAlertRule(ctx, request)
}

// prometheusRangeQueryResponse 解析 Prometheus /api/v1/query_range 的 matrix 响应。
// result 中每条 series 的 Values 是 [timestamp, "value"] 对的数组。
type prometheusRangeQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

var _ ports.ObservabilityService = (*PrometheusObservabilityService)(nil)
