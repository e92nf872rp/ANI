package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalPlatformAuditDeterministic(t *testing.T) {
	api := NewLocalPlatformAudit()
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	result, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from,
		TimeTo:   &to,
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(result.Items))
	}
	if result.Items[0].AuditID != "local-audit-00000001" || result.Items[1].AuditID != "local-audit-00000002" {
		t.Fatalf("samples order = %s/%s, want deterministic newest-first", result.Items[0].AuditID, result.Items[1].AuditID)
	}
	if result.DevProfile.Mode != "local" || result.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real", result.DevProfile)
	}
	if result.Items[0].Detail.RequestURI == "" || result.Items[0].User.Username == "" {
		t.Fatalf("item detail/user must be populated, got %+v", result.Items[0])
	}
}

func TestLocalPlatformAuditHonorsTimeWindow(t *testing.T) {
	api := NewLocalPlatformAudit()
	// 时间窗口完全避开样本时间 → 空结果。
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	result, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from,
		TimeTo:   &to,
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("len(items) = %d, want 0 outside time window", len(result.Items))
	}
	if result.TotalApprox != 0 {
		t.Fatalf("total_approx = %d, want 0 to reflect filtered count", result.TotalApprox)
	}
}

func TestLocalPlatformAuditTotalApproxReflectsFilter(t *testing.T) {
	api := NewLocalPlatformAudit()
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	// verb=patch 无匹配样本 → total 应随过滤裁剪为 0。
	result, err := api.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from,
		TimeTo:   &to,
		Verb:     "patch",
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("len(items) = %d, want 0 for verb=patch", len(result.Items))
	}
	if result.TotalApprox != 0 {
		t.Fatalf("total_approx = %d, want 0 to reflect filtered count", result.TotalApprox)
	}
}
