# TENANT-LIST-ISSUE-006：租户列表管理 — 列表与详情

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #6）
> **完成日期：** 2026-09-03
> **Scope：** US-003 `ListTenants` + US-004 `GetTenantDetail`（Core 读扩展 + service plan_code 装配 + Gateway svc JSON）
> **依赖：** Issue-001（契约）、Issue-003（迁移/tenant_auth）、Issue-004（路由骨架）；US-001 已归 Issue-005
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-006-tenant-list-detail-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入改动（含 review-it 对 `admin_count` 的 `roles.tenant_id IS NULL` 收口）

## 交付内容

1. **US-003 ListTenants：** Gateway `GET /svc/tenants` → gRPC `ListTenants` → Core `ListTenants`（status/search 下推 + keyset 游标 + LATERAL `admin_count`）→ service 批量 `MapPlanCodes` 装配 `plan_code`；列表**不含** auth；不写审计
2. **US-004 GetTenantDetail：** Gateway `GET /svc/tenants/{id}` → `GetTenantDetail` → Core `GetTenant`（同查询 LEFT JOIN `tenant_auth` + LATERAL user/admin count）→ `MapPlanCodes`；响应嵌套 `auth: {sso_enabled, mfa_required}`（缺行双 false）
3. **Core：** `PostgresTenant.ListTenants` / `GetTenant` 扩展字段；`admin_count` 仅计平台内置 `tenant-admin`（`r.tenant_id IS NULL AND r.name = 'tenant-admin'`）
4. **Gateway：** `tenantListItemJSON` / `tenantDetailJSON`；Core admin `listTenants` 无 auth，`toAdminTenantResponse` 含 auth（与 svc 详情对齐摘要）

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `pkg/ports/tenant.go` | `Tenant` / `TenantListItem` additive：counts、Auth、ListTenantsFilter |
| `pkg/adapters/runtime/postgres_tenant.go` | `ListTenants` keyset+LATERAL；`GetTenant` JOIN auth + counts |
| `pkg/adapters/runtime/postgres_tenant_test.go` | List/Get 单测（含非法 status/cursor/id） |
| `services/tenant-service/internal/service/tenant_service.go` | `ListTenants` / `GetTenantDetail` 编排 + plan_code |
| `services/tenant-service/internal/service/tenant_test.go` | plan 装配 / 详情全字段 / auth 缺省 / 404 |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | ListTenants SDK；decodeTenant 透传 auth |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | list/detail handler + JSON 映射 |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | Core admin 列表/详情字段对齐（详情含 auth） |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 游标分页 limit 默认 20 / 最大 100；keyset created_at DESC,id DESC | Core `ListTenants` + `types.EncodeCursor` | ✅ |
| status / search 下推 Core | WHERE 参数化 `$2`/`$3` + ILIKE | ✅ |
| LATERAL admin_count、无 N+1 | 单 SQL LATERAL | ✅ |
| plan_code 批量装配（非 Core、非逐行） | `MapPlanCodes` + `ListTenants_AssemblesPlanCode` | ✅ |
| 列表字段完整且不含 auth | `tenantListItemJSON` | ✅ |
| 详情扩展字段 + auth 摘要同查询 | GetTenant LEFT JOIN + 单测 | ✅ |
| auth 缺行双 false | COALESCE + `GetTenantDetail_AuthDefaults` | ✅ |
| 404 → TENANT_NOT_FOUND | `GetTenantDetail_NotFound` | ✅ |
| 只读不写审计 | 无 audit 调用 | ✅ |
| 真库集成（翻页 / 双 admin / auth 造数） | 本批以单测 + fake 为主 | ⚠️ 未跑 live（见 Open Questions） |

## Design Decisions

### D1：US-001 不在本 Issue；本批仅 US-003/US-004

- **Ambiguity：** 初版 Issue-006 曾含 available-plans。
- **Choice：** 可用套餐归 Issue-005；本批只做列表+详情读路径。
- **Rationale：** 创建向导数据源与 Create 同批更内聚；列表页不依赖 available-plans。

### D2：plan_code 只在 Services 装配，Core 只回 plan_id

- **Choice：** Core 列表/详情不 JOIN `tenant_plans`；service 对本页 plan_id 去重后 `MapPlanCodes`。
- **Rationale：** SPEC §8.2；套餐属 Services 域，避免 Core 依赖 plan_code 产品语义。

### D3：详情 auth 摘要同查询 LEFT JOIN，禁止二次 RPC

