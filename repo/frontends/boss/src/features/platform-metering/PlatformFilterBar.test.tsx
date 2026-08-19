/**
 * PlatformFilterBar 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - DateRangePicker: 必填，start < end 校验
 * - 指标视角 Select: 聚合页可切换 resource_type；专页不提供（hideMetricView=true）
 * - 租户 Select: filterable, clearable
 * - group_by Select: tenant_id / day / hour
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PlatformFilterBar } from './PlatformFilterBar'
import type { PlatformUsageFilter } from './types'
import { METRIC_VIEW_OPTIONS, PLATFORM_GROUP_BY_OPTIONS } from './constants'

const baseFilter: PlatformUsageFilter = {
  start_time: '2026-01-01T00:00:00.000Z',
  end_time: '2026-02-01T00:00:00.000Z',
  group_by: 'tenant_id',
}

describe('PlatformFilterBar', () => {
  it('聚合页模式下应渲染指标视角 Select', () => {
    const onFilterChange = vi.fn()
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
      />,
    )
    // 指标视角 Select 应存在（通过 placeholder 定位）
    expect(screen.getByPlaceholderText('选择指标视角')).toBeInTheDocument()
  })

  it('专页模式（hideMetricView=true）不应渲染指标视角 Select', () => {
    const onFilterChange = vi.fn()
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
        hideMetricView={true}
      />,
    )
    // 指标视角 Select 不应存在
    expect(screen.queryByPlaceholderText('选择指标视角')).not.toBeInTheDocument()
  })

  it('应渲染 group_by Select', () => {
    const onFilterChange = vi.fn()
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
      />,
    )
    // group_by Select 应存在（通过 placeholder 定位）
    expect(screen.getByPlaceholderText('选择分组维度')).toBeInTheDocument()
  })

  it('应渲染租户 Select', () => {
    const onFilterChange = vi.fn()
    const tenantOptions = [
      { label: '租户 A', value: 'tenant-a' },
      { label: '租户 B', value: 'tenant-b' },
    ]
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
        tenantOptions={tenantOptions}
      />,
    )
    expect(screen.getByPlaceholderText('筛选租户')).toBeInTheDocument()
  })

  it('group_by 选项应包含 tenant_id / day / hour', () => {
    // 验证常量（PlatformFilterBar 使用 PLATFORM_GROUP_BY_OPTIONS）
    const values = PLATFORM_GROUP_BY_OPTIONS.map((o) => o.value)
    expect(values).toEqual(['tenant_id', 'day', 'hour'])
  })

  it('指标视角选项应有 5 类（GPU/CPU/Memory/Input/Output）', () => {
    expect(METRIC_VIEW_OPTIONS).toHaveLength(5)
    const values = METRIC_VIEW_OPTIONS.map((o) => o.value)
    expect(values).toContain('instance_gpu_seconds')
    expect(values).toContain('instance_cpu_seconds')
    expect(values).toContain('instance_memory_gib_seconds')
    expect(values).toContain('token_input')
    expect(values).toContain('token_output')
  })

  it('DateRangePicker 应展示当前时间范围', () => {
    const onFilterChange = vi.fn()
    const { container } = render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
      />,
    )
    // DateRangePicker 输入框应包含日期值
    const inputs = container.querySelectorAll('input')
    expect(inputs.length).toBeGreaterThan(0)
    // 第一个输入框应包含 start_time 的日期部分
    expect(inputs[0]).toHaveValue('2026-01-01')
    // 第二个输入框应包含 end_time 的日期部分
    expect(inputs[1]).toHaveValue('2026-02-01')
  })

  it('时间范围无效时应显示错误提示', () => {
    const onFilterChange = vi.fn()
    const invalidFilter: PlatformUsageFilter = {
      ...baseFilter,
      start_time: '2026-02-01T00:00:00.000Z',
      end_time: '2026-01-01T00:00:00.000Z',
    }
    render(
      <PlatformFilterBar
        filter={invalidFilter}
        onFilterChange={onFilterChange}
      />,
    )
    expect(screen.getByText('结束时间必须晚于开始时间')).toBeInTheDocument()
  })

  it('时间范围有效时不应显示错误提示', () => {
    const onFilterChange = vi.fn()
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
      />,
    )
    expect(screen.queryByText('结束时间必须晚于开始时间')).not.toBeInTheDocument()
  })

  it('默认 group_by 应为 tenant_id', () => {
    const onFilterChange = vi.fn()
    render(
      <PlatformFilterBar
        filter={baseFilter}
        onFilterChange={onFilterChange}
      />,
    )
    // group_by Select 的值应为 tenant_id（对应 label「按租户」）
    // TDesign Select 选中值显示在 input 的 value 属性中
    expect(screen.getByDisplayValue('按租户')).toBeInTheDocument()
  })
})
