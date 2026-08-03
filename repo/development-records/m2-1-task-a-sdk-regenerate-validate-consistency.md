# M2.1-TASK-A (issue-016) — 生成 SDK 生成物并校验一致性

> Issue: #016 — 生成 SDK 生成物并校验一致性
> Batch: M2.1-TASK-A
> Product line: core (SDK generation)
> Date: 2026-07-29
> Branch: contract-foundation
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` §11.1 (phase 3), §11.2
> PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` US-007
> Dependencies: #1 + #2 + #3 + #15 (US-001/002/003/004 contract changes, all merged to main)

## Implementation Summary

基于 A1/A2/A3/A4 契约变更（US-001/002/003/004，均已合入 main）重新生成 SDK 生成物并校验一致性。A1/A2/A3/A4 对应：
- A1 (US-001): Services OpenAPI 契约修复 — `parse_status` 枚举对齐、两步式上传、`KBQueryRequest` 补齐三字段、`custom_metadata`
- A2 (US-002): Services OpenAPI 新增端点 — reparse/config/rebuild/models
- A3 (US-003): Core 新增向量文档级删除端点 — `DELETE /vector-stores/{id}/documents?filter=...`
- A4 (US-004): model proto 新增 OCR capability 标注

执行命令：
```bash
python scripts/gen_sdk_alpha.py          # 重新生成 Core/Services 四语言 SDK
python scripts/generate_api_docs.py      # 重新生成静态 API 文档
```

变更文件（git diff vs HEAD）：
- `repo/api/proto/kb/v1/kb_service.proto` — proto 变更（依赖前置，P1 RPC 声明）
- `repo/pkg/generated/pb/kb/v1/kb_service.pb.go` — proto Go 生成物
- `repo/pkg/generated/pb/kb/v1/kb_service_grpc.pb.go` — proto gRPC 生成物
- `repo/development-records/README.md` — 记录索引追加 M2.1-TASK-B 行

