import { useEffect, useRef } from 'react'
import {
  Button,
  Form,
  Input,
  InputNumber,
  Select,
  Skeleton,
  Tag,
} from 'tdesign-react'
import type { SubmitContext } from 'tdesign-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MessagePlugin } from 'tdesign-react'
import { getEmailSmtpConfig, putEmailSmtpConfig } from '@/api/notifications'
import type { PutEmailSmtpConfigRequest } from '@/api/notifications'

const { FormItem } = Form

export function SmtpForm() {
  const queryClient = useQueryClient()
  const [form] = Form.useForm()
  const formRef = useRef(false)

  const smtpQuery = useQuery({
    queryKey: ['email-smtp-config'],
    queryFn: getEmailSmtpConfig,
    retry: false,
  })

  // 回填已配置的数据
  useEffect(() => {
    if (smtpQuery.data?.configured && !formRef.current) {
      form.setFieldsValue({
        smtp_host: smtpQuery.data.smtp_host ?? '',
        smtp_port: smtpQuery.data.smtp_port ?? 465,
        encryption: smtpQuery.data.encryption ?? 'ssl',
        from_address: smtpQuery.data.from_address ?? '',
        username: smtpQuery.data.username ?? '',
        password: '',
        auth_code: '',
      })
      formRef.current = true
    }
  }, [smtpQuery.data, form])

  const saveMutation = useMutation({
    mutationFn: (values: PutEmailSmtpConfigRequest) => putEmailSmtpConfig(values),
    onSuccess: () => {
      MessagePlugin.success('发信通道已保存')
      formRef.current = false
      queryClient.invalidateQueries({ queryKey: ['email-smtp-config'] })
    },
    onError: (err: unknown) => {
      const e = err as { message?: string }
      MessagePlugin.error(`保存失败：${e?.message ?? '请稍后重试'}`)
    },
  })

  const handleSubmit = (ctx: SubmitContext) => {
    const values = ctx.fields as PutEmailSmtpConfigRequest
    // password / auth_code: 空字符串 = 清除, undefined = 不修改
    // openapi-fetch 会忽略 undefined 字段, 空字符串会发送为 ""
    const body: PutEmailSmtpConfigRequest = {
      smtp_host: values.smtp_host,
      smtp_port: values.smtp_port,
      encryption: values.encryption,
      from_address: values.from_address,
      username: values.username,
    }
    // 仅当用户输入了密码/授权码时才发送（空字符串 = 清除, 非空 = 覆盖）
    if (values.password !== undefined) {
      body.password = values.password
    }
    if (values.auth_code !== undefined) {
      body.auth_code = values.auth_code
    }
    saveMutation.mutate(body)
  }

  // loading 状态
  if (smtpQuery.isLoading) {
    return <Skeleton animation="gradient" style={{ height: 400 }} />
  }

  // forbidden
  if (smtpQuery.isError && (smtpQuery.error as { status?: number })?.status === 403) {
    return (
      <div style={{ padding: 24 }}>
        <Tag theme="warning" variant="light">当前账号仅可查看，无法修改邮件通知配置</Tag>
      </div>
    )
  }

  // api not ready
  if (smtpQuery.isError && (smtpQuery.error as { status?: number })?.status === 501) {
    return (
      <div style={{ padding: 24 }}>
        <Tag theme="warning" variant="light">邮件通知接口尚未就绪，配置暂不可保存</Tag>
      </div>
    )
  }

  const configured = smtpQuery.data?.configured ?? false

  return (
    <Form
      form={form}
      layout="vertical"
      onSubmit={handleSubmit}
      resetType="empty"
      style={{ maxWidth: 600 }}
    >
      <FormItem
        label="SMTP 主机"
        name="smtp_host"
        rules={[{ required: true, message: '请输入 SMTP 主机地址' }]}
      >
        <Input placeholder="smtp.example.com" />
      </FormItem>

      <FormItem
        label="SMTP 端口"
        name="smtp_port"
        rules={[{ required: true, message: '请输入端口号' }]}
        initialData={465}
      >
        <InputNumber min={1} max={65535} style={{ width: '100%' }} />
      </FormItem>

      <FormItem
        label="加密方式"
        name="encryption"
        rules={[{ required: true, message: '请选择加密方式' }]}
        initialData="ssl"
      >
        <Select
          options={[
            { label: 'SSL', value: 'ssl' },
            { label: 'STARTTLS', value: 'starttls' },
            { label: '无加密', value: 'none' },
          ]}
        />
      </FormItem>

      <FormItem
        label="发件人地址"
        name="from_address"
        rules={[
          { required: true, message: '请输入发件人邮箱' },
          { email: true, message: '请输入合法的邮箱地址' },
        ]}
      >
        <Input placeholder="noreply@example.com" />
      </FormItem>

      <FormItem
        label="登录账号"
        name="username"
        rules={[{ required: true, message: '请输入登录账号' }]}
      >
        <Input placeholder="user@example.com" />
      </FormItem>

      <FormItem label="登录密码" name="password">
        <Input
          type="password"
          placeholder={smtpQuery.data?.has_password ? '已配置，留空表示不修改' : '请输入密码'}
        />
      </FormItem>

      <FormItem label="授权码" name="auth_code">
        <Input
          type="password"
          placeholder={smtpQuery.data?.has_auth_code ? '已配置，留空表示不修改' : 'QQ/163 等 SMTP 授权码'}
        />
      </FormItem>

      <FormItem label="通道状态">
        {configured ? (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <Tag theme="success" variant="light">已配置</Tag>
            {smtpQuery.data?.has_password && <Tag theme="success" variant="light-outline">密码已设置</Tag>}
            {smtpQuery.data?.has_auth_code && <Tag theme="success" variant="light-outline">授权码已设置</Tag>}
          </div>
        ) : (
          <Tag theme="default" variant="light">未配置</Tag>
        )}
      </FormItem>

      <FormItem>
        <Button type="submit" theme="primary" loading={saveMutation.isPending}>
          保存通道
        </Button>
      </FormItem>
    </Form>
  )
}
