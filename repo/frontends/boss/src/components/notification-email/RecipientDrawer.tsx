import { useEffect } from 'react'
import { Button, Drawer, Form, Input } from 'tdesign-react'
import type { SubmitContext } from 'tdesign-react'
import type { EmailRecipient } from '@/api/notifications'

const { FormItem } = Form

interface RecipientDrawerProps {
  visible: boolean
  mode: 'create' | 'edit'
  editValues: EmailRecipient | null
  submitting: boolean
  onSubmit: (values: { email: string; label?: string }) => void
  onClose: () => void
}

export function RecipientDrawer({
  visible,
  mode,
  editValues,
  submitting,
  onSubmit,
  onClose,
}: RecipientDrawerProps) {
  const [form] = Form.useForm()

  useEffect(() => {
    if (visible) {
      if (mode === 'edit' && editValues) {
        form.setFieldsValue({
          email: editValues.email,
          label: editValues.label ?? '',
        })
      } else {
        form.reset({ type: 'empty' })
      }
    }
  }, [visible, mode, editValues, form])

  const handleSubmit = (ctx: SubmitContext) => {
    const values = ctx.fields as { email: string; label?: string }
    onSubmit(values)
  }

  return (
    <Drawer
      visible={visible}
      header={mode === 'create' ? '添加收件人' : '编辑收件人'}
      size="480px"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button
            theme="primary"
            loading={submitting}
            onClick={() => form.submit()}
          >
            {mode === 'create' ? '添加' : '保存'}
          </Button>
        </>
      }
    >
      <Form
        form={form}
        layout="vertical"
        onSubmit={handleSubmit}
        resetType="empty"
      >
        <FormItem
          label="邮箱地址"
          name="email"
          rules={[
            { required: true, message: '请输入邮箱地址' },
            { email: true, message: '请输入合法的邮箱地址' },
          ]}
        >
          <Input placeholder="user@example.com" />
        </FormItem>

        <FormItem label="备注" name="label">
          <Input placeholder="可选，如：运维值班邮箱" />
        </FormItem>
      </Form>
    </Drawer>
  )
}
