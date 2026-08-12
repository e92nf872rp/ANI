# DESIGN: kb-service 业务数据访问收口到 Core 通用数据面（SQL-over-HTTP）

> 状态：**草案待评审**（未实现）
> 目标分支：`backend-impl`
> 产品线：Core（通用基础设施数据面）+ Services（kb-service 迁移）
> 作者：Services 团队（AI 辅助）｜需 Core 共同评审
> 关联 SPEC：`spec-services-kb-service.md`（现态：asyncpg 直连 + RLS）
> 日期：2026-08-10

***

## 1. 背景与动机

### 1.1 现状

kb-service 通过 `asyncpg` **直连 PostgreSQL**，自行 `建表` / 读写以下 7 张表，并自研 RLS（`app/repositories/rls.py` 用 `SET LOCAL app.current_tenant_id`）：

| 表                 | 现 repository        | 主要使用方                                               |
| ----------------- | ------------------- | --------------------------------------------------- |
| `knowledge_bases` | `knowledge_base.py` | CreateKB/GetKB/ListKBs/DeleteKB                     |
| `kb_documents`    | `document.py`       | GetDocumentUploadURL/Notify/Get/List/DeleteDocument |
| `kb_chunks`       | `chunk.py`          | keyword\_search(pg\_trgm)/list/count                |
| `kb_messages`     | `message.py`        | Query 持久化                                           |
| `kb_sessions`     | `message.py`        | Query 会话                                            |
| `async_tasks`     | `async_task.py`     | 幂等重放 / parse 任务跟踪                                   |
| `outbox_events`   | `outbox.py`         | outbox 派发（跨租户 BYPASSRLS）                            |

### 1.2 问题

1. **违反 CLAUDE.md §3 分层边界**：Services 被要求「只能通过 Core OpenAPI REST API / Core SDK 调用 Core，禁止直接操作底层组件」。kb-service 直连 PostgreSQL 属于绕过分层边界直接操作底层数据组件。
2. **Schema 所有权分散**：kb-service 私有 `migrations/` 含建表逻辑（`kb_chunks`、pg\_trgm 等），Core 无法统一管控租户/多租户与迁移。
3. **RLS 实现重复**：自研 `rls.py` 与 Core Go 侧 `pkg/adapters/runtime` 的其他 store 未形成唯一实现。

### 1.3 目标（本次设计确定的迁移方向）

* 将 7 张表的**建表与读写**收口到 Core 托管的**通用数据面**。

* kb-service **彻底移除** **`asyncpg`** **直连与** **`rls.py`**，改为经 `CoreClient` 调用数据面 API。

* 作为通用基础设施能力，数据面本身不进「业务资源回流 Core API」范畴，需在 Core 评审中论证（见 §7）。

***

## 2. 决策记录（已与用户确认）

| 决策点                            | 结论                                         | 说明                                                                                             |
| ------------------------------ | ------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| 数据面形态                          | **通用数据面（SQL-over-HTTP）**                   | `POST /data/query` 接受参数化 SQL，单请求=单事务，保留跨表原子语义；另设受限的建表端点。                                       |
| 迁移范围                           | **全部 7 张表**                                | KB 业务 5 表 + `async_tasks` + `outbox_events` 统一收口到数据面。                                          |
| async\_tasks/outbox\_events 归属 | **统一收数据面**（不复用 Core 既存 task/outbox 专有 API） | 保持 7 表一致性；Core 既有 `pkg/repo/task_repo.go`/`outbox_repo.go` 能力可离线对照，但不在本设计中作为 kb-service 的目标端点。 |

> 注：用户问题描述曾写「8 张表」，实际 repo 为 **7 张表**，本设计按 7 张执行。

***

## 3. Core 侧：通用数据面契约草稿

### 3.1 端点

#### `POST /api/v1/data/query`

读写（DML + 查询）。请求体显式事务（`BEGIN … COMMIT/ROLLBACK`），一个调用 = 一个事务。

