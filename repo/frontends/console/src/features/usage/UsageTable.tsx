/**
 * UsageTable — Console 租户用量报表明细表格组件（Issue #11）。
 *
 * 职责：
 * - 4 列：资源类型、用量（total_quantity 原样）、单位（unit 原样）、统计周期（period 可空显示 —）
 * - FR-18：不做 seconds→hours 换算，原样展示 total_quantity + unit
 * - FR-17：未筛 resource_type 时表格可展示 token_total 行（由父组件传入 items 决定，本组件不做过滤）
 * - loading 态：Table `loading`
 * - rowKey：`resource_type+period`（复合键，处理不同周期同一资源类型的多行）
 *
 * @see UX §5.1 Table columns
 * @see SPEC §5.4 Edge Cases、§5.3 状态机
 */

import { useMemo } from 'react'
import { Table } from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import type { UsageRow } from './types'

/** UsageTable 组件 Props */
export interface UsageTableProps {
  /** 用量数据行（来自 API items[]，与 UsageChart 同源） */
  items: UsageRow[]
  /** loading 态：Table loading 属性 */
  loading: boolean
}

/** 复合行键字段名，TDesign Table rowKey 需为 string 类型 */
const ROW_KEY_FIELD = '_rowKey'

/** 带复合键的行类型（内部使用，不对外暴露） */
interface UsageRowWithKey extends UsageRow {
  [ROW_KEY_FIELD]: string
}

/**
 * 构造表格行复合键（resource_type + period）。
 *
 * 不同周期可能存在同一 resource_type 的多行，用复合键保证唯一性。
 * period 为空时用空字符串占位，避免 undefined 拼接出 "type+undefined"。
 *
 * @param row 用量数据行
 * @returns 形如 "token_input+2026-07-01" 的复合键
 */
function buildRowKey(row: UsageRow): string {
  return `${row.resource_type ?? ''}+${row.period ?? ''}`
}

/**
 * 为表格行添加复合键字段，满足 TDesign Table rowKey 需为 string 的约束。
 *
 * @param items 原始用量数据
 * @returns 带复合键的行数组
 */
function withRowKeys(items: UsageRow[]): UsageRowWithKey[] {
  return items.map((item) => ({
    ...item,
    [ROW_KEY_FIELD]: buildRowKey(item),
  }))
}

/**
 * 构造表格列定义（UX §5.1 Table columns）。
 *
 * 4 列：资源类型、用量、单位、统计周期。
 * total_quantity / unit 原样展示（FR-18）；period 可空显示 —。
 */
function buildColumns(): PrimaryTableCol<UsageRowWithKey>[] {
  return [
    { title: '资源类型', colKey: 'resource_type' },
    { title: '用量', colKey: 'total_quantity' },
    { title: '单位', colKey: 'unit' },
    {
      title: '统计周期',
      colKey: 'period',
      // period 可空显示 —（UX §5.1、SPEC §5.4）
      cell: ({ row }) => (row.period ? row.period : '—'),
    },
  ]
}

/**
 * 用量明细表格组件。
 *
 * 原样展示 API 返回的 total_quantity + unit（FR-18 不换算），
 * 未筛 resource_type 时可展示 token_total 行（FR-17，由父组件控制 items）。
 */
export function UsageTable({ items, loading }: UsageTableProps) {
  const columns = buildColumns()
  const data = useMemo(() => withRowKeys(items), [items])

  return (
    <Table
      data={data}
      columns={columns}
      rowKey={ROW_KEY_FIELD}
      loading={loading}
    />
  )
}
