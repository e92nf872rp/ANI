# SPEC: 租户列表管理

> Technical specification derived from:
> - PRD: [prd-new-boss-tenant-list.md](../../prd/boss/tenant/prd-new-boss-tenant-list.md)
> - UX: [ux-boss-tenant-list.md](../../ux/boss/tenant/ux-boss-tenant-list.md)
> Generated: 2026-09-02 | **Revised to match implementation: 2026-09-04**
> Evidence: `repo/development-records/tenant-list-issue-*.md`；Issues `issue-001`～`014`（010 OPEN）

---

## 1. Summary

### 1.1 What This SPEC Covers

BOSS 租户列表管理：**后端已落地**（OpenAPI + Gateway + tenant-service gRPC + Core ports/adapters + 迁移）。覆盖创建、列表/详情、基本信息编辑、冻结/解冻/禁用（四维配额守卫）、SSO/MFA 配置读写、配额代理、配额变更三件套、lifecycle/audit-logs、租户内管理员列表。

**未交付 / Deferred：**
- Issue-010：`TestTenantSso` → **501 `NOT_IMPLEMENTED`**（ports 有，adapter/`main` 注入 nil）
- Gateway 登录拦截 `TENANT_FROZEN` / `TENANT_DISABLED`
- MFA 登录强制执行（仅配置落库）
- 禁用时资源释放
- BOSS 前端页面（P5，规格见 UX）

**关键决策（已确认且已实现）：**
1. Core 随本模块交付 `/admin/tenants*` + `getTenant` additive
2. 首位管理员角色 `tenant-admin`
3. 三态 `active|frozen|disabled`（存量 suspended→frozen、deleted→disabled）

### 1.2 PRD Reference

US-001～017、FR-1～10；以实现为准的 AC 见已修订 PRD。

### 1.3 Design Decisions Summary

| Decision | Choice（实现） | Rationale |
|----------|----------------|-----------|
| gRPC | **无独立 TenantListService**；**19 RPC** 挂 `TenantService`（`tenant_plan.proto`）；`tenant_list_service.proto` = **messages only** | 与套餐/管理员同服务装配，减少进程内多 server |
| Core store | 扩展既有 `PostgresTenant`（`postgres_tenant.go`） | 非新建 `postgres_tenant_store.go` |
| Service 实现 | `tenant_service.go` | 非 `tenant_list_service.go` |
| lifecycle 归因 | Gateway `x-user-id`/`x-request-id` → Core `X-ANI-Actor-User-ID`/`X-Request-ID` → `WithTenantLifecycleAttribution(ctx)` | Freeze/Create 等方法签名**无** user_id/request_id |
| 枚举 | `TenantStatus` ≠ `TenantLifecycleAction` | status=active/frozen/disabled；action=create/freeze/unfreeze/disable |
| tenant_auth / lifecycle | Core 表，状态转换同事务写 lifecycle | 一致性 |
| audit_logs | tenant-service `AuditStore` 直读直写 | `ListTenantAuditLogs` |
| tenant_quota_change | `TenantStore`（非独立 QuotaChangeStore 名）；**无 HasPending** | 跨请求同维 pending 允许 |
| SSO test | stub 501；真实现延期 Issue-010 | Q1 Secret 约定仍 open |
| 禁用校验 | svc 调 GetQuota；仅四维 used+reserved；不释放资源 | PRD US-007 |
| 幂等 | Services body `idempotency_key`（header 回落）；**Core 写无幂等** | 与 tenant-plans 一致 |
| SSO/MFA API | Services 两 PUT → Core 单一 `PUT .../auth` | UX Tab 分块 vs 单表 |

---

## 2. Architecture

### 2.1 System Context

