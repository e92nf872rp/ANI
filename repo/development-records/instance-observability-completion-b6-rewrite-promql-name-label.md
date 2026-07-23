# INSTANCE-OBSERVABILITY-COMPLETION-B6-REWRITE-PROMQL-NAME-LABEL

> 批次 ID: B-6
> Issue: issue-010 rewritePromQLLabels 扩展支持 name label（OQ-4 决策）
> 日期: 2026-07-22
> 产品线: core
> 代码路径: `repo/pkg/adapters/runtime/prometheus_observability_service.go`

## 变更摘要

修改 `rewritePromQLLabels`，新增 `case "name":` 分支，用精确匹配 `name="record.Name"`（非正则 `name=~"..."`）。这是 OQ-4 的 SPEC 决策：**扩展后端重写链路支持 `name` label**，而非前端占位符注入。

- `labelValuePattern` 正则由 `(namespace|pod)="([^"]*)"` 扩展为 `(namespace|pod|name)="([^"]*)"`
- switch 新增 `case "name":` 分支：`name="<record.Name>"` 精确匹配（VMI `metadata.name` = `record.Name`，无随机后缀）
- 现有 `namespace`/`pod` 重写逻辑零改动
- 新增 4 个单元测试覆盖 `name` label 重写路径 + container `pod` label 回归

## Implementation Notes / Design Choices

### 1. 正则扩展方式（非新增独立 pattern）

- **歧义**：SPEC §5.8 给出了 `case "name":` 分支的代码片段，但未显式说明 `name` label 的识别方式（是扩展同一正则还是新增独立匹配）。
- **选择**：扩展现有 `labelValuePattern` 正则的 alternation，从 `(namespace|pod)` 扩展为 `(namespace|pod|name)`，复用同一 `FindAllStringSubmatchIndex` + switch 分发链路。
- **理由**：`namespace`/`pod`/`name` 三个 label 在 PromQL 中的语法形式完全相同（`label="value"`），且都需要走相同的 instance_id 解析 + 实例记录查询 + 值重写流程。扩展现有正则保持单一匹配链路，避免引入并行匹配器和合并逻辑，与 Karpathy 原则二「用能解决问题的最小代码」一致。

### 2. 精确匹配 vs 正则匹配

- **设计依据**：SPEC §5.8 明确「`name` label 用精确匹配 `name="record.Name"`，**非**正则 `name=~"..."`」，原因是 VMI `metadata.name` = `record.Name`（无随机后缀，见 PRD §7.1 已知约束）。
- **实现**：`case "name":` 写入 `name="` + `record.Name` + `"`（用 `=` 精确匹配），与 `namespace` 分支的写法一致；区别于 `pod` 分支的 `pod=~"` + `podMatcher` + `"`（用 `=~` 正则匹配）。

## Spec Deviations + Rationale

None — 实现严格遵循 SPEC §5.8 的代码片段与「关键差异」说明，无偏离。

- 正则扩展为 `(namespace|pod|name)="..."` — 与 SPEC §5.8 展示的 switch 分发链路一致
- `case "name":` 用 `name="record.Name"` 精确匹配 — 与 SPEC §5.8 「关键差异」一致
- 现有 `container`/`gpu_container` 的 `pod` label 重写不回归 — 有专门回归测试验证

## Alternatives Considered

### 方案 A（已选）：扩展 `labelValuePattern` 正则 alternation

- **优点**：单一匹配链路，零新增抽象，最小 diff，与现有 `namespace`/`pod` 重写逻辑结构对称
- **缺点**：正则 alternation `(namespace|pod|name)` 在理论上可匹配 `_name`/`_namespace`/`_pod` 后缀的 label（如 `container_name="..."`）
- **风险评估**：全仓库（`.go`/`.ts`/`.yaml`）无 `container_name=`/`pod_name=`/`_namespace=` label 模式，PromQL 冻结模板只用 `namespace`/`pod`/`name` 作为独立 label；现有 `(namespace|pod)="..."` 正则已有相同行为，扩展到 `name` 不引入新风险

### 方案 B：新增独立 `nameValuePattern` 正则 + 独立匹配链路

- **优点**：正则可用 `(^|,)name="..."` 加 word boundary 精确匹配 `name` label，避免后缀误匹配
- **缺点**：引入并行匹配器和合并逻辑，增加复杂度；偏离 SPEC §5.8 展示的单一 switch 链路；与现有 `namespace`/`pod` 的匹配行为不对称
- **未选原因**：违反 Karpathy 原则二「不为一次性代码创建抽象层」和原则五「奥卡姆剃刀」；现有代码已接受 alternation 无 word boundary 的行为，无实际缺陷需修复

### 方案 C：前端占位符注入（OQ-4 已否决）

- **优点**：后端零改动
- **缺点**：前端需感知 `name` label 与 `namespace`/`pod` 的差异，破坏「后端统一处理租户隔离和实例名注入，前端不感知重写逻辑」的架构一致性
- **未选原因**：OQ-4 SPEC 决策 D-13 已明确否决此方案

## Follow-ups / Blockers

### VM 端到端 live 验证待补

- **假设**：`name` label 重写在真实 VM 环境下应正确工作（VMI `metadata.name` = `record.Name`，`kubevirt_vmi_*` 指标带 `name` label）
- **当前状态**：当前系统无 VM，单元测试用 mock HTTP server 验证 PromQL 重写逻辑和转发链路
- **待验证**：等 VM 环境就绪后，需补齐端到端 live 验证：
  1. 创建 VM 实例，确认 VMI `metadata.name` 等于实例 `record.Name`
  2. 确认 `kubevirt_vmi_*` 指标带 `name="<vmi-name>"` label（依赖 issue-008 KubeVirt virt-handler scrape 配置已部署）
  3. 通过 Console 时序图发起 `queryObservability?query=<VM PromQL 模板>`，验证 `rewritePromQLLabels` 将 `name="inst_1"` 重写为 `name="<真实 VMI name>"` 后 Prometheus 返回非空时序数据
- **用户确认**：用户明确指示「现在由于系统还没有VM，所以就没有测试，等VM有了后再测」

## Verification Commands Run

```bash
# 单元测试（含 4 个新测试 + 回归测试）
cd repo && go test ./pkg/adapters/runtime/... -run "TestPrometheusObservabilityService" -v -count=1
# 结果: 12/12 PASS

# lint
cd repo && go vet ./pkg/adapters/runtime/...
# 结果: clean

# 架构守卫
cd repo && python scripts/validate_component_imports.py --root .
# 结果: component import guard passed

# 构建
cd repo && go build ./pkg/...
# 结果: PASS
```

## Changed Files

| 文件 | 变更 |
|---|---|
| `repo/pkg/adapters/runtime/prometheus_observability_service.go` | `labelValuePattern` 正则扩展 + `case "name":` 分支 + 相关注释更新 |
| `repo/pkg/adapters/runtime/prometheus_observability_service_test.go` | 新增 4 个单元测试（name label 单选择器/多选择器/容器回归/Query 端到端） |

## 对齐

- PRD: US-010 / FR-19
- SPEC: §5.8 rewritePromQLLabels 扩展、§11.1 OQ-4 已关闭（D-13 决策）
- UX: N/A（后端 label 重写，无前端改动）
- Issue: issue-010，Batch B-6
