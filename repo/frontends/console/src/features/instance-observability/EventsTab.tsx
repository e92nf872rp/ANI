/**
 * 实例可观测性 · 事件 Tab。
 *
 * 对齐：
 * - PRD US-009 事件 Tab（cursor 分页、空态、错误态）
 * - UX §4.3 事件 Tab 布局、§5.3 组件映射、§6.2 状态、§7.1/7.2 Copy
 * - SPEC §4.1.2 事件 API 调用契约、§5.7 边缘场景、§6.1 错误处理、§9.1 测试矩阵
 *
 * 行为：
 * - 调用 `listInstanceEvents`（`GET /instances/{instance_id}/events`），默认 `limit=100`
 * - 展示列：occurred_at、type Tag、reason、message、count
 * - `type=Warning` → `Tag theme="warning"`；`Normal` → `Tag theme="default"`
 * - 空事件：`Empty description="暂无事件"`
 * - API 失败：`Alert theme="error"` + message + request_id + 重试按钮
 * - loading 态：`Table loading`
 *
 * 契约边界说明：
 * Core OpenAPI（`v1.yaml`）`listInstanceEvents` 的 query 参数当前仅有 `limit` 与 `type`，
 * 未声明 `cursor` 入参（对比 `listInstanceLogs` 已声明 cursor）。
 * 本 UI 批次遵守 OpenAPI 唯一真实来源，不发明 API 字段，故 query 不传 `cursor`，
 * 暂不实现 cursor「加载更多」分页；待后续 Core 批次补齐 `cursor` query 参数后再启用。
 * 事件列表一次性加载 `limit=100` 条。
 */
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Empty, Select, Space, Table, Tag } from 'tdesign-react'
import type { BaseTableCol, TableRowData } from 'tdesign-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** 事件条目类型（来自 Core OpenAPI `InstanceEvent`）。 */
type InstanceEvent = components['schemas']['InstanceEvent']

/** 事件列表响应类型（来自 Core OpenAPI `InstanceEventListResponse`）。 */
type InstanceEventListResponse = components['schemas']['InstanceEventListResponse']

/** 事件类型（来自 OpenAPI `InstanceEvent.type`）。 */
type EventType = 'Normal' | 'Warning'

/** 类型筛选选项（含「全部」）。 */
const TYPE_OPTIONS: Array<{ label: string; value: EventType | '' }> = [
  { label: '全部', value: '' },
  { label: 'Normal', value: 'Normal' },
  { label: 'Warning', value: 'Warning' },
]

/** 默认每页条数。对齐 PRD US-009 / SPEC §4.1.2。 */
const DEFAULT_LIMIT = 100

/** Core API 错误响应结构（来自 OpenAPI `ErrorResponse`）。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/**
 * 事件类型 → Tag theme 映射。
 * 对齐 UX §4.3、issue AC：Warning→warning，Normal→default。
 */
function typeTheme(type: EventType): 'warning' | 'default' {
  return type === 'Warning' ? 'warning' : 'default'
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

/** 表格列定义。对齐 UX §4.3 / SPEC §4.1.2 / issue AC。 */
const COLUMNS: BaseTableCol<TableRowData>[] = [
  {
    title: '发生时间',
    colKey: 'occurred_at',
    width: 200,
    cell: ({ row }) => formatTimestamp((row as InstanceEvent).occurred_at),
  },
  {
    title: '类型',
    colKey: 'type',
    width: 100,
    cell: ({ row }) => {
      const eventRow = row as InstanceEvent
      return <Tag theme={typeTheme(eventRow.type)}>{eventRow.type}</Tag>
    },
  },
  {
    title: '原因',
    colKey: 'reason',
    width: 180,
    cell: ({ row }) => (row as InstanceEvent).reason ?? '-',
  },
  {
    title: '消息',
    colKey: 'message',
    // message 列 monospace，超长 ellipsis + tooltip
    cell: ({ row }) => {
      const eventRow = row as InstanceEvent
      return (
        <div
          title={eventRow.message}
          style={{
            fontFamily: 'var(--td-font-family-mono, monospace)',
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            maxWidth: 480,
          }}
        >
          {eventRow.message}
        </div>
      )
    },
  },
  {
    title: '次数',
    colKey: 'count',
    width: 100,
    cell: ({ row }) => {
      const eventRow = row as InstanceEvent
      // count 为可选字段，缺失时展示 '-'
      return eventRow.count != null ? String(eventRow.count) : '-'
    },
  },
]

/**
 * 事件 Tab 组件。
 *
 * 使用 `useQuery` 加载事件列表：
 * - 首屏加载 `limit=100`
 * - 类型筛选变更时重置查询
 * - 当前未启用 cursor 分页（见文件头契约边界说明）
 */
export function EventsTab() {
  const { instance } = useInstanceContext()
  const instanceId = instance.id

  const [typeFilter, setTypeFilter] = useState<EventType | ''>('')

  const {
    data,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['instance-events', instanceId, typeFilter],
    queryFn: async () => {
      const { data, error } = await coreApi.GET('/instances/{instance_id}/events', {
        params: {
          path: { instance_id: instanceId },
          query: {
            limit: DEFAULT_LIMIT,
            type: typeFilter || undefined,
          },
        },
      })
      if (error) throw error
      return data as InstanceEventListResponse
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
    const message = err?.message ?? '加载事件失败'
    const requestId = err?.request_id
    return (
      <Alert
        theme="error"
        title="加载事件失败"
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

  // 空态：Empty description="暂无事件"
  if (!isLoading && events.length === 0) {
    return (
      <div>
        <EventsToolbar
          typeFilter={typeFilter}
          onTypeChange={setTypeFilter}
          onRefresh={() => refetch()}
        />
        <Empty description="暂无事件" />
      </div>
    )
  }

  return (
    <div>
      <EventsToolbar
        typeFilter={typeFilter}
        onTypeChange={setTypeFilter}
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

/** 事件 Tab 工具条：类型筛选 + 刷新按钮。对齐 UX §4.3。 */
function EventsToolbar({
  typeFilter,
  onTypeChange,
  onRefresh,
  refreshing,
}: {
  typeFilter: EventType | ''
  onTypeChange: (value: EventType | '') => void
  onRefresh: () => void
  refreshing?: boolean
}) {
  return (
    <div style={{ marginBottom: 12 }}>
      <Space>
        <Select
          value={typeFilter}
          options={TYPE_OPTIONS}
          onChange={(value) => onTypeChange(value as EventType | '')}
          style={{ width: 160 }}
          placeholder="类型筛选"
        />
        <Button variant="outline" loading={refreshing} onClick={onRefresh}>
          刷新
        </Button>
      </Space>
    </div>
  )
}
