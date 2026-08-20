---
type: concept
title: Ports and Adapters Pattern
description: "Capability abstraction layer via pkg/ports (interfaces) and pkg/adapters (implementations): decoupling ANI platform from specific open-source components"
tags: [architecture, ports, adapters, coupling, abstraction]
---

# Ports and Adapters Pattern

## Design Principle

ANI uses a strict **ports/adapters** pattern to decouple product capabilities from specific open-source components. A `port` (in `pkg/ports/`) is a Go interface that expresses an ANI product capability — what the platform needs, not how it's implemented. An `adapter` (in `pkg/adapters/`) is a concrete implementation of that port backed by a specific component.

## Coupling Level System

Architecture governance enforces five coupling levels (defined in `repo/architecture/component-import-allowlist.yaml`):

| Level | Rule | Example |
|-------|------|---------|
| `port_required` | Must depend on the port interface, never on the adapter or underlying SDK | `ports.WorkloadRuntime`, `ports.MetadataStore` |
| `adapter_with_extensions` | May extend an adapter for provider-specific behavior; must not bypass port | `runtimeadapter.KubernetesProviderAdapter` |
| `bounded_direct` | Allowed direct SDK use in adapter/controller/preflight boundaries | `pgx` in repos, `nats.go` in bus adapter, client-go in K8s provider |
| `temporary_exception` | Must be removed or migrated within the current sprint; tracked with expiry | Legacy direct imports in model-service (pkg/bootstrap, pkg/types); ai/rag-engine/ asyncpg imports (migrate_by: Phase 2 KB gRPC API) |
| `forbidden` | Any use outside allowed boundaries | Services code importing `pkg/ports` directly |

## Port Interface Catalog

`pkg/ports/` contains ~30 capability interfaces. Key groups:

**Workload/Instance:** `WorkloadRuntime`, `WorkloadRenderer`, `WorkloadAdmission`, `WorkloadProviderDryRun`, `WorkloadProviderApply`, `WorkloadProviderStatusReader`, `WorkloadStatusReconciler`, `WorkloadReconcileController`, `WorkloadInstanceStore`, `WorkloadInstanceOrchestrator`, `WorkloadInstanceService`, `WorkloadInstanceOps`, `WorkloadIdentityService`, `SandboxRuntime`

**Network:** `NetworkService`, `NetworkResourceStore`, `NetworkProviderRenderer`, `NetworkProviderDryRun`, `NetworkProviderApply`, `NetworkStatusReconciler`

**Storage:** `StorageService`, `StorageResourceStore`, `StorageProviderRenderer`, `StorageProviderDryRun`, `StorageProviderApply`, `StorageStatusReconciler`

**Data/Infrastructure:** `MetadataStore`, `MessageBus`, `CacheStore`, `ObjectStore`, `VectorStore`, `VectorStoreService`, `ImageRegistry`, `LogStore`

**GPU:** `GPUInventory`, `GPUSpecService`, `GPUSchedulingQueueStore`

**Observability/Metering:** `ObservabilityService`, `InstanceObservability`, `MeteringCollectionService`, `MeteringService`

**Auth/Quota/Tenant:** `SecretService`, `IdentityProvider`, `QuotaAdminService`, `TenantService`, `EmailNotificationStore`

## Default Adapters

| Port Interface | Default Adapter | Component |
|----------------|----------------|-----------|
| `MetadataStore` | `postgresadapter.NewMetadataStore(db)` | PostgreSQL / CloudNativePG |
| `MessageBus` | `natsadapter.NewMessageBus(js)` | NATS JetStream |
| `CacheStore` | `redisadapter.NewCacheStore(client)` | Redis |
| `ObjectStore` | `objectstoreadapter.NewObjectStore(client)` | MinIO / S3-compatible |
| `VectorStore` | `vectorstoreadapter.NewVectorStore(client)` | Milvus |
| `ImageRegistry` | `registryadapter.NewImageRegistry(client)` | Harbor |
| `ObservabilityService` | `runtimeadapter.NewPrometheusObservabilityService(client)` | Prometheus |
| `LogStore` | `runtimeadapter.NewLokiLogStore(client)` | Loki |
| `WorkloadRuntime` | `runtimeadapter.NewKubernetesLifecycleExecutor(client)` | Kubernetes |
| `GPUInventory` | `runtimeadapter.NewKubernetesGPUInventory(client)` | K8s device-plugin + DCGM |

## References

- [Component Selection Criteria](tech-stack.md) — How default components were evaluated
- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Governance enforcement
- ANI-13: Open-Source Adapter Architecture (`/ANI-13-开源组件松耦合适配器架构.md`)
- pkg/ports/ source: `/repo/pkg/ports/`
- pkg/adapters/ source: `/repo/pkg/adapters/`
- Import allowlist: `/repo/architecture/component-import-allowlist.yaml`