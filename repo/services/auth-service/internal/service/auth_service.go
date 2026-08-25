package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/adapters/postgres"
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
	permissions   PermissionStore
	refreshTokens refreshTokenStore
	blocklist     tokenBlocklist
	oidc          *oidcLoginManager
	passwordLogin *passwordLoginManager
	platformLogin *platformLoginManager
	mintSecrets   map[string]string
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
		apiKeys:       newAPIKeyStore(db, cache),
		permissions:   newPermissionStore(db),
		refreshTokens: newRefreshTokenStore(db),
		blocklist:     blocklist,
		oidc:          newOIDCLoginManager(cache, jwtCfg, newOIDCSessionStore(db, newOIDCGroupRoleMapper(jwtCfg.OIDCGroupRoleMapJSON)), issuer),
		passwordLogin: newPasswordLoginManager(postgres.NewPasswordLoginStore(db), issuer),
		platformLogin: newPlatformLoginManager(postgres.NewPlatformLoginStore(db), issuer),
	}
}

func (s *AuthService) WithMintCredentials(raw string) *AuthService {
	s.mintSecrets = parseMintCredentials(raw)
	return s
}

func (s *AuthService) Register(server *grpc.Server) {
	authv1.RegisterAuthServiceServer(server, s)
}

func (s *AuthService) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.TokenPair, error) {
	if s == nil || s.passwordLogin == nil {
		return nil, status.Error(codes.Unimplemented, "login is not configured")
	}
	return s.passwordLogin.Login(ctx, req.GetTenantName(), req.GetUsername(), req.GetPassword())
}

func (s *AuthService) PlatformPasswordLogin(ctx context.Context, req *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error) {
	if s == nil || s.platformLogin == nil {
		return nil, status.Error(codes.Unimplemented, "platform login is not configured")
	}
	return s.platformLogin.Login(ctx, req.GetUsername(), req.GetPassword())
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
	// 按 scope 分流：平台 refresh token (tenant_id IS NULL) 续期为平台 access token，
	// 租户 refresh token 续期为租户 access token。混用会让平台 token 降级为
	// scope=tenant + tid=零值 UUID，被 jwt.go 校验拒绝。
	var token string
	if principal.Scope == "platform" {
		token, err = s.issuer.IssuePlatformAccessToken(
			platformPrincipal{UserID: principal.UserID, Roles: principal.Roles},
			defaultAccessTokenTTL,
		)
	} else {
		token, err = s.issuer.IssueAccessToken(principal, defaultAccessTokenTTL)
	}
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
		principal, err := s.apiKeys.validateWithOptions(ctx, req.GetToken(), apiKeyValidationOptions{
			EnforceRateLimit: true, TouchLastUsed: true,
		})
		if err != nil {
			if errors.Is(err, errAPIKeyRateLimitExceeded) {
				return nil, status.Error(codes.ResourceExhausted, "api key rate limit exceeded")
			}
			return nil, status.Error(codes.Unauthenticated, "invalid api key")
		}
		return &commonv1.TenantContext{
			TenantId: principal.TenantID.String(),
			UserId:   uuidString(principal.UserID),
			Roles:    append([]string{"service-account"}, principal.Permissions...),
			Scope:    "tenant",
		}, nil
	}
	if s.jwt == nil {
		return nil, status.Error(codes.FailedPrecondition, "jwt validator is not configured")
	}
	claims, err := s.jwt.Validate(ctx, req.GetToken())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	// legacy 路径只读 Legacy projection；不读取 V2 的 credential_domain。
	scope := claims.Legacy.Scope
	if scope == "" {
		scope = "tenant"
	}
	// platform token 的 tenant_id 为空串，旧 wire 行为保持零 UUID。
	tenantID := claims.Principal.TenantID
	if claims.Principal.Domain == "platform" || tenantID == "" {
		tenantID = uuid.Nil.String()
	}
	// 旧 wire 契约要求 TenantContext.UserId 是合法 UUID（Gateway 侧 uuid.Parse fail closed）。
	// V2 service principal 的 SubjectID 是服务名（如 inference-service，非 UUID），
	// legacy 投影必须回填 service actor UUID；user principal 的 SubjectID 本就是 UUID，直接透传。
	legacyUserID := claims.Principal.SubjectID
	if claims.Principal.Kind == "service" {
		legacyUserID = serviceActorUserID.String()
	}
	return &commonv1.TenantContext{
		TenantId: tenantID,
		UserId:   legacyUserID,
		Roles:    claims.Legacy.Roles,
		Scope:    scope,
	}, nil
}

