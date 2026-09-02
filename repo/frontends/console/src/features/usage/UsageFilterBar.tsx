/**
 * UsageFilterBar — Console 租户用量报表筛选区组件（Issue #9）。
 *
 * 组成：
 * - DateRangePicker（必填，校验 start < end，inline 错误）
 * - 预设视角 Tabs（theme="card"，5 P0 启用 + 2 P1 disabled + Tooltip）
 * - group_by Segmented（Radio.Group theme="button"，4 选项）
 *
 * 筛选变更由父组件通过 onChange 接收，debounce 自动查询由父组件实现（无查询按钮）。
 *
 * @see UX §5.1 组件映射
 * @see SPEC §5.1, §5.2, §5.3
 */

import { DateRangePicker, Radio, Tabs, Tooltip } from 'tdesign-react'
import type { DateRangeValue } from 'tdesign-react/es/date-picker/type'
import type { TabValue } from 'tdesign-react/es/tabs/type'
import { GROUP_BY_OPTIONS, RESOURCE_TYPE_TABS } from './constants'
import type { GroupByOption } from './constants'
import type { UsageFilter } from './types'
import { isValidRange } from './useDebouncedFilter'

/** UsageFilterBar 组件 Props */
export interface UsageFilterBarProps {
  /** 当前筛选状态（受控） */
  filter: UsageFilter
  /** 筛选变更回调（父组件负责 debounce 自动查询） */
  onChange: (filter: UsageFilter) => void
}

/**
 * 用量报表筛选区组件。
 *
 * 包含 DateRangePicker、预设视角 Tabs 和 group_by Segmented，
 * 筛选变更通过 onChange 通知父组件，无查询按钮（debounce auto-fetch）。
 */
export function UsageFilterBar({ filter, onChange }: UsageFilterBarProps) {
  const rangeInvalid = !isValidRange(filter)

  /** DateRangePicker 值变更处理 */
  const handleRangeChange = (value: DateRangeValue) => {
    if (!value || value.length < 2 || !value[0] || !value[1]) return
    onChange({
      ...filter,
      start_time: new Date(value[0] as string).toISOString(),
      end_time: new Date(value[1] as string).toISOString(),
    })
  }

  /** 预设视角 Tab 切换处理 */
  const handleTabChange = (value: TabValue) => {
    onChange({ ...filter, resource_type: value as string })
  }

  /** group_by Segmented 切换处理 */
  const handleGroupByChange = (value: GroupByOption) => {
    onChange({ ...filter, group_by: value })
  }

  /** 将 ISO 8601 时间字符串格式化为 DateRangePicker 所需的 YYYY-MM-DD HH:mm:ss 格式 */
  const formatForPicker = (iso: string): string => {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return ''
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }

  /** 当前 DateRangePicker 值（ISO 8601 转为 YYYY-MM-DD HH:mm:ss 格式） */
  const rangeValue: DateRangeValue = [formatForPicker(filter.start_time), formatForPicker(filter.end_time)]

  /** 当前选中的 Tab 值 */
  const activeTab: TabValue = filter.resource_type ?? ''

  /** 当前 group_by 值 */
  const activeGroupBy: GroupByOption = filter.group_by ?? 'resource_type'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* 统计时间范围 */}
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        <label
          style={{
            flexShrink: 0,
            paddingTop: 6,
            color: 'var(--td-text-color-primary)',
            fontWeight: 500,
          }}
        >
          统计时间范围
        </label>
        <DateRangePicker
          value={rangeValue}
          onChange={handleRangeChange}
          enableTimePicker
          valueType="YYYY-MM-DD HH:mm:ss"
          clearable={false}
          status={rangeInvalid ? 'error' : 'default'}
          tips={
            rangeInvalid ? (
              <span style={{ color: 'var(--td-error-color)' }}>
                结束时间必须晚于开始时间
              </span>
            ) : undefined
          }
          style={{ width: 360 }}
        />
      </div>

      {/* 预设视角 Tabs */}
      <Tabs value={activeTab} onChange={handleTabChange} theme="card">
        {RESOURCE_TYPE_TABS.map((tab) => (
          <Tabs.TabPanel
            key={tab.value}
            value={tab.value}
            label={
              tab.enabled ? (
                tab.label
              ) : (
                <Tooltip content={tab.disabledTooltip ?? '待 API 合入（P1）'}>
                  <span style={{ color: 'var(--td-text-color-disabled)' }}>
                    {tab.label}
                  </span>
                </Tooltip>
              )
            }
            disabled={!tab.enabled}
          />
        ))}
      </Tabs>

      {/* group_by Segmented（使用 Radio.Group theme="button" 实现） */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <label
          style={{
            flexShrink: 0,
            color: 'var(--td-text-color-primary)',
            fontWeight: 500,
          }}
        >
          分组维度
        </label>
        <Radio.Group
          value={activeGroupBy}
          onChange={handleGroupByChange}
          variant="default-filled"
        >
          {GROUP_BY_OPTIONS.map((option) => (
            <Radio key={option.value} value={option.value}>
              {option.label}
            </Radio>
          ))}
        </Radio.Group>
      </div>
    </div>
  )
}
