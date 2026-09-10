# KB-API-B2 — issue-041 契约层：chunks / sessions / messages / citations 契约 + proto + 生成物

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-041-b2-contract-chunks-sessions-citations.md`
> Batch: KB-API-B2 (contract phase) · 产品线: core（Services 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

完成日期：2026-09-02
分支：`feat/kb-api-completion`（基于 main `963bc88`，B1 issue-040 改动同分支叠加）
验证结果：validate-services-contract（112 accepted baseline warnings，含新 3 条豁免）/ validate-yaml / validate-spec-split / validate-doc-api / validate-doc-entrypoints / validate-sdk-beta / 零漂移双门禁（SDK-docs + Console schema）/ validate-architecture / `git diff --check` / `go build ./pkg/... ./services/ani-gateway/...` / model-service + inference-service go test 全绿；kb-service compileall 通过；kb pb 两侧 Python import smoke 验证（KBChunk 13 fields / KBCitation 10 / KBSessionMessage 9 / 3 RPC stub 正确生成）。route-contract 5 条 `spec_not_in_code`（B1 2 条 + B2 新增 3 条）为 SPEC §4.2 规定的同 PR 解除项（issue-046 Gateway 层）。review-it 收口审查 clean（0 accepted/actionable findings）。boundary 门禁本地环境噪音 3 项见 OQ4（CI 干净 checkout 不存在）。

## 实现了什么

为 KB-API-B2 批次（SPEC §4.3 #11 分块明细、#17 会话消息、#18 会话删除、#15 引用溯源增强）补齐契约层：`api/openapi/services/v1.yaml` 新增 3 个 path（GET `listKnowledgeBaseDocumentChunks` / GET `listKnowledgeBaseSessionMessages` / DELETE `deleteKnowledgeBaseSession` 204 幂等）+ 5 个新 schema（KBSourceChunk / KBChunk / KBChunkListResponse / KBSessionMessage / KBSessionMessageListResponse）+ 既有 `KBCitation` 追加 `message_id`/`session_id`（uuid nullable，该接口从未发布无兼容负担）；`api/proto/kb/v1/kb_service.proto` 新增 3 RPC（ListDocumentChunks / GetSessionMessages / DeleteSession→Empty）+ 7 message + KBCitation field 9/10，分页请求复用 `common.v1.CursorPageRequest`；3 个新 operationId 登记 `services-contract-baseline.yaml` `operation_security` 豁免；buf 生成 Go 侧 pb + grpc_tools 生成 Python 侧 pb；下游生成物（四语言 SDK、docs/api、Console schema.d.ts）全部重生成。纯新增契约，无破坏性变更，不含任何 servicer/Gateway 实现；未触碰 Core API `v1.yaml`，未回溯改造 `KBQueryResponse`（范围纪律）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改（4 处纯新增） | `KBCitation` +`message_id`/`session_id`（L849-850）；5 个新 schema 插入 KBSessionListResponse 与 UpdateKnowledgeBaseRequest 之间（L877-930）；3 个新 path：GET `.../documents/{doc_id}/chunks`（limit default 50 / 1–100 + cursor + chunk_type enum child\|parent\|doc_summary）、DELETE `.../sessions/{session_id}`（204，幂等：会话不存在也 204，404 仅当 KB 不存在）、GET `.../sessions/{session_id}/messages`（limit default 100 / max 100 + `created_at\|id` 复合键升序游标） |
| `api/proto/kb/v1/kb_service.proto` | 修改 | 3 RPC 追加在 UpdateKBPermissions 之后；KBCitation field 9/10（空串=未知）；7 message 追加在 UpdateKBPermissionsRequest 之后：KBChunk（13 字段，custom_metadata JSONB string）、ListDocumentChunksRequest/Response、KBSessionMessage（9 字段，source_chunks JSONB string，duration_ms int64）、GetSessionMessagesRequest/Response、DeleteSessionRequest——请求均含 tenant_id/kb_id 并复用 `common.v1.CursorPageRequest` |
| `architecture/services-contract-baseline.yaml` | 修改 | 追加 `listKnowledgeBaseDocumentChunks` / `listKnowledgeBaseSessionMessages` / `deleteKnowledgeBaseSession` 三条 `operation_security` 豁免（reason 与既有条目同文） |
| `pkg/generated/pb/kb/v1/kb_service{,_grpc}.pb.go` | 重新生成 | buf 生成：KBChunk / ListDocumentChunksRequest / KBSessionMessage / GetSessionMessagesRequest / DeleteSessionRequest struct + KBCitation GetMessageId/GetSessionId + client/server/handler 方法 |
| `services/kb-service/app/generated/kb/v1/` ×3 | 重新生成 | grpc_tools 生成：pb2.py / pb2.pyi / pb2_grpc.py（3 message 全字段 + 3 RPC stub，`__init__` 内 unary_unary 正确生成） |
| `services/kb-service/app/generated/common/v1/common_pb2.py{,i}` | 重新生成 | 附带 IdempotentResult 欠账追平（proto 源 L30-32 与 Go 侧 pb 均已存在，见「范围外良性同步」） |
| `sdks/services/*` ×7 + `sdk-metadata.json` | 重新生成 | 四语言 OPERATIONS/PATHS/SCHEMAS 增量 |
| `docs/api/{index,services}.html` | 重新生成 | Services Operations 117→120 |
| `frontends/console/src/api/schema.d.ts` | 重新生成 | 3 操作 + 5 schema + operations 定义（+200 行） |

## 设计决策（Design Decisions）

### D1：`custom_metadata` 定为 `object nullable`（Plan 简写与 AC/仓库惯例的冲突消解）
- **模糊点：** Plan §5.1 对 KBChunk.custom_metadata 简写 `{ type: object }`，而 issue AC #4 明确要求 `custom_metadata object nullable`；仓库现有惯例（v1.yaml KBDocument/KBDocumentDetail 的 custom_metadata，`additionalProperties: true, nullable: true`）为第三种写法。
- **选择：** `{ type: object, additionalProperties: true, nullable: true }`，与仓库惯例逐字对齐。
- **理由：** 查 DB schema（`002_kb_chunks.sql:27` `custom_metadata JSONB DEFAULT '{}'`，列无 NOT NULL）确认可空；REST 层 nullable 同时表达"客户端未设置元数据时字段缺省/null"，proto 侧空 JSONB string 经 Gateway 反序列化为 null 是自然映射。Plan 简写属笔误级歧义，SPEC §3.2 的 JSONB 链路定义（DB JSONB → proto string → Gateway Unmarshal）为准。

### D2：分页游标双形态——chunks 单列 id、messages/citations 复合 `created_at|id`
- **选择：** chunks 用 PK 单列 id ASC keyset（`id > $cursor`）；messages 用复合游标（`(created_at, id) > ($ts, $id)`），REST cursor description 显式写明复合键形态。
- **理由：** SPEC §4.4 冻结：chunk id 为入库序列分配，天然单调且为 PK，单列游标最稳；kb_messages.id 为 gen_random_uuid 随机值、user/assistant 分属不同事务，created_at 可能同秒，id tie-break 必须——复合游标消除重放漂移。limit 上限 100 双端点统一（chunks 含 content+parent_content 双文本，100 上限控制响应体）。

### D3：新操作沿用 `operation_security` baseline 豁免（与 B1 D3 同模式）
- **选择：** 3 个新 operationId 各加一条 accepted_baseline，与 KB 域既有豁免完全同格式。
- **理由：** v1.yaml 全局未声明 security scheme，Services 契约统一走豁免；auth 声明属独立批次，本批次不越权。

### D4：JSONB 双字段（`custom_metadata` / `source_chunks`）proto 侧统一为 string，Gateway 反序列化为 REST 结构化类型
- **模糊点：** proto3 无原生 JSON 类型，DB JSONB 列在 proto 侧的表达方式未在 Plan 明示。
- **选择：** proto `string custom_metadata = 11` / `string source_chunks = 5`（注释标注 JSONB string），REST 侧分别为 object 与 `array<KBSourceChunk> nullable`。
- **理由：** SPEC §3.2 冻结该链路（Gateway Unmarshal + 非法 JSON 跳过/500 分级处理见 §6.2）；避免了 proto 侧引入 google.protobuf.Struct 的版本化复杂度，且与 kb-service 既有 `_persist_assistant` 写入路径（source dict → json.dumps）零转换对接。**不回溯改造** `KBQueryResponse.sources` 内联对象（范围纪律，SPEC §4.2 明示）。

### D5：新列表响应统一 `items`+`next_cursor` 模式（对齐 P1 批次而非旧 `documents`+`meta`）
- **选择：** KBChunkListResponse / KBSessionMessageListResponse 均 `items: array + next_cursor: string nullable`。
- **理由：** 与同 proto 的 ListKBCitationsResponse / ListKBSessionsResponse（P1 批次落地）命名一致；旧批次 `documents`+`meta`（含 total）形态已被 cursor 分页范式取代，新 schema 不再引入 total（keyset 分页下 total 语义弱且代价高）。

## 偏差（Deviations vs PRD/UX/SPEC）

None — 手写契约 3 文件与 SPEC 冻结定义（§4.3 #11/#17/#18 path、§3.2 列映射、§2.4 proto、§4.2 豁免纪律、§9.4 生成物清单）逐字对照一致；`content_type`/`token_count` 无 nullable 符合 SPEC §3.2 L140「DB 可空 → proto 空串/0，序列化可省略」设计；`KBSourceChunk` 5 字段（doc_id/file_name/page/content/score）与 proto `SourceChunk` 及 `KBQueryResponse.sources` 内联对象字段集对齐，键名 `page` 与存储层写入键（`retrieve_service.py` sources dict）精确匹配。生成物为工具产物无人工内容。review-it 收口审查 0 accepted findings。

## 范围外良性同步生成物（随批次保留）

grpc_tools 全量重生成携带了 Python 侧生成物欠账追平，已逐 diff 确认非破坏性：
- `services/kb-service/app/generated/common/v1/common_pb2.py{,i}`（+15 行）：`IdempotentResult` message——proto 源（common.proto L30-32）与 Go 侧 pb 均已存在，Python 侧历史欠账自动追平（与 B1 批次 tenant/inference pb 追平同类情况），wire 兼容
- 受影响消费方验证：`go build ./pkg/... ./services/ani-gateway/...` 通过；kb-service Python import smoke（生成 stub + 3 RPC + 全字段数）通过

## 权衡（Tradeoffs）

### T1：不修改 route-baseline.yaml 登记新路径豁免，接受 route-contract 门禁红（沿用 B1 T1 决策，B2 新增 3 条）
- **备选 A（采纳）：** 不动 `services-route-baseline.yaml`，`validate-services-route-contract` 报 5 条 `spec_not_in_code`（B1 的 2 + B2 的 3）。
  - 优点：SPEC §4.2 硬约束规定新 path 必须与 Gateway 路由注册**同一 PR** 落地，登记豁免会绕过该约束；issue-046（Gateway，依赖 #041，同 PR）注册路由后门禁自然转绿。
  - 缺点：B2 单独验证时该门禁为红（预期中间状态），报告中显式归因。
- **备选 B（弃用）：** 加 `spec_not_in_code` 豁免。
  - 弃用理由：同 B1 T1——加豁免把"必然同 PR 落地"降级为"无限期豁免"，#046 还要删条目，徒增基线噪音。

### T2：servicer 不在本批次实现，接受契约/生成物先行
- **备选 A（采纳）：** 契约层完整落地（proto+两侧 pb+SDK），kb-service ListDocumentChunks/GetSessionMessages/DeleteSession servicer 留待 issue-045。
  - 优点：严格批次边界（issue-041 Type: core contract，allowed paths 不含 `app/api/`）；新 RPC 在 servicer 缺省时由 grpc base class 返回 UNIMPLEMENTED，Gateway 路由同样未注册（issue-046），无任何可达调用路径，运行时零风险；两侧 pb 先行使 #045/#046 可并行开发。
  - 缺点：分支合入主 PR 前 3 个新接口不可调用（由 #045/#046 同 PR 补齐，PR 级完整性不受损）。
- **备选 B（弃用）：** 本批次顺带实现 servicer。
  - 弃用理由：违反 issue Scope；kb-service 属冻结 Services 后端，跨批次改动破坏 review 边界。

### T3：messages 复合游标接受 tie-break 索引不完美（不预置迁移）
- **选择：** 契约冻结 `ORDER BY created_at ASC, id ASC`，`idx_kb_messages_session(session_id, created_at)` 可用、id 仅 tie-break（SPEC §4.5 已评估），本批次不加索引迁移。
- **理由：** SPEC §3.4 明确契约批次无迁移；单会话消息量级小（问答会话几十条），tie-break 额外 sort 实际影响可忽略。#045 实现时若压测暴露问题再评估 `(session_id, created_at, id)` 复合索引（记入 OQ2 同类事项）。

## 开放问题（Open Questions）

### OQ1：route-contract 门禁合入前仍红（5 条），依赖 issue-046 同 PR 解除
`validate-services-route-contract` 的 5 条 `spec_not_in_code`（B1 2 条 + B2 3 条：listKnowledgeBaseDocumentChunks / listKnowledgeBaseSessionMessages / deleteKnowledgeBaseSession）按 SPEC §4.2 必须由 issue-046 Gateway 路由注册解除。合入顺序建议：#041（契约）→ #045（servicer）/ #046（Gateway 路由）同 PR 合入 main 后门禁全绿。若 #046 延期需决策是否临时豁免（当前倾向不加，见 T1）。

### OQ2：#045 实现批次——chunks keyset 分页索引评估
`idx_kb_chunks_kb_doc(kb_id, doc_id)` 不含排序列 id，`WHERE kb_id=? AND doc_id=? ORDER BY id LIMIT n` 在大文档（数百 chunk）下可能退化为 sort。SPEC §3.4 明确契约批次无迁移、加索引属 #045 实现自由度；实现时评估 `(kb_id, doc_id, id)` 复合索引。messages 侧同类低优先级事项见 T3。

### OQ3：#046 Gateway 映射——`page`/`score` 零值→null 转换必须显式处理
生产者在写入时已把 DB NULL 归一为零值（`retrieve_service.py` `int(meta.get("page_number", 0) or 0)`），存储 JSONB 中 page 恒为 int、score 恒为 float（无 null）。契约 `page/score: nullable: true` 语义正确（对齐 DB），但 #046 Gateway 做 proto→REST 映射时需 `0 → null` 转换，否则前端会显示"第 0 页"而非"未知页"；`content_type`/`token_count` 同理需 omitempty 落实 SPEC §3.2 L140「序列化可省略」。另 `kbCitationJSON` 补 `message_id`/`session_id` 映射（SPEC §4.3 #15 明示属 #046 范围）。

### OQ4：本地环境噪音 3 项（CI 干净 checkout 均不存在，已验证+恢复）
与 B1 记录的 boundary venv 噪音同类，本次新增两处：① `validate_services_boundary.py` 扫 `.venv` 内 gitignored joblib big5 测试文件（UTF-8 decode 失败，移出后通过）；② `validate-architecture` 扫 `.run/gomodcache` 第三方模块缓存（脚本仅排除 `vendor/.cache`，移出后通过）；③ `compileall ai/` 遇 `.venv` 内 torch PEP 695 文件（本地 Python 3.10/3.11 语法不识别，限定 `ai/operators ai/pkg` 后通过）。三者均为 gitignored/untracked 本地文件，CI 干净 checkout 不复现。

## 验证命令（已运行）

```
python scripts/validate_services_contract.py     # 112 accepted baseline warning(s)（B1 109 + 新 3）
python scripts/validate_yaml.py api/openapi/services/v1.yaml
python scripts/validate_spec_split.py / validate_doc_api.py / validate_doc_entrypoints.py
python scripts/validate_sdk_beta.py
python scripts/validate_architecture.py          # gomodcache 移出后通过（本地噪音，见 OQ4）
python scripts/validate_services_boundary.py      # venv 移出后通过（本地噪音，见 OQ4/B1 注）
python scripts/validate_services_route_contract.py  # 预期红：5 spec_not_in_code（B1 2 + B2 3，issue-046 解除）
go build ./pkg/... ./services/ani-gateway/...    # kb pb 消费方编译
go test ./services/model-service/... ./services/inference-service/...
python -m compileall -q ai/operators ai/pkg      # 全量 ai/ 含 venv torch PEP 695 噪音（OQ4）
python（import smoke）                            # kb pb2/pyi/grpc：字段数 + 3 RPC stub 验证
python scripts/gen_sdk_alpha.py && python scripts/generate_api_docs.py && npx openapi-typescript  # 双次生成 git diff 不变（幂等零漂移）
git diff --check                                  # exit 0
```

> 注：Windows 本地验证与 B1 相同的环境噪音处理（venv/gomodcache 移出→验证→恢复）；`make validate-services` 完整清单逐项对照执行，唯一红为 route-contract 预期中间态（T1），CI 干净 checkout 下其余全绿。
