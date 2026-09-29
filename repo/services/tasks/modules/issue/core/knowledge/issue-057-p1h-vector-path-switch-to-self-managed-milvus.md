# KB-SPLIT-P1H 向量路径切换：parse 写入 + 检索路径从 Core vector-stores 切至自管 Milvus

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要把向量读写热路径全部切换到 kb 自管 Milvus（#056 的客户端）：parse_orchestrator 的向量写入（Embed → 向量直连写入 → kb_chunks 写入）、重解析的旧向量清理、retrieve_service/检索链路的 search 调用，全部去除对 Core vector-stores 的依赖，实现热路径内网直连、少一跳 Core。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/services/parse_orchestrator.py`、`repo/services/kb-service/app/services/retrieve_service.py`、`repo/services/kb-service/app/services/query_orchestrator.py`（若检索向量调用在此）、`repo/services/kb-service/app/repositories/chunk.py`（若涉及向量元数据写入）
- core_api/client.py 的 vector 调用面在本 issue 后不再被引用（模块删除归 #065）

## Acceptance Criteria
- [ ] [ADR §一-1.4] parse_orchestrator：Embed → 向量直连写入自管 Milvus → kb_chunks 写入顺序保持；不再调用 Core vector-stores
- [ ] [ADR §一-1.2] 重解析清理：重跑生成新 run_id（`parse-{doc_id}-{run_id}-b{batch_no}` 幂等键），先删旧 kb_chunks 行与旧向量（自管 Milvus delete），无 ON CONFLICT、靠显式清理语义保持
- [ ] [ADR §D1] 检索路径：向量检索（hybrid/vector 模式的向量腿）直连自管 Milvus search；hybrid = 向量 + pg_trgm 关键词 + RRF 融合（K=60）语义不变
- [ ] [ADR §一-1.4] kb 编排职责链完整保留：取文档内网 URL → Parse → 图片直连上传+链接嵌入 → 重解析清旧 → 索引 → 摘要（前 3 父块 best-effort）→ Embed → 向量写入 → ready | failed
- [ ] 单测：写入-检索往返（mock Milvus）；重解析后旧向量无残留（delete 调用断言）
- [ ] core_api 中 vector-stores 相关引用在上述路径清零（grep 验证）
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#056（Milvus 客户端与 collection 生命周期就绪）

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, milvus, vector, parse, retrieve, split, d1, adr-0001

## Batch
KB-SPLIT-P1H

## References
- ADR: §D1（热路径内网直连）、§一-1.2（解析幂等与清理语义）、§一-1.4（编排职责 + 查询编排三模式）
