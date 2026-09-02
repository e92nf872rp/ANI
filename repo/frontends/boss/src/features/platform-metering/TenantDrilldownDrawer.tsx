/**
 * TenantDrilldownDrawer — BOSS 平台单租户钻取抽屉。
 *
 * 从排行表行「查看明细」打开，调用平台 API 钻取单租户明细（FR-16）：
 *   GET /metering/usage/platform?tenant_id={id}&start_time=…&end_time=…
 *   + 继承主查询 resource_type + group_by 默认 day（Drawer 内默认按天）
 *
 * 禁止：GET /metering/usage（租户 path）、JWT 轮询、impersonate（FR-16）。
 * Drawer 内展示明细 Table + 趋势图；支持 loading / empty / error / forbidden 四态。
 * Drawer 内可切换 group_by（day / hour），联动图表和表格更新。
 */
import { useMemo, useState } from 'react'
import { Drawer, Alert, Empty, Button, Select } from 'tdesign-react'
import { usePlatformUsageQuery } from './usePlatformUsageQuery'
import { PlatformRankTable } from './PlatformRankTable'
import { PlatformTrendChart } from './PlatformTrendChart'
import type { PlatformUsageFilter, PlatformUsageRow } from './types'
import type { PlatformGroupByOption } from './constants'

/** Drawer 内 group_by 可选项（钻取已固定租户，不含 tenant_id） */
const DRILLDOWN_GROUP_BY_OPTIONS: { label: string; value: PlatformGroupByOption }[] = [
  { label: '按天', value: 'day' },
  { label: '按小时', value: 'hour' },
]

export interface TenantDrilldownDrawerProps {
  /** 是否显示 */
  visible: boolean
  /** 关闭回调 */
  onClose: () => void
  /** 钻取的租户行数据（含 tenant_id + resource_type） */
  row: PlatformUsageRow | null
  /** 主查询筛选条件（继承 start_time / end_time / resource_type） */
  baseFilter: PlatformUsageFilter
}

/** Drawer 内默认 group_by（FR-16：钻取默认按天） */
const DEFAULT_GROUP_BY: PlatformGroupByOption = 'day'

/**
 * 构建钻取查询的 filter。
 *
 * 继承主查询的 start_time / end_time / resource_type，
 * 设置 tenant_id 为行租户 ID，group_by 由 Drawer 内切换控制（默认 day）。
 */
function buildDrilldownFilter(
  row: PlatformUsageRow,
  baseFilter: PlatformUsageFilter,
  groupBy: PlatformGroupByOption,
): PlatformUsageFilter {
  return {
    start_time: baseFilter.start_time,
    end_time: baseFilter.end_time,
    resource_type: baseFilter.resource_type ?? row.resource_type,
    tenant_id: row.tenant_id,
    group_by: groupBy,
  }
}

/**
 * 将 API 返回的 items 转换为 PlatformUsageRow[]。
 *
 * OpenAPI 中 MeteringUsageRecord.tenant_id 为 `string | null | undefined`，
 * 但平台视角下业务约束为必填。此处过滤掉 tenant_id 为空的行并做类型收窄。
 */
function toPlatformRows(items: { tenant_id?: string | null; resource_type: string; total_quantity: number; unit: string; period?: string | null }[]): PlatformUsageRow[] {
  return items
    .filter((item): item is { tenant_id: string; resource_type: string; total_quantity: number; unit: string; period?: string | null } => Boolean(item.tenant_id))
    .map((item) => ({
      tenant_id: item.tenant_id,
      resource_type: item.resource_type,
      total_quantity: item.total_quantity,
      unit: item.unit,
      period: item.period ?? null,
    }))
}

/** 占位 filter（Drawer 关闭时使用，不触发真实查询） */
const EMPTY_FILTER: PlatformUsageFilter = { start_time: '', end_time: '' }

/**
 * 单租户钻取抽屉。
 *
 * visible=true 且 row 非空时触发平台 API 钻取查询（FR-16）。
 * Drawer 内展示明细 Table + 趋势图；支持 loading / empty / error / forbidden 四态。
 */
export function TenantDrilldownDrawer({
  visible,
  onClose,
  row,
  baseFilter,
}: TenantDrilldownDrawerProps) {
  // Drawer 内 group_by 状态，默认 day（FR-16），每次打开 Drawer 时重置
  const [drilldownGroupBy, setDrilldownGroupBy] = useState<PlatformGroupByOption>(DEFAULT_GROUP_BY)

  // 构建钻取 filter；row 为空或 Drawer 关闭时使用占位 filter
  const drilldownFilter = useMemo(() => {
    if (!visible || !row) return EMPTY_FILTER
    return buildDrilldownFilter(row, baseFilter, drilldownGroupBy)
  }, [visible, row, baseFilter, drilldownGroupBy])

  const query = usePlatformUsageQuery(drilldownFilter)

  // 错误状态分类
  const errorStatus = query.error ? (query.error as { status?: number }).status : undefined
  const isForbidden = errorStatus === 403
  const isApiNotReady = errorStatus === 404 || errorStatus === 501

  const drawerTitle = row ? `租户用量明细 · ${row.tenant_id}` : '租户用量明细'
  const items = query.data?.items ?? []
  const rows = toPlatformRows(items)

  return (
    <Drawer
      visible={visible}
      onClose={onClose}
      size="large"
      footer={false}
      header={drawerTitle}
    >
      {isApiNotReady && (
        <Alert theme="warning" message="平台计量接口尚未上线，暂无法展示租户明细" close={false} />
      )}
      {isForbidden && (
        <Alert theme="error" message="无权限查看该租户用量" close={false} />
      )}
      {query.error && !isForbidden && !isApiNotReady && (
        <Alert
          theme="error"
          message="用量数据加载失败，请稍后重试"
          close={false}
          operation={<Button onClick={() => query.refetch()}>重试</Button>}
        />
      )}
      {!query.error && (
        <>
          {/* Drawer 内 group_by 切换（day / hour），联动图表和表格更新 */}
          <div style={{ marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 14, color: 'var(--td-text-color-secondary)' }}>分组维度</span>
            <Select
              value={drilldownGroupBy}
              onChange={(val) => setDrilldownGroupBy(val as PlatformGroupByOption)}
              options={DRILLDOWN_GROUP_BY_OPTIONS}
              style={{ width: 140 }}
            />
          </div>
          <PlatformTrendChart
            data={rows}
            groupBy={drilldownGroupBy}
            loading={query.isFetching}
          />
          <div style={{ marginTop: 16 }}>
            <PlatformRankTable
              data={rows}
              loading={query.isFetching}
              showDrilldownAction={false}
            />
          </div>
          {!query.isFetching && rows.length === 0 && (
            <Empty description="当前条件下暂无该租户用量数据" />
          )}
        </>
      )}
    </Drawer>
  )
}
