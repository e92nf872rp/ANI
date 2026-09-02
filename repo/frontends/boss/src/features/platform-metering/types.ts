/**
 * BOSS 平台计量 — 类型定义。
 *
 * PlatformUsageFilter: 平台查询筛选状态。
 * PlatformUsageRow: 平台用量表格行类型，对齐 OpenAPI MeteringUsageRecord。
 */

import type { PlatformGroupByOption } from './constants'

/**
 * 平台用量查询筛选条件（SPEC §3.2）。
 *
 * 对齐 Core API GET /metering/usage/platform 的 query 参数：
 * - start_time / end_time：必填，ISO 8601 date-time
 * - resource_type：可选，聚合页可切换；专页固定
 * - group_by：可选，平台分组维度（含 tenant_id）
 * - tenant_id：可选，钻取时指定单租户
 */
export interface PlatformUsageFilter {
  /** 开始时间（ISO 8601 date-time），必填 */
  start_time: string
  /** 结束时间（ISO 8601 date-time），必填 */
  end_time: string
  /** 资源类型筛选，可选；聚合页可切换，专页固定 */
  resource_type?: string
  /** 分组维度，可选；平台 API 含 tenant_id / day / hour */
  group_by?: PlatformGroupByOption
  /** 钻取时指定的租户 ID，可选；后端 RBAC 二次校验 */
  tenant_id?: string
}

/**
 * 平台用量表格行类型（SPEC §3.2）。
 *
 * 对齐 OpenAPI MeteringUsageRecord，平台视角下 tenant_id 必填。
 */
export interface PlatformUsageRow {
  /** 租户 ID，平台视角下必填 */
  tenant_id: string
  /** 资源类型枚举值 */
  resource_type: string
  /** 用量数值，原样展示（FR-18 不做前端换算） */
  total_quantity: number
  /** 单位，原样展示（FR-18） */
  unit: string
  /** 统计周期，可空显示 — */
  period?: string | null
}
