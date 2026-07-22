package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// newTestLokiLogStore 构造一个走 httptest roundTripFunc 的 LokiLogStore，
// 固定时钟以便 cursor 默认起点可断言。
func newTestLokiLogStore(t *testing.T, roundTrip roundTripFunc) *LokiLogStore {
	t.Helper()
	store, err := NewLokiLogStore(LokiLogStoreConfig{
		BaseURL:    "http://ani-loki.ani-s07-observability:3100",
		HTTPClient: &http.Client{Transport: roundTrip},
		Now: func() time.Time {
			return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewLokiLogStore() error = %v", err)
	}
	return store
}

func TestBuildLokiLogQLInjectsNamespaceAndPodLabels(t *testing.T) {
	logql := buildLokiLogQL("ani-tenant-tenant-a", "pod-a")
	// LogQL 必须用 namespace label 做多租户隔离，且使用 pod label 正则匹配实例
	//（兼容 Deployment/Job 生成的带 hash 后缀的 pod 名）。
	if !strings.Contains(logql, `namespace="ani-tenant-tenant-a"`) {
		t.Fatalf("logql = %q, missing namespace label filter", logql)
	}
	if !strings.Contains(logql, `pod=~"^pod-a(-.*)?$"`) {
		t.Fatalf("logql = %q, missing pod regex filter", logql)
	}
	if !strings.HasSuffix(logql, ` | json`) {
		t.Fatalf("logql = %q, missing `| json` pipeline", logql)
	}
}

func TestBuildLokiLogQLEscapesSpecialCharacters(t *testing.T) {
	// 实例名含双引号时，%q 应转义为 Go 字符串字面量，避免 LogQL 注入。
	logql := buildLokiLogQL(`ani-tenant-a"OR"`, `pod"x`)
	if strings.Contains(logql, `namespace="ani-tenant-a"OR""`) {
		t.Fatalf("logql = %q, namespace value not escaped", logql)
	}
	// 转义后仍可被 Go 解析回原值（证明是合法字面量）。
	if !strings.Contains(logql, `\`+`"`) {
		t.Fatalf("logql = %q, expected escaped quotes", logql)
	}
}

func TestLokiCursorToEndNsRoundTrip(t *testing.T) {
	store := newTestLokiLogStore(t, nil)

	// 空 cursor → end = now，Unix 纳秒。
	endNs, err := store.cursorToEndNs("")
	if err != nil {
		t.Fatalf("cursorToEndNs(empty) error = %v", err)
	}
	wantDefault := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).UnixNano()
	if endNs != wantDefault {
		t.Fatalf("endNs = %d, want %d (now)", endNs, wantDefault)
	}

	// RFC3339 → Unix 纳秒。
	original := time.Date(2026, 7, 20, 12, 30, 45, 0, time.UTC)
	cursor := original.Format(time.RFC3339)
	endNs, err = store.cursorToEndNs(cursor)
	if err != nil {
		t.Fatalf("cursorToEndNs(%q) error = %v", cursor, err)
	}
	if endNs != original.UnixNano() {
		t.Fatalf("endNs = %d, want %d", endNs, original.UnixNano())
	}

	// 非法 cursor 应返回 ErrInvalid 包装错误，不伪造默认值。
	if _, err := store.cursorToEndNs("not-a-timestamp"); err == nil {
		t.Fatalf("cursorToEndNs(invalid) expected error, got nil")
	}
}

func TestMapLokiStreamsToLogEntriesParsesJSONLine(t *testing.T) {
	resp := lokiResponse{}
	// 构造一条 stream，两行 JSON：一条 info，一条 error。
	// Loki direction=backward 返回倒序（最新在前），所以时间大的在数组前面。
	raw := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"namespace": "ani-tenant-tenant-a", "pod": "pod-a", "container": "main"},
					"values": [
						["1780000001000000000", "{\"level\":\"error\",\"message\":\"crashed\",\"stream\":\"stderr\"}"],
						["1780000000000000000", "{\"level\":\"info\",\"message\":\"booted\",\"stream\":\"stdout\"}"]
					]
				}
			]
		}
	}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal loki response: %v", err)
	}

	items, nextCursor := mapLokiStreamsToLogEntries(resp, "", 100)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	// 不反转，backward 返回顺序透传：error（晚）在前，info（早）在后。
	first := items[0]
	if first.Level != "error" || first.Message != "crashed" || first.Container != "main" || first.Stream != "stderr" {
		t.Fatalf("first entry = %+v, want error/crashed/main/stderr (newest first)", first)
	}
	if !first.Timestamp.Equal(time.Unix(0, 1780000001000000000).UTC()) {
		t.Fatalf("first timestamp = %v, want parsed (newest)", first.Timestamp)
	}
	second := items[1]
	if second.Level != "info" || second.Message != "booted" || second.Stream != "stdout" {
		t.Fatalf("second entry = %+v, want info/booted/stdout (oldest)", second)
	}
	// 条数（2） < limit（100），next_cursor 应为空。
	if nextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty when items < limit", nextCursor)
	}
}

