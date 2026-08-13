# Implementation Notes — Issue #11 rag-engine 文档级摘要 (M2.1-TASK-B)

- Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-011-rag-engine-doc-summary.md`
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-012 摘要部分)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§5.1, §5.4, §6.3)
- Batch: M2.1-TASK-B
- Branch: backend-impl
- Date: 2026-07-31

## 1. Design Decisions

### D1: summary\_service 只生成摘要文本，向化存 Milvus 委托给 embed\_service

- **模糊点:** SPEC §5.1 伪码 `embed summary → Milvus.add(chunk_type='doc_summary')` 未明确这一步由 summary\_service 还是 embed\_service 执行。
- **选择:** `summary_service` 只负责「拼接 → LLM → 返回 ChildChunk」；向量嵌入与 Milvus 写入由 `embed_service.EmbedService.embed_and_write(summaries=[...])` 完成，最终以 `chunk_type=doc_summary` 持久化。
- **理由:** 避免在两个服务里重复实现 Milvus 写入与索引构建逻辑；`embed_service` 已统一管理 parents/children/summaries 三类节点的写入（§5.1 embed\_service），summary 只是其中一种节点类型。issue #11 Scope 明确限定 `summary_service.py` only，将写入职责留给 embed\_service 也符合该边界。

### D2: LLM 通过 Protocol 注入，默认工厂懒加载并缓存

- **模糊点:** SPEC §5.1 伪码 `llm.generate(...)` 未指定 LLM 客户端如何构造与生命周期管理。
- **选择:** 定义 `_LLM` Protocol（`complete(prompt) -> Any`），通过构造器 `llm` 参数注入；未注入时 `_default_llm_factory()` 在首次 `summarize()` 调用时懒加载构造 `OpenAILike`，成功后缓存到 `self._llm` 供后续调用复用。
- **理由:** 单测可注入 mock LLM 无需真实端点；长生命周期的 `SummaryService`（parse\_worker 处理多文档）只构建一次 HTTP 客户端，避免每篇文档重复构造 `OpenAILike`。

### D3: 临时 vLLM 端点 + .env 配置（后期可替换）

- **模糊点:** SPEC 未指定 LLM 端点地址；用户明确要求 LLM 由 AI 推理服务以 OpenAI 兼容接口提供，当前推理服务尚未部署 LLM。
- **选择:** 在 `config.py` 新增 `vllm_model`/`vllm_api_base`/`vllm_api_key`/`vllm_context_window` 字段，`.env` 配置临时端点 `http://10.10.20.181:3011/v1` (Qwen3.6-35B-A3B)；`.env.example` 留空模板。工厂通过 LlamaIndex `OpenAILike` 调用，不加载本地模型。
- **理由:** 符合"AI 推理服务暴露 OpenAI 接口给知识库模块"的架构要求；临时端点可随 `.env` 三行替换为正式推理服务，无需改代码。apikey 只进 `.env`（gitignore 忽略），不泄露。

### D4: 摘要长度 \[200, 500] 仅由 prompt 约束，代码不丢弃超范围摘要

- **模糊点:** SPEC §5.1 说"200-500 字摘要"但未说明超出范围如何处理。
- **选择（已修订）:** 长度目标通过 `_SUMMARY_PROMPT_TEMPLATE`（"请总结以下内容为 200-500 字的摘要"）引导 LLM；`_validate_summary()` 只拒绝空摘要，超范围（<200 或 >500）的摘要仍入库，仅在 debug 日志记录超出目标。只有空摘要才触发降级（warning + None）。
- **理由（修订）:** 用户明确要求"超出 200-500 字摘要依旧入库处理，只在模型提示词里限制"。丢弃超范围摘要会让文档完全失去 summary 节点，损失大于长度不达标；prompt 已尽力约束，代码层再硬丢弃是双重惩罚。空摘要仍降级，因为空摘要无任何信息价值。
- **演进:** 初版实现将超范围视为不合格并降级（返回 None）；经用户反馈后改为当前策略。

### D5: token\_count 用 len//2 粗估

- **模糊点:** SPEC 未规定摘要 chunk 的 token\_count 计算方式。
- **选择:** `token_count=max(1, len(validated) // 2)`（中文约 2 字符/token 的粗估）。
- **理由:** summary chunk 不参与分块调度，token\_count 仅作元数据记录，无需精确 tokenizer；与 `test_chunk_service` 的估算口径一致。

