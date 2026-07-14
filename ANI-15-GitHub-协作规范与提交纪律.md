# ANI GitHub 协作规范与提交纪律

> 版本：v1.0（2026-07-14）
> 适用范围：ANI Core、ANI Services、Console、文档、CI，以及人类和 AI Coding Agent 贡献者
> 仓库边界：GitHub Free + public；本文是协作规则，不替代架构、API、版本和安全规范。

## 1. 先记住这一件事：开发并行，main 串行收口

ANI 不是“一个人完成后下一个人才能开始”的队列。并行开发的最小模型是：

```text
多个任务 / 多个分支 / 多个 AI Agent
              │ 并行编码、本地验证、创建 PR
              ▼
       多个 PR 并行运行 CI 和审查
              │ 只有真实依赖才排序
              ▼
       main 受保护，逐个 squash 合并收口
              ▼
       各分支按需同步最新 origin/main
```

以下工作可以同时进行：

- Core、Services、Console 和文档的无依赖任务；
- 不同分支的编码、本地测试、PR 创建、CI job 和 review；
- 一个 PR 等待 review 时，其他独立 PR 继续开发。

以下情况才需要排序：

- 后一个 PR 依赖前一个 PR 尚未进入 `main` 的 API、schema、生成物或文件重命名；
- 两个 PR 修改同一契约或同一高冲突文件，必须先确定兼容方向；
- 前一个 PR 合并后导致后一个 PR 需要解决冲突或重新验证。

不存在“全仓库一次只能有一个 PR”“Services 只能等 Core 完成”“AI 分支不得合并”等全局锁。所有贡献者使用同一套 PR、审查和门禁规则；代码由 AI 生成不改变责任归属，也不自动获得快速通道。

## 2. 八条核心原则

1. **保护 main。** `main` 表示 ANI 整体的集成基线，只通过 PR 写入；禁止直接 push、force push 和删除。
2. **保持可集成。** 合并前必须有可核验的本地验证和远端 required gate 结果；CI setup failure、cancelled、unexpected skipped 或 unknown 都不是通过。
3. **一个 PR 一个可回滚目标。** 单个 PR 应有一个清晰意图，能够单独 review、squash 和 revert；大批量 AI 输出先切成可验证的切片。
4. **契约先行。** Core API、Services API、Proto、schema 或生成物发生变化时，先明确契约和兼容性，再实现 handler、SDK、生成物和测试。
5. **并行优先，依赖排序。** 无依赖任务不等待；只有真实的代码、契约或冲突依赖才要求 stacked PR、先后顺序或重新同步。
6. **AI 输出必须可解释。** PR 作者必须理解并能说明目标、关键实现、风险和验证结果；“AI 生成”不是审查责任的转移。
7. **安全默认拒绝。** public 仓库中的代码、Issue、PR、日志均按公开信息处理；秘密、凭据、客户数据和内部基础设施信息不得提交或打印。
8. **问题可逆、记录可追溯。** 优先通过 revert 恢复错误合并；绕过正常门禁的紧急处理必须在公开 PR/Issue 中记录原因、范围和补偿验证。

## 3. 日常最短流程

每个任务只需掌握以下闭环；它不是全员串行队列，而是每个任务自己的生命周期。

### 开始任务

1. 找到可追踪的任务来源：Issue、`repo/CURRENT-SPRINT.md` 条目或明确的 PR 需求。
2. 获取远端状态并从 `origin/main` 创建独立分支。长期并行任务使用 worktree，避免多个 Agent 覆盖同一工作区。
3. 判断是否有 Core/Services/API/共享文件依赖；无依赖就直接并行开发，有依赖就在 PR 描述中标明先后关系。

### 开发和提交前

1. 先读与任务相关的 `CLAUDE.md`、`ANI-DOCS-INDEX.md`、当前 Sprint、架构和 API 契约。
2. 保持改动聚焦；不要把无关格式化、顺手重构或另一项功能混入 PR。
3. 本地至少运行与改动匹配的门禁。跨层或 Services 变更执行 `make validate-services`；代码批次按项目要求执行 `make test`、`make validate-architecture` 和 `git diff --check`。
4. 提交信息使用 Conventional Commits，例如 `feat(services): add inference contract`、`fix(core): handle retry state`、`docs: clarify PR workflow`。

