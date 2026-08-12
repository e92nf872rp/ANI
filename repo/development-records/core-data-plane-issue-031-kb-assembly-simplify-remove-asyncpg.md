# Implementation Notes — Issue #031

> **Issue:** kb-service 进程装配简化（移除 asyncpg / rls.py / migrations，outbox 跨租户）
> **Branch:** `backend-impl`
> **SPEC:** `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2, §4.3, §4.4)
> **Date:** 2026-08-11
> **Type:** core (services)

---

## 1. Design Decisions

### DD-1: `/readyz` 探活改为真实 GET gateway `/healthz`，而非「CoreClient 已构造」

**Ambiguity:** SPEC §4.3 要求 `/readyz` 的 db/outbox_db 语义改为「数据面可达性」探活，但未指定探活手段。CoreClient 构造总是成功（httpx 是 lazy connect），所以「_outbox_core is not None」恒为 True，不反映 gateway 是否真的可达。

**Choice:** 在 CoreClient 上新增 `ping()` 方法：向 gateway 根路径 `/healthz` 发送带 2s 超时的 GET 请求（从 `base_url` 剥离 `/api/v1` 前缀，因 healthz 在 host 根），2xx 返回 True，传输错误/非 2xx 返回 False（不抛异常）。`/readyz` 调 `await _outbox_core.ping()` 作为 `data_plane` 探活结果。

**Rationale:**
1. 真实反映数据面可达性 — gateway 宕机时 `data_plane: False` → `/readyz` 返回 `degraded`，k8s 编排器可将 Pod 摘除流量。
2. GET `/healthz` 是 gateway 已有端点（`main.go` `h.GET("/healthz")`），无需新增。
3. 2s 超时 + try/except 兜底确保探活自身不会拖垮 `/readyz` 响应。
4. 端到端验证：三服务本地 + 服务器组件时 `data_plane: true`，gateway 关闭时 `data_plane: false`。

### DD-2: `_CoreClientFactory` 按 tenant_id 缓存 CoreClient，httpx 连接池跨 RPC 复用

**Ambiguity:** SPEC §4.3 要求 outbox dispatcher 注入数据面 client（`role="service"`），但未规定 gRPC servicer 侧的 CoreClient 生命周期。原 `_default_core_client` 每次 RPC 创建+销毁 CoreClient（含 httpx.AsyncClient），连接池无法跨 RPC 复用。

**Choice:** 新增 `_CoreClientFactory`：按 `tenant_id` 缓存 `CoreClient`（各自拥有独立 httpx 连接池 + X-Tenant-Id 头，保留 RLS 租户隔离）。缓存的 client `_owns_client` 置 False，使 servicer 的 `async with factory(t)` 退出时不关闭缓存 client；只在进程关闭时通过 `factory.aclose()` 统一关闭。

**Rationale:**
1. 性能 — httpx 连接池复用避免每个请求 TCP+TLS 握手，对高频 gRPC→REST 转发显著降低延迟。
2. 租户隔离 — 每个 tenant_id 独立 CoreClient + X-Tenant-Id header，不破坏 RLS。
3. 生命周期清晰 — 缓存 client 由 factory 统一管理，servicer 的 `async with` 不关闭，测试路径仍走 `_default_core_client`（每 RPC 独立 client，向后兼容）。

### DD-3: dispatcher 始终启动，NATS 启动不可用时懒重连自愈

**Ambiguity:** SPEC §7.3 要求「延迟派发而非丢失工作」，但 dispatcher 在 NATS 启动不可用时应立即失败还是自愈重连未明确。

**Choice:** dispatcher 新增 `nats_connect` 可选参数：NATS 为 None 时 dispatcher 仍启动，`_dispatch_once` 内懒重连；publish 失败则置 None 下轮重连。main.py 始终启动 dispatcher，传入 `nats_connect=_nats_connect`（复用 `_build_nats_client`）。

**Rationale:**
1. NATS 启动不可用时服务仍就绪（`/readyz` `outbox_dispatcher: true`），事件在 outbox_events 表中累积，NATS 恢复后自动派发。
2. 日志记为 `pending-reconnect` 而非永久 degraded。
3. 端到端验证：NATS 启动不可用时 dispatcher 正常运行，NATS 恢复后事件成功派发。

### DD-4: dispatcher backoff 对 NATS 连接失败生效（raise 而非 return None）

**Ambiguity:** `_build_nats_client` 在连接失败时 `return None`（吞异常 + WARNING 日志）。`_dispatch_once` 原设计将 `nats_connect() → None` 视为正常返回 0，导致 `_run_loop` 重置 `_consecutive_failures=0` 并 sleep 1s，backoff 与日志抑制机制完全失效。

**Choice:** `_dispatch_once` 在 `nats_connect()` 返回 None 时 `raise RuntimeError("NATS unavailable; nats_connect returned None")`，使 `_run_loop` 将其视为失败，递增 `_consecutive_failures` 并应用 backoff（1s→2s→4s...→30s 封顶）+ 日志抑制。

**Rationale:**
1. 违背 dispatcher docstring「持久 DB/NATS 故障不每秒刷日志」的设计目标 — 原行为每秒 1 条 WARNING 无限输出。
2. backoff 降频后 `_build_nats_client` 的 WARNING 也按 backoff 间隔触发（1s, 2s, 4s... 30s 封顶），而非每秒。
3. 测试 `test_loop_survives_transient_nats_error` 验证韧性，单测 126 全绿。

---

## 2. Deviations

### DEV-1: `ping()` URL 构造用 `netloc.decode('ascii')` 而非 `host` + 手动端口拼接

**Spec:** SPEC §4.3 要求 `/readyz` 探活数据面可达性，未规定 URL 构造细节。

**Implementation:** 用 `base.netloc.decode('ascii')`（保留 IPv6 方括号如 `[::1]:8080`）替代 `f"{base.host}:{base.port}"`（`URL.host` 返回去括号 IPv6 如 `::1`，会生成歧义 URL `http://::1:8080/healthz`）。`netloc` 在 httpx 中返回 `bytes`，故 `.decode('ascii')`。

