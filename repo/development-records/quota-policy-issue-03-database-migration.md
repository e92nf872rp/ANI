# QUOTA-POLICY-ISSUE-03：数据库迁移 — 配额基础表 + 套餐管理表 + 旧列清理

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #3）
> **完成日期：** 2026-08-10
> **Scope：** `repo/deploy/migrations/` 新增 3 个迁移文件
> **依赖：** #1 OpenAPI 契约、#2 接口/结构体
> **Product line：** boss（Core 配额底层）

## 交付内容

新增 3 个迁移文件落地配额套餐所需的全部表结构、RLS、授权、默认套餐 seed，并清理 `tenants` 表废弃配额列。

### `20260810000100_resource_quota.sql`（配额基础表）

- `resource_quota_meta`：维度注册表 + 8 维度初始 seed（ON CONFLICT DO NOTHING），不加 RLS（平台治理数据）。
- `resource_quota`：租户配额账本（tenant_id + resource_type PK / total/reserved/used + CHECK 约束），RLS 双策略 `platform_bypass` + `self` 且 FORCE。
- `resource_reservations`：TCC 配额流水（state: reserved→confirmed/cancelled/expired/released），RLS 双策略。
- GRANT 配额基础表读写给 `ani_app_user`（按 plan §4.3.4，含 `GRANT ... ON ALL SEQUENCES` 模板）。

### `20260810000200_tenant_plan_management.sql`（套餐管理表）

- `tenant_plans`：套餐主表（code/name/description/status CHECK IN draft|active|disabled + 软删除 is_deleted/deleted_at + partial unique code）。
- `plan_quota_limits`：套餐维度限额（plan_id + resource_type PK / total nullable / FK → tenant_plans & resource_quota_meta）。
- `tenants.plan_id`：分步实现最终 NOT NULL——3a 加可空 → 3b 插入固定 UUID `00000000-0000-0000-0000-000000000001` 的 starter 入门套餐 + 8 维度限额 seed → 3c 回填存量租户到 starter → 3d SET NOT NULL。
- 套餐表不授权普通用户 `ani_app_user`（平台治理数据，RBAC 限制为 platform-admin）。

### `20260810000300_drop_tenant_max_quota_columns.sql`（清理废弃列）

- `DROP COLUMN IF EXISTS` 删除 `tenants.max_gpu_count / max_cpu_cores / max_memory_gb`（配额职责已迁至 resource_quota 表，旧列无代码引用）。用新增迁移而非改基线。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 3 迁移文件 | 20260810000100/002/003 就位 | ✅ |
| 依赖顺序 | 001 < 002 < 003（plan_quota_limits→resource_quota_meta） | ✅ |
| plan_id NOT NULL 分步 | 可空→回填 starter→SET NOT NULL，存量行不报错 | ✅ |
| starter 套餐 seed | 固定 UUID + 8 维度限额 | ✅ |
| 旧列清理 | 003 DROP IF EXISTS 幂等 | ✅ |
| 授权范围 | 仅配额基础表给 ani_app_user；套餐表不授权 | ✅ |
| whitespace | `git diff --check` → ✅ | ✅ |
| review-it | NOT NULL 分步、合法 UUID、无冗余索引、依赖/幂等收敛 → 迁移层无待修复缺陷 | ✅ |

## 验证命令

```bash
cd repo
git diff --check
# SQL 由 applier 依次执行 001 → 002 → 003
```

## 边界声明

- 本 Issue 只做数据库结构与 seed，不涉及任何 API/服务端实现。
- RLS 平台旁路策略的运行时触发方式需在 issue-008（Core 配额客户端）确认，非建表阻断。
- starter 限额取值与 `total=0`（硬上限）语义见 batch note-it Open Questions 待业务确认。
