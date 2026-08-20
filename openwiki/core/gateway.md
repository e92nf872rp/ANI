---
type: concept
title: ANI Gateway
description: "services/ani-gateway/: unified HTTP entry point for Core and Services APIs using Hertz framework, middleware chain, route registration, provider wiring"
tags: [core, gateway, hertz, http, routing]
---

# ANI Gateway

## Overview

The ANI Gateway (`services/ani-gateway/`) is the **single HTTP entry point** for both Core API (`/api/v1/*`) and Services API (`/api/v1/svc/*`). It uses the [Hertz](https://github.com/cloudwego/hertz) Go HTTP framework from CloudWeGo/ByteDance.

## Main Entry Point (`main.go`)

`main.go` follows a consistent construction pattern:

1. **Create logger** — `slog.NewJSONHandler(os.Stdout, nil)`
2. **Parse listen address** — `gatewayListenAddr()` from `ANI_GATEWAY_LISTEN_ADDR` env (default `:8080`) + optional `ANI_GATEWAY_TLS_CERT_FILE`/`ANI_GATEWAY_TLS_KEY_FILE` for TLS
3. **Construct provider runtimes** via factory functions:
   - `newGatewayK8sClusterRuntime(ctx, config)` — K8s cluster proxy runtime
   - `newGatewayEncryptionService(config)` — KMS/SM4 encryption provider
   - `newGatewaySecretService(config)` — Secret provider (K8s or local)
   - `newGatewayGPUInventory(config)` — GPU inventory provider
   - Additional runtime adapters in `RegisterOptions`
4. **Assemble `RegisterOptions`** and call `router.RegisterWithOptions(h, options)`
5. **Graceful shutdown** — `SPIN` handler catches SIGINT/SIGTERM

## Gateway Runtime Construction

Each runtime adapter follows the same pattern: read config from environment (or zero-value for dev mode), construct the adapter, return it. Examples:

- `newGatewayK8sClusterRuntime()` — reads `K8S_CLUSTER_PROXY_RUNTIME` config, constructs `KubernetesRESTClient`, wraps with `KubernetesProxyTargetStore` etc.
- `newGatewayEncryptionService()` — reads `KMS_ENCRYPTION_PROVIDER` from env, constructs `KMSEncryptionProvider` or returns passthrough for dev
- `newGatewaySecretService()` — reads `SECRET_PROVIDER`, constructs `KubernetesSecretProvider` or `LocalSecretService` for dev
- `newGatewayGPUInventory()` — reads `GPU_INVENTORY_PROVIDER`, constructs `KubernetesGPUInventory` or `LocalGPUInventory`

### Error Handling on Unsupported/Misconfigured Provider

Unsupported provider mode values trigger `os.Exit(1)`:

```go
// encryption_runtime.go
switch mode {
case "kms":
    return newKMSEncryptionService(config)
case "local":
    return nil   // nil is tolerated — downstream handlers check before use
default:
    log.Error("unsupported encryption provider", "mode", mode)
    os.Exit(1)
}
```

**Nil provider tolerance**: Several adapters return `nil` for local/dev mode (e.g., encryption service). Downstream route handlers and middleware guard against nil before calling methods. This allows the Gateway to boot without every backend configured.

### Multi-Endpoint Failover

The `KubernetesRESTClient` (`pkg/adapters/runtime/kubernetes_rest_client.go`) implements multi-endpoint failover for K8s API access, reading multiple endpoints from config and falling back on connection errors.

## Route Registration

**File**: `services/ani-gateway/internal/router/router.go`

`RegisterWithOptions` wires routes in a specific dependency-safe order:

```text
Core routes (/api/v1):
  ├─ registerHealth, registerBranding, registerAuth
  ├─ registerTasksWithStore
  ├─ registerInstancesWithRuntime (instanceLookup returned)
  ├─ registerObservability (uses instanceLookup)
  ├─ registerGPUInventoryResources
  ├─ registerGPUSchedulingResources
  ├─ registerNetworkResources
  ├─ registerStorageResources
  ├─ registerVectorStoreResources (nil-guarded: if VectorStoreService nil, still registers)
  ├─ registerK8sClusterResources
  ├─ registerEncryptionResources, registerSecretResources
  ├─ registerEmailNotificationResources
  ├─ registerQuotaResources
  └─ registerAdminTenantResources

Services routes (/api/v1/svc):
  ├─ registerModels       → STUBS: all return empty JSON
  ├─ registerInferenceServices → STUBS: all return empty JSON
  ├─ registerKnowledgeBases   → gRPC proxy to kb-service (KBServiceClient nil → 503)
  ├─ registerGpuContainers    → STUBS
  ├─ registerSandboxes        → STUBS: all return empty JSON
  ├─ registerTenant           → STUBS: members, roles, SSO, webhooks return empty JSON
  └─ registerTenantPlans      → gRPC proxy to tenant-service
```

### Not-Implemented Handlers

File `stubs.go` provides `notImplemented()` returning `501 NOT_IMPLEMENTED`. The OpenAI-compatible inference proxy (`/v1/chat/completions`) returns 501.

## References

- [Middleware](middleware.md) — Auth/RBAC/audit/rate-limit/idempotency
- [Instances](instances.md) — Instance lifecycle via gateway routes
- Source: `repo/services/ani-gateway/`