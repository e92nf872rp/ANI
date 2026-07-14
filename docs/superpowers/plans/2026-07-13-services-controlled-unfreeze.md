# ANI Services 受控解冻实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不放松 ANI Core 保护范围、API 分层、架构导入和现有 Core 回归门禁的前提下，解除对 ANI Services 的全量只读冻结，使 Services 团队可以在明确归属目录中持续提交 PR，并让新增的跨层越界依赖在 PR 阶段被阻断。

**Architecture:** 采用“目录归属解冻 + CODEOWNERS 共同审查 + Services 专用边界门禁 + 存量例外基线”的控制模型。Services 业务目录和 `/api/v1/svc/*` handler 由 Services 主审，Core 保护目录继续由 Core 主审；混合的 Gateway handler 和 Services OpenAPI 由双方共同审查。门禁对现有代码做事实扫描：已登记的存量例外只产生可追踪告警，新增未登记越界依赖失败。

**Tech Stack:** Markdown/YAML governance documents, GitHub CODEOWNERS, Python 3 validator scripts and `unittest`, Go `go test`, existing OpenAPI/SDK validators, npm-based Console checks, GNU Make, GitHub Actions.

## Global Constraints

- 工作基线为 `codex/services-unfreeze-gates`，其父提交为已同步的 `origin/main`；不得改写历史，不得把 `.worktrees/` 或 `Picture/` 现有未跟踪文件加入提交。
- 只解除 Services 的“全量冻结”表达，不解除 Core 目录保护，不删除 Core owner review，不改变 `repo/api/openapi/v1.yaml`、Core SDK、Core CLI、Core deploy/scripts、Core readiness/freeze gate 的约束。
- Services 跨层仍只能走 Core OpenAPI REST API / Core SDK；禁止导入 `pkg/ports`、`pkg/adapters`、`pkg/bootstrap` 或 Core 内部 service 包。`repo/services/ani-gateway` 按 `/api/v1` Core 和 `/api/v1/svc` Services 的实际路由分区处理。
- 不将 Services 代码的当前状态描述为 production-ready；当前 `model-service` 的 Core `pkg/bootstrap` 依赖、RAG 的 `pymilvus` 直接依赖和已登记的 model-service pgx bounded-direct 依赖必须在记录中如实列出，并由基线文件约束后续不得扩大。
- 不新增 Models、Inference、Knowledge Bases、RAG 或前端产品功能；本批次只修改治理、归属、边界检查和必要的 CI 接入。
- 历史开发记录保留历史语境；只修订当前入口文档和当前协作规则，不能用全局替换抹除可追溯历史。

---

## Task 1: 固化代码事实与 Services 存量边界基线

**Files:**

- Create `repo/architecture/services-boundary-baseline.yaml`
- Create `repo/scripts/validate_services_boundary.py`
- Create `repo/scripts/validate_services_boundary_test.py`

- [ ] 先以当前代码事实建立基线条目：`repo/services/model-service/main.go` 和 `internal/config/config.go` 中的 `github.com/kubercloud/ani/pkg/bootstrap`，以及 `repo/ai/rag-engine/app/core/milvus.py` 中的 `pymilvus`。每一项必须包含 `path`、`rule`、具体 import、`status`、责任 owner、事实理由和迁移或保留结论；不得用目录级通配符掩盖整个 Services 树。
- [ ] 实现 validator 的扫描规则：扫描 Services Go/Python 业务路径，阻断新增 Core 内部包导入（`pkg/ports`、`pkg/adapters`、`pkg/bootstrap` 及 Core service `internal` 包）；扫描 AI 业务代码中的直接 provider SDK；将当前基线文件中的精确条目标为 accepted baseline 并输出警告；任何不在基线中的匹配返回非零退出码。
- [ ] 对 `repo/services/ani-gateway` 只校验跨层导入和路由分区，不把 Gateway 全目录误判为 Services 独占；继续调用既有 `validate_spec_split_contract.py` 检查 Core/Services OpenAPI 与 `/api/v1`、`/api/v1/svc` 分割。
- [ ] 为 validator 写最小单元测试：当前三类基线可被识别并只告警；临时 fixture 中新增 `pkg/bootstrap` 导入失败；新增未登记 provider SDK 失败；精确登记的例外通过；目录级或无理由的例外不通过。
- [ ] 保持 `repo/architecture/component-import-allowlist.yaml` 现有 model-service pgx 条目不变；validator 不扩大该 allowlist，确保“已有 bounded direct”与“Services 依赖 Core 内部包”是两类不同规则。

