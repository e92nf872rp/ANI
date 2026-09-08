# PRD: 配额套餐管理 (new)

> 来源：`repo/services/tasks/modules/plan/tenant/租户管理plan v3.0.md` §4.2 / §5.3 / §5.3.9
> Core 配额 API（resource_quota_meta / resource_quota / 配额初始化）不归本模块实现，由 Core 团队提供。
> Updated: 2026-08-14（对齐实现代码：total 物化落库、列表 next_cursor 空串、时间本地串等）

## 1. Introduction

为 BOSS 平台提供套餐模板的全生命周期管理。套餐定义各维度配额上限（通过 `plan_quota_limits` 关联 Core `resource_quota_meta`），创建租户时按套餐初始化配额。套餐限额创建后可通过 PUT 修改，修改后自动同步存量租户。套餐基本信息（name/description）创建后仍可修改。系统还提供配额元数据查询（透传 Core）和可绑定租户列表查询，为前端创建/绑定流程提供数据支撑。

## 2. Goals

- 套餐模板 CRUD + draft/active/disabled 状态机
- 套餐基本信息（name/description）创建后可修改（PUT /tenant-plans/{planId}）
- 套餐限额查询（service 层 buildQuotaLimitViews 组装：store 原始行 + Core ListQuotaMeta，COALESCE 兜底为具体 total）
- 套餐限额修改（同步存量租户，保留 approved 维度；逐租户调 Core UpsertQuota 一律 upsert）
- 绑定套餐按限额更新租户配额（调 Core API，UpsertQuota 一律 upsert）
- 配额元数据查询（GET /quota-meta 透传 Core resource_quota_meta）
- 可绑定租户列表查询（GET /tenant-plans/{planId}/bindable-tenants）
- 有租户关联的套餐不可删除

## 3. User Stories

### US-001: 创建套餐
**Description:** As platform-admin/ops，我需要创建套餐并配置各维度配额上限。
**Acceptance Criteria:**
- [x] POST `/api/v1/svc/tenant-plans`，入参 code/name/description/quota_limits，状态默认 draft
- [x] code 格式 `^[a-z0-9-]{3,40}$`，全局唯一（partial unique index WHERE is_deleted=FALSE），冲突 409 `PLAN_CODE_CONFLICT`
- [x] name 1-64 字符，description ≤ 512 字符
- [x] quota_limits 每项 resource_type 必须在 resource_quota_meta 中 enabled=true，否则 422 `QUOTA_RESOURCE_NOT_REGISTERED`
- [x] total 为 null 时服务端用 Core `default_quota` **物化写入** `plan_quota_limits.total`（落库为具体数值，不保留 NULL）；为负数 400 `VALIDATION_FAILED`
- [x] 同一 resource_type 不可重复出现
- [x] 响应 200 `{ id, message: "tenant plan created" }`，写审计日志
- [x] Typecheck/lint passes

### US-002: 查询套餐列表
**Description:** As platform-admin/ops/readonly，我需要分页查询套餐列表并按状态/关键字过滤。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans`，支持 limit/cursor/status/search
- [x] search 关键字模糊查询（匹配 name，大小写不敏感）
- [x] items 含 id/code/name/description/status/tenant_count/created_at/updated_at，不含 quota_limits（网关 JSON 含 description；时间字段为 `YYYY-MM-DD HH:mm:ss` Asia/Shanghai，非 RFC3339）
- [x] 响应含 total（满足筛选条件的总条数）与 next_cursor（网关经 nullIfEmpty 将空串映射为 JSON `null` = 已无更多；与审计列表一致）
- [x] 软删除套餐不出现
- [x] Typecheck/lint passes

### US-003: 查询套餐详情
**Description:** As platform-admin/ops/readonly，我需要查看单个套餐完整信息。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans/{planId}` 返回完整对象（id/code/name/description/status/tenant_count/created_at/updated_at）
- [x] 不存在或已软删除 404 `TENANT_PLAN_NOT_FOUND`
- [x] Typecheck/lint passes

