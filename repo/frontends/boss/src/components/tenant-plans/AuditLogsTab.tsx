import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Empty,
  Pagination,
  Select,
  Table,
  Tag,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { listTenantPlanAuditLogs, listTenantPlanBoundTenants } from '@/api/tenant-plans'
import type { ApiError, PlanAuditLog } from '@/api/tenant-plans'
import { formatDateTime } from './planStatus'

interface AuditLogsTabProps {
  planId: string
}

/** 去掉 tenant_plan. / tenant. 前缀，保留英文短名 */
function formatAuditAction(action: string): string {
  const raw = action.trim()
  return raw.includes('.') ? raw.slice(raw.lastIndexOf('.') + 1) : raw
}

function asRecord(details: unknown): Record<string, unknown> {
  if (details && typeof details === 'object' && !Array.isArray(details)) {
    return details as Record<string, unknown>
  }
  return {}
}

function formatQuotaLimitsSummary(raw: unknown): string | null {
  if (!Array.isArray(raw) || raw.length === 0) return null
  const parts = raw
    .map((it) => {
      if (!it || typeof it !== 'object') return null
      const row = it as Record<string, unknown>
      const rt = String(row.resource_type ?? '')
      if (!rt) return null
      if (row.total === undefined || row.total === null) return `${rt}=默认`
      return `${rt}=${row.total}`
    })
    .filter(Boolean)
  return parts.length > 0 ? parts.join('、') : null
}

function formatTenantLabel(
  d: Record<string, unknown>,
  tenantNames?: Record<string, string>,
): string | null {
  const display = d.tenant_display_name != null ? String(d.tenant_display_name).trim() : ''
  const name = d.tenant_name != null ? String(d.tenant_name).trim() : ''
  if (display && name && display !== name) return `${display}（${name}）`
  if (display) return display
  if (name) return name
  const id = d.tenant_id != null ? String(d.tenant_id) : ''
  if (id && tenantNames?.[id]) return tenantNames[id]
  if (id) return id
  return null
}

/** 解析 details，只保留「做了什么」的摘要（不展示更新维度 / plan_id） */
function formatAuditDetails(
  action: string,
  details: unknown,
  tenantNames?: Record<string, string>,
): string {
  const d = asRecord(details)
  const short = action.includes('.')
    ? action.slice(action.lastIndexOf('.') + 1)
    : action
  const parts: string[] = []

  switch (short) {
    case 'create': {
      if (d.code) parts.push(`套餐编码 ${String(d.code)}`)
      const ql = formatQuotaLimitsSummary(d.quota_limits)
      if (ql) parts.push(`限额 ${ql}`)
      break
    }
    case 'update': {
      const fields: string[] = []
      if (d.name_updated) fields.push('名称')
      if (d.description_updated) fields.push('说明')
      parts.push(fields.length > 0 ? `更新 ${fields.join('、')}` : '更新基本信息')
      break
    }
    case 'delete':
      parts.push('删除套餐')
      break
    case 'activate':
      parts.push('发布为启用')
      break
    case 'disable':
      parts.push('停用套餐')
      break
    case 'update_quota_limits': {
      if (d.synced_tenant_count != null) {
        parts.push(`同步 ${d.synced_tenant_count} 个租户`)
      }
      if (d.skipped_approved != null && Number(d.skipped_approved) > 0) {
        parts.push(`跳过已审批 ${d.skipped_approved} 处`)
      }
      if (d.tightened != null && Number(d.tightened) > 0) {
        parts.push(`已收缩 ${d.tightened} 处`)
      }
      if (parts.length === 0) parts.push('修改限额')
      break
    }
    case 'bind_plan_quota': {
      const tenant = formatTenantLabel(d, tenantNames)
      parts.push(tenant ? `绑定租户 ${tenant}` : '绑定租户')
      if (d.skipped_approved != null && Number(d.skipped_approved) > 0) {
        parts.push(`跳过已审批 ${d.skipped_approved} 处`)
      }
      if (d.tightened != null && Number(d.tightened) > 0) {
        parts.push(`已收缩 ${d.tightened} 处`)
      }
      break
    }
    case 'quota_init_failed': {
      const tenant = formatTenantLabel(d, tenantNames)
      if (tenant) parts.push(`租户 ${tenant}`)
      parts.push('配额下发失败')
      break
    }
    default:
      break
  }

  if (d.reason) {
    parts.push(`原因：${String(d.reason)}`)
  }

  return parts.length > 0 ? parts.join('；') : '—'
}

/**
 * 对齐当前后端契约：audit-logs 仅支持 limit/cursor。
 * 操作展示去前缀英文短名；详情解析为可读摘要（含租户展示名）。
 */
