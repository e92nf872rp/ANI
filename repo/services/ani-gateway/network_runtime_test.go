package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayNetworkServiceFromConfigDefaultsToRouterLocalService(t *testing.T) {
	service, _, err := newGatewayNetworkService(context.Background(), gatewayNetworkRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayNetworkService() error = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %T, want nil so router keeps local default", service)
	}
}

func TestGatewayNetworkServiceFromConfigUsesKubeOVNProvider(t *testing.T) {
	transport := &gatewayNetworkRoundTripper{}
	service, _, err := newGatewayNetworkService(context.Background(), gatewayNetworkRuntimeConfig{
		ProviderMode:         "kubeovn_rest",
		ProviderApply:        true,
		ProviderUserID:       "ani-core-network-provider",
		ProviderProof:        "rbac-scope:networks.write",
		KubernetesAPIHost:    "https://kubernetes.example.test",
		KubernetesHTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayNetworkService() error = %v", err)
	}
	if service == nil {
		t.Fatalf("service = nil, want provider-backed network service")
	}
	vpc, err := service.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "network-vpc-a",
		Name:           "tenant-a-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	route, err := service.CreateRoute(context.Background(), ports.NetworkRouteCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "network-route-a",
		VPCID:           vpc.VPCID,
		DestinationCIDR: "10.250.0.0/16",
		NextHopType:     "gateway",
		NextHopID:       "10.244.180.1",
	})
	if err != nil {
		t.Fatalf("CreateRoute() error = %v", err)
	}
	if route.State != ports.NetworkResourceAvailable {
		t.Fatalf("route state = %s, want available from Kube-OVN observe", route.State)
	}
	if transport.postCalls != 0 || transport.patchCalls != 4 || transport.getCalls != 2 {
		t.Fatalf("transport post=%d patch=%d get=%d, want VPC + route dry-run/apply PATCH and observe", transport.postCalls, transport.patchCalls, transport.getCalls)
	}
}

func TestGatewayNetworkServiceRejectsKubeOVNProviderWithoutProof(t *testing.T) {
	if _, _, err := newGatewayNetworkService(context.Background(), gatewayNetworkRuntimeConfig{
		ProviderMode:      "kubeovn_rest",
		KubernetesAPIHost: "https://kubernetes.example.test",
	}); err == nil {
		t.Fatalf("newGatewayNetworkService() error = nil, want missing proof error")
	}
}

func TestGatewayNetworkConfigFromEnvIncludesInClusterKubernetesService(t *testing.T) {
	t.Setenv("NETWORK_PROVIDER", "kubeovn_rest")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")

	cfg := gatewayNetworkRuntimeConfigFromEnv()
	if cfg.KubernetesServiceHost != "10.96.0.1" || cfg.KubernetesServicePort != "443" {
		t.Fatalf("service host/port = %q/%q, want in-cluster Kubernetes service", cfg.KubernetesServiceHost, cfg.KubernetesServicePort)
	}
	if cfg.KubernetesServiceAccountTokenFile == "" || cfg.KubernetesServiceAccountCAFile == "" {
		t.Fatalf("service account files not loaded from env: %#v", cfg)
	}
}

type gatewayNetworkRoundTripper struct {
	postCalls  int
	patchCalls int
	getCalls   int
}

func (t *gatewayNetworkRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.Contains(req.URL.Path, "/apis/kubeovn.io/v1/vpcs") {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"not found"}`)), Header: http.Header{}}, nil
	}
	switch req.Method {
	case http.MethodPost:
		t.postCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"kind":"Vpc"}`)), Header: http.Header{}}, nil
	case http.MethodPatch:
		t.patchCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"kind":"Vpc"}`)), Header: http.Header{}}, nil
	case http.MethodGet:
		t.getCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"kind":"Vpc","status":{"conditions":[{"type":"Ready","status":"True"}]}}`)),
			Header:     http.Header{},
		}, nil
	default:
		return &http.Response{StatusCode: http.StatusMethodNotAllowed, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	}
}
