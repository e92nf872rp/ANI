import type { ReactNode } from 'react'

export interface BossPageProps {
  children: ReactNode
}

export function BossPage({ children }: BossPageProps) {
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 16,
        maxWidth: 1200,
      }}
    >
      {children}
    </div>
  )
}