### US-004: 查询套餐限额
**Description:** As platform-admin/ops/readonly，我需要查看套餐各维度配额上限及有效值。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans/{planId}/quota-limits` 返回 `{ items[] }`
- [x] items 每项含 resource_type/display_name/unit/total
- [x] display_name 和 unit 来自 Core `resource_quota_meta`（service 层 buildQuotaLimitViews 组装：store.GetQuotaLimits 原始行 + Core ListQuotaMeta）
- [x] total 为具体数值：历史行若仍为 NULL，查询路径用 `default_quota` 兜底返回，并 best-effort 回写库；写入路径（Create/PUT）已物化，展示不返回 null
- [x] Typecheck/lint passes

### US-005: 发布套餐
**Description:** As platform-admin/ops，我需要将草稿或已禁用的套餐发布为活跃状态。
**Acceptance Criteria:**
- [x] POST `/api/v1/svc/tenant-plans/{planId}/activate`：draft/disabled → active
- [x] active 再激活 409 `PLAN_STATE_INVALID`
- [x] 响应 200 `{ id, message: "tenant plan activated" }`，写审计日志
- [x] Typecheck/lint passes

### US-006: 禁用套餐
**Description:** As platform-admin/ops，我需要禁用不再使用的套餐。
**Acceptance Criteria:**
- [x] POST `/api/v1/svc/tenant-plans/{planId}/disable`：active → disabled
- [x] disabled 不可被新租户引用，存量租户不受影响（继续按该模板计算配额）
- [x] disabled 可通过 activate 重新启用回 active
- [x] draft 不可直接 disable，409 `PLAN_STATE_INVALID`
- [x] 响应 200 `{ id, message: "tenant plan disabled" }`，写审计日志
- [x] Typecheck/lint passes

### US-007: 删除套餐
**Description:** As platform-admin/ops，我需要删除不再使用的套餐以释放 code。
**Acceptance Criteria:**
- [x] DELETE `/api/v1/svc/tenant-plans/{planId}` 软删除（is_deleted=TRUE + deleted_at=now()）
- [x] 有租户关联（tenants.plan_id = plan_id AND status!='disabled'）409 `TENANT_PLAN_IN_USE`
- [x] 任意状态（draft/active/disabled）均可删除，仅校验是否有非停用租户关联
- [x] 删除后 code 可被新套餐复用（partial unique index WHERE is_deleted=FALSE）
- [x] 响应 200 `{ id, message: "tenant plan deleted" }`，写审计日志
- [x] Typecheck/lint passes

### US-008: 修改套餐限额（同步存量租户）
**Description:** As platform-admin/ops，我需要修改套餐限额并自动同步到已绑定该套餐的存量租户。
**Acceptance Criteria:**
- [x] PUT `/api/v1/svc/tenant-plans/{planId}/quota-limits`，入参 idempotency_key + items[{resource_type, total}]
- [x] items 至少 1 项，每项 resource_type 格式校验，total >= 0 或 null（null → 服务端用 default_quota **物化写入**，不保留 NULL）
- [x] 同一 resource_type 不可重复，否则 400 `VALIDATION_FAILED`
- [x] resource_type 未注册或 enabled=false → 422 `QUOTA_RESOURCE_NOT_REGISTERED`
- [x] 任意状态（draft/active/disabled）均可修改限额
- [x] 修改 plan_quota_limits 表中该套餐的 total（落库为具体数值）
- [x] 同步存量租户：查询 tenants WHERE plan_id=planId AND status!='disabled'，逐租户收集需更新的维度，逐租户调 Core `UpsertQuota`（一律 upsert，不再先 GET 判断存在性）
- [x] 已 approved 配额变更申请的维度保留不覆盖（跳过 Core API 调用）
- [x] 不影响已有资源：新限额低于 used+reserved 时自动收紧为 used+reserved，已有资源继续运行，不会强制停止或回收
- [x] Core API 同步失败时写 `tenant.quota_init_failed` 审计并异步重试（最多 3 次，指数退避 1s/2s/4s），不阻塞限额修改成功响应
- [x] 审计日志在事务提交后写入（best-effort：审计失败不回滚已提交的限额变更）
- [x] 响应 200 `{ id, message: "quota limits updated" }`（响应体不含 synced_tenant_count；真实同步计数仅在审计 details）
- [x] 写审计日志 `action='tenant_plan.update_quota_limits'`，details 含 plan_id/updated_dimensions/synced_tenant_count/skipped_approved/tightened
- [x] Typecheck/lint passes

### US-009: 绑定套餐更新配额
**Description:** As platform-admin/ops，我需要为租户绑定套餐并按限额更新配额。
**Acceptance Criteria:**
- [x] POST `/api/v1/svc/tenants/{tenantId}/plan`，入参 idempotency_key + plan_id
- [x] plan_id 不存在或已软删除返回 404 `TENANT_PLAN_NOT_FOUND`；plan 状态非 active（draft/disabled）返回 422 `PLAN_NOT_ACTIVE`
- [x] 租户已 disabled 返回 409 `TENANT_STATE_INVALID`
- [x] 读 plan_quota_limits 逐维度：total 非 NULL 时 total=total；为 NULL 时 total=default_quota（service 层 buildQuotaLimitViews 组装兜底）
- [x] 已 approved 配额变更申请的维度保留不覆盖（跳过 Core API 调用）
- [x] 调 Core `UpsertQuota` 下发配额（一律 upsert，不再先 GET 判断存在性；自动收紧）
- [x] 同步更新 tenants.plan_id（若与当前不同）；Core 同步失败时 best-effort 回滚 plan_id（回滚失败也返回错误）
- [x] 不影响已有资源：更换套餐后已创建实例继续运行；新 total < used+reserved 时自动收紧为 used+reserved，不会强制停止或回收
- [x] 本端点直接修改配额，不走审批流程
- [x] 响应 200 `{ id, message: "quota bound to plan" }`
- [x] 写审计日志 `action='tenant.bind_plan_quota'`，details 含 plan_id/tenant_id/tenant_name/tenant_display_name/skipped_approved/tightened/updated；审计失败 best-effort（只 Warn 不阻塞成功响应）
- [x] Typecheck/lint passes

### US-010: 查询套餐绑定租户
**Description:** As platform-admin/ops/readonly，我需要查看绑定某套餐的所有租户。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans/{planId}/tenants` 返回 `{ items[] }`
- [x] items 每项含 id/name/display_name/status
- [x] 不分页，返回完整列表
- [x] 查询 tenants WHERE plan_id=planId AND status!='disabled'
- [x] 套餐不存在或已软删除 404 `TENANT_PLAN_NOT_FOUND`
- [x] Typecheck/lint passes

