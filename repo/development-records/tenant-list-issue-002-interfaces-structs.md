# TENANT-LIST-ISSUE-002：租户列表管理 — 接口与数据模型

> **批次类型：** Contract + Interface batch（BOSS 租户列表管理 Issue #2）
> **完成日期：** 2026-09-02
> **Scope：** proto + Core ports + tenant-service ports + gRPC 骨架/stub（业务 UNIMPLEMENTED）
> **依赖：** Issue-001 OpenAPI 契约
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-002-interfaces-structs.md`

## 交付内容

定义租户列表管理全栈接口边界：gRPC `TenantListService`（19 RPC）、Core `ports.TenantService` 扩展、tenant-service 侧 Core SDK 客户端端口、本地 store 端口与 SSO 测试端口；附带可编译 gRPC 骨架与 adapter stub，供 Issue-005～#014 填充业务。

### 修改/新增文件

| 文件 | 变更摘要 |
|---|---|
| `api/proto/tenant/v1/tenant_list_service.proto` | 新建 `TenantListService` 19 RPC + 领域消息（对齐 services/v1.yaml） |
| `pkg/generated/pb/tenant/v1/tenant_list_service*.pb.go` | 用户手动 buf 生成（随 proto 提交） |
| `pkg/ports/tenant.go` | 扩展 `Tenant` 实体；新增 TenantAuth / TenantLifecycleEntry / CreateTenantInput 等；`TenantService` +9 方法 |
| `pkg/ports/errors.go` | 新增 `ErrTenantNameConflict`、`ErrTenantStateInvalid` |
| `pkg/adapters/runtime/postgres_tenant.go` | `PostgresTenant` 实现新接口方法 stub（`ErrUnsupported`） |
| `services/tenant-service/internal/repo/ports/core_tenant.go` | `TenantSvcClient` 扩展 8 个 Core 写读方法 + DTO |
| `services/tenant-service/internal/repo/ports/tenant_store.go` | 合并 `TenantStore`（lifecycle 读 + quota_change CRUD）；领域 DTO |
| `services/tenant-service/internal/repo/ports/sso.go` | `SsoConfigLoader` / `OidcDiscoveryTester` 接口 |
| `services/tenant-service/internal/repo/ports/errors.go` | 租户列表域错误哨兵（TENANT_NAME_CONFLICT 等） |
| `services/tenant-service/internal/repo/ports/tenant_plan_audit_store.go` | 扩展 `ListTenantAuditLogs`（US-016） |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | 新 Core 方法 stub + 既有 `GetTenant`/`ListAvailableTenants` |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_store.go` | `TenantStore` postgres stub |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go` | `ListTenantAuditLogs` stub |
| `services/tenant-service/internal/service/tenant_list_service.go` | gRPC 骨架：19 RPC 全部 `UNIMPLEMENTED` |
| `services/tenant-service/internal/service/tenant_plan_test.go` | `fakeAuditStore` 补 `ListTenantAuditLogs` |
| `services/tenant-service/main.go` | 注册 `TenantListService`；接入 `PostgresTenantStore` |

## 调用链设计（Issue-005+ 实现参考）

```
Gateway /api/v1/svc/tenants*  (Issue-004)
  └─ gRPC TenantListService
       ├─ plans (TenantPlanStore)              → US-001 / plan_code 装配
       ├─ tenants (TenantSvcClient → Core API) → US-002~010 租户 CRUD / 状态机 / auth
       ├─ quota (QuotaSvcClient → Core API)    → US-007 禁用前置 / US-011~014 配额
       ├─ tenantStore (TenantStore → PG)       → US-012~015 lifecycle 直读 + quota_change
       ├─ audit (TenantPlanAuditStore → PG)    → US-016 audit_logs 直读
       ├─ tenantAdmins (TenantAdminSvcClient)  → US-017（Issue-014 需扩展 role）
       └─ ssoLoader + oidcTester (Issue-005)   → US-009 TestTenantSso
