# RAG-REFACTOR-STEP-10-CONTRACT — kb-service 新增 Retrieve RPC proto 定义 (步骤 10 契约)

- **Issue:** issue-026-contract-kb-service-retrieve-proto
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-17
- **Product line:** core (Services / kb-service)
- **Type:** contract (仅 proto 契约定义 + 生成 stub, 不含 RPC 实现代码)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `api/proto/kb/v1/kb_service.proto` | 新增 `Retrieve` server-streaming RPC 定义 + `RetrieveRequest`/`RetrieveEvent`(oneof) + 4 个子事件 message (`RetrieveTokenEvent`/`RetrieveSourcesEvent`/`RetrieveDoneEvent`/`RetrieveErrorEvent`) |
| `pkg/generated/pb/kb/v1/kb_service.pb.go` | `buf generate` 重新生成 (Go 序列化 stub) |
| `pkg/generated/pb/v1/kb_service_grpc.pb.go` | `buf generate` 重新生成 (Go gRPC stub); Retrieve 为 `ServerStreams: true` |
| `services/kb-service/app/generated/kb/v1/kb_service_pb2.py` | `grpc_tools.protoc` 重新生成 (Python 序列化 stub) |
| `services/kb-service/app/generated/kb/v1/kb_service_pb2.pyi` | `grpc_tools.protoc` 重新生成 (Python 类型 stub) |
| `services/kb-service/app/generated/kb/v1/kb_service_pb2_grpc.py` | `grpc_tools.protoc` 重新生成 (Python gRPC stub); Retrieve 为 `unary_stream_rpc_method_handler` |

## 1. Design Decisions

### 1.1 `RetrieveTokenEvent` 使用 `content` 字段而非 SSE 的 `delta`

- **Ambiguity:** SPEC §4.3 定义 SSE token 事件 data 为 `{"delta":"<增量文本>"}`，而 proto `RetrieveTokenEvent` 需要定义字段名。Plan §10.1 未明确字段名，issue AC 仅说"`content` 字段"。
- **Choice:** proto 使用 `string content = 1`，与 `rag.proto` 的 `GenerateToken.content` 保持一致，而非直接用 SSE 的 `delta`。
- **Rationale:** gRPC 是内部传输协议，SSE 是外部 API 协议，两者字段名无需一致。`rag.proto` 的 `GenerateToken` 已建立 gRPC 内部用 `content` 的约定，`RetrieveTokenEvent` 遵循此约定保持 gRPC 层命名一致性。Gateway 改线（issue-038）时做 `content → delta` 的一行转码映射，不影响 SSE 输出格式（SPEC §4.3 不变）。Plan §0.1 等价性约束的是客户端可见的 SSE 事件序列（token*→sources→done），不约束 gRPC 内部字段名。

### 1.2 `RetrieveRequest` 完整镜像 `QueryRequest` 的 9 个字段

- **Ambiguity:** issue AC 要求"`RetrieveRequest` message 定义（字段同 `QueryRequest`）"，但未明确是否包含所有字段还是仅检索相关字段。Plan §10.1 未列出具体字段。
- **Choice:** 完整镜像 `QueryRequest` 的全部 9 个字段：`tenant_id`、`kb_id`、`question`、`session_id`、`idempotency_key`、`top_k`、`score_threshold`、`inference_service_name`、`retrieval_mode`，字段号和类型完全一致。
- **Rationale:** Retrieve RPC 的编排与 Query RPC 一致（Plan §10.2：session 管理 + 无结果三道闸门 + GenerateStream），需要相同的输入参数。完整镜像避免编排层因缺字段而降级，且符合 issue AC "字段同 QueryRequest" 的字面要求。

### 1.3 `RetrieveErrorEvent` 新增 `code` 字段（超 issue AC 范围的契约补强）

