package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestWriteMeteringErrorMapsInvalidAndSanitizesUnexpectedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
		forbidden  string
	}{
		{name: "invalid", err: ports.ErrInvalid, wantStatus: http.StatusBadRequest, wantBody: "BAD_REQUEST"},
		{name: "not configured", err: ports.ErrNotConfigured, wantStatus: http.StatusServiceUnavailable, wantBody: "SERVICE_UNAVAILABLE"},
		{name: "unavailable", err: ports.ErrUnavailable, wantStatus: http.StatusServiceUnavailable, wantBody: "SERVICE_UNAVAILABLE"},
		{name: "unexpected", err: errors.New("database password leaked"), wantStatus: http.StatusInternalServerError, wantBody: "INTERNAL_ERROR", forbidden: "password"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &app.RequestContext{}
			writeMeteringError(c, test.err)
			body := string(c.Response.Body())
			if c.Response.StatusCode() != test.wantStatus || !strings.Contains(body, test.wantBody) {
				t.Fatalf("response = (%d, %s), want status %d body containing %q", c.Response.StatusCode(), body, test.wantStatus, test.wantBody)
			}
			if test.forbidden != "" && strings.Contains(strings.ToLower(body), test.forbidden) {
				t.Fatalf("response body leaked %q: %s", test.forbidden, body)
			}
		})
	}
}

// fakeMeteringService 记录 handler 传入的查询请求，返回固定结果。
type fakeMeteringService struct {
	queryErr           error
	platformQueryErr   error
	lastQueryReq       ports.MeteringUsageQueryRequest
	lastPlatformReq    ports.MeteringUsageQueryRequest
	platformCalled     bool
	tokenReportRequest ports.TokenUsageReportRequest
}

func (f *fakeMeteringService) QueryUsage(_ context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	f.lastQueryReq = request
	if f.queryErr != nil {
		return ports.MeteringUsageResult{}, f.queryErr
	}
	return ports.MeteringUsageResult{Items: []ports.MeteringUsageRecord{}, DevProfile: meteringFakeDevProfile()}, nil
}

func (f *fakeMeteringService) QueryPlatformUsage(_ context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	f.platformCalled = true
	f.lastPlatformReq = request
	if f.platformQueryErr != nil {
		return ports.MeteringUsageResult{}, f.platformQueryErr
	}
	return ports.MeteringUsageResult{
		Items: []ports.MeteringUsageRecord{
			{TenantID: "11111111-1111-1111-1111-111111111111", ResourceType: ports.MeteringResourceInstanceGPUSeconds, TotalQuantity: 120, Unit: "gpu_second", Period: "2026-08-25"},
		},
		DevProfile: meteringFakeDevProfile(),
	}, nil
}

func (f *fakeMeteringService) ReportTokenUsage(_ context.Context, request ports.TokenUsageReportRequest) (ports.TokenUsageReportRecord, error) {
	f.tokenReportRequest = request
	return ports.TokenUsageReportRecord{State: ports.TokenUsageReportAccepted}, nil
}

func meteringFakeDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{Mode: "postgres", Provider: "pg-metering-service", RealProvider: true}
}

func setupMeteringTestServer(service ports.MeteringService) *server.Hertz {
	h := server.New()
	v1 := h.Group("/api/v1")
	registerMetering(v1, service)
	return h
}

func TestQueryPlatformUsageHandlerResponse(t *testing.T) {
	fake := &fakeMeteringService{}
	h := setupMeteringTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?start_time=2026-08-25T00:00:00Z&end_time=2026-08-26T00:00:00Z&group_by=day",
		nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET platform status = %d, want 200, body = %s", resp.StatusCode(), string(resp.Body()))
	}
	var body meteringUsageResponse
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode response = %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("body = %+v, want 1 item", body)
	}
	if body.Items[0].TenantID != "11111111-1111-1111-1111-111111111111" || body.Items[0].Period != "2026-08-25" {
		t.Fatalf("item = %+v", body.Items[0])
	}
	if !fake.platformCalled {
		t.Fatal("QueryPlatformUsage not called")
	}
	if fake.lastPlatformReq.GroupBy != "day" || fake.lastPlatformReq.PlatformTenantID != "" {
		t.Fatalf("platform request = %+v", fake.lastPlatformReq)
	}
}

