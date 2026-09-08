# TENANT-ADMIN-ISSUE-008：跨租户管理员列表 — gRPC RPC + Core 批量查询适配器 + 网关转发

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #8）
> **完成日期：** 2026-08-26
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/ani-gateway/internal/router/`、`repo/pkg/adapters/runtime/`、`repo/pkg/ports/`、`repo/api/openapi/v1.yaml`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入、#5 可用租户列表、#6 邀请管理员、#7 重发邀请
> **Product line：** boss

## 交付内容

落地"跨租户管理员列表"端到端链路：网关 gRPC 转发 → tenant-service `ListAllTenantAdmins` RPC → Core SDK 适配器（全量拉取 admin + 批量查询邀请用户）→ ani-gateway Core handler → PG 查询。覆盖 SPEC §5.1.3 / US-003。

核心能力：合并 Core tenant-admin 用户与本地 inviting/expired 邀请标记，支持 status / is_inviting / is_expired 三选一过滤、role / source 独立过滤、search 模糊搜索（username + email + display_name）、游标分页。

### 修改文件

| 文件 | 变更 |
|------|------|
| `tenant-service/internal/service/tenant_admin_service.go` | 实现 `ListAllTenantAdmins` RPC + `listAllCoreTenantAdmins`（全量拉取）+ `batchGetUsersByFlags`（批量查询）+ `tenantAdminSearchMatch` + `pageTenantAdmins` + `adminWithTenantToProto` + role/source 过滤与校验 |
| `tenant-service/internal/service/tenant_admin_service_test.go` | 新增 `ListAll` 8 个子测试 + `BatchGetUsers` fake 实现 |
| `tenant-service/internal/repo/adapters/core/tenant_admin_svc_client.go` | 新增 `BatchGetUsers` SDK adapter（GET /admin/tenants/{id}/users/batch）+ `ListTenantAdmins` 硬编码 `role=tenant-admin` |
| `tenant-service/internal/repo/ports/core_tenant_admin.go` | `TenantAdminSvcClient` 接口新增 `BatchGetUsers(ctx, tenantID, userIDs) → map[uuid.UUID]AdminWithTenant` |
| `ani-gateway/internal/router/admin_tenant_admin_resources.go` | 新增 `GET /admin/tenants/:tenant_id/users/batch` 路由 + handler（逗号分隔 user_ids query param） |
| `ani-gateway/internal/router/tenant_admin_resources.go` | `is_inviting`/`is_expired` 仅 `true` 时设 wrapper + 解析 `role`/`source` query 参数 |
| `pkg/adapters/runtime/postgres_tenant_admin.go` | 实现 `ListUsers`（游标分页 + search 含 display_name）+ `BatchGetUsers`（`WHERE u.id = ANY($2::uuid[])`）+ `scanTenantAdminUserRow` 重构（scanner 接口复用） |
| `pkg/ports/tenant_admin.go` | `TenantAdminService` 接口新增 `BatchGetUsers(ctx, tenantID, userIDs) → []User` |
| `api/openapi/v1.yaml` | 新增 `/admin/tenants/{tenant_id}/users/batch` GET 端点定义 |
| `api/proto/tenant/v1/tenant_admin_service.proto` | `ListAllTenantAdminsRequest` 新增 `role`（field 8）+ `source`（field 9）字段 |
| `pkg/generated/pb/tenant/v1/tenant_admin_service.pb.go` | 手动同步 proto 生成：`Role`/`Source` 字段 + `GetRole()`/`GetSource()` 方法 |
| `api/openapi/services/v1.yaml` | `/tenant-admins` GET 新增 `role` + `source` query 参数 |

### 新增测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ListAll/all_admins_and_inviting_expired` | 默认模式：Core admin + 邀请中 + 已过期合并，plain user 不出现 |
| `TestTenantAdminService_ListAll/inviting_keeps_role` | is_inviting=true：仅返回邀请中用户，保留原始 role |
| `TestTenantAdminService_ListAll/expired_keeps_role` | is_expired=true：仅返回已过期用户，保留原始 role |
| `TestTenantAdminService_ListAll/filter_mutual_exclusion` | status + is_inviting 同时传 → InvalidArgument |
| `TestTenantAdminService_ListAll/role_filter` | role=auditor：仅返回 auditor 角色（admin 被过滤） |
| `TestTenantAdminService_ListAll/source_filter` | source=third_party：仅返回 oidc 来源用户（local 被过滤） |
| `TestTenantAdminService_ListAll/invalid_role` | role=superadmin → InvalidArgument |
| `TestTenantAdminService_ListAll/invalid_source` | source=unknown → InvalidArgument |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `GET /tenant-admins/admins` 支持 status / is_inviting / is_expired 三选一过滤 | service 步骤 2 校验 `exclusive > 1` → InvalidArgument | ✅ |
| 默认模式合并 Core tenant-admin + 邀请中/已过期非 admin 用户 | 步骤 5 default 分支：`listAllCoreTenantAdmins` + `batchGetUsersByFlags(pendingFlags)` | ✅ |
| is_inviting=true 仅返回邀请中用户 | 步骤 5 hasInviting 分支：按 flag 批量查询，不调 Core ListTenantAdmins | ✅ |
| is_expired=true 仅返回已过期用户 | 步骤 5 hasExpired 分支：同上 | ✅ |
| search 模糊匹配 username + email + display_name | `tenantAdminSearchMatch` 三字段匹配 + Core SQL `ILIKE` 三字段 | ✅ |
| 游标分页 (created_at DESC, id DESC) | `sort.Slice` + `pageTenantAdmins` + `types.EncodeCursor/DecodeCursor` | ✅ |
| 列表不回传 created_at/updated_at | `adminWithTenantToProto(it, false)` — includeTimestamps=false | ✅ |
| 只读端点，不写审计 | RPC 无 `writeAudit*` 调用 | ✅ |
| 网关通过 gRPC 转发，不直连 Core DB | `tenant_admin_resources.go` 调 `api.admins.ListAllTenantAdmins`（gRPC client） | ✅ |

