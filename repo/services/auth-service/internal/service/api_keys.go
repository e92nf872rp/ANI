package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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

var errAPIKeyRateLimitExceeded = errors.New("api key rate limit exceeded")

// errInvalidAPIKeyFormat 表示客户端提交的 API Key 格式非法（缺前缀/分段/tenant 非 UUID），
// 属于无效凭证（401），不能被错误分类为后端故障（503）。
var errInvalidAPIKeyFormat = errors.New("invalid api key format")

type apiKeyStore struct {
	db    *pgxpool.Pool
	cache ports.CacheStore
}

type apiKeyPrincipal struct {
	CredentialID uuid.UUID
	TenantID     uuid.UUID
	UserID       uuid.UUID
	Permissions  []string // 对应数据库 api_keys.scopes 列，V2 权限集合
}

func newAPIKeyStore(db *pgxpool.Pool, cache ports.CacheStore) *apiKeyStore {
	return &apiKeyStore{db: db, cache: cache}
}

func (s *apiKeyStore) create(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil || tenantID == uuid.Nil {
		return nil, fmt.Errorf("invalid tenant_id")
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

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, err
	}

	var keyID uuid.UUID
	var expiresAt any
	expiresAtTime, err := normalizeAPIKeyExpiresAt(req.GetExpiresAt(), time.Now())
	if err != nil {
		return nil, err
	}
	if !expiresAtTime.IsZero() {
		expiresAt = expiresAtTime
	}
	var userIDArg any
	if userID != uuid.Nil {
		userIDArg = userID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO api_keys (
			tenant_id, user_id, name, key_hash, key_prefix, scopes, rate_limit_rpm, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, tenantID, userIDArg, name, keyHash, keyPrefix, scopes, rateLimit, expiresAt).Scan(&keyID)
	if err != nil {
		return nil, fmt.Errorf("insert api key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &authv1.CreateAPIKeyResponse{
		KeyId:     keyID.String(),
		KeyValue:  rawKey,
		KeyPrefix: keyPrefix,
	}, nil
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

// apiKeyValidationOptions 控制一次 API Key 校验的副作用：
// ValidatePrincipal 用全开选项；CheckPermissionV2 重验全关，避免一次请求计两次 rate limit。
type apiKeyValidationOptions struct {
	EnforceRateLimit bool
	TouchLastUsed    bool
}

// loadActiveByRawCredential 在 RLS 事务内读取活跃 API Key，不含任何副作用。
func (s *apiKeyStore) loadActiveByRawCredential(ctx context.Context, rawKey string) (*apiKeyPrincipal, int32, error) {
	tenantID, err := parseAPIKeyTenant(rawKey)
	if err != nil {
		return nil, 0, err
	}
	// API Key 只能按 key 中解析出的 tenant 建立 RLS 上下文。
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackTx(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, 0, err
	}

	var principal apiKeyPrincipal
	var userID pgtype.UUID
	var rateLimitRPM int32
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, scopes, rate_limit_rpm
		FROM api_keys
		WHERE tenant_id=$1 AND key_hash=$2
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, tenantID, hashAPIKey(rawKey)).Scan(
		&principal.CredentialID, &principal.TenantID, &userID,
		&principal.Permissions, &rateLimitRPM,
	)
	if err != nil {
		return nil, 0, err
	}
	if userID.Valid {
		principal.UserID = uuid.UUID(userID.Bytes)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return &principal, rateLimitRPM, nil
}

// validateWithOptions 读取/重验 credential 并按选项执行单次请求副作用。
func (s *apiKeyStore) validateWithOptions(ctx context.Context, rawKey string, options apiKeyValidationOptions) (*apiKeyPrincipal, error) {
	principal, rateLimitRPM, err := s.loadActiveByRawCredential(ctx, rawKey)
	if err != nil {
		return nil, err
	}
	if options.EnforceRateLimit {
		if err := s.enforceRateLimit(ctx, hashAPIKey(rawKey), rateLimitRPM); err != nil {
			return nil, err
		}
	}
	if options.TouchLastUsed {
		if err := s.touchLastUsed(ctx, principal.TenantID, principal.CredentialID); err != nil {
			return nil, err
		}
	}
	return principal, nil
}

// touchLastUsed 在 RLS 事务内按 credential ID 更新 last_used_at；
// 跨 tenant 的 credential ID 因 RLS 命中 0 行，必须返回 ErrNoRows 而非静默跳过。
func (s *apiKeyStore) touchLastUsed(ctx context.Context, tenantID, credentialID uuid.UUID) error {
	if tenantID == uuid.Nil || credentialID == uuid.Nil {
		return errors.New("invalid api key identity")
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
		SET last_used_at = NOW()
		WHERE tenant_id = $1 AND id = $2
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, tenantID, credentialID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

// apiKeyPrincipalContext 将 apiKeyPrincipal 转为 V2 PrincipalContext；
// credential ID / tenant ID 任一缺失都 fail closed。
func apiKeyPrincipalContext(value *apiKeyPrincipal) (*authv1.PrincipalContext, error) {
	if value == nil || value.CredentialID == uuid.Nil || value.TenantID == uuid.Nil {
		return nil, errors.New("invalid api key principal")
	}
	subjectID := ""
	if value.UserID != uuid.Nil {
		subjectID = value.UserID.String()
	}
	return &authv1.PrincipalContext{
		PrincipalKind:    "api_key",
		CredentialScheme: "api_key",
		CredentialDomain: "tenant",
		TenantId:         value.TenantID.String(),
		SubjectId:        subjectID,
		CredentialId:     value.CredentialID.String(),
	}, nil
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
		return uuid.Nil, errInvalidAPIKeyFormat
	}
	tenantID, err := uuid.Parse(parts[2])
	if err != nil || tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: bad tenant", errInvalidAPIKeyFormat)
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
