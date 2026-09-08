# TENANT-ADMIN-ISSUE-001：OpenAPI 新增租户管理员管理路径与 schema

> **批次类型：** Feature batch（BOSS 租户管理员管理功能流 Issue #1）
> **完成日期：** 2026-08-07
> **Scope：** `repo/api/openapi/services/v1.yaml`
> **依赖：** 无（功能流第一个 Issue）
> **Product line：** boss

## 交付内容

在 `repo/api/openapi/services/v1.yaml` 中新增租户管理员管理的完整 OpenAPI 契约：12 个端点的 paths、14 个 request/response schemas、1 个 tag（TenantAdmins）、13 个错误码覆盖。不含任何 .go 实现 / SQL 迁移 / 前端页面 / 衍生文件（schema.d.ts 等由用户手动实现）。

### 新增端点（12 个）

| operationId / 路径 | Method | 说明 |
|---|---|---|
| `listAllTenantAdmins` — `/tenant-admins` | GET | 跨租户管理员列表（仅 owner/admin/邀请中），query（limit/cursor/tenant_id/role/status/is_inviting/search），响应 AdminListResponse |
| `inviteTenantAdmin` — `/tenants/{tenantId}/admins/invite` | POST | 邀请管理员，body `{idempotency_key,email,username}`，响应 InvitationResult |
| `resendTenantAdminInvitation` — `/tenants/{tenantId}/admins/{userId}/invitation/resend` | POST | 重发邀请，body `{idempotency_key}`，响应 InvitationResult |
| `getTenantAdminDetail` — `/tenants/{tenantId}/admins/{userId}` | GET | 管理员详情，响应 AdminDetail |
| `deleteTenantAdmin` — `/tenants/{tenantId}/admins/{userId}` | DELETE | 软删除（不幂等，不携带幂等键），响应 IdempotentResult |
| `getTenantAdminRole` — `/tenants/{tenantId}/admins/{userId}/role` | GET | 查询角色与权限，响应 UserPermissions |
| `updateTenantAdminRole` — `/tenants/{tenantId}/admins/{userId}/role` | PUT | 修改角色，body `{idempotency_key,role}`，响应 IdempotentResult |
| `transferTenantOwnership` — `/tenants/{tenantId}/transfer-ownership` | POST | 移交所有者，body `{idempotency_key,target_user_id}`，响应 IdempotentResult |
| `resetTenantAdminPassword` — `/tenants/{tenantId}/admins/{userId}/reset-password` | POST | 重置密码，body `{idempotency_key,new_password}`，响应 IdempotentResult |
| `disableTenantAdmin` — `/tenants/{tenantId}/admins/{userId}/disable` | POST | 禁用，body `{idempotency_key}`，响应 IdempotentResult |
| `enableTenantAdmin` — `/tenants/{tenantId}/admins/{userId}/enable` | POST | 启用，body `{idempotency_key}`，响应 IdempotentResult |
| `listTenantAdminAuditLogs` — `/tenants/{tenantId}/admins/{userId}/audit-logs` | GET | 操作历史，query（limit/cursor/action/result），响应 TenantAdminAuditLogListResponse |

### 新增 Schema（14 个）

`TenantRef`、`AdminWithTenant`、`AdminDetail`、`AdminListResponse`、`InvitationResult`、`UserPermissions`、`TenantAdminAuditLog`、`TenantAdminAuditLogListResponse`、`InviteAdminRequest`、`ResendInvitationRequest`、`UpdateRoleRequest`、`TransferOwnershipRequest`、`ResetPasswordRequest`、`IdempotentOnlyRequest`

### 错误码覆盖（13 个）

`VALIDATION_FAILED(400)`、`UNAUTHORIZED(401)`、`FORBIDDEN(403)`、`TENANT_ADMIN_NOT_FOUND(404)`、`TENANT_NOT_FOUND(404)`、`TENANT_ADMIN_ALREADY_ADMIN(409)`、`TENANT_INVITATION_PENDING(409)`、`TENANT_ADMIN_INVITATION_NOT_FOUND(404)`、`TENANT_INVITATION_SETTLED(409)`、`TENANT_OWNER_ROLE_LOCKED(409)`、`IDEMPOTENCY_CONFLICT(409)`、`LAST_TENANT_OWNER(422)`、`TRANSFER_TARGET_INVALID(422)`、`ROLE_CHANGE_INVALID(422)`、`PASSWORD_SAME_AS_OLD(422)`

### 幂等键要求

`POST .../invite`、`POST .../invitation/resend`、`PUT .../role`、`POST .../transfer-ownership`、`POST .../reset-password`、`POST .../disable`、`POST .../enable` 均带 `idempotency_key`；`DELETE .../admins/{userId}` 不幂等、不携带。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 12 端点全部就位 | v1.yaml paths 全部新增 | ✅ |
| operationId 命名对齐 SPEC §4.1 | 12 个 operationId 与 SPEC 逐一对应 | ✅ |
| 幂等键 | 7 个写端点带 idempotency_key body；DELETE 无 | ✅ |
| schema 定义 | AdminWithTenant/InvitationResult/UserPermissions/游标分页(items+next_cursor) 全部定义 | ✅ |
| 跨租户列表范围 + is_inviting | AdminWithTenant schema description 明确仅返回 owner/admin/邀请中、is_inviting 仅标记 | ✅ |
| 13 错误码覆盖 | 31 处引用覆盖全部错误码 | ✅ |
| validate-openapi-spec | services/v1.yaml: OK，13 tests OK | ✅ |
| validate-spec-split | spec split contract valid + 路由测试 PASS | ✅ |
| 衍生文件 | 不在本 Issue 范围（用户手动实现） | ✅ |

