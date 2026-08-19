import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/cpu-hours — 平台 CPU-Hours 专页。
 *
 * 固定 resource_type=instance_cpu_seconds，不提供指标视角切换。
 * 面包屑：平台计量与结算 / 平台 CPU-Hours
 */
export const Route = createFileRoute('/_authenticated/metering/cpu-hours')({
  component: () => <PlatformMetricPage route="/metering/cpu-hours" />,
})
