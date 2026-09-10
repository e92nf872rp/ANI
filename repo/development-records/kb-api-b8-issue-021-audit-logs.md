# KB-API-B8 — issue #21：审计日志 `GET /knowledge-bases/{kb_id}/audit-logs`

> Issue 编号：#21——kb-p1-api-completion-plan §2 / §6（无独立 issue 文件，plan 即实施契约）
> Batch: KB-API-B8 · 产品线: core（Services / kb-service + ani-gateway + 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-p1-api-completion-plan.md` §6（B8）
> 前置: KB-API-B1~B4 已实现（B5/B6/B7 未开工——B8 提前实施，见偏差 15）

完成日期：2026-09-09（实现）/ 2026-09-10（E2E 双输出重跑 + review-it + 框架审查收口）
分支：`feat/kb-api-completion`（工作区交付、未提交；分支上另有 B4 未推送提交）
验证结果：kb-service pytest **354 passed**（focus 复跑 89/62 passed）；`go test ./services/ani-gateway/internal/router/ -run TestKB` ok；**E2E 31/31 全绿**（本地 gateway + kb-service / 服务器 PG+Redis+MinIO 全链路，含请求/响应双输出日志）；`make validate-services` / `make validate-architecture` 全绿；review-it 终审 **clean**（5 项 P2：1 修复 / 2 拒绝有据 / 2 登记提交时处理）；全项目框架审查（架构/类型/性能）**通过，无需修改**。

## 实现了什么

1. **契约层（proto + OpenAPI + 生成物）**：
   - `kb_service.proto`：新增 `ListKBAuditLogs(ListKBAuditLogsRequest) returns (ListKBAuditLogsResponse)` RPC + `AuditLogEntry` message（id/kb_id/actor_user_id/action/before_state/after_state/error_code/error_msg/Timestamp created_at——JSONB 双状态字段 proto 侧 string 直通，复用 KBChunk.custom_metadata 先例）；
   - `v1.yaml`：新增 `GET /knowledge-bases/{kb_id}/audit-logs`（operationId `listKnowledgeBaseAuditLogs`，limit 默认 20 / min 1 / max 100，cursor=`created_at|id` 复合降序游标）+ `KBAuditLog` / `KBAuditLogListResponse` schema（before/after_state 为 JSON object 非 quoted string，SPEC §3.2）+ security 双声明与 `x-ani-authz`（resource: knowledge_base / action: read / boundary: tenant）；
   - 生成物同步：两侧 pb、四语言 SDK、docs/api、sdk-metadata（6 文件 +34 行）——doc-api/sdks 幂等门禁验证通过。
2. **数据层**（`services/kb-service/migrations/006_kb_audit_log.sql`，新建）：
   - `kb_audit_log` 表：`kb_id REFERENCES knowledge_bases ON DELETE CASCADE`（用户裁定：保留策略 CASCADE，删 KB 审计随之清空）+ `tenant_id REFERENCES tenants ON DELETE CASCADE` + actor_user_id 可空（NULL=系统内部）+ before/after_state JSONB + error_code/error_msg + created_at；
   - RLS 双 PERMISSIVE policy（`kal_self` + `kal_platform_bypass`，对齐 005_kb_permissions 先例——**未复现 B4 OQ3 警告的 §2.3 单 RESTRICTIVE 坑**，见偏差 14）；
   - `idx_kb_audit_log_kb_time (tenant_id, kb_id, created_at DESC, id DESC)`：前两列等值 + 后两列覆盖排序与 keyset 谓词，索引序向扫描无 Sort 节点；
   - GRANT 仅 SELECT/INSERT（insert-only，无 UPDATE/DELETE 权限——日志不可改语义落到 DB 层）。
3. **repository 层**（`app/repositories/audit.py`，新建）：
   - `insert_audit_in_tx`：业务事务内同事务写入（审计与业务变更原子提交/回滚）；全参数化 SQL；
   - `list_logs`：键集分页两条 SQL 分支（有/无 cursor）；cursor 复用 `parse_composite_cursor`（fromisoformat + UUID 双重校验，坏 cursor → InvalidCursorError → 400）。
4. **kb-service servicer**（`app/api/grpc_server.py`）：
   - `ListKBAuditLogs`：get_kb RLS 门控（404）→ `_list_kb_audit_logs` 键集分页 → AuditLogEntry 映射（`_audit_state_json`：asyncpg 默认返回 str 直通，dict 分支防御性保留）；
   - **12 处写路径埋点**（9 个 action）：kb.create/update/delete、kb.permissions.update（B4 回补）、doc.create/parse/delete、doc.reparse，外加失败审计分支——成功路径同事务原子、失败路径业务事务回滚后独立新事务写入（`_record_failure_audit` 尽力而为，审计失败仅 logger.debug 不阻断主流程）；
   - 失败口径：`_AUDITED_FAILURE_CODES = {NOT_FOUND, ALREADY_EXISTS, FAILED_PRECONDITION, RESOURCE_EXHAUSTED}`——INVALID_ARGUMENT（400 参数校验）不记（用户裁定「只记业务失败」）；
   - actor 链路：复用 Gateway 既有注入键 `x-user-id`（非 plan 的 `x-actor-user-id`，见偏差 16），UUID 校验失败归 NULL。
5. **Gateway**（`internal/router/kb_resources.go` + `kb_grpc_client.go`）：
   - 路由 `GET /knowledge-bases/:kb_id/audit-logs`；handler：nil-client 503 → limit 校验（>100 400；≤0 归 20，全 router 统一惯例）→ client 调用 → `kbAuditLogToJSON`（jsonbToRaw 展开 JSONB 字符串为 object；空/"null"→JSON null；invalid JSON→500 不静默丢数据）；
   - `KBGRPCClient` 接口 + 实现补 `ListKBAuditLogs`（tenant 从 Auth 中间件注入）。
6. **测试**：
   - `tests/test_audit_logging.py`（新建，1008 行）：`_AuditConn` fake 追踪事务深度——断言「成功路径同事务、失败路径独立事务」；快照内容、error_code、幂等重放不双写、SQL/args 契约（含 SET LOCAL 前置）真断言非 smoke；
   - gateway `kb_resources_test.go` 4 个新单测（透传/limit>100 400/404/503）+ `kb_sse_test.go` fake 空实现（编译义务）；
   - **B8 E2E**（`_e2e_test_b8_audit.py`，本地服务/服务器存储全链路）31/31：七类审计动作齐全、分页 walk 无重无漏、坏 cursor 400、limit=101 400、limit=0 → 200 默认、随机 kb 404、跨租户 404（RLS 隔离）、DeleteKB 204 + CASCADE（audit-logs 随之 404）；
   - **用户要求的双输出**：E2E 脚本每个 HTTP 调用完整记录请求（method/URL/headers/body）与响应（状态码/pretty JSON），终端 + 时间戳日志文件双通道（`_e2e_b8_audit_20260910_145946.log`）——接口正确性核查凭证。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | ListKBAuditLogs RPC + AuditLogEntry message |
| `api/openapi/services/v1.yaml` | 修改 | GET 路由 + KBAuditLog/KBAuditLogListResponse schema + x-ani-authz |
| `services/kb-service/migrations/006_kb_audit_log.sql` | 新增（未跟踪） | kb_audit_log 表 + 双 PERMISSIVE RLS + 索引 + insert-only GRANT；**必须随批次提交** |
| `services/kb-service/app/repositories/audit.py` | 新增（未跟踪） | insert_audit_in_tx + list_logs（键集分页）；**必须随批次提交** |
| `services/kb-service/app/api/grpc_server.py` | 修改 | ListKBAuditLogs servicer + 12 处埋点 + `_record_failure_audit` + `_audit_state_json`/`_kb_audit_snapshot`/`_doc_audit_snapshot` |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改 | doc_count 改关联子查询（软删立即生效）+ 快照辅助 |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改 | 路由 + handler + kbAuditLogJSON/kbAuditLogToJSON |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改 | 接口 + 实现 ListKBAuditLogs |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改 | 4 个新单测 |
| `services/kb-service/tests/test_audit_logging.py` | 新增（未跟踪） | 1008 行审计专项单测；**必须随批次提交** |
| Makefile + `scripts/validate_services_boundary.py` | 修改 | .venv 扫描排除修复（**与 B8 无关的工具链修复，提交时建议拆独立 commit**） |
| 生成物（pb ×2 侧 / SDK 四语言 / docs/api / sdk-metadata） | 修改 | 契约再生成产物（幂等验证） |

## 设计决策（Design Decisions）

### D1：失败口径只记业务失败（INVALID_ARGUMENT 不记）
- **模糊点：** plan §6.3「失败也记」泛指各 abort 分支——是否含 400 参数校验。
- **选择：** `_AUDITED_FAILURE_CODES` 四码白名单；400 参数校验失败不写审计行。
- **理由：** 参数校验失败无审计价值（请求未触达业务状态）且会被测试/扫描调用刷量。用户显式裁定。E2E 意外验证：409 FAILED_PRECONDITION 的 doc.create 失败确实落审计行（error_code 记录、after_state=null）——「业务失败也记」被真实数据印证。

### D2：JSONB before/after_state 全链路 string 直通
- **模糊点：** proto 用 google.protobuf.Struct 还是 string。
- **选择：** string（DB str → proto str → gateway json.RawMessage），REST 输出时才展开为 object。
- **理由：** 零解析零重序列化；Struct 会在写入时解析 JSON 撑大消息。gateway `jsonbToRaw` invalid JSON → 500 而非静默丢数据。

### D3：迁移 RLS 双 PERMISSIVE（规避 B4 偏差 1 同坑）
- **模糊点：** plan §6.2 原稿单 RESTRICTIVE policy——B4 OQ3 已警告照抄会复现「单 RESTRICTIVE 无 PERMISSIVE 全拒」。
- **选择：** `kal_self` + `kal_platform_bypass` 双 PERMISSIVE，对齐 005 已合入形态。
- **理由：** B4 实证教训直接吸收；plan §6.2 原稿形态未被任何已合入迁移采用（偏差 14 登记）。

### D4：审查拒绝的两项（记录依据）
- **limit≤0 → 默认 20 不改 400：** 全 router 20+ 端点统一 coerce 惯例（`queryInt` + 同模式），E2E 按 `limit=0 → 200` 断言；单改 B8 反而破坏一致性。OpenAPI `minimum:1` 偏差为既有全站现象，非本批引入。
- **doc_count 软删标记 4 处内联不重构：** `parse_status='failed' AND error_message='deleted'` 谓词散布 4 个查询，但行为已被 E2E+单测覆盖，纯重构无行为收益（Karpathy 原则三：只清理自己制造的脏）。

### D5：审查接受并修复的一项（docstring 方向反转）
- `_audit_state_json`/`_list_kb_audit_logs` 两处 docstring 原声称「repository row 是 parsed dict（asyncpg jsonb codec）」——实际全库无 `set_type_codec` 注册，asyncpg 默认返回 **str**，str 分支才是生产路径。注释已更正，防止后续维护者据错误注释「简化」掉 str 分支导致回归。修复后 62 passed 复跑确认。

## 偏差（Deviations vs PRD/UX/SPEC）

**16 项偏差已在 plan §6.8 完整登记并逐项裁定（2026-09-10 回写），此处仅列结构性要点：**

- **偏差 1**：迁移编号 007 → **006**（B5 未实施、原 006 号空缺；B6/B7 的 action 枚举位已在迁移注释预留）。
- **偏差 2**：Response `{items, CursorPageMeta page}` → `{items, next_cursor}` 扁平结构（对齐 citations/sessions 先例）。
- **偏差 3**：失败口径四码白名单（见 D1）。
- **偏差 4**：部分失败分支结构性不埋（kb.create 409 时 KB 行不存在无法满足 FK NOT NULL；kb.delete 404 无 before 可记；doc_row-None 跳过防 FK 违反）。
- **偏差 14**：RLS 双 PERMISSIVE（见 D3，**B4 OQ3 的文档债在 B8 主动规避**）。
- **偏差 15**：B8 提前实施（B5/B6/B7 未开工；B4 埋点由 B8 期间回补）。
- **偏差 16**：actor 注入键复用既有 `x-user-id`（非 plan 新键 `x-actor-user-id`），零新增键、与 sessions 先例一致。

## 权衡（Tradeoffs）

### T1：kb_audit_log 独立表 vs 复用全局 audit_logs
- **备选 A（采纳）：** kb-service 域内新建 kb_audit_log（before/after 快照模型）。
- **备选 B（弃用）：** 复用全局 `audit_logs`（tenant-service 写入、result+details 模型、按月分区）。
- **理由：** Python kb-service 直写全局表构成跨服务共享表边界违规（CLAUDE.md §3）；KB 场景 diff 语义重要、error_code 可索引（plan §6.2 逐维度裁定）；tenant-service 从未写入 kb.* 动作，无重复记录风险。

### T2：成功路径同事务 vs 异步旁路（outbox）
- **备选 A（采纳）：** 业务事务内同事务 INSERT（原子，审计与变更不脱钩）。
- **备选 B（弃用）：** outbox 异步——审计行可能晚于/丢失于业务变更，且失败路径已需独立事务写 error 行，双轨复杂化。
- **失败路径例外：** 业务事务回滚后独立新事务写失败审计行（FK CASCADE 语义已论证——KB 行不在时连失败行也不可能存在，与偏差 4 一致）。

### T3：E2E 借服务器基础设施（复用 B4 T3）
- 本地起 gateway/kb-service，PG/Redis/MinIO 走服务器（10.10.1.66 NodePort）。已知环境干扰：服务器部署 kb-service 的 OutboxDispatcher 共享 dev PG 抢 outbox 行 → 服务器 NATS → stale 消费者 MinIO 404 标 failed——issue-048 已记录，非 B8 缺陷（B8 的 doc.parse 审计在 notify RPC 同步落库，与异步解析解耦）。
- 测试期清理：租户级联清 9 表归零；kb-docs bucket 需 `POST /buckets` 重建（级联清理一并删掉，409 FAILED_PRECONDITION 暴露此前置依赖——顺带验证 D1 失败审计契约）。

## 开放问题（Open Questions）

### OQ1：表无界增长（继承 plan §6.2 / §10.2 Q6）
insert-only 无保留策略/分区。管理面写入频率低（每次写操作 1 行），P1 规模无虞；全局 audit_logs 有按月分区惯例（20260810000300 补分区先例），表达 GB 级或查询变慢时跟进。

### OQ2：失败审计 try/except 同构块 6 处
若 B6/B7 再加写路径，届时抽公共包装器；当前各处 intent 快照构造不同 + `context.abort` 控制流在装饰器里更难读，显式内联可辩护。

### OQ3：分支收口与提交纪律（ship 前必办）
- 工作区有 B8 全量交付（未提交）；分支上另有 2 个未推送本地提交（hotplug + B4），与远端分叉 13 提交（merge-tree 验证零冲突，B4 与远端同题提交文本级一致）；
- **kb_sse.go 幂等键前缀修复（P1，`"sse-"+uuid` 被 kb-service 拒绝）在工作区、不在任何提交内——推送时必须单独提交带上**；
- Makefile/validate 脚本工具链修复建议拆独立 commit（偏差 11/12）；
- untracked 临时文件（`_e2e_*` 脚本/日志、`scripts/_*.py` 60+ 个）勿入暂存区，建议 `.gitignore` 补 `_e2e_*` / `*.log` 模式。

### OQ4：CURRENT-SPRINT / ANI-06 四件套（继承 B4 OQ5）
KB-API 系列沿用 development-records/{批次}.md + README.md 两件惯例；系列收口 PR 时统一决定是否补齐。

### OQ5：B5/B6/B7 后续埋点
`kb.config.update` / `kb.rebuild` 枚举位已预留；届时各自补埋点即用，B8 提前合入不产生冲突。

## 验证命令（已运行）

```
cd repo/services/kb-service
.venv\Scripts\python.exe -m pytest tests -q --ignore=tests/e2e        # 354 passed
python _e2e_test_b8_audit.py                                          # E2E 31/31（系统 python，含双输出日志）
cd repo/services/ani-gateway
go test ./internal/router/ -run "TestKB" -count=1                     # ok
cd repo
make validate-services                                                # 全绿（含 route-contract / SDK 幂等）
make validate-architecture                                            # 全绿
git diff --check                                                      # exit 0
```

> E2E 双输出日志（接口正确性核查凭证）：`services/kb-service/_e2e_b8_audit_20260910_145946.log`——每个 HTTP 调用含请求 method/URL/headers/body 与响应状态码/完整 JSON。
> 审查收口：review-it 5 项 P2（D4/D5 处置：1 修复 + 2 拒绝 + 2 登记）；全项目框架审查（架构分层/跨服务审计体系/类型一致性/索引性能）通过，无需修改。
