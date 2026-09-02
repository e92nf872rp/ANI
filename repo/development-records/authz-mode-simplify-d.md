# AUTHZ-MODE-SIMPLIFY-D — Gateway 鉴权契约即开关收敛

> 计划来源：`services/tasks/modules/plan/plan-authz-mode-simplify-contract-switch.md`（第六版定稿，随本批次入库）
> 实施分支：`feat/authz-mode-simplify-contract-switch`（基于 origin/main @ `9c7bf2b`）
> 代码 commit：`4753a42`（初版）、`1d5c20a`（第六版修订）；文档同步 `caafef4`
> 完成日期：2026-08-28；修订日期：2026-08-31

## 实现了什么

落实"契约即开关"：删除 Gateway 鉴权 mode 开关（policy/dev/pilot/off），policy 路由恒为——带 `x-ani-authz`（generated）的 operation 走 V2 新链路，其余走 legacy，public 恒放行；`ANI_AUTH_MODE=dev` 时 generated 自动回落 legacy（dev 环境无 auth-service）。两个废弃 env `GATEWAY_AUTHZ_POLICY_MODE` / `GATEWAY_AUTHZ_PILOT_OPERATIONS` 直接删除、不设残留检测（新集群从头部署拍板，第六版修订）。存量 generated 接口（`listQuotaMeta` / `getPlatformMeteringUsage`）无需任何 pilot env 即常驻 V2，切流审批语义由 CODEOWNERS 共同审查 + drift 门禁承接，回滚退化为镜像回退/代码 revert。

## 关键文件改动

