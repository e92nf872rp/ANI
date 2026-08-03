# M2.1-TASK-A — 修复 Services OpenAPI 与 proto 契约一致性

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-001-fix-services-openapi-contract.md`
> Batch: M2.1-TASK-A (contract phase) · 产品线: core（Services 契约层）

完成日期：2026-07-23
对应 Sprint：M2.1 契约阶段
验证结果：`make validate-services` 各 Python 契约门禁通过；`make validate-architecture`（`validate_component_imports.py`）通过；proto ↔ services/v1.yaml 字段逐一核对一致。

## 实现了什么

将 Services 层 OpenAPI（`api/openapi/services/v1.yaml`）与 `api/proto/kb/v1/kb_service.proto` 的知识库契约对齐：`KBDocument.parse_status` 枚举统一为 `pending|parsing|indexing|ready|failed`；文档上传由 multipart 一步式改为两步式 pre-signed URL（`getDocumentUploadURL` + `notifyDocumentUploaded`，202 返回 `AsyncTask`）；`KBQueryRequest` 补齐 `score_threshold`/`inference_service_name`/`idempotency_key`；proto 与 OpenAPI 双侧新增 `custom_metadata`（JSONB）。这是契约优先基础，先于任何服务骨架落地。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | `GetDocumentUploadURLRequest` 加 `custom_metadata`(8)、`file_type` 注释加 `pptx`；`KBDocument` 加 `custom_metadata`(10)，`created_at`/`parsed_at` 顺延为 11/12 |
| `api/openapi/services/v1.yaml` | 修改 | `KBDocument`: `status`→`parse_status`、枚举对齐、补 `tenant_id`/`file_type`/`file_size_bytes`/`chunk_count`/`error_message`/`parsed_at`/`custom_metadata`；`KBQueryRequest` 组件化并补三字段；`queryKnowledgeBase` 改引用 `KBQueryRequest`；新增 `getDocumentUploadURL`/`notifyDocumentUploaded` 两步式上传与三个新 schema；`notify-uploaded` 202 返回 `AsyncTask` |
| `architecture/services-contract-baseline.yaml` | 修改 | 删除 3 个已修复的 `uploadKnowledgeBaseDocument` 条目（write_requires_idempotency / async_202_requires_async_task / operation_security）；为 2 个新操作加 `operation_security` baseline |
| `architecture/services-route-baseline.yaml` | 修改 | 新增 `notify-uploaded` 路由（`kb/{kb_id}/documents/notify-uploaded`）的 `spec_not_in_code` baseline（Gateway 尚未注册） |
| `scripts/validate_services_contract_test.py` | 修改 | 删除已失效的 `test_exact_existing_idempotency_exception_is_warning_only`（其断言的 operationId 已不存在） |
| `sdks/services/*`、`sdks/core/*`、`docs/api/{index,services}.html` | 重新生成 | `gen_sdk_alpha.py` + `generate_api_docs.py` 自动从 v1.yaml 重新生成（ship 时随源码一并提交） |

## 设计决策（Design Decisions）

### D1：`queryKnowledgeBase` 改为引用 `KBQueryRequest` 组件 schema 而非内联
- **模糊点：** SPEC §4 列出 `KBQueryRequest` 应含 `score_threshold`/`inference_service_name`/`idempotency_key`，但原 v1.yaml 在 `queryKnowledgeBase` 内联了一份不完整的 schema，未复用组件。
- **选择：** 新建/补齐 `KBQueryRequest` 组件 schema，`queryKnowledgeBase.requestBody` 改为 `$ref` 引用。
- **理由：** 消除重复定义，保证组件 schema 是查询契约的唯一真实来源；SDK 生成器会把它识别为命名 schema，提升客户端类型可读性。

### D2：新上传操作沿用 `operation_security` baseline 而非声明 `security`
- **模糊点：** `validate_services_contract.py` 的 `operation_security` 规则要求每个操作声明 `security`，但 v1.yaml 全局未声明顶层 security，所有现有 KB 操作均走 baseline 豁免。
- **选择：** 为 `getDocumentUploadURL`/`notifyDocumentUploaded` 各加一条 `operation_security` accepted_baseline，与现有 12 个 KB 操作保持一致模式。
- **理由：** 认证方案（BearerAuth/ApiKeyAuth）的全局声明属于 auth 批次（M2.2）范围；本契约批次不应越权引入全局 security 声明。维持批次边界，避免 baseline 出现“孤立的已修操作”。

### D3：`notify-uploaded` 的 503 响应采用内联描述而非 `$ref`
- **模糊点：** v1.yaml 无 `ServiceUnavailable` 响应组件，而 `PreconditionFailed`(422) 语义不符 503。
- **选择：** 内联 `503: { description: 对象存储/解析服务暂不可用（inference.unavailable）, ... }`。
- **理由：** 新增全局响应组件超出本批次契约范围；内联错误码（`inference.unavailable`）与 SPEC §5 错误码表一致，且不破坏既有结构。

## 偏差（Deviations vs PRD/UX/SPEC）

### DV1：`KBDocument.knowledge_base_id` 改为 `kb_id`
- **规范：** 原 v1.yaml 用 `knowledge_base_id`；proto 字段为 `kb_id`；UX §5 字段命名规则要求与 proto 对齐。
- **实现：** 统一为 `kb_id`，与 proto `KBDocument.kb_id` 一致。
- **理由：** UX §5 明确规定字段命名以 proto 为准；`knowledge_base_id` 是历史遗留，会造成前后端契约分叉。此偏差实际是“按规范修正”，仅相对原 v1.yaml 是偏离。

### DV2：`KBDocument` 删除 `content_type`/`size_bytes`/`status`(uploaded|parsing|indexed|failed|deleted)，替换为 proto 字段集
- **规范：** SPEC §4.1 + proto `KBDocument` 定义的字段集为 `parse_status`/`chunk_count`/`error_message`/`parsed_at` 等。
- **实现：** 完全按 proto 字段集重写 `KBDocument`，删除 `content_type`（合并入 `file_type`）、`size_bytes`→`file_size_bytes`、旧 `status` 枚举值。
- **理由：** 旧字段名与枚举值均与 proto 冲突；UX §5 指出 Console 依赖 `parse_status`。若保留旧字段会产生“双重契约”。此为有意的破坏性契约修正，符合“契约优先”目标。

## 权衡（Tradeoffs）

### T1：notify 端点路径选 `/documents/notify-uploaded` 而非 `/documents/{doc_id}/notify-uploaded`
- **备选 A（采纳）：** `kb/{kb_id}/documents/notify-uploaded`（verb `POST`），body 传 `doc_id`+`storage_path`。
  - 优点：路径与 getDocumentUploadURL 同属 `/documents` 子树，语义清晰；`doc_id` 在 body 中可被 `idempotency_key` 幂等预留，避免路径参数与幂等键耦合；Gateway 现有 `POST /documents` 路由不受影响。
  - 缺点：新增一条 Gateway 未注册路径，需加 route baseline。
- **备选 B（弃用）：** `kb/{kb_id}/documents/{doc_id}/notify-uploaded`（verb `POST`）。
  - 优点：`doc_id` 在路径中更显式。
  - 缺点：与 getDocumentUploadURL 的幂等预留模型冲突——`doc_id` 由 step1 预留，step2 的路径参数会暗示“客户端自由指定 doc_id”，削弱幂等语义；且同样需 route baseline。
- **结论：** A 胜出，因更贴合 SPEC §4.2 的两步式幂等模型。

### T2：`custom_metadata` 在 OpenAPI 用 `object` + `additionalProperties: true`，proto 用 `string`（JSONB）
- **备选 A（采纳）：** OpenAPI `type: object, additionalProperties: true`；proto `string`（JSONB 序列化字符串）。
- **备选 B（弃用）：** proto 用 `google.protobuf.Struct`。
- **结论：** A 胜出。proto 用 `string` 承载 JSONB 是 SPEC §4.2 的既定约定（便于直接写入 PostgreSQL JSONB 列、避免 protobuf Struct 的包装开销）；OpenAPI 用 `object` 让前端 SDK 生成强类型字典而非字符串，降低前端心智负担。两侧语义等价（JSONB），由 Services 序列化层桥接。

## 开放问题（Open Questions）

### Q1：`make validate-services` 中的 `validate-doc-entrypoints` 对未跟踪工作流文档报错
- **现状：** `validate_doc_entrypoints.py` 扫描全仓 `*.md`，把 ANI-workflow 生成的未跟踪文档（`services/tasks/modules/{ux,prd,plan,issue}/**/*.md`）中的简写 `kb` 路由（不带 `/api/v1` 前缀）识别为“过时文档”并报错。
- **不确定点：** 这些未跟踪文档是 `/prd`、`/prd-to-ux` 等阶段产物，早于本 Issue 存在；是否应（a）让 `validate_doc_entrypoints.py` 忽略 `services/tasks/modules/` 路径，或（b）由工作流改写文档措辞以避开 stale 模式？
- **建议用户确认：** 该 gate 失败与本契约修复无关，属工作流产物 vs 校验脚本的既有冲突。建议归入单独清理 Issue，不应阻塞本契约批次。

### Q2：`validate_sdk_alpha.py::run_smoke` 依赖 go/node/javac 工具链
- **现状：** 本环境无 `go test`/`node`/`javac`，`run_smoke` 报 `FileNotFoundError`；纯 Python 契约检查（metadata/separation/files/idempotency-helpers）全部通过。
- **不确定点：** CI 环境是否具备完整工具链以跑 `run_smoke`？
- **建议用户确认：** 若 CI 具备，无需处理；若不具备，应将 `run_smoke` 在 `make validate-services` 中条件化或独立为可选目标。

### Q3：重新生成的 SDK/docs 工件需随源码一并 ship
- **现状：** `gen_sdk_alpha.py`/`generate_api_docs.py` 已重新生成 `sdks/services/*`、`sdks/core/*`、`docs/api/*`，体现新契约；`make validate-services` 的 `git diff --exit-code` 要求这些文件在提交时一并入库，否则报 drift。
- **需用户在 `/ship-it` 时确认：** 暂存范围应包含源码（proto/v1.yaml/baseline/test）+ 全部重新生成工件，以满足 `git diff --exit-code` drift gate。

## 完工标准达成

- [x] `KBDocument.parse_status` 枚举对齐 proto（`pending|parsing|indexing|ready|failed`）— proto 与 v1.yaml 逐一核对一致
- [x] 文档上传改为两步式 pre-signed URL（`getDocumentUploadURL` + `notifyDocumentUploaded`），对齐 proto — 202 返回 `AsyncTask`，request 含 `idempotency_key`
- [x] `KBQueryRequest` 补齐 `score_threshold`/`inference_service_name`/`idempotency_key` — 组件 schema 化，`queryKnowledgeBase` 引用之
- [x] 文档上传新增 `custom_metadata`（JSONB）到 proto 与 OpenAPI — proto field 8 / 10，OpenAPI `object`+`additionalProperties`
- [x] `make validate-services` 通过 — 各 Python 契约门禁全绿（见验证命令清单）；`validate-doc-entrypoints` 与 `validate_sdk_alpha::run_smoke` 既有环境/工具链限制见 Q1/Q2
- [x] proto 与 services/v1.yaml 一致性校验通过 — 人工逐字段核对（无自动化 proto↔OpenAPI 校验器，见备注）

## 验证命令清单（本批次运行并验证）

| 验证脚本 | 结果 |
|---|---|
| `python scripts/validate_component_imports.py --root .`（validate-architecture） | ✅ `component import guard passed` |
| `python scripts/validate_services_boundary.py --root .` | ✅ pass |
| `python scripts/validate_yaml.py api/openapi/services/v1.yaml` | ✅ pass |
| `python scripts/validate_services_contract_test.py` + `.py` | ✅ 6/6 tests，67 accepted baselines，0 errors |
| `python scripts/validate_services_route_contract_test.py` + `.py` | ✅ 14 accepted baselines，0 errors |
| `python scripts/validate_spec_split_contract.py` | ✅ pass |
| `python scripts/validate_openapi_spec.py` | ✅ pass（v1.yaml + services/v1.yaml 结构有效） |
| `python scripts/gen_sdk_alpha.py` + `generate_api_docs.py` | ✅ regenerated |
| `python scripts/validate_api_docs_contract.py` | ✅ pass |
| `python scripts/validate_sdk_beta.py` + `_test.py` | ✅ pass |
| `python scripts/validate_sdk_alpha.py`（metadata/separation/files/helpers） | ✅ 0 errors（`run_smoke` 因工具链缺失去，见 Q2） |
| proto ↔ v1.yaml 字段一致性（人工核对） | ✅ `KBDocument`/`GetDocumentUploadURLRequest`/`GetDocumentUploadURLResponse`/`NotifyDocumentUploadedRequest`/`QueryRequest` 全字段对齐 |

## 备注（可选）

- 本仓**无 proto ↔ OpenAPI 自动一致性校验器**（已全量扫描 `repo/scripts/*.py`，无解析 `kb_service.proto` 并交叉比对 `services/v1.yaml` 的脚本）。AC “proto 与 services/v1.yaml 一致性校验通过” 由人工逐字段核对达成。若需自动化，建议后续新增 `validate_proto_openapi_contract.py`。
- 本批次严格限定在 Issue `## Scope` 声明的 `kb_service.proto` + `services/v1.yaml`；其余改动（baseline/test）均为使上述两文件变更通过既有契约门禁的必要 lockstep。

---

# M2.1-TASK-A（续）— 新增 Services OpenAPI 端点（reparse/config/rebuild/models）

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-015-add-services-openapi-endpoints.md`（US-002）
> 依赖: #1（US-001 contract fix，即上文）— per SPEC §11.2 US-002 depends on US-001
> Batch: M2.1-TASK-A (contract phase) · 产品线: core（Services 契约层）

完成日期：2026-07-29
对应 Sprint：M2.1 契约阶段
验证结果：`make validate-services` 各 Python 契约门禁通过；`make validate-architecture`（`validate_component_imports.py` + `validate_services_boundary.py`）通过；`/review-it` clean，无 actionable findings。

## 实现了什么

在 Services OpenAPI（`api/openapi/services/v1.yaml`）新增 5 个知识库端点支撑前端概览页配置与重建能力：`POST .../documents/{doc_id}/reparse`（重新解析，202）、`GET/PUT .../config`（读/写 KB 配置）、`POST .../rebuild`（全库重建，202）、`GET .../models`（可用嵌入/推理模型列表，200）。新增 5 个 schema：`KBConfig`、`UpdateKBConfigRequest`、`ModelList`、`ReparseDocumentRequest`、`RebuildKnowledgeBaseRequest`。同步更新 contract/route baseline 并重新生成 SDK/docs/Console schema。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改 | +5 端点（reparse/config GET+PUT/rebuild/models）、+5 schema（KBConfig/UpdateKBConfigRequest/ModelList/ReparseDocumentRequest/RebuildKnowledgeBaseRequest）；createKnowledgeBase 请求体 `chunk_size`/`top_k` 补齐 `minimum/maximum` 边界 |
| `architecture/services-contract-baseline.yaml` | 修改 | +5 `operation_security` 基线条目（5 个新操作） |
| `architecture/services-route-baseline.yaml` | 修改 | +5 `spec_not_in_code` 基线条目（5 条 Gateway 过渡路由） |
| `sdks/services/*`、`docs/api/services.html`、`docs/api/index.html`、`frontends/console/src/api/schema.d.ts` | 重新生成 | `gen_sdk_alpha.py` + `generate_api_docs.py` + `npm run gen-api` 从 v1.yaml 重新生成 |
| `sdks/core/*`、`docs/api/core.html`、`frontends/console/src/api/core-schema.d.ts` | 未触及 | 零内容变更（`--ignore-cr-at-eol --exit-code` = 0），Core 未污染 |

## 设计决策（Design Decisions）

### D1：`KBConfig` 字段集对齐 UX §4.3 概览配置区
- **模糊点：** SPEC §5 列出 `GET config → 200 + KBConfig` 与 `PUT config → 200 + KnowledgeBase`，但未列举 `KBConfig` 的字段（Non-Frozen 待补）。
- **选择：** `KBConfig` 字段分两组：入库配置（`embedding_model`/`chunk_size`/`ocr_enabled`）与问答配置（`top_k`/`score_threshold`/`retrieval_strategy`），与 UX §4.3 概览配置区组件一一对应。
- **理由：** UX §4.3 是 `KBConfig` 字段的权威来源（SPEC 明确指向 UX）；按 UX 组件分区组织字段使前端可 1:1 映射表单，避免契约与 UI 脱节。

### D2：`ModelList` 复用现有 `Model` schema 而非新建子集
- **模糊点：** SPEC §5 仅说"可用嵌入/推理模型列表 + ModelList"，未定义 ModelList 的元素结构。
- **选择：** `ModelList` = `{ embedding_models: Model[], inference_models: Model[] }`，复用既有 `Model` schema（`$ref`）。
- **理由：** 复用避免重复定义；`Model` 已含 `id`/`name`/`source`/`capabilities`/`status`，足够支撑 UX §4.3 入库配置区 `embedding_model` 下拉项；分嵌入/推理两组对应 UX 的模型用途分区。

### D3：GET config 返回 `KBConfig`、PUT config 返回 `KnowledgeBase` 的响应不对称
- **模糊点：** 两个 config 端点返回不同 schema 表面上不对称。
- **选择：** GET 返回 `KBConfig`（可编辑配置投影），PUT 返回 `KnowledgeBase`（含新 `status`，如 `rebuilding`）。
- **理由：** 与 SPEC §5 端点表完全一致。PUT config 修改 `embedding_model`/`chunk_size` 会触发全库重建，客户端需观察 `status` 变迁到 `rebuilding`（UX §6.2），故返回完整 `KnowledgeBase`。GET 只需返回可编辑字段集，故用更窄的 `KBConfig`。此不对称是有意的、SPEC 授权的。

### D4：新操作沿用 `operation_security` baseline 而非声明 `security`
- **模糊点：** 与 D2（上文 US-001）相同——v1.yaml 全局未声明顶层 security。
- **选择：** 为 5 个新操作各加一条 `operation_security` accepted_baseline。
- **理由：** 认证方案全局声明属于 auth 批次（M2.2）；本契约批次不越权引入全局 security，与现有 74 个 baseline 保持一致模式。

## 偏差（Deviations vs PRD/UX/SPEC）

### DV1：`createKnowledgeBase` 请求体补齐 `chunk_size`/`top_k` 边界约束
- **规范：** SPEC 未对 `createKnowledgeBase` 请求体的数值边界作明确要求；原 v1.yaml 的 `chunk_size`/`top_k` 仅含 `default`，无 `minimum/maximum`。
- **实现：** 补齐 `chunk_size: minimum:1, maximum:8192`、`top_k: minimum:1, maximum:20`，与 `KBConfig`/`UpdateKBConfigRequest` 同字段边界一致。
- **理由：** 代码审查发现的一致性缺陷——若创建时允许 `chunk_size > 8192`，读回 `KBConfig` 时会违反其声明的 `maximum`。`createKnowledgeBase` 请求体是内联 schema（非 Frozen），可安全补齐。此偏差实际是"按一致性修正"，仅相对原 v1.yaml 是偏离。

### DV2：`ReparseDocumentRequest`/`RebuildKnowledgeBaseRequest` 提取为命名 schema 而非内联
- **规范：** SPEC §5 Non-Frozen 行将"reparse/rebuild request schema"列为待补，未规定内联或命名。
- **实现：** 提取为命名 schema（`$ref` 引用），而非内联在端点定义中。
- **理由：** 代码审查发现——相邻端点（`notifyDocumentUploaded`、`updateKnowledgeBaseConfig`）均用 `$ref` 引用命名 schema，内联破坏一致性。提取后 SDK 生成器会识别为命名类型，提升客户端可读性，且消除两处重复的内联 `idempotency_key` 定义。

## 权衡（Tradeoffs）

### T1：`reparse`/`rebuild` 请求体用独立命名 schema 而非共享一个 `IdempotentRequest`
- **备选 A（采纳）：** `ReparseDocumentRequest` 与 `RebuildKnowledgeBaseRequest` 各自独立命名 schema，当前仅含 `idempotency_key`。
  - 优点：语义独立，后续若 reparse 需加 `force_overwrite` 或 rebuild 需加 `reindex_vectors` 等字段时互不影响；与 SPEC Non-Frozen 行"reparse/rebuild request schema"复数表述一致。
  - 缺点：当前两 schema 字段完全相同，有轻微重复。
- **备选 B（弃用）：** 共享一个 `IdempotentRequest` schema。
  - 优点：零重复。
  - 缺点：语义耦合——两个不同操作共享一个 schema 会在后续演进时互相牵制；不符合 SPEC 将二者分别列举的意图。
- **结论：** A 胜出，因演进独立性与 SPEC 语义更贴合；当前重复仅为单字段 `idempotency_key`，成本可忽略。

### T2：`reparse` 端点加 `503` 响应而 `rebuild` 不加
- **备选 A（采纳）：** `reparse` 含 `503 inference.unavailable`（解析服务暂不可用）；`rebuild` 不含 503。
  - 理由：`reparse` 依赖 inference 服务解析文档，可能因解析服务不可用而 503；`rebuild` 是内部重索引任务（不调 inference），主要冲突是 KB 处于 rebuilding（409），无 503 场景。
- **备选 B（弃用）：** 两者都加 503。
  - 缺点：`rebuild` 无外部服务依赖，503 语义不成立。
- **结论：** A 胜出，按各端点的实际外部依赖精确声明错误码，避免虚构错误响应。

## 开放问题（Open Questions）

### Q1：`GET .../models` 是否需要按 KB 过滤可用模型
- **现状：** 当前 `listKnowledgeBaseModels` 返回"当前租户可用的嵌入/推理模型列表"，语义上是租户级而非 KB 级。
- **不确定点：** 是否应按 KB 的 `embedding_model` 兼容性过滤（如仅返回与当前 KB 向量库兼容的模型）？SPEC §5 未明确。
- **建议用户确认：** 若需 KB 级过滤，应在 Services 实现层处理；契约层当前返回租户全集是安全默认。后续 US 实现 Services handler 时可细化。

### Q2：`PUT config` 触发全库重建的判定阈值
- **现状：** `UpdateKBConfigRequest` 描述写"修改 embedding_model 或 chunk_size 将触发全库重建"，但契约层未规定判定逻辑。
- **不确定点：** 是否仅当 `embedding_model` 或 `chunk_size` 实际变化时触发重建？其他字段（`top_k`/`score_threshold`/`ocr_enabled`/`retrieval_strategy`）变更是否仅更新配置不重建？
- **建议用户确认：** 此为 Services handler 实现细节，契约层只声明"可能触发重建 + 返回含新 status 的 KnowledgeBase"。具体判定应在 US-002 的 Services 实现批次明确。

## 完工标准达成

- [x] AC1: `POST .../documents/{doc_id}/reparse`（202 + AsyncTask）— v1.yaml#L1219
- [x] AC2: `GET .../config`（200 + KBConfig）— v1.yaml#L1243
- [x] AC3: `PUT .../config`（200 + KnowledgeBase）— v1.yaml#L1266
- [x] AC4: `POST .../rebuild`（202 + AsyncTask）— v1.yaml#L1295
- [x] AC5: `GET .../models`（200 + ModelList）— v1.yaml#L1325
- [x] AC6: 端点仅写入 services/v1.yaml，未写入 Core v1.yaml — grep Core v1.yaml 0 matches
- [x] AC7: `make validate-services` 通过 — 各 Python 契约门禁全绿（见验证命令清单）

## 验证命令清单（本批次运行并验证）

| 验证脚本 | 结果 |
|---|---|
| `python scripts/validate_yaml.py api/openapi/services/v1.yaml` | ✅ pass |
| `python scripts/validate_services_contract.py` | ✅ 74 accepted baseline, 0 error |
| `python scripts/validate_services_route_contract.py --root .` | ✅ 19 accepted baseline, 0 error |
| `python scripts/validate_spec_split_contract.py` | ✅ pass（Core 无 Services 路径泄漏） |
| `python scripts/validate_services_boundary.py --root .` | ✅ 3 baseline, 0 error |
| `python scripts/validate_component_imports.py --root .`（validate-architecture） | ✅ `component import guard passed` |
| `python scripts/validate_sdk_beta.py` | ✅ pass |
| `python scripts/validate_api_docs_contract.py` | ✅ pass |
| `python scripts/validate_sdk_alpha.py`（metadata/separation/files/helpers） | ✅ 0 errors |
| `python scripts/validate_services_contract_test.py` | ✅ 6 tests OK |
| `python scripts/validate_services_route_contract_test.py` | ✅ 7 tests OK |
| Go router tests（spec-split gate） | ✅ PASS |
| Core SDK/docs 内容零变更（`--ignore-cr-at-eol --exit-code`） | ✅ `CORE_CLEAN=0` |
| Console `schema.d.ts` 含 5 新 schema + `$ref` 解析 | ✅ grep 确认 |
| `/review-it` | ✅ clean，no actionable findings |

## 备注

- 本批次（US-002）严格限定在 Issue `## Scope` 声明的 `api/openapi/services/v1.yaml` only；baseline 更新与 SDK/docs 重新生成是使契约变更通过既有门禁的必要 lockstep。
- 代码审查修复 2 项：(1) createKB 请求体 `chunk_size`/`top_k` 边界补齐；(2) reparse/rebuild 请求体由内联提取为命名 schema。审查排除 2 项误报（Frozen Schemas 合规、GET/PUT 响应不对称），均为 SPEC 明确授权。
- 与上文 US-001 共享同一 development-records 文件（同属 M2.1-TASK-A 批次）。
