# 实现 LokiLogStore adapter（走 Loki HTTP API）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

新增 `pkg/adapters/runtime/loki_log_store.go`，实现 `ports.LogStore` interface。通过 Loki HTTP API `/loki/api/v1/query_range` 查询持久化日志，LogQL 用 namespace label 过滤实现多租户隔离，cursor 映射为 RFC3339 时间戳 ↔ Loki start（Unix 纳秒）。依赖 #4 LogStore port 定义。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/loki_log_store.go`（新增）

## Acceptance Criteria

- [ ] 新增文件 `repo/pkg/adapters/runtime/loki_log_store.go`，实现 `LogStore` interface
- [ ] 使用 Loki HTTP API `/loki/api/v1/query_range`，LogQL：`{namespace="<namespace>",pod="<instance_id>"} | json`
- [ ] cursor 映射：cursor 是 RFC3339 时间戳，adapter 内部转换为 Loki `start` 参数（Unix 纳秒）；`next_cursor` 是结果最后一条的 timestamp
- [ ] 多租户隔离：通过 LogQL 的 `{namespace="ani-tenant-<tenant_id>"}` label 过滤，不使用 Loki X-Scope-OrgID
- [ ] 解析 Loki 返回的日志行，映射为 `InstanceLogEntry`（timestamp、level、message、container/stream）
- [ ] Loki 不可达时返回包装错误，不伪造空结果
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 LogQL 构造、cursor 双向映射、Loki HTTP 响应解析

## Dependencies
#4 — LogStore port 定义

## Type
core

## Priority
high

## Labels
core

## Batch
B-3

## References
- SPEC: §3.3 LokiLogStore 实现（US-005）
- UX: N/A（后端 adapter 实现）
- PRD: US-005 / FR-9 / FR-10
