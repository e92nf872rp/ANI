import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import {
  Alert,
  Button,
  Col,
  Empty,
  Row,
  Skeleton,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { RefreshIcon } from 'tdesign-icons-react'
import { useQuery } from '@tanstack/react-query'
import ReactECharts from 'echarts-for-react'
import { ConsolePage } from '@/components/shell/ConsolePage'
import { ConsolePageHeader } from '@/components/shell/ConsolePageHeader'
import { ConsoleContentCard } from '@/components/shell/ConsoleContentCard'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/compute/gpu')({
  component: GpuManagementPage,
})

type GpuStatus = 'available' | 'in_use' | 'fault' | 'maintenance'
type GpuDevice = components['schemas']['GPUInventoryRecord']
type GpuOccupancy = components['schemas']['GPUOccupancyStats']
type ObservabilityQueryResponse = components['schemas']['ObservabilityQueryResponse']

const STATUS_THEME: Record<GpuStatus, 'success' | 'warning' | 'danger' | 'default'> = {
  available: 'success',
  in_use: 'warning',
  fault: 'danger',
  maintenance: 'default',
}

const STATUS_LABEL: Record<GpuStatus, string> = {
  available: '可用',
  in_use: '占用中',
  fault: '故障',
  maintenance: '维护中',
}

const DCGM_UTIL_PROMQL = 'avg(DCGM_FI_DEV_GPU_UTIL{job="dcgm-exporter"})'

// isForbidden inspects an openapi-fetch error for a 403 status. The error
// shape from TanStack Query wraps the ErrorResponse which carries the HTTP
// response; we narrow defensively because the runtime wrapper varies.
function isForbidden(error: unknown): boolean {
  if (!error) return false
  const anyError = error as { status?: number; response?: { status?: number } }
  return anyError.status === 403 || anyError.response?.status === 403
}

