package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayEncryptionServiceFromConfigUsesKMSHTTPProvider(t *testing.T) {
	var paths []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/keys"):
			return jsonResponse(http.StatusOK, `{"applied":true,"provider":"kms-sm4","resource_refs":["kms://tenant-a/ekey-real"]}`), nil
		case strings.HasSuffix(r.URL.Path, "/v1/seal"):
			return jsonResponse(http.StatusOK, `{"sealed_object_uri":"kms+sm4://tenant-a/ekey-real/model.bin","unseal_token":"kms-token","expires_at":"1970-01-01T00:33:20Z","provider":"kms-sm4","resource_refs":["kms://tenant-a/ekey-real"]}`), nil
		case strings.HasPrefix(r.URL.Path, "/v1/keys/") && strings.HasSuffix(r.URL.Path, "/delete"):
			return jsonResponse(http.StatusOK, `{"applied":true,"provider":"kms-sm4","resource_refs":["kms://tenant-a/ekey-real"]}`), nil
		default:
			t.Fatalf("unexpected KMS path %q", r.URL.Path)
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})

	service, err := newGatewayEncryptionService(gatewayEncryptionRuntimeConfig{
		ProviderMode:   "kms_sm4_http",
		KMSBaseURL:     "https://kms.example.test",
		KMSBearerToken: "kms-token",
		HTTPClient:     &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayEncryptionService() error = %v", err)
	}
	if service == nil {
		t.Fatalf("service = nil, want KMS-backed encryption service")
	}
	key, err := service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-key",
		Name:           "model-seal",
		Algorithm:      "SM4",
	})
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if !key.RealProvider || key.Provider != "kms-sm4" {
		t.Fatalf("key provider evidence = %+v, want kms-sm4", key)
	}
	sealed, err := service.Seal(context.Background(), ports.EncryptionSealRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "seal-model",
		KeyID:          key.KeyID,
		ObjectURI:      "s3://models/qwen/model.bin",
	})
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if sealed.SealedObjectURI != "kms+sm4://tenant-a/ekey-real/model.bin" || !sealed.RealProvider {
		t.Fatalf("sealed record = %+v, want KMS provider result", sealed)
	}
	deleted, err := service.DeleteKey(context.Background(), ports.EncryptionKeyGetRequest{
		TenantID: "tenant-a",
		KeyID:    key.KeyID,
	})
	if err != nil {
		t.Fatalf("DeleteKey() error = %v", err)
	}
	if deleted.State != "deleted" || !deleted.RealProvider {
		t.Fatalf("deleted record = %+v, want KMS provider result", deleted)
	}
	if len(paths) != 3 || !strings.HasPrefix(paths[0], "https://kms.example.test/v1/keys") || !strings.HasPrefix(paths[1], "https://kms.example.test/v1/seal") || !strings.Contains(paths[2], "/delete") {
		t.Fatalf("paths = %#v, want KMS create, seal, and delete endpoints", paths)
	}
}

func TestGatewayEncryptionServiceFromConfigRejectsInvalidProvider(t *testing.T) {
	if _, err := newGatewayEncryptionService(gatewayEncryptionRuntimeConfig{ProviderMode: "unknown"}); err == nil {
		t.Fatalf("newGatewayEncryptionService() error = nil, want unsupported provider error")
	}
}
