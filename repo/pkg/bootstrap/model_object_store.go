package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// NewModelObjectStore adapts the platform ObjectStore port to the compact
// model repository contract used by model-service. The adapter is kept in
// bootstrap wiring so Services internals remain ports/adapters agnostic.
func NewModelObjectStore(store ports.ObjectStore) types.ModelObjectStore {
	return modelObjectStoreAdapter{store: store}
}

type modelObjectStoreAdapter struct{ store ports.ObjectStore }

var errModelObjectStoreUnavailable = errors.New("model object store is unavailable")
var errModelObjectStoreUploadHeadersUnavailable = errors.New("model object store does not support signed upload headers")
var errModelObjectStoreVerifierUnavailable = errors.New("model object store does not support content verification")

func (s modelObjectStoreAdapter) ref(ref types.ModelObjectRef) ports.ObjectRef {
	return ports.ObjectRef{TenantID: ref.TenantID, BucketClass: ports.BucketClass(ref.BucketClass), ObjectKey: ref.ObjectKey, Version: ref.Version}
}

func (s modelObjectStoreAdapter) SignedUploadURL(ctx context.Context, ref types.ModelObjectRef, ttl time.Duration) (types.ModelSignedURL, error) {
	if s.store == nil {
		return types.ModelSignedURL{}, errModelObjectStoreUnavailable
	}
	value, err := s.store.SignedUploadURL(ctx, s.ref(ref), ttl)
	return types.ModelSignedURL{URL: value.URL, ExpiresAt: value.ExpiresAt, Headers: cloneHeaders(value.Headers)}, err
}

func (s modelObjectStoreAdapter) SignedUploadURLWithHeaders(ctx context.Context, ref types.ModelObjectRef, ttl time.Duration, headers map[string]string) (types.ModelSignedURL, error) {
	if s.store == nil {
		return types.ModelSignedURL{}, errModelObjectStoreUnavailable
	}
	signer, ok := s.store.(ports.ObjectStoreUploadHeaders)
	if !ok {
		return types.ModelSignedURL{}, errModelObjectStoreUploadHeadersUnavailable
	}
	value, err := signer.SignedUploadURLWithHeaders(ctx, s.ref(ref), ttl, headers)
	return types.ModelSignedURL{URL: value.URL, ExpiresAt: value.ExpiresAt, Headers: cloneHeaders(value.Headers)}, err
}

func (s modelObjectStoreAdapter) SignedDownloadURL(ctx context.Context, ref types.ModelObjectRef, ttl time.Duration) (types.ModelSignedURL, error) {
	if s.store == nil {
		return types.ModelSignedURL{}, errModelObjectStoreUnavailable
	}
	value, err := s.store.SignedDownloadURL(ctx, s.ref(ref), ttl)
	return types.ModelSignedURL{URL: value.URL, ExpiresAt: value.ExpiresAt}, err
}

func (s modelObjectStoreAdapter) StatObject(ctx context.Context, ref types.ModelObjectRef) (types.ModelObjectMetadata, error) {
	if s.store == nil {
		return types.ModelObjectMetadata{}, errModelObjectStoreUnavailable
	}
	value, err := s.store.StatObject(ctx, s.ref(ref))
	return types.ModelObjectMetadata{SizeBytes: value.SizeBytes, Checksum: value.Checksum}, err
}

func (s modelObjectStoreAdapter) VerifyObject(ctx context.Context, ref types.ModelObjectRef, expectedSize int64, expectedChecksum string) error {
	if s.store == nil {
		return errModelObjectStoreUnavailable
	}
	verifier, ok := s.store.(ports.ObjectStoreContentVerifier)
	if !ok {
		return errModelObjectStoreVerifierUnavailable
	}
	return verifier.VerifyObject(ctx, s.ref(ref), expectedSize, expectedChecksum)
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		cloned[name] = value
	}
	return cloned
}
