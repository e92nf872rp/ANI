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

// LokiPlatformAuditConfig 是 LokiPlatformAudit 的构造配置。
type LokiPlatformAuditConfig struct {
	// BaseURL 是 Loki HTTP API 根地址，例如 http://ani-loki.ani-s07-observability:3100。
	BaseURL string
	// HTTPClient 可选，注入自定义 http.Client（测试、超时控制）。
	HTTPClient *http.Client
	// Now 可选，用于计算默认查询终点。
	Now func() time.Time
}

// LokiPlatformAudit 实现 ports.PlatformAuditService，通过 Loki HTTP API 查询
// k8s 控制面审计日志（独立 stream="kubernetes-audit"）。
//
// 数据流：fluent-bit tail 采 apiserver audit.log → 打 label（audit_user/
// audit_verb/audit_resource/audit_namespace/audit_name/audit_code）→ Loki
// stream={stream="kubernetes-audit"}。查询只在 label 上做精确/正则过滤，
// 不含 node 维度，因此跨多 master 的多个 node=* stream 一次拉全。
//
// 排序/分页：query_range 取 limit=page_size+1（多取 1 判断下页），各 stream 结果
// 按 timestamp(+auditID) 全局倒序合并后截取一页；返回末条 timestamp 作为游标
// after，下一页 end=after（Loki start≤ts<end 左闭右开），天然去重、不依赖 offset。
type LokiPlatformAudit struct {
	baseURL string
	client  *http.Client
	now     func() time.Time
}

// 编译时断言 LokiPlatformAudit 实现 ports.PlatformAuditService。
var _ ports.PlatformAuditService = (*LokiPlatformAudit)(nil)

// NewLokiPlatformAudit 创建一个走 Loki HTTP API 的 PlatformAuditService 实现。
func NewLokiPlatformAudit(config LokiPlatformAuditConfig) (*LokiPlatformAudit, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("%w: loki base_url is required", ports.ErrNotConfigured)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &LokiPlatformAudit{
		baseURL: baseURL,
		client:  client,
		now:     now,
	}, nil
}

// QueryAuditLogs 查询平台审计日志。Loki 不可用/非 200 时按单源降级语义返回
// 200 等价结果（空 items + dev_profile.real_provider=false + reason），不报错。
//
// 注意：total_approx 为 best-effort（count_over_time 按 step 聚合）；若主查询成功而
// total 查询失败，仅 total 置 0，不降级 real_provider（Loki 可达主查询即认为可用）。
func (s *LokiPlatformAudit) QueryAuditLogs(ctx context.Context, query ports.PlatformAuditLogQuery) (ports.PlatformAuditLogResult, error) {
	now := s.now().UTC()
	end := now
	if query.TimeTo != nil {
		end = query.TimeTo.UTC()
	}
	if strings.TrimSpace(query.After) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(query.After))
		if err != nil {
			return ports.PlatformAuditLogResult{}, fmt.Errorf("%w: invalid after cursor %q: %v", ports.ErrPlatformAuditInvalid, query.After, err)
		}
		end = parsed.UTC() // 左闭右开，end=after 排除该边界，天然去重
	}
	start := end.Add(-defaultAuditWindow)
	if query.TimeFrom != nil {
		start = query.TimeFrom.UTC()
	}
	if start.After(end) {
		return ports.PlatformAuditLogResult{}, fmt.Errorf("%w: time_from must not be after time_to", ports.ErrPlatformAuditInvalid)
	}

	pageSize := normalizePlatformAuditPageSize(query.PageSize)
	limit := pageSize + 1 // 多取 1 判断是否有下页

	logql := buildPlatformAuditLogQL(query)

	items, err := s.fetchAuditItems(ctx, logql, start, end, limit)
	if err != nil {
		// 单源失败不阻塞 200：降级为空结果 + reason。
		return degradedPlatformAudit(err), nil
	}

	// 多 stream 全局倒序合并（timestamp 相同以 auditID 兜底稳定排序）。
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].Timestamp.Equal(items[j].Timestamp) {
			return items[i].Timestamp.After(items[j].Timestamp)
		}
		return items[i].AuditID > items[j].AuditID
	})

	hasMore := len(items) > pageSize
	if len(items) > pageSize {
		items = items[:pageSize]
	}

	nextAfter := ""
	if hasMore {
		nextAfter = items[len(items)-1].Timestamp.Format(time.RFC3339)
	}

	total := s.fetchAuditTotal(ctx, logql, start, end)

	return ports.PlatformAuditLogResult{
		Items:       items,
		NextAfter:   nextAfter,
		TotalApprox: total,
		DevProfile: ports.DevProfileInfo{
			Mode:         "real",
			Provider:     "loki",
			RealProvider: true,
		},
	}, nil
}

