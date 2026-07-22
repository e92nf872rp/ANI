import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Dialog,
  Empty,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Popconfirm,
  Select,
  Switch,
  Table,
  Tag,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { AddIcon } from 'tdesign-icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ConsolePage } from '@/components/shell/ConsolePage'
import { ConsolePageHeader } from '@/components/shell/ConsolePageHeader'
import { ConsoleContentCard } from '@/components/shell/ConsoleContentCard'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/settings/gpu-queues')({
  component: GpuQueueSettingsPage,
})

type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']
type WorkloadClass = 'inference' | 'training' | 'batch'

const WORKLOAD_CLASS_LABEL: Record<WorkloadClass, string> = {
  inference: '推理',
  training: '训练',
  batch: '批任务',
}

const WORKLOAD_CLASS_OPTIONS = [
  { label: '推理', value: 'inference' },
  { label: '训练', value: 'training' },
  { label: '批任务', value: 'batch' },
]

const QUEUE_NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

// canManageQueues is a placeholder for RBAC scope:gpu-scheduling:write.
// When auth store is wired, this should check the current user's scopes.
// For now dev/local profile defaults to true so the UI is testable.
function canManageQueues(): boolean {
  return true
}

type DialogMode = 'create' | 'edit' | null

export function GpuQueueSettingsPage() {
  const queryClient = useQueryClient()
  const [dialogMode, setDialogMode] = useState<DialogMode>(null)
  const [editingQueue, setEditingQueue] = useState<GPUSchedulingQueue | null>(null)
  const [form] = Form.useForm()
  const writable = canManageQueues()

  const queuesQuery = useQuery({
    queryKey: ['gpu-scheduling-queues'],
    queryFn: () => coreApi.GET('/gpu-scheduling/queues').then(({ data }) => data),
  })

  const queues = queuesQuery.data?.items ?? []
  const platformDefaultQueues = queues.filter((q) => q.is_platform_default)
  const customQueues = queues.filter((q) => !q.is_platform_default)

  const createMutation = useMutation({
    mutationFn: async (values: {
      name: string
      workload_class: WorkloadClass
      weight: number
      reclaimable: boolean
      project_id: string
    }) => {
      const idempotencyKey = crypto.randomUUID()
      const { data, error, response } = await coreApi.POST('/gpu-scheduling/queues', {
        params: {
          header: { 'Idempotency-Key': idempotencyKey },
        },
        body: {
          name: values.name,
          workload_class: values.workload_class,
          weight: values.weight,
          reclaimable: values.reclaimable,
          project_id: values.project_id || null,
        },
      })
      if (error) {
        const err = error as { code?: string; message?: string }
        throw { code: err.code, message: err.message, status: response.status }
      }
      return data
    },
    onSuccess: () => {
      MessagePlugin.success('队列创建成功')
      queryClient.invalidateQueries({ queryKey: ['gpu-scheduling-queues'] })
      setDialogMode(null)
    },
    onError: (err: unknown) => {
      const e = err as { code?: string; message?: string; status?: number }
      if (e.status === 409) {
        MessagePlugin.error('队列名称已存在')
      } else {
        MessagePlugin.error(`创建失败：${e.message ?? '请稍后重试'}`)
      }
    },
  })

  const updateMutation = useMutation({
    mutationFn: async (params: {
      queueId: string
      values: {
        workload_class: WorkloadClass
        weight: number
        reclaimable: boolean
        project_id: string
      }
    }) => {
      const { data, error, response } = await coreApi.PATCH('/gpu-scheduling/queues/{queue_id}', {
        params: { path: { queue_id: params.queueId } },
        body: {
          workload_class: params.values.workload_class,
          weight: params.values.weight,
          reclaimable: params.values.reclaimable,
          project_id: params.values.project_id || null,
        },
      })
      if (error) {
        const err = error as { code?: string; message?: string }
        throw { code: err.code, message: err.message, status: response.status }
      }
      return data
    },
    onSuccess: () => {
      MessagePlugin.success('队列更新成功')
      queryClient.invalidateQueries({ queryKey: ['gpu-scheduling-queues'] })
      setDialogMode(null)
    },
    onError: (err: unknown) => {
      const e = err as { code?: string; message?: string; status?: number }
      if (e.status === 403) {
        MessagePlugin.error('平台默认队列不可修改')
      } else {
        MessagePlugin.error(`更新失败：${e.message ?? '请稍后重试'}`)
      }
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async (queueId: string) => {
      const { error, response } = await coreApi.DELETE('/gpu-scheduling/queues/{queue_id}', {
        params: { path: { queue_id: queueId } },
      })
      if (error) {
        const err = error as { code?: string; message?: string }
        throw { code: err.code, message: err.message, status: response.status }
      }
    },
    onSuccess: () => {
      MessagePlugin.success('队列删除成功')
      queryClient.invalidateQueries({ queryKey: ['gpu-scheduling-queues'] })
    },
    onError: (err: unknown) => {
      const e = err as { code?: string; message?: string; status?: number }
      if (e.status === 403) {
        MessagePlugin.error('平台默认队列不可删除')
      } else {
        MessagePlugin.error(`删除失败：${e.message ?? '请稍后重试'}`)
      }
    },
  })

  function openCreateDialog() {
    setEditingQueue(null)
    setDialogMode('create')
    form.reset()
  }

  function openEditDialog(queue: GPUSchedulingQueue) {
    setEditingQueue(queue)
    setDialogMode('edit')
    // Form field values are injected via initialData on each FormItem
    // (see editInitialData below). This avoids the race between
    // setFieldsValue and FormItem's mount-time registration effect.
  }

  function closeDialog() {
    setDialogMode(null)
    setEditingQueue(null)
    form.reset()
  }

  async function handleSubmit() {
    const result = await form.validate()
    if (result !== true) return

    const values = form.getFieldsValue(true) as {
      name: string
      workload_class: WorkloadClass
      weight: number
      reclaimable: boolean
      project_id: string
    }

    if (dialogMode === 'create') {
      createMutation.mutate(values)
    } else if (dialogMode === 'edit' && editingQueue) {
      updateMutation.mutate({
        queueId: editingQueue.id,
        values: {
          workload_class: values.workload_class,
          weight: values.weight,
          reclaimable: values.reclaimable,
          project_id: values.project_id,
        },
      })
    }
  }

  function handleDelete(queue: GPUSchedulingQueue) {
    deleteMutation.mutate(queue.id)
  }

  const platformDefaultColumns: PrimaryTableCol<GPUSchedulingQueue>[] = [
    { title: '队列名称', colKey: 'name' },
    {
      title: '工作负载类型',
      colKey: 'workload_class',
      cell: ({ row }) => (
        <Tag variant="light">{WORKLOAD_CLASS_LABEL[row.workload_class] ?? row.workload_class}</Tag>
      ),
    },
    { title: '权重', colKey: 'weight' },
    {
      title: '可被回收',
      colKey: 'reclaimable',
      cell: ({ row }) => (row.reclaimable ? '是' : '否'),
    },
  ]

  const customColumns: PrimaryTableCol<GPUSchedulingQueue>[] = [
    { title: '队列名称', colKey: 'name' },
    {
      title: '工作负载类型',
      colKey: 'workload_class',
      cell: ({ row }) => (
        <Tag variant="light">{WORKLOAD_CLASS_LABEL[row.workload_class] ?? row.workload_class}</Tag>
      ),
    },
    { title: '权重', colKey: 'weight' },
    {
      title: '可被回收',
      colKey: 'reclaimable',
      cell: ({ row }) => (row.reclaimable ? '是' : '否'),
    },
    {
      title: '关联项目',
      colKey: 'project_id',
      cell: ({ row }) => row.project_id ?? '—',
    },
    ...(writable
      ? [
          {
            title: '操作',
            colKey: 'op',
            cell: ({ row }: { row: GPUSchedulingQueue }) => (
              <div style={{ display: 'flex', gap: 8 }}>
                <Button variant="text" size="small" onClick={() => openEditDialog(row)}>
                  编辑
                </Button>
                <Popconfirm content="确认删除此队列？" onConfirm={() => handleDelete(row)}>
                  <Button variant="text" theme="danger" size="small">
                    删除
                  </Button>
                </Popconfirm>
              </div>
            ),
          },
        ]
      : []),
  ]

  const isSubmitting = createMutation.isPending || updateMutation.isPending

    // In edit mode, seed FormItem initialData with the row's values so the
    // form mounts with the correct values directly. The `key` on Form
    // forces a fresh form instance whenever the edit target changes or
    // we switch between create/edit, ensuring initialData is re-applied.
    const formKey = dialogMode === 'edit'
      ? `edit-${editingQueue?.id ?? ''}`
      : dialogMode === 'create'
        ? 'create'
        : 'closed'
    const editValues = dialogMode === 'edit' && editingQueue
      ? {
          name: editingQueue.name,
          workload_class: editingQueue.workload_class,
          weight: editingQueue.weight,
          reclaimable: editingQueue.reclaimable,
          project_id: editingQueue.project_id ?? '',
        }
      : null

    return (
      <ConsolePage>
        <ConsolePageHeader
          title="GPU 调度队列"
          subtitle="设置 → GPU 调度队列"
          actions={
            writable ? (
              <Button theme="primary" icon={<AddIcon />} onClick={openCreateDialog}>
                新建队列
              </Button>
            ) : undefined
          }
        />

        {!writable && (
          <Alert theme="warning" message="仅租户管理员可管理队列" />
        )}

        {queuesQuery.isError && (
          <Alert
            theme="error"
            message="加载队列列表失败"
            operation={<Button variant="outline" onClick={() => queuesQuery.refetch()}>重试</Button>}
          />
        )}

        <ConsoleContentCard title="平台默认队列（只读）">
          <Table
            loading={queuesQuery.isLoading}
            data={platformDefaultQueues}
            columns={platformDefaultColumns}
            rowKey="id"
          />
        </ConsoleContentCard>

        <ConsoleContentCard title="我的队列">
          {customQueues.length === 0 && !queuesQuery.isLoading ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 16, padding: '32px 0' }}>
              <Empty description="暂无自定义队列" />
              {writable && (
                <Button theme="primary" icon={<AddIcon />} onClick={openCreateDialog}>
                  新建队列
                </Button>
              )}
            </div>
          ) : (
            <Table
              loading={queuesQuery.isLoading}
              data={customQueues}
              columns={customColumns}
              rowKey="id"
            />
          )}
        </ConsoleContentCard>

        <Dialog
          visible={dialogMode !== null}
          header={dialogMode === 'create' ? '新建队列' : '编辑队列'}
          width={480}
          onClose={closeDialog}
          footer={
            <>
              <Button variant="outline" onClick={closeDialog}>取消</Button>
              <Button theme="primary" loading={isSubmitting} onClick={handleSubmit}>
                {dialogMode === 'create' ? '创建' : '保存'}
              </Button>
            </>
          }
        >
          <Form
            form={form}
            labelWidth={100}
            labelAlign="right"
            key={formKey}
          >
            <Form.FormItem
              label="队列名称"
              name="name"
              initialData={editValues?.name ?? ''}
              rules={[
                { required: true, message: '请输入队列名称' },
                {
                  pattern: QUEUE_NAME_PATTERN.source,
                  message: '须符合 K8s 资源名规范：小写字母数字，连字符分隔',
                },
              ]}
            >
              <Input placeholder="如 proj-a-infer" disabled={dialogMode === 'edit'} />
            </Form.FormItem>

            <Form.FormItem
              label="工作负载类型"
              name="workload_class"
              initialData={editValues?.workload_class ?? 'inference'}
              rules={[{ required: true, message: '请选择工作负载类型' }]}
            >
              <Select options={WORKLOAD_CLASS_OPTIONS} />
            </Form.FormItem>

            <Form.FormItem
              label="权重"
              name="weight"
              initialData={editValues?.weight ?? 10}
              rules={[{ required: true }]}
            >
              <InputNumber min={1} step={1} />
            </Form.FormItem>

            <Form.FormItem
              label="可被回收"
              name="reclaimable"
              initialData={editValues?.reclaimable ?? false}
            >
              <Switch />
            </Form.FormItem>

            <Form.FormItem
              label="关联项目"
              name="project_id"
              initialData={editValues?.project_id ?? ''}
            >
              <Input placeholder="可选" />
            </Form.FormItem>
          </Form>
        </Dialog>
      </ConsolePage>
    )
  }

export default GpuQueueSettingsPage
