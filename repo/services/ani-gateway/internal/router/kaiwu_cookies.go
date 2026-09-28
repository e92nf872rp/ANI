package router

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// kaiwuCookieMinimumSecretLength 是 HMAC 签名密钥的最小长度。部署密钥
	// 过短时直接拒绝，不会静默补齐。
	kaiwuCookieMinimumSecretLength = 32
	// kaiwuCookieJTIBytes 是每个 Cookie 唯一标识使用的随机字节数。
	kaiwuCookieJTIBytes = 16
	// kaiwuCookieClockSkew 是允许的 issued_at 最大未来时间偏差。
	kaiwuCookieClockSkew = 30 * time.Second
)

// KaiwuCookieType 区分短期入口 bootstrap Cookie 和较长期的代理 session Cookie。
type KaiwuCookieType string

const (
	// KaiwuCookieTypeBootstrap 表示 entry API 授权成功后签发的入口 Cookie。
	KaiwuCookieTypeBootstrap KaiwuCookieType = "bootstrap"
	// KaiwuCookieTypeSession 表示首次 DSH token 交换完成后签发的代理 Cookie。
	KaiwuCookieTypeSession KaiwuCookieType = "session"
)

// KaiwuCookieIdentity 将 Cookie 绑定到已认证的 ANI 身份和单个开物客户端。
// 字段值只能来自 Gateway 中间件，不能来自请求体或查询参数。
type KaiwuCookieIdentity struct {
	Client   string
	Scope    string
	TenantID string
	UserID   string
}

// KaiwuCookieClaims 是保存在浏览器 Cookie 中的签名 JSON 数据。它刻意不
// 包含 DSH token、ClusterIP 或其他运行时秘密。
type KaiwuCookieClaims struct {
	Type       KaiwuCookieType `json:"typ"`
	Client     string          `json:"client"`
	Scope      string          `json:"scope"`
	TenantID   string          `json:"tenant_id,omitempty"`
	UserID     string          `json:"user_id"`
	IssuedAt   int64           `json:"issued_at"`
	ExpiresAt  int64           `json:"expires_at"`
	Identifier string          `json:"jti"`
}

// KaiwuCookie 描述 HTTP 层设置一个签名 Gateway Cookie 所需的全部信息。
// Value 对浏览器是不透明字符串，但并不加密。
type KaiwuCookie struct {
	Type   KaiwuCookieType
	Name   string
	Value  string
	Path   string
	MaxAge time.Duration
	Secure bool
}

// KaiwuCookieSigner 负责签发和校验受 HMAC-SHA256 保护的开物 Cookie。
// 实现必须使用部署共享密钥，不能自行生成进程本地随机默认密钥。
type KaiwuCookieSigner interface {
	// IssueKaiwuBootstrapCookie 在 Console 或 BOSS entry 授权成功后，
	// 创建短期入口 Cookie。
	IssueKaiwuBootstrapCookie(identity KaiwuCookieIdentity, now time.Time, secure bool) (KaiwuCookie, error)
	// IssueKaiwuSessionCookie 在 DSH 下发自身实例 Cookie 后，创建后续
	// 代理请求使用的 Cookie。
	IssueKaiwuSessionCookie(identity KaiwuCookieIdentity, now time.Time, secure bool) (KaiwuCookie, error)
	// VerifyKaiwuCookie 校验浏览器 Cookie 的签名、类型、客户端、身份
	// 和有效期。
	VerifyKaiwuCookie(cookieType KaiwuCookieType, client string, value string, now time.Time) (KaiwuCookieIdentity, error)
}

// kaiwuCookieSigner 是 KaiwuCookieSigner 的本地 HMAC 实现。
type kaiwuCookieSigner struct {
	secret       []byte
	bootstrapTTL time.Duration
	sessionTTL   time.Duration
}

// NewKaiwuCookieSigner 校验部署配置并返回签名器。这里刻意不为密钥提供
// 默认值或去除首尾空白，因为所有 Gateway 副本必须收到完全相同的密钥。
func NewKaiwuCookieSigner(secret string, bootstrapTTL, sessionTTL time.Duration) (KaiwuCookieSigner, error) {
	if len([]byte(secret)) < kaiwuCookieMinimumSecretLength {
		return nil, errors.New("Kaiwu cookie signing secret must be at least 32 bytes")
	}
	if bootstrapTTL <= 0 || bootstrapTTL%time.Second != 0 {
		return nil, errors.New("Kaiwu bootstrap cookie TTL must be a positive whole number of seconds")
	}
	if sessionTTL <= 0 || sessionTTL%time.Second != 0 {
		return nil, errors.New("Kaiwu session cookie TTL must be a positive whole number of seconds")
	}
	return &kaiwuCookieSigner{
		secret:       []byte(secret),
		bootstrapTTL: bootstrapTTL,
		sessionTTL:   sessionTTL,
	}, nil
}

// IssueKaiwuBootstrapCookie 签名并描述 Console/BOSS 入口 Cookie。
func (s *kaiwuCookieSigner) IssueKaiwuBootstrapCookie(identity KaiwuCookieIdentity, now time.Time, secure bool) (KaiwuCookie, error) {
	return s.issueKaiwuCookie(KaiwuCookieTypeBootstrap, identity, now, secure, s.bootstrapTTL)
}