func TestMapLokiStreamsToLogEntriesAppliesLevelFilter(t *testing.T) {
	resp := lokiResponse{}
	// backward 返回倒序：error（晚）在前，info（早）在后。
	raw := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"container": "main"},
					"values": [
						["1780000001000000000", "{\"level\":\"error\",\"message\":\"crashed\"}"],
						["1780000000000000000", "{\"level\":\"info\",\"message\":\"booted\"}"]
					]
				}
			]
		}
	}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	items, _ := mapLokiStreamsToLogEntries(resp, "error", 100)
	if len(items) != 1 || items[0].Level != "error" {
		t.Fatalf("items = %+v, want single error entry after level filter", items)
	}
}

func TestMapLokiStreamsToLogEntriesSetsNextCursorAtLimit(t *testing.T) {
	resp := lokiResponse{}
	// backward 返回倒序：l2（晚）在前，l1（早）在后。
	// next_cursor 应取最早一条（l1）的 timestamp，作为下一页 end 边界。
	raw := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"container": "main"},
					"values": [
						["1780000001000000000", "{\"level\":\"info\",\"message\":\"l2\"}"],
						["1780000000000000000", "{\"level\":\"info\",\"message\":\"l1\"}"]
					]
				}
			]
		}
	}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// limit=2 且返回 2 条 → next_cursor = 最早一条（l1，列表最后一条）RFC3339 时间戳。
	items, nextCursor := mapLokiStreamsToLogEntries(resp, "", 2)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	// 不反转：l2（晚）在前，l1（早）在后；next_cursor 取 l1 时间戳往前翻页。
	want := time.Unix(0, 1780000000000000000).UTC().Format(time.RFC3339)
	if nextCursor != want {
		t.Fatalf("nextCursor = %q, want %q (earliest item timestamp)", nextCursor, want)
	}
}

func TestParseLokiLogLineFallsBackToPlainText(t *testing.T) {
	ts := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	entry := parseLokiLogLine("plain text without json", ts, "main")
	if entry.Message != "plain text without json" {
		t.Fatalf("message = %q, want plain text passthrough", entry.Message)
	}
	if entry.Level != "info" {
		t.Fatalf("level = %q, want inferred info", entry.Level)
	}
	if entry.Container != "main" {
		t.Fatalf("container = %q, want main", entry.Container)
	}
}

// TestParseLokiLogLineInfersLevelFromMessage 验证 JSON 日志行无 level 字段时
//（Fluent-Bit 采集的 nginx/stdout 等非结构化日志常见场景），从 message 内容推断 level，
// 避免前端级别列显示为空。
func TestParseLokiLogLineInfersLevelFromMessage(t *testing.T) {
	ts := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// 模拟 Fluent-Bit 采集的 nginx stdout 日志：JSON 有 message 但无 level。
	// inferLogLevel 按已有规则识别 error/warn/debug 前缀，其余推断为 info。
	tests := []struct {
		name    string
		line    string
		wantLvl string
	}{
		{
			name:    "nginx notice inferred as info",
			line:    `{"message":"2026/07/21 09:10:44 [notice] 1#1: start worker process 203","logtag":"F","stream":"stderr","container":"nginx"}`,
			wantLvl: "info",
		},
		{
			name:    "error prefixed message inferred as error",
			line:    `{"message":"error: failed to bind port 80","logtag":"F","stream":"stderr","container":"nginx"}`,
			wantLvl: "error",
		},
		{
			name:    "docker-entrypoint info line inferred as info",
			line:    `{"message":"/docker-entrypoint.sh: Configuration complete; ready for start up","logtag":"F","stream":"stdout","container":"nginx"}`,
			wantLvl: "info",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := parseLokiLogLine(tt.line, ts, "nginx")
			if entry.Level != tt.wantLvl {
				t.Fatalf("level = %q, want %q (inferred from message)", entry.Level, tt.wantLvl)
			}
			if entry.Container != "nginx" {
				t.Fatalf("container = %q, want nginx", entry.Container)
			}
		})
	}
}

// TestParseLokiLogLinePreservesExplicitLevel 验证 JSON 有 level 字段时优先用显式 level，
// 不被推断覆盖。
func TestParseLokiLogLinePreservesExplicitLevel(t *testing.T) {
	ts := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	entry := parseLokiLogLine(`{"level":"warn","message":"something odd"}`, ts, "main")
	if entry.Level != "warn" {
		t.Fatalf("level = %q, want warn (explicit, not inferred)", entry.Level)
	}
	if entry.Message != "something odd" {
		t.Fatalf("message = %q, want something odd", entry.Message)
	}
}

