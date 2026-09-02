import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/gpu-hours — 平台 GPU-Hours 专页。
 *
 * 固定 resource_type=instance_gpu_seconds，不提供指标视角切换。
 * 面包屑：平台计量与结算 / 平台 GPU-Hours
 */
export const Route = createFileRoute('/_authenticated/metering/gpu-hours')({
  component: () => <PlatformMetricPage route="/metering/gpu-hours" />,
})
