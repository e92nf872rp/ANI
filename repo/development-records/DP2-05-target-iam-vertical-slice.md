# DP2-05 — Target IAM vertical slice

记录日期：2026-09-06

状态：工程实现与隔离验证 `pass`；Go/No-Go A 人工结论 `pass`（Go，2026-09-06）

固定起点：

- ani-iam：`e7ac8556196b2d0884a5686e18fe2473e68544c0`
- ANI 专用 worktree：`573d3735934f74f9f1eb78818cddefefd9f575eb`
- ANI 固定来源：`0cedae825a489d936cf41815dc27f278f6d3213c`

本记录描述专用本地 worktree 的 DP2-05 隔离 tracer bullet。Go/No-Go A 已由人工明确接受为 Go，本记录与 Gateway diff 由同一本地 commit 承载；它未进入 ANI 主线，未部署或切流。

## 纵向结果

Console/Tenant 最小链路已在真实独立 PostgreSQL、真实独立 Redis、受限 runtime role、真实 IAM 进程和独立 Gateway 进程之间跑通：

```text
Password Login
  -> Session / Session Grant / Refresh state / Security Audit
  -> EdDSA Access Token
  -> CheckPermission(listInstances)
  -> Gateway exactly one IAM decision
  -> GET /api/v1/instances
```

Gateway 使用 DP2-02 固定的 `passwordLogin` Public operation 与 `listInstances` Authorized operation。Public route 调用 IAM authorization 0 次；Authorized route 只调用一次 `CheckPermission`，不先调用 ValidatePrincipal。所有客户端 `x-ani-*` 先删除，allow 后仅注入可信 Principal、Tenant、Session/Grant、authn method 和 decision context。

## 安全与失败边界

| 验证项 | 结果 | 证据摘要 |
|---|---|---|
| Password 成功、无效 Credential | `pass` | 真实 Argon2id + PostgreSQL/Redis；稳定 200/401 |
| Session/Grant/Access Token | `pass` | 状态与 Audit 同事务；EdDSA `kid`、issuer/audience/expiry/Grant version 校验 |
| CheckPermission allow/deny | `pass` | `listInstances` 有权限 200；第二真实 Principal 无权限 403 |
| policy revision mismatch | `pass` | fail closed，稳定 503 `AUTHZ_POLICY_MISMATCH` |
| Gateway 401/403/503/504 | `pass` | 真实进程 401/403；关闭端口 503；接受 TCP 但不完成 TLS/gRPC 的 peer 触发固定 500 ms deadline 和 504 |
| hostile `x-ani-*` | `pass` | 调用 IAM 前删除，allow 后只注入生成 allowlist |
| PostgreSQL/Redis failure | `pass` | 真实容器故障门禁；无进程内 Redis fallback |
| two-Tenant/query mutation | `pass` | Tenant B 不可消费 Tenant A 状态；删 Tenant predicate 的 mutant 泄漏并被负向门禁捕获 |
| mutation + Security Audit | `pass` | 同事务 commit/rollback；Audit 失败导致 mutation 回滚 |
| empty migration/seed replay | `pass` | Atlas versioned migrations + restricted role fixture 从空库重放 |
| Console/Tenant Password Login | `pass` | tracer bullet 使用 `audience=console` 与非空 Tenant boundary |
| BOSS/Platform Password Login | `not_verified` | DP2-05 不实现 Platform Membership/Role persistence，且未运行 BOSS caller 或 Platform-boundary E2E；IAM 单元门禁证明错误的 BOSS/Tenant 组合在 use case 前返回 400，契约有效的 BOSS/Platform 组合在 use case 前以 `IAM_UNAVAILABLE` / `platform_authentication` 稳定返回 503；Console 结果不替代 BOSS |
| unified 24h Idempotency Ledger | `not_verified` | 本批仅验证必需 Idempotency-Key；统一 ledger 按已接受 ticket graph 属 DP2-16，不在 DP2-05 提前实现 |

没有 legacy fallback、默认 allow、RLS、owner/superuser/BYPASSRLS 业务查询、旧 `auth.v1.AuthService` registration 或多次 IAM authorization decision。

## Workload identity 与配置

- Gateway 到 IAM 使用 TLS 1.3 mutual TLS，独立 CA、Gateway client certificate/key 和 IAM server name。
- Gateway composition root 要求显式 `IAM_TARGET_MODE=dp2_05` 才进入目标链路；`disabled` 是唯一 opt-out，缺失、未知或不完整的 target 配置均启动失败，不会静默回落 legacy。
- IAM 精确验证 Gateway client DNS SAN `ani-gateway`。
- client identity 仅允许 health、PasswordLogin 和 CheckPermission 三个 RPC；其余 RPC fail closed。
- `cmd/server` 是唯一 composition root，使用显式构造函数；没有 Wire。
- 配置来自 typed config；committed YAML 不含数据库密码、私钥或证书内容。