### US-011: 套餐操作历史
**Description:** As platform-admin/ops/readonly，我需要查看套餐操作历史。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans/{planId}/audit-logs` 游标分页返回（limit/cursor + total + next_cursor）
- [x] ~~支持 action/result 过滤~~ — **未实现（设计决策）**：审计日志量小，前端本地 result 过滤即可
- [x] 查询 audit_logs WHERE resource='tenant_plan' AND details->>'plan_id'=planId
- [x] 按 created_at DESC 排序
- [x] items 每项含 id/action/result/details/created_at（5 个字段）；tenant_id/user_id/request_id/resource/ip_address/user_agent 虽在 DB 表存储但不在 API 响应中暴露
- [x] Typecheck/lint passes

### US-016: 更新套餐基本信息
**Description:** As platform-admin/ops，我需要修改套餐的名称和描述。
**Acceptance Criteria:**
- [x] PUT `/api/v1/svc/tenant-plans/{planId}`，入参 name（可选）、description（可选），需 idempotency_key（可选）
- [x] 校验：plan_id 存在且 is_deleted=FALSE（否则 404 `TENANT_PLAN_NOT_FOUND`）
- [x] name 和 description 均为可选字段：未传或为 null 表示不更新，传空串表示清空（proto StringValue 可选语义）
- [x] name 1-64 字符，description ≤ 512 字符
- [x] 响应 200 `{ id, message: "tenant plan updated" }`
- [x] 写审计日志 `action='tenant_plan.update'`，details 含 plan_id/name_updated/description_updated
- [x] Typecheck/lint passes

### US-017: 查询配额元数据
**Description:** As platform-admin/ops/readonly，我需要查询当前可用的配额维度列表，用于创建套餐时配置限额和展示限额明细。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/quota-meta` 返回 `{ items[] }`
- [x] items 每项含 resource_type/display_name/unit/default_quota/is_discrete
- [x] 透传 Core `GET /api/v1/admin/quota-meta` 结果（无缓存，每次实时调 Core）
- [x] Core 不可用时返回 502 `GRPC_CLIENT_UNAVAILABLE`
- [x] Typecheck/lint passes

