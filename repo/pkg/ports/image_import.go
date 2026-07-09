package ports

import (
	"context"
	"time"
)

type ImageFormat string

const (
	ImageFormatISO   ImageFormat = "iso"
	ImageFormatQCOW2 ImageFormat = "qcow2"
	ImageFormatRAW   ImageFormat = "raw"
)

type ImageState string

const (
	ImageStatePending    ImageState = "pending"
	ImageStateUploading  ImageState = "uploading"
	ImageStateProcessing ImageState = "processing"
	ImageStateReady      ImageState = "ready"
	ImageStateFailed     ImageState = "failed"
	ImageStateDeleting   ImageState = "deleting"
	ImageStateDeleted    ImageState = "deleted"
)

type ImageImportService interface {
	CreateUpload(ctx context.Context, req ImageUploadCreateRequest) (ImageUploadSession, error)
	Get(ctx context.Context, req ImageGetRequest) (ImageRecord, error)
	List(ctx context.Context, req ImageListRequest) (ImageListResult, error)
	Delete(ctx context.Context, req ImageDeleteRequest) (ImageRecord, error)
}

type ImageRecord struct {
	ID           string
	TenantID     string
	Name         string
	Format       ImageFormat
	SizeGiB      int64
	ContentType  string
	State        ImageState
	Reason       string
	Message      string
	VolumeID     string
	StorageClass string
	DevProfile   DevProfileInfo
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ImageUploadCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Format         ImageFormat
	SizeGiB        int64
	ContentType    string
	StorageClass   string
}

type ImageUploadSession struct {
	Image     ImageRecord
	UploadURL string
	Token     string
	ExpiresAt time.Time
	Method    string
}

type ImageGetRequest struct {
	TenantID string
	ImageID  string
}

type ImageListRequest struct {
	TenantID string
	Format   ImageFormat
	State    ImageState
	Limit    int
	Cursor   string
}

type ImageListResult struct {
	Items      []ImageRecord
	Total      int
	NextCursor string
}

type ImageDeleteRequest struct {
	TenantID string
	ImageID  string
}
