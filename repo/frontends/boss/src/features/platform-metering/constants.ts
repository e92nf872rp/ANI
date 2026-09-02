/**
 * BOSS 平台计量 — 常量定义。
 *
 * METRIC_PAGES: 平台计量专页配置，5 P0（GPU/CPU/Memory/Input/Output）+ 2 P1（Storage/KB, p0_enabled=false）。
 * PLATFORM_GROUP_BY_OPTIONS: 平台 API group_by 分组维度选项（含 tenant_id）。
 */

/** 平台计量专页配置项 */
export interface MetricPageConfig {
  /** 路由路径 */
  route: string
  /** 页面标题 */
  title: string
  /** 固定 resource_type 查询值 */
  resource_type: string
  /** P0 是否启用；false 表示 P1 占位页（api-not-ready 态） */
  p0_enabled: boolean
}

/**
 * 平台计量专页配置（SPEC §3.2、UX §1.1）。
 *
 * P0 启用 5 类：GPU/CPU/Memory/Input/Output。
 * P1 占位 2 类：Storage/KB（p0_enabled=false，路由可进但显示 api-not-ready）。
 */
export const METRIC_PAGES: MetricPageConfig[] = [
  { route: '/metering/gpu-hours', title: '平台 GPU-Hours', resource_type: 'instance_gpu_seconds', p0_enabled: true },
  { route: '/metering/cpu-hours', title: '平台 CPU-Hours', resource_type: 'instance_cpu_seconds', p0_enabled: true },
  { route: '/metering/memory-gbhours', title: '平台 Memory-GBHours', resource_type: 'instance_memory_gib_seconds', p0_enabled: true },
  { route: '/metering/input-tokens', title: '平台 Input Tokens', resource_type: 'token_input', p0_enabled: true },
  { route: '/metering/output-tokens', title: '平台 Output Tokens', resource_type: 'token_output', p0_enabled: true },
  { route: '/metering/storage-gbdays', title: '平台 Storage-GBDays', resource_type: 'storage_gb_days', p0_enabled: false },
  { route: '/metering/kb-queries', title: '平台 KB Queries', resource_type: 'kb_query_count', p0_enabled: false },
]

/** 平台 API group_by 分组维度选项值 */
export type PlatformGroupByOption = 'tenant_id' | 'day' | 'hour'

/** group_by 分组维度配置项 */
export interface PlatformGroupByOptionConfig {
  /** Select 显示文案 */
  label: string
  /** API group_by 值 */
  value: PlatformGroupByOption
}

/**
 * 平台 group_by 分组维度选项（SPEC §3.2、UX §5.2）。
 *
 * 平台 API group_by 枚举：tenant_id / day / hour。
 * 注意：与租户 API 不同，平台 API 不含 resource_type / az，但含 tenant_id。
 */
export const PLATFORM_GROUP_BY_OPTIONS: PlatformGroupByOptionConfig[] = [
  { label: '按租户', value: 'tenant_id' },
  { label: '按天', value: 'day' },
  { label: '按小时', value: 'hour' },
]

/** 指标视角配置项 */
export interface MetricViewOptionConfig {
  /** Select 显示文案 */
  label: string
  /** API resource_type 值 */
  value: string
}

/**
 * 聚合页指标视角选项（UX §5.2、PRD §6.3）。
 *
 * 聚合页可通过 Select 切换 resource_type；专页固定 resource_type，不提供切换。
 * P0 启用 5 类；token_total 不单独设视角（FR-17）。
 */
export const METRIC_VIEW_OPTIONS: MetricViewOptionConfig[] = [
  { label: 'GPU 算力', value: 'instance_gpu_seconds' },
  { label: 'CPU 算力', value: 'instance_cpu_seconds' },
  { label: '内存', value: 'instance_memory_gib_seconds' },
  { label: 'Input Tokens', value: 'token_input' },
  { label: 'Output Tokens', value: 'token_output' },
]

/**
 * 平台计量租户 Select 选项常量。
 *
 * 租户下拉列表不受当前筛选条件影响，始终显示所有已知租户，
 * 避免选择租户后 API 只返回该租户数据、导致下拉选项被缩减为单条。
 */
export const PLATFORM_TENANT_OPTIONS: { label: string; value: string }[] = [
  { label: 'tenant-alpha', value: 'tenant-alpha' },
  { label: 'tenant-beta', value: 'tenant-beta' },
  { label: 'tenant-gamma', value: 'tenant-gamma' },
  { label: 'tenant-delta', value: 'tenant-delta' },
]
