# SPEC: 平台运营账号管理 (new)

> Technical specification derived from:
> - PRD: [prd-new-boss-platform-admin.md](../../prd/boss/settings/prd-new-boss-platform-admin.md)
> - UX: [ux-new-boss-platform-admin.md](../../ux/boss/settings/ux-new-boss-platform-admin.md)
> Generated: 2026-08-18 | Target branch: `main`
> Code scope: `repo/api/openapi/v1.yaml` + `repo/pkg/` + `repo/services/ani-gateway/` + `repo/sdks/core/` + `repo/api/openapi/services/v1.yaml` + `repo/services/platform-settings-service/` + `repo/frontends/boss/src/`

> **分层说明（用户指示）：** `users` / `roles` / `user_roles` 表的操作（CRUD、角色绑定、密码哈希、状态变更、软删除、最后管理员保护、权限矩阵查询）**下沉到 Core 层**实现（端口 + 适配器 + 网关 handler + Core OpenAPI + SDK）。Services 层**新建独立微服务 `platform-settings-service`**（平台设置通用服务，本批承载平台运营账号模块），通过 **Core SDK** 调用，不直接 SQL 操作 Core 表。前端经 ani-gateway 访问 Services `/api/v1/svc/platform-admins/*`。

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 定义平台运营账号（platform-admin / platform-ops / platform-readonly）全生命周期管理的技术实现：创建（指定角色 + 初始密码）、列表/详情查询、可变更角色与权限矩阵查询、修改角色、重置密码、禁用/启用、软删除，以及最后管理员保护。完全复用 `users` 表（`tenant_id IS NULL`）+ `roles` + `user_roles`，不新建表。

**三层分层架构：**
- **Core 层**：新增 `PlatformUserAdminService`（端口 + PostgreSQL 适配器）+ ani-gateway handler + Core OpenAPI `/api/v1/admin/platform-users/*` 端点 + Core SDK 自动生成。负责 `users`/`user_roles`/`roles` 表的直接操作（platform bypass RLS）、bcrypt 哈希、最后管理员保护原子校验、权限矩阵查询。
- **Services 层**：**新建独立微服务 `platform-settings-service`**（平台设置通用服务；Go module `github.com/kubercloud/ani/services/platform-settings-service`，gRPC 端口 9106 / health 9206，环境变量 `PLATFORM_SETTINGS_SERVICE_ADDR`）。本批承载平台运营账号模块：新增 `PlatformAdminService`（gRPC server），通过 **Core SDK** 调 Core `/api/v1/admin/platform-users/*`，叠加审计日志写入（`audit_logs`，`tenant_id IS NULL`）、角色权限矩阵静态映射、操作历史查询。对外经 ani-gateway 暴露 `/api/v1/svc/platform-admins/*`。后续平台设置其他模块（安装部署、升级备份等）可复用该服务。
- **前端层**：BOSS 新增平台运营账号列表页 + 3 步创建向导（独立路由）+ 详情独立路由页（概览/权限/操作记录 Tabs）+ 修改角色/重置密码 Dialog + 禁用启用删除行操作。

**不覆盖：**
- 平台账密登录（已由 `spec-core-login.md` 实现 `POST /auth/platform/password/login`）
- 邮件邀请注册（PRD Non-Goal）
- 修改用户名（PRD Non-Goal）
- 平台账号 SSO/OIDC 集成（PRD Non-Goal）
- 租户管理员管理（见 `spec-new-boss-tenant-admin.md`）

### 1.2 PRD Reference

- Source: [prd-new-boss-platform-admin.md](../../prd/boss/settings/prd-new-boss-platform-admin.md)
- UX source: [ux-new-boss-platform-admin.md](../../ux/boss/settings/ux-new-boss-platform-admin.md)
- User Stories covered: US-001 ~ US-008
- Functional Requirements covered: FR-1 ~ FR-8

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| users/roles 操作归属 | **Core 层**（端口+适配器+网关 handler+OpenAPI+SDK） | 用户指示；Core 拥有 users/roles/user_roles 表的运行时操作权；Services 不得直接 SQL 操作 Core 表（边界校验脚本强制） |
| Services 承载服务 | **新建 `platform-settings-service`**（平台设置通用服务） | 用户指示；平台设置（settings）各模块统一由独立服务承载，不复用 tenant-service（租户域）；本批首个落地模块为平台运营账号 |
| Services 调 Core 方式 | Core SDK（anisdk.Client HTTP REST） | 对齐 Services 现有 client 模式；`make gen-core-sdk` 自动生成 |
| Core OpenAPI 路径前缀 | `/api/v1/admin/platform-users/*` | 对齐网关 `scopeAllowedForPath` 白名单（`/admin/*` 要求 scope=platform）；与 Services `/svc/platform-admins/*` 命名区分 |
| 平台账号存储 | 复用 `users` 表 `tenant_id IS NULL` + `roles`(tenant_id IS NULL) + `user_roles` | 不新建表；已有迁移 `20260707_014_platform_users.sql` |
| 密码哈希 | bcrypt cost=12（Core 层 `GenerateFromPassword`） | 对齐 `spec-core-login.md` §6；哈希在 Core 层生成后写库 |
| 最后管理员保护 | Core 层原子校验（事务内 `SELECT COUNT` + `UPDATE`/`DELETE`） | 避免 TOCTOU；Core 层拥有数据一致性强保证 |
| 权限矩阵 | 静态映射（platform-settings-service 内置），不查 Core DB | 4 维度权限为平台内置角色固有属性，不动态查询；`GET /roles` 端点返回静态映射 |
| 幂等性 | POST create / PUT role / POST reset-password / POST disable / POST enable / DELETE 均支持 `idempotency_key`（body） | DELETE 虽不幂等但 PRD §6 要求携带；Core 侧网关统一去重 |
| 审计日志 | platform-settings-service 写入 `audit_logs`（`tenant_id IS NULL`，`action='platform_admin.*'`） | 复用现有 `audit_logs` 分区表；action 命名 `platform_admin.create/change_role/reset_password/disable/enable/delete` |
| 操作历史查询 | platform-settings-service 直查 `audit_logs`（`user_id=$userId AND tenant_id IS NULL`） | 审计表为 Services 可直接访问（RLS platform_bypass）；不调 Core |
| 创建表单 | 3 步 Wizard（独立路由 `/new`） | UX 确认决策 |
| 详情呈现 | 独立路由页 `/boss/settings/platform-admins/$userId` + Tabs | UX 确认决策 |
| 邀请流程 | **不做**（直接创建设密码） | UX 确认 + PRD Non-Goal |

---

## 2. Architecture

### 2.1 System Context

```text
┌──────────────────┐   REST /api/v1/svc/platform-admins/*
│   BOSS Frontend  │──────────────────────▶┌─────────────────────┐    gRPC    ┌──────────────────────┐
│  (boss/src/)     │  JWT Bearer(platform) │   ani-gateway       │──────────▶│ platform-settings-   │
│                  │  + 幂等/审计          │  (已有)              │           │ service (NEW)        │
│ 列表/创建向导/   │                       │ 路由+鉴权+限流       │           │ :9106               │
│ 详情/改角色/     │                       │ 幂等/审计(RBAC)      │           │ PlatformAdminService│
│ 重置/禁用启用删除│                       │ gRPC 转发            │           │ (业务编排+审计)      │
└──────────────────┘                       └─────────────────────┘           └────┬─────────┬───────┘
                                                                              │         │ Core SDK
                                                                              │         │ (HTTP REST)
                                                                   ┌──────────▼─┐  ┌────▼───────────────┐
                                                                   │  ANI Core   │  │ platform-settings-  │
                                                                   │ /api/v1/    │  │ service             │
                                                                   │ admin/      │  │ CorePlatformUser-   │
                                                                   │ platform-   │  │ Client              │
                                                                   │ users/*     │  └──────────────┬─────┘
                                                                   │ (NEW)       │    ┌─────────────▼────┐
                                                                   └─────────────┘    │  PostgreSQL       │
                                                                        │ SQL        │ audit_logs        │
                                                                   ┌────▼──────────┐  │ (tenant_id NULL)  │
                                                                   │  PostgreSQL    │  └──────────────────┘
                                                                   │ users (tid    │
                                                                   │  IS NULL)     │
                                                                   │ roles (tid    │
                                                                   │  IS NULL)     │
                                                                   │ user_roles    │
                                                                   └───────────────┘
```

