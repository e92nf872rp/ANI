import { createFileRoute } from '@tanstack/react-router'
import { Card, Col, Row, Statistic } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

export const Route = createFileRoute('/_authenticated/')({
  component: Dashboard,
})

function Dashboard() {
  const { data: services } = useQuery({
    queryKey: ['inference-services'],
    queryFn: () => api.GET('/inference-services').then(({ data }) => data),
  })

  const runningCount = services?.items?.filter((item) => item.status === 'running').length ?? 0

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>仪表盘</h2>
      <Row gutter={16}>
        <Col span={6}>
          <Card>
            <Statistic title="运行中的推理服务" value={runningCount} />
          </Card>
        </Col>
        {/* GPU 资源卡片、知识库调用量图表等后续实现 */}
      </Row>
    </div>
  )
}
