# DP2-DIRECT-P2-SAFE-MERGE — Direct P2 安全合并候选

记录日期：2026-09-06

状态：Merge-Ready 已于 2026-09-06 人工接受；Standards/Spec 双轴评审均为 0 findings，80-path staged package 与检查点后全部本地强制门禁为 `pass`。本记录随本地 merge commit 落地，不含 push、远端 PR 或部署授权。

兼容性影响：`MAJOR`。候选包含 DP2-02 已人工接受但仍属破坏性的 `/api/v1` 目标契约；当前仅形成默认 disabled 的本地合并候选，不发布版本。DP2-03 独立 additive Proto 自身为 `MINOR`，安全修复自身为 `PATCH`，整体按最高等级 `MAJOR` 记录。

## 固定身份与范围

- 人工接受的 ANI main 基线：`bde4ea72b5a91cd43cc271dd44c09ff262c637e5`
- Direct P2 来源分支：`codex/direct-p2-01-05`
- 来源 HEAD：`f09a436c6edbd752271d1e4502bbdfd1f1b9e690`
- merge-base：`0cedae825a489d936cf41815dc27f278f6d3213c`
- integration worktree：`/home/chabking/workspace/ANI-direct-p2-safe-merge`
- integration 分支：`codex/direct-p2-safe-merge`
- 合并方式：`git merge --no-ff --no-commit f09a436c6edbd752271d1e4502bbdfd1f1b9e690`

来源提交顺序和身份保持不变：

1. `a221a7b50c2cfdb13f04c13f154338d836a48af3` — `feat(dp2-02): freeze public iam operation registry`
2. `573d3735934f74f9f1eb78818cddefefd9f575eb` — `feat(dp2-03): freeze core iam integration contracts`
3. `f09a436c6edbd752271d1e4502bbdfd1f1b9e690` — `feat(dp2-05): connect target gateway vertical slice`

本候选不 push、不创建或合并远端 PR、不修改 main、不部署、不切流、不失效 Credential、不删除旧 Auth，也不启动 DP2-06 或后续 IAM ticket。

## 合并冲突与处理

三方重建确认发生过内容冲突的文件为：

- `ANI-06-开发计划.md`：保留 accepted-main 当前摘要，再合入 DP2-02/03/05 状态。
- `repo/CURRENT-SPRINT.md`：保留 accepted-main 当前执行入口，再合入 Direct P2 批次记录。
- `repo/frontends/boss/src/api/core-schema.d.ts`：不手工选择冲突块，按合并后的 Core OpenAPI 使用固定 `openapi-typescript 7.13.0` 重生成。
- `repo/frontends/console/src/api/core-schema.d.ts`：同上，从合并后的唯一契约重生成。
- `repo/services/ani-gateway/main_test.go`：保留 accepted-main Gateway 测试，并合入 target IAM 配置测试。

当前 `git ls-files -u` 为空；`HEAD` 仍是 accepted-main 基线，`MERGE_HEAD` 仍是固定来源 HEAD。

## 安全合并修复

- 未设置 `IAM_TARGET_MODE` 与显式 `disabled` 都不构造 target IAM client，并保持旧 Gateway Auth 链。
- 只有显式 `dp2_05` 才构造 target client；未知 mode、缺地址或缺 mTLS 配置启动失败。
- `dp2_05` 仅选择 Password Login 与 `listInstances` 隔离 tracer；选择后错误 fail closed，不逐请求 fallback。
- target Password Login 绕过通用 Gateway 响应缓存，让 IAM 持有幂等结果；重复 key 仍返回安全 Refresh Cookie，Gateway cache 不保存 Access/Refresh Token 明文。
- Password Login 与授权 middleware 的 `AUTH_RATE_LIMITED` 共用冻结 `retry_after_seconds` 校验：仅正整数返回 429、非空 message 与 `Retry-After`；缺失、零或非法值 fail closed 为 503 `IAM_UNAVAILABLE`。
- production-shaped Gateway manifest 显式设置 `IAM_TARGET_MODE=disabled`，且不包含 target 地址或 TLS 配置。
- Gateway 业务代码只依赖 `pkg/ports.TargetIAM` 产品接口；gRPC、TLS、deadline、protobuf 与远端错误翻译集中在 `pkg/adapters/iam`。
- 固定 IAM protobuf Go 生成物迁入公共 `pkg/generated/pb/iam/v1`，旧 `services/ani-gateway/internal/targetiam` Go 实现和私有生成包删除；固定 descriptor 保持 byte-identical。
- `generate_gateway_authz.py` 只以缺失时补入的方式合并 accepted-main 三项兼容策略，避免覆盖固定 replacement semantics；生成与 drift 门禁通过。

## Accepted-main 三项语义映射

2026-09-06，人工在本安全合并 Goal 中明确接受以下三项 Direct P2 目标语义映射，并单独允许重新生成 `repo/services/ani-gateway/internal/authz/zz_generated_core_policies.go`。该接受解决了 accepted-main 新 operation 与固定 Direct P2 target registry 间的语义决策检查点；disabled-mode 兼容行为仍按 accepted-main 精确保留。

