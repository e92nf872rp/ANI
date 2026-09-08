package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalObservabilityServiceQueriesPromQLWithDevProfile(t *testing.T) {
	service := NewLocalObservabilityService()

	result, err := service.Query(context.Background(), ports.ObservabilityQueryRequest{
		TenantID: "tenant-a",
		Query:    "up",
	})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.Query != "up" || result.ResultType != ports.ObservabilityResultVector {
		t.Fatalf("result = %+v, want vector query result", result)
	}
	if len(result.Results) != 0 {
		t.Fatalf("results = %d, want empty local profile result", len(result.Results))
	}
	if result.DevProfile.Mode != "local" || result.DevProfile.Provider != "local-observability-service" || result.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", result.DevProfile)
	}
}

func TestLocalObservabilityServiceManagesAlertRulesWithIdempotency(t *testing.T) {
	service := NewLocalObservabilityService(WithObservabilityClock(func() time.Time {
		return time.Unix(2200, 0).UTC()
	}))
	request := ports.ObservabilityAlertRuleCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "alert-rule-create",
		Name:           "High CPU",
		PromQL:         "avg(rate(container_cpu_usage_seconds_total[5m])) > 0.8",
		Duration:       5 * time.Minute,
		Severity:       ports.ObservabilityAlertSeverityWarning,
		Labels:         map[string]string{"service": "inference"},
		Enabled:        true,
	}

	first, err := service.CreateAlertRule(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateAlertRule(first) error = %v", err)
	}
	second, err := service.CreateAlertRule(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateAlertRule(second) error = %v", err)
	}
	if second.RuleID != first.RuleID {
		t.Fatalf("idempotent rule id = %q, want %q", second.RuleID, first.RuleID)
	}
	if second.State != ports.ObservabilityAlertRuleActive {
		t.Fatalf("state = %s, want active", second.State)
	}

	list, err := service.ListAlertRules(context.Background(), ports.ObservabilityAlertRuleListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListAlertRules error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("rules = %d, want 1", len(list))
	}
	updated, err := service.UpdateAlertRule(context.Background(), ports.ObservabilityAlertRuleUpdateRequest{
		TenantID:       "tenant-a",
		RuleID:         first.RuleID,
		IdempotencyKey: "alert-rule-update",
		Severity:       ports.ObservabilityAlertSeverityCritical,
		Enabled:        boolPtr(false),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule error = %v", err)
	}
	if updated.Severity != ports.ObservabilityAlertSeverityCritical || updated.State != ports.ObservabilityAlertRuleDisabled {
		t.Fatalf("updated = %+v, want critical disabled", updated)
	}
	deleted, err := service.DeleteAlertRule(context.Background(), ports.ObservabilityAlertRuleGetRequest{
		TenantID: "tenant-a",
		RuleID:   first.RuleID,
	})
	if err != nil {
		t.Fatalf("DeleteAlertRule error = %v", err)
	}
	if deleted.State != ports.ObservabilityAlertRuleDeleted {
		t.Fatalf("deleted state = %s, want deleted", deleted.State)
	}
	if _, err := service.GetAlertRule(context.Background(), ports.ObservabilityAlertRuleGetRequest{TenantID: "tenant-a", RuleID: first.RuleID}); err == nil {
		t.Fatalf("GetAlertRule deleted rule succeeded, want not found")
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestLocalObservabilityServiceQueryResourceTrendReturnsEmptyMatrix(t *testing.T) {
	service := NewLocalObservabilityService()
	result, err := service.QueryResourceTrend(context.Background(), ports.ObservabilityResourceTrendRequest{
		TenantID: "tenant-a",
		Metric:   ports.ObservabilityResourceTrendCPU,
		Start:    time.Now().Add(-time.Hour),
		End:      time.Now(),
		Step:     time.Minute,
	})
	if err != nil {
		t.Fatalf("QueryResourceTrend error = %v", err)
	}
	if result.ResultType != ports.ObservabilityResultMatrix {
		t.Fatalf("result_type = %s, want matrix", result.ResultType)
	}
	if len(result.Results) != 0 {
		t.Fatalf("results = %d, want empty local matrix", len(result.Results))
	}
	if result.DevProfile.Mode != "local" || result.DevProfile.Provider != "local-observability-service" || result.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", result.DevProfile)
	}
}

func TestLocalObservabilityServiceQueryResourceTrendRejectsEmptyTenant(t *testing.T) {
	service := NewLocalObservabilityService()
	if _, err := service.QueryResourceTrend(context.Background(), ports.ObservabilityResourceTrendRequest{
		Metric: ports.ObservabilityResourceTrendGPU,
		Start:  time.Now().Add(-time.Hour),
		End:    time.Now(),
		Step:   time.Minute,
	}); err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
