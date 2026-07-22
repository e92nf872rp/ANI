# INSTANCE-OBSERVABILITY-COMPLETION-B7-VM-PROMQL-TEMPLATES

> 批次 ID: B-7
> Issue: issue-011 Console VM 指标 PromQL 模板与时序图（2 条曲线）
> 日期: 2026-07-22
> 产品线: console
> 代码路径: `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts`、`repo/frontends/console/src/features/instance-observability/MetricsChart.tsx`

## 变更摘要

在 `promqlTemplates.ts` 新增 VM kind 的冻结 PromQL 模板（`instance_vm_cpu_utilization`、`instance_vm_memory_utilization`），使用 `name` label 而非 `pod`。`getTemplatesForKind` 对 `kind=vm` 返回 VM 模板列表（2 条曲线：CPU 利用率、内存使用率），不展示网络 RX/TX 时序曲线。`MetricsChart.tsx` 的 `SERIES_COLORS` 新增 VM 模板配色（复用 container 蓝绿 `#0052D9`/`#2BA471`，语义一致）。`PROMQL_TEMPLATE_LABELS` 新增 VM 模板中文系列名。

- `PromQLTemplateId` 类型新增 `instance_vm_cpu_utilization`、`instance_vm_memory_utilization` 两个 ID
- `PROMQL_TEMPLATES` 新增 2 个 VM 模板正文（与 SPEC §5.6 冻结表逐字符一致）
- `getTemplatesForKind` 由 `if` 分支重写为 `switch` 结构，新增 `case 'vm'` 分支返回 2 条 VM 模板
- `PROMQL_TEMPLATE_LABELS` 新增 VM 中文系列名（CPU 利用率、内存使用率）
- `SERIES_COLORS` 新增 VM 配色（复用蓝/绿）；移除既有未使用的 `Space` import（修复 lint error）
- 依赖 #9 VM 分支后端修复（B-5）和 #10 name label 重写扩展（B-6）

## Implementation Notes / Design Choices

### 1. `getTemplatesForKind` 由 `if` 分支重写为 `switch` 结构

- **歧义**：SPEC §5.7 给出了 `case 'vm'` 的代码片段，但未显式说明是否应将既有的 `if (kind === 'gpu_container')` 结构整体重写为 switch。
- **选择**：将 `getTemplatesForKind` 重写为 `switch (kind)` 结构，`gpu_container`/`vm`/`container`/`sandbox`/`batch_job`/`notebook` 各为独立 case，`default` 兜底。
- **理由**：SPEC §5.7 展示的代码片段即为 switch 结构，重写后与 SPEC 代码片段结构一致；switch 结构对多 kind 分支可读性优于连续 `if`/`else if`，且每个 kind 的返回值一目了然。这是 SPEC 明确展示的结构，非过度设计。

### 2. VM 模板配色复用 container 蓝绿（非新增独立配色）

- **歧义**：Issue AC 只要求「复用 container 配色 `#0052D9`/`#2BA471`」，但未说明复用的语义对齐理由。
- **选择**：`instance_vm_cpu_utilization` 复用 `instance_cpu_utilization` 的蓝 `#0052D9`，`instance_vm_memory_utilization` 复用 `instance_memory_utilization` 的绿 `#2BA471`。
- **理由**：VM CPU 利用率与 container CPU 利用率语义一致（都表示 CPU 使用情况），复用相同色调让用户在切换 kind 时视觉语义连贯；SPEC §5.9 和 UX §8.4 假设均明确支持此复用决策。

### 3. VM 模板正文无 `* 100` 百分比转换

