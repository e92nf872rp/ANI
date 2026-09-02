/**
 * Console 租户用量报表 — 常量定义。
 *
 * RESOURCE_TYPE_TABS: 预设视角 Tab 配置，5 启用 + 2 disabled（FR-17 无 token_total Tab）。
 * GROUP_BY_OPTIONS: group_by 分组维度选项。
 */

/** 预设视角 Tab 配置项 */
export interface ResourceTypeTab {
  /** Tab 文案（UI 别名） */
  label: string
  /** 对应 API resource_type 查询值 */
  value: string
  /** P0 是否启用；false 表示 disabled + Tooltip「待 API 合入（P1）」 */
  enabled: boolean
  /** disabled 时的 Tooltip 文案 */
  disabledTooltip?: string
}

/**
 * 预设视角 Tabs（SPEC §2.4、UX §5.1）。
 *
 * P0 启用 5 类：GPU/CPU/Memory/Input/Output。
 * P1 disabled 2 类：Storage/KB（待 API 合入）。
 * **不含** token_total Tab（FR-17）。
 */
export const RESOURCE_TYPE_TABS: ResourceTypeTab[] = [
  { label: 'GPU 算力', value: 'instance_gpu_seconds', enabled: true },
  { label: 'CPU 算力', value: 'instance_cpu_seconds', enabled: true },
  { label: '内存', value: 'instance_memory_gib_seconds', enabled: true },
  { label: 'Input Tokens', value: 'token_input', enabled: true },
  { label: 'Output Tokens', value: 'token_output', enabled: true },
  {
    label: '存储',
    value: 'storage_gb_days',
    enabled: false,
    disabledTooltip: '该指标待 API 合入（P1）',
  },
  {
    label: '知识库查询',
    value: 'kb_query_count',
    enabled: false,
    disabledTooltip: '该指标待 API 合入（P1）',
  },
]

/** group_by 分组维度选项值 */
export type GroupByOption = 'resource_type' | 'day' | 'hour'

/** group_by 分组维度配置项 */
export interface GroupByOptionConfig {
  /** Segmented 显示文案 */
  label: string
  /** API group_by 值 */
  value: GroupByOption
}

/**
 * group_by 分组维度选项（SPEC §3.2、UX §5.1）。
 * 租户 API group_by 枚举：resource_type / day / hour（az 无底层列支持，契约已移除）。
 * 注意：平台 API 另有 tenant_id，租户侧不包含。
 */
export const GROUP_BY_OPTIONS: GroupByOptionConfig[] = [
  { label: '按资源类型', value: 'resource_type' },
  { label: '按天', value: 'day' },
  { label: '按小时', value: 'hour' },
]
