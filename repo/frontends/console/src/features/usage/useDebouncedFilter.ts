/**
 * Console 租户用量报表 — debounce 筛选 hook + 辅助函数。
 *
 * useDebouncedFilter: 筛选变更后延迟 300ms 返回 debounced 值，取消旧值。
 * defaultTimeRange: 近 30 天默认时间范围。
 * isValidRange: 校验 start_time < end_time。
 */

import { useEffect, useState } from 'react'
import type { UsageFilter } from './types'

/** 默认 debounce 延迟（ms），UX §8.4 定稿 */
export const DEFAULT_DEBOUNCE_MS = 300

/**
 * 计算 30 天前的起始时间（用于默认时间范围）。
 *
 * @returns 近 30 天的 { start_time, end_time } ISO 8601 字符串
 */
export function defaultTimeRange(): { start_time: string; end_time: string } {
  const end = new Date()
  const start = new Date(end.getTime() - 30 * 24 * 60 * 60 * 1000)
  return {
    start_time: start.toISOString(),
    end_time: end.toISOString(),
  }
}

/**
 * 校验时间范围是否有效（start_time < end_time）。
 *
 * @param filter 含 start_time / end_time 的筛选对象
 * @returns true 表示 start < end，可触发查询
 */
export function isValidRange(filter: { start_time: string; end_time: string }): boolean {
  const start = new Date(filter.start_time).getTime()
  const end = new Date(filter.end_time).getTime()
  return !Number.isNaN(start) && !Number.isNaN(end) && start < end
}

/**
 * Debounce 筛选 hook。
 *
 * 筛选变更后延迟 delayMs（默认 300ms）返回最新值；延迟内再次变更则取消旧值，
 * 只返回最新一次的 filter，避免频繁请求（SPEC §5.1）。
 *
 * @param filter 当前筛选状态
 * @param delayMs 延迟毫秒，默认 300ms
 * @returns debounced 后的筛选值
 */
export function useDebouncedFilter(filter: UsageFilter, delayMs: number = DEFAULT_DEBOUNCE_MS): UsageFilter {
  const [debounced, setDebounced] = useState<UsageFilter>(filter)

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebounced(filter)
    }, delayMs)
    return () => clearTimeout(timer)
  }, [filter, delayMs])

  return debounced
}
