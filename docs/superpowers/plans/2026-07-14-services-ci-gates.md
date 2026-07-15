# Services CI 门禁与 AI 协作治理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复当前不可执行的 GitHub Actions，并建立能够约束多人和 AI Coding Agent 并行 Services PR 的可审计、失败即阻断的 CI 门禁。

**Architecture:** 保留现有 `make` 校验作为本地真实来源，在 GitHub Actions 中按职责拆分 Go、Python、Console、OpenAPI、Services contract 和依赖安全检查。所有必需检查由一个始终运行的聚合 job 汇总，避免某个 job 被跳过或初始化失败后仍可合并；`main` 分支保护只要求这个稳定的聚合状态和必要的人工审查。Services 的既有差异只能以精确 baseline 告警保留，新差异和过期 baseline 必须失败。

**Tech Stack:** GitHub Actions、Make、Go 1.25、Python 3.12、PyYAML、pytest、OpenAPI validator、npm、CODEOWNERS。

## 当前执行状态（2026-07-14）

- 已完成：本地 workflow 契约测试、仓库内 OpenAPI 校验、Services contract/boundary/route 门禁、Go workspace module 自动发现、Python AI 变更测试策略、Go 1.25 依赖同步、Java SDK Smoke 修复。
- 已验证：Go 全量单元测试和全部服务构建通过；远端 Go 依赖漏洞和根目录 lint 问题已按真实日志修复方向处理。
- 待验证：推送后的远端 CI 全部 job、预编译 golangci-lint、多 module govulncheck、GitHub branch protection 实际阻断效果。
- 不得将上述待验证项标记为完成；Services 当前 3 条边界、70 条语义、13 条路由仍是精确存量 baseline 告警，不代表已清零。

## Global Constraints

- `CLAUDE.md`、`ANI-DOCS-INDEX.md`、`repo/CURRENT-SPRINT.md` 是治理入口；不得把动态 CI 记录堆入 `CLAUDE.md`。
- Services 业务资源必须先进入 `repo/api/openapi/services/v1.yaml`，再实现 handler、SDK、生成物和测试。
- Services PR 必须通过 `make validate-services`；不得用 `continue-on-error`、手工修改生成物或扩大 Services 目录所有权来绕过门禁。
- 既有 3 条 boundary、70 条 semantic、13 条 route 差异只是当前事实基线，不代表合规或 production-ready；新增差异和 stale baseline 必须失败。
- 所有 CI job 必须在 Linux runner 上使用可移植路径；不得依赖本机 `/private/tmp`、开发者全局 Python 包或未锁定的运行时工具版本。
- 本计划先拆为独立 PR：CI 可执行性修复、必需门禁/分支保护、AI Agent 协作规范；每个 PR 都必须有独立验证证据。

## 已验证问题

PR #18 的 GitHub Actions run `29310022606` 失败，失败原因已从 job 日志确认：

1. `OpenAPI Spec Lint` 引用的 `swaggerhub-actions/validate-openapi@v1` 无法解析。
2. `Python AI Services` 中 `langchain-community==0.3.0` 要求 `pydantic-settings>=2.4.0`，而运行时文件固定 `pydantic-settings==2.2.1`。
3. `Services Boundary / API / Docs Gate` 在安装整套 RAG 运行时依赖时提前失败，实际 Services gate 没有执行。
4. `Go Build & Test` 使用 `GOCACHE=/private/tmp/ani-go-build`，Ubuntu runner 无法创建 `/private`，构建在缓存初始化阶段失败。
5. `Dependency CVE Scan` 在仓库根目录运行 `govulncheck ./...`，根目录没有 `go.mod`，安全扫描没有形成结论。
6. `Frontend Build` 通过，但不能代替上述失败检查。

---

### Task 1: 建立 CI 配置契约测试，先固定失败行为

**Files:**
- Create: `repo/scripts/validate_ci_workflow.py`
- Create: `repo/scripts/validate_ci_workflow_test.py`
- Test: `.github/workflows/ci.yml`

**Interfaces:**
- `validate_ci_workflow.py` 接收 `--root PATH`，读取 `.github/workflows/ci.yml`，返回 `0` 表示工作流满足契约，返回 `1` 并打印具体违规项表示失败。
- 测试必须覆盖：必需 job 名称存在、聚合 job 使用 `always`、Services gate 不安装 `ai/rag-engine/requirements.txt`、workflow 不包含不可解析的 SwaggerHub Action、Makefile 不包含 `/private/tmp`。