```yaml
/ data/query:
  post:
    x-internal: services-data-plane
    summary: 参数化 SQL 执行（单事务），供 Services 业务读写
    security:
      - serviceIdentity: []
    requestBody:
      content:
        application/json:
          schema:
            type: object
            required: [sql, params]
            properties:
              sql:    { type: string, description: 参数化 SQL，占位符 $1..$n；一条或多条（同一事务） }
              params: { type: array, items: {}, description: 绑定参数，禁止拼接进 SQL }
              role:   { type: string, enum: [tenant, service], default: tenant,
                        description: tenant=按 X-Tenant-Id 设 RLS；service=跨租户（outbox 派发器专用） }
    responses:
      '200':
        description: 成功
        schema:
          type: object
          properties:
            rows:       { type: array, items: { type: object } }
            rowcount:   { type: integer }
            last_result: { type: boolean }
    x-errors:
      - 400 INVALID_PARAMETER
      - 403 FORBIDDEN（越权表/DDL/未授权 service role）
      - 429 RATE_LIMITED
      - 422 UNSUPPORTED_QUERY（含 DROP/多语句注入等被拒）
```

#### `POST /api/v1/data/tables`

受限建表（DDL）。**非**任意动态 DDL：仅接受受管 schema 定义，走 Core 迁移编排，记录审计。

```yaml
/ data/tables:
  post:
    x-internal: services-data-plane-admin
    summary: 建表/变更 Services 业务表（受管迁移）
    security:
      - platformAdmin: []
    requestBody:
      content:
        application/json:
          schema:
            type: object
            required: [name, definition]
            properties:
              name:       { type: string }
              definition: { type: string, description: 受管 DDL（create/alter），白名单校验，禁止 DROP/TRUNCATE 之外破坏性语句 }
    responses:
      '201': { description: 表已创建 }
      '422': { description: 非受管 DDL 被拒 }
```

### 3.2 数据面实现要点（Core `pkg` + gateway）

* **Handler**：`data_query` / `data_table` 两个 Gateway handler（Services boundary 内，不绕过 port/contract）。

* **Port/Adapter**：`pkg/ports` 新增数据面能力抽象（如 `SQLDataPlane` 接口：`QueryTx`、`CreateTable`），`pkg/adapters` 内实现对接 Core PG 连接池（复用 `pkg/bootstrap` DB）。

* **RLS 收口**：`X-Tenant-Id` → handler 内 `SET LOCAL app.current_tenant_id`；`role=service` 用服务身份（跨租户，对应原 `ani_outbox_publisher` BYPASSRLS 语义），需独立审计。

* **事务**：`/data/query` 每次执行为单事务，多语句在同一 `BEGIN/COMMIT` 内。

* **迁移编排**：kb-service 原 `migrations/`（`001_pg_trgm_extension`、`002_kb_chunks`、`003_kb_retrieval_mode`）迁移至 Core 受管迁移目录，由 Core 统一执行。

### 3.3 安全加固（最高风险面，强制前置）

1. **参数化唯一**：所有业务 SQL 走 `$1..$n` 绑定；任何情况下禁止把 `params` 拼接进 SQL 字符串。
2. **目标表白名单**：`/data/query` 的 SQL 需命中已注册的 Services 表（`knowledge_bases|kb_documents|kb_chunks|kb_messages|kb_sessions|async_tasks|outbox_events`...），其余拒绝。
3. **DDL 独立受限**：仅 `/data/tables`、仅平台管理员、受管校验、完整审计。
4. **禁止破坏性语句**：`DROP`、`TRUNCATE`、`ALTER SYSTEM`、`COPY` 到外部、`pg_read_file` 等一律拒绝。
5. **限流与超时**：按 service identity 限流；SQL 长度/语句数/执行时间上限，防止滥用拖垮 Core DB。
6. **审计**：每次 `data_query`/`data_table` 记录 service、tenant、表、语句哈希、耗时，写入审计日志。
7. **仅服务身份**：数据面只对 Services service identity 开放，不对租户最终用户开放（租户用户走 Console → kb-service → 数据面，不直连）。

***

## 4. kb-service 侧迁移方案

### 4.1 CoreClient 扩展

在 `app/core_api/client.py` 新增：

* `data_query(sql, params, role="tenant") -> {rows, rowcount}`：调 `POST /data/query`。

* `create_table(name, definition)`：调 `POST /data/tables`（供迁移使用）。

### 4.2 repositories 改写

`app/repositories/*.py` 从「接收 `asyncpg.Connection` 直连」改为「接收 `CoreClient`（数据面）」。签名逐一对齐现语义：

