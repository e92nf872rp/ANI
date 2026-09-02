/**
 * BOSS 平台计量 — 平台用量查询 hook。
 *
 * usePlatformUsageQuery: 基于 React Query 的平台跨租户用量查询。
 * queryKey 构建：['metering', 'platform', filter]，保证相同筛选不重复请求。
 * 调用 coreApi.GET('/metering/usage/platform')。
 */

import { useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'
import type { PlatformUsageFilter } from './types'

/**
 * 构建平台用量查询的 queryKey。
 *
 * queryKey 格式：['metering', 'platform', filter]
 * filter 中的 undefined 字段不参与 queryKey（React Query 默认行为）。
 *
 * @param filter 平台用量筛选条件
 * @returns React Query queryKey 数组
 */
export function buildPlatformUsageQueryKey(filter: PlatformUsageFilter): readonly unknown[] {
  return ['metering', 'platform', filter] as const
}

/**
 * 平台用量查询 hook（SPEC §4.1、§5.1）。
 *
 * 调用 GET /metering/usage/platform，需 scope:metering:platform:read 权限。
 * 403/404/501 不重试；5xx/network 默认重试 3 次。
 *
 * @param filter 平台用量筛选条件（start_time / end_time 必填）
 * @returns React Query 结果（data / isLoading / error / refetch 等）
 */
export function usePlatformUsageQuery(filter: PlatformUsageFilter) {
  return useQuery({
    queryKey: buildPlatformUsageQueryKey(filter),
    queryFn: async () => {
      const { data, error, response } = await coreApi.GET('/metering/usage/platform', {
        params: {
          query: {
            start_time: filter.start_time,
            end_time: filter.end_time,
            resource_type: filter.resource_type,
            group_by: filter.group_by,
            tenant_id: filter.tenant_id,
          },
        },
      })
      if (error) {
        // openapi-fetch 的 error 只是响应体 JSON，不含 status；
        // 将 response.status 挂载到 error 上供 retry 判断
        const responseError = error as unknown as { status: number }
        responseError.status = response.status
        throw error
      }
      return data
    },
    // 403 / 404 / 501 不重试（SPEC §6.2）
    retry: (failureCount, error) => {
      // openapi-fetch 的 error 响应对象含 status 字段
      const status = (error as { status?: number }).status
      if (status === 403 || status === 404 || status === 501) return false
      return failureCount < 3
    },
  })
}
