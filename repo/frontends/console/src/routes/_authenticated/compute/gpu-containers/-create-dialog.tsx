import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Dialog,
  Form,
  Input,
  MessagePlugin,
  Select,
  Tag,
} from 'tdesign-react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

type CreateInstanceRequest = components['schemas']['CreateInstanceRequest']
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']
type GPUSpecAvailability = components['schemas']['GPUSpecAvailability']

export interface CreateGpuContainerDialogProps {
  visible: boolean
  onClose: () => void
}

// 四态 → Tag theme + 文案（UX §7.3）
function specStatusTag(status: GPUSpecAvailability['status'], availableCount: number) {
  switch (status) {
    case 'available':
      return <Tag theme="success" variant="light">剩余 {availableCount}</Tag>
    case 'full':
      return <Tag theme="default" variant="light">配额已满</Tag>
    case 'device_full':
      return <Tag theme="warning" variant="light">设备已满，暂无空闲</Tag>
    case 'unavailable':
      return <Tag theme="default" variant="light">暂无匹配节点</Tag>
  }
}

export function CreateGpuContainerDialog({ visible, onClose }: CreateGpuContainerDialogProps) {
  const navigate = useNavigate()
  const [form] = Form.useForm()
  const [selectedSpecId, setSelectedSpecId] = useState<string>('')

  // 规格可用性（GET /gpu-specs/availability）
  const availabilityQuery = useQuery({
    queryKey: ['gpu-specs-availability'],
    queryFn: () => coreApi.GET('/gpu-specs/availability').then(({ data }) => data),
    enabled: visible,
  })

  // 调度队列（GET /gpu-scheduling/queues）
  const queuesQuery = useQuery({
    queryKey: ['gpu-scheduling-queues'],
    queryFn: () => coreApi.GET('/gpu-scheduling/queues').then(({ data }) => data),
    enabled: visible,
  })

  const queueOptions = useMemo(
    () =>
      (queuesQuery.data?.items ?? []).map((q: GPUSchedulingQueue) => ({
        label: q.name,
        value: q.name,
      })),
    [queuesQuery.data],
  )

  const availabilityItems: GPUSpecAvailability[] = useMemo(
    () => availabilityQuery.data?.items ?? [],
    [availabilityQuery.data],
  )
  const quotaRemaining: number = availabilityQuery.data?.quota_remaining ?? 0

  // 本地重算：选规格后扣减 quota_remaining，刷新各规格 available_count（UX §4.4）
  const recomputedItems = useMemo(() => {
    if (!selectedSpecId) return availabilityItems
    const selected = availabilityItems.find((s) => s.spec_id === selectedSpecId)
    if (!selected) return availabilityItems
    const consumedGpuCount = selected.gpu_count ?? 1
    const newQuotaRemaining = Math.max(0, quotaRemaining - consumedGpuCount)
    return availabilityItems.map((s) => {
      if (s.spec_id === selectedSpecId) return s
      const recalculatedAvailable = Math.min(newQuotaRemaining, s.device_idle_count)
      return {
        ...s,
        available_count: recalculatedAvailable,
      }
    })
  }, [availabilityItems, selectedSpecId, quotaRemaining])

  // 规格下拉选项：四态标注 + 置灰不可选项
  const specOptions = useMemo(
    () =>
      recomputedItems.map((s) => {
        const disabled = s.status !== 'available' || s.available_count <= 0
        return {
          label: (
            <span style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span>{s.spec_id}</span>
              {specStatusTag(s.status, s.available_count)}
            </span>
          ),
          value: s.spec_id,
          disabled,
        }
      }),
    [recomputedItems],
  )

  // 选中规格的详情（用于 Alert 展示）
  const selectedSpec = useMemo(
    () => recomputedItems.find((s) => s.spec_id === selectedSpecId),
    [recomputedItems, selectedSpecId],
  )

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

  useEffect(() => {
    if (!visible) {
      form.reset()
      setSelectedSpecId('')
    }
  }, [visible, form])

  async function handleSubmit() {
    const result = await form.validate()
    if (result !== true) return

    const values = form.getFieldsValue(true) as {
      name: string
      spec_id: string
      queue_name: string
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
      gpu_container_config: {
        replicas: 1,
        gpu: {
          spec_id: values.spec_id,
          count: 1,
          queue_name: values.queue_name,
          allocation_mode: 'dedicated',
          workload_class: 'inference',
        },
      },
    }

    try {
      const result = await createMutation.mutateAsync(payload)
      MessagePlugin.success('GPU 容器创建已提交')
      onClose()
      navigate({ to: '/compute/gpu-containers/$instanceId', params: { instanceId: result.instance.id } })
    } catch (err) {
      const e = err as { code?: string; message?: string; status?: number }
      if (e.status === 409) {
        if (e.code === 'QUOTA_EXCEEDED') {
          MessagePlugin.error('配额不足，无法创建')
        } else if (e.code === 'RESERVED_INSUFFICIENT') {
          MessagePlugin.error('预留额度不足，无法创建')
        } else {
          MessagePlugin.error(`创建失败：${e.message ?? e.code ?? '未知错误'}`)
        }
      } else if (e.status === 422 && e.code === 'QueueNotFound') {
        form.setFieldsValue({ _queueError: '所选调度队列不存在或已删除' })
        MessagePlugin.error('调度失败：所选队列不存在')
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
            创建
          </Button>
        </>
      }
    >
      <Form form={form} labelWidth={100} labelAlign="right">
        <Form.FormItem label="名称" name="name" rules={[{ required: true, message: '请输入名称' }]}>
          <Input placeholder="实例名称" />
        </Form.FormItem>

        <Form.FormItem
          label="GPU 规格"
          name="spec_id"
          rules={[{ required: true, message: '请选择 GPU 规格' }]}
        >
          <Select
            options={specOptions}
            placeholder="选择 GPU 规格"
            filterable
            loading={availabilityQuery.isLoading}
            onChange={(v) => setSelectedSpecId(v as string)}
          />
        </Form.FormItem>

        {selectedSpec && (
          <Alert
            theme="info"
            message={`已选规格：${selectedSpec.spec_id}，占用 ${selectedSpec.gpu_count ?? 1} 卡，剩余配额 ${Math.max(0, quotaRemaining - (selectedSpec.gpu_count ?? 1))} 卡`}
            style={{ marginBottom: 12 }}
          />
        )}

        <Form.FormItem
          label="调度队列"
          name="queue_name"
          rules={[{ required: true, message: '请选择调度队列' }]}
        >
          <Select
            options={queueOptions}
            placeholder="选择调度队列"
            clearable
            loading={queuesQuery.isLoading}
          />
        </Form.FormItem>
      </Form>
    </Dialog>
  )
}
