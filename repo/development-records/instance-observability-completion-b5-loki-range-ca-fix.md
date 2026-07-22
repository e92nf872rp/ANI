# INSTANCE-OBSERVABILITY-COMPLETION-B5-LOKI-RANGE-CA-FIX

> 批次类型：Feature batch（live gate 收尾修复，涉及 adapter 边界与 Gateway 组装顺序，非 guard micro-batch）
> 关联 PRD：`services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
> 关联 SPEC：`services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md`
> 关联批次：B4（Loki+Fluent-Bit 部署）、B1（handler pass kind）、CORE-INSTANCE-METRICS-MULTI-EXPORTER-A（range query 端点 + PromQL 重写）
> 完成日期：2026-07-21

## 背景

B4 已完成 Loki + Fluent Bit 推荐部署示例 yaml，live 部署在三节点全部 Running。但在真实 Gateway + 前端联调时发现三个阻断性缺陷，导致用户界面的 logs tab、events tab、metrics 时序图均无法显示真实数据：

1. **logs tab 只显示 3 条固定 mock 日志**——`.env` 缺少 `INSTANCE_OBSERVABILITY_LOG_STORE=loki` 配置，`buildLogStore` 走默认分支回落 K8s API；同时 Loki LogQL pod label 用精确匹配查不到带 ReplicaSet hash 的 Pod。
2. **events tab 报 `x509: certificate signed by unknown authority`**——`kubernetesHTTPClient` 在 `inCluster=false`（配置了 `KUBERNETES_API_HOST`）时直接返回 `http.DefaultClient`，不加载 `KUBERNETES_SERVICE_ACCOUNT_CA_FILE` 指向的自签 CA。
3. **metrics 时序图无数据**——`main.go` 调用 `gatewayObservabilityRuntimeConfigFromEnv(nil)` 传入 `nil` InstanceLookup，`newGatewayObservabilityService` 在 InstanceLookup=nil 时提前返回 nil，`PrometheusObservabilityService` 未创建，`/observability/query_range` 路由回退 local 空结果。

## 1. Design Decisions

### 1.1 Loki LogQL pod label 从精确匹配改为正则匹配

**Ambiguity:** PRD US-005 AC-2 和 SPEC §5.11 规定 LogQL 为 `{namespace="<namespace>",pod="<instance_id>"} | json`（精确匹配）。但真实环境中 Deployment 创建的 Pod 带 ReplicaSet hash（如 `demo-preview-nginx-6c748d8b7-tqfrl`），精确匹配 `pod="demo-preview-nginx"` 查不到任何日志。SPEC 未考虑控制器生成的 hash 后缀场景。

**Choice:** pod label 改为正则匹配 `pod=~"^<escaped_instance>(-.*)?$"`，复用 `promQLPodMatcher` 的转义逻辑（Loki LogQL 与 PromQL 正则语法一致）。

**Rationale:**
- 与 `promQLPodMatcher`（metrics 路径）保持一致，避免 logs 和 metrics 两个链路对 pod 名的匹配策略分叉
- 正则同时兼容直接 Pod（无后缀）和 Deployment/Job 控制器生成的 Pod（`name-<hash>[-<hash>]`）
- 多租户隔离仍由 namespace 精确匹配保证，pod 正则不跨 namespace 误匹配

### 1.2 Loki 日志查询方向与分页语义

**Ambiguity:** SPEC §5.11 规定 `next_cursor` 是"结果最后一条的 timestamp"，但未明确查询方向（forward/backward）和首屏语义。

**Choice:** 采用 `direction=backward`，cursor 作为 `end` 参数往前翻页：
- 首屏 `cursor` 为空时 `end=now`，返回最近 `limit` 条（与 `kubectl logs --tail` 对齐）
- `next_cursor` = 本批最早一条的 timestamp（RFC3339），供下一页 `end` 边界往前翻页
- 返回的 items 保持 Loki backward 的倒序（最新在前），符合日志应用展示习惯；前端「加载更多」追加更早日志到列表末尾

**Rationale:** 之前用 `direction=forward` + `limit=10` 返回最早的 10 条（docker-entrypoint 启动日志），与 `kubectl logs` 默认行为不一致，用户无法看到最新日志。

### 1.3 日志 level 从 message 推断（Fluent-Bit 采集的 nginx/stdout 日志无 level 字段）

**Ambiguity:** SPEC §5.11 规定解析 Loki 日志行 JSON 提取 `level` 字段。但 Fluent-Bit 采集的容器 stdout 日志 JSON 格式是 `{"message":"...","logtag":"F","stream":"stdout",...}`，没有 `level` 字段，导致前端级别列显示为空。

**Choice:** JSON 解析成功但 `level` 为空时，从 `message` 内容推断 level（复用已有 `inferLogLevel`：error/warn/debug 前缀识别，其余为 info）。

**Rationale:** 避免 adapter 层对日志格式做强假设；`inferLogLevel` 已有规则覆盖常见 nginx/k8s 日志前缀。

### 1.4 K8s REST client CA 加载策略

**Ambiguity:** SPEC 未覆盖 `KUBERNETES_API_HOST` + `KUBERNETES_SERVICE_ACCOUNT_CA_FILE` 显式配置外部 API server 的场景。原实现 `inCluster=false` 时直接返回 `http.DefaultClient`，不加载 CA，导致访问自签证书的 API server 报 x509 错误。

**Choice:** CA 加载按优先级分三种：
1. `inCluster=true`：必须加载 CA，路径为空回退默认 service account CA
2. `inCluster=false` 但 `caFile` 显式非空：仍加载该 CA（外部 API server 自签证书场景）
3. `inCluster=false` 且 `caFile` 为空：返回 `http.DefaultClient`（公网/标准 CA 场景，零回归）

**Rationale:** logs/metrics 链路不经过 K8s API（走 Loki/Prometheus），events 是第一个真正访问 K8s API 的链路，所以这个缺陷直到 events 联调才暴露。分三种策略既修复外部 API server 场景，又不破坏公网/标准 CA 场景。

### 1.5 PrometheusObservabilityService 延迟注入 InstanceLookup

**Ambiguity:** SPEC §4.1.4 描述 `rewritePromQLLabels` 需要 InstanceLookup 查实例记录，但未规定 Gateway 组装顺序。原实现 `NewPrometheusObservabilityService` 在 InstanceLookup=nil 时直接返回 error，`newGatewayObservabilityService` 提前返回 nil，service 未创建。根因是 `demoInstanceStore`（含 InstanceLookup 实现）在 router 内部创建，而 `ObservabilityService` 在 main.go 创建，两者独立。

**Choice:**
- `NewPrometheusObservabilityService` 允许 InstanceLookup 为 nil（延迟注入）
- 新增 `SetInstanceLookup` 方法
- `rewritePromQLLabels` 在 lookup nil 时返回 error（不 panic），QueryRange 降级返回空结果
- router.go 调整注册顺序：demo instances 先注册，拿其 service（实现 InstanceLookup 接口）注入到 ObservabilityService，再注册 observability 路由

**Rationale:** 
- 前端 `renderPromQL` 把 `instance.id`（如 `inst_1`）同时注入 namespace/pod 占位符，后端 `rewritePromQLLabels` 用它查实例记录，拿到真实 namespace（`ani-tenant-00000000-...`）和 pod 正则（`^demo-preview-nginx(-.*)?$`），链路完全正确
- 延迟注入避免破坏现有契约（其他调用方仍可在构造时传入 lookup）
- 类型断言注入（`*PrometheusObservabilityService`）只在 router 启动阶段执行一次，无运行时并发风险

## 2. Deviations

### 2.1 LogQL pod label 从精确匹配改为正则匹配

**Spec said:** PRD US-005 AC-2 / SPEC §5.11 规定 LogQL 为 `{namespace="<namespace>",pod="<instance_id>"} | json`（精确匹配）。

**Implemented:** `{namespace="<namespace>",pod=~"^<escaped_instance>(-.*)?$"} | json`（正则匹配）。

**Why:** 真实 Deployment 创建的 Pod 带 ReplicaSet hash（如 `demo-preview-nginx-6c748d8b7-tqfrl`），精确匹配查不到任何日志。这是 live gate 复现的真实缺陷，SPEC 撰写时未考虑控制器 hash 后缀。正则匹配与 metrics 路径的 `promQLPodMatcher` 策略一致。

**Spec update needed:** SPEC §5.11 和 PRD US-005 AC-2 应同步更新为正则匹配语法。

### 2.2 Loki 查询方向从 forward 改为 backward

**Spec said:** SPEC §5.11 未明确查询方向，但 `next_cursor` 描述为"结果最后一条的 timestamp"隐含 forward 语义。

**Implemented:** `direction=backward`，`next_cursor` = 本批最早一条 timestamp，作为下一页 `end` 边界往前翻页。

**Why:** forward + limit=10 返回最早的 10 条日志，用户看不到最新日志，与 `kubectl logs --tail` 行为不一致。backward 首屏返回最新 limit 条，符合日志应用习惯。

### 2.3 返回 items 不反转（保持 backward 倒序）

**Spec said:** SPEC §5.11 未规定返回顺序。

**Implemented:** 保持 Loki backward 的倒序（最新在前），不反转。

**Why:** 前端日志应用惯例是最新在前，「加载更多」追加更早日志到列表末尾。反转会导致首屏最旧在前，不符合用户预期。

## 3. Tradeoffs

### 3.1 Loki LogQL 正则匹配 vs 精确匹配

**Alternatives:**
- A. 保持精确匹配，要求用户创建 Pod（非 Deployment）——违反 ANI 产品语义（实例是抽象，底层可以是 Pod/Deployment/VM）
- B. 正则匹配（chosen）——兼容直接 Pod 和控制器生成的 Pod
- C. 用 `{namespace="...",pod=~".*<instance>.*"}` 宽松正则——跨 pod 误匹配风险

**Pros/Cons:** B 最精确，复用 `promQLPodMatcher` 既有逻辑，与 metrics 路径策略一致。

### 3.2 InstanceLookup 延迟注入 vs 调整 main.go 组装顺序

**Alternatives:**
- A. 延迟注入（chosen）：`SetInstanceLookup` + router 注册时类型断言注入
- B. main.go 先创建 demoInstanceAPI，传 service 给 observabilityService，再传给 router——需要导出 `newDemoInstanceAPIWithObservability`，破坏 router 封装
- C. 后置注入但不加 setter，直接反射——类型不安全

**Pros/Cons:** A 改动最小（4 处），不破坏 router 封装，类型安全；缺点是类型断言耦合 `*PrometheusObservabilityService` 具体类型，但 router 包已 import runtimeadapter，无新增依赖。

### 3.3 K8s REST client CA 加载策略

**Alternatives:**
- A. 分三种策略（chosen）：inCluster / 外部+caFile / 外部无 caFile
- B. `inCluster=false` 时始终加载 caFile（即使为空报错）——破坏公网/标准 CA 场景
- C. 始终返回 `http.DefaultClient`——破坏 in-cluster 自签证书场景

**Pros/Cons:** A 零回归（既保护 in-cluster，又支持外部+caFile，又不破坏公网场景），但逻辑稍复杂。新增 3 个单元测试覆盖三种分支。

## 4. Open Questions

### 4.1 SPEC 和 PRD 尚未同步更新

**Assumption:** LogQL pod label 改为正则匹配、查询方向改为 backward 是对 SPEC §5.11 和 PRD US-005 AC-2 的偏离，但 SPEC/PRD 尚未同步更新。

**Should verify:** 用户是否需要我同步更新 `spec-console-instance-observability-completion.md` §5.11 和 `prd-console-instance-observability-completion.md` US-005 AC-2 为正则匹配语法 + backward 方向语义？

### 4.2 多 stream 跨流排序

**Assumption:** `mapLokiStreamsToLogEntries` 注释提到"多 stream 合并不重新按时间排序，依赖 Loki backward 的全局倒序保证"。单 pod 查询通常单 stream，多 stream 场景（多容器 Pod）的跨流排序由 Loki 合并器保证。

**Should verify:** 多容器 Pod（如 sidecar）场景下，Loki backward 是否真正保证跨 stream 全局倒序？如不保证，需要在 adapter 层做跨 stream 合并排序。

### 4.3 前端 PromQL 模板 namespace 占位符语义

**Assumption:** 前端 `renderPromQL` 把 `instance.id` 同时注入 `{{namespace}}` 和 `{{pod}}`，后端 `rewritePromQLLabels` 用实例记录解析真实 namespace 和 pod 正则。这个契约依赖前端始终把 instance_id 注入 namespace 占位符。

**Should verify:** 其他前端模块（如 VM console 时序图，Issue #010/011 范围）是否也遵循这个注入约定？如有模块把真实 namespace 注入 `{{namespace}}`，后端 `rewritePromQLLabels` 会把它当作 instance_id 查实例记录，可能解析失败。

### 4.4 demoInstanceStore 内存 store 重启数据丢失

**Assumption:** Gateway 重启后 demoInstanceStore 清空，实例 ID 从 `inst_1` 重新开始。用户之前访问的 `inst_2` 在重启后不存在。

**Should verify:** 这是否是已知边界？生产环境用 PostgreSQL 持久化 store 不受影响，但 dev/demo 模式的内存 store 重启丢失是否需要在 UI 提示？

## 验证命令

```bash
cd repo
# 单元测试（Loki + PrometheusObservability + K8s REST client）
go test ./pkg/adapters/runtime/... -run "Loki|PrometheusObservability|KubernetesRESTClient|Observability" -count=1
# 结果：ok github.com/kubercloud/ani/pkg/adapters/runtime 0.323s

