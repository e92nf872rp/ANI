package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

// platformPrincipal carries platform-scoped identity for token issuance.
type platformPrincipal struct {
	UserID uuid.UUID
	Roles  []string
}

// platformLoginManager orchestrates platform admin password login using a
// ports.PlatformLoginStore for data access and a JWTIssuer for token issuance.
type platformLoginManager struct {
	store  ports.PlatformLoginStore
	issuer *JWTIssuer
	now    func() time.Time
}

func newPlatformLoginManager(store ports.PlatformLoginStore, issuer *JWTIssuer) *platformLoginManager {
	return &platformLoginManager{
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}
}

func (m *platformLoginManager) Login(ctx context.Context, username, password string) (*authv1.TokenPair, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "username and password required"))
	}
	if strings.Contains(username, ":") {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "username must not include namespace prefix"))
	}
	if m == nil || m.store == nil || m.issuer == nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "platform login is not configured"))
	}

	user, err := m.store.LookupUser(ctx, "local:"+username)
	if err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "user not found"))
	}
	if err := verifyPassword(user.PasswordHash, password); err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "password error"))
	}
	if user.Status != "active" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "user inactive"))
	}

	roles, err := m.store.LoadRoles(ctx, user.ID)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to load roles"))
	}

	// 先签发 access token（纯计算，不涉及 DB），成功后再持久化 refresh token。
	// 避免 IssuePlatformAccessToken 失败时 refresh token 已入库成为孤儿数据。
	accessToken, err := m.issuer.IssuePlatformAccessToken(platformPrincipal{UserID: user.ID, Roles: roles}, defaultAccessTokenTTL)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate access token"))
	}

	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate refresh token"))
	}
	if err := m.store.InsertRefreshToken(ctx, user.ID, hashRefreshToken(rawRefresh), roles, m.now().Add(defaultRefreshTokenTTL)); err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to insert refresh token"))
	}

	if err := m.store.TouchLastLogin(ctx, user.ID, m.now()); err != nil {
		_ = err
	}

	return &authv1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int32(defaultAccessTokenTTL.Seconds()),
		IssuedAt:     timestamppb.New(m.now()),
	}, nil
}
