# TENANT-LIST-ISSUE-005：租户列表管理 — 可用套餐 + 创建租户

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #5）
> **完成日期：** 2026-09-03
> **Scope：** US-001 `ListAvailablePlans` + US-002 `CreateTenant`（service 编排 + Core 事务 + Gateway 行为收口）
> **依赖：** Issue-003（迁移）、Issue-004（路由骨架）、既有 `tenant_plans` store / `buildQuotaLimitViews` / `scheduleQuotaSyncRetry`
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-005-create-tenant-api.md`
> **分支：** `tenant-list`
> **相关提交：** `5f7cca8`（实现主体）+ 本地收口（actor 头 / 创建超时 / 幂等不入 gRPC）

## 交付内容

1. **US-001：** `ListAvailablePlans` → `PostgresTenantPlanStore.ListActivePlans`（仅 `active` 且未删除；返回 id/code/name；空列表合法；不调 Core）
2. **US-002 service：** 入参校验（name/email/密码强度）→ 套餐 active 校验 → `buildQuotaLimitViews` → bcrypt(12) → Core `CreateTenant` → 事务外配额同步 + 失败不回滚 + 异步重试 → `audit_logs`
3. **US-002 Core：** `PostgresTenant.CreateTenant` 单事务：tenants + tenant_auth + users + user_roles(tenant-admin) + tenant_lifecycle('create')；name UNIQUE → `ErrTenantNameConflict`
4. **归因透传：** BOSS 操作者经 gRPC `x-user-id` → SDK `X-ANI-Actor-User-ID` → Core `adminActorUserID` → `tenant_lifecycle.user_id`；`X-Request-ID` 对齐 `request_id`
5. **Gateway：** 创建路径 30s 写超时；`idempotency_key` 仅网关中间件消费，**不**写入 gRPC `CreateTenantRequest`

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `services/tenant-service/internal/service/tenant_service.go` | `ListAvailablePlans` / `CreateTenant` 编排 + 校验 |
| `services/tenant-service/internal/service/tenant_test.go` | 可用套餐 / 密码边界 / 创建成功·冲突·PLAN_NOT_ACTIVE；actor metadata |
| `services/tenant-service/internal/repo/ports/tenant_plan_store.go` | `ListActivePlans` + `AvailableTenantPlan` |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go` | `ListActivePlans` SQL |
| `services/tenant-service/internal/repo/ports/core_tenant.go` | `CreateTenantInput` 增加 RequestID / ActorUserID |
| `services/tenant-service/internal/repo/adapters/core/{sdk_client,tenant_svc_client}.go` | CreateTenant SDK + `corePropagateHeaders` |
| `pkg/ports/tenant.go` / `pkg/adapters/runtime/postgres_tenant.go` | Core CreateTenant 事务实现 |
| `pkg/adapters/runtime/postgres_tenant_test.go` | 事务 INSERT / name 冲突单测 |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | create 不传幂等键；30s 超时 |
| `services/ani-gateway/internal/router/tenant_common.go` | `tenantWriteCallCtx` |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | `adminActorUserID`（可信头优先） |
| `services/ani-gateway/internal/router/admin_tenant_resources_test.go` | actor 头优先 / 回退单测 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| ListActivePlans 仅 active、空列表合法 | store SQL + `TestTenantService_ListAvailablePlans_*` | ✅ |
| 入参校验 / 密码 ≥3 类 | `validateCreateTenantInput` / `TestValidateAdminPassword` | ✅ |
| plan 非 active → PLAN_NOT_ACTIVE | service + 单测 | ✅ |
| bcrypt 后调 Core；明文不出边界 | CreateTenant + 单测断言 hash ≠ 明文 | ✅ |
| name 冲突 → TENANT_NAME_CONFLICT | Core UNIQUE + mapSDKError + 单测 | ✅ |
| 配额失败不回滚 + scheduleQuotaSyncRetry | 步骤 7 成功路径继续；复用 plan 域重试 | ✅（用户确认） |
| 审计含 email/username、不含密码 | writeAuditSuccess details | ✅ |
| 幂等由网关处理、service 不感知 | create handler 不设 IdempotencyKey | ✅（用户确认） |
| Core 5 表事务 + lifecycle | `PostgresTenant.CreateTenant` | ✅ |
| lifecycle.user_id = BOSS 操作者 | Actor 头透传 + `adminActorUserID` | ✅（用户确认） |
| 单测覆盖 Issue 所列路径 | service / runtime / gateway | ✅ |
| 真库集成（5 表 + quota + audit） | 本批以单测 + fake 为主 | ⚠️ 未跑 live 集成（见 Open Questions） |

## Design Decisions

### D1：US-001 与 US-002 同批，且可用套餐先于创建

- **Ambiguity：** 初版 Issue-006 曾含 available-plans；后迁入 Issue-005。
- **Choice：** 创建向导 Step2 数据源与创建编排同 Issue；AC 顺序先 ListAvailablePlans 再 CreateTenant。
- **Rationale：** UX 创建流强依赖 active 套餐列表；与 Create 的 plan_id 校验同源 store。

