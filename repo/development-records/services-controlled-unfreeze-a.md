# SERVICES-CONTROLLED-UNFREEZE-A - Services 受控解冻入口治理

## 背景

本批次把当前入口文档从旧 Services 冻结规则更新为“Services 受控并行 PR 阶段”。Core Sprint 13/14 既有事实继续有效：Sprint 13 S01-S07 production-shaped live gate evidence 仍只证明组件级 acceptance passed；Sprint 14 resilience live gate 的 production-ready 结论只限隔离 fixture，不外推到现有 Sprint13 单副本后端或 full platform。

历史批次中关于 Services 冻结的原因和结论保留为历史语境：当时用于防止 Core 基于不完整定义猜测实现 Services 业务。它们不是当前 PR 规则。

## 已验证代码事实

- `repo/api/openapi/v1.yaml` 仍是 Core OpenAPI REST API 与 Core/Services 跨层控制面契约来源。
- `repo/api/openapi/services/v1.yaml` 仍是 Services API 来源，Services 路径使用 `/api/v1/svc`。
- `.github/CODEOWNERS` 已存在 Core/Services owner 分层：Core 保护目录由 `@e92nf872rp` 主责；Services API、Services SDK、Services handler、model-service、kb-service、AI、frontends、inference operator 由 `@viccao-yue` 与 `@e92nf872rp` 共同可见 review。
- Task 1 已新增 `repo/scripts/validate_services_boundary.py` 和 `repo/architecture/services-boundary-baseline.yaml`，用于阻断新增 Core internal import、跨 Services internal import 和未登记 provider SDK 直连。

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
4. 边界：运行 `make validate-services`，它聚合 API split、Services boundary gate、Services OpenAPI YAML、SDK、model-service、RAG、Console schema drift 和现有 architecture gate。
5. 共同审查：触碰 Core API/OpenAPI、Core 保护目录、Gateway shared/mixed handler、Services API 或生成物时按 CODEOWNERS review，并在 PR 描述中列出触碰原因。

Services PR 最短必跑命令：

```bash
cd /Users/zhangfan/ANI/repo
make validate-services
make validate-doc-entrypoints
git diff --check
```

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
- validator 的当前作用域仍是受控最小范围：`services/model-service/`、`services/kb-service/`、`ai/rag-engine/app/`，外加 `services/ani-gateway/` 内禁止直接 import Services 业务实现；它不是对 `repo/services/*` 的全量扫描结论。
- Services 仍处于非 production 状态：`/api/v1/svc/*` 资源 handler 目前是过渡 stub，租户、RBAC、幂等和审计主要依赖 `ani-gateway` 全局 middleware 链，而不是各资源自身实现；因此 baseline 告警只能说明“现存例外被精确登记”，不能外推为边界已收敛或服务已就绪。
- Services OpenAPI 写接口的契约复核结果：绝大多数 POST/PUT/PATCH 已声明 `idempotency_key`，但 `POST /knowledge-bases/{kb_id}/documents` 当前 `multipart/form-data` 只要求 `file`，未声明幂等键；另外该规范虽声明了 `BearerAuth`/`ApiKeyAuth` scheme 与多处 `401`/`403` 响应，但当前文件未声明 top-level 或 operation-level `security` 块，应继续按“实现层已有 middleware，契约层仍待补齐”的非 production 状态看待。
- `services/ani-gateway/internal/router/router.go` 仍将 `/api/v1/svc/*` 描述为“stubs return 501”，但当前各资源 stub 实际多返回 `200/204` 占位响应，和部分 OpenAPI `201/202` 异步语义仍未对齐；本批次只记录事实，不改 bootstrap、provider wiring 或 API 行为。

## 非目标

- 不实现模型、推理、知识库、RAG、Console 或 BOSS 业务功能。
- 不修改 Core API 业务语义，不新增 Services API path。
- 不调整 CODEOWNERS、CI 或 Makefile；这些属于后续任务。
- 不把 local/mock/contract 验证升级为 real-provider、runtime-ready 或 production-ready。
- 不修复上述三项 baseline violation；本批次只把它们诚实记录到当前治理入口。

## 验证命令

Task 5 复核实际执行命令：

```bash
cd /Users/zhangfan/ANI/repo
python scripts/validate_services_boundary.py --root .
python scripts/validate_component_imports.py --root .
python scripts/validate_spec_split_contract.py
python scripts/validate_sdk_beta.py
go test ./services/model-service/...
python -m compileall -q ai/rag-engine
python scripts/validate_services_boundary_test.py
```
