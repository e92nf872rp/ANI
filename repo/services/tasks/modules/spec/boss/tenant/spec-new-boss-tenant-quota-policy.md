# SPEC: 配额套餐管理 (new)

> Technical specification derived from:
> - PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
> - UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
> - Plan: [租户管理plan v3.0](../../plan/tenant/租户管理plan%20v3.0.md) §4.2 / §5.3 / §6.3
> - Core API: [配额core层对接api设计.md](../../../../../配额core层对接api设计.md)
> Generated: 2026-08-04 | Updated: 2026-08-14（对齐实现代码） | Product: BOSS | Code scope: `repo/frontends/boss/src/` + `repo/services/tenant-service/`

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 定义套餐模板全生命周期的技术实现：套餐 CRUD + draft/active/disabled 状态机、套餐限额查询与修改（同步存量租户）、绑定套餐更新配额、套餐绑定租户查询、操作历史。后端为新建 `tenant-service`，前端为 `repo/frontends/boss/src/` 新增页面。配额元数据与配额存储由 Core 提供，本模块通过 Core API 代理调用。

### 1.2 PRD Reference

- Source: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX source: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- User Stories covered: US-001 ~ US-018
- Functional Requirements covered: FR-1 ~ FR-9

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 新建 tenant-service | 独立微服务 | 租户管理域独立于 Core，配额代理调 Core API |
| API 契约位置 | `repo/api/openapi/services/v1.yaml` | Services 层新增路径，Core v1.yaml 不修改 |
| 配额限额存储 | `plan_quota_limits` 表（tenant-service 迁移） | 跨层外键引用 resource_quota_meta |
| total=null 写入 | service `mapAndValidateQuotaLimits` 用 Core default_quota **物化落库** | 不保留 NULL；历史 NULL 仅由 GET `buildQuotaLimitViews` 回写 |
| code 唯一约束 | partial unique index WHERE is_deleted=FALSE | 软删除后 code 可复用 |
| 配额自动收紧 | Core API 返回 `tightened` 字段（正常 200 响应），不报错 | total < used+reserved 时 Core 自动收紧为 used+reserved，响应含 tightened=true |
| 修改限额同步存量租户 | 逐租户 GetQuota→CreateQuota/PutQuota 分流 | 先查询配额行是否存在，不存在→POST 新建，已存在→PUT 修改 |
| 限额查询元数据 | Core `ListQuotaMeta`（禁止 store JOIN meta） | Services 不直连 Core 表 |
| 前端修改限额交互 | 直接编辑 InputNumber + 底部修改按钮批量提交 | 简化交互，无需行级编辑态切换 |
| 前端路由 | `/tenants/quotas` + `/new` + `/$planId` | 独立创建/详情页 |

---

## 2. Architecture

### 2.1 System Context

```
┌─────────────────┐     ┌──────────────┐     ┌──────────────────┐     ┌─────────────┐
│  BOSS Frontend  │────▶│  ani-gateway  │────▶│  tenant-service  │────▶│    Core     │
│  (boss/src/)    │     │  (网关)       │     │  (新建微服务)     │     │  (已有)     │
│                 │     │  路由分发     │     │                  │     │             │
│ 套餐列表/详情   │     │  鉴权/限流   │     │ 套餐 CRUD        │     │ resource_   │
│ 创建/修改/绑定  │     │  幂等性      │     │ 限额管理         │     │  quota_meta │
│                 │     │              │     │ 配额代理(调Core) │     │ resource_   │
│                 │     │              │     │ 审计日志         │     │  quota      │
└─────────────────┘     └──────────────┘     └──────┬───────────┘     └─────────────┘
                                                  │
                                           ┌──────┴───────┐
                                           │  PostgreSQL  │
                                           │              │
                                           │ tenant_plans │
                                           │ plan_quota_  │
                                           │   limits     │
                                           │ audit_logs   │
                                           │ tenants      │
                                           └──────────────┘
```

### 2.2 Component Design

| Component | Responsibility | Location |
|-----------|---------------|----------|
| TenantPlansAPI（网关） | HTTP 入站转发：请求解析 + 校验 + 调 gRPC + 响应组装 + 错误码映射（对外仍 REST，内部走 gRPC） | `repo/services/ani-gateway/internal/router/tenant_plans.go` |
| TenantPlanService（gRPC server） | 套餐业务逻辑：CRUD、状态机、限额同步（gRPC server，仿 model-service） | `repo/services/tenant-service/internal/service/tenant_plan_service.go` |
| TenantService（gRPC server） | 绑定套餐 BindPlanQuota（gRPC） | `repo/services/tenant-service/internal/service/tenant_service.go` |
| TenantPlanStore | 数据持久化接口（tenant-service 自有 repo ports） | `repo/services/tenant-service/internal/repo/ports/tenant_plan_store.go` |
| PostgresTenantPlanStore | PostgreSQL 适配器 | `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go` |
| TenantStore | 租户数据最小访问接口（端口）— 判租户状态/换 plan_id | `repo/services/tenant-service/internal/repo/ports/tenant_store.go` |
| PostgresTenantStore | 租户 PostgreSQL 适配器 | `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go` |
| QuotaSvcClient | Core 配额 API 调用客户端 | `repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go` |
| TenantPlanAuditStore | 审计日志写入【配额套餐域】（复用现有 audit_logs 分区表） | `repo/services/tenant-service/internal/repo/ports/tenant_plan_audit_store.go` |
| PostgresTenantPlanAuditStore | 配额套餐域审计 PostgreSQL 适配器（已实现：写/按 plan_id 分页查 `audit_logs`） | `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go` |
| TenantPlanAPI | 前端 API 封装 | `repo/frontends/boss/src/api/tenant-plans.ts` |
| TenantPlansPage | 前端列表页 | `repo/frontends/boss/src/routes/_authenticated/tenants/quotas/index.tsx` |
| PlanDetailPage | 前端详情页组件（独立整页，非 Drawer） | `repo/frontends/boss/src/components/tenant-plans/PlanDetailPage.tsx` |
| CreatePlanWizard | 前端创建 Wizard 组件（3 步向导，非 Dialog） | `repo/frontends/boss/src/components/tenant-plans/CreatePlanWizard.tsx` |
| EditPlanInfoDialog | 前端修改套餐基本信息弹窗 | `repo/frontends/boss/src/components/tenant-plans/EditPlanInfoDialog.tsx` |
| QuotaLimitsTab | 前端限额 Tab 组件 | `repo/frontends/boss/src/components/tenant-plans/QuotaLimitsTab.tsx` |
| BoundTenantsTab | 前端绑定租户 Tab 组件（内联 Select 绑定，无独立 Dialog） | `repo/frontends/boss/src/components/tenant-plans/BoundTenantsTab.tsx` |
| AuditLogsTab | 前端操作历史 Tab 组件 | `repo/frontends/boss/src/components/tenant-plans/AuditLogsTab.tsx` |
| PlanTable | 前端列表表格组件 | `repo/frontends/boss/src/components/tenant-plans/PlanTable.tsx` |
| planStatus | 状态标签 + 时间格式化辅助 | `repo/frontends/boss/src/components/tenant-plans/planStatus.tsx` |
| quotaResourceOrder | 配额维度展示顺序排序 | `repo/frontends/boss/src/components/tenant-plans/quotaResourceOrder.ts` |

### 2.3 Module Interactions

