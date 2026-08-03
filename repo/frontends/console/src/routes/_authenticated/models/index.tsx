import { createFileRoute, Link } from '@tanstack/react-router'
import { Button, Table, Tag, Space } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

type ModelRow = {
  status?: string
  capabilities?: string[]
}

export const Route = createFileRoute('/_authenticated/models/')({
  component: ModelList,
})

function ModelList() {
  const { data, isLoading } = useQuery({
    queryKey: ['models'],
    queryFn: () => api.GET('/models').then(({ data }) => data),
  })

  const columns = [
    { title: '名称', colKey: 'display_name' },
    { title: '状态', colKey: 'status',
      cell: ({ row }: { row: ModelRow }) => <Tag theme={row.status === 'ready' ? 'success' : 'default'}>{row.status}</Tag> },
    { title: '能力', colKey: 'capabilities',
      cell: ({ row }: { row: ModelRow }) => row.capabilities?.join(', ') },
    { title: '操作', colKey: 'actions',
      cell: () => <span style={{ color: 'var(--td-text-color-placeholder)' }}>详情（开发中）</span> },
  ]

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2>模型管理</h2>
        <Space>
          <Link to="/models/import"><Button>导入模型</Button></Link>
        </Space>
      </div>
      <Table loading={isLoading} data={data?.items ?? []} columns={columns} rowKey="id" />
    </div>
  )
}
