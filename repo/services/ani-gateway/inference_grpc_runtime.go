package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

type gatewayInferenceServiceRuntimeConfig struct {
	Addr        string
	CallTimeout time.Duration
}

// gatewayInferenceServiceRuntimeConfigFromEnv 读现网 Gateway 连 inference-service 的地址。
// 默认超时 30s，覆盖同步 Ensure Core 的请求路径。
func gatewayInferenceServiceRuntimeConfigFromEnv() gatewayInferenceServiceRuntimeConfig {
	cfg := gatewayInferenceServiceRuntimeConfig{
		Addr:        strings.TrimSpace(os.Getenv("INFERENCE_SERVICE_GRPC_ADDR")),
		CallTimeout: gatewayDurationFromEnv("INFERENCE_SERVICE_GRPC_CALL_TIMEOUT"),
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 30 * time.Second
	}
	return cfg
}

func newGatewayInferenceServiceClient(ctx context.Context, cfg gatewayInferenceServiceRuntimeConfig) (router.InferenceControlClient, func(), error) {
	if cfg.Addr == "" {
		return nil, nil, nil
	}
	conn, client, err := router.DialInferenceControl(ctx, cfg.Addr, cfg.CallTimeout)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = conn.Close() }, nil
}
