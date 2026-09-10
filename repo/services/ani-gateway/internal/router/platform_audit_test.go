package router

import (
	"context"
	"testing"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPlatformAuditLocalFallbackProfile(t *testing.T) {
	api := newPlatformAuditAPI(nil)
	if api.service == nil {
		t.Fatal("service is nil, want local fallback from newPlatformAuditAPI(nil)")
	}
	if _, ok := api.service.(*runtimeadapter.LocalPlatformAudit); !ok {
		t.Fatalf("expected *runtimeadapter.LocalPlatformAudit, got %T", api.service)
	}

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	result, err := api.service.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from,
		TimeTo:   &to,
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	// 本地确定性假数据为固定 2 条 write 审计行，宽窗口下全部命中。
	if len(result.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2 deterministic local samples", len(result.Items))
	}
	if result.DevProfile.Mode != "local" || result.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real profile", result.DevProfile)
	}
}

func TestPlatformAuditLocalFiltering(t *testing.T) {
	api := newPlatformAuditAPI(nil)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	// verb 过滤：固定数据仅 1 条 create。
	res, err := api.service.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom: &from,
		TimeTo:   &to,
		Verb:     "create",
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Verb != "create" {
		t.Fatalf("verb-filtered items = %+v, want exactly 1 create", res.Items)
	}

	// namespace 过滤：本地样本之一在 ani-system。
	res, err = api.service.QueryAuditLogs(context.Background(), ports.PlatformAuditLogQuery{
		TimeFrom:  &from,
		TimeTo:    &to,
		Namespace: "ani-system",
	})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Resource.Namespace != "ani-system" {
		t.Fatalf("namespace-filtered items = %+v, want exactly 1 in ani-system", res.Items)
	}
}

func TestPlatformAuditResponseNotNullGroupsAndPassthrough(t *testing.T) {
	result := ports.PlatformAuditLogResult{
		Items: []ports.PlatformAuditLogItem{{
			AuditID:   "local-audit-1",
			Timestamp: time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC),
			Verb:      "create",
			User: ports.PlatformAuditUser{
				Username: "system:serviceaccount:ani-system:ani-gateway",
				Groups:   []string{},
			},
			Resource: ports.PlatformAuditResource{
				Namespace: "ani-system", Resource: "deployments", Name: "ani-gateway",
			},
			ResponseCode: 201,
			Detail: ports.PlatformAuditDetail{
				RequestURI: "/api/v1/namespaces/ani-system/deployments?token=***",
				UserAgent:  "kubectl/v1.30.0",
			},
		}},
		NextAfter:   "2026-09-10T07:59:40Z",
		TotalApprox: 120,
		DevProfile: ports.DevProfileInfo{
			Mode: "real", Provider: "loki", RealProvider: true, Reason: "",
		},
	}
	response := platformAuditResponseFromResult(result)
	if len(response.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(response.Items))
	}
	item := response.Items[0]
	if item.User.Groups == nil {
		t.Fatal("user.groups must serialize as [] not null")
	}
	if item.AuditID != "local-audit-1" || item.Verb != "create" || item.ResponseCode != 201 {
		t.Fatalf("item = %+v, want passthrough", item)
	}
	if item.Detail.RequestURI == "" || item.Detail.UserAgent == "" {
		t.Fatalf("detail must be populated, got %+v", item.Detail)
	}
	if !response.DevProfile.RealProvider || response.DevProfile.Provider != "loki" {
		t.Fatalf("dev_profile = %+v, want real loki passthrough", response.DevProfile)
	}
	if response.NextAfter != "2026-09-10T07:59:40Z" || response.TotalApprox != 120 {
		t.Fatalf("next_after/total = %s/%d, want passthrough", response.NextAfter, response.TotalApprox)
	}
}

func TestPlatformAuditRegisterOptionsWiresService(t *testing.T) {
	local := runtimeadapter.NewLocalPlatformAudit()
	options := RegisterOptions{PlatformAuditService: local}
	if options.PlatformAuditService == nil {
		t.Fatal("PlatformAuditService = nil, want injected service")
	}
	api := newPlatformAuditAPI(options.PlatformAuditService)
	if api.service != options.PlatformAuditService {
		t.Fatal("api.service should passthrough the injected service")
	}
}