CI 对 AI Coding 的最低行为要求是：修改 `ai/` 下 Python 源码必须在同一 PR 提交测试；Go lint 和依赖扫描必须覆盖 `repo/go.work` 实际列出的全部 module；Go、Python 和 npm 的 high/critical 依赖漏洞必须阻断；Services boundary/API/route 的新增问题和过期 baseline 必须失败。存量 baseline 只能作为有负责人、有原因的迁移债务告警，不能作为新问题的通行证。

### PR 和合并

1. push 独立分支并创建 PR；PR 描述写清目标、范围、依赖、验证、风险、AI 参与和回滚方式。
2. CI 与 review 同时进行。作者可以继续处理其他独立任务，不需要等待本 PR 完成才能编码。
3. 收到修改意见后，作者更新同一 PR 并重新验证；若变更影响审查结论，按仓库设置重新获得 approval。
4. 合并前，分支应按保护规则同步到最新 `main` 并重新通过 required gate。只有需要合并的 PR 做这一步，不要求所有开发分支每天 rebase。
5. 默认使用 squash merge，使一个 PR 对应一个可回滚的 main 提交。合并后的其他分支不自动失效；它们在下一次 push/PR 更新或出现冲突时再同步 `origin/main`。

## 4. 分支和并行协作

### 分支要求

- `main` 是唯一受保护主干，不在其上开发。
- 功能、修复、文档、CI 和 AI Coding 均使用普通短期分支；分支名称应包含范围和目的，例如 `feature/services-inference-contract`、`fix/core-retry`、`docs/github-governance`。
- AI Agent 可以使用独立分支或 worktree，但不能共享一个会被多个 Agent 同时写入的工作目录。
- 分支应从创建时的 `origin/main` 开始。分支创建后不要求因为别人每次合并就立即 rebase；在提 PR、解决冲突或合并前再同步即可。

### 无依赖任务

多个团队可以同时从同一 `origin/main` 快照创建分支。只要没有共享文件或契约依赖，Core 和 Services 可以并行编码、测试、审查和运行 CI。

### 有依赖任务

如果 Services 实现依赖尚未合并的 Core API，或两个 PR 共享一个契约：

1. 先在 Issue/PR 中写明依赖和兼容策略；
2. 可以使用 stacked PR、兼容性 stub 或先合并契约 PR，让实现工作继续进行；
3. 依赖 PR 合并后，后续 PR 同步最新 `origin/main`、解决冲突并重新运行 CI；
4. 不因存在一个跨层 PR 就锁住整个仓库。

## 5. Core、Services 和共享边界

- ANI Core 继续负责基础设施平台底座；ANI Services 处于受控并行 PR 阶段，不再按旧冻结规则处理。
- Services 业务资源维护在 `repo/api/openapi/services/v1.yaml`；Core 契约维护在 `repo/api/openapi/v1.yaml`，Services 不把业务资源回流到 Core API。
- Services 通过 Core OpenAPI REST API / Core SDK 调用 Core；不得 import Core 内部包、直接调用 Core 内部 gRPC service 或绕过既定 ports/adapters 边界。
- 触碰 Core 保护目录、Services API、Gateway mixed handler、SDK 或生成物时，应让对应 owner 参与审查，并执行相应 API、边界、路由、语义和生成物门禁。
- 文档、测试和 CI 改动也必须保持当前架构边界；“只是文档”不构成修改错误边界的豁免。

## 6. PR 的最低证据

PR 作者必须提供足够信息让不熟悉背景的人和 AI Agent 都能判断改动，不要求写长篇报告：

```text
目标：解决什么问题，关联哪个 Issue/任务
范围：改了哪些层和目录，明确未改什么
依赖：是否依赖其他 PR、API、schema 或生成物
验证：实际运行的命令及结果；未运行的命令说明原因
AI Coding：AI 参与范围、作者已复核的关键路径、不确定点
风险/回滚：兼容性、权限、租户、幂等、数据、发布和回滚影响
```

以下情况应拆 PR 或先与 reviewer 确认，而不是机械等待：

- 一个 PR 同时包含多个独立产品目标；
- 功能、重构和大规模格式化混在一起；
- 跨 Core/Services/Console 的改动无法说明契约依赖；
- AI 一次生成的大批量代码无法由作者解释和验证。

diff 行数只是切片信号，不作为所有任务的固定硬上限；API 初建、生成物或必要迁移可以较大，但必须说明原因并提供分阶段验证办法。

## 7. AI Coding 实践

### 作者责任

AI Agent 可以读取规范、修改代码、运行测试和创建草稿 PR，但最终作者负责：

