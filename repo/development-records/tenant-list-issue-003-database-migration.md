# TENANT-LIST-ISSUE-003：租户列表管理 — 数据库迁移

> **批次类型：** Migration batch（BOSS 租户列表管理 Issue #3）
> **完成日期：** 2026-09-03
> **Scope：** `repo/deploy/migrations/20260902_001_tenant_list_management.sql` + `atlas.sum`
> **依赖：** 无（可与 Issue-001/002 并行）；运行时依赖 `20260501000100` → `20260810000200`
> **Product line：** boss（Core 数据层）
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-003-database-migration.md`

## 交付内容

单文件前滚迁移，落地租户列表管理 Core 侧 schema：

1. `tenants` 状态机 CHECK 收窄为 `active|frozen|disabled`，存量 `suspended→frozen`、`deleted→disabled`
2. 列扩展：`contact_email` / `frozen_at` / `disabled_at`（`ADD COLUMN IF NOT EXISTS`）
3. 新建 `tenant_auth`（1:1）+ 存量回填
4. 新建 `tenant_lifecycle` + 复合索引；**存量回填 `action='create'`**（`created_at` 取 `tenants.created_at`，保证 US-015 非空）
5. 两表 RLS（platform_bypass + self_read）+ `GRANT` 至 `ani_app`
6. `atlas.sum` 登记本文件

不新建 `tenant_quota_change` / `audit_logs`（已存在）。

### 修改/新增文件

| 文件 | 变更 |
|---|---|
| `deploy/migrations/20260902_001_tenant_list_management.sql` | 新建迁移（含 lifecycle create 回填） |
| `deploy/migrations/atlas.sum` | 追加本文件 hash |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| status CHECK 三态 + 存量映射 | DROP → UPDATE → ADD CONSTRAINT 顺序 | ✅ |
| contact_email / frozen_at / disabled_at | `ADD COLUMN IF NOT EXISTS` | ✅ |
| tenant_auth 建表 + 1:1 回填 | CREATE + `INSERT … ON CONFLICT DO NOTHING` | ✅ |
| tenant_lifecycle + action CHECK + 索引 | CREATE TABLE/INDEX | ✅ |
| 存量 lifecycle create 回填（SPEC 可选 → 已落地） | `INSERT … WHERE NOT EXISTS (… action='create')` | ✅ |
| RLS 双策略 + GRANT ani_app | 对齐模板 / 20260831_001 NULLIF 写法 | ✅ |
| 不新建 quota_change / audit_logs | 文件内无 CREATE | ✅ |
| 文件头 Depends / Rationale / Rollback | 含 create 回填说明；回滚先删 create 行 | ✅ |
| rg 复核 suspended 租户状态 | `pkg/`、`ani-gateway`、`tenant-service` `.go` 无字面量 | ✅ |

## Design Decisions

### D1：RLS bypass 使用 `NULLIF(..., '') IS NULL`

- **Ambiguity：** Issue 要求对齐 `20260811000300_tenant_quota_change.sql`（裸 `IS NULL`）。
- **Choice：** 采用 `20260831_001_async_tasks_rls_fix` 已验证的 `NULLIF(current_setting(...), '') IS NULL`。
- **Rationale：** 连接池事务结束后 GUC 可能残留空串；裸 `IS NULL` 会误判平台上下文为租户上下文并返回 0 行。

### D2：GRANT 授给 `ani_app` 而非 `ani_app_user`

- **Ambiguity：** 旧迁移（quota_change / tenant_admin_invitation）曾直授 `ani_app_user`。
- **Choice：** `GRANT … TO ani_app`（`ani_app_user` 继承）。
- **Rationale：** `MIGRATION_TEMPLATE` 与 `20260828000200` / `20260831_001` 明确要求组角色授权。

### D3：CHECK 变更顺序固定为 DROP → UPDATE → ADD

- **Choice：** 先删旧约束，再映射存量，再加新 CHECK。
- **Rationale：** 若先加新 CHECK 或未 DROP，`suspended`/`deleted` 的 UPDATE 会被旧/新约束拒绝。

### D4：存量回填 `tenant_lifecycle('create')`，时间戳取 `tenants.created_at`

- **Ambiguity：** SPEC §3.1 将 lifecycle create 回填标为可选；Issue AC 未强制。
- **Choice（修复后）：** 对尚无 `action='create'` 的租户插入一行，`created_at = tenants.created_at`；`WHERE NOT EXISTS` 保证幂等。
- **Rationale：** 保证 US-015 生命周期 Tab 对存量租户非空；用租户创建时间而非 `NOW()`，避免时间线失真。

### D5：列扩展使用 `ADD COLUMN IF NOT EXISTS`

- **Choice（修复后）：** `contact_email` / `frozen_at` / `disabled_at` 均带 `IF NOT EXISTS`。
- **Rationale：** 对齐 `MIGRATION_TEMPLATE`「可重复执行的普通字段」建议；半成品库重跑更稳妥。

### D6：映射后不回填 `frozen_at` / `disabled_at`

- **Choice：** 仅改 status；时间戳列保持 NULL，留给后续真实状态转换写入。
- **Rationale：** 无法可靠还原历史冻结/禁用时刻；UX 通常仅在有值时展示时间戳。

## Deviations

### Dev-1：RLS 写法优于 Issue 引用的旧先例

- **Issue 说：** 模式对齐 `20260811000300`。
- **实现：** 策略形态仍是 platform_bypass + self_read，但 bypass 条件升级为 NULLIF 形态。
- **原因：** 与已 live-verified 的 async_tasks RLS 修复一致，避免连接池空串陷阱。

### Dev-2：落地 SPEC 可选的 lifecycle create 回填

- **SPEC 说：** 「存量租户补 tenant_lifecycle 'create' 行（可选）」。
- **实现（修复后）：** 已做幂等回填。
- **原因：** 优先保证 US-015 存量可读；Rollback 注释对应 `DELETE … WHERE action='create'`。

## Tradeoffs

### T1：ADD COLUMN 是否加 `IF NOT EXISTS`

| 方案 | 优点 | 缺点 |
|---|---|---|
| 不加 | Atlas 单次前滚语义最简 | 手工半成品库重跑可能撞列已存在 |
| **加 IF NOT EXISTS（选用，修复后）** | 对齐模板；重跑更安全 | 可能掩盖半失败状态 |

**修复后选用加 IF NOT EXISTS。**

### T2：回滚是否自动反向映射 status

| 方案 | 结果 |
|---|---|
| 自动 `frozen→suspended` / `disabled→deleted` | 无法区分「本就 frozen」与「由 suspended 映射」 |
| **注释警告 + 人工确认（选用）** | 回滚安全；避免错误还原 |

### T3：lifecycle create 回填的 `created_at` 来源

| 方案 | 结果 |
|---|---|
| `NOW()` | 实现简单；时间线与租户创建脱节 |
| **`tenants.created_at`（选用）** | US-015 时间线更接近真实创建时刻 |

## Review-it

- **目标：** Issue-003 未提交迁移（含后续修复）
- **结果：** clean — 无 accepted/actionable findings
- **修复相对初版笔记：** ① lifecycle create 回填；② `ADD COLUMN IF NOT EXISTS`；③ Rollback 同步删 create 行
- **验证：** AC 对照 + rg suspended；atlas CLI 不可用故未跑 migrate validate

## Verification Commands

```bash
# 代码侧 R1 复核
rg -n "suspended" repo/pkg repo/services/ani-gateway repo/services/tenant-service --glob '*.go'

# 有 atlas 时（修复后务必重算/校验 sum）
cd repo
make db-validate
make db-migrate-dry-run
```

## 后续 Issue 依赖

| Issue | 依赖本迁移 |
|---|---|
| Core TenantService 实现（create/状态机/auth） | 需要 tenant_auth / tenant_lifecycle / 新列 |
| tenant-service `TenantStore.ListLifecycle` | 直读 `tenant_lifecycle`（Issue-002 已定；存量已有 create 行） |
| Gateway / 业务 API | 无直接依赖本 SQL；依赖表存在 |
