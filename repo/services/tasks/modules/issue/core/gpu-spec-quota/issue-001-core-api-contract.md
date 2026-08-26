# Core API 契约 — 规格目录 + 配额 + 预留 + 设备管理接口定义

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，先定义 Core OpenAPI 契约，包含规格目录 CRUD、配额管理、预留管理、设备管理、规格可用性等 12 项新增/扩展接口，作为后续 port/adapter/handler/前端的契约边界。

## Scope

- Product line: core
- Code paths allowed: `repo/api/openapi/v1.yaml` only

## Acceptance Criteria

- [ ] `repo/api/openapi/v1.yaml` 新增 GPUSpec schema（含 node_affinity + volcano_resources 字段）
- [ ] 扩展 GPUNodeClass：新增 `gpu_mode`/`gpu_spec`/`gpu_sharing_spec`/`gpu_sharing_policy` 可选字段（从节点标签读）
- [ ] 扩展 CreateGPUContainerInstanceConfig：新增 `spec_id` 可选字段（走规格模式）
- [ ] 新增 12 项接口定义（规格 CRUD 4 端点 + 可用性查询 + 设备 PATCH + 预留 PUT + 预留查询 + /quotas/me + /reservations/me 等）
- [ ] Request/Response schemas：GPUSpecCreateRequest、GPUSpecAvailability、ReservationPutRequest、ReservationView、GPUDeviceStatusUpdateRequest
- [ ] Error responses：GPUSpecNotFound(404)、GPUSpecInUse(409)、GPUSpecConflict(409)、GPUTypeNotInNodes(422)、QUOTA_EXCEEDED(409)、RESERVED_INSUFFICIENT(409)、RESERVATION_EXCEEDS_QUOTA(422)、DeviceNotFound(404)
- [ ] idempotency_key required on: POST /gpu-specs、DELETE /gpu-specs/{spec_id}、PUT /admin/tenants/{tenant_id}/quota、PUT /admin/tenants/{tenant_id}/reservations、PATCH /gpu-inventory/{device_id}、POST /instances
- [ ] `make validate-services` 通过（OpenAPI 校验）
- [ ] `git diff --check` 通过

## Dependencies

None

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §4.1, §4.2, §4.3, §4.4, §4.5
- UX: N/A
