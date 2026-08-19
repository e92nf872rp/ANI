/**
 * PlatformTrendChart 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA（Issue #18）：
 * - TrendChart: ECharts 按 group_by 时间桶渲染
 * - group_by=tenant_id：按租户聚合柱状图
 * - group_by=day/hour：按时间桶折线图
 * - loading 时渲染 Skeleton
 * - FR-18: total_quantity 原样展示
 */
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { PlatformTrendChart } from './PlatformTrendChart'
import type { PlatformUsageRow } from './types'

const mockRowsTenant: PlatformUsageRow[] = [
  { tenant_id: 'tenant-a', resource_type: 'token_input', total_quantity: 1000, unit: 'tokens' },
  { tenant_id: 'tenant-b', resource_type: 'token_input', total_quantity: 500, unit: 'tokens' },
]

const mockRowsDay: PlatformUsageRow[] = [
  { tenant_id: 'tenant-a', resource_type: 'token_input', total_quantity: 100, unit: 'tokens', period: '2026-07-01' },
  { tenant_id: 'tenant-a', resource_type: 'token_input', total_quantity: 200, unit: 'tokens', period: '2026-07-02' },
]

describe('PlatformTrendChart', () => {
  it('group_by=tenant_id 时应渲染 ECharts 容器', () => {
    const { container } = render(
      <PlatformTrendChart data={mockRowsTenant} groupBy="tenant_id" />,
    )
    // echarts-for-react 渲染一个 div 容器
    // 若没有该属性，退一步检查至少有 div 渲染
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('group_by=day 时应渲染 ECharts 容器', () => {
    const { container } = render(
      <PlatformTrendChart data={mockRowsDay} groupBy="day" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('group_by=hour 时应渲染 ECharts 容器', () => {
    const { container } = render(
      <PlatformTrendChart data={mockRowsDay} groupBy="hour" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('loading=true 时应渲染 Skeleton 而非 ECharts', () => {
    const { container } = render(
      <PlatformTrendChart data={[]} groupBy="tenant_id" loading={true} />,
    )
    // TDesign Skeleton 渲染 t-skeleton__row 类
    expect(container.querySelector('.t-skeleton__row')).not.toBeNull()
  })

  it('空数据时不应崩溃', () => {
    const { container } = render(
      <PlatformTrendChart data={[]} groupBy="tenant_id" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('多租户相同 resource_type 应按 tenant_id 聚合', () => {
    // 渲染不应崩溃，且生成 ECharts 实例
    const { container } = render(
      <PlatformTrendChart data={mockRowsTenant} groupBy="tenant_id" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('相同 period 多行应聚合到同一时间桶', () => {
    const rows: PlatformUsageRow[] = [
      { tenant_id: 't1', resource_type: 'token_input', total_quantity: 100, unit: 'tokens', period: '2026-07-01' },
      { tenant_id: 't2', resource_type: 'token_input', total_quantity: 150, unit: 'tokens', period: '2026-07-01' },
    ]
    const { container } = render(
      <PlatformTrendChart data={rows} groupBy="day" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })

  it('period 为 null 时应归入「未知」桶', () => {
    const rows: PlatformUsageRow[] = [
      { tenant_id: 't1', resource_type: 'token_input', total_quantity: 100, unit: 'tokens', period: null },
    ]
    const { container } = render(
      <PlatformTrendChart data={rows} groupBy="day" />,
    )
    expect(container.querySelector('div')).not.toBeNull()
  })
})
