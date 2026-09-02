/**
 * UsageChart — Console 租户用量报表 ECharts 趋势图组件（Issue #10）。
 *
 * 职责：
 * - 按 group_by 时间桶渲染 ECharts 折线/柱图
 * - x 轴: period (day/hour) 或 resource_type；y 轴: total_quantity
 * - loading 态: Skeleton；empty 态 (items=[]): 不渲染假折线，显示 Empty
 *
 * 首例引入 echarts-for-react（全代码库无使用）。图表数据由父组件传入，
 * 与 UsageTable 共享同一 queryKey（父组件 useQuery 统一管理）。
 *
 * @see UX §4.1 趋势图区、§5.1 组件映射、§6.1 状态设计
 * @see SPEC §5.1 图表数据映射、§5.4 Edge Cases
 */

import { useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import { Empty, Skeleton } from 'tdesign-react'
import type { EChartsOption } from 'echarts'
import type { GroupByOption } from './constants'
import type { UsageRow } from './types'

/** UsageChart 组件 Props */
export interface UsageChartProps {
  /** 用量数据行（来自 API items[]，与 UsageTable 同源） */
  items: UsageRow[]
  /** loading 态：显示 Skeleton */
  loading: boolean
  /** 分组维度，决定 x 轴类型与图表类型（折线/柱） */
  groupBy?: GroupByOption
}

/**
 * 判断 group_by 是否为时间维度（决定 x 轴用 period 还是 resource_type）。
 *
 * @param groupBy 分组维度
 * @returns true 表示时间桶（day/hour），x 轴用 period；否则用 resource_type
 */
function isTimeBucket(groupBy?: GroupByOption): boolean {
  return groupBy === 'day' || groupBy === 'hour'
}

/**
 * 构建图表 x 轴类别与系列数据。
 *
 * 时间桶（day/hour）：x 轴 = period，按 resource_type 拆分多条系列（折线）。
 * 非时间桶（resource_type/az）：x 轴 = resource_type，单系列（柱图）。
 *
 * @param items 用量数据
 * @param groupBy 分组维度
 * @returns ECharts option 所需的 categories 与 series
 */
function buildChartData(
  items: UsageRow[],
  groupBy?: GroupByOption,
): { categories: string[]; series: { name: string; data: number[] }[] } {
  if (isTimeBucket(groupBy)) {
    // 时间桶：x 轴 = period，按 resource_type 拆分多系列
    const periods: string[] = []
    const periodSet = new Set<string>()
    for (const item of items) {
      const p = item.period ?? '—'
      if (!periodSet.has(p)) {
        periodSet.add(p)
        periods.push(p)
      }
    }

    // 按 resource_type 分组
    const seriesMap = new Map<string, number[]>()
    for (const item of items) {
      const name = item.resource_type ?? '未知'
      if (!seriesMap.has(name)) {
        seriesMap.set(name, new Array(periods.length).fill(0))
      }
      const p = item.period ?? '—'
      const idx = periods.indexOf(p)
      if (idx >= 0) {
        seriesMap.get(name)![idx] = item.total_quantity ?? 0
      }
    }

    const series = Array.from(seriesMap.entries()).map(([name, data]) => ({
      name,
      data,
    }))

    return { categories: periods, series }
  }

  // 非时间桶：x 轴 = resource_type，单系列
  const categories: string[] = []
  const values: number[] = []
  for (const item of items) {
    categories.push(item.resource_type ?? '未知')
    values.push(item.total_quantity ?? 0)
  }

  return {
    categories,
    series: [{ name: '用量', data: values }],
  }
}

/**
 * 用量趋势图组件。
 *
 * 根据 group_by 维度渲染 ECharts 折线（时间桶）或柱图（资源类型/可用区）。
 * loading 时显示 Skeleton；items 为空时不渲染假折线，显示 Empty。
 */
export function UsageChart({ items, loading, groupBy }: UsageChartProps) {
  const option = useMemo<EChartsOption>(() => {
    const { categories, series } = buildChartData(items, groupBy)
    const isTime = isTimeBucket(groupBy)

    return {
      tooltip: {
        trigger: isTime ? 'axis' : 'item',
      },
      legend: {
        type: isTime ? 'scroll' : 'plain',
        top: 0,
      },
      grid: {
        top: 40,
        left: 48,
        right: 16,
        bottom: 32,
      },
      xAxis: {
        type: 'category',
        data: categories,
        axisLabel: {
          hideOverlap: true,
        },
      },
      yAxis: {
        type: 'value',
        name: '用量',
      },
      series: series.map((s) => ({
        name: s.name,
        type: isTime ? ('line' as const) : ('bar' as const),
        data: s.data,
        smooth: false,
      })),
    }
  }, [items, groupBy])

  // loading 态：Skeleton（UX §6.1）
  if (loading) {
    return <Skeleton animation="gradient" style={{ height: 320 }} />
  }

  // empty 态：不渲染假折线（UX §6.1、SPEC §5.4）
  if (items.length === 0) {
    return <Empty description="当前时间范围内暂无用量数据" />
  }

  return (
    <ReactECharts
      option={option}
      style={{ height: 320, width: '100%' }}
      notMerge
      lazyUpdate
    />
  )
}
