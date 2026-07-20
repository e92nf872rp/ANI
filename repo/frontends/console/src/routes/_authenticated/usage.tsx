import { createFileRoute } from '@tanstack/react-router'
import { Table } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'

function usageWindow() {
  const end = new Date()
  const start = new Date(end)
  start.setDate(start.getDate() - 30)
  return {
    start_time: start.toISOString(),
    end_time: end.toISOString(),
  }
}

export const Route = createFileRoute('/_authenticated/usage')({
  component: UsagePage,
})

function UsagePage() {
  const window = usageWindow()
  const { data, isLoading, isError } = useQuery({
    queryKey: ['metering', 'usage', window.start_time, window.end_time],
    queryFn: () =>
      coreApi.GET('/metering/usage', {
        params: { query: window },
      }).then(({ data }) => data),
  })

  const columns = [
    { title: '资源类型', colKey: 'resource_type' },
    { title: '用量', colKey: 'total_quantity' },
    { title: '单位', colKey: 'unit' },
    { title: '周期', colKey: 'period' },
  ]

  return (
    <div>
      <h2>用量报表</h2>
      <p style={{ color: 'var(--td-text-color-secondary)', marginBottom: 16 }}>
        近 30 天租户用量（Core API `/metering/usage`）
      </p>
      {isError ? (
        <p style={{ color: 'var(--td-error-color)' }}>加载失败，请确认 gateway 已启动且已登录。</p>
      ) : (
        <Table loading={isLoading} data={data?.items ?? []} columns={columns} rowKey="resource_type" />
      )}
    </div>
  )
}
