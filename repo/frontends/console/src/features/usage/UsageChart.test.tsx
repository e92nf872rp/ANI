/**
 * UsageChart 单元测试（Issue #10）。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - ECharts 折线/柱图渲染 items[]
 * - x 轴: period (day/hour) 或 resource_type；y 轴: total_quantity
 * - loading 态: Skeleton
 * - empty 态 (items=[]): 不渲染假折线
 * - Typecheck 通过
 *
 * echarts-for-react 在 jsdom 中无法真正渲染 canvas，故 mock 为简单 div 并捕获 option prop。
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { EChartsOption } from 'echarts'
import { UsageChart } from './UsageChart'
import type { UsageRow } from './types'

// mock echarts-for-react：捕获传入的 option，渲染为带 data-testid 的 div
let lastOption: EChartsOption | undefined

interface TestChartSeries {
  name?: string
  type?: string
  data?: number[]
}

interface TestChartOption {
  xAxis?: { data?: string[] }
  series?: TestChartSeries[]
}

function testOption(): TestChartOption {
  return lastOption as TestChartOption
}

vi.mock('echarts-for-react', () => ({
  default: ({ option }: { option: EChartsOption }) => {
    lastOption = option
    return <div data-testid="echarts-mock" />
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

describe('UsageChart', () => {
  it('loading 态应显示 Skeleton 而非图表', () => {
    render(<UsageChart items={[]} loading={true} />)
    // Skeleton 渲染时不应出现 Empty 或图表
    expect(screen.queryByTestId('echarts-mock')).not.toBeInTheDocument()
    // Empty 的 description 文案不应出现
    expect(screen.queryByText('当前时间范围内暂无用量数据')).not.toBeInTheDocument()
  })

  it('empty 态 (items=[]) 应显示 Empty 且不渲染假折线', () => {
    render(<UsageChart items={[]} loading={false} />)
    // 不应渲染图表
    expect(screen.queryByTestId('echarts-mock')).not.toBeInTheDocument()
    // 应显示 Empty 文案
    expect(screen.getByText('当前时间范围内暂无用量数据')).toBeInTheDocument()
  })

  it('有数据时应渲染 ECharts 图表', () => {
    const items: UsageRow[] = [
      row('token_input', 100, 'tokens', '2026-07-01'),
      row('token_output', 200, 'tokens', '2026-07-01'),
    ]
    render(<UsageChart items={items} loading={false} groupBy="resource_type" />)
    expect(screen.getByTestId('echarts-mock')).toBeInTheDocument()
  })

  describe('group_by=resource_type（柱图）', () => {
    it('x 轴应为 resource_type，y 轴 total_quantity', () => {
      const items: UsageRow[] = [
        row('token_input', 100, 'tokens'),
        row('token_output', 200, 'tokens'),
      ]
      render(<UsageChart items={items} loading={false} groupBy="resource_type" />)

      const xAxis = testOption().xAxis
      expect(xAxis?.data).toEqual(['token_input', 'token_output'])

      const series = testOption().series ?? []
      expect(series).toHaveLength(1)
      expect(series[0].type).toBe('bar')
      expect(series[0].data).toEqual([100, 200])
    })
  })

  describe('group_by=day（时间桶折线）', () => {
    it('x 轴应为 period，按 resource_type 拆分多系列', () => {
      const items: UsageRow[] = [
        row('token_input', 10, 'tokens', '2026-07-01'),
        row('token_input', 20, 'tokens', '2026-07-02'),
        row('token_output', 30, 'tokens', '2026-07-01'),
        row('token_output', 40, 'tokens', '2026-07-02'),
      ]
      render(<UsageChart items={items} loading={false} groupBy="day" />)

      const xAxis = testOption().xAxis
      expect(xAxis?.data).toEqual(['2026-07-01', '2026-07-02'])

      const series = testOption().series ?? []
      expect(series).toHaveLength(2)
      // 每条系列应为 line 类型
      for (const s of series) {
        expect(s.type).toBe('line')
      }
      // 按 resource_type 分组
      const names = series.map((seriesItem) => seriesItem.name).sort()
      expect(names).toEqual(['token_input', 'token_output'])
    })

    it('不同 resource_type 应映射到对应 period 桶', () => {
      const items: UsageRow[] = [
        row('token_input', 10, 'tokens', '2026-07-01'),
        row('token_input', 30, 'tokens', '2026-07-02'),
        row('token_output', 50, 'tokens', '2026-07-01'),
      ]
      render(<UsageChart items={items} loading={false} groupBy="day" />)

      const series = testOption().series ?? []
      const inputSeries = series.find((seriesItem) => seriesItem.name === 'token_input')
      const outputSeries = series.find((seriesItem) => seriesItem.name === 'token_output')

      expect(inputSeries?.data).toEqual([10, 30])
      expect(outputSeries?.data).toEqual([50, 0])
    })
  })

  describe('group_by=hour（时间桶折线）', () => {
    it('应使用折线图且 x 轴为 period', () => {
      const items: UsageRow[] = [
        row('token_input', 5, 'tokens', '2026-07-01T00:00:00Z'),
        row('token_input', 8, 'tokens', '2026-07-01T01:00:00Z'),
      ]
      render(<UsageChart items={items} loading={false} groupBy="hour" />)

      const series = testOption().series ?? []
      expect(series).toHaveLength(1)
      expect(series[0].type).toBe('line')
      expect(testOption().xAxis?.data).toEqual([
        '2026-07-01T00:00:00Z',
        '2026-07-01T01:00:00Z',
      ])
    })
  })
})
