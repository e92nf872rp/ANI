/**
 * 实例可观测性 · 指标 Tab · 快照卡片子组件。
 *
 * 对齐：
 * - PRD US-010 指标 Tab（快照卡片）
 * - UX §4.4 Row2 快照区、§5.4 指标 Tab 组件映射、§6.3 指标 Tab 状态、§7.2 Copy
 * - SPEC §4.1.3 指标快照 API 调用契约、§5.7 边缘场景（字段 null 显示「暂不可用」）
 *
 * 行为：
 * - 调用 `getInstanceMetrics`（`GET /instances/{instance_id}/metrics`）
 * - 展示 `timestamp` 与「快照时间」标注
 * - 卡片：CPU %、内存 used/total、网络 RX/TX
 * - `kind=gpu_container` 额外 GPU 利用率、显存 used/total 卡片
 * - null 字段显示「暂不可用」（不显示 0）
 * - 错误态：Alert theme="error" + 重试；不阻塞图表区
 * - loading 态：Skeleton
 */
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Row, Skeleton, Space, Tag } from 'tdesign-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** 实例指标快照类型（来自 Core OpenAPI `InstanceMetrics`）。 */
type InstanceMetrics = components['schemas']['InstanceMetrics']

/** Core API 错误响应结构。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/** 「暂不可用」固定文案。对齐 UX §7.2。 */
const UNAVAILABLE = '暂不可用'

/** ISO 时间戳格式化为本地可读字符串。 */
function formatTimestamp(ts: string): string {
  try {
    const d = new Date(ts)
    if (Number.isNaN(d.getTime())) return ts
    return d.toLocaleString()
  } catch {
    return ts
  }
}

/** 字节数格式化为人类可读（KB/MB/GB）。 */
function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

/** 数值或 null → 展示文本；null 时显示「暂不可用」。 */
function valueOrUnavailable(
  value: number | null | undefined,
  formatter: (v: number) => string = (v) => String(v),
): string {
  if (value == null) return UNAVAILABLE
  return formatter(value)
}

/**
 * 快照卡片子组件。
 *
 * 通过 React Query 调用 `getInstanceMetrics`，按 kind 渲染卡片网格：
 * - 通用卡片：CPU 利用率、内存 used/total、网络 RX/TX
 * - gpu_container 额外：GPU 利用率、显存 used/total
 * - null 字段展示「暂不可用」（不显示 0）
 */
