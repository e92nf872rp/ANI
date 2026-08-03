import { createFileRoute } from '@tanstack/react-router'
import { Table } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { coreApi } from '@/api/coreClient'

export const Route = createFileRoute('/_authenticated/settings/api-keys')({
  component: ApiKeysPage,
})

function ApiKeysPage() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['auth', 'api-keys'],
    queryFn: () => coreApi.GET('/auth/api-keys').then(({ data }) => data),
  })

  const columns = [
    { title: '名称', colKey: 'name' },
    { title: 'Key 前缀', colKey: 'key_prefix' },
    { title: '创建时间', colKey: 'created_at' },
    { title: '过期时间', colKey: 'expires_at' },
  ]

  return (
    <div>
      <h2>API Key 管理</h2>
      <p style={{ color: 'var(--td-text-color-secondary)', marginBottom: 16 }}>
        当前租户 API Key（Core API `/auth/api-keys`）
      </p>
      {isError ? (
        <p style={{ color: 'var(--td-error-color)' }}>加载失败，请确认 gateway 已启动且已登录。</p>
      ) : (
        <Table loading={isLoading} data={data?.items ?? []} columns={columns} rowKey="id" />
      )}
    </div>
  )
}
