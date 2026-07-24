import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Tooltip } from 'tdesign-react'
import { createFileRoute } from '@tanstack/react-router'
import { SmtpForm } from '@/components/notification-email/SmtpForm'
import { TestSendButton } from '@/components/notification-email/TestSendButton'
import { getEmailSmtpConfig, listEmailRecipients } from '@/api/notifications'

export const Route = createFileRoute('/integration/notification-settings/email/smtp')({
  component: SmtpPage,
})

function SmtpPage() {
  // 获取 SMTP 配置和收件人列表，用于判断测试发送前置条件
  const smtpQuery = useQuery({
    queryKey: ['email-smtp-config'],
    queryFn: getEmailSmtpConfig,
    retry: false,
  })

  const recipientsQuery = useQuery({
    queryKey: ['email-recipients'],
    queryFn: listEmailRecipients,
    retry: false,
  })

  const smtpConfigured = smtpQuery.data?.configured ?? false
  const hasCredentials = (smtpQuery.data?.has_password ?? false) || (smtpQuery.data?.has_auth_code ?? false)
  const hasEnabledRecipients = (recipientsQuery.data?.items ?? []).some((r) => r.enabled)
  const testReady = smtpConfigured && hasCredentials && hasEnabledRecipients

  // error 状态
  if (smtpQuery.isError && (smtpQuery.error as { status?: number })?.status !== 403 && (smtpQuery.error as { status?: number })?.status !== 501) {
    return (
      <div>
        <Alert
          theme="error"
          message={`数据加载失败：${(smtpQuery.error as { message?: string })?.message ?? ''}`}
          operation={
            <Button variant="outline" onClick={() => smtpQuery.refetch()}>重试</Button>
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
          <h2 style={{ margin: 0 }}>发信设置</h2>
          <p style={{ margin: '4px 0 0 0', color: 'var(--td-text-color-secondary)', fontSize: 14 }}>
            邮件通知 · 配置平台 SMTP 发信通道
          </p>
        </div>
        <Tooltip
          content="请先配置发信通道，并在「收件邮箱」中添加至少一个启用的收件人"
          disabled={testReady}
        >
          <TestSendButton disabled={!testReady} />
        </Tooltip>
      </div>

      {/* 边界 Alert */}
      <Alert
        theme="info"
        message="本页用于邮件通知。企业微信/钉钉 Bot 请在「企业通知集成」中配置。"
        style={{ marginBottom: 16 }}
        close
      />

      {/* Form */}
      <SmtpForm />
    </div>
  )
}
