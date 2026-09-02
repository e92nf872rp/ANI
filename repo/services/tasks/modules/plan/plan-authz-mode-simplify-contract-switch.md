# 鉴权模式简化方案：删除 mode 开关，契约即开关（恒 full）

> 状态：已评审定稿（第五版 2026-08-31），待实施
> 关联文档：`plan-authz-policy-compat-contract-pilot-v4.md`（原方案，含决策 #12）、`HOW-TO-新增接口与权限字段配置.md`（操作速查，需同步修订）
> 编制日期：2026-08-28
> 修订：2026-08-28 第二版——在"去掉 pilot"基础上进一步**删除 `GATEWAY_AUTHZ_POLICY_MODE` 开关本身**，policy 路由恒为契约即开关，仅保留 `ANI_AUTH_MODE` 的 dev 例外
> 修订：2026-08-28 第三版——全仓复查补漏：① `auth.go`/`rbac.go` 兼容入口两处 `Config{Mode: ModeOff}` 编译失败点（§4.3）；② 存量部署 `sprint13-production-shaped-gateway-deployment.yaml` 携带废弃 env，必须同 PR 清理否则启动失败（§5）；③ 测试改写清单补全 `ratelimit_test.go`、`policy_test.go` 装配点、`pilot_test.go` cfg 构造点（§4.4）；④ `Registry.LookupOperation` 去留说明（§4.5）
> 修订：2026-08-31 第四版——评审拍板：§4.3 兼容入口从"改 `Config{}` 保留"改为**整体删除**。经全仓核实 `Auth`/`AuthWithClient`/`RBAC`/`RBACWithClient` 生产调用面为零（连带删除仅被其引用的 `AuthWithResolvedPolicy`/`RBACWithResolvedPolicy`，共 6 个函数），测试装配迁主链方式，`authenticateLegacy`/`authorizeLegacy` 保留（主链复用）
> 修订：2026-08-31 第五版——评审拍板两项：① 部署走新集群从头部署，存量残留场景不存在，**删除两个废弃 env 的残留检测**，`ConfigFromEnv` 收敛为纯 `ANI_AUTH_MODE` 解析（去掉 error 签名），接受"本地按旧文档带旧 env 启动被静默忽略"（dev 本就全 legacy，无行为影响）；② `mode.go` 名不副实，**改名 `config.go`**（`git mv` 保 rename 历史），`mode_test.go` 同步改名 `config_test.go`。`ANI_AUTH_MODE` 为唯一保留 env（dev 旁路载体），不删除
> 修订：2026-08-31 第六版——一致性扫描修复：状态行改"定稿待实施"；§4.4 测试覆盖归一（`EffectiveSource` 矩阵与 `ANI_AUTH_MODE` 解析断言唯一归属 `config_test.go`，principal_test 对应三条用例从"改写"改为"删除"）；§4.5 补改名标注

---

## 1. 背景与动机

当前新增 Core API 接口要接入 V2 鉴权链路，除了改 `v1.yaml` 加 `x-ani-authz` 并重新生成注册表，还必须**联动改两处**：

1. 代码：`services/ani-gateway/internal/authz/mode.go` 的 `functionalMVPPilotOperations` 严格集合（PR review）；
2. 部署：环境变量 `GATEWAY_AUTHZ_PILOT_OPERATIONS`（必须与代码集合逐项相等）。

这套 pilot allowlist 是迁移期脚手架，目的是"扩大 V2 试点范围必须走 PR 审批"。但代价是每加一个 pilot 接口都要代码 + env 双改，且严格集合校验让两者必须同步漂移，配置成本随接口数累积。

**事实核对（2026-08-28）**：当前 registry 中 generated operation 恰好只有 `listQuotaMeta`、`getPlatformMeteringUsage` 两个（`zz_generated_core_policies.go` 中 `PolicySourceGenerated` 仅 2 处），与 pilot allowlist 完全一致，且均已在 pilot 阶段端到端验证。**本方案合入部署后，运行时行为零变化**。

## 2. 方案概述：契约即开关，删除 mode 开关

