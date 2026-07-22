# Core 实例指标多 exporter 聚合 adapter

> Issue: `repo/services/tasks/modules/issue/console/compute/issue-002-core-metrics-multi-exporter-aggregation.md`
> Batch: `CORE-INSTANCE-METRICS-MULTI-EXPORTER-A`
> Product line: core
> Date: 2026-07-06

## 实现摘要

实现 `PrometheusInstanceObservabilityService.GetMetrics` 的多 exporter 聚合 adapter。原实现仅查 `container_cpu_usage_seconds_total`（CPU），内存/网络/GPU 字段全部 nil。本批次按 PRD US-003 AC #3+#4 与 SPEC §2.2.2 方案，从 metrics.k8s.io 聚合 CPU/内存/网络，从 DCGM exporter 聚合 GPU 利用率与显存，仅 `kind=gpu_container` 时采集 GPU 指标。

## 修改文件

- `repo/pkg/ports/instance_observability.go` — `InstanceObservationGetRequest` 新增 `Kind WorkloadKind` 字段
- `repo/pkg/adapters/runtime/prometheus_instance_observability.go` — 重写 `GetMetrics` 实现多 exporter 聚合
- `repo/services/ani-gateway/internal/router/demo_instances.go` — `getMetrics` handler 传递 `record.Kind`
- `repo/pkg/adapters/runtime/prometheus_instance_observability_test.go` — 新增 5 个测试

## Verification commands run

- `go test ./pkg/adapters/runtime/... -count=1 -run TestPrometheusInstanceObservability` → PASS (7/7)
- `go test ./pkg/adapters/runtime/... ./pkg/ports/...` → PASS
- `make validate-architecture` → PASS (✅ architecture guardrails valid)
- `git diff --check` → clean
- `git diff --stat api/openapi/v1.yaml` → unchanged（未修改 OpenAPI）
- `make test` → 仅 2 个预存失败（`pkg/bootstrap` Go 版本 / `TestDemoInstanceServiceRealShellExecutesCommand` shell 环境），经 `git stash` 验证与本次改动无关

---

## 1. Design Decisions

### D1: 通过 `InstanceObservationGetRequest.Kind` 路由 GPU 采集

**歧义：** SPEC §2.2.2 定义"多 exporter 聚合 adapter"方案但标记为"待补"，未明确 adapter 如何判断是否采集 GPU 指标。PRD US-003 AC #3 要求 `kind=gpu_container` 在 DCGM 可用时填充 GPU 字段，但未定义 Kind 如何从 handler 传递到 adapter。

**选择：** 在 `InstanceObservationGetRequest` 新增 `Kind WorkloadKind` 字段，handler 从 `WorkloadInstanceRecord.Kind` 透传，adapter 用 `if request.Kind == ports.WorkloadKindGPUContainer` 守卫 DCGM 查询块。

**理由：** Kind 是工作负载类型枚举，已存在于 `WorkloadInstanceRecord`，是判断"是否应采集 GPU"的最直接语义来源。通过 port 请求字段传递，保持 adapter 无状态，不引入额外依赖。

### D2: 单 exporter 不可用时不阻塞，用 `if err == nil` 守卫每个字段

**歧义：** SPEC §6.3 要求"adapter exporter 不可用时 `getInstanceMetrics` 返回 null 字段"，但未定义实现机制——是整体降级还是逐字段降级。

**选择：** 每个 PromQL 查询独立执行，用 `if sample, err := o.queryPrometheusScalar(...); err == nil { 填充字段 }` 守卫。查询失败时该字段保持 nil，其他字段正常采集。

**理由：** 逐字段降级最大化数据可用性。metrics.k8s.io 和 DCGM 是独立 exporter，一个不可用不应影响另一个。`InstanceMetricsRecord` 的 GPU/内存/网络字段均为可空指针（`*float64` / `*int64`），nil 即表示"不可采集"，与 OpenAPI §3.3.1 可空字段语义一致。

### D3: 非 gpu_container 的 GPU 字段保持 nil，禁止用 0 代替

**歧义：** PRD AC 明确"禁止用 0 代替缺失"，但未定义"非 gpu_container"时是完全跳过 DCGM 查询还是查询后置 nil。

**选择：** 完全跳过——GPU 查询块被 `if request.Kind == ports.WorkloadKindGPUContainer` 整体守卫，非 gpu_container 时不发起任何 DCGM 查询。

