package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type oidcLoginManager struct {
	cache       ports.CacheStore
	authURL     string
	clientID    string
	stateTTL    time.Duration
	statePrefix string
	exchanger   oidcCodeExchanger
	verifier    oidcIDTokenVerifier
	sessions    oidcSessionStore
	issuer      *JWTIssuer
	configErr   error
}

type oidcStateRecord struct {
	TenantName  string `json:"tenant_name"`
	RedirectURI string `json:"redirect_uri"`
	Nonce       string `json:"nonce"`
}

const oidcHTTPTimeout = 10 * time.Second
const oidcJWKSCacheTTL = 5 * time.Minute
const oidcTokenClockSkew = 2 * time.Minute
const oidcMinRSAKeyBits = 2048

func newOIDCLoginManager(cache ports.CacheStore, cfg JWTConfig, sessions oidcSessionStore, issuer *JWTIssuer) *oidcLoginManager {
	rawCfg := cfg
	cfg = withDexOIDCDefaults(cfg)
	var exchanger oidcCodeExchanger
	if cfg.OIDCTokenURL != "" && cfg.OIDCClientID != "" {
		exchanger = oidcHTTPExchanger{
			tokenURL:     cfg.OIDCTokenURL,
			clientID:     cfg.OIDCClientID,
			clientSecret: cfg.OIDCClientSecret,
			httpClient:   newOIDCHTTPClient(),
		}
	}
	verifier, err := newOIDCIDTokenVerifier(rawCfg)
	return &oidcLoginManager{
		cache:       cache,
		authURL:     cfg.OIDCAuthURL,
		clientID:    cfg.OIDCClientID,
		stateTTL:    10 * time.Minute,
		statePrefix: "oidc:state:",
		exchanger:   exchanger,
		verifier:    verifier,
		sessions:    sessions,
		issuer:      issuer,
		configErr:   err,
	}
}

func newOIDCHTTPClient() *http.Client {
	return &http.Client{Timeout: oidcHTTPTimeout}
}

func withDexOIDCDefaults(cfg JWTConfig) JWTConfig {
	issuer := strings.TrimRight(cfg.OIDCIssuerURL, "/")
	if issuer == "" {
		return cfg
	}
	cfg.OIDCIssuerURL = issuer
	if cfg.OIDCAuthURL == "" {
		cfg.OIDCAuthURL = issuer + "/auth"
	}
	if cfg.OIDCTokenURL == "" {
		cfg.OIDCTokenURL = issuer + "/token"
	}
	if cfg.OIDCJWKSURL == "" {
		cfg.OIDCJWKSURL = issuer + "/keys"
	}
	return cfg
}

func (m *oidcLoginManager) Begin(ctx context.Context, req *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	if req.GetTenantName() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_name required")
	}
	if req.GetRedirectUri() == "" {
		return nil, status.Error(codes.InvalidArgument, "redirect_uri required")
	}
	if err := validateOIDCRedirectURI(req.GetRedirectUri()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid redirect_uri")
	}
	if m != nil && m.configErr != nil {
		return nil, status.Error(codes.FailedPrecondition, "oidc login configuration is invalid")
	}
	if m == nil || m.cache == nil || m.authURL == "" || m.clientID == "" {
		return nil, status.Error(codes.FailedPrecondition, "oidc login is not configured")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create oidc state")
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create oidc nonce")
	}
	stateValue, err := json.Marshal(oidcStateRecord{
		TenantName:  req.GetTenantName(),
		RedirectURI: req.GetRedirectUri(),
		Nonce:       nonce,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encode oidc state")
	}
	if err := m.cache.Set(ctx, m.statePrefix+state, []byte(stateValue), m.stateTTL); err != nil {
		return nil, status.Error(codes.Internal, "failed to store oidc state")
	}
	authURL, err := m.authorizationURL(state, req.GetRedirectUri(), nonce)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "invalid oidc authorization url")
	}
	return &authv1.BeginOIDCLoginResponse{AuthorizationUrl: authURL, State: state}, nil
}

