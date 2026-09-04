# TENANT-LIST-ISSUE-012：租户列表管理 — 配额变更申请三件套（提交/列表/审批）

> **批次类型：** Feature batch（BOSS 租户列表管理 Issue #12）
> **完成日期：** 2026-09-04
> **Scope：** US-012/013/014 — `SubmitQuotaChangeRequest` / `ListQuotaChangeRequests` / `ReviewQuotaChangeRequest`；落 service 表 `tenant_quota_change`；审批生效走既有 Core `UpsertQuota`；不新增 Core 端点
> **依赖：** Issue-004（网关 `x-request-id` / `x-user-id` 透传、路由）；Issue-011（GetQuota 语义）；迁移 `20260811000300`；`QuotaSvcClient.ListQuotaMeta` / `GetQuota` / `UpsertQuota`
> **Product line：** boss
> **Issue 来源：** `repo/services/tasks/modules/issue/boss/tenant/tenant-list/issue-012-quota-change-request-api.md`
> **分支：** `tenant-list`
> **相关提交：** 本地未合入（含 review-it 四项修复：`req_` 前缀、approve 前校验 quota、`approved` 必填、FK→VALIDATION_FAILED）

## 交付内容

1. **Submit：** 网关 `x-request-id`（兼容 `req_<uuid>`）→ 校验 items → `ListQuotaMeta` 启用校验 → `GetQuota` 冻 `old_value` → 单事务 `INSERT` pending（同批共享 `request_id`）→ 审计 submit
2. **List：** 可选 status 过滤；不分页；`created_at DESC`；每行一维度
3. **Review：** `reqId=request_id` 整批；`approved` 必填（BoolValue / 网关 `*bool`）；先 `SetStatus` 再（通过时）`UpsertQuota`；Core 失败不回滚 + `apply_failed` 审计 + 异步重试
4. **冲突边界：** 跨请求同维 pending **允许**；同批重复维 → 422；同 `request_id`+同维 PK → 409 `QUOTA_CHANGE_REQUEST_CONFLICT`
5. **Store：** `InsertPendingQuotaChanges` / `ListQuotaChangesByTenant` / `ListQuotaChangesByRequestID` / `SetQuotaChangeStatusByRequestID`；FK → `VALIDATION_FAILED`

### 修改/新增文件（要点）