**理由：** 跳过查询比"查询后置 nil"更高效，且语义更清晰——非 GPU 工作负载本就不应有 GPU 指标。测试 `TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerGPUNil` 进一步断言 DCGM 查询不应到达（mock 遇到 DCGM 查询即 `t.Fatalf`）。

### D4: DCGM 查询带 namespace 过滤

**歧义：** SPEC §5.2.1 PromQL 模板表定义 GPU 指标注入参数为 `{instance_id}`，但 PromQL 正文维护在运维文档，未明确标签选择器。metrics.k8s.io 查询使用 `{namespace=...,pod=...}`，DCGM 查询的标签选择器未在 SPEC 定义。

**选择：** DCGM 查询使用 `{namespace=%q,pod=%q}`，与 metrics.k8s.io 查询保持一致。

**理由：** dcgm-exporter 通过 Kubernetes SD 自动 relabel 会附加 `namespace` 和 `pod` 标签。多租户环境下若仅用 `{pod=...}` 过滤，不同 namespace 的同名 pod 会误匹配，属于多租户隔离风险。带 namespace 过滤与 metrics.k8s.io 查询保持一致，确保租户隔离。此为 `/review-it` 阶段发现并修复的问题。

---

## 2. Deviations

### DV1: `memory_total_mb` 保留 nil

**SPEC：** SPEC §5.2.1 模板 `instance_memory_utilization` 描述为"内存 used/total 比率"，暗示应提供 memory total。

**实现：** `MemoryTotalMB` 字段保留 nil。

**理由：** metrics.k8s.io Prometheus 指标 `container_memory_working_set_bytes` 只提供 used，不直接提供 total。container memory limit 可从 `container_spec_memory_limit_bytes` 获取，但该指标在 many deployments 下不一定存在或为空。SPEC §3.3.1 明确 `memory_total_mb` 为可空字段。根据 Karpathy 原则二"拒绝带有猜想的实现"——不为不存在的指标伪造 0 或从其他来源猜测，保留 nil 并由 UI 层用"暂不可用"呈现（SPEC §6.3）。如后续需要 memory total，应在独立 Issue 中从 kubelet `container_spec_memory_limit_bytes` 采集。

### DV2: Timestamp 采用最后一个成功查询的 sample timestamp

**SPEC：** 未定义多 exporter 聚合时 `InstanceMetricsRecord.Timestamp` 应取哪个 exporter 的时间戳。

**实现：** 初始化为 `o.now()`，每个查询成功时用其 sample timestamp 覆盖，最终值为最后一个成功查询的 timestamp。

**理由：** 同一查询时刻不同 exporter 返回的 sample timestamp 通常一致（均指向采集时刻）。SPEC 未要求 timestamp 必须来自特定 exporter，此实现保证 timestamp 反映实际采集时刻而非代码执行时刻。全部查询失败时保留 `now()`，表示查询发起时刻。

---

## 3. Tradeoffs

### T1: 顺序独立查询 vs 并发查询

**备选 A（采用）：** 5-7 个 PromQL 查询顺序执行，每个独立 `if err == nil` 守卫。

- 优点：实现简单；错误隔离天然；无需 context 超时管理；测试 mock 简单
- 缺点：延迟为各查询之和（最坏情况 7x 单查询延迟）

**备选 B（拒绝）：** 用 `errgroup` 并发查询。

- 优点：延迟降为 max(各查询延迟)
- 缺点：实现复杂度增加；需管理 goroutine 和共享 record 的并发写入（需加锁或用 channel 汇总）；测试 mock 需处理并发请求；当前 Sprint 13 的 Prometheus adapter 已是顺序模式，引入并发会破坏一致性

**决策：** 选 A。本批次是补全 SPEC "待补"方案，应保持与现有 adapter 模式一致。查询延迟在 local profile 下可接受；real provider 性能优化应由独立批次基于实测数据决定，不提前优化（Karpathy 原则五）。

### T2: 在 port 请求加 Kind 字段 vs 在 adapter 内查询 instance kind

**备选 A（采用）：** `InstanceObservationGetRequest` 新增 `Kind` 字段，handler 透传。

- 优点：adapter 无状态；不引入额外依赖（无需查 instance store）；语义清晰
- 缺点：port 接口签名变更，所有实现需更新（但当前仅 2 个实现：Prometheus 和 local，local 不使用 Kind）

**备选 B（拒绝）：** adapter 内调用 `WorkloadRuntime` 查询 instance kind。

