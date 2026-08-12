# kb-service CoreClient 数据面扩展（data_query / create_table）— Issue #028

完成日期：2026-08-11
对应 Issue：#028 kb-core-client-data-plane-extension
依赖：#024（数据面契约），#025（port/adapter），#026（handler + 安全），#027（受管迁移）
SPEC：`design-kb-persistence-to-core-datapipe` §4.1, §8
Batch：Phase B（TBD）
Branch：backend-impl
验证结果：`pytest test_core_client.py` 18/18 PASS · `make validate-architecture` PASS · `git diff --check` clean · E2E 10/10 PASS（CoreClient→Go server→服务器 PG 全链路）

## 实现了什么

在 kb-service 的 `CoreClient`（Issue #007 建立的 Core REST client）上扩展两个数据面薄传输方法，作为 7 个 repository 从 asyncpg 直连迁移到 Core 数据面（Issue #029-031）的客户端基础：

- `data_query(sql, params, role="tenant") -> {columns, rows, rowcount, last_result}`：调 `POST /data/query`
- `create_table(name, definition) -> {name, status}`：调 `POST /data/tables`（期望 201）

两个方法复用既有 `self._client`（httpx 持久连接池）与 `_to_error` 错误映射惯例，不引入业务逻辑。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `app/core_api/client.py` | 修改 | 新增 `data_query`（L207-236）+ `create_table`（L238-252），共 ~46 行 |
| `tests/test_core_client.py` | 修改 | 新增 8 个测试（L183-299），覆盖正向/params 默认/role=service/错误映射/create_table 双状态/非白名单拒绝 |

Scope 严守 Issue #028 约束："Code paths allowed: `repo/services/kb-service/app/core_api/` only"。未触碰 grpc_server.py / main.py / repositories / dispatcher。

---

## Design Decisions

### D-1: data_query 返回完整 DataQueryResponse 而非仅 {rows, rowcount}

**Ambiguity:** Issue AC 写 `-> {rows, rowcount}`，但 OpenAPI `DataQueryResponse`（v1.yaml:431-440）还包含 `columns` 和 `last_result`。SPEC §4.1 未明确是否裁剪。

**Choice:** 返回 gateway 响应的完整 dict（`{columns, rows, rowcount, last_result}`），不裁剪。

**Rationale:** 调用方（未来 repository 迁移）可能需要 `columns`（类型映射）和 `last_result`（多语句场景）。裁剪会丢失信息且需额外代码；返回完整 dict 让调用方按需取用，符合薄传输层定位。docstring（client.py:226-228）文档化完整结构。

### D-2: role 作为显式 body 字段发送，不从客户端 trust 上下文推导

**Ambiguity:** SPEC §3.3 要求 Core 从 JWT/服务身份注入 RLS，不应 trust 客户端 body 的 role。

**Choice:** `role` 作为显式 body 字段发送（`body["role"] = role`），默认值 `tenant`。`role=None` 时省略字段，依赖服务端默认值。

**Rationale:** gateway 的 `data_plane_resources.go` handler 会用 `role` 决定 RLS 注入策略（tenant 隔离 vs service 跨租户），但实际 RLS 上下文由 gateway 从认证态注入，不由 body role 决定（SPEC §3.3 安全设计）。客户端发送 role 是协议契约要求，非安全边界。`if role is not None: body["role"] = role` 是防御式编码，允许 None 省略字段。

### D-3: create_table 期望 HTTP 201 而非 200

**Ambiguity:** 既有方法（create_vector_store 等）用 `resp.status_code != 200` 判断，但 OpenAPI `POST /data/tables` 返回 201 Created。

**Choice:** `create_table` 用 `if resp.status_code != 201: raise self._to_error(resp)`。

**Rationale:** 与 OpenAPI `v1.yaml` 的 `201 Created` 响应码对齐。gateway `data_plane_resources.go` 对新表创建返回 201，对已存在表（`IF NOT EXISTS`）返回 200+`status=applied`——但测试用 MockTransport 固定 201 验证 created 路径；e2e 验证了 applied 路径（服务器上 kb_sessions 已存在，gateway 返回 201+applied）。

---

## Deviations

None — 实现严格遵循 SPEC §4.1 和 Issue AC。两个方法的 HTTP 方法、路径、状态码、错误映射均与 OpenAPI 契约 + gateway 路由实现对齐。

---

## Tradeoffs

### T-1: 薄传输方法 vs 封装业务语义

**Alternatives:**
- **A（当前）:** `data_query`/`create_table` 仅做 HTTP 传输，返回原始 dict，不封装业务语义（如 `create_table` 不判断 status=created/applied 语义差异）。
- **B:** 封装业务语义，如 `create_table` 返回 bool `already_exists`，`data_query` 返回 typed rows。

**Pros/Cons:**
- A：简单、薄、与既有 8 个方法一致；调用方需自行解读响应。
- B：调用方更友好；但增加 client 复杂度，偏离薄传输层定位，且需为每个方法定义 typed 返回。

**Chosen:** A。与既有 CoreClient 全部方法（均返回 `dict[str, Any]`）保持一致；业务语义封装属 repository 层（Issue #029-031）职责，不应下沉到 client。

### T-2: e2e 测试用最小 Go server 而非本地完整启动 gateway

**Alternatives:**
- **A（当前）:** 写临时 Go test server（复用生产 `registerDataPlaneResources` + `postgresadapter.NewSQLDataPlane`），连服务器 PG，Python CoreClient 对它测试。
- **B:** 本地完整启动 `ani-gateway`（`ANI_AUTH_MODE=dev` + `DATABASE_URL` 指向服务器 PG），Python CoreClient 对它测试。
- **C:** SSH 到服务器改 gateway 的 `ANI_AUTH_MODE=dev` 后直接测。