| 文件 | 变更摘要 |
|---|---|
| `services/tenant-service/internal/service/tenant_service.go` | 三 RPC + 校验/request_id/补偿 helpers |
| `services/tenant-service/internal/service/tenant_test.go` | Submit/List/Review 单测（含 `req_`、approved 缺省、quota nil 不 SetStatus） |
| `services/tenant-service/internal/service/errors.go` | 映射 `QUOTA_CHANGE_REQUEST_CONFLICT` |
| `services/tenant-service/internal/repo/ports/errors.go` | `ErrQuotaChangeRequestConflict` |
| `services/tenant-service/internal/repo/ports/tenant_store.go` | InsertPending 语义（无 HasPending / 无覆盖） |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_store.go` | 完整 SQL；PK/FK 映射 |
| `services/ani-gateway/internal/router/tenant_list_resources.go` | 错误码表 + review `*bool approved` |
| `api/proto/tenant/v1/tenant_list_service.proto` | `approved` → `BoolValue`；生成物已更新 |
| `api/openapi/services/v1.yaml` | submit/review 描述与 409/422 文案 |
| `deploy/migrations/20260811000300_tenant_quota_change.sql` | 复合主键 `(tenant_id, request_id, resource_type)`；无 pending 全局唯一索引 |
| `architecture/component-import-allowlist.yaml` | `tenant_store.go` 允许 `pgx/v5` |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| items 校验 / 批内不重复 | `validateQuotaChangeItems` + DuplicateDim 单测 | ✅ |
| request_id 来自网关，不 uuid.New | `parseGatewayRequestID`；`req_` TrimPrefix 单测 | ✅ |
| meta → GetQuota 顺序 | Submit 步骤 3→4 | ✅ |
| 跨请求同维允许 | CrossRequestSameDimAllowed 单测 | ✅ |
| 同请求同维 PK → 409 | CONFLICT sentinel + 单测 | ✅ |
| List status / 不分页 | ListQuotaChangesByTenant | ✅ |
| Review 整批 SetStatus + Upsert | ListAndReview 单测 | ✅ |
| Upsert 失败不回滚 | SPEC 路径 + apply_failed/retry | ✅（单测未强制模拟失败路径） |
| approved 必填 | 网关 + service；ApprovedRequired 单测 | ✅ |
| approve 前 quota nil 不改状态 | QuotaNilBeforeSetStatus 单测 | ✅ |

## Design Decisions

### D1：`request_id` = 网关链路 ID，兼容 `req_` 前缀

- **Ambiguity：** 中间件默认生成 `req_` + UUID；若客户端自带裸 UUID 也可。
- **Choice：** `TrimPrefix("req_", …)` 后再 `uuid.Parse`；返回/落库均为裸 UUID。
- **Rationale：** 与现网日志前缀一致；审批 path 用提交响应的 `id`（裸 UUID）。

### D2：跨请求同维 pending 允许；同请求同维禁止

- **Ambiguity：** 早期草稿曾「同维至多一条 pending / 覆盖」。
- **Choice：** 无 `UNIQUE (tenant_id, resource_type) WHERE pending`；无 `HasPending`；仅批内去重 + PK。
- **Rationale：** 产品确认：不同请求可并存；同一请求内同维不允许。

### D3：Submit 强制 ListQuotaMeta → GetQuota

- **Choice：** 先 `validateEnabledQuotaResourceTypes`，再 GetQuota 冻 `old_value`（无行 → NULL）。
- **Rationale：** Issue/SPEC 顺序强制；未启用维度早失败，避免脏 pending。

### D4：Review 先 SetStatus，再 Upsert；失败仍返回成功

- **Choice：** 状态落库后 Core 失败 → `apply_failed` 审计 + `scheduleQuotaChangeApplyRetry`（1s/2s/4s）；HTTP/gRPC 仍成功。
- **Rationale：** SPEC §5.4-3；与创建租户配额补偿模式一致。

### D5：`approved` 用 BoolValue / 网关 `*bool`

- **Ambiguity：** proto3 `bool` 无法区分「未传」与 `false`。
- **Choice：** proto `google.protobuf.BoolValue`；网关 JSON `*bool`，nil → 400。
- **Rationale：** 缺省误当驳回会造成误操作。

### D6：approve 前校验 quota 客户端

- **Choice：** `approved==true` 且 `s.quota==nil` → 在 SetStatus **之前**失败。
- **Rationale：** 避免「已 approved 但无法 Upsert 且对外报错」的不一致。

## Deviations

### Dev-1：实现落在 `TenantService`，非独立 list service 文件

- **Issue Scope 写：** `tenant_service.go`（已对齐）/ store 路径。
- **相对更早 SPEC Issue 映射：** 曾有独立命名习惯。
- **原因：** 004 起合并进 `TenantService`。

### Dev-2：meta 未启用错误码用 `QUOTA_RESOURCE_NOT_REGISTERED`（422）

- **Issue 说：** 可用 `QUOTA_CHANGE_REQUEST_INVALID` 或既有 `QUOTA_RESOURCE_NOT_REGISTERED`。
- **实现：** 复用 `validateEnabledQuotaResourceTypes` → 后者；网关表已补该码。
- **原因：** 与套餐限额域一致，少造平行码。

### Dev-3：同请求同维冲突码 `QUOTA_CHANGE_REQUEST_CONFLICT`（409）

- **Issue 说：** 409；未强制码名。
- **实现：** 独立 sentinel（非 INVALID），避免与 422 混淆。
- **原因：** 网关按前缀映射 HTTP。

### Dev-4：`old_value` NULL 在 API 表现为 `0`

- **SPEC/DB：** NULL=首次设置。
- **实现：** proto `int64` 无 optional；`toProto` 用 0 占位。
- **原因：** 契约未改可空类型；区分需后续契约变更。

### Dev-5：未做「覆盖 UPDATE pending」

- **早期 UX/SPEC 草稿：** 曾有覆盖文案。
- **实现：** 仅 INSERT；文档与 Issue 已改为不覆盖。
- **原因：** 产品改口后全链路对齐。

## Tradeoffs

### T1：多 pending 同维 vs 全局 pending 唯一

| 方案 | 利 | 弊 |
|---|---|---|
| A 全局唯一 pending | 运营简单、审批无歧义 | 二次申请被拒或需覆盖语义 |
| B 允许多 pending（选用） | 并行申请灵活 | 后审覆盖先审；列表更嘈杂 |

选用 B：按产品确认；运营需按 `request_id` 批审。

### T2：审批成功与配额生效解耦 vs 事务式强一致

| 方案 | 利 | 弊 |
|---|---|---|
| A 状态与 Core 同成败 | 前端语义简单 | Core 抖动导致无法审批落库 |
| B 先状态后补偿（选用） | 审批轨迹保留 | 短暂「approved 但 total 未变」 |

选用 B：对齐 SPEC 与创建租户配额补偿。

### T3：request_id 复用网关 ID vs service 新 UUID

| 方案 | 利 | 弊 |
|---|---|---|
| A 网关 ID（选用） | 与链路追踪一致、少生成点 | 依赖 header 形态（故兼容 `req_`） |
| B service uuid.New | 形态可控 | 与网关日志断裂 |

## Verification commands run

```text
cd repo/services/tenant-service && go test ./internal/service/ -count=1 -run QuotaChange
cd repo/services/ani-gateway && go test ./internal/router/ -count=1 -run TenantList
python scripts/validate_component_imports.py --root .
# review-it（复审）：clean — 无 accepted/actionable findings
# make test 全量：曾因 Windows Sandbox symlink 无关失败；本批聚焦测试通过
```

## Follow-ups

- [ ] Feature batch 四文件：README / CURRENT-SPRINT / ANI-06（若本 PR 合入）
- [ ] 可选：live PG 集成；审批响应提示字段 / old_value nullable（见 Open Questions）
