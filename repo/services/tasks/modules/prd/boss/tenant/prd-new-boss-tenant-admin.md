# PRD: 租户管理员管理 (new)

> 来源：`repo/services/tasks/modules/plan/tenant/租户管理plan v3.0.md` §4.1.4 / §5.4

## 1. Introduction

为 BOSS 平台提供租户管理员的全生命周期管理，包括邀请（生成 token 链接）、重发邀请、权限修改、密码重置、禁用/启用、软删除，以及指定管理员权限查询。角色管理复用现有 `users` + `roles` + `user_roles` 三张表；邀请生命周期由新增 `tenant_admin_invitation` 表独立承载，不改 `users.status`、不预绑角色。

租户角色保留 tenant-admin / user / auditor；permissions 沿用已有的 resource/action/scope JSONB 数组格式（见 migration 003），不引入新维度模型。

> 配额变更申请提交、查询与审批属于 [租户列表 PRD](./prd-new-boss-tenant-list.md) 范围。

## 2. Goals

- 跨租户管理员列表查询（租户内管理员列表 `GET /tenants/{tenantId}/admins` 归租户列表 PRD）
- 邀请管理员（邮箱+用户名共同在租户内匹配现有用户，新建 `tenant_admin_invitation` 记录并生成 token 链接，不绑角色、不改 users.status）
- 重发邀请/邀请状态跟进（inviting/accepted/rejected/expired）
- 权限修改、密码重置、禁用/启用、软删除
- 查询指定管理员权限与操作历史查询

## 3. User Stories

### US-001: 邀请租户管理员
**Description:** As platform-admin/ops，我需要通过邮箱+用户名在租户内匹配现有用户并发送管理员邀请（生成邀请 token 链接）。
**Acceptance Criteria:**
- [ ] POST `/api/v1/svc/tenants/{tenantId}/admins/invite`，入参 email+username（共同判定），需 Idempotency-Key（body）
- [ ] 在指定租户（tenant_id=tenantId）内匹配现有用户，无匹配 404 `TENANT_ADMIN_NOT_FOUND`（不新建用户）
- [ ] 匹配成功则新建一条 `tenant_admin_invitation` 记录（`status='inviting'`、`token_hash=SHA-256(token)`、`expire_at=now()+72h`），**不改变用户角色、不改 `users.status`**（保持 active/disabled）
- [ ] 已是本租户 tenant-admin → 409 `TENANT_ADMIN_ALREADY_ADMIN`
- [ ] 该用户在本租户下已存在 status='inviting' 的邀请 → 409 `TENANT_INVITATION_PENDING`（应改用重发邀请）
- [ ] 响应 200 `{ id, token, expire_at, message }`；token 为原始邀请 token（仅本次返回一次，库中仅存 token_hash），触发通知渠道拼接为邀请链接发送
- [ ] 写审计日志 `tenant_admin.invite`
- [ ] Typecheck/lint passes

### US-002: 重发租户管理员邀请
**Description:** As platform-admin/ops，我需要重发某用户在本租户下的管理员邀请（用于邀请未接受/过期场景）。
**Acceptance Criteria:**
- [ ] POST `/api/v1/svc/tenants/{tenantId}/admins/{userId}/invitation/resend`，需 Idempotency-Key（body）
- [ ] 仅对最新一条状态为 `inviting` 或 `expired` 的 `tenant_admin_invitation` 记录允许重发；重发时重新生成 token（更新 token_hash）、刷新 `expire_at=now()+72h`、清空 accepted_at/rejected_at、状态回归 `inviting`
- [ ] 该租户内不存在该用户或 `tenantId+userId` 组合无匹配记录 → 404 `TENANT_ADMIN_INVITATION_NOT_FOUND`
- [ ] 最新邀请已 accepted/rejected（终态）→ 409 `TENANT_INVITATION_SETTLED`（不可重发）
- [ ] 响应 200 `{ id, token, expire_at, message }`；token 仅本次返回一次
- [ ] 写审计日志 `tenant_admin.resend_invitation`
- [ ] Typecheck/lint passes

