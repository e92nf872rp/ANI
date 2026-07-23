# instance-observability-completion B-3 — PrometheusInstanceObservability 注入 LogStore（带 fallback）

完成日期：2026-07-21
对应 Sprint：Sprint 15（Console Instance Observability Completion，第二轨 12-issue 计划）
批次：B-3（日志持久化链路 LogStore 注入，US-006 / FR-6 / FR-7 / FR-8）
对应 Issue：issue-006-logstore-injection
对应 PRD US：US-006 / FR-6 / FR-7 / FR-8
对应 SPEC：§3.2 PrometheusInstanceObservability 扩展 + §5.3 ListLogs fallback + §5.4 runtime 注入（US-006）
对应 UX：§3.3 Primary Flow — 用户查看日志（Loki 部署后）+ §6.4 日志 Tab 状态（Loki 适配后）
验证结果：`go vet ./pkg/adapters/runtime/... ./services/ani-gateway/...` EXIT 0；`gofmt -l` 4 文件全通过（review-it 修复 3 个文件格式化）；`go test -count=1 ./pkg/adapters/runtime/... ./services/ani-gateway/...` EXIT 0（runtime 1.500s + ani-gateway 1.046s + middleware 1.016s + router 1.107s）；`make test` EXIT 0（全量）；`make validate-architecture` EXIT 0（component import guard passed / architecture guardrails valid）；`git diff --check` EXIT 0（仅 CRLF 警告，无空白错误）

## 实现了什么

