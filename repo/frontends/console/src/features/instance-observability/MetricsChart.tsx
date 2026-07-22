/**
 * 实例可观测性 · 指标 Tab · PromQL 时序图子组件。
 *
 * 对齐：
 * - PRD US-011 指标 Tab（PromQL 时序图）、FR-7/FR-8
 * - UX §4.4 Row3/Row4 图表区、§5.4 Radio.Group + ReactECharts、§6.3 图表状态、§7.2 Copy
 * - SPEC §4.1.4 PromQL 时序 API 调用契约、§5.2 PromQL 模板注入方案、§8.2 缓存策略
 *
 * 行为：
 * - 时间范围 Radio.Group：15m / 1h / 6h / 24h（默认 1h）
 * - 通过 `queryRangeObservability`（`GET /observability/query_range`）拉取 PromQL 区间查询结果（matrix）
 * - PromQL 来自 `promqlTemplates` 冻结模板，按 `instance_id` 注入；不硬编码未文档化 label
 * - 至少 2 条曲线：CPU 利用率、内存使用率；gpu_container 额外 GPU 利用率、显存使用率
 * - 图表高度 ≥ 280px，使用 `echarts-for-react`
 * - PromQL 失败/无数据：Empty「所选时间范围暂无数据」或 Alert error，不伪造曲线
 * - 403：Alert theme="warning"「无权限查看趋势数据」
 * - 不展示 Prometheus 地址
 * - 时间标注独立：`趋势数据查询于 {queriedAt}`
 */
import { useMemo } from 'react'
import { useQueries } from '@tanstack/react-query'
import { Alert, Button, Empty, Loading, Radio } from 'tdesign-react'
import ReactECharts from 'echarts-for-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'
import {
  PROMQL_TEMPLATE_LABELS,
  getTemplatesForKind,
  renderPromQL,
  type PromQLTemplateId,
} from './promqlTemplates'

/** PromQL 区间查询响应类型（来自 Core OpenAPI `ObservabilityRangeQueryResponse`）。 */
type ObservabilityRangeQueryResponse = components['schemas']['ObservabilityRangeQueryResponse']

/** 时间范围选项。对齐 PRD US-011 AC / UX §4.4。 */
export type MetricsTimeRange = '15m' | '1h' | '6h' | '24h'

/** 时间范围选项配置。 */
const RANGE_OPTIONS: Array<{ label: string; value: MetricsTimeRange }> = [
  { label: '15m', value: '15m' },
  { label: '1h', value: '1h' },
  { label: '6h', value: '6h' },
  { label: '24h', value: '24h' },
]

/** 默认时间范围。对齐 PRD US-011 AC / UX §4.4。 */
const DEFAULT_RANGE: MetricsTimeRange = '1h'

/** 时间范围 → 秒数映射，用于计算 start/end 与 step。 */
const RANGE_SECONDS: Record<MetricsTimeRange, number> = {
  '15m': 15 * 60,
  '1h': 60 * 60,
  '6h': 6 * 60 * 60,
  '24h': 24 * 60 * 60,
}

/** 时间范围 → 采样步长映射。短范围用小 step（曲线更细），长范围用大 step（避免点过多）。 */
const RANGE_STEP: Record<MetricsTimeRange, string> = {
  '15m': '15s',
  '1h': '30s',
  '6h': '2m',
  '24h': '5m',
}

/** Core API 错误响应结构。openapi-fetch 的 error 是解析后的响应 body（ErrorResponse），
 * 不含 HTTP status；403 通过 `code === 'FORBIDDEN'` 判断。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/** 图表最小高度（px）。对齐 issue AC「图表高度 ≥ 280px」。 */
const CHART_HEIGHT = 320

/** ECharts 系列配色（与 TDesign token 对齐的简洁色板）。
 * VM 模板复用 container 的蓝/绿，语义一致（SPEC §5.9 / UX §8.4 假设）。 */
