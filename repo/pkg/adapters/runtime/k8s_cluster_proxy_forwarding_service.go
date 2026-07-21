package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type k8sClusterProxyForwardingService struct {
	base       ports.K8sClusterService
	resolver   ports.K8sClusterProxyTargetResolver
	httpClient *http.Client
	now        func() time.Time
}

type K8sClusterProxyForwardingOption func(*k8sClusterProxyForwardingService)

func WithK8sClusterProxyForwardingHTTPClient(client *http.Client) K8sClusterProxyForwardingOption {
	return func(service *k8sClusterProxyForwardingService) {
		if client != nil {
			service.httpClient = client
		}
	}
}

func WithK8sClusterProxyForwardingClock(now func() time.Time) K8sClusterProxyForwardingOption {
	return func(service *k8sClusterProxyForwardingService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewK8sClusterProxyForwardingService(base ports.K8sClusterService, resolver ports.K8sClusterProxyTargetResolver, options ...K8sClusterProxyForwardingOption) ports.K8sClusterService {
	service := &k8sClusterProxyForwardingService{
		base:       base,
		resolver:   resolver,
		httpClient: http.DefaultClient,
		now:        time.Now,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *k8sClusterProxyForwardingService) Health(ctx context.Context) error {
	if s.base == nil {
		return fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.Health(ctx)
}

func (s *k8sClusterProxyForwardingService) CreateCluster(ctx context.Context, req ports.K8sClusterCreateRequest) (ports.K8sClusterRecord, error) {
	if s.base == nil {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.CreateCluster(ctx, req)
}

func (s *k8sClusterProxyForwardingService) GetCluster(ctx context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	if s.base == nil {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.GetCluster(ctx, req)
}

func (s *k8sClusterProxyForwardingService) ListClusters(ctx context.Context, req ports.K8sClusterListRequest) ([]ports.K8sClusterRecord, error) {
	if s.base == nil {
		return nil, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.ListClusters(ctx, req)
}

func (s *k8sClusterProxyForwardingService) DeleteCluster(ctx context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	if s.base == nil {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.DeleteCluster(ctx, req)
}

func (s *k8sClusterProxyForwardingService) UpgradeCluster(ctx context.Context, req ports.K8sClusterUpgradeRequest) (ports.K8sClusterRecord, error) {
	if s.base == nil {
		return ports.K8sClusterRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.UpgradeCluster(ctx, req)
}

func (s *k8sClusterProxyForwardingService) CreateNodePool(ctx context.Context, req ports.K8sClusterNodePoolCreateRequest) (ports.K8sClusterNodePoolRecord, error) {
	if s.base == nil {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.CreateNodePool(ctx, req)
}

func (s *k8sClusterProxyForwardingService) GetNodePool(ctx context.Context, req ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	if s.base == nil {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.GetNodePool(ctx, req)
}

func (s *k8sClusterProxyForwardingService) ListNodePools(ctx context.Context, req ports.K8sClusterNodePoolListRequest) ([]ports.K8sClusterNodePoolRecord, error) {
	if s.base == nil {
		return nil, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.ListNodePools(ctx, req)
}

func (s *k8sClusterProxyForwardingService) UpdateNodePool(ctx context.Context, req ports.K8sClusterNodePoolUpdateRequest) (ports.K8sClusterNodePoolRecord, error) {
	if s.base == nil {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.UpdateNodePool(ctx, req)
}

func (s *k8sClusterProxyForwardingService) DeleteNodePool(ctx context.Context, req ports.K8sClusterNodePoolGetRequest) (ports.K8sClusterNodePoolRecord, error) {
	if s.base == nil {
		return ports.K8sClusterNodePoolRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.DeleteNodePool(ctx, req)
}

func (s *k8sClusterProxyForwardingService) GetKubeconfig(ctx context.Context, req ports.K8sClusterKubeconfigRequest) (ports.K8sClusterKubeconfigRecord, error) {
	if s.base == nil {
		return ports.K8sClusterKubeconfigRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	return s.base.GetKubeconfig(ctx, req)
}

func (s *k8sClusterProxyForwardingService) ListWorkloads(ctx context.Context, req ports.K8sClusterWorkloadListRequest) ([]ports.K8sClusterWorkloadRecord, error) {
	if s.base == nil {
		return nil, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	cluster, err := s.base.GetCluster(ctx, ports.K8sClusterGetRequest{TenantID: req.TenantID, ClusterID: req.ClusterID})
	if err != nil {
		return nil, err
	}
	if cluster.State != ports.K8sClusterStateRunning {
		return nil, fmt.Errorf("%w: list workloads requires a running k8s cluster", ports.ErrConflict)
	}
	if !cluster.RealProvider {
		return s.base.ListWorkloads(ctx, req)
	}
	if s.resolver == nil {
		return nil, fmt.Errorf("%w: k8s cluster proxy target resolver is required", ports.ErrNotConfigured)
	}
	target, err := s.resolver.ResolveK8sClusterProxyTarget(ctx, ports.K8sClusterGetRequest{TenantID: req.TenantID, ClusterID: req.ClusterID})
	if err != nil {
		return nil, err
	}
	if target.TenantID != req.TenantID || target.ClusterID != req.ClusterID {
		return nil, fmt.Errorf("%w: resolved k8s proxy target does not match request identity", ports.ErrInvalid)
	}
	paths, err := k8sWorkloadListPaths(req.Namespace, req.Kind)
	if err != nil {
		return nil, err
	}
	items := make([]ports.K8sClusterWorkloadRecord, 0)
	for _, workloadPath := range paths {
		upstreamURL, err := k8sProxyUpstreamURL(target.Server, workloadPath.path, nil)
		if err != nil {
			return nil, err
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		if target.BearerToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+target.BearerToken)
		}
		client, err := s.httpClientForTarget(target)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("%w: Kubernetes workload list returned HTTP %d", ports.ErrInvalid, resp.StatusCode)
		}
		records, err := k8sWorkloadsFromListDocument(workloadPath.kind, body)
		if err != nil {
			return nil, err
		}
		items = append(items, records...)
	}
	return items, nil
}

func (s *k8sClusterProxyForwardingService) Proxy(ctx context.Context, req ports.K8sClusterProxyRequest) (ports.K8sClusterProxyRecord, error) {
	if s.base == nil {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: base k8s cluster service is required", ports.ErrNotConfigured)
	}
	if s.resolver == nil {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: k8s cluster proxy target resolver is required", ports.ErrNotConfigured)
	}
	cluster, err := s.base.GetCluster(ctx, ports.K8sClusterGetRequest{TenantID: req.TenantID, ClusterID: req.ClusterID})
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	if cluster.State != ports.K8sClusterStateRunning {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: proxy requires a running k8s cluster", ports.ErrConflict)
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := normalizeK8sProxyPath(req.Path)
	if method == "" || path == "" {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: method/path required for k8s proxy", ports.ErrInvalid)
	}
	if !isAllowedK8sProxyPath(path) {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: k8s proxy path must start with /api/, /apis/, /healthz, /livez, /readyz or /version", ports.ErrInvalid)
	}
	if req.IdempotencyKey == "" {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: idempotency_key required for k8s proxy", ports.ErrInvalid)
	}
	target, err := s.resolver.ResolveK8sClusterProxyTarget(ctx, ports.K8sClusterGetRequest{TenantID: req.TenantID, ClusterID: req.ClusterID})
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	if target.TenantID != req.TenantID || target.ClusterID != req.ClusterID {
		return ports.K8sClusterProxyRecord{}, fmt.Errorf("%w: resolved k8s proxy target does not match request identity", ports.ErrInvalid)
	}
	upstreamURL, err := k8sProxyUpstreamURL(target.Server, path, req.Query)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	bodyBytes, err := k8sProxyRequestBody(req.Body)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	if bodyBytes != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if target.BearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+target.BearerToken)
	}
	client, err := s.httpClientForTarget(target)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	respBody, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	if closeErr != nil {
		return ports.K8sClusterProxyRecord{}, closeErr
	}
	decoded, err := k8sProxyResponseBody(respBody)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	return ports.K8sClusterProxyRecord{
		ClusterID:  req.ClusterID,
		TenantID:   req.TenantID,
		Method:     method,
		Path:       path,
		Query:      copyStringMap(req.Query),
		StatusCode: resp.StatusCode,
		Headers:    k8sProxyResponseHeaders(resp.Header),
		Body:       decoded,
		ProxiedAt:  s.now().UTC().Unix(),
	}, nil
}

func (s *k8sClusterProxyForwardingService) httpClientForTarget(target ports.K8sClusterProxyTarget) (*http.Client, error) {
	if target.CAData == "" && target.ClientCertificateData == "" && target.ClientKeyData == "" {
		return s.httpClient, nil
	}
	certPEM, err := decodeKubeconfigPEM("client certificate", target.ClientCertificateData)
	if err != nil {
		return nil, err
	}
	keyPEM, err := decodeKubeconfigPEM("client key", target.ClientKeyData)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid k8s proxy client certificate: %v", ports.ErrInvalid, err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	if target.CAData != "" {
		caPEM, err := decodeKubeconfigPEM("certificate authority", target.CAData)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("%w: invalid k8s proxy certificate authority data", ports.ErrInvalid)
		}
		tlsConfig.RootCAs = pool
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: s.httpClient.Timeout}, nil
}

func decodeKubeconfigPEM(label string, value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("%w: k8s proxy %s data is required", ports.ErrInvalid, label)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid k8s proxy %s data: %v", ports.ErrInvalid, label, err)
	}
	return decoded, nil
}

func k8sProxyUpstreamURL(server string, path string, query map[string]string) (string, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return "", fmt.Errorf("%w: k8s proxy target server is required", ports.ErrInvalid)
	}
	parsed, err := url.ParseRequestURI(server)
	if err != nil {
		return "", fmt.Errorf("%w: invalid k8s proxy target server: %v", ports.ErrInvalid, err)
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	parsed.RawQuery = ""
	return parsed.String() + path + querySuffix(values.Encode()), nil
}

type k8sWorkloadListPath struct {
	kind string
	path string
}

func k8sWorkloadListPaths(namespace string, kind string) ([]k8sWorkloadListPath, error) {
	kind = strings.TrimSpace(kind)
	if kind != "" {
		path, err := k8sWorkloadListPathForKind(namespace, kind)
		if err != nil {
			return nil, err
		}
		return []k8sWorkloadListPath{{kind: canonicalK8sWorkloadKind(kind), path: path}}, nil
	}
	kinds := []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}
	paths := make([]k8sWorkloadListPath, 0, len(kinds))
	for _, workloadKind := range kinds {
		path, err := k8sWorkloadListPathForKind(namespace, workloadKind)
		if err != nil {
			return nil, err
		}
		paths = append(paths, k8sWorkloadListPath{kind: workloadKind, path: path})
	}
	return paths, nil
}

func k8sWorkloadListPathForKind(namespace string, kind string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	canonical := canonicalK8sWorkloadKind(kind)
	var prefix string
	switch canonical {
	case "Deployment":
		prefix = "/apis/apps/v1"
		if namespace != "" {
			return prefix + "/namespaces/" + url.PathEscape(namespace) + "/deployments", nil
		}
		return prefix + "/deployments", nil
	case "StatefulSet":
		prefix = "/apis/apps/v1"
		if namespace != "" {
			return prefix + "/namespaces/" + url.PathEscape(namespace) + "/statefulsets", nil
		}
		return prefix + "/statefulsets", nil
	case "DaemonSet":
		prefix = "/apis/apps/v1"
		if namespace != "" {
			return prefix + "/namespaces/" + url.PathEscape(namespace) + "/daemonsets", nil
		}
		return prefix + "/daemonsets", nil
	case "Job":
		prefix = "/apis/batch/v1"
		if namespace != "" {
			return prefix + "/namespaces/" + url.PathEscape(namespace) + "/jobs", nil
		}
		return prefix + "/jobs", nil
	case "CronJob":
		prefix = "/apis/batch/v1"
		if namespace != "" {
			return prefix + "/namespaces/" + url.PathEscape(namespace) + "/cronjobs", nil
		}
		return prefix + "/cronjobs", nil
	default:
		return "", fmt.Errorf("%w: unsupported workload kind %q", ports.ErrInvalid, kind)
	}
}

func canonicalK8sWorkloadKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment", "deployments":
		return "Deployment"
	case "statefulset", "statefulsets":
		return "StatefulSet"
	case "daemonset", "daemonsets":
		return "DaemonSet"
	case "job", "jobs":
		return "Job"
	case "cronjob", "cronjobs":
		return "CronJob"
	default:
		return strings.TrimSpace(kind)
	}
}

func k8sWorkloadsFromListDocument(kind string, body []byte) ([]ports.K8sClusterWorkloadRecord, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes workload list response: %v", ports.ErrInvalid, err)
	}
	rawItems, ok := doc["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("%w: Kubernetes workload list response must include items", ports.ErrInvalid)
	}
	items := make([]ports.K8sClusterWorkloadRecord, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: Kubernetes workload item must be an object", ports.ErrInvalid)
		}
		record, err := k8sWorkloadFromObject(kind, item)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	return items, nil
}

func k8sWorkloadFromObject(kind string, item map[string]any) (ports.K8sClusterWorkloadRecord, error) {
	metadata, _ := item["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(namespace) == "" {
		return ports.K8sClusterWorkloadRecord{}, fmt.Errorf("%w: Kubernetes workload metadata.name and metadata.namespace are required", ports.ErrInvalid)
	}
	createdAt := time.Time{}
	if value, _ := metadata["creationTimestamp"].(string); strings.TrimSpace(value) != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return ports.K8sClusterWorkloadRecord{}, fmt.Errorf("%w: invalid Kubernetes workload creationTimestamp: %v", ports.ErrInvalid, err)
		}
		createdAt = parsed.UTC()
	}
	spec, _ := item["spec"].(map[string]any)
	status, _ := item["status"].(map[string]any)
	replicas := intFromK8sNumber(spec["replicas"])
	readyReplicas := intFromK8sNumber(status["readyReplicas"])
	canonicalKind := canonicalK8sWorkloadKind(kind)
	if canonicalKind == "DaemonSet" {
		replicas = intFromK8sNumber(status["desiredNumberScheduled"])
		readyReplicas = intFromK8sNumber(status["numberReady"])
	}
	if canonicalKind == "Job" {
		replicas = firstPositiveInt(intFromK8sNumber(spec["completions"]), intFromK8sNumber(spec["parallelism"]))
		readyReplicas = intFromK8sNumber(status["succeeded"])
	}
	return ports.K8sClusterWorkloadRecord{
		Name:          strings.TrimSpace(name),
		Namespace:     strings.TrimSpace(namespace),
		Kind:          canonicalKind,
		Replicas:      replicas,
		ReadyReplicas: readyReplicas,
		Image:         firstK8sWorkloadContainerImage(spec),
		Status:        k8sWorkloadStatusFromObject(canonicalKind, replicas, readyReplicas, status),
		CreatedAt:     createdAt,
	}, nil
}

func intFromK8sNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstK8sWorkloadContainerImage(spec map[string]any) string {
	if len(spec) == 0 {
		return ""
	}
	template, _ := spec["template"].(map[string]any)
	templateSpec, _ := template["spec"].(map[string]any)
	containers, _ := templateSpec["containers"].([]any)
	if len(containers) == 0 {
		jobTemplate, _ := spec["jobTemplate"].(map[string]any)
		jobSpec, _ := jobTemplate["spec"].(map[string]any)
		if len(jobSpec) == 0 {
			return ""
		}
		return firstK8sWorkloadContainerImage(jobSpec)
	}
	container, _ := containers[0].(map[string]any)
	image, _ := container["image"].(string)
	return strings.TrimSpace(image)
}

func k8sWorkloadStatusFromObject(kind string, replicas int, readyReplicas int, status map[string]any) ports.K8sWorkloadStatus {
	if intFromK8sNumber(status["failed"]) > 0 {
		return ports.K8sWorkloadFailed
	}
	if kind == "Job" && replicas > 0 && readyReplicas >= replicas {
		return ports.K8sWorkloadSucceeded
	}
	if replicas > 0 && readyReplicas >= replicas {
		return ports.K8sWorkloadRunning
	}
	return ports.K8sWorkloadPending
}

func k8sProxyRequestBody(body map[string]any) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	return json.Marshal(body)
}

func k8sProxyResponseBody(body []byte) (map[string]any, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return map[string]any{}, fmt.Errorf("%w: invalid k8s proxy JSON response: %v", ports.ErrInvalid, err)
	}
	return decoded, nil
}

func k8sProxyResponseHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[strings.ToLower(key)] = values[0]
	}
	return out
}

var _ ports.K8sClusterService = (*k8sClusterProxyForwardingService)(nil)
