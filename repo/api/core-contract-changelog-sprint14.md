# ANI Core 接口契约影响说明 · Sprint 14

> 面向 **外部 Services 团队 / Core SDK 调用方 / 前端团队** 的接口契约影响同步文档。
> 目的：说明 Sprint 14 Core resilience 工作是否修改 `repo/api/openapi/v1.yaml` 与 `repo/api/openapi/services/v1.yaml`，以及虽未修改 OpenAPI 文件但会被客户端观察到的运行期语义变化。
> 结论基于真实 git diff、当前代码和门禁结果，不基于会话记忆。

---

## 0. 结论（30 秒速览）

| 契约文件 | 是否变更 | 结论 |
|---|---:|---|
| `repo/api/openapi/v1.yaml`（Core 对外 / 跨层控制面契约） | **否** | Sprint 14 未新增、删除或修改 Core OpenAPI path、operationId、schema、request/response 字段或枚举。 |
| `repo/api/openapi/services/v1.yaml`（Services 业务契约） | **否** | Sprint 14 未触碰 Services OpenAPI；Services 业务资源仍不回流 Core。 |

**对接结论：**

- 不需要因 Sprint 14 重新生成 Core/Services SDK。
- 不需要改 Services API client 的 request/response 类型。
- 需要理解三类运行期语义变化：Gateway 统一限流、Gateway 幂等响应重放、`/readyz` strong/weak dependency 降级。
- 这些运行期语义均建立在既有 Core 契约语义上：`idempotency_key`、`ErrorResponse`、`Conflict`、`RateLimitExceeded` 与 `/readyz` 已在 Sprint 14 前存在。

---

## 1. 审计范围与基线

本次核查范围是 PR #11 / Sprint14 合入前后：

| 项 | 值 |
|---|---|
| 合入前 `main` | `c3bcb61` |
| 合入后 `main` | `17a11dd` |
| Sprint14 分支尾提交 | `bcdcd6c` |
| PR | `#11 feature/sprint14-core-resilience-semantics -> main` |

核查命令：

```bash
git diff --stat c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml
git diff --name-status c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml repo/api
git diff --numstat c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml
```

核查结果：以上命令对两个 OpenAPI 契约文件均无输出；`repo/api/` 下没有 Sprint14 对 OpenAPI YAML 的修改。

---

## 2. OpenAPI 契约文件判定

### 2.1 Core OpenAPI：未变更

`repo/api/openapi/v1.yaml` 在 Sprint14 范围内没有文件 diff，因此以下内容均未改变：

- `paths`
- `operationId`
- request schema
- response schema
- enum
- component schema
- `servers[0].url`
- security scheme

Sprint14 代码没有新增 Core REST 资源，也没有把 resilience/failover 细节暴露成新的业务 API。

### 2.2 Services OpenAPI：未变更

`repo/api/openapi/services/v1.yaml` 在 Sprint14 范围内没有文件 diff。Sprint14 没有新增或修改 `models`、`inference-services`、`knowledge-bases` 等 Services 业务资源。

这符合仓库规则：ANI Services 已冻结并移交外部产品团队，本仓库 Sprint14 只做 Core resilience。

---

## 3. 运行期可见语义影响（非 OpenAPI 文件变更）

Sprint14 虽未修改 OpenAPI YAML，但改变了 Gateway 和 probe 的运行期行为。调用方不需要改类型，但需要正确处理以下状态。

### 3.1 Gateway 统一限流：可能返回 429

Sprint14 新增 `RateLimit(store)` middleware，按 tenant + method + route class 做窗口计数。

实现位置：

- `repo/services/ani-gateway/internal/middleware/ratelimit.go`
- `repo/services/ani-gateway/internal/middleware/chain.go`

对客户端可见行为：

| 场景 | HTTP | code | 说明 |
|---|---:|---|---|
| 超出租户路由窗口限额 | `429` | `RATE_LIMIT_EXCEEDED` | 请求被限流，客户端应退避重试。 |
| 限流共享 store 不可用 | `503` | `RATE_LIMIT_UNAVAILABLE` | Gateway 无法确认限流状态，按失败处理。 |

契约关系：

