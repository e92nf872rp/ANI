# Sprint 14 Core 韧性与服务语义规划 — Adapter Resilience & Core Service Semantics

> **For agentic workers / AI Coding（任意模型均适用）：** 本文件是可被任意编码模型加载后独立执行的实施计划。执行前必须先读「§0 前置事实（防幻觉基线）」并在真实工作区逐条核对；不得凭记忆或本文之外的历史 handoff 推断现状。任务步骤使用 `- [ ]` 复选框跟踪。推荐配合 `superpowers:executing-plans` 或 `superpowers:subagent-driven-development` 执行。
>
> 记录类型：Planning / Sprint 14 resilience readiness plan
> 适用范围：ANI Core 生产级韧性与服务语义补齐（仅 Core，不含 Services）
> 前置 Sprint：Sprint 13（S01–S07 real provider / live gate 已 production-shaped passed）
> 计划状态：**草案，待 Sprint 13 收口后激活**。激活时按 CLAUDE.md §6.3 走 Feature batch 四件套闭环（本文件 + `README.md` + `CURRENT-SPRINT.md` + `ANI-06-开发计划.md`）。本文件本身不是完成记录。
>
> 被引用入口：`repo/development-records/README.md`（Sprint 14 Planning 草案条目）、`repo/CURRENT-SPRINT.md`（下一冲刺草案前向指针）。AI 可经标准加载顺序发现本文件。

---

## 加载方式（goal 提示词，可直接粘贴给任意 AI agent）

```text
goal: 执行 ANI Sprint 14 Core 韧性与服务语义计划
按顺序加载上下文，再动手：
  1. repo/CLAUDE.md
  2. repo/ANI-DOCS-INDEX.md
  3. repo/CURRENT-SPRINT.md
  4. repo/development-records/sprint14-core-resilience-plan.md   ← 本计划（执行主文件）
执行规则：
  - 先读 §0 现状事实 + §0.3 开工前置，逐条按「核对命令」grep 验证；现状不符则停下来先更新，不要带着旧假设继续。
  - **R-P0-1/R-P0-2 有硬前置 R-P0-0（gateway shared store，见 §0.3 F11）；当前分支 R-P0-0 已完成，后续批次必须复用该 store 注入模式。**
  - 严格按 §4 阶段顺序执行：阶段零 R-P0-0 → 阶段一 P0（R-P0-1..4）→ 阶段二 P1（R-P1-5..6）→ 阶段三 P2（R-P2-7）。批次依赖见 §4 依赖图与 §0.1 追溯矩阵。
  - 本计划命名的 make 门禁均为**待新建**（§0.3 F14），每批验收含"新建该 target"。
  - 每批次按 §5 的 TDD 步骤：先写失败测试 → 跑红 → 最小实现 → 跑绿 → 提交。
  - 不新增 Services 逻辑、不改 api/openapi/services/v1.yaml、不依赖外部产品定义（见 §2）。
  - 凡声称真实韧性必须跑通 §6 对应 live gate；未跑通只能标 local/logic verified，禁止标 production ready。
  - 每批完成跑 make test、make validate-architecture、git diff --check；完成后走 CLAUDE.md §6.3 四件套闭环。
```

---

## §0 现状事实（防幻觉基线 / 换模型必读）

> **重要：F1–F9 是「现状事实」的编号，不是开发阶段。** 开发阶段是 §4 的 **P0 / P1 / P2** 与批次 **R-P0-\* / R-P1-\* / R-P2-\***。每条事实暴露一个差距，由哪个批次在哪个阶段修复，见紧随其后的 **§0.1 追溯矩阵**。

下列事实在编写本计划时已逐条核对到文件与行号。任何模型在执行前**必须重新 grep 验证**；若现状已变，以真实代码为准并更新本节。