| 现函数                                                     | 改法（折叠为一次性 HTTP 调用）                                            |
| ------------------------------------------------------- | ------------------------------------------------------------- |
| `knowledge_base.create_kb`                              | `data_query("INSERT … RETURNING …")` 单事务                      |
| `document.create_document`                              | 同上                                                            |
| `chunk.keyword_search`                                  | `data_query("SELECT … similarity(content,$1) …")` 保留 pg\_trgm |
| `message.create_session_in_tx` + `insert_message_in_tx` | **合并**为一次 `data_query` 多语句（用户消息+会话同事务）                        |
| `async_task` + `outbox.insert_event`                    | **合并**为一次 `data_query` 多语句（doc+task+outbox 同事务，保 US-010）      |
| `outbox.list_undispatched / mark_dispatched`            | `role="service"` 跨租户调用                                        |

**跨表原子事务折叠**（关键）：

* `NotifyDocumentUploaded`：原 3 表同事务（kb\_documents + async\_tasks + outbox\_events）→ 单次 `data_query` 多语句（STABLE）。

* `Query` 用户消息分支：原 2 表同事务（kb\_sessions + kb\_messages）→ 单次 `data_query` 多语句（STABLE）。

`rls.py` 删除：租户上下文完全由 `role="tenant"` + `X-Tenant-Id` 在 Core 侧注入。

### 4.3 进程装配简化

`main.py`：

* 删除 `_db_pool`、`_outbox_pool` 两个 asyncpg pool 及其事件循环绑定逻辑。

* 删除 `_build_grpc_pool` / `_build_pool`。

* outbox dispatcher 改为注入数据面 client（`role="service"`），轮询/标记走 `data_query`。

* `/readyz` 的 `db`/`outbox_db` 语义改为「数据面可达性」探活。

`requirements.txt`：移除 `asyncpg`。

### 4.4 迁移目录

kb-service `migrations/` 删除，建表 DDL 移交 Core 受管迁移；kb-service 启动不再自助建表。

***

## 5. 演进顺序

| Phase      | 内容                                                                                   | 产出                                       | 门禁                                                            |
| ---------- | ------------------------------------------------------------------------------------ | ---------------------------------------- | ------------------------------------------------------------- |
| **A**      | Core 数据面：v1.yaml 契约 + port/adapter + handler + 安全加固 + Core 单测 + 迁移编排                 | Core 端除零，`/data/query`、`/data/tables` 可用 | `make validate-services`、`make validate-architecture`、Core 单测 |
| **A-gate** | **Core 共同评审 + CODEOWNERS approve**（触碰 v1.yaml/pkg/gateway）                           | 评审通过                                     | CODEOWNERS @e92nf872rp                                        |
| **B**      | kb-service 迁移：CoreClient 扩展 + 7 repo 改写 + 删 asyncpg/rls.py + main.py 简化 + outbox 跨租户 | kb-service 无直连，pytest 全绿                 | `pytest`、`make validate-services`                             |
| **C**      | 验证收口：语义对比（US-010 原子性、Query、pg\_trgm 检索）、真实 PG 联调、回归测试                                | 迁移后行为与现状一致                               | 全量回归 + `git diff --check`                                     |

> 提交纪律按 [ANI-15-GitHub-协作规范与提交纪律.md](../../../../../ANI-15-GitHub-协作规范与提交纪律.md)：`backend-impl` 分支开发，**不自动 commit/merge**；Phase A 触及 Core 保护目录必须 Core 评审后合入。

***

## 6. 验证计划

1. **Core 单测**：数据面 handler/port/adapter；安全用例（注入、越权表、DDL 拒绝、param 拼接拒绝）。
2. **kb-service pytest**：现有 `test_grpc_server.py`、`test_us010_wiring.py`、`test_message_repository.py`、`test_outbox_dispatcher.py` 改为对数据面（mock / MockTransport）断言相同语义。
3. **语义等价对比**：同一输入下，迁移前后 DB 落库结果与响应一致（尤其 US-010 原子 outbox、Query 会话持久化）。
4. **真实 PG 联调**：Core 数据面 + kb-service 端到端；验证 RLS 生效（跨租户读不到）。
5. 回归命令：`make validate-services`、`make validate-architecture`、`git diff --check`。

