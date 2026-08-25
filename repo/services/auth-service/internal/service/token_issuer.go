package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type JWTIssuer struct {
	privateKey *rsa.PrivateKey
	issuer     string
	now        func() time.Time
}

func NewJWTIssuer(cfg JWTConfig) (*JWTIssuer, error) {
	keyPEM := cfg.PrivateKeyPEM
	if keyPEM == "" && cfg.PrivateKeyFile != "" {
		data, err := os.ReadFile(cfg.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt private key file: %w", err)
		}
		keyPEM = string(data)
	}
	if keyPEM == "" {
		return nil, errJWTNotConfigured
	}
	key, err := parseRSAPrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &JWTIssuer{privateKey: key, issuer: cfg.Issuer, now: time.Now}, nil
}

func (i *JWTIssuer) IssueAccessToken(principal refreshPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	payload := jwtPayload{
		Subject:   principal.UserID.String(),
		Issuer:    i.issuer,
		Expires:   now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		JTI:       uuid.NewString(),
		TenantID:  principal.TenantID.String(),
		UserID:    principal.UserID.String(),
		// V2 规范字段：tenant user 凭证固定为 user/tenant 边界。
		PrincipalKind:    "user",
		CredentialDomain: "tenant",
		Roles:            principal.Roles,
		Scope:            "tenant",
	}
	return i.sign(payload)
}

// IssuePlatformAccessToken 为平台管理员签发 access token。
// token 携带 scope=platform、无 tenant_id、roles=["platform-admin"]。
func (i *JWTIssuer) IssuePlatformAccessToken(principal platformPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	roles := principal.Roles
	if len(roles) == 0 {
		roles = []string{"platform-admin"}
	}
	payload := jwtPayload{
		Subject:   principal.UserID.String(),
		Issuer:    i.issuer,
		Expires:   now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		JTI:       uuid.NewString(),
		TenantID:  "",
		UserID:    principal.UserID.String(),
		// V2 规范字段：platform user 凭证固定为 user/platform 边界。
		PrincipalKind:    "user",
		CredentialDomain: "platform",
		Roles:            roles,
		Scope:            "platform",
	}
	return i.sign(payload)
}

// IssueServiceTokenPayload 为已构造好的 service token claims 补齐签名时间字段并签发。
// payload 由 resolveIssueServiceTokenClaims 生成，这里只负责补 issuer/expiry/jti。
func (i *JWTIssuer) IssueServiceTokenPayload(payload jwtPayload, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultServiceTokenTTL
	}
	if ttl > maxServiceTokenTTL {
		ttl = maxServiceTokenTTL
	}
	now := i.now()
	payload.Issuer = i.issuer
	payload.Expires = now.Add(ttl).Unix()
	payload.NotBefore = now.Unix()
	payload.IssuedAt = now.Unix()
	payload.JTI = uuid.NewString()
	if payload.Subject == "" {
		payload.Subject = serviceActorUserID.String()
	}
	// uid 是 legacy ValidateToken.UserId 的 UUID 兼容投影，与 sub（V2 服务身份）分离；
	// 兜底必须是 magic UUID，不能复制 sub，否则服务名会泄漏到旧 wire 契约。
	if payload.UserID == "" {
		payload.UserID = serviceActorUserID.String()
	}
	return i.sign(payload)
}

// resolveIssueServiceTokenClaims 将 IssueServiceTokenRequest 映射为 V2 JWT claims。
// 同时写入 V2 规范字段（credential_domain + permissions）与 deprecated legacy projection。
func resolveIssueServiceTokenClaims(req *authv1.IssueServiceTokenRequest) (jwtPayload, error) {
	//nolint:staticcheck // scope 字段虽在 proto 标 deprecated，但 V2 兼容路径必须读取旧字段
	legacyScope := strings.TrimSpace(req.GetScope())
	hasLegacyScope := legacyScope != ""
	hasPermissions := len(req.GetPermissions()) != 0

	// 同时提交 scope 和 permissions 时，二者不一致必须拒绝，不能静默选一个。
	if hasLegacyScope && hasPermissions {
		expected := []string{legacyScope}
		if !equalStringSlice(expected, req.GetPermissions()) {
			return jwtPayload{}, status.Error(codes.InvalidArgument,
				"scope and permissions are both set but inconsistent")
		}
	}

	var permissions []string
	switch {
	case hasPermissions:
		permissions = req.GetPermissions()
	case hasLegacyScope:
		permissions = []string{legacyScope}
	default:
		return jwtPayload{}, status.Error(codes.InvalidArgument,
			"either scope or permissions must be set")
	}

	// permissions 为空或格式非法时 fail closed，不签发无权限的 service JWT。
	for _, permission := range permissions {
		if !isValidPermissionScopeClaim(permission) {
			return jwtPayload{}, status.Error(codes.InvalidArgument, "invalid service permission")
		}
	}

	// credential_domain：优先取请求值；旧调用方未填时按 tenant_id 推导。
	domain := strings.TrimSpace(req.GetCredentialDomain())
	if domain == "" {
		if strings.TrimSpace(req.GetTenantId()) != "" {
			domain = "tenant"
		} else {
			domain = "platform"
		}
	}
	if domain != "tenant" && domain != "platform" {
		return jwtPayload{}, status.Error(codes.InvalidArgument, "invalid credential_domain")
	}

	return jwtPayload{
		PrincipalKind:    "service",
		Audience:         serviceJWTAudience, // ani-core
		TenantID:         req.GetTenantId(),
		Subject:          req.GetCallerService(),      // V2 权威服务身份
		UserID:           serviceActorUserID.String(), // legacy ValidateToken.UserId 兼容投影
		CredentialDomain: domain,
		Permissions:      permissions,
		// deprecated legacy projection：旧 ValidateToken / 旧 Gateway 仍需消费。
		Scope: permissions[0], // 兼容期 legacy scope 只保留第一个权限
		Roles: []string{"service"},
	}, nil
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isValidPermissionScopeClaim 校验单个权限是否为规范 scope:<resource>:<action> 格式。
// 缺前缀、空 resource/action 或非法字符一律拒绝（fail closed）。
func isValidPermissionScopeClaim(raw string) bool {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "scope:") {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "scope:"), ":", 2)
	if len(parts) != 2 {
		return false
	}
	return validScopePart(parts[0]) && validScopePart(parts[1])
}

func (i *JWTIssuer) sign(payload jwtPayload) (string, error) {
	header := jwtHeader{Alg: "RS256", Typ: "JWT"}
	headerSegment, err := encodeJWTJSON(header)
	if err != nil {
		return "", err
	}
	payloadSegment, err := encodeJWTJSON(payload)
	if err != nil {
		return "", err
	}
	signingInput := headerSegment + "." + payloadSegment
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAPrivateKey(keyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errInvalidJWTKey
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errInvalidJWTKey
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errInvalidJWTKey
	}
	return key, nil
}

func encodeJWTJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
