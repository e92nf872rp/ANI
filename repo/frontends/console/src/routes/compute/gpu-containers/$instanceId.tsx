import { createFileRoute, Link } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Descriptions,
  Empty,
  Skeleton,
  Tag,
} from 'tdesign-react'
import { ChevronLeftIcon } from 'tdesign-icons-react'
import { useQuery } from '@tanstack/react-query'
import { ConsolePage } from '@/components/shell/ConsolePage'
import { ConsolePageHeader } from '@/components/shell/ConsolePageHeader'
import { ConsoleContentCard } from '@/components/shell/ConsoleContentCard'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/compute/gpu-containers/$instanceId')({
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

  const { data: instance, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['gpu-container-instance', instanceId],
    queryFn: () =>
      coreApi
        .GET('/instances/{instance_id}', { params: { path: { instance_id: instanceId } } })
        .then(({ data }) => data),
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
          <Tag theme={STATE_THEME[instance.state] ?? 'default'} variant="light">
            {STATE_LABEL[instance.state] ?? instance.state}
          </Tag>
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
    </ConsolePage>
  )
}
