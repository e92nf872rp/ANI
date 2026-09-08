# KB-API-B3 — issue-047 kb-service：ReparseDocument servicer + reset_for_reparse_in_tx

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-047-b3-kb-service-reparse.md`
> Batch: KB-API-B3 (servicer phase) · 产品线: core（Services / kb-service）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

完成日期：2026-09-07
分支：`feat/kb-api-completion`（基于 main `963bc88`，B1/B2/B3-#042 改动同分支叠加）
验证结果：kb-service pytest 316 passed（原 issue 范围 16 项 + review 后 B/C 修复新增 9 项，基线 307 零回归）；`validate-architecture`（component imports + legacy control plane）通过；`git diff --check` 通过。单 issue 审查（局部）+ 全项目框架审查（两项发现 A/B/C）+ review-it 收口审查（clean，3 项 candidate finding 均判定为不可达/过度工程拒绝）。

## 实现了什么

1. `repositories/document.py` 新增 `reset_for_reparse_in_tx`：`UPDATE kb_documents SET parse_status='pending', error_message=NULL, parsed_at=NULL, chunk_count=0`（chunk_count=0 非 NULL——列 NOT NULL，且既有 `update_parse_status_in_tx` 的 COALESCE 语义无法重置，故必须新函数）。
2. `api/grpc_server.py` 新增 `ReparseDocument` sync wrapper + `_reparse_document` 私有实现，与 `_notify_document_uploaded` 同构：
   - idempotency_key 非空校验 → INVALID_ARGUMENT；幂等键=**客户端 request.idempotency_key**（非 notify 合成键）
   - get_kb 前置（NOT_FOUND / rebuilding→FAILED_PRECONDITION）；get_document 前置（NOT_FOUND / ready→FAILED_PRECONDITION）
   - 单事务：doc reset + async_tasks INSERT（task_type='kb.reparse'）+ outbox INSERT（event_type='kb.reparse'，payload 8 字段，storage_path/file_name/object_id 取 DB doc_row 非 request）
   - 返回 AsyncTaskRef（task_id/type/status/location_url）
3. **全项目审查后续修复（用户指令"除issue48，其他错误进行修复"）**：
   - **错误 B（async_tasks 行永久 pending）**：outbox payload 新增 `task_id` 字段（notify+reparse 两处）；`parse_consumer.py` 在 orchestrator 调用后重读 doc 终态并 `complete_task` 闭环——`ready`→completed / `failed`→failed / 非终态留下次投递 / 无 task_id（旧消息）跳过闭环。
   - **错误 C（TOCTOU：幂等检查与 INSERT 之间同键并发第二请求撞 UNIQUE 以 UNKNOWN 上抛）**：notify+reparse 两处以嵌套 SAVEPOINT 包 `create_task_in_tx`，`UniqueViolationError` 后重查分派（模板=既有 `_update_kb` poison-key self-heal）：
     - 重查 None → raise（RLS 竞态，不掩盖）
     - notify：pending/completed 赢家 → 复用同 task_id 回放、不重发 outbox；failed → `revive_task_in_tx` 复活置 pending + 发新 outbox（合成键客户端无法换键，复活是 re-upload 唯一入口）
     - reparse：pending/completed → 复用回放；failed → `FAILED_PRECONDITION`（SPEC §5.4 须换新键），abort 连带回滚外层事务（含 doc reset）
   - `repositories/async_task.py` 新增 `revive_task_in_tx`（条件 `status='failed'` 的 UPDATE...RETURNING，竞态安全）。
4. **明确未动**：错误 A（Gateway handler/route baseline，#048 范围）零触碰——用户明确排除。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `app/repositories/document.py` | 修改 | `reset_for_reparse_in_tx`（约 L301–326） |
| `app/api/grpc_server.py` | 修改 | `ReparseDocument` wrapper + `_reparse_document`（L1624–1808）；notify 块 UNIQUE 竞态自愈（L630–769）；两处 outbox payload 加 task_id |
| `app/repositories/async_task.py` | 修改 | 新增 `revive_task_in_tx`（L167–199） |
| `app/consumers/parse_consumer.py` | 修改 | 任务闭环（L297–338）+ 导入 + 两处 docstring payload 形状更新（8 字段含 task_id） |
| `tests/test_reparse_document.py` | 新增+扩展 | 16 项原 AC → 17 项（UNIQUE 竞态回放用例 + failed 语义重写） |
| `tests/test_us010_wiring.py` | 扩展 | `_NotifyMockConn` 竞态夹具 + 2 项 notify 竞态用例（pending 回放 / failed 复活重发） |
| `tests/test_parse_consumer.py` | 扩展 | `_MockConn.execute` 记录调用 + 6 项闭环用例（completed / failed / 无 task_id / orchestrator 异常仍闭环 / 非终态留开 / doc 删除跳过） |

**范围偏差说明**：issue Scope 原定"禁止触碰 parse_consumer"——该约束针对 reparse 原始实现（不改消费逻辑）；错误 B 修复属用户明示的全项目审查后续修复，consumer 闭环为修 B 唯一正确层（task_id 由 API 层写进 outbox payload，闭环必须在消费侧），Scope 经用户指令扩展。

## 设计决策（Design Decisions）

### D1：reparse 幂等键=客户端 idempotency_key（非 notify 合成键）
- **模糊点：** notify 的键是 `kb.parse:{tenant}:{kb}:{doc}` 合成；reparse 是否照抄。
- **选择：** 按 issue AC / SPEC §5.1 用客户端键。理由：reparse 是显式用户操作，SPEC §5.4 "failed 须换新键" 的换键能力只有客户端键能提供；合成键会把用户锁死在失败任务上。

### D2：UNIQUE 竞态的 failed 分派按入口语义分化（notify 复活 / reparse 拒绝）
- **模糊点：** SPEC §5.4 只定义了客户端键的 failed 语义（换新键）；notify 合成键撞到 failed 行时 SPEC 无定义。
- **选择：** notify → `revive_task_in_tx` 复活重跑（客户端无法换键，复活是 re-upload 后再次解析的唯一入口）；reparse → `FAILED_PRECONDITION` 拒绝并回滚外层（换键是客户端义务）。理由：同一 UNIQUE 约束下两种键的所有权不同——合成键系统所有、客户端键用户所有，所有者决定 failed 行的处置权。
- **测试印证：** notify 复活用例断言重发 outbox 且 payload 带 task_id；reparse 拒绝用例断言无第二次 outbox（doc reset 一并回滚）。

### D3：任务闭环以文档终态为唯一判据（放 consumer 层，非 orchestrator）
- **模糊点：** 闭环可在 orchestrator（知道 task_id 后传参）或 consumer（读 doc 终态）。
- **选择：** consumer 层，判据=orchestrator 调用后重读 `kb_documents.parse_status` 终态（ready/failed）。理由：orchestrator 异常内部吞掉只写 doc 状态不 re-raise（L426–441 已核实），consumer 无法从异常区分成败；且 `ParseOrchestratorProtocol` 不含 task_id 参数，改签名会破坏既有契约与 rag-engine 复用。
- **旧消息兼容：** payload 无 task_id 时跳过闭环——存量消息不产生假闭环，新消息即刻生效，无需版本切换或 NATS subject 变更。

### D4：revive 用条件 UPDATE（`WHERE status='failed'`）而非无条件 UPDATE
- **选择：** 复活语句带条件 + 返回 None 表示竞态脱离。理由：调用方 lookup 与 UPDATE 之间若 status 变化（如消费者刚标 completed），无条件覆盖会篡改终态；条件更新使竞态可检测。revive 返回 None 时 notify 路径不发 outbox、返回现状 status——由后续 notify 的 pending 回放路径自愈。

### D5：reparse 竞态回放硬编码返回 "pending"（notify 侧返回赢家实际 status）
- **选择：** 接受此不对称。理由：并发赢家 INSERT 提交时必为 pending；consumer 闭环在毫秒级窗口内改 completed 的概率极低，且客户端轮询 GetTask 以 DB 为准，返回值只是快照。2 行改动的成本大于收益（review-it 已复核并接受）。

## 偏差（Deviations vs PRD/UX/SPEC）

1. **SPEC §5.1 reparse outbox payload 枚举未含 task_id，notify payload 模板同样未含**（§6.4 payload 字段清单 L337–338 为 7 字段）。实现为 8 字段（+task_id）。
   - **原因：** 错误 B 的根因即 consumer 无从得知 task 行标识——不加此字段闭环无锚点。字段为加法（旧消费者不读该字段），wire 兼容；SPEC 属设计性文档，须同步补记（见 OQ1）。
2. **issue Scope "禁止触碰 parse_consumer"** → 实际修改（闭环逻辑 + docstring）。
   - **原因：** 见"范围偏差说明"——用户明示的全项目修复指令扩展了 Scope；不动 consumer 则错误 B 无法修复。
3. **原 AC "同键重放同 task_id" 的 pytest 语义重写**：旧断言（failed 行存在时事务照跑、新 INSERT 正常）与真实 UNIQUE 约束行为冲突——失败键下 INSERT 必撞 `async_tasks_tenant_id_idempotency_key_key`。新语义=violation→重查→failed 拒绝（FAILED_PRECONDITION）。AC 意图（failed 须换键）不变，测试从不可达路径改为约束真实行为。

## 权衡（Tradeoffs）

### T1：闭环读终态而非传终态（一次额外 SELECT）
- 备选 A（采纳）：consumer 调 orchestrator 后重读 doc 行。多一次 SELECT，但 orchestrator 协议零改动、与 rag-engine 复用不破坏。
- 备选 B（弃用）：orchestrator 返回终态或接收 task_id。需改 `ParseOrchestratorProtocol` 签名，跨层传染（rag-engine 同协议实现）。
- 结论：一次 SELECT 换协议稳定，值。

### T2：非终态时留任务开放（warning 日志，不强制闭环）
- 备选 A（采纳）：`pending/parsing/indexing` 终态重读仍非终态 → warning + 留给下次投递。理由：非终态意味着处理未完成（如并发 reset 竞态），强行标 completed/failed 都会撒谎。
- 备选 B（弃用）：强制标 failed。会误杀活任务（下次投递无法复活——UNIQUE 键锁死）。
- 风险对冲：NATS at-least-once 保证重投；若最终投递耗尽，任务行残留 pending 属可见异常（可监控），好过错误终态。

### T3：TOCTOU 修复用 SAVEPOINT 自愈而非预查加锁
- 备选 A（采纳）：SAVEPOINT + UniqueViolation 捕获 + 重查分派。与 `_update_kb` 既有模式完全同构（仓库先例）；锁开销最小（撞键才多一次查询）；失败键语义可分派。
- 备选 B（弃用）：`SELECT ... FOR UPDATE` 预锁。需要额外 SQL 变体与死锁面分析；且幂等检查在事务外的门（find 先行）仍是 SPEC 规定入口，锁方案与之冲突。
- 备选 C（弃用）：吞 UNIQUE 异常直接重查复用（`_create_kb` 独立事务版模式）。无法区分 failed 键（notify 需要 revive / reparse 需要 abort），分派逻辑必须在 except 内。

### T4：notify 复活语义（failed 行置回 pending）而非另起新行
- 备选 A（采纳）：复活原行（id 不变，result/error 清空）。task_id 稳定——客户端持有的旧 ref 不失效；不产生孤儿行。
- 备选 B（弃用）：删除旧行+INSERT 新行。同事务内删除+插入绕 UNIQUE 可行，但 task_id 漂移破坏外部引用，且 DELETE 需新 repo 方法，改动面更大。
- 结论：复活保 id 稳定，Karpathy 最小改动。

## 开放问题（Open Questions）

### OQ1：SPEC payload 字段枚举待同步（文档债）
SPEC §5.1 L337–338 / §6.4 的 outbox payload 枚举为 7 字段，实现为 8（+task_id）。按 CLAUDE.md SPEC 更新义务应补——但 SPEC 属契约文档，变更建议随本批次 PR 一起提交时同步更新，或由 #048 批次（同 PR 合入，见 issue Dependencies "与 #048 同一 PR"）统一处理。**建议随同 PR 补上，避免文档漂移。**

### OQ2：closed-out 任务的错误详情留空（error_message=NULL）
`complete_task` 无 error_message 参数，failed 闭环只标 status。错误详情已在 `kb_documents.error_message`（orchestrator 写入），task 行定位状态跟踪器。若后续 Console 任务详情页需要 task 级错误展示，需扩 `complete_task` 签名——当前无消费方，不加。

### OQ3：revive 返回 None（竞态脱离）时 notify 返回过期 "failed" 快照
两个并发 retry-notify 抢同一 failed 行时，输家读到旧 failed status 返回。后续 notify 走 pending 回放路径自愈，无重复事件、无丢工作。极窄窗口的最终一致，接受（review-it 复核通过）。

### OQ4：issue-048 范围（错误 A）待 #048 批次处理
Gateway `kb_grpc_client.go`/`kb_resources.go` handler + 路由 + `services-route-baseline.yaml` reparse stale 条目——用户明确排除本轮修复，归 #048。与本 issue 同 PR 合入（issue Dependencies 约定），届时 route-contract 5 条红一并解除。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest tests -q --ignore=tests/e2e     # 316 passed（基线 307 + 新增 9）
  # 分文件：test_reparse_document 17 / test_us010_wiring 14 / test_parse_consumer 27
python -m compileall -q app                       # 语法门禁
cd repo
python scripts/validate_component_imports.py --root services/kb-service
python scripts/validate_inference_legacy_control_plane.py   # validate-architecture 等价（Windows 手动逐条）
git diff --check                                  # exit 0（CRLF 警告为仓库既有）
```

> 注：`make test` / `make validate-architecture` 的 POSIX env 前缀语法（`GOCACHE=` 等）在 Windows PowerShell 不识别，以等价命令逐条补跑（与 B3-#042 OQ4 同类）；CI Linux 不受影响。pytest 消费侧闭环用例依赖 `_MockConn` 扩展（execute 记录调用），不影响生产路径。
