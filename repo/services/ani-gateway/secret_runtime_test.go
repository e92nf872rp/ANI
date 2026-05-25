package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewaySecretServiceFromConfigUsesKubernetesRESTProvider(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"Secret"}`), nil
	})

	service, err := newGatewaySecretService(gatewaySecretRuntimeConfig{
		ProviderMode:              "kubernetes_rest",
		KubernetesAPIHost:         "https://kubernetes.example.test",
		KubernetesProviderManager: "ani-test",
		HTTPClient:                &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewaySecretService() error = %v", err)
	}
	if service == nil {
		t.Fatalf("service = nil, want Kubernetes-backed secret service")
	}
	_, err = service.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "idem-secret",
		Name:           "db-password",
		Data:           map[string]string{"password": "secret-value"},
	})
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}
	if !strings.Contains(gotPath, "/api/v1/namespaces/ani-tenant-tenant-a/secrets/sec-") {
		t.Fatalf("path = %q, want tenant Kubernetes Secret path", gotPath)
	}
}

func TestGatewaySecretServiceFromConfigRejectsInvalidProvider(t *testing.T) {
	if _, err := newGatewaySecretService(gatewaySecretRuntimeConfig{ProviderMode: "unknown"}); err == nil {
		t.Fatalf("newGatewaySecretService() error = nil, want unsupported provider error")
	}
}
