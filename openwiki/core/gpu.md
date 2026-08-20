---
type: concept
title: GPU Inventory & Scheduling
description: "GPU inventory discovery (NVIDIA device-plugin, DCGM), GPU specification catalog, GPU scheduling queues via Volcano/HAMi, GPU smoke live gates"
tags: [core, gpu, inventory, scheduling, dcgm, volcano, hami]
---

# GPU Inventory & Scheduling

## GPU Inventory

`ports.GPUInventory` discovers GPU nodes and their device classes from the K8s cluster, and produces scheduling decisions:

| Method | Description |
|--------|-------------|
| `ListNodeClasses(ctx, filter)` | List GPU-capable nodes matching `GPUDiscoveryFilter` (by vendor, pool, labels) |
| `GetNodeClass(ctx, nodeName)` | Get single `GPUNodeClass` by node name |
| `PlanScheduling(ctx, request)` | Resolve `GPUSchedulingRequest` into `GPUSchedulingDecision` |
| `ListSpecAvailability(ctx, specFilter)` | List GPU spec availability across cluster |

**GPUSchedulingRequest fields:** `TenantID`, `PreferredVendors`, `RequiredMemoryMiB`, `RequiredCount`, `QueueName`, `WorkloadClass`

**GPUSchedulingDecision fields:** `Accepted`, `NodeSelector` (K8s node selector labels), `Tolerations` (K8s tolerations), `ResourceName` (e.g. `nvidia.com/gpu`, `nvidia.com/vgpu`), `ResourceQuantity`, `RuntimeClassName`, `SchedulerName` (volcano | hami-scheduler), `QueueName`, `SelectedNodeModel`

**Four availability statuses** per GPU device class: `available`, `full` (device fully allocated), `device_full` (node has no capacity), `unavailable`

**Node class** (`GPUNodeClass`): NodeName, Vendor (NVIDIA/Huawei/Hygon/Unknown), Model, KernelVersion, OSImage, Pool, Labels/Annotations/Taints, Devices[], Allocatable (raw K8s allocatable map), Ready, Reason, GPUMode, GPUSpec, GPUSharingSpec, GPUSharingPolicy.

**Device class** (`GPUDeviceClass`): Vendor, Model, MemoryMiB, ResourceName (e.g. `nvidia.com/gpu`), VirtualizationMode (none/MIG/vGPU), DriverVersion, RuntimeVersion, Capabilities[].

Adapters:
- **`KubernetesGPUInventory`** (production) — discovers GPU nodes via K8s API, reads node labels for GPU mode/spec, reads device-plugin resources
- **`LocalGPUInventory`** (dev) — returns canned GPU node data for local development

## GPU Specification Service

`ports.GPUSpecService` provides a catalog of known GPU types:

- `ListSpecs(ctx) -> []GPUSpec` — GPU type catalog: ID, Name, GPUType, MemoryTotalMB, Shares, MBPerShare, Available
- `GetSpec(ctx, id) -> GPUSpec` — single spec lookup
- Labels: `ani.kubercloud.io/gpu-mode` (wholecard | vgpu), `ani.kubercloud.io/gpu-spec` (wholecard mode), `ani.kubercloud.io/gpu-sharing-spec`/`gpu-sharing-policy` (vgpu mode)

Default adapter: `LocalGPUSpecService` wraps `GPUInventory` to derive specs from discovered nodes.

## GPU Scheduling Queue

`ports.GPUSchedulingQueueStore` provides queue CRUD for GPU workload scheduling with dual implementations:

### LocalGPUSchedulingQueueStore (dev/in-memory)
- `NewLocalGPUSchedulingQueueStore()` creates an in-memory store
- `SeedDefaults()` preseeded with `ani-inference` and `ani-training` queues on startup
- Used in `dev` and `local` profiles
- Source: `pkg/adapters/runtime/local_gpu_scheduling_queue_store.go`

### VolcanoQueueStore (production, Volcano CRD-backed)
- CRUD over Volcano `Queue` CRD via K8s REST API
- `Create` and `Update` use `idempotencyKey` for deduplication — returns `IdempotentReplay=true` on replay
- Stamps labels during create/update:
  - `ani.kubercloud.io/tenant` — tenant ownership
  - `ani.kubercloud.io/queue-id` — platform queue identifier
  - `ani.kubercloud.io/platform-default` — marks platform default queues
- **Platform-default protection**: PATCH/DELETE on platform default queues is rejected via `ErrPlatformDefaultProtected`
- **Tenant isolation**: `List(ctx, tenantID)` scopes results to the caller's tenant; all mutations enforce tenant ownership via labels
- `EnsurePlatformDefaults` config option: if enabled, creates ani-inference and ani-training queues if they don't exist
- Integration with HAMi for vGPU sharing
- Queue admission enforces per-tenant GPU quota before accepting jobs

### Sentinel Errors (queue store)

| Error | HTTP Status | When Returned |
|-------|-------------|---------------|
| `ErrQueueNotFound` | 404 | Queue ID not found |
| `ErrQueueNameConflict` | 409 | Queue name already exists |
| `ErrPlatformDefaultProtected` | 422 | Attempt to update/delete a platform-default queue |
| `ErrQueueStoreUnavailable` | 503 | Volcano CRD API unavailable |

Source: `pkg/ports/gpu_scheduling.go` (lines 77-81), `pkg/adapters/runtime/volcano_queue_store.go`

## DCGM Integration

- NVIDIA DCGM (Data Center GPU Manager) for GPU health and metrics
- DCGM-Exporter for Prometheus metrics exposure (GPU utilization, memory, temperature, power)
- DCGM integration in metering collectors for GPU usage records
- GPU inventory live gate validates DCGM metrics are flowing

## Live Gates

| Gate | What It Validates | Evidence |
|------|------------------|----------|
| GPU Inventory Gate | DCGM-Exporter running, GPU node labels correct, device-plugin functional | `development-records/sprint13-gpu-inventory-dcgm-live-result.md` |
| GPU Scheduling Smoke A | Volcano queue creation + whole-card GPU job scheduling | `development-records/gpu-scheduling-issue-05-gpu-smoke-live-gate.md` |
| GPU Scheduling Smoke B | HAMi vGPU scheduling with shared GPU | Same gate |
| Queue CRUD Gate | Volcano queue CRUD over Gateway API | `development-records/gpu-scheduling-issue-06-queue-crud-live-gate.md` |

## References

- [Adapters](adapters.md) — GPU adapter implementations
- [Observability & Metering](observability-metering.md) — DCGM metrics collection
- [Instances](instances.md) — GPU container instance lifecycle
- Source: `repo/pkg/ports/gpu_inventory.go`, `repo/pkg/ports/gpu_scheduling.go`, `repo/pkg/ports/gpu_spec.go`, `repo/pkg/adapters/runtime/kubernetes_gpu_inventory*.go`, `repo/pkg/adapters/runtime/local_gpu_inventory*.go`, `repo/pkg/adapters/runtime/volcano_queue_store*.go`