| 文件 | 修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/authz/mode.go` | 重写 | 收敛为 `Config{AuthMode}` + `ConfigFromEnv` 废弃 env 残留检测（fail closed）+ `EffectiveSource`（public 恒放行、dev 回落 legacy、其余按契约直通）；删除 `Mode` 枚举 / pilot allowlist / `Validate(registry)` |
| `services/ani-gateway/internal/middleware/chain.go` | 修改 | 删除 `cfg.Validate(registry)` 调用 |
| `services/ani-gateway/internal/middleware/auth.go` | 修改 | 兼容入口 `Config{Mode: authz.ModeOff}` → `Config{}` |
| `services/ani-gateway/internal/middleware/rbac.go` | 修改 | 同上 |
| `services/ani-gateway/internal/authz/mode_test.go` | 重写 | ConfigFromEnv 残留检测负例 + EffectiveSource 三态（public 放行 / dev 回落 / generated 直通） |
| `services/ani-gateway/internal/authz/principal_test.go` | 修改 | 移除 Mode 相关断言，改用 `Config{}` |
| `services/ani-gateway/internal/middleware/pilot_test.go` → `contract_switch_test.go` | 重命名+重写 | pilot 试点语义改为契约即开关常驻语义：不配任何 env 时 generated 走 V2、dev 回落 legacy、legacy 不回归 |
| `services/ani-gateway/internal/middleware/policy_test.go` | 修改 | 按 `EffectiveSource` 新语义改写 |
| `services/ani-gateway/internal/middleware/ratelimit_test.go` | 修改 | `Config{}` 字段适配 |
| `deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml` | 修改 | 删除 `GATEWAY_AUTHZ_POLICY_MODE: "pilot"` 与 `GATEWAY_AUTHZ_PILOT_OPERATIONS: "listQuotaMeta"` 两个废弃 env 条目（与代码同分支落地，否则存量部署升级即启动失败） |

10 files changed, +120/−317（含 rename）。生成物 `zz_generated_core_policies.go` 零漂移，`repo/api/openapi/v1.yaml` 无契约变更。

## 明确不动（按方案 §4.5）

生成器与门禁脚本（`generate_gateway_authz.py` 等）、`zz_generated_core_policies.go`（生成物）、auth-service、V2 认证/授权 middleware（`generated_authz.go` 等）、`Registry.LookupOperation`（保留）——均未改动。

## 验证

- `go test ./services/ani-gateway/...` PASS
- `make gen-gateway-authz` 生成物零漂移
- `make validate-gateway-authz` PASS（18 tests、no drift、283 registered routes 0 errors）
- `make test` 除 `pkg/adapters/runtime` 的 Windows 预存失败（sandbox symlink 特权 / Python `os.O_DIRECTORY`；已在 origin/main @ `9c7bf2b` worktree 复跑同包同样 FAIL，且该目录不在本次改动集）外全部 PASS，与 ANI-06 L151 历史记录一致
- `make validate-architecture` PASS
- `git diff --check` PASS

## 本地实测

`ANI_AUTH_MODE=auth_service`（不带任何 policy env）启动 auth-service + ani-gateway：public 放行、generated 接口（quota-meta / metering usage）走 V2 返回 200、无 token/越权 401/403、legacy 接口不回归；`ANI_AUTH_MODE=dev` 回落验证通过。详见 `repo/CURRENT-SPRINT.md` 对应条目。

---

## 修订（2026-08-31，方案评审至第六版定稿）

> 以下记录取代本文前文与第六版冲突的表述：废弃 env 残留检测已删除，`mode.go`/`mode_test.go` 已改名，兼容入口已删除。前文保留作为第三版实施的历史脉络。

### 拍板变化（第五/六版）

1. **删除废弃 env 残留检测与 fail-closed 语义**：新集群从头部署，`GATEWAY_AUTHZ_POLICY_MODE` / `GATEWAY_AUTHZ_PILOT_OPERATIONS` 不设残留检测、无启动校验；deploy 清单删除两个废弃 env 条目维持不变。
2. **`mode.go` → `config.go`、`mode_test.go` → `config_test.go`**（git mv）：文件名与内容对齐——承载的是 `Config`/`ConfigFromEnv`/`EffectiveSource`，不再有 `Mode`。
3. **删除兼容入口 6 函数**：`auth.go` 删 `Auth` / `AuthWithClient` / `AuthWithResolvedPolicy`，`rbac.go` 删 `RBAC` / `RBACWithClient` / `RBACWithResolvedPolicy`；仅保留 legacy 分支内部函数 `authenticateLegacy` / `authorizeLegacy` / `legacyViewFromContext`。业务方一律走 `registerChain` 主链。
4. **测试覆盖归一**：`EffectiveSource` 矩阵与 `ANI_AUTH_MODE` 解析断言唯一归属 `config_test.go`；`principal_test.go` 删 3 条重复用例。
5. **`mode=off` 表述清理**：`auth_client.go`、`contract_switch_test.go` 等注释改为 legacy 链路表述。

### 修订改动明细

| 文件 | 修改 | 说明 |
|---|---|---|
| `internal/authz/mode.go` → `config.go` | git mv + 重写 | 删废弃 env 残留检测；`ConfigFromEnv` 只解析 `ANI_AUTH_MODE`（trim+lower，无 error）；`EffectiveSource` 保留（public 恒放行、dev 回落 legacy、其余按契约直通） |
| `internal/authz/mode_test.go` → `config_test.go` | git mv + 重写 | `TestEffectiveSourceContractSwitchMatrix`（public/generated/legacy × auth_service/dev 全矩阵）+ `TestConfigFromEnvParsesAuthMode`（空串、`" DEV "`→`"dev"`）；删残留检测负例 |
| `internal/authz/principal_test.go` | 修改 | 删 `TestEffectiveSourcePublicAlwaysPublic` / `TestGeneratedUsesGeneratedDirectly` / `TestConfigFromEnvParsesAuthMode` 3 条，归一至 config_test.go |
| `internal/middleware/chain.go` | 修改 | `Register` 简化：`registerChain(h, store, NewAuthClientFromEnv(), registry, authz.ConfigFromEnv())`，无启动校验 |
| `internal/middleware/auth.go` | 修改 | 删 `Auth` / `AuthWithClient` / `AuthWithResolvedPolicy`；保留 `authenticateLegacy`（历史来源注释不变）、`legacyViewFromContext` |
| `internal/middleware/rbac.go` | 修改 | 删 `RBAC` / `RBACWithClient` / `RBACWithResolvedPolicy`；保留 `authorizeLegacy`；删悬空 authz import |
| `internal/middleware/policy.go` | 修改 | 函数零改动；仅注释更新（"供测试或主链之外的组合场景复用"） |
| `internal/middleware/policy_test.go` | 修改 | 删 `TestRegisterFailsClosedOnRemovedAuthzEnv`；`TestRegisterSucceedsWithAuthServiceMode` 仅 `t.Setenv("ANI_AUTH_MODE", "auth_service")`；`TestAuthenticatePrincipalRejectsMissingPolicy` 更名并直连 `AuthenticatePrincipal`；ratelimit 用例迁主链装配 |
| `internal/middleware/sandbox_auth_test.go` | 修改 | 迁主链装配：`ResolveAuthzPolicy(authz.CoreRegistry(), authz.Config{})` + `AuthenticatePrincipal` + `AuthorizePrincipal` |
| `internal/middleware/service_token_test.go` | 修改 | 两处迁主链装配；`mode=off` 表述改"legacy 链路（含拒绝分支）不得触发 V2 RPC" |
| `internal/middleware/auth_client.go` | 修改 | 注释"mode=off 下不被调用"→"legacy 链路不经过此处" |
| `internal/middleware/contract_switch_test.go` | 修改 | 注释"mode=off 回滚验证用"→"legacy 链路验证用" |

12 files changed, +51/−222（净删 171 行，含 2 个 rename）。生成物 `zz_generated_core_policies.go` 零漂移，`repo/api/openapi/v1.yaml` 无契约变更。

### 修订后验证

- `go test ./services/ani-gateway/...` PASS
- `make gen-gateway-authz` 生成物零漂移
- `make validate-gateway-authz` PASS（18 tests、no drift、283 registered routes 0 errors）
- `make validate-architecture` PASS
- `git diff --check` PASS
- 代码与方案第六版 §4.1–§4.5 逐项复核一致，无出入
