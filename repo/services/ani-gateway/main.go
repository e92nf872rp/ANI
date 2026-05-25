package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	h := server.Default(
		server.WithHostPorts(":8080"),
		server.WithExitWaitTime(5),
	)

	runtimeCtx := context.Background()
	k8sClusterService, closeK8sClusterRuntime, err := newGatewayK8sClusterRuntime(runtimeCtx, gatewayK8sClusterRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure k8s cluster proxy runtime", "err", err)
		os.Exit(1)
	}
	defer closeK8sClusterRuntime()
	encryptionService, err := newGatewayEncryptionService(gatewayEncryptionRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure encryption provider runtime", "err", err)
		os.Exit(1)
	}
	secretService, err := newGatewaySecretService(gatewaySecretRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure secret provider runtime", "err", err)
		os.Exit(1)
	}
	middleware.StartAuditWorker()
	middleware.Register(h)
	router.RegisterWithOptions(h, router.RegisterOptions{K8sClusterService: k8sClusterService, EncryptionService: encryptionService, SecretService: secretService})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		h.Shutdown(context.Background())
	}()

	h.Spin()
}