- [ ] **Step 1: 写失败测试**

测试构造一个临时 workflow/Makefile fixture，分别验证以下情况失败：缺少 `required-gates`、聚合 job 没有 `always`、Services gate 安装生产依赖、出现 `/private/tmp`。当前仓库版本至少应因缺少聚合 job 而失败。

- [ ] **Step 2: 运行测试确认失败**

运行：

```bash
cd repo
python scripts/validate_ci_workflow_test.py
```

预期：FAIL，原因包含当前 workflow 尚未定义稳定的 required gate 聚合 job。

- [ ] **Step 3: 实现最小校验器**

使用 PyYAML 读取 YAML，不执行 workflow；对 job key、`needs`、`if`、`run` 和 `uses` 做结构校验。不要在校验器中维护依赖版本或复制业务门禁规则。

- [ ] **Step 4: 运行测试确认通过**

运行：

```bash
cd repo
python scripts/validate_ci_workflow_test.py
```

预期：PASS。后续每次修改 `.github/workflows/ci.yml` 都必须运行该测试。

- [ ] **Step 5: 接入 Makefile**

在 `repo/Makefile` 增加 `validate-ci-workflow` target，并把它加入文档帮助文本；运行 `make validate-ci-workflow` 必须执行上述测试。

- [ ] **Step 6: 提交**

```bash
git add repo/scripts/validate_ci_workflow.py repo/scripts/validate_ci_workflow_test.py repo/Makefile
git commit -m "test: add CI workflow contract guard"
```

### Task 2: 修复跨平台 Go 构建和安全扫描

**Files:**
- Modify: `repo/Makefile:148` 附近的 `GO_CACHE_ENV`
- Modify: `.github/workflows/ci.yml` 的 `go-ci`、`dependency-scan`
- Modify: `repo/go.work` 或新增 CI 专用模块遍历脚本，仅在实际需要时修改
- Test: `repo/scripts/validate_ci_workflow_test.py`

**Interfaces:**
- `GO_CACHE_ENV` 使用仓库内被 `.gitignore` 忽略的 `.cache/go-build` 和 `.cache/gomod`，不能写死宿主机路径。
- Go vulnerability scan 必须逐个进入 `repo/go.work` 中列出的 module 目录运行 `govulncheck ./...`；不得在没有 `go.mod` 的仓库根目录运行。

- [ ] **Step 1: 写失败测试**

增加断言：`GO_CACHE_ENV` 不包含 `/private/tmp`；dependency scan 命令包含明确的 module 工作目录；不存在裸的根目录 `govulncheck ./...`。

- [ ] **Step 2: 运行测试确认失败**

```bash
cd repo
python scripts/validate_ci_workflow_test.py
```

预期：FAIL，至少命中 `/private/tmp` 和根目录 `govulncheck`。

- [ ] **Step 3: 实现可移植缓存**

将缓存路径改为：

```make
GO_CACHE_ENV = GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/gomod
```

在 Linux CI 和本地 macOS 上分别执行 `make build-gateway`，确认目录可创建且不会产生 tracked diff。

- [ ] **Step 4: 实现 module 级安全扫描**

在 workflow 中显式扫描 `go.work` 当前 use 列出的 module，例如 `cli/ani`、`pkg`、`services/ani-gateway`、`services/auth-service`、`services/model-service`、`services/reconcile-worker`、`services/task-service`、`tools/kms-sm4-live-fixture`。每个 module 使用子 shell `cd` 后运行 `govulncheck ./...`；新增 module 必须同步扫描清单或由脚本从 `go.work` 解析。

- [ ] **Step 5: 运行本地验证**

```bash
cd repo
make build-gateway
make test-go
```

预期：不再出现 `/private` 权限错误；测试结果只反映真实 Go 编译/测试错误。

- [ ] **Step 6: 提交**

```bash
git add repo/Makefile .github/workflows/ci.yml repo/scripts/validate_ci_workflow_test.py
git commit -m "ci: make Go builds and vulnerability scans portable"
```

### Task 3: 解耦 Services 门禁与 RAG 运行时依赖，修复 Python 依赖闭环

