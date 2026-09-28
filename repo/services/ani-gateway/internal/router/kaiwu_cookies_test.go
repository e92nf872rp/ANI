package router

import (
	"strings"
	"testing"
	"time"
)

// testKaiwuCookieSecret 是固定的测试专用 HMAC 密钥，永远不会作为部署密钥。
const testKaiwuCookieSecret = "0123456789abcdef0123456789abcdef"

// newKaiwuCookieSignerForTests 创建 Cookie 单元测试共用的签名器。
func newKaiwuCookieSignerForTests(t *testing.T) KaiwuCookieSigner {
	t.Helper()
	signer, err := NewKaiwuCookieSigner(testKaiwuCookieSecret, 120*time.Second, 30*time.Minute)
	if err != nil {
		t.Fatalf("new Kaiwu cookie signer: %v", err)
	}
	return signer
}

// TestNewKaiwuCookieSigner_ValidatesDeploymentSettings 验证短密钥和非法 TTL
// 会在签发更弱 Cookie 前被拒绝。
func TestNewKaiwuCookieSigner_ValidatesDeploymentSettings(t *testing.T) {
	tests := []struct {
		name         string
		secret       string
		bootstrapTTL time.Duration
		sessionTTL   time.Duration
	}{
		{name: "short secret", secret: "short", bootstrapTTL: time.Minute, sessionTTL: time.Hour},
		{name: "zero bootstrap ttl", secret: testKaiwuCookieSecret, sessionTTL: time.Hour},
		{name: "fractional bootstrap ttl", secret: testKaiwuCookieSecret, bootstrapTTL: 1500 * time.Millisecond, sessionTTL: time.Hour},
		{name: "zero session ttl", secret: testKaiwuCookieSecret, bootstrapTTL: time.Minute},
		{name: "fractional session ttl", secret: testKaiwuCookieSecret, bootstrapTTL: time.Minute, sessionTTL: 1500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewKaiwuCookieSigner(tt.secret, tt.bootstrapTTL, tt.sessionTTL); err == nil {
				t.Fatal("invalid Kaiwu cookie settings accepted")
			}
		})
	}
}

// TestKaiwuCookieSigner_ConsoleBootstrapIssueAndVerify 验证 Console Cookie
// 属性以及签名的租户/用户身份绑定。
func TestKaiwuCookieSigner_ConsoleBootstrapIssueAndVerify(t *testing.T) {
	signer := newKaiwuCookieSignerForTests(t)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	identity := KaiwuCookieIdentity{
		Client:   "console",
		Scope:    "tenant",
		TenantID: "11111111-1111-1111-1111-111111111111",
		UserID:   "22222222-2222-2222-2222-222222222222",
	}

	cookie, err := signer.IssueKaiwuBootstrapCookie(identity, now, true)
	if err != nil {
		t.Fatalf("issue Console cookie: %v", err)
	}
	if cookie.Type != KaiwuCookieTypeBootstrap || cookie.Name != "ani_kaiwu_bootstrap_console" {
		t.Fatalf("cookie type/name = %s/%s", cookie.Type, cookie.Name)
	}
	if cookie.Path != "/kaiwu/console" || cookie.MaxAge != 120*time.Second || !cookie.Secure {
		t.Fatalf("cookie attributes = %#v", cookie)
	}
	verified, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, "console", cookie.Value, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("verify Console cookie: %v", err)
	}
	if verified != identity {
		t.Fatalf("verified identity = %#v", verified)
	}
}

// TestKaiwuCookieSigner_BossSessionIssueAndVerify 验证 BOSS Cookie 不携带
// 租户身份，且不能被 Console 客户端复用。
func TestKaiwuCookieSigner_BossSessionIssueAndVerify(t *testing.T) {
	signer := newKaiwuCookieSignerForTests(t)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	identity := KaiwuCookieIdentity{Client: "boss", Scope: "platform", UserID: "22222222-2222-2222-2222-222222222222"}

	cookie, err := signer.IssueKaiwuSessionCookie(identity, now, false)
	if err != nil {
		t.Fatalf("issue BOSS cookie: %v", err)
	}
	if cookie.Type != KaiwuCookieTypeSession || cookie.Name != "ani_kaiwu_session_boss" {
		t.Fatalf("cookie type/name = %s/%s", cookie.Type, cookie.Name)
	}
	if cookie.Path != "/kaiwu/boss" || cookie.MaxAge != 30*time.Minute || cookie.Secure {
		t.Fatalf("cookie attributes = %#v", cookie)
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeSession, "boss", cookie.Value, now.Add(time.Minute)); err != nil {
		t.Fatalf("verify BOSS cookie: %v", err)
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeSession, "console", cookie.Value, now.Add(time.Minute)); err == nil {
		t.Fatal("BOSS cookie accepted for Console client")
	}
}

