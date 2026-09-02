import { createFileRoute } from '@tanstack/react-router'
import { PlatformUsagePage } from '@/features/platform-metering/PlatformUsagePage'

/**
 * /tenant/usage-billing — BOSS 平台计量聚合页。
 *
 * 组合 FilterBar + KPI + RankTable + TrendChart + 专页入口 + Drawer。
 * 默认时间范围近 30 天；group_by=tenant_id 排行模式。
 */
export const Route = createFileRoute('/_authenticated/tenant/usage-billing')({
  component: PlatformUsagePage,
})
