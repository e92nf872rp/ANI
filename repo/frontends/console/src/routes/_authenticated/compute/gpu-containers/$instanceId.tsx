import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Descriptions,
  Dialog,
  Empty,
  MessagePlugin,
  Skeleton,
  Space,
  Tag,
} from 'tdesign-react'
import { ChevronLeftIcon } from 'tdesign-icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ConsolePage } from '@/components/shell/ConsolePage'
import { ConsolePageHeader } from '@/components/shell/ConsolePageHeader'
import { ConsoleContentCard } from '@/components/shell/ConsoleContentCard'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/_authenticated/compute/gpu-containers/$instanceId')({
  component: GpuContainerDetailPage,
})

type InstanceRecord = components['schemas']['InstanceRecord']
type InstanceState = InstanceRecord['state']

const STATE_THEME: Record<InstanceState, 'success' | 'primary' | 'default' | 'danger' | 'warning'> = {
  running: 'success',
  provisioning: 'primary',
  starting: 'primary',
  stopped: 'default',
  failed: 'danger',
  deleting: 'warning',
  pending: 'primary',
  stopping: 'warning',
  deleted: 'default',
}

const STATE_LABEL: Record<InstanceState, string> = {
  running: '运行中',
  provisioning: '创建中',
  starting: '启动中',
  stopped: '已停止',
  failed: '失败',
  deleting: '删除中',
  pending: '等待中',
  stopping: '停止中',
  deleted: '已删除',
}

function isNotFound(error: unknown): boolean {
  if (!error) return false
  const anyError = error as { status?: number; response?: { status?: number } }
  return anyError.status === 404 || anyError.response?.status === 404
}

