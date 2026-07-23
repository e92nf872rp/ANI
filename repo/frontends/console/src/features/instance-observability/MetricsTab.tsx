/**
 * 实例可观测性 · 指标 Tab（双通道：快照卡片 + PromQL 时序折线图）。
 *
 * 对齐：
 * - PRD US-010 指标 Tab（快照卡片）、US-011 指标 Tab（PromQL 时序图）、US-015 差异化指标
 * - UX §4.4 指标 Tab 双通道布局、§6.3 指标 Tab 状态、§7.1/7.2 Copy
 * - SPEC §4.1.3 指标快照 API 调用契约、§4.1.4 PromQL 时序 API 调用契约、
 *   §5.2 PromQL 模板注入方案、§8.2 缓存策略、§9.4(US-010,011) 测试矩阵
 *
 * 双通道布局：
 * - Row1 工具条：快照更新时间 | 刷新按钮 | 30s 自动刷新 Switch（默认开）
 * - Row2 快照区：MetricsSnapshot（CPU/内存/网络 + GPU 卡片）
 * - Row3 图表工具条：Radio.Group 时间范围（15m/1h/6h/24h，默认 1h）+ 趋势查询时间
 * - Row4 ECharts 折线图：MetricsChart
 *
 * 边界：
 * - 快照与图表时间标注独立（`快照时间` / `趋势数据查询于`）
 * - 不展示 Prometheus 地址
 * - PromQL 失败/无数据：图表区展示 Empty/Alert，不伪造曲线
 * - 无 observability 读权限：图表区 Alert theme="warning"
 * - null 字段显示「暂不可用」，不显示 0
 * - 快照与图表独立 refetch，互不阻塞
 */
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Space, Switch } from 'tdesign-react'
import { useInstanceContext } from './InstanceContext'
import { MetricsSnapshot } from './MetricsSnapshot'
import {
  MetricsChart,
  DEFAULT_RANGE,
  type MetricsTimeRange,
} from './MetricsChart'

/** 自动刷新间隔（毫秒）。对齐 issue AC「30s 自动刷新」。 */
const AUTO_REFRESH_INTERVAL_MS = 30_000

/**
 * 指标 Tab 组件。
 *
 * 管理：
 * - 时间范围状态（Radio.Group）
 * - 自动刷新开关（Switch，默认开）
 * - 趋势数据查询时间标注（每次图表查询完成更新）
 *
 * 快照区与图表区各自管理 React Query 状态，互不阻塞。
 */
export function MetricsTab() {
  const { instance } = useInstanceContext()
  const instanceId = instance.id
  const queryClient = useQueryClient()

  const [range, setRange] = useState<MetricsTimeRange>(DEFAULT_RANGE)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [queriedAt, setQueriedAt] = useState<string>('—')
  const refreshTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  /** 使快照与图表的 React Query 失效，触发 refetch。 */
  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['instance-metrics'] })
    queryClient.invalidateQueries({ queryKey: ['observability-query-range'] })
    setQueriedAt(new Date().toLocaleString())
  }

  // 自动刷新：每 30s 触发快照 + 图表 refetch。
  // 图表区随 range 变化自动 refetch；auto-refresh 时通过 invalidateQueries 触发。
  useEffect(() => {
    if (!autoRefresh) {
      if (refreshTimerRef.current) {
        clearInterval(refreshTimerRef.current)
        refreshTimerRef.current = null
      }
      return
    }
    // 标记查询时间
    setQueriedAt(new Date().toLocaleString())
    refreshTimerRef.current = setInterval(invalidateAll, AUTO_REFRESH_INTERVAL_MS)
    return () => {
      if (refreshTimerRef.current) {
        clearInterval(refreshTimerRef.current)
        refreshTimerRef.current = null
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoRefresh, instanceId, range])

  const handleManualRefresh = () => {
    invalidateAll()
  }

  return (
    <div data-testid="metrics-tab">
      {/* Row1 工具条：快照更新时间 | 刷新 | 30s 自动刷新 */}
      <div
        style={{
          marginBottom: 16,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 12,
        }}
      >
        <span style={{ color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>
          指标快照与趋势数据双通道
        </span>
        <Space>
          <Button variant="outline" onClick={handleManualRefresh}>
            刷新
          </Button>
          <Switch
            value={autoRefresh}
            onChange={(val) => setAutoRefresh(val as boolean)}
            label={['30s 自动刷新', '30s 自动刷新']}
          />
        </Space>
      </div>

      {/* Row2 快照区 */}
      <MetricsSnapshot />

      {/* Row3/Row4 图表区 */}
      <div style={{ marginTop: 24 }}>
        <MetricsChart
          range={range}
          onRangeChange={setRange}
          queriedAt={queriedAt}
        />
      </div>
    </div>
  )
}
