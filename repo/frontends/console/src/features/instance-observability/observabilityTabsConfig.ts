/**
 * 实例可观测性 Tab 可见性配置（kind → 可见 Tab 映射）。
 *
 * 来源：PRD §6 Kind × Tab 矩阵、UX §4.1 Kind × Tab 可见性、SPEC §3.3、SPEC §5.1.1。
 *
 * 首期 hardcode SPEC §4.1 矩阵；`metricsSupported=false` 的 kind 不渲染指标 Tab（非空态隐藏）。
 */

/** 计算实例类型，共 9 种（来自 OpenAPI `InstanceRecord.kind`）。 */
export type InstanceKind =
  | 'vm'
  | 'container'
  | 'gpu_container'
  | 'sandbox'
  | 'batch_job'
  | 'notebook'
  | 'k8s_cluster'
  | 'bare_metal'
  | 'dpu_node'

/** 可观测性 Tab 标识。 */
export type ObservabilityTabId =
  | 'logs'
  | 'events'
  | 'metrics'
  | 'terminal'
  | 'console'
  | 'security-events'

/** 单个 kind 的 Tab 配置。 */
export interface ObservabilityTabConfig {
  /** 该 kind 可见的 Tab 列表（顺序即渲染顺序）。 */
  tabs: ObservabilityTabId[]
  /** 是否支持指标 Tab；不支持则隐藏指标 Tab。 */
  metricsSupported: boolean
}

/** kind → 可见 Tab 映射常量。对齐 PRD §6 / UX §4.1 / SPEC §3.3。 */
export const INSTANCE_OBSERVABILITY_TAB_CONFIG: Record<
  InstanceKind,
  ObservabilityTabConfig
> = {
  container: { tabs: ['logs', 'events', 'metrics', 'terminal'], metricsSupported: true },
  gpu_container: { tabs: ['logs', 'events', 'metrics', 'terminal'], metricsSupported: true },
  sandbox: { tabs: ['logs', 'events', 'metrics', 'terminal', 'security-events'], metricsSupported: true },
  vm: { tabs: ['logs', 'events', 'metrics', 'console'], metricsSupported: true },
  batch_job: { tabs: ['logs', 'events', 'metrics'], metricsSupported: true },
  notebook: { tabs: ['logs', 'events', 'metrics'], metricsSupported: true },
  k8s_cluster: { tabs: ['logs', 'events'], metricsSupported: false },
  bare_metal: { tabs: ['logs', 'events'], metricsSupported: false },
  dpu_node: { tabs: ['logs', 'events'], metricsSupported: false },
}

/** 深链 `?tab=` 允许的取值集合。 */
export const OBSERVABILITY_TAB_QUERY_VALUES: readonly ObservabilityTabId[] = [
  'logs',
  'events',
  'metrics',
  'terminal',
  'console',
  'security-events',
]

/** 默认回退 Tab（非法或不可见 `?tab=` 回退到此）。 */
export const DEFAULT_OBSERVABILITY_TAB: ObservabilityTabId = 'logs'

/**
 * 获取指定 kind 的可见 Tab 列表。
 * 对齐 SPEC §5.1.1 `getVisibleTabs`。
 */
export function getVisibleTabs(kind: InstanceKind): ObservabilityTabId[] {
  return INSTANCE_OBSERVABILITY_TAB_CONFIG[kind].tabs
}

/**
 * 判断指定 kind 是否支持指标 Tab。
 * 对齐 SPEC §5.1.1 `isMetricsSupported`。
 */
export function isMetricsSupported(kind: InstanceKind): boolean {
  return INSTANCE_OBSERVABILITY_TAB_CONFIG[kind].metricsSupported
}

/**
 * 校验 `?tab=` 取值是否合法且对当前 kind 可见。
 * 非法或不可见时回退到「日志」。
 * 对齐 UX §2.1 深链规则与 SPEC §5.7 边缘场景。
 */
export function resolveTabFromQuery(
  query: string | null | undefined,
  kind: InstanceKind,
): ObservabilityTabId {
  if (!query) return DEFAULT_OBSERVABILITY_TAB
  if (!OBSERVABILITY_TAB_QUERY_VALUES.includes(query as ObservabilityTabId)) {
    return DEFAULT_OBSERVABILITY_TAB
  }
  const tab = query as ObservabilityTabId
  const visible = getVisibleTabs(kind)
  return visible.includes(tab) ? tab : DEFAULT_OBSERVABILITY_TAB
}
