package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func mustPlatformAudit(t *testing.T, baseURL string, now time.Time) *LokiPlatformAudit {
	t.Helper()
	srv, err := NewLokiPlatformAudit(LokiPlatformAuditConfig{BaseURL: baseURL, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewLokiPlatformAudit() error = %v", err)
	}
	return srv
}

// buildAuditStreamResponse 构造 Loki query_range 的 stream 响应。
func buildAuditStreamResponse(t *testing.T, streams map[string][]struct {
	TsNs int64
	Line string
}) string {
	t.Helper()
	result := make([]struct {
		Stream map[string]string `json:"stream"`
		Values [][]string        `json:"values"`
	}, 0, len(streams))
	for name, entries := range streams {
		values := make([][]string, 0, len(entries))
		for _, e := range entries {
			values = append(values, []string{strconv.FormatInt(e.TsNs, 10), e.Line})
		}
		result = append(result, struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		}{Stream: map[string]string{"node": name, "stream": "kubernetes-audit"}, Values: values})
	}
	body, err := json.Marshal(map[string]any{
		"status": "success",
		"data":   map[string]any{"resultType": "streams", "result": result},
	})
	if err != nil {
		t.Fatalf("marshal stream response: %v", err)
	}
	return string(body)
}

// auditLine 生成一条审计 JSON 行。
func auditLine(auditID, verb, user, resource, namespace string, code float64) string {
	line := map[string]any{
		"auditID": auditID,
		"verb":    verb,
		"user":    map[string]any{"username": user, "groups": []string{"system:masters"}},
		"objectRef": map[string]any{
			"namespace": namespace, "resource": resource, "name": "obj",
		},
		"responseStatus": map[string]any{"code": code},
		"requestURI":     "/api/v1/namespaces/" + namespace + "/" + resource + "/obj",
		"userAgent":      "kubectl",
	}
	b, _ := json.Marshal(line)
	return string(b)
}

func tsNs(t *testing.T, layout string) int64 {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, layout)
	if err != nil {
		t.Fatalf("parse ts %q: %v", layout, err)
	}
	return ts.UnixNano()
}

func TestPlatformAuditBuildLogQL(t *testing.T) {
	base := ports.PlatformAuditLogQuery{User: "u", Verb: "patch", ResourceType: "deployments", Namespace: "ns", Keyword: "token"}
	logql := buildPlatformAuditLogQL(base)
	for _, want := range []string{
		`stream="kubernetes-audit"`,
		`audit_user="u"`,
		`audit_verb="patch"`,
		`audit_resource="deployments"`,
		`audit_namespace="ns"`,
		`|~`,
	} {
		if !strings.Contains(logql, want) {
			t.Fatalf("logql %q missing %q", logql, want)
		}
	}
	// 无过滤时只含 stream 选择器
	if got := buildPlatformAuditLogQL(ports.PlatformAuditLogQuery{}); got != `{stream="kubernetes-audit"}` {
		t.Fatalf("no-filter logql = %q, want {stream=kubernetes-audit}", got)
	}
}

func TestPlatformAuditNormalizePageSize(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{
		{0, defaultAuditPageSize},
		{-1, defaultAuditPageSize},
		{20, 20},
		{999, maxAuditPageSize},
	} {
		if got := normalizePlatformAuditPageSize(tc.in); got != tc.want {
			t.Fatalf("normalizePlatformAuditPageSize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestPlatformAuditGlobalDescMergeAndNextPage(t *testing.T) {
	// 两个 stream、值乱序，limit=page_size+1=2 → 取前 1 条为一页，检测到下页。
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, buildAuditStreamResponse(t, map[string][]struct {
			TsNs int64
			Line string
		}{
			"node-a": {
				{tsNs(t, "2026-09-10T07:59:40Z"), auditLine("id-e2", "create", "u", "pods", "ns", 200)},
				{tsNs(t, "2026-09-10T08:00:00Z"), auditLine("id-e1", "create", "u", "pods", "ns", 200)},
			},
			"node-b": {
				{tsNs(t, "2026-09-10T07:59:50Z"), auditLine("id-e3", "create", "u", "pods", "ns", 200)},
			},
		}))
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	api := mustPlatformAudit(t, srv.URL, now)
	from := now.Add(-time.Hour)
	result, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, PageSize: 1,
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1 (page_size=1)", len(result.Items))
	}
	if result.Items[0].AuditID != "id-e1" {
		t.Fatalf("top item auditID = %q, want id-e1 (newest)", result.Items[0].AuditID)
	}
	if result.NextAfter == "" {
		t.Fatal("next_after must be set when a next page exists (limit+1 detection)")
	}
}

func TestPlatformAuditCursorEndBoundExclusive(t *testing.T) {
	// after 游标 → query_range 的 end 必须等于 after（左闭右开，天然去重）。
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	cursor := time.Date(2026, 9, 10, 7, 59, 40, 0, time.UTC)
	var capturedEnd int64
	handler := func(w http.ResponseWriter, r *http.Request) {
		capturedEnd, _ = strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, buildAuditStreamResponse(t, map[string][]struct {
			TsNs int64
			Line string
		}{"node": {}}))
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	api := mustPlatformAudit(t, srv.URL, now)
	from := now.Add(-time.Hour)
	if _, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, After: cursor.Format(time.RFC3339), PageSize: 10,
	}); err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if capturedEnd != cursor.UnixNano() {
		t.Fatalf("query_range end = %d, want after cursor %d (exclusive bound)", capturedEnd, cursor.UnixNano())
	}
}

