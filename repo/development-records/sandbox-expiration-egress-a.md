# IN-INSTANCE-SANDBOX-EXPIRATION-EGRESS-A

- 批次：`IN-INSTANCE-SANDBOX-EXPIRATION-EGRESS-A`
- 分支：`fix/instance-searchfield-and-ops-pagination`
- 日期：2026-09-15
- 状态：**LOCAL_VERIFIED（代码完成 + 单测门禁全绿）**；**未部署 ani-test2，不标 runtime ready**

> 本批次修复 `kjs-study/修复bug/测试问题分析与修复记录.md` 中的 **Bug-7（沙箱创建后到点未自动过期）** 与 **Bug-8（沙箱详情出口白名单为空，仅交付 A 回显部分）**。

---

## 1. Bug-7：沙箱到期自动过期引擎

### 根因

沙箱**能"续期/延长"（`extend`/`touch_idle`），但到点无人触发过期**：

1. `SandboxConfig` 只有 `SessionTimeout/IdleTimeout/OnTimeout`，**没有持久化的绝对到期时间**（无 `expires_at`/`last_activity_at`）。
2. **没有任何后台扫描**在归零时执行 `OnTimeout(pause|kill)` 或置 `SandboxStateExpired`。
3. K8s Provider 不消费 `SessionTimeout`，只把 spec 写入；Pod 未设 `ActiveDeadlineSeconds`，K8s 侧无原生兜底。

### 修复设计（遵守"尽量不动表结构"）

- 到期时间作为 `SandboxConfig`（存于 InstanceSpec JSONB）新增字段，**无新增列、无 migration**。
- 新增网关后台常驻 worker（跨租户枚举 + 定周期扫描 + OnTimeout 执行 + 幂等态）。

### 实现

