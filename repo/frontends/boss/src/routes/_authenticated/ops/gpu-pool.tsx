import { createFileRoute } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Card,
  Col,
  Empty,
  MessagePlugin,
  Popconfirm,
  Row,
  Skeleton,
  Statistic,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'tdesign-react'
import { AddIcon, RefreshIcon } from 'tdesign-icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { coreApi, newIdempotencyKey } from '@/api/coreClient'
import type { components } from '@/api/core-schema'
import { GpuSpecDrawer } from './-gpu-spec-drawer'
import { GpuPoolQuotaDrawer } from './-gpu-pool-quota-drawer'

export const Route = createFileRoute('/_authenticated/ops/gpu-pool')({
  component: GpuPoolPage,
})

type GPUOccupancyStats = components['schemas']['GPUOccupancyStats']
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']
type GPUInventoryRecord = components['schemas']['GPUInventoryRecord']
type GPUSpecSummary = components['schemas']['GPUSpecSummary']
type Quota = components['schemas']['Quota']

// 节点聚合行：由 inventory 按节点聚合派生，含 gpu_mode/gpu_spec 只读标签
interface NodeAggregate {
  node_name: string
  gpu_mode: string | null
  gpu_spec: string | null
  gpu_sharing_spec: string | null
  gpu_sharing_policy: string | null
  total: number
  in_use: number
  available: number
  fault: number
}

// 异常设备行：status ∈ {fault, maintenance}
interface FaultDevice {
  key: string
  device_id: string
  node_name: string
  gpu_type: string
  gpu_index: number
  status: 'fault' | 'maintenance'
}

// 设备状态 → Tag theme 映射（UX §7.3：maintenance=warning, fault=danger, 空闲=success）
function deviceStatusTheme(status: string): 'success' | 'warning' | 'danger' | 'default' {
  switch (status) {
    case 'available':
      return 'success'
    case 'maintenance':
      return 'warning'
    case 'fault':
      return 'danger'
    default:
      return 'default'
  }
}

function deviceStatusCopy(status: string): string {
  switch (status) {
    case 'available':
      return '空闲'
    case 'in_use':
      return '使用中'
    case 'maintenance':
      return '维护中'
    case 'fault':
      return '异常'
    default:
      return status
  }
}

// GPU 模式 → Tag theme（UX §7.3：wholecard=default, vgpu=primary）
function gpuModeTheme(mode: string | null): 'default' | 'primary' {
  return mode === 'vgpu' ? 'primary' : 'default'
}

function gpuModeCopy(mode: string | null): string {
  if (mode === 'wholecard') return '整卡'
  if (mode === 'vgpu') return 'vGPU'
  return mode ?? '—'
}

