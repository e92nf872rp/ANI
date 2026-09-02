package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

func TestQuotaSvcClient_ListQuotaMeta_Fetch(t *testing.T) {
	t.Parallel()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v1/admin/quota-meta" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"resource_type": "gpu_count", "display_name": "GPU", "unit": "card", "default_quota": 4, "is_discrete": true},
			},
		})
	}))
	defer srv.Close()

	client := &QuotaSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}

	items, err := client.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("ListQuotaMeta: %v", err)
	}
	if len(items) != 1 || items[0].ResourceType != "gpu_count" || !items[0].Enabled || items[0].DefaultQuota != 4 ||
		items[0].DisplayName != "GPU" || items[0].Unit != "card" || !items[0].IsDiscrete {
		t.Fatalf("unexpected items: %+v", items)
	}

	_, err = client.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("second ListQuotaMeta: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 remote HTTP calls, got %d", hits)
	}
}

func TestQuotaSvcClient_ListQuotaMeta_Unavailable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":"INTERNAL"}`))
	}))
	defer srv.Close()

	client := &QuotaSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.ListQuotaMeta(context.Background())
	if err == nil || !strings.Contains(err.Error(), ports.ErrCoreUnavailable.Error()) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}

func TestQuotaSvcClient_PutQuota_Tightened(t *testing.T) {
	t.Parallel()

	tenantID := "11111111-1111-1111-1111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method=%s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/tenants/"+tenantID+"/quota" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": tenantID,
			"items": []map[string]any{
				{"resource_type": "gpu_count", "total": 2, "used": 2, "reserved": 0, "tightened": true},
			},
		})
	}))
	defer srv.Close()

	client := &QuotaSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	id := uuid.MustParse(tenantID)
	out, err := client.PutQuota(context.Background(), id, []ports.CoreQuotaItem{
		{ResourceType: "gpu_count", Total: 1},
	})
	if err != nil {
		t.Fatalf("PutQuota: %v", err)
	}
	if len(out) != 1 || !out[0].Tightened || out[0].Total != 2 {
		t.Fatalf("result=%+v", out)
	}
}

func TestQuotaSvcClient_PutQuota_QuotaNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "QUOTA_NOT_FOUND", "message": "missing"})
	}))
	defer srv.Close()

	client := &QuotaSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.PutQuota(context.Background(), uuid.New(), []ports.CoreQuotaItem{
		{ResourceType: "gpu_count", Total: 1},
	})
	if err == nil || !strings.Contains(err.Error(), ports.ErrQuotaNotFound.Error()) {
		t.Fatalf("expected ErrQuotaNotFound, got %v", err)
	}
}

func TestQuotaSvcClient_UpsertQuota_Tightened(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/admin/tenants/"+id.String()+"/quota/upsert" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": id.String(),
			"items": []map[string]any{
				{"resource_type": "gpu_count", "total": 2, "used": 1, "reserved": 1, "tightened": true},
			},
		})
	}))
	defer srv.Close()

	client := &QuotaSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.UpsertQuota(context.Background(), id, []ports.CoreQuotaItem{
		{ResourceType: "gpu_count", Total: 1},
	})
	if err != nil {
		t.Fatalf("UpsertQuota: %v", err)
	}
	if len(out) != 1 || !out[0].Tightened || out[0].Total != 2 {
		t.Fatalf("result=%+v", out)
	}
}

func TestTenantSvcClient_GetTenant(t *testing.T) {
	t.Parallel()

	tenantID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	planID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/tenants/"+tenantID {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": tenantID, "name": "acme", "display_name": "Acme",
			"status": "active", "plan_id": planID,
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z",
		})
	}))
	defer srv.Close()

	client := &TenantSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	got, err := client.GetTenant(context.Background(), uuid.MustParse(tenantID))
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.Name != "acme" || got.Status != ports.TenantStatusActive || got.PlanID.String() != planID {
		t.Fatalf("got=%+v", got)
	}
}

func TestTenantPlanSvcClient_UpdateTenantPlan(t *testing.T) {
	t.Parallel()

	tenantID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	planID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/admin/tenants/"+tenantID+"/plan" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": tenantID, "name": "acme", "display_name": "Acme",
			"status": "active", "plan_id": planID,
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z",
		})
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	got, err := client.UpdateTenantPlan(context.Background(), uuid.MustParse(tenantID), uuid.MustParse(planID))
	if err != nil {
		t.Fatalf("UpdateTenantPlan: %v", err)
	}
	if got.PlanID.String() != planID {
		t.Fatalf("plan_id=%s", got.PlanID)
	}
}

func TestTenantPlanSvcClient_CountBoundTenants(t *testing.T) {
	t.Parallel()

	planA := "11111111-1111-4111-8111-111111111111"
	planB := "22222222-2222-4222-8222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/plans/bound-tenant-counts" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		got := r.URL.Query()["plan_id"]
		if len(got) != 2 || got[0] != planA || got[1] != planB {
			t.Fatalf("plan_id query = %v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"plan_id": planA, "count": 12},
				{"plan_id": planB, "count": 0},
			},
		})
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	got, err := client.CountBoundTenants(context.Background(), []uuid.UUID{
		uuid.MustParse(planA), uuid.MustParse(planB),
	})
	if err != nil {
		t.Fatalf("CountBoundTenants: %v", err)
	}
	if got[uuid.MustParse(planA)] != 12 || got[uuid.MustParse(planB)] != 0 {
		t.Fatalf("got=%v", got)
	}
}

func TestTenantPlanSvcClient_CountBoundTenants_MissingItems(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.CountBoundTenants(context.Background(), []uuid.UUID{uuid.New()})
	if err == nil || !strings.Contains(err.Error(), ports.ErrCoreUnavailable.Error()) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}

func TestTenantPlanSvcClient_ListBoundTenants(t *testing.T) {
	t.Parallel()

	planID := "11111111-1111-4111-8111-111111111111"
	tenantID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/plans/"+planID+"/bound-tenants" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": tenantID, "name": "acme", "display_name": "Acme", "status": "active"},
			},
		})
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	got, err := client.ListBoundTenants(context.Background(), uuid.MustParse(planID))
	if err != nil {
		t.Fatalf("ListBoundTenants: %v", err)
	}
	if len(got) != 1 || got[0].Name != "acme" || got[0].Status != ports.TenantStatusActive {
		t.Fatalf("got=%+v", got)
	}
}

func TestTenantPlanSvcClient_ListBindableTenants(t *testing.T) {
	t.Parallel()

	planID := "22222222-2222-4222-8222-222222222222"
	tenantID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/plans/"+planID+"/bindable-tenants" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": tenantID, "name": "beta", "display_name": "Beta", "status": "frozen"},
			},
		})
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	got, err := client.ListBindableTenants(context.Background(), uuid.MustParse(planID))
	if err != nil {
		t.Fatalf("ListBindableTenants: %v", err)
	}
	if len(got) != 1 || got[0].Name != "beta" || got[0].Status != ports.TenantStatusFrozen {
		t.Fatalf("got=%+v", got)
	}
}

func TestTenantPlanSvcClient_ListBoundTenants_MissingItems(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := &TenantPlanSvcClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.ListBoundTenants(context.Background(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), ports.ErrCoreUnavailable.Error()) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}
