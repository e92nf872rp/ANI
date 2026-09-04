# TENANT-LIST-ISSUE-014：租户列表管理 — 租户内管理员列表（tenant-admin ∪ inviting）

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #14）
> **完成日期：** 2026-09-04
> **Scope：** US-017 — `ListTenantAdmins` / `GET /api/v1/svc/tenants/{tenantId}/admins`；默认集合 = `tenant-admin` ∪ `inviting`；只读、无幂等键、不写审计
> **依赖：** Issue-004（网关路由）；租户管理员模块 `TenantAdminSvcClient` / `TenantAdminStore` / 邀请表（issue-admin-002/003/008）
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-014-tenant-admins-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入（含 review-it：OpenAPI 400、role/status 校验先于 GetTenant）

## 交付内容

1. **默认集合：** Core 拉取该租户全部 `tenant-admin` ∪ 本地 `tenant_admin_invitation.status=inviting` 目标用户；去重（admin 且 inviting → 一行且 `is_inviting=true`）；**不含**未邀请普通成员；**不含**仅 `expired` 邀请
2. **编排：** 校验 role/status → `GetTenant`（404）→ `ListInvitationFlags(inviting)` → `listAllCoreTenantAdmins` + `BatchGetUsers` 合并 → search/role 过滤 → 内存 keyset 分页
3. **字段：** Proto `TenantScopedAdmin` 补 `is_inviting`/`is_expired`；网关 JSON 透传；产品最小集 id/username/display_name/role/status；无 `permissions[]`
4. **依赖注入：** `NewTenantService` 增加 `adminStore ports.TenantAdminStore`；`main.go` 传入既有 `PostgresTenantAdminStore`
5. **OpenAPI：** `listTenantScopedAdmins` 描述对齐 admin∪inviting；补 400 `VALIDATION_FAILED`

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `api/proto/tenant/v1/tenant_list_service.proto` | `TenantScopedAdmin.is_inviting` / `is_expired` |
| `pkg/generated/pb/tenant/v1/*` | `buf generate` |
| `services/tenant-service/internal/service/tenant_service.go` | `ListTenantAdmins` + `toProtoTenantScopedAdmin`；注入 `adminStore` |
| `services/tenant-service/main.go` | 接线 `tenantAdmin` store |
| `services/tenant-service/internal/service/tenant_test.go` | AdminAndInviting / Dedup / Page / NotFound / InvalidRole / NoWriteAudit；`NewTenantService` 多一参 |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | 响应透传 `is_inviting` / `is_expired` |
| `api/openapi/services/v1.yaml` | 描述 + 400 |
| Issue/PRD/UX/SPEC | 产品语义同步（admin ∪ inviting；字段表） |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 默认 tenant-admin ∪ inviting；无未邀请 plain user | `ListTenantAdmins_AdminAndInviting` | ✅ |
| admin+inviting 去重且 `is_inviting=true` | `ListTenantAdmins_DedupAdminInviting` | ✅ |
| 必含 id/username/display_name/role/status | AdminAndInviting 字段断言 | ✅ |
| `is_inviting`/`is_expired` 透传；无 permissions[] | proto + gateway；未映射 permissions | ✅ |
| limit/cursor 翻页 | `ListTenantAdmins_Page` | ✅ |
| 非法 role → 400 | `InvalidRole`；先于 GetTenant | ✅ |
| 404 TENANT_NOT_FOUND | `NotFound` | ✅ |
| 只读不写审计 | `NoWriteAudit` | ✅ |
| OpenAPI 400/404 | services/v1.yaml | ✅ |

## Design Decisions

### D1：默认集合含 inviting、不含 expired

- **Ambiguity：** 跨租户 `GET /tenant-admins` 含 inviting+expired；详情 Tab 是否对齐。
- **Choice：** 仅 `inviting`；expired 留给跨租户管理页。
- **Rationale：** 产品确认「正在邀请中」；减少详情 Tab 噪音。

### D2：复用 `TenantAdminService` 私有 helper（同包内联构造）

