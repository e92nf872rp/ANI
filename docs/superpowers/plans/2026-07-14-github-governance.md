# ANI GitHub 协作治理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不压低 Core/Services 并行速度的前提下，落地 ANI GitHub 规范、AI Coding 实践、Services 速成工具和可强制执行的 CI/main 收口规则。

**Architecture:** 开发和 CI job 并行，只有 main 合并受保护地收口。仓库内文档、模板和脚本负责人的日常动作，GitHub branch protection/CODEOWNERS/Required PR Gates 负责不可绕过的合并条件；跨层依赖用契约 PR 和 stacked PR 表达，而不是全局串行锁。

**Tech Stack:** Markdown、GitHub Actions、Bash、Make、Go 1.23、Python 3.12、PyYAML、OpenAPI validator、npm。

## Global Constraints

- 不修改或删除现有未跟踪的 `.worktrees/` 和 `Picture/` 文件。
- 不在 `main` 上开发；当前分支必须是 `codex/github-governance-20260714`。
- `main` 只接受 PR；本分支中的 CI 修复必须让失败真正阻断，不得使用 `continue-on-error` 掩盖。
- Services API-first、CODEOWNERS、架构边界、公开仓库凭据保护和 baseline fail-closed 规则不可放宽。
- CI 修复与治理文档必须保持可独立审查；先修可执行性，再配置 required status。

### Task 1: 完成正式设计和文档入口

**Files:**
- Create: `ANI-15-GitHub-协作规范与提交纪律.md`
- Modify: `CLAUDE.md`
- Modify: `ANI-DOCS-INDEX.md`
- Test: `make validate-doc-entrypoints`

- [ ] 建立短主规范：八条原则、角色职责、分支模型、并行/依赖流程、PR、AI Coding、Free+public 安全和异常处理。
- [ ] 明确“并行开发、串行 main 收口”，删除全局一个跨层 PR 和 AI 分支不可合并等不必要限制。
- [ ] 在 CLAUDE 只增加入口，不复制长流程；在文档索引注册 ANI-15。
- [ ] 运行 `cd repo && make validate-doc-entrypoints`。
- [ ] 提交 `docs: add ANI GitHub collaboration standard`。

### Task 2: Services 速成手册和可执行脚本

**Files:**
- Create: `docs/ops/github-quickstart-for-services.md`
- Create: `docs/ops/scripts/new-feature.sh`
- Create: `docs/ops/scripts/sync-main.sh`
- Create: `docs/ops/scripts/prep-ai-batch.sh`
- Create: `.gitmessage`
- Create: `.gitmessage-ai-batch`
- Create: `.gitattributes`

- [ ] 速成手册只保留 Issue、建分支、修改、三条本地命令、push/PR、review、合并后同步和四个绝对禁忌。
- [ ] 脚本在脏工作区、非预期分支、非法 scope、远端同步失败时停止，不自动覆盖代码。
- [ ] 支持并行 worktree，不要求开发者等待其他 PR 完成。
- [ ] 运行 shell syntax check、脚本 dry-run fixture、`git diff --check`。
- [ ] 提交 `docs: add Services GitHub quickstart and scripts`。

### Task 3: PR/Issue 模板和协作证据

**Files:**
- Create: `.github/PULL_REQUEST_TEMPLATE.md`
- Create: `.github/ISSUE_TEMPLATE/bug_report.md`
- Create: `.github/ISSUE_TEMPLATE/feature_request.md`
- Create: `.github/ISSUE_TEMPLATE/ai_coding_batch.md`
- Modify: `.github/CODEOWNERS` only if template/CI ownership is missing

- [ ] PR 模板要求目标、范围、跨层影响、验证命令、CI 链接、AI 生成比例/风险、回滚方式。
- [ ] AI 批次模板要求 Issue、切片、依赖关系和二次验证，不要求所有 AI 分支使用特殊不可合并规则。
- [ ] Issue 模板适合 Core、Services 和 AI Agent，不要求 Services 成员掌握复杂 Git 理论。
- [ ] 验证模板路径、Markdown fenced code 和 CODEOWNERS 归属。
- [ ] 提交 `docs: add PR and issue collaboration templates`。

### Task 4: 修复 CI 可执行性

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `repo/Makefile`
- Create: `repo/ci/requirements-contract.txt`
- Modify: `repo/ai/rag-engine/requirements.txt` only after clean resolver verification
- Create or modify: `repo/scripts/validate_openapi_spec.py` and its test

- [ ] 将 Go cache 从 `/private/tmp` 改为仓库 `.cache` 或 runner 默认路径。
- [ ] 让 `govulncheck` 按 `go.work` module 执行，不在无 `go.mod` 的仓库根目录执行。
- [ ] Services gate 只安装契约校验依赖，避免被 RAG 运行时依赖冲突提前截断。
- [ ] 通过干净 Python 3.12 环境解决并验证 `pydantic-settings` / `langchain-community` 冲突。
- [ ] 用仓库内 OpenAPI validator 校验 Core 和 Services spec，移除不可解析的 SwaggerHub Action。
- [ ] 远端每个失败 job 必须到实际检查步骤，而不是 setup failure。
- [ ] 提交 `ci: restore executable repository gates`。

### Task 5: Required gate 和 main 分支保护

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create or modify: `repo/scripts/validate_ci_workflow.py` and its test
- Modify: `ANI-15-GitHub-协作规范与提交纪律.md`
- GitHub Settings: `main` branch protection/ruleset

- [ ] 增加始终运行的 `Required PR Gates` 聚合 job，任何 failure/cancelled/unexpected skipped 都失败。
- [ ] 增加 workflow `permissions: contents: read`、timeout、同一 PR 旧运行取消和第三方 Action 版本管理。
- [ ] 配置 main：PR、非作者 approval、CODEOWNERS、Required PR Gates、分支最新、conversation resolved、禁止直推/force push/delete。
- [ ] 用一个故意失败的验证 PR 验证 GitHub 确实阻断合并。
- [ ] 记录 required status 的精确名称和例外流程。
- [ ] 提交 `ci: enforce fail-closed main integration gate`。

### Task 6: 验收和交付

**Files:**
- Modify: `repo/CURRENT-SPRINT.md` only with verified status
- Modify: `ANI-DOCS-INDEX.md` if new entrypoints were added

- [ ] 本地：`make test`、`make validate-services`、`make validate-architecture`、`make validate-doc-entrypoints`、CI workflow tests、`git diff --check`。
- [ ] 远端：所有 required jobs 实际执行并通过，Services gate 不再被依赖安装截断。
- [ ] 并行验证：两个独立 feature 分支可同时开发，互不覆盖；一个 PR 合并后另一 PR 通过更新 main 重新验证。
- [ ] 安全验证：PR/Issue/Actions 日志不含 secrets、真实 IP、内部 URL。
- [ ] 使用 `superpowers:finishing-a-development-branch` 做最终分支交付决策，不在未经核验时声称 main 已安全。

