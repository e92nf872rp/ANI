import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button, MessagePlugin } from 'tdesign-react'
import { ChevronLeftSIcon } from 'tdesign-icons-react'
import { createTenantPlan } from '@/api/tenant-plans'
import type { ApiError } from '@/api/tenant-plans'
import {
  CreatePlanWizard,
  type CreatePlanFormValues,
} from '@/components/tenant-plans/CreatePlanWizard'

/** 对齐产品原型-7.23：新建套餐为独立创建向导页 */
export const Route = createFileRoute('/_authenticated/tenants/quotas/new')({
  component: CreateTenantQuotaPage,
})

function CreateTenantQuotaPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const goBack = () => {
    void navigate({ to: '/tenants/quotas' })
  }

  const createMutation = useMutation({
    mutationFn: (values: CreatePlanFormValues) => createTenantPlan(values),
    onSuccess: () => {
      MessagePlugin.success('套餐已创建')
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
      goBack()
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'PLAN_CODE_CONFLICT' || e.status === 409) {
        MessagePlugin.error('套餐代码已存在，请更换')
        return
      }
      if (e.code === 'QUOTA_RESOURCE_NOT_REGISTERED' || e.status === 422) {
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

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <Button
          variant="text"
          icon={<ChevronLeftSIcon />}
          onClick={goBack}
          style={{ marginLeft: -8, marginBottom: 8 }}
        >
          返回配额策略
        </Button>
        <h2 style={{ margin: 0 }}>新建套餐</h2>
        <p
          style={{
            margin: '4px 0 0 0',
            color: 'var(--td-text-color-secondary)',
            fontSize: 14,
          }}
        >
          名称与编码 → 限额配置 → 确认创建
        </p>
      </div>

      <CreatePlanWizard
        submitting={createMutation.isPending}
        onSubmit={(values) => createMutation.mutate(values)}
        onCancel={goBack}
      />
    </div>
  )
}
