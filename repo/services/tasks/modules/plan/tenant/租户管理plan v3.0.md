# BOSS 端租户管理功能设计 plan.md

> 生成日期：2026-07-24
> 目标仓库：`D:/Jczn/project/ANI/ANI`
> 设计依据：`CLAUDE.md`、`ANI-DOCS-INDEX.md`、`ANI-02-产品功能设计.md`、`ANI-05-系统架构设计.md`、`ANI-09-数据模型设计.md`、`ANI-11-代码实现规范.md`、`ANI-14-API对齐与开发工作流.md`、`ANI-SERVICES-TEAM-GUIDE.md`、`repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml`、`repo/deploy/migrations/`、原型 `ANI-doc/00-prd/产品原型-7.22/` 全套
> 实现路径：3 个 PR 阶段（契约 → 接口 → 实现+文档），全部新 API 放入 `repo/api/openapi/services/v1.yaml`，SQL 迁移与具体实现集中在 PR-3

---

## §1 摘要

### 1.1 范围

为 BOSS 平台运营端增加"租户管理"功能，覆盖 4 个核心闭环：

1. **租户生命周期管理**：创建/查看/编辑/冻结/解冻/禁用
2. **资源配额管理**：八维度配额（GPU / CPU / 内存 / 存储 / Token / KB 查询 / 成员数 / 推理服务数），平台可查看每维度最大配额与当前已用；仅 platform-admin / platform-ops 有权直接调整配额（增配/减配）
3. **租户管理员管理**：查看所有具有租户管理员角色的用户列表，重置密码、修改权限、禁用/启用
4. **平台运营账号管理**：platform-admin / platform-ops / platform-readonly 三类平台账号 CRUD；支持重置密码、启用/禁用、改角色、软删除；含最后管理员保护
5. **套餐模板管理**：套餐含配额限额模板（plan_quota_limits 表），状态 draft（草稿）→ active（启用）→ disabled（停用）；配额维度与默认值由 Core `resource_quota_meta` 注册表统一管理，套餐通过 plan_quota_limits 关联各维度限额值

> 计费与用量管理暂不实现，后续 PR 再补充。

### 1.2 关键决策

| 决策项 | 选择 | 理由 |
|--------|------|------|
| API 契约位置 | `repo/api/openapi/services/v1.yaml` | 租户管理是业务能力，归 Services；遵循 ANI-14 API 优先与分层 |
| PR 拆分 | 3 阶段：契约 → 接口 → 实现+文档 | 用户明确要求；契约稳定后再写实现，降低返工 |
| 数据库迁移位置 | PR-3 | PR-1 仅含 v1.yaml 及衍生；用户明确要求 |
| 状态机 | active / frozen / disabled | 管理员可手动冻结（无法登录、实例继续运行）；管理员可手动禁用（禁止登录、资源删除）；管理员可解冻（frozen → active），禁用不可逆 |
| 配额与计量归属 | Core（`通用资源配额与计量落地方案.md`） | BOSS 仅负责：配额元数据 UI/代理、租户配额配置 UI、创建租户时配额初始化；TCC（Try/Confirm/Cancel）与计量采集属 Core 责任 |
| 平台角色 | 保留 platform-admin，新增 platform-ops / platform-readonly 作为种子 | 与用户要求一致；platform-admin 仍为超级管理员 |
| 租户角色 | 新增 tenant-owner（所有者）；保留 tenant-admin / user / auditor；permissions 改为 4 维度 JSONB（compute / inference / member / transfer） | 所有者可操作全部 4 维度；管理员前 3 个；普通用户前 2 个；审计只读前 2 个 |
| 幂等性 | 复用现有 Gateway `Idempotency` 中间件 + Redis 存储（`idempotency:` key，TTL 24h） | 现有实现见 `repo/services/ani-gateway/internal/middleware/idempotency.go`；POST/PUT 自动 dedup，不写数据库 |
| 审计日志 | 复用现有 `audit_logs` 分区表（`20260501000100_init_schema.sql` SECTION 10） | 已有 `tenant_id`/`user_id`/`request_id`/`action`/`resource`/`result`/`details`/`ip_address`/`user_agent`/`created_at`，按月分区；不新建表 |
| 最后管理员保护 | 平台 admin 删除/禁用前检查活跃数；为 0 则 422 LAST_PLATFORM_ADMIN | 防止平台失能 |
| 租户最后所有者保护 | 删除/禁用/降级最后活跃 tenant-owner 前检查；计数 ≤ 1（排除目标）则 422 LAST_TENANT_OWNER，防止"租户无 owner"；移交给其他租户成员允许 | 每个租户恒有且仅有一名 tenant-owner |
| 前端基础路径 | `/boss/` | 与现有 BOSS 一致；TDesign React + TanStack Router |

### 1.3 边界

**本设计覆盖：**

- Core API 契约 `repo/api/openapi/v1.yaml` — **不修改**；租户管理属 Services 业务能力，按 CLAUDE.md §3 跨层契约规则不回流 Core API
- Services API 契约 `repo/api/openapi/services/v1.yaml` — 重新设计新增 `tenants` / `tenant-admins` / `platform-admins` / `tenant-plans` 路径与全套 schema/错误码/标签
- tenant-service 服务骨架（cmd / handlers / internal/service / internal/wiring）
- pkg/ports 接口抽象（TenantStore / TenantAdminStore / PlatformAdminStore / TenantPlanStore / TenantLifecycleStore / TenantAuthStore / AuditLogStore / QuotaSvcClient；QuotaSvcClient 封装 Core 配额 API 调用；幂等由 Gateway 中间件处理，不入 ports）
- postgres adapter 实现
- SQL 迁移 `20260723_015_tenant_management.sql`（含 tenant_plans 表；不新建 tenant_quotas 与 tenant_usage_records——配额与用量由 Core `resource_quota` / `metering_usage_records` 表承载，详见 `通用资源配额与计量落地方案.md`）
- BOSS 前端 5 页面（含套餐管理页面）+ API 客户端 + 受保护路由
- 单测、集成测试、E2E 验证清单
- 验收脚本与文档

**本设计不覆盖：**

- Console 端租户自助功能（`/api/v1/svc/tenant/members` 已存在，由 Console inviteTenantMember 维护）
- 计费与用量管理（充值/扣费/账单/欠费冻结等）——后续 PR 再补充
- TCC 配额扣减（Try/Confirm/Cancel）与计量采集——属 Core 责任，见 `通用资源配额与计量落地方案.md`，本设计仅在 BOSS 侧提供 UI 代理与租户创建时的配额初始化调用
- 业务配额（max_models / max_kb / max_sessions 等）的下发与快照——后续 PR
- 实际 SMTP / 短信通道接入——使用现有 notification-service stub

---

## §2 设计大纲

### 2.1 模块清单

| 模块 | 路径 | 角色 |
|------|------|------|
| API 契约 | `repo/api/openapi/services/v1.yaml` | 新增 tenants / tenant-admins / platform-admins / tenant-plans 路径与 schema（不含 quotas 子路径；配额由 Core API 承载） |
| 端口接口 | `repo/pkg/ports/` | 新增 8 个接口文件：TenantStore / TenantAdminStore / PlatformAdminStore / TenantPlanStore / TenantLifecycleStore / TenantAuthStore / QuotaChangeRequestStore / AuditLogStore（配额相关端口 QuotaMetaService / QuotaService 由 Core `通用资源配额与计量落地方案.md` 定义，本设计不重复；幂等由 Gateway 中间件处理，不入 ports） |
| Postgres 适配器 | `repo/pkg/adapters/postgres/` | 新增 8 个实现文件（不含 tenant_quota_store / tenant_usage_store——配额与用量由 Core adapter 承载） |
| Redis 适配器 | `repo/pkg/adapters/redis/` | 不新增 |
| 幂等中间件 | 现有 | 复用，不新增 |
| tenant-service | `repo/services/tenant-service/` | 新服务：cmd / handlers / service / wiring |
| SQL 迁移 | `repo/deploy/migrations/20260723_015_tenant_management.sql` | tenants 列扩展（contact_email + status 迁移）、tenant_plans、plan_quota_limits、tenant_lifecycle、tenant_auth（MFA/SSO 配置）、tenant_admin_invitation（租户管理员邀请表）、平台角色种子（audit_logs 复用现有分区表，不新建；不新建 tenant_quotas / tenant_usage_records——配额与用量表由 Core 迁移承载） |
| BOSS 前端 | `repo/frontends/boss/src/` | 5 页面 + API 客户端 + 受保护路由（含套餐管理、配额元数据管理与租户配额配置代理 Core API） |
| 集成测试 | `repo/services/tenant-service/internal/...` | adapter + handler 单测，跨服务集成测 |
| 文档 | `repo/services/tasks/modules/spec/boss/tenant/spec-boss-tenant-*.md` | 6 个 spec 文档（含 plans） |

### 2.2 模块依赖

```
┌──────────────────────────────────────────────────────────────┐
│                  BOSS Frontend (React)                       │
│  /boss/tenants  /boss/tenants/admins                          │
│  /tenants/quotas (+ /new, /$planId)  ← 配额套餐（实现对齐）   │
│  （不做 /tenants/quota-meta 管理页；套餐侧 GET /api/v1/svc/quota-meta）│
│  /boss/settings/platform-admins                               │
└──────────────────────────────────────────────────────────────┘
                          │ HTTPS
                          ▼
┌──────────────────────────────────────────────────────────────┐
│              Core Gateway (existing)                         │
│  - AuthN: platform JWT 校验                                  │
│  - AuthZ: platform-admin / platform-ops / platform-readonly   │
│  - 请求级 idempotency_key body 字段校验                         │
└──────────────────────────────────────────────────────────────┘
                          │ /api/v1/svc/*  +  /api/v1/* (Core 配额)
                          ▼
┌──────────────────────────────────────────────────────────────┐
│              tenant-service (new)                            │
│  - handlers (HTTP) → service (domain) → ports (interfaces)   │
│  - audit log 写入                                            │
│  - idempotency 复用                                          │
│  - 创建租户时调用 Core QuotaService 初始化 resource_quota    │
└──────────────────────────────────────────────────────────────┘
                          │ ports
                          ▼
┌──────────────────────────────────────────────────────────────┐
│           postgres adapter (new)                             │
│  - tenants / tenant_admins / platform_admins                 │
│  - tenant_plans                                              │
│  - audit_logs (existing partitioned)                         │
└──────────────────────────────────────────────────────────────┘
                          │ SQL
                          ▼
┌──────────────────────────────────────────────────────────────┐
│              PostgreSQL (shared)                             │
│  + Core 配额/计量表（由 Core 迁移维护，BOSS 通过 port 访问）:  │
│    resource_quota_meta / resource_quota / resource_reservations│
│    metering_usage_records                                     │
└──────────────────────────────────────────────────────────────┘
```

### 2.3 PR 阶段拆分

| PR | 范围 | 文件 |
|----|------|------|
| **PR-1 契约** | services/v1.yaml 新增 tenants / tenant-admins / platform-admins / tenant-plans 全部路径 + schema + 错误码 + 标签；自动衍生 schema.d.ts / core-schema.d.ts；不写实现 | `repo/api/openapi/services/v1.yaml`、`repo/frontends/boss/src/api/schema.d.ts`、`repo/frontends/console/src/api/schema.d.ts`、`repo/frontends/console/src/api/core-schema.d.ts`（如受影响） |
| **PR-2 接口** | pkg/ports 8 个接口定义（不含 TenantQuotaStore / TenantUsageCache——由 Core 定义）；tenant-service 骨架；handler 空实现（含 plans handler）；服务可启动但返回 501 | `repo/pkg/ports/tenant_*.go`、`services/tenant-service/internal/repo/ports/tenant_plan_store.go`（配额套餐相关 ports 已下沉 tenant-service 自有 repo）、`repo/services/tenant-service/cmd/`、`repo/services/tenant-service/handlers/`、`repo/services/tenant-service/internal/wiring/` |
| **PR-3 实现+文档** | SQL 迁移（含 tenant_plans；不新建 tenant_quotas / tenant_usage_records）；postgres adapter；service 实现（含 plan CRUD、CreateTenant 调用 Core QuotaService 初始化 resource_quota）；BOSS 5 页面（含套餐管理、配额元数据管理代理 Core）；测试；spec 文档；README | `repo/deploy/migrations/20260723_015_tenant_management.sql`、`services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`（配额套餐相关 adapter 已下沉 tenant-service 自有 repo）、`services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`、`services/tenant-service/internal/service/`、`repo/frontends/boss/src/routes/_authenticated/tenants/`、`repo/services/tasks/modules/spec/boss/tenant/spec-boss-tenant-*.md` |

---

## §3 架构

### 3.1 系统上下文

```
┌──────────────────┐      ┌──────────────────┐      ┌──────────────────┐
│   BOSS (运营)    │      │  Console (租户)  │      │  第三方 (预留)   │
│  /boss/tenants*  │      │  /console/*      │      │  /api/v1/svc/*   │
└────────┬─────────┘      └────────┬─────────┘      └────────┬─────────┘
         │                         │                         │
         └──────────┬──────────────┴─────────────────────────┘
                    │ HTTPS (JWT)
                    ▼
         ┌──────────────────────────────┐
         │      Core Gateway            │
         │  - 平台 JWT 校验              │
         │  - 平台角色鉴权              │
│  - frozen 状态禁止登录拦截     │
│  - disabled 状态禁止登录拦截   │
         │  - RLS 上下文设置            │
         └──────────┬───────────────────┘
                    │ /api/v1/svc/tenants*
                    │ /api/v1/svc/tenant-admins*
                    │ /api/v1/svc/platform-admins*
                    │ /api/v1/svc/tenant-plans*
                    ▼
         ┌──────────────────────────────┐
         │      tenant-service          │
         │  handlers → service → ports  │
         │  + audit log                 │
         │  + idempotency (复用)         │
         └──────────┬───────────────────┘
                    │ SQL (RLS)
                    ▼
         ┌──────────────────────────────┐
         │       PostgreSQL              │
         │  tenants / tenant_admins      │
         │  tenant_plans / platform_admins │
         │  audit_logs                    │
         │  (existing, partitioned)       │
         │  + Core 配额表（由 Core 维护）: │
         │  resource_quota_meta            │
         │  resource_quota                 │
         │  resource_reservations          │
         │  metering_usage_records         │
         └──────────────────────────────┘
```

### 3.2 组件职责

| 组件 | 职责 | 不做 |
|------|------|------|
| BOSS 前端 | 页面渲染、表单校验、TanStack Query 缓存、调用 fetch | 不直接连数据库；不内嵌业务规则 |
| Core Gateway | AuthN、AuthZ、冻结/禁用状态拦截、RLS 上下文、幂等 body 字段校验 | 不实现租户 CRUD |
| tenant-service handlers | HTTP 解析、请求体校验、错误转换、审计日志触发 | 不直接写 SQL |
| tenant-service service 层 | 业务规则：状态机、配额边界、最后管理员保护 | 不直接 HTTP；不直接 SQL |
| ports 接口 | 抽象数据访问，便于 mock | 不含业务规则 |
| postgres adapter | SQL 生成、RLS 设置、事务管理 | 不做业务决策 |
| 审计日志 | 跨服务可观测、合规追溯 | 不阻塞主流程（异步写入或同步事务内） |

### 3.3 数据流（创建租户为例）

```
1. BOSS 前端
   POST /api/v1/svc/tenants
   Headers: Authorization: Bearer <platform JWT>
   Body: { name, display_name, email, plan_id, admin_email, admin_name, admin_password, idempotency_key }

2. Core Gateway
   - 校验 JWT → 解出 platform_user_id, roles
   - 检查 roles 包含 platform-admin 或 platform-ops
   - 检查 body idempotency_key 格式（UUID）
   - 设置 RLS 上下文：SET app.current_tenant_id = NULL（平台操作绕过租户隔离）
   - 转发至 tenant-service

3. tenant-service handler
   - 解析 body（OpenAPI 校验）
   - 透传 body idempotency_key 至 service

4. tenant-service service.CreateTenant
   - 校验 name 全局唯一（UNIQUE 约束保证）
   - 校验 plan_id 对应套餐 status='active'
   - 幂等由 Gateway 中间件处理（Redis `idempotency:platform::POST:/api/v1/svc/tenants:{sha256(key)}`）
   - 事务内：
     INSERT tenants (name, display_name, contact_email, plan_id, status='active')
     INSERT tenant_auth (tenant_id) VALUES (tenant.id) -- 1:1 关系，全部默认值
     INSERT users (email=admin_email, username=admin_name, status='active', password_hash=bcrypt(admin_password, 12))
     INSERT user_roles (绑定 tenant-owner 内置角色)
     调用 Core API 逐维度初始化该租户各 resource_type 的 resource_quota.total（按 plan_quota_limits 读取套餐限额，total=NULL 时用 resource_quota_meta.default_quota 兜底）
     INSERT audit_logs (action='tenant.create', ...) -- 复用现有分区表
     INSERT tenant_lifecycle (action='active', ...) -- 生命周期记录
   - 提交

5. 响应 200 OK
   { id, message }

6. 前端 invalidateQuery(['tenants'])
```

### 3.4 文件结构（新增）

```
repo/
├── api/openapi/services/v1.yaml                    [MODIFY: 新增 tenants* / tenant-admins* / platform-admins* / tenant-plans*]
├── deploy/migrations/
│   └── 20260723_015_tenant_management.sql           [NEW: PR-3; 不含 tenant_quotas / tenant_usage_records——由 Core 迁移维护]
├── pkg/
│   ├── ports/
│   │   ├── tenant_store.go                          [NEW: PR-2]
│   │   ├── tenant_admin_store.go                   [NEW: PR-2]
│   │   ├── platform_admin_store.go                 [NEW: PR-2]
│   │   ├── tenant_plan_store.go                    [NEW: PR-2]
│   │   └── audit_log_store.go                       [NEW: PR-2]
│   │   └── tenant_lifecycle_store.go               [NEW: PR-2]
│   │   └── tenant_auth_store.go                   [NEW: PR-2]
│   └── adapters/postgres/
│       ├── tenant_store.go                          [NEW: PR-3]
│       ├── tenant_admin_store.go                    [NEW: PR-3]
│       ├── platform_admin_store.go                  [NEW: PR-3]
│       ├── tenant_plan_store.go                      [NEW: PR-3]
│       └── audit_log_store.go                       [NEW: PR-3]
│       └── tenant_lifecycle_store.go               [NEW: PR-3]
│       └── tenant_auth_store.go                   [NEW: PR-3]
├── services/tenant-service/                         [NEW: PR-2 骨架, PR-3 实现]
│   ├── cmd/main.go
│   ├── handlers/
│   │   ├── tenant_handler.go
│   │   ├── tenant_admin_handler.go
│   │   ├── tenant_plan_handler.go
│   │   └── platform_admin_handler.go
│   ├── internal/service/
│   │   ├── tenant_service.go
│   │   ├── tenant_admin_service.go
│   │   ├── tenant_plan_service.go
│   │   └── platform_admin_service.go
│   └── internal/wiring/
│       └── wiring.go
├── frontends/boss/src/
│   ├── api/
│   │   ├── tenant.ts                                [NEW: PR-3]
│   │   ├── tenant_admin.ts                          [NEW: PR-3]
│   │   ├── tenant_plan.ts                           [NEW: PR-3]
│   │   ├── platform_admin.ts                        [NEW: PR-3]
│   │   ├── quota_meta.ts                            [NEW: PR-3]  (代理 Core QuotaMetaService API)
│   │   ├── tenant_quota.ts                          [NEW: PR-3]  (代理 Core QuotaService 配置 UI)
│   └── routes/_authenticated/
│       ├── tenants/
│       │   ├── index.tsx                            [NEW: PR-3]
│       │   ├── plans.tsx                            [NEW: PR-3]
│       │   ├── quota-meta.tsx                       [NEW: PR-3]  (平台配额元数据管理,platform-admin)
│       │   ├── $tenantId/
│       │   │   ├── edit.tsx                         [NEW: PR-3]
│       │   │   └── quota.tsx                        [NEW: PR-3]  (租户配额配置,代理 Core)
│       │   └── admins.tsx                           [NEW: PR-3]
│       └── settings/
│           └── platform-admins.tsx                  [NEW: PR-3]
└── services/tasks/modules/spec/boss/tenant/
    ├── spec-boss-tenant-list.md                     [NEW: PR-3]
    ├── spec-boss-tenant-create.md                  [NEW: PR-3]
    ├── spec-boss-tenant-plan.md                     [NEW: PR-3]
    ├── spec-boss-tenant-quota.md                   [NEW: PR-3]  (引用 Core 配额方案,只覆盖 UI 代理与初始化)
    ├── spec-boss-tenant-admin.md                   [NEW: PR-3]
    └── spec-boss-platform-admin.md                 [NEW: PR-3]
```

---

## §4 数据模型设计

### 4.1 Tenant 模块

#### 4.1.1 tenants — 扩展（恢复并补全字段）

在 `tenants` 表上保留并补全套餐绑定、状态机等字段。**MFA/SSO 字段不在 tenants 表存放**——由 `tenant_auth` 表独立承载（§4.1.2）。**配额字段不在 tenants 表存**——配额由 Core `resource_quota` 表独立承载（§4.3.2），本设计通过 Core `QuotaService` port 访问。

**重要：** tenants 表**不再保留 `plan_code` 列**——`plan_id` 是唯一引用套餐的字段；套餐的 `code` 仅在 `tenant_plans` 表自身使用，不进入 tenants。需要展示套餐代码时由 service 层 JOIN `tenant_plans` 读取。tenants 表也不再保留任何 `max_*` 配额字段——所有配额由 Core `resource_quota` 表承载。

```sql
-- 注意：现有 tenants 表（20260501000100_init_schema.sql SECTION 1）：
--   - 有 name TEXT NOT NULL UNIQUE，display_name TEXT NOT NULL
--   - 现有 status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted'))
--   - 旧数据需迁移：suspended → frozen、deleted → disabled
--   - 现有 name 列已有 UNIQUE 约束（NOT NULL UNIQUE）；本设计保留全局 UNIQUE 约束

ALTER TABLE tenants
  -- display_name 已在 init_schema 建好（NOT NULL），不重新添加；仅允许 NULL 的场景可通过单独迁移处理
  -- 租户联系邮箱（新增）：平台联系租户的商务/管理员邮箱，与首位管理员邮箱（users.email）不同
  ADD COLUMN IF NOT EXISTS contact_email TEXT,
  -- 套餐绑定（仅通过 tenant_plans.id 关联；不再保留 plan_code 冗余列；配额由 Core resource_quota 表承载）
  ADD COLUMN IF NOT EXISTS plan_id          UUID        NOT NULL
    REFERENCES tenant_plans(id) ON DELETE RESTRICT,
  -- 状态机（新列复用现有 status：先删旧 CHECK，ADD COLUMN 不会覆盖现有默认值约束，需单独 DROP CONSTRAINT）
  ADD COLUMN IF NOT EXISTS frozen_at        TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS disabled_at      TIMESTAMPTZ,
  -- 注意：MFA/SSO 字段不在 tenants 表存放，由 tenant_auth 表独立承载（§4.1.2）
  -- 注意：配额字段不在此处添加；由 Core resource_quota 表承载（§4.3.2）

-- 状态 CHECK：DROP 旧约束 + 数据迁移 + 重建新约束
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_status_check;   -- init_schema 中未命名，实际名为 tenants_status_check
-- 数据迁移：旧状态映射到新状态
UPDATE tenants SET status = 'frozen'   WHERE status = 'suspended';
UPDATE tenants SET status = 'disabled' WHERE status = 'deleted';
ALTER TABLE tenants ADD CONSTRAINT tenants_status_chk
  CHECK (status IN ('active', 'frozen', 'disabled'));

-- name 唯一性：保留现有全局 UNIQUE 约束（init_schema 中 UNIQUE 约束默认名 tenants_name_key）
-- name 格式约束：英文 slug 风格（小写字母数字 + 连字符，3-40 字符）
ALTER TABLE tenants ADD CONSTRAINT tenants_name_format_chk
  CHECK (name ~ '^[a-z0-9-]{3,40}$');

CREATE INDEX IF NOT EXISTS idx_tenants_status
  ON tenants (status);
```

**字段语义：**

| 字段 | 来源 | 谁能改 | 说明 |
|------|------|--------|------|
| `name` | init_schema（已有 NOT NULL UNIQUE，本迁移保留全局 UNIQUE 约束 + 格式 CHECK） | 不可改 | 租户英文 slug 风格的唯一 key（小写字母数字 + 连字符，3-40 字符）；全局唯一；不可改 |
| `display_name` | init_schema（NOT NULL） | platform-admin / platform-ops | 中文显示名；创建时必填，可改 |
| `contact_email` | 本迁移新增 | platform-admin / platform-ops | 租户联系邮箱（平台联系租户的商务/管理员邮箱）；与首位管理员邮箱（users.email）不同；创建时必填，可改 |
| `plan_id` | 创建时指定 | platform-admin / platform-ops | UUID 外键 → `tenant_plans.id`；切换套餐时改此字段；ON DELETE RESTRICT 保护套餐不被误删 |
| `status` / `frozen_*` / `disabled_*` | 各专端点 | platform-admin / platform-ops | 状态机字段；详见 §4.4 |

> MFA/SSO 字段不在 tenants 表存放，由 `tenant_auth` 表独立承载（§4.1.2）。配额字段不在 tenants 表存放。租户配额由 Core `resource_quota` 表承载（§4.3.2），配额维度由 Core `resource_quota_meta` 表注册（§4.3.1）。本设计通过 Core `QuotaService` / `QuotaMetaService` port 访问。Port 契约见 `通用资源配额与计量落地方案.md` §4.1 / §4.2。

#### 4.1.2 tenant_auth — 租户身份认证表（新增）

将租户级 MFA / SSO 配置从 `tenants` 表拆出，独立存放。一个租户一行，与 `tenants` 表 1:1 关系。

