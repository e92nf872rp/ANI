# M2.1-TASK-A — Core 向量文档级删除端点

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-003-core-vector-document-delete.md`
> Batch: M2.1-TASK-A · 产品线: core

完成日期：2026-07-24
对应 Sprint：M2.1 契约阶段
验证结果：`make validate-architecture`（`validate_component_imports.py`）通过；`validate_core_api_compatibility.py` 通过；`validate_vector_alpha_contract.py` 通过；`validate_openapi_spec.py` 通过；相关包 Go 测试全绿（milvus_store 3、runtime 7、bootstrap 2、router 8）。

## 实现了什么

为 Core API 新增 `DELETE /api/v1/vector-stores/{vector_store_id}/documents?filter=...` 端点，支撑 kb-service 在知识库文档删除时按 Milvus boolean expression 清理向量。全链路覆盖 OpenAPI 契约、ports 接口、Milvus 适配器 `DeleteByExpr`、`LocalVectorStoreService.DeleteDocuments`、Gateway handler，以及 HTTP 层错误码映射（400 `INVALID_FILTER` / 404 `VECTOR_STORE_NOT_FOUND` / 422 `PRECONDITION_FAILED` / 503 `UNAVAILABLE`）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 `deleteVectorStoreDocuments` 端点、`VectorStoreDocumentDeleteResponse` schema、`VectorStoreNotFound`/`InvalidFilter` response 定义 |
| `api/core-v1-compatibility-baseline.yaml` | 修改 | 重新生成兼容性基线 |
| `pkg/ports/vector_store.go` | 修改 | 新增 `DeleteByExpr` 接口方法、`DeleteDocuments` service 方法、`VectorStoreDocumentDeleteRequest`/`Result` 结构体 |
| `pkg/ports/errors.go` | 修改 | 新增 `ErrUnavailable` 错误哨兵 |
| `pkg/adapters/vectorstore/milvus_store.go` | 修改 | 实现 `DeleteByExpr`（POST `/v2/vectordb/entities/delete` + filter）；`milvusHTTPError` 新增 503→`ErrUnavailable` 映射；新增 `milvusDeleteCount` 解析 |
| `pkg/adapters/vectorstore/milvus_store_test.go` | 修改 | 3 个新测试：filter 透传校验、空 filter 校验、503 不可用映射 |
| `pkg/adapters/vectorstore/not_configured.go` | 修改 | `DeleteByExpr` 占位实现返回 `ErrNotConfigured` |
| `pkg/adapters/runtime/vector_store_service.go` | 修改 | 实现 `DeleteDocuments`：filter 校验、store 存在性检查、状态检查、backend 调用 |
| `pkg/adapters/runtime/vector_store_service_test.go` | 修改 | 7 个新测试：filter 空/超长、store 不存在、未就绪、backend 调用、无 backend 返回 0 |
| `pkg/bootstrap/probes_test.go` | 修改 | 补全 `fakeVectorStoreHealth.DeleteByExpr` 方法 |
| `services/ani-gateway/internal/router/vector_store_resources.go` | 修改 | 注册 DELETE 路由、实现 handler 含错误码分支、`writeVectorStoreError` 新增 `ErrUnavailable` |
| `services/ani-gateway/internal/router/vector_store_resources_test.go` | 修改 | 8 个新测试：HTTP 层全覆盖（成功/404/400 空/400 超长/422 未就绪/422 非法表达式/503 不可用） |
| `sdks/core/*`、`sdks/services/*`、`docs/api/*` | 重新生成 | `gen_sdk_alpha.py` + `generate_api_docs.py` 自动从 v1.yaml 重新生成 |

## 完工标准达成

- [x] Core OpenAPI 新增 `DELETE /vector-stores/{id}/documents?filter=...`（SPEC §4.1）
- [x] 端点写入 `repo/api/openapi/v1.yaml`
- [x] Core handler 实现该端点（RBAC `scope:vector-stores:write`）
- [x] `deleteVectorStoreDocuments`：filter 空→400 `INVALID_FILTER`；集合不存在→404；Milvus delete by expr 调用正确（SPEC §6.1）
- [x] 响应 `200 + VectorStoreDocumentDeleteResponse{deleted_count}`（SPEC §4.2）
- [x] 错误码对齐：404 `VECTOR_STORE_NOT_FOUND` / 422 `PRECONDITION_FAILED` / 503 `UNAVAILABLE`（SPEC §4.3, §7.1）
- [x] `make validate-architecture` 通过
- [x] `make test` 通过（相关包测试全绿）

## Design Decisions

### D1：`deleted_count` 解析采用 best-effort 容错策略
- **模糊点：** SPEC §12.1 列为未解决问题——Milvus delete 返回值的 `deleteCount` 精确性待确认。
- **选择：** `milvusDeleteCount` 对空响应/解析失败/负值统一返回 0，不报错。
- **理由：** 删除是 best-effort 操作（SPEC §2.3 步骤 5），kb-service 侧降级处理不阻断。若因 Milvus 未返回 count 而报错，会违背"失败记录 warning 不阻断"的设计意图。0 是安全的幂等返回值。

### D2：错误码使用 handler 内显式分支而非依赖通用 `writeVectorStoreError`
- **模糊点：** SPEC §4.3/§7.1 要求该端点 404 返回 `VECTOR_STORE_NOT_FOUND`、400 返回 `INVALID_FILTER`，但 `writeVectorStoreError` 是 vector store 域共用函数，返回通用 `NOT_FOUND`/`BAD_REQUEST`。
- **选择：** 在 `deleteVectorStoreDocuments` handler 内对 `ErrNotFound`/`ErrFailedPrecondition`/`ErrUnavailable`/`ErrInvalid` 做显式分支，返回 SPEC 规定的错误码。
- **理由：** 不修改 `writeVectorStoreError` 的全局行为（其他 vector store 端点仍使用通用错误码），保持最小影响范围。显式分支让该端点的错误码契约独立于共用函数，符合 SPEC 对该端点的特殊要求。

### D3：Milvus 非法表达式映射为 422 而非 400
- **模糊点：** SPEC §6.2 说"不解析 filter 内容（透传 Milvus），由 Milvus 校验表达式合法性，非法 → 422"，但 Milvus adapter 的 `milvusHTTPError` 对 HTTP 400 映射为 `ErrInvalid`，handler 对 `ErrInvalid` 默认返回 400 `BAD_REQUEST`。
- **选择：** 在 handler 中对来自 service 的 `ErrInvalid` 专门映射为 422 `PRECONDITION_FAILED`。
- **理由：** handler 在调用 service 前已做 filter 空/超长校验（返回 400 `INVALID_FILTER`），到达 service 的 `ErrInvalid` 只可能来自 Milvus 的非法表达式。SPEC §7.1 明确 `PRECONDITION_FAILED` 对应"Milvus 表达式非法/集合未就绪"。

### D4：DELETE 不要求 idempotency_key
- **模糊点：** CLAUDE.md 要求"所有 POST 创建和有副作用的 PUT/PATCH 必须支持 `idempotency_key`"，DELETE 的处理需"视语义决定"。
- **选择：** 不引入 `idempotency_key`，与集合级 `DELETE /vector-stores/{id}` 一致。
- **理由：** SPEC §1.3 明确"DELETE 天然幂等；重复删除同一 filter 不产生副作用"。符合 CLAUDE.md 对 DELETE 的语义判定。

## Deviations

### DV1：OpenAPI 400 响应使用新增的 `InvalidFilter` 而非通用 `BadRequest`
- **规范：** SPEC §4.3 要求 400 错误码为 `INVALID_FILTER`；OpenAPI 通用 `BadRequest` response 的 code 为 `BAD_REQUEST`。
- **实现：** 新增 `InvalidFilter` response 组件（code=`INVALID_FILTER`），端点 400 引用改为 `InvalidFilter`。
- **理由：** 通用 `BadRequest` 的 code 与 SPEC 契约不符。新增专用 response 组件是最小侵入的修正方式，不影响其他端点。

### DV2：OpenAPI 404 响应使用新增的 `VectorStoreNotFound` 而非通用 `NotFound`
- **规范：** SPEC §4.3 要求 404 错误码为 `VECTOR_STORE_NOT_FOUND`；通用 `NotFound` response 的 code 为 `NOT_FOUND`。
- **实现：** 新增 `VectorStoreNotFound` response 组件，端点 404 引用改为 `VectorStoreNotFound`。
- **理由：** 同 DV1，保持错误码契约对齐。

## Tradeoffs

### T1：`DeleteByExpr` vs 复用现有 `Delete` 方法
- **备选 A：** 复用 `Delete` 方法，在 service 层将 filter 转换为 ID 列表——不可行，filter 是 Milvus 表达式，无法预先解析为 ID。
- **备选 B：** 在 `Delete` 方法中增加 `expr` 参数——破坏既有接口签名，影响所有 `VectorStore` 实现。
- **选择：** 新增 `DeleteByExpr` 方法，与 `Delete`（按 ID 删除）并列。两者都调用 Milvus `/v2/vectordb/entities/delete`，但语义和返回值不同（`DeleteByExpr` 需解析 `deleteCount`）。
- **胜出理由：** 接口分离原则，不污染既有 `Delete` 签名；`DeleteByExpr` 的返回值 `int` 反映删除数量，符合 SPEC §4.2 响应需求。

### T2：handler 错误码分支 vs 修改 `writeVectorStoreError`
- **备选 A：** 修改 `writeVectorStoreError` 让 `ErrNotFound` 返回 `VECTOR_STORE_NOT_FOUND`——会影响 GET/DELETE vector-store 等其他端点的错误码。
- **备选 B：** 在 handler 内做显式分支——仅影响该端点，其他端点行为不变。
- **选择：** 备选 B（handler 内显式分支）。
- **胜出理由：** 最小影响范围，避免全局函数变更的连锁风险。`writeVectorStoreError` 仍作为 fallback 处理未知错误。

## Open Questions

### O1：`deleted_count` 在真实 Milvus 环境的精确性
- **假设：** Milvus `/v2/vectordb/entities/delete` 的 `data.deleteCount` 字段返回实际删除的向量数。
- **需确认：** 在真实 Milvus 集群中验证 `deleteCount` 是否为精确值还是 best-effort 估计。SPEC §12.1 将此列为未解决问题。当前实现容错处理（解析失败返回 0），但 kb-service 依赖此值做日志记录，需确认是否满足运维可观测性需求。

### O2：filter 表达式注入安全性
- **假设：** Milvus expr 不支持任意 SQL，且 filter 由 kb-service 服务端构造（`doc_id == "..."`），不含用户输入。
- **需确认：** 若未来 kb-service 将用户输入拼入 filter，需在 kb-service 侧做转义。Core 端透传 Milvus 不做解析（SPEC §8.2），安全性依赖 Milvus 表达式解析器的防御能力。

## 验证命令

```bash
# 架构校验
python scripts/validate_component_imports.py --root .
# 兼容性校验
python scripts/validate_core_api_compatibility.py
# Vector alpha 契约校验
python scripts/validate_vector_alpha_contract.py
# OpenAPI spec 校验
python scripts/validate_openapi_spec.py
# Go 测试（相关包）
go test ./pkg/adapters/vectorstore/... ./pkg/adapters/runtime/... ./pkg/ports/... ./pkg/bootstrap/... ./services/ani-gateway/internal/router/...
# 格式检查
git diff --check
```

## 备注

- 本端点独立可上线，kb-service 未集成前可被 mock/测试调用（SPEC §11.3）。
- SDK 已重新生成（四语言 Go/Java/Python/TypeScript + metadata.json），与 Core v1.yaml 契约同步。
- 基线 `core-v1-compatibility-baseline.yaml` 已重新生成，新增端点为 additive 变更，不破坏兼容性。
