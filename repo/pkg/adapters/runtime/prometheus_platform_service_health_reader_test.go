package runtime

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestPrometheusPlatformServiceHealthReaderAggregatesFreshTargets(t *testing.T) {
	evaluationTime := time.Unix(1_800_000_000, 250_000_000).UTC()
	transport := &prometheusFixtureTransport{responses: map[string]string{
		`up{job="ani-components"}`: prometheusVector(
			prometheusSample(healthLabels("auth-service", "auth-1", "v2"), evaluationTime, "1"),
			prometheusSample(healthLabels("auth-service", "auth-2", "v1"), evaluationTime, "1"),
			prometheusSample(healthLabels("auth-service", "auth-stale", "v3"), evaluationTime, "1"),
			prometheusSample(healthLabels("model-service", "model-stale", "v1"), evaluationTime, "1"),
			prometheusSample(healthLabels("task-service", "task-1", "v1"), evaluationTime, "0"),
			prometheusSample(healthLabels("tenant-service", "tenant-1", "v1"), evaluationTime, "1"),
		),
		`timestamp(up{job="ani-components"})`: prometheusVector(
			prometheusSample(healthLabels("auth-service", "auth-1", "v2"), evaluationTime, seconds(evaluationTime.Add(-5*time.Second))),
			prometheusSample(healthLabels("auth-service", "auth-2", "v1"), evaluationTime, seconds(evaluationTime.Add(-7*time.Second))),
			prometheusSample(healthLabels("auth-service", "auth-stale", "v3"), evaluationTime, seconds(evaluationTime.Add(-46*time.Second))),
			prometheusSample(healthLabels("model-service", "model-stale", "v1"), evaluationTime, seconds(evaluationTime.Add(-46*time.Second))),
			prometheusSample(healthLabels("task-service", "task-1", "v1"), evaluationTime, seconds(evaluationTime.Add(-10*time.Second))),
			prometheusSample(healthLabels("tenant-service", "tenant-1", "v1"), evaluationTime, seconds(evaluationTime.Add(-8*time.Second))),
		),
		`target_info{job="ani-components"}`: prometheusVector(
			prometheusSample(targetInfoLabels("auth-service", "auth-1", "v2"), evaluationTime, "1"),
			prometheusSample(targetInfoLabels("auth-service", "auth-2", "v1"), evaluationTime, "1"),
			prometheusSample(targetInfoLabels("tenant-service", "tenant-1", "v2"), evaluationTime, "1"),
		),
		`timestamp(target_info{job="ani-components"})`: prometheusVector(
			prometheusSample(targetInfoLabels("auth-service", "auth-1", "v2"), evaluationTime, seconds(evaluationTime.Add(-4*time.Second))),
			prometheusSample(targetInfoLabels("auth-service", "auth-2", "v1"), evaluationTime, seconds(evaluationTime.Add(-6*time.Second))),
			prometheusSample(targetInfoLabels("tenant-service", "tenant-1", "v2"), evaluationTime, seconds(evaluationTime.Add(-7*time.Second))),
		),
	}}
	reader, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:    "http://prometheus.monitoring.svc:9090",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return evaluationTime },
	})
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}

	result, err := reader.ReadPlatformServiceHealth(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.Scope != ports.PlatformServiceHealthScope || result.Coverage != ports.PlatformServiceHealthCoverage || result.Signal != ports.PlatformServiceHealthSignal {
		t.Fatalf("fixed envelope = %+v", result)
	}
	if !result.ObservedAt.Equal(evaluationTime) || result.SourceStatus != "ok" {
		t.Fatalf("observation metadata = %+v", result)
	}
	if len(result.Components) != 7 {
		t.Fatalf("components = %d, want 7", len(result.Components))
	}
	auth := componentByName(t, result.Components, "auth-service")
	if auth.ScrapeStatus != ports.PlatformServiceScrapeReachable || auth.ObservedReplicas != 2 || auth.ReachableReplicas != 2 {
		t.Fatalf("auth = %+v", auth)
	}
	if strings.Join(auth.Versions, ",") != "v1,v2" {
		t.Fatalf("auth versions = %v, want sorted v1,v2", auth.Versions)
	}
	if auth.SampleAgeSeconds == nil || math.Abs(*auth.SampleAgeSeconds-7) > 0.001 {
		t.Fatalf("auth sample age = %v, want 7", auth.SampleAgeSeconds)
	}
	model := componentByName(t, result.Components, "model-service")
	if model.ScrapeStatus != ports.PlatformServiceScrapeUnknown || model.ObservedReplicas != 0 || model.SampleAgeSeconds != nil {
		t.Fatalf("stale model = %+v, want unknown", model)
	}
	task := componentByName(t, result.Components, "task-service")
	if task.ScrapeStatus != ports.PlatformServiceScrapeUnreachable || task.ObservedReplicas != 1 || task.ReachableReplicas != 0 {
		t.Fatalf("task = %+v, want unreachable", task)
	}
	tenant := componentByName(t, result.Components, "tenant-service")
	if tenant.ScrapeStatus != ports.PlatformServiceScrapeReachable || tenant.ReachableReplicas != 1 || len(tenant.Versions) != 0 {
		t.Fatalf("version-conflicted tenant = %+v, want reachable with hidden version", tenant)
	}

	requests := transport.snapshotRequests()
	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(requests))
	}
	times := map[string]struct{}{}
	queries := make([]string, 0, len(requests))
	for _, request := range requests {
		times[request.Query().Get("time")] = struct{}{}
		queries = append(queries, request.Query().Get("query"))
	}
	if len(times) != 1 {
		t.Fatalf("query evaluation times = %v, want exactly one T", times)
	}
	sort.Strings(queries)
	wantQueries := []string{
		`target_info{job="ani-components"}`,
		`timestamp(target_info{job="ani-components"})`,
		`timestamp(up{job="ani-components"})`,
		`up{job="ani-components"}`,
	}
	sort.Strings(wantQueries)
	if strings.Join(queries, "\n") != strings.Join(wantQueries, "\n") {
		t.Fatalf("queries = %v, want %v", queries, wantQueries)
	}
}

