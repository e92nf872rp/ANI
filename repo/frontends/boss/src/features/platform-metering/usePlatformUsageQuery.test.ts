/**
 * usePlatformUsageQuery 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - queryKey 构建
 * - queryKey 随 filter 变化而变化
 */
import { describe, it, expect } from 'vitest'
import { buildPlatformUsageQueryKey } from './usePlatformUsageQuery'
import type { PlatformUsageFilter } from './types'

describe('buildPlatformUsageQueryKey', () => {
  const baseFilter: PlatformUsageFilter = {
    start_time: '2026-01-01T00:00:00.000Z',
    end_time: '2026-02-01T00:00:00.000Z',
  }

  it('应返回以 metering/platform 开头的 queryKey', () => {
    const key = buildPlatformUsageQueryKey(baseFilter)
    expect(key[0]).toBe('metering')
    expect(key[1]).toBe('platform')
  })

  it('queryKey 第三项应包含完整 filter', () => {
    const key = buildPlatformUsageQueryKey(baseFilter)
    expect(key[2]).toEqual(baseFilter)
  })

  it('不同 filter 应产生不同 queryKey', () => {
    const filterA: PlatformUsageFilter = { ...baseFilter, resource_type: 'token_input' }
    const filterB: PlatformUsageFilter = { ...baseFilter, resource_type: 'token_output' }
    const keyA = buildPlatformUsageQueryKey(filterA)
    const keyB = buildPlatformUsageQueryKey(filterB)
    expect(keyA).not.toEqual(keyB)
  })

  it('相同 filter 应产生相同 queryKey', () => {
    const keyA = buildPlatformUsageQueryKey(baseFilter)
    const keyB = buildPlatformUsageQueryKey(baseFilter)
    expect(keyA).toEqual(keyB)
  })

  it('带 group_by 的 filter 应反映在 queryKey 中', () => {
    const filter: PlatformUsageFilter = { ...baseFilter, group_by: 'tenant_id' }
    const key = buildPlatformUsageQueryKey(filter)
    expect((key[2] as PlatformUsageFilter).group_by).toBe('tenant_id')
  })

  it('带 tenant_id 的 filter 应反映在 queryKey 中（钻取场景）', () => {
    const filter: PlatformUsageFilter = { ...baseFilter, tenant_id: 'tenant-123', group_by: 'day' }
    const key = buildPlatformUsageQueryKey(filter)
    expect((key[2] as PlatformUsageFilter).tenant_id).toBe('tenant-123')
  })
})
