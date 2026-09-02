# RAG-REFACTOR-STEP-3-FEATURE — kb-service 基础设施实现 (步骤 3 功能)

- **Issue:** issue-030-feature-kb-service-infra-impl
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-18
- **Product line:** core (Services / kb-service)
- **Type:** feature (基础设施实现 + E2E 端到端测试)
- **依赖:** #027 (接口) + #028 (Core 功能) + #029 (rag-engine 功能)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/migrations/004_kb_vector_store_id.sql` | 新增 migration: `ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS vector_store_id TEXT` |
| `services/kb-service/app/repositories/knowledge_base.py` | 新增 `set_vector_store_id()`；所有 SELECT 增加 `vector_store_id` 列 |
| `services/kb-service/app/repositories/chunk.py` | keyword_search 重构（jieba 分词 + token 覆盖率归一化）；新增 `write_chunks()` / `delete_chunks_by_doc()` |
| `services/kb-service/app/core_api/client.py` | 新增 `insert_vector_documents()` / `search_vector_store()` / `upload_object()` |
| `services/kb-service/app/rag_engine/client.py` | 新增 `RagEngineGRPCClient`（Parse/Embed/Generate/GenerateStream）；旧 `RagEngineClient` 标注 `[DEPRECATED]` |
| `services/kb-service/app/api/grpc_server.py` | CreateKB 持久化 `vector_store_id`；DeleteDocument 增加 chunks 清理 |
| `services/kb-service/requirements.txt` | 新增 `jieba>=0.42.1` |
| `services/kb-service/tests/test_core_client.py` | 新增 6 个测试（insert 202, search items+content, upload 两步, missing upload_url） |
| `services/kb-service/tests/test_chunk_repository.py` | 改造为 jieba.lcut mock + CJK 过滤 + 停用词测试 |
| `services/kb-service/tests/test_rag_grpc_client.py` | 新增 timeout + embed 长度校验 + kwargs 测试 |
| `services/kb-service/tests/e2e/test_issue030_e2e.py` | 新增 E2E 端到端测试脚本（MD + DOCX 多文件类型） |
| `ai/rag-engine/app/grpc/server.py` | 新增 Parse/Embed/Generate/GenerateStream RPC 实现 |
| `ai/rag-engine/app/core/embeddings.py` | `get_embed_model()` 返回裸 `OpenAICompatibleEmbedding`（去 LlamaIndex 依赖） |
| `ai/rag-engine/app/core/milvus.py` | `build_vector_store_index()` 适配新 embeddings 接口 |
| `pkg/adapters/runtime/vector_store_service.go` | `InsertDocuments` 支持预计算向量直传 + 维度校验 |
| `pkg/adapters/vectorstore/milvus_store.go` | 搜索结果返回 `Content` 字段 |
| `services/ani-gateway/internal/router/vector_store_resources_test.go` | 新增 delete documents 路由测试 |

---

## 1. Design Decisions（设计决策）

### 1.1 keyword_search 从 ILIKE 改为 jieba 分词 + pg_trgm `%` 操作符

- **模糊点:** Issue AC 写"改造 keyword_search（保持签名）：jieba 分词 + 多 token OR + token 覆盖率归一化，与 rag-engine `PgTrgmRetriever` 行为一致"，但未明确 SQL 实现方式。
- **选择:** 使用 pg_trgm `%` 操作符（而非 `ILIKE '%...%'`），配合 `similarity()` 函数和 `set_config('pg_trgm.similarity_threshold', '0.0', true)`。
- **理由:** (1) `%` 操作符走 GIN trigram 索引，性能优于 `ILIKE` 全表扫描；(2) `similarity()` 返回 0~1 的归一化分数，直接用于 token 覆盖率计算 `score = min(1.0, n_hits / n_tokens)`；(3) 与 rag-engine `retrieve_service._execute_pg_trgm_search_tx` 的 SQL 完全一致，保证跨服务检索行为等价。

### 1.2 `_tokenize_cn_keywords` 从 rag-engine 逐行移植

- **模糊点:** Issue AC 写"与 rag-engine `PgTrgmRetriever` 行为一致"，但未说明是否复用还是重新实现。
- **选择:** 逐行移植 `_tokenize_cn_keywords` + `_CJK_STOPWORDS` 到 `chunk.py`，不跨服务 import。
- **理由:** (1) kb-service 和 rag-engine 是独立部署的服务，跨服务 import 会导致部署耦合；(2) 函数仅 ~30 行，移植成本低于引入共享库；(3) 通过单元测试验证行为等价（`test_chunk_repository.py` 的 mock jieba.lcut + CJK 过滤 + 停用词测试）。

### 1.3 `RagEngineGRPCClient.embed` 反序列化展平数组