```
BOSS Frontend (规格见 UX；实现可另批)
  REST /api/v1/svc/tenants*  (19；body idempotency_key)
        │
        ▼
ani-gateway  tenant_list_resources.go
  tenantCallCtx: x-user-id, x-request-id
        │ gRPC TenantService (19 list RPCs)
        ▼
tenant-service  tenant_service.go
  ├── TenantStore / AuditStore (PG: quota_change, audit_logs, plans)
  └── TenantSvcClient ──HTTP──▶ Core /api/v1/admin/tenants*
                                    │
                         ani-gateway admin_tenant_resources.go
                         + WithTenantLifecycleAttribution
                                    │
                         PostgresTenant (postgres_tenant.go)
                         PG: tenants / tenant_auth / tenant_lifecycle / users / …
```

**Frozen Facts（现状）**

| 项目 | 状态 |
|------|------|
| Core OpenAPI `/admin/tenants*` + getTenant 扩展 | **已落地**（Issue-001） |
| Services OpenAPI `/tenants*`（19） | **已落地** |
| Gateway 19 svc + 9 admin 路由 | **已落地** |
| TestTenantSso | **stub 501** |
| 登录拦截 FROZEN/DISABLED | **Deferred** |
| BOSS 前端 | **未交付** |

### 2.2 Component Design

| Component | Location |
|-----------|----------|
| Gateway svc | `ani-gateway/internal/router/tenant_list_resources.go` |
| Gateway Core admin | `admin_tenant_resources.go` |
| gRPC 实现 | `tenant-service/internal/service/tenant_service.go`（`TenantService.Register`） |
| Core ports | `pkg/ports/tenant.go` |
| Core adapter | `pkg/adapters/runtime/postgres_tenant.go` |
| Core SDK 客户端 | `tenant-service/.../adapters/core/tenant_svc_client.go` |
| 配额变更 | `adapters/postgres/tenant_store.go` + ports `TenantStore` |
| 审计列表 | `adapters/postgres/audit_store.go` → `ListTenantAuditLogs` |
| SSO ports | `ports/sso.go`；**adapters/sso 未实现**；main 注入 nil |
| Proto | `tenant_list_service.proto`（messages）+ `tenant_plan.proto`（`TenantService` RPC） |

### 2.3 Module Interactions（摘要）

**创建：** 校验 → plan active → bcrypt → Core CreateTenant（同事务 lifecycle create，归因 ctx）→ UpsertQuota（失败不回滚）→ audit `tenant.create`

**禁用：** GetQuota → 四维 used+reserved → Core DisableTenant（lifecycle disable）→ audit；**不释放资源**

**配额变更提交：** `x-request-id`（可 `req_` 前缀）+ **`x-user-id` 必填** → ListQuotaMeta（未注册 → `QUOTA_RESOURCE_NOT_REGISTERED`）→ GetQuota（old_value int64，无行→0）→ InsertPending → 同维同 request → `QUOTA_CHANGE_REQUEST_CONFLICT`

**SSO test（目标）：** GetTenantAuth → Secret → OIDC discovery；**当前 501**

### 2.4 File Structure（以实现为准）

```
repo/api/openapi/v1.yaml
repo/api/openapi/services/v1.yaml
repo/api/proto/tenant/v1/tenant_list_service.proto   # messages only
repo/api/proto/tenant/v1/tenant_plan.proto           # TenantService + 19 RPCs
repo/deploy/migrations/20260902_001_tenant_list_management.sql
repo/pkg/ports/tenant.go
repo/pkg/adapters/runtime/postgres_tenant.go
repo/services/ani-gateway/internal/router/tenant_list_resources.go
repo/services/ani-gateway/internal/router/admin_tenant_resources.go
repo/services/tenant-service/main.go
repo/services/tenant-service/internal/service/tenant_service.go
repo/services/tenant-service/internal/service/tenant_test.go
repo/services/tenant-service/internal/repo/ports/{core_tenant,tenant_store,sso,errors}.go
repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go
repo/services/tenant-service/internal/repo/adapters/postgres/{tenant_store,audit_store}.go
# 未落地：adapters/sso/、frontends/boss tenant-list 页面
```

---

## 3. Data Model

### 3.1 Schema（`20260902_001_tenant_list_management.sql`）