function GpuContainerDetailPage() {
  const { instanceId } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [deleteDialogVisible, setDeleteDialogVisible] = useState(false)

  const { data: instance, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['gpu-container-instance', instanceId],
    queryFn: () =>
      coreApi
        .GET('/instances/{instance_id}', { params: { path: { instance_id: instanceId } } })
        .then(({ data }) => data),
  })

  const lifecycleMutation = useMutation({
    mutationFn: async (action: 'start' | 'stop' | 'delete') => {
      const { data, error, response } = await coreApi.POST('/instances/{instance_id}/lifecycle', {
        params: { path: { instance_id: instanceId } },
        body: {
          action,
          idempotency_key: `${action}-${instanceId}-${Date.now()}`,
        },
      })
      if (error) {
        const err = error as { code?: string; message?: string }
        throw { code: err.code, message: err.message, status: response.status }
      }
      return data
    },
    onSuccess: (_data, action) => {
      MessagePlugin.success(action === 'delete' ? '删除请求已提交' : `${action === 'stop' ? '停止' : '启动'}请求已提交`)
      if (action === 'delete') {
        queryClient.invalidateQueries({ queryKey: ['gpu-container-instances'] })
        navigate({ to: '/compute/gpu-containers' })
      } else {
        queryClient.invalidateQueries({ queryKey: ['gpu-container-instance', instanceId] })
        queryClient.invalidateQueries({ queryKey: ['gpu-container-instances'] })
      }
    },
    onError: (err: { message?: string }) => {
      MessagePlugin.error(err.message || '操作失败')
    },
  })

  if (isNotFound(error)) {
    return (
      <ConsolePage>
        <Link to="/compute/gpu-containers">
          <Button variant="text" icon={<ChevronLeftIcon />}>返回列表</Button>
        </Link>
        <Empty description="实例不存在" />
      </ConsolePage>
    )
  }

  if (isLoading) {
    return (
      <ConsolePage>
        <ConsolePageHeader title="GPU 容器详情" />
        <Skeleton animation="gradient" style={{ height: 200 }} />
      </ConsolePage>
    )
  }

  if (isError || !instance) {
    return (
      <ConsolePage>
        <Link to="/compute/gpu-containers">
          <Button variant="text" icon={<ChevronLeftIcon />}>返回列表</Button>
        </Link>
        <Alert
          theme="error"
          message="加载实例详情失败"
          operation={<Button variant="outline" onClick={() => refetch()}>重试</Button>}
        />
      </ConsolePage>
    )
  }

  const isProvisioning = instance.state === 'provisioning' || instance.state === 'pending' || instance.state === 'starting'
  const isFailed = instance.state === 'failed'
  const isRunning = instance.state === 'running'
  const isStopped = instance.state === 'stopped'
  const canStart = isStopped || isFailed
  const canStop = isRunning
  const canDelete = !isProvisioning && instance.state !== 'deleting' && instance.state !== 'deleted'

  function allocationModeLabel(resourceName?: string | null): string {
    if (!resourceName) return '—'
    if (resourceName.includes('vgpu')) return 'vGPU 切片'
    return '整卡'
  }

  return (
    <ConsolePage>
      <Link to="/compute/gpu-containers">
        <Button variant="text" icon={<ChevronLeftIcon />}>返回列表</Button>
      </Link>

      <ConsolePageHeader
        title={instance.name}
        subtitle={`实例 ID: ${instance.id}`}
        extra={
          <Space>
            <Tag theme={STATE_THEME[instance.state] ?? 'default'} variant="light">
              {STATE_LABEL[instance.state] ?? instance.state}
            </Tag>
            {canStop && (
              <Button
                theme="warning"
                variant="outline"
                loading={lifecycleMutation.isPending}
                onClick={() => lifecycleMutation.mutate('stop')}
              >
                停止
              </Button>
            )}
            {canStart && (
              <Button
                theme="primary"
                variant="outline"
                loading={lifecycleMutation.isPending}
                onClick={() => lifecycleMutation.mutate('start')}
              >
                启动
              </Button>
            )}
            {canDelete && (
              <Button
                theme="danger"
                variant="outline"
                loading={lifecycleMutation.isPending}
                onClick={() => setDeleteDialogVisible(true)}
              >
                删除
              </Button>
            )}
          </Space>
        }
      />

      {isFailed && instance.state_reason && (
        <Alert theme="error" message={`失败原因：${instance.state_reason}`} />
      )}

      {isProvisioning && (
        <Alert theme="info" message="调度中，预计 1-2 分钟" />
      )}

      <ConsoleContentCard title="基本信息">
        <Descriptions items={[
          { label: '名称', content: instance.name },
          { label: '状态', content: STATE_LABEL[instance.state] ?? instance.state },
          { label: '类型', content: instance.kind },
          { label: 'Provider', content: instance.provider },
          { label: '节点', content: instance.node_name ?? '—' },
          { label: 'Endpoint', content: instance.endpoint ?? '—' },
          { label: '创建时间', content: new Date(instance.created_at).toLocaleString('zh-CN') },
          { label: '更新时间', content: new Date(instance.updated_at).toLocaleString('zh-CN') },
        ]} />
      </ConsoleContentCard>

      <ConsoleContentCard title="GPU 与调度">
        <Descriptions items={[
          { label: 'GPU 数量', content: instance.gpu?.count ?? '—' },
          { label: '厂商', content: instance.gpu?.vendor ?? '—' },
          { label: '型号', content: instance.gpu?.model ?? '—' },
          { label: '分配模式', content: allocationModeLabel(instance.gpu?.resource_name) },
          { label: '调度队列', content: instance.gpu?.queue_name ?? '—' },
          { label: '调度说明', content: instance.gpu?.scheduling_reason ?? '—' },
          { label: '利用率', content: instance.gpu?.utilization_percent != null ? `${instance.gpu.utilization_percent}%` : '—' },
          { label: '失败原因', content: instance.state_reason ?? '—' },
        ]} />
      </ConsoleContentCard>

      <Dialog
        header="确认删除"
        visible={deleteDialogVisible}
        onClose={() => setDeleteDialogVisible(false)}
        footer={
          <Space>
            <Button variant="outline" onClick={() => setDeleteDialogVisible(false)}>取消</Button>
            <Button
              theme="danger"
              loading={lifecycleMutation.isPending}
              onClick={() => {
                lifecycleMutation.mutate('delete')
                setDeleteDialogVisible(false)
              }}
            >
              确认删除
            </Button>
          </Space>
        }
      >
        确定要删除实例「{instance.name}」吗？此操作不可撤销。
      </Dialog>
    </ConsolePage>
  )
}
