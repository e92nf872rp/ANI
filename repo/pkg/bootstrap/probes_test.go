package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adapterresilience "github.com/kubercloud/ani/pkg/adapters/resilience"
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

func TestRunProbeChecksFailsOnStrongDependencyFailure(t *testing.T) {
	result := runProbeChecks(context.Background(), []probeCheck{
		{name: "postgres", run: func(context.Context) error { return nil }},
		{name: "redis", run: func(context.Context) error { return errors.New("dial failed") }},
	})

	if result.Status != "fail" {
		t.Fatalf("status = %q, want fail", result.Status)
	}
	if result.Checks["postgres"].Status != "ok" {
		t.Fatalf("postgres status = %q, want ok", result.Checks["postgres"].Status)
	}
	if result.Checks["redis"].Status != "fail" || result.Checks["redis"].Error == "" {
		t.Fatalf("redis check = %+v, want fail with error", result.Checks["redis"])
	}
}

func TestDependencyProbeChecksReportsObjectStoreUnavailable(t *testing.T) {
	checks := dependencyProbeChecks(&Deps{
		Ports: Capabilities{
			ObjectStore: fakeObjectStoreHealth{err: errors.New("minio unavailable")},
		},
	})

	check, ok := findProbeCheck(checks, "object-store")
	if !ok {
		t.Fatal("object-store probe check missing")
	}
	if err := check.run(context.Background()); err == nil || !strings.Contains(err.Error(), "minio unavailable") {
		t.Fatalf("object-store check error = %v, want minio unavailable", err)
	}
}

func TestWeakDependencyDownYieldsDegradedNotFail(t *testing.T) {
	handler := newProbeHandler("test-service", []probeCheck{
		{name: "object-store", run: func(context.Context) error { return errors.New("minio unavailable") }},
	})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for weak dependency degradation", recorder.Code)
	}
	var body probeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("body status = %q, want degraded", body.Status)
	}
	if body.Checks["object-store"].Status != "degraded" {
		t.Fatalf("object-store status = %q, want degraded", body.Checks["object-store"].Status)
	}
}

func TestDependencyProbeChecksReportsVectorStoreUnavailable(t *testing.T) {
	checks := dependencyProbeChecks(&Deps{
		Ports: Capabilities{
			VectorStore: fakeVectorStoreHealth{err: errors.New("milvus unavailable")},
		},
	})

	check, ok := findProbeCheck(checks, "vector-store")
	if !ok {
		t.Fatal("vector-store probe check missing")
	}
	if err := check.run(context.Background()); err == nil || !strings.Contains(err.Error(), "milvus unavailable") {
		t.Fatalf("vector-store check error = %v, want milvus unavailable", err)
	}
}

func TestDependencyProbeChecksReportsKubernetesAPIUnavailable(t *testing.T) {
	checks := dependencyProbeChecks(&Deps{
		Ports: Capabilities{
			KubernetesAPI: fakeHealthChecker{err: errors.New("kubernetes api unavailable")},
		},
	})

	check, ok := findProbeCheck(checks, "kubernetes-api")
	if !ok {
		t.Fatal("kubernetes-api probe check missing")
	}
	if err := check.run(context.Background()); err == nil || !strings.Contains(err.Error(), "kubernetes api unavailable") {
		t.Fatalf("kubernetes-api check error = %v, want kubernetes api unavailable", err)
	}
}

func TestDependencyProbeChecksIgnoreTypedNilKubernetesAPI(t *testing.T) {
	var checker *panicHealthChecker
	checks := dependencyProbeChecks(&Deps{
		Ports: Capabilities{
			KubernetesAPI: checker,
		},
	})

	check, ok := findProbeCheck(checks, "kubernetes-api")
	if !ok {
		t.Fatal("kubernetes-api probe check missing")
	}
	if err := check.run(context.Background()); err != nil {
		t.Fatalf("kubernetes-api check error = %v, want nil for typed nil dependency", err)
	}
}

func TestDependencyProbeChecksIgnoreNotConfiguredDataPlane(t *testing.T) {
	checks := dependencyProbeChecks(&Deps{
		Ports: Capabilities{
			ObjectStore:   fakeObjectStoreHealth{err: ports.ErrNotConfigured},
			VectorStore:   fakeVectorStoreHealth{err: ports.ErrNotConfigured},
			KubernetesAPI: fakeHealthChecker{err: ports.ErrNotConfigured},
		},
	})

	for _, name := range []string{"object-store", "vector-store", "kubernetes-api"} {
		check, ok := findProbeCheck(checks, name)
		if !ok {
			t.Fatalf("%s probe check missing", name)
		}
		if err := check.run(context.Background()); err != nil {
			t.Fatalf("%s check error = %v, want nil for not configured", name, err)
		}
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

func TestRuntimeAdminPreservesLegacyReadinessAggregation(t *testing.T) {
	tests := []struct {
		name       string
		check      probeCheck
		wantLegacy string
		wantNew    string
		wantCode   int
	}{
		{
			name:       "strong failure",
			check:      probeCheck{name: "postgres", mode: adapterresilience.DependencyStrong, run: func(context.Context) error { return errors.New("database unavailable") }},
			wantLegacy: "fail",
			wantNew:    "error",
			wantCode:   http.StatusServiceUnavailable,
		},
		{
			name:       "weak failure",
			check:      probeCheck{name: "object-store", mode: adapterresilience.DependencyWeak, run: func(context.Context) error { return errors.New("object store unavailable") }},
			wantLegacy: "degraded",
			wantNew:    "degraded",
			wantCode:   http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := runProbeChecks(context.Background(), []probeCheck{test.check})
			if legacy.Status != test.wantLegacy {
				t.Fatalf("legacy status = %q, want %q", legacy.Status, test.wantLegacy)
			}

			runtime, err := newRuntimeAdmin("test-service", []probeCheck{test.check}, nil, nil)
			if err != nil {
				t.Fatalf("new runtime admin: %v", err)
			}
			t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
			runtime.SetServing(true)
			recorder := httptest.NewRecorder()
			runtime.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != test.wantCode {
				t.Fatalf("new status code = %d, want %d: %s", recorder.Code, test.wantCode, recorder.Body.String())
			}
			var body probeResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode new response: %v", err)
			}
			if body.Status != test.wantNew {
				t.Fatalf("new status = %q, want %q", body.Status, test.wantNew)
			}
			if strings.Contains(recorder.Body.String(), "object store unavailable") || strings.Contains(recorder.Body.String(), "database unavailable") {
				t.Fatalf("new response leaked raw error: %s", recorder.Body.String())
			}
		})
	}
}

