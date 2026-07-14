package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const apiKeyEnv = "dev"
const maxAPIKeyNameLength = 128
const defaultAPIKeyRateLimitRPM int32 = 60
const maxAPIKeyRateLimitRPM int32 = 10000
const apiKeyReplayTTL = 15 * time.Minute

var errAPIKeyRateLimitExceeded = errors.New("api key rate limit exceeded")
var errAPIKeyReplayExpired = errors.New("api key idempotency replay expired")

type apiKeyStore struct {
	db           *pgxpool.Pool
	cache        ports.CacheStore
	replayKey    [32]byte
	createRecord func(context.Context, apiKeyCreateRecord) (uuid.UUID, error)
}

type apiKeyPrincipal struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Scopes   []string
}

type apiKeyCreateRecord struct {
	TenantID       uuid.UUID
	UserID         uuid.UUID
	Name           string
	Scopes         []string
	RateLimitRPM   int32
	ExpiresAt      time.Time
	RawKey         string
	KeyHash        string
	KeyPrefix      string
	IdempotencyKey string
}

type apiKeyReplayRecord struct {
	KeyID     string `json:"key_id"`
	KeyValue  string `json:"key_value"`
	KeyPrefix string `json:"key_prefix"`
}

func newAPIKeyStore(db *pgxpool.Pool, cache ports.CacheStore, replayKeyMaterial ...[]byte) *apiKeyStore {
	store := &apiKeyStore{
		db:        db,
		cache:     cache,
		replayKey: deriveAPIKeyReplayKey(replayKeyMaterial...),
	}
	store.createRecord = store.insertRecord
	return store
}

