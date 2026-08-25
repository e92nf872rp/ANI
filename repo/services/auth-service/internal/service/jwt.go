package service

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

type JWTConfig struct {
	PublicKeyPEM         string
	PublicKeyFile        string
	PrivateKeyPEM        string
	PrivateKeyFile       string
	Issuer               string
	OIDCIssuerURL        string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCAuthURL          string
	OIDCTokenURL         string
	OIDCJWKSURL          string
	OIDCPublicKeyPEM     string
	OIDCPublicKeyFile    string
	OIDCGroupRoleMapJSON string
}

type JWTValidator struct {
	publicKey *rsa.PublicKey
	issuer    string
	blocklist tokenBlocklist
	now       func() time.Time
}

type Claims struct {
	TenantID      uuid.UUID
	UserID        uuid.UUID
	Roles         []string
	JTI           string
	Scope         string
	Audience      string
	PrincipalKind string
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type jwtPayload struct {
	Subject       string `json:"sub"`
	Issuer        string `json:"iss"`
	Audience      any    `json:"aud,omitempty"` // 兼容 JWT 标准 string/[]string 两种形式
	Expires       int64  `json:"exp"`
	NotBefore     int64  `json:"nbf"`
	IssuedAt      int64  `json:"iat"`
	JTI           string `json:"jti"`
	PrincipalKind string `json:"principal_kind,omitempty"`
	TenantID      string `json:"tid"`
	UserID        string `json:"uid"`

	// V2 规范字段：credential_domain 表示凭证边界；permissions 表示签名携带的权限集合。
	CredentialDomain string   `json:"credential_domain,omitempty"`
	Permissions      []string `json:"permissions,omitempty"` // service 权威权限；不来自 Gateway

	// Deprecated legacy projection：仅供旧 ValidateToken / 旧 Gateway 消费；V2 不读取。
	Scope string   `json:"scope,omitempty"` // deprecated，旧 TenantContext.Scope
	Roles []string `json:"roles"`           // deprecated，旧 CheckPermission roles
}

// principalRecord 是 auth-service 内部规范记录；不依赖 Gateway internal/authz 包。
type principalRecord struct {
	Kind             string
	CredentialScheme string
	Domain           string
	TenantID         string
	SubjectID        string
	CredentialID     string
	Permissions      []string // 仅 auth-service 内部重验结果使用，不进入 Gateway Proto
}

// legacyJWTProjection 是旧 RPC 仍需返回的 legacy projection；roles/scope 不进入 V2 Proto。
type legacyJWTProjection struct {
	Scope string
	Roles []string
}

// validatedJWT 同时承载新 Principal 和旧 RPC projection；V2 只读 Principal，legacy 只读 Legacy。
type validatedJWT struct {
	Principal principalRecord
	Legacy    legacyJWTProjection
	JTI       string
}

func NewJWTValidator(cfg JWTConfig, blocklist tokenBlocklist) (*JWTValidator, error) {
	keyPEM := cfg.PublicKeyPEM
	if keyPEM == "" && cfg.PublicKeyFile != "" {
		data, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt public key file: %w", err)
		}
		keyPEM = string(data)
	}
	if keyPEM == "" {
		return nil, errJWTNotConfigured
	}
	key, err := parseRSAPublicKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &JWTValidator{
		publicKey: key,
		issuer:    cfg.Issuer,
		blocklist: blocklist,
		now:       time.Now,
	}, nil
}

func (v *JWTValidator) Validate(ctx context.Context, token string) (*validatedJWT, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errInvalidJWT
	}

	var header jwtHeader
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, errInvalidJWT
	}
	if header.Alg != "RS256" {
		return nil, errInvalidJWT
	}

	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errInvalidJWT
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errInvalidJWT
	}

	var payload jwtPayload
	if err := decodeSegment(parts[1], &payload); err != nil {
		return nil, errInvalidJWT
	}
	if err := v.validatePayload(ctx, payload); err != nil {
		return nil, err
	}
	return normalizeJWTPrincipal(payload)
}