为 `PrometheusInstanceObservability` 新增可选 `logStore ports.LogStore` 字段 + `SetLogStore` 方法，修改 ani-gateway runtime 根据环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现（`loki` → LokiLogStore；`elasticsearch` / 未知值 → 走 fallback；`k8s` / 空 / `not_configured` → nil）。`ListLogs` 逻辑改为：`logStore != nil` 时调 `logStore.QueryLogs`（走持久化存储路径），nil 时 fallback 到现有 K8s pod log API（零回归）。未配置环境变量时行为与现状完全一致。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/pkg/adapters/runtime/prometheus_instance_observability.go` | 修改 | 新增 `logStore ports.LogStore` 字段 + `SetLogStore` 方法；`ListLogs` 改为 fallback 分发；抽取 `listLogsFromK8sAPI` + `listLogsFromLogStore`（+58/-6 行） |
| `repo/services/ani-gateway/instance_observability_runtime.go` | 修改 | config 新增 `LogStoreType`/`LokiURL` 字段 + 环境变量加载；新增 `buildLogStore` 按枚举值选择实现；`newGatewayInstanceObservability` 在 `prometheus_kubernetes` 分支注入 LogStore（+51 行） |
| `repo/pkg/adapters/runtime/prometheus_instance_observability_test.go` | 修改 | 新增 3 个测试：fallback 到 K8s API、注入 LogStore 路径、LogStore 错误透传；新增 `fakeLogStore` 测试桩（+296 行） |
| `repo/services/ani-gateway/instance_observability_runtime_test.go` | 修改 | 新增 7 个测试：`buildLogStore` 各分支（空/k8s/loki/custom URL/elasticsearch/未知值）+ 完整 runtime 工厂注入 + 环境变量加载（+111 行） |
| `repo/development-records/instance-observability-completion-b3-logstore-injection.md` | 新增 | 本笔记文件 |

## 完工标准达成

- [x] `PrometheusInstanceObservability` 结构体新增 `logStore ports.LogStore` 字段（可选，nil 时 fallback）
- [x] 新增 `SetLogStore(store ports.LogStore)` 方法，由 runtime 在创建时调用
- [x] 修改 `instance_observability_runtime.go`，按 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现：`loki` → LokiLogStore；`elasticsearch` → 暂未实现走 fallback；空/`k8s`/`not_configured` → nil
- [x] `ListLogs` 逻辑：`logStore != nil` 调 `logStore.QueryLogs`，nil 时 fallback 到 K8s pod log API
- [x] 未设置 `INSTANCE_OBSERVABILITY_LOG_STORE` 时行为与现状完全一致（零回归）
- [x] Typecheck/lint passes（`go vet` + `gofmt -l` 全通过）
- [x] 单元测试覆盖 fallback 路径和注入路径（adapter 3 个 + runtime 7 个）
- [x] `make test` 全通
- [x] `make validate-architecture` 通过
- [x] `git diff --check` 通过

## Implementation Notes

### 1. Design Decisions

#### D-1：`logStore` 作为 adapter 私有可选字段，不暴露到 `InstanceObservability` interface

- **歧义**：SPEC §3.2 要求 `PrometheusInstanceObservability` 持有可选 `logStore` 字段，但未明确该字段是否应暴露到 `ports.InstanceObservability` interface。
- **选择**：`logStore` 作为 `PrometheusInstanceObservability` 的私有字段，`SetLogStore` 是具体类型方法（非 interface 方法），不在 `ports.InstanceObservability` interface 声明。
- **理由**：
  1. **对齐 port 定义**：`ports.LogStore` 注释明确「LogStore 是内部组合能力，不对外暴露到 InstanceObservability interface，由 adapter 持有可选字段并决定 fallback 行为」（[log_store.go:39-40](file:///e:/go/project/ANI/repo/pkg/ports/log_store.go#L39-L40)）。这是 #4 issue 已确立的边界。
  2. **保持 interface 最小化**：`InstanceObservability` 已有 7 个方法，LogStore 注入是 adapter 内部实现细节，不应泄漏到 port 契约。runtime 通过类型断言 `*PrometheusInstanceObservability` 调用 `SetLogStore`（测试已覆盖）。
  3. **零回归保证**：其他 `InstanceObservability` 实现（如 local service）无需感知 LogStore，未注入时行为不变。

#### D-2：`buildLogStore` 对未知值走 fallback 而非报错

- **歧义**：SPEC §5.4 line 703 示例代码对未知 `INSTANCE_OBSERVABILITY_LOG_STORE` 值记录 warn 日志并走 fallback，但 Issue AC 第 3 条只显式列出 `loki`/`elasticsearch`/空/`k8s`/`not_configured` 的处理，未明确未知值是否应报错。
- **选择**：未知值走 fallback，记录 `slog.Warn` 日志，不阻塞启动。
- **理由**：
  1. **运维友好**：LogStore 是可选增强能力，配置错误不应导致 Gateway 启动失败而完全无日志可查。fallback 到 K8s API 保证基础可观测性。
  2. **对齐 SPEC 示例**：SPEC §5.4 line 703 `default:` 分支即为 `log.Warn(...) + fallback`，本实现与其一致。
  3. **可观测**：`slog.Warn` 带 `"value"` 字段记录具体未知值，运维可通过日志定位配置错误。

#### D-3：`listLogsFromLogStore` 透传 `Limit` 不在 adapter 外层裁剪

- **歧义**：K8s 路径用 `normalizeLimit(request.Limit, 100, 1000)` 做边界裁剪，LogStore 路径是否应在外层同样裁剪？
- **选择**：`listLogsFromLogStore` 直接透传 `request.Limit` 到 `LogQueryRequest.Limit`，不在外层裁剪。
- **理由**：
  1. **分层职责清晰**：`LogQueryRequest.Limit` 语义由 adapter（Loki）自行处理上限。Loki adapter 内部 `loki_log_store.go:98` 已调 `normalizeLimit(req.Limit, 100, 1000)`，外层再裁剪属重复逻辑。
  2. **port 层不约束**：`LogQueryRequest` 注释明确「Limit 单页条数上限」，port 层不绑定存储后端语义，由 adapter 内部映射。
  3. **避免双重裁剪**：若外层裁剪到 1000 后 Loki 再裁剪到 1000，无问题但冗余；若未来 ES adapter 有不同上限，外层裁剪会破坏其语义。

### 2. Deviations

None — 实现完全遵循 SPEC §3.2 / §5.3 / §5.4 草案。

- 默认 Loki URL `http://ani-loki.ani-s07-observability:3100` 与 SPEC §5.4 line 694 / §929-930 一致
- `elasticsearch` → 走 fallback 与 SPEC §5.4 line 697-699 / §948 一致
- 未知值 → warn + fallback 与 SPEC §5.4 line 703 / §949 一致
- `ListLogs` fallback 分发逻辑与 SPEC §5.3 line 88-100 一致
- `SetLogStore` 由 runtime 在创建时调用与 SPEC §5.4 line 696 一致

### 3. Tradeoffs

#### T-1：错误双层包装 `logStore query failed: loki query failed: ...`

