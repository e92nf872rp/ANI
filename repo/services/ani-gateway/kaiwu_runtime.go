package main

import (
	"os"
	"strings"

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
