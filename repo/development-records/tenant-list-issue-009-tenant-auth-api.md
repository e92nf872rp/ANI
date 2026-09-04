# TENANT-LIST-ISSUE-009：租户列表管理 — SSO/MFA 认证配置读写

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #9）
> **完成日期：** 2026-09-04
> **Scope：** US-008/US-009 写部分/US-010：`GetTenantAuth`、`UpdateTenantSso`、`UpdateTenantMfa`（svc 编排 + Core `tenant_auth` 读写）；SSO 测试连接不在本批（Issue-10）
> **依赖：** Issue-003（tenant_auth 表）、Issue-004（网关路由骨架）
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-009-tenant-auth-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入改动（含 review-it 后 disabled→409、provider null/`""` 语义收口）

## 交付内容

1. **GET Auth：** Core `GetTenantAuth` — 租户不存在 → 404；缺 `tenant_auth` 行 → 默认 `{sso_enabled=false, mfa_required=false, provider=null, updated_at=now}`；响应含 SSO + MFA + `updated_at`
2. **PUT SSO：** svc 至少一字段 → 读当前配置 → 有效 `sso_enabled=true` 且有效 provider 空 → **422 `TENANT_SSO_CONFIG_INVALID`** → Core 部分更新 → 审计 `tenant.sso.update`
3. **PUT MFA：** 网关强制 `mfa_required` 布尔必填 → Core 更新 → 审计 `tenant.mfa.update`
4. **disabled 写拒绝（Core）：** `UpdateTenantAuth` 对 disabled → **`ErrTenantStateInvalid` → 409**；不存在仍 404；svc 不预检，透传 Core
5. **provider 部分更新语义（用户确认）：** 未传 / `null` = 不更新；`""`（trim 后空）= 清空；网关（svc + Core admin）与 SDK 一致
6. **Gateway：** MFA omit/`null` 拒绝；SSO 字段类型错误 → 400；`TestTenantSso` 仍 stub（Issue-10）；幂等键网关处理

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `pkg/adapters/runtime/postgres_tenant.go` | `GetTenantAuth` / `UpdateTenantAuth`（ensure 行 + 动态 SET；disabled→STATE_INVALID） |
| `pkg/adapters/runtime/postgres_tenant_test.go` | GET 成功/缺行默认/NotFound；Update 部分更新/disabled 409/空 patch |
| `services/tenant-service/internal/service/tenant_service.go` | Get/UpdateSso/UpdateMfa + SSO 联动校验 + 审计 |
| `services/tenant-service/internal/service/tenant_test.go` | 默认值、RequiresProvider、disabled 409、清空/关 SSO、round-trip |
| `services/tenant-service/internal/service/tenant_plan_test.go` | fake `Get/UpdateTenantAuth`（模拟 Core disabled） |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | GET/PUT `/admin/tenants/{id}/auth`；清空发 `""` |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | auth GET/SSO/MFA handler；provider null≠清空 |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | Core admin auth provider 语义对齐 |
| `api/openapi/services/v1.yaml` | provider 说明；SSO/MFA PUT 409 disabled |
| `api/openapi/v1.yaml` | Core updateTenantAuth：disabled 409；provider null/`""` |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| GET：租户不存在 → 404 | Core `GetTenantAuth_NotFound` | ✅ |
| GET：缺行默认双 false | Core MissingRowDefaults + svc Defaults | ✅ |
| GET：一次返回 sso/provider/mfa/updated_at | `TenantAuthConfig` + gateway JSON | ✅ |
| SSO：enabled=true 无 provider → 422 | `UpdateTenantSso_RequiresProvider` | ✅ |
| SSO：Core 部分更新 + updated_at | `UpdateTenantAuth_Partial` | ✅ |
| SSO：审计 `tenant.sso.update` | round-trip / 成功路径 | ✅ |
| MFA：必填布尔；更新 mfa_required | 网关校验 + Core patch | ✅ |
| MFA：审计 `tenant.mfa.update` | round-trip | ✅ |
| 200 `{ id, message }`；幂等网关 | IdempotentResult | ✅ |
| 读改读闭环 + updated_at 前进 | `Auth_ReadWriteRoundTrip` | ✅ |
| SSO 测试连接 | Issue-10 | ⏭ 不在本批 |
| 真库集成 | 本批以单测 + fake 为主 | ⚠️ 未跑 live |

## Design Decisions

### D1：disabled 改 Auth 由 Core 判断，返回 409（用户确认）

- **Ambiguity：** 曾讨论 svc 预检 409 vs Core NotFound（对齐 UpdateTenant 基本信息）。
- **Choice：** **仅 Core** `UpdateTenantAuth` 查 status；disabled → **`ErrTenantStateInvalid`（409）**；不存在 → NotFound；svc 去掉 `rejectDisabledTenant`，直接透传。
- **Rationale：** Auth 写守卫归属数据面；与「基本信息」分层（svc 409 + Core 404）不同——Auth 写路径以 Core 为唯一状态门；用户明确要求 409 而非 404。

### D2：provider 部分更新 — null 不更新，空串清空（用户确认）

- **Ambiguity：** 初版网关把 `null` 当清空（与套餐 description 的「null/未传=不更新、空串=清空」不一致）。
- **Choice：** 未传 / `null` = 不更新；`""` = 清空（入库 NULL）；SDK 清空发 JSON `""`（不再发 `null`）。
- **Rationale：** 与租户套餐等部分更新惯例一致；避免客户端 JSON null 误清 provider。

