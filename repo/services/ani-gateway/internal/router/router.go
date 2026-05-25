// Package router registers all ANI Gateway API routes.
// Core routes follow /api/v1/{resource}; Services transitional routes follow
// /api/v1/svc/{resource}. Stubs return 501 until the backing service is
// implemented by the owning team.
package router

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kubercloud/ani/pkg/ports"
)

type RegisterOptions struct {
	K8sClusterService ports.K8sClusterService
	EncryptionService ports.EncryptionService
	SecretService     ports.SecretService
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
	registerHarbor(v1)
	registerDemoInstances(v1)
	registerNetworkResources(v1)
	registerStorageResources(v1)
	registerVectorStoreResources(v1)
	registerK8sClusterResourcesWithService(v1, options.K8sClusterService)
	registerEncryptionResourcesWithService(v1, options.EncryptionService)
	registerSecretResourcesWithService(v1, options.SecretService)

	svc := h.Group("/api/v1/svc")
	registerModels(svc)
	registerInferenceServices(svc)
	registerKnowledgeBases(svc)

	// OpenAI-compatible inference proxy (separate URL prefix, no /api prefix)
	h.Group("/v1").POST("/chat/completions", inferenceProxy)
	h.Group("/v1").GET("/inference/stream", inferenceProxy)
}
