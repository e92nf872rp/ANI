# SPEC: 租户管理员管理 (new)

> Technical specification derived from:
> - PRD: [prd-new-boss-tenant-admin.md](../../prd/boss/tenant/prd-new-boss-tenant-admin.md)
> - UX: [ux-boss-tenant-admin.md](../../ux/boss/tenant/ux-boss-tenant-admin.md)
> - Plan: [租户管理plan v3.0](../../plan/tenant/租户管理plan%20v3.0.md) §4.1.4 / §4.1.4b / §5.4.1~5.4.12 / §5.7 / §6.3.16 / §6.3.17 / §6.3.17b-d / §7.3.4 / §7.4
> Generated: 2026-08-07 | Product: BOSS | Code scope: `repo/frontends/boss/src/` + `repo/services/tenant-service/`（新建微服务）+ `repo/services/ani-gateway/` + `repo/api/openapi/services/v1.yaml`

> Scope: BOSS 前端 **+ 后端 tenant-service + 网关路由 + Services OpenAPI 契约**（非纯 UI 批次）。因当前 `services/v1.yaml` 尚未定义本模块端点（见 §2.1 待补），需在实现第一条 Issue 时补齐契约。

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 定义租户管理员全生命周期管理与查询的技术实现：邀请（生成 token 链接）、重发邀请、跨租户管理员列表、管理员详情、修改权限、重置密码、禁用/启用/软删除，以及指定管理员角色权限查询与操作历史查询，并新增可用租户列表查询端点（邀请管理员选择器数据源）。后端新建 `tenant-service`，新增 `tenant_admin_invitation` 邀请独立表（不改 `users.status`、不预绑角色），对外经 ani-gateway 提供 `/api/v1/svc/tenants/{tenantId}/admins/*` 与 `/api/v1/svc/tenant-admins` 端点（含 `/tenant-admins/tenants` 可用租户列表）；前端为 BOSS 新增「租户管理员」列表页 + 邀请向导 + 详情 Drawer。

本模块**不含**租户内管理员列表 `GET /tenants/{tenantId}/admins`（US-017，暂不实现，归租户列表 PRD）；不含配额变更申请（归租户列表 PRD）。

### 1.2 PRD Reference

- Source: [prd-new-boss-tenant-admin.md](../../prd/boss/tenant/prd-new-boss-tenant-admin.md)
- UX source: [ux-boss-tenant-admin.md](../../ux/boss/tenant/ux-boss-tenant-admin.md)
- User Stories covered: US-001 邀请、US-002 重发、US-003 跨租户列表、US-004 详情、US-005 改权限、US-007 重置密码、US-008 禁用/启用/删除、US-009 权限查询、US-010 操作历史、US-011 查询可用租户列表、US-012 查询可变角色列表
- Functional Requirements covered: FR-1、FR-1a、FR-1b、FR-2、FR-3、FR-4、FR-5、FR-6、FR-7、FR-8

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 邀请生命周期承载 | 新增 `tenant_admin_invitation` 独立表 | 不改 `users.status`（仅 active/disabled）、不预绑角色；邀请状态机 `inviting→accepted/rejected/expired` 独立演进 |
| 邀请 token 安全 | 仅落库 `token_hash=SHA-256(token)`，原始 token 仅一次性返回 | 库中不存明文 token，防拖库泄露 |
| 邀请有效期 | `expire_at = now() + 72h` | 契约 FR-1 |
| 跨租户列表范围 | **仅返回租户管理员(tenant-admin)、正在被邀请或邀请已过期的用户**；普通成员 user 仅邀请中或已过期时才出现 | 契约 FR-2 / US-003 |
| 「邀请中」是状态同级展示 | `is_inviting` 为独立标记字段（后端契约），前端将「邀请中」作为与 active/disabled 同级的**状态值**展示与筛选项 | 仅前端展示合并，不推翻「不改 users.status」契约 |
| 后端传输 | tenant-service gRPC server；对外 REST 由 ani-gateway 转发 | 对齐 quota-policy 已落地的 gRPC 模式 |
| 幂等性 | POST/PUT 写操作 body 必填 `idempotency_key`；DELETE 不幂等、不携带 | 契约 ANI Boundaries |

---

## 2. Architecture

### 2.1 System Context

```
┌──────────────────┐   REST /api/v1/svc/tenants/{tid}/admins/* + /tenant-admins
│   BOSS Frontend  │──────────────────────▶┌─────────────────────┐    gRPC    ┌────────────────────┐
│  (boss/src/)     │  JWT Bearer + 幂等/审计│   ani-gateway       │──────────▶│   tenant-service    │
│                  │                       │  (已有)              │           │  (新建 gRPC server) │
│ 列表/邀请/详情/  │                       │ 路由+鉴权+限流       │           │                    │
│ 改角色/重置      │                       │ 幂等/审计(RBAC)      │           │ 邀请/重发/权限/     │
│ 禁用/启用/删除   │                       │ gRPC client 转发     │           │ 重置/禁用/          │
│                  │                       └──────┬──────────────┘           │ 启用/删除/历史      │
└──────────────────┘                              │                           └────┬─────────┬─────┘
                                            gRPC  │                                │         │ Core SDK
                                                  │                                │         │ HTTP
                                        ┌─────────▼─┐                          ┌──────▼──────┐  ┌───▼──────────┐
                                        │ ANI Core   │                          │Core SDK     │  │ ANI Core     │
                                        │ gRPC       │                          │adapter      │  │ /api/v1/admin│
                                        │ (转发)      │                          │(core/*.go)  │  │ users/roles  │
                                        └───────────┘                          └─────────────┘  └──────────────┘
                                                                             ┌────────────────────▼─┐
                                                                             │  PostgreSQL          │
                                                                             │ tenant_admin_       │
                                                                             │ invitation / audit_logs│
                                                                             └──────────────────────┘
```

**Frozen Facts Table**（来自 `module-delivery-workflow.md`，针对 Services 层新端点）：

| 项目 | 状态 |
|------|------|
| Frozen Paths | `repo/api/openapi/services/v1.yaml` 中 **尚无** `/tenants/{tenantId}/admins/*`、`/tenant-admins` → **待补**（首个实现 Issue 补齐，引用 operationId 见 §4.1） |
| Frozen Schemas | `AdminWithTenant`、`InvitationResult`、`UserPermissions`、`CursorPage` → **待补**（不写为 frozen） |
| Frozen Response / Error codes | §6 错误码表（`TENANT_ADMIN_NOT_FOUND` 等）→ **待补**到 `services/v1.yaml` |
| Non-Frozen Capabilities | 邀请通知发送（SMTP/短信）— 待补，本模块仅触发通知渠道占位 |
| Known Risky Assumptions | `services/v1.yaml` 契约未冻结，按 §4 契约实现后须回填并校验 |

### 2.2 Component Design

