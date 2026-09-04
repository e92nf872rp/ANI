# PRD: 租户列表管理 (new)

> 来源：`repo/services/tasks/modules/plan/tenant/租户管理plan v3.0.md` §4.1.1 / §4.1.2 / §4.1.3 / §4.1.6 / §5.2 / §5.2.14  
> **以实现为准（2026-09）：** 后端 API（Core + Services + Gateway）已落地；Issue-010 SSO 测试连接仍为 stub（501）；登录拦截 / MFA 登录强制 / BOSS 前端未交付。  
> Core 配额 API 本身不归本模块；本模块创建时调用 Core 完成配额初始化与查询。

## 1. Introduction

为 BOSS 平台提供租户的创建、查看、编辑、冻结/解冻/禁用状态机管理，以及租户身份认证（SSO/MFA）配置、生命周期查询、操作历史查询。本模块是租户管理核心入口，关联套餐绑定（初始化配额）、首位管理员创建（绑定 tenant-admin）与 `tenant_auth` 初始化。

租户状态机三态（`TenantStatus`）：`active` / `frozen` / `disabled`（不可逆终态）。**本阶段**：状态落库 + lifecycle/审计；**不**自动释放资源；**不**在 Gateway 拦截登录（`TENANT_FROZEN` / `TENANT_DISABLED` 为延期能力）。生命周期动作（`TenantLifecycleAction`）：`create` / `freeze` / `unfreeze` / `disable`——与状态枚举不同，不可混用。

MFA/SSO 开关由独立 `tenant_auth`（1:1）承载；配额由 Core `resource_quota` 承载。

## 2. Goals

- 查询可用套餐（供创建绑定）
- 租户创建（首位管理员 + 套餐 + Core 配额初始化）
- 租户列表/详情查询
- 修改基本信息（display_name / contact_email）
- 冻结/解冻/禁用状态机
- SSO/MFA 配置查看与修改（**测试连接延期 / stub**）
- 查询租户配额（代理 Core）
- 配额变更申请提交 / 查询 / 审批
- 生命周期与操作历史查询
- 租户内管理员列表（tenant-admin ∪ inviting）

## 3. User Stories

> Services 路径相对 OpenAPI `servers`（`/api/v1/svc`），下文写 `/svc/tenants*` 便于阅读，契约文件为相对 `/tenants*`。  
> 写操作：body **`idempotency_key` required**（可回落 header `Idempotency-Key`）；**Core `/admin/tenants*` 写端点无幂等字段**。

### US-001: 查询可用套餐
**Description:** As platform-admin/ops/readonly，创建时需要查看可用 active 套餐。
**Acceptance Criteria:**
- [x] GET `/svc/tenants/available-plans` → `{ items[] }`
- [x] 仅 `status='active'`，不分页；每项 id/code/name
- [x] Typecheck/lint passes

### US-002: 创建租户
**Description:** As platform-admin/ops，创建租户并指定套餐与首位管理员。
**Acceptance Criteria:**
- [x] POST `/svc/tenants`：name/display_name/email/plan_id/admin_* + body `idempotency_key`
- [x] name `^[a-z0-9-]{3,40}$`；冲突 409 `TENANT_NAME_CONFLICT`
- [x] plan 非 active → 422 `PLAN_NOT_ACTIVE`
- [x] admin_password 8–64 且 ≥3 类字符；bcrypt 后传 Core `admin_password_hash`
- [x] Core 事务：tenants + tenant_auth + users + user_roles(tenant-admin) + lifecycle(`create`)
- [x] lifecycle 归因经 Gateway → headers → Core ctx（非 body 传 user_id）
- [x] 事务外 UpsertQuota；失败不回滚租户
- [x] 审计 `tenant.create`；响应 `{ id, message }`；不明文回显密码
- [x] Typecheck/lint passes

### US-003: 查询租户列表
**Description:** As platform-admin/ops/readonly，游标分页列表与过滤。
**Acceptance Criteria:**
- [x] GET `/svc/tenants`：limit(默认20,≤100)/cursor/status/search；keyset `created_at DESC, id DESC`
- [x] items：id/name/display_name/plan_id/plan_code/status/admin_count/created_at（**无 auth**）
- [x] plan_code 由 service 批量装配；套餐已删 → `plan_code=""`
- [x] admin_count 由 Core LATERAL 统计返回
- [x] Typecheck/lint passes

### US-004: 查询租户详情
**Description:** As platform-admin/ops/readonly，查看租户完整信息。
**Acceptance Criteria:**
- [x] GET `/svc/tenants/{tenantId}`：含 plan_code、user_count、admin_count、frozen_at、disabled_at
- [x] `auth: { sso_enabled, mfa_required }`（完整配置走 GET .../auth/sso）
- [x] 不存在 → 404 `TENANT_NOT_FOUND`
- [x] Typecheck/lint passes

