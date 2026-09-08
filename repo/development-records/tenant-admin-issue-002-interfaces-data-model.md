# TENANT-ADMIN-ISSUE-002：接口与数据模型设计 — 实现 + review-it 修复笔记

> **Issue**: issue-002-interfaces-data-model.md
> **Date**: 2026-08-11
> **Product**: BOSS
> **Scope**: `api/proto/tenant/v1/`、`pkg/generated/pb/tenant/v1/`、`services/tenant-service/internal/repo/ports/`、`services/tenant-service/internal/repo/adapters/`

---

## 1. Design Decisions

### 1.1 Core/Services 边界拆分：用户/角色操作归 Core，邀请/审计归 Services

**Ambiguity**: SPEC §3.2 原设计将所有 store 方法（含 MatchUser / IsAlreadyAdmin / ListAllTenantAdmins / ChangeRole / GetRolePermissions / TransferOwnership / SetStatus / SoftDelete / ResetPassword）放在 tenant-service 的 `TenantAdminStore` 中，直接 SQL 操作 `users` / `user_roles` / `roles` 表。但 CLAUDE.md 强制规定 Services 禁止 import `pkg/ports`、`pkg/adapters`，且 `validate_services_boundary.py` 编译期强制。

**Choice**: 将 store 拆分为两层：
- `TenantAdminStore`（本地端口）：仅操作 `tenant_admin_invitation` 和 `audit_logs` 表（HasPendingInvitation / InsertInvitation / GetLatestInvitation / UpdateInvitation / CreateAuditLog / ListAuditLogs）
- `UserSvcClient`（Core SDK 端口）：封装 Core `/admin/tenants/{tenant_id}/users/*` REST API（MatchUser / IsAlreadyAdmin / GetUser / GetAdminDetail / ListTenantAdmins / ChangeRole / GetRolePermissions / GetChangeableRoles / TransferOwnership / SetStatus / SoftDelete / ResetPassword）

**Rationale**: 对齐既有 quota-policy 模式（`QuotaSvcClient` / `TenantSvcClient` 均经 Core Go SDK 调 REST）。Core v1.yaml 已有全部 10 个用户管理端点（L9082-L9314），无需新增 Core API。

### 1.2 `IdempotentResult` 从 `tenant_plan.proto` 移至 `common.proto`

**Ambiguity**: `tenant_admin_service.proto` 需要复用 `IdempotentResult` 消息（6 个写 RPC 返回），但该消息定义在 `tenant_plan.proto` 中。直接 import 会导致跨文件耦合。

**Choice**: 将 `IdempotentResult` 移到 `common/v1/common.proto`（通用消息层），`tenant_admin_service.proto` 引用 `common.v1.IdempotentResult`。`tenant_plan.proto` 保留其本地 `IdempotentResult` 定义（避免破坏现有代码），后续 issue 可统一迁移。

**Rationale**: `common.proto` 是已有公共消息层（`CursorPageRequest` / `CursorPageMeta` / `TenantContext`），`IdempotentResult` 作为通用写返回类型属于此层。

### 1.3 `changeable-roles` 作为独立路径而非 role 子操作

**Ambiguity**: issue AC 要求"十三个端点，含 `GET .../changeable-roles`"，但 SPEC §4.1 原始端点表只列了 12 个。`changeable-roles` 语义上是 role 的辅助查询。

**Choice**: 在 `services/v1.yaml` 中新增独立路径 `GET /tenants/{tenantId}/admins/{userId}/changeable-roles`（非 role 路径的子 method），proto 中新增 `GetChangeableRoles` RPC，返回 `ChangeableRolesResponse`（current_role + changeable_roles 列表）。

**Rationale**: 与 Core v1.yaml 已有的 `GET /admin/tenants/{tenant_id}/users/{user_id}/changeable-roles` 端点对齐，保持 Core/Services 端点 1:1 映射。

---

## 2. Deviations

### 2.1 SPEC §3.2 store 方法签名拆分

**Spec**: SPEC §3.2 将所有方法放在单一 `TenantAdminStore` 中，直接操作 users/user_roles/roles 表。

**Implementation**: 拆分为 `TenantAdminStore`（仅 invitation/audit_logs）+ `UserSvcClient`（经 Core SDK 操作 users/roles）。

**Reason**: CLAUDE.md 架构边界强制约束——Services 禁止直接 SQL 操作 Core 表；必须经 Core OpenAPI REST / SDK 调用。

### 2.2 AuditLog `result` 枚举值统一

**Spec**: SPEC §4.2 audit-logs query param 和 TenantAdminAuditLog schema 使用 `enum: [success, failed]`；但既有 PlanAuditLog（同文件）和 ports 注释使用 `failure`。

**Implementation**: 统一为 `failure`（OpenAPI schema + query param + proto 注释，共 4 处修改）。

**Reason**: 全项目一致性——既有 `PlanAuditLog.result` 和 `ports.AuditLog` 注释均用 `failure`，新模块不应引入 `failed` 变体。

### 2.3 proto 移除 `import tenant/v1/tenant_plan.proto`

