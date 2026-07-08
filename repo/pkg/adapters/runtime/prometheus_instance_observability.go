package runtime

import (
	"bufio"
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
	execDialer    kubernetesExecDialer
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
		execDialer:    dialKubernetesExecWebSocket,
		now:           now,
		sessions:      make(map[string]ports.InstanceExecSessionRecord),
	}, nil
}

func (o *PrometheusInstanceObservability) ListLogs(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceLogListResult{}, err
	}
	podName, err := o.resolveObservationPodName(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceLogListResult{}, err
	}
	query := url.Values{}
	if request.Limit > 0 {
		query.Set("tailLines", strconv.Itoa(normalizeLimit(request.Limit, 100, 1000)))
	}
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+podPath(tenantNamespace(request.TenantID), podName)+"/log?"+query.Encode(), "", nil)
	if err != nil {
		return ports.InstanceLogListResult{}, err
	}
	items := parseInstanceLogEntries(string(body), o.now().UTC())
	items = filterLogs(items, request.Level)
	items = limitLogEntries(items, normalizeLimit(request.Limit, 100, 1000))
	return ports.InstanceLogListResult{Items: items, Total: len(items), DevProfile: prometheusInstanceObservabilityDevProfile()}, nil
}

func (o *PrometheusInstanceObservability) StreamLogs(ctx context.Context, request ports.InstanceLogStreamRequest, sink ports.InstanceLogStreamSink) error {
	if sink == nil {
		return fmt.Errorf("%w: log stream sink is required", ports.ErrInvalid)
	}
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return err
	}
	podName, err := o.resolveObservationPodName(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return err
	}
	query := url.Values{}
	query.Set("follow", "true")
	query.Set("tailLines", strconv.Itoa(normalizeLimit(request.TailLines, 100, 1000)))
	if strings.TrimSpace(request.Container) != "" {
		query.Set("container", strings.TrimSpace(request.Container))
	}
	endpoint := o.kubeClient.host + podPath(tenantNamespace(request.TenantID), podName) + "/log?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/plain")
	if o.kubeClient.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+o.kubeClient.bearerToken)
	}
	resp, err := o.kubeClient.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: Kubernetes API GET %s returned HTTP %d", ports.ErrInvalid, req.URL.Path, resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry := ports.InstanceLogEntry{
			Timestamp: o.now().UTC(),
			Level:     inferLogLevel(scanner.Text()),
			Message:   scanner.Text(),
			Container: firstNonEmpty(strings.TrimSpace(request.Container), "main"),
			Stream:    "stdout",
		}
		if strings.TrimSpace(request.Level) != "" && entry.Level != strings.TrimSpace(request.Level) {
			continue
		}
		if err := sink(entry); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (o *PrometheusInstanceObservability) ListEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceEventListResult, error) {
	if err := validateInstanceObservationIdentity(request.TenantID, request.InstanceID); err != nil {
		return ports.InstanceEventListResult{}, err
	}
	podName, err := o.resolveObservationPodName(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID, podName)
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
	podName, err := o.resolveObservationPodName(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceMetricsRecord{}, err
	}
	query := fmt.Sprintf(`container_cpu_usage_seconds_total{namespace=%q,pod=%q}`, tenantNamespace(request.TenantID), podName)
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
	podName, err := o.resolveObservationPodName(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.InstanceSecurityEventListResult{}, err
	}
	events, err := o.readKubernetesEvents(ctx, request.TenantID, request.InstanceID, podName)
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
	token, err := newInstanceExecToken()
	if err != nil {
		return ports.InstanceExecSessionRecord{}, err
	}
	record := ports.InstanceExecSessionRecord{
		ID:         sessionID,
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
		WSURL:      instanceExecWSURL(o.execBaseURL, request.InstanceID, sessionID, token),
		Token:      token,
		Container:  request.Container,
		Command:    append([]string(nil), request.Command...),
		TTY:        request.TTY,
		Rows:       request.Rows,
		Cols:       request.Cols,
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

func (o *PrometheusInstanceObservability) GetExecSession(_ context.Context, request ports.InstanceExecSessionGetRequest) (ports.InstanceExecSessionRecord, error) {
	if strings.TrimSpace(request.InstanceID) == "" {
		return ports.InstanceExecSessionRecord{}, fmt.Errorf("%w: instance_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return ports.InstanceExecSessionRecord{}, fmt.Errorf("%w: session_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Token) == "" {
		return ports.InstanceExecSessionRecord{}, fmt.Errorf("%w: token is required", ports.ErrUnauthorized)
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, record := range o.sessions {
		if record.ID != request.SessionID {
			continue
		}
		if request.TenantID != "" && record.TenantID != request.TenantID {
			return ports.InstanceExecSessionRecord{}, ports.ErrNotFound
		}
		if record.InstanceID != request.InstanceID {
			return ports.InstanceExecSessionRecord{}, ports.ErrNotFound
		}
		if record.Token != request.Token {
			return ports.InstanceExecSessionRecord{}, ports.ErrUnauthorized
		}
		if !o.now().UTC().Before(record.ExpiresAt) {
			return ports.InstanceExecSessionRecord{}, ports.ErrExpired
		}
		return record, nil
	}
	return ports.InstanceExecSessionRecord{}, ports.ErrNotFound
}

func (o *PrometheusInstanceObservability) readKubernetesEvents(ctx context.Context, tenantID string, instanceID string, podName string) ([]ports.InstanceEventRecord, error) {
	query := "fieldSelector=" + url.QueryEscape("involvedObject.name="+podName)
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+"/api/v1/namespaces/"+url.PathEscape(tenantNamespace(tenantID))+"/events?"+query, "", nil)
	if err != nil {
		return nil, err
	}
	return parseKubernetesEvents(body, instanceID, o.now().UTC())
}

func (o *PrometheusInstanceObservability) resolveObservationPodName(ctx context.Context, tenantID string, instanceID string) (string, error) {
	pods, err := o.listObservationPods(ctx, tenantID, instanceID)
	if err != nil {
		return "", err
	}
	if len(pods) == 0 {
		return instanceID, nil
	}
	if podName := selectObservationPod(pods); podName != "" {
		return podName, nil
	}
	return instanceID, nil
}

func (o *PrometheusInstanceObservability) listObservationPods(ctx context.Context, tenantID string, instanceID string) ([]kubernetesPod, error) {
	values := url.Values{}
	values.Set("labelSelector", "ani.kubercloud.io/tenant-id="+tenantID+",ani.kubercloud.io/instance="+instanceID)
	body, err := o.kubeClient.do(ctx, http.MethodGet, o.kubeClient.host+"/api/v1/namespaces/"+url.PathEscape(tenantNamespace(tenantID))+"/pods?"+values.Encode(), "", nil)
	if err != nil {
		return nil, err
	}
	var payload kubernetesPodList
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return prometheusScalarSample{}, fmt.Errorf("%w: Prometheus query returned %d", ports.ErrInvalid, resp.StatusCode)
	}
	var payload prometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return prometheusScalarSample{}, err
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

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

func selectObservationPod(pods []kubernetesPod) string {
	for _, pod := range pods {
		if strings.TrimSpace(pod.Metadata.Name) == "" {
			continue
		}
		if pod.Status.Phase == "Running" && podReady(pod) {
			return pod.Metadata.Name
		}
	}
	for _, pod := range pods {
		if strings.TrimSpace(pod.Metadata.Name) != "" {
			return pod.Metadata.Name
		}
	}
	return ""
}

func podReady(pod kubernetesPod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return true
		}
	}
	return false
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