**数据流向：**
- 前端 → ani-gateway（REST `/svc/platform-admins/*`，JWT scope=platform 校验 + RBAC）→ platform-settings-service（gRPC，`PLATFORM_SETTINGS_SERVICE_ADDR` 缺省 `127.0.0.1:9106`）→ Core SDK（HTTP `/api/v1/admin/platform-users/*`）→ Core DB（platform bypass RLS）
- 审计日志：platform-settings-service 直接写 `audit_logs`（`tenant_id IS NULL`，RLS platform_bypass）
- 操作历史：platform-settings-service 直接查 `audit_logs`（不调 Core）
- 权限矩阵：platform-settings-service 内置静态映射（不调 Core）

### 2.2 Frozen Facts Table

| 项目 | 状态 |
|------|------|
| **Core Frozen Paths** | `repo/api/openapi/v1.yaml` 中 **尚无** `/admin/platform-users/*` → **待补**（Core 层首个 Issue 补齐，见 §4.1 operationId） |
| **Services Frozen Paths** | `repo/api/openapi/services/v1.yaml` 中 **尚无** `/platform-admins/*` → **待补**（Services 层首个 Issue 补齐） |
| Frozen Schemas | `PlatformUser`、`PlatformAdminListItem`、`PlatformRole`、`CursorPage` → **待补** |
| Frozen Response / Error codes | §6.1 错误码表（`LAST_PLATFORM_ADMIN` 等）→ **待补**到 Core `v1.yaml` + Services `v1.yaml` |
| Non-Frozen Capabilities | 平台账号 SSO/OIDC 集成（Non-Goal，不实现） |
| Known Risky Assumptions | Core `/admin/platform-users/*` 端点尚未存在；Core SDK 需 `make gen-core-sdk` 重新生成；platform-settings-service 为全新微服务（骨架从零建立，gRPC 端口 9106 / health 9206 / `PLATFORM_SETTINGS_SERVICE_ADDR`） |

### 2.3 Component Design

| Component | Responsibility | Layer | Location |
|-----------|---------------|-------|----------|
| `PlatformUserAdminStore`（端口） | 平台用户 CRUD/角色/密码/状态/软删除/计数接口 | Core | `repo/pkg/ports/platform_user_admin.go` [NEW] |
| `PostgresPlatformUserAdminStore`（适配器） | PostgreSQL 实现，platform bypass RLS，事务内原子最后管理员保护 | Core | `repo/pkg/adapters/postgres/platform_user_admin.go` [NEW] |
| `PlatformUserAPI`（网关 handler） | Core HTTP 入站：请求解析+校验+调 Store+响应组装+错误码映射+幂等 | Core | `repo/services/ani-gateway/internal/router/platform_user_resources.go` [NEW] |
| Core OpenAPI 契约 | Core `/api/v1/admin/platform-users/*` 路径/schema/错误码 | Core | `repo/api/openapi/v1.yaml` [MODIFY] |
| Core SDK 自动生成 | anisdk.Client 新增 PlatformUser 方法 | Core | `repo/sdks/core/go/anisdk/` [REGEN `make gen-core-sdk`] |
| `PlatformAdminService`（gRPC） | 业务编排：调 Core SDK + 审计写入 + 权限矩阵静态映射 + 操作历史查询 | Services | `repo/services/platform-settings-service/internal/service/platform_admin_service.go` [NEW] |
| `PlatformAdminStore`（端口） | 审计日志写入/查询接口（仅 audit_logs，**不操作 users/roles**） | Services | `repo/services/platform-settings-service/internal/repo/ports/platform_admin_store.go` [NEW] |
| `PostgresPlatformAdminStore` | PostgreSQL 适配器（仅 audit_logs CRUD） | Services | `repo/services/platform-settings-service/internal/repo/adapters/postgres/platform_admin_store.go` [NEW] |
| `CorePlatformUserClient` | Core SDK 封装（调 `/api/v1/admin/platform-users/*`） | Services | `repo/services/platform-settings-service/internal/repo/adapters/core/platform_user_client.go` [NEW] |
| Services OpenAPI 契约 | Services `/api/v1/svc/platform-admins/*` 路径/schema/错误码 | Services | `repo/api/openapi/services/v1.yaml` [MODIFY] |
| gRPC proto | platform-settings-service PlatformAdminService RPC 定义 | Services | `repo/api/proto/platform_settings/v1/platform_admin_service.proto` [NEW] |
| `PlatformAdminAPI`（网关转发） | Services HTTP 入站转发到 platform-settings-service gRPC | Services | `repo/services/ani-gateway/internal/router/platform_admin_resources.go` [NEW] |
| 前端 API 封装 | openapi-fetch typed paths | Frontend | `repo/frontends/boss/src/api/platform-admins.ts` [NEW] |
| 前端页面组件 | 列表/创建向导/详情/Dialog | Frontend | `repo/frontends/boss/src/` [NEW] |

### 2.4 Module Interactions

```text
创建平台账号:
  Frontend → ani-gateway → POST /api/v1/svc/platform-admins
    → PlatformAdminAPI(网关)[幂等/审计/RBAC] → (gRPC) PlatformAdminService.Create
      → CorePlatformUserClient.Create(email, username, display_name, role, password)  // Core SDK → POST /api/v1/admin/platform-users
        → Core PlatformUserAPI → PlatformUserAdminStore.Create                        // bcrypt hash + INSERT users + INSERT user_roles
      → PlatformAdminStore.CreateAudit('platform_admin.create')                       // audit_logs
    → Response { id, message }

列表:
  Frontend → ani-gateway → GET /api/v1/svc/platform-admins?limit&cursor&role&status&source&search
    → PlatformAdminService.List
      → CorePlatformUserClient.List(filters)                                          // Core SDK → GET /api/v1/admin/platform-users
    → Response { items[], next_cursor }

详情:
  Frontend → ani-gateway → GET /api/v1/svc/platform-admins/{userId}
    → PlatformAdminService.GetDetail
      → CorePlatformUserClient.GetUser(userId)                                        // Core SDK

可变更角色与权限矩阵:
  Frontend → ani-gateway → GET /api/v1/svc/platform-admins/roles
    → PlatformAdminService.ListRoles
      → 返回静态映射（不调 Core，不写审计）                                            // 内置 platform-admin/ops/readonly 4 维权限

修改角色:
  Frontend → ani-gateway → PUT /api/v1/svc/platform-admins/{userId}/role
    → PlatformAdminService.ChangeRole
      → CorePlatformUserClient.ChangeRole(userId, new_role)                           // Core SDK → PUT /admin/platform-users/{id}/role
        → Core PlatformUserAdminStore.ChangeRole                                      // 事务内 DELETE 旧 user_roles + INSERT 新
      → PlatformAdminStore.CreateAudit('platform_admin.change_role')

重置密码:
  Frontend → ani-gateway → POST .../reset-password
    → PlatformAdminService.ResetPassword
      → CorePlatformUserClient.ResetPassword(userId, new_password)                     // Core SDK → bcrypt + UPDATE
      → PlatformAdminStore.CreateAudit('platform_admin.reset_password')

禁用/启用:
  Frontend → ani-gateway → POST .../disable | /enable
    → PlatformAdminService.Disable / Enable
      → CorePlatformUserClient.SetStatus(userId, 'disabled'/'active')                 // Core SDK → 最后管理员保护 + UPDATE
      → PlatformAdminStore.CreateAudit('platform_admin.disable / enable')

删除:
  Frontend → ani-gateway → DELETE .../platform-admins/{userId}
    → PlatformAdminService.Delete
      → CorePlatformUserClient.SoftDelete(userId)                                     // Core SDK → 最后管理员保护 + UPDATE is_deleted/deleted_at/status
      → PlatformAdminStore.CreateAudit('platform_admin.delete')

操作历史:
  Frontend → ani-gateway → GET .../audit-logs
    → PlatformAdminService.ListAuditLogs
      → PlatformAdminStore.ListAuditLogs(userId)                                      // 直查 audit_logs WHERE user_id=$1 AND tenant_id IS NULL
```

### 2.5 File Structure

