package main

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

// gatewayKaiwuCookieConfig 保存签名开物代理 Cookie 所需的共享部署配置。
// SigningSecret 永远不写入日志。
type gatewayKaiwuCookieConfig struct {
	SigningSecret string
	BootstrapTTL  time.Duration
	SessionTTL    time.Duration
}

// gatewayKaiwuCookieConfigFromEnv 读取 TTL 和共享签名密钥。空密钥仍是
// 有效配置状态，Gateway 可以启动，但在注入密钥前 Kaiwu entry API 会
// 失败关闭。
func gatewayKaiwuCookieConfigFromEnv() (gatewayKaiwuCookieConfig, error) {
	config := gatewayKaiwuCookieConfig{
		SigningSecret: os.Getenv("KAIWU_PROXY_SIGNING_SECRET"),
		BootstrapTTL:  120 * time.Second,
		SessionTTL:    30 * time.Minute,
	}
	if value := strings.TrimSpace(os.Getenv("KAIWU_PROXY_BOOTSTRAP_TTL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 || parsed%time.Second != 0 {
			return config, errors.New("KAIWU_PROXY_BOOTSTRAP_TTL must be a positive whole number of seconds")
		}
		config.BootstrapTTL = parsed
	}
	if value := strings.TrimSpace(os.Getenv("KAIWU_PROXY_SESSION_TTL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 || parsed%time.Second != 0 {
			return config, errors.New("KAIWU_PROXY_SESSION_TTL must be a positive whole number of seconds")
		}
		config.SessionTTL = parsed
	}
	return config, nil
}

// newGatewayKaiwuCookieSigner 在未配置签名密钥时返回 nil；router 随后
// 返回 503，而不是签发无保护入口。若配置了密钥，非法长度或 TTL 会导致
// 启动失败。
func newGatewayKaiwuCookieSigner(config gatewayKaiwuCookieConfig) (router.KaiwuCookieSigner, error) {
	if strings.TrimSpace(config.SigningSecret) == "" {
		return nil, nil
	}
	return router.NewKaiwuCookieSigner(config.SigningSecret, config.BootstrapTTL, config.SessionTTL)
}
