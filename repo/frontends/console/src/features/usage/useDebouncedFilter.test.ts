/**
 * useDebouncedFilter / defaultTimeRange / isValidRange 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - debounce 延迟后返回 debounced 值
 * - 取消旧值（延迟内再次变更只保留最新）
 * - defaultTimeRange 近 30 天
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  useDebouncedFilter,
  defaultTimeRange,
  isValidRange,
  DEFAULT_DEBOUNCE_MS,
} from './useDebouncedFilter'
import type { UsageFilter } from './types'

describe('defaultTimeRange', () => {
  it('应返回近 30 天的时间范围', () => {
    const before = Date.now()
    const range = defaultTimeRange()
    const after = Date.now()

    const end = new Date(range.end_time).getTime()
    const start = new Date(range.start_time).getTime()

    // end 应接近当前时间
    expect(end).toBeGreaterThanOrEqual(before)
    expect(end).toBeLessThanOrEqual(after)

    // start 应比 end 早约 30 天（允许 1 秒误差）
    const thirtyDaysMs = 30 * 24 * 60 * 60 * 1000
    expect(end - start).toBeGreaterThanOrEqual(thirtyDaysMs - 1000)
    expect(end - start).toBeLessThanOrEqual(thirtyDaysMs + 1000)
  })

  it('返回 ISO 8601 字符串', () => {
    const range = defaultTimeRange()
    expect(range.start_time).toMatch(/^\d{4}-\d{2}-\d{2}T/)
    expect(range.end_time).toMatch(/^\d{4}-\d{2}-\d{2}T/)
  })
})

describe('isValidRange', () => {
  it('start < end 时返回 true', () => {
    const filter = {
      start_time: '2026-01-01T00:00:00.000Z',
      end_time: '2026-02-01T00:00:00.000Z',
    }
    expect(isValidRange(filter)).toBe(true)
  })

  it('start >= end 时返回 false', () => {
    const filter = {
      start_time: '2026-02-01T00:00:00.000Z',
      end_time: '2026-01-01T00:00:00.000Z',
    }
    expect(isValidRange(filter)).toBe(false)
  })

  it('start === end 时返回 false', () => {
    const filter = {
      start_time: '2026-01-01T00:00:00.000Z',
      end_time: '2026-01-01T00:00:00.000Z',
    }
    expect(isValidRange(filter)).toBe(false)
  })

  it('无效日期返回 false', () => {
    const filter = {
      start_time: 'invalid',
      end_time: '2026-01-01T00:00:00.000Z',
    }
    expect(isValidRange(filter)).toBe(false)
  })
})

describe('useDebouncedFilter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const baseFilter: UsageFilter = {
    start_time: '2026-01-01T00:00:00.000Z',
    end_time: '2026-02-01T00:00:00.000Z',
  }

  it('初始值应等于传入的 filter', () => {
    const { result } = renderHook(() => useDebouncedFilter(baseFilter))
    expect(result.current).toEqual(baseFilter)
  })

  it('延迟后返回 debounced 值', () => {
    const { result, rerender } = renderHook(({ filter }) => useDebouncedFilter(filter), {
      initialProps: { filter: baseFilter },
    })

    const newFilter: UsageFilter = {
      ...baseFilter,
      resource_type: 'instance_gpu_seconds',
    }

    rerender({ filter: newFilter })

    // 延迟前应仍为旧值
    expect(result.current).toEqual(baseFilter)

    // 快进 300ms
    act(() => {
      vi.advanceTimersByTime(DEFAULT_DEBOUNCE_MS)
    })

    // 延迟后应为新值
    expect(result.current).toEqual(newFilter)
  })

  it('延迟内再次变更应取消旧值，只返回最新值', () => {
    const { result, rerender } = renderHook(({ filter }) => useDebouncedFilter(filter), {
      initialProps: { filter: baseFilter },
    })

    const filter1: UsageFilter = { ...baseFilter, resource_type: 'instance_gpu_seconds' }
    const filter2: UsageFilter = { ...baseFilter, resource_type: 'token_input' }

    // 第一次变更
    rerender({ filter: filter1 })

    // 快进 200ms（未到 300ms）
    act(() => {
      vi.advanceTimersByTime(200)
    })
    expect(result.current).toEqual(baseFilter)

    // 第二次变更（取消第一次的 timer）
    rerender({ filter: filter2 })

    // 快进 200ms（从第二次变更起算，总 400ms，但第一次已取消）
    act(() => {
      vi.advanceTimersByTime(200)
    })
    expect(result.current).toEqual(baseFilter)

    // 再快进 100ms（第二次变更满 300ms）
    act(() => {
      vi.advanceTimersByTime(100)
    })
    expect(result.current).toEqual(filter2)
  })

  it('支持自定义延迟', () => {
    const { result, rerender } = renderHook(
      ({ filter }) => useDebouncedFilter(filter, 500),
      { initialProps: { filter: baseFilter } },
    )

    const newFilter: UsageFilter = { ...baseFilter, group_by: 'day' }
    rerender({ filter: newFilter })

    // 300ms 时应未更新
    act(() => {
      vi.advanceTimersByTime(300)
    })
    expect(result.current).toEqual(baseFilter)

    // 500ms 时应更新
    act(() => {
      vi.advanceTimersByTime(200)
    })
    expect(result.current).toEqual(newFilter)
  })
})
