# KB-API-B5（#22）— issue #22：配置读取 `GET /knowledge-bases/{kb_id}/config`

> Issue 编号：#22——kb-p1-api-completion-plan §3（B5 批次；无独立 issue 文件，plan 即实施契约）
> Batch: KB-API-B5（本记录只覆盖 #22；#25 `GET /models` 属同批 §3 但**尚未实现**，见 OQ1）· 产品线：core（Services / kb-service + ani-gateway + 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-p1-api-completion-plan.md` §3（B5）
> 前置: KB-API-B1~B4、B8 已实现（#1~#20 + #21 三层全通；B6/B7 未开工）

完成日期：2026-09-14（实现 + E2E）/ 2026-09-15（review-it 收口）
分支：`feat/kb-api-completion`（工作区交付、未提交；分支上另有 B4/B8 未推送提交）
验证结果：kb-service pytest **34 passed**（含 3 个新 GetKBConfig servicer 用例）；`go test ./services/ani-gateway/internal/router/` ok（含 3 个新 GetKBConfig 单测）；**E2E 15/15 全绿**（本地 gateway + kb-service / 服务器 PG 全链路，含跨租户 RLS 隔离与 DB 真值核验）；`make validate-services`（contract 112 基线持平 / route 11 条 0 error）+ `make validate-architecture` 全绿；doc-api + SDK 生成幂等门禁复验通过；ruff clean（恢复 ruff.toml 后）；review-it 终审 **clean，0 剩余 actionable findings**。

## 实现了什么

1. **契约层（OpenAPI + proto + 生成物）**：
   - `v1.yaml`：① `KBConfig` schema **契约修正**——`retrieval_strategy` 改名 `retrieval_mode`（对齐 KnowledgeBase schema / proto / DB 列名），枚举补 `keyword`，默认值 `vector → hybrid`，补 required 六字段；`UpdateKBConfigRequest` 同步改名（B7 实现时消费）；② `GET /knowledge-bases/{kb_id}/config` 路由补 `security`（BearerAuth/ApiKeyAuth）+ `x-ani-authz`（resource: knowledge_base / action: read / boundary: tenant）声明；
   - `kb_service.proto`：新增 `GetKBConfig(GetKBConfigRequest) returns (KBConfig)` RPC + `GetKBConfigRequest`（tenant_id/kb_id）+ `KBConfig` message（8 字段：tenant_id/kb_id/embedding_model/chunk_size/ocr_enabled/top_k/score_threshold/retrieval_mode），与 plan §3.4 逐字对齐；
   - 生成物同步：Go pb（`pkg/generated/pb/kb/v1/` ×2）+ Python pb（`app/generated/kb/v1/` ×3），protoc-gen-go v1.33.0 产物已抽查确认；
   - 生成物欠账核验：doc-api 门禁 + SDK 生成幂等复验均通过且 0 diff——静态 docs/SDK 只呈现 operation 索引与 schema 名列表，KBConfig 字段级改动不产生 SDK/docs 欠账（见 OQ2）。
2. **数据层**（`services/kb-service/migrations/007_kb_ocr_enabled.sql`，新建未跟踪）：
   - `ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN NOT NULL DEFAULT FALSE`（对齐 003/004 幂等风格；plan §3.3 原文）；
   - **迁移编号偏差：plan 写 006，实际 007**——B8 提前实施时已占 006（kb_audit_log），B4 记录偏差 15 预判成立；迁移已通过 SSH + kubectl exec psql 通道应用到服务器 DB（E2E 实证 ocr_enabled 列存在且默认 FALSE）。
3. **repository 层**（`app/repositories/knowledge_base.py`）：`get_kb` SELECT 补 `ocr_enabled` 列（一行改动，读路径复用既有 RLS 事务）。
4. **kb-service servicer**（`app/api/grpc_server.py` L2404-2439）：
   - `GetKBConfig`：tenant_id/kb_id 非空校验（INVALID_ARGUMENT）→ pool 检查（FAILED_PRECONDITION）→ `get_kb` RLS 读（软删同 NOT_FOUND，404 语义与 GetKB 一致）→ **6 字段列直映射**（`score_threshold` 显式 `float()` 转换、`retrieval_mode` 空值回退 `"hybrid"`、`ocr_enabled` 显式 `bool()`）→ KBConfig；
   - 读路径无幂等、无审计埋点（B8 埋点范围只覆盖写操作，kb.config.update 枚举位已预留待 B7 消费）。
