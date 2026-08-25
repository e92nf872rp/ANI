# Reconciler 改造 — Confirm/Cancel + 超时 + 删除双调 + 对账

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，改造 reconciler 实现 Confirm/Cancel 同事务（监听 Volcano Pod 状态转换，含 outbox 事件写入）、provisioning 超时机制、删除实例 Cancel+Release 双调、排队 vs 调度失败区分、对账循环。

## Scope

- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/reconcile_controller.go`

## Acceptance Criteria

- [ ] Confirm/Cancel 同事务：pending→running 触发 Confirm（reserved→used）；pending→failed 触发 Cancel（释放 reserved）；running→failed 触发 Release（释放 used）
- [ ] 同事务 outbox 写入：每个状态转移在 tenant 事务内调用 `outboxWriter.WriteTx` 写 outbox 事件（plan.md §6.3.2 行 597/601/605）：
  - pending→running：`instance.confirmed`
  - pending→failed：`instance.cancelled`
  - running→failed：`instance.released`
  - deleting→deleted：`instance.deleted`
  - 超时失败（markProvisioningFailed）：`instance.create_failed`
- [ ] `bootstrap/deps.go`：`GPU_QUOTA_ENABLED=true` 时注入 `OutboxWriter` 到 reconciler（`WithOutboxWriter`）
- [ ] 新增 `markProvisioningFailed` 超时机制：默认 10 分钟（PROVISIONING_TIMEOUT_MIN 环境变量），超时标 failed 并 Cancel 释放配额
- [ ] 新增 `cancelQuotaAndFinalize` 公共方法，供删除流程复用
- [ ] 删除实例时 Cancel + Release 双调（不依赖原态判定，覆盖 pending/running/failed 三种删除前原子）
- [ ] 新增 Pod Events 读取能力 + 节点 idle 查询，区分排队中(queued) vs 调度失败(failed)
- [ ] 对账循环：遍历 K8s 中该租户非终态 GPU Pod 计算实际 used，与 PG 比对，本期只打 log 告警不修正
- [ ] Confirm/Cancel/Release 均在 tenant 事务内执行，配合 WorkloadInstanceStoreTx.UpsertStatusTx 同事务写状态
- [ ] `go build ./pkg/adapters/runtime/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestReconcile" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#4, #5

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §5.1 (Reconciler Confirm/Cancel 同事务算法, 删除双调算法, outbox 同事务写入), §2.4 (File Structure)
- plan.md: §6.3.2 (行 597-606, outbox INSERT 在每个状态转移点), §6.3.3 (行 686, 删除流程 outbox)
- UX: N/A