// credentialValidationStatus 将 credential 校验错误分类为固定 gRPC code；
// 错误文本只描述类别，不拼接 credential、JWT payload、API Key hash 或 SQL。
func credentialValidationStatus(err error) error {
	switch {
	case errors.Is(err, errAPIKeyRateLimitExceeded):
		return status.Error(codes.ResourceExhausted, "credential rate limit exceeded")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "credential validation deadline exceeded")
	case errors.Is(err, errInvalidJWT), errors.Is(err, pgx.ErrNoRows):
		// expired/revoked/API Key hash miss 均不暴露具体原因。
		return status.Error(codes.Unauthenticated, "invalid credential")
	case errors.Is(err, errInvalidAPIKeyFormat):
		// 客户端提交的 API Key 格式非法属于无效凭证（401），
		// 不是后端故障，不能落入 default 被误映射为 503。
		return status.Error(codes.Unauthenticated, "invalid credential")
	default:
		// blocklist/cache/DB 等依赖错误不能伪装成无效凭证。
		return status.Error(codes.Unavailable, "credential backend unavailable")
	}
}

// ValidatePrincipal 是 V2 入口：校验 raw credential 并返回权威 PrincipalContext。
// API Key 走一次完整副作用（rate limit + last_used）；bearer 走 JWT 校验 + V2 域校验。
func (s *AuthService) ValidatePrincipal(ctx context.Context, req *authv1.ValidatePrincipalRequest) (*authv1.PrincipalContext, error) {
	credential := req.GetCredential()
	if credential == "" {
		return nil, status.Error(codes.Unauthenticated, "credential required")
	}
	switch req.GetCredentialScheme() {
	case "api_key":
		principal, err := s.apiKeys.validateWithOptions(ctx, credential, apiKeyValidationOptions{
			EnforceRateLimit: true, TouchLastUsed: true,
		})
		if err != nil {
			return nil, credentialValidationStatus(err)
		}
		return apiKeyPrincipalContext(principal)
	case "bearer":
		if s.jwt == nil {
			return nil, status.Error(codes.Unavailable, "credential backend unavailable")
		}
		claims, err := s.jwt.Validate(ctx, credential)
		if err != nil {
			return nil, credentialValidationStatus(err)
		}
		principal, err := jwtPrincipalContext(claims)
		if err != nil {
			// 签名通过但 Principal claim 组合非法，仍属于无效 credential；
			// 不允许裸 error 变成 gRPC Unknown 后被 Gateway 误映射为 503。
			return nil, status.Error(codes.Unauthenticated, "invalid credential")
		}
		// V2 路径额外校验：CredentialDomain 非空。
		// 旧 JWT 缺 credential_domain 时 Domain 为空，V2 路径 fail；
		// legacy 路径（ValidateToken）不校验 Domain，只读 Legacy.Scope，不受影响。
		if claims.Principal.Domain == "" {
			return nil, status.Error(codes.Unauthenticated, "invalid credential")
		}
		// service 必须携带非空且格式正确的 permissions。
		// user 的权限由数据库权威读取，不校验 JWT 中的 permissions。
		if claims.Principal.Kind == "service" && !validPermissionScopeClaims(claims.Principal.Permissions) {
			return nil, status.Error(codes.Unauthenticated, "invalid credential")
		}
		return principal, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported credential scheme")
	}
}

