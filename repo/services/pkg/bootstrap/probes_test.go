package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHandler_Healthz(t *testing.T) {
	h := newProbeHandler("tenant-service", nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body probeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status=%q", body.Status)
	}
}

func TestRunProbeChecks_PostgresMissing(t *testing.T) {
	result := runProbeChecks(context.Background(), dependencyProbeChecks(nil))
	if result.Status != "fail" {
		t.Fatalf("status=%q want fail", result.Status)
	}
	if result.Checks["postgres"].Status != "fail" {
		t.Fatalf("postgres check=%+v", result.Checks["postgres"])
	}
}

func TestDepsCloseNilSafe(t *testing.T) {
	var d *Deps
	d.Close()
	(&Deps{}).Close()
}
