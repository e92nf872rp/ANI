# KB-API-B7 — issue #23：KB 配置更新 `PUT /knowledge-bases/{kb_id}/config`

> Issue 编号：#23——kb-p1-api-completion-plan §5（无独立 issue 文件，plan 即实施契约）
> Batch: KB-API-B7 · 产品线: core（Services / kb-service + ani-gateway + 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-p1-api-completion-plan.md` §5（B7）
> 前置: KB-API-B1~B6、B8 已实现（B7 复用 B6 `_trigger_rebuild_in_tx` 与 rebuilding 互斥矩阵；B5 遗留 #25 models 同分支叠加）

完成日期：2026-09-17
分支：`feat/kb-api-completion`（工作区交付、未提交；B5/B6/B8 及 JetStream 迁移轮改动同分支叠加）
验证结果：kb-service pytest **445 passed**（B6+JetStream 基线 425 → B7 净增 20：test_update_kb_config 18 + test_audit_logging 快照键集扩展 2）；gateway `go test ./internal/router/ -run TestKB` ok；**E2E 44/44 全绿**（`tests/e2e/test_kb_update_config_e2e.py`，本地三服务 + 服务器基础设施 NodePort；首轮 40/42 → 二轮 42/42 → review 修复 + 回归防护后 44/44）；`validate-architecture` 通过（Windows 下直接跑底层两脚本）；review-it clean（3 findings 全部修复 + E2E 回归防护）。

## 实现了什么

1. **契约层（proto + OpenAPI + 生成物）**：
   - proto 新增 `rpc UpdateKBConfig(UpdateKBConfigRequest) returns (UpdateKBConfigResponse)`：六字段**三态**（`optional` + `google.protobuf.BoolValue`）——absent = 保持现值，显式携带（含零值/显式 false）= 变更候选；注释固化 200 同步语义 + embedding_model/chunk_size 变更同事务触发全库重建（rebuild_task 附在响应中）；
   - `UpdateKBConfigResponse { KBConfig config; AsyncTaskRef rebuild_task; }`——rebuild_task 仅在触发重建时设置（线上未设置 = 契约 nullable）；
   - v1.yaml `PUT /knowledge-bases/{kb_id}/config`（200 allOf[KBConfig + nullable rebuild_task] + 400/401/403/404/409）+ `UpdateKBConfigRequest` schema（required idempotency_key uuid；六可空字段带值域）；**契约修正**：响应由早期草案 KnowledgeBase 改为 KBConfig + rebuild_task（plan §5.2/§5.5 裁定）；
   - 手写 pb 三文件（kb_service_pb2.py / .pyi / _pb2_grpc.py）+ Gateway `pkg/generated/pb/kb/v1` 两 .go 生成物 + 四语言 SDK（Go/Java/Python/TS）/ docs/services.html / sdk-metadata 同步（updateKnowledgeBaseConfig 入幂等集）。
2. **kb-service 仓储**：`knowledge_base.py` 新增 `update_config_in_tx`——**显式部分更新**（patch 键即 SET 列，absence = keep current，与 update_kb 的 COALESCE 语义相反）；`_CONFIG_COLUMNS` frozenset 白名单为动态 SET 的 SQL 注入守卫；RETURNING 全行含 **doc_count 计算子查询**（与 get_kb 逐字一致：活跃文档 = 排除 `parse_status='failed' AND error_message='deleted'` 软删）。
3. **servicer `_update_kb_config`（grpc_server.py L2793-3107）**：uuid 校验 → 显式携带字段值域校验（chunk_size 1-8192 / top_k 1-20 / score_threshold 0-1 / retrieval_mode 枚举 / embedding_model 非空；**模型目录校验推迟 #25**）→ 单事务七步原子提交：
   - 3a 幂等回放（find_by_idempotency_key task_type=kb.config.update；recorded result 带 config + rebuild_task，回放时重建 ref 原样恢复）；
   - 3b KB 门控（get_kb RLS 404；rebuilding → FAILED_PRECONDITION，复用 B6 互斥）；
   - 3c **三态变更检测**：HasField = 显式携带；值比较 = 有效变更（float32 proto vs float8 DB 用 `math.isclose(rel_tol=1e-6, abs_tol=1e-9)` 容差比较防 float4 回传误判）；全无效变更 → 400 "no effective change"；embedding_model/chunk_size 有效变更置 `rebuild_needed`；
   - 3d update_config_in_tx（patch 键才 SET）；
   - 3e `rebuild_needed` → 复用 B6 `_trigger_rebuild_in_tx`（**派生幂等键 `f"rebuild:{uuid}"`**：配置回放永不重触重建）：active→rebuilding 条件转置 + kb.rebuild 审计 + SAVEPOINT UNIQUE 自愈 + outbox kb.rebuild（payload kb_id/tenant_id/task_id 三字段）；
   - 3f 审计 kb.config.update（before=门控行快照 / after=RETURNING 行快照，`_kb_audit_snapshot` 投影含 ocr_enabled）；
   - 3g async_tasks 幂等记录（create pending → complete_task_in_tx，result = {config: updated, rebuild_task: ref|None}；毒键自愈同 UpdateKB）。
   失败路径 `_record_failure_audit`（404/409/412 业务码记审计；INVALID_ARGUMENT 事务前已拒不记）。
4. **Gateway（kb_resources.go + kb_grpc_client.go）**：路由 `PUT /knowledge-bases/:kb_id/config` + handler——nil-client 503 → BindJSON 400 → idempotency_key uuid 校验 400 → **六字段指针直传**（`*string`/`*int32`/`*wrapperspb.BoolValue`——JSON absent = nil 指针 = proto 未设置 = keep current；显式 false 经 BoolValue wrapper 与 absent 区分）→ 响应展平 allOf（KBConfig 六字段 inline + `RebuildTask *asyncTaskRefJSON json:"rebuild_task,omitempty"`）；`KBGRPCClient.UpdateKBConfig` 补接口与实现。
5. **测试**：
   - 单测 `test_update_kb_config.py` **18 项**（新增未跟踪）：三态矩阵（absent/零值/显式 false）、值域拒收、no effective change 400、rebuild_needed 检测、派生键 SQL/args 断言、replay 回放、毒键、审计快照/键集；
   - E2E `test_kb_update_config_e2e.py`（新增未跟踪，8 组 **44 项**）：P0 主路径（非 embedding patch 200 回显 + rebuild_task absent + DB 行 + 审计 before/after + async_tasks completed + result + **无 kb.rebuild 工件** + **P0d-2 doc_count 活跃计数回归防护**：种 2 条 ready 文档 → 任务 result config.doc_count==2 + 审计 after.doc_count==2）/ P1 三态保持 / P2 embedding 联动（200+ref+转置+派生键任务+outbox payload+双审计+result 内嵌）/ P3 chunk_size 联动 / P4 幂等回放（同 task_id、双键计数零新行、KB 保持 active）/ P5 门控（404/跨租户 404/rebuilding 409+失败审计/全同值 400）/ P7 毒派生键（409 + **config 连带回滚** + KB 回 active + 无 outbox + 毒行不动）/ P8 网关校验（空键/非 uuid/空 patch/坏 JSON）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | UpdateKBConfig RPC + Request（三态）/Response |
| `api/openapi/services/v1.yaml` | 修改 | PUT config path + UpdateKBConfigRequest schema |
| `architecture/services-route-baseline.yaml` | 修改 | 删 PUT config 条目 |
| `architecture/services-contract-baseline.yaml` | 修改 | 删 UpdateKBConfig 相关豁免 |
| `services/kb-service/app/generated/kb/v1/*`（3 文件） | 修改 | 手写 pb 同步 |
| `pkg/generated/pb/kb/v1/kb_service.pb.go` + `_grpc.pb.go` | 修改 | Gateway 生成物 |
| `sdks/services/`（6 文件）+ `docs/api/services.html` | 修改 | 四语言 SDK/docs 生成物同步 |
| `services/kb-service/app/api/grpc_server.py` | 修改（+300） | `_update_kb_config` 七步 + `import math` isclose 容差 + `_kb_audit_snapshot` 补 ocr_enabled |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改（+65） | `update_config_in_tx` + `_CONFIG_COLUMNS` 白名单 + RETURNING doc_count 计算子查询 |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改（+120） | 路由 + handler（指针三态直传 + BoolValue wrapper）+ `updateKBConfigJSON` omitempty |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改（+40） | `KBGRPCClient.UpdateKBConfig` |
| `services/ani-gateway/internal/router/{kb_resources_test.go, kb_sse_test.go}` | 修改 | handler 单测 + fake 空实现 |
| `services/kb-service/tests/test_update_kb_config.py` | **新增（未跟踪）** | 18 项单测；**必须随批次提交** |
| `services/kb-service/tests/e2e/test_kb_update_config_e2e.py` | **新增（未跟踪）** | 44 项 E2E；**必须随批次提交** |
| `services/kb-service/tests/test_audit_logging.py` | 修改 | fake `_kb_row` 补 ocr_enabled + 快照键集断言扩展 |
| `services/kb-service/tests/{test_grpc_server.py, test_grpc_wiring.py}` | 修改 | P1_RPCS 常量 + wiring 装配 |

> 工作区另有 B5/B6/B8/JetStream 轮遗留改动与大量 `_` 前缀临时脚本，**不属 B7 记录范围**，提交时按所属批次处置。

## 设计决策（Design Decisions）

### D1：三态用 proto3 `optional` + BoolValue wrapper，而非 repeated/sentinel 值
- **模糊点：** JSON absent 与显式零值/false 如何在 proto 层区分。
- **选择：** `optional` 标量字段（HasField 判定）+ `google.protobuf.BoolValue ocr_enabled`（bool 无零值可区分——proto3 optional bool 理论可行但 wrapper 是仓库既有先例且跨语言 SDK 序列化稳定）。
- **理由：** plan §5.2 已裁定 BoolValue；Gateway 侧 `*bool` → `wrapperspb.Bool(*v)` 直传，JSON absent = nil 指针 = 未设置。E2E P1（absent 保持现值）+ P0c（显式 false 生效）双向锁死。

### D2：变更检测 = HasField（显式携带）AND 值比较（有效变更），全无效 → 400
- **模糊点：** 显式携带同值算不算有效请求。
- **选择：** 双重条件——携带同值不进 patch；patch 空 → 400 "no effective change"（与 plan §5.3 "no-op 拒绝"一致）。
- **理由：** 防语义歧义（客户端无法区分"没改"与"改失败"）；400 而非 200 幂等回显可让客户端发现自身 bug。
- **测试印证：** P5d 全同值 400；单测三态矩阵 18 项。

### D3：rebuild 联动复用 B6 `_trigger_rebuild_in_tx`，派生幂等键 `f"rebuild:{uuid}"`
- **模糊点：** 配置变更触发的重建用什么幂等键；是否独立 RPC 走 RebuildKB。
- **选择：** 同事务内调 `_trigger_rebuild_in_tx`，idem_key = `f"rebuild:{idem_key}"`（44 字符，与原键必不同，async_tasks UNIQUE(tenant_id, idempotency_key) 两列约束无碰撞）。
- **理由：** plan §5.4 已定嵌套键；配置回放（3a 命中 recorded result）直接返回，永不触达 3e——派生键是第二道防线（毒键场景 P7 证明其回滚价值：派生键毒 → abort 连带回滚 config UPDATE）。payload 取 RETURNING 行（**新** embedding 设置），非门控行旧值——重建消费侧 `get_kb` 回查最新配置，双保险。

### D4：score_threshold 比较用 `math.isclose` 容差，而非 `!=`
- **模糊点：** proto `optional float`（32 位）与 DB float8（64 位）直接比较。
- **选择：** `math.isclose(request.score_threshold, float(kb_row.get(...)), rel_tol=1e-6, abs_tol=1e-9)`。
- **理由：**（review-it F2）migration `DEFAULT 0.3` 行回传相同值时 float32 舍入路径可能产生 1e-8 级差值 → `!=` 误判"有效变更"→ 非预期触发 rebuild_needed 或 no-op patch 通过。容差比较对齐人类语义"没改就是没改"。

### D5：`update_config_in_tx` RETURNING 的 doc_count 用计算子查询（对齐 get_kb），而非物理列
- **模糊点：** knowledge_bases 物理列 doc_count 全仓库无 SET 维护语句恒为 0；读路径（get_kb）用运行时子查询算活跃文档数（排除软删）。
- **选择：** RETURNING 内联与 get_kb **逐字一致**的计算子查询。
- **理由：**（review-it F1 major）RETURNING 行是审计 after 快照与任务 result 的唯一数据源；引用物理列则 doc_count 恒 0，与 before 快照（来自 get_kb 计算值）语义割裂。E2E 的 KB 恰好 0 文档时巧合掩盖此缺陷——**P0d-2 回归防护**（种 2 文档断言 result/审计 doc_count==2）是该类"值来源错误"缺陷的唯一防线（fake conn 单测结构性无法检出）。

### D6：rebuild_needed 仅由 embedding_model / chunk_size 驱动
- **选择：** 其余四字段（ocr/top_k/score_threshold/retrieval_mode）为查询时或下次解析设置，不失效既有向量。理由：plan §5.1 契约；ocr_enabled 影响未来解析行为（存量文档需 ReparseDocument 逐个补救，plan 未要求自动联动）。

## 偏差（Deviations vs Plan/SPEC）

1. **OpenAPI PUT config 无 security/x-ani-authz 声明**（GET config 在 B5 已加）：用户裁定**本次不加**（同批 rebuild/PUT permissions 均未加；鉴权一致性随系列收口 PR 统一处理，与 B4 OQ5 / B6 偏差 #1 同族惯例）。
2. **embedding_model 接受任意非空字符串**（无模型目录校验）：plan §5.3 值域表注明模型目录（ListKBModels）推迟 #25（B5 OQ1 延续）；校验缺口由 #25 models 批次收口。
3. **响应契约 doc_count 不在 PUT 200 顶层**（KBConfig 六字段 + rebuild_task）：doc_count 体现在任务 result 内嵌 config（update_config_in_tx RETURNING 含之）。E2E 断言初版误按"响应含 doc_count"写，修正为查任务 result（P0d-2a）。
4. **无新增 deviation 于重建联动语义本身**：plan §5.1/§5.4 的嵌套键、同事务、rebuild_task 附 200 均按契约实现。

## 权衡（Tradeoffs）

### T1：三态显式部分更新 vs UpdateKB 的 COALESCE "空保持现值"
- 备选 A（采纳）：proto optional 三态 + patch 键 SET。理由：显式携带零值（如 score_threshold=0）在 COALESCE 语义下无法表达（0 是空值假象）；配置场景客户端常发全量表单，absent/显式区分是刚需。
- 备选 B（弃用）：复用 update_kb 的 COALESCE。会把"置 0/false"变成不可能操作，语义陷阱。
- 代价：两套更新语义并存（UpdateKB vs UpdateKBConfig），servicer 3c 增加六字段逐一比较逻辑——由三态单测矩阵锁死。

### T2：fake conn 单测盲区 vs E2E 真库防线
- 备选 A（采纳）：单测覆盖协议/分支/SQL 形状 + E2E 真库覆盖值来源/联动/回滚。理由：fake 对 `UPDATE ... RETURNING` 整行 echo 不执行真实 SQL——"值来源错误"（物理列 vs 计算子查询）类缺陷 fake 结构性不可检出（F1 即漏过 18 项单测）；P0d-2 作为补偿防线固化。
- 备选 B（弃用）：为 repo 层引入真 PG 测试夹具。仓库无 testcontainers 基建，超出本批范围。

### T3：rebuild 联动同事务 vs 两阶段（先 200 再异步触发重建）
- 备选 A（采纳）：同事务七步原子。理由：config 更新与重建触发要么全成要么全无（P7 毒键连带回滚证明价值）；两阶段会留"配置已改但重建失败"中间态，需补偿逻辑。
- 备选 B（弃用）：200 后由客户端再调 POST /rebuild。UX 多一步交互且失败窗口不可控，违背"保存即生效"契约。

## 开放问题（Open Questions）

### OQ1：#25 models——embedding_model 无目录校验（B5 OQ1 / 本批偏差 #2 延续）
`GET /knowledge-bases/{kb_id}/models` + CreateKB/UpdateKBConfig 的 embedding_model 白名单校验均待 #25 批次。当前接受任意非空字符串，错误模型名在首次 rebuild/parse 时由 rag-engine 嵌入端暴露（HTTP 400），错误位置偏晚。#25 实现时须同步回补 UpdateKBConfig 的校验路径。

### OQ2：ocr_enabled 变更不联动存量文档重解析（D6 后续）
ocr_enabled 从 false→true 只影响**未来**上传文档；存量文档需手动 ReparseDocument 逐个补救。UX 是否需要"OCR 变更提示批量重解析"待产品确认；若做，可在 UpdateKBConfig 响应或 UX 提示层引导，不破坏当前 API 契约。

### OQ3：文档更新（CURRENT-SPRINT / ANI-06）继承系列惯例推迟
KB-API 系列沿用「development-records/{批次}.md + README.md 索引」两件惯例（B4 OQ5 / B8 OQ4 / B6 OQ4 确认）；系列收口 PR 时统一决定四件套补齐。

### OQ4：score_threshold 容差值 1e-6 的长期锚定
isclose 容差为经验值（覆盖 float32↔float8 舍入路径）；若未来 score_threshold 值域细化到小数点后 7 位以上精度语义，需重估容差。当前值域 0-1、UI 步进 0.05，余量充足。

## E2E 全链路验证轮 + review-it 收口（2026-09-16/17）

> 触发：用户指令「请你对修改配置接口进行端到端测试」（三应用服务仅本地运行——gateway :8080 / kb-service :8002+gRPC :50053；基础设施直连服务器 10.10.1.66 NodePort——PG :30945 / MinIO :30900 / NATS :31062 / Redis :30453 / Milvus :31930，rag-engine 本轮未启动——PUT config 主链路无解析依赖），随后 `/review-it` 审查。

### E2E：三轮修复时间线

- **首轮 40/42**：P0d 失败——上轮审查误把 doc_count 从 RETURNING 当死负载删除 → 审计 after.doc_count 缺失（None ≠ 0）；P4b 失败——**断言写错**（`_count_tasks_by_key` 按精确键计数只数 config 行，派生键 `rebuild:` 行不计入；日志 1→1 本身已证明无新增）→ 改 `IN ($2, $3)` 双键计数。
- **二轮 42/42**：P0d 修复暴露第二缺陷——`_kb_audit_snapshot` 投影 10 字段漏了六个可改字段之一的 ocr_enabled → 补投影 + 单测同步断言。
- **review-it 3 findings 后 44/44**：
  - **F1（major，真实回归）**：恢复 RETURNING doc_count 时用错值来源——物理列恒 0 而 get_kb 用子查询；E2E KB 恰好 0 文档巧合掩盖 → RETURNING 改计算子查询 + **P0d-2 回归防护**（种 2 条 ready 文档断言 result/审计 doc_count==2）；种子 SQL 列名按 init_schema.sql 核对（file_name/file_type/file_size_bytes/storage_path/checksum_sha256/parse_status）。
  - **F2（minor）**：score_threshold `!=` 直接比较 float32/float8 → isclose 容差（D4）。
  - **F3（nit）**：test_audit_logging fake `_kb_row` 键集与真实 RETURNING 列集漂移 → 补 ocr_enabled + 键集断言。
- **验证**：E2E 44/44；单测 445 passed；`validate-architecture` 通过（Windows 下 `make` 行内 env 赋值失败，直接跑 `scripts/validate_component_imports.py --root .` + `scripts/validate_inference_legacy_control_plane.py` 等价补跑，与 B3 OQ4/B6 偏差 #4 同类先例）；review-it 重跑 clean。
- **接口结论**（已答复用户）：接口正常——200/400/401/404/409 契约映射正确、三态保持、replay 零新工件、联动 rebuild 四工件（转置/审计/任务/outbox）与 config 原子同事务、毒派生键 409 全回滚、网关校验层完备。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest tests -q --ignore=tests/e2e     # 445 passed（B6+JetStream 基线 425 + B7 净增 20）
python -m pytest tests/e2e/test_kb_update_config_e2e.py -q   # 44/44（本地三服务 + 服务器 PG/NATS/Redis；OutboxGuardThread 防服务器 dispatcher 代发）
python -m compileall -q app                       # 语法门禁
cd repo
go test ./services/ani-gateway/internal/router/... -run TestKB   # ok（含 updateConfig handler 单测）
python scripts/validate_component_imports.py --root services/kb-service
python scripts/validate_inference_legacy_control_plane.py         # validate-architecture 等价
# SDK 幂等门禁：四语言 SDK/docs 重生成后 MD5 幂等验证（B7 零漂移）
```
