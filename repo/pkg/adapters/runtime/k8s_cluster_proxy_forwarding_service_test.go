package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestK8sClusterProxyForwardingServiceForwardsToResolvedAPIServer(t *testing.T) {
	base := NewLocalK8sClusterService()
	cluster, err := base.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	transport := &capturingK8sProxyRoundTripper{
		statusCode: http.StatusCreated,
		headers: http.Header{
			"Content-Type":          []string{"application/json"},
			"X-Kubernetes-Audit-ID": []string{"audit-1"},
		},
		body: `{"kind":"Pod","metadata":{"name":"demo-pod"}}`,
	}

	resolver := staticK8sProxyTargetResolver{target: ports.K8sClusterProxyTarget{
		TenantID:    "tenant-a",
		ClusterID:   cluster.ClusterID,
		Server:      "https://tenant-a-vcluster.example",
		BearerToken: "tenant-token",
	}}
	service := NewK8sClusterProxyForwardingService(
		base,
		resolver,
		WithK8sClusterProxyForwardingHTTPClient(&http.Client{Transport: transport}),
		WithK8sClusterProxyForwardingClock(func() time.Time { return time.Unix(700, 0) }),
	)

	result, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-1",
		Method:         "post",
		Path:           "api/v1/namespaces/default/pods",
		Query:          map[string]string{"limit": "20"},
		Body:           map[string]any{"metadata": map[string]any{"name": "demo-pod"}},
	})
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}

	if transport.method != http.MethodPost {
		t.Fatalf("upstream method = %s, want POST", transport.method)
	}
	if transport.path != "/api/v1/namespaces/default/pods" {
		t.Fatalf("upstream path = %s", transport.path)
	}
	if transport.query != "limit=20" {
		t.Fatalf("upstream query = %s, want limit=20", transport.query)
	}
	if transport.authorization != "Bearer tenant-token" {
		t.Fatalf("upstream authorization = %q", transport.authorization)
	}
	if metadata, _ := transport.decodedBody["metadata"].(map[string]any); metadata["name"] != "demo-pod" {
		t.Fatalf("upstream body = %+v", transport.decodedBody)
	}
	if result.StatusCode != http.StatusCreated || result.Body["kind"] != "Pod" {
		t.Fatalf("proxy result = %+v", result)
	}
	if result.Headers["x-kubernetes-audit-id"] != "audit-1" {
		t.Fatalf("proxy headers = %+v", result.Headers)
	}
	if result.ProxiedAt != 700 {
		t.Fatalf("ProxiedAt = %d, want 700", result.ProxiedAt)
	}
}

func TestK8sClusterProxyForwardingServiceRejectsMismatchedResolvedTarget(t *testing.T) {
	base := NewLocalK8sClusterService()
	cluster, err := base.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewK8sClusterProxyForwardingService(
		base,
		staticK8sProxyTargetResolver{target: ports.K8sClusterProxyTarget{
			TenantID:  "tenant-b",
			ClusterID: cluster.ClusterID,
			Server:    "https://tenant-b.invalid",
		}},
	)

	if _, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-1",
		Method:         "GET",
		Path:           "/version",
	}); err == nil {
		t.Fatalf("want mismatched resolved target error")
	}
}

type staticK8sProxyTargetResolver struct {
	target ports.K8sClusterProxyTarget
}

func (r staticK8sProxyTargetResolver) ResolveK8sClusterProxyTarget(context.Context, ports.K8sClusterGetRequest) (ports.K8sClusterProxyTarget, error) {
	return r.target, nil
}

type capturingK8sProxyRoundTripper struct {
	statusCode    int
	headers       http.Header
	body          string
	method        string
	path          string
	query         string
	authorization string
	decodedBody   map[string]any
}

func (t *capturingK8sProxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.method = req.Method
	t.path = req.URL.Path
	t.query = req.URL.RawQuery
	t.authorization = req.Header.Get("Authorization")
	if req.Body != nil {
		defer req.Body.Close()
		if err := json.NewDecoder(req.Body).Decode(&t.decodedBody); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: t.statusCode,
		Header:     t.headers,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}
