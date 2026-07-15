# ANI Services GitHub 速成手册

这份手册只回答 Services 工程师每天需要做什么。复杂 Git 理论不是提 PR 的前置条件；不确定时保留现场、不要强行修复历史，直接在 PR 中 @ Services Lead 或 Architect。

## 日常主流程

~~~text
Issue → 独立分支/worktree → 修改 → 本地验证 → PR → 并行 review/CI → squash 合并 → 同步本地 main
~~~

多个开发者和 AI Agent 可以同时执行这条流程。只有写入受保护 main 时需要排队；一个人的 feature 分支不会阻塞其他人的开发。

## 1. 从最新 main 创建分支

推荐使用脚本：

~~~bash
./docs/ops/scripts/new-feature.sh services models-crud
~~~

手动方式：

~~~bash
git status --short
git switch main
git fetch origin main
git merge --ff-only origin/main
git switch -c feature/services/models-crud
~~~

允许的 scope：core、services、gateway、auth、storage、network、k8s、gpu、observability、rag、frontend、infra、docs、ci。

## 2. 修改前先确认边界

- Services 业务资源先改 repo/api/openapi/services/v1.yaml。
- Core API 先改 repo/api/openapi/v1.yaml。
- 不要让 Services import Core 内部包、直接操作 Kubernetes 或底层组件。
- 生成的 SDK、API docs、Console 类型必须从契约重新生成，不手工改生成物。
- 跨层变更在 Issue/PR 中写明依赖关系；不要求所有任务停止等待，但实现 PR 必须在契约 PR 合并后 rebase 和重跑 CI。

## 3. 提 PR 前运行

~~~bash
cd repo
make test
make validate-architecture
make validate-services
make validate-doc-entrypoints
git diff --check
~~~

把实际运行的命令、通过/失败结果和 CI 链接填入 PR 模板。CI 初始化失败不是“可以忽略的红灯”，必须修复或停止合并。

## 4. PR 和 review

- PR 只做一个可回滚目标；AI 一次生成很多文件时先拆成多个切片。
- CODEOWNERS 会按路径请求 Core/Services reviewer；跨层、CI、Makefile 和安全文件通常需要双方关注。
- 作者不能自我批准；reviewer 提出修改后，push 新 commit 并重新请求 review。
- 只有 Required PR Gates、review 和 conversation 都满足后才能 squash 合并。

## 5. 合并后同步本地 main

~~~bash
./docs/ops/scripts/sync-main.sh
~~~

这只更新当前工作区的本地 main，不会修改其他人的 feature 分支。其他 feature 分支在下一次提 PR 前执行：

~~~bash
./docs/ops/scripts/sync-main.sh --rebase
~~~

脚本遇到未提交修改会停止，不会覆盖代码。

## 6. AI Coding 的最低要求

- AI 工具、生成比例、关键风险和人工验证写在 PR 中。
- 作者必须读懂并验证 AI 输出；不确定位置明确标记，不能用“AI 说可以”代替证据。
- AI Agent 使用自己的分支或 worktree，不直接写 main，不修改别人的工作区。
- 不得为了消除 CI 失败删除测试、扩大 baseline、使用 continue-on-error 或伪造通过结果。

## 四个绝对禁忌

1. 不在 main 直接改代码或 force push。
2. 不把密码、token、真实服务器 IP、内部域名写进 public 仓库、PR 或 Actions 日志。
3. 不把 OpenAPI/Gateway 不一致当成“先合并再说”。
4. 不把本地通过、CI skipped 或 setup failure 说成远端 CI 通过。