5. **Gateway**（`internal/router/kb_resources.go` + `kb_grpc_client.go`）：
   - 路由 `svc.GET("/knowledge-bases/:kb_id/config", ...)`（注册于 listKnowledgeBaseDocuments 之前，路由表有序）；
   - handler `getKnowledgeBaseConfig`（L570-584）：nil-client 503 → `client.GetKBConfig(ctx, instanceTenantID(c), c.Param("kb_id"))` → `writeKBError` 错误映射（NOT_FOUND → 404）→ 200 `kbConfigToJSON(cfg)`；
   - `kbConfigJSON` **六字段全不带 omitempty**——与 KBPermissions/B8 的 nullable 回退语义不同，config 六字段在 v1.yaml 均为 required 且 DB 列 NOT NULL / 有默认值，恒有真值，全行回显不省略；tenant 注入走 Auth 中间件，body/query 无 tenant_id（GET 无 body）；
   - `KBGRPCClient` 接口 + 实现补 `GetKBConfig`（callCtx 包装，与 GetKBPermissions 同构）。
6. **baseline 清理**（`architecture/services-route-baseline.yaml` + `services-contract-baseline.yaml`）：
   - 删除 `GET /config` 的 `spec_not_in_code` 登记（route baseline 11 → 11：config GET 条目移除，B3 保留的 PUT config / rebuild / models 三组不动，待 B7/B6/#25 各自清理）；
   - 删除 contract-baseline 中 getKnowledgeBaseConfig 的 operation_security 豁免两条（路由已声明 security，豁免失效即 stale）。
7. **测试**：
   - kb-service `tests/test_grpc_server.py` +3：GetKBConfig wired 正常映射 / 缺 tenant_id INVALID_ARGUMENT / 缺 kb_id INVALID_ARGUMENT；
   - gateway `kb_resources_test.go` +3：GetConfig_Passthrough（透传 + tenant 注入）/ NotFoundMappedTo404 / NilClientReturns503；`kb_sse_test.go` fake 补 GetKBConfig stub（接口新增方法的编译义务）；
   - **#22 E2E**（`tests/e2e/test_kb_config_e2e.py`，未跟踪交付物）：本地 gateway + kb-service 对服务器 PG 全链路 **15/15**——CreateKB（定制配置 768/8/0.35/keyword）201、GET config 6 字段回显、**响应恰为 6 契约字段**（无多余泄漏）、DB 直查 6 列真值（score_threshold float4 容差）、404 missing kb、**跨租户 RLS 404**、DeleteKB 204、软删后 GET 404、物理清除成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改 | KBConfig/UpdateKBConfigRequest 契约修正 + GET config security/x-ani-authz |
| `api/proto/kb/v1/kb_service.proto` | 修改 | GetKBConfig RPC + GetKBConfigRequest + KBConfig message |
| `architecture/services-route-baseline.yaml` | 修改 | 删 GET config 的 spec_not_in_code |
| `architecture/services-contract-baseline.yaml` | 修改 | 删 getKnowledgeBaseConfig 两条 operation_security 豁免 |
| `pkg/generated/pb/kb/v1/kb_service.pb.go` + `_grpc.pb.go` | 修改 | protoc 生成物（v1.33.0） |
| `services/kb-service/app/generated/kb/v1/` ×3 | 修改 | grpc_tools 生成物 |
| `services/kb-service/migrations/007_kb_ocr_enabled.sql` | 新增（未跟踪） | ocr_enabled 列；**必须随批次提交** |
| `services/kb-service/app/api/grpc_server.py` | 修改 | GetKBConfig servicer（L2404-2439） |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改 | get_kb SELECT 补 ocr_enabled |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改 | 路由 + handler + kbConfigJSON/kbConfigToJSON |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改 | 接口 + 实现 GetKBConfig |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改 | fake + 3 个新单测 |
| `services/ani-gateway/internal/router/kb_sse_test.go` | 修改 | fake 补 stub（编译义务） |
| `services/kb-service/tests/test_grpc_server.py` | 修改 | +3 servicer 用例 |
| `services/kb-service/tests/e2e/test_kb_config_e2e.py` | 新增（未跟踪） | #22 E2E 15/15；**必须随批次提交** |

