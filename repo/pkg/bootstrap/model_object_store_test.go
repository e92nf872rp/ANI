package bootstrap

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

func TestNewModelObjectStoreFailsClosedWhenProviderIsNil(t *testing.T) {
	store := NewModelObjectStore(nil)
	ref := types.ModelObjectRef{TenantID: "tenant", ModelID: "model", Version: "v1", ObjectKey: "model/v1/file"}
	if _, err := store.SignedUploadURL(context.Background(), ref, time.Minute); err == nil {
		t.Fatal("SignedUploadURL error = nil, want unavailable")
	}
	if _, err := store.SignedDownloadURL(context.Background(), ref, time.Minute); err == nil {
		t.Fatal("SignedDownloadURL error = nil, want unavailable")
	}
	if _, err := store.StatObject(context.Background(), ref); err == nil {
		t.Fatal("StatObject error = nil, want unavailable")
	}
}

func TestNewModelObjectStoreTransparentlyForwardsSignedHeaders(t *testing.T) {
	provider := &headerSigningObjectStore{}
	store := NewModelObjectStore(provider)
	ref := types.ModelObjectRef{TenantID: "tenant", ModelID: "model", Version: "v1", BucketClass: "model", ObjectKey: "model/v1/file"}
	got, err := store.(types.ModelObjectStoreUploadHeaders).SignedUploadURLWithHeaders(context.Background(), ref, time.Minute, map[string]string{"x-amz-meta-sha256": "sha256:abc"})
	if err != nil {
		t.Fatalf("SignedUploadURLWithHeaders() error = %v", err)
	}
	if got.URL != "https://object.invalid/signed" || got.Headers["x-amz-meta-sha256"] != "sha256:abc" {
		t.Fatalf("signed URL = %+v", got)
	}
	if provider.lastRef.TenantID != "tenant" || provider.lastRef.BucketClass != ports.BucketClassModel {
		t.Fatalf("ref = %+v", provider.lastRef)
	}
}

func TestNewModelObjectStoreForwardsContentVerification(t *testing.T) {
	provider := &headerSigningObjectStore{}
	store := NewModelObjectStore(provider)
	verifier, ok := store.(types.ModelObjectStoreContentVerifier)
	if !ok {
		t.Fatal("model object store adapter lacks content verifier capability")
	}
	ref := types.ModelObjectRef{TenantID: "tenant", ModelID: "model", Version: "v1", BucketClass: "model", ObjectKey: "model/v1/file"}
	if err := verifier.VerifyObject(context.Background(), ref, 7, "sha256:"+strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("VerifyObject() error = %v", err)
	}
	if provider.verifyCalls != 1 || provider.verifyRef.TenantID != "tenant" || provider.verifySize != 7 {
		t.Fatalf("verification call = %d ref=%+v size=%d", provider.verifyCalls, provider.verifyRef, provider.verifySize)
	}
}

type headerSigningObjectStore struct {
	lastRef     ports.ObjectRef
	verifyRef   ports.ObjectRef
	verifySize  int64
	verifyCalls int
}

func (s *headerSigningObjectStore) Health(context.Context) error                          { return nil }
func (s *headerSigningObjectStore) EnsureBucket(context.Context, ports.BucketClass) error { return nil }
func (s *headerSigningObjectStore) BucketUsage(context.Context, ports.BucketClass, string) (ports.BucketUsage, error) {
	return ports.BucketUsage{}, nil
}
func (s *headerSigningObjectStore) PutObject(context.Context, ports.PutObjectInput) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, nil
}
func (s *headerSigningObjectStore) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return io.NopCloser(strings.NewReader("")), ports.ObjectMetadata{}, nil
}
func (s *headerSigningObjectStore) DeleteObject(context.Context, ports.ObjectRef) error { return nil }
func (s *headerSigningObjectStore) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, nil
}
func (s *headerSigningObjectStore) VerifyObject(_ context.Context, ref ports.ObjectRef, size int64, _ string) error {
	s.verifyRef = ref
	s.verifySize = size
	s.verifyCalls++
	return nil
}
func (s *headerSigningObjectStore) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, nil
}
func (s *headerSigningObjectStore) SignedUploadURLWithHeaders(_ context.Context, ref ports.ObjectRef, _ time.Duration, headers map[string]string) (ports.SignedURL, error) {
	s.lastRef = ref
	return ports.SignedURL{URL: "https://object.invalid/signed", Headers: headers}, nil
}
func (s *headerSigningObjectStore) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, nil
}
