// Package router registers all ANI Gateway API routes.
// Core routes follow /api/v1/{resource}; Services transitional routes follow
// /api/v1/svc/{resource}. Resource handlers are transitional placeholders;
// their method/path surface is checked against Services OpenAPI, while
// response semantics remain non-production until the owning team implements it.
package router

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/pkg/ports"
)

type RegisterOptions struct {
	K8sClusterService                     ports.K8sClusterService
	EncryptionService                     ports.EncryptionService
	SecretService                         ports.SecretService
	GPUInventory                          ports.GPUInventory
	GPUSchedulingQueueStore               ports.GPUSchedulingQueueStore
	NetworkService                        ports.NetworkService
	StorageService                        ports.StorageService
	VectorStoreService                    ports.VectorStoreService
	InstanceObservability                 ports.InstanceObservability
	InstanceObservabilityUsesInstanceName bool
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
	registerObservability(v1)
	registerMetering(v1)
	registerHarbor(v1)
	registerDemoInstancesWithObservability(v1, options.InstanceObservability, options.InstanceObservabilityUsesInstanceName)
	registerGPUInventoryResourcesWithInventory(v1, options.GPUInventory)
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
