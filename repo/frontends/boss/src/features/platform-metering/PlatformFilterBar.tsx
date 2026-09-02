/**
 * PlatformFilterBar — BOSS 平台计量筛选区组件。
 *
 * 包含：
 * - DateRangePicker：时间范围（必填，start < end 校验）
 * - 指标视角 Select：聚合页可切换 resource_type；专页隐藏（hideMetricView=true）
 * - 租户 Select：filterable, clearable（可选筛选）
 * - group_by Select：tenant_id / day / hour
 *
 * 筛选变更通过 onChange 回调通知父组件，父组件配合 useDebouncedFilter 触发查询。
 */
import { DateRangePicker, Select } from 'tdesign-react'
import type { DateRangeValue } from 'tdesign-react'
import type { PlatformUsageFilter } from './types'
import {
  PLATFORM_GROUP_BY_OPTIONS,
  METRIC_VIEW_OPTIONS,
} from './constants'
import type { PlatformGroupByOption } from './constants'

export interface PlatformFilterBarProps {
  /** 当前筛选状态 */
  filter: PlatformUsageFilter
  /** 筛选变更回调 */
  onFilterChange: (filter: PlatformUsageFilter) => void
  /** 是否隐藏指标视角 Select；专页固定 resource_type 时设为 true */
  hideMetricView?: boolean
  /** 可选租户列表（label/value）；由父组件提供（如从 API 获取租户列表） */
  tenantOptions?: { label: string; value: string }[]
}

/** 时间范围非法时的 inline 错误文案（UX §7.2） */
const INVALID_RANGE_MESSAGE = '结束时间必须晚于开始时间'

/**
 * 校验时间范围是否有效（start < end）。
 *
 * DateRangePicker 的 value 可能为 null / undefined / 空数组 / 不完整数组，
 * 需要分别处理：只有当两端都有值且 start < end 时才有效。
 */
function isValidDateRange(value: DateRangeValue): boolean {
  if (!value || !Array.isArray(value) || value.length !== 2) return false
  const [start, end] = value
  if (!start || !end) return false
  return new Date(start).getTime() < new Date(end).getTime()
}

/**
 * 将 DateRangePicker 的值转换为 PlatformUsageFilter 的 start_time / end_time（ISO 8601）。
 *
 * DateRangePicker valueType 默认为 'YYYY-MM-DD'；
 * API 需要 ISO 8601 date-time，因此用 new Date() 转换后 toISOString()。
 */
function dateRangeToTimes(value: DateRangeValue): { start_time: string; end_time: string } | null {
  if (!value || !Array.isArray(value) || value.length !== 2) return null
  const [start, end] = value
  if (!start || !end) return null
  return {
    start_time: new Date(start).toISOString(),
    end_time: new Date(end).toISOString(),
  }
}

/**
 * 将 PlatformUsageFilter 的 start_time / end_time 转换为 DateRangePicker 的 value。
 *
 * DateRangePicker 默认 valueType 为 'YYYY-MM-DD'，取日期部分即可。
 */
function timesToDateRange(filter: PlatformUsageFilter): DateRangeValue {
  if (!filter.start_time || !filter.end_time) return ['', '']
  return [
    filter.start_time.slice(0, 10),
    filter.end_time.slice(0, 10),
  ]
}

/**
 * 平台计量筛选区组件。
 *
 * 聚合页使用时 hideMetricView=false（默认），提供指标视角 Select；
 * 专页使用时 hideMetricView=true，不提供指标视角切换。
 */
export function PlatformFilterBar({
  filter,
  onFilterChange,
  hideMetricView = false,
  tenantOptions = [],
}: PlatformFilterBarProps) {
  const dateRangeValue = timesToDateRange(filter)
  const isRangeValid = isValidDateRange(dateRangeValue)

  /** DateRangePicker 变更 */
  const handleDateRangeChange = (value: DateRangeValue) => {
    const times = dateRangeToTimes(value)
    if (times) {
      onFilterChange({ ...filter, start_time: times.start_time, end_time: times.end_time })
    } else {
      // 清空或不完整时，保留原 resource_type / group_by / tenant_id
      onFilterChange({
        ...filter,
        start_time: '',
        end_time: '',
      })
    }
  }

  /** 指标视角变更 */
  const handleMetricViewChange = (value: unknown) => {
    onFilterChange({ ...filter, resource_type: String(value) })
  }

  /** 租户变更 */
  const handleTenantChange = (value: unknown) => {
    // TDesign Select clearable 清空时 value=null，String(null) 会变成 'null' 字符串
    if (value === null || value === undefined) {
      onFilterChange({ ...filter, tenant_id: undefined })
    } else {
      onFilterChange({ ...filter, tenant_id: String(value) || undefined })
    }
  }

  /** group_by 变更 */
  const handleGroupByChange = (value: unknown) => {
    onFilterChange({ ...filter, group_by: value as PlatformGroupByOption })
  }

  return (
    <div
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-start',
        gap: 16,
      }}
    >
      {/* 时间范围筛选（必填） */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <DateRangePicker
          value={dateRangeValue}
          onChange={handleDateRangeChange}
          clearable={false}
          placeholder={['开始日期', '结束日期']}
        />
        {!isRangeValid && (filter.start_time || filter.end_time) && (
          <span style={{ color: 'var(--td-error-color)', fontSize: 12 }}>
            {INVALID_RANGE_MESSAGE}
          </span>
        )}
      </div>

      {/* 指标视角 Select — 聚合页可切换；专页隐藏 */}
      {!hideMetricView && (
        <Select
          value={filter.resource_type}
          onChange={handleMetricViewChange}
          options={METRIC_VIEW_OPTIONS}
          placeholder="选择指标视角"
          style={{ width: 160 }}
        />
      )}

      {/* 租户 Select — filterable, clearable */}
      <Select
        value={filter.tenant_id ?? ''}
        onChange={handleTenantChange}
        options={tenantOptions}
        placeholder="筛选租户"
        filterable
        clearable
        style={{ width: 200 }}
      />

      {/* group_by Select — tenant_id / day / hour */}
      <Select
        value={filter.group_by ?? 'tenant_id'}
        onChange={handleGroupByChange}
        options={PLATFORM_GROUP_BY_OPTIONS}
        placeholder="选择分组维度"
        style={{ width: 140 }}
      />
    </div>
  )
}
