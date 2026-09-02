/**
 * PlatformTrendChart — BOSS 平台计量趋势图（ECharts）。
 *
 * 按 group_by 维度渲染趋势：
 * - group_by=tenant_id：X 轴为租户 ID，单系列柱状图（跨租户排行趋势视角）
 * - group_by=day / hour：X 轴为时间桶，按 resource_type 聚合为单条折线
 *
 * 数据来源平台 API items[]，total_quantity 原样展示（FR-18 不做单位换算）。
 * loading 时渲染 Skeleton 占位；empty 时不画假数据折线（UX §6.2）。
 */
import { useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import type { EChartsOption } from 'echarts'
import { Skeleton } from 'tdesign-react'
import type { PlatformUsageRow } from './types'
import type { PlatformGroupByOption } from './constants'

export interface PlatformTrendChartProps {
  /** 图表数据，来自平台 API 响应 items[] */
  data: PlatformUsageRow[]
  /** 当前 group_by 维度；决定 X 轴与渲染模式 */
  groupBy: PlatformGroupByOption
  /** 加载态；fetching 时渲染 Skeleton */
  loading?: boolean
  /** 图表高度（px），默认 320 */
  height?: number
}

/**
 * 将 platform API items[] 转换为 ECharts option（按 group_by 维度）。
 *
 * - tenant_id：X 轴 = 租户列表，Y 轴 = total_quantity 柱状
 * - day / hour：X 轴 = period 时间桶，Y 轴 = total_quantity 折线
 */
function buildEChartsOption(
  data: PlatformUsageRow[],
  groupBy: PlatformGroupByOption,
): EChartsOption {
  if (groupBy === 'tenant_id') {
    // 按租户聚合：相同 tenant_id 的 total_quantity 求和（一个租户可能有多个 resource_type 行）
    const tenantMap = new Map<string, number>()
    for (const row of data) {
      tenantMap.set(
        row.tenant_id,
        (tenantMap.get(row.tenant_id) ?? 0) + row.total_quantity,
      )
    }
    // 按用量降序排列（排行语义）
    const sorted = Array.from(tenantMap.entries()).sort((a, b) => b[1] - a[1])
    const categories = sorted.map(([tenantId]) => tenantId)
    const values = sorted.map(([, qty]) => qty)

    return {
      tooltip: { trigger: 'axis' },
      grid: { left: 80, right: 32, top: 40, bottom: 64, containLabel: true },
      xAxis: {
        type: 'category',
        data: categories,
        axisLabel: { rotate: categories.length > 10 ? 30 : 0 },
        name: '租户 ID',
        nameLocation: 'middle',
        nameGap: 32,
      },
      yAxis: { type: 'value', name: '用量', nameLocation: 'end', nameGap: 12 },
      series: [{ type: 'bar', data: values, name: '用量' }],
    }
  }

  // group_by = day / hour：按 period 时间桶聚合
  const periodMap = new Map<string, number>()
  for (const row of data) {
    const periodKey = row.period ?? '未知'
    periodMap.set(periodKey, (periodMap.get(periodKey) ?? 0) + row.total_quantity)
  }
  // 按时间升序排列
  const sorted = Array.from(periodMap.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  const categories = sorted.map(([period]) => period)
  const values = sorted.map(([, qty]) => qty)

  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 80, right: 32, top: 40, bottom: 64, containLabel: true },
    xAxis: {
      type: 'category',
      data: categories,
      axisLabel: { rotate: categories.length > 10 ? 30 : 0 },
      name: groupBy === 'day' ? '日期' : '小时',
      nameLocation: 'middle',
      nameGap: 32,
    },
    yAxis: { type: 'value', name: '用量', nameLocation: 'end', nameGap: 12 },
    series: [{ type: 'line', data: values, name: '用量', smooth: true }],
  }
}

/**
 * 平台计量趋势图。
 *
 * loading=true 时渲染 Skeleton；data 为空时不渲染图表（父组件负责 Empty 态）。
 */
export function PlatformTrendChart({
  data,
  groupBy,
  loading = false,
  height = 320,
}: PlatformTrendChartProps) {
  const option = useMemo(() => buildEChartsOption(data, groupBy), [data, groupBy])

  if (loading) {
    return <Skeleton style={{ height, width: '100%' }} animation="gradient" />
  }

  return <ReactECharts option={option} style={{ height, width: '100%' }} />
}