- 优点：port 接口不变
- 缺点：adapter 引入对 `WorkloadRuntime` 的依赖，违反单一职责；增加一次额外查询；handler 已持有 `WorkloadInstanceRecord`，重复查询浪费

**决策：** 选 A。handler 已在 `instanceForObservation` 中获取 `WorkloadInstanceRecord`，Kind 是其字段，透传成本最低且不引入新依赖。local adapter 不受影响（不使用 Kind）。

---

## 4. Open Questions

### O1: memory_total_mb 采集来源

本批次保留 `MemoryTotalMB` 为 nil。如 PRD 后续要求 memory utilization 比率（used/total），需从 `container_spec_memory_limit_bytes` 或 kubelet cAdvisor 采集 total。是否需要新增独立 Issue 处理 memory total？

**待用户确认：** memory_total_mb 是否在当前 Sprint 范围内必须填充，还是可延后到下一批次。

### O2: DCGM exporter namespace 标签可用性

本实现假设 dcgm-exporter 通过 Kubernetes SD relabel 附加 `namespace` 标签。不同部署方式（helm chart / 原生 manifest）的 relabel 配置可能不同。real provider live gate 应验证目标集群的 dcgm-exporter 实际暴露的标签集合。

**待用户验证：** Sprint 13 已归档的 `sprint13-instance-observability-prometheus-live-evidence.json` 是否覆盖 DCGM 查询的 namespace 标签可用性；若未覆盖，real gate 需补充 DCGM 标签验证。

### O3: Feature batch 四文件更新

按 CLAUDE.md §6.3，Feature batch 完成时需更新四个文件（development-records、README、CURRENT-SPRINT、ANI-06）。本笔记完成 `development-records` 更新；其余三个文件的更新是否在 `/note-it` 阶段一并完成，还是留待 `/ship-it` 前处理？

**待用户确认：** 四文件更新的执行时机。

---

## AC 覆盖矩阵

| AC | 状态 | 实现位置 / 证据 |
|----|------|----------------|
| 聚合 CPU/内存/网络/GPU 填入 InstanceMetricsRecord | ✅ | `prometheus_instance_observability.go:122-149`（metrics.k8s.io）、`153-174`（DCGM） |
| gpu_container 填充 GPU 字段 | ✅ | `:153` `if request.Kind == ports.WorkloadKindGPUContainer` |
| 非 gpu_container GPU 字段为 nil | ✅ | GPU 块整体守卫；测试 `...NonGPUContainerGPUNil` 断言 DCGM 不到达 |
| 单 exporter 不可用不阻塞 | ✅ | 每查询独立 `if err == nil`；测试 `...SingleExporterDegradation` + `...GPUContainerDCGMUnavailable` |
| Gateway 不暴露 Prometheus/DCGM 地址 | ✅ | `demo_instances.go:655-659` 仅传 Kind，URL 在 adapter 内 |
| 错误语义 401/403/404 一致 | ✅ | 沿用 `writeInstanceObservabilityError`，未改错误路径 |
| RBAC: scope:instances:read | ✅ | 路由组上层中间件，未改鉴权链 |
| 单元测试覆盖全场景 | ✅ | 5 个新测试，7/7 PASS |
| make test 通过 | ✅ | 相关包全 PASS；2 个预存失败已验证无关 |
| 不修改 OpenAPI v1.yaml | ✅ | `git diff --stat` unchanged |

---

## 增量：可观测性链路接入 + Range Query（2026-07-08 补充）

> 以下为本批次首次笔记之后的增量改动，基于真实 K8s + Prometheus live gate 验证中发现的问题修复。本增量涉及 issue-011 的观测链路路由切换与本批次 metrics adapter 的 Live gate 修复。

### Verification Commands Run（增量）

| Command | Result |
|---------|--------|
| `go test ./pkg/adapters/runtime/... ./services/ani-gateway/... ./pkg/ports/... -count=1` | ✅ 全部通过 |
| `make validate-architecture` | ✅ 通过 |
| Live gate（真实 Prometheus 指标可见性） | ✅ 已验证：Pod running、Prometheus 有 cAdvisor 指标、快照/时序均返回真实数据 |

---

### Design Decisions（增量）

#### D5. PrometheusObservabilityService 的 PromQL label 重写策略

