package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalMeteringService struct {
	mu          sync.RWMutex
	now         func() time.Time
	reports     map[string]ports.TokenUsageReportRecord
	idempotency map[string]string
}

type MeteringOption func(*LocalMeteringService)

func WithMeteringClock(now func() time.Time) MeteringOption {
	return func(service *LocalMeteringService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewLocalMeteringService(options ...MeteringOption) *LocalMeteringService {
	service := &LocalMeteringService{
		now:         func() time.Time { return time.Now().UTC() },
		reports:     map[string]ports.TokenUsageReportRecord{},
		idempotency: map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalMeteringService) QueryUsage(_ context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.MeteringUsageResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var input, output, total int64
	for _, report := range s.reports {
		if report.TenantID != request.TenantID {
			continue
		}
		input += report.InputTokens
		output += report.OutputTokens
		total += report.TotalTokens
	}
	items := []ports.MeteringUsageRecord{}
	appendItem := func(resource ports.MeteringResourceType, quantity int64) {
		if quantity == 0 {
			return
		}
		if request.ResourceType != "" && request.ResourceType != resource {
			return
		}
		items = append(items, ports.MeteringUsageRecord{
			TenantID:      request.TenantID,
			ResourceType:  resource,
			TotalQuantity: float64(quantity),
			Unit:          "token",
		})
	}
	appendItem(ports.MeteringResourceTokenInput, input)
	appendItem(ports.MeteringResourceTokenOutput, output)
	appendItem(ports.MeteringResourceTokenTotal, total)
	return ports.MeteringUsageResult{Items: items, DevProfile: meteringDevProfile()}, nil
}

// QueryPlatformUsage 平台查询在 local/dev 模式下返回空 items，
// 不校验 TenantID（平台 token 可能无租户归属）。
func (s *LocalMeteringService) QueryPlatformUsage(_ context.Context, _ ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	return ports.MeteringUsageResult{Items: []ports.MeteringUsageRecord{}, DevProfile: meteringDevProfile()}, nil
}

func (s *LocalMeteringService) ReportTokenUsage(_ context.Context, request ports.TokenUsageReportRequest) (ports.TokenUsageReportRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.TokenUsageReportRecord{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Source) == "" {
		return ports.TokenUsageReportRecord{}, fmt.Errorf("%w: source is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Model) == "" {
		return ports.TokenUsageReportRecord{}, fmt.Errorf("%w: model is required", ports.ErrInvalid)
	}
	if request.InputTokens < 0 || request.OutputTokens < 0 {
		return ports.TokenUsageReportRecord{}, fmt.Errorf("%w: token counts must be non-negative", ports.ErrInvalid)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.TokenUsageReportRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.idempotency[idemKey]; ok {
		record := s.reports[id]
		record.State = ports.TokenUsageReportDuplicate
		record.DevProfile = meteringDevProfile()
		return record, nil
	}

	now := firstNonZeroTime(request.OccurredAt, s.now().UTC())
	record := ports.TokenUsageReportRecord{
		TenantID:     request.TenantID,
		ReportID:     "meter_" + uuid.NewString(),
		Source:       strings.TrimSpace(request.Source),
		Model:        strings.TrimSpace(request.Model),
		InputTokens:  request.InputTokens,
		OutputTokens: request.OutputTokens,
		TotalTokens:  request.InputTokens + request.OutputTokens,
		RequestID:    strings.TrimSpace(request.RequestID),
		InstanceID:   strings.TrimSpace(request.InstanceID),
		State:        ports.TokenUsageReportAccepted,
		DevProfile:   meteringDevProfile(),
		CreatedAt:    now,
	}
	s.reports[record.ReportID] = record
	s.idempotency[idemKey] = record.ReportID
	return record, nil
}

func meteringDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-metering-service",
		RealProvider: false,
		Reason:       "local profile records metering events; it is not a real metering backend execution",
	}
}

var _ ports.MeteringService = (*LocalMeteringService)(nil)
