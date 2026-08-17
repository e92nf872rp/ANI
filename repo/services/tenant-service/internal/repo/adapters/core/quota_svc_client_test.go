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
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("missing Idempotency-Key")
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

func TestTenantSvcClient_UpdateTenantPlan(t *testing.T) {
	t.Parallel()

	tenantID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	planID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/admin/tenants/"+tenantID+"/plan" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("missing Idempotency-Key")
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
	got, err := client.UpdateTenantPlan(context.Background(), uuid.MustParse(tenantID), uuid.MustParse(planID))
	if err != nil {
		t.Fatalf("UpdateTenantPlan: %v", err)
	}
	if got.PlanID.String() != planID {
		t.Fatalf("plan_id=%s", got.PlanID)
	}
}
