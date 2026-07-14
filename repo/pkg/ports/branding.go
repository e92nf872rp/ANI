package ports

import (
	"context"
	"io"
	"time"
)

type BrandingRecord struct {
	PlatformName   string
	LogoLightURL   string
	LogoDarkURL    string
	FaviconURL     string
	PrimaryColor   string
	SecondaryColor string
	LoginBgURL     string
	ICPNumber      string
	UpdatedAt      time.Time
}

type BrandingUpdateRequest struct {
	IdempotencyKey string
	PlatformName   string
	LogoLightURL   string
	LogoDarkURL    string
	FaviconURL     string
	PrimaryColor   string
	SecondaryColor string
	LoginBgURL     string
	ICPNumber      string
}

type BrandingLogoUploadRequest struct {
	IdempotencyKey string
	Variant        string
	ContentType    string
	Body           io.Reader
	SizeBytes      int64
}

type BrandingService interface {
	GetBranding(ctx context.Context) (BrandingRecord, error)
	UpdateBranding(ctx context.Context, request BrandingUpdateRequest) (BrandingRecord, error)
	UploadBrandingLogo(ctx context.Context, request BrandingLogoUploadRequest) (BrandingRecord, error)
}
