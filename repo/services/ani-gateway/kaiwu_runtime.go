package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

// gatewayKaiwuRuntimeConfig 是网关进程读取的环境变量配置，只包含
// 可选的资源定位信息，不包含任何秘密原文。
type gatewayKaiwuRuntimeConfig struct {
	Namespace      string // KAIWU_NAMESPACE；为空时使用 kaiwu。
	ConsoleService string // KAIWU_CONSOLE_SERVICE；为空时使用 kaiwu-console。
	ConsoleSecret  string // KAIWU_CONSOLE_SECRET；为空时使用 kaiwu-console-web-token。
	BossService    string // KAIWU_BOSS_SERVICE；为空时使用 kaiwu-boss。
	BossSecret     string // KAIWU_BOSS_SECRET；为空时使用 kaiwu-boss-web-token。
}

// gatewayKaiwuRuntimeConfigFromEnv 读取可选的开物 Kubernetes 资源名。空值
// 不视为错误，而是交给运行时适配器使用生产默认资源名。
func gatewayKaiwuRuntimeConfigFromEnv() gatewayKaiwuRuntimeConfig {
	return gatewayKaiwuRuntimeConfig{
		Namespace:      os.Getenv("KAIWU_NAMESPACE"),
		ConsoleService: os.Getenv("KAIWU_CONSOLE_SERVICE"),
		ConsoleSecret:  os.Getenv("KAIWU_CONSOLE_SECRET"),
		BossService:    os.Getenv("KAIWU_BOSS_SERVICE"),
		BossSecret:     os.Getenv("KAIWU_BOSS_SECRET"),
	}
}

// newGatewayKaiwuRuntimeReader 将基于 Kubernetes 的开物适配器装配到
// 网关路由。kubernetesClient 为 nil 时返回 nil，后续开物处理函数必须
// 把该状态视为运行时不可用并返回 503，不能公开未授权入口。
func newGatewayKaiwuRuntimeReader(kubernetesClient *runtimeadapter.KubernetesRESTClient, config gatewayKaiwuRuntimeConfig) router.KaiwuRuntimeReader {
	if kubernetesClient == nil {
		return nil
	}
	return runtimeadapter.NewKubernetesKaiwuRuntimeReader(kubernetesClient, runtimeadapter.KubernetesKaiwuRuntimeConfig{
		Namespace:      strings.TrimSpace(config.Namespace),
		ConsoleService: strings.TrimSpace(config.ConsoleService),
		ConsoleSecret:  strings.TrimSpace(config.ConsoleSecret),
		BossService:    strings.TrimSpace(config.BossService),
		BossSecret:     strings.TrimSpace(config.BossSecret),
	})
}

// gatewayKaiwuPublicEntryConfig 是开物独占 origin 的入口配置。开物页面
// 依赖根路径绝对资源，无法在子路径下运行；配置基址后 entry API 直接返回
// 开物自身 origin 的绝对入口地址，Gateway 只做鉴权与跳转。
type gatewayKaiwuPublicEntryConfig struct {
	ConsoleURL string        // KAIWU_CONSOLE_PUBLIC_URL，例如 http://10.10.1.66:30088。
	BossURL    string        // KAIWU_BOSS_PUBLIC_URL，例如 http://10.10.1.66:30089。
	TokenTTL   time.Duration // KAIWU_ENTRY_TOKEN_TTL，例如 300s；空值使用网关默认值。
}

// gatewayKaiwuPublicEntryConfigFromEnv 读取并校验独占 origin 入口配置。
// 未配置任何基址是合法状态（回退到子路径代理入口）；配置了非法基址则
// 直接失败，避免把畸形地址下发到浏览器。
func gatewayKaiwuPublicEntryConfigFromEnv() (gatewayKaiwuPublicEntryConfig, error) {
	config := gatewayKaiwuPublicEntryConfig{
		ConsoleURL: strings.TrimSpace(os.Getenv("KAIWU_CONSOLE_PUBLIC_URL")),
		BossURL:    strings.TrimSpace(os.Getenv("KAIWU_BOSS_PUBLIC_URL")),
	}
	for _, candidate := range []struct {
		name  string
		value string
	}{
		{name: "KAIWU_CONSOLE_PUBLIC_URL", value: config.ConsoleURL},
		{name: "KAIWU_BOSS_PUBLIC_URL", value: config.BossURL},
	} {
		if candidate.value == "" {
			continue
		}
		if err := validateKaiwuOriginBaseURL(candidate.name, candidate.value); err != nil {
			return gatewayKaiwuPublicEntryConfig{}, err
		}
	}
	rawTTL := strings.TrimSpace(os.Getenv("KAIWU_ENTRY_TOKEN_TTL"))
	if rawTTL == "" {
		return config, nil
	}
	ttl, err := time.ParseDuration(rawTTL)
	if err != nil {
		return gatewayKaiwuPublicEntryConfig{}, fmt.Errorf("parse KAIWU_ENTRY_TOKEN_TTL %q: %w", rawTTL, err)
	}
	if ttl <= 0 {
		return gatewayKaiwuPublicEntryConfig{}, fmt.Errorf("KAIWU_ENTRY_TOKEN_TTL must be positive, got %q", rawTTL)
	}
	config.TokenTTL = ttl
	return config, nil
}

// validateKaiwuOriginBaseURL 只接受 scheme + host（可带根路径斜杠）的
// origin 基址，拒绝会把目标地址拼错的子路径、查询串和凭据。
func validateKaiwuOriginBaseURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s %q must use http or https scheme", name, value)
	}
	if parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s %q must be an origin without credentials", name, value)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("%s %q must not contain a path", name, value)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s %q must not contain query or fragment", name, value)
	}
	return nil
}
