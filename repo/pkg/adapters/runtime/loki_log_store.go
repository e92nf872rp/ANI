package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// LokiLogStoreConfig 是 LokiLogStore 的构造配置。
type LokiLogStoreConfig struct {
	// BaseURL 是 Loki HTTP API 根地址，例如 http://ani-loki.ani-s07-observability:3100。
	BaseURL string
	// HTTPClient 可选，注入自定义 http.Client（测试、超时控制）。
	HTTPClient *http.Client
	// Now 可选，用于计算默认查询起点（cursor 为空时回退到最近 24 小时）。
	Now func() time.Time
}

// LokiLogStore 实现 ports.LogStore，通过 Loki HTTP API 查询持久化日志。
//
// 多租户隔离：通过 LogQL 的 namespace label 过滤实现，不使用 Loki X-Scope-OrgID
// （单租户模式 auth_enabled: false）。
//
// Cursor 映射：外部 cursor 是 RFC3339 时间戳字符串，adapter 内部转换为 Loki
// query_range 的 start 参数（Unix 纳秒）；next_cursor 取结果最后一条的 timestamp
// （RFC3339），对调用方透明。
type LokiLogStore struct {
	baseURL string
	client  *http.Client
	now     func() time.Time
}

// 编译时断言 LokiLogStore 实现 ports.LogStore（Issue #5 AC：实现 LogStore interface）。
var _ ports.LogStore = (*LokiLogStore)(nil)

// NewLokiLogStore 创建一个走 Loki HTTP API 的 LogStore 实现。
//
// baseURL 末尾的 `/` 会被去除；client 为 nil 时使用默认 http.Client
// （10s 超时，与推荐示例 yaml 的性能假设对齐）。
func NewLokiLogStore(config LokiLogStoreConfig) (*LokiLogStore, error) {
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
	return &LokiLogStore{
		baseURL: baseURL,
		client:  client,
		now:     now,
	}, nil
}

// QueryLogs 调用 Loki /loki/api/v1/query_range，按租户 namespace + 实例 pod 维度
// 查询持久化日志。
//
// 方向语义：direction=backward，首屏返回最近 limit 条日志（与 `kubectl logs --tail`
// 对齐）；cursor 表示「下一页 end 边界」，首屏 cursor 为空时 end 默认 now，后续
// cursor = 上一页最早一条的 timestamp。返回的 items 已反转为时间正序展示，
// next_cursor = 本批最早一条 timestamp（条数达到 limit 时），供前端「加载更多」
// 往前翻页。
//
// Loki 不可达或返回非 200 时返回包装错误，不伪造空结果（PRD FR-9 / AC）。
func (s *LokiLogStore) QueryLogs(ctx context.Context, req ports.LogQueryRequest) (ports.LogQueryResult, error) {
	if err := validateInstanceObservationIdentity(req.TenantID, req.InstanceID); err != nil {
		return ports.LogQueryResult{}, err
	}
	if strings.TrimSpace(req.Namespace) == "" {
		return ports.LogQueryResult{}, fmt.Errorf("%w: namespace is required for loki tenant isolation", ports.ErrInvalid)
	}

	// 1. 构造 LogQL：{namespace="<ns>",pod=~"^<instance>(-.*)?$"} | json
	// 多租户隔离完全依赖 namespace label 过滤，不使用 Loki X-Scope-OrgID。
	logql := buildLokiLogQL(req.Namespace, req.InstanceID)

	// 2. cursor（RFC3339）→ Loki end（Unix 纳秒）；首屏 cursor 为空时 end = now。
	endNs, err := s.cursorToEndNs(req.Cursor)
	if err != nil {
		return ports.LogQueryResult{}, err
	}

	// 3. 构造 query_range 参数
	// direction=backward：从 end 往前查最近 limit 条，与 kubectl logs --tail 语义对齐。
	// start 固定到 end-24h，避免 Loki 全量扫描；首屏覆盖最近 24h 的最新日志。
	limit := normalizeLimit(req.Limit, 100, 1000)
	startNs := endNs - int64(24*time.Hour)
	params := url.Values{}
	params.Set("query", logql)
	params.Set("start", strconv.FormatInt(startNs, 10))
	params.Set("end", strconv.FormatInt(endNs, 10))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("direction", "backward")

	// 4. HTTP GET /loki/api/v1/query_range
	reqURL := s.baseURL + "/loki/api/v1/query_range?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ports.LogQueryResult{}, fmt.Errorf("loki query request: %w", err)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return ports.LogQueryResult{}, fmt.Errorf("loki query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ports.LogQueryResult{}, fmt.Errorf("loki returned status %d", resp.StatusCode)
	}

	// 5. 解析 Loki stream 响应
	lokiResp, err := decodeLokiResponse(resp.Body)
	if err != nil {
		return ports.LogQueryResult{}, fmt.Errorf("decode loki response: %w", err)
	}

	// 6. 映射为 InstanceLogEntry（backward 返回需反转为正序），并计算 next_cursor。
	items, nextCursor := mapLokiStreamsToLogEntries(lokiResp, req.Level, limit)

	return ports.LogQueryResult{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

// buildLokiLogQL 构造 Loki LogQL：{namespace="<ns>",pod=~"^<instance>(-.*)?$"} | json。
//
// namespace 用精确匹配做多租户隔离；pod 用正则匹配，兼容直接 Pod（无后缀）
// 与 Deployment/Job 控制器生成的 pod（name-<hash>[-<hash>]），复用 promQLPodMatcher
// 的正则构造逻辑（Loki LogQL 与 PromQL 正则语法一致）。
// namespace/pod 值用 %q（Go 字符串字面量）转义，避免含特殊字符时注入 LogQL。
// Level 过滤在解析后做（Loki adapter 可选实现，不把 level 下推到 LogQL 以兼容非结构化日志行）。
func buildLokiLogQL(namespace string, instanceID string) string {
	return fmt.Sprintf(`{namespace=%q,pod=~%q} | json`, namespace, "^"+escapeLogQLRegex(instanceID)+"(-.*)?$")
}

// escapeLogQLRegex 转义 LogQL/LogQL 正则中的元字符，避免实例名含特殊字符时注入。
// 与 promQLPodMatcher 的转义逻辑保持一致（Loki LogQL 正则语法与 PromQL 一致）。
func escapeLogQLRegex(value string) string {
	return strings.NewReplacer(
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
	).Replace(value)
}

// cursorToEndNs 把外部 cursor（RFC3339 时间戳）映射为 Loki query_range 的 end 参数
// （Unix 纳秒）。cursor 为空时 end = now（首屏从最新日志开始）。
func (s *LokiLogStore) cursorToEndNs(cursor string) (int64, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return s.now().UnixNano(), nil
	}
	t, err := time.Parse(time.RFC3339, cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid cursor %q: %v", ports.ErrInvalid, cursor, err)
	}
	return t.UnixNano(), nil
}

