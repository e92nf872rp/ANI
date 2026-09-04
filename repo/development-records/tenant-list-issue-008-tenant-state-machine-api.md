# TENANT-LIST-ISSUE-008：租户列表管理 — 冻结 / 解冻 / 停用

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #8）
> **完成日期：** 2026-09-04
> **Scope：** US-006/US-007 `FreezeTenant` / `UnfreezeTenant` / `DisableTenant`（svc 编排 + Core 事务状态机 + lifecycle）
> **依赖：** Issue-003（tenants 状态列 / tenant_lifecycle）、Issue-004（网关路由）、既有 `QuotaSvcClient.GetQuota`
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-008-tenant-state-machine-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入改动（含 review-it 后 `used+reserved` 守卫收口）

## 交付内容

1. **freeze：** 仅 active → frozen；Core 同事务 `frozen_at=now()` + `tenant_lifecycle('freeze')`；非法 → 409 `TENANT_STATE_INVALID`
2. **unfreeze：** 仅 frozen → active；清 `frozen_at` + lifecycle(`unfreeze`)
3. **disable：** svc 前置 GetQuota，四维（gpu/cpu/memory/storage）任一 **`used+reserved > 0`** → 409 `TENANT_HAS_RUNNING_RESOURCES`；通过后 Core active/frozen → disabled（清 `frozen_at`、设 `disabled_at`）+ lifecycle；**不释放资源**
4. **审计：** `tenant.freeze` / `tenant.unfreeze` / `tenant.disable`，details 含 before/after status
5. **lifecycle 归因：** BOSS `x-user-id` / `x-request-id` → `X-ANI-Actor-User-ID` / `X-Request-ID` → Core `WithTenantLifecycleAttribution` → `user_id` / `request_id`
6. **Gateway：** svc 状态机写超时 15s；幂等键网关处理（仍透传 gRPC，service 不消费）

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `pkg/adapters/runtime/postgres_tenant.go` | Freeze/Unfreeze/Disable 事务 + lifecycleAttribution + state reject |
| `pkg/adapters/runtime/postgres_tenant_test.go` | 成功 / STATE_INVALID / NotFound / unfreeze 清 frozen_at |
| `pkg/ports/tenant.go` | Freeze/Unfreeze/Disable 签名（归因走 ctx，不传 requestID 参数） |
| `services/tenant-service/internal/service/tenant_service.go` | transitionTenantState + Disable 配额守卫 |
| `services/tenant-service/internal/service/tenant_test.go` | freeze/unfreeze/disable + Used/ReservedBlocked |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | 三 POST + `corePropagateHeaders` |
| `services/tenant-service/internal/repo/adapters/core/sdk_client.go` | 统一头透传 |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | `withTenantLifecycleCtx` / `adminActorUserID` |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | freeze/unfreeze/disable handler |
| `api/openapi/services/v1.yaml` | disable 描述：used+reserved |
| PRD / SPEC / UX / Issue-008 | 守卫规则同步为 used+reserved |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| freeze 仅 active；非法 → 409 | Core WHERE + `FreezeTenant_StateInvalid` | ✅ |
| unfreeze 仅 frozen；清 frozen_at | Core UPDATE + 单测 | ✅ |
| disable 四维 used+reserved>0 → 409，不调 Core | `DisableTenant_UsedBlocked` / `ReservedBlocked` | ✅ |
| 其它维占用不挡禁用 | `OtherDimUsedAllowed` | ✅ |
| 四维全 0 可通过（含从 frozen） | `AllGuardZero` | ✅ |
| 不实现资源释放 | Core 仅 UPDATE+lifecycle | ✅ |
| 审计前后状态 | transitionTenantState / disable 成功路径 | ✅ |
| 200 `{ id, message }`；幂等网关 | IdempotentResult + gateway middleware | ✅ |
| Core 单事务 + NotFound/STATE_INVALID 区分 | `tenantStateTransitionReject` | ✅ |
| 不改 users.status；quota 行保留 | 无相关 UPDATE/DELETE | ✅ |
| 真库集成（lifecycle 行 / 终态再操作） | 本批以单测 + fake 为主 | ⚠️ 未跑 live |

## Design Decisions

### D1：禁用守卫用 `used + reserved > 0`（用户确认）

- **Ambiguity / 初版：** Issue/PRD 初写仅 `used > 0`。
- **Choice：** 四维任一 `used+reserved > 0` 即拒绝；错误文案 `used+reserved > 0`；审计 snapshot 含 used/reserved。
- **Rationale：** 有预留无 used 时仍占容量，停用应同样拦截；用户明确要求。

