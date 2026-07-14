# SERVICES-CONTROLLED-UNFREEZE-A - Services 受控解冻入口治理

## 背景

本批次把当前入口文档从旧 Services 冻结规则更新为“Services 受控并行 PR 阶段”。Core Sprint 13/14 既有事实继续有效：Sprint 13 S01-S07 production-shaped live gate evidence 仍只证明组件级 acceptance passed；Sprint 14 resilience live gate 的 production-ready 结论只限隔离 fixture，不外推到现有 Sprint13 单副本后端或 full platform。

历史批次中关于 Services 冻结的原因和结论保留为历史语境：当时用于防止 Core 基于不完整定义猜测实现 Services 业务。它们不是当前 PR 规则。

## 已验证代码事实

- `repo/api/openapi/v1.yaml` 仍是 Core OpenAPI REST API 与 Core/Services 跨层控制面契约来源。
- `repo/api/openapi/services/v1.yaml` 仍是 Services API 来源，Services 路径使用 `/api/v1/svc`。
- `.github/CODEOWNERS` 已在本分支纳入 Core/Services owner 分层：Core 保护目录由 `@e92nf872rp` 主责；Services API、Services SDK、Services handler、model-service、kb-service、Services docs/tasks/prototypes、AI、frontends、inference operator 由 `@viccao-yue` 与 `@e92nf872rp` 共同可见 review。
- `repo/Makefile` 已在本分支提供 `make validate-services`，聚合 Services boundary、API split、OpenAPI/Gateway route contract、Services semantic contract、SDK/API docs 生成漂移、model-service Go、RAG Python compile/test、Console schema drift 和既有 architecture gate。
- `.github/workflows/ci.yml` 已在本 final-review fix wave 移到 GitHub 可发现的 checkout root；CI 中的 repo 命令以 `repo/` 为 working directory，Go/Python/Node cache 与 OpenAPI file path 均按 checkout-root 语义指向 `repo/...`。
- `repo/frontends/console/` 的 npm lockfile、lint/type-check/build 与 OpenAPI schema 生成一致性检查已纳入本分支的 Console gate；生成物仍必须由 OpenAPI source 驱动，不手工改 generated schema。
- Task 1 已新增并在本 final-review fix wave 加固 `repo/scripts/validate_services_boundary.py` 和 `repo/architecture/services-boundary-baseline.yaml`，用于阻断未知 Services root、docs-only root 源码、Services-owned Go 越界 import、Gateway 直连 Services 实现和未登记 AI provider SDK 直连。

## 目录 ownership

| 范围 | 当前归属 |
|---|---|
| `repo/services/model-service/`、`repo/services/kb-service/`、`repo/ai/`、`repo/frontends/` | Services 主责，Core 共同关注跨层边界 |
| `repo/api/openapi/services/v1.yaml`、`repo/sdks/services/`、`repo/docs/api/services.html` | Services API/生成物，Core/Services 共同 review |
| `repo/services/ani-gateway/internal/router/*_resources*` 中 `/api/v1/svc/*` handler | Services 主责，Core 共同 review |
| `repo/services/ani-gateway/` 其他 Core handler、middleware、runtime、bootstrap | Core 主责 |
| `repo/pkg/`、`repo/api/openapi/v1.yaml`、`repo/deploy/`、`repo/scripts/`、`repo/sdks/core/`、`repo/cli/`、`repo/installer/` | Core 保护目录 |

## 门禁

Services 受控解冻后的 PR 顺序：

1. API-first：先改 `repo/api/openapi/services/v1.yaml`；如触碰 Core 能力，先经 Core API 评审。
2. 实现：再改 Services handler、业务服务、前端和生成物。
3. 生成物：Services SDK、API docs 和前端 schema 必须由 OpenAPI 生成，不手工编辑。
4. 边界与语义：运行 `make validate-services`，它聚合 API split、Services boundary gate、OpenAPI/Gateway route contract、Services OpenAPI YAML、Services 语义合同、源驱动 SDK/API docs、SDK、model-service、RAG、Console schema drift 和现有 architecture gate。
5. 共同审查：触碰 Core API/OpenAPI、Core 保护目录、Gateway shared/mixed handler、Services API 或生成物时按 CODEOWNERS review，并在 PR 描述中列出触碰原因。

Services PR 最短必跑命令：

```bash
cd /Users/zhangfan/ANI/repo
make validate-services
make validate-doc-entrypoints
git diff --check
```

Services OpenAPI 语义门禁使用 `repo/architecture/services-contract-baseline.yaml` 登记精确的当前缺口；OpenAPI 与 Gateway 路由表面使用 `repo/architecture/services-route-baseline.yaml` 登记精确差异：

