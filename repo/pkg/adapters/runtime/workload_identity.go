package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

const defaultWorkloadIdentityRateLimitRPM = 120

var defaultWorkloadIdentityScopes = []string{
	"scope:instances:read",
	"scope:instances:update",
	"scope:instances:console",
	"scope:volumes:read",
	"scope:objects:read",
}

type LocalWorkloadIdentityService struct {
	mu       sync.RWMutex
	now      func() time.Time
	bindings map[string]ports.WorkloadIdentityBinding
}

type WorkloadIdentityOption func(*LocalWorkloadIdentityService)

func WithWorkloadIdentityClock(now func() time.Time) WorkloadIdentityOption {
	return func(service *LocalWorkloadIdentityService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewLocalWorkloadIdentityService(options ...WorkloadIdentityOption) *LocalWorkloadIdentityService {
	service := &LocalWorkloadIdentityService{
		now:      time.Now,
		bindings: map[string]ports.WorkloadIdentityBinding{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalWorkloadIdentityService) BindScopedKey(_ context.Context, request ports.WorkloadIdentityBindRequest) (ports.WorkloadIdentityBinding, error) {
	if err := validateWorkloadIdentityBindRequest(request); err != nil {
		return ports.WorkloadIdentityBinding{}, err
	}
	now := firstNonZeroTime(request.RequestedAt, s.now().UTC())
	scopes := normalizedWorkloadIdentityScopes(request.Scopes)
	keyValue, err := generateWorkloadIdentityKey(request.TenantID)
	if err != nil {
		return ports.WorkloadIdentityBinding{}, err
	}
	binding := ports.WorkloadIdentityBinding{
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
		KeyID:      uuid.NewString(),
		KeyPrefix:  workloadIdentityKeyPrefix(keyValue),
		KeyValue:   keyValue,
		Scopes:     scopes,
		Active:     true,
		CreatedAt:  now,
	}
	if request.TTL > 0 {
		binding.ExpiresAt = now.Add(request.TTL)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[workloadIdentityIndexKey(request.TenantID, request.InstanceID)] = binding
	return binding, nil
}

func (s *LocalWorkloadIdentityService) GetForInstance(_ context.Context, tenantID string, instanceID string) (ports.WorkloadIdentityBinding, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" {
		return ports.WorkloadIdentityBinding{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.bindings[workloadIdentityIndexKey(tenantID, instanceID)]
	if !ok {
		return ports.WorkloadIdentityBinding{}, ports.ErrNotFound
	}
	binding.KeyValue = ""
	binding.Scopes = append([]string(nil), binding.Scopes...)
	return binding, nil
}

func (s *LocalWorkloadIdentityService) RevokeForInstance(_ context.Context, request ports.WorkloadIdentityRevokeRequest) (ports.WorkloadIdentityBinding, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadIdentityBinding{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workloadIdentityIndexKey(request.TenantID, request.InstanceID)
	binding, ok := s.bindings[key]
	if !ok {
		return ports.WorkloadIdentityBinding{
			TenantID:   request.TenantID,
			InstanceID: request.InstanceID,
			Active:     false,
		}, nil
	}
	if binding.Active {
		binding.Active = false
		binding.RevokedAt = firstNonZeroTime(request.RequestedAt, s.now().UTC())
	}
	binding.KeyValue = ""
	s.bindings[key] = binding
	return binding, nil
}

var _ ports.WorkloadIdentityService = (*LocalWorkloadIdentityService)(nil)

type MetadataWorkloadIdentityService struct {
	store ports.MetadataStore
	now   func() time.Time
}

func NewMetadataWorkloadIdentityService(store ports.MetadataStore, options ...WorkloadIdentityOption) *MetadataWorkloadIdentityService {
	local := NewLocalWorkloadIdentityService(options...)
	return &MetadataWorkloadIdentityService{store: store, now: local.now}
}

func (s *MetadataWorkloadIdentityService) BindScopedKey(ctx context.Context, request ports.WorkloadIdentityBindRequest) (ports.WorkloadIdentityBinding, error) {
	if s.store == nil {
		return ports.WorkloadIdentityBinding{}, ports.ErrNotConfigured
	}
	if err := validateWorkloadIdentityBindRequest(request); err != nil {
		return ports.WorkloadIdentityBinding{}, err
	}
	now := firstNonZeroTime(request.RequestedAt, s.now().UTC())
	scopes := normalizedWorkloadIdentityScopes(request.Scopes)
	keyValue, err := generateWorkloadIdentityKey(request.TenantID)
	if err != nil {
		return ports.WorkloadIdentityBinding{}, err
	}
	binding := ports.WorkloadIdentityBinding{
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
		KeyPrefix:  workloadIdentityKeyPrefix(keyValue),
		KeyValue:   keyValue,
		Scopes:     scopes,
		Active:     true,
		CreatedAt:  now,
	}
	if request.TTL > 0 {
		binding.ExpiresAt = now.Add(request.TTL)
	}
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO api_keys (
				tenant_id, user_id, name, key_hash, key_prefix, scopes,
				rate_limit_rpm, expires_at, instance_id, created_at
			)
			VALUES (
				$1::uuid, NULL, $2, $3, $4, $5,
				$6, NULLIF($7, '')::timestamptz, $8, $9
			)
			RETURNING id::text, created_at
		`, request.TenantID, workloadIdentityKeyName(request), hashWorkloadIdentityKey(keyValue),
			binding.KeyPrefix, scopes, defaultWorkloadIdentityRateLimitRPM,
			nullableTimeString(binding.ExpiresAt), request.InstanceID, binding.CreatedAt)
		return row.Scan(&binding.KeyID, &binding.CreatedAt)
	})
	if err != nil {
		return ports.WorkloadIdentityBinding{}, fmt.Errorf("bind workload identity key: %w", err)
	}
	return binding, nil
}

func (s *MetadataWorkloadIdentityService) GetForInstance(ctx context.Context, tenantID string, instanceID string) (ports.WorkloadIdentityBinding, error) {
	if s.store == nil {
		return ports.WorkloadIdentityBinding{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" {
		return ports.WorkloadIdentityBinding{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	var binding ports.WorkloadIdentityBinding
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT id::text, tenant_id::text, instance_id, key_prefix, scopes,
				revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW()) AS active,
				created_at, COALESCE(expires_at::text, ''), COALESCE(revoked_at::text, '')
			FROM api_keys
			WHERE tenant_id = $1::uuid AND instance_id = $2
			ORDER BY created_at DESC
			LIMIT 1
		`, tenantID, instanceID)
		return scanWorkloadIdentityBinding(row, &binding)
	})
	if err != nil {
		return ports.WorkloadIdentityBinding{}, err
	}
	return binding, nil
}

func (s *MetadataWorkloadIdentityService) RevokeForInstance(ctx context.Context, request ports.WorkloadIdentityRevokeRequest) (ports.WorkloadIdentityBinding, error) {
	if s.store == nil {
		return ports.WorkloadIdentityBinding{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadIdentityBinding{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	var binding ports.WorkloadIdentityBinding
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			UPDATE api_keys
			SET revoked_at = COALESCE(revoked_at, $3)
			WHERE tenant_id = $1::uuid AND instance_id = $2
			RETURNING id::text, tenant_id::text, instance_id, key_prefix, scopes,
				false AS active, created_at, COALESCE(expires_at::text, ''), COALESCE(revoked_at::text, '')
		`, request.TenantID, request.InstanceID, firstNonZeroTime(request.RequestedAt, s.now().UTC()))
		return scanWorkloadIdentityBinding(row, &binding)
	})
	if err == nil {
		return binding, nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return ports.WorkloadIdentityBinding{}, err
	}
	return ports.WorkloadIdentityBinding{
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
		Active:     false,
	}, nil
}

var _ ports.WorkloadIdentityService = (*MetadataWorkloadIdentityService)(nil)

func validateWorkloadIdentityBindRequest(request ports.WorkloadIdentityBindRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if request.Kind == "" {
		return fmt.Errorf("%w: workload kind is required", ports.ErrInvalid)
	}
	return nil
}

func normalizedWorkloadIdentityScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return append([]string(nil), defaultWorkloadIdentityScopes...)
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	if len(normalized) == 0 {
		return append([]string(nil), defaultWorkloadIdentityScopes...)
	}
	return normalized
}

func workloadIdentityIndexKey(tenantID string, instanceID string) string {
	return tenantID + "\x00" + instanceID
}

func workloadIdentityKeyName(request ports.WorkloadIdentityBindRequest) string {
	name := firstNonEmpty(strings.TrimSpace(request.InstanceName), request.InstanceID)
	if len(name) > 96 {
		name = name[:96]
	}
	return "workload:" + name
}

func generateWorkloadIdentityKey(tenantID string) (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	tenant := strings.ReplaceAll(strings.TrimSpace(tenantID), "-", "")
	if len(tenant) > 12 {
		tenant = tenant[:12]
	}
	if tenant == "" {
		tenant = "tenant"
	}
	return "ani_wi_" + tenant + "_" + base64.RawURLEncoding.EncodeToString(random), nil
}

func workloadIdentityKeyPrefix(key string) string {
	if len(key) <= 20 {
		return key
	}
	return key[:20]
}

func hashWorkloadIdentityKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func nullableTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func scanWorkloadIdentityBinding(row scanner, binding *ports.WorkloadIdentityBinding) error {
	var expiresAt string
	var revokedAt string
	if err := row.Scan(
		&binding.KeyID,
		&binding.TenantID,
		&binding.InstanceID,
		&binding.KeyPrefix,
		&binding.Scopes,
		&binding.Active,
		&binding.CreatedAt,
		&expiresAt,
		&revokedAt,
	); err != nil {
		return err
	}
	if expiresAt != "" {
		parsed, err := parsePostgresTimestamp(expiresAt)
		if err != nil {
			return err
		}
		binding.ExpiresAt = parsed
	}
	if revokedAt != "" {
		parsed, err := parsePostgresTimestamp(revokedAt)
		if err != nil {
			return err
		}
		binding.RevokedAt = parsed
	}
	binding.KeyValue = ""
	binding.Scopes = append([]string(nil), binding.Scopes...)
	return nil
}