| # | 事实 | 证据（文件:行） | 含义 |
|---|---|---|---|
| F1 | async-task 创建走 DB 级幂等：`ON CONFLICT (tenant_id, idempotency_key) DO NOTHING` | `pkg/repo/task_repo.go:98` | 异步任务幂等**已有**，可复用其模式 |
| F2 | 幂等重放**实现碎片化、不统一**：observability 规则创建走**内存 map** 重放（`local_observability_service.go:77`，**重启/多副本即失效**）；async-task 走 DB `ON CONFLICT`（`task_repo.go:98`）；多数 mutating handler 只**校验** `idempotency_key` 必填、不重放。无统一且持久的重放机制，也无幂等中间件 | `local_observability_service.go:77`、`task_repo.go:98`、`middleware/` 无 idempotency | 不是"空白"，而是"碎片化 + 内存版不持久"，需收敛为一处持久机制 |
| F3 | 限流桩已由 R-P0-1 替换：`RateLimit(store)` 使用 gateway shared store 做 per-tenant + route-class 窗口计数，超限返回 429；本批仍仅为 local/logic verified | `services/ani-gateway/internal/middleware/ratelimit.go`、`services/ani-gateway/internal/middleware/ratelimit_test.go`、`Makefile:validate-gateway-ratelimit` | 背压/限流本地逻辑已落地；未跑真实压测/live gate，不标 production ready |
| F4 | readyz 仅探 postgres/nats/redis；**Milvus 已有 `Health()`**（`milvus_store.go:137`，未接 readyz），MinIO/network/storage/k8s/gpu 无 Health | `pkg/bootstrap/probes.go` 内 `dependencyProbeChecks`；`milvus_store.go:137` | vector 是"接线"，object/k8s 是"新增 Health" |
| F5 | 重连仅连接期：NATS `MaxReconnects(5)`、pgxpool `HealthCheckPeriod=30s`、go-redis pool 自动重连 | `pkg/bootstrap/nats.go:23-25`、`pkg/bootstrap/db.go:23-27` | 无操作级重试 |
| F6 | 超时仅启动期：db 30s / redis 10s / nats 30s。数据面真实调用（network/storage/k8s/gpu 共享 `kubernetes_rest_client` + minio/milvus）**无请求级超时**；`main.go` 不注入带 Timeout 的 http.Client → 回落到无超时默认 client。**唯二有超时模式**：`auth_client.go`（9 处）、`demo_instances.go:841`（exec 5s） | `pkg/bootstrap/{db,redis,nats}.go`；`kubernetes_rest_client.go:139/331`；`main.go`；`auth_client.go` | 无 adapter 每调用强制超时；auth_client 可作 R-P0-3 参考 |
| F10 | `kubernetes_rest_client.do()` 把**所有**非 2xx（含 5xx/429/网络错误）统一包成 `ports.ErrInvalid` | `kubernetes_rest_client.go:340-342` | **R-P1-5 前置**：先修错误分类，否则 `Retryable()` 无法区分可重试/不可重试 |
| F7 | 全线单 endpoint：MinIO `Endpoint`、Milvus `Endpoint`、Redis 单 `Addr`（普通 Options）、PG 单 URL | `pkg/adapters/objectstore/minio_store.go:31`、`pkg/adapters/vectorstore/milvus_store.go:25`、`pkg/bootstrap/redis.go` | 无 failover / 多端点 |
| F8 | 全仓库无断路器；reconcile 有失败退避计数 `ani_workload_reconcile_backoff_skips_total` | grep `circuitbreak`=0；`pkg/bootstrap/probes.go` | 断路器是空白 |
| F9 | SDK 与契约漂移风险已由当前门禁约束；2026-06-23 复核 `make validate-sdk-beta` 通过，历史缺口 `createNetworkRoute/createStorageBucket/...` 已不再复现 | `make validate-sdk-beta` 当前通过；相关 operationId 已存在于 `sdks/core/*` 与 `sdks/core/sdk-metadata.json` | 与本 Sprint 无强依赖；本 Sprint 仍不得引入新漂移 |

**核对命令（执行前先跑）：**
```bash
cd repo
grep -n "checkLimit" services/ani-gateway/internal/middleware/ratelimit.go
grep -rn "dependencyProbeChecks" pkg/bootstrap/probes.go
grep -rln "idempoten" services/ani-gateway/internal/middleware/ || echo "NO idempotency middleware (expected)"
grep -rn "circuitbreak\|CircuitBreaker" pkg/ || echo "NO circuit breaker (expected)"
```

---

## §0.1 差距 → 批次 → 阶段 追溯矩阵（把 F1–F9 和 P0/P1/P2 关联起来）

> 这张表是 §0 现状事实与 §4/§5 开发批次的唯一对照入口。读它就知道「每个差距由哪个批次、在哪个阶段修复」。

| 现状差距（§0 事实） | 修复批次 | 阶段 | 落点 |
|---|---|---|---|
| F11 gateway 无共享存储后端（R-P0-1/2 的前置） | **R-P0-0** 已引入 gateway 共享 store | **P0 前置** | main.go + chain.go + store.go |
| F3 限流是桩（`checkLimit` 恒 true） | **R-P0-1** 已落地限流背压（依赖 R-P0-0） | **P0** | gateway middleware |
| F2 幂等重放碎片化（内存版不持久） | **R-P0-2** 统一幂等重放中间件（收敛 + 修内存版缺陷） | **P0** | gateway middleware |
| F6 无每调用超时 | **R-P0-3** 每调用超时 + resilience 包骨架 | **P0** | `pkg/adapters/resilience` + 各 adapter |
| F4 数据面未接 readyz | **R-P0-4** 数据面 readyz | **P0** | ports `Health()` + `probes.go` |
| F5 无操作级重试（仅连接期）+ F8 无断路器 | **R-P1-5** 重试 + 断路器 | **P1** | `pkg/adapters/resilience`（叠加在 R-P0-3 之上） |
| 降级语义缺失（关联 F4 数据面健康 + F8 断路状态） | **R-P1-6** 优雅降级策略 | **P1** | `resilience/degradation.go` + `probes.go` |
| F7 全线单 endpoint、无 failover | **R-P2-7** 多端点 / failover | **P2** | adapter 多端点 config + installer 拓扑 |
| F1 async-task 幂等**已有** | 不新建，**被 R-P0-2 复用**其 DB 模式 | — | `pkg/repo/task_repo.go` |
| F9 SDK↔契约漂移风险（当前门禁已通过） | 非本 Sprint 批次，仅作**约束**：本 Sprint 不得引入新漂移 | — | 由 `validate-sdk-beta` 把关 |

