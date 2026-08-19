import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Button } from 'tdesign-react'
import { ChevronLeftSIcon } from 'tdesign-icons-react'
import { canWritePlatform } from '@/auth/permissions'
import { PlanDetailPage } from '@/components/tenant-plans/PlanDetailPage'

/** 对齐产品原型-7.23：套餐详情为独立页（非 Drawer） */
export const Route = createFileRoute('/_authenticated/tenants/quotas/$planId')({
  component: TenantQuotaDetailRoute,
})

function TenantQuotaDetailRoute() {
  const { planId } = Route.useParams()
  const navigate = useNavigate()
  const canWrite = canWritePlatform()

  const goBack = () => {
    void navigate({ to: '/tenants/quotas' })
  }

  return (
    <div>
      <div style={{ marginBottom: 12 }}>
        <Button
          variant="text"
          icon={<ChevronLeftSIcon />}
          onClick={goBack}
          style={{ marginLeft: -8 }}
        >
          返回配额策略
        </Button>
      </div>
      <PlanDetailPage
        planId={planId}
        canWrite={canWrite}
        onBack={goBack}
        onDeleted={goBack}
      />
    </div>
  )
}
