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
