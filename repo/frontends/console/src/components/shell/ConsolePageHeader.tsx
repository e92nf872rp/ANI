import type { ReactNode } from 'react'
import { Space } from 'tdesign-react'

export interface ConsolePageHeaderProps {
  /** Primary page title. */
  title: ReactNode
  /** Optional subtitle rendered below the title. */
  subtitle?: ReactNode
  /** Optional action area rendered on the right side. */
  actions?: ReactNode
  /** Extra metadata row rendered between title and actions. */
  extra?: ReactNode
}

/**
 * ConsolePageHeader renders the page header with title, subtitle, and an
 * actions slot aligned to the right. It is meant to be used as the first
 * child of `ConsolePage`.
 */
export function ConsolePageHeader({ title, subtitle, actions, extra }: ConsolePageHeaderProps) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: 16,
        flexWrap: 'wrap',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
        <h2 style={{ margin: 0, fontSize: 20, fontWeight: 600, lineHeight: 1.4 }}>{title}</h2>
        {subtitle && (
          <span style={{ color: 'var(--td-text-color-secondary)', fontSize: 13, lineHeight: 1.5 }}>
            {subtitle}
          </span>
        )}
        {extra && <div style={{ marginTop: 4 }}>{extra}</div>}
      </div>
      {actions && (
        <Space>
          {actions}
        </Space>
      )}
    </div>
  )
}

export default ConsolePageHeader
