import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Empty,
  InputNumber,
  MessagePlugin,
  Skeleton,
  Table,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getTenantPlanQuotaLimits,
  updateTenantPlanQuotaLimits,
} from '@/api/tenant-plans'
import type { ApiError, PlanQuotaLimitView } from '@/api/tenant-plans'
import { sortQuotaItemsByResourceType } from './quotaResourceOrder'

interface QuotaLimitsTabProps {
  planId: string
  canWrite: boolean
  syncedTenantCount?: number
}

export function QuotaLimitsTab({
  planId,
  canWrite,
  syncedTenantCount = 0,
}: QuotaLimitsTabProps) {
  const queryClient = useQueryClient()
  const [draftTotals, setDraftTotals] = useState<Record<string, number | null>>(
    {},
  )

  const limitsQuery = useQuery({
    queryKey: ['tenant-plan-quota-limits', planId],
    queryFn: () => getTenantPlanQuotaLimits(planId),
    enabled: !!planId,
    retry: false,
  })

  useEffect(() => {
    const items = limitsQuery.data?.items ?? []
    const next: Record<string, number | null> = {}
    for (const item of items) {
      next[item.resource_type] = item.total
    }
    setDraftTotals(next)
  }, [limitsQuery.data])

  const updateMutation = useMutation({
    mutationFn: () => {
      const items = (limitsQuery.data?.items ?? []).map((item) => {
        const draft = draftTotals[item.resource_type]
        return {
          resource_type: item.resource_type,
          // 清空输入框传 null（后端用 default_quota），不回落为 0
          total: draft === undefined ? item.total : draft,
        }
      })
      return updateTenantPlanQuotaLimits(planId, items)
    },
    onSuccess: () => {
      MessagePlugin.success(
        `限额已修改，已同步 ${syncedTenantCount} 个存量租户`,
      )
      queryClient.invalidateQueries({ queryKey: ['tenant-plan-quota-limits', planId] })
      queryClient.invalidateQueries({ queryKey: ['tenant-plan', planId] })
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.status === 422) {
        MessagePlugin.error('配额维度未注册或已禁用')
        return
      }
      if (e.status === 400) {
        MessagePlugin.error(`校验失败：${e.message ?? ''}`)
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const columns: PrimaryTableCol<PlanQuotaLimitView>[] = useMemo(
    () => [
      { colKey: 'resource_type', title: '维度', minWidth: 140 },
      { colKey: 'display_name', title: '展示名', minWidth: 120 },
      {
        colKey: 'total',
        title: '限额',
        width: 240,
        cell: ({ row }) => {
          const draft = draftTotals[row.resource_type]
          const inputValue =
            draft === null || draft === undefined ? undefined : draft
          return canWrite ? (
            <InputNumber
              theme="normal"
              style={{ width: 200 }}
              min={0}
              step={1}
              value={inputValue}
              onChange={(v) => {
                setDraftTotals((prev) => ({
                  ...prev,
                  [row.resource_type]:
                    v === undefined || v === null || v === ''
                      ? null
                      : Number(v),
                }))
              }}
            />
          ) : (
            row.total
          )
        },
      },
      { colKey: 'unit', title: '单位', width: 80 },
    ],
    [canWrite, draftTotals],
  )

  if (limitsQuery.isLoading) {
    return <Skeleton animation="gradient" style={{ height: 160 }} />
  }

  if (limitsQuery.isError) {
    return (
      <Alert
        theme="error"
        message={`限额加载失败：${(limitsQuery.error as ApiError)?.message ?? ''}`}
        operation={
          <Button variant="outline" onClick={() => limitsQuery.refetch()}>
            重试
          </Button>
        }
      />
    )
  }

  const items = sortQuotaItemsByResourceType(limitsQuery.data?.items ?? [])
  if (items.length === 0) {
    return <Empty description="暂无配额限额" />
  }

  return (
    <div>
      <Alert
        theme="info"
        style={{ marginBottom: 12 }}
        message="修改后自动同步已绑定该套餐的存量租户。已审批通过的配额变更申请维度将保留不覆盖。"
      />
      <Table
        data={items}
        columns={columns}
        rowKey="resource_type"
        bordered
        size="small"
      />
      {canWrite && (
        <div style={{ marginTop: 16, textAlign: 'right' }}>
          <Button
            theme="primary"
            loading={updateMutation.isPending}
            onClick={() => updateMutation.mutate()}
          >
            保存并同步绑定租户
          </Button>
        </div>
      )}
    </div>
  )
}
