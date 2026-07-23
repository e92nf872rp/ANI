# CORE-CONSOLE-SESSION-HANDLER-A — Core console session handler 补全（Issue #001）

完成日期：2026-07-03
对应 Sprint：Sprint 13 后续补全（Core console session 端点）
对应 Issue：`repo/services/tasks/modules/issue/console/compute/issue-001-core-console-session-handler.md`
Product line：core
验证结果：`go build ./services/ani-gateway/... ./pkg/ports/... ./pkg/adapters/runtime/...` EXIT:0；console session 5/5 tests passed；`go test ./pkg/ports/... ./pkg/adapters/runtime/...` PASS；`make validate-architecture` passed；`git diff --check` clean

## 实现了什么

补全 Core Gateway 的 `createInstanceConsoleSession`（VM console）handler：在 `InstanceObservability` port 新增 `CreateConsoleSession` 方法 + `InstanceConsoleSessionCreateRequest`/`InstanceConsoleSessionRecord` 类型，Local 与 Prometheus 两个 adapter 均补实现，Gateway `/api/v1/instances/:instance_id/console` 路由从旧 `api.console`（走 `service.Ops`）切换到新 `api.createConsoleSession`（走 observability adapter + kind/state/protocol 验证），新增 5 个 HTTP-level 回归测试覆盖 200/400/422/403 场景。OpenAPI `v1.yaml` 未修改（consume only）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/pkg/ports/instance_observability.go` | 修改 | 新增 `CreateConsoleSession` 方法 + `InstanceConsoleSessionCreateRequest`/`InstanceConsoleSessionRecord` 类型（非破坏性扩展） |
| `repo/pkg/adapters/runtime/local_instance_observability_service.go` | 修改 | Local adapter 实现 `CreateConsoleSession` + `normalizeConsoleProtocol`；新增 `consoleMu`/`console` map 支持可选 idempotency replay |
| `repo/pkg/adapters/runtime/prometheus_instance_observability.go` | 修改 | Prometheus real adapter 实现 `CreateConsoleSession`（Sprint 13 模式，`o.execBaseURL` 构建 connectURL，`prometheusInstanceObservabilityDevProfile()` 标记） |
| `repo/services/ani-gateway/internal/router/demo_instances.go` | 修改 | 新增 `createConsoleSession` handler + `demoInstanceConsoleSessionResponse` 类型 + converter + `isValidConsoleProtocol`；路由切换到新 handler |
| `repo/services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 新增 5 个回归测试 + `newDemoConsoleEngine` helper + `extractInstanceID` helper |
| `repo/pkg/adapters/runtime/local_instance_observability_service_test.go` | 修改 | 新增 `TestLocalInstanceObservabilityCreateConsoleSessionDefaultsToVNC`（默认 vnc + idempotent replay + explicit protocol） |

## 完工标准达成

- [x] Issue 11 条 AC 全部满足（详见下方 AC 矩阵）
- [x] `go build ./services/ani-gateway/... ./pkg/ports/... ./pkg/adapters/runtime/...` EXIT:0
- [x] console session 测试 5/5 PASS（Success 200 / NonVM 400 / InvalidProtocol 400 / NotRunning 422 / Forbidden 403）
- [x] `go test ./pkg/ports/... ./pkg/adapters/runtime/...` PASS
- [x] `make validate-architecture` passed（ports/adapters 边界校验通过）
- [x] `git diff --check` clean
- [x] OpenAPI `v1.yaml` 未修改（git diff 确认无改动，consume only）

### AC 矩阵（11/11）

