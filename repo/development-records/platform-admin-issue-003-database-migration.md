# PLATFORM-ADMIN-ISSUE-03：roles 表平台角色种子迁移（platform-ops / platform-readonly）

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #3）
> **完成日期：** 2026-08-31（本地未提交：迁移文件为工作区新增/暂存，含 review-it 修复）
> **Scope：** `repo/deploy/migrations/20260831000100_platform_roles_seed.sql`（新增）、`repo/deploy/migrations/atlas.sum`（更新）、本批连带修复的文档（issue-003 / SPEC §3.1.2 §3.4 / PRD §1）
> **依赖：** `20260502000300_permissions_schema.sql`（roles_permissions_schema CHECK + platform-admin 种子先例）
> **Product line：** core（纯 DB 迁移）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-003-database-migration.md`

## 交付内容

单一迁移文件，向 `roles` 表插入两个平台内置角色种子（tenant_id NULL），**无任何其他 DDL**（users 列扩展、邮箱唯一索引、audit_logs 分区均已由其他批次迁移覆盖，不在本 Issue 范围——用户 2026-08-31 指示"其余的已经实现，只需要添加两个 role"）。

### 种子数据

| Role | ID | permissions |
|---|---|---|
| platform-ops | `00000000-0000-0000-0000-000000000006` | tenants `["*"]` / resource_pool `["*"]` / metering `["read","list"]`（scope 均 platform） |
| platform-readonly | `00000000-0000-0000-0000-000000000007` | tenants / resource_pool / metering 均 `["read","list"]` |

`platform-admin`（`...0001`，`20260502000300` 已 seed）**不重复 seed**。

### 幂等写法（双保险）

```sql
INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '...0006', NULL, 'platform-ops', '...'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-ops'
)
ON CONFLICT (id) DO UPDATE
SET permissions = EXCLUDED.permissions, name = EXCLUDED.name;
```

## Design Decisions

1. **种子 resource 维度取 `tenants` / `resource_pool` / `metering`，而非 SPEC §3.1.2 早期草稿的 `tenant_ops` / `platform_user` / `audit_export` 四维**（本批最核心决策，源于 review-it 运行时证据核实）：
   - **Ambiguity：** SPEC §3.1.2 草稿与 issue AC 原稿都写了四维种子 SQL，但没有人验证过这些维度能否被运行时授权消费。
   - **Choice：** review-it 期间核实 [auth-service permissions.go](file:///d:/Jczn/project/ANI/ANI/repo/services/auth-service/internal/service/permissions.go) 的 V2 授权**直查 `roles.permissions` JSONB 作为权威数据**（`permissionStore.userPermissions`，按 resource 精确字符串匹配或 `*`，scope 须 platform）——种子就是运行时授权数据，不是展示数据。而 `tenant_ops`/`platform_user`/`audit_export` 既不在 `20260502000300` 头部有效资源枚举中，也无任何路由授权路径（v1.yaml 实际路由 scope 为 `scope:tenant:*`、`scope:metering:*`），且草稿的 `"actions":[]` 写法本身违反 CHECK。
   - **Rationale：** 种子若用无效维度，platform-ops/readonly 账号在 V2 授权下将被 deny 一切。`tenants` 恰与 Gateway legacy 授权从 `/admin/tenants/*` URL 推导的 resource 名一致，`metering` 与 `/metering/*` 一致，两代授权链路都能消费。
2. **「无权限」用省略条目表达**：`roles_permissions_schema` CHECK 要求每条目 `actions` 为非空数组，`"actions":[]` 会被拒。platform-ops 对平台账号管理无权限 = 不写 `users` 条目（读 `user_permissions` 得不到该 resource 即 deny）。
3. **固定 UUID `...0006` / `...0007`**：延续 `20260502000300` 的 `...0001`~`...0004` 平台内置角色 ID 段；已核实全迁移目录无占用。
4. **文件名 `20260831000100`（Atlas 14 位）**：SPEC 示例 `20260818_015` 与 Issue 草稿 `20260831_001` 均不符 Atlas 版本号约定；按目录最新 `20260828000200` 顺延。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| SPEC §3.1.2 草稿种子 SQL（四维 + `"actions":[]` + `ON CONFLICT (tenant_id, name)`） | 三处不可用，全部改写：① 维度改 `tenants`/`resource_pool`/`metering`（见 Design Decision 1）；② 空条目改省略；③ `ON CONFLICT (tenant_id, name)` 对 NULL tenant_id 不生效，改双保险写法。**SPEC 已同步修正**（§3.1.2 加「实现修正」块 + §3.4 Migration Plan 引用实际文件名） | 运行时授权代码与 CHECK 约束的硬性要求；review-it F2 裁定「迁移是对的，SPEC 草稿才是错的」 |
| PRD §1「实际权限由 Gateway AuthZ 中间件按角色名硬编码校验」 | 修正为「实际 API 访问授权由 `roles.permissions` 种子数据决定（auth-service V2 直查该 JSONB）」 | PRD 原断言与授权实现不符；已同步修正 PRD，并注明四维矩阵属展示层语义、DB 种子属授权层，两层并存 |
| Issue 草稿「platform-readonly 种子含 users 资源 read+list」 | 最终种子**不含** `users` 条目（readonly 对平台账号无任何权限） | PRD FR-8 readonly `platform_user: none`，与"仅可查看租户、资源池与审计导出"一致；无权限条目直接省略 |
| 初版实现 `ON CONFLICT (id) DO UPDATE` 单保险 | review-it 后加 `WHERE NOT EXISTS (tenant_id IS NULL AND name = ...)` 双保险（同 `20260502000300` 先例） | **F1（accepted，中危）**：NULL tenant_id 使 UNIQUE(tenant_id,name) 不去重，`ON CONFLICT (id)` 只能拦同 id 重放，拦不住手工建库/半迁移状态下的同名异 id 行，会产生重复角色行 |
| 初版 Rollback 注释按 name 删 | 改为按固定 id 删（`WHERE id IN ('...0006','...0007')`） | **F6（minor）**：按 name 删会连带误删手工建的同名行 |
| SPEC §3.4 Migration Plan「Step 1 users 列扩展 / Step 3 邮箱唯一索引」 | 本 Issue **不含**（范围收窄） | 用户确认列与索引已由其他批次迁移实现（`idx_users_platform_email` 在 `20260707001400_platform_users.sql`）；SPEC §3.4 已标注「Step 2 已实现；Step 1/3 已由其他批次覆盖」 |

## Tradeoffs

- **迁移维度对齐运行时授权（tenants/metering）vs 对齐 SPEC 草稿（tenant_ops/audit_export）**：前者胜出——种子是授权数据不是展示数据。代价：SPEC §3.2.3 的 `GET /roles` 静态四维矩阵与 DB 种子是**两层不同表示**（展示层 vs 授权层），需要文档明确区分（已在 SPEC §3.1.2 修正块与 PRD §1 写明），避免后人再"统一"它们。
- **`resource_pool` 条目保留 vs 删除**：`resource_pool` 不在 `20260502000300` 头部有效资源枚举、当前无对应路由（review F4）。保留：面向资源池模块的预留，CHECK 只校验类型不校验枚举，语法通过，无害；删除会让 platform-ops/readonly 在资源池路由上线时权限真空。**结论：保留（F4 rejected as defect）**。
- **双保险 `WHERE NOT EXISTS` + `ON CONFLICT (id)` vs 仅其一**：双保险多 2 行 SQL，但覆盖两类异常路径（同名异 id 不插入；同 id 重放收敛种子）。`20260502000300` 先例同款。
- **`DO UPDATE` vs `DO NOTHING`**：选 DO UPDATE（F5 consciously accepted）——重放时把人工对种子的改动收敛回权威值，与 issue AC「保证种子权威」一致；Atlas 常规流程每版本只执行一次，实际重放风险低。


## Verification

```bash
# 本会话已做（静态）：
#   - 通读 20260831000100 全文 + 20260502000300 CHECK/种子先例 + 20260707001400 NULL tenant_id 处理
#   - 核实固定 ID ...0006/...0007 全迁移目录唯一（Grep 无占用）
#   - 核实 auth-service V2 授权直查 roles.permissions（permissions.go）——种子维度决策的运行时依据
#   - 核实 v1.yaml 路由 scope（scope:tenant:* / scope:metering:*）——无 tenant_ops/audit_export 路径
#   - git status：迁移文件 staged（A）、atlas.sum 已修改（M）

# 待非沙箱环境执行（Open Questions #3/#4）：
cd repo
atlas migrate hash --dir file://deploy/migrations      # 文件修复后重算 atlas.sum（必须）
make db-validate                                        # = atlas migrate validate
atlas migrate apply --dir file://deploy/migrations --url $DATABASE_URL
# 幂等复跑一次 → 应 0 行变更
psql $DATABASE_URL -c "SELECT name FROM roles WHERE tenant_id IS NULL AND name IN ('platform-admin','platform-ops','platform-readonly');"
# 期望 3 行；platform-admin permissions 不变
```

## 边界声明

- 本批次为**纯 DB 种子迁移**：无表结构变更、无索引、无 RLS/GRANT、无任何 Go 代码改动。
- users 列扩展（display_name/is_deleted/deleted_at）、idx_users_platform_email、audit_logs 分区均已存在于其他迁移，本 Issue 不含（用户确认收窄范围）。
- 运行时消费方（auth-service V2 授权读 roles.permissions）为既有代码，本批不改；但种子维度选择以该消费方的匹配规则为准。
- 文档连带修复：issue-003 本身、SPEC §3.1.2/§3.4、PRD §1 已同步本批结论；atlas.sum 重算与 DB apply 在非沙箱环境完成前，本迁移**未达 done**。
