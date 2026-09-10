# KB-API-B2 — issue-045 实现层：chunks / sessions / messages / citations servicer + 修复批次

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-045-b2-kb-service-chunks-sessions-citations.md`
> Batch: KB-API-B2 (implementation phase) · 产品线: core（Services kb-service）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`（§5.1 游标分页、§5.2 参数校验、§5.4 单条坏数据不炸整页、§6.1 错误分类、§9.1 测试矩阵）

完成日期：2026-09-03
分支：`feat/kb-api-completion`（同 PR 含 issue-043/044 B1 残余）
验证结果：kb-service pytest 全量 290 collected / 288 passed（2 个存量失败与改动无关：`test_parse_consumer` 上游 RAG 合并 #129 引入、`test_401_manual` 需真实 PG 密码）；B2 专项 42 passed（含修复批次新增 9 项）；B2+test_grpc_server+test_update_kb 68→83 passed；`atlas migrate hash` 重生成 + validate exit 0；`validate_inference_legacy_control_plane.py` exit 0；`git diff --check` 干净；review-it 收尾审查 Review clean（1 项测试恒真断言已修复并重跑）。

## 实现了什么

1. **B2 四 servicer**（`grpc_server.py`，sync wrapper + `_run_async` 模式）：
   - `ListDocumentChunks`：limit 1–100（默认 50）+ chunk_type 白名单（child/parent/doc_summary）→ get_kb 门禁 → get_document（软删过滤）→ 键集分页。
   - `GetSessionMessages`：limit 1–100（默认 100）→ get_kb → get_session 归属校验（跨 KB 404）→ 复合键集回放。
   - `DeleteSession`：get_kb → 单事务 delete_session（消息先删）→ **提交后** best-effort Redis DEL；session 不存在仍 204 幂等，仅 KB 缺失 404。
   - `ListKBSessions`：get_kb → 聚合 SQL → KBSession 映射 + 复合游标。
2. **citations 展开替换 P1 占位**（`ListKBCitations`）：分页 assistant 消息（`source_chunks IS NOT NULL AND <> 'null'`，JOIN kb_sessions WHERE kb_id）→ Python 侧 json.loads → 按 doc_id 分组取 score 最高 → `KBCitation.id = uuid.uuid5(NAMESPACE_URL, f"ani:kb:citation:{kb_id}:{message_id}:{doc_id}")`；message_id/session_id 透传。
3. **repository 层**（`repositories/`）：
   - `chunk.py` 新增 `list_chunks_by_doc_paged`：`ORDER BY id ASC` 单列键集（`id > $cursor`），chunk_type 过滤，四分支 SQL。
   - `message.py` 新增 `list_sessions`（单条聚合 SQL：COUNT(message)/MAX(created_at)/last_query 关联子查询取最早 user 消息，复合键集 DESC）、`get_session`（kb_id 归属校验否则 None→404）、`list_session_messages_paged`（复合键集 ASC，id tie-break）、`delete_session`（单事务：先 DELETE kb_messages 再 DELETE kb_sessions WHERE id AND kb_id，RETURNING 判存在性）、`list_citation_messages_paged`（消息粒度分页，复合键集 DESC）。
   - `session/cache.py` SessionCache 新增 `delete_session`（Redis DEL，best-effort，失败仅 warning 不阻断）。
4. **P1 占位清理**（`p1_rpcs.py`）：删除 citations/sessions 的 UNIMPLEMENTED 存根（grpc_server.py 委托点改为真实实现）；**UpdateKBPermissions 存根保留**（真 P1 未排期）。
5. **游标共享解析层**（`repositories/cursor.py`，新文件）：`InvalidCursorError(ValueError)` 专用异常 + `parse_composite_cursor`（`{created_at_iso}|{uuid}`）/ `parse_chunk_cursor`（纯 uuid）两个解析辅助；servicer 捕获后 `context.abort(INVALID_ARGUMENT)`。
6. **修复批次（用户在架构复审后确认"修全部三项"）**：
   - P1：非法游标原 `ValueError → UNKNOWN` 改为 servicer 显式 abort `INVALID_ARGUMENT`（4 个调用点），对齐全项目惯例（Go 侧 model/tenant-service `DecodeCursor → InvalidArgument`、gateway `ports.ErrInvalid → 400`）。
   - P3：citations 展开两处 score/page 强转包 try/except（TypeError/ValueError → continue），单条坏数据不炸整页（SPEC §5.4）。
   - P2：迁移 `20260903000300`——`kb_sessions(kb_id, created_at DESC)` 复合索引，支撑 ListKBSessions 键集谓词（对齐 `idx_kb_docs_kb_id` 先例；单实例 PG 不用 CONCURRENTLY）。
