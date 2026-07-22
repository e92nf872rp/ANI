import { useEffect, useState } from 'react'
import { Button, Skeleton, Switch, Table } from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { MessagePlugin } from 'tdesign-react'
import { listEmailSubscriptions, putEmailSubscriptions } from '@/api/notifications'
import type { EmailSubscription, PutEmailSubscriptionsRequest } from '@/api/notifications'

export function SubscriptionTable() {
  const queryClient = useQueryClient()
  const [localData, setLocalData] = useState<EmailSubscription[]>([])
  const [dirty, setDirty] = useState(false)

  const subscriptionsQuery = useQuery({
    queryKey: ['email-subscriptions'],
    queryFn: listEmailSubscriptions,
    retry: false,
  })

  // 同步 API 数据到 local state
  useEffect(() => {
    if (subscriptionsQuery.data?.items) {
      setLocalData(subscriptionsQuery.data.items)
      setDirty(false)
    }
  }, [subscriptionsQuery.data])

  const saveMutation = useMutation({
    mutationFn: (body: PutEmailSubscriptionsRequest) => putEmailSubscriptions(body),
    onSuccess: () => {
      MessagePlugin.success('事件订阅已保存')
      queryClient.invalidateQueries({ queryKey: ['email-subscriptions'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`保存失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const handleSwitchChange = (eventType: string, enabled: boolean) => {
    setLocalData((prev) =>
      prev.map((item) =>
        item.event_type === eventType ? { ...item, enabled } : item,
      ),
    )
    setDirty(true)
  }

  const handleSave = () => {
    const body: PutEmailSubscriptionsRequest = {
      subscriptions: localData.map((item) => ({
        event_type: item.event_type,
        enabled: item.enabled,
      })),
    }
    saveMutation.mutate(body)
  }

  const columns: PrimaryTableCol<EmailSubscription>[] = [
    {
      colKey: 'event_type',
      title: '事件名称',
      minWidth: 200,
      cell: ({ row }) => {
        const labels: Record<string, string> = {
          platform_alert_p0: '平台告警 P0',
          platform_alert_p1: '平台告警 P1',
          incident_created: 'Incident 创建',
          incident_escalated: 'Incident 升级',
          platform_task_failed: '平台关键任务失败',
        }
        return labels[row.event_type] ?? row.event_type
      },
    },
    {
      colKey: 'description',
      title: '说明',
      minWidth: 200,
    },
    {
      colKey: 'enabled',
      title: '邮件通知',
      width: 120,
      cell: ({ row }) => (
        <Switch
          value={row.enabled}
          onChange={(val) => handleSwitchChange(row.event_type, val as boolean)}
          disabled={saveMutation.isPending}
        />
      ),
    },
  ]

  if (subscriptionsQuery.isLoading) {
    return <Skeleton animation="gradient" style={{ height: 300 }} />
  }

  return (
    <div>
      <Table
        data={localData}
        columns={columns}
        rowKey="event_type"
        bordered
        loading={saveMutation.isPending}
      />
      <div style={{ marginTop: 16, display: 'flex', justifyContent: 'flex-end' }}>
        <Button
          theme="primary"
          loading={saveMutation.isPending}
          disabled={!dirty}
          onClick={handleSave}
        >
          保存订阅
        </Button>
      </div>
    </div>
  )
}
