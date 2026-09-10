# REPO-SCOPE-CLEANUP-A

> 日期：2026-09-07
> 状态：实现完成，GitHub CI 待 PR 验证

## 决策

- 回滚 PR #60、#62、#68 引入的邮件通知能力；Core 不再提供 `/api/v1/notifications/email/*`。
- Console 与 BOSS 前端源码已由独立仓库维护，本仓库不再保存前端源码、依赖锁文件、生成类型、构建目标或前端 CI job。
- 保留历史开发记录中对 Console/BOSS 的事实描述；它们不再代表本仓库中的可构建前端实现。

## 变更

- 删除邮件通知 OpenAPI schema/path、兼容性基线条目、Core SDK 暴露、静态 API 文档、Gateway authz policy、port、local adapter、handler、测试和专项文档。
- 删除 `repo/frontends/boss` 与 `repo/frontends/console`。
- 从 Makefile、GitHub Actions、CODEOWNERS 和 CI policy validator 中移除前端生成、lint、dev、build 和 required-gate wiring。
- `gen-api` 仅保留 Gateway Go 代码生成；Core/Services SDK 与静态 API 文档仍由各自生成器维护。

## 验证边界

按用户明确要求，本地不运行 CI 或测试套件；只执行代码生成同步和静态差异/引用检查。完整 CI 结果以 GitHub PR 为准。