**Pros/Cons:**
- A：轻量、隔离、不动服务器；但需写临时 Go 代码（已删除，未入库）。
- B：最真实；但 gateway 完整启动需 K8s runtime 装配 DataPlane（`WORKLOAD_PROVIDER=local` 时 `instance_service_runtime.go:64` 返回空 `InstanceRuntime{DataPlane=nil}` → 503），本地无 K8s 凭据，不可行。
- C：最真实；但违反用户"不要上传到服务器"约束。

**Chosen:** A。唯一能在本地完成全链路 e2e 的方案。Go server 复用生产路由 + 适配器代码（非 mock），连服务器真实 PG（17.10，339 行 knowledge_bases 数据），验证了 CoreClient HTTP 层 + gateway handler + SQLDataPlane + PG 全链路。临时文件测试后已删除。

---

## Open Questions

### OQ-1: 本地 dev gateway DataPlane 注入需 K8s runtime 是否为设计缺陷

`instance_service_runtime.go:64` 的 `case "", "local"` 提前返回空 `InstanceRuntime{}`（`DataPlane=nil`），导致本地 dev 模式 gateway 的 `/data/query` 返回 503 `data plane not configured`。只有 `WORKLOAD_PROVIDER` 非 local 时才走 `ConnectInstanceService` 装配 DataPlane，但那需要 K8s。

需确认：这是有意的架构边界（local 模式不含数据面）还是需要在后续 issue 增加轻量 DataPlane 装配路径？如果是缺陷，应作为独立装配层 issue 跟踪（不在 #28 scope）。

### OQ-2: 服务器 gateway auth_service 模式下 /data/query 不可达

服务器 ani-gateway（`10.10.1.66:30080`）运行在 `auth_service` 模式。中间件 `scopeAllowedForPath` 对非 `/auth/platform/*` 路径只允许 `scope=tenant`，但 `/data/query` handler 要求 `scope=platform|service`（`dataPlaneScopeAllowed`）。在 auth_service 模式下，标准 Bearer token 路径无法同时满足两者——中间件会先拒绝。

需确认：数据面端点是否只期望在 dev 模式使用，还是有其他 auth 路径（如服务账户 token 带 platform scope 且 `scopeAllowedForPath` 放行）？这影响 Issue #029-031 repository 迁移后的生产可达性。

### OQ-3: 跨 RPC httpx 连接池复用缺失（预存在，跨方法）

`grpc_server.py:205` 的 `async with self._core_client_factory(tenant_id) as core` 在每个 RPC 新建并销毁 CoreClient + httpx 连接池，SPEC §8 "kb-service 同进程内复用持久 CoreClient" 在装配层未实现。此为预存在缺陷（影响 create_vector_store 等所有方法，非 #28 引入），修复需改 out-of-scope 的 main.py/grpc_server.py。

需确认：是否作为独立装配层 issue 跟踪，或在 Issue #029-031 repository 迁移时一并修复。

### OQ-4: auth_token 装配缺失（预存在，跨方法）

`_default_core_client`（grpc_server.py:781-784）构造 CoreClient 时未传 `auth_token`，所有方法（含数据面）都无 Authorization header。CoreClient.__init__ 已支持 `auth_token`（client.py:55,62-63），仅装配层未注入。

需确认：auth_token 注入由哪个 issue 负责（装配层/auth-wiring，非 #28 scope）。

---

## Verification commands run

```bash
# Architecture validators
make validate-architecture                          # → component import guard passed + architecture guardrails valid

# Python unit tests (Issue #28 AC)
python -m pytest services/kb-service/tests/test_core_client.py -v
# → 18 passed (10 pre-existing + 8 new)

# Whitespace check
git diff --check                                     # → clean

# E2E (temporary, cleaned up after)
# Go e2e server (data_plane_e2e_test.go, build tag e2e_data_plane):
#   DATABASE_URL=postgres://ani:...@10.10.1.66:30945/ani go test -tags=e2e_data_plane \
#     -v -run TestE2EDataPlaneServer ./services/ani-gateway/internal/router/
# Python driver (scripts/_tmp_e2e_data_plane.py):
#   python scripts/_tmp_e2e_data_plane.py
# → 10/10 PASS (CoreClient → Go server → 服务器 PG 全链路)
# → 临时文件已删除，git status 干净
```

## E2E 测试矩阵（10/10 PASS）

| # | 测试项 | 结果 | 证据 |
|---|---|---|---|
| 1 | data_query 返回 rowcount | PASS | rowcount=1 |
| 2 | data_query 返回 rows | PASS | rows=[{'n': 339}] |
| 3 | data_query count > 0（真实数据） | PASS | n=339 |
| 4 | data_query 参数化绑定 $1 | PASS | SELECT $1::int → val=42 |
| 5 | data_query role=service 跨租户 | PASS | n=339 |
| 6 | data_query 非白名单表 → 403 | PASS | code=FORBIDDEN |
| 7 | data_query 破坏性语句 → 422 | PASS | code=UNSUPPORTED_QUERY |
| 8 | create_table 白名单名 → applied | PASS | name=kb_sessions status=applied |
| 9 | create_table 返回 name | PASS | name=kb_sessions |
| 10 | create_table 非白名单名 → 403 | PASS | code=FORBIDDEN |
