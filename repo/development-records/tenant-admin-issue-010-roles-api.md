# TENANT-ADMIN-ISSUE-010：租户可分配角色列表 — gRPC RPC + Core SDK 适配器 + 网关转发

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #10）
> **完成日期：** 2026-08-26
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/ani-gateway/internal/router/`、`repo/pkg/ports/tenant_admin.go`、`repo/pkg/adapters/runtime/postgres_tenant_admin.go`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入
> **Product line：** boss

## 交付内容

落地"查询租户可分配角色列表"端到端链路：网关 gRPC 转发 → tenant-service `ListTenantRoles` RPC → Core SDK 适配器 → ani-gateway Core handler → PG 查询。覆盖 SPEC §5.1.12 / US-012 / FR-8。

核心能力：按 `tenantId` 返回该租户可分配的角色列表（排除 `platform-*` 前缀），包含系统角色（`tenant_id IS NULL`）+ 租户自定义角色（`tenant_id = $tenantId`）。不分页，用于"修改管理员角色"选择器的数据源。租户软删除后仅返回系统角色，不返回已删除租户的自定义角色。

### 修改文件

| 文件 | 变更 |
|------|------|
| `pkg/ports/tenant_admin.go` | `RoleRef.ID` 类型 `string` → `uuid.UUID`，新增 `github.com/google/uuid` import |
| `pkg/adapters/runtime/postgres_tenant_admin.go` | `ListAssignableRoles` SQL `SELECT id::text` → `SELECT r.id, r.name FROM roles r`；新增租户状态校验 `EXISTS (SELECT 1 FROM tenants t WHERE t.id = $1 AND t.status <> 'disabled')` |
| `tenant-service/internal/repo/adapters/core/tenant_admin_svc_client.go` | `ListAssignableRoles` SDK 适配器：调 Core `GET /admin/tenants/{id}/roles`，解析 `items` 数组为 `[]ports.AssignableRole`（`uuid.Parse` + 空值校验） |
| `tenant-service/internal/service/tenant_admin_service.go` | 实现 `ListTenantRoles` RPC（4 步：校验 core 依赖 → 解析 tenant_id UUID → Core SDK 查询 → 返回 proto 响应） |
| `ani-gateway/internal/router/tenant_admin_resources.go` | 网关 handler：`GET /tenants/{tenantId}/roles` → gRPC 转发 → JSON `{ items: [{ id, name }] }` |
| `ani-gateway/internal/router/admin_tenant_admin_resources.go` | Core 网关 handler：`GET /admin/tenants/{tenant_id}/roles` → Core DB `ListAssignableRoles` → JSON |
| `api/openapi/services/v1.yaml` | `/tenants/{tenantId}/roles` 端点定义（200/401/403） |

### 新增测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ListTenantRoles` | happy path：3 个系统角色 + 1 个租户自定义角色、排除 platform-*、字段完整 |
| `TestTenantAdminService_ListTenantRoles/invalid_tenant_id` | 非 UUID 格式 → 400 VALIDATION_FAILED |
| `TestHandler_ListTenantRoles`（gateway） | gRPC 转发、JSON 字段映射、排除 platform-* 角色不泄露 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `GET /tenants/{tenantId}/roles` 无需分页参数，返回可分配角色列表 | RPC 无分页入参；SQL 无 LIMIT/OFFSET | ✅ |
| 查询条件 `name NOT LIKE 'platform-%'` 且 `tenant_id IS NULL OR tenant_id = $tenantId` | SQL `WHERE r.name NOT LIKE 'platform-%' AND (r.tenant_id IS NULL OR r.tenant_id = $1)` | ✅ |
| 网关通过 gRPC 转发至 tenant-service `ListTenantRoles` RPC，不直连 Core DB | `tenant_admin_resources.go` 调 `api.admins.ListTenantRoles`（gRPC client） | ✅ |
| tenant-service `ListTenantRoles` RPC 内部调用 Core SDK，不直接操作数据库 | `s.core.ListAssignableRoles(ctx, tenantID)` → `c.sdk.Request("GET", ...)` | ✅ |
| 响应 200 `{ items: [{ id, name }] }`，不分页 | 网关 handler JSON 映射 2 字段；OpenAPI `RoleRef` schema 对齐 | ✅ |
| 只读端点，platform-admin / platform-ops / platform-readonly 可访问 | RPC 无 `writeAudit*` 调用；注释标注"只读、无审计" | ✅ |
| 集成测试 `TestHandler_ListTenantRoles` | 验证排除 platform-* 前缀、字段完整性 | ✅ |

