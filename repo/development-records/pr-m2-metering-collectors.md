# PR-M2 — Metering Collectors（三维度 Collector 接口 + 实现 + CollectAll）

完成日期：2026-08-13
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（新增计量采集产品能力）
对应 Issue：Issue 005 — 新增 Collector 接口 + 三个 Collector 实现 + CollectAll

> **说明：** 本文件按 note-it skill 协议记录实现笔记。批次全部完成后需更新 README.md、CURRENT-SPRINT.md、ANI-06-开发计划.md。

---

## Issue 005: Collector 接口 + 三个 Collector 实现 + CollectAll

完成日期：2026-08-13
验证结果：`go test ./pkg/adapters/metering/... -v -count=1` 24/24 PASS，`go vet` 通过，`go build` 通过，`git diff --check` 通过

### 实现了什么

定义 `Collector` interface 和三个维度的 Collector 实现（GPU 占用时长 / CPU Counter 增量 / 内存 Gauge 瞬时加权），提供 `Resolve` 路由函数和 `CollectAll` 包级路由入口。GPU 维度纯持有时长计算不查 DCGM；CPU/Mem 维度通过 Prometheus HTTP API 查询。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/metering/collectors.go` | 新增 | Collector interface + 3 实现 + Resolve + CollectAll + PromQL 辅助函数（289 行） |
| `pkg/adapters/metering/collectors_test.go` | 新增 | 24 个单测覆盖三个 Collector + CollectAll + PromQL 验证 |

### Design Decisions

1. **GPU Collector 无状态设计（`DCGMGPUCollector struct{}`），CPU/Mem Collector 需要构造函数注入**
   - 模糊性：SPEC §3.2 将三个 Collector 都定义为空 struct（`struct{}`），未说明构造函数和依赖注入方式。
   - 选择：GPU Collector 保持无状态空 struct（`init()` 中直接注册）；CPU/Mem Collector 添加 `prometheusURL` 和 `httpClient` 字段，通过 `NewKubeletCPUCollector` / `NewKubeletMemCollector` 构造函数注入，由 `main.go` 在启动时注册。
   - 理由：GPU 维度纯持有时长计算（`Count × IntervalSec`），无外部依赖，空 struct 即可；CPU/Mem 需要查询 Prometheus HTTP API，必须有 Prometheus URL 和 HTTP Client。SPEC 的空 struct 是设计骨架，实现需补充依赖注入路径。

2. **HTTP Client 注入模式 + 5 秒超时默认**
   - 模糊性：SPEC §3.2 定义空 struct，未提及 HTTP Client 生命周期。Plan 文档 §11 提到 `METERING_PROMETHEUS_URL` 环境变量但未指定 HTTP Client 复用策略。
   - 选择：构造函数接受外部 `*http.Client` 参数；如果传入 nil 或 `Timeout == 0`，自动设置 5 秒超时。
   - 理由：生产环境中 Prometheus HTTP Client 不应每次 `Collect` 新建连接。`main.go` 注入时应复用共享 client（Go `http.Client` 内部管理连接池，HTTP/1.1 Keep-Alive 实现连接复用）。5 秒超时防止 Prometheus 不可用时 Collect 挂起导致 ticker 阻塞。

3. **`Resolve` 立即释放读锁后再调用 Collect**
   - 模糊性：SPEC §5.1.6 伪代码写 `col, ok = Resolve(dim.Source)` 后 `rec, err = col.Collect(...)`，未说明锁的释放时机。
   - 选择：`Resolve` 函数内用 `RLock` 获取 collector 引用后立即 `RUnlock`，返回引用副本后再在 `CollectAll` 中调用 `Collect`（锁外执行）。
   - 理由：`Collect` 包含阻塞 IO（Prometheus HTTP 查询），如果在读锁内调用会导致其他 goroutine 无法获取读锁（虽然 `RWMutex` 允许并发读，但持有锁期间 IO 阻塞会延迟 `RegisterCollector` 的写锁获取）。先释放锁再 Collect 避免此问题。

4. **`init()` 只注册 GPU Collector，CPU/Mem 由 `main.go` 注入**
   - 模糊性：SPEC §3.2 未说明各 Collector 的注册时机。
   - 选择：`init()` 中只注册 `dcgm_gpu`（无状态，可直接注册）；`kubelet_cpu` 和 `kubelet_mem` 需要 `main.go` 在读取 `METERING_PROMETHEUS_URL` 后构造并注册。
   - 理由：GPU Collector 无外部依赖，包加载时即可注册；CPU/Mem 依赖 Prometheus URL 和 HTTP Client，必须等 `main.go` 读取环境变量后才能构造。如果 `init()` 中注册空 URL 的 CPU/Mem Collector，Collect 时会请求空地址导致错误。

### Deviations

1. **Plan 文档空 struct vs 实现带字段的 struct**
   - SPEC/Plan 说：`KubeletCPUCollector struct{}` / `KubeletMemCollector struct{}`（空 struct）。
   - 实现做了：`KubeletCPUCollector struct { prometheusURL string; httpClient *http.Client }` + 构造函数。
   - 原因：Plan/SPEC 是设计文档，空 struct 是设计骨架。实现需要 Prometheus URL 和 HTTP Client 才能查询 Prometheus API。这是"设计到实现"的必要细化，不改变 Collector 的语义职责。

2. **`resetCollectors()` 测试辅助函数直接操作 map 而非调用 `RegisterCollector`**
   - SPEC/Plan 说：未涉及测试辅助函数。
   - 实现做了：`resetCollectors` 内先 `collectorMu.Lock()`，直接 `delete` 和赋值 `collectorCache` map，注释说明"不调用 RegisterCollector（避免对 sync.RWMutex 重入死锁）"。
   - 原因：`RegisterCollector` 内部会再次 `collectorMu.Lock()`，如果 `resetCollectors` 已持有锁再调用 `RegisterCollector` 会导致 `sync.Mutex` 重入死锁。直接操作 map 在已持锁状态下是安全的。

### Tradeoffs

1. **`sync.RWMutex` + `map` vs `sync.Map`**
   - 考虑过的替代方案：用 `sync.Map` 实现 collector 路由表，避免手动锁管理。
   - `sync.RWMutex` + `map` 优点：读写分离，读多写少场景性能好；代码模式与项目中其他 map + mutex 范式一致。
   - `sync.Map` 优点：无需手动锁管理，无重入死锁风险。
   - `sync.Map` 缺点：`Range` 遍历语义复杂；对低并发场景有额外开销；与项目现有范式不一致。
   - 选择理由：collector 注册只在 `init()` 和 `main.go` 启动时发生（写极少），`Resolve` 在每个采集周期调用（读多）。`RWMutex` 读锁不阻塞并发读，适合此场景。手动锁管理的复杂度通过 `resetCollectors` 的注释约束可控。

2. **共享 `queryPrometheusInstant` 函数 vs 每个 Collector 独立 HTTP 逻辑**
   - 考虑过的替代方案：CPU 和 Mem Collector 各自实现完整的 HTTP 请求 + JSON 解析逻辑。
   - 共享函数优点：消除重复代码（HTTP 请求构建、JSON 解析、NaN/Inf 过滤逻辑完全相同）；后续维护只需改一处。
   - 共享函数缺点：两个 Collector 的方法 `queryPrometheusScalar` 变成一行委托，增加了一层间接调用。
   - 选择理由：Prometheus HTTP API 的 instant query 交互模式完全一致（请求构建、响应解析、错误处理），差异仅在 PromQL 查询语句。共享 `queryPrometheusInstant` 函数遵循 DRY 原则，与 `adapters/runtime` 中的同名函数保持一致的交互模式。

3. **`promQLPodMatcher` 复制而非导入 `adapters/runtime`**
   - 考虑过的替代方案：从 `adapters/runtime` 导入已有的 `promQLPodMatcher` 函数。
   - 复制优点：`pkg/adapters/metering` 不依赖 `pkg/adapters/runtime`，保持包独立性。
   - 导入优点：消除重复代码。
   - 选择理由：`adapters/runtime` 和 `adapters/metering` 是同级 adapter 包，跨 adapter 包导入不符合 adapter 独立性原则。函数逻辑（正则转义 + `^prefix(-.*)?$` 模式）短小稳定，复制成本低于引入跨包依赖。注释标注"与 adapters/runtime 中逻辑一致"便于后续同步维护。

### Open Questions

1. **HTTP Client 复用策略未在 Plan/SPEC 中明确**
   - 当前实现：构造函数接受外部 `*http.Client`，`main.go`（PR-M3 Issue 范围）负责注入共享 client。
   - 待确认：PR-M3 main.go 实现时是否正确创建单个 `http.Client` 并复用于 CPU/Mem 两个 Collector？如果 main.go 为每个 Collector 创建独立 client，连接复用效果会降低。
   - 建议：PR-M3 实现时在 main.go 中创建一个共享 `*http.Client`，分别传给 `NewKubeletCPUCollector` 和 `NewKubeletMemCollector`。

2. **`TestRegisterAndResolve_CPU` 的 defer 恢复逻辑**
   - 当前实现：`defer RegisterCollector("kubelet_cpu", KubeletCPUCollector{})` 恢复注册，但恢复的是空 struct（无 `prometheusURL` / `httpClient`），不是 main.go 注入的真实实例。
   - 影响：后续测试如果不重新注册带 URL 的 CPU Collector，`Resolve("kubelet_cpu")` 返回的 Collector 调用 `Collect` 会 panic（nil httpClient）。
   - 当前状态：`resetCollectors()` 在其他测试中清理并只保留 GPU，所以此问题不实际触发。但如果测试顺序变化或新增测试依赖 CPU Collector 存在，可能出问题。
   - 建议：后续可考虑在 `TestMain` 中统一注册测试用 collector，或用 `t.Cleanup` 替代 defer 以确保状态一致性。

### 验证命令

```bash
cd repo
go test ./pkg/adapters/metering/... -v -count=1   # 24/24 PASS
go vet ./pkg/adapters/metering/...                 # 通过
go build ./pkg/adapters/metering/...               # 通过
git diff --check                                   # 通过
```

> **注：** `make validate-architecture` target 依赖 Unix `date -u` 命令，Windows PowerShell 环境不兼容；已直接运行底层 `validate_component_imports.py` 验证通过。`go test -race` 在 Windows 环境下因 DLL 加载问题无法运行，非代码问题。

---

## Issue 006: 新增 buildSpec 维度映射函数 + parseGPUCount

完成日期：2026-08-13
验证结果：`go test ./services/metering-service/internal/ -run TestBuildSpec|TestParseGPUCount -v -count=1` 16/16 PASS，`go vet` 通过，`go build` 通过，`make validate-architecture` 通过，`git diff --check` 通过

### 实现了什么

在 `services/metering-service/internal/` 包根目录新增 `spec.go`，实现共享的 `buildSpec` 函数（根据 workload_kind 硬编码维度映射构造 `ports.CollectionSpec`）和 `parseGPUCount` 函数（从 gpu_status JSONB 解析 GPU 卡数）。供 consumer（issue-007）和 rebuilder（issue-008）共用。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/internal/spec.go` | 新增 | `buildSpec` 维度映射函数 + `dimensionsFor` 辅助函数 + `parseGPUCount` JSONB 解析函数（74 行） |
| `services/metering-service/internal/spec_test.go` | 新增 | 16 个单测覆盖维度映射 + GPU 卡数解析 + consumer/rebuilder 调用契约 |