// validateCredentialForAuthorization 是 CheckPermissionV2 的 credential 重验路径，
// 复用与 ValidatePrincipal 相同的错误分类，但不执行 rate-limit/last-used 副作用。
func (s *AuthService) validateCredentialForAuthorization(
	ctx context.Context, scheme, credential string,
) (*principalRecord, error) {
	switch scheme {
	case "api_key":
		value, err := s.apiKeys.validateWithOptions(ctx, credential, apiKeyValidationOptions{})
		if err != nil {
			return nil, credentialValidationStatus(err)
		}
		subjectID := ""
		if value.UserID != uuid.Nil {
			subjectID = value.UserID.String()
		}
		return &principalRecord{
			Kind: "api_key", CredentialScheme: "api_key", Domain: "tenant",
			TenantID: value.TenantID.String(), SubjectID: subjectID,
			CredentialID: value.CredentialID.String(), Permissions: append([]string(nil), value.Permissions...),
		}, nil
	case "bearer":
		if s.jwt == nil {
			return nil, status.Error(codes.Unavailable, "credential backend unavailable")
		}
		value, err := s.jwt.Validate(ctx, credential)
		if err != nil {
			return nil, credentialValidationStatus(err)
		}
		return &value.Principal, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported credential scheme")
	}
}

// samePrincipal 比较 Gateway 自报 Principal 与重验 Principal 的 6 个字段，防止伪造。
func samePrincipal(want, got *authv1.PrincipalContext) bool {
	return want.GetPrincipalKind() == got.GetPrincipalKind() &&
		want.GetCredentialScheme() == got.GetCredentialScheme() &&
		want.GetCredentialDomain() == got.GetCredentialDomain() &&
		want.GetTenantId() == got.GetTenantId() &&
		want.GetSubjectId() == got.GetSubjectId() &&
		want.GetCredentialId() == got.GetCredentialId()
}

// validateRequiredBoundary 校验 boundary 是否为合法枚举值。
// 未知 boundary 代表 Gateway/Auth 契约错误，返回 InvalidArgument。
func validateRequiredBoundary(boundary string) error {
	switch strings.TrimSpace(boundary) {
	case "own", "tenant", "platform":
		return nil
	default:
		return errInvalidAuthorizationBoundary
	}
}

// principalDomainAllowsBoundary 校验重验后的 Principal domain 是否允许进入 required boundary。
// 使用重验后的 Principal 判断，不使用 Gateway 自报的身份信息。
// "允许继续判断"不等于授权成功，后面还要查 permission。
func principalDomainAllowsBoundary(principal principalRecord, requiredBoundary string) bool {
	switch requiredBoundary {
	case "platform":
		return principal.Domain == "platform" && principal.TenantID == ""
	case "tenant", "own":
		return principal.Domain == "tenant" && principal.TenantID != ""
	default:
		return false
	}
}

