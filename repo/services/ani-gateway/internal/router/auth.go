package router

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type authAPI struct {
	client middleware.AuthClient
}

type authBeginOIDCRequest struct {
	TenantName  string `json:"tenant_name"`
	RedirectURI string `json:"redirect_uri"`
}

type authBeginOIDCResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

type authCompleteOIDCRequest struct {
	State       string `json:"state"`
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

type authPasswordLoginRequest struct {
	TenantName     string `json:"tenant_name"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type authPlatformPasswordLoginRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type authTokenPairResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int32  `json:"expires_in"`
	IssuedAt     string `json:"issued_at,omitempty"`
}

type authRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type authLogoutRequest struct {
	JTI string `json:"jti"`
}

type authAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int32  `json:"expires_in"`
}

type authCreateAPIKeyRequest struct {
	Name         string   `json:"name"`
	UserID       string   `json:"user_id,omitempty"`
	Scopes       []string `json:"scopes"`
	RateLimitRPM int32    `json:"rate_limit_rpm,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
}

type authCreateAPIKeyResponse struct {
	KeyID     string `json:"key_id"`
	KeyValue  string `json:"key_value"`
	KeyPrefix string `json:"key_prefix"`
}

type authAPIKeyInfoResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	KeyPrefix    string   `json:"key_prefix"`
	Scopes       []string `json:"scopes"`
	RateLimitRPM int32    `json:"rate_limit_rpm"`
	CreatedAt    string   `json:"created_at,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	LastUsedAt   string   `json:"last_used_at,omitempty"`
	IsActive     bool     `json:"is_active"`
}

type authListAPIKeysResponse struct {
	Items []authAPIKeyInfoResponse `json:"items"`
	Total int                      `json:"total"`
}

func registerAuth(v1 *route.RouterGroup) {
	api := authAPI{client: middleware.NewAuthClientFromEnv()}
	v1.POST("/auth/password/login", api.passwordLogin)
	v1.POST("/auth/platform/password/login", api.platformPasswordLogin)
	v1.POST("/auth/oidc/begin", api.beginOIDC)
	v1.POST("/auth/token", api.completeOIDC)
	v1.POST("/auth/refresh", api.refresh)
	v1.POST("/auth/logout", api.logout)
	v1.GET("/auth/api-keys", api.listAPIKeys)
	v1.POST("/auth/api-keys", api.createAPIKey)
	v1.DELETE("/auth/api-keys/:key_id", api.revokeAPIKey)
}

// 账号密码登录
func (api authAPI) passwordLogin(ctx context.Context, c *app.RequestContext) {
	var req authPasswordLoginRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid password login request")
		return
	}
	resp, httpStatus, errCode, message := api.passwordLoginHandler(ctx, req)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// 账号密码登录处理函数
func (api authAPI) passwordLoginHandler(ctx context.Context, req authPasswordLoginRequest) (authTokenPairResponse, int, string, string) {
	if strings.TrimSpace(req.TenantName) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "tenant_name required"
	}
	if strings.TrimSpace(req.Username) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "username required"
	}
	if req.Password == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "password required"
	}
	if api.client == nil {
		return authTokenPairResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	tokenPair, err := api.client.Login(ctx, &authv1.LoginRequest{
		TenantName: strings.TrimSpace(req.TenantName),
		Username:   strings.TrimSpace(req.Username),
		Password:   req.Password,
	})
	if err != nil {
		httpStatus, code, message := passwordLoginHTTPError(err)
		return authTokenPairResponse{}, httpStatus, code, message
	}
	return authTokenPairFromProto(tokenPair), http.StatusOK, "", ""
}

// 账号密码登录错误映射函数
func passwordLoginHTTPError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok || st == nil {
		return http.StatusBadGateway, "AUTH_SERVICE_ERROR", "auth service error"
	}
	switch st.Code() {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials"
	case codes.NotFound:
		return http.StatusNotFound, "TENANT_NOT_FOUND", "tenant not found"
	case codes.InvalidArgument:
		return http.StatusBadRequest, "BAD_REQUEST", st.Message()
	case codes.FailedPrecondition:
		return http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", st.Message()
	default:
		return http.StatusBadGateway, "AUTH_SERVICE_ERROR", "auth service error"
	}
}

// 平台账密登录
func (api authAPI) platformPasswordLogin(ctx context.Context, c *app.RequestContext) {
	var req authPlatformPasswordLoginRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid platform password login request")
		return
	}
	resp, httpStatus, errCode, message := api.platformPasswordLoginHandler(ctx, req)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// 平台账密登录处理函数
func (api authAPI) platformPasswordLoginHandler(ctx context.Context, req authPlatformPasswordLoginRequest) (authTokenPairResponse, int, string, string) {
	if strings.TrimSpace(req.Username) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "username required"
	}
	if req.Password == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "password required"
	}
	if api.client == nil {
		return authTokenPairResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	tokenPair, err := api.client.PlatformPasswordLogin(ctx, &authv1.PlatformPasswordLoginRequest{
		Username: strings.TrimSpace(req.Username),
		Password: req.Password,
	})
	if err != nil {
		httpStatus, code, message := platformLoginHTTPError(err)
		return authTokenPairResponse{}, httpStatus, code, message
	}
	return authTokenPairFromProto(tokenPair), http.StatusOK, "", ""
}

// 平台账密登录错误映射函数
func platformLoginHTTPError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok || st == nil {
		return http.StatusBadGateway, "AUTH_SERVICE_ERROR", "auth service error"
	}
	switch st.Code() {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials"
	case codes.InvalidArgument:
		return http.StatusBadRequest, "BAD_REQUEST", st.Message()
	case codes.FailedPrecondition:
		return http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", st.Message()
	default:
		return http.StatusBadGateway, "AUTH_SERVICE_ERROR", "auth service error"
	}
}

func (api authAPI) beginOIDC(ctx context.Context, c *app.RequestContext) {
	var req authBeginOIDCRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid oidc begin request")
		return
	}
	resp, httpStatus, errCode, message := api.beginOIDCLogin(ctx, req)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (api authAPI) completeOIDC(ctx context.Context, c *app.RequestContext) {
	var req authCompleteOIDCRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid oidc token request")
		return
	}
	resp, httpStatus, errCode, message := api.completeOIDCLogin(ctx, req)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (api authAPI) refresh(ctx context.Context, c *app.RequestContext) {
	var req authRefreshRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid refresh request")
		return
	}
	resp, httpStatus, errCode, message := api.refreshAccessToken(ctx, req.RefreshToken)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (api authAPI) logout(ctx context.Context, c *app.RequestContext) {
	var req authLogoutRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid logout request")
		return
	}
	_, httpStatus, errCode, message := api.revokeToken(ctx, req.JTI)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "revoked"})
}

func (api authAPI) createAPIKey(ctx context.Context, c *app.RequestContext) {
	var req authCreateAPIKeyRequest
	if err := c.BindJSON(&req); err != nil {
		writeAuthError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid api key request")
		return
	}
	resp, httpStatus, errCode, message := api.createAPIKeyForTenant(ctx, middleware.GetTenantID(c), middleware.GetUserID(c), req)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (api authAPI) listAPIKeys(ctx context.Context, c *app.RequestContext) {
	userID := strings.TrimSpace(c.Query("user_id"))
	resp, httpStatus, errCode, message := api.listAPIKeysForTenant(ctx, middleware.GetTenantID(c), userID)
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (api authAPI) revokeAPIKey(ctx context.Context, c *app.RequestContext) {
	_, httpStatus, errCode, message := api.revokeAPIKeyForTenant(ctx, middleware.GetTenantID(c), c.Param("key_id"))
	if errCode != "" {
		writeAuthError(c, httpStatus, errCode, message)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "revoked"})
}

func (api authAPI) beginOIDCLogin(ctx context.Context, req authBeginOIDCRequest) (authBeginOIDCResponse, int, string, string) {
	if strings.TrimSpace(req.TenantName) == "" {
		return authBeginOIDCResponse{}, http.StatusBadRequest, "BAD_REQUEST", "tenant_name required"
	}
	if strings.TrimSpace(req.RedirectURI) == "" {
		return authBeginOIDCResponse{}, http.StatusBadRequest, "BAD_REQUEST", "redirect_uri required"
	}
	if api.client == nil {
		return authBeginOIDCResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	resp, err := api.client.BeginOIDCLogin(ctx, &authv1.BeginOIDCLoginRequest{
		TenantName:  strings.TrimSpace(req.TenantName),
		RedirectUri: strings.TrimSpace(req.RedirectURI),
	})
	if err != nil {
		httpStatus, code, message := authHTTPError(err)
		return authBeginOIDCResponse{}, httpStatus, code, message
	}
	return authBeginOIDCResponse{
		AuthorizationURL: resp.GetAuthorizationUrl(),
		State:            resp.GetState(),
	}, http.StatusOK, "", ""
}

func (api authAPI) completeOIDCLogin(ctx context.Context, req authCompleteOIDCRequest) (authTokenPairResponse, int, string, string) {
	if strings.TrimSpace(req.State) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "state required"
	}
	if strings.TrimSpace(req.Code) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "code required"
	}
	if strings.TrimSpace(req.RedirectURI) == "" {
		return authTokenPairResponse{}, http.StatusBadRequest, "BAD_REQUEST", "redirect_uri required"
	}
	if api.client == nil {
		return authTokenPairResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	tokenPair, err := api.client.CompleteOIDCLogin(ctx, &authv1.CompleteOIDCLoginRequest{
		State:       strings.TrimSpace(req.State),
		Code:        strings.TrimSpace(req.Code),
		RedirectUri: strings.TrimSpace(req.RedirectURI),
	})
	if err != nil {
		httpStatus, code, message := authHTTPError(err)
		return authTokenPairResponse{}, httpStatus, code, message
	}
	return authTokenPairFromProto(tokenPair), http.StatusOK, "", ""
}

func (api authAPI) refreshAccessToken(ctx context.Context, refreshToken string) (authAccessTokenResponse, int, string, string) {
	if strings.TrimSpace(refreshToken) == "" {
		return authAccessTokenResponse{}, http.StatusBadRequest, "BAD_REQUEST", "refresh_token required"
	}
	if api.client == nil {
		return authAccessTokenResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	token, err := api.client.RefreshToken(ctx, refreshToken)
	if err != nil {
		httpStatus, code, message := authHTTPError(err)
		return authAccessTokenResponse{}, httpStatus, code, message
	}
	return authAccessTokenFromProto(token), http.StatusOK, "", ""
}

func (api authAPI) revokeToken(ctx context.Context, jti string) (struct{}, int, string, string) {
	if strings.TrimSpace(jti) == "" {
		return struct{}{}, http.StatusBadRequest, "BAD_REQUEST", "jti required"
	}
	if api.client == nil {
		return struct{}{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	if err := api.client.RevokeToken(ctx, strings.TrimSpace(jti)); err != nil {
		httpStatus, code, message := authHTTPError(err)
		return struct{}{}, httpStatus, code, message
	}
	return struct{}{}, http.StatusOK, "", ""
}

func (api authAPI) createAPIKeyForTenant(ctx context.Context, tenantID string, userID string, req authCreateAPIKeyRequest) (authCreateAPIKeyResponse, int, string, string) {
	if strings.TrimSpace(tenantID) == "" {
		return authCreateAPIKeyResponse{}, http.StatusForbidden, "FORBIDDEN", "tenant context missing"
	}
	if strings.TrimSpace(req.UserID) != "" {
		userID = req.UserID
	}
	if strings.TrimSpace(userID) == "" {
		return authCreateAPIKeyResponse{}, http.StatusBadRequest, "BAD_REQUEST", "user_id required"
	}
	if api.client == nil {
		return authCreateAPIKeyResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	expiresAt, httpStatus, errCode, message := parseOptionalTimestamp(req.ExpiresAt)
	if errCode != "" {
		return authCreateAPIKeyResponse{}, httpStatus, errCode, message
	}
	resp, err := api.client.CreateAPIKey(ctx, &authv1.CreateAPIKeyRequest{
		TenantId:     strings.TrimSpace(tenantID),
		UserId:       strings.TrimSpace(userID),
		Name:         req.Name,
		Scopes:       req.Scopes,
		RateLimitRpm: req.RateLimitRPM,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		httpStatus, code, message := authHTTPError(err)
		return authCreateAPIKeyResponse{}, httpStatus, code, message
	}
	return authCreateAPIKeyResponse{
		KeyID:     resp.GetKeyId(),
		KeyValue:  resp.GetKeyValue(),
		KeyPrefix: resp.GetKeyPrefix(),
	}, http.StatusCreated, "", ""
}

func (api authAPI) listAPIKeysForTenant(ctx context.Context, tenantID string, userID string) (authListAPIKeysResponse, int, string, string) {
	if strings.TrimSpace(tenantID) == "" {
		return authListAPIKeysResponse{}, http.StatusForbidden, "FORBIDDEN", "tenant context missing"
	}
	if api.client == nil {
		return authListAPIKeysResponse{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	resp, err := api.client.ListAPIKeys(ctx, &authv1.ListAPIKeysRequest{
		TenantId: strings.TrimSpace(tenantID),
		UserId:   strings.TrimSpace(userID),
	})
	if err != nil {
		httpStatus, code, message := authHTTPError(err)
		return authListAPIKeysResponse{}, httpStatus, code, message
	}
	items := make([]authAPIKeyInfoResponse, 0, len(resp.GetKeys()))
	for _, key := range resp.GetKeys() {
		items = append(items, authAPIKeyInfoFromProto(key))
	}
	return authListAPIKeysResponse{Items: items, Total: len(items)}, http.StatusOK, "", ""
}

func (api authAPI) revokeAPIKeyForTenant(ctx context.Context, tenantID string, keyID string) (struct{}, int, string, string) {
	if strings.TrimSpace(tenantID) == "" {
		return struct{}{}, http.StatusForbidden, "FORBIDDEN", "tenant context missing"
	}
	if strings.TrimSpace(keyID) == "" {
		return struct{}{}, http.StatusBadRequest, "BAD_REQUEST", "key_id required"
	}
	if api.client == nil {
		return struct{}{}, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "auth service unavailable"
	}
	if err := api.client.RevokeAPIKey(ctx, &authv1.RevokeAPIKeyRequest{
		TenantId: strings.TrimSpace(tenantID),
		KeyId:    strings.TrimSpace(keyID),
	}); err != nil {
		httpStatus, code, message := authHTTPError(err)
		return struct{}{}, httpStatus, code, message
	}
	return struct{}{}, http.StatusOK, "", ""
}

func authTokenPairFromProto(tokenPair *authv1.TokenPair) authTokenPairResponse {
	if tokenPair == nil {
		return authTokenPairResponse{}
	}
	return authTokenPairResponse{
		AccessToken:  tokenPair.GetAccessToken(),
		RefreshToken: tokenPair.GetRefreshToken(),
		ExpiresIn:    tokenPair.GetExpiresIn(),
		IssuedAt:     formatTimestamp(tokenPair.GetIssuedAt()),
	}
}

func authAccessTokenFromProto(token *authv1.AccessToken) authAccessTokenResponse {
	if token == nil {
		return authAccessTokenResponse{}
	}
	return authAccessTokenResponse{
		AccessToken: token.GetAccessToken(),
		ExpiresIn:   token.GetExpiresIn(),
	}
}

func authAPIKeyInfoFromProto(key *authv1.APIKeyInfo) authAPIKeyInfoResponse {
	if key == nil {
		return authAPIKeyInfoResponse{}
	}
	return authAPIKeyInfoResponse{
		ID:           key.GetId(),
		Name:         key.GetName(),
		KeyPrefix:    key.GetKeyPrefix(),
		Scopes:       key.GetScopes(),
		RateLimitRPM: key.GetRateLimitRpm(),
		CreatedAt:    formatTimestamp(key.GetCreatedAt()),
		ExpiresAt:    formatTimestamp(key.GetExpiresAt()),
		LastUsedAt:   formatTimestamp(key.GetLastUsedAt()),
		IsActive:     key.GetIsActive(),
	}
}

func parseOptionalTimestamp(value string) (*timestamppb.Timestamp, int, string, string) {
	if strings.TrimSpace(value) == "" {
		return nil, http.StatusOK, "", ""
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil, http.StatusBadRequest, "BAD_REQUEST", "expires_at must be RFC3339"
	}
	return timestamppb.New(parsed), http.StatusOK, "", ""
}

func formatTimestamp(value *timestamppb.Timestamp) string {
	if value == nil {
		return ""
	}
	return value.AsTime().UTC().Format(time.RFC3339)
}

func authHTTPError(err error) (int, string, string) {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHORIZED", status.Convert(err).Message()
	case codes.InvalidArgument:
		return http.StatusBadRequest, "BAD_REQUEST", status.Convert(err).Message()
	case codes.FailedPrecondition:
		return http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", status.Convert(err).Message()
	case codes.NotFound:
		return http.StatusNotFound, "NOT_FOUND", status.Convert(err).Message()
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", status.Convert(err).Message()
	default:
		return http.StatusBadGateway, "AUTH_SERVICE_ERROR", "auth service error"
	}
}

func writeAuthError(c *app.RequestContext, statusCode int, code string, message string) {
	c.JSON(statusCode, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
}
