# [功能] E2E 测试与观察 (步骤 8B 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§0.1, §0.2, §8B, 步骤 8B)

## Description
作为平台开发者，我需要执行完整的 E2E 测试矩阵，验证新旧路径功能等价（§0.1 等价性矩阵），观察延迟与准确率指标，为 NATS/SSE 切换和旧路径删除提供信心。跑通全部测试。

## Scope
- Product line: core (E2E cross-service)
- Code paths allowed: E2E 测试脚本 + 观察工具

## Acceptance Criteria
- [ ] [Plan §8B] E2E-1：KB 创建 + 文档上传 + 解析 — kb_chunks 行数与旧路径一致
- [ ] [Plan §8B] E2E-2：Query 三种检索模式 — sources Jaccard > 90%
- [ ] [Plan §8B] E2E-3：Query 准确率 — answer 非空率一致
- [ ] [Plan §8B] E2E-4：Query 无结果三道闸门 — ①② NO_RESULT_ANSWER + tokens=0 LLM 未调用；③ NO_RESULT_ANSWER + tokens=LLM实际值
- [ ] [Plan §8B] E2E-5：Query 延迟 — P99 < 旧路径 × 1.5
- [ ] [Plan §8B] E2E-6：SSE 流式 — 事件序列不变（token* → sources → done）
- [ ] [Plan §8B] E2E-7：删除文档 + 向量清理 — kb_chunks + Milvus 向量均删除
- [ ] [Plan §8B] E2E-8：多轮会话 Query — 第 2 轮 history 含第 1 轮 + 第 2 轮 user；question 末尾追加（复现旧行为：user 出现两次）
- [ ] [Plan §8B] E2E-10：Generate prompt 等价 — system prompt = DEFAULT_CONTEXT_TEMPLATE；上下文截断复现 CompactAndRefine；answer 语义一致
- [ ] [Plan §8B] E2E-9：flag 回滚 — 回滚后行为不变
- [ ] [Plan §0.2] 同一 KB + 同一文档集，新旧路径分别 Query，对比 P50/P99 延迟和准确率
- [ ] 所有 E2E 测试通过

## Dependencies
#035 (Query flag switch) — 步骤 8B 依赖 8A。

## Type
core (feature, e2e)

## Priority
high

## Labels
core, feature, e2e

## Batch
RAG-REFACTOR-STEP-8B-FEATURE

## References
- Plan: 步骤 8B (E2E 测试矩阵), §0.1 (等价性矩阵), §0.2 (等价性验证方法)