export function AuditLogsTab({ planId }: AuditLogsTabProps) {
  const [limit, setLimit] = useState(20)
  const [cursorStack, setCursorStack] = useState<(string | undefined)[]>([
    undefined,
  ])
  const [pageIndex, setPageIndex] = useState(0)
  const [resultFilter, setResultFilter] = useState<string | undefined>()

  const cursor = cursorStack[pageIndex]

  const logsQuery = useQuery({
    queryKey: ['tenant-plan-audit-logs', planId, limit, cursor],
    queryFn: () => listTenantPlanAuditLogs(planId, { limit, cursor }),
    enabled: !!planId,
    retry: false,
  })

  // 历史审计若无 display_name，用当前绑定租户列表兜底展示名
  const boundQuery = useQuery({
    queryKey: ['tenant-plan-bound-tenants', planId],
    queryFn: () => listTenantPlanBoundTenants(planId),
    enabled: !!planId,
    retry: false,
  })

  const tenantNames = useMemo(() => {
    const map: Record<string, string> = {}
    for (const t of boundQuery.data?.items ?? []) {
      const label =
        t.display_name && t.display_name !== t.name
          ? `${t.display_name}（${t.name}）`
          : t.display_name || t.name
      map[t.id] = label
    }
    return map
  }, [boundQuery.data])

  const items = useMemo(() => {
    let rows = logsQuery.data?.items ?? []
    if (resultFilter) {
      rows = rows.filter((r) => r.result === resultFilter)
    }
    return rows
  }, [logsQuery.data, resultFilter])

  const columns: PrimaryTableCol<PlanAuditLog>[] = useMemo(
    () => [
      {
        colKey: 'action',
        title: '操作',
        width: 160,
        cell: ({ row }) => formatAuditAction(row.action),
      },
      {
        colKey: 'result',
        title: '结果',
        width: 100,
        cell: ({ row }) => (
          <Tag
            theme={row.result === 'success' ? 'success' : 'danger'}
            variant="light"
          >
            {row.result === 'success' ? '成功' : '失败'}
          </Tag>
        ),
      },
      {
        colKey: 'details',
        title: '详情',
        minWidth: 240,
        cell: ({ row }) =>
          formatAuditDetails(row.action, row.details, tenantNames),
      },
      {
        colKey: 'created_at',
        title: '时间',
        minWidth: 160,
        cell: ({ row }) => formatDateTime(row.created_at),
      },
    ],
    [tenantNames],
  )

  if (logsQuery.isError) {
    return (
      <Alert
        theme="error"
        message={`操作历史加载失败：${(logsQuery.error as ApiError)?.message ?? ''}`}
        operation={
          <Button variant="outline" onClick={() => logsQuery.refetch()}>
            重试
          </Button>
        }
      />
    )
  }

  const nextCursor = logsQuery.data?.next_cursor ?? null

  return (
    <div>
      <div style={{ display: 'flex', gap: 12, marginBottom: 12, flexWrap: 'wrap' }}>
        <Select
          clearable
          placeholder="全部结果"
          style={{ width: 140 }}
          value={resultFilter}
          options={[
            { label: '成功', value: 'success' },
            { label: '失败', value: 'failure' },
          ]}
          onChange={(v) => setResultFilter(v ? String(v) : undefined)}
        />
      </div>

      {!logsQuery.isLoading && items.length === 0 ? (
        <Empty description="暂无操作历史" />
      ) : (
        <Table
          data={items}
          columns={columns}
          loading={logsQuery.isLoading}
          rowKey="id"
          bordered
          size="small"
        />
      )}

      <div
        style={{
          marginTop: 12,
          display: 'flex',
          justifyContent: 'flex-end',
        }}
      >
        <Pagination
          current={pageIndex + 1}
          pageSize={limit}
          total={
            nextCursor
              ? (pageIndex + 2) * limit
              : pageIndex * limit + items.length
          }
          pageSizeOptions={[10, 20, 50, 100]}
          onChange={(pageInfo) => {
            const nextPage = pageInfo.current - 1
            if (nextPage > pageIndex && nextCursor) {
              setCursorStack((stack) => {
                const copy = stack.slice(0, pageIndex + 1)
                copy.push(nextCursor)
                return copy
              })
              setPageIndex(nextPage)
            } else if (nextPage < pageIndex && pageIndex > 0) {
              setPageIndex(nextPage)
            }
          }}
          onPageSizeChange={(size) => {
            setLimit(size)
            setCursorStack([undefined])
            setPageIndex(0)
          }}
        />
      </div>
    </div>
  )
}
