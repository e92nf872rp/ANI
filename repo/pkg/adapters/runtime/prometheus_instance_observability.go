package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type PrometheusInstanceObservabilityConfig struct {
	PrometheusURL                     string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesFieldManager            string
	ExecBaseURL                       string
	HTTPClient                        *http.Client
	Now                               func() time.Time
}

type PrometheusInstanceObservability struct {
	prometheusURL string
	kubeClient    *KubernetesRESTClient
	execBaseURL   string
	now           func() time.Time
	mu            sync.RWMutex
	sessions      map[string]ports.InstanceExecSessionRecord
}

func NewPrometheusInstanceObservability(config PrometheusInstanceObservabilityConfig) (*PrometheusInstanceObservability, error) {
	prometheusURL := strings.TrimRight(strings.TrimSpace(config.PrometheusURL), "/")
	if prometheusURL == "" {
		return nil, fmt.Errorf("%w: prometheus_url is required", ports.ErrNotConfigured)
	}
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:            config.KubernetesAPIHost,
		ServiceHost:     config.KubernetesServiceHost,
		ServicePort:     config.KubernetesServicePort,
		BearerToken:     config.KubernetesBearerToken,
		BearerTokenFile: config.KubernetesServiceAccountTokenFile,
		CAFile:          config.KubernetesServiceAccountCAFile,
		FieldManager:    firstNonEmpty(config.KubernetesFieldManager, "ani-instance-observability"),
		HTTPClient:      config.HTTPClient,
		Now:             config.Now,
	})
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PrometheusInstanceObservability{
		prometheusURL: prometheusURL,
		kubeClient:    client,
		execBaseURL:   strings.TrimRight(firstNonEmpty(strings.TrimSpace(config.ExecBaseURL), "ws://127.0.0.1:8080/api/v1"), "/"),
		now:           now,
		sessions:      make(map[string]ports.InstanceExecSessionRecord),
	}, nil
}

func (o *PrometheusInstanceObservability) ListLogs(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceLogListResult{}, err
	}
	query := url.Values{}
	if request.Limit > 0 {
		query.Set("tailLines", strconv.Itoa(normalizeLimit(request.Limit, 100, 1000)))
	}
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+podPath(tenantNamespace(request.TenantID), request.InstanceID)+"/log?"+query.Encode(), "", nil)
	if err != nil {
		return ports.InstanceLogListResult{}, err
	}
	items := parseInstanceLogEntries(string(body), o.now().UTC())
	items = filterLogs(items, request.Level)
	items = limitLogEntries(items, normalizeLimit(request.Limit, 100, 1000))
	return ports.InstanceLogListResult{Items: items, Total: len(items), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) ListEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceEventListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceEventListResult{}, err
	}
	events = filterEvents(events, request.Type)
	events = limitEventRecords(events, normalizeLimit(request.Limit, 50, 500))
	return ports.InstanceEventListResult{Items: events, Total: len(events), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) GetMetrics(ctx context.Context, request ports.InstanceObservationGetRequest) (ports.InstanceMetricsRecord, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceMetricsRecord{}, err
	}
	query := fmt.Sprintf(`container_cpu_usage_seconds_total{namespace=%q,pod=%q}`, tenantNamespace(request.TenantID), request.InstanceID)
	sample, err := o.queryPrometheusScalar(ctx, query)
	if err != nil {
		return ports.InstanceMetricsRecord{}, err
	}
	return ports.InstanceMetricsRecord{
		InstanceID:        request.InstanceID,
		Timestamp:         sample.Timestamp,
		CPUUtilizationPct: &sample.Value,
		DevProfile:        prometheusInstanceObservabilityDevProfile(),
	}, nil
}