export function MetricsSnapshot() {
  const { instance, kind } = useInstanceContext()
  const instanceId = instance.id

  const {
    data: metrics,
    isLoading,
    isError,
    error,
    refetch,
    dataUpdatedAt,
  } = useQuery<InstanceMetrics>({
    queryKey: ['instance-metrics', instanceId],
    queryFn: async () => {
      const { data, error } = await coreApi.GET('/instances/{instance_id}/metrics', {
        params: { path: { instance_id: instanceId } },
      })
      if (error) throw error
      return data as InstanceMetrics
    },
    // SPEC §8.2：指标快照缓存 30s
    staleTime: 30_000,
  })

  if (isLoading) {
    return (
      <div data-testid="metrics-snapshot-loading">
        <Skeleton animation="gradient" />
      </div>
    )
  }

  if (isError) {
    const err = error as CoreApiError
    const message = err?.message ?? '无法加载指标快照，请稍后重试'
    const requestId = err?.request_id
    return (
      <Alert
        theme="error"
        title="无法加载指标快照"
        data-testid="metrics-snapshot-error"
        message={
          <span>
            {message}
            {requestId ? `（请求 ID：${requestId}）` : ''}
          </span>
        }
        operation={
          <Button theme="primary" variant="outline" size="small" onClick={() => refetch()}>
            重试
          </Button>
        }
      />
    )
  }

  const isGpu = kind === 'gpu_container'

  return (
    <div data-testid="metrics-snapshot-idle">
      {/* 快照时间标注：UX §4.4「快照时间：{metrics.timestamp}」 */}
      <div style={{ marginBottom: 12, color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>
        快照时间：{metrics ? formatTimestamp(metrics.timestamp) : '—'}
        {dataUpdatedAt ? `（UI 刷新于 ${formatTimestamp(new Date(dataUpdatedAt).toISOString())}）` : ''}
      </div>

      <Row gutter={[16, 16]}>
        {/* CPU 利用率 */}
        <Col xs={12} sm={6} md={4}>
          <Card title="CPU 利用率" bordered>
            <StatisticSnapshot
              value={metrics?.cpu_utilization_pct}
              formatter={(v) => `${v.toFixed(1)} %`}
            />
          </Card>
        </Col>

        {/* 内存 used / total */}
        <Col xs={12} sm={6} md={4}>
          <Card title="内存使用" bordered>
            <MemorySnapshot
              used={metrics?.memory_used_mb}
              total={metrics?.memory_total_mb}
            />
          </Card>
        </Col>

        {/* 网络 RX */}
        <Col xs={12} sm={6} md={4}>
          <Card title="网络接收（RX）" bordered>
            <StatisticSnapshot
              value={metrics?.network_rx_bytes}
              formatter={(v) => formatBytes(v)}
            />
          </Card>
        </Col>

        {/* 网络 TX */}
        <Col xs={12} sm={6} md={4}>
          <Card title="网络发送（TX）" bordered>
            <StatisticSnapshot
              value={metrics?.network_tx_bytes}
              formatter={(v) => formatBytes(v)}
            />
          </Card>
        </Col>

        {/* GPU 卡片：仅 gpu_container */}
        {isGpu ? (
          <>
            <Col xs={12} sm={6} md={4}>
              <Card title="GPU 利用率" bordered>
                <StatisticSnapshot
                  value={metrics?.gpu_utilization_pct}
                  formatter={(v) => `${v.toFixed(1)} %`}
                />
              </Card>
            </Col>
            <Col xs={12} sm={6} md={4}>
              <Card title="GPU 显存" bordered>
                <MemorySnapshot
                  used={metrics?.gpu_memory_used_mb}
                  total={metrics?.gpu_memory_total_mb}
                />
              </Card>
            </Col>
          </>
        ) : null}
      </Row>
    </div>
  )
}

/** 单值卡片内容：null → 「暂不可用」。 */
function StatisticSnapshot({
  value,
  formatter,
}: {
  value: number | null | undefined
  formatter: (v: number) => string
}) {
  if (value == null) {
    return (
      <div style={{ color: 'var(--td-text-color-placeholder)' }}>
        <Tag theme="warning" variant="light">
          {UNAVAILABLE}
        </Tag>
      </div>
    )
  }
  return <div style={{ fontSize: 22, fontWeight: 600 }}>{formatter(value)}</div>
}

/** 内存 used/total 双值卡片：任一为 null 显示「暂不可用」。 */
function MemorySnapshot({
  used,
  total,
}: {
  used: number | null | undefined
  total: number | null | undefined
}) {
  if (used == null && total == null) {
    return (
      <div style={{ color: 'var(--td-text-color-placeholder)' }}>
        <Tag theme="warning" variant="light">
          {UNAVAILABLE}
        </Tag>
      </div>
    )
  }
  const usedText = valueOrUnavailable(used, (v) => `${v.toFixed(1)} MB`)
  const totalText = valueOrUnavailable(total, (v) => `${v.toFixed(1)} MB`)
  return (
    <Space direction="vertical" size={2}>
      <div>
        <span style={{ color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>已用：</span>
        <span style={{ fontSize: 18, fontWeight: 600 }}>{usedText}</span>
      </div>
      <div>
        <span style={{ color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>总量：</span>
        <span style={{ fontSize: 14 }}>{totalText}</span>
      </div>
    </Space>
  )
}