func TestPrometheusPlatformServiceHealthReaderRequiresTargetInfoForReachableTarget(t *testing.T) {
	evaluationTime := time.Unix(1_800_000_000, 0).UTC()
	transport := &prometheusFixtureTransport{responses: map[string]string{
		`up{job="ani-components"}`: prometheusVector(
			prometheusSample(healthLabels("auth-service", "auth-1", "v1"), evaluationTime, "1"),
		),
		`timestamp(up{job="ani-components"})`: prometheusVector(
			prometheusSample(healthLabels("auth-service", "auth-1", "v1"), evaluationTime, seconds(evaluationTime.Add(-5*time.Second))),
		),
		`target_info{job="ani-components"}`:            prometheusVector(),
		`timestamp(target_info{job="ani-components"})`: prometheusVector(),
	}}
	reader, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:    "http://prometheus.monitoring.svc:9090",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return evaluationTime },
	})
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	if _, err := reader.ReadPlatformServiceHealth(context.Background()); err == nil {
		t.Fatal("read succeeded without target_info, want contract error")
	}
}

func TestPrometheusPlatformServiceHealthReaderFailsClosed(t *testing.T) {
	reader, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL: "http://prometheus.monitoring.svc:9090",
		HTTPClient: &http.Client{Transport: healthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream detail")), Header: make(http.Header)}, nil
		})},
	})
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	if _, err := reader.ReadPlatformServiceHealth(context.Background()); err == nil {
		t.Fatal("read succeeded on non-2xx Prometheus response")
	}
}

func TestPrometheusPlatformServiceHealthReaderRejectsMismatchedEvaluationTime(t *testing.T) {
	evaluationTime := time.Unix(1_800_000_000, 250_000_000).UTC()
	labels := healthLabels("task-service", "task-1", "v1")
	transport := &prometheusFixtureTransport{responses: map[string]string{
		`up{job="ani-components"}`: prometheusVector(
			prometheusSample(labels, evaluationTime.Add(time.Second), "0"),
		),
		`timestamp(up{job="ani-components"})`: prometheusVector(
			prometheusSample(labels, evaluationTime, seconds(evaluationTime.Add(-5*time.Second))),
		),
		`target_info{job="ani-components"}`:            prometheusVector(),
		`timestamp(target_info{job="ani-components"})`: prometheusVector(),
	}}
	reader, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:    "http://prometheus.monitoring.svc:9090",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return evaluationTime },
	})
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	if _, err := reader.ReadPlatformServiceHealth(context.Background()); err == nil {
		t.Fatal("read accepted a Prometheus response evaluated at a different time")
	}
}

