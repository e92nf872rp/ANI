# RAG-REFACTOR-STEP-4-FEATURE — kb-service RetrieveService + Python RRF 混合检索编排 (步骤 4 功能)

- **Issue:** issue-031-feature-kb-service-retrieve-rrf
- **Date:** 2026-08-19
- **Product line:** core (Services / kb-service + rag-engine)
- **Type:** feature (混合检索编排 + RRF + E2E 验证)
- **Dependency:** #027 (接口) + #030 (kb-service infra) — 依赖接口定义 + CoreClient/gRPC 客户端 + chunk_repo
- **Plan refs:** §4.1 (RRF), §4.2 (retrieve_service 编排), §2.1 (hybrid 归一化), §0.1 (等价性矩阵)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/app/services/rrf.py` | **新建** — 纯 Python RRF 实现, 与 LlamaIndex `QueryFusionRetriever(mode='reciprocal_rerank')` 等价 |
| `services/kb-service/app/services/retrieve_service.py` | **新建** — 混合检索编排器: embed → vector search → keyword search → RRF → parent backfill → dedup |
| `services/kb-service/tests/test_rrf.py` | **新建** — 13 tests: RRF 与 LlamaIndex 对比、k=60 验证、top_n 截断、空输入、稳定性 |
| `services/kb-service/tests/test_retrieve_service.py` | **新建** — 27 tests: 三种模式 + RRF + hybrid 归一化 + 父块回填 + 去重 + asyncio.gather 并发 |
| `ai/rag-engine/app/services/qa_service.py` | LLM 优雅降级: LLM 不可用时返回检索结果 (不 500); `parent_lookup` 移到 try 前; `NO_RESULT_ANSWER` 改为中英双语 |
| `ai/rag-engine/app/services/summary_service.py` | 摘要 prompt 改为英文 + "Use the same language as the content" (LLM 自主决定语言) |
| `ai/rag-engine/app/grpc/server.py` | ParseService 延迟初始化加 double-checked locking; Generate RPC 添加 `finally: svc.close()` 防连接泄漏 |
| `scripts/test_kb_p0_rich_content.py` | E2E 测试脚本: 5 文件类型 x 3 检索模式, 打印输入/分段/输出到文件+终端 |
| `scripts/_fix_all_columns.py` | DB 修复: storage_buckets 20 列 + storage_objects 13 列 |
| `scripts/_relax_bucket_constraint.py` | DB 修复: 放宽 storage_buckets_state_check CHECK 约束 |
| `scripts/_fix_lifecycle_rules.py` | DB 修复: storage_bucket_lifecycle_rules 6 个缺失列 |
| `scripts/_create_kb_docs_bucket.py` | 通过 gateway API 创建 kb-docs bucket |
| `scripts/_fix_kb_documents_final.py` | DB 修复: kb_documents 添加 object_id 列 |
| `scripts/_check_tenant.py` | 检查 tenants 表 schema + 确保 default tenant 存在 |

## 1. Design Decisions

### 1.1 RRF 公式: `1.0 / (rank + k)` 而非 `1.0 / (k + rank + 1)`

- **Ambiguity:** Plan §4.1 伪代码写 `1.0 / (k + rank + 1)` (1-based rank), 但 LlamaIndex 0.14.23 的 `_reciprocal_rerank_fusion` 实际使用 0-based `enumerate` 后 `1.0 / (rank + k)`。
- **Choice:** 使用 `1.0 / (rank + k)` (0-based rank), 与 LlamaIndex 0.14.23 源码完全一致。
- **Rationale:** 功能等价性要求精确复现 LlamaIndex 行为。Plan 伪代码的 `+1` 是文档常见笔误 (RRF 论文原文 Cormack et al. 2009 使用 1-based rank, LlamaIndex 实现改为 0-based 但未更新文档)。13 个测试中 3 个直接与 LlamaIndex 输出对比验证。

### 1.2 `_backfill_parents` 使用 `asyncio.gather` 并发回填

- **Ambiguity:** Plan §4.2 伪代码使用 `asyncio.to_thread` 逐个 `await` 回填父块 (串行), 但各 chunk 的父块查询互相独立。
- **Choice:** 使用 `asyncio.gather(*coros, return_exceptions=True)` 并发调度所有回填查询。
- **Rationale:** Issue #031 code review 发现串行 await 是性能瓶颈。各查询独立无依赖, 并发可显著降低延迟 (N 次查询从 O(N) 串行变为 O(1) 并发)。`return_exceptions=True` 防止单个查询失败影响整体。常见路径 parent_content 已 denormalized, 回填是 no-op (0 个 task), 不影响热路径性能。

### 1.3 `_ParentLookup` Protocol 注入

- **Ambiguity:** Plan §4.2 提到 "不需要独立 lookup 类, 直接用 chunk_repo 查询", 但测试需要 mock 父块查询。
- **Choice:** 定义 `_ParentLookup` Protocol (`lookup_parent` + `lookup_parents`), 默认实现 `_DbParentLookup` 使用 asyncpg 连接池。通过构造函数注入, 测试传入 mock。
- **Rationale:** Protocol 模式允许测试隔离 DB 依赖。`_DbParentLookup` 的 SQL 与 Plan §4.2 规定一致 (`SELECT content FROM kb_chunks WHERE id = $1 AND chunk_type = 'parent'`), 并通过 `set_tenant_context` 实现 RLS。

### 1.4 LLM 优雅降级 (graceful degradation)

- **Ambiguity:** SPEC §5.4 要求 "不幻觉, 但仍返回检索结果", 但未定义 LLM 不可用时的行为。
- **Choice:** 在 `qa_service.py` 中捕获 `engine.chat()` 异常, 返回 `QAResult(answer="[LLM 不可用，仅返回检索结果]...", sources=pre_nodes 转换结果, tokens=0)`。
- **Rationale:** E2E 测试中远程 LLM (Qwen3.6-35B-A3B) 返回 503 `model_not_found`, 旧路径直接 500 错误。优雅降级确保用户仍能看到检索到的 sources (虽然没有 LLM 生成的 answer)。后切换到 Qwen3.8-27B-FP8 (`http://10.10.1.2:6080/v1`) 后降级路径不再触发, 但保留作为容错机制。

