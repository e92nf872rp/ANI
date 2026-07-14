package router

import (
	"context"
	"testing"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAuthRefreshAccessToken(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		refresh: &authv1.AccessToken{AccessToken: "access-1", ExpiresIn: 3600},
	}}

	resp, httpStatus, code, message := api.refreshAccessToken(context.Background(), "refresh-1")
	if code != "" || message != "" {
		t.Fatalf("refreshAccessToken error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
	if resp.AccessToken != "access-1" || resp.ExpiresIn != 3600 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAuthRefreshAccessTokenRejectsBlankToken(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, _ := api.refreshAccessToken(context.Background(), "  ")
	if httpStatus != 400 || code != "BAD_REQUEST" {
		t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
	}
}

func TestAuthRefreshAccessTokenMapsUnauthenticated(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		err: status.Error(codes.Unauthenticated, "invalid refresh token"),
	}}

	_, httpStatus, code, _ := api.refreshAccessToken(context.Background(), "refresh-1")
	if httpStatus != 401 || code != "UNAUTHORIZED" {
		t.Fatalf("status/code = %d/%s, want 401/UNAUTHORIZED", httpStatus, code)
	}
}

func TestAuthBeginOIDCLogin(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		beginOIDC: &authv1.BeginOIDCLoginResponse{
			AuthorizationUrl: "https://dex.example/auth?state=state-1",
			State:            "state-1",
		},
	}}

	resp, httpStatus, code, message := api.beginOIDCLogin(context.Background(), authBeginOIDCRequest{
		TenantName:  "tenant-a",
		RedirectURI: "https://console.example/callback",
	})
	if code != "" || message != "" {
		t.Fatalf("beginOIDCLogin error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
	if resp.AuthorizationURL == "" || resp.State != "state-1" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAuthBeginOIDCLoginRejectsMissingRedirectURI(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, _ := api.beginOIDCLogin(context.Background(), authBeginOIDCRequest{TenantName: "tenant-a"})
	if httpStatus != 400 || code != "BAD_REQUEST" {
		t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
	}
}

func TestAuthCompleteOIDCLogin(t *testing.T) {
	issuedAt := timestamppb.New(time.Date(2026, 5, 18, 8, 30, 0, 0, time.UTC))
	api := authAPI{client: fakeGatewayAuthClient{
		completeOIDC: &authv1.TokenPair{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			ExpiresIn:    3600,
			IssuedAt:     issuedAt,
		},
	}}

	resp, httpStatus, code, message := api.completeOIDCLogin(context.Background(), authCompleteOIDCRequest{
		State:       "state-1",
		Code:        "code-1",
		RedirectURI: "https://console.example/callback",
	})
	if code != "" || message != "" {
		t.Fatalf("completeOIDCLogin error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
	if resp.AccessToken != "access-1" || resp.RefreshToken != "refresh-1" || resp.IssuedAt != "2026-05-18T08:30:00Z" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAuthCompleteOIDCLoginRejectsMissingCode(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, _ := api.completeOIDCLogin(context.Background(), authCompleteOIDCRequest{
		State:       "state-1",
		RedirectURI: "https://console.example/callback",
	})
	if httpStatus != 400 || code != "BAD_REQUEST" {
		t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
	}
}

func TestAuthRevokeToken(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, message := api.revokeToken(context.Background(), "jti-1")
	if code != "" || message != "" {
		t.Fatalf("revokeToken error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
}

func TestAuthRevokeTokenRejectsBlankJTI(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, _ := api.revokeToken(context.Background(), " ")
	if httpStatus != 400 || code != "BAD_REQUEST" {
		t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
	}
}

func TestAuthCreateAPIKeyForTenant(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		create: &authv1.CreateAPIKeyResponse{
			KeyId:     "key-1",
			KeyValue:  "ani_dev_secret",
			KeyPrefix: "ani_dev",
		},
	}}

	resp, httpStatus, code, message := api.createAPIKeyForTenant(context.Background(), "tenant-1", "user-1", authCreateAPIKeyRequest{
		Name:         "ci",
		Scopes:       []string{"scope:models:create"},
		RateLimitRPM: 120,
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
	if code != "" || message != "" {
		t.Fatalf("createAPIKeyForTenant error = %s/%s", code, message)
	}
	if httpStatus != 201 {
		t.Fatalf("status = %d, want 201", httpStatus)
	}
	if resp.KeyID != "key-1" || resp.KeyValue == "" || resp.KeyPrefix != "ani_dev" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAuthCreateAPIKeyRejectsInvalidExpiresAt(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}

	_, httpStatus, code, _ := api.createAPIKeyForTenant(context.Background(), "tenant-1", "user-1", authCreateAPIKeyRequest{
		Name:      "ci",
		Scopes:    []string{"scope:models:create"},
		ExpiresAt: "tomorrow",
	})
	if httpStatus != 400 || code != "BAD_REQUEST" {
		t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
	}
}

func TestAuthListAPIKeysForTenant(t *testing.T) {
	createdAt := timestamppb.New(time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC))
	api := authAPI{client: fakeGatewayAuthClient{
		list: &authv1.ListAPIKeysResponse{Keys: []*authv1.APIKeyInfo{{
			Id:           "key-1",
			Name:         "ci",
			KeyPrefix:    "ani_dev",
			Scopes:       []string{"scope:models:create"},
			RateLimitRpm: 120,
			CreatedAt:    createdAt,
			IsActive:     true,
		}}},
	}}

	resp, httpStatus, code, message := api.listAPIKeysForTenant(context.Background(), "tenant-1", "")
	if code != "" || message != "" {
		t.Fatalf("listAPIKeysForTenant error = %s/%s", code, message)
	}
	if httpStatus != 200 || resp.Total != 1 {
		t.Fatalf("status/total = %d/%d, want 200/1", httpStatus, resp.Total)
	}
	if resp.Items[0].CreatedAt != "2026-05-18T08:00:00Z" {
		t.Fatalf("created_at = %q", resp.Items[0].CreatedAt)
	}
}

func TestAuthRevokeAPIKeyMapsNotFound(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		revokeKeyErr: status.Error(codes.NotFound, "api key not found"),
	}}

	_, httpStatus, code, message := api.revokeAPIKeyForTenant(context.Background(), "tenant-1", "missing")
	if httpStatus != 404 || code != "NOT_FOUND" || message != "api key not found" {
		t.Fatalf("status/code/message = %d/%s/%s, want 404/NOT_FOUND/api key not found", httpStatus, code, message)
	}
}

type fakeGatewayAuthClient struct {
	beginOIDC       *authv1.BeginOIDCLoginResponse
	completeOIDC    *authv1.TokenPair
	refresh         *authv1.AccessToken
	create          *authv1.CreateAPIKeyResponse
	list            *authv1.ListAPIKeysResponse
	err             error
	beginErr        error
	completeErr     error
	revokeErr       error
	createErr       error
	listErr         error
	revokeKeyErr    error
	login           *authv1.TokenPair
	loginErr        error
	platformLogin   *authv1.TokenPair
	platformLoginErr error
}

func (f fakeGatewayAuthClient) ValidateToken(context.Context, string) (*commonv1.TenantContext, error) {
	return nil, nil
}

func (f fakeGatewayAuthClient) CheckPermission(context.Context, *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
	return nil, nil
}

func (f fakeGatewayAuthClient) BeginOIDCLogin(context.Context, *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.beginOIDC, nil
}

func (f fakeGatewayAuthClient) CompleteOIDCLogin(context.Context, *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	if f.completeErr != nil {
		return nil, f.completeErr
	}
	return f.completeOIDC, nil
}

func (f fakeGatewayAuthClient) RefreshToken(context.Context, string) (*authv1.AccessToken, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.refresh, nil
}

func (f fakeGatewayAuthClient) RevokeToken(context.Context, string) error {
	return f.revokeErr
}

func (f fakeGatewayAuthClient) CreateAPIKey(context.Context, *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.create, nil
}

func (f fakeGatewayAuthClient) ListAPIKeys(context.Context, *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

func (f fakeGatewayAuthClient) RevokeAPIKey(context.Context, *authv1.RevokeAPIKeyRequest) error {
	return f.revokeKeyErr
}

func (f fakeGatewayAuthClient) Login(context.Context, *authv1.LoginRequest) (*authv1.TokenPair, error) {
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return f.login, nil
}

func (f fakeGatewayAuthClient) PlatformPasswordLogin(context.Context, *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error) {
	if f.platformLoginErr != nil {
		return nil, f.platformLoginErr
	}
	return f.platformLogin, nil
}

func TestPasswordLoginHandlerSuccess(t *testing.T) {
	issuedAt := timestamppb.New(time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC))
	api := authAPI{client: fakeGatewayAuthClient{
		login: &authv1.TokenPair{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			ExpiresIn:    3600,
			IssuedAt:     issuedAt,
		},
	}}

	resp, httpStatus, code, message := api.passwordLoginHandler(context.Background(), authPasswordLoginRequest{
		TenantName: "tenant-a",
		Username:   "alice",
		Password:   "correct",
	})
	if code != "" || message != "" {
		t.Fatalf("passwordLoginHandler error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
	if resp.AccessToken != "access-1" || resp.RefreshToken != "refresh-1" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.IssuedAt != "2026-07-06T08:00:00Z" {
		t.Fatalf("issued_at = %q", resp.IssuedAt)
	}
}

func TestPasswordLoginHandlerRejectsMissingFields(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}
	for _, tc := range []authPasswordLoginRequest{
		{Username: "alice", Password: "pwd"},
		{TenantName: "tenant-a", Password: "pwd"},
		{TenantName: "tenant-a", Username: "alice"},
	} {
		_, httpStatus, code, _ := api.passwordLoginHandler(context.Background(), tc)
		if httpStatus != 400 || code != "BAD_REQUEST" {
			t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
		}
	}
}

func TestPasswordLoginHandlerMapsInvalidCredentials(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		loginErr: status.Error(codes.Unauthenticated, "INVALID_CREDENTIALS"),
	}}
	_, httpStatus, code, _ := api.passwordLoginHandler(context.Background(), authPasswordLoginRequest{
		TenantName: "tenant-a",
		Username:   "alice",
		Password:   "wrong",
	})
	if httpStatus != 401 || code != "INVALID_CREDENTIALS" {
		t.Fatalf("status/code = %d/%s, want 401/INVALID_CREDENTIALS", httpStatus, code)
	}
}

func TestPasswordLoginHandlerMapsTenantNotFound(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		loginErr: status.Error(codes.NotFound, "TENANT_NOT_FOUND"),
	}}
	_, httpStatus, code, _ := api.passwordLoginHandler(context.Background(), authPasswordLoginRequest{
		TenantName: "missing",
		Username:   "alice",
		Password:   "pwd",
	})
	if httpStatus != 404 || code != "TENANT_NOT_FOUND" {
		t.Fatalf("status/code = %d/%s, want 404/TENANT_NOT_FOUND", httpStatus, code)
	}
}

func TestPlatformPasswordLoginHandlerSuccess(t *testing.T) {
	issuedAt := timestamppb.New(time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC))
	api := authAPI{client: fakeGatewayAuthClient{
		platformLogin: &authv1.TokenPair{
			AccessToken:  "platform-access-1",
			RefreshToken: "platform-refresh-1",
			ExpiresIn:    3600,
			IssuedAt:     issuedAt,
		},
	}}

	resp, httpStatus, code, message := api.platformPasswordLoginHandler(context.Background(), authPlatformPasswordLoginRequest{
		Username: "admin",
		Password: "correct",
	})
	if code != "" || message != "" {
		t.Fatalf("platformPasswordLoginHandler error = %s/%s", code, message)
	}
	if httpStatus != 200 {
		t.Fatalf("status = %d, want 200", httpStatus)
	}
	if resp.AccessToken != "platform-access-1" || resp.RefreshToken != "platform-refresh-1" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.IssuedAt != "2026-07-07T08:00:00Z" {
		t.Fatalf("issued_at = %q", resp.IssuedAt)
	}
}

