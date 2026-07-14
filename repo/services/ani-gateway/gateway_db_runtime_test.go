package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestConnectGatewayMetadataStoreWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	store, closeStore, err := connectGatewayMetadataStore(t.Context())
	if err != nil {
		t.Fatalf("connectGatewayMetadataStore() error = %v", err)
	}
	if store != nil {
		t.Fatal("expected nil metadata store when DATABASE_URL is unset")
	}
	closeStore()
}

func TestConnectGatewayMetadataStoreRejectsInvalidDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", ":// invalid")

	store, closeStore, err := connectGatewayMetadataStore(t.Context())
	if err == nil {
		closeStore()
		t.Fatal("connectGatewayMetadataStore() error = nil, want invalid database URL error")
	}
	if store != nil {
		t.Fatal("expected nil metadata store on connection error")
	}
}

func TestGatewayNetworkServiceUsesMetadataStoreInLocalProfile(t *testing.T) {
	store := &gatewayK8sProxyMetadataStore{}
	service, err := newGatewayNetworkService(gatewayNetworkRuntimeConfig{}, store)
	if err != nil {
		t.Fatalf("newGatewayNetworkService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want metadata-backed local network service")
	}
}

func TestGatewayStorageServiceUsesMetadataStoreInLocalProfile(t *testing.T) {
	store := &gatewayK8sProxyMetadataStore{}
	service, err := newGatewayStorageService(gatewayStorageRuntimeConfig{}, store)
	if err != nil {
		t.Fatalf("newGatewayStorageService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want metadata-backed local storage service")
	}
}

func TestGatewayK8sClusterServiceUsesMetadataStoreInLocalProfile(t *testing.T) {
	store := &gatewayK8sProxyMetadataStore{}
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{
		MetadataStore: store,
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want metadata-backed local k8s cluster service")
	}
}

func TestGatewaySecretServiceUsesMetadataStoreInLocalProfile(t *testing.T) {
	store := &gatewayK8sProxyMetadataStore{}
	service, err := newGatewaySecretService(gatewaySecretRuntimeConfig{}, store)
	if err != nil {
		t.Fatalf("newGatewaySecretService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want metadata-backed local secret service")
	}
}

func TestGatewayEncryptionServiceUsesMetadataStoreInLocalProfile(t *testing.T) {
	store := &gatewayK8sProxyMetadataStore{}
	service, err := newGatewayEncryptionService(gatewayEncryptionRuntimeConfig{}, store)
	if err != nil {
		t.Fatalf("newGatewayEncryptionService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want metadata-backed local encryption service")
	}
}

func TestGatewayBrandingServiceWrapsObjectStoreWhenConfigured(t *testing.T) {
	objectStore := &fakeBrandingObjectStoreGateway{publicURL: "https://cdn.example.test/logo.png"}
	service := newGatewayBrandingService(nil, objectStore)
	if service == nil {
		t.Fatal("service = nil, want branding service")
	}
	record, err := service.UploadBrandingLogo(t.Context(), ports.BrandingLogoUploadRequest{
		IdempotencyKey: "branding-logo-gateway",
		Variant:        "favicon",
		ContentType:    "image/png",
		Body:           strings.NewReader("png"),
	})
	if err != nil {
		t.Fatalf("UploadBrandingLogo() error = %v", err)
	}
	if record.FaviconURL != objectStore.publicURL {
		t.Fatalf("favicon_url = %q, want %q", record.FaviconURL, objectStore.publicURL)
	}
}

type fakeBrandingObjectStoreGateway struct {
	publicURL string
}

func (s *fakeBrandingObjectStoreGateway) EnsureBucket(context.Context, ports.BucketClass) error {
	return nil
}
func (s *fakeBrandingObjectStoreGateway) Health(context.Context) error { return nil }
func (s *fakeBrandingObjectStoreGateway) PutObject(context.Context, ports.PutObjectInput) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, nil
}
func (s *fakeBrandingObjectStoreGateway) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return nil, ports.ObjectMetadata{}, ports.ErrUnsupported
}
func (s *fakeBrandingObjectStoreGateway) DeleteObject(context.Context, ports.ObjectRef) error {
	return ports.ErrUnsupported
}
func (s *fakeBrandingObjectStoreGateway) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrUnsupported
}
func (s *fakeBrandingObjectStoreGateway) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrUnsupported
}
func (s *fakeBrandingObjectStoreGateway) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrUnsupported
}
func (s *fakeBrandingObjectStoreGateway) PublicObjectURL(context.Context, ports.ObjectRef) (string, error) {
	return s.publicURL, nil
}