| Component | Responsibility | Location |
|-----------|---------------|----------|
| TenantAdminAPI（网关） | HTTP 入站转发：请求解析+校验+调 gRPC+响应组装+错误码映射+幂等/审计 | `repo/services/ani-gateway/internal/router/tenant_admin_resources.go`（新建） |
| TenantAdminService（gRPC） | 邀请/重发/详情/改角色/重置/禁用/启用/删除/权限查询/跨租户列表/操作历史业务逻辑 | `repo/services/tenant-service/internal/service/tenant_admin_service.go` |
| TenantAdminStore | 数据持久化接口（tenant-service 自有 ports）+ 领域模型 | `repo/services/tenant-service/internal/repo/ports/tenant_admin_store.go` |
| PostgresTenantAdminStore | PostgreSQL 适配器（仅 tenant_admin_invitation / audit_logs，**不直接操作 users/user_roles/roles**） | `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go` |
| TenantAdminSvcClient（Core SDK） | Core 租户管理员 API 客户端接口（MatchUser/IsAlreadyAdmin/GetUser/BatchGetUsers/GetAdminDetail/ListTenantAdmins/ChangeRole/GetRolePermissions/ListAssignableRoles/SetStatus/SoftDelete/ResetPassword） | `repo/services/tenant-service/internal/repo/ports/core_tenant_admin.go`（接口）+ `repo/services/tenant-service/internal/repo/adapters/core/`（SDK 适配器） |
| TenantSvcClient（Core SDK） | Core 租户最小读 API 客户端（GetTenant/ListAvailableTenants） | `repo/services/tenant-service/internal/repo/ports/core_tenant.go`（接口）+ `repo/services/tenant-service/internal/repo/adapters/core/`（SDK 适配器） |
| TenantAdminAPI（前端） | 前端 API 封装（fetchCore 或 openapi-fetch typed paths） | `repo/frontends/boss/src/api/tenant-admins.ts` |
| TenantAdminsPage | 前端列表页 | `repo/frontends/boss/src/routes/_authenticated/tenants/admins.tsx` |
| AdminListTable | 列表表格 + 行操作列 | `repo/frontends/boss/src/components/tenant-admins/AdminListTable.tsx` |
| AdminDetailDrawer | 详情 Drawer（概览/权限/操作记录三 Tab） | `repo/frontends/boss/src/components/tenant-admins/AdminDetailDrawer.tsx` |
| InviteAdminDialog | 邀请向导 Dialog | `repo/frontends/boss/src/components/tenant-admins/InviteAdminDialog.tsx` |
| RoleChangeDialog | 修改角色 Dialog | `repo/frontends/boss/src/components/tenant-admins/RoleChangeDialog.tsx` |
| ResetPasswordDialog | 重置密码 Dialog | `repo/frontends/boss/src/components/tenant-admins/ResetPasswordDialog.tsx` |

### 2.3 Module Interactions

```
邀请流程:
  Frontend → ani-gateway → POST /api/v1/svc/tenants/{tenantId}/admins/invite
    → TenantAdminAPI(网关)[幂等/审计/RBAC] → (gRPC) TenantAdminService.Invite
      → TenantAdminSvcClient.MatchUser(tenantId, email, username)           // Core SDK → Core /api/v1/admin/（无匹配 → 404）
      → TenantAdminSvcClient.IsAlreadyAdmin(tenantId, userId)               // Core SDK
      → TenantAdminStore.HasPendingInvitation                               // SQL → tenant_admin_invitation
      → TenantAdminStore.InsertInvitation(token_hash, status='inviting', expire_at+72h)
      → audit.Create('tenant_admin.invite')                               // 审计（复用 TenantPlanAuditStore）
      → 通知渠道占位(拼接邀请链接)
    → Response { id, token, expire_at, message }

重发流程:
  Frontend → ani-gateway → POST .../invitation/resend
    → TenantAdminService.Resend
      → TenantAdminStore.GetLatestInvitation(tenantId, userId)        // SQL → tenant_admin_invitation（404 / 终态 409）
      → TenantAdminStore.UpdateInvitation(new token_hash, expire_at+72h, status='inviting', clean accepted_at/rejected_at)
      → 审计 'tenant_admin.resend_invitation'

跨租户列表:
  Frontend → ani-gateway → GET /api/v1/svc/tenant-admins?limit&cursor&tenant_id&status&is_inviting&is_expired&search&role&source
    → TenantAdminService.ListAllTenantAdmins（只读，无审计）
      → TenantAdminSvcClient.ListTenantAdmins                               // Core SDK → Core /api/v1/admin/（JOIN users+user_roles+roles+tenants）

详情:
  Frontend → ani-gateway → GET /api/v1/svc/tenants/{tenantId}/admins/{userId}
    → TenantAdminService.GetAdminDetail
      → TenantAdminSvcClient.GetAdminDetail(tenantId, userId)                // Core SDK
      → TenantAdminStore.GetInvitationFlags(tenantId, userId)               // SQL → tenant_admin_invitation（is_inviting / is_expired）

改角色:
  Frontend → ani-gateway → PUT /api/v1/svc/tenants/{tenantId}/admins/{userId}/role
    → TenantAdminService.ChangeRole
      → TenantAdminSvcClient.ChangeRole(tenantId, userId, role_id)           // Core SDK → Core /api/v1/admin/

权限查询:
  Frontend → ani-gateway → GET /api/v1/svc/tenants/{tenantId}/admins/{userId}/role
    → TenantAdminService.GetRolePermissions
      → TenantAdminSvcClient.GetRolePermissions(tenantId, userId)            // Core SDK

可变角色列表:
  Frontend → ani-gateway → GET /api/v1/svc/tenants/{tenantId}/roles
    → TenantAdminService.ListTenantRoles（只读，无审计）
      → TenantAdminSvcClient.ListAssignableRoles(tenantId)                   // Core SDK → Core /api/v1/admin/
      → 返回 items [{ id, name, tenant_id, permissions }]

重置密码:
  Frontend → ani-gateway → POST .../reset-password
    → TenantAdminService.ResetPassword
      → TenantAdminSvcClient.ResetPassword(tenantId, userId, new_password)   // Core SDK → Core /api/v1/admin/

禁用/启用/删除:
  Frontend → ani-gateway → POST .../disable | /enable | DELETE ...
    → TenantAdminService.Disable / Enable / Delete
      → TenantAdminSvcClient.SetStatus(tenantId, userId, 'disabled'/'active')  // Core SDK
      → TenantAdminSvcClient.SoftDelete(tenantId, userId)                     // Core SDK（仅 Delete）
      → 审计 'tenant_admin.disable / enable / delete'

可用租户列表:
  Frontend → ani-gateway → GET /api/v1/svc/tenant-admins/tenants
    → TenantAdminService.ListAvailableTenants（只读，无审计）
      → TenantSvcClient.ListAvailableTenants                                // Core SDK → Core /api/v1/admin/tenant-admins/available-tenants
      → 返回 items [{ id, name, display_name, status }]
```