func TestRuntimeAdminRequiresExplicitCheckCriticality(t *testing.T) {
	runtime, err := newRuntimeAdmin(
		"test-service",
		[]probeCheck{{
			name: "object-store",
			run:  func(context.Context) error { return nil },
		}},
		nil,
		nil,
	)
	if runtime != nil {
		_ = runtime.Shutdown(context.Background())
		t.Fatal("runtime was created without explicit check criticality")
	}
	if err == nil || !strings.Contains(err.Error(), "criticality") {
		t.Fatalf("error = %v, want explicit criticality error", err)
	}
}

func TestRuntimeAdminPreservesReconcileMetrics(t *testing.T) {
	runtime, err := newRuntimeAdmin("instance-service", nil, fakeReconcileMetricsReader{
		metrics: ports.ReconcileControllerMetrics{
			Ticks:          7,
			Successes:      5,
			Failures:       2,
			SkippedBackoff: 3,
		},
	}, nil)
	if err != nil {
		t.Fatalf("new runtime admin: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	recorder := httptest.NewRecorder()
	runtime.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{
		`ani_workload_reconcile_ticks_total{service="instance-service"} 7`,
		`ani_workload_reconcile_successes_total{service="instance-service"} 5`,
		`ani_workload_reconcile_failures_total{service="instance-service"} 2`,
		`ani_workload_reconcile_backoff_skips_total{service="instance-service"} 3`,
		`target_info{service_instance_id=`,
		`service_name="instance-service"`,
		`service_namespace="ani"`,
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("metrics body missing %q:\n%s", want, recorder.Body.String())
		}
	}
}

type fakeReconcileMetricsReader struct {
	metrics ports.ReconcileControllerMetrics
}

func (r fakeReconcileMetricsReader) Metrics() ports.ReconcileControllerMetrics {
	return r.metrics
}

func findProbeCheck(checks []probeCheck, name string) (probeCheck, bool) {
	for _, check := range checks {
		if check.name == name {
			return check, true
		}
	}
	return probeCheck{}, false
}

type fakeObjectStoreHealth struct {
	err error
}

func (s fakeObjectStoreHealth) Health(context.Context) error {
	return s.err
}

func (fakeObjectStoreHealth) EnsureBucket(context.Context, ports.BucketClass) error {
	return nil
}

func (fakeObjectStoreHealth) BucketUsage(context.Context, ports.BucketClass, string) (ports.BucketUsage, error) {
	return ports.BucketUsage{}, nil
}

func (fakeObjectStoreHealth) PutObject(context.Context, ports.PutObjectInput) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, nil
}

func (fakeObjectStoreHealth) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return io.NopCloser(strings.NewReader("")), ports.ObjectMetadata{}, nil
}

func (fakeObjectStoreHealth) DeleteObject(context.Context, ports.ObjectRef) error {
	return nil
}

func (fakeObjectStoreHealth) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, nil
}

func (fakeObjectStoreHealth) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, nil
}

func (fakeObjectStoreHealth) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, nil
}

type fakeVectorStoreHealth struct {
	err error
}

func (s fakeVectorStoreHealth) Health(context.Context) error {
	return s.err
}

func (fakeVectorStoreHealth) CollectionHealth(context.Context, ports.VectorCollectionRef) (ports.VectorCollectionHealth, error) {
	return ports.VectorCollectionHealth{Ready: true}, nil
}

func (fakeVectorStoreHealth) EnsureCollection(context.Context, ports.VectorCollectionRef, int) error {
	return nil
}

func (fakeVectorStoreHealth) Upsert(context.Context, ports.VectorCollectionRef, []ports.VectorRecord) error {
	return nil
}

func (fakeVectorStoreHealth) Search(context.Context, ports.VectorSearchQuery) ([]ports.VectorSearchResult, error) {
	return nil, nil
}

func (fakeVectorStoreHealth) Delete(context.Context, ports.VectorCollectionRef, []string) error {
	return nil
}

func (fakeVectorStoreHealth) DeleteByExpr(context.Context, ports.VectorCollectionRef, string) (int, error) {
	return 0, nil
}

type fakeHealthChecker struct {
	err error
}

func (s fakeHealthChecker) Health(context.Context) error {
	return s.err
}

type panicHealthChecker struct{}

func (*panicHealthChecker) Health(context.Context) error {
	panic("typed nil health checker must not be called")
}
