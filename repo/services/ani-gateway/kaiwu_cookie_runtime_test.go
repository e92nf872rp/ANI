package main

import (
	"testing"
	"time"
)

// TestGatewayKaiwuCookieConfigFromEnvDefaults 验证文档中定义的默认 TTL。
func TestGatewayKaiwuCookieConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("KAIWU_PROXY_SIGNING_SECRET", "")
	t.Setenv("KAIWU_PROXY_BOOTSTRAP_TTL", "")
	t.Setenv("KAIWU_PROXY_SESSION_TTL", "")

	config, err := gatewayKaiwuCookieConfigFromEnv()
	if err != nil {
		t.Fatalf("config from env: %v", err)
	}
	if config.BootstrapTTL != 120*time.Second || config.SessionTTL != 30*time.Minute {
		t.Fatalf("TTL defaults = %#v", config)
	}
}

// TestGatewayKaiwuCookieConfigFromEnvValidatesTTLs 验证非法 duration 在
// Gateway 继续启动前即被拒绝。
func TestGatewayKaiwuCookieConfigFromEnvValidatesTTLs(t *testing.T) {
	for _, key := range []string{"KAIWU_PROXY_BOOTSTRAP_TTL", "KAIWU_PROXY_SESSION_TTL"} {
		for _, value := range []string{"invalid", "0s", "-1s", "1500ms"} {
			t.Setenv("KAIWU_PROXY_SIGNING_SECRET", "")
			t.Setenv("KAIWU_PROXY_BOOTSTRAP_TTL", "")
			t.Setenv("KAIWU_PROXY_SESSION_TTL", "")
			t.Setenv(key, value)
			if _, err := gatewayKaiwuCookieConfigFromEnv(); err == nil {
				t.Fatalf("%s=%q accepted", key, value)
			}
		}
	}
}

// TestNewGatewayKaiwuCookieSignerFailsClosedWithoutSecret 验证缺少密钥时
// 禁用签名器，而不是生成随机兜底密钥。
func TestNewGatewayKaiwuCookieSignerFailsClosedWithoutSecret(t *testing.T) {
	t.Setenv("KAIWU_PROXY_SIGNING_SECRET", "")
	config, err := gatewayKaiwuCookieConfigFromEnv()
	if err != nil {
		t.Fatalf("config from env: %v", err)
	}
	signer, err := newGatewayKaiwuCookieSigner(config)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	if signer != nil {
		t.Fatal("empty secret produced a signer")
	}
}