```
repo/api/openapi/
├── v1.yaml                              [MODIFY — 新增 /admin/platform-users/* 路径/schema/错误码]
└── services/
    └── v1.yaml                          [MODIFY — 新增 /svc/platform-admins/* 路径/schema/错误码]

repo/api/proto/platform_settings/v1/
└── platform_admin_service.proto         [NEW — gRPC 接口与数据模型]

repo/pkg/generated/pb/platform_settings/v1/   [NEW — buf 生成 pb.go]

repo/pkg/
├── ports/
│   └── platform_user_admin.go           [NEW — PlatformUserAdminStore 端口接口]
└── adapters/
    └── postgres/
        └── platform_user_admin.go       [NEW — PostgreSQL 适配器]

repo/sdks/core/go/anisdk/                [REGEN — make gen-core-sdk 重新生成]

repo/services/ani-gateway/
├── internal/router/
│   ├── platform_user_resources.go       [NEW — Core handler: /admin/platform-users/*]
│   └── platform_admin_resources.go      [NEW — Services 转发: /svc/platform-admins/* → gRPC]
└── main.go                              [MODIFY — 装配 PlatformUserAdminStore + 路由注册]

repo/services/platform-settings-service/  [NEW — 平台设置通用服务（本批：平台运营账号模块）]
├── main.go                              [NEW — bootstrap.RunGRPC，注册 PlatformAdminService]
├── Dockerfile                            [NEW]
├── go.mod                                [NEW — module github.com/kubercloud/ani/services/platform-settings-service]
├── internal/
│   ├── config/
│   │   └── config.go                    [NEW — GRPC_PORT 9106 / HEALTH_PORT 9206]
│   ├── service/
│   │   └── platform_admin_service.go    [NEW — gRPC PlatformAdminService server + 业务逻辑]
│   └── repo/
│       ├── ports/
│       │   ├── platform_admin_store.go  [NEW — 审计 store 接口]
│       │   └── core_platform_user.go   [NEW — Core SDK client 接口]
│       └── adapters/
│           ├── core/
│           │   └── platform_user_client.go [NEW — Core SDK 封装]
│           └── postgres/
│               └── platform_admin_store.go [NEW — audit_logs 适配器]

repo/frontends/boss/src/
├── api/
│   └── platform-admins.ts               [NEW]
├── routes/_authenticated/
│   └── settings/
│       └── platform-admins/
│           ├── index.tsx                [NEW — 列表页]
│           ├── new.tsx                  [NEW — 3 步创建向导]
│           └── $userId.tsx              [NEW — 详情页]
├── routes/_authenticated.tsx            [MODIFY — 「平台设置」改 SubMenu + 新增子项]
└── components/platform-admins/
    ├── PlatformAdminTable.tsx           [NEW]
    ├── CreatePlatformAdminWizard.tsx    [NEW]
    ├── PlatformAdminDetailPage.tsx      [NEW]
    ├── ChangeRoleDialog.tsx             [NEW]
    └── ResetPasswordDialog.tsx          [NEW]
```

---

## 3. Data Model

### 3.1 Schema Changes

**无新建表**（PRD Non-Goal）。完全复用 `users` + `roles` + `user_roles` + `audit_logs`。

#### 3.1.1 Core 层：users 表列扩展（ALTER）

平台运营账号模块需要 `users` 表支持 `display_name` 与软删除，当前已有迁移 `20260707_014_platform_users.sql` 将 `tenant_id` 改为 NULLABLE 并建立平台唯一索引。若 `display_name` / `is_deleted` / `deleted_at` 列尚未由租户管理员模块添加，则在本批次迁移中补齐：

```sql
-- display_name：平台运营账号展示名（昵称），NULL 表示使用 username
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;

-- is_deleted / deleted_at：软删除标记，Core PlatformUserAdminStore.SoftDelete 使用
-- is_deleted 与 users.status 独立——status 为 active/disabled（业务状态），
-- is_deleted 为软删除标记（DELETE 操作使用），二者不互斥
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
```

#### 3.1.2 Core 层：平台运营角色种子

现有平台内置角色为 `platform-admin` / `tenant-admin` / `user` / `auditor`（见 `20260502_003_permissions_schema.sql`），缺少 `platform-ops` / `platform-readonly`。平台运营账号模块的角色管理基于此三个平台角色。

> **实现修正（issue-003，2026-08-31）：** 下方种子 SQL 以实际迁移 `20260831000100_platform_roles_seed.sql` 为准。早期草稿的 `tenant_ops` / `platform_user` / `audit_export` 维度**不可用**：
> 1. 不在 `20260502000300` 头部有效资源枚举中，且无对应路由授权路径（auth-service V2 `permissionStore.userPermissions` 直查 `roles.permissions`，按 resource 精确匹配——种子即运行时权威授权数据）；
> 2. `"actions":[]` 违反 `roles_permissions_schema` CHECK（要求非空数组），「无权限」须省略条目；
> 3. `ON CONFLICT (tenant_id, name)` 对 NULL tenant_id 不生效，且 `ON CONFLICT (id)` 拦不住同名异 id 行——需 `WHERE NOT EXISTS` + `ON CONFLICT (id) DO UPDATE` 双保险。
> §3.2.3 的 `tenant_ops/resource_pool/platform_user/audit_export` 四维矩阵是 `GET /roles` 端点的**静态展示映射**（Services 内置硬编码，不查 Core DB），与 DB 种子分属两层，二者不矛盾。

```sql
-- platform-ops：租户/资源池全权限，计量只读，不可管理平台账号
INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000006', NULL, 'platform-ops', '[
    {"resource":"tenants","actions":["*"],"scope":"platform"},
    {"resource":"resource_pool","actions":["*"],"scope":"platform"},
    {"resource":"metering","actions":["read","list"],"scope":"platform"}
]'::jsonb
WHERE NOT EXISTS (SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-ops')
ON CONFLICT (id) DO UPDATE SET permissions = EXCLUDED.permissions;

-- platform-readonly：租户/资源池/计量仅 read+list
INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000007', NULL, 'platform-readonly', '[
    {"resource":"tenants","actions":["read","list"],"scope":"platform"},
    {"resource":"resource_pool","actions":["read","list"],"scope":"platform"},
    {"resource":"metering","actions":["read","list"],"scope":"platform"}
]'::jsonb
WHERE NOT EXISTS (SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-readonly')
ON CONFLICT (id) DO UPDATE SET permissions = EXCLUDED.permissions;
```

> `platform-admin` 已存在（`permissions='[{"resource":"*","actions":["*"],"scope":"platform"}]'`），不重复 seed。

#### 3.1.3 平台邮箱唯一索引

PRD US-001 要求 email 全局唯一（`idx_users_platform_email`）。当前迁移仅有 `idx_users_platform_username`（`WHERE tenant_id IS NULL`），需补充邮箱唯一索引：

```sql
-- 平台管理员（tenant_id IS NULL）按 email 全局唯一
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_email
    ON users(email) WHERE tenant_id IS NULL;
```

### 3.2 Entity Definitions

#### 3.2.1 Core 层（端口 + 适配器）

```go
// repo/pkg/ports/platform_user_admin.go

// PlatformUserAdminStore 定义平台运营账号对 users/roles/user_roles 表的全部操作。
// 实现必须使用 platform bypass RLS（WithPlatformTx），不设 tenant_id。
type PlatformUserAdminStore interface {
    // Create 创建平台账号（tenant_id IS NULL）+ 绑定平台角色。
    // passwordHash 由调用方（网关层）bcrypt 生成后传入，Store 不做哈希。
    Create(ctx context.Context, in PlatformUserCreate) (PlatformUser, error)

    // List 分页查询平台账号（tenant_id IS NULL, is_deleted=FALSE）。
    List(ctx context.Context, filter PlatformUserFilter) (PlatformUserListResult, error)

    // Get 按 ID 查询平台账号详情（不含 password_hash）。
    Get(ctx context.Context, userID uuid.UUID) (PlatformUser, error)

    // ChangeRole 事务内先 DELETE 旧 user_roles 再 INSERT 新角色。
    ChangeRole(ctx context.Context, userID uuid.UUID, newRole string) error

    // ResetPassword 校验与旧不同 + bcrypt 哈希 + 更新 password_hash。
    ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error

    // SetStatus 更新 users.status（active/disabled），含最后管理员保护校验。
    SetStatus(ctx context.Context, userID uuid.UUID, status string) error

    // SoftDelete 软删除：UPDATE is_deleted=TRUE, deleted_at=now(), status='disabled'。
    SoftDelete(ctx context.Context, userID uuid.UUID) error

    // CountActivePlatformAdmins 统计活跃 platform-admin 数（排除指定 userID）。
    // 用于最后管理员保护。
    CountActivePlatformAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error)
}

type PlatformUser struct {
    ID          uuid.UUID
    Email       string
    Username    string
    DisplayName *string
    Role        string // platform-admin | platform-ops | platform-readonly
    Status      string // active | disabled
    Source      string // local | third_party（推断：oidc: → third_party, local: → local）
    LastLoginAt *time.Time
    CreatedAt   time.Time
}

type PlatformUserCreate struct {
    Email        string
    Username     string // 不含前缀，Store 层拼 `local:`
    DisplayName  string
    Role         string
    PasswordHash string // bcrypt 已哈希
}

type PlatformUserFilter struct {
    Limit  int
    Cursor string
    Role   string
    Status string
    Source string // local | oidc（按 username 前缀过滤）
    Search string // email / username ILIKE
}

type PlatformUserListResult struct {
    Items      []PlatformUser
    NextCursor string // "" = 无更多
}
```

