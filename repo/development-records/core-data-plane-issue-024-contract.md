# CORE-DATA-PLANE-ISSUE-024：数据面契约（/data/query + /data/tables）

> **批次：** Core 数据面（SPEC: design-kb-persistence-to-core-datapipe）Phase A
> **完成日期：** 2026-08-10
> **Scope：** `repo/api/openapi/v1.yaml`（唯一手写改动）+ 再生成产物（`sdks/core/*`、`docs/api/*`、`frontends/console/src/api/core-schema.d.ts`）
> **依赖：** 无
> **Product line：** Core
> **下游：** #025 port/adapter → #026 handler/安全加固 → #027-#032 kb-service 迁移

## 交付内容

在 Core OpenAPI 契约新增通用数据面 2 端点 + 3 schema + 2 security scheme + 1 tag，使 Services 业务（kb-service）能经 Core OpenAPI 托管控建表与读写，而非直连 PostgreSQL。

### 新增端点（2 个）

| operationId | Method | Path | security | 语义 |
|---|---|---|---|---|
| `dataQuery` | POST | `/data/query` | `serviceIdentity` | 参数化 SQL，单请求=单事务；`role` enum `[tenant,service]` 默认 `tenant` |
| `dataCreateTable` | POST | `/data/tables` | `platformAdmin` | 受管建表（白名单 DDL），非受管 DDL → 422 |

### 新增 Schema（3 个）

- `DataQueryRequest`（`sql`/`params`/`role`）
- `DataQueryResponse`（`rows`/`rowcount`/`last_result`）
- `DataTableCreateRequest`（`name`/`definition`）

### 新增 Security Schemes（2 个）

- `serviceIdentity`（service-to-service bearer）
- `platformAdmin`（平台管理员 bearer）

## 1. Design Decisions

规范在若干处未指定具体取值，本实现的选择与理由：

- **`params` 标量类型收窄**：SPEC §3.1 为 `items: {}`（任意），本实现收窄为 `type: [string, number, boolean, 'null']`（标量 union）。理由：既是类型约束也是注入防护边界——绑定参数只应是可 JSON 序列化的标量，禁止嵌套对象/数组作为参数；kb-service 现有 SQL 参数均为标量。
- **SQL/参数数量上限取值**：SPEC §3.3 只要求"SQL 长度/语句数/执行时间上限"，未给具体阈值。本实现择定 `sql.maxLength: 16384`（16KiB）、`params.maxItems: 100`，并在 400 描述声明"结果集超上限（>10_000 行）"。理由：为下游 handler（#026）提供可执行的防滥用基准，防止拖垮 Core DB。
- **`/data/tables` 201 响应体**：SPEC §3.1 的 201 无 body。本实现扩展为 `{name, status}`（status enum `[created, applied]`），向调用方明确建表/变更结果。经用户确认保留（比无 body 更实用）。
- **`x-internal` 标记**：使用 `services-data-plane` / `services-data-plane-admin` 区分读写与受管建表两类 service-internal 端点（SPEC §3.1 如此定义），提示这些端点非租户最终用户直连。
- **新增 `DataPlane` tag**：SPEC 未指定 tag；为 API 文档分组新增。

## 2. Deviations

与 SPEC 的明确差异（均为有意收紧/扩展，非偏差事故）：

- **`params` 从 `items: {}` 收窄为标量 union**：规范宽松，实现更严。理由：类型安全 + 注入防护。
- **`/data/tables` 201 从"无 body"改为返回 `{name,status}`**：规范仅描述状态，实现返回建表结果。理由：更实用，随附的 handler 承诺随之增加（由此产生的下游契约承诺在 #026 兑现）。
- **400 错误描述追加具体上限语义**（"SQL 长度超限、结果集超上限 >10_000 行"）：规范 x-errors 只列错误码，实现补充触发条件说明。

## 3. Tradeoffs