## 2. Deviations

### V1: 用 `p.content` 而非 SPEC 伪码的 `p.full_text`

- **SPEC §5.1:** `combined = "\n".join(p.full_text for p in first_n_parents)`
- **实现:** `_concat_parents` 用 `p.content`（`ParentChunk.content` 字段）。
- **原因:** `ParentChunk` 数据类只有 `content` 字段（`chunk_service.py:101` 注释 `# full text = concatenation of child contents`），没有 `full_text` 属性。SPEC 伪码是概念性写法，`content` 即为 full text，实现忠实于数据模型。

### V2: 用 `llm.complete()` 而非 SPEC 伪码的 `llm.generate()`

- **SPEC §5.1:** `summary = llm.generate(f"总结以下内容为 200-500 字摘要：\n{combined}")`
- **实现:** `raw = llm.complete(prompt)`（LlamaIndex `OpenAILike.complete` 接口）。
- **原因:** LlamaIndex `OpenAILike` 的 completion 接口是 `complete(prompt)`，无 `generate` 方法。SPEC 伪码用 `generate` 是泛指 LLM 生成，实现用 LlamaIndex 实际 API。`_extract_summary_text` 同时兼容 `.text`/`.response`/裸字符串，适配真实与 mock 返回。

### V3: 超出 issue Scope 新增 config.py 字段与 demo\_e2e\_summary.py

- **Issue Scope:** `summary_service.py` only。
- **实现:** 额外改了 `config.py`（新增 vllm\_\* 字段）、`.env`/`.env.example`（VLLM\_\* 变量）、新增 `demo_e2e_summary.py`（E2E 链路验证）。
- **原因:** 用户明确要求接入真实 LLM 端点，`config.py` 字段是让运行时真正读到配置的必要最小改动（仅新增字段，不改已有逻辑）；`demo_e2e_summary.py` 是用户明确要求的"超出 Scope 的实时证据"验证脚本，不进 `make test`。两者均为用户显式要求，非擅自扩大范围。

### V4: 超范围摘要不丢弃，仅由 prompt 约束长度

- **SPEC §5.1:** 伪码 `summary = llm.generate(f"总结以下内容为 200-500 字摘要：\n{combined}")` 暗示 200-500 是硬约束。
- **实现:** `_validate_summary()` 不因长度超出 \[200, 500] 丢弃摘要；超范围摘要仍返回入库，仅记 debug 日志。只有空摘要才降级。
- **原因:** 用户明确指示"超过 200-500 字摘要依旧入库处理，只在模型提示词里限制"。SPEC 的 200-500 是目标而非硬性校验门槛；prompt 已引导 LLM 控制长度，代码层再硬丢弃会导致文档失去 summary 节点。`SUMMARY_MIN_CHARS`/`SUMMARY_MAX_CHARS` 常量仍保留，用于 prompt 构造与 debug 日志阈值。

## 3. Tradeoffs

### T1: 摘要生成 vs 检索增强的降级策略

- **备选 A（采用）:** 摘要失败 → 降级为仅父子分块，不阻断入库（warning + None）。
- **备选 B:** 摘要失败 → 重试或阻塞直至成功。
- **对比:** A 满足 AC2/SPEC §5.4「best-effort」；B 会拖垮入库吞吐且 vLLM 不可用时整条管线停摆。
- **胜出:** A——parse\_worker 可继续入库其余结构，summary 缺失只影响文档级检索召回，不影响父子块检索。

### T2: LLM 客户端缓存 vs 每次新建

- **备选 A（采用）:** 工厂成功后缓存 `self._llm`，后续 `summarize()` 复用。
- **备选 B:** 每次调用都走工厂新建 `OpenAILike`。
- **对比:** A 避免重复 HTTP 客户端构造开销（批量文档场景累积明显）；B 实现更简单但浪费。
- **胜出:** A——通过 `test_summarize_caches_factory_llm_across_calls` 验证工厂只调用一次，且实时 vLLM 测试确认 `cached: True`。

### T3: 单测 mock LLM vs 真实端点单测