#### 3.2.2 Services 层（审计 store + Core SDK client）

```go
// repo/services/platform-settings-service/internal/repo/ports/platform_admin_store.go

// PlatformAdminStore 仅操作 audit_logs，不操作 users/roles/user_roles。
type PlatformAdminStore interface {
    CreateAudit(ctx context.Context, in AuditCreateInput) error
    ListAuditLogs(ctx context.Context, userID uuid.UUID, filter AuditLogFilter) (AuditLogListResult, error)
}

type AuditCreateInput struct {
    UserID    *uuid.UUID
    RequestID string
    Action    string // platform_admin.create | change_role | reset_password | disable | enable | delete
    Resource  string // "platform_user"
    Result    string // success | failed
    Details   map[string]any
    IPAddress string
    UserAgent string
}

type AuditLogFilter struct {
    Limit  int
    Cursor string
    Action string // 可选过滤
    Result string // success | failed
}

type AuditLogListItem struct {
    ID        uuid.UUID
    Action    string
    Resource  string
    Result    string
    Details   map[string]any
    CreatedAt time.Time
}

type AuditLogListResult struct {
    Items      []AuditLogListItem
    NextCursor string
}
```

```go
// repo/services/platform-settings-service/internal/repo/ports/core_platform_user.go

// CorePlatformUserClient 是 Core SDK 封装接口，调 /api/v1/admin/platform-users/*。
type CorePlatformUserClient interface {
    Create(ctx context.Context, in PlatformUserCreateInput) (PlatformUserDTO, error)
    List(ctx context.Context, filter PlatformUserListFilter) (PlatformUserListDTO, error)
    Get(ctx context.Context, userID uuid.UUID) (PlatformUserDTO, error)
    ChangeRole(ctx context.Context, userID uuid.UUID, role string) error
    ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error
    SetStatus(ctx context.Context, userID uuid.UUID, status string) error
    SoftDelete(ctx context.Context, userID uuid.UUID) error
}

// DTO 与 Core OpenAPI schema 对齐，由 SDK 生成
type PlatformUserDTO struct {
    ID          string
    Email       string
    Username    string
    DisplayName *string
    Role        string
    Status      string
    Source      string
    LastLoginAt *time.Time
    CreatedAt   time.Time
}
```

#### 3.2.3 平台角色权限矩阵（静态映射）

```go
// repo/services/platform-settings-service/internal/service/platform_admin_service.go

// platformRolePermissions 是平台角色的 4 维权限矩阵静态映射。
// 不查询 Core DB；platform-admin 为 ["*"] 超级权限，其余按 PRD FR-8 硬编码。
var platformRolePermissions = []PlatformRole{
    {
        Name:        "platform-admin",
        Label:       "平台超级管理员",
        Description:  "拥有全部平台管理权限",
        Permissions: map[string]string{
            "tenant_ops":    "write",
            "resource_pool": "write",
            "platform_user": "write",
            "audit_export":  "write",
        },
    },
    {
        Name:        "platform-ops",
        Label:       "平台运维",
        Description:  "可管理租户与资源池，不可管理平台账号与审计导出",
        Permissions: map[string]string{
            "tenant_ops":    "write",
            "resource_pool": "write",
            "platform_user": "none",
            "audit_export":  "none",
        },
    },
    {
        Name:        "platform-readonly",
        Label:       "平台只读",
        Description:  "仅可查看租户、资源池与审计导出",
        Permissions: map[string]string{
            "tenant_ops":    "read",
            "resource_pool": "read",
            "platform_user": "none",
            "audit_export":  "read",
        },
    },
}

type PlatformRole struct {
    Name        string
    Label       string
    Description string
    Permissions map[string]string // tenant_ops / resource_pool / platform_user / audit_export → read / write / none
}
```

### 3.3 Relationships

```text
users (tenant_id IS NULL = 平台运营账号)
  ├── user_roles (N) ──> roles (tenant_id IS NULL = 平台内置角色)
  └── audit_logs (N) [user_id, tenant_id IS NULL, action='platform_admin.*']

无需新增 FK（users/roles/user_roles/audit_logs 表结构不变）
```

### 3.4 Migration Plan

| Step | Layer | Description | Rollback |
|------|-------|-------------|----------|
| 1 | Core | ALTER TABLE users ADD COLUMN display_name / is_deleted / deleted_at（IF NOT EXISTS） | DROP COLUMN（若仅本模块使用） |
| 2 | Core | INSERT INTO roles 种子 platform-ops / platform-readonly（`WHERE NOT EXISTS` + `ON CONFLICT (id) DO UPDATE` 双保险，见 §3.1.2 修正） | DELETE FROM roles WHERE id IN ('...0006','...0007')（按固定 id） |
| 3 | Core | CREATE UNIQUE INDEX idx_users_platform_email（IF NOT EXISTS） | DROP INDEX idx_users_platform_email |

> 迁移文件：`repo/deploy/migrations/20260831000100_platform_roles_seed.sql`（Step 2 已实现；Step 1/3 已由其他批次迁移覆盖，见 issue-003 范围收窄说明）。
> 若 display_name/is_deleted/deleted_at 列已由租户管理员模块迁移添加（见 `spec-new-boss-tenant-admin.md` §3.1.2），则 Step 1 跳过（IF NOT EXISTS 保证幂等）。
> **users/roles/user_roles 表的运行时 CRUD 通过 Core SDK 调 Core API，Services 层不直接 SQL 操作 Core 表。**

---

## 4. API Design

### 4.0 OpenAPI Change Plan

#### Core OpenAPI（`v1.yaml`）

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|--------------|-----------------|
| 新增 `POST /admin/platform-users` | `createPlatformUser` | additive | required (body) |
| 新增 `GET /admin/platform-users` | `listPlatformUsers` | additive | — |
| 新增 `GET /admin/platform-users/{userId}` | `getPlatformUser` | additive | — |
| 新增 `PUT /admin/platform-users/{userId}/role` | `updatePlatformUserRole` | additive | required (body) |
| 新增 `POST /admin/platform-users/{userId}/reset-password` | `resetPlatformUserPassword` | additive | required (body) |
| 新增 `POST /admin/platform-users/{userId}/disable` | `disablePlatformUser` | additive | required (body) |
| 新增 `POST /admin/platform-users/{userId}/enable` | `enablePlatformUser` | additive | required (body) |
| 新增 `DELETE /admin/platform-users/{userId}` | `deletePlatformUser` | additive | required (body) |
| 新增 schemas | `PlatformUser`, `PlatformUserCreateRequest`, `PlatformUserListResponse`, `CursorPage`（复用） | additive | — |
| 新增 error codes | `LAST_PLATFORM_ADMIN`, `EMAIL_ALREADY_EXISTS`, `USERNAME_ALREADY_EXISTS`, `PASSWORD_SAME_AS_OLD` | additive | — |

#### Services OpenAPI（`services/v1.yaml`）

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|--------------|-----------------|
| 新增 `POST /svc/platform-admins` | `createPlatformAdmin` | additive | required (body) |
| 新增 `GET /svc/platform-admins` | `listPlatformAdmins` | additive | — |
| 新增 `GET /svc/platform-admins/roles` | `listPlatformAdminRoles` | additive | — |
| 新增 `GET /svc/platform-admins/{userId}` | `getPlatformAdmin` | additive | — |
| 新增 `PUT /svc/platform-admins/{userId}/role` | `updatePlatformAdminRole` | additive | required (body) |
| 新增 `POST /svc/platform-admins/{userId}/reset-password` | `resetPlatformAdminPassword` | additive | required (body) |
| 新增 `POST /svc/platform-admins/{userId}/disable` | `disablePlatformAdmin` | additive | required (body) |
| 新增 `POST /svc/platform-admins/{userId}/enable` | `enablePlatformAdmin` | additive | required (body) |
| 新增 `DELETE /svc/platform-admins/{userId}` | `deletePlatformAdmin` | additive | required (body) |
| 新增 `GET /svc/platform-admins/{userId}/audit-logs` | `listPlatformAdminAuditLogs` | additive | — |
| 新增 schemas | `PlatformAdminListItem`, `PlatformAdminDetail`, `PlatformRole`, `PlatformAdminCreateRequest` | additive | — |

### 4.1 Core API Endpoints（`/api/v1/admin/platform-users/*`）

> 路径前缀 `/admin/*` 对齐网关 `scopeAllowedForPath` 白名单（仅 scope=platform 可访问）。

