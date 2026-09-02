# TENANT-ADMIN-ISSUE-009：租户管理员详情 — gRPC RPC + Core SDK 适配器 + 网关转发

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #9）
> **完成日期：** 2026-08-26
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/ani-gateway/internal/router/`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入、#5 可用租户列表、#6 邀请管理员、#7 重发邀请、#8 跨租户列表
> **Product line：** boss

## 交付内容

落地"租户管理员详情"端到端链路：网关 gRPC 转发 → tenant-service `GetTenantAdminDetail` RPC → Core SDK `GetAdminDetail`（复用 `GetUser`）→ Core handler → PG 查询 → 本地 `GetInvitationFlags` 合成邀请标记。覆盖 SPEC §5.1.9 / US-004。

核心能力：按 `tenantId + userId` 返回用户全字段（不含 `password_hash`）+ `is_inviting` / `is_expired` 标记 + `tenant` 对象 + `created_at` / `updated_at` timestamps。支持 lazy expire（inviting 且 `expire_at < now` 时自动写回 expired）。

### 修改文件

| 文件 | 变更 |
|------|------|
| `tenant-service/internal/service/tenant_admin_service.go` | 实现 `GetTenantAdminDetail` RPC（5 步：校验依赖 → 解析 UUID → Core 拉取 → Store 合成标记 → 返回含 timestamps） |
| `tenant-service/internal/service/tenant_admin_service_test.go` | 新增 `GetDetail` 5 个子测试 + `GetInvitationFlags` fake 实现 + `expireStaleInvitations` fake + `invitationFlagFromLatest` helper |
| `tenant-service/internal/service/errors.go` | `mapStoreError` 新增 `ErrStoreUnavailable` 分支 |
| `tenant-service/internal/repo/ports/errors.go` | 新增 `ErrStoreUnavailable = errors.New("STORE_UNAVAILABLE")` 哨兵错误 |
| `tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go` | `GetInvitationFlags` 加 lazy expire（inviting + `expire_at < now` → 写回 expired + 修改内存 status） |
| `ani-gateway/internal/router/tenant_admin_resources.go` | `tenantAdminBusinessCodeByHTTP` 新增 `STORE_UNAVAILABLE` 映射 |

### 新增测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_GetDetail/full_fields_with_inviting` | 全字段验证：id/email/username/display_name/role/status/source/last_login_at/created_at/updated_at/is_inviting/is_expired/tenant |
| `TestTenantAdminService_GetDetail/latest_expired_flag` | 最新邀请 expired → is_expired=true, is_inviting=false |
| `TestTenantAdminService_GetDetail/latest_settled_both_false` | 最新邀请 accepted → 两个标记均 false |
| `TestTenantAdminService_GetDetail/lazy_expire_stale_inviting` | inviting 且 expire_at < now → lazy expire 写回 expired，返回 is_expired=true |
| `TestTenantAdminService_GetDetail/not_found` | Core 用户不存在 → NotFound TENANT_ADMIN_NOT_FOUND |
| `TestHandler_Detail`（gateway） | 全字段 + password_hash 排除 + 顶层 tenant_id 排除 + tenant 对象验证 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 返回全字段（含 is_inviting/created_at） | `adminWithTenantToProto(user, true)` — includeTimestamps=true | ✅ |
| 不含 password_hash | Core SQL `SELECT` 未选取 `password_hash` 列 | ✅ |
| 无顶层 tenant_id 冗余 | `adminWithTenantJSON` 不输出 `tenant_id` key | ✅ |
| is_inviting/is_expired 互斥标记 | `GetInvitationFlags` switch 互斥置位 | ✅ |
| source 推断 oidc:→third_party / local:→local | Core DB `inferTenantUserSource` 按 username 前缀 | ✅ |
| role 由 user_roles+roles 解析 | Core SQL `LEFT JOIN LATERAL (SELECT r2.name FROM user_roles ...)` | ✅ |
| 只读端点不写审计 | RPC 无 `writeAudit*` 调用 | ✅ |
| TestHandler_Detail 验证全字段 | gateway 测试验证 13 个字段 + password_hash 排除 + tenant_id 排除 | ✅ |

## Design Decisions

### D1：GetAdminDetail 复用 GetUser Core 端点

- **Ambiguity：** SPEC §5.1.9 要求"按 tenantId+userId 返回用户全字段"。Core 已有 `GET /admin/tenants/{id}/users/{uid}` 端点（`GetUser`），是否需要新建独立的 admin-only 端点。
- **Choice：** `GetAdminDetail` 直接复用 `GetUser`（`return c.GetUser(ctx, tenantID, userID)`），不新建端点。
- **Rationale：** SPEC 未限定仅返回 admin 角色用户，"用户全字段"语义与 `GetUser` 完全一致。新建端点增加维护成本无额外价值。

