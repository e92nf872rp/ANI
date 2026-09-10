package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	platformServiceHealthDefaultTimeout = 3 * time.Second
	platformServiceHealthMaxTimeout     = 5 * time.Second
	platformServiceHealthFreshness      = 45 * time.Second
	platformServiceHealthTimeTolerance  = time.Millisecond
	platformServiceHealthMaxBodyBytes   = 2 << 20
)

var platformServiceNames = []string{
	"ani-gateway",
	"auth-service",
	"model-service",
	"task-service",
	"inference-service",
	"tenant-service",
	"metering-service",
}

var platformServiceHealthQueries = []string{
	`up{job="ani-components"}`,
	`timestamp(up{job="ani-components"})`,
	`target_info{job="ani-components"}`,
	`timestamp(target_info{job="ani-components"})`,
}

type PrometheusPlatformServiceHealthReaderConfig struct {
	BaseURL      string
	QueryTimeout time.Duration
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Now          func() time.Time
}

type PrometheusPlatformServiceHealthReader struct {
	queryURL     *url.URL
	queryTimeout time.Duration
	httpClient   *http.Client
	logger       *slog.Logger
	now          func() time.Time
}

func NewPrometheusPlatformServiceHealthReader(config PrometheusPlatformServiceHealthReaderConfig) (*PrometheusPlatformServiceHealthReader, error) {
	queryURL, err := platformServiceHealthQueryURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if config.QueryTimeout == 0 {
		config.QueryTimeout = platformServiceHealthDefaultTimeout
	}
	if config.QueryTimeout < 0 || config.QueryTimeout > platformServiceHealthMaxTimeout {
		return nil, fmt.Errorf("platform service health query timeout must be positive and at most %s", platformServiceHealthMaxTimeout)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &PrometheusPlatformServiceHealthReader{
		queryURL:     queryURL,
		queryTimeout: config.QueryTimeout,
		httpClient:   config.HTTPClient,
		logger:       config.Logger,
		now:          config.Now,
	}, nil
}

func (reader *PrometheusPlatformServiceHealthReader) ReadPlatformServiceHealth(ctx context.Context) (ports.PlatformServiceHealth, error) {
	evaluationTime := reader.now().UTC()
	queryContext, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()

	type queryResult struct {
		query  string
		series []prometheusInstantSample
		err    error
	}
	results := make(chan queryResult, len(platformServiceHealthQueries))
	for _, query := range platformServiceHealthQueries {
		go func() {
			series, err := reader.query(queryContext, query, evaluationTime)
			results <- queryResult{query: query, series: series, err: err}
		}()
	}

	byQuery := make(map[string][]prometheusInstantSample, len(platformServiceHealthQueries))
	for range platformServiceHealthQueries {
		select {
		case result := <-results:
			if result.err != nil {
				cancel()
				return ports.PlatformServiceHealth{}, fmt.Errorf("query platform service health source: %w", result.err)
			}
			byQuery[result.query] = result.series
		case <-queryContext.Done():
			return ports.PlatformServiceHealth{}, fmt.Errorf("query platform service health source: %w", queryContext.Err())
		}
	}

	upSeries, err := pairPrometheusSeries(
		byQuery[platformServiceHealthQueries[0]],
		byQuery[platformServiceHealthQueries[1]],
	)
	if err != nil {
		return ports.PlatformServiceHealth{}, fmt.Errorf("join up samples: %w", err)
	}
	targetInfoSeries, err := pairPrometheusSeries(
		byQuery[platformServiceHealthQueries[2]],
		byQuery[platformServiceHealthQueries[3]],
	)
	if err != nil {
		return ports.PlatformServiceHealth{}, fmt.Errorf("join target_info samples: %w", err)
	}

	return reader.aggregate(evaluationTime, upSeries, targetInfoSeries)
}

func (reader *PrometheusPlatformServiceHealthReader) query(ctx context.Context, query string, evaluationTime time.Time) ([]prometheusInstantSample, error) {
	requestURL := *reader.queryURL
	parameters := requestURL.Query()
	parameters.Set("query", query)
	parameters.Set("time", formatPrometheusTime(evaluationTime))
	requestURL.RawQuery = parameters.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Prometheus request: %w", err)
	}
	response, err := reader.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute Prometheus request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("prometheus returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, platformServiceHealthMaxBodyBytes))
	var payload prometheusInstantResponse
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Prometheus response: %w", err)
	}
	if payload.Status != "success" {
		return nil, errors.New("prometheus response status is not success")
	}
	if payload.Data.ResultType != "vector" {
		return nil, fmt.Errorf("prometheus result type %q is not vector", payload.Data.ResultType)
	}
	series := make([]prometheusInstantSample, 0, len(payload.Data.Result))
	for index, sample := range payload.Data.Result {
		if len(sample.Value) != 2 {
			return nil, fmt.Errorf("prometheus sample %d does not have a scalar pair", index)
		}
		observedAt, err := parsePrometheusNumber(sample.Value[0])
		if err != nil {
			return nil, fmt.Errorf("parse Prometheus sample %d evaluation time: %w", index, err)
		}
		requestedAt := float64(evaluationTime.UnixNano()) / float64(time.Second)
		if math.Abs(observedAt-requestedAt) > platformServiceHealthTimeTolerance.Seconds() {
			return nil, fmt.Errorf("prometheus sample %d evaluation time does not match requested time", index)
		}
		value, err := parsePrometheusNumber(sample.Value[1])
		if err != nil {
			return nil, fmt.Errorf("parse Prometheus sample %d value: %w", index, err)
		}
		series = append(series, prometheusInstantSample{Labels: sample.Metric, EvaluationTime: observedAt, Value: value})
	}
	return series, nil
}