- **模糊点:** rag.proto `EmbedResponse` 使用 `vectors_flat`（1D float 数组）+ `dimension` + `count`，而非 `repeated repeated float`。Plan §3.4 写"展平数组 `vectors[i] = vectors_flat[i*dim:(i+1)*dim]`"但未说明边界校验。
- **选择:** 在反序列化时添加长度校验 `if len(flat) < expected: raise RagEngineError(...)`。
- **理由:** gRPC 传输截断或服务端 bug 可能导致 `vectors_flat` 长度不足。不加校验会静默返回错误长度的向量，导致后续 Milvus 插入时维度不匹配（难以追溯）。显式 raise 让错误在最早点暴露。

### 1.4 `RagEngineGRPCClient` 的 timeout 设计

- **模糊点:** Plan §3.4 未指定 gRPC 调用超时策略。
- **选择:** 在 `__init__` 中增加 `timeout: float = 120.0` 参数，对 Parse/Embed/Generate 应用 `timeout=self._timeout`，但 GenerateStream 不设超时。
- **理由:** (1) Parse/Embed/Generate 是 unary RPC，需要防止无限等待；(2) GenerateStream 是 streaming RPC，设超时会在 LLM 长时间生成时中断流，违背设计意图；(3) 120s 与旧 REST 客户端默认一致。

### 1.5 CreateKB 持久化 vector_store_id

- **模糊点:** Plan §3.1 写"CreateKB 存储返回的 vector_store_id"，但未明确是在 CreateKB RPC 内部同步存储还是异步。
- **选择:** 在 CreateKB RPC 内部，Core `POST /vector-stores` 返回后立即调用 `kb_repo.set_vector_store_id()` 同步写入。
- **理由:** (1) vector_store_id 是后续文档上传/检索的关键 ID，必须同步可用；(2) 如果异步存储，在文档上传时可能还未写入，导致向量插入失败；(3) 写入在同一个 RPC 调用内，不会增加额外延迟。

### 1.6 DeleteDocument 增加 chunks 清理

- **模糊点:** Issue AC 未明确要求 DeleteDocument 删除 chunks，但 Plan §3.2 的 `delete_chunks_by_doc()` 暗示需要。
- **选择:** 在 `grpc_server._delete_document` 中，soft-delete 文档后立即在同一事务内调用 `chunk_repo.delete_chunks_by_doc()`。
- **理由:** (1) 文档删除后 chunks 成为孤儿数据，浪费存储空间；(2) chunks 残留会影响 keyword_search 返回已删除文档的内容；(3) 在同一 `async with self._pool.acquire() as conn` 块内执行，保证事务一致性。

### 1.7 E2E 测试使用服务器已部署组件 + 本地服务

- **模糊点:** 用户要求"三种服务只在本地测试，不要上传到服务器，数据库等组件可以使用服务器已经部署的"，但未明确测试脚本架构。
- **选择:** 测试脚本通过 subprocess 启动本地 gateway/rag-engine/kb-service，通过 NodePort 连接服务器 Postgres/Milvus/MinIO/NATS/Redis。
- **理由:** (1) 满足用户"服务本地运行"要求；(2) 服务器组件已部署且配置完整，避免本地重复部署；(3) NodePort 可从开发机直接访问，无需 K8s port-forward。

---

## 2. Deviations（偏离）

### 2.1 `insert_vector_documents` 状态码使用 202 而非 200

- **Spec 原文:** Plan §3.3 写 "POST /vector-stores/{id}/documents"。
- **实际实现:** 检查 `resp.status_code != 202`（而非 `!= 200`）。
- **原因:** OpenAPI `v1.yaml` line 6873 明确定义此端点返回 `202 Accepted`（异步插入：返回 task_id + Location polling header）。使用 200 会导致合法响应被误判为错误。这是对照 OpenAPI 契约源（唯一真实来源）的修正。

### 2.2 E2E 测试中使用 `Qwen3-235B-A22B` 而非 `.env` 中的 `Qwen3.6-35B-A3B`

- **Spec 原文:** `.env` 配置 `VLLM_MODEL=Qwen3.6-35B-A3B`。
- **实际实现:** E2E 测试使用 `VLLM_MODEL=Qwen3-235B-A22B`。
- **原因:** 服务器 10.10.20.181:3011 上的 `Qwen3.6-35B-A3B` 模型返回 HTTP 500（`upstream error: do request failed`），而 `Qwen3-235B-A22B` 正常工作。这是环境问题而非代码缺陷，测试脚本使用可用模型以保证 Query 测试通过。

### 2.3 `get_embed_model()` 返回类型从 `BaseEmbedding` 改为 `OpenAICompatibleEmbedding`

