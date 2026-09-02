# RAG-REFACTOR-STEP-1-FEATURE — Core API 预计算向量存储与 content 返回实现 (步骤 1 功能)

- **Issue:** issue-028-feature-core-vector-content-impl
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-18
- **Product line:** core (Core API / ani-gateway)
- **Type:** feature (实现逻辑, 依赖步骤 1 契约 issue #024)
- **Dependency:** #024 (Core 契约) — struct/schema 字段已由 #024 定义

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `pkg/adapters/runtime/vector_store_service.go` | `InsertDocuments` 优先使用 `doc.Vector` (预计算), 否则走 `localDocumentVector` 伪向量 fallback; 新增维度校验 |
| `pkg/adapters/vectorstore/milvus_store.go` | `milvusSearchResults` 构造 `VectorSearchResult` 时补上 `Content: content` 字段 (此前仅写入 `metadata["content"]`) |
| `pkg/adapters/runtime/vector_store_service_test.go` | 4 个新测试 + `fakeVectorStoreWithSearch` fake backend |
| `services/ani-gateway/internal/router/vector_store_resources_test.go` | 2 个新 HTTP 测试 + `contentReturningVectorStoreService` / `capturingInsertVectorStoreService` mocks |
| `scripts/test_vector_store_e2e.ps1` | 端到端测试脚本 (9 P0 接口, ani-gateway + 真实 Milvus) |

## 1. Design Decisions

### 1.1 防御性拷贝预计算向量

- **Ambiguity:** Plan §1.1 要求 `len(doc.Vector) > 0` 时用 `doc.Vector`, 但未指定是否需要拷贝。
- **Choice:** `vec = append([]float32(nil), document.Vector...)` — 完整拷贝调用方传入的 slice。
- **Rationale:** 调用方 (kb-service) 的 slice 可能在循环中被复用或 mutate。直接引用会导致后续文档覆盖前一文档的向量数据。与同文件 `SearchVectorStore` 第 227 行的拷贝模式一致。

### 1.2 维度校验在 InsertDocuments 层而非 Milvus 层

- **Ambiguity:** Plan 要求维度匹配, 但未指定在哪一层校验。
- **Choice:** 在 `InsertDocuments` 循环内 (第 397 行) 校验 `len(document.Vector) != record.Dimension`, 返回 `ErrInvalid`。
- **Rationale:** 提前校验 (fail-fast) 避免部分文档已写入 Milvus 后才发现维度不匹配。校验在 `backend.Upsert` (第 420 行) 之前完成, 保证原子性 — 维度不匹配时零文档写入。这也避免了 Milvus REST API 返回模糊的 400 错误, 给调用方明确的错误信息。

### 1.3 `milvus_store.go` 超出 Issue 声明 scope

- **Ambiguity:** Issue scope 声明允许的 code paths 为 `vector_store_service.go` 和 `vector_store_resources.go`, 不含 `milvus_store.go`。
- **Choice:** 经用户明确要求, 修复 `milvusSearchResults` 第 423 行 (现 424 行) 未填充 `Content` 字段的问题。
- **Rationale:** 契约 #024 定义了 `VectorSearchResult.Content` 字段, handler 已提取 `result.Content`, 但 Milvus 后端在第 420 行已取到 `item["content"]` 却只写入 `metadata["content"]`, 构造 `VectorSearchResult` 时漏设 `Content`。这是端到端契约链路的最后一环 — 不修复则生产路径 search 返回的 content 字段始终为空。改动极小 (仅新增 `Content: content` 字段赋值), `content` 变量已在同函数内定义。

### 1.4 `content` 变量从 `if` 块内提取到外层

- **Ambiguity:** 原代码 `if content, ok := item["content"].(string); ok && content != ""` 将 `content` 限制在 `if` 块作用域内, 无法在第 424 行使用。
- **Choice:** 改为 `content, _ := item["content"].(string)` 提取到外层, `if content != ""` 仅控制 `metadata["content"]` 写入。
- **Rationale:** `content` 需要在 `VectorSearchResult` 构造中使用, 必须提升作用域。类型断言 `.(string)` 失败时 `content` 为零值 `""`, 语义不变。`metadata["content"]` 的写入条件从 `ok && content != ""` 简化为 `content != ""`, 行为等价 (断言失败时 `content` 为 `""`, 条件同为 false)。

## 2. Deviations

### 2.1 `milvus_store.go` 改动超出 Issue 声明 scope

- **Spec said:** Issue #028 scope: "Code paths allowed: `repo/pkg/adapters/runtime/vector_store_service.go`, `repo/services/ani-gateway/internal/router/vector_store_resources.go`"
- **Implemented:** 额外修改了 `repo/pkg/adapters/vectorstore/milvus_store.go` 第 420-424 行。
- **Why:** 审查阶段发现端到端契约链路断裂 — Milvus 后端未将已取到的 `content` 字段填入 `VectorSearchResult.Content`, 导致生产路径 search 的 content 始终为空。用户明确要求修复。改动仅 1 行新增字段赋值, 无行为变更风险。

### 2.2 端到端测试脚本未在 Issue AC 中要求

- **Spec said:** Issue AC 要求 `go test` 单元测试通过, 未要求端到端测试。
- **Implemented:** 额外编写 `scripts/test_vector_store_e2e.ps1` 并运行 9 个 P0 接口端到端测试 (ani-gateway + 真实 Milvus 10.10.1.66:31930)。
- **Why:** 用户要求验证端到端契约。单元测试用 fake backend 无法验证与真实 Milvus 的 HTTP REST 契约 (`/v2/vectordb/entities/upsert` + `/search`)。端到端测试验证了预计算向量真正写入 Milvus、search 能读回 content 字段、维度校验在真实 collection schema 下一致。

## 3. Tradeoffs

### 3.1 伪向量 fallback 保留 vs 改为报错

- **Alternative A:** 无 `Vector` 字段时返回 `ErrInvalid` 报错, 避免静默写入不可检索的伪向量数据。
- **Alternative B:** 保留 `localDocumentVector` 伪向量 fallback, 与旧调用方行为一致。
- **Choice:** Alternative B
- **Pros/Cons:** A 更"干净" (伪向量存了也检索不到, 因为伪向量是 content 的确定性哈希, 与真实 query embedding 方向无关), 但会破坏 Issue AC #3 "不传 vector 时行为与旧调用方完全一致" 和 Plan §0 "功能效果不变" 核心约束。这是 11 步渐进改造的第 1 步, 旧调用方在步骤 1-10 期间通过 flag 并行运行, 靠 fallback 兜底。正确做法是在步骤 11 (删除旧路径) 之后新建 issue 将 fallback 改为报错。

### 3.2 测试 mock: 嵌入复用 vs 完整实现接口

- **Alternative A:** 为每个新 mock 完整实现 `VectorStoreService` 接口的所有方法。
- **Alternative B:** 新 mock 嵌入已有 mock (如 `fakeVectorStore` / `recordingVectorStoreService`), 仅 override 需要的方法。
- **Choice:** Alternative B
- **Pros/Cons:** A 产生大量样板代码 (~50 行/mock)。B 利用 Go struct embedding 的方法提升, 只需 override 目标方法, 代码量减少 ~80%。与同文件已有的 embedding 模式一致 (如 `recordingVectorStoreService` 嵌入 `fakeVectorStoreService`)。

## 4. Open Questions

### 4.1 伪向量 fallback 何时移除

- **Question:** 当所有调用方都已传预计算 vector 后 (预计步骤 11 删除旧路径之后), 是否应将 `InsertDocuments` 无 vector 时的行为从伪向量 fallback 改为 `ErrInvalid` 报错?
- **Impact:** 伪向量数据存入 Milvus 后无法被向量检索召回 (cosine 相似度极低), 是"存了也检索不到"的垃圾数据。
- **Suggestion:** 在步骤 11 (删除旧路径 + 清 allowlist) 验证无旧调用方走 fallback 路径后, 新建独立 issue 修改。

### 4.2 `go test -run TestVectorStore` AC 命令匹配零个测试

- **Question:** Issue AC 要求 `go test ./pkg/adapters/runtime/... -run TestVectorStore` 通过, 但该 `-run` pattern 匹配零个测试 (实际测试名为 `TestLocalVectorStoreService*`)。
- **Impact:** 严格按 AC 命令运行会显示 "no tests ran" 而非失败。实质等价的 `TestLocalVectorStoreService` 运行全部 14 个测试通过。
- **Note:** 这是 AC 中的 pre-existing 命名问题, 非本 issue 引入。后续 issue AC 建议使用更精确的 `-run` pattern。

### 4.3 Milvus REST API delete 不返回 deleted_count

- **Question:** `DeleteDocuments` 的 `deleted_count` 在生产路径 (Milvus REST API) 始终返回 0, 因为 Milvus `/v2/vectordb/entities/delete` 响应 `data` 为空对象 `{}`, 不包含删除计数。
- **Impact:** 调用方无法确认实际删除了多少条文档。
- **Suggestion:** 这是 Milvus REST API 的已知限制, 非 issue #028 引入。如需精确删除计数, 可考虑先 search 再 delete, 或使用 Milvus gRPC SDK (可能返回 delete count)。

## 5. Verification Commands Run

| Command | Result |
|---------|--------|
| `go test ./pkg/adapters/runtime/... -run TestLocalVectorStoreService -count=1` | 14/14 PASS |
| `go test ./services/ani-gateway/internal/router/... -run TestVectorStoreAPI -count=1` | 16/16 PASS |
| `go test ./pkg/adapters/vectorstore/... -run TestMilvus -count=1` | PASS |
| `python scripts/validate_component_imports.py --root .` | `component import guard passed` |
| `git diff --check` | PASS |
| `go vet ./pkg/adapters/runtime/... ./services/ani-gateway/internal/router/...` | PASS |
| `go build ./pkg/adapters/runtime/... ./services/ani-gateway/...` | PASS |
| E2E: `test_vector_store_e2e.ps1` (ani-gateway + real Milvus 10.10.1.66:31930) | 9/9 ALL PASS |

## 6. References

- Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-028-feature-core-vector-content-impl.md`
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` §1.1 (步骤 1.1), §1.4-3 (searchVectorStore 返回 content), §3.1 (Core 不做 embedding 推理)
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- Dependency: `repo/development-records/RAG-REFACTOR-STEP-1-CONTRACT.md` (issue #024 契约)
- CLAUDE.md: §3.1 (Core 不含模型推理), §4.1 (先改 API 契约), §4.4 (新增可选字段不破坏性变更)