# 编译验证
go build ./services/ani-gateway/...
# 结果：成功

# live gate 验证（真实 Loki + Prometheus + K8s API）
# 1. logs: curl http://localhost:8080/api/v1/instances/inst_1/logs?limit=10
#    → provider: prometheus-kubernetes-instance-observability, 10 条真实 nginx Pod 日志
# 2. events: curl http://localhost:8080/api/v1/instances/inst_1/events?limit=20
#    → 2 条 ScalingReplicaSet 事件（scale 1→2→1 触发）
# 3. metrics 时序图: curl http://localhost:8080/api/v1/observability/query_range?query=...&start=...&end=...&step=60s
#    → mode: real, 1 series, 16 个采样点，PromQL 已重写 namespace/pod
```

## 涉及文件

| 文件 | 改动 |
|---|---|
| `repo/.env` | 新增 `INSTANCE_OBSERVABILITY_LOG_STORE=loki` + `INSTANCE_OBSERVABILITY_LOKI_URL=http://127.0.0.1:13100` |
| `repo/pkg/adapters/runtime/loki_log_store.go` | LogQL pod 正则匹配 + escapeLogQLRegex；direction=backward；cursor 作为 end 往前翻页；parseLokiLogLine 从 message 推断 level |
| `repo/pkg/adapters/runtime/loki_log_store_test.go` | 更新断言匹配新正则语法 |
| `repo/pkg/adapters/runtime/kubernetes_rest_client.go` | `kubernetesHTTPClient` CA 加载策略分三种（inCluster / 外部+caFile / 外部无 caFile） |
| `repo/pkg/adapters/runtime/kubernetes_rest_client_test.go` | 新增 3 个测试覆盖三种 CA 加载分支 |
| `repo/pkg/adapters/runtime/prometheus_observability_service.go` | `NewPrometheusObservabilityService` 允许 nil lookup；新增 `SetInstanceLookup`；`rewritePromQLLabels` nil 保护 |
| `repo/pkg/adapters/runtime/prometheus_observability_service_test.go` | 更新测试覆盖 nil lookup 延迟注入场景 |
| `repo/services/ani-gateway/observability_runtime.go` | 移除 InstanceLookup=nil 提前返回 nil 的逻辑 |
| `repo/services/ani-gateway/internal/router/demo_instances.go` | `registerDemoInstancesWithObservability` 返回 demoInstanceAPI 的 service |
| `repo/services/ani-gateway/internal/router/router.go` | 调整注册顺序：demo instances 先注册，类型断言注入 InstanceLookup 到 ObservabilityService |
