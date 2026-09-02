# TENANT-ADMIN-ISSUE-006：邀请租户管理员 API — gRPC RPC + Core SDK 适配器 + Store + 竞态防护

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #6）
> **完成日期：** 2026-08-25
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/deploy/migrations/`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#3 数据库迁移
> **Product line：** boss

## 交付内容

落地"邀请租户管理员"端到端链路：网关 gRPC 转发 → tenant-service `InviteTenantAdmin` RPC → Core SDK 匹配用户 → PG 写入邀请记录 + 审计。覆盖 SPEC §5.1.1 / US-001。

### 修改文件

| 文件 | 变更 |
|------|------|
| `tenant-service/internal/service/tenant_admin_service.go` | 实现 `InviteTenantAdmin` RPC：10 步流程（依赖校验→入参校验→匹配用户→已是 admin→pending 检查→生成 token→INSERT→审计→通知占位→返回） |
| `tenant-service/internal/service/audit.go` | `writeAuditSuccess`/`writeAuditFailure` 加 `if audit == nil { return }` 守卫 |
| `tenant-service/internal/service/errors.go` | 从 `tenant_plan_service.go` 提取 `mapStoreError`/`businessError` 到独立文件（service 包共享） |
| `tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go` | `InsertInvitation` 区分 pending 唯一冲突（→`ErrTenantInvitationPending`）与 token_hash 冲突（→`ErrValidationFailed`）；删除 `CreateAuditLog` 死代码 |
| `tenant-service/internal/repo/ports/tenant_admin_store.go` | 删除 `CreateAuditLog` 接口方法（审计统一走 `TenantPlanAuditStore.Create`），保留 `ListAuditLogs` |
| `deploy/migrations/20260825_001_tenant_admin_invitation_pending_unique.sql` | 新增部分唯一索引 `uk_tenant_admin_invitation_pending ON (tenant_id, user_id) WHERE status='inviting'`，替换旧索引 |

### 新增测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_Invite/success_token_expire_audit` | happy path：返回 token(64位hex)/expire_at(72h)/id；store 写入 inviting 状态；token_hash==SHA256(token)；审计 success 含 target_id+token_hash |
| `TestTenantAdminService_Invite/not_found` | `MatchUser` 返回 `ErrTenantAdminNotFound` → 404 + 审计 failure |
| `TestTenantAdminService_Invite/already_admin` | `IsAlreadyAdmin` 返回 true → 409 `TENANT_ADMIN_ALREADY_ADMIN` + 审计 failure |
| `TestTenantAdminService_Invite/pending` | `HasPendingInvitation` 返回 true → 409 `TENANT_INVITATION_PENDING` + 不 INSERT + 审计 failure |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `POST .../admins/invite` 入参 email+username，需 `idempotency_key`（body） | `validateInviteIdentity` 校验 email/username；`req.GetIdempotencyKey()` 校验非空 | ✅ |
| 匹配现有用户，无匹配 → 404 `TENANT_ADMIN_NOT_FOUND` | `s.core.MatchUser` → `ErrTenantAdminNotFound` → `mapStoreError` → 404 | ✅ |
| 已是 tenant-admin → 409 `TENANT_ADMIN_ALREADY_ADMIN` | `s.core.IsAlreadyAdmin` → `businessError(AlreadyExists, ...)` → 409 | ✅ |
| 已有 inviting 邀请 → 409 `TENANT_INVITATION_PENDING` | `s.store.HasPendingInvitation` + DB 部分唯一索引兜底 → 409 | ✅ |
| INSERT invitation（status='inviting'、token_hash=SHA-256、expire_at=now()+72h），不改角色/users.status | `s.store.InsertInvitation` SQL 只写 invitation 表 | ✅ |
| token=crypto_random(32)，响应 `{id,token,expire_at,message}`，token 仅本次返回 | `generateInviteToken` 用 `crypto/rand` 32 字节；响应 `InvitationResult` | ✅ |
| 写审计 `tenant_admin.invite`（details 含 target_id + token_hash） | `writeAuditSuccess` details 含 target_id+token_hash | ✅ |
| 单测覆盖：成功/404/409 already admin/409 pending/token+expire/审计 | 4 个子测试覆盖全部场景 | ✅ |
| 集成测试 `TestHandler_InviteFlow` | 网关层 `TestHandler_InviteTenantAdmin` 覆盖 gRPC 转发 + JSON 映射 | ✅ |

## Design Decisions

### D2：竞态防护用 DB 部分唯一索引，不用事务

