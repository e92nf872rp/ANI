import type { CSSProperties, ReactNode } from 'react'

export interface ConsolePageProps {
  /** Page content rendered inside the shell. */
  children: ReactNode
  /** Optional inline style override for the page container. */
  style?: CSSProperties
}

/**
 * ConsolePage is the page-level shell container for Console routes.
 *
 * It wraps page content with vertical spacing and assumes the global
 * Layout (Header + Aside + Content) is already provided by `__root.tsx`.
 * ConsolePage only owns the in-page area; it does not re-render the
 * application sidebar or header.
 */
export function ConsolePage({ children, style }: ConsolePageProps) {
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 16,
        ...style,
      }}
    >
      {children}
    </div>
  )
}

export default ConsolePage
