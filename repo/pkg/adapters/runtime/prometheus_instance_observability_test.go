package runtime

import (
	"context"
	"crypto/tls"
	"io"
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
		switch {
		case r.URL.Path == "/api/v1/namespaces/ani-tenant-tenant-a/pods":
			query, _ := url.QueryUnescape(r.URL.RawQuery)
			if !strings.Contains(query, "ani.kubercloud.io/instance=workload-a") {
				t.Fatalf("pod list query = %q, want workload label selector", query)
			}
			return jsonResponse(http.StatusOK, `{
				"items": [
					{"metadata":{"name":"pod-a"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}
				]
			}`), nil
		case r.URL.Path == "/api/v1/namespaces/ani-tenant-tenant-a/pods/pod-a/log":
			return jsonResponse(http.StatusOK, "info booted\nwarn restarted\n"), nil
		case r.URL.Path == "/api/v1/namespaces/ani-tenant-tenant-a/events":
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
		InstanceID: "workload-a",
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
		InstanceID: "workload-a",
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
		InstanceID: "workload-a",
		Severity:   "warning",
	})
	if err != nil {
		t.Fatalf("ListSecurityEvents() error = %v", err)
	}
	if len(security.Items) != 1 || security.Items[0].EventType != "kubernetes_warning" {
		t.Fatalf("security events = %+v, want warning event projection", security)
	}
	if len(requests) != 6 || !strings.Contains(requests[1], "tailLines=1") || !strings.Contains(requests[3], "involvedObject.name%3Dpod-a") {
		t.Fatalf("requests = %+v, want Kubernetes pod selector plus logs/events API calls", requests)
	}
}

func TestPrometheusInstanceObservabilityGetsMetricsFromPrometheus(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/namespaces/ani-tenant-tenant-a/pods" {
			return jsonResponse(http.StatusOK, `{
				"items": [
					{"metadata":{"name":"pod-a-old"},"status":{"phase":"Pending"}},
					{"metadata":{"name":"pod-a"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}
				]
			}`), nil
		}
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
		InstanceID: "workload-a",
	})
	if err != nil {
		t.Fatalf("GetMetrics() error = %v", err)
	}
	if metrics.InstanceID != "workload-a" || metrics.CPUUtilizationPct == nil || *metrics.CPUUtilizationPct != 23.5 {
		t.Fatalf("metrics = %+v, want Prometheus CPU utilization", metrics)
	}
	if !metrics.Timestamp.Equal(time.Unix(1780000000, 0).UTC()) {
		t.Fatalf("timestamp = %s, want Prometheus sample timestamp", metrics.Timestamp)
	}
	if metrics.DevProfile.Provider != "prometheus-kubernetes-instance-observability" || metrics.DevProfile.RealProvider {
		t.Fatalf("metrics dev profile = %+v, want Prometheus/Kubernetes contract marker", metrics.DevProfile)
	}
}

func TestPrometheusInstanceObservabilityStreamsLogsFromResolvedPod(t *testing.T) {
	var requests []string
	var streamAccept string
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		switch r.URL.Path {
		case "/api/v1/namespaces/ani-tenant-tenant-a/pods":
			return jsonResponse(http.StatusOK, `{
				"items": [
					{"metadata":{"name":"container-ttt-094ae46b-f6f97bd95-vwdv2"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}
				]
			}`), nil
		case "/api/v1/namespaces/ani-tenant-tenant-a/pods/container-ttt-094ae46b-f6f97bd95-vwdv2/log":
			streamAccept = r.Header.Get("Accept")
			query, _ := url.QueryUnescape(r.URL.RawQuery)
			if !strings.Contains(query, "follow=true") || !strings.Contains(query, "tailLines=25") {
				t.Fatalf("log query = %q, want follow and tailLines", query)
			}
			return jsonResponse(http.StatusOK, "info booted\nwarn warming\n"), nil
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
			return nil, nil
		}
	})
	var entries []ports.InstanceLogEntry
	err := service.StreamLogs(context.Background(), ports.InstanceLogStreamRequest{
		TenantID:   "tenant-a",
		InstanceID: "container-ttt-094ae46b",
		TailLines:  25,
	}, func(entry ports.InstanceLogEntry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamLogs() error = %v", err)
	}
	if streamAccept != "*/*" {
		t.Fatalf("Accept = %q, want */* for Kubernetes log follow streams", streamAccept)
	}
	if len(entries) != 2 || entries[0].Message != "info booted" || entries[1].Level != "warn" {
		t.Fatalf("entries = %+v, want streamed log entries", entries)
	}
	if len(requests) != 2 || strings.Contains(requests[1], "/pods/container-ttt-094ae46b/log") {
		t.Fatalf("requests = %+v, want resolved Pod log stream request", requests)
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
	if first.Token == "" {
		t.Fatalf("token is empty, want short-lived websocket token")
	}
	if !strings.HasPrefix(first.WSURL, "wss://gateway.example.test/api/v1/instances/pod-a/exec/") {
		t.Fatalf("ws_url = %q, want gateway exec URL", first.WSURL)
	}
	if !strings.Contains(first.WSURL, "token="+url.QueryEscape(first.Token)) {
		t.Fatalf("ws_url = %q, want embedded websocket token", first.WSURL)
	}
	if !first.ExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires_at = %s, want 15 minute TTL", first.ExpiresAt)
	}
}