- 确认任务范围没有扩大；
- 阅读并理解关键路径、错误处理、权限、租户、幂等和跨层调用；
- 核对 AI 输出没有伪造测试结果、凭据、外部状态或完成声明；
- 对不确定的行为在 PR 中标记并请求人工判断；
- 以新鲜命令输出作为验证证据，不把“代码看起来合理”当作通过。

### 推荐的 AI 批次

```text
理解任务 → 列出最小切片 → 修改 → 自审 diff
→ 运行门禁 → 记录真实结果 → 创建/更新 PR → 并行等待 review/CI
```

AI 批次不需要专用的“不可合并分支”。探索性输出如果还不能满足 PR 证据要求，应留在分支或草稿 PR 中；完成切片和验证后，按普通 PR 进入相同门禁。

## 8. main 保护和 GitHub Free + public 边界

### 应配置的 main 保护

仓库管理员应在 GitHub 仓库设置中核实并逐项配置可用的分支保护能力：

- 必须通过 PR，禁止直推、force push 和删除；
- required status checks 必须来自真实执行的 CI job/聚合 gate；
- 至少一名非作者 reviewer approval，涉及 CODEOWNERS 路径时由对应 owner 审查；
- 新提交导致旧 approval 失效时重新审查；
- 合并前要求分支基于最新 `main`，并解决未完成 review conversation；
- 默认允许 squash merge，并保持可回滚、可定位的线性集成历史；
- 管理员也遵守保护规则。若 GitHub Free 的当前设置不提供某项能力，不得在文档或 PR 中声称该项已经强制生效，应记录替代控制和缺口。

当前分支已落地 CI workflow 的可执行修复、Go workspace module 自动发现、Python AI 变更测试策略、`Required PR Gates` fail-closed 聚合 job 和本地 workflow 契约测试；这只能证明仓库中的门禁定义可执行，不能替代 GitHub Settings 中的 branch protection。`main` 的实际保护状态、Required PR Gates 是否已设为 required、CODEOWNERS approval 和故意失败 PR 的阻断效果，必须在 GitHub 上单独核验后才能宣称生效。

### public 仓库安全

- 不提交密码、token、私钥、真实服务器地址、内部域名、客户数据或可识别的生产日志。
- Actions 默认只读权限；来自不可信 PR 的代码不得访问生产 secrets。第三方 Action 固定到可审计版本，升级走 PR。
- 日志、测试 fixture、Issue 和 PR 描述也按 public 内容处理；脱敏不能靠“以后删除”。
- 发现凭据泄漏时，先撤销/轮换凭据，再清理和记录事故；不要只删除工作区文件。

## 9. 异常、紧急变更和回滚

- CI 配置故障不能伪装成代码通过。若 required gate 尚未可靠执行，管理员应修复门禁或明确记录风险，不得把 setup failure 当作绿灯。
- 紧急 hotfix 可以缩短审查路径，但仍使用 PR、保留验证证据，并在事后补齐 review、测试和记录；不得把紧急路径变成日常快速通道。
- 合并后发现问题，优先 revert 对应 squash commit；修复 PR 另行提交，避免在 main 上直接改回。
- 出现反复冲突时，优先拆分任务、明确契约依赖或重建分支，不通过关闭保护规则解决并行问题。

## 10. 真实来源和后续落地

本文只负责 ANI 的 GitHub 协作、提交、PR、AI Coding 和 main 收口规则。下列内容仍以各自真实来源为准：

| 主题 | 真实来源 |
|---|---|
| 当前 Sprint 和任务状态 | `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` |
| Core/Services 架构边界 | `ANI-05-系统架构设计.md`、`CLAUDE.md` |
| API 契约 | `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml` |
| Services 开发目录和接口说明 | `ANI-SERVICES-TEAM-GUIDE.md`、`repo/services/README.md` |
| 版本和发布策略 | `ANI-12-版本管理策略.md` |
| CI 工作流和执行门禁 | `.github/workflows/ci.yml`、`repo/Makefile`；实际配置状态以 GitHub 运行结果为准 |

后续落地顺序是：先修复 CI 的可执行性，再建立 fail-closed 的聚合 required gate，最后在 GitHub 上启用 main 分支保护并用故意失败的验证 PR 检查阻断效果。规范、模板、脚本和 GitHub 设置分别可独立审查；不要求等所有治理工作完成后才允许无依赖任务并行开发。