- **Spec 原文:** 原代码 `get_embed_model()` 返回 LlamaIndex `BaseEmbedding` 包装的 adapter。
- **实际实现:** 直接返回 `OpenAICompatibleEmbedding` adapter，`_as_base_embedding` 保留但标注 `[DEPRECATED]`。
- **原因:** Plan §2.3-§2.4 要求新 Embed RPC 不依赖 LlamaIndex。旧的 `get_embed_model()` 返回 `BaseEmbedding` 会导致新 RPC 路径间接依赖 LlamaIndex。旧 Query 路径（`milvus.py` 的 `build_vector_store_index`）改为显式调用 `_as_base_embedding(get_embed_model())`。

### 2.4 Go `InsertDocuments` 增加预计算向量维度校验

- **Spec 原文:** Plan §1 写 `Vector` 字段为 `omitempty`，"旧调用方不传 vector 时 Core 内部用 `localDocumentVector` 生成伪向量（现有行为不变）"。
- **实际实现:** 增加了 `if len(document.Vector) != record.Dimension` 维度校验，返回 `ErrInvalid`。
- **原因:** 生产路径 kb-service 传预计算向量时，维度不匹配会导致 Milvus 插入失败（但错误在 Milvus 层才暴露，难追溯）。在 Core 层早期校验提供清晰错误。dev/测试路径（不传 vector）仍走 `localDocumentVector` 伪向量，行为不变。

### 2.5 E2E 测试脚本不在 Issue 定义的 Scope 内

- **Spec 原文:** Issue #030 Scope 仅包含 `migrations/`, `app/repositories/`, `app/core_api/client.py`, `app/rag_engine/client.py`, `requirements.txt`。
- **实际实现:** 额外修改了 `app/api/grpc_server.py`（CreateKB 持久化 + DeleteDocument chunks 清理）和创建了 E2E 测试脚本。
- **原因:** (1) `grpc_server.py` 的 CreateKB 持久化是 AC "CreateKB 存储返回的 vector_store_id" 的必要实现；(2) DeleteDocument chunks 清理是 AC "新增 delete_chunks_by_doc()" 的调用方；(3) E2E 测试是用户明确要求"判断需不需要进行端到端测试，需要则测试 P0 接口"。

---

## 3. Tradeoffs（权衡）

### 3.1 jieba 分词器：移植 vs 共享库

- **备选 A:** 将 `_tokenize_cn_keywords` 提取到 `repo/pkg/shared/` 共享库，kb-service 和 rag-engine 都 import。
  - 优点：单一真实来源，未来修改只需改一处。
  - 缺点：引入跨服务包依赖，增加部署耦合；Python 共享包在 monorepo 中尚无基础设施。
- **备选 B（选中）:** 逐行移植到 `chunk.py`，通过测试保证等价。
  - 优点：零部署耦合，两个服务独立部署。
  - 缺点：代码重复，未来修改需同步两处。
- **选择理由:** 当前阶段 RAG 重构是多步骤渐进式改造，共享库基础设施尚未建立。移植的 ~30 行代码维护成本低于引入共享包的架构成本。待改造稳定后可在后续 step 提取共享库。

### 3.2 gRPC `RagEngineGRPCClient` vs 扩展旧 REST `RagEngineClient`

- **备选 A:** 在旧 `RagEngineClient` 上增加 `parse()`/`embed()`/`generate()` 方法，内部用 REST 调用。
  - 优点：单一客户端类，不增加文件复杂度。
  - 缺点：Plan §3.4 明确要求 gRPC 客户端；REST 无法处理 Embed 的展平向量序列化；旧客户端无 timeout 支持。
- **备选 B（选中）:** 新建 `RagEngineGRPCClient`，旧 `RagEngineClient` 标注 `[DEPRECATED]`。
  - 优点：gRPC 性能更好（protobuf 二进制 vs JSON）；timeout 原生支持；与 Plan 一致。
  - 缺点：两个客户端共存直到 STEP-11 删除旧路径。
- **选择理由:** Plan 明确要求 gRPC 客户端；新旧共存是渐进式重构的标准模式（STEP-2 新增，STEP-11 删除旧路径）。

### 3.3 E2E 测试：subprocess 启动 vs docker-compose

- **备选 A:** 使用 docker-compose 本地启动所有组件（含 Postgres/Milvus/MinIO）。
  - 优点：完全隔离，不依赖服务器。
  - 缺点：Windows 上 Docker Desktop 资源消耗大；Milvus 初始化慢（60s+）；与用户"使用服务器已部署组件"的要求不符。