func (m *oidcLoginManager) Complete(ctx context.Context, req *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	if req.GetState() == "" || req.GetCode() == "" || req.GetRedirectUri() == "" {
		return nil, status.Error(codes.InvalidArgument, "state, code, and redirect_uri required")
	}
	if err := validateOIDCRedirectURI(req.GetRedirectUri()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid redirect_uri")
	}
	if m == nil || m.cache == nil {
		return nil, status.Error(codes.FailedPrecondition, "oidc login is not configured")
	}
	if m.configErr != nil {
		return nil, status.Error(codes.FailedPrecondition, "oidc login configuration is invalid")
	}
	value, err := m.cache.Get(ctx, m.statePrefix+req.GetState())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid oidc state")
	}
	stateRecord, err := decodeOIDCState(value)
	if err != nil || stateRecord.RedirectURI != req.GetRedirectUri() {
		return nil, status.Error(codes.Unauthenticated, "invalid oidc state")
	}
	if err := m.cache.Delete(ctx, m.statePrefix+req.GetState()); err != nil {
		return nil, status.Error(codes.Internal, "failed to consume oidc state")
	}
	if m.exchanger == nil || m.verifier == nil || m.sessions == nil || m.issuer == nil {
		return nil, status.Error(codes.FailedPrecondition, "oidc code exchange is not configured")
	}
	tokens, err := m.exchanger.Exchange(ctx, req.GetCode(), req.GetRedirectUri())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "oidc code exchange failed")
	}
	claims, err := m.verifier.Verify(ctx, tokens.IDToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid oidc id token")
	}
	if stateRecord.Nonce != "" && claims.Nonce != stateRecord.Nonce {
		return nil, status.Error(codes.Unauthenticated, "invalid oidc nonce")
	}
	principal, refreshToken, err := m.sessions.CreateSession(ctx, stateRecord.TenantName, claims)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "failed to create oidc session")
	}
	accessToken, err := m.issuer.IssueAccessToken(principal, defaultAccessTokenTTL)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to issue access token")
	}
	return &authv1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int32(defaultAccessTokenTTL.Seconds()),
		IssuedAt:     timestamppb.New(m.issuer.now()),
	}, nil
}

func decodeOIDCState(value []byte) (oidcStateRecord, error) {
	var record oidcStateRecord
	if err := json.Unmarshal(value, &record); err == nil && record.TenantName != "" && record.RedirectURI != "" {
		return record, nil
	}
	parts := strings.SplitN(string(value), "\n", 2)
	if len(parts) != 2 {
		return oidcStateRecord{}, errInvalidJWT
	}
	return oidcStateRecord{TenantName: parts[0], RedirectURI: parts[1]}, nil
}

func validateOIDCRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("redirect_uri must be absolute")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("redirect_uri must use http or https")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect_uri must not include a fragment")
	}
	return nil
}

func (m *oidcLoginManager) authorizationURL(state, redirectURI string, nonce string) (string, error) {
	parsed, err := url.Parse(m.authURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("client_id", m.clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid email profile groups")
	query.Set("state", state)
	if nonce != "" {
		query.Set("nonce", nonce)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func randomURLToken(size int) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("invalid token size")
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type oidcCodeExchanger interface {
	Exchange(ctx context.Context, code, redirectURI string) (oidcTokenResponse, error)
}

type oidcTokenResponse struct {
	IDToken string
}

type oidcHTTPExchanger struct {
	tokenURL     string
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

func (e oidcHTTPExchanger) Exchange(ctx context.Context, code, redirectURI string) (oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", e.clientID)
	if e.clientSecret != "" {
		form.Set("client_secret", e.clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return oidcTokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return oidcTokenResponse{}, closeErr
		}
		return oidcTokenResponse{}, fmt.Errorf("oidc token endpoint status %d", resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return oidcTokenResponse{}, readErr
	}
	if closeErr != nil {
		return oidcTokenResponse{}, closeErr
	}
	var decoded struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return oidcTokenResponse{}, err
	}
	if decoded.IDToken == "" {
		return oidcTokenResponse{}, fmt.Errorf("id_token missing")
	}
	return oidcTokenResponse{IDToken: decoded.IDToken}, nil
}

type oidcIDTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (oidcClaims, error)
}

type oidcClaims struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
	Nonce   string
}

type oidcStaticKeyVerifier struct {
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
	now       func() time.Time
}

func newOIDCIDTokenVerifier(cfg JWTConfig) (oidcIDTokenVerifier, error) {
	if cfg.OIDCJWKSURL == "" && (cfg.OIDCPublicKeyPEM != "" || cfg.OIDCPublicKeyFile != "") {
		return newOIDCStaticKeyVerifier(cfg)
	}
	cfg = withDexOIDCDefaults(cfg)
	if cfg.OIDCJWKSURL != "" && cfg.OIDCIssuerURL != "" && cfg.OIDCClientID != "" {
		return &oidcJWKSVerifier{
			jwksURL:    cfg.OIDCJWKSURL,
			issuer:     cfg.OIDCIssuerURL,
			audience:   cfg.OIDCClientID,
			httpClient: newOIDCHTTPClient(),
			now:        time.Now,
		}, nil
	}
	return newOIDCStaticKeyVerifier(cfg)
}

func newOIDCStaticKeyVerifier(cfg JWTConfig) (*oidcStaticKeyVerifier, error) {
	keyPEM := cfg.OIDCPublicKeyPEM
	if keyPEM == "" && cfg.OIDCPublicKeyFile != "" {
		data, err := os.ReadFile(cfg.OIDCPublicKeyFile)
		if err != nil {
			return nil, err
		}
		keyPEM = string(data)
	}
	if keyPEM == "" || cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, nil
	}
	key, err := parseRSAPublicKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &oidcStaticKeyVerifier{publicKey: key, issuer: cfg.OIDCIssuerURL, audience: cfg.OIDCClientID, now: time.Now}, nil
}

func (v *oidcStaticKeyVerifier) Verify(ctx context.Context, idToken string) (oidcClaims, error) {
	return verifyOIDCIDToken(ctx, idToken, v.issuer, v.audience, v.now, func(context.Context, string) (*rsa.PublicKey, error) {
		return v.publicKey, nil
	})
}

type oidcJWKSVerifier struct {
	jwksURL     string
	issuer      string
	audience    string
	httpClient  *http.Client
	now         func() time.Time
	cacheTTL    time.Duration
	cacheMu     sync.Mutex
	cachedKeys  map[string]*rsa.PublicKey
	cachedUntil time.Time
}

func (v *oidcJWKSVerifier) Verify(ctx context.Context, idToken string) (oidcClaims, error) {
	return verifyOIDCIDToken(ctx, idToken, v.issuer, v.audience, v.now, v.keyFor)
}

func (v *oidcJWKSVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, errInvalidJWT
	}
	now := v.now
	if now == nil {
		now = time.Now
	}
	cacheTTL := v.cacheTTL
	if cacheTTL <= 0 {
		cacheTTL = oidcJWKSCacheTTL
	}
	currentTime := now()
	v.cacheMu.Lock()
	defer v.cacheMu.Unlock()
	if currentTime.Before(v.cachedUntil) {
		if key := v.cachedKeys[kid]; key != nil {
			return key, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, errInvalidJWT
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return nil, err
	}
	v.cachedKeys = keys
	v.cachedUntil = currentTime.Add(cacheTTL)
	key := keys[kid]
	if key == nil {
		return nil, errInvalidJWT
	}
	return key, nil
}

func verifyOIDCIDToken(
	ctx context.Context,
	idToken string,
	issuer string,
	audience string,
	now func() time.Time,
	keyFor func(context.Context, string) (*rsa.PublicKey, error),
) (oidcClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return oidcClaims{}, errInvalidJWT
	}
	var header jwtHeader
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "RS256" {
		return oidcClaims{}, errInvalidJWT
	}
	publicKey, err := keyFor(ctx, header.Kid)
	if err != nil || publicKey == nil {
		return oidcClaims{}, errInvalidJWT
	}
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidcClaims{}, errInvalidJWT
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return oidcClaims{}, errInvalidJWT
	}
	var payload oidcIDTokenPayload
	if err := decodeSegment(parts[1], &payload); err != nil {
		return oidcClaims{}, errInvalidJWT
	}
	if now == nil {
		now = time.Now
	}
	currentTime := now()
	if payload.Issuer != issuer || payload.Subject == "" || !validOIDCTimeClaims(payload, currentTime) || !payload.Audience.Contains(audience) {
		return oidcClaims{}, errInvalidJWT
	}
	if payload.Email == "" {
		return oidcClaims{}, errInvalidJWT
	}
	return oidcClaims{Subject: payload.Subject, Email: payload.Email, Name: payload.Name, Groups: payload.Groups, Nonce: payload.Nonce}, nil
}

