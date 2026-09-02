import { createFileRoute } from '@tanstack/react-router'
import { PlatformMetricPage } from '@/features/platform-metering/PlatformMetricPage'

/**
 * /metering/kb-queries — 平台 KB Queries 专页（P1 占位）。
 *
 * 该指标 resource_type=kb_query_count 在 Core OpenAPI `MeteringUsageRecord.resource_type`
 * 枚举中尚未冻结（PRD NG-7、§9-Q5、UX §6.2、SPEC §5.3）：
 * - 路由可进入
 * - 内容区显示 api-not-ready Empty「该指标待 API 合入（P1）」
 * - 不伪造数据（FR-12、NG-7）
 *
 * 面包屑：平台计量与结算 / 平台 KB Queries
 */
export const Route = createFileRoute('/_authenticated/metering/kb-queries')({
  component: () => <PlatformMetricPage route="/metering/kb-queries" />,
})