| operationId | Handler / Owner | target authn | target authz | disabled-mode Core 行为 |
|---|---|---|---|---|
| `getPlatformServiceHealth` | `gateway.getPlatformServiceHealth` / `core-control` | human/service；access_token/api_key/service_token；OpenAPI Bearer | observability/read/platform；无 obligation | 保留 accepted-main generated Bearer、user/service |
| `queryResourceTrendObservability` | `gateway.queryResourceTrendObservability` / `core-control` | human/service；access_token/api_key/service_token；OpenAPI Bearer | observability/read/tenant；无 obligation | 保留 accepted-main generated Bearer OR API Key、user/api_key |
| `getPlatformCapacity` | `gateway.getPlatformCapacity` / `core-control` | human/service；access_token/api_key/service_token；OpenAPI Bearer | capacity/get/platform；无 obligation | 保留 accepted-main generated Bearer OR API Key、user |

这些 accepted-main operation 不添加 `x-ani-contract: direct-p2`；target registry 与 disabled-mode Core compatibility 分别表达目标语义和现有运行语义。

## 模式行为矩阵

| 模式 | target client | Password Login | `listInstances` | 其他旧路由 |
|---|---|---|---|---|
| 未设置 | 不构造 | 旧行为 | 旧认证授权链 | public/legacy/generated、API Key、Service Token 行为不变 |
| `disabled` | 不构造 | 旧行为 | 旧认证授权链 | 与未设置相同 |
| `dp2_05` | 必须以 TLS 1.3 mTLS 构造 | target PasswordLogin；无 Gateway token cache | 恰好一次 target CheckPermission；错误无 fallback | 仍使用现有 Gateway 链；不是全 Gateway 切换 |

## 当前验证证据

| 验证 | 结果 | 说明 |
|---|---|---|
| OpenAPI validator / breaking / operation registry / Gateway authz drift | `pass` | OpenAPI 两份有效；registry 298 operations；breaking 与生成物无漂移 |
| Core/target Proto、descriptor 与 fixtures | `pass` | 固定 Buf/protoc 插件重生成 byte-identical；descriptor/fixture tests 通过 |
| Core/Services SDK 生成与幂等 | `pass` | 四语言 source smoke 与 idempotence 通过 |
| Java compile/run | `not_verified` | 当前环境无 JDK；项目 validator 仅执行 Java source smoke |
| Console/BOSS Core schema | `pass` | 固定 `openapi-typescript 7.13.0` 重生成，两次 hash 相同 |
| Gateway `go test ./... -count=1` | `pass` | 全模块通过 |
| Gateway `go vet ./...` | `pass` | 无输出，退出码 0 |
| Gateway 串行 race scope | `pass` | main/authz/`pkg/adapters/iam`/middleware/router 全部通过；旧 `internal/targetiam` 已按架构迁移删除 |
| ANI `make test` | `pass` | architecture、Auth/Authz、Go 与 Python 聚合通过 |
| ANI `make validate-architecture` | `pass` | component import 与 legacy inference guard 通过 |
| ANI `make validate-doc-entrypoints` | `pass` | validator 与 5 tests 通过 |
| ANI `make validate-services` | `pass` | Services contract/route/spec、SDK、docs idempotence、model/inference Go tests 与 architecture 全部通过；一次环境性 `PATH` 漏 Node 后使用实际 Node 路径完整重跑通过 |
| API docs generation / contract / idempotence | `pass` | `docs/api/core.html` 与 `docs/api/index.html` 从唯一 OpenAPI source 生成；二次生成 clean |
| Direct P2 safe-merge architecture test | `pass` | 3 tests；确认 port/adapter/public generated chain、旧私有 Go package 消失及禁止 import 不存在 |
| target authorization 429 test-first | `pass` | RED 固定 429 缺 `Retry-After`/message；共享 metadata 校验后 focused、full、race 与聚合回归 GREEN |
| `git diff --check`（tracked + cached） | `pass` | 无 whitespace error |
| 新 IAM Token 在其他旧 Auth 路由的兼容性 | `not_verified` | 本候选不把 tracer 单测外推为全 Gateway 兼容证据 |
| cluster rollout / production traffic / BOSS caller E2E | `not_verified` | 本 Goal 明确不部署、不切流，Console 证据不替代 BOSS |

## 当前检查点与恢复

2026-09-06 人工已接受所请求的全部 Allowed paths 扩展，包括 authz generator/tests、Target IAM port/adapter、公共 IAM Go 生成物和静态 API docs；并授权删除且仅删除 `repo/frontends/boss/.cache/core-openapi.normalized.yaml`。该缓存已删除，可由 schema generator 恢复；未删除其他 frontend cache。

检查点前恢复入口仅限本 integration worktree：

```bash
git merge --abort
```

Standards/Spec 双轴评审最终均为 0 findings。2026-09-06 人工明确接受 Merge-Ready；随后显式暂存 80 个允许路径，完整 staged diff 已审查，Gateway full/vet/race、ANI 聚合、Services、architecture、文档、契约、生成幂等和 drift 门禁全部重跑为 `pass`。本记录随本地 merge commit 落地，精确 commit SHA 以 Git history 与最终报告为准。

本地 merge commit 创建后的恢复必须针对该精确 merge commit 创建新的 revert commit；不得 reset、stash、rebase、amend 或 force。
