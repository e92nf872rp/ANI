# PRD: 平台账户管理 (new)

> 来源：`repo/services/tasks/modules/plan/tenant/租户管理plan v3.0.md` §4.1.5 / §5.5

## 1. Introduction

为 BOSS 平台提供 platform-admin / platform-ops / platform-readonly 三类平台账号的 CRUD、角色修改、密码重置、禁用/启用、软删除，以及最后管理员保护。完全复用 `users` 表（tenant_id IS NULL）+ `roles` + `user_roles`，不新建表。

平台角色 4 维度权限模型（tenant_ops / resource_pool / platform_user / audit_export）：产品展示与前端交互语义，由 `GET /roles` 端点的静态映射呈现。platform-admin 为超级管理员（permissions=`[{"resource":"*","actions":["*"],"scope":"platform"}]`，不受 4 维度约束）。实际 API 访问授权由 `roles.permissions` 种子数据决定（auth-service V2 授权直查该 JSONB，按 resource/action 匹配）；4 维度矩阵与 DB 种子分属展示层与授权层（见 SPEC §3.1.2 修正）。

## 2. Goals

- 平台账号创建（指定 role + 密码，bcrypt cost=12）
- 平台账号列表/详情/权限查询
- 修改角色、重置密码、禁用/启用、软删除
- 最后管理员保护（活跃 platform-admin ≤ 1 时禁止删除/禁用）

## 3. User Stories

### US-001: 创建平台账号
**Description:** As platform-admin，我需要创建平台运营账号并指定角色与初始密码。
**Acceptance Criteria:**
- [ ] POST `/api/v1/svc/platform-admins`，入参 email/username/display_name/role/password，需 Idempotency-Key
- [ ] email RFC 5322，全局唯一（idx_users_platform_email）
- [ ] username 1-64 字符，全局唯一（idx_users_platform_username）
- [ ] display_name 1-128 字符
- [ ] role 为 platform-admin/ops/readonly 之一
- [ ] password 8-64 字符，至少 3 类（大写/小写/数字/特殊字符）
- [ ] 创建平台账号（tenant_id IS NULL），绑定指定平台角色
- [ ] 密码由调用方提供（非系统生成），bcrypt(cost=12) 写入 password_hash，明文不写入日志/审计/响应，不返回 temporary_password
- [ ] 响应 200 `{ id, message: "platform admin created" }`，写审计日志
- [ ] Typecheck/lint passes

### US-002: 查询平台账号列表
**Description:** As platform-admin，我需要分页查询平台账号列表并按角色/状态/来源/关键字过滤。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/platform-admins`，支持 limit/cursor/role/status/source/search
- [ ] 游标分页（limit + cursor），返回 CursorPage（items + next_cursor），按 created_at DESC 排序
- [ ] role 过滤：platform-admin / platform-ops / platform-readonly
- [ ] status 过滤：active / disabled（直接对应 users.status，不含 invited）
- [ ] source 过滤：local / oidc（按 username 前缀 `local:` / `oidc:` 过滤）
- [ ] search 模糊匹配 email / username
- [ ] items 每项含 id/username/display_name/role/status/source/last_login_at
- [ ] source 推断：username 以 oidc: 开头 → third_party，以 local: 开头 → local
- [ ] Typecheck/lint passes

### US-003: 查询平台账号详情
**Description:** As platform-admin，我需要查看账号详情。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/platform-admins/{userId}` 返回用户所有相关信息
- [ ] 返回字段：id/email/username/display_name/role/status/source/last_login_at/created_at
- [ ] source 推断：username 以 oidc: 开头 → third_party，以 local: 开头 → local
- [ ] Typecheck/lint passes

### US-004: 修改平台账号角色
**Description:** As platform-admin，我需要修改账号角色。
**Acceptance Criteria:**
- [ ] PUT `/api/v1/svc/platform-admins/{userId}/role`，入参 role
- [ ] 先 DELETE 旧平台角色，再 INSERT 新角色
- [ ] 不支持修改用户名，不支持直接改 status
- [ ] 响应 200 `{ id, message }`，需 Idempotency-Key，写审计日志
- [ ] Typecheck/lint passes

### US-005: 重置平台账号密码
**Description:** As platform-admin，我需要为平台账号重置密码。
**Acceptance Criteria:**
- [ ] POST `/api/v1/svc/platform-admins/{userId}/reset-password`，入参 new_password
- [ ] bcrypt(cost=12)，与旧密码相同 → 422 `PASSWORD_SAME_AS_OLD`
- [ ] 响应 200 `{ id, message }`，需 Idempotency-Key，写审计日志
- [ ] Typecheck/lint passes