- **歧义**：container 模板的 CPU/内存公式都带 `* 100`（百分比转换），但 SPEC §5.6 冻结表的 VM 模板正文无 `* 100`，而 `MetricsChart` Y 轴显示 `利用率（%）` 且 `max: 100`。
- **选择**：VM 模板正文严格按 SPEC §5.6 冻结表实现，不加 `* 100`。
- **理由**：SPEC §5.6 第 738-739 行冻结表明确冻结了无 `* 100` 的正文，Issue AC #2/#3 也逐字符要求无 `* 100`。前端无权修改冻结模板正文；Y 轴单位与模板正文的百分比换算属上游 SPEC 设计范畴，不在本 issue 范围内。`rate(kubevirt_vmi_cpu_usage_seconds_total[5m])` 本身返回的是 0-1 区间的利用率小数（core 使用率 = CPU 时间增量/时间窗口），与 container 的 `100 * rate(...)` 返回 0-100 不同；这是 SPEC D-12 / PRD FR-17 的既有设计决策。

## Spec Deviations + Rationale

None — 实现严格遵循 SPEC §5.6/§5.7/§5.9 冻结表与 Issue AC，无偏离。

- VM 模板正文与 SPEC §5.6 第 738-739 行逐字符一致（`name` label，无 `* 100`）
- `getTemplatesForKind` 的 `case 'vm'` 分支返回 2 条 VM 模板 — 与 SPEC §5.7 代码片段一致
- `SERIES_COLORS` VM 配色 `#0052D9`/`#2BA471` — 与 SPEC §5.9 第 834-835 行一致
- `PROMQL_TEMPLATE_LABELS` VM 中文系列名 — 与 UX §7.1 标签文案一致

唯一附带改动：移除 `MetricsChart.tsx` 既有的未使用 `Space` import。这不是 SPEC 偏离，而是修复该文件既有的 lint error（`@typescript-eslint/no-unused-vars`），使 `pnpm lint` 能通过。属于「清理自己接触的脏」（Karpathy 原则三），非越界重构。

## Alternatives Considered

### 方案 A（已选）：`switch` 结构重写 `getTemplatesForKind`

- **优点**：与 SPEC §5.7 代码片段结构一致；多 kind 分支可读性优；每个 case 返回值独立、不共享可变 `base` 数组
- **缺点**：diff 较 `if` 分支稍大（+41/-11）
- **风险评估**：无；原有 `if` 逻辑被等价替换，container/gpu_container 行为不变

### 方案 B：保留 `if` 结构，新增 `else if (kind === 'vm')` 分支

- **优点**：diff 最小，只新增几行
- **缺点**：偏离 SPEC §5.7 展示的 switch 结构；连续 `if`/`else if` 在 kind 增多时可读性下降；共享 `base` 数组的 push 模式对 VM（完全不同的模板 ID 集合）不自然
- **未选原因**：SPEC §5.7 明确展示 switch 结构；VM 模板与 container 模板是不同模板 ID 集合，不适合 `base.push` 模式

### 方案 C：为 VM 新增独立 `getVMTemplatesForKind` 函数

- **优点**：VM 逻辑完全隔离
- **缺点**：引入新函数抽象，违反 Karpathy 原则二「不为一次性代码创建抽象层」和原则五「奥卡姆剃刀」；`MetricsChart` 需新增 kind 判断分支选择调用哪个函数，增加调用点复杂度
- **未选原因**：现有 `getTemplatesForKind(kind)` 已是统一的 kind → templateIds 入口，VM 作为新 case 接入即可，无需新增函数

## Follow-ups / Blockers

### VM 端到端 live 验证待补

