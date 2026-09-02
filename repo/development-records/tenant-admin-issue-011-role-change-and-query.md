# TENANT-ADMIN-ISSUE-011：查询用户权限 + 修改权限 — 审查、修复与单角色约束

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #11）
> **完成日期：** 2026-08-27
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/ani-gateway/internal/router/`、`repo/pkg/ports/tenant_admin.go`、`repo/pkg/adapters/runtime/postgres_tenant_admin.go`、`repo/api/proto/tenant/v1/`、`repo/deploy/migrations/`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入、#10 可分配角色列表
> **Product line：** boss

## 交付内容

对 SPEC §5.1.5 `UpdateTenantAdminRole`（US-005）和 §5.1.8 `GetTenantAdminRole`（US-009）进行多轮 review-it 审查并修复。覆盖：role_id 全链路透传、old_role_id 移除与 upsert 简化、单角色唯一索引、幂等键清理、nil pointer 守卫、审计完整性、错误码语义、tenant-admin 排除。

### 修改文件

| 文件 | 变更 |
|------|------|
| `services/tenant-service/internal/service/tenant_admin_service.go` | F2: role_id 解析失败从 `ErrRoleChangeInvalid`（422）改为 `ErrValidationFailed`（400）；B1: `GetTenantAdminRole` nil guard for `*uuid.UUID` TenantID；P2+P3: `GetRolePermissions` 失败时写审计并返回错误；P1 role_id 透传：`UserPermissions.RoleId` 映射到 proto；P4: `structpb.NewList` 失败从 `ErrCoreUnavailable` 改为 `codes.Internal`；注释从"按 old_role_id + role_id 改绑"改为"upsert 改绑" |
| `pkg/adapters/runtime/postgres_tenant_admin.go` | P5: `ChangeRole` 和 `ListAssignableRoles` SQL 排除 `tenant-admin`；old_role_id 移除：`ChangeRole` 简化为 upsert `ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id`；步骤 2 从 `SELECT name` 改为 `SELECT 1`；单角色模型注释 |
| `pkg/ports/tenant_admin.go` | `ChangeRole` 接口签名去掉 `oldRoleID` 参数 |
| `services/tenant-service/internal/repo/ports/core_tenant_admin.go` | `ChangeRole` 接口签名去掉 `oldRoleID` 参数 |
| `services/tenant-service/internal/repo/adapters/core/tenant_admin_svc_client.go` | `ChangeRole` 去掉 `oldRoleID` 参数和 `body.old_role_id`；移除 `idempotencyHeaders` 函数及 4 处调用（ChangeRole/SetStatus/SoftDelete/ResetPassword） |
| `services/tenant-service/internal/repo/adapters/core/tenant_plan_svc_client.go` | 移除 `idempotencyHeaders` 调用（UpdateTenantPlan） |
| `services/tenant-service/internal/repo/adapters/core/quota_svc_client.go` | 移除 `idempotencyHeaders` 调用（PutQuota/CreateQuota/UpsertQuota/DeleteQuota） |
| `services/tenant-service/internal/repo/adapters/core/sdk_client.go` | 移除 `idempotencyHeaders` 函数定义 |
| `services/tenant-service/internal/repo/adapters/core/quota_svc_client_test.go` | 移除 3 处 `Idempotency-Key` header 断言 |
| `services/tenant-service/internal/service/tenant_admin_service_test.go` | 更新 fake mock `ChangeRole` 签名；`invalid_role_id` / `platform_role_rejected` 测试补充 `perms` mock 数据；`platform_account_rejected` 改为返回 `ErrTenantAdminNotFound`；`tenant_member` 子测试新增 `RoleID` 断言 |
| `api/proto/tenant/v1/tenant_admin_service.proto` | `UserPermissions` message 新增 `string role_id = 5` |
| `pkg/generated/pb/tenant/v1/tenant_admin_service.pb.go` | `buf generate` 重新生成 |
| `services/ani-gateway/internal/router/tenant_admin_resources.go` | `userPermissionsJSON` 输出 `role_id` 字段 |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go` | `updateTenantUserRole` 请求体去掉 `old_role_id`，调用去掉参数 |
| `services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md` | §4.2 `role` → `role_id`（UUID）；§5.1.5 去掉 `old_role_id`，改为 `upsert user_roles`；§5.1.8 补充 `role_id` 响应；§5.2 约束更新；§7.1 错误码更新 |
| `deploy/migrations/20260827_001_user_roles_single_role.sql` | 新增：`CREATE UNIQUE INDEX ON user_roles (user_id)` 强制单角色约束 |