function GpuPoolPage() {
  const [activeTab, setActiveTab] = useState('nodes')
  const [quotaDrawerVisible, setQuotaDrawerVisible] = useState(false)
  const [quotaTargetTenant, setQuotaTargetTenant] = useState<string>('')
  const [specDrawerVisible, setSpecDrawerVisible] = useState(false)

  const qc = useQueryClient()

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

  const specsQuery = useQuery({
    queryKey: ['boss-gpu-specs'],
    queryFn: () => coreApi.GET('/gpu-specs').then(({ data }) => data),
    retry: false,
  })

  // BOSS 分页配额列表，兼作"配额分配"Drawer 的租户列表来源
  const quotasQuery = useQuery({
    queryKey: ['boss-quotas'],
    queryFn: () => coreApi.GET('/quotas').then(({ data }) => data),
    retry: false,
  })

  const inventoryList: GPUInventoryRecord[] = useMemo(
    () => inventoryQuery.data?.items ?? [],
    [inventoryQuery.data],
  )
  const occupancy = occupancyQuery.data as GPUOccupancyStats | undefined
  const queues = (queueQuery.data as { items: GPUSchedulingQueue[] } | undefined)?.items ?? []
  const specs = (specsQuery.data as { items: GPUSpecSummary[] } | undefined)?.items ?? []
  const quotas: Quota[] = useMemo(
    () => (quotasQuery.data as { items: Quota[] } | undefined)?.items ?? [],
    [quotasQuery.data],
  )

  const isForbidden =
    inventoryQuery.error?.message.includes('403') ||
    occupancyQuery.error?.message.includes('403')

  const refetchAll = () => {
    inventoryQuery.refetch()
    occupancyQuery.refetch()
    queueQuery.refetch()
    specsQuery.refetch()
    quotasQuery.refetch()
  }

  // 聚合节点数据 + 派生 gpu_mode/gpu_spec 只读标签
  const { nodeData, faultDevices, wholecardNodes, vgpuNodes, wholecardGpuTypeOptions, vgpuGpuTypeOptions } = useMemo(() => {
    const nodeMap = new Map<string, NodeAggregate>()
    const faults: FaultDevice[] = []
    const wholecardNodeSet = new Set<string>()
    const vgpuNodeSet = new Set<string>()
    const wholecardGpuTypeSet = new Set<string>()
    const vgpuGpuTypeSet = new Set<string>()

    for (const item of inventoryList) {
      const existing = nodeMap.get(item.node_name) ?? {
        node_name: item.node_name,
        gpu_mode: item.gpu_mode ?? null,
        gpu_spec: item.gpu_spec ?? null,
        gpu_sharing_spec: item.gpu_sharing_spec ?? null,
        gpu_sharing_policy: item.gpu_sharing_policy ?? null,
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
        faults.push({
          key: `${item.node_name}-${item.gpu_index}`,
          device_id: item.id,
          node_name: item.node_name,
          gpu_type: item.gpu_type,
          gpu_index: item.gpu_index,
          status: item.status as 'fault' | 'maintenance',
        })
      }
      // 按节点 gpu_mode 分类（同节点同型号假设下，取首个设备标签即可）
      if (item.gpu_mode === 'wholecard') wholecardNodeSet.add(item.node_name)
      else if (item.gpu_mode === 'vgpu') vgpuNodeSet.add(item.node_name)
      // 收集 gpu_type 供规格创建 Drawer 选择（按模式分离，对齐节点标签）
      // wholecard 节点用 gpu_spec，vgpu 节点用 gpu_sharing_spec
      if (item.gpu_mode === 'wholecard' && item.gpu_spec) wholecardGpuTypeSet.add(item.gpu_spec)
      else if (item.gpu_mode === 'vgpu' && item.gpu_sharing_spec) vgpuGpuTypeSet.add(item.gpu_sharing_spec)
      nodeMap.set(item.node_name, existing)
    }
    return {
      nodeData: Array.from(nodeMap.values()),
      faultDevices: faults,
      wholecardNodes: wholecardNodeSet.size,
      vgpuNodes: vgpuNodeSet.size,
      wholecardGpuTypeOptions: Array.from(wholecardGpuTypeSet),
      vgpuGpuTypeOptions: Array.from(vgpuGpuTypeSet),
    }
  }, [inventoryList])

  // 租户选项：从 /quotas 列表派生，label 优先用租户名称
  const tenantOptions = useMemo(
    () => quotas.map((q) => ({ label: q.tenant_name || q.tenant_id, value: q.tenant_id })),
    [quotas],
  )

  // 当前选中租户的已有配额（用于 Drawer 回填）
  const currentTenantQuota = useMemo(
    () => quotas.find((q) => q.tenant_id === quotaTargetTenant),
    [quotas, quotaTargetTenant],
  )
  const currentGpuCountQuota = currentTenantQuota?.items?.find(
    (it) => it.resource_type === 'gpu_count',
  )

  // 设备状态翻转 mutation（标维护 / 恢复空闲）
  const deviceStatusMutation = useMutation({
    mutationFn: async ({
      device_id,
      status,
    }: {
      device_id: string
      status: 'maintenance' | 'idle'
    }) => {
      const idempotencyKey = newIdempotencyKey()
      const { data, error } = await coreApi.PATCH('/gpu-inventory/{device_id}', {
        params: {
          header: { 'Idempotency-Key': idempotencyKey },
          path: { device_id },
        },
        body: { status },
      })
      if (error) throw new Error(error?.message ?? '设备状态更新失败')
      return data
    },
    onSuccess: (_data, variables) => {
      MessagePlugin.success(
        variables.status === 'maintenance' ? '设备已标记维护' : '设备已恢复空闲',
      )
      qc.invalidateQueries({ queryKey: ['boss-gpu-inventory'] })
      qc.invalidateQueries({ queryKey: ['boss-gpu-occupancy'] })
    },
    onError: (err: Error) => {
      MessagePlugin.error(err.message || '设备状态更新失败')
    },
  })

  // 删除规格 mutation（UX §3.1 Flow 3b）
  const deleteSpecMutation = useMutation({
    mutationFn: async (specId: string) => {
      const idempotencyKey = newIdempotencyKey()
      const { error } = await coreApi.DELETE('/gpu-specs/{spec_id}', {
        params: {
          header: { 'Idempotency-Key': idempotencyKey },
          path: { spec_id: specId },
        },
      })
      if (error) throw new Error(error?.message ?? '规格删除失败')
      return specId
    },
    onSuccess: () => {
      MessagePlugin.success('规格已删除')
      qc.invalidateQueries({ queryKey: ['boss-gpu-specs'] })
    },
    onError: (err: Error) => {
      MessagePlugin.error(err.message || '规格删除失败')
    },
  })

  // 空集群
  if (!occupancyQuery.isLoading && !occupancyQuery.isError && occupancy?.total === 0) {
    return (
      <div>
        <h2 style={{ marginBottom: 16 }}>GPU 资源池管理</h2>
        <Alert theme="info" message="本页展示全平台 GPU 资源池。租户内资源请前往 Console「GPU 算力管理」。" style={{ marginBottom: 16 }} />
        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col span={4}><Card><Statistic title="总量" value={0} /></Card></Col>
          <Col span={4}><Card><Statistic title="已分配" value={0} /></Card></Col>
          <Col span={4}><Card><Statistic title="空闲" value={0} /></Card></Col>
          <Col span={4}><Card><Statistic title="异常" value={0} /></Card></Col>
          <Col span={4}><Card><Statistic title="整卡节点" value={0} /></Card></Col>
          <Col span={4}><Card><Statistic title="vGPU 节点" value={0} /></Card></Col>
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

      {/* KPI 6 卡：总量 | 已分配 | 空闲 | 异常 | 整卡节点 | vGPU 节点 */}
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="总量" value={total} />}
          </Card>
        </Col>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="已分配" value={inUse} />}
          </Card>
        </Col>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="空闲" value={available} />}
          </Card>
        </Col>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="异常" value={fault} />}
          </Card>
        </Col>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="整卡节点" value={wholecardNodes} />}
          </Card>
        </Col>
        <Col span={4}>
          <Card>
            {occupancyQuery.isLoading ? <Skeleton /> : <Statistic title="vGPU 节点" value={vgpuNodes} />}
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

      {/* Tabs: 节点聚合 | 异常设备 | 调度队列 | 规格目录 */}
      <Tabs value={activeTab} onChange={(v) => setActiveTab(v as string)}>
        {/* Tab 1: 节点聚合（扩展 gpu_mode/gpu_spec 列 + 配额分配入口） */}
        <Tabs.TabPanel value="nodes" label="节点聚合">
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12, gap: 8 }}>
            <Button
              theme="primary"
              onClick={() => {
                setQuotaTargetTenant('')
                setQuotaDrawerVisible(true)
              }}
            >
              配额分配
            </Button>
          </div>
          <Table
            data={nodeData}
            loading={inventoryQuery.isLoading}
            rowKey="node_name"
            columns={[
              { colKey: 'node_name', title: '节点', minWidth: 200 },
              {
                colKey: 'gpu_mode',
                title: 'GPU 模式',
                width: 120,
                cell: ({ row }) => (
                  <Tag theme={gpuModeTheme(row.gpu_mode)} variant="light">
                    {gpuModeCopy(row.gpu_mode)}
                  </Tag>
                ),
              },
              {
                colKey: 'gpu_spec',
                title: 'GPU 规格',
                width: 200,
                cell: ({ row }) =>
                  row.gpu_spec ?? row.gpu_sharing_spec ?? row.gpu_sharing_policy ?? '—',
              },
              { colKey: 'total', title: 'GPU 总数', width: 120 },
              { colKey: 'in_use', title: '已用', width: 100 },
              { colKey: 'available', title: '空闲', width: 100 },
              { colKey: 'fault', title: '异常数', width: 100 },
              {
                colKey: 'op',
                title: '操作',
                width: 120,
                cell: () => (
                  <Button
                    variant="text"
                    onClick={() => {
                      setQuotaTargetTenant('')
                      setQuotaDrawerVisible(true)
                    }}
                  >
                    配额分配
                  </Button>
                ),
              },
            ]}
          />
        </Tabs.TabPanel>

        {/* Tab 2: 异常设备（新增操作列 标维护 / 恢复空闲） */}
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
                  <Tag theme={deviceStatusTheme(row.status)} variant="light">
                    {deviceStatusCopy(row.status)}
                  </Tag>
                ),
              },
              {
                colKey: 'op',
                title: '操作',
                width: 140,
                cell: ({ row }) =>
                  row.status === 'maintenance' ? (
                    <Button
                      variant="text"
                      loading={deviceStatusMutation.isPending}
                      onClick={() =>
                        deviceStatusMutation.mutate({
                          device_id: row.device_id,
                          status: 'idle',
                        })
                      }
                    >
                      恢复空闲
                    </Button>
                  ) : (
                    <Button
                      variant="text"
                      loading={deviceStatusMutation.isPending}
                      onClick={() =>
                        deviceStatusMutation.mutate({
                          device_id: row.device_id,
                          status: 'maintenance',
                        })
                      }
                    >
                      标记维护
                    </Button>
                  ),
              },
            ]}
          />
        </Tabs.TabPanel>

        {/* Tab 3: 调度队列（新增 allocated 列展示 Queue status） */}
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
              {
                colKey: 'allocated',
                title: '已分配',
                width: 180,
                cell: ({ row }) => {
                  const allocated = row.status?.allocated
                  if (!allocated || Object.keys(allocated).length === 0) return '—'
                  return (
                    <span>
                      {Object.entries(allocated).map(([k, v]) => (
                        <Tag key={k} variant="light" style={{ marginRight: 4, marginBottom: 4 }}>
                          {k}: {v}
                        </Tag>
                      ))}
                    </span>
                  )
                },
              },
              {
                colKey: 'state',
                title: '状态',
                width: 100,
                cell: ({ row }) => {
                  const state = row.status?.state ?? 'unknown'
                  if (state === 'open') return <Tag theme="success" variant="light">open</Tag>
                  if (state === 'closed') return <Tag theme="default" variant="light">closed</Tag>
                  return <Tag theme="default" variant="light">unknown</Tag>
                },
              },
            ]}
          />
        </Tabs.TabPanel>

        {/* Tab 4: 规格目录（新增"新建规格"Button + 删除操作列 + Popconfirm） */}
        <Tabs.TabPanel value="specs" label="规格目录">
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12, gap: 8 }}>
            <Button theme="primary" icon={<AddIcon />} onClick={() => setSpecDrawerVisible(true)}>
              新建规格
            </Button>
          </div>
          <Table
            data={specs}
            loading={specsQuery.isLoading}
            rowKey="id"
            columns={[
              { colKey: 'id', title: '规格 ID', minWidth: 220 },
              { colKey: 'gpu_type', title: 'GPU 型号', width: 200 },
              {
                colKey: 'gpu_mode',
                title: 'GPU 模式',
                width: 120,
                cell: ({ row }) => (
                  <Tag theme={gpuModeTheme(row.gpu_mode ?? null)} variant="light">
                    {gpuModeCopy(row.gpu_mode ?? null)}
                  </Tag>
                ),
              },
              { colKey: 'shares', title: '切分数', width: 100 },
              { colKey: 'mb_per_share', title: '每份显存 (MiB)', width: 140 },
              {
                colKey: 'available',
                title: '可用',
                width: 100,
                cell: ({ row }) => (row.available ? '是' : '否'),
              },
              {
                colKey: 'op',
                title: '操作',
                width: 120,
                cell: ({ row }) =>
                  row.available ? (
                    <Popconfirm
                      theme="danger"
                      content="确定删除此规格？"
                      onConfirm={() => deleteSpecMutation.mutate(row.id)}
                    >
                      <Button
                        variant="text"
                        theme="danger"
                        loading={deleteSpecMutation.isPending}
                      >
                        删除
                      </Button>
                    </Popconfirm>
                  ) : (
                    <Tooltip content="该规格有运行中实例引用，无法删除" placement="top">
                      <Button variant="text" theme="danger" disabled>
                        删除
                      </Button>
                    </Tooltip>
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

      {/* 配额分配 Drawer（独立组件：设 total + allocated_gpu_count） */}
      <GpuPoolQuotaDrawer
        visible={quotaDrawerVisible}
        tenantOptions={tenantOptions}
        tenantId={quotaTargetTenant}
        onTenantChange={setQuotaTargetTenant}
        currentTotal={currentGpuCountQuota?.total ?? 0}
        onClose={() => setQuotaDrawerVisible(false)}
      />

      {/* 规格管理 Drawer（独立组件：新建规格） */}
      <GpuSpecDrawer
        visible={specDrawerVisible}
        wholecardGpuTypeOptions={wholecardGpuTypeOptions}
        vgpuGpuTypeOptions={vgpuGpuTypeOptions}
        inventoryList={inventoryList}
        onClose={() => setSpecDrawerVisible(false)}
      />
    </div>
  )
}