### 2.4 File Structure

```
api/proto/tenant/v1/
└── tenant_admin_service.proto     [NEW — gRPC 接口与数据模型（TenantAdminService RPC）]

pkg/generated/pb/tenant/v1/        [NEW — buf 生成 pb.go]

repo/api/openapi/services/
└── v1.yaml                        [MODIFY — 新增 /tenants/{tenantId}/admins/*、/tenant-admins 路径/schema/错误码]

repo/services/tenant-service/      [NEW — 已按 quota-policy 创建，本模块新增]
├── internal/service/
│   └── tenant_admin_service.go    [NEW — gRPC TenantAdminService server + 业务逻辑]
└── internal/repo/
    ├── ports/
    │   ├── tenant_admin_store.go  [NEW — 接口 + 领域模型]
    │   ├── core_tenant_admin.go   [NEW — TenantAdminSvcClient 接口]
    │   └── core_tenant.go         [NEW — TenantSvcClient 接口]
    └── adapters/
        ├── postgres/
        │   └── tenant_admin_store.go [NEW — Postgres 适配器]
        └── core/
            ├── tenant_admin_svc_client.go [NEW — Core SDK 适配器]
            ├── tenant_svc_client.go       [NEW — Core SDK 适配器]
            └── sdk_client.go              [NEW — Core SDK 错误映射]

repo/services/ani-gateway/
├── internal/router/
│   └── tenant_admin_resources.go  [NEW — gRPC client 在 router 层持有 + HTTP 转发 + 错误映射]
└── main.go                        [MODIFY — 注册 tenant-service gRPC client]

repo/frontends/boss/src/
├── api/
│   └── tenant-admins.ts           [NEW]
├── routes/_authenticated/
│   └── tenants/
│       └── admins.tsx             [NEW]
├── routes/_authenticated.tsx      [MODIFY — 新增「租户管理」SubMenu 与 3 个菜单项]
└── components/tenant-admins/
    ├── AdminListTable.tsx         [NEW]
    ├── AdminDetailDrawer.tsx      [NEW]
    ├── InviteAdminDialog.tsx      [NEW]
    ├── RoleChangeDialog.tsx       [NEW]
    └── ResetPasswordDialog.tsx    [NEW]
```

> 迁移文件（`tenant_admin_invitation` 建表）见 §3.1；归入现租户管理迁移文件或独立迁移（见 §3.4）。

---

## 3. Data Model

### 3.1 Schema Changes

#### 3.1.1 tenant_admin_invitation — 租户管理员邀请表（新增）

```sql
CREATE TABLE tenant_admin_invitation (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,                          -- SHA-256(原始 token)，不存明文
    status       TEXT NOT NULL
        CHECK (status IN ('inviting', 'accepted', 'rejected', 'expired')),
    expire_at    TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at  TIMESTAMPTZ,
    rejected_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX uk_tenant_admin_invitation_token_hash ON tenant_admin_invitation(token_hash);
-- idx_tenant_admin_invitation_user_status(user_id, status) 由 20260825_001 迁移删除，
-- 替换为部分唯一索引 uk_tenant_admin_invitation_pending(tenant_id, user_id) WHERE status='inviting'，
-- 覆盖 HasPendingInvitation 查询且防止并发邀请竞态。
CREATE UNIQUE INDEX uk_tenant_admin_invitation_pending ON tenant_admin_invitation(tenant_id, user_id) WHERE status = 'inviting';

-- RLS：平台上下文可读写全部行；租户上下文仅本租户行
ALTER TABLE tenant_admin_invitation ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_admin_invitation FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_admin_invitation_platform_bypass
    ON tenant_admin_invitation
    AS PERMISSIVE FOR ALL
    USING (
        current_setting('app.current_tenant_id', true) IS NULL
        OR current_setting('app.current_tenant_id', true) = ''
    );
CREATE POLICY tenant_admin_invitation_tenant_self
    ON tenant_admin_invitation
    AS PERMISSIVE FOR ALL
    USING (
        tenant_id IS NOT NULL
        AND tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
    );

-- GRANT 表级读写给 ani_app_user
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_admin_invitation TO ani_app_user;
```

- 不改 `users.status`（users.status 仅 active/disabled）、不预绑角色。
- 状态机：`inviting → accepted / rejected / expired`；仅 `inviting` 且 `expire_at > now()` 可被接受；接受/拒绝为终态。
- RLS 策略：`platform_bypass`（平台上下文 `app.current_tenant_id` 为 NULL/空 → 全部可见）+ `tenant_self`（租户上下文 → 仅 `tenant_id` 匹配本租户），对齐 `audit_logs` / `tenant_quota_change` 现有模式。

#### 3.1.2 Core 层：users 表列扩展（ALTER）

租户管理员模块需要 `users` 表支持展示名与软删除，当前 Core `users` 表缺少这些列：

```sql
-- display_name：租户管理员展示名（昵称），NULL 表示使用 username
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;

-- is_deleted / deleted_at：软删除标记，TenantAdminSvcClient.SoftDelete 使用
-- 注意：is_deleted 与 users.status 独立——status 为 active/disabled（业务状态），
-- is_deleted 为软删除标记（DELETE 操作使用），二者不互斥
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
```

> `is_deleted` / `deleted_at` 列由 `TenantAdminSvcClient.SoftDelete` 写入（§5.1.6 Delete 流程），列表查询 `WHERE is_deleted = FALSE` 过滤（§5.1.3）。
> `display_name` 由前端展示（§4.2 AdminWithTenant / GetAdminDetail 响应字段）。

### 3.2 Entity Definitions