### US-003: 跨租户查询所有管理员
**Description:** As platform-admin/ops/readonly，我需要跨所有租户游标分页查询管理员列表。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenant-admins`，支持 limit（默认 20，最大 100）/cursor/tenant_id（可选）/status/is_inviting/is_expired/search/role/source，响应 CursorPage（items + next_cursor）
- [ ] JOIN users + user_roles + roles + tenants，WHERE is_deleted=FALSE；传入可选 `tenant_id` 时按该租户过滤（返回该项的 role/status 等结果仅为该租户内的绑定）
- [ ] **仅返回租户管理员与正在被邀请或邀请已过期的用户**；普通成员（role='user'）默认不返回，仅当该用户正在被邀请（`tenant_admin_invitation.status='inviting'`）或邀请已过期（`tenant_admin_invitation.status='expired'`）时才出现在列表，便于重发邀请
- [ ] items 每项含 id/email/username/display_name/role/status/is_inviting/is_expired/source/last_login_at/tenant{id,name,display_name}；`is_inviting` 为该用户是否正在被邀请（仅 `status='inviting'` 为 true，已过期为 false）；`is_expired` 为该用户是否有已过期邀请（`status='expired'` 为 true）；两者仅作标记，不影响 role/status——邀请中或已过期用户仍展示其在该租户内原有的角色（可为 user）
- [ ] source 推断：oidc: → third_party，local: → local
- [ ] Typecheck/lint passes

### US-004: 查询管理员详情
**Description:** As platform-admin/ops/readonly，我需要查看某管理员的详细信息（返回该用户的所有用户信息，含用户全字段与所属租户对象）。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenants/{tenantId}/admins/{userId}` 返回该用户**所有用户信息**（不含 `password_hash` 等敏感字段）：id/username/email/display_name/role/status/source/last_login_at/created_at/updated_at/is_inviting，以及 tenant 对象（id/name/display_name）
- [ ] `is_inviting` 为该用户是否正在被邀请（该租户下存在 `tenant_admin_invitation.status='inviting'` 为 true，否则 false），仅作标记，便于详情页识别邀请态
- [ ] `source` 同跨租户推断：username 以 `oidc:` 开头 → `third_party`，以 `local:` 开头 → `local`
- [ ] `role` 由 user_roles+roles 解析（tenant-admin / user / auditor）
- [ ] Typecheck/lint passes

### US-005: 修改管理员权限
**Description:** As platform-admin/ops，我需要修改管理员角色。
**Acceptance Criteria:**
- [ ] PUT `/api/v1/svc/tenants/{tenantId}/admins/{userId}/role`，入参 `role_id`（UUID），须为可分配角色（非 platform-*、非 tenant-admin，tenant_id 为空或等于路径 tenantId）
- [ ] 非法 role_id → 422 `ROLE_CHANGE_INVALID`
- [ ] 响应 200 `{ id, message }`，需 Idempotency-Key，写审计日志 `tenant_admin.change_role`
- [ ] Typecheck/lint passes

### US-007: 重置管理员密码
**Description:** As platform-admin/ops，我需要为管理员重置密码。
**Acceptance Criteria:**
- [ ] POST `/api/v1/svc/tenants/{tenantId}/admins/{userId}/reset-password`，入参 new_password（明文 HTTPS）
- [ ] new_password 约束：8-64 字符，必须包含大写字母、小写字母、数字、特殊字符四类中的至少三类，且必须与旧密码不同
- [ ] bcrypt(cost=12)，明文不写入日志/审计/响应
- [ ] 与旧密码相同 → 422 `PASSWORD_SAME_AS_OLD`；复杂度不满足 → 400 `VALIDATION_FAILED`
- [ ] 已软删除用户不可重置密码 → 404 `TENANT_ADMIN_NOT_FOUND`（禁用态用户允许重置密码）
- [ ] 响应 200 `{ id, message }`，需 Idempotency-Key，写审计日志 `tenant_admin.reset_password`
- [ ] Typecheck/lint passes