### D2：lifecycle 归因统一走 HTTP/gRPC 头，不经 body

- **Choice：** `x-user-id` → `X-ANI-Actor-User-ID`；`x-request-id` → `X-Request-ID`；Core `withTenantLifecycleCtx` 注入 ctx；非法 actor UUID → `user_id` NULL。
- **Rationale：** 与 create（#005）一致；`user_id` 必须是 BOSS 操作者，不是 CORE_API_TOKEN 主体；直调 Core 时回退认证主体。

### D3：状态机条件 UPDATE + 二次 SELECT 区分错误

- **Choice：** `UPDATE … WHERE status=期望` 0 行后 `SELECT status`：无行 → NotFound；有行 → STATE_INVALID（含当前 status）。
- **Rationale：** Issue AC 要求区分；与 UpdateTenant「disabled=NotFound」不同——状态机应对终态给出明确 409。

### D4：禁用不释放资源（本批明确暂缓）

- **Choice：** 只落 status/disabled_at/lifecycle；不编排停实例/回收 used。
- **Rationale：** Issue/SPEC/PRD 同步收窄；运营须先清四维占用再禁。

### D5：freeze/unfreeze 共用 `transitionTenantState`

- **Choice：** Get before → Core → 审计；disable 单独路径（多配额守卫）。
- **Rationale：** 最小重复；disable 前置步骤不同。

## Deviations

### Dev-1：实现落在 `TenantService` / `postgres_tenant.go`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`、`postgres_tenant_store.go`。
- **实现：** 延续 004–007 合并边界。
- **原因：** proto / 注入已合并。

### Dev-2：网关仍把 idempotency_key 传入 gRPC

- **Issue：** 幂等由网关处理。
- **实现：** handler 仍设 IdempotencyKey；service 不消费。
- **原因：** 与 update/freeze 等写端点现状一致；统一策略待收口。

### Dev-3：真库集成未作为强制门禁

- **Issue：** tenants + lifecycle 断言、终态再操作 409。
- **实现：** Core/service 单测 + fake。
- **原因：** 与 005–007 同策略。

### Dev-4：登录拦截（TENANT_FROZEN / TENANT_DISABLED）不在本批

- **PRD/SPEC：** 冻结/禁用后无法登录。
- **实现：** 仅状态机落库；Gateway auth 403 未见实现（SPEC Q2）。
- **原因：** Issue-008 范围是状态转换 API；登录拦截属独立门禁。

## Tradeoffs

### T1：禁用守卫 — 仅 used vs used+reserved

| 方案 | 结果 |
|---|---|
| 仅 used>0 | 预留中租户仍可禁，容量语义弱 |
| **used+reserved>0（选用，用户确认）** | 更严；运营须释放预留 |

### T2：lifecycle 归因 — body 字段 vs 可信头

| 方案 | 结果 |
|---|---|
| Body actor | 改契约 |
| **头透传（选用）** | 与 #005 一致；依赖服务 token 可信 |

### T3：禁用配额校验 — svc vs Core 双检

| 方案 | 结果 |
|---|---|
| **仅 svc（选用）** | SPEC §5.4-1；接受竞态窗口 |
| Core 再检 | 更严，跨层配额耦合 |

## Review-it 修复记录（2026-09-03 ~ 2026-09-04）

- **用户确认：** 禁用前置改为 `used + reserved > 0`；同步 OpenAPI / Issue AC / PRD / SPEC / UX。
- **单测：** 新增 `DisableTenant_ReservedBlocked`；审计 snapshot 含 reserved。
- **注释：** Core DisableTenant 注明守卫在 svc（used+reserved）。
- **延后：** 登录拦截 Q2、资源释放、幂等入 gRPC 统一、live 集成。

## Verification Commands

```bash
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run "PostgresTenant(Freeze|Unfreeze|Disable)"
go test ./services/tenant-service/internal/service/ -count=1 -run "FreezeTenant|UnfreezeTenant|DisableTenant"
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #009 Auth | 状态机独立；frozen/disabled 下 SSO 写策略另定 |
| #013 Lifecycle 查询 | 本批已写入 freeze/unfreeze/disable 行，可供列表 |
| Gateway auth | 需补 TENANT_FROZEN / TENANT_DISABLED 才能完成 PRD「无法登录」 |
| BOSS UI | 禁用 409 文案应含「占用或预留」；按状态显隐冻结/解冻/禁用 |
