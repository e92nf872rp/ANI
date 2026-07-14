package runtime

import (
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type localEncryptionService struct {
	mu           sync.Mutex
	byID         map[string]ports.EncryptionKeyRecord
	idem         map[string]string
	rotationIdem map[string]ports.EncryptionKeyRotationRecord
	revokeIdem   map[string]ports.EncryptionKeyRecord
	sealIdem     map[string]ports.EncryptionSealRecord
	unsealIdem   map[string]ports.EncryptionUnsealTokenRecord
	provider     ports.EncryptionProvider
	store        ports.EncryptionKeyResourceStore
}

type EncryptionServiceOption func(*localEncryptionService)

func WithEncryptionProvider(provider ports.EncryptionProvider) EncryptionServiceOption {
	return func(service *localEncryptionService) {
		service.provider = provider
	}
}

func WithEncryptionResourceStore(store ports.EncryptionKeyResourceStore) EncryptionServiceOption {
	return func(service *localEncryptionService) {
		service.store = store
	}
}

func NewLocalEncryptionService(options ...EncryptionServiceOption) ports.EncryptionService {
	service := &localEncryptionService{
		byID:         map[string]ports.EncryptionKeyRecord{},
		idem:         map[string]string{},
		rotationIdem: map[string]ports.EncryptionKeyRotationRecord{},
		revokeIdem:   map[string]ports.EncryptionKeyRecord{},
		sealIdem:     map[string]ports.EncryptionSealRecord{},
		unsealIdem:   map[string]ports.EncryptionUnsealTokenRecord{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *localEncryptionService) CreateKey(ctx context.Context, req ports.EncryptionKeyCreateRequest) (ports.EncryptionKeyRecord, error) {
	if req.TenantID == "" || req.Name == "" || req.IdempotencyKey == "" {
		return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: tenant_id/name/idempotency_key required", ports.ErrInvalid)
	}
	if s.store != nil {
		if existing, err := s.store.GetEncryptionKeyByIdempotency(ctx, req.TenantID, req.IdempotencyKey); err == nil {
			return existing, nil
		} else if err != ports.ErrNotFound {
			return ports.EncryptionKeyRecord{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := req.TenantID + ":" + req.IdempotencyKey
	if id, ok := s.idem[key]; ok {
		return s.byID[id], nil
	}
	now := time.Now().Unix()
	algo := req.Algorithm
	if algo == "" {
		algo = "SM4"
	}
	rec := ports.EncryptionKeyRecord{KeyID: "ekey-" + uuid.NewString(), TenantID: req.TenantID, Name: req.Name, Algorithm: algo, State: "active", CreatedAt: now, UpdatedAt: now}
	if s.provider != nil {
		result, err := s.provider.CreateKeyMaterial(ctx, ports.EncryptionProviderCreateKeyRequest{
			TenantID:  rec.TenantID,
			KeyID:     rec.KeyID,
			Name:      rec.Name,
			Algorithm: rec.Algorithm,
		})
		if err != nil {
			return ports.EncryptionKeyRecord{}, err
		}
		if !result.Applied {
			return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: encryption provider did not create key material", ports.ErrNotConfigured)
		}
		rec = encryptionKeyWithProviderEvidence(rec, result)
	}
	s.byID[rec.KeyID] = rec
	s.idem[key] = rec.KeyID
	if err := s.upsertEncryptionKey(ctx, rec, req.IdempotencyKey); err != nil {
		delete(s.byID, rec.KeyID)
		delete(s.idem, key)
		return ports.EncryptionKeyRecord{}, err
	}
	return rec, nil
}
func (s *localEncryptionService) GetKey(ctx context.Context, req ports.EncryptionKeyGetRequest) (ports.EncryptionKeyRecord, error) {
	return s.getKeyRecord(ctx, req.TenantID, req.KeyID)
}
func (s *localEncryptionService) ListKeys(ctx context.Context, req ports.EncryptionKeyListRequest) ([]ports.EncryptionKeyRecord, error) {
	if s.store != nil {
		return s.store.ListEncryptionKeys(ctx, req.TenantID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.EncryptionKeyRecord{}
	for _, r := range s.byID {
		if r.TenantID == req.TenantID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (s *localEncryptionService) DeleteKey(ctx context.Context, req ports.EncryptionKeyGetRequest) (ports.EncryptionKeyRecord, error) {
	if s.store != nil {
		rec, err := s.getKeyRecord(ctx, req.TenantID, req.KeyID)
		if err != nil {
			return ports.EncryptionKeyRecord{}, err
		}
		if s.provider != nil {
			result, err := s.provider.DeleteKeyMaterial(ctx, ports.EncryptionProviderDeleteKeyRequest{
				TenantID:  rec.TenantID,
				KeyID:     rec.KeyID,
				Algorithm: rec.Algorithm,
			})
			if err != nil {
				return ports.EncryptionKeyRecord{}, err
			}
			if !result.Applied {
				return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: encryption provider did not delete key material", ports.ErrNotConfigured)
			}
			rec = encryptionKeyWithProviderEvidence(rec, result)
		}
		rec.State = "deleted"
		rec.UpdatedAt = time.Now().Unix()
		if err := s.upsertEncryptionKey(ctx, rec, ""); err != nil {
			return ports.EncryptionKeyRecord{}, err
		}
		s.mu.Lock()
		s.byID[req.KeyID] = rec
		s.mu.Unlock()
		return rec, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[req.KeyID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.EncryptionKeyRecord{}, ports.ErrNotFound
	}
	if s.provider != nil {
		result, err := s.provider.DeleteKeyMaterial(ctx, ports.EncryptionProviderDeleteKeyRequest{
			TenantID:  rec.TenantID,
			KeyID:     rec.KeyID,
			Algorithm: rec.Algorithm,
		})
		if err != nil {
			return ports.EncryptionKeyRecord{}, err
		}
		if !result.Applied {
			return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: encryption provider did not delete key material", ports.ErrNotConfigured)
		}
		rec = encryptionKeyWithProviderEvidence(rec, result)
	}
	rec.State = "deleted"
	rec.UpdatedAt = time.Now().Unix()
	s.byID[req.KeyID] = rec
	return rec, nil
}

func (s *localEncryptionService) RotateKey(ctx context.Context, req ports.EncryptionKeyRotateRequest) (ports.EncryptionKeyRotationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TenantID == "" || req.KeyID == "" || req.IdempotencyKey == "" {
		return ports.EncryptionKeyRotationRecord{}, fmt.Errorf("%w: tenant_id/key_id/idempotency_key required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	if rec, ok := s.rotationIdem[idemKey]; ok {
		return rec, nil
	}
	s.mu.Unlock()
	current, err := s.requireActiveKey(ctx, req.TenantID, req.KeyID)
	if err != nil {
		s.mu.Lock()
		return ports.EncryptionKeyRotationRecord{}, err
	}
	s.mu.Lock()
	if rec, ok := s.rotationIdem[idemKey]; ok {
		return rec, nil
	}
	now := time.Now().Unix()
	previous := current
	previous.State = "rotated"
	previous.UpdatedAt = now
	rotated := ports.EncryptionKeyRecord{
		KeyID:     "ekey-" + uuid.NewString(),
		TenantID:  current.TenantID,
		Name:      current.Name + "-rotated",
		Algorithm: current.Algorithm,
		State:     "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if s.provider != nil {
		result, err := s.provider.RotateKeyMaterial(ctx, ports.EncryptionProviderRotateKeyRequest{
			TenantID:      current.TenantID,
			PreviousKeyID: current.KeyID,
			RotatedKeyID:  rotated.KeyID,
			Name:          rotated.Name,
			Algorithm:     rotated.Algorithm,
		})
		if err != nil {
			return ports.EncryptionKeyRotationRecord{}, err
		}
		if !result.Applied {
			return ports.EncryptionKeyRotationRecord{}, fmt.Errorf("%w: encryption provider did not rotate key material", ports.ErrNotConfigured)
		}
		rotated = encryptionKeyWithProviderEvidence(rotated, result)
		previous.Provider = current.Provider
		previous.RealProvider = current.RealProvider
		previous.ProviderRefs = append([]string(nil), current.ProviderRefs...)
	}
	s.byID[previous.KeyID] = previous
	s.byID[rotated.KeyID] = rotated
	rec := ports.EncryptionKeyRotationRecord{
		TenantID:    current.TenantID,
		PreviousKey: previous,
		RotatedKey:  rotated,
		RotationID:  "erot-" + uuid.NewString(),
		RotatedAt:   now,
	}
	s.rotationIdem[idemKey] = rec
	if err := s.upsertEncryptionKey(ctx, previous, ""); err != nil {
		delete(s.rotationIdem, idemKey)
		delete(s.byID, rotated.KeyID)
		s.byID[current.KeyID] = current
		return ports.EncryptionKeyRotationRecord{}, err
	}
	if err := s.upsertEncryptionKey(ctx, rotated, ""); err != nil {
		delete(s.rotationIdem, idemKey)
		delete(s.byID, rotated.KeyID)
		s.byID[current.KeyID] = current
		return ports.EncryptionKeyRotationRecord{}, err
	}
	return rec, nil
}

func (s *localEncryptionService) RevokeKey(ctx context.Context, req ports.EncryptionKeyRevokeRequest) (ports.EncryptionKeyRecord, error) {
	if req.TenantID == "" || req.KeyID == "" || req.IdempotencyKey == "" {
		return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: tenant_id/key_id/idempotency_key required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	s.mu.Lock()
	if rec, ok := s.revokeIdem[idemKey]; ok {
		s.mu.Unlock()
		return rec, nil
	}
	s.mu.Unlock()
	rec, err := s.getKeyRecord(ctx, req.TenantID, req.KeyID)
	if err != nil {
		return ports.EncryptionKeyRecord{}, err
	}
	if rec.State == "deleted" {
		return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: deleted encryption key cannot be revoked", ports.ErrConflict)
	}
	if s.provider != nil {
		result, err := s.provider.RevokeKeyMaterial(ctx, ports.EncryptionProviderRevokeKeyRequest{
			TenantID:  rec.TenantID,
			KeyID:     rec.KeyID,
			Reason:    req.Reason,
			Algorithm: rec.Algorithm,
		})
		if err != nil {
			return ports.EncryptionKeyRecord{}, err
		}
		if !result.Applied {
			return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: encryption provider did not revoke key material", ports.ErrNotConfigured)
		}
		rec = encryptionKeyWithProviderEvidence(rec, result)
	}
	rec.State = "revoked"
	rec.UpdatedAt = time.Now().Unix()
	if err := s.upsertEncryptionKey(ctx, rec, ""); err != nil {
		return ports.EncryptionKeyRecord{}, err
	}
	s.mu.Lock()
	s.byID[req.KeyID] = rec
	s.revokeIdem[idemKey] = rec
	s.mu.Unlock()
	return rec, nil
}

func (s *localEncryptionService) Seal(ctx context.Context, req ports.EncryptionSealRequest) (ports.EncryptionSealRecord, error) {
	if req.TenantID == "" || req.IdempotencyKey == "" || req.KeyID == "" || req.ObjectURI == "" {
		return ports.EncryptionSealRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key/key_id/object_uri required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	s.mu.Lock()
	if rec, ok := s.sealIdem[idemKey]; ok {
		s.mu.Unlock()
		return rec, nil
	}
	s.mu.Unlock()
	key, err := s.requireActiveKey(ctx, req.TenantID, req.KeyID)
	if err != nil {
		return ports.EncryptionSealRecord{}, err
	}
	now := time.Now().Unix()
	if s.provider != nil {
		result, err := s.provider.SealObject(ctx, ports.EncryptionProviderSealRequest{
			TenantID:       key.TenantID,
			KeyID:          key.KeyID,
			Algorithm:      key.Algorithm,
			ObjectURI:      req.ObjectURI,
			IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			return ports.EncryptionSealRecord{}, err
		}
		if result.SealedObjectURI == "" || result.UnsealToken == "" {
			return ports.EncryptionSealRecord{}, fmt.Errorf("%w: encryption provider seal result is incomplete", ports.ErrInvalid)
		}
		expiresAt := result.ExpiresAt
		if expiresAt.IsZero() {
			expiresAt = time.Unix(now+3600, 0).UTC()
		}
		rec := ports.EncryptionSealRecord{
			KeyID:           key.KeyID,
			TenantID:        key.TenantID,
			ObjectURI:       req.ObjectURI,
			SealedObjectURI: result.SealedObjectURI,
			UnsealToken:     result.UnsealToken,
			Provider:        providerName(result.Provider),
			RealProvider:    true,
			ProviderRefs:    append([]string(nil), result.ResourceRefs...),
			ExpiresAt:       expiresAt.Unix(),
			CreatedAt:       now,
		}
		s.mu.Lock()
		s.sealIdem[idemKey] = rec
		s.mu.Unlock()
		return rec, nil
	}
	digest := sha256.Sum256([]byte(req.TenantID + ":" + req.KeyID + ":" + req.ObjectURI + ":" + req.IdempotencyKey))
	sealedURI := fmt.Sprintf("sealed://local/%s/%s", key.KeyID, hex.EncodeToString(digest[:12]))
	rec := ports.EncryptionSealRecord{
		KeyID:           key.KeyID,
		TenantID:        key.TenantID,
		ObjectURI:       req.ObjectURI,
		SealedObjectURI: sealedURI,
		UnsealToken:     "utok-" + uuid.NewString(),
		ExpiresAt:       now + 3600,
		CreatedAt:       now,
	}
	s.mu.Lock()
	s.sealIdem[idemKey] = rec
	s.mu.Unlock()
	return rec, nil
}

func (s *localEncryptionService) CreateUnsealToken(ctx context.Context, req ports.EncryptionUnsealTokenRequest) (ports.EncryptionUnsealTokenRecord, error) {
	if req.TenantID == "" || req.IdempotencyKey == "" || req.KeyID == "" || req.SealedObjectURI == "" {
		return ports.EncryptionUnsealTokenRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key/key_id/sealed_object_uri required", ports.ErrInvalid)
	}
	idemKey := req.TenantID + ":" + req.IdempotencyKey
	s.mu.Lock()
	if rec, ok := s.unsealIdem[idemKey]; ok {
		s.mu.Unlock()
		return rec, nil
	}
	s.mu.Unlock()
	key, err := s.requireActiveKey(ctx, req.TenantID, req.KeyID)
	if err != nil {
		return ports.EncryptionUnsealTokenRecord{}, err
	}
	now := time.Now().Unix()
	if s.provider != nil {
		result, err := s.provider.CreateUnsealToken(ctx, ports.EncryptionProviderUnsealTokenRequest{
			TenantID:        key.TenantID,
			KeyID:           key.KeyID,
			Algorithm:       key.Algorithm,
			SealedObjectURI: req.SealedObjectURI,
		})
		if err != nil {
			return ports.EncryptionUnsealTokenRecord{}, err
		}
		if result.UnsealToken == "" {
			return ports.EncryptionUnsealTokenRecord{}, fmt.Errorf("%w: encryption provider unseal token result is incomplete", ports.ErrInvalid)
		}
		expiresAt := result.ExpiresAt
		if expiresAt.IsZero() {
			expiresAt = time.Unix(now+3600, 0).UTC()
		}
		rec := ports.EncryptionUnsealTokenRecord{
			KeyID:           key.KeyID,
			TenantID:        key.TenantID,
			SealedObjectURI: req.SealedObjectURI,
			UnsealToken:     result.UnsealToken,
			Provider:        providerName(result.Provider),
			RealProvider:    true,
			ProviderRefs:    append([]string(nil), result.ResourceRefs...),
			ExpiresAt:       expiresAt.Unix(),
			CreatedAt:       now,
		}
		s.mu.Lock()
		s.unsealIdem[idemKey] = rec
		s.mu.Unlock()
		return rec, nil
	}
	rec := ports.EncryptionUnsealTokenRecord{
		KeyID:           key.KeyID,
		TenantID:        key.TenantID,
		SealedObjectURI: req.SealedObjectURI,
		UnsealToken:     "utok-" + uuid.NewString(),
		ExpiresAt:       now + 3600,
		CreatedAt:       now,
	}
	s.mu.Lock()
	s.unsealIdem[idemKey] = rec
	s.mu.Unlock()
	return rec, nil
}

func (s *localEncryptionService) SealObjectContent(ctx context.Context, req ports.EncryptionObjectContentSealRequest, plaintext io.Reader, sealed io.Writer) (ports.EncryptionObjectContentSealRecord, error) {
	if plaintext == nil || sealed == nil {
		return ports.EncryptionObjectContentSealRecord{}, fmt.Errorf("%w: plaintext reader and sealed writer are required", ports.ErrInvalid)
	}
	if req.TenantID == "" || req.IdempotencyKey == "" || req.KeyID == "" || req.ObjectURI == "" {
		return ports.EncryptionObjectContentSealRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key/key_id/object_uri required", ports.ErrInvalid)
	}
	key, err := s.requireActiveKey(ctx, req.TenantID, req.KeyID)
	if err != nil {
		return ports.EncryptionObjectContentSealRecord{}, err
	}
	gcm, err := localSM4GCMForKey(key)
	if err != nil {
		return ports.EncryptionObjectContentSealRecord{}, err
	}
	chunkSize := req.ChunkSize
	if chunkSize == 0 {
		chunkSize = 64 * 1024
	}
	if chunkSize < 0 {
		return ports.EncryptionObjectContentSealRecord{}, fmt.Errorf("%w: chunk_size must be greater than zero", ports.ErrInvalid)
	}
	nonce := localContentNonce(req.TenantID, req.KeyID, req.ObjectURI, req.IdempotencyKey)
	sealedURI := localSealedContentURI(req.TenantID, req.KeyID, req.ObjectURI, req.IdempotencyKey)
	record := ports.EncryptionObjectContentSealRecord{
		KeyID:           key.KeyID,
		TenantID:        key.TenantID,
		ObjectURI:       req.ObjectURI,
		SealedObjectURI: sealedURI,
		Algorithm:       "SM4-GCM",
		Nonce:           base64.RawURLEncoding.EncodeToString(nonce),
		ChunkSize:       chunkSize,
		Provider:        "local-sm4-gcm",
		RealProvider:    false,
		CreatedAt:       time.Now().Unix(),
	}
	return sealObjectContentWithRecord(ctx, gcm, record, plaintext, sealed)
}

func (s *localEncryptionService) OpenObjectContent(ctx context.Context, req ports.EncryptionObjectContentOpenRequest, sealed io.Reader, plaintext io.Writer) (ports.EncryptionObjectContentOpenRecord, error) {
	if sealed == nil || plaintext == nil {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: sealed reader and plaintext writer are required", ports.ErrInvalid)
	}
	if req.TenantID == "" || req.KeyID == "" || req.ObjectURI == "" || req.SealedObjectURI == "" || req.Nonce == "" {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: tenant_id/key_id/object_uri/sealed_object_uri/nonce required", ports.ErrInvalid)
	}
	if req.ChunkSize <= 0 || req.ChunkCount <= 0 {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: chunk_size and chunk_count must be greater than zero", ports.ErrInvalid)
	}
	key, err := s.requireActiveKey(ctx, req.TenantID, req.KeyID)
	if err != nil {
		return ports.EncryptionObjectContentOpenRecord{}, err
	}
	gcm, err := localSM4GCMForKey(key)
	if err != nil {
		return ports.EncryptionObjectContentOpenRecord{}, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(req.Nonce)
	if err != nil {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: invalid content nonce", ports.ErrInvalid)
	}
	if len(nonce) != gcm.NonceSize() {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: invalid content nonce size", ports.ErrInvalid)
	}
	return openObjectContentWithRecord(ctx, gcm, req, nonce, sealed, plaintext)
}

func (s *localEncryptionService) requireActiveKey(ctx context.Context, tenantID string, keyID string) (ports.EncryptionKeyRecord, error) {
	key, err := s.getKeyRecord(ctx, tenantID, keyID)
	if err != nil {
		return ports.EncryptionKeyRecord{}, err
	}
	if key.State != "active" {
		return ports.EncryptionKeyRecord{}, fmt.Errorf("%w: encryption key is not active", ports.ErrConflict)
	}
	return key, nil
}

func (s *localEncryptionService) getKeyRecord(ctx context.Context, tenantID, keyID string) (ports.EncryptionKeyRecord, error) {
	s.mu.Lock()
	if rec, ok := s.byID[keyID]; ok && rec.TenantID == tenantID {
		s.mu.Unlock()
		return rec, nil
	}
	s.mu.Unlock()
	if s.store != nil {
		return s.store.GetEncryptionKey(ctx, tenantID, keyID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[keyID]
	if !ok || rec.TenantID != tenantID {
		return ports.EncryptionKeyRecord{}, ports.ErrNotFound
	}
	return rec, nil
}

func (s *localEncryptionService) upsertEncryptionKey(ctx context.Context, record ports.EncryptionKeyRecord, idempotencyKey string) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertEncryptionKey(ctx, record, idempotencyKey)
}

func encryptionKeyWithProviderEvidence(rec ports.EncryptionKeyRecord, result ports.EncryptionProviderKeyResult) ports.EncryptionKeyRecord {
	rec.Provider = providerName(result.Provider)
	rec.RealProvider = true
	rec.ProviderRefs = append([]string(nil), result.ResourceRefs...)
	return rec
}

func providerName(provider string) string {
	if provider == "" {
		return "kms-sm4"
	}
	return provider
}

func localSM4GCMForKey(key ports.EncryptionKeyRecord) (cipher.AEAD, error) {
	if key.RealProvider {
		return nil, fmt.Errorf("%w: provider-backed object content encryption requires provider streaming support", ports.ErrNotConfigured)
	}
	if strings.ToUpper(strings.TrimSpace(key.Algorithm)) != "SM4" {
		return nil, fmt.Errorf("%w: object content encryption requires SM4 key", ports.ErrUnsupported)
	}
	digest := sha256.Sum256([]byte(key.TenantID + "\x00" + key.KeyID + "\x00" + key.Name + "\x00" + key.Algorithm))
	block, err := newSM4BlockCipher(digest[:16])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealObjectContentWithRecord(ctx context.Context, gcm cipher.AEAD, record ports.EncryptionObjectContentSealRecord, plaintext io.Reader, sealed io.Writer) (ports.EncryptionObjectContentSealRecord, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(record.Nonce)
	if err != nil {
		return ports.EncryptionObjectContentSealRecord{}, fmt.Errorf("%w: invalid content nonce", ports.ErrInvalid)
	}
	buf := make([]byte, record.ChunkSize)
	plainHash := sha256.New()
	cipherHash := sha256.New()
	chunkIndex := 0
	for {
		if err := ctx.Err(); err != nil {
			return ports.EncryptionObjectContentSealRecord{}, err
		}
		n, readErr := plaintext.Read(buf)
		if n > 0 {
			plainChunk := buf[:n]
			plainHash.Write(plainChunk)
			sealedChunk := gcm.Seal(nil, contentChunkNonce(nonce, chunkIndex), plainChunk, contentAAD(record.TenantID, record.KeyID, record.ObjectURI, record.SealedObjectURI, chunkIndex))
			if err := writeSealedContentFrame(sealed, cipherHash, sealedChunk); err != nil {
				return ports.EncryptionObjectContentSealRecord{}, err
			}
			record.PlaintextSizeBytes += int64(n)
			record.CiphertextSizeBytes += int64(4 + len(sealedChunk))
			chunkIndex++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return ports.EncryptionObjectContentSealRecord{}, readErr
		}
	}
	if chunkIndex == 0 {
		sealedChunk := gcm.Seal(nil, contentChunkNonce(nonce, 0), nil, contentAAD(record.TenantID, record.KeyID, record.ObjectURI, record.SealedObjectURI, 0))
		if err := writeSealedContentFrame(sealed, cipherHash, sealedChunk); err != nil {
			return ports.EncryptionObjectContentSealRecord{}, err
		}
		record.CiphertextSizeBytes = int64(4 + len(sealedChunk))
		chunkIndex = 1
	}
	record.ChunkCount = chunkIndex
	record.PlaintextSHA256 = hex.EncodeToString(plainHash.Sum(nil))
	record.CiphertextSHA256 = hex.EncodeToString(cipherHash.Sum(nil))
	return record, nil
}

func openObjectContentWithRecord(ctx context.Context, gcm cipher.AEAD, req ports.EncryptionObjectContentOpenRequest, nonce []byte, sealed io.Reader, plaintext io.Writer) (ports.EncryptionObjectContentOpenRecord, error) {
	plainHash := sha256.New()
	cipherHash := sha256.New()
	var plaintextSize int64
	for chunkIndex := 0; chunkIndex < req.ChunkCount; chunkIndex++ {
		if err := ctx.Err(); err != nil {
			return ports.EncryptionObjectContentOpenRecord{}, err
		}
		frame, err := readSealedContentFrame(sealed, cipherHash)
		if err != nil {
			return ports.EncryptionObjectContentOpenRecord{}, err
		}
		plainChunk, err := gcm.Open(nil, contentChunkNonce(nonce, chunkIndex), frame, contentAAD(req.TenantID, req.KeyID, req.ObjectURI, req.SealedObjectURI, chunkIndex))
		if err != nil {
			return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: object content authentication failed", ports.ErrInvalid)
		}
		if _, err := plaintext.Write(plainChunk); err != nil {
			return ports.EncryptionObjectContentOpenRecord{}, err
		}
		plainHash.Write(plainChunk)
		plaintextSize += int64(len(plainChunk))
	}
	var extra [1]byte
	if n, err := sealed.Read(extra[:]); err != io.EOF || n != 0 {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: sealed content has trailing bytes", ports.ErrInvalid)
	}
	plainDigest := hex.EncodeToString(plainHash.Sum(nil))
	if req.PlaintextSHA256 != "" && plainDigest != req.PlaintextSHA256 {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: plaintext digest mismatch", ports.ErrInvalid)
	}
	cipherDigest := hex.EncodeToString(cipherHash.Sum(nil))
	if req.CiphertextSHA256 != "" && cipherDigest != req.CiphertextSHA256 {
		return ports.EncryptionObjectContentOpenRecord{}, fmt.Errorf("%w: ciphertext digest mismatch", ports.ErrInvalid)
	}
	return ports.EncryptionObjectContentOpenRecord{
		KeyID:              req.KeyID,
		TenantID:           req.TenantID,
		ObjectURI:          req.ObjectURI,
		SealedObjectURI:    req.SealedObjectURI,
		Algorithm:          "SM4-GCM",
		ChunkSize:          req.ChunkSize,
		ChunkCount:         req.ChunkCount,
		PlaintextSizeBytes: plaintextSize,
		PlaintextSHA256:    plainDigest,
		Provider:           "local-sm4-gcm",
		RealProvider:       false,
		OpenedAt:           time.Now().Unix(),
	}, nil
}

func localContentNonce(tenantID string, keyID string, objectURI string, idempotencyKey string) []byte {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + keyID + "\x00" + objectURI + "\x00" + idempotencyKey))
	return append([]byte(nil), digest[:12]...)
}

func localSealedContentURI(tenantID string, keyID string, objectURI string, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + keyID + "\x00" + objectURI + "\x00" + idempotencyKey))
	return "sealed+sm4-gcm://local/" + keyID + "/" + hex.EncodeToString(digest[:12])
}

func contentChunkNonce(base []byte, chunkIndex int) []byte {
	nonce := append([]byte(nil), base...)
	offset := len(nonce) - 8
	value := binary.BigEndian.Uint64(nonce[offset:]) ^ uint64(chunkIndex)
	binary.BigEndian.PutUint64(nonce[offset:], value)
	return nonce
}

func contentAAD(tenantID string, keyID string, objectURI string, sealedObjectURI string, chunkIndex int) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%d", tenantID, keyID, objectURI, sealedObjectURI, chunkIndex))
}

func writeSealedContentFrame(w io.Writer, hash io.Writer, frame []byte) error {
	if len(frame) > int(^uint32(0)) {
		return fmt.Errorf("%w: sealed content frame is too large", ports.ErrInvalid)
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(frame)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	if _, err := hash.Write(size[:]); err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	_, err := hash.Write(frame)
	return err
}

func readSealedContentFrame(r io.Reader, hash io.Writer) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	if _, err := hash.Write(size[:]); err != nil {
		return nil, err
	}
	frameSize := binary.BigEndian.Uint32(size[:])
	frame := make([]byte, int(frameSize))
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	if _, err := hash.Write(frame); err != nil {
		return nil, err
	}
	return frame, nil
}