### 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ChangeRole/success` | 正常改绑 + 审计 old_role/new_role |
| `TestTenantAdminService_ChangeRole/invalid_role_id` | role_id 非 UUID → 400 VALIDATION_FAILED |
| `TestTenantAdminService_ChangeRole/platform_role_rejected` | platform-* 角色被拒绝 |
| `TestTenantAdminService_GetRolePermissions/tenant_member` | 正常查询 + role_id 透传 |
| `TestTenantAdminService_GetRolePermissions/platform_account_rejected` | 平台账户 → 404 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `GetTenantAdminRole` 返回 `role_id`（UUID） | proto `UserPermissions.role_id` + svc gateway `userPermissionsJSON` 输出 `role_id` | ✅ |
| `UpdateTenantAdminRole` 入参为 `role_id`（UUID），非 `role`（string） | SPEC §4.2 更新；proto `UpdateTenantAdminRoleRequest.role_id`；网关 body `role_id` | ✅ |
| `ChangeRole` 不再需要 `old_role_id` | Core DB / SDK / Service / Core gateway 全部去掉 `oldRoleID` 参数 | ✅ |
| 数据库强制单角色 | `user_roles(user_id)` 唯一索引 + upsert `ON CONFLICT (user_id) DO UPDATE` | ✅ |
| `role_id` 解析失败返回 400 而非 422 | `ErrValidationFailed`（400）+ `codes.InvalidArgument` | ✅ |
| `tenant-admin` 不可分配、不可修改 | SQL `AND name <> 'tenant-admin'` in ChangeRole + ListAssignableRoles | ✅ |
| 幂等键由网关中间件处理 | `idempotencyHeaders` 函数及所有调用点已移除 | ✅ |
| `GetTenantAdminRole` 平台账户 nil guard | `if perms.TenantID != nil` + `if perms.RoleID != uuid.Nil` | ✅ |

## Design Decisions

### D1：user_id 唯一索引 — 数据库层面强制单角色

- **Ambiguity：** `user_roles` 表主键为 `PRIMARY KEY (user_id, role_id)`，数据库层面允许一用户绑多角色。业务模型为单角色（一用户一角色），但无数据库约束保证。
- **Choice：** 新增迁移 `20260827_001_user_roles_single_role.sql`，在 `user_roles(user_id)` 上创建唯一索引。
- **Rationale：** 单角色模型从"业务约定"升级为"数据库约束"，消除并发写入产生多绑定残留的可能性。同时使 `ChangeRole` 的 upsert 语义成立——`ON CONFLICT (user_id) DO UPDATE` 直接定位唯一行。

### D2：ChangeRole 从 UPDATE/INSERT 分支简化为 upsert

- **Ambiguity：** `ChangeRole` 原需传入 `oldRoleID` 定位既有 `user_roles` 行（UPDATE WHERE user_id AND role_id = old_role_id），无匹配则 INSERT。逻辑复杂且有并发边界问题。
- **Choice：** 有了 `user_id` 唯一索引后，去掉 `oldRoleID` 参数，改为单条 upsert：`INSERT ... ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id`。
- **Rationale：** upsert 是原子操作，无并发问题。去掉 `oldRoleID` 后全链路简化——SDK body 不再发 `old_role_id`，Core gateway 请求体不再解析 `old_role_id`，Service 层 `GetRolePermissions` 仅用于审计记录 `old_role`（不再传给 Core）。

### D3：role_id 全链路透传

- **Ambiguity：** `GetTenantAdminRole` 响应只有 `role`（角色名），没有 `role_id`（UUID）。前端修改角色时需要 `role_id` 作为 `UpdateTenantAdminRole` 入参，但查询端点不返回 `role_id`，形成循环依赖——必须额外调 `ListTenantRoles` 反查。
- **Choice：** 在 proto `UserPermissions` 中新增 `string role_id = 5`，Core DB → SDK → Service → svc gateway 全链路透传 `role_id`，`uuid.Nil` 时省略。
- **Rationale：** 前端调一次 `GetTenantAdminRole` 即可获得当前 `role_id`，直接用于 `UpdateTenantAdminRole` 的 `role_id` 参数，消除不必要的网络往返。

### D4：幂等键由网关中间件统一处理，Service 层不再生成

- **Ambiguity：** SDK 适配器原有 `idempotencyHeaders()` 函数每次调用生成新 UUID，忽略请求传入的幂等键，与网关 `Idempotency` 中间件（24h 缓存 + SetNX + fingerprint replay）功能重复。
- **Choice：** 移除 `idempotencyHeaders` 函数及所有调用点。幂等键仅由网关中间件处理（HTTP 层），Core DB 的 `ON CONFLICT` 处理数据库层幂等。
- **Rationale：** 幂等键生命周期在 HTTP 边界（网关入口），透传到 gRPC 后 Core DB 的 upsert 已提供最终一致性，不需要 Service 层再生成幂等键。

## Deviations

### DV1：role_id 解析失败返回 VALIDATION_FAILED（400）而非 ROLE_CHANGE_INVALID（422）

- **Spec：** SPEC §7.1 定义 `ROLE_CHANGE_INVALID` 422 用于"role_id 不可分配"（角色存在但不在允许范围）。`role_id` 格式错误（非 UUID）属于参数校验失败，应返回 `VALIDATION_FAILED` 400。
- **Implementation：** `UpdateTenantAdminRole` 中 `parseTenantAdminUUID(req.GetRoleId(), "role_id")` 失败时返回 `ErrValidationFailed`（400），而非原来的 `ErrRoleChangeInvalid`（422）。
- **Reason：** 422 表示"请求格式正确但语义不可处理"，400 表示"请求格式错误"。UUID 解析失败是格式问题，用 400 更准确。review-it 第三轮发现此问题（F2）。