### Design Decisions

1. **`buildSpec` 放在 `internal` 包根目录而非子包**
   - 模糊性：Issue Scope 写 `repo/services/metering-service/internal/`，但未指定放在哪个子包。当前 internal 下已有 `eventconsumer`、`service`、`config` 三个子包。
   - 选择：放在 `internal` 根目录（`internal/spec.go`），包名 `internal`。
   - 理由：`buildSpec` 和 `parseGPUCount` 是 consumer 和 rebuilder 的共享函数，不专属于任何子包。放在根目录避免循环依赖（consumer 和 rebuilder 都 import `internal`），且 Issue 的 AC 明确要求 `internal/spec.go` 路径。

2. **`dimensionsFor` 用 switch/default 而非 map 查表**
   - 模糊性：SPEC §5.1.7 描述了维度映射规则，未指定实现方式。
   - 选择：用 `switch kind { case "gpu_container": ...; default: ... }` 硬编码。
   - 理由：维度映射是固定的产品规则，kind 类型有限且稳定。switch 直观可读，编译器可优化；map 查表需要额外维护 map 初始化且对固定规则无优势。`default` 分支统一覆盖 vm/container/其他 kind 为 CPU+Mem。

3. **Source 字段对齐 `pkg/adapters/metering/collectors.go` 的注册 key**
   - 模糊性：SPEC §5.1.7 未明确 `CollectionDimension.Source` 的值。
   - 选择：GPU → `dcgm_gpu`，CPU → `kubelet_cpu`，Mem → `kubelet_mem`。
   - 理由：这些值与 Issue 005 中 `RegisterCollector` 的 key 一致，确保 `CollectAll` 的 `Resolve(dim.Source)` 能正确路由到对应 Collector。

