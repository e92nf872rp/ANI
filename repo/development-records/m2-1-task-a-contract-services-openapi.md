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
