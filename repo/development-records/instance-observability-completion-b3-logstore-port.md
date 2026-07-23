# instance-observability-completion B-3 — LogStore port 抽象新增

完成日期：2026-07-21
对应 Sprint：Sprint 15（Console Instance Observability Completion，第二轨 12-issue 计划）
批次：B-3（日志持久化链路 port 抽象基础，US-004 / FR-5）
对应 Issue：issue-004-logstore-port
对应 PRD US：US-004 / FR-5
对应 SPEC：§3.1 ports.LogStore interface（US-004）
对应 UX：N/A（后端 port 抽象）
验证结果：`go build ./pkg/ports/...` EXIT 0；`go vet ./pkg/ports/...` EXIT 0；`go test ./pkg/ports/...` EXIT 0（port 抽象无逻辑，无单测文件）；`make validate-architecture` passed（component import guard passed）；`gofmt -l pkg/ports/log_store.go` 无输出；`git diff --check` EXIT 0

## 实现了什么

新增 `repo/pkg/ports/log_store.go`（44 行），定义日志持久化存储的 port 抽象：

- `LogQueryRequest` 结构体：入参含 `TenantID`、`InstanceID`、`Namespace`、`Limit`、`Cursor`、`Level` 六个字段，完全对齐 Issue AC 第 2 条与 SPEC §3.1 line 251-258
- `LogQueryResult` 结构体：出参含 `Items []InstanceLogEntry`、`NextCursor string`
- `LogStore` interface：单方法 `QueryLogs(ctx context.Context, req LogQueryRequest) (LogQueryResult, error)`

本 Issue 是日志持久化链路的 port 抽象基础，不实现任何 adapter（LokiLogStore 属于 issue-005 范围）、不修改 `InstanceObservability` interface、不触碰 handler/SDK/部署 yaml。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/pkg/ports/log_store.go` | 新增 | LogStore interface + LogQueryRequest/LogQueryResult 结构（44 行） |
| `repo/development-records/instance-observability-completion-b3-logstore-port.md` | 新增 | 本笔记文件 |

## Implementation Notes

### 1. Design Decisions

#### D-1：复用现有 `InstanceLogEntry` 而非重新定义

- **歧义**：SPEC §3.1 line 268-274 的代码示例中定义了一个 `InstanceLogEntry` struct（`Timestamp string // RFC3339`），与现有 `pkg/ports/instance_observability.go:26-32` 已定义的 `InstanceLogEntry`（`Timestamp time.Time`）字段类型不一致。Issue AC 第 2 条只要求「出参含 `Items []InstanceLogEntry`」，未明确是复用还是重新定义。
- **选择**：复用现有 `InstanceLogEntry`（`time.Time` 版本），不在 `log_store.go` 重新定义。
- **理由**：
  1. 同 package 内重复定义 `InstanceLogEntry` 会导致 Go 编译错误（`InstanceLogEntry redeclared`）。
  2. 现有 `InstanceLogEntry` 已被 `local_instance_observability_service.go`、`prometheus_instance_observability.go` 等多个 adapter 使用，重新定义会破坏现有代码。
  3. Issue AC 第 4 条「不修改现有 `InstanceObservability` interface」隐含不破坏现有类型契约。
  4. SPEC §3.3 line 409 的 `LokiLogStore` 示例也用 `ports.InstanceLogEntry{...}` 构造，说明 SPEC 意图是复用 `ports.InstanceLogEntry`，§3.1 的 struct 定义是**说明性伪代码**，不是要求照搬。
  5. OpenAPI `v1.yaml:1682` 的 `InstanceLogEntry.timestamp` 是 `format: date-time`（RFC3339 字符串），现有 Go `time.Time` 在 Gateway handler 序列化时自动转为 RFC3339，此映射是现有代码已建立的模式，复用保持一致性。

#### D-2：`Cursor` opaque 语义仅靠注释约束，无类型层面强制

- **歧义**：Issue AC 第 3 条要求「`Cursor` 为 opaque string，port 层不约束其内部语义」，未明确是否需要定义专门的 `Cursor` 类型来强制 opaque 语义。
- **选择**：`Cursor` 字段使用纯 `string` 类型，仅在注释中说明「opaque string，port 层不约束内部语义，由 adapter 内部映射为 Loki time / ES search_after / K8s tailLines」。
- **理由**：
  1. 定义 `type Cursor string` 会增加类型复杂度但无法真正阻止 adapter 内部解析（adapter 仍可 `string(cursor)` 转换）。
  2. SPEC D-4/D-7 也采用纯 string + 注释约束方式。
  3. 符合 Karpathy 原则五「如无必要，勿增实体」——注释是表达 port 契约的充分方式。

#### D-3：`LogStore` interface 仅含 `QueryLogs` 单方法

- **歧义**：日志持久化存储通常还可能有写入、删除、TTL 管理等方法，SPEC 未明确 `LogStore` interface 的完整方法集。
- **选择**：`LogStore` interface 仅含 `QueryLogs` 一个方法。
- **理由**：
  1. Issue AC 和 SPEC §3.1 只要求 `QueryLogs` 方法。
  2. 日志写入由 Fluent Bit DaemonSet 采集后直接推送到 Loki，不经 `LogStore` port（SPEC §2.3.2 line 162-166）。
  3. 删除/TTL 管理属于 Loki 运维范畴，不是 ANI Core 产品能力。
  4. 符合 Karpathy 原则二「用能解决问题的最小代码」——不实现未被要求的功能。

### 2. Deviations

None — 实现严格遵循 Issue AC 和 SPEC §3.1 的契约要求。唯一需要说明的是 D-1 中复用现有 `InstanceLogEntry` 而非照搬 SPEC §3.1 示例的 struct 定义，这是为避免编译冲突的必要技术决策，不构成对 SPEC 意图的偏离（SPEC §3.3 的 LokiLogStore 示例已证实复用意图）。

