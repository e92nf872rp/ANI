# B-8 — VM 指标 Tab 快照卡片验证（Issue #012）

- Issue: #012-vm-snapshot-verify
- Batch: B-8
- Product line: console
- Type: 纯验证 issue（无新增代码改动）
- Date: 2026-07-22

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md
- Issue: repo/services/tasks/modules/issue/console/compute/instance-observability-completion/issue-012-vm-snapshot-verify.md

## 依赖确认
- #9 (B-5 GetMetrics VM 分支)：✅ 已实现，`getMetricsForVM` 查询 `kubevirt_vmi_*` 指标，位于 GPU 分支前
- #1 (B-1 handler 传 Kind)：✅ 已实现
- #11 (B-7 VM PromQL 模板)：✅ 已实现

## Implementation notes / design choices

### Design Decisions

1. **纯验证 issue，无前端代码改动**
   - 模糊点：Issue Scope 明确 `MetricsSnapshot.tsx`「仅验证，无改动」，但 AC 列表包含多项 UI 验证。
   - 选择：不修改 `MetricsSnapshot.tsx`，仅通过代码审查 + 静态验证确认通用渲染逻辑覆盖 VM kind。
   - 理由：`MetricsSnapshot.tsx` 的 CPU/内存/网络 RX/TX 4 卡片对所有 kind 通用渲染，GPU 卡片仅在 `kind === 'gpu_container'` 时渲染，VM kind 天然满足 UX §4.2 布局要求，无需改动。

2. **验证方式：静态代码审查 + 门禁通过，runtime 验证延后**
   - 模糊点：AC 要求"在浏览器中验证 loading/partial-null/error 三态"，但当前环境无 browser automation 工具，且真实环境无 VM 实例。
   - 选择：通过代码路径审查确认三态渲染逻辑实现正确，记录手动验证步骤供 VM 就绪后执行。
   - 理由：三态逻辑（Skeleton loading / Tag 暂不可用 partial-null / Alert error）已在代码中实现且有明确对应，静态审查可证明逻辑正确性；runtime 行为需 VM + KubeVirt virt-handler 真实环境验证。

## Spec deviations + rationale

### Deviations

1. **AC 第 9 项（浏览器三态验证）未执行 runtime 验证**
   - Spec 要求：在浏览器中通过 browser automation 工具验证 VM 实例指标 Tab 的 loading / partial-null / error 三态。
   - 实际做法：记录手动验证步骤，未执行 runtime 验证。
   - 偏离原因：当前真实环境**尚无 VM 实例**（KubeVirt virt-handler 未部署或无 VMI 资源），无法触发真实 `kubevirt_vmi_*` 指标查询路径。browser automation 工具在当前 IDE 环境不可用。静态代码审查已证明三态逻辑实现完整。
   - 用户明确指示：**现在由于系统还没有 VM，所以就没有测试，等 VM 有了后再测。**

None（其他 AC 项均按 spec 验证通过，无偏离）。

## Alternatives considered

### Tradeoffs

1. **验证深度：静态审查 vs runtime 验证**
   - 备选 A：阻塞 issue 直到 VM 环境就绪再完成 runtime 验证。
     - 优点：AC 第 9 项 100% 满足。
     - 缺点：阻塞 B-8 收口，依赖 KubeVirt 环境部署进度，不在本批次控制范围内。
   - 备选 B（选择）：静态审查 + 门禁通过 + 记录手动验证步骤，标记 runtime 验证为 Open Question。
     - 优点：不阻塞批次收口；代码逻辑正确性已通过审查证明；VM 就绪后可快速执行手动验证。
     - 缺点：AC 第 9 项为"记录手动验证步骤"而非"已执行 runtime 验证"，验证强度较弱。
   - 胜出理由：用户明确指示延后 runtime 测试；静态审查覆盖了三态逻辑的完整实现证据。

## Follow-ups / blockers

### Open Questions

1. **VM runtime 验证待执行（用户明确延后）**
   - 不确定项：真实 KubeVirt 环境下 `kubevirt_vmi_*` 指标查询是否返回非 null 值，`getMetricsForVM` 的 `name="<vmi-name>"` 精确匹配是否命中 VMI。
   - 用户需验证：VM 实例就绪后，在 real-k8s-lab 环境执行以下手动验证步骤：
     1. 进入 `/instances/$vmInstanceId?tab=metrics`，确认 loading 态显示 Skeleton
     2. KubeVirt virt-handler 不可用时，4 卡片显示「暂不可用」Tag（非 0）
     3. virt-handler 可用时，4 卡片（CPU/内存/网络 RX/TX）显示真实 `kubevirt_vmi_*` 数值，无 GPU 卡片
     4. 后端返回 500/403 时显示 Alert + 请求 ID + 重试按钮
     5. 切换到非 VM kind 实例，确认走各自分支无回归
   - 后续动作：VM 环境就绪后补测，更新本笔记 AC 第 9 项状态为已验证。

2. **内存已用公式 `domain - usable` 的真实数据验证**
   - 不确定项：`kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes` 公式在真实 KubeVirt 版本下是否返回合理正值（PRD FR-17）。
   - 需 VM 环境验证：确认 `usable_bytes < domain_bytes`，避免出现负值。

## Verification commands run

| 命令 | 结果 | 说明 |
|------|------|------|
| `pnpm type-check` (console) | ✅ pass | TypeScript 类型检查 |
| `pnpm lint` (console) | ✅ pass | ESLint 0 errors |
| `pnpm build` (console) | ✅ pass | 构建成功 |
| `make test` (repo) | ✅ pass | 含 VM 分支单测 |
| `make validate-architecture` (repo) | ✅ pass | 架构守卫通过 |
| `git diff --check` | ✅ pass | 无空白错误 |
| `go test ./pkg/adapters/runtime/... ./services/ani-gateway/...` | ✅ pass | adapter + gateway 单测 |

## AC 状态汇总

| AC | 状态 | 验证方式 |
|----|------|----------|
| #9 完成后 VM 指标来源 `kubevirt_vmi_*` | ✅ | 代码审查 `getMetricsForVM` |
| VM 快照卡片通用渲染 CPU/内存/网络 | ✅ | 代码审查 `MetricsSnapshot.tsx` |
| KubeVirt 不可用字段 null 显示「暂不可用」 | ✅ | 代码审查 partial-null Tag |
| 非 VM kind 不走 VM 分支 | ✅ | 代码审查分支隔离 |
| Typecheck/lint passes | ✅ | 门禁执行 |
| UX §4.2 VM 快照 4 卡片无 GPU | ✅ | 代码审查 `isGpu` 条件 |
| UX §6.1 状态（loading/partial-null/error/forbidden/stale） | ✅ | 代码审查状态分支 |
| UX §6.5 Edge States | ✅ | 代码审查 partial-null Tag |
| 浏览器验证三态 | ⚠️ 延后 | 用户指示 VM 就绪后补测 |
