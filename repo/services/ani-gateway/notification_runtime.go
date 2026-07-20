package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// gatewayNotificationRuntimeConfig 控制 NotificationService 的 provider 选择。
// 当前只支持 local（nil handler 自带 fallback）。
type gatewayNotificationRuntimeConfig struct {
	ProviderMode string
}

func gatewayNotificationRuntimeConfigFromEnv() gatewayNotificationRuntimeConfig {
	return gatewayNotificationRuntimeConfig{
		ProviderMode: strings.TrimSpace(os.Getenv("NOTIFICATION_PROVIDER_MODE")),
	}
}

// newGatewayNotificationService 目前只认 local/空；其他模式返回 ErrUnsupported。
// 返回 nil 时，Gateway handler 会自动 fallback 到 runtimeadapter.NewLocalNotificationService()。
func newGatewayNotificationService(cfg gatewayNotificationRuntimeConfig) (ports.NotificationService, error) {
	switch cfg.ProviderMode {
	case "", "local":
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unsupported NOTIFICATION_PROVIDER_MODE %q", ports.ErrUnsupported, cfg.ProviderMode)
	}
}
