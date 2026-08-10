# M2.1-TASK-B — rag-engine 嵌入与 Milvus 直连 (Issue #012)

完成日期：2026-07-31
对应 Sprint：Sprint 14
验证结果：112 tests passed (纯逻辑), 4 e2e passed (真实 Milvus + 远程 embedding), make validate-architecture passed, git diff --check clean

| 字段           | 值                                                                                                 |
| ------------ | ------------------------------------------------------------------------------------------------- |
| Issue        | #012 — rag-engine 嵌入与 Milvus 直连                                                                   |
| PRD          | US-013 (`prd-core-knowledge-base-platform.md`)                                                    |
| UX           | N/A — backend-only                                                                                |
| SPEC         | `spec-services-rag-engine.md` §1.3, §3.1, §5.1                                                    |
| Batch        | M2.1-TASK-B                                                                                       |
| Dependencies | #009 (parse service, LlamaIndex dependency migration) — per SPEC §10.2 (US-013 depends on US-011) |

## 实现了什么

实现 `embed_service`：消费 `chunk_service` 输出的父子块，转为 LlamaIndex `TextNode`，经 `VectorStoreIndex.from_vector_store` 包装 Milvus 直连的 `MilvusVectorStore`，由 Index 层调用远程 AI 推理服务的 OpenAI 兼容 `/v1/embeddings` 端点完成嵌入后写入 Milvus（v1.2 架构，移除 CoreAPIVectorStore）。集合命名 `kb_{kb_id 去横杠}`，索引 HNSW/COSINE/M=16/efConstruction=200。

## 关键文件改动

| 文件                                              | 新增/修改 | 说明                                                                       |
| ----------------------------------------------- | ----- | ------------------------------------------------------------------------ |
| `ai/rag-engine/app/services/embed_service.py`   | 新增    | 编排器：EmbedService.embed\_and\_write + as\_retriever，Index 层嵌入             |
| `ai/rag-engine/app/core/milvus.py`              | 修改    | MilvusVectorStore 直连 + VectorStoreIndex.from\_vector\_store 包装 + HNSW 参数 |
| `ai/rag-engine/app/core/embeddings.py`          | 修改    | 远程 OpenAICompatibleEmbedding 适配器（替代本地 SentenceTransformer）               |
| `ai/rag-engine/app/core/config.py`              | 修改    | 新增 embedding\_api\_base/api\_key；移除 hf\_endpoint；MILVUS\_ADDR 解析         |
| `ai/rag-engine/requirements.txt`                | 修改    | openai SDK 替代 sentence-transformers + llama-index-embeddings-openai      |
| `ai/rag-engine/tests/test_embed_service.py`     | 新增    | 28 个纯逻辑单测                                                                |
| `ai/rag-engine/tests/test_e2e_embed_service.py` | 新增    | 4 个 e2e 测试（真实 Milvus + 远程 embedding）                                     |
| `ai/rag-engine/tests/demo_e2e_embed.py`         | 新增    | e2e 演示脚本（展示输入/输出）                                                        |
| `.env`                                          | 修改    | EMBEDDING\_MODEL/API\_BASE/API\_KEY/DIM；移除 HF\_ENDPOINT                  |

## 完工标准达成（Issue 6 条 AC）

- [x] \[SPEC] `embed_service` 用 `HuggingFaceEmbedding` 动态加载，写入与查询嵌入统一（SPEC §5.1 embed\_service）→ **架构变更见 Deviation 2.1**：改为远程 AI 推理服务的 OpenAI 兼容端点，写入与查询仍统一
- [x] \[SPEC] `MilvusVectorStore`（LlamaIndex 包 `llama-index-vector-stores-milvus`）直接操作 Milvus（SPEC §5.1）
- [x] \[SPEC] 经 `VectorStoreIndex.from_vector_store(vector_store, embed_model=...)` 包装，由 Index 层嵌入后调 `vector_store.add()`（SPEC §5.1）
- [x] \[SPEC] Milvus 集合命名 `kb_{kb_id 去横杠}`，索引 HNSW、metric=COSINE、M=16、efConstruction=200（SPEC §3.1, §5.1）→ **e2e 实测验证**：pymilvus 检查 `coll.indexes[0].params` 确认 index\_type=HNSW / metric\_type=COSINE / M=16 / efConstruction=200
- [x] \[SPEC] 不封装 CoreAPIVectorStore 适配器（v1.2 架构，SPEC §1.3）→ AST 守卫测试 `test_no_coreapi_vectorstore_adapter_referenced` 确认无代码引用
- [x] `make test` 通过（Python 门禁：112 passed + e2e 4 passed；compile exit 0；architecture ✅；git diff --check exit 0；make test Go 阶段因无 Go 工具链失败，与本期无关）