- **Choice：** Core `GetTenant` 一次查出 `sso_enabled`/`mfa_required`；缺行 COALESCE false。
- **Rationale：** Issue AC + 详情页首屏免二次请求；完整 Auth 仍归 Issue-9。

### D4：admin_count 对齐创建路径的平台内置角色

- **Ambiguity：** Issue 只写 `roles.name='tenant-admin'`，未写 `tenant_id IS NULL`。
- **Choice：** review-it 后补上 `r.tenant_id IS NULL`（列表 LATERAL 与详情一致）。
- **Rationale：** 与 CreateTenant 绑定 `tenant-admin` 查找条件一致，避免租户本地同名角色多计。

### D5：user_count 仅排除软删，不排除 disabled 用户

- **Choice：** `COALESCE(u.is_deleted, FALSE) = FALSE`；含 status=disabled 的用户。
- **Rationale：** Issue 写「统计 users 表」；未要求按用户状态再过滤。

## Deviations

### Dev-1：实现落在 `TenantService` / `postgres_tenant.go`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`、`postgres_tenant_store.go`。
- **实现：** 延续 Issue-004：RPC 并入 `TenantService`；Core 扩展既有 `PostgresTenant`。
- **原因：** proto / 注入边界已合并，避免双实现。

### Dev-2：真库集成测试未作为强制门禁跑通

- **Issue 说：** >limit 翻页、造 2 管理员、auth 造数与库一致。
- **实现：** Core/service 单测 + fake rows；live PG 留给部署环境。
- **原因：** 与 Issue-005 同策略——本地可复现闭环优先。

### Dev-3：search ILIKE 不转义 `%` / `_`

- **Issue/SPEC：** name/display_name ILIKE OR；未规定字面量转义。
- **实现：** 参数化拼接 `'%' || $3 || '%'`，用户输入中的 `%`/`_` 仍为通配符。
- **原因：** 防注入已满足；字面量匹配属 UX/P2，未在本批加 ESCAPE。

### Dev-4：套餐已删时 plan_code 为空串而非错误

- **SPEC 字段：** items 含 plan_code。
- **实现：** `MapPlanCodes` 缺失 key → `""`，列表/详情仍成功。
- **原因：** 历史绑定被删套餐时整页失败更差；前端用「—」兜底。

## Tradeoffs

### T1：admin_count — 仅 name vs name + tenant_id IS NULL

| 方案 | 优点 | 缺点 |
|---|---|---|
| 仅 `name='tenant-admin'` | 与 Issue 字面一致 | 可能计到租户本地同名角色 |
| **name + tenant_id IS NULL（选用）** | 与创建绑定一致 | 略严于 Issue 字面 |

### T2：auth 装配 — Core JOIN vs service 二次 GetTenantAuth

| 方案 | 结果 |
|---|---|
| service 再调 Auth API | 详情 N+1；违反 Issue AC |
| **Core 同查询 LEFT JOIN（选用）** | 一次往返；与 Issue-9 完整 Auth 端点解耦 |

### T3：plan_code — Core JOIN plans vs Services MapPlanCodes

| 方案 | 结果 |
|---|---|
| Core JOIN | 少一跳，但 Core 耦合 Services 套餐表 |
| **Services 批量装配（选用）** | 符合分层；多一次 store 查询（按页去重） |

## Review-it 修复记录（2026-09-03）

- **P1：** `admin_count` LATERAL 补 `AND r.tenant_id IS NULL`（GetTenant + ListTenants），与创建绑角色一致。
- **拒绝/延后：** ILIKE 通配符转义、user_count 排除 disabled、plan_code 空串策略、读路径 5s 超时 — 见上 Deviations / Tradeoffs。

## Verification Commands

```bash
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run "PostgresTenant(List|Get)Tenant"
go test ./services/tenant-service/internal/service/ -count=1 -run "ListTenants|GetTenantDetail"
```

## 后续 Issue 依赖

| Issue | 依赖本批次 |
|---|---|
| #007 Update | 详情字段/Plan 装配可复用；写路径幂等/超时惯例见 #005 |
| #008 状态机 | 列表 status 过滤已可用；lifecycle 写仍复用 Actor 头 |
| #009 Auth | 详情仅摘要；完整 GET/PUT SSO·MFA 在本批未实现 |
| BOSS UI | 列表/详情页可对接现有 `/svc/tenants` 字段；空 plan_code / auth 缺省需 UI 兜底 |
