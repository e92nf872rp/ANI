import { useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  Button,
  Dialog,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Radio,
  Select,
} from 'tdesign-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

type CreateInstanceRequest = components['schemas']['CreateInstanceRequest']
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']

export interface CreateGpuContainerDialogProps {
  visible: boolean
  onClose: () => void
  /** Optional model options from inventory for the "model preference" field. */
  modelOptions?: { label: string; value: string }[]
}

const WORKLOAD_CLASS_OPTIONS = [
  { label: '推理', value: 'inference' },
  { label: '训练', value: 'training' },
  { label: '批任务', value: 'batch' },
]

const ALLOCATION_MODE_OPTIONS = [
  { label: '整卡', value: 'dedicated' },
  { label: 'vGPU 切片', value: 'vgpu' },
]

export function CreateGpuContainerDialog({
  visible,
  onClose,
  modelOptions = [],
}: CreateGpuContainerDialogProps) {
  const navigate = useNavigate()
  const [form] = Form.useForm()

  const queuesQuery = useQuery({
    queryKey: ['gpu-scheduling-queues'],
    queryFn: () => coreApi.GET('/gpu-scheduling/queues').then(({ data }) => data),
    enabled: visible,
  })

  const queueOptions = (queuesQuery.data?.items ?? [])
    .map((q: GPUSchedulingQueue) => ({ label: q.name, value: q.name }))

  const createMutation = useMutation({
    mutationFn: async (payload: CreateInstanceRequest) => {
      const { data, error, response } = await coreApi.POST('/instances', {
        body: payload,
      })
      if (error) {
        const err = error as { code?: string; message?: string }
        throw { code: err.code, message: err.message, status: response.status }
      }
      return data
    },
  })

  function resetForm() {
    form.reset()
  }

  useEffect(() => {
    if (!visible) {
      resetForm()
    }
  }, [visible, form])

  async function handleSubmit() {
    const result = await form.validate()
    if (result !== true) return

    const values = form.getFieldsValue(true) as {
      name: string
      gpu_count: number
      allocation_mode: 'dedicated' | 'vgpu'
      workload_class: 'inference' | 'training' | 'batch'
      queue_name: string
      model: string
    }

    const idempotencyKey = crypto.randomUUID()
    const payload: CreateInstanceRequest = {
      idempotency_key: idempotencyKey,
      name: values.name,
      kind: 'gpu_container',
      instance_type: 'gpu_container',
      auto_start: true,
      termination_protection: false,
      ssh_username: null,
      replicas: 1,
      gpu: {
        count: values.gpu_count,
        allocation_mode: values.allocation_mode,
        workload_class: values.workload_class,
        queue_name: values.queue_name || null,
        model: values.model || undefined,
      },
    }

    try {
      const result = await createMutation.mutateAsync(payload)
      MessagePlugin.success('GPU 容器创建已提交')
      onClose()
      navigate({ to: '/compute/gpu-containers/$instanceId', params: { instanceId: result.instance.id } })
    } catch (err) {
      const e = err as { code?: string; message?: string; status?: number }
      if (e.status === 422) {
        if (e.code === 'QueueNotFound') {
          form.setFieldsValue({ _queueError: '所选调度队列不存在或已删除' })
        }
        MessagePlugin.error(`调度失败：${e.message ?? e.code ?? '未知错误'}`)
      } else {
        MessagePlugin.error(`创建失败：${e.message ?? '请稍后重试'}`)
      }
    }
  }

  return (
    <Dialog
      visible={visible}
      header="创建 GPU 容器"
      width={520}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button
            theme="primary"
            loading={createMutation.isPending}
            onClick={handleSubmit}
          >
            提交
          </Button>
        </>
      }
    >
      <Form form={form} labelWidth={100} labelAlign="right">
        <Form.FormItem label="名称" name="name" rules={[{ required: true, message: '请输入名称' }]}>
          <Input placeholder="容器名称" />
        </Form.FormItem>

        <Form.FormItem label="GPU 数量" name="gpu_count" initialData={1} rules={[{ required: true }]}>
          <InputNumber min={1} step={1} />
        </Form.FormItem>

        <Form.FormItem label="分配模式" name="allocation_mode" initialData="dedicated">
          <Radio.Group options={ALLOCATION_MODE_OPTIONS} />
        </Form.FormItem>

        <Form.FormItem label="工作负载类型" name="workload_class" initialData="inference">
          <Radio.Group options={WORKLOAD_CLASS_OPTIONS} />
        </Form.FormItem>

        <Form.FormItem label="调度队列" name="queue_name">
          <Select
            options={queueOptions}
            placeholder="留空按工作负载类型选默认队列"
            clearable
            loading={queuesQuery.isLoading}
          />
        </Form.FormItem>

        <Form.FormItem label="型号偏好" name="model">
          <Select
            options={modelOptions}
            placeholder="可选，留空不指定"
            clearable
            filterable
          />
        </Form.FormItem>
      </Form>
    </Dialog>
  )
}
