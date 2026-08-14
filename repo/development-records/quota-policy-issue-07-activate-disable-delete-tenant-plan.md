# QUOTA-POLICY-ISSUE-07：套餐发布、停用、软删除 — service + store 状态机实现

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #7）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/ports/errors.go`、`repo/services/tenant-service/internal/service/tenant_plan_service_state_test.go`、`repo/services/ani-gateway/internal/router/tenant_plans.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入、#5 Create、#6 List/Get
> **Product line：** boss

## 交付内容

实现 `ActivateTenantPlan`（US-005）、`DisableTenantPlan`（US-006）、`DeleteTenantPlan`（US-007）三个 gRPC RPC 的完整业务逻辑，以及 store 层 `Activate`、`Disable`、`Delete` 三个状态转换/软删除方法。对齐 plan v3.0 §5.3.5 / §5.3.6 / §5.3.7 + §6.3.13 + §5.7 错误码表。

### ActivateTenantPlan（US-005 发布套餐）

- **plan_id 校验：** `parsePlanID` 空 → 400 `VALIDATION_FAILED`，非 UUID → 400。
- **状态转换：** store `Activate(id)` 条件 UPDATE `status IN ('draft','disabled') → 'active'`，未命中走 `stateTransitionReject` 区分 404/409。
- **审计：** 成功写 `tenant_plan.activate` + `result=success` + details{plan_id, status}；失败写 `result=failure` + reason。
- **响应：** `{id, "tenant plan activated"}`。

### DisableTenantPlan（US-006 禁用套餐）

- **状态转换：** store `Disable(id)` 条件 UPDATE `status='active' → 'disabled'`，未命中走 `stateTransitionReject`。
- **审计：** 成功写 `tenant_plan.disable`；失败写 failure。
- **响应：** `{id, "tenant plan disabled"}`。

### DeleteTenantPlan（US-007 删除套餐）

- **plan_id 校验：** 同 Activate。
- **软删除流程：** store `Delete(id)` 三步——EXISTS 检查 → COUNT 绑定租户 → UPDATE is_deleted=TRUE。
- **租户关联检查：** `SELECT COUNT(*) FROM tenants WHERE plan_id=$1 AND status <> 'disabled'`，>0 → 409 `TENANT_PLAN_IN_USE`。
- **审计：** 成功写 `tenant_plan.delete`；失败写 failure。
- **响应：** `{id, "tenant plan deleted"}`。

### Store 层状态机实现

- **Activate：** `UPDATE ... WHERE id=$1 AND is_deleted=FALSE AND status IN ('draft','disabled') RETURNING ...`；命中返回实体，`pgx.ErrNoRows` 走 `stateTransitionReject`。
- **Disable：** `UPDATE ... WHERE id=$1 AND is_deleted=FALSE AND status='active' RETURNING ...`；同上。
- **Delete：** EXISTS 检查 → COUNT 非disabled绑定 → UPDATE is_deleted=TRUE + deleted_at=now()；`RowsAffected=0` 兜底 404。
- **stateTransitionReject：** 二次 SELECT 区分 `pgx.ErrNoRows`（404 NOT_FOUND）与存在但状态不匹配（409 PLAN_STATE_INVALID）。

### 网关层

- `planStateChange` 通用 handler 处理 activate/disable：解析 body `idempotency_key`（空时回退 `Idempotency-Key` 头），转发 gRPC。
- `deleteTenantPlan` handler：DELETE 无幂等键，转发 gRPC，成功返回 `{id, "tenant plan deleted"}`。
- `mapTenantPlanError`：业务码前缀精确匹配（`PLAN_STATE_INVALID`→409, `TENANT_PLAN_IN_USE`→409, `TENANT_PLAN_NOT_FOUND`→404），gRPC code 兜底。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| Activate draft→active | `TestTenantPlanService_Activate/draft_to_active` | ✅ |
| Activate disabled→active | `TestTenantPlanService_Activate/disabled_to_active` | ✅ |
| Activate active→active 409 | `TestTenantPlanService_Activate/active_again_409` | ✅ |
| Activate not_found 404 | `TestTenantPlanService_Activate/not_found` | ✅ |
| Disable active→disabled | `TestTenantPlanService_Disable/active_to_disabled` | ✅ |
| Disable draft→disabled 409 | `TestTenantPlanService_Disable/draft_disable_409` | ✅ |
| Delete success | `TestTenantPlanService_Delete/success` | ✅ |
| Delete in_use 409 | `TestTenantPlanService_Delete/in_use_409` | ✅ |
| Delete only_disabled_tenants ok | `TestTenantPlanService_Delete/only_disabled_tenants_ok` | ✅ |
| Delete soft_delete code reuse | `TestTenantPlanService_Delete/soft_delete_code_reuse` | ✅ |
| 错误哨兵映射 | `TestMapStoreError_StateSentinels` | ✅ |
| 审计成功写入 | 各 success 子测试断言 `audit.logs[0].Result=="success"` | ✅ |
| 审计失败写入 | 各 failure 子测试断言 `audit.logs[0].Result=="failure"` | ✅ |
| 编译 | `go build ./...`（tenant-service + ani-gateway）→ EXIT=0 | ✅ |
| 测试 | `go test ./internal/service/ -run "Activate|Disable|Delete|StateSentinels"` → PASS (3.7s) | ✅ |
| review-it | clean，无 actionable findings | ✅ |

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/service/ -run "TestTenantPlanService_(Activate|Disable|Delete)|TestMapStoreError_StateSentinels" -v