**Spec**: 原始 proto import `tenant_plan.proto` 以复用 `IdempotentResult`。

**Implementation**: `IdempotentResult` 移至 `common.proto`，移除 `tenant_plan.proto` import。

**Reason**: 解除跨业务 proto 的编译耦合——`tenant_admin_service.proto` 不应依赖 `tenant_plan.proto` 的具体消息定义。

---

## 3. Tradeoffs

### 3.1 `IdempotentResult` 迁移范围

**Alternatives**:
- A: 仅在 `common.proto` 新增 `IdempotentResult`，`tenant_plan.proto` 保留本地定义（当前选择）
- B: 同时迁移 `tenant_plan.proto` 的 `IdempotentResult` 到 `common.proto`，全局统一

**Pros/Cons**:
- A: 零破坏性（现有 tenant_plan 代码不变），但存在两个同义类型
- B: 全局统一，但需修改 tenant_plan.proto + 重新生成 pb + 修改所有引用 `tenantv1.IdempotentResult` 的代码

**Decision**: 选 A——issue-002 范围仅限 tenant-admin 模块，不应扩大修改面。后续可独立 issue 统一迁移。

### 3.2 Core SDK 重新生成

**Alternatives**:
- A: 在本 issue 中重新生成 Core SDK（`gen_sdk_alpha.py`）以包含 `listTenantUsers` 等新端点
- B: 只在 ship 时统一重新生成

**Decision**: 选 A——`validate_sdk_beta.py` 编译期校验 `cursorPaginationOperations` 列表必须与 v1.yaml 一致，不重新生成则 CI 失败。

---

## 4. Open Questions

### 4.1 `adapters/core/user_svc_client.go` 实现时机

`core_user.go` 定义了 `UserSvcClient` 接口（12 个方法），但 `adapters/core/` 下尚未有 `user_svc_client.go` 实现。issue-002 的 AC 要求 "adapters 骨架"，但未明确是否包含 core adapter。

**需确认**: core adapter 骨架（`user_svc_client.go` 返回 `ErrNotImplemented`）是否归入 issue-002，还是 issue-003（网关接入）？

### 4.2 `tenant_plan.proto` 的 `IdempotentResult` 后续统一

`common.proto` 和 `tenant_plan.proto` 现各有一个 `IdempotentResult` 定义。后续是否统一迁移 `tenant_plan.proto` 的引用到 `common.v1.IdempotentResult`？

### 4.3 Core v1.yaml 新增端点的 Core handler 实现

Core v1.yaml 已声明 `/admin/tenant-users`、`/admin/tenants/{tenant_id}/user-lookup` 等 10 个端点，但 Core handler 实现是否已完成？若未完成，`UserSvcClient` adapter 实现后调用将返回 404。

**需确认**: Core handler 实现的 issue 归属和进度。

---

## 5. Verification Commands Run

```bash
# 编译
go build ./pkg/generated/... ./services/tenant-service/...    # PASS

# 契约验证
python scripts/validate_services_contract_test.py             # 6 tests OK
python scripts/validate_services_route_contract_test.py       # 7 tests OK
python scripts/validate_yaml.py api/openapi/services/v1.yaml   # validated

# 边界验证
python scripts/validate_services_boundary.py                   # 3 accepted baseline warnings, 0 errors

# proto 生成
buf generate --template buf.gen.yaml .                        # PASS

# SDK 生成
python scripts/gen_sdk_alpha.py                                # PASS
```

---

## 6. Changed Files

| File | Change |
|------|--------|
| `api/proto/tenant/v1/tenant_admin_service.proto` | NEW — 13 RPC + 消息类型 |
| `api/proto/common/v1/common.proto` | MODIFY — 新增 `IdempotentResult` |
| `pkg/generated/pb/tenant/v1/tenant_admin_service*.pb.go` | NEW — buf 生成 |
| `pkg/generated/pb/common/v1/common.pb.go` | REGEN — 含新 `IdempotentResult` |
| `api/openapi/services/v1.yaml` | MODIFY — 新增 `changeable-roles` 端点 + `ChangeableRolesResponse` schema；`result` 枚举统一为 `failure` |
| `services/tenant-service/internal/repo/ports/tenant_admin_store.go` | NEW — 领域模型 + TenantAdminStore 接口 |
| `services/tenant-service/internal/repo/ports/core_user.go` | NEW — UserSvcClient 接口 |
| `services/tenant-service/internal/repo/ports/errors.go` | MODIFY — 新增 9 个 tenant-admin 哨兵错误 |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go` | NEW — 骨架（返回 ErrNotImplemented） |
| `services/tenant-service/internal/service/tenant_admin_service.go` | NEW — gRPC server 骨架（全部 UNIMPLEMENTED） |
| `architecture/services-contract-baseline.yaml` | MODIFY — 新增 `getChangeableRoles` security 基线 |
| `architecture/services-route-baseline.yaml` | MODIFY — 新增 `changeable-roles` 路由基线 |
| `sdks/core/` | REGEN — 重新生成 Core SDK（含 listTenantUsers） |