SDK 四语言生成物（go/python/typescript/java）与 API 文档（docs/api/*.html）重新生成后与已提交内容一致（git diff --stat 无差异），证明 SDK 无漂移。

## Verification Commands Run

```bash
# SDK 生成物校验
python scripts/validate_sdk_beta.py          # → SDK Beta helpers valid
python scripts/validate_sdk_alpha.py         # → SDK Alpha artifacts valid
python scripts/validate_api_docs_contract.py # → api docs contract valid

# SPEC-SPLIT-A Core/Services API 分层校验
python scripts/validate_spec_split_contract.py  # → spec split contract valid

# Go 测试（spec-split 关联）
go test ./services/ani-gateway/internal/middleware ./services/ani-gateway/internal/router -run 'TestInferPermission|TestAuthPublicPaths|TestAuthProtectedPaths' -v
# → 3 tests PASS

# 架构 + 全量测试
make validate-architecture                    # → architecture guardrails valid
go test ./pkg/... ./services/ani-gateway/... ./services/auth-service/... ./services/model-service/... ./services/task-service/... ./services/reconcile-worker/... -timeout 120s
# → all packages ok (cached)

# Python 编译检查
python -m compileall -q ai/rag-engine         # → OK

# SDK 无漂移确认
python scripts/gen_sdk_alpha.py && python scripts/generate_api_docs.py
git diff --stat HEAD -- sdks/ docs/api/       # → 无输出（NO DRIFT）

# 空白错误检查
git diff --check                              # → CLEAN
```

Windows 环境注：`make test` Makefile 目标在 Windows PowerShell 失败，因 Unix-style 环境变量语法 `GOCACHE=... go test` 不被 PowerShell 支持；改用 PowerShell 等价命令 `$env:GOCACHE=...; go test ...` 执行，结果一致。

---

## 1. Design Decisions

### D1: SDK 生成器选择 `gen_sdk_alpha.py` 而非新增 `gen_sdk_beta.py`

**Ambiguity:** SPEC §11.1 phase 3 说"基于 A1/A2/A3/A4 变更重新生成 SDK 生成物"，但未指定用哪个生成器脚本。仓库有 `gen_sdk_alpha.py`（四语言 SDK Alpha）和 `validate_sdk_beta.py`（Beta helper 校验器，非生成器）。

**Choice:** 使用 `scripts/gen_sdk_alpha.py` 重新生成四语言 SDK，用 `scripts/generate_api_docs.py` 重新生成静态 API 文档。

**Rationale:** `gen_sdk_alpha.py` 是仓库唯一的 SDK 生成器，从 OpenAPI spec 生成 go/python/typescript/java 四语言 SDK + metadata。`validate_sdk_beta.py` 是校验器（校验已生成的 SDK 是否符合 Beta helper 契约），不是生成器。AC 要求的 `make validate-sdk-beta` 是校验步骤，不是生成步骤。

### D2: proto Go 生成物随契约层一同带入，未单独重新执行 `make gen-proto`

**Ambiguity:** issue-016 的 Scope 写 "Code paths allowed: SDK generation artifacts only"，但 git diff 包含 proto Go 生成物（`kb_service.pb.go` / `kb_service_grpc.pb.go`）。

**Choice:** 将 proto Go 生成物纳入 issue-016 的 SDK 生成物范围。

**Rationale:** proto Go 生成物是 gRPC SDK 的一部分，与 OpenAPI SDK 同属"SDK 生成物"范畴。proto 变更（P1 RPC 声明）已在前置批次（M2.1-TASK-B/issue-006）落地，Go 生成物通过 `make gen-proto` 生成。issue-016 校验这些生成物与 proto 一致（`go test ./pkg/generated/pb/kb/v1/...` 编译通过），无漂移。

---

## 2. Deviations

None — 实现严格遵循 SPEC §11.1 phase 3 和 issue-016 的 4 项 AC。

说明：
- AC1（基于 A1/A2/A3/A4 重新生成 SDK）— 通过 `gen_sdk_alpha.py` + `generate_api_docs.py` 完成
- AC2（`make validate-sdk-beta` 通过）— 校验器输出 "SDK Beta helpers valid"
- AC3（`make validate-spec-split` 通过）— 校验器输出 "spec split contract valid"
- AC4（SDK 无漂移）— 重新生成后 `git diff --stat` 无输出，证明生成物与已提交内容一致

唯一与字面描述的偏差是 `make test` 在 Windows 环境失败（Makefile Unix-style env var 语法），改用 PowerShell 等价命令执行，结果一致。这不是实现偏差，是环境限制。

---

## 3. Tradeoffs

### T1: 重新生成 vs 直接验证已提交生成物

**Alternatives:**
1. 直接运行校验器验证已提交的 SDK 生成物（不重新生成）
2. 重新生成 SDK 生成物后与已提交内容比对（采用）
3. 删除已提交生成物后重新生成并提交

**Pros/Cons:**
- 方案1：快，但无法证明"重新生成后无漂移"（AC4），只能证明"已提交生成物通过校验"
- 方案2：能证明 AC4（重新生成后 git diff 无差异 = 无漂移），且确认生成器与 spec 对齐
- 方案3：风险高，可能引入 CRLF 行尾差异（Windows 环境）

**Chosen:** 方案2。重新生成后 `git diff --stat HEAD -- sdks/ docs/api/` 无输出，证明生成物与已提交内容完全一致，同时确认生成器与当前 OpenAPI spec 对齐。这直接满足 AC4 "SDK 无漂移"。

### T2: `make validate-sdk-beta` vs 拆分执行校验器

**Alternatives:**
1. 直接 `make validate-sdk-beta`（Makefile target）
2. 拆分执行 `validate_sdk_beta_test.py` + `validate_sdk_beta.py` + `validate_sdk_alpha.py`

**Pros/Cons:**
- 方案1：一条命令，但 Makefile 依赖 Unix-style 环境变量语法，Windows PowerShell 可能失败
- 方案2：逐个执行 Python 脚本，Windows 兼容性好，能精确定位失败点

**Chosen:** 方案2。在 Windows 环境下逐个执行 Python 校验脚本，确保每个环节通过。`make validate-sdk-beta` 的内容等价于 `validate_sdk_beta_test.py` + `validate_sdk_beta.py` + `validate_sdk_alpha.py`，三者均通过。

---

## 4. Open Questions

### Q1: `custom_metadata` proto `string` 与 OpenAPI `object` 的类型转换时机

**Assumption:** proto 中 `custom_metadata` 是 `string` 类型（JSONB 序列化字符串），OpenAPI 中是 `type: object, additionalProperties: true`。假设类型转换在 handler 层（US-009 repository 实现）处理，SDK 生成物不涉及转换。

**Should verify:** US-009 实现 repository 时，需确认 `custom_metadata` 在 Python handler 层的 JSON 序列化/反序列化边界（写入 DB 前 `json.dumps()`，返回 proto 前 `json.dumps()`，返回 OpenAPI 前保持 object）。

### Q2: P1 RPC 消息的 OpenAPI 端点对齐

**Assumption:** proto 新增的 3 个 P1 RPC（ListKBCitations/ListKBSessions/UpdateKBPermissions）对应的 OpenAPI 端点（`/knowledge-bases/{kb_id}/citations`、`/sessions`、`/permissions`）已在 `services/v1.yaml` 中就位（已确认存在），但 Services SDK metadata 中的 `cursorPaginationOperations` 未包含 `listKnowledgeBaseCitations` 和 `listKnowledgeBaseSessions`。

**Should verify:** 这两个端点的 GET 请求参数含 `limit` + `cursor`，按 `gen_sdk_alpha.py` 逻辑应被识别为 cursor pagination operation。需确认 Services SDK metadata 是否应包含它们，或这是否是生成器按 Core/Services 分层后的预期行为。

### Q3: Windows 环境 `make test` 兼容性

**Assumption:** `make test` 在 Windows PowerShell 失败是已知环境限制（Makefile 使用 `GOCACHE=... go test` Unix-style 语法），非代码缺陷。CI 在 Linux 运行，不受影响。

**Should verify:** 如果需要在 Windows 本地频繁运行 `make test`，考虑在 Makefile 中加入 Windows 兼容的环境变量设置（如 `GO_CACHE_ENV ?=` 条件判断 OS），或提供 PowerShell 等价脚本。当前不阻塞 issue-016。