**Verification:**

```bash
cd /Users/zhangfan/ANI/repo
python scripts/validate_services_boundary_test.py
python scripts/validate_services_boundary.py --root .
```

## Task 2: 更新当前治理入口，解除全量冻结并写明门禁

**Files:**

- Modify `/Users/zhangfan/ANI/CLAUDE.md`
- Modify `/Users/zhangfan/ANI/ANI-DOCS-INDEX.md`
- Modify `/Users/zhangfan/ANI/ANI-06-开发计划.md`
- Modify `/Users/zhangfan/ANI/repo/CURRENT-SPRINT.md`
- Modify `/Users/zhangfan/ANI/ANI-05-系统架构设计.md`
- Modify `/Users/zhangfan/ANI/ANI-11-代码实现规范.md`
- Modify `/Users/zhangfan/ANI/ANI-02-产品功能设计.md`
- Modify `/Users/zhangfan/ANI/ANI-SERVICES-TEAM-GUIDE.md`
- Create `/Users/zhangfan/ANI/repo/development-records/services-controlled-unfreeze-a.md`
- Modify `/Users/zhangfan/ANI/repo/development-records/README.md`
- Modify `/Users/zhangfan/ANI/repo/scripts/validate_doc_entrypoints.py`
- Modify `/Users/zhangfan/ANI/repo/scripts/validate_doc_entrypoints_test.py`

- [ ] 在 `CLAUDE.md`、`ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md` 和 `repo/CURRENT-SPRINT.md` 的当前状态区域明确写出：Core Sprint 13/14 既有事实继续有效；Services 进入受控并行 PR 阶段；Services 的目录、API、handler、生成物和跨层边界分别受 CODEOWNERS、API split、Services validator、现有 architecture gate 约束。
- [ ] 删除这些入口文档中“Services 当前全量只读冻结/不再开发”的当前时态要求，替换为“受控解冻”；保留历史批次中的原冻结原因和历史结论，并注明它们不是当前 PR 规则。
- [ ] 在 `ANI-05` 增补当前 Services ownership matrix、混合 Gateway handler 归属和解冻后的门禁顺序；在 `ANI-11` 将旧的“删除/覆盖旧 Services 逻辑”改为 API-first、目录归属、Core review、幂等/租户/RBAC/审计/异步语义要求；在 `ANI-SERVICES-TEAM-GUIDE.md` 同步保护目录、可修改目录、禁止 Core internal import、生成文件和 PR 检查命令。
- [ ] 在 `ANI-02` 仅调整仍描述 Services 必须被冻结或由 Core 猜测实现的当前协作表述，不改产品功能定义和历史需求。
- [ ] 新增开发记录 `services-controlled-unfreeze-a.md`，按“背景—已验证代码事实—目录 ownership—门禁—已知基线例外—非目标—验证命令”记录本批次；明确当前并不意味着 Services 实现已经全部符合边界或已具备生产就绪结论。
- [ ] 更新 `validate_doc_entrypoints.py` 的当前入口断言：要求受控解冻、共同审查、Services boundary gate 和当前 Sprint 入口；移除只适用于旧当前状态的“外部移交后只读冻结”硬性断言；保留对 Core/Services API、gRPC 内部通信和历史文档反漂移规则的检查。
- [ ] 扩展 `validate_doc_entrypoints_test.py`，覆盖受控解冻新 marker 和 RKE token 边界；运行全仓 Markdown 扫描，确保没有引入当前时态的旧冻结或错误 Services API 路径。