## 固定依赖与 Artifact

```text
PostgreSQL: postgres:16.4-alpine@sha256:5660c2cbfea50c7a9127d17dc4e48543eedd3d7a41a595a2dfa572471e37e64c
Redis:      redis:7.4-alpine@sha256:ff02b58f971e7d7d156a1267e283fcbbeee91773b6aa36c49dac28ecfe28eadf

OpenAPI SHA-256:          2466982a7e8f904c6bb6f7790588359c6faf9b230a39a0e28939fcbedc72d0e5
operation registry:       319bd3746098b79d29da18141872b97263c1d899da0306654eda9fad736c2ad2
generated registry:       b28b90fef06dc013c61c4d339574262e27505de5d5a207a2d9fd59b6ed7f0bdc
policy revision:          sha256:f222e2c6d3cd6442449cd722389d3d4fbfcdc7a0fee950c9d28385d3c264affa
IAM descriptor:           df863beb3b095d1f01350c5334d80daf10cdf48083ce0e5663781171aa99a001
Core descriptor:          7dd40f9053b7c1c0c8905decab0f81b07173d0b25651113147bde9a5370d352a
cross-project pins:       33376182b2bcd2f0dd7c84bdf9790d492b6a643560a169e80c0fe63e9113c3b9
CycloneDX SBOM:           3b764a875600d0878b5467ea2ff52fdf74e7e3e6d7b2f5ebaee20873992f2ed4
```

Dependencies introduced by the tracer bullet are pinned to `argon2id v1.0.0`, `jwx/v3 v3.2.0` and `go-redis/v9 v9.22.0`. CycloneDX reports 94 dependency components and no missing dependency license. `govulncheck v1.7.0` reports zero affected vulnerabilities; the unused `x/crypto/openpgp` module advisory remains recorded.

## Executed gates

```text
# ani-iam
go test ./... -count=1
go vet ./...
go test -race -p=1 ./internal/biz ./internal/data ./internal/service ./internal/server ./cmd/server ./tests/cp0 -count=1
go test -tags=integration ./tests/integration -count=1 -v
go mod verify

# ANI Gateway module
go test ./... -count=1
go vet ./...
go test -race -p=1 . ./internal/authz ./internal/targetiam ./internal/middleware ./internal/router -count=1
python3 tools/dp2_operation_registry_test.py
python3 tools/dp2_openapi_breaking_test.py
go mod verify
```

这些已执行门禁为 `pass`。ANI `make validate-architecture`、`make validate-doc-entrypoints`、显式 `make test-go` 与 `make test-python` 也为 `pass`。沙箱内第一次 `make test-go` 因既有 httptest 无权绑定 loopback 返回 `fail`，允许本机 loopback 后同一命令为 `pass`。

最终 audience/boundary fail-closed 修复后再次使用当前 staged source 完整串行复验：IAM 与 Gateway unit/vet/race 均为 `pass`，真实 PostgreSQL/Redis/Gateway integration 全套在 61.187s 内 `pass`；最终 `make validate-architecture`、`make validate-doc-entrypoints`、`make test-go`、`make test-python` 仍全部 `pass`。固定 CycloneDX SBOM 离线重生 hash 不变，固定 govulncheck 扫描 31 个 root packages、84 个 modules，affected vulnerabilities 为 0。

聚合 `make test` 保持 `fail`：旧 Auth gate 仍要求 `logout` / `revokeAPIKey`，与已人工接受的 DP2-02 `logoutSession` / `revokeIAMAPIKey` 不同；旧 Gateway authz generator 仍要求 `action` / `boundary` / `principal_kinds` 字段。DP2-02 replacement registry/breaking gates 为 20/20 与 4/4 `pass`，本批不回退冻结目标契约。Standards/Spec 双评审各报告 4 项（最严重分别为 High/P1），实现阻断项均已修复且 post-review 回归为 `pass`；最终 staged-path audit 对 IAM 60 条和 ANI 24 条显式 cached path 的结果为 `pass`。Go/No-Go A 已由人工明确接受为 Go，结果为 `pass`。

## 已知边界与恢复

- Argon2 latency/memory benchmark、CheckPermission load、Fuzz、HA、生产密钥轮换、备份恢复、生产部署和集群调用方切换均为 `not_verified`。
- 本批不实现 OIDC、完整 Refresh/browser、Invitation、完整 Role、API Key、Service Token、Core/NATS 或五类调用方切换。
- Go/No-Go A 已获人工接受；本地 commit 后的恢复方式是对精确 DP2-05 commit 创建新 revert commit，不 reset/stash。
- 未 push、未发布远端 Artifact、未部署、未切流、未重建数据、未失效 Credential、未删除旧部署资产、未启动 DP2-06。
