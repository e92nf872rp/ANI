package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

const defaultImageImportUploadBaseURL = "http://127.0.0.1:31001"

type LocalImageImportService struct {
	mu            sync.RWMutex
	now           func() time.Time
	uploadBaseURL string
	records       map[string]ports.ImageRecord
	sessions      map[string]ports.ImageUploadSession
	idempotency   map[string]string
}

type ImageImportServiceOption func(*LocalImageImportService)

func WithImageImportUploadBaseURL(baseURL string) ImageImportServiceOption {
	return func(service *LocalImageImportService) {
		if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
			service.uploadBaseURL = strings.TrimRight(trimmed, "/")
		}
	}
}

func WithImageImportClock(now func() time.Time) ImageImportServiceOption {
	return func(service *LocalImageImportService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewLocalImageImportService(options ...ImageImportServiceOption) *LocalImageImportService {
	service := &LocalImageImportService{
		now:           func() time.Time { return time.Now().UTC() },
		uploadBaseURL: defaultImageImportUploadBaseURL,
		records:       map[string]ports.ImageRecord{},
		sessions:      map[string]ports.ImageUploadSession{},
		idempotency:   map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalImageImportService) CreateUpload(ctx context.Context, req ports.ImageUploadCreateRequest) (ports.ImageUploadSession, error) {
	_ = ctx
	if err := validateImageUploadCreateRequest(req); err != nil {
		return ports.ImageUploadSession{}, err
	}
	if req.Format != ports.ImageFormatISO {
		return ports.ImageUploadSession{}, fmt.Errorf("%w: image format %q", ports.ErrUnsupported, req.Format)
	}
	idemKey := imageImportIdempotencyKey(req.TenantID, req.IdempotencyKey)

	s.mu.Lock()
	defer s.mu.Unlock()
	if imageID, ok := s.idempotency[idemKey]; ok {
		if session, exists := s.sessions[imageID]; exists {
			return session, nil
		}
	}

	now := s.now().UTC()
	imageID := "img-" + uuid.NewString()
	record := ports.ImageRecord{
		ID:           imageID,
		TenantID:     req.TenantID,
		Name:         strings.TrimSpace(req.Name),
		Format:       req.Format,
		SizeGiB:      req.SizeGiB,
		ContentType:  strings.TrimSpace(req.ContentType),
		State:        ports.ImageStateUploading,
		StorageClass: strings.TrimSpace(req.StorageClass),
		DevProfile:   imageImportDevProfile(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	session := ports.ImageUploadSession{
		Image:     record,
		UploadURL: s.uploadBaseURL + "/images/" + imageID + "/upload",
		Token:     "imgtok-" + uuid.NewString(),
		ExpiresAt: now.Add(15 * time.Minute),
		Method:    "POST",
	}
	s.records[imageID] = record
	s.sessions[imageID] = session
	s.idempotency[idemKey] = imageID
	return session, nil
}

func (s *LocalImageImportService) Get(ctx context.Context, req ports.ImageGetRequest) (ports.ImageRecord, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[req.ImageID]
	if !ok || record.TenantID != req.TenantID || record.State == ports.ImageStateDeleted {
		return ports.ImageRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalImageImportService) List(ctx context.Context, req ports.ImageListRequest) (ports.ImageListResult, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.ImageRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.TenantID != req.TenantID || record.State == ports.ImageStateDeleted {
			continue
		}
		if req.Format != "" && record.Format != req.Format {
			continue
		}
		if req.State != "" && record.State != req.State {
			continue
		}
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return ports.ImageListResult{Items: items, Total: len(items)}, nil
}

func (s *LocalImageImportService) Delete(ctx context.Context, req ports.ImageDeleteRequest) (ports.ImageRecord, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[req.ImageID]
	if !ok || record.TenantID != req.TenantID || record.State == ports.ImageStateDeleted {
		return ports.ImageRecord{}, ports.ErrNotFound
	}
	record.State = ports.ImageStateDeleted
	record.UpdatedAt = s.now().UTC()
	s.records[req.ImageID] = record
	delete(s.sessions, req.ImageID)
	return record, nil
}

func validateImageUploadCreateRequest(req ports.ImageUploadCreateRequest) error {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("%w: tenant_id/idempotency_key/name required", ports.ErrInvalid)
	}
	if req.Format == "" {
		return fmt.Errorf("%w: format required", ports.ErrInvalid)
	}
	if req.SizeGiB < 1 {
		return fmt.Errorf("%w: size_gib must be >= 1", ports.ErrInvalid)
	}
	return nil
}

func imageImportIdempotencyKey(tenantID, idempotencyKey string) string {
	return strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(idempotencyKey)
}

func imageImportDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-image-import-service",
		RealProvider: false,
		Reason:       "local image import profile; CDI provider is not wired",
	}
}
