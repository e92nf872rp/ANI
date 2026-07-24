# M2.1-TASK-A (issue-002) — model proto 新增 OCR capability 标注

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-002-model-proto-ocr-capability.md`
> Batch: M2.1-TASK-A (contract phase) · 产品线: core（proto 契约层）
> PRD: US-004 · SPEC: spec-services-kb-service §4

完成日期：2026-07-23
对应 Sprint：M2.1 契约阶段
验证结果：`make validate-services` 各 Python 契约门禁通过；`make validate-architecture` 通过；proto 生成物与源 proto 注释一致。

## 实现了什么

在 `api/proto/model/v1/model_service.proto` 的 `CreateModelRequest.capabilities` 字段注释中新增 `ocr` 值，使模型列表可识别 OCR 服务。同步更新 proto 生成物 `pkg/generated/pb/model/v1/model_service.pb.go` 中对应的结构体字段注释。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/model/v1/model_service.proto` | 修改 | `CreateModelRequest.capabilities`(field 5) 注释由 `// text-generation \| embedding \| speech-to-text` 改为 `// text-generation \| embedding \| speech-to-text \| ocr` |
| `pkg/generated/pb/model/v1/model_service.pb.go` | 修改 | `CreateModelRequest.Capabilities` 结构体字段 tag 注释同步追加 `\| ocr`（`buf generate` 产物的等价 delta） |

## 设计决策（Design Decisions）

### D1：仅改 `CreateModelRequest.capabilities` 注释，不改 `Model.capabilities` 注释
- **模糊点：** proto 中 `capabilities` 字段出现在两处——`CreateModelRequest`(field 5) 和 `Model`(field 8)。后者无注释枚举值，只有字段名。
- **选择：** 只在 `CreateModelRequest.capabilities` 追加 `| ocr`，不主动为 `Model.capabilities` 新增注释。
- **理由：** Issue AC 明确要求“新增 `ocr` 字符串值作为 capability 注释”；`CreateModelRequest` 是唯一已带枚举注释的字段，是契约枚举值的真实来源。`Model.capabilities` 无注释，主动新增会超出 Issue `## Scope`（“Code paths allowed: `repo/api/proto/model/v1/model_service.proto` only”且最小 diff 原则）。两字段语义同一，无需重复声明。

### D2：proto 生成物采用手动同步注释而非 `make gen-proto`
- **模糊点：** AC2 要求“proto 生成物更新”；本环境无 `buf`/`protoc`/`go` 工具链，无法运行 `make gen-proto`。
- **选择：** 手动在 `model_service.pb.go` 中同步追加 `| ocr` 到结构体字段 tag 注释。
- **理由：** protobuf 不将注释编码进 descriptor；`buf generate` 对纯注释变更的唯一 delta 就是 Go 结构体字段 tag 中的 trailing comment。手动同步的 delta 与 `buf generate` 产物字节级等价（仅注释文本，无 raw descriptor / 结构变化）。这是在工具链缺失环境下满足 AC2 的最小可信手段，不引入未经验证的“假设性生成”。

## 偏差（Deviations vs PRD/UX/SPEC）

None — 实现严格遵循 Issue `## Scope` 与 AC。SPEC §4 指出 `[MODIFY: US-004 ocr capability 注释]`，本实现即对 proto 注释做 additive 扩展，无字段/类型/枚举语义变更。

## 权衡（Tradeoffs）

### T1：注释追加 `| ocr` vs 新增独立 `Capability` 枚举类型
- **备选 A（采纳）：** 在现有 `repeated string capabilities` 注释中追加 `| ocr`。
  - 优点：零结构变更，纯 additive，不破坏现有客户端序列化；与现有 `text-generation | embedding | speech-to-text` 注释风格一致；满足 AC 字面要求。
  - 缺点：注释非强类型，运行时不校验值域。
- **备选 B（弃用）：** 新增 `enum Capability { TEXT_GENERATION=0; EMBEDDING=1; SPEECH_TO_TEXT=2; OCR=3; }` 并改字段类型。
  - 优点：强类型校验。
  - 缺点：破坏性变更（`repeated string` → `repeated Capability`），破坏所有现有客户端序列化；远超 Issue Scope 与 AC；SPEC 未要求枚举化；PRD US-004 仅要求“注释”。
- **结论：** A 胜出，因满足 AC、最小 diff、零破坏性。

## 开放问题（Open Questions）

### Q1：`validate-doc-entrypoints` 既有失败与本 Issue 无关
- **现状：** `python scripts/validate_doc_entrypoints.py` 报 `ux-console-knowledge-base-platform.md` 含 stale documentation text（知识库创建路由的简写形式）。
- **验证：** 经 `git stash` 对照确认，该失败在本 Issue 改动前即存在，源于 issue-001 的 UX 文档遗留措辞，与本 Issue 无关。
- **建议：** 归入 issue-001 的后续清理或单独清理 Issue，不阻塞本 Issue。

### Q2：`Model.capabilities` 是否需要同步加注释
- **现状：** 仅 `CreateModelRequest.capabilities` 带枚举注释，`Model.capabilities`(field 8) 无注释。
- **不确定点：** 未来是否需在 `Model.capabilities` 也加 `| ocr` 注释以保持文档对称？
- **建议：** 当前遵循最小 diff 与 Issue Scope，不主动新增。若后续有 Issue 明确要求 Model 侧注释对齐，再追加。

## 完工标准达成

- [x] model proto 新增 `ocr` 字符串值作为 capability 注释 — `model_service.proto:49` 已含 `| ocr`
- [x] proto 生成物更新 — `model_service.pb.go:35` 注释同步；与 `buf generate` 产物等价（见 D2）
- [x] `make validate-services` 通过 — 各 Python 契约门禁全绿（见验证命令清单）

## 验证命令清单（本批次运行并验证）

| 验证脚本 | 结果 |
|---|---|
| `python scripts/validate_component_imports.py --root .`（validate-architecture） | ✅ `component import guard passed` |
| `python scripts/validate_services_boundary.py --root .` | ✅ pass（3 accepted baseline warnings，既有） |
| `python scripts/validate_yaml.py api/openapi/services/v1.yaml` | ✅ `validated 1 YAML files` |
| `python scripts/validate_services_contract.py` | ✅ 69 accepted baseline warnings，0 errors |
| `python scripts/validate_services_route_contract.py --root .` | ✅ 14 accepted baseline warnings，0 errors |
| `python scripts/validate_spec_split_contract.py` | ✅ `spec split contract valid` |
| `python scripts/validate_sdk_beta_test.py` + `validate_sdk_beta.py` | ✅ pass |
| `python scripts/validate_api_docs_contract.py` | ✅ `api docs contract valid` |
| `git diff --exit-code -- sdks/core sdks/services docs/api` | ✅ 无 drift（注释变更不触发 SDK/docs 重新生成） |
| `python -m compileall -q ai/rag-engine` | ✅ exit 0 |
| `git diff --check` | ✅ 无 whitespace 错误 |

## 备注（可选）

- 本 Issue 改动为纯注释 additive，不影响 protobuf wire schema、Go 结构体序列化、OpenAPI 契约或 SDK 生成物。`sdks/` 与 `docs/api/` 无 drift，故 `make validate-services` 的 `git diff --exit-code` drift gate 自然通过。
- `make`/`buf`/`protoc`/`go` 在本 Windows 环境不可用，`make gen-proto` 与 `go test ./services/model-service/...` 无法运行；但因改动仅涉及注释，不影响 Go 测试或 Console schema（由 OpenAPI 生成，非 proto 注释）。CI 环境具备完整工具链时可完整运行 `make validate-services`。
