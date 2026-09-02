/**
 * Console 租户用量报表 — 类型定义。
 *
 * UsageFilter: 前端筛选状态。
 * UsageRow: 表格行类型，对齐 OpenAPI MeteringUsageRecord。
 */

import type { GroupByOption } from './constants'

/**
 * 用量查询筛选条件（SPEC §3.2）。
 *
 * 对齐 Core API GET /metering/usage 的 query 参数：
 * - start_time / end_time：必填，ISO 8601 date-time
 * - resource_type：可选，从 Tab 选择；undefined 表示全部
 * - group_by：可选，分组维度
 */
export interface UsageFilter {
  /** 开始时间（ISO 8601 date-time），必填 */
  start_time: string
  /** 结束时间（ISO 8601 date-time），必填 */
  end_time: string
  /** 资源类型筛选，可选；undefined 表示不筛（全部，可能含 token_total 行） */
  resource_type?: string
  /** 分组维度，可选 */
  group_by?: GroupByOption
}

/**
 * 用量表格行类型（SPEC §3.2）。
 *
 * 对齐 OpenAPI MeteringUsageRecord，字段为可选以兼容 API 宽松返回。
 */
export interface UsageRow {
  /** 资源类型枚举值 */
  resource_type?: string
  /** 用量数值，原样展示（FR-18 不做前端换算） */
  total_quantity?: number
  /** 单位，原样展示（FR-18） */
  unit?: string
  /** 统计周期，可空显示 — */
  period?: string
}