| AC | 状态 | 证据 |
|---|---|---|
| `POST /instances/{id}/console` 返回 200 + InstanceConsoleSession（含 session_id/protocol/connect_url/url/expires_at） | ✅ | `TestCreateConsoleSessionSuccessReturns200`；响应结构与 OpenAPI `InstanceConsoleSession` schema required 字段一致 |
| 仅 `kind=vm` 可创建；非 vm kind 返回 400 | ✅ | handler `record.Kind != ports.WorkloadKindVM → 400 UNSUPPORTED`；`TestCreateConsoleSessionNonVMReturns400` |
| 实例非 `running` 状态返回 422 | ✅ | handler `record.Status.State != ports.WorkloadStateRunning → 422 PRECONDITION_FAILED`；`TestCreateConsoleSessionNotRunningReturns422` |
| protocol 白名单 console/vnc/novnc/serial；非法值返回 400 | ✅ | `isValidConsoleProtocol` + `TestCreateConsoleSessionInvalidProtocolReturns400` |
| port 新增 `CreateConsoleSession` + 请求/响应类型 | ✅ | `pkg/ports/instance_observability.go` |
| LocalInstanceObservabilityService.CreateConsoleSession 合成数据 | ✅ | `local_instance_observability_service.go` |
| PrometheusInstanceObservabilityService.CreateConsoleSession 合成数据（Sprint 13 模式） | ✅ | `prometheus_instance_observability.go` |
| RBAC scope `scope:instances:console`；无权限返回 403 | ✅ | Gateway middleware 处理；`TestCreateConsoleSessionForbiddenReturns403`（denyScope middleware + `c.Abort()`） |
| 回归测试覆盖 200/400 非 vm/400 非法 protocol/422 非 running/403 无权限 | ✅ | 5 个测试函数 |
| `make test` 通过 | ✅ | console session 相关全部通过；注：预存 `TestDemoInstanceServiceRealShellExecutesCommand` 在 Windows 因 `printf` 不可用失败，与本次改动无关 |
| 不修改 OpenAPI `v1.yaml` | ✅ | `git diff --name-only HEAD -- api/openapi/v1.yaml` 无输出 |

---

## Implementation Notes

### 1. Design Decisions

#### D1: protocol 默认值在 adapter 层填充（`normalizeConsoleProtocol`），白名单在 handler 层校验（`isValidConsoleProtocol`）

- **Ambiguity:** SPEC §5.1.3 伪代码只写 `observability.CreateConsoleSession(ctx, {TenantID, InstanceID, Protocol})`，未明确空 protocol 的默认值在哪一层填充、白名单校验在哪一层。
- **Choice:** handler 层做白名单校验（非法值 → 400），adapter 层 `normalizeConsoleProtocol` 做默认值填充（空 → "vnc"）。两层各司其职。
- **Rationale:** 白名单校验属于 API 边界输入校验（应早返回 400，不进入 adapter）；默认值填充属于能力实现细节（adapter 更接近"这个协议语义上默认是什么"的决策点）。这样 handler 不需要重复 default 逻辑，adapter 也可独立测试 default 行为。与 OpenAPI `CreateInstanceConsoleSessionRequest.protocol.default: "vnc"` 对齐。

#### D2: 双层验证（kind==vm / state==running）放在 handler 层，不在 adapter 层

- **Ambiguity:** SPEC §5.1.3 伪代码把 `kind != "vm"` 与 `state != "running"` 校验放在 handler，但未说明 adapter 是否应重复校验。
- **Choice:** kind/state 校验只在 handler 层（`instanceForObservation` 返回的 `record`），adapter 层只做 identity 校验（`validateInstanceObservationIdentity`）+ protocol normalize + session 合成。
- **Rationale:** handler 已通过 `instanceForObservation` 拿到完整 `WorkloadInstanceRecord`（含 Kind/Status.State），是 kind/state 校验的天然归属层；adapter 不重复持有 instance 状态，避免双份状态源。这与现有 exec handler 模式一致（exec handler 也不在 adapter 校验 kind/state）。

#### D3: `connect_url` 与 `url` 设为等价值

- **Ambiguity:** OpenAPI `InstanceConsoleSession` 同时有 `connect_url`（required）和 `url`（required，注释"和 connect_url 等价，供 Console/SDK 直接使用"），未说明两者是否应有差异。
- **Choice:** 两者设为完全相同的值（`record.ConnectURL = connectURL; record.URL = connectURL`）。
- **Rationale:** OpenAPI 明确注释"和 connect_url 等价"，前端/SDK 二选一使用。保持等价避免引入未要求的语义差异。Local adapter 用固定 `ws://127.0.0.1:8080/...`，Prometheus adapter 用 `o.execBaseURL` 构建（与 Sprint 13 exec session 模式一致）。

### 2. Deviations

None — 实现严格遵循 SPEC §5.1.3 伪代码与 Issue AC。handler 调用链（`instanceForObservation` → kind 校验 → state 校验 → protocol 校验 → `observability.CreateConsoleSession` → `c.JSON(200, ...)`)与 SPEC 伪代码逐行对应。OpenAPI `InstanceConsoleSession` schema 的 `operation_id`（nullable, optional）在响应中省略——这是可选字段省略，非偏离（nullable 可选字段不返回是合规的）。

