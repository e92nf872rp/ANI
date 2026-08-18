# [功能] 受控切换同步 Query flag 可回滚 (步骤 8A 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§0.1, 步骤 8A)

## Description
作为 Services 层开发者，我需要实现 kb-service Query RPC 的 flag 切换逻辑和 `QueryOrchestrator`，用 flag 切换新旧路径可随时回滚。新路径复现旧路径全部行为：无结果三道闸门、多轮会话 history 含当前轮 user + question 末尾追加、token 计数等价。跑通全部测试。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/core/config.py`, `app/api/grpc_server.py`, `app/services/query_orchestrator.py`

## Acceptance Criteria
- [ ] [Plan 步骤8A] `config.py` 新增 `kb_query_use_new_path: bool = False`（默认 false）+ `kb_query_shadow_mode: bool = False`
- [ ] [Plan 步骤8A] `grpc_server.py` Query RPC 按 flag 选择路径：flag=true → `_query_new_path`；flag=false → 旧路径不变
- [ ] [Plan 步骤8A] shadow 模式：主路径返回后 fire-and-forget 异步对比
- [ ] [Plan 步骤8A] 实现 `QueryOrchestrator`：实现 `QueryOrchestratorProtocol` 接口，retrieve → 无结果闸门 → Generate RPC → 返回
- [ ] [Plan §0.1] 无结果闸门 ①：检索为空 → `NO_RESULT_ANSWER` + `input_tokens=0`，LLM 未调用
- [ ] [Plan §0.1] 无结果闸门 ②：`max_score < score_threshold` → `NO_RESULT_ANSWER` + `input_tokens=0`，LLM 未调用
- [ ] [Plan §0.1] 无结果闸门 ③：dedup 后 sources 为空（LLM 已调用）→ `NO_RESULT_ANSWER` + `input_tokens=LLM实际值`
- [ ] [Plan 步骤8A] 多轮会话：先持久化 user 到 DB+Redis → `_load_history`（Redis 含当前轮 user）→ 调 Generate RPC（history 含当前轮 user，Generate 末尾追加 question 为 USER 消息）→ 复现旧行为（user 出现两次）
- [ ] [Plan 步骤8A] `_load_history` 改用 `LRANGE key -limit -1` 取最近 N 条（与旧 ChatMemoryBuffer token_limit 一致，非最老 N 条）；修改在 flag=true 之后进行
- [ ] [Plan 步骤8A] `NO_RESULT_ANSWER = "未检索到与问题相关的内容，无法回答。"`（与旧 qa_service.py 一致）
- [ ] [Plan 步骤8A] token 计数：Generate RPC 用 `response.usage`（openai SDK 不丢弃）；流式用 `stream_options={"include_usage": True}`
- [ ] flag=false：现有测试全通过
- [ ] flag=true：新路径单测通过
- [ ] 无结果测试：三道闸门 ①② tokens=0 LLM 未调用；③ tokens=LLM实际值 LLM 已调用
- [ ] 多轮测试：第 2 轮 history 含第 1 轮 user+assistant 及第 2 轮 user；Generate 末尾追加 question
- [ ] prompt 等价测试：system prompt = DEFAULT_CONTEXT_TEMPLATE；refine = DEFAULT_REFINE_TEMPLATE；上下文截断复现 CompactAndRefine

## Dependencies
#027 (接口) + #034 (单测+shadow+契约) — 实现依赖接口定义 + 测试验证基础。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-8A-FEATURE

## References
- Plan: 步骤 8A (Query flag 切换设计), §0.1 (等价性矩阵 - 无结果三道闸门/多轮会话/tokens), §2.3 (CompactAndRefine 复现)