**一句话读法：** §0 的事实里，F2/F3/F4/F6 → P0 四批，F5/F8 + 降级 → P1 两批，F7 → P2 一批；F1 是可复用的既有能力，F9 是约束项，**F10 是 R-P1-5 的前置（先修错误分类）**。无遗漏，七个批次（R-P0-1..4、R-P1-5..6、R-P2-7）一一覆盖所有差距。

---

## §0.2 逐 adapter 韧性现状矩阵（深扫补全，2026-06-22）

> 逐文件核对 network / storage / k8s / gpu / registry / object / vector，避免下一步开发按"抽样印象"跑偏。✅=已具备，❌=缺失，N/A=能力未建。

| Adapter（真实 provider） | 数据通路 | 每调用超时 | 操作重试 | 断路器 | 健康探测 | 单端点/failover | 备注 |
|---|---|---|---|---|---|---|---|
| **network**（kubeovn_rest） | `kubernetes_rest_client.go`（共享） | ❌ | ❌ | ❌ | ❌ 无 Health | 单 host，❌failover | 走共享 REST client，继承其全部缺口；本体 `network_service.go` 0 韧性原语 |
| **storage**（kubernetes_rest / Rook-Ceph） | `kubernetes_rest_client.go`（共享） | ❌ | ❌ | ❌ | ❌ 无 Health | 单 host，❌failover | 同上 |
| **k8s**（vCluster/K8s API） | `kubernetes_rest_client.go` + `k8s_cluster_proxy_forwarding_service.go` | ❌（proxy 会**传播**父 client Timeout，但默认父 Timeout=0） | ❌ | ❌ | ❌ 无 Health | 单 host，❌failover | proxy:313 传播 Timeout 是唯一"超时感知"，但默认仍 0 |
| **gpu**（kubernetes_rest） | `kubernetes_gpu_inventory.go` 包 `*KubernetesRESTClient` | ❌ | ❌ | ❌ | ❌ 无 Health | 单 host，❌failover | 完全继承共享 client 缺口 |
| **object**（MinIO） | `minio_store.go` | ❌ | ❌ | ❌ | ❌ 无 Health | 单 `Endpoint`，❌failover | — |
| **vector**（Milvus） | `milvus_store.go` | ❌ | ❌ | ❌ | ✅ **已有 `Health()`**（:137，未接 readyz） | 单 `Endpoint`，❌failover | R-P0-4 对它是"接线" |
| **registry** | `local_image_registry.go`（内存）+ `not_configured.go` | N/A | N/A | N/A | N/A | N/A | **无真实 Harbor adapter**；韧性待该能力建成后再纳入 |

**关键结论（影响 Sprint 14 范围）：**
1. **network/storage/k8s/gpu 的韧性缺口是同一个根**——共享的 `kubernetes_rest_client`。**在它上面（或外面包一层 `resilience.Do`）一次性补超时/重试/断路，四个 provider 同时受益**，不要四处各写。这是 R-P0-3/R-P1-5 的最高杠杆点。
2. **F10 错误分类**必须先修：当前所有非 2xx → `ErrInvalid`，重试层无法判断可重试性。
3. **registry 不在本 Sprint 韧性范围**（真实 provider 尚未建）。

---

## §0.3 开工前置（装配层事实，已核实 2026-06-23，决定可启动性）

> 这些是「开工第一小时就会撞上」的事实，逐条有代码证据。不先解决，R-P0-1/R-P0-2 无法开工。

| # | 事实 | 证据 | 对计划的影响 |
|---|---|---|---|
| **F11** | **gateway shared store 前置已由 R-P0-0 建立**：`main.go` 通过 bootstrap 构造 Redis-backed `ports.CacheStore`，`Register(h, store)` 显式接收；middleware 仍不直接 import Redis SDK；audit 落库仍是 `// TODO: batch-write ... via DB pool` | `services/ani-gateway/main.go`、`services/ani-gateway/internal/middleware/chain.go`、`services/ani-gateway/internal/middleware/store.go`、`pkg/bootstrap/redis.go` | R-P0-1/R-P0-2 的共享存储前置已满足；后续批次必须继续通过 store 注入，不得在 middleware 直接依赖 Redis SDK |
| **F12** | 中间件依赖注入模式已扩展为 `Register(h, store)`：auth client 仍在 `Register` 内构造，RateLimit 已由 R-P0-1 接收 shared store | `chain.go:9-16`、`ratelimit.go` | R-P0-2 应复用同一个 store 注入模式，在 `RateLimit(store)` 之后、`Audit()` 之前接入 |
| **F13** | gateway **无中央 Config**；每个 runtime 各自 `gatewayXxxRuntimeConfigFromEnv()` 从 env 取值并构造自己的 client | `services/ani-gateway/*_runtime.go`（network/storage/k8s/gpu/...） | R-P0-3 超时注入落点 = 这些 per-runtime config 函数 + 各自 http.Client 构造，**不是某个中央 config** |
| **F14** | 本计划命名的 `make validate-gateway-ratelimit` 已由 R-P0-1 新建；其余 `validate-gateway-idempotency`、`validate-readyz-dataplane-live-gate`、`validate-resilience-faultinjection-live-gate`、`validate-ha-failover-live-gate` 仍不存在 | `Makefile` | 后续每批的"验收 gate"仍含"新建该 target"这一步；R-P0-1 gate 已可复跑 |

