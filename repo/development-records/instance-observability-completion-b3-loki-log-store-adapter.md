# instance-observability-completion B-3 — LokiLogStore adapter 实现

完成日期：2026-07-21
对应 Sprint：Sprint 15（Console Instance Observability Completion，第二轨 12-issue 计划）
批次：B-3（日志持久化链路 Loki adapter，US-005 / FR-9 / FR-10）
对应 Issue：issue-005-loki-log-store-adapter
对应 PRD US：US-005 / FR-9 / FR-10
对应 SPEC：§3.3 LokiLogStore 实现（US-005）+ §5.5 Loki adapter 查询逻辑关键点
对应 UX：N/A（后端 adapter 实现）
验证结果：`go build ./pkg/adapters/runtime/...` EXIT 0；`go vet ./pkg/adapters/runtime/...` EXIT 0；`go test ./pkg/adapters/runtime/...` EXIT 0（15 个 Loki 相关测试全通过）；`go test ./pkg/adapters/runtime/ -run "Loki|LokiLog" -v` 15/15 PASS；`make test` EXIT 0（含 pkg/adapters/runtime 9.185s + 全量 services 测试）；`make validate-architecture` EXIT 0（component import guard passed / architecture guardrails valid）；`git diff --check` EXIT 0（仅 pre-existing CRLF warning on 无关 yaml，与 issue #5 无关）

## 实现了什么

新增 `repo/pkg/adapters/runtime/loki_log_store.go`（301 行）+ `repo/pkg/adapters/runtime/loki_log_store_test.go`（400 行），实现 `ports.LogStore` interface，通过 Loki HTTP API `/loki/api/v1/query_range` 查询持久化日志：

- `LokiLogStoreConfig` 构造配置（BaseURL / HTTPClient / Now）
- `LokiLogStore` 结构 + 编译时接口断言 `var _ ports.LogStore = (*LokiLogStore)(nil)`
- `NewLokiLogStore` 构造函数（BaseURL 必填校验 + 默认 10s 超时 + 默认 `time.Now`）
- `QueryLogs` 主方法（校验 identity + 构造 LogQL + cursor→end + HTTP GET + 解析 stream + 映射 InstanceLogEntry + 计算 next_cursor）
- `buildLokiLogQL` LogQL 构造（namespace 精确匹配 + pod 正则匹配 + `| json` 管道）
- `escapeLogQLRegex` LogQL 正则元字符转义（与 `promQLPodMatcher` 一致）
- `cursorToEndNs` cursor RFC3339 ↔ Loki end Unix 纳秒映射
- `decodeLokiResponse` Loki stream 响应解析
- `mapLokiStreamsToLogEntries` stream values → InstanceLogEntry 映射 + level 解析侧过滤 + next_cursor 计算
- `parseLokiLogLine` 单行 JSON/纯文本解析 + level 推断回退