7. **review-it 收尾审查**：接受 1 项 finding（`test_citations_full_page_emits_cursor` 中 `... or True` 恒真断言死代码行，已删除并重跑 42 passed）；拒绝多项（`make validate-architecture` 失败全部由 `.run/gomodcache/` 未跟踪目录误扫构成，findings 零条指向本次改动）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/kb-service/app/repositories/cursor.py` | 新增（未跟踪） | InvalidCursorError + 复合/单列游标解析 |
| `services/kb-service/app/repositories/chunk.py` | 修改 | `list_chunks_by_doc_paged`（四分支：chunk_type×cursor）+ `parse_chunk_cursor` 接入 |
| `services/kb-service/app/repositories/message.py` | 修改 | 5 个新函数（含 `delete_session` 单事务）+ `parse_composite_cursor` 接入（3 处）+ cursor_id 类型 `str → uuid.UUID`（SQL 传参消除二次转换） |
| `services/kb-service/app/session/cache.py` | 修改 | `delete_session`（Redis DEL best-effort） |
| `services/kb-service/app/api/grpc_server.py` | 修改 | 4 个 B2 servicer + citations 展开 + 4 处 InvalidCursorError 捕获 + `_chunk_row_to_pb`/`_session_message_row_to_pb`/`_message_cursor`/`_session_cursor` + `_ts` 接受 ISO 字符串 + `json` 顶部导入 |
| `services/kb-service/app/api/p1_rpcs.py` | 修改 | 删 citations/sessions 占位（保留 UpdateKBPermissions） |
| `deploy/migrations/20260903000300_kb_sessions_kb_id_index.sql` | 新增 | kb_sessions(kb_id, created_at DESC) 复合索引 |
| `deploy/migrations/atlas.sum` | 重生成 | 追加 20260903000300 条目 |
| `services/kb-service/tests/test_b2_chunks_sessions_citations.py` | 新增（未跟踪） | 42 用例（含修复批次 9 项：游标解析拒绝、传播、4 个 servicer INVALID_ARGUMENT、score/page 强转跳过）；**必须随批次提交** |
| `services/kb-service/tests/test_grpc_wiring.py` | 修改 | P1 占位锁定测试改造：`test_b2_rpcs_wired_not_unimplemented`（5 RPC 断言 FAILED_PRECONDITION 而非 UNIMPLEMENTED） |
| `services/kb-service/tests/test_grpc_server.py` | 修改 | B2 servicer 校验/映射/门禁用例 |

## 设计决策（Design Decisions）

### D1：游标解析独立成 `cursor.py` 共享模块 + 专用异常，而非各 repository 内联 try/except
- **模糊点：** SPEC §5.1 只定义游标格式，未规定解析层归属与异常契约；B2 修复前游标解析内联在各 repository，`ValueError` 泄漏后被 gRPC worker 线程默认映射为 `UNKNOWN`。
- **选择：** 新建 `repositories/cursor.py`：`InvalidCursorError(ValueError)` 专用异常（继承 ValueError 保持语义兼容）+ 两个解析辅助函数；4 个 servicer 调用点统一 `except InvalidCursorError → context.abort(INVALID_ARGUMENT)`。
- **理由：** ① 跨服务契约对齐：全项目 Go 侧（model/tenant-service `types.DecodeCursor` → `codes.InvalidArgument`，gateway `ports.ErrInvalid` → HTTP 400）均把非法游标归为客户端错误，修复前 kb-service 是唯一映射 `UNKNOWN` 的例外；② 解析逻辑单点定义，三处复合游标 + 两处 chunk 游标不再重复；③ 专用异常类型使 servicer 能精准捕获，不会误吞业务 ValueError。

### D2：citations 分页在消息粒度而非展开后的 citation 粒度
- **模糊点：** SPEC §5.1 #15 只说"citations 由 source_chunks 在 Python 侧展开 + 键集分页"，未规定分页对象是消息还是 citation。
- **选择：** `list_citation_messages_paged` 分页查询 assistant 消息行（复合键集 DESC），每条消息在 servicer 内展开为 0..N 条 citations。
- **理由：** ① SQL 无法在 JSONB 数组内做键集分页（doc_id/score 不是列）；② 消息行是物理实体，游标稳定性天然有保障；③ 单条消息的 citations 数量有限（检索 top-k），页大小抖动可接受（limit 作用于消息数，每页 citations 数 ≤ limit×top-k）。

### D3：`delete_session` 显式先删 kb_messages，即使 ON DELETE CASCADE 已覆盖
- **模糊点：** SPEC §5.1 #18 写"DELETE kb_messages → DELETE kb_sessions"，但表定义已有 CASCADE。
- **选择：** 保持显式两步 DELETE，写入 docstring 说明理由。
- **理由：** ① 对齐 SPEC 文字与 `*_in_tx` 模式先例；② 使单测可对两步 DELETE 的执行顺序/行数做断言（依赖 CASCADE 的隐式行为不可测）；③ 若未来迁移误删 CASCADE 约束，显式删除仍正确。

### D4：DeleteSession 缓存删除双重 best-effort（factory 失败 + DEL 失败均仅 warning）
- **模糊点：** SPEC §5.1 #18 只说"提交后 best-effort 缓存删除"，未规定缓存工厂异常的处理。
- **选择：** `_session_cache_factory()` 构造异常与 `cache.delete_session()` 内部 Redis DEL 异常分别捕获，均 logger.warning 后继续返回 204。
- **理由：** DB 事务已提交是事实源；缓存失效失败仅影响最长 24h TTL 的陈旧读窗口（cache.py 的 DEL 自身也再包一层 try/except，双层防御）；把缓存故障升格为 500 会把可用性问题（DB 已成功）误报为失败。

### D5：next_cursor 采用 `len(rows) >= limit` 判满而非 limit+1 探测
- **模糊点：** SPEC 未规定 next_cursor 生成策略。
- **选择：** 请求 limit 行，返回行数 ≥ limit 即发游标；客户端可能拿到一页空数据 + 游标（恰好取尽）。
- **理由：** ① 与本服务既有 Query/notify 路径模式一致；② 少一次 limit+1 的 fetch 且无需丢弃尾行；③ 空页 + 游标是键集分页的合法状态，客户端翻到空页即停。代价：末页恰好等于 limit 时多一次空翻页，可接受（见 T3）。

### D6：`get_session` 归属校验用 SQL WHERE 而非取出后 Python 比对
- **模糊点：** SPEC §5.1 #17 写"session.kb_id 必须等于 path kb_id，否则 None"。
- **选择：** `WHERE id = $1 AND kb_id = $2` 下推到 SQL。
- **理由：** 单次往返；kb_id 条件在索引/RLS 之上再过滤，语义等价于取出后比对但无多余数据传输；跨 KB session 返回 None → 404，不泄露存在性。

## 偏差（Deviations vs PRD/UX/SPEC）

### DEV1：修复批次 P2 迁移超出 issue-045「无 DDL」Scope
- **Spec 说：** Issue Scope 明确"无 DDL：全部基于既有表"，allowed paths 仅 `repo/services/kb-service/`。
- **实现：** 追加 `deploy/migrations/20260903000300_kb_sessions_kb_id_index.sql` + atlas.sum 重哈希。
- **理由：** 用户在架构复审发现索引缺口后明示"修全部三项"；同分支 B1（issue-043）已有加 migration 先例（20260903000100/00200）；索引是 ListKBSessions 键集谓词 `(created_at, id) < (...)` + `WHERE kb_id = $` 的性能必要条件（此前 kb_sessions 只有 tenant 索引，每页全表扫 kb_id 过滤）。迁移经 atlas hash/validate 入链，改动在 review-it 中单独审查。

### DEV2：citations score/page 展开阶段双重强转防护，超出 SPEC §5.4 字面要求
- **Spec 说：** §5.4 只要求"json.loads 失败跳过该消息"。
- **实现：** 分组阶段 score 与展开阶段 page+score 均包 try/except，非数值跳过该 source/citation。
- **理由：** `json.loads` 成功不保证字段类型（`"score": "0.9"` 或 `"page": "3a"` 合法 JSON 但 float()/int() 崩）；单条 source 坏数据炸整页违背 §5.4 意图（该节标题即"单条坏数据不炸整页"）。属修复批次 P3，用户确认后实施。

### DEV3：B2 专项测试文件含修复批次 9 项用例，测试命名沿用修复前
- **Spec 说：** Issue AC 的测试清单不含游标异常/score 强转用例（这些是 review 后补的修复）。
- **实现：** 9 项修复验证用例并入 `test_b2_chunks_sessions_citations.py`（游标解析拒绝×2、repository 传播×2、servicer INVALID_ARGUMENT×4、score/page 跳过×1）。
- **理由：** 修复与 B2 实现同批次交付，拆文件无意义；用例归入 B2 主题文件便于追溯。

## 权衡（Tradeoffs）

### T1：游标明文 `{iso}|{uuid}` 而非 Go 侧 base64 编码
- **备选 A（采纳）：** 明文复合游标（iso timestamp + `|` + uuid），`_message_cursor`/`_session_cursor` 编码端用 `row['created_at'].isoformat()`。
  - 优点：零编码依赖、可读可调试、uuid 自校验（uuid.UUID 解析即验证）；Python 侧 datetime.fromisoformat/uuid.UUID 异常天然统一到 InvalidCursorError。
  - 缺点：与 Go 侧 `pkg/types/pagination.go` 的 base64 编码风格不一致（跨服务不统一）；游标长度略长。
- **备选 B（弃用）：** 对齐 Go 侧 base64。
  - 弃用理由：两侧游标从不互操作（各服务自管游标）；引入 base64 层徒增一跳编码/解码错误面；B2 三个分页 endpoint 均为 kb-service 私有游标。

### T2：4 个 servicer 调用点逐个 try/except，而非 repository 抛 grpc 状态
- **备选 A（采纳）：** repository 只抛 InvalidCursorError（纯领域异常，无 gRPC 依赖）；servicer 各自捕获转 abort。
  - 优点：repository 层可测试（pytest 直接断言异常类型）；架构约束 repository 不 import grpc（分层干净）。
  - 缺点：4 个调用点重复 6 行模式。
- **备选 B（弃用）：** 辅助函数统一包装（decorator 或 helper）。
  - 弃用理由：servicer 内 abort 时机各异（有 get_kb 门禁在前），统一包装会引入回调/装饰器复杂度；6 行×4 显式优于一层间接。

### T3：`len(rows) >= limit` 判满（无 limit+1 探测）
- **备选 A（采纳）：** fetch limit 行，行数 ≥ limit 发游标。
  - 优点：请求行数=返回行数，语义简单；与既有 servicer 一致。
  - 缺点：末页恰好 = limit 时下一页为空（客户端多一次空翻页）。
- **备选 B（弃用）：** fetch limit+1 行探测。
  - 弃用理由：DB 行为差异（fetch limit+1 再丢弃尾行）增加每个 repository 函数的心智负担；空翻页代价仅一次 RPC 往返；键集分页天然无 OFFSET 重复问题。

### T4：citations 按 (message, doc) 取 score 最高在 Python 分组，而非 SQL JSONB 展开
- **备选 A（采纳）：** SQL 分页消息行，Python 侧 dict 分组取 max。
  - 优点：SQL 简单（无需 jsonb_array_elements + 窗口函数组合分页，那无法键集分页）；每页消息数有限，Python 分组 O(n·k) 可忽略。
  - 缺点：分组逻辑在应用层（若未来 citations 需 SQL 级过滤/排序需重写）。
- **备选 B（弃用）：** SQL `jsonb_array_elements` 展开后 ROW_NUMBER() 取最高分再分页。
  - 弃用理由：展开行无稳定排序列做键集游标（citation id 是 uuid5 计算值，不是列）；复杂度爆炸换不来可测性收益。

## 开放问题（Open Questions）

### OQ1：kb_chunks 分页索引缺 id 列（沿承修复后复审，可接受现状）
`idx_kb_chunks_kb_doc(kb_id, doc_id)` 不含 id；键集 `WHERE kb_id=$1 AND doc_id=$2 AND id > $3 ORDER BY id ASC` 在大文档下需在索引过滤集内排序。kb_chunks 表在 service 本地 migrations（`002_kb_chunks.sql`），不在 deploy 共享集——加索引需评估双迁移目录策略。当前数据量级可接受，记 follow-up。

### OQ2：`make validate-architecture` 本机 `.run/gomodcache/` 误扫（沿承 B1 issue-043 OQ1，升级为 follow-up 建议）
校验器排除表缺 `/.run/`（只排 `/vendor/`、`/.cache/`）；本机 Go 模块缓存迁至 `.run/` 后每次跑产生数百条第三方库自 import 误报，findings 零条指向业务代码。建议 follow-up issue：脚本加排除 + `.gitignore` 补 `.run/`。本次审查按 review-it 契约拒绝修改（基础设施问题非本批次引入）。

### OQ3：存量测试失败 2 项与本批次无关，待上游处理
`test_parse_consumer.py::test_process_message_missing_object_id_dropped`（上游 RAG 合并 #129 引入）、`tests/e2e/test_401_manual.py::test_download`（需真实 Postgres 密码，环境门禁用例）。已用 git log/import 分析确认为存量，不阻塞本批次。

### OQ4：全量 pytest 的 290 collected 与 B2 专项 42 的统计口径
全量含 e2e manual 用例（`start_rag_manual.py` 相关收集）。CI/门禁口径以聚焦命令为准（见验证命令）；全量在本地全跑用于回归确认。

### OQ5：提交时须排除本批次临时工件（沿承 B1 issue-043 OQ5）
e2e 调试产物（`.run/`、日志、`review-it diff` 临时文件）勿入库；`repositories/cursor.py`、`20260903000300_*.sql`、`tests/test_b2_chunks_sessions_citations.py` 为未跟踪交付物，**必须随批次提交**。

### OQ6：route-contract 3 条 B2 `spec_not_in_code` 仍待 issue-046 Gateway 路由解除
B2 契约批次（issue-041 OQ1）登记的 3 条豁免（listKnowledgeBaseDocumentChunks / listKnowledgeBaseSessionMessages / deleteKnowledgeBaseSession）在本批次后 servicer 侧已就绪，但 Gateway 路由（issue-046 范畴）尚未注册，门禁仍红——属预期中间状态。#046 落地 Gateway 3 条路由 + proto client 方法 + `message_id`/`session_id` 映射 + page/score 零值→null 转换（B2 契约 OQ3 的显式要求）后全绿。

## 验证命令（已运行）

```
cd services/kb-service && python -m pytest tests/test_b2_chunks_sessions_citations.py -q          # 42 passed
cd services/kb-service && python -m pytest tests/test_b2_chunks_sessions_citations.py tests/test_grpc_server.py tests/test_update_kb.py -q   # 83 passed
cd services/kb-service && python -m pytest -q                       # 288/290 passed（2 存量失败见 OQ3）
C:\...\atlas.exe migrate hash --dir file://deploy/migrations         # 重生成 atlas.sum
C:\...\atlas.exe migrate validate --dir file://deploy/migrations     # exit 0
python scripts/validate_inference_legacy_control_plane.py            # exit 0
git diff --check                                                    # exit 0
```

> 注：`make validate-architecture` 整体 exit 2 全部由 `.run/gomodcache/` 误报构成（OQ2），过滤后真实代码零违规（本批次改动全为 Python/SQL，无 Go 改动）。Atlas CLI 位于 `ANI\.bin-atlas\atlas.exe`（绝对路径调用，相对路径不被识别；atlas 命令需 `requires_approval`）。
