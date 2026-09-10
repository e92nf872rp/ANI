package ports

import (
	"context"
	"testing"
	"time"
)

// fakeAuditService 实现 PlatformAuditService，用于编译期断言端口结构完整性。
type fakeAuditService struct{}

func (fakeAuditService) QueryAuditLogs(context.Context, PlatformAuditLogQuery) (PlatformAuditLogResult, error) {
	return PlatformAuditLogResult{}, nil
}

// 编译期断言 fakeAuditService 与实现接口对齐。
var _ PlatformAuditService = fakeAuditService{}

func TestPlatformAuditServiceInterfaceSatisfiedByFake(t *testing.T) {
	var svc PlatformAuditService = fakeAuditService{}
	res, err := svc.QueryAuditLogs(context.Background(), PlatformAuditLogQuery{})
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}
	if res.Items != nil && len(res.Items) != 0 {
		t.Fatalf("items = %+v, want empty", res.Items)
	}
}

func TestPlatformAuditQueryFieldDefaults(t *testing.T) {
	q := PlatformAuditLogQuery{}
	if q.PageSize != 0 {
		t.Fatalf("PageSize zero-value must be 0 (adapter applies default)")
	}
	now := time.Now()
	q = PlatformAuditLogQuery{TimeFrom: &now, TimeTo: &now, After: "2026-09-10T08:00:00Z"}
	if q.TimeFrom == nil || q.TimeTo == nil || q.After == "" {
		t.Fatalf("cursor+time window fields must round-trip: %+v", q)
	}
}

func TestPlatformAuditItemResponseCodeType(t *testing.T) {
	item := PlatformAuditLogItem{
		AuditID:      "id-1",
		Timestamp:    time.Now(),
		Verb:         "create",
		User:         PlatformAuditUser{Username: "u", Groups: []string{"g"}},
		Resource:     PlatformAuditResource{Namespace: "ns", Resource: "pods", Name: "obj"},
		ResponseCode: 201,
		Detail:       PlatformAuditDetail{RequestURI: "/api", UserAgent: "kube"},
	}
	var itemIf any = item
	_ = itemIf
	if item.ResponseCode != 201 || len(item.User.Groups) != 1 {
		t.Fatalf("item = %+v", item)
	}
}