### D2：lazy expire 写回 DB（非纯内存判断）

- **Ambiguity：** 邀请 `status='inviting'` 但 `expire_at < now` 时，是在读取后纯内存标记为 expired，还是写回 DB。
- **Choice：** 写回 DB（`UPDATE tenant_admin_invitation SET status='expired' WHERE id=$1 AND status='inviting'`），同时修改内存 status。
- **Rationale：** 写回 DB 保证后续查询一致——其他端点（列表、重发）也能看到 expired 状态，避免不同端点返回矛盾结果。乐观锁 `WHERE status='inviting'` 保证并发安全。

### D3：ErrStoreUnavailable 与 ErrCoreUnavailable 分离

- **Ambiguity：** `s.store == nil`（本地 tenant-service DB 不可用）原先用 `ErrCoreUnavailable`（`GRPC_CLIENT_UNAVAILABLE`），导致排障混淆。
- **Choice：** 新增 `ErrStoreUnavailable`（`STORE_UNAVAILABLE`）哨兵错误，4 处 `store == nil` 守卫全部替换；`mapStoreError` 和 gateway 映射表同步。
- **Rationale：** Core 不可用和 Store 不可用是不同故障域：前者是 Core API 宕机/网络不通，后者是 tenant-service 自身 DB 连接断。分离后排障不再误导。

## Deviations

### DV1：lazy expire 延伸到详情路径

- **Spec：** SPEC §5.1.9 只说"is_inviting / is_expired 标记"，未描述过期写回机制。
- **Implementation：** `GetInvitationFlags` 在读取最新邀请后，若 `status='inviting' && expire_at < now`，写回 `expired` 并修改内存 status。
- **Reason：** 与列表路径（`ListInvitationFlags` 调 `expireStaleLatestInvitations` 批量写回）保持一致，确保详情和列表返回的标记一致。

## Tradeoffs

### T1：串行 Core + Store vs 并行

- **备选 A（已选）：** 步骤 3（Core `GetAdminDetail`）和步骤 4（Store `GetInvitationFlags`）串行执行
- **备选 B：** 用 `errgroup.Go` 并行执行
- **A 优势：** 代码简单，Core 失败时不必再查 Store（节省一次 DB 查询）
- **A 劣势：** 总延迟 = Core RTT + Store RTT（~7-23ms）
- **B 优势：** 延迟 = max(Core, Store)（~5-20ms）
- **B 劣势：** 增加代码复杂度；Core 失败时 Store 查询浪费
- **结论：** 选 A — 当前延迟可接受，详情端点不是极高频场景

### T2：lazy expire 写回 vs 纯内存

- **备选 A（已选）：** 写回 DB + 修改内存 status
- **备选 B：** 纯内存标记 expired，不写回 DB
- **A 优势：** 后续查询一致，不依赖每次都做内存判断
- **A 劣势：** 多一次 UPDATE 操作（但有乐观锁保护，并发安全）
- **B 优势：** 无写操作，纯读
- **B 劣势：** 列表路径已写回，详情路径不写回会导致不一致
- **结论：** 选 A — 与列表路径保持一致

## Open Questions

None — 实现遵循 SPEC §5.1.9 和用户明确指令。三轮 review-it 均无阻塞性发现。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService_GetDetail" -v
# 5 sub-tests PASS:
#   full_fields_with_inviting: PASS
#   latest_expired_flag: PASS
#   latest_settled_both_false: PASS
#   lazy_expire_stale_inviting: PASS
#   not_found: PASS

# 全量回归
go test ./internal/... -run "TestTenantAdminService" -v
# 29 sub-tests PASS（含 GetDetail 5 + ListAll 10 + Invite 5 + Resend 5 + ListAvailable 2 + Unimplemented 8 - 重叠计数）

# review-it 三轮
# Round 1: clean — 无阻塞性发现
# Round 2: F1 lazy expire + F3 ErrStoreUnavailable → 已修复
# Round 3: clean — 3 个观察级问题（软删除孤儿数据、串行延迟、角色不限定），均非阻塞
```

## 边界声明

- 本 Issue 完成 `GetTenantAdminDetail` 端到端链路（RPC + Core 复用 + Store 邀请标记合成 + lazy expire + 网关转发 + 测试）。
- F1 lazy expire 修复同时影响列表路径（`ListInvitationFlags` 已有 `expireStaleLatestInvitations`）和详情路径（`GetInvitationFlags` 新增写回逻辑）。
- F3 `ErrStoreUnavailable` 修复涉及全部 4 个已实现 RPC（Invite / Resend / ListAll / GetDetail）的 `store == nil` 守卫。
- `TenantAdminService` 其余 8 个 RPC（角色/密码/禁用启用删除/审计）仍返回 `UNIMPLEMENTED`，属后续 Issue 范围。
