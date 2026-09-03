# KB-API-B3 — issue-042 契约层：ReparseDocument proto + 两侧生成物

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-042-b3-contract-reparse-proto.md`
> Batch: KB-API-B3 (contract phase) · 产品线: core（Services 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

完成日期：2026-09-02
分支：`feat/kb-api-completion`（基于 main `963bc88`，B1 issue-040 / B2 issue-041 改动同分支叠加）
验证结果：validate-services 全链路逐阶段验证——boundary / validate-yaml / inference-control-plane / services-contract（112 accepted baseline warnings，零新增）/ **spec-split / sdk-beta / SDK+docs 重生成幂等（8 文件 MD5 字节级一致）/ doc-api / doc-entrypoints / model+inference go test / kb 两侧 pb import smoke / validate-architecture 全绿**；`git diff --check` 通过；route-contract 5 条 `spec_not_in_code`（B1 2 + B2 3）为 SPEC §4.2 规定的同 PR 解除项（issue-044/#046 Gateway 批次），本批次零新增。review-it 收口审查 1 accepted finding 已修复（见 OQ1，SDK 欠账发现与补齐）；本地环境噪音（venv / `.run/gomodcache`）与 B2 OQ4 同类，验证时临时移出、验证后原样恢复。

## 实现了什么

为 KB-API-B3 批次补齐 ReparseDocument 的 gRPC proto 契约并同步两侧生成物：`api/proto/kb/v1/kb_service.proto` 新增 `rpc ReparseDocument(ReparseDocumentRequest) returns (common.v1.AsyncTaskRef)`（service 块末尾、DeleteSession 之后）+ `ReparseDocumentRequest` message（tenant_id=1 / kb_id=2 / doc_id=3 / idempotency_key=4，对齐 NotifyDocumentUploadedRequest 模式）；buf 生成 Go 侧 pb + grpc、grpc_tools 生成 Python 侧 pb2/pyi/grpc。**零 OpenAPI 改动、零 contract-baseline 改动**——REST 契约（v1.yaml `reparseKnowledgeBaseDocument`）与 baseline 豁免（L44-46）均已在前置批次冻结，本 issue Scope 即"纯 proto 补齐"。无 servicer / Gateway 实现（grpc base class 未 override 时返回 UNIMPLEMENTED，运行时零风险）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | service 块 L64-68 追加 ReparseDocument RPC（注释完整说明 202 异步语义：Outbox → NATS `ani.tasks.kb.parse`，复用 NotifyDocumentUploaded 事件管线）；L380-385 追加 ReparseDocumentRequest（4 字段，idempotency_key 注释标明 client-generated uuid; replay-safe, required） |
| `pkg/generated/pb/kb/v1/kb_service{,_grpc}.pb.go` | 重新生成 | buf 生成：ReparseDocumentRequest struct + Getter + client/server/handler 方法（ReparseDocument RPC 全套） |
| `services/kb-service/app/generated/kb/v1/` ×3 | 重新生成 | grpc_tools 生成：pb2.py / pb2.pyi / pb2_grpc.py（ReparseDocumentRequest 4 字段 + ReparseDocument stub，`__init__` unary_unary 正确生成） |

OpenAPI `v1.yaml` / `services-contract-baseline.yaml` **无 diff**（AC #4 冻结事实验证）；SDK/docs/schema.d.ts 因 OpenAPI 未变更本应零漂移——review-it 发现的历史欠账补齐见 OQ1。

## 设计决策（Design Decisions）

### D1：AsyncTaskRef 返回类型复用 NotifyDocumentUploaded 既有模式（而非新 message / google.protobuf.Empty）
- **模糊点：** SPEC §2.4 规定返回 `common.v1.AsyncTaskRef`，但 proto 无既有 reparse response message，理论上可仿 B2 DeleteSession→Empty 或新建 response。
- **选择：** 严格按 SPEC 返回 `common.v1.AsyncTaskRef`，与同文件 `NotifyDocumentUploaded`、model `ImportModel`、inference `CreateInferenceService` 模式完全一致。
- **理由：** REST 侧 `reparseKnowledgeBaseDocument` 202 响应（AsyncTaskRef schema：task_id/type/resource_id）已冻结于 v1.yaml:2379-2385，proto 直接复用 common 类型零翻译损耗；202 异步任务语义下自建 message 属于无信息量重复。

### D2：idempotency_key 为 proto 字段必填（注释约束）而非仅 REST 层约束
- **选择：** `idempotency_key = 4` 注释标明 required；REST 侧 v1.yaml 该字段本就 required，双端一致。
- **理由：** proto3 无 required 概念，字段级注释 + REST required + #047 servicer 实现时校验（缺失返回 INVALID_ARGUMENT）三层约束链；与 NotifyDocumentUploadedRequest L105-110 既有模式逐字对齐，不引入新形态。

### D3：RPC 注释显式写明 outbox→NATSGo 复用管线（而非仅"re-parse"一笔带过）
- **选择：** 注释三行：re-trigger 语义 / 202-style async task / Outbox pattern onto NATS `ani.tasks.kb.parse` (reuses the NotifyDocumentUploaded event pipeline)。
- **理由：** issue AC #1 明确要求注释说明 202 异步任务语义；同时把 #047 实现批次的管线复用决策（不新建 subject、不加 `.v2` 切换）固化在契约源文件，后续开发者读 proto 即知实现意图。

## 偏差（Deviations vs PRD/UX/SPEC）

None — proto 改动与 issue AC 逐条对照一致（RPC 签名/位置、message 字段与编号、注释内容、两侧生成物同步、OpenAPI/baseline 零 diff）；生成物为工具产物无人工内容。review-it 收口审查发现并修复的 SDK 历史欠账不属本批次偏差（B2 批次遗漏、本批次发现并补齐，见 OQ1）。

## 权衡（Tradeoffs）

### T1：servicer 不在本批次实现，接受 UNIMPLEMENTED 缺省（沿用 B1/B2 T2 决策模式）
- **备选 A（采纳）：** 仅 proto + 生成物，kb-service ReparseDocument servicer 留待 issue-047。
  - 优点：严格批次边界（issue Type: core contract，allowed paths 不含 `app/api/`）；REST 侧路由已注册（既有）但 Gateway→gRPC 转发本就未接（#048），proto stub 无消费方即零运行时风险；两侧 pb 先行使 #047/#048 可并行开发。
  - 缺点：proto 声明到 #047 实现落地之间，gRPC 调用返回 UNIMPLEMENTED（不可达路径，见下）。
- **备选 B（弃用）：** 本批次顺带实现 servicer。
  - 弃用理由：违反 issue Scope；kb-service 属冻结 Services 后端，跨批次改动破坏 review 边界。

### T2：不动 `kb_parse_consumer_enabled` 标志与 NATS subject 版本化（`.v2` vs legacy）——留给 #047 决策
- **模糊点：** NATS 消费存在 legacy `ani.tasks.kb.parse`（rag-engine 消费）与 `.v2`（kb-service 自身消费）双 subject，由 `kb_parse_consumer_enabled` 切换；proto 注释选择了 legacy 名。
- **选择：** 注释按 Plan §6.3（复用 outbox 管线）写 legacy subject 名；subject 版本化决策显式留给 #047 实现批次。
- **理由：** 契约层固化的是"复用 NotifyDocumentUploaded 事件管线"这一架构事实；subject 名是部署级实现细节，kb-service 已有标志位处理双 subject，#047 实现时按当时消费方归属（rag-engine 是否已退役）最终拍板。proto 注释即使偏差也只是文档级，wire 格式不受影响。

## 开放问题（Open Questions）

### OQ1：B2 批次 SDK 历史欠账（review-it 发现并已修复，本记录补登）
review-it 收口审查发现 SDK 四语言 + sdk-metadata.json + docs/api 仅含 B1 增量，**缺失 B2 的 3 operation**（listKnowledgeBaseDocumentChunks / listKnowledgeBaseSessionMessages / deleteKnowledgeBaseSession）**+ 5 schema**（KBChunk/KBSessionMessage/KBSourceChunk/KBChunkListResponse/KBSessionMessageListResponse）。
- **根因：** `make validate-services` 在 route-contract（预期中间态红，L1223）**短路**，L1227-1229 的 SDK 重生成+零漂移检查从未执行，B2 批次漏做此步且无门禁兜底；B2 记录"验证结果"栏所列零漂移双门禁实际未跑到该步骤（记录存疑教训：中间态红的门禁链必须逐阶段单跑补证）。
- **处置：** 本审查中运行 `gen_sdk_alpha.py` + `generate_api_docs.py` 补齐，SDK diff 从 6 行/文件增至 19 行/文件；二次重生成 MD5 幂等验证（8 文件字节级一致）；幂等集自动收录 `updateKnowledgeBase`（B1）、cursor 分页集收录 2 个 B2 列表操作均验证在位。
- **遗留：** B2 开发记录的"验证结果"与"关键文件改动"两节按事实保留原文（其 SDK 重生成描述对应欠账状态），以本 OQ 为准勘误。

### OQ2：route-contract 门禁合入前仍红（5 条，B1 2 + B2 3），#044/#046 同 PR 解除
与 B1 T1 / B2 T1/OQ1 同一约定：SPEC §4.2 规定新 path 必须与 Gateway 路由注册同一 PR 落地。本批次零新增红。合入顺序：#040/#041/#042（契约）→ #043/#045（servicer）/ #044/#046（Gateway 路由）同 PR 合入后门禁全绿。

### OQ3：#047 实现批次注意事项——doc 行 reset 与幂等重放语义
AC 注释声明 "resets the doc row and enqueues a reparse task"。#047 实现时需确认：① doc 行 reset 的字段范围（state/status/chunk 关联清理，不误删 source blob 引用）；② idempotency_key 重放路径——重复 reparse 请求在任务未完成时返回原 task_id（Outbox 幂等表）而非重复入队；③ NATS subject 版本化拍板（见 T2）。

### OQ4：本地环境噪音（CI 干净 checkout 不存在，已验证 + 原样恢复）
与 B1/B2 OQ4 同类：boundary 校验扫 `.venv`（joblib 编码夹具 / torch py312 语法夹具 / pymilvus 禁用模块）、architecture 校验扫 `.run/gomodcache`（validator 仅排除 vendor/.cache）。验证时临时移出、验证后 MD5 无关的原样移回（venv 恢复 `ai/rag-engine/.venv`，`.run` 10622 文件往返无损）。另：`make validate-spec-split` 内 Go 步骤在 Windows 因 POSIX env 前缀语法（`GOCACHE=... go test`）失败，以 PowerShell 等价环境变量命令补跑通过——CI Linux 不受影响。

### OQ5：validate-services 门禁链短路结构（流程观察，供 #048 参考）
route-contract 短路点位于 SDK 零漂移检查之前，意味着**契约批次中间态期间 SDK/docs 漂移永远无门禁兜底**（OQ1 的直接根因）。#044/#046 解除路由豁免后门禁恢复全链路可达；可选加固：把 SDK 重生成+零漂移移到 route-contract 之前（生成物与路由实现无关，顺序无依赖）。另 `validate_component_imports.py` 可考虑把 `/.run/` 纳入排除清单（当前仅 vendor/.cache）。

## 验证命令（已运行）

```
python scripts/validate_services_boundary.py --root .    # venv 移出后通过（本地噪音 OQ4）
python scripts/validate_yaml.py api/openapi/services/v1.yaml
make validate-inference-control-plane / validate-services-contract   # 112 accepted（零新增）
python scripts/validate_spec_split_contract.py            # spec split contract valid
go test ./services/ani-gateway/internal/{middleware,router} -run 'TestInferPermission|TestAuthPublicPaths|TestAuthProtectedPaths' -v   # 全过（make 内 POSIX env 语法 Windows 失败，等价补跑）
make validate-sdk-beta                                    # SDK Beta + Alpha valid
python scripts/gen_sdk_alpha.py && python scripts/generate_api_docs.py   # 双次运行 MD5 幂等（OQ1 补齐后）
python scripts/validate_api_docs_contract.py / validate_doc_entrypoints.py(+_test.py)
go test ./services/model-service/... ./services/inference-service/...   # 全绿
python -m compileall -q ai/rag-engine                     # 通过（venv 移出后）
npm --prefix frontends/console run gen-api                # schema.d.ts/core-schema.d.ts 重跑 MD5 幂等
make validate-architecture                                # guardrails valid（.run/gomodcache 移出后）
python（import smoke）                                    # kb pb2/pyi/grpc：ReparseDocumentRequest 4 字段 + stub 验证
git diff --check                                           # exit 0
git diff -- api/openapi/services/v1.yaml architecture/services-contract-baseline.yaml   # 空（AC #4 冻结事实）
```

> 注：`make validate-services` 完整链路因 route-contract 预期中间态红（OQ2）短路，全部后续阶段逐阶段单跑补证（skill 契约：中间态红不掩盖下游验证）；CI 干净 checkout 下 #044/#046 合入后预期全链路绿。
