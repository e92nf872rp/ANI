import type { ReactNode } from 'react'
import { Card } from 'tdesign-react'

export interface BossContentCardProps {
  children: ReactNode
  title?: string
}

export function BossContentCard({ children, title }: BossContentCardProps) {
  return (
    <Card title={title} bordered>
      {children}
    </Card>
  )
}