- **Ambiguity:** issue AC 第 22 行仅要求 `RetrieveErrorEvent`（`message` 字段），未提及 `code` 字段。但 SPEC §4.3 定义 SSE error 事件为 `{"code":"...","message":"..."}`，有 6 种 code（BAD_REQUEST/UNAUTHORIZED/KB_NOT_FOUND/INFERENCE_UNAVAILABLE/RETRIEVE_FAILED/STREAM_INTERRUPTED）。旧路径 Gateway 自己编排检索+生成，能根据失败阶段设 code；新路径编排移入 kb-service，Gateway 仅收 gRPC stream，无法区分失败阶段。
- **Choice:** 在契约阶段（本 issue）主动新增 `string code = 2` 字段，超出 issue AC 的最小要求。
- **Rationale:** issue-038（Gateway 改线）的 Scope 不允许修改 proto（仅 `grpc_server.py` + `kb_sse.go` + `kb_grpc_client.go`），它依赖 issue-026 定义的契约。若无 `code` 字段，issue-038 只能用 gRPC status code 有损映射（UNAVAILABLE 无法区分检索失败 vs 推理不可用）或硬编码通用 code，违反 SPEC §4.3 和 Plan §0.1（功能效果不变）。契约阶段修改成本最低（+1 字段 + 重新生成），且向后兼容（`code` 默认空串）。两轮独立子代理交叉验证均确认此为 major 级契约缺陷，应在契约阶段修复。

## 2. Deviations

### 2.1 `RetrieveErrorEvent` 超出 issue AC 定义新增 `code` 字段

- **Spec said:** issue AC 第 22 行明确要求 `RetrieveErrorEvent error = 4`（`message` 字段），仅列了 `message` 一个字段。
- **Implemented:** 在 `RetrieveErrorEvent` 中新增 `string message = 1` + `string code = 2`，超出 AC 的最小字段集。
- **Why:** 代码审查发现 `code` 字段缺失会导致 issue-038 Gateway 改线时无法忠实复现 SSE error 事件的 `{"code":"...","message":"..."}` 格式。这是 SPEC §4.3 的外部 API 契约要求，且 Plan §0.1 明确要求"功能效果不变 — 改造前后用户可感知的 SSE 行为完全一致"。在契约阶段补强比在功能阶段 workaround 更合理，修改成本更低。

### 2.2 `make gen-proto` 通过直接调用 `buf` 完成

- **Spec said:** issue AC 要求"运行 `make gen-proto` 重新生成 Go/Python stub"。
- **Implemented:** Go stub 通过直接调用 `E:\GoPath\bin\buf.exe generate --template buf.gen.yaml .` 生成；Python stub 通过 `python -m grpc_tools.protoc` 生成。`make gen-proto` 在沙箱环境中执行退出码 0 但未实际生成文件（buf 路径解析问题）。
- **Why:** `make gen-proto` 的 Makefile 目标使用 `PATH=$(TOOLS_BIN):$$PATH` 查找 buf，但环境中 buf 安装在 `E:\GoPath\bin` 而非 `tools/bin`。直接调用 buf 和 grpc_tools.protoc 生成结果与 `make gen-proto` 等价，最终生成物已验证正确（Go `Code` 字段 + Python `code: str` 均存在）。

## 3. Tradeoffs

### 3.1 `RetrieveErrorEvent.code` 在契约阶段新增 vs 留待功能 issue 处理

- **Alternative A:** 在契约阶段（issue-026）主动新增 `string code = 2`（当前选择）
- **Alternative B:** 严格遵循 issue AC 仅定义 `message`，在功能 issue（issue-038）中用 gRPC status code 有损映射或硬编码通用 code
- **Choice:** Alternative A
- **Pros/Cons:** B 严格遵循 AC 范围，但 issue-038 的 Scope 不允许修改 proto，只能用有损 workaround，且后续补字段需契约变更 + 重新生成。A 超出 AC 范围但修改成本极低（+1 字段），向后兼容（默认空串），且避免了 issue-038 的设计债务。经两轮独立子代理交叉验证，一致认为 A 是正确选择。

### 3.2 `RetrieveTokenEvent.content` vs `delta` 命名

- **Alternative A:** proto 用 `content`（与 `rag.proto` `GenerateToken` 一致），Gateway 转码为 SSE `delta`（当前选择）
- **Alternative B:** proto 直接用 `delta`，与 SSE 字段名一致，Gateway 无需转码
- **Choice:** Alternative A
- **Pros/Cons:** B 看似简化 Gateway 转码，但破坏了 gRPC 内部命名约定（`rag.proto` `GenerateToken.content`）。gRPC 是内部协议，SSE 是外部协议，两者字段名分层是正常设计。Gateway 转码 `content→delta` 是一行映射，不引入 bug。A 保持了 gRPC 层命名一致性。

