import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/memory-gbhours — 平台 Memory-GBHours 专页。
 *
 * 固定 resource_type=instance_memory_gib_seconds，不提供指标视角切换。
 * 面包屑：平台计量与结算 / 平台 Memory-GBHours
 */
export const Route = createFileRoute('/_authenticated/metering/memory-gbhours')({
  component: () => <PlatformMetricPage route="/metering/memory-gbhours" />,
})