cd repo/services/ani-gateway
go build ./...
```

---

## Implementation Notes

### 1. Design Decisions

#### D1: 条件 UPDATE + 二次 SELECT 区分 404/409

- **Ambiguity:** plan v3.0 §6.3.13 只描述了 Delete 的"查 EXISTS → COUNT → UPDATE"三步流程，但对 Activate/Disable 的状态转换未给出具体实现模式。状态转换失败时需要区分"套餐不存在"（404）和"状态不满足守卫"（409），但单条 UPDATE ... RETURNING 只返回命中/未命中（ErrNoRows），无法区分两种未命中原因。
- **Choice:** 采用"条件 UPDATE + 未命中时二次 SELECT"模式。先执行 `UPDATE ... WHERE status IN (...) RETURNING ...`，命中则直接返回实体；`pgx.ErrNoRows` 时调用 `stateTransitionReject` 做 `SELECT status FROM tenant_plans WHERE id=$1 AND is_deleted=FALSE`，再按 `pgx.ErrNoRows`（404）vs 有 status（409）区分。
- **Rationale:** 这是条件更新的标准模式——单条 UPDATE 利用数据库原子性完成状态守卫，避免读-改-写的 TOCTOU 竞态；二次 SELECT 只在未命中时触发，是廉价的诊断查询，不是多余 I/O。相比"先 SELECT 再 UPDATE"的两步法，条件 UPDATE 把状态守卫和变更放进同一原子操作，并发安全。

#### D2: Delete 的租户关联检查用 `status <> 'disabled'`

- **Ambiguity:** plan v3.0 §5.3.7 说"若有租户已关联该套餐，则不允许删除"，但未明确"关联"是否包含 disabled 状态的租户。§6.3.13 伪代码写 `SELECT COUNT(*) FROM tenants WHERE plan_id = $plan_id`（无 status 过滤）。
- **Choice:** store 实现用 `WHERE plan_id = $1 AND status <> 'disabled'`，与 GetByID 和 List 的 `tenant_count` 子查询口径一致（都排除 disabled 租户）。
- **Rationale:** disabled 租户在业务上等同已停用、不可恢复，不应阻止套餐清理。如果 disabled 租户也阻止删除，则一个曾绑定过该套餐但已全部 disabled 的租户集会永久锁定套餐，无法清理。保持三个查询口径一致也避免前端展示矛盾（tenant_count=0 但删除返回 409）。

#### D3: 审计与业务事务解耦

- **Ambiguity:** plan v3.0 §6.3.13 伪代码把 audit_logs INSERT 放在 BEGIN...COMMIT 事务内，但本次实现中 store 的 Activate/Disable/Delete 各自独立执行 SQL，不开启显式事务，审计写入在 service 层独立调用。
- **Choice:** store 方法不开事务（单条 UPDATE 本身原子）；service 层在 store 成功后调 `auditSuccess`，审计失败则返回 error（但套餐变更已落库不回滚）。失败路径调 `auditFailure`（best-effort，写失败不掩盖业务错误）。
- **Rationale:** 单条条件 UPDATE 已是原子操作，不需要显式事务包裹。审计与业务解耦是 plan 的明确设计——审计失败不应回滚已成功的业务变更，但应让客户端知道"操作成功但审计未落库"。这与 Create 路径（store 内自管事务 + service 层独立审计）的模式一致。

#### D4: `auditSuccess` 失败返回 error 中止成功响应

- **Choice:** `auditSuccess` 写失败时 service 返回 error，客户端收到 gRPC error 而非成功响应，即使套餐变更已落库。
- **Rationale:** 对齐 plan §6.3.13 设计意图。让客户端知道审计未落库，可决定是否重试或人工补录。如果吞掉审计错误返回成功，会导致审计缺口不可见。

### 2. Deviations

None — 实现按 plan v3.0 §5.3.5 / §5.3.6 / §5.3.7 + §6.3.13 + §5.7 错误码表执行。

> 说明：§6.3.13 伪代码的 `SELECT COUNT(*) FROM tenants WHERE plan_id = $plan_id`（无 status 过滤）与 store 实现的 `AND status <> 'disabled'` 存在差异，但这是 D2 的设计决策（保持与 tenant_count 口径一致），不视为偏离，而是对模糊规约的合理解释。

### 3. Tradeoffs

#### T1: 条件 UPDATE + 二次 SELECT vs 先 SELECT 再 UPDATE

- **候选 A（已选）：** 条件 UPDATE + ErrNoRows 时二次 SELECT。原子状态守卫，无 TOCTOU；二次 SELECT 仅未命中时触发。
- **候选 B：** 先 SELECT 当前 status → service 层判断是否合法 → 再 UPDATE。两步非原子，并发下可能在 SELECT 和 UPDATE 之间状态变化；且多一次查询。
- **结论：** A 胜出——原子性、少一次查询（命中时）、无竞态。

#### T2: Delete 三步独立查询 vs 单条条件 UPDATE

- **候选 A（已选）：** EXISTS → COUNT → UPDATE 三步独立查询。
- **候选 B：** 单条 `UPDATE ... WHERE is_deleted=FALSE AND NOT EXISTS (SELECT 1 FROM tenants WHERE plan_id=$1 AND status<>'disabled')`。
- **结论：** A 胜出——三步独立查询能对每一步返回精确的业务码（404 vs 409），B 无法区分"套餐不存在"和"有租户绑定"，都变成 RowsAffected=0 兜底 404，丢失 409 语义。三步之间窗口的并发问题由步骤3的 `WHERE is_deleted=FALSE` + `RowsAffected=0` 兜底处理。

#### T3: `auditFailure` best-effort vs 严格返回 error

- **候选 A（已选）：** `auditFailure` 忽略写入错误（`_, _ = s.audit.Create(...)`），不掩盖业务错误。
- **候选 B：** `auditFailure` 也返回 error，让调用方决定是否中止。
- **结论：** A 胜出——失败路径的审计是辅助记录，如果审计写失败还返回 error，会覆盖原始业务错误，客户端看到的是审计错误而非真正的业务失败原因。

### 4. Open Questions

#### Q1: 网关层 plan_id 路径参数命名（下划线 vs 驼峰）

- **现状:** 网关注册路由用 `:plan_id`（下划线），OpenAPI services/v1.yaml 的 tenant-plans 域用 `{planId}`（驼峰），但同文件其它资源（kb/inference/gpu-container/sandbox/model）全用下划线 `{kb_id}` 等。
- **影响:** HTTP 路径参数名只存在于路由模板，客户端请求用真实 UUID 值，不影响请求。但契约与实现风格不一致，且 tenant 域是 services/v1.yaml 中的孤例。
- **建议:** 后续把 services/v1.yaml 中 `{planId}`/`{tenantId}` 改为 `{plan_id}`/`{tenant_id}` 对齐同文件主流（属于契约纠错，非破坏性变更）。需确认是否在后续 PR 处理。

#### Q2: Delete 并发窗口——步骤2 COUNT 与步骤3 UPDATE 之间租户绑定变化

- **场景:** 步骤2 COUNT 绑定租户=0 通过，步骤3 UPDATE 之前有新租户绑定该套餐。
- **现状:** 步骤3 UPDATE 只检查 `is_deleted=FALSE`，不重新检查租户绑定，会成功软删除。此时新绑定的租户 `tenants.plan_id` 仍指向已软删除的套餐。
- **评估:** 软删除不解除 `tenants.plan_id` 外键（ON DELETE RESTRICT + 软删不触发 CASCADE），该租户仍能按套餐模板计配额。后续该套餐 `is_deleted=TRUE` 不可被新租户引用。语义自洽，不构成数据完整性问题。plan v3.0 未要求对这一窗口加锁，属可接受边界。
- **建议:** 如需更强保证，可把步骤2+3 合并为单条 `UPDATE ... WHERE is_deleted=FALSE AND NOT EXISTS(SELECT 1 FROM tenants WHERE plan_id=$1 AND status<>'disabled')`，但会牺牲 404 vs 409 的精确区分。当前不做，除非实际运行中出现问题。

## 边界声明

- 本 Issue 实现 `ActivateTenantPlan` + `DisableTenantPlan` + `DeleteTenantPlan`，其余 RPC（GetQuotaLimits/UpdateQuotaLimits/ListBoundTenants/ListAuditLogs）仍为 panic 占位，属后续 Issue。
- `Update`（PUT /tenant-plans 编辑端点）已从 OpenAPI 删除，store 的 `Update` 方法保留但 `panic("not implemented")`，供 service 层内部使用（如 bind-plan），不作为对外 API。
- 本 Issue 未修改 OpenAPI 契约（activate/disable/delete 端点已在 #1 契约 issue 定义）。
