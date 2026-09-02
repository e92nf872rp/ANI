import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/input-tokens — 平台 Input Tokens 专页。
 *
 * 固定 resource_type=token_input，不提供指标视角切换。
 * 面包屑：平台计量与结算 / 平台 Input Tokens
 */
export const Route = createFileRoute('/_authenticated/metering/input-tokens')({
  component: () => <PlatformMetricPage route="/metering/input-tokens" />,
})