- tenants：CHECK `active|frozen|disabled`；列 contact_email / frozen_at / disabled_at（`IF NOT EXISTS`）
- tenant_auth：1:1；存量 `INSERT … ON CONFLICT DO NOTHING`
- tenant_lifecycle：action CHECK create|freeze|unfreeze|disable；索引 `(tenant_id, created_at DESC)`
- **存量 lifecycle create 回填**（幂等 `WHERE NOT EXISTS`，`created_at = tenants.created_at`）
- RLS：平台绕过用 **`NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL`**
- GRANT TO `ani_app`；不新建 quota_change / audit_logs

### 3.2 Entity / Ports（要点）

```go
// pkg/ports
type TenantStatus string // active | frozen | disabled
type TenantLifecycleAction string // create | freeze | unfreeze | disable

type Tenant struct { /* … */ Status TenantStatus; Auth *TenantAuthSummary; /* … */ }
type TenantLifecycleEntry struct { Action TenantLifecycleAction; /* … */ }

// FreezeTenant(ctx, tenantID) — 无 reqID 参数；归因来自 ctx
```

service 侧 `TenantSvcClient` 同语义；配额变更走 `TenantStore.InsertPendingQuotaChanges` / `SetQuotaChangeStatusByRequestID` 等。

### 3.3 Relationships / Migration Plan

同既有：auth 1:1、lifecycle 1:N、plan_id、quota_change、首位 tenant-admin。单文件迁移；依赖 `20260501000100` → `20260810000200` → 本文件。

---

## 4. API Design

### 4.1 Services（相对 `/tenants*`，base `/api/v1/svc`）→ gRPC **TenantService**

端点表同实现（19 行：available-plans … admins）。幂等列 = body `idempotency_key`（header 回落）。  
`POST .../auth/sso/test`：**当前 501**。

### 4.2 Core（`/api/v1/admin/tenants*`）

9 新端点 + getTenant 扩展。**写端点无 idempotency。**  
Services SSO/MFA 两 PUT → Core `PUT .../auth`。  
lifecycle action filter：`TenantLifecycleAction`。

### 4.3 Schema 要点

- UpdateTenant：动态 SET；svc disabled→**409**；Core disabled→**404**
- Auth provider：null/omit=不更新；`""`=清空；disabled 改 Auth→**409**
- MFA：Gateway 要求显式 `mfa_required`（omit→400）
- GetQuota：单次 Core；display_name 空兜底 resource_type；不可达 **502**
- quota-requests list items 用 **`request_id`**（非顶层 id）；old_value **int64（0 占位）**
- admins：`TenantScopedAdmin`；默认 admin ∪ inviting

### 4.4 Breaking Changes

1. status CHECK 收窄  
2. getTenant additive（兼容）  
3. 无 URL 破坏；全部新增路径

---

## 5. Business Logic

### 5.1 Algorithms

创建 / 配额变更 / 审批：见 §2.3；与 PRD US-002/012/014 一致。

### 5.2 Validation

| 规则 | 错误 |
|------|------|
| name / password / plan | 400 / 409 / 422 PLAN_NOT_ACTIVE |
| SSO 开启无 provider | 422 TENANT_SSO_CONFIG_INVALID |
| quota items 批内重复等 | 422 QUOTA_CHANGE_REQUEST_INVALID |
| meta 未注册/未启用 | **422 QUOTA_RESOURCE_NOT_REGISTERED** |
| 同 request 同维 PK | **409 QUOTA_CHANGE_REQUEST_CONFLICT** |
| 缺 x-request-id / x-user-id（提交变更） | 400 |
| 状态机非法 | 409 TENANT_STATE_INVALID |
| 禁用四维占用 | 409 TENANT_HAS_RUNNING_RESOURCES |

### 5.3 State Machine

freeze / unfreeze / disable 同事务写 lifecycle；无 enable。

### 5.4 Edge Cases