- `repo/api/openapi/v1.yaml` 在 Sprint14 前已存在公共响应组件 `RateLimitExceeded`，描述 `429` / `RATE_LIMIT_EXCEEDED`。
- Sprint14 未把 `429` 逐个追加到每个 operation 的 response 列表，因此严格依赖 per-operation response union 的 SDK/前端仍应按通用非 2xx `ErrorResponse` 处理。

对接建议：

- Services/前端调用 Core API 时，必须把 `429` 作为可重试/退避错误处理。
- 不要把 `429` 当作业务参数错误；它是 Gateway 策略层拒绝。

### 3.2 Gateway 幂等响应重放：重复 mutating 请求不再进入 handler

Sprint14 新增 `Idempotency(store)` middleware，对 `POST`、`PUT`、`PATCH`、`DELETE` 等 mutating 请求统一处理 `idempotency_key`。

实现位置：

- `repo/services/ani-gateway/internal/middleware/idempotency.go`
- `repo/services/ani-gateway/internal/middleware/chain.go`

对客户端可见行为：

| 场景 | HTTP | code/header | 说明 |
|---|---:|---|---|
| 首次请求完成后，同一 tenant/method/path/idempotency_key 重放 | 与首次响应一致 | `Idempotent-Replay: true` | Gateway 直接回放首次响应体。 |
| 首次请求仍在处理中，重复请求到达 | `409` | `IDEMPOTENCY_IN_PROGRESS` | 客户端应等待后重试同一 key。 |
| 幂等共享 store 不可用 | `503` | `IDEMPOTENCY_UNAVAILABLE` | Gateway 无法保证幂等语义，按失败处理。 |

契约关系：

- Core OpenAPI 在 Sprint14 前已经要求有副作用请求携带 `idempotency_key`。
- `Conflict` / `409` 与 `ErrorResponse` 已存在。
- Sprint14 没有修改 request schema；它把原本分散在 handler/service 的幂等约束推进到 Gateway 中间件层。

对接建议：

- 客户端重试必须复用同一个 `idempotency_key`。
- 遇到 `IDEMPOTENCY_IN_PROGRESS` 时，不应换 key 立即重提同一业务动作，否则可能制造重复业务意图。
- 如果收到 `Idempotent-Replay: true`，调用方可把响应视为第一次请求的最终结果。

### 3.3 `/readyz` strong/weak dependency 降级：弱依赖失败可返回 200 + degraded

Sprint14 为 bootstrap probe 增加数据面依赖检查，并引入 strong/weak dependency 映射。

实现位置：

- `repo/pkg/bootstrap/probes.go`
- `repo/pkg/adapters/resilience/degradation.go`
- `repo/pkg/ports/health.go`

对探针调用方可见行为：

| 场景 | HTTP | body.status | 说明 |
|---|---:|---|---|
| strong dependency 失败，例如 postgres / nats / redis / kubernetes-api | `503` | `fail` | 不可接流量。 |
| weak dependency 失败，例如 object-store / vector-store | `200` | `degraded` | 进程仍可接流量，但能力降级。 |
| 所有已启用依赖正常 | `200` | `ok` | 可接流量。 |

契约关系：

- `repo/api/openapi/v1.yaml` 在 Sprint14 前已经包含 `/readyz`，且 `status` enum 包含 `ok` 和 `degraded`。
- Sprint14 没有修改 `/readyz` schema；它补齐了真实依赖检查和降级语义。

对接建议：

- K8s readiness 可继续按 HTTP status 判断摘流：`503` 摘除，`200` 保留。
- 运维/UI 若展示健康详情，应读取 `checks.*.status`，区分 `degraded` 与 `fail`。
- 不应把 `degraded` 解读为 full platform production ready；它只说明 Gateway 仍能服务强依赖路径。

---

## 4. 对 SDK / Mock / 前端的影响

| 对象 | 是否需要变更 | 说明 |
|---|---:|---|
| Core SDK schema 生成 | 否 | `repo/api/openapi/v1.yaml` 未变更。 |
| Services SDK schema 生成 | 否 | `repo/api/openapi/services/v1.yaml` 未变更。 |
| Core Mock Server 契约 | 否 | 无新增 path/schema；若 mock 覆盖 429/409/readyz degraded，可作为测试增强，不是契约变更。 |
| Console / Services 前端 | 类型无需变更 | 需要在通用 request/error 层处理 `429`、`IDEMPOTENCY_IN_PROGRESS`、`Idempotent-Replay` 与 `/readyz degraded`。 |
| 外部 Services 后端 | 类型无需变更 | 调 Core mutating API 时继续遵守同一 `idempotency_key` 重试规则。 |

