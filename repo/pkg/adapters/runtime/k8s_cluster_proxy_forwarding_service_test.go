package runtime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
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

func TestK8sClusterProxyForwardingServiceListsWorkloadsFromResolvedAPIServer(t *testing.T) {
	clusterProvider := &fakeK8sClusterForwardingProvider{
		result: ports.K8sClusterProviderApplyResult{
			Applied:      true,
			Provider:     "vcluster",
			ResourceRefs: []string{"vcluster/HelmRelease/vc-workloads"},
			Reason:       "vCluster Helm release applied",
		},
	}
	base := NewLocalK8sClusterService(WithK8sClusterProviderApply(clusterProvider))
	cluster, err := base.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-workloads",
		Name:           "vc-workloads",
		Version:        "v1.36.1",
	})
	if err != nil {
		t.Fatal(err)
	}

	transport := &capturingK8sProxyRoundTripper{
		statusCode: http.StatusOK,
		headers:    http.Header{"Content-Type": []string{"application/json"}},
		body: `{
			"kind":"DeploymentList",
			"items":[{
				"metadata":{"name":"web","namespace":"default","creationTimestamp":"2026-06-19T10:00:00Z"},
				"spec":{"replicas":3,"template":{"spec":{"containers":[{"image":"registry.example/web:v1"}]}}},
				"status":{"readyReplicas":2}
			}]
		}`,
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
	)

	workloads, err := service.ListWorkloads(context.Background(), ports.K8sClusterWorkloadListRequest{
		TenantID:  "tenant-a",
		ClusterID: cluster.ClusterID,
		Namespace: "default",
		Kind:      "Deployment",
	})
	if err != nil {
		t.Fatalf("ListWorkloads() error = %v", err)
	}
	if transport.method != http.MethodGet || transport.path != "/apis/apps/v1/namespaces/default/deployments" {
		t.Fatalf("upstream request = %s %s, want GET deployments", transport.method, transport.path)
	}
	if transport.authorization != "Bearer tenant-token" {
		t.Fatalf("upstream authorization = %q", transport.authorization)
	}
	if len(workloads) != 1 {
		t.Fatalf("workloads = %+v, want one Deployment", workloads)
	}
	got := workloads[0]
	if got.Name != "web" || got.Namespace != "default" || got.Kind != "Deployment" || got.Replicas != 3 || got.ReadyReplicas != 2 || got.Image != "registry.example/web:v1" || got.Status != ports.K8sWorkloadPending {
		t.Fatalf("workload = %+v, want parsed pending Deployment", got)
	}
}

func TestK8sClusterProxyForwardingServiceUsesClientCertificateTarget(t *testing.T) {
	base := NewLocalK8sClusterService()
	cluster, err := base.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-mtls-vc",
		Name:           "mtls-vc",
		Version:        "v1.35.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	clientCertPEM, clientKeyPEM, clientCert := mustSelfSignedClientCertificate(t)
	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(clientCert)

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Fatalf("upstream did not receive client certificate")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"35","gitVersion":"v1.35.0"}`))
	}))
	upstream.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAPool,
	}
	upstream.StartTLS()
	defer upstream.Close()

	resolver := staticK8sProxyTargetResolver{target: ports.K8sClusterProxyTarget{
		TenantID:              "tenant-a",
		ClusterID:             cluster.ClusterID,
		Server:                upstream.URL,
		CAData:                base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})),
		ClientCertificateData: base64.StdEncoding.EncodeToString(clientCertPEM),
		ClientKeyData:         base64.StdEncoding.EncodeToString(clientKeyPEM),
	}}
	service := NewK8sClusterProxyForwardingService(
		base,
		resolver,
		WithK8sClusterProxyForwardingClock(func() time.Time { return time.Unix(800, 0) }),
	)

	result, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-mtls",
		Method:         "GET",
		Path:           "/version",
	})
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if result.StatusCode != http.StatusOK || result.Body["gitVersion"] != "v1.35.0" {
		t.Fatalf("proxy result = %+v, want Kubernetes version through mTLS target", result)
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

type fakeK8sClusterForwardingProvider struct {
	result ports.K8sClusterProviderApplyResult
}

func (p *fakeK8sClusterForwardingProvider) ApplyK8sCluster(context.Context, ports.K8sClusterProviderApplyRequest) (ports.K8sClusterProviderApplyResult, error) {
	return p.result, nil
}

func (t *capturingK8sProxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.method = req.Method
	t.path = req.URL.Path
	t.query = req.URL.RawQuery
	t.authorization = req.Header.Get("Authorization")
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&t.decodedBody); err != nil {
			return nil, err
		}
		if err := req.Body.Close(); err != nil {
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

func mustSelfSignedClientCertificate(t *testing.T) ([]byte, []byte, *x509.Certificate) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ani-sprint13-vcluster-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, cert
}
