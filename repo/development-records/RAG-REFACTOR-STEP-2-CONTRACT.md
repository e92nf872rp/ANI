# RAG-REFACTOR-STEP-2-CONTRACT — rag-engine 新增 Parse/Embed/Generate/GenerateStream RPC proto 定义 (步骤 2 契约)

- **Issue:** issue-025-contract-rag-engine-rpc-proto
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-14
- **Product line:** core (Services / rag-engine)
- **Type:** contract (仅 proto 契约定义 + 生成 stub, 不含 RPC 实现代码)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `ai/rag-engine/app/grpc/rag.proto` | 新增 Parse/Embed/Generate/GenerateStream 4 个 RPC 定义 + 9 个 message; `SourceChunk` 新增 `chunk_id` 字段并重新编号; 旧 `Query` RPC 标注 deprecated; 文件头注释更新 |
| `ai/rag-engine/app/grpc/rag_pb2.py` | `protoc` 重新生成 (序列化 stub) |
| `ai/rag-engine/app/grpc/rag_pb2.pyi` | `protoc` 重新生成 (类型 stub) |
| `ai/rag-engine/app/grpc/rag_pb2_grpc.py` | `protoc` 重新生成 (gRPC stub); 手动修正 import 为相对导入 `from . import rag_pb2` |

## 1. Design Decisions

### 1.1 `SourceChunk.chunk_id` 设为 field 1，旧字段偏移至 2-6

- **Ambiguity:** Plan §2.1 要求 `SourceChunk` 新增 `chunk_id` 字段，AC 要求"向后兼容，旧 Query RPC 不受影响"。但旧 `rag.proto` 注释明确声明 "Message shapes are intentionally identical to kb_service.proto ... same field numbers and types"，`kb_service.proto` 的 `SourceChunk` 使用 `doc_id=1, file_name=2, page=3, content=4, score=5`。
- **Choice:** 遵循 Plan §2.1，将 `chunk_id` 设为 field 1，旧字段偏移至 2-6。同时更新文件头注释，明确说明字段已重新编号，不再 wire-compatible with `kb_service.proto`。
- **Rationale:** 三个因素使此选择安全：(1) 旧 Query RPC 走 REST 而非 gRPC，protobuf 字段号变更不影响 REST JSON 路径；(2) 新 gRPC 客户端（issue-030 `RagEngineGRPCClient`）直接使用 `rag.proto` 生成的 stub，不跨 proto 序列化；(3) 搜索 issue-026~039 确认无后续 issue 依赖 `kb_service.proto` 与 `rag.proto` 的 SourceChunk wire compatibility。Plan §2.1 是权威设计来源。

### 1.2 `ParsedChunk` 字段顺序与 Plan §2.1 完全对齐

- **Ambiguity:** 初次实现时 `parent_chunk_id=6, chunk_type=7`，与 Plan §2.1 中的 `chunk_type=6, parent_chunk_id=10` 顺序不一致。proto3 按字段号匹配不按声明顺序，功能无影响，但可能误导后续实现 issue。
- **Choice:** 在 review 阶段修正字段号：`chunk_type=6, metadata_json=7, image_bytes=8, image_format=9, parent_chunk_id=10`，与 Plan §2.1 完全一致。
- **Rationale:** 契约 issue 的核心价值是作为后续功能 issue 的权威参考。字段号与 Plan 对齐避免实现 issue 开发者查阅 Plan 时产生混淆。

### 1.3 `EmbedResponse` 使用展平一维数组 `vectors_flat`

- **Ambiguity:** Plan §2.1 要求 EmbedResponse 返回向量数组。可选方案：`repeated repeated float`（嵌套）或 `repeated float vectors_flat`（展平）+ `dimension` + `count`。
- **Choice:** 使用展平一维数组 `repeated float vectors_flat` + `int32 dimension` + `int32 count`。
- **Rationale:** `repeated float` 使用 packed 编码，对大向量（如 bge-m3 的 1024 维 × N 文本）序列化效率高。嵌套 `repeated repeated float` 在 protobuf 中不支持 packed 编码，且反序列化复杂度高。展平数组 + dimension/count 是 protobuf 社区推荐的向量传输模式。