***

## Implementation Notes

### 1. Design Decisions

**1.1 嵌入模型由本地加载改为远程 AI 推理服务调用（用户确认的架构变更）**

- **Ambiguity:** SPEC §1.3 / PRD §5 / plan.md §0 规定"嵌入统一在 rag-engine 本地 HuggingFaceEmbedding"，但用户明确要求 "向量模型应该由 ai 推理服务提供向量模型连接接口，不应该是本地模型连接"。
- **Choice:** 将嵌入模型从"rag-engine 本地加载 HuggingFace/SentenceTransformer"改为"调用 AI 推理服务的 OpenAI 兼容 `/v1/embeddings` 接口"。`embeddings.py` 实现 `OpenAICompatibleEmbedding` 适配器，通过 `openai` SDK 连接远程端点。
- **Rationale:** 用户决策——向量模型集中由 AI 推理服务托管，rag-engine 不在进程内加载模型权重，降低资源占用与版本耦合。临时端点 `http://10.10.20.197:8006/v1` + `Qwen3-Embedding-0.6B`（无鉴权），后续替换为正式 inference-service 地址仅需改 `.env` 的 `EMBEDDING_API_BASE`/`EMBEDDING_MODEL`。写入与查询仍共享同一 embedding 单例，SPEC §1.3 "嵌入统一"语义不变。

**1.2 自定义 OpenAICompatibleEmbedding 适配器而非 llama-index-embeddings-openai**

- **Ambiguity:** LlamaIndex 有现成的 `OpenAIEmbedding`（`llama-index-embeddings-openai`），但 `OpenAIEmbedding.get_engine()` 强制把 `model` 字段转成 `OpenAIEmbeddingModelType` 枚举，拒绝 `Qwen3-Embedding-0.6B` 这类自定义模型名。
- **Choice:** 实现框架轻量的 `OpenAICompatibleEmbedding` 类（暴露 `get_text_embedding`/`get_text_embedding_batch`/`get_query_embedding` 接口），用 `_as_base_embedding` 包装为 `BaseEmbedding` 子类以通过 LlamaIndex `resolve_embeddings` 的 `isinstance` 校验。
- **Rationale:** 自定义适配器不受 OpenAI 官方模型枚举限制，能传入任意推理服务模型名；用 `openai` SDK 直接调用 `/v1/embeddings`，语义与 OpenAIEmbedding 一致但无枚举校验。包装器缓存避免每次调用重建 pydantic 模型。

**1.3 MilvusVectorStore 1.1.0 用 index\_config dict 而非扁平参数**

- **Ambiguity:** SPEC §3.1 规定 HNSW/COSINE/M=16/efConstruction=200，但 MilvusVectorStore 1.1.0 的构造参数名未在 SPEC 中明确。
- **Choice:** 用 `index_config={'index_type':'HNSW','metric_type':'COSINE','params':{'M':16,'efConstruction':200}}` + `similarity_metric='COSINE'`，而非 `index_type=`/`M=`/`efConstruction=` 扁平参数。
- **Rationale:** e2e 测试发现扁平参数被静默忽略，集合默认用 FLAT 索引。查文档确认 1.1.0 版本通过 `index_config` dict 传递索引规格。这是纯逻辑单测无法发现的——mock 不校验参数语义。

**1.4 Milvus URI 加 tcp\:// 协议前缀**

- **Ambiguity:** pymilvus 连接 Milvus 可以用 `host:port` 或 `uri`，但 2.5+ 的 `MilvusVectorStore` 要求 `uri` 带协议前缀。
- **Choice:** `_milvus_uri()` 返回 `tcp://{host}:{port}`。
- **Rationale:** e2e 测试发现 `uri: 10.10.1.66:31930 is illegal, needs start with [unix,http,https,tcp]`。`init_milvus` 的 `connections.connect(host=, port=)` 不受影响，但 `MilvusVectorStore(uri=)` 需要前缀。

**1.5 get\_embed\_model() 缓存 BaseEmbedding 包装器**

