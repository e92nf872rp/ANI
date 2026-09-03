# TENANT-LIST-ISSUE-007：租户列表管理 — 修改租户基本信息

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #7）
> **完成日期：** 2026-09-03
> **Scope：** US-005 `UpdateTenant`（service 部分更新编排 + 审计 + Core SQL 部分更新）
> **依赖：** Issue-001（契约）、Issue-003（tenants 扩展列）、Issue-004（网关路由骨架）
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-007-update-tenant-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入改动（含 review-it 后 Core WHERE 语义收口）

## 交付内容

1. **service：** `UpdateTenant` — 空更新拒绝 → 校验 display_name(1-128) / contact_email → `GetTenant` → **disabled → 409 `TENANT_STATE_INVALID`** → Core 部分更新 → 审计 `tenant.update`（before/after 非敏感字段）→ `{ id, message }`
2. **Core：** `PostgresTenant.UpdateTenant` — 动态 SET 仅提供字段 + `updated_at`；`WHERE id = $1 AND status <> 'disabled'`；0 行（不存在或 disabled）→ `ErrTenantNotFound`；不触碰 name/status/plan_id；成功后 `loadTenantDetail` 回读
3. **SDK / Gateway：** Core SDK `PUT /admin/tenants/{id}`；svc `PUT /svc/tenants/{tenantId}`（可选字段 + idempotency_key 透传 gRPC，service 不消费）
4. **契约：** Services OpenAPI update 409 注明 `TENANT_STATE_INVALID` / 幂等冲突；Core OpenAPI 说明 disabled → 404（与 Core 实现一致）

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `pkg/adapters/runtime/postgres_tenant.go` | `UpdateTenant` 动态 SET + `status <> 'disabled'` |
| `pkg/adapters/runtime/postgres_tenant_test.go` | 部分更新 / 仅 display_name / 空拒绝 / NotFound / disabled→NotFound |
| `services/tenant-service/internal/service/tenant_service.go` | `UpdateTenant` 编排 + 审计 |
| `services/tenant-service/internal/service/tenant_test.go` | 空拒绝 / disabled 409 / 部分字段 / frozen 可改 |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | `UpdateTenant` SDK body 映射 |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | update handler（*string 可选语义） |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | Core admin update（raw JSON 可选字段） |
| `api/openapi/services/v1.yaml` | update 409 语义补全 |
| `api/openapi/v1.yaml` | Core update：disabled 并入 404 说明 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 两者均未提供 → 400 | service + `UpdateTenant_EmptyRejected` | ✅ |
| name/status 不可改（schema + 仅两可选字段） | `UpdateTenantInput` 无 name/status | ✅ |
| disabled → 409（svc） | `UpdateTenant_DisabledRejected` | ✅ |
| 审计 before/after | `tenant.update` + details | ✅ |
| 200 `{ id, message }` | IdempotentResult | ✅ |
| Core 仅更新提供字段 + updated_at | 动态 SET | ✅ |
| Core 0 行 → NotFound | UPDATE RETURNING + 单测 | ✅ |
| 不触碰 name/status/plan_id | SET 白名单 + 单测断言 | ✅ |
| frozen 可更新 | `UpdateTenant_DisplayNameOnly`（frozen） | ✅ |
| 真库集成（单字段/双字段） | 本批以单测 + fake 为主 | ⚠️ 未跑 live |

## Design Decisions

### D1：disabled 校验分层 — svc 409，Core 404

- **Ambiguity：** Issue 要求 svc 对 disabled 返回 409；Core AC 只写 `WHERE id`、0 行 NotFound。
- **Choice（用户确认）：** svc 仍先读状态并返回 **409 `TENANT_STATE_INVALID`**；Core 使用 `WHERE id AND status <> 'disabled'`，disabled 与不存在**不区分**，一律 **404 NotFound**。
- **Rationale：** BOSS 路径给明确终态错误；Core 保持单次 UPDATE、错误面最简。

### D2：部分更新用指针 / proto wrappers，非 COALESCE 占位

- **Choice：** 未传字段不进入 SET；传了再 trim/校验。Core 动态拼装 SQL，非 `COALESCE($n, col)`。
- **Rationale：** 避免「传 null/空」与「未传」语义混淆；与 OpenAPI「未传表示不更新」一致。空串 display_name / 非法 email → 400。