```
创建套餐流程:
  Frontend → ani-gateway → POST /api/v1/svc/tenant-plans
    → TenantPlansAPI(网关) → (gRPC) TenantPlanService.CreateTenantPlan
      → TenantPlanStore.Create (INSERT tenant_plans + plan_quota_limits)
      → TenantPlanAuditStore.Create (写审计日志)
    → Response { id, message }

修改限额同步流程:
  Frontend → ani-gateway → PUT /api/v1/svc/tenant-plans/{planId}/quota-limits
    → TenantPlansAPI(网关) → (gRPC) TenantPlanService.UpdateTenantPlanQuotaLimits
      → TenantPlanStore.UpdateQuotaLimits (UPDATE plan_quota_limits)
      → TenantPlanStore.ListBoundTenants (查 tenants WHERE plan_id=planId)
      → FOR EACH tenant:
          → TenantPlanStore.GetApprovedQuotaChanges (查 approved 维度)
          → QuotaSvcClient.GetQuota (先查询判断配额行是否存在)
          → QuotaSvcClient.CreateQuota/PutQuota (不存在→POST 新建；已存在→PUT 修改 total)
      → TenantPlanAuditStore.Create
    → Response { id, message }

绑定套餐流程:
  Frontend → ani-gateway → POST /api/v1/svc/tenants/{tenantId}/plan
    → TenantPlansAPI(网关) → (gRPC) TenantService.BindPlanQuota
      → service.buildQuotaLimitViews    // COALESCE total, default_quota 兜底为具体 total（内部调 GetQuotaLimits + Core ListQuotaMeta）
      → TenantStore.UpdatePlan (先更新 tenants.plan_id，若与当前不同)
      → TenantPlanStore.GetApprovedQuotaChanges (查 approved 维度，跳过不覆盖)
      → QuotaSvcClient.GetQuota (先查询判断配额行是否存在)
      → QuotaSvcClient.CreateQuota/PutQuota (不存在→POST 新建 used/reserved=0；已存在→PUT 修改 total 自动收紧)
      → Core 失败时 best-effort 回滚 plan_id（回滚失败也返回错误）
      → TenantPlanAuditStore.Create
    → Response { id, message }
```

### 2.4 File Structure

```
repo/services/tenant-service/
├── internal/
│   ├── service/
│   │   ├── tenant_plan_service.go        [NEW — gRPC TenantPlanService server]
│   │   └── tenant_service.go            [NEW — gRPC TenantService server (BindPlanQuota)]
│   └── repo/
│       ├── ports/
│       │   ├── tenant_plan_store.go     [NEW — TenantPlanStore 接口与领域模型]
│       │   ├── tenant_store.go          [NEW — 最小 TenantStore：GetByID/UpdatePlan]
│       │   ├── tenant_plan_audit_store.go [NEW — TenantPlanAuditStore 接口（配额套餐域）与 AuditLog 模型]
│       │   └── core_quota.go            [NEW — QuotaSvcClient 接口与 CoreQuotaItem/Result]
│       └── adapters/
│           ├── postgres/
│           │   ├── tenant_plan_store.go         [NEW — PostgresTenantPlanStore]
│           │   ├── tenant_plan_audit_store.go   [NEW — PostgresTenantPlanAuditStore（配额套餐域审计，已实现）]
│           │   └── tenant_store.go              [NEW — PostgresTenantStore]
│           └── core/
│               └── quota_svc_client.go  [NEW — QuotaSvcClient 实现]
├── go.mod                                [NEW — module + grpc/protobuf + replace pkg]
└── Dockerfile                            [NEW]

api/proto/tenant/v1/
└── tenant_plan.proto                     [NEW — gRPC 接口与数据模型]

pkg/generated/pb/tenant/v1/
├── tenant_plan.pb.go                     [NEW — buf 生成]
└── tenant_plan_grpc.pb.go                [NEW — buf 生成]

repo/services/ani-gateway/
└── internal/router/
    └── tenant_plans.go                  [NEW — HTTP 转发 + 错误映射；内部直接创建并持有 tenant gRPC client]

repo/api/openapi/services/
└── v1.yaml                              [MODIFY — 新增 tenant-plans 路径]

repo/frontends/boss/src/
├── api/
│   └── tenant-plans.ts                  [NEW]
├── routes/_authenticated/tenants/
│   └── quotas/index.tsx                 [NEW]
└── components/tenant-plans/
    ├── PlanTable.tsx                     [NEW]
    ├── CreatePlanWizard.tsx              [NEW — 3 步向导]
    ├── PlanDetailPage.tsx                [NEW — 独立整页，非 Drawer]
    ├── EditPlanInfoDialog.tsx            [NEW — 修改 name/description 弹窗]
    ├── QuotaLimitsTab.tsx                [NEW]
    ├── BoundTenantsTab.tsx               [NEW — 内联 Select 绑定，无独立 Dialog]
    ├── AuditLogsTab.tsx                  [NEW]
    ├── planStatus.tsx                    [NEW — 状态标签 + 时间格式化]
    └── quotaResourceOrder.ts             [NEW — 配额维度展示排序]
```

---

## 3. Data Model

### 3.1 Schema Changes

#### 3.1.1 tenant_plans 表

```sql
-- deploy/migrations/20260810000200_tenant_plan_management.sql
CREATE TABLE tenant_plans (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code            TEXT        NOT NULL,
  name            TEXT        NOT NULL,
  description     TEXT,
  status          TEXT        NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'active', 'disabled')),
  is_deleted      BOOLEAN     NOT NULL DEFAULT FALSE,
  deleted_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- code 唯一约束仅对未删除的套餐生效
CREATE UNIQUE INDEX idx_tenant_plans_code_active
  ON tenant_plans(code) WHERE is_deleted = FALSE;
```

#### 3.1.2 plan_quota_limits 表

```sql
-- 同迁移文件 20260810000200_tenant_plan_management.sql
CREATE TABLE plan_quota_limits (
    plan_id        UUID   NOT NULL REFERENCES tenant_plans(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT,          -- Create/PUT 写入路径物化为具体值；历史行可能仍为 NULL（GET 用 default_quota 回写）
    CHECK (total IS NULL OR total >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, resource_type)
);
-- 注：plan_id 为复合主键前导列，无需额外创建 idx_plan_quota_limits_plan 索引
```

#### 3.1.3 resource_quota_meta — 配额元数据注册表

```sql
-- deploy/migrations/20260810000100_resource_quota.sql
CREATE TABLE resource_quota_meta (
    resource_type     TEXT PRIMARY KEY,
    display_name      TEXT NOT NULL,
    unit              TEXT NOT NULL,
    is_discrete       BOOLEAN NOT NULL DEFAULT TRUE,
    default_quota     BIGINT NOT NULL,
    collector_id      TEXT,
    description       TEXT,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 初始 seed（8 维度）
INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, collector_id, description) VALUES
  ('gpu_count',              'GPU 份数',     '份', true,  8,        'prometheus_dcgm',    '单租户可持有的 GPU 份数上限'),
  ('cpu_core',               'CPU 核数',     '核',  true,  8,       'prometheus_kubelet', '单租户可占用的 CPU 核数上限'),
  ('memory_gb',              '内存 GB',      'gb',  true,  32,      'prometheus_kubelet', '单租户可占用的内存 GB 上限'),
  ('storage_gb',             '存储 GB',      'gb',  true,  64,     NULL,                  '单租户可占用的存储 GB 上限'),
  ('token_count',            'Token 数',     'token', true, 1000000, 'inference_token',  '单租户可消耗的 Token 总量上限'),
  ('kb_query_count',         'KB 查询次数',  '次', true, 10000,    NULL,                  '单租户知识库查询次数上限'),
  ('member_count',           '成员上限',     '人', true,  20,       NULL,                  '单租户可邀请的成员数量上限'),
  ('inference_service_count','推理服务上限', '个', true,  10,      NULL,                  '单租户可创建的推理服务数量上限')
ON CONFLICT (resource_type) DO NOTHING;
```

