// Package bootstrap provides standardized dependency initialization for ANI services.
// Every Go microservice calls bootstrap.MustConnect() to get a *Deps,
// then passes it to bootstrap.RunGRPC() to start serving.
package bootstrap

import (
	"fmt"
	"log/slog"
	"net"
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
	Metadata              ports.MetadataStore
	MessageBus            ports.MessageBus
	Cache                 ports.CacheStore
	KubernetesAPI         ports.HealthChecker
	ObjectStore           ports.ObjectStore
	VectorStore           ports.VectorStore
	VectorStoreResources  ports.VectorStoreService
	ImageRegistry         ports.ImageRegistry
	GPUInventory          ports.GPUInventory
	GPUSpecs              ports.GPUSpecService
	QuotaService          ports.QuotaService
	QuotaStore            ports.QuotaStoreService
	GPUSpecStore          ports.GPUSpecStore
	WorkloadRuntime       ports.WorkloadRuntime
	WorkloadRenderer      ports.WorkloadRenderer
	WorkloadAdmission     ports.WorkloadAdmission
	WorkloadPlanAudit     ports.WorkloadPlanAuditStore
	WorkloadDryRun        ports.WorkloadProviderDryRun
	WorkloadApply         ports.WorkloadProviderApply
	WorkloadReconcile     ports.WorkloadStatusReconciler
	WorkloadController    ports.WorkloadReconcileController
	WorkloadStatus        ports.WorkloadProviderStatusReader
	WorkloadInstances     ports.WorkloadInstanceOrchestrator
	WorkloadStore         ports.WorkloadInstanceStore
	WorkloadOperations    ports.WorkloadOperationStore
	WorkloadIdentity      ports.WorkloadIdentityService
	SandboxRuntime        ports.SandboxRuntime
	AsyncTasks            ports.AsyncTaskStore
	SecretService         ports.SecretService
	InstanceService       ports.WorkloadInstanceService
	InstanceOps           ports.WorkloadInstanceOps
	InstanceObservability ports.InstanceObservability
	NetworkStore          ports.NetworkResourceStore
	NetworkRenderer       ports.NetworkProviderRenderer
	NetworkDryRun         ports.NetworkProviderDryRun
	NetworkApply          ports.NetworkProviderApply
	NetworkStatus         ports.NetworkProviderStatusReader
	NetworkReconcile      ports.NetworkStatusReconciler
	NetworkResources      ports.NetworkService
	StorageStore          ports.StorageResourceStore
	StorageRenderer       ports.StorageProviderRenderer
	StorageDryRun         ports.StorageProviderDryRun
	StorageApply          ports.StorageProviderApply
	StorageStatus         ports.StorageProviderStatusReader
	StorageReconcile      ports.StorageStatusReconciler
	StorageResources      ports.StorageService
}

// Deps holds all initialized external dependencies.
// All fields are non-nil after MustConnect returns successfully.
type Deps struct {
	DB     *pgxpool.Pool
	NATS   *nats.Conn
	JS     nats.JetStreamContext
	Redis  redis.UniversalClient
	Ports  Capabilities
	Logger *slog.Logger

	ServiceName string
	HealthPort  int

	WorkloadReconcileControllerEnabled bool
}

func NewCapabilities(db *pgxpool.Pool, js nats.JetStreamContext, redisClient redis.UniversalClient) Capabilities {
	capabilities, err := NewCapabilitiesWithConfig(db, js, redisClient, Config{})
	if err != nil {
		panic(err)
	}
	return capabilities
}

