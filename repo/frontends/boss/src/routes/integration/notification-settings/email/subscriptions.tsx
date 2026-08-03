import { createFileRoute } from '@tanstack/react-router'
import { Alert, Button } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { SubscriptionTable } from '@/components/notification-email/SubscriptionTable'
import { listEmailSubscriptions } from '@/api/notifications'

export const Route = createFileRoute('/integration/notification-settings/email/subscriptions')({
  component: SubscriptionsPage,
})

function SubscriptionsPage() {
  const subscriptionsQuery = useQuery({
    queryKey: ['email-subscriptions'],
    queryFn: listEmailSubscriptions,
    retry: false,
  })

  // error 状态
  if (subscriptionsQuery.isError && (subscriptionsQuery.error as { status?: number })?.status !== 403 && (subscriptionsQuery.error as { status?: number })?.status !== 501) {
    return (
      <div>
        <Alert
          theme="error"
          message={`数据加载失败：${(subscriptionsQuery.error as { message?: string })?.message ?? ''}`}
          operation={
            <Button variant="outline" onClick={() => subscriptionsQuery.refetch()}>重试</Button>
          }
        />
      </div>
    )
  }

  return (
    <div>
      {/* Page Header */}
      <div style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>事件订阅</h2>
        <p style={{ margin: '4px 0 0 0', color: 'var(--td-text-color-secondary)', fontSize: 14 }}>
          邮件通知 · 选择哪些平台事件发送邮件
        </p>
      </div>

      {/* Subscription Table */}
      <SubscriptionTable />
    </div>
  )
}