```go
// repo/services/tenant-service/internal/repo/ports/tenant_admin_store.go

type TenantAdminInvitation struct {
    ID         uuid.UUID
    TenantID   uuid.UUID
    UserID     uuid.UUID
    TokenHash  string   // SHA-256 hex
    Status     string   // inviting | accepted | rejected | expired
    ExpireAt   time.Time
    CreatedAt  time.Time
    AcceptedAt *time.Time
    RejectedAt *time.Time
}

type AdminWithTenant struct {
    ID          uuid.UUID
    Email       string
    Username    string
    DisplayName *string
    Role        string // tenant-admin | user | auditor（列表仅含 admin/邀请中/已过期）
    Status      string // active | disabled
    IsInviting  bool   // true = 该租户下存在 status='inviting' 的邀请；仅作标记
    IsExpired   bool   // true = 该租户下存在 status='expired' 的邀请；仅作标记
    Source      string // third_party | local
    LastLoginAt *time.Time
    CreatedAt   *time.Time // 详情返回
    UpdatedAt   *time.Time // 详情返回
    Tenant      TenantRef
}

type TenantRef struct {
    ID          uuid.UUID
    Name        string
    DisplayName string
}

type TenantAdminListFilter struct {
    Limit      int
    Cursor     string
    TenantID   *uuid.UUID
    Status     string
    IsInviting *bool
    IsExpired  *bool
    Search     string
}

type ListResult struct {
    Items      []AdminWithTenant
    NextCursor string // "" = 无更多
}

type InviteInput struct {
    TenantID uuid.UUID
    Email    string
    Username string
}

type InvitationResult struct {
    ID       uuid.UUID
    Token    string // 原始 token，仅本次返回
    ExpireAt time.Time
    Message  string
}

type UserPermissions struct {
    UserID      uuid.UUID
    TenantID    *uuid.UUID
    RoleID      uuid.UUID // 无绑定时为零值
    Role        string
    Permissions []any    // roles.permissions JSONB 原样
}

type AssignableRole struct {
    ID          uuid.UUID
    TenantID    *uuid.UUID // nil = 平台内置
    Name        string
    Permissions []any      // roles.permissions JSONB 原样
}

type InvitationFlag struct {
    TenantID   uuid.UUID
    UserID     uuid.UUID
    IsInviting bool
    IsExpired  bool
}

type AuditLogListItem struct {
    ID        uuid.UUID
    Action    string
    Resource  string
    Result    string
    UserID    *uuid.UUID
    Details   map[string]any
    CreatedAt time.Time
}
```

> 前端 `AdminWithTenant` 类型以 plan §7.4 为准（id/email/username/display_name/role/status/is_inviting/is_expired/source/last_login_at/created_at?/updated_at?/tenant{id,name,display_name}）。列表不含 created_at/updated_at（可选字段），详情返回。

### 3.3 Relationships

```
tenants (1) ──< tenant_admin_invitation (N) ──> users (1)
tenants (1) ──< tenant_auth (1)              [mfa_required, sso_enabled, sso_provider]
users  (N) ──< user_roles (N) ──> roles (N)
audit_logs (N)  [tenant_id / user_id / action='tenant_admin.*' 关联]
```

### 3.4 Migration Plan

| Step | Layer | Description | Rollback |
|------|-------|-------------|----------|
| 1 | Services | CREATE TABLE tenant_admin_invitation + 索引（token_hash UNIQUE + uk_pending 部分唯一）+ RLS（platform_bypass / tenant_self）+ GRANT | DROP TABLE（索引/策略/RLS 随表删） |
| 1b | Services | 部分唯一索引替换：DROP idx_tenant_admin_invitation_user_status → CREATE uk_tenant_admin_invitation_pending（并发邀请竞态防护） | DROP uk_tenant_admin_invitation_pending → 重建旧索引 |
| 2 | Core | ALTER TABLE users ADD COLUMN display_name / is_deleted / deleted_at | DROP COLUMN display_name / is_deleted / deleted_at |

- 迁移文件：`repo/deploy/migrations/` 下分为三个独立文件：
  - `20260821_001_tenant_admin_invitation.sql` — Step 1（建表 + 索引 + RLS）
  - `20260825_001_tenant_admin_invitation_pending_unique.sql` — Step 1b（部分唯一索引替换）
  - `20260827000200_users_display_name_soft_delete.sql` — Step 2（users 表列扩展）
- Core 层变更（Step 2）因 users 为 Core 表，迁移直接操作 DDL（运行时 CRUD 通过 Core SDK TenantAdminSvcClient 调 Core API）。
- 无跨 Core 表依赖（tenant_admin_invitation 仅引用 tenants.id / users.id 作 FK，二者须已存在）；**users/user_roles/roles 表的运行时操作通过 Core SDK TenantAdminSvcClient 调用 Core `/api/v1/admin/` API，不直接 SQL 操作 Core 表**。

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | Description | AuthZ | idempotency_key (body) |
|--------|------|-------------|-------|------------------------|
| POST | `/api/v1/svc/tenants/{tenantId}/admins/invite` | 邀请管理员 | admin/ops | required |
| POST | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/invitation/resend` | 重发邀请 | admin/ops | required |
| GET | `/api/v1/svc/tenant-admins` | 跨租户管理员列表（仅 admin/邀请中/已过期） | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenants/{tenantId}/admins/{userId}` | 管理员详情（含 is_inviting + created_at/updated_at + tenant 对象） | admin/ops/readonly | — |
| PUT | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/role` | 修改角色 | admin/ops | required |
| GET | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/role` | 查询角色与权限 | admin/ops/readonly | — |
| POST | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/reset-password` | 重置密码 | admin/ops | required |
| POST | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/disable` | 禁用 | admin/ops | required |
| POST | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/enable` | 启用 | admin/ops | required |
| DELETE | `/api/v1/svc/tenants/{tenantId}/admins/{userId}` | 软删除 | admin/ops | **不幂等，不携带** |
| GET | `/api/v1/svc/tenants/{tenantId}/admins/{userId}/audit-logs` | 操作历史 | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenant-admins/tenants` | 可用租户列表（邀请管理员选择器数据源） | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenants/{tenantId}/roles` | 可变角色列表（修改角色选择器数据源） | admin/ops/readonly | — |

> operationId（建议，待回填 `services/v1.yaml`）：`inviteTenantAdmin` / `resendTenantAdminInvitation` / `listAllTenantAdmins` / `getTenantAdminDetail` / `updateTenantAdminRole`(PUT) / `getTenantAdminRole`(GET) / `resetTenantAdminPassword` / `disableTenantAdmin` / `enableTenantAdmin` / `deleteTenantAdmin` / `listTenantAdminAuditLogs` / `listAvailableTenantsForAdmin`(GET /tenant-admins/tenants) / `listTenantRoles`(GET /tenants/{tenantId}/roles)。

### 4.2 Request/Response Schemas

#### POST /tenants/{tenantId}/admins/invite — 邀请

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "email": "admin@acme.io",
  "username": "acme_admin"
}
```
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键 |
| email | string | yes | RFC 5322 |
| username | string | yes | 1-64 字符，不含 `:` |

**Response 200:**
```json
{ "id": "uuid", "token": "raw-token-only-once", "expire_at": "2026-08-10T10:00:00Z", "message": "invitation sent" }
```

#### POST .../invitation/resend — 重发

**Request:** `{ "idempotency_key": "..." }`
**Response 200:** `{ "id": "uuid", "token": "...", "expire_at": "...", "message": "invitation resent" }`

#### GET /tenant-admins — 跨租户列表

**Query:** `limit`(default 20, max 100) / `cursor` / `tenant_id`(可选) / `status` / `is_inviting`(boolean) / `is_expired`(boolean) / `search` / `role` / `source`