- **有 `x-ani-authz`（generated）→ 走 V2 新链路；没有（legacy）→ 走旧 middleware；public → 放行。**
- 判定完全由路由注册表（`registry.Lookup`，`middleware/policy.go#L73`）的 `Policy.Source` 决定，per-operation 自动新旧共存。
- **删除 pilot 模式、`off`/`full` 模式开关、`GATEWAY_AUTHZ_POLICY_MODE` 与 `GATEWAY_AUTHZ_PILOT_OPERATIONS` 两个环境变量、代码内严格集合**。不再存在"切流动作"——带扩展即 V2，部署即生效。
- **唯一保留的例外：`ANI_AUTH_MODE=dev`**。dev 环境没有 auth-service，generated operation 自动回落 legacy（等价于原 `dev + off` 行为），保住本地开发旁路；由"原 dev+pilot/full 启动失败"改为"自动回落"，dev 不再需要配置任何 policy 相关变量。
- 两个废弃 env 直接删除、**不设残留检测**（第五版拍板：部署走新集群从头部署，存量 Deployment 携带旧 env 的场景不存在，检测永远不会触发属死防御）；仓库部署清单与本地文档随 PR 同步清理兜底。

原 pilot / mode 开关承载的职责转移为：

| 被删除的机制 | 替代机制 |
|---|---|
| pilot 代码严格集合（PR 审批切流范围） | v1.yaml 变更本身走 CODEOWNERS 共同审查（改契约的 PR 天然被审） |
| 启动校验"pilot operation 必须 generated" | 既有门禁：新增 route 必须加 `x-ani-authz`、`make validate-gateway-authz` 四项校验不变 |
| 运行时逐接口验证后进 allowlist | 合入前一次性盘点（见 §6）；后续每个新接口在合入 PR 中补 V2 契约测试 |
| `off` 运行时逃生舱 | **删除且暂不替代**（决策见 §8 风险表首行）；V2 故障回滚手段变为回滚镜像/revert |

### 2.1 合入后的使用常态（结论）

1. **新旧共存是自动的**：带 `x-ani-authz` 的接口走 V2 新链路，不带 `x-ani-authz` 的存量接口继续走旧 Auth/RBAC middleware，互不干扰；每个接口由契约自行决定，无任何配置。
2. **合入即终态**：不存在"切换 full"这个动作，部署本方案后行为恒为契约即开关；当前 generated 集合与已验证集合一致（见 §1 事实核对），行为零变化。
3. **"不带扩展也可以存在"仅适用存量接口**：新增 route 仍必须带 `x-ani-authz`（冻结决策 #8 门禁，不带拒绝合并）。本方案简化的是接入成本，不是取消该门禁。
4. **新增接口的完整清单（对比现在）**：

```bash
# 1. v1.yaml：加 path + method + operationId + x-ani-authz
# 2. 重新生成注册表 + 跑门禁
cd repo
make gen-gateway-authz
make validate-gateway-authz
# 3. 补该接口的 V2 契约测试（替代原 pilot 审批的纪律）
make test
```

对比现状少了两步联动：不再改 `functionalMVPPilotOperations`，不再配置任何 policy env；同时消失的坑：忘改集合 / 集合不同步 / mode 配错导致的启动失败。

## 3. 行为语义对照（改前 → 改后）

| 配置（改前） | 改前行为 | 改后行为 |
|---|---|---|
| `mode=off` | 非 public 全走 legacy | **删除**；等价场景仅剩 `ANI_AUTH_MODE=dev`（自动全 legacy） |
| `mode=pilot` + allowlist | generated 且在 allowlist → V2，否则 legacy | **删除** |
| `mode=full` | generated → V2，其余 legacy | **成为唯一行为**（无需配置） |
| `ANI_AUTH_MODE=dev` + pilot/full | 启动失败 | **改为自动全 legacy**（与原 dev+off 等价），不再启动失败 |
| `ANI_AUTH_MODE=auth_service` | 正常 V2 | 不变（唯一生产取值） |

per-operation 路由结果（改后）：

```text
policy.Source == public     → public（放行）
AuthMode == dev             → 一律 legacy（dev 无 auth-service）
policy.Source == legacy     → legacy middleware
policy.Source == generated  → V2 链路
```

### 3.1 请求链路识别逻辑（现状已实现，本方案不改 policy.go）

