package bootstrap

import (
	"context"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type InstanceRuntime struct {
	Service              ports.WorkloadInstanceService
	Store                ports.WorkloadInstanceStore
	Operations           ports.WorkloadOperationStore
	SandboxRuntime       ports.SandboxRuntime
	AsyncTasks           ports.AsyncTaskStore
	KubernetesRESTClient *runtimeadapter.KubernetesRESTClient
	ReconcileController  ports.WorkloadReconcileController
}

func ConnectInstanceService(ctx context.Context, cfg Config) (InstanceRuntime, func(), error) {
	closeRuntime := func() {}
	if err := ctx.Err(); err != nil {
		return InstanceRuntime{}, closeRuntime, err
	}

	cfg = cfg.withEnvironmentOverrides()
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	}
	pool, err := connectDB(cfg.DatabaseURL)
	if err != nil {
		return InstanceRuntime{}, closeRuntime, err
	}

	capabilities, err := NewCapabilitiesWithConfig(pool, nil, nil, instanceRuntimeConfig(cfg))
	if err != nil {
		pool.Close()
		return InstanceRuntime{}, closeRuntime, err
	}
	kubernetesRESTClient, _ := capabilities.KubernetesAPI.(*runtimeadapter.KubernetesRESTClient)
	return InstanceRuntime{
		Service:              capabilities.InstanceService,
		Store:                capabilities.WorkloadStore,
		Operations:           capabilities.WorkloadOperations,
		SandboxRuntime:       capabilities.SandboxRuntime,
		AsyncTasks:           capabilities.AsyncTasks,
		KubernetesRESTClient: kubernetesRESTClient,
		ReconcileController:  capabilities.WorkloadController,
	}, pool.Close, nil
}

func instanceRuntimeConfig(cfg Config) Config {
	return Config{
		WorkloadProvider:                  cfg.WorkloadProvider,
		WorkloadProviderApplyEnabled:      cfg.WorkloadProviderApplyEnabled,
		SecretService:                     cfg.SecretService,
		GPUInventoryProvider:              cfg.GPUInventoryProvider,
		RegistryProviderMode:              cfg.RegistryProviderMode,
		HarborEndpoint:                    cfg.HarborEndpoint,
		HarborUsername:                    cfg.HarborUsername,
		HarborPassword:                    cfg.HarborPassword,
		HarborRequestTimeout:              cfg.HarborRequestTimeout,
		RegistryTLSInsecure:               cfg.RegistryTLSInsecure,
		SharedNetworkService:              cfg.SharedNetworkService,
		SharedStorageService:              cfg.SharedStorageService,
		SharedImageRegistry:               cfg.SharedImageRegistry,
		WorkloadLifecycleProvider:         cfg.WorkloadLifecycleProvider,
		WorkloadLifecycleApplyEnabled:     cfg.WorkloadLifecycleApplyEnabled,
		WorkloadOpsProvider:               cfg.WorkloadOpsProvider,
		WorkloadOpsEnabled:                cfg.WorkloadOpsEnabled,
		KubernetesAPIHost:                 cfg.KubernetesAPIHost,
		KubernetesServiceHost:             cfg.KubernetesServiceHost,
		KubernetesServicePort:             cfg.KubernetesServicePort,
		KubernetesBearerToken:             cfg.KubernetesBearerToken,
		KubernetesServiceAccountTokenFile: cfg.KubernetesServiceAccountTokenFile,
		KubernetesServiceAccountCAFile:    cfg.KubernetesServiceAccountCAFile,
		KubernetesProviderFieldManager:    cfg.KubernetesProviderFieldManager,
		WorkloadReconcileNormalInterval:   cfg.WorkloadReconcileNormalInterval,
		WorkloadReconcileActiveInterval:   cfg.WorkloadReconcileActiveInterval,
		WorkloadReconcileStaleThreshold:   cfg.WorkloadReconcileStaleThreshold,
		WorkloadReconcileMaxBatch:         cfg.WorkloadReconcileMaxBatch,
		WorkloadReconcileFailureBackoff:   cfg.WorkloadReconcileFailureBackoff,
		GPUQuotaEnabled:                   cfg.GPUQuotaEnabled,
		ProvisioningTimeoutMin:            cfg.ProvisioningTimeoutMin,
	}
}
