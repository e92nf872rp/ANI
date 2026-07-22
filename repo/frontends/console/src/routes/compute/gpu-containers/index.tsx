import { useMemo, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Empty,
  Input,
  Select,
  Table,
  Tag,
} from 'tdesign-react'
import type { PrimaryTableCol, SelectValue } from 'tdesign-react'
import { AddIcon } from 'tdesign-icons-react'
import { useQuery } from '@tanstack/react-query'
import { ConsolePage } from '@/components/shell/ConsolePage'
import { ConsolePageHeader } from '@/components/shell/ConsolePageHeader'
import { ConsoleContentCard } from '@/components/shell/ConsoleContentCard'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'
import { CreateGpuContainerDialog } from './create-dialog'

export const Route = createFileRoute('/compute/gpu-containers/')({
  component: GpuContainerListPage,
})

type InstanceState = components['schemas']['InstanceRecord']['state']
type InstanceRecord = components['schemas']['InstanceRecord']

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

const STATE_OPTIONS = (Object.keys(STATE_LABEL) as InstanceState[]).map((v) => ({
  label: STATE_LABEL[v],
  value: v,
}))

function GpuContainerListPage() {
  const [nameFilter, setNameFilter] = useState('')
  const [stateFilter, setStateFilter] = useState<InstanceState | ''>('')
  const [dialogVisible, setDialogVisible] = useState(false)

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['gpu-container-instances'],
    queryFn: () =>
      coreApi
        .GET('/instances', { params: { query: { kind: 'gpu_container' } } })
        .then(({ data }) => data),
  })

  const modelOptions = useMemo(() => {
    const set = new Set<string>()
    for (const item of data?.items ?? []) {
      if (item.gpu?.model) set.add(item.gpu.model)
    }
    return Array.from(set).map((v) => ({ label: v, value: v }))
  }, [data])

  const filteredItems = useMemo(() => {
    let items = data?.items ?? []
    if (nameFilter) {
      items = items.filter((item) =>
        item.name.toLowerCase().includes(nameFilter.toLowerCase()),
      )
    }
    if (stateFilter) {
      items = items.filter((item) => item.state === stateFilter)
    }
    return items
  }, [data, nameFilter, stateFilter])

  const columns: PrimaryTableCol<InstanceRecord>[] = [
    {
      title: '名称',
      colKey: 'name',
      cell: ({ row }) => (
        <Link to="/compute/gpu-containers/$instanceId" params={{ instanceId: row.id }}>
          {row.name}
        </Link>
      ),
    },
    {
      title: '状态',
      colKey: 'state',
      cell: ({ row }) => (
        <Tag theme={STATE_THEME[row.state] ?? 'default'} variant="light">
          {STATE_LABEL[row.state] ?? row.state}
        </Tag>
      ),
    },
    {
      title: 'GPU 数量',
      colKey: 'gpu.count',
      cell: ({ row }) => row.gpu?.count ?? '—',
    },
    {
      title: '型号',
      colKey: 'gpu.model',
      cell: ({ row }) => row.gpu?.model ?? '—',
    },
    {
      title: '调度队列',
      colKey: 'gpu.queue_name',
      cell: ({ row }) => row.gpu?.queue_name ?? '—',
    },
    {
      title: '操作',
      colKey: 'op',
      cell: ({ row }) => (
        <Link to="/compute/gpu-containers/$instanceId" params={{ instanceId: row.id }}>
          <Button variant="text" size="small">查看详情</Button>
        </Link>
      ),
    },
  ]

  return (
    <ConsolePage>
      <ConsolePageHeader
        title="GPU 容器实例"
        subtitle="GPU 容器实例列表与创建"
        actions={
          <Button theme="primary" icon={<AddIcon />} onClick={() => setDialogVisible(true)}>
            创建
          </Button>
        }
      />

      {isError && (
        <Alert
          theme="error"
          message="加载 GPU 容器实例失败"
          operation={<Button variant="outline" onClick={() => refetch()}>重试</Button>}
        />
      )}

      <ConsoleContentCard>
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Input
            value={nameFilter}
            onChange={(val) => setNameFilter(val as string)}
            placeholder="按名称搜索"
            style={{ width: 220 }}
          />
          <Select
            value={stateFilter}
            onChange={(val: SelectValue) => setStateFilter((val as InstanceState) ?? '')}
            options={STATE_OPTIONS}
            placeholder="按状态筛选"
            clearable
            style={{ width: 180 }}
          />
        </div>

        {data && data.items.length === 0 && !nameFilter && !stateFilter ? (
          <Empty description="暂无 GPU 容器实例" />
        ) : (
          <Table
            loading={isLoading}
            data={filteredItems}
            columns={columns}
            rowKey="id"
          />
        )}
      </ConsoleContentCard>

      <CreateGpuContainerDialog
        visible={dialogVisible}
        onClose={() => setDialogVisible(false)}
        modelOptions={modelOptions}
      />
    </ConsolePage>
  )
}