- `uploadKnowledgeBaseDocument` 当前 multipart request 未要求 `idempotency_key`；
- 当前 Services spec 的操作没有 top-level/operation-level `security`，但保留了 `BearerAuth` 与 `ApiKeyAuth` scheme；
- 当前若干 `202` 操作仍返回资源或没有 response schema，而不是 `AsyncTask`。
- 当前 Gateway 有 2 个 Services 路由未在 OpenAPI 声明，OpenAPI 有 11 个操作尚未在 Gateway 注册；差异按 method/path 精确登记，新增差异和失效基线阻断 PR。

这些条目只作为逐操作、warning-only 的 accepted baseline；新增缺口和失效基线均阻断 PR。`make validate-services` 还会重新运行 `gen_sdk_alpha.py` 与 `generate_api_docs.py`，并拒绝 `sdks/core/`、`sdks/services/`、`docs/api/` 生成物漂移。以上门禁通过不代表 Services 已 production-ready。

## 已知基线例外

以下三项是 Task 1 固化的精确 accepted baseline violation。它们只代表当前代码事实和迁移前告警，不代表边界合规，也不代表 Services production-ready：

| 路径 | 规则 | 精确 import | 当前结论 |
|---|---|---|---|
| `services/model-service/main.go` | `core_internal_go_import` | `github.com/kubercloud/ani/pkg/bootstrap` | model-service 入口仍直接调用 Core bootstrap 的连接与 gRPC 启动装配；受控解冻时应迁移为 Services 自有启动与依赖装配 |
| `services/model-service/internal/config/config.go` | `core_internal_go_import` | `github.com/kubercloud/ani/pkg/bootstrap` | model-service 配置仍直接返回 Core `bootstrap.Config`；受控解冻时应迁移为 Services 自有配置类型 |
| `ai/rag-engine/app/core/milvus.py` | `provider_sdk_python_import` | `pymilvus` | rag-engine 当前仍直接导入 Milvus provider SDK；后续继续演进需先明确保留理由或迁移到受控边界 |

## Task 5 复核（2026-07-14）

- 已按当前源码逐项复核三条 baseline finding，路径、rule、精确 import 与原因描述仍与源码一致；未发现需要改写 `repo/architecture/services-boundary-baseline.yaml` 的事实性偏差。
- `repo/scripts/validate_services_boundary.py` 当前仍坚持精确文件级例外，不接受目录级/通配符放行；`validate_services_boundary_test.py` 继续覆盖未登记 Core import、未登记 provider import、跨 service internal import、空 metadata 与 wildcard path 拒绝。
- validator 的当前作用域仍是受控最小范围，但已 fail-closed 覆盖 `repo/services/` immediate children：Core-protected service roots（`ani-gateway`、`auth-service`、`task-service`、`metering-service`、`reconcile-worker`）只分类不扫描业务实现；Services-owned source roots（`model-service`、`kb-service`）扫描 Go 越界 import；docs-only roots（`docs`、`tasks`、`prototypes`）禁止出现源文件；`repo/ai/**/*.py` 全量扫描未登记 provider SDK import；`services/ani-gateway/` 继续禁止直接 import Services 业务实现。
- Services 仍处于非 production 状态：`/api/v1/svc/*` 资源 handler 目前是过渡 stub，租户、RBAC、幂等和审计主要依赖 `ani-gateway` 全局 middleware 链，而不是各资源自身实现；因此 baseline 告警只能说明“现存例外被精确登记”，不能外推为边界已收敛或服务已就绪。
- Services OpenAPI 写接口的契约复核结果：绝大多数 POST/PUT/PATCH 已声明 `idempotency_key`，但 `POST /knowledge-bases/{kb_id}/documents` 当前 `multipart/form-data` 只要求 `file`，未声明幂等键；另外该规范虽声明了 `BearerAuth`/`ApiKeyAuth` scheme 与多处 `401`/`403` 响应，但当前文件未声明 top-level 或 operation-level `security` 块，应继续按“实现层已有 middleware，契约层仍待补齐”的非 production 状态看待。
- 源码复核发现原先 `services/ani-gateway/internal/router/router.go` 将 `/api/v1/svc/*` 描述为“stubs return 501”，但各资源 stub 实际多返回 `200/204` 占位响应，和部分 OpenAPI `201/202` 异步语义未对齐；本批次已将注释改为“transitional placeholders”，没有改 bootstrap、provider wiring 或 API 行为。
- `repo/scripts/validate_services_route_contract.py` 只校验 method/path 表面，不把当前过渡 handler 的占位响应误报为已实现；正式开放某个操作前，仍须同步 OpenAPI、handler 响应语义、生成物和业务测试。

## 后续聚合门禁与 final-review fix wave A（2026-07-14）

