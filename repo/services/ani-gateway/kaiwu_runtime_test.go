package main

import (
	"testing"
	"time"
)

// TestGatewayKaiwuPublicEntryConfigFromEnvEmpty 验证未配置独占 origin 时
// 保持合法空配置，网关继续使用子路径代理入口。
func TestGatewayKaiwuPublicEntryConfigFromEnvEmpty(t *testing.T) {
	t.Setenv("KAIWU_CONSOLE_PUBLIC_URL", "")
	t.Setenv("KAIWU_BOSS_PUBLIC_URL", "")
	t.Setenv("KAIWU_ENTRY_TOKEN_TTL", "")

	config, err := gatewayKaiwuPublicEntryConfigFromEnv()
	if err != nil {
		t.Fatalf("config from env: %v", err)
	}
	if config.ConsoleURL != "" || config.BossURL != "" || config.TokenTTL != 0 {
		t.Fatalf("config = %#v", config)
	}
}

// TestGatewayKaiwuPublicEntryConfigFromEnvValid 验证基址与 TTL 正常解析。
func TestGatewayKaiwuPublicEntryConfigFromEnvValid(t *testing.T) {
	t.Setenv("KAIWU_CONSOLE_PUBLIC_URL", "http://10.10.1.66:30088")
	t.Setenv("KAIWU_BOSS_PUBLIC_URL", "https://kaiwu.example.com/")
	t.Setenv("KAIWU_ENTRY_TOKEN_TTL", "300s")

	config, err := gatewayKaiwuPublicEntryConfigFromEnv()
	if err != nil {
		t.Fatalf("config from env: %v", err)
	}
	if config.ConsoleURL != "http://10.10.1.66:30088" ||
		config.BossURL != "https://kaiwu.example.com/" ||
		config.TokenTTL != 300*time.Second {
		t.Fatalf("config = %#v", config)
	}
}

// TestGatewayKaiwuPublicEntryConfigFromEnvRejectsInvalidURL 验证畸形基址在
// 网关启动阶段即被拒绝，避免把错误地址下发给浏览器。
func TestGatewayKaiwuPublicEntryConfigFromEnvRejectsInvalidURL(t *testing.T) {
	for _, value := range []string{
		"10.10.1.66:30088",
		"http://",
		"ftp://10.10.1.66:30088",
		"http://10.10.1.66:30088/kaiwu",
		"http://user:password@10.10.1.66:30088",
		"http://10.10.1.66:30088?token=secret",
		"http://10.10.1.66:30088#fragment",
	} {
		t.Setenv("KAIWU_CONSOLE_PUBLIC_URL", value)
		t.Setenv("KAIWU_BOSS_PUBLIC_URL", "")
		t.Setenv("KAIWU_ENTRY_TOKEN_TTL", "")
		if _, err := gatewayKaiwuPublicEntryConfigFromEnv(); err == nil {
			t.Fatalf("KAIWU_CONSOLE_PUBLIC_URL=%q accepted", value)
		}
	}
}

// TestGatewayKaiwuPublicEntryConfigFromEnvRejectsInvalidTTL 验证非法 TTL 在
// 网关启动阶段即被拒绝。
func TestGatewayKaiwuPublicEntryConfigFromEnvRejectsInvalidTTL(t *testing.T) {
	for _, value := range []string{"invalid", "0s", "-1s"} {
		t.Setenv("KAIWU_CONSOLE_PUBLIC_URL", "")
		t.Setenv("KAIWU_BOSS_PUBLIC_URL", "")
		t.Setenv("KAIWU_ENTRY_TOKEN_TTL", value)
		if _, err := gatewayKaiwuPublicEntryConfigFromEnv(); err == nil {
			t.Fatalf("KAIWU_ENTRY_TOKEN_TTL=%q accepted", value)
		}
	}
}