***

## 7. 边界影响与评审项（必须由 Core 团队拍板）

### 7.1 触碰的 Core 保护目录

* `repo/api/openapi/v1.yaml`（新增 `/data/query`、`/data/tables`）

* `repo/pkg/ports`、`repo/pkg/adapters`（数据面 port/实现）

* `repo/services/ani-gateway`（两个 handler）

* `repo/deploy`（迁移编排，视范围）

→ 按 [ANI-SERVICES-TEAM-GUIDE.md](../../../../../ANI-SERVICES-TEAM-GUIDE.md) §4.2 需 Core `@e92nf872rp` 共同评审 + `make validate-services`。

### 7.2 架构张力：是否算「Services 业务资源回流 Core API」

* **立场（本设计）**：数据面是**通用基础设施能力**（如同 object-store/vector-store），不是 knowledge-bases 专有端点；业务资源仍由 kb-service 通过 `/api/v1/svc/*` 对外暴露。把「SQL 执行」纳为 Core 基础设施服务，与 CLAUDE.md §3 的「Core 提供基础设施、Services 经 OpenAPI 调用」一致。

* **需要 Core 拍板的点**：

  1. 是否接受 Core 托管 Services 业务表 schema 与 SQL 执行面（即数据面范围）。
  2. RLS 的「service 跨租户角色」安全审核标准。
  3. 通用 SQL 代理的注入/审计/限流保障是否达标；是否需要限定为「受管查询管理器」而非通用 SQL 代理。

### 7.3 Karpathy 原则五论证（新增实体的必要性）

* **为什么不能继续让 Services 直连 PG**：违反 CLAUDE.md §3 分层；schema 分散、RLS 重复、无统一迁移/审计。

* **为什么用通用数据面而非逐个资源端点**：7 张业务表语义各异（含 pg\_trgm 检索、幂等、outbox），逐个塑造成 Core 专有端点会要求 Core 深度理解每个 Services 业务，违背「Core 只管基础设施」；通用数据面把 schema 所有权收归 Core、把业务语义留在 kb-service，边界最清晰。代价是 Core 必须承担 SQL 执行面的安全成本。

***

## 8. 风险与开放问题

| 风险/问题                                    | 影响  | 缓解                                                    |
| ---------------------------------------- | --- | ----------------------------------------------------- |
| 通用 SQL 代理安全风险                            | 高   | §3.3 强制加固 + Core 安全评审 + `role` 隔离                     |
| 跨租户 service 角色滥用                         | 中   | `role=service` 仅限平台受管 service identity + 独立审计 + 白名单表  |
| 多语句事务折叠的 SQL 复杂度                         | 中   | 迁移脚本逐用例等价测试（US-010 / Query）                           |
| pg\_trgm 依赖 PostgreSQL 扩展                | 低   | 扩展随表迁入 Core PG，`similarity` 语义不变                      |
| 性能（每业务操作一次 HTTP 往返）                      | 中   | kb-service 同进程内复用持久 `CoreClient`（httpx 连接池）；必要时数据面走内网 |
| Core 既有 `task_repo`/`outbox_repo` 与数据面并存 | 中   | 本设计不再让其作为 kb-service 目标；是否统一归并由 Core 评审决定             |
| `async_tasks` 幂等键唯一约束语义                  | 需复核 | 迁移后依赖数据面在单事务内保证 UNIQUE 约束不变量                          |

***

## 9. 验收定义（Done）

1. `repo/api/openapi/v1.yaml` 含 `/data/query`、`/data/tables` 契约，SDK/API 文档生成无漂移。
2. Core 数据面 handler/port/adapter + 安全用例通过；`make validate-services`、`make validate-architecture` 通过。
3. kb-service 无 `import asyncpg`、无 `rls.py`、无 `migrations/`；7 个 repository 全部改走 `CoreClient.data_query`/`create_table`。
4. `NotifyDocumentUploaded`、`Query` 的跨表原子语义在数据面单事务下保持；pytest 全绿。
5. 真实 PG 端到端：RLS 生效、pg\_trgm 检索正常、跨租户隔离成立；回归命令全绿。
6. Core 共同评审（CODEOWNERS）已 approval；提交纪律遵守（后端分支，人工 gate）。

