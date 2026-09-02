/**
 * PlatformUsagePage — BOSS 平台计量聚合页容器。
 *
 * 组合 FilterBar + KPI + RankTable + TrendChart + 专页入口 Link 组 + Drawer。
 * 默认时间范围近 30 天；group_by=tenant_id 排行模式。
 *
 * 状态优先级（页顶 Alert 仅显示一条）：
 *   api-not-ready > forbidden > error > dev_profile
 *
 * 租户 Select 使用常量列表，不受筛选条件影响。
 */
import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Alert, Button, Empty } from 'tdesign-react'
import { BossPage, BossPageHeader, BossContentCard } from '@/components/shell'
import { PlatformFilterBar } from './PlatformFilterBar'
import { ApiNotReadyAlert } from './ApiNotReadyAlert'
import { DevProfileAlert } from './DevProfileAlert'
import { PlatformKPI } from './PlatformKPI'
import { PlatformRankTable } from './PlatformRankTable'
import { PlatformTrendChart } from './PlatformTrendChart'
import { TenantDrilldownDrawer } from './TenantDrilldownDrawer'
import { useDebouncedFilter, defaultTimeRange } from './useDebouncedFilter'
import { usePlatformUsageQuery } from './usePlatformUsageQuery'
import { METRIC_PAGES, PLATFORM_TENANT_OPTIONS } from './constants'
import type { PlatformUsageFilter, PlatformUsageRow } from './types'

/**
 * 将 API 返回的 items 转换为 PlatformUsageRow[]。
 *
 * OpenAPI 中 tenant_id 为 `string | null | undefined`，平台视角下业务约束为必填。
 * 过滤掉 tenant_id 为空的行并做类型收窄。
 */
function toPlatformRows(items: { tenant_id?: string | null; resource_type: string; total_quantity: number; unit: string; period?: string | null }[]): PlatformUsageRow[] {
  return items
    .filter((item): item is { tenant_id: string; resource_type: string; total_quantity: number; unit: string; period?: string | null } => Boolean(item.tenant_id))
    .map((item) => ({
      tenant_id: item.tenant_id,
      resource_type: item.resource_type,
      total_quantity: item.total_quantity,
      unit: item.unit,
      period: item.period ?? null,
    }))
}

/** 聚合页默认 group_by（排行模式） */
const DEFAULT_GROUP_BY = 'tenant_id' as const

/**
 * 平台计量聚合页。
 *
 * 进入 /tenant/usage-billing 时渲染。默认时间范围近 30 天，group_by=tenant_id。
 */
export function PlatformUsagePage() {
  // 初始 filter：近 30 天 + group_by=tenant_id
  const [filter, setFilter] = useState<PlatformUsageFilter>(() => ({
    ...defaultTimeRange(),
    group_by: DEFAULT_GROUP_BY,
  }))
  const debouncedFilter = useDebouncedFilter(filter)
  // debounce 等待期：filter 已变但 debouncedFilter 还是旧值，此时旧数据不应展示
  const isDebouncing = filter !== debouncedFilter

  // 钻取 Drawer 状态
  const [drilldownRow, setDrilldownRow] = useState<PlatformUsageRow | null>(null)
  const [drawerVisible, setDrawerVisible] = useState(false)

  const query = usePlatformUsageQuery(debouncedFilter)

  // 错误状态分类
  const errorStatus = query.error ? (query.error as { status?: number }).status : undefined
  const isApiNotReady = errorStatus === 404 || errorStatus === 501
  const isForbidden = errorStatus === 403
  const isError = Boolean(query.error) && !isApiNotReady && !isForbidden

  const devProfile = query.data?.dev_profile
  const isDevProfile = devProfile ? !devProfile.real_provider : false

  const rows = useMemo(
    () => toPlatformRows(query.data?.items ?? []),
    [query.data?.items],
  )

  // 统一 loading 态：查询中或 debounce 等待中均显示 loading，避免旧数据残留
  const loading = query.isFetching || isDebouncing
  // loading 期间传空数据，避免 Table 在 loading 遮罩下显示旧行
  const displayRows = loading ? [] : rows

  /** 行「查看明细」回调 */
  const handleRowDrilldown = (row: PlatformUsageRow) => {
    setDrilldownRow(row)
    setDrawerVisible(true)
  }

  // KPI 标题
  const kpiTitle = '全平台用量汇总'

  return (
    <BossPage>
      <BossPageHeader
        title="租户计费与用量"
        description="跨租户用量排行与趋势分析"
      />

      {/* 状态机：页顶 Alert 优先级 api-not-ready > forbidden > error > dev_profile */}
      <ApiNotReadyAlert visible={isApiNotReady} />
      {isForbidden && (
        <Alert theme="error" message="您没有权限查看平台计量数据" close={false} />
      )}
      {isError && (
        <Alert
          theme="error"
          message="用量数据加载失败，请稍后重试"
          close={false}
          operation={<Button onClick={() => query.refetch()}>重试</Button>}
        />
      )}
      {isDevProfile && devProfile && <DevProfileAlert dev_profile={devProfile} />}

      {/* api-not-ready 时禁用数据区 */}
      {!isApiNotReady && (
        <>
          {/* 筛选区 */}
          <BossContentCard>
            <PlatformFilterBar
              filter={filter}
              onFilterChange={setFilter}
              tenantOptions={PLATFORM_TENANT_OPTIONS}
            />
          </BossContentCard>

          {/* KPI 汇总卡 */}
          <PlatformKPI
            title={kpiTitle}
            items={displayRows}
            loading={loading}
          />

          {/* 租户排行表（UX §4.2：排行表在趋势图上方） */}
          <BossContentCard title="跨租户排行">
            <PlatformRankTable
              data={displayRows}
              loading={loading}
              onRowDrilldown={handleRowDrilldown}
            />
            {!loading && displayRows.length === 0 && !query.error && (
              <Empty description="当前条件下暂无租户用量数据" />
            )}
          </BossContentCard>

          {/* 趋势图（UX §4.2：趋势图在排行表下方） */}
          <BossContentCard title="趋势图">
            <PlatformTrendChart
              data={displayRows}
              groupBy={debouncedFilter.group_by ?? DEFAULT_GROUP_BY}
              loading={loading}
            />
          </BossContentCard>

          {/* 专页入口 Link 组 — 7 专页跳转（UX §4.2 / Issue AC） */}
          <BossContentCard title="专页入口">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
              {METRIC_PAGES.map((page) => (
                <Link
                  key={page.route}
                  to={page.route}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    padding: '6px 12px',
                    borderRadius: 4,
                    border: '1px solid var(--td-component-border)',
                    color: 'var(--td-brand-color)',
                    textDecoration: 'none',
                    fontSize: 14,
                    opacity: page.p0_enabled ? 1 : 0.5,
                  }}
                >
                  {page.title}
                  {!page.p0_enabled && (
                    <span style={{ marginLeft: 4, fontSize: 12, color: 'var(--td-text-color-secondary)' }}>
                      （待 API）
                    </span>
                  )}
                </Link>
              ))}
            </div>
          </BossContentCard>
        </>
      )}

      {/* 钻取 Drawer */}
      <TenantDrilldownDrawer
        visible={drawerVisible}
        onClose={() => setDrawerVisible(false)}
        row={drilldownRow}
        baseFilter={debouncedFilter}
      />
    </BossPage>
  )
}
