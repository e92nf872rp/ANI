package runtime

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	defaultBrandingPlatformTenantID = "platform"
	brandingLogoSignedURLTTL        = 7 * 24 * time.Hour
)

type objectStorePublicURL interface {
	PublicObjectURL(ctx context.Context, ref ports.ObjectRef) (string, error)
}

type ObjectStoreBrandingService struct {
	base           ports.BrandingService
	objectStore    ports.ObjectStore
	platformTenant string
}

type ObjectStoreBrandingOption func(*ObjectStoreBrandingService)

func WithBrandingPlatformTenantID(tenantID string) ObjectStoreBrandingOption {
	return func(service *ObjectStoreBrandingService) {
		if strings.TrimSpace(tenantID) != "" {
			service.platformTenant = strings.TrimSpace(tenantID)
		}
	}
}

func NewObjectStoreBrandingService(base ports.BrandingService, objectStore ports.ObjectStore, options ...ObjectStoreBrandingOption) *ObjectStoreBrandingService {
	service := &ObjectStoreBrandingService{
		base:           base,
		objectStore:    objectStore,
		platformTenant: defaultBrandingPlatformTenantID,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *ObjectStoreBrandingService) GetBranding(ctx context.Context) (ports.BrandingRecord, error) {
	return s.base.GetBranding(ctx)
}

func (s *ObjectStoreBrandingService) UpdateBranding(ctx context.Context, request ports.BrandingUpdateRequest) (ports.BrandingRecord, error) {
	return s.base.UpdateBranding(ctx, request)
}

func (s *ObjectStoreBrandingService) UploadBrandingLogo(ctx context.Context, request ports.BrandingLogoUploadRequest) (ports.BrandingRecord, error) {
	if s.objectStore == nil {
		return ports.BrandingRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.BrandingRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if request.Body == nil {
		return ports.BrandingRecord{}, fmt.Errorf("%w: logo file body is required", ports.ErrInvalid)
	}
	variant, err := normalizeBrandingLogoVariant(request.Variant)
	if err != nil {
		return ports.BrandingRecord{}, err
	}
	contentType := strings.TrimSpace(request.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectKey := brandingLogoObjectKey(variant, contentType)
	ref := ports.ObjectRef{
		TenantID:    s.platformTenant,
		BucketClass: ports.BucketClassBranding,
		ObjectKey:   objectKey,
	}
	if err := s.objectStore.EnsureBucket(ctx, ports.BucketClassBranding); err != nil {
		return ports.BrandingRecord{}, err
	}
	if _, err := s.objectStore.PutObject(ctx, ports.PutObjectInput{
		Ref:         ref,
		Body:        request.Body,
		SizeBytes:   request.SizeBytes,
		ContentType: contentType,
	}); err != nil {
		return ports.BrandingRecord{}, err
	}
	publicURL, err := s.publicObjectURL(ctx, ref)
	if err != nil {
		return ports.BrandingRecord{}, err
	}
	update := ports.BrandingUpdateRequest{IdempotencyKey: request.IdempotencyKey}
	switch variant {
	case "light":
		update.LogoLightURL = publicURL
	case "dark":
		update.LogoDarkURL = publicURL
	case "favicon":
		update.FaviconURL = publicURL
	}
	return s.base.UpdateBranding(ctx, update)
}

func (s *ObjectStoreBrandingService) publicObjectURL(ctx context.Context, ref ports.ObjectRef) (string, error) {
	if resolver, ok := s.objectStore.(objectStorePublicURL); ok {
		return resolver.PublicObjectURL(ctx, ref)
	}
	signed, err := s.objectStore.SignedDownloadURL(ctx, ref, brandingLogoSignedURLTTL)
	if err != nil {
		return "", err
	}
	return signed.URL, nil
}

func normalizeBrandingLogoVariant(variant string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "", "light":
		return "light", nil
	case "dark":
		return "dark", nil
	case "favicon":
		return "favicon", nil
	default:
		return "", fmt.Errorf("%w: branding logo variant must be light, dark or favicon", ports.ErrInvalid)
	}
}

func brandingLogoObjectKey(variant string, contentType string) string {
	ext := brandingLogoExtension(contentType)
	return path.Join("logos", variant+"-"+uuid.NewString()+ext)
}

func brandingLogoExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/svg+xml":
		return ".svg"
	case "image/webp":
		return ".webp"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	default:
		return ".bin"
	}
}

var _ ports.BrandingService = (*ObjectStoreBrandingService)(nil)
