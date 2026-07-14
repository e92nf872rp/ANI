package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type MetadataBrandingService struct {
	store ports.MetadataStore
	now   func() time.Time
}

func NewMetadataBrandingService(store ports.MetadataStore) *MetadataBrandingService {
	return &MetadataBrandingService{
		store: store,
		now:   time.Now,
	}
}

func (s *MetadataBrandingService) GetBranding(ctx context.Context) (ports.BrandingRecord, error) {
	if s.store == nil {
		return ports.BrandingRecord{}, ports.ErrNotConfigured
	}
	var record ports.BrandingRecord
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT platform_name,
				COALESCE(logo_light_url, ''),
				COALESCE(logo_dark_url, ''),
				COALESCE(favicon_url, ''),
				primary_color,
				secondary_color,
				COALESCE(login_bg_url, ''),
				COALESCE(icp_number, ''),
				updated_at
			FROM platform_branding
			ORDER BY updated_at DESC
			LIMIT 1
		`)
		var loginBg, icp sql.NullString
		if err := row.Scan(
			&record.PlatformName,
			&record.LogoLightURL,
			&record.LogoDarkURL,
			&record.FaviconURL,
			&record.PrimaryColor,
			&record.SecondaryColor,
			&loginBg,
			&icp,
			&record.UpdatedAt,
		); err != nil {
			return err
		}
		record.LoginBgURL = loginBg.String
		record.ICPNumber = icp.String
		return nil
	})
	if err != nil {
		return ports.BrandingRecord{}, fmt.Errorf("get platform branding: %w", err)
	}
	return record, nil
}

func (s *MetadataBrandingService) UpdateBranding(ctx context.Context, request ports.BrandingUpdateRequest) (ports.BrandingRecord, error) {
	if s.store == nil {
		return ports.BrandingRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.BrandingRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	current, err := s.GetBranding(ctx)
	if err != nil {
		return ports.BrandingRecord{}, err
	}
	record := mergeBrandingUpdate(current, request)
	record.UpdatedAt = s.now().UTC()
	err = s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE platform_branding SET
				platform_name = $1,
				logo_light_url = NULLIF($2, ''),
				logo_dark_url = NULLIF($3, ''),
				favicon_url = NULLIF($4, ''),
				primary_color = $5,
				secondary_color = $6,
				login_bg_url = NULLIF($7, ''),
				icp_number = NULLIF($8, ''),
				updated_at = $9
			WHERE id = (
				SELECT id FROM platform_branding ORDER BY updated_at DESC LIMIT 1
			)
		`, record.PlatformName, record.LogoLightURL, record.LogoDarkURL, record.FaviconURL,
			record.PrimaryColor, record.SecondaryColor, record.LoginBgURL, record.ICPNumber, record.UpdatedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected > 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO platform_branding (
				platform_name, logo_light_url, logo_dark_url, favicon_url,
				primary_color, secondary_color, login_bg_url, icp_number, updated_at
			)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9)
		`, record.PlatformName, record.LogoLightURL, record.LogoDarkURL, record.FaviconURL,
			record.PrimaryColor, record.SecondaryColor, record.LoginBgURL, record.ICPNumber, record.UpdatedAt)
		return err
	})
	if err != nil {
		return ports.BrandingRecord{}, fmt.Errorf("update platform branding: %w", err)
	}
	return record, nil
}

func (s *MetadataBrandingService) UploadBrandingLogo(context.Context, ports.BrandingLogoUploadRequest) (ports.BrandingRecord, error) {
	return ports.BrandingRecord{}, ports.ErrNotConfigured
}

var _ ports.BrandingService = (*MetadataBrandingService)(nil)

func mergeBrandingUpdate(current ports.BrandingRecord, request ports.BrandingUpdateRequest) ports.BrandingRecord {
	if name := strings.TrimSpace(request.PlatformName); name != "" {
		current.PlatformName = name
	}
	if request.LogoLightURL != "" {
		current.LogoLightURL = strings.TrimSpace(request.LogoLightURL)
	}
	if request.LogoDarkURL != "" {
		current.LogoDarkURL = strings.TrimSpace(request.LogoDarkURL)
	}
	if request.FaviconURL != "" {
		current.FaviconURL = strings.TrimSpace(request.FaviconURL)
	}
	if color := strings.TrimSpace(request.PrimaryColor); color != "" {
		current.PrimaryColor = color
	}
	if color := strings.TrimSpace(request.SecondaryColor); color != "" {
		current.SecondaryColor = color
	}
	if request.LoginBgURL != "" {
		current.LoginBgURL = strings.TrimSpace(request.LoginBgURL)
	}
	if request.ICPNumber != "" {
		current.ICPNumber = strings.TrimSpace(request.ICPNumber)
	}
	return current
}

func defaultBrandingRecord() ports.BrandingRecord {
	return ports.BrandingRecord{
		PlatformName:   "KuberCloud ANI",
		PrimaryColor:   "#1677FF",
		SecondaryColor: "#13C2C2",
		UpdatedAt:      time.Now().UTC(),
	}
}

type LocalBrandingService struct {
	mu      sync.RWMutex
	current *ports.BrandingRecord
	idem    map[string]ports.BrandingRecord
}

func NewLocalBrandingService() *LocalBrandingService {
	return &LocalBrandingService{idem: map[string]ports.BrandingRecord{}}
}

func (s *LocalBrandingService) GetBranding(_ context.Context) (ports.BrandingRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current != nil {
		return *s.current, nil
	}
	return defaultBrandingRecord(), nil
}

func (s *LocalBrandingService) UpdateBranding(_ context.Context, request ports.BrandingUpdateRequest) (ports.BrandingRecord, error) {
	idemKey := strings.TrimSpace(request.IdempotencyKey)
	if idemKey == "" {
		return ports.BrandingRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	s.mu.RLock()
	if record, ok := s.idem[idemKey]; ok {
		s.mu.RUnlock()
		return record, nil
	}
	s.mu.RUnlock()
	current, err := s.GetBranding(context.Background())
	if err != nil {
		return ports.BrandingRecord{}, err
	}
	record := mergeBrandingUpdate(current, request)
	record.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := record
	s.current = &copy
	s.idem[idemKey] = copy
	return record, nil
}

func (s *LocalBrandingService) UploadBrandingLogo(_ context.Context, request ports.BrandingLogoUploadRequest) (ports.BrandingRecord, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.BrandingRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	return ports.BrandingRecord{}, ports.ErrNotConfigured
}

var _ ports.BrandingService = (*LocalBrandingService)(nil)
