package runtimeadmin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHealthzDoesNotRunChecks(t *testing.T) {
	var calls atomic.Int32
	runtime := newTestRuntime(t, Config{
		Checks: []Check{{
			Name:        "postgres",
			Criticality: Strong,
			Probe: func(context.Context) error {
				calls.Add(1)
				return errors.New("secret database failure")
			},
		}},
	})

	response := serve(runtime, http.MethodGet, "/healthz")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("healthz executed %d checks, want 0", calls.Load())
	}
	assertHeader(t, response, "Content-Type", "application/json; charset=utf-8")
	assertHeader(t, response, "Cache-Control", "no-store")
	assertBodyContains(t, response, `"status":"ok"`)
}

func TestReadyzStrongFailureIsSanitizedAndUnavailable(t *testing.T) {
	runtime := newTestRuntime(t, Config{
		Identity: Identity{Version: "v0.9.0"},
		Checks: []Check{{
			Name:        "postgres",
			Criticality: Strong,
			Probe:       func(context.Context) error { return errors.New("postgres://user:password@db/internal") },
		}},
	})
	runtime.SetServing(true)

	response := serve(runtime, http.MethodGet, "/readyz")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", response.Code, response.Body.String())
	}
	assertBodyContains(t, response, `"status":"error"`)
	assertBodyContains(t, response, `"status":"fail"`)
	assertBodyContains(t, response, `"error":"unavailable"`)
	assertBodyContains(t, response, `"version":"v0.9.0"`)
	if strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "postgres://") {
		t.Fatalf("response leaked raw error: %s", response.Body.String())
	}
}

func TestReadyzLogsOnlySanitizedFailure(t *testing.T) {
	var logs bytes.Buffer
	runtime := newTestRuntime(t, Config{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Checks: []Check{{
			Name:        "postgres",
			Criticality: Strong,
			Probe:       func(context.Context) error { return errors.New("postgres://user:secret@db/internal") },
		}},
	})
	runtime.SetServing(true)

	response := serve(runtime, http.MethodGet, "/readyz")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "postgres://") {
		t.Fatalf("readiness log leaked raw error: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "error=unavailable") {
		t.Fatalf("readiness log missing sanitized category: %s", logs.String())
	}
}

func TestReadyzWeakFailureIsDegradedAndAvailable(t *testing.T) {
	runtime := newTestRuntime(t, Config{
		Checks: []Check{{
			Name:        "object-store",
			Criticality: Weak,
			Probe:       func(context.Context) error { return errors.New("not configured") },
		}},
	})
	runtime.SetServing(true)

	response := serve(runtime, http.MethodGet, "/readyz")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	assertBodyContains(t, response, `"status":"degraded"`)
	assertBodyContains(t, response, `"error":"not_configured"`)
}

func TestReadyzRunsChecksConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	probe := func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	}
	runtime := newTestRuntime(t, Config{
		CheckTimeout:   200 * time.Millisecond,
		OverallTimeout: 300 * time.Millisecond,
		Checks: []Check{
			{Name: "postgres", Criticality: Strong, Probe: probe},
			{Name: "redis", Criticality: Strong, Probe: probe},
		},
	})
	runtime.SetServing(true)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(runtime, http.MethodGet, "/readyz") }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("checks did not start concurrently")
		}
	}
	close(release)
	response := <-done
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
}

func TestReadyzBoundsIndividualCheckAndPropagatesCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	runtime := newTestRuntime(t, Config{
		CheckTimeout:   20 * time.Millisecond,
		OverallTimeout: 100 * time.Millisecond,
		Checks: []Check{{
			Name:        "redis",
			Criticality: Strong,
			Probe: func(ctx context.Context) error {
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			},
		}},
	})
	runtime.SetServing(true)

	response := serve(runtime, http.MethodGet, "/readyz")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	assertBodyContains(t, response, `"error":"timeout"`)
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("probe did not observe context cancellation")
	}
}

func TestReadyzOverallTimeoutBoundsChecksThatIgnoreCancellation(t *testing.T) {
	release := make(chan struct{})
	runtime := newTestRuntime(t, Config{
		CheckTimeout:   time.Second,
		OverallTimeout: 20 * time.Millisecond,
		Checks: []Check{{
			Name:        "postgres",
			Criticality: Strong,
			Probe: func(context.Context) error {
				<-release
				return nil
			},
		}},
	})
	runtime.SetServing(true)
	startedAt := time.Now()
	response := serve(runtime, http.MethodGet, "/readyz")
	close(release)
	if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
		t.Fatalf("readyz elapsed %s, want bounded by overall timeout", elapsed)
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	assertBodyContains(t, response, `"postgres":{"status":"fail"`)
	assertBodyContains(t, response, `"error":"timeout"`)
}

func TestSetServingControlsBuiltInStrongGate(t *testing.T) {
	runtime := newTestRuntime(t, Config{})

	before := serve(runtime, http.MethodGet, "/readyz")
	if before.Code != http.StatusServiceUnavailable {
		t.Fatalf("before serving status = %d, want 503", before.Code)
	}
	assertBodyContains(t, before, `"service-serving":{"status":"fail"`)

	runtime.SetServing(true)
	after := serve(runtime, http.MethodGet, "/readyz")
	if after.Code != http.StatusOK {
		t.Fatalf("after serving status = %d, want 200: %s", after.Code, after.Body.String())
	}

	runtime.SetServing(false)
	if got := runtime.Summary().Status; got != "error" {
		t.Fatalf("summary status = %q, want error", got)
	}
}

