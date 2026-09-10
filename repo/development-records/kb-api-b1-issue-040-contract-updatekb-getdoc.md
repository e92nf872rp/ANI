# KB-API-B1 — issue-040 契约层：UpdateKB / GetDocument 契约 + proto + 生成物

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-040-b1-contract-updatekb-getdoc.md`
> Batch: KB-API-B1 (contract phase) · 产品线: core（Services 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

完成日期：2026-09-02
分支：`feat/kb-api-completion`（基于 main `963bc88`）
验证结果：validate-services-contract（109 accepted baseline warnings，含新 2 条豁免）/ validate-services-boundary / validate-spec-split / validate-doc-api / validate-doc-entrypoints / validate-sdk-beta / 零漂移双门禁（SDK-docs + Console schema）/ validate-architecture / `git diff --check` 全部通过；Go 侧 model-service / inference-service / ani-gateway / tenant-service build+test 全绿；kb-service pytest 223 passed + 1 failed（main HEAD 既有失败，stash 对比法归因）。route-contract 2 条 `spec_not_in_code` 为 SPEC §4.2 规定的同 PR 解除项（issue-044 Gateway 层）。

## 实现了什么

为 KB-API-B1 批次（SPEC §4.3 #5 KB 更新、#10 文档详情）补齐契约层：`api/openapi/services/v1.yaml` 既有 path `/knowledge-bases/{kb_id}` 追加 PUT `updateKnowledgeBase`（200 返回 `KnowledgeBase`，请求体新 schema `UpdateKnowledgeBaseRequest`）、既有 path `.../documents/{doc_id}` 追加 GET `getKnowledgeBaseDocument`（200 返回既有 `KBDocument`）；`api/proto/kb/v1/kb_service.proto` 新增 `UpdateKB` RPC + `UpdateKBRequest` message（GetDocument RPC 已存在，仅校验）；2 个新 operationId 登记 `services-contract-baseline.yaml` `operation_security` 豁免；buf 生成 Go 侧 pb + grpc_tools 生成 Python 侧 pb；下游生成物（四语言 SDK `sdks/services/`、`docs/api/services.html`+index.html、Console `schema.d.ts`）全部重生成。纯新增契约，无破坏性变更，不含任何 handler/servicer/Gateway 实现。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改（3 处纯新增） | PUT `updateKnowledgeBase`（200/400/401/403/404/409）挂既有 KB path；GET `getKnowledgeBaseDocument`（200/401/403/404）挂既有 documents path；新组件 schema `UpdateKnowledgeBaseRequest`（idempotency_key required uuid / name / description nullable） |
| `api/proto/kb/v1/kb_service.proto` | 修改 | `rpc UpdateKB(UpdateKBRequest) returns (KnowledgeBase)`（插在 ListKBs 与 DeleteKB 之间，CRUD 分组惯例）；`UpdateKBRequest` 字段编号 1-5（tenant_id/kb_id/idempotency_key/name/description），注释注明 empty=keep 语义 |
| `architecture/services-contract-baseline.yaml` | 修改 | 追加 `updateKnowledgeBase` / `getKnowledgeBaseDocument` 两条 `operation_security` 豁免（reason 与既有 18 条同文） |
| `pkg/generated/pb/kb/v1/kb_service{,_grpc}.pb.go` | 重新生成 | buf 生成：UpdateKBRequest struct + 索引位移；client/server/handler 方法 |
| `services/kb-service/app/generated/kb/v1/` ×3 | 重新生成 | grpc_tools 生成：pb2.py / pb2.pyi / pb2_grpc.py（UpdateKBRequest + UpdateKB stub） |
| `sdks/services/*` ×7 | 重新生成 | 四语言 OPERATIONS/PATHS/SCHEMAS + `updateKnowledgeBase` 自动登记 IDEMPOTENCY_OPERATIONS |
| `docs/api/{index,services}.html` | 重新生成 | Services Operations 115→117 |
| `frontends/console/src/api/schema.d.ts` | 重新生成 | put/get 操作 + `UpdateKnowledgeBaseRequest` + operations 定义（+80 行） |

## 设计决策（Design Decisions）

### D1：UpdateKB 部分更新语义在 proto 注释固化 "empty = keep"，REST 侧由 `description: nullable` 承接
- **模糊点：** SPEC §4.3 #5 要求"空字段=不修改"（partial update 语义），但 proto3 string 无法表达 null/absent 区别。
- **选择：** proto `UpdateKBRequest` 注释显式写 `empty string = keep current value`；OpenAPI 侧 `name` 非 nullable、`description` nullable（与 SPEC 冻结 schema 逐字一致）。
- **理由：** proto→SQL 转换层（issue-043 将用 `COALESCE(NULLIF($n,''), col)`）天然把空串与 null 均收敛为"不修改"，双注释对齐消除两侧语义分叉。清空 description 本批次不支持（nullable 仅表达"字段缺省=不改"），属 SPEC 有意的简化边界。

### D2：PUT `updateKnowledgeBase` 声明 409 响应，冲突检测依赖 DB 唯一约束而非应用层预查询
- **模糊点：** SPEC §6.1 新增 409 ALREADY_EXISTS（本批次错误分类），但未规定检测时机。
- **选择：** 契约声明 409；实现路径（issue-043）走 `knowledge_bases UNIQUE(tenant_id, name)` 约束触发 23505 → ALREADY_EXISTS → 409，不做 SELECT-then-UPDATE 预查询。
- **理由：** 避免 TOCTOU 竞态（两个并发改名到同名可同时通过预查询）与一次额外往返；与 kb-service `_create_kb` 既有错误映射模式一致。

### D3：新操作沿用 `operation_security` baseline 豁免而非声明 `security`（与 m2-1-task-a D2 同模式）
- **选择：** 2 个新 operationId 各加一条 accepted_baseline，与 KB 域既有豁免完全同格式。
- **理由：** v1.yaml 全局未声明 security scheme，Services 契约统一走豁免；auth 声明属独立批次，本批次不越权。

### D4：GET `getKnowledgeBaseDocument` 不复用既有 GET KB doc 端点而挂 documents 子路径
- **模糊点：** v1.yaml 已有 GET `/knowledge-bases/{kb_id}`（返回 KB 本体），文档详情是否单列。
- **选择：** 按 SPEC §4.3 #10 冻结设计挂 `.../documents/{doc_id}`，operationId `getKnowledgeBaseDocument` 与 `updateKnowledgeBase`/`updateKnowledgeBaseConfig`/`updateKnowledgeBasePermissions` 命名空间无冲突。
- **理由：** REST 资源层级与 proto `GetDocument(tenant_id/kb_id/doc_id)` 三元组一一对应；kb-service GetDocument servicer 已有实现（P0 批次落地），issue-044 只需 Gateway 路由注册即可全链打通。

## 偏差（Deviations vs PRD/UX/SPEC）

None — 手写契约 3 文件与 SPEC 冻结定义（§4.2 path/§4.3 #5/#10/§2.4 proto/§9.4 生成物清单）逐字对照一致；响应集与 §6.1 错误分类吻合。生成物为工具产物无人工内容。

## 范围外良性同步生成物（随批次保留）

buf 全量重生成携带了 proto 源先行的修正追平，已逐 diff 确认非破坏性：
- `pkg/generated/pb/tenant/v1/{tenant_plan,tenant_admin_service}.pb.go`（各 353 行）：proto `reserved` 字段（AuditLog 2/3 等）生成物欠账追平，wire 兼容
- `pkg/generated/pb/inference/control/v1/inference_control.pb.go`（11 行）：`InferenceServiceAccelerator` 注释同步
- `sdks/core/*` M 状态为纯 CRLF 行尾差异（内容 diff 为空），零漂移门禁不受影响
- 受影响 Go 消费方（pkg/generated + ani-gateway + tenant-service + model-service + inference-service）`go build` 全部通过

## 权衡（Tradeoffs）

### T1：不修改 route-baseline.yaml 登记新路径豁免，接受 route-contract 门禁红
- **备选 A（采纳）：** 不动 `services-route-baseline.yaml`，`validate-services-route-contract` 报 2 条 `spec_not_in_code`。
  - 优点：SPEC §4.2 硬约束规定新 path 必须与 Gateway 路由注册**同一 PR** 落地（防契约与实现长期漂移），登记豁免会绕过该约束；issue-044（Gateway，依赖 #040，同 PR）注册路由后门禁自然转绿，无需事后删豁免条目。
  - 缺点：issue-040 单独验证时该门禁为红（预期中间状态），需在报告中显式归因。
- **备选 B（弃用）：** 像 m2-1-task-a 那样加 `spec_not_in_code` 豁免。
  - 弃用理由：m2-1 时期无 §4.2 同 PR 硬约束（SPEC 是本次才引入）；加豁免等于把"必然同 PR 落地"的承诺降级为"无限期豁免"，B 批次还要删条目，徒增基线噪音。

### T2：UpdateKB servicer 不在本批次实现，接受 proto/生成物先行
- **备选 A（采纳）：** 契约层完整落地（proto+两侧 pb+SDK），kb-service `UpdateKB` servicer 留待 issue-043（kb-service 实现批次）。
  - 优点：严格批次边界（issue-040 Type: core contract）；新增 RPC 在 servicer 缺省时由 grpc 返回 UNIMPLEMENTED，而 Gateway 路由同样未注册（issue-044），无任何可达调用路径，运行时零风险；两侧 pb 先行使 issue-043/044 可并行开发。
  - 缺点：分支合入主 PR 前 UpdateKB 不可调用（由 043/044 同 PR 补齐，PR 级完整性不受损）。
- **备选 B（弃用）：** 本批次顺带实现 servicer。
  - 弃用理由：违反 issue Scope（allowed paths 不含 `app/api/`/`app/core/`）；kb-service 属冻结 Services 后端，跨批次改动破坏 review 边界。

## 开放问题（Open Questions）

### OQ1：route-contract 门禁合入前仍红，依赖 issue-044 同 PR 解除
`validate-services-route-contract` 的 2 条 `spec_not_in_code`（PUT updateKB / GET getKBDocument）按 SPEC §4.2 L207 必须由 issue-044 Gateway 路由注册解除。合入顺序建议：#040（契约）→ #043（servicer）→ #044（Gateway 路由）→ 同一 PR 合入 main 后门禁全绿。若 #044 延期，需决策是否临时加 route-baseline 豁免（当前倾向不加，见 T1）。

### OQ2：kb-service 既有测试失败 `test_process_message_missing_object_id_dropped`（main HEAD 即失败）
`tests/test_parse_consumer.py` 该用例在干净基线（stash 生成物后）同样失败（1 failed, 19 passed），与本批次无关。属 main 上待修缺陷，建议独立 issue 跟踪，避免 B 批次合入时误归因。

### OQ3：CURRENT-SPRINT.md 未记录 KB-API-B1/B2/B3 批次状态
进度文档 630 行无 KB-API 补全计划任何描述（批次计划仅在 plan/spec 文件中）。按 CLAUDE.md §6-3 Feature batch 四文件更新要求，批次合入时应同步补 README.md + CURRENT-SPRINT.md + ANI-06 状态行。

### OQ4：`name` 字段无 maxLength（KB 域既有惯例，未单方面收紧）
`UpdateKnowledgeBaseRequest.name` 与 `createKnowledgeBase` 内联 schema（v1.yaml L2162）均无 maxLength，SPEC 冻结 schema 未要求。若未来统一补长度约束，应 create/update 整域同步做，避免不对称契约。

## 验证命令（已运行）

```
python scripts/validate_services_contract.py     # 109 accepted baseline warning(s)
python scripts/validate_yaml.py api/openapi/services/v1.yaml
python scripts/validate_services_boundary.py     # venv 移出后通过（本地环境噪音）
python scripts/validate_services_route_contract.py  # 预期红：2 spec_not_in_code（issue-044 解除）
go build ./pkg/generated/... ./services/{ani-gateway,tenant-service,model-service,inference-service}/...
go test ./services/model-service/... ./services/inference-service/...
python -m pytest（kb-service）                    # 223 passed + 1 main 既有失败
python scripts/gen_sdk_alpha.py && python scripts/generate_api_docs.py && npx openapi-typescript  # 生成后 git diff 零漂移
git diff --check                                  # exit 0
```

> 注：Windows 下 `make validate-spec-split` 内 `$(GO_CACHE_ENV) go test` 内联环境变量语法不识别，用 PowerShell `$env:GOCACHE=...` 预设后运行等价命令（PASS 等价成立）；`validate_services_boundary.py` 扫描 ai/ 全量 .py 含 gitignored 的 rag-engine venv（joblib big5 测试文件编码干扰），本地验证时临时移出 venv 后通过并已恢复——CI 干净 checkout 无此问题。
