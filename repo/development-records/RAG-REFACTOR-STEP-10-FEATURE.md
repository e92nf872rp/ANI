# RAG-REFACTOR-STEP-10-FEATURE — SSE 切换 kb-service.Retrieve + Gateway 改线 (步骤 10 功能)

- **Issue:** issue-038-feature-sse-switch-gateway
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-28
- **Product line:** core (Services / kb-service + ani-gateway)
- **Type:** feature (功能实现)
- **Dependencies:** #026 (kb-service Retrieve 契约), #036 (E2E 测试)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/app/services/query_orchestrator.py` | 新增 `query_stream()` 方法 + 4 个 StreamEvent dataclass (`StreamTokenEvent`/`StreamSourcesEvent`/`StreamDoneEvent`/`StreamNoResultEvent`) |
| `services/kb-service/app/services/contracts.py` | `QueryOrchestratorProtocol` 新增 `query_stream` 方法声明 |
| `services/kb-service/app/api/grpc_server.py` | `_retrieve_stream` 委托 `orch.query_stream()`；`_retrieve_stream_sync` 增加 S1/S2 修复（GeneratorExit 取消 + ev_q 超时）；`idempotency_key` 必填校验；`message_repo` 提升模块级 import；Query 复用 `_persist_assistant` |
| `services/ani-gateway/internal/router/kb_sse.go` | 新路径 `kbClient.Retrieve` gRPC stream → SSE 转码；`errors.Is(recvErr, io.EOF)`；`defer stream.CloseSend()`；移除冗余 `TenantId`/`KbId` |
| `services/ani-gateway/internal/router/kb_sse_test.go` | `fakeRetrieveStream.RecvMsg` 返回 `io.EOF`；`fakeKBRetrieveClient.Retrieve` 签名适配 + 设置 `TenantId`/`KbId` |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 新增 `Retrieve` 方法 + `retrieveStreamWrapper`（确保流关闭时取消 context） |

## 1. Design Decisions

### 1.1 将三道闸门逻辑提取为 `QueryOrchestrator.query_stream()` 公开方法

- **Ambiguity:** Plan §10.2 要求 `_retrieve_stream` 的编排逻辑（retrieve → 三道闸门 → GenerateStream → token events → done event）与 Query RPC 一致，但未指定代码组织方式。可直接在 `grpc_server.py` 的 `_retrieve_stream` 内联编排，也可提取到 orchestrator。
- **Choice:** 新增 `QueryOrchestrator.query_stream()` 公开方法，返回 `AsyncIterator[Any]`，通过 4 个 StreamEvent dataclass 传递事件。`_retrieve_stream` 仅做 proto 映射（`isinstance` 判断事件类型 → 构造 `RetrieveEvent`）。
- **Rationale:** 直接内联会导致 `_retrieve_stream` 重复 `query()` 中约 100 行闸门逻辑，违反 DRY 且后续维护时两处易不一致。提取到 orchestrator 使闸门逻辑有单一来源，`query()` 和 `query_stream()` 共享一致的编排行为。StreamEvent dataclass 作为 orchestrator 与 grpc_server 之间的松耦合事件契约，比直接 yield proto 类型更可测试。

### 1.2 StreamEvent 使用 dataclass 而非 proto 类型

- **Ambiguity:** `query_stream()` 的事件返回类型可选择 proto 生成的 `RetrieveEvent` 或自定义 dataclass。
- **Choice:** 使用 4 个自定义 dataclass（`StreamTokenEvent`/`StreamSourcesEvent`/`StreamDoneEvent`/`StreamNoResultEvent`），在 `grpc_server.py` 中通过 `isinstance` 映射为 proto `RetrieveEvent`。
- **Rationale:** orchestrator 不应依赖 proto 生成代码（分层约束：Services 编排层不依赖传输层类型）。dataclass 是纯 Python 类型，可独立测试，且 `isinstance` 检查比 proto oneof 字段判断更清晰。`StreamNoResultEvent` 额外携带 `answer` 字段（无结果时 LLM 已生成的实际 token），使 grpc_server 能正确输出 token event 而非空 answer。

### 1.3 `_retrieve_stream_sync` 使用 `queue.Queue` + `run_coroutine_threadsafe` 桥接

- **Ambiguity:** gRPC Python 的 server-streaming RPC handler 可以是 sync generator 或 async generator。kb-service 使用 `ThreadPoolExecutor` 运行 gRPC server，但 orchestrator 的 `query_stream()` 是 async generator。
- **Choice:** 在 `_retrieve_stream_sync` 中通过 `queue.Queue` + `asyncio.run_coroutine_threadsafe` 将 async 生成器桥接为 sync 生成器。async 代码在专用 `_grpc_loop` 事件循环上执行，事件通过线程安全 queue 传递到 sync 线程。
- **Rationale:** 这是 gRPC Python 在 ThreadPoolExecutor 模式下支持 async handler 的标准模式。直接使用 `asyncio.run()` 在每次调用时创建新事件循环会导致 asyncpg 连接池失效（连接池绑定到创建它的事件循环）。

