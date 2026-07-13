package middleware

import (
	"context"
	"os"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
}

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
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
