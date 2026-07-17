# ANI Services 受控解冻设计

> 日期：2026-07-13
> 分支：`codex/services-unfreeze-gates`
> 状态：已获用户确认，待实施

## 目标

解除 ANI Services 团队在本 monorepo 中的目录冻结，使 Services 团队可以持续开发、提交和合并 PR；同时保留 ANI Core/Services 的架构边界、API 契约、权限、安全、生成物和质量门禁。

本设计将“禁止 Services 修改”改为“按目录归属和 PR review 管理”，不把 monorepo 变成无边界共享代码仓库。

## 背景与现状

ANI Core 和 ANI Services 当前由两个团队并行开发。早期冻结 Services 目录是为了防止 Core 开发误改 Services 范围，当前 Services 团队已经进入持续代码开发和 PR 阶段，全面冻结不再适用。

当前仓库已经存在部分新治理基础：`.github/CODEOWNERS` 已为 Services API、Services handler、model-service、kb-service、AI、前端和 inference operator 配置 Services owner；`ANI-SERVICES-TEAM-GUIDE.md` 也已经描述了 Services 的可开发目录。但入口规则仍把整个 Services 层表述为冻结，`repo/scripts/validate_doc_entrypoints.py` 还把“只读冻结，不再开发”作为强制文本，因此文档与实际协作机制不一致。

当前代码还存在需要诚实处理的边界事实：`model-service` 直接依赖 Core `pkg/bootstrap`，并在 bounded persistence 位置使用 pgx；`repo/ai/rag-engine` 直接使用 pymilvus。解冻设计不掩盖这些现状，后续通过明确的边界规则、例外记录或迁移批次处理。

## 目录归属模型

### Services 主责目录

Services 团队可以直接开发并提交 PR：

- `repo/services/model-service/`
- `repo/services/kb-service/`
- `repo/ai/`
- `repo/frontends/`
- `repo/services/docs/`
- `repo/services/tasks/`
- `repo/services/prototypes/`
- `repo/api/openapi/services/v1.yaml`

`repo/sdks/services/` 只能提交由 `services/v1.yaml` 生成的结果，不手工编辑生成文件。

### 混合归属目录

`repo/services/ani-gateway/` 按功能路径分治：

- `/api/v1/svc/*` 对应的 Services handler：Services 主责，Core 共同 review。
- Core Gateway、认证、中间件、provider/runtime、Core `/api/v1/*` handler：Core 主责。

`repo/api/proto/model/`、`repo/api/proto/inference/`、`repo/api/proto/kb/` 和 Services API 文档生成物由 Services 主责、Core 共同 review；任何跨层协议变化仍须确认 REST/OpenAPI 边界。

### Core 保护目录

以下目录不因 Services 解冻而开放给 Services 自行合并：

- `repo/pkg/`
- `repo/api/openapi/v1.yaml`
- `repo/api/core-alpha-freeze.yaml`
- `repo/api/core-beta-readiness.yaml`
- `repo/api/core-v1-compatibility-baseline.yaml`
- `repo/deploy/`
- `repo/scripts/`
- `repo/sdks/core/`
- `repo/cli/`
- `repo/installer/`
- Core-owned 的 `auth-service`、`task-service`、`reconcile-worker`、`metering-service` 代码
- `.github/CODEOWNERS`、`CLAUDE.md`、`ANI-06-开发计划.md` 等全局治理文件

Services 可以通过 PR 提出 Core 变更需求，但不能绕过 Core owner 评审。

## 不可解除的工程门禁

### 跨层契约

- Services 业务资源只能维护在 `repo/api/openapi/services/v1.yaml`，路径前缀为 `/api/v1/svc`。
- Core 资源只能维护在 `repo/api/openapi/v1.yaml`，路径前缀为 `/api/v1`。
- Services 只能通过 Core OpenAPI REST API 或 Core SDK 调用 Core。
- Services 不得把模型、推理、知识库、PaaS 业务资源回流到 Core API。
- API 变更必须先更新契约，再更新实现、测试、生成 SDK 和前端类型。

### 代码依赖

- Services 不得 import Core 的 `pkg/ports`、`pkg/adapters`、`pkg/bootstrap` 或 Core 内部业务包。
- Services 业务服务不得直接拼装 Kubernetes provider 对象或直接调用 Kubernetes、KubeVirt、Kube-OVN、MinIO、Milvus 等 Core 底座 SDK。
- Services 自有数据库、消息队列或 AI 组件依赖必须位于清晰的 Services-owned adapter/repository 边界；已有 bounded direct 例外必须有路径、理由和迁移/保留结论。
- 现有 `model-service` 的 `pkg/bootstrap` 依赖和 `repo/ai/rag-engine` 的 pymilvus 依赖必须被列入后续边界修正，不得用“解冻”掩盖为合规。

