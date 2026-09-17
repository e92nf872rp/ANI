# KB-API-B6 — issue #24：知识库全库重建 `POST /knowledge-bases/{kb_id}/rebuild`

> Issue 编号：#24——kb-p1-api-completion-plan §4（无独立 issue 文件，plan 即实施契约）
> Batch: KB-API-B6 · 产品线: core（Services / kb-service + ani-gateway + 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-p1-api-completion-plan.md` §4（B6）
> 前置: KB-API-B1~B5、B8 已实现（B7 未开工；B5 遗留 #25 models 同分支叠加）

完成日期：2026-09-10
分支：`feat/kb-api-completion`（工作区交付、未提交；B5+B6 改动同分支叠加）
验证结果：kb-service pytest **404 passed**（B5 基线 354 → B6 净增 50：test_rebuild_kb 25 + test_rebuild_consumer 2 + test_grpc_server 1 + test_grpc_wiring 1 + test_reparse_document 2 + test_notify_document_uploaded 2 + test_update_kb 2 + test_delete_kb 1 + test_upload_url 2 + test_delete_document 2 + test_permissions 2 + test_audit_logging 9 等互斥/埋点扩展）；gateway `go test ./internal/router/ -run TestKB` ok；`make validate-services` 16 子步骤全绿（Windows 两次环境中断后语义等价补跑，见验证命令注）；`validate-architecture` 通过；baseline：services-route 删 1 条（rebuild）减至 10 条、services-contract 删 3 条豁免减至 7 条。

## 实现了什么

1. **契约层（proto + 生成物）**：
   - `kb_service.proto` 新增 `rpc RebuildKB(RebuildKBRequest) returns (common.v1.AsyncTaskRef)` + `RebuildKBRequest`（tenant_id/kb_id/idempotency_key），注释固化 202 异步语义：KB status→rebuilding、重建期间写操作 FAILED_PRECONDITION、查询不受影响（旧索引继续服务）、Outbox→NATS `ani.tasks.kb.rebuild.v1`；
   - OpenAPI `POST /knowledge-bases/{kb_id}/rebuild`（202 返回 AsyncTask + 400/401/403/404/409）与 `RebuildKnowledgeBaseRequest` schema 本批次前已在库（B8 前的契约阶段入）；route baseline rebuild 条目本批删除，route-contract 全绿；
   - 手写 pb 四文件同步（kb_service_pb2.py / .pyi / _pb2_grpc.py 两侧）+ Gateway `make gen-proto` 生成物；四语言 SDK / docs / doc-api / doc-entrypoints 幂等门禁通过（B6 无新 OpenAPI path，零漂移）。
2. **kb-service 仓储增量**：
   - `async_task.py`：`mark_running`（pending→running 条件 UPDATE，非 pending 竞态返回 None）+ `set_progress`（`GREATEST(progress, $n)` 单调 + LEAST 0..100 钳制）；
   - `knowledge_base.py`：`set_status_in_tx`（`UPDATE ... SET status=$to WHERE kb_id=$id AND status=$from` 条件转置，0 行=竞态脱离返回 False——**B6 互斥的权威机制**）；
   - `document.py`：`list_documents_for_rebuild`（快照 `parse_status IN ('ready','failed') ORDER BY created_at, id` 稳定序）；
   - `core/config.py`：`kb_rebuild_consumer_enabled`（默认 false，运维开关）+ `kb_rebuild_subject`（`ani.tasks.kb.rebuild.v1`）。
3. **servicer `_rebuild_kb` 七步**（`grpc_server.py` L2254–2464）：idempotency_key 非空+uuid 校验 → 幂等回放（find by key：pending/completed 复用回放，failed 不回放——SPEC §5.4 须换新键）→ `get_kb` 前置门控（NOT_FOUND；rebuilding/被 gate 拦截不会到此处，转置先于 gate）→ 单事务：`set_status_in_tx(active→rebuilding)`（0 行→`FAILED_PRECONDITION`，幂等键 find 与转置之间的并发竞争由此条件 UPDATE 原子消除）→ 同事务 audit `kb.rebuild`（before=active/after=rebuilding，B8 埋点枚举位预留即用）→ SAVEPOINT 包 `create_task_in_tx`（`UniqueViolationError` 自愈：pending/completed 回放赢家 task_id 且不再发 outbox；failed → `FAILED_PRECONDITION`，abort 连带回滚含 rebuilding 转置的全事务）→ outbox `kb.rebuild`（payload 仅 `kb_id`/`tenant_id`/`task_id` 三字段；consumer 消费时 `get_kb` 回查最新 KB 配置——回查而非快照是有意选择：重建用的 vector_store_id/embedding_model/chunk_size 始终取最新值，避免快照过期）。失败路径走 `_record_failure_audit`（`_AUDITED_FAILURE_CODES` 白名单，四码记审计）。
4. **rebuild_consumer**（`app/consumers/rebuild_consumer.py` 新建，429 行）：`process_message` 七步——`mark_running` → `get_kb`（无 vector_store_id → failed 收口）→ `list_documents_for_rebuild` 快照 → 逐文档（`get_document` 重验 eligible，防快照后删除）→ `reset_for_reparse_in_tx`（**reset 发生在消费侧每个文档处理前**，查询期间旧索引继续服务）→ `orchestrator.process_document`（per-doc try 隔离：单文档失败不阻断后续）→ `set_progress` 单调推进 → `finally`：恢复 `active`（best-effort，崩溃残留 rebuilding 由下次 rebuild 的转置+运维干预收口）→ `complete_task`（全败 failed / 有成功 completed 且 result JSON 带 `failed_doc_ids`）。`REBUILD_MAX_CONCURRENCY = 1`（`asyncio.Semaphore(1)` 串行）；`build_rebuild_consumer` 工厂，`main.py` 按 `kb_rebuild_consumer_enabled` 条件装配。
5. **互斥矩阵**：六个写 servicer（UpdateKB / DeleteKB / CreateDocumentUploadURL / NotifyDocumentUploaded / DeleteDocument / UpdateKBPermissions）在既有状态门控中追加 rebuilding → `FAILED_PRECONDITION`（HTTP 409）；**查询（GetKB / ListDocuments / GetKBConfig / chunks / sessions / audit-logs）零拦截**（Q1 决策）。
6. **Gateway**（`kb_resources.go` + `kb_grpc_client.go`）：路由 `POST /knowledge-bases/:kb_id/rebuild` + handler（nil-client 503 → idempotency_key 校验 400 → client 调用 → 202 + `kbAsyncTaskJSON`）+ `KBGRPCClient` 接口与实现补 `RebuildKB`（tenant 从 Auth 中间件注入）；`kb_sse_test.go` fake 空实现补编译义务。
7. **baseline 清理**：`services-route-baseline.yaml` 删 rebuild 条目（0/-12）；`services-contract-baseline.yaml` 删 3 条豁免（0/-3，含 rebuild 相关），route-contract / spec-split 全绿。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | RebuildKB RPC + RebuildKBRequest |
| `api/openapi/services/v1.yaml` | 修改 | rebuild path/早前在库；audit action 枚举补 `kb.rebuild`；契约细节零漂移 |
| `architecture/services-route-baseline.yaml` | 修改 | 删 rebuild 条目（0/-12） |
| `architecture/services-contract-baseline.yaml` | 修改 | 删 3 条豁免（0/-3） |
| `services/kb-service/app/generated/kb/v1/*`（4 文件） | 修改 | 手写 pb 同步 |
| `services/kb-service/app/api/grpc_server.py` | 修改（+315） | `_rebuild_kb` 七步 + 六 servicer rebuilding gate + 审计埋点接 `_record_failure_audit` |
| `services/kb-service/app/consumers/rebuild_consumer.py` | **新增（未跟踪）** | 七步消费 + Semaphore(1) + finally 恢复；**必须随批次提交** |
| `services/kb-service/app/outbox/dispatcher.py` | 修改（+15） | `subject_overrides`：event_type→subject 映射注入，rebuild 分流独立 subject |
| `services/kb-service/app/core/config.py` | 修改（+13） | `kb_rebuild_consumer_enabled` / `kb_rebuild_subject` |
| `services/kb-service/app/repositories/async_task.py` | 修改（+48） | `mark_running` + `set_progress` |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改（+35） | `set_status_in_tx` 条件转置 |
| `services/kb-service/app/repositories/document.py` | 修改（+29） | `list_documents_for_rebuild` |
| `services/kb-service/main.py` | 修改（+54） | rebuild consumer 条件装配 + dispatcher 接线 |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改（+94） | 路由 + handler |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改（+33） | `KBGRPCClient.RebuildKB` |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改（+245） | rebuild handler 单测（202 透传/400/404/409/503） |
| `services/ani-gateway/internal/router/kb_sse_test.go` | 修改（+6） | fake 空实现 |
| `services/kb-service/tests/test_rebuild_kb.py` | **新增（未跟踪）** | 25 项（转置竞态/SAVEPOINT 自愈/互斥/SQL 契约/outbox payload）；**必须随批次提交** |
| `services/kb-service/tests/test_rebuild_consumer.py` | **新增（未跟踪）** | 3 项（串行性断言 + 工厂装配 + 非 pending 幂等闸门跳过；mock 追加 SET LOCAL 事务内断言）；**必须随批次提交** |
| `services/kb-service/tests/{test_grpc_server.py, test_grpc_wiring.py}` | 修改 | P1_RPCS 常量 + wiring 装配 |

> 工作区另有 B5/B8 遗留的若干 `_e2e_test_*.py` 临时脚本，**不属 B6 记录范围**，提交时按所属批次处置。

## 设计决策（Design Decisions）

### D1：逐文档 reset（消费侧每文档处理前），而非 servicer 批量预 reset
- **模糊点：** reset 全库文档可在触发时（servicer 单事务批量）或消费时逐文档。
- **选择：** 消费侧。理由：查询期间旧索引与 chunk 全程可服务（OpenAPI 注释已固化「queries stay served」）；批量预 reset 会产生「整库瞬间不可查」窗口且长事务风险高。
- **测试印证：** test_rebuild_consumer 断言每文档处理序列为 reset→orchestrator，而非前置批量 UPDATE。

### D2：进度语义 = 已处理文档数推进，而非向量存储侧回调
- **选择：** consumer 每文档终态后 `set_progress`，`GREATEST` 单调 + LEAST 钳制 0..100。理由：rag-engine 无进度回调契约，跨层加回调签名破坏 B3 D3 同类协议稳定性；async_tasks 行是进度唯一权威（TASKCENTER-C1 语义）。

### D3：rebuilding 互斥以 `set_status_in_tx` 条件 UPDATE 为唯一权威点
- **模糊点：** 互斥可由 servicer 先 get_kb 检查再转置（TOCTOU），或单条件 UPDATE。
- **选择：** `UPDATE ... WHERE status=$from` 单语句原子转置，0 行即竞态脱离 → `FAILED_PRECONDITION`。理由：find-then-act 在幂等检查与转置之间留并发窗口，条件 UPDATE 把竞态判定收敛到一行；与 B3 D4 `revive_task_in_tx` 条件 UPDATE 同构（仓库先例）。
- **测试印证：** `test_rebuild_conditional_update_race_loser_rejected` 模拟 set_status_in_tx 返回 False → 断言 FAILED_PRECONDITION 且无 task/outbox 写入。

### D4：崩溃残留 rebuilding 的收口 = 下次 rebuild 转置失败提示 + 运维，而非自动检测恢复
- **模糊点：** consumer 崩溃后 KB 卡 rebuilding，是否加自动探测/恢复。
- **选择：** finally best-effort 恢复 active；崩溃路径由下次 rebuild 触发时 `set_status_in_tx(active→rebuilding)` 失败暴露卡死状态（0 行 → 409），运维审计日志定位后人工干预（audit-logs 可查 rebuilding 起止）。理由：自动恢复需引入分布式心跳/租约，超出 P1 范围；409 + 审计组合已可诊断。登记 OQ 待 P2。

### D5：全库重建串行 `Semaphore(1)`，而非并行度可配
- **选择：** `REBUILD_MAX_CONCURRENCY = 1` 硬编码串行。理由：重建期间同 KB 本就禁止写入，无写竞争收益；并行会放大 vector store / DB 连接占用；plan §4 明确「rebuild 是重操作，串行最简」。多 KB 场景（不同 KB 并发 rebuild）由 consumer 进程级 Semaphore 串行，吞吐由横向扩容解决（登记 OQ）。

### D6：failed_doc_ids 进 task result JSON，而非逐文档子任务行
- **选择：** completed 任务 result 带 `failed_doc_ids` 列表。理由：避免 N 行子任务表（读侧聚合复杂）；单文档失败不阻断重建（per-doc try），部分失败以 completed+列表呈现，客户端据列表对失败文档逐个 ReparseDocument（B3 已提供单文档入口）补救。

## 偏差（Deviations vs Plan/SPEC）

1. **OpenAPI rebuild path 无 security/x-ani-authz 声明**（plan §4.5 有示例）：用户裁定**本次不加**（同批 reparse、PUT permissions 均未加，仅 B5 GET /config 加了；鉴权一致性随系列收口 PR 统一处理，与 B4 OQ5/B8 OQ4 同族惯例）。
2. **plan §4.7 baseline 清理范围收窄**：原文含「PUT /config 条目」——该条归 B7（config.update 触发重建联动）批次，本批只删 rebuild 相关（route 1 + contract 3）。
3. **`_rebuild_kb` 的 UNIQUE 竞态 failed 分派 = FAILED_PRECONDITION 拒绝**（非 notify 复活）：SPEC §5.4 客户端键 failed 须换新键语义，与 B3 reparse 同款；不构成偏差，登记为 D3 补充印证。
4. **验证在 Windows 手动补跑两段**（validate-services 16 步中 spec-split go test 与 model/inference go test 段）：环境问题非改动问题，CI Linux 不受影响（与 B3 OQ4 同类）。

## 权衡（Tradeoffs）

### T1：finally 恢复 active 无条件（含全败路径）vs 保留 rebuilding 标记
- 备选 A（采纳）：finally 无论成败恢复 active。理由：rebuilding 的语义是「进行中」而非「失败」；恢复 active 后 audit + task result 已承载成败信息，KB 可立即再次触发 rebuild 或查询。
- 备选 B（弃用）：全败时保留 rebuilding 引导用户重试。会与互斥矩阵冲突（写操作 409 死锁无法自救），必须运维介入，体验更差。

### T2：list_documents_for_rebuild 快照后逐文档重验 eligible vs 事务内加锁快照
- 备选 A（采纳）：快照 + 每文档 get_document 重验（parse_status/软删）。理由：重建期间写操作已被 gate 禁（快照后状态变化仅剩删除/reparse 类边缘），重验一行 SELECT 即可覆盖；共享锁长事务在重建分钟级时长下不可行。
- 备选 B（弃用）：`SELECT ... FOR UPDATE` 或 advisory lock 长事务。重建时长与事务时长解耦是架构前提。

### T3：rebuild 分流独立 NATS subject vs 复用 ani.tasks.kb.parse
- 备选 A（采纳）：`ani.tasks.kb.rebuild.v1` + dispatcher `subject_overrides` 注入。理由：parse consumer 与 rebuild consumer 进程开关独立（`kb_rebuild_consumer_enabled` 默认 false）、部署节奏解耦；复用 subject 需 consumer 内再分派，违背单一职责。
- 备选 B（弃用）：复用 parse subject + payload type 字段分派。consumer 改动面大且 parse 流量与 rebuild 流量互相干扰。

### T4：完整 rebuild E2E 未随批执行
- 说明：B4/B5/B8 的 E2E 依赖服务器 PG+Redis+MinIO 全链路环境；B6 的 rebuild E2E（触发→rebuilding→409 互斥→逐文档重解析→completed）待环境可用时补跑，功能正确性已由 404 项单测（含 SQL/args 真断言、竞态路径、互斥矩阵）覆盖。登记 OQ2。

## 开放问题（Open Questions）

### OQ1：崩溃残留 rebuilding 的自动检测（D4 后续）→ 孤儿接管已部分落地（2026-09-15）
consumer 进程被 kill 后 KB 停留 rebuilding，写操作持续 409 直至人工干预。P2 可考虑：task 心跳超时 + 定时器收口（TASKCENTER dead_letter_at 字段已有位）；启动时扫描「status=rebuilding 但无进行中 task」的 KB 自动恢复（孤儿自愈——审查发现的 `stop()` 超时路径 cancel 后未 await、且超时跳过 finally 恢复，使进程内优雅关闭也可能留下孤儿 rebuilding，同一自愈机制可覆盖）。当前由 409 + audit-logs 定位，接受。

**→ 2026-09-15 JetStream 迁移轮更新：孤儿接管已部分落地**——`mark_running` 支持过期租约重入（consumer 崩溃后 running 行在租约到期时由 JetStream 重投接管续跑，活租约绝不偷）+ `set_progress` 每文档心跳续租 + rebuild 崩溃静默依赖 `REBUILD_ACK_WAIT == DEFAULT_LEASE_SECONDS`（30 分钟）有意耦合；`stop()` 两消费者均已改 cancel-then-await 收尸（优雅关闭残留缺口收窄）。**残留缺口**：MaxDeliver 耗尽（连续 3 次接管均崩溃）后消息死亡、KB 仍卡 rebuilding——「启动扫描孤儿 KB」定时器自愈仍是候选，优先级降低（仅极端连续崩溃场景）。

### OQ2：rebuild 全链路 E2E 待补 → 主体已执行（2026-09-16，剩守卫收尾）
待服务器存储环境可用时执行：202 触发 → 重建期间查询可用/写操作 409 → completed + failed_doc_ids → 审计 trail（kb.rebuild + doc.reparse 埋点组合）。与 B5 OQ1（#25 models）同归系列收口前的验证清单。

**→ 2026-09-16 已执行**（见「E2E 全链路验证轮」段）：Mode B 24/24（契约+DB 工件+守卫+幂等）、Mode A 29/30（T9 全链路：真文档→parse→chunk×4→rebuild→reparse→completed）；审计 trail（kb.rebuild before/after JSONB）已验。**残留**：precondition parse / T1 rebuild 事件的「本地代发守卫」未实现（OQ-E2E1），实现后 Mode A 最终 rerun 应 30/30。

### OQ3：多 KB 并发重建吞吐（D5 后续）
进程级 Semaphore(1) 串行所有 KB 的重建任务。KB 数量增长后若重建排队明显，需改为 per-KB 串行 + 全局有限并行（subject 按 kb_id 分区或 consumer pool）。当前单实例部署下接受。

### OQ4：文档更新（CURRENT-SPRINT / ANI-06）继承系列惯例推迟
KB-API 系列沿用「development-records/{批次}.md + README.md 索引」两件惯例（B4 OQ5 / B8 OQ4 确认）；CURRENT-SPRINT 与 ANI-06 无 KB-API 专门章节，系列收口 PR 时统一决定四件套补齐。

### OQ5：`complete_task` 无终态守卫（框架审查登记）→ 已关闭（2026-09-15 JetStream 迁移轮落地）
`async_task.py` 的 `complete_task` 用 `WHERE id=$1` 无条件覆盖 status——若同一 task 行已被并发路径（如重投递输家在赢家 complete 后到达）触碰，晚到写入会覆盖终态。mark_running 幂等闸门（首轮审查修复）落地后，重投递输家在闸门处即被拒，触达 complete_task 的路径收敛为单消费者，风险大幅降低。与 B3「接受此不对称」同族：窗口毫秒级、DB 行为权威。后续若引入多消费者并发（OQ3 per-KB 并行改造）须一并加 `WHERE status='running'` 守卫。

**→ 已关闭**：JetStream 迁移轮已给 `complete_task_in_tx` / `complete_task` 加终态守卫 `AND status NOT IN ('completed','failed','cancelled','dead_letter')`（返回 UPDATE 1 行数语义，首个终态写入胜出），与 mark_running 闸门、租约接管共同构成完整的多消费者防御。多消费者并发（OQ3）改造时无需再动。

## 框架审查修复记录（2026-09-15，批次交付后审查）

> 触发：用户对 B6 全链路（servicer / consumer / repositories / dispatcher / main / Gateway / 两测试文件）做框架级审查（架构正确性、类型、性能）。总评：整体架构正确，无 P1 阻断缺陷；性能符合 D5 串行定位无需修改。处置（用户裁定「全部修复」）：2 个 P2 修复 + 3 个 P4 修复 + 2 个 P3 登记 OQ（OQ1 扩写 / OQ5 新增）。

1. **P2-1（修复）：finally 恢复块裸调 `set_status_in_tx`** ——in_tx 函数依赖调用方包 `conn.transaction()`（docstring 明示），原代码 finally 块裸调：**该表在 deploy 基线（`20260501000100_init_schema.sql` L552-554）已 ENABLE + FORCE ROW LEVEL SECURITY + tenant_isolation policy，裸调用下 SET LOCAL 落在事务外、RLS 上下文未生效 → 恢复 UPDATE 静默 0 行匹配、KB 永久卡 rebuilding**。已包 `async with conn.transaction():`（与 servicer `_rebuild_kb` 同款前提）；测试 mock 追加 `in_tx` 跟踪 + `set_local_outside_tx` 违规记录，lifecycle 断言所有 SET LOCAL 均在事务内。（勘误 2026-09-15：初版记录「当前因 knowledge_bases 表无 RLS 仅产生 PG WARNING」失实——deploy 基线该表已有 RLS；裸调的真实后果即静默 0 行，非「未来补 RLS 才暴露」。）
2. **P2-2（修复）：`mark_running` 返回值被忽略，重投递/并发投递无幂等闸门** ——outbox 是 at-least-once，同一 rebuild 事件重投或并发投递时原代码会重跑全量重建（转置竞态输家进 consumer 后必然重复）。已改：`mark_running` 返回 False（task 非 pending）时记 warning 并 return（在 try/finally 之前，不触发恢复逻辑——此时 KB 未转置）。新增测试 `test_process_message_non_pending_task_skips_rebuild`。
3. **P4-3（修复）：`chunk_size = ... or 1024` 短路**——值为 0 时误回退 1024，改 `is None` 判断（NOT NULL 列防御性回退）。
4. **P4-2（修复）：main.py rebuild consumer 装配注释与代码不符**——原注释称「与 parse consumer 共享 orchestrator 实例」，实际无条件新建；改为如实描述（orchestrator 无状态仅持 pool/client 引用，新建廉价，不受 parse consumer 开关门控）。
5. **P4-1（修复）：本记录及 README payload 描述失实**——原「payload 含 8 项 KB 配置快照」，实际仅 kb_id/tenant_id/task_id 三字段 + consumer `get_kb` 回查最新配置；已勘误并说明回查是设计选择（避免快照过期）。
6. **P3-1（登记 OQ5）：`complete_task` 无终态守卫**——`WHERE id=$1` 无条件覆盖，闸门落地后触达路径收敛单消费者，后续观察。
7. **P3-2（登记 OQ1 扩写）：`stop()` 超时路径 cancel 未 await + 跳过 finally 恢复**——进程内优雅关闭超时也会留孤儿 rebuilding，与崩溃残留同收敛到「启动扫描孤儿自愈」候选方案。

验证：`python -m pytest tests -q --ignore=tests/e2e` → **405 passed**（含新增幂等闸门测试；唯一 warning 为既有 test_grpc_wiring coroutine ResourceWarning，非本批引入）。

## JetStream 迁移轮（2026-09-15，二轮框架审查处置）

> 触发：首轮审查收尾时对齐 Go 侧 `pkg/adapters/nats/message_bus.go` 与部署契约（`component-contracts/nats.yaml`：`jetstream.required: true`、subjects `ani.tasks.kb.*`），发现 kb-service 两消费者仍是核心 NATS 订阅 + dispatcher 核心发布——**NATS 无持久化订阅队列，broker 重启或消费者离线即丢消息**，与 Go 全体消费者（JetStream durable + ManualAck）不一致。用户裁定（AskUserQuestion）：「本批迁移 JetStream」（未选「登记 OQ 另行立项」），连同首轮遗留的 complete_task 终态守卫、subject_overrides 专项测试一并执行。

### 迁移内容

1. **共享封装 `app/consumers/jetstream.py`**（新增）：
   - `ensure_ani_tasks_stream`：幂等 ensure `ANI_TASKS` stream（WorkQueue retention + file storage + 24h MaxAge，`ani.tasks.>` 兜底捕获）——生产环境由 Go bootstrap 先建，此处为本地防御路径；两消费者各自 `start()` 内调用（幂等 + flag 门控，dispatcher-only 模式无需 stream）；
   - `subscribe_durable`：durable push consumer + ManualAck，`queue == durable`（nats-py 强制一致，deliver group 让重启副本可重绑而非「consumer is already bound」）；`DeliverPolicy.ALL` + `AckPolicy.EXPLICIT`；`max_ack_pending = max_concurrency` 背压；
   - `ack` / `nak` / `heartbeat_loop`（InProgress 每 ack_wait/3 续租，镜像 Go 心跳 goroutine）。
2. **两消费者迁移**（`parse_consumer.py` / `rebuild_consumer.py`）：
   - 订阅走 `subscribe_durable`（parse：durable `kb-parse-consumer`，`ani.tasks.kb.parse.v2`；rebuild：durable `kb-rebuild-consumer`，`ani.tasks.kb.rebuild.v1`；均 ack_wait 30 分钟 == `DEFAULT_LEASE_SECONDS` 有意对齐 Go model-import-worker，max_deliver 3）；
   - `_handle` ack 语义（镜像 Go handlerFunc）：invalid payload → **Ack**（poison pill 吞掉，durable outbox 行是审计真相）；正常返回（含 task failed 收口）→ **Ack**（task 行是真相，重投会被 mark_running 闸门跳过）；unhandled crash → **既不 Ack 也不 Nak**（静默等 ack_wait 到期重投——rebuild 侧此时 task 租约恰好到期、由过期租约路径接管续跑；Nak 立即重投反而撞活租约闸门后 Ack，消息死、task 卡 running）；
   - **非 dict JSON poison pill 缺口修复**：合法 JSON 但非 dict（如 `[1,2,3]`）原会走 AttributeError 崩溃分支白耗 MaxDeliver 次重投；两消费者 `_handle` 对称加 `isinstance(payload, dict)` 守卫 → raise ValueError → Ack 分支；
   - `stop()` 改 **cancel-then-await**：超时先 cancel 每个残留 task 再逐个 await（cleanup 真正执行后才返回，裸 cancel 会在关闭事件循环上留孤儿 task）；
   - parse 崩溃静默依赖 pipeline 幂等（无状态 pending→parsing UPDATE + re-entrant 清理 + ready-skip）而非租约。
3. **`async_task.py` 防御纵深**（首轮 OQ5 落地 + 迁移扩展）：
   - `complete_task_in_tx` 终态守卫：`AND status NOT IN ('completed','failed','cancelled','dead_letter')`——首个终态写入胜出，晚到者 0 行；
   - `mark_running` 扩展**孤儿接管**：`status='running' AND (lease_until IS NULL OR lease_until < now())` 过期租约可重入（镜像 Go `AcquireLease`），活租约绝不偷——重投递在闸门处被拒；
   - `set_progress` 每次写即**心跳续租**（lease_until / last_heartbeat_at），长重建不失租；`GREATEST` 单调 + 0..100 钳制。
4. **dispatcher `subject_overrides` 专项测试**（`test_outbox_dispatcher.py` +5）：override 命中（kb.rebuild → `ani.tasks.kb.rebuild.v1`）未命中走默认 parse subject、混合批次按行分流、event_type None/空回退默认、`subject_overrides=None` 等价空 dict。

### `.v1` / `.v2` 双轨命名登记

kb 域 NATS subject 命名存在两代后缀并存，均为**有意设计**、按代际固定：
- `ani.tasks.kb.parse.v2`——B6 之前的 Issue 037 切换产物：`.v2` 标记「由 kb-service 自有 parse consumer 消费」（订阅侧 flag `kb_parse_consumer_enabled`），旧 `ani.tasks.kb.parse`（无后缀）仍由 rag-engine parse_worker 消费（回滚路径）；
- `ani.tasks.kb.rebuild.v1`——B6 新增：`.v1` 为新 subject 首代版本号（rebuild 是全新事件流无历史包袱，从 v1 起版），由 rebuild consumer 独占消费（flag `kb_rebuild_consumer_enabled` 默认 false）。
- 后缀语义 = **消费方协议代际**，非 OpenAPI 版本；两 subject 均匹配 stream 兜底 `ani.tasks.>` 与契约 `ani.tasks.kb.*`，互不冲突。后续新增 kb 任务 subject 沿用 `.v1` 起版惯例。

### 测试与验证

- `test_parse_consumer.py` 33 passed（fakes 改造为 _FakeJS/_FakeNATS.jetstream() 门面 + JetStream start durable 全参断言 + stream ensure 幂等 + ack 语义专项 5 项含 non-dict / crash 静默 / heartbeat 取消）；
- `test_rebuild_consumer.py` 23 passed（同构改造 + non-dict 对称测试 + 过期租约接管 / 活租约拒偷）；
- `test_outbox_dispatcher.py` 19 passed（+5 subject_overrides 专项）；
- 全量 `python -m pytest tests -q` → **425 passed**（B6 基线 405 + 净增 20；唯一 warning 为既有 test_grpc_wiring ResourceWarning）；`python -m compileall -q app` OK。
- dispatcher 发布侧未迁移 JetStream publish：核心 NATS publish + outbox 表本身即持久层（crash 后未 mark 的行下轮重发），at-least-once 语义已由 outbox 保证，无需双写。

### 登记 OQ（迁移轮）

- **OQ-JS1：MaxDeliver 耗尽后消息死亡**——rebuild 连续 3 次接管均崩溃则消息移出 WorkQueue stream，KB 永卡 rebuilding。与 OQ1 残留缺口同源，归「启动扫描孤儿 KB」定时器自愈候选统一处置（优先级低：需连续崩溃才会触发）。

## E2E 全链路验证轮 + review-it 收口（2026-09-16）

> 触发：用户指令「端到端测试重建接口」（三应用服务仅本地运行——gateway :8080 / kb-service :8002+gRPC :50053 / rag-engine :8001+gRPC :50052；基础设施直连服务器 10.10.1.66 NodePort——PG :30945 / MinIO :30900 / NATS :31062 / Redis :30453 / Milvus :31930），随后 `/review-it` 审查全部未提交变更。

### E2E：`tests/e2e/test_kb_rebuild_e2e.py`（新增未跟踪，须随批次提交）

- **双模式设计**：Mode B（默认，`kb_rebuild_consumer_enabled=false`）只验契约+DB 工件+守卫+幂等（**24/24 全绿**）；Mode A（`--mode-a`，consumer ON + 本地 rag-engine）验全链路（**29/30，唯一 FAIL 为环境竞速非接口缺陷**；T9 全链路通过：真文档上传→parse→chunk×4→rebuild→逐文档 reparse→completed）。
- **种子直写 PG**（asyncpg，RLS-scoped）：failed/ready 文档等状态是上传管线按需不产出的，直接种 async_tasks/kb_audit_log/outbox_events 制造 T4（rebuilding 态）/T6（毒键）/T8（异型 task_type）前置态。
- **`_OutboxGuardThread`**（现仅 Mode B 武装）：预连接 PG + 会话 GUC + 10ms tight poll + 30s deadline + 独立线程——T1 提交后 ~10-50ms 内将 outbox 事件标记 published，抢在共享 dev DB 的服务器 kb-service（1s 轮询）之前，防其代发到服务器 subject。
- **环境修复两处**：① 服务器 NATS 残留 `kb-rebuild-consumer` durable（filter 绑定旧 tag）→ 探测删除 + 脚本 pre-run/post-run 自动清理；② rag-engine 启动 `No module named 'openai'` → `start_rag_engine()` 改用 rag-engine 自身 `.venv`。
- **E2E 期间跨仓修复**：rag-engine `chunk_service.py` 超长 fenced code block 由「永不可分」改为 force-truncate——真文档中超长 code fence 作为单一 child chunk 打爆 bge-small-en-v1.5 512-token 上限（embedding 端 HTTP 400）；link 维持永不可分；父块仍携带全文（检索不丢内容）；+2 回归测试（截断重组无内容丢失 / 超长 link 原子性）。
- **接口结论**（已答复用户）：接口正常——202/400/401/403/404/409 契约映射全部正确、幂等回放同 task_id 零新行、DB 四工件（KB 转置/audit/async_tasks/outbox）原子同事务、毒键 409 连带回滚 rebuilding 转置。唯一 FAIL 根因：共享 dev DB 下服务器 dispatcher 抢先标 published 并发到服务器 subject，服务器网关 Core 在自己 bucket 前缀找不到对象→404（环境竞速，非接口缺陷）。

### review-it 收口（28 文件 +2780/−451 未提交变更）

- 聚焦单测 **75 passed**（outbox_dispatcher 19 + parse_consumer 33 + rebuild_consumer 23，含 docstring 修复后复跑）；`make validate-architecture` 通过。
- **[P4 已修] docstring 与实现/测试矛盾**：`rebuild_consumer.py` / `jetstream.py` 模块 docstring 声称「handler crash → Nak（redeliver up to MaxDeliver）」，实际 `_handle` 是**刻意静默**（既不 Ack 也不 Nak，等 ack_wait 重投）——有专项测试断言与行内理由注释（Nak 立即重投会撞仍存活租约、闸门 skip 后 Ack 把消息困死）。已修两处 docstring 如实表述，并注明**刻意偏离 Go `handlerFunc` Nak-on-error 先例**。
- **[拒绝] rebuild 路径缺 security/x-ani-authz**：用户已裁定（本记录偏差 #1，系列收口 PR 统一）；baseline `operation_security` 例外维持正确。审查中曾按 GET config 同型补声明后撤销（净零变更）。
- **[拒绝] 幂等键 trim 校验/未 trim 存储**：与 reparse 先例四处同型（grpc_server.py L486/L2094/L2344/L2798），网关同样只校验不归一化；系列级一致行为，非本批引入。
- **[观察] `jetstream.nak` 助手零调用者**：按 CLAUDE.md 原则三「发现死代码，提出来，不自作主张删除」仅登记——若系列收口批次决定删除，须同步删测试 fake 的 nak 方法。
- **结论：review-it clean**，无 actionable findings。

### 本轮偏差与开放问题

- **偏差（rag-engine）**：SPEC §5.1「code block 不可分」→ 超预算 code fence force-truncate。理由：embedding 模型上下文硬限（512 tokens）优先于分块原子性；link 维持不可分是产品规则（撕裂 URL 无意义）；父块携带全文保检索完整性。
- **OQ-E2E1（待办）**：Mode A「本地代发守卫」未实现——precondition kb.parse **与** T1 kb.rebuild 事件同暴露于服务器 dispatcher 竞速（rebuild consumer 是 NATS 订阅者非 outbox 直读者）。方案已定型：守卫预连接 PG+NATS，发现事件先标 published=TRUE 再手动 publish 到 LOCAL subject。同步须修正 e2e 脚本 docstring L42-44（「Mode A 守卫不武装：本地 consumer 须能拿到事件」表述有误——不武装守卫的后果恰是事件被服务器抢发、本地 consumer 拿不到）。实现后 Mode A 最终 rerun 应全绿。
- **OQ-E2E2（待办）**：E2E 测试数据清理 + 最终汇总报告。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest tests -q --ignore=tests/e2e     # 405 passed（B5 基线 354 + B6 净增 51；含审查后新增幂等闸门 1 项）
python -m compileall -q app                       # 语法门禁
cd repo
make validate-services PYTHON=python             # Windows 需覆盖 PYTHON 变量（默认 python3 不存在）
#  → 16 子步骤全绿；其中两段在 Windows 因 Makefile 行内 env 赋值（GOCACHE=... Unix 语法）
#    中断，以下列等价命令补跑（语义等价，CI Linux 不受影响）：
$env:GOCACHE='repo/.cache/go-build'; $env:GOMODCACHE='repo/.cache/gomod'
go test ./services/ani-gateway/internal/middleware/...        # ok 7.1s
go test ./services/ani-gateway/internal/router/... -run TestKB  # ok（含 rebuild handler 5 项）
go test ./services/model-service/... ./services/inference-service/...  # 全包 ok
python scripts/validate_component_imports.py --root services/kb-service
python scripts/validate_inference_legacy_control_plane.py      # validate-architecture 等价
python -m compileall -q services/rag-engine/src               # 静默通过
```

> 注：`PYTHON=python` 覆盖与两段手动补跑均为 Windows 环境问题（Makefile L1135 默认 `python3`、L194 `GOCACHE=` 行内赋值为 Unix 语法），与 B3 OQ4 同类先例；CI Linux 不受影响。baseline 状态：services-route-baseline 10 条既有警告（rebuild 条目已移除）、services-contract-baseline 7 条（删 3 条豁免后）。
