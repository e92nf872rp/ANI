package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
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
	kubernetesRESTClient, err := newGatewayKubernetesClient(gatewayGPUInventoryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure kubernetes rest client for orphan discovery", "err", err)
		os.Exit(1)
	}
	gpuSchedulingQueueStore, err := newGatewayGPUSchedulingQueueStore(gatewayGPUSchedulingQueueRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gpu scheduling queue store runtime", "err", err)
		os.Exit(1)
	}
	gpuInstanceStore, err := newGatewayGPUInstanceStore(runtimeCtx, gatewayGPUInstanceStoreConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gpu instance store runtime", "err", err)
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
	gatewayStore, closeGatewayStore, err := bootstrap.ConnectRedisCacheStoreWithConfig(gatewayRedisConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gateway shared store", "err", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := closeGatewayStore(); closeErr != nil {
			logger.Error("failed to close gateway shared store", "err", closeErr)
		}
	}()
	middleware.StartAuditWorker()
	middleware.Register(h, gatewayStore)
	router.RegisterWithOptions(h, router.RegisterOptions{
		K8sClusterService:                     k8sClusterService,
		EncryptionService:                     encryptionService,
		SecretService:                         secretService,
		GPUInventory:                          gpuInventory,
		GPUSchedulingQueueStore:               gpuSchedulingQueueStore,
		GPUInstanceStore:                      gpuInstanceStore,
		NetworkService:                        networkService,
		StorageService:                        storageService,
		VectorStoreService:                    vectorStoreService,
		InstanceObservability:                 instanceObservability,
		InstanceObservabilityUsesInstanceName: instanceObservabilityUsesInstanceName,
		KubernetesRESTClient:                  kubernetesRESTClient,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		if shutdownErr := h.Shutdown(context.Background()); shutdownErr != nil {
			logger.Error("failed to shut down gateway", "err", shutdownErr)
		}
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

func gatewayRedisConfigFromEnv() bootstrap.RedisConfig {
	cfg := bootstrap.RedisConfig{URL: gatewayRedisURLFromEnv()}
	mode := firstGatewayEnv("GATEWAY_REDIS_MODE", "REDIS_MODE")
	addrs := firstGatewayEnv("GATEWAY_REDIS_ADDRS", "REDIS_ADDRS")
	if strings.TrimSpace(mode) != "" || strings.TrimSpace(addrs) != "" {
		cfg.URL = ""
		cfg.Mode = strings.TrimSpace(mode)
		cfg.Addrs = splitGatewayCSVEnv(addrs)
	}
	cfg.MasterName = firstGatewayEnv("GATEWAY_REDIS_MASTER_NAME", "REDIS_MASTER_NAME")
	cfg.Username = firstGatewayEnv("GATEWAY_REDIS_USERNAME", "REDIS_USERNAME")
	cfg.Password = firstGatewayEnv("GATEWAY_REDIS_PASSWORD", "REDIS_PASSWORD")
	cfg.SentinelUsername = firstGatewayEnv("GATEWAY_REDIS_SENTINEL_USERNAME", "REDIS_SENTINEL_USERNAME")
	cfg.SentinelPassword = firstGatewayEnv("GATEWAY_REDIS_SENTINEL_PASSWORD", "REDIS_SENTINEL_PASSWORD")
	if value := firstGatewayEnv("GATEWAY_REDIS_DB", "REDIS_DB"); strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.DB = parsed
		}
	}
	return cfg
}

func firstGatewayEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