- **字段（新增）**：[sandbox_runtime.go](file:///e:/go/project/ANI/repo/pkg/ports/sandbox_runtime.go#L43-L50) `SandboxConfig` 增加 `ExpiresAt time.Time`（会话绝对到期 = createdAt+SessionTimeout）与 `LastActivityAt time.Time`（最后活跃，供 idle 到期）。
- **本地 runtime**：[local_sandbox_runtime.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/local_sandbox_runtime.go#L139-L141) `Create` 时 `ExpiresAt=now+SessionTimeout`、`LastActivityAt=now`；[ApplyLifecycle](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/local_sandbox_runtime.go#L901-L914)：
  - `extend` → **推进绝对 `ExpiresAt += duration`，不再改 `SessionTimeout` 基线**（避免与展示基线脱节）；
  - `touch_idle` → `LastActivityAt = now`。
- **后台扫描器**：[sandbox_expiration_controller.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/sandbox_expiration_controller.go)（新增）：
  - 默认 30s 周期、每次枚举上限 100；
  - 跨租户枚举非终态沙箱 → 见 `ListRunningSandboxes`；
  - 到期判定 `sandboxConfigExpired`：`!ExpiresAt.IsZero() && now>=ExpiresAt`（session）或 `IdleTimeout>0 && !LastActivityAt.IsZero() && now>=LastActivityAt+IdleTimeout`（idle）；
  - `onTimeoutAction`：`kill→WorkloadLifecycleDelete`、其余（含 `pause`/空）→ `WorkloadLifecyclePause`；
  - 执行 `ApplyLifecycle` 后置 `SandboxStateExpired` 并用 `UpsertStatus` 持久化（expired 后不再被枚举，天然幂等）。
- **端口定义（新增）**：[sandbox_runtime.go](file:///e:/go/project/ANI/repo/pkg/ports/sandbox_runtime.go#L306-L319) `ExpirableSandboxLister`（跨租户 `ListRunningSandboxes`）与 `SandboxExpirationController.Start(ctx)`。
- **跨租户枚举**：[instance_store.go #ListRunningSandboxes](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/instance_store.go#L292-L347)：`workload_kind='sandbox' AND state NOT IN (deleted, stopped, failed)`，`WithPlatformTx` 平台读，`ORDER BY updated_at ASC LIMIT $1`。
- **装配**：
  - [deps.go:290-294](file:///e:/go/project/ANI/repo/pkg/bootstrap/deps.go#L290-L294) 构造 controller（lister/store 复用 instanceStore）；
  - [Capabilities.SandboxExpiration](file:///e:/go/project/ANI/repo/pkg/bootstrap/instance.go#L49) → gateway [main.go:110-117](file:///e:/go/project/ANI/repo/services/ani-gateway/main.go#L110-L117) 随 existing 启动 goroutine。

### 测试（新增/更新）

- [sandbox_expiration_controller_test.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/sandbox_expiration_controller_test.go)（新增）：
  - `sandboxConfigExpired` 表驱动（session 到期前/后、idle 前/后、零值永不、idle 禁用 + 零活动）；
  - `onTimeoutAction` 映射（kill→delete、pause/空/未知→pause）；
  - controller 对到期沙箱执 pause 并落 expired（upsert=1、WorkloadState→stopped、Reason=SandboxExpired）；
  - 非到期跳过（upsert=0）；缺依赖 `Start` no-op。
- [local_sandbox_runtime_test.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/local_sandbox_runtime_test.go)（新增）：创建时到期字段初始化；`extend` 推进 ExpiresAt 且 SessionTimeout 不变；`touch_idle` 只刷新 LastActivityAt。
- [instance_service_test.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/instance_service_test.go#L1928-L1934)（更新）：既有 lifecycle 测试由"断言 `SessionTimeout` 累加"改为"断言 `ExpiresAt` 推进 + `SessionTimeout` 保持 30m"。

### 验收

`go build ./pkg/bootstrap ./services/ani-gateway/...`、`go test ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/...`、`validate_component_imports.py`、`validate_inference_legacy_control_plane.py`、gofmt、`git diff --check` 全绿。

**门槛/已知范围**：未做 Pod `ActiveDeadlineSeconds`、DB 级过期过滤/幂等锁、`remain_seconds` 展示；真实 provider（KubernetesSandboxRuntime）到期 pause/kill 落地依赖其既有 `ApplyLifecycle`。**未 deploy 前线上不生效。**

---

## 2. Bug-8（A 部分）：沙箱详情出口白名单回显

### 根因

白名单**已被创建请求接收并随 spec 落库**，但详情响应结构 `instanceSandboxResponse` **漏了 `egress_allowlist`**，`sandboxResponseFromRecord` 不回填 → 前端详情恒空。此为"详情白名单为空"的直接可见缺陷。

### 修复（仅 A 回显，B 网络策略下发按平台出网前提搁置）

- [instances.go:530](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/instances.go#L530) `instanceSandboxResponse` 新增 `EgressAllowlist []string json:"egress_allowlist,omitempty"`。
- [instances.go:3783](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/instances.go#L3783) `sandboxResponseFromRecord` 从 `record.Sandbox.Config.EgressAllowlist` 拷贝填入。
- OpenAPI 沙箱详情响应 schema 本就含 `egress_allowlist`（[v1.yaml:1819](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml#L1819)）——属网关回显补漏，**无契约/SDK 生成物变更**。

### 验收

gateway/router 测试全绿，gofmt/`git diff --check` 通过。

**门槛**：B（真实 egress 网络策略下发）因租户 VPC 无 centralized gateway / Kube-OVN 域名支持缺位而阻塞，作为与 Bug-10 同根的平台缺口一并跟踪，本批次不实施。

---

## 3. 变更文件清单

| 文件 | 说明 |
|---|---|
| repo/pkg/ports/sandbox_runtime.go | `SandboxConfig` 增 `ExpiresAt/LastActivityAt`；新增 `ExpirableSandboxLister`、`SandboxExpirationController` 接口 |
| repo/pkg/adapters/runtime/local_sandbox_runtime.go | Create 初始化到期字段；extend/touch_idle 更新 |
| repo/pkg/adapters/runtime/sandbox_expiration_controller.go | 后台到期扫描器（新增） |
| repo/pkg/adapters/runtime/sandbox_expiration_controller_test.go | 控制器测试（新增） |
| repo/pkg/adapters/runtime/local_sandbox_runtime_test.go | 到期字段测试（新增） |
| repo/pkg/adapters/runtime/instance_service_test.go | lifecycle 测试断言更新 |
| repo/pkg/adapters/runtime/instance_store.go | `ListRunningSandboxes` 跨租户枚举 |
| repo/pkg/bootstrap/deps.go | 构造并注入 controller |
| repo/pkg/bootstrap/instance.go | `InstanceRuntime.SandboxExpiration` |
| repo/services/ani-gateway/internal/router/instances.go | 详情回显 `egress_allowlist` |
| repo/services/ani-gateway/main.go | gateway 启动到期扫描 goroutine |

## 4. 后续项

- ani-test2 部署实测：到期自动过期（创建短 SessionTimeout 沙箱观察 states）、egress 白名单详情回显。
- 平台租户 VPC 出网打通后评估 Bug-8-B 网络策略渲染 + Bug-10 preview 可达性（同根）。