**核对命令：**
```bash
cd repo
grep -rn "redis\|pgx" services/ani-gateway | grep -v _test
sed -n '1,20p' services/ani-gateway/internal/middleware/chain.go
grep -cE "validate-gateway-ratelimit|validate-gateway-idempotency|validate-readyz-dataplane-live-gate|validate-resilience-faultinjection-live-gate|validate-ha-failover-live-gate" Makefile
```

### R-P0-0 · 为 gateway 引入限流/幂等的共享存储后端（R-P0-1/R-P0-2 的硬前置）

**问题：** gateway 当前无状态（F11）。限流（令牌桶计数）与幂等重放（响应缓存）都需要**跨请求、跨副本共享**的存储。

**决策点（必须先定，二选一，建议人工拍板）：**
- **方案 A（推荐）：给 gateway 接 Redis。** 限流的令牌桶天然需要低延迟共享计数，Redis 是标准选择；幂等重放也复用同一 Redis（带 TTL）。代价：gateway 新增 Redis 依赖 + main.go 装配 + `Register` 注入。
- **方案 B：幂等下沉到服务层、限流另解。** 幂等重放放进已有 DB 的 Core 服务层（与 `task_repo` 幂等同构），限流仍需共享存储。代价：幂等不再是统一中间件，回到"按 handler"；与 R-P0-2"收敛为一处"目标冲突。

> 本计划**默认方案 A**。若选 B，R-P0-2 的落点与目标需相应改写。

**Files（方案 A）：**
- Modify: `services/ani-gateway/main.go`（构造 Redis client，传入 `middleware.Register`）
- Modify: `services/ani-gateway/internal/middleware/chain.go`（`Register(h, store)` 注入）
- Create: `services/ani-gateway/internal/middleware/store.go`（gateway 侧 KV 抽象 + Redis 实现；超时复用 R-P0-3）
- 复用：`pkg/bootstrap/redis.go` 的连接模式（注意 F7：当前单 Addr）

**契约影响：** 无。

**任务步骤：**
- [x] 人工确认方案 A/B（采用本计划默认方案 A：gateway 接 Redis）。
- [x] 写失败测试：`TestGatewayStoreSetGetTTL`。
- [x] 实现 gateway KV 抽象 + Redis 实现 + main 装配 + `Register` 注入。
- [x] 跑绿；`go test ./services/ani-gateway/internal/middleware -run Store -v`。
- [x] 提交：`0856f9a feat(gateway): add shared store foundation for sprint14`

**验收 gate：** `go test ./services/ani-gateway/internal/middleware/ -run Store`（新建）。

---

## §1 目标

把 Sprint 13 已 production-shaped 的 Core handler/ports/adapters，从「功能可开通」提升到「生产级运行期韧性」。补齐两类能力：

1. **Adapter 韧性**：每调用超时、操作级重试退避、断路器、数据面健康探测、（拓扑就绪后的）多端点 failover。
2. **Core 服务语义**：通用幂等响应重放、背压/限流落地、优雅降级策略。

**非目标（明确不做，奥卡姆 / YAGNI）：** 不重写 Sprint 12/13 handler；不新增 Services 业务逻辑；不为「未来可能」预造多实现框架；不改 OpenAPI 资源语义。

---

## §2 与 ANI Services 的交互声明（关键）

> **本 Sprint 与 ANI Services 零交互，可完全独立开展。**

理由：
- 本 Sprint 全部改动落在 **Core 自有边界**：`pkg/adapters/*`、`pkg/bootstrap/*`、`services/ani-gateway/internal/middleware/*`、`pkg/ports/*`（仅加只读 `Health()` 能力方法）。
- 不触碰 `repo/services/`（Services 业务）、不触碰 `api/openapi/services/v1.yaml`、不依赖外部团队 2026-06-10 的产品定义。
- 韧性是「Core 调用底座组件」时的可靠性属性，与 Services 业务定义正交。Services 未来通过 Core OpenAPI/SDK 调用 Core 时，会**自动**受益于这些韧性，无需 Services 改一行代码。

**因此：Sprint 14 的 Core 技术范围不被外部 Services 依赖阻塞。** 正式开工仍需按入口文档完成 Sprint 13 收口与 Sprint 14 激活，并先确认 R-P0-0 的 gateway 共享存储方案。

---

## §3 架构落点（符合 CLAUDE.md §5 ports/adapters 边界）