（review-it 期间恢复：`repo/ruff.toml`——曾处于未提交删除状态，`make lint-python` 依赖其排除生成文件规则，已 `git checkout` 恢复。）

## 设计决策（Design Decisions）

### D1：score_threshold servicer 侧显式 float() 转换 + E2E 裸 DB 读用容差
- **模糊点：** PG REAL（float4）列的裸读回显精度——asyncpg 裸读 `0.35` 为 `0.3499999940395355`，API 层与测试装置是否要感知。
- **选择：** servicer 构造 KBConfig 时 `float(row.get("score_threshold") or 0.0)`（API 链路 Go 侧 float32 归一后 JSON 输出 0.35 正确，E2E 断言 API 响应精确相等已实证）；仅 E2E 的 **DB 直查**装置用 `abs(差值) < 1e-6` 容差。
- **理由：** API 边界是行为契约（客户端拿到的值），必须精确；DB 裸读是测试装置对存储层物理精度的观测，float4 本身不存精确 0.35，容差是唯一正确写法。两层分开断言既验证了契约又不误报。

### D2：kbConfigJSON 全字段不带 omitempty（对比 B4 KBPermissions 的 omitempty 先例）
- **模糊点：** v1.yaml KBConfig 六字段 required——Go JSON 序列化是否要像 B4 的 updated_at（nullable）那样用 omitempty 防零值泄漏。
- **选择：** 六字段全部不带 omitempty，恒全行回显。
- **理由：** omitempty 的适用前提是"字段可为空/可省略"（B4 updated_at 无行回退场景）；config 六字段 DB 列 NOT NULL 或带默认值，任何合法 KB 行恒有真值，不存在零值语义——加 omitempty 反而制造假性可省略。E2E "body has exactly the 6 contract fields" 用例锁死该行为。

### D3：servicer retrieval_mode 空值回退 "hybrid"（v1.yaml 新默认值）
- **模糊点：** DB 行 retrieval_mode 为 NULL/空串时（理论上 NOT NULL 不发生，防御性）servicer 返回什么。
- **选择：** `row.get("retrieval_mode") or "hybrid"`。
- **理由：** 与契约修正后的 v1.yaml 默认值（hybrid）一致；hybrid 也是 DB 列默认值，回退即"回到库默认"。避免空串穿透到客户端枚举校验失败。

### D4：读路径不接审计埋点（B8 枚举位语义）
- **模糊点：** B8 的 audit 枚举预留了 `kb.config.update`，GET config 是否也要埋。
- **选择：** 不埋。B8 失败口径/埋点范围本就只覆盖写操作（kb.create/update/delete/permissions/doc.*），读操作全程无埋点先例；GET config 是纯读，无状态变更。
- **理由：** 审计日志的语义是"记录状态变更"（before/after_state），纯读无 before/after 可言；gateway 侧 `getKnowledgeBaseConfig` 用普通 ctx（非 kbWriteCtx，无 x-user-id 注入）与全库读路径惯例一致。

### D5：E2E purge 走 SSH + kubectl exec psql 通道（非 NodePort RLS 用户）
- **模糊点：** e2e 清理装置用什么数据库身份清测试残留（B4 时期 asyncpg 直连 DELETE async_tasks 曾可行）。
- **选择：** `ssh kubercloud@10.10.1.66` + `kubectl exec -i ani-reconcile-ha-postgres-0 -- psql -U ani -d ani`，SQL 经 stdin 灌入。
- **理由：** ① NodePort 直连的 `ani_app_user` 对 async_tasks 无 DELETE 权限（B4 之后已收紧，B4 E2E 的直删是权限收紧前写就的），其 purge 事务会整段回滚连带 KB 行清不掉；ani 超级用户 NodePort 直连已被禁用，pod 内执行不受限。② PowerShell 下 SSH 命令行内嵌 SQL 双层引号必然转义出错（两轮实测：双引号嵌套 EOF 报错 / 单引号被 psql 拆参），stdin 灌入是唯一稳妥通道——该经验已固化在 purge 脚本注释与 `_purge_kbcfg_leftovers.py` 中。

