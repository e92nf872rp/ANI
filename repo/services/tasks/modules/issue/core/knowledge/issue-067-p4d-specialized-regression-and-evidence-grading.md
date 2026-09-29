# KB-SPLIT-P4D 专项回归：降级路径 / 重解析幂等 / 跨租户负向 / D1·D4 专项 + 证据分级

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要在 #066 全链路环境上完成专项回归：Redis/NATS 宕机降级路径、重解析幂等（同 run_id 不撞幂等键、旧块旧向量清理）、跨租户负向、D1 专项（预签名直传全链路：过期 URL 拒绝、key 越权；图片按前缀批量删除）、D4 专项（重复 RPC 命中 kb_operations 幂等回放），证据按 `pass / fail / not_verified` 分级落档。

## Scope
- Product line: core (kb-service / test)
- Code paths allowed: `repo/services/kb-service/tests/`（专项回归套件，复用 #066 e2e 基建）
- 修复类改动若发现缺陷：走对应功能 issue 回修（不塞本 issue），本 issue 收敛回归与证据

## Acceptance Criteria
- [ ] [ADR §五-阶段4] Redis/NATS 宕机降级路径回归：Redis 挂 → 会话 DB-only；NATS 挂 → outbox 积压恢复后 at-least-once 追平；两者均不使进程不健康（/readyz 语义符合 #063 门控定义）
- [ ] [ADR §五-阶段4] 重解析幂等回归：重跑生成新 run_id（同 doc 幂等键 `parse-{doc_id}-{run_id}-b{batch_no}` 不撞）；旧 kb_chunks 行与旧向量显式清理无残留（DB + Milvus 双验证）
- [ ] [ADR §五-阶段4] 跨租户负向回归（真实依赖级）：另一租户 GET/UPDATE/DELETE → 404/空集；覆盖 KB/Document/Session/Message/Chunk/Permission/Operation
- [ ] [ADR §五-阶段4] D1 专项：预签名直传全链路——过期 URL 拒绝（时效生效）、key 越权拒绝（预定 key 不可写他处）；图片按 `{kb_id}/{doc_id}/images/` 前缀批量删除验证
- [ ] [ADR §五-阶段4] D4 专项：重复 RPC（CreateKB/UpdateKB/Reparse 同 idempotency_key）命中 kb_operations 幂等回放，不重复执行副作用（向量/collection/对象/DB 行数核对）
- [ ] [ADR §五-阶段4] 证据分级落档：每项标 `pass / fail / not_verified`，层次区分（源码级/真实依赖级/正式进程级/产品链路级/部署级）；结果写入 docs/execution/status.md
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#066（独立环境与 e2e 套件就绪）

## Type
test (feature)

## Priority
high

## Labels
core, kb-service, regression, testing, split, adr-0001

## Batch
KB-SPLIT-P4D

## References
- ADR: §五-阶段4（专项回归全项）、§一-1.2（解析幂等语义）、§D1（预签名时效/越权测试面）、§D4（幂等回放）
