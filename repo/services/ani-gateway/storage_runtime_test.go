package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayStorageServiceFromConfigDefaultsToRouterLocalService(t *testing.T) {
	service, closeRuntime, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayStorageService() error = %v", err)
	}
	defer closeRuntime()
	if service != nil {
		t.Fatalf("service = %T, want nil so router keeps local default", service)
	}
}

func TestGatewayStorageServiceRequiresDatabaseURLForKubernetesProvider(t *testing.T) {
	_, _, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{
		ProviderMode:         "kubernetes_rest",
		ProviderApply:        true,
		ProviderUserID:       "ani-core-storage-provider",
		ProviderProof:        "rbac-scope:storage.write",
		KubernetesAPIHost:    "https://kubernetes.example.test",
		KubernetesHTTPClient: &http.Client{Transport: &gatewayStorageRoundTripper{}},
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("newGatewayStorageService() error = %v, want DATABASE_URL required", err)
	}
}

func TestGatewayStorageServiceFromConfigUsesKubernetesProviderWithControlPlaneStore(t *testing.T) {
	transport := &gatewayStorageRoundTripper{}
	service, closeRuntime, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{
		ProviderMode:         "kubernetes_rest",
		ProviderApply:        true,
		ProviderUserID:       "ani-core-storage-provider",
		ProviderProof:        "rbac-scope:storage.write",
		KubernetesAPIHost:    "https://kubernetes.example.test",
		KubernetesHTTPClient: &http.Client{Transport: transport},
		MetadataStore:        newStubControlPlaneMetadataStore(),
	})
	if err != nil {
		t.Fatalf("newGatewayStorageService() error = %v", err)
	}
	defer closeRuntime()
	if service == nil {
		t.Fatalf("service = nil, want provider-backed storage service")
	}
	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "storage-volume-a",
		Name:           "data-a",
		SizeGiB:        1,
		StorageClass:   "ani-rbd-ssd",
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	snapshot, err := service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "storage-snapshot-a",
		VolumeID:       volume.VolumeID,
		Name:           "data-a-snap",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot() error = %v", err)
	}
	if snapshot.Status != ports.VolumeSnapshotAvailable {
		t.Fatalf("snapshot status = %s, want available from Kubernetes observe", snapshot.Status)
	}
	if transport.postCalls != 0 || transport.patchCalls != 4 || transport.getCalls != 2 {
		t.Fatalf("transport post=%d patch=%d get=%d, want volume and snapshot dry-run/apply/observe", transport.postCalls, transport.patchCalls, transport.getCalls)
	}
}

func TestGatewayStorageServiceRejectsKubernetesProviderWithoutProof(t *testing.T) {
	if _, _, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{
		ProviderMode:      "kubernetes_rest",
		KubernetesAPIHost: "https://kubernetes.example.test",
		MetadataStore:     newStubControlPlaneMetadataStore(),
	}); err == nil {
		t.Fatalf("newGatewayStorageService() error = nil, want missing proof error")
	}
}

func TestGatewayStorageServiceRequiresDatabaseURLForMinIO(t *testing.T) {
	_, _, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{
		ObjectStoreProvider:        "minio",
		ObjectStoreEndpoint:        "http://minio.internal:9000",
		ObjectStoreAccessKeyID:     "minio",
		ObjectStoreSecretAccessKey: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("newGatewayStorageService() error = %v, want DATABASE_URL required", err)
	}
}

func TestGatewayStorageServiceCanInjectMinIOObjectStore(t *testing.T) {
	objectStoreTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Fatalf("MinIO request missing SigV4 authorization header: %q", r.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})

	service, closeRuntime, err := newGatewayStorageService(context.Background(), gatewayStorageRuntimeConfig{
		ObjectStoreProvider:        "minio",
		ObjectStoreEndpoint:        "http://minio.internal:9000",
		ObjectStorePublicEndpoint:  "http://minio-public.example:30900",
		ObjectStoreAccessKeyID:     "minio",
		ObjectStoreSecretAccessKey: "secret",
		ObjectStoreBucketPrefix:    "ani-s13-",
		ObjectStoreHTTPClient:      &http.Client{Transport: objectStoreTransport},
		MetadataStore:              newStubControlPlaneMetadataStore(),
	})
	if err != nil {
		t.Fatalf("newGatewayStorageService() error = %v", err)
	}
	defer closeRuntime()
	if service == nil {
		t.Fatal("service = nil, want object-store-backed storage service")
	}
	bucket, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "bucket-a",
		Name:           "models",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}
	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "object-a",
		BucketID:       bucket.BucketID,
		Key:            "live.txt",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}
	if !strings.HasPrefix(upload.UploadURL, "http://minio-public.example:30900/ani-s13-models/") {
		t.Fatalf("upload URL = %q, want MinIO public endpoint", upload.UploadURL)
	}
}