### 1.4 `rag_pb2_grpc.py` 相对导入修正

- **Ambiguity:** `protoc` 生成的 `rag_pb2_grpc.py` 使用绝对导入 `import rag_pb2`，但该文件是 `app.grpc` 包的一部分，绝对导入在包被 import 时会失败。旧版本手动修正为 `from . import rag_pb2`。
- **Choice:** 生成后手动将 `import rag_pb2 as rag__pb2` 替换为 `from . import rag_pb2 as rag__pb2`。
- **Rationale:** `protoc` 不感知 Python 包结构。旧代码已确立相对导入约定，保持一致避免 `ImportError`。此修正不影响 proto 契约本身。

## 2. Deviations

### 2.1 文件头注释从 "identical to kb_service.proto" 改为明确标注 wire-incompatible

- **Spec said:** 旧 `rag.proto` 头注释声明 "Message shapes are intentionally identical to kb_service.proto ... same field numbers and types"。
- **Implemented:** 头注释改为明确说明 SourceChunk 已重新编号，不再 wire-compatible with `kb_service.proto`，并解释为何安全（REST 路径 + 新客户端用 rag.proto）。
- **Why:** 旧注释与新字段号矛盾，保留会误导后续开发者。新注释如实记录设计决策和兼容性边界，是契约 issue 的必要文档。

### 2.2 未运行 `make gen-proto`

- **Spec said:** Issue AC 要求"运行 `make gen-proto` 重新生成 Python stub"。
- **Implemented:** 直接运行 `python -m grpc_tools.protoc -I app/grpc --python_out=app/grpc --grpc_python_out=app/grpc --pyi_out=app/grpc app/grpc/rag.proto`。
- **Why:** `make gen-proto` 目标针对 `api/proto/` 下 Go proto 代码生成，不覆盖 rag-engine 的 Python proto。rag-engine 没有专门的 Makefile 生成目标。直接调用 `grpc_tools.protoc` 是 rag-engine Python stub 的唯一生成方式，与 STEP-1 的 `RAG-REFACTOR-STEP-1-CONTRACT.md` 记录一致。

## 3. Tradeoffs

### 3.1 `chunk_id=1` 重新编号 vs `chunk_id=6` 追加

- **Alternative A:** `chunk_id=1`，旧字段偏移至 2-6（当前选择，遵循 Plan §2.1）
- **Alternative B:** `chunk_id=6` 追加到末尾，保持 `doc_id=1~score=5` 不变，与 `kb_service.proto` wire-compatible
- **Choice:** Alternative A
- **Pros/Cons:** B 更安全（wire-compatible），但偏离 Plan §2.1 的明确设计。A 破坏了旧 wire compatibility，但经分析确认无实际影响（REST 路径 + 新客户端用 rag.proto）。Plan 作为权威设计来源优先于旧注释中的兼容性声明。

### 3.2 proto 中保留 `bytes image_bytes` vs 仅存 download_url

- **Alternative A:** `ParsedChunk` 包含 `bytes image_bytes` + `string image_format`（当前选择）
- **Alternative B:** 图片不内联，仅在 metadata 中记录图片 URL，客户端按需下载
- **Choice:** Alternative A
- **Pros/Cons:** A 的风险是 gRPC 默认 4MB 限制，但 Parse RPC 传 `download_url` 而非整个文件，图片 chunk 作为子资源通常较小。B 增加一次额外网络往返。Plan §2.1 明确要求 `image_bytes` 和 `image_format` 字段，遵循 Plan。

## 4. Open Questions

### 4.1 `kb_service.proto` 的 `SourceChunk` 是否需要同步重新编号

