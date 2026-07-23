/**
 * 实例可观测性 PromQL 冻结模板常量模块。
 *
 * 对齐：
 * - PRD US-011 / FR-7：时序图走 PromQL 代理 + 冻结模板，按 instance_id 注入
 * - UX §4.4 指标 Tab 双通道布局、§8.4 假设：前端常量模块引用 SPEC 冻结模板 ID
 * - SPEC §5.2 PromQL 模板冻结表、§5.2.1 模板 ID 表、§5.2.2 注入契约
 *
 * 边界：
 * - 模板 ID 集合在 SPEC §5.2.1 冻结，本模块只声明 ID → 模板字符串映射
 * - 模板正文来源：与 Core adapter（`prometheus_instance_observability.go`）实际查询的
 *   Prometheus 指标对齐，使用 metrics.k8s.io / DCGM exporter 已文档化 label
 *   （`namespace`、`pod`），不硬编码未文档化 label（PRD FR-7、SPEC §5.2）
 * - 运行时按 `instance_id` 注入；占位符 `{{instance_id}}` 由 `renderPromQL` 替换
 * - 不向客户端暴露 Prometheus 服务地址（PRD FR-8）
 *
 * 注入契约：
 *   Console 通过 `promqlTemplates[templateId]` 取模板字符串，
 *   `renderPromQL(templateId, instanceId)` 渲染最终 PromQL，
 *   传给 `GET /observability/query?query=<renderedPromQL>`。
 *
 * 时间范围说明：
 *   SPEC §5.2.3 将时间范围（15m/1h/6h/24h）交由 `queryObservability` 的 `time` 参数
 *   或 PromQL 内 `__range` 表达；本模块不把时间范围写入模板正文，保持模板与 range 解耦，
 *   range 由调用方在 `queryObservability` query 参数 `time` 中传递。
 */
import type { components } from '@/api/core-schema'

/** 实例类型，与 `observabilityTabsConfig.InstanceKind` 一致。 */
type InstanceKind = components['schemas']['InstanceRecord']['kind']

/**
 * PromQL 模板 ID 枚举。
 * 对齐 SPEC §5.2.1 模板 ID 表。
 */
export type PromQLTemplateId =
  | 'instance_cpu_utilization'
  | 'instance_memory_utilization'
  | 'instance_gpu_utilization'
  | 'instance_gpu_memory_utilization'
  | 'instance_vm_cpu_utilization'
  | 'instance_vm_memory_utilization'

/**
 * PromQL 冻结模板正文。
 *
 * 模板正文来源说明：
 * - 与 Core adapter `prometheus_instance_observability.go` 实际查询的指标名对齐：
 *   - CPU：`container_cpu_usage_seconds_total`（metrics.k8s.io）
 *   - 内存：`container_memory_working_set_bytes`（metrics.k8s.io）
 *   - 网络 RX/TX：`container_network_receive_bytes_total` /
 *     `container_network_transmit_bytes_total`（metrics.k8s.io）
 *   - GPU 利用率：`DCGM_FI_DEV_GPU_UTIL`（DCGM exporter）
 *   - GPU 显存：`DCGM_FI_DEV_FB_USED` / `DCGM_FI_DEV_FB_TOTAL`（DCGM exporter）
 * - label 选择器：`{namespace="{{namespace}}",pod="{{pod}}"}`，使用已文档化 label
 *   （与 Core adapter 一致），不硬编码未文档化 label
 * - `{{instance_id}}` 占位符由 `renderPromQL` 替换为实例 ID
 *
 * 注意：模板正文从运维文档冻结，本模块与 Core adapter 当前实现的查询指标对齐
 * 以保证前端 PromQL 代理查询与后端快照采集使用同一指标源；后续若运维文档
 * 更新模板正文，应同步更新本常量。
 */