**Files:**
- Create: `repo/ci/requirements-contract.txt`
- Modify: `.github/workflows/ci.yml` 的 `python-ci`、`services-pr-gate`
- Modify: `repo/ai/rag-engine/requirements.txt`，仅在 resolver 和 RAG 测试确认后调整冲突 pin
- Modify: `repo/Makefile` 的 `validate-services`，只在验证发现需要时调整
- Test: 新增 Python dependency smoke test，位置 `repo/scripts/validate_python_dependencies_test.py`

**Interfaces:**
- `repo/ci/requirements-contract.txt` 只包含 contract/docs gate 所需的锁定工具，例如 PyYAML、pytest 和 OpenAPI validator；它不能引入 FastAPI、LangChain、Milvus、Docling 或 PaddleOCR。
- `python-ci` 负责真实 RAG 运行时依赖、ruff、mypy 和 pytest；`services-pr-gate` 负责 contract、生成物和文档漂移，两者失败原因必须可区分。

- [ ] **Step 1: 写失败的依赖检查**

为 `requirements.txt` 增加一个干净虚拟环境安装测试，检查 resolver、`pip check`、RAG 模块导入和现有 pytest。当前版本应明确复现 `pydantic-settings` 与 `langchain-community` 的 ResolutionImpossible，而不是吞掉错误。

- [ ] **Step 2: 运行确认失败**

```bash
cd repo
python scripts/validate_python_dependencies_test.py
```

预期：FAIL，并打印冲突双方和当前 pin，不得改成 warning-only。

- [ ] **Step 3: 修复运行时依赖**

以 resolver 的明确约束为准，在 `pydantic-settings` 与 `langchain-community` 之间选择一组兼容且可复现的 pin；先在干净 Python 3.12 虚拟环境执行安装、`pip check`、RAG import 和 pytest，再提交 requirements 变化。不得只把版本放宽而不保留可复现锁定。

- [ ] **Step 4: 建立独立 contract 依赖集**

将 Services gate 的安装改为：

```bash
pip install -r ci/requirements-contract.txt
```

删除 Services gate 对 `ai/rag-engine/requirements.txt` 的安装依赖；RAG 代码仍由 `python-ci` 单独验证。

- [ ] **Step 5: 运行分层验证**

```bash
cd repo
python -m venv /tmp/ani-ci-contract-venv
/tmp/ani-ci-contract-venv/bin/pip install -r ci/requirements-contract.txt
make validate-services
```

另建干净 RAG venv 执行 dependency smoke test。两套环境均必须显式失败或通过，不能因开发机已有包而假通过。

- [ ] **Step 6: 提交**

```bash
git add repo/ci/requirements-contract.txt repo/ai/rag-engine/requirements.txt repo/scripts/validate_python_dependencies_test.py .github/workflows/ci.yml repo/Makefile
git commit -m "ci: isolate Services gates and repair Python dependency checks"
```

### Task 4: 用仓库内 OpenAPI 校验替换失效 Action，并校验 Core/Services 两份契约

**Files:**
- Modify: `.github/workflows/ci.yml` 的 `api-spec-lint`
- Modify: `repo/ci/requirements-contract.txt`
- Modify: `repo/Makefile` 的 `validate-services` 或新增 `validate-openapi-spec`
- Test: `repo/scripts/validate_ci_workflow_test.py`

**Interfaces:**
- `validate-openapi-spec` 同时校验 `api/openapi/v1.yaml` 和 `api/openapi/services/v1.yaml`，使用仓库内可安装且锁定版本的 validator。
- OpenAPI validation 失败必须让 `api-spec-lint` 和最终 `required-gates` 失败。

- [ ] **Step 1: 先增加本地契约测试**

在 `repo/scripts/validate_openapi_spec_test.py` 中加入合法 fixture、缺少 `openapi` 字段 fixture、非法 path/method fixture；当前仓库两份真实 spec 都必须纳入测试命令。

- [ ] **Step 2: 运行失败测试**

```bash
cd repo
python scripts/validate_openapi_spec_test.py
```

预期：新 validator 命令尚未接入时失败。

- [ ] **Step 3: 实现本地校验命令**

使用 `ci/requirements-contract.txt` 中的固定 validator 版本，分别读取两份 YAML 并返回非零退出码；不要再依赖无法解析的 `swaggerhub-actions/validate-openapi@v1`。

