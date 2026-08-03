package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/types"
)

type refreshTokenStore interface {
	Validate(ctx context.Context, rawToken string) (refreshPrincipal, error)
}

type refreshPrincipal struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Roles    []string
	// Scope 推断自 tenant_id：NULL→"platform"，非 NULL→"tenant"。
	// RefreshToken RPC 按此分流到 IssuePlatformAccessToken / IssueAccessToken。
	Scope string
}

type persistentRefreshTokenStore struct {
	db *pgxpool.Pool
}

func newRefreshTokenStore(db *pgxpool.Pool) *persistentRefreshTokenStore {
	return &persistentRefreshTokenStore{db: db}
}

func (s *persistentRefreshTokenStore) Validate(ctx context.Context, rawToken string) (refreshPrincipal, error) {
	if rawToken == "" {
		return refreshPrincipal{}, types.ErrUnauthorized
	}
	if s.db == nil {
		return refreshPrincipal{}, errJWTNotConfigured
	}
	var principal refreshPrincipal
	err := s.db.QueryRow(ctx, `
		SELECT tenant_id, user_id, roles
		FROM refresh_tokens
		WHERE token_hash=$1
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
	`, hashRefreshToken(rawToken)).Scan(&principal.TenantID, &principal.UserID, &principal.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshPrincipal{}, types.ErrUnauthorized
	}
	if err != nil {
		return refreshPrincipal{}, fmt.Errorf("validate refresh token: %w", err)
	}
	// 推断 scope：tenant_id IS NULL → 平台 refresh token，否则租户。
	if principal.TenantID == uuid.Nil {
		principal.Scope = "platform"
	} else {
		principal.Scope = "tenant"
	}
	_, err = s.db.Exec(ctx, `
		UPDATE refresh_tokens
		SET last_used_at=NOW()
		WHERE token_hash=$1
	`, hashRefreshToken(rawToken))
	if err != nil {
		return refreshPrincipal{}, fmt.Errorf("touch refresh token: %w", err)
	}
	return principal, nil
}

func generateRefreshToken() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return "ani_refresh_" + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func hashRefreshToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

const defaultRefreshTokenTTL = 7 * 24 * time.Hour