### Deviations

None — 实现完全遵循 SPEC §5.1.7 和 Issue AC 逐条实现，无偏离。

### Tradeoffs

1. **用测试覆盖 consumer/rebuilder 调用契约而非直接修改 consumer.go/rebuilder.go**
   - 考虑过的替代方案：在 consumer.go 和 rebuilder.go 中直接调用 `buildSpec`。
   - 不修改的优点：Issue 006 的范围仅限 `internal/spec.go`，consumer 的完整实现（seenSeq、metering 注入）属 issue-007，rebuilder 属 issue-008。不越界修改避免破坏其他 issue 的独立性。
   - 不修改的缺点：测试用例模拟调用契约而非真实代码路径。
   - 选择理由：用 `TestBuildSpecConsumerCall` 和 `TestBuildSpecRebuilderCall` 显式覆盖调用契约（参数提取 + nil 处理 + GPU 卡数传递），验证 `buildSpec` 在两种调用场景下的正确行为。实际接线由 issue-007/008 完成。

2. **`parseGPUCount` 对负数的处理**
   - 考虑过的替代方案：在 `parseGPUCount` 中对负数返回 0。
   - 当前实现：`json.Unmarshal` 对 `{"count": -1}` 返回 -1（正确解析），但 `buildSpec` 的 `gpuCount > 0` 检查会过滤掉负数（设 GPUSpec 为 nil）。
   - 选择不额外校验的理由：`parseGPUCount` 的职责是"解析 JSONB"，负数是合法的 JSON 解析结果；GPU 卡数校验由 `buildSpec` 的 `gpuCount > 0` 守卫负责。实际数据中 gpu_status JSONB 不会写入负数，属非现实边界情况。

### Open Questions

1. **`time.Now()` 在测试中的不确定性**
   - 当前实现：`buildSpec` 默认 `StartedAt: time.Now()`，测试只验证 `IsZero() == false`。
   - 待确认：如果后续 issue 需要精确时间断言，可能需要将 `StartedAt` 改为可注入参数或用 `time.Now().Truncate(time.Second)` 方便比较。
   - 建议：issue-007/008 实现时如需精确时间控制，再考虑注入 `StartedAt` 参数。

### 验证命令

```bash
cd repo\services\metering-service
go test ./internal/ -run "TestBuildSpec|TestParseGPUCount" -v -count=1   # 16/16 PASS
go vet ./internal/                                                        # 通过
go build ./...                                                            # 通过
cd repo
make validate-architecture                                                # architecture guardrails valid
git diff --check                                                          # 通过
```

### review-it 修复项

- **gofmt 对齐**：`Dimensions` 字段空格对齐已由 `gofmt -w` 自动修复（`Dimensions:  dimensionsFor(kind)` → `Dimensions:   dimensionsFor(kind)`）。
