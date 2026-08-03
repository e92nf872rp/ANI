import { Button, Popconfirm, Table, Tag } from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import type { EmailRecipient } from '@/api/notifications'

interface RecipientTableProps {
  data: EmailRecipient[]
  loading: boolean
  onEdit: (recipient: EmailRecipient) => void
  onToggleEnabled: (recipient: EmailRecipient) => void
  onDelete: (recipient: EmailRecipient) => void
}

export function RecipientTable({
  data,
  loading,
  onEdit,
  onToggleEnabled,
  onDelete,
}: RecipientTableProps) {
  const columns: PrimaryTableCol<EmailRecipient>[] = [
    {
      colKey: 'email',
      title: '邮箱',
      minWidth: 240,
    },
    {
      colKey: 'label',
      title: '备注',
      minWidth: 160,
      cell: ({ row }) => row.label ?? '—',
    },
    {
      colKey: 'enabled',
      title: '状态',
      width: 100,
      cell: ({ row }) => (
        <Tag theme={row.enabled ? 'success' : 'default'} variant="light">
          {row.enabled ? '启用' : '停用'}
        </Tag>
      ),
    },
    {
      colKey: 'operations',
      title: '操作',
      width: 200,
      cell: ({ row }) => (
        <div style={{ display: 'flex', gap: 4 }}>
          <Button variant="text" onClick={() => onEdit(row)}>
            编辑
          </Button>
          {row.enabled ? (
            <Popconfirm
              content="停用后该邮箱将不再接收邮件通知，是否继续？"
              onConfirm={() => onToggleEnabled(row)}
            >
              <Button variant="text">停用</Button>
            </Popconfirm>
          ) : (
            <Button variant="text" onClick={() => onToggleEnabled(row)}>
              启用
            </Button>
          )}
          <Popconfirm
            content="删除后不可恢复，是否继续？"
            onConfirm={() => onDelete(row)}
          >
            <Button variant="text" theme="danger">
              删除
            </Button>
          </Popconfirm>
        </div>
      ),
    },
  ]

  return (
    <Table
      data={data}
      columns={columns}
      loading={loading}
      rowKey="id"
      bordered
    />
  )
}
