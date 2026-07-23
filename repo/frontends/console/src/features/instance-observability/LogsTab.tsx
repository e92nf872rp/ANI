/**
 * 实例可观测性 · 日志 Tab。
 *
 * 对齐：
 * - PRD US-008 日志 Tab（cursor 分页、级别筛选、空态、错误态）
 * - UX §4.2 日志 Tab 布局、§5.2 组件映射、§6.1 状态、§7.1/7.2 Copy
 * - SPEC §4.1.1 日志 API 调用契约、§5.7 边缘场景、§6.1 错误处理、§9.1 测试矩阵
 *
 * 行为：
 * - 调用 `listInstanceLogs`（`GET /instances/{instance_id}/logs`），默认 `limit=100`
 * - cursor 分页：「加载更多」按钮，无 `next_cursor` 时隐藏
 * - 级别筛选 `Select`：全部 / debug / info / warn / error → query `level`
 * - 列：timestamp、level Tag、message（monospace + ellipsis + tooltip）、container、stream
 * - 空日志：`Empty description="暂无日志"`
 * - API 失败：`Alert theme="error"` + message + request_id + 重试按钮
 * - loading 态：`Table loading`
 */
import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Alert, Button, Empty, Select, Space, Table, Tag } from 'tdesign-react'
import type { BaseTableCol, TableRowData } from 'tdesign-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** 日志条目类型（来自 Core OpenAPI `InstanceLogEntry`）。 */
type InstanceLogEntry = components['schemas']['InstanceLogEntry']

/** 日志列表响应类型（来自 Core OpenAPI `InstanceLogListResponse`）。 */
type InstanceLogListResponse = components['schemas']['InstanceLogListResponse']

/** 日志级别类型（来自 OpenAPI `InstanceLogEntry.level`）。 */
type LogLevel = 'debug' | 'info' | 'warn' | 'error'

/** 级别筛选选项（含「全部」）。 */
const LEVEL_OPTIONS: Array<{ label: string; value: LogLevel | '' }> = [
  { label: '全部', value: '' },
  { label: 'debug', value: 'debug' },
  { label: 'info', value: 'info' },
  { label: 'warn', value: 'warn' },
  { label: 'error', value: 'error' },
]

/** 默认每页条数。对齐 PRD US-008 / SPEC §4.1.1。 */
const DEFAULT_LIMIT = 100

/** Core API 错误响应结构（来自 OpenAPI `ErrorResponse`）。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/**
 * 日志级别 → Tag theme 映射。
 * 对齐 UX §5.2：error→danger, warn→warning, info→primary, debug→default。
 */