| 韧性类别 | 落点 | 原因 |
|---|---|---|
| 幂等响应重放、限流背压、请求超时 | **Gateway middleware**（`services/ani-gateway/internal/middleware/`，已有 chain） | HTTP 横切关注点 |
| 每调用超时、重试退避、断路器 | **新增 `pkg/adapters/resilience` 共享包**，被各 adapter 包裹外部 client 调用 | 同一类策略复用一处；不污染 domain service 与 ports 语义 |
| 数据面健康 | **ports 加只读 `Health(ctx) error`** + `pkg/bootstrap/probes.go` 接线 | readyz 需感知数据面 |
| 多端点 / failover | **adapter 接受端点列表/VIP** + installer 提供拓扑 | adapter「不假设单点」+ 拓扑解耦 |

> 仅新增 `pkg/adapters/resilience` 一个实体。断路器/重试/超时是同类策略，复用一处而非每 adapter 重写——符合奥卡姆剃刀。**ports 仍只表达产品意图**（如 `Health()`），不暴露重试/断路实现细节。

`resilience` 包的对外契约（实现前先固定签名，避免各 adapter 各写一套）：

```go
// pkg/adapters/resilience/resilience.go
package resilience

import (
	"context"
	"time"
)

// Policy 描述一次外部调用的韧性策略。零值 = 不启用对应能力。
type Policy struct {
	Timeout        time.Duration // 每次尝试的 deadline；0 表示继承 ctx
	MaxAttempts    int           // 含首次的总尝试次数；<=1 表示不重试
	BaseBackoff    time.Duration // 首次退避
	MaxBackoff     time.Duration // 退避上限（指数退避 + 抖动）
	BreakerName    string        // 断路器键；空表示不启用断路器
	FailureRatio   float64       // 滚动窗口失败比超过即 open（0,1]
	MinRequests    uint32        // open 前的最小样本量，避免冷启动误判
	CooldownPeriod time.Duration // open -> half-open 的冷却时间
}

// Retryable 判定错误是否可重试（瞬时类：连接拒绝、超时、5xx、gRPC Unavailable）。
// 仅幂等操作才允许传入带重试的 Policy。
func Retryable(err error) bool

// Do 在「每调用超时 + 可重试错误重试退避 + 断路器」下执行 fn。
// 断路器 open 时立即返回 ErrCircuitOpen，不调用 fn。
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error
```

---

## §4 批次序列与执行顺序（先做什么，再做什么）

```
阶段零 P0 前置（必须先做）
  R-P0-0 为 gateway 引入共享存储后端 ← R-P0-1/R-P0-2 的硬前置（见 §0.3 F11）

阶段一 P0（零契约风险，立即生产收益）
  R-P0-1 限流落地 ──┐ 依赖 R-P0-0
  R-P0-2 幂等重放 ──┼─ 依赖 R-P0-0；R-P0-1/R-P0-2 之间可并行
  R-P0-3 每调用超时（含 resilience 包骨架）──┐ 不依赖 R-P0-0，可与之并行
  R-P0-4 数据面 readyz ─────────────────────┘ 依赖 ports 加 Health()

阶段二 P1（依赖 P0-3 的 resilience 包）
  R-P1-5 重试 + 断路器  ← 必须在 R-P0-3 之后（复用 resilience 包）
  R-P1-6 优雅降级       ← 依赖 R-P0-4（readyz 数据面）+ R-P1-5（断路状态）

阶段三 P2（部分 installer 强耦合，最后做）
  R-P2-7 多端点 / failover ← 依赖真实拓扑就绪 + R-P1-5（failover 期间断路）
```

**顺序理由：**
1. **P0 先做**：解决「现在生产真会出事」的洞（无限流→被打爆、无超时→线程耗尽、无数据面健康→流量打到挂掉的依赖），且全部零契约风险、Core 内闭环。
2. **R-P0-3 先于 R-P1-5**：R-P0-3 建立 `resilience` 包骨架（超时），R-P1-5 在同一包上叠加重试与断路，避免重复脚手架。
3. **P2 最后**：failover 依赖 installer 提供的真实 HA 拓扑（多副本/VIP/Sentinel），与上一轮「HA 拓扑归 installer、adapter 不假设单点」的结论对应；在拓扑就绪前做 adapter 改造收益有限。

---

## §5 批次详细计划

> 每个批次遵循 TDD：先写失败测试 → 跑红 → 最小实现 → 跑绿 → 提交。每批完成跑 `make test`、`make validate-architecture`、`git diff --check`。契约影响一栏=「无」者，不需要改 `api/openapi/*.yaml`。

### R-P0-1 · 限流背压落地（替换桩）

**目标：** 用 shared store 窗口计数替换 `checkLimit` 桩，per-tenant + 路由类别限流，超限返回 429。

**前置：** R-P0-0 已完成（gateway 有共享 store）。本批已将 `RateLimit()` 改为 `RateLimit(store)` 并在 `Register` 注入。

**Files：**
- Modify: `services/ani-gateway/internal/middleware/ratelimit.go`（`RateLimit(store)` + shared store 窗口计数）
- Modify: `services/ani-gateway/internal/middleware/chain.go`（注入 store 到 `RateLimit`）
- Test: `services/ani-gateway/internal/middleware/ratelimit_test.go`（新建）
- 复用：R-P0-0 的 gateway store（Redis）