func (o *PrometheusInstanceObservability) ListSecurityEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceSecurityEventListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceSecurityEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceSecurityEventListResult{}, err
	}
	items := make([]ports.InstanceSecurityEventRecord, 0, len(events))
	for _, event := range events {
		if event.Type != "Warning" {
			continue
		}
		items = append(items, ports.InstanceSecurityEventRecord{
			ID:          event.ID,
			InstanceID:  request.InstanceID,
			EventType:   "kubernetes_warning",
			Severity:    "warning",
			Description: strings.TrimSpace(event.Reason + ": " + event.Message),
			OccurredAt:  event.OccurredAt,
		})
	}
	items = filterSecurityEvents(items, request.Severity)
	items = limitSecurityEventRecords(items, normalizeLimit(request.Limit, 50, 500))
	return ports.InstanceSecurityEventListResult{Items: items, Total: len(items), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) CreateExecSession(_ context.Context, request ports.InstanceExecSessionCreateRequest) (ports.InstanceExecSessionRecord, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceExecSessionRecord{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.InstanceExecSessionRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	key := request.TenantID + "/" + request.InstanceID + "/" + request.IdempotencyKey
	o.mu.RLock()
	if record, ok := o.sessions[key]; ok {
		o.mu.RUnlock()
		return record, nil
	}
	o.mu.RUnlock()

	now := o.now().UTC()
	sessionID := uuid.NewString()
	record := ports.InstanceExecSessionRecord{
		ID:         sessionID,
		InstanceID: request.InstanceID,
		WSURL:      o.execBaseURL + "/instances/" + url.PathEscape(request.InstanceID) + "/exec/" + sessionID,
		ExpiresAt:  now.Add(15 * time.Minute),
		DevProfile: prometheusInstanceObservabilityDevProfile(),
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing, ok := o.sessions[key]; ok {
		return existing, nil
	}
	o.sessions[key] = record
	return record, nil
}

func (o *PrometheusInstanceObservability) readKubernetesEvents(ctx context.Context, tenantID string, instanceID string) ([]ports.InstanceEventRecord, error) {
	query := "fieldSelector=" + url.QueryEscape("involvedObject.name="+instanceID)
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+"/api/v1/namespaces/"+url.PathEscape(tenantNamespace(tenantID))+"/events?"+query, "", nil)
	if err != nil {
		return nil, err
	}
	return parseKubernetesEvents(body, instanceID, o.now().UTC())
}

func (o *PrometheusInstanceObservability) queryPrometheusScalar(ctx context.Context, query string) (prometheusScalarSample, error) {
	values := url.Values{"query": []string{query}}
	endpoint := o.prometheusURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.kubeClient.httpClient.Do(req)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return prometheusScalarSample{}, closeErr
		}
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus query returned %d", ports.ErrInvalid, resp.StatusCode)
	}
	var payload prometheusQueryResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
	closeErr := resp.Body.Close()
	if decodeErr != nil {
		return prometheusScalarSample{}, decodeErr
	}
	if closeErr != nil {
		return prometheusScalarSample{}, closeErr
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus query returned no samples", ports.ErrInvalid)
	}
	return payload.Data.Result[0].scalar(o.now().UTC())
}

func parseInstanceLogEntries(body string, timestamp time.Time) []ports.InstanceLogEntry {
	lines := strings.Split(body, "\n")
	items := make([]ports.InstanceLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, ports.InstanceLogEntry{
			Timestamp: timestamp,
			Level:     inferLogLevel(line),
			Message:   line,
			Container: "main",
			Stream:    "stdout",
		})
	}
	return items
}

func inferLogLevel(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(lower, "debug"), strings.Contains(lower, " debug "):
		return "debug"
	case strings.HasPrefix(lower, "warn"), strings.Contains(lower, " warning "), strings.Contains(lower, " warn "):
		return "warn"
	case strings.HasPrefix(lower, "error"), strings.Contains(lower, " error "):
		return "error"
	default:
		return "info"
	}
}

type kubernetesEventList struct {
	Items []kubernetesEvent `json:"items"`
}

type kubernetesEvent struct {
	Metadata struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	} `json:"metadata"`
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	EventTime      string `json:"eventTime"`
	LastTimestamp  string `json:"lastTimestamp"`
	FirstTimestamp string `json:"firstTimestamp"`
}

func parseKubernetesEvents(body []byte, instanceID string, fallback time.Time) ([]ports.InstanceEventRecord, error) {
	var payload kubernetesEventList
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	records := make([]ports.InstanceEventRecord, 0, len(payload.Items))
	for _, item := range payload.Items {
		records = append(records, ports.InstanceEventRecord{
			ID:         firstNonEmpty(item.Metadata.UID, item.Metadata.Name, uuid.NewString()),
			InstanceID: instanceID,
			Type:       item.Type,
			Reason:     item.Reason,
			Message:    item.Message,
			Count:      item.Count,
			OccurredAt: parseKubernetesTimestamp(firstNonEmpty(item.EventTime, item.LastTimestamp, item.FirstTimestamp), fallback),
		})
	}
	return records, nil
}

func parseKubernetesTimestamp(value string, fallback time.Time) time.Time {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fallback
	}
	return parsed.UTC()
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []prometheusVectorResult `json:"result"`
	} `json:"data"`
}

type prometheusVectorResult struct {
	Value []any `json:"value"`
}

type prometheusScalarSample struct {
	Timestamp time.Time
	Value     float64
}

func (r prometheusVectorResult) scalar(fallback time.Time) (prometheusScalarSample, error) {
	if len(r.Value) < 2 {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus sample value is incomplete", ports.ErrInvalid)
	}
	timestamp := fallback
	switch value := r.Value[0].(type) {
	case float64:
		timestamp = time.Unix(int64(value), 0).UTC()
	case string:
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			timestamp = time.Unix(int64(parsed), 0).UTC()
		}
	}
	raw, ok := r.Value[1].(string)
	if !ok {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus sample scalar is not a string", ports.ErrInvalid)
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return prometheusScalarSample{}, err
	}
	return prometheusScalarSample{Timestamp: timestamp, Value: parsed}, nil
}

func prometheusInstanceObservabilityDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "dev_profile",
		Provider:     "prometheus-kubernetes-instance-observability",
		RealProvider: false,
		Reason:       "Sprint 13 A-track adapter maps Prometheus and Kubernetes API contracts; live provider evidence remains human-gated",
	}
}

var _ ports.InstanceObservability = (*PrometheusInstanceObservability)(nil)
