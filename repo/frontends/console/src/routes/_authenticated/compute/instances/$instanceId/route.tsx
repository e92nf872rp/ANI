/**
 * 实例详情路由壳层：PageHeader + Tab 栏 + Tab Panel。
 *
 * 对齐：
 * - PRD US-007 统一观测 Tab 壳层（全 kind）
 * - UX §4.0 详情页可观测性公共壳、§4.1 Kind × Tab 可见性、§2.1 深链
 * - SPEC §2.4.1 Console 端新建文件结构、§3.3 Console 内部类型、§5.1.1 壳层与导航组件
 *
 * 说明：本文件为 issue-003 范围，仅建立壳层、上下文与 Tab 可见性过滤。
 * 各 Tab 面板内容（日志/事件/指标/终端/控制台/安全事件）由后续 issue 实现，
 * 此处渲染占位 Panel。
 */
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Alert, Breadcrumb, Loading, Space, Tabs, Tag, MessagePlugin } from 'tdesign-react'
import { CopyIcon } from 'tdesign-icons-react'
import { coreApi } from '@/api/coreClient'
import {
  InstanceContext,
  buildInstanceContextValue,
  useInstanceContext,
} from '@/features/instance-observability/InstanceContext'
import {
  type ObservabilityTabId,
  getVisibleTabs,
  resolveTabFromQuery,
} from '@/features/instance-observability/observabilityTabsConfig'
import { LogsTab } from '@/features/instance-observability/LogsTab'
import { EventsTab } from '@/features/instance-observability/EventsTab'
import { MetricsTab } from '@/features/instance-observability/MetricsTab'
import { TerminalTab } from '@/features/instance-observability/TerminalTab'
import { ConsoleTab } from '@/features/instance-observability/ConsoleTab'
import { SecurityEventsTab } from '@/features/instance-observability/SecurityEventsTab'

/** 深链 `?tab=` 允许的取值。 */
const TAB_QUERY_VALUES = [
  'logs',
  'events',
  'metrics',
  'terminal',
  'console',
  'security-events',
] as const

/** Tabs 组件要求的 value 类型为 string。 */
type TabValue = string

/** Tab 元数据：id + 中文标签。对齐 UX §7.1 Labels & Tabs。 */
const TAB_LABELS: Record<ObservabilityTabId, string> = {
  logs: '日志',
  events: '事件',
  metrics: '指标',
  terminal: '终端',
  console: '控制台',
  'security-events': '安全事件',
}

export const Route = createFileRoute('/_authenticated/compute/instances/$instanceId')({
  /**
   * 解析 `?tab=` 深链；非法/不可见 tab 在组件内回退到「日志」。
   * 这里只做宽松校验（返回 string），严格可见性校验依赖 kind，在组件内完成。
   */
  validateSearch: (input: Record<string, unknown>): { tab?: string } => {
    const raw = input?.tab
    if (typeof raw === 'string' && TAB_QUERY_VALUES.includes(raw as (typeof TAB_QUERY_VALUES)[number])) {
      return { tab: raw }
    }
    return {}
  },
  component: InstanceDetailShell,
})

