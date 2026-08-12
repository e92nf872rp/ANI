# Core 数据面 Gateway handler + 安全加固（data_query / data_table）— Issue #026

完成日期：2026-08-11
对应 Issue：#026 core-data-plane-handler-security（Phase A）
依赖：#25（port/adapter）
SPEC：`design-kb-persistence-to-core-datapipe` §3.2, §3.3
验证结果：`go build` PASS，`go test` 44 个数据面单测 PASS，`validate_component_imports.py` PASS，`validate_spec_split_contract.py` PASS，E2E 6/6 PASS（真实 PG）

## 实现了什么

实现 ANI Gateway 的数据面 HTTP handler（`POST /data/query`、`POST /data/tables`）及其安全加固，连接 issue #025 的 `ports.SQLDataPlane` adapter。Handler 从 middleware 获取租户上下文，强制 service/platform scope，执行目标表白名单、破坏性语句拒绝、参数化查询、按 service identity 限流、完整审计。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/router/data_plane_resources.go` | 新增 | `/data/query` + `/data/tables` handler、安全加固（白名单/破坏性拒绝/限流/参数上限） |
| `services/ani-gateway/internal/router/data_plane_resources_test.go` | 新增 | 44 个单测（白名单、破坏性、scope、限流、参数化、超时） |
| `services/ani-gateway/internal/router/router.go` | 修改 | `RegisterOptions` 新增 `DataPlane` + `Store`，注册 `/data/query` + `/data/tables` 路由 |
| `services/ani-gateway/main.go` | 修改 | 从 `instanceRuntime.DataPlane` + `gatewayStore` 注入到 router |
| `pkg/bootstrap/instance.go` | 修改 | `InstanceRuntime` 新增 `DataPlane` 字段，复用同一 PG 连接池 |
| `pkg/ports/data_plane.go` | 修改 | `DataPlaneQueryRequest` + `CreateTableRequest` 新增 `ServiceIdentity` 字段 |
| `pkg/adapters/postgres/data_plane_audit.go` | 修改 | 审计记录新增 `ServiceIdentity`、`StatementHash`、`DurationMs` |
| `pkg/adapters/postgres/data_plane.go` | 修改 | 填充审计新字段 |
| `services/ani-gateway/internal/middleware/auth.go` | 修改 | dev mode 新增 `X-Dev-Scope` header 支持 |
| `pkg/adapters/postgres/e2e_data_plane_test.go` | 新增 | E2E 测试（`//go:build e2e`），连接真实 PG |
| `api/openapi/v1.yaml` | 修改 | `DataQueryResponse` 新增 `columns` 字段 |

## 完工标准达成

- [x] [SPEC §3.2] `POST /data/query` 调 `SQLDataPlane.QueryTx`；`POST /data/tables` 调 `CreateTable`
- [x] handler 从 middleware 获取租户上下文，不信任请求体租户参数
- [x] [SPEC §3.3-1] 参数化唯一（禁止 params 拼接进 SQL）
- [x] [SPEC §3.3-2] 目标表白名单校验（knowledge_bases/kb_documents/kb_chunks/kb_messages/kb_sessions/async_tasks/outbox_events）
- [x] [SPEC §3.3-4] 禁止破坏性语句（DROP/TRUNCATE/ALTER SYSTEM/COPY/pg_read_file/ALTER TABLE DROP COLUMN/CREATE EXTENSION/GRANT/REVOKE/DO block），422 拒绝
- [x] [SPEC §3.3-5] 按 service identity 限流 + SQL 长度(16KiB)/参数数(100)/语句数(16)/耗时(30s) 上限
- [x] [SPEC §3.3-6] 完整审计：service、tenant、表、语句哈希、耗时
- [x] [SPEC §3.3-7] 仅对 Services service identity 开放，不对租户最终用户开放
- [x] `make validate-architecture` 通过（`validate_component_imports.py` + `validate_spec_split_contract.py`）
- [x] E2E 6/6 PASS（参数化查询、多语句、审计、建表、RLS、超时）

