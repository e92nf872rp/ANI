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
	InstanceSessionIssuer                 ports.InstanceSessionIssuer
	InstanceObservabilityUsesInstanceName bool
	InstanceRuntime                       *InstanceRuntime
	KubernetesRESTClient                  *runtimeadapter.KubernetesRESTClient
	ObservabilityService                  ports.ObservabilityService
	PlatformServiceHealthReader           ports.PlatformServiceHealthReader
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
	// vllmStreamer is nil the SSE handler degrades to an
	// empty stream so the gateway stays functional without backends.
	KBSSEConfig             KbSSEConfig
	AsyncTaskStore          ports.AsyncTaskStore
	QuotaAdminService       ports.QuotaAdminService
	PlatformWorkloadService ports.PlatformWorkloadService
	TenantService           ports.TenantService
	TenantPlanService       ports.TenantPlanService
	TenantAdminService      ports.TenantAdminService
	// GPUSpecStore backs the GPU spec directory CRUD endpoints (POST/DELETE
	// in gpu_spec_resources.go). When nil those handlers return 503.
	GPUSpecStore ports.GPUSpecStore
	// GPUPartitionPlanner backs POST /gpu-inventory/gpu-partitions (BOSS
	// cluster GPU split, gpu_partition_resources.go). When nil the handler
	// returns 503; the kubernetes_rest inventory adapter implements it.
	GPUPartitionPlanner ports.GPUPartitionPlanner
	// MetadataStore enables platform-scoped (RLS-bypass) queries for the
	// cross-tenant GPUSpecInUse check in gpu_spec_resources.go. When nil
	// the check falls back to a tenant-scoped instanceStore.List.
	MetadataStore ports.MetadataStore
	// QuotaStoreService backs the tenant self-query endpoint GET /quotas/me
	// (GetMy self-opens a tenant-scoped transaction so RLS applies). When nil
	// the handler returns 503.
	QuotaStoreService ports.QuotaStoreService
	// MeteringService backs the metering usage query endpoints
	// (GET /metering/usage + GET /metering/usage/platform). When nil the
	// handlers fall back to the in-process local adapter.
	MeteringService ports.MeteringService
	// PlatformCapacityService backs the platform capacity overview endpoint
	// (GET /platform/capacity). When nil the handler falls back to the
	// local deterministic adapter.
	PlatformCapacityService ports.PlatformCapacityService
	// PlatformAuditService backs the platform audit logs endpoint
	// (GET /platform/audit-logs). Nil is not expected: main always wires
	// the Loki-backed adapter (transitional design, no local fallback).
	PlatformAuditService ports.PlatformAuditService
	// ComponentStatusService backs the platform component status endpoint
	// (GET /platform/components). When nil the handler falls back to the
	// local deterministic adapter.
	ComponentStatusService ports.ComponentStatusService
	// ComponentMetricsReader / ComponentLogReader 背书平台组件诊断接口
	// （GET /platform/components/{name}/metrics、/logs、/logs/stream）。
	// 为 nil 时 handler 回退 local 确定性 adapter。
	ComponentMetricsReader ports.PlatformComponentMetricsReader
	ComponentLogReader     ports.PlatformComponentLogReader
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
	registerAuth(v1)
	registerMetering(v1, options.MeteringService)
	registerPlatformCapacity(v1, options.PlatformCapacityService)
	registerPlatformAudit(v1, options.PlatformAuditService)
	registerComponentStatus(v1, options.ComponentStatusService)
	registerComponentDiagnostics(v1, options.ComponentMetricsReader, options.ComponentLogReader)
	registerHarbor(v1, options.ImageRegistry)
	// Instances register first so their service can act as InstanceLookup.
	// 注入到 ObservabilityService（时序图 PromQL 代理需要解析实例记录的
	// namespace/pod 映射）。注入后再注册 observability 路由。
	if options.InstanceRuntime != nil && options.InstanceRuntime.TaskStore == nil {
		options.InstanceRuntime.TaskStore = options.AsyncTaskStore
	}
	instanceLookup, observeInstance := registerInstancesWithRuntime(v1, options.InstanceObservability, options.InstanceSessionIssuer, options.InstanceObservabilityUsesInstanceName, options.GPUInventory, options.KubernetesRESTClient, options.SecretService, options.InstanceRuntime, options.GPUSpecStore)
	// Tasks register after instances so the lazy-sync observer (store read +
	// single-instance Kubernetes refresh) is available for GET /tasks/{id}.
	registerTasksWithStore(v1, options.AsyncTaskStore, observeInstance)
	if promSvc, ok := options.ObservabilityService.(*runtimeadapter.PrometheusObservabilityService); ok {
		promSvc.SetInstanceLookup(instanceLookup)
	}
	registerObservability(v1, options.ObservabilityService)
	registerPlatformServiceHealth(v1, options.PlatformServiceHealthReader)
	registerGPUInventoryResourcesWithStore(v1, options.GPUInventory, options.GPUInstanceStore, options.KubernetesRESTClient, options.GPUSpecStore, options.QuotaStoreService, options.QuotaAdminService)
	registerGPUSchedulingResourcesWithStore(v1, options.GPUSchedulingQueueStore)
	registerNetworkResourcesWithService(v1, options.NetworkService)
	registerStorageResourcesWithServiceAndTasksAndStore(v1, options.StorageService, options.AsyncTaskStore, options.GPUInstanceStore)
	if options.VectorStoreService != nil {
		registerVectorStoreResourcesWithServiceAndTasks(v1, options.VectorStoreService, options.AsyncTaskStore)
	} else {
		registerVectorStoreResourcesWithServiceAndTasks(v1, nil, options.AsyncTaskStore)
	}
	registerK8sClusterResourcesWithService(v1, options.K8sClusterService)
	registerEncryptionResourcesWithService(v1, options.EncryptionService)
	registerSecretResourcesWithService(v1, options.SecretService)
	registerQuotaResources(v1, options.QuotaAdminService, options.QuotaStoreService)
	registerPlatformWorkloadResources(v1, options.PlatformWorkloadService, options.AsyncTaskStore)
	registerAdminTenantResources(v1, options.TenantService)
	registerAdminTenantAdminResources(v1, options.TenantAdminService)
	registerAdminTenantPlanResources(v1, options.TenantPlanService)
	// GPU spec directory CRUD (POST/DELETE) + reservation management +
	// tenant self-query endpoints (SPEC §4.3).
	registerGPUSpecResources(v1, options.GPUSpecStore, options.GPUInventory, options.GPUInstanceStore, options.MetadataStore)
	// BOSS cluster GPU split (GPU-PARTITION-C): async task accepted + in-process apply.
	registerGPUPartitionResources(v1, options.GPUPartitionPlanner, options.AsyncTaskStore)
	registerReservationResources(v1, options.QuotaAdminService, options.QuotaStoreService)

	svc := h.Group("/api/v1/svc")
	modelServiceClient = options.ModelServiceClient
	registerModels(svc)
	inferenceControlClient = options.InferenceServiceClient
	inferencePolicyClient = nil
	if policyClient, ok := options.InferenceServiceClient.(InferencePolicyClient); ok {
		inferencePolicyClient = policyClient
	}
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
	registerTenantList(svc)
	registerTenantAdmins(svc)

	// OpenAI-compatible chat traffic is served by the independent Envoy AI
	// Gateway data plane, not by this control-plane gateway. Keep the legacy
	// stream placeholder isolated until its ownership is decided separately.
	h.Group("/v1").GET("/inference/stream", legacyInferenceStream)
}
