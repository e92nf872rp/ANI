package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	h := server.Default(
		server.WithHostPorts(gatewayHTTPAddrFromEnv()),
		server.WithExitWaitTime(5),
	)

	runtimeCtx := context.Background()
	metadataStore, closeMetadataStore, err := connectGatewayMetadataStore(runtimeCtx)
	if err != nil {
		logger.Error("failed to configure gateway metadata store", "err", err)
		os.Exit(1)
	}
	defer closeMetadataStore()
	if metadataStore != nil {
		logger.Info("gateway metadata store configured", "database_configured", true)
	}

	objectStore, err := newGatewayObjectStoreFromEnv()
	if err != nil {
		logger.Error("failed to configure gateway object store", "err", err)
		os.Exit(1)
	}

	k8sClusterRuntimeConfig := gatewayK8sClusterRuntimeConfigFromEnv()
	k8sClusterRuntimeConfig.MetadataStore = metadataStore
	k8sClusterService, closeK8sClusterRuntime, err := newGatewayK8sClusterRuntime(runtimeCtx, k8sClusterRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure k8s cluster proxy runtime", "err", err)
		os.Exit(1)
	}
	defer closeK8sClusterRuntime()
	encryptionService, err := newGatewayEncryptionService(gatewayEncryptionRuntimeConfigFromEnv(), metadataStore)
	if err != nil {
		logger.Error("failed to configure encryption provider runtime", "err", err)
		os.Exit(1)
	}
	secretRuntimeConfig := gatewaySecretRuntimeConfigFromEnv()
	secretService, err := newGatewaySecretService(secretRuntimeConfig, metadataStore)
	if err != nil {
		logger.Error("failed to configure secret provider runtime", "err", err)
		os.Exit(1)
	}
	imageImportService, err := newGatewayImageImportService(gatewayImageImportRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure image import provider runtime", "err", err)
		os.Exit(1)
	}
	gpuInventory, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gpu inventory provider runtime", "err", err)
		os.Exit(1)
	}
	networkService, err := newGatewayNetworkService(gatewayNetworkRuntimeConfigFromEnv(), metadataStore)
	if err != nil {
		logger.Error("failed to configure network provider runtime", "err", err)
		os.Exit(1)
	}
	storageService, err := newGatewayStorageService(gatewayStorageRuntimeConfigFromEnv(), metadataStore)
	if err != nil {
		logger.Error("failed to configure storage provider runtime", "err", err)
		os.Exit(1)
	}
	imageRegistry, err := newGatewayImageRegistry(gatewayRegistryRuntimeConfigFromEnv(), metadataStore)
	if err != nil {
		logger.Error("failed to configure image registry provider runtime", "err", err)
		os.Exit(1)
	}
	registryPullSecretKubernetesApply, err := newGatewayRegistryPullSecretKubernetesApply(imageRegistry, secretRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure registry pull secret kubernetes apply runtime", "err", err)
		os.Exit(1)
	}
	vectorStoreRuntimeConfig := gatewayVectorStoreRuntimeConfigFromEnv()
	vectorStoreService, err := newGatewayVectorStoreService(vectorStoreRuntimeConfig, metadataStore)
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
	instanceWorkloadRuntimeConfig := gatewayWorkloadRuntimeConfigFromEnv()
	instanceWorkloadRuntime, err := newGatewayInstanceWorkloadRuntime(instanceWorkloadRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure instance workload provider runtime", "err", err)
		os.Exit(1)
	}
	logger.Info("instance workload provider runtime configured",
		"provider", strings.TrimSpace(instanceWorkloadRuntime.Provider),
		"apply_enabled", instanceWorkloadRuntimeConfig.ProviderApplyEnabled,
		"lifecycle_provider", strings.TrimSpace(instanceWorkloadRuntimeConfig.LifecycleProvider),
		"lifecycle_apply_enabled", instanceWorkloadRuntimeConfig.LifecycleApplyEnabled,
		"ops_provider", strings.TrimSpace(instanceWorkloadRuntimeConfig.OpsProvider),
		"ops_enabled", instanceWorkloadRuntimeConfig.OpsEnabled,
	)
	warnIfReconcileWorkerUnavailable(logger, instanceWorkloadRuntimeConfig)
	gatewayStore, closeGatewayStore, err := bootstrap.ConnectRedisCacheStoreWithConfig(gatewayRedisConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gateway shared store", "err", err)
		os.Exit(1)
	}
	defer closeGatewayStore()
	middleware.StartAuditWorker()
	middleware.Register(h, gatewayStore)
	router.RegisterWithOptions(h, router.RegisterOptions{
		MetadataStore:                         metadataStore,
		BrandingService:                       newGatewayBrandingService(metadataStore, objectStore),
		AsyncTaskService:                      newGatewayAsyncTaskService(metadataStore),
		MeteringService:                       newGatewayMeteringService(metadataStore),
		K8sClusterService:                     k8sClusterService,
		EncryptionService:                     encryptionService,
		SecretService:                         secretService,
		ImageImportService:                    imageImportService,
		GPUInventory:                          gpuInventory,
		ImageRegistry:                         imageRegistry,
		RegistryPullSecretKubernetesApply:     registryPullSecretKubernetesApply,
		NetworkService:                        networkService,
		StorageService:                        storageService,
		VectorStoreService:                    vectorStoreService,
		InstanceWorkloadRuntime:               instanceWorkloadRuntime,
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

func warnIfReconcileWorkerUnavailable(logger *slog.Logger, cfg gatewayWorkloadRuntimeConfig) {
	if logger == nil {
		return
	}
	// Local profile does not rely on async status reconciliation loop.
	usesRealProvider := strings.EqualFold(strings.TrimSpace(cfg.ProviderMode), "kubernetes_rest") ||
		strings.EqualFold(strings.TrimSpace(cfg.LifecycleProvider), "kubernetes_rest") ||
		strings.EqualFold(strings.TrimSpace(cfg.OpsProvider), "kubernetes_rest")
	if !usesRealProvider {
		return
	}
	if value := strings.TrimSpace(os.Getenv("ANI_RECONCILE_WORKER_REQUIRED")); strings.EqualFold(value, "false") {
		return
	}
	reconcileHealthURL := strings.TrimSpace(os.Getenv("ANI_RECONCILE_WORKER_HEALTH_URL"))
	if reconcileHealthURL == "" {
		reconcileHealthURL = "http://127.0.0.1:9205/healthz"
	}
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Get(reconcileHealthURL)
	if err != nil {
		logger.Warn("reconcile-worker health probe failed; instance state may become stale",
			"health_url", reconcileHealthURL,
			"err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Warn("reconcile-worker health probe is not ready; instance state may become stale",
			"health_url", reconcileHealthURL,
			"status_code", resp.StatusCode)
	}
}
