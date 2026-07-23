/**
 * 实例可观测性 · 安全事件 Tab（仅 sandbox kind）。
 *
 * 对齐：
 * - PRD US-014 Sandbox 安全事件 Tab（severity 筛选、空态、错误态）
 * - UX §4.7 安全事件 Tab 布局、§5.6 组件映射、§6.6 状态、§7.1/7.2 Copy
 * - SPEC §4.1.7 安全事件 API 调用契约、§5.7 边缘场景、§6.1 错误处理、§9.1 测试矩阵
 *
 * 行为：
 * - 调用 `listInstanceSecurityEvents`（`GET /instances/{instance_id}/security-events`），默认 `limit=100`
 * - severity 筛选 `Select`：全部 / info / warning / critical → query `severity`
 * - 展示列：occurred_at、severity Tag、event_type、description
 * - severity Tag theme：critical→danger, warning→warning, info→primary
 * - 空事件：`Empty description="暂无安全事件"`
 * - API 失败：`Alert theme="error"` + message + request_id + 重试按钮
 * - loading 态：`Table loading`
 *
 * 契约边界说明：
 * Core OpenAPI（`v1.yaml`）`listInstanceSecurityEvents` 的 query 参数当前仅有 `severity` 与 `limit`，
 * 未声明 `cursor` 入参。本 UI 批次遵守 OpenAPI 唯一真实来源，不发明 API 字段，故 query 不传 `cursor`，
 * 暂不实现 cursor「加载更多」分页；待后续 Core 批次补齐 `cursor` query 参数后再启用。
 * 安全事件列表一次性加载 `limit=100` 条。
 *
 * 可见性：
 * 本 Tab 仅在 `kind=sandbox` 实例渲染；其余 kind 不出现此 Tab。
 * Tab 可见性由 `observabilityTabsConfig.ts` 与 `route.tsx` 壳层控制，本组件不重复校验。
 */
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Empty, Select, Space, Table, Tag } from 'tdesign-react'
import type { BaseTableCol, TableRowData } from 'tdesign-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** 安全事件条目类型（来自 Core OpenAPI `InstanceSecurityEvent`）。 */
type InstanceSecurityEvent = components['schemas']['InstanceSecurityEvent']

/** 安全事件列表响应类型（来自 Core OpenAPI `InstanceSecurityEventListResponse`）。 */
type InstanceSecurityEventListResponse =
  components['schemas']['InstanceSecurityEventListResponse']

/** 安全事件 severity（来自 OpenAPI `InstanceSecurityEvent.severity`）。 */
type Severity = 'info' | 'warning' | 'critical'

/** severity 筛选选项（含「全部」）。对齐 issue AC：全部 / info / warning / critical。 */
const SEVERITY_OPTIONS: Array<{ label: string; value: Severity | '' }> = [
  { label: '全部', value: '' },
  { label: 'info', value: 'info' },
  { label: 'warning', value: 'warning' },
  { label: 'critical', value: 'critical' },
]

/** 默认每页条数。对齐 issue AC / SPEC §4.1.7（OpenAPI 默认 50，UI 取 100 以对齐其它 Tab）。 */
const DEFAULT_LIMIT = 100

/** Core API 错误响应结构（来自 OpenAPI `ErrorResponse`）。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/**
 * severity → Tag theme 映射。
 * 对齐 UX §5.6、issue AC：critical→danger, warning→warning, info→primary。
 */
function severityTheme(severity: Severity): 'danger' | 'warning' | 'primary' {
  switch (severity) {
    case 'critical':
      return 'danger'
    case 'warning':
      return 'warning'
    case 'info':
      return 'primary'
  }
}

/** ISO 时间戳格式化为本地可读字符串。 */
function formatTimestamp(ts: string): string {
  // 保留 ISO 原值作为 tooltip，展示本地时间
  try {
    const d = new Date(ts)
    if (Number.isNaN(d.getTime())) return ts
    return d.toLocaleString()
  } catch {
    return ts
  }
}

