# Core console session handler 补全

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

补全 Core Gateway 的 `createInstanceConsoleSession`（VM console）handler。OpenAPI 已声明 `POST /api/v1/instances/{instance_id}/console`，但 handler、port 方法、local adapter 尚未实现。本 Issue 对齐 PRD US-005 与 SPEC §3.1、§4.0、§5.1.3、§9.3。

Sprint 12 已完成 logs/events/metrics/security-events/exec 的 local adapter + handler；Sprint 13 已完成 Prometheus real adapter。本 Issue 仅补 console session 一个端点，不触碰其余已完成的 5 个观测端点。

## Scope
- Product line: core
- Code paths allowed: repo/pkg/ports/、repo/pkg/adapters/runtime/、repo/services/ani-gateway/internal/router/

## Acceptance Criteria
- [ ] `POST /api/v1/instances/{instance_id}/console` 返回 `200 + InstanceConsoleSession`（含 `session_id`、`protocol`、`connect_url`、`url`、`expires_at`）
- [ ] 仅 `kind=vm` 可创建 console session；非 vm kind 返回 `400`
- [ ] 实例非 `running` 状态返回 `422`
- [ ] `protocol` 白名单：`console / vnc / novnc / serial`（与 vm-management.md 一致）；非法值返回 `400`
- [ ] `InstanceObservability` port 新增 `CreateConsoleSession` 方法 + `InstanceConsoleSessionCreateRequest` / `InstanceConsoleSessionRecord` 类型
- [ ] `LocalInstanceObservabilityService.CreateConsoleSession` 合成 console session 数据
- [ ] `PrometheusInstanceObservabilityService.CreateConsoleSession` 合成 console session 数据（real adapter，按 Sprint 13 模式）
- [ ] RBAC scope：`scope:instances:console`；无权限返回 `403`
- [ ] 回归测试覆盖：200 成功、400 非 vm、400 非法 protocol、422 非 running、403 无权限
- [ ] `make test` 通过
- [ ] 不修改 OpenAPI `v1.yaml`（consume only）

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
TBD

## SPEC Reference
- §3.1 Endpoint 冻结路径（`POST /instances/{id}/console` → `createInstanceConsoleSession`）
- §4.0 Core handler 调用链
- §5.1.3 Console handler 伪代码
- §9.3 Core 单元测试矩阵（US-005 `createConsoleSession 200 + 400 + 422` 待补）

## UX Reference
N/A（Core only）