### D3：frozen 允许修改基本信息

- **Choice：** 仅拒绝 disabled；frozen 可改 display_name/contact_email（单测覆盖）。
- **Rationale：** Issue/PRD 只约束终态不可编辑；冻结挡登录，不挡运营改展示信息。UX 详情页仍可出「编辑信息」。

### D4：成功后回读详情视图

- **Choice：** UPDATE RETURNING id 后同事务 `loadTenantDetail`（counts + auth）。
- **Rationale：** Core admin 响应与 GetTenant 字段对齐；svc 审计 after 取 ContactEmail/DisplayName。

## Deviations

### Dev-1：实现落在 `TenantService` / `postgres_tenant.go`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`、`postgres_tenant_store.go`。
- **实现：** 延续 004–006：RPC 并入 `TenantService`；Core 扩展 `PostgresTenant`。
- **原因：** 边界已合并。

### Dev-2：Core WHERE 比 Issue 字面多 `status <> 'disabled'`

- **Issue Core AC：** `WHERE id`。
- **实现：** `WHERE id AND status <> 'disabled'`；disabled → NotFound（用户确认）。
- **原因：** 单语句防终态被直调 admin 改写；不引入二次 SELECT / 状态错误码。

### Dev-3：网关仍把 `idempotency_key` 传入 gRPC

- **Create（#005）：** 幂等仅网关，不入 gRPC。
- **Update：** handler 仍设 `IdempotencyKey`；service **不消费**。
- **原因：** 与 freeze 等其它写端点现状一致；统一策略待后续收口。

### Dev-4：真库集成未作为强制门禁

- **Issue 说：** 单字段/双字段更新集成断言 name/status 不变。
- **实现：** Core/service 单测 + fake。
- **原因：** 与 005/006 同策略。

## Tradeoffs

### T1：Core disabled — 409 vs 并入 404 vs 仅 svc 校验

| 方案 | 优点 | 缺点 |
|---|---|---|
| Core 返回 STATE_INVALID | 与 svc 错误码对称 | 需区分 0 行原因（多一次读或 RETURNING status） |
| 仅 svc 校验、Core 无 status 条件 | 实现最少 | 直调 admin 可改 disabled |
| **WHERE status<>disabled → NotFound（选用，用户确认）** | 单次 UPDATE；Core 错误面简单 | svc 409 与 Core 404 语义分层，调用方需知 |

### T2：审计 before — 先 Get vs UPDATE … RETURNING 旧值

| 方案 | 结果 |
|---|---|
| **先 GetTenant（选用）** | 自然拿到 before；顺带做 disabled 409 |
| RETURNING 旧列 | 少一跳，但 svc 仍要状态机语义 |

### T3：邮箱校验 — 仅 svc vs Core 也校

| 方案 | 结果 |
|---|---|
| **svc `validEmail`（选用）** | 与 create 一致；Core 只保证非空 |
| Core 再校 format | 直调 admin 更严；略重复 |

## Review-it 修复记录（2026-09-03）

- **初版：** 曾考虑 Core `SELECT FOR UPDATE` + `TENANT_STATE_INVALID`。
- **用户纠正：** 改为单次 `UPDATE … WHERE id AND status <> 'disabled'`；disabled **不**单独报状态错，与不存在同为 **NotFound**。
- **契约：** Core OpenAPI update 按 404 说明；Services OpenAPI 保留 svc 层 409 `TENANT_STATE_INVALID`。
- **延后：** gRPC 仍传幂等键、Update 5s 超时、Core 不强制 email format。

## Verification Commands

```bash
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run "PostgresTenantUpdateTenant"
go test ./services/tenant-service/internal/service/ -count=1 -run "UpdateTenant"
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #008 状态机 | 写路径审计/幂等惯例；disabled 终态与「不可编辑」一致 |
| #009 Auth | 基本信息与 auth 写路径独立；详情编辑 Dialog 不改 auth |
| BOSS UI | 编辑 Dialog：空更新勿提交；disabled 隐藏入口；处理 409 vs 404 |
