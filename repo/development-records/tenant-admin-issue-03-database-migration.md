# TENANT-ADMIN-ISSUE-03：数据库迁移 — tenant_admin_invitation 建表 + Core users 表列扩展

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #3）
> **完成日期：** 2026-08-21
> **Scope：** `repo/deploy/migrations/` 新增 1 个迁移文件
> **依赖：** 无（SPEC §3.4 标注 "无跨 Core 表依赖"）
> **Product line：** boss（Services 层邀请表 + Core 层 users 列扩展）

## 交付内容

新增迁移文件 `20260821_001_tenant_admin_invitation.sql`，落地租户管理员邀请独立表与 Core users 表列扩展。

### `20260821_001_tenant_admin_invitation.sql`（单文件双 Step）

- **Step 1（Services）：** `CREATE TABLE tenant_admin_invitation` + 两个索引（`uk_tenant_admin_invitation_token_hash` UNIQUE / `idx_tenant_admin_invitation_user_status`）+ RLS 双策略（`platform_bypass` + `tenant_self` PERMISSIVE）+ `GRANT` 表级读写给 `ani_app_user`。
- **Step 2（Core）：** `ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name / is_deleted / deleted_at`（展示名 + 软删除标记，与 `users.status` 独立）。
- **Rollback：** 注释块提供完整回滚 DDL（DROP COLUMN → REVOKE → DROP POLICY → DROP TABLE）。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 3.1.1 DDL 对齐 SPEC §3.1.1 | 字段/类型/约束/索引/RLS/GRANT 逐项匹配 | ✅ |
| token_hash 存 SHA-256、status CHECK、expire_at 由应用层写 | 注释与列类型对齐 | ✅ |
| RLS 对齐 audit_logs / tenant_quota_change 模式 | `platform_bypass` + `tenant_self` PERMISSIVE + FORCE | ✅ |
| 3.1.2 users 列扩展对齐 SPEC §3.1.2 | `display_name TEXT` / `is_deleted BOOLEAN NOT NULL DEFAULT FALSE` / `deleted_at TIMESTAMPTZ` | ✅ |
| 不破坏现有约束（status 与 is_deleted 独立） | 注释明确"与 users.status(active/disabled) 独立" | ✅ |
| 迁移可回滚 | 注释块完整 rollback DDL | ✅ |
| 迁移命名对齐 `YYYYMMDD_NNN_name.sql` | `20260821_001_tenant_admin_invitation.sql` | ✅ |
| 迁移执行成功 | 待 `make migrate` 验证（见 Open Questions） | ⏳ |

## Design Decisions

### D1：单迁移文件承载 Services + Core 双层 DDL

- **Ambiguity：** SPEC §3.4 Migration Plan 列出两个 Step（Services 建表 + Core ALTER），并指出"可并入或独立新增文件，按部署流程"，未强制单文件或双文件。
- **Choice：** 合并为单文件 `20260821_001_tenant_admin_invitation.sql`，以 `BEGIN`/`COMMIT` 事务包裹。
- **Rationale：** 两者无跨表依赖（`tenant_admin_invitation` 仅 FK 引用已存在的 `tenants.id` / `users.id`），合并后部署原子性更好——要么全部就位，要么全部回滚。`ALTER TABLE users ADD COLUMN IF NOT EXISTS` 幂等，即使与平台运营账号迁移共存也不会冲突。

### D2：`ADD COLUMN IF NOT EXISTS` 幂等策略

- **Ambiguity：** SPEC §3.1.2 注释提到"IF NOT EXISTS 幂等，便于与平台运营账号迁移共存"，但未说明是否为强制。
- **Choice：** 对 Core `users` 表三列均使用 `ADD COLUMN IF NOT EXISTS`。
- **Rationale：** `users` 为 Core 共享表，可能存在其他批次（如平台运营账号迁移）同时扩展列。`IF NOT EXISTS` 保证重复执行不报错，符合迁移可回滚与幂等要求。`is_deleted` 带 `NOT NULL DEFAULT FALSE`，即使存量行也能自动填充默认值。

### D3：`is_deleted` 与 `users.status` 语义独立

- **Ambiguity：** `users.status` 已有 active/disabled 业务状态，新增 `is_deleted` 是否冗余或冲突。
- **Choice：** 保留两者独立——`status` 为业务状态（active/disabled），`is_deleted` 为软删除标记（DELETE 操作使用）。
- **Rationale：** SPEC §3.1.2 明确要求二者不互斥。`status='disabled'` 可恢复（enable），`is_deleted=TRUE` 不可恢复（DELETE 软删除后列表 `WHERE is_deleted = FALSE` 过滤）。注释中注明语义边界，避免后续维护混淆。