1. 禁用竞态窗口接受；**不释放资源**；仅四维守卫  
2. 配额初始化失败不回滚 + 重试  
3. 审批后 Upsert 失败：状态已改，审计 failure  
4. 跨请求同维 pending 允许  
5. 幂等由 Gateway Redis；service 不感知重放  
6. tenant_auth 缺行 → 默认双 false  
7. **SSO discovery：目标行为**；当前端点 501（勿当已交付）

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Code | HTTP | 说明 |
|------|------|------|
| VALIDATION_FAILED | 400 | |
| TENANT_NOT_FOUND | 404 | |
| TENANT_NAME_CONFLICT | 409 | |
| TENANT_STATE_INVALID | 409 | |
| TENANT_HAS_RUNNING_RESOURCES | 409 | |
| PLAN_NOT_ACTIVE | 422 | |
| TENANT_SSO_CONFIG_INVALID | 422 | |
| QUOTA_CHANGE_REQUEST_INVALID | 422 | |
| QUOTA_RESOURCE_NOT_REGISTERED | 422 | |
| QUOTA_CHANGE_REQUEST_CONFLICT | 409 | |
| QUOTA_CHANGE_REQUEST_NOT_PENDING | 409 | |
| QUOTA_CHANGE_REQUEST_NOT_FOUND | 404 | |
| GRPC_CLIENT_UNAVAILABLE | **502** | Core/gRPC 不可达（非 503） |
| NOT_IMPLEMENTED | 501 | TestTenantSso stub |
| TENANT_FROZEN / TENANT_DISABLED | 403 | **Deferred**（未接线） |

### 6.2 Retry / Failure

写操作幂等重试（Gateway）；配额补偿 3 次指数退避。SSO test 不自动重试。PG 不可达：bootstrap 摘除。

---

## 7. Security

- svc：platform JWT；admin/ops 写、readonly 读  
- Core admin：内部转发 + RBAC scope  
- **登录拦截 FROZEN/DISABLED：未实现**（勿当当前保证）  
- 密码 bcrypt(cost=12)；审计不含明文  
- RLS：NULLIF 平台绕过

---

## 8. Performance

量级 BOSS 管理端；LATERAL admin_count；plan_code 批量；keyset 分页；详情 Tab 懒加载（前端规格）。

---

## 9. Testing Strategy

- 单测：`tenant_test.go` + `postgres_tenant_test.go`（状态机、配额变更、auth、lifecycle、admins）  
- **不以真库集成为强制门禁**  
- US-009 test / 登录拦截 / MFA enforce：**不在已覆盖验收内**（OPEN/Deferred）

---

## 10. Implementation Plan（回顾）

| 实际批次 | 内容 | 状态 |
|----------|------|------|
| Issue-001～009 | 契约/接口/迁移/网关/CRUD/状态机/Auth | DONE |
| Issue-011～014 | 配额/变更/lifecycle·audit/admins | DONE |
| Issue-010 | SSO test | **OPEN / 501** |
| 前端 P5 | UX 规格 | 未交付 |
| 登录拦截 / MFA enforce / 资源释放 | — | Deferred |

历史 Phase/Issue 编号表已由 `tenant-list/issue-001`～`014` 取代。

---

## 11. Open Questions & Risks

### 11.1

- **Q1：** SSO Secret 命名（阻塞 Issue-010）  
- **Q2：** 登录拦截 → **Confirmed deferred**（非本模块当前验收）  
- **Q3：** search ILIKE 同时匹配 name/display_name（已实现；不转义 `%`/`_`）  
- **Q4：** audit resource 透传展示、不过滤（已实现）  
- **Q5：** MFA 登录强制 → Deferred  

### 11.2 Risks

R1 status 收窄；R2 创建事务；R3 禁用竞态；R4 单角色；R5 SSO/K8s（仅影响 010）。

### 11.3 Assumptions

- 共享 PG；Gateway Redis 幂等 + `tenantCallCtx`  
- `buildQuotaLimitViews` / `scheduleQuotaSyncRetry` 可复用  
- PRD/UX/Issue 已按实现回写（2026-09-04）