- **Ambiguity:** `_as_base_embedding` 每次 call 构造新的 pydantic `_RemoteEmbedding` 实例，会丢失 LlamaIndex 内部嵌入缓存。
- **Choice:** 新增 `_wrapped_model` 模块级缓存，首次 `get_embed_model()` 包装后复用，`init_embedding_model()` 重新初始化时清除缓存。
- **Rationale:** 性能优化——`embed_and_write` / `as_retriever` 频繁调用 `get_embed_model()`，缓存避免重建 pydantic 模型并保留 LlamaIndex `_text_embeddings_cache`。实测 `same_instance: True`。

### 2. Deviations

**2.1 嵌入模型从本地 HuggingFace 改为远程 AI 推理服务（用户确认）**

- **Spec said:** SPEC §1.3 / PRD §5 / plan.md §0 — "嵌入统一在 rag-engine 本地 HuggingFaceEmbedding"。
- **Implemented:** 远程 AI 推理服务的 OpenAI 兼容 `/v1/embeddings` 端点（`OpenAICompatibleEmbedding` 适配器 + `openai` SDK）。
- **Why:** 用户明确要求 "向量模型应该由 ai 推理服务提供向量模型连接接口，不应该是本地模型连接"。这是架构决策变更，写入与查询仍共享同一 embedding 单例，SPEC §1.3 "嵌入统一"语义不变。AC1 的"HuggingFaceEmbedding 动态加载"由"远程 OpenAI 兼容端点动态加载"实现等价语义。

**2.2 requirements.txt 用 openai SDK 替代 llama-index-embeddings-openai**

- **Spec said:** SPEC §1.3 v1.2 暗示用 LlamaIndex 生态 embedding 包。
- **Implemented:** 直接依赖 `openai>=1.50.0` + 自定义适配器，移除 `llama-index-embeddings-openai`。
- **Why:** `OpenAIEmbedding` 的模型名枚举校验拒绝自定义模型名，无法连接推理服务的 `Qwen3-Embedding-0.6B`。自定义适配器 + `openai` SDK 是绕过枚举限制的最小方案。

### 3. Tradeoffs

**3.1 自定义适配器 vs llama-index-embeddings-openai**

- **Alternatives:** (A) 用 `OpenAIEmbedding` + 绕过枚举校验（monkeypatch 或子类重写 `get_engine`）；(B) 自定义 `OpenAICompatibleEmbedding` + `openai` SDK。
- **Pros/Cons:** (A) 复用 LlamaIndex 生态但需 hack 绕过枚举，脆弱且随版本升级易碎；(B) 适配器独立于 LlamaIndex 内部实现，但需手动实现 `BaseEmbedding` 包装。
- **Chosen:** (B)，因枚举校验是 pydantic 模型层强制约束，monkeypatch 不可靠；自定义适配器用 `openai` SDK 直连，稳定且支持任意模型名。

**3.2 e2e 测试 vs 纯单元测试**

- **Alternatives:** (A) 仅纯逻辑单测（mock 重依赖）；(B) 新增 e2e 测试连接真实 Milvus + 远程 embedding。
- **Pros/Cons:** (A) 快速无外部依赖，但无法发现 MilvusVectorStore 参数语义、URI 协议前缀等真实链路问题；(B) 验证完整链路但依赖真实基础设施。
- **Chosen:** (B)，因有 `real-k8s-lab` 部署的 Milvus（10.10.1.66:31930）和用户提供的 embedding 端点（10.10.20.197:8006/v1）可用。e2e 测试发现了 3 个纯逻辑单测无法发现的真实链路 bug（枚举校验、URI 前缀、index\_config 参数名），证明价值。

### 4. Open Questions

**4.1 嵌入端点的正式归属与替换**

- **Assumption:** 当前 `http://10.10.20.197:8006/v1` + `Qwen3-Embedding-0.6B` 是临时 embedding 服务，无鉴权（`api_key=""`）。
- **To verify:** 正式 inference-service 部署 embedding 模型后，替换 `.env` 的 `EMBEDDING_API_BASE`/`EMBEDDING_MODEL`/`EMBEDDING_API_KEY` 即可，无需改代码。需确认 inference-service 侧是否已有对应 issue 跟进 embedding 模型托管。

**4.2 SPEC §1.3 与实现的架构决策变更记录**

- **Assumption:** 本次改动将"本地 HuggingFace"改为"远程推理服务"，与 SPEC §1.3 / PRD §5 / plan.md §0 的"嵌入统一在 rag-engine 本地 HuggingFaceEmbedding"决策冲突。
- **To verify:** 后续是否需更新 SPEC §1.3 / PRD §5 以反映此架构决策变更？建议在 inference-service 正式部署 embedding 模型后创建对应 issue 补齐该侧实现，并同步更新规划文档。