### US-005: 修改租户基本信息
**Description:** As platform-admin/ops，修改 display_name / contact_email。
**Acceptance Criteria:**
- [x] PUT `/svc/tenants/{tenantId}` 部分更新；不可改 name/status；空更新 → 400
- [x] **disabled → 409 `TENANT_STATE_INVALID`**（service）；Core 对 disabled 更新为 404
- [x] 审计 `tenant.update`；响应 `{ id, message }`
- [x] Typecheck/lint passes

### US-006: 冻结/解冻租户
**Description:** As platform-admin/ops，冻结或解冻租户。
**Acceptance Criteria:**
- [x] POST `.../freeze`：active → frozen + frozen_at；`.../unfreeze`：frozen → active，清空 frozen_at
- [x] 非法转换 → 409 `TENANT_STATE_INVALID`
- [x] 写审计 + lifecycle（归因经 ctx）
- [x] 响应 `{ id, message }`
- [ ] **Deferred：** Gateway 登录拦截 403 `TENANT_FROZEN`（本阶段实例可继续运行，状态仅落库）
- [x] Typecheck/lint passes

### US-007: 禁用租户
**Description:** As platform-admin/ops，禁用租户（不可逆）；禁用前确认关键计算/存储配额无占用。
**Acceptance Criteria:**
- [x] POST `.../disable`：active/frozen → disabled + disabled_at
- [x] 前置：Core GetQuota；仅 `gpu_count`/`cpu_core`/`memory_gb`/`storage_gb` 任一 `used+reserved>0` → 409 `TENANT_HAS_RUNNING_RESOURCES`
- [x] 其余配额维度暂不参与；**不实现资源释放**
- [x] 不改 users.status；保留 resource_quota 行；审计 + lifecycle
- [ ] **Deferred：** Gateway 登录拦截 403 `TENANT_DISABLED`
- [x] Typecheck/lint passes

### US-008: 查看 SSO/MFA 配置
**Description:** As platform-admin/ops/readonly，查看认证配置。
**Acceptance Criteria:**
- [x] GET `.../auth/sso` → `{ sso_enabled, provider, mfa_required, updated_at }`
- [x] SSO 详细密钥不在 tenant_auth（K8s Secret 等外部承载）
- [x] Typecheck/lint passes

### US-009: 修改 SSO 与测试连接
**Description:** As platform-admin/ops，切换 SSO 并（远期）测试连接。
**Acceptance Criteria:**
- [x] PUT `.../auth/sso`：sso_enabled/provider 部分更新 + `idempotency_key`
- [x] 有效开启 SSO 时 provider 必填，否则 422 `TENANT_SSO_CONFIG_INVALID`
- [x] provider：省略/null=不更新；`""`=清空；disabled 租户改 Auth → 409
- [x] Services PUT 映射 Core 单一 `PUT /admin/tenants/{id}/auth`
- [x] 审计 `tenant.sso.update`
- [ ] **OPEN（Issue-010 stub）：** POST `.../auth/sso/test` 当前 **501 `NOT_IMPLEMENTED`**；目标：OIDC discovery，不写库不写审计，返回 `{ success, discovery_result, error, tested_at }`
- [x] Typecheck/lint passes（写路径）；测试连接待实现

### US-010: 切换强制 MFA
**Description:** As platform-admin/ops，切换租户级 MFA 强制开关（配置落库）。
**Acceptance Criteria:**
- [x] PUT `.../auth/mfa`：`mfa_required` 必填布尔 + `idempotency_key`；审计 `tenant.mfa.update`
- [x] 响应 `{ id, message }`
- [ ] **Deferred：** 登录侧强制执行 MFA（本阶段仅配置落库）
- [x] Typecheck/lint passes

### US-011: 查询租户配额
**Description:** As platform-admin/ops/readonly，查看配额使用。
**Acceptance Criteria:**
- [x] GET `.../quota`：单次代理 Core GetQuota（响应已含 display_name/unit；svc **不再二次 ListQuotaMeta**）
- [x] items：resource_type/display_name/used/total/unit；display_name 空则兜底 resource_type
- [x] 租户不存在 → 404；Core 不可达 → Gateway **502 `GRPC_CLIENT_UNAVAILABLE`**
- [x] 只读不写审计
- [x] Typecheck/lint passes

### US-012: 提交配额变更申请
**Description:** As platform-admin/ops，批量多维度提交配额变更。
**Acceptance Criteria:**
- [x] POST `.../quota-requests`：items[{resource_type,new_value}] + `idempotency_key`
- [x] 校验：items≥1、格式、new_value≥0、批内维不重复 → 422 `QUOTA_CHANGE_REQUEST_INVALID`
- [x] `request_id` 来自网关 `x-request-id`（兼容 `req_<uuid>`）；service 不自生成；缺失 → 400
- [x] **`x-user-id` 必填** 作 `requested_by`；缺失 → 400
- [x] 先 ListQuotaMeta：未注册/未启用 → **422 `QUOTA_RESOURCE_NOT_REGISTERED`**；再 GetQuota 冻 old_value（线协议 int64，无行→**0**）
- [x] 跨请求同维 pending **允许**；同 request 同维 → **409 `QUOTA_CHANGE_REQUEST_CONFLICT`**
- [x] 仅 INSERT；审计含 request_id；响应 id=request_id
- [x] Typecheck/lint passes