**Reason:** IPv6 环境下 `URL.host` 去括号会导致 ping URL 歧义解析失败。当前默认配置是 DNS 主机名，但这是正确性修复。新增 `test_ping_handles_ipv6_base_url` 回归测试验证 `[::1]:8080` 保留方括号。

### DEV-2: `OutboxDispatcher.nats_client` 公共 property 替代 main.py 读私有属性

**Spec:** SPEC 未规定 NATS 所有权协调的接口。

**Implementation:** 新增 `nats_client` 公共 property 暴露当前 NATS client（可能是启动时的或重连后的）。main.py shutdown 路径改用 `_outbox_dispatcher.nats_client` 替代 `_outbox_dispatcher._nats`。

**Reason:** 消除 main.py 对 dispatcher 内部表示的耦合。如果 dispatcher 重命名 `_nats` 或改变重连簿记，main.py 不会静默破坏。单点访问、逻辑正确，property 是最小侵入的解耦方式。

### DEV-3: 移除 `_core_client_factory` 的前向引用类型注解

**Spec:** N/A — 实现期发现。

**Implementation:** 模块全局 `_core_client_factory = None`（移除 `_CoreClientFactory | None` 类型注解），因为 `_CoreClientFactory` 类定义在下方，无 `from __future__ import annotations` 时前向引用注解会触发 `NameError`。

**Reason:** 启动期 `NameError: name '_CoreClientFactory' is not defined` 导致 kb-service 无法启动。移除注解是最小修复（Python 动态类型，注解非必需）。端到端测试时发现并修复。

---

## 3. Tradeoffs

### TR-1: CoreClient 缓存 vs 每次 RPC 创建

**Alternatives:**
1. **缓存（chosen）** — `_CoreClientFactory` 按 tenant_id 缓存，httpx 连接池跨 RPC 复用。
2. **每次创建** — `_default_core_client` 每次 RPC 新建+销毁 CoreClient。

**Pros/Cons:**
- 缓存：性能优（连接池复用），但需管理生命周期（aclose）+ `_owns_client=False` 防止 per-RPC 关闭。
- 每次创建：简单，无需生命周期管理，但高频场景 TCP 握手开销大。

**Why chosen:** gRPC servicer 对每个 RPC 都要调 gateway Core API，高频场景下连接池复用性能优势显著。缓存按 tenant_id 隔离不破坏 RLS。测试路径仍走 `_default_core_client` 向后兼容。

### TR-2: ping() 用 GET /healthz vs 其他探活方式