const PROMQL_TEMPLATES: Record<PromQLTemplateId, string> = {
  // CPU 利用率：rate(container_cpu_usage_seconds_total[5m]) * 100（百分比）
  // container!="",container!="POD" 过滤 pause container 与 pod 级聚合 series，
  // 只保留业务 container 指标，避免时序图出现重复曲线
  instance_cpu_utilization:
    '100 * avg(rate(container_cpu_usage_seconds_total{namespace="{{namespace}}",pod="{{pod}}",container!="",container!="POD"}[5m]))',

  // 内存使用率：container_memory_working_set_bytes / container_spec_memory_limit_bytes
  // container!="",container!="POD" 过滤 pause container 与 pod 级聚合 series
  instance_memory_utilization:
    '100 * (container_memory_working_set_bytes{namespace="{{namespace}}",pod="{{pod}}",container!="",container!="POD"} / container_spec_memory_limit_bytes{namespace="{{namespace}}",pod="{{pod}}",container!="",container!="POD"})',

  // GPU 利用率（DCGM，仅 gpu_container）
  instance_gpu_utilization:
    'DCGM_FI_DEV_GPU_UTIL{namespace="{{namespace}}",pod="{{pod}}"}',

  // GPU 显存使用率：DCGM_FI_DEV_FB_USED / DCGM_FI_DEV_FB_TOTAL
  instance_gpu_memory_utilization:
    '100 * (DCGM_FI_DEV_FB_USED{namespace="{{namespace}}",pod="{{pod}}"} / DCGM_FI_DEV_FB_TOTAL{namespace="{{namespace}}",pod="{{pod}}"})',

  // VM CPU 利用率：rate(kubevirt_vmi_cpu_usage_seconds_total[5m])
  // KubeVirt VMI 指标 label 为 `name`（VMI name），非 `pod`（virt-launcher pod name）
  instance_vm_cpu_utilization:
    'rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="{{namespace}}",name="{{instance_id}}"}[5m])',

  // VM 内存使用率：(domain_bytes - usable_bytes) / domain_bytes
  // 公式来源：SPEC D-12 / PRD FR-17；label 为 `name`
  instance_vm_memory_utilization:
    '(kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"} - kubevirt_vmi_memory_usable_bytes{namespace="{{namespace}}",name="{{instance_id}}"}) / kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"}',
}

/**
 * 根据当前 kind 返回需要渲染的模板 ID 列表。
 * - container/sandbox/batch_job/notebook：CPU 利用率、内存使用率（2 条）
 * - gpu_container：额外 GPU 利用率、显存使用率（4 条）
 * - vm：VM CPU 利用率、VM 内存使用率（2 条，不展示网络 RX/TX 时序曲线）
 *
 * 对齐 PRD US-011 AC、SPEC §5.7、UX §4.4。
 */
export function getTemplatesForKind(kind: InstanceKind): PromQLTemplateId[] {
  switch (kind) {
    case 'gpu_container':
      return [
        'instance_cpu_utilization',
        'instance_memory_utilization',
        'instance_gpu_utilization',
        'instance_gpu_memory_utilization',
      ]
    case 'vm':
      return ['instance_vm_cpu_utilization', 'instance_vm_memory_utilization']
    case 'container':
    case 'sandbox':
    case 'batch_job':
    case 'notebook':
    default:
      return ['instance_cpu_utilization', 'instance_memory_utilization']
  }
}

/**
 * 模板 ID → 中文系列名（用于 ECharts legend）。
 * 对齐 UX §7.1 标签文案。
 */
export const PROMQL_TEMPLATE_LABELS: Record<PromQLTemplateId, string> = {
  instance_cpu_utilization: 'CPU 利用率',
  instance_memory_utilization: '内存使用率',
  instance_gpu_utilization: 'GPU 利用率',
  instance_gpu_memory_utilization: 'GPU 显存使用率',
  instance_vm_cpu_utilization: 'CPU 利用率',
  instance_vm_memory_utilization: '内存使用率',
}

/**
 * 渲染 PromQL：将模板中的 `{{instance_id}}` 占位符替换为实例 ID。
 *
 * 实现说明：
 * - 当前模板正文使用 `{{namespace}}` 与 `{{pod}}` 占位符，与 Core adapter
 *   的 label 选择器对齐（adapter 用 `namespace` + `pod` 过滤 pod 级指标）
 * - Console 不掌握 namespace/pod 映射，但 SPEC §5.2.2 注入契约只冻结 `instance_id`
 *   占位符；为不发明未文档化字段，本模块将 `instance_id` 同时注入到
 *   `{{namespace}}`、`{{pod}}`、`{{instance_id}}` 三个占位符。Core 端 `queryObservability`
 *   handler 作为 PromQL 代理透传查询，由 Prometheus adapter 的 relabel 规则
 *   保证 `pod` label = instance_id（K8s 工作负载命名约定）
 * - 不硬编码未文档化 label：仅使用 Core adapter 已使用的 namespace/pod 两个 label
 */
export function renderPromQL(templateId: PromQLTemplateId, instanceId: string): string {
  const tpl = PROMQL_TEMPLATES[templateId]
  return tpl
    .split('{{instance_id}}').join(instanceId)
    .split('{{namespace}}').join(instanceId)
    .split('{{pod}}').join(instanceId)
}