- **假设**：VM PromQL 模板在真实 VM 环境下应正确渲染时序图（`kubevirt_vmi_cpu_usage_seconds_total`/`kubevirt_vmi_memory_domain_bytes`/`kubevirt_vmi_memory_usable_bytes` 指标带 `name` label，依赖 B-5 KubeVirt virt-handler scrape 配置 + B-6 `rewritePromQLLabels` name label 重写）
- **当前状态**：当前系统无 VM，无法进行浏览器端 loading/empty/error 三态的端到端验证。type-check/lint/build 均通过，但仅为静态验证。
- **待验证**：等 VM 环境就绪后，需补齐浏览器端三态验证：
  1. **loading 态**：VM 实例 → 指标 Tab → 时序图首屏显示 `Loading text="加载趋势数据中…"`（高度 320px 居中）
  2. **empty 态**：KubeVirt virt-handler 不可用 → `Empty description="所选时间范围暂无数据"`
  3. **error 态**：Prometheus 查询失败 → `Alert theme="error"` + 错误消息 + 请求 ID + 重试；403 → `Alert theme="warning"`「无权限查看趋势数据」
  4. **idle 态**：2 条曲线（CPU 蓝、内存绿），legend 显示「CPU 利用率」「内存使用率」
  5. **range-change**：切换 15m/1h/6h/24h → loading 后刷新
- **用户确认**：用户明确指示「现在由于系统还没有VM，所以就没有测试，等VM有了后再测」

### VM 模板 Y 轴单位一致性（上游 SPEC 范畴）

- **假设**：VM 模板无 `* 100`，`rate()` 返回 0-1 区间小数，但 `MetricsChart` Y 轴 `max: 100` 显示「利用率（%）」。这可能导致 VM 曲线在 0-1 区间显示，与 Y 轴 0-100 刻度不匹配。
- **当前状态**：本 issue 严格按 SPEC §5.6 冻结表实现，不修改模板正文。Y 轴单位一致性属上游 SPEC 设计范畴。
- **待确认**：等 VM 环境就绪后，实际渲染时序图后确认 Y 轴显示是否符合预期。若 VM 曲线因无 `* 100` 而显示在 0-1 区间，需上游 SPEC 评估是否调整 VM 模板正文或 Y 轴配置（不在本 issue 范围）。

## Verification Commands Run

```bash
# TypeScript 类型检查
cd repo/frontends/console && pnpm type-check
# 结果: PASS (tsc --noEmit, exit 0)

# ESLint
cd repo/frontends/console && pnpm lint
# 结果: PASS (0 errors, 1 warning 在 gpu-containers/create-dialog.tsx，与本 issue 无关)

# 生产构建
cd repo/frontends/console && pnpm build
# 结果: PASS (vite build, 5850 modules transformed, exit 0)

# 架构守卫
cd repo && make validate-architecture
# 结果: PASS (component import guard passed)

# 空白错误检查
cd repo && git diff --check -- frontends/console/src/features/instance-observability/
# 结果: PASS (无空白错误)

# 行尾空格检查
# 结果: 两文件均无行尾空格（Grep " +$" 无匹配）

# 单元测试
# cd repo/frontends/console && pnpm test
# 结果: 项目未定义 test script，跳过

# Go 测试
# cd repo && make test
# 结果: FAIL — Go 测试失败来自工作区预先存在的 B-1~B-6 后端改动，与本前端 issue 无关
```

## Changed Files

| 文件 | 变更 |
|---|---|
| `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts` | `PromQLTemplateId` 新增 2 个 VM ID；`PROMQL_TEMPLATES` 新增 2 个 VM 模板正文；`getTemplatesForKind` 重写为 switch + `case 'vm'` 分支；`PROMQL_TEMPLATE_LABELS` 新增 VM 中文系列名 |
| `repo/frontends/console/src/features/instance-observability/MetricsChart.tsx` | `SERIES_COLORS` 新增 2 个 VM 配色（复用蓝/绿）；移除未使用的 `Space` import（修复既有 lint error） |

## 对齐

- PRD: US-011 / FR-19
- SPEC: §5.6 PromQL 模板冻结表、§5.7 getTemplatesForKind 扩展、§5.9 SERIES_COLORS 扩展
- UX: §4.4 VM 时序图布局（2 条曲线）、§6.2 时序图状态、§7.1 标签文案
- Issue: issue-011，Batch B-7
- 依赖: #9（B-5 VM 分支后端修复）、#10（B-6 name label 重写扩展）