**4.3 e2e 测试的 CI 集成**

- **Assumption:** `test_e2e_embed_service.py` 需真实 Milvus + embedding 端点可达，默认不纳入 `make test`（与 `test_e2e_parse.py` 一致）。
- **To verify:** CI 环境是否有这些基础设施可达？若有，可考虑在 CI 中加入 e2e 阶段；若否，e2e 仅在本地 lab 可用时手动运行。

**4.4 make test 的 Go 工具链依赖**

- **Assumption:** `make test` 在 `test-go` 阶段失败仅因本 Windows 环境无 Go 工具链，与 #012 无关（与 #010 §4.3 相同的既有环境问题）。
- **To verify:** CI 环境是否有 Go 工具链；若有则 `make test` 完整通过。

***

## Verification commands run

```bash
# 纯逻辑单测（无需外部组件）
cd repo/ai/rag-engine && PYTHONPATH=. python -m pytest tests/test_embed_service.py -q
# → 28 passed

# 全量纯逻辑单测（除 e2e）
python -m pytest ai/rag-engine/tests -q --ignore=ai/rag-engine/tests/test_e2e_parse.py --ignore=ai/rag-engine/tests/test_e2e_embed_service.py
# → 112 passed (84 既有 + 28 新增)

# e2e 测试（真实 Milvus 10.10.1.66:31930 + 远程 embedding 10.10.20.197:8006/v1）
python -m pytest ai/rag-engine/tests/test_e2e_embed_service.py -v -s
# → 4 passed
#   - test_e2e_collection_name_strips_hyphens ✓
#   - test_e2e_build_vector_store_creates_collection_with_hnsw ✓ (pymilvus 确认 index_type=HNSW, metric_type=COSINE, M=16, efConstruction=200)
#   - test_e2e_embed_and_write_then_retrieve ✓ (embed_and_write 3 节点 + as_retriever 查询返回相关结果)
#   - test_e2e_cleanup_collections ✓

# e2e 演示脚本（展示输入/输出）
python ai/rag-engine/tests/demo_e2e_embed.py
# → 远程 embedding 返回 1024 维向量；Milvus 集合创建 HNSW 索引；
#   查询"知识库的向量嵌入和检索"→"知识库模块支持文档解析、向量嵌入和混合检索"得分最高(0.5250)

# 编译检查
python -m compileall -q ai/rag-engine
# → exit 0

# 架构门禁
make validate-architecture
# → ✅ architecture guardrails valid

# 空白检查
git diff --check
# → exit 0
```

## review-it 结果

**review** 评估 5 个发现（F1-F5），均为低/信息级别，经核实实际代码路径后全部判定为故意设计或无需修复：

1. **F1 低** `_RemoteEmbedding` 是闭包类，类型无法静态解析 → **拒绝**（刻意设计，闭包捕获 adapter 避免全局状态）
2. **F2 低** `get_text_embedding_batch` 防御性排序 → **拒绝**（保证顺序正确性，开销可忽略）
3. **F3 低** 单条文本走 batch 路径 → **拒绝**（保持单一路径减少分支复杂度）
4. **F4 信息** `embed()` 顶层 helper 走包装器 → **拒绝**（缓存后零开销，保持单一入口）
5. **F5 信息** `.env` 含凭据 → **已确认安全**（`.gitignore`，非本次引入）

**review-it clean: 无已接受/可执行的发现。**

## 框架审查（用户要求的额外审查）

用户要求 "从整体的框架上查看代码是否有问题，类型，性能优化等是否需要进行修改"，发现并修复 4 个问题：

1. **性能**: `get_embed_model()` 每次重建 `_RemoteEmbedding`（pydantic 模型）丢失嵌入缓存 → 新增 `_wrapped_model` 缓存
2. **文档正确性**: `embed_service.py` docstring 仍显示旧的 `index_type=`/`M=` 扁平参数 → 更新为 `index_config` dict
3. **文档矛盾**: `OpenAICompatibleEmbedding` docstring 与 `_as_base_embedding` 行为矛盾 → 重写 docstring
4. **类型注解缺失**: `embed_model`/`_build_text_node`/`_nodes_from_chunks` 无类型 → 用 `TYPE_CHECKING` 补充（避免循环导入）