**Verification:**

```bash
cd /Users/zhangfan/ANI/repo
python scripts/validate_doc_entrypoints.py
python scripts/validate_doc_entrypoints_test.py
```

## Task 3: 将目录解冻落实到 CODEOWNERS，而不是只改文字

**Files:**

- Modify `/Users/zhangfan/ANI/.github/CODEOWNERS`

- [ ] 在现有具体规则之后，为 `repo/services/docs/`、`repo/services/tasks/`、`repo/services/prototypes/` 和 `repo/services/README.md` 增加 `@viccao-yue @e92nf872rp` 共同审查规则；保留 Services OpenAPI、SDK、API docs、model/kb/inference proto 的共同审查。
- [ ] 在 CODEOWNERS 注释中明确 GitHub “最后匹配规则生效”，说明 `repo/services/ani-gateway` 默认仍由 Core owner 保护，精确的 `/api/v1/svc/*` handler 模式由 Services/Core 共同审查；不得添加覆盖整个 `repo/services/` 的 Services-only 规则。
- [ ] 明确 `repo/operators/inference-operator/` 是共享的 bounded operator 范围，不把 Kubernetes operator、Core `pkg/`、deploy、scripts 或 generated Core SDK 错误解冻为 Services 独占。

**Verification:**

```bash
cd /Users/zhangfan/ANI
git diff --check
rg -n "services/docs|services/tasks|services/prototypes|services/README|ani-gateway|model_resources|inference_resources|kb_resources" .github/CODEOWNERS
```

## Task 4: 建立可直接执行的 Services PR 门禁并修正真实 CI 入口

**Files:**

- Modify `/Users/zhangfan/ANI/repo/Makefile`
- Modify `/Users/zhangfan/ANI/repo/.github/workflows/ci.yml`

- [ ] 在 Makefile 增加 `validate-services` phony target，按顺序执行 Services boundary validator、`validate-spec-split`、`validate-sdk-beta`、Services 相关 Go 测试、RAG Python compile/test 和 Console API 生成一致性检查；失败即停止，不绕过 Core architecture gate。
- [ ] `validate-services` 同时执行 OpenAPI 与 `/api/v1/svc` Gateway method/path route contract；当前已知差异只能在 `repo/architecture/services-route-baseline.yaml` 中逐条登记，新增差异和失效基线必须失败。
- [ ] Services target 的 Console 检查使用仓库实际存在的 `repo/frontends/console/package-lock.json` 和 `npm ci`/`npm run` 脚本；不得继续引用当前树中不存在的 `frontends/boss` 或不存在的前端 workspace lockfile。
- [ ] API 生成一致性检查执行 `npm --prefix frontends/console run gen-api`，随后只允许预期的生成文件变化；若生成文件已同步则用 `git diff --exit-code -- frontends/console/src/api/schema.d.ts frontends/console/src/api/core-schema.d.ts` 证明一致。不得手工编辑 generated schema 绕过 OpenAPI source。
- [ ] 在 CI 中增加独立的 Services boundary/doc/API gate，使用当前仓库实际目录；同时修正前端 job 对 `frontends/console` 的安装、type-check、lint、build 命令，移除对不存在 `frontends/boss` 和 `frontends/pnpm-lock.yaml` 的引用。既有 Go/Python/security/dependency jobs 保持不削弱。
- [ ] 在 `ANI-SERVICES-TEAM-GUIDE.md` 和新 development record 写出 Services PR 的最短必跑命令，以及涉及 Core API/OpenAPI、Gateway shared handler、generated SDK 或 protected path 时的共同审查要求。

**Verification:**

```bash
cd /Users/zhangfan/ANI/repo
make validate-services
make validate-architecture
make validate-doc-entrypoints
```

