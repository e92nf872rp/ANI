# TENANT-LIST-ISSUE-013：租户列表管理 — 生命周期查询 + 操作历史查询

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #13）
> **完成日期：** 2026-09-04
> **Scope：** US-015/016 — `ListTenantLifecycle`（Core `tenant_lifecycle`）+ `ListTenantAuditLogs`（Services `audit_logs`）；只读、无幂等键、不写审计
> **依赖：** Issue-004（网关路由/RBAC）；Issue-003（`tenant_lifecycle` 表）；状态转换写入 lifecycle（Issue-5/8）；既有 `AuditStore` / `audit_logs` 分区表
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-013-lifecycle-audit-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入（含 review-it：枚举 Parse*、audit 404、OpenAPI 400、YAML Tab 修复、result 校验先于 GetTenant）

## 交付内容

1. **ListTenantLifecycle：** tenant-service 校验 `action` 枚举 → Core SDK `GET /admin/tenants/{id}/lifecycle`；Core adapter 校验租户 EXISTS + action 枚举 + keyset `(created_at, id) DESC`；缺租户 → `TENANT_NOT_FOUND`
2. **ListTenantAuditLogs：** 校验 `result` 枚举 → Core `GetTenant` 做 404 → 本地 `AuditStore.ListTenantAuditLogs`（`tenant_id` + 可选 action/result + keyset）；非法 cursor/result → `VALIDATION_FAILED`
3. **枚举：** Services 侧 `TenantLifecycleAction` / `AuditResult` + `Parse*Filter`（对齐 `ParseTenantStatusFilter`）；Core 侧沿用 `pkg/ports.ParseTenantLifecycleActionFilter`
4. **OpenAPI：** lifecycle/audit-logs 均含 400/404；audit-logs 路径键 Tab 缩进已修为合法 YAML
5. **网关：** Issue-4 已接线；本批仅依赖既有 `listTenantLifecycle` / `listTenantAuditLogs` 透传与错误映射

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `pkg/ports/tenant.go` | `TenantLifecycleAction` + `ParseTenantLifecycleActionFilter`；`ListTenantLifecycle` port |
| `pkg/adapters/runtime/postgres_tenant.go` | Core `ListTenantLifecycle` SQL（EXISTS + keyset + action 过滤） |
| `pkg/adapters/runtime/postgres_tenant_test.go` | Core lifecycle 单测 |
| `services/ani-gateway/internal/router/admin_tenant_resources.go` | Core admin lifecycle 路由/错误映射 |
| `services/tenant-service/internal/repo/ports/core_tenant.go` | Services 侧 lifecycle action 枚举 + filter 类型 |
| `services/tenant-service/internal/repo/ports/audit_store.go` | `AuditResult` + `ParseAuditResultFilter`；`ListTenantAuditLogs` |
| `services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | SDK `ListTenantLifecycle` |
| `services/tenant-service/internal/repo/adapters/postgres/audit_store.go` | 按 `tenant_id` keyset 列表 |
| `services/tenant-service/internal/service/tenant_service.go` | 两 RPC；audit 先校 result 再 GetTenant |
| `services/tenant-service/internal/service/audit.go` | 写入改用 `AuditResultSuccess/Failure` 常量 |
| `services/tenant-service/internal/service/tenant_test.go` | Filter/Page、InvalidAction/Result、NotFound、NoWriteAudit |
| `api/openapi/services/v1.yaml` | audit-logs 补 400；路径缩进修复 |
| `issue-013-lifecycle-audit-api.md` | AC 补充 audit 404/400 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| Core lifecycle SELECT + action 过滤 + keyset | `postgres_tenant.go` ListTenantLifecycle；runtime 单测 | ✅ |
| lifecycle 游标 limit 20/100 | service + Core 双侧 clamp | ✅ |
| lifecycle 字段 id/action/reason/user_id/request_id/created_at | proto 映射 + gateway JSON | ✅ |
| lifecycle 404 TENANT_NOT_FOUND | Core EXISTS → SDK → mapStoreError；NotFound 单测 | ✅ |
| audit WHERE tenant_id + action/result + keyset | `audit_store.go` ListTenantAuditLogs | ✅ |
| audit 字段含 resource 透传、无 resource 过滤 | OpenAPI/UX/SPEC Q4；gateway 返回 resource | ✅ |
| audit 404 GetTenant | service 步骤 3；NotFound 单测 | ✅ |
| 非法 result/cursor → 400 | ParseAuditResultFilter + DecodeCursor；InvalidResult 单测 | ✅ |
| 只读不写审计 | NoWriteAudit 单测 | ✅ |
| OpenAPI 400/404 | services/v1.yaml lifecycle + audit-logs | ✅ |

## Design Decisions

### D1：审计 404 用 Core `GetTenant`，不在 audit_logs 上 EXISTS

- **Ambiguity：** 审计表在 Services；租户权威在 Core。空列表 vs 404 早期未写死，OpenAPI 已声明 404。
- **Choice：** `s.tenants.GetTenant` 失败 → `TENANT_NOT_FOUND`；再查本地 audit。
- **Rationale：** 与详情/其它租户子资源一致；避免「租户已删但仍有历史行」时误报 200 空表与 lifecycle Tab 不一致。

### D2：过滤枚举走 `Parse*Filter`，不硬编码 case

- **Ambiguity：** service 层曾手写 switch 字面量。
- **Choice：** 与 `ParseTenantStatusFilter` 同模式；Services `AuditResult` / `TenantLifecycleAction`；Core 保留自有 Parse。
- **Rationale：** 单一枚举真源、错误文案与 store 校验一致。

### D3：非法 `result` 先于 `GetTenant` 校验

- **Ambiguity：** 先 404 还是先 400。
- **Choice：** 先 `ParseAuditResultFilter`，再 GetTenant（与 lifecycle 先校 action 再打 Core 对齐）。
- **Rationale：** 非法入参不消耗 Core；坏 tenantId+坏 result 时返回 VALIDATION_FAILED 而非 NOT_FOUND。

### D4：audit `action` 保持自由字符串；`result` 枚举

- **Choice：** OpenAPI action 无 enum；result 仅 `success|failure`。
- **Rationale：** 审计 action 命名随业务增长（`tenant.freeze` 等）；result 域固定。

### D5：keyset 用 `(created_at, id)`，与 plan audit 模式一致

- **SPEC：** `(tenant_id, created_at)` 索引路径；实现补 `id` 打破并列时间戳。
- **Choice：** `ORDER BY created_at DESC, id DESC` + EncodeCursor；与既有 ListPlanAuditLogs 一致。
- **Rationale：** 稳定翻页；Issue「对齐 TenantPlanAuditStore.List」。

## Deviations

### Dev-1：RPC 落在 `TenantService`，非 Issue 文件名 `tenant_list_service.go`

- **Issue Scope：** 写 `tenant_list_service.go`。
- **实现：** `tenant_service.go`（与 004 起合并一致）。
- **原因：** 无独立 list gRPC service；网关已绑 TenantServiceClient。

### Dev-2：Core 实现文件为 `postgres_tenant.go`，非 Issue 写的 `postgres_tenant_store.go`

- **Issue Scope：** 文件名略旧。
- **实现：** 现网 `pkg/adapters/runtime/postgres_tenant.go`。
- **原因：** 仓库既有命名。

### Dev-3：Issue 初稿 audit AC 未强制 404；实现与后续 AC/OpenAPI 强制 404

- **早期 review：** 曾允许空列表。
- **实现：** GetTenant + Issue AC / OpenAPI 已更新。
- **原因：** 产品要求两 Tab 404 语义一致。

### Dev-4：未做真实 PG 集成造 freeze 后列表（Issue「集成」）

- **Issue 测试：** 集成造转换后有序列表。
- **实现：** Core/service 单测 + fake store；无 live PG gate 本批。
- **原因：** 聚焦单元边界；live 留 Follow-ups。

## Tradeoffs

### T1：审计 404 — GetTenant 全量 vs 轻量 EXISTS

| 方案 | 利 | 弊 |
|---|---|---|
| A GetTenant（选用） | 复用客户端与错误映射；零新 Core API | 多一跳、载荷大于 EXISTS |
| B Core 专用 EXISTS/HEAD | 更轻 | 新端点/契约成本 |
| C 仅查 audit 空列表 | 零 Core | 与 lifecycle/OpenAPI 404 不一致 |

选用 A：一致性优先；QPS 低（BOSS ≤10³ 租户）。

### T2：Core / Services 双份 lifecycle action 枚举

| 方案 | 利 | 弊 |
|---|---|---|
| A 双份对齐（选用） | 不跨层 import Core ports 类型到 Services 校验文案 | 值漂移风险 |
| B Services 只透传、仅 Core 校验 | 少代码 | 非法 action 必打 Core |

选用 A：service 早失败；值与 OpenAPI 手工对齐。

### T3：空 details → `{}` vs JSON null

| 方案 | 利 | 弊 |
|---|---|---|
| A 解码后始终 map（选用） | structpb 稳定 | 前端看到 `{}` 而非 null |
| B 缺省 null | 与「无扩展」语义更贴 | 映射分支更多 |

选用 A：与既有 plan audit 编码路径一致。

## Open Questions

- [ ] 审计 `GetTenant` 是否值得后续换成轻量 EXISTS（仅当延迟成为问题）？
- [ ] 分区表是否需要时间窗参数，避免跨分区全扫？
- [ ] Feature batch 四文件（README / CURRENT-SPRINT / ANI-06）是否与 Issue-012 同 PR 一并收口？

## Verification commands run

```text
cd repo/services/tenant-service && go test ./internal/service/ -count=1 -run "ListTenantLifecycle|ListTenantAuditLogs"
cd repo && go test ./pkg/adapters/runtime/ -count=1 -run ListTenantLifecycle
python scripts/validate_component_imports.py --root .
python -c "import yaml; yaml.safe_load(open('api/openapi/services/v1.yaml',encoding='utf-8'))"
# review-it（二次）：clean — Tab 与 result 校验顺序已修；无剩余 accepted findings
```

## Follow-ups

- [ ] Feature batch 四文件：README / CURRENT-SPRINT / ANI-06（合入前）
- [ ] 可选：live PG 集成（freeze/disable 后 lifecycle 有序 + audit 过滤翻页）
- [ ] 可选：审计存在性检查轻量化（见 Open Questions）