```sql
-- deploy/migrations/20260723_015_tenant_management.sql（同迁移文件内）
CREATE TABLE tenant_auth (
    tenant_id           UUID        PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    mfa_required        BOOLEAN     NOT NULL DEFAULT FALSE,   -- B2: 平台可强制该租户所有用户登录必须 MFA
    sso_enabled         BOOLEAN     NOT NULL DEFAULT FALSE,    -- B1: SSO 状态（TRUE=已开启，FALSE=已关闭）
    sso_provider        TEXT,                                  -- B1: （IdP 提供商；sso_enabled=FALSE 时仍保留以备重新开启）
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenant_auth_sso_enabled ON tenant_auth(sso_enabled) WHERE sso_enabled = TRUE;

-- RLS: 平台操作绕过 RLS（平台管理员可查所有租户认证配置）
ALTER TABLE tenant_auth ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_auth FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_auth_platform_bypass
  ON tenant_auth
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文只能看自己的认证配置（Console 自助场景）
CREATE POLICY tenant_auth_self_read
  ON tenant_auth
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

**设计要点：**

- **1:1 关系**：`tenant_id` 作为主键且外键 `ON DELETE CASCADE`，跟随租户删除。
- **创建时机**：`CreateTenant`（§6.3.1）事务内同步 `INSERT INTO tenant_auth (tenant_id) VALUES (...)`，使用全部默认值（`mfa_required=FALSE, sso_enabled=FALSE, sso_provider=NULL`）。SSO 详细配置（issuer_url / client_id / client_secret_ref / scopes / auto_provision / email_domains）不在 tenant_auth 表中存放，由外部系统（K8s Secret/ConfigMap）承载。
- **与 tenants 表解耦**：tenants 表不再存放 MFA/SSO 字段，认证配置变更不影响 tenants 行。
- **外部配置承载**：tenant_auth 仅记录 SSO 状态开关（`sso_enabled`）与提供商类型（`sso_provider`），SSO 详细配置由平台运维在 K8s Secret/ConfigMap 中维护，通过 `sso_provider` 标识提供商类型。
- **RLS**：平台管理员通过 `current_setting('app.current_tenant_id') IS NULL` 绕过；租户上下文只能读自己的认证配置。

#### 4.1.3 tenant_lifecycle — 租户生命周期记录表（新增）

记录租户状态变化的完整历史，与 `audit_logs` 互补：`audit_logs` 记录所有操作审计（含非状态变更操作如 reset_password），本表专注状态流转，便于按租户查询生命周期时间线。

```sql
-- deploy/migrations/20260723_015_tenant_management.sql（同迁移文件内）
CREATE TABLE tenant_lifecycle (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    action          TEXT        NOT NULL,           -- 当前状态/动作：active / frozen / disabled
    reason          TEXT,                           -- 变更原因（如"管理员手动冻结"）
    user_id         UUID,                           -- 操作者 user_id（系统触发时为 NULL）
    request_id      TEXT,                           -- 关联请求 ID（与 audit_logs.request_id 对应）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenant_lifecycle_tenant ON tenant_lifecycle(tenant_id, created_at DESC);
CREATE INDEX idx_tenant_lifecycle_action ON tenant_lifecycle(action, created_at DESC);

-- RLS: 平台操作绕过 RLS（平台管理员可查所有租户生命周期）
ALTER TABLE tenant_lifecycle ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_lifecycle FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_lifecycle_platform_bypass
  ON tenant_lifecycle
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文只能看自己的生命周期记录（Console 自助场景）
CREATE POLICY tenant_lifecycle_self_read
  ON tenant_lifecycle
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

**设计要点：**

- **一行一次状态变更**：每次租户状态转换时，在事务内同步写入一行，`action` 记录当前状态、原因、操作者。
- **创建时记录**：`CreateTenant` 时写入第一行，`action='active'`。
- **与 audit_logs 互补**：`audit_logs` 是全量操作审计（含 reset_password 等非状态变更），本表只记录状态流转，便于按租户快速查询生命周期时间线。
- **request_id 关联**：通过 `request_id` 与 `audit_logs` 关联，可交叉查询完整操作上下文。

**写入时机（与 §4.4 状态机转换表对应）：**

| action | reason 示例 | user_id |
|--------|------------|---------|
| active | "新建租户，套餐 pro" | platform-admin |
| frozen | "管理员手动冻结" | platform-admin |
| active | "管理员解冻" | platform-admin |
| disabled | "违规停用" | platform-admin |

#### 4.1.4 tenant_admins

**不新建表，不新增列**。完全复用现有 `users` + `roles` + `user_roles` 三张表（见 `20260501000100_init_schema.sql` SECTION 2）：

- 租户管理员 = `users.tenant_id IS NOT NULL` 且 `users.status='active'` 且通过 `user_roles` 关联到 `roles.tenant_id IS NULL AND roles.name='tenant-admin'` 的内置角色（seed 已存在）
- 租户普通成员 = `users.tenant_id IS NOT NULL` 且通过 `user_roles` 关联到 `roles.name='user'`
- 禁用/启用 = 直接改 `users.status`（`active` / `disabled`），无需新增 `status_in_tenant` 字段
- 角色（admin / member）= 通过 `user_roles` 增删角色行实现，无需新增 `role_in_tenant` 字段

> **迁移注意：**
> 1. `users` 表新增软删除字段：
> ```sql
> ALTER TABLE users ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE;
> ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;
> ```
> 2. `users` 表新增显示名字段：
> ```sql
> ALTER TABLE users ADD COLUMN display_name TEXT;
> ```

**复用的现有字段：**

| 字段 | 表 | 含义 |
|------|------|------|
| id | users | 用户 ID |
| tenant_id | users | 租户 ID（NOT NULL） |
| username | users | 用户名（UNIQUE per tenant） |
| email | users | 邮箱（UNIQUE per tenant） |
| password_hash | users | bcrypt（OIDC 用户可为 NULL） |
| status | users | `active` / `disabled` |
| last_login_at | users | 最近登录 |
| created_at / updated_at | users | 时间戳 |

**角色判定逻辑（service 层封装）：**

```sql
-- 判断某用户是否为租户管理员
SELECT EXISTS(
  SELECT 1 FROM user_roles ur
  JOIN roles r ON r.id = ur.role_id
  WHERE ur.user_id = $1 AND r.tenant_id IS NULL AND r.name = 'tenant-admin'
) AS is_admin;

-- 列出某租户的管理员
SELECT u.* FROM users u
JOIN user_roles ur ON ur.user_id = u.id
JOIN roles r ON r.id = ur.role_id
WHERE u.tenant_id = $1 AND u.status = 'active'
  AND r.tenant_id IS NULL AND r.name = 'tenant-admin';

-- 升级为 admin（若尚未绑定）
INSERT INTO user_roles (user_id, role_id)
SELECT $1, r.id FROM roles r
WHERE r.tenant_id IS NULL AND r.name = 'tenant-admin'
ON CONFLICT DO NOTHING;

-- 降级为 member（解绑 admin，绑定 user）
DELETE FROM user_roles ur
WHERE ur.user_id = $1
  AND ur.role_id = (SELECT id FROM roles WHERE tenant_id IS NULL AND name = 'tenant-admin');
INSERT INTO user_roles (user_id, role_id)
SELECT $1, r.id FROM roles r
WHERE r.tenant_id IS NULL AND r.name = 'user'
ON CONFLICT DO NOTHING;
```

**新增 seed 角色补强（可选，仅当现有 `tenant-admin` / `user` 不够用时）：**

```sql
-- 现有 seed 已含 platform-admin / tenant-admin / user / auditor（见 001 SECTION 12）
-- 新增 tenant-owner 租户所有者角色；同时更新现有 tenant-admin / user / auditor 的 permissions 为 4 维度模型
-- permissions 4 个维度：
--   compute:   算力实例（创建/管理/删除 GPU 实例、推理服务）
--   inference:  推理/模型（模型管理、推理调用、知识库管理）
--   member:    成员邀请（邀请/管理租户成员、重置密码、禁用/启用）
--   transfer:   所有者移交（转移所有者身份给其他成员）
-- 权限值：read=只读、write=读写、none=无权限

-- 新增租户所有者角色（每个租户仅一人）
INSERT INTO roles (id, tenant_id, name, permissions) VALUES
  (gen_random_uuid(), NULL, 'tenant-owner',
   '{"compute":"write","inference":"write","member":"write","transfer":"write"}')
ON CONFLICT (name, tenant_id) WHERE tenant_id IS NULL DO NOTHING;

-- 更新现有租户角色的 permissions 为 4 维度模型
UPDATE roles SET permissions =
  CASE name
    WHEN 'tenant-admin' THEN '{"compute":"write","inference":"write","member":"write","transfer":"none"}'
    WHEN 'user'          THEN '{"compute":"read","inference":"write","member":"none","transfer":"none"}'
    WHEN 'auditor'       THEN '{"compute":"read","inference":"read","member":"none","transfer":"none"}'
  END
WHERE tenant_id IS NULL AND name IN ('tenant-admin', 'user', 'auditor');
```

**租户角色权限矩阵：**

| 角色 | 算力实例 (compute) | 推理/模型 (inference) | 成员邀请 (member) | 所有者移交 (transfer) |
|------|-------|-------|-------|-------|
| tenant-owner | write | write | write | write |
| tenant-admin | write | write | write | none |
| user | read | write | none | none |
| auditor | read | read | none | none |

**设计要点：**

- **tenant-owner**：每个租户仅一人，由 `CreateTenant` 时自动绑定首位管理员为所有者；可通过 `transfer` 权限移交给其他成员。
- **权限校验**：service 层在执行操作前检查当前用户 `roles.permissions` 对应维度的权限值（`write` > `read` > `none`）。

**租户管理员操作：**

| 操作 | 实现 | 幂等性 |
|------|------|--------|
| 邀请 | `POST /api/v1/svc/tenants/{tenantId}/admins/invite`（新建 `tenant_admin_invitation` 记录 `status='inviting'`，不改 `users.status`/角色，返回 `{id, token, expire_at, message}`；§5.4.1） | idempotency_key 支持 |
| 重发邀请 | `POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/invitation/resend`（重新生成 token、刷新 expire_at=now()+72h、清空 accepted_at/rejected_at、状态回归 inviting；仅 inviting/expired 可重发；§5.4.2） | idempotency_key 支持 |
| 列表 | `GET /api/v1/svc/tenants/{tenantId}/admins`（JOIN user_roles + roles 过滤 tenant-admin；§5.2.7） | 幂等 |
| 查看详细 | `GET /api/v1/svc/tenants/{tenantId}/admins/{userId}` | 幂等 |
| 修改权限 | `PUT /api/v1/svc/tenants/{tenantId}/admins/{userId}/role`（改 user_roles 绑定：admin ↔ user / auditor / tenant-admin；tenantId 和 userId 在路径参数；tenant-owner 不可被修改角色） | idempotency_key 支持 |
| 重置密码 | `POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/reset-password`（tenantId 和 userId 在路径参数） | idempotency_key 支持 |
| 禁用 | `POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/disable`（改 `users.status='disabled'`；tenantId 和 userId 在路径参数；最后活跃 tenant-owner 不可禁用 → 422 `LAST_TENANT_OWNER`） | idempotency_key 支持 |
| 启用 | `POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/enable`（改 `users.status='active'`；tenantId 和 userId 在路径参数） | idempotency_key 支持 |
| 删除 | `DELETE /api/v1/svc/tenants/{tenantId}/admins/{userId}`（软删除，`users.is_deleted=TRUE` + `deleted_at` + `status='disabled'`；tenant-owner 不可删，最后活跃 tenant-owner → 422 `LAST_TENANT_OWNER`） | **不幂等（DELETE 不做幂等，无需 idempotency_key）** |
| 跨租户查询 | `GET /api/v1/svc/tenant-admins`（分页，含租户对象；**仅返回租户所有者、租户管理员与正在被邀请的用户**，不返回普通成员 user；邀请中用户 `is_inviting=true`，仅作标记，不改变该用户的 role/status） | 幂等 |
| 查询管理员权限 | `GET /api/v1/svc/tenants/{tenantId}/admins/{userId}/role`（与 PUT role 同路径不同方法） | 幂等 |
| 移交所有者 | `POST /api/v1/svc/tenants/{tenantId}/transfer-ownership`（target_user_id 在 body；owner 唯一且不能没有，但可移交——目标接任后仍唯一） | idempotency_key 支持 |
| 操作历史 | `GET /api/v1/svc/tenants/{tenantId}/admins/{userId}/audit-logs` | 幂等 |

**密码处理：** bcrypt cost=12；密码由调用方提供，不存明文；创建和重置密码时同样。

#### 4.1.4b tenant_admin_invitation — 租户管理员邀请表（新增）

记录租户管理员**邀请**的独立状态，替代原「复用 `users.status='invited'`」的做法。邀请状态不写入 `users.status`（`users.status` 仅保留 `active` / `disabled` 两态），邀请生命周期完全由本表承载。

**状态机（status）：** `inviting`（邀请中）→ `accepted`（同意）/ `rejected`（拒绝）/ `expired`（过期）

| status 值 | 含义 | 可跳转 |
|-----------|------|--------|
| `inviting` | 邀请中，待被邀请人接受 | accepted / rejected / expired |
| `accepted` | 已同意（绑定 tenant-admin 角色并激活） | — 终态 |
| `rejected` | 已拒绝（不绑定角色） | — 终态 |
| `expired` | 已过期（超过 expire_at 仍未处理） | — 终态 |

**建表 DDL（PostgreSQL，TTL 由应用层计算）：**

> **类型说明：** 原拟 `bigint / auto_increment` 因与 `tenants.id`、`users.id`（均为 UUID 主键）不兼容，无法建立外键，已改为 UUID，与项目现有 schema 一致。

```sql
CREATE TABLE tenant_admin_invitation (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,                          -- SHA-256(原始 token)，不存明文 token
    status       TEXT NOT NULL                           -- inviting | accepted | rejected | expired
        CHECK (status IN ('inviting', 'accepted', 'rejected', 'expired')),
    expire_at    TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at  TIMESTAMPTZ,
    rejected_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX uk_tenant_admin_invitation_token_hash ON tenant_admin_invitation(token_hash);
CREATE INDEX idx_tenant_admin_invitation_user_status ON tenant_admin_invitation(user_id, status);
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | UUID | 是 | 主键，`gen_random_uuid()` |
| tenant_id | UUID | 是 | 被邀请用户所属租户；FK → `tenants(id)` ON DELETE CASCADE |
| user_id | UUID | 是 | 被邀请用户；FK → `users(id)` ON DELETE CASCADE |
| token_hash | TEXT | 是 | 邀请 token 的 SHA-256 哈希（64 位十六进制）；`UNIQUE`；不存原始 token |
| status | TEXT | 是 | `inviting` / `accepted` / `rejected` / `expired`，CHECK 约束 |
| expire_at | TIMESTAMPTZ | 是 | 过期时间；超过后状态应置为 `expired` |
| created_at | TIMESTAMPTZ | 是 | 创建时间，`DEFAULT NOW()` |
| accepted_at | TIMESTAMPTZ | 否 | 同意时间（status=accepted 时写入） |
| rejected_at | TIMESTAMPTZ | 否 | 拒绝时间（status=rejected 时写入） |

**设计要点：**

- **外键**：`tenant_id` → `tenants(id)`，`user_id` → `users(id)`，均 `ON DELETE CASCADE`（随租户/用户删除一并清理邀请记录）。
- **token 安全**：仅落库 `token_hash`（SHA-256），原始邀请链接 token 只在催发响应/通知中一次性返回，用于接受请求时校验。
- **一次性使用**：仅 `status='inviting'` 且 `expire_at > now()` 的邀请可被接受；接受/拒绝后为终态，不能再变更。
- **并发唯一**：`UNIQUE (token_hash)` 防 token 重复；接受时需 `UPDATE ... WHERE id=$1 AND status='inviting'` 原子判断，条件不满足即为冲突/已处理。
- **过期处理**：读取/接受时可惰性判定 `now() > expire_at` 置为 `expired`；或由定时任务扫描批量翻转。过期不可再接受。

#### 4.1.5 platform_admins

**不新建表，不新增列**。完全复用 `users` 表 + `tenant_id IS NULL`（见 `20260707001400_platform_users.sql`）。

- 平台账号 = `users.tenant_id IS NULL`（全局唯一 username / email，见 `idx_users_platform_username` / `idx_users_platform_email`）
- 平台角色 = 通过 `user_roles` 关联到 `roles.tenant_id IS NULL AND roles.name IN ('platform-admin', 'platform-ops', 'platform-readonly')`
- 禁用/启用 = 改 `users.status`（同 §4.1.4）
- 改角色 = 改 `user_roles` 绑定（先 DELETE 旧平台角色行，再 INSERT 新平台角色行）

**新增 seed 平台角色：**

```sql
-- 现有 seed 已含 platform-admin（permissions='["*"]'，见 001 SECTION 12）
-- 本设计新增 platform-ops / platform-readonly 两个平台角色（tenant_id IS NULL）
-- 平台 permissions 4 个维度：
--   tenant_ops:    租户开通/冻结（创建/冻结/解冻/禁用/删除租户）
--   resource_pool: 平台资源池（配额元数据管理、租户配额配置）
--   platform_user:  平台运营账号（CRUD、重置密码、启用/禁用）
--   audit_export:  审计导出（导出审计日志）
-- 权限值：read=只读、write=读写、none=无权限
INSERT INTO roles (id, tenant_id, name, permissions) VALUES
  (gen_random_uuid(), NULL, 'platform-ops',
   '{"tenant_ops":"write","resource_pool":"write","platform_user":"none","audit_export":"none"}'),
  (gen_random_uuid(), NULL, 'platform-readonly',
   '{"tenant_ops":"read","resource_pool":"read","platform_user":"none","audit_export":"read"}')
ON CONFLICT (name, tenant_id) WHERE tenant_id IS NULL DO NOTHING;
```

**平台角色权限矩阵：**

| 角色 | 租户开通/冻结 (tenant_ops) | 平台资源池 (resource_pool) | 平台运营账号 (platform_user) | 审计导出 (audit_export) | 说明 |
|------|-------|-------|-------|-------|------|
| platform-admin | write | write | write | write | 超级管理员，permissions=`["*"]`，不受 4 维度约束 |
| platform-ops | write | write | none | none | 运维管理员，可管理租户与资源池 |
| platform-readonly | read | read | none | read | 只读角色，可查看租户、资源池和审计 |

**设计要点：**

- **platform-admin** 拥有 `["*"]` 超级权限，不受 4 维度模型约束。
- **platform-ops / platform-readonly** 的 permissions 仅为声明性元数据，实际权限由 Gateway AuthZ 中间件按角色名硬编码校验。
- 平台权限维度与租户权限维度（compute / inference / member / transfer）独立，平台角色不使用租户维度。

**平台账号操作：**

| 操作 | API | 权限要求 | 备注 |
|------|-----|---------|------|
| 列表 | `GET /api/v1/svc/platform-admins`（JOIN user_roles + roles，按平台角色过滤） | platform-admin | 分页、按 role 过滤 |
| 创建 | `POST /api/v1/svc/platform-admins`（建 user + 绑定平台角色） | platform-admin | 必须指定 role 之一 |
| 查看详细 | `GET /api/v1/svc/platform-admins/{userId}` | platform-admin | |
| 修改 | `PUT /api/v1/svc/platform-admins/{userId}/role`（改 user_roles 平台角色绑定；userId 在路径参数） | platform-admin | 仅修改 role |
| 重置密码 | `POST /api/v1/svc/platform-admins/{userId}/reset-password`（userId 在路径参数） | platform-admin | |
| 禁用 | `POST /api/v1/svc/platform-admins/{userId}/disable`（userId 在路径参数） | platform-admin | 改 `users.status='disabled'` |
| 启用 | `POST /api/v1/svc/platform-admins/{userId}/enable`（userId 在路径参数） | platform-admin | 改 `users.status='active'` |
| 删除 | `DELETE /api/v1/svc/platform-admins/{userId}` | platform-admin | 软删除 |

**最后管理员保护：**

- 删除 / 禁用前查询活跃 `platform-admin` 数量（JOIN `user_roles` + `roles` 过滤 `platform-admin` + `users.status='active'`）
- 若为 1 且即将被删除/禁用 → 返回 422 `LAST_PLATFORM_ADMIN`
- 若为 0（异常状态）→ 同样 422，要求先创建

#### 4.1.6 tenant_quota_change — 租户配额变更申请表（新增）

记录租户配额变更申请的审批流程。一个申请对应一个租户的一个配额维度。

```sql
-- deploy/migrations/20260723_015_tenant_management.sql（同迁移文件内）
CREATE TABLE tenant_quota_change (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type   TEXT        NOT NULL,                    -- 配额维度（如 storage_gb / token_count / kb_query_count / member_count / inference_service_count）
    old_value       BIGINT,                                  -- 变更前配额值（NULL=首次设置）
    new_value       BIGINT      NOT NULL,                    -- 申请变更为的配额值
    requested_by    UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,  -- 申请人 user_id
    status          TEXT        NOT NULL DEFAULT 'pending',  -- pending / approved / rejected
    reviewed_by     UUID        REFERENCES users(id) ON DELETE RESTRICT,           -- 审核人 user_id（NULL=未审核）
    reviewed_at     TIMESTAMPTZ,                             -- 审核时间（NULL=未审核）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_quota_change_status CHECK (status IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_tenant_quota_change_tenant ON tenant_quota_change(tenant_id);
CREATE INDEX idx_tenant_quota_change_status ON tenant_quota_change(status);
CREATE INDEX idx_tenant_quota_change_requested_by ON tenant_quota_change(requested_by);

-- RLS: 平台操作绕过 RLS（平台管理员可查所有租户的配额变更申请）
ALTER TABLE tenant_quota_change ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_quota_change FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_quota_change_platform_bypass
  ON tenant_quota_change
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文只能看自己的配额变更申请（Console 自助场景）
CREATE POLICY tenant_quota_change_self_read
  ON tenant_quota_change
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

**设计要点：**

- **审批流程**：申请创建时 `status='pending'`；平台管理员审核后置为 `approved` 或 `rejected`；`approved` 时 service 层调用 Core API 实际修改 `resource_quota.total`。
- **old_value 记录**：申请创建时从 Core `resource_quota` 表读取当前 `total` 值并冻结到 `old_value`，作为变更前快照。
- **resource_type**：引用 Core `resource_quota_meta.resource_type`，但本表不建外键（Core 表与 BOSS 表跨库）。
- **RLS**：租户只能看自己的申请；平台管理员可查所有。

### 4.2 TenantPlan 模块

#### 4.2.1 tenant_plans — 套餐模板表（新增）

集中存放套餐模板信息。**配额上限不在本表存放**——配额维度由 Core `resource_quota_meta` 注册表统一管理（§4.3.1），租户配额由 Core `resource_quota` 表承载（§4.3.2）。套餐各维度的配额上限由 `plan_quota_limits` 表（§4.2.2）关联承载。本表的套餐模板字段仅用于：
- 套餐模板不再直接承载"租户配额上限"职责；创建租户时由 tenant-service 读取 `plan_quota_limits` + `resource_quota_meta` 计算有效限额，调用 Core API 逐维度初始化完成（§6.3.1）

> 计费单价字段暂不实现，后续 PR 再补充。

```sql
CREATE TABLE tenant_plans (
  id                              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code                            TEXT        NOT NULL,                       -- starter / pro / enterprise（软删除后可复用）
  name                            TEXT        NOT NULL,
  description                     TEXT,                                      -- 套餐描述（可空）
  status                          TEXT        NOT NULL DEFAULT 'draft'
                                    CHECK (status IN ('draft', 'active', 'disabled')),
  is_deleted                      BOOLEAN     NOT NULL DEFAULT FALSE,         -- 逻辑删除标记
  deleted_at                      TIMESTAMPTZ,                                -- 删除时间（NULL=未删除）
  created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()          -- 更新时间
);

-- code 唯一约束仅对未删除的套餐生效（软删除后 code 可被新套餐复用）
CREATE UNIQUE INDEX idx_tenant_plans_code_active ON tenant_plans(code) WHERE is_deleted = FALSE;
```

#### 4.2.2 plan_quota_limits — 套餐限额模板表（新增，归属 tenant-service 迁移）

套餐模板中各维度的配额上限。每行一个维度。表归属 tenant-service 迁移文件 `20260810000200_tenant_plan_management.sql`，通过 `plan_id` 外键关联 `tenant_plans`，通过 `resource_type` 外键关联 Core 层 `resource_quota_meta`。

```sql
-- deploy/migrations/20260810000200_tenant_plan_management.sql
CREATE TABLE plan_quota_limits (
    plan_id        UUID   NOT NULL REFERENCES tenant_plans(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total    BIGINT,          -- Create/PUT 写入物化为具体值；历史行可能仍为 NULL
    CHECK (total IS NULL OR total >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, resource_type)
);

-- 注：plan_id 为复合主键前导列，无需额外创建 idx_plan_quota_limits_plan 索引

-- 平台治理表，不加 RLS（跨租户可见，由应用层 RBAC 控制写权限）
```

**设计要点：**

- **一行一维度**：`PRIMARY KEY (plan_id, resource_type)`，新增维度只需在 `resource_quota_meta` 注册后，套餐中加一行即可，无需改表结构。
- **写入物化（实现对齐 2026-08-14）**：请求 `total: null` 时，tenant-service 用 Core `default_quota` **物化为具体数值再落库**（不保留 NULL）。列仍允许 NULL 以兼容历史行；GET `buildQuotaLimitViews` 对历史 NULL 兜底并可回写。后续改 Core `default_quota` **不会**自动改变已物化套餐行。
- **跨层外键**：`plan_id` 引用 Services 层 `tenant_plans.id`，`resource_type` 引用 Core 层 `resource_quota_meta.resource_type`。表归属 tenant-service 迁移文件，与 `tenant_plans` 同文件维护。
- **软删除保留**：套餐删除走软删除（UPDATE is_deleted=TRUE），不触发 ON DELETE CASCADE，限额行随套餐行保留。
- **不加 RLS**：平台治理数据，跨租户可见，写权限由应用层 RBAC 限制为 platform-admin。
- **DB 权限**：迁移文件中对 `tenant_plans` 和 `plan_quota_limits` 执行 `GRANT SELECT, INSERT, UPDATE, DELETE ON ... TO ani_app_user`（tenant-service 以 `ani_app_user` 连接 DB）。

#### 4.2.3 starter 入门套餐（迁移内置 seed）

迁移文件 `20260810000200_tenant_plan_management.sql` 内置一条固定 UUID 的入门套餐，作为存量租户的默认套餐：

- **固定 UUID**：`00000000-0000-0000-0000-000000000001`（幂等插入，冲突则跳过）
- **字段**：code=`starter`，name=`入门版`，status=`active`，is_deleted=FALSE
- **8 维度限额**：gpu_count=2 / cpu_core=4 / memory_gb=16 / storage_gb=32 / token_count=500000 / kb_query_count=1000 / member_count=5 / inference_service_count=3（均物化为具体值，幂等插入）
- **用途**：迁移中先将 `tenants.plan_id` 加为可空列，插入 starter 套餐，回填所有 `plan_id IS NULL` 的存量租户指向 starter，最后收紧 `plan_id` 为 NOT NULL

### 4.3 Quota 模块

#### 4.3.1 resource_quota_meta — 配额元数据注册表（建表 + 8 维度 seed）

平台管理员维护的可限额资源注册表。配额与计量共用。**平台管理员专用表，不加 RLS**（跨租户平台治理数据）；`enabled=false` 的维度，Try 时拒绝。

```sql
-- deploy/migrations/20260810000100_resource_quota.sql（与 §4.3.2 / §4.3.3 合并为单文件）
CREATE TABLE resource_quota_meta (
    resource_type     TEXT PRIMARY KEY,   -- 'gpu_count' | 'cpu_core' | 'memory_gb' | 'storage_gb' | 'token_count' | 'kb_query_count' | 'member_count' | 'inference_service_count'
    display_name      TEXT NOT NULL,       -- 'GPU 份数' | 'CPU 核数'
    unit              TEXT NOT NULL,       -- 'share' | 'core' | 'gb' | 'token' | 'count'
    is_discrete       BOOLEAN NOT NULL DEFAULT TRUE,
    default_quota     BIGINT NOT NULL,    -- 租户未显式配置时的默认上限（只影响首次建行）
    collector_id      TEXT,                -- 'prometheus_dcgm' | 'prometheus_kubelet' | 'inference_token' | NULL（无采集）
    description       TEXT,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 初始 seed（8 维度，统一在同一迁移文件中创建）
INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, collector_id, description) VALUES
  ('gpu_count',              'GPU 份数',     '份', true,  8,        'prometheus_dcgm',    '单租户可持有的 GPU 份数上限'),
  ('cpu_core',               'CPU 核数',     '核',  true,  8,       'prometheus_kubelet', '单租户可占用的 CPU 核数上限'),
  ('memory_gb',              '内存 GB',      'gb',    true,  32,      'prometheus_kubelet', '单租户可占用的内存 GB 上限'),
  ('storage_gb',             '存储 GB',      'gb',    true,  64,     NULL,                  '单租户可占用的存储 GB 上限'),
  ('token_count',            'Token 数',     'token', true, 1000000,  'inference_token',     '单租户可消耗的 Token 总量上限'),
  ('kb_query_count',         'KB 查询次数',  '次', true, 10000,    NULL,                  '单租户知识库查询次数上限'),
  ('member_count',           '成员上限',     '人', true,  20,       NULL,                  '单租户可邀请的成员数量上限'),
  ('inference_service_count','推理服务上限', '个', true,  10,       NULL,                  '单租户可创建的推理服务数量上限')
ON CONFLICT (resource_type) DO NOTHING;
```

> **维度非固定**：以上 8 个维度是现阶段初始 seed，后续可通过 `QuotaMetaService.Register` 动态增减，无需改表结构或代码。`plan_quota_limits`（§4.2.2）与 `resource_quota`（§4.3.2）均通过 `resource_type` 外键引用 meta，自动跟随。
>
> `resource_quota_meta` 表结构定义迁入本文档（原 `通用资源配额与计量落地方案.md` §3.1）。

#### 4.3.2 resource_quota — 配额配置 + 运行时账本（建表 SQL 迁入本文档）

租户上限（`total`，平台管理员配置）+ 运行时占用（`reserved/used`，Try/Confirm/Cancel 维护），合一行。外键引用 meta：配置必须引用已注册资源（DB 约束保证语义）。**RLS**：租户只能看自己的配额行。

```sql
-- deploy/migrations/20260810000100_resource_quota.sql（与 §4.3.1 / §4.3.3 合并为单文件）
CREATE TABLE resource_quota (
    tenant_id      UUID   NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT NOT NULL DEFAULT 0,  -- 租户上限（平台管理员配置）
    reserved       BIGINT NOT NULL DEFAULT 0,  -- 运行时预占
    used           BIGINT NOT NULL DEFAULT 0,  -- 运行时实扣
    CHECK (total >= 0 AND reserved >= 0 AND used >= 0),
    CHECK (reserved + used <= total),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);

-- RLS: 平台操作绕过 RLS（平台管理员可查所有租户配额）
ALTER TABLE resource_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_quota FORCE ROW LEVEL SECURITY;
-- 平台操作：未设 tenant_id 上下文时放行所有行（管理员场景）
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文：只能操作自己的行（SELECT + INSERT + UPDATE + DELETE）
-- FOR ALL + USING = WITH CHECK，保证租户只能写自己的行、不能 INSERT 别人的行
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

> `resource_quota` 表结构定义迁入本文档（原 `通用资源配额与计量落地方案.md` §3.2）。核心设计：`total`（上限）与 `reserved/used`（占用）在同一行，避免"配置改了但占用表没同步"的一致性问题。

#### 4.3.3 resource_reservations — TCC 配额流水（建表 SQL 迁入本文档）

幂等守卫 + 跨事务关联 + TTL 回收依据。承担四职责：① Confirm/Cancel 幂等状态守卫 ② tx_id↔tenant/amount 关联 ③ TTL 孤儿回收 ④ 审计追溯。**RLS**：租户只能看自己的预占流水。

```sql
-- deploy/migrations/20260810000100_resource_quota.sql（与 §4.3.1 / §4.3.2 合并为单文件）
CREATE TABLE resource_reservations (
    tx_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL REFERENCES resource_quota_meta(resource_type),
    amount        BIGINT NOT NULL CHECK (amount > 0),
    state         TEXT NOT NULL DEFAULT 'reserved'
        CHECK (state IN ('reserved','confirmed','cancelled','expired','released')),
    resource_ref  TEXT,                    -- instance_id / model_id（通用资源承载者）
    expires_at    TIMESTAMPTZ NOT NULL,     -- Try 时设定，默认 now()+10min
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_res_state_expires
    ON resource_reservations(state, expires_at) WHERE state = 'reserved';
CREATE INDEX idx_res_tenant
    ON resource_reservations(tenant_id, state);

-- RLS: 平台操作绕过 RLS（平台管理员可查所有预占流水）
ALTER TABLE resource_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservations FORCE ROW LEVEL SECURITY;
-- 平台操作：未设 tenant_id 上下文时放行所有行（管理员场景）
CREATE POLICY resource_reservations_platform_bypass
  ON resource_reservations FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文：只能操作自己的行（SELECT + INSERT + UPDATE + DELETE）
CREATE POLICY resource_reservations_self
  ON resource_reservations FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

> `resource_reservations` 表结构定义迁入本文档（原 `通用资源配额与计量落地方案.md` §3.3）。该表是 TCC 幂等与跨事务关联的唯一可靠载体——删掉它会把幂等保证从 DB 状态机降级为调用方约定，在 reconciler 周期重入 + NATS 至少一次投递的现实下会出竞态（重复实扣、Cancel 找不到原始 tenant/amount、TTL 回收无依据）。

#### 4.3.4 应用用户权限分配

为普通应用用户 `ani_app_user` 分配配额相关表的读写权限：

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_reservations TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota_meta TO ani_app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ani_app_user;
```

### 4.4 状态机

**三种状态：**

| 状态 | 含义 | 登录 | 资源操作 | 实例 |
|------|------|------|---------|------|
| **active** | 活跃 | 可登录 | 全部操作 | 正常运行 |
| **frozen** | 冻结（资源保持原状，无法登录，无法创建） | 禁止登录 | 无（禁止登录） | 已创建实例继续运行 |
| **disabled** | 禁用（资源删除，无法登录/查看/创建，不可逆） | 禁止登录 | 无 | 资源停止运行 |

```
       ┌─────────────┐
       │   active    │
       │   活跃      │
       └──────┬──────┘
              │ 管理员手动冻结
              ▼
       ┌─────────────┐
       │   frozen    │
       │   冻结      │
       └──────┬──────┘
              │ 管理员手动禁用
              ▼
       ┌─────────────┐
       │  disabled   │
       │   禁用      │
       └─────────────┘
```

| 转换 | 触发 | 谁能触发 | 行为说明 |
|------|------|---------|---------|
| active → frozen | 管理员手动冻结 | platform-admin / platform-ops | 设置 frozen_at；禁止登录、实例继续运行 |
| frozen → active | 管理员解冻 | platform-admin / platform-ops | 清空 frozen_at，恢复全部操作 |
| active → disabled | 管理员手动禁用 | platform-admin / platform-ops | 设置 disabled_at；禁止登录、资源强制删除 |
| frozen → disabled | 管理员手动禁用 | platform-admin / platform-ops | 同上 |

**冻结 vs 禁用核心区别：**

| 维度 | 冻结 (frozen) | 禁用 (disabled) |
|------|-------------|---------------|
| 触发方 | 管理员手动 | 管理员手动 |
| 登录 | 禁止 | 禁止 |
| 资源操作 | 无（禁止登录） | 无 |
| 实例 | 继续运行 | 停止运行 |
| 恢复后 | 解冻恢复全功能 | 不可逆（终态） |

### 4.5 audit_logs 表（复用现有分区表）

**不新建表。** 完全复用 `20260501000100_init_schema.sql` SECTION 10 的 `audit_logs`（按月 RANGE 分区）。

**现有 schema（见 `20260501000100_init_schema.sql:500-522`）：**

```sql
-- 已存在，本设计不修改
CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,                       -- NULL = 平台操作
    user_id     UUID,                        -- 操作者
    request_id  TEXT NOT NULL,
    action      TEXT NOT NULL,               -- 如 'tenant.create'
    resource    TEXT NOT NULL,               -- 目标资源类型
    result      TEXT NOT NULL,               -- 'success' / 'failure'
    details     JSONB NOT NULL DEFAULT '{}', -- 含 target_id, before, after 等
    ip_address  TEXT,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);
```

**写入映射：** 本设计的审计字段映射到现有 schema：

| 本设计概念 | 现有 audit_logs 字段 | 说明 |
|------------|---------------------|------|
| actor_user_id | `user_id` | 操作者 |
| actor_tenant_id | `tenant_id` | NULL 表示平台操作 |
| action | `action` | 如 `tenant.create` |
| target_type | `resource` | 如 `tenant` / `tenant_admin` / `platform_admin` |
| target_id | `details.target_id` | 放入 details JSONB |
| target_tenant_id | `details.target_tenant_id` | 放入 details JSONB |
| audit_note | `details.note` | 放入 details JSONB |
| request_id | `request_id` | 现有字段 |
| idempotency_key | `details.idempotency_key` | 放入 details JSONB |
| payload | `details.payload` | 放入 details JSONB |
| - | `result` | `'success'`（失败回滚不记录） |
| - | `ip_address` / `user_agent` | 由 Gateway 中间件透传 |

**action 命名规范：** `<domain>.<verb>`（动词可为复合词，采用小写 + 下划线），如 `tenant.create`、`tenant.freeze`、`tenant.unfreeze`、`tenant.disable`、`tenant_admin.reset_password`、`tenant_admin.resend_invitation`、`tenant_admin.transfer_ownership`、`platform_admin.create`。

**写入时机：** 所有写操作成功后，在事务内同步写入；失败则回滚（不记录失败尝试）。

**分区维护：** 现有 `audit_logs_2026_05` / `audit_logs_2026_06` 分区已建；后续分区由独立维护任务负责，不在本设计范围。

### 4.6 幂等性（复用 Gateway Redis 中间件）

**不新建表，不进 `pkg/ports`。** 完全复用现有 `repo/services/ani-gateway/internal/middleware/idempotency.go` 的 `Idempotency(store)` 中间件。

**现有实现（见 `idempotency.go:30-85`）：**

- 适用方法：`POST` / `PUT`（`idempotencyApplies`；**不包含 `DELETE`**）
- 幂等键来源：`Idempotency-Key` 请求头，fallback 到 body 的 `idempotency_key` 字段
- 缓存 key：`idempotency:{scope}:{tenant_id}:{method}:{path}:{sha256(Idempotency-Key)}`
- 存储：Redis（通过 `GatewayStore` 的 `SetNX` / `Get` / `Set` / `Delete`）
- TTL：24 小时
- 状态：`processing`（SETNX 抢占）→ `completed`（请求完成后 SET 覆盖）
- 同 key 重复请求：返回缓存的响应体 + `Idempotent-Replay: true` 头
- 同 key 但请求已在处理中：409 `IDEMPOTENCY_IN_PROGRESS`
- 存储不可用：503 `IDEMPOTENCY_UNAVAILABLE`（fail-open 策略由 store 决定）

**键格式：** UUID，由客户端生成。

**租户管理的幂等行为：**

| 端点 | 幂等键作用域 | 说明 |
|------|------------|------|
| `POST /api/v1/svc/tenants` | platform + path | 创建租户；重复请求回放原响应 |
| `POST /api/v1/svc/tenants/{tenantId}/freeze` | platform + path | 冻结操作 |
| `POST /api/v1/svc/tenants/{tenantId}/disable` | platform + path | 禁用操作 |
| `POST /api/v1/svc/platform-admins` | platform + path | 创建平台管理员 |
| `POST /tenants/{tenantId}/admins/invite` | platform + path | 邀请管理员（§5.4.1） |
| `POST /tenants/{tenantId}/admins/{userId}/invitation/resend` | platform + path | 重发邀请（§5.4.2） |
| `PUT /tenants/{tenantId}/admins/{userId}/role` | platform + path | 修改管理员权限（§5.4.6，PUT 已纳入 idempotencyApplies） |
| `POST /tenants/{tenantId}/admins/{userId}/reset-password` | platform + path | 重置密码（§5.4.8） |
| `POST /tenants/{tenantId}/admins/{userId}/disable` | platform + path | 禁用管理员（§5.4.9） |
| `POST /tenants/{tenantId}/admins/{userId}/enable` | platform + path | 启用管理员（§5.4.10） |
| `POST /tenants/{tenantId}/transfer-ownership` | platform + path | 移交所有者（§5.4.7） |
| `DELETE /tenants/{tenantId}/admins/{userId}` | — | **不幂等**：删除为软删除，重复调用按幂等键回放无意义；客户端不加 Idempotency-Key |
| 其它 POST/PUT | 同上 | 通用规则 |

**tenant-service 不实现自己的幂等存储**：handler 从 `Idempotency-Key` 头读取并透传至 service 层仅用于审计日志记录（`audit_logs.details.idempotency_key`），真正的去重由 Gateway 完成。

### 4.7 RLS 策略

```sql
-- 平台操作绕过 RLS
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenants_platform_bypass
  ON tenants
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文只能读自己的租户行（Console 自助场景）
CREATE POLICY tenants_self_read
  ON tenants
  USING (id::text = current_setting('app.current_tenant_id', true));

-- audit_logs: 复用现有 RLS（见 20260501000100_init_schema.sql SECTION 11）
-- 现有策略：tenant_isolation 为 RESTRICTIVE，要求 tenant_id = current_setting('app.current_tenant_id')
-- 平台操作（tenant_id IS NULL）需新增 PERMISSIVE bypass 策略，否则平台无法跨租户读审计
-- 注意：现有 audit_logs 没有 target_tenant_id 列，只有 tenant_id（操作者租户）；
--   - 平台操作 tenant_id IS NULL → 直接放行（新增 PERMISSIVE 策略）
--   - 租户操作 tenant_id = current tenant → 放行（现有 RESTRICTIVE 策略）
--   - 租户只能看到自己租户的操作日志，不能看到平台对其他租户操作的日志（合规要求）
CREATE POLICY audit_platform_bypass
  ON audit_logs
  USING (tenant_id IS NULL);

-- 注：现有 tenant_isolation RESTRICTIVE 策略保留不变，仍要求非 NULL tenant_id 匹配 current_setting
```

---

## §5 API 契约设计

全部新增路径放入 `repo/api/openapi/services/v1.yaml`，前缀 `/api/v1/svc`。Core API `repo/api/openapi/v1.yaml` 不修改。

### 5.0 路径组职责说明

| 路径组 | 作用 | 主要端点 |
|--------|------|---------|
| **tenants** | 租户生命周期管理：创建/查看/编辑/冻结/解冻/禁用（禁用后关联资源强制删除，不可逆）；身份认证（SSO/IdP 配置与测试、MFA 强制开关）；配额查询与变更申请（配额实际数据由 Core API 承载，本服务代理查询并记录变更申请至 `tenant_quota_change` 表 §4.1.6）；租户管理员列表；生命周期查询；操作历史查询 | `POST /tenants` 创建、`GET /tenants` 列表、`GET /tenants/{tenantId}` 详情、`PUT /tenants/{tenantId}` 修改基本信息、`POST /tenants/{tenantId}/freeze`、`POST /tenants/{tenantId}/unfreeze`、`POST /tenants/{tenantId}/disable`、`GET /tenants/{tenantId}/auth/sso` 查看 SSO、`PUT /tenants/{tenantId}/auth/sso` 修改 SSO、`POST /tenants/{tenantId}/auth/sso/test` 测试连接、`PUT /tenants/{tenantId}/auth/mfa` 切换强制 MFA、`GET /tenants/{tenantId}/quota` 查询配额、`POST /tenants/{tenantId}/quota-requests` 配额变更申请、`GET /tenants/{tenantId}/quota-requests` 查询申请列表、`POST /tenants/{tenantId}/quota-requests/{reqId}/approve` 审批申请、`GET /tenant-plans/{planId}/bindable-tenants` 查询可绑定该套餐的租户、`GET /tenants/{tenantId}/admins` 分页查询租户管理员（§5.2.7）、`GET /tenants/{tenantId}/lifecycle` 分页查看生命周期、`GET /tenants/{tenantId}/audit-logs` 分页查看操作历史 |
| **tenant-admins** | 租户管理员管理（跨租户）：跨租户分页查询 | `GET /tenant-admins` 跨租户分页查询所有管理员（返回租户对象） |
| **tenants/{tenantId}/admins** | 租户管理员管理（租户内）：详情、查询/修改角色权限、移交所有者、重置密码、禁用/启用、软删除；管理员操作历史查询 | `GET /tenants/{tenantId}/admins/{userId}` 详情、`GET /tenants/{tenantId}/admins/{userId}/role` 查询角色权限、`PUT /tenants/{tenantId}/admins/{userId}/role` 改权限、`POST /tenants/{tenantId}/transfer-ownership` 移交所有者、`POST /tenants/{tenantId}/admins/{userId}/reset-password`、`POST /tenants/{tenantId}/admins/{userId}/disable`、`POST /tenants/{tenantId}/admins/{userId}/enable`、`DELETE /tenants/{tenantId}/admins/{userId}`、`GET /tenants/{tenantId}/admins/{userId}/audit-logs` 管理员操作历史 |
| **tenant-plans** | 套餐模板管理：draft→active→disabled 状态机；套餐限额查询与修改（任意状态可改，修改后同步存量租户）；删除套餐（有租户关联时不可删除）；套餐绑定租户查询；可绑定租户列表；套餐操作历史；配额元数据透传 | `POST /tenant-plans`、`GET /tenant-plans`、`GET /tenant-plans/{planId}`、`PUT /tenant-plans/{planId}`（更新 name/description）、`GET /tenant-plans/{planId}/quota-limits`、`PUT /tenant-plans/{planId}/quota-limits`、`POST /tenant-plans/{planId}/activate`、`POST /tenant-plans/{planId}/disable`、`DELETE /tenant-plans/{planId}`、`GET /tenant-plans/{planId}/tenants`、`GET /tenant-plans/{planId}/bindable-tenants`、`GET /tenant-plans/{planId}/audit-logs`、`GET /quota-meta`、`POST /tenants/{tenantId}/plan` |
| **tenants/{tenantId}/plan** | 绑定套餐更新租户配额（操作对象是租户，挂在租户下） | `POST /tenants/{tenantId}/plan` 绑定套餐更新配额（plan_id 在 body） |
| **platform-admins** | 平台运营账号管理：三类平台账号 CRUD、重置密码、启用/禁用、改角色、软删除、最后管理员保护；查询指定运营账号权限 | `POST /platform-admins` 创建（含 password）、`GET /platform-admins` 列表、`GET /platform-admins/{userId}` 详情、`GET /platform-admins/{userId}/permissions` 查询指定运营账号权限、`PUT /platform-admins/{userId}/role` 改角色、`POST /platform-admins/{userId}/reset-password`、`POST /platform-admins/{userId}/disable`、`POST /platform-admins/{userId}/enable`、`DELETE /platform-admins/{userId}` |

**路径前缀**：全部以 `/api/v1/svc/*` 开头（Services 层），由 Core Gateway 鉴权后转发至 tenant-service。配额元数据管理与租户配额配置通过 Core API（`/api/v1/admin/*`）承载，BOSS 前端直接代理调用。

**职责分层：**

- `tenants` / `tenants/{tenantId}/admins` / `tenant-admins` / `platform-admins` = 人/组织的生命周期与配额视图
- `tenant-plans` = 套餐模板（套餐限额的集中定义）
- `tenants/{tenantId}/plan` = 租户绑定套餐（操作对象是租户）
- 配额元数据 / 配额配置 / 计量采集 = Core 责任（见 `通用资源配额与计量落地方案.md`），BOSS 仅代理 UI 与创建租户时初始化
- 计费与账单（充值/扣费/欠费冻结）暂不实现，后续 PR 再补充

### 5.1 标签与通用约定

```yaml
tags:
  - name: Tenants
    description: 租户生命周期管理（含配额与用量查询）
  - name: TenantAdmins
    description: 租户管理员管理
  - name: PlatformAdmins
    description: 平台运营账号管理
  - name: TenantPlans
    description: 套餐模板管理
```

**通用头：**

| 头 | 必填 | 格式 | 说明 |
|----|------|------|------|
| Authorization | 是 | `Bearer <platform JWT>` | 平台 JWT |
| X-Request-Id | 否 | - | 链路追踪 |

**响应约定：**

所有写操作（Create / Update / Delete）成功统一返回 HTTP **200 OK**（不使用 201 Created）；响应体仅含目标资源 id 与操作结果描述，不返回完整资源对象，前端按需调用 GET 端点获取最新数据。

| 操作类型 | HTTP | 响应体 | 说明 |
|---------|------|--------|------|
| Create / Update / Delete（写操作） | 200 | `{ id, message }` | 仅返回目标资源 id 与操作结果描述；前端按需调用 GET 端点获取最新数据 |
| GET 查询（单个） | 200 | `{ ...资源完整字段 }` | 返回完整资源对象 |
| GET 查询（列表） | 200 | `{ items: [...], next_cursor }` | 返回 CursorPage 分页数据（limit + cursor 游标） |

通用响应字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid \| null | 操作目标资源 ID（创建=新资源 id；修改/删除=被操作资源 id；批量操作时为 null） |
| message | string | 操作结果描述；成功="ok"或具体动作描述；失败时含具体失败原因 |

### 5.2 Tenants 路径

#### 5.2.1 `POST /tenants` — 创建租户

**AuthZ:** platform-admin, platform-ops

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| name | string | 是 | `^[a-z0-9-]{3,40}$`，活动租户间唯一 | 租户名（英文 slug 风格唯一 key；不可改） |
| display_name | string | 是 | 1-128 字符 | 租户显示名（中英文均可；可改） |
| email | string | 是 | RFC 5322 | 租户联系邮箱（平台联系租户的商务/管理员邮箱；与首位管理员邮箱不同） |
| plan_id | uuid | 是 | 必须指向 active 套餐 | 配额套餐 ID（tenants.plan_id 外键 → tenant_plans.id） |
| admin_email | string | 是 | RFC 5322 | 首位租户管理员登录邮箱（写入 users.email） |
| admin_name | string | 是 | 1-64 字符 | 首位租户管理员用户名（写入 users.username） |
| admin_password | string | 是 | 8-64 字符；必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类 | 首位租户管理员初始密码（前端**直接传明文**，由 HTTPS 保护传输；后端 bcrypt(cost=12) 写入 users.password_hash；永不明文返回、不写入日志/审计） |

```json
{
  "name": "acme",
  "display_name": "Acme Inc.",
  "email": "contact@acme.com",
  "plan_id": "00000000-0000-0000-0000-000000000001",
  "admin_email": "admin@acme.com",
  "admin_name": "Alice",
  "admin_password": "Ab3x!Yz9"
}
```

**Response 200:** `{ id, message }`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 新建的租户 ID |
| message | string | 操作结果描述（成功时为"tenant created"，失败时含具体原因） |

```json
{
  "id": "uuid",
  "message": "tenant created"
}
```

**说明：** 创建租户时仅传 7 个必填字段（name / display_name / email / plan_id / admin_email / admin_name / admin_password）；配额初始化不通过请求体显式传入——tenant-service 在事务内调用 Core API 批量初始化配额，按 Core `resource_quota_meta.default_quota` 自动建行。创建后如需调整租户配额，通过 Core API（`PUT /api/v1/admin/tenants/{id}/quota` 批量）调整，BOSS 前端代理。其余属性也一律**不在创建时输入**：
- MFA 强制 → 默认 FALSE；调整走 `PUT /tenants/{tenantId}/auth/mfa`（tenantId 在路径参数）
- SSO 配置 → 默认未配置；配置走 `PUT /tenants/{tenantId}/auth/sso`（tenantId 在路径参数）；测试连接走 `POST /tenants/{tenantId}/auth/sso/test`（tenantId 在路径参数）

**admin_password 传输与存储约定：**
- 前端**不做任何加密 / Hash**，直接以明文字符串放入 JSON 请求体（HTTPS 保护传输链路）
- 后端收到后立即 bcrypt(cost=12) 入库 `users.password_hash`，明文密码不写入任何日志、审计、响应
- 明文 admin_password 仅存在于：前端内存 → HTTPS 请求体 → 后端 handler 内存 → bcrypt 调用栈；出栈即丢弃

创建成功后如需展示完整租户对象，前端调用 `GET /tenants/{tenantId}` 获取。

**错误：**

| HTTP | code | 说明 |
|------|------|------|
| 400 | VALIDATION_FAILED | 请求体校验失败（含 name 格式、email 格式、admin_password 复杂度、字段缺失等） |
| 409 | TENANT_NAME_CONFLICT | name 已被活动租户占用 |
| 409 | IDEMPOTENCY_CONFLICT | 同 key 不同 body |
| 422 | PLAN_NOT_ACTIVE | plan_id 指向的套餐非 active 或不存在 |
| 403 | FORBIDDEN | 非 platform-admin/ops |

#### 5.2.2 `GET /tenants` — 列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Query 参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| status | string | 否 | 全部 | 过滤：active / frozen / disabled |
| search | string | 否 | 无 | name / display_name 模糊匹配 |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 租户摘要列表 |
| items[].id | uuid | 租户 ID |
| items[].name | string | 租户名（英文 slug 风格唯一 key） |
| items[].display_name | string | 租户显示名 |
| items[].plan_id | uuid | 套餐 ID（外键 → tenant_plans.id） |
| items[].plan_code | string | 套餐代码（service 层 JOIN tenant_plans 读取，不在 tenants 表存） |
| items[].status | string | 状态 |
| items[].admin_count | integer | 租户管理员数量（COUNT(users WHERE tenant_id = id AND status='active' AND role='tenant-admin')；service 层 JOIN 装配） |
| items[].created_at | timestamp | 创建时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

```json
{
  "items": [
    {
      "id": "uuid",
      "name": "acme",
      "display_name": "Acme Inc.",
      "plan_id": "00000000-0000-0000-0000-000000000001",
      "plan_code": "starter",
      "status": "active",
      "admin_count": 3,
      "created_at": "2026-07-24T10:00:00Z"
    }
  ],
  "next_cursor": "<base64-cursor>"
}
```

**说明：** 按 created_at DESC 排序，游标分页（limit + cursor）返回 CursorPage（items + next_cursor）。

#### 5.2.3 `GET /tenants/{tenantId}` — 详情

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Response 200:** 完整租户对象 + 用户统计

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 租户 ID |
| name | string | 租户名（英文 slug 风格唯一 key） |
| display_name | string | 租户显示名 |
| email | string | 租户联系邮箱 |
| plan_id | uuid | 套餐 ID |
| plan_code | string | 套餐代码（service 层 JOIN tenant_plans 读取） |
| status | string | 租户状态：active / frozen / disabled |
| frozen_at | string \| null | 冻结时间 |
| disabled_at | string \| null | 禁用时间 |
| created_at | string | 创建时间 |
| updated_at | string | 更新时间 |
| user_count | integer | 当前租户用户总数（含所有角色） |
| admin_count | integer | 当前租户管理员数（tenant-admin 角色） |

```json
{
  "id": "uuid",
  "name": "acme",
  "display_name": "Acme Inc.",
  "email": "contact@acme.com",
  "plan_id": "00000000-0000-0000-0000-000000000001",
  "plan_code": "starter",
  "status": "active",
  "frozen_at": null,
  "disabled_at": null,
  "created_at": "2026-07-24T10:00:00Z",
  "updated_at": "2026-07-24T10:00:00Z",
  "user_count": 15,
  "admin_count": 3
}
```

#### 5.2.4 `POST /tenants/{tenantId}/freeze` — 冻结

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**说明:** 冻结后用户无法登录，实例继续运行，资源保持原状。设置 `status='frozen'`, `frozen_at`。`tenant_lifecycle.reason` 由后端自动填充（如"管理员手动冻结"）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant frozen" }
```

#### 5.2.5 `POST /tenants/{tenantId}/unfreeze` — 解冻

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**说明:** 管理员解冻后恢复活跃状态。清空 `frozen_at`，状态置 `active`，恢复全部操作。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant unfrozen" }
```

#### 5.2.6 `POST /tenants/{tenantId}/disable` — 禁用

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**说明:** 禁用后资源删除，无法登录/查看/创建（Gateway 登录链路检查 `tenants.status='disabled'` → 403 `TENANT_DISABLED`）。设置 `status='disabled'`, `disabled_at`。**不修改 users.status**，登录时由 Gateway 拦截租户级禁用即可。**禁用不可逆，无法恢复**（禁用是终态，不存在启用端点）。Core 侧 `resource_quota` 行保留（数据不删除），但资源实例停止运行。`tenant_lifecycle.reason` 由后端自动填充（如"管理员手动禁用"）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant disabled" }
```

#### 5.2.7 `GET /tenants/{tenantId}/admins` — 分页查询租户管理员

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Query 参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| role | string | 否 | 全部 | 过滤：`tenant-owner` / `tenant-admin` / `user` |
| status | string | 否 | 全部 | 过滤：`active` / `disabled`（直接对应 `users.status`） |
| search | string | 否 | 无 | email / username 模糊匹配 |

**Response 200:** `CursorPage`（items + next_cursor），items 每条含 id / email / username / display_name / role / status / source / last_login_at（最近登录时间）及 tenant 对象（id / name / display_name / mfa_required），字段与详情（§5.4.4）一致。按 created_at DESC 排序，`next_cursor: null` 表示无更多数据

> MFA/SSO 字段存放在 `tenant_auth` 表（§4.1.2），共 3 个业务字段：`mfa_required`（BOOLEAN）+ `sso_enabled`（BOOLEAN）+ `sso_provider`（TEXT，'oidc'/'custom'/NULL）。SSO 详细配置（issuer_url / client_id / client_secret_ref / scopes / auto_provision / email_domains）不在 tenant_auth 表中存放，由外部系统（K8s Secret/ConfigMap）承载，通过 `sso_provider` 标识提供商类型。身份认证共 3 个写接口 + 1 个查询接口，统一以 `/tenants/{tenantId}/auth` 为前缀，租户 ID 通过路径参数 `{tenantId}` 传递。

#### 5.2.8 `GET /tenants/{tenantId}/auth/sso` — 查看 SSO 配置

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| sso_enabled | boolean | SSO 状态（TRUE=已开启，FALSE=已关闭） |
| provider | string \| null | `oidc` / `custom` / null |
| updated_at | timestamp | 上次配置更新时间 |

**说明：** SSO 详细配置（issuer_url / client_id 等）不在 tenant_auth 表中存放，如需查看请联系平台运维查询 K8s Secret/ConfigMap。

#### 5.2.9 `PUT /tenants/{tenantId}/auth/sso` — 修改 SSO 状态与 IdP 提供商

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| sso_enabled | boolean | 否 | TRUE / FALSE | SSO 开关；传入时切换状态 |
| provider | string | 否 | `oidc` / `custom` | IdP 提供商；传入时更新 sso_provider |

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "sso updated" }
```

**说明：**

- 本端点支持部分更新：仅传入 `sso_enabled` 则切换开关，仅传入 `provider` 则更新 `sso_provider`，两者同时传入则一并更新。
- 本端点只修改 `sso_enabled` 和 `sso_provider` 两个字段；SSO 详细配置（issuer_url / client_id / client_secret_ref / scopes / auto_provision / email_domains）由外部系统（K8s Secret/ConfigMap）承载，不在本端点处理。
- `sso_enabled=TRUE` 时要求 `sso_provider` 已存在（表中已有值或本次请求同时传入 `provider`），否则 422 `TENANT_SSO_CONFIG_INVALID`。
- `sso_enabled=FALSE` 无前置条件，关闭后保留 `sso_provider` 值以备重新开启。
- 关闭 SSO 后，已存在的 SSO 用户（`users.password_hash IS NULL`）无法登录，需平台改回本地密码或重新开启 SSO。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 422 | TENANT_SSO_CONFIG_INVALID | 开启 sso_enabled 但 sso_provider 为 NULL |
| 409 | TENANT_STATE_INVALID | 租户已 disabled |

#### 5.2.10 `POST /tenants/{tenantId}/auth/sso/test` — 测试 SSO 连接

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| success | boolean | 连接是否成功 |
| provider | string | 测试的 IdP 提供商 |
| discovery_result | object \| null | 成功时返回 OIDC discovery 文档摘要（authorization_endpoint / token_endpoint / userinfo_endpoint） |
| error | string \| null | 失败时返回错误信息 |
| tested_at | timestamp | 测试时间 |

```json
{
  "success": true,
  "provider": "oidc",
  "discovery_result": {
    "authorization_endpoint": "https://idp.example.com/auth",
    "token_endpoint": "https://idp.example.com/token",
    "userinfo_endpoint": "https://idp.example.com/userinfo"
  },
  "error": null,
  "tested_at": "2026-07-24T10:00:00Z"
}
```

```json
{
  "success": false,
  "provider": "oidc",
  "discovery_result": null,
  "error": "issuer_url 不可达：连接超时",
  "tested_at": "2026-07-24T10:00:00Z"
}
```

**说明：**

- 测试流程：读取 `tenant_auth.sso_provider`，根据 provider 类型从外部系统（K8s Secret/ConfigMap）加载 SSO 详细配置（issuer_url / client_id 等）进行连接测试 → 向 issuer_url 发起 OIDC discovery 请求（GET `/.well-known/openid-configuration`）→ 校验返回的 discovery 文档是否包含必需 endpoint。
- 不修改任何数据，不写审计日志，不触发幂等。
- 前端"测试连接"按钮调用此端点；建议在 `PUT /tenants/{tenantId}/auth/sso` 保存配置后、开启 `sso_enabled` 前先测试连接。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 422 | TENANT_SSO_CONFIG_INVALID | 当前租户未配置 SSO（sso_provider 为 NULL）或外部系统缺少 SSO 详细配置 |

#### 5.2.11 `PUT /tenants/{tenantId}/auth/mfa` — 切换强制 MFA（B2）

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| mfa_required | boolean | 是 | TRUE=强制 MFA / FALSE=关闭 |

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "mfa required updated" }
```

**说明:**

- 切换为 TRUE 后，该租户所有用户下次登录必须完成 MFA 二次校验；已登录 session 不受影响，但下次刷新 token 时强制
- 切换为 FALSE 后，用户可继续用 MFA 或关闭 MFA（用户级 MFA 配置不修改，仅租户级强制开关变更）
- 与用户级 MFA 配置关系：`tenant_auth.mfa_required=TRUE` 时，即使用户未开启 MFA 也会被强制要求设置；`FALSE` 时遵循用户级配置
- MFA 校验形式：TOTP（Google Authenticator / 1Password 等）；本设计不包含 SMS/邮箱验证码
- MFA secret 存储：用户级（`users.mfa_secret_encrypted`，由后续 PR 添加），不在本设计范围
- 本端点仅切换租户级开关；用户级 MFA enrollment 端点（`POST /api/v1/auth/mfa/enroll` 等）属 auth-service 现有职责，不在本设计范围

> 配额变更申请记录在 `tenant_quota_change` 表（§4.1.6）。配额实际数据（已用/限额）由 Core `resource_quota` 表承载，本服务通过 Core API 代理查询。共 3 个接口：查询租户配额、提交配额变更申请、查询申请列表与审批。`tenantId` 通过路径参数传递。

#### 5.2.12 `GET /tenants/{tenantId}/quota` — 查询租户配额

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 配额列表 |
| items[].resource_type | string | 配额维度标识（如 storage_gb / token_count） |
| items[].display_name | string | 配额展示名（来自 Core `resource_quota_meta.display_name`） |
| items[].used | integer | 已用量（来自 Core `resource_quota.used`） |
| items[].total | integer | 限额（来自 Core `resource_quota.total`） |
| items[].unit | string | 单位名（来自 Core `resource_quota_meta.unit`） |

```json
{
  "items": [
    {
      "resource_type": "storage_gb",
      "display_name": "存储空间",
      "used": 45,
      "total": 100,
      "unit": "GB"
    },
    {
      "resource_type": "token_count",
      "display_name": "Token 额度",
      "used": 1200000,
      "total": 5000000,
      "unit": "次"
    }
  ]
}
```

**说明：** 本端点代理 Core API `GET /api/v1/admin/tenants/{tenant_id}/quota`，组装 `resource_quota`（used / total）与 `resource_quota_meta`（display_name / unit）的 JOIN 结果返回。

#### 5.2.13 `POST /tenants/{tenantId}/quota-requests` — 提交配额变更申请

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| items | array | 是 | 至少 1 项 | 变更申请列表，每项一个配额维度 |

**items 每项结构:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| resource_type | string | 是 | `^[a-z0-9_]{2,40}$` | 配额维度标识 |
| new_value | integer | 是 | >= 0 | 申请变更为的配额值 |

```json
{
  "items": [
    { "resource_type": "gpu_count", "new_value": 32 },
    { "resource_type": "storage_gb", "new_value": 4096 },
    { "resource_type": "member_count", "new_value": 200 }
  ]
}
```

**Response 200:**

```json
{ "id": "uuid", "message": "quota change request submitted" }
```

**说明：**

- 支持一次提交多个配额维度的变更申请，每个维度生成一条独立的 `tenant_quota_change` 记录。
- 提交时逐维度从 Core `resource_quota` 读取当前 `total` 值冻结到 `old_value`，`status` 置为 `pending`。
- 重复检查：同一租户同一 `resource_type` 已有 `pending` 状态的申请时，该维度跳过并返回 409 `QUOTA_CHANGE_REQUEST_DUPLICATE`（其他维度仍正常处理）。
- `items` 中同一 `resource_type` 不可重复出现，否则 422 `QUOTA_CHANGE_REQUEST_INVALID`。
- 申请创建后需平台管理员审核（审核端点属后续需求）。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 409 | QUOTA_CHANGE_REQUEST_DUPLICATE | 该租户该配额维度已有待审核的申请 |
| 422 | QUOTA_CHANGE_REQUEST_INVALID | resource_type 不存在于 resource_quota_meta / new_value 为负数 / items 为空 / items 中 resource_type 重复 |

#### 5.2.14 `GET /tenant-plans/{planId}/bindable-tenants` — 查询可绑定该套餐的租户列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 可绑定租户列表 |
| items[].id | string | 租户 ID |
| items[].name | string | 租户名称 |
| items[].display_name | string | 显示名 |
| items[].status | string | 状态：active / frozen / disabled |

```json
{
  "items": [
    { "id": "uuid", "name": "acme", "display_name": "ACME 公司", "status": "active" },
    { "id": "uuid", "name": "globex", "display_name": "Globex 公司", "status": "frozen" }
  ]
}
```

**说明：** 查询 `tenants` 表中 `status != 'disabled' AND plan_id IS DISTINCT FROM planId`（排除已绑定该套餐的租户），不分页，按 name 排序。供前端在套餐详情页"绑定租户"Tab 下拉选择使用。

#### 5.2.15 `GET /tenants/{tenantId}/lifecycle` — 分页查看租户生命周期

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| action | string | 否 | 全部 | 过滤：`active` / `frozen` / `disabled` |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 生命周期记录列表（按 created_at DESC 排序） |
| items[].id | string | 记录 ID |
| items[].tenant_id | string | 租户 ID |
| items[].action | string | 当前状态/动作：active / frozen / disabled |
| items[].reason | string \| null | 变更原因 |
| items[].user_id | string \| null | 操作者 user_id（NULL=系统触发） |
| items[].request_id | string \| null | 关联请求 ID |
| items[].created_at | timestamp | 记录时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

```json
{
  "items": [
    {
      "id": "uuid",
      "tenant_id": "uuid",
      "action": "frozen",
      "reason": "管理员手动冻结",
      "user_id": "uuid",
      "request_id": "req-123",
      "created_at": "2026-07-24T10:00:00Z"
    }
  ],
  "next_cursor": "<base64-cursor>"
}
```

**说明：** 本端点直接查询 `tenant_lifecycle` 表（§4.1.3），按 `created_at DESC` 排序，支持按 `action` 过滤。不调用 Core API。

#### 5.2.16 `GET /tenants/{tenantId}/audit-logs` — 分页查看操作历史

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| action | string | 否 | 全部 | 过滤操作类型（如 `tenant.create` / `tenant.freeze` / `tenant.disable` / `tenant.update_sso` / `tenant.quota_change_request` 等） |
| result | string | 否 | 全部 | 过滤：`success` / `failed` |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 操作历史列表（按 created_at DESC 排序） |
| items[].id | string | 记录 ID |
| items[].tenant_id | string \| null | 租户 ID（平台级操作时为 NULL） |
| items[].user_id | string \| null | 操作者 user_id |
| items[].request_id | string \| null | 关联请求 ID |
| items[].action | string | 操作类型（如 tenant.create / tenant.freeze） |
| items[].resource | string | 操作资源类型（如 tenant / tenant_admin / tenant_plan） |
| items[].result | string | 结果：success / failed |
| items[].details | object \| null | 操作详情（JSON） |
| items[].ip_address | string \| null | 操作者 IP |
| items[].user_agent | string \| null | 操作者 User-Agent |
| items[].created_at | timestamp | 记录时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

```json
{
  "items": [
    {
      "id": "uuid",
      "tenant_id": "uuid",
      "user_id": "uuid",
      "request_id": "req-123",
      "action": "tenant.freeze",
      "resource": "tenant",
      "result": "success",
      "details": { "target_id": "uuid", "reason": "管理员手动冻结" },
      "ip_address": "10.0.0.1",
      "user_agent": "Mozilla/5.0",
      "created_at": "2026-07-24T10:00:00Z"
    }
  ],
  "next_cursor": "<base64-cursor>"
}
```

**说明：** 本端点直接查询 `audit_logs` 分区表（§4.5），按 `created_at DESC` 排序，支持按 `action` 和 `result` 过滤。不调用 Core API。

#### 5.2.17 `PUT /tenants/{tenantId}` — 修改租户基本信息

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| display_name | string | 否 | 1-128 字符 | 租户显示名（可改） |
| contact_email | string | 否 | RFC 5322 | 租户联系邮箱（可改） |

```json
{
  "display_name": "Acme Inc. Updated",
  "contact_email": "new-contact@acme.com"
}
```

**说明：** 部分更新，仅传入需要修改的字段。不支持修改 `name`（英文 slug 唯一 key，创建后不可改）与 `status`（通过 freeze/unfreeze/disable 专用端点操作）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant updated" }
```

#### 5.2.18 `GET /tenants/{tenantId}/quota-requests` — 查询配额变更申请列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| status | string | 否 | 全部 | 过滤：`pending` / `approved` / `rejected` |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 申请列表（按 created_at DESC 排序） |
| items[].id | string | 申请 ID |
| items[].tenant_id | string | 租户 ID |
| items[].resource_type | string | 配额维度标识 |
| items[].old_value | integer | 原配额值 |
| items[].new_value | integer | 申请变更为的配额值 |
| items[].status | string | 状态：pending / approved / rejected |
| items[].requested_by | string | 申请人 user_id |
| items[].created_at | timestamp | 申请时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

**说明：** 查询 `tenant_quota_change` 表（§4.1.6）中该租户的配额变更申请列表，按 `created_at DESC` 排序，游标分页（limit + cursor）返回 CursorPage（items + next_cursor），支持按 `status` 过滤。不调用 Core API。

#### 5.2.19 `POST /tenants/{tenantId}/quota-requests/{reqId}/approve` — 审批配额变更申请

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| reqId | uuid | 是 | 配额变更申请 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| approved | boolean | 是 | TRUE=通过 / FALSE=驳回 | 审批结果 |

```json
{ "approved": true }
```

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "quota change request approved" }
```

**说明：**

- 审批通过（`approved=true`）：将 `tenant_quota_change.status` 置为 `approved`，并调用 Core API `PUT /api/v1/admin/tenants/{tenant_id}/quota`（批量）将 `total` 设为 `new_value`。
- 审批驳回（`approved=false`）：将 `tenant_quota_change.status` 置为 `rejected`，不修改配额。
- 仅 `pending` 状态的申请可审批，否则 409 `QUOTA_CHANGE_REQUEST_NOT_PENDING`。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | QUOTA_CHANGE_REQUEST_NOT_FOUND | 申请不存在 |
| 409 | QUOTA_CHANGE_REQUEST_NOT_PENDING | 申请非 pending 状态，不可审批 |

### 5.3 TenantPlans 路径

#### 5.3.1 `POST /tenant-plans` — 创建套餐模板

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| code | string | 是 | `^[a-z0-9-]{3,40}$`，全局唯一 | 套餐代码 |
| name | string | 是 | 1-64 字符 | 套餐名称 |
| quota_limits | array | 否 | 每项 `{resource_type, total}` | 各维度配额上限；`total` 为 null 表示用 `resource_quota_meta.default_quota`；未列出的维度也用 default_quota |
| description | string | 否 | ≤ 512 字符 | 套餐描述 |
| idempotency_key | string (uuid) | 是 | UUID | 幂等键，由客户端生成，放在 request body 中 |

```json
{
  "code": "pro",
  "name": "专业版",
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "quota_limits": [
    {"resource_type": "gpu_count", "total": 16},
    {"resource_type": "cpu_core", "total": 128},
    {"resource_type": "memory_gb", "total": 512},
    {"resource_type": "storage_gb", "total": 2048},
    {"resource_type": "token_count", "total": 5000000},
    {"resource_type": "kb_query_count", "total": 50000},
    {"resource_type": "member_count", "total": 200},
    {"resource_type": "inference_service_count", "total": 30}
  ]
}
```

> `quota_limits` 中的 `resource_type` 必须是 `resource_quota_meta` 中 `enabled=true` 的已注册维度，否则 422 `QUOTA_RESOURCE_NOT_REGISTERED`。`total` 为 null 表示该维度用默认值。审计日志在事务提交后 best-effort 写入（审计失败不回滚已提交的创建），action='tenant_plan.create'，details 含 plan_id/code/quota_limits。

**Response 200:** `{ id, message }`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 新建套餐 ID |
| message | string | 操作结果描述 |

```json
{ "id": "uuid", "message": "tenant plan created" }
```

**说明:** `code` 全局唯一；新套餐默认 `status='draft'`，需通过 `POST /tenant-plans/{planId}/activate` 转为 `active` 后才能被租户引用。如需展示完整套餐对象，前端调用 `GET /tenant-plans/{planId}` 获取。

#### 5.3.2 `GET /tenant-plans` — 列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | — | 上一页返回的 next_cursor |
| status | string | 否 | 全部 | 过滤：draft / active / disabled |
| search | string | 否 | — | 关键字模糊查询（匹配 name，大小写不敏感） |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 套餐列表 |
| items[].id | string | 套餐 ID |
| items[].code | string | 套餐代码 |
| items[].name | string | 套餐名称 |
| items[].description | string \| null | 套餐描述 |
| items[].status | string | 状态：draft / active / disabled |
| items[].tenant_count | integer | 绑定租户数量（COUNT tenants WHERE plan_id = id AND status != 'disabled'） |
| items[].created_at | timestamp | 创建时间 |
| items[].updated_at | timestamp | 更新时间 |
| total | integer | 满足筛选条件的总条数（用于前端分页展示） |
| next_cursor | string \| null | 下一页游标；null 表示已无更多数据 |

```json
{
  "items": [
    {
      "id": "uuid",
      "code": "pro",
      "name": "专业版",
      "description": "专业版套餐",
      "status": "active",
      "tenant_count": 12,
      "created_at": "2026-07-20T10:00:00Z",
      "updated_at": "2026-07-20T10:00:00Z"
    }
  ],
  "total": 12,
  "next_cursor": "cursor-string"
}
```

**说明：** 返回套餐列表，不包含 `quota_limits`（配额限额需调用详情端点 `GET /tenant-plans/{planId}/quota-limits` 获取）。`tenant_count` 通过子查询 `COUNT(tenants WHERE plan_id = id AND status != 'disabled')` 统计。

#### 5.3.3 `GET /tenant-plans/{planId}` — 详情

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 套餐 ID |
| code | string | 套餐代码 |
| name | string | 套餐名称 |
| description | string \| null | 套餐描述 |
| status | string | 状态：draft / active / disabled |
| tenant_count | integer | 绑定租户数量（COUNT tenants WHERE plan_id = id AND status != 'disabled'） |
| created_at | timestamp | 创建时间 |
| updated_at | timestamp | 修改时间 |

```json
{
  "id": "uuid",
  "code": "pro",
  "name": "专业版",
  "description": "适用于中小团队",
  "status": "active",
  "tenant_count": 12,
  "created_at": "2026-07-20T10:00:00Z",
  "updated_at": "2026-07-20T10:00:00Z"
}
```

#### 5.3.4 `GET /tenant-plans/{planId}/quota-limits` — 查询套餐限额

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 限额列表 |
| items[].resource_type | string | 配额维度标识（如 storage_gb / token_count） |
| items[].display_name | string | 限额展示名（来自 Core `resource_quota_meta.display_name`） |
| items[].unit | string | 单位名（来自 Core `resource_quota_meta.unit`） |
| items[].total | integer | 展示限额值；未显式设置的维度已用 `resource_quota_meta.default_quota` 兜底为具体数值（不返回 null） |

```json
{
  "items": [
    {
      "resource_type": "storage_gb",
      "display_name": "存储空间",
      "unit": "GB",
      "total": 2048
    },
    {
      "resource_type": "token_count",
      "display_name": "Token 额度",
      "unit": "次",
      "total": 1000000
    }
  ]
}
```

**说明：** service 层 `buildQuotaLimitViews`（store 原始行 + Core `ListQuotaMeta`，**非** SQL JOIN meta）返回限额名、单位、当前值。历史 `total` 为 NULL 时用 `default_quota` 兜底并可回写；Create/PUT 写入侧 `total: null` 物化为具体值落库。展示 `total` 始终为具体数值。

> 注：套餐限额可在创建时设置（§5.3.1 `POST /tenant-plans`），也可通过 `PUT /tenant-plans/{planId}/quota-limits`（§5.3.8）修改（**任意状态**均可改），修改后自动同步存量租户。套餐模板字段 name/description 可通过 `PUT /tenant-plans/{planId}` 修改。配额元数据列表走 Services `GET /quota-meta`（透传 Core）；本模块 **不** 提供独立配额元数据管理页（Non-Goal）。

#### 5.3.4b `PUT /tenant-plans/{planId}` — 修改套餐基本信息

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Request Body:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| idempotency_key | string | 是 | 幂等键（body 或 header `Idempotency-Key`） |
| name | string | 否 | 套餐名称（1-64 字符）；nil=不更新，空串=清空 |
| description | string | 否 | 套餐描述（≤512 字符）；nil=不更新，空串=清空 |

```json
{
  "idempotency_key": "uuid",
  "name": "专业版 V2",
  "description": "更新后的描述"
}
```

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 套餐 ID |
| message | string | "tenant plan updated" |

```json
{ "id": "uuid", "message": "tenant plan updated" }
```

**说明：** 仅允许修改 name 和 description 字段（code 不可修改）。nil 表示不更新该字段，空串表示清空。任意状态（draft/active/disabled）均可修改。审计日志在事务提交后 best-effort 写入，action='tenant_plan.update'，details 含 plan_id/name_updated/description_updated。

#### 5.3.4c `GET /quota-meta` — 查询配额元数据（透传 Core）

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 配额维度列表 |
| items[].resource_type | string | 维度标识（如 gpu_count / cpu_count / storage_gb 等） |
| items[].display_name | string | 展示名（如「GPU 卡数」） |
| items[].unit | string | 单位（如 card / core / GB） |
| items[].default_quota | integer | 默认配额上限 |
| items[].is_discrete | boolean | 是否离散维度（离散维度 InputNumber 不允许小数） |

```json
{
  "items": [
    { "resource_type": "gpu_count", "display_name": "GPU 卡数", "unit": "card", "default_quota": 4, "is_discrete": true }
  ]
}
```

**说明：** 透传 Core `GET /api/v1/admin/quota-meta` 结果（无缓存，每次实时调 Core）。Core 不可用时返回 502 `GRPC_CLIENT_UNAVAILABLE`。供前端创建/修改套餐限额时展示可用维度列表。

#### 5.3.5 `POST /tenant-plans/{planId}/activate` — 发布套餐

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**说明:** `draft` 或 `disabled` → `active`。转 active 后可被租户引用绑定。

**Request Body:** 仅含 `idempotency_key`（string (uuid)，必填，由客户端生成的幂等键）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant plan activated" }
```

#### 5.3.6 `POST /tenant-plans/{planId}/disable` — 禁用套餐

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**说明:** `active` → `disabled`。disabled 套餐不可被新租户引用，但已绑定该套餐的存量租户不受影响（继续按该模板计算配额）。disabled 套餐可通过 `POST /tenant-plans/{planId}/activate` 重新启用回 active。

**Request Body:** 仅含 `idempotency_key`（string (uuid)，必填，由客户端生成的幂等键）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant plan disabled" }
```

#### 5.3.7 `DELETE /tenant-plans/{planId}` — 删除套餐

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**说明:** 软删除套餐（`is_deleted=TRUE`, `deleted_at=now()`）。**若有租户已关联该套餐，则不允许删除**（返回 409 `TENANT_PLAN_IN_USE`）。任意状态（`draft` / `active` / `disabled`）均可删除，仅校验是否有租户关联。删除后套餐 `code` 可被新套餐复用（partial unique index 仅约束未删除套餐）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "tenant plan deleted" }
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_PLAN_NOT_FOUND | 套餐不存在或已删除 |
| 409 | TENANT_PLAN_IN_USE | 有租户已关联该套餐（`tenants.plan_id = plan_id`），不可删除 |

#### 5.3.8 `PUT /tenant-plans/{planId}/quota-limits` — 修改套餐限额（同步存量租户）

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Request Body:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| items | array | 是 | 限额维度列表，至少 1 项 |
| idempotency_key | string (uuid) | 是 | 幂等键，由客户端生成，放在 request body 中 |

**items 每项结构:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| resource_type | string | 是 | 配额维度标识 |
| total | integer \| null | 是 | 新的限额值（>= 0）；NULL 表示使用 `resource_quota_meta.default_quota` |

```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "items": [
    { "resource_type": "storage_gb", "total": 4096 },
    { "resource_type": "token_count", "total": 2000000 },
    { "resource_type": "member_count", "total": null }
  ]
}
```

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "quota limits updated" }
```

**说明：**

- 修改 `plan_quota_limits` 表中该套餐的限额值（UPSERT：INSERT ... ON CONFLICT (plan_id, resource_type) DO UPDATE SET total=EXCLUDED.total）
- **同步存量租户**：查询 `tenants WHERE plan_id = $planId AND status != 'disabled'`，对每个租户：
  - 收集需更新的维度（排除已 approved 配额变更申请的维度），逐租户调 Core API `GET /api/v1/admin/tenants/{tenant_id}/quota` 判断配额行是否存在，不存在则 `POST`（新建配额行 used/reserved=0），已存在则 `PUT`（修改 total，自动收紧）
  - **若该维度存在已审核通过的配额变更申请**（`tenant_quota_change.status='approved'`），则保留其配额值，不覆盖（从请求中排除该维度）
  - **不影响已有资源**：若新限额低于当前 `used + reserved`，则 `total` 自动收紧为 `used + reserved`，已有资源继续运行，不会强制停止或回收
- 审计日志在事务提交后 best-effort 写入：`action='tenant_plan.update_quota_limits'`，`details` 含 `plan_id`、`updated_dimensions`、`synced_tenant_count`（已同步租户数）、`skipped_approved`（各租户保留的已审核维度列表）、`tightened`（Core 自动收紧的维度数）
- 任意状态（`active` / `draft` / `disabled`）套餐均可修改限额

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_PLAN_NOT_FOUND | 套餐不存在或已删除 |
| 422 | QUOTA_RESOURCE_NOT_REGISTERED | resource_type 未注册或 enabled=false |
| 400 | VALIDATION_FAILED | total 为负数 / items 为空 / items 中 resource_type 重复 |

#### 5.3.9 `POST /tenants/{tenantId}/plan` — 绑定套餐（按套餐限额更新配额）

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| plan_id | uuid | 是 | 套餐 ID（外键 → tenant_plans.id） |
| idempotency_key | string (uuid) | 是 | 幂等键，由客户端生成，放在 request body 中 |

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "quota bound to plan" }
```

**说明：**

- 先更新 `tenants.plan_id = plan_id`（若与当前不同），再同步 Core
- 读取 `plan_quota_limits` 表中该 `plan_id` 对应的所有维度限额（`resource_type` → `total`），对每个维度：
  - **若该维度存在已审核通过的配额变更申请**（`tenant_quota_change.status='approved'`），则保留其配额值，不覆盖（跳过 Core API 调用）
  - 否则（包括 pending 申请中的维度），收集到待更新列表：
    - `total` 非 NULL → new_total = total
    - `total` 为 NULL → new_total = resource_quota_meta.default_quota
  - 收集完成后，逐租户调 Core API `GET /api/v1/admin/tenants/{tenant_id}/quota` 判断配额行是否存在，不存在则 `POST`（新建 used/reserved=0），已存在则 `PUT`（修改 total，自动收紧）
- Core 同步失败时 best-effort 回滚 plan_id（回滚失败也返回错误）
- 本端点直接修改配额，不走审批流程（仅 platform-admin / platform-ops 有权调用）
- **不影响已有资源**：更换套餐后 Core `resource_quota.total` 被更新为新套餐限额，但已创建的实例/资源不受影响、继续运行。若新限额低于当前 `used + reserved`，则 `total` 自动收紧为 `used + reserved`，已有资源继续运行，不会强制停止或回收
- 写审计日志：`action='tenant.bind_plan_quota'`，`details` 含 `plan_id`、`tenant_id`、`tenant_name`、`tenant_display_name`、`skipped_approved`（保留的已审核维度列表）、`tightened`（Core 自动收紧的维度数）、`updated`（已更新维度列表）；审计失败 best-effort（只 Warn 不阻塞成功响应）

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_PLAN_NOT_FOUND | plan_id 不存在 |
| 422 | PLAN_NOT_ACTIVE | 套餐状态非 active（draft / disabled） |
| 404 | TENANT_NOT_FOUND | Core 层返回：租户在 Core 侧不存在 |
| 409 | TENANT_STATE_INVALID | 租户已 disabled |

#### 5.3.10 `GET /tenant-plans/{planId}/tenants` — 查询套餐绑定的租户列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**说明:** 查询绑定该套餐的所有租户（`tenants WHERE plan_id = planId AND status != 'disabled'`），不分页，返回完整列表。

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 租户列表 |
| items[].id | string | 租户 ID |
| items[].name | string | 租户名称 |
| items[].display_name | string | 显示名 |
| items[].status | string | 状态：active / frozen / disabled |

```json
{
  "items": [
    { "id": "uuid", "name": "acme", "display_name": "ACME 公司", "status": "active" },
    { "id": "uuid", "name": "globex", "display_name": "Globex 公司", "status": "frozen" }
  ]
}
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_PLAN_NOT_FOUND | 套餐不存在或已软删除 |

#### 5.3.10b `GET /tenant-plans/{planId}/bindable-tenants` — 查询可绑定该套餐的租户列表

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**说明:** 查询可绑定该套餐的租户（`tenants WHERE status != 'disabled' AND plan_id IS DISTINCT FROM planId`），包含未绑定任何套餐的租户及当前绑定其它套餐的租户，按 name 排序，不分页，返回完整列表。供套餐详情页"绑定租户"Tab 的租户下拉列表使用。

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 租户列表 |
| items[].id | string | 租户 ID |
| items[].name | string | 租户名称 |
| items[].display_name | string | 显示名 |
| items[].status | string | 状态：active / frozen / disabled |

```json
{
  "items": [
    { "id": "uuid", "name": "acme", "display_name": "ACME 公司", "status": "active" },
    { "id": "uuid", "name": "globex", "display_name": "Globex 公司", "status": "active" }
  ]
}
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_PLAN_NOT_FOUND | 套餐不存在或已软删除 |

#### 5.3.11 `GET /tenant-plans/{planId}/audit-logs` — 分页查看套餐操作历史

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| planId | uuid | 是 | 套餐 ID |

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | — | 上一页返回的 next_cursor |

> 注：action / result 筛选参数未在 API 实现中支持（仅 limit/cursor 分页），前端 result 筛选为本地过滤。

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 操作历史列表（按 created_at DESC 排序） |
| items[].id | string | 记录 ID |
| items[].action | string | 操作类型（如 tenant_plan.create / tenant_plan.activate / tenant_plan.disable / tenant.bind_plan_quota 等） |
| items[].result | string | 结果：success / failure |
| items[].details | object \| null | 操作详情（JSON，含 plan_id 等） |
| items[].created_at | timestamp | 记录时间（格式：年-月-日 时:分:秒，Asia/Shanghai） |
| total | integer | 满足筛选条件的总条数（用于前端分页展示） |
| next_cursor | string \| null | 下一页游标；null 表示已无更多数据 |

> 注：tenant_id / user_id / request_id / resource / ip_address / user_agent 虽在 audit_logs 表中存储，但不在 API 响应中暴露（仅返回 5 个字段）。

**说明：** 查询 `audit_logs` 表中 `resource = 'tenant_plan' AND details->>'plan_id' = $plan_id` 的记录，按 `created_at DESC` 排序（cursor 基于 created_at）。不调用 Core API。

### 5.4 TenantAdmins 路径

#### 5.4.1 `POST /tenants/{tenantId}/admins/invite` — 邀请租户管理员

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| email | string | 是 | RFC 5322 | 被邀请人邮箱（在指定租户内匹配） |
| username | string | 是 | 1-64 字符，不含 `:` | 被邀请人用户名（与 email 共同匹配） |
| idempotency_key | string (uuid) | 是 | UUID | 幂等键，由客户端生成，放在 request body 中 |

```json
{
  "email": "bob@acme.com",
  "username": "local:bob",
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response 200:** `{ id, token, expire_at, message }`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 邀请记录 ID（tenant_admin_invitation.id） |
| token | string | 原始邀请 token（**仅本次返回一次**，用于发送通知；不落库，库中仅存 token_hash） |
| expire_at | timestamp | 邀请过期时间 |
| message | string | 操作结果描述 |

```json
{
  "id": "uuid",
  "token": "raw-invitation-token",
  "expire_at": "2026-08-10T10:00:00Z",
  "message": "admin invitation sent"
}
```

**说明:** 按 `email + username` 在指定租户（`tenant_id = tenantId`）内匹配现有用户；匹配成功则在该租户下新建一条 `tenant_admin_invitation` 记录（`status='inviting'`，`token_hash = SHA-256(token)`，`expire_at = now() + 72h`），**不改变用户角色、不改 `users.status`**（保持 `active` / `disabled`）。触发通知渠道将 `token` 拼接为邀请链接发送。

**错误：**
- 该租户内不存在匹配 `email + username` 的用户 → 404 `TENANT_ADMIN_NOT_FOUND`（不新建用户）
- 该用户已是本租户 `tenant-admin` / `tenant-owner` → 409 `TENANT_ADMIN_ALREADY_ADMIN`
- 该用户在本租户下已存在 `status='inviting'` 的邀请 → 409 `TENANT_INVITATION_PENDING`（应改用重发邀请）

#### 5.4.2 `POST /tenants/{tenantId}/admins/{userId}/invitation/resend` — 重发租户管理员邀请

**AuthZ:** platform-admin, platform-ops

**idempotency_key:** body 必填

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 被邀请用户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| idempotency_key | string (uuid) | 是 | UUID | 幂等键，由客户端生成，放在 request body 中 |

```json
{ "idempotency_key": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response 200:** `{ id, token, expire_at, message }`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 邀请记录 ID（tenant_admin_invitation.id） |
| token | string | 新生成的原始邀请 token（**仅本次返回一次**） |
| expire_at | timestamp | 刷新后的过期时间 |
| message | string | 操作结果描述 |

```json
{
  "id": "uuid",
  "token": "new-raw-invitation-token",
  "expire_at": "2026-08-10T10:00:00Z",
  "message": "admin invitation resent"
}
```

**说明:** 对该用户在本租户下的**最新一条** `tenant_admin_invitation` 记录重新生成 token（更新 `token_hash`）、刷新 `expire_at = now() + 72h`、清空 `accepted_at` / `rejected_at`，且状态回归为 `inviting`。触发通知渠道将新 token 重发。仅 `inviting` / `expired` 状态可重发。

**错误：**
- 该租户内不存在该用户或 `tenantId + userId` 组合无匹配记录 → 404 `TENANT_ADMIN_INVITATION_NOT_FOUND`
- 最新邀请已 `accepted` / `rejected`（终态）→ 409 `TENANT_INVITATION_SETTLED`（不可重发）

#### 5.4.3 `GET /tenant-admins` — 分页查询所有管理员（跨租户）

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Query 参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| tenant_id | uuid | 否 | 全部 | 可选的租户过滤：传入时仅返回该租户内的管理员绑定（role/status 等结果为该租户内的绑定） |
| role | string | 否 | 全部 | 过滤：`tenant-owner` / `tenant-admin`（不含普通成员 `user`） |
| status | string | 否 | 全部 | 过滤：`active` / `disabled`（直接对应 `users.status`） |
| is_inviting | boolean | 否 | 全部 | 过滤：传入 `true` 时仅返回「正在被邀请」（该租户下 `tenant_admin_invitation.status='inviting'`）的用户；传入 `false` 返回非邀请中的用户 |
| search | string | 否 | 无 | email / username 模糊匹配 |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 管理员列表（跨所有租户） |
| items[].id | string | 用户 ID |
| items[].email | string | 邮箱 |
| items[].username | string | 用户名 |
| items[].display_name | string \| null | 显示名 |
| items[].role | string | 角色：tenant-owner / tenant-admin（本端点**仅返回租户所有者、租户管理员与邀请中用户**，不返回普通成员 user）；邀请中用户仍展示其在该租户内原有的角色（若该用户已绑定了角色） |
| items[].status | string | 状态：active / disabled（直接对应 `users.status`） |
| items[].is_inviting | boolean | 是否正在被邀请（仅作标记，不影响 role/status）：`true`（该租户下存在 `tenant_admin_invitation.status='inviting'`）/ `false`（非邀请中） |
| items[].source | string | 来源：`third_party`（oidc: 开头）/ `local`（local: 开头） |
| items[].last_login_at | timestamp \| null | 最后登录时间 |
| items[].tenant | object | 所属租户对象 |
| items[].tenant.id | string | 租户 ID |
| items[].tenant.name | string | 租户名称 |
| items[].tenant.display_name | string | 租户显示名 |
| items[].tenant.mfa_required | boolean | 租户是否强制 MFA |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

```json
{
  "items": [
    {
      "id": "uuid",
      "email": "admin@acme.com",
      "username": "local:acme_admin",
      "display_name": "张三",
      "role": "tenant-admin",
      "status": "active",
      "is_inviting": false,
      "source": "local",
      "last_login_at": "2026-07-24T10:00:00Z",
      "tenant": {
        "id": "uuid",
        "name": "acme",
        "display_name": "Acme Inc.",
        "mfa_required": true
      }
    },
    {
      "id": "uuid",
      "email": "invitee@acme.com",
      "username": "local:carol",
      "display_name": "Carol",
      "role": "user",
      "status": "active",
      "is_inviting": true,
      "source": "local",
      "last_login_at": null,
      "tenant": {
        "id": "uuid",
        "name": "acme",
        "display_name": "Acme Inc.",
        "mfa_required": true
      }
    }
  ],
  "next_cursor": "<base64-cursor>"
}
```

**说明：** 本端点跨所有租户查询管理员列表。JOIN `users` + `user_roles` + `roles` + `tenants` 表（含 `roles` 以返回角色名），并关联 `tenant_admin_invitation` 表。**返回范围仅包含：**
1. 租户所有者（`role='tenant-owner'`）
2. 租户管理员（`role='tenant-admin'`）
3. 正在被邀请的用户（该租户下存在 `tenant_admin_invitation.status='inviting'` 的邀请，`is_inviting=true`）

普通成员（`role='user'`）默认不返回，**仅当该用户正在被邀请（`is_inviting=true`）时才出现在列表**。`is_inviting` 仅作标记（让 BOSS 端知道哪些用户正在被邀请，可按该状态过滤），**不影响用户的 role/status**——邀请中用户仍展示其在该租户内原有的角色（本例中为 `user`）。返回管理员所属租户对象。支持 tenant_id / role / status / is_inviting / search 过滤。按 created_at DESC 排序，游标分页（limit + cursor）返回 CursorPage（items + next_cursor）。

#### 5.4.4 `GET /tenants/{tenantId}/admins/{userId}` — 详情

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 用户 ID |
| username | string | 用户名 |
| email | string | 邮箱 |
| display_name | string \| null | 显示名 |
| role | string | 角色：tenant-owner / tenant-admin / user |
| status | string | 状态：active / disabled |
| source | string | 来源：`third_party`（oidc: 开头）/ `local`（local: 开头） |
| last_login_at | timestamp \| null | 最近登录时间 |
| created_at | timestamp | 创建时间 |
| updated_at | timestamp | 更新时间 |
| is_inviting | boolean | 是否正在被邀请（仅作标记，不影响 role/status）：`true`（该租户下存在 `tenant_admin_invitation.status='inviting'`）/ `false`（非邀请中） |
| tenant | object | 所属租户对象 |
| tenant.id | string | 租户 ID |
| tenant.name | string | 租户名称 |
| tenant.display_name | string | 租户显示名 |
| tenant.mfa_required | boolean | 租户是否强制 MFA |

> 本端点返回该用户的**所有用户信息**（`users` 表全字段，不含冗余的 `tenant_id` 顶层字段，租户信息由下方 `tenant` 对象承载）；出于安全**不返回** `password_hash` 等敏感字段。

```json
{
  "id": "uuid",
  "email": "admin@acme.com",
  "username": "local:acme_admin",
  "display_name": "张三",
  "role": "tenant-admin",
  "status": "active",
  "source": "local",
  "last_login_at": "2026-07-24T10:00:00Z",
  "created_at": "2026-07-01T08:00:00Z",
  "updated_at": "2026-07-24T10:00:00Z",
  "is_inviting": false,
  "tenant": {
    "id": "uuid",
    "name": "acme",
    "display_name": "Acme Inc.",
    "mfa_required": true
  }
}
```

**说明：** 返回该用户所有用户信息（不含 `password_hash` 等敏感字段），并包含所属租户对象。`source` 由 `username` 前缀推断；`role` 由 `user_roles` + `roles` 解析；`is_inviting` 表示该用户是否正在被邀请（仅作标记，便于详情页识别并提供重发邀请等操作）。

#### 5.4.5 `GET /tenants/{tenantId}/admins/{userId}/role` — 查询指定管理员角色与权限

**AuthZ:** platform-admin, platform-ops

**Path 参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**说明:** 查询指定用户在指定租户内的角色及 permissions（4 维度权限模型），前端用于按钮/菜单的显示控制。与 `PUT .../role` 同路径不同方法（GET 查询 / PUT 修改）。

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 用户 ID |
| tenant_id | string \| null | 租户 ID（本端点仅租户账号非空；平台账号为 null） |
| role | string | 当前角色：tenant-owner / tenant-admin / user / auditor（仅租户角色） |
| permissions | object | 4 维度权限对象 |
| permissions.compute | string | 算力实例权限：read / write / none |
| permissions.inference | string | 推理/模型权限：read / write / none |
| permissions.member | string | 成员邀请权限：read / write / none |
| permissions.transfer | string | 所有者移交权限：read / write / none |

```json
{
  "user_id": "uuid",
  "tenant_id": "uuid",
  "role": "tenant-admin",
  "permissions": {
    "compute": "write",
    "inference": "write",
    "member": "write",
    "transfer": "none"
  }
}
```

**说明:** JOIN `user_roles` + `roles` 查询指定用户角色及 `roles.permissions` JSONB 字段直接返回。本端点**只能查询到租户成员的权限**：role ∈ tenant-owner / tenant-admin / user / auditor（租户内 `tenant_id` 非空），直接使用 `roles.permissions` 的 4 维度（compute / inference / member / transfer）。

**平台账户（`tenant_id=null`）权限不可通过本端点查询：** 平台账号不参与租户 4 维权限模型（其权限为平台 4 维 tenant_ops / resource_pool / platform_user / audit_export），其权限由平台侧查询，不在本端点返回。若传入平台账号 `userId`（tenant_id=null），本端点不返回其平台权限。

不调用 Core API。

#### 5.4.6 `PUT /tenants/{tenantId}/admins/{userId}/role` — 修改权限

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| role | string | 是 | `user` \| `auditor` \| `tenant-admin` | 可修改为 user / auditor / tenant-admin（不可设为 tenant-owner） |
| idempotency_key | string (uuid) | 是 | UUID | 幂等键，客户端生成，body 必填（PUT 已纳入 Gateway idempotencyApplies） |

```json
{ "role": "user", "idempotency_key": "550e8400-e29b-41d4-a716-446655440000" }
```

**约束:**
- 可将用户修改为 `user` / `auditor` / `tenant-admin` 角色，不可设为 `tenant-owner`（所有者通过移交端点转移）
- `tenant-owner` 的角色不可被修改（一律 409 `TENANT_OWNER_ROLE_LOCKED`，与是否唯一 owner 无关，不触发 `LAST_TENANT_OWNER`）
- 写审计日志 `action='tenant_admin.change_role'`，`details` 含 `old_role` / `new_role`

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "admin role updated" }
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 422 | ROLE_CHANGE_INVALID | role 不在允许范围内（仅 user / auditor / tenant-admin） |
| 409 | TENANT_OWNER_ROLE_LOCKED | 目标用户为 tenant-owner，角色不可修改 |

#### 5.4.7 `POST /tenants/{tenantId}/transfer-ownership` — 移交所有者

**AuthZ:** platform-admin, platform-ops

**说明:** 由平台运营将某租户的所有者身份移交给该租户内一名 tenant-admin 用户。移交后原所有者降级为 tenant-admin，目标用户升级为 tenant-owner。每个租户仅一人持有 tenant-owner 角色。

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |

**Request:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| target_user_id | uuid | 是 | 被移交为所有者的用户 ID（须为该租户的 tenant-admin） |
| idempotency_key | string (uuid) | 是 | 幂等键，客户端生成，body 必填 |

```json
{ "target_user_id": "uuid", "idempotency_key": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "ownership transferred" }
```

**约束:**
- 由 `platform-admin` / `platform-ops` 发起（AuthZ 中间件按角色校验）；tenant-owner 不可直接发起
- `target_user_id` 必须为该租户内 `status='active'` 且角色为 `tenant-admin` 的用户；否则 422 `TRANSFER_TARGET_INVALID`
- 移交为原子操作：原所有者 `user_roles` 中 tenant-owner 行 → 改绑 tenant-admin；目标用户 `user_roles` 中 tenant-admin 行 → 改绑 tenant-owner
- 写审计日志 `action='tenant_admin.transfer_ownership'`，`details` 含 `old_owner_id` / `new_owner_id`
- **所有者唯一且不能没有：** 每个租户恒有且仅有一名 tenant-owner。移交是允许的（移交后由目标用户接任 owner，租户仍保持唯一 owner），**不受** `LAST_TENANT_OWNER` 保护限制；该保护仅作用于会导致"租户无 owner"的操作（删除/禁用/降级）

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 403 | FORBIDDEN | 当前用户无平台运营权限（非 platform-admin / platform-ops） |
| 422 | TRANSFER_TARGET_INVALID | 目标用户不存在、非 active、或非 tenant-admin 角色 |

#### 5.4.8 `POST /tenants/{tenantId}/admins/{userId}/reset-password` — 重置密码

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| new_password | string | 是 | 8-64 字符；必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类；必须与旧密码不同 | 新密码（前端**直接传明文**，由 HTTPS 保护传输；后端 bcrypt(cost=12) 写入 users.password_hash） |
| idempotency_key | string (uuid) | 是 | UUID | 幂等键，客户端生成，body 必填 |

```json
{ "new_password": "Ab3x!Yz9", "idempotency_key": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "password reset" }
```

**约束:**
- `new_password` 必须与旧密码不同，否则 422 `PASSWORD_SAME_AS_OLD`
- 后端校验：bcrypt.Compare(new_password, users.password_hash) 通过即说明与旧密码相同 → 422
- 禁用态用户（`users.status='disabled'`，含已软删除）不可重置密码 → 404 `TENANT_ADMIN_NOT_FOUND`（无可操作的目标管理员）

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 422 | PASSWORD_SAME_AS_OLD | 新密码与旧密码相同 |
| 400 | VALIDATION_FAILED | 密码复杂度不满足要求 |

#### 5.4.9 `POST /tenants/{tenantId}/admins/{userId}/disable` — 禁用

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**说明:** 设置 `users.status='disabled'`；用户不能再登录。

**Request:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| idempotency_key | string (uuid) | 是 | 幂等键，客户端生成，body 必填 |

**最后租户所有者保护:** 若该用户为该租户内**唯一**的活跃 `tenant-owner`（active 数 ≤ 1，排除目标），则返回 422 `LAST_TENANT_OWNER`，防止租户失去所有者。管理员（tenant-admin）不受此保护。

**审计:** 写审计日志 `action='tenant_admin.disable'`，`details` 含 `target_id`。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "admin disabled" }
```

#### 5.4.10 `POST /tenants/{tenantId}/admins/{userId}/enable` — 启用

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**说明:** `users.status='active'`，恢复登录。

**Request:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| idempotency_key | string (uuid) | 是 | 幂等键，客户端生成，body 必填 |

**审计:** 写审计日志 `action='tenant_admin.enable'`，`details` 含 `target_id`。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "admin enabled" }
```

#### 5.4.11 `DELETE /tenants/{tenantId}/admins/{userId}` — 删除（软删除）

**AuthZ:** platform-admin, platform-ops

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 用户 ID |

**说明:** 软删除用户，设置 `users.is_deleted=TRUE`、`users.deleted_at=now()`、`users.status='disabled'`；保留审计可追溯。tenant-owner 不可被删除（409 `TENANT_OWNER_ROLE_LOCKED`）。

**幂等性:** 本端点为 `DELETE`，**不做幂等**（不要求客户端提供 `idempotency_key`）。软删除重复调用结果一致，无需按幂等键回放。

**最后租户所有者保护:** 若该用户为该租户内**唯一**的活跃 `tenant-owner`（active 数 ≤ 1，排除目标），则返回 422 `LAST_TENANT_OWNER`，防止租户失去所有者。管理员（tenant-admin）不受此保护。

**审计:** 写审计日志 `action='tenant_admin.delete'`，`details` 含 `target_id`（软删除，保留可追溯）。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "admin deleted" }
```

#### 5.4.12 `GET /tenants/{tenantId}/admins/{userId}/audit-logs` — 分页查看管理员操作历史

**AuthZ:** platform-admin, platform-ops, platform-readonly

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| tenantId | uuid | 是 | 租户 ID |
| userId | uuid | 是 | 管理员用户 ID |

**Query 参数:**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| action | string | 否 | 全部 | 过滤操作类型 |
| result | string | 否 | 全部 | 过滤：`success` / `failed` |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 操作历史列表（按 created_at DESC 排序） |
| items[].id | string | 记录 ID |
| items[].tenant_id | string \| null | 租户 ID |
| items[].user_id | string \| null | 操作者 user_id |
| items[].request_id | string \| null | 关联请求 ID |
| items[].action | string | 操作类型 |
| items[].resource | string | 操作资源类型 |
| items[].result | string | 结果：success / failed |
| items[].details | object \| null | 操作详情（JSON） |
| items[].ip_address | string \| null | 操作者 IP |
| items[].user_agent | string \| null | 操作者 User-Agent |
| items[].created_at | timestamp | 记录时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

**说明：** 查询 `audit_logs` 表中 `tenant_id = $tenantId AND user_id = $userId` 的记录，按 `created_at DESC` 排序，游标分页（limit + cursor）返回 CursorPage（items + next_cursor）。不调用 Core API。

### 5.5 PlatformAdmins 路径

#### 5.5.1 `POST /platform-admins` — 创建平台账号

**AuthZ:** platform-admin only

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| email | string | 是 | RFC 5322，全局唯一（`idx_users_platform_email`） | 平台账号邮箱 |
| username | string | 是 | 1-64 字符，全局唯一（`idx_users_platform_username`） | 平台账号用户名 |
| display_name | string | 是 | 1-128 字符 | 平台账号显示名 |
| role | string | 是 | `platform-admin` \| `platform-ops` \| `platform-readonly` | 平台角色（绑定到对应 roles 行） |
| password | string | 是 | 8-64 字符；必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类 | 初始密码（前端**直接传明文**，由 HTTPS 保护传输；后端 bcrypt(cost=12) 写入 users.password_hash；永不明文返回、不写入日志/审计） |

```json
{
  "email": "ops@ani.io",
  "username": "ops_user",
  "display_name": "运维管理员",
  "role": "platform-ops",
  "password": "Ab3x!Yz9"
}
```

**Response 200:** `{ id, message }`

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uuid | 用户 ID |
| message | string | 操作结果描述 |

```json
{
  "id": "uuid",
  "message": "platform admin created"
}
```

**说明:** 创建平台账号（`tenant_id IS NULL`），绑定指定平台角色，密码由调用方提供（非系统生成），不返回 temporary_password。

#### 5.5.2 `GET /platform-admins` — 列表

**AuthZ:** platform-admin

**Query 参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| limit | integer | 否 | 20（max 100） | 每页数量 |
| cursor | string | 否 | 无 | 上一页返回的 next_cursor |
| role | string | 否 | 全部 | 过滤：`platform-admin` / `platform-ops` / `platform-readonly` |
| status | string | 否 | 全部 | 过滤：`active` / `disabled`（直接对应 `users.status`） |
| source | string | 否 | 全部 | 过滤：`builtin` / `imported`（对应 `users.source`） |
| search | string | 否 | 无 | email / username 模糊匹配 |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 平台账号列表 |
| items[].id | string | 用户 ID |
| items[].username | string | 用户名 |
| items[].display_name | string \| null | 显示名 |
| items[].role | string | 角色：platform-admin / platform-ops / platform-readonly |
| items[].status | string | 状态：active / disabled |
| items[].source | string | 来源：`third_party`（oidc: 开头）/ `local`（local: 开头） |
| items[].last_login_at | timestamp \| null | 最后登录时间 |
| next_cursor | string \| null | 下一页游标；null 表示无更多数据 |

**说明：** 按 created_at DESC 排序，游标分页（limit + cursor）返回 CursorPage（items + next_cursor）。

#### 5.5.3 `GET /platform-admins/{userId}` — 详情

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 用户 ID |
| email | string | 邮箱 |
| username | string | 用户名 |
| display_name | string \| null | 显示名 |
| role | string | 角色：platform-admin / platform-ops / platform-readonly |
| status | string | 状态：active / disabled |
| source | string | 来源：`third_party`（oidc: 开头）/ `local`（local: 开头） |
| last_login_at | timestamp \| null | 最后登录时间 |
| created_at | timestamp | 创建时间 |

#### 5.5.3b `GET /platform-admins/{userId}/permissions` — 查询指定运营账号权限

**AuthZ:** platform-admin

**说明:** 查询指定运营账号的角色及 permissions（4 维度权限模型），前端用于按钮/菜单的显示控制。userId 通过路径参数传入。

**Response 200:**

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 当前用户 ID |
| role | string | 当前角色：platform-admin / platform-ops / platform-readonly |
| permissions | object | 4 维度平台权限对象 |
| permissions.tenant_ops | string | 租户开通/冻结权限：read / write / none |
| permissions.resource_pool | string | 平台资源池权限：read / write / none |
| permissions.platform_user | string | 平台运营账号权限：read / write / none |
| permissions.audit_export | string | 审计导出权限：read / write / none |

```json
{
  "user_id": "uuid",
  "role": "platform-ops",
  "permissions": {
    "tenant_ops": "write",
    "resource_pool": "write",
    "platform_user": "none",
    "audit_export": "none"
  }
}
```

**说明:** 后端从 JWT token 提取 user_id，JOIN `user_roles` + `roles` 查询用户角色及 `roles.permissions` JSONB 字段直接返回。platform-admin 返回 `permissions: {"tenant_ops":"write","resource_pool":"write","platform_user":"write","audit_export":"write"}`（超级权限）。

#### 5.5.4 `PUT /platform-admins/{userId}/role` — 修改角色

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| role | string | 是 | `platform-admin` \| `platform-ops` \| `platform-readonly` | 平台角色（改 user_roles 绑定：先 DELETE 旧平台角色，再 INSERT 新角色） |

**约束:** 不支持修改用户名；不支持直接改 `users.status`，启用/禁用必须通过 §5.5.6 / §5.5.7 专用端点。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "platform admin updated" }
```

#### 5.5.5 `POST /platform-admins/{userId}/reset-password` — 重置密码

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**Request:**

| 字段 | 类型 | 必填 | 约束 | 说明 |
|------|------|------|------|------|
| new_password | string | 是 | 8-64 字符；必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类；必须与旧密码不同 | 新密码（前端**直接传明文**，由 HTTPS 保护传输；后端 bcrypt(cost=12) 写入 users.password_hash） |

```json
{ "new_password": "Ab3x!Yz9" }
```

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "password reset" }
```

**约束:**
- `new_password` 必须与旧密码不同，否则 422 `PASSWORD_SAME_AS_OLD`
- 后端校验：bcrypt.Compare(new_password, users.password_hash) 通过即说明与旧密码相同 → 422

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 422 | PASSWORD_SAME_AS_OLD | 新密码与旧密码相同 |
| 400 | VALIDATION_FAILED | 密码复杂度不满足要求 |

#### 5.5.6 `POST /platform-admins/{userId}/disable` — 禁用

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**说明:** 设置 `users.status='disabled'`；账号不能再登录。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "platform admin disabled" }
```

#### 5.5.7 `POST /platform-admins/{userId}/enable` — 启用

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**说明:** `users.status='active'`，恢复登录。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "platform admin enabled" }
```

#### 5.5.8 `DELETE /platform-admins/{userId}` — 删除（软删除）

**AuthZ:** platform-admin

**Path 参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | uuid | 是 | 用户 ID |

**最后管理员保护:** 删除/禁用前若活跃 platform-admin 数 ≤ 1 → 422 `LAST_PLATFORM_ADMIN`。

**Response 200:** `{ id, message }`

```json
{ "id": "uuid", "message": "platform admin deleted" }
```

### 5.6 错误响应统一格式

```json
{
  "error": {
    "code": "TENANT_NAME_CONFLICT",
    "message": "Tenant name already exists",
    "details": { "name": "acme" },
    "request_id": "uuid",
    "idempotency_key": "..."
  }
}
```

### 5.7 错误码总表（新增）

| code | HTTP | 场景 |
|------|------|------|
| TENANT_NOT_FOUND | 404 | 租户不存在（含 Core 层返回：绑定时租户在 Core 侧不存在） |
| TENANT_NAME_CONFLICT | 409 | name 已被活动租户占用 |
| TENANT_STATE_INVALID | 409 | 状态转换非法 |
| TENANT_PLAN_NOT_FOUND | 404 | 套餐不存在 |
| QUOTA_NOT_FOUND | 404 | Core 层返回：改限额时租户配额行不存在（未绑定套餐） |
| PLAN_CODE_CONFLICT | 409 | 套餐 code 已存在 |
| PLAN_STATE_INVALID | 409 | 套餐状态转换非法（如 active 再激活、draft 不可直接 disable） |
| TENANT_PLAN_IN_USE | 409 | 有租户已关联该套餐，不可删除 |
| QUOTA_ALREADY_EXISTS | 409 | Core 层返回：绑定新建配额时配额行已存在 |
| PLAN_NOT_ACTIVE | 422 | 套餐状态非 active，不可被租户引用 |
| QUOTA_RESOURCE_NOT_REGISTERED | 422 | resource_type 未注册或 enabled=false |
| GRPC_CLIENT_UNAVAILABLE | 502 | Core 服务不可用（配额元数据/配额下发失败） |
| TENANT_FROZEN | 403 | 租户已冻结，禁止登录 |
| TENANT_DISABLED | 403 | 租户已禁用，禁止登录 |
| TENANT_ADMIN_NOT_FOUND | 404 | 租户管理员不存在（含邀请时无匹配用户、重置密码遇到禁用态用户） |
| TENANT_ADMIN_ALREADY_ADMIN | 409 | 邀请时该用户已是本租户 tenant-admin / tenant-owner |
| TENANT_INVITATION_PENDING | 409 | 该用户在本租户下已有 status='inviting' 的待接受邀请（应改用重发） |
| TENANT_ADMIN_INVITATION_NOT_FOUND | 404 | 重发邀请时该租户内无匹配的邀请记录 |
| TENANT_INVITATION_SETTLED | 409 | 最新邀请已 accepted / rejected（终态，不可重发） |
| TENANT_SSO_CONFIG_INVALID | 422 | SSO 配置校验失败（issuer_url 不合法 / client_id 缺失 / client_secret_ref 未指向有效 K8s Secret / 开启 sso_enabled 但 provider / issuer_url / client_id / client_secret_ref 为空） |
| PLATFORM_ADMIN_NOT_FOUND | 404 | 平台账号不存在 |
| LAST_PLATFORM_ADMIN | 422 | 最后管理员保护触发 |
| LAST_TENANT_OWNER | 422 | 最后租户所有者保护触发（删除/禁用/降级唯一活跃 tenant-owner，防止"租户无 owner"；移交除外） |
| MFA_REQUIRED | 403 | 该租户已启用 mfa_required，但用户未完成 MFA 二次校验 |
| IDEMPOTENCY_CONFLICT | 409 | 同 key 不同 body |
| IDEMPOTENCY_KEY_INVALID | 400 | idempotency_key body 字段格式错 |
| VALIDATION_FAILED | 400 | 请求体校验失败 |
| FORBIDDEN | 403 | 角色不匹配 |
| QUOTA_CHANGE_REQUEST_DUPLICATE | 409 | 该租户该配额维度已有待审核的申请 |
| QUOTA_CHANGE_REQUEST_INVALID | 422 | resource_type 不存在 / new_value 为负数 |
| TENANT_OWNER_ROLE_LOCKED | 409 | 目标用户为 tenant-owner，角色不可修改/删除 |
| TRANSFER_TARGET_INVALID | 422 | 移交所有者目标用户不存在、非 active、或非 tenant-admin 角色 |
| ROLE_CHANGE_INVALID | 422 | role 不在允许范围内（仅 user / auditor / tenant-admin） |
| PASSWORD_SAME_AS_OLD | 422 | 重置密码时新密码与旧密码相同 |

> 计费相关错误码（BILLING_*）暂不实现，后续 PR 再补充。

> 配额相关错误码（`QUOTA_RESOURCE_NOT_REGISTERED` / `TENANT_NOT_FOUND` / `QUOTA_NOT_FOUND` / `QUOTA_ALREADY_EXISTS` / `GRPC_CLIENT_UNAVAILABLE`）由网关层在 `businessCodeByHTTP` 中定义 HTTP 状态映射，确保 Core 返回的业务错误能正确还原为 HTTP 状态码。配额自动收紧不报错，Core API 通过响应中的 `tightened` 字段返回，前端据此判断是否收紧。

---

## §6 服务设计

### 6.1 tenant-service 模块结构

```
repo/services/tenant-service/
├── cmd/
│   └── main.go                      # 入口，加载配置，启动 gRPC server（bootstrap.RunGRPC）；每个 service 经 Register(grpc.Server) 挂载
├── internal/service/                # gRPC server 层（仿 model-service）：RPC handler + 业务逻辑，Register(*grpc.Server)
│   ├── tenant_service.go            # gRPC TenantService server（BindPlanQuota）；不含 Quotas 子路径业务；CreateTenant 调用 Core QuotaService 初始化
│   ├── tenant_admin_service.go
│   ├── tenant_plan_service.go       # gRPC TenantPlanService server（10 个 RPC，嵌入 UnimplementedTenantPlanServiceServer + Register）
│   ├── platform_admin_service.go
│   └── audit_helper.go
├── internal/repo/                   # tenant-service 自有仓储层（配额套餐相关 ports 由 pkg/ports 下沉至此，Services 自包含、不依赖 Core 的 ports/adapters）
│   ├── ports/                       # 接口与领域模型：TenantPlanStore / TenantStore / TenantPlanAuditStore（配额套餐域审计）/ QuotaSvcClient
│   └── adapters/                    # 实现：postgres（PostgresTenantPlanStore / PostgresTenantPlanAuditStore / PostgresTenantStore）+ core（QuotaSvcClient）
└── internal/wiring/
    └── wiring.go                    # 依赖注入：ports ↔ adapters ↔ services；注入 Core QuotaService / QuotaMetaService 客户端
```

> 传输层说明：QUOTA 套餐（tenant-plan）功能已按 gRPC 模式落地——`internal/service/tenant_plan_service.go` 为 gRPC server（`Register(server *grpc.Server)`）；对外 REST 由 ani-gateway 提供（`/api/v1/svc/tenant-plans*`、`/api/v1/svc/tenants/:tenantId/plan`），网关注册对应路由后经 gRPC client 转发到 tenant-service（`TENANT_SERVICE_ADDR`，缺省 `127.0.0.1:9105`，与 tenant-service `GRPC_PORT`/`HEALTH_PORT` 一致）。

### 6.2 端口接口（pkg/ports）

```go
// services/tenant-service/internal/repo/ports/tenant_store.go
package ports

type CreateTenantInput struct {
    Name        string   // ^[a-z0-9-]{3,40}$，活动租户间唯一
    DisplayName string   // 1-128 字符
    Email       string   // 租户联系邮箱 RFC 5322
    PlanID      uuid.UUID
    AdminEmail    string // 首位管理员邮箱 RFC 5322
    AdminName     string // 首位管理员用户名 1-64 字符
    AdminPassword string // 首位管理员初始密码 8-64 字符 + 至少 3 类（bcrypt cost=12 写入 users.password_hash；永不明文返回）
}

type TenantListFilter struct {
    Limit  int    // 每页数量，默认 20，max 100
    Cursor string // 上一页返回的 next_cursor；空=第一页
    Status string // active / frozen / disabled；空=全部
    Search string // name / display_name 模糊匹配
}

type TenantListItem struct {
    ID          uuid.UUID
    Name, DisplayName string
    PlanID      uuid.UUID
    PlanCode    string  // service 层 JOIN tenant_plans 读取
    Status      string
    AdminCount   int     // COUNT(users WHERE tenant_id = id AND status='active' AND role='tenant-admin')
    CreatedAt   time.Time
}

type TenantStore interface {
    Create(ctx context.Context, in CreateTenantInput) (Tenant, error)
    GetByID(ctx context.Context, id uuid.UUID) (Tenant, error)
    List(ctx context.Context, filter TenantListFilter) (PagedResult[TenantListItem], error)
    Update(ctx context.Context, id uuid.UUID, in UpdateTenantInput) (Tenant, error)
    UpdatePlan(ctx context.Context, id uuid.UUID, planID uuid.UUID) (Tenant, error) // 切换套餐，校验 active
    Freeze(ctx context.Context, id uuid.UUID, reason string) (Tenant, error)
    Unfreeze(ctx context.Context, id uuid.UUID) (Tenant, error)
    Disable(ctx context.Context, id uuid.UUID, reason string) (Tenant, error)
}

type Tenant struct {
    ID uuid.UUID
    Name, DisplayName, ContactEmail, Status string // status ∈ {active, frozen, disabled}
    PlanID uuid.UUID          // 外键 → tenant_plans.id（tenants 表不存 plan_code）
    FrozenAt, DisabledAt *time.Time
    CreatedAt, UpdatedAt time.Time
    // MFA/SSO 不在 Tenant struct 内；由 tenant_auth 表承载，通过 TenantAuthStore 访问
    // 配额不在 Tenant struct 内；由 Core resource_quota 表承载，通过 Core QuotaService port 访问
}

// repo/pkg/ports/tenant_auth_store.go
type TenantAuthStore interface {
    Create(ctx context.Context, tenantID uuid.UUID) error                                       // CreateTenant 时同步建行（全部默认值）
    Get(ctx context.Context, tenantID uuid.UUID) (TenantAuth, error)                             // 读取 MFA/SSO 配置
    GetSSOConfig(ctx context.Context, tenantID uuid.UUID) (SSOConfigView, error)                // B1 SSO 查询（含 sso_enabled 状态）
    UpdateSSO(ctx context.Context, tenantID uuid.UUID, in UpdateSSOInput) (SSOConfigView, error) // B1 修改 SSO 状态与 IdP 提供商（部分更新）
    UpdateMFARequired(ctx context.Context, tenantID uuid.UUID, required bool) error             // B2 MFA 强制开关
}

type TenantAuth struct {
    TenantID        uuid.UUID
    MFARequired     bool       // B2
    SSOEnabled      bool       // B1: SSO 开关（TRUE=已开启，FALSE=已关闭）
    SSOProvider     *string    // B1: 'oidc' | 'custom' | nil
    CreatedAt, UpdatedAt time.Time
}

// SSOConfigView 是 GET /tenants/{tenantId}/auth/sso 的返回结构
type SSOConfigView struct {
    SSOEnabled bool       // 对应 tenant_auth.sso_enabled
    Provider   *string    // 对应 tenant_auth.sso_provider
    UpdatedAt  time.Time
}

// UpdateSSOInput 支持 SSO 部分更新（指针字段为 nil 表示不更新）
type UpdateSSOInput struct {
    SSOEnabled *bool      // SSO 开关；nil=不更新
    Provider   *string    // IdP 提供商（写入 sso_provider）；nil=不更新
}

// repo/pkg/ports/quota_change_request_store.go
type QuotaChangeRequestStore interface {
    Create(ctx context.Context, req QuotaChangeRequest) (QuotaChangeRequest, error)
    ListByTenant(ctx context.Context, tenantID uuid.UUID, status string) ([]QuotaChangeRequest, error)
    GetByID(ctx context.Context, id uuid.UUID) (QuotaChangeRequest, error)
    UpdateStatus(ctx context.Context, id uuid.UUID, status string, reviewedBy uuid.UUID) error
}

type QuotaChangeRequest struct {
    ID            uuid.UUID
    TenantID      uuid.UUID
    ResourceType  string
    OldValue      *int64
    NewValue      int64
    RequestedBy   uuid.UUID
    Status        string    // pending / approved / rejected
    ReviewedBy    *uuid.UUID
    ReviewedAt    *time.Time
    CreatedAt, UpdatedAt time.Time
}

type QuotaItemView struct {
    ResourceType  string
    DisplayName   string
    Used          int64
    Total         int64
    Unit          string
}

// services/tenant-service/internal/repo/ports/tenant_plan_store.go

// TenantPlanStatus 套餐状态机：draft → active → disabled。
type TenantPlanStatus string

const (
    TenantPlanStatusDraft    TenantPlanStatus = "draft"
    TenantPlanStatusActive   TenantPlanStatus = "active"
    TenantPlanStatusDisabled TenantPlanStatus = "disabled"
)

// ParseTenantPlanStatusFilter 解析列表过滤用 status：空=全部；非法值报错。
func ParseTenantPlanStatusFilter(raw string) (TenantPlanStatus, error)

type TenantPlanStore interface {
    Create(ctx context.Context, in CreateTenantPlanInput) (TenantPlan, error)
    GetByID(ctx context.Context, id uuid.UUID) (TenantPlan, error)   // 主键查询（tenants.plan_id 外键目标）
    List(ctx context.Context, filter TenantPlanListFilter) (TenantPlanListResult, error)  // 游标分页（limit + cursor + total + next_cursor）；返回平铺 TenantPlanListItem
    Update(ctx context.Context, id uuid.UUID, in UpdateTenantPlanInput) (TenantPlan, error)  // PUT /tenant-plans/{planId} 修改 name/description（nil=不更新，空串=清空）；亦用于 service 层内部
    Activate(ctx context.Context, id uuid.UUID) (TenantPlan, error) // draft OR disabled → active
    Disable(ctx context.Context, id uuid.UUID) (TenantPlan, error)  // active → disabled
    Delete(ctx context.Context, id uuid.UUID) error  // 软删除（is_deleted=TRUE, deleted_at=now()）；校验无租户关联
    // quota_limits 相关
    GetQuotaLimits(ctx context.Context, planID uuid.UUID) ([]PlanQuotaLimit, error)      // 读取套餐各维度限额原始行（保留 NULL 原始语义）；预留供其余业务需要
    UpdateQuotaLimits(ctx context.Context, planID uuid.UUID, items []PlanQuotaLimitInput) error // UPSERT plan_quota_limits（PUT /tenant-plans/{planId}/quota-limits；Total 由 service 层物化为具体值后写入）
    ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]BoundTenant, error) // 查询绑定套餐的租户摘要（GET /tenant-plans/{planId}/tenants）
    ListBindableTenants(ctx context.Context, planID uuid.UUID) ([]BoundTenant, error) // 查询可绑定该套餐的租户（status != disabled 且 plan_id IS DISTINCT FROM planID）（GET /tenant-plans/{planId}/bindable-tenants）
    GetApprovedQuotaChanges(ctx context.Context, tenantID uuid.UUID) ([]ApprovedQuotaChange, error) // 已审批（status='approved'）维度，绑定/修改限额同步时保留不覆盖
}

// 注：GetQuotaLimitViews 不在 store 接口中——由 service 层 buildQuotaLimitViews 函数组装
// （store.GetQuotaLimits 获取原始行 + Core QuotaSvcClient.ListQuotaMeta 获取元数据，COALESCE total, default_quota 兜底）。

// services/tenant-service/internal/repo/ports/core_quota.go

// CoreQuotaItem 表示下发给 Core 的单个配额维度项（请求侧）。
type CoreQuotaItem struct {
    ResourceType string
    Total        int64  // 限额值（须为具体数值，禁止 NULL）
}

// CoreQuotaResult 表示 Core 对单个维度下发后的返回结果（响应侧）。
type CoreQuotaResult struct {
    ResourceType string
    Total        int64  // 生效后的限额值
    Used         int64  // 当前已用
    Reserved     int64  // 当前预留
    Tightened    bool   // 是否被自动收紧（total < used+reserved 时 Core 自动收紧）
}

// QuotaMeta 表示 Core GET /admin/quota-meta 返回的单个维度。
type QuotaMeta struct {
    ResourceType string
    Enabled      bool
    DefaultQuota int64
    DisplayName  string
    Unit         string
    IsDiscrete   bool
}

type QuotaSvcClient interface {
    ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)                              // GET /admin/quota-meta
    GetQuota(ctx context.Context, tenantID uuid.UUID) ([]CoreQuotaResult, error)        // GET /admin/tenants/{id}/quota
    CreateQuota(ctx context.Context, tenantID uuid.UUID, items []CoreQuotaItem) ([]CoreQuotaResult, error)  // POST /admin/tenants/{id}/quota
    PutQuota(ctx context.Context, tenantID uuid.UUID, items []CoreQuotaItem) ([]CoreQuotaResult, error)    // PUT /admin/tenants/{id}/quota
    DeleteQuota(ctx context.Context, tenantID uuid.UUID) error                          // DELETE /admin/tenants/{id}/quota
}

// TenantPlanListItem：套餐列表/详情的查询视图（仅在查询接口返回，不进 TenantPlan 实体）。
// 平铺字段（不复用 TenantPlan，不含 IsDeleted/DeletedAt）。
type TenantPlanListItem struct {
    ID          uuid.UUID
    Code        string
    Name        string
    Description string
    Status      TenantPlanStatus // draft | active | disabled
    TenantCount int64  // 绑定租户数量（COUNT tenants WHERE plan_id=? AND status != 'disabled'）
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// TenantPlanListResult：套餐列表查询返回（游标分页）。
type TenantPlanListResult struct {
    Items      []TenantPlanListItem // 本页数据
    Total      int                  // 满足过滤条件的总条数（用于前端分页/计数）
    NextCursor string               // 下一页游标；空串 = 已无更多数据
}

type PlanQuotaLimit struct {
    PlanID       uuid.UUID
    ResourceType string  // 外键 → resource_quota_meta.resource_type
    Total        *int64  // nil = 用 resource_quota_meta.default_quota（保留原始语义）
}

type PlanQuotaLimitInput struct {
    ResourceType string
    Total        *int64  // nil = 用默认值
}

type PlanQuotaLimitView struct {
    ResourceType    string
    DisplayName    string   // 来自 resource_quota_meta.display_name
    Unit           string   // 来自 resource_quota_meta.unit
    Total          int64    // 兜底后的展示值 COALESCE(total, default_quota)；丢弃空语义（GET /quota-limits 总是具体数值；绑定下发亦取该值）
}

type TenantPlan struct {
    ID          uuid.UUID        // 主键，被 tenants.plan_id 引用
    Code        string           // 业务代码
    Name        string           // 套餐名称
    Description string
    Status      TenantPlanStatus // status ∈ {draft, active, disabled}；Code 保留 partial unique index（WHERE is_deleted = FALSE）
    IsDeleted   bool
    DeletedAt   *time.Time
    TenantCount int64            // 绑定租户数（仅读路径填充；Create 为 0）
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type BoundTenant struct {
    ID          uuid.UUID
    Name        string
    DisplayName string
    Status      string // active | frozen | disabled
}

type ApprovedQuotaChange struct {
    TenantID     uuid.UUID
    ResourceType string // 已审批通过的配额维度
}

type CreateTenantPlanInput struct {
    Code, Name       string
    Description      string
    QuotaLimits      []PlanQuotaLimitInput  // 可选；每项 {resource_type, total}
}

type TenantPlanListFilter struct {
    Limit  int    // 每页数量，default 20，max 100
    Cursor string // 上一页返回的 next_cursor；空串 = 第一页
    Status TenantPlanStatus // "" = 全部；否则 draft | active | disabled
    Search string // 模糊匹配 name
}

type UpdateTenantPlanInput struct {
    // PUT /tenant-plans/{planId} 修改套餐基本信息；nil=不更新，空串=清空
    Name             *string
    Description      *string
}
```

> 配额相关端口（`QuotaSvcClient`）在 tenant-service 的 `ports/core_quota.go` 中定义，封装 Core 配额 API 的 5 个方法：`ListQuotaMeta`（GET /admin/quota-meta）、`GetQuota`（GET /admin/tenants/{id}/quota）、`CreateQuota`（POST /admin/tenants/{id}/quota）、`PutQuota`（PUT /admin/tenants/{id}/quota）、`DeleteQuota`（DELETE /admin/tenants/{id}/quota）。BOSS 前端通过 Services 网关 `GET /quota-meta` 代理调用 Core。

其余接口（TenantAdminStore / PlatformAdminStore / TenantLifecycleStore / AuditLogStore）签名同理，略。

**TenantLifecycleStore** 关键方法：

```go
// repo/pkg/ports/tenant_lifecycle_store.go
type TenantLifecycleStore interface {
    Append(ctx context.Context, in LifecycleEvent) error                                   // 写入一条状态变更记录
    ListByTenant(ctx context.Context, tenantID uuid.UUID, limit int, cursor string) (PagedResult[LifecycleEvent], error)  // 按租户查询生命周期时间线（游标分页）
}

type LifecycleEvent struct {
    ID         uuid.UUID
    TenantID   uuid.UUID
    Action     string   // active / frozen / disabled
    Reason     string
    UserID     *uuid.UUID  // nil = 系统触发
    RequestID  string
    CreatedAt  time.Time
}
```

**TenantAdminStore** 关键方法：

```go
type TenantAdminStore interface {
    ListAdmins(ctx context.Context, tenantID uuid.UUID, filter AdminListFilter) (PagedResult[TenantAdmin], error)  // AdminListFilter 含 Limit/Cursor（游标分页）+ Status/Search/Role 过滤
    GetAdmin(ctx context.Context, tenantID, userID uuid.UUID) (TenantAdmin, error)
    UpdateAdmin(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, in UpdateAdminInput) (TenantAdmin, error) // 改 user_roles 绑定（admin ↔ user / auditor / tenant-admin）
    ResetPassword(ctx context.Context, tenantID, userID uuid.UUID, newPassword string) error  // 由调用方提供新密码；校验与旧密码不同
    Disable(ctx context.Context, tenantID, userID uuid.UUID) error  // UPDATE users SET status='disabled'
    Enable(ctx context.Context, tenantID, userID uuid.UUID) error   // UPDATE users SET status='active'
    SoftDelete(ctx context.Context, tenantID, userID uuid.UUID) error
}

type TenantAdmin struct {
    ID         uuid.UUID
    TenantID   uuid.UUID
    Email      string
    Username   string
    DisplayName string // users.display_name（可空）
    Status     string // 直接读 users.status（active / disabled）
    Role       string // 由 service 层 JOIN user_roles + roles 得到（tenant-owner / tenant-admin / user / auditor）
    Source     string // 由 username 前缀推断：oidc: → third_party，local: → local
    LastLoginAt *time.Time
    CreatedAt  time.Time
}
```

> BillingRecordStore 及计费相关端口暂不实现，后续 PR 再补充。

### 6.3 service 层业务规则

#### 6.3.1 TenantService.CreateTenant

```
输入校验
  - name 正则 ^[a-z0-9-]{3,40}$
  - name 活动租户间唯一（全局 UNIQUE 约束保证）
  - display_name 1-128 字符（可改）
  - email（租户联系邮箱）RFC 5322；与首位管理员邮箱 admin_email 不同字段
  - plan_id 必填，对应 tenant_plans.status='active'；否则 422 PLAN_NOT_ACTIVE
  - admin_email RFC 5322，admin_username 不含 ':' 且非空
  - admin_password 8-64 字符；必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类；否则 400 VALIDATION_FAILED

幂等检查（由 Gateway 中间件在到达 service 之前完成）
  - 缓存 key: idempotency:platform::POST:/api/v1/svc/tenants:{sha256(body.idempotency_key)}
  - 命中且已 completed → 直接回放响应（Idempotent-Replay: true）
  - 命中且 processing → 409 IDEMPOTENCY_IN_PROGRESS
  - 未命中 → SETNX 抢占 processing，请求完成后 SET completed

事务（service 层）
  BEGIN
    -- 从 tenant_plans 读取模板信息（按 plan_id 主键查询）
    SELECT id, code
      FROM tenant_plans WHERE id = plan_id AND status='active'
    IF NOT FOUND THEN 422 PLAN_NOT_ACTIVE

    -- 写入 tenants 表（仅 6 个输入字段 + 状态初始化；不存 MFA/SSO 字段；不存配额字段；不存 slug 列）
    INSERT tenants (name, display_name, contact_email, plan_id,
                    status='active')
      VALUES (name, display_name, email, plan_id, ...)

    -- 写入 tenant_auth 表（全部默认值，1:1 关系）
    INSERT INTO tenant_auth (tenant_id) VALUES (tenant.id)

    INSERT users (tenant_id, status='active', email=admin_email, username=admin_name, display_name=admin_name, password_hash, is_deleted=FALSE, deleted_at=NULL)
      VALUES (...)
      RETURNING id AS new_user_id
      -- password_hash = bcrypt(admin_password, 12)
    -- 绑定 tenant-owner 内置角色（首位管理员为租户所有者，roles.tenant_id IS NULL AND name='tenant-owner'）
    INSERT INTO user_roles (user_id, role_id)
      SELECT new_user_id, r.id FROM roles r
      WHERE r.tenant_id IS NULL AND r.name = 'tenant-owner'
    -- 复用现有 audit_logs 分区表，写入 details JSONB
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor_user_id, request_id, 'tenant.create', 'tenant', 'success',
              jsonb_build_object('target_id', tenant.id, 'idempotency_key', idem_key, 'plan_id', plan_id))
    INSERT INTO tenant_lifecycle (tenant_id, action, reason, user_id, request_id)
      VALUES (tenant.id, 'active', NULL, actor_user_id, request_id)
  COMMIT

  -- 配额初始化（事务外，调用 Core API，失败补偿）
  -- 读取套餐有效限额（plan_quota_limits + resource_quota_meta 兜底）
  -- 读取 SQL：COALESCE(pql.total, rqm.default_quota) AS effective_total
  --   FROM resource_quota_meta rqm
  --   LEFT JOIN plan_quota_limits pql ON pql.resource_type = rqm.resource_type AND pql.plan_id = plan_id
  --  WHERE rqm.enabled = TRUE
  -- 对每个 enabled 维度计算 effective_total，批量调用 Core API PUT /api/v1/admin/tenants/{tenant_id}/quota 设置 total=effective_total
  -- Core 在 resource_quota 表 lazy init（INSERT...ON CONFLICT DO NOTHING 占位行 + 设置 total）
  -- 失败重试机制（内联 goroutine，非补偿队列）：
  --   - 同步调用 Core API 失败后，启动内联 goroutine 异步重试（scheduleQuotaSyncRetry）
  --   - 最多 3 次，指数退避 1s/2s/4s；每次重试复用 syncPlanQuotaToTenant 逻辑
  --   - 失败时写审计日志（action='tenant.quota_init_failed'，details 含 attempt 次数）
  --   - 前端可通过 GET /tenants/{tenantId}/quota 查看配额是否已初始化完成
  --   - 租户创建本身已成功（返回 200），配额初始化失败不影响租户可用性（配额未初始化的维度按 Core default_quota 兜底）
  core.PUT /api/v1/admin/tenants/{tenant.id}/quota { items: [{resource_type, total: effective_total}, ...] }
    ON FAILURE:
      INSERT INTO audit_logs (..., action='tenant.quota_init_failed', details={tenant_id, resource_type, effective_total, retry_count})

响应 200：{ id, message }（plan_code / 配额等完整字段由前端按需调用 GET /tenants/{tenantId} 获取，不在写操作响应中返回；admin_password 已由调用方提供，不回传；配额初始化异步完成，前端可轮询 GET /tenants/{tenantId}/quota 确认）
```

#### 6.3.2 TenantService.FreezeTenant

```
状态检查
  - 当前状态 = 'active'
  - 否则 409 TENANT_STATE_INVALID

事务
  BEGIN
    UPDATE tenants SET status='frozen', frozen_at=now()
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.freeze', 'tenant', 'success',
              jsonb_build_object('target_id', id))
    INSERT INTO tenant_lifecycle (tenant_id, action, reason, user_id, request_id)
      VALUES (id, 'frozen', '管理员手动冻结', actor, req_id)
  COMMIT

说明：冻结不改变 users 状态（Gateway 登录链路检查 tenants.status='frozen' → 403 TENANT_FROZEN，用户无法登录）；不停止实例
```

#### 6.3.3 TenantService.UnfreezeTenant

```
状态检查
  - 当前状态 = 'frozen'
  - 否则 409 TENANT_STATE_INVALID

事务
  BEGIN
    UPDATE tenants SET status='active', frozen_at=NULL
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.unfreeze', 'tenant', 'success',
              jsonb_build_object('target_id', id))
    INSERT INTO tenant_lifecycle (tenant_id, action, reason, user_id, request_id)
      VALUES (id, 'active', NULL, actor, req_id)
  COMMIT
```

#### 6.3.4 TenantService.DisableTenant

```
状态检查
  - 当前状态 ∈ {active, frozen}
  - 否则 409 TENANT_STATE_INVALID

事务
  BEGIN
    UPDATE tenants SET status='disabled', disabled_at=now()
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.disable', 'tenant', 'success',
              jsonb_build_object('target_id', id))
    INSERT INTO tenant_lifecycle (tenant_id, action, reason, user_id, request_id)
      VALUES (id, 'disabled', '管理员手动禁用', actor, req_id)
  COMMIT

说明：禁用后资源删除，无法登录/查看/创建（Gateway 登录链路检查 tenants.status='disabled' → 403 TENANT_DISABLED）；不修改 users.status（登录时由 Gateway 拦截租户级禁用即可）。禁用不可逆，无法恢复（终态，无 EnableTenant 方法）。禁用后 Core 侧 resource_quota 行保留（数据不删除），但资源实例停止运行。
```

#### 6.3.5 TenantService.UpdatePlan（切换套餐，内部方法，配额更新由 §6.3.11 BindPlanQuota 承载）

```
状态检查
  - 新 plan_id 对应的 tenant_plans.status='active'
  - 否则 422 PLAN_NOT_ACTIVE
  - 不允许直接切到 draft / disabled 套餐

事务
  BEGIN
    UPDATE tenants
      SET plan_id = new_plan_id
      WHERE id = id
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.update_plan', 'tenant', 'success',
              jsonb_build_object('target_id', id, 'payload', jsonb_build_object('old_plan_id', old_plan_id, 'new_plan_id', new_plan_id)))
  COMMIT

说明：本端点用于"切换套餐"，仅修改 plan_id；不影响配额（配额由 Core 维护，调整配额走 Core API）。
  tenants 表不保留 plan_code 列；响应中如需展示 plan_code 由 service 层 JOIN tenant_plans 读取。
```

#### 6.3.6 TenantService.UpdateSSO（修改 SSO 状态与 IdP 提供商，§5.2.9，B1）

```
AuthZ: platform-admin / platform-ops
输入校验（部分更新，仅校验传入字段）
  - tenant_id 必填（路径参数 {tenantId}）
  - 若传入 provider ∈ {'oidc','custom'}
  - 若传入 sso_enabled=TRUE 且 sso_provider
    当前为 NULL 且本次未传入 provider → 422 TENANT_SSO_CONFIG_INVALID
  （SSO 详细配置 issuer_url / client_id / client_secret_ref / scopes / auto_provision / email_domains
   不在本端点处理，由外部系统 K8s Secret/ConfigMap 承载）

状态检查
  - 当前 status != 'disabled'
  - 否则 409 TENANT_STATE_INVALID

事务
  BEGIN
    UPDATE tenant_auth
      SET sso_enabled  = COALESCE($sso_enabled, sso_enabled),
          sso_provider = COALESCE($provider, sso_provider)
      WHERE tenant_id = $tenant_id
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.update_sso', 'tenant', 'success',
              jsonb_build_object('target_id', $tenant_id, 'payload', jsonb_build_object(
                'sso_enabled', new_sso_enabled, 'provider', new_provider)))
  COMMIT

返回：{ id, message }（如需展示配置后完整 SSO 对象，调用 GET /tenants/{tenantId}/auth/sso 获取）

```
#### 6.3.7 TenantService.TestSSOConnection（测试 SSO 连接，§5.2.10，B1）

```
AuthZ: platform-admin / platform-ops
前置检查

  - tenant_id 必填（路径参数 {tenantId}）
  - 读取 tenant_auth.sso_provider
  - 若 sso_provider 为 NULL → 422 TENANT_SSO_CONFIG_INVALID
  - 根据 sso_provider 类型从外部系统（K8s Secret/ConfigMap）加载 SSO 详细配置
    （issuer_url / client_id / client_secret 等）
  - 若外部系统缺少对应 SSO 详细配置 → 422 TENANT_SSO_CONFIG_INVALID

执行（只读，不写事务）

  - 向外部系统加载的 issuer_url 发起 GET /.well-known/openid-configuration（超时 10s）
  - 校验返回 JSON 是否包含 authorization_endpoint / token_endpoint / userinfo_endpoint
  - 不修改任何数据，不写审计日志，不触发幂等

返回：
  成功：{ success: true, provider, discovery_result: {authorization_endpoint, token_endpoint, userinfo_endpoint}, error: null, tested_at: now() }
  失败：{ success: false, provider, discovery_result: null, error: <错误信息>, tested_at: now() }
```
#### 6.3.8 TenantService.UpdateMFARequired（MFA 强制开关，§5.2.11，B2）

```
AuthZ: platform-admin / platform-ops
输入校验

  - tenant_id 必填（路径参数 {tenantId}）
  - mfa_required ∈ {TRUE, FALSE}

事务
  BEGIN
    UPDATE tenant_auth SET mfa_required = new_value WHERE tenant_id = $tenant_id
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (NULL, actor, req_id, 'tenant.update_mfa', 'tenant', 'success',
              jsonb_build_object('target_id', $tenant_id, 'payload', jsonb_build_object('mfa_required', new_value)))
  COMMIT

返回：{ id, message }

说明：本端点不修改用户级 MFA 配置；用户级 MFA enrollment 由 auth-service 负责。
  Gateway 登录链路：JWT 校验 → 解出 tenant_id → 查 tenant_auth.mfa_required
    - TRUE 且 用户未完成 MFA → 403 MFA_REQUIRED（前端跳转 MFA enrollment / 验证页）
    - FALSE → 遵循用户级 MFA 配置


```
#### 6.3.9 TenantService.GetTenantQuota（查询租户配额，§5.2.12）

```

AuthZ: platform-admin / platform-ops / platform-readonly
执行：

  - 代理 Core API GET /api/v1/admin/tenants/{tenant_id}/quota
  - JOIN resource_quota（used / total）与 resource_quota_meta（display_name / unit）
  - 组装 items[]{resource_type, display_name, used, total, unit} 返回
  - 不写本服务数据库，不写审计日志

```
#### 6.3.10 TenantService.CreateQuotaChangeRequest（提交配额变更申请，§5.2.13）


```

AuthZ: platform-admin / platform-ops
输入校验：

  - items 非空数组，至少 1 项
  - 每项 resource_type 满足 ^[a-z0-9_]{2,40}$
  - 每项 new_value >= 0
  - items 中同一 resource_type 不可重复，否则 422 QUOTA_CHANGE_REQUEST_INVALID
  - 调 Core API 校验每个 resource_type 存在于 resource_quota_meta
  - 否则 422 QUOTA_CHANGE_REQUEST_INVALID

重复检查（逐维度）：

  - 对每个 (tenant_id, resource_type)，查 tenant_quota_change WHERE tenant_id=$tenant_id AND resource_type=$resource_type AND status='pending'
  - 若存在 → 该维度跳过，记录 409 QUOTA_CHANGE_REQUEST_DUPLICATE（其他维度仍正常处理）

读取当前配额：

  - 代理 Core API GET /api/v1/admin/tenants/{tenant_id}/quota 获取各维度当前 total → 逐维度冻结 old_value

事务：
  BEGIN
    FOR EACH (resource_type, new_value) IN items:
      INSERT INTO tenant_quota_change (tenant_id, resource_type, old_value, new_value, requested_by, status='pending')
        VALUES (tenant_id, resource_type, old_value, new_value, actor, 'pending')
    INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (tenant_id, actor, req_id, 'tenant.quota_change_request', 'tenant', 'success',
              jsonb_build_object('target_id', tenant_id, 'items', jsonb_build_array(
                jsonb_build_object('resource_type', rt, 'old_value', ov, 'new_value', nv) FOR EACH item
              )))
  COMMIT

返回：{ id, message: "quota change request submitted" }


```
#### 6.3.11 TenantService.BindPlanQuota（绑定套餐更新配额，§5.3.9）

```

AuthZ: platform-admin / platform-ops
输入校验：

  - plan_id 存在且 tenant_plans.status='active' AND is_deleted=FALSE（查 tenant_plans 表）
  - 否则 404 TENANT_PLAN_NOT_FOUND

状态检查：

  - 当前租户 status != 'disabled'
  - 否则 409 TENANT_STATE_INVALID

执行：

  - service.buildQuotaLimitViews（store.GetQuotaLimits + Core ListQuotaMeta，COALESCE total, default_quota 兜底为具体 total）
  - 若 plan_id 与当前不同 → 先 UPDATE tenants SET plan_id=$plan_id（先更新 plan_id）
  - 读取该租户已审核通过的配额变更维度：
    SELECT resource_type FROM tenant_quota_change
      WHERE tenant_id=$tenant_id AND status='approved'
  - 对每个维度：
    · 若该维度存在 approved 变更申请 → 保留其配额值不覆盖（跳过 Core API 调用）
    · 否则收集到待更新列表
  - 逐租户调 Core API GET /api/v1/admin/tenants/{tenant_id}/quota 判断配额行是否存在
    · 不存在 → POST 新建配额行 used/reserved=0
    · 已存在 → PUT 修改 total，自动收紧
  - Core 同步失败 → best-effort 回滚 plan_id（回滚失败也返回错误）

  -- 审计日志在所有操作完成后 best-effort 写入（不在事务内）
  INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
    VALUES (tenant_id, actor, req_id, 'tenant.bind_plan_quota', 'tenant', 'success',
            jsonb_build_object('plan_id', plan_id, 'tenant_id', tenant_id,
                               'tenant_name', tenant_name,
                               'tenant_display_name', tenant_display_name,
                               'skipped_approved', jsonb_build_array(已审核维度列表),
                               'tightened', tightened_count,
                               'updated', jsonb_build_array(已更新维度列表)))

返回：{ id: tenant_id, message: "quota bound to plan" }

说明：

  - 更换套餐不影响已有资源：Core resource_quota.total 更新后，已创建的实例继续运行
  - 若新 total < 当前 used + reserved：Core API 自动收紧 total 为 used + reserved，已有资源不受影响、不会被停止或回收
  - approved 变更申请的维度保留不覆盖；pending 维度按套餐值覆盖（等审批通过后再覆盖套餐值）


```

#### 6.3.12 TenantPlanService.Create（含 quota_limits）

```

AuthZ: platform-admin / platform-ops
输入校验

  - code 格式 ^[a-z0-9-]{3,40}$（仅 Create 必填）
  - quota_limits（可选数组）：每项 {resource_type, total}
    · resource_type 必须在 resource_quota_meta 中 enabled=TRUE，否则 422 QUOTA_RESOURCE_NOT_REGISTERED
    · total 为 null 或 ≥ 0；null 表示用 default_quota 兜底
    · 同一 resource_type 不可重复出现

Create 事务
  BEGIN
    INSERT tenant_plans (code, name, description, status='draft')
    -- 写入 plan_quota_limits（每维度一行）
    INSERT INTO plan_quota_limits (plan_id, resource_type, total)
      VALUES (new_plan_id, rt, materialized_total) FOR EACH (rt, lv) IN quota_limits
      -- total 为 null 时在 service 层物化为 resource_quota_meta.default_quota 具体值再落库（不保留 NULL）
      -- 未列出的维度不建行
  COMMIT
  -- 审计日志在事务提交后 best-effort 写入（审计失败不回滚已提交的创建）
  INSERT INTO audit_logs (..., action='tenant_plan.create', details={plan_id, code, quota_limits})
```

> 注：套餐模板字段 name/description 可通过 `PUT /tenant-plans/{planId}` 修改（nil=不更新，空串=清空）。套餐限额可在创建时设置（§5.3.1），也可通过 `PUT /tenant-plans/{planId}/quota-limits`（§5.3.8）修改，修改后自动同步存量租户。

#### 6.3.13 TenantPlanService.Delete（删除套餐，§5.3.7）


```

AuthZ: platform-admin / platform-ops
输入校验：

  - plan_id 非空
  - 查 tenant_plans WHERE id = plan_id AND is_deleted = FALSE
  - 若不存在 → 404 TENANT_PLAN_NOT_FOUND

租户关联检查：

  - SELECT COUNT(*) FROM tenants WHERE plan_id = $plan_id AND status != 'disabled'
  - 若 COUNT > 0 → 409 TENANT_PLAN_IN_USE

事务：
  BEGIN
    UPDATE tenant_plans SET is_deleted = TRUE, deleted_at = now() WHERE id = plan_id
    -- 软删除不触发 ON DELETE CASCADE，plan_quota_limits 行随套餐行保留
  COMMIT
  -- 审计日志在事务提交后 best-effort 写入（审计失败不回滚已提交的软删除）
  INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
    VALUES (NULL, actor, req_id, 'tenant_plan.delete', 'tenant_plan', 'success',
            jsonb_build_object('plan_id', plan_id))

返回：{ id: plan_id, message: "tenant plan deleted" }


```
#### 6.3.15 TenantAdminService.ResetPassword

```

AuthZ: platform-admin, platform-ops
入参：tenant_id（路径参数 {tenantId}）、user_id（路径参数 {userId}）、new_password（body）
约束：
  - 禁用态用户（status='disabled'，含已软删除）不可重置 → 404 TENANT_ADMIN_NOT_FOUND
  - new_password 需 8-64 字符、四类至少三类；bcrypt.Compare 命中旧密码 → 422 PASSWORD_SAME_AS_OLD
bcrypt(new_password, 12) → hashed
UPDATE users SET password_hash=hashed
  WHERE id = user_id AND tenant_id = tenant_id AND status != 'disabled'
INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
  VALUES (NULL, actor, req_id, 'tenant_admin.reset_password', 'tenant_admin', 'success',
          jsonb_build_object('target_id', user_id, 'target_tenant_id', tenant_id))
返回：{ id, message }

```
#### 6.3.16 TenantAdminService.GetRolePermissions（查询指定管理员角色与权限，§5.4.5 `GET .../role`）

```

AuthZ: platform-admin, platform-ops
入参：tenant_id（路径参数 {tenantId}）、user_id（路径参数 {userId}）
执行：

  - JOIN user_roles + roles 查询指定用户在指定租户下的角色及 roles.permissions JSONB
  - **仅返回租户成员权限**：tenant-owner / tenant-admin / user / auditor（tenant_id 非空）→ 直接返回 roles.permissions 的 4 维
  - **平台账户（tenant_id=null）权限不可查询**：本端点不返回平台权限（平台权限由平台侧查询）
  - 不写审计日志，不调用 Core API
    返回：{ user_id, tenant_id, role, permissions{compute, inference, member, transfer} }


```
#### 6.3.17 TenantAdminService.ListAllAdmins（跨租户查询所有管理员，§5.4.3）

```

AuthZ: platform-admin / platform-ops / platform-readonly
执行：

  - JOIN users + user_roles + roles + tenants + tenant_admin_invitation 跨租户查询
  - WHERE users.is_deleted = FALSE
  - **仅返回 role ∈ (tenant-owner, tenant-admin) 或正在被邀请（`tenant_admin_invitation.status='inviting'`，is_inviting=true）的用户，不返回普通成员 user**
  - 可选过滤：tenant_id（传入时仅返回该租户内的管理员绑定）、role（通过 roles.name，tenant-owner/tenant-admin）、status（users.status）、is_inviting、search（email/username ILIKE）
  - source 字段推断：username 以 'oidc:' 开头 → 'third_party'，以 'local:' 开头 → 'local'
  - 游标分页（limit + cursor），按 created_at DESC 排序
  - 每条返回 tenant 对象（tenants.id / name / display_name / mfa_required，JOIN tenant_auth 获取 mfa_required）
  - 不写审计日志，不调用 Core API
    返回：CursorPage { items[]{id, email, username, display_name, role, status, is_inviting, source, last_login_at, tenant{...}}, next_cursor }


```
#### 6.3.17b TenantAdminService.Invite（邀请租户管理员，§5.4.1）

```
AuthZ: platform-admin, platform-ops
入参：tenant_id（路径参数 {tenantId}）、email、username（body）
执行：

  - 校验幂等：Gateway Redis 中间件按 Idempotency-Key 去重（body 必填）
  - 按 email + username 在本租户（tenant_id = tenantId）内匹配现有用户
    - 无匹配 → 404 TENANT_ADMIN_NOT_FOUND（不新建用户）
  - 该用户已是本租户 tenant-admin / tenant-owner → 409 TENANT_ADMIN_ALREADY_ADMIN
  - 该用户在本租户下已存在 status='inviting' 的邀请 → 409 TENANT_INVITATION_PENDING（应改用重发）
  - token = crypto_random(32)（原始 token，仅本次返回一次）
  - INSERT INTO tenant_admin_invitation (tenant_id, user_id, token_hash, status, expire_at)
      VALUES (tenant_id, user_id, SHA-256(token), 'inviting', now() + interval '72 hours')
  - 不改 users.status / 不绑定角色
  - INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (tenant_id, actor, req_id, 'tenant_admin.invite', 'tenant_admin', 'success',
              jsonb_build_object('target_id', user_id, 'token_hash', SHA-256(token)))
  - 触发通知渠道将 token 拼接为邀请链接发送（不落库明文 token）
    返回：{ id, token, expire_at, message }
```

#### 6.3.17c TenantAdminService.Resend（重发租户管理员邀请，§5.4.2）

```
AuthZ: platform-admin, platform-ops
入参：tenant_id（路径参数 {tenantId}）、user_id（路径参数 {userId}）
执行：

  - 校验幂等：Gateway Redis 中间件按 Idempotency-Key 去重（body 必填）
  - 查询该 tenantId+userId 下的最新一条 tenant_admin_invitation
    - 无记录 → 404 TENANT_ADMIN_INVITATION_NOT_FOUND
    - 最新状态为 accepted / rejected（终态）→ 409 TENANT_INVITATION_SETTLED（不可重发）
  - 仅允许 status='inviting' 或 status='expired' 的邀请重发
  - token = crypto_random(32)（仅本次返回一次）
  - UPDATE tenant_admin_invitation
      SET token_hash = SHA-256(token), expire_at = now() + interval '72 hours',
          status = 'inviting', accepted_at = NULL, rejected_at = NULL
    WHERE id = invitation_id
  - INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
      VALUES (tenant_id, actor, req_id, 'tenant_admin.resend_invitation', 'tenant_admin', 'success',
              jsonb_build_object('target_id', user_id, 'token_hash', SHA-256(token)))
    返回：{ id, token, expire_at, message }
```

#### 6.3.17d TenantAdminService.ChangeRole / Disable / Enable / Delete / ListTenantAdmins / ListAllTenantAdmins / GetAdminDetail / TransferOwnership

```
ChangeRole（§5.4.6 PUT .../role）：
  AuthZ: platform-admin, platform-ops
  - 校验 role 参数在允许范围（user/auditor/tenant-admin），否则 → 422 ROLE_CHANGE_INVALID
  - 目标用户为 tenant-owner → 409 TENANT_OWNER_ROLE_LOCKED（owner 角色锁定，不可修改，与是否唯一 owner 无关，不触发 LAST_TENANT_OWNER）
  - DELETE FROM user_roles WHERE user_id=target AND role_id IN (本租户 user/auditor/tenant-admin 角色)
  - INSERT INTO user_roles SELECT target, 目标角色 id
  - 写审计 action='tenant_admin.change_role'，details 含 old_role / new_role

Disable（§5.4.9 POST .../disable）：
  AuthZ: platform-admin, platform-ops
  - 目标为唯一活跃 tenant-owner → 422 LAST_TENANT_OWNER
  - UPDATE users SET status='disabled' WHERE id=target AND tenant_id=tenantId
  - 写审计 action='tenant_admin.disable'

Enable（§5.4.10 POST .../enable）：
  AuthZ: platform-admin, platform-ops
  - UPDATE users SET status='active' WHERE id=target AND tenant_id=tenantId
  - 写审计 action='tenant_admin.enable'

Delete（§5.4.11 DELETE .../admins/{userId}）：
  AuthZ: platform-admin, platform-ops
  - tenant-owner 不可删；目标为唯一活跃 tenant-owner → 422 LAST_TENANT_OWNER
  - 软删除：UPDATE users SET is_deleted=TRUE, deleted_at=now(), status='disabled'
      WHERE id=target AND tenant_id=tenantId AND is_deleted=FALSE
  - 写审计 action='tenant_admin.delete'

ListTenantAdmins（§5.2.7 GET .../admins，租户内）：
  AuthZ: platform-admin, platform-ops, platform-readonly
  - JOIN user_roles + roles 过滤 tenant-admin；可选 role/status/search 过滤；游标分页
  - 不写审计（只读）

ListAllTenantAdmins（§5.4.3 GET /tenant-admins，跨租户）：
  AuthZ: platform-admin, platform-ops, platform-readonly
  - JOIN users + user_roles + roles + tenants 返回租户管理员；**仅返回 role ∈ (tenant-owner, tenant-admin) 或 is_inviting=true 的用户**（不返回普通成员 user）
  - 关联 tenant_admin_invitation：某用户在该租户下存在 status='inviting' 的邀请时，该用户亦返回，且 is_inviting=true（仅作标记，不改变其 role/status，仍展示原有角色）
  - 可选 tenant_id / role / status / is_inviting / search 过滤；游标分页；返回租户对象
  - 不写审计（只读）

GetAdminDetail（§5.4.4 GET .../admins/{userId}）：
  AuthZ: platform-admin, platform-ops, platform-readonly
  - 查单个管理员详情（含 tenant 对象）；不写审计（只读）

TransferOwnership（§5.4.7 POST .../transfer-ownership）：
  AuthZ: platform-admin, platform-ops
  - 目标用户须为**当前租户**（tenant_id 相同）`status='active'` 且角色为 `tenant-admin` 的成员 → 否则 422 TRANSFER_TARGET_INVALID
  - 当前 owner 角色降为 tenant-admin / 目标角色升为 tenant-owner（唯一）
  - 写审计 action='tenant_admin.transfer_ownership'，details 含 old_owner_id / new_owner_id
```

#### 6.3.18 PlatformAdminService.Delete / Disable / ChangeRole / ResetPassword

```
AuthZ: platform-admin
最后管理员保护（仅 Delete / Disable 生效）
  active_admins_count = SELECT count(*) FROM users u
    JOIN user_roles ur ON ur.user_id = u.id
    JOIN roles r ON r.id = ur.role_id
    WHERE u.tenant_id IS NULL
      AND r.tenant_id IS NULL AND r.name = 'platform-admin'
      AND u.status = 'active'
      AND u.id != target_user_id  -- 排除当前操作目标

  IF active_admins_count == 0
    RETURN 422 LAST_PLATFORM_ADMIN

继续删除/禁用/改角色/重置密码流程

Delete：       UPDATE users SET is_deleted=TRUE, deleted_at=now(), status='disabled' WHERE id = target_user_id  -- 软删除
               INSERT INTO audit_logs (tenant_id, user_id, request_id, action, resource, result, details)
                 VALUES (NULL, actor, req_id, 'platform_admin.delete', 'platform_admin', 'success',
                         jsonb_build_object('target_id', target_user_id))
Disable：      UPDATE users SET status='disabled' WHERE id = target_user_id
Enable：       UPDATE users SET status='active' WHERE id = target_user_id
ChangeRole：   DELETE FROM user_roles WHERE user_id = target_user_id
                 AND role_id IN (SELECT id FROM roles WHERE tenant_id IS NULL
                                   AND name IN ('platform-admin','platform-ops','platform-readonly'))
               INSERT INTO user_roles (user_id, role_id)
                 SELECT target_user_id, r.id FROM roles r
                 WHERE r.tenant_id IS NULL AND r.name = new_role
               （新 platform-admin 由下次 Delete/Disable 触发 LAST_PLATFORM_ADMIN 保护）
ResetPassword：UPDATE users SET password_hash=bcrypt(new_pwd,12) WHERE id = target_user_id
               返回：{ id, message }

> TenantBillingService.Recharge / Charge 暂不实现，后续 PR 再补充。


```

> 

#### 6.3.19 PlatformAdminService.GetPlatformPermissions（查询运营账号权限，§5.5.3b）

```

AuthZ: platform-admin
入参：user_id（路径参数 {userId}）
执行：

  - JOIN user_roles + roles 查询指定用户角色及 roles.permissions JSONB
  - 若 role 为 platform-admin → 返回 permissions: {"tenant_ops":"write","resource_pool":"write","platform_user":"write","audit_export":"write"}
  - 否则直接返回 roles.permissions JSONB 字段值（平台 4 维度：tenant_ops / resource_pool / platform_user / audit_export）
  - 不写审计日志，不调用 Core API
    返回：{ user_id, role, permissions{tenant_ops, resource_pool, platform_user, audit_export} }


```
### 6.4 handler 层错误转换

```go
// repo/services/tenant-service/handlers/errors.go
func writeError(w http.ResponseWriter, r *http.Request, err error) {
    var ce CodeError
    switch {
    case errors.As(err, &ce):
        w.WriteHeader(ce.HTTP())
        json.NewEncoder(w).Encode(map[string]any{
            "error": map[string]any{
                "code":         ce.Code(),
                "message":      ce.Message(),
                "details":      ce.Details(),
                "request_id":   middleware.RequestID(r),
                "idempotency_key": middleware.IdempotencyKey(r),
            },
        })
    default:
        w.WriteHeader(500)
        json.NewEncoder(w).Encode(map[string]any{
            "error": map[string]any{
                "code": "INTERNAL",
                "message": "internal server error",
                "request_id": middleware.RequestID(r),
            },
        })
    }
}
```

### 6.5 wiring

```go
// repo/services/tenant-service/internal/wiring/wiring.go
func BuildApp(deps Deps) *App {
    db := deps.DB

    tenantStore := postgres.NewTenantStore(db)              // 不含 UpdateQuotas；UpdatePlan 仅改 plan_id
    tenantPlanStore := postgres.NewTenantPlanStore(db)      // 套餐 CRUD + Activate/Disable
    tenantAdminStore := postgres.NewTenantAdminStore(db)    // 管理员角色绑定 + 禁用/启用/软删除
    platformAdminStore := postgres.NewPlatformAdminStore(db)
    auditStore := postgres.NewAuditLogStore(db)             // 复用现有 audit_logs 分区表
    lifecycleStore := postgres.NewTenantLifecycleStore(db)    // 租户生命周期记录
    authStore := postgres.NewTenantAuthStore(db)               // MFA/SSO 配置
    quotaChangeReqStore := postgres.NewQuotaChangeRequestStore(db)  // 配额变更申请（tenant_quota_change 表 §4.1.6）

    // 配额相关：调用 Core 客户端（Core SDK 或 Core HTTP API）
    quotaSvcClient := core.NewQuotaServiceClient(deps.CoreAPIBaseURL, deps.CoreAPIAuthToken)
    quotaMetaSvcClient := core.NewQuotaMetaServiceClient(deps.CoreAPIBaseURL, deps.CoreAPIAuthToken)

    // 幂等由 Gateway Idempotency 中间件 + Redis 处理，service 不持有 idempotencyStore
    tenantSvc := service.NewTenantService(tenantStore, tenantPlanStore, auditStore, lifecycleStore, authStore, quotaChangeReqStore, quotaSvcClient)  // CreateTenant 调用 Core API 逐维度初始化配额；状态变更写入 lifecycle；MFA/SSO 读写 authStore；配额变更申请写 quotaChangeReqStore
    adminSvc := service.NewTenantAdminService(tenantAdminStore, auditStore)
    platformSvc := service.NewPlatformAdminService(platformAdminStore, auditStore)
    planSvc := service.NewTenantPlanService(tenantPlanStore, auditStore)

    mux := http.NewServeMux()
    // tenants 路径组（租户生命周期 + 身份认证 + 配额 + 管理员子资源）
    mux.Handle("/tenants", handlers.NewTenantHandler(tenantSvc))                    // POST 创建 + GET 列表
    mux.Handle("/tenants/{tenantId}", handlers.NewTenantHandler(tenantSvc))         // GET 详情 + PUT 修改基本信息
    mux.Handle("/tenants/{tenantId}/freeze", handlers.NewTenantHandler(tenantSvc))
    mux.Handle("/tenants/{tenantId}/unfreeze", handlers.NewTenantHandler(tenantSvc))
    mux.Handle("/tenants/{tenantId}/disable", handlers.NewTenantHandler(tenantSvc))
    mux.Handle("/tenants/{tenantId}/auth/sso", handlers.NewTenantAuthHandler(authSvc))
    mux.Handle("/tenants/{tenantId}/auth/sso/test", handlers.NewTenantAuthHandler(authSvc))
    mux.Handle("/tenants/{tenantId}/auth/mfa", handlers.NewTenantAuthHandler(authSvc))
    mux.Handle("/tenants/{tenantId}/quota", handlers.NewTenantQuotaHandler(tenantSvc))              // GET 查询配额
    mux.Handle("/tenants/{tenantId}/quota-requests", handlers.NewTenantQuotaHandler(tenantSvc))      // POST 提交申请 + GET 查询申请列表
    mux.Handle("/tenants/{tenantId}/quota-requests/{reqId}/approve", handlers.NewTenantQuotaHandler(tenantSvc))  // POST 审批申请
    mux.Handle("/tenant-plans/{planId}/bindable-tenants", handlers.NewTenantPlanHandler(tenantPlanSvc))    // GET 查询可绑定该套餐的租户
    mux.Handle("/tenants/{tenantId}/plan", handlers.NewTenantPlanHandler(planSvc))                   // POST 绑定套餐
    mux.Handle("/tenants/{tenantId}/lifecycle", handlers.NewTenantLifecycleHandler(tenantSvc))
    mux.Handle("/tenants/{tenantId}/audit-logs", handlers.NewTenantAuditLogHandler(tenantSvc))
    mux.Handle("/tenants/{tenantId}/admins", handlers.NewTenantAdminHandler(adminSvc))               // §5.2.7 列表 GET /tenants/{tenantId}/admins + POST 邀请（§5.4.1）
    mux.Handle("/tenants/{tenantId}/admins/invite", handlers.NewTenantAdminHandler(adminSvc))        // POST /tenants/{tenantId}/admins/invite 邀请租户管理员（§5.4.1）
    mux.Handle("/tenants/{tenantId}/admins/{userId}", handlers.NewTenantAdminHandler(adminSvc))      // GET 详情 + DELETE 软删除
    mux.Handle("/tenants/{tenantId}/admins/{userId}/invitation/resend", handlers.NewTenantAdminHandler(adminSvc)) // POST 重发邀请（§5.4.2）
    mux.Handle("/tenants/{tenantId}/admins/{userId}/role", handlers.NewTenantAdminHandler(adminSvc))            // GET 查询角色权限 + PUT 改权限
    mux.Handle("/tenants/{tenantId}/admins/{userId}/reset-password", handlers.NewTenantAdminHandler(adminSvc)) // POST 重置密码
    mux.Handle("/tenants/{tenantId}/admins/{userId}/disable", handlers.NewTenantAdminHandler(adminSvc))       // POST 禁用
    mux.Handle("/tenants/{tenantId}/admins/{userId}/enable", handlers.NewTenantAdminHandler(adminSvc))        // POST 启用
    mux.Handle("/tenants/{tenantId}/admins/{userId}/audit-logs", handlers.NewTenantAdminHandler(adminSvc))    // GET 管理员操作历史
    mux.Handle("/tenants/{tenantId}/transfer-ownership", handlers.NewTenantAdminHandler(adminSvc))  // POST 移交所有者

    // tenant-admins 路径组（跨租户管理员）
    mux.Handle("/tenant-admins", handlers.NewTenantAdminHandler(adminSvc))                           // GET 跨租户查询所有管理员

    // platform-admins 路径组
    mux.Handle("/platform-admins", handlers.NewPlatformAdminHandler(platformSvc))                    // POST 创建 + GET 列表
    mux.Handle("/platform-admins/{userId}/permissions", handlers.NewPlatformAdminHandler(platformSvc))  // GET 指定运营账号权限
    mux.Handle("/platform-admins/{userId}", handlers.NewPlatformAdminHandler(platformSvc))           // GET 详情 + DELETE 软删除
    mux.Handle("/platform-admins/{userId}/role", handlers.NewPlatformAdminHandler(platformSvc))      // PUT 改角色
    mux.Handle("/platform-admins/{userId}/reset-password", handlers.NewPlatformAdminHandler(platformSvc))  // POST 重置密码
    mux.Handle("/platform-admins/{userId}/disable", handlers.NewPlatformAdminHandler(platformSvc))   // POST 禁用
    mux.Handle("/platform-admins/{userId}/enable", handlers.NewPlatformAdminHandler(platformSvc))    // POST 启用

    // tenant-plans 路径组
    mux.Handle("/tenant-plans", handlers.NewTenantPlanHandler(planSvc))                    // POST 创建 + GET 列表
    mux.Handle("/tenant-plans/{planId}", handlers.NewTenantPlanHandler(planSvc))           // GET 详情 + DELETE 删除
    mux.Handle("/tenant-plans/{planId}/quota-limits", handlers.NewTenantPlanHandler(planSvc))  // GET 查询限额 + PUT 修改限额
    mux.Handle("/tenant-plans/{planId}/activate", handlers.NewTenantPlanHandler(planSvc))      // POST 发布
    mux.Handle("/tenant-plans/{planId}/disable", handlers.NewTenantPlanHandler(planSvc))       // POST 禁用
    mux.Handle("/tenant-plans/{planId}/tenants", handlers.NewTenantPlanHandler(planSvc))         // GET 套餐绑定租户列表
    mux.Handle("/tenant-plans/{planId}/audit-logs", handlers.NewTenantPlanHandler(planSvc))     // GET 套餐操作历史

    return &App{Mux: mux}
}
```

> 注：配额元数据管理与租户配额配置端点（`/api/v1/admin/quota-meta/*`、`/api/v1/admin/tenants/{id}/quota/*`）由 Core 服务承载；BOSS 前端通过 Core API 直接代理调用，不经 tenant-service 转发。

### 6.6 Core Gateway 集成

- 平台 JWT 校验后，在请求上下文注入 `platform_user_id` 和 `roles`
- 平台操作路径前缀：
  - `/api/v1/svc/tenants*`（含 `/admins`、`/auth`、`/quota`、`/quota-requests`、`/plan`、`/lifecycle`、`/audit-logs` 子路径）
  - `/api/v1/svc/tenant-admins*`（跨租户管理员查询）
  - `/api/v1/svc/platform-admins*`
  - `/api/v1/svc/tenant-plans*`
  - `/api/v1/admin/quota-meta*`（Core 配额元数据管理，BOSS 前端代理调用）
  - `/api/v1/admin/tenants/{id}/quota*`（Core 租户配额配置与用量查询，BOSS 前端代理调用）
  → Services 路径要求 roles 含 `platform-admin` 或 `platform-ops` 或 `platform-readonly`（写操作排除 readonly）；Core 配额管理路径由 Core API 鉴权（platform-admin only）
- 跨租户请求（如 Console 访问 `/api/v1/svc/tenant/members`）执行冻结/禁用状态拦截
- frozen 状态拦截：所有请求返回 403 TENANT_FROZEN（禁止登录）
- disabled 状态拦截：所有请求返回 403 TENANT_DISABLED（禁止登录）
- idempotency_key body 字段校验：写操作（POST/PUT）必填，格式 UUID；**DELETE 除外（不做幂等）**

---

## §7 前端设计

### 7.1 技术栈

- Vite 5 + React 18 + TypeScript 5
- TanStack Router 1.40 + TanStack Query 5
- TDesign React（与现有 BOSS 一致）
- basepath `/boss/`（已配置）
- 跨端链接：`login.tsx` 含 Console 跳转链接

### 7.2 受保护路由

`/boss/tenants*` 等所有租户管理页面位于 `_authenticated/` 下，需平台 JWT：

```
/boss/login          (未认证)
/boss/tenants        (platform-admin / platform-ops / platform-readonly)
/boss/tenants/quota-meta            (platform-admin only; 代理 Core API)
/boss/tenants/admins
/boss/tenants/plans
/boss/tenants/{id}/quota             (platform-admin / platform-ops; 代理 Core API)
/boss/settings/platform-admins      (platform-admin only)
```

### 7.3 5 个页面设计

#### 7.3.1 租户列表 `/boss/tenants`

- 顶部：搜索框（name / display_name）、状态筛选（active/frozen/disabled）、新建按钮
- 表格列：name / display_name / plan_id（隐藏到详情或 tooltip 显示 plan_code）/ status / admin_count / actions
- 操作：查看详情、编辑、冻结/解冻、禁用
- 分页：默认 20/页
- 状态徽标：active=绿，frozen=橙，disabled=红
- MFA / SSO 标记：列表不显示，进入详情页查看
- 编辑表单：已移除 `PUT /tenants` 编辑端点；display_name / email / plan_id / mfa_required 的修改通过各自专端点完成（MFA → `PUT /tenants/{tenantId}/auth/mfa`，套餐 → `POST /tenants/{tenantId}/plan`，display_name / email → `PUT /tenants/{tenantId}`）
- 切换套餐：选择新套餐（前端先用 GET /tenant-plans 列出 id+code 名称对照）；切换套餐仅修改 plan_id，不影响配额；BOSS 前端可在切换前提示"配额需单独在配额页面调整"

#### 7.3.2 租户配额元数据 `/boss/tenants/quota-meta`（代理 Core API）

> 真实来源：Core API `/api/v1/admin/quota-meta/*`（详见 `通用资源配额与计量落地方案.md` §4.1 QuotaMetaService port）。本页面是 BOSS 侧的代理 UI。

- 顶部：刷新按钮、新建配额维度按钮（platform-admin only）
- 表格列：resource_type / display_name / unit / default_quota / collector_id / enabled / actions
- 操作：
  - 新建（弹窗：resource_type / display_name / unit / is_discrete / default_quota / collector_id / description）→ POST /api/v1/admin/quota-meta
  - 编辑（弹窗：display_name / default_quota / collector_id / description / enabled）→ PUT /api/v1/admin/quota-meta/{resource_type}
  - 禁用 → POST /api/v1/admin/quota-meta/{resource_type}/disable
- 说明文案：配额维度由平台管理员维护；新增维度后，新建租户时 Core 自动按 `default_quota` 为该维度建行；已有租户不受影响
- 错误提示：QUOTA_RESOURCE_ALREADY_REGISTERED → 红色提示"该 resource_type 已注册"

#### 7.3.3 租户配额配置 `/boss/tenants/{id}/quota`（代理 Core API）

> 真实来源：Core API `/api/v1/admin/tenants/{id}/quota/*`（详见 `通用资源配额与计量落地方案.md` §4.2 QuotaService）。本页面是 BOSS 侧的代理 UI。

- 顶部：租户选择器
- 列出该租户所有 `resource_quota` 行（resource_type / total / reserved / used）
- 编辑按钮（platform-admin/ops）：弹出表单，单个 `total` 字段（整数），提交调用 `PUT /api/v1/admin/tenants/{id}/quota`（批量，单维度）设置 total
- 用量进度条：used / total（数据由 Core `resource_quota.reserved/used` 提供）
- 用量明细查询：调用 `GET /api/v1/admin/tenants/{id}/quota/usage?period_start=&period_end=` 拉取 Core metering_usage_records 聚合数据
- 错误提示：QUOTA_EXCEEDED → "总配额已超出系统上限"；QUOTA_RESOURCE_NOT_REGISTERED → "该维度未在 resource_quota_meta 中注册"；响应中 `tightened=true` → 黄色提示"配额已自动收紧为当前已占用值"

#### 7.3.3b 租户配额查询与变更申请 `/boss/tenants/{id}/quota`（Services API 路径）

> 真实来源：Services API `/api/v1/svc/tenants/{tenantId}/quota` 与 `/api/v1/svc/tenants/{tenantId}/quota-requests`（§5.2.12 / §5.2.13）。本页面与 §7.3.3 共用路由 `/boss/tenants/{id}/quota`，作为该页面的"申请变更"入口补充：platform-admin/ops 可直接编辑 total（§7.3.3），tenant-admin 仅能提交变更申请走审批流（本节）。配额实际数据由 Core `resource_quota` 表承载，本服务通过 Services API 代理查询并记录变更申请到 `tenant_quota_change` 表（§4.1.6）。

- 调用 `GET /tenants/{tenantId}/quota` 展示配额列表，表格列：resource_type / display_name / used / total / unit，每行带用量进度条 used/total
- "申请变更"按钮（tenant-admin / platform-admin / platform-ops 可见）：弹窗含可多选的 resource_type 列表（选项从配额列表 items 取）+ 每个维度对应 new_value 输入框（整数 >= 0），支持一次提交多个维度的变更申请，调用 `POST /tenants/{tenantId}/quota-requests`（body: items[{resource_type, new_value}]，body 含 idempotency_key）
- 提交成功：返回 `{ id, message }` 后提示"申请已提交，等待审核"，并 invalidateQuery 拉取最新配额列表
- 409 `QUOTA_CHANGE_REQUEST_DUPLICATE` → 提示"该配额维度已有待审核的申请"
- 422 `QUOTA_CHANGE_REQUEST_INVALID` → 提示"resource_type 不存在或 new_value 为负数"
- 说明文案：变更申请提交后需平台管理员审核（`POST /tenants/{tenantId}/quota-requests/{reqId}/approve`）；old_value 由后端从 Core `resource_quota.total` 读取并冻结
- "绑定套餐"按钮（platform-admin / platform-ops 可见）：套餐详情页"绑定租户"Tab 含租户下拉列表（选项从 `GET /tenant-plans/{planId}/bindable-tenants` 获取可绑定租户列表），选择后调用 `POST /tenants/{tenantId}/plan`（body: plan_id，body 含 idempotency_key），返回 `{ id, message }` 后提示"套餐已绑定，配额已更新"并 invalidateQuery 刷新配额列表

#### 7.3.3c 套餐管理 `/tenants/quotas`（实现对齐 2026-08-14）

> 旧路径 `/boss/tenant-plans`、弹窗改限额、仅 active 可改限额 — **已废弃**。现行 UX：[ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)。

- 列表路由：`/tenants/quotas`；创建：`/tenants/quotas/new`（独立 3 步 Wizard）；详情：`/tenants/quotas/$planId`（整页四 Tab：概览 / 限额明细 / 绑定租户 / 操作历史）
- 顶部：新建套餐按钮（platform-admin / platform-ops 可见）
- 工具栏：按名称搜索（点击搜索/Enter）+ Radio 状态（全部/启用/停用/草稿）
- 表格列：套餐(name) / 编码 / 状态 / 绑定租户 / 更新时间 / 操作
- 操作：详情、发布（activate）、停用（disable）、删除（软删除；有非停用租户关联时 409）
- 新建套餐 Wizard（POST /tenant-plans）：名称编码 → 限额配置（GET /quota-meta）→ 确认发布（按钮「确认创建」；提交后为 **draft**，需再发布）
- **限额明细 Tab**：任意状态可编辑 InputNumber；底部「保存并同步绑定租户」→ PUT `/quota-limits`；成功文案用详情 `tenant_count`（真实同步数在审计 details）
- **绑定租户 Tab**：列表 GET `.../tenants`；内联 Select（GET `.../bindable-tenants`）+「分配」→ POST `/tenants/{id}/plan`（仅 active 套餐）
- **操作历史 Tab**：GET `.../audit-logs`；前端本地 result 过滤
- 修改基本信息：EditPlanInfoDialog → PUT `/tenant-plans/{planId}`（idempotency_key 必填，由前端 `crypto.randomUUID()` 生成）
- 元数据管理页 `/tenants/quota-meta`：**本模块不做**（仅创建/限额表单消费 `GET /api/v1/svc/quota-meta`）

#### 7.3.4 租户管理员 `/boss/tenants/admins`

- 顶部：租户选择器（可选，选中后按该租户过滤）
- 顶部操作：邀请管理员（platform-admin / platform-ops 可见）→ 表单 email + username → 调用 `POST /tenants/{tenantId}/admins/invite`（body 含 idempotency_key），成功提示"邀请已发送"；TENANT_INVITATION_PENDING → 提示"该用户已有待接受的邀请，可重发"
- 筛选：关键字（email/username）+ 状态（active/disabled）+ 角色（tenant-owner/tenant-admin）+ 邀请状态（is_inviting：全部/正在被邀请/非邀请中）
- 表格列：email / username / display_name / role / status / source / 邀请状态（is_inviting：正在被邀请 Tag / 非邀请中） / MFA（租户 mfa_required） / last_login / actions
- 操作：查看详情、重置密码、修改权限（tenant-admin ↔ user / auditor）、禁用、启用、删除（软删除）、重发邀请（针对邀请中/过期）
- 重置密码表单：输入新密码（8-64 字符，复杂度校验）→ 提交 → 成功提示"密码已重置"
- "操作历史"标签页：调用 `GET /tenants/{tenantId}/admins/{userId}/audit-logs` 分页展示该管理员的操作历史

> 租户计费页面（§7.3.5）暂不实现，后续 PR 再补充。

#### 7.3.6 平台账号 `/boss/settings/platform-admins`

- 表格列：username / display_name / role / status / source / last_login / actions
- 操作：查看、重置密码、修改角色、禁用、启用、删除（软删除）
- 新建按钮：表单含 email / username / role 下拉（platform-admin / platform-ops / platform-readonly）
- 最后管理员保护：422 LAST_PLATFORM_ADMIN → 红色提示"无法删除/禁用最后一名活跃平台管理员"
- 重置密码表单：输入新密码（8-64 字符，复杂度校验）→ 提交 → 成功提示"密码已重置"

#### 7.3.7 租户安全 `/boss/tenants/{id}/security`（B1 + B2）

- 顶部：租户信息摘要（name / status / mfa_required 徽标 / sso_enabled 徽标 / sso_provider 徽标）
- **MFA 强制开关区块**：
  - Switch 组件显示 `tenant_auth.mfa_required` 当前值
  - 切换为 ON 时弹窗确认"该租户所有用户下次登录必须完成 MFA 二次校验，确认？"
  - 调用 `PUT /tenants/{tenantId}/auth/mfa` { mfa_required: true/false }
  - 提示："用户级 MFA enrollment 由 auth-service 负责；本端点仅切换租户级强制开关"
- **SSO 配置区块**：
  - 顶部"一键开关"Switch：显示 `tenant_auth.sso_enabled` 当前值
    - 切换为 ON：调用 `PUT /tenants/{tenantId}/auth/sso` { sso_enabled: true }；若 `sso_provider` 为 NULL，前端提示"需先选择 SSO 提供商（oidc / custom）后再开启；SSO 详细配置（issuer_url / client_id 等）由平台运维在 K8s Secret/ConfigMap 中预配置"
    - 切换为 OFF：调用 `PUT /tenants/{tenantId}/auth/sso` { sso_enabled: false }
  - 显示当前配置（GET /tenants/{tenantId}/auth/sso）：sso_enabled / provider / updated_at（SSO 详细配置不在 tenant_auth 表中存放，前端不展示 issuer_url / client_id 等字段；如需查看请联系平台运维查询 K8s Secret/ConfigMap）
  - 编辑按钮：弹出表单 PUT /tenants/{tenantId}/auth/sso（部分更新，tenantId 在路径参数）
    - provider 下拉：oidc / custom（写入 sso_provider）
    - 提示文案："SSO 详细配置（issuer_url / client_id / client_secret_ref / scopes / auto_provision / email_domains）由平台运维在 K8s Secret/ConfigMap 中维护，本表单仅切换 SSO 开关与提供商类型"
  - 测试连接按钮：调用 `POST /tenants/{tenantId}/auth/sso/test`，后端根据 sso_provider 从外部系统加载详细配置进行 OIDC discovery 测试
  - 错误提示：TENANT_SSO_CONFIG_INVALID → 内联提示"sso_provider 未配置或外部系统缺少 SSO 详细配置"

#### 7.3.8 租户生命周期 `/boss/tenants/{id}/lifecycle`

- 调用 `GET /tenants/{tenantId}/lifecycle` 分页展示租户生命周期记录列表（按 created_at DESC 排序）
- 表格列：action（状态/动作）、reason（原因，NULL 显示"—"）、user_id（操作者，NULL 显示"系统"）、created_at（时间）
- 支持 action 过滤下拉（active / frozen / disabled）
- 分页控件（limit / cursor 游标分页，加载更多 / 上一页下一页）
- 权限：platform-admin / platform-ops / platform-readonly 可访问

#### 7.3.9 租户操作历史 `/boss/tenants/{id}/audit-logs`

- 调用 `GET /tenants/{tenantId}/audit-logs` 分页展示操作历史列表（按 created_at DESC 排序）
- 表格列：action（操作类型）、resource（资源）、result（结果，success 绿色 / failed 红色）、user_id（操作者，NULL 显示"系统"）、details（详情，可展开）、ip_address、created_at（时间）
- 支持 action 过滤下拉和 result 过滤下拉（success / failed）
- 分页控件（limit / cursor 游标分页，加载更多 / 上一页下一页）
- 权限：platform-admin / platform-ops / platform-readonly 可访问

### 7.4 API 客户端

```ts
// repo/frontends/boss/src/api/tenant.ts
import { fetchCore, fetchCoreAdmin } from './client';

export type TenantStatus = 'active' | 'frozen' | 'disabled';
export type PlanStatus = 'draft' | 'active' | 'disabled';

export interface Tenant {
  id: string;
  name: string;            // 英文 slug 风格唯一 key（不可改；活动租户间唯一）
  display_name: string;    // 中文显示名（可改）
  email: string;           // 租户联系邮箱（可改）
  plan_id: string;        // 外键 → tenant_plans.id（tenants 表只存 plan_id）
  plan_code: string;       // 由后端 service 层 JOIN tenant_plans 读取，不在 tenants 表存
  status: TenantStatus;
  frozen_at: string | null;
  disabled_at: string | null;
  created_at: string;
  updated_at: string;
  user_count: number;   // 租户用户总数（含所有角色）
  admin_count: number;  // 租户管理员数（tenant-admin 角色）
  // 配额字段不在 Tenant interface 内；由 Core resource_quota 承载，通过 /api/v1/admin/tenants/{id}/quota/* 查询
}

export interface SSOConfigView {
  sso_enabled: boolean;
  provider: 'oidc' | 'custom' | null;
  updated_at: string;
}

export interface TenantPlan {
  id: string;             // 主键，被 tenants.plan_id 引用
  code: string;           // 人类可读唯一标识；tenants 表不存此字段
  name: string;
  description: string | null;
  status: PlanStatus;
  tenant_count: number;   // 绑定租户数量
  created_at: string;
  updated_at: string;
}

export interface PlanQuotaLimitView {
  resource_type: string;          // 外键 → resource_quota_meta.resource_type
  display_name: string;           // 展示名（来自 Core quota-meta）
  unit: string;                   // 单位（来自 Core quota-meta）
  total: number;                  // 后端已用 default_quota 兜底，始终为具体数值
}

export interface TenantListItem {
  id: string;
  name: string;            // 英文 slug 风格唯一 key（不可改；活动租户间唯一）
  display_name: string;    // 中文显示名（可改）
  plan_id: string;         // 外键 → tenant_plans.id
  plan_code: string;       // 由后端 service 层 JOIN tenant_plans 读取
  status: TenantStatus;
  admin_count: number;     // 租户管理员数量（后端 COUNT(users) JOIN 装配）
  created_at: string;
}

export interface ListTenantsParams {
  limit?: number;
  cursor?: string;          // 上一页返回的 next_cursor
  status?: TenantStatus;
  search?: string;         // name / display_name 模糊匹配
}

export async function listTenants(params: ListTenantsParams): Promise<PagedResult<TenantListItem>> {
  return fetchCore('/api/v1/svc/tenants', { method: 'GET', query: params });
}

export async function createTenant(input: CreateTenantInput, idempotencyKey: string): Promise<{ id: string; message: string }> {
  return fetchCore('/api/v1/svc/tenants', {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

// B2 MFA 强制开关
export async function updateTenantMFARequired(
  id: string,
  mfa_required: boolean,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${id}/auth/mfa`, {
    method: 'PUT',
    body: { mfa_required },
  });
}

// B1 SSO 配置
export interface TenantSSOView {
  sso_enabled: boolean;
  provider: 'oidc' | 'custom' | null;
  updated_at: string;
}

export async function getTenantSSO(id: string): Promise<TenantSSOView> {
  return fetchCore(`/api/v1/svc/tenants/${id}/auth/sso`, { method: 'GET' });
}

export async function updateTenantSSO(
  id: string,
  input: {
    sso_enabled?: boolean;
    provider?: 'oidc' | 'custom';
  },
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${id}/auth/sso`, {
    method: 'PUT',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export async function testTenantSSOConnection(id: string): Promise<{
  success: boolean;
  provider: string;
  discovery_result: {
    authorization_endpoint: string;
    token_endpoint: string;
    userinfo_endpoint: string;
  } | null;
  error: string | null;
  tested_at: string;
}> {
  return fetchCore(`/api/v1/svc/tenants/${id}/auth/sso/test`, { method: 'POST' });
}

export interface AdminWithTenant {
  id: string;
  email: string;
  username: string;
  display_name: string | null;
  role: string;          // 该租户内角色：tenant-owner / tenant-admin（列表仅含 owner/admin/邀请中；邀请中用户仍展示原有角色，可为 user）
  status: string;
  is_inviting: boolean;   // true = 正在被邀请（该租户下存在 status='inviting' 的邀请）；仅作标记，不影响 role/status
  source: 'third_party' | 'local';
  last_login_at: string | null;
  created_at?: string;    // 详情响应返回（跨租户列表响应不返回，故可选）
  updated_at?: string;    // 详情响应返回（跨租户列表响应不返回，故可选）
  tenant: {
    id: string;
    name: string;
    display_name: string;
    mfa_required: boolean;
  };
}

export interface InvitationResult {
  id: string;
  token: string;
  expire_at: string;
  message: string;
}

export async function inviteTenantAdmin(
  tenantId: string,
  input: { email: string; username: string },
  idempotencyKey: string,
): Promise<InvitationResult> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/invite`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export async function resendTenantAdminInvitation(
  tenantId: string,
  userId: string,
  idempotencyKey: string,
): Promise<InvitationResult> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/invitation/resend`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey },
  });
}

export async function transferOwnership(
  tenantId: string,
  targetUserId: string,
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/transfer-ownership`, {
    method: 'POST',
    body: { target_user_id: targetUserId, idempotency_key: idempotencyKey },
  });
}

export interface UserPermissions {
  user_id: string;
  tenant_id: string | null;
  role: string;
  permissions: {
    compute: 'read' | 'write' | 'none';
    inference: 'read' | 'write' | 'none';
    member: 'read' | 'write' | 'none';
    transfer: 'read' | 'write' | 'none';
  };
}

export async function getRolePermissions(tenantId: string, userId: string): Promise<UserPermissions> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/role`, { method: 'GET' });
}

export async function getAllTenantAdmins(
  limit: number = 20,
  cursor?: string,
  filters?: { tenant_id?: string; role?: string; status?: string; is_inviting?: boolean; search?: string },
): Promise<{ items: AdminWithTenant[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (filters?.tenant_id) params.set('tenant_id', filters.tenant_id);
  if (filters?.role) params.set('role', filters.role);
  if (filters?.status) params.set('status', filters.status);
  if (filters?.is_inviting !== undefined) params.set('is_inviting', String(filters.is_inviting));
  if (filters?.search) params.set('search', filters.search);
  return fetchCore(`/api/v1/svc/tenant-admins?${params}`, { method: 'GET' });
}

export async function updateTenantAdminRole(
  tenantId: string,
  userId: string,
  input: { role: 'user' | 'auditor' | 'tenant-admin' },
  idempotencyKey: string,
) {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/role`, {
    method: 'PUT',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export async function createPlatformAdmin(
  input: { email: string; username: string; role: 'platform-admin' | 'platform-ops' | 'platform-readonly'; password: string },
  idempotencyKey: string,
) {
  return fetchCore('/api/v1/svc/platform-admins', {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export interface PlatformPermissions {
  user_id: string;
  role: string;
  permissions: {
    tenant_ops: 'read' | 'write' | 'none';
    resource_pool: 'read' | 'write' | 'none';
    platform_user: 'read' | 'write' | 'none';
    audit_export: 'read' | 'write' | 'none';
  };
}

export async function getPlatformPermissions(userId: string): Promise<PlatformPermissions> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}/permissions`, { method: 'GET' });
}

export interface CreateTenantInput {
  name: string;
  display_name: string;
  email: string;
  plan_id: string;
  admin_email: string;
  admin_name: string;
  admin_password: string;
}

export interface PlatformAdmin {
  id: string;
  username: string;
  display_name: string | null;
  role: string;
  status: string;
  source: 'third_party' | 'local';
  last_login_at: string | null;
}

export interface PlatformAdminDetail {
  id: string;
  email: string;
  username: string;
  display_name: string | null;
  role: string;
  status: string;
  source: 'third_party' | 'local';
  last_login_at: string | null;
  created_at: string;
}

export async function getAdminDetail(tenantId: string, userId: string): Promise<AdminWithTenant> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}`, { method: 'GET' });
}

export async function resetTenantAdminPassword(
  tenantId: string,
  userId: string,
  newPassword: string,
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/reset-password`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, new_password: newPassword },
  });
}

export async function disableTenantAdmin(tenantId: string, userId: string, idempotencyKey: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/disable`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey },
  });
}

export async function enableTenantAdmin(tenantId: string, userId: string, idempotencyKey: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/enable`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey },
  });
}

export async function deleteTenantAdmin(tenantId: string, userId: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}`, { method: 'DELETE' });
}

export async function listPlatformAdmins(
  limit: number = 20,
  cursor?: string,
  filters?: { role?: string; status?: string; search?: string },
): Promise<{ items: PlatformAdmin[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (filters?.role) params.set('role', filters.role);
  if (filters?.status) params.set('status', filters.status);
  if (filters?.search) params.set('search', filters.search);
  return fetchCore(`/api/v1/svc/platform-admins?${params}`, { method: 'GET' });
}

export async function getPlatformAdminDetail(userId: string): Promise<PlatformAdminDetail> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}`, { method: 'GET' });
}

export async function updatePlatformAdminRole(
  userId: string,
  role: 'platform-admin' | 'platform-ops' | 'platform-readonly',
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}/role`, {
    method: 'PUT',
    body: { idempotency_key: idempotencyKey, role },
  });
}

export async function resetPlatformAdminPassword(
  userId: string,
  newPassword: string,
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}/reset-password`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, new_password: newPassword },
  });
}

export async function disablePlatformAdmin(userId: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}/disable`, { method: 'POST' });
}

export async function enablePlatformAdmin(userId: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}/enable`, { method: 'POST' });
}

export async function deletePlatformAdmin(userId: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/platform-admins/${userId}`, { method: 'DELETE' });
}
```

```ts
// repo/frontends/boss/src/api/quota_meta.ts  (代理 Core API /api/v1/admin/quota-meta/*)
import { fetchCoreAdmin } from './client';

export interface QuotaResourceMeta {
  resource_type: string;        // 'gpu_count' | 'cpu_core' | 'memory_gb' | ...
  display_name: string;
  unit: string;                 // 'share' | 'core' | 'gb' | ...
  is_discrete: boolean;
  default_quota: number;
  collector_id: string | null;   // 'prometheus_dcgm' | 'prometheus_kubelet' | null
  description: string | null;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export async function listQuotaMeta(): Promise<QuotaResourceMeta[]> {
  return fetchCoreAdmin('/api/v1/admin/quota-meta', { method: 'GET' });
}

export async function registerQuotaMeta(
  input: { resource_type: string; display_name: string; unit: string; is_discrete: boolean; default_quota: number; collector_id?: string; description?: string },
  idempotencyKey: string,
): Promise<QuotaResourceMeta> {
  return fetchCoreAdmin('/api/v1/admin/quota-meta', {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export async function updateQuotaMeta(
  resourceType: string,
  input: Partial<Pick<QuotaResourceMeta, 'display_name' | 'default_quota' | 'collector_id' | 'description' | 'enabled'>>,
  idempotencyKey: string,
): Promise<QuotaResourceMeta> {
  return fetchCoreAdmin(`/api/v1/admin/quota-meta/${resourceType}`, {
    method: 'PUT',
    body: { idempotency_key: idempotencyKey, ...input },
  });
}

export async function disableQuotaMeta(resourceType: string, idempotencyKey: string): Promise<void> {
  return fetchCoreAdmin(`/api/v1/admin/quota-meta/${resourceType}/disable`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey },
  });
}
```

```ts
// repo/frontends/boss/src/api/tenant_quota.ts  (代理 Core API /api/v1/admin/tenants/{id}/quota/*)
import { fetchCoreAdmin } from './client';

export interface TenantQuotaLine {
  resource_type: string;
  total: number;       // 租户上限
  reserved: number;    // 运行时预占
  used: number;        // 运行时实扣
  updated_at: string;
}

export async function listTenantQuota(tenantId: string): Promise<TenantQuotaLine[]> {
  return fetchCoreAdmin(`/api/v1/admin/tenants/${tenantId}/quota`, { method: 'GET' });
}

export async function updateTenantQuotaTotal(
  tenantId: string,
  items: { resource_type: string; total: number }[],
  idempotencyKey: string,
): Promise<{ tenant_id: string; items: { resource_type: string; total: number; used: number; reserved: number; tightened: boolean }[] }> {
  return fetchCoreAdmin(`/api/v1/admin/tenants/${tenantId}/quota`, {
    method: 'PUT',
    body: { idempotency_key: idempotencyKey, items },
  });
}

export async function queryTenantQuotaUsage(
  tenantId: string,
  params: { period_start: string; period_end: string },
): Promise<{
  usage_records: Array<{ record_date: string; resource_type: string; quantity: number; unit: string }>;
}> {
  return fetchCoreAdmin(`/api/v1/admin/tenants/${tenantId}/quota/usage`, { method: 'GET', query: params });
}
```

```ts
// repo/frontends/boss/src/api/quota_change_request.ts  (调用 Services API /api/v1/svc/tenants/{tenantId}/quota*)
import { fetchCore } from './client';

// 配额查询与变更申请
export interface QuotaItem {
  resource_type: string;
  display_name: string;
  used: number;
  total: number;
  unit: string;
}

export async function getTenantQuota(tenantId: string): Promise<{ items: QuotaItem[] }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/quota`, { method: 'GET' });
}

export async function createQuotaChangeRequest(
  tenantId: string,
  items: Array<{ resource_type: string; new_value: number }>,
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/quota-requests`, {
    method: 'POST',
    body: { idempotency_key: idempotencyKey, items },
  });
}

// 套餐绑定：POST /tenants/{tenantId}/plan（§5.3.9；实际实现在 tenant-plans.ts 中使用 OpenAPI 类型化客户端）
// bindTenantPlan(tenantId: string, planId: string): Promise<IdempotentResult>
//   — 幂等键由函数内 crypto.randomUUID() 自动生成

// 套餐限额查询（§5.3.4）
export interface PlanQuotaLimitView {
  resource_type: string;
  display_name: string;
  unit: string;
  total: number;  // 后端已用 default_quota 兜底，始终为具体数值
}

export async function getPlanQuotaLimits(planId: string): Promise<{ items: PlanQuotaLimitView[] }> {
  return fetchCore(`/api/v1/svc/tenant-plans/${planId}/quota-limits`, { method: 'GET' });
}

// 套餐限额修改（§5.3.8，同步存量租户）
export interface UpdatePlanQuotaLimitItem {
  resource_type: string;
  total: number | null;
}

export async function updatePlanQuotaLimits(
  planId: string,
  items: UpdatePlanQuotaLimitItem[],
  idempotencyKey: string,
): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenant-plans/${planId}/quota-limits`, {
    method: 'PUT',
    body: JSON.stringify({ idempotency_key: idempotencyKey, items }),
  });
}

export async function deleteTenantPlan(planId: string): Promise<{ id: string; message: string }> {
  return fetchCore(`/api/v1/svc/tenant-plans/${planId}`, { method: 'DELETE' });
}

// 生命周期查询
export interface LifecycleEvent {
  id: string;
  tenant_id: string;
  action: string;
  reason: string | null;
  user_id: string | null;
  request_id: string | null;
  created_at: string;
}

export async function getTenantLifecycle(
  tenantId: string,
  limit: number = 20,
  cursor?: string,
  action?: string,
): Promise<{ items: LifecycleEvent[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (action) params.set('action', action);
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/lifecycle?${params}`, { method: 'GET' });
}

// 操作历史查询
export interface AuditLogEntry {
  id: string;
  tenant_id: string | null;
  user_id: string | null;
  request_id: string | null;
  action: string;
  resource: string;
  result: string;
  details: Record<string, unknown> | null;
  ip_address: string | null;
  user_agent: string | null;
  created_at: string;
}

export async function getTenantAuditLogs(
  tenantId: string,
  limit: number = 20,
  cursor?: string,
  action?: string,
  result?: string,
): Promise<{ items: AuditLogEntry[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (action) params.set('action', action);
  if (result) params.set('result', result);
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/audit-logs?${params}`, { method: 'GET' });
}

export async function getAdminAuditLogs(
  tenantId: string,
  userId: string,
  limit: number = 20,
  cursor?: string,
  action?: string,
  result?: string,
): Promise<{ items: AuditLogEntry[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (action) params.set('action', action);
  if (result) params.set('result', result);
  return fetchCore(`/api/v1/svc/tenants/${tenantId}/admins/${userId}/audit-logs?${params}`, { method: 'GET' });
}

export async function getPlanAuditLogs(
  planId: string,
  limit: number = 20,
  cursor?: string,
  action?: string,
  result?: string,
): Promise<{ items: AuditLogEntry[]; next_cursor: string | null }> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set('cursor', cursor);
  if (action) params.set('action', action);
  if (result) params.set('result', result);
  return fetchCore(`/api/v1/svc/tenant-plans/${planId}/audit-logs?${params}`, { method: 'GET' });
}
```

### 7.5 TanStack Query 使用

- `useQuery(['tenants', filters], listTenants)`
- `useMutation(createTenant, { onSuccess: () => qc.invalidateQueries(['tenants']) })`
- 写操作 mutation 由前端生成 `idempotency_key = crypto.randomUUID()` 放入 request body

### 7.6 路由结构

```
repo/frontends/boss/src/routes/_authenticated/
├── tenants/
│   ├── index.tsx                       列表
│   ├── quota-meta.tsx                  配额元数据（代理 Core API /api/v1/admin/quota-meta/*）
│   ├── $tenantId/
│   │   ├── edit.tsx                     编辑
│   │   ├── quota.tsx                    配额配置（代理 Core API /api/v1/admin/tenants/{id}/quota/*）
│   │   └── security.tsx                 租户安全（MFA + SSO）
│   ├── admins.tsx                       管理员
│   └── plans.tsx                        套餐管理
└── settings/
    └── platform-admins.tsx              平台账号
```

### 7.7 跨端链接

`boss/src/routes/login.tsx` 与 `console/src/routes/login.tsx` 已含跨端跳转（DEV 模式下根据 `import.meta.env.DEV` 切换 basepath）。无需改动。

---

## §8 异常处理与错误码

### 8.1 错误码总表（已在 §5.7 列出）

### 8.2 客户端处理策略

| HTTP | code | 前端行为 |
|------|------|---------|
| 400 | VALIDATION_FAILED | 表单内联错误提示 |
| 400 | IDEMPOTENCY_KEY_INVALID | 重新生成 key 重试 |
| 403 | FORBIDDEN | 提示无权限 |
| 403 | TENANT_FROZEN | 提示租户已冻结，仅可查看资源 |
| 403 | TENANT_DISABLED | 提示租户已禁用，禁止登录 |
| 404 | *_NOT_FOUND | 提示不存在 + 返回列表 |
| 409 | TENANT_NAME_CONFLICT | name 字段错误提示（提示已被活动租户占用） |
| 409 | TENANT_STATE_INVALID | 提示当前状态不允许该操作 |
| 409 | IDEMPOTENCY_CONFLICT | 提示"请勿重复提交" |
| 409 | PLAN_CODE_CONFLICT | 套餐 code 字段错误提示 |
| 409 | TENANT_OWNER_ROLE_LOCKED | 提示"租户所有者角色不可修改/删除" |
| 422 | PLAN_NOT_ACTIVE | 提示"套餐未发布，无法被租户引用" |
| 422 | TRANSFER_TARGET_INVALID | 提示"移交目标用户不存在、非 active、或非 tenant-admin 角色" |
| 422 | ROLE_CHANGE_INVALID | 提示"角色不在允许范围内" |
| 422 | PASSWORD_SAME_AS_OLD | 提示"新密码与旧密码相同" |
| 422 | LAST_PLATFORM_ADMIN | 红色 toast 提示 |
| 500 | INTERNAL | 灰色 toast 提示"系统繁忙" |

### 8.3 服务端日志

- 所有 4xx 记录 WARN 级日志（含 request_id、idempotency_key、user_id、path、错误码）
- 所有 5xx 记录 ERROR 级日志 + 上报链路追踪
- 审计日志只记写操作成功，不记失败

### 8.4 重试策略

| 错误 | 客户端重试 | 服务端重试 |
|------|-----------|-----------|
| 网络超时 | 最多 1 次 | 否 |
| 5xx | 最多 1 次（指数退避 1s） | 否 |
| 4xx | 否 | 否 |
| 409 IDEMPOTENCY_CONFLICT | 否 | 否 |

---

## §9 测试设计

### 9.1 单元测试

#### 9.1.1 service 层

| 测试 | 目的 |
|------|------|
| TestTenantService_Create | name 活动租户间唯一、admin_password bcrypt 入库、tenants 与 users 同事务一致性；**配额初始化通过调用 Core API 逐维度完成（按 resource_quota_meta.default_quota 写入 Core resource_quota 表）** |
| TestTenantService_Freeze | active 可冻结，frozen/disabled 不可冻结（disabled 已删除资源）；冻结后状态=frozen、资源保持原状、用户无法登录 |
| TestTenantService_Unfreeze | frozen 可解冻→active；非 frozen 不可解冻 |
| TestTenantService_Disable_State | active/frozen 可禁用，disabled 不可禁用 |
| TestTenantService_UpdatePlan | 切换套餐：active 校验；**仅修改 plan_id，不调用 ReplaceFromPlan、不刷新配额**；audit_log 记录 old/new plan_id |
| TestTenantService_Idempotency | 同 key 同 body 回放、同 key 不同 body 409 |
| TestTenantPlanService_CRUD | Create 创建套餐（含 quota_limits）；PUT /tenant-plans/{planId} 修改 name/description；套餐限额可通过 PUT /quota-limits 修改并同步存量租户；Delete 软删除（有租户关联 409 TENANT_PLAN_IN_USE；任意状态可删除） |
| TestTenantPlanService_QuotaLimits | GetQuotaLimitViews：total=NULL 时用 default_quota 兜底为具体值；total 非 NULL 时用指定值 |
| TestTenantPlanService_Activate | draft→active、disabled→active；active 不可再 active |
| TestTenantPlanService_Disable | active→disabled；draft 不可直接 disable |
| TestPlatformAdminService_LastAdmin | 活跃数 ≤ 1 时 422 |
| TestPlatformAdminService_ResetPassword | 调用方提供新密码、bcrypt 校验、与旧密码不同校验 |
| TestPlatformAdminService_ChangeRole | 改角色生效、audit_log 记录 |
| TestTenantAdminService_ResetPassword | 调用方提供新密码、bcrypt 校验、与旧密码不同校验 |

> 计费相关测试（TestTenantBillingService_*、TestBillingRecordStore_*）暂不实现，后续 PR 再补充。

#### 9.1.2 adapter 层

| 测试 | 目的 |
|------|------|
| TestTenantStore_CRUD | 增删改查 SQL 正确 |
| TestTenantStore_RLS | 平台上下文绕过、租户上下文受限 |
| TestTenantPlanStore_CRUD | 套餐 CRUD、status 状态机约束（CHECK） |
| TestTenantPlanStore_QuotaLimits | plan_quota_limits INSERT/SELECT；COALESCE(total, default_quota) 兜底逻辑；软删除套餐时限额行保留（不触发 ON DELETE CASCADE） |
| TestAuditLogStore_Insert | 字段完整、索引命中 |

### 9.2 集成测试

| 测试 | 目的 |
|------|------|
| TestHandler_CreateTenant_FullFlow | POST（含 admin_password）→ 200 `{ id, message }`（无 temporary_password）→ GET /tenants/{tenantId} → 详情匹配；admin_password 不在响应、日志、审计中明文出现；配额初始化通过 Core API 逐维度完成（不写本服务表） |
| TestHandler_Idempotency_Replay | 同 key 重放（返回缓存响应 + Idempotent-Replay: true），同 key 不同 body 409 |
| TestHandler_UpdatePlanInfo | PUT /tenant-plans/{planId} 修改 name/description → 200 `{ id, message }`；nil=不更新，空串=清空；审计 details 含 plan_id/name_updated/description_updated |
| TestHandler_QuotaChangeRequest | POST /tenants/{tenantId}/quota-requests 提交 → 200 { id, message }；一次提交多维度 items[] 批量申请；old_value 从 Core resource_quota.total 读取；同一 tenant_id + resource_type 重复 pending → 409 QUOTA_CHANGE_REQUEST_DUPLICATE；resource_type 不存在 → 422 QUOTA_CHANGE_REQUEST_INVALID |
| TestHandler_PlanLifecycle | 创建 draft（含 quota_limits）→ active → disabled → 重新 active；GET /tenant-plans/{planId}/quota-limits 返回 items[]{resource_type, display_name, unit, total}；DELETE /tenant-plans/{planId} 删除 → 200；有租户关联 → 409 TENANT_PLAN_IN_USE |
| TestHandler_FreezeUnfreeze | 冻结后用户无法登录，解冻后恢复全功能 |
| TestHandler_LastPlatformAdmin | 删除/禁用最后一名活跃 platform-admin 422 |
| TestHandler_Roles | platform-readonly 写操作 403 |

### 9.3 前端测试

| 测试 | 目的 |
|------|------|
| TenantListPage_render | 列表渲染、状态徽标、分页、admin_count 列展示 |
| TenantListPage_EditPlan | 编辑表单切换套餐：仅修改 plan_id；提示"配额需单独在配额页面调整" |
| TenantCreateForm_validation | 字段校验、提交后跳转 |
| QuotaMetaPage_list | 配额元数据列表渲染、维度新增/编辑/禁用（代理 Core API） |
| TenantQuotaPage_list | 租户配额列表渲染、total 编辑、用量进度条（代理 Core API） |
| TenantAdminResetPassword_dialog | 新密码输入弹窗、复杂度校验 |
| PlatformAdminPage_LastAdmin | 422 LAST_PLATFORM_ADMIN toast |

### 9.4 E2E

| 场景 | 步骤 |
|------|------|
| 创建租户 | BOSS 登录 → /boss/tenants → 新建（选 active 套餐 + admin email + admin username + admin password） → 表单提交 → 列表显示 → 详情查看；配额由 Core 在创建时按 resource_quota_meta.default_quota 自动初始化 |
| 冻结/解冻 | 列表 → 冻结 → 状态变 frozen → Console 登录被禁止 → 解冻 → 状态变 active |
| 禁用 | 列表 → 禁用 → 状态变 disabled → 禁止登录、资源删除（不可逆，无启用入口） |
| 切换套餐 | 详情 → 编辑 → 切到更高级套餐 → 提交 → 仅修改 plan_id，不影响配额；前端提示"配额需单独在配额页面调整" |
| 配置配额元数据 | /boss/tenants/quota-meta → 新建维度（resource_type/display_name/unit/default_quota）→ 提交 → 列表显示新维度 |
| 调整租户配额 | /boss/tenants/{id}/quota → 修改某维度 total → 提交 → 列表显示新值（用量进度条同步刷新） |
| 重置密码 | 列表 → 重置密码 → 输入新密码 → 用新密码登录 Console |
| 套餐生命周期 | /boss/tenant-plans → 创建 draft（含 quota_limits）→ 查看限额 → activate → 创建租户选该套餐 → disable 套餐 → 已用该套餐的租户继续可用但不可新租户选 → 删除套餐 → 200；有租户关联 → 409 TENANT_PLAN_IN_USE |
| 平台账号管理 | /boss/settings/platform-admins → 创建 → 修改角色 → 删除最后管理员 → 422 |

### 9.5 验收脚本

```bash
# 后端
cd repo && make test
cd repo && make validate-architecture
cd repo && git diff --check

# tenant-service 单独
cd repo/services/tenant-service && go test ./...

# 前端
cd repo/frontends/boss && pnpm type-check
cd repo/frontends/boss && pnpm lint
cd repo/frontends/boss && pnpm build
```

---

## §10 具体实现清单

### 10.1 PR-1（契约阶段）

| # | 任务 | 文件 |
|---|------|------|
| 1.1 | services/v1.yaml 新增 tenants 路径与 schema（含 freeze/unfreeze/disable 子路径；列表 GET /tenants 仅含 limit / cursor / status / search 四个 query 参数，不含 plan_id；响应 items[].admin_count 由后端 JOIN COUNT 装配，不含 email；MFA/SSO 字段存放在 tenant_auth 表（§4.1.2），仅 3 个业务字段 mfa_required / sso_enabled / sso_provider，SSO 详细配置由外部系统承载；**本服务不定义 quotas 子路径**——配额元数据/配额配置/用量查询全部由 Core API `/api/v1/admin/quota-meta/*` 与 `/api/v1/admin/tenants/{id}/quota/*` 承载；身份认证统一以 `/tenants/{tenantId}/auth` 为前缀，tenantId 通过路径参数传递：B1 auth/sso 子路径 GET/PUT/POST test（PUT 修改 SSO 状态与 IdP 提供商；POST 测试 SSO 连接）；B2 auth/mfa PUT） | `repo/api/openapi/services/v1.yaml` |
| 1.2 | services/v1.yaml 新增 tenant-admins 路径与 schema | 同上 |
| 1.3 | services/v1.yaml 新增 platform-admins 路径与 schema（含 reset-password/disable/enable/change-role/delete） | 同上 |
| 1.4 | services/v1.yaml 新增 tenant-plans 路径与 schema（draft/active/disabled 状态机 + activate/disable 端点 + quota-limits 查询端点；**不含九维 default_max_***——配额模板由 Core resource_quota_meta.default_quota 承载） | 同上 |
| 1.5 | services/v1.yaml 新增错误码（PLAN_* / LAST_PLATFORM_ADMIN / TENANT_SSO_CONFIG_INVALID / MFA_REQUIRED）、标签、通用 idempotency_key body 字段；**配额相关错误码（TENANT_NOT_FOUND / QUOTA_NOT_FOUND / QUOTA_ALREADY_EXISTS / QUOTA_RESOURCE_NOT_REGISTERED / GRPC_CLIENT_UNAVAILABLE）由网关层在 `businessCodeByHTTP` 中定义 HTTP 状态映射，确保 Core 返回的业务错误能正确还原为 HTTP 状态码** | 同上 |
| 1.6 | 重新生成前端 schema.d.ts | `repo/frontends/{boss,console}/src/api/schema.d.ts` |
| 1.7 | OpenAPI lint 校验 | `make openapi-lint` |
| 1.8 | 提交 PR-1 | 标题：`feat(services/v1): add tenant management API contracts` |

### 10.2 PR-2（接口阶段）

| # | 任务 | 文件 |
|---|------|------|
| 2.1 | ports.TenantStore 接口（含 UpdatePlan；**不含 SSO/MFA 方法**——由 TenantAuthStore 承载；**不含 UpdateQuotas**——配额由 Core QuotaService 承载） | `services/tenant-service/internal/repo/ports/tenant_store.go`（配额套餐所需最小 TenantStore 已下沉；完整版后续扩展） |
| 2.2 | ports.TenantAdminStore 接口 | `repo/pkg/ports/tenant_admin_store.go` |
| 2.3 | ports.PlatformAdminStore 接口（含 ResetPassword / ChangeRole / Disable / Enable / SoftDelete） | `repo/pkg/ports/platform_admin_store.go` |
| 2.4 | ports.TenantPlanStore 接口（含 Activate / Disable / Delete；GetQuotaLimits / UpdateQuotaLimits / ListBoundTenants / ListBindableTenants / GetApprovedQuotaChanges；**不含 GetQuotaLimitViews**——由 service 层 buildQuotaLimitViews 组装；**不含九维 default_max_***） + ports.QuotaSvcClient 接口（ListQuotaMeta / GetQuota / CreateQuota / PutQuota / DeleteQuota） | `services/tenant-service/internal/repo/ports/tenant_plan_store.go` + `services/tenant-service/internal/repo/ports/core_quota.go`（已下沉 tenant-service 自有 repo） |
| 2.5 | ports.AuditLogStore 接口 | `repo/pkg/ports/audit_log_store.go` |
| 2.5b | ports.TenantLifecycleStore 接口（Append / ListByTenant） | `repo/pkg/ports/tenant_lifecycle_store.go` |
| 2.5c | ports.TenantAuthStore 接口（Create / Get / GetSSOConfig / UpdateSSO / UpdateMFARequired） | `repo/pkg/ports/tenant_auth_store.go` |
| 2.5d | ports.QuotaChangeRequestStore 接口（Create / ListByTenant / GetByID / UpdateStatus） | `repo/pkg/ports/quota_change_request_store.go` |
| 2.6 | tenant-service cmd/main.go | `repo/services/tenant-service/cmd/main.go` |
| 2.7 | tenant-service handlers 空实现（501）含 `GET /tenants/{tenantId}/auth/sso`、`PUT /tenants/{tenantId}/auth/sso`、`POST /tenants/{tenantId}/auth/sso/test`、`PUT /tenants/{tenantId}/auth/mfa`、`GET /tenants/{tenantId}/quota`、`POST /tenants/{tenantId}/quota-requests`、`GET /tenants/{tenantId}/quota-requests`、`POST /tenants/{tenantId}/quota-requests/{reqId}/approve`、`GET /tenant-plans/{planId}/bindable-tenants`、`GET /tenants/{tenantId}/lifecycle`、`GET /tenants/{tenantId}/audit-logs`、`GET /tenant-admins`（跨租户查询所有管理员；**不含 `PUT /tenants/quotas`**——配额走 Core API）、`GET /tenants/{tenantId}/admins/{userId}/audit-logs`、`GET /tenant-plans/{planId}/audit-logs` | `repo/services/tenant-service/handlers/*.go` |
| 2.8 | tenant-service wiring 骨架（注入 Core API 客户端：quotaSvcClient / quotaMetaSvcClient） | `repo/services/tenant-service/internal/wiring/wiring.go` |
| 2.9 | service 骨架（接口签名 + panic("not implemented")） | `repo/services/tenant-service/internal/service/*.go` |
| 2.10 | make test 通过（编译 + 现有测试） | — |
| 2.11 | 提交 PR-2 | 标题：`feat(tenant-service): add ports and handler scaffolding` |

### 10.3 PR-3（实现+文档阶段）

| # | 任务 | 文件 |
|---|------|------|
| 3.1 | SQL 迁移（tenant_plans + draft/active/disabled；**plan_quota_limits 表**（Core 层，外键关联 tenant_plans + resource_quota_meta）；**resource_quota_meta seed 扩展到 8 维度**（storage_gb / token_count / kb_query_count / member_count / inference_service_count）；**tenant_lifecycle 表**；**tenant_auth 表**（MFA/SSO 配置，1:1 关系）；**不新建 tenant_quotas 表**——租户配额由 Core resource_quota 承载；tenants 含 contact_email + status 旧→新迁移（不含 MFA/SSO 字段、不含九维 max_*、不含 slug 列）；**name 全局 UNIQUE 约束 + name 格式 CHECK**；**不新建 billing_records / tenant_usage_records 表**——用量由 Core metering_usage_records 承载；平台角色种子。**users 表 ALTER**：新增 is_deleted/deleted_at/display_name 三列（§4.1.4 迁移注意）；**tenant_quota_change 表**（§4.1.6，配额变更申请，pending/approved/rejected 状态机）；audit_logs 复用现有分区表不新建） | `repo/deploy/migrations/20260723_015_tenant_management.sql` |
| 3.2 | postgres.TenantStore 实现（Create 仅写身份/状态 + 同步 INSERT tenant_auth，**配额初始化通过调用 Core API 逐维度完成，不在本服务 SQL 内写配额表**；UpdatePlan 仅改 plan_id，不影响配额；Freeze/Unfreeze/Disable） | `services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`（已下沉；当前占位，后续补全实现） |
| 3.3 | postgres.TenantAdminStore 实现（ResetPassword、Disable/Enable/SoftDelete） | `repo/pkg/adapters/postgres/tenant_admin_store.go` |
| 3.4 | postgres.PlatformAdminStore 实现（LastAdmin 保护、ChangeRole、ResetPassword） | `repo/pkg/adapters/postgres/platform_admin_store.go` |
| 3.5 | postgres.TenantPlanStore 实现（CRUD + Activate + Disable + Delete + GetQuotaLimits / UpdateQuotaLimits / ListBoundTenants / ListBindableTenants / GetApprovedQuotaChanges；**不含 GetQuotaLimitViews**——由 service 层 buildQuotaLimitViews 组装；**不含九维 default_max_***） + core.QuotaSvcClient 实现（ListQuotaMeta / GetQuota / CreateQuota / PutQuota / DeleteQuota） | `services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go` + `services/tenant-service/internal/repo/adapters/core/quota_client.go`（已下沉；当前占位，后续补全实现） |
| 3.6 | postgres.AuditLogStore 实现（复用现有 audit_logs 分区表） | `repo/pkg/adapters/postgres/audit_log_store.go` |
| 3.6b | postgres.TenantLifecycleStore 实现（Append / ListByTenant） | `repo/pkg/adapters/postgres/tenant_lifecycle_store.go` |
| 3.6c | postgres.TenantAuthStore 实现（Create / Get / GetSSOConfig / UpdateSSO / UpdateMFARequired） | `repo/pkg/adapters/postgres/tenant_auth_store.go` |
| 3.6d | postgres.QuotaChangeRequestStore 实现（Create / ListByTenant / GetByID / UpdateStatus） | `repo/pkg/adapters/postgres/quota_change_request_store.go` |
| 3.7 | service 层完整实现（§6.3 全部业务规则，含 CreateTenant 调用 Core API 逐维度初始化配额 + 同步 INSERT tenant_auth / 读取 plan_quota_limits 有效限额 / UpdatePlan 仅改 plan_id 不动配额 / TenantPlanService.Create 含 quota_limits 创建 / TenantPlanService.Delete 软删除含状态与租户关联校验 / UpdateSSO / TestSSOConnection / UpdateMFARequired） | `repo/services/tenant-service/internal/service/*.go` |
| 3.8 | handler 层完整实现（含错误转换；GET /tenants/{tenantId}/auth/sso；PUT /tenants/{tenantId}/auth/sso；POST /tenants/{tenantId}/auth/sso/test；PUT /tenants/{tenantId}/auth/mfa（tenantId 通过路径参数传递）；GET /tenants/{tenantId}/quota；POST /tenants/{tenantId}/quota-requests（tenantId 通过路径参数传递，old_value 从 Core resource_quota.total 读取）；GET /tenants/{tenantId}/quota-requests（查询申请列表）；POST /tenants/{tenantId}/quota-requests/{reqId}/approve（审批申请）；GET /tenant-plans/{planId}/bindable-tenants（查询可绑定该套餐的租户，排除已绑定该套餐的租户，不分页）；GET /tenant-plans/{planId}/quota-limits（查询套餐限额，JOIN resource_quota_meta 返回 display_name/unit/total）；POST /tenants/{tenantId}/plan（绑定套餐，按 plan_quota_limits 更新各维度 total，同步 tenants.plan_id）；GET /tenants/{tenantId}/lifecycle（分页查询 tenant_lifecycle 表，按 created_at DESC 排序，支持 action 过滤，不调 Core API）；GET /tenants/{tenantId}/audit-logs（分页查询 audit_logs 分区表，按 created_at DESC 排序，支持 action 和 result 过滤，不调 Core API）；GET /tenants/{tenantId}/admins/{userId}/audit-logs（按租户管理员分页查询 audit_logs 表，按 created_at DESC 排序，支持 action 和 result 过滤，不调 Core API）；GET /tenant-plans/{planId}/audit-logs（按套餐分页查询 audit_logs 表，按 created_at DESC 排序，支持 action 和 result 过滤，不调 Core API）；GET /tenant-admins（跨租户分页查询所有管理员，JOIN users + user_roles + tenants，返回租户对象）；GET /tenants/{tenantId}/admins/{userId}/role（查询指定管理员角色与权限，与 PUT role 同路径不同方法）；**不实现 /tenants/quotas 端点**） | `repo/services/tenant-service/handlers/*.go` |
| 3.9 | wiring 完整接线（注入 quotaSvcClient / quotaMetaSvcClient 至 TenantService 构造器） | `repo/services/tenant-service/internal/wiring/wiring.go` |
| 3.10 | BOSS API 客户端（含 updateTenantMFARequired / getTenantSSO / updateTenantSSO / getAllTenantAdmins / getRolePermissions / transferOwnership / getPlatformPermissions / listPlans 等；新增 quota_meta.ts 代理 Core API `/api/v1/admin/quota-meta/*`、tenant_quota.ts 代理 Core API `/api/v1/admin/tenants/{id}/quota/*`、quota_change_request.ts 调用 Services API `/api/v1/svc/tenants/{tenantId}/quota*` 提供 getTenantQuota / createQuotaChangeRequest） | `repo/frontends/boss/src/api/{tenant,tenant_admin,platform_admin,tenant_plan,quota_meta,tenant_quota,quota_change_request}.ts` |
| 3.11 | BOSS 5 页面（含配额元数据页、租户配额页含配额查询列表 + 申请变更弹窗（调用 Services API `/api/v1/svc/tenants/{tenantId}/quota*`）、租户安全页含 MFA 开关 + SSO 配置） | `repo/frontends/boss/src/routes/_authenticated/{tenants/*,settings/platform-admins.tsx}` |
| 3.12 | 单元测试（§9.1） | 同名 _test.go |
| 3.13 | 集成测试（§9.2，含 SSO discovery 校验、MFA enforcement、CreateTenant 调用 Core API 逐维度配额初始化、配额变更申请提交与 409 重复 pending 校验、GET /tenants/{tenantId}/quota 与 Core resource_quota + resource_quota_meta JOIN 一致性） | `repo/services/tenant-service/integration_test.go` |
| 3.14 | 前端测试（§9.3） | 同名 .test.tsx |
| 3.15 | spec 文档（5 份：list / create / quota / admin / platform-admin；另含 plan 合并进 create；含 security 文档含 SSO/MFA；**quota 文档明确标注 BOSS 仅代理 Core API**） | `repo/services/tasks/modules/spec/boss/tenant/spec-boss-tenant-*.md` |
| 3.16 | README | `repo/services/tenant-service/README.md` |
| 3.17 | make test / validate-architecture / 前端 gates 全通过 | — |
| 3.18 | 提交 PR-3 | 标题：`feat(tenant-service): implement tenant management with SQL, adapter, handlers, frontend` |

---

## §11 风险预测

### 11.1 技术风险

| 风险 | 影响 | 概率 | 缓解 |
|------|------|------|------|
| services/v1.yaml 与 Core v1.yaml 前缀冲突 | 高 | 低 | 严格使用 `/api/v1/svc/*` 前缀；lint 校验 |
| RLS 策略误伤平台操作 | 高 | 中 | `current_tenant_id IS NULL` 绕过；集成测试覆盖 |
| bcrypt cost=12 在低配机器登录慢 | 低 | 中 | 仅在创建/重置时；登录仍用现有 cost |
| 最后管理员保护在并发删除下失效 | 高 | 低 | DB 行锁 `SELECT ... FOR UPDATE`；事务内计数 |
| 审计日志事务回滚导致丢失 | 低 | 中 | 审计日志与业务写同一事务；失败回滚一致 |
| OpenAPI 衍生 schema.d.ts 与前端手写类型不一致 | 中 | 中 | PR-1 重新生成后 `pnpm type-check` 校验 |
| Core v1.yaml 后续添加 /tenants* 路径与本设计冲突 | 高 | 低 | 与 Core 团队同步；本设计在 services/v1.yaml 内 |
| tenant-service 与 Console inviteTenantMember 同时改 users 表 | 中 | 中 | 明确职责：Console 仅可 invite，BOSS 可全权管理；RLS 隔离 |
| TDesign React 版本升级破坏样式 | 低 | 中 | 锁定版本；测试覆盖关键页面 |

### 11.2 业务风险

| 风险 | 影响 | 概率 | 缓解 |
|------|------|------|------|
| 误操作禁用导致用户业务完全中断 | 高 | 中 | 禁用仅 platform-admin 可操作；UI 二次确认弹窗 + 强制填写原因 |
| 误删最后一名 platform-admin | 严重 | 低 | 422 拦截 + UI 强提示 + 软删除可恢复 |
| 配额元数据 default_quota 修改后已有租户不自动跟随 | 中 | 高 | 这是 Core QuotaMetaService 的设计：default_quota 只影响新建租户的首条 resource_quota 行，不传播到已有租户；已有租户需通过 /api/v1/admin/tenants/{id}/quota 批量调整 |

### 11.3 安全风险

| 风险 | 缓解 |
|------|------|
| idempotency_key 注入 | 服务端校验 UUID 格式；拒绝非法字符 |
| 密码明文传输泄露 | HTTPS 保护传输；后端立即 bcrypt；不写入日志/审计/响应 |
| 越权访问 | 平台 JWT + 角色检查；RLS 隔离 |
| 审计日志篡改 | 仅 INSERT，无 UPDATE/DELETE；RLS 限制 |
| SQL 注入 | 参数化查询；无字符串拼接 |

---

## §12 验收标准

### 12.1 PR-1 验收

- [ ] `repo/api/openapi/services/v1.yaml` 新增 §5 所有路径与 schema
- [ ] `make openapi-lint` 通过
- [ ] `repo/frontends/boss/src/api/schema.d.ts` 重新生成且类型与 v1.yaml 一致
- [ ] `repo/frontends/console/src/api/schema.d.ts` 同上（若受影响）
- [ ] PR 描述含路径列表、schema 摘要、错误码表
- [ ] 不含任何 .go 实现 / SQL 迁移 / 前端页面

### 12.2 PR-2 验收

- [ ] `repo/pkg/ports/` 含 8 个新接口文件（TenantStore / TenantAdminStore / PlatformAdminStore / TenantPlanStore / TenantLifecycleStore / TenantAuthStore / QuotaChangeRequestStore / AuditLogStore，不含 TenantQuotaStore / TenantUsageCache）
- [ ] `repo/services/tenant-service/` 可独立编译
- [ ] `make test` 通过（含编译 + 现有测试）
- [ ] handler 返回 501 Not Implemented
- [ ] PR 描述含接口签名摘要

### 12.3 PR-3 验收

#### 12.3.1 后端

- [ ] `20260723_015_tenant_management.sql` 迁移成功（tenant_plans 状态 CHECK，**不含九维 default_max_***；**plan_quota_limits 表**（Core 层，外键关联 tenant_plans + resource_quota_meta）；**resource_quota_meta seed 扩展到 8 维度**；**tenant_lifecycle 表**（记录租户状态变更历史）；**tenant_auth 表**（MFA/SSO 配置，1:1 关系）；**tenant_quota_change 表**（§4.1.6，配额变更申请审批流，pending/approved/rejected 状态机）；**不新建 tenant_quotas 表**——租户配额由 Core resource_quota 承载；tenants 含 contact_email + status 旧→新三态迁移（不含 MFA/SSO 字段、不含九维 max_*、不含 slug 列）；**name 全局 UNIQUE 约束 + name 格式 CHECK**；**不新建 billing_records / tenant_usage_records 表**——用量由 Core metering_usage_records 承载；平台角色种子。audit_logs 复用现有分区表不新建；幂等由 Gateway Redis 中间件处理不新增表）
- [ ] tenant-service 启动并可接收请求
- [ ] §9.1 单元测试全部通过（含 CreateTenant 调用 Core API 逐维度初始化配额 + 同步 INSERT tenant_auth / UpdatePlan 仅改 plan_id 不动配额 / UpdateSSO / TestSSOConnection / UpdateMFARequired / QuotaChangeRequestStore.Create 含 old_value 从 Core resource_quota.total 读取 + 重复 pending 409 校验）
- [ ] §9.2 集成测试全部通过（含 SSO discovery 校验、MFA enforcement 模拟、CreateTenant 调用 Core API 逐维度配额初始化、配额变更申请提交与 409 重复 pending 校验、GET /tenants/{tenantId}/quota 与 Core resource_quota + resource_quota_meta JOIN 一致性）
- [ ] `make test` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 无空白错误

#### 12.3.2 前端

- [ ] BOSS 5 页面可访问（含配额元数据、租户配额配置含配额查询列表 + 申请变更弹窗（调用 Services API `/api/v1/svc/tenants/{tenantId}/quota*`）、租户安全页含 MFA 开关 + SSO 配置）
- [ ] `pnpm type-check` 通过
- [ ] `pnpm lint` 通过
- [ ] `pnpm build` 成功
- [ ] §9.3 前端测试通过
- [ ] §9.4 E2E 关键场景通过（含切换套餐、调整配额、配额变更申请提交与 409 重复校验、套餐生命周期、SSO 配置、MFA 切换）

#### 12.3.3 功能验收

- [ ] 创建租户：返回 `{ id, message }`（仅 2 字段，无 temporary_password）；DB 中 status=active，users.password_hash = bcrypt(admin_password, 12)；admin_password 不在响应、日志、审计中明文出现；GET /tenants/{tenantId} 验证完整字段；**配额初始化由 CreateTenant 流程中调用 Core API 逐维度按 resource_quota_meta.default_quota 写入 resource_quota 表（BOSS 不直接写配额表）**
- [ ] 列表/筛选/分页正常；GET /tenants 仅用 limit / cursor / status / search 四个 query 参数，响应为 CursorPage（items + next_cursor）；响应只含活动租户；items[].admin_count 正确反映租户管理员数量；GET /tenants/{tenantId} 验证完整字段
- [ ] 修改租户属性：各属性通过专端点修改（plan_id → POST /tenants/{tenantId}/plan，mfa_required → PUT /tenants/{tenantId}/auth/mfa，display_name / contact_email → PUT /tenants/{tenantId}）；不可改 status / name
- [ ] 冻结/解冻/禁用：均响应 `{ id, message }`；DB 状态机正确；冻结后用户无法登录、实例运行；禁用禁止登录、资源删除（不可恢复）（Core 侧 resource_quota 行保留但资源实例停止运行）；审计日志写入
- [ ] SSO 配置（B1）：PUT /tenants/{tenantId}/auth/sso 响应 `{ id, message }`（tenantId 在路径参数；支持部分更新：仅传 sso_enabled 切换开关，仅传 provider 更新 sso_provider）；GET /tenants/{tenantId}/auth/sso 返回 { sso_enabled, provider, updated_at }（不含 SSO 详细配置，由外部系统承载）；sso_enabled=TRUE 时若 sso_provider 为 NULL 返回 422 TENANT_SSO_CONFIG_INVALID；POST /tenants/{tenantId}/auth/sso/test（tenantId 在路径参数）根据 sso_provider 从外部系统加载详细配置，返回 { success, discovery_result, error, tested_at }
- [ ] MFA 强制开关（B2）：PUT /tenants/{tenantId}/auth/mfa 响应 `{ id, message }`（tenantId 在路径参数）；mfa_required=TRUE 时模拟登录 → 403 MFA_REQUIRED
- [ ] 跨租户查询所有管理员：GET /tenant-admins 分页返回 items[]{id, email, username, display_name, role, status, is_inviting, source, last_login_at, tenant{id, name, display_name, mfa_required}}；**仅返回 role ∈ (tenant-owner, tenant-admin) 或正在被邀请（is_inviting=true）的用户，不含普通成员 user**；支持 tenant_id / role / status / is_inviting / search 过滤；返回租户对象；设置了该租户下 `status='inviting'` 邀请的用户也要返回且 `is_inviting=true`（仅作标记，不改变 role/status，仍展示原有角色）
- [ ] 查询管理员角色与权限：GET /tenants/{tenantId}/admins/{userId}/role（与 PUT role 同路径不同方法）返回 { user_id, tenant_id, role, permissions{compute,inference,member,transfer} }
- [ ] 查询运营账号权限：GET /platform-admins/{userId}/permissions 返回 { user_id, role, permissions{tenant_ops,resource_pool,platform_user,audit_export} }
- [ ] 移交所有者：POST /tenants/{tenantId}/transfer-ownership 响应 { id, message }；目标非 tenant-admin 422 TRANSFER_TARGET_INVALID；非 owner 发起 403
- [ ] 租户管理员列表：GET /tenants/{tenantId}/admins 分页返回用户列表，支持 role/status/search 过滤
- [ ] 平台账号列表：GET /platform-admins 分页返回 items[]{id,username,display_name,role,status,source,last_login_at}
- [ ] 平台账号详情：GET /platform-admins/{userId} 返回完整对象（含 email/created_at）
- [ ] 修改权限：PUT /tenants/{tenantId}/admins/{userId}/role 响应 { id, message }；可设为 user / auditor / tenant-admin（不可设 tenant-owner）；tenant-owner 不可被修改 → 409 TENANT_OWNER_ROLE_LOCKED；非法 role → 422 ROLE_CHANGE_INVALID；禁用/启用/删除均响应 { id, message }
- [ ] 重置密码：响应 `{ id, message }`；新密码由调用方提供，必须与旧密码不同（422 PASSWORD_SAME_AS_OLD）
- [ ] 平台账号：创建/改角色/重置密码/禁用/启用/删除均响应 `{ id, message }`
- [ ] 最后管理员保护：删除/禁用活跃数 ≤ 1 时 422
- [ ] 配额元数据管理（代理 Core API）：`/boss/tenants/quota-meta` 页面调用 Core `/api/v1/admin/quota-meta/*` 实现列表/新建/编辑/禁用；新建维度后新建租户自动按 default_quota 初始化；已有租户不受影响
- [ ] 租户配额配置（代理 Core API）：`/boss/tenants/{id}/quota` 页面调用 Core `/api/v1/admin/tenants/{id}/quota/*` 实现 list/update total/usage 查询；用量进度条与 Core resource_quota.reserved/used 一致
- [ ] 用量：按日查询 GPU/CPU/内存/存储/Token/KB 查询/成员数/推理服务数八维（数据来源 Core `GET /api/v1/admin/tenants/{id}/quota/usage`）
- [ ] 配额查询：GET /tenants/{tenantId}/quota 返回 items[]{resource_type, display_name, used, total, unit}，数据与 Core resource_quota + resource_quota_meta JOIN 一致
- [ ] 配额变更申请：POST /tenants/{tenantId}/quota-requests 响应 { id, message }；重复 pending 申请 409 QUOTA_CHANGE_REQUEST_DUPLICATE；resource_type 不存在 422 QUOTA_CHANGE_REQUEST_INVALID；old_value 从 Core resource_quota.total 读取
- [ ] 查询可绑定租户：GET /tenant-plans/{planId}/bindable-tenants 返回 items[]{id, name, display_name, status}，不分页，排除已绑定该套餐的租户
- [ ] 绑定套餐：POST /tenants/{tenantId}/plan 响应 { id, message }；plan_id 不存在 404 TENANT_PLAN_NOT_FOUND；套餐状态非 active 422 PLAN_NOT_ACTIVE；存在 approved 变更申请的维度保留不覆盖，其余维度（含 pending）total 按 plan_quota_limits.total 或 resource_quota_meta.default_quota 更新；tenants.plan_id 同步更新
- [ ] 生命周期查询：GET /tenants/{tenantId}/lifecycle 分页返回 items[]{id, action, reason, user_id, created_at}，按 created_at DESC 排序；支持 action 过滤
- [ ] 操作历史查询：GET /tenants/{tenantId}/audit-logs 分页返回 items[]{id, action, resource, result, user_id, created_at}，按 created_at DESC 排序；支持 action 和 result 过滤
- [ ] 管理员操作历史：GET /tenants/{tenantId}/admins/{userId}/audit-logs 分页返回 items[]{id, action, resource, result, user_id, created_at}，按 created_at DESC 排序；支持 action 和 result 过滤
- [ ] 套餐操作历史：GET /tenant-plans/{planId}/audit-logs 游标分页返回 items[]{id, action, result, details, created_at}（5 个字段）+ next_cursor，按 created_at DESC 排序；不支持 action/result 过滤（前端 result 为本地过滤）
- [ ] 套餐：POST 创建响应 `{ id, message }`；GET 返回完整套餐对象；POST activate/disable 响应 `{ id, message }`；draft/active/disabled 状态机（不含九维 default_max_*）
- [ ] 删除套餐：DELETE /tenant-plans/{planId} 响应 { id, message }；有租户关联 409 TENANT_PLAN_IN_USE；任意状态（draft/active/disabled）均可删除
- [ ] 查询套餐限额：GET /tenant-plans/{planId}/quota-limits 返回 items[]{resource_type, display_name, unit, total}；total 为 NULL 表示使用 default_quota
- [ ] 幂等：同 key 同 body 回放（Idempotent-Replay: true）；同 key 不同 body 409
- [ ] 角色鉴权：platform-readonly 写操作 403；非平台账号 403

#### 12.3.4 文档

- [ ] `repo/services/tasks/modules/spec/boss/tenant/spec-boss-tenant-list.md`
- [ ] `spec-boss-tenant-create.md`
- [ ] `spec-boss-tenant-quota.md`
- [ ] `spec-boss-tenant-admin.md`
- [ ] `spec-boss-platform-admin.md`
- [ ] `repo/services/tenant-service/README.md`
- [ ] PR 描述含功能截图、E2E 视频、性能数据

### 12.4 整体退出标准

- [ ] 所有 3 个 PR 合并至主分支
- [ ] 生产环境迁移成功
- [ ] 监控仪表盘含 tenant-service 关键指标（QPS、错误率、延迟）
- [ ] 文档归档至 `repo/services/tasks/modules/spec/boss/tenant/`
- [ ] 后续 PR 计划（业务配额、计费）已起草

---

### 12.1 当前阶段暂不完成的内容

以下功能在当前阶段**不实现**，留待后续 PR 补充：

| 模块 | 不完成内容 | 说明 |
|------|-----------|------|
| **租户计费** | 充值、扣费、账单、欠费自动冻结、充值解冻、周期扣费 | `BillingRecordStore` / `TenantBillingService.Recharge` / `Charge` 等端口和 service 暂不实现；租户计费页面（§7.3.5）暂不实现；计费相关错误码（`BILLING_*`）暂不定义 |
| **租户区域** | 租户区域分配、区域切换、区域感知路由 | 租户与部署区域的绑定关系、区域级配额隔离、跨区域迁移等功能不在本设计范围 |
| **平台账号邀请用户** | 平台运营账号通过邮件邀请用户注册 | 平台账号（platform-admin / platform-ops / platform-readonly）的创建仅通过 `POST /platform-admins` 由 platform-admin 直接创建，不支持邮件邀请流程 |

> 以上功能的端口接口、数据表、API 端点均不在本阶段定义；待后续 PR 立项时补充设计文档与实现。

---

## 附录 A：与 CLAUDE.md 对齐检查

| CLAUDE.md 规则 | 本设计遵守 |
|----------------|-----------|
| API 优先：先改契约再写实现 | §2.3 PR-1 仅契约，PR-2 接口，PR-3 实现 |
| 不提交密码 / token / 私钥 | §6.3.1 密码由调用方提供，bcrypt 入库，不存明文、不返回 |
| 不自造 /api/v1/boss/tenants | §5 全部 /api/v1/svc/* 前缀 |
| 不伪造未声明 API 为已冻结 | §5 所有路径在 services/v1.yaml 显式声明 |
| 平台运营账号 ≠ 租户管理员 | §4.1.4 / §4.1.5 严格区分 |
| 不自动 git commit / push / PR | 设计文档，提交由用户确认 |
| 复用现有幂等 | §4.6 显式复用 Gateway Redis `Idempotency` 中间件（`idempotency:` key, TTL 24h）；不新建 `idempotency_keys` 表 |
| bcrypt cost | §6.3.1 cost=12 |
| idempotency_key 格式 | §5.1 UUID 格式 |
| 套餐状态 draft/active/disabled | §4.2.1 CHECK 约束 + §5.3 状态机 |
| 配额由 Core 承载 | **配额不在本服务存放**——配额元数据 / 配额配置均通过代理 Core API `/api/v1/admin/quota-meta/*` 与 `/api/v1/admin/tenants/{id}/quota/*` 实现 |

> 计费相关特性（欠费自动冻结、充值解冻、周期扣费）暂不实现，后续 PR 再补充。

## 附录 B：与 ANI-02 产品功能对齐

| ANI-02 章节 | 本设计覆盖 |
|-------------|-----------|
| BOSS 租户管理 | §3.2 / §5.2 / §7.3.1 |
| BOSS 管理员管理 | §5.4 / §7.3.4 |
| BOSS 平台账号管理 | §5.5 / §7.3.6 |
| 资源配额 | §4.3.1-4.3.2 / §5.3 / §7.3.2 |
| 套餐模板管理 | §4.2.1 / §5.3 / §7.3.3c |

## 附录 C：与原型对齐

| 原型文件 | 本设计对应章节 |
|----------|---------------|
| `boss-tenant-闭环.html` 闭环 | §5 / §7.3 全覆盖（含套餐管理） |
| `boss-platform-admins-提案.md` 选项 C | §4.1.5 / §5.5 / §7.3.6 |
| `index.html` BOSS 菜单 | §7.2 路由结构 |
| `market-detail.html` §7B | §5.2 / §7.3.1 |

---

**END OF plan.md**
