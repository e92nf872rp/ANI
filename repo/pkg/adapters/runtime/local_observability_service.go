package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalObservabilityService struct {
	mu          sync.RWMutex
	now         func() time.Time
	rules       map[string]ports.ObservabilityAlertRuleRecord
	idempotency map[string]string
}

type ObservabilityOption func(*LocalObservabilityService)

func WithObservabilityClock(now func() time.Time) ObservabilityOption {
	return func(service *LocalObservabilityService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewLocalObservabilityService(options ...ObservabilityOption) *LocalObservabilityService {
	service := &LocalObservabilityService{
		now:         func() time.Time { return time.Now().UTC() },
		rules:       map[string]ports.ObservabilityAlertRuleRecord{},
		idempotency: map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalObservabilityService) Query(_ context.Context, request ports.ObservabilityQueryRequest) (ports.ObservabilityQueryResult, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return ports.ObservabilityQueryResult{}, fmt.Errorf("%w: observability query is required", ports.ErrInvalid)
	}
	return ports.ObservabilityQueryResult{
		Query:      query,
		ResultType: ports.ObservabilityResultVector,
		Results:    []ports.ObservabilityQuerySample{},
		DevProfile: observabilityDevProfile(),
	}, nil
}

func (s *LocalObservabilityService) CreateAlertRule(_ context.Context, request ports.ObservabilityAlertRuleCreateRequest) (ports.ObservabilityAlertRuleRecord, error) {
	if err := requireObservabilityTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.ObservabilityAlertRuleRecord{}, err
	}
	if strings.TrimSpace(request.PromQL) == "" {
		return ports.ObservabilityAlertRuleRecord{}, fmt.Errorf("%w: promql is required", ports.ErrInvalid)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.ObservabilityAlertRuleRecord{}, err
	}
	severity, err := normalizeObservabilitySeverity(request.Severity)
	if err != nil {
		return ports.ObservabilityAlertRuleRecord{}, err
	}

	s.mu.Lock()
	if id, ok := s.idempotency[idemKey]; ok {
		if record, exists := s.rules[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	s.mu.Unlock()

	now := s.now().UTC()
	record := ports.ObservabilityAlertRuleRecord{
		TenantID:    request.TenantID,
		RuleID:      "alert_" + uuid.NewString(),
		Name:        strings.TrimSpace(request.Name),
		PromQL:      strings.TrimSpace(request.PromQL),
		Duration:    firstNonZeroDuration(request.Duration, 5*time.Minute),
		Severity:    severity,
		Labels:      cloneStringMap(request.Labels),
		Annotations: cloneStringMap(request.Annotations),
		Enabled:     request.Enabled,
		State:       alertStateFromEnabled(request.Enabled),
		DevProfile:  observabilityDevProfile(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[record.RuleID] = record
	s.idempotency[idemKey] = record.RuleID
	return record, nil
}

func (s *LocalObservabilityService) ListAlertRules(_ context.Context, request ports.ObservabilityAlertRuleListRequest) ([]ports.ObservabilityAlertRuleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.ObservabilityAlertRuleRecord, 0, len(s.rules))
	for _, record := range s.rules {
		if record.TenantID == request.TenantID && record.State != ports.ObservabilityAlertRuleDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalObservabilityService) GetAlertRule(_ context.Context, request ports.ObservabilityAlertRuleGetRequest) (ports.ObservabilityAlertRuleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.rules[request.RuleID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.ObservabilityAlertRuleDeleted {
		return ports.ObservabilityAlertRuleRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalObservabilityService) UpdateAlertRule(_ context.Context, request ports.ObservabilityAlertRuleUpdateRequest) (ports.ObservabilityAlertRuleRecord, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.ObservabilityAlertRuleRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rules[request.RuleID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.ObservabilityAlertRuleDeleted {
		return ports.ObservabilityAlertRuleRecord{}, ports.ErrNotFound
	}
	if strings.TrimSpace(request.Name) != "" {
		record.Name = strings.TrimSpace(request.Name)
	}
	if strings.TrimSpace(request.PromQL) != "" {
		record.PromQL = strings.TrimSpace(request.PromQL)
	}
	if request.Duration > 0 {
		record.Duration = request.Duration
	}
	if request.Severity != "" {
		severity, err := normalizeObservabilitySeverity(request.Severity)
		if err != nil {
			return ports.ObservabilityAlertRuleRecord{}, err
		}
		record.Severity = severity
	}
	if request.Labels != nil {
		record.Labels = cloneStringMap(request.Labels)
	}
	if request.Annotations != nil {
		record.Annotations = cloneStringMap(request.Annotations)
	}
	if request.Enabled != nil {
		record.Enabled = *request.Enabled
		record.State = alertStateFromEnabled(*request.Enabled)
	}
	record.UpdatedAt = s.now().UTC()
	record.DevProfile = observabilityDevProfile()
	s.rules[record.RuleID] = record
	return record, nil
}

func (s *LocalObservabilityService) DeleteAlertRule(_ context.Context, request ports.ObservabilityAlertRuleGetRequest) (ports.ObservabilityAlertRuleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rules[request.RuleID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.ObservabilityAlertRuleDeleted {
		return ports.ObservabilityAlertRuleRecord{}, ports.ErrNotFound
	}
	record.State = ports.ObservabilityAlertRuleDeleted
	record.Enabled = false
	record.UpdatedAt = s.now().UTC()
	record.DevProfile = observabilityDevProfile()
	s.rules[record.RuleID] = record
	return record, nil
}

func requireObservabilityTenantAndName(tenantID string, name string) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	return nil
}

func normalizeObservabilitySeverity(value ports.ObservabilityAlertSeverity) (ports.ObservabilityAlertSeverity, error) {
	if value == "" {
		return ports.ObservabilityAlertSeverityWarning, nil
	}
	switch value {
	case ports.ObservabilityAlertSeverityInfo, ports.ObservabilityAlertSeverityWarning, ports.ObservabilityAlertSeverityCritical:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unsupported alert severity %q", ports.ErrInvalid, value)
	}
}

func alertStateFromEnabled(enabled bool) ports.ObservabilityAlertRuleState {
	if enabled {
		return ports.ObservabilityAlertRuleActive
	}
	return ports.ObservabilityAlertRuleDisabled
}

func observabilityDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-observability-service",
		RealProvider: false,
		Reason:       "local profile records observability intent; it is not a real Prometheus provider execution",
	}
}

func firstNonZeroDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

var _ ports.ObservabilityService = (*LocalObservabilityService)(nil)

// QueryRange is a stub; real implementation arrives in PR3.
func (s *LocalObservabilityService) QueryRange(ctx context.Context, request ports.ObservabilityRangeQueryRequest) (ports.ObservabilityRangeQueryResult, error) {
	return ports.ObservabilityRangeQueryResult{}, errors.New("QueryRange: not implemented yet, see PR3")
}