### D3：SSO 联动校验在 svc，不在 Core

- **Choice：** 有效 `sso_enabled=true` 且有效 provider 空 → 422；关 SSO 可不传 provider；可在 SSO off 时单独设 provider；开启时可沿用原 provider。
- **Rationale：** Issue/SPEC 将联动校验放在 Services；Core 保持薄存储部分更新。直连 Core admin 可写出脏数据——生产应走 `/svc/.../auth/sso`。

### D4：MFA 网关强制必填，proto bool 无 omit 语义

- **Choice：** Gateway 要求 body 含布尔 `mfa_required`（omit/`null` → 400）；不静默当 `false`。
- **Rationale：** review-it 修复：proto 普通 bool 无法区分「未传」与 false，必须在 HTTP 层显式校验。

### D5：缺 auth 行 GET 返回 `updated_at=Now()`

- **Choice：** SPEC 缺行默认值路径用当前时间，不造假历史时间戳。
- **Rationale：** 无行可回读；UI 勿仅靠该字段判断「运营刚改过」。

### D6：frozen 允许改 Auth；disabled 不可

- **Choice：** Core 只拦 disabled；frozen 可改 SSO/MFA（单测 `ProviderWhileSSOOff` 用 frozen）。
- **Rationale：** 与 UpdateTenant「frozen 可改信息」一致；冻结挡登录，不挡运营改认证配置。

## Deviations

### Dev-1：实现落在 `TenantService` / `postgres_tenant.go`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`、`postgres_tenant_store.go`。
- **实现：** 延续 004–008 合并边界。
- **原因：** proto / 注入已合并。

### Dev-2：Core Update 用动态 SET，非字面 COALESCE

- **Issue：** `UPDATE … COALESCE`。
- **实现：** 仅提供字段进入 SET（与 UpdateTenant 一致）。
- **原因：** 部分更新语义更清晰；空串清空显式写 NULL。

### Dev-3：disabled 写拒绝在 Core，且为 409 而非「与不存在同 404」

- **对比 #007 UpdateTenant：** Core disabled≡NotFound；svc 另返回 409。
- **本批 Auth：** Core 直接 STATE_INVALID；svc 不预检。
- **原因：** 用户对本批明确要求「Core 层判断」+「disabled→409」。

### Dev-4：真库集成 / SSO 测试未做

- **Issue：** 集成闭环；SSO test 属 Issue-10。
- **实现：** 单测 round-trip + Core fake；`TestTenantSso` stub。
- **原因：** 与 005–008 同策略；test 连接独立交付。

### Dev-5：MFA 开关不拦截登录

- **PRD：** MFA 强制语义。
- **实现：** 只落 `mfa_required` 标志；登录 enforcement / frozen·disabled 403 属 SPEC Q2 与后续。
- **原因：** Issue-009 范围是配置读写。

## Tradeoffs

### T1：disabled Auth — svc 预检 409 vs Core 判定

| 方案 | 结果 |
|---|---|
| svc GetTenant 预检 | 多一次读；可在 SSO 校验前统一 409 |
| **Core UpdateTenantAuth（选用）** | 守卫在数据面；SSO 非法配置可能先 422 再碰不到 409 |

### T2：provider null — 清空 vs 不更新

| 方案 | 结果 |
|---|---|
| null=清空（初版） | 易与「未传」客户端行为混淆 |
| **null=不更新、""=清空（选用）** | 与套餐 description 等惯例一致 |

### T3：SSO 校验层 — Core vs Services

| 方案 | 结果 |
|---|---|
| Core 校验 | 所有入口一致，Core 变厚 |
| **Services 校验（选用）** | 符合 Issue；Core admin 旁路可脏写 |

## Review-it 修复记录（2026-09-04）

- Gateway MFA：omit/`null` → 400（不再静默 false）。
- Gateway SSO：字段类型错误 → 400；provider `null`/`""` 语义按用户确认收口。
- disabled Auth：先 svc 预检 → 改为 Core 判定；再由 NotFound 改为 **409 STATE_INVALID**。
- SDK：清空 provider 发 `""`，与网关「null≠清空」对齐。
- OpenAPI（services + Core）：409 / provider 说明与实现同步。

## Verification Commands

```bash
cd repo
# 如 C: 盘满：GOCACHE/GOTMPDIR 指到 repo 下目录
go test ./pkg/adapters/runtime/ -count=1 -run "PostgresTenant(GetTenantAuth|UpdateTenantAuth)"
go test ./services/tenant-service/internal/service/ -count=1 -run "GetTenantAuth|UpdateTenantSso|UpdateTenantMfa|Auth_"
```

## 后续 Issue 依赖

| Issue / 项 | 依赖本批次 |
|---|---|
| #010 SSO 测试连接 | GET/PUT auth 已就绪；本批 stub |
| Gateway auth Q2 | 登录 MFA 强制 / TENANT_FROZEN·DISABLED 403 |
| BOSS UI 认证 Tab | provider null≠清空；disabled 表单禁用；409/422 文案 |
| Feature-batch 四文件 | README / CURRENT-SPRINT / ANI-06 系列收口时同步 |
| #007 对比备忘 | 基本信息 disabled：svc 409 + Core 404；Auth：仅 Core 409 |