function InstanceDetailShell() {
  const { instanceId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()

  // 拉取实例详情。对齐 SPEC §4.0 `getInstance`（GET /instances/{instance_id}）。
  const { data: instance, isLoading, error } = useQuery({
    queryKey: ['instance', instanceId],
    queryFn: async () => {
      const { data, error } = await coreApi.GET('/instances/{instance_id}', {
        params: { path: { instance_id: instanceId } },
      })
      if (error) throw error
      return data
    },
  })

  if (isLoading) {
    return <Loading text="加载实例详情中…" />
  }

  if (error || !instance) {
    return (
      <Alert
        theme="error"
        title="加载实例失败"
        message={(error as { message?: string })?.message ?? '实例不存在或无访问权限'}
      />
    )
  }

  const ctx = buildInstanceContextValue(instance)

  // deleted 状态：整页 Empty「实例已删除」，不渲染 Tab。
  // 对齐 issue AC：deleted 状态实例不可进入观测 Tab。
  if (ctx.isDeleted) {
    return (
      <Alert
        theme="warning"
        title="实例已删除"
        message="该实例已删除，可观测性功能不可用。"
      />
    )
  }

  const visibleTabs = getVisibleTabs(ctx.kind)
  const activeTab = resolveTabFromQuery(search.tab, ctx.kind)

  return (
    <InstanceContext.Provider value={ctx}>
      <InstanceDetailHeader />
      <Tabs
        value={activeTab as TabValue}
        onChange={(value) => {
          // Tab 切换同步到 `?tab=` 深链。
          // 对齐 UX §2.1 深链规则。
          const next = value as ObservabilityTabId
          if (visibleTabs.includes(next)) {
            navigate({ to: '.', search: { tab: next } })
          }
        }}
        placement="top"
      >
        {visibleTabs.map((tabId) => (
          <Tabs.TabPanel key={tabId} value={tabId as TabValue} label={TAB_LABELS[tabId]}>
            <TabPanelPlaceholder tabId={tabId} />
          </Tabs.TabPanel>
        ))}
      </Tabs>
    </InstanceContext.Provider>
  )
}

/** PageHeader：name、id（可复制）、state Tag、kind Tag + 面包屑。对齐 UX §4.0、§5.1。 */
function InstanceDetailHeader() {
  const { instance, kind, state } = useInstanceContext()

  const stateTheme = mapStateTheme(state)
  const kindLabel = mapKindLabel(kind)

  const handleCopyId = async () => {
    try {
      await navigator.clipboard.writeText(instance.id)
      MessagePlugin.success('已复制实例 ID')
    } catch {
      MessagePlugin.warning('复制失败，请手动选择 ID 复制')
    }
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <Breadcrumb
        style={{ marginBottom: 12 }}
        options={[
          { content: '算力与云资源' },
          { content: '实例管理' },
          { content: instance.name },
        ]}
      />
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <h2 style={{ margin: 0 }}>{instance.name}</h2>
        <Tag theme={stateTheme}>{state}</Tag>
        <Tag variant="outline">{kindLabel}</Tag>
        <Space size="small">
          <span style={{ color: 'var(--td-text-color-placeholder)', fontSize: 12 }}>
            ID: {instance.id}
          </span>
          <button
            type="button"
            onClick={handleCopyId}
            title="复制实例 ID"
            style={{
              border: 'none',
              background: 'transparent',
              cursor: 'pointer',
              padding: 0,
              color: 'var(--td-brand-color)',
              display: 'inline-flex',
              alignItems: 'center',
            }}
          >
            <CopyIcon />
          </button>
        </Space>
      </div>
    </div>
  )
}

/**
 * Tab 面板内容路由：logs Tab 渲染真实组件，其余 Tab 仍渲染占位。
 * 各 Tab 实际内容由后续 issue 实现，逐步替换占位。
 */
function TabPanelPlaceholder({ tabId }: { tabId: ObservabilityTabId }) {
  if (tabId === 'logs') {
    return <LogsTab />
  }
  if (tabId === 'events') {
    return <EventsTab />
  }
  if (tabId === 'metrics') {
    return <MetricsTab />
  }
  if (tabId === 'terminal') {
    return <TerminalTab />
  }
  if (tabId === 'console') {
    return <ConsoleTab />
  }
  if (tabId === 'security-events') {
    return <SecurityEventsTab />
  }
  return (
    <Alert
      theme="info"
      title={`${TAB_LABELS[tabId]} Tab`}
      message="该 Tab 内容由后续 issue 实现，当前仅提供壳层与上下文。"
    />
  )
}

// ---- 辅助函数 ----

/**
 * 实例状态 → Tag theme 映射。
 * 对齐 UX §5.1：running→success, stopped→default, failed→danger。
 */
function mapStateTheme(state: string): 'success' | 'default' | 'danger' | 'warning' {
  switch (state) {
    case 'running':
      return 'success'
    case 'stopped':
      return 'default'
    case 'failed':
      return 'danger'
    case 'pending':
    case 'provisioning':
    case 'starting':
    case 'stopping':
    case 'deleting':
      return 'warning'
    default:
      return 'default'
  }
}

/** kind → 中文标签（首期直接展示 kind 原值，后续可扩展为友好名称）。 */
function mapKindLabel(kind: string): string {
  return kind
}
