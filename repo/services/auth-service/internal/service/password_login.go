package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

// passwordLoginManager orchestrates tenant password login using a
// ports.PasswordLoginStore for data access and a JWTIssuer for token issuance.
type passwordLoginManager struct {
	store  ports.PasswordLoginStore
	issuer *JWTIssuer
	now    func() time.Time
}

func newPasswordLoginManager(store ports.PasswordLoginStore, issuer *JWTIssuer) *passwordLoginManager {
	return &passwordLoginManager{
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}
}

func (m *passwordLoginManager) Login(ctx context.Context, tenantName, username, password string) (*authv1.TokenPair, error) {
	tenantName = strings.TrimSpace(tenantName)
	username = strings.TrimSpace(username)
	if tenantName == "" || username == "" || password == "" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "tenant_name, username, and password required"))
	}
	if strings.Contains(username, ":") {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "username must not include namespace prefix"))
	}
	if m == nil || m.store == nil || m.issuer == nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "password login is not configured"))
	}

	tenantID, err := m.store.LookupTenant(ctx, tenantName)
	if err != nil {
		if errors.Is(err, ports.ErrTenantNotFound) {
			return nil, statusFromAuthError(newAuthError(ErrCodeTenantNotFound, "tenant not found"))
		}
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "tenant error"))
	}

	user, err := m.store.LookupUser(ctx, tenantID, "local:"+username)
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
	// 避免 IssueAccessToken 失败时 refresh token 已入库成为孤儿数据。
	accessToken, err := m.issuer.IssueAccessToken(refreshPrincipal{TenantID: tenantID, UserID: user.ID, Roles: roles}, defaultAccessTokenTTL)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate access token"))
	}

	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate refresh token"))
	}
	if err := m.store.InsertRefreshToken(ctx, tenantID, user.ID, hashRefreshToken(rawRefresh), roles, m.now().Add(defaultRefreshTokenTTL)); err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to insert refresh token"))
	}

	if err := m.store.TouchLastLogin(ctx, user.ID, m.now()); err != nil {
		_ = err // best-effort
	}

	return &authv1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int32(defaultAccessTokenTTL.Seconds()),
		IssuedAt:     timestamppb.New(m.now()),
	}, nil
}

func verifyPassword(hashed, password string) error {
	if hashed == "" {
		return errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password)); err != nil {
		return errors.New("invalid credentials")
	}
	return nil
}
