package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayImageImportServiceDefaultsToLocalProvider(t *testing.T) {
	service, err := newGatewayImageImportService(gatewayImageImportRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayImageImportService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want local image import service")
	}
	session, err := service.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	})
	if err != nil {
		t.Fatalf("CreateUpload() error = %v", err)
	}
	if session.UploadURL == "" || session.Token == "" {
		t.Fatalf("session missing upload credentials: %+v", session)
	}
}

func TestGatewayImageImportServiceUsesCDIRESTProvider(t *testing.T) {
	t.Setenv("KUBERNETES_CONFIG_AUTO_LOAD", "false")
	t.Setenv("KUBERNETES_API_HOST", "https://kubernetes.example.test")

	var calls []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/datavolumes/"):
			return jsonResponse(http.StatusNotFound, `{"kind":"Status","code":404}`), nil
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/datavolumes"):
			return jsonResponse(http.StatusCreated, `{"metadata":{"name":"img-fake","namespace":"ani-tenant-tenant-a","creationTimestamp":"2026-07-09T00:00:00Z","annotations":{"ani.io/image-name":"ubuntu-2204"}},"spec":{"storage":{"storageClassName":"ani-rbd-ssd","resources":{"requests":{"storage":"5Gi"}}}},"status":{"phase":"Pending"}}`), nil
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/uploadtokenrequests"):
			return jsonResponse(http.StatusCreated, `{"status":{"token":"tok-real"}}`), nil
		default:
			return jsonResponse(http.StatusOK, `{}`), nil
		}
	})

	service, err := newGatewayImageImportService(gatewayImageImportRuntimeConfig{
		ProviderMode:         "cdi_rest",
		UploadProxyURL:       "https://cdi-uploadproxy.example:31001",
		KubernetesHTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayImageImportService() error = %v", err)
	}

	session, err := service.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	})
	if err != nil {
		t.Fatalf("CreateUpload() error = %v", err)
	}
	if session.UploadURL != "https://cdi-uploadproxy.example:31001/v1beta1/upload" {
		t.Fatalf("upload_url = %s, want CDI uploadproxy base + path", session.UploadURL)
	}
	if session.Token != "tok-real" {
		t.Fatalf("token = %s, want tok-real", session.Token)
	}
	if len(calls) < 3 {
		t.Fatalf("calls = %v, want Get DataVolume + Create DataVolume + Create UploadTokenRequest", calls)
	}
}

func TestGatewayImageImportServiceRejectsCDIRESTWithoutUploadProxyURL(t *testing.T) {
	if _, err := newGatewayImageImportService(gatewayImageImportRuntimeConfig{ProviderMode: "cdi_rest"}); err == nil {
		t.Fatal("newGatewayImageImportService() error = nil, want CDI_UPLOADPROXY_URL required error")
	}
}

func TestGatewayImageImportServiceRejectsInvalidProvider(t *testing.T) {
	if _, err := newGatewayImageImportService(gatewayImageImportRuntimeConfig{ProviderMode: "unknown"}); err == nil {
		t.Fatal("newGatewayImageImportService() error = nil, want unsupported provider error")
	}
}
