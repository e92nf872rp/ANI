import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Card, Col, Row, Statistic, Skeleton, Tag } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { coreApi } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

export const Route = createFileRoute('/')({
  component: Dashboard,
})

type GpuOccupancy = components['schemas']['GPUOccupancyStats']

const DCGM_UTIL_PROMQL = 'avg(DCGM_FI_DEV_GPU_UTIL{job="dcgm-exporter"})'

function Dashboard() {
  const navigate = useNavigate()

  const { data: services } = useQuery({
    queryKey: ['inference-services'],
    queryFn: () => api.GET('/inference-services').then(({ data }) => data),
  })

  const runningCount = services?.items?.filter((item) => item.status === 'running').length ?? 0

  const occupancyQuery = useQuery({
    queryKey: ['gpu-occupancy'],
    queryFn: () => coreApi.GET('/gpu-inventory/occupancy').then(({ data }) => data),
  })

  const dcgmQuery = useQuery({
    queryKey: ['gpu-dcgm-util'],
    queryFn: () =>
      coreApi
        .GET('/observability/query', { params: { query: { query: DCGM_UTIL_PROMQL } } })
        .then(({ data }) => data),
    retry: false,
  })

  const occupancy = occupancyQuery.data as GpuOccupancy | undefined
  const total = occupancy?.total ?? 0
  const inUse = occupancy?.in_use ?? 0
  const available = occupancy?.available ?? 0
  const fault = occupancy?.fault ?? 0
  const dcgmReady = !dcgmQuery.isError && !!dcgmQuery.data

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>仪表盘</h2>
      <Row gutter={16}>
        <Col span={6}>
          <Card>
            <Statistic title="运行中的推理服务" value={runningCount} />
          </Card>
        </Col>
        <Col span={6}>
          <div
            style={{ cursor: 'pointer' }}
            onClick={() => navigate({ to: '/compute/gpu' })}
          >
            <Card>
            {occupancyQuery.isLoading ? (
              <Skeleton loading style={{ width: '100%' }} />
            ) : occupancyQuery.isError ? (
              <div style={{ color: 'var(--td-text-color-disabled)' }}>数据加载失败</div>
            ) : (
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                  <span style={{ fontSize: 14, color: 'var(--td-text-color-secondary)' }}>
                    GPU 资源
                  </span>
                  {!dcgmReady && (
                    <Tag theme="warning" variant="light">
                      监控未就绪
                    </Tag>
                  )}
                </div>
                <Row gutter={8}>
                  <Col span={6}>
                    <Statistic title="总量" value={total} />
                  </Col>
                  <Col span={6}>
                    <Statistic title="已分配" value={inUse} />
                  </Col>
                  <Col span={6}>
                    <Statistic title="空闲" value={available} />
                  </Col>
                  <Col span={6}>
                    <Statistic title="异常" value={fault} />
                  </Col>
                </Row>
              </div>
            )}
            </Card>
          </div>
        </Col>
      </Row>
    </div>
  )
}
