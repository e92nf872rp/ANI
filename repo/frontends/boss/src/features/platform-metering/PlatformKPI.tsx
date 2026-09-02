/**
 * PlatformKPI — BOSS 平台计量 KPI 汇总卡组件。
 *
 * 展示全平台 total_quantity 汇总值（FR-17）：
 * - 聚合页 KPI 可使用 resource_type=token_total 查询（FR-17）
 * - 专页 KPI 展示该固定 resource_type 的全平台汇总
 * - total_quantity + unit 原样展示（FR-18 不做单位换算）
 *
 * 父组件传入已聚合的数据 items[]，组件内部求和；或直接传入 total 值。
 * loading 时渲染 Skeleton 占位。
 */
import { Card, Skeleton, Statistic } from 'tdesign-react'
import type { PlatformUsageRow } from './types'

export interface PlatformKPIProps {
  /** KPI 标题（如「全平台 Token 合计」「平台 GPU 算力总量」） */
  title: string
  /**
   * KPI 数值来源。两种模式：
   * 1. 传入 items[]，组件内部对 total_quantity 求和（聚合多租户/多周期）
   * 2. 传入 total 数值（若父组件已计算）
   * 二者同时提供时优先使用 total。
   */
  items?: PlatformUsageRow[]
  /** 已计算的汇总值；优先于 items 求和 */
  total?: number
  /** 单位，原样展示（FR-18）；若未提供则取 items[0].unit */
  unit?: string
  /** 加载态；fetching 时渲染 Skeleton */
  loading?: boolean
}

/**
 * 对 items[] 的 total_quantity 求和。
 *
 * 用于全平台汇总：跨租户、跨周期的 total_quantity 累加。
 */
function sumTotalQuantity(items: PlatformUsageRow[]): number {
  return items.reduce((sum, row) => sum + (row.total_quantity ?? 0), 0)
}

/**
 * 平台 KPI 汇总卡。
 *
 * loading=true 时渲染 Skeleton；否则展示 Card + Statistic。
 * total_quantity + unit 原样展示（FR-18）。
 */
export function PlatformKPI({
  title,
  items,
  total,
  unit,
  loading = false,
}: PlatformKPIProps) {
  const value = total ?? (items ? sumTotalQuantity(items) : 0)
  const displayUnit = unit ?? items?.[0]?.unit ?? ''

  if (loading) {
    return <Skeleton style={{ height: 100, width: '100%' }} animation="gradient" />
  }

  return (
    <Card title={title} bordered>
      <Statistic
        value={value}
        unit={displayUnit}
        decimalPlaces={0}
      />
    </Card>
  )
}