| Method | Path | Description | AuthZ | idempotency_key |
|--------|------|-------------|-------|-----------------|
| POST | `/api/v1/admin/platform-users` | 创建平台账号 | platform（JWT scope=platform） | required (body) |
| GET | `/api/v1/admin/platform-users` | 平台账号列表 | platform | — |
| GET | `/api/v1/admin/platform-users/{userId}` | 平台账号详情 | platform | — |
| PUT | `/api/v1/admin/platform-users/{userId}/role` | 修改角色 | platform | required (body) |
| POST | `/api/v1/admin/platform-users/{userId}/reset-password` | 重置密码 | platform | required (body) |
| POST | `/api/v1/admin/platform-users/{userId}/disable` | 禁用 | platform | required (body) |
| POST | `/api/v1/admin/platform-users/{userId}/enable` | 启用 | platform | required (body) |
| DELETE | `/api/v1/admin/platform-users/{userId}` | 软删除 | platform | required (body) |

### 4.2 Services API Endpoints（`/api/v1/svc/platform-admins/*`）

| Method | Path | Description | AuthZ | idempotency_key |
|--------|------|-------------|-------|-----------------|
| POST | `/api/v1/svc/platform-admins` | 创建平台运营账号 | platform-admin | required (body) |
| GET | `/api/v1/svc/platform-admins` | 平台运营账号列表 | platform-admin/ops/readonly | — |
| GET | `/api/v1/svc/platform-admins/roles` | 可变更角色与权限矩阵 | platform-admin/ops/readonly | — |
| GET | `/api/v1/svc/platform-admins/{userId}` | 账号详情 | platform-admin/ops/readonly | — |
| PUT | `/api/v1/svc/platform-admins/{userId}/role` | 修改角色 | platform-admin | required (body) |
| POST | `/api/v1/svc/platform-admins/{userId}/reset-password` | 重置密码 | platform-admin | required (body) |
| POST | `/api/v1/svc/platform-admins/{userId}/disable` | 禁用 | platform-admin | required (body) |
| POST | `/api/v1/svc/platform-admins/{userId}/enable` | 启用 | platform-admin | required (body) |
| DELETE | `/api/v1/svc/platform-admins/{userId}` | 软删除 | platform-admin | required (body) |
| GET | `/api/v1/svc/platform-admins/{userId}/audit-logs` | 操作历史 | platform-admin/ops/readonly | — |

> AuthZ 由 ani-gateway 网关层校验（鉴权 + RBAC），通过 gRPC metadata 透传角色。
> 写操作仅 platform-admin；读操作含 platform-ops/readonly。

### 4.3 Request/Response Schemas

#### POST /svc/platform-admins — 创建

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "email": "admin@ani.io",
  "username": "platform_admin",
  "display_name": "平台管理员",
  "role": "platform-admin",
  "password": "P@ssw0rd123"
}
```
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键 |
| email | string | yes | RFC 5322，全局唯一 |
| username | string | yes | 1-64 字符，不含 `:`，全局唯一 |
| display_name | string | yes | 1-128 字符 |
| role | string | yes | platform-admin / platform-ops / platform-readonly |
| password | string | yes | 8-64 字符，四类至少三类 |

**Response 200:** `{ "id": "uuid", "message": "platform admin created" }`

> Services 层将 password 明文透传给 Core SDK；Core 层 `PlatformUserAPI` handler 负责 `bcrypt.GenerateFromPassword(password, 12)` 生成 `password_hash` 后传给 Store。明文不落日志/审计/响应。

#### GET /svc/platform-admins — 列表

**Query:** `limit`(default 20, max 100) / `cursor` / `role`(platform-admin|platform-ops|platform-readonly) / `status`(active|disabled) / `source`(local|oidc) / `search`(email|username 模糊)

**Response 200 (CursorPage):**
```json
{
  "items": [
    {
      "id": "uuid", "username": "platform_admin", "display_name": "平台管理员",
      "role": "platform-admin", "status": "active", "source": "local",
      "last_login_at": "2026-08-18T10:00:00Z"
    }
  ],
  "next_cursor": "cursor-string"
}
```
> items 每项不含 email（仅详情返回）；source 推断：`oidc:` → third_party，`local:` → local。

#### GET /svc/platform-admins/roles — 可变更角色与权限矩阵

**Response 200:**
```json
{
  "items": [
    {
      "name": "platform-admin",
      "label": "平台超级管理员",
      "description": "拥有全部平台管理权限",
      "permissions": {
        "tenant_ops": "write", "resource_pool": "write",
        "platform_user": "write", "audit_export": "write"
      }
    },
    {
      "name": "platform-ops",
      "label": "平台运维",
      "description": "可管理租户与资源池，不可管理平台账号与审计导出",
      "permissions": {
        "tenant_ops": "write", "resource_pool": "write",
        "platform_user": "none", "audit_export": "none"
      }
    },
    {
      "name": "platform-readonly",
      "label": "平台只读",
      "description": "仅可查看租户、资源池与审计导出",
      "permissions": {
        "tenant_ops": "read", "resource_pool": "read",
        "platform_user": "none", "audit_export": "read"
      }
    }
  ]
}
```
> 不调 Core API，不写审计日志；静态映射来自 §3.2.3。

#### GET /svc/platform-admins/{userId} — 详情

**Response 200:**
```json
{
  "id": "uuid", "email": "admin@ani.io", "username": "platform_admin",
  "display_name": "平台管理员", "role": "platform-admin", "status": "active",
  "source": "local", "last_login_at": "2026-08-18T10:00:00Z",
  "created_at": "2026-08-01T00:00:00Z"
}
```
> 不含 password_hash。

#### PUT /svc/platform-admins/{userId}/role — 修改角色

**Request:** `{ "idempotency_key": "...", "role": "platform-ops" }`
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| role | string | yes | platform-admin / platform-ops / platform-readonly |

**Response 200:** `{ "id": "uuid", "message": "role updated" }`

#### POST .../reset-password — 重置密码

**Request:** `{ "idempotency_key": "...", "new_password": "NewP@ssw0rd" }`
| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| new_password | string | yes | 8-64 字符，四类至少三类，与旧密码不同 |

**Response 200:** `{ "id": "uuid", "message": "password reset" }`

#### POST .../disable | .../enable

**Request:** `{ "idempotency_key": "..." }`
**Response 200:** `{ "id": "uuid", "message": "admin disabled | enabled" }`

#### DELETE /svc/platform-admins/{userId} — 软删除

**Request:** `{ "idempotency_key": "..." }`
**Response 200:** `{ "id": "uuid", "message": "admin deleted" }`

#### GET .../audit-logs — 操作历史

**Query:** `limit`(default 20, max 100) / `cursor` / `action` / `result`(success|failed)

**Response 200 (CursorPage):**
```json
{
  "items": [
    {
      "id": "uuid", "action": "platform_admin.create", "resource": "platform_user",
      "result": "success", "details": { "target_id": "uuid" },
      "created_at": "2026-08-18T10:00:00Z"
    }
  ],
  "next_cursor": "cursor-string"
}
```

### 4.4 Error Responses

见 §6.1 错误分类总表。

### 4.5 Breaking Changes

无破坏性变更。所有端点为新增。Core `v1.yaml` + Services `v1.yaml` 须在首个实现 Issue 回填。

---

## 5. Business Logic

### 5.1 Core 层算法（PlatformUserAdminStore）

#### 5.1.1 Create（创建，US-001）
```text
入参: email, username, display_name, role, password_hash（已由网关 bcrypt 哈希）
校验:
  - email RFC 5322 / username 1-64 字符不含 ':'
  - role ∈ {platform-admin, platform-ops, platform-readonly}
执行 (WithPlatformTx):
  - 查 email 是否已存在 (WHERE tenant_id IS NULL AND email=$1) → 409 EMAIL_ALREADY_EXISTS
  - 查 username 是否已存在 (WHERE tenant_id IS NULL AND username='local:'||$1) → 409 USERNAME_ALREADY_EXISTS
  - 查 role 是否存在 (SELECT id FROM roles WHERE name=$1 AND tenant_id IS NULL) → 404 ROLE_NOT_FOUND
  - INSERT users(tenant_id=NULL, email, username='local:'||username, display_name, password_hash, status='active')
  - INSERT user_roles(user_id, role_id)
返回 PlatformUser
```

#### 5.1.2 List（列表，US-002）
```text
入参: limit, cursor, role, status, source, search
执行 (WithPlatformTx, 只读):
  WHERE users.tenant_id IS NULL AND users.is_deleted = FALSE
  可选: AND role=$role（JOIN user_roles+roles）
  可选: AND status=$status
  可选: source='local' → AND username LIKE 'local:%'
        source='oidc'  → AND username LIKE 'oidc:%'
  可选: AND (email ILIKE '%search%' OR username ILIKE '%search%')
  ORDER BY created_at DESC, id DESC
  游标分页（limit + cursor，多取 1 条判断 next_cursor）
  source 推断: 'oidc:' → third_party, 'local:' → local