func TestHandlerRejectsMethodsAndUnknownPaths(t *testing.T) {
	runtime := newTestRuntime(t, Config{})
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		response := serve(runtime, http.MethodPost, path)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status = %d, want 405", path, response.Code)
		}
		assertHeader(t, response, "Allow", http.MethodGet)
	}
	if response := serve(runtime, http.MethodGet, "/health"); response.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", response.Code)
	}
}

func TestNewRejectsInvalidChecksAndDuplicateCollectors(t *testing.T) {
	validProbe := func(context.Context) error { return nil }
	tests := []struct {
		name   string
		checks []Check
	}{
		{name: "blank name", checks: []Check{{Criticality: Strong, Probe: validProbe}}},
		{name: "blank criticality", checks: []Check{{Name: "postgres", Probe: validProbe}}},
		{name: "unknown criticality", checks: []Check{{Name: "postgres", Criticality: "maybe", Probe: validProbe}}},
		{name: "nil probe", checks: []Check{{Name: "postgres", Criticality: Strong}}},
		{name: "duplicate name", checks: []Check{{Name: "postgres", Criticality: Strong, Probe: validProbe}, {Name: "postgres", Criticality: Weak, Probe: validProbe}}},
		{name: "reserved name", checks: []Check{{Name: "service-serving", Criticality: Strong, Probe: validProbe}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(Config{Identity: testIdentity(), Checks: test.checks})
			if err == nil {
				t.Fatal("New succeeded, want error")
			}
		})
	}

	collector := prometheus.NewCounter(prometheus.CounterOpts{Name: "duplicate_total", Help: "test"})
	_, err := New(Config{Identity: testIdentity(), Collectors: []prometheus.Collector{collector, collector}})
	if err == nil {
		t.Fatal("New accepted duplicate collector")
	}
}

func TestNewResolvesIdentityAndRejectsKubernetesMisconfiguration(t *testing.T) {
	t.Run("local startup id and placeholder version omitted", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		t.Setenv("POD_UID", "")
		t.Setenv("ANI_SERVICE_NAME", "")
		t.Setenv("ANI_SERVICE_VERSION", "(devel)")
		runtime, err := New(Config{Identity: Identity{Namespace: "ani", Name: "auth-service"}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
		if runtime.identity.InstanceID == "" {
			t.Fatal("local instance id is empty")
		}
		if runtime.identity.Version != "" {
			t.Fatalf("version = %q, want omitted", runtime.identity.Version)
		}
	})

	t.Run("kubernetes requires pod uid", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
		t.Setenv("POD_UID", "")
		_, err := New(Config{Identity: testIdentity()})
		if err == nil || !strings.Contains(err.Error(), "POD_UID") {
			t.Fatalf("error = %v, want POD_UID error", err)
		}
	})

	t.Run("service name mismatch", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
		t.Setenv("POD_UID", "pod-uid")
		t.Setenv("ANI_SERVICE_NAME", "model-service")
		_, err := New(Config{Identity: testIdentity()})
		if err == nil || !strings.Contains(err.Error(), "ANI_SERVICE_NAME") {
			t.Fatalf("error = %v, want ANI_SERVICE_NAME error", err)
		}
	})
}

func TestMetricsExposeTargetInfoAndCollector(t *testing.T) {
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "ani_existing_total", Help: "Existing metric."})
	counter.Add(3)
	runtime := newTestRuntime(t, Config{
		Identity:   Identity{Version: "v0.9.0", InstanceID: "instance-1"},
		Collectors: []prometheus.Collector{counter},
	})

	response := serve(runtime, http.MethodGet, "/metrics")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	assertBodyContains(t, response, "ani_existing_total 3")
	assertBodyContains(t, response, `target_info{service_instance_id="instance-1",service_name="auth-service",service_namespace="ani",service_version="v0.9.0"} 1`)
}

func TestMetricsCollectionFailureReturns500(t *testing.T) {
	runtime := newTestRuntime(t, Config{Collectors: []prometheus.Collector{failingCollector{}}})
	response := serve(runtime, http.MethodGet, "/metrics")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
	}
}

type failingCollector struct{}

func (failingCollector) Describe(chan<- *prometheus.Desc) {}

func (failingCollector) Collect(metrics chan<- prometheus.Metric) {
	metrics <- prometheus.NewInvalidMetric(prometheus.NewDesc("ani_broken", "Broken metric.", nil, nil), errors.New("collection failed"))
}

func newTestRuntime(t *testing.T, config Config) *Runtime {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("POD_UID", "")
	t.Setenv("ANI_SERVICE_NAME", "")
	t.Setenv("ANI_SERVICE_VERSION", "")
	identity := testIdentity()
	if config.Identity.Version != "" {
		identity.Version = config.Identity.Version
	}
	if config.Identity.InstanceID != "" {
		identity.InstanceID = config.Identity.InstanceID
	}
	config.Identity = identity
	runtime, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return runtime
}

func testIdentity() Identity {
	return Identity{Namespace: "ani", Name: "auth-service"}
}

func serve(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func assertHeader(t *testing.T, response *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	if got := response.Header().Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertBodyContains(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	if !strings.Contains(response.Body.String(), want) {
		t.Fatalf("body missing %q:\n%s", want, response.Body.String())
	}
}
