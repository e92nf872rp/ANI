package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/kubercloud/ani/pkg/bootstrap"
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
	gpuInventory, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gpu inventory provider runtime", "err", err)
		os.Exit(1)
	}
	networkService, err := newGatewayNetworkService(gatewayNetworkRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure network provider runtime", "err", err)
		os.Exit(1)
	}
	storageService, err := newGatewayStorageService(gatewayStorageRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure storage provider runtime", "err", err)
		os.Exit(1)
	}
	vectorStoreRuntimeConfig := gatewayVectorStoreRuntimeConfigFromEnv()
	vectorStoreService, err := newGatewayVectorStoreService(vectorStoreRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure vector store provider runtime", "err", err)
		os.Exit(1)
	}
	if vectorStoreService != nil {
		logger.Info("vector store provider runtime configured",
			"provider", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreProvider),
			"database_configured", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreDatabase) != "",
			"collection_prefix_configured", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreCollectionPrefix) != "",
		)
	}
	instanceObservabilityRuntimeConfig := gatewayInstanceObservabilityRuntimeConfigFromEnv()
	instanceObservability, instanceObservabilityUsesInstanceName, err := newGatewayInstanceObservability(instanceObservabilityRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure instance observability provider runtime", "err", err)
		os.Exit(1)
	}
	if instanceObservability != nil {
		logger.Info("instance observability provider runtime configured",
			"provider", strings.TrimSpace(instanceObservabilityRuntimeConfig.Provider),
			"prometheus_configured", strings.TrimSpace(instanceObservabilityRuntimeConfig.PrometheusURL) != "",
		)
	}
	gatewayStore, closeGatewayStore, err := bootstrap.ConnectRedisCacheStore(gatewayRedisURLFromEnv())
	if err != nil {
		logger.Error("failed to configure gateway shared store", "err", err)
		os.Exit(1)
	}
	defer closeGatewayStore()
	middleware.StartAuditWorker()
	middleware.Register(h, gatewayStore)
	router.RegisterWithOptions(h, router.RegisterOptions{
		K8sClusterService:                     k8sClusterService,
		EncryptionService:                     encryptionService,
		SecretService:                         secretService,
		GPUInventory:                          gpuInventory,
		NetworkService:                        networkService,
		StorageService:                        storageService,
		VectorStoreService:                    vectorStoreService,
		InstanceObservability:                 instanceObservability,
		InstanceObservabilityUsesInstanceName: instanceObservabilityUsesInstanceName,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		h.Shutdown(context.Background())
	}()

	h.Spin()
}

func gatewayRedisURLFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("GATEWAY_REDIS_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("REDIS_URL")); value != "" {
		return value
	}
	return "redis://:ani_dev_password@127.0.0.1:6379/0"
}
