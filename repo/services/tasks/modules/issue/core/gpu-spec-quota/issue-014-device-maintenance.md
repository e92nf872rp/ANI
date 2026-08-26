# 设备维护功能（Deferred）— PATCH /gpu-inventory/{device_id} Handler

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

实现 `PATCH /gpu-inventory/{device_id}` 端点的 Gateway handler，支持设备标维护（maintenance）和恢复空闲（idle）操作。契约已在 issue-001 中定义并合入 main（PR #96），本 issue 仅实现 handler 层。

从 issue-008-gateway-handler.md 拆出，本期（M2.1-TASK-A）不实现，后续 Sprint 再做。

## Scope

- Product line: core
- Code paths allowed: `repo/services/ani-gateway/internal/router/`

## Acceptance Criteria

- [ ] 扩展 `internal/router/gpu_inventory_resources.go`：设备管理 PATCH handler（标维护 maintenance / 恢空闲 idle）
- [ ] PATCH handler 改 K8s 节点标签/cordoned 状态
- [ ] 错误响应：DeviceNotFound(404)、idempotency_key 校验
- [ ] 扩展 `internal/router/router.go`：注册 PATCH /gpu-inventory/{device_id} 路由
- [ ] `go build ./services/ani-gateway/...` 通过
- [ ] `go test ./services/ani-gateway/...` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#8 (issue-008-gateway-handler.md，需先完成 inventory 扩展字段部分)

## Type

core

## Priority

low

## Labels

core, deferred

## Batch

M2.1-TASK-A-deferred

## References

- SPEC: §4.3 (PATCH /gpu-inventory/{device_id}), §4.5 (Error Responses - DeviceNotFound), §7.3 (权限 - gpu-inventory:write)
- UX: N/A
- 契约来源: PR #96 (issue-001-core-api-contract)