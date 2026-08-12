# Implementation Notes — Issue #029

> **Issue:** kb-service repository 改走数据面（knowledge_base / document / chunk）
> **Branch:** `backend-impl`
> **SPEC:** `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2, §6)
> **Date:** 2026-08-11
> **Type:** core (services)

---

## 1. Design Decisions

### DD-1: `tenant_id` 参数保留在签名但不传给 `data_query`

**Ambiguity:** SPEC §4.2 要求租户上下文由 Core 侧 `X-Tenant-Id` header + `role="tenant"` 注入 RLS，不通过 SQL 传递。但三个 repo 的方法签名（`create_kb(tenant_id, ...)`, `get_document(tenant_id, ...)` 等）是否保留 `tenant_id` 参数？

**Choice:** 保留 `tenant_id` 参数在签名中，但不传递给 `core.data_query()`。租户上下文完全由 `CoreClient` 的 `X-Tenant-Id` header 注入。

**Rationale:**
1. 保持 API 签名兼容性 — `grpc_server.py` 中所有 RPC 方法调用 `repo.xxx(tenant_id, ...)` 时不需要修改。
2. `tenant_id` 在签名中提供语义清晰度 — 调用方明确知道操作属于哪个租户。
3. SPEC §4.2 明确要求 RLS 上下文由 Core 侧注入，不在 SQL `WHERE` 中传递。

### DD-2: `_ts()` 函数支持 ISO 字符串解析

**Ambiguity:** Core 数据面返回 PostgreSQL `timestamptz` 字段为 ISO 8601 字符串（JSON 编码），而旧 asyncpg 直接返回 `datetime` 对象。gRPC `_ts()` 辅助函数需要处理哪种格式？

**Choice:** `_ts()` 同时接受 `datetime` 对象和 ISO 8601 字符串，自动用 `datetime.fromisoformat()` 解析字符串。

**Rationale:**
1. 数据面 JSON 响应中时间戳为字符串格式 `"2026-08-11T07:12:34.567890+00:00"`。
2. 单元测试可能注入 `datetime` 对象（mock 场景）。
3. `except ValueError` 防御性回退到空 Timestamp（epoch 0），不会崩溃。

### DD-3: E2E 测试使用 dev auth mode + `X-Dev-Scope` header

**Ambiguity:** 本地 E2E 测试需要网关认证。生产模式需 JWT token + auth-service，但本地测试不想依赖 auth-service。SPEC 未规定本地测试认证方式。

**Choice:** 使用 `ANI_AUTH_MODE=dev` + `X-Dev-Scope` header 进行认证。kb-service 的 `_default_core_client` 在 dev 模式下注入 `X-Dev-Scope: service` header。

**Rationale:**
1. Gateway dev auth mode 已支持 `X-Dev-*` header 认证（`auth.go:33-56`）。
2. `X-Dev-Scope: service` 使数据面 handler 接受 kb-service 的调用（`dataPlaneScopeAllowed("service") = true`）。
3. 生产模式需要真实 service token，超出 issue-029 scope（OQ-4 已记录）。

### DD-4: Gateway local 模式下初始化 DataPlane

**Ambiguity:** `instance_service_runtime.go` 的 `local` 分支返回空 `InstanceRuntime{}`（`DataPlane=nil`），导致本地 dev 模式 gateway 的 `/data/query` 返回 503。OQ-1（issue-028 记录）已提出此问题。

**Choice:** 修改 `newGatewayInstanceRuntime` 的 `local` 分支：当 `DATABASE_URL` 已配置时，调用 `ConnectInstanceService` 初始化 DataPlane，但只保留 `DataPlane` 和 `AsyncTasks` 字段，丢弃 K8s 相关的 `Service`/`Store` 字段。

**Rationale:**
1. 本地开发只需数据面，不需要 K8s 实例编排。
2. `ConnectInstanceService` 使用 `WorkloadProvider=""` → `workloadProviderAdapters` 返回 local adapter（不需要 K8s API）。
3. 保留 `AsyncTasks` 供 kb-service 的 parse 任务跟踪使用。

---

## 2. Deviations

### DEV-1: `role` 参数默认值保持 `"tenant"` 而非改为 `"service"`

**Spec:** SPEC §3.3-7 定义 `role=service` 为跨租户（outbox 派发器专用），`role=tenant` 为按 `X-Tenant-Id` 设 RLS。

**Implementation:** 三个迁移后的 repo 全部使用 `core.data_query()` 的默认 `role="tenant"`。未改为 `role="service"`。

**Why:** kb-service 的 KB/doc/chunk 操作都是租户范围内的，RLS 应根据 `X-Tenant-Id` 过滤。`role=service` 仅用于 outbox 派发器（issue-030 scope）。issue-028 记录中的 OQ-2 指出 `role=service` 额外要求 `scope=platform`，当前 kb-service 只有 `scope=service`，使用 `role=tenant` 是正确的。

### DEV-2: `document.py` 的 `_DOC_COLUMNS` 未包含 `doc_id` 和 `updated_at`

**Spec:** 原始 asyncpg 实现的 SELECT 列包含 `doc_id`, `updated_at`。

**Implementation:** `_DOC_COLUMNS = "id, kb_id, tenant_id, file_name, file_type, file_size_bytes, storage_path, checksum_sha256, parse_status, chunk_count, error_message, custom_metadata, created_at, parsed_at, object_id"`。实际 schema 无 `doc_id`/`updated_at` 列，使用 `object_id`/`parsed_at`/`custom_metadata`。

**Why:** 数据库 schema 已迁移（Core 受管迁移），新 schema 列名与旧不同。代码与实际 schema 一致，E2E 测试验证通过。

### DEV-3: E2E 测试创建了 MinIO `kb-docs` bucket

**Spec:** SPEC 未提及 E2E 测试需要预创建 MinIO bucket。

**Implementation:** E2E 测试运行前需通过 gateway `/api/v1/buckets` 创建 `kb-docs` bucket，否则文档上传的 presigned URL 生成会失败。

**Why:** MinIO bucket 不预创建时，`putObject` 生成 presigned URL 需要 bucket 存在。这是运行时前置条件，非代码缺陷。

---

## 3. Tradeoffs

### TR-1: 每次调用创建新 CoreClient + httpx 连接池 vs 持久复用

**Alternatives:**
- **A: 每个 RPC 新建 CoreClient**（当前实现）— `grpc_server.py` 的 `async with self._core_client_factory(tenant_id) as core` 每次 RPC 创建并销毁 CoreClient + httpx 连接池。
- **B: 持久复用 CoreClient**（SPEC §8 建议）— 在 `main.py` 创建单例 CoreClient，按 tenant_id 切换 header。

**Pros/Cons:**
- A: 简单、无状态、租户隔离天然安全；每次 RPC 连接开销（TCP+TLS handshake）。
- B: 性能更优（连接池复用）；但需处理租户上下文切换、生命周期管理。

**Chosen:** A（当前实现），因 OQ-3（issue-028 记录）已标记为预存在缺陷，修复需改 `main.py`/`grpc_server.py` 装配层，超出 issue-029 scope。

### TR-2: E2E 测试通过 gateway 全链路 vs 直接测试 SQL 层

**Alternatives:**
- **A: 全链路 E2E**（`e2e_issue029_full_stack.py`，12 测试）— httpx → gateway :8080 → kb-service gRPC → CoreClient → gateway /data/query → PostgreSQL。
- **B: SQL 层 E2E**（`e2e_issue029_data_plane.py`，25 测试）— 直接连接 PostgreSQL，验证三个 repo 的 SQL 语句正确性。

**Pros/Cons:**
- A: 验证完整调用链、gRPC 转换、auth、网关路由；但需要启动 3 个服务、依赖服务器组件。
- B: 快速验证 SQL 语义、RLS、pg_trgm；但不覆盖 gRPC/网关层。

**Chosen:** 两者都保留。A 覆盖集成层，B 覆盖 SQL 语义层。共 37 个 E2E 测试全部通过。

---

## 4. Open Questions

### OQ-5: 生产模式 `auth_token` 注入（承继 OQ-4）

`_default_core_client` 在 dev 模式下通过 `X-Dev-Scope: service` 认证。生产模式（`ANI_AUTH_MODE!=dev`）下 `extra_headers` 为空，不发送任何 scope header。需注入真实 service token（携带 `platform`/`service` scope 的 JWT）。

**需确认：** auth_token 注入由哪个 issue 负责？可能需要：
1. `config.py` 新增 `CORE_SERVICE_TOKEN` 环境变量。
2. `_default_core_client` 读取并注入 `Authorization: Bearer <token>`。
3. Gateway 的 `scopeAllowedForPath` 对 `/api/v1/data/*` 放行 `platform`/`service` scope（OQ-2）。

### OQ-6: Gateway `scopeAllowedForPath` 对数据面路径的放行（承继 OQ-2）

生产 auth 模式下，`scopeAllowedForPath` 对非 `/auth/platform/*` 路径只允许 `scope=tenant`。数据面 `/api/v1/data/query` handler 要求 `scope=platform|service`。需修改 `auth.go:225-233` 对 `/api/v1/data/*` 放行 `platform`/`service` scope。

**需确认：** 这是 gateway 侧修改，是否作为独立 issue 跟踪？

### OQ-7: `async_task.py` / `outbox.py` / `message.py` 迁移（issue-030 scope）

本次仅迁移 `knowledge_base.py`、`document.py`、`chunk.py` 三个 repo。`async_task.py`、`outbox.py`、`message.py` 仍使用 asyncpg 直连 + `rls.py`。

**需确认：** issue-030 的迁移计划是否与此一致？`rls.py` 在 issue-030 完成后可完全删除？

---

## 5. Verification commands run

```bash
# Python unit tests (Issue #029 AC)
cd repo/services/kb-service
python -m pytest tests/test_kb_repos_data_plane.py tests/test_core_client.py tests/test_grpc_server.py -x -q
# → 68 passed

# E2E SQL layer (25 tests)
python tests/e2e_issue029_data_plane.py
# → 25/25 PASS (PostgreSQL @ 10.10.1.66:30945)

# Go build + tests
cd repo/services/ani-gateway
go build ./...
# → ok
go test ./internal/router/ ./internal/middleware/ -count=1 -short
# → ok (both packages)

# E2E full stack (12 tests)
# Prerequisites: start 3 services via scripts/start_local_services.ps1
# Create MinIO bucket: POST /api/v1/buckets {"name":"kb-docs"}
python tests/e2e_issue029_full_stack.py
# → 12/12 PASS (gateway :8080 + kb-service :8002/:50053 + rag-engine :8001/:50052)

# Architecture validators
make validate-architecture
# → component import guard passed + architecture guardrails valid
```

---

## 6. Files Modified

| File | Change | Scope |
|------|--------|-------|
| `services/kb-service/app/repositories/knowledge_base.py` | asyncpg → CoreClient.data_query | Issue-029 |
| `services/kb-service/app/repositories/document.py` | asyncpg → CoreClient.data_query | Issue-029 |
| `services/kb-service/app/repositories/chunk.py` | asyncpg → CoreClient.data_query | Issue-029 |
| `services/kb-service/app/core_api/client.py` | 新增 `extra_headers` 参数 | E2E fix |
| `services/kb-service/app/api/grpc_server.py` | `_default_core_client` 注入 `X-Dev-Scope`; `_ts()` 支持 ISO 字符串; 移除冗余 `import json` | E2E fix |
| `services/ani-gateway/instance_service_runtime.go` | local 模式初始化 DataPlane | E2E fix |
| `services/kb-service/tests/e2e_issue029_full_stack.py` | 全栈 E2E 测试 (12 tests) | New |
| `services/kb-service/tests/e2e_issue029_data_plane.py` | SQL 层 E2E 测试 (25 tests) | New |

---

## 7. Acceptance Criteria Status

- [x] [SPEC] `knowledge_base.create_kb` → `data_query("INSERT … RETURNING …")` 单事务（SPEC §4.2）
- [x] [SPEC] `document.create_document` → `data_query` 单事务（SPEC §4.2）
- [x] [SPEC] `chunk.keyword_search` → `data_query("SELECT … similarity(content,$1) …")`，保留 pg_trgm 与 GIN 索引语义（SPEC §4.2）
- [x] 租户上下文由 `role="tenant"` + X-Tenant-Id 在 Core 侧注入，`rls.py` 不再被这三个 repo 使用（SPEC §4.2）
- [x] 签名/返回结构与现语义一致（list/count/soft_delete 等）
- [x] `pytest` 通过（68 unit + 25 SQL E2E + 12 full-stack E2E = 105 tests passed）
