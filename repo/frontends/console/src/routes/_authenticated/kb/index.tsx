import { createFileRoute, Link } from '@tanstack/react-router'
import { Button, Table, Tag } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

type KnowledgeBaseRow = {
  id: string
  status?: string
}

export const Route = createFileRoute('/_authenticated/kb/')({
  component: KBList,
})

function KBList() {
  const { data, isLoading } = useQuery({
    queryKey: ['knowledge-bases'],
    queryFn: () => api.GET('/knowledge-bases').then(({ data }) => data),
  })

  const columns = [
    { title: '名称', colKey: 'name' },
    { title: '文档数', colKey: 'doc_count' },
    { title: '状态', colKey: 'status',
      cell: ({ row }: { row: KnowledgeBaseRow }) => <Tag theme={row.status === 'active' ? 'success' : 'default'}>{row.status}</Tag> },
    { title: '操作', colKey: 'actions',
      cell: ({ row }: { row: KnowledgeBaseRow }) => (
        <Link to="/kb/$kbId/chat" params={{ kbId: String(row.id) }}>问答</Link>
      ) },
  ]

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2>知识库</h2>
        <Button>新建知识库</Button>
      </div>
      <Table loading={isLoading} data={data?.items ?? []} columns={columns} rowKey="id" />
    </div>
  )
}