- [ ] **Step 4: 接入 Actions 和 Services gate**

让 `api-spec-lint` 运行本地校验命令；让 `make validate-services` 继续负责 Services 语义、路由和生成物门禁。不要把既有 baseline 警告改成无条件通过。

- [ ] **Step 5: 验证**

```bash
cd repo
make validate-openapi-spec
make validate-services
```

预期：两份 OpenAPI 通过结构校验，Services 只输出已登记 baseline warning，不出现 error。

- [ ] **Step 6: 提交**

```bash
git add .github/workflows/ci.yml repo/ci/requirements-contract.txt repo/scripts/validate_openapi_spec.py repo/scripts/validate_openapi_spec_test.py repo/Makefile
git commit -m "ci: run repository-owned OpenAPI validation"
```

### Task 5: 建立始终运行的 required-gates 聚合门禁

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `repo/scripts/validate_ci_workflow.py` 及其测试
- Create: `docs/CI-门禁与AI协作规范.md`
- Modify: `ANI-DOCS-INDEX.md`
- Modify: `CLAUDE.md`，只增加稳定入口和 required-gates 规则摘要
- Modify: `repo/CURRENT-SPRINT.md`，记录 CI 修复批次和当前验证状态

**Interfaces:**
- job 名称固定为 `required-gates`，显示名固定为 `Required PR Gates`。
- `required-gates` 必须 `needs` 所有必须 job，使用 `if: ${{ always() }}`，并在 shell 中检查每个 `needs.<job>.result == 'success'`；任何 `failure`、`cancelled` 或意外 `skipped` 都失败。
- workflow 使用 `concurrency` 取消同一 PR 的旧运行；必须保留最新提交的结果。

- [ ] **Step 1: 写聚合逻辑测试**

为 success、failure、cancelled、skipped 四种 job result 编写 shell/Python fixture 测试，确认只有全 success 返回 0。

- [ ] **Step 2: 实现聚合 job**

为 `go-ci`、`python-ci`、`frontend-ci`、`services-pr-gate`、`dependency-scan`、`api-spec-lint` 增加 `needs`，再增加 `required-gates`。不得设置 `continue-on-error: true`。

- [ ] **Step 3: 加入工作流基础安全设置**

在 workflow 顶层设置 `permissions: contents: read`、合理 `timeout-minutes` 和 PR concurrency。第三方 Action 后续固定到 commit SHA，并由 Dependabot 或明确升级 PR 管理。

- [ ] **Step 4: 更新治理文档**

在 `docs/CI-门禁与AI协作规范.md` 明确：PR 必须引用 `Required PR Gates`；AI Agent 不得声称 skipped 等于 passed；Services PR 必须 API-first、运行本地命令、说明 baseline 变化、提供回滚方案；依赖/工作流/Makefile 变化必须 Core owner review。

- [ ] **Step 5: 验证**

```bash
cd repo
make validate-ci-workflow
make test
make validate-services
make validate-architecture
git diff --check
```

然后提交 PR，确认 GitHub Actions 中 `Required PR Gates` 在每个 PR 上出现且只在所有 required job 成功时通过。

- [ ] **Step 6: 提交**

```bash
git add .github/workflows/ci.yml repo/scripts/validate_ci_workflow.py repo/scripts/validate_ci_workflow_test.py docs/CI-门禁与AI协作规范.md ANI-DOCS-INDEX.md CLAUDE.md repo/CURRENT-SPRINT.md
git commit -m "ci: add fail-closed required PR gate"
```

### Task 6: 配置 main 分支保护和 CODEOWNERS 审查规则

**Files:**
- Modify: `.github/CODEOWNERS` only if validation finds a missing ownership rule
- Document: `docs/CI-门禁与AI协作规范.md`
- GitHub repository settings: `main` branch ruleset/branch protection

**Interfaces:**
- `main` 禁止直接 push、force push、删除；必须通过 PR。
- 必须要求状态检查 `CI / Required PR Gates`，并启用 “Require branches to be up to date before merging”。
- 必须要求至少 1 个非 PR 作者的 approving review，并启用 CODEOWNERS review；对 Services API、handler、SDK、前端和 Services 文档至少要求 Services owner 参与，对 Core/CI/Makefile/架构文件要求 Core owner 参与。
- 必须要求 unresolved conversation 为 0；管理员也受规则约束，除非有明确 break-glass 记录。

