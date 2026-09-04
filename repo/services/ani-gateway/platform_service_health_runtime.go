package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	platformServiceHealthDefaultQueryTimeout = 3 * time.Second
	platformServiceHealthMaxQueryTimeout     = 5 * time.Second
)

type gatewayPlatformServiceHealthRuntimeConfig struct {
	Enabled       bool
	PrometheusURL string
	QueryTimeout  time.Duration
}

func gatewayPlatformServiceHealthRuntimeConfigFromEnv() (gatewayPlatformServiceHealthRuntimeConfig, error) {
	config := gatewayPlatformServiceHealthRuntimeConfig{
		PrometheusURL: strings.TrimSpace(os.Getenv("PLATFORM_SERVICE_HEALTH_PROMETHEUS_URL")),
		QueryTimeout:  platformServiceHealthDefaultQueryTimeout,
	}
	if value := strings.TrimSpace(os.Getenv("PLATFORM_SERVICE_HEALTH_ENABLED")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return gatewayPlatformServiceHealthRuntimeConfig{}, fmt.Errorf("PLATFORM_SERVICE_HEALTH_ENABLED must be a boolean")
		}
		config.Enabled = enabled
	}
	if value := strings.TrimSpace(os.Getenv("PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT")); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 || timeout > platformServiceHealthMaxQueryTimeout {
			return gatewayPlatformServiceHealthRuntimeConfig{}, fmt.Errorf("PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT must be positive and at most %s", platformServiceHealthMaxQueryTimeout)
		}
		config.QueryTimeout = timeout
	}
	if config.Enabled && config.PrometheusURL == "" {
		return gatewayPlatformServiceHealthRuntimeConfig{}, fmt.Errorf("PLATFORM_SERVICE_HEALTH_PROMETHEUS_URL is required when platform service health is enabled")
	}
	return config, nil
}

func newGatewayPlatformServiceHealthReader(config gatewayPlatformServiceHealthRuntimeConfig, logger *slog.Logger) (ports.PlatformServiceHealthReader, error) {
	if !config.Enabled {
		return nil, nil
	}
	reader, err := runtimeadapter.NewPrometheusPlatformServiceHealthReader(runtimeadapter.PrometheusPlatformServiceHealthReaderConfig{
		BaseURL:      config.PrometheusURL,
		QueryTimeout: config.QueryTimeout,
		Logger:       logger,
	})
	if err != nil {
		return nil, fmt.Errorf("configure platform service health reader: %w", err)
	}
	return reader, nil
}