// defaultAuditWindow 是未显式指定 time_from 时回退的查询窗口（最近 24 小时）。
const defaultAuditWindow = 24 * time.Hour

// normalizePlatformAuditPageSize 钳制每页条数：默认 20，上限 100，超限钳制为 100。
func normalizePlatformAuditPageSize(pageSize int) int {
	if pageSize <= 0 {
		return defaultAuditPageSize
	}
	if pageSize > maxAuditPageSize {
		return maxAuditPageSize
	}
	return pageSize
}

const (
	// defaultAuditPageSize 默认每页条数。
	defaultAuditPageSize = 20
	// maxAuditPageSize 每页条数上限。
	maxAuditPageSize = 100
)

// buildPlatformAuditLogQL 构造审计 LogQL 选择器 {stream="kubernetes-audit", <label过滤>}，
// 可选追加 keyword 行过滤器（|~，作用于原始 JSON 行，限时间窗使用）。
// 不含 node 维度 → 跨多 master 的多个 node=* stream 一次拉全。
func buildPlatformAuditLogQL(query ports.PlatformAuditLogQuery) string {
	selectors := []string{`stream="kubernetes-audit"`}
	if query.User != "" {
		selectors = append(selectors, `audit_user=`+strconv.Quote(query.User))
	}
	if query.Verb != "" {
		selectors = append(selectors, `audit_verb=`+strconv.Quote(query.Verb))
	}
	if query.ResourceType != "" {
		selectors = append(selectors, `audit_resource=`+strconv.Quote(query.ResourceType))
	}
	if query.Namespace != "" {
		selectors = append(selectors, `audit_namespace=`+strconv.Quote(query.Namespace))
	}
	logql := "{" + strings.Join(selectors, ",") + "}"
	if query.Keyword != "" {
		logql += ` |~ "` + escapeLogQLRegex(query.Keyword) + `"`
	}
	return logql
}

// fetchAuditItems 调用 /loki/api/v1/query_range 拉取审计条目，映射为 Item。
func (s *LokiPlatformAudit) fetchAuditItems(ctx context.Context, logql string, start, end time.Time, limit int) ([]ports.PlatformAuditLogItem, error) {
	params := url.Values{}
	params.Set("query", logql)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(limit))

	resp, err := s.getLoki(ctx, "/loki/api/v1/query_range", params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki returned status %d", resp.StatusCode)
	}
	lokiResp, err := decodeLokiResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode loki response: %w", err)
	}

	var items []ports.PlatformAuditLogItem
	for _, stream := range lokiResp.Data.Result {
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			tsNs, parseErr := strconv.ParseInt(v[0], 10, 64)
			if parseErr != nil {
				continue
			}
			item := parsePlatformAuditLine(v[1], time.Unix(0, tsNs).UTC())
			if item == nil {
				continue
			}
			items = append(items, *item)
		}
	}
	return items, nil
}