返回 items[]（不含 email）+ next_cursor
```

#### 5.1.3 Get（详情，US-003）
```text
入参: userID
执行: SELECT 全字段（不含 password_hash）WHERE id=$1 AND tenant_id IS NULL AND is_deleted=FALSE
  - 无 → 404 PLATFORM_USER_NOT_FOUND
返回 PlatformUser（含 email/created_at）
```

#### 5.1.4 ChangeRole（修改角色，US-004）
```text
入参: userID, newRole
校验: newRole ∈ {platform-admin, platform-ops, platform-readonly}
执行 (WithPlatformTx):
  - 查用户存在 + tenant_id IS NULL → 404 PLATFORM_USER_NOT_FOUND
  - 查新角色 id (SELECT id FROM roles WHERE name=$newRole AND tenant_id IS NULL) → 404 ROLE_NOT_FOUND
  - 事务:
    DELETE FROM user_roles WHERE user_id=$1 AND role_id IN (SELECT id FROM roles WHERE tenant_id IS NULL)
    INSERT INTO user_roles(user_id, role_id) VALUES($1, $newRoleId)
返回 success
```

#### 5.1.5 ResetPassword（重置密码，US-005）
```text
入参: userID, newPassword（明文，由 Services 透传）
校验:
  - 用户存在 + tenant_id IS NULL → 404 PLATFORM_USER_NOT_FOUND
  - 复杂度校验（8-64 字符，四类至少三类）→ 400 VALIDATION_FAILED
执行:
  - bcrypt.CompareHashAndPassword(旧hash, newPassword) 成功 → 422 PASSWORD_SAME_AS_OLD
  - bcrypt.GenerateFromPassword(newPassword, 12)
  - UPDATE users SET password_hash=$1 WHERE id=$2
返回 success
```

#### 5.1.6 SetStatus + 最后管理员保护（禁用/启用，US-006）
```text
Disable 入参: userID
执行 (WithPlatformTx):
  - 查用户 → 404 PLATFORM_USER_NOT_FOUND
  - 查用户当前角色是否为 platform-admin
    - 是 → CountActivePlatformAdmins(excludeUserID=userID)
      - 结果 ≤ 0 → 422 LAST_PLATFORM_ADMIN
    - 否 → 跳过保护
  - UPDATE users SET status='disabled' WHERE id=$1
返回 success

Enable 入参: userID
执行: UPDATE users SET status='active' WHERE id=$1
  - 无最后管理员保护（启用不减少管理员数）
```

#### 5.1.7 SoftDelete + 最后管理员保护（删除，US-006）
```text
入参: userID
执行 (WithPlatformTx):
  - 查用户 → 404 PLATFORM_USER_NOT_FOUND
  - 查用户当前角色是否为 platform-admin
    - 是 → CountActivePlatformAdmins(excludeUserID=userID)
      - 结果 ≤ 0 → 422 LAST_PLATFORM_ADMIN
    - 否 → 跳过保护
  - UPDATE users SET is_deleted=TRUE, deleted_at=now(), status='disabled' WHERE id=$1
返回 success
```

#### 5.1.8 CountActivePlatformAdmins
```text
SELECT COUNT(*) FROM users u
JOIN user_roles ur ON ur.user_id = u.id
JOIN roles r ON r.id = ur.role_id
WHERE u.tenant_id IS NULL
  AND u.is_deleted = FALSE
  AND u.status = 'active'
  AND r.name = 'platform-admin'
  AND r.tenant_id IS NULL
  AND u.id != $excludeUserID
```

### 5.2 Services 层算法（PlatformAdminService）

#### 5.2.1 Create
```text
AuthZ: platform-admin；幂等（body idempotency_key）
校验:
  - email RFC 5322 / username 1-64 不含 ':' / display_name 1-128 / role 白名单 / password 复杂度
执行:
  → CorePlatformUserClient.Create(email, username, display_name, role, password)
    → Core SDK POST /admin/platform-users（Core 层 bcrypt 哈希 + INSERT）
  → PlatformAdminStore.CreateAudit('platform_admin.create', details={target_id, role})
  - Core 返回 409 EMAIL_ALREADY_EXISTS → 透传
  - Core 返回 409 USERNAME_ALREADY_EXISTS → 透传
返回 { id, message }
```

#### 5.2.2 List / Get
```text
AuthZ: platform-admin/ops/readonly；只读
  → CorePlatformUserClient.List / Get
  → 不写审计
返回 items / detail
```

#### 5.2.3 ListRoles
```text
AuthZ: platform-admin/ops/readonly；只读
  → 返回 platformRolePermissions 静态映射（§3.2.3）
  → 不调 Core，不写审计
返回 items[]
```

#### 5.2.4 ChangeRole
```text
AuthZ: platform-admin；幂等
  → CorePlatformUserClient.ChangeRole(userId, newRole)
  → PlatformAdminStore.CreateAudit('platform_admin.change_role', details={old_role, new_role})
返回 { id, message }
```

#### 5.2.5 ResetPassword
```text
AuthZ: platform-admin；幂等
校验: new_password 复杂度（前端预校验，Core 强校验）
  → CorePlatformUserClient.ResetPassword(userId, new_password)
    → Core 层 bcrypt 校验与旧不同 + GenerateFromPassword(cost=12) + UPDATE
  → PlatformAdminStore.CreateAudit('platform_admin.reset_password')
  - Core 返回 422 PASSWORD_SAME_AS_OLD → 透传
返回 { id, message }
```

#### 5.2.6 Disable / Enable / Delete
```text
AuthZ: platform-admin；幂等
Disable:
  → CorePlatformUserClient.SetStatus(userId, 'disabled')
    → Core 层最后管理员保护 + UPDATE
  → PlatformAdminStore.CreateAudit('platform_admin.disable')
  - Core 返回 422 LAST_PLATFORM_ADMIN → 透传
Enable:
  → CorePlatformUserClient.SetStatus(userId, 'active')
  → PlatformAdminStore.CreateAudit('platform_admin.enable')
Delete:
  → CorePlatformUserClient.SoftDelete(userId)
    → Core 层最后管理员保护 + UPDATE is_deleted/deleted_at/status
  → PlatformAdminStore.CreateAudit('platform_admin.delete')
  - Core 返回 422 LAST_PLATFORM_ADMIN → 透传
返回 { id, message }
```

#### 5.2.7 ListAuditLogs
```text
AuthZ: platform-admin/ops/readonly；只读
  → PlatformAdminStore.ListAuditLogs(userId, {limit, cursor, action, result})
    → SQL: SELECT * FROM audit_logs WHERE user_id=$1 AND tenant_id IS NULL
           可选 AND action=$action AND result=$result
           ORDER BY created_at DESC, id DESC，游标分页
  → 不调 Core，不写审计