判定在请求路径上只有两行，就在 [middleware/policy.go#L73-L77](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/policy.go#L73-L77)：

```go
policy, ok := registry.Lookup(string(c.Method()), normalized)   // 查注册表
...
return ResolvedPolicy{Policy: policy, Source: cfg.EffectiveSource(policy)}, nil
```

`policy.Source` 在 `make gen-gateway-authz` 时已按 `v1.yaml` 中 `x-ani-authz` 的有无固化进注册表。原 full 分支就是 `return policy.Source`——查表结果即最终结果；本方案只是把 `EffectiveSource` 里 off/pilot 分支删掉，剩下 public 与 dev 两个特判（见 §4.1）。`middleware/policy.go` **零改动**（`EffectiveSource` 方法签名不变）。

## 4. 具体代码改动

### 4.1 `services/ani-gateway/internal/authz/config.go`（核心；原 `mode.go` 经 `git mv` 改名）

**删除：**

| 位置（现状） | 内容 |
|---|---|
| L10-L17 | `Mode` 类型与 `ModeOff` / `ModePilot` / `ModeFull` 常量 |
| L20-L24 | `Config.Mode`、`Config.PilotOperations` 字段（仅保留 `AuthMode`） |
| L26-L50 | `ConfigFromEnv` 中 mode 解析与 `GATEWAY_AUTHZ_PILOT_OPERATIONS` 解析，收敛为纯 `ANI_AUTH_MODE` 解析（第五版：不设废弃 env 残留检测，签名去掉 error） |
| L52-L61 | `ValidateBase`（dev×mode 组合、allowlist 约束均随 mode 消失） |
| L63-L80 | `functionalMVPPilotOperations` + `sameOperationSet` |
| L82-L110 | `Validate(registry Registry)` 整个方法（mode 删除后无存在理由） |
| L112-L132 | `EffectiveSource` 的 mode switch，收敛为 public/dev 两个特判 |

**改为（目标全貌）：**

```go
package authz

import (
	"os"
	"strings"
)

// Config 承载 auth mode。
// policy 路由恒为契约即开关：带 x-ani-authz（generated）的 operation 走 V2，
// 其余走 legacy；dev 环境无 auth-service，generated 同样回落 legacy（保留 dev 旁路）。
type Config struct {
	AuthMode string
}

// ConfigFromEnv 从环境变量解析配置。
func ConfigFromEnv() Config {
	return Config{
		AuthMode: strings.ToLower(strings.TrimSpace(os.Getenv("ANI_AUTH_MODE"))),
	}
}

// EffectiveSource 返回 policy 的有效 source：
// public 恒放行；dev 无 auth-service 一律 legacy；
// 其余按契约直通：generated→V2，legacy→legacy。
func (c Config) EffectiveSource(policy Policy) PolicySource {
	if policy.Source == PolicySourcePublic {
		return PolicySourcePublic
	}
	if c.AuthMode == "dev" {
		return PolicySourceLegacy
	}
	return policy.Source
}
```

**设计说明：**

- **`Config` 仅保留 `AuthMode`**：`EffectiveSource` 方法签名不变，`middleware/policy.go` 调用点零改动；`ConfigFromEnv` 去掉 error 返回（第五版拍板删残留检测后无失败来源），chain.go 调用点同步简化。
- **不设废弃 env 残留检测（第五版拍板）**：部署走新集群从头部署，存量 Deployment 携带旧 env 的场景不存在，检测永远不会触发属死防御。接受残余风险：本地按旧文档/旧脚本带废弃 env 启动会被静默忽略——本地 `ANI_AUTH_MODE=dev` 本就全 legacy，无行为影响，靠 §5 清单与文档同步清理兜底。
- **文件改名 `mode.go` → `config.go`**：mode 概念删除后原名名不副实；收敛后内容是 `Config` 类型 + 构造 + 方法，按 Go 惯例文件名跟核心类型走。`git mv` 保 rename 历史，`mode_test.go` 同步改名 `config_test.go`（§4.4）。
- **dev 从"启动失败"改为"自动回落 legacy"**：原约束"dev 只允许 off"的目的是防止 dev 误切 V2；mode 删除后等价保障改为运行时回落——dev 下 generated 也走 legacy（与原 `dev+off` 行为逐请求一致），本地开发无需任何 policy 配置即可工作，且不可能再出现"dev 忘配 off 启动失败"。
- **不留 `off` 逃生舱**：属有意决策（见 §8）；如未来 generated 数量增多需要运行时 kill-switch，届时再议，不提前设计。

### 4.2 `services/ani-gateway/internal/middleware/chain.go`

`Register`（L14-L29）删除 L23-L26 的 `cfg.Validate(registry)` 调用及注释——mode 删除后无启动校验可做；第五版拍板后 `ConfigFromEnv` 收敛为纯解析、不再返回 error，调用点同步去掉 err 处理。`registry` 继续传给 `registerChain`，不改。

```go
func Register(h *server.Hertz, store GatewayStore) error {
	if store == nil {
		return errors.New("gateway middleware store is required")
	}
	registry := authz.CoreRegistry()
	registerChain(h, store, NewAuthClientFromEnv(), registry, authz.ConfigFromEnv())
	return nil
}
```

`middleware/policy.go` **不动**（`ResolvePolicyForRequest` / `EffectiveSource` 调用点签名不变）。

### 4.3 `internal/middleware/auth.go` 与 `rbac.go`：删除兼容入口（2026-08-31 评审拍板）

第三版原计划把两处 `Config{Mode: authz.ModeOff}` 硬编码改为 `Config{}` 保留入口；评审确认这组入口生产调用面为零，**拍板整体删除**，并连带删除仅被它们引用的两个 `ResolvedPolicy` 壳，避免死代码换位残留。

**删除清单（6 个函数）：**

| 文件 | 函数 | 删除理由 |
|---|---|---|
| `auth.go` | `Auth()`（L23-L25） | 薄封装，全仓无装配点 |
| `auth.go` | `AuthWithClient`（L27-L42） | 兼容入口壳，仅测试装配 |
| `auth.go` | `AuthWithResolvedPolicy`（L44-L64） | 删 `WithClient` 后仅剩 policy_test 一处引用；主链 `AuthenticatePrincipal` 已内联等价分流逻辑 |
| `rbac.go` | `RBAC()`（L18-L20） | 薄封装，全仓无装配点 |
| `rbac.go` | `RBACWithClient`（L22-L37） | 兼容入口壳，仅测试装配 |
| `rbac.go` | `RBACWithResolvedPolicy`（L39-L59） | 删 `WithClient` 后零调用 |

**调用面核实依据：**

- 生产主链 [chain.go#L34-L48](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/chain.go#L34-L48) 的 `registerChain` 走 `ResolveAuthzPolicy + AuthenticatePrincipal + AuthorizePrincipal`，不经过上述任何函数；`Auth()`/`RBAC()` 全仓无 `middleware.Auth()`/`middleware.RBAC()` 装配点。
- `internal` 包边界（`services/ani-gateway/internal/...`）保证引用面只可能在 gateway 内，已全量 grep 核实：除两文件内部互调（`Auth()`→`AuthWithClient`、`RBAC()`→`RBACWithClient`，随删除连带消失）外，仅剩测试装配 5 处（§4.4）。

**明确保留：**

- `authenticateLegacy`（[auth.go#L87](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/auth.go#L87)）/ `authorizeLegacy`（[rbac.go#L62](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/rbac.go#L62)）：主链 legacy 分支直接复用的真实实现（[generated_authz.go#L167](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/generated_authz.go#L167)、[#L247](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/generated_authz.go#L247)），生产 legacy 接口的认证授权全靠它们。
- `middleware/policy.go`、`generated_authz.go` 及其余全部。

**语义与影响面：**

- **生产行为零变化**：主链不经过被删函数。
- **fail-closed 语义不丢**：`AUTHZ_POLICY_MISSING` 错误码来自链上 `ResolveAuthzPolicy`（[policy.go#L94](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/middleware/policy.go#L94)），与被删函数无关；主链对 policy context 缺失本就 503（`AuthenticatePrincipal`/`AuthorizePrincipal` 首行）。
- 原 `ModeOff`/`Config{}` 的过渡语义随之消失，不存在"legacy 入口收到 generated"的场景——能收到 generated 的只剩主链，主链本就正确分流。
- 受影响的只有直接装配被删函数的测试，处置见 §4.4。

### 4.4 测试改写

| 文件 | 改动 |
|---|---|
| `internal/authz/config_test.go`（原 `mode_test.go`，随 §4.1 改名） | 删除全部 mode/allowlist 用例：mode 解析、空 allowlist、额外 operation、拼写错误、dev+pilot、严格集合相等、off/full 携带 allowlist；`frozenSet()` 辅助删除。新增：`EffectiveSource` 矩阵：public→public（auth_service 与 dev 均是）、dev×generated→legacy、auth_service×generated→generated、legacy→legacy；`ConfigFromEnv` 解析断言（不设 → 空串，设 `dev` → `"dev"`）——矩阵与解析断言只在本文件维护，principal_test 对应用例删除（见下，第六版归一）。**第五版：原计划的"废弃 env 任一非空 → 失败"用例随残留检测删除而取消** |
| `internal/authz/principal_test.go` | 第二版行号描述有误，按实际函数改写：`TestModeOffAlwaysUsesLegacy`（L11-L17）删除——"generated 回落 legacy"语义由 config_test.go 的 dev 用例承接；`TestEffectiveSourcePublicAlwaysPublic`（L19-L27）、`TestFullUsesGeneratedDirectly`（L48-L54）、`TestConfigFromEnvDefaultsToOff`（L81-L92）删除——public 特判、generated 直通、`ANI_AUTH_MODE` 解析的覆盖统一由 config_test.go 矩阵与解析断言承接（第六版归一，避免两文件重复断言）；`TestPilotOnlyUsesGeneratedForAllowlistedOperations`（L29-L46）、`TestValidateBaseRejectsDevWithNonOffMode`（L56-L66）、`TestValidateBaseRejectsAllowlistWithoutPilot`（L68-L79）、`TestConfigFromEnvRejectsInvalidMode`（L94-L99）、`TestConfigFromEnvRejectsDevWithPilot`（L101-L108）、`TestConfigFromEnvRejectsAllowlistWithFull`（L110-L117）整体删除。L119 起的 identity key / principal / legacy view 用例不涉及 mode，**不动** |
| `internal/middleware/pilot_test.go` | 除 allowlist env 断言（L241、L266、L440、L454、L470、L561）外，**第二版还遗漏 4 处编译失败点：L247、L280、L476、L575 的 `authz.Config{Mode: ...}` 构造**，统一改为 `authz.Config{}` 或 `authz.Config{AuthMode: "auth_service"}`；`TestQuotaMetaModeOffUsesLegacy`（L425 起）的 ModeOff 概念消失，**改写为"dev 下 generated→legacy"用例**（承接原回滚路径语义）或删除；auth_service 下 generated→V2 认证/授权链路、legacy→旧 middleware 的断言**保留**；新增 dev 回落断言。建议重命名 `contract_switch_test.go`（git 跟踪 rename） |
| `internal/middleware/policy_test.go` | 第二版只列了 L78、L90 的 `t.Setenv("GATEWAY_AUTHZ_PILOT_OPERATIONS", ...)`；**遗漏 3 处：L35-L36（`AuthWithClient`/`RBACWithClient` 链路装配）、L127、L153（`authz.Config{Mode: authz.ModeOff}` 构造，编译失败点）**，后两处改 `authz.Config{}`；env 断言用例改为 auth_service 下断言。**第四版追加（兼容入口删除）**：`TestRateLimitAppliesToPlatformPrincipal`（L21-L56）装配点 L35-L36 迁主链 `ResolveAuthzPolicy + AuthenticatePrincipal + AuthorizePrincipal`；`TestAuthWithClientRejectsMissingPolicy`（L111-L135）随 `AuthWithResolvedPolicy` 删除改用 `AuthenticatePrincipal` 并更名（`AUTHZ_POLICY_MISSING` 断言不受影响，错误码来自链上 `ResolveAuthzPolicy`） |
| `internal/middleware/ratelimit_test.go` | **第二版完全遗漏**：L76 `authz.Config{Mode: authz.ModeOff}` → `authz.Config{}`（限流测试的链路装配，编译失败点） |
| `internal/middleware/sandbox_auth_test.go` | **第四版随兼容入口删除调整**：L79-L80 装配由 `AuthWithClient(nil)`/`RBACWithClient(nil)` 迁主链方式（`ResolveAuthzPolicy` + `AuthenticatePrincipal(nil)` + `AuthorizePrincipal(nil)`）；覆盖路由为 legacy/sandbox 场景，主链 legacy 分支最终调同一 `authenticateLegacy`/`authorizeLegacy`，行为等价 |
| `internal/middleware/service_token_test.go` | **第四版随兼容入口删除调整**：L103-L104、L151 装配迁主链方式（同上）；顺带修正 L124、L162 已过时的 "mode=off" 注释（mode 概念已删除，改为"legacy 链路不得触发 V2 RPC"表述） |
| 迁移后无需再改动的测试 | `contract_switch_test.go`（原 pilot_test.go）auth_service 下 generated→V2、legacy→旧 middleware 断言均走主链装配，不受兼容入口删除影响 |

### 4.5 明确不动的部分

- 生成器与门禁脚本（`scripts/generate_gateway_authz*.py`、`validate_gateway_authz_drift.py`、`validate_core_gateway_authz_routes.py`）：无 pilot/mode 引用，不改。
- `zz_generated_core_policies.go`：生成物，不手改。
- `auth-service` 侧：无 pilot/mode 概念，不改。
- V2 认证/授权 middleware（`generated_authz.go` 等）：不改。
- `Registry.LookupOperation`（[policy.go#L101](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/authz/policy.go#L101)）：第三版复查补充。删除 `Validate` 后其生产引用仅剩 config.go（原 mode.go，§4.1 改名）L104（随 §4.1 收敛一并消失），但 `policy_test.go#L55` 仍在使用，且它是 registry 的按 operationId 查询能力（与 mode 概念无关）——**保留不删**。

## 5. 部署配置变更

```yaml
# 改前
env:
  - name: ANI_AUTH_MODE
    value: "auth_service"
  - name: GATEWAY_AUTHZ_POLICY_MODE
    value: "pilot"
  - name: GATEWAY_AUTHZ_PILOT_OPERATIONS
    value: "listQuotaMeta,getPlatformMeteringUsage"

# 改后
env:
  - name: ANI_AUTH_MODE
    value: "auth_service"
```

本地开发：必须设 `ANI_AUTH_MODE=dev`（generated 自动回落 legacy），不再需要任何 policy 相关变量。注意：不设 `ANI_AUTH_MODE` 时 AuthMode 为空、不等于 dev，generated 直通 V2，本地无 auth-service 会请求失败——"回落 legacy"仅由 `dev` 显式触发，无隐式默认。同步清理引用旧 env 的本地文档（如计量任务 `本地启动步骤-*.md` 的 Gateway env 列表）与本地启动脚本。

**存量部署清单（第三版复查补充，必须与代码同 PR 清理）：**

- [sprint13-production-shaped-gateway-deployment.yaml#L40-L42](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml#L40-L42) 当前携带 `GATEWAY_AUTHZ_POLICY_MODE` 与 `GATEWAY_AUTHZ_PILOT_OPERATIONS` 两个 env 条目，随本 PR 删除、保持清单干净。第五版拍板：部署走新集群从头部署，无存量 Deployment 残留场景，代码不设残留检测（携带废弃 env 启动会被静默忽略，见 §4.1/§8）。
- 历史文档（`repo/services/tasks/modules/plan/*.md`、`repo/development-records/*.md`）中的旧 env 引用属历史记录，**不改**；V4 方案按 §7 追加修订记录即可。

## 6. 合入前检查清单（一次性门禁）

由于"generated 即切 V2"且无 mode 开关，本 PR 合入部署前必须确认 registry 中**所有** generated operation 的 V2 链路已验证：

1. `grep PolicySourceGenerated zz_generated_core_policies.go` 盘点 generated 清单；
2. 逐个确认已有 V2 契约测试（认证、授权、boundary 403、错误映射）——当前清单就是 `listQuotaMeta`、`getPlatformMeteringUsage`，均已在 pilot 阶段端到端验证过；
3. 部署即生效：当前 generated 集合与已验证集合一致，行为零变化；观察鉴权指标确认。

**后续新增接口纪律**：新接口在合入 PR 中随 `x-ani-authz` 一起补 V2 契约测试（负例至少覆盖：boundary/principal_kinds 403、`CREDENTIAL_DOMAIN_MISMATCH`、错误码映射）；review 时 v1.yaml 变更与测试同看，缺测试不合并。此纪律写入 HOW-TO 文档，作为 pilot 审批的替代闸门。

## 7. 文档同步更新

| 文件 | 更新内容 |
|---|---|
| `HOW-TO-新增接口与权限字段配置.md` | 删/改：核心结论 7、8、9；Step 5 全部 env/pilot 内容；5.5 全节；§7 常见错误表所有 mode/allowlist 行；§8.1 config.go（原 mode.go，改名后）行说明、§8.2 部署 env 说明。新增：Step 5 改为"补 V2 契约测试"；新增接口三步清单（§2.1）；dev 行为说明（generated 自动回落 legacy） |
| `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md`、`repo/development-records/README.md`、`repo/development-records/{批次名}.md` | 按 CLAUDE.md Feature batch 规则四件套更新 |

## 8. 风险与回滚

| 风险 | 缓解/决策 |
|---|---|
| **删除 mode 开关后无运行时逃生舱**：V2 链路生产故障时无法 env 一键回 legacy，只能回滚镜像 / revert | **有意接受**（2026-08-28 决策）：当前 generated 仅 2 个且均端到端验证，暴露面小；回滚手段为重新部署上一镜像（分钟级）。未来 generated 数量增多后如需秒级 kill-switch，再评估重新引入轻量开关，不提前设计 |
| 存量 legacy route 后续加 `x-ani-authz` 时，合并即切 V2，若 V2 语义有缺陷直接暴露 | 替代闸门：合入 PR 必须同补 V2 契约测试；v1.yaml 变更 CODEOWNERS 共同审查 |
| dev 行为语义变化：原 dev+pilot/full 启动失败 → 现自动全 legacy | 与原 `dev+off` 逐请求行为一致，本地开发更简单（零 policy 配置）；文档同步说明 |
| 删除兼容入口（§4.3）后测试装配迁移引入回归 | 迁移是机械替换：主链 legacy 分支与被删入口最终调同一 `authenticateLegacy`/`authorizeLegacy`，且迁移后测试走真实生产装配路径；全量 `go test ./services/ani-gateway/...` 验证 |
| 本地/旧清单携带废弃 env 启动 | 静默忽略，无行为影响（第五版拍板：新集群部署无存量残留场景，不设检测）；仓库清单与本地文档随 PR 清理兜底 |
| review 漏掉"加扩展未补测试" | 可选加固：后续在 `validate-gateway-authz` 增加一项"generated operation ↔ V2 契约测试覆盖"比对脚本（本方案不强制，避免过度工程，先跑纪律观察） |
| 严格集合删除后失去"代码级切流审批" | 该审批语义已被"契约即开关"取代：切流范围 = 契约内容本身，契约变更有 PR 审查与 drift 门禁兜底 |

**回滚路径**：回滚镜像 / revert 本批次 PR（改动集中单包，冲突面小）；本地开发异常时改 `ANI_AUTH_MODE=dev` 即全 legacy。

## 9. 实施顺序（单 PR）

1. `git mv mode.go config.go` + 收敛（§4.1，含删残留检测、`ConfigFromEnv` 去 error 签名）+ `chain.go` 删 Validate 调用与 error 处理（§4.2）+ `auth.go`/`rbac.go` 删除兼容入口 6 个函数（§4.3，2026-08-31 评审拍板）；
2. 测试改写（§4.4）；
3. 验证：

```bash
cd repo
go test ./services/ani-gateway/...
make gen-gateway-authz          # 确认生成物无漂移（本批次不应触碰生成物）
make validate-gateway-authz
make test
make validate-architecture
git diff --check
```

4. 文档更新（§7）；
5. 部署：清理部署清单中两个废弃 env——**含存量 [sprint13-production-shaped-gateway-deployment.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml)，必须与代码同 PR（§5）**（保留 `ANI_AUTH_MODE=auth_service`），部署即按 §6 观察。本地开发同步更新启动脚本。

## 10. 改动量预估

| 项 | 量 |
|---|---|
| `mode.go` → `config.go` | `git mv` 改名；132 行 → 约 30 行（净删约 100 行） |
| `chain.go` | 删 4 行 |
| `auth.go` / `rbac.go` | 删 6 个函数约 80 行（`Auth`/`AuthWithClient`/`AuthWithResolvedPolicy`、`RBAC`/`RBACWithClient`/`RBACWithResolvedPolicy`）；保留 `authenticateLegacy`/`authorizeLegacy` |
| 测试 | config_test（原 mode_test，随 §4.1 改名）/ principal_test / pilot_test / policy_test / ratelimit_test 改写，净删约 120+ 行；policy_test / sandbox_auth_test / service_token_test 共 5 处装配迁主链方式（§4.4 第四版追加） |
| 部署 | 删 2 个 env 变量（含 `sprint13-production-shaped-gateway-deployment.yaml` 存量条目） |
| 文档 | HOW-TO 精简约 70 行；V4 追加修订记录 |
