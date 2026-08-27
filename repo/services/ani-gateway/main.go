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

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	h := server.Default(
		server.WithHostPorts(gatewayListenAddr()),
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
	kubernetesRESTClient, err := newGatewayKubernetesClient(gatewayGPUInventoryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure kubernetes rest client for orphan discovery", "err", err)
		os.Exit(1)
	}
	networkService, err := newGatewayNetworkService(gatewayNetworkRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure network provider runtime", "err", err)
		os.Exit(1)
	}
	storageRuntimeCfg := gatewayStorageRuntimeConfigFromEnv()
	storageService, closeStorageRuntime, err := newGatewayStorageService(runtimeCtx, storageRuntimeCfg)
	if err != nil {
		logger.Error("failed to configure storage provider runtime", "err", err)
		os.Exit(1)
	}
	logger.Info("storage provider runtime configured",
		"provider", strings.TrimSpace(storageRuntimeCfg.ProviderMode),
		"object_store", strings.TrimSpace(storageRuntimeCfg.ObjectStoreProvider),
		"control_plane_store", storageNeedsControlPlaneStore(storageRuntimeCfg),
		"router_default_in_memory_service", storageService == nil,
	)
	if closeStorageRuntime != nil {
		defer closeStorageRuntime()
	}
	imageRegistry, closeRegistryRuntime, err := newGatewayImageRegistry(runtimeCtx, gatewayRegistryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure image registry provider runtime", "err", err)
		os.Exit(1)
	}
	if closeRegistryRuntime != nil {
		defer closeRegistryRuntime()
	}
	instanceRuntimeConfig := gatewayInstanceRuntimeConfigFromEnv()
	instanceRuntimeConfig.SharedNetworkService = networkService
	instanceRuntimeConfig.SharedStorageService = storageService
	instanceRuntimeConfig.SharedImageRegistry = imageRegistry
	instanceRuntime, closeInstanceRuntime, err := newGatewayInstanceRuntime(runtimeCtx, instanceRuntimeConfig, secretService)
	if err != nil {
		logger.Error("failed to configure instance provider runtime", "err", err)
		os.Exit(1)
	}
	defer closeInstanceRuntime()
	if instanceRuntime.KubernetesRESTClient != nil {
		kubernetesRESTClient = instanceRuntime.KubernetesRESTClient
		logger.Info("instance provider runtime configured",
			"provider", strings.TrimSpace(instanceRuntimeConfig.WorkloadProvider),
			"persistent_store", true,
			"shared_network_storage_registry", true,
		)
	}
	if instanceRuntime.ReconcileController != nil {
		go func() {
			logger.Info("workload reconcile controller starting")
			if err := instanceRuntime.ReconcileController.Start(runtimeCtx); err != nil {
				logger.Error("workload reconcile controller stopped with error", "err", err)
			}
		}()
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
	gpuSpecStore, err := newGatewayGPUSpecStore(gatewayGPUInventoryRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure gpu spec store runtime", "err", err)
		os.Exit(1)
	}
	vectorStoreRuntimeConfig := gatewayVectorStoreRuntimeConfigFromEnv()
	vectorStoreService, closeVectorStoreRuntime, err := newGatewayVectorStoreService(runtimeCtx, vectorStoreRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure vector store provider runtime", "err", err)
		os.Exit(1)
	}
	if closeVectorStoreRuntime != nil {
		defer closeVectorStoreRuntime()
	}
	if vectorStoreService != nil {
		logger.Info("vector store provider runtime configured",
			"provider", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreProvider),
			"database_configured", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreDatabase) != "",
			"collection_prefix_configured", strings.TrimSpace(vectorStoreRuntimeConfig.VectorStoreCollectionPrefix) != "",
			"control_plane_store", true,
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
	observabilityService, err := newGatewayObservabilityService(gatewayObservabilityRuntimeConfigFromEnv(nil))
	if err != nil {
		logger.Error("failed to configure observability provider runtime", "err", err)
		os.Exit(1)
	}
	if observabilityService != nil {
		logger.Info("observability provider runtime configured",
			"provider", strings.TrimSpace(os.Getenv("INSTANCE_OBSERVABILITY_PROVIDER")),
		)
	}
	inferenceServiceClient, closeInferenceGRPC, err := newGatewayInferenceServiceClient(runtimeCtx, gatewayInferenceServiceRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure inference-service gRPC client", "err", err)
		os.Exit(1)
	}
	if closeInferenceGRPC != nil {
		defer closeInferenceGRPC()
	}
	if inferenceServiceClient != nil {
		logger.Info("inference-service gRPC client configured",
			"addr", strings.TrimSpace(os.Getenv("INFERENCE_SERVICE_GRPC_ADDR")),
		)
	}
	kbServiceClient, closeKBGRPC, err := newGatewayKBServiceClient(runtimeCtx, gatewayKBServiceRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure kb-service gRPC client", "err", err)
		os.Exit(1)
	}
	if closeKBGRPC != nil {
		defer closeKBGRPC()
	}
	if kbServiceClient != nil {
		logger.Info("kb-service gRPC client configured",
			"addr", strings.TrimSpace(os.Getenv("KB_SERVICE_GRPC_ADDR")),
		)
	}
	modelServiceClient, closeModelGRPC, err := newGatewayModelServiceClient(runtimeCtx, gatewayModelServiceRuntimeConfigFromEnv())
	if err != nil {
		logger.Error("failed to configure model-service gRPC client", "err", err)
		os.Exit(1)
	}
	if closeModelGRPC != nil {
		defer closeModelGRPC()
	}
	if modelServiceClient != nil {
		logger.Info("model-service gRPC client configured",
			"addr", strings.TrimSpace(os.Getenv("MODEL_SERVICE_GRPC_ADDR")),
		)
	}
	middleware.StartAuditWorker()
	if err := middleware.Register(h, gatewayStore); err != nil {
		logger.Error("failed to configure gateway authz", "err", err)
		os.Exit(1)
	}
	quotaAdminService, quotaStoreService, quotaMetadataStore, closeQuotaStore, err := newGatewayQuotaStore(runtimeCtx)
	if err != nil {
		logger.Error("failed to configure quota admin store", "err", err)
		os.Exit(1)
	}
	defer closeQuotaStore()
	platformWorkloadRuntimeConfig := gatewayPlatformWorkloadRuntimeConfigFromEnv()
	platformWorkloadService, closePlatformWorkload, err := newGatewayPlatformWorkloadService(runtimeCtx, platformWorkloadRuntimeConfig)
	if err != nil {
		logger.Error("failed to configure platform workload provider runtime", "err", err)
		os.Exit(1)
	}
	defer closePlatformWorkload()
	platformWorkloadProvider := strings.TrimSpace(platformWorkloadRuntimeConfig.ProviderMode)
	if platformWorkloadProvider == "" {
		platformWorkloadProvider = "local"
	}
	logger.Info("platform workload provider runtime configured", "provider", platformWorkloadProvider)
	tenantService, closeTenantStore, err := newGatewayTenantStore(runtimeCtx)
	if err != nil {
		logger.Error("failed to configure tenant admin store", "err", err)
		os.Exit(1)
	}
	defer closeTenantStore()
	gpuInventory, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfigFromEnv(), gpuSchedulingQueueStore, gpuSpecStore, quotaStoreService)
	if err != nil {
		logger.Error("failed to configure gpu inventory provider runtime", "err", err)
		os.Exit(1)
	}
	var routeInstanceRuntime *router.InstanceRuntime
	if instanceRuntime.Service != nil {
		routeInstanceRuntime = &router.InstanceRuntime{
			Service:        instanceRuntime.Service,
			Store:          instanceRuntime.Store,
			Operations:     instanceRuntime.Operations,
			SandboxRuntime: instanceRuntime.SandboxRuntime,
			TaskStore:      instanceRuntime.AsyncTasks,
			RealProvider:   true,
			Provider:       strings.TrimSpace(instanceRuntimeConfig.WorkloadProvider),
		}
	}
	router.RegisterWithOptions(h, router.RegisterOptions{
		K8sClusterService:                     k8sClusterService,
		EncryptionService:                     encryptionService,
		SecretService:                         secretService,
		GPUInventory:                          gpuInventory,
		GPUSchedulingQueueStore:               gpuSchedulingQueueStore,
		GPUInstanceStore:                      gpuInstanceStore,
		NetworkService:                        networkService,
		StorageService:                        storageService,
		ImageRegistry:                         imageRegistry,
		VectorStoreService:                    vectorStoreService,
		InstanceObservability:                 instanceObservability,
		InstanceObservabilityUsesInstanceName: instanceObservabilityUsesInstanceName,
		InstanceRuntime:                       routeInstanceRuntime,
		KubernetesRESTClient:                  kubernetesRESTClient,
		ObservabilityService:                  observabilityService,
		EmailNotificationStore:                runtimeadapter.NewLocalEmailNotificationStore(),
		InferenceServiceClient:                inferenceServiceClient,
		ModelServiceClient:                    modelServiceClient,
		KBServiceClient:                       kbServiceClient,
		KBSSEConfig:                           newGatewaySSEConfig(gatewaySSERuntimeConfigFromEnv()),
		AsyncTaskStore:                        instanceRuntime.AsyncTasks,
		QuotaAdminService:                     quotaAdminService,
		PlatformWorkloadService:               platformWorkloadService,
		TenantService:                         tenantService,
		GPUSpecStore:                          gpuSpecStore,
		MetadataStore:                         quotaMetadataStore,
		QuotaStoreService:                     quotaStoreService,
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

// gatewayListenAddr returns the gateway listen address, defaulting to :8080.
// GATEWAY_LISTEN_ADDR overrides it for local smoke runs / multi-instance tests.
func gatewayListenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("GATEWAY_LISTEN_ADDR")); addr != "" {
		return addr
	}
	return ":8080"
}
