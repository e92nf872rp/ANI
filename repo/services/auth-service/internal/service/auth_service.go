package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type AuthService struct {
	authv1.UnimplementedAuthServiceServer
	jwt           *JWTValidator
	issuer        *JWTIssuer
	apiKeys       *apiKeyStore
	refreshTokens refreshTokenStore
	blocklist     tokenBlocklist
	oidc          *oidcLoginManager
	rolePerms     rolePermissionResolver
}

func NewAuthService(db *pgxpool.Pool, cache ports.CacheStore, jwtCfg JWTConfig) *AuthService {
	blocklist := newTokenBlocklist(db, cache)
	validator, err := NewJWTValidator(jwtCfg, blocklist)
	if err != nil && !errors.Is(err, errJWTNotConfigured) {
		validator = nil
	}
	issuer, err := NewJWTIssuer(jwtCfg)
	if err != nil && !errors.Is(err, errJWTNotConfigured) {
		issuer = nil
	}
	return &AuthService{
		jwt:           validator,
		issuer:        issuer,
		apiKeys:       newAPIKeyStore(db, cache, []byte(jwtCfg.PrivateKeyPEM), []byte(jwtCfg.OIDCClientSecret)),
		refreshTokens: newRefreshTokenStore(db),
		blocklist:     blocklist,
		oidc:          newOIDCLoginManager(cache, jwtCfg, newOIDCSessionStore(db, newOIDCGroupRoleMapper(jwtCfg.OIDCGroupRoleMapJSON)), issuer),
		rolePerms:     newDBRolePermissionResolver(db),
	}
}

func (s *AuthService) Register(server *grpc.Server) {
	authv1.RegisterAuthServiceServer(server, s)
}

func (s *AuthService) Login(context.Context, *authv1.LoginRequest) (*authv1.TokenPair, error) {
	return nil, status.Error(codes.Unimplemented, "login requires Dex/OIDC integration")
}

func (s *AuthService) BeginOIDCLogin(ctx context.Context, req *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	return s.oidc.Begin(ctx, req)
}

func (s *AuthService) CompleteOIDCLogin(ctx context.Context, req *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	return s.oidc.Complete(ctx, req)
}

func (s *AuthService) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.AccessToken, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token required")
	}
	if s.issuer == nil || s.refreshTokens == nil {
		return nil, status.Error(codes.FailedPrecondition, "refresh token flow is not configured")
	}
	principal, err := s.refreshTokens.Validate(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}
	token, err := s.issuer.IssueAccessToken(principal, defaultAccessTokenTTL)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to issue access token")
	}
	return &authv1.AccessToken{AccessToken: token, ExpiresIn: int32(defaultAccessTokenTTL.Seconds())}, nil
}

func (s *AuthService) RevokeToken(ctx context.Context, req *authv1.RevokeTokenRequest) (*emptypb.Empty, error) {
	if req.GetJti() == "" {
		return nil, status.Error(codes.InvalidArgument, "jti required")
	}
	if s.blocklist == nil {
		return nil, status.Error(codes.FailedPrecondition, "token revocation cache is not configured")
	}
	if err := s.blocklist.Revoke(ctx, req.GetJti(), defaultAccessTokenTTL); err != nil {
		return nil, status.Error(codes.Internal, "failed to revoke token")
	}
	return &emptypb.Empty{}, nil
}

func (s *AuthService) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*commonv1.TenantContext, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "token required")
	}
	if isAPIKey(req.GetToken()) {
		principal, err := s.apiKeys.validate(ctx, req.GetToken())
		if err != nil {
			if errors.Is(err, errAPIKeyRateLimitExceeded) {
				return nil, status.Error(codes.ResourceExhausted, "api key rate limit exceeded")
			}
			return nil, status.Error(codes.Unauthenticated, "invalid api key")
		}
		return &commonv1.TenantContext{
			TenantId: principal.TenantID.String(),
			UserId:   uuidString(principal.UserID),
			Roles:    append([]string{"service-account"}, principal.Scopes...),
		}, nil
	}
	if s.jwt == nil {
		return nil, status.Error(codes.FailedPrecondition, "jwt validator is not configured")
	}
	claims, err := s.jwt.Validate(ctx, req.GetToken())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &commonv1.TenantContext{
		TenantId: claims.TenantID.String(),
		UserId:   claims.UserID.String(),
		Roles:    claims.Roles,
	}, nil
}