---

## 5. 兼容性判定

| 项 | 判定 |
|---|---|
| 删除 path / operationId | 无 |
| 修改 HTTP method | 无 |
| 修改 request 字段 | 无 |
| 修改 response schema | 无 |
| 修改 enum | 无 |
| 修改 Services 业务契约 | 无 |
| 新增客户端必须处理的运行期错误状态 | 有：`429 RATE_LIMIT_EXCEEDED`、`409 IDEMPOTENCY_IN_PROGRESS`、`503 *_UNAVAILABLE` |
| 是否破坏 OpenAPI 类型兼容性 | 否 |

**最终判定：** Sprint14 对 OpenAPI 文件是零变更；对运行期行为是兼容性增强。严格类型层无需更新，错误处理层应按已有 `ErrorResponse` 统一处理新增可见错误码。

---

## 6. 与 Sprint12-13 changelog 的关系

Sprint12-13 的契约变更说明见：

- `repo/api/core-contract-changelog-sprint12-13.md`

二者差异：

| 文档 | 核心结论 |
|---|---|
| Sprint12-13 changelog | Sprint12 修改 Core OpenAPI，新增 19 个 operationId，并有 ID 类型放宽、卷快照异步化等对接动作。Sprint13 不改契约。 |
| 本文 Sprint14 changelog | Sprint14 不改 Core/Services OpenAPI，只补运行期 resilience 语义。 |

---

## 7. 复核证据

文件级证据：

- `git diff --stat c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml`：无输出。
- `git diff --numstat c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml`：无输出。
- `git diff --name-status c3bcb61..17a11dd -- repo/api/openapi/v1.yaml repo/api/openapi/services/v1.yaml repo/api`：无 OpenAPI YAML 变更。

代码级影响证据：

- Gateway middleware chain：`repo/services/ani-gateway/internal/middleware/chain.go`
- Rate limit：`repo/services/ani-gateway/internal/middleware/ratelimit.go`
- Idempotency replay：`repo/services/ani-gateway/internal/middleware/idempotency.go`
- Readyz dependency semantics：`repo/pkg/bootstrap/probes.go`
- Dependency mode table：`repo/pkg/adapters/resilience/degradation.go`

验证门禁：

```bash
make validate-sprint14-resilience-live-gate
make validate-architecture
make test
git diff --check
```

---

## Machine-readable summary

```yaml
contract_change_report:
  scope: "ANI Core Sprint 14"
  generated_for: "external Services team / Core SDK / frontend contract sync"
  baseline:
    before_main: "c3bcb61"
    after_main: "17a11dd"
    sprint14_head: "bcdcd6c"
  files:
    core_openapi:
      path: repo/api/openapi/v1.yaml
      changed: false
      diff_range: "c3bcb61..17a11dd"
      required_sdk_regeneration: false
    services_openapi:
      path: repo/api/openapi/services/v1.yaml
      changed: false
      diff_range: "c3bcb61..17a11dd"
      required_sdk_regeneration: false
  openapi_contract_changes:
    paths_added: []
    paths_removed: []
    operations_added: []
    operations_removed: []
    request_schema_changes: []
    response_schema_changes: []
    enum_changes: []
  runtime_semantics:
    - id: gateway-rate-limit
      http_status: 429
      code: RATE_LIMIT_EXCEEDED
      compatibility: "existing ErrorResponse / RateLimitExceeded semantics; no OpenAPI file change"
      client_action: "treat as retryable with backoff"
    - id: gateway-idempotency-in-progress
      http_status: 409
      code: IDEMPOTENCY_IN_PROGRESS
      compatibility: "existing Conflict/ErrorResponse semantics; no request schema change"
      client_action: "retry later with the same idempotency_key"
    - id: gateway-idempotency-replay
      header: Idempotent-Replay
      value: "true"
      compatibility: "response replay behavior; no schema change"
      client_action: "treat replayed body as original operation result"
    - id: readyz-degraded
      endpoint: /readyz
      http_status: 200
      body_status: degraded
      compatibility: "status enum already existed before Sprint14"
      client_action: "distinguish degraded from ok/fail in observability"
  breaking_change: false
```
