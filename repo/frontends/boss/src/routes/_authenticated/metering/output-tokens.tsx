import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/output-tokens — 平台 Output Tokens 专页。
 *
 * 固定 resource_type=token_output，不提供指标视角切换。
 * 面包屑：平台计量与结算 / 平台 Output Tokens
 */
export const Route = createFileRoute('/_authenticated/metering/output-tokens')({
  component: () => <PlatformMetricPage route="/metering/output-tokens" />,
})
