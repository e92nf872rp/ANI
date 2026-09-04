# TENANT-LIST-ISSUE-011：租户列表管理 — 租户配额代理查询

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #11）
> **完成日期：** 2026-09-04
> **Scope：** US-011 `GetTenantQuota` — 代理 Core `getTenantQuota`，封装 BOSS 配额展示视图；不新增 Core 端点；配额变更申请属 Issue-12
> **依赖：** Issue-004（网关路由）；既有 `QuotaSvcClient.GetQuota`
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-011-tenant-quota-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入改动（含 review-it 后去掉二次 `ListQuotaMeta`，单次 GetQuota 封装）

## 交付内容

1. **svc `GetTenantQuota`：** 调 `QuotaSvcClient.GetQuota` → `assembleTenantQuotaViews` → `{ items: [{ resource_type, display_name, used, total, unit }] }`
2. **展示字段来源：** Core GET 已 JOIN `resource_quota_meta` 返回 `display_name`/`unit`；SDK `decodeQuotaItems` 解析进 `CoreQuotaResult`；**不再**二次 `ListQuotaMeta`
3. **兜底：** `display_name` 空 → 用 `resource_type`；`unit` 可空；空 `resource_type` 行跳过；租户存在无配额行 → `items: []`
4. **错误：** 租户不存在 → 404 `TENANT_NOT_FOUND`；Core 不可达 → **502** `GRPC_CLIENT_UNAVAILABLE`（与网关错误码表一致；Issue 字面 503 未采用）
5. **只读：** 不写审计
6. **Gateway：** `GET /svc/tenants/:tenantId/quota` 映射 JSON `items`

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `services/tenant-service/internal/service/tenant_service.go` | `GetTenantQuota` + `assembleTenantQuotaViews`（单次 GetQuota） |
| `services/tenant-service/internal/service/tenant_test.go` | AssemblesFromGetQuota / NotFound / CoreUnavailable / NoAudit |
| `services/tenant-service/internal/repo/ports/core_quota.go` | `CoreQuotaResult` 增加 DisplayName/Unit；GetQuota 注释 |
| `services/tenant-service/internal/repo/adapters/core/quota_svc_client.go` | `decodeQuotaItems` 解析 display_name/unit |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | `getTenantQuota` handler（Issue-4 已接线，本批填通） |
| `api/openapi/services/v1.yaml` | getTenantQuotaView 说明：响应已含 JOIN meta，不再 ListQuotaMeta |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| GetQuota 取 used/total | `GetTenantQuota` → `quota.GetQuota` | ✅ |
| display_name/unit 组装；缺失兜底 | Core JOIN + 空 display_name→resource_type；单测 mystery_dim | ✅（来源改为 Core GET，非自有 meta 表） |
| 响应 schema 五字段、不分页 | OpenAPI `TenantQuotaViewItem` + gateway | ✅ |
| 404 TENANT_NOT_FOUND | `GetTenantQuota_TenantNotFound` | ✅ |
| Core 不可达错误映射 | `GetTenantQuota_CoreUnavailable` → 502 码前缀 | ✅（HTTP 502 非 Issue 503） |
| 只读不写审计 | `GetTenantQuota_NoAudit` | ✅ |
| 不二次 ListQuotaMeta | 单测 `quota.calls == 0` | ✅ |
| 真库集成 | 本批以 mock 单测为主 | ⚠️ 未跑 live |

## Design Decisions

### D1：只用一次 Core GetQuota，不二次 ListQuotaMeta（用户确认）

- **Ambiguity：** Issue 写「GetQuota 取 used/total，再 JOIN 自有 quota-meta」；SPEC 写 display_name/unit 来自 Core quota-meta JOIN；Core `getTenantQuota` 实现已 INNER JOIN `resource_quota_meta` 返回两字段。
- **Choice：** SDK 解码 `display_name`/`unit`；svc 仅封装视图；去掉 `ListQuotaMeta`。
- **Rationale：** 少一次往返；消除「配额已取到、meta 失败整页 502」；与 Core 契约一致。

