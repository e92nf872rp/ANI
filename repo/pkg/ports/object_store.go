package ports

import (
	"context"
	"io"
	"time"
)

type BucketClass string

const (
	BucketClassModel    BucketClass = "model"
	BucketClassDataset  BucketClass = "dataset"
	BucketClassKBDoc    BucketClass = "kb-docs"
	BucketClassBranding BucketClass = "branding"
)

type ObjectRef struct {
	TenantID    string
	BucketClass BucketClass
	ObjectKey   string
	Version     string
}

type ObjectMetadata struct {
	Ref         ObjectRef
	ContentType string
	SizeBytes   int64
	Checksum    string
	UpdatedAt   time.Time
}

type SignedURL struct {
	URL       string
	ExpiresAt time.Time
	Headers   map[string]string
}

// BucketUsage reflects live usage from the object store authority so bucket
// statistics match what the S3-compatible backend actually holds, instead of
// control-plane records alone.
type BucketUsage struct {
	ObjectCount int64
	SizeBytes   int64
}

type PutObjectInput struct {
	Ref         ObjectRef
	Body        io.Reader
	SizeBytes   int64
	ContentType string
	Checksum    string
}

type ObjectStore interface {
	Health(ctx context.Context) error
	EnsureBucket(ctx context.Context, class BucketClass) error
	BucketUsage(ctx context.Context, class BucketClass, tenantID string) (BucketUsage, error)
	PutObject(ctx context.Context, input PutObjectInput) (ObjectMetadata, error)
	GetObject(ctx context.Context, ref ObjectRef) (io.ReadCloser, ObjectMetadata, error)
	DeleteObject(ctx context.Context, ref ObjectRef) error
	StatObject(ctx context.Context, ref ObjectRef) (ObjectMetadata, error)
	SignedUploadURL(ctx context.Context, ref ObjectRef, ttl time.Duration) (SignedURL, error)
	SignedDownloadURL(ctx context.Context, ref ObjectRef, ttl time.Duration) (SignedURL, error)
}

// ObjectStoreUploadHeaders is an optional capability for presigned PUTs that
// require immutable request metadata to be covered by the SigV4 signature.
// Implementations that do not support it must not be used for checksum-bound
// model uploads.
type ObjectStoreUploadHeaders interface {
	ObjectStore
	SignedUploadURLWithHeaders(ctx context.Context, ref ObjectRef, ttl time.Duration, headers map[string]string) (SignedURL, error)
}

// ObjectStoreContentVerifier is an optional capability for control-plane
// registration of presigned uploads. Implementations must stream the object,
// compute SHA-256 from the bytes returned by the store, and compare both the
// declared size and checksum; metadata/ETag alone is not proof of content.
type ObjectStoreContentVerifier interface {
	ObjectStore
	VerifyObject(ctx context.Context, ref ObjectRef, expectedSize int64, expectedChecksum string) error
}