- **Question:** issue-026 在 `kb_service.proto` 中新增 `RetrieveSourcesEvent`（含 `repeated SourceChunk sources`），但未说明 SourceChunk 的定义来源。`kb_service.proto` 现有 SourceChunk 使用 `doc_id=1~score=5`，与 `rag.proto` 新 SourceChunk（`chunk_id=1~score=6`）字段号不一致。
- **Impact:** 如果 kb-service 的 Retrieve RPC 返回 `kb_service.proto` 的 SourceChunk，再调用 rag-engine 的 Generate RPC 传入 `rag.proto` 的 SourceChunk，需要字段映射（而非直接传递）。
- **Suggestion:** issue-026 实现时需明确 `kb_service.proto` 的 SourceChunk 是否重新编号或追加 `chunk_id`。如果 kb-service 内部 Retrieve 返回的 SourceChunk 与传给 rag-engine Generate 的 SourceChunk 使用不同 proto，kb-service 需要做字段映射。

### 4.2 rag-engine Python proto 生成是否应纳入 Makefile

- **Question:** `make gen-proto` 不覆盖 rag-engine Python proto 生成。每次 proto 变更需手动运行 `grpc_tools.protoc`。
- **Impact:** 开发者可能忘记重新生成 stub，导致 proto 定义与 stub 不同步。
- **Suggestion:** 可在 `ai/rag-engine/` 下新增 `Makefile` 或 `scripts/gen_proto.sh`，或在根 Makefile 追加 `gen-rag-proto` 目标。不属本 issue 范围。

## 5. Verification Commands Run

| Command | Result |
|---------|--------|
| `python -m grpc_tools.protoc -I app/grpc --python_out=app/grpc --grpc_python_out=app/grpc --pyi_out=app/grpc app/grpc/rag.proto` | PASS (exit 0) |
| `python -m compileall -q ai/rag-engine/app/grpc/` | PASS |
| `python -m pytest ai/rag-engine/tests/test_parse_worker_and_grpc.py -v` | 16/16 PASS |
| `python scripts/validate_component_imports.py --root .` | `component import guard passed` |
| `git diff --check -- ai/rag-engine/` | PASS |

## 6. AC 逐项核对

| AC | 状态 | 证据 |
|----|------|------|
| `Parse` RPC 定义 | ✅ | `rag.proto:27` |
| `Embed` RPC 定义 | ✅ | `rag.proto:30` |
| `Generate` RPC 定义 | ✅ | `rag.proto:34` |
| `GenerateStream` RPC 定义 | ✅ | `rag.proto:38` (stream GenerateToken) |
| `ParseRequest` 4 字段 | ✅ | `rag.proto:77-82` (download_url, file_name, file_type, chunk_size) |
| `ParsedChunk` 10 字段 | ✅ | `rag.proto:84-95` (chunk_id, content, content_type, page_number, parent_content, chunk_type, metadata_json, image_bytes, image_format, parent_chunk_id) |
| `ParseResponse` repeated ParsedChunk | ✅ | `rag.proto:97-99` |
| `EmbedRequest` repeated string texts | ✅ | `rag.proto:103-105` |
| `EmbedResponse` vectors_flat + dimension + count | ✅ | `rag.proto:107-113` |
| `GenerateRequest` 6 字段 | ✅ | `rag.proto:117-124` |
| `ChatMessage` role + content | ✅ | `rag.proto:126-129` |
| `GenerateResponse` 4 字段 | ✅ | `rag.proto:131-136` |
| `GenerateToken` 4 字段 | ✅ | `rag.proto:138-143` |
| `SourceChunk` 新增 chunk_id 向后兼容 | ✅ | `rag.proto:66-73` (chunk_id=1, 旧字段偏移 2-6) |
| 旧 `Query` RPC 保留 + deprecated 注释 | ✅ | `rag.proto:20-23` ([DEPRECATED] 标注) |
| 重新生成 Python stub | ✅ | `rag_pb2.py` / `rag_pb2_grpc.py` / `rag_pb2.pyi` 均已重新生成 |
| 不含 RPC 实现代码 | ✅ | `server.py` 未修改; 新 RPC 基类方法为 `UNIMPLEMENTED` stub |

## 7. References

- Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-025-contract-rag-engine-rpc-proto.md`
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` §2.1 (Proto 设计), §2.5 (旧 Query 保留)
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- CLAUDE.md: §4.1 (proto 先行)
- 前序记录: `repo/development-records/RAG-REFACTOR-STEP-1-CONTRACT.md`