### US-013: 查询配额变更申请列表
**Acceptance Criteria:**
- [x] GET `.../quota-requests`：可选 status；created_at DESC；不分页
- [x] items 含 request_id/tenant_id/resource_type/old_value/new_value/status/requested_by/created_at
- [x] 不调 Core；Typecheck/lint passes

### US-014: 审批配额变更申请
**Acceptance Criteria:**
- [x] POST `.../quota-requests/{reqId}/approve`：整批按 request_id；approved true/false + `idempotency_key`
- [x] 非 pending → 409 `QUOTA_CHANGE_REQUEST_NOT_PENDING`；不存在 → 404
- [x] 通过：先改状态再 UpsertQuota；Core 失败不回滚审批态；驳回不改配额
- [x] Typecheck/lint passes

### US-015: 查询租户生命周期
**Acceptance Criteria:**
- [x] GET `.../lifecycle`：limit/cursor/action（`create|freeze|unfreeze|disable`）；CursorPage
- [x] 经 Core `tenant_lifecycle`；404 TENANT_NOT_FOUND
- [x] Typecheck/lint passes

### US-016: 查询租户操作历史
**Acceptance Criteria:**
- [x] GET `.../audit-logs`：limit/cursor/action/result；读 service `audit_logs`
- [x] Typecheck/lint passes

### US-017: 查询租户内管理员列表
**Acceptance Criteria:**
- [x] GET `.../admins`：limit/cursor/role/status/search；CursorPage
- [x] 默认：**tenant-admin** ∪ **inviting**（不含未邀请普通成员；默认不含 expired）
- [x] 字段对齐 proto `TenantScopedAdmin`（形状同 AdminWithTenant）；含 is_inviting/is_expired；**无 permissions[]**
- [x] Typecheck/lint passes

## 4. Functional Requirements

- FR-1: 创建租户：事务内 tenants + tenant_auth + users + user_roles(tenant-admin) + lifecycle；事务外 Core 配额初始化
- FR-2: 列表/详情游标分页；plan_code service 装配；admin_count Core 返回；详情 auth 仅两布尔
- FR-3: 可改 display_name/contact_email；不可改 name/status；disabled 不可改基本信息（409）
- FR-4: 冻结/解冻/禁用写 lifecycle+审计；禁用四维 used+reserved 守卫；本阶段不释放资源、不登录拦截
- FR-5: SSO/MFA 配置读写；测试连接待 Issue-010；MFA 登录强制延期
- FR-6: 配额代理单次 GetQuota；502 不可达
- FR-7: 可用套餐仅 active
- FR-8: 配额变更提交/列表/整批审批；跨请求同维 pending 允许
- FR-9: 三态状态机；禁用不可逆
- FR-10: tenants 不存 MFA/SSO 密钥/配额/plan_code

## 5. Non-Goals / Deferred

- 不实现 Core 配额 API 本身、TCC 扣减、计量采集
- 不实现绑定套餐更新配额（配额套餐 PRD）
- 不实现管理员权限矩阵查询（租户管理员 PRD）
- 不实现计费；无 Console 自助；无 EnableTenant
- **暂不**禁用时资源释放；**暂不**将其它配额维纳入禁用守卫
- **Deferred：** Gateway 登录拦截 FROZEN/DISABLED；MFA 登录强制执行
- **OPEN：** SSO 测试连接真实现（当前 501 stub）
- **本阶段后端已改 Core OpenAPI**（`/admin/tenants*` + getTenant 扩展）；旧「不修改 Core v1.yaml」作废
- BOSS 前端页面实现不在本 PRD 后端批次强制交付（见 UX 规格）

## 6. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | boss |
| Code scope（后端已落地） | `repo/services/tenant-service/`、`repo/services/ani-gateway/`、`repo/pkg/adapters/runtime/postgres_tenant.go`、`repo/api/openapi/{v1,services/v1}.yaml`、`repo/api/proto/tenant/v1/` |
| 前端（规格见 UX） | `repo/frontends/boss/`（实现可另批） |
| OpenAPI | Services 相对 `/tenants*`（base `/api/v1/svc`）；Core `/admin/tenants*` |
| gRPC | **无独立 TenantListService**；19 RPC 挂 `TenantService`（`tenant_plan.proto`）；messages 在 `tenant_list_service.proto` |
| Frozen exclusions | Core 配额表/迁移由 Core 维护；本模块不实现 Core 配额 API 本体 |
| idempotency_key | Services 写端点 body required（header 回落）；Core admin 写 **无** 幂等字段 |
| Module main docs | `spec-new-boss-tenant-list.md` + `ux-boss-tenant-list.md` |

## 7. 关联模块

- [PRD: 配额套餐](./prd-new-boss-tenant-quota-policy.md)
- [PRD: 租户管理员](./prd-new-boss-tenant-admin.md)
- [PRD: 平台账户管理](./prd-new-boss-platform-admin.md)