### 3. Tradeoffs

#### T-1：port 抽象 vs 直接 Loki client 调用

- **备选方案 A**：在 adapter 中直接调用 Loki client，不引入 `LogStore` port 抽象。
  - 优点：减少一层抽象，代码更直接。
  - 缺点：无法支持后续 Elasticsearch 等多后端切换；`PrometheusInstanceObservability.ListLogs` 与 Loki 强耦合；不符合 CLAUDE.md §5.2「承载 ANI 产品能力、存在合理替换/多实现可能的组件必须经过 ports/adapters」。
- **备选方案 B**（采用）：引入 `LogStore` port 抽象，adapter 实现 `LokiLogStore`。
  - 优点：支持多后端（Loki/ES/K8s fallback）；`PrometheusInstanceObservability` 持有可选 `logStore` 字段，未配置时 fallback 到 K8s API，零回归（SPEC D-5）；符合 ports/adapters 架构边界。
  - 缺点：多一层抽象，但对单方法 interface 而言成本极低。
- **胜出原因**：SPEC D-4 明确要求 port 抽象；CLAUDE.md §5.2 强制要求多实现可能的组件经 ports/adapters；未来 ES 后端是 PRD 规划路径。

#### T-2：`Cursor` 纯 string vs 专用类型

- **备选方案 A**：定义 `type Cursor string` 并提供 `String()`/`IsEmpty()` 方法。
  - 优点：类型层面更显式表达 opaque 语义。
  - 缺点：无法真正阻止 adapter 解析；增加 API 表面；调用方需要额外转换。
- **备选方案 B**（采用）：纯 `string` + 注释约束。
  - 优点：最小复杂度；与 `InstanceObservationListRequest.Cursor`（现有 `string` 类型）保持一致；adapter 可直接使用。
  - 缺点：opaque 语义仅靠注释，无类型强制。
- **胜出原因**：与现有 `InstanceObservationListRequest.Cursor` 一致；避免过度设计；注释约束是 Go 生态常见模式。

### 4. Open Questions

#### OQ-1：`Level` 字段的过滤语义在 adapter 层是否必须实现

- **假设**：SPEC §3.1 line 257 注释「adapter 可选实现」，即 adapter 可以忽略 `Level` 过滤返回全部日志，也可以实现服务端过滤。
- **待用户确认**：`LokiLogStore`（issue-005）实现 `Level` 过滤时，应使用 LogQL `| json | level="<level>"` 管道过滤（服务端），还是返回全量后客户端过滤？前者性能更好但依赖日志行是 JSON 格式且含 `level` 字段；后者简单但浪费带宽。
- **可能后续**：issue-005 LokiLogStore 实现时决策。

#### OQ-2：`Namespace` 字段是否冗余

- **假设**：`Namespace` 可从 `TenantID` 推导（格式 `ani-tenant-<tenant_id>`，SPEC D-6 line 44），似乎冗余。
- **待用户确认**：保留 `Namespace` 字段是否为有意设计（例如未来支持非标准 namespace 映射），还是应移除以减少冗余？当前实现保留该字段以对齐 Issue AC 第 2 条明确要求的入参字段列表。
- **可能后续**：若确认冗余，可在后续 Issue 中移除，但本 Issue 严格按 AC 要求保留。

#### OQ-3：`Limit` 为 0 时的默认值语义

- **假设**：`Limit` 为 0 时 adapter 应使用默认值（如 100），而非返回空结果。SPEC 未明确 `Limit=0` 的语义。
- **待用户确认**：adapter 是否应将 `Limit=0` 视为「使用默认值」，还是视为「无限制」？前者更安全，后者可能带来性能风险。
- **可能后续**：issue-005 LokiLogStore 实现时决策，或在 SPEC 补充说明。

## 验证命令

```bash
cd repo
gofmt -l pkg/ports/log_store.go                    # 无输出（格式合规）
go build ./pkg/ports/...                            # EXIT 0
go vet ./pkg/ports/...                              # EXIT 0
go test ./pkg/ports/...                             # EXIT 0（no test files，port 抽象无逻辑）
make validate-architecture                          # EXIT 0（component import guard passed）
git diff --check                                    # EXIT 0
```

## AC 满足情况（6/6）

- [x] 新增文件 `repo/pkg/ports/log_store.go`，定义 `LogStore` interface、`LogQueryRequest`、`LogQueryResult` 结构
- [x] `LogStore.QueryLogs(ctx, req)` 方法签名：入参含 `TenantID`/`InstanceID`/`Namespace`/`Limit`/`Cursor`/`Level`；出参含 `Items []InstanceLogEntry`/`NextCursor string`
- [x] `Cursor` 为 opaque string，port 层不约束内部语义（注释说明 adapter 内部映射）
- [x] 不修改现有 `InstanceObservability` interface（仅新增独立 port 文件）
- [x] Typecheck/lint passes（`go vet` + `go build` + `gofmt`）
- [x] `make validate-architecture` 通过

## 后续依赖

本 Issue 是 B-3 日志持久化链路的 port 抽象基础，后续 Issue 依赖此 port：

- issue-005（LokiLogStore adapter）：实现 `LogStore` interface，走 Loki HTTP API
- issue-006（PrometheusInstanceObservability 扩展）：新增 `logStore` 可选字段 + `SetLogStore` 方法 + fallback 逻辑（SPEC §3.2）
- issue-007（runtime 环境变量注入）：`INSTANCE_OBSERVABILITY_LOG_STORE` 环境变量处理（SPEC §3.5）