func TestPrometheusPlatformServiceHealthReaderRejectsNonFiniteTimestamp(t *testing.T) {
	evaluationTime := time.Unix(1_800_000_000, 0).UTC()
	labels := healthLabels("task-service", "task-1", "v1")
	transport := &prometheusFixtureTransport{responses: map[string]string{
		`up{job="ani-components"}`: prometheusVector(
			prometheusSample(labels, evaluationTime, "0"),
		),
		`timestamp(up{job="ani-components"})`: prometheusVector(
			prometheusSample(labels, evaluationTime, "NaN"),
		),
		`target_info{job="ani-components"}`:            prometheusVector(),
		`timestamp(target_info{job="ani-components"})`: prometheusVector(),
	}}
	reader, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:    "http://prometheus.monitoring.svc:9090",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return evaluationTime },
	})
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	if _, err := reader.ReadPlatformServiceHealth(context.Background()); err == nil {
		t.Fatal("read accepted a non-finite Prometheus timestamp")
	}
}

func TestNewPrometheusPlatformServiceHealthReaderValidatesConfig(t *testing.T) {
	for _, baseURL := range []string{"", "ftp://prometheus.internal", "http://user:pass@prometheus.internal", "http://prometheus.internal/path?query=up"} {
		t.Run(baseURL, func(t *testing.T) {
			_, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{BaseURL: baseURL})
			if err == nil {
				t.Fatal("constructor succeeded, want error")
			}
		})
	}
	_, err := NewPrometheusPlatformServiceHealthReader(PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:      "https://prometheus.internal",
		QueryTimeout: 6 * time.Second,
	})
	if err == nil {
		t.Fatal("constructor accepted timeout above 5s")
	}
}

type prometheusFixtureTransport struct {
	mu        sync.Mutex
	responses map[string]string
	requests  []*url.URL
}

func (transport *prometheusFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	copyURL := *request.URL
	transport.requests = append(transport.requests, &copyURL)
	transport.mu.Unlock()
	body, exists := transport.responses[request.URL.Query().Get("query")]
	if !exists {
		return nil, errors.New("unexpected query")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (transport *prometheusFixtureTransport) snapshotRequests() []*url.URL {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]*url.URL(nil), transport.requests...)
}

type healthRoundTripFunc func(*http.Request) (*http.Response, error)

func (function healthRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func healthLabels(serviceName, pod, version string) map[string]string {
	return map[string]string{
		"job":                  "ani-components",
		"instance":             pod + ":9200",
		"kubernetes_namespace": "ani-system",
		"pod":                  pod,
		"ani_service_name":     serviceName,
		"k8s_service_version":  version,
	}
}

func targetInfoLabels(serviceName, pod, version string) map[string]string {
	result := healthLabels(serviceName, pod, version)
	result["service_namespace"] = "ani"
	result["service_name"] = serviceName
	result["service_instance_id"] = pod + "-uid"
	result["service_version"] = version
	return result
}

func prometheusSample(metric map[string]string, evaluationTime time.Time, value string) string {
	parts := make([]string, 0, len(metric))
	for key := range metric {
		parts = append(parts, key)
	}
	sort.Strings(parts)
	labelsJSON := make([]string, 0, len(parts))
	for _, key := range parts {
		labelsJSON = append(labelsJSON, `"`+key+`":"`+metric[key]+`"`)
	}
	return `{"metric":{` + strings.Join(labelsJSON, ",") + `},"value":[` + seconds(evaluationTime) + `,"` + value + `"]}`
}

func prometheusVector(samples ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(samples, ",") + `]}}`
}

func seconds(value time.Time) string {
	return strconv.FormatFloat(float64(value.UnixNano())/float64(time.Second), 'f', -1, 64)
}

func componentByName(t *testing.T, components []ports.PlatformServiceHealthComponent, name string) ports.PlatformServiceHealthComponent {
	t.Helper()
	for _, component := range components {
		if component.ServiceName == name {
			return component
		}
	}
	t.Fatalf("component %q missing", name)
	return ports.PlatformServiceHealthComponent{}
}