func TestQueryPlatformUsageHandlerRejectsInvalidTenantID(t *testing.T) {
	fake := &fakeMeteringService{}
	h := setupMeteringTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?start_time=2026-08-25T00:00:00Z&end_time=2026-08-26T00:00:00Z&tenant_id=not-a-uuid",
		nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode(), string(resp.Body()))
	}
	if !strings.Contains(string(resp.Body()), "BAD_REQUEST") {
		t.Fatalf("body = %s, want BAD_REQUEST", string(resp.Body()))
	}
	if fake.platformCalled {
		t.Fatal("service must not be called on invalid tenant_id")
	}
}

func TestQueryPlatformUsageHandlerRejectsMissingTimeRange(t *testing.T) {
	fake := &fakeMeteringService{}
	h := setupMeteringTestServer(fake)

	// 缺 start_time
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?end_time=2026-08-26T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("missing start status = %d, want 400", resp.StatusCode())
	}
	// 缺 end_time
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?start_time=2026-08-25T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("missing end status = %d, want 400", resp.StatusCode())
	}
	// 格式错误
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?start_time=2026-08-25&end_time=2026-08-26T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("invalid start status = %d, want 400", resp.StatusCode())
	}
	if fake.platformCalled {
		t.Fatal("service must not be called on missing time range")
	}
}

func TestQueryUsageHandlerRejectsMissingTimeRange(t *testing.T) {
	fake := &fakeMeteringService{}
	h := setupMeteringTestServer(fake)

	// 缺 start_time
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage?end_time=2026-08-26T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("missing start status = %d, want 400", resp.StatusCode())
	}
	// 缺 end_time
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage?start_time=2026-08-25T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("missing end status = %d, want 400", resp.StatusCode())
	}
	// 格式错误
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage?start_time=not-a-time&end_time=2026-08-26T00:00:00Z", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("invalid RFC3339 status = %d, want 400", resp.StatusCode())
	}
	if fake.lastQueryReq.TenantID != "" {
		t.Fatal("service must not be called on missing time range")
	}
}

func TestQueryPlatformUsageHandlerMapsServiceUnavailable(t *testing.T) {
	fake := &fakeMeteringService{platformQueryErr: ports.ErrNotConfigured}
	h := setupMeteringTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/metering/usage/platform?start_time=2026-08-25T00:00:00Z&end_time=2026-08-26T00:00:00Z",
		nil).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode(), string(resp.Body()))
	}
	if !strings.Contains(string(resp.Body()), "SERVICE_UNAVAILABLE") {
		t.Fatalf("body = %s, want SERVICE_UNAVAILABLE", string(resp.Body()))
	}
}

func TestMeteringAPIUsageResponseMarksLocalProfile(t *testing.T) {
	api := newMeteringAPI()

	result, err := api.service.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID: "tenant-a",
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	response := meteringUsageFromResult(result)
	if response.Total != 0 {
		t.Fatalf("total = %d, want 0", response.Total)
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-metering-service")
}

func TestMeteringAPITokenUsageReportResponse(t *testing.T) {
	api := newMeteringAPI()

	report, err := api.service.ReportTokenUsage(context.Background(), ports.TokenUsageReportRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "token-usage-router",
		Source:         "model-service",
		Model:          "qwen2.5",
		InputTokens:    7,
		OutputTokens:   11,
	})
	if err != nil {
		t.Fatalf("ReportTokenUsage error = %v", err)
	}
	response := tokenUsageReportFromRecord(report)
	if response.ID == "" || response.TotalTokens != 18 || response.State != "accepted" {
		t.Fatalf("response = %+v, want accepted total 18", response)
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-metering-service")
}