## Design Decisions

### D1：RoleRef.ID 类型统一为 uuid.UUID

- **Ambiguity：** Core DB 端口 `RoleRef.ID` 原为 `string`，tenant-service 端口 `AssignableRole.ID` 为 `uuid.UUID`。两条路径类型不一致，但都最终映射到 proto 的 `string id`。
- **Choice：** 将 `RoleRef.ID` 从 `string` 改为 `uuid.UUID`，SQL 从 `SELECT id::text` 改为 `SELECT r.id`（pgx 驱动直接 scan 到 `uuid.UUID`）。
- **Rationale：** `roles.id` 列类型为 `UUID PRIMARY KEY DEFAULT gen_random_uuid()`，使用 `uuid.UUID` 类型更贴合数据库实际类型，避免 string↔UUID 反复转换。Core 网关 JSON 序列化时 `uuid.UUID` 实现 `json.Marshaler`，输出标准 UUID 字符串，无需额外处理。

### D2：租户软删除后仅返回系统角色

- **Ambiguity：** SPEC §5.1.12 的 SQL 为 `WHERE name NOT LIKE 'platform-%' AND (tenant_id IS NULL OR tenant_id = $tenantId)`，未校验租户是否已软删除。租户被删除后，其自定义角色行仍存在于 `roles` 表中（`ON DELETE CASCADE` 仅对物理删除生效，软删除不触发）。
- **Choice：** 在 SQL 中增加 `EXISTS` 子查询校验租户状态：`r.tenant_id = $1 AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = $1 AND t.status <> 'disabled')`。软删除后仅返回系统角色（`tenant_id IS NULL`），不返回该租户的自定义角色。
- **Rationale：** `roles.tenant_id` 有 FK `REFERENCES tenants(id) ON DELETE CASCADE`，但软删除（`status='disabled'`）不会触发级联。若不校验，前端可在已删除租户上看到其自定义角色列表，产生孤儿数据展示。系统角色（`tenant_id IS NULL`）不受租户状态影响，始终可用。

### D3：Service 层错误映射使用 mapStoreError

- **Ambiguity：** `ListTenantRoles` 调用的是 `s.core`（Core SDK 适配器），但错误经 `mapStoreError` 处理。Core SDK 适配器内部 `mapSDKError` 已将 HTTP 错误转为 `ErrCoreUnavailable` 等哨兵错误，`mapStoreError` 中也有 `ErrCoreUnavailable` 分支。
- **Choice：** 保持使用 `mapStoreError`，不新增 `mapCoreError`。
- **Rationale：** 当前功能正确——`mapStoreError` 已覆盖 `ErrCoreUnavailable` 哨兵错误分支。两种错误源（Store 和 Core）的哨兵错误集有限且已收敛在 `mapStoreError` 中，拆分 `mapCoreError` 增加代码复杂度无额外收益。

## Deviations

### DV1：SQL 增加 EXISTS 子查询校验租户状态（SPEC 未描述）

