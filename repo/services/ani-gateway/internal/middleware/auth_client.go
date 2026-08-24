package middleware

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type AuthClient interface {
	ValidateToken(ctx context.Context, token string) (*commonv1.TenantContext, error)
	CheckPermission(ctx context.Context, req *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error)
	BeginOIDCLogin(ctx context.Context, req *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error)
	CompleteOIDCLogin(ctx context.Context, req *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error)
	Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.TokenPair, error)
	PlatformPasswordLogin(ctx context.Context, req *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error)
	RefreshToken(ctx context.Context, refreshToken string) (*authv1.AccessToken, error)
	RevokeToken(ctx context.Context, jti string) error
	CreateAPIKey(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error)
	ListAPIKeys(ctx context.Context, req *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error)
	RevokeAPIKey(ctx context.Context, req *authv1.RevokeAPIKeyRequest) error

	// V2 鉴权契约：B1 只闭合接口，mode=off 下不被调用。
	ValidatePrincipal(ctx context.Context, credential string, scheme authz.CredentialScheme) (*authv1.PrincipalContext, error)
	CheckPermissionV2(ctx context.Context, req *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error)
}

var _ AuthClient = (*grpcAuthClient)(nil)

type grpcAuthClient struct {
	client  authv1.AuthServiceClient
	timeout time.Duration
}

func NewAuthClientFromEnv() AuthClient {
	addr := os.Getenv("AUTH_SERVICE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:9101"
	}
	timeout := 2 * time.Second
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	return &grpcAuthClient{
		client:  authv1.NewAuthServiceClient(conn),
		timeout: timeout,
	}
}

func (c *grpcAuthClient) ValidateToken(ctx context.Context, token string) (*commonv1.TenantContext, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.ValidateToken(callCtx, &authv1.ValidateTokenRequest{Token: token})
}

func (c *grpcAuthClient) CheckPermission(ctx context.Context, req *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.CheckPermission(callCtx, req)
}

func (c *grpcAuthClient) BeginOIDCLogin(ctx context.Context, req *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.BeginOIDCLogin(callCtx, req)
}

func (c *grpcAuthClient) CompleteOIDCLogin(ctx context.Context, req *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.CompleteOIDCLogin(callCtx, req)
}

func (c *grpcAuthClient) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.TokenPair, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Login(callCtx, req)
}

func (c *grpcAuthClient) PlatformPasswordLogin(ctx context.Context, req *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.PlatformPasswordLogin(callCtx, req)
}

func (c *grpcAuthClient) RefreshToken(ctx context.Context, refreshToken string) (*authv1.AccessToken, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.RefreshToken(callCtx, &authv1.RefreshTokenRequest{RefreshToken: refreshToken})
}

func (c *grpcAuthClient) RevokeToken(ctx context.Context, jti string) error {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.client.RevokeToken(callCtx, &authv1.RevokeTokenRequest{Jti: jti})
	return err
}

func (c *grpcAuthClient) CreateAPIKey(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.CreateAPIKey(callCtx, req)
}

func (c *grpcAuthClient) ListAPIKeys(ctx context.Context, req *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.ListAPIKeys(callCtx, req)
}

func (c *grpcAuthClient) RevokeAPIKey(ctx context.Context, req *authv1.RevokeAPIKeyRequest) error {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.client.RevokeAPIKey(callCtx, req)
	return err
}

func (c *grpcAuthClient) ValidatePrincipal(ctx context.Context, credential string, scheme authz.CredentialScheme) (*authv1.PrincipalContext, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.ValidatePrincipal(callCtx, &authv1.ValidatePrincipalRequest{
		Credential: credential, CredentialScheme: string(scheme),
	})
}

func (c *grpcAuthClient) CheckPermissionV2(ctx context.Context, req *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.CheckPermissionV2(callCtx, req)
}

// writeAuthRPCError 是 ValidatePrincipal/CheckPermissionV2 的固定错误映射表。
// 只记录 gRPC code / operation ID / decision reason code / request ID，
// 不得把 auth-service 原始错误文本直接返回 HTTP。
func writeAuthRPCError(c *app.RequestContext, err error) {
	switch status.Code(err) {
	case codes.Unauthenticated:
		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired credential")
	case codes.PermissionDenied:
		respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied")
	case codes.ResourceExhausted:
		respondError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "credential rate limit exceeded")
	case codes.DeadlineExceeded:
		respondError(c, http.StatusGatewayTimeout, "AUTHZ_DEADLINE_EXCEEDED", "authorization deadline exceeded")
	case codes.InvalidArgument:
		respondError(c, http.StatusInternalServerError, "AUTHZ_CONTRACT_ERROR", "authorization contract error")
	case codes.Unavailable, codes.FailedPrecondition:
		respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
	default:
		respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
	}
}
