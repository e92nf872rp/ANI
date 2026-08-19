import { Button, Dialog, Form, Input, Textarea } from 'tdesign-react'
import type { TenantPlan } from '@/api/tenant-plans'

export interface EditPlanInfoValues {
  name: string
  /** 空串表示清空数据库 description */
  description: string
}

interface EditPlanInfoDialogProps {
  visible: boolean
  plan: TenantPlan | null
  submitting: boolean
  onSubmit: (values: EditPlanInfoValues) => void
  onClose: () => void
}

/** 修改套餐基本信息（name / description）；打开时回填当前值 */
export function EditPlanInfoDialog({
  visible,
  plan,
  submitting,
  onSubmit,
  onClose,
}: EditPlanInfoDialogProps) {
  const [form] = Form.useForm()

  const handleConfirm = async () => {
    const valid = await form.validate()
    if (valid !== true) return
    const values = form.getFieldsValue(true) as {
      name?: string
      description?: string
    }
    onSubmit({
      name: (values.name ?? '').trim(),
      // 保留空串：后端约定空串 = 清空 description
      description: (values.description ?? '').trim(),
    })
  }

  if (!plan) return null

  return (
    <Dialog
      visible={visible}
      header="修改套餐信息"
      width={520}
      onClose={onClose}
      destroyOnClose
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button
            theme="primary"
            loading={submitting}
            onClick={() => void handleConfirm()}
          >
            保存
          </Button>
        </>
      }
    >
      {/* key 随打开重置，保证回填当前 name / description */}
      <Form
        key={`${plan.id}-${visible ? 'open' : 'closed'}`}
        form={form}
        layout="vertical"
        disabled={submitting}
      >
        <Form.FormItem
          label="套餐名称"
          name="name"
          initialData={plan.name}
          rules={[
            { required: true, message: '请输入名称' },
            { max: 64, message: '最多 64 个字符' },
          ]}
        >
          <Input placeholder="套餐名称" maxlength={64} />
        </Form.FormItem>
        <Form.FormItem
          label="说明"
          name="description"
          initialData={plan.description ?? ''}
        >
          <Textarea
            placeholder="可清空说明"
            maxlength={512}
            autosize={{ minRows: 2, maxRows: 4 }}
          />
        </Form.FormItem>
      </Form>
    </Dialog>
  )
}
