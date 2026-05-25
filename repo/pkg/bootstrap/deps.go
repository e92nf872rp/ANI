// Package bootstrap provides standardized dependency initialization for ANI services.
// Every Go microservice calls bootstrap.MustConnect() to get a *Deps,
// then passes it to bootstrap.RunGRPC() to start serving.
package bootstrap

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/adapters/gpu"
	natsadapter "github.com/kubercloud/ani/pkg/adapters/nats"
	"github.com/kubercloud/ani/pkg/adapters/objectstore"
	postgresadapter "github.com/kubercloud/ani/pkg/adapters/postgres"
	redisadapter "github.com/kubercloud/ani/pkg/adapters/redis"
	"github.com/kubercloud/ani/pkg/adapters/registry"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/adapters/vectorstore"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// Capabilities exposes ANI-defined ports for loosely-coupled component access.
// Existing raw clients stay available during the ARCH-ADAPTER migration window.
type Capabilities struct {
	Metadata             ports.MetadataStore
	MessageBus           ports.MessageBus
	Cache                ports.CacheStore
	ObjectStore          ports.ObjectStore
	VectorStore          ports.VectorStore
	VectorStoreResources ports.VectorStoreService
	ImageRegistry        ports.ImageRegistry
	GPUInventory         ports.GPUInventory
	WorkloadRuntime      ports.WorkloadRuntime
	WorkloadRenderer     ports.WorkloadRenderer
	WorkloadAdmission    ports.WorkloadAdmission
	WorkloadPlanAudit    ports.WorkloadPlanAuditStore
	WorkloadDryRun       ports.WorkloadProviderDryRun
	WorkloadApply        ports.WorkloadProviderApply
	WorkloadReconcile    ports.WorkloadStatusReconciler
	WorkloadController   ports.WorkloadReconcileController
	WorkloadStatus       ports.WorkloadProviderStatusReader
	WorkloadInstances    ports.WorkloadInstanceOrchestrator
	WorkloadStore        ports.WorkloadInstanceStore
	WorkloadOperations   ports.WorkloadOperationStore
	WorkloadIdentity     ports.WorkloadIdentityService
	InstanceService      ports.WorkloadInstanceService
	InstanceOps          ports.WorkloadInstanceOps
	NetworkStore         ports.NetworkResourceStore
	NetworkRenderer      ports.NetworkProviderRenderer
	NetworkDryRun        ports.NetworkProviderDryRun
	NetworkApply         ports.NetworkProviderApply
	NetworkStatus        ports.NetworkProviderStatusReader
	NetworkReconcile     ports.NetworkStatusReconciler
	NetworkResources     ports.NetworkService
	StorageStore         ports.StorageResourceStore
	StorageRenderer      ports.StorageProviderRenderer
	StorageDryRun        ports.StorageProviderDryRun
	StorageApply         ports.StorageProviderApply
	StorageStatus        ports.StorageProviderStatusReader
	StorageReconcile     ports.StorageStatusReconciler
	StorageResources     ports.StorageService
}

// Deps holds all initialized external dependencies.
// All fields are non-nil after MustConnect returns successfully.
type Deps struct {
	DB     *pgxpool.Pool
	NATS   *nats.Conn
	JS     nats.JetStreamContext
	Redis  *redis.Client
	Ports  Capabilities
	Logger *slog.Logger

	ServiceName string
	HealthPort  int

	WorkloadReconcileControllerEnabled bool
}

func NewCapabilities(db *pgxpool.Pool, js nats.JetStreamContext, redisClient *redis.Client) Capabilities {
	capabilities, err := NewCapabilitiesWithConfig(db, js, redisClient, Config{})
	if err != nil {
		panic(err)
	}
	return capabilities
}