- [ ] **Step 1: 在 GitHub 设置 main ruleset**

设置上述规则，required status check 只填稳定的 `CI / Required PR Gates`，不要把会频繁重命名的内部 job 名逐个写入分支保护。

- [ ] **Step 2: 用故意失败的验证 PR 验证阻断**

创建不改变业务的临时 PR，让一个 contract test 故意失败；确认 `Required PR Gates` 为 failure 且 GitHub 不显示可合并。关闭该临时 PR 后再删除测试改动。

- [ ] **Step 3: 用真实 Services PR 验证审查路由**

确认只改 Services 目录会请求 Services owner；改 `.github/workflows/ci.yml`、`repo/Makefile`、Core API 或 `repo/pkg` 会请求 Core owner；混合 PR 同时请求双方。

- [ ] **Step 4: 记录规则变更**

在 `docs/CI-门禁与AI协作规范.md` 写明规则生效日期、required check 精确名称、例外审批人和 break-glass 记录位置。不要把 GitHub UI 中不可审计的临时设置当作唯一文档。

### Task 7: 建立适合多人和 AI Agent 的 PR 模板与持续审计

**Files:**
- Create: `.github/pull_request_template.md`
- Modify: `.github/CODEOWNERS` if the template/governance files need explicit owners
- Modify: `docs/CI-门禁与AI协作规范.md`
- Modify: `ANI-DOCS-INDEX.md`
- Test: `repo/scripts/validate_ci_workflow_test.py` or a small template validator

- [ ] **Step 1: 添加 PR 必填清单**

模板必须要求填写：变更层级（Core/Services/mixed）、路径范围、API-first 顺序、生成物来源、执行过的命令、CI 失败是否为 setup/configuration failure、baseline 新增/删除、租户/权限/幂等性影响、回滚方式、人工 reviewer。

- [ ] **Step 2: 加入 AI Agent 特有约束**

明确 AI Agent 不得自我批准、不得把本地测试替代远端 required check、不得修改 baseline 只为消除失败、不得把 `continue-on-error` 或手工跳过作为修复；发现 CI setup failure 必须修 workflow 或明确阻断 PR。

- [ ] **Step 3: 验证模板和文档入口**

```bash
cd repo
make validate-doc-entrypoints
make validate-ci-workflow
git diff --check
```

- [ ] **Step 4: 提交**

```bash
git add .github/pull_request_template.md docs/CI-门禁与AI协作规范.md ANI-DOCS-INDEX.md repo/scripts/validate_ci_workflow_test.py
git commit -m "docs: define Services CI and AI PR operating rules"
```

## 交付顺序与暂停标准

1. **P0：Task 1–4**。在 required-gates 生效前，先让每个基础检查真正可执行；任何 job 因 setup 失败都不能进入下一阶段。
2. **P1：Task 5–6**。required-gates 和 `main` ruleset 同一批次启用；在此之前不把失败 CI 当作可接受合并状态。
3. **P2：Task 7**。让多人和 AI Agent 使用同一套 PR 证据格式。

暂停标准：若 Go、Python、OpenAPI 或 Services gate 仍有 setup failure，停止扩大 Services 解冻范围；若只剩代码/契约真实失败，则按失败项修复，不得添加 warning baseline 或放宽门禁。

## 最终验收

以下条件全部满足才称为 CI 治理完成：

- GitHub Actions 的每个 required job 在干净 Ubuntu runner 上实际执行，而不是在安装/路径/Action 解析阶段失败。
- `CI / Required PR Gates` 在新 PR 上稳定出现，并能汇总 failure、cancelled、skipped。
- `make test`、`make validate-services`、`make validate-architecture`、OpenAPI 双 spec 校验、依赖扫描均有远端成功记录。
- Services 新增 Core import、越界路径、OpenAPI/Gateway 新差异、语义违规、生成物漂移、文档入口错误都能阻断 PR。
- main 分支无法绕过 PR、required gate、CODEOWNERS review 和 unresolved conversation 规则。
- PR 模板能让人和 AI Agent明确区分“代码失败”“CI 配置失败”“既有 baseline 告警”。
- CI 修复本身的 PR 不得通过关闭检查、强制合并或把失败改成 warning 来完成。