**Ambiguity:** SPEC §2.3.3 定义观测 handler 走 adapter，但没定义前端冻结模板里的 `namespace`/`pod` 占位符如何映射到真实 K8s namespace/pod。前端把 `instance_id` 同时注入 `namespace="{{pod}}"` 和 `pod="{{pod}}"`，但真实 namespace 是 `ani-tenant-{tenantId}`，真实 pod 名是 `{name}-{hash}`。

**Choice:** 在后端 `PrometheusObservabilityService.Query` 中用正则识别 PromQL 里的 `namespace="..."`/`pod="..."` 值，用该值（即 `instance_id`）查实例记录，拿到真实 `tenant_id`→namespace 和 `name`→pod 正则，然后逐个替换 label 值。namespace 用精确匹配 `=`，pod 用正则匹配 `=~"^name(-.*)?$"`。

**Rationale:** 前端不掌握 namespace/pod 映射（这是 Core 域知识），后端重写是唯一不破坏前端冻结模板契约的方案。正则匹配 pod 兼容 Deployment 生成的 hash 后缀（如 `test-container-6-78d-p428s`）。

#### D6. NaN/Inf 采样值过滤

**Ambiguity:** SPEC §6.3 Failure Modes 覆盖 adapter 不可用降级，但没覆盖 Prometheus 返回 NaN/Inf（如内存利用率 `used/limit` 当 limit=0 时 +Inf）导致 Go `encoding/json` 序列化 panic 的场景。

**Choice:** 在 `scalar()` 解析层用 `math.IsNaN`/`math.IsInf` 过滤，返回错误让上层降级为 nil 字段或空结果。range query 的 `queryPrometheusRange` 同样跳过 NaN/Inf 采样点（跳过单点而非整条 series）。

**Rationale:** 在最底层解析过滤，GetMetrics 和 Query/QueryRange 两条链路都受益。跳过单点而非整条 series 保证部分有效数据仍能返回。

#### D7. Range Query 端点设计

**Ambiguity:** SPEC §4.1.4 只定义了 `GET /observability/query`（instant query），没有 range query 端点。但 instant query 只返回当前一个采样点，无法绘制时序曲线。

**Choice:** 新增 `GET /observability/query_range` 端点，参数 `query`/`start`/`end`/`step`，返回 `ObservabilityRangeQueryResponse`（matrix，每条 series 含 `values` 时间序列采样点数组）。后端 `PrometheusObservabilityService.QueryRange` 重写 PromQL label 后转发 Prometheus `/api/v1/query_range`。

**Rationale:** 按 CLAUDE.md 强制规则"新 Core API 必须先改 OpenAPI 契约"，先新增 schema 再实现。range query 是时序图的标准需求，Prometheus 原生支持。前端时序图组件需改用此端点（留待前端 issue 处理）。

#### D8. 正则 pod matcher 兼容 Deployment hash 后缀

**Ambiguity:** SPEC §5.2.1 模板注入 `pod={{pod}}` 暗示精确匹配，但 Deployment 生成的 pod 名带 ReplicaSet hash 后缀（如 `test-container-6-78d-p428s`），精确匹配查不到数据。

**Choice:** 快照 GetMetrics 与时序 Query 均用 `promQLPodMatcher` 生成 `^name(-.*)?$` 正则，用 `pod=~"..."` 正则匹配。同时 CPU/内存查询加 `container!="",container!="POD"` 过滤 pause container，用 `sum()` 聚合消除多 series 命中时取值非确定性。

**Rationale:** 实例创建走 Deployment 渲染，K8s 生成的 pod 名必然带 hash 后缀，精确匹配无法工作。`sum()` 聚合保证多 pod 命中时返回聚合值而非随机单个 pod 值。

---

### Deviations（增量）

#### DV3. dryrun_renderer 把 CPU/Memory 同时写入 limits 和 requests

**Spec:** SPEC 未明确 resources 映射到 K8s `limits`/`requests` 的策略。

**Implementation:** 发现 `containerResources` 只把 `spec.Resources.CPU`/`Memory` 放进 `requests`（调度保证），没放进 `limits`（硬限制）。导致 `container_spec_memory_limit_bytes=0`，内存利用率 `used/limit` 除零 → +Inf，快照 `memory_total_mb=null`（O1 问题），时序图内存线无数据。修复：CPU 和 Memory 同时写入 `limits` 和 `requests`。

**Why:** 用户创建实例时设置的 memory（如 2Gi）应作为硬限制生效，否则 Prometheus limit 指标为 0，观测链路无法计算利用率。这是渲染层 bug，不是 SPEC 要求。此修复直接解决了 O1（memory_total_mb 采集来源）。