**Response 200 (CursorPage):**
```json
{
  "items": [
    {
      "id": "uuid", "email": "a@acme.io", "username": "acme_admin",
      "display_name": "Admin", "role": "tenant-admin", "status": "active",
      "is_inviting": false, "is_expired": false, "source": "local", "last_login_at": "2026-07-20T10:00:00Z",
      "tenant": { "id": "uuid", "name": "acme", "display_name": "ACME" }
    },
    {
      "id": "uuid", "email": "u@acme.io", "username": "acme_user",
      "display_name": null, "role": "user", "status": "active",
      "is_inviting": true, "is_expired": false, "source": "local", "last_login_at": null,
      "tenant": { "id": "uuid", "name": "acme", "display_name": "ACME" }
    }
  ],
  "next_cursor": "cursor-string"
}
```
> **仅返回** role ∈ (tenant-admin) 或正在被邀请（`is_inviting=true`）或邀请已过期（`tenant_admin_invitation.status='expired'`）的用户；普通成员 user 仅邀请中或已过期时才出现。`is_inviting` 仅作标记，不改变 role/status（第二条示例 role=user 且 is_inviting=true）。

#### GET /tenants/{tenantId}/admins/{userId} — 详情

**Response 200:**
```json
{
  "id": "uuid", "email": "a@acme.io", "username": "acme_admin", "display_name": "Admin",
  "role": "tenant-admin", "status": "active", "is_inviting": false, "is_expired": false,
  "source": "local", "last_login_at": "2026-07-20T10:00:00Z",
  "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z",
  "tenant": { "id": "uuid", "name": "acme", "display_name": "ACME" }
}
```
> 不含 password_hash；无顶层 tenant_id 冗余。`is_inviting` 由该租户下是否存在 `tenant_admin_invitation.status='inviting'` 判定；`is_expired` 由是否存在 `tenant_admin_invitation.status='expired'` 判定。

#### PUT /tenants/{tenantId}/admins/{userId}/role — 修改角色

**Request:**
```json
{ "idempotency_key": "...", "role_id": "550e8400-e29b-41d4-a716-446655440000" }
```
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| role_id | string (uuid) | yes | 目标角色 UUID；须为可分配角色（非 platform-*、非 tenant-admin，tenant_id 为空或等于路径 tenantId） |

**Response 200:** `{ "id": "uuid", "message": "role updated" }`

#### GET /tenants/{tenantId}/admins/{userId}/role — 查询角色与权限

**Response 200:**
```json
{
  "user_id": "uuid", "tenant_id": "uuid",
  "role": "tenant-admin", "role_id": "uuid",
  "permissions": [
    {"resource":"instances","actions":["*"],"scope":"tenant"},
    {"resource":"tenants","actions":["read","list","delete","disable","enable","reset-password","change-role","invite","resend-invitation"],"scope":"tenant"}
  ]
}
```
> 仅返回租户成员（tenant_id 非空）权限；平台账户（tenant_id=null）不可经本端点查询。
> role_id 为当前角色绑定 UUID（无绑定时省略）。

#### POST .../reset-password — 重置密码

**Request:**
```json
{ "idempotency_key": "...", "new_password": "NewP@ssw0rd" }
```
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| new_password | string | yes | 8-64 字符，四类中至少三类，须与旧密码不同（HTTPS，明文不落日志） |

**Response 200:** `{ "id": "uuid", "message": "password reset" }`

#### POST .../disable | .../enable

**Request:** `{ "idempotency_key": "..." }`
**Response 200:** `{ "id": "uuid", "message": "admin disabled | enabled" }`

#### DELETE .../admins/{userId} — 软删除

**Response 200:** `{ "id": "uuid", "message": "admin deleted" }`
> 不幂等，不携带 idempotency_key。

#### GET .../audit-logs — 操作历史

**Query:** `limit`(default 20, max 100) / `cursor` / `action` / `result`(success|failure)

**Response 200 (CursorPage):** items 各含 `id/action/resource/result/user_id/details/created_at` + `next_cursor`

#### GET /tenant-admins/tenants — 可用租户列表

**Query:** 无（不分页）

**Response 200:**
```json
{
  "items": [
    {
      "id": "uuid",
      "name": "acme",
      "display_name": "Acme Inc.",
      "status": "active"
    },
    {
      "id": "uuid",
      "name": "globex",
      "display_name": "Globex Corp.",
      "status": "frozen"
    }
  ]
}
```
> 返回 `status <> 'disabled'` 的租户列表，按 `created_at DESC` 排序，不分页。用于邀请管理员时选择目标租户。网关通过 gRPC 转发至 tenant-service `ListAvailableTenants` RPC。

#### GET /tenants/{tenantId}/roles — 可变角色列表

**Query:** 无（不分页）

**Response 200:**
```json
{
  "items": [
    { "id": "uuid", "name": "tenant-admin", "tenant_id": null, "permissions": [] },
    { "id": "uuid", "name": "user", "tenant_id": "uuid", "permissions": [{"resource":"instances","actions":["read"],"scope":"tenant"}] },
    { "id": "uuid", "name": "auditor", "tenant_id": null, "permissions": [] }
  ]
}
```
> 通过 Core SDK `ListAssignableRoles` 查询 `roles` 表 `WHERE name NOT LIKE 'platform-%' AND (tenant_id IS NULL OR tenant_id = $tenantId)`，不分页。用于修改管理员角色时选择目标角色。`tenant_id` 为 null 表示平台内置角色；`permissions` 为 roles.permissions JSONB 原样返回。

### 4.3 Error Responses

见 §6.1 错误分类总表。

### 4.4 Breaking Changes

无破坏性变更。所有端点为新增，须在首个实现 Issue 回填 `services/v1.yaml`。

---

## 5. Business Logic

### 5.1 Core Algorithms

#### 5.1.1 Invite（邀请，US-001 / FR-1）
```
AuthZ: admin/ops；幂等（body idempotency_key 必填）
校验:
  - email 格式 / username 长度
  - MatchUser(tenantId, email, username) → 无匹配 → 404 TENANT_ADMIN_NOT_FOUND（不新建用户）
  - IsAlreadyAdmin(tenantId, userId) → 是 → 409 TENANT_ADMIN_ALREADY_ADMIN
  - HasPendingInvitation(tenantId, userId) → 存在 inviting → 409 TENANT_INVITATION_PENDING
执行:
  token = crypto_random(32)
  INSERT tenant_admin_invitation(tenant_id, user_id, token_hash=SHA-256(token), status='inviting', expire_at=now()+72h)
  -- 不改 users.status / 不绑定角色
  INSERT audit_logs(action='tenant_admin.invite', details={target_id, token_hash})
  -- 触发通知渠道拼接邀请链接（占位）
返回 { id, token, expire_at, message }
```

