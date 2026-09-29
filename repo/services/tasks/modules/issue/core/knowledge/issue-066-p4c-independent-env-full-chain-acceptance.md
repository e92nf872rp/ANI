# KB-SPLIT-P4C 独立测试环境全链路验收：真实依赖（PG/MinIO/NATS/rag-engine/Milvus）

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要搭建独立测试环境（专属 PG、独立 NATS stream、独立 Milvus 实例、kb 自管 bucket）并走真实依赖全链路验收：预签名直传上传 → complete 登记 → 解析（v2 路径、内网 URL 拉取）→ 检索（三模式 × 三闸门）→ 会话（流式 + 降级）；含可观测性验收（/metrics 真实数据产出）。

## Scope
- Product line: core (kb-service / e2e)
- Code paths allowed: `repo/services/kb-service/`（e2e 测试、test-infra 编排）、测试环境配置
- 独立 NATS stream 沿用 `ani.tasks.kb.*` subject 名（与生产语义一致）

## Acceptance Criteria
- [ ] [ADR §五-阶段4] 独立测试环境就绪：专属 PG、独立 NATS stream（`ani.tasks.kb.*` subject 名沿用）、独立 Milvus 实例、kb 自管 bucket、真实 rag-engine
- [ ] [ADR §五-阶段4] 全链路验收（真实依赖，非 mock）：预签名直传上传 → complete 登记 → 解析 v2（内网 ClusterIP URL 拉取正本）→ 图片产物入库 → 分块/摘要/向量化 → 检索三模式（hybrid/vector/keyword）× 三道无结果闸门（检索空 / 分数<阈值 / 去重后空，前两道不调 LLM）→ 会话（流式 Generate + Redis/DB 双写）
- [ ] [ADR §五-阶段4] 可观测性验收：/metrics 可被采集；#063 领域指标（outbox 积压、consumer 在途、解析时长、闸门命中率、降级计数、预签名/complete 成功率）在全链路中产出真实数据；确认无高基数标签
- [ ] [ADR §五-阶段4] 会话降级验证：Redis 挂 → Query 走 DB-only（会话功能不中断）；NATS 挂 → 事件积压 outbox 延迟投递恢复后追平
- [ ] [ADR §五-阶段4] 证据区分 `pass / fail / not_verified` 并区分层次：源码级 ≠ 真实依赖级 ≠ 正式进程级 ≠ 产品链路级 ≠ 部署级；本 issue 达到"产品链路级"
- [ ] 全链路脚本化可重复执行（e2e 套件入库，供 #067 专项回归复用）
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#055 #060 #061 #062 #063 #064 #065（阶段 4 依赖阶段 1/2/3 全部完成；#064/#065 保证验收的即目标态代码）

## Type
test (feature)

## Priority
high

## Labels
core, kb-service, e2e, testing, split, adr-0001

## Batch
KB-SPLIT-P4C

## References
- ADR: §五-阶段4（隔离验证全项）、§一-1.3（降级语义）、§一-1.4（查询编排三模式三闸门）、§六（阶段 4 依赖 1/2/3）