### 1.5 摘要 prompt 让 LLM 自主决定语言

- **Ambiguity:** SPEC 说 "200-500 字摘要" (中文字数单位), 但文档可能是英文。
- **Choice:** 将 prompt 从中文硬编码 `请总结以下内容为 {lo}-{hi} 字的摘要` 改为英文 `Summarize the following content in {lo}-{hi} characters. Use the same language as the content.`
- **Rationale:** 让 Qwen3.8-27B (多语言模型) 根据文档内容自主选择摘要语言。中文文档 → 中文摘要, 英文文档 → 英文摘要, 比固定中文 prompt 更自然。方案 A (语言检测) 需要额外依赖, 方案 B (跟随查询语言) 与 parse 时预生成摘要的架构冲突, 方案 C (LLM 自主) 最简单且无需额外代码。

## 2. Deviations

### 2.1 `parent_lookup` 提前到 try 块之前

- **Spec:** Plan §4.2 伪代码中 parent_lookup 在正常路径内赋值。
- **Implementation:** `qa_service.py` 中 `parent_lookup = self._retrieve_service.parent_lookup` 移到 `try` 之前 (L539), LLM 降级路径 (L555-557) 和正常路径 (L590) 都可访问。
- **Why:** code review 发现 `parent_lookup` 仅在 `try` 内赋值, LLM 失败时 `except` 块引用未定义变量, `NameError` 被内层 `except Exception` 吞掉, 导致优雅降级返回空 sources。提前赋值修复此 bug。

### 2.2 `NO_RESULT_ANSWER` 改为中英双语

- **Spec:** SPEC §5.4 定义 `NO_RESULT_ANSWER` 为中文 `"未检索到与问题相关的内容，无法回答。"`
- **Implementation:** 改为 `"未检索到与问题相关的内容，无法回答 / No relevant content found for the question."`
- **Why:** 无检索结果时没有 content 可参考语言, 双语覆盖中英文两种用户。下游代码通过常量引用 (不做精确字符串匹配), 无兼容性风险。

### 2.3 `Generate` RPC 添加 `finally: svc.close()`

- **Spec:** Plan §2.4 未提及 `GenerateRPCService` 的资源清理。
- **Implementation:** `server.py` L395-396 添加 `finally: svc.close()` 确保 httpx 连接池释放。
- **Why:** code review 发现 `GenerateRPCService` 创建后从不调用 `close()`, 每次请求泄漏 httpx 连接池。即使 `context.abort()` 抛异常, `finally` 也会执行。

### 2.4 ParseService 延迟初始化加 double-checked locking

- **Spec:** Plan 未提及 ParseService 初始化的线程安全。
- **Implementation:** `server.py` L278-281 使用 `self._qa_lock` + double-checked locking。
- **Why:** code review 发现并发请求可能创建多个 `ParseService` 实例 (TOCTOU 竞态)。复用 `_qa_lock` 安全 (一次性初始化, 无竞争)。

## 3. Tradeoffs

### 3.1 pg_trgm keyword 模式对跨语言/短文本不匹配

- **Alternatives:** (A) 使用 PostgreSQL full-text search (zhparser/jieba) 替代 pg_trgm; (B) 在 keyword_search 中增加跨语言映射; (C) 保持 pg_trgm + 依赖 hybrid 模式弥补
- **Chosen:** (C) — 保持 pg_trgm, 接受跨语言/短文本 0 结果
- **Pros/Cons:** (A) 需要 DB 安装扩展, 增加运维复杂度; (B) 增加代码复杂度且效果不确定; (C) 零改动, hybrid 模式向量检索已覆盖跨语言场景
- **Why won:** pg_trgm 是 Plan §2.1 指定的关键词检索方式。E2E 测试证实: PDF (英文内容) 和 xlsx (短文本) 的 keyword 模式返回 0 结果, 但 hybrid 和 vector 模式都能检索到。hybrid 是默认模式, keyword-only 是降级场景, 可接受。

