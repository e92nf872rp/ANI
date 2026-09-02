# TENANT-LIST-ISSUE-001：租户列表管理 OpenAPI 契约

> **批次类型：** Contract batch（BOSS 租户列表管理 Issue #1）
> **完成日期：** 2026-09-02
> **Scope：** `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml` only（Issue 明确不含 SDK/pb 生成）
> **依赖：** 无
> **Product line：** boss（Core + Services 契约层）
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-001-openapi-contract.md`

## 交付内容

在 Core 与 Services 两层 OpenAPI 契约中补齐租户列表管理（US-001～US-017）的完整 API 面，作为后续迁移、ports/adapters、Gateway、gRPC、前端实现的唯一真实来源。

| 层 | 新增/扩展 | 数量 |
|---|---|---|
| Core `v1.yaml` | 9 个 `/admin/tenants*` 新端点 + `getTenant` 响应 additive 扩展 | 9 + 1 扩展 |
| Services `v1.yaml` | 19 个 `/tenants*` 新端点 + 共享 schemas | 19 |
| 共享 schemas | TenantListItem、TenantDetail、TenantAuthConfig、SsoTestResult、QuotaChangeRequest*、TenantLifecycleEntry、TenantAuditLogEntry 等 | — |

### 修改文件

| 文件 | 变更摘要 |
|---|---|
| `api/openapi/v1.yaml` | 扩展 `Tenant` schema；新增 TenantCreateRequest / TenantListItem / TenantListResponse / TenantUpdateRequest / TenantAuthConfig / TenantAuthUpdateRequest / TenantLifecycleEntry / TenantLifecycleListResponse；注册 9 个 Core admin 端点（create/list/update/freeze/unfreeze/disable/auth/lifecycle） |
| `api/openapi/services/v1.yaml` | 新增 Tenant List schemas 块；注册 19 个 svc 端点（available-plans、CRUD、状态机、SSO/MFA、quota 代理、quota-requests、lifecycle、audit-logs、scoped admins）；新增 tag `Tenants` |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| Core 9 端点 + getTenant 扩展 | `grep /admin/tenants` + operationId 清单核对 | ✅ |
| Services 19 端点 | paths `/tenants*` 计数 = 19（tenant-list 范围，不含 tenant-admin/plan 既有路径） | ✅ |
| 共享 schemas | components/schemas 块已定义 | ✅ |
| 错误码 400/404/409/422/502 | responses description 标注；502 引用 BadGateway（GRPC_CLIENT_UNAVAILABLE） | ✅ |
| 复用既有 quota / listTenantUsers | 未重复定义 getTenantQuota / listTenantUsers；quota 代理在 description 中指向 Core | ✅ |
| YAML 校验 | `python scripts/validate_yaml.py` 两文件通过 | ✅ |
| API split 契约 | `python scripts/validate_spec_split_contract.py` 通过 | ✅ |
| Gateway route contract | 19 个新 svc 路由尚未注册 → **预期失败**（Issue #4 范围） | ⏳ 待 #4 |
| SDK / pb 生成 | Issue 明确由用户手动生成，本批次未做 | ⏭️ 跳过 |

## Design Decisions

### D1：Core 写 API 不含幂等键，Services 写 API 含 body `idempotency_key`

- **Ambiguity：** PRD 要求写操作 Idempotency-Key，但未明确 Core 内部 admin API 是否也要声明。
- **Choice：** Core 9 个新写端点（create/update/freeze/unfreeze/disable/auth）无 idempotency；Services 全部写端点 required `idempotency_key`（或 `IdempotentOnlyRequest`），并注明可回落 `Idempotency-Key` header。
- **Rationale：** 与 tenant-plans / tenant-admin 模块先例一致——幂等由 ani-gateway Redis 中间件在 svc 入站保证；Core 为 tenant-service 内部调用面，避免双层幂等语义冲突。

### D2：Services SSO/MFA 拆两个 PUT，Core 合并为一个 `updateTenantAuth`

- **Ambiguity：** UX 认证 Tab 分 SSO 与 MFA 两个操作区；Core 只有一张 `tenant_auth` 表。
- **Choice：** Services 暴露 `PUT .../auth/sso` 与 `PUT .../auth/mfa`；Core 单一 `PUT /admin/tenants/{tenant_id}/auth` 接受 sso_enabled/provider/mfa_required 部分更新。
- **Rationale：** BOSS 前端按 Tab 区块调用；tenant-service 将两个 svc PUT 映射到同一 Core 端点，减少 Core 表操作分散。

### D3：创建请求字段 `email`（Services）→ `contact_email`（详情/Core 存储语义）

- **Ambiguity：** PRD US-002 入参写 `email`，US-004/US-005 详情/编辑用 `contact_email`。
- **Choice：** `CreateTenantRequest.email` 落库为 `tenants.contact_email`；`TenantDetail.contact_email` 为 required+nullable。
- **Rationale：** 与 SPEC §4.3 及 UX 创建向导 Step1 字段名一致；详情页统一 contact_email 命名。

### D4：Core 列表用 `TenantListResponse`，Services 列表用 `CursorPage` allOf

- **Ambiguity：** 两层分页 wrapper 命名不统一。
- **Choice：** Core 独立 `TenantListResponse{items, next_cursor}`；Services 复用既有 `CursorPage` + allOf items。
- **Rationale：** Core SDK 生成物独立；Services 与 tenant-plans / tenant-admins 分页模式保持一致。

### D5：US-017 复用 `AdminWithTenant` schema，不在 tenant-list 重复定义

- **Ambiguity：** 租户详情 Admin Tab 返回字段与 tenant-admin 模块跨租户列表高度重叠。
- **Choice：** `listTenantScopedAdmins` 响应 items 引用既有 `AdminWithTenant`（含 is_inviting/is_expired/tenant 嵌套对象）。
- **Rationale：** 减少 schema 漂移；前端 Admin Tab 与 tenant-admin 模块字段对齐（PRD US-017）。

## Deviations

### Dev-1：`listTenantScopedAdmins` 描述"复用 Core listTenantUsers"与 Core 契约能力不完全对齐（**已知缺口，未在本 Issue 修**）

- **Spec 说：** US-017 支持 role 过滤 tenant-admin/user/auditor；search 含 display_name；返回 is_inviting/is_expired。
- **Core 现状：** `listTenantUsers` role enum 仅 `[tenant-admin]`；search 仅 email/username；`TenantUser` 无 is_inviting/is_expired。
- **本 Issue 实现：** Services 端点已声明完整 role/search 参数与 `AdminWithTenant` 响应，但未同步扩展 Core `listTenantUsers`。
- **原因：** Issue #1 scope 限定仅改两个 v1.yaml，且 tenant-list 与 tenant-admin 模块边界需 Issue #2/#14 实现层编排（参考 tenant-admin #8 的 Core+Store 合成模式）。
- **后续：** Issue #14 或 Core additive 扩展 `listTenantUsers` role enum；tenant-service 从 `tenant_admin_invitation` 合成 is_inviting/is_expired。

### Dev-2：新增 tag 名 `Tenants`，与既有 `Tenant` / `TenantPlans` 并存

- **Spec 说：** 未强制 tag 命名。
- **本 Issue 实现：** 新端点使用 tag `Tenants`（复数），既有 tenant-admin/plan 路径仍用 `Tenant` / `TenantPlans`。
- **原因：** 区分"租户列表管理"与"租户成员/SSO/Webhook"旧 tag 语义；不影响路由或 operationId。
- **影响：** SDK/静态文档分组可能出两个 Tenants 相关 tag；可接受，或在文档生成阶段合并。

### Dev-3：Issue #1 未更新 Gateway / baseline / SDK 生成物

- **Spec / Issue 说：** 衍生文件由用户手动生成；Gateway 在 Issue #4。
- **本 Issue 实现：** 仅 OpenAPI 源文件。
- **原因：** 严格遵循 Issue scope 与"先契约后实现"流水线。

## Tradeoffs

### T1：Contract-first 先合 OpenAPI vs 与 Gateway 同 PR 合入

| 方案 | 优点 | 缺点 |
|---|---|---|
| **A. 仅 OpenAPI（本 Issue）** | Issue 边界清晰；#2～#14 可并行读契约 | `validate_services_route_contract` 19 error，单独合入会红 CI |
| B. OpenAPI + Gateway 同 PR | CI 绿 | 超出 Issue #1 scope；Review 面过大 |

**选择 A。** 合入策略：与 Issue #4 Gateway 同 PR，或短期 baseline（不推荐长期）。

### T2：Core `listTenantUsers` 扩展 vs tenant-service 多次调用

| 方案 | 优点 | 缺点 |
|---|---|---|
| A. 扩展 Core role enum（additive） | 单次调用；契约清晰 | 需改 Core v1.yaml + handler（超出 #1） |
| **B. 实现层编排（defer）** | #1 不改 Core 既有端点 | 契约描述易误导；#14 复杂度高 |

**#1 选择 defer（B 的实现留 #14）**；review 已标记为 P1 跟进项。

### T3：Core 状态转换 POST 无 requestBody vs Services 用 `IdempotentOnlyRequest`

- **选择：** Core 无 body（内部 API）；Services 强制 idempotency body。
- **理由：** 与 `bindTenantPlan`（Core header 幂等）/ svc 层 body 幂等模式一致；tenant-service 调 Core 时不重复传幂等键。

## Verification Commands

```bash
cd repo
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml
# → validated 2 YAML files

python scripts/validate_spec_split_contract.py
# → spec split contract valid

python scripts/validate_services_route_contract.py
# → 19 ERROR（新 /tenants* 路由待 Issue #4 注册，预期内）

# 完整门禁（Windows 环境 make 可能部分失败；route contract 同上）
make validate-services
```

## 后续 Issue 依赖

| Issue | 依赖本契约 |
|---|---|
| #2 接口/结构体 | proto TenantListService + ports 字段对齐本 schemas |
| #3 数据库迁移 | tenant_auth / tenant_lifecycle / tenants 列扩展 |
| #4 Gateway | **必须**注册 19 svc + 9 Core admin 路由；扩展 adminTenantResponse DTO |
| #5～#14 各 API | 按 operationId 实现；#14 处理 US-017 跨层差异 |
| SDK/pb 生成 | 用户手动触发（Issue #1 不含） |
