import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Empty, MessagePlugin } from 'tdesign-react'
import { AddIcon } from 'tdesign-icons-react'
import { RecipientTable } from '@/components/notification-email/RecipientTable'
import { RecipientDrawer } from '@/components/notification-email/RecipientDrawer'
import {
  listEmailRecipients,
  createEmailRecipient,
  updateEmailRecipient,
  deleteEmailRecipient,
} from '@/api/notifications'
import type { EmailRecipient } from '@/api/notifications'

export const Route = createFileRoute('/integration/notification-settings/email/recipients')({
  component: RecipientsPage,
})

function RecipientsPage() {
  const queryClient = useQueryClient()
  const [drawerVisible, setDrawerVisible] = useState(false)
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create')
  const [editValues, setEditValues] = useState<EmailRecipient | null>(null)

  const recipientsQuery = useQuery({
    queryKey: ['email-recipients'],
    queryFn: listEmailRecipients,
    retry: false,
  })

  const createMutation = useMutation({
    mutationFn: (values: { email: string; label?: string }) => createEmailRecipient(values),
    onSuccess: () => {
      MessagePlugin.success('收件人已保存')
      setDrawerVisible(false)
      queryClient.invalidateQueries({ queryKey: ['email-recipients'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`保存失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, values }: { id: string; values: { email?: string; label?: string } }) =>
      updateEmailRecipient(id, values),
    onSuccess: () => {
      MessagePlugin.success('收件人已保存')
      setDrawerVisible(false)
      queryClient.invalidateQueries({ queryKey: ['email-recipients'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`保存失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      updateEmailRecipient(id, { enabled }),
    onSuccess: (_data, variables) => {
      MessagePlugin.success(variables.enabled ? '收件人已启用' : '收件人已停用')
      queryClient.invalidateQueries({ queryKey: ['email-recipients'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`操作失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteEmailRecipient(id),
    onSuccess: () => {
      MessagePlugin.success('收件人已删除')
      queryClient.invalidateQueries({ queryKey: ['email-recipients'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`删除失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const handleAdd = () => {
    setDrawerMode('create')
    setEditValues(null)
    setDrawerVisible(true)
  }

  const handleEdit = (recipient: EmailRecipient) => {
    setDrawerMode('edit')
    setEditValues(recipient)
    setDrawerVisible(true)
  }

  const handleToggleEnabled = (recipient: EmailRecipient) => {
    toggleMutation.mutate({ id: recipient.id, enabled: !recipient.enabled })
  }

  const handleDelete = (recipient: EmailRecipient) => {
    deleteMutation.mutate(recipient.id)
  }

  const handleDrawerSubmit = (values: { email: string; label?: string }) => {
    if (drawerMode === 'create') {
      createMutation.mutate(values)
    } else if (editValues) {
      updateMutation.mutate({ id: editValues.id, values })
    }
  }

  const submitting = createMutation.isPending || updateMutation.isPending
  const recipients = recipientsQuery.data?.items ?? []

  // error 状态
  if (recipientsQuery.isError && (recipientsQuery.error as { status?: number })?.status !== 403 && (recipientsQuery.error as { status?: number })?.status !== 501) {
    return (
      <div>
        <Alert
          theme="error"
          message={`数据加载失败：${(recipientsQuery.error as { message?: string })?.message ?? ''}`}
          operation={
            <Button variant="outline" onClick={() => recipientsQuery.refetch()}>重试</Button>
          }
        />
      </div>
    )
  }

  return (
    <div>
      {/* Page Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div>
          <h2 style={{ margin: 0 }}>收件邮箱</h2>
          <p style={{ margin: '4px 0 0 0', color: 'var(--td-text-color-secondary)', fontSize: 14 }}>
            邮件通知 · 管理全局收件人；已开启订阅的事件将发往所有「启用」状态的收件邮箱
          </p>
        </div>
        <Button theme="primary" icon={<AddIcon />} onClick={handleAdd}>
          添加收件人
        </Button>
      </div>

      {/* 空态 */}
      {!recipientsQuery.isLoading && recipients.length === 0 && !recipientsQuery.isError ? (
        <Empty
          description="暂无收件人"
          action={
            <Button theme="primary" icon={<AddIcon />} onClick={handleAdd}>
              添加收件人
            </Button>
          }
        />
      ) : (
        <RecipientTable
          data={recipients}
          loading={recipientsQuery.isLoading}
          onEdit={handleEdit}
          onToggleEnabled={handleToggleEnabled}
          onDelete={handleDelete}
        />
      )}

      {/* Drawer */}
      <RecipientDrawer
        visible={drawerVisible}
        mode={drawerMode}
        editValues={editValues}
        submitting={submitting}
        onSubmit={handleDrawerSubmit}
        onClose={() => setDrawerVisible(false)}
      />
    </div>
  )
}