// CheckPermissionV2 是 V2 授权入口：重验 credential、比较 Principal、
// 校验 domain/boundary 后查询权威 permission 并返回 decision。
func (s *AuthService) CheckPermissionV2(ctx context.Context, req *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error) {
	// 1. 校验请求结构：7 字段非空，用 TrimSpace 防空白。
	if req.GetPrincipal() == nil ||
		strings.TrimSpace(req.GetResource()) == "" ||
		strings.TrimSpace(req.GetAction()) == "" ||
		strings.TrimSpace(req.GetRequiredBoundary()) == "" ||
		strings.TrimSpace(req.GetOperationId()) == "" ||
		strings.TrimSpace(req.GetCredential()) == "" ||
		strings.TrimSpace(req.GetCredentialScheme()) == "" {
		return nil, status.Error(codes.InvalidArgument, "authorization request incomplete")
	}

	// 2. 校验 boundary 是合法枚举值；未知值代表 Gateway/Auth 契约错误。
	if err := validateRequiredBoundary(req.GetRequiredBoundary()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "unsupported authorization boundary")
	}

	// 3. 重验 raw credential，读取权威状态。
	//    不重复执行 API Key rate-limit increment / last_used_at 等单次 HTTP 请求副作用。
	verifiedRecord, err := s.validateCredentialForAuthorization(
		ctx, req.GetCredentialScheme(), req.GetCredential(),
	)
	if err != nil {
		return nil, err
	}

	// 4. 比较 Gateway Principal 与重验 Principal，防止伪造。
	verified := verifiedRecord.Proto()
	if !samePrincipal(req.GetPrincipal(), verified) {
		return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "PRINCIPAL_MISMATCH"}, nil
	}

	// 5. 校验重验后的 Principal domain 是否允许 required boundary。
	//    domain mismatch 返回 deny decision（403），不是 gRPC error：
	//    credential 有效（不是 401），boundary 是合法值（不是 500），只是身份不允许进入该边界。
	if !principalDomainAllowsBoundary(*verifiedRecord, req.GetRequiredBoundary()) {
		return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "CREDENTIAL_DOMAIN_MISMATCH"}, nil
	}

	// 6. 读取权威 permission 并匹配 resource/action/permission scope。
	if s.permissions == nil {
		return nil, status.Error(codes.Unavailable, "authorization backend unavailable")
	}
	allowed, err := s.permissions.Allows(ctx, *verifiedRecord, req.GetResource(), req.GetAction(), req.GetRequiredBoundary())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "authorization deadline exceeded")
		}
		if errors.Is(err, errInvalidPermissionScope) {
			return nil, status.Error(codes.FailedPrecondition, "authoritative permission scope invalid")
		}
		return nil, status.Error(codes.Unavailable, "authorization backend unavailable")
	}

	// 7. 返回 decision。
	if !allowed {
		return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "PERMISSION_DENIED"}, nil
	}
	return &authv1.AuthorizationDecision{Allowed: true, ReasonCode: "ALLOWED"}, nil
}

func (s *AuthService) IssueServiceToken(_ context.Context, req *authv1.IssueServiceTokenRequest) (*authv1.AccessToken, error) {
	if s == nil || s.issuer == nil {
		return nil, status.Error(codes.FailedPrecondition, "service token issuer is not configured")
	}
	if len(s.mintSecrets) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "service token minting is not configured")
	}
	if !s.mintAllowed(req.GetCallerService(), req.GetCallerSecret()) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	// V2 签发映射：scope/permissions/credential_domain 统一解析为规范 claims，
	// 同时写入 V2 字段与 deprecated legacy projection。
	payload, err := resolveIssueServiceTokenClaims(req)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	token, err := s.issuer.IssueServiceTokenPayload(payload, ttl)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to issue service token")
	}
	if ttl <= 0 {
		ttl = defaultServiceTokenTTL
	}
	if ttl > maxServiceTokenTTL {
		ttl = maxServiceTokenTTL
	}
	return &authv1.AccessToken{AccessToken: token, ExpiresIn: int32(ttl.Seconds())}, nil
}

func (s *AuthService) CheckPermission(_ context.Context, req *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
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

func (s *AuthService) CreateAPIKey(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	resp, err := s.apiKeys.create(ctx, req)
	if err != nil {
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
		case "scope:*:*", "*:*", resource + ":" + action, "scope:" + resource + ":" + action:
			return true
		case resource + ":*", "scope:" + resource + ":*":
			return true
		}
	}
	return false
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

const (
	defaultAccessTokenTTL = time.Hour
	allowedMintCaller     = "inference-service"
)

func (s *AuthService) mintAllowed(name, secret string) bool {
	if strings.TrimSpace(name) != allowedMintCaller {
		return false
	}
	want := s.mintSecrets[allowedMintCaller]
	if want == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(secret)) == 1
}

func parseMintCredentials(raw string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, secret, ok := strings.Cut(item, ":")
		name = strings.TrimSpace(name)
		secret = strings.TrimSpace(secret)
		if !ok || name == "" || secret == "" {
			continue
		}
		out[name] = secret
	}
	return out
}

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
	case "get", "list", "read", "watch", "use", "create":
		return true
	default:
		return false
	}
}