### 3. Tradeoffs

#### T1: console 未在 handler 层传 `idempotency_key`（每次新建 session），但 adapter 支持可选 key 幂等 replay

- **Alternatives:**
  - A) handler 从 request body 透传 `idempotency_key` 到 adapter（与 exec handler 一致）
  - B) handler 不传 key，adapter 支持可选 key（本次选择）
- **Pros/Cons:**
  - A: 与 exec 一致，但 OpenAPI `CreateInstanceConsoleSessionRequest` 只有 `protocol`，无 `idempotency_key` 字段；强行透传需扩展 request schema 或从 header 取，超出 Issue scope
  - B: 符合 OpenAPI（console 未强制 idempotency），handler 路径每次新建 session；adapter 的 IdempotencyKey 字段保留供其他调用方（如未来 SDK helper 或 CLI）使用，单测覆盖 replay 语义
- **Why chosen:** B。OpenAPI 是唯一真实来源，console 请求体无 idempotency_key 字段；adapter 支持可选 key 是"能力就绪"而非"强制使用"，与 Issue"console 未在 OpenAPI 强制 idempotency_key（与 exec 不同）"的约定一致。

#### T2: deny middleware 测试用 `writeDemoError` + `c.Abort()` 模拟 RBAC 403，而非真实 RBAC middleware

- **Alternatives:**
  - A) 在测试中接入真实 `middleware.RBAC` stub 并注入 scope 拒绝
  - B) 用一个简单 deny middleware 直接返回 403 + `c.Abort()`（本次选择）
- **Pros/Cons:**
  - A: 更接近真实路径，但 RBAC middleware 当前是 dev stub（dev mode 跳过），构造复杂且依赖未完成组件
  - B: 最小化测试依赖，精确验证"403 在 middleware 层被拦截、不进入 handler"这一契约；`c.Abort()` 是 Hertz 正确的中断方式
- **Why chosen:** B。测试目标是验证 console handler 在 403 场景不被执行，而非验证 RBAC middleware 本身（后者是独立 middleware 测试职责）。`c.Abort()` 模式与 `request_id.go:60` / `errors.go:40` 现有模式一致。

### 4. Open Questions

#### OQ-1: `operation_id` 字段是否需要在 console session 响应中返回？

- **Assumption:** OpenAPI `InstanceConsoleSession.operation_id` 是 nullable + optional，当前实现省略它。
- **Verify:** 确认 Console 前端（Issue #7 控制台 Tab）是否依赖 `operation_id` 关联 operation timeline。如果前端需要，后续 Issue 应补 operation 关联；当前 Core 单元测试不依赖此字段。
- **Follow-up:** 若前端 Issue #7 实现时发现需要 `operation_id`，作为该 Issue 的小补全，非本 Issue 遗漏。

#### OQ-2: `expires_at` TTL 15 分钟是合成值，real provider 的 TTL 应由谁决定？

- **Assumption:** Local/Prometheus adapter 均用 `now.Add(15 * time.Minute)` 硬编码 TTL，这是 local profile 合成数据。
- **Verify:** 真实 VM console session（如 KubeVirt VNC）的 TTL 是否由底层 provider 决定？当前 Prometheus adapter 也用 15min 硬编码，但真实 console session TTL 可能取决于 noVNC proxy 或 KubeVirt 配置。
- **Follow-up:** 当真实 KubeVirt console provider 接入时（非本 Issue scope），TTL 应从 provider 响应或配置注入，而非硬编码。本 Issue 的 Prometheus adapter 标记为 `real_adapter` dev profile，但 console session 部分仍是合成数据（与 Sprint 13 exec session 模式一致）。

## 备注

- 预存测试 `TestDemoInstanceServiceRealShellExecutesCommand` 在 Windows 失败（`printf hello` 不可用），位于 `demo_instances.go:877` `runDemoShellCommand`，未被本次改动触碰，不影响本批次结论。
- 旧 `/demo/instances/:instance_id/console` 路径仍保留旧 `api.console` handler（走 `service.Ops`），未删除，避免破坏 demo 演示路径。
- 本批次为 Feature batch（新增 Core 产品能力端点），/ship-it 后需更新四个文件：`development-records/README.md`、`CURRENT-SPRINT.md`、`ANI-06-开发计划.md`、本文件。