func TestPlatformAuditTotalApprox(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "count_over_time") {
			// matrix: sum(count_over_time(...)) 返回 1 个样本 137
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1757480400000000000,"137"]]}]}}`)
			return
		}
		_, _ = fmt.Fprint(w, buildAuditStreamResponse(t, map[string][]struct {
			TsNs int64
			Line string
		}{"node": {}}))
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	api := mustPlatformAudit(t, srv.URL, now)
	from := now.Add(-time.Hour)
	result, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if result.TotalApprox != 137 {
		t.Fatalf("total_approx = %d, want 137", result.TotalApprox)
	}
	if !result.DevProfile.RealProvider {
		t.Fatalf("dev_profile real_provider = false, want true on success")
	}
}

func TestPlatformAuditErrorWhenLokiUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	api := mustPlatformAudit(t, "http://127.0.0.1:1", now) // 端口 1 不可达
	from := now.Add(-time.Hour)
	_, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, PageSize: 10,
	})
	if err == nil {
		t.Fatal("QueryAuditLogs() must return error on Loki unavailability (no degrade)")
	}
}

func TestPlatformAuditErrorWhenLokiNon200(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	api := mustPlatformAudit(t, srv.URL, now)
	from := now.Add(-time.Hour)
	_, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, PageSize: 10,
	})
	if err == nil {
		t.Fatal("QueryAuditLogs() must return error on Loki non-200 (no degrade)")
	}
}

func TestPlatformAuditInvalidCursorAndRange(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, buildAuditStreamResponse(t, map[string][]struct {
			TsNs int64
			Line string
		}{"node": {}}))
	}))
	defer srv.Close()
	api := mustPlatformAudit(t, srv.URL, now)
	from := now.Add(-time.Hour)

	// 非法 after → ErrPlatformAuditInvalid
	if _, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, After: "not-a-time",
	}); err == nil || !isPlatformAuditInvalid(err) {
		t.Fatalf("invalid after should return ErrPlatformAuditInvalid, got %v", err)
	}

	// from>to → ErrPlatformAuditInvalid
	later := now.Add(time.Hour)
	if _, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &later, TimeTo: &now,
	}); err == nil || !isPlatformAuditInvalid(err) {
		t.Fatalf("from>to should return ErrPlatformAuditInvalid, got %v", err)
	}
}

func isPlatformAuditInvalid(err error) bool {
	return strings.Contains(err.Error(), "invalid request")
}

func TestPlatformAuditQueryRangeParamsEncoded(t *testing.T) {
	now := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	from := now.Add(-2 * time.Hour)
	var gotQuery string
	handler := func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, buildAuditStreamResponse(t, map[string][]struct {
			TsNs int64
			Line string
		}{"node": {}}))
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()
	api := mustPlatformAudit(t, srv.URL, now)

	_, _ = api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from, TimeTo: &now, User: "alice", Verb: "delete", PageSize: 5,
	})
	// 校验 query 参数经 url.Values.Encode 正确编码（特殊字符不破坏）。
	_, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("encoded query must be parseable, got %q err=%v", gotQuery, err)
	}
	if !strings.Contains(gotQuery, "alice") {
		t.Fatalf("LogQL missing user filter: %q", gotQuery)
	}
}