// IssueKaiwuSessionCookie 签名并描述较长期的代理 session Cookie。
func (s *kaiwuCookieSigner) IssueKaiwuSessionCookie(identity KaiwuCookieIdentity, now time.Time, secure bool) (KaiwuCookie, error) {
	return s.issueKaiwuCookie(KaiwuCookieTypeSession, identity, now, secure, s.sessionTTL)
}

// VerifyKaiwuCookie 校验两段式签名值并返回绑定的身份。所有校验失败都
// 返回错误；调用方必须映射为 401/403，不能回显 Cookie 或其 claims。
func (s *kaiwuCookieSigner) VerifyKaiwuCookie(cookieType KaiwuCookieType, client string, value string, now time.Time) (KaiwuCookieIdentity, error) {
	segments := strings.Split(strings.TrimSpace(value), ".")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie format")
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie signature encoding")
	}
	expectedSignature := s.signKaiwuCookiePayload(segments[0])
	if !hmac.Equal(signature, expectedSignature) {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie signature")
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie payload encoding")
	}
	var claims KaiwuCookieClaims
	if err := json.Unmarshal(claimBytes, &claims); err != nil {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie claims")
	}
	if claims.Type != cookieType || claims.Client != client {
		return KaiwuCookieIdentity{}, errors.New("Kaiwu cookie type or client mismatch")
	}
	if !validKaiwuCookieIdentity(claims.KaiwuCookieIdentity()) {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie identity")
	}
	if claims.IssuedAt == 0 || claims.ExpiresAt <= claims.IssuedAt || claims.Identifier == "" {
		return KaiwuCookieIdentity{}, errors.New("invalid Kaiwu cookie metadata")
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(kaiwuCookieClockSkew)) {
		return KaiwuCookieIdentity{}, errors.New("Kaiwu cookie issued in the future")
	}
	if !now.Before(time.Unix(claims.ExpiresAt, 0)) {
		return KaiwuCookieIdentity{}, errors.New("Kaiwu cookie expired")
	}
	return claims.KaiwuCookieIdentity(), nil
}

// issueKaiwuCookie 校验身份，创建 claims，完成签名，并映射为浏览器
// Cookie 属性。
func (s *kaiwuCookieSigner) issueKaiwuCookie(cookieType KaiwuCookieType, identity KaiwuCookieIdentity, now time.Time, secure bool, ttl time.Duration) (KaiwuCookie, error) {
	if now.IsZero() {
		return KaiwuCookie{}, errors.New("Kaiwu cookie issue time is required")
	}
	if !validKaiwuCookieIdentity(identity) {
		return KaiwuCookie{}, errors.New("invalid Kaiwu cookie identity")
	}
	identifierBytes := make([]byte, kaiwuCookieJTIBytes)
	if _, err := rand.Read(identifierBytes); err != nil {
		return KaiwuCookie{}, fmt.Errorf("generate Kaiwu cookie identifier: %w", err)
	}
	claims := KaiwuCookieClaims{
		Type:       cookieType,
		Client:     identity.Client,
		Scope:      identity.Scope,
		TenantID:   identity.TenantID,
		UserID:     identity.UserID,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(ttl).Unix(),
		Identifier: base64.RawURLEncoding.EncodeToString(identifierBytes),
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		return KaiwuCookie{}, fmt.Errorf("encode Kaiwu cookie claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claimBytes)
	signature := base64.RawURLEncoding.EncodeToString(s.signKaiwuCookiePayload(payload))
	return KaiwuCookie{
		Type:   cookieType,
		Name:   kaiwuCookieName(cookieType, identity.Client),
		Value:  payload + "." + signature,
		Path:   kaiwuCookiePath(identity.Client),
		MaxAge: ttl,
		Secure: secure,
	}, nil
}

// signKaiwuCookiePayload 对已编码的 claims 段计算原始 HMAC。
func (s *kaiwuCookieSigner) signKaiwuCookiePayload(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// validKaiwuCookieIdentity 在签名或信任 claims 前，强制校验客户端专属
// 身份不变量。
func validKaiwuCookieIdentity(identity KaiwuCookieIdentity) bool {
	if strings.TrimSpace(identity.UserID) == "" {
		return false
	}
	switch identity.Client {
	case kaiwuClientConsole:
		return identity.Scope == "tenant" && strings.TrimSpace(identity.TenantID) != ""
	case kaiwuClientBoss:
		return identity.Scope == "platform" && strings.TrimSpace(identity.TenantID) == ""
	default:
		return false
	}
}

// kaiwuCookieName 返回按客户端隔离的浏览器 Cookie 名称。
func kaiwuCookieName(cookieType KaiwuCookieType, client string) string {
	if cookieType == KaiwuCookieTypeSession {
		return "ani_kaiwu_session_" + client
	}
	return "ani_kaiwu_bootstrap_" + client
}

// kaiwuCookiePath 将每个 Cookie 限制在对应的代理子路径内。
func kaiwuCookiePath(client string) string {
	if client == kaiwuClientBoss {
		return "/kaiwu/boss"
	}
	return "/kaiwu/console"
}

// KaiwuCookieIdentity 将已签名 claims 转回 handler 使用的身份类型，
// 同时省略运行时秘密和认证凭据。
func (c KaiwuCookieClaims) KaiwuCookieIdentity() KaiwuCookieIdentity {
	return KaiwuCookieIdentity{
		Client:   c.Client,
		Scope:    c.Scope,
		TenantID: c.TenantID,
		UserID:   c.UserID,
	}
}
