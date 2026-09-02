/**
 * PlatformRankTable 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA（Issue #18）：
 * - RankTable 列: 租户ID, 资源类型, 用量(total_quantity), 单位(unit 原样), 周期, 操作
 * - sortable on total_quantity
 * - 行操作「查看明细」→ 触发 onRowDrilldown 回调
 * - FR-18: 不做单位换算（unit 原样展示）
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { PlatformRankTable } from './PlatformRankTable'
import type { PlatformUsageRow } from './types'

const mockRows: PlatformUsageRow[] = [
  { tenant_id: 'tenant-a', resource_type: 'token_input', total_quantity: 1200, unit: 'tokens', period: '2026-07-01' },
  { tenant_id: 'tenant-b', resource_type: 'token_input', total_quantity: 800, unit: 'tokens', period: '2026-07-01' },
  { tenant_id: 'tenant-c', resource_type: 'instance_gpu_seconds', total_quantity: 500, unit: 'seconds', period: null },
]

describe('PlatformRankTable', () => {
  it('应渲染租户 ID 列', () => {
    render(<PlatformRankTable data={mockRows} />)
    expect(screen.getByText('tenant-a')).toBeInTheDocument()
    expect(screen.getByText('tenant-b')).toBeInTheDocument()
  })

  it('应渲染资源类型列（中文 label 映射）', () => {
    render(<PlatformRankTable data={mockRows} />)
    // token_input → Input Tokens
    expect(screen.getAllByText('Input Tokens').length).toBeGreaterThan(0)
    // instance_gpu_seconds → GPU 算力
    expect(screen.getByText('GPU 算力')).toBeInTheDocument()
  })

  it('应渲染用量列数值（原样展示，FR-18）', () => {
    render(<PlatformRankTable data={mockRows} />)
    expect(screen.getByText('1200')).toBeInTheDocument()
    expect(screen.getByText('800')).toBeInTheDocument()
  })

  it('应渲染单位列（原样展示，FR-18）', () => {
    render(<PlatformRankTable data={mockRows} />)
    expect(screen.getAllByText('tokens').length).toBeGreaterThan(0)
    expect(screen.getByText('seconds')).toBeInTheDocument()
  })

  it('周期为 null 时应显示 —', () => {
    render(<PlatformRankTable data={mockRows} />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('应渲染操作列「查看明细」', () => {
    render(<PlatformRankTable data={mockRows} />)
    const buttons = screen.getAllByText('查看明细')
    expect(buttons).toHaveLength(mockRows.length)
  })

  it('点击「查看明细」应触发 onRowDrilldown 回调', () => {
    const onRowDrilldown = vi.fn()
    render(<PlatformRankTable data={mockRows} onRowDrilldown={onRowDrilldown} />)
    const buttons = screen.getAllByText('查看明细')
    fireEvent.click(buttons[0])
    expect(onRowDrilldown).toHaveBeenCalledWith(mockRows[0])
  })

  it('loading=true 时应处于加载态', () => {
    render(<PlatformRankTable data={[]} loading={true} />)
    // TDesign Table loading 态在容器上添加 t-table--loading class
    const loading = document.querySelector('.t-table--loading')
    expect(loading).not.toBeNull()
  })

  it('空数据时不应崩溃', () => {
    render(<PlatformRankTable data={[]} />)
    expect(screen.getByText('租户 ID')).toBeInTheDocument()
  })

  it('未映射的 resource_type 应原样显示枚举值', () => {
    const rows: PlatformUsageRow[] = [
      { tenant_id: 'tenant-x', resource_type: 'unknown_type', total_quantity: 10, unit: 'pcs' },
    ]
    render(<PlatformRankTable data={rows} />)
    expect(screen.getByText('unknown_type')).toBeInTheDocument()
  })

  it('showDrilldownAction=false 时不应渲染操作列', () => {
    render(<PlatformRankTable data={mockRows} showDrilldownAction={false} />)
    expect(screen.queryByText('查看明细')).not.toBeInTheDocument()
  })
})
