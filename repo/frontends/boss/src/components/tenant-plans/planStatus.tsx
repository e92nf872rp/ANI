import { Tag } from 'tdesign-react'

export type PlanStatus = 'draft' | 'active' | 'disabled'

const STATUS_LABEL: Record<PlanStatus, string> = {
  draft: '草稿',
  active: '启用',
  disabled: '停用',
}

const STATUS_THEME: Record<PlanStatus, 'default' | 'success' | 'danger'> = {
  draft: 'default',
  active: 'success',
  disabled: 'danger',
}

export function PlanStatusTag({ status }: { status: PlanStatus }) {
  return (
    <Tag theme={STATUS_THEME[status]} variant="light">
      {STATUS_LABEL[status]}
    </Tag>
  )
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('zh-CN', { hour12: false })
}