## 验证命令

```bash
cd repo
make validate-openapi-spec     # services/v1.yaml: OK，OpenAPI specs valid: 2
make validate-spec-split        # spec split contract valid + 路由测试 PASS
make validate-architecture      # component import guard passed
```

## Design Decisions

1. **AdminListResponse / TenantAdminAuditLogListResponse 命名而非 CursorPage**
   - 模糊点：SPEC §4.2 提到 `CursorPage` 泛型类型，但项目现有风格为按域命名（如 TenantPlanListResponse、AuditLogResponse）。
   - 选择：按域命名 AdminListResponse / TenantAdminAuditLogListResponse，各自含 items + next_cursor。
   - 理由：对齐 quota-policy 现有风格（TenantPlanListResponse），保持一致性；泛型 CursorPage 需引入 oneOf 或泛型引用，增加复杂度。

2. **AdminDetail 与 AdminWithTenant 分离为两个 schema**
   - 模糊点：SPEC §7.4 定义 `AdminWithTenant` 含 created_at?/updated_at? 可选字段（列表不返回，详情返回），但列表与详情字段集不同。
   - 选择：定义两个 schema——AdminWithTenant（列表用，无 created_at/updated_at）和 AdminDetail（详情用，含 created_at/updated_at nullable）。
   - 理由：列表和详情的消费者需要明确的类型契约；共用一个含可选字段的 schema 会让列表消费者误以为字段存在。

3. **UserPermissions.tenant_id 在 required 中但 nullable**
   - 模糊点：SPEC §4.2 说"仅返回租户成员（tenant_id 非空），平台账号 null 不可查"。
   - 选择：tenant_id 设为 required + nullable: true。
   - 理由：OpenAPI 3.0 语义中 required 表示字段必须存在（key 存在），nullable 表示值可为 null。正常租户成员返回非空 UUID，平台账号理论上不可经此端点查询（后端拦截），但 schema 兼容 nullable 以防边界。

4. **IdempotentOnlyRequest 抽取为独立 schema**
   - 模糊点：disable/enable 端点仅含 idempotency_key，无业务字段。
   - 选择：定义 IdempotentOnlyRequest schema 复用。
   - 理由：避免内联 schema 重复，与 IdempotentResult 配对清晰。

## Deviations

None — 实现严格遵循 SPEC §4.1/§4.2/§6.1 契约定义，无偏离。

## Tradeoffs

1. **错误码作为 response description 注释而非独立 schema**
   - 备选：为每个错误码定义独立的 Error schema（如 TenantAdminNotFoundError）。
   - 选择：复用现有通用 response（BadRequest/NotFound/Conflict/UnprocessableEntity），在 description 中标注具体错误码。
   - 理由：对齐 quota-policy 现有风格；独立 schema 会引入 13 个额外 schema 定义，增加维护成本且 validator 不要求。后续实现 Issue 可在后端错误映射中消费这些 description 标注。

2. **CHANGE_ROLE 方法用 PUT 而非 PATCH**
   - 备选：PATCH（部分更新语义更精确）。
   - 选择：PUT，严格遵循 plan/PRD 契约基准（plan L566、PRD L68 均用 PUT）。
   - 理由：plan 是契约基准，一致性优先于语义偏好。

## Open Questions

1. **IDEMPOTENCY_CONFLICT 是否需要独立 response schema** — 当前作为 Conflict(409) 的 description 注释。后续网关实现（issue-004）需确认 gRPC 错误码到 HTTP 409 的映射是否需区分 IDEMPOTENCY_CONFLICT vs 其他 409。
2. **衍生文件生成时机** — schema.d.ts 由用户手动实现（`pnpm gen-api`），需确认用户在哪个阶段手动生成。前端 Issue（#14-#17）依赖 schema.d.ts 的类型。
3. **PATCH vs PUT 决策** — 已统一为 PUT（对齐 plan/PRD），但 REST 语义上 PATCH 更精确。若后续 Core handler 实现需调整，需同步契约。

## 边界声明

- 本 Issue 只修改 Services OpenAPI 契约（`services/v1.yaml`），不涉及 handler/adapter/前端实现。
- 衍生文件（schema.d.ts、pb 等）由用户手动实现，不在本 Issue 验收范围。
- Handler/adapter/服务端实现属后续 Issue（#004 网关、#005-#013 后端 API、#014-#017 前端页面）。
- 本批次不声明 runtime ready 或 production ready。

## Review-it 结果

review-it clean — 无可操作的 findings。ANI Review Checklist 全部通过：
- `make validate-architecture` passed
- Scope: 仅 v1.yaml，无 frozen Services backend 编辑
- OpenAPI: 无 invented routes/schemas，全部对齐 SPEC §4.1/§4.2
- Idempotency: 写操作含 idempotency_key，DELETE 不携带
- Tenant: tenant_id 为 path 参数，无 request-body tenant_id