function levelTheme(level: LogLevel): 'danger' | 'warning' | 'primary' | 'default' {
  switch (level) {
    case 'error':
      return 'danger'
    case 'warn':
      return 'warning'
    case 'info':
      return 'primary'
    case 'debug':
      return 'default'
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

/** 表格列定义。对齐 UX §4.2 / SPEC §4.1.1。 */
const COLUMNS: BaseTableCol<TableRowData>[] = [
  {
    title: '时间',
    colKey: 'timestamp',
    width: 200,
    cell: ({ row }) => formatTimestamp((row as InstanceLogEntry).timestamp),
  },
  {
    title: '级别',
    colKey: 'level',
    width: 100,
    cell: ({ row }) => {
      const logRow = row as InstanceLogEntry
      return <Tag theme={levelTheme(logRow.level)}>{logRow.level}</Tag>
    },
  },
  {
    title: '消息',
    colKey: 'message',
    // message 列 monospace，超长 ellipsis + tooltip
    cell: ({ row }) => {
      const logRow = row as InstanceLogEntry
      return (
        <div
          title={logRow.message}
          style={{
            fontFamily: 'var(--td-font-family-mono, monospace)',
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            maxWidth: 480,
          }}
        >
          {logRow.message}
        </div>
      )
    },
  },
  {
    title: '容器',
    colKey: 'container',
    width: 160,
    cell: ({ row }) => (row as InstanceLogEntry).container ?? '-',
  },
  {
    title: '流',
    colKey: 'stream',
    width: 100,
    cell: ({ row }) => (row as InstanceLogEntry).stream ?? '-',
  },
]

/**
 * 日志 Tab 组件。
 *
 * 使用 `useInfiniteQuery` 实现 cursor 分页：
 * - 首屏加载 `limit=100`
 * - 「加载更多」传入 `next_cursor` 追加下一页
 * - 级别筛选变更时重置查询
 */
export function LogsTab() {
  const { instance } = useInstanceContext()
  const instanceId = instance.id

  const [levelFilter, setLevelFilter] = useState<LogLevel | ''>('')

  const {
    data,
    isLoading,
    isError,
    error,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
    refetch,
  } = useInfiniteQuery({
    queryKey: ['instance-logs', instanceId, levelFilter],
    queryFn: async ({ pageParam }: { pageParam: string | undefined }) => {
      const { data, error } = await coreApi.GET('/instances/{instance_id}/logs', {
        params: {
          path: { instance_id: instanceId },
          query: {
            limit: DEFAULT_LIMIT,
            cursor: pageParam,
            level: levelFilter || undefined,
          },
        },
      })
      if (error) throw error
      return data as InstanceLogListResponse
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: InstanceLogListResponse) =>
      lastPage.next_cursor ?? undefined,
  })

  // 合并所有已加载页的日志条目，并附加合成行键
  const logs: TableRowData[] =
    data?.pages.flatMap((page, pageIndex) =>
      page.items.map((item, itemIndex) => ({
        ...item,
        __rowKey: `${pageIndex}-${itemIndex}-${item.timestamp}`,
      })),
    ) ?? []

  // 错误态：Alert theme="error" + message + request_id + 重试按钮
  if (isError) {
    const err = error as CoreApiError
    const message = err?.message ?? '加载日志失败'
    const requestId = err?.request_id
    return (
      <Alert
        theme="error"
        title="加载日志失败"
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

  // 空态：Empty description="暂无日志"
  if (!isLoading && logs.length === 0) {
    return (
      <div>
        <LogsToolbar
          levelFilter={levelFilter}
          onLevelChange={setLevelFilter}
          onRefresh={() => refetch()}
        />
        <Empty description="暂无日志" />
      </div>
    )
  }

  return (
    <div>
      <LogsToolbar
        levelFilter={levelFilter}
        onLevelChange={setLevelFilter}
        onRefresh={() => refetch()}
        refreshing={isLoading}
      />
      <Table
        loading={isLoading}
        data={logs}
        columns={COLUMNS}
        rowKey="__rowKey"
        size="small"
        bordered
      />
      {/* 加载更多：有 next_cursor 时显示；无 next_cursor 时隐藏 */}
      {hasNextPage ? (
        <div style={{ marginTop: 12, textAlign: 'center' }}>
          <Button
            variant="outline"
            loading={isFetchingNextPage}
            onClick={() => fetchNextPage()}
          >
            加载更多
          </Button>
        </div>
      ) : null}
    </div>
  )
}

/** 日志 Tab 工具条：级别筛选 + 刷新按钮。对齐 UX §4.2。 */
function LogsToolbar({
  levelFilter,
  onLevelChange,
  onRefresh,
  refreshing,
}: {
  levelFilter: LogLevel | ''
  onLevelChange: (value: LogLevel | '') => void
  onRefresh: () => void
  refreshing?: boolean
}) {
  return (
    <div style={{ marginBottom: 12 }}>
      <Space>
        <Select
          value={levelFilter}
          options={LEVEL_OPTIONS}
          onChange={(value) => onLevelChange(value as LogLevel | '')}
          style={{ width: 160 }}
          placeholder="级别筛选"
        />
        <Button variant="outline" loading={refreshing} onClick={onRefresh}>
          刷新
        </Button>
      </Space>
    </div>
  )
}
