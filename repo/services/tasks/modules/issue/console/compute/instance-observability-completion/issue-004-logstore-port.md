# 新增 ports.LogStore interface 抽象

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

新增 `pkg/ports/log_store.go`，定义 `LogStore` interface、`LogQueryRequest`、`LogQueryResult` 结构。`Cursor` 为 opaque string，port 层不约束其内部语义，由 adapter 内部映射为 Loki time / ES search_after / K8s tailLines。这是日志持久化链路的 port 抽象基础。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/ports/log_store.go`（新增）

## Acceptance Criteria

- [ ] 新增文件 `repo/pkg/ports/log_store.go`，定义 `LogStore` interface、`LogQueryRequest`、`LogQueryResult` 结构
- [ ] `LogStore.QueryLogs(ctx, req)` 方法签名：入参含 `TenantID`、`InstanceID`、`Namespace`、`Limit`、`Cursor`、`Level`；出参含 `Items []InstanceLogEntry`、`NextCursor string`
- [ ] `Cursor` 为 opaque string，port 层不约束其内部语义（adapter 内部映射为 Loki time / ES search_after / K8s tailLines）
- [ ] 不修改现有 `InstanceObservability` interface（LogStore 是内部组合，不对外暴露）
- [ ] Typecheck/lint passes
- [ ] `make validate-architecture` 通过（port 抽象符合 ports/adapters 规则）

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
B-3

## References
- SPEC: §3.1 ports.LogStore interface（US-004）
- UX: N/A（后端 port 抽象）
- PRD: US-004 / FR-5