// lokiResponse 是 Loki /loki/api/v1/query_range 的 stream 响应结构。
type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [timestamp_ns, json_line]
		} `json:"result"`
	} `json:"data"`
}

// decodeLokiResponse 从 HTTP body 解析 Loki stream 响应。
func decodeLokiResponse(body interface{ Read(p []byte) (int, error) }) (lokiResponse, error) {
	var resp lokiResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return lokiResponse{}, err
	}
	return resp, nil
}

// mapLokiStreamsToLogEntries 把 Loki stream values 映射为 InstanceLogEntry，
// 并在解析侧按 level 过滤（adapter 可选实现）。
//
// 返回值：
//   - items：按时间倒序合并所有 stream 的日志条目（最新在前），与日志应用展示习惯
//     对齐；前端「加载更多」时追加更早的日志到列表末尾。
//     Loki direction=backward 已在服务端按时间倒序返回，这里直接透传不反转。
//   - nextCursor：当且仅当返回条数达到 limit 且存在有效时间戳时，取最早一条
//     （列表最后一条）的 RFC3339 时间戳，作为下一页 end 边界往前翻页；
//     否则为空字符串表示已到末尾。
//
// 注意：多 stream 合并不重新按时间排序，依赖 Loki backward 的全局倒序保证
// （单 pod 查询通常单 stream，多 stream 场景的跨流排序由 Loki 合并器保证）。
func mapLokiStreamsToLogEntries(resp lokiResponse, level string, limit int) ([]ports.InstanceLogEntry, string) {
	level = strings.TrimSpace(level)
	items := make([]ports.InstanceLogEntry, 0, limit)
	var earliestT time.Time
	var hasEarliest bool

	for _, stream := range resp.Data.Result {
		container := stream.Stream["container"]
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			tsNsStr, line := v[0], v[1]

			tsInt, parseErr := strconv.ParseInt(tsNsStr, 10, 64)
			if parseErr != nil {
				// 时间戳格式异常的行跳过，不阻塞整体查询。
				continue
			}
			ts := time.Unix(0, tsInt).UTC()

			entry := parseLokiLogLine(line, ts, container)
			if level != "" && entry.Level != level {
				continue
			}

			items = append(items, entry)
			// backward 返回最新在前，最后遍历到的是最早一条；记录最早一条作为下一页 cursor。
			earliestT = ts
			hasEarliest = true
		}
	}

	// 当返回条数达到 limit 时，next_cursor = 最早一条 timestamp（RFC3339），
	// 作为下一页 end 边界往前翻页。条数不足 limit 说明已到末尾，NextCursor 为空
	//（PRD FR-10 / AC）。
	var nextCursor string
	if len(items) >= limit && hasEarliest {
		nextCursor = earliestT.Format(time.RFC3339)
	}
	return items, nextCursor
}

// parseLokiLogLine 解析单条 Loki 日志行（JSON 或纯文本），映射为 InstanceLogEntry。
//
// Loki `| json` 管道会把日志行解析为 JSON 字段；此处从 JSON 中提取
// level/message/stream。当日志行是 JSON 但不含 level 字段（Fluent-Bit 采集的
// nginx/stdout 等非结构化日志常见），从 message 内容推断 level（info/warn/error），
// 避免前端级别列显示为空。
// 提取失败时回退为纯文本语义（message=整行，level 推断）。
func parseLokiLogLine(line string, ts time.Time, container string) ports.InstanceLogEntry {
	var parsed struct {
		Level   string `json:"level"`
		Message string `json:"message"`
		Stream  string `json:"stream"`
	}
	if json.Unmarshal([]byte(line), &parsed) == nil && (parsed.Level != "" || parsed.Message != "") {
		level := parsed.Level
		if level == "" {
			// JSON 无 level 字段时从 message 推断（如 nginx stdout 日志）。
			level = inferLogLevel(parsed.Message)
		}
		stream := parsed.Stream
		if stream == "" {
			stream = "stdout"
		}
		return ports.InstanceLogEntry{
			Timestamp: ts,
			Level:     level,
			Message:   parsed.Message,
			Container: container,
			Stream:    stream,
		}
	}
	// 非 JSON 或缺少结构化字段：回退为纯文本语义。
	return ports.InstanceLogEntry{
		Timestamp: ts,
		Level:     inferLogLevel(line),
		Message:   line,
		Container: container,
		Stream:    "stdout",
	}
}