// TestKaiwuCookieSigner_RejectsInvalidIdentities 验证签名前会强制校验
// 客户端专属身份不变量。
func TestKaiwuCookieSigner_RejectsInvalidIdentities(t *testing.T) {
	signer := newKaiwuCookieSignerForTests(t)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		identity KaiwuCookieIdentity
	}{
		{name: "missing user", identity: KaiwuCookieIdentity{Client: "console", Scope: "tenant", TenantID: "tenant-id"}},
		{name: "console missing tenant", identity: KaiwuCookieIdentity{Client: "console", Scope: "tenant", UserID: "user-id"}},
		{name: "console wrong scope", identity: KaiwuCookieIdentity{Client: "console", Scope: "platform", TenantID: "tenant-id", UserID: "user-id"}},
		{name: "boss with tenant", identity: KaiwuCookieIdentity{Client: "boss", Scope: "platform", TenantID: "tenant-id", UserID: "user-id"}},
		{name: "boss wrong scope", identity: KaiwuCookieIdentity{Client: "boss", Scope: "tenant", UserID: "user-id"}},
		{name: "unknown client", identity: KaiwuCookieIdentity{Client: "other", Scope: "platform", UserID: "user-id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := signer.IssueKaiwuBootstrapCookie(tt.identity, now, false); err == nil {
				t.Fatalf("invalid identity accepted: %#v", tt.identity)
			}
		})
	}
}

// TestKaiwuCookieSigner_VerifyRejectsTamperingAndTimeErrors 覆盖签名篡改、
// 类型混淆、畸形数据和过期场景。
func TestKaiwuCookieSigner_VerifyRejectsTamperingAndTimeErrors(t *testing.T) {
	signer := newKaiwuCookieSignerForTests(t)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	identity := KaiwuCookieIdentity{Client: "console", Scope: "tenant", TenantID: "tenant-id", UserID: "user-id"}
	cookie, err := signer.IssueKaiwuBootstrapCookie(identity, now, false)
	if err != nil {
		t.Fatalf("issue cookie: %v", err)
	}

	tampered := cookie.Value[:len(cookie.Value)-2] + "aa"
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, "console", tampered, now); err == nil {
		t.Fatal("tampered signature accepted")
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeSession, "console", cookie.Value, now); err == nil {
		t.Fatal("bootstrap cookie accepted as session cookie")
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, "console", "", now); err == nil {
		t.Fatal("empty cookie accepted")
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, "console", "invalid", now); err == nil {
		t.Fatal("invalid cookie format accepted")
	}
	if _, err := signer.VerifyKaiwuCookie(KaiwuCookieTypeBootstrap, "console", cookie.Value, now.Add(121*time.Second)); err == nil {
		t.Fatal("expired cookie accepted")
	}
}

// TestKaiwuCookieSigner_SecretsAreNotInCookieValues 验证 Cookie 值不包含
// 签名密钥或内部 ClusterIP。
func TestKaiwuCookieSigner_SecretsAreNotInCookieValues(t *testing.T) {
	signer := newKaiwuCookieSignerForTests(t)
	identity := KaiwuCookieIdentity{Client: "boss", Scope: "platform", UserID: "user-id"}
	cookie, err := signer.IssueKaiwuSessionCookie(identity, time.Now(), true)
	if err != nil {
		t.Fatalf("issue cookie: %v", err)
	}
	if strings.Contains(cookie.Value, testKaiwuCookieSecret) {
		t.Fatal("cookie value contains signing secret")
	}
	if strings.Contains(cookie.Value, "10.0.0.") {
		t.Fatal("cookie value contains internal IP")
	}
}
