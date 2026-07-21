# ANI GitHub 协作治理设计

## 目标

在 ANI Core、ANI Services 多人并行且 AI Coding 占主导的情况下，让开发速度不被流程串行化，同时保证 `main` 始终是可审查、可回滚、可集成的整体。

## 核心判断：并行开发，串行收口

流程不是让所有人依次等待，而是把不同阶段拆开：

```text
多个 Issue / 多个分支 / 多个 AI Agent
              │ 并行开发、并行本地验证
              ▼
       多个 PR 并行审查和 CI
              │ 只有依赖关系需要排序
              ▼
       main 受保护、逐个 squash 收口
              ▼
       合并后各自同步本地 main
```

只有 `main` 的写入是串行的；编码、测试、PR 创建、CODEOWNERS 审查和 CI job 都可以并行。跨层变更只有在真实存在契约依赖时排序，不设置“全仓库同时只能有一个 PR”的人为锁。

## 设计原则

1. `main` 只接受 PR，不接受直接 push；合并前必须通过稳定的 required gate 和人工审查。
2. 每个开发者或 AI Agent 使用独立分支；长期并行任务使用 worktree，互不覆盖工作区。
3. 一个 PR 一个可回滚目标；大批量 AI 输出先拆成多个可验证切片。
4. API-first：先改对应 Core/Services OpenAPI 契约，再实现 handler、SDK、生成物和测试。
5. 不用“AI 分支禁止合并”制造特殊流程；AI 代码和人工代码进入同一套审查、CI 和责任规则。
6. 本地验证是快速反馈，远端 required gate 是合并依据；任何 skipped、setup failure 或 unknown 都不算通过。
7. 既有 baseline 是事实记录，不是永久豁免；新违规和 stale baseline 必须阻断。
8. 所有变更可通过 squash commit 单点回滚；紧急绕过必须留下公开、可审计的事后记录。

## 并行工作模型

### 无依赖任务

Core、Services、Console、文档等无交叉文件依赖的任务可以同时开发。每个任务从同一个 `origin/main` 快照创建独立分支，互不 rebase 对方分支。

### 有契约依赖的任务

Core API 或 Services API 先作为契约 PR。实现 PR 可以在契约分支上提前开发，使用 stacked PR 或 Issue 依赖表达顺序；契约 PR 合并后，实施 PR rebase 到最新 `origin/main`，重新跑 CI，再进入合并队列。

### 合并后的本地同步

某个 PR 合并不会阻塞其他开发者的 feature 分支。其他分支在提 PR 前同步 `origin/main`，解决自己的冲突并重新验证；本地 `main` 的同步是每个工作区自己的收尾动作。

## GitHub 收口机制

- `main`：禁止直推、force push、删除；必须 PR、CODEOWNERS review、required gate、conversation resolved。
- required gate：使用稳定的 `CI / Required PR Gates` 聚合状态，汇总 Go、Python、Console、OpenAPI、Services contract 和依赖安全检查。
- 聚合状态必须在任何依赖 job failure、cancelled 或 unexpected skipped 时失败，不能用 `continue-on-error` 掩盖。
- 启用 “Require branches to be up to date before merging”，防止基于旧 main 合并。
- 普通 PR 至少一名非作者 reviewer；Core/Services/API/CI/Makefile 等路径按 CODEOWNERS 请求对应负责人。
- 默认使用 squash merge；PR 只代表一个可回滚目标。

## AI Coding 约束

AI 负责提高编码吞吐，不改变责任归属：PR 作者必须能够解释目标、关键设计、风险和验证结果。AI 批次必须有 Issue、切片计划、分支和验证记录；不要求作者逐行复制审查仪式，但要求对关键路径、权限、租户、幂等、错误处理和跨层边界进行人工确认并标记不确定点。

## Free + public 安全边界

- 所有代码、Issue、PR 描述和 Actions 日志按公开信息处理；禁止写入密码、token、真实服务器 IP、内部域名和客户信息。
- Actions 默认 `contents: read`；来自不可信 PR 的 workflow 不访问生产 secrets。
- 第三方 Action 固定版本并通过 PR 升级；CI setup failure 必须失败，不可转为 warning。
- 凭据泄漏时先 rotate，再清理历史并发布事故记录；错误代码进入 main 时优先 revert 原 squash commit。

## 落地形态

正式仓库内容分为：

- 主规范：规则、角色、分支、PR、AI、公开仓库安全。
- Services 速成手册：只保留日常必须掌握的命令和判断。
- PR/Issue/commit 模板：把要求变成填写动作。
- 一键脚本：创建分支、同步 main、准备 AI 批次，脚本遇到脏工作区直接停止。
- CI 门禁：工作流修复、聚合 required gate 和分支保护设置。