### 产品与安全语义

- 所有有副作用的 POST、PUT、PATCH 遵守 `idempotency_key` 规则。
- 租户身份从认证上下文取得，不信任请求体的 `tenant_id`。
- 保持 RBAC scope、审计、统一错误结构、异步任务和状态机语义。
- `ANI_AUTH_MODE=dev` 只用于本地联调，不能进入提交或生产配置。
- local/mock/contract 验证不能标记 real-provider、runtime-ready 或 production-ready。

## PR 与自动化门禁

### 路径审核

CODEOWNERS 必须准确表达主责和共同审核关系。Services PR 触碰 Core 保护目录时自动请求 Core review；Core PR 触碰 Services 主责目录时自动请求 Services review。

### Services PR 最小验证

Services 代码或契约 PR 至少运行：

- Services OpenAPI 结构、路径前缀、operationId、幂等性和 Core/Services 分层校验
- 受影响 Go/Python/TypeScript 模块的格式化、构建和单元测试
- Services 生成 SDK/前端类型与 OpenAPI 契约一致性校验
- Services OpenAPI 与 `/api/v1/svc` Gateway method/path 注册一致性校验；现存差异必须逐条进入 accepted baseline，新增或失效差异阻断
- Core/Services import boundary 校验
- `make test`、`make validate-architecture`、`make validate-doc-entrypoints` 和 `git diff --check`

Services 自有 mock 由 Services 团队维护；Core mock 只覆盖 Core API，不得被当作 Services 业务 mock。

### 生产化门禁

Services 业务通过 local profile 或 mock 只能证明契约和应用逻辑。凡依赖 Core 真实 provider 的路径，必须复用对应 Core live gate/evidence；Services 应补充业务层端到端验证，但不能替代 Core 的真实底座门禁。

## 文档与脚本调整范围

第一阶段修改以下入口和治理文件，使其从“Services 全面冻结”切换为“Services 目录解冻、边界和审核保留”：

- `CLAUDE.md`
- `ANI-DOCS-INDEX.md`
- `ANI-06-开发计划.md`
- `repo/CURRENT-SPRINT.md`
- `ANI-05-系统架构设计.md`
- `ANI-11-代码实现规范.md`
- `ANI-02-产品功能设计.md` 中过期的当前态表述
- `ANI-SERVICES-TEAM-GUIDE.md`
- `.github/CODEOWNERS`
- `repo/scripts/validate_doc_entrypoints.py`
- `repo/scripts/validate_doc_entrypoints_test.py`

历史 development record 中描述当时冻结状态的内容不做批量改写；Services 领域文档中“API 尚未冻结”的产品语义也不因目录解冻而自动改成正式契约。

第二阶段增加 Services 专用边界/契约校验入口，先以当前真实代码为基线明确已有例外，再逐项修正，不通过大量静默 allowlist 隐藏问题。

## 实施顺序

1. 更新入口规则和目录归属矩阵。
2. 更新 CODEOWNERS，使 Services docs/tasks/prototypes 等目录归属明确。
3. 将文档入口 validator 从冻结断言改为受控解冻断言，并补回归测试。
4. 增加 Services PR 验证入口，覆盖契约、OpenAPI/Gateway route surface、生成物、依赖边界和模块测试。
5. 对现有 Services 代码做 baseline 检查，单独记录并修复 `pkg/bootstrap`、pymilvus 等边界问题。
6. 运行全量 Core 回归和 Services 相关验证，确认解冻没有削弱 Core 门禁。

## 验收标准

- 入口文档不再把 Services 目录描述为当前冻结。
- Services 主责目录可通过 CODEOWNERS 获得正确 owner review。
- Core/Services OpenAPI 分层和路径前缀校验仍通过。
- Services import boundary、幂等、租户、RBAC、生成物和测试门禁可复跑。
- 历史冻结记录保持历史语义，不被伪造为当前状态。
- `make test`、`make validate-architecture`、`make validate-doc-entrypoints` 和 `git diff --check` 通过。
- 现有边界例外有明确记录，不因“解冻”被默认为合规。

## 非目标

- 本批次不实现模型服务、知识库服务、推理服务或前端业务功能。
- 本批次不修改 Core API 业务语义，不新增 Services 业务 API。
- 本批次不移除 Core owner review，不允许直接 push main。
- 本批次不把历史文档逐篇重写，也不把 local/mock 验证升级为 production-ready。
