package objectstore

import (
	"context"
	"io"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type NotConfigured struct{}

var _ ports.ObjectStore = NotConfigured{}

func (NotConfigured) Health(context.Context) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) EnsureBucket(context.Context, ports.BucketClass) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) BucketUsage(context.Context, ports.BucketClass, string) (ports.BucketUsage, error) {
	return ports.BucketUsage{}, ports.ErrNotConfigured
}

func (NotConfigured) PutObject(context.Context, ports.PutObjectInput) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrNotConfigured
}

func (NotConfigured) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return nil, ports.ObjectMetadata{}, ports.ErrNotConfigured
}

func (NotConfigured) DeleteObject(context.Context, ports.ObjectRef) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrNotConfigured
}

func (NotConfigured) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrNotConfigured
}

func (NotConfigured) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, ports.ErrNotConfigured
}
