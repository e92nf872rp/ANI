# KB-SPLIT-P4A v1 解析路径收敛删除：v1 消费者 + flag 门控 + OFF 发 v1 逻辑

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要删除 v1 解析路径实现"首发即目标态"：v1 subject `ani.tasks.kb.parse` 及其消费者代码、flag `kb_parse_consumer_enabled` 门控逻辑（默认 OFF + OFF 时发 v1）全部不带入；v2 `ani.tasks.kb.parse.v2` 为唯一解析路径，消费者常开。同时验证 rag-engine 侧 parse_worker（v1 消费者）已由 #039 删除，不留残余。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/core/config.py`（删 flag 与 v1 subject 配置）、`repo/services/kb-service/app/consumers/`（v1 消费者删除）、相关调用点
- 验证性检查 `repo/ai/rag-engine/`（#039 已删 parse_worker，本 issue 只验证不重做）

## Acceptance Criteria
- [ ] [ADR §一-1.3] 删除 v1 subject `ani.tasks.kb.parse`（config.py `nats_parse_subject`）与消费者路径；grep 无 `ani.tasks.kb.parse` 非 v2 引用（注意 `ani.tasks.kb.parse.v2` 与 `ani.tasks.kb.parse` 前缀重叠，验证需精确）
- [ ] [ADR §一-1.3] 删除 flag `kb_parse_consumer_enabled`（config.py L43）及全部门控分支（默认 OFF + OFF 时发 v1 的逻辑）；消费者常开，v2 唯一路径
- [ ] [ADR §五-阶段5-不带入清单] v1 解析路径与 flag 门控不带入清单执行完毕；JetStream consumer（durable push、ManualAck、AckWait 30m、MaxDeliver 3）仅服务 v2 + rebuild
- [ ] [ADR §五-阶段5] 验证 rag-engine 侧 parse_worker 与 `ani.tasks.kb.parse` 消费已清（#039 已完成，本项为验证记录：grep rag-engine 无匹配）
- [ ] [ADR §一-1.2] 解析幂等语义保持：v2 路径 `parse-{doc_id}-{run_id}-b{batch_no}` 幂等键、重跑新 run_id、显式清理旧块旧向量不变
- [ ] 单测：v2 全链路单测通过且无 v1 相关死代码；config 中无 flag 残留
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#057 #058（v2 唯一路径闭环：向量/对象路径均已切换自管后再删 v1）

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, cleanup, v1-path-removal, split, adr-0001

## Batch
KB-SPLIT-P4A

## References
- ADR: §一-1.3（subject 目标态 + flag 不带入）、§四 #8（双解析路径耦合点）、§五-阶段5（不带入清单）、#039（rag-engine 侧已删）
