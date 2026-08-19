/**
 * constants.ts 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - RESOURCE_TYPE_TABS: 5 启用 + 2 disabled
 * - 不含 token_total Tab（FR-17）
 * - GROUP_BY_OPTIONS: resource_type / az / day / hour
 */
import { describe, it, expect } from 'vitest'
import { RESOURCE_TYPE_TABS, GROUP_BY_OPTIONS } from './constants'

describe('RESOURCE_TYPE_TABS', () => {
  it('应有 7 个 Tab（5 启用 + 2 disabled）', () => {
    expect(RESOURCE_TYPE_TABS).toHaveLength(7)
  })

  it('应有 5 个启用 Tab', () => {
    const enabled = RESOURCE_TYPE_TABS.filter((t) => t.enabled)
    expect(enabled).toHaveLength(5)
  })

  it('应有 2 个 disabled Tab', () => {
    const disabled = RESOURCE_TYPE_TABS.filter((t) => !t.enabled)
    expect(disabled).toHaveLength(2)
  })

  it('启用 Tab 应为 GPU/CPU/Memory/Input/Output', () => {
    const enabledValues = RESOURCE_TYPE_TABS.filter((t) => t.enabled).map((t) => t.value)
    expect(enabledValues).toEqual([
      'instance_gpu_seconds',
      'instance_cpu_seconds',
      'instance_memory_gib_seconds',
      'token_input',
      'token_output',
    ])
  })

  it('disabled Tab 应为 Storage/KB', () => {
    const disabledValues = RESOURCE_TYPE_TABS.filter((t) => !t.enabled).map((t) => t.value)
    expect(disabledValues).toEqual(['storage_gb_days', 'kb_query_count'])
  })

  it('disabled Tab 应有 Tooltip 文案', () => {
    const disabled = RESOURCE_TYPE_TABS.filter((t) => !t.enabled)
    for (const tab of disabled) {
      expect(tab.disabledTooltip).toBeTruthy()
    }
  })

  it('不含 token_total Tab（FR-17）', () => {
    const hasTokenTotal = RESOURCE_TYPE_TABS.some((t) => t.value === 'token_total')
    expect(hasTokenTotal).toBe(false)
  })
})

describe('GROUP_BY_OPTIONS', () => {
  it('应有 4 个选项', () => {
    expect(GROUP_BY_OPTIONS).toHaveLength(4)
  })

  it('应包含 resource_type / az / day / hour', () => {
    const values = GROUP_BY_OPTIONS.map((o) => o.value)
    expect(values).toEqual(['resource_type', 'az', 'day', 'hour'])
  })

  it('每个选项应有 label', () => {
    for (const opt of GROUP_BY_OPTIONS) {
      expect(opt.label).toBeTruthy()
    }
  })
})