## Design Decisions

### D1：全量拉取 + 内存排序分页（Core 侧不分页）

- **Ambiguity：** SPEC §5.1.3 要求跨租户列出 admin + 邀请中/已过期用户合并后分页。Core `/admin/tenant-users` 已有游标分页，但合并逻辑需要全量数据才能正确排序和分页。
- **Choice：** `listAllCoreTenantAdmins` 循环拉取 Core 全部分页（pageSize=100），在 service 层内存中合并邀请标记后统一排序 + 游标分页。
- **Rationale：** 用户明确要求"Core 层不要分页查询，全部查询出来"。合并后需要按 (created_at DESC, id DESC) 统一排序，若 Core 侧分页则跨页合并无法保证全局有序。

### D2：BatchGetUsers 三层贯通替代 N+1

- **Ambiguity：** 邀请中/已过期用户需要从 Core 查询用户信息（username/email/display_name/role 等）。原实现逐个调用 `GetUser` 产生 N+1 问题。
- **Choice：** 新增 `BatchGetUsers` 端点，贯通三层：
  - **Core DB**（`postgres_tenant_admin.go`）：`WHERE u.id = ANY($2::uuid[])` 批量查询
  - **Core gateway**（`admin_tenant_admin_resources.go`）：`GET /admin/tenants/:tenant_id/users/batch?user_ids=...`
  - **tenant-service SDK**（`tenant_admin_svc_client.go`）：`BatchGetUsers(ctx, tenantID, userIDs) → map[uuid.UUID]AdminWithTenant`
  - `batchGetUsersByFlags` 按 tenant_id 分组合并调用，同一租户的多个用户一次查询完成。
- **Rationale：** N+1 在 1000+ 邀请标记时会产生 1000+ 次 HTTP 调用；批量查询降为 O(租户数) 次。

### D3：BatchGetUsers 用 GET + query params（非 POST body）

- **Ambiguity：** 批量查询语义上可用 POST body 传 user_ids 列表，也可用 GET query params。
- **Choice：** 用 `GET /admin/tenants/{tenant_id}/users/batch?user_ids=id1,id2,...`，逗号分隔的 query param。
- **Rationale：** 用户明确要求"批量获取是 GET 类型"。GET 语义匹配查询操作，且利于缓存和日志审计。

### D4：Role 不默认设为 "user"，无角色返回空字符串

- **Ambiguity：** `BatchGetUsers` SQL 中用户可能没有角色记录，`COALESCE(r.name, ...)` 的默认值应为什么。
- **Choice：** `COALESCE(r.name, '') AS role` — 无角色返回空字符串，不默认设为 "user"。
- **Rationale：** 用户明确要求"不要将权限默认设置为 user，没有就为空"。真实反映数据状态，避免误导。

### D5：is_inviting/is_expired false 值不下发

- **Ambiguity：** gateway 解析 `is_inviting=false` 时是否应设 `wrapperspb.Bool(false)` 下发给 service。
- **Choice：** 仅在 `parseBool == true` 时设 `wrapperspb.Bool(true)`，false 值不设 wrapper（nil），等同于无过滤。
- **Rationale：** service 层对 `is_inviting=false` 会报 `InvalidArgument`（"only supports true"）。gateway 将 false 视为无过滤，避免误传 false 导致 400 错误。

## Deviations

### DV1：1000 条截断移除