返回 items[] + next_cursor
```

### 5.3 Validation Rules

| Field | Rule | Layer |
|-------|------|-------|
| email | RFC 5322 | Services + Core |
| username | 1-64 字符，不含 `:` | Services + Core |
| display_name | 1-128 字符 | Services + Core |
| role | platform-admin / platform-ops / platform-readonly | Services + Core |
| password | 8-64 字符，四类至少三类 | Services（预校验）+ Core（强校验） |
| new_password | 同上 + 与旧密码不同 | Core |
| idempotency_key | UUID 格式 | 网关 |

### 5.4 Edge Cases

| Case | Handling |
|------|----------|
| 创建时 email 已存在 | Core 409 EMAIL_ALREADY_EXISTS → Services 透传 |
| 创建时 username 已存在 | Core 409 USERNAME_ALREADY_EXISTS → Services 透传 |
| 修改角色目标不存在 | Core 404 PLATFORM_USER_NOT_FOUND → Services 透传 |
| 重置密码与旧相同 | Core 422 PASSWORD_SAME_AS_OLD → Services 透传 |
| 禁用/删除唯一活跃 platform-admin | Core 422 LAST_PLATFORM_ADMIN → Services 透传 |
| 列表无数据 | 返回 items=[], next_cursor="" |
| 详情用户已软删除 | Core 404 PLATFORM_USER_NOT_FOUND |
| 操作历史无记录 | 返回 items=[], next_cursor="" |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP | Layer | Condition | User Message (zh-CN) |
|------------|------|-------|-----------|---------------------|
| EMAIL_ALREADY_EXISTS | 409 | Core | 创建时 email 已存在 | 邮箱已被占用 |
| USERNAME_ALREADY_EXISTS | 409 | Core | 创建时 username 已存在 | 用户名已被占用 |
| PLATFORM_USER_NOT_FOUND | 404 | Core | 用户不存在/已软删除/非平台账号 | 平台账号不存在或已删除 |
| ROLE_NOT_FOUND | 404 | Core | 角色不存在（seed 缺失） | 角色不存在 |
| LAST_PLATFORM_ADMIN | 422 | Core | 唯一活跃 platform-admin 删除/禁用 | 至少保留一名活跃的平台超级管理员 |
| PASSWORD_SAME_AS_OLD | 422 | Core | 新密码与旧密码相同 | 新密码不能与旧密码相同 |
| ROLE_CHANGE_INVALID | 422 | Services | role 不在允许范围 | 角色不在允许范围（platform-admin/ops/readonly） |
| VALIDATION_FAILED | 400 | Core/Services | 参数校验失败 | 校验失败：{message} |
| FORBIDDEN | 403 | 网关 | 角色不匹配（非 platform-admin 写操作） | 无平台运营权限 |
| IDEMPOTENCY_CONFLICT | 409 | 网关 | 同 key 不同 body | 请求冲突，请重试 |
| UNAUTHORIZED | 401 | 网关 | 未登录/token 失效 | 未登录或登录已过期 |

> 幂等键校验（`IDEMPOTENCY_KEY_INVALID` 400）由网关中间件处理。

### 6.2 Retry Strategy

| Operation | Retry | Max | Backoff |
|-----------|-------|-----|---------|
| Core SDK 调用失败 | 是（幂等操作） | 3 | 指数退避 200ms/400ms/800ms |
| 非幂等操作失败 | 否 | — | — |

### 6.3 Failure Modes

| Failure | Impact | Handling |
|---------|--------|----------|
| Core API 不可用 | 全部操作失败 | 502/503，前端展示服务不可用 |
| platform-settings-service gRPC 不可用 | 全部操作失败 | 502/503 |
| PostgreSQL（Core DB）连接失败 | Core 层失败 | 500 |
| PostgreSQL（Services DB，audit_logs）连接失败 | 审计写入失败 | best-effort，不阻断业务（Warn 日志） |

---

## 7. Security

### 7.1 Authentication & Authorization

| Endpoint | platform-admin | platform-ops | platform-readonly |
|----------|---------------|-------------|-------------------|
| POST /svc/platform-admins | ✅ | ❌ | ❌ |
| GET /svc/platform-admins | ✅ | ✅ | ✅ |
| GET /svc/platform-admins/roles | ✅ | ✅ | ✅ |
| GET /svc/platform-admins/{userId} | ✅ | ✅ | ✅ |
| PUT .../role | ✅ | ❌ | ❌ |
| POST .../reset-password | ✅ | ❌ | ❌ |
| POST .../disable \| enable | ✅ | ❌ | ❌ |
| DELETE .../{userId} | ✅ | ❌ | ❌ |
| GET .../audit-logs | ✅ | ✅ | ✅ |

> AuthZ 由 ani-gateway 网关层校验：
> - 路径前缀 `/api/v1/svc/*` 允许 scope=platform 和 scope=tenant（`scopeAllowedForPath`）
> - RBAC 中间件按角色名校验：写操作仅 platform-admin，读操作含 ops/readonly
> - Core `/api/v1/admin/*` 端点仅 scope=platform 可访问（网关白名单）
> - Services 层通过 gRPC metadata 透传角色，不重复校验

### 7.2 Input Validation

- email/username/password 服务端强校验（见 §5.3）
- role 白名单校验
- `idempotency_key` 格式与去重由网关处理
- username 不含 `:` 前缀（防注入），Core 层拼 `local:`

### 7.3 Data Protection

- `password_hash` 用 bcrypt cost=12；明文密码不落日志/审计/响应
- 密码在 Services → Core SDK 传输走 HTTPS（内网），Core 层哈希后写库
- 审计日志记录所有写操作（`platform_admin.*`），含 user_id / request_id / ip_address / user_agent
- `audit_logs.details` 不含密码明文

---

## 8. Performance

### 8.1 Expected Load

| Metric | Estimate |
|--------|----------|
| 平台账号总数 | < 50 |
| 列表 QPS | < 5 |
| 写操作频率 | 极低（运维操作） |
| 操作历史行数/账号 | < 1000 |

### 8.2 Optimization Strategy

| Strategy | Application |
|----------|-------------|
| 游标分页 | limit+cursor，按 created_at DESC |
| 单次 JOIN | Core List 查询单次 JOIN users+user_roles+roles |
| 静态权限映射 | ListRoles 不查 DB，内存返回 |
| 审计 best-effort | 审计写入失败不阻断业务 |

### 8.3 Database Considerations

| Index | Purpose |
|-------|---------|
| idx_users_platform_username (UNIQUE, WHERE tenant_id IS NULL) | username 全局唯一 + 查询 |
| idx_users_platform_email (UNIQUE, WHERE tenant_id IS NULL) | email 全局唯一 + 查询 |
| idx_audit_tenant (tenant_id, created_at DESC) | 操作历史查询（tenant_id IS NULL 走 platform_bypass） |
| roles (tenant_id IS NULL, name) UNIQUE | 角色查询 |

---

## 9. Testing Strategy

### 9.1 Core 层 Unit Tests

| Test | Scope | Description |
|------|-------|-------------|
| TestPlatformUserAdminStore_Create | adapter | 创建成功、email 重复 409、username 重复 409、role 不存在 404 |
| TestPlatformUserAdminStore_List | adapter | 分页、过滤（role/status/source/search）、is_deleted 过滤 |
| TestPlatformUserAdminStore_ChangeRole | adapter | 事务内 DELETE+INSERT、用户不存在 404 |
| TestPlatformUserAdminStore_ResetPassword | adapter | 成功、同旧密码 422、复杂度 400 |
| TestPlatformUserAdminStore_Disable_LastAdmin | adapter | 唯一活跃 admin 禁用 422 |
| TestPlatformUserAdminStore_SoftDelete_LastAdmin | adapter | 唯一活跃 admin 删除 422 |
| TestPlatformUserAdminStore_CountActiveAdmins | adapter | 排除目标用户计数 |

### 9.2 Services 层 Unit Tests

| Test | Scope | Description |
|------|-------|-------------|
| TestPlatformAdminService_Create | service | 调 Core SDK 成功/失败、审计写入 |
| TestPlatformAdminService_ListRoles | service | 静态映射返回 3 个角色 + 权限矩阵 |
| TestPlatformAdminService_ChangeRole | service | 调 Core SDK + 审计 |
| TestPlatformAdminService_ResetPassword | service | Core SDK PASSWORD_SAME_AS_OLD 透传 |
| TestPlatformAdminService_Disable_LastAdmin | service | Core SDK LAST_PLATFORM_ADMIN 透传 |
| TestPlatformAdminService_ListAuditLogs | service | audit_logs 查询 + 过滤 + 分页 |

### 9.3 Integration Tests

| Test | Description |
|------|-------------|
| TestHandler_CreateFlow | POST create → GET list 验证 → GET detail 验证全字段 |
| TestHandler_ChangeRoleFlow | PUT role → GET detail 验证新角色 |
| TestHandler_ResetPasswordFlow | POST reset-password → 验证旧密码登录失败、新密码登录成功 |
| TestHandler_DisableEnableFlow | POST disable → GET detail status=disabled → POST enable → status=active |
| TestHandler_DeleteFlow | DELETE → GET detail 404 |
| TestHandler_LastAdminProtection | 唯一 admin delete/disable → 422 |
| TestHandler_AuditLogsFlow | 各写操作后 GET audit-logs 验证记录 |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type |
|-------|------|------|
| US-001 创建 | TestPlatformUserAdminStore_Create + TestHandler_CreateFlow | unit + integration |
| US-002 列表 | TestPlatformUserAdminStore_List | unit |
| US-003 详情 | TestHandler_CreateFlow（详情断言） | integration |
| US-004 改角色 | TestPlatformUserAdminStore_ChangeRole + TestHandler_ChangeRoleFlow | unit + integration |
| US-005 重置密码 | TestPlatformUserAdminStore_ResetPassword + TestHandler_ResetPasswordFlow | unit + integration |
| US-006 禁用启用删除 | TestPlatformUserAdminStore_Disable_LastAdmin + TestHandler_DisableEnableFlow + TestHandler_DeleteFlow + TestHandler_LastAdminProtection | unit + integration |
| US-007 角色与权限矩阵 | TestPlatformAdminService_ListRoles | unit |
| US-008 操作历史 | TestPlatformAdminService_ListAuditLogs + TestHandler_AuditLogsFlow | unit + integration |
| FR-1 | TestPlatformUserAdminStore_Create | unit |
| FR-2 | TestPlatformUserAdminStore_List + TestPlatformUserAdminStore_Get | unit |
| FR-3 | TestPlatformAdminService_ListRoles | unit |
| FR-4 | TestPlatformUserAdminStore_ChangeRole + TestPlatformUserAdminStore_ResetPassword | unit |
| FR-5 | TestPlatformUserAdminStore_Disable_LastAdmin + TestPlatformUserAdminStore_SoftDelete_LastAdmin | unit |
| FR-6 | TestPlatformAdminService_ListAuditLogs | unit |
| FR-7/8 | TestPlatformAdminService_ListRoles（权限矩阵断言） | unit |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | Description | Depends On |
|-------|-------------|------------|
| 1 | Core OpenAPI 契约：`v1.yaml` 补齐 `/admin/platform-users/*` 路径/schema/错误码 | — |
| 2 | Core SDK 重新生成：`make gen-core-sdk` | 1 |
| 3 | 数据库迁移：users 列扩展 + roles 种子 + email 唯一索引 | — |
| 4 | Core 端口 + 适配器：`PlatformUserAdminStore` + `PostgresPlatformUserAdminStore` | 3 |
| 5 | Core 网关 handler：`PlatformUserAPI` + 路由注册 | 1, 4 |
| 6 | Services OpenAPI 契约：`services/v1.yaml` 补齐 `/svc/platform-admins/*` | — |
| 7 | gRPC proto + buf 生成 | 6 |
| 8 | platform-settings-service 新建：服务骨架 + Core SDK client + 审计 store + PlatformAdminService | 2, 7 |
| 9 | ani-gateway Services 转发路由：`platform_admin_resources.go` | 7, 8 |
| 10 | 前端 API 封装 + `_authenticated.tsx` 菜单 | 6 |
| 11 | 前端页面组件：列表/创建向导/详情/Dialog | 10 |
| 12 | 集成测试 + E2E 验证 | 5, 9, 11 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 Core OpenAPI 契约 | 2.2, 4.0, 4.1, 4.3 | high | — |
| #2 Core SDK 重新生成 | 2.3 | high | #1 |
| #3 数据库迁移 | 3.1, 3.4 | high | — |
| #4 Core 端口 + 适配器 | 2.3, 3.2.1, 5.1 | high | #3 |
| #5 Core 网关 handler | 2.3, 2.5, 5.1, 6.1 | high | #1, #4 |
| #6 Services OpenAPI 契约 | 2.2, 4.0, 4.2, 4.3 | high | — |
| #7 gRPC proto + 生成 | 2.5, 2.3 | high | #6 |
| #8 platform-settings-service 新建 + 业务逻辑 | 2.3, 3.2.2, 3.2.3, 5.2 | high | #2, #7 |
| #9 ani-gateway Services 转发 | 2.3, 2.5, 7.1 | high | #7, #8 |
| #10 前端 API + 菜单 | UX §2, §5 | high | #6 |
| #11 前端页面组件 | UX §4, §5, §6 | high | #10 |
| #12 集成/E2E 测试 | 9.3, 9.4 | medium | #5, #9, #11 |

### 10.3 Incremental Delivery

1. Phase 1-3 可并行（Core 契约 + 迁移）
2. Phase 6 可与 Phase 1 并行（Services 契约独立）
3. Phase 10-11（前端）依赖 Services 契约；可先用 mock 先行
4. 写操作依赖 Core handler + platform-settings-service 完成
5. Core SDK 重新生成（Phase 2）是 Services 层（Phase 8）的硬依赖

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- Core `/admin/platform-users/*` 端点尚未在 `v1.yaml` 声明——首个 Issue 需补齐 Core OpenAPI 契约并重新生成 SDK。
- `display_name` / `is_deleted` / `deleted_at` 列是否已由租户管理员模块迁移添加——需确认 `20260723_015_tenant_management.sql` 内容；IF NOT EXISTS 保证幂等。
- Core 网关 `scopeAllowedForPath` 已对 `/admin/*` 放行 scope=platform，无需修改中间件。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Core SDK 需重新生成 | Services 层无法调 Core | Phase 2 在 Phase 8 前完成；`make gen-core-sdk` |
| Core v1.yaml 契约未冻结 | SDK/网关契约漂移 | 首个 Issue 回填 Core 契约并校验 |
| Services v1.yaml 契约未冻结 | 前端契约漂移 | 首个 Issue 回填 Services 契约 |
| platform-settings-service 服务骨架 | 全新微服务从零搭建 | `bootstrap.RunGRPC` + `services/pkg` 已提供通用启动框架；对齐 tenant-service 建服模式 |

### 11.3 Assumptions

- Core 层 `PlatformUserAdminStore` 使用 `WithPlatformTx`（platform bypass RLS，不设 tenant_id）。
- Core `/api/v1/admin/*` 路径已被网关 `scopeAllowedForPath` 白名单放行（scope=platform）——见 `auth.go` 第 224-228 行。
- `bcrypt.GenerateFromPassword` 在 Core 网关 handler 层调用（非 Store 层），Store 只接收已哈希的 `password_hash`。但 ResetPassword 需 Core 层先 `CompareHashAndPassword` 校验与旧不同再 `GenerateFromPassword`——因此 ResetPassword 的哈希在 Core Store 层完成。
- 审计日志复用现有 `audit_logs` 分区表；action 命名 `platform_admin.create / change_role / reset_password / disable / enable / delete`。
- 前端使用 TDesign React + TanStack Router + TanStack Query；`idempotency_key` 由 `crypto.randomUUID()` 生成对用户不可见。
- 平台角色权限矩阵为静态映射（§3.2.3），不查询 Core DB；`platform-admin` 权限为全 write（超级管理员）。
- source 推断：`oidc:` → third_party（「第三方」），`local:` → local（「本地」）；P0 仅存在本地账号。
- 最后管理员保护：删除/禁用前活跃 platform-admin 数 ≤ 0（排除当前目标）→ 422 LAST_PLATFORM_ADMIN，Core 层事务内原子校验。

---

## 12. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | **core** + **services** + **boss**（跨三层） |
| Code scope | `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml`、`repo/pkg/ports/`、`repo/pkg/adapters/postgres/`、`repo/services/ani-gateway/`、`repo/sdks/core/`、`repo/services/platform-settings-service/`（NEW）、`repo/api/proto/platform_settings/v1/`、`repo/frontends/boss/src/`、`repo/deploy/migrations/` |
| OpenAPI authority | Core `v1.yaml`（新增 `/admin/platform-users/*`）+ Services `v1.yaml`（新增 `/svc/platform-admins/*`），均须先改 |
| Frozen exclusions | Services 层不得 import `pkg/ports`/`pkg/adapters`（边界校验脚本强制）；不新建表；不改 `users.status` 语义（仅 active/disabled）；不做邮件邀请 |
| idempotency_key | POST create / PUT role / POST reset-password / POST disable / POST enable / DELETE 均支持（body） |
| Architecture gate | Core 层 `WithPlatformTx`（platform bypass RLS）；Services 层经 Core SDK 调 Core，不直接 SQL 操作 Core 表 |
| Services 承载 | **新建 `platform-settings-service`**（平台设置通用服务；module `github.com/kubercloud/ani/services/platform-settings-service`；gRPC 9106 / health 9206；网关经 `PLATFORM_SETTINGS_SERVICE_ADDR` 连接，缺省 `127.0.0.1:9106`）；本批承载平台运营账号模块，后续平台设置模块复用 |
| Module main doc | `spec-boss-platform-admin.md` |

---

## 13. 参考文档

- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- Core 登录 SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`（bcrypt cost=12、平台账号查询谓词）
- 租户管理员 SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`（Core SDK 模式、审计 store、users 列扩展）
- 配额服务 SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`（Core ports/adapters 范式、RLS 双 policy）
- Core Handler 实现指南: `repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md`

---

## 变更记录

| 日期 | 说明 |
|------|------|
| 2026-08-18 | 初版：基于 PRD + UX 生成；按用户指示将 users/roles 操作下沉到 Core 层（端口+适配器+网关 handler+OpenAPI+SDK），Services 层经 Core SDK 调用 |
| 2026-08-31 | 修订：Services 承载从 tenant-service 改为**新建 platform-settings-service**（平台设置通用服务）；proto 包 `tenant/v1` → `platform_settings/v1`；gRPC 9106 / health 9206；网关新增 `PLATFORM_SETTINGS_SERVICE_ADDR`；删除前端 Issue |