#### 5.1.2 Resend（重发，US-002 / FR-1a）
```
AuthZ: admin/ops；幂等
  inv = GetLatestInvitation(tenantId, userId)
  - 无记录 → 404 TENANT_ADMIN_INVITATION_NOT_FOUND
  - inv.status ∈ {accepted, rejected}（终态）→ 409 TENANT_INVITATION_SETTLED
  - 仅 status ∈ {inviting, expired} 允许重发
  token = crypto_random(32)
  UPDATE tenant_admin_invitation SET token_hash=SHA-256(token), expire_at=now()+72h,
    status='inviting', accepted_at=NULL, rejected_at=NULL WHERE id=inv.id
  审计 'tenant_admin.resend_invitation'
返回 { id, token, expire_at, message }
```

#### 5.1.3 ListAllTenantAdmins（跨租户列表，US-003 / FR-2）
```
AuthZ: admin/ops/readonly；只读
  1. TenantAdminSvcClient.ListTenantAdmins → Core SDK → Core DB JOIN users+user_roles+roles+tenants（WHERE is_deleted=FALSE）
  2. TenantAdminStore.ListInvitationFlags → 本地 SQL → tenant_admin_invitation（批量拉取 is_inviting/is_expired 标记）
  3. Service 内存合并：Core 列表 + 本地邀请标记 → 组装 AdminWithTenant[]
  **仅返回 role='tenant-admin' 或 is_inviting=true 或 invitation.status='expired' 的用户**
  可选过滤 tenant_id / status / is_inviting / is_expired / search(username|email|display_name ILIKE) / role / source
  **status / is_inviting / is_expired 三选一互斥**（同时传 ≥2 个 → 400 VALIDATION_FAILED）
  is_inviting / is_expired 仅支持 true（传 false → 400 VALIDATION_FAILED）
  status 仅接受 active / disabled（其他值 → 400 VALIDATION_FAILED）
  source 推断: username 'oidc:' 前缀 → third_party；'local:' → local
  游标分页(limit+cursor，limit max 100)，按 created_at DESC；不写审计
```

#### 5.1.5 ChangeRole（改角色，US-005）
```
AuthZ: admin/ops；幂等
  role_id 须为可分配角色（非 platform-*、非 tenant-admin，tenant_id 为空或等于路径 tenantId）
  → TenantAdminSvcClient.ChangeRole(tenantId, userId, role_id)（Core SDK，upsert user_roles）
  审计 'tenant_admin.change_role'，details={tenant_id, target_id, old_role, old_role_id, new_role, new_role_id}
```

#### 5.1.6 Disable / Enable / Delete（US-008）
```
Disable:
  → TenantAdminSvcClient.SetStatus(tenantId, userId, 'disabled')
    → Core DB 层校验不可重复：status 未变化 → 409 USER_STATE_INVALID "admin already disabled"
  审计 'tenant_admin.disable'
Enable:
  → TenantAdminSvcClient.SetStatus(tenantId, userId, 'active')
    → Core DB 层校验不可重复：status 未变化 → 409 USER_STATE_INVALID "admin already active"
  审计 'tenant_admin.enable'
Delete:
  → TenantAdminSvcClient.SoftDelete(tenantId, userId)（Core SDK，UPDATE users SET is_deleted=TRUE, deleted_at=now()；不改 status）
  审计 'tenant_admin.delete'（不幂等）
```

#### 5.1.7 ResetPassword（重置，US-007）
```
AuthZ: admin/ops；幂等
  target 已软删除 → 404 TENANT_ADMIN_NOT_FOUND（禁用态用户允许重置密码）
  bcrypt(new_password, 12) == 旧 hash → 422 PASSWORD_SAME_AS_OLD
  复杂度不满足 → 400 VALIDATION_FAILED
  → TenantAdminSvcClient.ResetPassword(tenantId, userId, new_password)（Core SDK，UPDATE users SET password_hash；明文不落日志/审计/响应）
  审计 'tenant_admin.reset_password'
```

#### 5.1.8 GetRolePermissions（查询权限，US-009）
```
AuthZ: admin/ops/readonly；只读
  → TenantAdminSvcClient.GetRolePermissions(tenantId, userId)（Core SDK，JOIN user_roles + roles 查角色及 permissions JSONB）
  仅返回租户成员（tenant_id 非空）；平台账户不可查询
  返回 { user_id, tenant_id, role, role_id, permissions[] }（resource/action/scope 数组）
  role_id 为当前角色绑定 UUID（无绑定时省略）
```

#### 5.1.9 GetAdminDetail（详情，US-004）
```
AuthZ: admin/ops/readonly；只读
  按 tenantId+userId 返回用户全字段（不含 password_hash）+ is_inviting + tenant 对象
  source 推断：'oidc:' → third_party；'local:' → local
```

#### 5.1.10 ListAuditLogs（操作历史，US-010）
```
AuthZ: admin/ops/readonly；只读
  查询 audit_logs WHERE tenant_id=tenantId AND details->>'target_id'=userId；action/result 过滤；游标分页(limit max 100)
  无审计（只读查询）
```

#### 5.1.11 ListAvailableTenants（可用租户列表，US-011 / FR-7）
```
AuthZ: admin/ops/readonly；只读
  → TenantSvcClient.ListAvailableTenants()（Core SDK → Core /api/v1/admin/tenant-admins/available-tenants）
  返回 items [{ id, name, display_name, status }]
  无审计（只读查询）
```

#### 5.1.12 ListTenantRoles（可变角色列表，US-012 / FR-8）
```
AuthZ: admin/ops/readonly；只读
  → TenantAdminSvcClient.ListAssignableRoles(tenantId)（Core SDK → Core /api/v1/admin/tenants/{id}/roles）
  查询 roles WHERE name NOT LIKE 'platform-%' AND (tenant_id IS NULL OR tenant_id = $tenantId)
  返回 items [{ id, name, tenant_id, permissions }]
  无审计（只读查询）
```

### 5.2 Validation Rules

| Field | Rule |
|-------|------|
| email | RFC 5322（invite） |
| username | 1-64 字符，不含 `:` |
| role_id (PUT) | 目标角色 UUID；须为可分配角色（非 platform-*、非 tenant-admin，tenant_id 为空或等于路径 tenantId） |
| new_password | 8-64 字符、四类至少三类、与旧密码不同 |

### 5.3 State Machine

**邀请状态机（tenant_admin_invitation.status）：**
```
                接受(绑定 admin 角色+激活)
  inviting ──────────────────────────▶ accepted
      │                                   │ 终态
      │ 拒绝（不绑定角色）                │
      ├──────────────────────────────▶ rejected  (终态)
      │ 过期（now() > expire_at）
      └──────────────────────────────▶ expired   (终态)
```
- 仅 `inviting` 且 `expire_at > now()` 可被接受；接受/拒绝后不可变更。
- 重发仅允许 `inviting` / `expired` → 回归 `inviting`（清空 accepted_at/rejected_at）。
- `users.status` 状态机维持 `active ⇄ disabled`（不受邀请影响）。

### 5.4 Edge Cases

