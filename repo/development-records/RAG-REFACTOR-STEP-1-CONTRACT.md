# RAG-REFACTOR-STEP-1-CONTRACT — Core API 扩展预计算向量与 content 字段 (步骤 1 契约)

- **Issue:** issue-024-contract-core-vector-content-schema
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-14
- **Product line:** core (Core API / ani-gateway)
- **Type:** contract (仅契约定义, 不含实现逻辑)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `pkg/ports/vector_store.go` | `VectorDocumentInput` 新增 `Vector []float32`; `VectorSearchResult` 新增 `Content string` |
| `services/ani-gateway/internal/router/vector_store_resources.go` | `vectorDocumentInputBody` 新增 `vector` JSON 字段; `vectorSearchHitResponse` 新增 `content` JSON 字段; handler 映射新字段 |
| `api/openapi/v1.yaml` | `VectorStoreDocumentInsertRequest` 文档项新增 `vector` 可选字段; `VectorStoreSearchHit` 新增 `content` 可选字段 |
| `frontends/console/src/api/core-schema.d.ts` | Console TS 类型派生物 (gen-core-schema.mjs 生成) |
| `frontends/boss/src/api/core-schema.d.ts` | BOSS TS 类型派生物 (gen-core-schema.mjs 生成) |
| `api/core-v1-compatibility-baseline.yaml` | 兼容性基线派生物 (generate_core_api_compatibility_baseline.py 生成) |

## 1. Design Decisions

### 1.1 Plan 中 `VectorSearchHit` 对应实际代码中的 `VectorSearchResult`

- **Ambiguity:** Plan §1.4 步骤 1 使用 `ports.VectorSearchHit` 命名，但代码中不存在此类型。实际搜索结果 port 类型为 `VectorSearchResult` (pkg/ports/vector_store.go:145)。
- **Choice:** 将 `Content string` 字段添加到 `VectorSearchResult`，而非创建新类型 `VectorSearchHit`。
- **Rationale:** `VectorSearchResult` 是现有唯一的搜索结果 port 类型，被 `VectorStoreService.SearchVectorStore` 接口和所有 adapter 使用。创建新类型 `VectorSearchHit` 会导致大量不必要重构，违背 Karpathy 原则三"只触碰你必须改动的部分"。`vectorSearchHitResponse` (gateway HTTP 响应类型) 仍然使用此名，因为它是 HTTP schema 层的命名，与 port 层解耦。

### 1.2 `content` 字段在 OpenAPI 中为非 required

- **Ambiguity:** Issue AC 要求 `content` 为"新增返回字段, 旧调用方忽略"，但未明确指定 OpenAPI `required` 列表是否应包含 `content`。
- **Choice:** `content` 不加入 `VectorStoreSearchHit` 的 `required: [id, score, metadata]`。
- **Rationale:** 向后兼容。Core API v1 允许新增可选 response 字段 (CLAUDE.md §4.4)；兼容性验证器 `validate_core_api_compatibility.py` 禁止对受保护 schema 新增 required 字段。`content` 的实际值取决于后端实现——Milvus adapter 当前将 content 塞入 `metadata["content"]`，新 `Content` 字段在功能 issue 中才会被填充。

### 1.3 Gateway handler 映射新字段但不添加实现逻辑

- **Ambiguity:** Issue AC 要求"不包含任何实现逻辑"，但新增字段需要在 handler 中映射以保持数据流通和编译通过。
- **Choice:** 在 `insertVectorStoreDocuments` handler 中将 `document.Vector` 传入 `ports.VectorDocumentInput`；在 `searchVectorStore` handler 中将 `result.Content` 映射到 `vectorSearchHitResponse`。
- **Rationale:** 这是契约层的字段映射（plumbing），不是业务逻辑。`InsertDocuments` adapter 仍使用 `localDocumentVector()` 生成伪向量（忽略 `Vector` 字段），`SearchVectorStore` 仍从 Milvus 返回 `metadata["content"]`（不填充 `Content` 字段）。真正的"优先用 doc.Vector"和"提取 content"逻辑在功能 issue（步骤 2）中完成。

## 2. Deviations

### 2.1 BOSS 前端 core-schema.d.ts 派生物

