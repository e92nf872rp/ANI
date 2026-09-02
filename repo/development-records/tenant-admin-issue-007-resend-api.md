# TENANT-ADMIN-ISSUE-007：重发邀请 API — gRPC RPC + Store + 竞态防护修复

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #7）
> **完成日期：** 2026-08-25
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`
> **依赖：** #2 接口/数据模型、#3 数据库迁移、#6 邀请 API
> **Product line：** boss

## 交付内容

落地"重发邀请"端到端链路：网关 gRPC 转发 → tenant-service `ResendTenantAdminInvitation` RPC → PG 查最新邀请 → 校验终态 → 重新生成 token → UPDATE 邀请记录 → 审计。覆盖 SPEC §5.1.2 / US-002。

本轮在初次实现基础上，经两轮 review 修复了 2 个问题：
1. `UpdateInvitation` 未处理 pending 唯一索引冲突（竞态 bug）
2. 终态错误 detail 不区分 accepted/rejected（用户体验）

### 修改文件

| 文件 | 变更 |
|------|------|
| `tenant-service/internal/service/tenant_admin_service.go` | 实现 `ResendTenantAdminInvitation` RPC：8 步流程（依赖校验→入参校验→取最新邀请→终态校验→重新生成 token→UPDATE→审计→返回）；终态 detail 拆分 accepted/rejected |
| `tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go` | `UpdateInvitation` 按 `ConstraintName` 区分 `uk_tenant_admin_invitation_pending`（→`ErrTenantInvitationPending`）与 `uk_tenant_admin_invitation_token_hash`（→`ErrValidationFailed`） |
| `tenant-service/internal/service/tenant_admin_service_test.go` | 5 个子测试：success_inviting/success_expired/not_found/settled_accepted/settled_rejected |
| `ani-gateway/internal/router/tenant_admin_resources_test.go` | `TestHandler_ResendFlow`：happy path + settling 409 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `POST .../admins/{userId}/invitation/resend`，需 `idempotency_key`（body） | 网关 [第 249-259 行](file:///d:\Jczn\project\ANI\ANI\repo\services\ani-gateway\internal\router\tenant_admin_resources.go#L249-L259) 解析 body + 补充 header | ✅ |
| 仅对最新一条 `inviting`/`expired` 允许重发；重新生成 token、刷新 expire_at=now()+72h、清空 accepted_at/rejected_at、状态回归 inviting | service 步骤 4-5 + `UpdateInvitation` SQL SET token_hash/expire_at/status/accepted_at=NULL/rejected_at=NULL | ✅ |
| 无匹配记录 → 404 `TENANT_ADMIN_INVITATION_NOT_FOUND` | `GetLatestInvitation` 无行 → `ErrTenantAdminInvitationNotFound` → `mapStoreError` → 404 | ✅ |
| 终态 accepted/rejected → 409 `TENANT_INVITATION_SETTLED` | service 步骤 4 switch 拦截 → `ErrTenantInvitationSettled` → 409 | ✅ |
| 响应 `{id, token, expire_at, message}`；token 仅本次返回 | `InvitationResult` 返回明文 token，DB 只存 hash | ✅ |
| 写审计 `tenant_admin.resend_invitation` | `writeAuditSuccess`/`writeAuditFailure`，action=`"tenant_admin.resend_invitation"` | ✅ |
| 单测覆盖：inviting/expired 成功、终态 409、无记录 404 | 5 个子测试全绿 | ✅ |
| 集成测试 `TestHandler_ResendFlow` | happy path + settling 409 验证 | ✅ |

## Design Decisions

### D1：UpdateInvitation 按 ConstraintName 区分两种唯一冲突

- **Ambiguity：** `UpdateInvitation` 把 expired/inviting 行 UPDATE 回 inviting，与并发的 `InsertInvitation` 可能产生 `uk_tenant_admin_invitation_pending` 冲突。原实现只处理 `token_hash conflict`，不区分冲突来源。
- **Choice：** 捕获 23505 后用 `pgErr.ConstraintName` 区分 `uk_tenant_admin_invitation_pending`（→`ErrTenantInvitationPending`）和 `uk_tenant_admin_invitation_token_hash`（→`ErrValidationFailed`），与 `InsertInvitation` 对齐。
- **Rationale：** ① 竞态场景：请求 B 重发 expired→inviting，请求 C 并发邀请 INSERT 新 inviting 行，B 的 UPDATE 触发 23505；② 原实现把此冲突误归类为 `token_hash conflict` → 400 `VALIDATION_FAILED`，错误码和消息都误导；③ 修正后返回 `ErrTenantInvitationPending` → 409 `TENANT_INVITATION_PENDING`，语义准确。

### D2：终态错误 detail 区分 accepted/rejected

- **Ambiguity：** SPEC 只要求终态返回 409 `TENANT_INVITATION_SETTLED`，未明确 detail 文案是否需区分两种终态。
- **Choice：** 拆分为两个 case，detail 分别为 `"invitation already accepted"` / `"invitation already rejected"`。
- **Rationale：** ① 前端可直接展示终态原因，无需额外查询；② 业务码不变（`TENANT_INVITATION_SETTLED`），不影响网关错误映射；③ default 分支仍保留，防御未知状态。

### D3：重发不校验 idempotency_key 非空

- **Ambiguity：** AC 要求 `idempotency_key`（body），但 service 层是否需校验非空未明确。
- **Choice：** service 层不校验 `idempotency_key`，网关层补充幂等键。与邀请流程（校验非空）不一致。
- **Rationale：** ① 重发是 UPDATE 操作，本身幂等（同一行 UPDATE 不会产生新记录），幂等键语义弱；② 用户明确决定 service 层不做幂等；③ 保持现状，无需改动。

## Deviations

None — 实现遵循 SPEC §5.1.2 / Issue-007 AC 如文。

## Tradeoffs

### T1：UpdateInvitation 冲突处理用 ConstraintName vs 不区分

- **备选 A（已选）：** 按 `pgErr.ConstraintName` 区分 pending/token_hash 冲突
- **备选 B：** 保持原实现，所有 23505 统一返回 `token_hash conflict`
- **A 优势：** 错误码语义准确，竞态时返回 409 而非 400；**A 劣势：** 依赖 PG 错误的 `ConstraintName` 字段
- **B 优势：** 代码更简单；**B 劣势：** 竞态时错误码误导（400 而非 409）
- **结论：** 选 A，与 `InsertInvitation` 保持一致，错误语义准确

## Open Questions

None — 实现与 SPEC/Issue 对齐，两轮审查通过，测试全绿。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService_Resend" -v
go test ./internal/...
```

## 边界声明

- 本 Issue 完成 `ResendTenantAdminInvitation` RPC + UpdateInvitation 竞态修复 + 终态 detail 拆分。
- `UpdateInvitation` 的 `pgconn.PgError` 匹配逻辑与 `InsertInvitation` 完全对齐。
- 终态 detail 拆分后测试仅验证业务码（`TENANT_INVITATION_SETTLED`），未断言 detail 包含 "accepted"/"rejected"（低风险测试覆盖缺口，不阻断）。
- 其余 tenant-admin RPC（ListAllTenantAdmins/GetTenantAdminDetail/角色管理等）仍返回 `UNIMPLEMENTED`，属后续 Issue 范围。
