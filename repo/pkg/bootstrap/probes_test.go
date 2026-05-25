package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestProbeHandlerHealthz(t *testing.T) {
	handler := newProbeHandler("test-service", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body probeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Version == "" {
		t.Fatalf("body = %+v, want ok with version", body)
	}
}

func TestRunProbeChecksDegradesOnDependencyFailure(t *testing.T) {
	result := runProbeChecks(context.Background(), []probeCheck{
		{name: "postgres", run: func(context.Context) error { return nil }},
		{name: "redis", run: func(context.Context) error { return errors.New("dial failed") }},
	})

	if result.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", result.Status)
	}
	if result.Checks["postgres"].Status != "ok" {
		t.Fatalf("postgres status = %q, want ok", result.Checks["postgres"].Status)
	}
	if result.Checks["redis"].Status != "fail" || result.Checks["redis"].Error == "" {
		t.Fatalf("redis check = %+v, want fail with error", result.Checks["redis"])
	}
}

func TestProbeHandlerMetricsExportsReconcileControllerCounters(t *testing.T) {
	handler := newProbeHandler("instance-service", nil, fakeReconcileMetricsReader{
		metrics: ports.ReconcileControllerMetrics{
			Ticks:          7,
			Successes:      5,
			Failures:       2,
			SkippedBackoff: 3,
		},
	})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain", contentType)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		`ani_workload_reconcile_ticks_total{service="instance-service"} 7`,
		`ani_workload_reconcile_successes_total{service="instance-service"} 5`,
		`ani_workload_reconcile_failures_total{service="instance-service"} 2`,
		`ani_workload_reconcile_backoff_skips_total{service="instance-service"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

type fakeReconcileMetricsReader struct {
	metrics ports.ReconcileControllerMetrics
}

func (r fakeReconcileMetricsReader) Metrics() ports.ReconcileControllerMetrics {
	return r.metrics
}