### US-006: 禁用/启用/删除平台账号
**Description:** As platform-admin，我需要禁用、启用或软删除平台账号。
**Acceptance Criteria:**
- [ ] POST `.../disable` → status='disabled'；POST `.../enable` → status='active'
- [ ] DELETE `/api/v1/svc/platform-admins/{userId}` 软删除（is_deleted + deleted_at + status='disabled'）
- [ ] 删除/禁用前执行最后管理员保护：活跃 platform-admin 数 ≤ 0（排除当前目标）→ 422 `LAST_PLATFORM_ADMIN`
- [ ] 响应 200 `{ id, message }`，写审计日志
- [ ] Typecheck/lint passes

### US-007: 查询可变更的平台角色列表
**Description:** As platform-admin，我需要查询可分配的平台角色及其权限矩阵，用于创建/修改角色时的下拉选项与权限预览。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/platform-admins/roles` 返回可变更的平台角色列表
- [ ] 返回 items，每项含 name（platform-admin / platform-ops / platform-readonly）/ permissions（4 维度：tenant_ops / resource_pool / platform_user / audit_export，值 read / write / none）/ description
- [ ] platform-admin 权限为全 write（超级管理员，permissions=`["*"]`）
- [ ] platform-ops：tenant_ops=write / resource_pool=write / platform_user=none / audit_export=none
- [ ] platform-readonly：tenant_ops=read / resource_pool=read / platform_user=none / audit_export=read
- [ ] 不写审计日志，不调用 Core API
- [ ] Typecheck/lint passes

### US-008: 查询平台账号操作历史
**Description:** As platform-admin，我需要分页查看指定平台账号的操作历史，用于审计追溯。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/platform-admins/{userId}/audit-logs`，支持 limit/cursor 游标分页
- [ ] 查询 `audit_logs` 表中 `user_id = $userId AND tenant_id IS NULL` 的记录，按 created_at DESC 排序
- [ ] 返回 CursorPage：items[]{id, action, resource, result, details, created_at} + next_cursor
- [ ] 支持 action 过滤（如 platform_admin.create / platform_admin.change_role / platform_admin.reset_password / platform_admin.disable / platform_admin.enable / platform_admin.delete）
- [ ] 支持 result 过滤（success / failed）
- [ ] 不调用 Core API，不写审计日志
- [ ] Typecheck/lint passes

## 4. Functional Requirements

- FR-1: 系统必须支持平台账号创建（tenant_id IS NULL），email/username 全局唯一，密码由调用方提供 bcrypt(cost=12)
- FR-2: 系统必须支持列表/详情查询，详情返回完整字段（含 email/created_at）
- FR-3: 系统必须支持可变更角色列表查询（platform-admin/ops/readonly 及其 4 维度权限矩阵）
- FR-4: 系统必须支持角色修改（先 DELETE 旧角色再 INSERT 新角色）、密码重置（校验与旧不同）
- FR-5: 系统必须支持禁用/启用/软删除，删除/禁用前执行最后管理员保护
- FR-6: 系统必须支持查询指定平台账号的操作历史（audit_logs，按 created_at DESC 排序，支持 action/result 过滤）
- FR-7: platform-admin 拥有 ["*"] 超级权限，不受 4 维度约束
- FR-8: 平台角色权限矩阵：platform-admin 全 write、platform-ops tenant_ops+resource_pool write、platform-readonly 全 read

## 5. Non-Goals

- 不新建 platform_admins 表（复用 users 表 tenant_id IS NULL）
- 不支持平台账号邮件邀请注册（仅由 platform-admin 直接创建）
- 不支持修改用户名
- 不实现平台账号 SSO/OIDC 集成

## 6. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core + services + boss（跨三层） |
| Code scope | `repo/api/openapi/v1.yaml` + `repo/pkg/` + `repo/services/ani-gateway/` + `repo/sdks/core/` + `repo/api/openapi/services/v1.yaml` + `repo/services/tenant-service/` + `repo/frontends/boss/src/` |
| OpenAPI authority | Core `v1.yaml` 新增 `/admin/platform-users/*` + Services `v1.yaml` 新增 `/svc/platform-admins/*`，均须先改 |
| Frozen exclusions | Services 层不得 import `pkg/ports`/`pkg/adapters`；不新建表；不做邮件邀请 |
| idempotency_key | required on: POST, PUT role, reset-password, disable, enable, delete |
| Architecture gate | users/roles 操作下沉 Core 层（端口+适配器+网关 handler+SDK），Services 经 Core SDK 调用 |
| Module main doc | spec-new-boss-platform-admin.md |

## 7. 关联模块

- [PRD: 配额套餐](../tenant/prd-new-boss-tenant-quota-policy.md) — platform-admin/ops 有权管理套餐
- [PRD: 租户管理员](../tenant/prd-new-boss-tenant-admin.md) — platform-admin/ops 有权管理租户管理员
- [PRD: 租户列表](../tenant/prd-new-boss-tenant-list.md) — platform-admin/ops 有权创建/冻结/禁用租户