### DV2：structpb.NewList 编码失败返回 codes.Internal 而非 ErrCoreUnavailable

- **Spec：** SPEC 未显式描述此场景。
- **Implementation：** `GetTenantAdminRole` 中 `structpb.NewList(permItems)` 失败时返回 `status.Errorf(codes.Internal, "encode permissions: %v", lvErr)`，而非原来的 `businessError(codes.Internal, ports.ErrCoreUnavailable, "encode permissions")`。
- **Reason：** 编码失败是 Core 返回的数据格式问题，不是"Core 不可用"。`ErrCoreUnavailable` 映射为 502 `GRPC_CLIENT_UNAVAILABLE`，误导客户端判断故障来源。`codes.Internal` 映射为 500 `INTERNAL_ERROR`，语义准确。review-it 发现此问题（P4）。

## Tradeoffs

### T1：old_role_id upsert vs UPDATE WHERE old_role_id

- **备选 A（已选）：** `INSERT ... ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id`（不传 old_role_id）
- **备选 B：** 保留 `old_role_id`，`UPDATE user_roles SET role_id = $3 WHERE user_id = $1 AND role_id = $2`
- **A 优势：** 原子操作无并发问题；不需要 Service 层先调 `GetRolePermissions` 读 old_role_id 再传给 Core；全链路少传一个参数。
- **A 前提：** `user_id` 唯一索引保证单行。
- **B 优势：** 乐观锁语义——如果 old_role_id 不匹配说明并发修改，UPDATE 影响 0 行可检测冲突。
- **B 劣势：** 需要 Service 层先读 old_role_id（增加一次 Core 调用）；并发 INSERT 仍需 `ON CONFLICT` 兜底；逻辑复杂。
- **结论：** 选 A — 单角色模型下 upsert 更简洁，且 `user_id` 唯一索引已保证唯一性。乐观锁在单角色场景无额外价值。

### T2：user_id 唯一索引 vs 应用层保证单角色

- **备选 A（已选）：** 数据库 `UNIQUE INDEX ON user_roles (user_id)`
- **备选 B：** 仅在应用层（ChangeRole upsert 逻辑）保证单角色，不加索引
- **A 优势：** 数据库强制约束，任何写入路径（包括未来新增的 API）都无法绕过；`ON CONFLICT (user_id)` 语法合法。
- **A 劣势：** 如果未来需要多角色，需删除索引。
- **B 优势：** 不限制未来扩展为多角色。
- **B 劣势：** 依赖应用层约束，新增写入路径可能遗漏。
- **结论：** 选 A — 当前业务明确为单角色模型，数据库约束比应用层约定更可靠。用户明确确认"单角色"。

## Open Questions

None — 用户确认单角色模型后，`user_id` 唯一索引 + upsert 方案确定。所有 review-it 发现（B1/F2/F3/P1-P5/O2）均已修复。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/service/ -run "TestTenantAdminService_ChangeRole|TestTenantAdminService_GetRolePermissions" -v -count=1
# 5 sub-tests PASS:
#   ChangeRole/success, ChangeRole/invalid_role_id, ChangeRole/platform_role_rejected
#   GetRolePermissions/tenant_member, GetRolePermissions/platform_account_rejected

cd repo/services/ani-gateway
go build ./...
go test ./internal/router/ -run "TestTenantAdminRoutes" -v -count=1

# review-it (5 rounds)
# Round 1: B1 nil panic / P1 idempotencyHeaders / P2+P3 audit / P4 spec / P5 tenant-admin exclusion → 均已修复
# Round 2: O2 concurrent INSERT → ON CONFLICT DO NOTHING → 已修复
# Round 3: F2 wrong error code → VALIDATION_FAILED → 已修复; F3 重复赋值 → 用户手动修复
# Round 4 (GetTenantAdminRole + UpdateTenantAdminRole 最终): P1 role_id 透传 → 已修复; P4 encode error → 已修复
# Round 5 (simplification): F1 注释过时 → 已修复; F2 SELECT name → SELECT 1 → 已修复; gofmt → clean
# Final: clean, no actionable finding
```

## 边界声明

- `user_id` 唯一索引迁移（`20260827_001`）需在部署前执行。如果 `user_roles` 表已有重复 `user_id` 的多角色数据，迁移会失败——需先清理数据。
- `old_role_id` 移除是跨层变更：Core DB port、SDK client、Service port、Service 层、Core gateway、SPEC 全部同步修改，无遗留引用。
- `role_id` 透传涉及 proto 重新生成（`buf generate`），下游消费方需更新生成的客户端代码。
- `tenants.status` 当前 schema 为 `active | suspended | deleted`，后续将统一变更为包含 `disabled`，SQL 已使用 `status <> 'disabled'` 对齐未来状态值。