### US-008: 禁用/启用/删除管理员
**Description:** As platform-admin/ops，我需要禁用、启用或软删除管理员。
**Acceptance Criteria:**
- [ ] POST `.../disable` → status='disabled'；POST `.../enable` → status='active'（两者需 Idempotency-Key）
- [ ] DELETE `.../admins/{userId}` 软删除（is_deleted=TRUE + deleted_at，**不改 status**）；**不做幂等，不需 Idempotency-Key**
- [ ] 不可重复禁用/启用（Core DB 层校验状态未变化 → 409 `USER_STATE_INVALID`）
- [ ] 响应 200 `{ id, message }`，写审计日志；disable → `tenant_admin.disable`、enable → `tenant_admin.enable`、delete → `tenant_admin.delete`
- [ ] Typecheck/lint passes

### US-009: 查询指定管理员角色与权限
**Description:** As platform-admin/ops，我需要查询指定管理员在指定租户内的角色和权限以控制 UI 显示。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenants/{tenantId}/admins/{userId}/role`（与 PUT role 同路径不同方法，GET 查询 / PUT 修改），tenantId+userId 通过路径参数传入
- [ ] JOIN user_roles + roles 查询指定用户在指定租户下的角色及 roles.permissions JSONB 直接返回
- [ ] 返回 `{ user_id, tenant_id, role, role_id, permissions[] }`（permissions 沿用已有 resource/action/scope JSONB 数组格式；role_id 为当前角色绑定 UUID，无绑定时省略）
- [ ] **仅能查询到租户成员的权限**：role ∈ tenant-admin / user / auditor（租户内 `tenant_id` 非空）
- [ ] **平台账户（tenant_id=null）权限不可通过本端点查询**（平台账号不参与租户权限模型，其权限由平台侧查询，不在本端点返回）
- [ ] 通过 Core SDK 调用 Core API 查询角色与权限
- [ ] Typecheck/lint passes

### US-010: 查询管理员操作历史
**Description:** As platform-admin/ops/readonly，我需要查看某管理员的操作历史。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenants/{tenantId}/admins/{userId}/audit-logs` 游标分页返回 limit（默认 20，最大 100）/cursor/action/result，响应 CursorPage（items + next_cursor）
- [ ] 查询 audit_logs WHERE tenant_id=tenantId AND details->>'target_id'=userId，支持 action / result（success / failure）过滤
- [ ] Typecheck/lint passes

