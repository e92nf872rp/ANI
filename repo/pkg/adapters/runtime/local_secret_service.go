package runtime

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type localSecretService struct {
	mu            sync.Mutex
	byID          map[string]secretEntry
	idem          map[string]string
	bindings      map[string]ports.SecretBindingRecord
	providerApply ports.SecretProviderApply
}

type secretEntry struct {
	record ports.SecretRecord
	data   map[string]string
}

type SecretServiceOption func(*localSecretService)

func WithSecretProviderApply(provider ports.SecretProviderApply) SecretServiceOption {
	return func(service *localSecretService) {
		service.providerApply = provider
	}
}

func NewLocalSecretService(options ...SecretServiceOption) ports.SecretService {
	service := &localSecretService{
		byID:     map[string]secretEntry{},
		idem:     map[string]string{},
		bindings: map[string]ports.SecretBindingRecord{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *localSecretService) CreateSecret(ctx context.Context, req ports.SecretCreateRequest) (ports.SecretRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.IdempotencyKey == "" || req.Name == "" || len(req.Data) == 0 {
		return ports.SecretRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key/name/data required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	if id, ok := s.idem[idemKey]; ok {
		return s.byID[id].record, nil
	}
	now := time.Now().Unix()
	secretType := req.Type
	if secretType == "" {
		secretType = "opaque"
	}
	rec := ports.SecretRecord{
		SecretID:  "sec-" + uuid.NewString(),
		TenantID:  req.TenantID,
		Name:      req.Name,
		Type:      secretType,
		Keys:      sortedSecretKeys(req.Data),
		State:     "active",
		Provider:  "local",
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.byID[rec.SecretID] = secretEntry{record: rec, data: cloneSecretData(req.Data)}
	s.idem[idemKey] = rec.SecretID
	if s.providerApply != nil {
		result, err := s.providerApply.ApplySecret(ctx, ports.SecretProviderApplyRequest{
			TenantID: rec.TenantID,
			SecretID: rec.SecretID,
			Name:     rec.Name,
			Type:     rec.Type,
			Data:     cloneSecretData(req.Data),
		})
		if err != nil {
			delete(s.byID, rec.SecretID)
			delete(s.idem, idemKey)
			return ports.SecretRecord{}, err
		}
		if !result.Applied {
			delete(s.byID, rec.SecretID)
			delete(s.idem, idemKey)
			return ports.SecretRecord{}, fmt.Errorf("%w: secret provider did not apply secret", ports.ErrNotConfigured)
		}
		rec.Provider = result.Provider
		rec.RealProvider = true
		rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
		s.byID[rec.SecretID] = secretEntry{record: rec, data: cloneSecretData(req.Data)}
	}
	return rec, nil
}

func (s *localSecretService) GetSecret(_ context.Context, req ports.SecretGetRequest) (ports.SecretRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[req.SecretID]
	if !ok || entry.record.TenantID != req.TenantID {
		return ports.SecretRecord{}, ports.ErrNotFound
	}
	return entry.record, nil
}

func (s *localSecretService) ListSecrets(_ context.Context, req ports.SecretListRequest) ([]ports.SecretRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.SecretRecord{}
	for _, entry := range s.byID {
		if entry.record.TenantID == req.TenantID {
			out = append(out, entry.record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (s *localSecretService) DeleteSecret(_ context.Context, req ports.SecretGetRequest) (ports.SecretRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[req.SecretID]
	if !ok || entry.record.TenantID != req.TenantID {
		return ports.SecretRecord{}, ports.ErrNotFound
	}
	entry.record.State = "deleted"
	entry.record.UpdatedAt = time.Now().Unix()
	s.byID[req.SecretID] = entry
	return entry.record, nil
}

func (s *localSecretService) BindSecret(_ context.Context, req ports.SecretBindRequest) (ports.SecretBindingRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[req.SecretID]
	if !ok || entry.record.TenantID != req.TenantID {
		return ports.SecretBindingRecord{}, ports.ErrNotFound
	}
	if entry.record.State != "active" {
		return ports.SecretBindingRecord{}, fmt.Errorf("%w: secret is not active", ports.ErrConflict)
	}
	if req.TargetType == "" || req.TargetID == "" {
		return ports.SecretBindingRecord{}, fmt.Errorf("%w: target_type/target_id required", ports.ErrInvalid)
	}
	now := time.Now().Unix()
	rec := ports.SecretBindingRecord{
		BindingID:  "sbind-" + uuid.NewString(),
		SecretID:   req.SecretID,
		TenantID:   req.TenantID,
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		MountPath:  req.MountPath,
		EnvPrefix:  req.EnvPrefix,
		State:      "bound",
		CreatedAt:  now,
	}
	s.bindings[rec.BindingID] = rec
	return rec, nil
}

func sortedSecretKeys(data map[string]string) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneSecretData(data map[string]string) map[string]string {
	out := make(map[string]string, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}
