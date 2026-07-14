package runtime

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestObjectStoreBrandingServiceUploadsLightLogo(t *testing.T) {
	base := &fakeBrandingService{}
	objectStore := &fakeBrandingObjectStore{
		publicURL: "https://cdn.example.test/ani-branding/logos/light-test.png",
	}
	service := NewObjectStoreBrandingService(base, objectStore)
	record, err := service.UploadBrandingLogo(context.Background(), ports.BrandingLogoUploadRequest{
		IdempotencyKey: "branding-logo-light",
		Variant:        "light",
		ContentType:    "image/png",
		SizeBytes:      4,
		Body:           bytes.NewReader([]byte{0x89, 0x50, 0x4e, 0x47}),
	})
	if err != nil {
		t.Fatalf("UploadBrandingLogo() error = %v", err)
	}
	if record.LogoLightURL != objectStore.publicURL {
		t.Fatalf("logo_light_url = %q, want %q", record.LogoLightURL, objectStore.publicURL)
	}
	if objectStore.ensureBucket != ports.BucketClassBranding {
		t.Fatalf("ensureBucket = %q, want branding bucket class", objectStore.ensureBucket)
	}
	if !strings.HasPrefix(objectStore.putRef.ObjectKey, "logos/light-") {
		t.Fatalf("object key = %q, want logos/light- prefix", objectStore.putRef.ObjectKey)
	}
	if objectStore.putRef.TenantID != "platform" {
		t.Fatalf("tenant = %q, want platform", objectStore.putRef.TenantID)
	}
	if base.lastUpdate.IdempotencyKey != "branding-logo-light" {
		t.Fatalf("branding update idempotency_key = %q, want branding-logo-light", base.lastUpdate.IdempotencyKey)
	}
}

func TestObjectStoreBrandingServiceRejectsMissingObjectStore(t *testing.T) {
	service := NewObjectStoreBrandingService(NewLocalBrandingService(), nil)
	_, err := service.UploadBrandingLogo(context.Background(), ports.BrandingLogoUploadRequest{
		Variant: "light",
		Body:    bytes.NewReader([]byte("logo")),
	})
	if err != ports.ErrNotConfigured {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

type fakeBrandingService struct {
	lastUpdate ports.BrandingUpdateRequest
}

func (s *fakeBrandingService) GetBranding(context.Context) (ports.BrandingRecord, error) {
	return defaultBrandingRecord(), nil
}

func (s *fakeBrandingService) UpdateBranding(_ context.Context, request ports.BrandingUpdateRequest) (ports.BrandingRecord, error) {
	s.lastUpdate = request
	record := defaultBrandingRecord()
	record.LogoLightURL = request.LogoLightURL
	record.LogoDarkURL = request.LogoDarkURL
	record.FaviconURL = request.FaviconURL
	return record, nil
}

func (s *fakeBrandingService) UploadBrandingLogo(context.Context, ports.BrandingLogoUploadRequest) (ports.BrandingRecord, error) {
	return ports.BrandingRecord{}, ports.ErrNotConfigured
}

type fakeBrandingObjectStore struct {
	ensureBucket ports.BucketClass
	putRef       ports.ObjectRef
	publicURL    string
}

func (s *fakeBrandingObjectStore) EnsureBucket(_ context.Context, class ports.BucketClass) error {
	s.ensureBucket = class
	return nil
}

func (s *fakeBrandingObjectStore) Health(context.Context) error {
	return nil
}

func (s *fakeBrandingObjectStore) PutObject(_ context.Context, input ports.PutObjectInput) (ports.ObjectMetadata, error) {
	s.putRef = input.Ref
	return ports.ObjectMetadata{}, nil
}

func (s *fakeBrandingObjectStore) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return nil, ports.ObjectMetadata{}, ports.ErrUnsupported
}

func (s *fakeBrandingObjectStore) DeleteObject(context.Context, ports.ObjectRef) error {
	return ports.ErrUnsupported
}

func (s *fakeBrandingObjectStore) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrUnsupported
}

func (s *fakeBrandingObjectStore) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrUnsupported
}

func (s *fakeBrandingObjectStore) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrUnsupported
}

func (s *fakeBrandingObjectStore) PublicObjectURL(context.Context, ports.ObjectRef) (string, error) {
	return s.publicURL, nil
}

var _ ports.ObjectStore = (*fakeBrandingObjectStore)(nil)
