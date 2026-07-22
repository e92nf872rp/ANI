import { createFileRoute } from '@tanstack/react-router'
import { Alert, Button, Card, Col, Empty, Row, Skeleton, Statistic, Table, Tabs, Tag } from 'tdesign-react'
import { RefreshIcon } from 'tdesign-icons-react'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/ops/gpu-pool')({
  component: GpuPoolPage,
})

type GPUOccupancyStats = components['schemas']['GPUOccupancyStats']
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']

interface NodeAggregate {
  node_name: string
  total: number
  in_use: number
  available: number
  fault: number
}

interface FaultDevice {
  key: string
  node_name: string
  gpu_type: string
  gpu_index: number
  status: string
}

function GpuPoolPage() {
  const [activeTab, setActiveTab] = useState('nodes')

  const inventoryQuery = useQuery({
    queryKey: ['boss-gpu-inventory'],
    queryFn: () => coreApi.GET('/gpu-inventory').then(({ data }) => data),
    retry: false,
  })

  const occupancyQuery = useQuery({
    queryKey: ['boss-gpu-occupancy'],
    queryFn: () => coreApi.GET('/gpu-inventory/occupancy').then(({ data }) => data),
    retry: false,
  })

  const queueQuery = useQuery({
    queryKey: ['boss-gpu-queues'],
    queryFn: () => coreApi.GET('/gpu-scheduling/queues').then(({ data }) => data),
    retry: false,
  })

  const inventoryList = inventoryQuery.data?.items ?? []
  const occupancy = occupancyQuery.data as GPUOccupancyStats | undefined
  const queues = (queueQuery.data as { items: GPUSchedulingQueue[] } | undefined)?.items ?? []

  const isForbidden =
    inventoryQuery.error?.message.includes('403') ||
    occupancyQuery.error?.message.includes('403')

  const refetchAll = () => {
    inventoryQuery.refetch()
    occupancyQuery.refetch()
    queueQuery.refetch()
  }

  // 空集群
  if (!occupancyQuery.isLoading && !occupancyQuery.isError && occupancy?.total === 0) {
    return (
      <div>
        <h2 style={{ marginBottom: 16 }}>GPU 资源池管理</h2>
        <Alert theme="info" message="本页展示全平台 GPU 资源池。租户内资源请前往 Console「GPU 算力管理」。" style={{ marginBottom: 16 }} />
        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col span={6}><Card><Statistic title="总量" value={0} /></Card></Col>
          <Col span={6}><Card><Statistic title="已分配" value={0} /></Card></Col>
          <Col span={6}><Card><Statistic title="空闲" value={0} /></Card></Col>
          <Col span={6}><Card><Statistic title="异常" value={0} /></Card></Col>
        </Row>
        <Empty description="集群暂无 GPU 设备" />
      </div>
    )
  }

  // forbidden
  if (isForbidden) {
    return (
      <div>
        <h2 style={{ marginBottom: 16 }}>GPU 资源池管理</h2>
        <Alert theme="error" message="无权查看平台 GPU 资源池" />
      </div>
    )
  }

  // error
  if (inventoryQuery.isError && occupancyQuery.isError) {
    return (
      <div>
        <h2 style={{ marginBottom: 16 }}>GPU 资源池管理</h2>
        <Alert
          theme="error"
          message={`数据加载失败：${inventoryQuery.error?.message ?? ''}`}
          operation={<Button variant="outline" onClick={refetchAll}>重试</Button>}
        />
      </div>
    )
  }

  // 聚合节点数据
  const nodeMap = new Map<string, NodeAggregate>()
  const faultDevices: FaultDevice[] = []
  for (const item of inventoryList) {
    const existing = nodeMap.get(item.node_name) ?? {
      node_name: item.node_name,
      total: 0,
      in_use: 0,
      available: 0,
      fault: 0,
    }
    existing.total++
    if (item.status === 'in_use') existing.in_use++
    else if (item.status === 'available') existing.available++
    else if (item.status === 'fault' || item.status === 'maintenance') {
      existing.fault++
      faultDevices.push({
        key: `${item.node_name}-${item.gpu_index}`,
        node_name: item.node_name,
        gpu_type: item.gpu_type,
        gpu_index: item.gpu_index,
        status: item.status,
      })
    }
    nodeMap.set(item.node_name, existing)
  }
  const nodeData = Array.from(nodeMap.values())

  const total = occupancy?.total ?? 0
  const inUse = occupancy?.in_use ?? 0
  const available = occupancy?.available ?? 0
  const fault = occupancy?.fault ?? 0
  const byGpuType = occupancy?.by_gpu_type ?? []

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>GPU 资源池管理</h2>
        <Button variant="outline" icon={<RefreshIcon />} onClick={refetchAll} loading={occupancyQuery.isFetching}>
          刷新
        </Button>
      </div>

      <Alert theme="info" message="本页展示全平台 GPU 资源池。租户内资源请前往 Console「GPU 算力管理」。" style={{ marginBottom: 16 }} />

      {/* partial-data warning */}
      {inventoryQuery.isError && !occupancyQuery.isError && (
        <Alert theme="warning" message="Inventory 数据加载失败，仅显示占用统计" style={{ marginBottom: 16 }} />
      )}
      {!inventoryQuery.isError && occupancyQuery.isError && (
        <Alert theme="warning" message="Occupancy 数据加载失败，仅显示设备清单" style={{ marginBottom: 16 }} />
      )}

      {/* KPI 4 卡 */}
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="总量" value={total} />}
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="已分配" value={inUse} />}
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="空闲" value={available} />}
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="异常" value={fault} />}
          </Card>
        </Col>
      </Row>

      {/* 型号分布 */}
      {byGpuType.length > 0 && (
        <Card title="型号分布" style={{ marginBottom: 16 }}>
          <Row gutter={16}>
            {byGpuType.map((t, i) => (
              <Col span={6} key={i}>
                <div style={{ textAlign: 'center', padding: '12px 0' }}>
                  <div style={{ fontSize: 18, fontWeight: 600 }}>{t.gpu_type ?? '—'}</div>
                  <div style={{ color: 'var(--td-text-color-secondary)', marginTop: 4 }}>
                    总量 {t.total ?? 0} · 已用 {t.in_use ?? 0} · 空闲 {t.available ?? 0}
                  </div>
                </div>
              </Col>
            ))}
          </Row>
        </Card>
      )}

      {/* Tabs */}
      <Tabs value={activeTab} onChange={(v) => setActiveTab(v as string)}>
        <Tabs.TabPanel value="nodes" label="节点">
          <Table
            data={nodeData}
            loading={inventoryQuery.isLoading}
            rowKey="node_name"
            columns={[
              { colKey: 'node_name', title: '节点', minWidth: 200 },
              { colKey: 'total', title: 'GPU 总数', width: 120 },
              { colKey: 'in_use', title: '已用', width: 100 },
              { colKey: 'available', title: '空闲', width: 100 },
              { colKey: 'fault', title: '异常数', width: 100 },
            ]}
          />
        </Tabs.TabPanel>

        <Tabs.TabPanel value="faults" label={`异常设备${faultDevices.length > 0 ? ` (${faultDevices.length})` : ''}`}>
          <Table
            data={faultDevices}
            loading={inventoryQuery.isLoading}
            rowKey="key"
            columns={[
              { colKey: 'node_name', title: '节点', minWidth: 200 },
              { colKey: 'gpu_type', title: '型号', width: 200 },
              { colKey: 'gpu_index', title: 'GPU 索引', width: 100 },
              {
                colKey: 'status',
                title: '状态',
                width: 120,
                cell: ({ row }) => (
                  <Tag theme={row.status === 'fault' ? 'danger' : 'warning'} variant="light">
                    {row.status}
                  </Tag>
                ),
              },
            ]}
          />
        </Tabs.TabPanel>

        <Tabs.TabPanel value="queues" label="调度队列（只读）">
          <Table
            data={queues}
            loading={queueQuery.isLoading}
            rowKey="id"
            columns={[
              { colKey: 'name', title: '队列名', minWidth: 200 },
              { colKey: 'workload_class', title: '负载类别', width: 120 },
              { colKey: 'weight', title: '权重', width: 100 },
              {
                colKey: 'reclaimable',
                title: '可回收',
                width: 100,
                cell: ({ row }) => (row.reclaimable ? '是' : '否'),
              },
              {
                colKey: 'scope',
                title: '范围',
                width: 120,
                cell: ({ row }) => (
                  row.is_platform_default ? <Tag theme="primary" variant="light">平台默认</Tag> : <span>租户</span>
                ),
              },
            ]}
          />
        </Tabs.TabPanel>
      </Tabs>

      {/* 租户排行占位 */}
      <Card title="租户排行" style={{ marginTop: 16 }}>
        <Alert theme="info" message="租户排行功能将在后续版本提供。" />
      </Card>
    </div>
  )
}
