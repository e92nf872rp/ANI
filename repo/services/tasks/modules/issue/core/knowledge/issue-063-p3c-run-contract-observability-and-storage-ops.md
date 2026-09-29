# KB-SPLIT-P3C 运行合同与可观测性 + 存储运维归属落地

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要固化运行合同（保持并强化 main.py 双栈单进程结构）：/readyz 门控语义（db pool + outbox dispatcher + gRPC server，不把 NATS/rag-engine/Milvus/MinIO 加入门控）、补领域指标、日志关联、保持消费者先订阅后投递/双 loop 资源绑定/JetStream ack 语义/优雅停机逆序；同时落地 D1 存储运维归属：Milvus 监控/备份/容量（etcd 配额与 auto-compaction 教训写入运维手册）、bucket 生命周期策略与配额、预签名 URL 时效配置。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/main.py`、`repo/services/kb-service/app/`（指标、日志、readyz）、`docs/`（运维手册）
- 不改变降级语义（NATS 挂→outbox 积压、Redis 挂→DB-only，均为设计内行为）

## Acceptance Criteria
- [ ] [ADR §五-阶段3-运行合同] `/readyz` 门控语义 = db pool + outbox dispatcher + gRPC server；session_cache / consumers 只上报不门控；**NATS/rag-engine/Milvus/MinIO 不进门控**（宕机属设计内降级或快速失败），依赖健康作为上报项
- [ ] [ADR §五-阶段3-运行合同] 领域指标补齐：outbox 积压量、consumer 在途/重试、解析 run 时长分布、三道无结果闸门命中率、降级事件计数、预签名签发/complete 成功率；指标标签控制基数（无高基数标签）
- [ ] [ADR §五-阶段3-运行合同] 日志关联 request / kb / document / run_id 四要素
- [ ] [ADR §一-1.3] 运行合同保持：消费者先订阅后启动 dispatcher、双事件循环资源绑定（跨 loop 不复用 asyncpg 连接/客户端）、JetStream ack 语义（成功→Ack / poison pill→Ack 吞 / crash→静默重投 / InProgress 续租）、优雅停机逆序
- [ ] [ADR §五-阶段3-存储运维] Milvus 运维手册：监控/备份/容量；现网 etcd 配额与 compaction 教训写入继承
- [ ] [ADR §五-阶段3-存储运维] bucket 生命周期策略与配额；预签名 URL 时效配置项化
- [ ] [ADR §五-阶段4-可观测] /metrics 端点可被采集（为 #066 全链路验收产出真实数据做准备）
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#050 #052 #057 #058（outbox/操作面/向量/对象路径就绪后定义门控与指标）

## Type
core (feature)

## Priority
medium

## Labels
core, kb-service, observability, readiness, runbook, split, adr-0001

## Batch
KB-SPLIT-P3C

## References
- ADR: §一-1.3（运行形态全部代码实证细节）、§五-阶段3（运行合同 + 存储运维归属）、§D1-代价（kb 承接运维）