## Deviations

None — 实现严格遵循 SPEC §3.1.1 / §3.1.2 / §3.4 的 DDL、索引、RLS 策略和回滚方案。

## Tradeoffs

### T1：单文件 vs 双文件迁移

- **备选 A：** 单文件合并（已选）
  - 优点：部署原子性（单事务），减少迁移文件数量，依赖关系内聚
  - 缺点：Services 层与 Core 层变更物理耦合，若未来 Core 层迁移有独立节奏需注意顺序
- **备选 B：** 双文件（`20260821_001_tenant_admin_invitation.sql` + `20260821_002_users_columns.sql`）
  - 优点：层边界更清晰，Core 变更可独立回滚
  - 缺点：需保证 002 在 001 之前执行（FK 依赖 users.id 已存在），但 users 表早已存在，非真依赖
- **选择理由：** SPEC 允许合并，且两者无真实执行顺序依赖（`users` 表在 `20260501_001_init_schema.sql` 已创建），单文件更简洁。

### T2：RLS FORCE 强制策略

- **备选 A：** `FORCE ROW LEVEL SECURITY`（已选，对齐 `audit_logs` / `tenant_quota_change`）
  - 优点：即使表 owner 也受 RLS 约束，租户隔离更强
  - 缺点：平台操作必须显式设置 `app.current_tenant_id` 为空才能 bypass
- **备选 B：** 不 FORCE，依赖 owner 自动 bypass
  - 优点：平台操作无需显式设置 session 变量
  - 缺点：隔离弱化，与现有 `audit_logs` / `tenant_quota_change` 模式不一致
- **选择理由：** 对齐现有 RLS 模式，保证多租户隔离一致性。

## Open Questions

### Q1：迁移执行验证未完成

- **Assumption：** 迁移文件 DDL 语法与逻辑正确，但尚未在真实 PostgreSQL 实例执行 `make migrate` 验证。
- **Should verify：** 在开发环境执行迁移，确认表结构、索引、RLS 策略、GRANT 就位，且 rollback DDL 可正确回滚。
- **Follow-up：** Issue-004（网关集成）依赖本表存在，执行验证需在 Issue-004 实现前完成。

### Q2：RLS `platform_bypass` 运行时触发方式

- **Assumption：** `platform_bypass` 策略依赖 `current_setting('app.current_tenant_id', true) IS NULL OR = ''`，假设 tenant-service / 网关在平台操作上下文不设置该变量或设为空。
- **Should verify：** tenant-service 与 ani-gateway 的 DB 连接初始化代码是否正确设置/清空 `app.current_tenant_id`。此为运行时行为，非建表阻断项，需在 Issue-004（网关集成）或 Issue-003 后续（tenant-service 骨架）确认。
- **Follow-up：** 若运行时未正确管理 session 变量，可能导致平台操作被 RLS 误拦截或租户数据泄露。

### Q3：`expire_at = now() + 72h` 由应用层写入

- **Assumption：** SPEC §3.1.1 明确 `expire_at` 由应用层写入（`now()+72h`），迁移文件不设 DEFAULT。
- **Should verify：** tenant-service 的 `TenantAdminStore.InsertInvitation` 实现必须显式传入 `expire_at`，不能依赖 DB DEFAULT（本表无该列 DEFAULT）。
- **Follow-up：** Issue-004 / Issue-005 实现 Invite/Resend 逻辑时验证。

## 验证命令

```bash
cd repo
git diff --check
# SQL 由 applier 执行：psql -f repo/deploy/migrations/20260821_001_tenant_admin_invitation.sql
# 验证表结构：
# \d tenant_admin_invitation
# \d+ users  (检查 display_name / is_deleted / deleted_at 列)
# 验证 RLS 策略：
# SELECT polname, polcmd, polqual FROM pg_policy WHERE polrelid = 'tenant_admin_invitation'::regclass;
```

## 边界声明

- 本 Issue 只做数据库结构与 RLS/GRANT，不涉及任何 .go 实现、前端页面、API 契约。
- `tenant_admin_invitation` 表的运行时 CRUD 由 tenant-service 的 `PostgresTenantAdminStore` 适配器操作（Issue-003 后续 / Issue-004）。
- `users` 表的运行时操作通过 Core SDK `UserSvcClient` 调用 Core `/api/v1/users/` API，**不直接 SQL 操作 Core 表**（SPEC §3.4 强制）。
- 本迁移文件与 `20260810_002_tenant_plan_management.sql`（套餐管理）无依赖关系，可并行执行。