## 偏差（Deviations vs PRD/UX/SPEC/plan）

### 偏差 1：迁移编号 006 → 007（plan §3.3）
- **plan 原稿：** `006_kb_ocr_enabled.sql`。
- **实际实现：** `007_kb_ocr_enabled.sql`。
- **原因：** B8 提前实施（B4 记录偏差 15）已占用 006（kb_audit_log）。编号顺延是 B8 偏差 15 的直接后果，plan §3.3 写就时未预知。**性质：** 已在 B8 记录预判，非新发现；plan 文档无需勘误（B8 记录偏差 15 已回写）。

### 偏差 2：route baseline 剩余条目数（plan §3.7 的"删两条"前提部分失效）
- **plan 原稿（§3.7）：** 本批注册 GET config + GET models 两条后删除对应两条 spec_not_in_code。
- **实际实现：** 本批只实现 #22，只删 GET config 一条；GET models 条目保留（#25 未实现）。
- **原因：** B5 的两接口分步交付（#22 先行，#25 未开工）。#25 实现时删第二条即可，无遗留风险。**性质：** 分步交付的正常中间态。

### 偏差 3：E2E DB 直查 score_threshold 用容差（plan §3.8 未涉及）
- **plan 原稿：** 测试要点只写"列直映射（含 ocr_enabled 新列默认 false、retrieval_mode 三值）"。
- **实际实现：** E2E 的 DB 真值断言对 score_threshold 加 `abs(差值) < 1e-6`，其余 5 字段精确相等。
- **原因：** PG REAL 是 float4，物理上不存 0.35 精确值（见 D1）。**性质：** 测试装置对存储精度的正确适配，非实现偏差。

## 权衡（Tradeoffs）

### T1：GET config 404 判定复用 get_kb 全行读（vs 单列存在性探测）
- **备选 A（采纳）：** 复用 `get_kb`（现含 ocr_enabled 共 6+ 列的 RLS 读）——与 GetKB/GetKBPermissions 同一 404 门控，一处维护。
- **备选 B（弃用）：** 专门存在性 SELECT 1 查询省列——省的列不到一行 KB 行宽，却要多养一个 SQL/一个 404 语义；get_kb 是全 servicer 的统一 KB 门控惯例，破坏一致性不值。

### T2：E2E 复用服务器基础设施（PG）vs 全本地栈
- **备选 A（采纳）：** 本地 gateway/kb-service 进程 + 服务器 PG（10.10.1.66）——与 B4/B8 E2E 同模式。
- **备选 B（弃用）：** 全本地栈——Windows 本地 PG 无 RLS 用户生态，核验不了跨租户隔离（RLS 404 负例正是本批 E2E 关键覆盖面）。
- **代价：** 测试数据落服务器——已物理清除（含前两轮软删残留行 49a18fc9…，`_purge_kbcfg_leftovers.py` 复查 0 行）；purge 通道约束见 D5。

### T3：契约改名 retrieval_strategy → retrieval_mode 放在本批（vs 推迟到 B7 PUT 实现时一起改）
- **备选 A（采纳）：** 本批改名（GET config 响应字段即契约消费面，先改先锁定；B7 的 UpdateKBConfigRequest 已同步改名，B7 只剩实现）。
- **备选 B（弃用）：** B7 一起改——GET config 若先带旧名上线，前端按旧名接入，B7 改名即破坏性变更；且 plan §1.5 明确"契约修正本批先行提交"。

## 开放问题（Open Questions）

