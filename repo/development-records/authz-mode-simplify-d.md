# AUTHZ-MODE-SIMPLIFY-D — Gateway 鉴权契约即开关收敛

> 计划来源：`kjs-study/平台鉴权任务/plan-authz-mode-simplify-contract-switch.md`（第三版；该文档位于本地 kjs-study 目录，不入库）
> 实施分支：`feat/authz-mode-simplify-contract-switch`（基于 origin/main @ `9c7bf2b`）
> 代码 commit：`4753a42`
> 完成日期：2026-08-28

## 实现了什么

落实"契约即开关"：删除 Gateway 鉴权 mode 开关（policy/dev/pilot/off），policy 路由恒为——带 `x-ani-authz`（generated）的 operation 走 V2 新链路，其余走 legacy，public 恒放行；`ANI_AUTH_MODE=dev` 时 generated 自动回落 legacy（dev 环境无 auth-service）。废弃 env `GATEWAY_AUTHZ_POLICY_MODE` / `GATEWAY_AUTHZ_PILOT_OPERATIONS` 残留时启动 fail closed（监听前报错）。存量 generated 接口（`listQuotaMeta` / `getPlatformMeteringUsage`）无需任何 pilot env 即常驻 V2，切流审批语义由 CODEOWNERS 共同审查 + drift 门禁承接，回滚退化为镜像回退/代码 revert。

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