func NewCapabilitiesWithConfig(db *pgxpool.Pool, js nats.JetStreamContext, redisClient *redis.Client, cfg Config) (Capabilities, error) {
	metadata := postgresadapter.NewMetadataStore(db)
	gpuInventory := gpu.NotConfigured{}
	planner := runtimeadapter.NewPlanningRuntime(runtimeadapter.WithGPUInventory(gpuInventory))
	admission := runtimeadapter.NewLocalAdmissionGuard()
	audit := runtimeadapter.NewMetadataPlanAuditStore(metadata)
	dryRun, apply, statusReader, kubeClient, err := workloadProviderAdapters(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	lifecycle, err := workloadLifecycleExecutor(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	instanceOps, err := workloadOpsExecutor(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	reconciler := runtimeadapter.NewLocalStatusReconciler()
	instanceStore := runtimeadapter.NewMetadataInstanceStore(metadata)
	reconcileController := ports.WorkloadReconcileController(runtimeadapter.NewLocalWorkloadReconcileController(instanceStore, instanceStore, statusReader, reconciler, reconcileControllerConfig(cfg)))
	if cfg.WorkloadReconcileLeaderElectionEnabled {
		elector, err := runtimeadapter.NewMetadataReconcileLeaderElector(metadata, runtimeadapter.MetadataReconcileLeaderElectorConfig{
			LeaseName:            cfg.WorkloadReconcileLeaderLeaseName,
			Identity:             cfg.WorkloadReconcileLeaderIdentity,
			LeaseTTLSeconds:      cfg.WorkloadReconcileLeaderLeaseTTL,
			RenewIntervalSeconds: cfg.WorkloadReconcileLeaderRenewInterval,
		})
		if err != nil {
			return Capabilities{}, err
		}
		reconcileController = runtimeadapter.NewLeaderElectingWorkloadReconcileController(reconcileController, elector)
	}
	operationStore := runtimeadapter.NewMetadataOperationStore(metadata)
	workloadIdentity := runtimeadapter.NewMetadataWorkloadIdentityService(metadata)
	networkStore := runtimeadapter.NewMetadataNetworkStore(metadata)
	networkRenderer := runtimeadapter.NewKubeOVNNetworkRenderer()
	networkProvider := runtimeadapter.NewKubeOVNNetworkProviderAdapter(kubeClient)
	storageStore := runtimeadapter.NewMetadataStorageStore(metadata)
	storageProvider := runtimeadapter.NewKubernetesStorageProviderAdapter(kubeClient)
	orchestrator := runtimeadapter.NewLocalInstanceOrchestrator(
		planner,
		runtimeadapter.NewKubernetesDryRunRenderer(planner),
		admission,
		audit,
		dryRun,
		apply,
		statusReader,
		reconciler,
		runtimeadapter.WithInstanceStore(instanceStore),
		runtimeadapter.WithInstanceOrchestratorWorkloadIdentityService(workloadIdentity),
	)
	return Capabilities{
		Metadata:             metadata,
		MessageBus:           natsadapter.NewMessageBus(js),
		Cache:                redisadapter.NewCacheStore(redisClient),
		ObjectStore:          objectstore.NotConfigured{},
		VectorStore:          vectorstore.NotConfigured{},
		VectorStoreResources: runtimeadapter.NewLocalVectorStoreService(),
		ImageRegistry:        registry.NotConfigured{},
		GPUInventory:         gpuInventory,
		WorkloadRuntime:      planner,
		WorkloadRenderer:     runtimeadapter.NewKubernetesDryRunRenderer(planner),
		WorkloadAdmission:    admission,
		WorkloadPlanAudit:    audit,
		WorkloadDryRun:       dryRun,
		WorkloadApply:        apply,
		WorkloadReconcile:    reconciler,
		WorkloadController:   reconcileController,
		WorkloadStatus:       statusReader,
		WorkloadStore:        instanceStore,
		WorkloadOperations:   operationStore,
		WorkloadIdentity:     workloadIdentity,
		WorkloadInstances:    orchestrator,
		InstanceService: runtimeadapter.NewLocalInstanceServiceWithOptions(
			orchestrator,
			instanceStore,
			instanceOps,
			runtimeadapter.WithOperationStore(operationStore),
			runtimeadapter.WithInstanceLifecycleExecutor(lifecycle),
			runtimeadapter.WithWorkloadIdentityService(workloadIdentity),
		),
		InstanceOps:      instanceOps,
		NetworkStore:     networkStore,
		NetworkRenderer:  networkRenderer,
		NetworkDryRun:    networkProvider,
		NetworkApply:     networkProvider,
		NetworkStatus:    networkProvider,
		NetworkReconcile: runtimeadapter.NewLocalNetworkStatusReconciler(networkStore),
		NetworkResources: runtimeadapter.NewLocalNetworkService(runtimeadapter.WithNetworkResourceStore(networkStore)),
		StorageStore:     storageStore,
		StorageRenderer:  runtimeadapter.NewKubernetesStorageRenderer(),
		StorageDryRun:    storageProvider,
		StorageApply:     storageProvider,
		StorageStatus:    storageProvider,
		StorageReconcile: runtimeadapter.NewLocalStorageStatusReconciler(storageStore),
		StorageResources: runtimeadapter.NewLocalStorageService(runtimeadapter.WithStorageResourceStore(storageStore)),
	}, nil
}

func reconcileControllerConfig(cfg Config) ports.ReconcileControllerConfig {
	return ports.ReconcileControllerConfig{
		NormalIntervalSeconds:   cfg.WorkloadReconcileNormalInterval,
		ActiveIntervalSeconds:   cfg.WorkloadReconcileActiveInterval,
		StaleThresholdSeconds:   cfg.WorkloadReconcileStaleThreshold,
		MaxConcurrentReconciles: cfg.WorkloadReconcileMaxBatch,
		FailureBackoffSeconds:   cfg.WorkloadReconcileFailureBackoff,
	}
}

func workloadProviderAdapters(cfg Config) (ports.WorkloadProviderDryRun, ports.WorkloadProviderApply, ports.WorkloadProviderStatusReader, *runtimeadapter.KubernetesRESTClient, error) {
	switch strings.TrimSpace(cfg.WorkloadProvider) {
	case "", "local":
		return runtimeadapter.NewLocalProviderDryRun(),
			runtimeadapter.NewLocalProviderApply(runtimeadapter.WithProviderApplyEnabled(cfg.WorkloadProviderApplyEnabled)),
			runtimeadapter.NewLocalProviderStatusReader(),
			nil,
			nil
	case "kubernetes_rest":
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:         cfg.KubernetesAPIHost,
			BearerToken:  cfg.KubernetesBearerToken,
			FieldManager: cfg.KubernetesProviderFieldManager,
		})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		adapter := runtimeadapter.NewKubernetesProviderAdapter(
			client,
			runtimeadapter.WithKubernetesProviderApplyEnabled(cfg.WorkloadProviderApplyEnabled),
		)
		return adapter, adapter, adapter, client, nil
	default:
		return nil, nil, nil, nil, fmt.Errorf("%w: unsupported workload provider %q", ports.ErrUnsupported, cfg.WorkloadProvider)
	}
}

func workloadLifecycleExecutor(cfg Config, kubeClient *runtimeadapter.KubernetesRESTClient) (ports.WorkloadInstanceLifecycleExecutor, error) {
	switch strings.TrimSpace(cfg.WorkloadLifecycleProvider) {
	case "", "local":
		return nil, nil
	case "kubernetes_rest":
		client := kubeClient
		if client == nil {
			var err error
			client, err = runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
				Host:         cfg.KubernetesAPIHost,
				BearerToken:  cfg.KubernetesBearerToken,
				FieldManager: cfg.KubernetesProviderFieldManager,
			})
			if err != nil {
				return nil, err
			}
		}
		return runtimeadapter.NewKubernetesLifecycleExecutor(
			client,
			runtimeadapter.WithKubernetesLifecycleEnabled(cfg.WorkloadLifecycleApplyEnabled),
		), nil
	default:
		return nil, fmt.Errorf("%w: unsupported workload lifecycle provider %q", ports.ErrUnsupported, cfg.WorkloadLifecycleProvider)
	}
}