- **备选方案 A**（采用）：`listLogsFromLogStore` 包装为 `"logStore query failed: %w"`，Loki adapter 内部再包装 `"loki query failed: %w"`，最终错误如 `logStore query failed: loki query failed: connection refused`。
- **备选方案 B**：`listLogsFromLogStore` 直接透传 LogStore 错误不包装，最终错误如 `loki query failed: connection refused`。
- **取舍**：
  - A 优点：外层标识「注入路径」错误来源，handler 层可据 `logStore query failed` 前缀映射到 `LOG_STORE_UNAVAILABLE` 错误码（SPEC §5.2 §963）；未来 ES adapter 错误也会被这层捕获，错误码映射统一。
  - A 缺点：错误消息稍长，有轻微重复（`logStore query failed: loki query failed`）。
  - B 优点：错误消息简洁。
  - B 缺点：handler 层需嗅探内层错误字符串判断来源，耦合具体 adapter 实现；未来加 ES adapter 后无法统一区分「LogStore 路径错误」vs「其他错误」。
- **结论**：选 A。外层包装帮助 handler 层统一错误码映射，符合 SPEC §5.2 错误码设计意图。

#### T-2：`TestNewGatewayInstanceObservabilityInjectsLokiWhenConfigured` 仅断言类型而非行为

- **备选方案 A**（采用）：测试断言返回的 observability 是 `*PrometheusInstanceObservability` 类型（可注入 LogStore 的唯一实现），但不直接断言 `logStore` 字段非 nil。
- **备选方案 B**：通过调用 `ListLogs` 观察是否走 Loki 路径来验证注入确实发生。
- **取舍**：
  - A 优点：测试聚焦 runtime 工厂装配逻辑，与 `TestBuildLogStoreInjectsLoki`（验证 `buildLogStore("loki")` 非 nil）形成逻辑链：`buildLogStore` 非 nil → 工厂 `store != nil → SetLogStore` → 返回类型正确。分层清晰。
  - A 缺点：未端到端验证注入副作用。
  - B 优点：端到端验证注入行为。
  - B 缺点：需 mock Loki HTTP 响应，测试复杂度高；与 adapter 层 `TestPrometheusInstanceObservabilityListLogsUsesInjectedLogStore` 重复，违背测试分层。
- **结论**：选 A。注入行为的端到端验证已由 adapter 测试 `TestPrometheusInstanceObservabilityListLogsUsesInjectedLogStore` 覆盖，runtime 测试只需验证工厂装配。

### 4. Open Questions

None — 实现遵循 SPEC 草案，所有决策点均有 SPEC 依据或明确理由。

#### OQ-1（信息性，非阻断）：handler 层错误码映射待后续批次实现

- **假设**：SPEC §5.2 定义了 `LOG_STORE_UNAVAILABLE` / `INVALID_CURSOR` / `LOKI_HTTP_ERROR` / `LOKI_DECODE_ERROR` 错误码，由 handler 层把 adapter 包装错误转 HTTP 错误码。本 Issue #006 只实现 adapter + runtime 注入，未触碰 handler 层错误码映射。
- **需用户验证**：handler 层错误码映射是否属于另一 issue 范围（如 B-6 或后续 Core handler 批次）？若是，本批次的 `logStore query failed:` 前缀已为 handler 层提供可识别的映射锚点，无需返工。

#### OQ-2（信息性）：`elasticsearch` 分支的 warn 日志在测试中未断言

- **假设**：`TestBuildLogStoreElasticsearchFallsBack` 验证返回 nil（fallback），但未断言 `slog.Warn` 被调用。
- **需用户验证**：是否需要捕获 `slog` 输出断言 warn 日志？当前判定：fallback 行为是 AC 关键，warn 日志是辅助可观测性，测试覆盖行为即可，不断言日志输出属合理简化。

## 备注

- **依赖确认**：#4（LogStore port）和 #5（Loki adapter）已实现，本 issue 直接消费 `ports.LogStore` 和 `runtimeadapter.NewLokiLogStore`，无新建 port/adapter。
- **diff 范围说明**：工作区中存在 B-1（handler 传 Kind）和 B-3（GPU DCGM 修正）的未提交改动，与本 issue #006 的 4 个文件改动共存于同一 diff。`/ship-it` 前建议先确认要包含的文件范围，避免混合提交。
- **review-it 修复**：review 过程发现 3 个文件 `gofmt -l` 报告需格式化（test 文件 import 块、struct 字段对齐），已 `gofmt -w` 修正并重测通过，无逻辑变更。