### D2：lifecycle.user_id 必须是 BOSS 操作者（非 CORE_API_TOKEN 主体）

- **Ambiguity：** Core Gateway 直调时用 `GetUserID`；svc→Core 时认证主体是服务 token。
- **Choice：** 网关 `x-user-id` → service `ActorUserID` → HTTP 头 `X-ANI-Actor-User-ID` → Core `adminActorUserID`（合法 UUID 优先，否则回退认证主体）。
- **Rationale：** 用户明确要求；US-015 生命周期需展示真实平台操作人；与 `audit_logs.user_id` 同源 metadata。

### D3：幂等只在网关，不传入 tenant-service

- **Choice：** Idempotency 中间件读 body/header；`CreateTenant` gRPC 不填 `idempotency_key`（proto 字段可保留、service 不消费）。
- **Rationale：** 用户确认 + Issue AC「本层不感知重放」；避免 service 与网关双重幂等语义。

### D4：创建写路径 gRPC 超时 30s

- **Choice：** `tenantWriteCallCtx(..., 30s)`，读路径仍 5s。
- **Rationale：** bcrypt(12)+Core 事务+配额初始化易超过 5s；避免「Core 已成功、网关已取消 → 重试 name 冲突」。

### D5：配额初始化失败不回滚租户

- **Choice：** 与 SPEC §5.4-2 / PRD US-002 一致：租户保留 + `tenant.quota_init_failed` 审计 + 1s/2s/4s 重试。
- **Rationale：** 用户确认；部分失败优于整单回滚导致向导假失败。

## Deviations

### Dev-1：实现落在 `TenantService` / `postgres_tenant.go`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`、`postgres_tenant_store.go`。
- **实现：** 延续 Issue-004：RPC 并入 `TenantService`；Core 扩展 `PostgresTenant`。
- **原因：** proto / 注入边界已合并，避免双实现。

### Dev-2：真库集成测试未在本记录中作为强制门禁跑通

- **Issue 说：** 集成验证 5 表 + resource_quota + audit_logs。
- **实现：** Core/store/service 单测 + fake；live PG 集成留给部署环境或后续 gate。
- **原因：** 本地可复现闭环优先；live 依赖迁移已应用的开发库。

### Dev-3：proto `CreateTenantRequest.idempotency_key` 仍存在但网关不传

- **SPEC/OpenAPI：** 客户端仍可在 HTTP body 带 `idempotency_key`（供网关中间件）。
- **实现：** gRPC 调用不设该字段。
- **原因：** 幂等所有权在网关；保留 proto 字段以免破坏生成物/其他调用方。

## Tradeoffs

### T1：操作者透传 — 可信头 vs 改 OpenAPI body

| 方案 | 优点 | 缺点 |
|---|---|---|
| Body 增 actor 字段 | 显式 | 破坏「不在 OpenAPI body」约定 |
| **X-ANI-Actor-User-ID（选用）** | 不改契约；直调 Core 仍用认证主体 | 依赖服务 token 可信；头需文档化 |

### T2：配额失败 — 回滚 vs 补偿

| 方案 | 结果 |
|---|---|
| 回滚租户 | 向导失败清晰，但 Core 补偿复杂 |
| **不回滚 + 异步重试（选用）** | 与 SPEC/用户确认一致；需盯审计告警 |

### T3：创建超时 — 全局加大 vs 仅写路径

| 方案 | 结果 |
|---|---|
| 全部 gRPC 30s | 读路径拖死连接 |
| **仅 create 30s（选用）** | 最小改动 |

## Review-it 修复记录（2026-09-03）

- **P1：** 创建网关 5s 超时 → 30s 写超时（`tenantWriteCallCtx`）。
- **P1：** `X-Request-ID` / **`X-ANI-Actor-User-ID`** 透传，lifecycle 归因 BOSS 操作者（用户确认）。
- **P1：** create 路径不再把 `idempotency_key` 传入 service（用户确认）。
- **确认：** 配额失败不回滚租户（用户确认，保持 SPEC）。

## Verification Commands

```bash
cd repo
go test ./services/tenant-service/internal/service/ -count=1 -run "ListAvailablePlans|CreateTenant|ValidateAdminPassword"
go test ./pkg/adapters/runtime/ -count=1 -run "PostgresTenantCreateTenant"
go test ./services/ani-gateway/internal/router/ -count=1 -run "AdminActor|AdminTenant|TenantList|ToAdmin"
go test ./services/tenant-service/internal/repo/adapters/core/ -count=1
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #006 列表/详情 | GetTenant 扩展字段 / ListTenants；available-plans 已不在 #006 |
| #007 Update | 复用 Core UpdateTenant + 幂等/超时惯例 |
| #008 状态机 | lifecycle 写入应复用 Actor 头 |
| #011/#012 配额 | 复用 sync / retry 模式 |