## Task 5: 按现有代码事实验证并收口实现边界

**Files:**

- Modify `repo/architecture/services-boundary-baseline.yaml` only when the exact current import path, rationale or owner is corrected by source inspection.
- Modify `repo/development-records/services-controlled-unfreeze-a.md` to record the verified baseline and its non-production status.
- Modify `repo/scripts/validate_services_boundary.py` or its tests only when source inspection exposes a validator false positive or false negative.

- [ ] 本批次不重构 `model-service` bootstrap 或 RAG provider wiring；这是为解除治理冻结而设的最小范围。保留精确基线例外，输出可见告警，并在记录中明确它们不代表边界合规或生产就绪。
- [ ] 运行 validator、Go import guard、OpenAPI split guard 和现有 tests，核对报告中的路径与当前源码逐项一致；不得凭文档推断“model-service 已脱离 bootstrap”或“RAG 已经通过 adapter”。
- [ ] 将后续迁移责任、owner、触发条件和允许范围写入基线文件；validator 必须继续阻断任何新路径或新 provider import，不能把 provider 直接依赖通过目录通配符永久放行。
- [ ] 检查 Services API 的所有写入接口仍满足幂等键、tenant context、RBAC/audit、异步状态机和错误语义；本批次不新增接口，不改 Core API 语义。

**Verification:**

```bash
cd /Users/zhangfan/ANI/repo
python scripts/validate_services_boundary.py --root .
python scripts/validate_component_imports.py --root .
python scripts/validate_spec_split_contract.py
python scripts/validate_sdk_beta.py
go test ./services/model-service/...
python -m compileall -q ai/rag-engine
```

## Task 6: 全量验收、提交和 PR 前置条件确认

**Files:**

- All files changed by Tasks 1–5; no unrelated untracked files.

- [ ] 运行完整门禁：`make test`、`make validate-architecture`、`make validate-doc-entrypoints`、`make validate-services`、前端 `npm ci && npm run type-check && npm run lint && npm run build`，以及 `git diff --check`。
- [ ] 用 `git diff --name-status origin/main...HEAD` 审查变更只覆盖本计划范围；检查 generated files、CODEOWNERS 最后匹配规则、文档当前状态和历史记录是否保持一致。
- [ ] 在提交前记录每条命令的真实结果；任何因外部依赖、集群、网络或环境缺失未执行的检查必须标为未验证，不得写成通过。
- [ ] 形成单一语义清晰的提交，例如 `feat: enable controlled Services development`；提交不包含 `.worktrees/`、`Picture/` 或其他用户既有未跟踪文件。
- [ ] 只有在本地门禁结果可复现、本批次变更已提交、既有未跟踪文件已核对且未纳入提交、远端分支已推送后，才具备创建 PR 的前置条件；PR 描述需列出 Services 可修改/保护目录、门禁命令、存量例外和未纳入范围。

**Final verification commands:**

```bash
cd /Users/zhangfan/ANI/repo
make test
make validate-architecture
make validate-doc-entrypoints
make validate-services
git diff --check
cd /Users/zhangfan/ANI
git status --short
git diff --name-status origin/main...HEAD
```

## Definition of Done

- 当前入口文档不再把 Services 描述为全量冻结，且历史冻结记录仍可追溯。
- Services 可修改目录、Core protected 目录和 Gateway mixed ownership 在 CODEOWNERS 中可执行、无全目录误放行。
- 新增 Services boundary gate 能阻断未登记 Core internal/provider 越界依赖，并对现有三类事实例外给出精确告警。
- Services API split、Core architecture/import guard、SDK/API generation、幂等与安全约束均继续执行。
- 真实 CI 不再引用不存在的前端目录/lockfile；Services PR 有可复跑的最短门禁。
- 完整验证结果、已知限制和变更范围均写入开发记录；满足这些条件后才创建 PR。
