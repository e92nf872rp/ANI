# PLATFORM-ADMIN-ISSUE-11：操作历史查询 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #11）
> **完成日期：** 2026-09-02（本地未提交；含 review-it 补操作者字段 + 测试补强）
> **Scope：** Services `ListPlatformAdminAuditLogs` RPC + Postgres `ListUserAuditLogs` + Services Gateway `GET .../audit-logs`；响应增加操作者 `user_id`；proto/OpenAPI 同步
> **Product line：** services（主）；直查 `audit_logs`，不调 Core
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-011-audit-logs-api.md`
> **PRD / UX / SPEC：** `prd-new-boss-platform-admin.md` / `ux-new-boss-platform-admin.md` §4.3 操作记录 Tab / `spec-new-boss-platform-admin.md` §5.2.7
> **依赖：** #2（审计 store 端口）、#5/#8/#9/#10（写操作审计落库）

## 交付内容

1. **Services：** `ListPlatformAdminAuditLogs` 替换 501——校验 `user_id` + `action`/`result` 过滤枚举 → `auditStore.ListUserAuditLogs` → 映射 `items[]` + `next_cursor`；**只读，不写审计、不调 Core**
2. **Postgres Store：** `ListUserAuditLogs`——`tenant_id IS NULL` + `resource='platform_user'` + `details->>'target_id'=$userId`；可选 `action`/`result`；`created_at DESC, id DESC` 游标分页（`types.EncodeCursor/DecodeCursor`）；`SELECT` 含 `user_id`（操作者）；去掉未暴露的 `COUNT(*)`
3. **契约：** OpenAPI `PlatformAdminAuditLog` 增 `user_id`（nullable uuid，操作者）；proto `PlatformAdminAuditLog.user_id` 为 `google.protobuf.StringValue`（对齐 TenantAdminAuditLog）
4. **Services Gateway：** `listPlatformAdminAuditLogs` 转发 `limit/cursor/action/result` → gRPC；`platformAdminAuditLogJSON` 输出 `user_id`（无操作者时为 JSON `null`）；`next_cursor` 空串 → `null`
5. **测试：** Service `ListAuditLogs_Success` / `FilterAndEmpty` / `OperatorNullable`；Gateway `TestHandler_AuditLogsFlow`（写操作后 list + action 过滤 + `user_id` 断言）；Postgres `ImplementsPort` 编译断言

### API 契约摘要

| 项 | 值 |
|---|---|
| HTTP | `GET /api/v1/svc/platform-admins/{userId}/audit-logs` |
| AuthZ | platform-admin / platform-ops / platform-readonly（网关层） |
| Query | `limit`（default 20, max 100）、`cursor`、`action`（可选）、`result`（`success` \| `failure`） |
| 响应 | `{ items: PlatformAdminAuditLog[], next_cursor: string \| null }` |
| 单项字段 | `id`, `action`, `resource`, `result`, `user_id`（操作者，nullable）, `details`, `created_at` |
| action 覆盖 | `platform_admin.create` / `change_role` / `reset_password` / `disable` / `enable` / `delete` |
| 错误 | 非法 `userId`/`result` → 400 `VALIDATION_FAILED`；非法 `cursor` → 400；无记录 → 200 空列表 |

### 涉及文件

| 层 | 文件 |
|---|---|
| 契约 | `api/openapi/services/v1.yaml`、`api/proto/platform_settings/v1/platform_admin_service.proto`、`pkg/generated/pb/.../platform_admin_service.pb.go` |
| Port | `platform-settings-service/internal/repo/ports/platform_admin_audit_store.go` |
| Store | `platform-settings-service/internal/repo/adapters/postgres/platform_admin_audit_store.go` |
| Service | `platform-settings-service/internal/service/platform_admin_service.go`、`validate.go` |
| Gateway | `ani-gateway/internal/router/platform_admin_resources.go` |
| 测试 | `platform_admin_service_test.go`、`platform_admin_resources_test.go`、`platform_admin_audit_store_test.go` |

## Design Decisions

1. **按 `details.target_id` 查目标账号，不按 `audit_logs.user_id`**
   - **Ambiguity：** Issue/SPEC §5.2.7 草稿写 `WHERE user_id=$1`；#004 ports 写 `target_id` 关联。
   - **Choice：** 列表 path `{userId}` 过滤 `details->>'target_id'`；`audit_logs.user_id` 列表示**操作者**并在响应 `user_id` 返回。
   - **Rationale：** 写入侧 `writeAudit*` 已将目标放进 `details.target_id`、操作者放进 `user_id`；与租户管理员审计模型一致。

2. **操作者字段对齐 TenantAdminAuditLog**
   - **Choice：** proto 用 `google.protobuf.StringValue user_id`；网关无操作者时 JSON `null`。
   - **Rationale：** 复用既有 nullable 模式；BOSS 操作记录 Tab 可展示「谁操作的」。

3. **只读不写审计**
   - **Choice：** `ListPlatformAdminAuditLogs` 无 `writeAudit*`、无 Core 调用。
   - **Rationale：** Issue AC / SPEC §5.2.7 明确查询本身不产生审计行。

4. **`result` 仅校验 `success`/`failure`；`action` 透传**
   - **Choice：** `validateListAuditLogFilters` 只约束 `result`；非法 action 返回空列表。
   - **Rationale：** 与 quota-policy audit 列表策略一致；便于新增 action 不改校验器。UX 筛选 `failed` 与 API `failure` 不一致——前端须映射。

5. **未知目标账号返回空列表，非 404**
   - **Choice：** 不先 `GetPlatformAdmin` 验存在性。
   - **Rationale：** SPEC §5.4「操作历史无记录 → items=[], next_cursor=""」；与「用户不存在」对外表现合并，减少 Core 往返。

6. **去掉列表 `COUNT(*)`**
   - **Choice：** OpenAPI 响应无 `total`（不同于 tenant-plan audit）；Store 不再每请求 COUNT。
   - **Rationale：** review-it 发现 COUNT 结果未暴露；游标分页足够支撑 UX Tab。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC：`WHERE user_id=$1` | `details->>'target_id' = $userId` + 响应 `user_id` 为操作者 | #004 ports 与写入模型为准；Issue 草稿 SQL 过时 |
| Issue AC 初版响应字段无 `user_id` | 增 `user_id`（操作者，nullable） | 产品/review 要求列表返回操作者 |
| Issue AC：`PlatformAdminStore.ListAuditLogs` | 端口名 `PlatformAdminAuditStore.ListUserAuditLogs` | #004 已确立审计 store 拆分命名 |
| SPEC query `result=failed` | OpenAPI/实现为 `failure` | 与 tenant-admin audit、DB 写入 `result` 列一致 |
| OpenAPI 404 `PLATFORM_USER_NOT_FOUND` | 实际仅 400（非法 uuid/result）；无记录 200 空列表 | 与 SPEC 边界表一致 |
| SPEC §9.3 集成测「真实 DB 写后读」 | Gateway fake gRPC 内存审计 + disable/enable 后 list | 与 #5–#10 合成流同模式 |
| Issue AC：`make test` 全绿 | 本会话跑聚焦 `AuditLog` 包测试 | 合入前仍需全量门禁 |
| UX Table 列未强制含 `user_id` | API 已返回；前端列待 BOSS 批次接入 | 后端先行补齐契约 |

## Tradeoffs

- **直查 DB vs 经 Core：** 选 Services 直查 `audit_logs`——读路径短、与 Issue 范围一致；代价是审计 schema 与 Services postgres adapter 耦合。
- **不返回 `total`：** 减 DB 负载、契约更简单；代价是前端无法显示「共 N 条」总数（UX 当前用游标翻页即可）。
- **action 不白名单校验：** 扩展性好；代价是 typo 得空列表而非 400。
- **操作者仅 UUID：** 不 join 用户表解析显示名——响应轻、实现小；BOSS 需另查账号列表或后续 enrichment API。
- **Postgres 集成测缺失：** `listingAuditStore` + Gateway 合成流覆盖主路径；真实 PG 游标/JSONB 索引留给后续 live gate。

## Verification

```bash
# 本会话已执行（全部通过）：
go test ./services/platform-settings-service/internal/service/... -run "AuditLog" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/postgres/... -count=1
go test ./services/ani-gateway/internal/router/... -run "AuditLog" -count=1
python scripts/validate_services_route_contract.py   # 0 errors
cd api/proto && buf generate --template buf.gen.yaml .

# 待合入前：
cd repo
make test
make validate-services
make validate-architecture
git diff --check
```

## 边界声明

- **本批为平台运营账号功能流读路径收尾**（#001–#011 后端 API 批次）；不含 BOSS 前端 Tab。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
