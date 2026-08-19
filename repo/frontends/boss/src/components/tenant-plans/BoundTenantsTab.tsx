import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Empty,
  MessagePlugin,
  Select,
  Table,
  Tag,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  bindTenantPlan,
  listBindableTenants,
  listTenantPlanBoundTenants,
} from '@/api/tenant-plans'
import type { ApiError, BoundTenant } from '@/api/tenant-plans'

interface BoundTenantsTabProps {
  planId: string
  planActive: boolean
  canWrite: boolean
}

const TENANT_STATUS_THEME: Record<
  BoundTenant['status'],
  'success' | 'warning' | 'danger'
> = {
  active: 'success',
  frozen: 'warning',
  disabled: 'danger',
}

const TENANT_STATUS_LABEL: Record<BoundTenant['status'], string> = {
  active: '活跃',
  frozen: '冻结',
  disabled: '停用',
}

/** 页内下拉选择可绑定租户，点「分配」直接绑定（无弹窗） */
export function BoundTenantsTab({
  planId,
  planActive,
  canWrite,
}: BoundTenantsTabProps) {
  const queryClient = useQueryClient()
  const [tenantId, setTenantId] = useState<string | undefined>()

  const boundQuery = useQuery({
    queryKey: ['tenant-plan-bound-tenants', planId],
    queryFn: () => listTenantPlanBoundTenants(planId),
    enabled: !!planId,
    retry: false,
  })

  const bindableQuery = useQuery({
    queryKey: ['bindable-tenants', planId],
    queryFn: () => listBindableTenants(planId),
    enabled: !!planId && canWrite && planActive,
    retry: false,
  })

  const bindMutation = useMutation({
    mutationFn: (id: string) => bindTenantPlan(id, planId),
    onSuccess: () => {
      MessagePlugin.success('套餐已绑定，配额已更新')
      setTenantId(undefined)
      queryClient.invalidateQueries({
        queryKey: ['tenant-plan-bound-tenants', planId],
      })
      queryClient.invalidateQueries({ queryKey: ['tenant-plan', planId] })
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
      queryClient.invalidateQueries({ queryKey: ['bindable-tenants', planId] })
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.status === 404) {
        MessagePlugin.error('套餐不存在或未发布')
        return
      }
      if (e.code === 'TENANT_STATE_INVALID' || e.status === 409) {
        MessagePlugin.error('租户已停用，不可绑定套餐')
        return
      }
      if (e.code === 'PLAN_NOT_ACTIVE') {
        MessagePlugin.error('套餐未发布，不可被租户引用')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const options = useMemo(
    () =>
      (bindableQuery.data?.items ?? []).map((t) => ({
        value: t.id,
        label: `${t.display_name || t.name}（${t.name}）· ${TENANT_STATUS_LABEL[t.status] ?? t.status}`,
      })),
    [bindableQuery.data],
  )

  const columns: PrimaryTableCol<BoundTenant>[] = useMemo(
    () => [
      { colKey: 'name', title: '租户标识', minWidth: 140 },
      { colKey: 'display_name', title: '显示名', minWidth: 140 },
      {
        colKey: 'status',
        title: '状态',
        width: 100,
        cell: ({ row }) => (
          <Tag theme={TENANT_STATUS_THEME[row.status]} variant="light">
            {TENANT_STATUS_LABEL[row.status]}
          </Tag>
        ),
      },
    ],
    [],
  )

  const handleAssign = () => {
    if (!planActive) {
      MessagePlugin.warning('套餐未发布，不可被租户引用')
      return
    }
    if (!tenantId) {
      MessagePlugin.warning('请选择租户')
      return
    }
    bindMutation.mutate(tenantId)
  }

  if (boundQuery.isError) {
    return (
      <Alert
        theme="error"
        message={`绑定租户加载失败：${(boundQuery.error as ApiError)?.message ?? ''}`}
        operation={
          <Button variant="outline" onClick={() => boundQuery.refetch()}>
            重试
          </Button>
        }
      />
    )
  }

  const items = boundQuery.data?.items ?? []

  return (
    <div>
      {canWrite && (
        <div
          style={{
            display: 'flex',
            gap: 8,
            marginBottom: 12,
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          <Select
            filterable
            clearable
            placeholder={
              planActive ? '选择可绑定租户' : '仅已发布套餐可分配'
            }
            options={options}
            value={tenantId}
            disabled={!planActive || bindMutation.isPending}
            loading={bindableQuery.isLoading}
            empty={
              bindableQuery.isError ? '加载失败' : '暂无可绑定租户'
            }
            onChange={(v) => setTenantId(v ? String(v) : undefined)}
            style={{ width: 360, maxWidth: '100%' }}
          />
          <Button
            theme="primary"
            loading={bindMutation.isPending}
            disabled={!planActive}
            onClick={handleAssign}
          >
            分配
          </Button>
          {bindableQuery.isError && planActive && (
            <Button
              variant="text"
              size="small"
              onClick={() => void bindableQuery.refetch()}
            >
              重新加载
            </Button>
          )}
        </div>
      )}

      {!boundQuery.isLoading && items.length === 0 ? (
        <Empty description="未绑定租户" />
      ) : (
        <Table
          data={items}
          columns={columns}
          loading={boundQuery.isLoading}
          rowKey="id"
          bordered
          size="small"
        />
      )}
    </div>
  )
}
