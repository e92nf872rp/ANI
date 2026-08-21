import {
  Alert,
  Button,
  Drawer,
  Form,
  InputNumber,
  MessagePlugin,
  Select,
} from 'tdesign-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { coreApi, newIdempotencyKey } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

type ReservationView = components['schemas']['ReservationView']

// 配额分配 Drawer（UX §4.2 / §8.3 假设：配额/预留合为一个 Drawer 两个 InputNumber 字段）
// 提交时串行调用 PUT /admin/tenants/{tenant_id}/quota + PUT /admin/tenants/{tenant_id}/reservations
// 下调 clamp 时显示 Message.success "配额已下调至 N 卡（实际使用量），差额无法回收"
interface GpuPoolQuotaDrawerProps {
  visible: boolean
  tenantOptions: { label: string; value: string }[]
  tenantId: string
  onTenantChange: (v: string) => void
  // 当前租户已有 gpu_count 配额上限，用于回填
  currentTotal: number
  onClose: () => void
}

export function GpuPoolQuotaDrawer({
  visible,
  tenantOptions,
  tenantId,
  onTenantChange,
  currentTotal,
  onClose,
}: GpuPoolQuotaDrawerProps) {
  const [form] = Form.useForm()
  const qc = useQueryClient()

  // Drawer 打开时回填已有配额；visible/tenantId/currentTotal 变化时重新设置
  useEffect(() => {
    if (visible) {
      form.setFieldsValue({
        tenant_id: tenantId,
        total: currentTotal,
        allocated_gpu_count: 0,
      })
    }
  }, [visible, tenantId, currentTotal, form])

  // 配额 + 预留分配 mutation（同 Drawer 提交，两个 PUT 串行）
  const quotaMutation = useMutation({
    mutationFn: async (vars: {
      tenant_id: string
      total: number
      allocated_gpu_count: number
    }) => {
      const idempotencyKey = newIdempotencyKey()
      // 1. 设配额上限 total（resource_type=gpu_count）
      const { error: quotaErr } = await coreApi.PUT('/admin/tenants/{tenant_id}/quota', {
        params: {
          header: { 'Idempotency-Key': idempotencyKey },
          path: { tenant_id: vars.tenant_id },
        },
        body: {
          items: [{ resource_type: 'gpu_count', total: vars.total }],
        },
      })
      if (quotaErr) throw new Error(quotaErr?.message ?? '配额分配失败')
      // 2. 设预留额度 allocated_gpu_count
      const { data: reservationData, error: reservationErr } = await coreApi.PUT(
        '/admin/tenants/{tenant_id}/reservations',
        {
          params: {
            header: { 'Idempotency-Key': newIdempotencyKey() },
            path: { tenant_id: vars.tenant_id },
          },
          body: { allocated_gpu_count: vars.allocated_gpu_count },
        },
      )
      if (reservationErr) throw new Error(reservationErr?.message ?? '预留额度设置失败')
      return reservationData as ReservationView | undefined
    },
    onSuccess: (reservation) => {
      if (reservation?.tightened) {
        MessagePlugin.success(
          `配额已下调至 ${reservation.allocated_gpu_count} 卡（实际使用量），差额无法回收`,
        )
      } else {
        MessagePlugin.success('配额分配成功')
      }
      qc.invalidateQueries({ queryKey: ['boss-quotas'] })
      onClose()
    },
    onError: (err: Error) => {
      MessagePlugin.error(err.message || '配额分配失败')
    },
  })

  return (
    <Drawer
      visible={visible}
      header="配额分配"
      size="480px"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button theme="primary" loading={quotaMutation.isPending} onClick={() => form.submit()}>
            确定
          </Button>
        </>
      }
    >
      <Form
        form={form}
        layout="vertical"
        onSubmit={(ctx) => {
          const values = ctx.fields as {
            tenant_id: string
            total: number
            allocated_gpu_count: number
          }
          if (!values.tenant_id) {
            MessagePlugin.warning('请选择租户')
            return
          }
          if (values.allocated_gpu_count > values.total) {
            MessagePlugin.warning('预留额度不能超过配额上限')
            return
          }
          quotaMutation.mutate({
            tenant_id: values.tenant_id,
            total: values.total,
            allocated_gpu_count: values.allocated_gpu_count,
          })
        }}
        resetType="empty"
      >
        <Form.FormItem
          label="选择租户"
          name="tenant_id"
          rules={[{ required: true, message: '请选择租户' }]}
        >
          <Select
            filterable
            clearable
            placeholder="搜索租户"
            options={tenantOptions}
            onChange={(v) => onTenantChange(v as string)}
          />
        </Form.FormItem>

        <Form.FormItem
          label="配额上限 (卡数)"
          name="total"
          rules={[{ required: true, message: '请填写配额上限' }]}
        >
          <InputNumber min={0} step={1} placeholder="resource_quota.total" />
        </Form.FormItem>

        <Form.FormItem
          label="预留额度 (卡数)"
          name="allocated_gpu_count"
          rules={[
            { required: true, message: '请填写预留额度' },
            {
              validator: (val: number) =>
                val <= (form.getFieldValue('total') ?? 0)
                  ? Promise.resolve(true)
                  : Promise.reject('预留额度不能超过配额上限'),
            },
          ]}
        >
          <InputNumber min={0} step={1} placeholder="<= 配额上限，单维度不分 spec" />
        </Form.FormItem>

        <Alert
          theme="info"
          message="下调配额或预留时，服务端会自动 clamp 到已使用量（used+reserved），差额无法回收。"
          style={{ marginTop: 8 }}
        />
      </Form>
    </Drawer>
  )
}