### 1.4 S1/S2 修复：GeneratorExit 取消 + ev_q 超时

- **Ambiguity:** async→sync 桥接模式中，客户端断连和事件循环异常是两个资源泄漏场景，Plan 未提及（属于实现层细节）。
- **Choice:**
  - **S1:** 捕获 `GeneratorExit`（客户端断连时 Python 生成器抛出），在 `except` 块调用 `future.cancel()` 取消 `_runner` 协程。
  - **S2:** `ev_q.get(timeout=130.0)` 替代无超时 `ev_q.get()`，超时后取消 future 并 `context.abort(DEADLINE_EXCEEDED)`。
- **Rationale:** S1 防止客户端断连后 LLM 生成和 DB 操作继续运行（协程泄漏 + queue 无限增长 + DB 连接占用）。S2 防止事件循环异常停止时 sync 线程永久挂起（ThreadPoolExecutor 线程耗尽）。130s = gateway 120s 超时 + 10s grace margin。

## 2. Deviations

### 2.1 `_retrieve_stream` 未产出 `RetrieveEvent.error` oneof 分支

- **Spec said:** Plan §10.1 proto 定义了 `RetrieveErrorEvent error = 4` oneof 分支，issue-026 契约阶段新增了 `code` 字段以支持结构化错误传递。
- **Implemented:** `_retrieve_stream` 从不产出 `RetrieveEvent(error=...)`。流式传输开始后的中途错误通过 `context.abort()` 终止，Go 侧收到非 EOF 错误后走 `STREAM_INTERRUPTED` 路径。
- **Why:** 流式 RPC 中结构化错误需要中途 yield error event 然后关闭流，实现复杂度高。当前方案（异常→abort→Go 侧兜底 `STREAM_INTERRUPTED`）功能上可工作，客户端能感知到流中断。Go 侧 `kb_sse.go` 保留了 error oneof 处理分支作为防御性代码。建议后续 issue 统一实现结构化中途错误。

### 2.2 `idempotency_key` 仅校验必填，未实现幂等回放

- **Spec said:** Plan §0.1 等价性矩阵未明确要求 Retrieve 实现幂等回放，但 `idempotency_key` 是 `RetrieveRequest` 的字段之一，且 `CreateKB`/`NotifyDocumentUploaded` 真正实现了回放。
- **Implemented:** 仅校验 `idempotency_key` 非空（`context.abort(INVALID_ARGUMENT)`），未做回放/去重。
- **Why:** 流式 RPC 的幂等回放需要缓存整个事件流（token events 可能数百个），内存和复杂度成本高。当前必填校验是渐进式实现的第一步，确保 Gateway 每次请求携带唯一 key，为后续回放实现预留接口。属于超出 issue-038 范围的后续工作。

### 2.3 Go 侧移除 `RetrieveRequest` 构造中的 `TenantId`/`KbId`

- **Spec said:** Plan §10.3 示例代码中 `kbClient.Retrieve(ctx, req)` 传入完整 req。
- **Implemented:** 从 `RetrieveRequest` 构造中移除 `TenantId` 和 `KbId`，由 `KBClient.Retrieve` 方法内设置（与真实 `kbGRPCClient.Retrieve` 行为一致）。
- **Why:** `kbGRPCClient.Retrieve` 的方法签名包含 `tenantID` 和 `kbID` 参数，在方法内设置 `req.TenantId` 和 `req.KbId`。如果 handler 也设置这两个字段，会导致双重赋值。测试中 `fakeKBRetrieveClient.Retrieve` 也镜像了此行为。

## 3. Tradeoffs

### 3.1 `query_stream()` 内联闸门逻辑 vs 复用 `query()` 内部方法

- **Alternative A:** 新增 `query_stream()` 方法，内联三道闸门逻辑（与 `query()` 有部分重复但返回不同类型）— 当前选择
- **Alternative B:** 提取共享闸门逻辑为内部方法，`query()` 和 `query_stream()` 都调用
- **Choice:** Alternative A
- **Pros/Cons:** B 理论上更 DRY，但 `query()` 返回 `QueryResult`（一次性），`query_stream()` 返回 `AsyncIterator`（流式），两者的闸门处理方式不同（`query()` 返回 `NO_RESULT_ANSWER` 字符串，`query_stream()` yield `StreamNoResultEvent` + `StreamSourcesEvent([])` + `StreamDoneEvent`）。强行抽象会增加间接调用层且难以统一返回类型。A 有约 30 行逻辑相似但类型不同，可接受。

### 3.2 S1 修复：`GeneratorExit` 捕获 vs `context.add_callback` 取消

- **Alternative A:** 捕获 `GeneratorExit` 在 sync 生成器中取消 future — 当前选择
- **Alternative B:** 使用 gRPC `context.add_callback()` 注册回调，在 context 取消时取消 future
- **Choice:** Alternative A
- **Pros/Cons:** B 是 gRPC 原生机制，但 `context.add_callback` 在 Python gRPC 中行为不稳定（回调在 gRPC 内部线程执行，与 sync 生成器的交互不明确）。A 利用 Python 生成器标准语义（`GeneratorExit` 在生成器被 GC 时抛出），行为可预测。两者都达到取消 `_runner` 的效果。

