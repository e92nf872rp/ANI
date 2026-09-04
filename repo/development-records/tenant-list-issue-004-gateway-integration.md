# TENANT-LIST-ISSUE-004：租户列表管理 — 网关集成 + service→Core 骨架

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #4）
> **完成日期：** 2026-09-03
> **Scope：** Gateway svc/Core admin 路由 + tenant-service gRPC 骨架 + Core/adapters stub；最小闭环 `GetTenantDetail`
> **依赖：** Issue-001（OpenAPI）、Issue-002（接口）、Issue-003（迁移；stub 可不触库）
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-004-gateway-integration.md`
> **分支：** `tenant-list`

## 交付内容

打通 BOSS → Gateway → tenant-service（gRPC）→ Core 的端到端骨架：

1. **Gateway svc：** 新建 `tenant_list_resources.go`，注册 19 个 `/api/v1/svc/tenants*`；`tenantCallCtx`、幂等头透传、`mapTenantListError`（SPEC §6.1）
2. **Gateway Core admin：** 扩展 `admin_tenant_resources.go`，注册 9 个新 `/admin/tenants*`；错误码 `TENANT_NAME_CONFLICT` / `TENANT_STATE_INVALID` / `NOT_IMPLEMENTED`
3. **Authz：** `zz_generated_core_policies.go` 含新 Core admin 路径；svc 侧对齐 `tenant-plans`（legacy CheckPermission + OpenAPI summary 角色说明）
4. **tenant-service：** 租户列表 RPC 挂在既有 `TenantService`（非独立 `TenantListService`）；18 个 stub → `NOT_IMPLEMENTED`；**`GetTenantDetail` 最小闭环**经 `TenantSvcClient.GetTenant`
5. **Adapters：** Core `TenantSvcClient` 新方法 stub；`decodeTenant` 解析 additive 字段；`PostgresTenant` 新方法 `ErrUnsupported`；`TenantStore` 仅 `tenant_quota_change`
6. **main：** `NewTenantService(..., tenantStore, audit, …, nil, nil)`（SSO 端口暂 nil）

### 修改/新增文件

| 文件 | 变更摘要 |
|---|---|
| `services/ani-gateway/internal/router/tenant_list_resources.go` | 新建 19 svc 路由 + 错误映射 + JSON 组装 |
| `services/ani-gateway/internal/router/tenant_list_resources_test.go` | 路由完整性 + 错误映射单测 |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | 9 Core 端点 + 可空字段响应 helper |
| `services/ani-gateway/internal/router/admin_tenant_resources_test.go` | Core admin 错误码 / 响应字段单测 |
| `services/ani-gateway/internal/router/router.go` | `registerTenantList` |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | Core admin tenants 策略条目 |
| `services/tenant-service/internal/service/tenant_service.go` | 列表 RPC stub + `GetTenantDetail` 闭环 |
| `services/tenant-service/internal/service/tenant_test.go` | 闭环 / NotFound / 错误映射 |
| `services/tenant-service/internal/service/errors.go` | 租户列表域哨兵 → gRPC 业务码 |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | 新方法 stub + `decodeTenant` 扩展字段 |
| `services/tenant-service/internal/repo/ports/{core_tenant,tenant_store}.go` | 端口注释：lifecycle 走 Core |
| `pkg/adapters/runtime/postgres_tenant.go` | 9 新方法 stub（`ErrUnsupported`） |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 19 svc 端点注册 + tenantCallCtx / 幂等 / mapTenantListError | `tenant_list_resources.go` + 单测 | ✅ |
| RBAC 对齐既有 svc 模式 | summary 角色文案 + legacy `CheckPermission`（同 tenant-plans） | ✅ |
| 9 Core admin 端点 + 新错误码 | `admin_tenant_resources.go` + 单测 | ✅ |
| Core store 扩展方法 stub | `PostgresTenant` → `ErrUnsupported`（等价 NOT_IMPLEMENTED） | ✅ |
| gRPC 骨架 + Register | 并入 `TenantService.Register`；stub → `codes.Unimplemented` | ✅（形态偏离见 Dev-1） |
| adapters / main 可编译 | SSO 端口 nil；QuotaChange 经 `TenantStore` | ✅ |
| 最小闭环 GetTenantDetail | Gateway → gRPC → Core GetTenant | ✅ |
| 未实现端点明确 501 | `NOT_IMPLEMENTED` 映射 | ✅ |
| 单测 + route contract | router / service / core adapter 测试；route contract 0 error | ✅ |

## Design Decisions

### D1：租户列表 RPC 并入 `TenantService`，不单独 `TenantListService`

- **Ambiguity：** Issue 写 `tenant_list_service.go` + `UnimplementedTenantListServiceServer`；Issue-002 后期 proto 已合并进 `TenantService`（`tenant_plan.proto`）。
- **Choice：** 在既有 `TenantService` 上增加列表 RPC；Gateway 使用 `TenantServiceClient`。
- **Rationale：** 与生成物一致，避免双 service 注册与双 dial 语义分叉；plans 与 list 共用同一 gRPC 服务定义。

### D2：最小闭环只实现 `GetTenantDetail`

- **Ambiguity：** Issue 要求骨架 +「一个」最小闭环。
- **Choice：** 仅 `GetTenantDetail` 调 Core `GetTenant`；其余列表 RPC 一律 `NOT_IMPLEMENTED`。
- **Rationale：** 复用已有 Core 读路径即可验证全链路；业务写路径留给 Issue-005+。

### D3：Core store 扩展落在 `postgres_tenant.go`，不新建 `postgres_tenant_store.go`

- **Ambiguity：** Issue / SPEC 文件名写 `postgres_tenant_store.go`。
- **Choice：** 扩展现有 `PostgresTenant`（`ports.TenantService` 实现者）。
- **Rationale：** 避免双实现与 gateway 注入分叉；新方法 stub 与既有 `GetTenant` 同对象。

### D4：Admin / lifecycle JSON 用 map 字面量 + helper，避免 if/else 填空

- **Choice：** `nullIfEmpty` / `derefStringOrNil` / `timePtrRFC3339OrNil`；`user_count` / `admin_count` 始终返回（含 0）。
- **Rationale：** 响应组装可读；与 listTenants 始终带 `admin_count` 一致。

### D5：lifecycle 读路径继续走 Core SDK

- **Choice：** `TenantSvcClient.ListTenantLifecycle`；`TenantStore` 仅 `tenant_quota_change`。
- **Rationale：** 对齐 SPEC §2.3 与 Issue-002 最终确认（Core 表 + Core API）；与 freeze/disable 同事务写入同层读取。

## Deviations

### Dev-1：无独立 `TenantListService` / `tenant_list_service.go`

- **Issue 说：** 新建 `TenantListService` 栅栏服务并 Register。
- **实现：** RPC 并入 `TenantService`。
- **原因：** proto 已合并；见 D1。

### Dev-2：Core stub 哨兵用 `ErrUnsupported` 而非 `ErrNotImplemented`

- **Issue 说：** `ports.ErrNotImplemented`（或等价）。
- **实现：** Core runtime 用既有 `ErrUnsupported` → Gateway `NOT_IMPLEMENTED` 501。
- **原因：** Core ports 惯例；语义等价。

### Dev-3：Gateway 双 dial `TenantServiceClient`（plans + list）

- **Issue 说：** 未要求合并连接。
- **实现：** `registerTenantPlans` 与 `registerTenantList` 各建一条 gRPC 连接。
- **原因：** 跟随既有 plans 装配，未做连接复用重构（非功能阻塞）。

### Dev-4：`TenantScopedAdmin` 响应仍缺 `is_inviting` / `is_expired`

- **OpenAPI 说：** `AdminWithTenant` required 含邀请标记。
- **实现：** Gateway 已映射 proto 有的 `source` / `last_login_at`；邀请字段 proto 无定义。
- **原因：** Issue-002 D4；完整对齐留给 Issue-014。

## Tradeoffs

### T1：独立 TenantListService vs 合并 TenantService

| 方案 | 优点 | 缺点 |
|---|---|---|
| A. 独立 ListService | 严格按 Issue 文件名 | 与合并后 proto 冲突；双 Register |
| **B. 并入 TenantService（选用）** | 单客户端、与 proto 一致 | Issue 文件名/结构偏离 |

### T2：闭环字段完整度

| 方案 | 结果 |
|---|---|
| 假数据拼详情 | 测不通真实 Core |
| **Core GetTenant 最小字段（选用）** | 真链路；`plan_code`/计数等后续 Issue 补 |

### T3：Services RBAC 注解 vs legacy

| 方案 | 结果 |
|---|---|
| 为 19 路由补 `x-ani-authz` + 生成策略 | 超出本 Issue；需 authz 生成流水线 |
| **legacy CheckPermission + summary 角色（选用）** | 与 tenant-plans 一致，可交付 |

## Review-it 修复记录（2026-09-03）

- **P1：** `listTenantAdmins` 把 `*wrapperspb.StringValue` 直接写入 JSON（会变成对象），并漏 `source` / `last_login_at` → unwrap + 补字段。
- **P1：** `decodeTenant` 不解析 `contact_email` / `frozen_at` / `disabled_at` → 补解析，避免 Core 返回扩展字段时闭环丢失。
- **风格：** `admin_tenant_resources` 可空字段改为 map 字面量 + helper（用户要求去掉 if/else 赋值）。

## Verification Commands

```bash
cd repo
go test ./services/ani-gateway/internal/router/ -count=1 -run "TenantList|AdminTenant|ToAdminTenant"
go test ./services/tenant-service/internal/service/ -count=1 -run "GetTenantDetail"
go test ./services/tenant-service/internal/repo/adapters/core/ -count=1
python scripts/validate_services_route_contract.py
# 期望：Services route contract … 0 error(s)
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #005 CreateTenant | svc create + Core CreateTenant 实现 |
| #006 List/Detail | ListTenants + GetTenant 扩展字段 / plan_code |
| #007–#013 | 对应 RPC stub → 业务实现 |
| #014 US-017 Admins | ListTenantAdmins + 邀请标记 / role 过滤 |
