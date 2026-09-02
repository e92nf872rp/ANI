/**
 * constants.ts 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - METRIC_PAGES: 5 P0（GPU/CPU/Memory/Input/Output）+ 2 P1（Storage/KB, p0_enabled=false）
 * - PLATFORM_GROUP_BY_OPTIONS: tenant_id / day / hour
 * - METRIC_VIEW_OPTIONS: 聚合页指标视角 5 类（GPU/CPU/Memory/Input/Output）
 */
import { describe, it, expect } from 'vitest'
import { METRIC_PAGES, PLATFORM_GROUP_BY_OPTIONS, METRIC_VIEW_OPTIONS } from './constants'

describe('METRIC_PAGES', () => {
  it('应有 7 个专页（5 P0 + 2 P1）', () => {
    expect(METRIC_PAGES).toHaveLength(7)
  })

  it('应有 5 个 P0 启用页', () => {
    const p0 = METRIC_PAGES.filter((p) => p.p0_enabled)
    expect(p0).toHaveLength(5)
  })

  it('应有 2 个 P1 占位页（p0_enabled=false）', () => {
    const p1 = METRIC_PAGES.filter((p) => !p.p0_enabled)
    expect(p1).toHaveLength(2)
  })

  it('P0 页应为 GPU/CPU/Memory/Input/Output', () => {
    const p0Routes = METRIC_PAGES.filter((p) => p.p0_enabled).map((p) => p.route)
    expect(p0Routes).toEqual([
      '/metering/gpu-hours',
      '/metering/cpu-hours',
      '/metering/memory-gbhours',
      '/metering/input-tokens',
      '/metering/output-tokens',
    ])
  })

  it('P0 resource_type 应为对应的 API 枚举值', () => {
    const p0ResourceTypes = METRIC_PAGES.filter((p) => p.p0_enabled).map((p) => p.resource_type)
    expect(p0ResourceTypes).toEqual([
      'instance_gpu_seconds',
      'instance_cpu_seconds',
      'instance_memory_gib_seconds',
      'token_input',
      'token_output',
    ])
  })

  it('P1 页应为 Storage/KB', () => {
    const p1Routes = METRIC_PAGES.filter((p) => !p.p0_enabled).map((p) => p.route)
    expect(p1Routes).toEqual(['/metering/storage-gbdays', '/metering/kb-queries'])
  })

  it('P1 resource_type 应为 storage_gb_days / kb_query_count', () => {
    const p1ResourceTypes = METRIC_PAGES.filter((p) => !p.p0_enabled).map((p) => p.resource_type)
    expect(p1ResourceTypes).toEqual(['storage_gb_days', 'kb_query_count'])
  })

  it('每个配置项应有 route / title / resource_type', () => {
    for (const page of METRIC_PAGES) {
      expect(page.route).toBeTruthy()
      expect(page.title).toBeTruthy()
      expect(page.resource_type).toBeTruthy()
    }
  })

  it('不含 token_total 专页', () => {
    const hasTokenTotal = METRIC_PAGES.some((p) => p.resource_type === 'token_total')
    expect(hasTokenTotal).toBe(false)
  })
})

describe('PLATFORM_GROUP_BY_OPTIONS', () => {
  it('应有 3 个选项', () => {
    expect(PLATFORM_GROUP_BY_OPTIONS).toHaveLength(3)
  })

  it('应包含 tenant_id / day / hour', () => {
    const values = PLATFORM_GROUP_BY_OPTIONS.map((o) => o.value)
    expect(values).toEqual(['tenant_id', 'day', 'hour'])
  })

  it('每个选项应有 label', () => {
    for (const opt of PLATFORM_GROUP_BY_OPTIONS) {
      expect(opt.label).toBeTruthy()
    }
  })
})

describe('METRIC_VIEW_OPTIONS', () => {
  it('应有 5 个指标视角选项', () => {
    expect(METRIC_VIEW_OPTIONS).toHaveLength(5)
  })

  it('应包含 GPU/CPU/Memory/Input/Output', () => {
    const values = METRIC_VIEW_OPTIONS.map((o) => o.value)
    expect(values).toEqual([
      'instance_gpu_seconds',
      'instance_cpu_seconds',
      'instance_memory_gib_seconds',
      'token_input',
      'token_output',
    ])
  })

  it('每个选项应有 label', () => {
    for (const opt of METRIC_VIEW_OPTIONS) {
      expect(opt.label).toBeTruthy()
    }
  })

  it('不应包含 token_total（FR-17：无独立视角）', () => {
    const hasTokenTotal = METRIC_VIEW_OPTIONS.some((o) => o.value === 'token_total')
    expect(hasTokenTotal).toBe(false)
  })
})