function GpuManagementPage() {
  const occupancyQuery = useQuery({
    queryKey: ['gpu-occupancy'],
    queryFn: () => coreApi.GET('/gpu-inventory/occupancy').then(({ data }) => data),
  })

  const inventoryQuery = useQuery({
    queryKey: ['gpu-inventory'],
    queryFn: () => coreApi.GET('/gpu-inventory').then(({ data }) => data),
  })

  const dcgmQuery = useQuery({
    queryKey: ['gpu-dcgm-util'],
    queryFn: () =>
      coreApi
        .GET('/observability/query', { params: { query: { query: DCGM_UTIL_PROMQL } } })
        .then(({ data }) => data),
    retry: false,
  })

  const occupancy = occupancyQuery.data
  const inventory = inventoryQuery.data
  const total = occupancy?.total ?? 0
  const isError = occupancyQuery.isError || inventoryQuery.isError

  const dcgmUtilization = useMemo(() => {
    if (dcgmQuery.isError || !dcgmQuery.data) return null
    const results = dcgmQuery.data as ObservabilityQueryResponse
    if (!results?.results?.length) return null
    return results.results[0].value
  }, [dcgmQuery])

  // Aggregate inventory by node for the "nodes" tab.
  const nodeAggregation = useMemo(() => {
    const map = new Map<
      string,
      { node_name: string; total: number; in_use: number; available: number; fault: number }
    >()
    for (const item of inventory?.items ?? []) {
      const existing = map.get(item.node_name) ?? {
        node_name: item.node_name,
        total: 0,
        in_use: 0,
        available: 0,
        fault: 0,
      }
      existing.total += 1
      if (item.status === 'in_use') existing.in_use += 1
      else if (item.status === 'available') existing.available += 1
      else if (item.status === 'fault') existing.fault += 1
      map.set(item.node_name, existing)
    }
    return Array.from(map.values())
  }, [inventory])

  const deviceColumns: PrimaryTableCol<GpuDevice>[] = [
    { title: '节点', colKey: 'node_name' },
    { title: '型号', colKey: 'gpu_type' },
    { title: '索引', colKey: 'gpu_index' },
    {
      title: '显存',
      colKey: 'memory_total_mb',
      cell: ({ row }) => (row.memory_total_mb != null ? `${row.memory_total_mb} MB` : '—'),
    },
    {
      title: '状态',
      colKey: 'status',
      cell: ({ row }) => (
        <Tag theme={STATUS_THEME[row.status] ?? 'default'} variant="light">
          {STATUS_LABEL[row.status] ?? row.status}
        </Tag>
      ),
    },
    {
      title: '占用实例',
      colKey: 'instance_id',
      cell: ({ row }) => row.instance_id ?? '—',
    },
  ]

  const nodeColumns: PrimaryTableCol<(typeof nodeAggregation)[number]>[] = [
    { title: '节点名', colKey: 'node_name' },
    { title: 'GPU 总数', colKey: 'total' },
    { title: '已用', colKey: 'in_use' },
    { title: '空闲', colKey: 'available' },
    { title: '异常', colKey: 'fault' },
  ]

  const chartOption = useMemo(() => {
    const byType = occupancy?.by_gpu_type ?? []
    return {
      tooltip: { trigger: 'axis' as const },
      xAxis: {
        type: 'category' as const,
        data: byType.map((entry) => entry.gpu_type ?? 'unknown'),
      },
      yAxis: { type: 'value' as const },
      series: [
        {
          name: '总量',
          type: 'bar' as const,
          stack: 'gpu',
          data: byType.map((entry) => entry.total ?? 0),
        },
        {
          name: '已用',
          type: 'bar' as const,
          stack: 'gpu',
          data: byType.map((entry) => entry.in_use ?? 0),
        },
        {
          name: '空闲',
          type: 'bar' as const,
          stack: 'gpu',
          data: byType.map((entry) => entry.available ?? 0),
        },
      ],
    }
  }, [occupancy])

  function refetchAll() {
    occupancyQuery.refetch()
    inventoryQuery.refetch()
    dcgmQuery.refetch()
  }

  if (isForbidden(occupancyQuery.error) || isForbidden(inventoryQuery.error)) {
    return (
      <ConsolePage>
        <Alert theme="error" message="无权查看 GPU 算力管理，请联系管理员获取权限。" />
      </ConsolePage>
    )
  }

  if (isError) {
    return (
      <ConsolePage>
        <Alert
          theme="error"
          message="加载 GPU 数据失败，请确认 Gateway 已启动且网络正常。"
          operation={<Button variant="outline" onClick={refetchAll}>重试</Button>}
        />
      </ConsolePage>
    )
  }

  const isLoading = occupancyQuery.isLoading || inventoryQuery.isLoading

  return (
    <ConsolePage>
      <ConsolePageHeader
        title="GPU 算力管理"
        subtitle="集群 GPU 设备清单与占用统计"
        actions={
          <Button variant="outline" icon={<RefreshIcon />} onClick={refetchAll}>
            刷新
          </Button>
        }
      />

      {/* KPI 5 卡 */}
      <Row gutter={16}>
        <Col span={4}>
          {isLoading ? (
            <Skeleton animation="gradient" style={{ height: 80 }} />
          ) : (
            <Statistic title="GPU 总量" value={total} />
          )}
        </Col>
        <Col span={4}>
          {isLoading ? (
            <Skeleton animation="gradient" style={{ height: 80 }} />
          ) : (
            <Statistic title="已分配" value={occupancy?.in_use ?? 0} />
          )}
        </Col>
        <Col span={4}>
          {isLoading ? (
            <Skeleton animation="gradient" style={{ height: 80 }} />
          ) : (
            <Statistic title="空闲" value={occupancy?.available ?? 0} />
          )}
        </Col>
        <Col span={4}>
          {isLoading ? (
            <Skeleton animation="gradient" style={{ height: 80 }} />
          ) : dcgmUtilization != null ? (
            <Statistic title="平均利用率" value={dcgmUtilization} unit="%" />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span style={{ color: 'var(--td-text-color-secondary)', fontSize: 13 }}>平均利用率</span>
              <Tag theme="warning" variant="light">
                监控未就绪
              </Tag>
            </div>
          )}
        </Col>
        <Col span={4}>
          {isLoading ? (
            <Skeleton animation="gradient" style={{ height: 80 }} />
          ) : (
            <Statistic title="异常" value={occupancy?.fault ?? 0} />
          )}
        </Col>
      </Row>

      {/* Empty state */}
      {!isLoading && total === 0 && (
        <ConsoleContentCard>
          <Empty description="集群暂无 GPU 设备" />
        </ConsoleContentCard>
      )}

      {total > 0 && (
        <>
          {/* 型号分布 */}
          <ConsoleContentCard title="GPU 型号分布">
            <ReactECharts option={chartOption} style={{ height: 280 }} />
          </ConsoleContentCard>

          {/* Tabs: 节点 / 设备 / 占用分布 */}
          <ConsoleContentCard>
            <Tabs placement="top">
              <Tabs.TabPanel value="nodes" label="节点">
                <Table
                  loading={inventoryQuery.isLoading}
                  data={nodeAggregation}
                  columns={nodeColumns}
                  rowKey="node_name"
                />
              </Tabs.TabPanel>
              <Tabs.TabPanel value="devices" label="设备">
                <Table
                  loading={inventoryQuery.isLoading}
                  data={inventory?.items ?? []}
                  columns={deviceColumns}
                  rowKey="id"
                />
              </Tabs.TabPanel>
              <Tabs.TabPanel value="occupancy" label="占用分布">
                <Space direction="vertical" style={{ width: '100%' }}>
                  {(occupancy?.by_gpu_type ?? []).map((entry) => (
                    <div
                      key={entry.gpu_type}
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        padding: '8px 0',
                        borderBottom: '1px solid var(--td-component-border)',
                      }}
                    >
                      <span>{entry.gpu_type ?? 'unknown'}</span>
                      <span>
                        总 {entry.total ?? 0} · 已用 {entry.in_use ?? 0} · 空闲 {entry.available ?? 0}
                      </span>
                    </div>
                  ))}
                </Space>
              </Tabs.TabPanel>
            </Tabs>
          </ConsoleContentCard>
        </>
      )}
    </ConsolePage>
  )
}
