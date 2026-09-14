# 数据库迁移文件

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
新增迁移文件 `repo/deploy/migrations/20260831000100_platform_roles_seed.sql`：**仅**向 `roles` 表插入两个平台内置角色种子 `platform-ops` / `platform-readonly`（tenant_id NULL）。无列扩展、无索引变更、无分区变更、无表/RLS/GRANT 变更（users 表所需 `display_name` / `is_deleted` / `deleted_at` 及 audit_logs 分区已由其他批次实现，不在本 Issue 范围）。

**与 SPEC §3.1/§3.4 的差异（依据当前迁移文件与运行时授权代码核实）：**
1. **文件名**：SPEC 示例 `20260818_015` 与 Atlas 14 位版本号约定不符；按目录最新 `20260828000200` 之后使用 `20260831000100`。
2. **roles 种子维度用 003 有效资源而非 SPEC 草稿四维**：SPEC 草稿 `tenant_ops`/`platform_user`/`audit_export` 不在 `20260502000300` 头部有效资源枚举中、无对应路由授权路径，且其 `"actions":[]` 写法违反 `roles_permissions_schema` CHECK（要求非空数组）。auth-service V2 授权直查 `roles.permissions`（`permissionStore.userPermissions`）按 resource 精确匹配——种子就是运行时权威数据。故维度取 `tenants`（对应 `/admin/tenants/*` 路由）/ `resource_pool` / `metering`（对应 `/metering/*` 路由）；「无权限」用**省略条目**表达。SPEC §3.2.3 的 `tenant_ops/resource_pool/platform_user/audit_export` 四维矩阵是 `GET /roles` 的**静态展示映射**（Services 内置，不查 DB），与 DB 种子是两层不同的表示，均保留。
3. **范围收窄**：仅 role 种子。users 列扩展、邮箱唯一索引、audit_logs 分区均不在本 Issue 范围（用户指示"其余的已实现"；`idx_users_platform_email` 亦已存在于 `20260707001400_platform_users.sql`）。
4. **幂等双保险**：`UNIQUE (tenant_id, name)` 对 `tenant_id IS NULL` 不按重复处理（见 `20260502000300` 注释），`ON CONFLICT (id)` 只能拦截同 id 重放、拦不住同名异 id 行（手工建库/半迁移状态）。故 `INSERT ... SELECT ... WHERE NOT EXISTS (tenant_id IS NULL AND name = ...)` + `ON CONFLICT (id) DO UPDATE` 双保险（同 `20260502000300` 先例）。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/migrations/`

## Acceptance Criteria
- [ ] 新增 `repo/deploy/migrations/20260831000100_platform_roles_seed.sql`（Atlas 14 位版本号；不写 `BEGIN`/`COMMIT`——Atlas 按文件包裹事务；头部含 Description / Depends on / Rationale；尾部含 Rollback 注释块）
- [ ] `INSERT INTO roles` 种子 `platform-ops`：id `00000000-0000-0000-0000-000000000006`，tenant_id NULL，permissions `[{"resource":"tenants","actions":["*"],"scope":"platform"},{"resource":"resource_pool","actions":["*"],"scope":"platform"},{"resource":"metering","actions":["read","list"],"scope":"platform"}]`；写法 `INSERT ... SELECT ... WHERE NOT EXISTS (tenant_id IS NULL AND name='platform-ops')` + `ON CONFLICT (id) DO UPDATE SET permissions = EXCLUDED.permissions`（幂等双保险：前者拦同名异 id，后者对固定 id 重放收敛种子）
- [ ] `INSERT INTO roles` 种子 `platform-readonly`：id `00000000-0000-0000-0000-000000000007`，tenant_id NULL，permissions `[{"resource":"tenants","actions":["read","list"],"scope":"platform"},{"resource":"resource_pool","actions":["read","list"],"scope":"platform"},{"resource":"metering","actions":["read","list"],"scope":"platform"}]`，同上双保险写法
- [ ] 种子 JSON 通过 `roles_permissions_schema` CHECK（每条目 resource:string + actions 非空数组 + scope∈{tenant,own,platform}）；不出现 `"actions":[]`
- [ ] 固定 ID `...0006` / `...0007` 未被现有迁移占用（`20260502000300` 用 `...0001`~`...0004`）；`platform-admin`（`...0001`）不重复 seed
- [ ] 不含任何其他 DDL（无 ALTER TABLE / CREATE INDEX / CREATE TABLE / RLS / GRANT）
- [ ] 更新 `repo/deploy/migrations/atlas.sum`（`atlas migrate hash`）且 `atlas migrate validate` 通过
- [ ] `psql $DATABASE_URL -f ...` 或 `atlas migrate apply` 执行成功（本地有 DB 时）；重复执行验证幂等
- [ ] 验证种子：`SELECT name FROM roles WHERE tenant_id IS NULL AND name IN ('platform-admin','platform-ops','platform-readonly')` 返回 3 行；`platform-admin` permissions 保持 `[{"resource":"*","actions":["*"],"scope":"platform"}]` 不变
- [ ] PR 描述含迁移步骤 + rollback 说明 + 与 SPEC 的差异说明

## Rollback（写入迁移尾部注释）
- `DELETE FROM roles WHERE id IN ('00000000-0000-0000-0000-000000000006','00000000-0000-0000-0000-000000000007')`（按固定 id 删除，不按 name，避免误删手工建的同名行；先确认无 user_roles 引用）

## Dependencies
None

## Type
backend / infra

## Priority
high

## References
- SPEC: §3.1.2 平台运营角色种子 / §3.4 Migration Plan（本 Issue 按上述差异修正）
- 现有迁移依据：
  - `20260502000300_permissions_schema.sql` — `roles_permissions_schema` CHECK + 平台角色种子先例（`...0001`~`...0004`）
  - `20260707001400_platform_users.sql` — users.tenant_id NULLABLE（平台账号存于 users，tenant_id IS NULL）
  - `20260828000200_app_role_privileges.sql` — 迁移编号序列参考（本文件接续为 `20260831000100`）