func validOIDCTimeClaims(payload oidcIDTokenPayload, now time.Time) bool {
	if payload.Expires <= now.Add(-oidcTokenClockSkew).Unix() {
		return false
	}
	if payload.NotBefore > 0 && time.Unix(payload.NotBefore, 0).After(now.Add(oidcTokenClockSkew)) {
		return false
	}
	if payload.IssuedAt > 0 && time.Unix(payload.IssuedAt, 0).After(now.Add(oidcTokenClockSkew)) {
		return false
	}
	return true
}

func parseJWKS(data []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, item := range set.Keys {
		if item.Kid == "" || item.Kty != "RSA" || item.N == "" || item.E == "" || !validOIDCJWKUseAndAlg(item.Use, item.Alg) {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(item.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(item.E)
		if err != nil {
			return nil, err
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 + int(b)
		}
		if e == 0 {
			continue
		}
		key := &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
		if !validOIDCRSAPublicKey(key) {
			continue
		}
		keys[item.Kid] = key
	}
	return keys, nil
}

func validOIDCJWKUseAndAlg(use string, alg string) bool {
	return (use == "" || use == "sig") && (alg == "" || alg == "RS256")
}

func validOIDCRSAPublicKey(key *rsa.PublicKey) bool {
	return key != nil && key.N != nil && key.N.BitLen() >= oidcMinRSAKeyBits && key.E > 1 && key.E%2 == 1
}

type oidcIDTokenPayload struct {
	Issuer    string       `json:"iss"`
	Subject   string       `json:"sub"`
	Audience  oidcAudience `json:"aud"`
	Expires   int64        `json:"exp"`
	IssuedAt  int64        `json:"iat"`
	NotBefore int64        `json:"nbf"`
	Email     string       `json:"email"`
	Name      string       `json:"name"`
	Groups    []string     `json:"groups"`
	Nonce     string       `json:"nonce"`
}

type oidcAudience []string

func (a *oidcAudience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a oidcAudience) Contains(want string) bool {
	for _, got := range a {
		if got == want {
			return true
		}
	}
	return false
}
