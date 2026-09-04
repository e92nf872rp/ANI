package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRuntimeAdminPreservesServicesBootstrapFailureSemantics(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("ANI_SERVICE_NAME", "")
	check := probeCheck{
		name: "postgres",
		run:  func(context.Context) error { return errors.New("postgres://user:secret@db/internal") },
	}
	legacy := runProbeChecks(context.Background(), []probeCheck{check})
	if legacy.Status != "fail" {
		t.Fatalf("legacy status=%q want fail", legacy.Status)
	}

	runtime, err := newRuntimeAdmin("tenant-service", []probeCheck{check}, nil)
	if err != nil {
		t.Fatalf("new runtime admin: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	runtime.SetServing(true)
	recorder := httptest.NewRecorder()
	runtime.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"error"`) || !strings.Contains(recorder.Body.String(), `"error":"unavailable"`) {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "postgres://") {
		t.Fatalf("response leaked raw error: %s", recorder.Body.String())
	}
}

func TestRuntimeAdminAddsMetricsToServicesBootstrap(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("ANI_SERVICE_NAME", "")
	runtime, err := newRuntimeAdmin("tenant-service", nil, nil)
	if err != nil {
		t.Fatalf("new runtime admin: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	recorder := httptest.NewRecorder()
	runtime.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", recorder.Code)
	}
	for _, want := range []string{`target_info{service_instance_id=`, `service_name="tenant-service"`, `service_namespace="ani"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, recorder.Body.String())
		}
	}
}

func TestDepsCloseNilSafe(t *testing.T) {
	var d *Deps
	d.Close()
	(&Deps{}).Close()
}
