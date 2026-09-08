# DP2-03 — IAM/Core integration contract freeze

完成日期：2026-09-04

状态：`pass`（契约、生成和 producer-consumer fixtures）；运行时集成：`not_verified`

兼容性影响：`MINOR`。本批新增独立 `iam.v1` 与 `tenant.integration.v1` Proto package/RPC，针对各自固定基线为向后兼容加法；未来替换旧 `auth.v1` 是另一个 `MAJOR` 切换，不由本批执行。

固定起点：

- ani-iam：`5ff9f3cfe083b3b911bb076450abbbb967e82a37`
- ANI 专用 worktree：`a221a7b50c2cfdb13f04c13f154338d836a48af3`
- ani-iam 目标契约提交：`1bdc3e3657c233b5a47be706f251a4529ec80b5b`

## 交付内容

- 冻结 `iam.v1.AuthenticationService`（15 RPC）、`iam.v1.AuthorizationService`（1 RPC）和 `iam.v1.IAMAdminService`（53 RPC），合计 3 services / 69 RPC。
- 目标 IAM descriptor 明确不包含旧 `auth.v1.AuthService`，并固定稳定 gRPC status 与 `google.rpc.ErrorInfo` reason/domain/metadata。
- 由 Core 拥有 `tenant.integration.v1` Lifecycle、Heartbeat、Bootstrap、Snapshot 和 error contract；只读 `TenantIAMIntegrationService` 仅提供 `BeginTenantLifecycleSnapshot` 与 `ListTenantLifecycleSnapshotPage`，不包含 Lifecycle writer。
- 固定 Lifecycle monotonic version、Bootstrap payload fingerprint、Snapshot cursor/page/version、schema major 与 v1 additive evolution 规则。
- 两仓库各自保存对方 descriptor、相同 `contract_pins.json` 和八组逐字节一致 fixtures；双方独立构建，不共享内部 Go package、数据库或跨数据库事务。

## Breaking 结论

新 `iam.v1` 和 `tenant.integration.v1` package 相对两个固定起点均为 additive，完整 Buf breaking 结果为 `pass`，没有删除或改名既有目标 symbol。

它们是旧 `auth.v1.AuthService` 的未来 replacement contract，而不是旧 14-RPC surface 的兼容扩展。未来切换时，旧客户端必须重新生成并显式映射；本批不切换 Gateway、不删除旧 Auth、不发布远端 Artifact，也不启动 Core Publisher、Snapshot Server、IAM Consumer 或 NATS 基础设施。

## 固定 Artifact

| Artifact | SHA-256 |
|---|---|
| IAM descriptor | `df863beb3b095d1f01350c5334d80daf10cdf48083ce0e5663781171aa99a001` |
| Core descriptor | `7dd40f9053b7c1c0c8905decab0f81b07173d0b25651113147bde9a5370d352a` |
| 两仓库 `contract_pins.json` | `33376182b2bcd2f0dd7c84bdf9790d492b6a643560a169e80c0fe63e9113c3b9` |

生成工具固定为 Buf `1.72.0`、`protoc-gen-go v1.36.12` 和 `protoc-gen-go-grpc 1.6.2`；版本、可执行文件摘要、Google APIs module identity、源文件与 fixture 摘要由两仓库的 `contract_pins.json` 和 ani-iam DP2-03 evidence 固定。

## 验证

| 结果 | 门禁 |
|---|---|
| `pass` | IAM/Core Buf lint；IAM 空 target baseline 与完整 ANI Proto module breaking |
| `pass` | 两仓库固定命令重复生成，生成 Go 与 descriptor 逐字节无漂移 |
| `pass` | 两仓库独立 descriptor inventory、fixture decode、ErrorInfo、fingerprint 和 immutable pin tests |
| `pass` | ani-iam `go test ./... -count=1` 与 `go vet ./...` |
| `pass` | ANI generated/Gateway/Auth 相关 Go tests、vet 与 `make validate-architecture` |
| `fail` | ANI 聚合 `make test` 仍停在旧 DP2-02 Auth operationId gate：期望 `logout` / `revokeAPIKey`，已接受目标为 `logoutSession` / `revokeIAMAPIKey`；本批不回退目标契约 |
| `not_verified` | IAM 运行时仅注册三个目标服务 |
| `not_verified` | 真实 Core Publisher、Snapshot Server、IAM Consumer、NATS、部署、远端发布和调用方切换 |
| `not_verified` | Gateway public operation 到目标 IAM RPC 的运行时映射 |

一次 ANI 相关 Go 回归使用默认 linker 临时目录时因本机 `/tmp` quota 返回 `fail`；改用 Goal 专属 `/home/chabking/.cache/ani-direct-p2-01-05/dp2-03/{go-cache,go-tmp}` 后，同一命令结果为 `pass`。该环境失败不作为源码通过证据。

## 人工检查点与恢复

2026-09-04 已人工接受精确 DP2-03 Proto/Core contract diff，并批准本 Feature-batch 的四个文档闭环路径。

提交后只允许通过针对精确本地 commit SHA 的新 revert commit 恢复，并按消费者到生产者的逆依赖顺序执行。未授权 reset、stash、amend、force、push、发布、部署、切流、NATS 创建或真实 Core Publisher 启动。
