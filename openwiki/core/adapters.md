---
type: concept
title: Core Adapters Catalog
description: "Default provider implementations in pkg/adapters/: K8s runtime adapters, local dev adapters, third-party component adapters, and resilience wrappers"
tags: [core, adapters, providers, kubernetes, resilience]
---

# Core Adapters Catalog

## Overview

`pkg/adapters/` provides the default (and often only) implementations of every port defined in `pkg/ports/`. Each adapter maps a port interface to a concrete open-source component while respecting the coupling boundaries defined in the [component import allowlist](../ci-cd/architecture-baselines.md).

## Kubernetes Runtime Adapters (`pkg/adapters/runtime/`)

These are the primary adapters that implement compute, network, storage, GPU, and identity capabilities on top of Kubernetes:

| Adapter | Port(s) Implemented | Description |
|---------|---------------------|-------------|
| `KubernetesLifecycleExecutor` | `WorkloadRuntime` | Create/start/stop/restart/resize/rebuild/delete workloads via K8s API |
| `KubernetesProviderAdapter` | `WorkloadProviderDryRun`, `WorkloadProviderApply`, `WorkloadProviderStatusReader` | Full dry-run → apply → observe pipeline against K8s |
| `KubernetesProviderExecutionProfile` | — | Execution profile generation from provider config |
| `KubernetesRESTClient` | — | Low-level K8s REST client with TLS, multi-endpoint failover (underlying the other adapters) |
| `KubernetesNodePoolProvider` | `WorkloadInstanceOps` | Node pool CRUD (create/scale/delete) |
| `KubernetesGPUInventory` | `GPUInventory` | GPU node/device discovery via K8s node labels + device-plugin |
| `KubernetesSandboxRuntime` | `SandboxRuntime` | Sandbox instance lifecycle (Kata Containers with checkpoint) |
| `KubernetesSandboxPorts` | — | Sandbox port mapping management |
| `KubernetesSandboxCheckpoints` | — | Sandbox checkpoint (CSI VolumeSnapshot, process checkpoint) |
| `KubernetesSecretProvider` | `SecretService` | K8s Secret CRUD and binding to instances |
| `KubeOVNNetworkProvider` | `NetworkProviderApply` | Apply Kube-OVN network manifests |
| `KubeOVNNetworkRenderer` | `NetworkProviderRenderer` | Generate Kube-OVN VPC/subnet/SG manifests |
| `KMSEncryptionProvider` | — | SM4/AES encryption via KMS integration |
| `StorageProvider` | `StorageProviderApply` | Apply storage manifests (Rook-Ceph) |
| `StorageRenderer` | `StorageProviderRenderer` | Generate storage manifests |
| `VolcanoQueueStore` | `GPUSchedulingQueueStore` | Volcano queue CRUD for GPU scheduling |

## Local Dev Adapters (`pkg/adapters/runtime/local_*.go`)

In-memory / local-only implementations used when no real provider is configured:

| Adapter | Port | When Used |
|---------|------|-----------|
| `LocalSecretService` | `SecretService` | `ANI_AUTH_MODE=dev` or no real K8s cluster |
| `LocalGPUInventory` | `GPUInventory` | No real GPU discovery configured |
| `LocalGPUSpecService` | `GPUSpecService` | Always wraps inventory (local or real) |
| `LocalObservabilityService` | `ObservabilityService` | No Prometheus configured |
| `LocalInstanceObservabilityService` | `InstanceObservability` | No real observability provider |
| `LocalSandboxRuntime` | `SandboxRuntime` | No real sandbox runtime configured |
| `LocalK8sClusterService` | `K8sClusterService` | No real K8s cluster proxy configured |
| `LocalAdmissionGuard` | `WorkloadAdmission` | Default admission guard |
| `LocalStatusReconciler` | `WorkloadStatusReconciler` | Default status reconciler |
| `LocalMeteringService` | `MeteringService` | No real metering backend |
| `LocalNotificationEmailStore` | `EmailNotificationStore` | No real email provider |

## Third-Party Adapters

| Adapter | Port | Component | File(s) |
|---------|------|-----------|---------|
| `PostgresMetadataStore` | `MetadataStore` | PostgreSQL (via pgx) | `pkg/adapters/postgres/metadata_store.go` |
| `NATSMessageBus` | `MessageBus` | NATS JetStream | `pkg/adapters/nats/message_bus.go` |
| `RedisCacheStore` | `CacheStore` | Redis | `pkg/adapters/redis/cache_store.go` |
| `MinioObjectStore` | `ObjectStore` | MinIO / S3-compatible | `pkg/adapters/objectstore/minio_store.go` |
| `MilvusVectorStore` | `VectorStore`, `VectorStoreService` | Milvus | `pkg/adapters/vectorstore/milvus_store.go` |
| `HarborImageRegistry` | `ImageRegistry` | Harbor | `pkg/adapters/registry/harbor_image_registry.go` |
| `PrometheusObservabilityService` | `ObservabilityService` | Prometheus | `pkg/adapters/runtime/prometheus_observability_service.go` |
| `LokiLogStore` | `LogStore` | Loki | `pkg/adapters/runtime/loki_log_store.go` |
| `PrometheusInstanceObservability` | `InstanceObservability` | Prometheus + Loki | `pkg/adapters/runtime/prometheus_instance_observability.go` |

## Resilience Wrappers (`pkg/adapters/resilience/`)

Cross-cutting resilience subsystem that wraps any port call with retry/circuit-breaker/timeout/degradation behavior.

**Policy struct:**
- `Timeout`: per-call timeout
- `BaseAttempts`: retry count (including first attempt)
- `BaseBackoff`, `MaxBackoff`: exponential backoff bounds
- `BreakerName`, `FailureRatio`, `MinRequests`, `CooldownPeriod`, `HalfOpenMaxRequests`: circuit breaker configuration

**Circuit breaker state machine:** `closed → open → half-open → closed`

**Retryable error classification:**
- `StatusError`: HTTP 429 RateLimitExceeded, 500+ server errors
- `net.OpError`: connection refused, DNS lookup failure
- `context.DeadlineExceeded` / `context.Canceled`

**Degradation mode:** When enabled after multiple failures, non-critical dependencies are skipped. The `/readyz` endpoint returns a degraded status instead of failing entirely.

**Usage:** `policy := NewResiliencePolicy(cfg)`; `result, err := WrapCall(ctx, policy, fn)`

**Sprint 14 coverage:** R-P0-3 (adapter timeout wrapper), R-P1-5 (retry + circuit breaker foundation), R-P1-6 (degradation mode), R-P2-7 (multi-endpoint failover config).

## References

- [Ports Catalog](ports-catalog.md) — Ports these adapters implement
- [Bootstrap](bootstrap.md) — How adapters are assembled in MustConnect
- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Coupling levels and accepted_baseline exceptions
- [Resilience](resilience.md) — Detailed resilience wrapper documentation
- Source: `/repo/pkg/adapters/`