func (s *AuthService) CheckPermission(ctx context.Context, req *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
	if req.GetTenantId() == "" {
		return deny("tenant_id required"), nil
	}
	if req.GetResource() == "" || req.GetAction() == "" {
		return deny("resource and action required"), nil
	}
	if hasRole(req.GetRoles(), "platform-admin") || hasRole(req.GetRoles(), "tenant-admin") {
		return allow(), nil
	}
	if hasScope(req.GetRoles(), req.GetResource(), req.GetAction()) {
		return allow(), nil
	}
	if s.rolePerms != nil {
		_, allowed, err := s.rolePerms.Allows(ctx, req.GetTenantId(), req.GetRoles(), req.GetResource(), req.GetAction())
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to resolve role permissions")
		}
		if allowed {
			return allow(), nil
		}
	}
	if hasRole(req.GetRoles(), "auditor") {
		if isReadAction(req.GetAction()) {
			return allow(), nil
		}
		return deny("auditor role is read-only"), nil
	}
	if hasRole(req.GetRoles(), "user") {
		if isUserAction(req.GetAction()) {
			return allow(), nil
		}
		return deny("user role cannot perform this action"), nil
	}
	return deny("no matching role or scope"), nil
}

type rolePermissionResolver interface {
	Allows(ctx context.Context, tenantID string, roles []string, resource string, action string) (resolved bool, allowed bool, err error)
}

type dbRolePermissionResolver struct {
	db *pgxpool.Pool
}

func newDBRolePermissionResolver(db *pgxpool.Pool) rolePermissionResolver {
	if db == nil {
		return nil
	}
	return &dbRolePermissionResolver{db: db}
}

func (r *dbRolePermissionResolver) Allows(ctx context.Context, tenantID string, roles []string, resource string, action string) (bool, bool, error) {
	if r == nil || r.db == nil || len(roles) == 0 {
		return false, false, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT permissions
		FROM roles
		WHERE name = ANY($1::text[])
		  AND (tenant_id IS NULL OR tenant_id = NULLIF($2, '')::uuid)
	`, roles, tenantID)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	resolved := false
	for rows.Next() {
		resolved = true
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return false, false, err
		}
		if permissionsAllow(raw, resource, action) {
			return true, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	return resolved, false, nil
}

type structuredPermission struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

func (s *AuthService) CreateAPIKey(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	resp, err := s.apiKeys.create(ctx, req)
	if err != nil {
		if errors.Is(err, errAPIKeyReplayExpired) {
			return nil, status.Error(codes.Aborted, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return resp, nil
}

func (s *AuthService) ListAPIKeys(ctx context.Context, req *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	resp, err := s.apiKeys.list(ctx, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return resp, nil
}

func (s *AuthService) RevokeAPIKey(ctx context.Context, req *authv1.RevokeAPIKeyRequest) (*emptypb.Empty, error) {
	if err := s.apiKeys.revoke(ctx, req); err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "api key not found")
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &emptypb.Empty{}, nil
}

func allow() *authv1.CheckPermissionResponse {
	return &authv1.CheckPermissionResponse{Allowed: true}
}

func deny(reason string) *authv1.CheckPermissionResponse {
	return &authv1.CheckPermissionResponse{Allowed: false, Reason: reason}
}

func hasRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func hasScope(roles []string, resource, action string) bool {
	for _, role := range roles {
		switch role {
		case "*", "scope:*:*", "*:*", resource + ":" + action, "scope:" + resource + ":" + action:
			return true
		case resource + ":*", "scope:" + resource + ":*":
			return true
		}
	}
	return false
}

func permissionsAllow(raw json.RawMessage, resource string, action string) bool {
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err == nil {
		return hasScope(scopes, resource, action) || hasActionAliasScope(scopes, resource, action)
	}
	var permissions []structuredPermission
	if err := json.Unmarshal(raw, &permissions); err != nil {
		return false
	}
	for _, permission := range permissions {
		if permission.Resource != "*" && permission.Resource != resource {
			continue
		}
		for _, allowedAction := range permission.Actions {
			if allowedAction == "*" || allowedAction == action || actionAliasMatches(allowedAction, action) {
				return true
			}
		}
	}
	return false
}

func hasActionAliasScope(scopes []string, resource string, action string) bool {
	if action != "get" {
		return false
	}
	return hasScope(scopes, resource, "read")
}

func actionAliasMatches(allowedAction string, action string) bool {
	return action == "get" && allowedAction == "read"
}

func isAPIKey(token string) bool {
	return len(token) > 4 && token[:4] == "ani_"
}

func jwtBlocklistKey(jti string) string {
	return "jwt:blocklist:" + jti
}

func uuidString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

const defaultAccessTokenTTL = time.Hour

func isReadAction(action string) bool {
	switch action {
	case "get", "list", "read", "watch":
		return true
	default:
		return false
	}
}

func isUserAction(action string) bool {
	switch action {
	case "get", "list", "read", "watch", "use", "create", "update", "delete",
		"start", "stop", "restart":
		return true
	default:
		return false
	}
}
