/**
 * PlatformKPI 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA（Issue #18）：
 * - KPI: 全平台 total_quantity 汇总
 * - FR-17: 聚合页可用 token_total 查询
 * - FR-18: 单位原样展示（不做换算）
 * - loading 时渲染 Skeleton
 */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PlatformKPI } from './PlatformKPI'
import type { PlatformUsageRow } from './types'

const mockRows: PlatformUsageRow[] = [
  { tenant_id: 'tenant-a', resource_type: 'token_total', total_quantity: 1000, unit: 'tokens' },
  { tenant_id: 'tenant-b', resource_type: 'token_total', total_quantity: 500, unit: 'tokens' },
  { tenant_id: 'tenant-c', resource_type: 'token_total', total_quantity: 200, unit: 'tokens' },
]

describe('PlatformKPI', () => {
  it('应对 items[] 的 total_quantity 求和', () => {
    render(<PlatformKPI title="全平台 Token 合计" items={mockRows} />)
    // 1000 + 500 + 200 = 1700；TDesign Statistic 渲染带千分位分隔符 "1,700"
    expect(screen.getByText('1,700')).toBeInTheDocument()
  })

  it('应原样展示单位（FR-18）', () => {
    render(<PlatformKPI title="全平台 Token 合计" items={mockRows} />)
    expect(screen.getByText('tokens')).toBeInTheDocument()
  })

  it('传入 total 时应优先使用 total 而非 items 求和', () => {
    render(<PlatformKPI title="测试" items={mockRows} total={9999} />)
    // TDesign Statistic 渲染带千分位分隔符
    expect(screen.getByText('9,999')).toBeInTheDocument()
  })

  it('传入 unit 时应优先使用 unit', () => {
    render(<PlatformKPI title="测试" items={mockRows} unit="custom_unit" />)
    expect(screen.getByText('custom_unit')).toBeInTheDocument()
  })

  it('空 items 时应显示 0', () => {
    render(<PlatformKPI title="测试" items={[]} />)
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('未传 items 时应显示 0', () => {
    render(<PlatformKPI title="测试" />)
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('应展示标题', () => {
    render(<PlatformKPI title="全平台 Token 合计" items={mockRows} />)
    expect(screen.getByText('全平台 Token 合计')).toBeInTheDocument()
  })

  it('loading=true 时应渲染 Skeleton', () => {
    const { container } = render(
      <PlatformKPI title="测试" items={mockRows} loading={true} />,
    )
    // TDesign Skeleton 渲染 t-skeleton__row 类
    expect(container.querySelector('.t-skeleton__row')).not.toBeNull()
  })

  it('FR-17: 可用于 token_total 聚合查询', () => {
    const tokenTotalRows: PlatformUsageRow[] = [
      { tenant_id: 't1', resource_type: 'token_total', total_quantity: 800, unit: 'tokens' },
      { tenant_id: 't2', resource_type: 'token_total', total_quantity: 300, unit: 'tokens' },
    ]
    render(<PlatformKPI title="全平台 Token 合计" items={tokenTotalRows} />)
    // 800 + 300 = 1100；TDesign Statistic 渲染带千分位分隔符
    expect(screen.getByText('1,100')).toBeInTheDocument()
  })

  it('不同 unit 的行应使用第一行 unit', () => {
    const mixedRows: PlatformUsageRow[] = [
      { tenant_id: 't1', resource_type: 'instance_gpu_seconds', total_quantity: 100, unit: 'seconds' },
      { tenant_id: 't2', resource_type: 'instance_gpu_seconds', total_quantity: 200, unit: 'seconds' },
    ]
    render(<PlatformKPI title="GPU 算力总量" items={mixedRows} />)
    expect(screen.getByText('300')).toBeInTheDocument()
    expect(screen.getByText('seconds')).toBeInTheDocument()
  })
})