const SERIES_COLORS: Record<PromQLTemplateId, string> = {
  instance_cpu_utilization: '#0052D9',
  instance_memory_utilization: '#2BA471',
  instance_gpu_utilization: '#D54941',
  instance_gpu_memory_utilization: '#E37318',
  instance_vm_cpu_utilization: '#0052D9',
  instance_vm_memory_utilization: '#2BA471',
}

/** 把时间范围转换为 [start, end, step] 参数。end=当前时间，start=end-range。 */
function buildRangeParams(range: MetricsTimeRange): { start: string; end: string; step: string } {
  const now = Date.now()
  const seconds = RANGE_SECONDS[range]
  const end = new Date(now)
  const start = new Date(now - seconds * 1000)
  return {
    start: start.toISOString(),
    end: end.toISOString(),
    step: RANGE_STEP[range],
  }
}

/**
 * 时序图子组件。
 *
 * 使用 `useQueries` 并发查询当前 kind 的所有模板曲线：
 * - 每条曲线一个独立 query，key 含 `[templateId, instanceId, range]`
 * - 切换 range 触发 refetch
 * - 单条曲线失败不影响其他曲线；全部失败显示错误态
 * - 403 单独处理为「无权限查看趋势数据」
 */
export function MetricsChart({
  range,
  onRangeChange,
  queriedAt,
}: {
  range: MetricsTimeRange
  onRangeChange: (r: MetricsTimeRange) => void
  queriedAt: string
}) {
  const { instance, kind } = useInstanceContext()
  const instanceId = instance.id

  const templateIds = useMemo(() => getTemplatesForKind(kind), [kind])
  const rangeParams = useMemo(() => buildRangeParams(range), [range])

  const queries = useQueries({
    queries: templateIds.map((templateId) => ({
      queryKey: ['observability-query-range', templateId, instanceId, range],
      queryFn: async () => {
        const rendered = renderPromQL(templateId, instanceId)
        const { data, error } = await coreApi.GET('/observability/query_range', {
          params: {
            query: {
              query: rendered,
              start: rangeParams.start,
              end: rangeParams.end,
              step: rangeParams.step,
              timeout: '30s',
            },
          },
        })
        if (error) throw error
        return data as ObservabilityRangeQueryResponse
      },
      // SPEC §8.2：PromQL 查询缓存，key 含 [templateId, instanceId, range]
      staleTime: 30_000,
    })),
  })

  const isLoading = queries.some((q) => q.isLoading)
  const allError = queries.every((q) => q.isError)
  const hasForbidden = queries.some(
    (q) => q.isError && (q.error as CoreApiError)?.code === 'FORBIDDEN',
  )
  const hasAnyData = queries.some(
    (q) => q.data && q.data.results.length > 0 && q.data.results.some((r) => r.values.length > 0),
  )

  // 403：无权限查看趋势数据（UX §6.3、§7.2）
  if (hasForbidden && !hasAnyData) {
    return (
      <div>
        <ChartToolbar range={range} onRangeChange={onRangeChange} queriedAt={queriedAt} />
        <Alert
          theme="warning"
          message="无权限查看趋势数据"
          data-testid="metrics-chart-forbidden"
        />
      </div>
    )
  }

  // 全部失败且非 403：错误态
  if (allError && !hasAnyData) {
    const firstError = queries.find((q) => q.isError)?.error as CoreApiError
    const message = firstError?.message ?? '加载趋势数据失败'
    const requestId = firstError?.request_id
    return (
      <div>
        <ChartToolbar range={range} onRangeChange={onRangeChange} queriedAt={queriedAt} />
        <Alert
          theme="error"
          title="加载趋势数据失败"
          data-testid="metrics-chart-error"
          message={
            <span>
              {message}
              {requestId ? `（请求 ID：${requestId}）` : ''}
            </span>
          }
          operation={
            <Button
              theme="primary"
              variant="outline"
              size="small"
              onClick={() => queries.forEach((q) => q.refetch())}
            >
              重试
            </Button>
          }
        />
      </div>
    )
  }

  // loading 态
  if (isLoading && !hasAnyData) {
    return (
      <div>
        <ChartToolbar range={range} onRangeChange={onRangeChange} queriedAt={queriedAt} />
        <div style={{ height: CHART_HEIGHT, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <Loading text="加载趋势数据中…" />
        </div>
      </div>
    )
  }

  // 无数据：Empty「所选时间范围暂无数据」（UX §6.3、§7.2）
  if (!hasAnyData) {
    return (
      <div>
        <ChartToolbar range={range} onRangeChange={onRangeChange} queriedAt={queriedAt} />
        <Empty
          description="所选时间范围暂无数据"
          data-testid="metrics-chart-empty"
        />
      </div>
    )
  }

  // 构建 ECharts option：每个模板一条折线 series
  const option = buildChartOption(
    queries.map((q) => q.data),
    templateIds,
    rangeParams.start,
    rangeParams.end,
  )

  return (
    <div>
      <ChartToolbar range={range} onRangeChange={onRangeChange} queriedAt={queriedAt} />
      <ReactECharts
        option={option}
        style={{ height: CHART_HEIGHT, width: '100%' }}
        notMerge
        lazyUpdate
        data-testid="metrics-chart-idle"
      />
    </div>
  )
}

/** 构建 ECharts option：每个模板一条折线 series。
 * Range query 返回 matrix，每个 series 含 values（时间序列采样点数组）。 */
function buildChartOption(
  queryData: (ObservabilityRangeQueryResponse | undefined)[],
  templateIds: PromQLTemplateId[],
  start: string,
  end: string,
): Record<string, unknown> {
  const series = templateIds.map((templateId, idx) => {
    const data = queryData[idx]
    // range query 每条 series 的 values 是 [timestamp, value] 数组
    const points: Array<[string, number]> = []
    for (const seriesResult of data?.results ?? []) {
      for (const point of seriesResult.values) {
        points.push([point.timestamp, point.value])
      }
    }
    return {
      name: PROMQL_TEMPLATE_LABELS[templateId],
      type: 'line' as const,
      data: points,
      itemStyle: { color: SERIES_COLORS[templateId] },
      lineStyle: { color: SERIES_COLORS[templateId], width: 2 },
      smooth: false,
    }
  })

  return {
    tooltip: {
      trigger: 'axis',
    },
    legend: {
      data: templateIds.map((id) => PROMQL_TEMPLATE_LABELS[id]),
      top: 0,
    },
    grid: {
      left: '3%',
      right: '4%',
      bottom: '3%',
      top: 40,
      containLabel: true,
    },
    xAxis: {
      type: 'time',
      boundaryGap: false,
      // 显式设置横轴范围为请求的 start/end，使不同时间范围切换时横轴明显不同。
      // 否则 ECharts 默认按数据点范围缩放，数据不足时 1h/6h/24h 横轴看起来一样。
      min: new Date(start).getTime(),
      max: new Date(end).getTime(),
    },
    yAxis: {
      type: 'value',
      name: '利用率（%）',
      min: 0,
      max: 100,
    },
    series,
  }
}

/** 图表区工具条：时间范围 Radio.Group + 查询时间标注。 */
function ChartToolbar({
  range,
  onRangeChange,
  queriedAt,
}: {
  range: MetricsTimeRange
  onRangeChange: (r: MetricsTimeRange) => void
  queriedAt: string
}) {
  return (
    <div
      style={{
        marginBottom: 12,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        flexWrap: 'wrap',
        gap: 12,
      }}
    >
      <Radio.Group
        value={range}
        onChange={(value) => onRangeChange(value as MetricsTimeRange)}
        variant="default-filled"
      >
        {RANGE_OPTIONS.map((opt) => (
          <Radio key={opt.value} value={opt.value}>
            {opt.label}
          </Radio>
        ))}
      </Radio.Group>
      <span style={{ color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>
        趋势数据查询于 {queriedAt}
      </span>
    </div>
  )
}

/** 默认时间范围常量导出，供父组件初始化使用。 */
export { DEFAULT_RANGE }