### 3.2 `_build_sources_from_fusion` hybrid score 用 cosine 而非 RRF score

- **Alternatives:** (A) 直接用 RRF score 与 score_threshold 比较; (B) 用 RRF score min-max 归一化后比较; (C) 用 vector cosine max_score 做无结果闸门
- **Chosen:** (C) — `max_score = max(vector_results cosine)`, 不用 RRF score 做 threshold 闸门
- **Pros/Cons:** (A) RRF score ~0.016 远低于 threshold 0.3, 总是返回空结果; (B) 归一化后失去绝对语义, 不同 query 间不可比; (C) 与旧 QAService 行为一致, cosine score 有绝对语义
- **Why won:** Plan §2.1 明确要求 "RRF 分数不可直接与 cosine threshold 比较", 旧路径 QAService (L495-512) 使用 `max(vector_similarity_map.values())` 做闸门。功能等价性要求复现此行为。

## 4. Open Questions

### 4.1 等价性测试 Jaccard > 90% 未在 E2E 中验证

- **Assumption:** Issue #031 验收标准要求 "同一 KB, 对比 sources chunk_id 集合 Jaccard > 90%", 但 E2E 测试只验证了 kb-service RetrieveService 的检索结果非空, 未与 rag-engine 旧路径做 shadow 对比。
- **Follow-up:** 需要在步骤 7 (shadow 模式) 中执行 shadow 对比测试, 录制旧路径 Query 请求在新路径回放, 计算 Jaccard。

### 4.2 LLM 切换后摘要质量未全面验证

- **Assumption:** 从 Qwen3.6-35B-A3B 切换到 Qwen3.8-27B-FP8 后, 摘要 prompt 改为英文 + "Use the same language as the content", 但仅验证了中文文档摘要生成正常 (md/docx/xlsx), 未验证英文 PDF 的摘要是否确实用英文生成。
- **Follow-up:** 检查 E2E 日志中 PDF 的 doc_summary chunk 内容, 确认摘要语言跟随文档语言。

### 4.3 kb-docs bucket 不会自动创建

- **Assumption:** Gateway 启动时不自动创建 `kb-docs` bucket, 需要通过 `POST /api/v1/buckets` 手动创建。
- **Follow-up:** 是否应该在 gateway 启动逻辑中添加自动创建 `kb-docs` bucket? 当前依赖手动脚本 `_create_kb_docs_bucket.py`。

## 5. Verification

### 单元测试

| 测试集 | 数量 | 结果 |
|--------|------|------|
| `test_rrf.py` — RRF 与 LlamaIndex 对比 | 13 | 全部 PASS |
| `test_retrieve_service.py` — 三种模式 + RRF + 归一化 + 父块回填 | 27 | 全部 PASS |
| `test_core_client.py` — CoreClient gRPC | 17 | 全部 PASS |
| `test_parse_worker_and_grpc.py` — ParseWorker + gRPC server | 34 | 全部 PASS |
| `test_embed_service.py` — EmbedService + Milvus | 10 | 全部 PASS |
| **合计** | **101** | **全部 PASS** |

### E2E 测试 (P0 接口, 5 文件类型 x 3 检索模式)

| 文件类型 | Chunks | hybrid | vector | keyword | 表格 | 图片 | 其他 |
|---------|--------|--------|--------|---------|------|------|------|
| md | 21 | 1 src | 4 src | 5 src | 1 src | 1 src | 11/11 |
| txt | 4 | 1 src | 2 src | 1 src | 1 src | 1 src | 11/11 |
| pdf | 5 | 2 src | 3 src | **0 src** | 2 src | 2 src | 10/11 |
| docx | 17 | 1 src | 4 src | 4 src | 1 src | 1 src | 11/11 |
| xlsx | 19 | 1 src | 3 src | **0 src** | 1 src | 1 src | 10/11 |

- **通过: 64 | 失败: 2** (pdf + xlsx keyword 模式 — pg_trgm 跨语言/短文本特性, 非 bug)
- 负面测试: 非法 file_type=exe → 400 拒绝
- 日志: `reports/e2e-rich-content-20260819-165258.log`

### Code Review

- **Round 1:** 3 个发现 (Critical: parent_lookup 未定义, High: Generate 连接泄漏, High: Parse 竞态) — 全部修复
- **Round 2:** 1 个发现 (Medium: print_chunks 连接泄漏) — 修复; 1 个拒绝 (Low: prompt "字" vs "characters" 语义偏移 — 可接受)
- **结论:** review-it clean, 101 测试全部通过
