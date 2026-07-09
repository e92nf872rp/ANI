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
	Metadata              ports.MetadataStore
	MessageBus            ports.MessageBus
	Cache                 ports.CacheStore
	KubernetesAPI         ports.HealthChecker
	ObjectStore           ports.ObjectStore
	VectorStore           ports.VectorStore
	VectorStoreResources  ports.VectorStoreService
	ImageRegistry         ports.ImageRegistry
	ImageImport           ports.ImageImportService
	GPUInventory          ports.GPUInventory
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
	imageRegistry, err := imageRegistryAdapter(cfg)
	if err != nil {
		return Capabilities{}, err
	}
	vectorStoreServiceOptions := []runtimeadapter.VectorStoreServiceOption{}
	if strings.TrimSpace(cfg.VectorStoreProvider) == "milvus" {
		vectorStoreServiceOptions = append(vectorStoreServiceOptions, runtimeadapter.WithVectorStoreBackend(vectorStore))
	}
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
		KubernetesAPI:        kubeClient,
		ObjectStore:          objectStore,
		VectorStore:          vectorStore,
		VectorStoreResources: runtimeadapter.NewLocalVectorStoreService(vectorStoreServiceOptions...),
		ImageRegistry:        imageRegistry,
		ImageImport:          runtimeadapter.NewLocalImageImportService(),
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
		InstanceOps:           instanceOps,
		InstanceObservability: instanceObservability,
		NetworkStore:          networkStore,
		NetworkRenderer:       networkRenderer,
		NetworkDryRun:         networkProvider,
		NetworkApply:          networkProvider,
		NetworkStatus:         networkProvider,
		NetworkReconcile:      runtimeadapter.NewLocalNetworkStatusReconciler(networkStore),
		NetworkResources:      runtimeadapter.NewLocalNetworkService(networkServiceOptions...),
		StorageStore:          storageStore,
		StorageRenderer:       runtimeadapter.NewKubernetesStorageRenderer(),
		StorageDryRun:         storageProvider,
		StorageApply:          storageProvider,
		StorageStatus:         storageProvider,
		StorageReconcile:      runtimeadapter.NewLocalStorageStatusReconciler(storageStore),
		StorageResources:      runtimeadapter.NewLocalStorageService(storageServiceOptions...),
	}, nil
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

func imageRegistryAdapter(cfg Config) (ports.ImageRegistry, error) {
	switch strings.TrimSpace(cfg.RegistryProvider) {
	case "", "local":
		return registry.NewLocalImageRegistry(), nil
	case "not_configured":
		return registry.NotConfigured{}, nil
	case "harbor":
		return registry.NewHarborImageRegistry(registry.HarborImageRegistryConfig{
			Endpoint:    cfg.RegistryEndpoint,
			Username:    cfg.RegistryUsername,
			Password:    cfg.RegistryPassword,
			Secure:      cfg.RegistrySecure,
			TLSInsecure: cfg.RegistryTLSInsecure,
		})
	default:
		return nil, fmt.Errorf("%w: unsupported image registry provider %q", ports.ErrUnsupported, cfg.RegistryProvider)
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
	clientCfg := runtimeadapter.KubernetesRESTClientConfig{
		Host:            cfg.KubernetesAPIHost,
		ServiceHost:     cfg.KubernetesServiceHost,
		ServicePort:     cfg.KubernetesServicePort,
		BearerToken:     cfg.KubernetesBearerToken,
		BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
		CAFile:          cfg.KubernetesServiceAccountCAFile,
		FieldManager:    cfg.KubernetesProviderFieldManager,
	}
	if clientCfg.RequestTimeout == 0 {
		clientCfg.RequestTimeout = runtimeadapter.LoadKubernetesRESTEnvFromOS().RequestTimeout
	}
	return clientCfg
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
