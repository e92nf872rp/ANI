# ANI-IAM-PRE930-CONTAINMENT

日期：2026-09-08

状态：已吸收用户接受的 Observability main，GitHub PR exact-SHA 复验待执行

## 目的

ANI 正在为 2026-09-30 的 v1.0.0 补齐功能。为避免 ani-iam Direct P2 重构改变该日期前的活动版本，本批在最新已接受 ANI 基线上外科式移除 PR #145 提前合入的目标 IAM 契约和运行时接线，同时保留其后的全部 ANI 功能。

这不是撤销 Direct P2 设计。目标 IAM 的领域模型、契约候选和 Go/No-Go A 证据继续由 ani-iam 保存；ANI 只恢复到当前 Auth 发布轨道，待 9 月 30 日功能冻结并人工接受不可变 release SHA 后再进行一次 rebaseline。

## 固定基线

- 初始 ANI base：`caa2a5e72fad98215a5ea26696e453e5c2ef6523`
- 用户接受的 reconciliation base：`804db51a5f93605f9bbd4ac407f0489ecb1d187c`
- reconciliation base tree：`28eb0508c93aab28e86e044a88ada9e19bf16830`
- PR #145 merge：`50f7b422707c2ab78462bd9bb8186bae018a14fe`
- PR #145 parent：`e895af8cdfd5431804b64e1f571b3c6803278cc5`
- 必须保留的后续提交：`9bfedfd`、`98b881d`、`caa2a5e`、`804db51`

## 恢复内容

- 恢复现行 Auth/API Key/Refresh/Logout Core v1 operationId、request/response 与旧 Gateway handler 语义。
- 移除 Target IAM client、port、middleware、router 分流、composition-root wiring、目标 registry、目标 Proto copy 和 production-shaped `IAM_TARGET_MODE` 配置。
- 删除仅用于提前 Direct P2 合入的 replacement/breaking artifacts 与 ANI 内 DP2 批次记录；历史仍可通过 Git 查阅，ani-iam 保留权威候选证据。
- 从恢复后的 Core OpenAPI 重新生成四语言 Core SDK、静态 API 文档和 Gateway Core policy，保留后续 Tenant API。
- 新增 required Actions job，显式执行 `make validate-core-api-compatibility` 和 `make validate-gateway-authz`。

## 保留边界

- PR #149 的 VM status 修复保持不变。
- PR #144 的 Tenant API、handler、migration、SDK 与 policy 保持在当前契约中。
- PR #150 的 KB API 与实现保持不变。
- PR #148 的平台组件状态/诊断 OpenAPI、ports/adapters、Gateway handlers 和 current Core policy 保持不变。
- 不为新增 Tenant operations 提前决定未来 Direct P2 handler/owner/classification；目标 registry 在冻结后由候选轨道重新生成。

## 验证边界

按用户要求不运行本地测试门禁。本地只运行生成器、冲突/路径审计和 `git diff --check`。以下结果必须以 PR exact SHA 的 GitHub Actions 为准：

- Core Compatibility / Gateway Authz；
- Go Build & Test；
- Python AI Services；
- Services Boundary / API / Docs Gate；
- OpenAPI Spec Lint；
- Required PR Gates。

在 Actions 完成前，上述结果均为 `not_verified`。本批未执行部署、集群访问、数据修改、Credential 失效、切流或 PR merge。

## 恢复

PR 合并前不影响 `main`。若候选有问题，保留 PR 未合并即可；删除远端分支或在合并后恢复均需针对精确目标另行确认。
