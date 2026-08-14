import { useEffect, useState } from 'react'
import {
  Alert,
  Button,
  MessagePlugin,
  Popconfirm,
  Skeleton,
  Tabs,
} from 'tdesign-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  activateTenantPlan,
  deleteTenantPlan,
  disableTenantPlan,
  getTenantPlan,
  updateTenantPlan,
} from '@/api/tenant-plans'
import type { ApiError } from '@/api/tenant-plans'
import { PlanStatusTag, formatDateTime } from './planStatus'
import { QuotaLimitsTab } from './QuotaLimitsTab'
import { BoundTenantsTab } from './BoundTenantsTab'
import { AuditLogsTab } from './AuditLogsTab'
import {
  EditPlanInfoDialog,
  type EditPlanInfoValues,
} from './EditPlanInfoDialog'

interface PlanDetailPageProps {
  planId: string
  canWrite: boolean
  initialTab?: string
  onBack: () => void
  /** 删除成功后回列表 */
  onDeleted: () => void
}

/** 对齐产品原型-7.23：详情整页（概览 / 限额明细 / 绑定租户 / 操作历史） */
export function PlanDetailPage({
  planId,
  canWrite,
  initialTab = 'overview',
  onBack,
  onDeleted,
}: PlanDetailPageProps) {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState(initialTab)
  const [editVisible, setEditVisible] = useState(false)

  useEffect(() => {
    setTab(initialTab)
  }, [planId, initialTab])

  const planQuery = useQuery({
    queryKey: ['tenant-plan', planId],
    queryFn: () => getTenantPlan(planId),
    enabled: !!planId,
    retry: false,
  })

  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
    queryClient.invalidateQueries({ queryKey: ['tenant-plan', planId] })
  }

  const activateMutation = useMutation({
    mutationFn: () => activateTenantPlan(planId),
    onSuccess: () => {
      MessagePlugin.success('套餐已发布')
      invalidateAll()
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'PLAN_STATE_INVALID' || e.status === 409) {
        MessagePlugin.error('套餐状态不允许发布')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const disableMutation = useMutation({
    mutationFn: () => disableTenantPlan(planId),
    onSuccess: () => {
      MessagePlugin.success('套餐已停用')
      invalidateAll()
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'PLAN_STATE_INVALID' || e.status === 409) {
        MessagePlugin.error('草稿状态不可直接停用')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteTenantPlan(planId),
    onSuccess: () => {
      MessagePlugin.success('套餐已删除')
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
      onDeleted()
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'TENANT_PLAN_IN_USE' || e.status === 409) {
        MessagePlugin.error('该套餐已关联租户，不可删除')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const updateInfoMutation = useMutation({
    mutationFn: (values: EditPlanInfoValues) =>
      // description 始终传字符串：空串表示清空库中 description（不可省略该字段）
      updateTenantPlan(planId, {
        name: values.name,
        description: values.description ?? '',
      }),
    onSuccess: () => {
      MessagePlugin.success('套餐信息已更新')
      setEditVisible(false)
      invalidateAll()
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.status === 404) {
        MessagePlugin.error('套餐不存在或已删除')
        return
      }
      if (e.status === 400) {
        MessagePlugin.error(`校验失败：${e.message ?? ''}`)
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const plan = planQuery.data

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ margin: 0 }}>
          {plan
            ? `套餐详情 - ${plan.name}（${plan.code}）`
            : '套餐详情'}
        </h2>
        <p
          style={{
            margin: '4px 0 0 0',
            color: 'var(--td-text-color-secondary)',
            fontSize: 14,
          }}
        >
          概览 · 限额明细 · 绑定租户 · 操作历史
        </p>
      </div>

      {planQuery.isLoading && (
        <Skeleton animation="gradient" style={{ height: 240 }} />
      )}

      {planQuery.isError && (
        <Alert
          theme="error"
          message={`详情加载失败：${(planQuery.error as ApiError)?.message ?? ''}`}
          operation={
            <div style={{ display: 'flex', gap: 8 }}>
              <Button variant="outline" onClick={() => planQuery.refetch()}>
                重试
              </Button>
              <Button variant="outline" onClick={onBack}>
                返回
              </Button>
            </div>
          }
        />
      )}

      {!planQuery.isLoading && !planQuery.isError && !plan && (
        <Alert
          theme="warning"
          message="套餐不存在或已删除"
          operation={
            <Button variant="outline" onClick={onBack}>
              返回
            </Button>
          }
        />
      )}

      {plan && (
        <div>
          {canWrite && (
            <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
              <Button variant="outline" onClick={() => setEditVisible(true)}>
                修改信息
              </Button>
              {(plan.status === 'draft' || plan.status === 'disabled') && (
                <Popconfirm
                  content="确认发布该套餐？发布后可被新租户引用。"
                  onConfirm={() => activateMutation.mutate()}
                >
                  <Button theme="primary" loading={activateMutation.isPending}>
                    发布
                  </Button>
                </Popconfirm>
              )}
              {plan.status === 'active' && (
                <Popconfirm
                  content="停用后不可被新租户引用，已绑定租户不受影响。确认停用？"
                  onConfirm={() => disableMutation.mutate()}
                >
                  <Button variant="outline" loading={disableMutation.isPending}>
                    停用
                  </Button>
                </Popconfirm>
              )}
              <Popconfirm
                content="删除后套餐编码可被新套餐复用。此操作不可撤销，确认删除？"
                theme="danger"
                onConfirm={() => deleteMutation.mutate()}
              >
                <Button theme="danger" loading={deleteMutation.isPending}>
                  删除
                </Button>
              </Popconfirm>
            </div>
          )}

          <Tabs value={tab} onChange={(v) => setTab(String(v))}>
            <Tabs.TabPanel value="overview" label="概览">
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '100px 1fr',
                  rowGap: 8,
                  columnGap: 8,
                  fontSize: 14,
                }}
              >
                <div style={{ color: 'var(--td-text-color-secondary)' }}>编码</div>
                <div>{plan.code}</div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>套餐</div>
                <div>{plan.name}</div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>说明</div>
                <div>{plan.description?.trim() ? plan.description : '—'}</div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>状态</div>
                <div>
                  <PlanStatusTag status={plan.status} />
                </div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>绑定租户</div>
                <div>{plan.tenant_count}</div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>创建时间</div>
                <div>{formatDateTime(plan.created_at)}</div>
                <div style={{ color: 'var(--td-text-color-secondary)' }}>更新时间</div>
                <div>{formatDateTime(plan.updated_at)}</div>
              </div>
            </Tabs.TabPanel>
            <Tabs.TabPanel value="quota-limits" label="限额明细">
              <QuotaLimitsTab
                planId={plan.id}
                canWrite={canWrite}
                syncedTenantCount={plan.tenant_count}
              />
            </Tabs.TabPanel>
            <Tabs.TabPanel value="bound-tenants" label="绑定租户">
              <BoundTenantsTab
                planId={plan.id}
                planActive={plan.status === 'active'}
                canWrite={canWrite}
              />
            </Tabs.TabPanel>
            <Tabs.TabPanel value="audit-logs" label="操作历史">
              <AuditLogsTab planId={plan.id} />
            </Tabs.TabPanel>
          </Tabs>

          <EditPlanInfoDialog
            visible={editVisible}
            plan={plan}
            submitting={updateInfoMutation.isPending}
            onSubmit={(values) => updateInfoMutation.mutate(values)}
            onClose={() => {
              if (!updateInfoMutation.isPending) setEditVisible(false)
            }}
          />
        </div>
      )}
    </div>
  )
}