func TestPlatformPasswordLoginHandlerRejectsMissingFields(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{}}
	for _, tc := range []authPlatformPasswordLoginRequest{
		{Password: "pwd"},
		{Username: "admin"},
	} {
		_, httpStatus, code, _ := api.platformPasswordLoginHandler(context.Background(), tc)
		if httpStatus != 400 || code != "BAD_REQUEST" {
			t.Fatalf("status/code = %d/%s, want 400/BAD_REQUEST", httpStatus, code)
		}
	}
}

func TestPlatformPasswordLoginHandlerMapsInvalidCredentials(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		platformLoginErr: status.Error(codes.Unauthenticated, "INVALID_CREDENTIALS"),
	}}
	_, httpStatus, code, _ := api.platformPasswordLoginHandler(context.Background(), authPlatformPasswordLoginRequest{
		Username: "admin",
		Password: "wrong",
	})
	if httpStatus != 401 || code != "INVALID_CREDENTIALS" {
		t.Fatalf("status/code = %d/%s, want 401/INVALID_CREDENTIALS", httpStatus, code)
	}
}

func TestPlatformPasswordLoginHandlerMapsBadGateway(t *testing.T) {
	api := authAPI{client: fakeGatewayAuthClient{
		platformLoginErr: status.Error(codes.Internal, "boom"),
	}}
	_, httpStatus, code, _ := api.platformPasswordLoginHandler(context.Background(), authPlatformPasswordLoginRequest{
		Username: "admin",
		Password: "pwd",
	})
	if httpStatus != 502 || code != "AUTH_SERVICE_ERROR" {
		t.Fatalf("status/code = %d/%s, want 502/AUTH_SERVICE_ERROR", httpStatus, code)
	}
}
