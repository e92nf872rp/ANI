/**
 * UsageTable 单元测试（Issue #11）。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - 4 列：资源类型、用量（total_quantity 原样）、单位（unit 原样）、统计周期（period 可空显示 —）
 * - FR-18：不做 seconds→hours 换算，原样展示
 * - FR-17：未筛 resource_type 时可展示 token_total 行
 * - loading 态：Table loading
 * - rowKey：resource_type+period
 * - [UI] 匹配 UX §5.1 Table columns
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { UsageTable } from './UsageTable'
import type { UsageRow } from './types'

// mock tdesign-react Table：捕获 props，渲染为简易结构便于断言
interface MockTableColumn {
  title?: string
  colKey?: string
  cell?: (ctx: { row: UsageRow }) => unknown
}

interface MockTableProps {
  data?: Record<string, unknown>[]
  loading?: boolean
  rowKey?: string
  columns?: MockTableColumn[]
}

let lastTableProps: MockTableProps = {}

vi.mock('tdesign-react', () => ({
  Table: (props: MockTableProps) => {
    lastTableProps = props
    return (
      <div data-testid="table-mock" data-loading={props.loading}>
        {/* 渲染列标题 */}
        {props.columns?.map((column, index) => (
          <span key={index} data-testid={`col-${column.colKey}`}>
            {column.title}
          </span>
        ))}
        {/* 渲染行数据 */}
        {props.data?.map((row, index) => {
          const key = props.rowKey ? String(row[props.rowKey] ?? index) : String(index)
          const periodCell = props.columns?.find((column) => column.colKey === 'period')
          const periodContent =
            periodCell?.cell?.({ row: row as unknown as UsageRow }) ?? row.period
          return (
            <div key={key} data-testid={`row-${key}`}>
              <span data-testid={`cell-resource_type-${key}`}>{String(row.resource_type ?? '')}</span>
              <span data-testid={`cell-total_quantity-${key}`}>{String(row.total_quantity ?? '')}</span>
              <span data-testid={`cell-unit-${key}`}>{String(row.unit ?? '')}</span>
              <span data-testid={`cell-period-${key}`}>{String(periodContent ?? '')}</span>
            </div>
          )
        })}
      </div>
    )
  },
}))

/** 构造 UsageRow 测试数据 */
function row(
  resourceType: string,
  totalQuantity: number,
  unit: string,
  period?: string,
): UsageRow {
  return { resource_type: resourceType, total_quantity: totalQuantity, unit, period }
}

/** 从原始 UsageRow 计算 rowKey（与组件内 buildRowKey 逻辑一致） */
function expectedRowKey(r: UsageRow): string {
  return `${r.resource_type ?? ''}+${r.period ?? ''}`
}

describe('UsageTable', () => {
  it('应有 4 列：资源类型、用量、单位、统计周期', () => {
    render(<UsageTable items={[]} loading={false} />)

    expect(screen.getByTestId('col-resource_type')).toHaveTextContent('资源类型')
    expect(screen.getByTestId('col-total_quantity')).toHaveTextContent('用量')
    expect(screen.getByTestId('col-unit')).toHaveTextContent('单位')
    expect(screen.getByTestId('col-period')).toHaveTextContent('统计周期')
  })

  it('loading=true 时应传入 Table loading 属性', () => {
    render(<UsageTable items={[]} loading={true} />)
    expect(lastTableProps.loading).toBe(true)
    expect(screen.getByTestId('table-mock')).toHaveAttribute('data-loading', 'true')
  })

  it('loading=false 时 loading 属性应为 false', () => {
    render(<UsageTable items={[]} loading={false} />)
    expect(lastTableProps.loading).toBe(false)
  })

  it('rowKey 应为 resource_type+period 复合键（通过 _rowKey 字段）', () => {
    const items: UsageRow[] = [
      row('token_input', 100, 'tokens', '2026-07-01'),
    ]
    render(<UsageTable items={items} loading={false} />)

    expect(lastTableProps.rowKey).toBe('_rowKey')
    const data = lastTableProps.data ?? []
    expect(data[0]._rowKey).toBe('token_input+2026-07-01')
  })

  it('rowKey 在 period 为空时应用空字符串占位', () => {
    const items: UsageRow[] = [
      row('token_input', 100, 'tokens'),
    ]
    render(<UsageTable items={items} loading={false} />)

    const data = lastTableProps.data ?? []
    expect(data[0]._rowKey).toBe('token_input+')
  })

  it('rowKey 应区分同一 resource_type 不同 period 的多行', () => {
    const items: UsageRow[] = [
      row('token_input', 10, 'tokens', '2026-07-01'),
      row('token_input', 20, 'tokens', '2026-07-02'),
    ]
    render(<UsageTable items={items} loading={false} />)

    const data = lastTableProps.data ?? []
    expect(data[0]._rowKey).not.toBe(data[1]._rowKey)
  })

  it('应原样展示 total_quantity + unit（FR-18 不做换算）', () => {
    const items: UsageRow[] = [
      row('instance_gpu_seconds', 3600, 'seconds'),
    ]
    render(<UsageTable items={items} loading={false} />)

    const key = expectedRowKey(items[0])
    // 原样展示 3600 和 seconds，不换算为 1 hours
    expect(screen.getByTestId(`cell-total_quantity-${key}`)).toHaveTextContent('3600')
    expect(screen.getByTestId(`cell-unit-${key}`)).toHaveTextContent('seconds')
  })

  it('period 为空时应显示 —', () => {
    const items: UsageRow[] = [
      row('token_input', 100, 'tokens'),
    ]
    render(<UsageTable items={items} loading={false} />)

    const key = expectedRowKey(items[0])
    expect(screen.getByTestId(`cell-period-${key}`)).toHaveTextContent('—')
  })

  it('period 有值时应原样展示 period', () => {
    const items: UsageRow[] = [
      row('token_input', 100, 'tokens', '2026-07-01'),
    ]
    render(<UsageTable items={items} loading={false} />)

    const key = expectedRowKey(items[0])
    expect(screen.getByTestId(`cell-period-${key}`)).toHaveTextContent('2026-07-01')
  })

  describe('FR-17: token_total 行展示', () => {
    it('未筛 resource_type 时应展示 token_total 行', () => {
      const items: UsageRow[] = [
        row('token_total', 300, 'tokens', '2026-07-01'),
      ]
      render(<UsageTable items={items} loading={false} />)

      const key = expectedRowKey(items[0])
      expect(screen.getByTestId(`cell-resource_type-${key}`)).toHaveTextContent('token_total')
      expect(screen.getByTestId(`cell-total_quantity-${key}`)).toHaveTextContent('300')
    })

    it('应允许同时展示 token_total 和其他 resource_type 行', () => {
      const items: UsageRow[] = [
        row('token_input', 100, 'tokens', '2026-07-01'),
        row('token_output', 200, 'tokens', '2026-07-01'),
        row('token_total', 300, 'tokens', '2026-07-01'),
      ]
      render(<UsageTable items={items} loading={false} />)

      // 三行都应渲染
      expect(screen.getByTestId('row-token_input+2026-07-01')).toBeInTheDocument()
      expect(screen.getByTestId('row-token_output+2026-07-01')).toBeInTheDocument()
      expect(screen.getByTestId('row-token_total+2026-07-01')).toBeInTheDocument()
    })
  })
})