- **备选 B（选中）:** subprocess 启动三个服务，通过 NodePort 连接服务器组件。
  - 优点：符合用户要求；启动快（仅三个 Python/Go 进程）；服务器组件配置完整。
  - 缺点：依赖服务器网络可用性；端口冲突需处理。
- **选择理由:** 用户明确要求"数据库等组件可以使用服务器已经部署的"。subprocess 方式最简单直接。

---

## 4. Open Questions（待确认问题）

### 4.1 `.env` 中 `VLLM_MODEL=Qwen3.6-35B-A3B` 返回 500

- **假设:** 服务器 10.10.20.181:3011 上 `Qwen3.6-35B-A3B` 模型可能未正确加载或 vLLM 配置有误。
- **需验证:** 运维团队检查 vLLM pod 日志确认模型状态。如模型确实不可用，需更新 `.env` 为 `Qwen3-235B-A22B`。
- **当前处理:** E2E 测试硬编码使用 `Qwen3-235B-A22B`，不影响生产配置。

### 4.2 rag-engine `REDIS_URL` 默认值为 `localhost:6379`

- **假设:** rag-engine 的 QA 服务需要 Redis 作为 chat store，但默认配置指向 `localhost:6379`，本地运行时需手动设置 `REDIS_URL`。
- **需验证:** 生产部署中 rag-engine 是否正确从环境变量读取 `REDIS_URL`，还是依赖 `.env` 文件。
- **建议:** 在 rag-engine 的 K8s deployment 中确认 `REDIS_URL` 环境变量已配置。

### 4.3 `parse_status` 终态 "ready" vs "parsed"

- **假设:** rag-engine parse_worker 完成后设置 `parse_status = 'ready'`，但 Plan 和 Issue 中未明确终态枚举值。
- **需确认:** parse_status 的合法终态列表。当前测试接受 `("parsed", "done", "completed", "ready", "failed", "error")`。
- **建议:** 在 rag-engine 中统一 parse_status 终态命名（建议使用 `parsed` 而非 `ready`，与 `parse` 动作一致）。

### 4.4 Markdown 表格被 parser 转换为 HTML `<table>`

- **假设:** rag-engine 的 markdown parser 将 `|...|...|` 表格转换为 HTML `<table><tr>...` 格式，而非保留原始 markdown 语法。
- **需确认:** 这是有意行为还是 parser 配置导致。E2E 测试同时检测 `|` 和 `<table>` 两种格式以兼容。
- **建议:** 如需保留 markdown 表格原文，检查 parser 的 `mdformat` 或 `markdown-it` 配置。

### 4.5 MinIO bucket `ani-s13-kb-docs` 需手动创建

- **假设:** Gateway 的 `OBJECT_STORE_BUCKET_PREFIX=ani-s13-` 不会自动创建 bucket，需测试前手动创建。
- **需确认:** 生产环境中 bucket 是否由 infra 脚本自动创建，还是需要应用层 `make_bucket` 逻辑。
- **建议:** 在 gateway 启动时或 KB 创建时增加 bucket 自动创建逻辑（best-effort）。

---

## 5. 验证命令

```bash
# kb-service 单元测试
cd repo/services/kb-service
python -m pytest tests/test_core_client.py tests/test_chunk_repository.py tests/test_rag_grpc_client.py -v
# 结果: 41 passed

# rag-engine 单元测试
cd repo/ai/rag-engine
python -m pytest tests/test_embed_service.py tests/test_parse_worker_and_grpc.py -v
# 结果: 44 passed

# Go router 测试
cd repo
go test ./services/ani-gateway/internal/router/... -run TestVectorStore -v
# 结果: 全部通过

# E2E 端到端测试
cd repo/services/kb-service
python tests/e2e/test_issue030_e2e.py
# 结果: 26 passed, 0 failed
```

## 6. E2E 测试覆盖

| 测试项 | 文件类型 | 验证内容 |
|--------|----------|----------|
| CreateKB | — | 创建知识库，返回 id + name |
| GetKB | — | 读取知识库 |
| 文档上传 + Parse | Markdown (.md) | 显示文字 + 表格 + 图片链接 |
| 文档上传 + Parse | Word (.docx) | 显示文字 + 表格 + **嵌入图片上传到 MinIO 返回 OSS 链接** |
| ListChunks | MD | 验证 text/table/image_link |
| ListChunks | DOCX | 验证 text/table/**MinIO OSS image links** (`[图片:...](ani-kb-docs/...)`) |
| keyword_search | — | 搜索"向量检索"返回 5 条，score 0.5~1.0 |
| Query (LLM) | — | LLM 生成答案 + 2 条 source chunks |
| DeleteDocument | MD + DOCX | 文档删除 + chunks 同步清理 |
| DeleteKB | — | 知识库删除 |