func (reader *PrometheusPlatformServiceHealthReader) aggregate(
	evaluationTime time.Time,
	upSeries []pairedPrometheusSeries,
	targetInfoSeries []pairedPrometheusSeries,
) (ports.PlatformServiceHealth, error) {
	allowedServices := make(map[string]struct{}, len(platformServiceNames))
	components := make(map[string]*ports.PlatformServiceHealthComponent, len(platformServiceNames))
	for _, serviceName := range platformServiceNames {
		allowedServices[serviceName] = struct{}{}
		components[serviceName] = &ports.PlatformServiceHealthComponent{
			ServiceName:  serviceName,
			ScrapeStatus: ports.PlatformServiceScrapeUnknown,
			Versions:     []string{},
		}
	}

	freshTargetInfo := make(map[string][]pairedPrometheusSeries)
	for _, series := range targetInfoSeries {
		serviceName := series.Sample.Labels["ani_service_name"]
		if _, allowed := allowedServices[serviceName]; !allowed {
			return ports.PlatformServiceHealth{}, fmt.Errorf("target_info contains non-whitelisted ani_service_name %q", serviceName)
		}
		fresh, _, err := sampleFreshness(evaluationTime, series.Timestamp.Value)
		if err != nil {
			return ports.PlatformServiceHealth{}, fmt.Errorf("validate target_info timestamp: %w", err)
		}
		if fresh {
			key, err := targetJoinKey(series.Sample.Labels)
			if err != nil {
				return ports.PlatformServiceHealth{}, fmt.Errorf("target_info identity: %w", err)
			}
			freshTargetInfo[key] = append(freshTargetInfo[key], series)
		}
	}

	versions := make(map[string]map[string]struct{}, len(platformServiceNames))
	for _, serviceName := range platformServiceNames {
		versions[serviceName] = make(map[string]struct{})
	}
	for _, series := range upSeries {
		serviceName := series.Sample.Labels["ani_service_name"]
		if _, allowed := allowedServices[serviceName]; !allowed {
			return ports.PlatformServiceHealth{}, fmt.Errorf("up contains non-whitelisted ani_service_name %q", serviceName)
		}
		fresh, age, err := sampleFreshness(evaluationTime, series.Timestamp.Value)
		if err != nil {
			return ports.PlatformServiceHealth{}, fmt.Errorf("validate up timestamp: %w", err)
		}
		if !fresh {
			continue
		}
		if series.Sample.Value != 0 && series.Sample.Value != 1 {
			return ports.PlatformServiceHealth{}, fmt.Errorf("up sample for %q is neither 0 nor 1", serviceName)
		}
		component := components[serviceName]
		component.ObservedReplicas++
		if component.SampleAgeSeconds == nil || age > *component.SampleAgeSeconds {
			ageCopy := age
			component.SampleAgeSeconds = &ageCopy
		}
		if series.Sample.Value == 0 {
			continue
		}

		joinKey, err := targetJoinKey(series.Sample.Labels)
		if err != nil {
			return ports.PlatformServiceHealth{}, fmt.Errorf("up target identity: %w", err)
		}
		matches := freshTargetInfo[joinKey]
		if len(matches) != 1 {
			return ports.PlatformServiceHealth{}, fmt.Errorf("reachable target %q has %d fresh target_info matches", joinKey, len(matches))
		}
		targetLabels := matches[0].Sample.Labels
		if targetLabels["service_namespace"] != "ani" || targetLabels["service_name"] != serviceName || strings.TrimSpace(targetLabels["service_instance_id"]) == "" {
			return ports.PlatformServiceHealth{}, fmt.Errorf("reachable target %q has invalid OTel identity", joinKey)
		}
		component.ReachableReplicas++
		kubernetesVersion := strings.TrimSpace(series.Sample.Labels["k8s_service_version"])
		telemetryVersion := strings.TrimSpace(targetLabels["service_version"])
		if kubernetesVersion != "" && telemetryVersion != "" && kubernetesVersion != telemetryVersion {
			reader.logger.Warn("platform service target version contract mismatch", "service", serviceName, "pod", series.Sample.Labels["pod"])
			continue
		}
		if kubernetesVersion != "" {
			versions[serviceName][kubernetesVersion] = struct{}{}
		}
	}

	result := ports.PlatformServiceHealth{
		Scope:        ports.PlatformServiceHealthScope,
		Coverage:     ports.PlatformServiceHealthCoverage,
		Signal:       ports.PlatformServiceHealthSignal,
		ObservedAt:   evaluationTime,
		SourceStatus: "ok",
		Components:   make([]ports.PlatformServiceHealthComponent, 0, len(platformServiceNames)),
	}
	for _, serviceName := range platformServiceNames {
		component := components[serviceName]
		switch {
		case component.ReachableReplicas > 0:
			component.ScrapeStatus = ports.PlatformServiceScrapeReachable
		case component.ObservedReplicas > 0:
			component.ScrapeStatus = ports.PlatformServiceScrapeUnreachable
		default:
			component.ScrapeStatus = ports.PlatformServiceScrapeUnknown
		}
		for version := range versions[serviceName] {
			component.Versions = append(component.Versions, version)
		}
		sort.Strings(component.Versions)
		result.Components = append(result.Components, *component)
	}
	return result, nil
}

func platformServiceHealthQueryURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil || parsed.Host == "" {
		return nil, errors.New("platform service health Prometheus URL is required and must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("platform service health Prometheus URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("platform service health Prometheus URL must not contain userinfo, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("platform service health Prometheus URL must be a base origin without a path")
	}
	parsed.Path = "/api/v1/query"
	return parsed, nil
}

type prometheusInstantResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusInstantSample struct {
	Labels         map[string]string
	EvaluationTime float64
	Value          float64
}

type pairedPrometheusSeries struct {
	Sample    prometheusInstantSample
	Timestamp prometheusInstantSample
}

func pairPrometheusSeries(samples, timestamps []prometheusInstantSample) ([]pairedPrometheusSeries, error) {
	timestampByKey := make(map[string]prometheusInstantSample, len(timestamps))
	for _, timestamp := range timestamps {
		key := fullLabelKey(timestamp.Labels)
		if _, exists := timestampByKey[key]; exists {
			return nil, fmt.Errorf("duplicate timestamp series for labels %q", key)
		}
		timestampByKey[key] = timestamp
	}
	paired := make([]pairedPrometheusSeries, 0, len(samples))
	seenSamples := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		key := fullLabelKey(sample.Labels)
		if _, exists := seenSamples[key]; exists {
			return nil, fmt.Errorf("duplicate sample series for labels %q", key)
		}
		seenSamples[key] = struct{}{}
		timestamp, exists := timestampByKey[key]
		if !exists {
			return nil, fmt.Errorf("missing timestamp series for labels %q", key)
		}
		delete(timestampByKey, key)
		paired = append(paired, pairedPrometheusSeries{Sample: sample, Timestamp: timestamp})
	}
	if len(timestampByKey) != 0 {
		return nil, errors.New("timestamp query contains unmatched series")
	}
	return paired, nil
}

func fullLabelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key != "__name__" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(strconv.Quote(key))
		builder.WriteByte('=')
		builder.WriteString(strconv.Quote(labels[key]))
		builder.WriteByte(',')
	}
	return builder.String()
}

func targetJoinKey(labels map[string]string) (string, error) {
	keys := []string{"job", "instance", "kubernetes_namespace", "pod"}
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(labels[key])
		if value == "" {
			return "", fmt.Errorf("target label %q is required", key)
		}
		values = append(values, value)
	}
	return strings.Join(values, "\x00"), nil
}

func sampleFreshness(evaluationTime time.Time, timestampSeconds float64) (bool, float64, error) {
	timestamp := time.Unix(0, int64(timestampSeconds*float64(time.Second))).UTC()
	if timestamp.After(evaluationTime) {
		return false, 0, errors.New("sample timestamp is later than evaluation time")
	}
	age := evaluationTime.Sub(timestamp).Seconds()
	return age <= platformServiceHealthFreshness.Seconds(), age, nil
}

func parsePrometheusNumber(raw json.RawMessage) (float64, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		parsed, parseErr := strconv.ParseFloat(value, 64)
		if parseErr != nil {
			return 0, parseErr
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, errors.New("prometheus number must be finite")
		}
		return parsed, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, errors.New("prometheus number must be finite")
	}
	return parsed, nil
}

func formatPrometheusTime(value time.Time) string {
	return strconv.FormatFloat(float64(value.UnixNano())/float64(time.Second), 'f', -1, 64)
}

var _ ports.PlatformServiceHealthReader = (*PrometheusPlatformServiceHealthReader)(nil)