- **params 标量 union vs 任意 JSON 数组**：标量更安全（防止注入/复杂结构滥用），但限制嵌套绑定参数；任意 JSON 灵活但敞口大。因 kb-service 只用标量参数，选标量。
- **`serviceIdentity`/`platformAdmin` 均为 bearer vs 更细粒度 scheme**：承认二者在 OpenAPI 层无法表达运行时权限差异（都是 `type: http/bearer`），实际"是否 service/平台管理员"需由 token 的 audience/scope 在运行时判定。这是 OpenAPI 承载力的边界，并非缺陷；运行时判定留给 #026 handler。
- **单事务多语句（一次请求）vs 每语句一次请求**：前者保留跨表原子语义（SPEC 关键要求，如 US-010 outbox + doc 同事务）；后者无原子性但更简单。选前者，符合设计意图。

## 4. Open Questions

需确认/跟进的开放项：

- **gateway 认证框架衔接（需 #026 补充）**：`scopeAllowedForPath`（[auth.go:225](file:///c:/Users/PC/Desktop/ANI/repo/services/ani-gateway/internal/middleware/auth.go#L225)）对所有非 `/auth/platform/*` 路径硬编码要求 `scope=tenant`。若 #026 不扩展它，`role=service`（跨租户）与 `platformAdmin`（platform scope）调 `/data/*` 都会被 403 拦截。**请在 #026 AC 中补充「扩展 `scopeAllowedForPath`/路由白名单使 `/data/*` 支持 service/platform scope」**。
- **`role=service` 跨租户安全审核标准**：SPEC §7.2 指出需 Core 拍板 "service 跨租户角色" 的安全审核标准。请确认。
- **通用 SQL 代理 vs 受管查询管理器**：SPEC §7.2-3 抛给 Core 的评审问题——数据面是否应限定为"受管查询管理器"而非通用 SQL 代理。实现按"通用数据面"落地，待评审确认。
- **feature-batch 收尾**：本 issue 属 Phase A 契约，是否需在 ship 前完成 development-records 四文件更新，请按 CLUDE.md feature-batch 规则确认。

## 5. 审查发现与修复（review-it 前置）

- 修复 **spec 漂移**：`v1.yaml`（简化版）与再生成产物（`core-schema.d.ts` 丰富版）不一致，会导致 `make validate-services` 漂移检查失败。已按 SPEC §3.3 完整契约对齐并重新生成全部产物，重跑生成校验和稳定。
- 补充**安全/性能上限契约**（SQL 长度、结果集上限、参数数量），对应 SPEC §3.3。
- review-it 结论：clean，无接受/可行动 finding 需本 issue 内修改。

## 6. Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `/api/v1/data/query`：body `{sql,params:[],role}`，role enum `[tenant,service]` 默认 `tenant` | `DataQueryRequest`（v1.yaml L403-428） | ✅ |
| `/api/v1/data/tables`：body `{name,definition}`，`platformAdmin` 安全，非受管 DDL → 422 | `/data/tables`（v1.yaml L5270-5312） | ✅ |
| 响应 `rows`/`rowcount`/`last_result`；错误语义 400/403/429/422 明确 | `DataQueryResponse` + `x-errors` | ✅ |
| `servers[0].url = https://{host}/api/v1` | v1.yaml L41（保持不变） | ✅ |
| SDK/API 文档/前端 schema 无漂移 | 再生成后校验和稳定；`validate_sdk_alpha`/`validate_api_docs_contract` exit 0 | ✅ |

## 7. 验证命令

```bash
cd repo
python scripts/validate_openapi_spec.py api/openapi/v1.yaml
python scripts/validate_spec_split_contract.py
python scripts/validate_api_docs_contract.py
python scripts/validate_component_imports.py --root .
python scripts/validate_sdk_alpha.py
python scripts/validate_sdk_beta.py
git diff --check
```

## 8. 边界声明

- 本 issue 只修改 Core OpenAPI 契约与再生成产物，**不涉及** handler/adapter 实现。
- Port/adapter 属于 #025，Gateway handler + 安全加固属于 #026。
- 本 issue 不声明 runtime ready 或 production ready；数据面安全加固由 #026 承载（合入前建议 security review）。