### US-011: 查询可用租户列表
**Description:** As platform-admin/ops/readonly，我需要在邀请管理员时获取可用租户列表作为目标租户选择器数据源。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenant-admins/tenants`，无需分页参数，返回 `status <> 'disabled'` 的租户列表，按 `created_at DESC` 排序
- [ ] 网关通过 gRPC 转发至 tenant-service `ListAvailableTenants` RPC，与其他 TenantAdminService RPC 保持一致
- [ ] 响应 200 `{ items: [{ id, name, display_name, status }] }`，status ∈ active / frozen（不含 disabled）
- [ ] 幂等（GET 只读）
- [ ] Typecheck/lint passes

### US-012: 查询可变角色列表
**Description:** As platform-admin/ops/readonly，我需要在修改管理员角色时获取可变角色列表作为角色选择器数据源。
**Acceptance Criteria:**
- [ ] GET `/api/v1/svc/tenants/{tenantId}/roles`，无需分页参数，返回该租户可分配的角色列表
- [ ] 查询条件：`name` 前缀不为 `platform-`，且 `tenant_id` 为空或与传入 `tenantId` 相同
- [ ] 网关通过 gRPC 转发至 tenant-service，tenant-service 内部调用 Core SDK 获取角色列表
- [ ] 响应 200 `{ items: [{ id, name, tenant_id, permissions }] }`，不分页（tenant_id 为 null 表示平台内置角色；permissions 为 roles.permissions JSONB 原样返回）
- [ ] 只读端点，platform-admin / platform-ops / platform-readonly 可访问
- [ ] Typecheck/lint passes

## 4. Functional Requirements

- FR-1: 系统必须支持邀请管理员，邮箱+用户名共同在租户内匹配，无匹配不新建；邀请复用独立 `tenant_admin_invitation` 表（status=`inviting`/`accepted`/`rejected`/`expired`，token_hash 存 SHA-256、expire_at=now()+72h），不改变用户角色、不改 `users.status`
- FR-1a: 系统必须支持重发邀请（`POST .../invitation/resend`），对该用户最新一条邀请重新生成 token、刷新过期时间、状态回归 inviting；终态（accepted/rejected）不可重发（409 `TENANT_INVITATION_SETTLED`）
- FR-1b: 邀请可通过 token 链接被被邀请人接受或拒绝，邀请状态机：`inviting → accepted / rejected / expired`；仅 `inviting` 且未过期可被接受
- FR-2: 系统必须支持跨租户管理员列表查询（`GET /tenant-admins`），返回 tenant 对象、source 字段与 last_login_at（最近登录时间）；**仅返回租户管理员与正在被邀请或邀请已过期的用户（不含普通成员 user）**，正在被邀请（该租户下 `tenant_admin_invitation.status='inviting'`）的用户用 `is_inviting=true` 标记，邀请已过期（`tenant_admin_invitation.status='expired'`）的用户用 `is_inviting=false` 标记（仅作标记，不改变该用户 role/status，仍展示原有角色），便于重发邀请；租户内列表 `GET /tenants/{tenantId}/admins` 归租户列表 PRD
- FR-3: 系统必须支持权限修改（user/auditor/tenant-admin）、密码重置（bcrypt cost=12）
- FR-4: 系统必须支持禁用/启用/软删除
- FR-5: 系统必须支持查询指定管理员角色与权限（`GET /tenants/{tenantId}/admins/{userId}/role`，与 PUT role 同路径不同方法），返回已有 resource/action/scope 权限数组
- FR-6: 租户角色权限沿用已有的 resource/action/scope JSONB 数组格式（见 migration 003），不引入新维度模型
- FR-7: 系统必须支持查询可用租户列表（`GET /tenant-admins/tenants`），返回 `status <> 'disabled'` 的租户列表，按 `created_at DESC` 排序，不分页；用于邀请管理员时选择目标租户；网关通过 gRPC 转发至 tenant-service
- FR-8: 系统必须支持查询可变角色列表（`GET /tenants/{tenantId}/roles`），返回 `name` 前缀不为 `platform-` 且 `tenant_id` 为空或等于传入 `tenantId` 的角色列表，不分页；用于修改管理员角色时选择目标角色；网关通过 gRPC 转发至 tenant-service

## 5. Non-Goals

- 不新建 tenant_admins 表；角色绑定复用 users + roles + user_roles；仅新增 `tenant_admin_invitation` 表承载邀请生命周期
- 不实现 Console 端租户自助功能
- 不实现 SMTP/短信通道接入
- 不实现用户级 MFA enrollment（属 auth-service）
- 不实现配额变更申请提交/查询/审批（属租户列表 PRD 范围）
- 不实现租户内管理员列表查询 `GET /tenants/{tenantId}/admins`（属租户列表 PRD 范围）；本模块负责该前缀下的邀请/详情/权限/重置/禁用/启用/删除/操作历史等子路径

## 6. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | boss |
| Code scope | `repo/frontends/boss/src/` + `repo/services/tenant-service/` |
| OpenAPI authority | 新增 Services `/api/v1/svc/tenants/{tenantId}/admins/*`（含 `/invitation/resend`、`/{userId}/role` GET/PUT）+ `/api/v1/svc/tenant-admins`（跨租户列表）+ `/api/v1/svc/tenant-admins/tenants`（可用租户列表）+ `/api/v1/svc/tenants/{tenantId}/roles`（可变角色列表） |
| Frozen exclusions | Core v1.yaml 不修改 |
| idempotency_key | required on: invite, resend, role(PUT), reset-password, disable, enable；**not** on: delete(DELETE) |
| Module main doc | spec-boss-tenant-admin.md |

## 7. 关联模块

- [PRD: 配额套餐](./prd-new-boss-tenant-quota-policy.md) — 配额变更申请审批后调 Core API 修改配额
- [PRD: 租户列表](./prd-new-boss-tenant-list.md) — 创建租户时绑定首位管理员为 tenant-admin
