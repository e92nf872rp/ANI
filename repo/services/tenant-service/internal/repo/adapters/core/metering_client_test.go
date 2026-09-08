package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// MeteringClient 单测：httptest 模拟 Core GET /metering/usage/platform
//（平台跨租户聚合），覆盖查询参数组装、脏行跳过、协议错误与不可用映射。

func newMeteringTestClient(srv *httptest.Server) *MeteringClient {
	return &MeteringClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
}

func TestMeteringClient_GetPlatformUsage_Success(t *testing.T) {
	t.Parallel()

	tenantA := "11111111-1111-1111-1111-111111111111"
	tenantB := "22222222-2222-2222-2222-222222222222"

	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/metering/usage/platform" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"tenant_id": tenantA, "resource_type": "instance_gpu_seconds", "total_quantity": 7200},
				{"tenant_id": tenantB, "resource_type": "token_total", "total_quantity": 1000000},
				// 脏行：tenant_id 无法解析 / resource_type 为空 → 跳过，不毁掉整月折算
				{"tenant_id": "not-a-uuid", "resource_type": "instance_cpu_seconds", "total_quantity": 1},
				{"tenant_id": tenantA, "resource_type": "", "total_quantity": 2},
			},
		})
	}))
	defer srv.Close()

	start, end, err := ports.BillingMonthWindow("2026-09")
	if err != nil {
		t.Fatalf("month window: %v", err)
	}
	records, err := newMeteringTestClient(srv).GetPlatformUsage(context.Background(), start, end, nil)
	if err != nil {
		t.Fatalf("GetPlatformUsage: %v", err)
	}

	// 查询参数：月窗口（RFC3339）+ group_by=tenant_id
	if got := gotQuery.Get("group_by"); got != "tenant_id" {
		t.Fatalf("group_by = %q", got)
	}
	if got := gotQuery.Get("start_time"); got != start.Format(time.RFC3339) {
		t.Fatalf("start_time = %q, want %q", got, start.Format(time.RFC3339))
	}
	if got := gotQuery.Get("end_time"); got != end.Format(time.RFC3339) {
		t.Fatalf("end_time = %q, want %q", got, end.Format(time.RFC3339))
	}
	if got := gotQuery.Get("tenant_id"); got != "" {
		t.Fatalf("tenant_id = %q, want empty（无过滤）", got)
	}

	// 只解析出 2 条有效记录
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2（脏行跳过）", len(records))
	}
	if records[0].TenantID.String() != tenantA || records[0].ResourceType != "instance_gpu_seconds" || records[0].TotalQuantity != 7200 {
		t.Fatalf("record[0] = %+v", records[0])
	}
	if records[1].TenantID.String() != tenantB || records[1].ResourceType != "token_total" || records[1].TotalQuantity != 1000000 {
		t.Fatalf("record[1] = %+v", records[1])
	}
}

func TestMeteringClient_GetPlatformUsage_TenantFilter(t *testing.T) {
	t.Parallel()

	tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("tenant_id"); got != tenantA.String() {
			t.Errorf("tenant_id = %q, want %q（单租户钻取）", got, tenantA)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
	}))
	defer srv.Close()

	start, end, _ := ports.BillingMonthWindow("2026-09")
	records, err := newMeteringTestClient(srv).GetPlatformUsage(context.Background(), start, end, &tenantA)
	if err != nil {
		t.Fatalf("GetPlatformUsage: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want 0", len(records))
	}
}

func TestMeteringClient_GetPlatformUsage_MissingItems(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"unexpected": true}) // 缺 items → 协议错误
	}))
	defer srv.Close()

	start, end, _ := ports.BillingMonthWindow("2026-09")
	_, err := newMeteringTestClient(srv).GetPlatformUsage(context.Background(), start, end, nil)
	if err == nil || !errors.Is(err, ports.ErrCoreUnavailable) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}

func TestMeteringClient_GetPlatformUsage_Unavailable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":"INTERNAL"}`))
	}))
	defer srv.Close()

	start, end, _ := ports.BillingMonthWindow("2026-09")
	_, err := newMeteringTestClient(srv).GetPlatformUsage(context.Background(), start, end, nil)
	if err == nil || !errors.Is(err, ports.ErrCoreUnavailable) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}