### 3.3 Go SSE 假流式不在本 issue 修复

- **Alternative A:** 在 issue-038 中修复 `writeSSEEvent` 的假流式问题（`c.Write` → HijackWriter + Flush）— 拒绝
- **Alternative B:** 保持现状，后续单独 issue 修复 — 当前选择
- **Choice:** Alternative B
- **Pros/Cons:** A 超出 issue-038 范围（issue-038 是改线，不是框架级修复）。假流式是 pre-existing 问题，影响 legacy 和 new 两条路径，修复需要 Hertz 框架级改造。在 issue-038 中修复会扩大 scope 并引入风险。B 将框架级问题留给专门 issue 处理。

## 4. Open Questions

### 4.1 Go SSE 假流式何时修复

- **Question:** `writeSSEEvent` 用 `c.Write`（Hertz `Response.AppendBody`）只追加内存缓冲，从不刷到网络。注释声称 "chunked transfer-encoding hijack writer and c.Flush" 但代码中无此机制。全仓搜索确认无 `Flush`/`Hijack`/`SetBodyStreamWriter` 调用。影响 legacy 和 new 两条路径 — SSE 事件在 handler 返回后才一次性发送，非真正流式。
- **Impact:** 用户体验：客户端收到完整 SSE 响应而非逐 token 流式显示。功能上可工作但失去流式的 UX 价值。
- **Suggestion:** 单独开 issue，标题 "Fix SSE pseudo-streaming — implement Hertz HijackWriter + Flush in writeSSEEvent"。需要研究 Hertz 框架的流式响应 API。

### 4.2 `RetrieveEvent.error` oneof 分支是否需要实现

- **Question:** proto 声明了 `RetrieveErrorEvent`（含 `message` + `code`），Go 侧有处理分支，但 Python 侧从不产出。流式传输中途的错误以 `context.abort` 终止。
- **Impact:** 客户端收到 gRPC status code（如 UNAVAILABLE）而非结构化 `{"code":"INFERENCE_UNAVAILABLE","message":"..."}`。Go 侧映射为 `STREAM_INTERRUPTED` 而非精确的 error code。
- **Suggestion:** 评估是否值得实现结构化中途错误。如果需要，Python 侧在 `_retrieve_stream` 的 `except` 块中 yield `RetrieveEvent(error=RetrieveErrorEvent(message=..., code=...))` 然后关闭流，Go 侧已有处理分支。

### 4.3 `idempotency_key` 幂等回放是否需要实现

- **Question:** 当前仅校验必填，未做回放。流式 RPC 的幂等回放需要缓存整个事件流。
- **Impact:** 客户端重试相同 `idempotency_key` 会产生重复的 LLM 生成和 DB 写入。
- **Suggestion:** 评估后续是否需要实现。如果需要，可考虑 Redis 缓存事件流（TTL = session TTL），重放时从 Redis 读取。

### 4.4 120s 超时与 legacy 路径不对称

- **Question:** Go 侧 `queryRPCTimeout=120s` 为新路径的整体 deadline，legacy vLLM 流式无整体超时。长答案（如代码生成）可能超过 120s。
- **Impact:** 新路径可能截断长答案，legacy 路径不会。
- **Suggestion:** 配置调优。可调大 `queryRPCTimeout` 或改为 per-token 超时（每个 token 间隔不超过 N 秒）。属于运维层面，非代码 bug。

## 5. Verification commands run

| Command | Result |
|---------|--------|
| `go test ./services/ani-gateway/internal/router/ -run TestSSE -v -count=1` | ✅ 全部 PASS |
| `python -m pytest tests/test_grpc_server.py -v -q` | ✅ 21/21 PASS |
| `python -m pytest tests/test_query_orchestrator.py tests/test_rag_grpc_client.py -q` | ✅ 34/34 PASS |
| `git diff --check` | ✅ exit 0 |

## 6. Review findings

### 已修复（2 项）

| # | 严重度 | 问题 | 修复 |
|---|--------|------|------|
| S1 | 严重 | 客户端断连导致 `_runner` 协程泄漏 | `GeneratorExit` 捕获 + `future.cancel()` |
| S2 | 严重 | `ev_q.get()` 无超时永久挂起 | 130s 超时 + `queue.Empty` 处理 + `future.cancel()` |

### 拒绝修复（4 项，超出 issue-038 范围）

| # | 严重度 | 问题 | 拒绝原因 |
|---|--------|------|----------|
| L1 | 低 | Go SSE 假流式（`c.Write` 不刷网络） | pre-existing，应单独 issue 修复 |
| M1 | 中 | `RetrieveEvent.error` oneof 死代码 | 功能可工作，后续 issue 统一 |
| M2 | 中 | `idempotency_key` 仅校验未回放 | 超出 scope，后续 issue |
| M3 | 中 | 120s 超时与 legacy 不对称 | 配置调优，非代码 bug |