func TestPrometheusInstanceObservabilityConnectsKubernetesExecTerminalStream(t *testing.T) {
	service := newTestPrometheusInstanceObservability(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/namespaces/ani-tenant-tenant-a/pods" {
			t.Fatalf("unexpected HTTP request before exec dial: %s", r.URL.String())
		}
		return jsonResponse(http.StatusOK, `{
			"items": [
				{"metadata":{"name":"pod-a"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}
			]
		}`), nil
	})
	stream := &fakeInstanceExecTerminalStream{
		recv: []ports.InstanceExecTerminalClientMessage{
			{Op: "stdin", Data: []byte("ls\n")},
			{Op: "resize", Cols: 120, Rows: 30},
		},
	}
	conn := &fakeKubernetesExecConnection{
		readFrames: [][]byte{
			append([]byte{1}, []byte("ok\n")...),
			append([]byte{2}, []byte("warn\n")...),
		},
	}
	service.execDialer = func(ctx context.Context, request kubernetesExecRequest) (io.ReadWriteCloser, error) {
		if request.Namespace != "ani-tenant-tenant-a" || request.PodName != "pod-a" {
			t.Fatalf("exec target namespace=%q pod=%q, want resolved pod", request.Namespace, request.PodName)
		}
		if got := request.Query.Get("command"); got != "/bin/sh" {
			t.Fatalf("exec command = %q, want /bin/sh", got)
		}
		if request.Query.Get("stdin") != "true" || request.Query.Get("stdout") != "true" || request.Query.Get("stderr") != "true" || request.Query.Get("tty") != "true" {
			t.Fatalf("exec query = %s, want stdin/stdout/stderr/tty true", request.Query.Encode())
		}
		return conn, nil
	}

	err := service.ConnectExecSession(context.Background(), ports.InstanceExecSessionRecord{
		TenantID:   "tenant-a",
		InstanceID: "workload-a",
		Command:    []string{"/bin/sh"},
		TTY:        true,
		Rows:       24,
		Cols:       80,
	}, stream)
	if err != nil {
		t.Fatalf("ConnectExecSession() error = %v", err)
	}

	if got := string(conn.writes[0]); got != "\x00ls\n" {
		t.Fatalf("stdin frame = %q, want Kubernetes stdin channel frame", got)
	}
	if got := string(conn.writes[1]); got != "\x04{\"Width\":120,\"Height\":30}" {
		t.Fatalf("resize frame = %q, want Kubernetes resize channel frame", got)
	}
	if len(stream.sent) != 2 || string(stream.sent[0].Data) != "ok\n" || string(stream.sent[1].Data) != "warn\n" {
		t.Fatalf("terminal output = %+v, want stdout projection for Kubernetes stdout/stderr channels", stream.sent)
	}
}

func TestKubernetesExecTLSConfigForcesHTTP1WebSocketUpgrade(t *testing.T) {
	client := &KubernetesRESTClient{
		httpClient: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				NextProtos: []string{"h2", "http/1.1"},
			},
		}},
	}

	config := kubernetesExecTLSConfig(client, "kubernetes.default.svc")
	if got := strings.Join(config.NextProtos, ","); got != "http/1.1" {
		t.Fatalf("exec TLS ALPN = %q, want http/1.1 only", got)
	}
	if config.ServerName != "kubernetes.default.svc" {
		t.Fatalf("exec TLS server name = %q, want kubernetes.default.svc", config.ServerName)
	}
}

type fakeInstanceExecTerminalStream struct {
	recv []ports.InstanceExecTerminalClientMessage
	sent []ports.InstanceExecTerminalServerMessage
}

func (s *fakeInstanceExecTerminalStream) Recv(_ context.Context) (ports.InstanceExecTerminalClientMessage, error) {
	if len(s.recv) == 0 {
		return ports.InstanceExecTerminalClientMessage{}, io.EOF
	}
	msg := s.recv[0]
	s.recv = s.recv[1:]
	return msg, nil
}

func (s *fakeInstanceExecTerminalStream) Send(_ context.Context, msg ports.InstanceExecTerminalServerMessage) error {
	s.sent = append(s.sent, msg)
	return nil
}

type fakeKubernetesExecConnection struct {
	readFrames [][]byte
	writes     [][]byte
}

func (c *fakeKubernetesExecConnection) Read(payload []byte) (int, error) {
	if len(c.readFrames) == 0 {
		return 0, io.EOF
	}
	frame := c.readFrames[0]
	c.readFrames = c.readFrames[1:]
	copy(payload, frame)
	return len(frame), nil
}

func (c *fakeKubernetesExecConnection) Write(payload []byte) (int, error) {
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return len(payload), nil
}

func (c *fakeKubernetesExecConnection) Close() error {
	return nil
}

func newTestPrometheusInstanceObservability(t *testing.T, roundTrip roundTripFunc) *PrometheusInstanceObservability {
	t.Helper()
	return newTestPrometheusInstanceObservabilityWithClock(t, roundTrip, func() time.Time {
		return time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	})
}

func newTestPrometheusInstanceObservabilityWithClock(t *testing.T, roundTrip roundTripFunc, now func() time.Time) *PrometheusInstanceObservability {
	t.Helper()
	var transport http.RoundTripper = http.DefaultTransport
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