## Design Decisions

### 1. 破坏性语句正则扩展范围

**Ambiguity:** SPEC §3.3-4 列出了 DROP/TRUNCATE/ALTER SYSTEM/COPY/pg_read_file 五类禁止语句，但没有明确是否应覆盖语义等价的变体（ALTER TABLE DROP COLUMN、CREATE EXTENSION、GRANT/REVOKE、DO block）。

**Choice:** 在 `destructiveStatementRe` 正则中额外覆盖了 `ALTER TABLE ... DROP COLUMN`、`CREATE EXTENSION`、`GRANT`、`REVOKE`、`DO $$ ... $$` 五类。

**Rationale:** 这些语句在语义上具有破坏性或安全风险：DROP COLUMN 删除数据、CREATE EXTENSION 加载不受信任的代码、GRANT/REVOKE 变更权限、DO block 可嵌入任意 PL/pgSQL。正则覆盖比依赖 DB 层权限控制更早拦截，符合"defense in depth"原则。

### 2. `X-Dev-Scope` dev-mode header

**Ambiguity:** Gateway 的 dev auth mode (`ANI_AUTH_MODE=dev`) 硬编码 scope 为 "tenant"，但数据面 handler 要求 scope 为 "platform" 或 "service"，本地 E2E 测试无法触达。

**Choice:** 在 `middleware/auth.go` 的 dev 分支中新增 `X-Dev-Scope` header，允许覆盖 scope（默认仍为 "tenant"）。

**Rationale:** 仅在 `ANI_AUTH_MODE=dev` 时生效，生产环境不受影响。让本地 E2E 测试能模拟 service identity 调用数据面，无需真实 auth-service token。

### 3. `DataPlaneQueryResponse` 新增 `columns` 字段

**Ambiguity:** Issue #025 的 adapter 已经填充 `DataPlaneQueryResult.Columns`，但 issue #026 的 handler 最初未透传此字段。开发记录 #025 已将此标记为开放问题。

**Choice:** 在 `DataPlaneQueryResponse` 新增 `Columns []string` 字段（`json:"columns,omitempty"`），并同步更新 OpenAPI `DataQueryResponse` schema。

**Rationale:** `map[string]any` 的 JSON key 经 `encoding/json` 排序后是字母序而非 schema 序，客户端无法获得真实列顺序。透传 `columns` 解决了此问题。

## Deviations

### 1. `writeDataPlaneError` 迁移到 `errors.APIError` 规范类型

**Spec said:** Gateway 声明了 `errors.APIError` 作为规范错误响应类型，文档要求"Every API error MUST use this format"。

**Implementation:** 将 `writeDataPlaneError` 从 `map[string]any` 迁移到 `apierrors.APIError` 结构体。使用 `apierrors` 别名避免与标准库 `errors` 冲突。保留 `middleware.GetRequestID(c)` 作为 request_id 来源（与网关其他 handler 一致），不调用 `c.Abort()`（caller 已 return）。

**Why better:** 向规范类型靠拢，减少 `map[string]any` 的 ad-hoc 模式。`writeInstanceError` 仍使用 `map[string]any`，后续应统一迁移。

### 2. `collectRows` 保留 `map[string]any` 而非改为 `[][]any`

**Spec said:** 框架审查建议 `[][]any` + 共享 `[]string` columns 可减少 10000 行时的 map 分配压力。

**Implementation:** 保留 `[]map[string]any`。

**Why:** OpenAPI 契约定义 `rows` 为 `items: { type: object }`（JSON 对象数组），改为 `[][]any`（数组的数组）会破坏 wire format。需同步改 OpenAPI spec 且影响所有客户端 SDK，超出 issue #026 范围。

## Tradeoffs

### 1. `countStatements` 使用 `strings.Count` vs rune 循环