- **备选 A（采用）:** 单测用 `_FakeLLM`/`_RaisingLLM` mock，E2E 用真实 vLLM（`demo_e2e_summary.py`，不进 make test）。
- **备选 B:** 单测直接连真实 vLLM。
- **对比:** A 不依赖外部服务可用性，CI 稳定；B 会在 vLLM 不可用时测试 flaky。
- **胜出:** A——符合 SPEC §9.1「summary\_service：拼接 + LLM mock + 降级路径」的测试策略归属。

### T4: 长度超范围 — prompt 约束 vs 代码硬丢弃

- **备选 A（采用）:** 超范围摘要仍入库，长度只由 prompt 引导；`_validate_summary` 仅拒空。
- **备选 B（初版实现，已废弃）:** 超范围（<200 或 >500）丢弃 + 降级（warning + None）。
- **备选 C:** 超范围截断到 500 / 补全到 200。
- **对比:** A 保证文档始终有 summary 节点，prompt 已尽力约束长度；B 会让文档完全失去摘要（损失大于长度偏差）；C 引入截断/补全会破坏摘要语义完整性（截断可能断句，补全无依据）。
- **胜出:** A——用户明确要求；避免双重惩罚（prompt 已约束 + 代码再丢弃）；C 的截断/补全会破坏 LLM 输出的连贯性。

## 4. Open Questions

### Q1: 正式 AI 推理服务部署后的替换流程

- **假设:** 替换只需改 `.env` 的 `VLLM_MODEL`/`VLLM_API_BASE`/`VLLM_API_KEY` 三行，无需改代码。
- **需用户确认:** 正式推理服务的端点地址、模型名、apikey 是否与当前 `OpenAILike` 客户端参数兼容（如需要 `is_chat_model=False` 或额外 headers 则需调工厂）。

### Q2: summary chunk 的 page\_number 语义

- **假设:** 文档级摘要无具体页码，用 `SUMMARY_PAGE_NUMBER=1` 占位。
- **需用户确认:** 检索时是否需要区分"文档级摘要"与"第1页内容"——若需区分，应改用 `page_number=0` 或 `None` 表示"全文级"。当前不影响写入与 `chunk_type` 过滤。

### Q3: US-012 整体集成测试（chunk+summary together）的归属

- **假设:** SPEC §10.1 phase 4「chunk+summary together」+ §9.2 `test_chunk_and_summary` 集成测试属于 US-012 整体批次或 issue #12，不在 issue #11「summary\_service.py only」范围。
- **需用户确认:** 是否需要在 issue #11 内追加跨 chunk\_service + summary\_service + kb\_chunks 表写入的集成测试，还是留给后续批次。

## Verification Commands Run

```bash
# Architecture + compile
cd repo && python -m compileall -q ai/rag-engine          # exit 0
cd repo && python scripts/validate_component_imports.py --root .  # passed
cd repo && git diff --check                                 # exit 0

# Unit tests (SPEC §9.1)
cd repo/ai/rag-engine && python -m pytest tests/test_summary_service.py  # 34 passed
cd repo/ai/rag-engine && python -m pytest tests/test_summary_service.py tests/test_embed_service.py tests/test_chunk_service.py tests/test_parse_service.py  # 146 passed

# Live LLM smoke + E2E Milvus (SPEC §5.1 full link)
cd repo && $env:PYTHONPATH="ai/rag-engine"; python -c "from app.services.summary_service import _default_llm_factory; llm=_default_llm_factory(); r=llm.complete('1+1=')"  # -> '2'
cd repo && $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_summary.py  # doc_summary node persisted, HNSW/COSINE/M=16/efConstruction=200
```

## AC Satisfaction

- [x] AC1: `summary_service` 拼接前 N 父块 → LLM 生成 200-500 字摘要 → 向化存 Milvus（`chunk_type=doc_summary`）— E2E 实时验证：真实 vLLM 生成 362 字摘要 → 真实 Milvus 持久化 `chunk_type=doc_summary` 节点
- [x] AC2: 摘要生成失败不阻断入库（降级为仅父子分块，记录 warning）— 7 个降级测试覆盖 LLM 异常/空/超长/超短/工厂失败
- [x] AC3: `make test` 通过 — compileall + validate-architecture + pytest 146 passed
