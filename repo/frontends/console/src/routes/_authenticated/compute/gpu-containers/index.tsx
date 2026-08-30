import { useMemo, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Col,
  Empty,
  Input,
  Row,
  Select,
  Skeleton,
  Statistic,
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
import { CreateGpuContainerDialog } from './-create-dialog'

export const Route = createFileRoute('/_authenticated/compute/gpu-containers/')({
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

  // 本租户配额（GET /quotas/me，UX §4.6 配额卡片）
  const quotaQuery = useQuery({
    queryKey: ['my-quota'],
    queryFn: () => coreApi.GET('/quotas/me').then(({ data }) => data),
  })

  // 本租户预留额度（GET /reservations/me，UX §4.6 预留卡片）
  const reservationQuery = useQuery({
    queryKey: ['my-reservations'],
    queryFn: () => coreApi.GET('/reservations/me').then(({ data }) => data),
  })

  // 从 Quota.items 取 gpu_count 维度（可用余量 = total - used - reserved）
  const gpuCountQuota = useMemo(
    () => quotaQuery.data?.items?.find((it) => it.resource_type === 'gpu_count'),
    [quotaQuery.data],
  )
  const quotaAvailable = gpuCountQuota
    ? Math.max(0, gpuCountQuota.total - gpuCountQuota.used - gpuCountQuota.reserved)
    : 0

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

      {/* 配额 + 预留卡片（UX §4.6） */}
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <ConsoleContentCard title="配额（GPU 卡数）">
            {quotaQuery.isLoading ? (
              <Skeleton />
            ) : quotaQuery.isError ? (
              <Alert theme="warning" message="配额加载失败" />
            ) : gpuCountQuota ? (
              <Row gutter={8}>
                <Col span={6}>
                  <Statistic title="总量" value={gpuCountQuota.total} />
                </Col>
                <Col span={6}>
                  <Statistic title="已用" value={gpuCountQuota.used} />
                </Col>
                <Col span={6}>
                  <Statistic title="预留" value={gpuCountQuota.reserved} />
                </Col>
                <Col span={6}>
                  <Statistic title="可用余量" value={quotaAvailable} />
                </Col>
              </Row>
            ) : (
              <Empty description="暂无配额数据" />
            )}
          </ConsoleContentCard>
        </Col>
        <Col span={6}>
          <ConsoleContentCard title="预留额度">
            {reservationQuery.isLoading ? (
              <Skeleton />
            ) : reservationQuery.isError ? (
              <Alert theme="warning" message="预留额度加载失败" />
            ) : reservationQuery.data ? (
              <Row gutter={8}>
                <Col span={8}>
                  <Statistic title="已分配" value={reservationQuery.data.allocated_gpu_count} />
                </Col>
                <Col span={8}>
                  <Statistic title="已使用" value={reservationQuery.data.used} />
                </Col>
                <Col span={8}>
                  <Statistic title="可用" value={reservationQuery.data.available} />
                </Col>
              </Row>
            ) : (
              <Empty description="暂无预留数据" />
            )}
          </ConsoleContentCard>
        </Col>
      </Row>

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
      />
    </ConsolePage>
  )
}