func workloadOpsExecutor(cfg Config, kubeClient *runtimeadapter.KubernetesRESTClient) (ports.WorkloadInstanceOps, error) {
	switch strings.TrimSpace(cfg.WorkloadOpsProvider) {
	case "", "local":
		return runtimeadapter.NewLocalInstanceOpsGuard(runtimeadapter.WithInstanceOpsEnabled(cfg.WorkloadOpsEnabled)), nil
	case "kubernetes_rest":
		client := kubeClient
		if client == nil {
			var err error
			client, err = runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
				Host:         cfg.KubernetesAPIHost,
				BearerToken:  cfg.KubernetesBearerToken,
				FieldManager: cfg.KubernetesProviderFieldManager,
			})
			if err != nil {
				return nil, err
			}
		}
		return runtimeadapter.NewKubernetesInstanceOps(client, runtimeadapter.WithKubernetesInstanceOpsEnabled(cfg.WorkloadOpsEnabled)), nil
	default:
		return nil, fmt.Errorf("%w: unsupported workload ops provider %q", ports.ErrUnsupported, cfg.WorkloadOpsProvider)
	}
}

// Close releases all connections. Call with defer after MustConnect.
func (d *Deps) Close() {
	if d.DB != nil {
		d.DB.Close()
	}
	if d.NATS != nil {
		d.NATS.Close()
	}
	if d.Redis != nil {
		_ = d.Redis.Close()
	}
}
