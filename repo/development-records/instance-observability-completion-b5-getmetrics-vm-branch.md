# INSTANCE-OBSERVABILITY-COMPLETION-B5-GETMETRICS-VM-BRANCH

> 批次类型：Feature batch（GetMetrics 新增 VM 分支，查询 KubeVirt kubevirt_vmi_* 指标）
> 关联 PRD：`services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
> 关联 SPEC：`services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md`
> 关联 UX：`services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md`
> 关联 Issue：`services/tasks/modules/issue/console/compute/instance-observability-completion/issue-009-getmetrics-vm-branch.md`
> 关联批次：B-5（与 b5-prometheus-kubevirt-scrape 同批次）
> 完成日期：2026-07-22

## 背景

Issue-009 要求在 `PrometheusInstanceObservability.GetMetrics` 方法新增 `if request.Kind == ports.WorkloadKindVM` 分支，位于现有 GPU 分支之前。VM 分支查询 5 个 `kubevirt_vmi_*` 指标（CPU/内存/网络），label 用 `name="<vmi-name>"` 精确匹配，内存使用率用 `domain_bytes - usable_bytes` 公式计算。

本次改动只触碰 `repo/pkg/adapters/runtime/prometheus_instance_observability.go` 和对应测试文件，不涉及 OpenAPI、Gateway、前端。

## 1. Design Decisions

### 1.1 resident_bytes 查询结果不赋给任何字段

**Ambiguity:** SPEC §5.2 代码示例（L598-609）将 `kubevirt_vmi_memory_resident_bytes` 赋给 `memUsedQuery`（注释"内存 used"），但同一注释块又说"内存使用率公式：domain_bytes - usable_bytes（FR-17）"——SPEC 示例存在内部矛盾。AC2 要求"查询 resident_bytes"作为"内存已用"指标，但 AC5/FR-17 又说"不得直接用 resident_bytes 作为使用率分子"。

**Choice:** 查询 `kubevirt_vmi_memory_resident_bytes`（满足 AC2/FR-15"查询"硬性要求），但查询结果不赋给 `MemoryUsedMB` 或任何字段，仅更新 `record.Timestamp`。`MemoryUsedMB` 由 `domain_bytes - usable_bytes` 公式计算（满足 AC5/FR-17）。

**Rationale:**
- AC2 和 FR-15 明确要求"VM 分支查询指标：kubevirt_vmi_memory_resident_bytes"——查询动作是硬性要求，测试也验证查询发生
- FR-17 明确"不得直接用 resident_bytes 作为使用率分子"——`MemoryUsedMB` 必须用 `domain - usable` 计算
- 两者同时满足的唯一方式：查询 resident_bytes 但结果不赋给使用率字段
- resident_bytes 的语义是 guest 物理内存驻留量，与"使用率"概念不同；查询它可为将来扩展（如暴露 resident 原始值字段）预留数据源

### 1.2 MemoryUsedMB 计算顺序与 domain 查询失败降级

**Ambiguity:** `domain_bytes` 和 `usable_bytes` 两个查询独立执行，如果 domain 查询失败但 usable 查询成功，`used = domain - usable` 会得到负数或无意义值。SPEC 未明确这种降级场景的处理。

**Choice:** 用 `memDomainBytes > 0` 守卫 usable 查询——domain 查询失败时 `memDomainBytes` 保持 0，usable 查询被跳过，`MemoryUsedMB` 保持 nil。

**Rationale:**
- 没有 total 就无法计算 used（used = domain - usable 依赖 domain），跳过 usable 查询避免无效 Prometheus 往返
- 与现有 container 分支 `limit > 0` 守卫（L201）语义一致：limit 查询失败时不计算 used
- virt-handler 不可用时所有字段为 nil，不伪造 0（延续单 exporter 降级语义）

### 1.3 VM 分支用 `request.InstanceID` 作为 `name` label 值

**Ambiguity:** AC4 说"VMI metadata.name 等于 record.Name"，但 GetMetrics 方法签名中没有 `record.Name`，只有 `request.InstanceID`。SPEC 示例用 `record.Name`（L588），但实际代码中 `pod := request.InstanceID`（L160）。

**Choice:** 用 `request.InstanceID` 作为 `name` label 值，传入 `getMetricsForVM` 的 `vmiName` 参数。

**Rationale:**
- VMI `metadata.name` = `record.Name`，而 `record.Name` 在 GetMetrics 调用链中等价于 `request.InstanceID`（实例 ID 即 VMI name，无随机后缀）
- GetMetrics 方法不查询 store 获取 record，直接用 `request.InstanceID` 推导（与现有 container 分支用 `pod := request.InstanceID` 一致）
- AC4 的"record.Name"是 SPEC 语境的概念名，实际代码对应 `request.InstanceID`

## 2. Deviations

### 2.1 SPEC §5.2 代码示例的 resident_bytes 赋值矛盾

**Spec:** SPEC §5.2 示例（L598-609）将 `kubevirt_vmi_memory_resident_bytes` 赋给 `memUsedQuery`（注释"内存 used"），但同一注释块又说"使用率公式：domain_bytes - usable_bytes（FR-17）"——示例代码将 resident_bytes 作为内存已用查询，但注释又说使用率不用 resident_bytes。

**Implementation:** 查询 resident_bytes 但不赋给 `MemoryUsedMB`；`MemoryUsedMB` 由 `domain_bytes - usable_bytes` 计算。

**Why:** PRD FR-17 是强制约束（"不得直接用 resident_bytes 作为使用率分子"），优先级高于 SPEC 示例代码的赋值。AC2/FR-15 要求"查询"resident_bytes 也已满足。这是 SPEC 示例本身的内部矛盾，实现以 PRD FR-17 为准。

### 2.2 SPEC §5.2 示例签名与实际代码签名不同

**Spec:** SPEC 示例方法签名为 `getMetricsForVM(ctx, record *InstanceRecord, req ports.InstanceObservationGetRequest)`，从 record 取 `record.Name` 和 `record.TenantID`。

**Implementation:** 实际签名为 `getMetricsForVM(ctx, namespace, vmiName string, record ports.InstanceMetricsRecord)`，namespace 和 vmiName 由调用方传入。

**Why:** 现有 GetMetrics 方法不查询 store 获取 `*InstanceRecord`，而是用 `request.InstanceID` 推导 pod 名、`tenantNamespace(request.TenantID)` 推导 namespace。VM 分支复用此模式，避免新增 store 查询依赖。`ports.InstanceMetricsRecord` 是返回值类型，与 SPEC 示例的 `*InstanceRecord`（store 实体）不同。

## 3. Tradeoffs

### 3.1 resident_bytes 查询保留 vs 删除

**Alternatives:**
- A. 保留 resident_bytes 查询，结果不赋值（chosen）——满足 AC2/FR-15"查询"硬性要求，测试可验证查询发生
- B. 删除 resident_bytes 查询——简化代码，减少一次 Prometheus 往返，但违反 AC2/FR-15"查询"要求，测试需删除 resident_bytes 断言

**Pros/Cons:** A 满足 AC 但浪费一次网络往返；B 更简洁但违反 AC。AC 是硬性要求，A 胜出。

### 3.2 内存使用率：domain-usable 公式 vs 直查 resident_bytes

**Alternatives:**
- A. `MemoryUsedMB = (domain_bytes - usable_bytes) / 1024 / 1024`（chosen）——PRD FR-17 强制公式
- B. `MemoryUsedMB = resident_bytes / 1024 / 1024`——更简单（单次查询），但违反 FR-17

**Pros/Cons:** A 需要 3 次 Prometheus 查询（resident + domain + usable），B 只需 2 次（resident + domain）。但 FR-17 是强制约束，A 胜出。测试含反向断言验证 `MemoryUsedMB != resident_bytes/1024/1024`。

### 3.3 VM 分支位置：GPU 分支之前 vs 之后

**Alternatives:**
- A. VM 分支位于 GPU 分支之前（chosen）——AC1 明确要求
- B. VM 分支位于 GPU 分支之后——功能等价（kind 互斥），但违反 AC1

**Pros/Cons:** A 满足 AC1；B 也功能正确但违反 AC 字面要求。A 胜出。

## 4. Open Questions

### 4.1 端到端验证需真实 VM 环境（当前无法验证）

**Assumption:** 当前系统没有 VM 实例，无法端到端验证 VM 分支的 Prometheus 查询返回真实数据。单元测试用 mock HTTP server 验证 PromQL 构造和 label 匹配，但无法验证真实 KubeVirt virt-handler exporter 的指标格式、label 值、rate[5m] 窗口行为。

**Should verify:** 等 VM 环境就绪后（依赖 #8 KubeVirt scrape 配置部署 + 创建 VM 实例），需在真实集群执行端到端验证：
1. 创建一个 VM 实例，确认 VMI `metadata.name` 无随机后缀
2. 调用 GetMetrics(kind=vm)，验证 CPU/memory/network 字段非 nil 且数值合理
3. 验证 `MemoryUsedMB` 等于 `domain_bytes - usable_bytes` 的换算值，不等于 `resident_bytes` 的换算值
4. 验证 virt-handler 不可用时字段为 nil（不伪造 0）

### 4.2 resident_bytes 查询结果当前丢弃，将来是否需要暴露

**Assumption:** resident_bytes 查询结果当前仅用于更新 Timestamp，不赋给任何返回字段。如果未来 UX 需要展示"内存驻留量"（与"使用率"区分），需要新增字段（如 `MemoryResidentMB`）。

**Should verify:** PRD FR-15 要求查询 resident_bytes 但未明确其用途。UX §4.2 VM 快照卡片布局是否需要展示 resident 原始值？当前 UX 只展示使用率（used/total），不需要 resident。如未来需要，再扩展 `InstanceMetricsRecord` 结构。

### 4.3 rate[5m] 窗口在低流量场景可能返回 0

**Assumption:** 网络 RX/TX 用 `rate(...[5m])`，如果 VM 网络流量极低（5 分钟内无变化），rate 可能返回 0。这是 Counter rate 的标准行为，非 bug。

**Should verify:** 真实 VM 环境验证时，确认低流量场景 `NetworkRXBytes`/`NetworkTXBytes` 为 0 而非 nil（rate 返回 0 是有效值，与查询失败返回 nil 不同）。

## 验证命令

```bash
cd repo
# VM 分支单元测试
go test ./pkg/adapters/runtime/ -run "TestPrometheusInstanceObservabilityGetMetricsVM" -v
# 结果：3/3 PASS (VMBranch + DoesNotQueryContainerMetrics + VirtHandlerUnavailable)

