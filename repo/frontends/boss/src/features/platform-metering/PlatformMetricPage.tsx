/**
 * PlatformMetricPage — BOSS 平台计量专页通用模板。
 *
 * 7 个专页（5 P0 + 2 P1）共享此模板，通过 route 参数从 METRIC_PAGES 查找配置。
 * 专页固定 resource_type，UI 不提供指标视角切换（hideMetricView=true）。
 *
 * P1 占位页（p0_enabled=false）显示 api-not-ready Empty「该指标待 API 合入（P1）」。
 * P0 专页：FilterBar + KPI + RankTable + TrendChart + 边界说明 + 钻取 Drawer，状态机同聚合页。
 */
import { useMemo, useState } from 'react'
import { Alert, Breadcrumb, Button, Empty } from 'tdesign-react'
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
 * 平台视角下 tenant_id 业务约束为必填，过滤掉空值行。
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

/** 专页默认 group_by（排行模式） */
const DEFAULT_GROUP_BY = 'tenant_id' as const

export interface PlatformMetricPageProps {
  /** 当前专页路由路径，用于从 METRIC_PAGES 查找配置 */
  route: string
}

/**
 * 平台计量专页通用模板。
 *
 * 根据 route 从 METRIC_PAGES 查找配置：
 * - p0_enabled=false → P1 占位页，显示 api-not-ready Empty
 * - p0_enabled=true → FilterBar(hideMetricView) + KPI + RankTable + TrendChart + 边界说明 + Drawer
 */
export function PlatformMetricPage({ route }: PlatformMetricPageProps) {
  const pageConfig = METRIC_PAGES.find((p) => p.route === route)

  if (!pageConfig) {
    return (
      <BossPage>
        <BossPageHeader title="未知专页" />
        <Empty description={`未找到路由 ${route} 的专页配置`} />
      </BossPage>
    )
  }

  // P1 占位页
  if (!pageConfig.p0_enabled) {
    return (
      <BossPage>
        <Breadcrumb>
          <Breadcrumb.BreadcrumbItem>平台计量与结算</Breadcrumb.BreadcrumbItem>
          <Breadcrumb.BreadcrumbItem>{pageConfig.title}</Breadcrumb.BreadcrumbItem>
        </Breadcrumb>
        <BossPageHeader title={pageConfig.title} />
        <BossContentCard>
          <Empty description="该指标待 API 合入（P1）" />
        </BossContentCard>
      </BossPage>
    )
  }

  // P0 专页
  return <PlatformMetricPageContent title={pageConfig.title} resourceType={pageConfig.resource_type} />
}

/**
 * P0 专页内容区。
 *
 * 固定 resource_type，hideMetricView=true，状态机同聚合页。
 * 包含：面包屑 + FilterBar + KPI + RankTable + TrendChart + 边界说明 + 钻取 Drawer。
 */
function PlatformMetricPageContent({ title, resourceType }: { title: string; resourceType: string }) {
  const [filter, setFilter] = useState<PlatformUsageFilter>(() => ({
    ...defaultTimeRange(),
    resource_type: resourceType,
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

  /** 行「查看明细」回调 → 打开 Drawer */
  const handleRowDrilldown = (row: PlatformUsageRow) => {
    setDrilldownRow(row)
    setDrawerVisible(true)
  }

  return (
    <BossPage>
      {/* 面包屑：平台计量与结算 / {title} */}
      <Breadcrumb>
        <Breadcrumb.BreadcrumbItem>平台计量与结算</Breadcrumb.BreadcrumbItem>
        <Breadcrumb.BreadcrumbItem>{title}</Breadcrumb.BreadcrumbItem>
      </Breadcrumb>

      <BossPageHeader title={title} />

      {/* 状态机：页顶 Alert 优先级 */}
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
          {/* 筛选区 — 专页隐藏指标视角切换 */}
          <BossContentCard>
            <PlatformFilterBar
              filter={filter}
              onFilterChange={setFilter}
              hideMetricView
              tenantOptions={PLATFORM_TENANT_OPTIONS}
            />
          </BossContentCard>

          {/* KPI 汇总卡 */}
          <PlatformKPI
            title={`${title} 全平台汇总`}
            items={displayRows}
            loading={loading}
          />

          {/* 跨租户排行表（固定 resource_type，含「查看明细」行操作） */}
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

          {/* 趋势图 */}
          <BossContentCard title="趋势图">
            <PlatformTrendChart
              data={displayRows}
              groupBy={debouncedFilter.group_by ?? DEFAULT_GROUP_BY}
              loading={loading}
            />
          </BossContentCard>

          {/* 边界说明（UX §4.3）：POST token-usage 为写入侧，非本页查询 */}
          <BossContentCard title="边界说明">
            <span style={{ color: 'var(--td-text-color-secondary)', fontSize: 14 }}>
              本页仅展示用量统计，不含账单金额、发票与结算。POST token-usage 为写入侧，非本页查询。
            </span>
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