func NewCapabilitiesWithConfig(db *pgxpool.Pool, js nats.JetStreamContext, redisClient redis.UniversalClient, cfg Config) (Capabilities, error) {
	metadata := postgresadapter.NewMetadataStore(db)
	admission := runtimeadapter.NewLocalAdmissionGuard()
	audit := runtimeadapter.NewMetadataPlanAuditStore(metadata)
	dryRun, apply, statusReader, kubeClient, err := workloadProviderAdapters(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	gpuInventory, err := gpuInventoryAdapter(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	localGPUSpecs := runtimeadapter.NewLocalGPUSpecService(gpuInventory)
	planner := runtimeadapter.NewPlanningRuntime(runtimeadapter.WithGPUInventory(gpuInventory))
	lifecycle, err := workloadLifecycleExecutor(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	instanceOps, err := workloadOpsExecutor(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	instanceObservability, err := instanceObservabilityAdapter(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	reconciler := runtimeadapter.NewLocalStatusReconciler()
	instanceStore := runtimeadapter.NewMetadataInstanceStore(metadata)

	// GPU quota wiring (SPEC §5.1). PostgresQuota implements both
	// QuotaService (Try/TryMany/TryTx/TryManyTx/Confirm/Cancel/Release)
	// and QuotaStoreService (GetMy/Put/List). CRDGPUSpecStore backs the
	// VolcanoResourceTranslator. All are nil-safe: when GPUQuotaEnabled
	// is false the orchestrator and reconciler bypass quota entirely.
	quotaService := runtimeadapter.NewPostgresQuota(metadata)

	// Inject quota admin into the GPU inventory so ListSpecAvailability
	// uses allocated_gpu_count (reservation) per plan.md §4.4.1.
	if kgi, ok := gpuInventory.(*runtimeadapter.KubernetesGPUInventory); ok {
		kgi.WithQuotaAdmin(quotaService)
	}

	var gpuSpecStore ports.GPUSpecStore
	var volcanoTranslator *runtimeadapter.VolcanoResourceTranslator
	if kubeClient != nil {
		gpuSpecStore = runtimeadapter.NewCRDGPUSpecStore(runtimeadapter.CRDGPUSpecStoreConfig{
			Doer:    kubeClient,
			BaseURL: kubernetesAPIBaseURL(cfg),
		})
		volcanoTranslator = runtimeadapter.NewVolcanoResourceTranslator(gpuSpecStore)
	}
	gpuSpecs := runtimeadapter.NewCompositeGPUSpecService(gpuSpecStore, localGPUSpecs)

	reconcileControllerOptions := []runtimeadapter.ReconcileControllerOption{}
	if cfg.GPUQuotaEnabled {
		outboxWriter := runtimeadapter.NewMetadataOutboxWriter()
		reconcileControllerOptions = append(reconcileControllerOptions,
			runtimeadapter.WithQuotaService(quotaService),
			runtimeadapter.WithMetadataStore(metadata),
			runtimeadapter.WithWorkloadInstanceStoreTx(instanceStore),
			runtimeadapter.WithOutboxWriter(outboxWriter),
		)
		if cfg.ProvisioningTimeoutMin > 0 {
			reconcileControllerOptions = append(reconcileControllerOptions,
				runtimeadapter.WithProvisioningTimeoutMin(cfg.ProvisioningTimeoutMin))
		}
	}
	reconcileController := ports.WorkloadReconcileController(
		runtimeadapter.NewLocalWorkloadReconcileController(instanceStore, instanceStore, statusReader, reconciler, reconcileControllerConfig(cfg), reconcileControllerOptions...),
	)
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
	imageRegistry, err := imageRegistryAdapter(cfg, instanceStore, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	resourceRegistry := imageRegistry
	if _, notConfigured := imageRegistry.(registry.NotConfigured); notConfigured {
		resourceRegistry = nil
	}
	networkStore := runtimeadapter.NewMetadataNetworkStore(metadata)
	networkRenderer := runtimeadapter.NewKubeOVNNetworkRenderer()
	networkProvider, err := networkProviderAdapter(cfg, kubeClient)
	if err != nil {
		return Capabilities{}, err
	}
	networkServiceOptions := []runtimeadapter.NetworkServiceOption{
		runtimeadapter.WithNetworkResourceStore(networkStore),
	}
	if strings.TrimSpace(cfg.NetworkProvider) == "kubeovn_rest" {
		networkServiceOptions = append(networkServiceOptions, runtimeadapter.WithNetworkRouteProvider(
			networkRenderer,
			networkProvider,
			networkProvider,
			networkProvider,
			runtimeadapter.NetworkProviderExecutionConfig{
				UserID:          cfg.NetworkProviderUserID,
				PermissionProof: cfg.NetworkProviderPermissionProof,
			},
		))
	}
	storageStore := runtimeadapter.NewMetadataStorageStore(metadata)
	storageProvider := runtimeadapter.NewKubernetesStorageProviderAdapter(kubeClient)
	objectStore, err := objectStoreAdapter(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	storageServiceOptions := []runtimeadapter.StorageServiceOption{
		runtimeadapter.WithStorageResourceStore(storageStore),
	}
	switch strings.TrimSpace(cfg.StorageProvider) {
	case "", "local", "not_configured":
	case "kubernetes_rest":
		if strings.TrimSpace(cfg.StorageProviderUserID) == "" || strings.TrimSpace(cfg.StorageProviderPermissionProof) == "" {
			return Capabilities{}, fmt.Errorf("%w: storage provider requires STORAGE_PROVIDER_USER_ID and STORAGE_PROVIDER_PERMISSION_PROOF", ports.ErrInvalid)
		}
		storageProvider = runtimeadapter.NewKubernetesStorageProviderAdapter(
			kubeClient,
			runtimeadapter.WithKubernetesStorageProviderApplyEnabled(cfg.StorageProviderApplyEnabled),
		)
		storageServiceOptions = append(storageServiceOptions, runtimeadapter.WithStorageProvider(
			runtimeadapter.NewKubernetesStorageRenderer(),
			storageProvider,
			storageProvider,
			storageProvider,
			runtimeadapter.StorageProviderExecutionConfig{
				UserID:          cfg.StorageProviderUserID,
				PermissionProof: cfg.StorageProviderPermissionProof,
			},
		))
	default:
		return Capabilities{}, fmt.Errorf("%w: unsupported storage provider %q", ports.ErrUnsupported, cfg.StorageProvider)
	}
	if strings.TrimSpace(cfg.ObjectStoreProvider) == "minio" {
		storageServiceOptions = append(storageServiceOptions, runtimeadapter.WithStorageObjectStore(objectStore))
	}
	vectorStore, err := vectorStoreAdapter(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	vectorStoreServiceOptions := []runtimeadapter.VectorStoreServiceOption{}
	if strings.TrimSpace(cfg.VectorStoreProvider) == "milvus" {
		vectorStoreServiceOptions = append(vectorStoreServiceOptions, runtimeadapter.WithVectorStoreBackend(vectorStore))
	}
	networkResources := runtimeadapter.NewLocalNetworkService(networkServiceOptions...)
	storageResources := runtimeadapter.NewLocalStorageService(storageServiceOptions...)
	resolverNetwork := ports.NetworkService(networkResources)
	resolverStorage := ports.StorageService(storageResources)
	if cfg.SharedNetworkService != nil {
		resolverNetwork = cfg.SharedNetworkService
	}
	if cfg.SharedStorageService != nil {
		resolverStorage = cfg.SharedStorageService
	}
	if cfg.SharedImageRegistry != nil {
		imageRegistry = cfg.SharedImageRegistry
		resourceRegistry = cfg.SharedImageRegistry
		if _, notConfigured := imageRegistry.(registry.NotConfigured); notConfigured {
			resourceRegistry = nil
		}
	}
	secretService := cfg.SecretService
	if secretService == nil {
		secretService = runtimeadapter.NewLocalSecretService()
	}
	var sandboxRuntime ports.SandboxRuntime = runtimeadapter.NewLocalSandboxRuntime()
	if cfg.WorkloadProviderApplyEnabled && kubeClient != nil {
		sandboxRuntime = runtimeadapter.NewKubernetesSandboxRuntime(
			kubeClient,
			runtimeadapter.WithKubernetesSandboxApplyEnabled(true),
		)
	}
	orchestratorOptions := []runtimeadapter.InstanceOrchestratorOption{
		runtimeadapter.WithInstanceStore(instanceStore),
		runtimeadapter.WithInstanceOrchestratorWorkloadIdentityService(workloadIdentity),
	}
	if cfg.GPUQuotaEnabled {
		orchestratorOptions = append(orchestratorOptions,
			runtimeadapter.WithInstanceOrchestratorQuotaService(quotaService),
			runtimeadapter.WithInstanceOrchestratorMetadataStore(metadata),
			runtimeadapter.WithInstanceOrchestratorStoreTx(instanceStore),
		)
	}
	// Volcano resource translation is a Core capability independent of
	// GPU_QUOTA_ENABLED (plan.md §4.7). Inject into the inner orchestrator
	// so it works even when quota is disabled.
	if volcanoTranslator != nil {
		orchestratorOptions = append(orchestratorOptions,
			runtimeadapter.WithInstanceOrchestratorTranslator(volcanoTranslator))
	}
	innerOrchestrator := runtimeadapter.NewLocalInstanceOrchestrator(
		planner,
		runtimeadapter.NewKubernetesDryRunRenderer(planner),
		admission,
		audit,
		dryRun,
		apply,
		statusReader,
		reconciler,
		orchestratorOptions...,
	)
	orchestrator := ports.WorkloadInstanceOrchestrator(innerOrchestrator)
	if cfg.GPUQuotaEnabled {
		outboxWriter := runtimeadapter.NewMetadataOutboxWriter()
		quotaAwareOptions := []runtimeadapter.QuotaAwareInstanceOrchestratorOption{
			runtimeadapter.WithQuotaAwareQuotaEnabled(true),
			runtimeadapter.WithQuotaAwareQuotaService(quotaService),
			runtimeadapter.WithQuotaAwareQuotaStore(quotaService),
			runtimeadapter.WithQuotaAwareQuotaAdmin(quotaService),
			runtimeadapter.WithQuotaAwareMetadataStore(metadata),
			runtimeadapter.WithQuotaAwareStoreTx(instanceStore),
			runtimeadapter.WithQuotaAwareStore(instanceStore),
			runtimeadapter.WithQuotaAwareOutboxWriter(outboxWriter),
		}
		if volcanoTranslator != nil {
			quotaAwareOptions = append(quotaAwareOptions,
				runtimeadapter.WithQuotaAwareTranslator(volcanoTranslator))
		}
		orchestrator = runtimeadapter.NewQuotaAwareInstanceOrchestrator(innerOrchestrator, quotaAwareOptions...)
	}
	return Capabilities{
		Metadata:             metadata,
		MessageBus:           natsadapter.NewMessageBus(js, slog.Default()),
		Cache:                redisadapter.NewCacheStore(redisClient),
		KubernetesAPI:        kubeClient,
		ObjectStore:          objectStore,
		VectorStore:          vectorStore,
		VectorStoreResources: runtimeadapter.NewLocalVectorStoreService(vectorStoreServiceOptions...),
		ImageRegistry:        imageRegistry,
		GPUInventory:         gpuInventory,
		GPUSpecs:             gpuSpecs,
		QuotaService:         quotaService,
		QuotaStore:           quotaService,
		GPUSpecStore:         gpuSpecStore,
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
		SandboxRuntime:       sandboxRuntime,
		AsyncTasks:           runtimeadapter.NewMetadataAsyncTaskStore(metadata),
		SecretService:        secretService,
		WorkloadInstances:    orchestrator,
		InstanceService: runtimeadapter.NewLocalInstanceServiceWithOptions(
			orchestrator,
			instanceStore,
			instanceOps,
			append([]runtimeadapter.InstanceServiceOption{
				runtimeadapter.WithOperationStore(operationStore),
				runtimeadapter.WithInstanceLifecycleExecutor(lifecycle),
				runtimeadapter.WithWorkloadIdentityService(workloadIdentity),
				runtimeadapter.WithSandboxRuntime(sandboxRuntime),
				runtimeadapter.WithInstanceStorageService(resolverStorage),
				runtimeadapter.WithInstanceResourceResolver(runtimeadapter.NewLocalInstanceResourceResolverWithDependencies(resolverNetwork, resolverStorage, gpuSpecs, resourceRegistry, secretService)),
			}, instanceServiceQuotaOptions(cfg, quotaService, metadata, instanceStore)...)...,
		),
		InstanceOps:           instanceOps,
		InstanceObservability: instanceObservability,
		NetworkStore:          networkStore,
		NetworkRenderer:       networkRenderer,
		NetworkDryRun:         networkProvider,
		NetworkApply:          networkProvider,
		NetworkStatus:         networkProvider,
		NetworkReconcile:      runtimeadapter.NewLocalNetworkStatusReconciler(networkStore),
		NetworkResources:      resolverNetwork,
		StorageStore:          storageStore,
		StorageRenderer:       runtimeadapter.NewKubernetesStorageRenderer(),
		StorageDryRun:         storageProvider,
		StorageApply:          storageProvider,
		StorageStatus:         storageProvider,
		StorageReconcile:      runtimeadapter.NewLocalStorageStatusReconciler(storageStore),
		StorageResources:      resolverStorage,
	}, nil
}

// instanceServiceQuotaOptions returns the quota-related InstanceService
// options gated by cfg.GPUQuotaEnabled, matching the inner orchestrator and
// reconciler gating. When GPU_QUOTA_ENABLED=false the quota service,
// metadata store and store-tx are NOT injected, so persistLifecycleWithQuota
// falls back to plain store.UpsertStatus and never calls TryManyTx.
func instanceServiceQuotaOptions(cfg Config, quotaService ports.QuotaService, metadata ports.MetadataStore, instanceStore ports.WorkloadInstanceStoreTx) []runtimeadapter.InstanceServiceOption {
	if !cfg.GPUQuotaEnabled {
		return nil
	}
	return []runtimeadapter.InstanceServiceOption{
		runtimeadapter.WithInstanceQuotaService(quotaService),
		runtimeadapter.WithInstanceMetadataStore(metadata),
		runtimeadapter.WithInstanceStoreTx(instanceStore),
	}
}

func gpuInventoryAdapter(cfg Config, kubeClient *runtimeadapter.KubernetesRESTClient) (ports.GPUInventory, error) {
	switch strings.TrimSpace(cfg.GPUInventoryProvider) {
	case "", "local", "not_configured":
		return gpu.NotConfigured{}, nil
	case "kubernetes_rest":
		client := kubeClient
		if client == nil {
			var err error
			client, err = runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
			if err != nil {
				return nil, err
			}
		}
		return runtimeadapter.NewKubernetesGPUInventory(client), nil
	default:
		return nil, fmt.Errorf("%w: unsupported GPU inventory provider %q", ports.ErrUnsupported, cfg.GPUInventoryProvider)
	}
}

func imageRegistryAdapter(cfg Config, instanceStore ports.WorkloadInstanceStore, kubeClient *runtimeadapter.KubernetesRESTClient) (ports.ImageRegistry, error) {
	switch strings.TrimSpace(cfg.RegistryProviderMode) {
	case "", "local", "not_configured":
		return registry.NotConfigured{}, nil
	case "harbor":
		client := kubeClient
		if client == nil {
			var err error
			client, err = runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
			if err != nil {
				return nil, err
			}
		}
		return registry.NewHarborImageRegistry(registry.HarborImageRegistryConfig{
			Endpoint:           cfg.HarborEndpoint,
			Username:           cfg.HarborUsername,
			Password:           cfg.HarborPassword,
			RequestTimeout:     cfg.HarborRequestTimeout,
			InsecureSkipVerify: cfg.RegistryTLSInsecure,
			PullSecretWriter:   registry.NewKubernetesPullSecretWriter(client),
			ReferenceReader:    registry.NewWorkloadImageReferenceReader(instanceStore),
		})
	default:
		return nil, fmt.Errorf("%w: unsupported REGISTRY_PROVIDER_MODE %q", ports.ErrUnsupported, cfg.RegistryProviderMode)
	}
}

func networkProviderAdapter(cfg Config, kubeClient *runtimeadapter.KubernetesRESTClient) (*runtimeadapter.KubeOVNNetworkProviderAdapter, error) {
	switch strings.TrimSpace(cfg.NetworkProvider) {
	case "", "local", "not_configured":
		return runtimeadapter.NewKubeOVNNetworkProviderAdapter(kubeClient), nil
	case "kubeovn_rest":
		if strings.TrimSpace(cfg.NetworkProviderUserID) == "" || strings.TrimSpace(cfg.NetworkProviderPermissionProof) == "" {
			return nil, fmt.Errorf("%w: network provider requires NETWORK_PROVIDER_USER_ID and NETWORK_PROVIDER_PERMISSION_PROOF", ports.ErrInvalid)
		}
		client := kubeClient
		if client == nil {
			var err error
			client, err = runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
			if err != nil {
				return nil, err
			}
		}
		return runtimeadapter.NewKubeOVNNetworkProviderAdapter(
			client,
			runtimeadapter.WithKubeOVNNetworkProviderApplyEnabled(cfg.NetworkProviderApplyEnabled),
		), nil
	default:
		return nil, fmt.Errorf("%w: unsupported network provider %q", ports.ErrUnsupported, cfg.NetworkProvider)
	}
}

func objectStoreAdapter(cfg Config) (ports.ObjectStore, error) {
	switch strings.TrimSpace(cfg.ObjectStoreProvider) {
	case "", "local", "not_configured":
		return objectstore.NotConfigured{}, nil
	case "minio":
		return objectstore.NewMinIOObjectStore(objectstore.MinIOObjectStoreConfig{
			Endpoint:        cfg.ObjectStoreEndpoint,
			Endpoints:       cfg.ObjectStoreEndpoints,
			PublicEndpoint:  cfg.ObjectStorePublicEndpoint,
			AccessKeyID:     cfg.ObjectStoreAccessKeyID,
			SecretAccessKey: cfg.ObjectStoreSecretAccessKey,
			SessionToken:    cfg.ObjectStoreSessionToken,
			Region:          cfg.ObjectStoreRegion,
			Secure:          cfg.ObjectStoreSecure,
			BucketPrefix:    cfg.ObjectStoreBucketPrefix,
		})
	default:
		return nil, fmt.Errorf("%w: unsupported object store provider %q", ports.ErrUnsupported, cfg.ObjectStoreProvider)
	}
}

func vectorStoreAdapter(cfg Config) (ports.VectorStore, error) {
	switch strings.TrimSpace(cfg.VectorStoreProvider) {
	case "", "local", "not_configured":
		return vectorstore.NotConfigured{}, nil
	case "milvus":
		return vectorstore.NewMilvusVectorStore(vectorstore.MilvusVectorStoreConfig{
			Endpoint:         cfg.VectorStoreEndpoint,
			Endpoints:        cfg.VectorStoreEndpoints,
			Token:            cfg.VectorStoreToken,
			Database:         cfg.VectorStoreDatabase,
			CollectionPrefix: cfg.VectorStoreCollectionPrefix,
		})
	default:
		return nil, fmt.Errorf("%w: unsupported vector store provider %q", ports.ErrUnsupported, cfg.VectorStoreProvider)
	}
}

func instanceObservabilityAdapter(cfg Config) (ports.InstanceObservability, error) {
	switch strings.TrimSpace(cfg.InstanceObservabilityProvider) {
	case "", "local", "not_configured":
		return runtimeadapter.NewLocalInstanceObservabilityService(), nil
	case "prometheus_kubernetes":
		return runtimeadapter.NewPrometheusInstanceObservability(runtimeadapter.PrometheusInstanceObservabilityConfig{
			PrometheusURL:                     cfg.InstanceObservabilityPrometheusURL,
			KubernetesAPIHost:                 cfg.KubernetesAPIHost,
			KubernetesServiceHost:             cfg.KubernetesServiceHost,
			KubernetesServicePort:             cfg.KubernetesServicePort,
			KubernetesBearerToken:             cfg.KubernetesBearerToken,
			KubernetesServiceAccountTokenFile: cfg.KubernetesServiceAccountTokenFile,
			KubernetesServiceAccountCAFile:    cfg.KubernetesServiceAccountCAFile,
			KubernetesFieldManager:            cfg.KubernetesProviderFieldManager,
			ExecBaseURL:                       cfg.InstanceObservabilityExecBaseURL,
		})
	default:
		return nil, fmt.Errorf("%w: unsupported instance observability provider %q", ports.ErrUnsupported, cfg.InstanceObservabilityProvider)
	}
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
		client, err := runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
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
			client, err = runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
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
			client, err = runtimeadapter.NewKubernetesRESTClient(kubernetesRESTClientConfig(cfg))
			if err != nil {
				return nil, err
			}
		}
		return runtimeadapter.NewKubernetesInstanceOps(client, runtimeadapter.WithKubernetesInstanceOpsEnabled(cfg.WorkloadOpsEnabled)), nil
	default:
		return nil, fmt.Errorf("%w: unsupported workload ops provider %q", ports.ErrUnsupported, cfg.WorkloadOpsProvider)
	}
}

func kubernetesRESTClientConfig(cfg Config) runtimeadapter.KubernetesRESTClientConfig {
	return runtimeadapter.KubernetesRESTClientConfig{
		Host:            cfg.KubernetesAPIHost,
		ServiceHost:     cfg.KubernetesServiceHost,
		ServicePort:     cfg.KubernetesServicePort,
		BearerToken:     cfg.KubernetesBearerToken,
		BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
		CAFile:          cfg.KubernetesServiceAccountCAFile,
		FieldManager:    cfg.KubernetesProviderFieldManager,
	}
}

// kubernetesAPIBaseURL derives the K8s API base URL from the bootstrap
// config, mirroring kubernetesRESTHost in the runtime adapter. Used to
// construct the CRDGPUSpecStore base URL without reaching into the
// private host field of KubernetesRESTClient.
func kubernetesAPIBaseURL(cfg Config) string {
	host := strings.TrimRight(strings.TrimSpace(cfg.KubernetesAPIHost), "/")
	if host != "" {
		return host
	}
	serviceHost := strings.TrimSpace(cfg.KubernetesServiceHost)
	servicePort := strings.TrimSpace(cfg.KubernetesServicePort)
	if serviceHost == "" {
		return ""
	}
	if servicePort == "" {
		servicePort = "443"
	}
	return "https://" + net.JoinHostPort(serviceHost, servicePort)
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