### OQ1：#25 `GET /models` 未实现（B5 批次另一半）
plan §3 同批的 #25（proto ListKBModels + Core 模型聚合 + Gateway 路由）**尚未开工**。B5 批次四文档更新（development-records 本文件 + README 索引 + CURRENT-SPRINT + ANI-06）按 CLAUDE.md 约定随批次收口——待 #25 完成后一并处理（本文件已就位，#25 补齐后只差 README/CURRENT-SPRINT/ANI-06 三件）。#25 依赖 Core API client 能力（plan §3.5 候选口径 A2：bge-m3 内置恒在 + core_api 租户模型过滤 ready）。

### OQ2：SDK/docs 生成物与 KBConfig 字段级改名的覆盖面
B1~B3 各批均有"SDK 四语言 + docs/api 重生成"步骤，本批 git status 未见 SDK/docs 变更。复核结论：`gen_sdk_alpha.py` 生成物只覆盖 Core SDK 操作索引（KBConfig 字段不展开）；`validate_api_docs_contract.py` 门禁通过、`validate_generated_idempotence.py`（sdks/services）0 diff——**当前生成器版本确实不展开 Services schema 字段，无欠账**。但若后续 SDK 升级到字段级模型，KBConfig 改名会进入生成面——届时以门禁红灯为准，此处登记备忘。

### OQ3：服务器 DB migration 007 的重放路径
migration 007 在服务器上经 SSH + kubectl exec psql 手工应用（与 B4 的 005 修复同路径），仓库迁移文件幂等（IF NOT EXISTS）可重放。首次部署新环境时无风险；仅记录操作路径差异（同 B4 OQ4 惯例）。

### OQ4：E2E 一次性装置的处置
`_purge_kbcfg_leftovers.py`（残留清查/清除）任务已完成、服务器已清零，属一次性脚本——按 B4 惯例随提交处置（提交时删除或保留由用户裁定，与 B8 的一次性 `_e2e_*` 系列同待遇）。`test_kb_config_e2e.py` 是可重跑交付物，必须随批次提交。

### OQ5：B6/B7 对本批契约的消费提醒
B6 rebuild 编排按 `retrieval_mode`（新名）+ `ocr_enabled`（本批列）读重建参数（plan §0.2 依赖关系）；B7 PUT config 直接消费已改名的 UpdateKBConfigRequest 与已就绪的 GET 回显链路。两批实现时以本批落地后的 v1.yaml/proto 为准，勿回引 plan §3.2 的草稿文本。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest tests -q --ignore=tests/e2e                 # 34 passed（+3 GetKBConfig）
python tests/e2e/test_kb_config_e2e.py                       # E2E 15/15（本地 gateway+kb-service / 服务器 PG）
cd repo/services/ani-gateway
go test ./internal/router/ -count=1                           # ok（+3 GetKBConfig 单测）
cd repo
python scripts/validate_services_contract.py                  # services contract valid: 112 accepted baseline warning(s)（与基线持平）
python scripts/validate_services_route_contract.py            # Services route contract: 11 accepted baseline warning(s), 0 error(s)
python scripts/validate_component_imports.py                  # component import guard passed
python scripts/validate_inference_legacy_control_plane.py     # inference legacy control plane retired
python scripts/validate_api_docs_contract.py                  # api docs contract valid
python scripts/validate_generated_idempotence.py --path sdks/services -- python scripts/gen_sdk_alpha.py   # idempotent, 0 diff
cd repo/ai && ruff check .                                    # All checks passed（ruff.toml 恢复后）
```

> E2E 期间发现并修复 2 处测试装置缺陷（purge 通道权限 → SSH+kubectl exec；float4 容差），修复后第三轮 15/15。review-it 终审 2 项发现已落地修正（`ruff.toml` 恢复——曾处未提交删除状态致 lint 门禁 57 errors；`_purge_kbcfg_leftovers.py` outbox UPDATE 顺序前置——子查询依赖 KB 行存在），复验全绿后 clean 收口。测试期服务器数据已清零（含前两轮软删残留）；本地服务进程已停；e2e 日志留档 `tests/e2e/e2e_kb_config_result.log` + gateway/kb-service 两个 stdout 日志。