- **Spec：** 原实现在 `listAllCoreTenantAdmins` 中有 `if len(all) >= 1000 { break }` 硬截断。
- **Implementation：** 移除 1000 截断，循环拉取全部分页直到 `NextCursor == ""`。
- **Reason：** 用户明确要求"不要 1000 隔断了"。截断会导致大型租户的管理员列表不完整。

### DV2：搜索扩展到 display_name

- **Spec：** 原 `tenantAdminSearchMatch` 仅匹配 username。
- **Implementation：** 扩展为匹配 username + email + display_name，同时 Core `ListUsers` SQL 也加 `u.display_name ILIKE`。
- **Reason：** 用户明确要求扩展搜索范围。仅匹配 username 无法覆盖按邮箱或显示名搜索的场景。

### DV3：ListTenantAdmins SDK 硬编码 role=tenant-admin

- **Spec：** 原 `ListTenantAdmins` SDK 不传 role 参数。
- **Implementation：** 硬编码 `q.Set("role", ports.TenantAdminRoleAdmin)`。
- **Reason：** Core `/admin/tenant-users` 默认仅返回 tenant-admin 角色，但显式传 role 确保语义一致；非 admin 的邀请中/已过期用户通过 `BatchGetUsers`（不按 role 过滤）单独获取。

### DV4：scanTenantAdminUser 重构为 scanner 接口

- **Spec：** 原 `scanTenantAdminUser` 只接受 `QueryRow`。
- **Implementation：** 抽取 `tenantAdminUserScanner` 接口 + `scanTenantAdminUserDest`，同时支持 `QueryRow`（单行）和 `Rows`（多行），新增 `scanTenantAdminUserRow` 用于批量查询。
- **Reason：** `BatchGetUsers` 需要从多行结果集扫描，复用同一套 Scan 逻辑避免代码重复。

## Tradeoffs

### T1：全量拉取 vs Core 侧分页

- **备选 A（已选）：** Core 全量拉取 + 内存排序分页
- **备选 B：** Core 侧分页 + service 层合并后重新分页
- **A 优势：** 合并后全局排序正确
- **A 劣势：** 大租户时内存占用高（但管理员数量通常 < 10000）
- **B 优势：** 内存友好
- **B 劣势：** 跨页合并无法保证全局排序正确性
- **结论：** 选 A — 管理员列表量级可控，全局排序正确性优先于内存优化

### T2：BatchGetUsers 返回 map vs slice

- **备选 A（已选）：** SDK 返回 `map[uuid.UUID]AdminWithTenant`
- **备选 B：** Core 返回 `[]User`，SDK 也返回 `[]User`
- **A 优势：** service 层按 user_id 查找 O(1)
- **B 优势：** 更接近 REST 语义（JSON array）
- **B 劣势：** service 层需自己建 map
- **结论：** 选 A — SDK 层建 map，service 层直接按 key 查找

### T3：ListUsers 保留 vs 删除

- **备选 A（已选）：** 保留 `ListUsers`
- **备选 B：** 删除 `ListUsers`，全部用 `BatchGetUsers`
- **A 优势：** gateway `GET /admin/tenant-users` 端点仍需 `ListUsers`；测试也引用
- **B 优势：** 减少代码面
- **B 劣势：** 破坏 gateway 既有端点
- **结论：** 选 A — 用户确认 `ListUsers` 仍有使用方，不删除

## Open Questions

None — 实现遵循 SPEC §5.1.3 和用户明确指令。三轮 review-it 均无阻塞性发现。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService" -v
# 27 sub-tests PASS:
#   ListAvailableTenants + NilClient: PASS
#   Invite (5 sub-tests): PASS
#   Resend (5 sub-tests): PASS
#   ListAll (8 sub-tests: all_admins_and_inviting_expired, inviting_keeps_role,
#            expired_keeps_role, filter_mutual_exclusion, role_filter,
#            source_filter, invalid_role, invalid_source): PASS
#   Unimplemented (9 sub-tests): PASS

# review-it 三轮
# Round 1: 4 Findings (N+1, 1000 truncation, search scope, gateway false) → all fixed
# Round 2: 3 observations (comment POST→GET, SQL display_name, OpenAPI) → all fixed
# Round 3: clean — no actionable findings
```

## 边界声明

- 本 Issue 完成 `ListAllTenantAdmins` 端到端链路（RPC + Core 批量查询适配器 + 网关转发 + Core handler/SQL + 测试）。
- `TenantAdminService` 其余 8 个 RPC（详情/角色/密码/禁用启用删除/审计）仍返回 `UNIMPLEMENTED`，属后续 Issue 范围。
- `fakeTenantAdminCoreClient` 新增 `BatchGetUsers` 方法，复用 `users` map，无新增字段。
