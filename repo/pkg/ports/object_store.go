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

// BucketACLPolicy is the normalized bucket access policy applied to the object
// store authority. It is intentionally a separate vocabulary from the console
// api's access_mode/acl fields so a single canonical value reaches the store.
type BucketACLPolicy string

const (
	// BucketACLPolicyPrivate keeps the bucket readable by credentialed callers only.
	BucketACLPolicyPrivate BucketACLPolicy = "private"
	// BucketACLPolicyTenantRead allows anonymous reads of the tenant prefix only.
	BucketACLPolicyTenantRead BucketACLPolicy = "tenant_read"
)

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

// ObjectStorePolicyApplier is an optional capability for applying bucket-level
// access policy to the object store authority. Implementations that do not
// support it must not be treated as having applied the console ACL: the
// control-plane record alone is not proof that the real bucket changed.
type ObjectStorePolicyApplier interface {
	ObjectStore
	ApplyBucketPolicy(ctx context.Context, class BucketClass, tenantID string, policy BucketACLPolicy) error
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

type MultipartPart struct {
	Number int    `json:"number"`
	ETag   string `json:"etag"`
	Size   int64  `json:"size"`
}

// MultipartObjectStore is an optional capability for streaming large objects
// in independently retryable parts.
type MultipartObjectStore interface {
	ObjectStore
	BeginMultipart(ctx context.Context, ref ObjectRef, contentType string) (string, error)
	ListParts(ctx context.Context, ref ObjectRef, uploadID string) ([]MultipartPart, error)
	UploadPart(ctx context.Context, ref ObjectRef, uploadID string, partNumber int, body io.Reader, size int64) (MultipartPart, error)
	CompleteMultipart(ctx context.Context, ref ObjectRef, uploadID string, parts []MultipartPart) (ObjectMetadata, error)
	AbortMultipart(ctx context.Context, ref ObjectRef, uploadID string) error
}
