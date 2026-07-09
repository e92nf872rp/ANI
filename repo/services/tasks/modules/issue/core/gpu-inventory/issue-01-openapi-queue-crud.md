# Issue #1: OpenAPI 新增队列 CRUD + InstanceGPU 扩展

> **Priority:** high
> **Depends On:** —
> **Product line:** Core
> **Document Links:**
>
> - PRD: `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md` §8.2
> - SPEC: `repo/services/tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md` §3.1, §4.1, §4.2
> - Module: `repo/services/docs/console-modules/compute/gpu-management.md`

## Scope

`repo/api/openapi/v1.yaml`

## Description

在 Core OpenAPI 契约中新增 GPU 调度队列 CRUD 5 端点 + 4 schema + 2 RBAC scope，并扩展 `InstanceRecord.gpu` 调度字段。这是 PRD §8.2 强制的第一步：先改契约，再实现 handler/adapter。

## Acceptance Criteria

- [x] 新增 5 端点：`listGPUSchedulingQueues` / `createGPUSchedulingQueue` / `getGPUSchedulingQueue` / `updateGPUSchedulingQueue` / `deleteGPUSchedulingQueue`
- [x] 新增 4 schema：`GPUSchedulingQueue` / `GPUSchedulingQueueListResponse` / `GPUSchedulingQueueCreateRequest` / `GPUSchedulingQueueUpdateRequest`
- [x] 新增 2 RBAC scope：`scope:gpu-scheduling:read` / `scope:gpu-scheduling:write`
- [x] `POST` 要求 `Idempotency-Key` header
- [x] `InstanceRecord.gpu` 扩展 `queue_name` / `resource_name` / `scheduling_reason`
- [x] 新增错误码：`QueueNameConflict` / `QueueNotFound` / `PlatformDefaultProtected` / `InsufficientGPU` / `GPUNodeIncompatible`
- [x] `make validate-architecture` 通过
- [x] 前端 `make gen-core-schema` 可生成新类型

## Validation

```bash
cd repo
make validate-architecture
```