// normalizeJWTPrincipal 解析 payload 为 validatedJWT；只负责解析填充，不做 V2 特有校验。
// 旧 JWT 缺 credential_domain 时 Domain 为空，由调用方（ValidatePrincipal V2 路径）决定是否拒绝。
func normalizeJWTPrincipal(payload jwtPayload) (*validatedJWT, error) {
	kind := payload.PrincipalKind
	if kind == "" {
		kind = "user" // 旧 JWT 兼容规则
	}
	if kind != "user" && kind != "service" {
		return nil, errInvalidJWT
	}

	// V2 从 credential_domain 读取边界，不做 scope fallback。
	domain := payload.CredentialDomain
	if domain != "" && domain != "tenant" && domain != "platform" {
		return nil, errInvalidJWT
	}

	// Core OpenAPI 已冻结 service token audience=ani-core。
	if kind == "service" && !audienceContains(payload.Audience, serviceJWTAudience) {
		return nil, errInvalidJWT
	}

	tenantID := strings.TrimSpace(payload.TenantID)
	// domain 为空时跳过 tenantID 校验（旧 JWT 没有 credential_domain，由调用方决定是否拒绝）。
	switch domain {
	case "platform":
		if tenantID != "" {
			return nil, errInvalidJWT
		}
	case "tenant":
		if id, err := uuid.Parse(tenantID); err != nil || id == uuid.Nil {
			return nil, errInvalidJWT
		}
	}

	subjectID := strings.TrimSpace(payload.UserID)
	if kind == "user" {
		if id, err := uuid.Parse(subjectID); err != nil || id == uuid.Nil {
			return nil, errInvalidJWT
		}
	} else {
		subjectID = strings.TrimSpace(payload.Subject)
		if subjectID == "" {
			return nil, errInvalidJWT
		}
	}

	principal := principalRecord{
		Kind:             kind,
		CredentialScheme: "bearer",
		Domain:           domain,
		TenantID:         tenantID,
		SubjectID:        subjectID,
		Permissions:      append([]string(nil), payload.Permissions...),
	}

	// legacy projection：scope + roles。
	// 如果 V2 没填 scope，按 domain 回填，保证旧 TenantContext.Scope 不为空。
	legacyScope := payload.Scope
	if legacyScope == "" {
		legacyScope = domain
	}
	legacy := legacyJWTProjection{
		Scope: legacyScope,
		Roles: append([]string(nil), payload.Roles...),
	}

	return &validatedJWT{Principal: principal, Legacy: legacy, JTI: payload.JTI}, nil
}

// validPermissionScopeClaims 校验签名凭证携带的 permissions 是否符合 scope:<resource>:<action> 格式。
func validPermissionScopeClaims(permissions []string) bool {
	if len(permissions) == 0 {
		return false
	}
	for _, raw := range permissions {
		if !strings.HasPrefix(strings.TrimSpace(raw), "scope:") {
			return false
		}
		value := strings.TrimPrefix(strings.TrimSpace(raw), "scope:")
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return false
		}
	}
	return true
}

// audienceContains 兼容 JWT 标准允许的 string/array 形式，并 fail closed。
func audienceContains(raw any, want string) bool {
	switch value := raw.(type) {
	case string:
		return value == want
	case []any:
		for _, item := range value {
			if audience, ok := item.(string); ok && audience == want {
				return true
			}
		}
	}
	return false
}

// Proto 将 internal principalRecord 转为 auth-service Proto PrincipalContext。
func (p principalRecord) Proto() *authv1.PrincipalContext {
	return &authv1.PrincipalContext{
		PrincipalKind:    p.Kind,
		CredentialScheme: p.CredentialScheme,
		CredentialDomain: p.Domain,
		TenantId:         p.TenantID,
		SubjectId:        p.SubjectID,
		CredentialId:     p.CredentialID,
	}
}

// jwtPrincipalContext 从 validatedJWT 提取 V2 PrincipalContext；
// V2 路径在读取后额外校验 CredentialDomain 非空等规则。
func jwtPrincipalContext(value *validatedJWT) (*authv1.PrincipalContext, error) {
	if value == nil {
		return nil, errInvalidJWT
	}
	return value.Principal.Proto(), nil
}

func (v *JWTValidator) validatePayload(ctx context.Context, payload jwtPayload) error {
	now := v.now().Unix()
	if payload.Expires <= 0 || now >= payload.Expires {
		return errInvalidJWT
	}
	if payload.NotBefore > 0 && now < payload.NotBefore {
		return errInvalidJWT
	}
	if v.issuer != "" && payload.Issuer != v.issuer {
		return errInvalidJWT
	}
	if payload.JTI != "" && v.blocklist != nil {
		blocked, err := v.blocklist.IsRevoked(ctx, payload.JTI)
		if err != nil {
			return fmt.Errorf("check jwt blocklist: %w", err)
		}
		if blocked {
			return errInvalidJWT
		}
	}
	return nil
}

func parseRSAPublicKey(keyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errInvalidJWTKey
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := pub.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	return nil, errInvalidJWTKey
}

func decodeSegment(segment string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

const (
	serviceAudience        = "ani-core"
	serviceJWTAudience     = serviceAudience // 别名：与计划文档中的命名保持一致
	defaultServiceTokenTTL = 5 * time.Minute
	maxServiceTokenTTL     = time.Hour
)

var serviceActorUserID = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")

var (
	errJWTNotConfigured = errors.New("jwt validator is not configured")
	errInvalidJWT       = errors.New("invalid jwt")
	errInvalidJWTKey    = errors.New("invalid jwt public key")
)