**契约影响：** 无（429 已属标准错误语义，无需改 OpenAPI）。

**实现要点：**
- 原子窗口计数：复用 `GatewayStore.Increment(ctx,key,ttl)` 的 Redis `INCR` + `EXPIRE`，键 `ratelimit:{tenant_id}:{method}:{route_class}`。
- 限额来源 env / 配置；缺省给安全默认（如 100 req/s/tenant）。
- 超限 `respondError(c, 429, "RATE_LIMIT_EXCEEDED", ...)`（保持现有错误体格式）。
- 公共路径（`isPublicPath`）与无 tenant 请求维持放行。

**任务步骤：**
- [x] 写失败测试：`TestRateLimitRejectsOverQuotaAndRecoversAfterWindow`（同一 tenant 连续请求超阈值返回 429，恢复后放行）。
- [x] 跑红：`go test ./services/ani-gateway/internal/middleware/ -run RateLimit -v` → FAIL（`RateLimit` 尚未接收 store）。
- [x] 实现 shared store 窗口计数替换 `checkLimit` 恒 true。
- [x] 跑绿：同上命令 → PASS。
- [x] 提交：`feat(gateway): replace rate-limit stub with shared-store window counter`

**验收 gate：** `go test ./services/ani-gateway/internal/middleware/ -run RateLimit`；并新增 `make validate-gateway-ratelimit`（包装上述测试）。

---

### R-P0-2 · 统一幂等响应重放中间件（收敛碎片化实现）

**目标：** 现状是碎片化的（见 §0 F2：observability 内存 map 重放、async-task DB 去重、多数 handler 仅校验不重放）。本批**把幂等重放收敛为一处持久机制**：对所有 mutating 路由，同一 `(tenant_id, method, path, idempotency_key)` 的重复请求回放首次响应，副作用只发生一次。**顺带修掉 observability 内存 map 重启/多副本失效的真实缺陷。**

**前置：** R-P0-0 已完成（gateway 有共享 store）。

**Files：**
- Create: `services/ani-gateway/internal/middleware/idempotency.go`（`Idempotency(store)`）
- Test: `services/ani-gateway/internal/middleware/idempotency_test.go`
- Modify: `services/ani-gateway/internal/middleware/chain.go`（在 `RateLimit` 之后、`Audit` 之前插入 `Idempotency(store)`；执行顺序见 chain.go 注释）

**契约影响：** 无（`idempotency_key` 已在契约，本批只加运行期重放）。

**实现要点：**
- key = `idempotency:{tenant}:{method}:{path}:hash(idempotency_key)`；`idempotency_key` 取 Header `Idempotency-Key` 或 body 字段（沿用现有约定）。
- 首次：处理完成后把 `{status, content_type, body}` 存 Redis，TTL 24h。
- 重放（已完成）：直接返回存储响应，加响应头 `Idempotent-Replay: true`，不进入 handler。
- 并发重放（进行中）：写入「processing」哨兵；并发重复请求返回 `409 IDEMPOTENCY_IN_PROGRESS`。
- 仅作用于 POST / 有副作用的 PUT/PATCH；GET 跳过。

**任务步骤：**
- [ ] 写失败测试：`TestIdempotentReplayReturnsSameResponse`（同 key 两次 POST → 同 body、handler 只执行一次）。
- [ ] 写失败测试：`TestConcurrentIdempotentInProgressReturns409`。
- [ ] 跑红 → FAIL。
- [ ] 实现中间件并装配进 chain。
- [ ] 跑绿 → PASS。
- [ ] 提交：`feat(gateway): add idempotent response replay middleware`

**验收 gate：** `make validate-gateway-idempotency`（包装上述测试）。

---

### R-P0-3 · 每调用超时 + resilience 包骨架

**目标：** 建立 `pkg/adapters/resilience`（先只实现 `Timeout`），给 MinIO/Milvus/K8s REST client 的每次外部调用注入可配 deadline。

**Files：**
- Create: `pkg/adapters/resilience/resilience.go`、`pkg/adapters/resilience/resilience_test.go`
- Modify: `pkg/adapters/objectstore/minio_store.go`、`pkg/adapters/vectorstore/milvus_store.go`、`pkg/adapters/runtime/kubernetes_rest_client.go`

**契约影响：** 无。

**实现要点：**
- 先实现 §3 中 `Do` 的 Timeout 分支（`context.WithTimeout` 包裹 fn），`MaxAttempts<=1` 时不重试，`BreakerName==""` 时不断路——保证本批最小。
- **最高杠杆点：包裹 `kubernetes_rest_client.go:do()` 一处，network/storage/k8s/gpu 四个真实 provider 同时获得超时**（见 §0.2）。minio/milvus 各自调用点再包一次。
- 现成参考：`services/ani-gateway/internal/middleware/auth_client.go` 已用 `context.WithTimeout(ctx, c.timeout)` 模式，照此即可。
- **超时配置落点（F13）：gateway 无中央 config，超时值加在各 `gatewayXxxRuntimeConfigFromEnv()`（network/storage/k8s/gpu/object/vector runtime）里，并在各自构造 http.Client/REST client 时设 `Timeout` 或传入 `resilience.Policy`。** `main.go` 不变（它只调 `...FromEnv()`）。
- minio/milvus 的超时同理加在各自 runtime config + 调用点。