func TestGatewayStorageConfigFromEnvIncludesInClusterKubernetesService(t *testing.T) {
	t.Setenv("STORAGE_PROVIDER", "kubernetes_rest")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	t.Setenv("DATABASE_URL", "postgres://ani@ani-postgres:5432/ani")

	cfg := gatewayStorageRuntimeConfigFromEnv()
	if cfg.KubernetesServiceHost != "10.96.0.1" || cfg.KubernetesServicePort != "443" {
		t.Fatalf("service host/port = %q/%q, want in-cluster Kubernetes service", cfg.KubernetesServiceHost, cfg.KubernetesServicePort)
	}
	if cfg.KubernetesServiceAccountTokenFile == "" || cfg.KubernetesServiceAccountCAFile == "" {
		t.Fatalf("service account files not loaded from env: %#v", cfg)
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("DatabaseURL not loaded from env")
	}
}

func TestGatewayStorageConfigFromEnvIncludesObjectStoreProvider(t *testing.T) {
	t.Setenv("OBJECT_STORE_PROVIDER", "minio")
	t.Setenv("OBJECT_STORE_ENDPOINT", "http://minio.example:9000")
	t.Setenv("OBJECT_STORE_ENDPOINTS", "http://minio-a.example:9000,http://minio-b.example:9000")
	t.Setenv("OBJECT_STORE_PUBLIC_ENDPOINT", "http://minio-public.example:30900")
	t.Setenv("OBJECT_STORE_ACCESS_KEY_ID", "minio")
	t.Setenv("OBJECT_STORE_SECRET_ACCESS_KEY", "secret")
	t.Setenv("OBJECT_STORE_REGION", "us-east-1")
	t.Setenv("OBJECT_STORE_SECURE", "false")
	t.Setenv("OBJECT_STORE_BUCKET_PREFIX", "ani-s13-")

	cfg := gatewayStorageRuntimeConfigFromEnv()
	if cfg.ObjectStoreProvider != "minio" || cfg.ObjectStoreEndpoint != "http://minio.example:9000" || cfg.ObjectStorePublicEndpoint != "http://minio-public.example:30900" {
		t.Fatalf("object store provider config not loaded from env: %#v", cfg)
	}
	if len(cfg.ObjectStoreEndpoints) != 2 || cfg.ObjectStoreEndpoints[0] != "http://minio-a.example:9000" || cfg.ObjectStoreEndpoints[1] != "http://minio-b.example:9000" {
		t.Fatalf("object store endpoints = %#v, want parsed endpoint list", cfg.ObjectStoreEndpoints)
	}
	if cfg.ObjectStoreAccessKeyID == "" || cfg.ObjectStoreSecretAccessKey == "" || cfg.ObjectStoreBucketPrefix != "ani-s13-" {
		t.Fatalf("object store credentials/prefix not loaded from env")
	}
}

type gatewayStorageRoundTripper struct {
	postCalls  int
	patchCalls int
	getCalls   int
}

func (t *gatewayStorageRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodPost:
		t.postCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"kind":"Accepted"}`)), Header: http.Header{}}, nil
	case http.MethodPatch:
		t.patchCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"kind":"Applied"}`)), Header: http.Header{}}, nil
	case http.MethodGet:
		t.getCalls++
		if strings.Contains(req.URL.Path, "/volumesnapshots/") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"kind":"VolumeSnapshot","status":{"readyToUse":true}}`)),
				Header:     http.Header{},
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"kind":"PersistentVolumeClaim","status":{"phase":"Bound"}}`)),
			Header:     http.Header{},
		}, nil
	default:
		return &http.Response{StatusCode: http.StatusMethodNotAllowed, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	}
}