- **Ambiguity：** 复制合并逻辑 vs 抽公共函数 vs 委托已有 RPC。
- **Choice：** `&TenantAdminService{core, store, tenants}` 调用 `listAllCoreTenantAdmins` / `batchGetUsersByFlags`。
- **Rationale：** 与跨租户列表行为一致、改动面最小；不新增 gRPC 转发层。

### D3：用户权限 = `role`，非 `permissions[]`

- **Ambiguity：** 「用户权限」可指角色或细粒度权限矩阵。
- **Choice：** 列表返回 `role`；矩阵仍走 `GET .../admins/{userId}/role`。
- **Rationale：** 对齐 UX 表格与 `AdminWithTenant`；避免 N 次权限查询。

### D4：非法 role/status 先于 GetTenant

- **Choice：** 与 lifecycle/audit 一致，非法过滤不打 Core。
- **Rationale：** review-it 收口；坏参 + 坏 tenant 优先 400。

### D5：Proto 用 `TenantScopedAdmin` 而非直接复用 `AdminWithTenant` message

- **Choice：** 扩展既有 list proto 字段；网关/OpenAPI 仍按 AdminWithTenant 形状返回。
- **Rationale：** TenantService 契约已存在；少改 RPC 类型名。

## Deviations

### Dev-1：RPC 在 `TenantService`，非独立 list service 文件

- **Issue Scope：** 允许 `internal/service/`。
- **实现：** `tenant_service.go`（004 起合并）。
- **原因：** 网关已绑 TenantServiceClient。

### Dev-2：相对早期 SPEC「仅复用 listTenantUsers」

- **旧 SPEC：** 参数对齐 listTenantUsers、无邀请合并。
- **实现：** Core admin 列表 + 邀请表合并（与更新后 Issue/PRD/SPEC 一致）。
- **原因：** 产品补「邀请中」后 Spec 决策表已改。

### Dev-3：未做 live PG 集成造数

- **Issue 测试：** 集成造 2 admin + inviting + plain。
- **实现：** fake Core/store 单测覆盖同一断言。
- **原因：** 聚焦单元边界；live 留 Follow-ups。

## Tradeoffs

### T1：详情 Tab 是否含 expired

| 方案 | 利 | 弊 |
|---|---|---|
| A 仅 inviting（选用） | Tab 更干净；与产品表述一致 | 过期邀请需跳转跨租户页 |
| B 对齐跨租户（admin+inviting+expired） | 两入口一致 | Tab 更嘈杂 |

选用 A：按 Issue-014 明确 AC。

### T2：全量拉取再内存分页 vs Core 真分页

| 方案 | 利 | 弊 |
|---|---|---|
| A 内存合并分页（选用） | 邀请行可并入同一游标；与跨租户列表同模式 | 管理员很多时延迟升高 |
| B 仅透传 Core 游标 | 轻 | 邀请用户难稳定插入分页 |

选用 A：BOSS 租户/管理员量级可接受。

### T3：GetTenant 404 vs 空列表

| 方案 | 利 | 弊 |
|---|---|---|
| A GetTenant（选用） | 与详情/其它子资源一致 | 多一跳 |
| B 空列表 | 零存在性检查 | 与 OpenAPI 404 冲突 |

选用 A。

## Verification commands run

```text
cd repo/api/proto && buf generate --template buf.gen.yaml .
cd repo/services/tenant-service && go test ./internal/service/ -count=1 -run ListTenantAdmins
cd repo/services/ani-gateway && go test ./internal/router/ -count=1 -run TenantList
python scripts/validate_component_imports.py --root .
# review-it：clean — OpenAPI 400 + 校验顺序已修
```

## Follow-ups

- [ ] Feature batch 四文件：README / CURRENT-SPRINT / ANI-06（合入前）
- [ ] 可选：live PG 集成（2 admin + inviting + plain）
- [ ] 可选：抽 `listScopedTenantAdmins` 包级函数，去掉内联 `TenantAdminService` 构造