/** 表格列定义。对齐 UX §4.7 / SPEC §4.1.7 / issue AC。 */
const COLUMNS: BaseTableCol<TableRowData>[] = [
  {
    title: '发生时间',
    colKey: 'occurred_at',
    width: 200,
    cell: ({ row }) => formatTimestamp((row as InstanceSecurityEvent).occurred_at),
  },
  {
    title: '严重程度',
    colKey: 'severity',
    width: 120,
    cell: ({ row }) => {
      const eventRow = row as InstanceSecurityEvent
      return <Tag theme={severityTheme(eventRow.severity)}>{eventRow.severity}</Tag>
    },
  },
  {
    title: '事件类型',
    colKey: 'event_type',
    width: 200,
    cell: ({ row }) => (row as InstanceSecurityEvent).event_type,
  },
  {
    title: '描述',
    colKey: 'description',
    // description 列允许换行展示，超长以 tooltip 辅助
    cell: ({ row }) => {
      const eventRow = row as InstanceSecurityEvent
      const desc = eventRow.description
      if (desc == null || desc === '') return '-'
      return (
        <div title={desc} style={{ maxWidth: 480, wordBreak: 'break-word' }}>
          {desc}
        </div>
      )
    },
  },
]

/**
 * 安全事件 Tab 组件。
 *
 * 使用 `useQuery` 加载安全事件列表：
 * - 首屏加载 `limit=100`
 * - severity 筛选变更时重置查询
 * - 当前未启用 cursor 分页（见文件头契约边界说明）
 */
export function SecurityEventsTab() {
  const { instance } = useInstanceContext()
  const instanceId = instance.id

  const [severityFilter, setSeverityFilter] = useState<Severity | ''>('')

  const {
    data,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['instance-security-events', instanceId, severityFilter],
    queryFn: async () => {
      const { data, error } = await coreApi.GET(
        '/instances/{instance_id}/security-events',
        {
          params: {
            path: { instance_id: instanceId },
            query: {
              severity: severityFilter || undefined,
              limit: DEFAULT_LIMIT,
            },
          },
        },
      )
      if (error) throw error
      return data as InstanceSecurityEventListResponse
    },
  })

  // 合成行键，用于 Table rowKey
  const events: TableRowData[] =
    (data?.items ?? []).map((item, index) => ({
      ...item,
      __rowKey: `${index}-${item.id}`,
    }))

  // 错误态：Alert theme="error" + message + request_id + 重试按钮
  if (isError) {
    const err = error as CoreApiError
    const message = err?.message ?? '加载安全事件失败'
    const requestId = err?.request_id
    return (
      <Alert
        theme="error"
        title="加载安全事件失败"
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

  // 空态：Empty description="暂无安全事件"
  if (!isLoading && events.length === 0) {
    return (
      <div>
        <SecurityEventsToolbar
          severityFilter={severityFilter}
          onSeverityChange={setSeverityFilter}
          onRefresh={() => refetch()}
        />
        <Empty description="暂无安全事件" />
      </div>
    )
  }

  return (
    <div>
      <SecurityEventsToolbar
        severityFilter={severityFilter}
        onSeverityChange={setSeverityFilter}
        onRefresh={() => refetch()}
        refreshing={isLoading}
      />
      <Table
        loading={isLoading}
        data={events}
        columns={COLUMNS}
        rowKey="__rowKey"
        size="small"
        bordered
      />
    </div>
  )
}

/** 安全事件 Tab 工具条：severity 筛选 + 刷新按钮。对齐 UX §4.7。 */
function SecurityEventsToolbar({
  severityFilter,
  onSeverityChange,
  onRefresh,
  refreshing,
}: {
  severityFilter: Severity | ''
  onSeverityChange: (value: Severity | '') => void
  onRefresh: () => void
  refreshing?: boolean
}) {
  return (
    <div style={{ marginBottom: 12 }}>
      <Space>
        <Select
          value={severityFilter}
          options={SEVERITY_OPTIONS}
          onChange={(value) => onSeverityChange(value as Severity | '')}
          style={{ width: 160 }}
          placeholder="严重程度筛选"
        />
        <Button variant="outline" loading={refreshing} onClick={onRefresh}>
          刷新
        </Button>
      </Space>
    </div>
  )
}