- **Spec said:** Issue AC 要求"v1.yaml 通过生成脚本派生更新（契约生成物）"，但未明确列举 BOSS 前端。
- **Implemented:** 额外运行了 `frontends/boss/scripts/gen-core-schema.mjs` 重新生成 BOSS TS 类型。
- **Why:** `make validate-services` 的 gate 检查 `git diff --exit-code -- frontends/console/src/api/schema.d.ts frontends/console/src/api/core-schema.d.ts`，但未覆盖 BOSS。然而如果 BOSS schema 与 v1.yaml 漂移，后续 BOSS PR 会失败。主动同步避免跨 issue 漂移。注意：Makefile `gen-api` 目标仅覆盖 Console，未覆盖 BOSS——这是 Makefile 的已有缺口，不属本 issue 范围。

### 2.2 package-lock.json 还原

- **Spec said:** N/A
- **Implemented:** npm install openapi-typescript 修改了 `frontends/boss/package-lock.json`，已 `git checkout` 还原。
- **Why:** `package-lock.json` 变更是 npm install 副产物，不属于契约变更。BOSS 的 `gen-core-schema.mjs` 通过 npx 调用 openapi-typescript，不依赖 `package-lock.json` 的修改。

## 3. Tradeoffs

### 3.1 手动编辑 v1.yaml vs 代码生成

- **Alternative A:** 从 Go struct 注解生成 OpenAPI spec（spec ← code）
- **Alternative B:** 手动编辑 v1.yaml，从 spec 派生其他产物（code ← spec）
- **Choice:** Alternative B（手动编辑 v1.yaml）
- **Pros/Cons:** v1.yaml 是项目中 Core API 的唯一真实来源 (CLAUDE.md §4.1)，已被 `validate_core_api_compatibility.py`、`validate_openapi_spec.py`、`gen_sdk_alpha.py`、`generate_api_docs.py` 等多个脚本消费。仓库中没有 Go → YAML 生成器。选择 B 与项目现有架构一致。A 需要引入新工具链，违背奥卡姆剃刀。

### 3.2 handler 中映射 Vector/Content 字段 vs 仅定义 struct

- **Alternative A:** 仅在 struct 中定义新字段，handler 不映射（留空）
- **Alternative B:** 在 handler 中映射新字段（完整数据流通）
- **Choice:** Alternative B
- **Pros/Cons:** A 会导致新字段在 HTTP 请求中传入但 handler 丢弃，编译通过但数据链断裂。B 保证完整的数据流通（HTTP → body struct → port struct → adapter），下游功能 issue 只需修改 adapter 逻辑即可消费字段。B 的代价是 handler 代码多两行映射，但这是必要的 plumbing。

## 4. Open Questions

### 4.1 Makefile gen-api 缺少 BOSS 覆盖

- **Question:** `make gen-api` (Makefile:197-205) 仅重新生成 Console TS 类型，不覆盖 BOSS。是否应在后续 issue 中补充 `make gen-boss-api` 目标？
- **Impact:** 如果 Core API 契约变更后忘记手动运行 BOSS 生成脚本，BOSS schema 会漂移。
- **Suggestion:** 可在 `gen-api` 目标中追加 `node frontends/boss/scripts/gen-core-schema.mjs`，或在 `validate-services` gate 中追加 `git diff --exit-code -- frontends/boss/src/api/core-schema.d.ts`。

### 4.2 `validate_vector_alpha_contract.py` pre-existing 失败

- **Question:** 该验证器期望 `router.go` 中包含 `registerVectorStoreResources(v1)`，但实际代码使用 `registerVectorStoreResourcesWithServiceAndTasks`。这是上游 main 分支的 pre-existing 问题，非本 issue 引入。是否需要修复验证器或 router 注册函数名？
- **Impact:** `make validate-services` 中的 `validate-services-contract` step 会失败。
- **Note:** 需单独 issue 处理，不属本契约 issue 范围。

## 5. Verification Commands Run

| Command | Result |
|---------|--------|
| `go build ./pkg/ports/... ./services/ani-gateway/...` | PASS |
| `go test -run TestLocalVectorStore\|TestVectorStore ./pkg/adapters/runtime/...` | 14/14 PASS |
| `go test -run TestVectorStore ./services/ani-gateway/internal/router/...` | ALL PASS |
| `python scripts/validate_component_imports.py --root .` | `component import guard passed` |
| `python scripts/validate_core_api_compatibility.py` | `core api compatibility valid` |
| `python scripts/validate_openapi_spec.py` | `OpenAPI specs valid: 2` |
| `git diff --check` | PASS |

## 6. References

- Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-024-contract-core-vector-content-schema.md`
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` §1.4 步骤 1
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- CLAUDE.md: §4.1 (先改 API 契约), §3.1 (Core 不含推理), §4.4 (新增可选字段不破坏性变更)
