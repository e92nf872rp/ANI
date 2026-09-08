package router

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestObservabilityAPIQueryResponseMarksLocalProfile(t *testing.T) {
	api := newObservabilityAPI(nil)

	result, err := api.service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "tenant-a",
		Query:    "up",
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	response := observabilityQueryFromResult(result)
	if response.Query != "up" || response.ResultType != "vector" {
		t.Fatalf("response = %+v, want vector query", response)
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-observability-service")
}

func TestObservabilityAPIAlertRuleCRUDResponse(t *testing.T) {
	api := newObservabilityAPI(nil)

	rule, err := api.service.CreateAlertRule(context.Background(), ports.ObservabilityAlertRuleCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "obs-alert-create",
		Name:           "High GPU",
		PromQL:         "avg(DCGM_FI_DEV_GPU_UTIL) > 80",
		Severity:       ports.ObservabilityAlertSeverityWarning,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule error = %v", err)
	}
	response := observabilityAlertRuleFromRecord(rule)
	if response.ID == "" || response.State != "active" || response.Severity != "warning" {
		t.Fatalf("response = %+v, want active warning rule", response)
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-observability-service")
}

// setupResourceTrendTestServer 构造可观测性路由 HTTP server，
// middleware 用 JWT 层提供的 tenant_id（等价 instanceTenantID 的真实来源）。
func setupResourceTrendTestServer() *server.Hertz {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerObservability(h.Group("/api/v1"), nil)
	return h
}

func TestObservabilityResourceTrendValidReturnsEmptyMatrix(t *testing.T) {
	h := setupResourceTrendTestServer()
	resp := ut.PerformRequest(h.Engine, "GET",
		"/api/v1/observability/resource_trend?metric=cpu&start=2026-09-01T06:00:00Z&end=2026-09-01T07:00:00Z&step=30s",
		nil).Result()
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200, body %s", resp.StatusCode(), string(resp.Body()))
	}
	var body struct {
		Query      string `json:"query"`
		ResultType string `json:"result_type"`
		Results    []any  `json:"results"`
		DevProfile struct {
			RealProvider bool `json:"real_provider"`
		} `json:"dev_profile"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("unmarshal body: %v, body %s", err, string(resp.Body()))
	}
	if body.ResultType != "matrix" {
		t.Fatalf("result_type = %s, want matrix", body.ResultType)
	}
	if len(body.Results) != 0 {
		t.Fatalf("results len = %d, want empty local matrix", len(body.Results))
	}
	if body.DevProfile.RealProvider {
		t.Fatalf("real_provider = true, want false for local profile")
	}
}

// TestObservabilityResourceTrendValidation 覆盖缺参/非法枚举/step 0/负/RFC3339 校验 → 400。
func TestObservabilityResourceTrendValidation(t *testing.T) {
	h := setupResourceTrendTestServer()
	base := "/api/v1/observability/resource_trend"
	start := "2026-09-01T06:00:00Z"
	end := "2026-09-01T07:00:00Z"

	cases := map[string]string{
		"missing metric":    "?start=" + start + "&end=" + end + "&step=30s",
		"invalid metric":    "?metric=bogus&start=" + start + "&end=" + end + "&step=30s",
		"missing start":     "?metric=cpu&end=" + end + "&step=30s",
		"missing end":       "?metric=cpu&start=" + start + "&step=30s",
		"missing step":      "?metric=cpu&start=" + start + "&end=" + end,
		"step zero":         "?metric=cpu&start=" + start + "&end=" + end + "&step=0s",
		"step negative":     "?metric=cpu&start=" + start + "&end=" + end + "&step=-30s",
		"start not RFC3339": "?metric=cpu&start=not-a-time&end=" + end + "&step=30s",
		"end not RFC3339":   "?metric=cpu&start=" + start + "&end=not-a-time&step=30s",
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			resp := ut.PerformRequest(h.Engine, "GET", base+q, nil).Result()
			if resp.StatusCode() != 400 {
				t.Fatalf("status = %d, want 400, body %s", resp.StatusCode(), string(resp.Body()))
			}
		})
	}
}

// TestObservabilityResourceTrendIgnoresClientTenantQuery 验证端点只认 JWT tenant_id，
// 不读取任何前端传入的 tenant_id 查询参数（参数被静默忽略，不改变租户来源）。
func TestObservabilityResourceTrendIgnoresClientTenantQuery(t *testing.T) {
	h := setupResourceTrendTestServer()
	resp := ut.PerformRequest(h.Engine, "GET",
		"/api/v1/observability/resource_trend?tenant_id=evil-tenant&metric=cpu&start=2026-09-01T06:00:00Z&end=2026-09-01T07:00:00Z&step=30s",
		nil).Result()
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200 (tenant_id query param ignored), body %s", resp.StatusCode(), string(resp.Body()))
	}
}

// TestObservabilityResourceTrendQueryNotAccepted 验证端点不接收/不透传 query PromQL，
// 即使夹带 query 参数也不应作为 PromQL 回显，避免裸聚合跨租户泄露。
func TestObservabilityResourceTrendQueryNotAccepted(t *testing.T) {
	h := setupResourceTrendTestServer()
	resp := ut.PerformRequest(h.Engine, "GET",
		"/api/v1/observability/resource_trend?metric=gpu&start=2026-09-01T06:00:00Z&end=2026-09-01T07:00:00Z&step=30s&query=something",
		nil).Result()
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200, body %s", resp.StatusCode(), string(resp.Body()))
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.Contains(body.Query, "something") {
		t.Fatalf("response query echoes client PromQL, tenant isolation broken: %q", body.Query)
	}
}
