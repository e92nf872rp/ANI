/**
 * PlatformRankTable — BOSS 平台跨租户排行表格组件。
 *
 * 列（UX §5.2）：
 * - 租户 ID (tenant_id)：平台视角下必填
 * - 资源类型 (resource_type)：专页固定 / 聚合页可切换
 * - 用量 (total_quantity)：sortable；原样展示（FR-18 不做单位换算）
 * - 单位 (unit)：原样展示（FR-18）
 * - 周期 (period)：group_by 时间桶时显示，可空显示 —
 * - 操作：「查看明细」→ 打开 Drawer（FR-16）
 *
 * 行操作「查看明细」通过 onRowDrilldown 回调通知父组件打开 Drawer。
 */
import { useMemo, useState } from 'react'
import { Table } from 'tdesign-react'
import type { PrimaryTableCol, TableSort } from 'tdesign-react'
import type { PlatformUsageRow } from './types'

/** resource_type 枚举值 → 中文 label 映射（与 METRIC_VIEW_OPTIONS / METRIC_PAGES 对齐） */
const RESOURCE_TYPE_LABELS: Record<string, string> = {
  instance_gpu_seconds: 'GPU 算力',
  instance_cpu_seconds: 'CPU 算力',
  instance_memory_gib_seconds: '内存',
  token_input: 'Input Tokens',
  token_output: 'Output Tokens',
  token_total: 'Token 合计',
}

/**
 * 将 resource_type 枚举值映射为中文 label；未映射的值原样返回。
 *
 * 与 METRIC_VIEW_OPTIONS / METRIC_PAGES 文案保持一致。
 */
function formatResourceType(resource_type: string): string {
  return RESOURCE_TYPE_LABELS[resource_type] ?? resource_type
}

export interface PlatformRankTableProps {
  /** 表格数据，来自平台 API 响应 items[] */
  data: PlatformUsageRow[]
  /** 加载态；fetching 时 Table 显示 loading */
  loading?: boolean
  /** 行「查看明细」回调；参数为行对应的 PlatformUsageRow */
  onRowDrilldown?: (row: PlatformUsageRow) => void
  /** 是否显示「查看明细」操作列，默认 true；钻取 Drawer 内明细表传 false 隐藏 */
  showDrilldownAction?: boolean
}

/**
 * 平台跨租户排行表格。
 *
 * - 默认按 total_quantity 降序（排行语义）
 * - 用量列 sortable（UX §5.2 / Issue AC: sortable on total_quantity）
 * - 单位列原样展示（FR-18，不做换算）
 * - 周期列空值显示 —
 * - 操作列「查看明细」触发 onRowDrilldown；showDrilldownAction=false 时隐藏
 */
export function PlatformRankTable({
  data,
  loading = false,
  onRowDrilldown,
  showDrilldownAction = true,
}: PlatformRankTableProps) {
  // 受控排序状态：TDesign Table 在受控 data 模式下内部排序会被覆盖，
  // 需通过 onSortChange + useMemo 手动排序
  const [sortInfo, setSortInfo] = useState<TableSort>({
    sortBy: 'total_quantity',
    descending: true,
  })

  const sortedData = useMemo(() => {
    const sort = Array.isArray(sortInfo) ? sortInfo[0] : sortInfo
    if (!sort?.sortBy) return data
    if (sort.sortBy !== 'total_quantity') return data
    const copy = [...data]
    copy.sort((a, b) =>
      sort.descending
        ? b.total_quantity - a.total_quantity
        : a.total_quantity - b.total_quantity,
    )
    return copy
  }, [data, sortInfo])

  // TDesign Table rowKey 只接受字段名（string），不接受函数；
  // 预计算每行唯一键，挂到 __rowKey 字段上作为 rowKey
  const dataWithKey = useMemo(
    () => sortedData.map((row) => ({ ...row, __rowKey: `${row.tenant_id}::${row.resource_type}` })),
    [sortedData],
  )

  const columns: PrimaryTableCol<PlatformUsageRow>[] = [
    {
      colKey: 'tenant_id',
      title: '租户 ID',
      ellipsis: true,
      minWidth: 120,
    },
    {
      colKey: 'resource_type',
      title: '资源类型',
      cell: ({ row }) => formatResourceType(row.resource_type),
      minWidth: 120,
    },
    {
      colKey: 'total_quantity',
      title: '用量',
      // sortable on total_quantity（Issue AC）：传比较函数使 TDesign 自动排序
      // sorter: true 只显示排序图标但不排序数据，必须是函数才生效
      sorter: (a, b) => a.total_quantity - b.total_quantity,
      sortType: 'all',
      minWidth: 120,
    },
    {
      colKey: 'unit',
      title: '单位',
      cell: ({ row }) => row.unit ?? '',
      minWidth: 80,
    },
    {
      colKey: 'period',
      title: '周期',
      cell: ({ row }) => row.period ?? '—',
      minWidth: 120,
    },
  ]

  // 钻取 Drawer 内明细表传 showDrilldownAction=false 隐藏操作列
  if (showDrilldownAction) {
    columns.push({
      colKey: 'operate',
      title: '操作',
      fixed: 'right',
      width: 100,
      cell: ({ row }) => {
        // 解构过滤 __rowKey，只传出原始 PlatformUsageRow 字段
        const cleanRow = { ...row } as PlatformUsageRow & { __rowKey?: string }
        delete cleanRow.__rowKey
        return (
          <span
            role="button"
            tabIndex={0}
            style={{ color: 'var(--td-brand-color)', cursor: 'pointer' }}
            onClick={() => onRowDrilldown?.(cleanRow)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                onRowDrilldown?.(cleanRow)
              }
            }}
          >
            查看明细
          </span>
        )
      },
    })
  }

  return (
    <Table
      rowKey="__rowKey"
      data={dataWithKey}
      columns={columns}
      loading={loading}
      size="medium"
      hover
      stripe
      // 受控排序：sort + onSortChange 配合 useMemo 手动排序
      sort={sortInfo}
      onSortChange={(sort) => setSortInfo(sort)}
    />
  )
}