| Case | Handling |
|------|----------|
| 邀请时用户已在本租户为 admin | 409 TENANT_ADMIN_ALREADY_ADMIN |
| 邀请时已有 inviting 邀请 | 409 TENANT_INVITATION_PENDING（引导重发） |
| 重发时最新邀请为终态 | 409 TENANT_INVITATION_SETTLED |
| 跨租户列表普通成员 user | 仅该用户 being invited 或邀请已过期时返回（is_inviting=true/false，role 仍 user） |
| 邀请中/已过期用户角色 | is_inviting 仅标记，不改变其 role/status（可为 user） |
| 平台账号查询权限 | `GET .../role` 不返回平台权限（tenant_id=null） |
| 删除已不存在用户 | 幂等由网关处理；DELETE 不幂等，重复删除按软删除条件过滤 |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP | Condition | User Message (zh-CN) |
|------------|------|-----------|---------------------|
| TENANT_ADMIN_NOT_FOUND | 404 | 邀请无匹配用户 / 重置密码遇已软删除用户 / 管理员不存在 | 管理员不存在或已软删除 |
| TENANT_ADMIN_ALREADY_ADMIN | 409 | 邀请时该用户已是本租户 admin | 该用户已是此租户管理员 |
| TENANT_INVITATION_PENDING | 409 | 已有 inviting 邀请 | 该用户已有待接受的邀请，请改用重发邀请 |
| TENANT_ADMIN_INVITATION_NOT_FOUND | 404 | 重发时无匹配邀请记录 | 未找到可重发的邀请记录 |
| TENANT_INVITATION_SETTLED | 409 | 最新邀请已 accepted/rejected | 该邀请已处理（接受/拒绝），不可重发 |
| ROLE_CHANGE_INVALID | 422 | role_id 不可分配 | role_id 对应角色不可分配（platform-*、tenant-admin、或 tenant_id 不匹配） |
| PASSWORD_SAME_AS_OLD | 422 | 新密码与旧密码相同 | 新密码不能与旧密码相同 |
| USER_STATE_INVALID | 409 | 重复 disable/enable（状态未变化） | 管理员已处于该状态 |
| VALIDATION_FAILED | 400 | 参数校验失败 | 校验失败：{message} |
| FORBIDDEN | 403 | 角色不匹配（platform-admin/ops/readonly RBAC 校验失败） | 无平台运营权限 |
| TENANT_NOT_FOUND | 404 | 租户不存在 | 租户不存在 |

> 幂等键校验（`IDEMPOTENCY_KEY_INVALID` 400 / `IDEMPOTENCY_CONFLICT` 409）由网关中间件处理，不在 service 错误码表中。

### 6.2 Retry Strategy

| Operation | Retry | Max | Backoff |
|-----------|-------|-----|---------|
| 邀请/重发 token 生成 | 否（幂等键去重） | — | — |
| 通知渠道发送 | 是 | 3 次 | 指数退避 |

### 6.3 Failure Modes

| Failure | Impact | Handling |
|---------|--------|----------|
| PostgreSQL 连接失败 | 全部操作失败 | 500，前端展示网络错误 |
| 通知渠道不可用 | 邀请链接未发送 | 记录审计 + 异步重试（3 次）；邀请记录已成功 |
| 网关 gRPC 到 tenant-service 不可用 | 全部写/读失败 | 502/503，前端展示服务不可用 |

---

## 7. Security

### 7.1 Authentication & Authorization

| Endpoint | platform-admin | platform-ops | platform-readonly |
|----------|---------------|-------------|-------------------|
| POST .../admins/invite | ✅ | ✅ | ❌ |
| POST .../invitation/resend | ✅ | ✅ | ❌ |
| GET /tenant-admins | ✅ | ✅ | ✅ |
| GET .../admins/{userId} | ✅ | ✅ | ✅ |
| PUT .../role | ✅ | ✅ | ❌ |
| GET .../role | ✅ | ✅ | ❌ |
| POST .../reset-password | ✅ | ✅ | ❌ |
| POST .../disable \| enable | ✅ | ✅ | ❌ |
| DELETE .../admins/{userId} | ✅ | ✅ | ❌ |
| GET .../audit-logs | ✅ | ✅ | ✅ |
| GET /tenant-admins/tenants | ✅ | ✅ | ✅ |
| GET /tenants/{tenantId}/roles | ✅ | ✅ | ✅ |

> AuthZ 由 ani-gateway 网关层校验（鉴权 + RBAC），通过 gRPC metadata 透传角色。读端点（列表/详情/历史）含 platform-readonly；写端点和 GET role 不含 readonly。
>
> **⚠️ 待实现**：当前网关代码尚未落地 RBAC 角色中间件（无 platform-admin/ops/readonly 角色校验），上述矩阵为设计目标。落地后需在 `tenant_admin_resources.go` 注册路由时添加角色校验中间件。

### 7.2 Input Validation

- email/username/password 服务端强校验（见 §5.2）
- role 白名单校验
- `idempotency_key` 格式与去重由网关处理

### 7.3 Data Protection

- password_hash 用 bcrypt cost=12；明文密码不落日志/审计/响应
- invitation token 仅存 SHA-256 hash；原始 token 仅一次性返回
- 审计日志记录所有写操作（`tenant_admin.*`），含 user_id / request_id / ip_address / user_agent

---

## 8. Performance

### 8.1 Expected Load

| Metric | Estimate |
|--------|----------|
| 每租户管理员数 | < 50 |
| 跨租户列表行数 | < 1000 |
| 列表 QPS | < 10 |
| 邀请/写操作频率 | 低 |

### 8.2 Optimization Strategy

| Strategy | Application |
|----------|-------------|
| JOIN 单次查询 | ListAllTenantAdmins 单次 JOIN 全部关联表 + is_inviting 子查询，避免 N+1 |
| 游标分页 | limit+cursor，按 created_at DESC |
| is_inviting 高效判断 | correlated EXISTS（有索引 uk_tenant_admin_invitation_pending） |

### 8.3 Database Considerations

| Index | Purpose |
|-------|---------|
| uk_tenant_admin_invitation_token_hash (UNIQUE) | token 并发唯一 |
| uk_tenant_admin_invitation_pending | 按租户+用户查 pending 邀请 + 并发竞态防护 |
| users.is_deleted / user_roles.role_id | 列表 JOIN 过滤（通过 Core SDK TenantAdminSvcClient 透明传递） |
| tenants 表 | 租户过滤 + tenant 对象 JOIN |

---

## 9. Testing Strategy

### 9.1 Unit Tests

