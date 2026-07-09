import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { Select, Table, Tag } from 'tdesign-react'
import type { PrimaryTableCol, SelectValue } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/gpu-inventory')({
  component: GpuInventoryPage,
})

// GPUInventoryRecord 与 query.status 共享的状态枚举（OpenAPI 真实来源）
type GpuStatus = 'available' | 'in_use' | 'fault' | 'maintenance'
type GpuDevice = components['schemas']['GPUInventoryRecord']

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

function GpuInventoryPage() {
  const [gpuType, setGpuType] = useState<string>('')
  const [status, setStatus] = useState<GpuStatus | ''>('')

  const { data, isLoading, isError } = useQuery({
    queryKey: ['gpu-inventory', gpuType, status],
    queryFn: () =>
      coreApi
        .GET('/gpu-inventory', {
          params: {
            query: {
              gpu_type: gpuType || undefined,
              status: (status || undefined) as GpuStatus | undefined,
            },
          },
        })
        .then(({ data }) => data),
  })

  // 从已加载数据派生筛选选项，避免额外请求
  const gpuTypeOptions = useMemo(() => {
    const set = new Set<string>()
    data?.items?.forEach((item) => set.add(item.gpu_type))
    return Array.from(set).map((v) => ({ label: v, value: v }))
  }, [data])

  const columns: PrimaryTableCol<GpuDevice>[] = [
    { title: '节点', colKey: 'node_name' },
    { title: 'GPU 型号', colKey: 'gpu_type' },
    { title: '索引', colKey: 'gpu_index' },
    { title: '显存 (MB)', colKey: 'memory_total_mb' },
    { title: '驱动版本', colKey: 'driver_version' },
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
      title: '归属实例',
      colKey: 'instance_id',
      // spec §3 提及跳转 kind=gpu_container 实例详情，但实例详情页尚未落地，此处先只读展示
      cell: ({ row }) => row.instance_id ?? '-',
    },
  ]

  return (
    <div>
      <h2>GPU 设备清单</h2>
      <p style={{ color: 'var(--td-text-color-secondary)', marginBottom: 16 }}>
        集群 GPU 设备清单与占用（Core API <code>/gpu-inventory</code>）
      </p>

      <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
        <Select
          value={gpuType}
          onChange={(val: SelectValue) => setGpuType((val as string) ?? '')}
          options={gpuTypeOptions}
          placeholder="按 GPU 型号筛选"
          clearable
          style={{ width: 220 }}
        />
        <Select
          value={status}
          onChange={(val: SelectValue) => setStatus((val as GpuStatus) ?? '')}
          options={(Object.keys(STATUS_LABEL) as GpuStatus[]).map((value) => ({
            label: STATUS_LABEL[value],
            value,
          }))}
          placeholder="按状态筛选"
          clearable
          style={{ width: 180 }}
        />
      </div>

      {isError ? (
        <p style={{ color: 'var(--td-error-color)' }}>加载失败，请确认 gateway 已启动且已登录。</p>
      ) : (
        <Table
          loading={isLoading}
          data={data?.items ?? []}
          columns={columns}
          rowKey="id"
        />
      )}
    </div>
  )
}