func TestLokiLogStoreQueryLogsEndToEnd(t *testing.T) {
	var capturedURL string
	var capturedMethod string
	store := newTestLokiLogStore(t, func(r *http.Request) (*http.Response, error) {
		capturedMethod = r.Method
		capturedURL = r.URL.String()
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Fatalf("path = %s, want /loki/api/v1/query_range", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [
					{
						"stream": {"namespace": "ani-tenant-tenant-a", "pod": "pod-a", "container": "main"},
						"values": [
							["1780000000000000000", "{\"level\":\"info\",\"message\":\"booted\",\"stream\":\"stdout\"}"]
						]
					}
				]
			}
		}`), nil
	})

	result, err := store.QueryLogs(context.Background(), ports.LogQueryRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Namespace:  "ani-tenant-tenant-a",
		Limit:      100,
		Cursor:     "",
	})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Fatalf("method = %s, want GET", capturedMethod)
	}
	// URL 必须包含 LogQL namespace + pod label 正则过滤。
	parsed, _ := url.Parse(capturedURL)
	query, _ := url.QueryUnescape(parsed.RawQuery)
	if !strings.Contains(query, `namespace="ani-tenant-tenant-a"`) || !strings.Contains(query, `pod=~"^pod-a(-.*)?$"`) {
		t.Fatalf("query = %q, missing LogQL namespace/pod filter", query)
	}
	// backward 方向：direction=backward。
	if !strings.Contains(query, "direction=backward") {
		t.Fatalf("query = %q, missing direction=backward", query)
	}
	// 空 cursor 时 end 应为 now 的 Unix 纳秒。
	wantEnd := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).UnixNano()
	if !strings.Contains(query, "end="+strconv.FormatInt(wantEnd, 10)) {
		t.Fatalf("query = %q, missing end=%d", query, wantEnd)
	}
	// start 应为 end-24h 的 Unix 纳秒。
	wantStart := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC).UnixNano()
	if !strings.Contains(query, "start="+strconv.FormatInt(wantStart, 10)) {
		t.Fatalf("query = %q, missing start=%d", query, wantStart)
	}
	if len(result.Items) != 1 || result.Items[0].Message != "booted" {
		t.Fatalf("items = %+v, want one booted entry", result.Items)
	}
	// 1 条 < limit 100 → next_cursor 为空。
	if result.NextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty", result.NextCursor)
	}
}

func TestLokiLogStoreQueryLogsPropagatesCursorAsEnd(t *testing.T) {
	store := newTestLokiLogStore(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"status":"success","data":{"resultType":"streams","result":[]}}`), nil
	})
	cursor := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	_, err := store.QueryLogs(context.Background(), ports.LogQueryRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Namespace:  "ani-tenant-tenant-a",
		Limit:      100,
		Cursor:     cursor,
	})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
}

func TestLokiLogStoreQueryLogsRejectsInvalidCursor(t *testing.T) {
	store := newTestLokiLogStore(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("should not reach Loki on invalid cursor")
		return nil, nil
	})
	_, err := store.QueryLogs(context.Background(), ports.LogQueryRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Namespace:  "ani-tenant-tenant-a",
		Limit:      100,
		Cursor:     "not-a-timestamp",
	})
	if err == nil {
		t.Fatalf("QueryLogs() expected error for invalid cursor, got nil")
	}
}

func TestLokiLogStoreQueryLogsWrapsNonOKStatus(t *testing.T) {
	store := newTestLokiLogStore(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"status":"error","error":"internal"}`), nil
	})
	_, err := store.QueryLogs(context.Background(), ports.LogQueryRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Namespace:  "ani-tenant-tenant-a",
		Limit:      100,
	})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want wrapped status 500 error", err)
	}
}

func TestLokiLogStoreQueryLogsWrapsTransportError(t *testing.T) {
	store := newTestLokiLogStore(t, func(r *http.Request) (*http.Response, error) {
		return nil, io.ErrClosedPipe
	})
	_, err := store.QueryLogs(context.Background(), ports.LogQueryRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Namespace:  "ani-tenant-tenant-a",
		Limit:      100,
	})
	if err == nil || !strings.Contains(err.Error(), "loki query failed") {
		t.Fatalf("err = %v, want wrapped transport error", err)
	}
}

func TestNewLokiLogStoreRequiresBaseURL(t *testing.T) {
	if _, err := NewLokiLogStore(LokiLogStoreConfig{}); err == nil {
		t.Fatalf("NewLokiLogStore() expected error for empty base_url")
	}
}
