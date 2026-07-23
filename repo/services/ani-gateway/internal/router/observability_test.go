package router

import (
	"context"
	"testing"

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
