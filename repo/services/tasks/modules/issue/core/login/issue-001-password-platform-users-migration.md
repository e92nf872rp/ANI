# Issue 001: users 表扩展支持平台管理员 + refresh_tokens 索引补全

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: N/A（数据库迁移，无 UI）
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

调整数据库 schema，为账号密码登录功能提供数据存储基础：

1. `20260707_014_platform_users.sql`：修改 `users` 表以支持平台管理员存储（`tenant_id IS NULL`），调整 UNIQUE 约束，并补 `refresh_tokens` 缺失的 `user_id` 索引
2. `20260707_014_platform_refresh_tokens.sql`：放宽 `refresh_tokens.tenant_id` 为 NULLABLE，并重建 `refresh_tokens.user_id` FK 到 `users(id)`

**2026-07-15 决策**：取消原计划的独立 `platform_users` 表，改为平台管理员复用 `users` 表，由 `tenant_id IS NULL` 谓词区分。平台管理员角色通过 `user_roles` + `roles`（`roles.tenant_id IS NULL` 表示平台内置角色）映射，与现有 RBAC 基础设施一致。

`users.password_hash` 列已在 `20260501_001_init_schema.sql` line 55 中存在（允许 NULL，兼容 OIDC 用户），本 Issue 不变更。`roles.tenant_id IS NULL` 表示平台内置角色的约定也已在 init_schema line 68 既有。

**不包含**：`password_history` 表（密码修改/重置功能不在本 Issues 范围）。

## Scope
- Product line: core
- Code paths allowed:
  - `repo/deploy/migrations/`（修改迁移文件）
  - 不改 init_schema，仅追加新迁移

## Acceptance Criteria

### 迁移文件 1：users 表扩展 + refresh_tokens 索引

- [ ] 修改文件 `repo/deploy/migrations/20260707_014_platform_users.sql`
- [ ] `ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_fkey`（删除 tenant_id FK，允许平台管理员无租户）
- [ ] `ALTER TABLE users ALTER COLUMN tenant_id DROP NOT NULL`（放宽为 NULLABLE）
- [ ] 删除既有 `UNIQUE (tenant_id, username)` 与 `UNIQUE (tenant_id, email)` 约束
- [ ] 创建 `idx_users_tenant_username` UNIQUE INDEX ON `users(tenant_id, username)`（租户用户按租户内唯一）
- [ ] 创建 `idx_users_tenant_email` UNIQUE INDEX ON `users(tenant_id, email)`（租户用户按租户内唯一）
- [ ] 创建 `idx_users_platform_username` UNIQUE INDEX ON `users(username) WHERE tenant_id IS NULL`（平台管理员全局唯一）
- [ ] 创建 `idx_users_platform_email` UNIQUE INDEX ON `users(email) WHERE tenant_id IS NULL`（平台管理员全局唯一）
- [ ] `CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id)`（补缺失索引）

### 迁移文件 2：refresh_tokens 放宽 + FK 重建

- [ ] 修改文件 `repo/deploy/migrations/20260707_014_platform_refresh_tokens.sql`
- [ ] `ALTER TABLE refresh_tokens ALTER COLUMN tenant_id DROP NOT NULL`（平台 refresh token tenant_id=NULL）
- [ ] 重建 `refresh_tokens.user_id` FK 到 `users(id) ON DELETE CASCADE`（平台管理员现在也在 users 表，FK 可成立）

### 向后兼容

- [ ] `users.password_hash` 既有列不动（init_schema line 55，允许 NULL）
- [ ] 既有 OIDC 用户 `password_hash` 仍为 NULL，不受影响
- [ ] 既有租户用户数据在 `idx_users_tenant_username` / `idx_users_tenant_email` 下保持原 UNIQUE 语义
- [ ] `roles` 表 `tenant_id IS NULL` 表示平台内置角色的约定既有（init_schema line 68），本迁移不动
- [ ] `user_roles` 关联表既有（init_schema line 74-79），本迁移不动

### 回滚

- [ ] 验证回滚 SQL 可执行（见各迁移文件末尾 Rollback 段）

### 测试

- [ ] 迁移在本地 PostgreSQL 执行成功
- [ ] 回滚在本地 PostgreSQL 执行成功
- [ ] `make test` 通过（若有迁移测试）
- [ ] `make validate-architecture` 通过

## Dependencies
None（基础迁移，无前置依赖）

## Type
core（数据库迁移）

## Priority
high

## References
- SPEC: §2.4 File Structure、§3.1 Schema Changes、§3.3 Relationships、§3.4 Migration Plan、§11.3 Assumptions
- ANI-08-安全架构设计.md：bcrypt cost≥12、密码哈希存储规范
- init_schema: `repo/deploy/migrations/20260501_001_init_schema.sql` line 50-63 (users)、line 66-72 (roles)、line 74-79 (user_roles)、line 108-120 (refresh_tokens)

## Notes
- **平台用户存储决策**（2026-07-15 修订）：取消独立 `platform_users` 表，改为平台管理员复用 `users` 表，由 `tenant_id IS NULL` 谓词区分。平台管理员角色通过 `user_roles` + `roles`（`roles.tenant_id IS NULL`）映射，与现有 RBAC 基础设施一致。比原方案更简洁，减少冗余表和密码字段重复。
- `users.password_hash` 允许 NULL（兼容 OIDC 用户）；平台管理员必须通过应用层或安装器初始化时设置非空 bcrypt hash，空 hash 在登录时被防御性拒绝
- 本 Issue 是后续账密登录、平台账密登录的基础依赖
- 迁移文件命名遵循 `YYYYMMDD_NNN_description.sql` 格式，保留原文件名以维持迁移历史
- **不包含**：`password_history` 表（密码修改/重置功能不在本 Issues 范围）