### 3.3 `kb_service.proto` 的 `SourceChunk` 不对齐 `rag.proto` 的 `chunk_id`

- **Alternative A:** 保持 `kb_service.proto` 的 `SourceChunk` 为 5 字段（无 `chunk_id`），内部编排用 Python dict 传递（当前选择）
- **Alternative B:** 在 `kb_service.proto` 的 `SourceChunk` 追加 `string chunk_id = 6`，与 `rag.proto` 对齐
- **Choice:** Alternative A（不在本 issue 修改）
- **Pros/Cons:** B 使跨 proto 字段一致，但本 issue AC 未要求，且 kb-service 内部编排用 Python `list[dict]` 传递 sources（含 chunk_id），proto 的 SourceChunk 只在 gRPC 边界转换。chunk_id 是 RRF 去重 key，在 `RetrieveService` 内部完成去重后即完成使命，外部调用方（前端）不需要 chunk_id。现有 Query RPC 的 `grpc_server.py` 构建SourceChunk 时也已丢弃 chunk_id（既有行为）。A 遵循最小变更原则，不扩大 issue 范围。

## 4. Open Questions

### 4.1 `kb_service.proto` 的 `SourceChunk` 是否需要在后续 issue 中追加 `chunk_id`

- **Question:** issue-026 保持 `kb_service.proto` 的 `SourceChunk` 为 5 字段（无 `chunk_id`），与 `rag.proto` 的 6 字段版本不一致。issue-025 开发记录 OQ4.1 已显式记录此为开放问题。
- **Impact:** kb-service 内部编排用 Python dict 传递 sources（含 chunk_id），gRPC 边界丢弃 chunk_id 是现有 Query RPC 的既有行为。如果后续 issue-034 的等价性测试需要在 gRPC 响应边界验证 chunk_id 对齐，则需要新增契约 issue 追加该字段。
- **Current status:** 后续 issue 027-039 的 Scope 均不包含 `api/proto/kb/v1/kb_service.proto`，无 issue 计划解决此问题。issue-034 的 `test_source_chunk_has_chunk_id` 在 rag-engine tests 目录下，验证的是 `rag.proto` 的 SourceChunk（已有 chunk_id），不是 `kb_service.proto` 的。目前设计接受 chunk_id 在 kb-service gRPC 边界丢弃。

### 4.2 `RetrieveErrorEvent.code` 的枚举值是否需要正式定义为 proto enum

- **Question:** 当前 `code` 是 `string` 类型，注释列出了 6 种值（BAD_REQUEST/UNAUTHORIZED/KB_NOT_FOUND/INFERENCE_UNAVAILABLE/RETRIEVE_FAILED/STREAM_INTERRUPTED）。是否应改为 proto enum 以获得编译期类型安全？
- **Impact:** string 类型更灵活（可向前兼容新增 code），但无编译期校验。enum 类型安全但新增 code 需改 proto。SPEC §4.3 的 code 值与 SSE 协议绑定，未来可能扩展。
- **Suggestion:** 保持 string 类型。SSE error code 本质是字符串协议，string 类型与 SSE JSON 格式天然对齐（`{"code":"RETRIEVE_FAILED"}`），且向后兼容性更好。issue-038 实现时 Gateway 直接透传 string 即可。

## 5. Verification commands run

| Command | Result |
|---------|--------|
| `buf generate --template buf.gen.yaml .` (Go stub) | ✅ exit 0, `Code` 字段 + `GetCode()` 方法已生成 |
| `python -m grpc_tools.protoc` (Python stub) | ✅ exit 0, `code: str` 字段已生成 |
| `go build ./pkg/generated/pb/kb/v1/... ./services/ani-gateway/...` | ✅ exit 0 |
| `go test ./services/ani-gateway/...` | ✅ ok (全部包通过) |
| `python -m compileall app/generated/` | ✅ COMPILE_EXIT=0 |
| `python -m pytest tests -q` (kb-service) | ✅ 81 passed |
| `python scripts/validate_component_imports.py --root .` | ✅ component import guard passed |
| `git diff --check` | ✅ exit 0 |
