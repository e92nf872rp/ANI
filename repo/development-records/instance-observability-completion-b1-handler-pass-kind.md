# instance-observability-completion B-1 — handler 透传 Kind 修复 GPU 死分支

完成日期：2026-07-20
对应 Sprint：Sprint 15（Console Instance Observability Completion）
批次：B-1（handler 传 Kind + GPU 分支验证）
对应 Issue：issue-001-handler-pass-kind
验证结果：`make test` EXIT:0；`make validate-architecture` passed；`git diff --check` passed；`go vet` + `gofmt -l` 无输出

## 实现了什么

在 `demo_instances.go` 的 `getMetrics` handler 中,给 `ports.InstanceObservationGetRequest` 字面量新增 `Kind: record.Kind` 字段,修复 `PrometheusInstanceObservability.GetMetrics` 在 `request.Kind == ports.WorkloadKindGPUContainer` 分支恒不触发的死分支问题,使 `gpu_container` 实例的 GPU 利用率与显存指标在生产路径下能正确填充。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | `getMetrics` handler 新增 `Kind: record.Kind` 字段透传 |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 新增 `metricsKindSpy` 与 `TestDemoInstanceGetMetricsHandlerPassesRecordKind` 覆盖 container/gpu_container/vm 三种路径 |

## 完工标准达成

- [x] 修改 `demo_instances.go` 的 `getMetrics` 调用,新增 `Kind: record.Kind` 字段
- [x] 生产路径下 `request.Kind` 等于 `record.Kind`,不再是空字符串(三种 kind 测试断言通过)
- [x] 现有 `container` kind 指标行为不回归(`make test` 全量通过)
- [x] Typecheck/lint passes(`go vet` + `gofmt -l` 无输出)
- [x] 单元测试覆盖 `kind=container`、`kind=gpu_container`、`kind=vm` 三种路径,断言传入 adapter 的 `request.Kind` 值(3/3 subtests PASS)
- [x] `make validate-architecture` 通过
- [x] `git diff --check` 通过

## Design Decisions

### 1. 透传 `record.Kind` 而不是在 handler 内做 kind 分支

- **歧义点**: SPEC §5.1 给出示例 `Kind: record.Kind`,但未明确说明 handler 是否应基于 kind 做不同处理或纯粹透传。
- **选择**: 单纯透传 `record.Kind` 到 `InstanceObservationGetRequest.Kind`,不在 handler 内做任何 kind 判断或分支。
- **理由**: handler 层职责是 HTTP 解码 + 调用 adapter;kind 分支属于 adapter 的指标采集策略(DCGM vs kubevirt promql),放在 adapter 才符合 ports/adapters 分层。handler 透传 Kind 让 adapter 自主决策,保持 handler 单一职责。

### 2. 测试使用端到端 HTTP 路径而非直接调用 `api.getMetrics`

- **歧义点**: SPEC §9.1 要求 "handler 单测",但未明确是直接调 handler 方法还是通过 HTTP。
- **选择**: 通过 Hertz `ut.PerformRequest` 端到端测试,先 POST `/api/v1/instances` 创建实例,再 GET `/api/v1/instances/:id/metrics`,用 `metricsKindSpy` 捕获传入 adapter 的 `request.Kind`。
- **理由**: 直接调用 `api.getMetrics` 需要构造 `*app.RequestContext`,参数复杂且绕过了路由匹配和中间件链;端到端测试更接近生产路径,能验证整个请求链路(路由 → tenant 中间件 → `instanceForObservation` → `getMetrics` → adapter)的正确性。

## Deviations

None — 实现严格遵循 SPEC §5.1 示例和 Issue AC,SPEC 给出的代码示例 `Kind: record.Kind` 直接落地,未做任何偏离。

## Tradeoffs

### 1. `metricsKindSpy` 仅捕获 `GetMetrics`,其他 5 个方法返回零值

- **备选方案 A**(采用): spy 仅实现 `GetMetrics` 的捕获逻辑,其他方法(`ListLogs`/`ListEvents`/`ListSecurityEvents`/`CreateExecSession`/`CreateConsoleSession`)返回零值空响应。
- **备选方案 B**: spy 委托给 `LocalInstanceObservabilityService` 的默认实现,所有方法都走真实本地逻辑。
- **取舍**: 方案 A 更简单,测试聚焦于 `request.Kind` 透传这一单一职责,不引入对 local adapter 行为的间接依赖;方案 B 会让测试耦合 local adapter 的实现细节,违反最小测试 mock 原则。选 A。

### 2. `metricsKindSpy` 使用 `sync.Mutex` 保护 `capturedKind`

- **备选方案 A**(采用): 用 `sync.Mutex` 保护 `capturedKind` 字段的读写。
- **备选方案 B**: 不加锁,依赖测试单线程假设。
- **取舍**: 虽然当前测试是单 goroutine 调用,但 Hertz handler 可能并发执行;加锁符合并发安全惯例,开销可忽略,且避免未来扩展测试时引入 race condition。选 A。

## Open Questions

1. **本批次仅修复 handler 透传,未触碰 adapter 的 GPU 分支实现**。`PrometheusInstanceObservability.GetMetrics` 的 DCGM 查询分支(line 172)是否已在 Sprint 13 real-provider live gate 中验证可查询到真实 DCGM exporter 指标?需在后续批次(issue-002-prometheus-dcgm-scrape / issue-003-gpu-adapter-e2e-verify)中通过真实 live gate 证据确认,本批次不覆盖 adapter 端到端验证。

2. **VM kind 的指标采集链路**(issue-009-getmetrics-vm-branch)依赖本次 `Kind: record.Kind` 透传作为前置,但 SPEC §5.1 未明确 VM 分支在 adapter 侧的具体实现。本批次测试仅断言 `request.Kind == "vm"`,未验证 VM 指标是否被 adapter 正确采集,需在 issue-009 中补全。

## Verification commands run

```bash
# 1. 单元测试(三种 kind 路径)
go test -run TestDemoInstanceGetMetricsHandlerPassesRecordKind -v ./services/ani-gateway/internal/router/...
# 结果: 3/3 subtests PASS (container/gpu_container/vm)

# 2. 全量测试
make test
# 结果: EXIT 0, 无 FAIL

# 3. 架构边界
make validate-architecture
# 结果: architecture guardrails valid

# 4. 空白检查
git diff --check
# 结果: 无空白错误

# 5. Typecheck/lint
go vet ./services/ani-gateway/internal/router/...
gofmt -l services/ani-gateway/internal/router/demo_instances.go services/ani-gateway/internal/router/demo_instances_test.go
# 结果: 均无输出
```

## 备注

- 本次变更未触碰 Core API 契约(`repo/api/openapi/v1.yaml`),仅修改 handler 内部调用逻辑,无破坏性变更。
- 本次变更未触碰 `pkg/ports/instance_observability.go` 或 `pkg/adapters/runtime/prometheus_instance_observability.go`,`InstanceObservationGetRequest.Kind` 字段与 adapter GPU 分支检查已在前序 Sprint 存在,handler 透传是补齐链路的最后一步。
- 未提交,未推送,未创建 PR — 遵循 Issue 约束,等待用户显式 `/ship-it`。