- 历史验证保留：Task 5 中直接执行 `go test ./services/model-service/...` 时，依赖下载 `golang.org/x/net`、`golang.org/x/sys`、`golang.org/x/sync` 从 `proxy.golang.org` 超时；已做一次 require_escalated 重跑且结果相同。该 blocker 作为当时网络依赖下载环境问题保留，不改写为通过。
- 后续聚合通过项已追加到本分支记录：Console `npm ci`、`npm run lint`、`npm run type-check`、`npm run build`，`make validate-doc-entrypoints`，`make validate-services`，`make lint-ts` 和 `git diff --check` 均已在后续 Task 6/fix 报告中记录为通过；`make validate-services` 同时输出三条 boundary、70 条 semantic contract 和 13 条 route surface accepted baseline warning，均属于本记录列明的精确存量例外。
- final-review fix wave A 修正了 GitHub workflow discoverability：`repo/.github/workflows/ci.yml` 已移到 `.github/workflows/ci.yml`；各 job 的 `run` 步骤从 `repo/` 执行，Go/Python/Node cache dependency path 与 OpenAPI action file path 均指向 checkout root 下的 `repo/...`。
- final-review fix wave A 加固了 Services boundary validator：立即分类 `repo/services/*`，未知 service root fail closed；docs-only roots 出现源文件 fail closed；Go import 扫描保持在 Services-owned source roots；AI provider SDK 扫描扩大到 `repo/ai/**/*.py`；现有 `pymilvus` 只保留 `ai/rag-engine/app/core/milvus.py` 精确 baseline。
- final-review fix wave A 只修正当前入口中的歧义措辞：Core-only 表述限定为 Core 保护范围，Services 业务可继续在主责目录走受控 PR；历史冻结记录和旧 token 未批量改写。

## final-review fix wave B（2026-07-14）

- 新增 `repo/scripts/validate_services_contract.py` 及 fixture tests，对 Services 每个 operation 的写入幂等、安全声明和 `202`/`AsyncTask` 语义 fail closed；现状只有精确 baseline 才能 warning-only，新增或 stale 条目阻断。
- 新增 `repo/architecture/services-contract-baseline.yaml`，逐 operation 记录当前真实缺口；未借此声称实现层或契约层已经 production-ready。
- `make validate-services` 现在重新生成 SDK/API docs 并检查 Core/Services SDK 与 API docs 无生成漂移，避免只验证 Console schema 而漏掉 Services SDK/docs。

## 非目标

- 不实现模型、推理、知识库、RAG、Console 或 BOSS 业务功能。
- 不修改 Core API 业务语义，不新增 Services API path。
- CODEOWNERS、CI、Makefile 和 Console gate 已纳入本分支；本 final-review fix wave 只修正 CI 发现路径/checkout-root 语义、Services boundary fail-closed 范围和当前入口措辞。
- 不把 local/mock/contract 验证升级为 real-provider、runtime-ready 或 production-ready。
- 不修复上述三项 baseline violation；本批次只把它们诚实记录到当前治理入口。
- 不修复本 wave 登记的 Services API 语义缺口；本 wave 只把它们精确纳入 warning-only baseline，并阻断未来新增缺口。

## 验证命令

Task 5 复核与 final-review fix wave A 实际执行命令：

```bash
cd /Users/zhangfan/ANI/repo
python scripts/validate_services_boundary.py --root .
python scripts/validate_component_imports.py --root .
python scripts/validate_spec_split_contract.py
python scripts/validate_sdk_beta.py
go test ./services/model-service/...
python -m compileall -q ai/rag-engine
python scripts/validate_services_boundary_test.py
make validate-doc-entrypoints
cd /Users/zhangfan/ANI
python repo/scripts/validate_yaml.py .github/workflows/ci.yml
git diff --check
```

审计友好的结果摘要：

- 已通过：
  - `python scripts/validate_services_boundary.py --root .`
  - `python scripts/validate_component_imports.py --root .`
  - `python scripts/validate_spec_split_contract.py`
  - `python scripts/validate_sdk_beta.py`
  - `python -m compileall -q ai/rag-engine`
  - `python scripts/validate_services_boundary_test.py`
  - `make validate-doc-entrypoints`
  - `python repo/scripts/validate_yaml.py .github/workflows/ci.yml`
  - `git diff --check`
- 阻塞项：
  - `go test ./services/model-service/...`
  - 阻塞原因：从 `proxy.golang.org` 下载 `golang.org/x/net`、`golang.org/x/sys`、`golang.org/x/sync` 依赖时超时
  - 已做一次 require_escalated 重跑，结果相同；因此当前 blocker 属于外部依赖下载环境问题，而不是本批次已验证到的文档/边界事实回归
