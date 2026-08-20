package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

type gatewayModelServiceRuntimeConfig struct {
	Addr        string
	CallTimeout time.Duration
}

func gatewayModelServiceRuntimeConfigFromEnv() gatewayModelServiceRuntimeConfig {
	cfg := gatewayModelServiceRuntimeConfig{
		Addr:        strings.TrimSpace(os.Getenv("MODEL_SERVICE_GRPC_ADDR")),
		CallTimeout: gatewayDurationFromEnv("MODEL_SERVICE_GRPC_CALL_TIMEOUT"),
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 5 * time.Second
	}
	return cfg
}

func newGatewayModelServiceClient(ctx context.Context, cfg gatewayModelServiceRuntimeConfig) (router.ModelServiceClient, func(), error) {
	if cfg.Addr == "" {
		return nil, nil, nil
	}
	conn, client, err := router.DialModelService(ctx, cfg.Addr, cfg.CallTimeout)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = conn.Close() }, nil
}