func (s *apiKeyStore) create(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil || tenantID == uuid.Nil {
		return nil, fmt.Errorf("invalid tenant_id")
	}
	idempotencyKey := strings.TrimSpace(req.GetIdempotencyKey())
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency_key required")
	}
	if len(idempotencyKey) > 128 {
		return nil, fmt.Errorf("idempotency_key too long")
	}
	if s.cache == nil {
		return nil, fmt.Errorf("api key idempotency replay cache required")
	}
	if replay, err := s.getReplay(ctx, tenantID, idempotencyKey); err == nil {
		return replay, nil
	} else if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return nil, err
	}
	name, err := normalizeAPIKeyName(req.GetName())
	if err != nil {
		return nil, err
	}
	scopes, err := normalizeAPIKeyScopes(req.GetScopes())
	if err != nil {
		return nil, err
	}
	var userID uuid.UUID
	if req.GetUserId() != "" {
		userID, err = uuid.Parse(req.GetUserId())
		if err != nil || userID == uuid.Nil {
			return nil, fmt.Errorf("invalid user_id")
		}
	}
	rateLimit, err := normalizeAPIKeyRateLimit(req.GetRateLimitRpm())
	if err != nil {
		return nil, err
	}

	rawKey, err := generateAPIKey(tenantID)
	if err != nil {
		return nil, err
	}
	keyHash := hashAPIKey(rawKey)
	keyPrefix := prefixAPIKey(rawKey)
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID, UserID: userID})

	expiresAtTime, err := normalizeAPIKeyExpiresAt(req.GetExpiresAt(), time.Now())
	if err != nil {
		return nil, err
	}
	keyID, err := s.createRecord(ctx, apiKeyCreateRecord{
		TenantID:       tenantID,
		UserID:         userID,
		Name:           name,
		Scopes:         scopes,
		RateLimitRPM:   rateLimit,
		ExpiresAt:      expiresAtTime,
		RawKey:         rawKey,
		KeyHash:        keyHash,
		KeyPrefix:      keyPrefix,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	resp := &authv1.CreateAPIKeyResponse{
		KeyId:     keyID.String(),
		KeyValue:  rawKey,
		KeyPrefix: keyPrefix,
	}
	if err := s.putReplay(ctx, tenantID, idempotencyKey, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *apiKeyStore) insertRecord(ctx context.Context, record apiKeyCreateRecord) (uuid.UUID, error) {
	if s.db == nil {
		return uuid.Nil, fmt.Errorf("api key db not configured")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return uuid.Nil, err
	}
	var userIDArg any
	if record.UserID != uuid.Nil {
		userIDArg = record.UserID
	}
	var expiresAt any
	if !record.ExpiresAt.IsZero() {
		expiresAt = record.ExpiresAt
	}
	var keyID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO api_keys (
			tenant_id, user_id, name, key_hash, key_prefix, scopes, rate_limit_rpm, expires_at, idempotency_key
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, record.TenantID, userIDArg, record.Name, record.KeyHash, record.KeyPrefix, record.Scopes, record.RateLimitRPM, expiresAt, record.IdempotencyKey).Scan(&keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, errAPIKeyReplayExpired
		}
		return uuid.Nil, fmt.Errorf("insert api key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return keyID, nil
}

func (s *apiKeyStore) getReplay(ctx context.Context, tenantID uuid.UUID, idempotencyKey string) (*authv1.CreateAPIKeyResponse, error) {
	if s.cache == nil {
		return nil, ports.ErrNotFound
	}
	raw, err := s.cache.Get(ctx, apiKeyReplayCacheKey(tenantID, idempotencyKey))
	if err != nil {
		return nil, err
	}
	record, err := s.openReplay(raw)
	if err != nil {
		return nil, fmt.Errorf("api key idempotency replay decrypt: %w", err)
	}
	return &authv1.CreateAPIKeyResponse{
		KeyId:     record.KeyID,
		KeyValue:  record.KeyValue,
		KeyPrefix: record.KeyPrefix,
	}, nil
}

func (s *apiKeyStore) putReplay(ctx context.Context, tenantID uuid.UUID, idempotencyKey string, resp *authv1.CreateAPIKeyResponse) error {
	if s.cache == nil {
		return fmt.Errorf("api key idempotency replay cache required")
	}
	value, err := s.sealReplay(apiKeyReplayRecord{
		KeyID:     resp.GetKeyId(),
		KeyValue:  resp.GetKeyValue(),
		KeyPrefix: resp.GetKeyPrefix(),
	})
	if err != nil {
		return err
	}
	return s.cache.Set(ctx, apiKeyReplayCacheKey(tenantID, idempotencyKey), value, apiKeyReplayTTL)
}

func (s *apiKeyStore) sealReplay(record apiKeyReplayRecord) ([]byte, error) {
	plaintext, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.replayKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *apiKeyStore) openReplay(value []byte) (apiKeyReplayRecord, error) {
	block, err := aes.NewCipher(s.replayKey[:])
	if err != nil {
		return apiKeyReplayRecord{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return apiKeyReplayRecord{}, err
	}
	if len(value) < gcm.NonceSize() {
		return apiKeyReplayRecord{}, fmt.Errorf("ciphertext too short")
	}
	nonce := value[:gcm.NonceSize()]
	ciphertext := value[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return apiKeyReplayRecord{}, err
	}
	var record apiKeyReplayRecord
	if err := json.Unmarshal(plaintext, &record); err != nil {
		return apiKeyReplayRecord{}, err
	}
	if record.KeyID == "" || record.KeyValue == "" || record.KeyPrefix == "" {
		return apiKeyReplayRecord{}, fmt.Errorf("incomplete replay record")
	}
	return record, nil
}

func deriveAPIKeyReplayKey(materials ...[]byte) [32]byte {
	hash := sha256.New()
	wrote := false
	for _, material := range materials {
		material = []byte(strings.TrimSpace(string(material)))
		if len(material) == 0 {
			continue
		}
		_, _ = hash.Write(material)
		wrote = true
	}
	if wrote {
		var key [32]byte
		copy(key[:], hash.Sum(nil))
		return key
	}
	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return sha256.Sum256([]byte("ani-auth-api-key-replay-fallback"))
	}
	return key
}

func apiKeyReplayCacheKey(tenantID uuid.UUID, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(tenantID.String() + "\x00" + strings.TrimSpace(idempotencyKey)))
	return "auth:api-key:create:" + hex.EncodeToString(sum[:])
}

func (s *apiKeyStore) list(ctx context.Context, req *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil || tenantID == uuid.Nil {
		return nil, fmt.Errorf("invalid tenant_id")
	}
	var userID uuid.UUID
	if req.GetUserId() != "" {
		userID, err = uuid.Parse(req.GetUserId())
		if err != nil || userID == uuid.Nil {
			return nil, fmt.Errorf("invalid user_id")
		}
	}
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID, UserID: userID})
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT id, name, key_prefix, scopes, rate_limit_rpm, created_at, expires_at, last_used_at,
			revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW()) AS is_active
		FROM api_keys
		WHERE tenant_id=$1
	`
	args := []any{tenantID}
	if userID != uuid.Nil {
		query += " AND user_id=$2"
		args = append(args, userID)
	}
	query += " ORDER BY created_at DESC"

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	resp := &authv1.ListAPIKeysResponse{}
	for rows.Next() {
		var info authv1.APIKeyInfo
		var id uuid.UUID
		var createdAt time.Time
		var expiresAt pgtype.Timestamptz
		var lastUsedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &info.Name, &info.KeyPrefix, &info.Scopes, &info.RateLimitRpm, &createdAt, &expiresAt, &lastUsedAt, &info.IsActive); err != nil {
			return nil, err
		}
		info.Id = id.String()
		info.CreatedAt = timestamppb.New(createdAt)
		if expiresAt.Valid {
			info.ExpiresAt = timestamppb.New(expiresAt.Time)
		}
		if lastUsedAt.Valid {
			info.LastUsedAt = timestamppb.New(lastUsedAt.Time)
		}
		resp.Keys = append(resp.Keys, &info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *apiKeyStore) revoke(ctx context.Context, req *authv1.RevokeAPIKeyRequest) error {
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil || tenantID == uuid.Nil {
		return fmt.Errorf("invalid tenant_id")
	}
	keyID, err := uuid.Parse(req.GetKeyId())
	if err != nil || keyID == uuid.Nil {
		return fmt.Errorf("invalid key_id")
	}
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at=COALESCE(revoked_at, NOW())
		WHERE tenant_id=$1 AND id=$2
	`, tenantID, keyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return types.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *apiKeyStore) validate(ctx context.Context, rawKey string) (*apiKeyPrincipal, error) {
	tenantID, err := parseAPIKeyTenant(rawKey)
	if err != nil {
		return nil, err
	}
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, err
	}

	var principal apiKeyPrincipal
	var userID pgtype.UUID
	var rateLimitRPM int32
	err = tx.QueryRow(ctx, `
		SELECT tenant_id, user_id, scopes, rate_limit_rpm
		FROM api_keys
		WHERE tenant_id=$1
		  AND key_hash=$2
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, tenantID, hashAPIKey(rawKey)).Scan(&principal.TenantID, &userID, &principal.Scopes, &rateLimitRPM)
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		principal.UserID = uuid.UUID(userID.Bytes)
	}
	keyHash := hashAPIKey(rawKey)
	if err := s.enforceRateLimit(ctx, keyHash, rateLimitRPM); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE api_keys SET last_used_at=NOW()
		WHERE tenant_id=$1 AND key_hash=$2
	`, tenantID, keyHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &principal, nil
}

func normalizeAPIKeyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("name required")
	}
	if len(name) > maxAPIKeyNameLength {
		return "", fmt.Errorf("name too long")
	}
	return name, nil
}

func normalizeAPIKeyExpiresAt(value *timestamppb.Timestamp, now time.Time) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if err := value.CheckValid(); err != nil {
		return time.Time{}, fmt.Errorf("invalid expires_at")
	}
	expiresAt := value.AsTime()
	if !expiresAt.After(now) {
		return time.Time{}, fmt.Errorf("expires_at must be in the future")
	}
	return expiresAt, nil
}

func normalizeAPIKeyRateLimit(value int32) (int32, error) {
	if value <= 0 {
		return defaultAPIKeyRateLimitRPM, nil
	}
	if value > maxAPIKeyRateLimitRPM {
		return 0, fmt.Errorf("rate_limit_rpm too high")
	}
	return value, nil
}

func (s *apiKeyStore) enforceRateLimit(ctx context.Context, keyHash string, limitRPM int32) error {
	if s == nil || s.cache == nil || limitRPM <= 0 {
		return nil
	}
	count, err := s.cache.Increment(ctx, "api-key:rate:"+keyHash, time.Minute)
	if err != nil {
		return fmt.Errorf("api key rate limit check: %w", err)
	}
	if count > int64(limitRPM) {
		return errAPIKeyRateLimitExceeded
	}
	return nil
}

func generateAPIKey(tenantID uuid.UUID) (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(randomBytes)
	return "ani_" + apiKeyEnv + "_" + tenantID.String() + "_" + secret, nil
}

func parseAPIKeyTenant(rawKey string) (uuid.UUID, error) {
	parts := strings.SplitN(rawKey, "_", 4)
	if len(parts) != 4 || parts[0] != "ani" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return uuid.Nil, fmt.Errorf("invalid api key format")
	}
	tenantID, err := uuid.Parse(parts[2])
	if err != nil || tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid api key tenant")
	}
	return tenantID, nil
}

func hashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

func prefixAPIKey(rawKey string) string {
	if len(rawKey) <= 24 {
		return rawKey
	}
	return rawKey[:24]
}

func normalizeAPIKeyScopes(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("at least one api key scope is required")
	}
	seen := map[string]struct{}{}
	scopes := make([]string, 0, len(input))
	for _, raw := range input {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			return nil, fmt.Errorf("api key scope cannot be empty")
		}
		parts := strings.Split(scope, ":")
		if len(parts) == 3 {
			if parts[0] != "scope" {
				return nil, fmt.Errorf("invalid api key scope %q", raw)
			}
			parts = parts[1:]
		}
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid api key scope %q", raw)
		}
		resource := strings.TrimSpace(parts[0])
		action := strings.TrimSpace(parts[1])
		if !validScopePart(resource) || !validScopePart(action) {
			return nil, fmt.Errorf("invalid api key scope %q", raw)
		}
		normalized := "scope:" + resource + ":" + action
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		scopes = append(scopes, normalized)
	}
	return scopes, nil
}

func validScopePart(value string) bool {
	if value == "*" {
		return true
	}
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func rollbackTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}