**任务步骤：**
- [ ] 写失败测试：`TestDoEnforcesTimeout`（fn 阻塞超过 Timeout → 返回 deadline 错误）。
- [ ] 跑红 → FAIL。
- [ ] 实现 `resilience.Do` Timeout 分支 + `Retryable` 桩（先恒 false）。
- [ ] 跑绿 → PASS。
- [ ] 给三个 adapter 外部调用点接入 `Do`（保持各自单测绿）。
- [ ] 提交：`feat(adapters): add resilience timeout wrapper for external calls`

**验收 gate：** `go test ./pkg/adapters/resilience/...` + 三 adapter 既有单测。

---

### R-P0-4 · 数据面 readyz

**目标：** 给 ObjectStore / VectorStore / K8sClusterService 加只读 `Health(ctx)`，接入 readyz；后端不可用时 readyz 返回 503。

**Files：**
- Modify: `pkg/ports/object_store.go`、`pkg/ports/vector_store.go`、`pkg/ports/k8s_clusters.go`（接口加 `Health(ctx) error`）
- Modify: 对应 adapter（MinIO `BucketExists`/匿名 ping；Milvus health；K8s `/version`）
- Modify: `pkg/bootstrap/probes.go`（`dependencyProbeChecks` 追加数据面检查）、`pkg/bootstrap/deps.go`（注入）

**契约影响：** 无（readyz 是运维端点，非业务 API）。

**实现要点：**
- **Milvus 已有 `Health()`（`milvus_store.go:137`）→ 对 vector 只需"接线"到 readyz，不新增**；MinIO/K8s 需新增轻量 `Health()`（MinIO `BucketExists`/匿名 ping；K8s `/version`）。
- network/storage/gpu 走共享 K8s client，可复用 K8s `/version` 探测作为其后端健康信号，避免重复造。
- `Health` 必须轻量、带短超时（复用 R-P0-3 的 `resilience.Do` Timeout）。
- 数据面为「可选依赖」时，readyz 用 `degraded` 而非 `fail`（与 R-P1-6 协同；本批先全部计入 fail，降级策略在 R-P1-6 细化）。

**任务步骤：**
- [ ] 写失败测试：`TestReadyzReportsDataPlaneUnavailable`（mock ObjectStore.Health 返回错误 → readyz 503）。
- [ ] 跑红 → FAIL。
- [ ] ports 加 `Health`，adapter 实现，probes 接线。
- [ ] 跑绿 → PASS。
- [ ] 提交：`feat(bootstrap): wire data-plane health into readyz`

**验收 gate：** `go test ./pkg/bootstrap/ -run Probe`；真实环境侧新增 `make validate-readyz-dataplane-live-gate`（kill 后端 → readyz 503，见 §6 真实门禁）。

---

### R-P1-5 · 操作级重试 + 断路器

**目标：** 在 `resilience` 包补齐重试退避与断路器，套到 MinIO/Milvus/K8s/registry 的**幂等**调用。

**Files：**
- Modify: `pkg/adapters/resilience/resilience.go`（实现重试退避 + 断路器 + `Retryable`）、`resilience_test.go`
- Modify: 上述 adapter 的幂等调用点（传入带 `MaxAttempts`/`BreakerName` 的 Policy）

**契约影响：** 无。

**实现要点：**
- 重试：指数退避 + 抖动，仅当 `Retryable(err)` 且操作幂等；上限 `MaxAttempts`/`MaxBackoff`。
- 断路器：滚动窗口失败比 `>FailureRatio` 且样本 `>=MinRequests` → open；冷却 `CooldownPeriod` 后 half-open 探测。open 时返回 `ErrCircuitOpen`。
- **只给幂等操作配重试**（读、幂等 upsert）；非幂等写不重试，由 R-P0-2 幂等重放兜底。

**前置（F10，必须先做）：** 修 `kubernetes_rest_client.go:340-342` 的错误分类——非 2xx 不再一律 `ErrInvalid`，要按状态码区分可重试（5xx/429/网络错误 → 可重试语义）与不可重试（4xx）。否则 `Retryable()` 无判据。

**任务步骤：**
- [ ] 写失败测试：`TestKubernetesRESTErrorClassifiesRetryable`（503/网络错误→可重试；400→不可重试）。
- [ ] 实现错误分类修正，跑绿。
- [ ] 写失败测试：`TestDoRetriesTransientThenSucceeds`、`TestBreakerOpensAfterSustainedFailures`、`TestRetryableClassification`。
- [ ] 跑红 → FAIL。
- [ ] 实现重试退避 + 断路器 + `Retryable`。
- [ ] 跑绿 → PASS。
- [ ] adapter 幂等调用点升级 Policy。
- [ ] 提交：`feat(adapters): classify k8s rest errors and add retry/circuit-breaker`

**验收 gate：** `go test ./pkg/adapters/resilience/...`；真实门禁 `make validate-resilience-faultinjection-live-gate`（注入瞬时错→重试成功；持续错→断路 open）。