- **Ambiguity：** `HasPendingInvitation` + `InsertInvitation` 是两次独立 DB 连接，存在 TOCTOU 竞态窗口。
- **Choice：** DB 层加 `CREATE UNIQUE INDEX (tenant_id, user_id) WHERE status='inviting'`，并发 INSERT 第二条直接冲突。
- **Rationale：** ① 事务方案需包裹 check+insert 在同一 `WithPlatformTx` 内，引入长事务；② 部分唯一索引是声明式约束，更可靠且无锁竞争；③ 索引同时覆盖 `HasPendingInvitation` 查询（前缀 tenant_id+user_id + status 条件），一石二鸟。

### D3：InsertInvitation 按 ConstraintName 区分两种唯一冲突

- **Choice：** 捕获 23505 后用 `pgErr.ConstraintName` 区分 `uk_tenant_admin_invitation_pending`（→`ErrTenantInvitationPending`）和 `uk_tenant_admin_invitation_token_hash`（→`ErrValidationFailed`）。
- **Rationale：** 两个唯一索引在同一张表，PG 23505 错误带 `constraint_name`（即索引名），可精确区分。token_hash 碰撞极罕见（32 字节随机），但语义不同（一个是业务冲突、一个是内部错误），需分别映射。

### D4：审计统一走 TenantPlanAuditStore，不走 TenantAdminStore.CreateAuditLog

- **Choice：** 删除 `TenantAdminStore.CreateAuditLog` 接口方法及实现，审计写入复用 `TenantPlanAuditStore.Create`。
- **Rationale：** ① `audit_logs` 表按 `resource` 字段区分业务域（`tenant_plan`/`tenant_admin`），无需按 store 拆分；② `TenantPlanAuditStore` 接口已有 `Create` 方法且已注入 `TenantAdminService`；③ 消除两套审计接口并存的维护负担。`ListAuditLogs` 保留在 `TenantAdminStore`，因查询语义不同（按 tenant_id+user_id 过滤）。

### D5：writeAuditSuccess/writeAuditFailure 加 nil 守卫

- **Choice：** 入口加 `if audit == nil { return }`。
- **Rationale：** 审计是 best-effort，store 缺失不应阻断业务错误返回。生产路径注入了非 nil audit，但测试路径可能传 nil，不加守卫会 nil pointer panic。

### D6：mapStoreError/businessError 提取到 errors.go

- **Choice：** 从 `tenant_plan_service.go` 提取到 `internal/service/errors.go`。
- **Rationale：** 两个函数被 service 包内三个文件（`tenant_plan_service.go`/`tenant_service.go`/`tenant_admin_service.go`）+ 测试文件共享，提取到独立文件提升可维护性。

## Deviations

None — 实现遵循 SPEC §5.1.1 / Issue-006 AC 如文。

## Tradeoffs

### T1：DB 部分唯一索引 vs 事务包裹

- **备选 A（已选）：** `CREATE UNIQUE INDEX (tenant_id, user_id) WHERE status='inviting'`
- **备选 B：** `WithPlatformTx` 包裹 `HasPendingInvitation` + `InsertInvitation`
- **A 优势：** 声明式约束，无锁竞争，索引复用查询；**A 劣势：** 需 migration，错误处理需按 ConstraintName 区分
- **B 优势：** 无需 migration；**B 劣势：** 长事务，check-then-act 仍可能被并发破坏（除非用 SELECT FOR UPDATE）
- **结论：** 选 A，更可靠且性能更优

### T2：删除 CreateAuditLog vs 保留

- **备选 A（已选）：** 删除接口/实现/test stub，审计统一走 `TenantPlanAuditStore.Create`
- **备选 B：** 保留 `CreateAuditLog`，将来 `TenantAdminService` 改用它
- **A 优势：** 消除死代码，减少维护负担；**A 劣势：** `ListAuditLogs` 仍在 `TenantAdminStore`，审计读写分离在两个 store
- **B 优势：** 审计读写在同一 store；**B 劣势：** `CreateAuditLog` 与 `TenantPlanAuditStore.Create` 功能重复
- **结论：** 选 A，`audit_logs` 表按 `resource` 区分域，写入无需按 store 拆分

## Open Questions

None — 实现与 SPEC/Issue 对齐，审查通过，测试全绿。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService_Invite" -v
go test ./internal/...
```

## 边界声明

- 本 Issue 完成 `InviteTenantAdmin` RPC + 竞态防护 + 审计 nil 守卫 + 死代码清理 + errors.go 提取。
- `ResendTenantAdminInvitation` 仍返回 `UNIMPLEMENTED`，属 Issue #7 范围。
- `UpdateInvitation` 的唯一冲突处理未覆盖 `uk_tenant_admin_invitation_pending`，因重发功能当前 `unimplemented()`，将来落地时需补充。
- Migration `20260825_001` 未清理潜在脏数据（多条 inviting），因系新功能表无生产数据，开发环境可手动清理。
