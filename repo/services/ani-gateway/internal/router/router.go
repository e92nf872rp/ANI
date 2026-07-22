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
	KubernetesRESTClient                  *runtimeadapter.KubernetesRESTClient
	ObservabilityService                  ports.ObservabilityService
	EmailNotificationStore                ports.EmailNotificationStore
}

// Register wires all route groups onto the Hertz server.
func Register(h *server.Hertz) {
	RegisterWithOptions(h, RegisterOptions{})
}

func RegisterWithOptions(h *server.Hertz, options RegisterOptions) {
	// Health/readiness probes (no auth required)
	registerHealth(h.Group(""))

	v1 := h.Group("/api/v1")
	registerBranding(v1)
	registerTasks(v1)
	registerAuth(v1)
	registerMetering(v1)
	registerHarbor(v1, options.ImageRegistry)
	// demo instances 先注册，拿到其 instance service 作为 InstanceLookup，
	// 注入到 ObservabilityService（时序图 PromQL 代理需要解析实例记录的
	// namespace/pod 映射）。注入后再注册 observability 路由。
	instanceLookup := registerDemoInstancesWithObservability(v1, options.InstanceObservability, options.InstanceObservabilityUsesInstanceName, options.GPUInventory, options.KubernetesRESTClient)
	if promSvc, ok := options.ObservabilityService.(*runtimeadapter.PrometheusObservabilityService); ok {
		promSvc.SetInstanceLookup(instanceLookup)
	}
	registerObservability(v1, options.ObservabilityService)
	registerGPUInventoryResourcesWithStore(v1, options.GPUInventory, options.GPUInstanceStore, options.KubernetesRESTClient)
	registerGPUSchedulingResourcesWithStore(v1, options.GPUSchedulingQueueStore)
	registerNetworkResourcesWithService(v1, options.NetworkService)
	registerStorageResourcesWithService(v1, options.StorageService)
	if options.VectorStoreService != nil {
		registerVectorStoreResourcesWithService(v1, options.VectorStoreService)
	} else {
		registerVectorStoreResources(v1)
	}
	registerK8sClusterResourcesWithService(v1, options.K8sClusterService)
	registerEncryptionResourcesWithService(v1, options.EncryptionService)
	registerSecretResourcesWithService(v1, options.SecretService)
	registerEmailNotificationResourcesWithService(v1, options.EmailNotificationStore)

	svc := h.Group("/api/v1/svc")
	registerModels(svc)
	registerInferenceServices(svc)
	registerKnowledgeBases(svc)
	registerGpuContainers(svc)
	registerSandboxes(svc)
	registerTenant(svc)

	// OpenAI-compatible inference proxy (separate URL prefix, no /api prefix)
	h.Group("/v1").POST("/chat/completions", inferenceProxy)
	h.Group("/v1").GET("/inference/stream", inferenceProxy)
}
