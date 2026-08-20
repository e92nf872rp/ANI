---
type: concept
title: Gateway Router
description: "Gateway route registration: Core routes (/api/v1/*), Services routes (/api/v1/svc/*), RegisterWithOptions wiring, nil-provider guards, stub handler groups (models, inference, sandboxes, tenant) returning 501/empty JSON, OpenAI inference proxy returning 501."
tags: [core, gateway, router, route-registration, stubs]
---

# Gateway Router

## Overview

**File**: `services/ani-gateway/internal/router/router.go`

The Gateway router wires all API routes onto the Hertz HTTP server. It is the single place where Core API paths (`/api/v1/*`) and Services layer paths (`/api/v1/svc/*`) are registered.

## Route Registration

`RegisterWithOptions(h *server.Hertz, options RegisterOptions)` wires routes in a dependency-safe order:

```
Core routes (/api/v1):
  ├─ registerHealth, registerBranding, registerAuth   (public, no auth)
  ├─ registerTasksWithStore
  ├─ registerInstancesWithRuntime  (returns instanceLookup)
  ├─ registerObservability (uses instanceLookup)
  ├─ registerGPUInventoryResources
  ├─ registerGPUSchedulingResources
  ├─ registerNetworkResources
  ├─ registerStorageResources
  ├─ registerVectorStoreResources (nil-guarded: if nil, registers with nil handler)
  ├─ registerK8sClusterResources
  ├─ registerEncryptionResources, registerSecretResources
  ├─ registerEmailNotificationResources
  ├─ registerQuotaResources
  └─ registerAdminTenantResources

Services routes (/api/v1/svc):
  ├─ registerModels              → STUBS: all return empty JSON
  ├─ registerInferenceServices   → STUBS: all return empty JSON
  ├─ registerKnowledgeBases      → gRPC proxy to kb-service (KBServiceClient nil → 503)
  ├─ registerGpuContainers       → STUBS
  ├─ registerSandboxes           → STUBS: all return empty JSON
  ├─ registerTenant              → STUBS: members, roles, SSO, webhooks return empty JSON
  └─ registerTenantPlans         → gRPC proxy to tenant-service
```

## RegisterOptions

| Field | Type | Nil Behavior |
|-------|------|--------------|
| `K8sClusterService` | `ports.K8sClusterService` | Routes registered with nil handler → 501 |
| `EncryptionService` | `ports.EncryptionService` | nil is tolerated (dev mode) |
| `SecretService` | `ports.SecretService` | nil → defaults to `LocalSecretService` |
| `GPUInventory` | `ports.GPUInventory` | Routes registered with nil handler |
| `GPUSchedulingQueueStore` | `ports.GPUSchedulingQueueStore` | Routes registered with nil handler |
| `VectorStoreService` | `ports.VectorStoreService` | **nil-guard**: registers with nil handler instead of panicking |
| `KBServiceClient` | `KBGRPCClient` | nil → KB gRPC handlers return 503 UNAVAILABLE |
| `KBSSEConfig` | `KbSSEConfig` | nil `RagClient`/`VLLMStreamer` → degrades to empty SSE stream |

## Not-Implemented Handlers

**File**: `services/ani-gateway/internal/router/stubs.go`

```go
func notImplemented(ctx context.Context, c *app.RequestContext) {
    c.JSON(http.StatusNotImplemented, map[string]any{
        "code":       "NOT_IMPLEMENTED",
        "message":    "this endpoint is not yet implemented",
        "request_id": middleware.GetRequestID(c),
    })
}
```

**OpenAI-compatible inference proxy** (`/v1/chat/completions`) also returns 501 NOT_IMPLEMENTED — the streaming reverse-proxy to vLLM is future work.

## Stub Handler Groups

| Group | File | Status |
|-------|------|--------|
| Models | `model_resources.go` | All handlers return empty `items: []` |
| Inference Services | `inference_resources.go` | All handlers return empty `items: []` |
| Sandboxes | `sandbox_resources.go` | All handlers return empty `items: []` |
| Tenant (members, roles, SSO, webhooks) | `tenant_resources.go` | All handlers return empty `items: []` |

## References

- [Gateway](gateway.md) — Entry point and runtime construction
- [Middleware](middleware.md) — Middleware chain composition
- [KB Service SSE](../services-layer/kb-service.md) — SSE streaming handler
- Source: `services/ani-gateway/internal/router/router.go`, `services/ani-gateway/internal/router/stubs.go`