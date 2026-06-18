import { createFileRoute } from '@tanstack/react-router'
import { Table } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'

export const Route = createFileRoute('/registry')({
  component: RegistryPage,
})

function RegistryPage() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['registry', 'projects'],
    queryFn: () => coreApi.GET('/registry/projects').then(({ data }) => data),
  })

  const columns = [
    { title: '项目', colKey: 'name' },
    { title: '公开', colKey: 'public', cell: ({ row }: { row: { public?: boolean } }) => (row.public ? '是' : '否') },
    { title: '创建时间', colKey: 'created_at' },
  ]

  return (
    <div>
      <h2>容器镜像仓库</h2>
      <p style={{ color: 'var(--td-text-color-secondary)', marginBottom: 16 }}>
        Harbor 项目列表（Core API `/registry/projects`）
      </p>
      {isError ? (
        <p style={{ color: 'var(--td-error-color)' }}>加载失败，请确认 gateway 已启动且已登录。</p>
      ) : (
        <Table loading={isLoading} data={data?.items ?? []} columns={columns} rowKey="name" />
      )}
    </div>
  )
}
