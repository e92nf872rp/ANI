---
type: concept
title: Core Ports Catalog
description: "Complete inventory of pkg/ports capability interfaces: workload, network, storage, GPU, observability, metering, async tasks, quota, tenant, sandbox"
tags: [core, ports, interfaces, catalog]
---

# Core Ports Catalog

## Overview

`pkg/ports/` defines 30+ Go interfaces representing ANI's product capabilities. Every interface describes what the platform needs, not how it's implemented. Default implementations live in `pkg/adapters/`.

## Workload / Instance Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `WorkloadRuntime` | `WorkloadKind`, `WorkloadState`, `LifecycleAction` | Abstract compute provider |
| `WorkloadRenderer` | `WorkloadManifest` | Generate provider-specific manifests |
| `WorkloadAdmission` | `AdmissionResult` | Admission guardrails before planning |
| `WorkloadProviderDryRun` | `DryRunResult` | Server-side dry-run before apply |
| `WorkloadProviderApply` | `ApplyResult` | Apply with intent tracking |
| `WorkloadProviderStatusReader` | `ProviderObservation` | Read current status from provider |
| `WorkloadStatusReconciler` | — | Reconcile stored status with observed status |
| `WorkloadReconcileController` | — | Background controller loop with backoff |
| `WorkloadInstanceStore` | `WorkloadInstanceRecord` | Persist instance state |
| `WorkloadInstanceOrchestrator` | — | Full orchestration pipeline |
| `WorkloadInstanceService` | — | Instance CRUD service |
| `WorkloadInstanceOps` | `LifecycleRequest` | Lifecycle operations |
| `WorkloadIdentityService` | `IdentityClaims` | Workload identity management |
| `SandboxRuntime` | — | Sandbox instance runtime |

## Network Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `NetworkService` | `VPC`, `Subnet`, `SecurityGroup` | VPC/subnet/secgroup CRUD |
| `NetworkResourceStore` | — | Network resource persistence |
| `NetworkProviderRenderer` | — | Kube-OVN manifest generation |
| `NetworkProviderDryRun` | — | Server-side dry-run |
| `NetworkProviderApply` | — | Apply network manifests |
| `NetworkStatusReconciler` | — | Network status reconciliation |

## Storage Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `StorageService` | `Volume`, `Filesystem` | Volume/filesystem CRUD, overview |
| `StorageResourceStore` | `StorageVolumeRecord` | Storage resource persistence |
| `StorageProviderRenderer` | — | Storage manifest generation |
| `StorageProviderDryRun` | — | Server-side dry-run |
| `StorageProviderApply` | — | Apply storage manifests |
| `StorageStatusReconciler` | — | Storage status reconciliation |

## Data Infrastructure Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `MetadataStore` | `MetadataTx` | Transactional metadata access (PG) |
| `MessageBus` | `Message`, `PublishOptions` | NATS JetStream pub/sub |
| `CacheStore` | — | Redis-backed cache (blocklist, rate-limit) |
| `ObjectStore` | `ObjectRef`, `PutObjectInput`, `SignedURL` | S3-compatible object storage |
| `VectorStore` | — | Milvus vector collection management |
| `VectorStoreService` | — | Vector store resource API |
| `ImageRegistry` | `ImageRef`, `ImageScanStatus` | Harbor container registry |

## GPU Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `GPUInventory` | `GPUNodeClass`, `GPUDeviceClass` | GPU node/device discovery |
| `GPUSpecService` | `GPUSpec` | GPU type catalog, shares, MB/share |
| `GPUSchedulingQueueStore` | — | GPU scheduling queue CRUD |

## Observability & Metering Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `ObservabilityService` | `QueryRequest`, `QueryResult` | Prometheus query proxy |
| `InstanceObservability` | `LogEntry`, `EventRecord`, `MetricsRecord` | Per-instance logs/events/metrics/terminal |
| `LogStore` | — | Loki log storage |
| `MeteringCollectionService` | `CollectionSpec` | Periodic usage collection ticker |
| `MeteringService` | — | Metering CRUD service |

## Async Task Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `AsyncTaskStore` | `AsyncTaskRecord`, `AsyncTaskUpdate` | Create, get, update async tasks |

## Auth & Security Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `SecretService` | `SecretRecord`, `SecretBindingRecord` | Secret CRUD and binding |
| `IdentityProvider` | `IdentityClaims` | OIDC/SSO identity validation |

## Quota & Tenant Ports

| Port | Key Types | Purpose |
|------|-----------|---------|
| `QuotaAdminService` | `ResourceType`, `QuotaReservation`, `QuotaView` | Quota meta, get/put/upsert, reservation |
| `TenantService` | `Tenant`, `TenantSummary` | Tenant CRUD, plan binding |
| `EmailNotificationStore` | — | Email notification config and subscriptions |

## Error Sentinel Catalog

All port-level sentinel errors are defined in `pkg/ports/errors.go`:
`ErrNotConfigured`, `ErrUnsupported`, `ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrFailedPrecondition`, `ErrPayloadTooLarge`, `ErrInvalidCredentials`, `ErrTenantNotFound`, `ErrTenantPlanNotFound`, `ErrUnavailable`, `ErrQuotaExceeded`, `ErrQuotaResourceNotRegistered`, `ErrQuotaIdempotencyConflict`, `ErrQuotaNotFound`, `ErrQuotaAlreadyExists`, `ErrQuotaUpdateUncertain`, `ErrReservationNotFound`, `ErrMetadataTenantTxBegin/Commit`, `ErrMetadataPlatformTxBegin/Commit`.

## References

- [Ports and Adapters Pattern](../architecture/ports-and-adapters.md)
- [Adapters](adapters.md) — Default implementations
- `pkg/ports/` source: `/repo/pkg/ports/`
- Error sentinels: `pkg/ports/errors.go`
- Import allowlist: `/repo/architecture/component-import-allowlist.yaml`