import type { CSSProperties, ReactNode } from 'react'
import { Card } from 'tdesign-react'

export interface ConsoleContentCardProps {
  /** Card title shown in the header area. */
  title?: ReactNode
  /** Optional actions rendered on the right of the card header. */
  actions?: ReactNode
  /** Card body content. */
  children: ReactNode
  /** Optional inline style override. */
  style?: CSSProperties
  /** Whether the card body should have no padding. */
  bodyNoPadding?: boolean
}

/**
 * ConsoleContentCard is the standard content container for Console pages.
 * It wraps TDesign `Card` with consistent spacing and an optional header
 * actions slot. Use it inside `ConsolePage` to hold tables, charts, or
 * other content blocks.
 */
export function ConsoleContentCard({
  title,
  actions,
  children,
  style,
  bodyNoPadding = false,
}: ConsoleContentCardProps) {
  return (
    <Card
      title={title}
      actions={actions}
      style={style}
      bodyStyle={bodyNoPadding ? { padding: 0 } : undefined}
      bordered
    >
      {children}
    </Card>
  )
}

export default ConsoleContentCard
