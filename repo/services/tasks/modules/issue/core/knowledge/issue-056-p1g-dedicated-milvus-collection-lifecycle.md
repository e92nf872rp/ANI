# KB-SPLIT-P1G kb 直连独立 Milvus：接入 + collection 生命周期（D1 向量自管）

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要接入独立 Milvus 实例（pymilvus 进 kb 进程，直连 insert/search/delete，不经 Core 登记），实现 collection 生命周期管理：CreateKB 直建 `kb_{kb_id}` collection、DeleteKB 随删、rebuild 重建，无存量迁移。D1 纪律：向量库平移不换型（pgvector 收敛评估放演进期）。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/`（新建 vector store 客户端模块，如 `app/vector_store/`）、`repo/services/kb-service/requirements.txt`（pymilvus 依赖钉死版本）
- 不动 rag-engine（无状态计算引擎，不受 D1 影响）
- Core 的 vector-stores / knowledge-base-link / 预计算向量插入端点不再被 kb 调用（其下线知会在 #065）

## Acceptance Criteria
- [ ] [ADR §D1-向量自管] pymilvus 客户端模块：连接独立 Milvus 实例（连接配置进 config.py，credentials 走 Secret 由 #061 落位），insert/search/delete 直连操作
- [ ] [ADR §D1-向量自管] collection 生命周期：CreateKB 建 `kb_{kb_id}`（沿用 `vector_store_name = "kb_{kb_id}"` 派生，不经 Core vector-stores 登记）；DeleteKB 删 collection；rebuild 重建
- [ ] [ADR §D1-向量自管] requirements.txt 钉死 pymilvus 版本（全部依赖钉死原则，ADR §三-1）
- [ ] [ADR §一-1.4] embedding 维度对齐：`embedding_model`/`embedding_dim` 共享键（默认 BAAI/bge-m3 / 1024）暂沿用，collection schema 维度与之一致（kb 专属命名改造归 #061）
- [ ] 单测（可 mock Milvus）：collection 建删重建流转、insert/search/delete 参数正确性
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#049（基线已固定）

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, milvus, vector, split, d1, adr-0001

## Batch
KB-SPLIT-P1G

## References
- ADR: §D1（向量自管裁定 + 归属判据）、§一-1.2（KB 状态机 vector_store_name 派生）、§一-1.4（embedding 能力合同）、§一-1.5（不换引擎纪律）
