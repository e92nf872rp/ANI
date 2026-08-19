import { Button, Popconfirm, Table } from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import type { TenantPlanListItem } from '@/api/tenant-plans'
import { PlanStatusTag, formatDateTime } from './planStatus'

interface PlanTableProps {
  data: TenantPlanListItem[]
  loading: boolean
  canWrite: boolean
  onDetail: (plan: TenantPlanListItem) => void
  onActivate: (plan: TenantPlanListItem) => void
  onDisable: (plan: TenantPlanListItem) => void
  onDelete: (plan: TenantPlanListItem) => void
}

/** 对齐产品原型-7.23 `/boss/tenants/quotas`：套餐 · 编码 · 状态 · 绑定租户 · 更新时间 · 操作 */
export function PlanTable({
  data,
  loading,
  canWrite,
  onDetail,
  onActivate,
  onDisable,
  onDelete,
}: PlanTableProps) {
  const columns: PrimaryTableCol<TenantPlanListItem>[] = [
    { colKey: 'name', title: '套餐', minWidth: 160 },
    { colKey: 'code', title: '编码', minWidth: 140 },
    {
      colKey: 'status',
      title: '状态',
      width: 100,
      cell: ({ row }) => <PlanStatusTag status={row.status} />,
    },
    {
      colKey: 'tenant_count',
      title: '绑定租户',
      width: 100,
    },
    {
      colKey: 'updated_at',
      title: '更新时间',
      minWidth: 160,
      cell: ({ row }) => formatDateTime(row.updated_at),
    },
    {
      colKey: 'operations',
      title: '操作',
      width: canWrite ? 220 : 80,
      cell: ({ row }) => (
        <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          <Button variant="text" onClick={() => onDetail(row)}>
            详情
          </Button>
          {canWrite && (row.status === 'draft' || row.status === 'disabled') && (
            <Popconfirm
              content="确认发布该套餐？发布后可被新租户引用。"
              onConfirm={() => onActivate(row)}
            >
              <Button variant="text">发布</Button>
            </Popconfirm>
          )}
          {canWrite && row.status === 'active' && (
            <Popconfirm
              content="停用后不可被新租户引用，已绑定租户不受影响。确认停用？"
              onConfirm={() => onDisable(row)}
            >
              <Button variant="text">停用</Button>
            </Popconfirm>
          )}
          {canWrite && (
            <Popconfirm
              content="删除后套餐编码可被新套餐复用。此操作不可撤销，确认删除？"
              theme="danger"
              onConfirm={() => onDelete(row)}
            >
              <Button variant="text" theme="danger">
                删除
              </Button>
            </Popconfirm>
          )}
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