### US-018: 查询可绑定租户列表
**Description:** As platform-admin/ops/readonly，我需要查询可以绑定到某套餐的租户列表，用于绑定租户时选择。
**Acceptance Criteria:**
- [x] GET `/api/v1/svc/tenant-plans/{planId}/bindable-tenants` 返回 `{ items[] }`
- [x] 可绑定条件：status != 'disabled' 且 plan_id IS DISTINCT FROM {planId}（未绑定该套餐）
- [x] items 每项含 id/name/display_name/status，按 name 排序
- [x] 校验：plan_id 存在且 is_deleted=FALSE（否则 404 `TENANT_PLAN_NOT_FOUND`）
- [x] Typecheck/lint passes

## 4. Functional Requirements

- FR-1: 系统必须支持套餐 CRUD，code 全局唯一（partial unique index WHERE is_deleted=FALSE），状态 draft/active/disabled
- FR-2: 系统必须支持套餐限额查询，service 层 buildQuotaLimitViews 组装：store.GetQuotaLimits 原始行 + Core ListQuotaMeta；历史 NULL 用 default_quota 兜底为具体 total（并可回写）；Create/PUT 写入侧 total=null 一律物化为 default_quota 落库
- FR-3: 系统必须支持套餐限额修改（PUT），任意状态均可修改，修改后自动同步存量租户（保留 approved 维度；逐租户调 Core UpsertQuota 一律 upsert；Core 失败异步重试不阻塞；成功响应不含同步计数）
- FR-4: 套餐模板字段（name/description）创建后可通过 PUT /tenant-plans/{planId} 修改（可选字段语义：未设置=不更新，空串=清空；idempotency_key 可选）
- FR-5: 有租户关联（status!='disabled'）的套餐不可删除（409 TENANT_PLAN_IN_USE）
- FR-6: 系统必须支持绑定套餐更新配额，读 plan_quota_limits 收集维度后逐租户调 Core `UpsertQuota`（一律 upsert），保留 approved 维度；Core 失败 best-effort 回滚 plan_id
- FR-7: 状态转换：draft→active（activate）、active→disabled（disable）、disabled→active（activate）
- FR-8: 系统必须支持配额元数据查询（GET /quota-meta），透传 Core resource_quota_meta，无缓存
- FR-9: 系统必须支持可绑定租户列表查询（GET /tenant-plans/{planId}/bindable-tenants），排除已停用租户和已绑定该套餐的租户

## 5. Non-Goals

- 不实现 Core 配额 API（Core quota 不归本模块）
- 不实现配额元数据管理 UI（Core 责任）
- 不实现租户配额查询与修改 UI（Core 责任）
- 不实现查询可用套餐（属租户列表 PRD 范围）
- 不实现计费单价字段（后续 PR）
- 不实现 TCC 配额扣减与计量采集
- 不实现操作历史 action 服务端筛选（设计决策：审计日志量小，前端本地 result 过滤即可）
- 不实现配额元数据本地缓存（每次实时调 Core）

## 6. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | boss |
| Code scope | `repo/frontends/boss/src/` + `repo/services/tenant-service/` |
| OpenAPI authority | 新增 Services `/api/v1/svc/tenant-plans/*` + `/api/v1/svc/tenants/{tenantId}/plan` + `/api/v1/svc/quota-meta` |
| Frozen exclusions | Core v1.yaml 不修改；配额表由 Core 迁移维护 |
| idempotency_key (body) | required（schema）on: POST /tenant-plans, POST /tenant-plans/{id}/activate, POST /tenant-plans/{id}/disable, PUT /tenant-plans/{id}/quota-limits, POST /tenants/{id}/plan；**可选** on: PUT /tenant-plans/{id}。实际去重由 Gateway Idempotency 中间件（header `Idempotency-Key` 或 body `idempotency_key`）；tenant-service 不校验该字段 |
| Module main doc | spec-new-boss-tenant-quota-policy.md |

## 7. 关联模块

- [PRD: 租户列表](./prd-new-boss-tenant-list.md) — 创建租户时引用套餐初始化配额、绑定套餐更新配额
- [PRD: 租户管理员](./prd-new-boss-tenant-admin.md) — 配额变更申请审批（approved 维度保留不覆盖）