### D2：BOSS 视图不暴露 reserved / is_discrete

- **Choice：** 契约仅 `used`/`total`（及展示字段）；`CoreQuotaResult.Reserved` 仍解码供他用（如 #008 禁用守卫），但不进本响应。
- **Rationale：** SPEC §4.3 / OpenAPI `TenantQuotaViewItem` 未列 reserved；扩字段需另开契约变更。

### D3：空 display_name 兜底，不丢弃行

- **Choice：** 有 used/total 的行一律返回；缺名用 `resource_type`。
- **Rationale：** Issue 允许「跳过或兜底」；兜底更利于运营看见占用数字。Core INNER JOIN 下无 meta 的行本就不会出现。

### D4：Core 不可达用既有 `GRPC_CLIENT_UNAVAILABLE` → 502

- **Choice：** `mapStoreError(ErrCoreUnavailable)`；网关映射 502。
- **Rationale：** 与 tenant-service / 网关错误码表一致；Issue「503」为过时表述。

## Deviations

### Dev-1：实现落在 `TenantService`，非 Issue 文件名

- **Issue 说：** `tenant_list_service.go`。
- **实现：** 延续 004–010 合并进 `TenantService`。
- **原因：** 边界已合并。

### Dev-2：非「自有 quota-meta 表」二次 JOIN

- **Issue 说：** 自有 meta store / 与 `buildQuotaLimitViews` 同源再组装。
- **实现：** 消费 Core GET 内嵌 meta 字段。
- **原因：** Core 已 JOIN；用户确认单次调用；套餐域的 ListQuotaMeta 仍供 plan 限额视图使用，与本查询解耦。

### Dev-3：HTTP 502 而非 Issue 字面 503

- **Issue：** Core 不可达 → 503。
- **实现：** 502 `GRPC_CLIENT_UNAVAILABLE`。
- **原因：** 与既有 taxonomy / OpenAPI 一致。

### Dev-4：真库集成未作为强制门禁

- **Issue：** 造配额后断言与 Core 一致。
- **实现：** fake + 单测。
- **原因：** 与 005–010 同策略。

## Tradeoffs

### T1：展示字段 — 二次 ListQuotaMeta vs 单次 GetQuota

| 方案 | 结果 |
|---|---|
| GetQuota + ListQuotaMeta（初版） | 与 Issue 字面接近；双 RTT；meta 失败拖垮整请求 |
| **仅 GetQuota（选用，用户确认）** | 对齐 Core JOIN；更简、更稳 |

### T2：响应是否含 reserved

| 方案 | 结果 |
|---|---|
| 暴露 reserved | 利于解释禁用 409；需改 OpenAPI/proto |
| **不暴露（选用）** | 严格跟 SPEC；禁用语义靠 #008 + UI 文案 |

## Review-it 修复记录（2026-09-04）

- 初版 svc 曾二次 `ListQuotaMeta` 组装；审查指出 Core GET 已带 display_name/unit。
- **用户确认：** 只调 GetQuota 后封装。
- SDK `decodeQuotaItems` 补解析；单测断言不调 ListQuotaMeta；OpenAPI description 同步。

## Verification Commands

```bash
cd repo
# 如 C: 盘满：GOCACHE/GOTMPDIR 指到 repo 下目录
go test ./services/tenant-service/internal/service/ -count=1 -run "GetTenantQuota"
go test ./services/tenant-service/internal/repo/adapters/core/ -count=1 -run "QuotaSvcClient"
```

## 后续 Issue 依赖

| Issue / 项 | 依赖本批次 |
|---|---|
| #008 Disable | 共用 GetQuota 语义；本视图无 reserved，禁用文案勿仅依赖本 API |
| #012 配额变更申请 | 本批只读查询；写申请/审批另开 |
| BOSS UI 配额 Tab | 空 items=未初始化；勿把空列表当 404 |
| Feature-batch 四文件 | README / CURRENT-SPRINT / ANI-06 系列收口时同步 |