**Alternatives:**
- (A) `for _, ch := range sql` 逐 rune 解码 — 原实现
- (B) `strings.Count(sql, ";")` — runtime 字节级扫描

**Choice:** (B)，快 3-10x。

**Why:** 输入已限制 16KiB，每请求调用一次，绝对开销极小但修复无成本。`strings.Count` 是 Go runtime 内联的字节级实现，语义等价（`;` 是 ASCII 0x3B，rune 循环不会从 `;` 字节组合出 rune）。

### 2. E2E 测试直接连接 PG adapter vs 完整 gateway HTTP 栈

**Alternatives:**
- (A) 启动完整 gateway 二进制 + HTTP 请求 — 测试全栈
- (B) 直接实例化 `SQLDataPlane` adapter 连接真实 PG — 测试 adapter+DB

**Choice:** (B)。

**Why:** (A) 需要配置 K8s/Harbor/MinIO/Milvus 等全部 runtime provider，启动复杂度过高且超出 issue #026 scope。adapter 是 handler 的核心依赖，直接测试 adapter+DB 覆盖了参数化查询、多语句、审计、RLS、超时等核心路径。Handler 层的安全加固（白名单、scope、限流）由 44 个单测覆盖。

## Open Questions

### 1. `data_plane_audit` 表迁移

Issue #027 应创建 `data_plane_audit` 表的受管迁移。E2E 测试中临时通过 `CREATE TABLE IF NOT EXISTS` 创建该表，生产环境需要正式 migration。

### 2. `writeInstanceError` 统一迁移

`writeInstanceError` 仍使用 `map[string]any`，应与 `writeDataPlaneError` 一起迁移到 `errors.APIError`。这是框架级技术债，超出 issue #026 范围。

### 3. 错误码命名空间

数据面 handler 使用未前缀的通用错误码（`FORBIDDEN`、`BAD_REQUEST`），而 instance handler 使用前缀码（`INSTANCE_NOT_FOUND`）。应统一命名空间策略（如 `DATA_PLANE_FORBIDDEN`），后续处理。

### 4. 幂等性键

`/data/query` 和 `/data/tables` 未强制 `idempotency_key`。`/data/query` 是只读无需幂等；`/data/tables` 是 platform-managed DDL，SPEC 未要求幂等。如果未来需要，可补充。

## 验证命令

```bash
# 构建
go build ./services/ani-gateway/... ./pkg/...

# 单元测试（44 个数据面测试）
go test ./services/ani-gateway/... ./pkg/bootstrap/... ./pkg/adapters/postgres/... ./pkg/ports/...

# 架构验证
python scripts/validate_component_imports.py --root .
python scripts/validate_spec_split_contract.py --root .

# E2E 测试（需 SSH 隧道到服务器 PG）
# ssh -L 15432:10.111.199.100:5432 kubercloud@10.10.1.66 -N
$env:DATABASE_URL="postgres://ani:ani_dev_password@127.0.0.1:15432/ani?sslmode=disable"
go test -tags=e2e -v -timeout 120s ./pkg/adapters/postgres/ -run E2E
```

## E2E 测试结果（2026-08-11）

```
=== RUN   TestE2EQuerySelect              --- PASS (0.55s)  参数化查询 + columns 透传
=== RUN   TestE2EQueryMultiStatement       --- PASS (0.17s)  多语句批执行
=== RUN   TestE2EQueryAudit                --- PASS (0.57s)  审计记录 (service/tenant/hash)
=== RUN   TestE2ECreateTable               --- PASS (0.34s)  CreateTable managed DDL
=== RUN   TestE2EQueryRoleTenantWithRLS    --- PASS (0.35s)  RLS 隔离 (339 rows)
=== RUN   TestE2EQueryTimeout              --- PASS (2.00s)  2s 超时截断 pg_sleep(10)
PASS — ok github.com/kubercloud/ani/pkg/adapters/postgres 5.405s
```
