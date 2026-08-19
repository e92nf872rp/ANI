import type { ReactNode } from 'react'

export interface BossPageHeaderProps {
  title: string
  description?: string
  extra?: ReactNode
}

export function BossPageHeader({ title, description, extra }: BossPageHeaderProps) {
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'flex-start',
        gap: 16,
      }}
    >
      <div>
        <h2 style={{ margin: 0, fontSize: 20, fontWeight: 600, lineHeight: '28px' }}>{title}</h2>
        {description && (
          <p
            style={{
              margin: '4px 0 0 0',
              color: 'var(--td-text-color-secondary)',
              fontSize: 14,
              lineHeight: '22px',
            }}
          >
            {description}
          </p>
        )}
      </div>
      {extra && <div style={{ flexShrink: 0 }}>{extra}</div>}
    </div>
  )
}
