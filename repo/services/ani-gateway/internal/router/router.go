// Package router registers all ANI Gateway API routes.
// Core routes follow /api/v1/{resource}; Services transitional routes follow
// /api/v1/svc/{resource}. Stubs return 501 until the backing service is
// implemented by the owning team.
package router

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type RegisterOptions struct {
	K8sClusterService                     ports.K8sClusterService
	EncryptionService                     ports.EncryptionService
	SecretService                         ports.SecretService
	GPUInventory                          ports.GPUInventory
	GPUSchedulingQueueStore               ports.GPUSchedulingQueueStore
	GPUInstanceStore                      ports.WorkloadInstanceStore
	NetworkService                        ports.NetworkService
	StorageService                        ports.StorageService
	ImageRegistry                         ports.ImageRegistry
	VectorStoreService                    ports.VectorStoreService
	InstanceObservability                 ports.InstanceObservability
	InstanceObservabilityUsesInstanceName bool
	InstanceRuntime                       *InstanceRuntime
	KubernetesRESTClient                  *runtimeadapter.KubernetesRESTClient
	ObservabilityService                  ports.ObservabilityService
	EmailNotificationStore                ports.EmailNotificationStore
	// InferenceServiceClient routes /api/v1/svc/inference-services* to
	// inference-service via internal InferenceControl gRPC. When nil the
	// product handlers return 503 DEPENDENCY_UNAVAILABLE so the gateway
	// still boots without inference-service configured.
	InferenceServiceClient InferenceControlClient
	// ModelServiceClient routes /api/v1/svc/models* to model-service.
	// When nil the product handlers return 503 DEPENDENCY_UNAVAILABLE.
	// GetModelVersion stays internal and is not registered on Gateway.
	ModelServiceClient ModelServiceClient
	// KBServiceClient routes /api/v1/svc/knowledge-bases/* to kb-service via
	// gRPC. When nil the KB handlers return 503 UNAVAILABLE so the gateway
	// still boots in environments without kb-service configured.
	KBServiceClient KBGRPCClient
	// KBSSEConfig wires the SSE streaming query endpoint (US-017). When
	// ragClient or vllmStreamer is nil the SSE handler degrades to an
	// empty stream so the gateway stays functional without backends.
	KBSSEConfig             KbSSEConfig
	AsyncTaskStore          ports.AsyncTaskStore
	QuotaAdminService       ports.QuotaAdminService
	PlatformWorkloadService ports.PlatformWorkloadService
	TenantService           ports.TenantService
	// GPUSpecStore backs the GPU spec directory CRUD endpoints (POST/DELETE
	// in gpu_spec_resources.go). When nil those handlers return 503.
	GPUSpecStore ports.GPUSpecStore
	// MetadataStore enables platform-scoped (RLS-bypass) queries for the
	// cross-tenant GPUSpecInUse check in gpu_spec_resources.go. When nil
	// the check falls back to a tenant-scoped instanceStore.List.
	MetadataStore ports.MetadataStore
	// QuotaStoreService backs the tenant self-query endpoint GET /quotas/me
	// (GetMy self-opens a tenant-scoped transaction so RLS applies). When nil
	// the handler returns 503.
	QuotaStoreService ports.QuotaStoreService
}

// Register wires all route groups onto the Hertz server.
func Register(h *server.Hertz) {
	RegisterWithOptions(h, RegisterOptions{})
}

func RegisterWithOptions(h *server.Hertz, options RegisterOptions) {
	if options.SecretService == nil {
		options.SecretService = runtimeadapter.NewLocalSecretService()
	}
	// Health/readiness probes (no auth required)
	registerHealth(h.Group(""))

	v1 := h.Group("/api/v1")
	registerBranding(v1)
	registerTasksWithStore(v1, options.AsyncTaskStore)
	registerAuth(v1)
	registerMetering(v1)
	registerHarbor(v1, options.ImageRegistry)
	// Instances register first so their service can act as InstanceLookup.
	// 注入到 ObservabilityService（时序图 PromQL 代理需要解析实例记录的
	// namespace/pod 映射）。注入后再注册 observability 路由。
	if options.InstanceRuntime != nil && options.InstanceRuntime.TaskStore == nil {
		options.InstanceRuntime.TaskStore = options.AsyncTaskStore
	}
	instanceLookup := registerInstancesWithRuntime(v1, options.InstanceObservability, options.InstanceObservabilityUsesInstanceName, options.GPUInventory, options.KubernetesRESTClient, options.SecretService, options.InstanceRuntime, options.GPUSpecStore)
	if promSvc, ok := options.ObservabilityService.(*runtimeadapter.PrometheusObservabilityService); ok {
		promSvc.SetInstanceLookup(instanceLookup)
	}
	registerObservability(v1, options.ObservabilityService)
	registerGPUInventoryResourcesWithStore(v1, options.GPUInventory, options.GPUInstanceStore, options.KubernetesRESTClient, options.GPUSpecStore, options.QuotaStoreService, options.QuotaAdminService)
	registerGPUSchedulingResourcesWithStore(v1, options.GPUSchedulingQueueStore)
	registerNetworkResourcesWithService(v1, options.NetworkService)
	registerStorageResourcesWithServiceAndTasks(v1, options.StorageService, options.AsyncTaskStore)
	if options.VectorStoreService != nil {
		registerVectorStoreResourcesWithServiceAndTasks(v1, options.VectorStoreService, options.AsyncTaskStore)
	} else {
		registerVectorStoreResourcesWithServiceAndTasks(v1, nil, options.AsyncTaskStore)
	}
	registerK8sClusterResourcesWithService(v1, options.K8sClusterService)
	registerEncryptionResourcesWithService(v1, options.EncryptionService)
	registerSecretResourcesWithService(v1, options.SecretService)
	registerEmailNotificationResourcesWithService(v1, options.EmailNotificationStore)
	registerQuotaResources(v1, options.QuotaAdminService, options.QuotaStoreService)
	registerPlatformWorkloadResources(v1, options.PlatformWorkloadService, options.AsyncTaskStore)
	registerAdminTenantResources(v1, options.TenantService)
	// GPU spec directory CRUD (POST/DELETE) + reservation management +
	// tenant self-query endpoints (SPEC §4.3).
	registerGPUSpecResources(v1, options.GPUSpecStore, options.GPUInventory, options.GPUInstanceStore, options.MetadataStore)
	registerReservationResources(v1, options.QuotaAdminService, options.QuotaStoreService)

	svc := h.Group("/api/v1/svc")
	modelServiceClient = options.ModelServiceClient
	registerModels(svc)
	inferenceControlClient = options.InferenceServiceClient
	inferenceImageRegistry = options.ImageRegistry
	registerInferenceServices(svc)
	// Inject the KB gRPC client + SSE wiring into the package-level holders
	// before registering the KB surface (Spec-split contract requires the
	// single-argument registerKnowledgeBases(svc) form).
	kbInjectedClient = options.KBServiceClient
	kbInjectedSSEConfig = options.KBSSEConfig
	registerKnowledgeBases(svc)
	registerGpuContainers(svc)
	registerSandboxes(svc)
	registerTenant(svc)
	registerTenantPlans(svc)

	// OpenAI-compatible inference proxy (separate URL prefix, no /api prefix)
	h.Group("/v1").POST("/chat/completions", inferenceProxy)
	h.Group("/v1").GET("/inference/stream", inferenceProxy)
}