**Alternatives:**
1. **GET /healthz（chosen）** — gateway 根路径健康端点，2s 超时。
2. **POST /data/query SELECT 1** — 数据面真实查询，但更重（需序列化 SQL + DB 往返）。
3. **TCP connect 探活** — 仅验证端口可达，不验证 gateway 进程健康。

**Pros/Cons:**
- GET /healthz：轻量，gateway 已有端点，验证进程级健康（非仅端口）。
- POST /data/query：最真实但过重，探活不应触发 DB 查询。
- TCP connect：最轻但不验证进程健康（端口开但 gateway 可能 hang）。

**Why chosen:** GET /healthz 是正确粒度 — 验证 gateway 进程响应 HTTP 请求（而非仅端口监听），且不触发 DB 查询。2s 超时 + try/except 兜底确保自身不拖垮 `/readyz`。

### TR-3: dispatcher NATS 失败用 raise vs return 0

**Alternatives:**
1. **raise RuntimeError（chosen）** — 让 `_run_loop` backoff 生效。
2. **return 0** — 原行为，正常返回，backoff 失效，每秒刷 WARNING。
3. **让 `_build_nats_client` 重抛** — 改变其「启动 best-effort」语义。

**Pros/Cons:**
- raise：backoff + 日志抑制生效，符合 dispatcher 韧性设计目标。
- return 0：简单但违背「持久故障不每秒刷日志」的设计目标。
- 重抛：会改变 `_build_nats_client` 的「启动 best-effort return None」语义，影响 main.py 启动路径。

**Why chosen:** raise 是最小改动且正确 — `_build_nats_client` 保持「启动 best-effort return None」语义不变，dispatcher 自己决定如何处理 None 返回。backoff 降频后 `_build_nats_client` 的 WARNING 也按 backoff 间隔触发。

---

## 4. Open Questions

### OQ-1: MinIO bucket 配置导致 document upload 端到端失败

**Assumption:** 端到端测试中 `get upload URL` 失败（503 `bucket not found`），但 `ani-kb-docs` bucket 在服务器 MinIO 上确实存在。疑为 gateway 的 `OBJECT_STORE_BUCKET_PREFIX=ani-` 与 bucket 名拼接逻辑不匹配。

**Verification needed:** 检查 gateway 的 `storage_runtime.go` bucket 名解析逻辑（`OBJECT_STORE_BUCKET_PREFIX` + `kb-docs` = `ani-kb-docs`？还是 `ani-kb-docs` 被前缀为 `ani-ani-kb-docs`？）。此问题属于 gateway 对象存储 runtime 配置，**不是 issue-031 的回归**（issue-031 不涉及对象存储）。

### OQ-2: rag-engine e2e 测试脚本 `fixture 'client' not found`

**Assumption:** `test_e2e_issue030.py` 的 9 个错误（`fixture 'client' not found`）是 pre-existing 问题 — 该 e2e 脚本需要运行的真实 gateway+PG，且使用了文件中未定义的 pytest fixture。

**Verification needed:** 确认该 e2e 脚本是否应迁移为独立运行脚本（如 `e2e_issue029_full_stack.py` 的 `if __name__ == "__main__"` 模式），而非 pytest 收集模式。不属于 issue-031 范围。

---

## 5. Verification Commands Run

| 命令 | 结果 |
|------|------|
| `python -m pytest tests/ -q --ignore=tests/test_e2e_issue030.py --ignore=tests/e2e_issue029_data_plane.py --ignore=tests/e2e_issue029_full_stack.py` | **126 passed** |
| `Select-String -Path services\kb-service\app\**\*.py -Pattern "import asyncpg\|from asyncpg"` | **0 匹配** |
| `Test-Path services\kb-service\app\repositories\rls.py` | **False**（已删除） |
| `Test-Path services\kb-service\migrations` | **False**（已删除） |
| `Select-String -Path requirements.txt -Pattern "^asyncpg"` | **0 匹配**（仅注释行提到 asyncpg removed） |
| `go build -o bin\ani-gateway.exe .`（services/ani-gateway） | **成功** |
| `python tests\e2e_issue029_full_stack.py`（三服务本地 + 服务器组件） | **9/10 passed**（唯一失败为 MinIO bucket 配置，pre-existing） |
| 诊断（所有改动文件） | **无报错** |