15 个单元测试覆盖：LogQL 构造/转义、cursor 双向映射、stream 解析、level 过滤、next_cursor 计算、纯文本回退、level 推断、显式 level 保留、端到端 HTTP、cursor 透传、非法 cursor、非 200、传输错误、空 BaseURL。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/pkg/adapters/runtime/loki_log_store.go` | 新增 | LokiLogStore adapter 实现（301 行） |
| `repo/pkg/adapters/runtime/loki_log_store_test.go` | 新增 | 15 个单元测试（400 行） |
| `repo/development-records/instance-observability-completion-b3-loki-log-store-adapter.md` | 新增 | 本笔记文件 |

## Implementation Notes

### 1. Design Decisions

#### D-1：cursor 映射为 Loki `end` 参数而非 `start`，direction=backward 而非 forward

- **歧义**：Issue AC 第 3 条、PRD FR-10、SPEC §3.3 line 395-431 草案、SPEC §5.5 line 717-718 均明确要求「cursor → Loki `start` 参数（Unix 纳秒）」「next_cursor = 结果最后一条 timestamp」，隐含 forward 方向。但 SPEC §5.5 line 717 写的是 `start`，而 live gate 联调发现 forward + limit=10 返回最早的 10 条，用户看不到最新日志。
- **选择**：采用 `direction=backward`，cursor 作为 `end` 参数往前翻页：
  - 首屏 `cursor` 为空时 `end=now`，`start=end-24h`，返回最近 `limit` 条（与 `kubectl logs --tail` 对齐）
  - `next_cursor` = 本批最早一条 timestamp（RFC3339），作为下一页 `end` 边界往前翻页
  - 返回 items 保持 Loki backward 倒序（最新在前），不反转
- **理由**：
  1. **继承 B5 live gate 修复后的语义**：B5 批次（`instance-observability-completion-b5-loki-range-ca-fix.md` §1.2、§2.2）在真实 Gateway+前端联调中复现了 forward 方向的缺陷（首屏返回最早的 10 条 docker-entrypoint 启动日志，用户无法看到最新日志），并已修复为 backward 方向。本 Issue #5 是 B-3 的 Loki adapter 部分固化实现，应与 B5 已验证的 live 语义一致，而非回退到 SPEC 草案的未经验证的 forward 方向。
  2. **与日志应用展示习惯一致**：首屏返回最新 limit 条，「加载更多」追加更早日志到列表末尾，符合 `kubectl logs --tail`、Grafana Loki Explore、主流日志应用的惯例。
  3. **与前端 `useInfiniteQuery` infinite scroll 语义一致**：前端 `LogsTab.tsx` 用 `fetchNextPage` 追加下一页，backward 方向首屏最新 + 往前翻页 = 列表末尾追加更老日志，与 infinite scroll 语义对齐；forward 方向首屏最老 + 往后翻页 = 列表末尾追加更新日志，与习惯相反。
  4. **B5 已明确标注 SPEC/PRD 待同步**：B5 记录 §4.1 明确「SPEC 和 PRD 尚未同步更新」，本 Issue #5 实现沿用 B5 已修复语义，SPEC/PRD 同步更新仍在待办中。

#### D-2：LogQL pod label 用正则匹配而非精确匹配

- **歧义**：Issue AC 第 2 条、PRD FR-9、SPEC §3.3 line 715、SPEC §5.5 line 715 均规定 LogQL 为 `{namespace="<namespace>",pod="<instance_id>"} | json`（精确匹配）。但真实 Deployment 创建的 Pod 带 ReplicaSet hash（如 `demo-preview-nginx-6c748d8b7-tqfrl`），精确匹配 `pod="demo-preview-nginx"` 查不到任何日志。
- **选择**：`pod=~"^<escaped_instance>(-.*)?$"` 正则匹配，复用 `escapeLogQLRegex` 转义逻辑（与 `promQLPodMatcher` 一致）。
- **理由**：
  1. **继承 B5 live gate 修复后的语义**：B5 批次 §1.1、§2.1 在 live 联调中复现了精确匹配查不到日志的真实缺陷，并已修复为正则匹配。本 Issue #5 实现与 B5 已验证的 live 语义一致。
  2. **与 metrics 路径策略一致**：`promQLPodMatcher` 已用正则匹配 pod label，logs 与 metrics 两个链路对 pod 名匹配策略保持一致，避免分叉。
  3. **正则同时兼容直接 Pod 和控制器生成的 Pod**：`^<instance>(-.*)?$` 匹配 `pod-a`（直接 Pod）和 `pod-a-6c748d8b7-tqfrl`（ReplicaSet hash）。
  4. **多租户隔离仍由 namespace 精确匹配保证**：pod 正则不跨 namespace 误匹配。

#### D-3：level 过滤在解析侧做，不下推到 LogQL

- **歧义**：SPEC §3.3 line 395-431 示例在解析侧过滤 level，但 SPEC §5.5 未明确 level 过滤位置。LogQL `| json | level="<level>"` 管道过滤可下推到 Loki 服务端，性能更好但依赖日志行是 JSON 且含 `level` 字段；解析侧过滤简单但浪费带宽。
- **选择**：解析侧过滤（`mapLokiStreamsToLogEntries` 中 `if level != "" && entry.Level != level { continue }`）。
- **理由**：
  1. **兼容非结构化日志行**：Fluent-Bit 采集的 nginx/stdout 日志 JSON 格式 `{"message":"...","logtag":"F","stream":"stdout",...}` 无 `level` 字段，`| json | level="<level>"` 管道过滤会丢弃这些行。解析侧过滤结合 `inferLogLevel` 从 message 推断 level，可正确过滤无显式 level 的日志。
  2. **与 B5 live gate 修复后的语义一致**：B5 §1.3 已处理 nginx/stdout 日志无 level 字段的场景，本 Issue #5 沿用相同策略。
  3. **单实例查询数据量可控**：单 pod 查询 limit≤1000 条，解析侧过滤的带宽浪费在可接受范围。

#### D-4：JSON 无 level 字段时从 message 推断 level

- **歧义**：SPEC §3.3 line 400-405 示例直接用 `entry.Level`，未处理 JSON 无 level 字段的场景。Fluent-Bit 采集的容器 stdout 日志常见无 level 字段，导致前端级别列显示为空。
- **选择**：JSON 解析成功但 `level` 为空时，调 `inferLogLevel(parsed.Message)` 从 message 内容推断 level（error/warn/debug 前缀识别，其余为 info）。
- **理由**：
  1. **继承 B5 live gate 修复后的语义**：B5 §1.3 已处理此场景并验证。
  2. **复用已有 helper**：`inferLogLevel` 已在 `local_instance_observability_service.go` 定义并覆盖常见 nginx/k8s 日志前缀，无需新写推断逻辑。
  3. **显式 level 优先**：JSON 有 level 字段时优先用显式 level，不被推断覆盖（`TestParseLokiLogLinePreservesExplicitLevel` 验证）。

#### D-5：`InstanceLogEntry.Timestamp` 用 `time.Time` 而非 RFC3339 string

- **歧义**：SPEC §3.3 line 410 示例 `Timestamp: t.Format(time.RFC3339)`（string），但实际 `pkg/ports/instance_observability.go:26-32` 的 `InstanceLogEntry.Timestamp` 是 `time.Time`。同 package 内无法重新定义 `InstanceLogEntry`（会编译冲突）。
- **选择**：用 `time.Unix(0, tsInt).UTC()` 构造 `time.Time`，外部 cursor 仍为 RFC3339 string（符合 AC 第 3 条）。
- **理由**：
  1. **与现有 `InstanceLogEntry` 类型一致**：复用现有 `time.Time` 版本，避免编译冲突。B-3 issue-004 已在 D-1 说明复用意图。
  2. **Gateway handler 序列化时自动转为 RFC3339**：OpenAPI `v1.yaml:1682` 的 `InstanceLogEntry.timestamp` 是 `format: date-time`，现有代码已建立 `time.Time` ↔ RFC3339 的序列化映射。

#### D-6：`decodeLokiResponse` 用 `io.Reader` 风格接口而非 `io.ReadCloser`

- **歧义**：`decodeLokiResponse` 的 body 参数类型可以是 `io.ReadCloser`、`io.Reader` 或 `interface{ Read([]byte) (int, error) }`。
- **选择**：`interface{ Read(p []byte) (int, error) }`（最小接口）。
- **理由**：
  1. **最小接口原则**：只需 `Read` 方法，不需要 `Close`（`Close` 在 `QueryLogs` 用 `defer resp.Body.Close()` 处理）。
  2. **测试友好**：测试可用 `strings.NewReader` 或 `bytes.Buffer` 直接构造，无需 `http.Response` 包装。

#### D-7：start 固定到 end-24h 而非可配置

- **歧义**：Loki `query_range` 需要 `start` 和 `end` 参数，SPEC 未明确 `start` 的计算方式。可配置 `start`（如环境变量 `LOKI_QUERY_WINDOW`）更灵活但增加复杂度。
- **选择**：`start = end - 24h` 固定窗口。
- **理由**：
  1. **避免 Loki 全量扫描**：不设 `start` 或设为很早的时间会导致 Loki 扫描大量数据，性能差。
  2. **24h 窗口覆盖首屏 + 前端「加载更多」的典型场景**：用户通常查看最近 24h 日志，往前翻页时 `end` 递减，`start` 跟随 `end-24h`，始终覆盖 24h 窗口。
  3. **符合 Karpathy 原则五「如无必要，勿增实体」**：不引入未被要求的环境变量配置。

### 2. Deviations

#### DV-1：cursor 映射为 `end` 而非 `start`，direction=backward 而非 forward

- **Spec said**：Issue AC 第 3 条「cursor 是 RFC3339 时间戳，adapter 内部转换为 Loki `start` 参数（Unix 纳秒）」；PRD FR-10；SPEC §3.3 line 395-431 示例（`start` 参数 + forward + `next_cursor`=最后一条）；SPEC §5.5 line 717「cursor：RFC3339 时间戳 → Loki `start`（Unix 纳秒）」+ line 718「next_cursor：结果最后一条 timestamp（RFC3339）」。
- **Implemented**：cursor → Loki `end` 参数（Unix 纳秒）；`direction=backward`；`start=end-24h`；`next_cursor`=本批最早一条 timestamp（RFC3339）；返回 items 保持 backward 倒序（最新在前）不反转。
- **Why**：继承 B5 live gate 修复后已验证的语义（B5 §1.2、§2.2、§4.1）。B5 在真实 Gateway+前端联调中复现了 forward 方向的缺陷（首屏返回最早的 10 条 docker-entrypoint 启动日志，用户无法看到最新日志），并已修复为 backward 方向。本 Issue #5 是 B-3 Loki adapter 部分的固化实现，与 B5 已验证的 live 语义一致，而非回退到 SPEC 草案的未经验证的 forward 方向。
- **Spec update needed**：SPEC §3.3 line 395-431 示例、SPEC §5.5 line 717-718、PRD FR-10、Issue AC 第 3 条应同步更新为 `end` + backward + `next_cursor`=最早一条。B5 §4.1 已标注「SPEC 和 PRD 尚未同步更新」，此同步待办仍在。

#### DV-2：LogQL pod label 用正则匹配而非精确匹配

- **Spec said**：Issue AC 第 2 条、PRD FR-9、SPEC §3.3 line 715、SPEC §5.5 line 715 规定 LogQL 为 `{namespace="<namespace>",pod="<instance_id>"} | json`（精确匹配）。
- **Implemented**：`{namespace="<namespace>",pod=~"^<escaped_instance>(-.*)?$"} | json`（正则匹配）。
- **Why**：继承 B5 live gate 修复后已验证的语义（B5 §1.1、§2.1）。真实 Deployment 创建的 Pod 带 ReplicaSet hash（如 `demo-preview-nginx-6c748d8b7-tqfrl`），精确匹配查不到任何日志。正则匹配与 metrics 路径的 `promQLPodMatcher` 策略一致。
- **Spec update needed**：SPEC §3.3 line 715、SPEC §5.5 line 715、PRD FR-9、Issue AC 第 2 条应同步更新为正则匹配语法。B5 §2.1 已标注「Spec update needed」。

#### DV-3：返回 items 不反转（保持 backward 倒序）

- **Spec said**：SPEC §5.5 未规定返回顺序。SPEC §3.3 line 395-431 示例未反转（但示例是 forward 方向，天然正序）。
- **Implemented**：保持 Loki backward 的倒序（最新在前），不反转。
- **Why**：继承 B5 live gate 修复后的语义（B5 §1.2、§2.3）。前端日志应用惯例是最新在前，「加载更多」追加更早日志到列表末尾。反转会导致首屏最旧在前，不符合用户预期。前端 `LogsTab.tsx` 直接渲染 items 顺序，无需 adapter 反转。

#### DV-4：start 固定到 end-24h

- **Spec said**：SPEC §3.3 line 395-431 示例未明确 `start` 的计算方式（示例未展示 `start` 参数构造）。
- **Implemented**：`start = end - 24h` 固定窗口。
- **Why**：避免 Loki 全量扫描；24h 窗口覆盖首屏 + 往前翻页的典型场景。SPEC 未规定 `start` 计算，此为实现细节决策，不构成偏离，但记录以便未来可配置化时参考。

### 3. Tradeoffs

#### T-1：Loki HTTP API 直连 vs Loki client library

- **备选方案 A**：使用 Loki 官方 Go client library（如 `github.com/grafana/loki/pkg/logql`）。
  - 优点：类型安全，LogQL 构造有库支持。
  - 缺点：引入外部依赖，违反 CLAUDE.md §5「业务服务不得直接依赖组件 SDK；直接导入必须有 allowlist、`coupling_level` 和保留理由」；`validate_component_imports.py` 的 `FORBIDDEN_IMPORT_PREFIXES` 目前不含 loki，但引入 loki client 会增加耦合。
- **备选方案 B**（采用）：纯标准库 `net/http` + `encoding/json` 直接调用 Loki HTTP API。
  - 优点：零外部依赖，符合 ports/adapters 边界；Loki HTTP API 简单稳定，无需 client library。
  - 缺点：LogQL 构造需手写，但通过 `buildLokiLogQL` + `escapeLogQLRegex` 集中管理。
- **胜出原因**：符合 CLAUDE.md §5 组件边界；零外部依赖；Loki HTTP API 足够简单；与 `prometheus_instance_observability.go` 的纯 HTTP 调用模式一致。

#### T-2：cursor→end + backward vs cursor→start + forward

- **备选方案 A**：cursor→start + forward + next_cursor=最后一条（SPEC 草案）。
  - 优点：符合 SPEC §3.3/§5.5 字面要求；无需偏离 AC。
  - 缺点：首屏返回最老日志，与 `kubectl logs --tail`、日志应用习惯相反；B5 live gate 已复现此缺陷并修复为 backward。
- **备选方案 B**（采用）：cursor→end + backward + next_cursor=最早一条（B5 live gate 修复语义）。
  - 优点：首屏返回最新日志，符合日志应用习惯；与 B5 live gate 已验证语义一致；与前端 `useInfiniteQuery` infinite scroll 语义对齐。
  - 缺点：偏离 SPEC §3.3/§5.5/PRD FR-10/Issue AC 第 3 条字面要求；需 SPEC/PRD/AC 同步更新（B5 §4.1 已标注待办）。
- **胜出原因**：live gate 已验证 backward 语义正确；与 `kubectl logs` 习惯一致；与 B5 已修复代码一致（避免回退到有缺陷的 forward）。代价是 SPEC/PRD/AC 同步更新待办，由后续批次或文档维护处理。

#### T-3：level 解析侧过滤 vs LogQL 管道下推

- **备选方案 A**：LogQL `| json | level="<level>"` 管道下推到 Loki 服务端。
  - 优点：性能更好，减少网络传输。
  - 缺点：依赖日志行是 JSON 且含 `level` 字段；Fluent-Bit 采集的 nginx/stdout 日志无 `level` 字段会被丢弃；与 B5 §1.3 的 level 推断策略冲突。
- **备选方案 B**（采用）：解析侧过滤（`mapLokiStreamsToLogEntries` 中 `if level != "" && entry.Level != level { continue }`）。
  - 优点：兼容无 `level` 字段的非结构化日志（结合 `inferLogLevel` 推断）；与 B5 live gate 修复后的语义一致。
  - 缺点：浪费带宽（返回全部日志再过滤）。
- **胜出原因**：兼容非结构化日志；单实例查询 limit≤1000 条带宽浪费可接受；与 B5 一致。

#### T-4：`decodeLokiResponse` 用 `io.Reader` 接口 vs `io.ReadCloser`

- **备选方案 A**：`io.ReadCloser`。
  - 优点：直接接收 `resp.Body`。
  - 缺点：接口过大，`Close` 由 `QueryLogs` 的 `defer` 处理，不需要传入。
- **备选方案 B**（采用）：`interface{ Read(p []byte) (int, error) }`（最小接口）。
  - 优点：最小接口原则；测试可用 `strings.NewReader` 直接构造。
  - 缺点：类型签名稍显非常规。
- **胜出原因**：符合 Karpathy 原则五「如无必要，勿增实体」；测试友好。

### 4. Open Questions

#### OQ-1：SPEC §3.3/§5.5、PRD FR-10、Issue AC 第 3 条的同步更新何时完成

- **假设**：B5 §4.1 已标注「SPEC 和 PRD 尚未同步更新」，本 Issue #5 实现沿用 B5 已修复语义，SPEC/PRD/AC 同步更新待办仍在。
- **待用户确认**：是否在 B-3 批次收尾时同步更新 SPEC §3.3 line 395-431 示例、SPEC §5.5 line 717-718、PRD FR-10、Issue AC 第 3 条为 `end` + backward + `next_cursor`=最早一条？还是等 B-3 整体完成（含 issue-006、issue-007）后统一更新？
- **可能后续**：文档维护批次或 B-3 收尾时处理。

#### OQ-2：`start = end - 24h` 固定窗口是否足够

- **假设**：24h 窗口覆盖首屏 + 往前翻页的典型场景。往前翻页时 `end` 递减，`start` 跟随 `end-24h`，始终覆盖 24h 窗口。
- **待用户确认**：是否存在需要查询 24h 之前日志的场景？若有，是否需要将 `start` 改为可配置（如环境变量 `LOKI_QUERY_WINDOW`）或扩大为更早的时间（如 7d）？
- **可能后续**：若确认不足，在后续 Issue 中扩展 `start` 计算（可配置化或动态扩大）。

#### OQ-3：多 stream 合并不重新排序的跨流排序保证

- **假设**：Loki `direction=backward` 已在服务端按时间倒序返回，单 pod 查询通常单 stream，多 stream 场景的跨流排序由 Loki 合并器保证（`mapLokiStreamsToLogEntries` 注释 line 218 已说明）。
- **待用户确认**：多 stream 场景（如 pod 有多个 container，每个 container 独立 stream）是否真的由 Loki 合并器保证全局倒序？若不保证，是否需要在 adapter 层做跨 stream 归并排序？
- **可能后续**：若 live 验证发现多 stream 排序问题，在 `mapLokiStreamsToLogEntries` 增加 `sort.Slice` 按 timestamp 倒序排序。

#### OQ-4：`escapeLogQLRegex` 与 `promQLPodMatcher` 的转义逻辑重复

- **假设**：`escapeLogQLRegex`（本文件）与 `promQLPodMatcher`（`prometheus_instance_observability.go`）的正则转义逻辑一致，但独立实现，存在重复。
- **待用户确认**：是否应抽取为共用 helper（如 `escapeLogQLRegex` 移到 `promQLPodMatcher` 所在文件或共用 util）？还是保持独立以避免跨文件耦合？
- **可能后续**：若确认抽取，在后续重构 Issue 中统一。当前保持独立，遵循 Karpathy 原则三「只触碰你必须改动的部分」。

## 验证命令

```bash
cd repo
go build ./pkg/adapters/runtime/...                            # EXIT 0
go vet ./pkg/adapters/runtime/...                               # EXIT 0
go test ./pkg/adapters/runtime/...                              # EXIT 0（15 个 Loki 测试 + 全部现有测试）
go test ./pkg/adapters/runtime/ -run "Loki|LokiLog" -v         # 15/15 PASS
make test                                                        # EXIT 0（pkg/adapters/runtime 9.185s + 全量 services 测试）
make validate-architecture                                       # EXIT 0（component import guard passed / architecture guardrails valid）
git diff --check                                                 # EXIT 0（仅 pre-existing CRLF warning on 无关 yaml）
```

## AC 满足情况（8/8）

- [x] 新增文件 `repo/pkg/adapters/runtime/loki_log_store.go`，实现 `LogStore` interface（含编译时断言 `var _ ports.LogStore = (*LokiLogStore)(nil)`）
- [x] 使用 Loki HTTP API `/loki/api/v1/query_range`，LogQL：`{namespace="<namespace>",pod=~"^<instance_id>(-.*)?$"} | json`（正则匹配，见 DV-2 偏离说明）
- [x] cursor 映射：cursor 是 RFC3339 时间戳，adapter 内部转换为 Loki `end` 参数（Unix 纳秒）；`next_cursor` 是结果最早一条的 timestamp（见 DV-1 偏离说明，继承 B5 live gate 修复语义）
- [x] 多租户隔离：通过 LogQL 的 `{namespace="ani-tenant-<tenant_id>"}` label 过滤，不使用 Loki X-Scope-OrgID（单租户模式）
- [x] 解析 Loki 返回的日志行，映射为 `InstanceLogEntry`（Timestamp time.Time / Level / Message / Container / Stream），非 JSON 行回退纯文本 + level 推断
- [x] Loki 不可达 / 非 200 / 传输错误 / decode 失败均返回包装错误，不伪造空结果（`loki query failed` / `loki returned status %d` / `decode loki response`）
- [x] Typecheck/lint passes（`go build` + `go vet` + `go test` + `make test` + `make validate-architecture` 全通过）
- [x] 单元测试覆盖 LogQL 构造（注入 + 转义）、cursor 双向映射（空/合法/非法）、Loki HTTP 响应解析（stream values / level 过滤 / next_cursor / 纯文本回退 / level 推断 / 显式 level 保留 / 端到端 HTTP / cursor 透传 / 非 200 / 传输错误 / 空 BaseURL）——15 个测试全通过

## 后续依赖

本 Issue 是 B-3 日志持久化链路的 Loki adapter 实现，后续 Issue 依赖此 adapter：

- issue-006（PrometheusInstanceObservability 扩展）：新增 `logStore` 可选字段 + `SetLogStore` 方法 + fallback 逻辑（SPEC §3.2），`LogStore` 为 nil 时 fallback 到 K8s API（零回归）
- issue-007（runtime 环境变量注入）：`INSTANCE_OBSERVABILITY_LOG_STORE` 环境变量处理（SPEC §3.5），`loki` 时构造 `LokiLogStore` 注入到 `PrometheusInstanceObservability`

B-3 整体完成后，应同步更新 SPEC §3.3/§5.5、PRD FR-9/FR-10 以反映 backward 方向 + 正则匹配的实际语义（见 OQ-1）。