> 不加 RLS（平台治理数据，跨租户可见，写权限由应用层 RBAC 限制为 platform-admin）。维度非固定，后续可通过 QuotaMetaService.Register 动态增减。

#### 3.1.4 resource_quota — 配额配置 + 运行时账本

```sql
-- 同迁移文件 20260810000100_resource_quota.sql
CREATE TABLE resource_quota (
    tenant_id      UUID   NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT NOT NULL DEFAULT 0,
    reserved       BIGINT NOT NULL DEFAULT 0,
    used           BIGINT NOT NULL DEFAULT 0,
    CHECK (total >= 0 AND reserved >= 0 AND used >= 0),
    CHECK (reserved + used <= total),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);

ALTER TABLE resource_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_quota FORCE ROW LEVEL SECURITY;
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

> total（上限）与 reserved/used（占用）在同一行，避免配置与占用不同步。RLS 保证租户只能看自己的配额行，平台管理员绕过 RLS 查所有。

#### 3.1.5 resource_reservations — TCC 配额流水

```sql
-- 同迁移文件 20260810000100_resource_quota.sql
CREATE TABLE resource_reservations (
    tx_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL REFERENCES resource_quota_meta(resource_type),
    amount        BIGINT NOT NULL CHECK (amount > 0),
    state         TEXT NOT NULL DEFAULT 'reserved'
        CHECK (state IN ('reserved','confirmed','cancelled','expired','released')),
    resource_ref  TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_res_state_expires
    ON resource_reservations(state, expires_at) WHERE state = 'reserved';
CREATE INDEX idx_res_tenant
    ON resource_reservations(tenant_id, state);

ALTER TABLE resource_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY resource_reservations_platform_bypass
  ON resource_reservations FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY resource_reservations_self
  ON resource_reservations FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

> 幂等守卫 + 跨事务关联 + TTL 回收依据。state: reserved→confirmed/cancelled/expired/released。

#### 3.1.6 tenants 表扩展

> **实现补充说明：** 原 SPEC 直接 `ADD COLUMN ... NOT NULL`，实际因存量数据不为空，改为分 4 步实施（3a 加列可空→3b 插入 starter 套餐→3c 回填存量→3d 收紧 NOT NULL）。

```sql
-- 3a) 加可空列（NOT NULL 需在回填完成后收紧）
ALTER TABLE tenants
  ADD COLUMN IF NOT EXISTS plan_id UUID
    REFERENCES tenant_plans(id) ON DELETE RESTRICT;

-- 3b) 固定 UUID 的入门套餐 starter（幂等：冲突则跳过）
INSERT INTO tenant_plans (id, code, name, description, status, is_deleted, deleted_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'starter', '入门版', ...)
ON CONFLICT (id) DO NOTHING;
-- + starter 套餐 8 维度默认限额 INSERT

-- 3c) 存量租户回填到 starter
UPDATE tenants SET plan_id = '00000000-0000-0000-0000-000000000001' WHERE plan_id IS NULL;

-- 3d) 收紧为 NOT NULL
ALTER TABLE tenants ALTER COLUMN plan_id SET NOT NULL;
```

#### 3.1.7 应用用户权限分配

配额基础表读写权限按「租户管理 plan v3.0 §4.3.4」授权给普通应用用户 `ani_app_user`（应用逻辑需要读写这些表）：

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_reservations TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota_meta TO ani_app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ani_app_user;
```

### 3.2 Entity Definitions

```go
// repo/services/tenant-service/internal/repo/ports/tenant_plan_store.go

type TenantPlan struct {
    ID          uuid.UUID
    Code        string
    Name        string
    Description string
    Status      string // draft | active | disabled
    TenantCount int64  // 绑定租户数量（COUNT tenants WHERE plan_id=? AND status != 'disabled'）；详情和列表均填充
    IsDeleted   bool
    DeletedAt   *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// PlanQuotaLimit：套餐各维度限额原始行（保留 NULL 原始语义，不做 COALESCE 兜底）。
// 定位：预留供其余业务需要（如需要区分"未显式设置/用默认值"的场景）；
// 当前展示与绑定下发均用 service 层 buildQuotaLimitViews（COALESCE 兜底为具体值）。
type PlanQuotaLimit struct {
    PlanID       uuid.UUID
    ResourceType string
    Total        *int64 // nil = 用 default_quota（保留原始语义）
}

// PlanQuotaLimitView：套餐各维度限额展示值（service 层 buildQuotaLimitViews 组装：store.GetQuotaLimits 原始行 + Core ListQuotaMeta，COALESCE 兜底为具体 total）。
// 展示（GET /quota-limits）与绑定下发均用它；不保留 NULL 原始语义（丢弃空语义）。
type PlanQuotaLimitView struct {
    ResourceType    string
    DisplayName    string // 来自 resource_quota_meta
    Unit           string // 来自 resource_quota_meta
    Total          int64  // 兜底后的展示值 COALESCE(total, default_quota)；丢弃空语义（GET /quota-limits 返回；绑定下发亦取该值）
}

type CreateTenantPlanInput struct {
    Code         string
    Name         string
    Description  string
    QuotaLimits  []PlanQuotaLimitInput
}

type PlanQuotaLimitInput struct {
    ResourceType string
    Total       *int64 // nil = 用默认值
}

type TenantPlanListFilter struct {
    Limit   int    // 每页数量，default 20，max 100
    Cursor  string // 上一页返回的 next_cursor；空串 = 第一页
    Status  string // "" = 全部
    Search  string // 模糊匹配 name
}

// 分页结果：具体类型（不用泛型，对齐项目 ports 风格）。
// Items + Total（总数，用于前端展示）+ NextCursor（游标，"" = 已无更多数据）。
type TenantPlanListResult struct {
    Items      []TenantPlanListItem
    Total      int
    NextCursor string // "" = 已无更多数据
}

type AuditLogListResult struct {
    Items      []AuditLog
    Total      int
    NextCursor string // "" = 已无更多数据
}

// TenantPlanListItem：套餐列表的查询视图。
// 与 TenantPlan 实体平铺字段一致（含 TenantCount），不含 IsDeleted/DeletedAt。
// 实现中详情和列表复用同一 TenantPlan 实体（含 TenantCount），TenantPlanListItem 仅为列表语义别名。
type TenantPlanListItem struct {
    ID          uuid.UUID
    Code        string
    Name        string
    Description string
    Status      string // draft | active | disabled
    TenantCount int64  // 绑定租户数量（COUNT tenants WHERE plan_id=? AND status != 'disabled'）
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// BoundTenant：绑定到某套餐的租户摘要（GET /tenant-plans/{planId}/tenants 返回，不分页）。
type BoundTenant struct {
    ID          uuid.UUID
    Name        string
    DisplayName string
    Status      string // active | frozen | disabled
}

// ApprovedQuotaChange：租户已审批通过（status='approved'）的配额变更维度。
// 用于绑定套餐 / 修改限额同步时：这些维度保留不覆盖。
type ApprovedQuotaChange struct {
    TenantID     uuid.UUID
    ResourceType string // 已审批通过的配额维度
}

// AuditLog：审计日志记录（对应 audit_logs 表一行；复用现有分区表）。
// 平台级操作 TenantID 为 NULL；套餐相关记录 resource='tenant_plan'，以 details.plan_id 关联。
// 注意：API 响应（GET /audit-logs）仅暴露 5 个字段：ID/Action/Result/Details/CreatedAt；
//       TenantID/UserID/RequestID/Resource/IPAddress/UserAgent 仅在 DB 存储，不在 API 返回。
type AuditLog struct {
    ID        uuid.UUID
    TenantID  *uuid.UUID  // 平台级操作（套餐管理）为 NULL
    UserID    *uuid.UUID  // 操作者；系统/后台触发可为 NULL
    RequestID    string      // 关联请求（网关透传的 x-request-id，非 UUID 格式）
    Action    string      // 如 tenant_plan.create / tenant_plan.activate / tenant.bind_plan_quota
    Resource  string      // 如 tenant_plan
    Result    string      // success | failure
    Details   map[string]any // 扩展信息，如 {plan_id, skipped_approved, updated}
    IPAddress string
    UserAgent string
    CreatedAt time.Time
}
```

### 3.3 Relationships

```
resource_quota_meta (1) ──< plan_quota_limits (N) ──> tenant_plans (1)
        │                                                          │
        │                                                          └──< tenants (N) [plan_id FK, ON DELETE RESTRICT]
        │                                                                        │
        └──< resource_quota (N) [tenant_id + resource_type FK]                   │
        │                                                                        │
        └──< resource_reservations (N) [tenant_id + resource_type FK] ──────────┘

audit_logs (N)  [details->>'plan_id' 关联，非外键]
```

### 3.4 Migration Plan

分为两个迁移文件，按依赖顺序执行：

**文件 1：`20260810000100_resource_quota.sql` — 配额基础表（先执行）**

| Step | Description | Rollback |
|------|-------------|----------|
| 1 | CREATE TABLE resource_quota_meta + seed 8 维度 | DROP TABLE resource_quota_meta |
| 2 | CREATE TABLE resource_quota + RLS policies | DROP TABLE resource_quota |
| 3 | CREATE TABLE resource_reservations + indexes + RLS | DROP TABLE resource_reservations |

**文件 2：`20260810000200_tenant_plan_management.sql` — 配额套餐表（后执行，依赖文件 1）**

| Step | Description | Rollback |
|------|-------------|----------|
| 4 | CREATE TABLE tenant_plans | DROP TABLE tenant_plans |
| 5 | CREATE UNIQUE INDEX idx_tenant_plans_code_active | DROP INDEX |
| 6 | CREATE TABLE plan_quota_limits (FK → tenant_plans + resource_quota_meta) | DROP TABLE plan_quota_limits |
| 7 | ALTER TABLE tenants ADD COLUMN plan_id（分 4 步：加列可空→插入 starter→回填存量→收紧 NOT NULL） | ALTER TABLE tenants DROP COLUMN plan_id |

> **迁移顺序**：文件 1 必须先于文件 2 执行，因 plan_quota_limits 外键引用 resource_quota_meta。两文件文件名为 `20260810000100` / `20260810000200`，按文件名排序天然满足该依赖。文件 1 中的 resource_quota / resource_reservations 也依赖 tenants 表和 resource_quota_meta 已存在。各文件内事务执行。

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | Description | AuthZ | idempotency_key (body) |
|--------|------|-------------|-------|------------------------|
| POST | `/api/v1/svc/tenant-plans` | 创建套餐 | admin/ops | required |
| GET | `/api/v1/svc/tenant-plans` | 列表 | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenant-plans/{planId}` | 详情 | admin/ops/readonly | — |
| PUT | `/api/v1/svc/tenant-plans/{planId}` | 更新基本信息 | admin/ops | optional |
| GET | `/api/v1/svc/tenant-plans/{planId}/quota-limits` | 查询限额 | admin/ops/readonly | — |
| POST | `/api/v1/svc/tenant-plans/{planId}/activate` | 发布 | admin/ops | required |
| POST | `/api/v1/svc/tenant-plans/{planId}/disable` | 禁用 | admin/ops | required |
| DELETE | `/api/v1/svc/tenant-plans/{planId}` | 删除 | admin/ops | — |
| PUT | `/api/v1/svc/tenant-plans/{planId}/quota-limits` | 修改限额 | admin/ops | required |
| POST | `/api/v1/svc/tenants/{tenantId}/plan` | 绑定套餐 | admin/ops | required |
| GET | `/api/v1/svc/tenant-plans/{planId}/tenants` | 绑定租户列表 | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenant-plans/{planId}/bindable-tenants` | 可绑定租户列表 | admin/ops/readonly | — |
| GET | `/api/v1/svc/tenant-plans/{planId}/audit-logs` | 操作历史 | admin/ops/readonly | — |
| GET | `/api/v1/svc/quota-meta` | 查询配额元数据 | admin/ops/readonly | — |

### 4.2 Request/Response Schemas

#### POST /tenant-plans — 创建

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "code": "pro",
  "name": "专业版",
  "description": "适用于中小团队",
  "quota_limits": [
    { "resource_type": "gpu_count", "total": 16 },
    { "resource_type": "cpu_core", "total": 128 },
    { "resource_type": "memory_gb", "total": null }
  ]
}
```

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键，前端 `crypto.randomUUID()` 生成 |
| code | string | yes | `^[a-z0-9-]{3,40}$`，全局唯一（未删除间） |
| name | string | yes | 1-64 字符 |
| description | string | no | ≤ 512 字符 |
| quota_limits | array | no | 每项 `{resource_type, total}`；total null = default_quota |

**Response 200:**
```json
{ "id": "uuid", "message": "tenant plan created" }
```

#### GET /tenant-plans — 列表

**Query:** `limit` (default 20, max 100), `cursor` (上一页返回的 next_cursor), `status` (draft/active/disabled), `search`

**Response 200:**
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

> items 不含 quota_limits；tenant_count = COUNT(tenants WHERE plan_id=id AND status!='disabled')；total = 满足筛选条件的总条数（用于前端分页展示）

#### GET /tenant-plans/{planId} — 详情

**Response 200:**
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

#### GET /tenant-plans/{planId}/quota-limits — 查询限额

**Response 200:**
```json
{
  "items": [
    { "resource_type": "gpu_count", "display_name": "GPU 卡数", "unit": "card", "total": 16 },
    { "resource_type": "memory_gb", "display_name": "内存", "unit": "gb", "total": 32 }
  ]
}
```

> 展示路径：`buildQuotaLimitViews` = store.GetQuotaLimits 原始行 + Core `ListQuotaMeta`（**禁止** store JOIN `resource_quota_meta`）。历史 `total` 为 NULL 时用 `default_quota` 兜底返回并可回写库；写入侧 Create/PUT 的 `total: null` 在 service 层物化为具体 `default_quota` 再落库（不保留 NULL）。列表 `next_cursor` 网关返回空串 `""` 表示无更多；审计列表经 `nullIfEmpty` 返回 JSON `null`。时间字段为 `YYYY-MM-DD HH:mm:ss`（Asia/Shanghai），非 RFC3339。

#### PUT /tenant-plans/{planId}/quota-limits — 修改限额

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "items": [
    { "resource_type": "storage_gb", "total": 4096 },
    { "resource_type": "token_count", "total": null }
  ]
}
```

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键，前端 `crypto.randomUUID()` 生成 |
| items[].resource_type | string | yes | 必须在 resource_quota_meta 中 enabled=true |
| items[].total | integer\|null | yes | >= 0 或 null |

**Response 200:**
```json
{ "id": "uuid", "message": "quota limits updated" }
```

#### POST /tenant-plans/{planId}/activate — 发布套餐

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000"
}
```

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键，前端 `crypto.randomUUID()` 生成 |

**Response 200:**
```json
{ "id": "uuid", "message": "tenant plan activated" }
```

#### POST /tenant-plans/{planId}/disable — 禁用套餐

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000"
}
```

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键，前端 `crypto.randomUUID()` 生成 |

**Response 200:**
```json
{ "id": "uuid", "message": "tenant plan disabled" }
```

#### POST /tenants/{tenantId}/plan — 绑定套餐

**Request:**
```json
{
  "idempotency_key": "550e8400-e29b-41d4-a716-446655440000",
  "plan_id": "uuid"
}
```

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| idempotency_key | string (UUID) | yes | 幂等键，前端 `crypto.randomUUID()` 生成 |
| plan_id | string (UUID) | yes | 必须为 status=active 的未删除套餐 |

**Response 200:**
```json
{ "id": "uuid", "message": "quota bound to plan" }
```

#### GET /tenant-plans/{planId}/tenants — 绑定租户列表

**Response 200:**
```json
{
  "items": [
    { "id": "uuid", "name": "acme", "display_name": "ACME 公司", "status": "active" }
  ]
}
```

> 不分页；查询 tenants WHERE plan_id=planId AND status!='disabled'

#### GET /tenant-plans/{planId}/bindable-tenants — 可绑定租户列表

**Response 200:**
```json
{
  "items": [
    { "id": "uuid", "name": "acme", "display_name": "ACME 公司", "status": "active" }
  ]
}
```

> 不分页；查询 tenants WHERE status != 'disabled' AND plan_id IS DISTINCT FROM planId（排除已绑定该套餐的租户），按 name 排序。

#### GET /tenant-plans/{planId}/audit-logs — 操作历史

**Query:** `limit` (default 20, max 100), `cursor` (上一页返回的 next_cursor)

> **实现补充说明：** 原 SPEC 含 `action` / `result` 服务端过滤参数，实际未实现（设计决策：审计日志量小，前端本地 `result` 过滤即可）。`AuditLogFilter` 仅含 `Limit`/`Cursor`。

**Response 200:**
```json
{
  "items": [
    {
      "id": "uuid",
      "action": "tenant_plan.create",
      "result": "success",
      "details": { "plan_id": "uuid", "code": "pro" },
      "created_at": "2026-07-20 10:00:00"
    }
  ],
  "total": 12,
  "next_cursor": "cursor-string"
}
```

> **字段说明：** 网关仅返回 5 个字段（id/action/result/details/created_at）。`tenant_id`/`user_id`/`request_id`/`resource`/`ip_address`/`user_agent` 虽在数据库表中存储，但不在 API 响应中暴露。`created_at` 格式为「年-月-日 时:分:秒」（Asia/Shanghai）。

#### GET /quota-meta — 查询配额元数据

**Response 200:**
```json
{
  "items": [
    {
      "resource_type": "gpu_count",
      "display_name": "GPU 卡数",
      "unit": "card",
      "default_quota": 4,
      "is_discrete": true
    }
  ]
}
```

> 透传 Core `GET /admin/quota-meta` 结果（无缓存，每次实时调 Core）。Core 不可用时返回 502 `GRPC_CLIENT_UNAVAILABLE`。

### 4.3 Error Responses

| HTTP | code | Condition |
|------|------|-----------|
| 400 | VALIDATION_FAILED | total 为负数 / items 为空 / resource_type 重复 / code 格式不合法 |
| 404 | TENANT_PLAN_NOT_FOUND | 套餐不存在或已软删除 |
| 409 | PLAN_CODE_CONFLICT | code 与未删除套餐冲突（创建时） |
| 409 | PLAN_STATE_INVALID | 状态转换不合法（active 再 activate / draft 直接 disable） |
| 409 | TENANT_PLAN_IN_USE | 删除时有租户关联 |
| 409 | TENANT_STATE_INVALID | 绑定套餐时租户已 disabled |
| 422 | PLAN_NOT_ACTIVE | 绑定套餐时套餐状态非 active（draft / disabled） |
| 422 | QUOTA_RESOURCE_NOT_REGISTERED | resource_type 未注册或 enabled=false |
| 404 | TENANT_NOT_FOUND | 绑定时租户不存在（tenant-service 本地 store 返回，非 Core 层） |
| 404 | QUOTA_NOT_FOUND | Core 层返回：改限额时租户配额行不存在（未绑定套餐） |
| 409 | QUOTA_ALREADY_EXISTS | Core 层返回：绑定新建配额时配额行已存在 |
| 502 | GRPC_CLIENT_UNAVAILABLE | Core 服务不可用（配额元数据/配额下发失败） |

### 4.4 Breaking Changes

无破坏性变更。所有端点为新增。

---

## 5. Business Logic

### 5.1 Core Algorithms

#### 5.1.1 创建套餐 (TenantPlanService.Create)

```
输入校验:
  - code 格式 ^[a-z0-9-]{3,40}$
  - name 非空，1-64 字符
  - description ≤ 512 字符
  - quota_limits（可选）每项:
    · resource_type 经 QuotaSvcClient.ListQuotaMeta（Core GET /admin/quota-meta）校验 enabled=true → 否则 422
    · total null 或 >= 0
    · 同一 resource_type 不可重复

事务（store 内自管）:
  BEGIN
    INSERT tenant_plans (code, name, description, status='draft')
    ON CONFLICT (code) WHERE is_deleted=FALSE → 409 PLAN_CODE_CONFLICT
    INSERT plan_quota_limits FOR EACH (rt, lv) IN quota_limits
  COMMIT
  -- 审计日志在事务提交后写入（best-effort：审计失败不回滚已提交的套餐创建）
  INSERT audit_logs (action='tenant_plan.create', details={plan_id, code, quota_limits})
```

#### 5.1.2 修改限额同步存量租户 (TenantPlanService.UpdateQuotaLimits)

```
输入校验:
  - items 至少 1 项
  - 每项 resource_type 经 QuotaSvcClient.ListQuotaMeta 校验 enabled=true + total >= 0 或 null
  - 同一 resource_type 不可重复

事务:
  BEGIN
    UPSERT plan_quota_limits (INSERT ... ON CONFLICT (plan_id, resource_type) DO UPDATE SET total=EXCLUDED.total)
  COMMIT
  -- 审计日志在事务提交后写入（best-effort：审计失败不回滚已提交的限额变更）
  INSERT audit_logs (action='tenant_plan.update_quota_limits',
    details={plan_id, updated_dimensions: []string, synced_tenant_count: int, skipped_approved: int, tightened: int})
  -- 注：skipped_approved 和 tightened 为 int 计数（跳过/收紧的维度数量），updated_dimensions 为维度名列表 []string

同步存量租户（事务外，异步或同步）:
  tenants = SELECT * FROM tenants WHERE plan_id=planId AND status!='disabled'
  FOR EACH tenant:
    approved_dims = SELECT resource_type FROM tenant_quota_change
      WHERE tenant_id=tenant.id AND status='approved'
    update_items = [维度 NOT IN approved_dims 的 items]
    IF len(update_items) > 0:
      existing = QuotaSvcClient.GetQuota(tenant_id)  // 查询当前已存在的配额行
      → 逐维度分流（per resource_type）：
        - 维度在 existing 中 → putItems（PutQuota 修改 total，自动收紧）
        - 维度不在 existing 中 → createItems（CreateQuota 新建配额行）
      IF len(putItems) > 0: QuotaSvcClient.PutQuota(tenant_id, putItems)
      IF len(createItems) > 0: QuotaSvcClient.CreateQuota(tenant_id, createItems)
      → Core 自动收紧：total < used+reserved 时 tightened=true
```

#### 5.1.3 绑定套餐更新配额 (TenantService.BindPlanQuota)

```
校验:
  - plan_id 存在 AND is_deleted=FALSE → 否则 404 TENANT_PLAN_NOT_FOUND
  - plan.status == 'active' → 否则 422 PLAN_NOT_ACTIVE
  - tenant.status != 'disabled' → 否则 409 TENANT_STATE_INVALID

执行:
  quota_limits = TenantPlanStore.GetQuotaLimits(plan_id)  // 取原始行（保留 NULL 语义）
  quota_views = service.buildQuotaLimitViews(quota_limits)  // COALESCE(total, default_quota) 兜底为具体 total
  approved_dims = SELECT resource_type FROM tenant_quota_change
    WHERE tenant_id=tenant_id AND status='approved'
  update_items = [维度 NOT IN approved_dims]
  -- plan_id 变更时先更新 DB，再同步 Core；Core 失败则回滚 plan_id（best-effort）
  IF new_plan_id != current_plan_id:
    UPDATE tenants SET plan_id=new_plan_id
  IF len(update_items) > 0:
    existing = QuotaSvcClient.GetQuota(tenant_id)  // 先查询判断配额行是否存在
    IF existing 为空:
      QuotaSvcClient.CreateQuota(tenant_id, update_items)  // POST 新建配额行 used/reserved=0
    ELSE:
      QuotaSvcClient.PutQuota(tenant_id, update_items)  // PUT 修改 total，自动收紧
    → Core 自动收紧：total < used+reserved 时 tightened=true
    -- Core 失败 → 回滚 plan_id（best-effort：回滚失败也返回错误）
    IF core_err != nil AND plan_changed:
      UPDATE tenants SET plan_id=prev_plan_id
      → 回滚成功：details 含 items + rolled_back=true
      → 回滚失败：details 含 rollback_plan_id + items + rollback_error；返回错误
  -- 审计 best-effort：审计失败只 Warn 不阻塞成功响应
  -- failure 审计自动追加 reason 字段（gRPC status message）
  INSERT audit_logs (action='tenant.bind_plan_quota', details={plan_id, tenant_id, tenant_name, tenant_display_name, skipped_approved: int, tightened: int, updated: []string})
```

#### 5.1.4 删除套餐 (TenantPlanService.Delete)

```
校验:
  - 套餐存在 AND is_deleted=FALSE → 否则 404
  - SELECT COUNT(*) FROM tenants WHERE plan_id=plan_id AND status!='disabled' → 若 > 0 则 409 TENANT_PLAN_IN_USE

事务（store 内自管）:
  BEGIN
    UPDATE tenant_plans SET is_deleted=TRUE, deleted_at=now() WHERE id=plan_id
    -- 限额行随软删除保留（UPDATE is_deleted=TRUE 不触发 ON DELETE CASCADE）
  COMMIT
  -- 审计日志在事务提交后写入（best-effort：审计失败不回滚已提交的软删除）
  INSERT audit_logs (action='tenant_plan.delete', details={plan_id: plan_id})
```

### 5.2 Validation Rules

| Field | Rule |
|-------|------|
| code | `^[a-z0-9-]{3,40}$`，未删除套餐间全局唯一 |
| name | 非空，1-64 字符 |
| description | ≤ 512 字符 |
| quota_limits[].resource_type | 在 resource_quota_meta 中 enabled=true |
| quota_limits[].total | null 或 >= 0 |
| quota_limits | 同一 resource_type 不可重复 |

### 5.3 State Machine

```
         activate              disable
  draft ─────────▶ active ◀────────▶ disabled
    │                                  │
    └────────── activate ──────────────┘
    
  任意状态均可 DELETE（仅校验无租户关联）
  任意状态均可 PUT quota-limits
  name/description 可通过 PUT /tenant-plans/{planId} 修改（可选字段语义：未设置=不更新，空串=清空）
```

| Transition | Trigger | Guard |
|------------|---------|-------|
| draft → active | POST /activate | — |
| active → disabled | POST /disable | — |
| disabled → active | POST /activate | — |
| active → active | POST /activate | 409 PLAN_STATE_INVALID |
| draft → disabled | POST /disable | 409 PLAN_STATE_INVALID |

### 5.4 Edge Cases

| Case | Handling |
|------|----------|
| 修改限额时新 total < used+reserved | Core API 自动收紧为 used+reserved，返回 tightened=true；已有资源继续运行 |
| 绑定套餐时某维度有 approved 配额变更 | 跳过该维度，保留 approved 值不覆盖 |
| 绑定套餐时某维度有 pending 配额变更 | 按套餐值覆盖（等审批通过后再覆盖套餐值） |
| 删除套餐时 plan_quota_limits 行 | 软删除（UPDATE is_deleted=TRUE）不触发 ON DELETE CASCADE，限额行随套餐行保留 |
| 软删除后 code 复用 | partial unique index WHERE is_deleted=FALSE 允许复用 |
| 同步存量租户时 Core API 失败 | 记录到 audit_logs (action='tenant.quota_init_failed'，details 含 plan_id/tenant_id/items)，内联 goroutine 异步重试（最多 3 次，指数退避 1s/2s/4s；重试失败审计 details 追加 attempt 次数） |
| activate/disable 审计 details | details 含 {plan_id, status}（status 为转换后的状态值） |
| update（PUT 基本信息）审计 details | details 含 {plan_id, name_updated: bool, description_updated: bool} |
| failure 审计自动追加 reason | 所有 writeAuditFailure 调用自动在 details 追加 reason 字段（gRPC status message） |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP | Condition | User Message (zh-CN) |
|------------|------|-----------|---------------------|
| VALIDATION_FAILED | 400 | 参数校验失败 | 校验失败：{message} |
| TENANT_PLAN_NOT_FOUND | 404 | 套餐不存在或已删除 | 套餐不存在 |
| PLAN_CODE_CONFLICT | 409 | code 冲突 | 套餐代码已存在，请更换 |
| PLAN_STATE_INVALID | 409 | 状态转换不合法 | 套餐状态不允许此操作 |
| TENANT_PLAN_IN_USE | 409 | 删除时有租户关联 | 该套餐已关联租户，不可删除 |
| TENANT_STATE_INVALID | 409 | 绑定时租户已 disabled | 租户已停用，不可绑定套餐 |
| PLAN_NOT_ACTIVE | 422 | 绑定时套餐状态非 active | 套餐未发布，不可被租户引用 |
| QUOTA_RESOURCE_NOT_REGISTERED | 422 | 维度未注册 | 配额维度未注册或已禁用 |
| TENANT_NOT_FOUND | 404 | 绑定时租户不存在（tenant-service 本地 store 返回，非 Core 层） | 租户不存在 |
| QUOTA_NOT_FOUND | 404 | Core 层返回：改限额时租户配额行不存在（未绑定套餐） | 租户配额不存在，请先绑定套餐 |
| QUOTA_ALREADY_EXISTS | 409 | Core 层返回：绑定新建配额时配额行已存在 | 租户配额已存在 |
| GRPC_CLIENT_UNAVAILABLE | 502 | Core 服务不可用（配额元数据/配额下发失败） | Core 服务不可用，请稍后重试 |

### 6.2 Retry Strategy

| Operation | Retry | Max | Backoff |
|-----------|-------|-----|---------|
| Core POST /quota（绑定新建配额行） | 是 | 3 次 | 指数退避 |
| Core PUT /quota（改限额同步存量租户） | 是 | 3 次 | 指数退避 |
| 创建/修改/删除套餐 | 否 | — | — |

### 6.3 Failure Modes

| Failure | Impact | Handling |
|---------|--------|----------|
| Core API 不可用 | 限额同步失败 | 记录 audit_logs + 异步重试；套餐修改本身已成功 |
| PostgreSQL 连接失败 | 全部操作失败 | 返回 500，前端展示网络错误 |
| resource_quota_meta 中维度被禁用 | 创建/修改限额 422 | 前端提示「配额维度未注册或已禁用」 |

---

## 7. Security

### 7.1 Authentication & Authorization

| Endpoint | platform-admin | platform-ops | platform-readonly |
|----------|---------------|-------------|-------------------|
| POST /tenant-plans | ✅ | ✅ | ❌ |
| GET /tenant-plans* | ✅ | ✅ | ✅ |
| GET /tenant-plans/{id}/tenants | ✅ | ✅ | ✅ |
| GET /tenant-plans/{id}/audit-logs | ✅ | ✅ | ✅ |
| PUT /tenant-plans/{id} | ✅ | ✅ | ❌ |
| POST /tenant-plans/{id}/activate | ✅ | ✅ | ❌ |
| POST /tenant-plans/{id}/disable | ✅ | ✅ | ❌ |
| DELETE /tenant-plans/{id} | ✅ | ✅ | ❌ |
| PUT /tenant-plans/{id}/quota-limits | ✅ | ✅ | ❌ |
| POST /tenants/{id}/plan | ✅ | ✅ | ❌ |
| GET /tenant-plans/{id}/bindable-tenants | ✅ | ✅ | ✅ |
| GET /quota-meta | ✅ | ✅ | ✅ |

> AuthZ 由 ani-gateway 网关层校验，通过网关透传给 tenant-service 的 gRPC metadata（x-request-id / x-user-id）供追踪。tenant-service 信任网关鉴权结果，不自行校验角色。

### 7.2 Input Validation

- code: 正则 `^[a-z0-9-]{3,40}$` 服务端校验
- name: 长度 1-64 服务端校验
- description: 长度 ≤ 512 服务端校验
- total: >= 0 或 null 服务端校验
- resource_type: 查 resource_quota_meta 确认 enabled=true

### 7.3 Data Protection

- 审计日志记录所有写操作（create/activate/disable/delete/update/update_quota_limits/bind_plan_quota；失败写 failure，含 `tenant.quota_init_failed`）
- 审计写入填充 user_id / request_id（网关 gRPC metadata `x-user-id` / `x-request-id`）；**当前实现不写入** `ip_address` / `user_agent`（列可空）
- 无敏感字段需加密

---

## 8. Performance

### 8.1 Expected Load

| Metric | Estimate |
|--------|----------|
| 套餐总数 | < 100 |
| 每套餐绑定租户数 | < 1000 |
| 列表 QPS | < 10 |
| 修改限额同步频率 | 低（< 1 次/天） |

### 8.2 Optimization Strategy

| Strategy | Application |
|----------|-------------|
| tenant_count 子查询 | 列表页 COUNT(tenants WHERE plan_id=id AND status!='disabled')，数据量小无需缓存 |
| 逐租户分流调 Core API | 修改限额同步时逐租户 GetQuota→CreateQuota/PutQuota 分流，减少 API 往返 |
| 限额查询 | GET quota-limits：store 原始行 + Core ListQuotaMeta 组装（无本地 JOIN meta） |

### 8.3 Database Considerations

| Index | Purpose |
|-------|---------|
| idx_tenant_plans_code_active (UNIQUE WHERE is_deleted=FALSE) | code 唯一 + 软删除复用 |
| plan_quota_limits PK (plan_id, resource_type) | 按套餐查限额（复合主键前导列覆盖，无需额外索引） |
| tenants.plan_id (FK) | 查套餐绑定租户 |
| resource_quota_meta PK (resource_type) | 维度注册表主键 |
| resource_quota PK (tenant_id, resource_type) | 租户配额行主键 |
| resource_quota RLS | 租户隔离（platform_bypass + self） |
| idx_res_state_expires (WHERE state='reserved') | TTL 孤儿回收扫描 |
| idx_res_tenant (tenant_id, state) | 按租户查预占流水 |
| resource_reservations RLS | 租户隔离（platform_bypass + self） |

---

## 9. Testing Strategy

### 9.1 Unit Tests

| Test | Scope | Description |
|------|-------|-------------|
| TestTenantPlanService_Create | service | code 格式校验、quota_limits 维度校验、事务一致性、审计日志写入 |
| TestTenantPlanService_Create_DuplicateCode | service | code 冲突 409 |
| TestTenantPlanService_Activate | service | draft→active、disabled→active、active→active 409 |
| TestTenantPlanService_Disable | service | active→disabled、draft→disabled 409 |
| TestTenantPlanService_Delete | service | 无租户关联成功删除、有租户关联 409、软删除后 code 可复用 |
| TestTenantPlanService_UpdateQuotaLimits | service | 限额更新 + 同步存量租户（mock Core API）+ approved 维度跳过 |
| TestTenantService_BindPlanQuota | service | plan_id 校验、tenant 状态校验、Core API 调用、approved 维度跳过 |

### 9.2 Integration Tests

| Test | Scope | Description |
|------|-------|-------------|
| TestHandler_Create_FullFlow | handler | POST → 200 → GET 详情验证 |
| TestHandler_QuotaLimits_FullFlow | handler | POST 创建 → GET quota-limits → PUT 修改 → GET 验证 |
| TestHandler_Delete_InUse | handler | POST 创建 + POST tenants → DELETE 409 |
| TestHandler_AuditLogs | handler | POST 创建 → GET audit-logs 验证记录 |
| TestHandler_UpdatePlanInfo | handler | PUT /tenant-plans/{id} 更新 name/description → GET 验证 |
| TestHandler_ListQuotaMeta | handler | GET /quota-meta → 200 + items[] 验证 |
| TestHandler_ListBindableTenants | handler | GET /tenant-plans/{id}/bindable-tenants → 200 + 排除已绑定 |

### 9.3 Edge Case Tests

| Test | Description |
|------|-------------|
| Test_Create_QuotaResourceNotRegistered | quota_limits 含未注册维度 → 422 |
| Test_UpdateQuotaLimits_Tightened | 同步时 Core 返回 tightened=true → 不报错 |
| Test_BindPlanQuota_ApprovedSkip | approved 维度跳过 Core API 调用 |
| Test_BindPlanQuota_DisabledTenant | tenant disabled → 409 |
| Test_Delete_SoftDeleteCodeReuse | 软删除后新套餐使用相同 code → 成功 |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type |
|-------|------|------|
| US-001 创建套餐 | TestTenantPlanService_Create | unit |
| US-002 查询列表 | TestHandler_Create_FullFlow | integration |
| US-003 查询详情 | TestHandler_Create_FullFlow | integration |
| US-004 查询限额 | TestHandler_QuotaLimits_FullFlow | integration |
| US-005 发布套餐 | TestTenantPlanService_Activate | unit |
| US-006 禁用套餐 | TestTenantPlanService_Disable | unit |
| US-007 删除套餐 | TestTenantPlanService_Delete + Test_Delete_SoftDeleteCodeReuse | unit |
| US-008 修改限额 | TestTenantPlanService_UpdateQuotaLimits + Test_UpdateQuotaLimits_Tightened | unit |
| US-009 绑定套餐 | TestTenantService_BindPlanQuota + Test_BindPlanQuota_ApprovedSkip | unit |
| US-010 绑定租户列表 | TestHandler_Delete_InUse (验证 tenant_count) | integration |
| US-011 操作历史 | TestHandler_AuditLogs | integration |
| US-016 更新套餐基本信息 | TestHandler_UpdatePlanInfo | integration |
| US-017 查询配额元数据 | TestHandler_ListQuotaMeta | integration |
| US-018 可绑定租户列表 | TestHandler_ListBindableTenants | integration |
| FR-1 套餐 CRUD | TestTenantPlanService_Create/Delete | unit |
| FR-2 限额查询 | TestHandler_QuotaLimits_FullFlow | integration |
| FR-3 限额修改 | TestTenantPlanService_UpdateQuotaLimits | unit |
| FR-5 不可删除 | TestHandler_Delete_InUse | integration |
| FR-6 绑定套餐 | TestTenantService_BindPlanQuota | unit |
| FR-7 状态转换 | TestTenantPlanService_Activate/Disable | unit |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | Description | Depends On |
|-------|-------------|------------|
| 1 | OpenAPI 契约：services/v1.yaml 新增 tenant-plans 路径 + schema + 错误码 | — |
| 2 | 数据库迁移：文件 1 resource_quota_meta + resource_quota + resource_reservations；文件 2 tenant_plans + plan_quota_limits + tenants.plan_id | — |
| 3 | tenant-service 骨架：go.mod + grpc server 骨架（Unimplemented 占位）+ ports 接口 | — |
| 4 | 网关接入：tenant gRPC client + /api/v1/svc/tenant-plans*、/tenants/:id/plan 路由 + 错误映射 | 3 |
| 5-9 | 后端 API（按关联切分）：创建 / 列表+详情 / 状态+删除 / 限额(含 QuotaSvcClient) / 绑定+租户列表(含 TenantStore) | 3, 4 |
| 10-13 | 后端扩展 API：更新基本信息 / 查询配额元数据 / 可绑定租户列表 / 操作历史 | 3, 4 |
| 14-18 | 前端页面（按页拆）：列表页 / 详情页 / 限额 Tab / 绑定租户 Tab / 操作历史 Tab | 1, 后端对应 API |

### 10.2 Issue Mapping

| Issue | SPEC/UX Sections | Priority | Depends On |
|-------|------------------|----------|------------|
| #1 OpenAPI 契约 | SPEC 4.1, 4.2, 4.3 | high | — |
| #2 接口与结构体 | SPEC 2.2, 2.4, 3.2 | high | #1 |
| #3 数据库迁移 | SPEC 3.1, 3.4 | high | — |
| #4 网关实现 | SPEC 2.3, 2.4, 7.1, 6.3 | high | #2, #3 |
| #5 创建套餐 API | SPEC 5.1.1, 4.2, 6.1 | high | #2, #3, #4 |
| #6 列表+详情 API | SPEC 5.1, 4.2, 6.1 | high | #2, #3, #4 |
| #7 状态+删除 API | SPEC 5.3, 5.1.4, 4.2 | high | #2, #3, #5 |
| #8 限额查询+修改 API（含 QuotaSvcClient） | SPEC 5.1.2, 4.2, 6.2 | high | #2, #3, #6 |
| #9 绑定套餐+租户列表 API（含 TenantStore） | SPEC 5.1.3, 4.2, 6.1 | high | #2, #3, #8 |
| #10 更新套餐基本信息 API | SPEC 5.3, 4.2 | high | #2, #3, #4 |
| #11 查询配额元数据 API | SPEC 4.1, 4.2 | high | #2, #4 |
| #12 可绑定租户列表 API | SPEC 4.1, 4.2 | high | #2, #3, #4 |
| #13 操作历史 API（含 AuditStore 查询） | SPEC 4.2, 5.1 | high | #2, #3, #7, #9 |
| #14 前端列表页（含创建 Wizard） | UX 4.1, 4.2, 5.1, 5.2, 6.1, 6.2, 7 | high | #1, #6 |
| #15 前端详情页（基础信息+状态操作+Tabs 框架） | UX 4.3, 5.3, 6.3, 7 | high | #14 |
| #16 前端限额 Tab | UX 4.3, 5.3, 6.4, 7 | high | #15, #8 |
| #17 前端绑定租户 Tab | UX 4.3, 5.3, 6.5, 7 | high | #15, #9 |
| #18 前端操作历史 Tab | UX 4.3, 5.3, 6.6, 7 | high | #15, #13 |

### 10.3 Incremental Delivery

1. Phase 1-2 可并行（契约 + 迁移互不依赖）
2. Phase 14-18（前端）依赖 Phase 1（契约），不依赖后端实现完成 — 可用 mock 先行
3. Phase 9（绑定套餐）依赖套餐 Service + Core 客户端均完成

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- Core `resource_quota_meta` / Core quota API 是否可用？限额查询与同步依赖 Core `ListQuotaMeta` / GetQuota / PutQuota / CreateQuota（非本地 JOIN）。
- Core API `GET/POST/PUT/DELETE /api/v1/admin/tenants/{tenant_id}/quota` 端点是否均已实现？本文档假设已实现（GetQuota 查存在性 + CreateQuota 新建 + PutQuota 修改 + DeleteQuota 删除）。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Core API 不可用时限额同步失败 | 存量租户配额不更新 | audit_logs 记录失败 + 异步重试（3 次，指数退避） |
| plan_quota_limits 跨层外键到 Core resource_quota_meta | 迁移顺序依赖 | 两个迁移文件按顺序执行：文件 1（resource_quota）先于文件 2（tenant_plan_management） |
| 同步大量租户时 Core API 超时 | 部分租户未同步 | 逐租户调用 + 失败记录 + 异步重试 |

### 11.3 Assumptions

- Core `resource_quota_meta` 表已存在且有 enabled=true 的维度数据
- Core API `GET /api/v1/admin/tenants/{tenant_id}/quota`（查询配额行是否存在）已实现
- Core API `POST /api/v1/admin/tenants/{tenant_id}/quota`（新建配额行，used/reserved 初始 0）已实现
- Core API `PUT /api/v1/admin/tenants/{tenant_id}/quota`（批量修改 total）已实现，返回含 `tightened` 字段
- Core API `DELETE /api/v1/admin/tenants/{tenant_id}/quota`（删除租户全部配额行）已实现（QuotaSvcClient.DeleteQuota 封装，当前预留，尚未被业务流程调用）
- ani-gateway 通过 gRPC client（`TENANT_SERVICE_ADDR`，缺省 `127.0.0.1:9105`，与 tenant-service `GRPC_PORT` 一致）将 `/api/v1/svc/tenant-plans*`、`/tenants/:tenantId/plan` 请求转发到 tenant-service
- 路由分发、鉴权、限流、幂等性校验由 ani-gateway 网关完成，tenant-service 仅处理业务逻辑
- 审计日志复用现有 `audit_logs` 分区表
- 前端使用 TDesign React + TanStack Router + TanStack Query
- `idempotency_key` 由前端 `crypto.randomUUID()` 生成放入 request body