#### DV4. 前端 promqlTemplates 加 container 过滤

**Spec:** SPEC §5.2 PromQL 模板注入方案未提及 container label 过滤。

**Implementation:** 发现前端 CPU/内存模板没有过滤 pause container 和 pod 级聚合，cAdvisor 对同一 pod 返回多条 series（无 container label 的 pod 级聚合 + 有 container label 的业务容器），时序图画出重复曲线。给 CPU/内存模板加 `container!="",container!="POD"`，GPU 模板用 DCGM 指标（无 container label）不加。

**Why:** 这是前端模板的过滤条件修正，不影响注入契约。此修复是 live gate 验证发现的必要修正。

---

### Tradeoffs（增量）

#### T3. Instant Query vs Range Query（时序图数据获取）

| 方案 | 优点 | 缺点 |
|------|------|------|
| Instant query（原有） | 实现简单、单次 HTTP 调用 | 只返回一个点，无法画线 |
| **Range query（选定）** | 返回时间区间多个采样点，能画时序曲线 | 新增端点 + 前端需改用 |

**选定理由:** 时序图的核心需求是显示趋势曲线，instant query 只能显示一个点。range query 是 Prometheus 的标准时序查询方式。

---

### Open Questions（增量）

#### O4. 前端时序图需改用 range query 端点

后端 `GET /observability/query_range` 已就绪，但前端 `MetricsChart.tsx` 仍调用 `GET /observability/query`（instant query）。前端需改用 range query，传 `start`（当前时间-range）、`end`（当前时间）、`step`（按 range 选 15s/30s/1m），把返回的 `results[].values[]` 喂给 ECharts。留待前端 issue 处理。

#### O5. Logs 链路仍用精确 pod 名匹配

`ListLogs` 用 K8s pod log API（需精确 pod 名，不支持正则）。Deployment 场景下 pod 名带 hash 后缀，`ListLogs` 会 404。需要先 list pods 再查日志，改动较大。本批次未修，待后续 issue 处理。

---

### Files Changed（增量）

| File | Status | Summary |
|------|--------|---------|
| `pkg/adapters/runtime/prometheus_observability_service.go` | NEW | `PrometheusObservabilityService` 实现 `ObservabilityService`，Query/QueryRange 重写 PromQL label 后转发 Prometheus |
| `pkg/adapters/runtime/prometheus_observability_service_test.go` | NEW | 6 个测试：label 重写、Query 转发、降级、Inf 过滤、QueryRange、QueryRange Inf 过滤 |
| `services/ani-gateway/observability_runtime.go` | NEW | 按 `INSTANCE_OBSERVABILITY_PROVIDER` env 装配 ObservabilityService |
| `api/openapi/v1.yaml` | MODIFIED | 新增 `/observability/query_range` 端点 + `ObservabilityRangeQueryResponse` schema |
| `pkg/ports/observability.go` | MODIFIED | 新增 `QueryRange` 接口方法 + `ObservabilityRangeQueryRequest`/`ObservabilityRangeSeries`/`ObservabilityRangeQueryResult` 类型 |
| `pkg/adapters/runtime/local_observability_service.go` | MODIFIED | `QueryRange` 返回空 matrix |
| `services/ani-gateway/internal/router/observability.go` | MODIFIED | 新增 `queryRange` handler + 路由注册 + 响应转换 |
| `services/ani-gateway/internal/router/router.go` | MODIFIED | `RegisterOptions.ObservabilityService` 字段 + 注入 |
| `services/ani-gateway/main.go` | MODIFIED | 装配 `newGatewayObservabilityService` + 注入 |
| `pkg/adapters/runtime/prometheus_instance_observability.go` | MODIFIED | 正则 pod matcher、`container!="",container!="POD"` 过滤、`sum()` 聚合、NaN/Inf 过滤、新增 `memory_total_mb` 查询 |
| `pkg/adapters/runtime/prometheus_instance_observability_test.go` | MODIFIED | 新增 `container_spec_memory_limit_bytes` mock + `memory_total_mb` 断言更新 |
| `pkg/adapters/runtime/dryrun_renderer.go` | MODIFIED | `containerResources` 把 CPU/Memory 同时写入 limits 和 requests |
| `frontends/console/src/features/instance-observability/promqlTemplates.ts` | MODIFIED | CPU/内存模板加 `container!="",container!="POD"` 过滤 |
