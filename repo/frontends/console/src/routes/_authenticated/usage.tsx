/**
 * Console 租户用量报表页（Issue #012）。
 *
 * 组合 UsageFilterBar + UsageChart + UsageTable，三者共享同一 queryKey。
 * 完整状态机：idle/success、loading、empty、error、forbidden、dev_profile 横幅、invalid range、tab disabled。
 *
 * @see PRD FR-4（调用 GET /metering/usage，不绕路 Services）
 * @see PRD FR-12（dev_profile 横幅）
 * @see PRD FR-17（token_total 无 Tab）
 * @see PRD FR-18（单位原样展示）
 * @see UX §6.1 全部 8 个状态
 * @see SPEC §5.3 状态机
 */

import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button } from 'tdesign-react'
import { coreApi } from '@/api/coreClient'
import { ConsoleContentCard, ConsolePage, ConsolePageHeader } from '@/components/shell'
import { UsageFilterBar } from '@/features/usage/UsageFilterBar'
import { UsageChart } from '@/features/usage/UsageChart'
import { UsageTable } from '@/features/usage/UsageTable'
import {
  defaultTimeRange,
  isValidRange,
  useDebouncedFilter,
} from '@/features/usage/useDebouncedFilter'
import type { UsageFilter, UsageRow } from '@/features/usage/types'

export const Route = createFileRoute('/_authenticated/usage')({
  component: UsagePage,
})

/** FR-12 dev 横幅固定文案 */
const DEV_PROFILE_BANNER =
  '当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。'

/**
 * Console 租户用量报表页。
 *
 * 组合 FilterBar + Chart + Table，共享同一 queryKey。
 * debounce 300ms 自动查询，无查询按钮。
 */
function UsagePage() {
  // 默认时间范围：近 30 天（SPEC §5.1）
  const initial = defaultTimeRange()
  const [filter, setFilter] = useState<UsageFilter>({
    start_time: initial.start_time,
    end_time: initial.end_time,
    group_by: 'resource_type',
  })

  // debounce 300ms 自动查询（UX §8.4 定稿）
  const debouncedFilter = useDebouncedFilter(filter)

  const isValid = isValidRange(debouncedFilter)

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['metering', 'usage', debouncedFilter],
    queryFn: async () => {
      const { data: responseData, error: responseError, response } =
        await coreApi.GET('/metering/usage', {
          params: {
            query: {
              start_time: debouncedFilter.start_time,
              end_time: debouncedFilter.end_time,
              resource_type: debouncedFilter.resource_type,
              group_by: debouncedFilter.group_by,
            },
          },
        })
      if (responseError !== undefined) {
        // openapi-fetch 非 2xx 时不抛异常，需手动抛出让 React Query 进入 error 通道
        throw { status: response.status, body: responseError }
      }
      return responseData
    },
    enabled: isValid,
    // 403/401 为权限拒绝，重试无意义；其余错误最多重试 1 次
    retry: (failureCount, err) => {
      const status = (err as unknown as Record<string, unknown>)?.status
      if (status === 403 || status === 401) return false
      return failureCount < 1
    },
  })

  // 403 forbidden 检测（SPEC §5.3、UX §6.1）
  const isForbidden = error !== null && error !== undefined && is403Error(error)

  const items: UsageRow[] = (data?.items ?? []) as UsageRow[]
  const devProfile = data?.dev_profile
  const showDevBanner =
    !isError && devProfile !== undefined && devProfile.real_provider === false

  return (
    <ConsolePage>
      <ConsolePageHeader
        title="用量报表"
        subtitle="查看本租户用量统计（Core API /metering/usage）"
      />

      {/* dev_profile 横幅（FR-12，条件显示） */}
      {showDevBanner && (
        <Alert theme="warning" message={DEV_PROFILE_BANNER} />
      )}

      <ConsoleContentCard>
        {/* 筛选区 */}
        <UsageFilterBar filter={filter} onChange={setFilter} />

        {/* forbidden 态：Alert + 隐藏数据区（UX §6.1） */}
        {isForbidden ? (
          <Alert
            theme="error"
            message="您没有权限查看用量报表"
            style={{ marginTop: 16 }}
          />
        ) : isError ? (
          /* error 态：Alert + 重试按钮，保留筛选（UX §6.1） */
          <Alert
            theme="error"
            message="用量数据加载失败，请稍后重试"
            operation={
              <Button variant="text" onClick={() => void refetch()}>
                重试
              </Button>
            }
            style={{ marginTop: 16 }}
          />
        ) : (
          /* 数据区：Chart + Table 共享同一数据源 */
          <>
            <div style={{ marginTop: 16 }}>
              <UsageChart
                items={items}
                loading={isLoading}
                groupBy={debouncedFilter.group_by}
              />
            </div>
            <div style={{ marginTop: 16 }}>
              <UsageTable items={items} loading={isLoading} />
            </div>
          </>
        )}
      </ConsoleContentCard>

      {/* 边界说明（静态文案，不含账单金额） */}
      <ConsoleContentCard>
        <p style={{ color: 'var(--td-text-color-secondary)', fontSize: 14, margin: 0 }}>
          本页仅展示用量统计，不含账单金额、发票与结算。
        </p>
      </ConsoleContentCard>
    </ConsolePage>
  )
}

/**
 * 判断 React Query error 是否为 403 forbidden。
 *
 * openapi-fetch 在非 2xx 时将 response 放入 error 通道；
 * 通过检查 HTTP status 判断是否为 403。
 *
 * @param error React Query 返回的 error 对象
 * @returns true 表示 403 forbidden
 */
function is403Error(error: unknown): boolean {
  if (error === null || error === undefined) return false
  if (typeof error !== 'object') return false
  const err = error as Record<string, unknown>
  return err.status === 403 || err.statusCode === 403
}