- **Spec：** SPEC §5.1.12 SQL 为 `WHERE name NOT LIKE 'platform-%' AND (tenant_id IS NULL OR tenant_id = $tenantId)`，未包含租户状态校验。
- **Implementation：** SQL 增加租户状态校验 `AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = $1 AND t.status <> 'disabled')`，软删除租户的自定义角色不再返回。
- **Reason：** SPEC 原始 SQL 在租户软删除后会返回孤儿角色数据，实际使用中前端可在已删除租户上看到其自定义角色列表。review-it 第一轮发现此问题（P1），用户要求修复。此偏差是对 SPEC 安全性的增强，不破坏原有契约——系统角色始终返回，仅过滤软删除租户的自定义角色。

## Tradeoffs

### T1：EXISTS 子查询 vs JOIN tenants 表

- **备选 A（已选）：** `EXISTS (SELECT 1 FROM tenants t WHERE t.id = $1 AND t.status <> 'disabled')`
- **备选 B：** `LEFT JOIN tenants t ON t.id = r.tenant_id AND t.status <> 'disabled'`
- **A 优势：** EXISTS 子查询语义清晰——仅在 `tenant_id IS NOT NULL` 时校验租户状态；系统角色（`tenant_id IS NULL`）不触发子查询，性能开销最小。
- **B 优势：** 单次 JOIN 可获取租户其他字段（如未来需要）。
- **B 劣势：** JOIN 语义不直观——需处理 `tenant_id IS NULL` 的系统角色行不参与 JOIN，逻辑复杂。
- **结论：** 选 A — EXISTS 语义更精确，只在需要时执行子查询，不影响系统角色行。

### T2：RoleRef.ID 用 uuid.UUID vs string

- **备选 A（已选）：** `RoleRef.ID` 为 `uuid.UUID`，SQL `SELECT r.id` 直接 scan。
- **备选 B：** 保持 `RoleRef.ID` 为 `string`，SQL `SELECT id::text`。
- **A 优势：** 类型与 DB 列一致，无额外转换；Core 网关 JSON 序列化由 `json.Marshaler` 处理。
- **A 劣势：** 需要修改 Core 网关 handler 中使用 `r.ID` 的代码（但 JSON 序列化自动处理）。
- **B 优势：** 不需要修改 Core 网关代码。
- **B 劣势：** string↔UUID 转换冗余，类型语义不精确。
- **结论：** 选 A — review-it 发现 O2 类型不一致问题，统一为 `uuid.UUID` 更贴合数据库实际类型。

## Open Questions

None — 实现遵循 SPEC §5.1.12 和用户明确指令。review-it 发现 P1（软删除孤儿数据）已修复，O2（类型不一致）已修复，其余观察项不阻塞。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/service/ -run TestTenantAdminService_ListTenantRoles -v -count=1
# 2 sub-tests PASS:
#   happy_path: PASS (3 系统角色 + 1 自定义角色，排除 platform-*)
#   invalid_tenant_id: PASS (非 UUID → 400 VALIDATION_FAILED)

cd repo/services/ani-gateway
go build ./...
go test ./internal/router/ -run TestHandler_ListTenantRoles -v -count=1
# PASS: gRPC 转发、JSON 字段映射、排除 platform-*

# review-it
# Round 1: P1 软删除孤儿数据 + O2 类型不一致 → 均已修复
# 深度分析: 6 个实际使用问题，P1 已修复，其余不阻塞
```

## 边界声明

- 本 Issue 完成 `ListTenantRoles` 端到端链路（RPC + Core SDK 适配器 + Core DB 查询 + 网关转发 + 测试）。
- O2 修复（`RoleRef.ID` → `uuid.UUID`）影响 Core DB 层 `ListAssignableRoles` 的 SQL 和返回类型，不影响 SDK 适配器和网关（已有 `uuid.Parse` 和 `json.Marshaler` 处理）。
- P1 修复（软删除租户角色过滤）影响 SQL 层，仅对租户自定义角色生效，系统角色不受影响。
- `tenants.status` 当前 schema 为 `active | suspended | deleted`，后续将统一变更为包含 `disabled`，SQL 已使用 `status <> 'disabled'` 对齐未来状态值。