// fetchAuditTotal 用 count_over_time（按 step 聚合）返回近似总量；任意失败返回 0。
func (s *LokiPlatformAudit) fetchAuditTotal(ctx context.Context, logql string, start, end time.Time) int64 {
	window := end.Sub(start)
	if window <= 0 {
		return 0
	}
	rangeStr := fmt.Sprintf("%ds", int64(window.Seconds()))
	query := "sum(count_over_time(" + logql + "[" + rangeStr + "]))"

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("step", rangeStr)

	resp, err := s.getLoki(ctx, "/loki/api/v1/query_range", params)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var matrix struct {
		Data struct {
			Result []struct {
				Values [][]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&matrix); err != nil {
		return 0
	}
	var total float64
	for _, stream := range matrix.Data.Result {
		for _, pair := range stream.Values {
			if len(pair) < 2 {
				continue
			}
			total += lokiSampleFloat(pair[1])
		}
	}
	return int64(total)
}

// lokiSampleFloat 把 Loki matrix 样本 value 解析为 float。Loki 返回的样本 value
// 是字符串（[ts, "value"]），也兼容浮点类型的测试/降级。
func lokiSampleFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

// getLoki 复用 loki_log_store 的 HTTP 基建，向 Loki 发起 GET。
func (s *LokiPlatformAudit) getLoki(ctx context.Context, apiPath string, params url.Values) (*http.Response, error) {
	reqURL := s.baseURL + apiPath + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("loki query request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki query failed: %w", err)
	}
	return resp, nil
}

// degradedPlatformAudit 构造单源降级结果（200 等价，real_provider=false + reason）。
func degradedPlatformAudit(err error) ports.PlatformAuditLogResult {
	return ports.PlatformAuditLogResult{
		Items:       []ports.PlatformAuditLogItem{},
		NextAfter:   "",
		TotalApprox: 0,
		DevProfile: ports.DevProfileInfo{
			Mode:         "real",
			Provider:     "loki",
			RealProvider: false,
			Reason:       err.Error(),
		},
	}
}

// platformAuditLine 是 fluent-bit 存入 Loki 的审计 record 的关键字段（脱敏后）。
type platformAuditLine struct {
	AuditID                  string `json:"auditID"`
	RequestReceivedTimestamp string `json:"requestReceivedTimestamp"`
	Verb                     string `json:"verb"`
	User                     *struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	} `json:"user"`
	ObjectRef *struct {
		Namespace string `json:"namespace"`
		Resource  string `json:"resource"`
		Name      string `json:"name"`
	} `json:"objectRef"`
	ResponseStatus *struct {
		Code float64 `json:"code"`
	} `json:"responseStatus"`
	RequestURI string `json:"requestURI"`
	UserAgent  string `json:"userAgent"`
}

// parsePlatformAuditLine 将单条 Loki 审计 JSON 行映射为 Item；非审计/畸形行返回 nil。
// 时间戳以 Loki 索引时间（tsNs）为权威（Time_Key=requestReceivedTimestamp）。
func parsePlatformAuditLine(line string, ts time.Time) *ports.PlatformAuditLogItem {
	var rec platformAuditLine
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return nil
	}
	if rec.AuditID == "" && rec.Verb == "" {
		return nil // 非审计行（如误入流），跳过
	}
	item := &ports.PlatformAuditLogItem{
		AuditID:      rec.AuditID,
		Timestamp:    ts,
		Verb:         rec.Verb,
		ResponseCode: 0,
		Detail: ports.PlatformAuditDetail{
			RequestURI: rec.RequestURI,
			UserAgent:  rec.UserAgent,
		},
	}
	if rec.User != nil {
		item.User.Username = rec.User.Username
		item.User.Groups = rec.User.Groups
	}
	if rec.ObjectRef != nil {
		item.Resource.Namespace = rec.ObjectRef.Namespace
		item.Resource.Resource = rec.ObjectRef.Resource
		item.Resource.Name = rec.ObjectRef.Name
	}
	if rec.ResponseStatus != nil {
		item.ResponseCode = int64(rec.ResponseStatus.Code)
	}
	return item
}
