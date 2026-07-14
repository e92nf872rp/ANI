package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayEmailNotificationRuntimeConfig struct {
	SMTPProviderMode string // "" / "local" = simulate; "smtp" = real SMTP
}

func gatewayEmailNotificationRuntimeConfigFromEnv() gatewayEmailNotificationRuntimeConfig {
	return gatewayEmailNotificationRuntimeConfig{
		SMTPProviderMode: strings.TrimSpace(os.Getenv("EMAIL_SMTP_PROVIDER")),
	}
}

func newGatewayEmailNotificationService(cfg gatewayEmailNotificationRuntimeConfig) (ports.EmailNotificationService, error) {
	switch strings.TrimSpace(cfg.SMTPProviderMode) {
	case "", "local":
		// No SMTP provider — simulate success (local-only mode)
		return runtimeadapter.NewLocalEmailNotificationService(), nil
	case "smtp":
		// Real SMTP provider — actually connects to SMTP server and sends email
		return runtimeadapter.NewLocalEmailNotificationService(
			runtimeadapter.WithSMTPProvider(runtimeadapter.NewSMTPProvider()),
		), nil
	default:
		return nil, fmt.Errorf("%w: unsupported EMAIL_SMTP_PROVIDER %q", ports.ErrUnsupported, cfg.SMTPProviderMode)
	}
}