---

### R-P1-6 · 优雅降级策略

**目标：** 定义每个 adapter 的降级语义（强依赖挂=503，弱依赖挂=降级模式），在 readyz 与结构化错误体一致体现。

**Files：**
- Create: `pkg/adapters/resilience/degradation.go`（依赖等级声明表：strong/weak）
- Modify: `pkg/bootstrap/probes.go`（弱依赖 → `degraded`，强依赖 → `fail`）
- Modify: 相关 handler 错误映射（弱依赖不可用时返回明确降级语义错误码）

**契约影响：** 无（复用既有错误体；不新增错误语义）。

**实现要点：**
- 显式声明依赖等级：postgres/redis=strong；object/vector 视业务=weak（具体由 readyz 后端不可用时表现验证）。
- 降级表是数据而非分支散落，便于审计。

**任务步骤：**
- [ ] 写失败测试：`TestWeakDependencyDownYieldsDegradedNotFail`。
- [ ] 跑红 → FAIL。
- [ ] 实现依赖等级表 + readyz 映射。
- [ ] 跑绿 → PASS。
- [ ] 提交：`feat(resilience): add explicit dependency degradation policy`

**验收 gate：** `go test ./pkg/bootstrap/ -run Degrad`；真实门禁复用 R-P0-4 live gate（弱依赖 down → degraded 而非 503）。

---

### R-P2-7 · 多端点 / failover（installer 拓扑就绪后做）

**目标：** adapter 不假设单 endpoint；Redis 支持 Sentinel/Cluster，MinIO/Milvus 支持端点列表或 LB-VIP，PG 支持读副本/VIP。

**Files：**
- Modify: `pkg/bootstrap/redis.go`（`FailoverOptions`/`ClusterOptions`）、`pkg/adapters/objectstore/minio_store.go`、`pkg/adapters/vectorstore/milvus_store.go`、对应 config/bootstrap
- 协同：`installer/`（提供多副本/VIP/Sentinel 拓扑——installer 侧改动单列批次）

**契约影响：** 无。

**实现要点：**
- config 从单 endpoint 升级为「端点列表或 VIP」，向后兼容单值。
- failover 期间复用 R-P1-5 断路器，避免对挂掉端点持续打。
- **依赖真实 HA 拓扑**，否则只能本地多实例模拟，不能标 production failover ready。

**任务步骤：**
- [ ] 写失败测试：`TestRedisFailoverConfigParsesSentinel`、`TestMinIOAcceptsEndpointList`。
- [ ] 跑红 → FAIL。
- [ ] 实现多端点 config + 客户端装配。
- [ ] 跑绿 → PASS。
- [ ] 提交：`feat(adapters): support multi-endpoint failover config`

**验收 gate：** 真实门禁 `make validate-ha-failover-live-gate`（kill primary → 业务在断路 + failover 后恢复，evidence JSON）。**未跑通前不得标 failover production ready。**

---

## §6 真实环境门禁（CLAUDE.md §6.6 强制）

凡声称真实韧性的批次（R-P0-4、R-P1-5、R-P1-6、R-P2-7），必须有可复跑 live gate + 非敏感 evidence JSON：

| 批次 | live gate（命名约定） | 通过标准 |
|---|---|---|
| R-P0-4 | `validate-readyz-dataplane-live-gate` | kill 数据面后端 → readyz 503 / degraded，evidence 记录探测延迟与状态 |
| R-P1-5 | `validate-resilience-faultinjection-live-gate` | 瞬时错→重试成功；持续错→断路 open→冷却 half-open |
| R-P2-7 | `validate-ha-failover-live-gate` | kill primary → 业务恢复，无数据丢失 |

约束（与 Sprint 13 一致）：
- evidence **不得包含**凭据、服务器 IP 或完整内网端点。
- 真实写/故障注入前必须重新只读盘点并取得人工确认。
- local profile / 单测只能证明逻辑，**不能**标 production ready。

---

## §7 进入条件

1. Sprint 13 已收口（S01–S07 production-shaped passed，已归档）。
2. `make test`、`make validate-architecture`、`git diff --check` 在主工作区全绿。
3. 已核对 §0 前置事实仍成立。

## §8 退出标准（Sprint 14 Done）

- R-P0-1..4 全部完成并通过对应 gate（阶段一必达）。
- R-P1-5/6 完成并通过 fault-injection live gate（阶段二目标）。
- R-P2-7 视 installer 拓扑就绪情况：拓扑就绪则完成 live gate；未就绪则归档为「待拓扑」并明确不标 failover ready。
- 每个完成批次走 Feature batch 四件套闭环；新增 `make` gate 已登记到 Makefile `.PHONY`。

## §9 边界

- 本计划不是 Sprint 14 完成记录。
- 不新增 Services 业务逻辑，不改 `/api/v1/svc` 资源，不依赖外部产品定义。
- 不为「未来可能」预造抽象；`resilience` 包是唯一新增实体，且仅承载已验证为空白的能力。
- 凡未跑通真实 live gate 的韧性，只能标 local/logic verified，不得标 production ready。
