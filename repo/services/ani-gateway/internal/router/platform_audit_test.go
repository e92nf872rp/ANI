package router

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestPlatformAuditResponseNotNullGroupsAndPassthrough(t *testing.T) {
	result := ports.PlatformAuditLogResult{
		Items: []ports.PlatformAuditLogItem{{
			AuditID:   "audit-1",
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
	if item.AuditID != "audit-1" || item.Verb != "create" || item.ResponseCode != 201 {
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
	// 过渡方案不再有 local 回退：注入什么就透传什么。
	var injected ports.PlatformAuditService = &fakePlatformAuditService{} // 用 fake 最小实现验证透传
	options := RegisterOptions{PlatformAuditService: injected}
	if options.PlatformAuditService == nil {
		t.Fatal("PlatformAuditService = nil, want injected service")
	}
	api := newPlatformAuditAPI(options.PlatformAuditService)
	if api.service != options.PlatformAuditService {
		t.Fatal("api.service should passthrough the injected service")
	}
}

// fakePlatformAuditService 最小 fake，仅用于注入透传断言。
type fakePlatformAuditService struct{}

func (f *fakePlatformAuditService) QueryAuditLogs(_ context.Context, _ ports.PlatformAuditLogQuery) (ports.PlatformAuditLogResult, error) {
	return ports.PlatformAuditLogResult{}, nil
}
