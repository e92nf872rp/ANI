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
	bindingIdem   map[string]string
	providerApply ports.SecretProviderApply
	store         ports.SecretResourceStore
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

func WithSecretResourceStore(store ports.SecretResourceStore) SecretServiceOption {
	return func(service *localSecretService) {
		service.store = store
	}
}

func NewLocalSecretService(options ...SecretServiceOption) ports.SecretService {
	service := &localSecretService{
		byID:        map[string]secretEntry{},
		idem:        map[string]string{},
		bindings:    map[string]ports.SecretBindingRecord{},
		bindingIdem: map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *localSecretService) CreateSecret(ctx context.Context, req ports.SecretCreateRequest) (ports.SecretRecord, error) {
	if req.TenantID == "" || req.IdempotencyKey == "" || req.Name == "" || len(req.Data) == 0 {
		return ports.SecretRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key/name/data required", ports.ErrInvalid)
	}
	if s.store != nil {
		if existing, err := s.store.GetSecretByIdempotency(ctx, req.TenantID, req.IdempotencyKey); err == nil {
			return existing, nil
		} else if err != ports.ErrNotFound {
			return ports.SecretRecord{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := s.upsertSecret(ctx, rec, req.IdempotencyKey); err != nil {
		delete(s.byID, rec.SecretID)
		delete(s.idem, idemKey)
		return ports.SecretRecord{}, err
	}
	return rec, nil
}

func (s *localSecretService) GetSecret(ctx context.Context, req ports.SecretGetRequest) (ports.SecretRecord, error) {
	return s.getSecretRecord(ctx, req.TenantID, req.SecretID)
}

func (s *localSecretService) ListSecrets(ctx context.Context, req ports.SecretListRequest) ([]ports.SecretRecord, error) {
	if s.store != nil {
		return s.store.ListSecrets(ctx, req.TenantID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.SecretRecord{}
	for _, entry := range s.byID {
		if entry.record.TenantID == req.TenantID && entry.record.State != "deleted" {
			out = append(out, entry.record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (s *localSecretService) DeleteSecret(ctx context.Context, req ports.SecretGetRequest) (ports.SecretRecord, error) {
	if s.store != nil {
		rec, err := s.getSecretRecord(ctx, req.TenantID, req.SecretID)
		if err != nil {
			return ports.SecretRecord{}, err
		}
		rec.State = "deleted"
		rec.UpdatedAt = time.Now().Unix()
		if err := s.upsertSecret(ctx, rec, ""); err != nil {
			return ports.SecretRecord{}, err
		}
		s.mu.Lock()
		if entry, ok := s.byID[req.SecretID]; ok {
			entry.record = rec
			s.byID[req.SecretID] = entry
		}
		s.mu.Unlock()
		return rec, nil
	}
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

func (s *localSecretService) BindSecret(ctx context.Context, req ports.SecretBindRequest) (ports.SecretBindingRecord, error) {
	if req.TenantID == "" || req.IdempotencyKey == "" {
		return ports.SecretBindingRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	s.mu.Lock()
	if bindingID, ok := s.bindingIdem[idemKey]; ok {
		binding := s.bindings[bindingID]
		s.mu.Unlock()
		return binding, nil
	}
	s.mu.Unlock()
	rec, err := s.getSecretRecord(ctx, req.TenantID, req.SecretID)
	if err != nil {
		return ports.SecretBindingRecord{}, err
	}
	if rec.State != "active" {
		return ports.SecretBindingRecord{}, fmt.Errorf("%w: secret is not active", ports.ErrConflict)
	}
	if req.TargetType == "" || req.TargetID == "" {
		return ports.SecretBindingRecord{}, fmt.Errorf("%w: target_type/target_id required", ports.ErrInvalid)
	}
	now := time.Now().Unix()
	binding := ports.SecretBindingRecord{
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
	s.mu.Lock()
	s.bindings[binding.BindingID] = binding
	s.bindingIdem[idemKey] = binding.BindingID
	s.mu.Unlock()
	if err := s.upsertSecretBinding(ctx, binding); err != nil {
		s.mu.Lock()
		delete(s.bindings, binding.BindingID)
		delete(s.bindingIdem, idemKey)
		s.mu.Unlock()
		return ports.SecretBindingRecord{}, err
	}
	return binding, nil
}

func (s *localSecretService) getSecretRecord(ctx context.Context, tenantID, secretID string) (ports.SecretRecord, error) {
	s.mu.Lock()
	if entry, ok := s.byID[secretID]; ok && entry.record.TenantID == tenantID {
		s.mu.Unlock()
		return entry.record, nil
	}
	s.mu.Unlock()
	if s.store != nil {
		return s.store.GetSecret(ctx, tenantID, secretID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[secretID]
	if !ok || entry.record.TenantID != tenantID {
		return ports.SecretRecord{}, ports.ErrNotFound
	}
	return entry.record, nil
}

func (s *localSecretService) upsertSecret(ctx context.Context, record ports.SecretRecord, idempotencyKey string) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertSecret(ctx, record, idempotencyKey)
}

func (s *localSecretService) upsertSecretBinding(ctx context.Context, record ports.SecretBindingRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertSecretBinding(ctx, record)
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
