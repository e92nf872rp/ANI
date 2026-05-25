package runtime

import (
	"bytes"
	"context"
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
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.K8sClusterProxyRecord{}, err
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