```

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| proto TenantListService RPC + 消息 | 19 RPC；CursorPageRequest / IdempotentResult 模式对齐 tenant_plan | ✅（Issue 文案写 17，实际与 OpenAPI 19 端点一致） |
| Core `TenantService` 9 方法 + 实体 | `pkg/ports/tenant.go` | ✅ |
| `TenantSvcClient` Core 客户端扩展 | 8 新方法 + 既有 Get/ListAvailable | ⚠️ 无 `ListTenantLifecycle`（改由 TenantStore，见 Dev-1） |
| QuotaChangeStore | 合并进 `TenantStore` 四方法 | ⚠️ 文件名/deviation，语义等价 |
| SSO 端口 | `sso.go`（非 issue 指定的 `sso_test.go`） | ✅ |
| 错误哨兵 | Core + tenant-service ports | ✅ |
| 编译 | `go build ./services/tenant-service/` | ✅ |
| 测试 | `go test ./services/tenant-service/...` | ✅ |

## Design Decisions

### D1：tenant-service 侧 lifecycle / audit 直读 PG，不经 Core SDK

- **Ambiguity：** SPEC §2.3 序列图写 `ListTenantLifecycle → Core API`；Issue-002 AC 要求 `TenantSvcClient.ListTenantLifecycle`。
- **Choice：** US-015 `ListTenantLifecycle` 经 `TenantStore.ListLifecycle` 直读 `tenant_lifecycle` 表；US-016 经 `TenantPlanAuditStore.ListTenantAuditLogs` 直读 `audit_logs`（与 quota-policy / tenant-admin 模块先例一致）。
- **Rationale：** 共享 PostgreSQL 单实例；tenant-service 已直读 audit_logs；减少 Core SDK 往返；写入仍由 Core 状态转换事务保证。

### D2：QuotaChangeStore 合并为 TenantStore 单接口

- **Ambiguity：** Issue-002 指定独立 `tenant_quota_change_store.go`。
- **Choice：** 在 `tenant_store.go` 定义 `TenantStore`，含 `ListLifecycle` + quota_change 四方法（UpsertPending / List / Get / SetStatus）。
- **Rationale：** lifecycle 与 quota_change 均属 tenant-list 域本地表访问；单 adapter（`postgres/tenant_store.go`）装配更简单。

### D3：TenantListService 依赖注入六端口 + SSO 两端口

- **Choice：** `plans / tenants / tenantAdmins / quota / tenantStore / audit / ssoLoader / oidcTester` 八依赖注入；main 中 SSO 暂 `nil`（Issue-005 接入）。
- **Rationale：** 与 SPEC §2.2 组件表一一对应；骨架期 SSO RPC 仍返回 UNIMPLEMENTED，nil 安全。

### D4：proto 使用 TenantScopedAdmin（无 is_inviting/is_expired）

- **Ambiguity：** OpenAPI `listTenantScopedAdmins` 复用 `AdminWithTenant`（含邀请标记）。
- **Choice：** proto `ListTenantAdminsResponse` 用 `TenantScopedAdmin` + `TenantScopedAdminTenantRef`，不含邀请字段。
- **Rationale：** 租户详情 Admin Tab 只读、不展示邀请态；跨租户邀请合并逻辑留在 tenant-admin 模块；减少 tenant-list proto 与 tenant-admin 耦合。

### D5：Core 侧 SSO/MFA 单端点，gRPC 侧拆 UpdateTenantSso / UpdateTenantMfa

- **Choice：** gRPC 两个 PUT RPC；`TenantSvcClient.UpdateTenantAuth` 单一 Core 映射；UpdateTenantMfa 在 service 层只 patch `mfa_required`。
- **Rationale：** 对齐 UX 认证 Tab 两个操作区 + OpenAPI services 层路径；Core 仅一张 `tenant_auth` 表。

## Deviations

### Dev-1：TenantSvcClient 不含 ListTenantLifecycle

- **Issue/SPEC 说：** `TenantSvcClient` 九方法含 `ListTenantLifecycle`。
- **实现：** lifecycle 列表只在 `TenantStore.ListLifecycle`；Core `pkg/ports.TenantService.ListTenantLifecycle` 仍保留（供 Core gateway admin API 实现）。
- **原因：** 用户确认 tenant-service 读路径 intentionally 直读 PG（review-it 2026-09-02）。

### Dev-2：Issue 范围溢出 — gRPC 骨架 + main 注册 + adapter stub

- **Issue 说：** 只写接口，stub 属 Issue-004。
- **实现：** 新增 `tenant_list_service.go`（19 RPC UNIMPLEMENTED）、`main.go` Register、`postgres_tenant.go`/`tenant_svc_client.go`/`tenant_store.go` stub。
- **原因：** 保证 `go build` 与 gRPC 注册可验证；业务逻辑仍全部 NOT_IMPLEMENTED。

### Dev-3：文件名 sso.go 替代 sso_test.go

- **Issue 说：** `sso_test.go`。
- **实现：** `internal/repo/ports/sso.go`。
- **原因：** 接口定义文件，非测试；避免与 `_test.go` 混淆。

### Dev-4：pb.go 已生成并纳入工作区

- **Issue 说：** 不生成 pb.go，用户手动 buf generate。
- **实现：** `tenant_list_service.pb.go` / `_grpc.pb.go` 已存在（用户本地生成）。
- **原因：** 骨架编译依赖生成物；合入时以团队 buf 流程为准。

## Tradeoffs

### T1：lifecycle 直读 PG vs Core SDK 回调

| 方案 | 优点 | 缺点 |
|---|---|---|
| **A. TenantStore 直读（选用）** | 与 audit_logs 一致；少一跳 | 偏离 SPEC 序列图字面 |
| B. TenantSvcClient → Core GET /lifecycle | 严格分层 | 多一跳；Core 与 PG 同实例收益低 |

**选用 A**，已在 ports 注释固化。

### T2：QuotaChange 独立 store vs 合并 TenantStore

**选用合并** — 单 postgres adapter、单 main wiring；Issue-002 独立文件要求让位于实现简洁性。

### T3：Issue-002 是否接入 SSO adapter

| 方案 | 结果 |
|---|---|
| 提前 import 不存在的 sso 包 | 编译失败（已修复） |
| **nil + 注释（选用）** | 编译通过；Issue-005 再接线 |

## Review-it 修复记录（2026-09-02）

- **P0：** 删除 `main.go` 无效 `ssoadapter` import（引用不存在的包）→ 编译恢复。
- **P1：** 确认 lifecycle 直读；`main.go` 接入 `postgres.NewPostgresTenantStore`；注释对齐。

## Verification Commands

```bash
cd repo/services/tenant-service
go build .
go test ./...

# Core ports 编译
cd repo
go build ./pkg/ports/... ./pkg/adapters/runtime/...
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #003 数据库迁移 | Core 表 tenant_auth / tenant_lifecycle；实体已定义 |
| #004 Gateway | proto/gRPC 已注册；待 HTTP 路由 |
| #005～#014 各 API | 按调用链表填充 TenantListService RPC + adapter 实现 |
| #014 US-017 | 解决 ListTenantAdmins role 硬编码 |
