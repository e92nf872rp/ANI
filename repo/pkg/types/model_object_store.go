package types

import (
	"context"
	"time"
)

// ModelObjectRef is the small object-store contract needed by model-service.
// It lives in the shared types package so bootstrap can adapt the platform
// object-store port without making Services internal code depend on adapters.
type ModelObjectRef struct {
	TenantID    string
	ModelID     string
	BucketClass string
	ObjectKey   string
	Version     string
}

type ModelObjectMetadata struct {
	SizeBytes int64
	Checksum  string
}

type ModelSignedURL struct {
	URL       string
	ExpiresAt time.Time
	// Headers are the non-secret request headers covered by the presigned
	// signature. Implementations must return a defensive copy.
	Headers map[string]string
}

type ModelObjectStore interface {
	SignedUploadURL(context.Context, ModelObjectRef, time.Duration) (ModelSignedURL, error)
	SignedDownloadURL(context.Context, ModelObjectRef, time.Duration) (ModelSignedURL, error)
	StatObject(context.Context, ModelObjectRef) (ModelObjectMetadata, error)
}

// ModelObjectStoreUploadHeaders is an optional capability for upload flows
// that need request headers covered by the presigned signature. Callers must
// fail closed when this capability is unavailable instead of issuing an
// unsigned metadata header.
type ModelObjectStoreUploadHeaders interface {
	ModelObjectStore
	SignedUploadURLWithHeaders(context.Context, ModelObjectRef, time.Duration, map[string]string) (ModelSignedURL, error)
}

// ModelObjectStoreContentVerifier is an optional capability used when a
// presigned upload is registered as a model version. The provider must hash
// the bytes it reads from object storage; metadata/ETag alone is not proof.
type ModelObjectStoreContentVerifier interface {
	ModelObjectStore
	VerifyObject(ctx context.Context, ref ModelObjectRef, expectedSize int64, expectedChecksum string) error
}