# 非 VM kind 回归测试
go test ./pkg/adapters/runtime/ -run "TestPrometheusInstanceObservabilityGetMetricsNonVMDoesNotQueryKubeVirt" -v
# 结果：5/5 PASS (container/gpu_container/sandbox/batch_job/notebook)

# 全量测试
make test
# 结果：ok (exit 0)

# 架构门禁
make validate-architecture
# 结果：✅ architecture guardrails valid (exit 0)

# 静态检查
go vet ./pkg/adapters/runtime/
# 结果：无输出 (pass)

# diff 检查
git diff --check
# 结果：仅 CRLF warning（Windows 环境正常），无错误

# 端到端验证（需真实 VM 环境，当前无法执行）
# 等 VM 环境就绪后执行：
# 1. kubectl get vmi -n ani-tenant-<tenant> 确认 VMI 存在且 name 无随机后缀
# 2. curl Prometheus API 确认 kubevirt_vmi_* 指标非空
# 3. 调用 GetMetrics API 验证字段填充
```

## 涉及文件

| 文件 | 改动 |
|---|---|
| `repo/pkg/adapters/runtime/prometheus_instance_observability.go` | GetMetrics 新增 `if request.Kind == ports.WorkloadKindVM` 分支（L171，GPU 分支之前）；新增 `getMetricsForVM` 方法（L253-324），查询 6 个 kubevirt_vmi_* 指标，label 用 `name=%q` 精确匹配，MemoryUsedMB 用 domain-usable 公式计算 |
| `repo/pkg/adapters/runtime/prometheus_instance_observability_test.go` | 新增 4 个 VM 分支测试：TestPrometheusInstanceObservabilityGetMetricsVMBranch（PromQL+label+字段填充+resident_bytes 反向断言）、TestPrometheusInstanceObservabilityGetMetricsVMDoesNotQueryContainerMetrics（不走 container 分支）、TestPrometheusInstanceObservabilityGetMetricsVMVirtHandlerUnavailable（降级 nil）、TestPrometheusInstanceObservabilityGetMetricsNonVMDoesNotQueryKubeVirt（5 种非 VM kind 回归） |
