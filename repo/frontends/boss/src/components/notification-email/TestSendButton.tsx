import { useState } from 'react'
import { Button, MessagePlugin } from 'tdesign-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { sendTestEmail } from '@/api/notifications'

interface TestSendButtonProps {
  disabled: boolean
}

export function TestSendButton({ disabled }: TestSendButtonProps) {
  const queryClient = useQueryClient()
  const [sending, setSending] = useState(false)

  const sendMutation = useMutation({
    mutationFn: sendTestEmail,
    onMutate: () => {
      console.log('[email-test] sending test email...')
      setSending(true)
    },
    onSuccess: (data) => {
      console.log('[email-test] response:', data)
      if (data.success) {
        MessagePlugin.success('测试邮件已发送，请查收启用中的收件邮箱')
      } else {
        MessagePlugin.error(`测试发送失败：${data.message}（请求 ID：${data.request_id}）`)
      }
      queryClient.invalidateQueries({ queryKey: ['email-smtp-config'] })
    },
    onError: (err: unknown) => {
      const e = err as { status?: number; message?: string }
      console.error('[email-test] error:', e)
      if (e?.status === 422) {
        MessagePlugin.warning('请先完成发信通道配置，并在「收件邮箱」中添加至少一个启用的收件人')
      } else {
        MessagePlugin.error(`测试发送失败：${e?.message ?? '请稍后重试'}`)
      }
    },
    onSettled: () => setSending(false),
  })

  return (
    <Button
      variant="outline"
      loading={sending}
      disabled={disabled}
      onClick={() => sendMutation.mutate()}
    >
      发送测试邮件
    </Button>
  )
}
