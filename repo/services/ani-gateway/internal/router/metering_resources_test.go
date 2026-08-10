package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
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
