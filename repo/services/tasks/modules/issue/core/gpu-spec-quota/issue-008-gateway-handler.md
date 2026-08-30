# Gateway Handler — 规格目录 + 预留 + 实例 spec_id 接入

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，实现规格目录 4 端点 handler、预留管理端点、/quotas/me + /reservations/me 查询端点、创建实例 handler 的 spec_id 接入，完成 Core REST API 的业务逻辑层。

## Scope

- Product line: core
- Code paths allowed: `repo/services/ani-gateway/internal/router/`

## Acceptance Criteria

- [ ] 新增 `internal/router/gpu_spec_resources.go`：规格目录 4 端点 handler（GET list / POST create / GET by-id / DELETE），含 idempotency_key 校验、gpu_type 对齐节点标签校验（422 GPUTypeNotInNodes）、spec_id 已存在返回 409、有实例引用禁止删除返回 409 GPUSpecInUse
- [ ] POST create handler 按 gpu_mode 自动派生 volcano_resources（SPEC §3.2，GPUSpecCreateRequest 不含此字段，由代码生成）：wholecard 模式填充 `wholecard: {nvidia.com/gpu: "{count}"}`；vgpu 模式填充 `vgpu: {volcano.sh/vgpu-memory: "{mb_per_share}", volcano.sh/vgpu-number: "{count}"}`（FR-26 vgpu-memory 不能为空），派生后随 GPUSpecCRD 传入 CRD Store 持久化
- [ ] 扩展 `internal/router/gpu_inventory_resources.go`：inventory 返回 gpu_mode/gpu_spec 等扩展字段（设备 PATCH handler 移至 issue-014 deferred）
- [ ] 新增 `internal/router/reservation_resources.go`：预留 PUT 端点（设 allocated_gpu_count，<= total 校验 422）+ `/quotas/me`（tenant scope 查自己配额）+ `/reservations/me`（tenant scope 查自己预留）
- [ ] 扩展 `internal/router/router.go`：注册新路由
- [ ] 扩展 `internal/router/instances.go`：创建实例 handler 接 spec_id（传 spec_id 走规格模式，不传走旧模式），network_config 默认值兜底
- [ ] 扩展 `gpu_inventory_runtime.go`：GPUSpecStore 注入
- [ ] `go build ./services/ani-gateway/...` 通过
- [ ] `go test ./services/ani-gateway/...` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#7

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §4.3 (Endpoint Reference 全表), §4.5 (Error Responses), §5.2 (删除双调)
- UX: N/A
- Deferred: issue-014-device-maintenance.md（设备维护功能后续实现）
