package main

import (
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

type gatewayInstanceSessionRuntimeConfig struct {
	Addr        string
	CallTimeout time.Duration
}

func gatewayInstanceSessionRuntimeConfigFromEnv() gatewayInstanceSessionRuntimeConfig {
	cfg := gatewayInstanceSessionRuntimeConfig{
		Addr:        strings.TrimSpace(os.Getenv("SESSION_GATEWAY_GRPC_ADDR")),
		CallTimeout: gatewayDurationFromEnv("SESSION_GATEWAY_GRPC_TIMEOUT"),
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 5 * time.Second
	}
	return cfg
}

func newGatewayInstanceSessionIssuer(cfg gatewayInstanceSessionRuntimeConfig) (ports.InstanceSessionIssuer, func(), error) {
	if cfg.Addr == "" {
		return nil, nil, nil
	}
	conn, issuer, err := router.DialSessionGateway(cfg.Addr, cfg.CallTimeout)
	if err != nil {
		return nil, nil, err
	}
	return issuer, func() { _ = conn.Close() }, nil
}
