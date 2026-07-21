package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestPrometheusInstanceObservabilityListsLogsEventsAndSecurityEvents(t *testing.T) {
	var requests []string
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.String())
		switch r.URL.Path {
		case "/api/v1/namespaces/ani-tenant-tenant-a/pods/pod-a/log":
			return jsonResponse(http.StatusOK, "info booted\nwarn restarted\n"), nil
		case "/api/v1/namespaces/ani-tenant-tenant-a/events":
			return jsonResponse(http.StatusOK, `{
				"items": [
					{"metadata":{"uid":"evt-a"},"type":"Normal","reason":"Scheduled","message":"pod scheduled","count":2,"lastTimestamp":"2026-06-19T08:29:00Z"},
					{"metadata":{"uid":"evt-b"},"type":"Warning","reason":"Unhealthy","message":"readiness probe failed","count":1,"eventTime":"2026-06-19T08:30:00Z"}
				]
			}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	logs, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Limit:      1,
		Level:      "warn",
	})
	if err != nil {
		t.Fatalf("ListLogs() error = %v", err)
	}
	if len(logs.Items) != 1 || logs.Items[0].Level != "warn" || logs.Items[0].Message != "warn restarted" {
		t.Fatalf("logs = %+v, want one warning log from Kubernetes pod logs", logs)
	}
	if logs.DevProfile.Mode != "dev_profile" || logs.DevProfile.RealProvider {
		t.Fatalf("logs dev profile = %+v, want non-real dev_profile marker", logs.DevProfile)
	}

	events, err := service.ListEvents(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Type:       "Warning",
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events.Items) != 1 || events.Items[0].ID != "evt-b" || events.Items[0].Reason != "Unhealthy" {
		t.Fatalf("events = %+v, want filtered Kubernetes warning event", events)
	}

	security, err := service.ListSecurityEvents(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
		Severity:   "warning",
	})
	if err != nil {
		t.Fatalf("ListSecurityEvents() error = %v", err)
	}
	if len(security.Items) != 1 || security.Items[0].EventType != "kubernetes_warning" {
		t.Fatalf("security events = %+v, want warning event projection", security)
	}
	if len(requests) != 3 || !strings.Contains(requests[0], "tailLines=1") || !strings.Contains(requests[1], "involvedObject.name%3Dpod-a") {
		t.Fatalf("requests = %+v, want Kubernetes logs/events API calls", requests)
	}
}

func TestPrometheusInstanceObservabilityGetsMetricsFromPrometheus(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("path = %s, want Prometheus query API", r.URL.Path)
		}
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		if !strings.Contains(query, `pod="pod-a"`) || !strings.Contains(query, "container_cpu_usage_seconds_total") {
			t.Fatalf("query = %q, want pod-scoped CPU query", query)
		}
		return jsonResponse(http.StatusOK, `{
			"status":"success",
			"data":{"resultType":"vector","result":[{"value":[1780000000,"23.5"]}]}
		}`), nil
	})

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "pod-a",
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.InstanceID != "pod-a" || metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 23.5 {
		t.Fatalf("metrics = %+v, want Prometheus CPU utilization", metrics)
	}
	if !metrics.Timestamp.Equal(time.Unix(1780000000, 0).UTC()) {
		t.Fatalf("timestamp = %s, want Prometheus sample timestamp", metrics.Timestamp)
	}
	if metrics.DevProfile.Provider != "prometheus-kubernetes-instance-observability" || metrics.DevProfile.RealProvider {
		t.Fatalf("metrics dev profile = %+v, want Prometheus/Kubernetes contract marker", metrics.DevProfile)
	}
}

func TestPrometheusInstanceObservabilityCreatesIdempotentShortLivedExecSession(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	service := newTestPrometheusInstanceObservabilityWithClock(t, nil, func() time.Time { return now })
	request := ports.InstanceExecSessionCreateRequest{
		TenantID:       "tenant-a",
		InstanceID:     "pod-a",
		IdempotencyKey: "exec-once",
		Command:        []string{"/bin/sh"},
		TTY:            true,
		Rows:           24,
		Cols:           80,
	}

	first, err := service.CreateExecSession(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateExecSession() first error = %v", err)
	}
	second, err := service.CreateExecSession(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateExecSession() replay error = %v", err)
	}
	if first.ID == "" || second.ID != first.ID || second.WSURL != first.WSURL {
		t.Fatalf("replay = %+v, want same session as %+v", second, first)
	}
	if first.Token != "" {
		t.Fatalf("token = %q, want no long-lived credential", first.Token)
	}
	if !strings.HasPrefix(first.WSURL, "wss://gateway.example.test/api/v1/instances/pod-a/exec/") {
		t.Fatalf("ws_url = %q, want gateway exec URL", first.WSURL)
	}
	if !first.ExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires_at = %s, want 15 minute TTL", first.ExpiresAt)
	}
}

func newTestPrometheusInstanceObservability(t *testing.T, roundTrip roundTripFunc) *PrometheusInstanceObservability {
	t.Helper()
	return newTestPrometheusInstanceObservabilityWithClock(t, roundTrip, func() time.Time {
		return time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	})
}

func newTestPrometheusInstanceObservabilityWithClock(t *testing.T, roundTrip roundTripFunc, now func() time.Time) *PrometheusInstanceObservability {
	t.Helper()
	transport := http.RoundTripper(http.DefaultTransport)
	if roundTrip != nil {
		transport = roundTrip
	}
	service, err := NewPrometheusInstanceObservability(PrometheusInstanceObservabilityConfig{
		PrometheusURL:         "https://prometheus.example.test",
		KubernetesAPIHost:     "https://kubernetes.example.test",
		KubernetesBearerToken: "token",
		ExecBaseURL:           "wss://gateway.example.test/api/v1",
		HTTPClient:            &http.Client{Transport: transport},
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("NewPrometheusInstanceObservability() error = %v", err)
	}
	return service
}
