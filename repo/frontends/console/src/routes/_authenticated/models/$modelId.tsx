import { useEffect, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Empty, Skeleton, Space, Table, Tag } from 'tdesign-react'
import { ChevronLeftIcon } from 'tdesign-icons-react'
import { ConsoleContentCard, ConsolePage, ConsolePageHeader } from '@/components/shell'
import type { components } from '@/api/schema'
import { fetchModel, fetchModelVersions } from '@/features/models/modelQueries'
import { formatModelChecksum, formatModelDate, formatModelSize } from '@/features/models/modelDetail'

export const Route = createFileRoute('/_authenticated/models/$modelId')({
  component: ModelDetailPage,
})

type Model = components['schemas']['Model']
type ModelVersion = components['schemas']['ModelVersion']

const MODEL_STATUS: Record<Model['status'], { label: string; theme: 'success' | 'warning' | 'danger' | 'default' }> = {
  pending: { label: '等待中', theme: 'warning' },
  downloading: { label: '导入中', theme: 'warning' },
  ready: { label: '就绪', theme: 'success' },
  error: { label: '错误', theme: 'danger' },
  deleted: { label: '已删除', theme: 'default' },
}

function isNotFound(error: unknown): boolean {
  const candidate = error as { status?: number } | undefined
  return candidate?.status === 404
}

function errorMessage(error: unknown, fallback: string): string {
  const candidate = error as { message?: string } | undefined
  return candidate?.message || fallback
}

function ModelDetailPage() {
  const { modelId } = Route.useParams()
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const cursor = cursorStack.length > 0 ? cursorStack[cursorStack.length - 1] : undefined

  useEffect(() => {
    setCursorStack([])
  }, [modelId])

  const modelQuery = useQuery({
    queryKey: ['model', modelId],
    queryFn: () => fetchModel(modelId),
  })
  const versionsQuery = useQuery({
    queryKey: ['model-versions', modelId, cursor],
    queryFn: () => fetchModelVersions(modelId, cursor),
  })

  if (modelQuery.isLoading) {
    return (
      <ConsolePage>
        <Link to="/models">
          <Button variant="text" icon={<ChevronLeftIcon />}>返回模型列表</Button>
        </Link>
        <Skeleton animation="gradient" style={{ height: 220 }} />
      </ConsolePage>
    )
  }

  if (isNotFound(modelQuery.error)) {
    return (
      <ConsolePage>
        <Link to="/models">
          <Button variant="text" icon={<ChevronLeftIcon />}>返回模型列表</Button>
        </Link>
        <Empty description="模型不存在或无权访问" />
      </ConsolePage>
    )
  }

  if (modelQuery.isError || !modelQuery.data) {
    return (
      <ConsolePage>
        <Link to="/models">
          <Button variant="text" icon={<ChevronLeftIcon />}>返回模型列表</Button>
        </Link>
        <Alert
          theme="error"
          title="加载模型详情失败"
          message={errorMessage(modelQuery.error, '请稍后重试')}
          operation={<Button variant="outline" onClick={() => modelQuery.refetch()}>重试</Button>}
        />
      </ConsolePage>
    )
  }

  const model = modelQuery.data
  const status = MODEL_STATUS[model.status]
  const versions = (versionsQuery.data?.items ?? []) as ModelVersion[]
  const nextCursor = versionsQuery.data?.next_cursor ?? undefined

  const columns = [
    { title: '版本', colKey: 'version' },
    { title: '格式', colKey: 'format' },
    { title: '大小', colKey: 'size_bytes', cell: ({ row }: { row: ModelVersion }) => formatModelSize(row.size_bytes) },
    {
      title: 'SHA-256',
      colKey: 'checksum_sha256',
      cell: ({ row }: { row: ModelVersion }) => (
        <span title={row.checksum_sha256 ?? undefined}>{formatModelChecksum(row.checksum_sha256)}</span>
      ),
    },
    { title: '存储路径', colKey: 'storage_path', cell: ({ row }: { row: ModelVersion }) => row.storage_path || '—' },
    { title: '创建时间', colKey: 'created_at', cell: ({ row }: { row: ModelVersion }) => formatModelDate(row.created_at) },
  ]

  return (
    <ConsolePage>
      <Link to="/models">
        <Button variant="text" icon={<ChevronLeftIcon />}>返回模型列表</Button>
      </Link>

      <ConsolePageHeader
        title={model.display_name || model.name}
        subtitle={`模型 ID: ${model.id}`}
        extra={<Tag theme={status.theme} variant="light">{status.label}</Tag>}
      />

      <ConsoleContentCard title="模型信息">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 16 }}>
          <InfoItem label="名称" value={model.name} />
          <InfoItem label="来源" value={model.source} />
          <InfoItem label="能力" value={model.capabilities?.join('、') || '—'} />
          <InfoItem label="总大小" value={formatModelSize(model.total_size_bytes)} />
          <InfoItem label="创建时间" value={formatModelDate(model.created_at)} />
          <InfoItem label="更新时间" value={formatModelDate(model.updated_at)} />
        </div>
        {model.description && (
          <p style={{ margin: '16px 0 0', color: 'var(--td-text-color-secondary)', whiteSpace: 'pre-wrap' }}>
            {model.description}
          </p>
        )}
      </ConsoleContentCard>

      <ConsoleContentCard
        title="模型版本"
        actions={(
          <Space>
            <Button
              variant="outline"
              disabled={cursorStack.length === 0 || versionsQuery.isFetching}
              onClick={() => setCursorStack((stack) => stack.slice(0, -1))}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              disabled={!nextCursor || versionsQuery.isFetching}
              onClick={() => nextCursor && setCursorStack((stack) => [...stack, nextCursor])}
            >
              下一页
            </Button>
          </Space>
        )}
      >
        {versionsQuery.isError ? (
          <Alert
            theme="error"
            title="加载版本失败"
            message={errorMessage(versionsQuery.error, '请稍后重试')}
            operation={<Button variant="outline" onClick={() => versionsQuery.refetch()}>重试</Button>}
          />
        ) : versions.length === 0 && !versionsQuery.isLoading ? (
          <Empty description="暂无已登记版本" />
        ) : (
          <Table
            loading={versionsQuery.isLoading || versionsQuery.isFetching}
            data={versions}
            columns={columns}
            rowKey="id"
            bordered
          />
        )}
      </ConsoleContentCard>
    </ConsolePage>
  )
}

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ color: 'var(--td-text-color-secondary)', fontSize: 12, marginBottom: 4 }}>{label}</div>
      <div style={{ wordBreak: 'break-word' }}>{value}</div>
    </div>
  )
}