| Test | Scope | Description |
|------|-------|-------------|
| TestTenantAdminService_Invite | service | 匹配成功、无匹配 404、already admin 409、pending 409、token/expire_at、审计 |
| TestTenantAdminService_Resend | service | inviting/expired 重发成功、终态 409、无记录 404 |
| TestTenantAdminService_ChangeRole | service | 合法 role 成功、非法 role 422 |
| TestTenantAdminService_DisableEnableDelete | service | 禁用/启用/软删除、重复禁用/启用 409 USER_STATE_INVALID |
| TestTenantAdminService_ResetPassword | service | 成功、同旧密码 422、复杂度 400、已软删除 404、禁用态允许 |
| TestTenantAdminService_GetRolePermissions | service | 租户成员权限、平台账号不可查 |

### 9.2 Integration Tests

| Test | Description |
|------|-------------|
| TestHandler_InviteFlow | POST invite → GET 列表验证 is_inviting=true + token 一次性 |
| TestHandler_ResendFlow | POST resend → 验证新 token + expire_at + settling |
| TestHandler_Detail | GET detail 验证全字段（含 is_inviting/created_at） |

### 9.3 Edge Case Tests

| Test | Description |
|------|-------------|
| TestTenantAdminService_ListAll/all_admins_and_inviting_expired | 跨租户列表仅含 admin/邀请中/已过期，普通 user 不出现 |
| TestTenantAdminService_ListAll/inviting_keeps_role | 邀请中 user 返回且 role 仍为 user、is_inviting=true |
| TestTenantAdminService_ListAll/expired_keeps_role | 已过期 user 返回且 role 仍为 user、is_expired=true |
| TestTenantAdminService_ListAll/filter_mutual_exclusion | status/is_inviting/is_expired 三选一互斥校验 |
| TestTenantAdminService_ListAll/role_filter | role 过滤参数校验 |
| TestTenantAdminService_ListAll/source_filter | source 过滤参数校验 |
| TestTenantAdminService_ResetPassword/disabled_user_allowed | 禁用态用户允许重置密码 |
| TestTenantAdminService_DisableEnableDelete/already_disabled | 重复禁用 → 409 USER_STATE_INVALID |
| TestTenantAdminService_DisableEnableDelete/already_active | 重复启用 → 409 USER_STATE_INVALID |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type |
|-------|------|------|
| US-001 邀请 | TestTenantAdminService_Invite + TestHandler_InviteFlow | unit + integration |
| US-002 重发 | TestTenantAdminService_Resend + TestHandler_ResendFlow | unit + integration |
| US-003 跨租户列表 | TestTenantAdminService_ListAll | unit |
| US-004 详情 | TestHandler_Detail | integration |
| US-005 改权限 | TestTenantAdminService_ChangeRole | unit |
| US-007 重置密码 | TestTenantAdminService_ResetPassword | unit |
| US-008 禁用/启用/删除 | TestTenantAdminService_DisableEnableDelete | unit |
| US-009 权限查询 | TestTenantAdminService_GetRolePermissions | unit |
| US-010 操作历史 | TestHandler_InviteFlow（审计记录断言） | integration |
| US-011 可用租户列表 | TestHandler_ListAvailableTenants | integration |
| US-012 可变角色列表 | TestHandler_ListTenantRoles | integration |
| FR-1/1a/1b | TestTenantAdminService_Invite/Resend | unit |
| FR-2 | TestTenantAdminService_ListAll | unit |
| FR-3 | TestTenantAdminService_ResetPassword | unit |
| FR-5 | TestTenantAdminService_GetRolePermissions | unit |
| FR-7 | TestHandler_ListAvailableTenants | integration |
| FR-8 | TestHandler_ListTenantRoles | integration |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | Description | Depends On |
|-------|-------------|------------|
| 1 | Services OpenAPI 契约：`services/v1.yaml` 补齐路径/schema/错误码 + gRPC proto + buf 生成 | — |
| 2 | 数据库迁移：`tenant_admin_invitation` 建表 | — |
| 3 | tenant-service 结构：ports 接口 + TenantAdminStore + 网关 gRPC client/router | 1, 2 |
| 4 | TenantAdminService 全部业务逻辑 + 审计 | 3 |
| 5 | 前端 API 封装 `tenant-admins.ts` + `_authenticated.tsx` 菜单 | 1 |
| 6 | 前端页面组件：列表页 + 邀请 + 详情 Drawer + 改角色 + 重置 + 行操作 | 5 |
| 7 | 集成测试 + E2E 验证 | 4, 6 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 Services OpenAPI 契约 | 2.1, 4.1, 4.2, 4.3 | high | — |
| #2 数据库迁移 | 3.1, 3.4 | high | — |
| #3 tenant-service 骨架 + Store | 2.2, 2.4, 3.2 | high | #1, #2 |
| #4 TenantAdminService 业务逻辑 | 5.1, 5.2, 5.3 | high | #3 |
| #5 网关路由 | 2.3, 7.1 | high | #3 |
| #6 前端 API + 菜单 | UX §4, §5 | high | #1 |
| #7 前端页面组件 | UX §4, §5, §6 | high | #6 |
| #8 集成/E2E 测试 | 9.2, 9.3 | medium | #4, #7 |

### 10.3 Incremental Delivery

1. Phase 1-2 可并行（契约 + 迁移）
2. Phase 6-7（前端）依赖契约；可先用 mock 先行
3. 写操作（邀请/重置）依赖后端 Service 完成

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- 邀请通知发送通道（SMTP/短信）具体实现待定，本模块仅触发占位 + 异步重试。
- PRD US-017（租户内管理员列表）暂不实现——`GET /tenants/{tenantId}/admins` 端点不在本模块交付。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| `services/v1.yaml` 契约未冻结 | 前端/网关契约漂移 | 首个 Issue 回填契约并校验，前端经 openapi-fetch typed paths 消费 |
| 网关已存在 `tenant_resources.go` | 路由命名冲突 | 新建独立 `tenant_admin_resources.go`，复用既有 router 注册 |
| tenant-service 尚未创建基础 | 需先有 gRPC server 骨架 | 复用 quota-policy 已创设的 tenant-service 骨架 |

### 11.3 Assumptions

- tenant-service 已按 quota-policy SPEC 建立 gRPC server 骨架（`main.go` + `bootstrap.RunGRPC` + `Register(*grpc.Server)`）。
- ani-gateway 通过 gRPC client（`TENANT_SERVICE_ADDR`，缺省 `127.0.0.1:9105`）转发 `/api/v1/svc/tenants/{tenantId}/admins/*`、`/tenant-admins`。
- 审计日志复用现有 `audit_logs` 分区表；action 命名 `tenant_admin.invite / resend_invitation / change_role / reset_password / disable / enable / delete`。
- 前端使用 TDesign React + TanStack Router + TanStack Query；`idempotency_key` 由 `crypto.randomUUID()` 生成对用户不可见；DELETE 不携带。
- 跨租户列表仅返回 admin/邀请中；`is_inviting` 前后端契约字段；前端将「邀请中」作为状态同级值展示/筛选。
- 详情返回全字段 + is_inviting + created_at/updated_at + tenant 对象，不含 password_hash、无顶层 tenant_id 冗余。
